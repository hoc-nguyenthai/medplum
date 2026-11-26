# Kịch bản kiểm thử Trivita Auth (module 1533158)

Sinh bởi `generate.py`. Mức ưu tiên: P0 chạy luôn, P1 chức năng chính, P2 cần cờ bật thêm.

## S-01 auth-smoke — Smoke: health + discovery

- Ưu tiên: P0
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: Kiểm nhanh service sống và các tài liệu công khai: /health/*, JWKS, openid-configuration, oauth-authorization-server, smart-configuration, smart-styles, oauth-protected-resource. Không cần thông tin đăng nhập.
- Các bước:
  1. Liveness — `GET /health/live` (endpoint 47548474); kiểm: status == 200, $.status == ok
  2. Readiness (DB + accepting_traffic) — `GET /health/ready` (endpoint 47548475); kiểm: status == 200, $.ready == true
  3. JWKS có khóa — `GET /.well-known/jwks.json` (endpoint 47548420); kiểm: status == 200, $.keys is array, $.keys[0].kid exists
  4. OpenID configuration — `GET /.well-known/openid-configuration` (endpoint 47548423); kiểm: status == 200, $.issuer exists, $.token_endpoint exists, $.jwks_uri exists
  5. OAuth authorization server metadata — `GET /.well-known/oauth-authorization-server` (endpoint 47548421); kiểm: status == 200, $.issuer exists
  6. SMART configuration — `GET /.well-known/smart-configuration` (endpoint 47548424); kiểm: status == 200, $.token_endpoint exists
  7. SMART styles — `GET /.well-known/smart-styles.json` (endpoint 47548425); kiểm: status == 200
  8. Protected resource metadata (mặc định) — `GET /.well-known/oauth-protected-resource` (endpoint 47548422); kiểm: status == 200, $.resource exists

## S-02 auth-wellknown-caching — JWKS ETag và 304

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: GET JWKS lấy ETag rồi gửi lại If-None-Match: 304 không body. Kiểm Cache-Control public, max-age=300.
- Các bước:
  1. JWKS lần 1: lưu ETag — `GET /.well-known/jwks.json` (endpoint 47548420); kiểm: status == 200
  2. JWKS lần 2: If-None-Match đúng ETag → 304 — `GET /.well-known/jwks.json` (endpoint 47548420); kiểm: status == 304
  3. JWKS: If-None-Match lạ → 200 — `GET /.well-known/jwks.json` (endpoint 47548420); kiểm: status == 200, $.keys is array

## S-03 auth-wellknown-resource — oauth-protected-resource: tham số resource

- Ưu tiên: P1
- Dữ liệu: `data/auth-wellknown-resource.csv` (6 hàng)
- Mô tả: Data-driven: resource hợp lệ (bắt đầu bằng AUTH_BASE_URL) 200; resource lạ/rỗng 400 văn bản 'Invalid resource URL' (RFC 9728 chống mở rộng).
- Các bước:
  1. GET protected-resource?resource=… — `GET /.well-known/oauth-protected-resource` (endpoint 47548422); kiểm: status == {{expected_status}}
- Case: WK-01 … WK-06

## S-04 auth-client-credentials-lifecycle — Vòng đời client_credentials: token → me → userinfo → introspect → logout

- Ưu tiên: P0
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: Luồng máy-với-máy bằng client seed: xin token, đọc /auth/me (ClientApplication: không có user/security), userinfo, introspect, đăng xuất; token chết ngay sau logout.
- Các bước:
  1. Lấy token client_credentials — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  2. Introspect chính token (cùng client) → active — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == 200, $.active == true, $.client_id == {{clientId}}, $.iss exists
  3. GET /auth/me của ClientApplication — `GET /auth/me` (endpoint 47548454); kiểm: status == 200, $.project.id == {{projectId}}, $.user not exists, $.security not exists
  4. GET /oauth2/userinfo (chỉ sub) — `GET /oauth2/userinfo` (endpoint 47548483); kiểm: status == 200, $.sub == {{clientId}}, $.email not exists
  5. POST /oauth2/userinfo cùng kết quả — `POST /oauth2/userinfo` (endpoint 47548484); kiểm: status == 200, $.sub == {{clientId}}
  6. Đăng xuất POST /oauth2/logout — `POST /oauth2/logout` (endpoint 47548480); kiểm: status == 200
  7. Sau logout: /auth/me → 401 — `GET /auth/me` (endpoint 47548454); kiểm: status == 401, $.issue[0].code == login
  8. Sau logout: introspect bằng token chết → 401 — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == 401

## S-05 auth-user-login-lifecycle — Vòng đời đăng nhập admin: login → token → me → userinfo → revoke

- Ưu tiên: P0
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: Admin của project seed đăng nhập email+mật khẩu, đổi code lấy token, đọc me/userinfo/mfa status, introspect rồi thu hồi phiên: token chết ngay.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. GET /auth/me của admin — `GET /auth/me` (endpoint 47548454); kiểm: status == 200, $.user.email == {{adminEmail}}, $.project.id == {{projectId}}, $.membership.admin == true, $.security.sessions is array
  4. GET /oauth2/userinfo (scope profile email) — `GET /oauth2/userinfo` (endpoint 47548483); kiểm: status == 200, $.email == {{adminEmail}}, $.sub exists, $.email_verified exists
  5. GET /auth/mfa/status — `GET /auth/mfa/status` (endpoint 47548461); kiểm: status == 200
  6. Introspect token admin (cùng client) — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == 200, $.active == true
  7. Thu hồi phiên bằng loginId — `POST /auth/revoke` (endpoint 47548469); kiểm: status == 200
  8. Sau revoke: /auth/me → 401 — `GET /auth/me` (endpoint 47548454); kiểm: status == 401
  9. Revoke lần hai bằng token chết → 401 — `POST /auth/revoke` (endpoint 47548469); kiểm: status == 401

## S-06 auth-code-reuse — Đổi authorization code hai lần thu hồi login

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: AC8: dùng lại code → 400 invalid_grant 'Token already granted' và Login bị thu hồi, access token đã cấp chết (401).
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Đổi lại cùng code → 400 — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 400, $.error == invalid_grant, $.error_description == Token already granted
  4. Token đã cấp ở lần 1 chết (login bị thu hồi) — `GET /auth/me` (endpoint 47548454); kiểm: status == 401

## S-07 auth-login-poll-scope — GET /auth/login/{login}, /auth/scope, /auth/profile

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: Login đang dở: GET /auth/login/{id} phát code mới và vô hiệu code cũ; /auth/scope thu hẹp scope (mở rộng bị chặn 'Invalid scope'); /auth/profile thiếu trường; login đã cấp token không đọc lại được.
- Các bước:
  1. Đăng nhập admin — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists
  2. GET /auth/login/{login} phát code mới — `GET /auth/login/{{loginId}}` (endpoint 47548453); kiểm: status == 200, $.code exists
  3. Code cũ bị vô hiệu → Invalid code — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 400, $.error == invalid_request, $.error_description == Invalid code
  4. Scope mở rộng hơn lúc đăng nhập → 400 Invalid scope — `POST /auth/scope` (endpoint 47548470); kiểm: status == 400, $.issue[0].details.text == Invalid scope
  5. Thiếu scope → 400 Missing scope — `POST /auth/scope` (endpoint 47548470); kiểm: status == 400, $.issue[0].details.text == Missing scope
  6. Thu hẹp scope → code mới — `POST /auth/scope` (endpoint 47548470); kiểm: status == 200, $.code exists
  7. Thiếu profile → 400 Missing profile — `POST /auth/profile` (endpoint 47548467); kiểm: status == 400, $.issue[0].details.text == Missing profile
  8. Đổi code mới nhất lấy token — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.scope == openid profile
  9. Login đã cấp token: GET /auth/login → 400 Login already granted — `GET /auth/login/{{loginId}}` (endpoint 47548453); kiểm: status == 400, $.issue[0].details.text == Login already granted
  10. GET /auth/login/abc → 400 Login ID is required — `GET /auth/login/abc` (endpoint 47548453); kiểm: status == 400, $.issue[0].details.text == Login ID is required
  11. GET /auth/login/<uuid lạ> → 404 — `GET /auth/login/00000000-0000-4000-8000-000000000000` (endpoint 47548453); kiểm: status == 404, $.issue[0].code == not-found

## S-08 auth-token-negative — POST /oauth2/token: grant, client, scope (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-token-negative.csv` (16 hàng)
- Mô tả: Một bước POST /oauth2/token theo từng hàng dữ liệu: thiếu/sai grant_type, client, secret, scope bị cấm (patient/, fhir.*), code sai. Thông điệp error/error_description theo mã.
- Các bước:
  1. POST /oauth2/token theo dữ liệu — `POST /oauth2/token` (endpoint 47548482); kiểm: status == {{expected_status}}
- Case: TK-01 … TK-16

## S-09 auth-negative-misc — Phản hồi lỗi chung: Content-Type, thiếu Bearer, introspect rỗng

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: T1: /oauth2/token nhận JSON → 400 văn bản 'Unsupported content type'; thiếu Bearer → 401 + WWW-Authenticate; introspect không token → 400; token lạ → active:false; header bảo mật.
- Các bước:
  1. Token với Content-Type JSON → 400 văn bản — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 400
  2. GET /auth/me không Authorization → 401 + WWW-Authenticate — `GET /auth/me` (endpoint 47548454); kiểm: status == 401, $.issue[0].code == login
  3. Lấy token client_credentials — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  4. Introspect không có token → 400 văn bản — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == 400
  5. Introspect token lạ → active:false — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == 200, $.active == false
  6. Introspect id_token/rác không lộ thông tin: một khóa duy nhất — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == 200, $.active == false, $.iss not exists
  7. GET /oauth2/logout thu hồi login — `GET /oauth2/logout` (endpoint 47548479); kiểm: status == 200
  8. Gọi lại logout bằng token đã thu hồi → 401 (L4) — `GET /oauth2/logout` (endpoint 47548479); kiểm: status == 401

## S-10 auth-authz-bad-bearer — Authz: Bearer sai trên mọi route bảo vệ → 401 (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-authz-bad-bearer.csv` (7 hàng)
- Mô tả: Với mỗi token xấu, gọi mọi route cần Bearer (me, revoke, changepassword, mfa/status, introspect, userinfo, logout, admin) và đòi 401 + OperationOutcome code=login, không lộ thông tin.
- Các bước:
  1. GET /auth/me — `GET /auth/me` (endpoint 47548454); kiểm: status == {{expected_status}}
  2. POST /auth/revoke — `POST /auth/revoke` (endpoint 47548469); kiểm: status == {{expected_status}}
  3. POST /auth/changepassword — `POST /auth/changepassword` (endpoint 47548445); kiểm: status == {{expected_status}}
  4. GET /auth/mfa/status — `GET /auth/mfa/status` (endpoint 47548461); kiểm: status == {{expected_status}}
  5. POST /oauth2/introspect — `POST /oauth2/introspect` (endpoint 47548478); kiểm: status == {{expected_status}}
  6. GET /oauth2/userinfo — `GET /oauth2/userinfo` (endpoint 47548483); kiểm: status == {{expected_status}}
  7. POST /oauth2/logout — `POST /oauth2/logout` (endpoint 47548480); kiểm: status == {{expected_status}}
  8. GET /admin/projects/{id} — `GET /admin/projects/{{projectId}}` (endpoint 47548427); kiểm: status == {{expected_status}}
  9. POST /admin/projects/{id}/settings — `POST /admin/projects/{{projectId}}/settings` (endpoint 47548439); kiểm: status == {{expected_status}}
- Case: AZ-01 … AZ-07

## S-11 auth-signup-lifecycle — Đăng ký → tạo project → token → đổi mật khẩu

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: newuser (projectId=new, PKCE) → newproject → đổi code (code_verifier) → me/admin của project mới; admin project mới không đọc được project seed (403); changepassword sai/đúng; đổi mật khẩu thu hồi mọi phiên.
- Các bước:
  1. Đăng ký tài khoản mới — `POST /auth/newuser` (endpoint 47548465); kiểm: status == 200, $.login exists, $.emailVerificationRequired == false
  2. Tạo project mới — `POST /auth/newproject` (endpoint 47548464); kiểm: status == 200, $.code exists
  3. Đổi code (PKCE) lấy token — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.project.reference exists
  4. GET /auth/me: chủ project là admin — `GET /auth/me` (endpoint 47548454); kiểm: status == 200, $.membership.admin == true, $.project.id == {{suProjectId}}
  5. Admin project mới đọc project của mình — `GET /admin/projects/{{suProjectId}}` (endpoint 47548427); kiểm: status == 200, $.project.id == {{suProjectId}}
  6. Admin project mới đọc project seed → 403 chung — `GET /admin/projects/{{projectId}}` (endpoint 47548427); kiểm: status == 403, $.issue[0].code == forbidden, $.issue[0].details.text == Forbidden
  7. changepassword: mật khẩu cũ sai → 400 — `POST /auth/changepassword` (endpoint 47548445); kiểm: status == 400, $.issue[0].details.text == Incorrect password
  8. changepassword: mật khẩu mới quá ngắn → 400 — `POST /auth/changepassword` (endpoint 47548445); kiểm: status == 400, $.issue[0].details.text == Password must be at least 8 characters
  9. changepassword đúng → 200 — `POST /auth/changepassword` (endpoint 47548445); kiểm: status == 200
  10. Đổi mật khẩu thu hồi mọi phiên → /auth/me 401 — `GET /auth/me` (endpoint 47548454); kiểm: status == 401
  11. newproject dùng lại login đã cấp code → 400 — `POST /auth/newproject` (endpoint 47548464); kiểm: status == 400

## S-12 auth-signup-validation — POST /auth/newuser: kiểm tra đầu vào (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-signup-validation.json` (21 hàng)
- Mô tả: Phân vùng tương đương và biên của firstName/lastName (1-128), email (3-72), password (8 ký tự đến 72 byte), trùng email, chuỗi nguy hiểm. Hàng 200 tạo user qa.* (dọn bằng cách xóa DB test).
- Các bước:
  1. POST /auth/newuser theo dữ liệu — `POST /auth/newuser` (endpoint 47548465); kiểm: status == {{expected_status}}
- Case: NU-01 … NU-21

## S-13 auth-login-validation — POST /auth/login: đầu vào và thông tin sai (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-login-validation.json` (13 hàng)
- Mô tả: Validator gom issue (email, projectId, password 8 ký tự..72 byte), sai thông tin không phân biệt email/mật khẩu, client/project lạ, chuỗi nguy hiểm.
- Các bước:
  1. POST /auth/login theo dữ liệu — `POST /auth/login` (endpoint 47548452); kiểm: status == {{expected_status}}
- Case: LG-01 … LG-13

## S-14 auth-userinfo-scope — GET /oauth2/userinfo theo scope (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-userinfo-scope.csv` (4 hàng)
- Mô tả: Đăng nhập với scope khác nhau rồi đọc userinfo: claim chỉ xuất hiện khi scope có đúng từ (openid/email/profile).
- Các bước:
  1. Đăng nhập admin với scope theo dữ liệu — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists
  2. Đổi code lấy token — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200
  3. GET /oauth2/userinfo kiểm claim theo scope — `GET /oauth2/userinfo` (endpoint 47548483); kiểm: status == {{expected_status}}
- Case: UI-01 … UI-04

## S-15 auth-public-lookups — Route công khai: clientinfo, method, resetpassword

- Ưu tiên: P2
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: clientinfo: client seed 200, lạ 404, redirect_uri lạ 400. method: email hợp lệ 200 '{}', sai 400. resetpassword luôn 200 'All OK' cho email hợp lệ (có tài khoản hay không), email/projectId sai 400.
- Các bước:
  1. clientinfo: client seed — `GET /auth/clientinfo/{{clientId}}` (endpoint 47548446); kiểm: status == 200, $.secret not exists
  2. clientinfo: client không tồn tại → 404 — `GET /auth/clientinfo/00000000-0000-4000-8000-000000000000` (endpoint 47548446); kiểm: status == 404, $.issue[0].code == not-found
  3. clientinfo: redirect_uri lạ → 400 — `GET /auth/clientinfo/{{clientId}}` (endpoint 47548446); kiểm: status == 400, $.issue[0].details.text == Invalid redirect URI
  4. method: email admin → mật khẩu ({}) — `POST /auth/method` (endpoint 47548455); kiểm: status == 200
  5. method: email sai → 400 — `POST /auth/method` (endpoint 47548455); kiểm: status == 400, $.issue[0].details.text == Valid email address is required
  6. resetpassword: có tài khoản → 200 All OK — `POST /auth/resetpassword` (endpoint 47548468); kiểm: status == 200, $.issue[0].details.text == All OK
  7. resetpassword: không có tài khoản → 200 giống hệt (chống dò) — `POST /auth/resetpassword` (endpoint 47548468); kiểm: status == 200, $.issue[0].details.text == All OK
  8. resetpassword: email sai → 400 — `POST /auth/resetpassword` (endpoint 47548468); kiểm: status == 400, $.issue[0].details.text == Valid email address is required
  9. resetpassword: projectId không phải UUID → 400 — `POST /auth/resetpassword` (endpoint 47548468); kiểm: status == 400, $.issue[0].details.text == Invalid projectId

## S-16 auth-admin-lifecycle — Admin project: setting/secret/site/client/rotate-secret/invite/member/delete

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: Admin seed quản trị chính project: đặt setting, secret (che), site, tạo client rồi xoay secret (retiring), mời user Practitioner, sửa/tắt/xóa membership. Cần AUTH_ADMIN_ENABLED=true.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Đọc project; sinh email/tên duy nhất cho các bước sau — `GET /admin/projects/{{projectId}}` (endpoint 47548427); kiểm: status == 200, $.project.id == {{projectId}}
  4. Đặt setting appName — `POST /admin/projects/{{projectId}}/settings` (endpoint 47548439); kiểm: status == 200, $.project.setting[0].name == appName
  5. Đặt secret qaSecret — `POST /admin/projects/{{projectId}}/secrets` (endpoint 47548438); kiểm: status == 200
  6. Đọc project: secret bị che — `GET /admin/projects/{{projectId}}` (endpoint 47548427); kiểm: status == 200, $.project.secret[0].name == qaSecret, $.project.secret[0].valueString == ********
  7. Đặt site — `POST /admin/projects/{{projectId}}/sites` (endpoint 47548440); kiểm: status == 200
  8. Tạo client client_credentials — `POST /admin/projects/{{projectId}}/client` (endpoint 47548428); kiểm: status == 201, $.secret exists, $.id exists
  9. Token của client mới — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  10. Xoay secret client — `POST /admin/projects/{{projectId}}/client/{{newClientId}}/rotate-secret` (endpoint 47548429); kiểm: status == 200, $.clientSecret exists, $.retiring == true
  11. Secret cũ còn dùng được khi retiring — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  12. Secret mới dùng được — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  13. Xoay secret trùng secret hiện tại → 400 — `POST /admin/projects/{{projectId}}/client/{{newClientId}}/rotate-secret` (endpoint 47548429); kiểm: status == 400, $.issue[0].details.text == New secret must differ from the current secret
  14. Bỏ secret cũ (retireOnly) — `POST /admin/projects/{{projectId}}/client/{{newClientId}}/rotate-secret` (endpoint 47548429); kiểm: status == 200, $.retiring == false
  15. Secret cũ hết hiệu lực → 400 Invalid secret — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 400, $.error == invalid_request, $.error_description == Invalid secret
  16. Mời user Practitioner (không gửi thư) — `POST /admin/projects/{{projectId}}/invite` (endpoint 47548431); kiểm: status == 200, $.resourceType == ProjectMembership, $.id exists
  17. Mời lại cùng email → 409 — `POST /admin/projects/{{projectId}}/invite` (endpoint 47548431); kiểm: status == 409, $.issue[0].details.text == User is already a member of this project
  18. Đọc membership; dựng body tắt active — `GET /admin/projects/{{projectId}}/members/{{membershipId}}` (endpoint 47548432); kiểm: status == 200, $.id == {{membershipId}}
  19. Tắt membership (active=false) — `POST /admin/projects/{{projectId}}/members/{{membershipId}}` (endpoint 47548433); kiểm: status == 200, $.active == false
  20. Admin đặt mật khẩu cho user của project — `POST /admin/projects/setpassword` (endpoint 47548426); kiểm: status == 200
  21. Đổi email của member — `POST /admin/projects/{{projectId}}/members/{{membershipId}}/update-email` (endpoint 47548437); kiểm: status == 200
  22. Đổi email lần nữa về chính email đó → 400 — `POST /admin/projects/{{projectId}}/members/{{membershipId}}/update-email` (endpoint 47548437); kiểm: status == 400, $.issue[0].details.text == New email is the same as the current email
  23. Xóa membership — `DELETE /admin/projects/{{projectId}}/members/{{membershipId}}` (endpoint 47548434); kiểm: status == 200
  24. Đọc membership đã xóa → 403 chung — `GET /admin/projects/{{projectId}}/members/{{membershipId}}` (endpoint 47548432); kiểm: status == 403, $.issue[0].details.text == Forbidden
  25. Xóa lần hai → 403 chung — `DELETE /admin/projects/{{projectId}}/members/{{membershipId}}` (endpoint 47548434); kiểm: status == 403
  26. Trả setting/secret/site về rỗng — `POST /admin/projects/{{projectId}}/settings` (endpoint 47548439); kiểm: status == 200
  27. Xóa secret — `POST /admin/projects/{{projectId}}/secrets` (endpoint 47548438); kiểm: status == 200
  28. Xóa site — `POST /admin/projects/{{projectId}}/sites` (endpoint 47548440); kiểm: status == 200

## S-17 auth-admin-authz — Admin: phân quyền (client không admin, project khác, membership lạ)

- Ưu tiên: P1
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: client_credentials chưa nâng admin → 403; admin gọi project khác hoặc membership không tồn tại → 403 chung (không cho dò id); Basic không được chấp nhận.
- Các bước:
  1. Lấy token client_credentials — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  2. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  3. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  4. Client chưa admin đọc project → 403 — `GET /admin/projects/{{projectId}}` (endpoint 47548427); kiểm: status == 403, $.issue[0].code == forbidden
  5. Client chưa admin tạo client → 403 — `POST /admin/projects/{{projectId}}/client` (endpoint 47548428); kiểm: status == 403
  6. Client chưa admin mời user → 403 — `POST /admin/projects/{{projectId}}/invite` (endpoint 47548431); kiểm: status == 403
  7. Admin đọc project khác → 403 — `GET /admin/projects/00000000-0000-4000-8000-000000000001` (endpoint 47548427); kiểm: status == 403, $.issue[0].details.text == Forbidden
  8. Admin đặt setting ở project khác → 403 — `POST /admin/projects/00000000-0000-4000-8000-000000000001/settings` (endpoint 47548439); kiểm: status == 403
  9. Admin đọc membership không tồn tại → 403 chung — `GET /admin/projects/{{projectId}}/members/00000000-0000-4000-8000-000000000000` (endpoint 47548432); kiểm: status == 403
  10. Admin xóa membership không tồn tại → 403 chung — `DELETE /admin/projects/{{projectId}}/members/00000000-0000-4000-8000-000000000000` (endpoint 47548434); kiểm: status == 403
  11. Admin xoay secret client không tồn tại → 403 chung — `POST /admin/projects/{{projectId}}/client/00000000-0000-4000-8000-000000000000/rotate-secret` (endpoint 47548429); kiểm: status == 403
  12. Body hỏng của người không admin vẫn nhận 400 trước 403 (thứ tự kiểm tra) — `POST /admin/projects/{{projectId}}/client` (endpoint 47548428); kiểm: status == 400, $.issue[0].details.text == Content could not be parsed

## S-18 auth-admin-client-validation — POST /admin/projects/{id}/client: đầu vào (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-admin-client-validation.json` (22 hàng)
- Mô tả: Phân vùng/biên của name, secret (32..256), grantType, redirectUris, launchUri, accessPolicy, trường lạ, JSON hỏng. Hàng 201 tạo client qa.* trong project seed.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Tạo client theo dữ liệu — `POST /admin/projects/{{projectId}}/client` (endpoint 47548428); kiểm: status == {{expected_status}}
- Case: AC-01 … AC-22

## S-19 auth-admin-settings-validation — POST /admin/projects/{id}/settings: allowlist và giá trị (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-admin-settings-validation.json` (17 hàng)
- Mô tả: Allowlist 4 tên, biên appName 1..100, boolean, danh sách MFA, tên miền Practitioner, trùng tên, body sai. Hàng cuối đặt lại [] để không để lại setting.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Đặt settings theo dữ liệu — `POST /admin/projects/{{projectId}}/settings` (endpoint 47548439); kiểm: status == {{expected_status}}
- Case: AS-01 … AS-99

## S-20 auth-admin-secrets-validation — POST /admin/projects/{id}/secrets: tên, giá trị, số lượng (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-admin-secrets-validation.json` (17 hàng)
- Mô tả: Biên tên 1..64 ký tự, valueString ≤ 4096, tối đa 50 mục, đúng một value[x], giữ '********', tiền tố smtp. Hàng cuối xóa hết secret.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Đặt secrets theo dữ liệu — `POST /admin/projects/{{projectId}}/secrets` (endpoint 47548438); kiểm: status == {{expected_status}}
- Case: ST-01 … ST-99

## S-21 auth-admin-sites-validation — POST /admin/projects/{id}/sites: tên, miền, cặp khóa (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-admin-sites-validation.json` (15 hàng)
- Mô tả: Biên tên 1..100, ≤ 20 site, định dạng miền, cặp Google/reCAPTCHA đi cùng nhau, giá trị ≤ 512. Hàng cuối xóa hết site.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Đặt sites theo dữ liệu — `POST /admin/projects/{{projectId}}/sites` (endpoint 47548440); kiểm: status == {{expected_status}}
- Case: SI-01 … SI-99

## S-22 auth-admin-invite-validation — POST /admin/projects/{id}/invite: đầu vào (data-driven)

- Ưu tiên: P1
- Dữ liệu: `data/auth-admin-invite-validation.json` (13 hàng)
- Mô tả: Trường bắt buộc (resourceType, firstName, lastName, email|externalId), định dạng email, scope, trường lạ, policy lạ. Hàng 200 tạo user/membership qa.iv.*.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. Mời user theo dữ liệu — `POST /admin/projects/{{projectId}}/invite` (endpoint 47548431); kiểm: status == {{expected_status}}
- Case: IV-01 … IV-13

## S-23 auth-scim-lifecycle — SCIM v2 Users: tạo, đọc, lọc, sửa, xóa

- Ưu tiên: P2
- Dữ liệu: không (kịch bản tĩnh)
- Mô tả: Cần AUTH_SCIM_ENABLED=true và project có feature scim (AUTH_DEFAULT_PROJECT_FEATURES=scim lúc seed). Admin Bearer tạo/đọc/lọc/PATCH/PUT/xóa user SCIM; thiếu token 401 hai dòng WWW-Authenticate; client không admin 403.
- Các bước:
  1. Lấy token client_credentials — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists
  2. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  3. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  4. Sinh userName/email duy nhất — `GET /health/live` (endpoint 47548474); kiểm: status == 200
  5. POST /scim/v2/Users — `POST /scim/v2/Users` (endpoint 47548486); kiểm: status == 201, $.id exists, $.active == true
  6. GET /scim/v2/Users/{id} — `GET /scim/v2/Users/{{scimId}}` (endpoint 47548487); kiểm: status == 200, $.id == {{scimId}}, $.name.givenName == QA
  7. GET list lọc userName eq — `GET /scim/v2/Users` (endpoint 47548485); kiểm: status == 200, $.totalResults == 1, $.Resources is array
  8. PATCH replace name.givenName — `PATCH /scim/v2/Users/{{scimId}}` (endpoint 47548490); kiểm: status == 200
  9. PUT thay thế user — `PUT /scim/v2/Users/{{scimId}}` (endpoint 47548488); kiểm: status == 200
  10. PATCH thiếu schema PatchOp → 400 — `PATCH /scim/v2/Users/{{scimId}}` (endpoint 47548490); kiểm: status == 400, $.scimType == invalidSyntax
  11. PATCH remove active → 400 mutability — `PATCH /scim/v2/Users/{{scimId}}` (endpoint 47548490); kiểm: status == 400, $.scimType == mutability
  12. DELETE user → 204 — `DELETE /scim/v2/Users/{{scimId}}` (endpoint 47548489); kiểm: status == 204
  13. GET sau khi xóa → 404 SCIM Error — `GET /scim/v2/Users/{{scimId}}` (endpoint 47548487); kiểm: status == 404, $.detail == Not found
  14. Không token → 401 + hai dòng WWW-Authenticate — `GET /scim/v2/Users` (endpoint 47548485); kiểm: status == 401
  15. Client không admin → 403 — `GET /scim/v2/Users` (endpoint 47548485); kiểm: status == 403
  16. DELETE lần hai id đã xóa → 404 — `DELETE /scim/v2/Users/{{scimId}}` (endpoint 47548489); kiểm: status == 404

## S-24 auth-scim-create-validation — POST /scim/v2/Users: đầu vào (data-driven)

- Ưu tiên: P2
- Dữ liệu: `data/auth-scim-create-validation.json` (9 hàng)
- Mô tả: Trường bắt buộc (userName, emails[0].value, name), định dạng email, RelatedPerson thiếu patient, JSON hỏng. Cần SCIM bật.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. POST /scim/v2/Users theo dữ liệu — `POST /scim/v2/Users` (endpoint 47548486); kiểm: status == {{expected_status}}
- Case: SC-01 … SC-09

## S-25 auth-scim-list-validation — GET /scim/v2/Users: filter, phân trang (data-driven)

- Ưu tiên: P2
- Dữ liệu: `data/auth-scim-list-validation.csv` (7 hàng)
- Mô tả: filter chỉ hỗ trợ userName eq; startIndex/count phải là số; count âm/0 và startIndex<1 được chuẩn hóa. Cần SCIM bật.
- Các bước:
  1. Đăng nhập admin (email + mật khẩu) — `POST /auth/login` (endpoint 47548452); kiểm: status == 200, $.code exists, $.login exists
  2. Đổi code lấy token (authorization_code) — `POST /oauth2/token` (endpoint 47548482); kiểm: status == 200, $.access_token exists, $.token_type == Bearer
  3. GET list theo dữ liệu — `GET /scim/v2/Users` (endpoint 47548485); kiểm: status == {{expected_status}}
- Case: SL-01 … SL-07

## Chỉ thiết kế (chưa dựng, kèm lý do)

| Route / luồng | Lý do chưa dựng | Điều kiện để dựng |
|---|---|---|
| `GET/POST /oauth2/authorize` | trả 302 tới `AUTH_APP_BASE_URL`; bước Apidog có theo chuyển hướng hay không chưa kiểm chứng | xác nhận tùy chọn follow-redirect, client có redirectUris |
| `POST /oauth2/register` | route tắt mặc định (`AUTH_OAUTH_REGISTER_ENABLED`) | bật cờ; client dựng sẵn theo redirect_uri |
| grant `refresh_token`, `pre-authorized_code`, `token-exchange`, `private_key_jwt` | cần client có grantType tương ứng/JWKS/khóa ký của bên test | client riêng tạo qua admin/client + JWKS tĩnh (`AUTH_ALLOW_PRIVATE_JWKS`) |
| HTTP Basic của client ở `/oauth2/token` (A2, A3) | builder Apidog chỉ dựng Basic qua security scheme, header `Authorization` không khai báo bị server bỏ | cần bước customHttp có auth basic thủ công |
| `/auth/mfa/*`, `/admin/.../mfa/reset` | cần mã TOTP tính theo thời gian (script không có thư viện) và `AUTH_MFA_SECRET_KEYS`; thiếu cấu hình là 500 fail đóng | bật khóa MFA, bước script HMAC-SHA1 |
| `/auth/setpassword`, `/auth/verifyemail` | secret chỉ nằm trong link thư (provider `log` in ra log container) | đọc log hoặc SMTP bắt thư |
| `/auth/google`, `/auth/external*`, `/auth/exchange`, `/auth/preauthorize` | cần IdP ngoài/Google JWKS thật hoặc giả lập | IdP giả lập + `auth domain add` |
| `POST /email/v1/send` | route tắt mặc định, cần SMTP + feature `email` | SMTP giả (mailpit) |
| `POST /auth/newpatient` | cần project mở đăng ký (openPatientRegistration + chính sách Patient) mà seed không tạo | tạo project mở bằng admin/settings hoặc CLI trước |
| `POST /admin/.../members/{id}/resetpassword` | cần mailer cấu hình (`Email sending is not configured` nếu không) | SMTP giả |
| `/admin/super/*` | cần `AUTH_SUPER_ADMIN_ENABLED` và đánh dấu `auth project super` bằng CLI | chạy lệnh CLI trong container trước |
| `POST /admin/projects/{id}/client/{clientId}/smart-launch` | cần client có `launchUri` và `AUTH_FHIR_BASE_URL` | tạo client có launchUri trong scenario admin |
| Hạn mức IP (429) và khóa tài khoản | mặc định tắt ở local; bật làm mọi scenario khác bị 429 | môi trường riêng có `AUTH_RATE_LIMIT=2`, `AUTH_LOGIN_RATE_LIMIT=1`; lặp 5 lần bằng nhiều bước |
| Boundary email đúng 72 ký tự ở newuser | email phải duy nhất mỗi lần chạy mà độ dài cố định; `{{$timestamp}}` độ dài không cố định | pre-script sinh email theo độ dài |
