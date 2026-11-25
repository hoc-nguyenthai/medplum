# Ma trận phủ kế hoạch: server Medplum so với các kế hoạch hiện có

Trạng thái (cập nhật 2026-11-24 cho khớp code; mục 1, 5.2, 5.4, 5.5 đã đối chiếu lại bằng grep trong `apps/` và `pkg/`): về **kế hoạch** vẫn **chưa đủ** (nhóm mức B đã có kế hoạch chi tiết ở [modules/README.md](modules/README.md), nay là mức A, nhưng còn nhiều câu hỏi mở, mục 3); về **cài đặt** các khu vực `auth`, `oauth`, `scim`, `email`, `storage`, `ws`, `fhircast` đã chạy trong `apps/auth`, `apps/fhir-server`, `apps/ws-gateway`, phần còn lại của mức A và toàn bộ mức B/C chưa có code (cột "Code Go" ở mục 1). Nguồn: `~/Downloads/medplum/packages/server/src` (v5.2.18). Số dòng là dòng source **không tính test**, do tôi đếm; số dòng chỉ là thước đo thô về quy mô, không phản ánh độ khó. Ba mức **kế hoạch**: **A** có kế hoạch chi tiết (và có thể đã cài), **B** chỉ có khảo sát ([modules-survey.md](modules-survey.md)), **C** chưa có gì. Cột "Code Go" là trạng thái **cài đặt** tại ngày cập nhật, đối chiếu bằng cách tìm route, operation và cấu hình trong mã, nên độc lập với mức kế hoạch.

## 1. Theo từng khu vực

