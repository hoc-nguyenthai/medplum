# Checklist: phần Go chưa có so với Medplum

Cập nhật 2026-11-23. Mỗi dòng là một việc cụ thể còn thiếu, đánh dấu `[x]` khi xong. Nguồn đối chiếu và chi tiết ở [coverage-matrix.md](coverage-matrix.md) mục 5 (bản Medplum dùng để so: `v5.2.17-5-ge7e7e4019`, cũ hơn tag `v5.2.18`). Cột "#" trỏ tới việc trong [task-list.md](task-list.md).

Ký hiệu chặn: **M1** runtime Bot, **M3** `agent`/`ccda`/`hl7`, **M4** AI, **M5** kho đối tượng ngoài S3 (xem [coverage-matrix.md](coverage-matrix.md) mục 3); **Q-F*** ở [modules/fhir-parity-audit.md](modules/fhir-parity-audit.md). Dòng không ghi chặn là làm được ngay.

## 1. Job nền và worker (#17)

Kế hoạch: [modules/p17-async-batch-download-autodisable.md](modules/p17-async-batch-download-autodisable.md).

- [x] Tự tắt Subscription lỗi kéo dài (`subscription-failure-tracker`): bản bộ nhớ (C1)
- [x] Tự tắt Subscription: bản Redis dùng chung nhiều replica (C2)
- [x] Trạng thái checkpoint dùng chung cho job (`Input.JobID`, `SaveJobState`) (A1)
- [x] `ExpireJobs` dọn cả job `batch?`; `outputBinaryIDs` đọc `valueReference` (A1)
- [x] Batch async `POST /fhir/R4` + `Prefer: respond-async` (A2)
- [x] Checkpoint, giữ id POST và hủy giữa chừng cho batch async (A3)
- [ ] Worker `download`: tải attachment URL ngoài về Binary (B1, B2)
- [ ] Extension `subscription-max-attempts` trên Subscription
- [ ] Worker `dispatch`, `set-accounts`, `dicom`, `lambda-cleaner`, `post-deploy-migration` — xét từng cái có cần không (một phần không áp dụng cho Go)
- [ ] `cron` (Bot chạy theo lịch) — chặn bởi **M1**

## 2. Nạp định nghĩa và terminology (#18)

- [ ] `CodeSystem/$import`
- [ ] `ConceptMap/$import`
- [ ] `PackageRelease/$install`
- [ ] `$rebuild-base-definitions`
- [ ] Bước seed SearchParameter, StructureDefinition, ValueSet dựng sẵn (thay `seeds/`)

## 3. Operation lâm sàng và dữ liệu (#19)

- [ ] `Patient/$summary` (IPS)
- [ ] `Patient/$match`
- [ ] `QuestionnaireResponse/$extract`
- [ ] `:type/:id/$graph` (cần GraphDefinition)
- [ ] `:type/$csv`
- [ ] `$refresh-reference-display`
- [ ] `:type/:id/$resend`
- [ ] `StructureDefinition/$expand-profile`
- [ ] `Measure/$evaluate-measure`
- [ ] `PlanDefinition/$apply`
- [ ] `Patient/$ccda-export` — chặn bởi **M3**

## 4. Kiểm soát truy cập (#20, trùng #16)

- [ ] Resource `AccessPolicy` lưu trong server (hiện chính sách chỉ đến từ claim token)
- [ ] Compartment `patient/` và biến `%patient` (token Patient đọc được dữ liệu lâm sàng của chính mình)
- [ ] `hiddenFields` và `readonlyFields`
- [ ] Nhiều rule cho một loại resource (OR giữa các rule)
- [ ] Claim `fhir_membership` và `Principal.Membership` (điều kiện cho `keyvalue`, `mcp`, `webhook`)

## 5. Quản trị project, user, client (#21)

- [ ] `Project/$init`
- [ ] `Project/:id/$clone`
- [ ] `Project/:id/$archive`
- [ ] `/$restore`
- [ ] `Project/:id/$rate-limits`
- [ ] `User/:id/$rescope`
- [ ] `User/:id/$update-email`
- [ ] `ClientApplication/:id/$rotate-secret`
- [ ] `ClientApplication/:id/$smart-launch`
- [ ] `/admin/projects/:id/bot` — chặn bởi **M1**
- [ ] `/admin/projects/:projectId/secrets`
- [ ] Lệnh CLI thay `/admin/super/*`: reindex, purge, vacuum, valuesets, searchparameters, structuredefinitions, setpassword, tablesettings, rebuildprojectid, migrations, reconcile-db-schema-drift

## 6. Module HTTP ngoài FHIR

