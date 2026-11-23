# server/fhir

> Trung tâm: Repository (CRUD, search, history), access policy, lookup tables, token column, batch, binary, bulk data, projectmembership, SMART.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/fhir` |
| Đường dẫn đích | `apps/medplum-server/src/fhir` |
| Số file nguồn (không tính test) | 49 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `fhir/operations` (86), `(root)` (38), `config` (24), `storage` (7), `oauth` (6), `util` (6), `otel` (5), `bots` (2), `cloud` (1), `sharding` (1), `workers` (1), `ws` (1)
- Được gọi bởi: `fhir/operations` (96), `auth` (48), `(root)` (33), `workers` (24), `migrations` (21), `oauth` (17), `admin` (13), `bots` (9), `ws` (5), `util` (3), `seeds` (3), `dicom` (3), `webhook` (2), `scim` (2), `storage` (2), `cloud` (2), `email` (2), `config` (1), `sharding` (1), `otel` (1), `keyvalue` (1), `fhircast` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| POST | `/fhir/R4/$ai` | Bearer |
| GET | `/fhir/R4/$versions` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/.well-known/smart-configuration` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/.well-known/smart-styles.json` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/:resourceType/$csv` | Bearer |
| POST | `/fhir/R4/Agent/$push` | Bearer |
| POST | `/fhir/R4/Agent/:id/$push` | Bearer |
| DELETE | `/fhir/R4/bulkdata/export/:id` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/bulkdata/export/:id` | không guard ở route (xem middleware cha) |
| DELETE | `/fhir/R4/job/:id/status` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/job/:id/status` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/metadata` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `fhir/accesspolicy.ts`
- `fhir/batch-telemetry.ts`
- `fhir/batch/checkpoint-store.ts`
- `fhir/binary.ts`
- `fhir/bulkdata.ts`
- `fhir/default-access-policies.ts`
- `fhir/fhirquota.ts`
- `fhir/job.ts`
- `fhir/jsonschema.ts`
- `fhir/lookups/address.ts`
- `fhir/lookups/coding.ts`
- `fhir/lookups/conceptmapping.ts`
- `fhir/lookups/humanname.ts`
- `fhir/lookups/lookuptable.ts`
- `fhir/lookups/reference.ts`
- `fhir/lookups/util.ts`
- `fhir/metadata.ts`
- `fhir/outcomes.ts`
- `fhir/patient.ts`
- `fhir/precommit.ts`
- `fhir/presence.ts`
- `fhir/projectmembership.ts`
- `fhir/range-column.ts`
- `fhir/references.ts`
- `fhir/repo.ts`
- … và 24 file nữa
