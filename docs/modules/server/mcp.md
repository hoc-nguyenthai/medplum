# server/mcp

> Máy chủ MCP cho công cụ AI (stream và SSE).

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/mcp` |
| Đường dẫn đích | `apps/medplum-server/src/mcp` |
| Số file nguồn (không tính test) | 2 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (7), `oauth` (1), `config` (1)
- Được gọi bởi: `(root)` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/mcp/sse` | không guard ở route (xem middleware cha) |
| POST | `/mcp/sse` | không guard ở route (xem middleware cha) |
| ALL | `/mcp/stream` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `mcp/routes.ts`
- `mcp/server.ts`
