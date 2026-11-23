# @medplum/server

> Máy chủ FHIR (Express + PostgreSQL + Redis): REST FHIR R4, OAuth2/OIDC, quản trị project, bot, subscription, bulk export, job nền. Khối lớn nhất (791 file).

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/server` |
| Đường dẫn đích | `apps/medplum-server` |
| Số file nguồn (không tính test) | 513 |
| Thư mục `src/` | `@types`, `__mocks__`, `admin`, `agent`, `ai`, `api-catalog.test.ts`, `api-catalog.ts`, `app.test.ts`, `app.ts`, `async-batch.test.ts`, `async-batch.ts`, `auth`, `bots`, `cds`, `cloud`, `config`, `constants.ts`, `context.test.ts`, `context.ts`, `cors.test.ts`, `cors.ts`, `database-config.test.ts`, `database-migration.test.ts`, `database.test.ts`, `database.ts`, `delegation.test.ts`, `dicom`, `email`, `fhir`, `fhircast`, `healthcheck.test.ts`, `healthcheck.ts`, `heartbeat.ts`, `hl7`, `img`, `index.test.ts`, `index.ts`, `keyvalue`, `logger.test.ts`, `logger.ts`, `mcp`, `migration-sql.test.ts`, `migration-sql.ts`, `migrations`, `oauth`, `openapi.test.ts`, `openapi.ts`, `otel`, `pubsub.ts`, `ratelimit.test.ts`, `ratelimit.ts`, `redis.test.ts`, `redis.ts`, `request-context-store.ts`, `scim`, `seed.test.ts`, `seed.ts`, `seeds`, `server-registry.test.ts`, `server-registry.ts`, `sharding`, `storage`, `test-matchers.test.ts`, `test-matchers.ts`, `test.global-setup.test.ts`, `test.global-setup.ts`, `test.setup.fetch.ts`, `test.setup.ts`, `util`, `webhook`, `wellknown.test.ts`, `wellknown.ts`, `workers`, `ws` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `dev`, `lint`, `start`, `test` |
| Thứ tự di chuyển | Bước 4, thứ tự 2/7 |

## Phụ thuộc

- Runtime: [`bot-layer`](bot-layer.md), [`ccda`](ccda.md), [`core`](core.md), [`definitions`](definitions.md), [`fhir-router`](fhir-router.md)
- Chỉ dev: [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/server`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/server` phải xanh.
