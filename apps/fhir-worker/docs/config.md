# fhir-worker: cấu hình

Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_NAME` | `trivita-fhir-worker` | tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt) |
| `BROKER_DRIVER` | `memory` | BrokerDriver: "redis" khi phải phát sự kiện (Subscriptions hoặc AutoDownload); "memory" nếu không (không có gì để phát). |
| `DATABASE_CONN_MAX_LIFETIME` | `30m0s` | tuổi tối đa của một kết nối trước khi được thay |
| `DATABASE_ECHO` | `false` | ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số). |
| `DATABASE_MAX_IDLE_CONNS` | `5` | số kết nối nhàn rỗi giữ lại |
| `DATABASE_MAX_OPEN_CONNS` | `20` | số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md). |
| `DATABASE_URL` | `sqlite://./fhir.db` | phải trỏ tới CÙNG schema `fhir` với fhir-server (đọc và ghi AsyncJob, đọc và ghi dữ liệu theo quyền của người đã tạo job). fhir-worker KHÔNG chạy migration (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động. |
| `ENVIRONMENT` | `local` | ENVIRONMENT: local \| development \| staging \| production |
| `FHIR_AUTO_DOWNLOAD` | `false` | AutoDownload: xem Subscriptions. |
| `FHIR_BASE64_BINARY_MAX_BYTES` | `1048576` | trần `data` base64 (đã giải mã) khi batch ghi Binary (khi dùng kho); âm: không trần |
| `FHIR_BASE_URL` | `http://localhost:8005/fhir/R4` | là địa chỉ công khai của API FHIR (FHIR_BASE_URL của fhir-server): URL file kết quả export (`<base>/Binary/<id>`) và Location của batch async được dựng từ đây, nên PHẢI trùng fhir-server. |
| `FHIR_BATCH_CHECKPOINT_ENTRIES` | `10` | BatchCheckpointEntries: số entry giữa hai lần lưu checkpoint của batch async (ngoài ra lưu ít nhất mỗi 5 giây). |
| `FHIR_BINARY_STORAGE` | `(rỗng)` | (tùy chọn) là kho nội dung Binary, PHẢI trùng FHIR_BINARY_STORAGE của fhir-server khi API dùng kho: file kết quả export và đầu vào, kết quả của batch async ghi ra kho thay vì base64 trong CSDL. Rỗng: kết quả nằm base64 trong CSDL (như fhir-server không bật kho): `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`. |
| `FHIR_EXPORT_RETENTION` | `24h0m0s` | là thời gian giữ kết quả bulk export và batch async (âm: không bao giờ xóa); xóa bằng Expunge nên nội dung ở kho cũng bị xóa. |
| `FHIR_JOB_CANCEL_POLL` | `2s` | JobCancelPoll: Chu kỳ replica đang chạy job kiểm tra job có bị hủy (từ replica khác) không; việc hủy có hiệu lực trong chừng này thay vì chờ JobLease/3. Mỗi job đang chạy tốn một lần đọc mỗi chu kỳ; không vượt JobLease/3. Phải > 0. |
| `FHIR_JOB_CONCURRENCY` | `2` | là số job chạy đồng thời trong một instance. |
| `FHIR_JOB_LEASE` | `5m0s` | JobLease: thời gian một instance giữ quyền chạy job trước khi instance khác tiếp quản (tự gia hạn khi job còn chạy). |
| `FHIR_JOB_MAINTENANCE_INTERVAL` | `5s` | là chu kỳ nhận job mới (accepted), tiếp quản job mồ côi và xóa kết quả export hết hạn; fhir-worker nhận job qua vòng này nên mặc định ngắn (5 giây). Phải > 0. |
| `FHIR_JOB_RECOVERY_GRACE` | `2s` | là tuổi tối thiểu của job accepted mới được nhận (mặc định 2 giây): fhir-server không tự chạy job nên không cần chờ lâu như khi chạy cùng tiến trình. |
| `FHIR_JOB_SIGNING_KEY` | `(rỗng)` | là khóa ký quyền của job (tối thiểu 32 ký tự), dùng chung với fhir-server; bắt buộc khi bật JWT trừ khi dùng dev HMAC. |
| `FHIR_JWT_HMAC_SECRET` | `(rỗng)` | CHỈ cho dev: khi FHIR_JWT_HMAC_SECRET của fhir-server dùng HS256 thì khóa ký job được suy từ nó (như fhir-server); cấm ở staging/production. |
| `FHIR_JWT_ISSUER` | `(rỗng)` | chỉ để biết fhir-server có bật JWT không (fhir-worker không kiểm token): có thì job mang chữ ký quyền của người gọi (job-seal) và chỉ chạy khi chữ ký hợp lệ, bằng đúng quyền và project của người đó; khi đó phải có khóa ký job (FHIR_JOB_SIGNING_KEY, hoặc dev HMAC). Rỗng: token tĩnh, job chạy không tách project như fhir-server ở chế độ đó. |
| `FHIR_PROFILE_UNKNOWN_ERROR` | `false` | profile không có trong kho bị từ chối (mặc định chỉ cảnh báo) |
| `FHIR_S3_ACCESS_KEY_ID` | `(rỗng)` | bắt buộc khi dùng s3: |
| `FHIR_S3_ENDPOINT` | `(rỗng)` | kho tương thích S3 (MinIO, RustFS...), http(s)://host[:port]; trống: AWS |
| `FHIR_S3_REGION` | `(rỗng)` | S3 (BinaryStorage dạng `s3:`). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log. bắt buộc (vd eu-west-1) |
| `FHIR_S3_SECRET_ACCESS_KEY` | `(rỗng)` | bắt buộc khi dùng s3:; không có trong log |
| `FHIR_S3_SESSION_TOKEN` | `(rỗng)` | tùy chọn, cho khóa tạm |
| `FHIR_S3_SSE` | `(rỗng)` | "", "AES256" hoặc "aws:kms" |
| `FHIR_SKIP_CONSTRAINTS` | `false` | chỉ kiểm cấu trúc, bỏ invariant FHIRPath |
| `FHIR_SKIP_VALIDATION` | `false` | SkipValidation, SkipConstraints, ValidateProfiles, ProfileUnknownError, UpdateAsCreate và Base64BinaryMaxBytes PHẢI trùng fhir-server: batch async chạy từng entry qua cùng bộ xử lý ghi như API. bỏ kiểm tra bằng fhirvalidate khi ghi (chỉ dev/test) |
| `FHIR_STORAGE_BASE_URL` | `(rỗng)` | (tùy chọn) là địa chỉ công khai của /storage nếu khác mặc định; dùng để nhận ra và viết lại URL kho trong dữ liệu batch (như fhir-server). |
| `FHIR_SUBSCRIPTIONS` | `false` | Publish: có phát sự kiện ghi resource lên Redis không. Tự suy từ FHIR_SUBSCRIPTIONS hoặc FHIR_AUTO_DOWNLOAD (cùng điều kiện của fhir-server): batch async ghi resource nên các service nghe (fhir-notifier, fhir-downloader) phải thấy các lần ghi đó. Cần BROKER_DRIVER=redis và REDIS_URL (broker bộ nhớ không đến được tiến trình khác). |
| `FHIR_UPDATE_AS_CREATE` | `false` | cho phép PUT Type/id tạo resource mới với id do client chọn |
| `FHIR_VALIDATE_PROFILES` | `false` | kiểm theo meta.profile bằng StructureDefinition trong kho |
| `HTTP_ADDR` | `:8011` | địa chỉ nghe của các probe, ví dụ :8011 (service không có route nghiệp vụ) |
| `READINESS_TIMEOUT` | `2s` | hạn tối đa của /health/ready khi kiểm database |
| `READ_HEADER_TIMEOUT` | `10s` | hạn đọc phần đầu HTTP của mỗi yêu cầu probe (chống kết nối treo) |
| `REDIS_URL` | `redis://localhost:6379/0` | RedisURL: Redis của broker sự kiện (chỉ dùng khi phải phát sự kiện). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | sau SIGTERM: readiness 503 nhưng vẫn chạy, để orchestrator gỡ instance khỏi danh sách |
| `SHUTDOWN_TIMEOUT` | `25s` | thời gian tối đa chờ job đang chạy dừng sau khi hết drain (job chưa xong nhả lease cho nơi khác); terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị |
