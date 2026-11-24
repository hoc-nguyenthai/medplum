# fhir-notifier: tài liệu

| Tệp | Nội dung |
|---|---|
| [architecture.md](architecture.md) | Vì sao tách (điều kiện A, B, C, D và bằng chứng), luồng, độ bền của việc giao rest-hook, websocket, consumer group |
| [config.md](config.md) | Biến môi trường, sinh từ `internal/config/config.go` (test `TestConfigDocIsUpToDate` giữ cho khớp) |
| [testing.md](testing.md) | Bốn tầng test, lệnh chạy, đột biến đã chạy |
| [runbook.md](runbook.md) | Bật service thay cho phần giao trong API, sự cố thường gặp, định cỡ |

Nghiệp vụ Subscription (rest-hook, websocket, tự tắt) mô tả ở [fhir-server F-21, F-22](../../fhir-server/docs/business/04-chuc-nang.md); service này chạy đúng các luật đó trong tiến trình riêng, thêm giao bền. Kế hoạch tách: [docs/plans/fhir-server-split-candidates.md](../../../docs/plans/fhir-server-split-candidates.md).
