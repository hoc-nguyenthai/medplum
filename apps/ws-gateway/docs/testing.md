# ws-gateway: test

Bố cục chung: [docs/plans/service-independence.md](../../../docs/plans/service-independence.md). Test đơn vị nằm **cạnh mã**; các tầng còn lại nằm trong `tests/`.

| Tầng | Ở đâu | Cần Docker? | Chạy | Kiểm gì |
|---|---|---|---|---|
| Đơn vị | `internal/**/*_test.go` | không | `make test-unit` | cấu hình (kể cả tài liệu `config.md` không lỗi thời), route HTTP và FHIRcast (OperationOutcome, xác thực, giới hạn body), bộ xác thực |
| Tích hợp | `tests/integration` | có (Redis thật) | `make test-integration` | gateway thật trong process + bên khớp/cấp token ở "tiến trình khác" chỉ chung Redis và khóa; readiness khi Redis chết và sống lại; tắt êm; metric; FHIRcast qua hai instance chung Redis |
| e2e (service) | `tests/e2e` | có | `make test-service` | **image dựng từ Dockerfile** chạy cùng Redis thật: người dùng không root, `HEALTHCHECK`, probe, Redis chết và sống lại, nhận sự kiện qua Redis, token giả bị từ chối, FHIRcast qua HTTP và websocket, tắt êm (1001, mã thoát 0), từ chối cấu hình sai |
| Tải | `tests/load` | có (Redis) | `make test-load` (`WS_LOAD_CONNS=4000`) | RSS mỗi socket và độ trễ fan-out; không phải kiểm đúng sai |
| Hệ thống | `tests/e2e` ở gốc repo | không (hiện tại) | `make test-e2e` | `auth` + `fhir-server` + `ws-gateway` nhiều tiến trình (sẽ chuyển sang image ở bước T4) |

Chạy mọi tầng của service: `make test-app` (trong thư mục này hoặc `make test-app APP=ws-gateway` ở gốc repo).

## Docker

Test tích hợp và e2e dựng Redis bằng `pkg/testkit` (testcontainers). Không có Docker thì test **bị bỏ qua có thông báo**; trong CI đặt `TRIVITA_REQUIRE_DOCKER=1` để thiếu Docker thì **thất bại**. `go test -short` luôn bỏ qua các test cần Docker. Lần đầu có thể mất vài phút để kéo image `redis:7.4-alpine` và dựng image gateway (các lần sau dùng cache).

e2e dựng image từ `apps/ws-gateway/Dockerfile` (context là gốc repo; thư viện quan sát, vì `go.mod` có `replace ../trivita-observability-go`, đi qua build context có tên). Cần thư mục `trivita-observability-go` nằm cạnh repo, nếu không e2e **bỏ qua có thông báo**.

## Quy tắc

- `tests/*` chỉ dùng API công khai và `internal/` của chính gateway, không import app khác (`architecture_test.go`).
- Test chống lỗi thời: `docs/config.md` được sinh từ `internal/config/config.go`; thêm biến mà quên cập nhật thì `TestConfigDocIsUpToDate` đỏ (cập nhật: `UPDATE_DOCS=1 go test ./apps/ws-gateway/internal/config -run TestConfigDocIsUpToDate`).
