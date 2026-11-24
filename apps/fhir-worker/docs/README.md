# fhir-worker: tài liệu

| Tệp | Nội dung |
|---|---|
| [architecture.md](architecture.md) | Vì sao tách (kể cả điều chưa chứng minh), luồng nhận và chạy job, quyền của job, kết quả ra kho đối tượng, đánh đổi |
| [config.md](config.md) | Biến môi trường, sinh từ `internal/config/config.go` (test `TestConfigDocIsUpToDate` giữ cho khớp) |
| [testing.md](testing.md) | Bốn tầng test, lệnh chạy, đột biến đã chạy |
| [runbook.md](runbook.md) | Bật service thay cho bộ chạy job trong API, sự cố thường gặp, định cỡ |

Nghiệp vụ job (bulk export F-17, `$reindex` F-18, job nền F-20) mô tả ở [fhir-server business](../../fhir-server/docs/business/04-chuc-nang.md); service này chạy đúng các luật đó trong tiến trình riêng. Kế hoạch tách: [docs/plans/fhir-server-split-candidates.md](../../../docs/plans/fhir-server-split-candidates.md).
