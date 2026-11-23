# Auth API — request và response (DTO)

> **Đã đối chiếu với source Medplum** (`packages/server/src/auth/*.ts`: `routes`, `login`, `method`, `profile`, `scope`, `exchange`, `google`, `newuser`, `newproject`, `newpatient`, `changepassword`, `resetpassword`, `setpassword`, `verifyemail`, `revoke`, `preauthorize`, `clientinfo`, `status`, `me`, `mfa`, `utils.sendLoginResult`). Chưa gọi thử server thật. Đây là hợp đồng của server Medplum TypeScript; fhir-server Go chưa có route `/auth` (xem [auth.md](auth.md)). Mục ghi **chưa đọc** là phần chưa mở trong source.

## Trạng thái cài đặt trong `apps/auth` (Go)

| Route | Trạng thái | Khác Medplum |
|---|---|---|
| `POST /auth/login`, `/auth/profile`, `/auth/scope`, `/auth/method`, `GET /auth/login/{login}` | **Đã cài** (P2) | Lỗi nội bộ trả `500` với thông điệp chung `Internal server error` (Medplum trả `400` kèm thông điệp lỗi). Nhánh cần MFA **fail đóng** tới khi có P5: trả `500`, không bao giờ trả `code`. |
| `GET /auth/me` | **Đã cài**, cần Bearer | `profile` chỉ có `{resourceType, id}` (hồ sơ đầy đủ ở `fhir-server`); chưa có `accessPolicy`; `config.menu` mặc định như Medplum. Tới khi có P3, mọi yêu cầu `Bearer` đều bị `401`. |
| `POST /auth/revoke` | **Đã cài**, cần Bearer | như trên (`401` tới khi có P3). Ca "on-behalf-of" chưa có. |
| `/auth/mfa/*` | **Đã cài (P5, 2026-11-22)**: xem mục "MFA" bên dưới, gồm các lệch có chủ đích so với Medplum | P5 |
| `external`, `clientinfo`, `method` (có state ký) | **Đã cài (P6 bước 6.0 đến 6.2, 2026-11-22)**: `POST /auth/method` nhận thêm `clientId`, `projectId`, `scope`, `nonce`, `launch`, `codeChallenge`, `codeChallengeMethod`, `redirectUri`, `returnTo`, trả `{domain, authorizeUrl}` với `authorizeUrl` ĐÃ KÈM `state` do auth ký và đặt cookie `auth-external-state` (app không tự thêm state); `POST /auth/external/start` (client có `identityProvider` riêng); `GET /auth/external[/{clientId}]` (callback, 302; lỗi trả OperationOutcome 400: `Missing code`, `Missing state`, `Invalid state`, `Invalid client`, `Identity provider not found`, `Invalid issuer`, `Missing JWKS URL for external identity provider`, `Failed to verify code - check your identity provider configuration`, `Email address is not verified by the identity provider`, `Email address does not match domain`, `Invalid project`, `Invalid redirect URI`, `User not found`); `GET /auth/clientinfo/{clientId}`. Lệch so với Medplum: state phải do auth ký và gắn cookie của trình duyệt, email IdP chưa xác minh bị từ chối, id_token phải có `jwksUrl`, kiểm redirect trước khi tạo Login, Login chỉ tạo sau khi biết có membership | P6 |
| `exchange` và grant `token-exchange` | **Đã cài (P6.3, 2026-11-22)**: xem mục `POST /auth/exchange`. Lệch so với Medplum: IdP cấu hình ở máy chủ bắt buộc `membership_id`; userinfo dạng JWT phải có chữ ký xác minh được; subject token là JWT thì `aud`/`azp` phải thuộc ứng dụng; email IdP chưa xác minh bị từ chối | P6 |
| `google` | **Đã cài (P6.4, 2026-11-22)**: bật bằng `AUTH_GOOGLE_ENABLED=true` hoặc `AUTH_GOOGLE_CLIENT_ID`. Lệch so với Medplum: credential phải báo `email_verified` là `true`, thiếu email là `400`, lỗi xác minh luôn là `Failed to verify Google credential` (không lộ chi tiết thư viện JWT), chỉ nhận RS256, tạo user qua đường có khóa chống trùng. Chưa port cập nhật ảnh đại diện (`googleAuthProfilePictures`) | P6 |
| `preauthorize` | **Đã cài (P6.5, 2026-11-22)**: header `X-Medplum-On-Behalf-Of` (`ProjectMembership/<id>` hoặc tham chiếu profile, cùng project, còn hoạt động; mọi thất bại là `403 Forbidden` chung). Validator chạy trước kiểm quyền (`Client ID is required`, `Scope must be a string`, `Nonce must be a string`, `expiresIn must be a positive integer not exceeding 604800 seconds`). Mã chỉ trả một lần, kho giữ SHA-256; dùng lại mã thu hồi cả token đã cấp. Tầng thứ tự: Bearer (401) rồi validator, nên thiếu Bearer luôn 401 dù body sai | P6 |
| `newpatient` | **Đã cài (P4.6, 2026-11-22)**: lỗi `Login already has a membership`, `Invalid project`, `Login revoked`, `Login granted`, `User must be scoped to the project`, `Project not active`, `Project does not allow open registration`, `Missing login` (trường `login`), `Project ID is required` (trường `projectId`). Lệch so với Medplum: kiểm trạng thái login, project và chính sách TRƯỚC khi tạo hồ sơ (L3); user phải thuộc ĐÚNG `projectId` (Medplum chỉ kiểm user có project); login thiếu MFA nhận phản hồi MFA mà chưa tạo gì | P4 |
| `newproject` | **Đã cài (P4.5, 2026-11-22)**: lỗi `Registration is disabled`, `New project registration is disabled`, `Login already has a membership`, `Invalid project`, `Login revoked`, `Login granted`, `Email verification is required to create a project. Check your email for a verification link.`, `Login ID is required.` (trường `login`), `Project name must be between 4 and 72 characters` (trường `projectName`). Lệch so với Medplum (L3): kiểm trạng thái login TRƯỚC khi tạo project (Medplum tạo project rồi mới báo `Login revoked`/`Login granted`); login còn thiếu MFA nhận phản hồi MFA mà không tạo project; thư chào dùng tên `Trivita` và bỏ các link quảng bá của Medplum | P4 |
| `newuser` | **Đã cài (P4.4, 2026-11-22)**: lỗi `Recaptcha token is required`, `Recaptcha failed`, `Invalid recaptchaSiteKey`, `Invalid recaptchaSecretKey`, `Project not active`, `Project does not allow open registration`, `Client and project do not match`, `Registration is disabled`, `Email already registered` (trường `email`), `Password found in breach database` (trường `password`). Lệch so với Medplum: kiểm PKCE/redirect/origin TRƯỚC khi tạo user (L1); lỗi gửi thư xác minh chỉ ghi log, vẫn 200 (L5); lỗi gọi reCAPTCHA/HIBP là 500 chứ không phải 400 kèm message; secret reCAPTCHA gửi trong body | P4 |
| `resetpassword`, `setpassword`, `verifyemail`, `changepassword` | **Đã cài (P4.3, 2026-11-22)**: lệch so với Medplum đã duyệt: thông báo thư dùng tên `Trivita` thay `Medplum`; `UserSecurityRequest.secret` lưu băm SHA-256; lỗi gửi thư của `resetpassword` bị nuốt (luôn 200); `sendEmail=false` không phát request; `verifyemail` kiểm `redirectUri` TRƯỚC khi tiêu thụ; reCAPTCHA của `resetpassword` có từ P4.4 (không kiểm project) | P4 |

