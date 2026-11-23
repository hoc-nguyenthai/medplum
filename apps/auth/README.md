# auth

Dịch vụ xác thực và cấp token, port từ Medplum (đăng nhập, MFA, OAuth2/OIDC). Kế hoạch: [docs/plans/auth/README.md](../../docs/plans/auth/README.md). Hợp đồng API: [docs/api-contract/auth-dto.md](../../docs/api-contract/auth-dto.md).

**Trạng thái: xong P0 (khung), P1 (lõi dữ liệu và an toàn), bước 7.0 (CLI), P2 (đăng nhập lõi qua HTTP) và P3 một phần (B1 khóa/JWKS/well-known, B2 `client_credentials` và Bearer, B3 `authorization_code` + PKCE, B4 `refresh_token`, B5 `userinfo`/`introspect`/`logout`, B6 `authorize`, B7 `private_key_jwt`/`pre-authorized_code`/`register`, B8 tích hợp `fhir-server` (test hai tiến trình `make test-e2e`); còn rà soát bảo mật B9).** Chưa có đăng ký và mật khẩu qua HTTP (P4), MFA (P5), đăng nhập ngoài (P6).

| Route | Ghi chú |
|---|---|
| `POST /auth/login`, `/auth/profile`, `/auth/scope`, `/auth/method`; `GET /auth/login/{login}` | công khai; mô tả trong `/swagger/index.html` |
| `POST /auth/newuser` | công khai, hạn mức `login` (chặt hơn). Tạo user rồi Login CHƯA có membership; phản hồi `{login, emailVerificationRequired}`, KHÔNG có `code`. reCAPTCHA kiểm trước cờ `AUTH_REGISTER_ENABLED`. `projectId`: `new`, id project cho đăng ký mở (`openPatientRegistration` + chính sách Patient), hoặc bỏ trống; chỉ gửi `clientId` thì user không thuộc project nào. Kiểm tra PKCE/redirect TRƯỚC khi tạo user (không để user mồ côi); trùng email được chặn bằng khóa theo (phạm vi, email) trong transaction |
| `POST /auth/newproject` | công khai, hạn mức `login`. Dùng `login` từ `/auth/newuser`: tạo project (6 chính sách mặc định, client mặc định, hồ sơ Practitioner, membership admin) trong MỘT transaction rồi trả `code` như `/auth/login`. Mọi kiểm tra trạng thái login (đã thu hồi, đã cấp code, đã có membership, thuộc project khác) chạy TRƯỚC khi tạo project; login còn thiếu MFA nhận `mfaRequired`/`mfaEnrollRequired` và chưa tạo gì. Chủ project lần đầu nhận thư chào |
| `POST /auth/newpatient` | công khai, không có hạn mức riêng (chỉ hạn mức chung của `/auth/*`, như Medplum). Dùng `login` từ `/auth/newuser` của user thuộc project (`projectId` trong body của newuser): tạo hồ sơ Patient và membership kèm chính sách đăng ký mở trong một transaction rồi trả `code`. Project phải đang hoạt động và có `openPatientRegistration` cùng chính sách Patient (hoặc `defaultPatientAccessPolicy`); user phải thuộc ĐÚNG project đó. Mọi kiểm tra chạy trước khi tạo gì |
| `POST /auth/external/start`, `GET /auth/external[/{clientId}]`, `GET /auth/clientinfo/{clientId}` | đăng nhập qua IdP ngoài (OIDC), chỉ có khi đặt `AUTH_APP_BASE_URL`. `POST /auth/method` phát `state` do auth ký kèm cookie `auth-external-state`; callback chỉ nhận state đó từ đúng trình duyệt, xác minh `id_token` bằng JWKS (bắt buộc `jwksUrl`), từ chối email IdP chưa xác minh, và chỉ tạo Login khi có membership. Cấu hình IdP bằng `auth domain add` (URL phải https, không nội bộ). Callback và `start` chịu hạn mức của bước nhập thông tin đăng nhập |
| `POST /auth/resetpassword`, `/auth/setpassword`, `/auth/verifyemail` | công khai (P4.3). `resetpassword` luôn trả 200 với email hợp lệ, có tài khoản hay không, gửi thư được hay không; link trong thư dùng một lần, `reset` 1 giờ, `verify-email` 7 ngày; kho chỉ giữ BĂM của secret. `setpassword` thu hồi mọi phiên. Cần `AUTH_APP_BASE_URL` để dựng link (không có thì bỏ qua, ghi cảnh báo) |
| `POST /auth/mfa/verify`, `/auth/mfa/login-enroll`, `/auth/mfa/send-email` | công khai theo id login, dùng chung hạn mức IP của bước nhập thông tin đăng nhập; mỗi lượt thử còn bị giới hạn theo login (5) và theo user (20) trong 10 phút, vượt thì `429` kèm `rate-limit-reset` |
| `GET /auth/mfa/status`, `POST /auth/mfa/enroll`, `/auth/mfa/send-email-challenge`, `/auth/mfa/disable` | cần Bearer |
| `GET /auth/me`, `POST /auth/revoke`, `POST /auth/changepassword` | cần Bearer (access token do chính auth ký, có trạng thái: login bị thu hồi thì 401 ngay) |
| `GET\|POST /oauth2/authorize` | chuyển hướng 302; chỉ có khi đặt `AUTH_APP_BASE_URL` (xem bên dưới) |
| `POST /oauth2/token` | grant `client_credentials`, `authorization_code` và `refresh_token` (xem bên dưới); chỉ `application/x-www-form-urlencoded`; client xác thực bằng HTTP Basic (đúng RFC 6749 §2.3.1) hoặc `client_id`/`client_secret` trong body |
| `GET\|POST /oauth2/userinfo`, `POST /oauth2/introspect`, `GET\|POST /oauth2/logout` | cần Bearer (xem bên dưới) |
| `GET /.well-known/{jwks.json, openid-configuration, oauth-authorization-server, smart-configuration, smart-styles.json, oauth-protected-resource}` | công khai; discovery chỉ khai thứ đã cài |

**P4 (đăng ký, mật khẩu, thư) đã xong** (bộ test bảo mật S1-S17 và e2e ở `e2e/signup_test.go`). Client công khai đăng ký qua `/auth/newuser` phải gửi `codeChallenge` ngay từ đầu để đổi được code sau `/auth/newproject`.

**MFA (P5) đã xong**: TOTP và mã email, đăng ký khi đăng nhập hoặc khi đã đăng nhập, hạn mức thử, tắt MFA, xem hợp đồng ở [docs/api-contract/auth-dto.md](../../docs/api-contract/auth-dto.md) mục MFA. Mọi nhánh cần MFA vẫn **fail đóng**: thiếu phụ thuộc MFA hay kho trạng thái hỏng thì trả 500 chung, không bao giờ trả `code`. Phần lõi (P1) gồm:

| Tầng | Có gì |
|---|---|
| `domain` | lỗi nghiệp vụ, độ dài mật khẩu (8 ký tự, 72 byte), secret và so sánh thời gian hằng, vòng đời `UserSecurityRequest`, IP rules, scope SMART (`IsSmartScopeSubset`...), `InactiveReason`, MFA helper, client dựng sẵn, `IncludeRefreshToken` |
| `application` | `Service`: tra cứu client/user/membership, `SetLoginMembership` (kèm IP và SMART), `SetLoginScope`, `GrantLogin`, `RevokeLogin`, security request, `SetPassword` (transaction), `RevokeUserLogins` |
| `infrastructure` | `store` (kho trên `pkg/fhirstore`, `Mutate` có thử lại thay `patchResource`), `password` (bcrypt, đọc được hash `$2b$` của Node), `pwned` (HIBP k-anonymity, fail đóng) |

Test chạy trên bộ nhớ và SQLite; thêm PostgreSQL thật bằng `make test-auth-pg` (cần Docker).


