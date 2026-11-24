# fhir-downloader: kiểm thử

Bốn tầng theo [service-independence.md](../../../docs/plans/service-independence.md); mẫu `apps/ws-gateway`.

| Tầng | Vị trí | Cần | Lệnh | Kiểm gì |
|---|---|---|---|---|
| Đơn vị | `internal/**/*_test.go` | không | `make test-unit` | cấu hình (từ chối sai, gom mọi lỗi, tài liệu cấu hình khớp mã), probe (liveness không chạm phụ thuộc, 404 mọi route nghiệp vụ, header bảo mật, panic thành 500), lắp ráp trên miniredis và SQLite tệp: tải từ sự kiện, group riêng nhận mọi sự kiện, chặn địa chỉ nội bộ, bỏ URL của chính hệ thống, từ chối schema chưa migrate, readiness, tắt êm |
| Tích hợp | `tests/integration` | Docker | `make test-integration` | PostgreSQL thật với role hẹp quyền và `DATABASE_REQUIRE_SCHEMA=true`; nội dung kho đúng từng byte; Redis chết rồi sống lại |
| e2e | `tests/e2e` | Docker | `make test-service` | dựng image từ Dockerfile, chạy cùng PostgreSQL và Redis thật: healthcheck, tải đầu-cuối, chặn địa chỉ nội bộ mặc định, SIGTERM thoát mã 0, chạy lại xử lý sự kiện ghi lúc dừng, từ chối cấu hình sai và schema chưa migrate |
| Tải | chưa có | | | điều kiện A chưa có số đo (xem architecture.md) |

Phía `fhir-server` được mô phỏng trong test bằng `pkg/fhirstore` + `pkg/fhirsub` (ghi resource, phát sự kiện lên Redis), vì app không được import app khác (C-1, C-7). e2e cần `host.docker.internal` (Docker Desktop, OrbStack); trên Linux phải thêm `--add-host`. Docker của OrbStack mặc định tắt: `orbctl start`, và `export DOCKER_HOST=unix://$HOME/.orbstack/run/docker.sock` (xem `docs/` của repo về OrbStack).

## Đột biến đã chạy (2026-11-24)

Mỗi thay đổi dưới đây làm test đỏ (bắt đúng test nêu tên):

| Đột biến | Test bắt |
|---|---|
| ghi resource không qua `fhirsub.Wrap` | `TestEachConsumerGroupReceivesEveryEvent` |
| bỏ `OwnPrefixes` | `TestSkipsOwnAndIgnoredURLs` |
| luôn cho địa chỉ nội bộ | `TestBlocksInternalAddressesByDefault` |
| group là `trivita` | `TestEachConsumerGroupReceivesEveryEvent` |
| bỏ kiểm `Pending` | `TestRefusesToStartOnUnmigratedSchema` |
| readiness bỏ kiểm broker | `TestReadinessReflectsDependenciesAndDraining` |

Chưa chạy: đột biến cho tầng integration và e2e; test redelivery thật của PEL sau khi consumer chết (do `pkg/messaging` giữ, `ClaimIdle` mặc định 30s, `messaging.New` không cho chỉnh).
