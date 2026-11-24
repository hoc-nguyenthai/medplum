# fhir-worker: kiểm thử

Bốn tầng theo [service-independence.md](../../../docs/plans/service-independence.md); mẫu `apps/ws-gateway`.

| Tầng | Vị trí | Cần | Lệnh | Kiểm gì |
|---|---|---|---|---|
| Đơn vị | `internal/**/*_test.go` | không | `make test-unit` | cấu hình (từ chối sai, khóa ký job suy từ dev HMAC trùng `fhir-server`, dùng chung tệp môi trường), probe, lắp ráp trên SQLite tệp và miniredis: chạy job do API tạo mà không chạy (DisableJobExecution), tách project theo chữ ký, từ chối chữ ký sai, hai worker chạy mỗi job một lần, kết quả ra kho và Expunge khi hết hạn, batch async ghi resource và phát sự kiện, từ chối schema chưa migrate, readiness, tắt êm |
| Tích hợp | `tests/integration` | Docker | `make test-integration` | PostgreSQL thật với role hẹp quyền và `DATABASE_REQUIRE_SCHEMA=true`; ba worker tranh sáu job trên PostgreSQL thật, mỗi job chạy đúng một lần |
| e2e | `tests/e2e` | Docker | `make test-service` | dựng image từ Dockerfile (root filesystem chỉ đọc), chạy cùng PostgreSQL thật: healthcheck, job có chữ ký chạy đúng project, SIGTERM thoát mã 0, chạy lại thì chạy nốt job tạo lúc dừng, từ chối cấu hình sai và schema chưa migrate |
| Tải | chưa có | | | chưa đo thông lượng riêng của worker (xem architecture.md) |

Phía `fhir-server` được mô phỏng trong test bằng một `fhirrouter.Router` thật với `DisableJobExecution=true` trên cùng schema (ký quyền bằng `pkg/fhirauth`), vì app không được import app khác (C-1, C-7). Test của `pkg/fhirrouter` kiểm lease, nhả lease khi tắt, tiếp quản job mồ côi (`jobs_durable_test.go`). Test P0-2 ở phía API: `TestExportResultsGoToObjectStorageAndAreExpungedOnExpiry` (`apps/fhir-server/internal/bootstrap/split_e2e_test.go`). e2e cần `host.docker.internal` (Docker Desktop, OrbStack). Docker của OrbStack mặc định tắt: `orbctl start`, và `export DOCKER_HOST=unix://$HOME/.orbstack/run/docker.sock`.

## Đột biến đã chạy (2026-11-24)

Mỗi thay đổi dưới đây làm test đỏ (bắt đúng test nêu tên):

| Đột biến | Test bắt |
|---|---|
| không kiểm chữ ký job (chấp nhận mọi job) | `TestJobWithAnInvalidSealIsRejected` |
| job không chạy trên kho tách project | `TestJobsRunWithTheCallersProjectOnly` |
| file kết quả không đi qua kho đối tượng | `TestResultsGoToObjectStorageAndAreExpungedWhenExpired` |
| không phát sự kiện ghi resource | `TestBatchAsyncWritesResourcesAndPublishesEvents` |
| bỏ `JobRecoveryGrace` (về mặc định 30 giây của router) | `TestRunsJobsThatTheAPICreatedButDoesNotRun`, `TestJobsRunWithTheCallersProjectOnly` |
| P0-2: job không qua kho đối tượng ở `fhir-server` | `TestExportResultsGoToObjectStorageAndAreExpungedOnExpiry` |
| P0-2: xóa kết quả bằng xóa mềm thay vì Expunge | `TestExportResultsGoToObjectStorageAndAreExpungedOnExpiry` |

Chưa chạy: đột biến cho tầng integration và e2e; test worker chết giữa lúc chạy job dài rồi worker khác tiếp quản (phần lõi do `pkg/fhirrouter` giữ); đo tải.