Chi tiết đã khớp thêm ở phía Go: mọi phản hồi có `Cache-Control: no-store` và header bảo mật; CORS cho origin trong `AUTH_ALLOWED_ORIGINS` (mặc định `http://localhost:3000` ở môi trường `local`), lộ các header `Location`, `ETag`, `X-Request-Id`...; JSON hỏng trả `400 Content could not be parsed`, body quá lớn trả `413 Request body too large`; cookie đăng nhập `medplum-<clientId>` chỉ đặt khi phản hồi có `code` và login thuộc một client (`HttpOnly`, `Max-Age=3600`, `Secure` + `SameSite=None`; ở dev tắt `AUTH_COOKIE_SECURE` thì `SameSite=Lax`); IP người gọi (dùng cho `ipAccessRule`) là địa chỉ kết nối, **không** tin `X-Forwarded-For` trừ khi `AUTH_TRUSTED_PROXIES` chỉ rõ proxy tin cậy; user toàn cục (không gắn project) vẫn đăng nhập được qua client của project (như Medplum: thử user của project trước, rồi rơi về user toàn cục).

## Quy ước chung

- Request `Content-Type: application/json`. Mọi validator lỗi trả `400` `OperationOutcome`.
- "OK" nghĩa là `200` với `OperationOutcome` `allOk` (`{"resourceType":"OperationOutcome","id":"ok","issue":[{"severity":"information","code":"informational","details":{"text":"All OK"}}]}`).
- Mật khẩu: tối thiểu **8** ký tự, tối đa **72 byte** (`MIN_PASSWORD_LENGTH`, `MAX_PASSWORD_LENGTH`). Khi đặt mật khẩu mới còn kiểm tra rò rỉ (pwned password): trùng thì `400 "Password found in breach database"`.
- Endpoint **Bearer** cần `Authorization: Bearer <access_token>`; còn lại công khai.

