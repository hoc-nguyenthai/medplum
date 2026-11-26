# Import test data vào Apidog app — module Trivita Full Flow

Branch: `ai/20261125-from-main-trivita-platform-tests` · Folder scenario: **Trivita Full Flow** · Environment: **—**

Apidog CLI không upload được dòng test data (test-data create/update chỉ nhận metadata — đã kiểm tra apidog-cli 2.2.11 và 2.2.12), nên import trong app:
1. Chuyển sang branch ở trên, mở folder scenario rồi mở từng scenario trong bảng.
2. Ở phần Test Data của scenario, import file tương ứng (CSV hoặc JSON; dòng tiêu đề / khóa JSON = tên biến).
3. Giữ nguyên tên cột: step dùng `{{tên_cột}}`; `case_id`, `case_group`, `description`, `expected_status`, `expected_code` dùng trong assertion/script.
4. Khi chạy trong app, chọn environment ở trên và bộ test data vừa import.

| Scenario | ID | File | Dòng |
|---|---|---|---|
| Full flow: một JWT của auth dùng chung cho các service | 1998569 | `data/full-flow-cross-service.csv` | 1 |
| Full flow: auth → FHIR → $export → DICOM → FHIRcast → dọn dẹp | 1998572 | `data/full-flow-e2e.csv` | 1 |
| Full flow negative: token sai → 401 trên cả 4 service | 1998592 | `data/full-flow-negative-bad-token.csv` | 6 |
| Full flow negative: không token → 401 trên cả 4 service | 1998598 | `data/full-flow-negative-no-token.csv` | 1 |

Biến scenario dùng (giá trị nhập trong environment của app hoặc do step trước tạo ra; không lưu giá trị ở đây): `authUrl`, `fhirUrl`, `dicomUrl`, `wsUrl`, `clientId`, `clientSecret`, `accessToken`, `wsToken`, `bad_token`, `studyUid`, `seriesUid`, `sop`, `pid`, `topic`, `patient_id`, `v1`, `obs_id`, `job_id`, `bin_id`, `channel_endpoint`.
