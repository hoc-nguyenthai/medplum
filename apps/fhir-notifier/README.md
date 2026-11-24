# fhir-notifier

Service nền giao **thông báo Subscription**: nghe sự kiện ghi resource do `fhir-server` phát lên Redis, khớp với các Subscription đang hoạt động, giao **rest-hook** qua `pkg/safehttp` (chặn địa chỉ nội bộ) **bền** (đồng bộ trong handler của Redis Stream, ACK sau khi giao xong hoặc bỏ cuộc, nên restart không mất thông báo) và, với `FHIR_WS_MODE=issuer`, phát sự kiện **websocket** lên kênh Redis cho `ws-gateway`. Không có route nghiệp vụ; cổng `:8010` chỉ có probe.

Đây là bước S3 của [kế hoạch tách fhir-server](../../docs/plans/fhir-server-split-candidates.md) (gồm P0-1: giao rest-hook bền). Nghiệp vụ khớp và giao nằm ở `pkg/fhirsub` (dùng chung với `fhir-server` khi chạy cùng tiến trình); app này chỉ lắp ráp (config, DB, broker Redis) và vận hành.

## Vì sao tách

Tóm tắt (đầy đủ ở [docs/architecture.md](docs/architecture.md)): webhook chậm hoặc chết chiếm worker, kết nối và bộ nhớ của API; khớp `criteria` mỗi sự kiện tốn CPU theo số Subscription; hàng đợi giao nằm trong bộ nhớ của API nên restart là mất thông báo. Tách ra thì chỉ service này gọi webhook ra ngoài, và việc giao được làm bền. Không đạt điều kiện B (dùng chung schema `fhir`), điều kiện A chưa có số đo.

## Chạy

```
fhir-notifier               chạy worker và cổng probe (:8010)
fhir-notifier healthcheck   GET /healthz (HEALTHCHECK của Docker)
```

Yêu cầu: schema `fhir` đã được `fhir-server migrate up`; Redis mà `fhir-server` phát sự kiện lên (`BROKER_DRIVER=redis`). Phía `fhir-server` đặt `FHIR_SUBSCRIPTIONS=true` và `FHIR_SUBSCRIPTION_DELIVERY=false` (API chỉ phát sự kiện, không giao).

```sh
make run                 # cd apps/fhir-notifier && make run  (đọc .env)
make image               # image trivita-fhir-notifier:local
make test-unit           # không cần Docker
make test-integration    # PostgreSQL và Redis thật (cần Docker)
make test-service        # e2e bằng image dựng từ Dockerfile (cần Docker)
```

Cấu hình: [docs/config.md](docs/config.md) (sinh từ mã), mẫu `.env.example`. Biến dùng chung với `fhir-server` giữ nguyên tên để dùng chung một tệp môi trường.

## Tài liệu

| Tệp | Nội dung |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Vì sao tách, luồng xử lý, độ bền và đánh đổi (giao ít nhất một lần, trùng, thứ tự), consumer group, websocket |
| [docs/config.md](docs/config.md) | Biến môi trường (sinh từ mã, có test chống lỗi thời) |
| [docs/testing.md](docs/testing.md) | Bốn tầng test, đột biến đã chạy |
| [docs/runbook.md](docs/runbook.md) | Chuyển từ giao trong API sang service riêng, sự cố thường gặp, định cỡ |
