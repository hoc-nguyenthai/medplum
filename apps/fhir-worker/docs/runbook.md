# fhir-worker: vận hành

## Bật service thay cho bộ chạy job trong API

Thứ tự (không tắt trước khi bật; hai bên cùng chạy vẫn an toàn nhờ lease nhưng API còn gánh tải):

1. Chạy `fhir-server migrate up` (schema `fhir` do `fhir-server` sở hữu).
2. Dựng `fhir-worker` với **cùng** `DATABASE_URL` (schema `fhir`), `FHIR_BASE_URL`, `FHIR_BINARY_STORAGE` (và `FHIR_S3_*`), `FHIR_JOB_SIGNING_KEY` và `FHIR_JWT_ISSUER` nếu API bật JWT, các tuỳ chọn kiểm dữ liệu (`FHIR_SKIP_VALIDATION`, `FHIR_SKIP_CONSTRAINTS`, `FHIR_UPDATE_AS_CREATE`, `FHIR_BASE64_BINARY_MAX_BYTES`), và `FHIR_SUBSCRIPTIONS`/`FHIR_AUTO_DOWNLOAD` kèm `BROKER_DRIVER=redis`, `REDIS_URL` nếu API bật. Kiểm `/health/ready` trả 200 và log "đang nhận job nền".
3. Ở `fhir-server` đặt `FHIR_JOB_EXECUTION=false`, rồi rolling restart. Từ lúc này API chỉ tạo job (`202`) và không chạy; job đang chạy dở trong API bị tiếp quản bởi `fhir-worker` sau khi lease hết hạn (`FHIR_JOB_LEASE`, mặc định 5 phút).
4. Thử một `$export` nhỏ: `202` rồi `200` với output trong vài giây.

Quay lại: đặt `FHIR_JOB_EXECUTION=true` ở `fhir-server`, rolling restart, rồi dừng service này (job đang chạy nhả lease khi tắt êm).

## Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Từ chối khởi động: `schema chưa được migrate` | chạy trước `fhir-server migrate up` | chạy migrate rồi khởi động lại |
| Từ chối khởi động: `FHIR_JOB_SIGNING_KEY: bắt buộc` | API bật JWT (`FHIR_JWT_ISSUER`) mà worker thiếu khóa ký job | đặt đúng khóa của `fhir-server` |
| Job ở `accepted` mãi (poll `202`) | không có worker chạy, hoặc API `FHIR_JOB_EXECUTION=false` mà worker chết, hoặc worker không cùng DB/schema | kiểm worker `/health/ready`, log, `DATABASE_URL`; xem `AsyncJob.status` |
| Job lỗi ngay, log "chữ ký" | khóa `FHIR_JOB_SIGNING_KEY` lệch `fhir-server` (kể cả khi vừa xoay khóa) | đồng bộ khóa; tạo lại job |
| Export thiếu dữ liệu hoặc chứa project khác | không xảy ra với job có chữ ký; nếu API token tĩnh (không JWT) thì job không tách project | kiểm `FHIR_JWT_ISSUER` ở worker khớp API |
| URL kết quả sai miền | `FHIR_BASE_URL` của worker lệch API | đặt trùng |
| Batch async hành xử khác API (validation, update-as-create) | `FHIR_SKIP_*`, `FHIR_UPDATE_AS_CREATE` lệch API | đặt trùng |
| `GET Binary/{id}` kết quả 404 sau khi xong | kho `FHIR_BINARY_STORAGE` của worker khác của API | đặt trùng |
| Kết quả không bị xóa sau `FHIR_EXPORT_RETENTION` | vòng bảo trì không chạy (`FHIR_JOB_MAINTENANCE_INTERVAL`) hoặc retention âm | kiểm cấu hình; xóa dùng Expunge |

## Định cỡ

`FHIR_JOB_CONCURRENCY` là số job chạy đồng thời mỗi instance (mặc định 2); mỗi export giữ khoảng 100 MB bộ nhớ đỉnh ở cỡ 50.000 resource (đo S4: RSS 70 MB lên 295 MB một export, 395 MB bốn export đồng thời; số này đo trên tiến trình `fhir-server` khi chạy job, chưa đo riêng worker). Scale bằng thêm replica (lease chia việc) và đặt requests/limits bộ nhớ theo `FHIR_JOB_CONCURRENCY` x 150 MB làm điểm khởi đầu. Tổng kết nối DB của mọi replica của mọi service phải nhỏ hơn `max_connections` (mỗi service mặc định 20, D-7). Độ trễ nhận job ≈ `FHIR_JOB_MAINTENANCE_INTERVAL` + `FHIR_JOB_RECOVERY_GRACE`.