## Kiểu dùng chung: `LoginResult`

Kết quả của `login`, `profile`, `scope`, `newproject`, `newpatient`, `google`, `exchange`, `mfa/verify`, `mfa/login-enroll` và `GET /auth/login/{login}` (hàm `sendLoginResult`). Xét theo thứ tự, dạng đầu tiên khớp được trả:

| # | Điều kiện | Response |
|---|---|---|
| 1 | MFA bắt buộc (`isMfaRequired`: project bắt buộc **hoặc** `User.mfaRequired`), user chưa đăng ký MFA, đăng nhập bằng mật khẩu, chưa xác minh MFA | `{ login, mfaEnrollRequired: true, enrollUri, enrollQrCode, allowedMfaMethods }` |
| 2 | User đã đăng ký MFA, đăng nhập bằng mật khẩu, chưa xác minh MFA | `{ login, mfaRequired: true, mfaMethods: string[], email }` |
| 3 | `login.project` là `Project/new` | `{ login }` (rồi gọi `/auth/newproject`) |
| 4 | Login đã có membership | `{ login, code }` (kèm cookie đăng nhập nếu là client OAuth) |
| 5 | Còn lại (nhiều profile) | `{ login, memberships: [{ id, project: Reference, profile: Reference, identifier? }] }` (rồi gọi `/auth/profile`) |

Ghi chú: ở dạng 2, nếu email là phương thức MFA duy nhất thì server tự gửi mã ngay (chỉ gửi một lần cho mỗi login). `code` đổi lấy token ở `POST /oauth2/token`. `Reference` là `{ "reference": "Project/<id>", "display"?: string }`.

## Đăng nhập

### `POST /auth/login`

| Trường | Kiểu | Bắt buộc | Ràng buộc / mặc định |
|---|---|---|---|
| `email` | string | có | email hợp lệ |
| `password` | string | có | 8–72 byte |
| `projectId` | string | không | UUID hoặc `"new"` |
| `clientId` | string | không | dùng để suy ra project |
| `scope` | string | không | mặc định `openid` |
| `nonce` | string | không | mặc định UUID ngẫu nhiên |
| `codeChallenge`, `codeChallengeMethod` | string | không | PKCE |
| `launch` | string | không | launch id (SMART) |
| `resourceType` | string | không | loại profile muốn chọn |
| `redirectUri` | string | không | |
| `remember` | boolean | không | |

Response `200`: `LoginResult`. `projectId: "new"` cho phép đăng nhập khi chưa có membership. Sai thông tin: `400`.