- [ ] `/keyvalue/v1/:key` ([kế hoạch](modules/keyvalue-mcp-webhook.md); cần claim membership)
- [ ] `/mcp` (`/sse`, `/stream`) ([kế hoạch](modules/keyvalue-mcp-webhook.md); cần claim membership)
- [ ] DICOMweb `/dicom/PS3/*`, app riêng `dicomweb` ([kế hoạch](cds-dicom/dicom.md))
- [ ] `/webhook/:id` — chặn bởi **M1**
- [ ] `/cds-services` — chặn bởi **M1**
- [ ] Base URL theo project: `/projects/:projectId/*`, `/api/*` (#22; Q-F1)
- [ ] `/openapi.json` (#22)
- [ ] `/.well-known/api-catalog` (#22)
- [ ] `$versions`

## 7. SMART Health Cards/Links (#23)

- [ ] `Patient/:id/$generate-smart-health-card`
- [ ] `Patient/:id/$generate-smart-health-link`
- [ ] `$verify-smart-health-card`
- [ ] `$resolve-smart-health-link`
- [ ] Route `/shl/`

## 8. Lịch hẹn (#24, chặn bởi Q-F4)

- [ ] `Appointment/$find`
- [ ] `Appointment/$book`
- [ ] `Appointment/$hold`
- [ ] `Appointment/:id/$cancel`
- [ ] `Appointment/:id/$confirm`
- [ ] `Appointment/:id/$reschedule`

## 9. Thanh toán và bảo hiểm (chặn bởi M1, Q-F5)

- [ ] `Claim/$submit`
- [ ] `Claim/$export`
- [ ] `CoverageEligibilityRequest/$submit`
- [ ] `ChargeItemDefinition/:id/$apply`
- [ ] `:type/:id/$set-accounts`

## 10. Bot (chặn bởi M1)

- [ ] Chốt runtime Bot (HTTP ngoài, sidecar Node, hay port module)
- [ ] `Bot/$execute` (4 biến thể đường dẫn)
- [ ] `Bot/:id/$deploy`
- [ ] `Bot/$init`

## 11. Agent (chặn bởi M3)

- [ ] `Agent/$push`, `Agent/:id/$push`
- [ ] `Agent/$status`, `Agent/$bulk-status`, `Agent/$stats`
- [ ] `Agent/$reload-config`, `Agent/$upgrade`, `Agent/$fetch-logs`
- [ ] Websocket `agent`

## 12. AI (chặn bởi M4)

- [ ] `$ai`
- [ ] `:type/:id/$aws-textract`
- [ ] Websocket `ai-realtime`

## 13. Vận hành và CSDL (#25, ưu tiên thấp)

- [ ] `$explain`
- [ ] `$db-stats`, `$db-indexes`, `$db-index-bloat`, `$db-invalid-indexes`
- [ ] `$db-schema-diff`, `$db-column-statistics`, `$db-configure-indexes`, `$db-configure-column-statistics`
- [ ] `$get-ws-sub-stats`, `$get-ws-sub-project-stats`, `$clear-all-ws-subs`
- [ ] `Binary/:id/$scan`
- [ ] `AsyncJob/:id/$cancel` (hoặc ghi rõ `DELETE bulkdata/:id/status` là bản thay thế)

## 14. Tính năng ngang

- [ ] Search `_contained`
- [ ] Chained trong `_sort` và `_include`; `:iterate` lồng chained
- [ ] SubscriptionTopic (R5)
- [ ] Validator: profile và slicing
- [ ] Validator: fixed/pattern, binding terminology, kiểm tra đích Reference
- [ ] Ghi `AuditEvent` vào kho (hiện chỉ là dòng log)
- [ ] Hạn mức dùng chung nhiều replica qua Redis (#14)
- [ ] Khóa tài khoản theo số lần đăng nhập sai (#13)

## 15. Hạ tầng cloud (chặn bởi M5)

- [ ] Kho Binary Azure Blob
- [ ] Kho Binary Google Cloud Storage
- [ ] Gửi thư qua SES
- [ ] Quét dọn Binary mồ côi trong kho (S3, file)

## 16. Quyết định cần chốt trước

- [x] M1: runtime Bot — chốt 2026-11-24: ngoài phạm vi
- [x] M2: có chuyển dữ liệu từ CSDL Medplum đang chạy không — chốt 2026-11-24: không
- [x] M3: phạm vi `agent`, `ccda`, `hl7`, tích hợp bên thứ ba — chốt 2026-11-24: ngoài phạm vi
- [x] M4: nhà cung cấp AI — chốt 2026-11-24: ngoài phạm vi
- [x] M5: kho đối tượng ngoài S3 — chốt 2026-11-24: chỉ hệ thống tệp và S3
- [ ] Q-F1: mức tương thích với client Medplum (quyết định có làm base URL theo project, `openapi.json`)
- [ ] Q-17.1 đến Q-17.7 của kế hoạch #17
