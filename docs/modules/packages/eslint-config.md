# @medplum/eslint-config

> Cấu hình ESLint dùng chung.

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/eslint-config` |
| Đường dẫn đích | `tools/medplum-eslint-config` |
| Số file nguồn (không tính test) | 0 |
| Thư mục `src/` | — |
| Entry point | `index.mjs` |
| Lệnh CLI | — |
| Script npm | `lint`, `test` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: —
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/eslint-config`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/eslint-config` phải xanh.
