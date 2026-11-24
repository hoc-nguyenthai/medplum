# 3. Danh mục chức năng

Chức năng của **chính `fhir-worker`** (không có route; kích hoạt bởi `AsyncJob` trong CSDL). Phần nhận yêu cầu tạo job ở phía `fhir-server` (F-17 export, F-18 reindex, F-20 job nền) thuộc tài liệu của API.

| Mã | Chức năng | Tác nhân |
|---|---|---|
| [F-01](#f-01) | Nhận và chạy job nền (vòng nhận job, lease, gia hạn, tiếp quản) | A4, A5 |
| [F-02](#f-02) | Mở chữ ký quyền của job và chạy bằng đúng quyền, project của người gọi | A4 |
| [F-03](#f-03) | Chạy bulk export (job `export`) | A1 |
| [F-04](#f-04) | Chạy `$reindex` cả loại theo project (job `reindex`) | A2 |
| [F-05](#f-05) | Chạy batch async có checkpoint (job `batch`) | A1 |
| [F-06](#f-06) | Nhận biết job bị hủy và dừng | A1 |
| [F-07](#f-07) | Dọn kết quả hết hạn (vòng bảo trì) | A3 |
| [F-08](#f-08) | Lưu kết quả job (kho Binary hoặc CSDL) | A1 |
| [F-09](#f-09) | Phát sự kiện ghi resource do batch async tạo | A6 |
| [F-10](#f-10) | Khởi động, probe và tắt êm | A3 |

<a id="f-01"></a>
### F-01. Nhận và chạy job nền

| Mục | Nội dung |
|---|---|
| Mục đích | Tìm và chạy các `AsyncJob` mà API đã tạo nhưng không chạy, đúng một lần dù có nhiều worker. |
| Tác nhân | A4 (tạo job), A5 (worker khác tranh job), A3 (cấu hình). |
| Tiền điều kiện | Schema `fhir` đã migrate; cấu hình hợp lệ; `FHIR_JOB_MAINTENANCE_INTERVAL` > 0. |
| Cách gọi | Không có route. Vòng nền chạy mỗi `FHIR_JOB_MAINTENANCE_INTERVAL` (mặc định 5 giây). |
| Cấu hình | `FHIR_JOB_CONCURRENCY` (2), `FHIR_JOB_LEASE` (5 phút), `FHIR_JOB_MAINTENANCE_INTERVAL` (5 giây), `FHIR_JOB_RECOVERY_GRACE` (2 giây). |

**Luồng chính:**

1. Mỗi chu kỳ, tìm `AsyncJob` có `status=accepted` theo `_lastUpdated` (trang 100). Bỏ qua job đang có lease còn hiệu lực, và job có `requestTime` trẻ hơn `FHIR_JOB_RECOVERY_GRACE`.
2. Nộp job vào bộ điều phối trong tiến trình (tối đa `FHIR_JOB_CONCURRENCY` job chạy đồng thời, phần còn lại chờ chỗ).
3. Khi tới lượt: đọc lại job; nếu không còn `accepted` hoặc đã có lease thì bỏ qua; nếu không thì **giành lease** bằng cập nhật có điều kiện `If-Match` (đặt `transactionTime` = hiện tại + `FHIR_JOB_LEASE`); chỉ một worker thắng.
4. Trong lúc chạy, gia hạn lease mỗi `FHIR_JOB_LEASE`/3 và kiểm job có bị hủy không (F-06).
5. Chạy hàm của loại job (F-03, F-04, F-05) với quyền ở F-02; ghi kết quả hoặc lỗi lên `AsyncJob` bằng ngữ cảnh độc lập (xem BR-05).

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Hai worker cùng giành | một thắng, bên thua im lặng bỏ qua (`412` không log) |
| Loại job không biết | `status=error`, `loại job không tồn tại: <loại>` |
| Job lỗi hoặc panic | `status=error` kèm OperationOutcome (`exception`), log ERROR `job lỗi`; worker không sập |
| Worker chết giữa chừng | lease hết hạn, worker khác nhận lại ở chu kỳ sau |

**Tác dụng phụ:** cập nhật `AsyncJob` (lease, trạng thái, kết quả). **Luật:** BR-01..BR-08.

<a id="f-02"></a>
### F-02. Mở chữ ký quyền của job

| Mục | Nội dung |
|---|---|
| Mục đích | Bảo đảm job chạy đúng quyền và đúng tổ chức của người đã tạo nó, không rộng hơn. |
| Tiền điều kiện | `FHIR_JWT_ISSUER` đặt (fhir-server bật JWT) và khóa ký job (`FHIR_JOB_SIGNING_KEY`, hoặc dev HMAC suy từ `FHIR_JWT_HMAC_SECRET`). Không bật JWT thì chức năng này không chạy (job không có chữ ký). |

**Luồng chính:**

1. Lấy chuỗi ký ở extension `JobSealURL` của `AsyncJob`.
2. Kiểm HMAC ràng buộc `jobID`, `project` (của AsyncJob), `request` và ảnh chụp quyền.
3. Dựng lại Principal: project của AsyncJob, grants theo loại và AccessPolicy đã ký, vai trò đọc, thêm quyền **ghi** (không đọc) trên `Binary` để lưu và dọn file kết quả.
4. Job loại có phạm vi (`export`, `batch`, `reindex`) chạy bằng kho đã tách project với Principal đó.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Thiếu hoặc sai chữ ký (khóa lệch, job bị sửa, chữ ký của job khác) | `status=error`, `job thiếu hoặc sai chữ ký quyền`, không chạy, không để lại kết quả |
| Ảnh chụp quyền hỏng | `job có ảnh chụp quyền hỏng` |
| Ảnh chụp quyền phiên bản cũ | `job có ảnh chụp quyền phiên bản cũ, hãy chạy lại` |
| Chính sách hỏng | `job có chính sách hỏng` |

**Luật:** BR-09..BR-14.

<a id="f-03"></a>
### F-03. Chạy bulk export (job `export`)

| Mục | Nội dung |
|---|---|
| Mục đích | Xuất resource ra các tệp ndjson, mỗi loại một hoặc nhiều tệp. |
| Tác nhân | A1 (tạo ở API bằng `GET [base]/$export`, `Patient/$export` hoặc `Group/{id}/$export`). |
| Tham số trong `request` | `_level` (`system`, `patient`, `group`), `_type`, `_typeFilter`, `_since`, `_group`. |

**Luồng chính:**

1. Xác định loại cần xuất: có `_type` thì theo đó (mức patient và group chỉ giữ Patient và loại thuộc compartment Patient); không thì mọi loại trừ `AsyncJob` và `Binary` (mức patient và group: Patient và compartment). Mức group: đọc lại Group, lấy thành viên Patient đang hoạt động.
2. Với mỗi loại: tìm theo `_lastUpdated` tăng dần, trang 1000, áp `_typeFilter` (OR giữa các điều kiện cùng loại) và `_since` (`_lastUpdated ge`); mức patient loại bỏ trùng giữa các tham số compartment.
3. Ghi dòng ndjson vào bộ đệm; đủ 8 MiB thì tạo một `Binary` (`application/fhir+ndjson`); hết loại thì tạo nốt.
4. Kết quả: `Parameters` có mỗi tệp một `output` gồm `type`, `url` (`<FHIR_BASE_URL>/Binary/<id>`), `count`.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Loại người gọi không được đọc và **không** chỉ định `_type` | loại đó bị bỏ qua, job vẫn xong |
| Loại không được đọc và có `_type` | job `error` (`tìm <Loại>: ...`) |
| Lỗi hoặc bị hủy giữa chừng | các tệp đã ghi bị xóa, không để lại file mồ côi (trừ khi worker chết đột ngột) |

**Luật:** BR-15..BR-18, BR-11. **Hậu điều kiện:** `AsyncJob` `completed` với `output`, hoặc `error`.

<a id="f-04"></a>
### F-04. Chạy `$reindex` cả loại theo project (job `reindex`)

| Mục | Nội dung |
|---|---|
| Mục đích | Lập lại chỉ mục tìm kiếm của mọi resource của một loại, chỉ trong tổ chức của người gọi. |
| Tác nhân | A2 (token ADMIN ở API, `POST [base]/{Loại}/$reindex`). |

**Luồng chính:** kiểm quyền cập nhật loại đó và token không có AccessPolicy; liệt kê id resource của project (lô 200) bằng kho bên dưới; với mỗi resource kiểm sở hữu theo project rồi lập lại chỉ mục; kết quả `count`.
**Thay thế / lỗi:** token có AccessPolicy: job `error` (`Reindex không dùng được với chính sách truy cập`, `403`); không JWT: job chạy trên cả kho (không tách project). **Luật:** BR-13.

<a id="f-05"></a>
### F-05. Chạy batch async có checkpoint (job `batch`)

| Mục | Nội dung |
|---|---|
| Mục đích | Chạy một Bundle `batch` lớn ở nền, chịu được dừng giữa chừng, cho kết quả giống batch đồng bộ. |
| Tác nhân | A1 (gửi Bundle `batch` với `Prefer: respond-async`; `FHIR_ASYNC_BATCH` bật ở API). |

**Luồng chính:**

1. Đọc Bundle đầu vào từ Binary (đã lưu lúc nhận); đọc checkpoint nếu có (`AsyncJob` extension `job-state`): `position`, `chunks`, `ids`.
2. Lần đầu: cấp sẵn id cho mọi POST không điều kiện (không `If-None-Exist`), lưu checkpoint.
3. Chạy từng entry từ `position` qua cùng bộ xử lý như batch đồng bộ, với quyền người gọi (kho tách project, kiểm dữ liệu, kho Binary). Entry POST có id cấp sẵn mà resource đã tồn tại thì trả lại resource đó (không tạo bản thứ hai).
4. Cứ `FHIR_BATCH_CHECKPOINT_ENTRIES` entry (mặc định 10) hoặc 5 giây thì ghi một đoạn kết quả thành Binary và lưu checkpoint.
5. Hết entry: ghép các đoạn thành Bundle `batch-response`, ghi Binary, xóa Bundle đầu vào và đoạn tạm; `output.results` = `Binary/<id>`.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Tắt máy hoặc bị gián đoạn | lưu checkpoint rồi dừng, **giữ** Binary tạm; worker khác chạy tiếp từ `position` |
| Bị hủy | dừng, **dọn** Binary tạm và đầu vào; các resource đã ghi trước đó không hoàn tác |
| Checkpoint hỏng hoặc lệch số entry | `trạng thái checkpoint của job hỏng`, job `error` |
| Lỗi hạ tầng khi ghi kết quả | job `error`, dọn Binary tạm |

**Luật:** BR-19..BR-21.

<a id="f-06"></a>
### F-06. Nhận biết job bị hủy và dừng

**Mục đích:** dừng job mà người dùng đã hủy ở API. **Cách:** API đánh dấu `AsyncJob.status=cancelled` (chỉ khi job còn `accepted`); worker đang chạy phát hiện ở heartbeat (đọc `AsyncJob` mỗi `FHIR_JOB_LEASE`/3) hoặc, với batch, khi lưu checkpoint (`ErrJobCancelled`); rồi hủy ngữ cảnh chạy, bỏ kết quả (xóa Binary kết quả đã ghi) và không ghi `completed`. **Luật:** BR-22, BR-23.

<a id="f-07"></a>
### F-07. Dọn kết quả hết hạn

**Mục đích:** giải phóng dung lượng kết quả export và batch async. **Cách:** vòng bảo trì (cùng chu kỳ nhận job) tìm `AsyncJob` `completed`, `error`, `cancelled` có `_lastUpdated` cũ hơn `FHIR_EXPORT_RETENTION` (mặc định 24 giờ; âm: không bao giờ xóa), loại `export?`, `batch?` và `reindex?` (job `reindex` chỉ có dòng, không có Binary); xóa các Binary của job rồi xóa job. **Tác dụng phụ:** xóa vĩnh viễn (Expunge) dòng, lịch sử và tệp ở kho khi repository cho phép. **Luật:** BR-24, BR-25.

<a id="f-08"></a>
### F-08. Lưu kết quả job

**Mục đích:** ghi file kết quả, Bundle đầu vào và đoạn tạm của batch. **Cách:** có `FHIR_BINARY_STORAGE`: nội dung ra kho (`file:` hoặc `s3:`), không trần base64 (file export tới 8 MiB), dòng Binary trong CSDL chỉ giữ metadata; không có kho: base64 trong CSDL. Kho và `FHIR_BASE_URL` phải trùng API để `GET Binary/{id}` ở API đọc được kết quả. **Luật:** BR-26, BR-27.

<a id="f-09"></a>
### F-09. Phát sự kiện ghi resource do batch async tạo

**Mục đích:** để `fhir-notifier` và `fhir-downloader` thấy các resource do batch ghi (như khi API tự ghi). **Cách:** khi `FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD` bật (cùng điều kiện với `fhir-server`), kho của worker phát `fhir.resource.changed` lên Redis sau mỗi lần ghi; chỉ phát, không nghe. Cần `BROKER_DRIVER=redis` và `REDIS_URL`. **Luật:** BR-28.

<a id="f-10"></a>
### F-10. Khởi động, probe và tắt êm

| Mục | Nội dung |
|---|---|
| Mục đích | Chỉ chạy khi cấu hình đúng và schema đã migrate; báo sống và sẵn sàng; tắt không làm hỏng job. |
| Route | `GET /healthz`, `/health/live`, `/health/startup` (luôn `200`), `GET /health/ready` (`200` hoặc `503`). |

**Khởi động:** đọc cấu hình, gom mọi lỗi và từ chối; mở DB; kiểm `Pending == 0` (còn migration thì từ chối: `schema chưa được migrate`); dựng kho Binary, broker (nếu phải phát sự kiện), mở chữ ký job (nếu JWT), dựng bộ chạy job.
**Probe:** `/health/ready` kiểm database trong `READINESS_TIMEOUT`; `503` khi đang tắt (`checks.draining`) hoặc DB hỏng.
**Tắt êm:** `SIGTERM` → `/health/ready` `503` → chờ `SHUTDOWN_DRAIN_DELAY` → hủy ngữ cảnh chạy: job dở **nhả lease** (xóa `transactionTime`, không đánh dấu lỗi), batch lưu checkpoint → đóng broker, kho, DB → thoát mã 0.
**Luật:** BR-05, BR-29..BR-33.
