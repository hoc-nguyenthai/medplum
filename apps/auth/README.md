# auth

Dịch vụ xác thực và cấp token, port từ Medplum (đăng nhập, MFA, OAuth2/OIDC). Kế hoạch: [docs/plans/auth/README.md](../../docs/plans/auth/README.md). Hợp đồng API của service: [docs/api-contract.md](docs/api-contract.md) (bảng route sinh từ swagger, có test chống lệch); diễn giải và ví dụ: [docs/api-contract/auth-dto.md](../../docs/api-contract/auth-dto.md).

**Trạng thái (2026-11-23):** đã có đăng nhập lõi, đăng ký và mật khẩu (P4), MFA (P5), OAuth2/OIDC đầy đủ (P3, gồm `private_key_jwt`, `pre-authorized_code`, `register`, tích hợp `fhir-server`), đăng nhập ngoài, Google, `exchange`, `preauthorize` (P6), lệnh quản trị `auth ...`, `/admin/projects/*`, SCIM 2.0 và `POST /email/v1/send` (ba route sau tắt mặc định). Kế hoạch theo pha ở [docs/plans/auth/README.md](../../docs/plans/auth/README.md) (dòng trạng thái ở đó còn cũ). Tài liệu của service: [docs/README.md](docs/README.md) (kiến trúc, cấu hình, kiểm thử).

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


