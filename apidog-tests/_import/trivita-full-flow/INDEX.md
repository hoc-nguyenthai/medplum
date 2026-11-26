# Import test data — Trivita Full Flow

Branch `ai/20261125-from-main-trivita-platform-tests` · folder scenario **Trivita Full Flow** · 4 file / 4 scenario (scenario không có file là scenario tĩnh, không cần import).

Cách nhanh (Bulk Edit): mở bộ Test Data → **Bulk Edit** → dán toàn bộ nội dung file ở cột *File dán Bulk Edit* → OK (app tự lưu). Cách import file: mở scenario trong folder → tab **Test Data** → Mở bộ Test Data đã tạo sẵn (tên ở cột *Bộ Test Data*) → Import/Export → Import (đặt mã hóa **UTF-8** nếu có ô chọn) → chọn file ở cột *File* (hộp chọn file: `Cmd+Shift+G`, dán đường dẫn). Giữ nguyên tên cột (step dùng `{{tên_cột}}`).

| # | Scenario (tên trong app) | ID | Bộ Test Data (đã tạo sẵn, rỗng) | File dán Bulk Edit | File import | Dòng |
|---|---|---|---|---|---|---|
| 1 | Full flow negative: không token → 401 trên cả 4 service | 1998598 | `full-flow-negative-no-token` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/bulk/01-full-flow-negative-no-token.txt` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/01-full-flow-negative-no-token.csv` | 1 |
| 2 | Full flow negative: token sai → 401 trên cả 4 service | 1998592 | `full-flow-negative-bad-token` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/bulk/02-full-flow-negative-bad-token.txt` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/02-full-flow-negative-bad-token.csv` | 6 |
| 3 | Full flow: auth → FHIR → $export → DICOM → FHIRcast → dọn dẹp | 1998572 | `full-flow-e2e` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/bulk/03-full-flow-e2e.txt` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/03-full-flow-e2e.csv` | 1 |
| 4 | Full flow: một JWT của auth dùng chung cho các service | 1998569 | `full-flow-cross-service` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/bulk/04-full-flow-cross-service.txt` | `/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/apidog-tests/_import/trivita-full-flow/04-full-flow-cross-service.csv` | 1 |