### `POST /auth/method`

Request `{ "email": string }`. Response `200`: `{}` nếu đăng nhập bằng mật khẩu; nếu domain của email cấu hình identity provider ngoài: `{ "domain": string, "authorizeUrl": string }` (URL kèm `client_id`, `redirect_uri=<baseUrl>auth/external`, `response_type=code`, `scope=openid profile email`).

### `POST /auth/profile`

Request `{ "login": string, "profile": string }` (`profile` là id `ProjectMembership`, cả hai bắt buộc). Response `200`: `LoginResult`. Membership không tồn tại: `400 "Profile not found"`.

### `POST /auth/scope`

Request `{ "login": string, "scope": string }` (bắt buộc). Response `200`: `LoginResult`.

### `GET /auth/login/{login}`

`login` là UUID. Response `200`: `LoginResult`. Lỗi `400`: `"Login already granted"` hoặc `"Login revoked"`.

### `POST /auth/google`

| Trường | Kiểu | Bắt buộc |
|---|---|---|
| `googleClientId` | string | có |
| `googleCredential` | string (JWT Google, RS256) | có |
| `redirectUri` | string | không |
| `projectId`, `clientId`, `resourceType`, `scope` (mặc định `openid offline_access`), `nonce`, `launch`, `codeChallenge`, `codeChallengeMethod` | string | không |
| `createUser` | boolean | không (tạo user nếu chưa có) |

Response `200`: `LoginResult`, hoặc `{ domain, authorizeUrl }` (kèm cookie `auth-external-state`) nếu domain email dùng identity provider ngoài. `googleClientId` phải là client chính (`AUTH_GOOGLE_CLIENT_ID`) hoặc khớp `Project.site.googleClientId`. Lỗi `400`: `Missing googleClientId`, `Missing googleCredential`, `Invalid redirectUri`, `Invalid googleClientId`, `Failed to verify Google credential`, `Google credential does not contain email address`, `Email address is not verified by Google`, `User not found` (khi không `createUser`), `Registration is disabled` (chỉ khi không có `projectId` cụ thể).

### `POST /auth/exchange`

Request `{ "externalAccessToken": string, "clientId": string, "membershipId"?: string }` (hai trường đầu bắt buộc: `Missing externalAccessToken`, `Missing clientId`, dạng OperationOutcome). Endpoint **deprecated**, thay bằng grant `urn:ietf:params:oauth:grant-type:token-exchange` ở `/oauth2/token` (tham số `client_id`, `subject_token`, `subject_token_type=urn:ietf:params:oauth:token-type:access_token`, `membership_id`, `scope`, `nonce`); cả hai gọi chung `ExchangeToken`. Response `200` là **body token** (`access_token`, `refresh_token`, `id_token`, `token_type`...), **không phải** `LoginResult`, kèm `Cache-Control: no-store`.

`clientId` là client id của một IdP cấu hình ở máy chủ (`AUTH_EXTERNAL_AUTH_PROVIDERS`) hoặc id `ClientApplication` có `identityProvider` riêng. Với IdP ở máy chủ, `membershipId` là **bắt buộc** (`Missing membership_id`, `Invalid membership`). Dạng lỗi: yêu cầu/IdP/danh tính là lỗi OAuth `{error, error_description}` 400 (`Invalid client`, `Invalid subject_token`, `Invalid subject_token_type`, `Missing user info URL`, thông báo cố định khi IdP lỗi; 429 của IdP giữ nguyên); lỗi đăng nhập (`User not found`, membership không thuộc user...) ở `/auth/exchange` là OperationOutcome 400 (như Medplum), ở `/oauth2/token` là lỗi OAuth `invalid_request`.

### `GET /auth/external`, `GET /auth/external/{clientId}`

Callback OIDC từ identity provider ngoài, query `code` và `state` (thiếu thì `400 "Missing code"` / `"Missing state"`). Nội dung chuyển hướng và lỗi: **chưa đọc** đầy đủ (`external.ts`, 482 dòng).

### `GET /auth/clientinfo/{clientId}`

