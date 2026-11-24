# fhir-notifier: kiểm thử

Bốn tầng theo [service-independence.md](../../../docs/plans/service-independence.md); mẫu `apps/ws-gateway`.

| Tầng | Vị trí | Cần | Lệnh | Kiểm gì |
|---|---|---|---|---|
| Đơn vị | `internal/**/*_test.go` | không | `make test-unit` | cấu hình (từ chối sai, gom mọi lỗi, dùng chung tệp môi trường với `fhir-server`, tài liệu khớp mã), probe, lắp ráp trên miniredis và SQLite tệp: giao theo sự kiện, tách theo project, thử lại tại chỗ rồi bỏ cuộc, group riêng nhận mọi sự kiện, giao sự kiện phát lúc service chưa chạy, replica khác giao nốt phần dở dang, tự tắt Subscription, websocket (phát lên kênh chỉ khi có gateway), từ chối schema chưa migrate, readiness, Redis chết, tắt êm |
| Tích hợp | `tests/integration` | Docker | `make test-integration` | PostgreSQL thật với role hẹp quyền và `DATABASE_REQUIRE_SCHEMA=true`; nhận lại message chưa ACK bằng XCLAIM trên Redis thật; Redis chết rồi sống lại |
| e2e | `tests/e2e` | Docker | `make test-service` | dựng image từ Dockerfile (root filesystem chỉ đọc), chạy cùng PostgreSQL và Redis thật: healthcheck, giao đầu-cuối, chặn địa chỉ nội bộ mặc định, SIGTERM thoát mã 0, chạy lại giao nốt sự kiện ghi lúc dừng, từ chối cấu hình sai và schema chưa migrate |
| Tải | chưa có | | | điều kiện A chưa có số đo (xem architecture.md) |

Phía `fhir-server` được mô phỏng trong test bằng `pkg/fhirstore` + `pkg/fhirsub` (ghi resource, phát sự kiện lên Redis), vì app không được import app khác (C-1, C-7). Test của `pkg/fhirsub` (`durable_test.go`) và `pkg/messaging` (`TestRedisConcurrencyDoesNotBlockBehindSlowHandler`) kiểm phần lõi giao bền. e2e cần `host.docker.internal` (Docker Desktop, OrbStack); trên Linux phải thêm `--add-host`. Docker của OrbStack mặc định tắt: `orbctl start`, và `export DOCKER_HOST=unix://$HOME/.orbstack/run/docker.sock`.

## Đột biến đã chạy (2026-11-24)

Mỗi thay đổi dưới đây làm test đỏ (bắt đúng test nêu tên). Phần lõi (`pkg/fhirsub`):

| Đột biến | Test bắt |
|---|---|
| bỏ qua việc giao dở dang khi ctx hủy (vẫn trả nil) | `TestDurableRedeliversAfterTheDeliveringProcessStops` |
| coi lần giao bị hủy là đã xong | `TestDurableRedeliversAfterTheDeliveringProcessStops` |
| không thử lại tại chỗ | `TestDurableDeliversInsideTheHandlerAndRetriesInPlace`, `TestDurableStopsRetryingAfterMaxAttempts` |
| không dừng khi hết `MaxAttempts` | `TestDurableStopsRetryingAfterMaxAttempts` |
| giao các Subscription của một sự kiện tuần tự | `TestDurableSlowWebhookDoesNotBlockOthers` (sau khi sửa test: ban đầu đột biến này sống sót vì thứ tự id vô tình có lợi) |

Phần lắp ráp (`internal/bootstrap`):

| Đột biến | Test bắt |
|---|---|
| group là `trivita` | `TestEachConsumerGroupReceivesEveryEvent` |
| không giao bền (`Durable` tắt) | `TestAnotherReplicaDeliversWhatTheStoppedOneLeftUnfinished` |
| bỏ `AttachRemote` (websocket) | `TestWebsocketIssuerPublishesToChannelOnlyWhenAGatewayListens` |
| bỏ `ClaimIdle` tính theo chuỗi giao (về mặc định 30s) | `TestAnotherReplicaDeliversWhatTheStoppedOneLeftUnfinished` |
| bỏ `Concurrency` của broker | `TestSlowWebhookDoesNotBlockOtherEvents` (sau khi bổ sung test: ban đầu đột biến này sống sót) |

Chưa chạy: đột biến cho tầng integration và e2e; đo thời gian xử lý khi nhiều Subscription chậm.
