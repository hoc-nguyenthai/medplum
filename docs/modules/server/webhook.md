# server/webhook

> Nhận webhook đến, chạy bot tương ứng.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/webhook` |
| Đường dẫn đích | `apps/medplum-server/src/webhook` |
| Số file nguồn (không tính test) | 1 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `bots` (2), `fhir` (2)
- Được gọi bởi: `(root)` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| POST | `/webhook/:id` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `webhook/routes.ts`
