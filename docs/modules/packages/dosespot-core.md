# @medplum/dosespot-core

> SDK tích hợp DoseSpot (kê đơn điện tử): client, kiểu, hằng số.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/dosespot-core` |
| Đường dẫn đích | `libs/medplum-dosespot-core` |
| Số file nguồn (không tính test) | 3 |
| Thư mục `src/` | `index.ts`, `pharmacy-utils.test.ts`, `pharmacy-utils.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`fhirtypes`](fhirtypes.md)
- Chỉ dev: [`definitions`](definitions.md), [`mock`](mock.md)
- Được dùng bởi: [`dosespot-react`](dosespot-react.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/dosespot-core`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/dosespot-core` phải xanh.
