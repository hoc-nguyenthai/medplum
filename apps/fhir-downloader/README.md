# fhir-downloader

Service nền tự tải **attachment có URL ngoài** về kho Binary: nghe sự kiện ghi resource do `fhir-server` phát lên Redis, tải tệp qua `pkg/safehttp` (chặn địa chỉ nội bộ), ghi `Binary` vào đúng project rồi đổi `url` của attachment thành `Binary/<id>`. Không có route nghiệp vụ; cổng `:8009` chỉ có probe.

Đây là bước S2 của [kế hoạch tách fhir-server](../../docs/plans/fhir-server-split-candidates.md). Nghiệp vụ nằm ở `pkg/fhirdownload` (dùng chung với `fhir-server` khi chạy cùng tiến trình); app này chỉ lắp ráp (config, DB, kho Binary, broker Redis) và vận hành.

## Vì sao tách

Tóm tắt (đầy đủ ở [docs/architecture.md](docs/architecture.md)): tải URL do người dùng cung cấp là bề mặt SSRF và là đường ra Internet. Tách ra cho phép `fhir-server` chạy **không có quyền ra Internet** (chính sách mạng chỉ mở cho service này) và cô lập sự cố, thời gian và băng thông của máy chủ ngoài khỏi API. Không đạt điều kiện B (dùng chung schema `fhir`), điều kiện A chưa có số đo.

## Chạy

```
fhir-downloader               chạy worker và cổng probe (:8009)
fhir-downloader healthcheck   GET /healthz (HEALTHCHECK của Docker)
```

Yêu cầu: schema `fhir` đã được `fhir-server migrate up`; Redis mà `fhir-server` phát sự kiện lên (`BROKER_DRIVER=redis`); kho Binary **trùng** `FHIR_BINARY_STORAGE` của `fhir-server`. Phía `fhir-server` đặt `FHIR_AUTO_DOWNLOAD=true` và `FHIR_AUTO_DOWNLOAD_WORKER=false` (API chỉ phát sự kiện, không tải).

```sh
make run                 # cd apps/fhir-downloader && make run  (đọc .env)
make image               # image trivita-fhir-downloader:local
make test-unit           # không cần Docker
make test-integration    # PostgreSQL và Redis thật (cần Docker)
make test-service        # e2e bằng image dựng từ Dockerfile (cần Docker)
```

Cấu hình: [docs/config.md](docs/config.md) (sinh từ mã), mẫu `.env.example`. Biến dùng chung với `fhir-server` giữ nguyên tên để dùng chung một tệp môi trường.

## Tài liệu

| Tệp | Nội dung |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Vì sao tách, luồng xử lý, độ bền và idempotency, consumer group, đánh đổi |
| [docs/config.md](docs/config.md) | Biến môi trường (sinh từ mã, có test chống lỗi thời) |
| [docs/testing.md](docs/testing.md) | Bốn tầng test và cách chạy |
| [docs/runbook.md](docs/runbook.md) | Chuyển từ tải trong API sang service riêng, sự cố thường gặp, vận hành |
