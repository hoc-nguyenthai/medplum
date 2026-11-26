# Import test data vào Apidog app — module Trivita Auth

Branch: `ai/20261125-from-main-trivita-platform-tests` · Folder scenario: **Trivita Auth** · Environment: **—**

Apidog CLI không upload được dòng test data (test-data create/update chỉ nhận metadata — đã kiểm tra apidog-cli 2.2.11 và 2.2.12), nên import trong app:
1. Chuyển sang branch ở trên, mở folder scenario rồi mở từng scenario trong bảng.
2. Ở phần Test Data của scenario, import file tương ứng (CSV hoặc JSON; dòng tiêu đề / khóa JSON = tên biến).
3. Giữ nguyên tên cột: step dùng `{{tên_cột}}`; `case_id`, `case_group`, `description`, `expected_status`, `expected_code` dùng trong assertion/script.
4. Khi chạy trong app, chọn environment ở trên và bộ test data vừa import.

| Scenario | ID | File | Dòng |
|---|---|---|---|
| Auth | POST /admin/projects/{id}/client: đầu vào (data-driven) | 1998574 | `data/auth-admin-client-validation.json` | 22 |
| Auth | POST /admin/projects/{id}/invite: đầu vào (data-driven) | 1998578 | `data/auth-admin-invite-validation.json` | 13 |
| Auth | POST /admin/projects/{id}/secrets: tên, giá trị, số lượng (data-driven) | 1998601 | `data/auth-admin-secrets-validation.json` | 17 |
| Auth | POST /admin/projects/{id}/settings: allowlist và giá trị (data-driven) | 1998608 | `data/auth-admin-settings-validation.json` | 17 |
| Auth | POST /admin/projects/{id}/sites: tên, miền, cặp khóa (data-driven) | 1998611 | `data/auth-admin-sites-validation.json` | 15 |
| Auth | Authz: Bearer sai trên mọi route bảo vệ → 401 (data-driven) | 1998615 | `data/auth-authz-bad-bearer.csv` | 7 |
| Auth | POST /auth/login: đầu vào và thông tin sai (data-driven) | 1998626 | `data/auth-login-validation.json` | 13 |
| Auth | POST /scim/v2/Users: đầu vào (data-driven) | 1998635 | `data/auth-scim-create-validation.json` | 9 |
| Auth | GET /scim/v2/Users: filter, phân trang (data-driven) | 1998640 | `data/auth-scim-list-validation.csv` | 7 |
| Auth | POST /auth/newuser: kiểm tra đầu vào (data-driven) | 1998644 | `data/auth-signup-validation.json` | 21 |
| Auth | POST /oauth2/token: grant, client, scope (data-driven) | 1998648 | `data/auth-token-negative.csv` | 16 |
| Auth | GET /oauth2/userinfo theo scope (data-driven) | 1998652 | `data/auth-userinfo-scope.csv` | 4 |
| Auth | oauth-protected-resource: tham số resource | 1998664 | `data/auth-wellknown-resource.csv` | 6 |

Biến scenario dùng (giá trị nhập trong environment của app hoặc do step trước tạo ra; không lưu giá trị ở đây): `authUrl`, `clientId`, `clientSecret`, `projectId`, `adminEmail`, `adminPassword`, `apiKey`, `etag`, `invEmail`, `invEmail2`, `memberJson`, `scimEmail`, `suProjectId`.
