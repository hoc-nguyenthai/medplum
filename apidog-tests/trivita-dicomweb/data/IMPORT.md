# Import test data vào Apidog app — module Trivita DICOMweb

Branch: `ai/20261125-from-main-trivita-platform-tests` · Folder scenario: **Trivita DICOMweb** · Environment: **—**

Apidog CLI không upload được dòng test data (test-data create/update chỉ nhận metadata — đã kiểm tra apidog-cli 2.2.11 và 2.2.12), nên import trong app:
1. Chuyển sang branch ở trên, mở folder scenario rồi mở từng scenario trong bảng.
2. Ở phần Test Data của scenario, import file tương ứng (CSV hoặc JSON; dòng tiêu đề / khóa JSON = tên biến).
3. Giữ nguyên tên cột: step dùng `{{tên_cột}}`; `case_id`, `case_group`, `description`, `expected_status`, `expected_code` dùng trong assertion/script.
4. Khi chạy trong app, chọn environment ở trên và bộ test data vừa import.

| Scenario | ID | File | Dòng |
|---|---|---|---|
| DICOMweb: authz — token sai (data-driven) | 1998567 | `data/dicomweb-authz-invalid-token.csv` | 6 |
| DICOMweb: health smoke | 1998573 | `data/dicomweb-health.csv` | 1 |
| DICOMweb: lifecycle STOW → QIDO → WADO | 1998577 | `data/dicomweb-lifecycle.csv` | 2 |
| DICOMweb: QIDO-RS /studies (data-driven) | 1998597 | `data/dicomweb-qido-matrix.csv` | 30 |
| DICOMweb: WADO-RS / WADO-URI / QIDO cấp con (data-driven) | 1998599 | `data/dicomweb-retrieve-matrix.csv` | 69 |
| DICOMweb: bốn tiền tố, cô lập project, route chưa cài | 1998600 | `data/dicomweb-routing-isolation.csv` | 1 |
| DICOMweb: STOW-RS validation (data-driven) | 1998607 | `data/dicomweb-stow-validation.json` | 18 |

Biến scenario dùng (giá trị nhập trong environment của app hoặc do step trước tạo ra; không lưu giá trị ở đây): `apiKey`, `dicomUrl`, `authUrl`, `clientId`, `clientSecret`, `accessToken`, `projectId`, `callBad`, `studyUid`, `seriesUid`, `series2Uid`, `sop1`, `sop2`, `sop3`, `otherStudyUid`, `pid`.
