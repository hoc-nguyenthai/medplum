# server/email

> Gửi email (SES hoặc SMTP).

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/email` |
| Đường dẫn đích | `apps/medplum-server/src/email` |
| Số file nguồn (không tính test) | 3 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (4), `config` (4), `util` (3), `fhir` (2), `oauth` (1), `cloud` (1), `storage` (1)
- Được gọi bởi: `auth` (6), `admin` (2), `(root)` (1), `cloud` (1), `fhir/operations` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| POST | `/email/v1/send` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `email/email.ts`
- `email/routes.ts`
- `email/utils.ts`
