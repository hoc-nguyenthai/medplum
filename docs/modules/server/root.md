# server/(root)

> Khởi tạo ứng dụng: app.ts (middleware, mount router), database.ts, redis.ts, ratelimit, healthcheck, context, openapi, pubsub, logger.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/` |
| Đường dẫn đích | `apps/medplum-server/src/` |
| Số file nguồn (không tính test) | 24 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `fhir` (33), `config` (18), `oauth` (11), `util` (9), `migrations` (4), `admin` (3), `fhir/operations` (3), `otel` (3), `seeds` (3), `auth` (2), `storage` (2), `__mocks__` (1), `cds` (1), `dicom` (1), `email` (1), `fhircast` (1), `hl7` (1), `keyvalue` (1), `mcp` (1), `scim` (1), `webhook` (1), `ws` (1), `workers` (1), `sharding` (1)
- Được gọi bởi: `fhir/operations` (126), `fhir` (38), `workers` (34), `auth` (23), `ws` (21), `migrations` (15), `admin` (13), `oauth` (12), `cloud` (8), `mcp` (7), `dicom` (7), `fhircast` (6), `email` (4), `keyvalue` (3), `bots` (2), `util` (2), `config` (2), `otel` (2), `storage` (2), `scim` (1), `cds` (1), `ai` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/.well-known/api-catalog` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/jwks.json` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/oauth-authorization-server` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/oauth-protected-resource` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/openid-configuration` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/smart-configuration` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/smart-styles.json` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `api-catalog.ts`
- `app.ts`
- `async-batch.ts`
- `constants.ts`
- `context.ts`
- `cors.ts`
- `database.ts`
- `healthcheck.ts`
- `heartbeat.ts`
- `index.ts`
- `logger.ts`
- `migration-sql.ts`
- `openapi.ts`
- `pubsub.ts`
- `ratelimit.ts`
- `redis.ts`
- `request-context-store.ts`
- `seed.ts`
- `server-registry.ts`
- `test-matchers.ts`
- `test.global-setup.ts`
- `test.setup.fetch.ts`
- `test.setup.ts`
- `wellknown.ts`
