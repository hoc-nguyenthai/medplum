# auth: tài liệu

| Tài liệu | Nội dung |
|---|---|
| [business/](business/README.md) | **Tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)**: tổng quan và tác nhân, luồng nghiệp vụ đầu-cuối, luồng người dùng, danh mục chức năng F-01..F-29, luật BR-xx, phân quyền, vòng đời trạng thái, kịch bản chấp nhận |
| [api-contract.md](api-contract.md) | **Hợp đồng API** của auth: xác thực, định dạng lỗi, route có điều kiện, bảng route (sinh từ swagger, có test chống lệch) |
| [api-contract/common.md](api-contract/common.md) · [reference/](api-contract/reference/README.md) · [cases/](api-contract/cases/README.md) | **Hợp đồng API chi tiết từng route**: quy ước chung (header, cookie, envelope lỗi, hạn mức); `reference/` bảng từng trường, header, tham số, JSON mẫu (**sinh từ swagger**, có test); `cases/` từng trường hợp phản hồi (điều kiện, HTTP, header, thông điệp lỗi nguyên văn, ví dụ; có test phủ route) |
| [erd/](erd/README.md) | **ERD và từ điển dữ liệu**: kho lưu trữ (schema `identity`, Redis, khóa), 10 loại resource với từng trường và mức nhạy cảm (kể cả secret nào lưu bản rõ), bảng quan hệ và hành vi khi xóa, khóa Redis và TTL. Bảng vật lý dùng chung ở [../../../docs/erd/fhirstore-physical.md](../../../docs/erd/fhirstore-physical.md) |
| [architecture.md](architecture.md) | Làm gì, không làm gì, vì sao tách, các tầng và adapter, dữ liệu, bí mật, luồng đăng nhập, hành vi khi lỗi |
| [config.md](config.md) | Mọi biến môi trường, mặc định, ý nghĩa và ràng buộc ở staging/production (**sinh từ mã**, có test chống lỗi thời) |
| [testing.md](testing.md) | Các tầng test, cách chạy, yêu cầu Docker |
| `swagger.json`, `swagger.yaml`, `docs.go` | OpenAPI do swaggo sinh (`make swagger APP=auth`); xem trực tiếp ở `/swagger/index.html` khi không phải staging/production |
| [../README.md](../README.md) | Route, lệnh quản trị `auth ...`, header bảo mật, hạn mức, token và tích hợp `fhir-server` |
| [api-contract/](api-contract/) | Tài liệu chi tiết của hợp đồng: `auth.md` (đăng ký, đăng nhập, MFA, mật khẩu), `auth-dto.md` (DTO `/auth`), `oauth.md` (OAuth2/OIDC), `admin.md` (quản trị project, SCIM), `email.md` |
| [runbook.md](runbook.md) | **Vận hành** (tự chứa): sức khỏe, tắt êm, migrate, cơ sở dữ liệu (schema `identity`, role, `DATABASE_REQUIRE_SCHEMA`), cấu hình production, triển khai lần đầu, smoke test, xoay khóa, sao lưu, sự cố |
| [plans/](plans/README.md) | **Kế hoạch** port từ Medplum theo pha (P0 đến P8) và quyết định (`apps-auth.md`) |
