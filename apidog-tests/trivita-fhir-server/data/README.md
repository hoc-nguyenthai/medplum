# Test data: Trivita FHIR Server

Sinh bằng `generate.py` (không sửa tay). 26 tệp, 196 dòng: Positive 54, Negative 91, Boundary 24, Security 27. Mỗi dòng = một lần chạy trọn scenario (một iteration); dữ liệu tạo mới dùng `{{$timestamp}}` + `case_id` nên chạy lại không đụng bản cũ. Dữ liệu thử nghiệm đều mang tiền tố `Qa`/`qa.trivita.test` để dọn.

## Kỹ thuật theo trường/nhánh
- **Phân vùng tương đương**: gender, loại resource (Patient/Observation/CodeSystem...), form/JSON/Parameters.
- **Biên** (theo giới hạn trong mã): `_count` 0/1/1000/1001; `_offset` bằng tổng và rất lớn; `$everything _count` 1/10000/10001/0; truy vấn GraphQL 12 cấp và 64 KiB; id 129 ký tự; token 4096 ký tự; mảng JSON Patch rỗng; Bundle rỗng.
- **Bảng quyết định**: `authz-matrix` (scope token × thao tác đọc/tạo/sửa/$reindex/xóa) và `search` (modifier × giá trị).
- **Luồng trạng thái**: tạo → đọc → sửa → history/vread → patch → xóa → 410 → expunge → 404; ghi có điều kiện 0/1/nhiều khớp; transaction rollback.
- **Bảo mật**: không token/token giả/chuỗi SQLi, `X-Fhir-Project` giả, cách ly project, SQLi/XSS trong giá trị tìm kiếm và dữ liệu, id/meta giả mạo.

## Cột
Chung: `case_id`, `case_group`, `description`, `expected_status`, `expected_code` (issue.code), `expected_message` (chuỗi con của thông điệp lỗi, nguyên văn từ tài liệu/mã; rỗng = không kiểm). Riêng từng tệp: xem tiêu đề cột; các cột `expected_entries/total/next/prev`, `st_*`, `msg_*`, `forbidden_*` được kiểm trong customScript (rỗng = bỏ qua). Mã lỗi chỉ đặt khi mã chắc chắn.

## Không có / lý do
- Không có ca 413 (16 MiB), 429 (hạn mức), 5xx (DB hỏng), AccessPolicy: xem `scenarios/scenarios.md` mục "Chỉ thiết kế".
- Không assert binding của `gender` (G1).
- Không có dữ liệu bí mật: `clientId/clientSecret/otherClient*` là biến chạy. Cột `bad_token` chứa chuỗi giả (không phải JWT thật), `token_scope` là scope SMART.
