# server/admin

> Quản trị project (mời user, tạo bot/client, member, secret, site) và super admin (reindex, purge, vacuum, migrate).

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/admin` |
| Đường dẫn đích | `apps/medplum-server/src/admin` |
| Số file nguồn (không tính test) | 7 |
| Cụm | danh tính |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (13), `fhir` (13), `auth` (7), `workers` (7), `oauth` (6), `util` (6), `migrations` (3), `seeds` (3), `email` (2), `fhir/operations` (2), `config` (2), `cloud` (1)
- Được gọi bởi: `(root)` (3), `scim` (2), `fhir/operations` (2)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/admin/projects/:projectId` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/bot` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/client` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/invite` | không guard ở route (xem middleware cha) |
| DELETE | `/admin/projects/:projectId/members/:membershipId` | không guard ở route (xem middleware cha) |
| GET | `/admin/projects/:projectId/members/:membershipId` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/members/:membershipId` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/members/:membershipId/mfa/reset` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/members/:membershipId/resetpassword` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/secrets` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/settings` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/sites` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/setpassword` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/drop-invalid-indexes` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/lambda-cleaner` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/migrate` | không guard ở route (xem middleware cha) |
| GET | `/admin/super/migrations` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/purge` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/rebuild-index` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/rebuildprojectid` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/reconcile-db-schema-drift` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/reindex` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/reloadcron` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/removebotidjobsfromqueue` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/searchparameters` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/setdataversion` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/setpassword` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/setschemaversion` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/structuredefinitions` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/tablesettings` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/vacuum` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/valuesets` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm danh tính (`auth`, `oauth`, `admin`, `scim`): phụ thuộc `fhir` nặng. Giai đoạn 1 chuyển cùng server; giai đoạn 2 có thể thay bằng service Go identity.

## File chính

- `admin/bot.ts`
- `admin/client.ts`
- `admin/invite.ts`
- `admin/project.ts`
- `admin/routes.ts`
- `admin/super.ts`
- `admin/utils.ts`
