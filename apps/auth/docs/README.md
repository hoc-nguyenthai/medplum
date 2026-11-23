# auth: tài liệu

| Tài liệu | Nội dung |
|---|---|
| [api-contract.md](api-contract.md) | **Hợp đồng API** của auth: xác thực, định dạng lỗi, route có điều kiện, bảng route (sinh từ swagger, có test chống lệch) |
| [architecture.md](architecture.md) | Làm gì, không làm gì, vì sao tách, các tầng và adapter, dữ liệu, bí mật, luồng đăng nhập, hành vi khi lỗi |
| [config.md](config.md) | Mọi biến môi trường, mặc định, ý nghĩa và ràng buộc ở staging/production (**sinh từ mã**, có test chống lỗi thời) |
| [testing.md](testing.md) | Các tầng test, cách chạy, yêu cầu Docker |
| `swagger.json`, `swagger.yaml`, `docs.go` | OpenAPI do swaggo sinh (`make swagger APP=auth`); xem trực tiếp ở `/swagger/index.html` khi không phải staging/production |
| [../README.md](../README.md) | Route, lệnh quản trị `auth ...`, header bảo mật, hạn mức, token và tích hợp `fhir-server` |
| [../../../docs/api-contract/](../../../docs/api-contract/) | Hợp đồng API (`auth.md`, `auth-dto.md`, `oauth.md`, `admin.md`, `others.md`) |
| [../../../docs/plans/auth/](../../../docs/plans/auth/) | Kế hoạch port từ Medplum theo pha |
| [../../../docs/runbook.md](../../../docs/runbook.md) | Mục 12: schema `identity`, role `identity_app`/`identity_migrate`, xử lý lỗi |
