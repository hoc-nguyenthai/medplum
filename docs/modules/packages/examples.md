# @medplum/examples

> Mã ví dụ dùng trong tài liệu, có test.

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/examples` |
| Đường dẫn đích | `tools/medplum-examples` |
| Số file nguồn (không tính test) | 81 |
| Thư mục `src/` | `access`, `api`, `auth`, `billing`, `binaries`, `bots`, `careplans`, `charting`, `communications`, `deployment`, `fhir-datastore`, `graphql`, `medications`, `migration`, `provider`, `questionnaires`, `react`, `scheduling`, `search`, `terminology`, `tutorials` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md), [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md), [`mock`](mock.md), [`react`](react.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/examples`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/examples` phải xanh.
