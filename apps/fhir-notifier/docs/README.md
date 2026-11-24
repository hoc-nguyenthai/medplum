# fhir-notifier: tài liệu

| Tệp | Nội dung |
|---|---|
| [architecture.md](architecture.md) | Vì sao tách (điều kiện A, B, C, D và bằng chứng), luồng, độ bền của việc giao rest-hook, websocket, consumer group |
| [api-contract.md](api-contract.md) | Hợp đồng API: không có route nghiệp vụ; 4 probe (thân, header, mã), lệnh `healthcheck`, hợp đồng dây (sự kiện, webhook gửi ra, kênh websocket); ghim bằng `TestProbeContractIsPinned` |
| [business/](business/README.md) | **Tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)**: 6 luồng nghiệp vụ, luồng người dùng, 9 chức năng F-01..F-09, 21 luật BR-xx, 19 kịch bản chấp nhận, truy vết test, điểm lệch và rủi ro (giao ít nhất một lần, bỏ cuộc không lưu, Subscription lỗi bị bỏ qua âm thầm, nội dung không lọc theo quyền người tạo) |
| [erd/](erd/README.md) | **ERD và từ điển dữ liệu**: không có bảng riêng (schema `fhir`, role `fhir_app`, không migrate); `Subscription` từng trường (secret và header lưu rõ), chỉ sửa `status`/`error` khi tự tắt; Redis: stream và nhóm `fhir-notifier` (PEL, claim, dead-letter), khóa đếm thất bại `trivita:sub:failures:<id>`, kênh `fhirws.sub.<id>` và sổ hiện diện `fhirws.bound.<id>` (chỉ đọc); bí mật nào lưu thế nào |
| [config.md](config.md) | Biến môi trường, sinh từ `internal/config/config.go` (test `TestConfigDocIsUpToDate` giữ cho khớp) |
| [testing.md](testing.md) | Bốn tầng test, lệnh chạy, đột biến đã chạy |
| [runbook.md](runbook.md) | Bật service thay cho phần giao trong API, sự cố thường gặp, định cỡ |

Nghiệp vụ Subscription (rest-hook, websocket, tự tắt) mô tả ở [fhir-server F-21, F-22](../../fhir-server/docs/business/04-chuc-nang.md); service này chạy đúng các luật đó trong tiến trình riêng, thêm giao bền. Kế hoạch tách: [docs/plans/fhir-server-split-candidates.md](../../../docs/plans/fhir-server-split-candidates.md).
