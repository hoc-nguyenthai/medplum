# fhir-notifier: cấu hình

Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_NAME` | `trivita-fhir-notifier` | tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt) |
| `BROKER_CLAIM_IDLE` | `0s` | là thời gian một message nằm trong PEL không được ACK thì replica khác nhận lại và giao lại (instance chết giữa lúc giao). 0: tự tính, dài hơn gấp đôi chuỗi giao xấu nhất (FHIR_WEBHOOK_MAX_ATTEMPTS, FHIR_WEBHOOK_TIMEOUT và thời gian chờ giữa các lần thử), tối thiểu 1 phút. Đặt ngắn hơn một chuỗi giao thì message đang giao bị giao trùng ở replica khác. |
| `BROKER_GROUP` | `fhir-notifier` | là consumer group của service này trên stream sự kiện. KHÁC group của fhir-server ("trivita") để cả hai cùng nhận MỌI sự kiện (cùng group thì chia nhau); nhiều replica của fhir-notifier dùng cùng group để chia việc. |
| `DATABASE_CONN_MAX_LIFETIME` | `30m0s` | tuổi tối đa của một kết nối trước khi được thay |
| `DATABASE_ECHO` | `false` | ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số). |
| `DATABASE_MAX_IDLE_CONNS` | `5` | số kết nối nhàn rỗi giữ lại |
| `DATABASE_MAX_OPEN_CONNS` | `20` | số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md). |
| `DATABASE_URL` | `sqlite://./fhir.db` | phải trỏ tới CÙNG schema `fhir` với fhir-server (đọc Subscription, tự tắt Subscription khi giao thất bại lặp lại). fhir-notifier KHÔNG chạy migration (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động. |
| `ENVIRONMENT` | `local` | ENVIRONMENT: local \| development \| staging \| production |
| `FHIR_SUBSCRIPTION_AUTO_DISABLE` | `(rỗng)` | là JSON mảng trigger tự tắt Subscription rest-hook giao thất bại lặp lại, dạng [{"maxConsecutiveFailures":10,"timeWindowSeconds":3600}] (cùng dạng subscriptionAutoDisable của Medplum). Rỗng: không tự tắt. Đếm chung giữa các replica trên Redis. |
| `FHIR_WEBHOOK_ALLOW_PRIVATE` | `false` | CHỈ cho dev: cho phép giao tới địa chỉ nội bộ (chống SSRF bị tắt). Cấm ở staging/production. |
| `FHIR_WEBHOOK_MAX_ATTEMPTS` | `5` | là số lần thử tối đa cho mỗi lần giao (thử lại tại chỗ, chờ 1 giây nhân đôi mỗi lần, trần 1 phút); hết thì bỏ cuộc, ghi log và tính vào ngưỡng tự tắt. |
| `FHIR_WEBHOOK_TIMEOUT` | `10s` | là hạn của mỗi request webhook. |
| `FHIR_WEBHOOK_WORKERS` | `4` | vừa là số sự kiện xử lý song song, vừa là số webhook giao song song cho mỗi sự kiện. |
| `FHIR_WS_MODE` | `(rỗng)` | WSMode: "off" (mặc định, không khớp Subscription websocket) hoặc "issuer" (khớp một lần cho cả cụm rồi phát lên kênh Redis cho ws-gateway; token bind vẫn do fhir-server cấp). "embedded" (Hub trong tiến trình) không dùng được ở service này. |
| `HTTP_ADDR` | `:8010` | địa chỉ nghe của các probe, ví dụ :8010 (service không có route nghiệp vụ) |
| `READINESS_TIMEOUT` | `2s` | hạn tối đa của /health/ready khi kiểm database và Redis |
| `READ_HEADER_TIMEOUT` | `10s` | hạn đọc phần đầu HTTP của mỗi yêu cầu probe (chống kết nối treo) |
| `REDIS_URL` | `redis://localhost:6379/0` | (bắt buộc): broker Redis Streams mà fhir-server phát sự kiện ghi resource lên (BROKER_DRIVER=redis, FHIR_SUBSCRIPTIONS=true ở API), cũng là nơi đếm thất bại giao chung giữa các replica và kênh websocket. Broker bộ nhớ không đến được tiến trình khác nên service này không có lựa chọn đó. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | sau SIGTERM: readiness 503 nhưng vẫn chạy, để orchestrator gỡ instance khỏi danh sách |
| `SHUTDOWN_TIMEOUT` | `25s` | thời gian tối đa chờ lần giao đang chạy bị hủy và dừng sau khi hết drain; terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị |
