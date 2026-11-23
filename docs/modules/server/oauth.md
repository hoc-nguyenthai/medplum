# server/oauth

> OAuth2/OIDC: authorize, token (5 grant), userinfo, introspect, logout, register, JWKS.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/oauth` |
| Đường dẫn đích | `apps/medplum-server/src/oauth` |
| Số file nguồn (không tính test) | 12 |
| Cụm | danh tính |

## Phụ thuộc (số import)

- Gọi tới: `fhir` (17), `(root)` (12), `config` (7), `util` (7), `auth` (7), `fhircast` (1)
- Được gọi bởi: `auth` (26), `(root)` (11), `admin` (6), `fhir` (6), `workers` (5), `fhir/operations` (5), `ws` (5), `bots` (1), `scim` (1), `mcp` (1), `cds` (1), `keyvalue` (1), `fhircast` (1), `dicom` (1), `email` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/oauth2/authorize` | không guard ở route (xem middleware cha) |
| POST | `/oauth2/authorize` | không guard ở route (xem middleware cha) |
| POST | `/oauth2/introspect` | Bearer |
| GET | `/oauth2/logout` | Bearer |
| POST | `/oauth2/logout` | Bearer |
| POST | `/oauth2/register` | không guard ở route (xem middleware cha) |
| POST | `/oauth2/token` | không guard ở route (xem middleware cha) |
| GET | `/oauth2/userinfo` | Bearer |
| POST | `/oauth2/userinfo` | Bearer |

## Ghi chú tách

Cụm danh tính (`auth`, `oauth`, `admin`, `scim`): phụ thuộc `fhir` nặng. Giai đoạn 1 chuyển cùng server; giai đoạn 2 có thể thay bằng service Go identity.

## File chính

- `oauth/authorize.ts`
- `oauth/cert.ts`
- `oauth/clients.ts`
- `oauth/introspect.ts`
- `oauth/keys.ts`
- `oauth/logout.ts`
- `oauth/middleware.ts`
- `oauth/register.ts`
- `oauth/routes.ts`
- `oauth/token.ts`
- `oauth/userinfo.ts`
- `oauth/utils.ts`
