# dicomweb: cấu hình

Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_NAME` | `trivita-dicomweb` | tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt) |
| `DATABASE_CONN_MAX_LIFETIME` | `30m0s` | tuổi tối đa của một kết nối trước khi được thay |
| `DATABASE_ECHO` | `false` | ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số). |
| `DATABASE_MAX_IDLE_CONNS` | `5` | số kết nối nhàn rỗi giữ lại |
| `DATABASE_MAX_OPEN_CONNS` | `20` | số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md). |
| `DATABASE_URL` | `sqlite://./fhir.db` | phải trỏ tới CÙNG schema `fhir` với fhir-server: DicomStudy, DicomSeries, DicomInstance là resource FHIR, tìm được qua API của fhir-server. dicomweb KHÔNG chạy migration (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động. |
| `DEBUG` | `false` | bật chế độ gỡ lỗi của Echo (không dùng ở production) |
| `DICOMWEB_ALLOWED_ORIGINS` | `(rỗng)` | là origin được gọi API từ trình duyệt (CORS); rỗng: không origin nào. Mặc định http://localhost:3000 ở local. |
| `DICOMWEB_BASE_URL` | `http://localhost:8008` | là địa chỉ công khai của dicomweb (không gồm tiền tố /dicomweb), dùng cho RetrieveURL trong phản hồi STOW, ví dụ https://dicom.example.org. |
| `DICOMWEB_MAX_HEADER_BYTES` | `1048576` | là trần phần đầu (mọi phần tử trước PixelData) được nạp vào bộ nhớ để đọc metadata; vượt thì instance bị từ chối (0xA700). Cùng với giới hạn độ sâu SQ (16 lớp, cố định) chống tệp độc (docs/plans/cds-dicom/d0-spike.md 3.1). |
| `DICOMWEB_MAX_INSTANCE_BYTES` | `1073741824` | là trần kích thước một phần (một instance) của STOW: vượt thì instance đó bị từ chối riêng (FailureReason 0xA700) và không để lại gì; MaxRequestBytes là trần cả yêu cầu (nhiều phần): vượt thì 413 (các instance đã xong trước đó vẫn được giữ). |
| `DICOMWEB_MAX_REQUEST_BYTES` | `4294967296` | trần cả yêu cầu STOW (nhiều phần); xem MaxInstanceBytes |
| `DICOMWEB_RATE_BURST` | `0` | là cỡ đợt của hạn mức (<=0: gấp đôi RateLimit, tối thiểu 1). |
| `DICOMWEB_RATE_LIMIT` | `0 (tắt ở local; 20 ở staging/production)` | là số yêu cầu mỗi giây mỗi IP (0: tắt). Không đặt: tắt ở local, 20 ở staging/production. Đếm theo từng instance. |
| `DICOMWEB_TRUSTED_PROXIES` | `(rỗng)` | là CIDR của proxy được tin header X-Forwarded-For (cách nhau bằng dấu phẩy); rỗng: không tin header nào. |
| `ENVIRONMENT` | `local` | ENVIRONMENT: local \| development \| staging \| production |
| `FHIR_BINARY_STORAGE` | `(rỗng)` | là kho nội dung DICOM (bắt buộc), PHẢI trùng với FHIR_BINARY_STORAGE của fhir-server vì Binary do dicomweb ghi được fhir-server đọc: `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`. |
| `FHIR_JWKS_URL` | `(rỗng)` | JWKS của auth (production: https), được cache; đã tải khóa thì vẫn xác minh khi auth tạm tắt |
| `FHIR_JWT_AUDIENCE` | `(rỗng)` | audience mà token phải có (bắt buộc) |
| `FHIR_JWT_HMAC_SECRET` | `(rỗng)` | CHỈ cho dev/test: khóa HS256 dùng chung thay cho JWKS (tối thiểu 32 ký tự) |
| `FHIR_JWT_ISSUER` | `(rỗng)` | JWT (bắt buộc): access token do auth cấp, kiểm bằng JWKS (production) hoặc HS256 dùng chung (chỉ dev/test). Dữ liệu tách theo project lấy từ claim project. |
| `FHIR_JWT_POLICY_CLAIM` | `fhir_access_policy` | tên claim chứa chính sách truy cập (AccessPolicy) đã dịch |
| `FHIR_JWT_PROJECT_CLAIM` | `fhir_project` | tên claim chứa project của token |
| `FHIR_JWT_SCOPE_CLAIM` | `scope` | tên claim chứa scope |
| `FHIR_JWT_SUPER_ADMIN` | `false` | cho phép token super-admin hành động trong project chọn bằng header X-Fhir-Project |
| `FHIR_S3_ACCESS_KEY_ID` | `(rỗng)` | bắt buộc khi dùng s3: |
| `FHIR_S3_ENDPOINT` | `(rỗng)` | kho tương thích S3 (MinIO...), http(s)://host[:port]; trống: AWS |
| `FHIR_S3_REGION` | `(rỗng)` | S3 (BinaryStorage dạng `s3:`). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log. bắt buộc (vd eu-west-1) |
| `FHIR_S3_SECRET_ACCESS_KEY` | `(rỗng)` | bắt buộc khi dùng s3:; không có trong log |
| `FHIR_S3_SESSION_TOKEN` | `(rỗng)` | tùy chọn, cho khóa tạm |
| `FHIR_S3_SSE` | `(rỗng)` | "", "AES256" hoặc "aws:kms" |
| `HTTP_ADDR` | `:8008` | địa chỉ nghe, ví dụ :8008 |
| `READINESS_TIMEOUT` | `2s` | hạn tối đa của /health/ready khi kiểm database |
| `READ_HEADER_TIMEOUT` | `10s` | hạn đọc phần đầu HTTP của mỗi yêu cầu (chống kết nối treo) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | sau SIGTERM: readiness 503 nhưng vẫn phục vụ, để orchestrator gỡ instance khỏi tải |
| `SHUTDOWN_TIMEOUT` | `25s` | thời gian tối đa chờ yêu cầu đang chạy xong sau khi hết drain; terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị |
