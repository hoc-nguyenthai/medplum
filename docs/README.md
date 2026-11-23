# Tài liệu medplum trong monorepo Trivita

Bộ tài liệu phục vụ việc port backend medplum sang Go (xem go-port-status.md). `modules/`, `features/`, `diagrams/`, `api-contract/` là đặc tả nguồn; `migration-plan.md` là kế hoạch giữ TypeScript cũ, đã bị thay thế.

| Tài liệu | Nội dung |
|---|---|
| [service-principles.md](service-principles.md) | **Nguyên tắc tách service và triển khai microservice** (khi nào tách, quy tắc code, dữ liệu, giao tiếp, hợp đồng triển khai, danh sách kiểm cho service mới) |
| [go-port-status.md](go-port-status.md) | Tiến độ port sang Go (hướng đi hiện tại) |
| [migration-plan.md](migration-plan.md) | (Đã bị thay thế) Kế hoạch di chuyển giữ TypeScript |
| [modules/README.md](modules/README.md) | 30 package và 28 module của `server/src`: vai trò, phụ thuộc, đường dẫn đích |
| [features/README.md](features/README.md) | 12 tính năng, module liên quan, endpoint, ghi chú |
| [diagrams/business-flow.md](diagrams/business-flow.md) | Sơ đồ luồng nghiệp vụ và kiến trúc (Mermaid) |
| [diagrams/user-flow.md](diagrams/user-flow.md) | Sơ đồ luồng người dùng (Mermaid) |
| [api-contract/README.md](api-contract/README.md) | Hợp đồng API: FHIR REST, operations, auth, OAuth, admin, khác |

## Nguồn và độ tin cậy

- `modules/` và `api-contract/` (các bảng route) **sinh tự động từ mã nguồn** lúc 2026-11-20 (medplum trong `medplum/packages`). Số liệu phụ thuộc là số câu lệnh import giữa các module, không tính test.
- `features/` và `diagrams/` viết tay, chỉ nêu điều đã đối chiếu với mã. Chỗ nào mô tả khái quát hoặc chưa kiểm chứng đều ghi chú ngay tại đó.
- Chi tiết request và response đầy đủ lấy từ server đang chạy: `GET /fhir/R4/metadata`, `GET /.well-known/api-catalog`, OpenAPI sinh từ `server/src/openapi.ts`.
- Sau khi di chuyển, đường dẫn `medplum/packages/...` trong tài liệu chuyển thành đường dẫn ở cột "Đích".
