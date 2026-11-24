# 4. Kho khác ngoài SQL, bộ nhớ và bí mật

## 4.1 Kho Binary (`FHIR_BINARY_STORAGE`)

| Khóa / đường dẫn | Giá trị | Vòng đời | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| `binary/<id>/<versionId>` (`file:<thư mục>` hoặc `s3:<bucket>/<prefix>binary/...`) | nội dung thô của một phiên bản `Binary`: file export (ndjson), Bundle đầu vào, đoạn và kết quả của batch (JSON) | tạo cùng `Binary` (CSDL trước, kho sau); xóa bởi Expunge (mọi phiên bản) khi job hết hạn, bị hủy hoặc lỗi; `sweep-binary` dọn khóa không còn dòng | `fhir-worker` (job), `fhir-server` (Binary thường, Bundle đầu vào của batch) | `fhir-server` (`GET Binary/{id}`), `fhir-worker` (đọc đầu vào và đoạn qua `BinaryContent`, giới hạn 64 MiB mỗi lần) | `GET Binary/{id}` `404`; batch đang chạy lỗi `đọc kết quả đoạn` hoặc `đọc Bundle đầu vào` |

- **Bắt buộc ở staging/production?** Không bắt buộc (không có kho thì kết quả base64 trong CSDL), nhưng **phải trùng `fhir-server`**: worker và API ghi và đọc cùng kho.
- Kho `file:` cần thư mục ghi được bởi UID `65532` (image distroless không root) và phải **dùng chung** (volume) với `fhir-server`; production dùng `s3:`.
- Kết quả export là **dữ liệu lâm sàng (S3)** ở dạng ndjson nguyên văn; quyền đọc dựa vào `meta.project` ở dòng `Binary` (không có `securityContext`), nên ai đọc được kho S3 trực tiếp đọc được toàn bộ file export: giới hạn quyền kho theo nguyên tắc tối thiểu.

## 4.2 Redis: chỉ phát sự kiện

| Khóa | Giá trị | TTL | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| stream `trivita:events:fhir.resource.changed` (tiền tố `trivita:events` + `:` + topic) | mỗi entry một trường `data` = JSON envelope (id, loại, thời điểm, payload là `ChangeEvent`: `interaction`, `resourceType`, `id`, **nội dung JSON của resource**) | **không có** (không `MAXLEN`, không hết hạn) | `fhir-worker` (`XADD`, sau commit, cho resource do batch async ghi); `fhir-server` cho resource nó ghi | `fhir-notifier`, `fhir-downloader`, `fhir-server` (mỗi bên một consumer group) | sự kiện của batch không tới service nghe; **dữ liệu không mất** (resource đã ở CSDL) |

- Worker **chỉ `XADD`**: không tạo consumer group, không đọc, không dùng Pub/Sub; mất Redis lúc ghi không làm lần ghi resource thất bại (lỗi phát chỉ được log).
- **Dữ liệu lâm sàng (S3) nằm trong Redis dạng rõ**, không giới hạn độ dài và thời gian; Binary được bọc **dưới** lớp phát sự kiện nên sự kiện của Binary không mang `data`. Cần quy định `MAXLEN`/retention và mã hóa Redis ở hạ tầng (chưa có).
- **Bắt buộc?** Chỉ khi `fhir-server` bật `FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD` (cấu hình từ chối `BROKER_DRIVER` khác `redis` khi đó). Không có Redis khi hai cờ tắt: worker không mở kết nối nào.

## 4.3 Bộ nhớ của tiến trình

| Thứ | Nội dung | Mất khi tiến trình chết? | Hệ quả |
|---|---|---|---|
| Bộ điều phối job (`workflow.Inline`) | goroutine mỗi job đang chờ hoặc chạy, giới hạn `FHIR_JOB_CONCURRENCY` | có | không mất việc: job nằm ở `AsyncJob` trong CSDL, được nhận lại ở chu kỳ sau |
| Bản đồ hủy (`cancels`) | hàm hủy ngữ cảnh của từng job đang chạy ở tiến trình này | có | hủy xuyên tiến trình chỉ qua heartbeat (xem business BR-22) |
| Bộ đệm export | tối đa khoảng 8 MiB cho mỗi tệp đang ghi và một trang 1.000 resource | có | RSS đỉnh tăng theo số export đồng thời (đo S4: 70 MB lên 295 MB một export, 395 MB bốn export, trên `fhir-server`) |
| Kết quả batch | worker **đọc lại mọi đoạn rồi ghép Bundle `batch-response` trong bộ nhớ** trước khi ghi Binary kết quả | có | bộ nhớ đỉnh theo tổng kích thước kết quả của batch (suy từ mã, chưa đo) |

## 4.4 Bí mật: cái nào lưu thế nào

| Bí mật | Nơi | Dạng lưu | Có trong log? | Ghi chú |
|---|---|---|---|---|
| `FHIR_JOB_SIGNING_KEY` (hoặc dev: suy từ `FHIR_JWT_HMAC_SECRET` bằng SHA-256 `fhir-job-seal|<secret>`) | biến môi trường | không lưu ở CSDL; bản rõ trong môi trường tiến trình | không (đã grep `slog` trong `apps/fhir-worker`: không có dòng log nào in giá trị cấu hình) | S4. Dùng chung với `fhir-server`; xoay khóa làm job đang chờ bị từ chối |
| Chữ ký quyền job (HMAC + payload) | `AsyncJob.extension[job-seal]` (CSDL, kể cả `fhir_history`) | payload **bản rõ** (base64url, không mã hóa) + HMAC | không | S2: id người gọi, grants, AccessPolicy; không phải bí mật xác thực nhưng nằm lại vĩnh viễn trong lịch sử (mục 3.4) |
| Mật khẩu CSDL (`DATABASE_URL`) | biến môi trường | bản rõ trong URL | không (không log URL; `DATABASE_ECHO` log câu SQL kèm tham số, chỉ gỡ lỗi) | S4 |
| Khóa truy cập S3 (`FHIR_S3_ACCESS_KEY_ID`, `FHIR_S3_SECRET_ACCESS_KEY`, `FHIR_S3_SESSION_TOKEN`) | biến môi trường | bản rõ | không | S4 |
| Mật khẩu Redis (nếu có trong `REDIS_URL`) | biến môi trường | bản rõ trong URL | không | S4 |

Worker **không lưu và không đọc** bí mật xác thực của người dùng (mật khẩu, MFA, refresh token): đó là schema `identity` của `auth`, mà role `fhir_app` không đọc được.

## 4.5 SQLite (dev)

`DATABASE_URL=sqlite://<tệp>` (mặc định `sqlite://./fhir.db`): một tệp dùng chung giữa `fhir-server` và `fhir-worker` chỉ phù hợp khi chạy cùng máy; không có schema, cột `content` là text, không có role; **cấm ở staging/production**. Các test của worker dùng tệp SQLite tạm để mô phỏng API và worker trên cùng cơ sở dữ liệu.