Query tùy chọn `redirect_uri` (phải khớp URI đã đăng ký, sai thì `400 "Invalid redirect URI"`). Response `200`: nội dung `signInForm` của `ClientApplication` (object tùy chỉnh form đăng nhập; `{}` nếu không có), attachment được thay bằng presigned URL.

## Đăng ký

### `POST /auth/newuser` (reCAPTCHA)

| Trường | Kiểu | Bắt buộc | Ràng buộc |
|---|---|---|---|
| `firstName` | string | có | 1–128 ký tự |
| `lastName` | string | có | 1–128 ký tự |
| `email` | string | có | email hợp lệ, 3–72 ký tự |
| `password` | string | có | 8–72 byte |
| `projectId` | string | không | UUID hoặc `"new"` |
| `clientId` | string | không | phải thuộc `projectId` nếu có cả hai |
| `scope`, `nonce`, `codeChallenge`, `codeChallengeMethod`, `remember` | | không | như login |
| `recaptchaSiteKey`, token reCAPTCHA | string | tùy cấu hình | |

Response `200`: `{ "login": string, "emailVerificationRequired": boolean }`. Lỗi `400`: `"Registration is disabled"`, `"Email already registered"` (trường `email`), `"Client and project do not match"`, hoặc project không cho đăng ký.

### `POST /auth/newproject`

Request `{ "login": string (UUID), "projectName": string (4–72 ký tự) }`. Response `200`: `LoginResult` (dạng 4 sau khi tạo project và membership). Lỗi `400`: `"Registration is disabled"`, `"New project registration is disabled"`, `"Login already has a membership"`, `"Invalid project"`, hoặc `"Email verification is required to create a project..."` (khi bật `requireVerifiedEmailForProjectCreation` và email chưa xác minh; server gửi lại mail xác minh).

### `POST /auth/newpatient`

Request `{ "login": string, "projectId": string }` (cả hai bắt buộc). Response `200`: `LoginResult`. Lỗi `400`: `"Login already has a membership"`, `"Invalid project"`, `"User must be scoped to the project"`, `"Project does not allow open registration"`.

## Mật khẩu và email

| Endpoint | Request | Response `200` | Lỗi `400` thường gặp |
|---|---|---|---|
| `POST /auth/changepassword` (Bearer) | `oldPassword` (bắt buộc), `newPassword` (8–72 byte) | OK | `"Existing password not set"`, `"Incorrect password"` (trường `oldPassword`), pwned |
| `POST /auth/resetpassword` (reCAPTCHA) | `email` (3–72 ký tự); tùy chọn `projectId`, `redirectUri`, `sendEmail` (mặc định gửi) | OK, kể cả khi email không tồn tại (không lộ user) | `"Cannot reset password for external auth..."` |
| `POST /auth/setpassword` | `id` (UUID), `secret`, `password` (8–72 byte) | OK; đồng thời đánh dấu `emailVerified` và thu hồi mọi login của user | `"Already used"`, `"Expired"`, `"Incorrect secret"`, `"Invalid request type"`, pwned |
| `POST /auth/verifyemail` | `id` (UUID), `secret` | OK; hoặc chuyển hướng tới `redirectUri` nếu yêu cầu có lưu | `"Already used"`, `"Expired"`, `"Incorrect secret"`, `"Invalid user security request type"` |

## MFA (`/auth/mfa/*`)

Đã cài ở P5 (2026-11-22). Phản hồi `LoginResult` của `verify` và `login-enroll` như `/auth/login` (đặt cookie nếu login thuộc client). Mọi lỗi nghiệp vụ là `400` OperationOutcome; vượt hạn mức là `429` kèm header `Retry-After` và extension `https://medplum.com/fhir/StructureDefinition/rate-limit-reset` (giây). Lỗi hạ tầng (kho trạng thái hỏng, không gửi được thư mã) là `500` chung: **fail đóng**, không bao giờ cho qua yếu tố.

| Endpoint | Request | Response `200` | Lỗi `400` |
|---|---|---|---|
| `GET /status` (Bearer) | không | `{ enrolled, enrolledMethods[], allowedMethods[], mfaRequired, email, enrollUri?, enrollQrCode? }` (`enroll*` chỉ có khi chưa đăng ký TOTP; secret trong `enrollUri` chỉ thành yếu tố khi chính phiên này xác nhận) | `403` (xem bên dưới) |
| `POST /enroll` (Bearer) | `method` (`totp` mặc định, hoặc `email`), `token` | OK | `"Already enrolled"`, `"MFA method not allowed"`, `"Secret not found"` (trước `"Missing token"`), `"Missing token"`, `"Invalid token"`, `429` |
| `POST /login-enroll` | `login` (bắt buộc), `method`, `token` (bắt buộc với `totp`) | `LoginResult` | `"Missing login"`, `"Already enrolled"`, các lỗi `Login ...` bên dưới, `"MFA method not allowed"`, `"Secret not found"`, `"Missing token"`, `"Invalid MFA token"`, `429` |
| `POST /verify` | `login`, `token` (cả hai bắt buộc) | `LoginResult` | `"Missing login"`, `"Missing token"`, các lỗi `Login ...`, `"MFA code expired"`, `"Already enrolled"`, `"User not enrolled in MFA"`, `"Invalid MFA token"` (cùng thông báo cho TOTP và email), `429` |
| `POST /send-email` | `login` (bắt buộc) | OK | `"Missing login"`, các lỗi `Login ...`, `"User not enrolled in email MFA"`, `429` |
| `POST /send-email-challenge` (Bearer) | không | OK | `"Email MFA not available"`, `429` |
| `POST /disable` (Bearer) | `token`, `method` (tùy chọn) | OK | `"User not enrolled in MFA"` (kiểm trước kiểu của body), `"Token must be a string"` (trường `token`), `"Method must be a string"` (trường `method`), `"Invalid method"`, `"User not enrolled in MFA method"`, `"Cannot remove the last MFA factor because MFA is required"`, `"Missing token"`, `"Invalid token"`, `429` |

Các lỗi `Login ...` theo đúng thứ tự kiểm tra: `"Login revoked"`, `"Login granted"`, `"Login already verified"`, `"Invalid login authentication method"` (chỉ đăng nhập mật khẩu qua MFA), `"Login expired"` (login chỉ dùng được để xác minh MFA trong 10 phút kể từ `authTime`).

`403` ở các route Bearer: chủ thể không phải `User`, login có scope SMART, hoặc user toàn cục không đăng nhập bằng mật khẩu/Google.

**Hạn mức thử** (đặt chỗ NGUYÊN TỬ trước khi biết mã đúng hay sai): tối đa 5 lượt mỗi login và 20 lượt mỗi user trong 10 phút (cửa sổ cố định tính từ lượt đầu); lần sai thứ 5 thu hồi login; thành công trả lại một lượt của bộ đếm user. Áp cho `verify`, `login-enroll` **và, khác Medplum, cả `enroll` và `disable`** (Medplum chỉ có hạn mức IP chung ở hai route này nên mã email 6 chữ số dò được hàng nghìn lần). Gửi mã email tối đa 5 lần mỗi login trong 20 phút (`send-email`, `send-email-challenge`). Hạn mức IP: `verify`, `login-enroll`, `send-email` dùng chung bộ đếm các bước nhập thông tin đăng nhập (chặt hơn mức chung của `/auth/*`).

**Mã**: TOTP chuẩn RFC 6238 (SHA-1, 6 chữ số, bước 30 giây, chấp nhận lệch 1 bước mỗi bên), mỗi mã dùng MỘT lần cho mỗi user (nhớ 4 bước); mã dùng để đăng ký ở `enroll` thì KHÔNG bị tiêu thụ. Mã email 6 chữ số, lưu bcrypt, hạn 20 phút, một lần dùng, gửi lại ghi đè mã cũ. Secret TOTP 20 byte (base32), do server sinh cho mỗi cặp (user, login) và giữ 20 phút; chỉ thành `User.mfaSecret` khi chính login đó xác nhận bằng mã (hai login đăng ký cùng lúc: đúng một cái thắng).

