# Coverage: Trivita Auth (module 1533158, project 1380475)

## 1. Nguồn đã đọc
- Apidog nhánh `ai/20261125-from-main-trivita-platform-tests`: `spec/endpoints.json` (71 endpoint), `spec/openapi.json` (export OAS 3.1), `spec/endpoint-details/<id>.json` (`apidog endpoint get`). Module được tạo bằng import `apps/auth/docs/swagger.json` (Swagger 2.0, 62 path, 71 method).
- Mã và tài liệu hợp đồng của service: `apps/auth/docs/api-contract/common.md`, `cases/{admin,auth,oauth,scim,well-known,health,email}.md` (thông điệp lỗi nguyên văn từ mã/test), `reference/*.md`, `docs/config.md`, `README.md`, `tests/full_smoke` (seed + token), `docker-compose.full.yml`.
- Chưa đọc server sống (không chạy docker ở bước này).

## 2. Quy ước phản hồi (tóm tắt, chi tiết ở common.md)
| Vùng | Thành công | Lỗi |
|---|---|---|
| `/auth/*`, `/admin/*` | JSON / OperationOutcome `All OK` | OperationOutcome `application/fhir+json`: `issue[].code` (invalid 400, login 401, forbidden 403, not-found 404, conflict 409, throttled 429, exception 500), `details.text` nguyên văn, `expression` theo trường; validator gom nhiều issue theo thứ tự |
| `/oauth2/*` | JSON token/introspect | `{error, error_description}` HTTP **400 cho lỗi client**; 401 của userinfo/introspect/logout là OperationOutcome; sai Content-Type ở `/oauth2/token` là văn bản `Unsupported content type` |
| `/scim/v2/*` | `application/scim+json` | SCIM Error `{schemas,status(chuỗi),scimType,detail}`; 401 hai dòng WWW-Authenticate |
| `/.well-known/*`, `/health/*` | JSON | `{"error":...}`; `oauth-protected-resource` 400 là văn bản `Invalid resource URL` |
Access token do auth ký **có trạng thái** (login thu hồi ⇒ 401 ngay). Header bảo mật chung: `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Request-Id`...

## 3. Môi trường cần để chạy (điều kiện của test, không phải mặc định của compose)
`docker-compose.full.yml` mặc định KHÔNG bật nhiều route test cần. Dùng `dryrun/compose.apidog-auth.yml` (chồng lên compose full) để đặt:
| Biến | Giá trị | Vì sao |
|---|---|---|
| `AUTH_ADMIN_ENABLED` | `true` | `/admin/projects/*` mặc định 404 |
| `AUTH_SCIM_ENABLED` | `true` | `/scim/v2/*` mặc định 404 (scenario P2) |
| `AUTH_DEFAULT_PROJECT_FEATURES` | `scim` | project seed cần feature `scim` (seed đọc biến này) |
| `AUTH_PWNED_ENABLED` | `false` | HIBP gọi Internet và fail đóng (500) khi không tới được; chỉ được tắt ở local |
| `AUTH_COOKIE_SECURE` | `false` | base URL là http |
| `AUTH_MAIL_PROVIDER` | `none` | không in link bí mật ra log; test dùng `sendEmail:false` |
Rate limit để mặc định (tắt ở local) vì bật sẽ làm các scenario khác dính 429.

