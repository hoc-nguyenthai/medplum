# Ma trận phủ kế hoạch: server Medplum so với các kế hoạch hiện có

Trạng thái: **chưa đủ.** (Cập nhật: nhóm mức B đã có kế hoạch chi tiết ở [modules/README.md](modules/README.md); chúng nay là mức A, nhưng còn nhiều câu hỏi mở và hai lỗi bảo mật cần xử lý sớm.) Nguồn: `~/Downloads/medplum/packages/server/src` (v5.2.18). Số dòng là dòng source **không tính test**, do tôi đếm; số dòng chỉ là thước đo thô về quy mô, không phản ánh độ khó. Ba mức: **A** có kế hoạch chi tiết (và có thể đã cài), **B** chỉ có khảo sát ([modules-survey.md](modules-survey.md)), **C** chưa có gì.

## 1. Theo từng khu vực

| Khu vực | Dòng | Mức | Ở đâu / ghi chú |
|---|---|---|---|
| `auth` | 3858 | A | [auth/](../../apps/auth/docs/plans/README.md); **đã cài** P0 đến P6, 7.0 (CLI), P7 HTTP một phần; còn B9 (rà soát bảo mật) |
| `oauth` | 4267 | A | `auth` P3, **đã cài** (B1 đến B8) |
| `admin` | 1926 | A (hoãn) | `auth` P7; CLI 7.0 thay phần tối thiểu |
| `cds` | 82 | A | [cds-dicom/cds.md](cds-dicom/cds.md); chặn bởi quyết định runtime Bot |
| `dicom` (+ worker `dicom`) | 934 (+246) | A | [cds-dicom/dicom.md](cds-dicom/dicom.md) |
| `email` | 548 | A | [email-storage.md](modules/email-storage.md); dùng chung với `Mailer` của `auth` |
| `storage` | 577 | A | [email-storage.md](modules/email-storage.md); là nền của DICOM |
| `keyvalue` | 128 | A | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); đã có kế hoạch |
| `mcp` | 220 | A | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); đã có kế hoạch |
| `scim` | 694 | A | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); thuộc `auth` |
| `webhook` | 74 | A | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); chặn bởi runtime Bot |
| `ws` | 1965 | A | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); `subscriptions-r4` đã có ở Go |
| `fhircast` | 930 | A | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); đã có kế hoạch |
| `fhir` | 37352 | A (kiểm kê) | [modules/fhir-parity-audit.md](modules/fhir-parity-audit.md): 204 mục, 45 CÓ, 35 MỘT PHẦN, 97 CHƯA, 27 KHÔNG ÁP DỤNG; 17 gói công việc |
| `workers` (11 hàng đợi) | 5286 | B/C | chỉ có bảng đối chiếu ở mục 7.3 của khảo sát; chưa có kế hoạch cho `download`, `dispatch`, `set-accounts`, `cron`, `batch` (async) |
| `bots` (`execute`, `vmcontext`, `utils`) | 877 | B/C | chỉ là "phương án D" trong `cds.md`; **chưa có kế hoạch**, mà `cds`, `webhook`, `cron`, `$execute` đều chờ nó |
| `cloud` (aws, azure, gcp, fission) | 2241 | **C** | S3/Azure/GCP storage, SES, Lambda (chạy Bot), Textract, Fission; chưa nhắc tới ngoài việc "chỉ làm SMTP và hệ thống tệp" |
| `ai` (`openai.ts`) | 652 | **C** | gắn với `$ai` và `ws/ai-realtime`; chưa có |
| `config` (loader, SSM, file) | 1293 | C | Go dùng `envconfig` theo từng app; không cần port nguyên, nhưng chưa ghi quyết định |
| `util` | 1795 | C | hàm phụ trợ; đa số sẽ có sẵn hoặc không cần |
| `migrations` | 30248 | C | **migration schema PostgreSQL của Medplum, không áp dụng** cho kho Go (goose riêng). Điều **chưa có** là kế hoạch **chuyển dữ liệu** từ một CSDL Medplum đang chạy (nếu có), xem mục 3 |
| `otel`, `pubsub`, `redis`, `database`, `context`, `logger`, `healthcheck` | 390, 181, 221, 414, 380, 43, 150 | C | hạ tầng; Go đã có phần tương đương (`pkg/observability`, `pkg/messaging`, `pkg/database`, `httpserve`) nhưng chưa có bảng đối chiếu |
| `ratelimit`, `heartbeat`, `server-registry` | 197, 36, 147 | C | rate limit là điều kiện trước khi mở `auth` ra Internet (`fhir-server` đã có hạn mức theo IP trong bộ nhớ, bật mặc định 20 rps ở staging/production, + `pkg/httpsec`, 2026-11-21; `auth` cũng đã có, 2026-11-21: chung 20 rps và đăng nhập 1 rps/IP); `server-registry` chưa đọc |
| `seed`, `seeds` | 143, 36 | C | `auth seed project` thay phần cần thiết; `seeds/` nạp search parameter, structure definition, value set đã dựng sẵn |
| `openapi`, `api-catalog`, `wellknown` | 500, 91, 89 | C/A | `wellknown` nằm trong `auth` P3; `openapi.json` và `api-catalog` chưa quyết định |
| `hl7`, `sharding`, `agent` | 26, 18, 14 | C | nhỏ; `hl7` chỉ cần khi có Bot |

