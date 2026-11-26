# Import test data vào Apidog app — module Trivita FHIR Server

Branch: `ai/20261125-from-main-trivita-platform-tests` · Folder scenario: **Trivita FHIR Server** · Environment: **—**

Apidog CLI không upload được dòng test data (test-data create/update chỉ nhận metadata — đã kiểm tra apidog-cli 2.2.11 và 2.2.12), nên import trong app:
1. Chuyển sang branch ở trên, mở folder scenario rồi mở từng scenario trong bảng.
2. Ở phần Test Data của scenario, import file tương ứng (CSV hoặc JSON; dòng tiêu đề / khóa JSON = tên biến).
3. Giữ nguyên tên cột: step dùng `{{tên_cột}}`; `case_id`, `case_group`, `description`, `expected_status`, `expected_code` dùng trong assertion/script.
4. Khi chạy trong app, chọn environment ở trên và bộ test data vừa import.

| Scenario | ID | File | Dòng |
|---|---|---|---|
| Xác thực: token sai hoặc thiếu | 1998570 | `data/authn-negative.json` | 4 |
| Phân quyền theo scope token (RBAC + SMART) | 1998571 | `data/authz-matrix.json` | 6 |
| Header X-Fhir-Project với token thường | 1998576 | `data/authz-project-header.json` | 3 |
| Batch và transaction (luồng) | 1998580 | `data/batch-transaction-flow.json` | 1 |
| Bulk export $export (async) đầu-cuối | 1998583 | `data/bulk-export-flow.json` | 1 |
| $export: tham số sai | 1998585 | `data/bulk-export-negative.json` | 5 |
| POST / (batch/transaction): Bundle sai | 1998603 | `data/bundle-negative.json` | 14 |
| CodeSystem/{id}/$import (lô nhỏ) | 1998609 | `data/code-system-import.json` | 6 |
| POST If-None-Exist: điều kiện sai | 1998612 | `data/conditional-create-negative.json` | 5 |
| PUT /{resourceType}?query: điều kiện sai | 1998614 | `data/conditional-update-negative.json` | 6 |
| Ghi có điều kiện (If-None-Exist, PUT theo query) | 1998617 | `data/conditional-write-flow.json` | 1 |
| Vòng đời Patient (CRUD, history, vread, patch, expunge) | 1998620 | `data/crud-lifecycle.json` | 8 |
| Patient/{id}/$everything | 1998624 | `data/everything.json` | 10 |
| POST $graphql (chỉ đọc) | 1998627 | `data/graphql.json` | 11 |
| PATCH Patient: JSON Patch hợp lệ và lỗi | 1998630 | `data/patch-negative.json` | 15 |
| Cô lập dữ liệu giữa hai project | 1998633 | `data/project-isolation.json` | 1 |
| Project/{id}/$rate-limits | 1998636 | `data/rate-limits-flow.json` | 1 |
| Tìm kiếm Patient: lọc, sắp xếp, phân trang, lỗi | 1998638 | `data/search.json` | 38 |
| POST /{resourceType}/_search (form) | 1998639 | `data/search-post.json` | 6 |
| Smoke fhir-server | 1998641 | `data/smoke.csv` | 1 |
| bulkdata/job status: id không tồn tại | 1998643 | `data/status-unknown-id.json` | 5 |
| ValueSet/$expand | 1998645 | `data/terminology-expand.json` | 10 |
| CodeSystem/$lookup | 1998647 | `data/terminology-lookup.json` | 10 |
| PUT/GET: If-Match, If-None-Match, Prefer, id lệch | 1998649 | `data/update-rules.csv` | 1 |
| POST /{resourceType}/$validate | 1998651 | `data/validate-operation.json` | 11 |
| POST resource: validate thân và lỗi | 1998656 | `data/write-validation.json` | 16 |

Biến scenario dùng (giá trị nhập trong environment của app hoặc do step trước tạo ra; không lưu giá trị ở đây): `fhirUrl`, `authUrl`, `clientId`, `clientSecret`, `projectId`, `otherClientId`, `otherClientSecret`, `job_id`, `bin_id`, `apiKey`.
