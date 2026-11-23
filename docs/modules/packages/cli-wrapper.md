# medplum

> Vỏ mỏng của CLI, phát hành với tên package `medplum`.

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/cli-wrapper` |
| Đường dẫn đích | `apps/medplum-cli-wrapper` |
| Số file nguồn (không tính test) | 1 |
| Thư mục `src/` | `index.ts` |
| Entry point | `—` |
| Lệnh CLI | `medplum` |
| Script npm | `build`, `lint` |
| Thứ tự di chuyển | Bước 4, thứ tự 5/7 |

## Phụ thuộc

- Runtime: [`cli`](cli.md), [`core`](core.md)
- Chỉ dev: —
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `medplum`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=medplum` phải xanh.
