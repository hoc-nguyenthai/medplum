# @medplum/health-gorilla-react

> Component React cho Health Gorilla.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/health-gorilla-react` |
| Đường dẫn đích | `libs/medplum-health-gorilla-react` |
| Số file nguồn (không tính test) | 9 |
| Thư mục `src/` | `HealthGorillaLabOrderContext.ts`, `HealthGorillaLabOrderProvider.tsx`, `autocomplete-endpoint.test.ts`, `autocomplete-endpoint.ts`, `index.test.ts`, `index.ts`, `test-utils.ts`, `test.setup.ts`, `useHealthGorillaLabOrder.test.tsx`, `useHealthGorillaLabOrder.ts`, `useHealthGorillaLabOrderContext.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`fhirtypes`](fhirtypes.md), [`health-gorilla-core`](health-gorilla-core.md), [`react`](react.md)
- Chỉ dev: [`definitions`](definitions.md), [`mock`](mock.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/health-gorilla-react`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/health-gorilla-react` phải xanh.