Tổng khoảng 69,5 nghìn dòng (chưa tính `migrations`). Phần có kế hoạch chi tiết (mức A) khoảng 11 nghìn dòng, tức khoảng một phần sáu theo dòng, nhưng `fhir` một mình chiếm hơn một nửa nên con số này gây hiểu lầm: theo **số khu vực**, 5 trong khoảng 30 khu vực có kế hoạch chi tiết.

## 2. Ngoài `server`: các package khác của monorepo Medplum

`packages/` còn có `agent`, `app`, `bot-layer`, `ccda`, `cli`, `core`, `fhir-router`, `fhirtypes`, `hl7`, `react`, `react-hooks`, `graphiql`, `generator`, `create-medplum` và các tích hợp (`dosespot-*`, `health-gorilla-*`, `scriptsure-react`). Trong repo Go đã có phần tương đương của `core`/`fhirtypes` (`pkg/fhir`), `fhir-router` (`pkg/fhirrouter`) và `cli` (`apps/fhir-cli`). **`agent`** (cầu nối HL7/DICOM chạy tại cơ sở y tế), `ccda`, `hl7` và các tích hợp bên thứ ba **chưa nằm trong bất kỳ kế hoạch nào**; chưa rõ chúng có thuộc phạm vi migration hay không.

## 3. Quyết định còn thiếu (chặn việc lập kế hoạch cho phần C)

| # | Câu hỏi | Vì sao chặn |
|---|---|---|
| M1 | Có chạy Bot không, và bằng cách nào (HTTP ngoài, sidecar Node, port module Bot)? | Chặn `cds`, `webhook`, `cron`, `Bot/$execute`, `cloud` (Lambda, Fission), `hl7` |
| M2 | Có cần **chuyển dữ liệu** từ CSDL Medplum hiện có sang kho Go không? | Nếu có, cần kế hoạch di trú (ánh xạ bảng, băm mật khẩu `$2b$`, resource, binary); nếu không, `migrations/` hoàn toàn không liên quan |
| M3 | Phạm vi `agent`, `ccda`, `hl7` và các tích hợp bên thứ ba (Health Gorilla, DoseSpot, ScriptSure) | Chưa rõ có thuộc migration không |
| M4 | `ai`: có dùng nhà cung cấp AI nào, và có cần `$ai` và `ws/ai-realtime` không | Chặn `ai` |
| M5 | Lưu trữ đối tượng: chỉ hệ thống tệp, hay S3/Azure/GCP | Chặn `storage` và `cloud` |