> **Docker:** `apps/auth/Dockerfile` là Dockerfile riêng của service, build từ gốc repo: `make docker APP=auth` hoặc `docker build -f apps/auth/Dockerfile --build-context observability=../trivita-observability-go -t trivita-auth .`. e2e dựng chính image này: `make test-service APP=auth`.
>
> **Đổi tên (2026-11-22):** service trước đây tên `authen` nay là `auth` (giống `auth` của Medplum). Thay đổi cho người triển khai:
> thư mục `apps/auth`, binary và lệnh `auth`, image `trivita-auth`, service compose `auth` (và `auth-migrate`), target Make `run-auth` và `test-auth-pg`,
> tên telemetry `trivita-auth`, tệp SQLite dev mặc định `./auth.db`, và **tiền tố biến môi trường `AUTHEN_*` thành `AUTH_*`** (ví dụ `AUTHEN_BASE_URL` thành
> `AUTH_BASE_URL`). Service **từ chối khởi động** nếu còn biến `AUTHEN_*` và liệt kê tên mới cần đổi, để cấu hình cũ không bị bỏ qua âm thầm.
> **Không đổi:** schema PostgreSQL `identity` và các role `identity_app`, `identity_migrate` (tên miền nghiệp vụ, đổi sẽ cần di chuyển dữ liệu), đường dẫn HTTP.
> Tệp SQLite dev cũ `authen.db`, nếu cần giữ dữ liệu, đổi tên thành `auth.db` hoặc đặt `DATABASE_URL=sqlite://./authen.db`.

## Chạy

```bash
make run APP=auth                  # :8006, SQLite ./auth.db (chỉ dev); đọc apps/auth/.env nếu có
make swagger APP=auth              # sinh lại apps/auth/docs sau khi sửa annotation
cd apps/auth && go run ./cmd/auth migrate status   # up | down | status
```

| Đường dẫn | Mô tả |
|---|---|
| `GET /healthz`, `/health/live`, `/health/startup` | còn sống |
| `GET /health/ready` | DB và trạng thái nhận traffic (503 khi đang tắt) |
| `GET /swagger/index.html`, `/swagger/doc.json` | chỉ khi `ENVIRONMENT` khác staging/production |

## Header bảo mật và CORS

