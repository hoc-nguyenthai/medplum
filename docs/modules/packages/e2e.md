# @medplum/e2e

> Test end-to-end (Playwright).

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/e2e` |
| Đường dẫn đích | `tools/medplum-e2e` |
| Số file nguồn (không tính test) | 1 |
| Thư mục `src/` | — |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | — |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md), [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/e2e`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/e2e` phải xanh.
