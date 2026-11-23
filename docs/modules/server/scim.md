# server/scim

> SCIM v2: provisioning user.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/scim` |
| Đường dẫn đích | `apps/medplum-server/src/scim` |
| Số file nguồn (không tính test) | 3 |
| Cụm | danh tính |

## Phụ thuộc (số import)

- Gọi tới: `admin` (2), `auth` (2), `fhir` (2), `(root)` (1), `oauth` (1), `config` (1), `util` (1)
- Được gọi bởi: `(root)` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/scim/v2/Users` | không guard ở route (xem middleware cha) |
| POST | `/scim/v2/Users` | không guard ở route (xem middleware cha) |
| DELETE | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| GET | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| PATCH | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| PUT | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm danh tính (`auth`, `oauth`, `admin`, `scim`): phụ thuộc `fhir` nặng. Giai đoạn 1 chuyển cùng server; giai đoạn 2 có thể thay bằng service Go identity.

## File chính

- `scim/routes.ts`
- `scim/types.ts`
- `scim/utils.ts`