Mọi phản hồi (kể cả 404 và lỗi) có header bảo mật như Medplum, trong đó `Cache-Control: no-store` (phản hồi có thể chứa mã đăng nhập, sau này token), CSP `default-src 'none'`, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`; HSTS chỉ khi `AUTH_BASE_URL` là https. Riêng `/swagger/*` (chỉ ngoài production) được bỏ qua để giao diện tải được script. CORS (gói dùng chung `pkg/httpsec`) chỉ áp cho `/auth/`, `/oauth2/`, `/.well-known/`, `/admin/` và chỉ với origin trong `AUTH_ALLOWED_ORIGINS` (mặc định `http://localhost:3000` khi `ENVIRONMENT=local`); phản chiếu đúng origin kèm credentials, không bao giờ trả `*`.

**Hạn mức theo IP** (trong bộ nhớ từng instance, trước mọi xử lý, trả 429 + `Retry-After` + OperationOutcome `throttled`): `AUTH_RATE_LIMIT` (mặc định 20 rps, burst 40) áp cho `/auth/`, `/oauth2/`, `/.well-known/`, `/admin/`; thêm `AUTH_LOGIN_RATE_LIMIT` (mặc định 1 rps, burst 10, tức khoảng 60 lần thử/phút/IP) riêng cho `POST /auth/login|profile|scope|method` để chậm việc dò mật khẩu. Staging/production tự bật nếu không đặt; dev tắt; đặt `0` để tắt hẳn (có cảnh báo). Đứng sau proxy phải khai `AUTH_TRUSTED_PROXIES`, không thì mọi người dùng chung một hạn mức. Đây chưa phải khóa tài khoản theo số lần sai (chưa làm, xem [docs/plans/task-list.md](../../docs/plans/task-list.md) #13).

## Lệnh quản trị (bước 7.0)

Thay `/admin/*` HTTP (hoãn) để có dữ liệu thử và vận hành tối thiểu. Mật khẩu **chỉ** nhận qua biến môi trường hoặc stdin, không qua tham số dòng lệnh; secret của client chỉ hiện một lần. Xem đầy đủ bằng `auth help`.

```bash
echo 'mat-khau-admin' | auth seed project -name "Bệnh viện A" -admin-email admin@example.org -admin-first An -admin-last Nguyen
auth client create -project <projectId> -name "Web" -redirect-uri https://app.example.org/cb -grant-type refresh_token
AUTH_NEW_PASSWORD=... auth user set-password -email admin@example.org
auth purge-logins -before 2026-01-01 -dry-run        # đếm trước; thêm -yes để xóa VĨNH VIỄN
```

`seed project` tạo trong một transaction: admin (nếu chưa có; có rồi thì giữ nguyên mật khẩu), 6 access policy mặc định (Patient, RelatedPerson, Admin, Practitioner, Bot, ClientApplication), project (`strictMode`), client mặc định cùng membership, Practitioner và membership admin. Lỗi giữa chừng hoàn tác hết. `domain add` (đăng nhập ngoài) để P6, vì cần chống SSRF khi lưu URL IdP.

## Cấu hình

Xem [.env.example](.env.example). Điều bắt buộc nhớ: dữ liệu của auth (`User`, `Login`, `UserSecurityRequest` chứa bí mật) phải nằm ở **schema riêng hoặc database riêng**, không lẫn bảng với dữ liệu lâm sàng của fhir-server. Chung một PostgreSQL với `fhir-server` thì dùng role `identity_app` có `search_path = identity` và bật `DATABASE_REQUIRE_SCHEMA=true` (xem [docs/runbook.md mục 12](../../docs/runbook.md)); staging/production cấm SQLite, cấm `DEBUG`, bắt buộc https.

## Cấu trúc

```
cmd/auth            điểm vào: serve | migrate | healthcheck
internal/domain       luật thuần (chỉ stdlib và pkg/fhir)
internal/application  use case và cổng (ports.go)
internal/infrastructure adapter: kho trên pkg/fhirstore, bcrypt, TOTP, mail...
internal/presentation/httpapi  Echo, DTO, validator, ánh xạ lỗi
internal/bootstrap    composition root
docs/                 OpenAPI do swaggo sinh
```

Quy tắc phụ thuộc giữa các tầng được `architecture_test.go` (gốc repo) kiểm tra.

## Token và tích hợp với `fhir-server`

`auth` ký access token (RS256 mặc định, ES256/ES384 tùy chọn) mà `fhir-server` nhận qua JWKS. Cấu hình `fhir-server`: `FHIR_JWT_ISSUER` = `AUTH_ISSUER` (mặc định `AUTH_BASE_URL`, **đúng từng ký tự**), `FHIR_JWT_AUDIENCE` = `AUTH_AUDIENCE` (mặc định chính issuer), `FHIR_JWKS_URL` = `<AUTH_BASE_URL>/.well-known/jwks.json`, và `FHIR_JOB_SIGNING_KEY` (bắt buộc khi dùng JWKS).

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `AUTH_SIGNING_KEY_PATH` | rỗng | thư mục chứa khóa riêng `*.pem` (sinh bằng `auth keys generate`). **Bắt buộc ở staging/production**; ở dev để trống thì dùng khóa ES256 tạm trong bộ nhớ |
| `AUTH_ACTIVE_KID` | rỗng | chọn khóa ký khi thư mục có nhiều khóa (xoay khóa); rỗng chỉ hợp lệ khi có đúng một khóa |
| `AUTH_SIGNING_ALG` | `RS256` | thuật toán của khóa do `keys generate` sinh: `RS256` (RSA 3072), `ES256`, `ES384` |
| `AUTH_ACCESS_TTL`, `AUTH_REFRESH_TTL`, `AUTH_ID_TTL` | `15m`, `336h`, `1h` | dạng Go. `AUTH_ACCESS_TTL` là cửa sổ thu hồi ở `fhir-server` (nó không tra Login mỗi yêu cầu) |
| `AUTH_MAIL_PROVIDER` | `log` ở local/development/test, `none` ở staging/production | kênh gửi thư: `log` in thư (KÈM link có secret) ra log (**cấm ở staging/production**), `none` bỏ qua, `smtp` gửi thật qua SMTP (xem các biến `AUTH_SMTP_*` bên dưới) |
| `AUTH_SMTP_HOST`, `AUTH_SMTP_PORT` | rỗng, `587` | máy chủ SMTP (bắt buộc khi `AUTH_MAIL_PROVIDER=smtp`); cổng 465 hoặc `AUTH_SMTP_SECURE=true` là TLS ngay từ đầu, ngược lại STARTTLS |
| `AUTH_SMTP_FROM` | rỗng | người gửi mặc định, `a@b` hoặc `Tên <a@b>` (bắt buộc); `AUTH_SMTP_APPROVED_SENDERS` là các địa chỉ trần khác (cách nhau bằng dấu phẩy) được đề nghị làm người gửi |
| `AUTH_SMTP_USERNAME`, `AUTH_SMTP_PASSWORD` | rỗng | đăng nhập SMTP (PLAIN hoặc LOGIN; chỉ gửi sau khi đã mã hóa). Bỏ trống username nếu relay không cần đăng nhập. Mật khẩu chỉ qua biến môi trường, không bao giờ vào log |
| `AUTH_SMTP_SECURE` | rỗng (suy ra theo cổng) | `true` TLS ngay từ đầu, `false` STARTTLS |
| `AUTH_SMTP_CA_FILE` | rỗng | tệp PEM CA riêng được tin thêm (relay nội bộ dùng chứng chỉ tự cấp) |
| `AUTH_SMTP_TIMEOUT` | `20s` | chờ nối và nhận lời chào (sau đó là 120s nhàn rỗi, 5 phút tổng) |
| `AUTH_SMTP_ALLOW_PLAINTEXT` | `false` | **chỉ dev**: cho gửi không mã hóa khi máy chủ thiếu STARTTLS; cấm ở staging/production. Mặc định TLS là bắt buộc: thư và mật khẩu không bao giờ đi ở dạng rõ |
| `AUTH_EMAIL_ENABLED` | `false` | bật `POST /email/v1/send` (admin project có tính năng `email`); xem `docs/api-contract/others.md` |
| `AUTH_EMAIL_ALLOW_PROJECT_SMTP` | `false` | cho dùng SMTP riêng khai ở `Project.secret` (mật khẩu nằm dạng rõ ở đó: bật có cân nhắc) |
| `AUTH_EMAIL_FHIR_URL` | trống | gốc API FHIR (ví dụ `https://fhir.internal/fhir/R4`) để `POST /email/v1/send` đọc tệp đính kèm `Binary/<id>` **bằng chính Bearer token của người gọi** (quyền, project, `securityContext` do fhir-server quyết định; token phải được fhir-server chấp nhận). Rỗng: `Binary/<id>` bị từ chối là chưa hỗ trợ. Địa chỉ nội bộ được phép (do vận hành viên đặt) nhưng không theo redirect, không dùng proxy môi trường; staging/production bắt buộc https vì token được gửi tới đó |
| `AUTH_EMAIL_FHIR_TIMEOUT` | `30s` | timeout đọc một Binary từ dịch vụ FHIR |
| `AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND` | `false` | **chỉ dev/test**: tắt chặn địa chỉ nội bộ của SMTP project và tệp đính kèm URL; cấm ở staging/production |
| `AUTH_EMAIL_MAX_RECIPIENTS`, `AUTH_EMAIL_MAX_BYTES` | `50`, `10485760` | tổng người nhận mỗi thư; cỡ thư và cỡ mỗi tệp tải theo URL |
| `AUTH_RECAPTCHA_SITE_KEY`, `AUTH_RECAPTCHA_SECRET_KEY` | rỗng | reCAPTCHA mức máy chủ cho `newuser` và `resetpassword`; `SECRET_KEY` rỗng thì không kiểm (project vẫn đặt khóa riêng được trong `Project.site`). Lỗi gọi Google là lỗi 500 (fail đóng), không cho qua. Secret gửi trong body POST, không đặt trong URL |
| `AUTH_RECAPTCHA_TIMEOUT` | `3s` | thời gian chờ Google; phải > 0 |
| `AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION` | `false` | `newuser` với `projectId=new` gửi thư xác minh và trả `emailVerificationRequired: true` |
| `AUTH_MFA_SECRET_KEYS` | rỗng (lưu thô, chỉ dev/test) | danh sách `kid:base64(32 byte)` cách nhau bằng dấu phẩy để mã hóa `User.mfaSecret` khi lưu; khóa đầu tiên mã hóa, mọi khóa giải mã (xoay khóa: thêm khóa mới lên đầu). **Bắt buộc ở staging/production**, sai định dạng thì không khởi động |
| `AUTH_MFA_WINDOW`, `AUTH_MFA_LOGIN_ATTEMPT_LIMIT`, `AUTH_MFA_USER_ATTEMPT_LIMIT` | `1`, `5`, `20` | số bước TOTP chấp nhận mỗi bên; lượt thử tối đa mỗi login và mỗi user |
| `AUTH_MFA_LOGIN_EXPIRATION`, `AUTH_MFA_EMAIL_CODE_EXPIRATION`, `AUTH_MFA_ENROLLMENT_EXPIRATION` | `10m`, `20m`, `20m` | tuổi tối đa của login chờ MFA (cũng là cửa sổ đếm), hạn mã email, hạn secret được mời |
| `AUTH_MFA_EMAIL_SEND_LIMIT` | `5` | số lần gửi mã email tối đa mỗi login trong thời hạn mã |
| `AUTH_EXTERNAL_STATE_KEY` | rỗng (khóa tạm trong bộ nhớ, chỉ dev/test) | khóa HMAC ký `state` đăng nhập ngoài (base64, tối thiểu 32 byte), mọi instance dùng chung; **bắt buộc ở staging/production khi có `AUTH_APP_BASE_URL`** |
| `AUTH_GOOGLE_CLIENT_ID` | rỗng | Google client chính của `POST /auth/google`; đặt giá trị này (hoặc `AUTH_GOOGLE_ENABLED=true`) thì route được bật; client của project khai ở `Project.site.googleClientId` cũng được nhận |
| `AUTH_GOOGLE_ENABLED` | `false` | bật `/auth/google` chỉ với client của project (không có client chính) |
| `AUTH_ADMIN_ENABLED` | `false` | bật `/admin/projects/*` (quản trị project qua HTTP, chỉ Bearer của admin project); xem `docs/api-contract/admin.md` |
| `AUTH_BLOCKED_EMAIL_DOMAINS` | rỗng | các tên miền email bị chặn khi mời Practitioner (cách nhau bằng dấu phẩy; ưu tiên hơn `allowedPractitionerEmailDomain` của project) |
| `AUTH_SCIM_ENABLED` | `false` | bật `/scim/v2/Users` (SCIM 2.0); project còn phải có tính năng `scim` (`auth project feature -project <id> -feature scim`) |
| `AUTH_SCIM_ALLOW_BASIC` | `true` | cho `Authorization: Basic` của client có quyền admin (Okta chỉ có Basic). **Staging/production bắt buộc kèm `AUTH_SCIM_ALLOWED_IPS`** |
| `AUTH_SCIM_ALLOWED_IPS` | rỗng | CIDR (cách nhau bằng dấu phẩy) được gọi `/scim/v2`; rỗng là không hạn chế |
| `AUTH_SCIM_MAX_PAGE`, `AUTH_SCIM_MAX_PATCH_OPS`, `AUTH_SCIM_MAX_BODY_BYTES` | `100`, `50`, `1048576` | cỡ trang mặc định và trần, số thao tác PATCH tối đa, cỡ body tối đa (echo còn giới hạn chung 1 MiB) |
| `AUTH_GOOGLE_JWKS_URL` | JWKS của Google | chỉ cho test; URL nội bộ cần `AUTH_ALLOW_PRIVATE_JWKS` |
| `AUTH_EXTERNAL_STATE_TTL` | `10m` | hạn của state và cookie liên kết |
| `AUTH_EXTERNAL_AUTH_PROVIDERS` | rỗng | JSON mảng `{issuer, identityProvider}` tra theo `issuer` của state (IdP cấu hình ở máy chủ) |
| `AUTH_SUPPORT_EMAIL` | rỗng | địa chỉ hỗ trợ trong thư chào (địa chỉ thuần); rỗng thì bỏ dòng này |
| `AUTH_MAIL_APP_NAME` | `Trivita` | tên trong thư |
| `AUTH_APP_BASE_URL` | `http://localhost:3000` khi `ENVIRONMENT=local`, ngoài đó rỗng | địa chỉ ứng dụng đăng nhập; `/oauth2/authorize` chuyển tới `<AUTH_APP_BASE_URL>/oauth`. **Rỗng thì không có `/oauth2/authorize`.** https bắt buộc ở staging/production |
| `AUTH_FHIR_BASE_URL` | rỗng | địa chỉ `fhir-server`, được chấp nhận làm `aud` ở `/oauth2/authorize` (SMART) |
| `AUTH_OAUTH_REGISTER_ENABLED` | `false` | bật `POST /oauth2/register` (tra client dựng sẵn theo redirect URI) |
| `AUTH_ALLOW_PRIVATE_JWKS` | `false` | cho `jwks_uri` của client là http hoặc trỏ vào dải nội bộ. **Chỉ dev/test**, cấm ở staging/production (tắt chống SSRF) |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) `true` -> từ chối khởi động và từ chối `migrate` nếu `search_path` của role không phải đúng MỘT schema riêng đã tồn tại (không phải `public`, không kèm schema khác). Bật khi chung một database với `fhir-server` (xem [docs/runbook.md mục 12](../../docs/runbook.md)) |
| `DATABASE_MIGRATE_URL` | rỗng | (chỉ PostgreSQL) URL của role `identity_migrate` (có DDL) dùng riêng cho lệnh `auth migrate`; app chạy bằng `DATABASE_URL` là role `identity_app` chỉ có DML (D-DB-4, xem [runbook mục 12.5](../../docs/runbook.md)). Đặt thì **bắt buộc** `DATABASE_AUTO_MIGRATE=false` |
| `AUTH_REDIS_URL` | rỗng | nơi nhớ `jti` của client assertion, topic FHIRcast **và trạng thái MFA** (secret được mời, hạn mức thử, mã TOTP đã dùng; khóa tiền tố `auth:mfa:`) dùng chung nhiều instance; rỗng thì nhớ trong bộ nhớ (chỉ đúng với một instance: chạy nhiều instance thì hạn mức MFA nhân lên theo số instance). **Bắt buộc ở staging/production** |
| `AUTH_FHIRCAST_HUB_URL` | rỗng (tắt) | địa chỉ Hub FHIRcast công khai (thường `https://<ws-gateway>/fhircast/STU3`) trả ở `hub.url`; có thì token có scope `fhircast/` kèm `hub.url` và `hub.topic` (ổn định theo chủ thể token, giữ ở `AUTH_REDIS_URL`). Không query/fragment; https ở staging/production, và staging/production **bắt buộc** `AUTH_REDIS_URL` (topic phải giống nhau giữa các instance) |
| `AUTH_AUTH_CODE_TTL` | `10m` | hạn của authorization code tính từ lúc phát |
| `AUTH_REFRESH_GRACE` | `30s` | cửa sổ chấp nhận lại refresh token vừa xoay, chỉ từ cùng IP; `0` là tắt |
| `AUTH_SESSION_MAX_AGE` | `0` | trần tuyệt đối của phiên refresh tính từ lúc đăng nhập (`0`: không giới hạn, phiên có thể sống vô hạn vì mỗi lần xoay cấp lại hạn đầy) |
| `AUTH_ALLOW_PLAIN_PKCE` | `false` | cho `code_challenge_method=plain` (mặc định chỉ S256) |
| `AUTH_MAX_POLICY_CLAIM_BYTES` | `16384` | trần claim `fhir_access_policy`; vượt thì từ chối cấp token |

Xoay khóa: `auth keys generate` (khóa mới vào cùng thư mục) → triển khai để JWKS có cả hai khóa → đợi quá TTL cache của người xác minh (`fhirauth`: 10 phút) → đặt `AUTH_ACTIVE_KID` là kid mới → khi mọi token cũ đã hết hạn thì xóa file khóa cũ. Token ký bằng khóa cũ vẫn xác minh được trong lúc đó.

Access token mang `fhir_project` (project của membership), `scope` và `fhir_access_policy`. **Scope do client khai không bao giờ trở thành quyền**: mọi term `fhir.*` bị từ chối (`invalid_scope`); `fhir.admin`, `fhir.write`, `fhir.read` do auth tự thêm theo membership (admin của project → `fhir.admin`; mọi rule chỉ đọc → `fhir.read`; còn lại `fhir.write`) và **không thêm** khi login có scope SMART. AccessPolicy được dịch sang claim theo nguyên tắc chỉ thu hẹp (loại có hiddenFields, interaction, compartment, `%...` bị cấm; readonlyFields và writeConstraint thành chỉ đọc; xem `domain.PolicyClaim`), chỗ thu hẹp được ghi log; compartment hoặc basedOn ở mức chính sách thì từ chối cấp token (`access_denied`).

**`authorization_code`.** SPA đăng nhập qua `POST /auth/login` (kèm `clientId`, `codeChallenge` S256, `redirectUri`) rồi đổi `code` ở `POST /oauth2/token` với `code_verifier` và `redirect_uri`. Kho chỉ lưu BĂM của code và cookie đăng nhập (đọc được Login cũng không dùng lại được code). Code hết hạn sau `AUTH_AUTH_CODE_TTL` kể từ lúc phát (không bị kéo dài bởi các lần ghi Login); mỗi lần dựng lại kết quả đăng nhập, kể cả poll `GET /auth/login/{id}`, phát code mới và vô hiệu code cũ. PKCE luôn được kiểm khi Login có challenge, kể cả client `pkceOptional`; Login không có challenge chỉ đổi được khi client xác thực bằng secret. Verifier sai hoặc secret sai không làm mất code. Code đổi lần hai thu hồi Login (`invalid_grant`/`Token already granted`); việc chiếm code là một lần ghi có điều kiện phiên bản nên hai yêu cầu song song chỉ một cái lấy được token.

**`refresh_token`.** Mỗi lần dùng, refresh secret của Login được xoay bằng một lần ghi có điều kiện phiên bản (hai yêu cầu song song không thể cùng xoay) và refresh token mới được cấp. Dùng lại một secret đã xoay, ngoài ân hạn hoặc từ IP khác, thu hồi Login (`invalid_grant`/`Token revoked`) và ghi log cảnh báo. Client bí mật (có secret, đăng nhập không PKCE) phải xác thực khi refresh; client công khai (PKCE) thì không. `ipAccessRule` của membership được kiểm. Thu hồi có hiệu lực ngay ở các route của auth, còn `fhir-server` chỉ biết khi access token hết hạn (`AUTH_ACCESS_TTL`).

**`userinfo`, `introspect`, `logout`.** `userinfo` trả `sub` và, theo từng TỪ trong scope của login, `profile` (tham chiếu profile, `locale`, `updated_at`, tên) và `email`; các trường lấy từ hồ sơ lâm sàng (điện thoại, địa chỉ, ngày sinh...) không có vì hồ sơ nằm ở `fhir-server`. `introspect` (RFC 7662) chỉ cho hỏi token của cùng project và cùng client với người gọi; mọi lý do token không dùng được đều ra `{"active": false}`, kể cả membership hoặc project bị tắt và refresh token đã xoay. `logout` thu hồi login của access token và xóa cookie đăng nhập; sau đó access token và refresh token của login đó bị từ chối ở auth (fhir-server chỉ biết khi access token hết hạn).

**`/oauth2/authorize`.** Như Medplum, `auth` không có giao diện đăng nhập: kiểm tra tham số rồi chuyển người dùng sang ứng dụng đăng nhập (`<AUTH_APP_BASE_URL>/oauth`) kèm tham số, ứng dụng đó gọi `/auth/login`, `/auth/profile`, `/auth/scope` rồi chuyển về `redirect_uri` kèm `code`. Đã có phiên (cookie `medplum-<client_id>` hoặc `id_token_hint` là id token của client này) thì tạo Login mới (`basedOn` Login cũ, không kế thừa PKCE); `prompt=none` trả code ngay, `prompt=login` bắt đăng nhập lại. Lỗi về `client_id` và `redirect_uri` hiển thị trực tiếp (400), các lỗi khác chuyển về `redirect_uri` kèm `error`, `state` và `iss`. `redirect_uri` phải khớp chính xác; client không có secret bắt buộc gửi `code_challenge` (S256). `logout` thu hồi cả chuỗi `basedOn` tổ tiên.

**`private_key_jwt`, `pre-authorized_code`, `register`.** Client xác thực bằng `client_assertion` (RFC 7523) ở `client_credentials`, `authorization_code` và `refresh_token`: JWKS tải từ `jwks_uri` của client qua `pkg/safehttp` (https, không dải nội bộ), assertion phải có `exp` (tối đa 5 phút) và `jti` dùng một lần; không gộp với Basic hay `client_secret`. `pre-authorized_code` (`grant_type=urn:ietf:params:oauth:grant-type:pre-authorized_code`) đổi một mã cấp sẵn, lưu băm, dùng một lần; việc tạo mã thuộc P6. `/oauth2/register` chỉ tra client dựng sẵn theo redirect URI và mặc định tắt. Chạy nhiều instance thì đặt `AUTH_REDIS_URL`, nếu không một assertion bị lộ phát lại được giữa các instance trong tối đa 5 phút.

**Tích hợp đã kiểm.** `make test-e2e` chạy `auth` và `fhir-server` thật (hai binary, SQLite tạm) và đi hết chuỗi đăng nhập, đổi code, gọi API FHIR; hợp đồng đầy đủ ở [docs/api-contract/oauth.md](../../docs/api-contract/oauth.md). `docker-compose.auth.yml` nối hai service trong compose (chưa chạy thử bằng Docker).
