# FHIR Operations (`$...`)

> **Trạng thái: phần lớn CHƯA CÀI trong fhir-server Go** (cài rồi: `$validate`, `$everything`, `$reindex`, `$expunge`, `$export`, `$graphql`, `job/{id}/status`, terminology `$expand`, `$lookup`, `$validate-code`, `$subsumes`, `$translate`; xem `fhir-rest.md`). Tài liệu này mô tả server Medplum bản TypeScript gốc, chưa phải hợp đồng của server đang chạy. Route Go thật xem `fhir-rest.md` và `/swagger/index.html`. Không viết DTO cho các route dưới đây cho tới khi chúng được port sang Go.

Sinh tự động từ `packages/server/src/fhir/routes.ts` (115 route). Mỗi operation có `OperationDefinition` mô tả tham số; input nhận `Parameters` hoặc JSON phẳng.

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

Nhóm theo mục đích: terminology (`ValueSet/$expand`, `CodeSystem/$lookup|$validate-code|$subsumes|$import`, `ConceptMap/$translate|$import`), lâm sàng (`Patient/$everything|$summary|$match|$ccda-export`, `$extract`, `$evaluate-measure`, `PlanDefinition/$apply`), lịch hẹn (`Appointment/$find|$book|$hold|$cancel|$confirm|$reschedule`), dữ liệu lớn (`$export`, `Group/$export`, `Patient/$export`), vận hành project (`Project/$init|$clone|$archive`, `$restore`), agent (`Agent/$status|$stats|$upgrade|$fetch-logs|$reload-config|$bulk-status|$push`), bot (`Bot/$init|$deploy|$execute`), hạ tầng DB (`$db-*`, `$explain`).
