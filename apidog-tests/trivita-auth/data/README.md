# Test data: Trivita Auth

Sinh bởi `generate.py` (không sửa tay). 13 tệp, 167 hàng. Hàng nào cũng có `case_id` (duy nhất toàn module), `case_group`, `description`, `expected_status`; `expected_code`/`expected_message` chỉ đặt khi mã làm chắc chắn (lấy nguyên văn từ `apps/auth/docs/api-contract/cases/*.md`).

| Tệp | Scenario | Hàng | Kỹ thuật |
|---|---|---|---|
| auth-wellknown-resource.csv | auth-wellknown-resource | 6 | phân vùng: resource trong/ngoài base URL, rỗng, nhái tiền tố, file:// |
| auth-token-negative.csv | auth-token-negative | 16 | bảng quyết định grant × client × secret × scope; scope bị cấm (patient/, fhir.* không phân biệt hoa thường); tiêm SQL |
| auth-authz-bad-bearer.csv | auth-authz-bad-bearer | 7 | Bearer rỗng/rác/alg=none/a.b.c/SQL/4096 ký tự trên 9 route bảo vệ |
| auth-signup-validation.json | auth-signup-validation | 21 | biên firstName/lastName 128/129, email 73, password 7/8/72/73 byte, é (2 byte) 36/37, trùng email, XSS/SQL |
| auth-login-validation.json | auth-login-validation | 13 | validator gom issue, sai thông tin không phân biệt, client/project lạ, mật khẩu 10000 ký tự |
| auth-userinfo-scope.csv | auth-userinfo-scope | 4 | scope theo từ (`emailx` không khớp `email`) |
| auth-admin-client-validation.json | auth-admin-client-validation | 22 | biên secret 31/32/256/257, grantType, redirectUris, launchUri, trường lạ, JSON hỏng |
| auth-admin-settings-validation.json | auth-admin-settings-validation | 17 | allowlist, biên appName 100/101, boolean, MFA, tên miền; hàng cuối dọn `[]` |
| auth-admin-secrets-validation.json | auth-admin-secrets-validation | 17 | biên tên 64/65, giá trị 4096/4097, 50/51 mục, `********`, tiền tố smtp; hàng cuối dọn |
| auth-admin-sites-validation.json | auth-admin-sites-validation | 15 | biên tên 100/101, 20/21 site, miền, cặp khóa; hàng cuối dọn |
| auth-admin-invite-validation.json | auth-admin-invite-validation | 13 | trường bắt buộc, email, scope, trường lạ, policy lạ |
| auth-scim-create-validation.json | auth-scim-create-validation | 9 | trường bắt buộc SCIM, RelatedPerson, JSON hỏng |
| auth-scim-list-validation.csv | auth-scim-list-validation | 7 | filter, startIndex/count sai kiểu, chuẩn hóa |

Biến trong ô dữ liệu: `{{clientId}}`, `{{clientSecret}}`, `{{adminEmail}}`, `{{adminPassword}}`, `{{projectId}}`, `{{authUrl}}` (biến môi trường Apidog, giá trị nhập lúc chạy; ô không chứa bí mật thật) và `{{$timestamp}}` (email duy nhất, tiền tố `qa.`). Cột tên `*password*` chứa mật khẩu mẫu dùng một lần cho user `qa.*`, không phải thông tin thật.

## Không có hoặc ít hàng
- **Boundary email đúng 72 ký tự ở newuser**: cần email vừa duy nhất vừa đúng độ dài; `{{$timestamp}}` có độ dài không cố định. Chỉ có hàng 73 ký tự (400).
- **Rate limit/khóa tài khoản/MFA/IdP/email/super-admin/authorize**: xem "Chỉ thiết kế" trong `scenarios/scenarios.md`.
- **Hàng Security kỳ vọng thành công** (XSS/SQL được lưu nguyên, không 500): mục đích là chứng minh không bị từ chối nhầm và không lỗi nội bộ; checker cảnh báo WARNING có chủ đích.
- Hàng tạo dữ liệu (user/client `qa.*`) để lại bản ghi trong DB test; dọn bằng `make down-full`-style (xóa volume) hoặc `auth purge-logins`.

## Dữ liệu nhạy cảm
Không có JWT/secret thật trong tệp. Cặp PKCE trong `generate.py` là ví dụ công khai của RFC 7636 phụ lục B.
