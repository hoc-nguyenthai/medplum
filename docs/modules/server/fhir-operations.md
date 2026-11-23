# server/fhir/operations

> Các FHIR operation `$...` (113 route): export, expand, translate, $everything, $summary, Appointment, Agent, DB admin, Project clone/archive.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/fhir/operations` |
| Đường dẫn đích | `apps/medplum-server/src/fhir/operations` |
| Số file nguồn (không tính test) | 93 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (126), `fhir` (96), `util` (38), `config` (18), `storage` (11), `auth` (8), `workers` (7), `bots` (6), `migrations` (5), `oauth` (5), `cloud` (4), `ai` (2), `agent` (2), `admin` (2), `email` (1)
- Được gọi bởi: `fhir` (86), `workers` (8), `(root)` (3), `seeds` (3), `ws` (3), `auth` (2), `admin` (2), `migrations` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| POST | `/fhir/R4/$clear-all-ws-subs` | Bearer |
| GET | `/fhir/R4/$db-column-statistics` | Bearer |
| POST | `/fhir/R4/$db-configure-column-statistics` | Bearer |
| POST | `/fhir/R4/$db-configure-indexes` | Bearer |
| GET | `/fhir/R4/$db-index-bloat` | Bearer |
| GET | `/fhir/R4/$db-indexes` | Bearer |
| POST | `/fhir/R4/$db-invalid-indexes` | Bearer |
| POST | `/fhir/R4/$db-schema-diff` | Bearer |
| POST | `/fhir/R4/$db-stats` | Bearer |
| POST | `/fhir/R4/$explain` | Bearer |
| GET | `/fhir/R4/$export` | Bearer |
| POST | `/fhir/R4/$export` | Bearer |
| GET | `/fhir/R4/$get-ws-sub-project-stats` | Bearer |
| GET | `/fhir/R4/$get-ws-sub-stats` | Bearer |
| POST | `/fhir/R4/$rebuild-base-definitions` | Bearer |
| POST | `/fhir/R4/$resolve-smart-health-link` | Bearer |
| POST | `/fhir/R4/$restore` | Bearer |
| POST | `/fhir/R4/$verify-smart-health-card` | Bearer |
| POST | `/fhir/R4/:resourceType/$validate` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/$aws-textract` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/$expunge` | Bearer |
| GET | `/fhir/R4/:resourceType/:id/$graph` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/$refresh-reference-display` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/$reindex` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/$resend` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/$set-accounts` | Bearer |
| POST | `/fhir/R4/:resourceType/:id/_history/:vid/$resend` | Bearer |
| GET | `/fhir/R4/Agent/$bulk-status` | Bearer |
| GET | `/fhir/R4/Agent/$fetch-logs` | Bearer |
| GET | `/fhir/R4/Agent/$reload-config` | Bearer |
| GET | `/fhir/R4/Agent/$stats` | Bearer |
| GET | `/fhir/R4/Agent/$status` | Bearer |
| GET | `/fhir/R4/Agent/$upgrade` | Bearer |
| GET | `/fhir/R4/Agent/:id/$fetch-logs` | Bearer |
| GET | `/fhir/R4/Agent/:id/$reload-config` | Bearer |
| GET | `/fhir/R4/Agent/:id/$stats` | Bearer |
| GET | `/fhir/R4/Agent/:id/$status` | Bearer |
| GET | `/fhir/R4/Agent/:id/$upgrade` | Bearer |
| POST | `/fhir/R4/Appointment/$book` | Bearer |
| GET | `/fhir/R4/Appointment/$find` | Bearer |
| POST | `/fhir/R4/Appointment/$hold` | Bearer |
| POST | `/fhir/R4/Appointment/:id/$cancel` | Bearer |
| POST | `/fhir/R4/Appointment/:id/$confirm` | Bearer |
| POST | `/fhir/R4/Appointment/:id/$reschedule` | Bearer |
| POST | `/fhir/R4/AsyncJob/:id/$cancel` | Bearer |
| GET | `/fhir/R4/Binary/:id/$presigned-url` | Bearer |
| POST | `/fhir/R4/Binary/:id/$scan` | Bearer |
| POST | `/fhir/R4/Bot/$init` | Bearer |
| POST | `/fhir/R4/Bot/:id/$deploy` | Bearer |
| POST | `/fhir/R4/ChargeItemDefinition/:id/$apply` | Bearer |
| POST | `/fhir/R4/Claim/$export` | Bearer |
| POST | `/fhir/R4/Claim/$submit` | Bearer |
| GET | `/fhir/R4/Claim/:id/$export` | Bearer |
| POST | `/fhir/R4/Claim/:id/$submit` | Bearer |
| POST | `/fhir/R4/ClientApplication/:id/$rotate-secret` | Bearer |
| GET | `/fhir/R4/ClientApplication/:id/$smart-launch` | Bearer |
| POST | `/fhir/R4/CodeSystem/$import` | Bearer |
| GET | `/fhir/R4/CodeSystem/$lookup` | Bearer |
| POST | `/fhir/R4/CodeSystem/$lookup` | Bearer |
| GET | `/fhir/R4/CodeSystem/$subsumes` | Bearer |
| POST | `/fhir/R4/CodeSystem/$subsumes` | Bearer |
| GET | `/fhir/R4/CodeSystem/$validate-code` | Bearer |
| POST | `/fhir/R4/CodeSystem/$validate-code` | Bearer |
| POST | `/fhir/R4/CodeSystem/:id/$import` | Bearer |
| GET | `/fhir/R4/CodeSystem/:id/$lookup` | Bearer |
| POST | `/fhir/R4/CodeSystem/:id/$lookup` | Bearer |
| GET | `/fhir/R4/CodeSystem/:id/$subsumes` | Bearer |
| POST | `/fhir/R4/CodeSystem/:id/$subsumes` | Bearer |
| GET | `/fhir/R4/CodeSystem/:id/$validate-code` | Bearer |
| POST | `/fhir/R4/CodeSystem/:id/$validate-code` | Bearer |
| POST | `/fhir/R4/ConceptMap/$import` | Bearer |
| GET | `/fhir/R4/ConceptMap/$translate` | Bearer |
| POST | `/fhir/R4/ConceptMap/$translate` | Bearer |
| POST | `/fhir/R4/ConceptMap/:id/$import` | Bearer |
| GET | `/fhir/R4/ConceptMap/:id/$translate` | Bearer |
| POST | `/fhir/R4/ConceptMap/:id/$translate` | Bearer |
| POST | `/fhir/R4/CoverageEligibilityRequest/$submit` | Bearer |
| POST | `/fhir/R4/CoverageEligibilityRequest/:id/$submit` | Bearer |
| GET | `/fhir/R4/Group/:id/$export` | Bearer |
| POST | `/fhir/R4/Group/:id/$export` | Bearer |
| POST | `/fhir/R4/Measure/:id/$evaluate-measure` | Bearer |
| POST | `/fhir/R4/PackageRelease/:id/$install` | Bearer |
| GET | `/fhir/R4/Patient/$export` | Bearer |
| POST | `/fhir/R4/Patient/$export` | Bearer |
| POST | `/fhir/R4/Patient/$match` | Bearer |
| GET | `/fhir/R4/Patient/:id/$ccda-export` | Bearer |
| POST | `/fhir/R4/Patient/:id/$ccda-export` | Bearer |
| GET | `/fhir/R4/Patient/:id/$everything` | Bearer |
| POST | `/fhir/R4/Patient/:id/$everything` | Bearer |
| POST | `/fhir/R4/Patient/:id/$generate-smart-health-card` | Bearer |
| POST | `/fhir/R4/Patient/:id/$generate-smart-health-link` | Bearer |
| GET | `/fhir/R4/Patient/:id/$summary` | Bearer |
| POST | `/fhir/R4/Patient/:id/$summary` | Bearer |
| POST | `/fhir/R4/PlanDefinition/:id/$apply` | Bearer |
| POST | `/fhir/R4/Project/$init` | Bearer |
| POST | `/fhir/R4/Project/:id/$archive` | Bearer |
| POST | `/fhir/R4/Project/:id/$clone` | Bearer |
| GET | `/fhir/R4/Project/:id/$rate-limits` | Bearer |
| POST | `/fhir/R4/QuestionnaireResponse/$extract` | Bearer |
| GET | `/fhir/R4/QuestionnaireResponse/:id/$extract` | Bearer |
| POST | `/fhir/R4/QuestionnaireResponse/:id/$extract` | Bearer |
| POST | `/fhir/R4/StructureDefinition/$expand-profile` | Bearer |
| GET | `/fhir/R4/Subscription/:id/$get-ws-binding-token` | Bearer |
| POST | `/fhir/R4/User/:id/$rescope` | Bearer |
| POST | `/fhir/R4/User/:id/$update-email` | Bearer |
| GET | `/fhir/R4/ValueSet/$expand` | Bearer |
| POST | `/fhir/R4/ValueSet/$expand` | Bearer |
| GET | `/fhir/R4/ValueSet/$validate-code` | Bearer |
| POST | `/fhir/R4/ValueSet/$validate-code` | Bearer |
| GET | `/fhir/R4/ValueSet/:id/$expand` | Bearer |
| POST | `/fhir/R4/ValueSet/:id/$expand` | Bearer |
| GET | `/fhir/R4/ValueSet/:id/$validate-code` | Bearer |
| POST | `/fhir/R4/ValueSet/:id/$validate-code` | Bearer |
| POST | `/shl/:id/manifest` | không guard ở route (xem middleware cha) |
| GET | `/shl/:id/payload` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `fhir/operations/agentbulkstatus.ts`
- `fhir/operations/agentfetchlogs.ts`
- `fhir/operations/agentpush.ts`
- `fhir/operations/agentreloadconfig.ts`
- `fhir/operations/agentstats.ts`
- `fhir/operations/agentstatus.ts`
- `fhir/operations/agentupgrade.ts`
- `fhir/operations/ai.ts`
- `fhir/operations/asyncjobcancel.ts`
- `fhir/operations/binary-presigned-url.ts`
- `fhir/operations/binary-scan.ts`
- `fhir/operations/book.ts`
- `fhir/operations/botinit.ts`
- `fhir/operations/cancel.ts`
- `fhir/operations/ccdaexport.ts`
- `fhir/operations/chargeitemdefinitionapply.ts`
- `fhir/operations/claimexport.ts`
- `fhir/operations/claimsubmit.ts`
- `fhir/operations/clearallwssubs.ts`
- `fhir/operations/codesystemimport.ts`
- `fhir/operations/codesystemlookup.ts`
- `fhir/operations/codesystemvalidatecode.ts`
- `fhir/operations/conceptmapimport.ts`
- `fhir/operations/conceptmaptranslate.ts`
- `fhir/operations/confirm.ts`
- … và 68 file nữa
