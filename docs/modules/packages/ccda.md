# @medplum/ccda

> Chuyển đổi hai chiều giữa C-CDA và FHIR.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/ccda` |
| Đường dẫn đích | `libs/medplum-ccda` |
| Số file nguồn (không tính test) | 28 |
| Thư mục `src/` | `address.test.ts`, `b1.test.ts`, `ccda-to-fhir.ts`, `datetime.test.ts`, `datetime.ts`, `fhir-to-ccda`, `g9.test.ts`, `index.ts`, `medication-dose.test.ts`, `nullflavor.test.ts`, `oids.ts`, `roundtrip.test.ts`, `sanitize.test.ts`, `sanitize.ts`, `systems.test.ts`, `systems.ts`, `templates.ts`, `types.ts`, `xml.test.ts`, `xml.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt B |

## Phụ thuộc

- Runtime: [`core`](core.md)
- Chỉ dev: [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: [`bot-layer`](bot-layer.md), [`server`](server.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/ccda`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/ccda` phải xanh.
