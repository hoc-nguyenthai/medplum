# server/cds

> CDS Hooks: liệt kê dịch vụ, thực thi bot làm CDS service.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/cds` |
| Đường dẫn đích | `apps/medplum-server/src/cds` |
| Số file nguồn (không tính test) | 1 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `bots` (2), `(root)` (1), `oauth` (1)
- Được gọi bởi: `(root)` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/cds-services/` | không guard ở route (xem middleware cha) |
| POST | `/cds-services/:id` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `cds/routes.ts`