> **Docker:** `apps/auth/Dockerfile` là Dockerfile riêng của service, build từ gốc repo: `make docker APP=auth` hoặc `docker build -f apps/auth/Dockerfile -t trivita-auth .`. e2e dựng chính image này: `make test-service APP=auth`.
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
cmd/auth              điểm vào: serve | migrate | healthcheck | seed | client | user | domain | project | purge-logins | keys
internal/domain       luật thuần (chỉ stdlib và pkg/fhir)
internal/application  use case và cổng (ports.go)
internal/infrastructure adapter: kho trên pkg/fhirstore, bcrypt, TOTP, mail...
internal/presentation/httpapi  Echo, DTO, validator, ánh xạ lỗi
internal/bootstrap    composition root
docs/                 architecture.md, config.md (sinh từ mã), testing.md; OpenAPI do swaggo sinh
```

Quy tắc phụ thuộc giữa các tầng được `architecture_test.go` (gốc repo) kiểm tra.

## Token và tích hợp với `fhir-server`

`auth` ký access token (RS256 mặc định, ES256/ES384 tùy chọn) mà `fhir-server` nhận qua JWKS. Cấu hình `fhir-server`: `FHIR_JWT_ISSUER` = `AUTH_ISSUER` (mặc định `AUTH_BASE_URL`, **đúng từng ký tự**), `FHIR_JWT_AUDIENCE` = `AUTH_AUDIENCE` (mặc định chính issuer), `FHIR_JWKS_URL` = `<AUTH_BASE_URL>/.well-known/jwks.json`, và `FHIR_JOB_SIGNING_KEY` (bắt buộc khi dùng JWKS).

Mọi biến (mặc định, ý nghĩa, ràng buộc ở staging/production) nằm ở [docs/config.md](docs/config.md), **sinh từ mã** và có test chống lỗi thời; không chép lại ở đây.

Xoay khóa: `auth keys generate` (khóa mới vào cùng thư mục) → triển khai để JWKS có cả hai khóa → đợi quá TTL cache của người xác minh (`fhirauth`: 10 phút) → đặt `AUTH_ACTIVE_KID` là kid mới → khi mọi token cũ đã hết hạn thì xóa file khóa cũ. Token ký bằng khóa cũ vẫn xác minh được trong lúc đó.

Access token mang `fhir_project` (project của membership), `scope` và `fhir_access_policy`. **Scope do client khai không bao giờ trở thành quyền**: mọi term `fhir.*` bị từ chối (`invalid_scope`); `fhir.admin`, `fhir.write`, `fhir.read` do auth tự thêm theo membership (admin của project → `fhir.admin`; mọi rule chỉ đọc → `fhir.read`; còn lại `fhir.write`) và **không thêm** khi login có scope SMART. AccessPolicy được dịch sang claim theo nguyên tắc chỉ thu hẹp (loại có hiddenFields, interaction, compartment, `%...` bị cấm; readonlyFields và writeConstraint thành chỉ đọc; xem `domain.PolicyClaim`), chỗ thu hẹp được ghi log; compartment hoặc basedOn ở mức chính sách thì từ chối cấp token (`access_denied`).

**`authorization_code`.** SPA đăng nhập qua `POST /auth/login` (kèm `clientId`, `codeChallenge` S256, `redirectUri`) rồi đổi `code` ở `POST /oauth2/token` với `code_verifier` và `redirect_uri`. Kho chỉ lưu BĂM của code và cookie đăng nhập (đọc được Login cũng không dùng lại được code). Code hết hạn sau `AUTH_AUTH_CODE_TTL` kể từ lúc phát (không bị kéo dài bởi các lần ghi Login); mỗi lần dựng lại kết quả đăng nhập, kể cả poll `GET /auth/login/{id}`, phát code mới và vô hiệu code cũ. PKCE luôn được kiểm khi Login có challenge, kể cả client `pkceOptional`; Login không có challenge chỉ đổi được khi client xác thực bằng secret. Verifier sai hoặc secret sai không làm mất code. Code đổi lần hai thu hồi Login (`invalid_grant`/`Token already granted`); việc chiếm code là một lần ghi có điều kiện phiên bản nên hai yêu cầu song song chỉ một cái lấy được token.

**`refresh_token`.** Mỗi lần dùng, refresh secret của Login được xoay bằng một lần ghi có điều kiện phiên bản (hai yêu cầu song song không thể cùng xoay) và refresh token mới được cấp. Dùng lại một secret đã xoay, ngoài ân hạn hoặc từ IP khác, thu hồi Login (`invalid_grant`/`Token revoked`) và ghi log cảnh báo. Client bí mật (có secret, đăng nhập không PKCE) phải xác thực khi refresh; client công khai (PKCE) thì không. `ipAccessRule` của membership được kiểm. Thu hồi có hiệu lực ngay ở các route của auth, còn `fhir-server` chỉ biết khi access token hết hạn (`AUTH_ACCESS_TTL`).

**`userinfo`, `introspect`, `logout`.** `userinfo` trả `sub` và, theo từng TỪ trong scope của login, `profile` (tham chiếu profile, `locale`, `updated_at`, tên) và `email`; các trường lấy từ hồ sơ lâm sàng (điện thoại, địa chỉ, ngày sinh...) không có vì hồ sơ nằm ở `fhir-server`. `introspect` (RFC 7662) chỉ cho hỏi token của cùng project và cùng client với người gọi; mọi lý do token không dùng được đều ra `{"active": false}`, kể cả membership hoặc project bị tắt và refresh token đã xoay. `logout` thu hồi login của access token và xóa cookie đăng nhập; sau đó access token và refresh token của login đó bị từ chối ở auth (fhir-server chỉ biết khi access token hết hạn).

**`/oauth2/authorize`.** Như Medplum, `auth` không có giao diện đăng nhập: kiểm tra tham số rồi chuyển người dùng sang ứng dụng đăng nhập (`<AUTH_APP_BASE_URL>/oauth`) kèm tham số, ứng dụng đó gọi `/auth/login`, `/auth/profile`, `/auth/scope` rồi chuyển về `redirect_uri` kèm `code`. Đã có phiên (cookie `medplum-<client_id>` hoặc `id_token_hint` là id token của client này) thì tạo Login mới (`basedOn` Login cũ, không kế thừa PKCE); `prompt=none` trả code ngay, `prompt=login` bắt đăng nhập lại. Lỗi về `client_id` và `redirect_uri` hiển thị trực tiếp (400), các lỗi khác chuyển về `redirect_uri` kèm `error`, `state` và `iss`. `redirect_uri` phải khớp chính xác; client không có secret bắt buộc gửi `code_challenge` (S256). `logout` thu hồi cả chuỗi `basedOn` tổ tiên.

**`private_key_jwt`, `pre-authorized_code`, `register`.** Client xác thực bằng `client_assertion` (RFC 7523) ở `client_credentials`, `authorization_code` và `refresh_token`: JWKS tải từ `jwks_uri` của client qua `pkg/safehttp` (https, không dải nội bộ), assertion phải có `exp` (tối đa 5 phút) và `jti` dùng một lần; không gộp với Basic hay `client_secret`. `pre-authorized_code` (`grant_type=urn:ietf:params:oauth:grant-type:pre-authorized_code`) đổi một mã cấp sẵn, lưu băm, dùng một lần; việc tạo mã thuộc P6. `/oauth2/register` chỉ tra client dựng sẵn theo redirect URI và mặc định tắt. Chạy nhiều instance thì đặt `AUTH_REDIS_URL`, nếu không một assertion bị lộ phát lại được giữa các instance trong tối đa 5 phút.

**Tích hợp đã kiểm.** `make test-e2e` chạy `auth` và `fhir-server` thật (hai binary, SQLite tạm) và đi hết chuỗi đăng nhập, đổi code, gọi API FHIR; hợp đồng đầy đủ ở [docs/api-contract/oauth.md](../../docs/api-contract/oauth.md). `docker-compose.auth.yml` nối hai service trong compose (chưa chạy thử bằng Docker).
