# Danh sách việc

Cập nhật 2026-11-23 (thêm mục Ưu tiên 1b từ đối chiếu route, [coverage-matrix.md](coverage-matrix.md) mục 5). Thứ tự ưu tiên do người dùng chốt: **hoàn thành các module và gắn thành service API trước**; việc hardening riêng lẻ (như khóa tài khoản) làm sau. Chi tiết từng việc nằm ở tài liệu được dẫn; file này chỉ là sổ theo dõi.

Trạng thái: `[ ]` chưa làm, `[~]` đang chặn bởi quyết định, `[x]` xong.

## Ưu tiên 1: hoàn thành module và gắn thành service API

| # | Việc | Trạng thái | Ghi chú / chặn bởi | Chi tiết |
|---|---|---|---|---|
| 1 | `auth` P3: token và OAuth2 (`/oauth2/*`, `.well-known`, `Authenticator`, claim `fhir_membership`) | [x] xong P3 (B1-B9, 2026-11-21) | D-12 đến D-17 đã duyệt 2026-11-21. **Xong B1 đến B6**: khóa/JWKS/well-known, `client_credentials`, `authorization_code` + PKCE (code lưu băm, chiếm code nguyên tử), `refresh_token` (xoay, ân hạn, thu hồi khi dùng lại), `userinfo`/`introspect`/`logout`, `authorize` (cookie phiên, `id_token_hint`, `prompt`); token đã qua `fhirauth.Verifier` trong test. Xong thêm B7 (`private_key_jwt`, `pre-authorized_code`, `register`, tách `pkg/safehttp`) và B8 (test hai tiến trình `make test-e2e`, hợp đồng `oauth.md`, `docker-compose.auth.yml`). Còn B9 rà soát bảo mật. Claim `fhir_membership` và `Principal.Membership` chưa làm | [auth/p3-oauth2-tokens.md](../../apps/auth/docs/plans/p3-oauth2-tokens.md) |
| 2 | `auth` P4/P5: đăng ký, reCAPTCHA, MFA | [x] P4 và P5 xong; còn P6/P7 (#3); P4 đã duyệt, **P4 và P5 xong (2026-11-22)**: `resetpassword`, `setpassword`, `verifyemail`, `changepassword`, `newuser` + reCAPTCHA, `newproject` + thư chào, `newpatient`, bộ test S1-S17 và e2e; P5 (MFA: TOTP và mã email, hạn mức thử, mã hóa `mfaSecret`, Q8 ở token endpoint, e2e) | Kênh gửi thư thật chưa có (D4) | [auth/p4-p5-register-mfa.md](../../apps/auth/docs/plans/p4-p5-register-mfa.md) |
| 3 | `auth` P6/P7: đăng nhập ngoài, `domain add`, `/admin/*` HTTP, SCIM | [x] P6, P7 (HTTP, `sites`) và SCIM XONG (2026-11-22: `/admin/projects/*` và `/scim/v2/Users` mặc định tắt) | P7 HTTP đã hoãn (chỉ CLI); Q-SC-1 (Basic auth cho SCIM) | [auth/p6-p7-external-admin.md](../../apps/auth/docs/plans/p6-p7-external-admin.md) |
| 4 | Gói dùng chung: ~~`pkg/safehttp`~~ (xong, B7), ~~`pkg/mailer`~~ (xong 2026-11-22: lõi, SMTP, `smtptest`; `auth` dùng qua `AUTH_MAIL_PROVIDER=smtp`), ~~`pkg/binarystore`~~ (xong S1, S2, S5 2026-11-22: `Store` FS/Memory, `CheckMetadata`, `Signer` RSA-SHA256, `storetest`), `Mutate` dùng chung, `SessionBus` | [~] còn `Mutate` dùng chung, `SessionBus`; S3a, S3b xong 2026-11-22 (`pkg/fhirbinary`, `fhir-server` bật bằng `FHIR_BINARY_STORAGE`); S3c (`$presigned-url`), S6 (`rewriteAttachments`) và E5 (email đính kèm `Binary/<id>`) xong 2026-11-22; S7 (adapter S3, `pkg/binarystore/s3store`) xong 2026-11-23; còn quét mồ côi, kiểm log không ghi `Signature` | Điều kiện cho storage, webhook, dicom | [modules/README.md](modules/README.md) |
| 5 | `email` + `storage` | [~] `pkg/mailer` (E2, E3) và route `/email/v1/send` (E4) xong; còn `storage` và E5 | M5 (object storage) chưa chốt | [modules/email-storage.md](modules/email-storage.md) |
| 6 | `keyvalue`, `mcp` | [ ] | | [modules/keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md) |
| 7 | `scim`, `ws` còn lại (hidden fields, `unbind-from-token`, hạn mức bind), `fhircast` | [x] xong, trừ lọc `hiddenFields` từng trường (chuyển sang #20) | `scim` xong trong `auth` (2026-11-22, `/scim/v2/Users` tắt mặc định); **WS-1..WS-9 xong (2026-11-22)**: `ws` (kể cả `unbind-from-token` và hạn mức bind `FHIR_WS_MAX_BINDINGS_PER_USER`) và `fhircast` đã chạy trong `apps/ws-gateway` (token của `auth` kèm `hub.url`, `hub.topic`); `subscriptions-r4` kiểm quyền lúc phát. Kế hoạch tách service: [modules/split-scim-ws-services.md](modules/split-scim-ws-services.md) (ws → `apps/ws-gateway`; scim ở lại `auth`) | [modules/scim-ws-fhircast.md](modules/scim-ws-fhircast.md) |
| 8 | Gói công việc kiểm kê `fhir` (17 WP; WP-1 lệch REST, các WP khác) | [ ] | WP-2a (SMART v2) xong | [modules/fhir-parity-audit.md](modules/fhir-parity-audit.md) |
| 9 | `cds`, `webhook`, `cron`, `Bot/$execute` | [~] | Chặn bởi M1 (runtime Bot) | [cds-dicom/cds.md](cds-dicom/cds.md) |
| 10 | `dicom` | [~] | Cần #4, #5; quyết định H1 đến H5 | [cds-dicom/dicom.md](cds-dicom/dicom.md) |
| 11 | Chốt M1 đến M5 và H1 đến H5; lập kế hoạch mức C (`bots`, `workers`, `cloud`, `ai`) | [ ] | Quyết định của người dùng | [coverage-matrix.md](coverage-matrix.md) |
| 12 | (một phần xong: B8 test hai tiến trình; B9 đã chạy thử `docker-compose.auth.yml` bằng Docker OK với auth + fhir-server + postgres; còn `dicomweb`, K8s) Đóng gói và chạy thành service: compose/K8s cho `fhir-server`, `auth`, `dicomweb`; `auth` cấp token cho `fhir-server` qua JWKS; kiểm đầu-cuối cả chuỗi | [ ] | Làm khi #1 xong; liên quan Q-SC-1 và `Principal.Membership` | [apps-auth.md](../../apps/auth/docs/plans/apps-auth.md) |
| 12b | Tổ chức microservice theo nghiệp vụ, dùng chung một PostgreSQL (schema và role riêng cho từng service) | [ ] | D-DB-1, D-DB-2 và D-DB-3 đã chốt (D-DB-3: chưa tách `fhir-worker`, chờ đo tải); **P1, P2 và P3a xong (2026-11-22)**; còn D-DB-4 và D-DB-5 | [microservices-shared-db.md](microservices-shared-db.md) |
| 12c | **Đo tải export song song với API** (quyết định có mở P3b `fhir-worker` hay không): chạy `export` lớn cùng lúc với tải API, ghi p95/p99 của API, CPU và bộ nhớ, so với đường cơ sở không có export | [ ] | Điều kiện mở lại P3b (D-DB-3). Cần chốt: kích thước dữ liệu mẫu, môi trường đo (máy cục bộ hay có máy riêng), ngưỡng "bị ảnh hưởng" (ví dụ p95 API tăng quá X%). Kết quả tính được ngay cho `FHIR_JOB_CONCURRENCY` | [microservices-shared-db.md](microservices-shared-db.md) mục 14 |

## Thứ tự thực hiện đã chốt (2026-11-23, người dùng duyệt)

Áp dụng cho các việc còn lại của Ưu tiên 1 và 1b; nguyên tắc: hoàn thành module và gắn thành service API trước, hardening (Ưu tiên 2) làm sau. Việc không bị chặn bởi quyết định đi trước.

| Thứ tự | Việc | Số việc | Ghi chú |
|---|---|---|---|
| 1 | Compartment Patient (P8 M1 đến M5) | #16, #20 | Đã duyệt trước đó. **XONG 2026-11-23** (M0 đến M5) (`_compartment` ở `pkg/fhirsearch`, đẩy điều kiện chính sách xuống kho, `$everything` với token giới hạn, OR nhiều rule cùng loại, `auth` giải `%patient`/`%profile` sau cờ `AUTH_PATIENT_COMPARTMENT_ENABLED` mặc định tắt, e2e hai tiến trình); tài liệu, thứ tự deploy và quy trình tạo Patient cùng id ở runbook auth §12. Còn hở: chưa tự đồng bộ Patient sang fhir-server |
| 2 | Nạp định nghĩa: `$import`, `PackageRelease/$install`, seed | #18 | Điều kiện để có SNOMED/LOINC. **Đang làm**: D1 (`$install`) xong 2026-11-23, D2 (`CodeSystem/$import` + bảng dẫn xuất) xong 2026-11-24, D3 (lookup, `$validate-code`, `$expand` đọc bảng mới) xong 2026-11-24, D4 (`ConceptMap/$import`) xong 2026-11-24, D5 (`fhir-server seed definitions`) xong 2026-11-24; còn e2e và kiểm PostgreSQL |
| 3 | Worker `download` (B1, B2) | #17 | Đóng nốt #17 |
| 4 | Operation lâm sàng không bị chặn | #19 | `$summary`, `$match`, `$extract`, `$graph`, `$csv`, `$refresh-reference-display`, `$resend`, `$expand-profile` |
| 5 | `keyvalue`, `mcp` | #6 | `mcp` cần claim `fhir_membership`, làm sau thứ tự 1 |
| 6 | Quản trị qua FHIR, `/admin/super/*` | #21 | Cần vạch ranh giới với `auth` trước |
| 7 | Base URL theo project, `/openapi.json`, `api-catalog` | #22 | Chờ Q-F1 |
| 8 | SMART Health Cards/Links | #23 | Cần khóa ký |
| 9 | Operation vận hành và DB | #25 | Ưu tiên thấp |
| sau cùng | Hardening: khóa tài khoản, hạn mức dùng chung nhiều replica | #13, #14 | Ưu tiên 2. #12c (đo tải) chạy khi tiện, quyết định có tách `fhir-worker` |
| chặn | Bot, `cds`, `webhook`, `cron`, `$execute`, thanh toán, xử lý HL7 | #9, #26 | Chờ **M1**; `agent`/`hl7`/`ccda` chờ **M3**; `ai` chờ M4; cloud ngoài S3 chờ M5; `dicom` chờ H1 đến H5. Đề nghị chốt M1 sớm vì chặn nhiều nhất |

## Ưu tiên 1b: phần còn thiếu theo đối chiếu route (2026-11-23)

Nguồn: [coverage-matrix.md](coverage-matrix.md) mục 5. Thứ tự thực hiện đã chốt ở mục ngay trên (2026-11-23). Từng việc nhỏ (route, operation) để đánh dấu: [go-gap-checklist.md](go-gap-checklist.md).

| # | Việc | Trạng thái | Ghi chú / chặn bởi | Chi tiết |
|---|---|---|---|---|
| 17 | Batch/transaction async (`Prefer: respond-async`), worker `download`, tự tắt Subscription lỗi kéo dài (`subscription-failure-tracker`) | [ ] một phần: **xong** A1 đến A3 (batch async có checkpoint, `FHIR_ASYNC_BATCH`, tắt mặc định) và C1, C2 (tự tắt Subscription, `FHIR_SUBSCRIPTION_AUTO_DISABLE`, bộ đếm bộ nhớ và Redis); **chưa** B1, B2 (worker `download`) | B1, B2 chưa làm (không có worker tải trong mã); Q-17.1 đến Q-17.5 theo đề xuất trong kế hoạch | [modules/p17-async-batch-download-autodisable.md](modules/p17-async-batch-download-autodisable.md) |
| 18 | Nạp định nghĩa: `CodeSystem/$import`, `ConceptMap/$import`, `PackageRelease/$install`, bước seed SearchParameter/StructureDefinition/ValueSet | [~] D1 (`$install`) xong 2026-11-23, D2 (`CodeSystem/$import`) D3 (terminology đọc kho concept) D4 (`ConceptMap/$import`) và D5 (`fhir-server seed definitions`) xong 2026-11-24; còn e2e hai tiến trình và kiểm PostgreSQL theo [modules/definitions-import.md](modules/definitions-import.md) (Q-D1 = bảng dẫn xuất mới, đã duyệt) | không chặn; điều kiện để có SNOMED/LOINC | mục 5.2, 5.4 |
| 19 | Operation lâm sàng không bị chặn: `Patient/$summary`, `QuestionnaireResponse/$extract`, `Patient/$match`, `$graph`, `$csv`, `$refresh-reference-display`, `$resend`, `StructureDefinition/$expand-profile` | [ ] | WP-9 | mục 5.2 |
| 20 | Kiểm soát truy cập trong server: resource `AccessPolicy`, compartment `patient/` và `%patient`, `hiddenFields` | [~] compartment và `%patient` xong (P8); còn `hiddenFields` | WP-2b, WP-2c; trùng #16, gộp khi lập kế hoạch | mục 5.5 |
| 21 | Quản trị: `Project/$init`, `$clone`, `$archive`, `$restore`, `$rate-limits`, `User/$rescope`, `$update-email`, `ClientApplication/$rotate-secret`, `$smart-launch`, `/admin/projects/:id/bot`, `/:projectId/secrets`, lệnh CLI thay `/admin/super/*` | [ ] | chốt ranh giới `auth` và `fhir-server`; WP-15 | mục 5.2, 5.3 |
| 22 | Base URL theo project (`/projects/:projectId/*`, `/api/*`), `/openapi.json`, `/.well-known/api-catalog` | [ ] | Q-F1 (mức tương thích với client Medplum) | mục 5.3 |
| 23 | SMART Health Cards/Links (`$generate-*`, `$verify-smart-health-card`, `$resolve-smart-health-link`, `/shl/`) | [ ] | không chặn; cần khóa ký | mục 5.2 |
| 24 | Lịch hẹn (`Appointment/$find`, `$book`, `$hold`, `$cancel`, `$confirm`, `$reschedule`) | [~] | Q-F4; WP-10 | mục 5.2 |
| 25 | Operation vận hành và DB (`$explain`, `$db-*`, `$get-ws-sub-*`, `$clear-all-ws-subs`, `$versions`, `Binary/$scan`) | [ ] | ưu tiên thấp | mục 5.2 |
| 26 | Phần chờ quyết định: Bot (`$execute`, `$deploy`, `cron`, thanh toán, `$set-accounts`) M1; Agent và `ws/agent` M3; `$ccda-export` M3; `$ai`, `$aws-textract`, `ws/ai-realtime` M4; cloud ngoài S3 M5 | [~] | trùng #9, #11; ghi để đủ danh sách | mục 5.2 đến 5.4 |

## Ưu tiên 2: hardening, làm sau khi module đã chạy thành service

| # | Việc | Trạng thái | Ghi chú | Chi tiết |
|---|---|---|---|---|
| 13 | **Khóa tài khoản theo số lần đăng nhập sai** | [ ] | Xem mục dưới | dưới |
| 14 | Hạn mức dùng chung nhiều replica (Redis) thay cho bộ đếm trong bộ nhớ; tách limiter dùng chung ra `pkg` | [ ] | Hiện N replica thì tổng hạn mức gấp N lần, ở cả `fhir-server` và `auth` | |
| 15 | Cấm tiền tố `fhir.*` trong scope client khai ở `auth` (client `client_credentials` xin được `fhir.admin`) | [x] | Xong cùng B2 (2026-11-21): `domain.ValidateRequestedScope`, có test đột biến | [auth/p3-oauth2-tokens.md](../../apps/auth/docs/plans/p3-oauth2-tokens.md) |
| 16 | Compartment (`patient/` scope, AccessPolicy `%patient`) ở `pkg/fhirauth`, `hiddenFields` và `interaction` | [~] compartment và `%patient` xong (P8); còn `hiddenFields`, `interaction` | Token Patient đọc được dữ liệu trong compartment khi bật cờ (mặc định tắt: vẫn `deny`); claim chính sách mặc định ~5 KB | [auth/README.md](../../apps/auth/docs/plans/README.md) mục 5.2 |

Việc #15 thực chất là điều kiện của #1, không phải việc hậu kỳ; ghi ở đây để không bị quên nhưng phải làm cùng P3.

### #13 Khóa tài khoản theo số lần đăng nhập sai

Vì sao: hạn mức theo IP (đã có ở `auth`, 1 rps burst 10) chỉ làm chậm một IP; kẻ tấn công đổi IP vẫn dò được mật khẩu của một tài khoản cụ thể.

Phạm vi đề xuất (chưa chốt, cần duyệt trước khi làm):
- Đếm lần sai liên tiếp theo user (không theo IP); đạt ngưỡng (đề xuất 10) thì khóa tạm một khoảng (đề xuất 15 phút), thành công thì xóa bộ đếm.
- Không lộ tài khoản có tồn tại hay không: email lạ vẫn trả lỗi chung như hiện nay, và việc khóa không đổi thông báo lỗi khi sai mật khẩu.
- Lưu bộ đếm trong kho FHIR của `auth` (trường trên `User`) bằng `Mutate` có retry IfMatch, hoặc Redis nếu chạy nhiều replica; cần quyết định.
- Phải xử lý hai vấn đề: kẻ tấn công cố ý khóa tài khoản người khác (DoS), và đua nhau khi nhiều yêu cầu song song (kiểm trên PostgreSQL như các test hiện có).
- Có lệnh CLI mở khóa (`auth user unlock`), ghi nhật ký kiểm toán.

Câu hỏi cần chốt: ngưỡng và thời gian khóa; khóa vĩnh viễn hay tự mở; lưu ở kho FHIR hay Redis; có gửi email thông báo khi bị khóa (cần `pkg/mailer`, việc #4).

Lưu ý: không khẳng định Medplum có tính năng này; chưa đối chiếu với source Medplum 5.2.18, cần grep trước khi thiết kế.