| Khu vực | Dòng | Mức | Code Go (2026-11-23) | Ở đâu / ghi chú |
|---|---|---|---|---|
| `auth` | 3858 | A | **Xong P0 đến P7**: đăng nhập, đăng ký, mật khẩu, MFA, đăng nhập ngoài, `exchange`/`google`/`preauthorize`, `/admin/projects/*`, CLI. P8 (compartment Patient) **xong** (M0 đến M5, sau cờ `AUTH_PATIENT_COMPARTMENT_ENABLED`) | [auth/](../../apps/auth/docs/plans/README.md); chi tiết route: [auth api-contract](../../apps/auth/docs/api-contract.md) |
| `oauth` | 4267 | A | **Xong** (`auth` P3, B1 đến B9): authorize, token (`authorization_code`, `refresh_token`, `client_credentials`, `private_key_jwt`, `pre-authorized_code`), userinfo, introspect, logout, register, well-known | `auth` P3 |
| `admin` | 1926 | A | **Một phần**: `/admin/projects/*` (project, client, invite, members, settings, sites, secrets, update-email, rotate-secret, setpassword, resetpassword, mfa/reset; tắt mặc định `AUTH_ADMIN_ENABLED`) và CLI; `/admin/super/projects`, `/admin/super/users/{id}/rescope`, `/admin/super/projects/{id}/archive|restore` (lưu trữ mềm); **chưa** `/:id/bot` | `auth` P7 |
| `cds` | 82 | A | Chưa | [cds-dicom/cds.md](cds-dicom/cds.md); chặn bởi quyết định runtime Bot |
| `dicom` (+ worker `dicom`) | 934 (+246) | A | **Xong D0 đến D12** (2026-11-24): `apps/dicomweb` (STOW-RS, QIDO-RS, WADO-RS, bulkdata, WADO-URI, rendered, `reindex`; `pkg/dicom*`), kiểm trên PostgreSQL 16 và S3 thật; không cần worker; compose chưa chạy thử Docker | [cds-dicom/dicom.md](cds-dicom/dicom.md) |
| `email` | 548 | A | **Xong** (phần cần thiết): `pkg/mailer` (SMTP), `POST /email/v1/send` kèm đính kèm URL và `Binary/<id>`, SMTP riêng theo project (tắt mặc định); chưa SES | [email-storage.md](modules/email-storage.md); dùng chung với `Mailer` của `auth` |
| `storage` | 577 | A | **Xong** (S1 đến S7): `pkg/binarystore` (tệp `file:` và S3), Binary thô, `/storage` URL ký, `$presigned-url`, `fhir-server sweep-binary`; chưa Azure/GCP | [email-storage.md](modules/email-storage.md); là nền của DICOM |
| `keyvalue` | 128 | A | Chưa | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); đã có kế hoạch |
| `mcp` | 220 | A | Chưa | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); đã có kế hoạch |
| `scim` | 694 | A | **Xong** (`/scim/v2/Users` trong `auth`, tắt mặc định `AUTH_SCIM_ENABLED`) | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); thuộc `auth` |
| `webhook` | 74 | A | Chưa | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); chặn bởi runtime Bot |
| `ws` | 1965 | A | **Một phần**: `/ws/subscriptions-r4` đã chạy trong `apps/ws-gateway` (tách khỏi `fhir-server` 2026-11-22); chưa `agent`, `ai-realtime`, `echo` | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md), [split-scim-ws-services.md](modules/split-scim-ws-services.md) |
| `fhircast` | 930 | A | **Xong** (Hub STU2/STU3 trong `ws-gateway`, tắt mặc định `FHIRCAST_ENABLED`; token của `auth` kèm `hub.url`, `hub.topic`) | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md) |
| `fhir` | 37352 | A (kiểm kê) | **Một phần lớn**: lõi REST, search, batch/transaction, history, bulk export, job bền, terminology cơ bản, Subscription rest-hook, GraphQL, SMART v2 (WP-2a), AccessPolicy mịn (`hiddenFields`, `readonlyFields`, `interaction`, nhiều rule OR, compartment Patient), nạp định nghĩa (`$import`, `$install`, seed), operation lâm sàng (`$summary`, `$match`, `$extract`, `$graph`, `$csv`, `$resend`, `$refresh-reference-display`, `$expand-profile`); xem mục 5 | [modules/fhir-parity-audit.md](modules/fhir-parity-audit.md): kiểm kê 2026-11-21, 204 mục, 45 CÓ, 35 MỘT PHẦN, 97 CHƯA, 27 KHÔNG ÁP DỤNG, **chưa tính lại** sau các việc đã xong từ đó (batch async, tự tắt Subscription, kho Binary S3, AccessPolicy mịn, nạp định nghĩa, operation lâm sàng, hạn mức Redis); 17 gói công việc |
| `workers` (11 hàng đợi) | 5286 | B/C | **Một phần**: có `bulk-export`, `reindex`, `subscription` (rest-hook, bền), `batch` async (`FHIR_ASYNC_BATCH`, tắt mặc định, có checkpoint), tự tắt Subscription lỗi kéo dài (`FHIR_SUBSCRIPTION_AUTO_DISABLE`), `download` (`pkg/fhirdownload`; service riêng `apps/fhir-downloader`); đã tách thành `fhir-worker` và `fhir-notifier`; chưa `dispatch`, `set-accounts`, `cron` (M1), `dicom` (không cần worker) | bảng đối chiếu ở mục 7.3 của khảo sát và mục 5.4 dưới đây |
| `bots` (`execute`, `vmcontext`, `utils`) | 877 | B/C | Chưa | chỉ là "phương án D" trong `cds.md`; **chưa có kế hoạch**, mà `cds`, `webhook`, `cron`, `$execute` đều chờ nó |
| `cloud` (aws, azure, gcp, fission) | 2241 | **C** | **Rất ít**: chỉ S3 storage và SMTP; chưa Azure, GCP, SES, Lambda, Fission, Textract | chưa nhắc tới ngoài việc "chỉ làm SMTP, S3 và hệ thống tệp" |
| `ai` (`openai.ts`) | 652 | **C** | Chưa | gắn với `$ai` và `ws/ai-realtime`; chưa có |
| `config` (loader, SSM, file) | 1293 | C | **Có theo cách riêng của Go**: `pkg/envconfig` từng app, tài liệu cấu hình sinh từ mã (`apps/*/docs/config.md`) kèm test chống lỗi thời, `.env.example` đủ biến; không có SSM/loader kiểu Medplum | không cần port nguyên; quyết định ghi ở [service-principles.md](../service-principles.md) mục 6.2 |
| `util` | 1795 | C | Không áp dụng nguyên khối | hàm phụ trợ; đa số sẽ có sẵn hoặc không cần |
| `migrations` | 30248 | C | Không áp dụng (kho Go dùng goose riêng, `*/migrate`) | **migration schema PostgreSQL của Medplum, không áp dụng** cho kho Go. Điều **chưa có** là kế hoạch **chuyển dữ liệu** từ một CSDL Medplum đang chạy (nếu có), xem mục 3 |
| `otel`, `pubsub`, `redis`, `database`, `context`, `logger`, `healthcheck` | 390, 181, 221, 414, 380, 43, 150 | C | **Có phần tương đương**: `pkg/observability` (đã nhúng vào repo 2026-11-23), `pkg/messaging`, `pkg/database`, `pkg/httpserve` (probe, tắt êm) | hạ tầng; vẫn chưa có bảng đối chiếu từng mục |
| `ratelimit`, `heartbeat`, `server-registry` | 197, 36, 147 | C | **Có hạn mức theo IP trong bộ nhớ ở cả ba service**: `fhir-server` (20 rps ở staging/production, `pkg/httpsec`), `auth` (20 rps chung và đăng nhập 1 rps mỗi IP), `ws-gateway` (10 lần mở websocket mỗi giây mỗi IP); hạn mức dùng chung nhiều replica qua Redis (`pkg/ratelimit`, xong 2026-11-24; Redis lỗi thì rơi về bộ nhớ từng replica); thêm hạn mức ĐIỂM theo người gọi và project cho `fhir-server` (`pkg/ratelimit.Quota`, `FHIR_QUOTA_*`, mặc định tắt, xong 2026-11-25); `server-registry` chưa đọc | rate limit là điều kiện trước khi mở `auth` ra Internet |
| `seed`, `seeds` | 143, 36 | C | **Một phần**: `auth seed project` thay phần cần thiết; bước nạp `seeds/` tương đương là `fhir-server seed definitions` (`pkg/fhirseed`, xong 2026-11-24; tùy chọn, mỗi project một bản) | xem mục 5.4 |
| `openapi`, `api-catalog`, `wellknown` | 500, 91, 89 | C/A | **Một phần**: `wellknown` ở `auth` (jwks, openid-configuration, oauth-authorization-server, smart-configuration, smart-styles, oauth-protected-resource); **OpenAPI (swaggo) cho cả ba service** (`apps/*/docs/swagger.*`, có test chống lệch); chưa có `/openapi.json` theo kiểu Medplum và `/.well-known/api-catalog` | quyết định về `openapi.json` và `api-catalog` vẫn chưa có |
| `hl7`, `sharding`, `agent` | 26, 18, 14 | C | Chưa | nhỏ; `hl7` chỉ cần khi có Bot |

