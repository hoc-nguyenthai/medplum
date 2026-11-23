# API dữ liệu FHIR (CRUD, search, history, batch, GraphQL)

Lưu và truy xuất resource FHIR R4 theo REST chuẩn: tạo, đọc, sửa, vá, xóa, lịch sử phiên bản, tìm kiếm, batch/transaction, GraphQL.

## Module liên quan

- [`server/fhir`](../modules/server/fhir.md)
- [`server/migrations`](../modules/server/migrations.md)
- [`server/(root)`](../modules/server/root.md)
- [`fhir-router`](../modules/packages/fhir-router.md)
- [`core`](../modules/packages/core.md)
- [`definitions`](../modules/packages/definitions.md)
- [`fhirtypes`](../modules/packages/fhirtypes.md)

## API contract

- [fhir-rest.md](../api-contract/fhir-rest.md)

## Ghi chú kỹ thuật và di chuyển

- `fhir-router` tách phần định tuyến REST khỏi lưu trữ; server cài `Repository` trên Postgres, mock cài trên bộ nhớ.
- Repository (`server/src/fhir/repo.ts`) kiểm tra quyền, chạy pre-commit, kiểm tra tham chiếu, ghi DB trong transaction, cập nhật cache Redis, rồi đưa job nền vào hàng đợi.
- Bảng tra cứu (lookup tables) và cột token phục vụ search nằm trong `fhir/lookups` và `fhir/token-column.ts`.
- Điểm khó khi chuyển: `fhir` gọi `fhir/operations` 86 lần và ngược lại 96 lần (vòng phụ thuộc), nên hai module phải chuyển cùng nhau.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
