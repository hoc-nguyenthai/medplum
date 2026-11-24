# fhir-downloader: cấu hình

Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_NAME` | `trivita-fhir-downloader` | tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt) |
| `BROKER_GROUP` | `fhir-downloader` | là consumer group của service này trên stream sự kiện. KHÁC group của fhir-server ("trivita") để cả hai cùng nhận MỌI sự kiện (cùng group thì chia nhau); nhiều replica của fhir-downloader dùng cùng group để chia việc. |
| `DATABASE_CONN_MAX_LIFETIME` | `30m0s` | tuổi tối đa của một kết nối trước khi được thay |
| `DATABASE_ECHO` | `false` | ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số). |
| `DATABASE_MAX_IDLE_CONNS` | `5` | số kết nối nhàn rỗi giữ lại |
| `DATABASE_MAX_OPEN_CONNS` | `20` | số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md). |
| `DATABASE_URL` | `sqlite://./fhir.db` | phải trỏ tới CÙNG schema `fhir` với fhir-server (đọc resource nguồn, ghi Binary, cập nhật attachment). fhir-downloader KHÔNG chạy migration (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động. |
| `ENVIRONMENT` | `local` | ENVIRONMENT: local \| development \| staging \| production |
| `FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES` | `(rỗng)` | (cách nhau bằng dấu phẩy, không rỗng) chỉ cho tải URL bắt đầu bằng một tiền tố trong danh sách; rỗng: mọi URL ngoài. |
| `FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE` | `false` | CHỈ cho dev: cho phép tải từ địa chỉ nội bộ (chống SSRF bị tắt). Cấm ở staging/production. |
| `FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES` | `(rỗng)` | (cách nhau bằng dấu phẩy) là các tiền tố URL KHÔNG bao giờ tải; thắng danh sách cho phép. Địa chỉ API và /storage luôn bị bỏ qua, không cần khai. |
| `FHIR_AUTO_DOWNLOAD_MAX_BYTES` | `0` | là trần kích thước một tệp tải về; 0: dùng FHIR_STORAGE_MAX_UPLOAD_BYTES. |
| `FHIR_AUTO_DOWNLOAD_TIMEOUT` | `30s` | là hạn của mỗi lần tải. |
| `FHIR_BASE_URL` | `http://localhost:8005/fhir/R4` | là địa chỉ công khai của API FHIR (FHIR_BASE_URL của fhir-server): URL bắt đầu bằng nó không bị tải (đã là của chính hệ thống). |
| `FHIR_BINARY_STORAGE` | `(rỗng)` | là kho nội dung tệp tải về (bắt buộc), PHẢI trùng với FHIR_BINARY_STORAGE của fhir-server vì Binary do service này ghi được fhir-server đọc: `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`. |
| `FHIR_S3_ACCESS_KEY_ID` | `(rỗng)` | bắt buộc khi dùng s3: |
| `FHIR_S3_ENDPOINT` | `(rỗng)` | kho tương thích S3 (MinIO, RustFS...), http(s)://host[:port]; trống: AWS |
| `FHIR_S3_REGION` | `(rỗng)` | S3 (BinaryStorage dạng `s3:`). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log. bắt buộc (vd eu-west-1) |
| `FHIR_S3_SECRET_ACCESS_KEY` | `(rỗng)` | bắt buộc khi dùng s3:; không có trong log |
| `FHIR_S3_SESSION_TOKEN` | `(rỗng)` | tùy chọn, cho khóa tạm |
| `FHIR_S3_SSE` | `(rỗng)` | "", "AES256" hoặc "aws:kms" |
| `FHIR_STORAGE_BASE_URL` | `(rỗng)` | (tùy chọn) là địa chỉ công khai của /storage nếu khác mặc định (BaseURL bỏ /fhir/R4, thêm /storage); URL bắt đầu bằng nó cũng không bị tải. |
| `FHIR_STORAGE_MAX_UPLOAD_BYTES` | `268435456` | là trần mặc định của một tệp tải về khi FHIR_AUTO_DOWNLOAD_MAX_BYTES là 0 (cùng biến với fhir-server). |
| `HTTP_ADDR` | `:8009` | địa chỉ nghe của các probe, ví dụ :8009 (service không có route nghiệp vụ) |
| `READINESS_TIMEOUT` | `2s` | hạn tối đa của /health/ready khi kiểm database và Redis |
| `READ_HEADER_TIMEOUT` | `10s` | hạn đọc phần đầu HTTP của mỗi yêu cầu probe (chống kết nối treo) |
| `REDIS_URL` | `redis://localhost:6379/0` | (bắt buộc): broker Redis Streams mà fhir-server phát sự kiện ghi resource lên (BROKER_DRIVER=redis, FHIR_SUBSCRIPTIONS hoặc FHIR_AUTO_DOWNLOAD bật ở API). Broker bộ nhớ không đến được tiến trình khác nên service này không có lựa chọn đó. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | sau SIGTERM: readiness 503 nhưng vẫn chạy, để orchestrator gỡ instance khỏi danh sách |
| `SHUTDOWN_TIMEOUT` | `25s` | thời gian tối đa chờ việc tải đang chạy xong sau khi hết drain; terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị |
