# auth: hợp đồng API

Cập nhật 2026-11-23. **Chỉ mô tả phần `auth` đang phục vụ.** Route của Medplum chưa port không có ở đây. Tài liệu chi tiết của chính auth (đăng ký, DTO, OAuth2, admin, SCIM, email) nằm ở thư mục [api-contract/](api-contract/) cạnh tệp này.

> **Chi tiết từng route (request, response, header, từng trường hợp)** ở [api-contract/common.md](api-contract/common.md), [api-contract/reference/](api-contract/reference/README.md) (bảng từng trường, sinh từ swagger) và [api-contract/cases/](api-contract/cases/README.md) (từng trường hợp phản hồi). Tệp này giữ bảng route tổng hợp và quy ước.

| Nguồn | Vai trò |
|---|---|
| `swagger.json` / `swagger.yaml` (cùng thư mục) | **Nguồn chuẩn** của route, tham số, DTO (`definitions`). Sinh từ annotation: `make swagger APP=auth` |
| Tệp này | Quy ước chung và bảng route; bảng **sinh từ `swagger.json`** |
| `/swagger/index.html`, `/swagger/doc.json` | Cùng spec, xem trực tiếp (tắt ở staging/production) |
| [auth-dto.md](api-contract/auth-dto.md), [oauth.md](api-contract/oauth.md), [admin.md](api-contract/admin.md), [email.md](api-contract/email.md) | Diễn giải, ví dụ JSON và luồng (viết tay, đã đối chiếu source Medplum) |

**Bốn test giữ hợp đồng không lệch** (đỏ khi lệch):

| Test | Bắt lỗi gì |
|---|---|
| `internal/bootstrap` `TestRoutesMatchSwaggerContract` | Route phục vụ thật (khi bật mọi tính năng tùy chọn) khác với `swagger.json`, theo cả hai chiều |
| `docs` `TestAPIContractIsUpToDate` | Bảng route dưới đây khác với `swagger.json` (`UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIContractIsUpToDate`) |
| `docs` `TestSwaggerDTOsAreComplete` | DTO của auth thiếu mô tả trường, thiếu danh sách `required`, thiếu `example` ở body yêu cầu; route thiếu tóm tắt/mô tả/schema phản hồi/mô tả tham số; tag thiếu mô tả |
| `architecture_test.go` | Phụ thuộc giữa các tầng (xem [architecture.md](architecture.md)) |

Các test này kiểm **độ đầy đủ và khớp route** của hợp đồng, **không** kiểm giá trị từng trường so với hành vi (mô tả, `required` do người viết struct đặt theo validator); hành vi do test HTTP ở `internal/bootstrap` và `e2e/` kiểm. Cách khai báo DTO: bình luận cuối dòng của trường là mô tả; `binding:"required"` đánh dấu trường bắt buộc (tag này chỉ để sinh swagger, validator thật là hàm `validateXxx`); `example:"..."` và `enums:"a,b"` cho giá trị mẫu và tập giá trị.

> **`extension` của các resource FHIR (`ProjectMembership`, `ClientApplication`, ...) được khai báo là mảng object tự do** trong `swagger.json`/`swagger.yaml`, không phải kiểu `fhir.Extension` đầy đủ (override ở `apps/auth/.swaggo`, dùng khi `make swagger APP=auth`). Lý do: kiểu đầy đủ kéo theo khoảng 30 kiểu dữ liệu FHIR (`Address`, `Dosage`, `TimingRepeat`...) làm spec phình gần gấp đôi và làm IDE đứng khi mở `swagger.yaml`. Cấu trúc thật của extension theo chuẩn FHIR R4 (`url` bắt buộc, kèm đúng một `value[x]`).

## 1. Địa chỉ và phiên bản

- Cổng mặc định `:8006` (image Docker: `:8000`). Địa chỉ công khai là `AUTH_BASE_URL`; đó cũng là `iss` của token trừ khi đặt `AUTH_ISSUER`.
- **Không có tiền tố phiên bản chung.** Chỉ `/email/v1/send` và `/scim/v2/Users` mang phiên bản (theo chuẩn của chúng). Đường dẫn khác giữ nguyên Medplum để client Medplum dùng lại được.
- Đổi route, tham số bắt buộc, hoặc ý nghĩa trường là thay đổi hợp đồng: phải cập nhật annotation, chạy `make swagger APP=auth` và `UPDATE_DOCS=1` cho bảng dưới; test ở trên sẽ đỏ nếu quên.

## 2. Xác thực

| Loại | Cách | Dùng ở |
|---|---|---|
| Công khai | Không cần header (nhiều route vẫn chịu hạn mức theo IP) | `/auth/login`, `/auth/newuser`, `/.well-known/*`, ... |
| Bearer | `Authorization: Bearer <access_token>`; token do chính `auth` ký và **có trạng thái**: login bị thu hồi thì `401` ngay | `/auth/me`, `/auth/revoke`, `/auth/mfa/*` (phần đã đăng nhập), `/oauth2/userinfo`, `/oauth2/introspect`, `/admin/*`, `/scim/*`, `/email/*` |
| Client OAuth | HTTP Basic (`client_id:client_secret`), hoặc `client_id` + `client_secret` trong body, hoặc `client_assertion` (`private_key_jwt`, RFC 7523) | `POST /oauth2/token` |
| Cookie đăng nhập | `medplum-<clientId>` (đặt khi `/auth/login` cho client có định danh); `auth-external-state` cho luồng đăng nhập ngoài | `/oauth2/authorize`, `/auth/external` |

Cột "Xác thực" trong bảng route chỉ có hai giá trị là `công khai` hoặc `Bearer`, theo khai báo `security` của swagger. `/oauth2/token` (xác thực client) và các route công khai cần cookie ghi chú ở mô tả của chúng trong `swagger.json`.

## 3. Định dạng lỗi

| Phạm vi | Định dạng | Ghi chú |
|---|---|---|
| `/auth/*`, `/admin/*`, `/email/*` | `OperationOutcome` (FHIR) | cấu trúc: `fhir.OperationOutcome` trong `swagger.json`; ví dụ ở [auth-dto.md](api-contract/auth-dto.md) |
| `/oauth2/*` | `{"error","error_description"}` (RFC 6749 §5.2) | HTTP `400` kể cả lỗi client, như Medplum; riêng `userinfo`, `introspect`, `logout` trả `401` khi thiếu hoặc sai Bearer |
| `/scim/v2/*` | `scim.Error` (RFC 7644) | |
| Hạn mức vượt | `429` + `Retry-After` + `OperationOutcome` (`throttled`) | MFA còn thêm header `rate-limit-reset` |
| Chưa sẵn sàng | `/health/ready` trả `503` khi đang tắt hoặc DB lỗi | |

Mã lỗi từng route nằm ở cột "Mã lỗi" của bảng dưới.

## 4. Quy ước chung

