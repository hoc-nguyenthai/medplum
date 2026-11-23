# @medplum/definitions

> Dữ liệu định nghĩa FHIR dạng JSON (StructureDefinition, SearchParameter, ValueSet) để core, server và mock nạp.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/definitions` |
| Đường dẫn đích | `libs/medplum-definitions` |
| Số file nguồn (không tính test) | 2 |
| Thư mục `src/` | `fhir`, `index.test.ts`, `index.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt A |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: [`bot-layer`](bot-layer.md), [`fhir-router`](fhir-router.md), [`mock`](mock.md), [`server`](server.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/definitions`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/definitions` phải xanh.
