# fhir-downloader: tài liệu

| Tệp | Nội dung |
|---|---|
| [architecture.md](architecture.md) | Vì sao tách (điều kiện A, B, C, D và bằng chứng), luồng xử lý, độ bền, consumer group, ranh giới với `fhir-server` |
| [config.md](config.md) | Biến môi trường, sinh từ `internal/config/config.go` (test `TestConfigDocIsUpToDate` giữ cho khớp) |
| [testing.md](testing.md) | Bốn tầng test, lệnh chạy, điều mỗi tầng kiểm |
| [runbook.md](runbook.md) | Bật service thay cho worker trong API, sự cố thường gặp, định cỡ |

Nghiệp vụ tải attachment (luật BR-66..71, kịch bản AT-65..73) mô tả ở [fhir-server F-28](../../fhir-server/docs/business/04-chuc-nang.md); service này chạy đúng các luật đó trong tiến trình riêng. Kế hoạch tách: [docs/plans/fhir-server-split-candidates.md](../../../docs/plans/fhir-server-split-candidates.md).
