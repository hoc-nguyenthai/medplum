# Scenarios: Trivita FHIR Server (module 1533159)

Sinh tự động từ `generate.py`. Mỗi scenario mở đầu bằng bước token `client_credentials` (customHttp tới `{{authUrl}}/oauth2/token`).

## S-01 authn-negative — Xác thực: token sai hoặc thiếu

- Ưu tiên: P1; dữ liệu: `data/authn-negative.json|csv` (4 dòng, TC-AN-01 … TC-AN-04)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. GET Patient với token hợp lệ (đối chứng) — `GET /fhir/R4/Patient`
  4. GET Patient với token sai → 401 — `GET /fhir/R4/Patient`
  5. GET Patient không Authorization → 401 — `GET /fhir/R4/Patient`
  6. GET metadata kèm token sai → 200 (công khai) — `GET /fhir/R4/metadata`

## S-02 authz-matrix — Phân quyền theo scope token (RBAC + SMART)

- Ưu tiên: P1; dữ liệu: `data/authz-matrix.json|csv` (6 dòng, TC-AZ-01 … TC-AZ-06)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST Patient bằng token admin — `POST /fhir/R4/Patient`
  4. Token theo scope của dòng dữ liệu — `POST {{authUrl}}/oauth2/token`
  5. GET Patient/{id} — `GET /fhir/R4/Patient/{{patient_id}}`
  6. POST Patient — `POST /fhir/R4/Patient`
  7. PUT Patient/{id} — `PUT /fhir/R4/Patient/{{patient_id}}`
  8. POST Patient/{id}/$reindex (cần admin) — `POST /fhir/R4/Patient/{{patient_id}}/$reindex`
  9. DELETE Patient/{id} — `DELETE /fhir/R4/Patient/{{patient_id}}`
  10. (dọn) $expunge bằng token admin — `POST /fhir/R4/Patient/{{patient_id}}/$expunge`

## S-03 authz-project-header — Header X-Fhir-Project với token thường

- Ưu tiên: P2; dữ liệu: `data/authz-project-header.json|csv` (3 dòng, TC-PH-01 … TC-PH-03)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. GET Patient + X-Fhir-Project — `GET /fhir/R4/Patient`

## S-04 batch-transaction-flow — Batch và transaction (luồng)

