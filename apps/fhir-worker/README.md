# fhir-worker

Service nền chạy **job nền** của `fhir-server`: bulk export (`$export`), `$reindex` cả loại, batch async (`Prefer: respond-async`) và vòng bảo trì (tiếp quản job mồ côi, xóa kết quả export hết hạn). `fhir-server` (với `FHIR_JOB_EXECUTION=false`) chỉ nhận yêu cầu và tạo `AsyncJob` rồi trả `202`; `fhir-worker` nhận job qua vòng bảo trì và lease trong CSDL, chạy bằng **đúng quyền và project của người đã tạo job** (chữ ký job-seal). Không có route nghiệp vụ; cổng `:8011` chỉ có probe.

Đây là bước S5 của [kế hoạch tách fhir-server](../../docs/plans/fhir-server-split-candidates.md) (gồm P0-2: kết quả job ra kho đối tượng). Nghiệp vụ job nằm ở `pkg/fhirrouter` (dùng chung với `fhir-server` khi chạy cùng tiến trình); app này chỉ lắp ráp (config, DB, kho Binary, broker phát sự kiện, Router chạy job) và vận hành.

## Vì sao tách

Tóm tắt (đầy đủ ở [docs/architecture.md](docs/architecture.md)): **không phải vì điều kiện A đã được chứng minh.** Đo S4 cho thấy một export không làm chậm API ở 200.000 resource; bốn export đồng thời làm API chậm sát ngưỡng (p99 x1,48, thông lượng x0,76) và RSS server lên 395 MB. Lý do tách là vận hành: cô lập bộ nhớ đỉnh và tải của nhiều export khỏi API, scale job độc lập, và deploy API không làm dừng job đang chạy.

## Chạy

```
fhir-worker               chạy bộ chạy job và cổng probe (:8011)
fhir-worker healthcheck   GET /healthz (HEALTHCHECK của Docker)
```

Yêu cầu: schema `fhir` đã được `fhir-server migrate up`; **cùng** `FHIR_BASE_URL`, `FHIR_BINARY_STORAGE` (và `FHIR_S3_*`), `FHIR_JOB_SIGNING_KEY`, `FHIR_JWT_ISSUER` (chỉ để biết có bật JWT), `FHIR_SKIP_VALIDATION`, `FHIR_SKIP_CONSTRAINTS`, `FHIR_UPDATE_AS_CREATE`, `FHIR_BASE64_BINARY_MAX_BYTES` với `fhir-server`; nếu `fhir-server` bật Subscription hoặc tự tải thì cả `BROKER_DRIVER=redis` và `REDIS_URL` (batch async ghi resource nên phải phát sự kiện). Phía `fhir-server` đặt `FHIR_JOB_EXECUTION=false`.

```sh
make run                 # cd apps/fhir-worker && make run  (đọc .env)
make image               # image trivita-fhir-worker:local
make test-unit           # không cần Docker
make test-integration    # PostgreSQL thật (cần Docker)
make test-service        # e2e bằng image dựng từ Dockerfile (cần Docker)
```

Cấu hình: [docs/config.md](docs/config.md) (sinh từ mã), mẫu `.env.example`. Biến dùng chung với `fhir-server` giữ nguyên tên để dùng chung một tệp môi trường.

## Tài liệu

| Tệp | Nội dung |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Vì sao tách, cách nhận job, quyền của job, kết quả ra kho, độ trễ và đánh đổi |
| [docs/config.md](docs/config.md) | Biến môi trường (sinh từ mã, có test chống lỗi thời) |
| [docs/testing.md](docs/testing.md) | Bốn tầng test, đột biến đã chạy |
| [docs/runbook.md](docs/runbook.md) | Bật service thay cho bộ chạy job trong API, sự cố thường gặp, định cỡ |
