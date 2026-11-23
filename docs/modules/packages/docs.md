# @medplum/docs

> Trang tài liệu Docusaurus (nội dung của Medplum).

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/docs` |
| Đường dẫn đích | `tools/medplum-docs` |
| Số file nguồn (không tính test) | 0 |
| Thư mục `src/` | — |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `dev` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/docs`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/docs` phải xanh.