## 4. Bảng endpoint ⇄ scenario
| endpointId | Method Path | Scenario dùng |
|---|---|---|
| 47548420 | GET /.well-known/jwks.json | `auth-smoke`, `auth-wellknown-caching` |
| 47548421 | GET /.well-known/oauth-authorization-server | `auth-smoke` |
| 47548422 | GET /.well-known/oauth-protected-resource | `auth-smoke`, `auth-wellknown-resource` |
| 47548423 | GET /.well-known/openid-configuration | `auth-smoke` |
| 47548424 | GET /.well-known/smart-configuration | `auth-smoke` |
| 47548425 | GET /.well-known/smart-styles.json | `auth-smoke` |
| 47548426 | POST /admin/projects/setpassword | `auth-admin-lifecycle` |
| 47548427 | GET /admin/projects/{projectId} | `auth-admin-authz`, `auth-admin-lifecycle`, `auth-authz-bad-bearer`, `auth-signup-lifecycle` |
| 47548428 | POST /admin/projects/{projectId}/client | `auth-admin-authz`, `auth-admin-client-validation`, `auth-admin-lifecycle` |
| 47548429 | POST /admin/projects/{projectId}/client/{clientId}/rotate-secret | `auth-admin-authz`, `auth-admin-lifecycle` |
| 47548430 | POST /admin/projects/{projectId}/client/{clientId}/smart-launch | — (chỉ thiết kế) |
| 47548431 | POST /admin/projects/{projectId}/invite | `auth-admin-authz`, `auth-admin-invite-validation`, `auth-admin-lifecycle` |
| 47548434 | DELETE /admin/projects/{projectId}/members/{membershipId} | `auth-admin-authz`, `auth-admin-lifecycle` |
| 47548432 | GET /admin/projects/{projectId}/members/{membershipId} | `auth-admin-authz`, `auth-admin-lifecycle` |
| 47548433 | POST /admin/projects/{projectId}/members/{membershipId} | `auth-admin-lifecycle` |
| 47548435 | POST /admin/projects/{projectId}/members/{membershipId}/mfa/reset | — (chỉ thiết kế) |
| 47548436 | POST /admin/projects/{projectId}/members/{membershipId}/resetpassword | — (chỉ thiết kế) |
| 47548437 | POST /admin/projects/{projectId}/members/{membershipId}/update-email | `auth-admin-lifecycle` |
| 47548438 | POST /admin/projects/{projectId}/secrets | `auth-admin-lifecycle`, `auth-admin-secrets-validation` |
| 47548439 | POST /admin/projects/{projectId}/settings | `auth-admin-authz`, `auth-admin-lifecycle`, `auth-admin-settings-validation`, `auth-authz-bad-bearer` |
| 47548440 | POST /admin/projects/{projectId}/sites | `auth-admin-lifecycle`, `auth-admin-sites-validation` |
| 47548441 | POST /admin/super/projects | — (chỉ thiết kế) |
| 47548442 | POST /admin/super/projects/{projectId}/archive | — (chỉ thiết kế) |
| 47548443 | POST /admin/super/projects/{projectId}/restore | — (chỉ thiết kế) |
| 47548444 | POST /admin/super/users/{userId}/rescope | — (chỉ thiết kế) |
| 47548445 | POST /auth/changepassword | `auth-authz-bad-bearer`, `auth-signup-lifecycle` |
| 47548446 | GET /auth/clientinfo/{clientId} | `auth-public-lookups` |
| 47548447 | POST /auth/exchange | — (chỉ thiết kế) |
| 47548448 | GET /auth/external | — (chỉ thiết kế) |
| 47548449 | POST /auth/external/start | — (chỉ thiết kế) |
| 47548450 | GET /auth/external/{clientId} | — (chỉ thiết kế) |
| 47548451 | POST /auth/google | — (chỉ thiết kế) |
| 47548452 | POST /auth/login | `auth-admin-authz`, `auth-admin-client-validation`, `auth-admin-invite-validation`, `auth-admin-lifecycle` … |
| 47548453 | GET /auth/login/{login} | `auth-login-poll-scope` |
| 47548454 | GET /auth/me | `auth-authz-bad-bearer`, `auth-client-credentials-lifecycle`, `auth-code-reuse`, `auth-negative-misc` … |
| 47548455 | POST /auth/method | `auth-public-lookups` |
| 47548456 | POST /auth/mfa/disable | — (chỉ thiết kế) |
| 47548457 | POST /auth/mfa/enroll | — (chỉ thiết kế) |
| 47548458 | POST /auth/mfa/login-enroll | — (chỉ thiết kế) |
| 47548459 | POST /auth/mfa/send-email | — (chỉ thiết kế) |
| 47548460 | POST /auth/mfa/send-email-challenge | — (chỉ thiết kế) |
| 47548461 | GET /auth/mfa/status | `auth-authz-bad-bearer`, `auth-user-login-lifecycle` |
| 47548462 | POST /auth/mfa/verify | — (chỉ thiết kế) |
| 47548463 | POST /auth/newpatient | — (chỉ thiết kế) |
| 47548464 | POST /auth/newproject | `auth-signup-lifecycle` |
| 47548465 | POST /auth/newuser | `auth-signup-lifecycle`, `auth-signup-validation` |
| 47548466 | POST /auth/preauthorize | — (chỉ thiết kế) |
| 47548467 | POST /auth/profile | `auth-login-poll-scope` |
| 47548468 | POST /auth/resetpassword | `auth-public-lookups` |
| 47548469 | POST /auth/revoke | `auth-authz-bad-bearer`, `auth-user-login-lifecycle` |
| 47548470 | POST /auth/scope | `auth-login-poll-scope` |
| 47548471 | POST /auth/setpassword | — (chỉ thiết kế) |
| 47548472 | POST /auth/verifyemail | — (chỉ thiết kế) |
| 47548473 | POST /email/v1/send | — (chỉ thiết kế) |
| 47548474 | GET /health/live | `auth-scim-lifecycle`, `auth-smoke` |
| 47548475 | GET /health/ready | `auth-smoke` |
| 47548476 | GET /oauth2/authorize | — (chỉ thiết kế) |
| 47548477 | POST /oauth2/authorize | — (chỉ thiết kế) |
| 47548478 | POST /oauth2/introspect | `auth-authz-bad-bearer`, `auth-client-credentials-lifecycle`, `auth-negative-misc`, `auth-user-login-lifecycle` |
| 47548479 | GET /oauth2/logout | `auth-negative-misc` |
| 47548480 | POST /oauth2/logout | `auth-authz-bad-bearer`, `auth-client-credentials-lifecycle` |
| 47548481 | POST /oauth2/register | — (chỉ thiết kế) |
| 47548482 | POST /oauth2/token | `auth-admin-authz`, `auth-admin-client-validation`, `auth-admin-invite-validation`, `auth-admin-lifecycle` … |
| 47548483 | GET /oauth2/userinfo | `auth-authz-bad-bearer`, `auth-client-credentials-lifecycle`, `auth-user-login-lifecycle`, `auth-userinfo-scope` |
| 47548484 | POST /oauth2/userinfo | `auth-client-credentials-lifecycle` |
| 47548485 | GET /scim/v2/Users | `auth-scim-lifecycle`, `auth-scim-list-validation` |
| 47548486 | POST /scim/v2/Users | `auth-scim-create-validation`, `auth-scim-lifecycle` |
| 47548489 | DELETE /scim/v2/Users/{id} | `auth-scim-lifecycle` |
| 47548487 | GET /scim/v2/Users/{id} | `auth-scim-lifecycle` |
| 47548490 | PATCH /scim/v2/Users/{id} | `auth-scim-lifecycle` |
| 47548488 | PUT /scim/v2/Users/{id} | `auth-scim-lifecycle` |

26 route chưa dựng được (MFA, IdP ngoài, super-admin, email, authorize...): lý do và điều kiện dựng ở `scenarios/scenarios.md` mục "Chỉ thiết kế".

## 5. Lệch giữa spec (swagger/Apidog) và mã/tài liệu (M#)
- **M1** Swagger 2.0 khai `BearerAuth` là `apiKey` header `Authorization` (mô tả "Bearer <access_token>"); Apidog nhập thành scheme apiKey với giá trị `{{apiKey}}` (không tự thêm tiền tố "Bearer "). Bộ test không dùng scheme này mà đặt auth Bearer tường minh theo từng bước (`{{userToken}}`, `{{ccToken}}`...). Biến `apiKey` chỉ khai trong `environment.json` cho bộ kiểm tĩnh.
- **M2** Ví dụ swagger của `/oauth2/token` nêu `unsupported_grant_type`, nhưng mã **không bao giờ** phát mã này: grant lạ trả `invalid_request` `Unsupported grant_type` (cases/oauth.md). Bộ test assert theo mã.
- **M3** Ví dụ swagger của introspect/token trả `patient` dạng `Patient/p7`; mã trả id trần `p7`. Không có assertion trực tiếp (cần SMART launch).
- **M4** `common.md` mục 7 nói hạn mức bước đăng nhập áp cho mọi admin ghi; mã chỉ áp cho tám route (cases/admin.md "Phát hiện lệch"). Không ảnh hưởng test (rate limit tắt).
- **M5** `oauth.md` nói cả bốn grant kiểm `ipAccessRule`; mã `authorization_code` không kiểm (cases/oauth.md AC). Không có test (cần AccessPolicy ipAccessRule).