- Ưu tiên: P1; dữ liệu: `data/batch-transaction-flow.json|csv` (1 dòng, TC-BF-01 … TC-BF-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST / batch (3 entry) — `POST /fhir/R4`
  4. POST / transaction (urn:uuid) — `POST /fhir/R4`
  5. GET Observation: subject đã được viết lại thành Patient/{id} — `GET /fhir/R4/Observation/{{obs_id}}`
  6. POST / transaction có entry lỗi → lỗi, rollback — `POST /fhir/R4`
  7. GET Patient?identifier (rollback: không có) — `GET /fhir/R4/Patient?identifier=urn:trivita:qa-mrn|tx-{{case_id}}-{{$timestamp}}&_total=accurate`

## S-05 bulk-export-flow — Bulk export $export (async) đầu-cuối

- Ưu tiên: P1; dữ liệu: `data/bulk-export-flow.json|csv` (1 dòng, TC-BX-01 … TC-BX-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST Patient (đảm bảo có dữ liệu) — `POST /fhir/R4/Patient`
  4. GET $export (Prefer: respond-async) — `GET /fhir/R4/$export`
  5. GET bulkdata/{id}/status (lần 1) — `GET /fhir/R4/bulkdata/{{job_id}}/status`
  6. GET bulkdata/{id}/status (lần 2) — `GET /fhir/R4/bulkdata/{{job_id}}/status`
  7. GET bulkdata/{id}/status (lần 3) — `GET /fhir/R4/bulkdata/{{job_id}}/status`
  8. GET bulkdata/{id}/status → 200 manifest — `GET /fhir/R4/bulkdata/{{job_id}}/status`
  9. GET Binary/{id} (ndjson) — `GET /fhir/R4/Binary/{{bin_id}}`
  10. DELETE bulkdata/{id}/status → 202 — `DELETE /fhir/R4/bulkdata/{{job_id}}/status`
  11. GET status sau xóa → 404/410 — `GET /fhir/R4/bulkdata/{{job_id}}/status`

## S-06 bulk-export-negative — $export: tham số sai

- Ưu tiên: P1; dữ liệu: `data/bulk-export-negative.json|csv` (5 dòng, TC-BXN-01 … TC-BXN-05)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. GET $export theo data — `GET /fhir/R4/$export`

## S-07 bundle-negative — POST / (batch/transaction): Bundle sai

- Ưu tiên: P1; dữ liệu: `data/bundle-negative.json|csv` (14 dòng, TC-BN-01 … TC-BN-14)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST / theo data — `POST /fhir/R4`

## S-08 code-system-import — CodeSystem/{id}/$import (lô nhỏ)

- Ưu tiên: P2; dữ liệu: `data/code-system-import.json|csv` (6 dòng, TC-CI-01 … TC-CI-06)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST CodeSystem content=not-present — `POST /fhir/R4/CodeSystem`
  4. POST CodeSystem/{id}/$import theo data — `POST /fhir/R4/CodeSystem/{{cs_id}}/$import`
  5. POST $import lần hai cùng dữ liệu (idempotent với ca hợp lệ, cùng lỗi với ca sai) — `POST /fhir/R4/CodeSystem/{{cs_id}}/$import`

## S-09 conditional-create-negative — POST If-None-Exist: điều kiện sai

- Ưu tiên: P1; dữ liệu: `data/conditional-create-negative.json|csv` (5 dòng, TC-CC-01 … TC-CC-05)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST Patient + If-None-Exist theo data — `POST /fhir/R4/Patient`

## S-10 conditional-update-negative — PUT /{resourceType}?query: điều kiện sai

- Ưu tiên: P1; dữ liệu: `data/conditional-update-negative.json|csv` (6 dòng, TC-CU-01 … TC-CU-06)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. PUT /Patient?{cond} theo data — `PUT /fhir/R4/Patient?{{cond}}`

## S-11 conditional-write-flow — Ghi có điều kiện (If-None-Exist, PUT theo query)

- Ưu tiên: P1; dữ liệu: `data/conditional-write-flow.json|csv` (1 dòng, TC-CW-01 … TC-CW-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST Patient (ident mới) — `POST /fhir/R4/Patient`
  4. POST If-None-Exist (1 khớp) → 200, không tạo mới — `POST /fhir/R4/Patient`
  5. POST cùng ident không điều kiện → 201 (tạo trùng) — `POST /fhir/R4/Patient`
  6. POST If-None-Exist (2 khớp) → 412 — `POST /fhir/R4/Patient`
  7. PUT ?identifier (2 khớp) → 412 — `PUT /fhir/R4/Patient?identifier=urn:trivita:qa-mrn|{{ident}}`
  8. PUT ?identifier (0 khớp) → 201 tạo mới — `PUT /fhir/R4/Patient?identifier=urn:trivita:qa-mrn|{{ident}}-moi`
  9. PUT ?identifier (1 khớp) → 200 cập nhật cùng id — `PUT /fhir/R4/Patient?identifier=urn:trivita:qa-mrn|{{ident}}-moi`
  10. PUT ?identifier (1 khớp) + If-Match cũ → 412 — `PUT /fhir/R4/Patient?identifier=urn:trivita:qa-mrn|{{ident}}-moi`
  11. PUT ?identifier (1 khớp) + id lệch → 400 — `PUT /fhir/R4/Patient?identifier=urn:trivita:qa-mrn|{{ident}}-moi`

## S-12 crud-lifecycle — Vòng đời Patient (CRUD, history, vread, patch, expunge)

- Ưu tiên: P0; dữ liệu: `data/crud-lifecycle.json|csv` (8 dòng, TC-CR-01 … TC-CR-08)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST Patient — `POST /fhir/R4/Patient`
  4. GET Patient/{id} — `GET /fhir/R4/Patient/{{patient_id}}`
  5. PUT Patient/{id} (đổi gender) — `PUT /fhir/R4/Patient/{{patient_id}}`
  6. GET _history (2 phiên bản) — `GET /fhir/R4/Patient/{{patient_id}}/_history`
  7. GET _history/{v1} (vread bản đầu) — `GET /fhir/R4/Patient/{{patient_id}}/_history/{{v1}}`
  8. PATCH Patient/{id} (JSON Patch) — `PATCH /fhir/R4/Patient/{{patient_id}}`
  9. DELETE Patient/{id} → 204 — `DELETE /fhir/R4/Patient/{{patient_id}}`
  10. GET Patient đã xóa → 410 — `GET /fhir/R4/Patient/{{patient_id}}`
  11. DELETE lần hai → 204 (không sinh phiên bản) — `DELETE /fhir/R4/Patient/{{patient_id}}`
  12. GET _history sau xóa (4 phiên bản) — `GET /fhir/R4/Patient/{{patient_id}}/_history`
  13. POST $expunge → 200 — `POST /fhir/R4/Patient/{{patient_id}}/$expunge`
  14. GET sau expunge → 404 — `GET /fhir/R4/Patient/{{patient_id}}`
  15. POST $expunge lần hai → 404 — `POST /fhir/R4/Patient/{{patient_id}}/$expunge`

## S-13 everything — Patient/{id}/$everything

- Ưu tiên: P1; dữ liệu: `data/everything.json|csv` (10 dòng, TC-EV-01 … TC-EV-10)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST Patient — `POST /fhir/R4/Patient`
  4. (setup) POST Observation — `POST /fhir/R4/Observation`
  5. (setup) POST Condition — `POST /fhir/R4/Condition`
  6. GET Patient/{id}/$everything theo data — `GET /fhir/R4/Patient/{{patient_id}}/$everything`
  7. DELETE Patient (xóa mềm) — `DELETE /fhir/R4/Patient/{{patient_id}}`
  8. $everything của Patient đã xóa → 410 — `GET /fhir/R4/Patient/{{patient_id}}/$everything`
  9. $everything của id lạ → 404 — `GET /fhir/R4/Patient/00000000-0000-4000-8000-000000000000/$everything`

## S-14 graphql — POST $graphql (chỉ đọc)

- Ưu tiên: P1; dữ liệu: `data/graphql.json|csv` (11 dòng, TC-GQ-01 … TC-GQ-11)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST Patient — `POST /fhir/R4/Patient`
  4. POST $graphql theo data — `POST /fhir/R4/$graphql`

## S-15 patch-negative — PATCH Patient: JSON Patch hợp lệ và lỗi

- Ưu tiên: P1; dữ liệu: `data/patch-negative.json|csv` (15 dòng, TC-PN-01 … TC-PN-15)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST Patient — `POST /fhir/R4/Patient`
  4. PATCH Patient/{id} theo data — `PATCH /fhir/R4/Patient/{{patient_id}}`

## S-16 project-isolation — Cô lập dữ liệu giữa hai project

- Ưu tiên: P1; dữ liệu: `data/project-isolation.json|csv` (1 dòng, TC-PI-01 … TC-PI-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (A) POST Patient — `POST /fhir/R4/Patient`
  4. Token project B — `POST {{authUrl}}/oauth2/token`
  5. (B) GET Patient/{id} → 404 — `GET /fhir/R4/Patient/{{patient_id}}`
  6. (B) PUT Patient/{id} → 404 — `PUT /fhir/R4/Patient/{{patient_id}}`
  7. (B) DELETE Patient/{id} → 404 — `DELETE /fhir/R4/Patient/{{patient_id}}`
  8. (B) GET _history → 404 — `GET /fhir/R4/Patient/{{patient_id}}/_history`
  9. (B) GET $everything → 404 — `GET /fhir/R4/Patient/{{patient_id}}/$everything`
  10. (B) tìm theo family của A → 0 kết quả — `GET /fhir/R4/Patient?family={{marker}}&_total=accurate`
  11. (A) vẫn đọc được → 200 — `GET /fhir/R4/Patient/{{patient_id}}`

## S-17 rate-limits-flow — Project/{id}/$rate-limits

- Ưu tiên: P2; dữ liệu: `data/rate-limits-flow.json|csv` (1 dòng, TC-RL-01 … TC-RL-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. GET Project/{projectId}/$rate-limits — `GET /fhir/R4/Project/{{projectId}}/$rate-limits`
  4. GET Project/khac/$rate-limits → 403 — `GET /fhir/R4/Project/project-khac/$rate-limits`
  5. GET $rate-limits không token → 401 — `GET /fhir/R4/Project/{{projectId}}/$rate-limits`

## S-18 search-post — POST /{resourceType}/_search (form)

- Ưu tiên: P1; dữ liệu: `data/search-post.json|csv` (6 dòng, TC-SP-01 … TC-SP-06)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) Patient 1 — `POST /fhir/R4/Patient`
  4. (setup) Patient 2 — `POST /fhir/R4/Patient`
  5. POST Patient/_search (form) — `POST /fhir/R4/Patient/_search`

## S-19 search — Tìm kiếm Patient: lọc, sắp xếp, phân trang, lỗi

- Ưu tiên: P1; dữ liệu: `data/search.json|csv` (38 dòng, TC-SE-01 … TC-SE-38)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) Patient 1 (male, 1980-01-01) — `POST /fhir/R4/Patient`
  4. (setup) Patient 2 (female, 1990-06-15) — `POST /fhir/R4/Patient`
  5. (setup) Patient 3 (female, 2000-12-31) — `POST /fhir/R4/Patient`
  6. GET /Patient?family={marker}{query} — `GET /fhir/R4/Patient?family={{marker}}{{query}}`

## S-20 smoke — Smoke fhir-server

- Ưu tiên: P0; dữ liệu: `data/smoke.json|csv` (1 dòng, TC-SM-01 … TC-SM-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. GET /healthz — `GET /healthz`
  4. GET /readyz — `GET /readyz`
  5. GET /fhir/R4/metadata (công khai) — `GET /fhir/R4/metadata`
  6. GET Patient không token → 401 — `GET /fhir/R4/Patient`
  7. GET Patient?_count=1 có token — `GET /fhir/R4/Patient`

## S-21 status-unknown-id — bulkdata/job status: id không tồn tại

- Ưu tiên: P2; dữ liệu: `data/status-unknown-id.json|csv` (5 dòng, TC-ST-01 … TC-ST-05)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. GET bulkdata/{id}/status — `GET /fhir/R4/bulkdata/{{job_id}}/status`
  4. DELETE bulkdata/{id}/status — `DELETE /fhir/R4/bulkdata/{{job_id}}/status`
  5. GET job/{id}/status — `GET /fhir/R4/job/{{job_id}}/status`

## S-22 terminology-expand — ValueSet/$expand

- Ưu tiên: P1; dữ liệu: `data/terminology-expand.json|csv` (10 dòng, TC-EX-01 … TC-EX-10)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST CodeSystem (A > A1, B) — `POST /fhir/R4/CodeSystem`
  4. (setup) POST ValueSet include CodeSystem — `POST /fhir/R4/ValueSet`
  5. GET ValueSet/$expand theo data — `GET /fhir/R4/ValueSet/$expand`

## S-23 terminology-lookup — CodeSystem/$lookup

- Ưu tiên: P1; dữ liệu: `data/terminology-lookup.json|csv` (10 dòng, TC-LK-01 … TC-LK-10)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. (setup) POST CodeSystem (A > A1, B) — `POST /fhir/R4/CodeSystem`
  4. GET CodeSystem/$lookup theo data — `GET /fhir/R4/CodeSystem/$lookup`

## S-24 update-rules — PUT/GET: If-Match, If-None-Match, Prefer, id lệch

- Ưu tiên: P1; dữ liệu: `data/update-rules.json|csv` (1 dòng, TC-UR-01 … TC-UR-01)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST Patient — `POST /fhir/R4/Patient`
  4. (setup) đọc v1 — `GET /fhir/R4/Patient/{{patient_id}}`
  5. PUT If-Match sai → 412 — `PUT /fhir/R4/Patient/{{patient_id}}`
  6. PUT If-Match đúng (W/"v1") → 200 — `PUT /fhir/R4/Patient/{{patient_id}}`
  7. PUT If-Match v1 (đã cũ) → 412 nêu phiên bản hiện tại — `PUT /fhir/R4/Patient/{{patient_id}}`
  8. PUT If-Match * → 412 — `PUT /fhir/R4/Patient/{{patient_id}}`
  9. PUT id thân lệch đường dẫn → 400 — `PUT /fhir/R4/Patient/{{patient_id}}`
  10. PUT id chưa tồn tại → 404 (update-as-create tắt) — `PUT /fhir/R4/Patient/00000000-0000-4000-8000-000000000000`
  11. GET If-None-Match khớp → 304 — `GET /fhir/R4/Patient/{{patient_id}}`
  12. GET If-None-Match khác → 200 — `GET /fhir/R4/Patient/{{patient_id}}`
  13. PUT y hệt → 200 và ETag mới (vẫn sinh phiên bản) — `PUT /fhir/R4/Patient/{{patient_id}}`
  14. PUT Prefer return=minimal → 200 thân rỗng — `PUT /fhir/R4/Patient/{{patient_id}}`
  15. PUT Prefer return=OperationOutcome → thân OperationOutcome — `PUT /fhir/R4/Patient/{{patient_id}}`

## S-25 validate-operation — POST /{resourceType}/$validate

- Ưu tiên: P1; dữ liệu: `data/validate-operation.json|csv` (11 dòng, TC-VA-01 … TC-VA-11)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST /{resourceType}/$validate theo data — `POST /fhir/R4/{{resource_type}}/$validate`

## S-26 write-validation — POST resource: validate thân và lỗi

- Ưu tiên: P1; dữ liệu: `data/write-validation.json|csv` (16 dòng, TC-WV-01 … TC-WV-16)
- Các bước:
  1. Đăng nhập admin project (auth /auth/login) — `POST {{authUrl}}/auth/login`
  2. Đổi code lấy token admin (authorization_code) — `POST {{authUrl}}/oauth2/token`
  3. POST /{resourceType} theo data — `POST /fhir/R4/{{resource_type}}`

## Chỉ thiết kế (không dựng, kèm lý do)

- **CodeSystem/$import và ConceptMap/$import lô lớn (>10000 phần tử, nhiều lô)**: nặng, ghi nhiều dòng kho concept; chỉ lô nhỏ ở code-system-import. ConceptMap/$import chưa có scenario (cùng khuôn: Parameters.mapping, lỗi M3-M9 ở cases/operation.md)
- **PackageRelease/{id}/$install**: ghi hàng loạt resource từ gói Binary 64 MiB, không idempotent (cài lại tạo bản sao); cần gói mẫu riêng
- **{resourceType}/$reindex cả loại (202 + job nền)**: tạo AsyncJob quét toàn loại của project, chạy ở fhir-worker; chỉ chạy được khi chủ project đồng ý; $reindex một resource đã phủ ở authz-matrix
- **POST / batch async (Prefer: respond-async)**: chỉ có tác dụng khi FHIR_ASYNC_BATCH=true (mặc định tắt) và cần fhir-worker
- **PUT update-as-create (201 với id client chọn)**: chỉ khi FHIR_UPDATE_AS_CREATE=true (mặc định tắt: ca 404 đã có ở update-rules)
- **413 thân quá 16 MiB; 429 hạn mức IP/điểm; Project/$rate-limits enabled=true**: cần tạo thân 16 MiB hoặc đổi cấu hình FHIR_RATE_LIMIT/FHIR_QUOTA_*; làm bằng test Go (pkg/ratelimit) thay vì Apidog
- **AccessPolicy (criteria, readonly, hiddenFields, interaction) và compartment patient/**: AccessPolicy không có API tạo ở auth (chỉ seed 6 policy mặc định), nên không dựng được client có policy riêng bằng Apidog; đã phủ ở test Go e2e/bootstrap
- **Binary thô (RawBinary: POST/GET byte, ký URL) và $export tải qua /storage**: phụ thuộc FHIR_BINARY_STORAGE và URL ký; luồng đọc ndjson từ Binary đã nằm ở bulk-export-flow
- **Subscription rest-hook / websocket**: cần máy nhận webhook và ws-gateway; thuộc scenario full-flow xuyên hệ thống, không thuộc module này
- **Super-admin: X-Fhir-Project hợp lệ và audit**: cần project bật superAdmin (auth project super); chỉ ca token thường bị 403 ở authz-project-header
