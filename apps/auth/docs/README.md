# auth: tài liệu

| Tài liệu | Nội dung |
|---|---|
| [api-contract.md](api-contract.md) | **Hợp đồng API** của auth: xác thực, định dạng lỗi, route có điều kiện, bảng route (sinh từ swagger, có test chống lệch) |
| [architecture.md](architecture.md) | Làm gì, không làm gì, vì sao tách, các tầng và adapter, dữ liệu, bí mật, luồng đăng nhập, hành vi khi lỗi |
| [config.md](config.md) | Mọi biến môi trường, mặc định, ý nghĩa và ràng buộc ở staging/production (**sinh từ mã**, có test chống lỗi thời) |
| [testing.md](testing.md) | Các tầng test, cách chạy, yêu cầu Docker |
| `swagger.json`, `swagger.yaml`, `docs.go` | OpenAPI do swaggo sinh (`make swagger APP=auth`); xem trực tiếp ở `/swagger/index.html` khi không phải staging/production |
| [../README.md](../README.md) | Route, lệnh quản trị `auth ...`, header bảo mật, hạn mức, token và tích hợp `fhir-server` |
| [api-contract/](api-contract/) | Tài liệu chi tiết của hợp đồng: `auth.md` (đăng ký, đăng nhập, MFA, mật khẩu), `auth-dto.md` (DTO `/auth`), `oauth.md` (OAuth2/OIDC), `admin.md` (quản trị project, SCIM), `email.md` |
| [runbook.md](runbook.md) | **Vận hành** (tự chứa): sức khỏe, tắt êm, migrate, cơ sở dữ liệu (schema `identity`, role, `DATABASE_REQUIRE_SCHEMA`), cấu hình production, triển khai lần đầu, smoke test, xoay khóa, sao lưu, sự cố |
| [plans/](plans/README.md) | **Kế hoạch** port từ Medplum theo pha (P0 đến P8) và quyết định (`apps-auth.md`) |