Tổng khoảng 69,5 nghìn dòng (chưa tính `migrations`). Phần có kế hoạch chi tiết (mức A) khoảng 11 nghìn dòng, tức khoảng một phần sáu theo dòng, nhưng `fhir` một mình chiếm hơn một nửa nên con số này gây hiểu lầm: theo **số khu vực**, 5 trong khoảng 30 khu vực có kế hoạch chi tiết. Về **cài đặt**, sáu khu vực đã xong phần cần thiết (`auth`, `oauth`, `scim`, `email`, `storage`, `fhircast`), ba khu vực một phần (`admin`, `ws`, `workers`) và `fhir` đã có phần lõi.

## 2. Ngoài `server`: các package khác của monorepo Medplum

`packages/` còn có `agent`, `app`, `bot-layer`, `ccda`, `cli`, `core`, `fhir-router`, `fhirtypes`, `hl7`, `react`, `react-hooks`, `graphiql`, `generator`, `create-medplum` và các tích hợp (`dosespot-*`, `health-gorilla-*`, `scriptsure-react`). Trong repo Go đã có phần tương đương của `core`/`fhirtypes` (`pkg/fhir`), `fhir-router` (`pkg/fhirrouter`) và `cli` (`apps/fhir-cli`). **`agent`** (cầu nối HL7/DICOM chạy tại cơ sở y tế), `ccda`, `hl7` và các tích hợp bên thứ ba **chưa nằm trong bất kỳ kế hoạch nào**; chưa rõ chúng có thuộc phạm vi migration hay không.

## 3. Quyết định M1 đến M5 (chốt 2026-11-24)

Đã chốt theo đề xuất, tất cả theo hướng **ngoài phạm vi** của đợt này; mở lại khi có nhu cầu cụ thể (ghi quyết định mới ở đây).

