# Import test data vào Apidog app — module Trivita WS Gateway

Branch: `ai/20261125-from-main-trivita-platform-tests` · Folder scenario: **Trivita WS Gateway** · Environment: **—**

Apidog CLI không upload được dòng test data (test-data create/update chỉ nhận metadata — đã kiểm tra apidog-cli 2.2.11 và 2.2.12), nên import trong app:
1. Chuyển sang branch ở trên, mở folder scenario rồi mở từng scenario trong bảng.
2. Ở phần Test Data của scenario, import file tương ứng (CSV hoặc JSON; dòng tiêu đề / khóa JSON = tên biến).
3. Giữ nguyên tên cột: step dùng `{{tên_cột}}`; `case_id`, `case_group`, `description`, `expected_status`, `expected_code` dùng trong assertion/script.
4. Khi chạy trong app, chọn environment ở trên và bộ test data vừa import.

| Scenario | ID | File | Dòng |
|---|---|---|---|
| Hub authz: token sai | 1998579 | `data/hub-authz-bad-token.json` | 6 |
| Hub authz: scope | 1998581 | `data/hub-authz-scope.json` | 3 |
| Hub GET topic (STU3) | 1998606 | `data/hub-get-topic.json` | 9 |
| Hub lifecycle HUB | 1998610 | `data/hub-lifecycle-hub-alias.json` | 3 |
| Hub lifecycle STU2 | 1998613 | `data/hub-lifecycle-stu2.json` | 3 |
| Hub lifecycle STU3 | 1998619 | `data/hub-lifecycle-stu3.json` | 3 |
| Hub POST {v}/{topic} khớp topic | 1998625 | `data/hub-topic-path.json` | 6 |
| Hub validation đổi ngữ cảnh | 1998628 | `data/hub-validation-event.json` | 21 |
| Hub validation đăng ký/hủy | 1998631 | `data/hub-validation-subscribe.json` | 34 |

Biến scenario dùng (giá trị nhập trong environment của app hoặc do step trước tạo ra; không lưu giá trị ở đây): `authUrl`, `wsUrl`, `clientId`, `clientSecret`, `access_token`, `topic`, `channel_endpoint`, `ctx_version`, `ctx_version_2`, `apiKey`.
