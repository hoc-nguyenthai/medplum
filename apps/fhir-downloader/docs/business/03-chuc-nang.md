# 3. Danh mục chức năng

Chức năng của **chính `fhir-downloader`** (không có route; kích hoạt bởi sự kiện trên Redis). Phần cờ bật và cấu hình ở phía API thuộc tài liệu của `fhir-server` (F-28).

| Mã | Chức năng | Tác nhân |
|---|---|---|
| [F-01](#f-01) | Nhận sự kiện ghi resource | A2, A5 |
| [F-02](#f-02) | Chọn URL cần tải | A1 |
| [F-03](#f-03) | Tải tệp từ máy chủ ngoài | A3 |
| [F-04](#f-04) | Tạo Binary đúng project và ghi nội dung vào kho | A1 |
| [F-05](#f-05) | Đổi `url` attachment thành `Binary/<id>` | A1 |
| [F-06](#f-06) | Xử lý lỗi tạm, lỗi vĩnh viễn và dead-letter | A4 |
| [F-07](#f-07) | Khởi động, probe và tắt êm | A4 |

<a id="f-01"></a>
### F-01. Nhận sự kiện ghi resource

| Mục | Nội dung |
|---|---|
| Mục đích | Nhận mọi sự kiện ghi resource của `fhir-server` và chia việc giữa các replica. |
| Tiền điều kiện | Redis (`REDIS_URL`); nhóm `BROKER_GROUP` (mặc định `fhir-downloader`, khác `trivita`); schema đã migrate. |
| Cách gọi | Không có route. Redis Streams `trivita:events:fhir.resource.changed`, đọc bằng consumer group. |

**Luồng chính:** nhóm được tạo từ đầu stream khi khởi động (nên nhận cả sự kiện ghi lúc service chưa chạy); mỗi message giao cho đúng một replica trong nhóm; xử lý xong (thành công hoặc lỗi vĩnh viễn) thì ACK.
**Thay thế / lỗi:** handler trả lỗi (lỗi tạm) thì không ACK, message ở PEL và được nhận lại sau `ClaimIdle` (30 giây); quá 5 lần giao thì chuyển sang `...:dead` và ACK (F-06); vỏ message (envelope) hỏng thì bị ACK và bỏ qua; payload không giải mã được thành `ChangeEvent` thì trả lỗi (thử lại rồi dead-letter), còn nội dung resource không giải mã được thì bị bỏ qua.
**Luật:** BR-01, BR-12, BR-14, BR-15.

<a id="f-02"></a>
### F-02. Chọn URL cần tải

**Mục đích:** chỉ tải đúng thứ cần tải. **Luồng chính:** bỏ qua sự kiện `delete`, loại `Binary`, `AsyncJob`, `AuditEvent`, `Subscription`, sự kiện không có nội dung hoặc id, và nội dung không giải mã được thành resource FHIR; duyệt toàn bộ cây resource (sâu tối đa 64 tầng) lấy mọi `Attachment`; giữ `url` là `http` hoặc `https` tuyệt đối có host, **không** bắt đầu bằng `FHIR_BASE_URL/` hay địa chỉ `/storage` (đã là của hệ thống), không khớp `FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES` (thắng danh sách cho phép), và nếu `FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES` không rỗng thì phải khớp một tiền tố; mỗi URL chỉ tải một lần dù xuất hiện nhiều lần trong một resource. **Luật:** BR-02, BR-03.

<a id="f-03"></a>
### F-03. Tải tệp từ máy chủ ngoài

| Mục | Nội dung |
|---|---|
| Mục đích | Lấy nội dung tệp một cách an toàn. |
| Cấu hình | `FHIR_AUTO_DOWNLOAD_TIMEOUT` (30 giây), `FHIR_AUTO_DOWNLOAD_MAX_BYTES` (0: theo `FHIR_STORAGE_MAX_UPLOAD_BYTES`), `FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE` (chỉ dev). |

**Luồng chính:** đọc lại resource nguồn (mất hoặc đã xóa thì bỏ; `url` không còn thì bỏ); kiểm URL qua `safehttp` (bị chặn thì bỏ); `GET`; mã `>= 400` hoặc lỗi mạng là **lỗi tạm**; `Content-Length` lớn hơn trần thì bỏ; xác định `Content-Type` (phản hồi, rồi `attachment.contentType`, rồi `application/octet-stream`); loại bị cấm thì bỏ.
**Thay thế / lỗi:** bị chặn SSRF (kể cả chuyển hướng hoặc DNS trỏ nội bộ lúc kết nối): bỏ, log chỉ ghi host; `url.Error` được rút gọn để token ký trong query không vào log. **Luật:** BR-04..BR-08.

<a id="f-04"></a>
### F-04. Tạo Binary và ghi nội dung

**Luồng chính:** tạo `Binary` (id mới, `contentType`, `securityContext` = `<Loại>/<id>` của resource nguồn, `meta.project` của resource nguồn nếu có) qua kho gốc (không phát sự kiện), rồi ghi nội dung theo luồng vào kho `binary/<id>/<versionId>` (có trần `MaxBytes`). **Thay thế / lỗi:** lỗi kho: dọn Binary vừa tạo (và khóa nếu đã ghi) rồi lỗi tạm; vượt trần khi ghi: dọn và bỏ (vĩnh viễn). **Luật:** BR-09, BR-10.

<a id="f-05"></a>
### F-05. Đổi `url` thành `Binary/<id>`

**Luồng chính:** đọc lại resource nguồn lần nữa; đổi `url` của **mọi** attachment trùng thành `Binary/<id>` (điền `contentType` nếu trống); cập nhật bằng `If-Match` phiên bản vừa đọc, qua kho **phát sự kiện** (nên Subscription thấy một `update`). **Thay thế / lỗi:** không còn attachment trùng: dọn Binary và bỏ; thua ghi song song (`412`): dọn rồi lỗi tạm để thử lại; resource đã mất: dọn và bỏ. **Luật:** BR-11, BR-12.

<a id="f-06"></a>
### F-06. Xử lý lỗi

| Loại | Gồm | Xử lý |
|---|---|---|
| Vĩnh viễn | URL bị chặn, quá trần, loại nội dung cấm, resource đã mất hoặc đã đổi URL, không còn attachment trùng | ghi log (host, lý do), dọn, ACK; resource giữ nguyên |
| Tạm | HTTP `4xx` hoặc `5xx`, lỗi mạng, lỗi ghi kho hoặc CSDL, thua ghi song song | dọn, trả lỗi, không ACK: nhận lại sau `ClaimIdle` (30 giây), tối đa 5 lần |
| Hết lượt | giao lần thứ 5 vẫn lỗi tạm | chuyển vào `trivita:events:fhir.resource.changed:dead` rồi ACK |

Một sự kiện có nhiều URL xử lý tuần tự: một URL lỗi tạm làm cả sự kiện được thử lại; URL đã đổi ở lần trước được bỏ qua (idempotent). **Luật:** BR-11..BR-13.

<a id="f-07"></a>
### F-07. Khởi động, probe và tắt êm

**Khởi động:** đọc cấu hình, gom mọi lỗi và từ chối; mở DB; kiểm `Pending == 0`; mở kho; nối Redis; đăng ký handler; bắt đầu nghe. **Probe:** `/health/ready` kiểm database và Redis trong `READINESS_TIMEOUT`, `503` khi đang tắt hoặc hỏng; ba probe còn lại luôn `200`. **Tắt êm:** `SIGTERM` → ready `503` → chờ `SHUTDOWN_DRAIN_DELAY` → ngừng nhận, chờ việc đang tải → đóng Redis, kho, DB → thoát mã 0. **Luật:** BR-14..BR-17.