**Lệch có chủ đích so với Medplum (đã duyệt 2026-11-22):** (Q6) mã email hết hạn không chặn mã TOTP đúng; (Q7) `User.mfaSecret` lưu mã hóa AES-256-GCM (`enc:v1:<kid>:...`, khóa từ `AUTH_MFA_SECRET_KEYS`, xoay khóa bằng cách thêm khóa mới lên đầu; giá trị cũ chưa mã hóa vẫn đọc được); (Q8) `POST /oauth2/token` từ chối đổi `code` (`invalid_grant`, `"MFA verification required"`) khi login mật khẩu của user đã đăng ký hoặc bị bắt buộc MFA mà chưa xác minh, và không tiêu thụ code; (L-M1) hạn mức thử cho `enroll`/`disable` và giới hạn gửi mã email; (L-M2) khóa Redis tiền tố `auth:mfa:`. Gỡ TOTP thì `mfaSecret` bị thay bằng secret ngẫu nhiên mới.

## Phiên

### `GET /auth/me` (Bearer)

Response `200`:

```json
{
  "user": { "resourceType": "User", "id": "...", "email": "...", "identifier": [] },
  "project": { "resourceType": "Project", "id": "...", "name": "...", "features": [], "description": "...", "strictMode": false, "superAdmin": false },
  "membership": { "resourceType": "ProjectMembership", "id": "...", "identifier": [], "user": {}, "profile": {}, "admin": false },
  "profile": { "resourceType": "Practitioner", "...": "..." },
  "config": { "resourceType": "UserConfiguration", "...": "..." },
  "accessPolicy": { "resourceType": "AccessPolicy", "...": "..." },
  "security": {
    "mfaEnrolled": false,
    "mfaRequired": false,
    "sessions": [{ "id": "...", "lastUpdated": "...", "authMethod": "password", "remoteAddress": "...", "browser": "...", "os": "...", "project": {} }],
    "memberships": []
  }
}
```

`user` và `security` chỉ có khi đăng nhập bằng `User` và không phải SMART scope hạn chế. Với `ClientApplication`, `profile` đã bỏ `secret` và `retiringSecret`. Các trường nhạy cảm bị ẩn theo access policy.

### `POST /auth/revoke` (Bearer)

Request `{ "loginId": string (UUID) }`. Response `200`: OK. Login không thuộc user gọi hoặc ngoài project: `404`.

### `POST /auth/preauthorize` (Bearer, project admin, không phải super admin)

| Trường | Kiểu | Bắt buộc | Ràng buộc |
|---|---|---|---|
| `clientId` | string | có | UUID của `ClientApplication` thuộc project |
| `scope` | string | không | phải là tập con scope của người gọi; mặc định `openid` (+ SMART scope của người gọi) |
| `nonce` | string | không | mặc định UUID |
| `expiresIn` | integer | không | 1–604800 giây, mặc định 3600 |

Response `200`: `{ "preAuthorizedCode": string, "expiresAt": string (ISO) }`. Dùng với grant `pre-authorized_code` ở `POST /oauth2/token`. Lỗi: `403` nếu không phải admin, là super admin, hoặc header on-behalf-of không hợp lệ; `400` `"Pre-authorization requires onBehalfOfMembership"` hoặc `"Invalid scope"` (trường `scope`); `404` nếu client ngoài project hoặc không có. Header bắt buộc: `X-Medplum-On-Behalf-Of`.

## Việc còn lại

1. `external.ts` và `GET /auth/external`: xem [../plans/auth/p6-p7-external-admin.md](../plans/p6-p7-external-admin.md) (chưa tự đối chiếu). Các điểm còn thiếu của `newuser`, reCAPTCHA và hạn mức IP: làm khi vào P4.
2. Bằng chứng chạy thật: gọi thử một server Medplum, hoặc viết test hợp đồng khi route được port sang Go (`apps/auth`).