| # | Quyết định | Hệ quả |
|---|---|---|
| M1 | **Không chạy Bot** trong đợt này (ngoài phạm vi, làm sau) | `cds`, `webhook` thật (executor W7), `cron`, `Bot/$execute`/`$deploy`/`$init`, `/admin/projects/:id/bot`, thanh toán (`Claim/$submit`...), `hl7`, `cloud` (Lambda, Fission) và `bots` **ngoài phạm vi**. Khung `webhook` với executor giả (đã có) giữ nguyên. Không lập kế hoạch `bots`. |
| M2 | **Không chuyển dữ liệu** từ CSDL Medplum, bắt đầu với dữ liệu mới | `migrations/` của Medplum không liên quan; không cần kế hoạch di trú (ánh xạ bảng, băm mật khẩu `$2b$`, Binary). |
| M3 | `agent`, `ccda`, `hl7` và tích hợp bên thứ ba (Health Gorilla, DoseSpot, ScriptSure) **ngoài phạm vi** | `Agent/*`, `ws/agent`, `Patient/$ccda-export` không làm. |
| M4 | `ai` **ngoài phạm vi** | `$ai`, `$aws-textract`, `ws/ai-realtime` không làm. |
| M5 | **Chỉ hệ thống tệp và S3** | Giữ S1 đến S7. Azure Blob, GCP Storage, SES ngoài phạm vi. |

H1 đến H5 (chốt 2026-11-24, [cds-dicom/README.md](cds-dicom/README.md) mục 4): H1 và H3 vô hiệu vì CDS ngoài phạm vi theo M1; H2 DICOMweb là app riêng `apps/dicomweb`; H4 dùng `pkg/binarystore` (tệp + S3); H5 sửa các lỗi của Medplum.

Còn mở: Q-F1 và các Q khác ở bảng 5.

### 3.1 Bản gốc (trước khi chốt)

| # | Câu hỏi | Vì sao chặn |
|---|---|---|
| M1 | Có chạy Bot không, và bằng cách nào (HTTP ngoài, sidecar Node, port module Bot)? | Chặn `cds`, `webhook`, `cron`, `Bot/$execute`, `cloud` (Lambda, Fission), `hl7` |
| M2 | Có cần **chuyển dữ liệu** từ CSDL Medplum hiện có sang kho Go không? | Nếu có, cần kế hoạch di trú (ánh xạ bảng, băm mật khẩu `$2b$`, resource, binary); nếu không, `migrations/` hoàn toàn không liên quan |
| M3 | Phạm vi `agent`, `ccda`, `hl7` và các tích hợp bên thứ ba (Health Gorilla, DoseSpot, ScriptSure) | Chưa rõ có thuộc migration không |
| M4 | `ai`: có dùng nhà cung cấp AI nào, và có cần `$ai` và `ws/ai-realtime` không | Chặn `ai` |
| M5 | Lưu trữ đối tượng: chỉ hệ thống tệp, hay S3/Azure/GCP | Đã làm hệ thống tệp và S3 (S1 đến S7); câu hỏi còn lại là Azure/GCP, chặn phần còn lại của `cloud` |

## 4. Việc nên làm để "đủ" ở mức kế hoạch

1. ~~Chốt M1 đến M5~~ (xong 2026-11-24, tất cả ngoài phạm vi, xem mục 3).
2. Bản kiểm kê tương đương cho `fhir` (đã ở mục 6 của khảo sát).
3. ~~Viết kế hoạch chi tiết cho `email` + `storage`, `keyvalue`, `mcp`~~ (đã có, xem mục 1); `email` và `storage` đã cài, `keyvalue` và `mcp` chưa.
4. Kế hoạch cho `workers` (nhóm theo hàng đợi, gắn với từng module sở hữu nó) (`bots` ngoài phạm vi theo M1).
5. Bảng đối chiếu hạ tầng (`otel`, `redis`, `pubsub`, `ratelimit`, `config`, `healthcheck`) để biết phần nào Go đã có, phần nào còn thiếu.

## 5. Đối chiếu route và operation (2026-11-23)

