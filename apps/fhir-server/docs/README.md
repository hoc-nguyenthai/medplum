# fhir-server: tài liệu

| Tài liệu | Nội dung |
|---|---|
| [api-contract.md](api-contract.md) | **Hợp đồng API**: xác thực, định dạng, bảng route (sinh từ swagger, có test chống lệch) |
| [api-contract/](api-contract/) | Tài liệu chi tiết của hợp đồng: `fhir-rest.md` (CRUD, search, history, batch, GraphQL), `fhir-operations.md` (113 operation), `fhir-server-go.md` (DTO và ví dụ JSON phần đã cài) |
| [architecture.md](architecture.md) | Làm gì, không làm gì, vì sao tách, cấu trúc và thư viện, dữ liệu, xác thực, luồng chính, hành vi khi lỗi |
| [config.md](config.md) | Mọi biến môi trường, mặc định, ý nghĩa và ràng buộc ở staging/production (**sinh từ mã**, có test chống lỗi thời) |
| [testing.md](testing.md) | Các tầng test, cách chạy, yêu cầu Docker |
| [runbook.md](runbook.md) | **Vận hành** (tự chứa): sức khỏe, tắt êm, migrate, cơ sở dữ liệu (schema `fhir`, role), cấu hình production, triển khai lần đầu, smoke test, xoay khóa, job nền, Subscription, sao lưu, sự cố |
| `swagger.json`, `swagger.yaml`, `docs.go` | OpenAPI do swaggo sinh (`make swagger APP=fhir-server`); xem trực tiếp ở `/swagger/index.html` khi không phải staging/production |
| [../README.md](../README.md) | Endpoint, operation, Subscription, xác thực, cấu hình, Binary |
