# @medplum/generator

> Sinh mã: fhirtypes, definitions, search parameter từ đặc tả FHIR.

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/generator` |
| Đường dẫn đích | `tools/medplum-generator` |
| Số file nguồn (không tính test) | 8 |
| Thư mục `src/` | `baseschema.ts`, `compare.ts`, `data`, `fhirpath`, `index.test.ts`, `index.ts`, `jsonschema.ts`, `mockclient.ts`, `umls.ts`, `valuesets.ts` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md), [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/generator`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/generator` phải xanh.