Cập nhật 2026-11-23 cho khớp code hiện tại (đối chiếu lại từng dòng bằng cách tìm trong `pkg/` và `apps/`; các mục "chưa có" bên dưới đã được kiểm lại, trừ chỗ ghi rõ). So trực tiếp các route và operation đăng ký trong `medplum/packages/server/src` (`fhir/routes.ts`, `app.ts`, `wellknown.ts` và router của từng module) với route đăng ký trong `apps/*` và `pkg/fhirrouter`. "Chưa có" nghĩa là không tìm thấy route hoặc handler tương ứng trong Go; tính năng ngang (không phải route) kiểm bằng grep, nên có thể sót. **Lưu ý phiên bản:** bản Medplum dùng để so là `v5.2.17-5-ge7e7e4019` (`/Users/hoc.nguyenthai/Documents/med-vita/medplum`), cũ hơn tag `v5.2.18` ghi ở [UPSTREAM.md](../UPSTREAM.md); thay đổi chỉ có ở 5.2.18 không hiện ra ở đây.

### 5.1 Đã có ở Go

- `fhir-server`: CRUD, history, search, PATCH, batch/transaction (kể cả batch async `Prefer: respond-async` khi bật `FHIR_ASYNC_BATCH`, có checkpoint), update-as-create; `$export` (hệ thống, Patient, Group) và `bulkdata/{id}/status`, `job/{id}/status`; `Patient/$everything`, `$expunge`, `$reindex`, `$validate`, `$graphql`; `$lookup`, `$subsumes`, `$validate-code`, `$expand`, `$translate`; Binary thô, `Binary/$presigned-url`, `/storage/` (kho `file:` hoặc `s3:`); `Subscription/$get-ws-binding-token`; Subscription rest-hook (kể cả tự tắt khi lỗi lặp lại); CLI `migrate`, `assign-project`, `sweep-binary`, `seed definitions`; `Patient/$summary`, `$match`, `QuestionnaireResponse/$extract`, `$csv`, `$graph`, `$resend`, `$refresh-reference-display`, `StructureDefinition/$expand-profile`, `CodeSystem/$import`, `ConceptMap/$import`, `PackageRelease/$install`.
- `dicomweb`: STOW-RS, QIDO-RS, WADO-RS (metadata, frame, bulkdata, rendered), WADO-URI, CLI `reindex` (D0 đến D12).
- Tách service từ `fhir-server` (S0 đến S6, 2026-11-24): `fhir-downloader` (tự tải attachment ngoài), `fhir-notifier` (giao rest-hook bền, khớp websocket), `fhir-worker` (job nền).
- `auth`: `/auth/*` (đăng ký, đăng nhập, mật khẩu, MFA, `exchange`, `google`, `preauthorize`, đăng nhập ngoài), `/oauth2/*`, `/.well-known/*` (trừ `api-catalog`), `/scim/v2/Users`, `/email/v1/send`, `/admin/projects/*` (`client`, `invite`, `members`, `settings`, `sites`, `setpassword`, `resetpassword`, `mfa/reset`); ba nhóm `/admin/projects/*`, `/scim/v2/Users` và `/email/v1/send` **tắt mặc định**.
- `ws-gateway`: `/ws/subscriptions-r4`, FHIRcast (`/fhircast/STU2|STU3`, `/hub`, `/ws/fhircast/{endpoint}`, tắt mặc định), probe `/health/*`.

### 5.2 FHIR operation còn thiếu