- Body JSON (`application/json`), **trừ** `POST /oauth2/token` chỉ nhận `application/x-www-form-urlencoded` (JSON bị từ chối `400`).
- Mọi phản hồi (kể cả `404` và lỗi) có header bảo mật: `Cache-Control: no-store`, CSP `default-src 'none'`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`; HSTS khi `AUTH_BASE_URL` là https. Ngoại lệ là `/swagger/*` (và JWKS có `Cache-Control: public, max-age=300` kèm `ETag`).
- CORS: chỉ origin trong `AUTH_ALLOWED_ORIGINS` (kèm credentials).
- Hạn mức theo IP: `AUTH_RATE_LIMIT` cho `/auth/`, `/oauth2/`, `/.well-known/`, `/admin/`; chặt hơn cho các bước nhập thông tin đăng nhập (`AUTH_LOGIN_RATE_LIMIT`). Giá trị mặc định ở [config.md](config.md).
- `POST /auth/login` trả đúng **một** dạng của `application.LoginResult`: `code` (đã chọn được profile), `memberships` (cần chọn profile), `mfaRequired` hoặc `mfaEnrollRequired`.
- Access token mang `fhir_project`, `scope` và `fhir_access_policy`. Scope client khai không bao giờ thành quyền: term `fhir.*` bị `invalid_scope`; `fhir.admin/write/read` do auth tự thêm theo membership.

### Claim `fhir_access_policy`

Mảng rule `[{resourceType, criteria, readonly, deny}]` mà `fhir-server` đọc bằng `pkg/fhirauth.ParsePolicy`:

- `resourceType`: loại tài nguyên, hoặc `*` cho mọi loại không được liệt kê (trừ loại quản trị).
- `criteria`: **chỉ** chuỗi query (`code=x&status=final`), không có tiền tố `Type?`. Auth tự bỏ tiền tố `Type?` khi khớp loại; lệch loại thì rule thành `deny`. Có thêm `_compartment=Patient/<id>` (OR của mọi tham số thuộc compartment Patient của loại đó). `_filter` bị từ chối.
- `readonly`: chỉ đọc. `deny`: cấm hẳn loại này (cũng là kết quả khi dịch thất bại: **fail đóng**).
- **Nhiều rule cùng loại là OR** (tối đa 16, cùng `readonly`, không trộn rule không có `criteria` hoặc `deny` với rule khác). Auth chỉ phát nhiều rule cùng loại khi bật `AUTH_PATIENT_COMPARTMENT_ENABLED`; tắt thì giữ hành vi cũ (một rule mỗi loại).
- Biến trong `criteria` do auth thay khi cấp token: `%patient`, `%patient.id` lấy từ tham số `patient` của `ProjectMembership.access[i]` (dạng `Patient/<id>`); `%profile`, `%profile.id` lấy từ profile (`Patient|Practitioner|RelatedPerson/<id>`). Giá trị sai định dạng hoặc thiếu thì loại đó bị `deny`. Vận hành: [runbook.md §12](runbook.md#12-bật-compartment-patient).

## 5. Route có điều kiện

Route sau **không tồn tại (404)** nếu điều kiện không thỏa; bảng route bên dưới liệt kê đủ chúng. (`AUTH_REGISTER_ENABLED=false` và `AUTH_NEW_PROJECT_REGISTRATION_ENABLED=false` không gỡ route `/auth/newuser`, `/auth/newproject`: chúng đổi hành vi, không đổi sự có mặt của route.)

| Route | Điều kiện |
|---|---|
| `/oauth2/authorize`, `/auth/external*`, `/auth/clientinfo/*` (đăng nhập ngoài) | đặt `AUTH_APP_BASE_URL` (mặc định `http://localhost:3000` khi `ENVIRONMENT=local`) |
| `POST /oauth2/register` | `AUTH_OAUTH_REGISTER_ENABLED=true` |
| `POST /auth/google` | `AUTH_GOOGLE_CLIENT_ID` hoặc `AUTH_GOOGLE_ENABLED=true` |
| `/admin/projects/*` | `AUTH_ADMIN_ENABLED=true`; người gọi là admin của project |
| `/scim/v2/*` | `AUTH_SCIM_ENABLED=true` **và** project bật tính năng `scim` (`auth project feature`); Basic cần `AUTH_SCIM_ALLOW_BASIC` kèm `AUTH_SCIM_ALLOWED_IPS` ở staging/production |
| `POST /email/v1/send` | `AUTH_EMAIL_ENABLED=true`, project bật tính năng `email`, người gọi là admin |
| `/swagger/*` | `ENVIRONMENT` khác `staging`/`production` |

Test route ở mục đầu bật cùng lúc `AUTH_APP_BASE_URL`, `AUTH_OAUTH_REGISTER_ENABLED`, `AUTH_ADMIN_ENABLED`, `AUTH_SCIM_ENABLED`, `AUTH_EMAIL_ENABLED`, `AUTH_GOOGLE_ENABLED`; nó chưa kiểm từng điều kiện tắt riêng lẻ có thật sự làm route biến mất.

## 6. Bảng route

Mỗi nhóm là một `tag` của swagger. Cột "Yêu cầu" cho tên schema của body (xem `definitions` trong `swagger.json`), hoặc danh sách tham số form/query; "Phản hồi thành công" cho mã 2xx/3xx kèm schema.

<!-- BEGIN GENERATED ROUTES (từ swagger.json; chạy UPDATE_DOCS=1 go test để sinh lại, đừng sửa tay) -->

### Nhóm `admin` (13 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `POST /admin/projects/setpassword` | Đặt mật khẩu cho user của project | Bearer | body `httpapi.AdminSetPasswordBody` | 200 `fhir.OperationOutcome` | 400, 401, 403, 429 |
| `GET /admin/projects/{projectId}` | Đọc project của chính mình (admin) | Bearer | - | 200 `httpapi.AdminProjectResponse` | 401, 403 |
| `POST /admin/projects/{projectId}/client` | Tạo ClientApplication trong project | Bearer | body `httpapi.AdminClientBody` | 201 `fhir.ClientApplication` | 400, 401, 403 |
| `POST /admin/projects/{projectId}/client/{clientId}/rotate-secret` | Xoay secret của client (ClientApplication/$rotate-secret) | Bearer | body `httpapi.RotateSecretBody` | 200 `httpapi.RotateSecretResponse` | 400, 401, 403, 429 |
| `POST /admin/projects/{projectId}/invite` | Mời user vào project | Bearer | body `httpapi.InviteBody` | 200 `fhir.ProjectMembership` | 400, 401, 403, 409, 429 |
| `GET /admin/projects/{projectId}/members/{membershipId}` | Đọc một membership của project | Bearer | - | 200 `fhir.ProjectMembership` | 401, 403 |
| `POST /admin/projects/{projectId}/members/{membershipId}` | Sửa membership | Bearer | body `fhir.ProjectMembership` | 200 `fhir.ProjectMembership` | 400, 401, 403, 409 |
| `DELETE /admin/projects/{projectId}/members/{membershipId}` | Xóa membership | Bearer | - | 200 `fhir.OperationOutcome` | 400, 401, 403 |
| `POST /admin/projects/{projectId}/members/{membershipId}/mfa/reset` | Gỡ một yếu tố MFA của member | Bearer | body `httpapi.MFAResetBody` | 200 `fhir.OperationOutcome` | 400, 401, 403, 429 |
| `POST /admin/projects/{projectId}/members/{membershipId}/resetpassword` | Gửi thư đặt lại mật khẩu cho một member | Bearer | - | 200 `fhir.OperationOutcome` | 400, 401, 403, 429 |
| `POST /admin/projects/{projectId}/settings` | Đặt các setting của project (allowlist) | Bearer | body `[]fhir.ProjectSetting` | 200 `httpapi.AdminProjectResponse` | 400, 401, 403 |
| `POST /admin/projects/{projectId}/sites` | Đặt các site của project (Google, reCAPTCHA) | Bearer | body `[]fhir.ProjectSite` | 200 `httpapi.AdminProjectResponse` | 400, 401, 403, 409 |
| `POST /admin/super/projects` | Tạo project mới (Project/$init) | Bearer | body `httpapi.SuperProjectBody` | 201 `httpapi.SuperProjectResponse` | 400, 401, 403 |

### Nhóm `auth` (28 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `POST /auth/changepassword` | Đổi mật khẩu của chính mình | Bearer | body `httpapi.ChangePasswordBody` | 200 `fhir.OperationOutcome` | 400, 401, 403 |
| `GET /auth/clientinfo/{clientId}` | Thông tin công khai của form đăng nhập của một client | công khai | query: redirect_uri | 200 `fhir.ClientApplicationSignInForm` | 400, 404 |
| `POST /auth/exchange` | Đổi access token của IdP ngoài lấy token của auth (deprecated) | công khai | body `httpapi.ExchangeBody` | 200 `object` | 400, 429 |
| `GET /auth/external` | Callback đăng nhập qua identity provider ngoài (OIDC) | công khai | query: code, state, iss | 302 | 400, 404, 429 |
| `POST /auth/external/start` | Bắt đầu đăng nhập qua identity provider RIÊNG của một client | công khai | body `httpapi.ExternalStartBody` | 200 `application.MethodResult` | 400, 404, 429 |
| `GET /auth/external/{clientId}` | Callback đăng nhập qua identity provider ngoài (OIDC) | công khai | query: code, state, iss | 302 | 400, 404, 429 |
| `POST /auth/google` | Đăng nhập bằng Google Identity Services | công khai | body `httpapi.GoogleBody` | 200 `application.LoginResult` | 400, 429 |
| `POST /auth/login` | Đăng nhập bằng email và mật khẩu | công khai | body `httpapi.LoginBody` | 200 `application.LoginResult` | 400 |
| `GET /auth/login/{login}` | Trạng thái một login đang dở | công khai | - | 200 `application.LoginResult` | 400, 404 |
| `GET /auth/me` | Thông tin người đang đăng nhập | Bearer | - | 200 `application.Me` | 401 |
| `POST /auth/method` | Cách đăng nhập của một email | công khai | body `httpapi.MethodBody` | 200 `application.MethodResult` | 400 |
| `POST /auth/mfa/disable` | Tắt MFA (một phương thức hoặc tất cả) | Bearer | body `httpapi.MFADisableBody` | 200 `fhir.OperationOutcome` | 400, 401, 403, 429 |
| `POST /auth/mfa/enroll` | Đăng ký thêm một phương thức MFA cho chính mình | Bearer | body `httpapi.MFAEnrollBody` | 200 `fhir.OperationOutcome` | 400, 401, 403, 429 |
| `POST /auth/mfa/login-enroll` | Đăng ký MFA ngay trong lúc đăng nhập | công khai | body `httpapi.MFALoginEnrollBody` | 200 `application.LoginResult` | 400, 429 |
| `POST /auth/mfa/send-email` | Gửi (lại) mã MFA qua email cho login đang chờ | công khai | body `httpapi.MFASendEmailBody` | 200 `fhir.OperationOutcome` | 400, 429 |
| `POST /auth/mfa/send-email-challenge` | Gửi mã email cho phiên đang dùng | Bearer | - | 200 `fhir.OperationOutcome` | 400, 401, 403, 429 |
| `GET /auth/mfa/status` | Trạng thái MFA của chính mình | Bearer | - | 200 `application.MFAStatusResult` | 401, 403 |
| `POST /auth/mfa/verify` | Xác minh mã MFA khi đăng nhập | công khai | body `httpapi.MFAVerifyBody` | 200 `application.LoginResult` | 400, 404, 429 |
| `POST /auth/newpatient` | Bệnh nhân tự đăng ký vào project | công khai | body `httpapi.NewPatientBody` | 200 `application.LoginResult` | 400, 404 |
| `POST /auth/newproject` | Tạo project mới cho người vừa đăng ký | công khai | body `httpapi.NewProjectBody` | 200 `application.LoginResult` | 400, 404, 429 |
| `POST /auth/newuser` | Đăng ký tài khoản | công khai | body `httpapi.NewUserBody` | 200 `httpapi.NewUserResponse` | 400, 404, 429 |
| `POST /auth/preauthorize` | Mint pre-authorized code cho một thành viên khác của project | Bearer | body `httpapi.PreAuthorizeBody` | 200 `application.PreAuthorizeResult` | 400, 401, 403, 404 |
| `POST /auth/profile` | Chọn profile (ProjectMembership) cho login | công khai | body `httpapi.ProfileBody` | 200 `application.LoginResult` | 400 |
| `POST /auth/resetpassword` | Yêu cầu thư đặt lại mật khẩu | công khai | body `httpapi.ResetPasswordBody` | 200 `fhir.OperationOutcome` | 400 |
| `POST /auth/revoke` | Thu hồi một phiên đăng nhập của chính mình | Bearer | body `httpapi.RevokeBody` | 200 `fhir.OperationOutcome` | 400, 401, 403, 404 |
| `POST /auth/scope` | Thu hẹp scope của login | công khai | body `httpapi.ScopeBody` | 200 `application.LoginResult` | 400 |
| `POST /auth/setpassword` | Đặt mật khẩu bằng link trong thư | công khai | body `httpapi.SetPasswordBody` | 200 `fhir.OperationOutcome` | 400, 404 |
| `POST /auth/verifyemail` | Xác minh email bằng link trong thư | công khai | body `httpapi.VerifyEmailBody` | 200 `fhir.OperationOutcome` | 400, 404 |

### Nhóm `email` (1 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `POST /email/v1/send` | Gửi thư (admin project) | Bearer | body `httpapi.SendEmailBody` | 200 `fhir.OperationOutcome` | 400, 401, 403, 502 |

### Nhóm `health` (2 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /health/live` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /health/ready` | Readiness probe (kiểm tra phụ thuộc) | công khai | - | 200 `httpapi.ReadyResponse` | 503 |

### Nhóm `oauth` (9 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /oauth2/authorize` | Bắt đầu luồng authorization code (OAuth2/OIDC) | công khai | query: response_type, client_id, redirect_uri, scope, state, nonce, code_challenge, code_challenge_method, aud, launch, prompt, max_age, id_token_hint | 302 | 400 |
| `POST /oauth2/authorize` | Bắt đầu luồng authorization code (OAuth2/OIDC) | công khai | query: response_type, client_id, redirect_uri, scope, state, nonce, code_challenge, code_challenge_method, aud, launch, prompt, max_age, id_token_hint | 302 | 400 |
| `POST /oauth2/introspect` | Kiểm tra một token (RFC 7662) | Bearer | form: token | 200 `httpapi.IntrospectResponse` | 400, 401 |
| `GET /oauth2/logout` | Đăng xuất (thu hồi login của access token) | Bearer | - | 200 `fhir.OperationOutcome` | 401 |
| `POST /oauth2/logout` | Đăng xuất (thu hồi login của access token) | Bearer | - | 200 `fhir.OperationOutcome` | 401 |
| `POST /oauth2/register` | Tra client dựng sẵn theo redirect URI | công khai | body `httpapi.RegisterBody` | 201 `application.RegisteredClient` | 400 |
| `POST /oauth2/token` | Đổi thông tin lấy token (OAuth2) | công khai | form: grant_type, code, code_verifier, redirect_uri, refresh_token, client_id, client_secret, scope | 200 `object` | 400 |
| `GET /oauth2/userinfo` | OIDC userinfo | Bearer | - | 200 `object` | 401 |
| `POST /oauth2/userinfo` | OIDC userinfo | Bearer | - | 200 `object` | 401 |

### Nhóm `scim` (6 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /scim/v2/Users` | SCIM: liệt kê user của project | Bearer | query: filter, startIndex, count | 200 `scim.ListResponse` | 400, 401, 403 |
| `POST /scim/v2/Users` | SCIM: tạo user | Bearer | body `scim.User` | 201 `scim.User` | 400, 409 |
| `GET /scim/v2/Users/{id}` | SCIM: đọc một user | Bearer | - | 200 `scim.User` | 404 |
| `PUT /scim/v2/Users/{id}` | SCIM: thay user (PUT) | Bearer | body `scim.User` | 200 `scim.User` | 400, 403, 404 |
| `PATCH /scim/v2/Users/{id}` | SCIM: sửa user (PATCH) | Bearer | body `scim.PatchOp` | 200 `scim.User` | 400 |
| `DELETE /scim/v2/Users/{id}` | SCIM: xóa user | Bearer | - | 204 | 400, 404 |

### Nhóm `well-known` (6 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /.well-known/jwks.json` | JWKS (khóa công khai để xác minh token) | công khai | - | 200 `object` | - |
| `GET /.well-known/oauth-authorization-server` | Discovery OIDC / OAuth 2.0 authorization server | công khai | - | 200 `object` | - |
| `GET /.well-known/oauth-protected-resource` | Protected resource metadata (RFC 9728) | công khai | query: resource | 200 `object` | 400 |
| `GET /.well-known/openid-configuration` | Discovery OIDC / OAuth 2.0 authorization server | công khai | - | 200 `object` | - |
| `GET /.well-known/smart-configuration` | SMART configuration | công khai | - | 200 `object` | - |
| `GET /.well-known/smart-styles.json` | SMART style (màu, font cho ứng dụng nhúng) | công khai | - | 200 `object` | - |

<!-- END GENERATED ROUTES -->

## 7. Ngoài bảng

- Probe không liệt kê hết trong swagger: `GET /healthz`, `/health/live`, `/health/startup`, `/health/ready` (xem README).
- `GET /.well-known/*` chỉ khai những gì đã cài (discovery không quảng bá grant hay phương thức chưa có).
