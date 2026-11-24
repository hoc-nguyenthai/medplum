# fhir-downloader: tài liệu

| Tệp | Nội dung |
|---|---|
| [architecture.md](architecture.md) | Vì sao tách (điều kiện A, B, C, D và bằng chứng), luồng xử lý, độ bền, consumer group, ranh giới với `fhir-server` |
| [api-contract.md](api-contract.md) | Hợp đồng API: không có route nghiệp vụ; 4 probe (thân, header, mã), lệnh `healthcheck`, hợp đồng dây (sự kiện, Binary và cập nhật resource, gọi ra ngoài); ghim bằng `TestProbeContractIsPinned` |
| [business/](business/README.md) | **Tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)**: 5 luồng nghiệp vụ, luồng người dùng, 7 chức năng F-01..F-07, 19 luật BR-xx, 16 kịch bản chấp nhận, truy vết test, điểm lệch và rủi ro (mọi 4xx là lỗi tạm, dead-letter không có công cụ, không giới hạn số URL) |
| [erd/](erd/README.md) | **ERD và từ điển dữ liệu**: không có bảng riêng (dùng schema `fhir`, trỏ về `docs/erd/fhirstore-physical.md`), `Binary` do service ghi, resource nguồn có `Attachment`, quan hệ và toàn vẹn, Redis (nhóm `fhir-downloader`, PEL, dead-letter, không có khóa theo dõi thất bại), kho Binary, dữ liệu nhạy cảm và dữ liệu nằm lại (lịch sử, stream, mồ côi) |
| [config.md](config.md) | Biến môi trường, sinh từ `internal/config/config.go` (test `TestConfigDocIsUpToDate` giữ cho khớp) |
| [testing.md](testing.md) | Bốn tầng test, lệnh chạy, điều mỗi tầng kiểm |
| [runbook.md](runbook.md) | Bật service thay cho worker trong API, sự cố thường gặp, định cỡ |

Nghiệp vụ tải attachment (luật BR-66..71, kịch bản AT-65..73) mô tả ở [fhir-server F-28](../../fhir-server/docs/business/04-chuc-nang.md); service này chạy đúng các luật đó trong tiến trình riêng. Kế hoạch tách: [docs/plans/fhir-server-split-candidates.md](../../../docs/plans/fhir-server-split-candidates.md).
