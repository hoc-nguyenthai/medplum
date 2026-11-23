# @medplum/bot-layer

> Lambda layer chứa thư viện cho bot chạy trên AWS Lambda (đóng gói core, ccda, definitions).

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/bot-layer` |
| Đường dẫn đích | `apps/medplum-bot-layer` |
| Số file nguồn (không tính test) | 0 |
| Thư mục `src/` | — |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | — |
| Thứ tự di chuyển | Bước 4, thứ tự 1/7 |

## Phụ thuộc

- Runtime: [`ccda`](ccda.md), [`core`](core.md), [`definitions`](definitions.md)
- Chỉ dev: —
- Được dùng bởi: [`server`](server.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/bot-layer`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/bot-layer` phải xanh.