## 6. Câu hỏi mở / giả định cần kiểm ở lần chạy đầu (G#)
- **G1** Client seed (client mặc định của `seed project`) có đổi được `authorization_code` mà không cần redirectUri/PKCE, và `/auth/login` với `clientId` này không đòi `redirectUri`? Tài liệu nói client có secret không cần PKCE; nếu server trả `Invalid redirect URI`/PKCE thì scenario login phải gửi `redirectUri` (cần tạo client có redirectUris qua admin).
- **G2** Membership của client seed có `admin=false` (suy từ cases/admin.md "client chưa được nâng admin"): `auth-admin-authz` giả định 403. Nếu seed đặt admin thì đổi kỳ vọng.
- **G3** `newuser` với `projectId:"new"` có chấp nhận `codeChallenge` khi không có `clientId` và `authorization_code` không cần `client_id` (scenario `auth-signup-lifecycle`)? Suy từ README ("client công khai phải gửi codeChallenge ngay từ đầu").
- **G4** Email admin seed có phạm vi toàn cục (user không thuộc project) nên `newuser` trùng email ra `Email already registered` (hàng NU-18).
- **G5** Bước API-bound/customHttp có giữ được tiêu đề `If-None-Match` (scenario `auth-wellknown-caching`) và `Content-Type: application/json` ở `/oauth2/token` (auth-negative-misc)? Server Apidog từng bỏ header không khai báo ở bước API-bound; các bước này đều là customHttp (kỳ vọng status không phải 2xx) nhưng chưa kiểm chứng.
- **G6** `pm.variables.set(...)` và `pm.iterationData.get(...)` trong customScript Apidog (dùng để sinh email duy nhất, dựng `memberJson`, kiểm lỗi theo hàng): theo ghi chú CLI `pm.iterationData.get` chạy được; `pm.variables.set` chưa kiểm chứng.
- **G7** Hàng AC-22 (name 5000 ký tự → 201) và AC-12 (grantType) có thể bị server từ chối bằng thông điệp khác; kỳ vọng suy ra từ validator, chưa chạy.
- **G8** `{{authUrl}}` phải đúng bằng `AUTH_BASE_URL` (http://localhost:8006) để `oauth-protected-resource` hàng WK-01/02 ra 200.
- **G9** Nếu bật `AUTH_REDIS_URL` thì khóa tài khoản theo số lần sai (10 lần/15 phút) có thể khóa admin seed sau các hàng sai mật khẩu của `auth-login-validation` (LG-03..LG-12 ≈ 7 lần/lượt chạy). Compose full không có Redis cho auth nên mặc định không xảy ra.
