# ws-gateway: cấu hình

Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_NAME` | `trivita-ws-gateway` | tên service trong log và telemetry |
| `DEBUG` | `false` | bật chế độ debug của Echo (không dùng ở production) |
| `ENVIRONMENT` | `local` | ENVIRONMENT: local \| development \| staging \| production |
| `FHIRCAST_ALLOW_TOPIC_MISMATCH` | `false` | cho phép POST /{topic} dùng event["hub.topic"] khác topic trên đường dẫn (như Medplum, bỏ qua topic đường dẫn). Mặc định false: từ chối 400 "Topic mismatch". |
| `FHIRCAST_CONTEXT_TTL` | `24h0m0s` | hạn ngữ cảnh của topic (mặc định 24 giờ) |
| `FHIRCAST_ENABLED` | `false` | bật Hub FHIRcast (/fhircast/STU2, /fhircast/STU3, /hub). Cần Redis (dùng chung) và xác thực JWT. |
| `FHIRCAST_HEARTBEAT` | `10s` | chu kỳ heartbeat của từng socket (mặc định 10s) |
| `FHIRCAST_JWKS_URL` | `(rỗng)` | địa chỉ JWKS của auth (https ở staging/production); đặt đúng một trong JWKS và HMAC |
| `FHIRCAST_JWT_AUDIENCE` | `(rỗng)` | claim aud mà token phải có (bắt buộc khi bật FHIRcast) |
| `FHIRCAST_JWT_HMAC_SECRET` | `(rỗng)` | khóa HS256 dùng chung, tối thiểu 32 ký tự; CHỈ dev/test, cấm ở staging/production |
| `FHIRCAST_JWT_ISSUER` | `(rỗng)` | Xác thực JWT: cùng nhà phát hành token với fhir-server (auth). JWKS là đường production; HMAC chỉ cho dev/test. claim iss mà token phải có (bắt buộc khi bật FHIRcast) |
| `FHIRCAST_LEASE` | `1h0m0s` | hạn một đăng ký (mặc định 1 giờ) |
| `FHIRCAST_MAX_CONNECTIONS` | `1000` | Kênh WebSocket /ws/fhircast/{endpoint}. IP, Origin và thời gian đóng dùng chung FHIR_WS_MAX_CONNS_PER_IP, FHIR_WS_TRUSTED_PROXIES, FHIR_WS_ALLOWED_ORIGINS, FHIR_WS_CLOSE_TIMEOUT, FHIR_WS_RATE_LIMIT. số socket FHIRcast tối đa mỗi instance (mặc định 1000; tách khỏi FHIR_WS_MAX_CONNECTIONS) |
| `FHIRCAST_MAX_CONTEXT_BYTES` | `262144` | kích thước tối đa một ngữ cảnh lưu (mặc định 256 KiB) |
| `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` | `4` | số socket đồng thời tối đa của một endpoint, mỗi instance (mặc định 4) |
| `FHIRCAST_MAX_UPDATE_ENTRIES` | `200` | số entry tối đa của một update bundle (mặc định 200) |
| `FHIRCAST_PUBLIC_URL` | `(rỗng)` | là địa chỉ WebSocket công khai của gateway (ws:// hoặc wss://host[:port]) dùng để dựng hub.channel.endpoint trả cho client; KHÔNG suy từ header Host của yêu cầu (tránh giả mạo). |
| `FHIRCAST_REQUIRE_SCOPE` | `true` | đòi token có scope fhircast/* (mặc định true; tắt chỉ để chuyển tiếp cho client cũ). |
| `FHIR_WS_ALLOWED_ORIGINS` | `(rỗng)` | mẫu host của Origin được phép, cách nhau bằng dấu phẩy (rỗng: chỉ cùng host) |
| `FHIR_WS_CLOSE_TIMEOUT` | `5s` | thời gian chờ client đáp khung đóng khi tắt |
| `FHIR_WS_HEARTBEAT` | `30s` | chu kỳ heartbeat và ping |
| `FHIR_WS_INSTANCE_ID` | `(rỗng)` | định danh trong sổ hiện diện (rỗng: ngẫu nhiên mỗi process) |
| `FHIR_WS_MAX_BINDINGS_PER_USER` | `20` | số lượt bind đồng thời tối đa của một người trong một project (mỗi instance) |
| `FHIR_WS_MAX_CONNECTIONS` | `1000` | số kết nối tối đa mỗi instance |
| `FHIR_WS_MAX_CONNS_PER_IP` | `0` | số kết nối tối đa từ một IP (0: không giới hạn) |
| `FHIR_WS_PRESENCE_TTL` | `30s` | hạn mục hiện diện nếu gateway không gia hạn (bên phát ngừng gửi sau thời gian này khi gateway chết) |
| `FHIR_WS_RATE_BURST` | `0` | cỡ đợt của hạn mức trên (0: gấp đôi RateLimit, tối thiểu 1) |
| `FHIR_WS_RATE_LIMIT` | `tắt ở local; 10 ở staging/production` | là số lần mở websocket mỗi giây mỗi IP (0: tắt; không đặt thì ở staging/production là DefaultProdRateLimit); RateBurst là cỡ đợt (mặc định gấp đôi, tối thiểu 1). |
| `FHIR_WS_SIGNING_KEY` | `(rỗng)` | là khóa HMAC xác minh token bind do fhir-server cấp (>= 32 ký tự, bắt buộc, KHÔNG có giá trị ngẫu nhiên thay thế vì phải trùng bên cấp). WSSigningKeyPrevious là khóa cũ chỉ để xác minh khi xoay khóa, cách nhau bằng dấu phẩy. |
| `FHIR_WS_SIGNING_KEY_PREVIOUS` | `(rỗng)` | khóa cũ chỉ để xác minh khi xoay khóa (tối đa 3, cách nhau bằng dấu phẩy); đặt khóa mới ở gateway trước, đổi bên cấp sau |
| `FHIR_WS_TRUSTED_PROXIES` | `(rỗng)` | là các CIDR của proxy được tin khi đọc X-Forwarded-For (để đếm theo IP và hạn mức). Rỗng: dùng địa chỉ kết nối. |
| `HTTP_ADDR` | `:8007` | địa chỉ HTTP lắng nghe (HTTP_ADDR), ví dụ :8007 |
| `READINESS_TIMEOUT` | `2s` | hạn mỗi lần kiểm Redis của /health/ready |
| `READ_HEADER_TIMEOUT` | `10s` | hạn đọc header của một yêu cầu HTTP (chống kết nối chậm) |
| `REDIS_URL` | `redis://localhost:6379/0` | là Redis dùng chung với fhir-server: kênh sự kiện và sổ hiện diện của Subscription (bắt buộc ở staging/production). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | thời gian báo "chưa sẵn sàng" trước khi đóng cổng để K8s rút khỏi Service (pkg/httpserve) |
| `SHUTDOWN_TIMEOUT` | `25s` | thời gian tối đa của cả quá trình tắt êm (pkg/httpserve) |
