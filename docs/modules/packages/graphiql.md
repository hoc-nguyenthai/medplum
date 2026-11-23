# @medplum/graphiql

> GraphiQL trỏ tới endpoint GraphQL của server.

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/graphiql` |
| Đường dẫn đích | `tools/medplum-graphiql` |
| Số file nguồn (không tính test) | 3 |
| Thư mục `src/` | `config.ts`, `index.tsx` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `dev`, `lint` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md), [`fhirtypes`](fhirtypes.md), [`react`](react.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/graphiql`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/graphiql` phải xanh.
