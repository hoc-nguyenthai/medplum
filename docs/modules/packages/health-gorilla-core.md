# @medplum/health-gorilla-core

> SDK tích hợp Health Gorilla (xét nghiệm, hình ảnh): client, kiểu.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/health-gorilla-core` |
| Đường dẫn đích | `libs/medplum-health-gorilla-core` |
| Số file nguồn (không tính test) | 7 |
| Thư mục `src/` | `__test__`, `aoe.test.ts`, `aoe.ts`, `constants.ts`, `index.test.ts`, `index.ts`, `lab-order.test.ts`, `lab-order.ts`, `test-utils.ts`, `types.test.ts`, `types.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt C |

## Phụ thuộc

- Runtime: [`core`](core.md), [`fhirtypes`](fhirtypes.md)
- Chỉ dev: [`definitions`](definitions.md), [`mock`](mock.md)
- Được dùng bởi: [`health-gorilla-react`](health-gorilla-react.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/health-gorilla-core`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/health-gorilla-core` phải xanh.
