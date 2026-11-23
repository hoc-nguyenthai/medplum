# create-medplum

> Trình khởi tạo `npm create medplum`.

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/create-medplum` |
| Đường dẫn đích | `tools/medplum-create-medplum` |
| Số file nguồn (không tính test) | 2 |
| Thư mục `src/` | `main.test.ts`, `main.ts` |
| Entry point | `—` |
| Lệnh CLI | `medplum` |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: —
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `create-medplum`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=create-medplum` phải xanh.