| Nhóm | Operation | Chặn bởi |
|---|---|---|
| Lâm sàng, dữ liệu | `Measure/$evaluate-measure`, `PlanDefinition/$apply` (các operation khác của nhóm này đã xong 2026-11-24: `$summary`, `$match`, `$extract`, `$graph`, `$csv`, `$refresh-reference-display`, `$resend`, `$expand-profile`) | không |
| C-CDA | `Patient/$ccda-export` | M3 |
| Lịch hẹn | `Appointment/$find`, `$book`, `$hold`, `$cancel`, `$confirm`, `$reschedule` | Q-F4 |
| Thanh toán | `Claim/$submit`, `Claim/$export`, `CoverageEligibilityRequest/$submit`, `ChargeItemDefinition/$apply`, `$set-accounts` | M1, Q-F5 |
| Nạp định nghĩa | `PackageRelease/$install`, `CodeSystem/$import`, `ConceptMap/$import` **có** (2026-11-23 và 2026-11-24). Chưa có: `$rebuild-base-definitions` | không; kế hoạch [modules/definitions-import.md](modules/definitions-import.md) |
| SMART Health Cards/Links | `$generate-smart-health-card`, `$generate-smart-health-link`, `$verify-smart-health-card`, `$resolve-smart-health-link`, route `/shl/` | không |
| Quản trị qua FHIR | (`Project/$init` và `ClientApplication/$rotate-secret` **xong** ở `auth` 2026-11-24 dạng `POST /admin/super/projects` và `POST /admin/projects/{id}/client/{clientId}/rotate-secret`) `$clone` | ranh giới với `auth` |
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
| `/dicom/PS3/*` (DICOMweb, khoảng 15 route) | **có** (`apps/dicomweb`, 2026-11-24) |
| `/admin/projects/:id/bot` | chưa (Bot ngoài phạm vi M1) |
| `/admin/projects/:projectId/secrets` | **xong** ở `auth` 2026-11-24 |
| `/admin/super/*` (reindex, purge, vacuum, valuesets, searchparameters, structuredefinitions, setpassword, tablesettings, rebuildprojectid, reloadcron, lambda-cleaner, removebotidjobsfromqueue, migrations, reconcile-db-schema-drift) | chưa có cả HTTP lẫn CLI |
| `/projects/:projectId/*`, `/api/*` (base URL theo project) | chưa |
| `/openapi.json`, `/.well-known/api-catalog` | chưa (Go có Swagger riêng) |
| WebSocket `agent`, `ai-realtime` | chưa; M3, M4 |

### 5.4 Worker và tính năng nền

| Worker Medplum | Ở Go |
|---|---|
| `bulk-export`, `reindex`, `subscription` (rest-hook) | có |
| `batch` (batch/transaction với `Prefer: respond-async`) | **có** (`FHIR_ASYNC_BATCH`, tắt mặc định; job `AsyncJob` có checkpoint, hủy bằng `DELETE job/{id}/status`) |
| `download` (tải attachment URL ngoài về Binary) | **có** (`pkg/fhirdownload`, `FHIR_AUTO_DOWNLOAD`; service `apps/fhir-downloader`) |
| `subscription-failure-tracker` (tự tắt Subscription lỗi kéo dài) | **có** (`FHIR_SUBSCRIPTION_AUTO_DISABLE`; chưa tạo `AuditEvent`, chỉ ghi log; `AuditEvent` cho thao tác REST đã có ở `FHIR_AUDIT`) |
| `cron` | chưa; M1 |
| `dispatch`, `set-accounts`, `dicom`, `lambda-cleaner`, `post-deploy-migration` | chưa; một phần không áp dụng |
| `seeds/` (SearchParameter, StructureDefinition, ValueSet dựng sẵn) | **có** (`fhir-server seed definitions`, `pkg/fhirseed`) |
| `cloud` | chỉ có S3 storage và SMTP; chưa có Azure, GCP, SES, Lambda, Fission, Textract (M5, M1, M4) |

### 5.5 Tính năng ngang còn thiếu

- Kiểm soát truy cập: resource `AccessPolicy` do `auth` lưu (schema `identity`) và dịch thành claim `fhir_access_policy`; `fhir-server` chỉ đọc claim, không lưu `AccessPolicy` (WP-2b). `ParsePolicy` đã **từ chối khóa lạ** (P8 M0 xong). Compartment Patient và `%patient` **xong** (P8, 2026-11-23). `hiddenFields`, `readonlyFields`, `interaction` và nhiều rule OR cho một loại **xong** (2026-11-24; `pkg/fhirauth/fieldrules.go`, `hiddensearch.go`; ws lọc theo người nhận). **Chưa có:** claim `fhir_membership` (điều kiện của `keyvalue`, `mcp`).
- Search: `_contained` (bị bỏ qua), SubscriptionTopic (R5), chained trong `_sort`/`_include`.
- Validator: profile/slicing và fixed/pattern **xong 2026-11-24** (`FHIR_VALIDATE_PROFILES`, tắt mặc định; còn discriminator `profile`, resource lồng, Extension theo profile kiểu); **chưa có** binding terminology, kiểm tra đích Reference (WP-5).
- `AuditEvent`: **xong 2026-11-24** cho thao tác REST (`pkg/fhiraudit`, `FHIR_AUDIT=off|writes|all`, tắt mặc định, ở `fhir-server` và `fhir-worker`). **Chưa có:** AuditEvent cho đăng nhập (`auth`), tự tắt Subscription (vẫn chỉ ghi log), thao tác bị từ chối, lưu giữ tự động.
