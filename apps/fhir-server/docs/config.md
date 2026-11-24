# fhir-server: cấu hình

Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_NAME` | `trivita-fhir-server` | Tên service trong log và telemetry. |
| `BROKER_DRIVER` | `memory` | Broker sự kiện của Subscription: `memory` (một tiến trình) hoặc `redis` (nhiều replica, cần REDIS_URL). |
| `DATABASE_AUTO_MIGRATE` | `true` | Tự chạy migration khi khởi động. Nhiều replica hoặc role migrate riêng thì đặt false và chạy `fhir-server migrate up` ở bước deploy; phải là false khi đặt DATABASE_MIGRATE_URL. |
| `DATABASE_CONN_MAX_LIFETIME` | `30m0s` | Tuổi tối đa của một kết nối trong pool. |
| `DATABASE_ECHO` | `false` | In câu SQL ra log (chỉ để gỡ lỗi; có thể lộ dữ liệu lâm sàng). |
| `DATABASE_MAX_IDLE_CONNS` | `5` | Số kết nối nhàn rỗi giữ lại (>= 0). |
| `DATABASE_MAX_OPEN_CONNS` | `20` | Số kết nối tối đa của pool (phải > 0). Tổng mọi replica của mọi service phải nhỏ hơn `max_connections` của PostgreSQL. |
| `DATABASE_MIGRATE_URL` | `(rỗng)` | (chỉ PostgreSQL) là URL của role *_migrate (có DDL) dùng riêng cho lệnh `migrate`; app chạy bằng DATABASE_URL (role *_app chỉ có DML, D-DB-4). Đặt thì app KHÔNG tự migrate (DATABASE_AUTO_MIGRATE phải là false). Rỗng: `migrate` dùng DATABASE_URL. |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng của service (mô hình một database nhiều schema, docs/plans/microservices-shared-db.md). Mặc định tắt để không phá cách chạy cũ (database riêng, schema public); BẬT khi nhiều service chung một database. |
| `DATABASE_URL` | `sqlite://./fhir.db` | URL cơ sở dữ liệu: `postgres://` (production) hoặc `sqlite://<tệp>` (chỉ dev). Cấm SQLite ở staging/production. Chung một PostgreSQL với auth thì dùng role `fhir_app` có schema riêng `fhir` và bật DATABASE_REQUIRE_SCHEMA. |
| `DEBUG` | `false` | Chế độ debug; cấm ở staging/production. |
| `ENVIRONMENT` | `local` | ENVIRONMENT: local \| development \| staging \| production |
| `FHIR_ADMIN_TOKENS` | `(rỗng)` | ghi và quản trị ($reindex, $expunge) |
| `FHIR_ALLOWED_ORIGINS` | `(rỗng)` | là origin (cách nhau bằng dấu phẩy) được gọi API từ trình duyệt qua CORS, dạng http(s)://host[:port]. Rỗng: không origin nào (mặc định ENVIRONMENT=local: http://localhost:3000). Khác FHIR_WS_ALLOWED_ORIGINS (kiểm Origin lúc mở websocket). |
| `FHIR_ASYNC_BATCH` | `false` | AsyncBatch: POST Bundle batch kèm `Prefer: respond-async` chạy thành job nền (tắt: header bị bỏ qua, chạy đồng bộ). BatchCheckpointEntries: số entry giữa hai lần lưu checkpoint của batch async (ngoài ra lưu ít nhất mỗi 5 giây). |
| `FHIR_AUTH_DISABLED` | `false` | CHỈ cho dev: tắt xác thực |
| `FHIR_AUTO_DOWNLOAD` | `false` | bật tự tải attachment có URL NGOÀI về kho Binary sau khi resource được ghi (như worker `download` của Medplum, nhưng mặc định TẮT: gọi URL do người dùng cung cấp là bề mặt SSRF và chi phí). Cần FHIR_BINARY_STORAGE; việc tải chạy nền, thử lại 3 lần với broker bộ nhớ (broker Redis: thử lại và dead-letter theo broker). |
| `FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES` | `(rỗng)` | (cách nhau bằng dấu phẩy, không rỗng) chỉ cho tải URL bắt đầu bằng một tiền tố trong danh sách; AutoDownloadIgnoredPrefixes là danh sách bỏ qua (thắng danh sách cho phép). |
| `FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE` | `false` | CHỈ cho dev: cho phép tải từ địa chỉ nội bộ (chống SSRF bị tắt). |
| `FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES` | `(rỗng)` | (chưa có mô tả trong mã) |
| `FHIR_AUTO_DOWNLOAD_MAX_BYTES` | `0` | là trần kích thước một tệp tải về; 0: dùng FHIR_STORAGE_MAX_UPLOAD_BYTES. |
| `FHIR_AUTO_DOWNLOAD_TIMEOUT` | `30s` | là hạn của mỗi lần tải. |
| `FHIR_BASE64_BINARY_MAX_BYTES` | `1048576` | là trần `data` base64 (đã giải mã) khi ghi resource Binary (mặc định 1 MiB; âm: không trần). |
| `FHIR_BASE_URL` | `http://localhost:8005/fhir/R4` | là địa chỉ công khai của API FHIR (dùng cho Location, fullUrl, link phân trang), ví dụ https://fhir.example.org/fhir/R4. |
| `FHIR_BATCH_CHECKPOINT_ENTRIES` | `10` | Số entry giữa hai lần lưu checkpoint của batch async (ngoài ra lưu ít nhất mỗi 5 giây); phải > 0. |
| `FHIR_BINARY_STORAGE` | `(rỗng)` | Kho nội dung Binary (docs/plans/modules/email-storage.md, S3a/S3b). BinaryStorage rỗng: tắt (Binary giữ `data` base64 trong CSDL như trước, không có /storage và không có Binary thô). Dạng `file:<thư mục>` (giữ bố cục Medplum); tiền tố khác bị từ chối. |
| `FHIR_EXPORT_RETENTION` | `24h0m0s` | Thời gian giữ kết quả bulk export trước khi bị dọn; âm là không bao giờ xóa. |
| `FHIR_JOB_CONCURRENCY` | `2` | Job nền (AsyncJob): số job chạy đồng thời trong process. |
| `FHIR_JOB_LEASE` | `5m0s` | JobLease: thời gian một replica giữ quyền chạy job trước khi replica khác tiếp quản (tự gia hạn khi job còn chạy). JobMaintenanceInterval: chu kỳ chạy lại job mồ côi và xóa kết quả export hết hạn (0 = tắt). ExportRetention: thời gian giữ kết quả bulk export (âm = không bao giờ xóa). |
| `FHIR_JOB_MAINTENANCE_INTERVAL` | `30s` | Chu kỳ chạy lại job mồ côi và xóa kết quả export hết hạn; 0 là tắt (phải >= 0). |
| `FHIR_JOB_SIGNING_KEY` | `(rỗng)` | khóa ký quyền của job nền (dùng chung mọi replica); bắt buộc với JWKS, dev HMAC tự suy ra |
| `FHIR_JWKS_URL` | `(rỗng)` | URL JWKS của identity provider (thường `<auth>/.well-known/jwks.json`) để kiểm chữ ký access token; https ở staging/production. Khi bật JWT cần đúng một trong FHIR_JWKS_URL hoặc FHIR_JWT_HMAC_SECRET. |
| `FHIR_JWT_AUDIENCE` | `(rỗng)` | Claim `aud` mong đợi của access token (bằng AUTH_AUDIENCE của auth, mặc định chính issuer); bắt buộc khi đặt FHIR_JWT_ISSUER. |
| `FHIR_JWT_HMAC_SECRET` | `(rỗng)` | CHỈ cho dev/test: khóa HS256 dùng chung thay cho JWKS |
| `FHIR_JWT_ISSUER` | `(rỗng)` | OAuth2/OIDC: bật khi FHIR_JWT_ISSUER được đặt. Access token (JWT) được kiểm tra chữ ký theo JWKS của identity provider, và dữ liệu được tách theo project lấy từ claim project. Loại trừ với token tĩnh ở trên. |
| `FHIR_JWT_POLICY_CLAIM` | `fhir_access_policy` | Tên claim chứa AccessPolicy đã dịch của token (mặc định `fhir_access_policy`); không được rỗng. |
| `FHIR_JWT_PROJECT_CLAIM` | `fhir_project` | Tên claim chứa project của token, dùng để tách dữ liệu theo project (mặc định `fhir_project`); không được rỗng. |
| `FHIR_JWT_SCOPE_CLAIM` | `scope` | Tên claim chứa scope của token (mặc định `scope`); không được rỗng. |
| `FHIR_JWT_SUPER_ADMIN` | `false` | bật scope fhir.super (super-admin xuyên project, có nhật ký kiểm toán) |
| `FHIR_RATE_BURST` | `0` | Cỡ đợt của hạn mức mỗi IP (mặc định gấp đôi FHIR_RATE_LIMIT, tối thiểu 1; không được âm). |
| `FHIR_RATE_LIMIT` | `tắt ở local; staging/production: 20` | là số yêu cầu mỗi giây cho mỗi IP trên /fhir/R4 (0: tắt; không đặt thì ở staging/production là DefaultProdRateLimit); RateBurst là cỡ đợt (mặc định gấp đôi RateLimit, tối thiểu 1). |
| `FHIR_RATE_LIMIT_SHARED` | `false` | RateLimitShared: đếm hạn mức trên Redis (REDIS_URL) để N replica dùng chung một hạn mức thay vì mỗi replica một hạn mức. Redis lỗi: tạm rơi về bộ đếm riêng của replica. |
| `FHIR_READ_TOKENS` | `(rỗng)` | Token dạng `tên:secret`, phân tách bằng dấu phẩy (nhiều token để xoay vòng khóa). chỉ đọc và tìm kiếm |
| `FHIR_S3_ACCESS_KEY_ID` | `(rỗng)` | Khóa truy cập S3, bắt buộc khi FHIR_BINARY_STORAGE dạng `s3:`; chỉ qua biến môi trường, không bao giờ vào log (chưa hỗ trợ khóa tạm từ IMDS/STS). |
| `FHIR_S3_ENDPOINT` | `(rỗng)` | kho tương thích S3 (MinIO...), http(s)://host[:port]; trống: AWS |
| `FHIR_S3_REGION` | `(rỗng)` | S3 (BinaryStorage dạng `s3:<bucket>[/<prefix>]`, S7). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log. bắt buộc (vd eu-west-1) |
| `FHIR_S3_SECRET_ACCESS_KEY` | `(rỗng)` | Secret của khóa truy cập S3, bắt buộc khi dùng `s3:`; chỉ qua biến môi trường, không bao giờ vào log. |
| `FHIR_S3_SESSION_TOKEN` | `(rỗng)` | Token phiên tạm (STS) đi kèm khóa truy cập S3, nếu bạn cấp khóa tạm bằng tay; rỗng là khóa dài hạn. |
| `FHIR_S3_SSE` | `(rỗng)` | "", "AES256" hoặc "aws:kms" |
| `FHIR_SKIP_CONSTRAINTS` | `false` | Chỉ kiểm cấu trúc khi ghi, bỏ invariant FHIRPath (nhanh hơn, kém an toàn hơn). Khác FHIR_SKIP_VALIDATION (bỏ hẳn kiểm tra, cấm ở staging/production). |
| `FHIR_SKIP_VALIDATION` | `false` | bỏ kiểm tra resource khi ghi; SkipConstraints chỉ kiểm tra cấu trúc, bỏ invariant FHIRPath. |
| `FHIR_STORAGE_BASE_URL` | `(rỗng)` | là gốc công khai của URL ký (mặc định: gốc của FHIR_BASE_URL + /storage); host phải là host client nhìn thấy. |
| `FHIR_STORAGE_LEGACY_GET_SIGNATURE` | `false` | chấp nhận chữ ký GET kiểu cũ của Medplum (không có tiền tố "GET ") |
| `FHIR_STORAGE_MAX_UPLOAD_BYTES` | `268435456` | trần nội dung tải lên sau giải nén (mặc định 256 MiB) |
| `FHIR_STORAGE_SIGNING_KEY_PATH` | `(rỗng)` | là PEM khóa riêng RSA (PKCS#8 không mật khẩu, quyền 0600). Staging/production bắt buộc; dev không đặt thì sinh khóa tạm (URL mất hiệu lực khi khởi động lại, không chạy được nhiều replica). StorageSigningPreviousKeys là các tệp PEM khóa CÔNG KHAI cũ (cách nhau bằng dấu phẩy) chỉ để xác minh khi xoay khóa. |
| `FHIR_STORAGE_SIGNING_PREVIOUS_KEYS` | `(rỗng)` | Các tệp PEM khóa CÔNG KHAI cũ (cách nhau bằng dấu phẩy, tối đa 3), chỉ để xác minh URL ký cũ khi xoay khóa; cần đặt cả FHIR_STORAGE_SIGNING_KEY_PATH. |
| `FHIR_STORAGE_URL_TTL` | `1h0m0s` | hạn URL ký (mặc định 1h, tối đa 24h) |
| `FHIR_SUBSCRIPTIONS` | `false` | Subscription (rest-hook). Sự kiện thay đổi đi qua broker: memory (1 process) \| redis (nhiều replica). |
| `FHIR_SUBSCRIPTION_AUTO_DISABLE` | `(rỗng)` | là JSON mảng trigger tự tắt Subscription rest-hook giao thất bại lặp lại, cùng dạng subscriptionAutoDisable của Medplum: [{"maxConsecutiveFailures":10,"timeWindowSeconds":3600}]. Rỗng: không tự tắt. Đọc bằng AutoDisableTriggers. |
| `FHIR_TRUSTED_PROXIES` | `(rỗng)` | là CIDR của reverse proxy được tin khi đọc IP người gọi từ X-Forwarded-For (cách nhau bằng dấu phẩy). Rỗng: dùng IP kết nối trực tiếp, nên đứng sau proxy mà không khai thì mọi người gọi dùng chung một IP (và một hạn mức). |
| `FHIR_UPDATE_AS_CREATE` | `false` | UpdateAsCreate: PUT Type/id tạo mới với id do client chọn khi chưa có (mặc định tắt: 404). |
| `FHIR_WEBHOOK_ALLOW_PRIVATE` | `false` | CHỈ cho dev: cho phép giao tới địa chỉ nội bộ (chống SSRF bị tắt) |
| `FHIR_WEBHOOK_MAX_ATTEMPTS` | `5` | Số lần thử giao tối đa cho mỗi sự kiện rest-hook (phải > 0). |
| `FHIR_WEBHOOK_TIMEOUT` | `10s` | Thời gian chờ mỗi lần giao rest-hook (phải > 0). |
| `FHIR_WEBHOOK_WORKERS` | `4` | Số worker giao Subscription rest-hook đồng thời (phải > 0). |
| `FHIR_WEBSOCKET` | `false` | BÍ DANH CŨ của WSMode="embedded" (FHIR_WEBSOCKET) |
| `FHIR_WRITE_TOKENS` | `(rỗng)` | đọc và ghi (create, update, patch, delete, batch, transaction) |
| `FHIR_WS_ALLOWED_ORIGINS` | `(rỗng)` | origin được phép, cách nhau bằng dấu phẩy (rỗng: chỉ cùng host) |
| `FHIR_WS_HEARTBEAT` | `30s` | chu kỳ heartbeat |
| `FHIR_WS_MAX_BINDINGS_PER_USER` | `20` | số lượt bind websocket đồng thời tối đa của một người trong một project (mỗi instance) |
| `FHIR_WS_MAX_CONNECTIONS` | `1000` | số kết nối websocket tối đa mỗi instance |
| `FHIR_WS_MODE` | `(rỗng)` | chọn nơi giữ websocket của Subscription: "off" (không có), "embedded" (Hub trong chính tiến trình này) hoặc "issuer" (chỉ cấp token bind và khớp sự kiện; kết nối do ws-gateway giữ, cần Redis). Rỗng: suy từ WebSocket (bí danh cũ FHIR_WEBSOCKET=true là "embedded", còn lại "off"). Dùng WSModeResolved để đọc. |
| `FHIR_WS_SIGNING_KEY` | `(rỗng)` | khóa ký token websocket (dùng chung mọi instance) |
| `FHIR_WS_SIGNING_KEY_PREVIOUS` | `(rỗng)` | khóa cũ chỉ để xác minh khi xoay khóa, cách nhau bằng dấu phẩy (tối đa 3) |
| `FHIR_WS_URL` | `(rỗng)` | websocket-url trả cho client (mặc định suy từ FHIR_BASE_URL) |
| `HTTP_ADDR` | `:8005` | Địa chỉ lắng nghe HTTP. |
| `READINESS_TIMEOUT` | `2s` | Thời gian chờ tối đa khi `/readyz` kiểm DB (phải > 0). |
| `READ_HEADER_TIMEOUT` | `10s` | Thời gian chờ đọc header của yêu cầu (chống slowloris). |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis (`redis://...`) cho broker khi bật FHIR_SUBSCRIPTIONS với BROKER_DRIVER=redis (bắt buộc); cùng Redis với ws-gateway khi FHIR_WS_MODE=issuer. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Thời gian giữ service sống nhưng báo `/readyz` 503 trước khi đóng, để load balancer rút instance (SIGTERM); phải >= 0. |
| `SHUTDOWN_TIMEOUT` | `25s` | Thời gian tối đa chờ yêu cầu đang chạy xong khi tắt (phải > 0). Job nền chưa xong được nhả lease để instance khác chạy lại. |

## Ràng buộc ở staging/production

`ENVIRONMENT` là `staging` hoặc `production` thì service **từ chối khởi động** (liệt kê mọi lỗi một lượt) nếu vi phạm:

| Bắt buộc | Cấm |
|---|---|
| Xác thực bật: token tĩnh (`FHIR_READ_TOKENS`, `FHIR_WRITE_TOKENS`, `FHIR_ADMIN_TOKENS`, secret >= 24 ký tự) hoặc JWT (`FHIR_JWT_ISSUER` kèm `FHIR_JWT_AUDIENCE` và `FHIR_JWKS_URL`) | `FHIR_AUTH_DISABLED=true` |
| `FHIR_JWKS_URL` dùng https khi bật JWT | `FHIR_JWT_HMAC_SECRET` (chỉ cho dev/test; dùng JWKS) |
| `FHIR_JOB_SIGNING_KEY` (>= 32 ký tự, mọi replica dùng chung) khi dùng JWKS | `FHIR_SKIP_VALIDATION=true` |
| `FHIR_WS_SIGNING_KEY` (>= 32 ký tự) khi bật websocket Subscription | SQLite làm `DATABASE_URL` |
| `FHIR_WS_URL` dùng `wss://` ở `FHIR_WS_MODE=issuer` | `FHIR_WEBHOOK_ALLOW_PRIVATE=true` (tắt chống SSRF) |
| `FHIR_STORAGE_SIGNING_KEY_PATH` (khóa ký URL cố định) và `FHIR_STORAGE_BASE_URL` https khi bật kho Binary | |
| `FHIR_S3_ENDPOINT` dùng https khi có | |
| `FHIR_ALLOWED_ORIGINS` chỉ https, không có `*` | |

Hạn mức `FHIR_RATE_LIMIT` tự bật (20 yêu cầu/giây/IP) khi không đặt; đặt `0` để tắt hẳn. `FHIR_JWT_ISSUER` loại trừ với token tĩnh và `FHIR_AUTH_DISABLED`. `FHIR_WS_MODE=issuer` cần `BROKER_DRIVER=redis`.
