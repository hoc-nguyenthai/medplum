# server/keyvalue

> API key-value trên Redis.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/keyvalue` |
| Đường dẫn đích | `apps/medplum-server/src/keyvalue` |
| Số file nguồn (không tính test) | 2 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (3), `fhir` (1), `oauth` (1)
- Được gọi bởi: `(root)` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| DELETE | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| GET | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| PUT | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `keyvalue/routes.ts`
- `keyvalue/store.ts`