## 4. Việc nên làm để "đủ" ở mức kế hoạch

1. Chốt M1 đến M5 (hoặc ghi rõ "ngoài phạm vi").
2. Bản kiểm kê tương đương cho `fhir` (đã ở mục 6 của khảo sát).
3. Viết kế hoạch chi tiết cho các khu vực mức B mà bạn muốn làm trước (đề xuất: `email` + `storage`, `keyvalue`, `mcp`).
4. Kế hoạch cho `workers` (nhóm theo hàng đợi, gắn với từng module sở hữu nó) và cho `bots` nếu chọn phương án D ở M1.
5. Bảng đối chiếu hạ tầng (`otel`, `redis`, `pubsub`, `ratelimit`, `config`, `healthcheck`) để biết phần nào Go đã có, phần nào còn thiếu.

## 5. Đối chiếu route và operation (2026-11-23)

So trực tiếp các route và operation đăng ký trong `medplum/packages/server/src` (`fhir/routes.ts`, `app.ts`, `wellknown.ts` và router của từng module) với route đăng ký trong `apps/*` và `pkg/fhirrouter`. "Chưa có" nghĩa là không tìm thấy route hoặc handler tương ứng trong Go; tính năng ngang (không phải route) kiểm bằng grep, nên có thể sót. **Lưu ý phiên bản:** bản Medplum dùng để so là `v5.2.17-5-ge7e7e4019` (`/Users/hoc.nguyenthai/Documents/med-vita/medplum`), cũ hơn tag `v5.2.18` ghi ở [UPSTREAM.md](../UPSTREAM.md); thay đổi chỉ có ở 5.2.18 không hiện ra ở đây.

### 5.1 Đã có ở Go

- `fhir-server`: CRUD, history, search, PATCH, batch/transaction, update-as-create; `$export` (hệ thống, Patient, Group); `Patient/$everything`, `$expunge`, `$reindex`, `$validate`, `$graphql`; `$lookup`, `$subsumes`, `$validate-code`, `$expand`, `$translate`; `Binary/$presigned-url`, `/storage/`; `Subscription/$get-ws-binding-token`.
- `auth`: `/auth/*`, `/oauth2/*`, `/.well-known/*` (trừ `api-catalog`), MFA, `/scim/v2/Users`, `/email/v1/send`, `/admin/projects/*` (`client`, `invite`, `members`, `settings`, `sites`).
- `ws-gateway`: `/ws/subscriptions-r4`, FHIRcast (`/fhircast/STU2|STU3`, `/hub`, `/ws/fhircast`).

### 5.2 FHIR operation còn thiếu

