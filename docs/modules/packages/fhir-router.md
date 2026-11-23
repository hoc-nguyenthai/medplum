# @medplum/fhir-router

> Router FHIR độc lập framework: ánh xạ REST (read, search, create, update, patch, delete, history, batch, GraphQL) sang interface Repository. Server và mock cùng dùng.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/fhir-router` |
| Đường dẫn đích | `libs/medplum-fhir-router` |
| Số file nguồn (không tính test) | 12 |
| Thư mục `src/` | `batch.test.ts`, `batch.ts`, `fhirrouter.test.ts`, `fhirrouter.ts`, `graphql`, `index.ts`, `repo.test.ts`, `repo.ts`, `search-include.test.ts`, `search-include.ts`, `urlrouter.test.ts`, `urlrouter.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt B |

## Phụ thuộc

- Runtime: [`core`](core.md), [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md)
- Chỉ dev: —
- Được dùng bởi: [`mock`](mock.md), [`server`](server.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/fhir-router`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/fhir-router` phải xanh.