| Nhóm | Operation | Chặn bởi |
|---|---|---|
| Lâm sàng, dữ liệu | `Patient/$summary`, `Patient/$match`, `QuestionnaireResponse/$extract`, `:type/:id/$graph`, `:type/$csv`, `$refresh-reference-display`, `$resend`, `StructureDefinition/$expand-profile`, `Measure/$evaluate-measure`, `PlanDefinition/$apply` | không (`$graph` cần GraphDefinition) |
| C-CDA | `Patient/$ccda-export` | M3 |
| Lịch hẹn | `Appointment/$find`, `$book`, `$hold`, `$cancel`, `$confirm`, `$reschedule` | Q-F4 |
| Thanh toán | `Claim/$submit`, `Claim/$export`, `CoverageEligibilityRequest/$submit`, `ChargeItemDefinition/$apply`, `$set-accounts` | M1, Q-F5 |
| Nạp định nghĩa | `CodeSystem/$import`, `ConceptMap/$import`, `PackageRelease/$install`, `$rebuild-base-definitions` | không; cần để nạp SNOMED/LOINC |
| SMART Health Cards/Links | `$generate-smart-health-card`, `$generate-smart-health-link`, `$verify-smart-health-card`, `$resolve-smart-health-link`, route `/shl/` | không |
| Quản trị qua FHIR | `Project/$init`, `$clone`, `$archive`, `/$restore`, `Project/:id/$rate-limits`, `User/$rescope`, `User/$update-email`, `ClientApplication/$rotate-secret`, `ClientApplication/$smart-launch` | ranh giới với `auth` |
| Bot | `Bot/$execute` (4 biến thể), `Bot/$deploy`, `Bot/$init` | M1 |
| Agent | `Agent/$push`, `$status`, `$bulk-status`, `$reload-config`, `$upgrade`, `$fetch-logs`, `$stats` | M3 |
| AI | `$ai`, `$aws-textract` | M4 |
| Vận hành, DB | `$explain`, `$db-stats`, `$db-indexes`, `$db-index-bloat`, `$db-invalid-indexes`, `$db-schema-diff`, `$db-column-statistics`, `$db-configure-*`, `$get-ws-sub-stats`, `$get-ws-sub-project-stats`, `$clear-all-ws-subs`, `$versions`, `Binary/$scan`, `AsyncJob/$cancel` | không; ưu tiên thấp (`DELETE bulkdata/:id/status` có thể thay `AsyncJob/$cancel`) |

### 5.3 Module HTTP ngoài FHIR còn thiếu

| Route Medplum | Ở Go |
|---|---|
| `/keyvalue/v1/:key` | chưa; có kế hoạch |
| `/mcp` (`/sse`, `/stream`) | chưa; có kế hoạch, cần claim `fhir_membership` |
| `/webhook/:id`, `/cds-services` | chưa; M1 |
| `/dicom/PS3/*` (DICOMweb, khoảng 15 route) | chưa; có kế hoạch |
| `/admin/projects/:id/bot`, `/:projectId/secrets` | chưa |
| `/admin/super/*` (reindex, purge, vacuum, valuesets, searchparameters, structuredefinitions, setpassword, tablesettings, rebuildprojectid, reloadcron, lambda-cleaner, removebotidjobsfromqueue, migrations, reconcile-db-schema-drift) | chưa có cả HTTP lẫn CLI |
| `/projects/:projectId/*`, `/api/*` (base URL theo project) | chưa |
| `/openapi.json`, `/.well-known/api-catalog` | chưa (Go có Swagger riêng) |
| WebSocket `agent`, `ai-realtime` | chưa; M3, M4 |

### 5.4 Worker và tính năng nền

| Worker Medplum | Ở Go |
|---|---|
| `bulk-export`, `reindex`, `subscription` (rest-hook) | có |
| `batch` (batch/transaction với `Prefer: respond-async`) | chưa: Go chỉ nhận `respond-async` cho `$export` |
| `download` (tải attachment URL ngoài về Binary) | chưa |
| `subscription-failure-tracker` (tự tắt Subscription lỗi kéo dài) | chưa |
| `cron` | chưa; M1 |
| `dispatch`, `set-accounts`, `dicom`, `lambda-cleaner`, `post-deploy-migration` | chưa; một phần không áp dụng |
| `seeds/` (SearchParameter, StructureDefinition, ValueSet dựng sẵn) | chưa có bước seed tương đương |
| `cloud` | chỉ có S3 storage; chưa có Azure, GCP, SES, Lambda, Fission, Textract (M5, M1, M4) |

### 5.5 Tính năng ngang còn thiếu

- Kiểm soát truy cập: resource `AccessPolicy` lưu trong server (chính sách chỉ đến từ claim), compartment `patient/` và `%patient`, `hiddenFields` (WP-2b, WP-2c).
- Search: `_contained`, SubscriptionTopic (R5), chained trong `_sort`/`_include`.
- Validator: profile/slicing, binding terminology, kiểm tra đích Reference (WP-5).
- `AuditEvent`: kiểm toán mới là dòng log.

