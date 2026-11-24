# auth: hợp đồng API theo từng trường hợp (nhóm `auth`)

Tệp này mô tả 28 route của nhóm tag `auth`: với mỗi route có **thứ tự kiểm tra** đúng như trong mã, **bảng trường hợp** (điều kiện, HTTP, header riêng, body, tác dụng phụ) và **ví dụ** yêu cầu/phản hồi cho từng dạng phản hồi khác cấu trúc. Người đọc: BA kỹ thuật, QA, dev FE. Bảng từng trường (kiểu, bắt buộc, ràng buộc) ở [../reference/auth.md](../reference/auth.md) và không lặp lại ở đây. Header chung, cookie, envelope OperationOutcome, mã HTTP và hạn mức IP ở [../common.md](../common.md) và chỉ được trỏ tới. Thông điệp lỗi trích **nguyên văn** từ mã và test của `apps/auth` (mã thắng tài liệu cũ `auth-dto.md`, vốn mô tả cả hợp đồng Medplum). Mục ghi `(chưa kiểm)` là chỗ chưa đối chiếu được bằng test.

## Sơ đồ chuyển giữa các route

```mermaid
flowchart TD
    M["POST /auth/method<br/>cách đăng nhập của email"] -->|"{} = mật khẩu"| L
    M -->|"{domain, authorizeUrl} + cookie auth-external-state"| IdP["Trình duyệt tới IdP ngoài"]
    ES["POST /auth/external/start<br/>client có IdP riêng"] -->|"{authorizeUrl} + cookie"| IdP
    IdP --> CB["GET /auth/external[/clientId]<br/>callback, 302"]
    CB -->|"302 redirectUri?login&code"| TOK["POST /oauth2/token"]
    CB -->|"302 app/signin?login=..."| ST
    G["POST /auth/google"] --> LR
    L["POST /auth/login"] --> LR{{"LoginResult<br/>5 dạng"}}
    LR -->|"1 mfaEnrollRequired"| LE["POST /auth/mfa/login-enroll"]
    LR -->|"2 mfaRequired"| V["POST /auth/mfa/verify"]
    LE -->|"email: mfaRequired"| V
    LE -->|"totp đúng: LoginResult"| LR
    V -->|"LoginResult mới"| LR
    SE["POST /auth/mfa/send-email"] -.->|"gửi lại mã"| V
    LR -->|"3 chỉ có login (project new)"| NP["POST /auth/newproject"]
    LR -->|"5 memberships"| P["POST /auth/profile"]
    NP --> LR
    P --> LR
    LR -->|"4 code"| SC["POST /auth/scope (tùy chọn, thu hẹp)"]
    SC --> LR
    LR -->|"4 code"| TOK
    ST["GET /auth/login/login<br/>lấy lại trạng thái"] --> LR
    NU["POST /auth/newuser<br/>{login, emailVerificationRequired}"] -->|"projectId = new"| NP
    NU -->|"projectId = project mở"| NPT["POST /auth/newpatient"]
    NPT --> LR
    TOK --> ME["GET /auth/me, /auth/revoke, /auth/mfa/*, /auth/changepassword (Bearer)"]
    RP["POST /auth/resetpassword"] -.->|"thư chứa id và secret"| SP["POST /auth/setpassword"]
    VE["POST /auth/verifyemail"]
    PA["POST /auth/preauthorize (Bearer, admin)"] -->|"preAuthorizedCode"| TOK
    EX["POST /auth/exchange"] -->|"body token, không phải LoginResult"| ME
```

## LoginResult: dạng nào xuất hiện khi nào

Trả bởi `POST /auth/login`, `/auth/profile`, `/auth/scope`, `/auth/newproject`, `/auth/newpatient`, `/auth/google`, `/auth/mfa/verify`, `/auth/mfa/login-enroll` và `GET /auth/login/{login}`. Mã quyết định ở `domain.DecideLoginResult` rồi `application.BuildLoginResult`; **dạng đầu tiên khớp được trả**. Các mục route bên dưới chỉ trỏ lại bảng này (`LoginResult.<dạng>`).

| Ưu tiên | Dạng | Điều kiện (theo mã) | Body | Tác dụng phụ riêng của dạng |
|---|---|---|---|---|
| 1 | `mfaEnrollRequired` | Login có `authMethod=password`, chưa `mfaVerified`, MFA bắt buộc (setting `mfaRequired` của project của login, hoặc `User.mfaRequired`), user chưa đăng ký MFA | `{login, mfaEnrollRequired:true, enrollUri, enrollQrCode, allowedMfaMethods}` | Sinh (hoặc lấy lại) secret TOTP được mời cho cặp (user, login), giữ 20 phút trong kho trạng thái MFA; **chưa** ghi vào User |
| 2 | `mfaRequired` | Login mật khẩu, chưa `mfaVerified`, user đã đăng ký MFA (kể cả khi MFA không bắt buộc) | `{login, mfaRequired:true, mfaMethods:[...], email}` | Nếu email là phương thức duy nhất và login chưa có mã (`emailMfa` rỗng): **tự gửi mã email 6 số**, ghi băm mã lên Login, tăng bộ đếm gửi mã (5/login/20 phút). Lỗi trong bước tự gửi (kể cả vượt hạn mức) thành 500 chung, không phải 429 |
| 3 | project new | `login.project = Project/new` | `{login}` | Không |
| 4 | `code` | Login đã có membership | `{login, code}` | Ghi lại **băm** `code` và `cookie` mới lên Login, hạn code mặc định 10 phút (`AUTH_AUTH_CODE_TTL`); **mỗi lần BuildLoginResult chạy là một code mới, code cũ mất hiệu lực** (kể cả `GET /auth/login/{login}`). Kèm `Set-Cookie: medplum-<clientId>` khi login thuộc một client |
| 5 | `memberships` | Còn lại: chưa có membership, chưa chọn được profile | `{login, memberships:[{id, project, profile, identifier?}]}` | Không. `project` lấy tên mới nhất từ kho; không đọc được thì bỏ trường `project` |

Ghi chú đọc bảng:

- Luật MFA (dạng 1, 2) chỉ áp cho login `authMethod=password`. Login do `google`, `external`, `exchange` không bao giờ nhận dạng 1 hoặc 2 từ bảng này.
- MFA bắt buộc theo **project** chỉ được xét khi login đã có project. Người dùng nhiều project: `POST /auth/login` ra dạng 5 (chưa biết project), `POST /auth/profile` mới có thể ra dạng 1 hoặc 2.
- Nếu `memberships` rỗng và không phải project new thì body chỉ có `{login}` (do `omitempty`), trông giống dạng 3. Gặp ở login tạo bởi `/auth/newuser` mà người dùng gọi `GET /auth/login/{login}`; bước kế tiếp vẫn là `/auth/newproject` hoặc `/auth/newpatient`.
- Dạng 1 và 2 không bao giờ kèm `code`. Thiếu phụ thuộc MFA (kho trạng thái, QR, TOTP) thì **fail đóng**: HTTP 500 `Internal server error`, không có đường nào trả `code`.
- Cookie `medplum-<clientId>` chỉ đặt khi phản hồi có `code` **và** login thuộc client. Chi tiết thuộc tính ở [common.md mục 2.1](../common.md#21-cookie).

## Hàng nền áp dụng cho mọi route có body JSON

Các hàng này không lặp lại ở từng mục (mục chỉ nêu trong "Thứ tự kiểm tra"):

| # | Trường hợp | HTTP | Body |
|---|---|---|---|
| N1 | Vượt hạn mức IP chung (`AUTH_RATE_LIMIT`), hoặc hạn mức riêng của bước nhập thông tin đăng nhập (`AUTH_LOGIN_RATE_LIMIT`) ở route có đánh dấu "hạn mức đăng nhập" | 429, `Retry-After: 1` | OperationOutcome `throttled`, `details.text` `Too many requests` |
| N2 | Không xác định được IP người gọi | 403 | `cannot identify caller` |
| N3 | JSON hỏng, không phải object (`""`, `[]`, `null`, chuỗi) | 400 | `Content could not be parsed` (không `expression`) |
| N4 | Body quá lớn | 413 | `Request body too large` |
| N5 | Route Bearer: thiếu header, sai kiểu, hoặc token sai/hết hạn/login bị thu hồi | 401, `WWW-Authenticate: Bearer realm="auth"` (thiếu) hoặc `Bearer realm="auth", error="invalid_token"` (sai) | OperationOutcome `login`, `details.text` `Unauthorized` |
| N6 | Lỗi hạ tầng không lường trước (kho dữ liệu, SMTP...) | 500 | OperationOutcome `exception`, `Internal server error`; chi tiết chỉ ở log theo `X-Request-Id` |

Route dùng **hạn mức đăng nhập** (`AUTH_LOGIN_RATE_LIMIT`, mặc định staging/production 1 rps burst 10 mỗi IP): `login`, `profile`, `scope`, `method`, `newuser`, `newproject`, `exchange`, `google`, `external/start`, `GET external`, `GET external/{clientId}`, `mfa/verify`, `mfa/login-enroll`, `mfa/send-email`. **Không** có: `newpatient`, `resetpassword`, `setpassword`, `verifyemail`, `clientinfo`, `GET login/{login}` và các route Bearer (chỉ hạn mức chung).

---

<a id="post-auth-changepassword"></a>
## `POST /auth/changepassword`

Người đang đăng nhập đổi mật khẩu của chính mình bằng mật khẩu cũ. [Tham chiếu trường](../reference/auth.md#post-auth-changepassword)

**Tiền điều kiện:** Bearer của login do `User` (không phải ClientApplication) và không bị giới hạn scope SMART.

**Thứ tự kiểm tra:**
1. Bearer (N5), hạn mức chung (N1).
2. Đọc JSON (N3).
3. Validator, gom nhiều issue: `oldPassword` rỗng hoặc sai kiểu; `newPassword` sai độ dài.
4. Chủ thể phải là `User` và scope không phải SMART resource scope, nếu không `403 Forbidden`.
5. Đọc User (404 nếu mất). User toàn cục (không thuộc project) mà login không phải `password`/`google`: 403.
6. User chưa có mật khẩu: 400.
7. So mật khẩu cũ: sai thì 400. **Không có bộ đếm thử riêng** (chỉ hạn mức IP chung).
8. Kiểm mật khẩu rò rỉ (HIBP): trùng thì 400; dịch vụ lỗi thì **fail đóng** 500. Kiểm 72 byte.
9. **Transaction:** ghi băm mật khẩu mới (không tiêu thụ request nào vì `sr = nil`). Sau transaction: thu hồi **mọi** login của user (kể cả phiên đang dùng).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - | OperationOutcome `All OK` (`id: ok`) | Đổi băm mật khẩu; thu hồi mọi login của user; phiên hiện tại hết hiệu lực ngay |
| C2 | Thiếu mật khẩu cũ | `oldPassword` rỗng/thiếu/không phải chuỗi | 400 | - | `Missing oldPassword`, `expression` `oldPassword` | Không |
| C3 | Mật khẩu mới ngắn | dưới 8 ký tự (đếm ký tự) | 400 | - | `Password must be at least 8 characters`, `expression` `newPassword` | Không |
| C4 | Mật khẩu mới dài | trên 72 byte | 400 | - | `Password must be no more than 72 characters`, `expression` `newPassword` | Không |
| C5 | Chủ thể không được tự quản lý | Bearer của ClientApplication, hoặc login scope SMART, hoặc user toàn cục đăng nhập bằng external/exchange | 403 | - | `Forbidden` | Không |
| C6 | Chưa có mật khẩu | User không có băm mật khẩu (đăng nhập Google/IdP) | 400 | - | `Existing password not set`, `expression` `oldPassword` | Không |
| C7 | Mật khẩu cũ sai | So khớp thất bại | 400 | - | `Incorrect password`, `expression` `oldPassword` | Không (không tăng bộ đếm) |
| C8 | Mật khẩu mới bị rò rỉ | HIBP đếm > 0 | 400 | - | `Password found in breach database` (**không** có `expression`) | Không |
| C9 | Dịch vụ HIBP lỗi | Fail đóng | 500 | - | `Internal server error` | Không đổi mật khẩu |
| C10 | Bearer/hạn mức/JSON | N1, N3, N4, N5 | 429/400/413/401 | như hàng nền | như hàng nền | Không |

**Ví dụ**

```http
POST /auth/changepassword HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
Content-Type: application/json

{ "oldPassword": "mat-khau-cu-123", "newPassword": "mat-khau-moi-456" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "issue": [ { "severity": "error", "code": "invalid", "details": { "text": "Incorrect password" }, "diagnostics": "Incorrect password", "expression": ["oldPassword"] } ] }
```

**Lưu ý tích hợp:** sau 200 mọi access token và refresh token của user đã chết, FE phải đưa người dùng về đăng nhập lại. Thông điệp `Incorrect password` không bị bộ đếm MFA chặn, chỉ hạn mức IP.

---

<a id="get-auth-clientinfo-clientid"></a>
## `GET /auth/clientinfo/{clientId}`

Lấy phần cấu hình công khai của form đăng nhập (`signInForm`) của một client để FE dựng giao diện trước khi đăng nhập. [Tham chiếu trường](../reference/auth.md#get-auth-clientinfo-clientid)

**Tiền điều kiện:** không (route luôn bật). Không cần xác thực. Hạn mức chung (không thuộc hạn mức đăng nhập).

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1).
2. Tìm client: client dựng sẵn (ví dụ `medplum-cli`) trước, rồi đọc `ClientApplication` theo id; không có thì 404.
3. Nếu query có `redirect_uri` (kể cả chuỗi rỗng) thì phải **khớp chính xác** một URI đã đăng ký, sai thì 400.
4. Trả `signInForm` của client, hoặc `{}` nếu client không có.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Client có `signInForm` | 200 | - | nội dung `signInForm` (không có secret) | Không |
| C2 | Thành công, trống | Client không có `signInForm` | 200 | - | `{}` | Không |
| C3 | Client không tồn tại | `clientId` không có | 404 | - | `Not found`, code `not-found` | Không |
| C4 | `redirect_uri` lạ | Có tham số nhưng không khớp (kể cả `?redirect_uri=`) | 400 | - | `Invalid redirect URI` | Không |
| C5 | Hạn mức | N1 | 429 | `Retry-After: 1` | như N1 | Không |

**Ví dụ**

```http
GET /auth/clientinfo/a3b1c2d4-0000-4000-8000-000000000001?redirect_uri=https%3A%2F%2Fapp.example.org%2Fcallback HTTP/1.1
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "welcomeString": "Chào mừng", "logo": { "reference": "Binary/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77" } }
```

(Tên trường trong `signInForm` theo schema `fhir.ClientApplicationSignInForm`, xem reference.)

**Lưu ý tích hợp:** URL `Binary` được giữ nguyên tham chiếu, auth không ký lại. `redirect_uri` rỗng vẫn tính là "có gửi" nên trả 400; FE không nên gửi tham số này khi chưa có giá trị.

---

<a id="post-auth-exchange"></a>
## `POST /auth/exchange`

(Deprecated) Đổi access token của IdP ngoài lấy token của auth; vỏ mỏng của grant `urn:ietf:params:oauth:grant-type:token-exchange`. Trả **body token** (không phải LoginResult). [Tham chiếu trường](../reference/auth.md#post-auth-exchange)

**Tiền điều kiện:** route luôn bật. Cần IdP cấu hình ở máy chủ (`AUTH_EXTERNAL_AUTH_PROVIDERS`) hoặc client có `identityProvider` riêng. Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator gom: `externalAccessToken` rỗng, `clientId` rỗng (OperationOutcome 400).
3. `ExchangeToken`: `clientId` rỗng, subject token rỗng, kiểu token khác `urn:ietf:params:oauth:token-type:access_token` (route này luôn đặt đúng kiểu) → lỗi OAuth.
4. Tìm IdP: theo `clientId` trong danh sách IdP máy chủ, không có thì đọc `ClientApplication` (phải có `identityProvider`, `status` rỗng hoặc `active`); không đọc được 404 hoặc không hợp lệ thành `Invalid client`.
5. IdP máy chủ: `membershipId` bắt buộc, phải đọc được membership có project.
6. Thiếu `userInfoUrl` của IdP; kiểm `aud`/`azp` nếu subject token là JWT.
7. Gọi userinfo của IdP (ra ngoài); xác định email hoặc `sub`; email IdP báo chưa xác minh bị từ chối.
8. `TryLogin` kiểu `exchange` (kiểm membership **trước** khi tạo Login: lỗi thì không để lại Login). Lỗi ở bước này trả dạng **OperationOutcome**.
9. **Ghi:** tạo Login, chọn membership đầu tiên khớp, đặt `emailVerified=true` (idempotent), rồi cấp token (đánh dấu Login `granted`).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | `Cache-Control: no-store`, `Pragma: no-cache` | body token (`access_token`, `id_token`, `token_type`, `expires_in`, `refresh_token` nếu có...) | Tạo Login `authMethod=exchange`, `granted`; `emailVerified=true`; phát token |
| C2 | Thiếu token/client | Một hoặc cả hai trường rỗng | 400 | - | OperationOutcome, mỗi trường một issue: `Missing externalAccessToken` (`externalAccessToken`), `Missing clientId` (`clientId`) | Không |
| C3 | Client không hợp lệ | Không phải IdP máy chủ và client không có/không hoạt động/không tồn tại | 400 | `Cache-Control: no-store`, `Pragma: no-cache` | `{"error":"invalid_request","error_description":"Invalid client"}` | Không |
| C4 | Subject token sai | `aud`/`azp` của JWT không thuộc ứng dụng này | 400 | như C3 | OAuth `invalid_request` `Invalid subject_token` | Không |
| C5 | Thiếu membership | IdP máy chủ mà không có `membershipId` | 400 | như C3 | OAuth `invalid_request` `Missing membership_id` | Không |
| C6 | Membership sai | `membershipId` không đọc được hoặc thiếu project | 400 | như C3 | OAuth `invalid_request` `Invalid membership` | Không |
| C7 | Thiếu userinfo URL | IdP không có `userInfoUrl` | 400 | như C3 | OAuth `invalid_request` `Missing user info URL` | Không |
| C8 | IdP userinfo lỗi | Gọi IdP hỏng hoặc phản hồi không xác minh được | 400 | như C3 | OAuth `invalid_request`, `error_description` một trong: `Failed to verify code - check your identity provider configuration`, `Failed to verify code - unsigned user info response`, `Failed to verify code - unsupported content type`, `Unsupported user info mode` | Không |
| C9 | IdP giới hạn tốc độ | IdP trả 429 | 429 | như C3 | `{"error":"invalid_request","error_description":"Too many requests"}` | Không |
| C10 | Danh tính IdP thiếu | Token không có email/sub hoặc cấu hình ánh xạ không hỗ trợ | 400 | như C3 | OAuth `invalid_request`: `External token does not contain email address`, `External token does not contain subject`, `Unsupported identity provider configuration` | Không |
| C11 | Email IdP chưa xác minh | `email_verified` là false | 400 | như C3 | OAuth `invalid_request` `Email address is not verified by the identity provider` | Không |
| C12 | Lỗi đăng nhập | `TryLogin` thất bại (không có user, không có membership khớp...) | 400 | - | **OperationOutcome** `invalid`, ví dụ `User not found` | Không tạo Login (kiểm membership trước khi tạo) |
| C13 | Lỗi cấp token | Membership/project không hoạt động, launch, chính sách... | 400 | như C3 | OAuth: ví dụ `invalid_request` `Invalid profile`; `access_denied` kèm `Profile not active` hoặc `Project not active`; còn lại theo `/oauth2/token` (chưa kiểm từng thông điệp, xem oauth.md) | Login đã tạo có thể ở trạng thái chưa cấp token |
| C14 | Lỗi nội bộ | Ghi `emailVerified` lỗi, hoặc mọi lỗi OAuth status >= 500 | 500 | như C3 | `{"error":"server_error","error_description":"Internal server error"}` | Không lộ chi tiết |
| C15 | Hạn mức/JSON | N1, N3, N4 | 429/400/413 | như N | như N | Không |

**Ví dụ**

```http
POST /auth/exchange HTTP/1.1
Content-Type: application/json

{ "externalAccessToken": "ya29.a0AfH6SMB...", "clientId": "srv-cid", "membershipId": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Cache-Control: no-store
Pragma: no-cache

{ "token_type": "Bearer", "access_token": "eyJhbGciOiJSUzI1NiIs...", "id_token": "eyJhbGciOiJSUzI1NiIs...", "expires_in": 3600 }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json
Cache-Control: no-store
Pragma: no-cache

{ "error": "invalid_request", "error_description": "Invalid client" }
```

**Lưu ý tích hợp:** có **hai dạng lỗi** trên cùng route: validator và lỗi đăng nhập (`TryLogin`) là OperationOutcome, còn lỗi yêu cầu/IdP/danh tính là `{error, error_description}`. Hãy rẽ nhánh theo `Content-Type` hoặc theo sự có mặt của `resourceType`. Dùng grant token-exchange ở `/oauth2/token` cho tích hợp mới.

---

<a id="get-auth-external"></a>
## `GET /auth/external`

Callback OIDC chung: IdP chuyển trình duyệt về đây kèm `code` và `state`; auth đổi code lấy id_token, tìm user, tạo Login rồi **chuyển hướng 302**. [Tham chiếu trường](../reference/auth.md#get-auth-external)

**Tiền điều kiện:** `AUTH_APP_BASE_URL` đặt (kèm khóa ký state `AUTH_EXTERNAL_STATE_KEY`, bắt buộc ở staging/production). Không có thì route **không đăng ký**: 404. Hạn mức đăng nhập (mỗi lần gọi làm auth gọi ra IdP). `state` do `POST /auth/method` hoặc `/auth/google` phát và phải đi cùng cookie `auth-external-state` của **chính trình duyệt**.

**Thứ tự kiểm tra** (cùng mã cho `GET /auth/external/{clientId}`, khác ở bước 5 và 8):
1. Hạn mức đăng nhập (N1).
2. Đọc cookie `auth-external-state` rồi **luôn xóa** nó (`Set-Cookie` hết hạn) ở mọi phản hồi, thành công hay lỗi.
3. `code` rỗng: 400 `Missing code`. `state` rỗng: 400 `Missing state`.
4. Mở state: chữ ký HMAC, hạn (mặc định 10 phút, `AUTH_EXTERNAL_STATE_TTL`), khớp cookie; mọi lý do (kể cả cookie vắng) là `Invalid state`.
5. (Chỉ callback riêng theo client) `clientId` trên đường dẫn phải bằng `clientId` trong state: `Invalid client`.
6. Đọc `DomainConfiguration` theo domain trong state (callback chung), đọc client theo state (404 nếu mất).
7. Chọn IdP: client có IdP riêng mà project không bật `legacyExternalAuth` (systemSetting) thì lỗi `must use the per-client callback`; rồi theo domain; rồi theo `iss` trong state cho IdP máy chủ. Không có: `Identity provider not found`.
8. `iss` trên query (RFC 9207) khác issuer của IdP: `Invalid issuer`.
9. Đổi code lấy id_token (ra ngoài), xác minh JWKS/iss/aud/hạn.
10. Xác định email hoặc `sub`; email IdP chưa xác minh bị từ chối; email phải thuộc domain trong state.
11. Project trong state phải khớp project của client (`Invalid project`); redirect URI trong state phải khớp (`Invalid redirect URI`), **trước khi tạo gì**.
12. `TryLogin` kiểu `external`: kiểm membership **trước** khi tạo Login; không có membership: `User not found` và **không tạo Login**.
13. **Ghi:** tạo Login; đặt `emailVerified=true` (idempotent).
14. Nếu login đã có membership và state có `redirectUri`: dựng LoginResult (dạng code, ghi code/cookie băm) rồi 302 tới `redirectUri` kèm `login` và `code`. Ngược lại 302 về trang đăng nhập của app.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, có code | Login có đúng một membership và state có `redirectUri` | 302 | `Location: <redirectUri>?code=<code>&login=<uuid>`; `Set-Cookie: auth-external-state=; Max-Age=0` | Rỗng | Tạo Login `authMethod=external`, chọn membership, `emailVerified=true`, phát code (băm). **Không** đặt cookie `medplum-<clientId>` |
| C2 | Thành công, về trang app | Chưa có membership đơn, hoặc state không có `redirectUri` | 302 | `Location: <app>/signin?login=<uuid>&scope=..&nonce=..&code_challenge=..&code_challenge_method=..` (hoặc `<app>/oauth?...` nếu login có `launch`, hoặc `returnTo` hợp lệ của domain); cookie bị xóa | Rỗng | Tạo Login, `emailVerified=true`; app gọi tiếp `GET /auth/login/{login}` hoặc `/auth/profile` |
| C3 | Thiếu tham số | `code` rỗng / `state` rỗng | 400 | cookie bị xóa | `Missing code` / `Missing state` | Không |
| C4 | State không hợp lệ | Sai chữ ký, hết hạn, cookie vắng/khác, dài > 8 KiB, đăng nhập ngoài chưa bật | 400 | cookie bị xóa | `Invalid state` | Không gọi IdP |
| C5 | Client lạ trong state | Client không còn | 404 | cookie bị xóa | `Not found` | Không |
| C6 | IdP riêng dùng sai callback | Client có IdP riêng, project không bật `legacyExternalAuth` | 400 | cookie bị xóa | `Client identity provider must use the per-client callback <BaseURL>/auth/external/<clientId>` | Không |
| C7 | Không có IdP | Không chọn được IdP | 400 | cookie bị xóa | `Identity provider not found` | Không |
| C8 | Sai issuer | `iss` khác issuer cấu hình | 400 | cookie bị xóa | `Invalid issuer` | Không gọi IdP |
| C9 | Cấu hình IdP thiếu | IdP không có `jwksUrl` (ngoài dev/test) / thiếu token URL / thiếu client id-secret / thiếu issuer | 400 | cookie bị xóa | `Missing JWKS URL for external identity provider`; `Missing token URL for external identity provider`; `Missing client ID or client secret for external identity provider`; `Missing issuer for external identity provider` | Không |
| C10 | IdP trả sai | Đổi code/xác minh id_token thất bại; thiếu id_token | 400 | cookie bị xóa | `Failed to verify code - check your identity provider configuration`; `Missing id_token in external identity provider response` | Không |
| C11 | IdP giới hạn tốc độ | IdP trả 429 | 429 | `Retry-After: 0`; cookie bị xóa | `Too many requests` + extension `rate-limit-reset` = 0 | Không |
| C12 | Danh tính thiếu | `External token does not contain email address` / `External token does not contain subject` / `Unsupported identity provider configuration` | 400 | cookie bị xóa | như cột điều kiện | Không |
| C13 | Email chưa xác minh | IdP báo `email_verified=false` | 400 | cookie bị xóa | `Email address is not verified by the identity provider` | Không tạo Login |
| C14 | Email lệch domain | Email không thuộc domain của state | 400 | cookie bị xóa | `Email address does not match domain` | Không |
| C15 | Project lệch | Project trong state khác project của client | 400 | cookie bị xóa | `Invalid project` | Không |
| C16 | Redirect URI sai | State chứa `redirectUri` không khớp client (khớp một phần chỉ khi project bật `allow-dangerous-redirect`) | 400 | cookie bị xóa | `Invalid redirect URI` | Không tạo Login |
| C17 | Không có user/membership | `TryLogin` không tìm thấy | 400 | cookie bị xóa | `User not found` | **Không** tạo Login |
| C18 | Lỗi đăng nhập khác | Login kiểu external bị chặn: `Invalid origin`, PKCE, `IP address not allowed`, `Project not active`, `Profile not active`... (thông điệp theo SetLoginMembership/TryLogin) | 400 | cookie bị xóa | tương ứng | Có thể đã tạo Login (nếu lỗi ở bước chọn membership) |
| C19 | Thiếu cấu hình app | `AUTH_APP_BASE_URL` rỗng khi cần về trang app | 500 | cookie bị xóa | `Internal server error` | Login đã tạo |
| C20 | Route tắt | Không có `AUTH_APP_BASE_URL` | 404 | - | `Not Found` | Không |
| C21 | Hạn mức đăng nhập | N1 | 429 | `Retry-After: 1` | như N1 | Không |

**Ví dụ**

```http
GET /auth/external?code=4%2F0AX4XfWh...&state=eyJzIjp7...fQ.a1b2c3 HTTP/1.1
Cookie: auth-external-state=3f9c2e7a1b3d5f60a8c1e2d3b4a59687
```

```http
HTTP/1.1 302 Found
Location: https://app.example.org/callback?code=4f9c2e7a1b3d5f60a8c1e2d3b4a59687&login=0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66
Set-Cookie: auth-external-state=; Path=/auth/external; Max-Age=0; HttpOnly; SameSite=Lax
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8
Set-Cookie: auth-external-state=; Path=/auth/external; Max-Age=0; HttpOnly; SameSite=Lax

{ "resourceType": "OperationOutcome", "issue": [ { "severity": "error", "code": "invalid", "details": { "text": "Invalid state" }, "diagnostics": "Invalid state" } ] }
```

**Lưu ý tích hợp:** lỗi trả JSON 400, **không** chuyển hướng về app, nên người dùng cuối thấy JSON thô; FE nên bắt luồng này bằng cách kiểm tra từ bước `POST /auth/method`. Callback là một lần: cookie bị xóa kể cả khi lỗi, phát lại là `Invalid state`. Không đặt `medplum-<clientId>`: cookie SSO chỉ đặt sau khi app gọi `GET /auth/login/{login}` (hoặc `/auth/profile`) và có `code`. `code` hết hạn sau 10 phút mặc định.

---

<a id="post-auth-external-start"></a>
## `POST /auth/external/start`

Bắt đầu đăng nhập qua IdP **riêng** của một client (callback `/auth/external/{clientId}`), không đi theo tên miền email. [Tham chiếu trường](../reference/auth.md#post-auth-external-start)

**Tiền điều kiện:** `AUTH_APP_BASE_URL` đặt (route không đăng ký nếu không có: 404). Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. `clientId` rỗng: 400 `Missing clientId`.
3. Đọc client (dựng sẵn hoặc kho; 404 nếu không có).
4. Client không có `identityProvider` đủ `authorizeUrl` và `clientId`: 400.
5. Nếu có `redirectUri`: phải khớp chính xác URI đã đăng ký của client.
6. **Ghi (kho trạng thái, không phải DB):** ký `state` (HMAC, TTL mặc định 10 phút) kèm nonce cookie.
7. Dựng `authorizeUrl` (400 nếu `authorizeUrl` của IdP thiếu scheme/host). Đặt cookie `auth-external-state`.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | `Set-Cookie: auth-external-state=<nonce>; Path=/auth/external; HttpOnly; SameSite=Lax; Max-Age=<TTL>` (thêm `Secure` theo cấu hình) | `{ "authorizeUrl": "..." }` (không có `domain`) | Phát state ký; chưa ghi DB |
| C2 | Thiếu clientId | rỗng/thiếu | 400 | - | `Missing clientId`, `expression` `clientId` | Không |
| C3 | Client không tồn tại | - | 404 | - | `Not found` | Không |
| C4 | Client không có IdP | Không có `identityProvider` hoặc thiếu `authorizeUrl`/`clientId` | 400 | - | `Client does not have an identity provider` | Không |
| C5 | Redirect lạ | `redirectUri` không khớp | 400 | - | `Invalid redirect URI` | Không |
| C6 | authorizeUrl hỏng | `authorizeUrl` của IdP không có scheme/host | 400 | - | `Failed to construct URL for the domain` | Không |
| C7 | Route tắt | Không có `AUTH_APP_BASE_URL` | 404 (hoặc 405) | - | `Not Found` | Không |
| C8 | Hạn mức/JSON | N1, N3, N4 | 429/400/413 | như N | như N | Không |

**Ví dụ**

```http
POST /auth/external/start HTTP/1.1
Content-Type: application/json

{ "clientId": "a3b1c2d4-0000-4000-8000-000000000000", "redirectUri": "https://app.example.org/callback", "scope": "openid", "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", "codeChallengeMethod": "S256" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: auth-external-state=3f9c2e7a1b3d5f60a8c1e2d3b4a59687; Path=/auth/external; Max-Age=600; HttpOnly; SameSite=Lax

{ "authorizeUrl": "https://idp.example.org/authorize?client_id=idp-client&redirect_uri=https%3A%2F%2Fauth.example.org%2Fauth%2Fexternal%2Fa3b1c2d4-0000-4000-8000-000000000000&response_type=code&scope=openid+profile+email&state=eyJzIjp7...fQ.a1b2c3" }
```

**Lưu ý tích hợp:** FE chỉ chuyển trình duyệt tới `authorizeUrl` nguyên vẹn, **không tự thêm `state`**. Cookie liên kết trình duyệt phải còn khi IdP quay về (cùng trình duyệt, `SameSite=Lax`). Callback của client này là `GET /auth/external/{clientId}`.

---

<a id="get-auth-external-clientid"></a>
## `GET /auth/external/{clientId}`

Callback OIDC **riêng của client** có `identityProvider`: cùng xử lý với [`GET /auth/external`](#get-auth-external) nhưng IdP lấy từ chính client. [Tham chiếu trường](../reference/auth.md#get-auth-external-clientid)

**Tiền điều kiện:** như `GET /auth/external` (`AUTH_APP_BASE_URL`). Hạn mức đăng nhập. `state` phải do `POST /auth/external/start` phát cho đúng `clientId` này.

**Thứ tự kiểm tra:** giống `GET /auth/external`, khác: (a) sau bước mở state, `clientId` trên đường dẫn phải bằng `clientId` trong state, nếu không `400 Invalid client`; (b) chọn IdP **chỉ** từ `client.identityProvider` (không thử domain/issuer; client không có IdP thì `Identity provider not found`); (c) redirect_uri gửi cho IdP là `<BaseURL>/auth/external/<clientId>`; (d) state không có `domain` nên không kiểm `Email address does not match domain`; (e) danh tính có thể ánh xạ theo `sub` (`useSubject`).

Bảng trường hợp: dùng lại C1 đến C21 của `GET /auth/external` (cùng HTTP, header, body), cộng các hàng riêng:

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | như `GET /auth/external` C1/C2 | 302 | `Location` như đó; cookie `auth-external-state` bị xóa | Rỗng | Tạo Login `external`, `emailVerified=true` |
| C2 | Client trong path khác state | `clientId` path khác `clientId` của state | 400 | cookie bị xóa | `Invalid client` | Không |
| C3 | Chưa ánh xạ `sub` | Subject chưa gắn `externalId` vào membership | 400 | cookie bị xóa | `User not found` | **Không** tạo Login |
| C4 | Client không có IdP | Client đã đổi cấu hình | 400 | cookie bị xóa | `Identity provider not found` | Không |
| C5 | Mọi lỗi còn lại | như `GET /auth/external` C3, C4, C8 đến C16, C19, C21 | 400/429/500 | cookie bị xóa | như đó | như đó |

**Ví dụ**

```http
GET /auth/external/a3b1c2d4-0000-4000-8000-000000000000?code=4%2F0AX4XfWh...&state=eyJzIjp7...fQ.a1b2c3 HTTP/1.1
Cookie: auth-external-state=3f9c2e7a1b3d5f60a8c1e2d3b4a59687
```

```http
HTTP/1.1 302 Found
Location: https://app.example.org/signin?login=0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66&scope=openid&nonce=c1a9f0e2
Set-Cookie: auth-external-state=; Path=/auth/external; Max-Age=0; HttpOnly; SameSite=Lax
```

**Lưu ý tích hợp:** nếu dùng nhầm callback chung `/auth/external` với state của client có IdP riêng thì nhận `Client identity provider must use the per-client callback ...`. Cấu hình `redirect_uri` ở IdP phải là địa chỉ callback riêng này.

---

<a id="post-auth-google"></a>
## `POST /auth/google`

Đăng nhập bằng Google Identity Services (credential JWT RS256); có thể tạo user mới nếu `createUser=true`. Trả LoginResult, hoặc `{domain, authorizeUrl}` nếu tên miền email dùng IdP ngoài. [Tham chiếu trường](../reference/auth.md#post-auth-google)

**Tiền điều kiện:** `AUTH_GOOGLE_ENABLED=true` hoặc `AUTH_GOOGLE_CLIENT_ID` đặt. Không có thì route **không đăng ký** (404/405). Hạn mức đăng nhập. Cho `{domain, authorizeUrl}` cần thêm `AUTH_APP_BASE_URL`.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator gom: `googleClientId` thiếu (kiểm bằng **có mặt**, không phải khác rỗng), `googleCredential` thiếu, `redirectUri` có giá trị nhưng không phải chuỗi.
3. `projectId` lạ (không phải UUID/`new`) bị **bỏ qua im lặng**. `ResolveProjectID(clientId, projectId)`: client không có 404, `projectId` lệch project của client `Invalid projectId`.
4. `googleClientId` khác client chính: tìm project có `site.googleClientId`; 0 project: `Invalid googleClientId`; 1 project: dùng project đó.
5. Xác minh credential (RS256, `aud`): sai thì `Failed to verify Google credential` (cố định).
6. Credential thiếu email; email chưa xác minh.
7. Domain email có IdP ngoài (khi đăng nhập ngoài bật): phát state ký và dừng, trả `{domain, authorizeUrl}`.
8. Tìm user theo email (ưu tiên user của project rồi user toàn cục). Không có: nếu không `createUser` thì `User not found`; nếu `AUTH_REGISTER_ENABLED=false` và không có project cụ thể thì `Registration is disabled`; **ghi** User mới (`emailVerified=true`, khóa chống trùng). Đã có mà chưa xác minh email: nâng `emailVerified=true`.
9. `TryLogin` kiểu `google`: kiểm client, origin, redirect, PKCE, launch; **ghi** Login; chọn membership nếu có đúng một.
10. Dựng LoginResult (không bao giờ ra dạng 1 hoặc 2); đặt cookie đăng nhập khi có `code` và login thuộc client.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, code | Đã có membership | 200 | `Set-Cookie: medplum-<clientId>=...` (khi có client) | `LoginResult.code` | Tạo Login `authMethod=google`; có thể nâng `emailVerified` |
| C2 | Thành công, chọn profile | Nhiều membership | 200 | - | `LoginResult.memberships` | Như C1, không code |
| C3 | Thành công, project new | `projectId` là `new` | 200 | - | `LoginResult` chỉ `{login}` (dạng 3) | Tạo Login |
| C4 | Đăng ký mới qua Google | `createUser=true`, user chưa có | 200 | - | `LoginResult` dạng 3 hoặc 5 (không code, chưa có membership) | Tạo User (`emailVerified=true`) và Login |
| C5 | Miền email dùng IdP ngoài | Domain có `DomainConfiguration` đủ cấu hình | 200 | `Set-Cookie: auth-external-state=...` | `{ domain, authorizeUrl }` (không phải LoginResult) | Phát state ký; không tạo Login |
| C6 | Thiếu trường | `googleClientId`/`googleCredential` không có trong body | 400 | - | `Missing googleClientId` / `Missing googleCredential` (có thể cả hai issue) | Không |
| C7 | redirectUri sai kiểu | Có giá trị không phải chuỗi | 400 | - | `Invalid redirectUri`, `expression` `redirectUri` | Không |
| C8 | Client không tồn tại | `clientId` lạ | 404 | - | `Not found` | Không |
| C9 | Lệch project | `projectId` khác project của client | 400 | - | `Invalid projectId` | Không |
| C10 | googleClientId lạ | Không phải client chính, không project nào khai | 400 | - | `Invalid googleClientId` | Không |
| C11 | Credential không xác minh được | Rác, sai chữ ký, sai `aud`, hết hạn, không phải RS256 | 400 | - | `Failed to verify Google credential` | Không |
| C12 | Thiếu email | Credential không có email | 400 | - | `Google credential does not contain email address` | Không |
| C13 | Email chưa xác minh | `email_verified` không phải true | 400 | - | `Email address is not verified by Google` | Không |
| C14 | Không có user | Không `createUser` | 400 | - | `User not found` | Không |
| C15 | Đăng ký tắt | `createUser`, `AUTH_REGISTER_ENABLED=false`, không project cụ thể | 400 | - | `Registration is disabled` | Không |
| C16 | Trùng email khi tạo | Đua tạo cùng lúc | 400 | - | `Email already registered`, `expression` `email` | Không |
| C17 | Lỗi đăng nhập | `Invalid origin`, `Invalid redirect URI`, PKCE (`Invalid code challenge`, `Invalid code challenge method`), launch lạ 404, thiếu membership (`User not found`) | 400/404 | - | tương ứng | Login có thể chưa tạo |
| C18 | Lỗi chọn membership | `Project not active`, `Profile not active`, `IP address not allowed`, SMART... | 400 | - | tương ứng | Login đã tạo, chưa gắn membership |
| C19 | Hạn mức/JSON | N1, N3, N4 | 429/400/413 | như N | như N | Không |
| C20 | Route tắt | Không cấu hình Google | 404 (hoặc 405) | - | `Not Found` | Không |

**Ví dụ**

```http
POST /auth/google HTTP/1.1
Content-Type: application/json

{ "googleClientId": "1234567890-abc.apps.googleusercontent.com", "googleCredential": "eyJhbGciOiJSUzI1NiIs...", "clientId": "a3b1c2d4-0000-4000-8000-000000000001", "scope": "openid", "redirectUri": "https://app.example.org/callback" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=5d41402abc4b2a76b9719d911017c592; Path=/; Max-Age=3600; HttpOnly; Secure; SameSite=None

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: auth-external-state=3f9c2e7a1b3d5f60a8c1e2d3b4a59687; Path=/auth/external; Max-Age=600; HttpOnly; SameSite=Lax

{ "domain": "corp.example.org", "authorizeUrl": "https://idp.example.org/authorize?client_id=idp-client&response_type=code&state=eyJzIjp7...fQ.a1b2c3" }
```

**Lưu ý tích hợp:** `scope` mặc định `openid offline_access` (khác `/auth/login` là `openid`). Đăng nhập Google không qua MFA ở bước LoginResult. Phản hồi có thể là **hai kiểu body**: LoginResult hoặc `{domain, authorizeUrl}`; phân biệt bằng sự có mặt của `authorizeUrl`.

---

<a id="post-auth-login"></a>
## `POST /auth/login`

Bước đầu của đăng nhập bằng email và mật khẩu; trả một trong năm dạng LoginResult. [Tham chiếu trường](../reference/auth.md#post-auth-login)

**Tiền điều kiện:** không (route luôn bật). Hạn mức đăng nhập. Cookie `medplum-<clientId>` cần HTTPS khi `AUTH_COOKIE_SECURE=true`.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator gom theo thứ tự: `email` (hợp lệ), `projectId` (nếu **không falsy**: `new` hoặc UUID), `password` (8 ký tự đến 72 byte), `redirectUri` (nếu không falsy phải là chuỗi).
3. `ResolveProjectID`: có `clientId` thì project = project của client (404 nếu client không tồn tại; `Invalid projectId` nếu gửi `projectId` khác project của client). `scope` mặc định `openid`, `nonce` mặc định UUID.
4. `TryLogin`: kiểm yêu cầu (`Invalid projectId` dạng gắn trường), đọc client, `Invalid origin`, `Invalid redirect URI`, PKCE, launch (404 nếu không có).
5. Tìm user (project trước, rơi về user toàn cục); không có thì vẫn **tốn một lần bcrypt giả** rồi `Email or password is invalid`.
6. So mật khẩu: sai cùng thông điệp.
7. **Ghi: tạo Login** (kể cả khi bước sau lỗi).
8. Đọc lại user: mật khẩu đã đổi trong lúc đó thì thu hồi Login vừa tạo và `Email or password is invalid`.
9. Liệt kê membership (lọc theo `resourceType`, loại project không hoạt động; toàn bộ không hoạt động thì `Project not active`); không có và không phải `projectId=new` thì `User not found` (Login đã tạo vẫn tồn tại, không bao giờ trả code).
10. Đúng một membership: **ghi** gắn membership vào Login (`SetLoginMembership`: kiểm project/profile hoạt động, `google-auth-required`, scope SMART, IP).
11. Dựng LoginResult (bảng ở đầu tệp); dạng code thì **ghi** code và cookie băm.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, code | Đúng một membership, MFA không chặn | 200 | `Set-Cookie: medplum-<clientId>=...` nếu có `clientId` | `LoginResult.code` | Tạo Login `password`, gắn membership, phát code (băm); log `login success` |
| C2 | Thành công, chọn profile | Nhiều membership | 200 | - | `LoginResult.memberships` | Tạo Login chưa membership |
| C3 | Thành công, project new | `projectId` là `new` | 200 | - | `LoginResult` dạng 3 `{login}` | Tạo Login `project=Project/new` |
| C4 | Cần MFA | User đã đăng ký MFA | 200 | - | `LoginResult.mfaRequired` | Tạo Login; có thể tự gửi mã email |
| C5 | Cần đăng ký MFA | MFA bắt buộc, chưa đăng ký | 200 | - | `LoginResult.mfaEnrollRequired` | Tạo Login; sinh secret TOTP được mời |
| C6 | Lỗi nhập liệu | Email/projectId/password/redirectUri sai | 400 | - | Mỗi lỗi một issue theo thứ tự validator: `Valid email address is required` (`email`); `Invalid projectId` (`projectId`); `Password must be at least 8 characters` hoặc `Password must be no more than 72 characters` (`password`); `Invalid redirectUri` (`redirectUri`) | Không |
| C7 | Sai thông tin | Email không tồn tại, mật khẩu sai, hoặc tài khoản đang bị khóa do đăng nhập sai nhiều lần (phản hồi giống hệt nhau; khi khóa, mật khẩu đúng cũng bị từ chối) | 400 | - | `Email or password is invalid` | Không (user không tồn tại); sai mật khẩu cũng không tạo Login |
| C8 | Client không tồn tại | `clientId` lạ | 404 | - | `Not found` | Không |
| C9 | Lệch project | `projectId` khác project của client | 400 | - | `Invalid projectId` | Không |
| C10 | Origin/redirect/PKCE | `Invalid origin`; `Invalid redirect URI`; `Invalid code challenge` (`code_challenge`); `Invalid code challenge method` (`code_challenge_method`) | 400 | - | tương ứng (PKCE có `expression`) | Không |
| C11 | Launch không có | `launch` không tồn tại | 404 | - | `Not found` | Không |
| C12 | Không có membership | Không có và không phải `new` | 400 | - | `User not found` | Đã tạo Login (không dùng được, dọn bằng `purge-logins`) |
| C13 | Project không hoạt động | Mọi membership thuộc project `active=false` | 400 | - | `Project not active` | Đã tạo Login |
| C14 | Membership/profile không hoạt động | Một membership được chọn tự động bị tắt | 400 | - | `Profile not active` / `Project not active` | Đã tạo Login |
| C15 | IP bị chặn | `ipAccessRule` của AccessPolicy | 400 | - | `IP address not allowed` | Đã tạo Login |
| C16 | Chính sách hỏng | AccessPolicy không đọc được | 400 | - | `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` | Đã tạo Login |
| C17 | Yêu cầu Google | Project có feature `google-auth-required` | 400 | - | `Google authentication is required` | Đã tạo Login |
| C18 | SMART | Super admin xin SMART scope / thiếu patient context | 400 | - | `SMART scopes are not supported for super admin logins` / `Missing patient context for patient/ scope` | Đã tạo Login |
| C19 | Fail đóng MFA | Cần MFA mà phụ thuộc MFA hỏng/chưa lắp | 500 | - | `Internal server error` | Tạo Login; **không** trả code |
| C20 | Hạn mức đăng nhập | N1 | 429 | `Retry-After: 1` | như N1 | Không |
| C21 | JSON hỏng | N3 | 400 | - | `Content could not be parsed` | Không |

**Ví dụ**

```http
POST /auth/login HTTP/1.1
Content-Type: application/json
Origin: https://app.example.org

{ "email": "an@example.org", "password": "mat-khau-dai-123", "clientId": "a3b1c2d4-0000-4000-8000-000000000001", "scope": "openid profile", "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", "codeChallengeMethod": "S256", "redirectUri": "https://app.example.org/callback" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=5d41402abc4b2a76b9719d911017c592; Path=/; Max-Age=3600; HttpOnly; Secure; SameSite=None

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "memberships": [ { "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55", "project": { "reference": "Project/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a11", "display": "Bệnh viện A" }, "profile": { "reference": "Practitioner/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a22" } } ] }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "mfaRequired": true, "mfaMethods": ["totp"], "email": "an@example.org" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "mfaEnrollRequired": true, "enrollUri": "otpauth://totp/Trivita:an@example.org?secret=JBSWY3DPEHPK3PXP&issuer=Trivita", "enrollQrCode": "data:image/png;base64,iVBORw0KGgo...", "allowedMfaMethods": ["totp"] }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "issue": [
  { "severity": "error", "code": "invalid", "details": { "text": "Valid email address is required" }, "diagnostics": "Valid email address is required", "expression": ["email"] },
  { "severity": "error", "code": "invalid", "details": { "text": "Password must be at least 8 characters" }, "diagnostics": "Password must be at least 8 characters", "expression": ["password"] } ] }
```

**Lưu ý tích hợp:** `projectId` rỗng/`null`/`0`/`false` được bỏ qua như không gửi. Mỗi lần đăng nhập thành công tạo một Login mới; Login "mồ côi" do lỗi bước 9 trở đi không dùng được. Sai mật khẩu và email không tồn tại cố ý **không phân biệt** (cả thời gian xử lý). Không có cơ chế khóa tài khoản theo số lần sai, chỉ hạn mức IP. Có `code` thì đổi ở `POST /oauth2/token` trong 10 phút; gọi lại `GET /auth/login/{login}` sẽ làm code đó mất hiệu lực.

---

<a id="get-auth-login-login"></a>
## `GET /auth/login/{login}`

Lấy lại LoginResult hiện tại của một login đang dở để FE tiếp tục luồng (sau redirect IdP, tải lại trang). [Tham chiếu trường](../reference/auth.md#get-auth-login-login)

**Tiền điều kiện:** không. Công khai (biết UUID login là đủ). Chỉ hạn mức chung (không thuộc hạn mức đăng nhập).

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1).
2. `login` phải là UUID: nếu không 400.
3. Đọc Login (404 nếu không có).
4. `granted` thì 400 `Login already granted`; rồi `revoked` thì 400 `Login revoked`.
5. Dựng LoginResult (bảng ở đầu tệp). Dạng code **ghi** code mới (code cũ chết); dạng 1 ghi/đọc secret mời; dạng 2 có thể gửi mã email lần đầu.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Có code | Login có membership, MFA đã xong/không cần | 200 | `Set-Cookie: medplum-<clientId>=...` nếu login thuộc client | `LoginResult.code` | **Phát code mới, vô hiệu code cũ**; ghi cookie băm |
| C2 | Chọn profile | Chưa có membership | 200 | - | `LoginResult.memberships` (hoặc chỉ `{login}` nếu không có membership nào, xem bảng đầu tệp) | Không |
| C3 | Project new | `Project/new` | 200 | - | `LoginResult` `{login}` | Không |
| C4 | Cần MFA | Như dạng 2 | 200 | - | `LoginResult.mfaRequired` | Tự gửi mã nếu email là phương thức duy nhất và chưa gửi |
| C5 | Cần đăng ký MFA | Như dạng 1 | 200 | - | `LoginResult.mfaEnrollRequired` | Sinh/lấy secret mời |
| C6 | Id không phải UUID | vd `abc` | 400 | - | `Login ID is required`, `expression` `login` | Không |
| C7 | Không có login | UUID lạ | 404 | - | `Not found` | Không |
| C8 | Đã cấp token | `granted=true` | 400 | - | `Login already granted` | Không |
| C9 | Đã thu hồi | `revoked=true` | 400 | - | `Login revoked` | Không |
| C10 | Fail đóng MFA | Dạng 1/2 mà thiếu phụ thuộc MFA | 500 | - | `Internal server error` | Không |
| C11 | Phát code lỗi | Đua: login vừa bị thu hồi/cấp token | 400 | - | `Login cannot issue a code` | Không |

**Ví dụ**

```http
GET /auth/login/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66 HTTP/1.1
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "9a8b7c6d5e4f30211234567890abcdef" }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "issue": [ { "severity": "error", "code": "invalid", "details": { "text": "Login ID is required" }, "diagnostics": "Login ID is required", "expression": ["login"] } ] }
```

**Lưu ý tích hợp:** **không idempotent về code**: mỗi lần gọi khi login đã có membership là một code mới, code lấy trước đó không còn đổi được token. Dùng route này một lần sau callback IdP, đừng poll.

---

<a id="get-auth-me"></a>
## `GET /auth/me`

Thông tin người đang đăng nhập: user, project, membership, profile rút gọn, cấu hình giao diện, phiên và membership của user. [Tham chiếu trường](../reference/auth.md#get-auth-me)

**Tiền điều kiện:** Bearer. Chỉ hạn mức chung.

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1); Bearer (N5): token do auth ký, `token_use=access`, Login `granted` và chưa thu hồi, membership và project còn hoạt động.
2. Dựng `project`/`membership` rút gọn từ trạng thái xác thực; `profile` chỉ `{resourceType, id}`.
3. Đọc `UserConfiguration` của membership nếu có; thiếu `menu` thì dùng menu mặc định.
4. Nếu membership trỏ tới `User`: đọc user (`user`: id, email, identifier); và nếu login không bị giới hạn SMART resource scope: dựng `security` (mfaEnrolled, mfaRequired, sessions trong 1 giờ qua đã `granted` và chưa thu hồi, memberships).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, người dùng | Login của `User`, không giới hạn SMART | 200 | - | `user`, `project`, `membership`, `profile`, `config`, `security` | Không |
| C2 | Thành công, SMART | Login có SMART resource scope | 200 | - | như C1 **không** có `security` | Không |
| C3 | Thành công, ClientApplication | Token client_credentials | 200 | - | **không** có `user` và `security` | Không |
| C4 | Thiếu/sai Bearer | N5 | 401 | `WWW-Authenticate` | `Unauthorized` | Không |
| C5 | Cấu hình người dùng hỏng | `UserConfiguration` trỏ tới mà đọc lỗi | 404 hoặc 500 (chưa kiểm) | - | `Not found` hoặc `Internal server error` | Không |
| C6 | Lỗi nội bộ | N6 | 500 | - | `Internal server error` | Không |

**Ví dụ**

```http
GET /auth/me HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "user": { "resourceType": "User", "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a33", "email": "an@example.org" },
  "project": { "resourceType": "Project", "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a11", "name": "Bệnh viện A" },
  "membership": { "resourceType": "ProjectMembership", "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55", "user": { "reference": "User/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a33" }, "profile": { "reference": "Practitioner/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a22" }, "admin": true },
  "profile": { "resourceType": "Practitioner", "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a22" },
  "config": { "resourceType": "UserConfiguration", "menu": [] },
  "security": { "mfaEnrolled": false, "mfaRequired": false, "sessions": [ { "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "lastUpdated": "2026-11-24T10:15:00.000Z", "authMethod": "password", "remoteAddress": "203.0.113.10", "browser": "Chrome", "os": "macOS" } ], "memberships": [] } }
```

**Lưu ý tích hợp:** `profile` **không** có hồ sơ đầy đủ (lấy từ fhir-server); chưa có `accessPolicy` ở bản này. Access token là **có trạng thái**: login bị thu hồi thì 401 ngay. `sessions` chỉ liệt kê login cập nhật trong giờ qua.

---

<a id="post-auth-method"></a>
## `POST /auth/method`

Cho biết email đăng nhập bằng mật khẩu (`{}`) hay qua IdP ngoài của tên miền (`{domain, authorizeUrl}` kèm state ký và cookie). [Tham chiếu trường](../reference/auth.md#post-auth-method)

**Tiền điều kiện:** `AUTH_APP_BASE_URL` (và khóa state) để `authorizeUrl` có `state` và có cookie; chưa bật thì `authorizeUrl` **không** có state và không có cookie. Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. `email` phải là chuỗi email hợp lệ (một issue, `expression` `email`).
3. Lấy phần domain sau `@` (chữ thường); tìm `DomainConfiguration`; thiếu hoặc `identityProvider` chưa đủ `authorizeUrl`/`clientId` thì coi là đăng nhập mật khẩu: `{}`.
4. Đăng nhập ngoài bật: ký `state` chứa `clientId`, `projectId`, `scope`, `nonce`, `launch`, `codeChallenge`, `codeChallengeMethod`, `redirectUri`, `returnTo` và `domain`; dựng `authorizeUrl` (`authorizeUrl` của IdP thiếu scheme/host: 400); đặt cookie `auth-external-state`.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Mật khẩu | Domain không có IdP | 200 | - | `{}` | Không |
| C2 | IdP ngoài, có state | Domain có IdP, đăng nhập ngoài bật | 200 | `Set-Cookie: auth-external-state=<nonce>; Path=/auth/external; HttpOnly; SameSite=Lax; Max-Age=<TTL>` | `{ domain, authorizeUrl }` (`authorizeUrl` có `client_id`, `redirect_uri=<BaseURL>/auth/external`, `response_type=code`, `scope=openid profile email`, `state`) | Phát state ký (chưa ghi DB) |
| C3 | IdP ngoài, chưa bật | Domain có IdP, không có `AUTH_APP_BASE_URL` | 200 | - | `{ domain, authorizeUrl }` không có `state` | Không |
| C4 | Email sai | Không phải email | 400 | - | `Valid email address is required`, `expression` `email` | Không |
| C5 | authorizeUrl hỏng | IdP cấu hình `authorizeUrl` thiếu scheme/host | 400 | - | `Failed to construct URL for the domain` | Không |
| C6 | Hạn mức/JSON | N1, N3, N4 | 429/400/413 | như N | như N | Không |

**Ví dụ**

```http
POST /auth/method HTTP/1.1
Content-Type: application/json

{ "email": "an@corp.example.org", "clientId": "a3b1c2d4-0000-4000-8000-000000000001", "redirectUri": "https://app.example.org/callback", "returnTo": "https://app.example.org/signin" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: auth-external-state=3f9c2e7a1b3d5f60a8c1e2d3b4a59687; Path=/auth/external; Max-Age=600; HttpOnly; SameSite=Lax

{ "domain": "corp.example.org", "authorizeUrl": "https://idp.example.org/authorize?client_id=idp-client&redirect_uri=https%3A%2F%2Fauth.example.org%2Fauth%2Fexternal&response_type=code&scope=openid+profile+email&state=eyJzIjp7...fQ.a1b2c3" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{}
```

**Lưu ý tích hợp:** FE **không tự thêm state**, chỉ chuyển người dùng tới `authorizeUrl`. `domain` luôn chữ thường. Route này không phân biệt email có tài khoản hay không (chỉ nhìn domain). Tham số phụ (`clientId`...) chỉ có tác dụng khi đăng nhập ngoài bật.

---

## Hạn mức thử MFA

Áp cho các route MFA (và cho việc **tự gửi mã** khi dựng LoginResult). Có **ba lớp độc lập**, lớp sau không thay thế lớp trước:

1. **Hạn mức IP** (N1): `mfa/verify`, `mfa/login-enroll`, `mfa/send-email` dùng chung bộ đếm "đăng nhập" (`AUTH_LOGIN_RATE_LIMIT`); các route MFA Bearer chỉ chịu hạn mức chung. Body `Too many requests` (không có extension), `Retry-After: 1`.
2. **Bộ đếm lượt thử** (`verify`, `login-enroll` với TOTP, `enroll`, `disable`):
   - Giới hạn **5 lượt mỗi login** (`AUTH_MFA_LOGIN_ATTEMPT_LIMIT`) và **20 lượt mỗi user** (`AUTH_MFA_USER_ATTEMPT_LIMIT`) trong **10 phút** (`AUTH_MFA_LOGIN_EXPIRATION`), cửa sổ cố định tính từ lượt đầu tiên.
   - **Đặt chỗ nguyên tử trước khi biết mã đúng hay sai** (Redis script hoặc khóa bộ nhớ): cả hai bộ đếm cùng tăng, hoặc từ chối mà không tăng. Hết hạn mức login kiểm trước, rồi hạn mức user.
   - **Sai lần thứ 5 của một login thu hồi login đó** (lần đó vẫn trả 400 như lần sai thường). Lần gọi sau: `verify`/`login-enroll`/`send-email` nhận 400 `Login revoked`; route Bearer nhận 401 (vì login của chính phiên đã bị thu hồi).
   - **Thành công**: xóa bộ đếm của login và **trả lại một lượt** của bộ đếm user.
   - Login chỉ dùng được để xác minh MFA trong 10 phút kể từ `authTime`, quá hạn là 400 `Login expired`; kiểm này chạy **trước** khi đặt chỗ (không tốn lượt).
3. **Bộ đếm gửi mã email**: tối đa **5 lần mỗi login** (`AUTH_MFA_EMAIL_SEND_LIMIT`) trong **20 phút** (`AUTH_MFA_EMAIL_CODE_EXPIRATION`), khóa `send:<loginId>`. Tính cho `send-email`, `send-email-challenge` và lần tự gửi trong LoginResult; tăng **trước** khi gửi thư (gửi lỗi vẫn mất lượt).

Phản hồi vượt hạn mức MFA (cả bộ đếm lượt thử lẫn gửi mã):

| Thành phần | Giá trị |
|---|---|
| HTTP | 429 |
| Header | `Retry-After: <giây còn lại, làm tròn lên>` |
| Body | OperationOutcome `throttled`, `details.text` **`Too many requests`** (mọi route, không có thông điệp riêng theo route), kèm `extension[0].url = https://medplum.com/fhir/StructureDefinition/rate-limit-reset`, `valueUnsignedInt` = số giây như `Retry-After` |

```http
HTTP/1.1 429 Too Many Requests
Content-Type: application/fhir+json; charset=utf-8
Retry-After: 540

{ "resourceType": "OperationOutcome", "issue": [ { "severity": "error", "code": "throttled", "details": { "text": "Too many requests" }, "diagnostics": "Too many requests" } ], "extension": [ { "url": "https://medplum.com/fhir/StructureDefinition/rate-limit-reset", "valueUnsignedInt": 540 } ] }
```

Điểm dễ nhầm: kho trạng thái mặc định là **bộ nhớ một instance** (chỉ dev/test); nhiều instance phải đặt `AUTH_REDIS_URL` (bắt buộc ở staging/production), nếu không hạn mức nhân lên theo số instance. Lỗi kho trạng thái là 500 chung (fail đóng, không cho qua yếu tố). Các route ở dưới dùng mã `H1` để chỉ "hàng 429 hạn mức MFA" ở bảng này.

---

<a id="post-auth-mfa-disable"></a>
## `POST /auth/mfa/disable`

Tắt một phương thức MFA (hoặc tất cả) của chính mình, phải chứng minh một yếu tố đang có. [Tham chiếu trường](../reference/auth.md#post-auth-mfa-disable)

**Tiền điều kiện:** Bearer; MFA lắp ghép đủ (không thì 500). Chịu bộ đếm lượt thử (lệch có chủ đích so với Medplum).

**Thứ tự kiểm tra:**
1. Bearer (N5), JSON (N3).
2. MFA sẵn sàng (không thì 500 fail đóng).
3. Chủ thể là `User`, không SMART scope (403 `Forbidden`); user toàn cục chỉ khi login `password`/`google` (403 `Forbidden`); đọc User.
4. User chưa đăng ký MFA: `User not enrolled in MFA` (**trước** khi kiểm kiểu body).
5. `token` có mặt mà không phải chuỗi: `Token must be a string`; `method` không phải chuỗi: `Method must be a string`.
6. Nếu có `method`: phải là `totp` hoặc `email` (`Invalid method`) và user đã đăng ký (`User not enrolled in MFA method`). Không có `method` nghĩa là gỡ tất cả.
7. MFA bắt buộc (project hoặc user) và không còn yếu tố nào sau khi gỡ: `Cannot remove the last MFA factor because MFA is required` (gỡ tất cả luôn rơi vào đây khi MFA bắt buộc).
8. `token` rỗng: `Missing token`.
9. Đặt chỗ lượt thử (H1), kiểm token là mã TOTP (tiêu thụ mã) hoặc mã email còn hạn; sai thì `Invalid token` (có thể thu hồi login, xem mục hạn mức).
10. **Ghi:** nếu gỡ TOTP: thay `mfaSecret` bằng secret ngẫu nhiên mới (mã hóa); cập nhật `mfaEnrolled`/`mfaMethod`; xóa mã email trên Login.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - | OperationOutcome `All OK` | Gỡ yếu tố; đổi `mfaSecret` nếu gỡ TOTP; xóa mã email; trả lại lượt thử |
| C2 | Chưa đăng ký MFA | `mfaEnrolled` không true | 400 | - | `User not enrolled in MFA` | Không |
| C3 | Token sai kiểu | `token` có mặt, không phải chuỗi | 400 | - | `Token must be a string`, `expression` `token` | Không |
| C4 | Method sai kiểu | `method` có mặt, không phải chuỗi | 400 | - | `Method must be a string`, `expression` `method` | Không |
| C5 | Method lạ | khác `totp`/`email` | 400 | - | `Invalid method` | Không |
| C6 | Chưa đăng ký method đó | - | 400 | - | `User not enrolled in MFA method` | Không |
| C7 | Yếu tố cuối | MFA bắt buộc và không còn yếu tố | 400 | - | `Cannot remove the last MFA factor because MFA is required` | Không |
| C8 | Thiếu token | rỗng | 400 | - | `Missing token` | Không |
| C9 | Token sai | Mã không khớp yếu tố nào | 400 | - | `Invalid token` | Tăng bộ đếm; lần sai thứ 5 **thu hồi phiên hiện tại** |
| C10 | Vượt hạn mức MFA | H1 | 429 | `Retry-After` | `Too many requests` + extension | Không tăng |
| C11 | Không được tự quản lý | ClientApplication, SMART, user toàn cục external/exchange | 403 | - | `Forbidden` | Không |
| C12 | MFA chưa lắp | Thiếu phụ thuộc MFA | 500 | - | `Internal server error` | Không |
| C13 | Bearer/JSON | N3, N4, N5 | 400/413/401 | như N | như N | Không |

**Ví dụ**

```http
POST /auth/mfa/disable HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
Content-Type: application/json

{ "method": "email", "token": "482913" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** token TOTP vừa dùng để đăng nhập không dùng lại được (mỗi mã một lần, nhớ 4 bước 30 giây): đợi mã kế. Nhập sai 5 lần thu hồi **chính phiên đang dùng** (token chết, 401 ở lần sau). `method: null` coi như không gửi (gỡ tất cả).

---

<a id="post-auth-mfa-enroll"></a>
## `POST /auth/mfa/enroll`

Đăng ký thêm một phương thức MFA cho chính mình bằng cách chứng minh đang giữ yếu tố đó (email hoặc TOTP). [Tham chiếu trường](../reference/auth.md#post-auth-mfa-enroll)

**Tiền điều kiện:** Bearer. Phương thức phải nằm trong `allowedMfaMethods` của project (mặc định chỉ `totp`; setting dạng `totp,email`). Chịu bộ đếm lượt thử.

**Thứ tự kiểm tra:**
1. Bearer (N5), JSON (N3), MFA sẵn sàng (500).
2. Chủ thể `User`/không SMART (403 `Forbidden`), đọc User.
3. `method` lấy `email` nếu đúng chuỗi `email`, mọi giá trị khác là `totp`. Đã đăng ký phương thức đó: `Already enrolled`.
4. Project không cho phép phương thức: `MFA method not allowed`.
5. Đọc Login của phiên.
6. `email`: `token` rỗng `Missing token`; rồi đặt chỗ (H1), kiểm mã email trên Login (từ `send-email-challenge`); sai `Invalid token`.
7. `totp`: không có secret mời của (user, phiên) trong kho (`GET /auth/mfa/status` chưa gọi hoặc hết 20 phút): `Secret not found` (**trước** `Missing token`); `token` rỗng `Missing token`; đặt chỗ (H1), kiểm TOTP **không tiêu thụ mã**; sai `Invalid token`.
8. **Ghi:** email: `mfaEnrolled`, `mfaMethod`, `emailVerified=true`, xóa mã email. TOTP: ghi `mfaSecret` (mã hóa), `mfaMethod`; xóa secret mời. Hai phiên đăng ký cùng lúc: đúng một thắng, cái kia `Already enrolled`.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - | OperationOutcome `All OK` | Ghi yếu tố; xóa mã email hoặc secret mời; trả lại lượt thử |
| C2 | Đã đăng ký | Có phương thức này rồi | 400 | - | `Already enrolled` | Không |
| C3 | Phương thức không cho phép | Ngoài `allowedMfaMethods` | 400 | - | `MFA method not allowed` | Không |
| C4 | Không có secret | TOTP, chưa gọi status / hết hạn 20 phút | 400 | - | `Secret not found` | Không |
| C5 | Thiếu token | rỗng/thiếu | 400 | - | `Missing token` | Không |
| C6 | Token sai | Mã không khớp | 400 | - | `Invalid token` | Tăng bộ đếm; lần sai thứ 5 **thu hồi phiên hiện tại** |
| C7 | Vượt hạn mức MFA | H1 | 429 | `Retry-After` | `Too many requests` + extension | Không |
| C8 | Không được tự quản lý | như disable | 403 | - | `Forbidden` | Không |
| C9 | MFA chưa lắp | - | 500 | - | `Internal server error` | Không |
| C10 | Bearer/JSON | N3, N4, N5 | như N | như N | như N | Không |

**Ví dụ**

```http
POST /auth/mfa/enroll HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
Content-Type: application/json

{ "method": "totp", "token": "123456" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** thứ tự gọi TOTP: `GET /auth/mfa/status` (nhận `enrollUri`) rồi `POST /auth/mfa/enroll`. Email: `POST /auth/mfa/send-email-challenge` rồi `enroll` với `method:"email"`. Mã TOTP dùng để đăng ký **không** bị tiêu thụ. Khác Medplum: có bộ đếm lượt thử.

---

<a id="post-auth-mfa-login-enroll"></a>
## `POST /auth/mfa/login-enroll`

Đăng ký phương thức MFA **đầu tiên** ngay trong lúc đăng nhập (sau LoginResult `mfaEnrollRequired`). [Tham chiếu trường](../reference/auth.md#post-auth-mfa-login-enroll)

**Tiền điều kiện:** MFA lắp ghép đủ. Công khai (dùng id login). Hạn mức đăng nhập. Muốn dùng `totp` thì login phải từng nhận `enrollUri` (secret mời) trong 20 phút.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. `login` rỗng/thiếu: 400 `Missing login`.
3. MFA sẵn sàng (500). Đọc Login (404), đọc User.
4. User đã đăng ký MFA: `Already enrolled`.
5. Login còn dùng được cho MFA (thứ tự): `Login revoked`, `Login granted`, `Login already verified`, `Invalid login authentication method` (chỉ đăng nhập mật khẩu), `Login expired` (quá 10 phút từ `authTime`).
6. `method`: `email` hoặc mặc định `totp`. Phương thức không được project cho phép: `MFA method not allowed`.
7. **Email:** **ghi** `mfaEnrolled`, `mfaMethod` (race: `Already enrolled`); dựng LoginResult, kết quả là `mfaRequired` (và tự gửi mã email vì là phương thức duy nhất; tăng bộ đếm gửi mã). **Không** có bước đặt chỗ lượt thử.
8. **TOTP:** không có secret mời: `Secret not found`; `token` rỗng: `Missing token`; kiểm lại trạng thái login, đặt chỗ lượt thử (H1), kiểm mã (tiêu thụ); sai `Invalid MFA token`.
9. **TOTP đúng, ghi (theo thứ tự):** `mfaSecret` (mã hóa) + `mfaEnrolled` + `mfaMethod`, xóa secret mời, đặt `mfaVerified=true` trên Login (race: `Login revoked`), trả lại lượt thử; dựng LoginResult.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | TOTP thành công | Mã đúng | 200 | `Set-Cookie: medplum-<clientId>=...` nếu có code và login thuộc client | `LoginResult` (thường `.code`; có thể `.memberships` hoặc dạng project new) | Ghi yếu tố TOTP; Login `mfaVerified`; code mới |
| C2 | Email thành công | `method:"email"` | 200 | - | `LoginResult.mfaRequired` (`mfaMethods:["email"]`) | Ghi yếu tố email; gửi mã; nhập mã ở `mfa/verify` |
| C3 | Thiếu login | rỗng/thiếu | 400 | - | `Missing login`, `expression` `login` | Không |
| C4 | Login không có | UUID lạ (id không phải UUID: chưa kiểm) | 404 | - | `Not found` | Không |
| C5 | Đã đăng ký | User có MFA rồi | 400 | - | `Already enrolled` | Không |
| C6 | Login không dùng được | `Login revoked` / `Login granted` / `Login already verified` / `Invalid login authentication method` / `Login expired` | 400 | - | thông điệp tương ứng | Không |
| C7 | Phương thức không cho phép | project không cho | 400 | - | `MFA method not allowed` | Không |
| C8 | Không có secret | Chưa nhận `enrollUri` cho login này / hết hạn | 400 | - | `Secret not found` | Không |
| C9 | Thiếu token | TOTP, `token` rỗng | 400 | - | `Missing token` | Không |
| C10 | Mã sai | TOTP không khớp, hoặc mã đã dùng | 400 | - | `Invalid MFA token` | Tăng bộ đếm; lần sai thứ 5 thu hồi login |
| C11 | Vượt hạn mức MFA | H1 | 429 | `Retry-After` | `Too many requests` + extension | Không |
| C12 | Hạn mức IP | N1 | 429 | `Retry-After: 1` | như N1 | Không |
| C13 | MFA chưa lắp | - | 500 | - | `Internal server error` | Không |

**Ví dụ**

```http
POST /auth/mfa/login-enroll HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "method": "totp", "token": "123456" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=5d41402abc4b2a76b9719d911017c592; Path=/; Max-Age=3600; HttpOnly; Secure; SameSite=None

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "mfaRequired": true, "mfaMethods": ["email"], "email": "an@example.org" }
```

**Lưu ý tích hợp:** secret trong `enrollUri` thuộc cặp (user, login): đăng ký bằng login khác thì `Secret not found`. Nhánh email chưa xác minh gì: phải gọi tiếp `POST /auth/mfa/verify` với mã nhận qua thư.

---

<a id="post-auth-mfa-send-email"></a>
## `POST /auth/mfa/send-email`

Gửi lại mã MFA qua email cho một login đang chờ MFA (user đã đăng ký phương thức email). [Tham chiếu trường](../reference/auth.md#post-auth-mfa-send-email)

**Tiền điều kiện:** MFA và kênh thư (SMTP) lắp ghép đủ. Công khai theo id login. Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. `login` rỗng/thiếu: `Missing login`.
3. MFA sẵn sàng (500). Đọc Login (404).
4. Trạng thái login dùng được cho MFA (thứ tự: `Login revoked`, `Login granted`, `Login already verified`, `Invalid login authentication method`, `Login expired`).
5. Đọc User; chưa đăng ký **email** MFA: `User not enrolled in email MFA`.
6. Gửi mã: không có kênh thư 500; user không có email `User has no email address`; bộ đếm gửi (H1, 5/login/20 phút); **ghi** băm mã 6 số lên Login (ghi đè mã cũ, hạn 20 phút); gửi thư (lỗi gửi: 500).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - | OperationOutcome `All OK` | Ghi băm mã mới (mã cũ mất); gửi thư; tăng bộ đếm gửi |
| C2 | Thiếu login | rỗng/thiếu | 400 | - | `Missing login`, `expression` `login` | Không |
| C3 | Login không có | UUID lạ | 404 | - | `Not found` | Không |
| C4 | Login không dùng được | 5 thông điệp `Login ...` như login-enroll C6 | 400 | - | tương ứng | Không |
| C5 | Chưa đăng ký email | user không có email MFA | 400 | - | `User not enrolled in email MFA` | Không |
| C6 | User không có email | - | 400 | - | `User has no email address` | Không |
| C7 | Quá số lần gửi | Hơn 5 lần trong 20 phút cho login | 429 | `Retry-After` | `Too many requests` + extension | Không gửi |
| C8 | Gửi lỗi/chưa có kênh thư | SMTP lỗi | 500 | - | `Internal server error` | Mã đã ghi trên Login nhưng thư không đến; lượt gửi đã tính |
| C9 | Hạn mức IP | N1 | 429 | `Retry-After: 1` | như N1 | Không |
| C10 | MFA chưa lắp | - | 500 | - | `Internal server error` | Không |

**Ví dụ**

```http
POST /auth/mfa/send-email HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** nếu email là phương thức MFA duy nhất, mã **đã được tự gửi** khi login trả `mfaRequired`; chỉ gọi route này để gửi lại. Gửi lại làm mã trước mất hiệu lực. Mã hết hạn sau 20 phút.

---

<a id="post-auth-mfa-send-email-challenge"></a>
## `POST /auth/mfa/send-email-challenge`

Gửi mã email cho **phiên đang dùng** để đăng ký email MFA (`/auth/mfa/enroll`) hoặc làm yếu tố khi tắt MFA. [Tham chiếu trường](../reference/auth.md#post-auth-mfa-send-email-challenge)

**Tiền điều kiện:** Bearer; MFA và kênh thư lắp ghép. Không có body.

**Thứ tự kiểm tra:**
1. Bearer (N5), MFA sẵn sàng (500).
2. Chủ thể `User`/không SMART (403 `Forbidden`), đọc User.
3. User chưa có email MFA **và** project không cho phép email: 400 `Email MFA not available`.
4. Đọc Login của phiên; gửi mã: không có kênh thư 500, `User has no email address`, bộ đếm gửi (H1), **ghi** băm mã lên Login, gửi thư.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - | OperationOutcome `All OK` | Ghi băm mã mới trên Login phiên; gửi thư; tăng bộ đếm gửi |
| C2 | Email không khả dụng | Chưa có email MFA và project không cho `email` | 400 | - | `Email MFA not available` | Không |
| C3 | User không có email | - | 400 | - | `User has no email address` | Không |
| C4 | Quá số lần gửi | > 5 lần/20 phút/login | 429 | `Retry-After` | `Too many requests` + extension | Không |
| C5 | Không được tự quản lý | - | 403 | - | `Forbidden` | Không |
| C6 | Gửi lỗi / MFA chưa lắp | - | 500 | - | `Internal server error` | Lượt gửi đã tính |
| C7 | Bearer | N5 | 401 | `WWW-Authenticate` | `Unauthorized` | Không |

**Ví dụ**

```http
POST /auth/mfa/send-email-challenge HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** chia sẻ bộ đếm gửi mã với `send-email` (khóa theo login). Mã gắn với Login của phiên nên dùng bằng chính token đã gọi.

---

<a id="get-auth-mfa-status"></a>
## `GET /auth/mfa/status`

Trạng thái MFA của chính mình; nếu chưa có TOTP thì kèm `enrollUri`/`enrollQrCode` để đăng ký. [Tham chiếu trường](../reference/auth.md#get-auth-mfa-status)

**Tiền điều kiện:** Bearer; MFA lắp ghép đủ.

**Thứ tự kiểm tra:**
1. Bearer (N5), MFA sẵn sàng (500).
2. Chủ thể `User`/không SMART (403 `Forbidden`); user toàn cục chỉ khi login `password`/`google` (403 `Forbidden`).
3. Dựng `enrolled`, `enrolledMethods`, `allowedMethods` (từ project), `mfaRequired`.
4. Chưa đăng ký TOTP: **sinh (hoặc lấy lại) secret mời** cho cặp (user, phiên), giữ 20 phút, dựng `enrollUri` và `enrollQrCode`.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Chưa có TOTP | User chưa đăng ký TOTP | 200 | - | `enrolled`, `enrolledMethods`, `allowedMethods`, `mfaRequired`, `email`, `enrollUri`, `enrollQrCode` | Ghi/giữ secret mời (kho trạng thái, 20 phút) |
| C2 | Đã có TOTP | - | 200 | - | như C1 **không** có `enrollUri`/`enrollQrCode` | Không |
| C3 | Không được tự quản lý | ClientApplication, SMART, user toàn cục external/exchange | 403 | - | `Forbidden` | Không |
| C4 | MFA chưa lắp / QR lỗi | - | 500 | - | `Internal server error` | Không |
| C5 | Bearer | N5 | 401 | `WWW-Authenticate` | `Unauthorized` | Không |

**Ví dụ**

```http
GET /auth/mfa/status HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "enrolled": false, "enrolledMethods": [], "allowedMethods": ["totp", "email"], "mfaRequired": true, "email": "an@example.org", "enrollUri": "otpauth://totp/Trivita:an@example.org?secret=JBSWY3DPEHPK3PXP&issuer=Trivita", "enrollQrCode": "data:image/png;base64,iVBORw0KGgo..." }
```

**Lưu ý tích hợp:** secret trong `enrollUri` chỉ thành yếu tố khi chính phiên này xác nhận bằng `POST /auth/mfa/enroll`. Gọi lại trong 20 phút trả **cùng secret** (không sinh mới).

---

<a id="post-auth-mfa-verify"></a>
## `POST /auth/mfa/verify`

Xác minh mã TOTP hoặc mã email khi đăng nhập (sau LoginResult `mfaRequired`, hoặc đăng ký TOTP luôn nếu user bắt buộc MFA mà chưa đăng ký). [Tham chiếu trường](../reference/auth.md#post-auth-mfa-verify)

**Tiền điều kiện:** MFA lắp ghép đủ. Công khai (id login). Hạn mức đăng nhập và [Hạn mức thử MFA](#hạn-mức-thử-mfa).

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator gom: `login` rỗng `Missing login`, `token` rỗng `Missing token`.
3. MFA sẵn sàng (500). Đọc Login (404), đọc User.
4. User chưa đăng ký MFA và project cho phép TOTP: lấy secret mời của login này (nếu có) để đăng ký luôn.
5. Login dùng được cho MFA (thứ tự: `Login revoked`, `Login granted`, `Login already verified`, `Invalid login authentication method`, `Login expired`).
6. Mã email trên Login đã hết hạn: nếu không có yếu tố TOTP thì `MFA code expired`, nếu có TOTP thì bỏ qua mã email.
7. Không có yếu tố nào để kiểm: `User not enrolled in MFA`.
8. Đặt chỗ lượt thử (H1).
9. So mã email (bcrypt), rồi TOTP (tiêu thụ mã: mỗi mã một lần cho mỗi user); sai thì `Invalid MFA token` (cùng thông điệp cho cả hai yếu tố) và tăng bộ đếm, lần thứ 5 **thu hồi login**.
10. **Ghi (đúng):** mã email đúng thì đánh dấu `emailVerified`; đăng ký TOTP luôn thì ghi `mfaSecret` (mã hóa) và xóa secret mời; đặt `mfaVerified=true` trên Login (race với thu hồi: `Login revoked`), xóa mã email; trả lại lượt thử; dựng LoginResult.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, có code | Mã đúng, login có membership | 200 | `Set-Cookie: medplum-<clientId>=...` nếu login thuộc client | `LoginResult.code` | Login `mfaVerified`; tiêu thụ mã TOTP / xóa mã email; code mới |
| C2 | Thành công, chọn profile | Chưa có membership | 200 | - | `LoginResult.memberships` | Như C1, không code |
| C3 | Thành công, project new | Login `Project/new` | 200 | - | `LoginResult` `{login}` | Như C1 |
| C4 | Đăng ký TOTP luôn | User bắt buộc MFA, chưa đăng ký, mã đúng cho secret mời | 200 | như C1 | `LoginResult` như trên | Ghi `mfaSecret`, `mfaEnrolled`; xóa secret mời |
| C5 | Thiếu trường | `login`/`token` rỗng | 400 | - | Một issue mỗi trường: `Missing login` (`login`), `Missing token` (`token`) | Không |
| C6 | Login không có | UUID lạ (id không phải UUID: chưa kiểm) | 404 | - | `Not found` | Không |
| C7 | Login không dùng được | `Login revoked`, `Login granted`, `Login already verified`, `Invalid login authentication method`, `Login expired` | 400 | - | tương ứng | Không |
| C8 | Mã email hết hạn | Chỉ có yếu tố email, quá 20 phút | 400 | - | `MFA code expired` | Không (gọi `send-email` để lấy mã mới) |
| C9 | Không đăng ký MFA | Không có yếu tố nào | 400 | - | `User not enrolled in MFA` | Không |
| C10 | Mã sai | Không khớp / mã TOTP đã dùng | 400 | - | `Invalid MFA token` | Tăng bộ đếm login và user; lần sai thứ 5 thu hồi login |
| C11 | Vượt hạn mức MFA | H1 | 429 | `Retry-After` | `Too many requests` + extension `rate-limit-reset` | Không tăng |
| C12 | Hạn mức IP | N1 | 429 | `Retry-After: 1` | như N1 | Không |
| C13 | MFA chưa lắp / kho trạng thái lỗi | Fail đóng | 500 | - | `Internal server error` | Không cho qua yếu tố |

**Ví dụ**

```http
POST /auth/mfa/verify HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "token": "123456" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=5d41402abc4b2a76b9719d911017c592; Path=/; Max-Age=3600; HttpOnly; Secure; SameSite=None

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "issue": [ { "severity": "error", "code": "invalid", "details": { "text": "Invalid MFA token" }, "diagnostics": "Invalid MFA token" } ] }
```

**Lưu ý tích hợp:** sau 5 lần sai login bị thu hồi, FE phải đưa người dùng về `POST /auth/login` làm lại. Đừng gửi lại cùng mã TOTP ở hai lần gọi (mã dùng một lần); đợi bước 30 giây kế. Login chỉ dùng được 10 phút từ lúc đăng nhập mật khẩu. Hai bước gọi trong luồng email: `send-email` (nếu cần) rồi `verify`.

---

<a id="post-auth-newpatient"></a>
## `POST /auth/newpatient`

Bệnh nhân tự đăng ký vào một project cho phép đăng ký mở: tạo hồ sơ Patient và membership, trả LoginResult. [Tham chiếu trường](../reference/auth.md#post-auth-newpatient)

**Tiền điều kiện:** project có setting `openPatientRegistration=true` **và** chính sách Patient (`defaultAccessPolicies` loại Patient, hoặc `defaultPatientAccessPolicy`) còn tồn tại. Login lấy từ `POST /auth/newuser` của user thuộc **đúng** project này. Không có hạn mức đăng nhập riêng, và (theo mã) **không** kiểm cờ `AUTH_REGISTER_ENABLED`.

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1), JSON (N3).
2. Validator gom: `login` không phải chuỗi không rỗng (`Missing login`), `projectId` không phải chuỗi không rỗng (`Project ID is required`).
3. Đọc Login (404; id không phải UUID: chưa kiểm). Rồi theo thứ tự: đã có membership `Login already has a membership`; login có project khác `Project/<projectId>`: `Invalid project`; `Login revoked`; `Login granted`; thiếu user `User reference is missing`.
4. Đọc User; user không thuộc đúng `Project/<projectId>`: `User must be scoped to the project`.
5. Login còn thiếu MFA: trả LoginResult `mfaRequired`/`mfaEnrollRequired` **mà chưa tạo gì**.
6. Đọc Project (404); `Project not active`; chưa cho đăng ký mở `Project does not allow open registration`; đọc AccessPolicy (404 nếu mất).
7. **Transaction:** tạo Patient (tên từ user, email telecom) và ProjectMembership gắn chính sách.
8. **Ghi:** gắn membership vào Login (`SetLoginMembership`: `Profile not active`, IP, SMART...). Dựng LoginResult (dạng code, ghi code/cookie băm).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | `Set-Cookie: medplum-<clientId>=...` nếu login thuộc client | `LoginResult.code` | Tạo Patient + membership (một transaction); gắn membership; phát code |
| C2 | Cần MFA | Login chưa xác minh MFA | 200 | - | `LoginResult.mfaRequired` hoặc `.mfaEnrollRequired` | Không tạo Patient/membership |
| C3 | Thiếu trường | `login`/`projectId` thiếu hoặc rỗng | 400 | - | `Missing login` (`login`), `Project ID is required` (`projectId`) (hai issue nếu thiếu cả hai) | Không |
| C4 | Login không có | UUID lạ | 404 | - | `Not found` | Không |
| C5 | Đã có membership | Dùng lại login | 400 | - | `Login already has a membership` | Không |
| C6 | Project lệch | Login thuộc project khác | 400 | - | `Invalid project` | Không |
| C7 | Login đã dùng | `Login revoked` / `Login granted` | 400 | - | tương ứng | Không |
| C8 | User ngoài project | User không thuộc `projectId` | 400 | - | `User must be scoped to the project` | Không |
| C9 | Project không hoạt động | `active=false` | 400 | - | `Project not active` | Không |
| C10 | Không cho đăng ký mở | Thiếu setting hoặc chính sách | 400 | - | `Project does not allow open registration` | Không |
| C11 | Project/chính sách không tồn tại | - | 404 | - | `Not found` | Không |
| C12 | Lỗi chọn membership | `Profile not active`, `IP address not allowed`... | 400 | - | tương ứng | Patient và membership **đã tạo** (đã commit), Login chưa gắn |
| C13 | Fail đóng MFA | Cần MFA mà MFA hỏng | 500 | - | `Internal server error` | Không |
| C14 | JSON hỏng/lớn | N3, N4 | 400/413 | - | như N | Không |

**Ví dụ**

```http
POST /auth/newpatient HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "projectId": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a11" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=5d41402abc4b2a76b9719d911017c592; Path=/; Max-Age=3600; HttpOnly; Secure; SameSite=None

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

**Lưu ý tích hợp:** thứ tự: `POST /auth/newuser` (với `projectId` của project mở) rồi `POST /auth/newpatient` với `login` nhận được. Không idempotent: gọi lại là `Login already has a membership`. Mọi kiểm tra chạy **trước** khi tạo gì, trừ lỗi ở bước 8 (hiếm). Không có `Set-Cookie` nếu login không thuộc client (đăng ký không kèm `clientId`).

---

<a id="post-auth-newproject"></a>
## `POST /auth/newproject`

Người vừa đăng ký tạo project mới (6 chính sách mặc định, client mặc định, hồ sơ Practitioner, membership admin) rồi nhận LoginResult. [Tham chiếu trường](../reference/auth.md#post-auth-newproject)

**Tiền điều kiện:** `AUTH_REGISTER_ENABLED` và `AUTH_NEW_PROJECT_REGISTRATION_ENABLED` bật. Login từ `POST /auth/newuser` (project `new` hoặc không project). Hạn mức đăng nhập. Khi `AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION=true`, email phải đã xác minh.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator gom: `login` không phải UUID (`Login ID is required.`, chú ý dấu chấm cuối), `projectName` 4 đến 72 **ký tự** (đếm ký tự, không phải byte).
3. `Registration is disabled`; `New project registration is disabled`.
4. Đọc Login (404). Theo thứ tự: `Login already has a membership`; login đã có project khác `new`: `Invalid project`; `Login revoked`; `Login granted`; `User reference is missing`.
5. Đọc User. Login còn thiếu MFA: trả LoginResult `mfaRequired`/`mfaEnrollRequired` mà **chưa tạo gì**.
6. Bắt xác minh email mà user chưa xác minh: **gửi lại thư xác minh** (lỗi gửi chỉ log) rồi 400.
7. **Transaction:** tạo Project, 6 AccessPolicy, ClientApplication mặc định, Practitioner, ProjectMembership admin.
8. **Ghi:** gắn membership vào Login (nếu lỗi ở đây, project đã commit). Gửi thư chào nếu đây là project đầu tiên của user (lỗi chỉ log). Dựng LoginResult (dạng code).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | `Set-Cookie: medplum-<clientId>=...` nếu login thuộc client | `LoginResult.code` | Tạo project đầy đủ trong một transaction; gắn membership; thư chào (nếu lần đầu); phát code |
| C2 | Cần MFA | Login chưa xác minh MFA | 200 | - | `LoginResult.mfaRequired` hoặc `.mfaEnrollRequired` | Không tạo gì |
| C3 | Lỗi nhập liệu | `login` không UUID; tên 4 đến 72 ký tự | 400 | - | `Login ID is required.` (`login`); `Project name must be between 4 and 72 characters` (`projectName`) | Không |
| C4 | Đăng ký tắt | `AUTH_REGISTER_ENABLED=false` | 400 | - | `Registration is disabled` | Không |
| C5 | Tạo project tắt | `AUTH_NEW_PROJECT_REGISTRATION_ENABLED=false` | 400 | - | `New project registration is disabled` | Không |
| C6 | Login không có | UUID lạ | 404 | - | `Not found` | Không |
| C7 | Đã có membership | Dùng lại login | 400 | - | `Login already has a membership` | Không |
| C8 | Project lệch | Login đã gắn project thật (ví dụ đăng ký qua client của project khác) | 400 | - | `Invalid project` | Không |
| C9 | Login đã dùng | `Login revoked` / `Login granted` | 400 | - | tương ứng | Không |
| C10 | Cần xác minh email | Cờ bật, `emailVerified` chưa true | 400 | - | `Email verification is required to create a project. Check your email for a verification link.` | Gửi lại thư xác minh (nếu cấu hình thư) |
| C11 | Lỗi chọn membership | Sau khi tạo: `IP address not allowed`... | 400 | - | tương ứng | Project **đã tạo**, Login chưa gắn |
| C12 | Fail đóng MFA | MFA hỏng | 500 | - | `Internal server error` | Không |
| C13 | Hạn mức đăng nhập | N1 | 429 | `Retry-After: 1` | như N1 | Không |

**Ví dụ**

```http
POST /auth/newproject HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "projectName": "Phòng khám Bình" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

**Lưu ý tích hợp:** thứ tự: `newuser` (`projectId:"new"`), rồi (nếu `emailVerificationRequired`) `verifyemail`, rồi `newproject`. Login thuộc client của một project khác sẽ bị `Invalid project`; dùng login mới với project `new`. Dùng lại login: `Login already has a membership`. Thứ tự đúng của lỗi (mã thắng): tắt đăng ký rồi tên project (ở validator) rồi trạng thái login.

---

<a id="post-auth-newuser"></a>
## `POST /auth/newuser`

Đăng ký tài khoản: tạo User rồi tạo Login **chưa có membership**; phản hồi **không có `code`**. [Tham chiếu trường](../reference/auth.md#post-auth-newuser)

**Tiền điều kiện:** `AUTH_REGISTER_ENABLED` (không thì `Registration is disabled`). reCAPTCHA chỉ khi cấu hình (`AUTH_RECAPTCHA_SITE_KEY`/`SECRET_KEY` hoặc khóa trong `Project.site`). Gửi thư xác minh cần SMTP và `AUTH_APP_BASE_URL`. Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator gom theo thứ tự: `firstName`, `lastName` (1 đến 128 ký tự), `email` (3 đến 72 ký tự, hợp lệ), `password` (8 ký tự đến 72 byte).
3. reCAPTCHA **trước** cờ đăng ký: khóa lạ `Invalid recaptchaSiteKey`; project không có secret `Invalid recaptchaSecretKey`; project không nhận đăng ký mở (`Project not active`, `Project does not allow open registration`); thiếu token `Recaptcha token is required`; sai `Recaptcha failed`.
4. `Registration is disabled`.
5. Có `clientId`: đọc client (404); `projectId` có mặt mà khác project của client: `Client and project do not match`; không gửi `projectId` thì lấy project của client cho login (nhưng User **không** gắn project).
6. Kiểm yêu cầu đăng nhập **trước khi tạo user**: `Invalid projectId`, client/origin/redirect, PKCE, launch.
7. `projectId` cụ thể (khác `new`): đọc project (404), phải hoạt động và cho đăng ký mở.
8. **Transaction có khóa theo (phạm vi, email):** kiểm mật khẩu rò rỉ (HIBP; lỗi dịch vụ 500) và băm mật khẩu, kiểm trùng email, **ghi User** (email chữ thường; thuộc project chỉ khi body có `projectId` khác `new`).
9. **Ghi:** tạo Login (không membership).
10. Nếu `AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION` và `projectId = "new"`: `emailVerificationRequired=true`, **ghi** UserSecurityRequest (hạn 7 ngày) và gửi thư xác minh (lỗi gửi chỉ log, vẫn 200).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - (không có cookie) | `{ login, emailVerificationRequired: false }` | Tạo User và Login chưa membership |
| C2 | Thành công, cần xác minh email | Cờ bật và `projectId` là `new` | 200 | - | `{ login, emailVerificationRequired: true }` | Như C1, thêm UserSecurityRequest `verify-email`; gửi thư (nếu có SMTP và `AUTH_APP_BASE_URL`) |
| C3 | Lỗi nhập liệu | Mỗi trường một issue | 400 | - | `First name must be between 1 and 128 characters` (`firstName`); `Last name must be between 1 and 128 characters` (`lastName`); `Valid email address between 3 and 72 characters is required` (`email`); `Password must be at least 8 characters` / `Password must be no more than 72 characters` (`password`) | Không |
| C4 | reCAPTCHA | `Recaptcha token is required` / `Recaptcha failed` / `Invalid recaptchaSiteKey` / `Invalid recaptchaSecretKey` | 400 | - | thông điệp tương ứng | Không |
| C5 | Đăng ký tắt | `AUTH_REGISTER_ENABLED=false` | 400 | - | `Registration is disabled` (sau reCAPTCHA: không lộ cờ cho người chưa qua reCAPTCHA) | Không |
| C6 | Client lạ | `clientId` không tồn tại | 404 | - | `Not found` | Không |
| C7 | Client và project lệch | `projectId` khác project của client (kể cả `new` với client của project thật) | 400 | - | `Client and project do not match` | Không |
| C8 | Yêu cầu đăng nhập sai | `Invalid projectId`, `Invalid origin`, `Invalid redirect URI`, PKCE (`Invalid code challenge`/`Invalid code challenge method`) | 400 | - | tương ứng | Không tạo User |
| C9 | Project không nhận đăng ký | Không hoạt động / không mở | 400 | - | `Project not active` / `Project does not allow open registration` | Không tạo User |
| C10 | Project không tồn tại | `projectId` UUID lạ | 404 | - | `Not found` | Không |
| C11 | Mật khẩu rò rỉ | HIBP > 0 | 400 | - | `Password found in breach database`, `expression` `password` | Không |
| C12 | Trùng email | Đã có trong cùng phạm vi (project hoặc toàn cục) | 400 | - | `Email already registered`, `expression` `email` | Không |
| C13 | Fail đóng | HIBP/reCAPTCHA lỗi hoặc không lắp | 500 | - | `Internal server error` | Không tạo User |
| C14 | Lỗi sau khi tạo User | `TryLogin` lỗi (hiếm) | 400/500 | - | tương ứng | User đã tạo |
| C15 | Hạn mức đăng nhập | N1 | 429 | `Retry-After: 1` | như N1 | Không |

**Ví dụ**

```http
POST /auth/newuser HTTP/1.1
Content-Type: application/json

{ "firstName": "Bình", "lastName": "Trần", "email": "binh@example.org", "password": "mat-khau-dai-123", "projectId": "new", "recaptchaToken": "03AGdBq25...", "recaptchaSiteKey": "6LdExampleSiteKey" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "emailVerificationRequired": false }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "issue": [ { "severity": "error", "code": "invalid", "details": { "text": "Email already registered" }, "diagnostics": "Email already registered", "expression": ["email"] } ] }
```

**Lưu ý tích hợp:** phản hồi **không có code và không có cookie**; bước kế tiếp: `projectId:"new"` thì `POST /auth/newproject`; `projectId` của project mở thì `POST /auth/newpatient`. Phạm vi trùng email theo `projectId` (email có thể trùng giữa hai project). Email lưu chữ thường. Gửi `clientId` mà không `projectId` thì user vẫn **không** thuộc project nào. `Email already registered` cho phép dò email, được giảm nhẹ bằng hạn mức đăng nhập.

---

<a id="post-auth-preauthorize"></a>
## `POST /auth/preauthorize`

Admin project mint **mã pre-authorized** (dùng một lần) cho một thành viên khác của project, để đổi token ở `POST /oauth2/token` (grant `pre-authorized_code`). [Tham chiếu trường](../reference/auth.md#post-auth-preauthorize)

**Tiền điều kiện:** Bearer của admin project (membership `admin=true`, project không phải super admin). Header `X-Medplum-On-Behalf-Of` bắt buộc.

**Thứ tự kiểm tra:**
1. Bearer (N5), hạn mức chung (N1), JSON (N3). Thiếu Bearer luôn 401 dù body sai.
2. Validator **trước** kiểm quyền, gom: `clientId` UUID (`Client ID is required`), `scope` nếu không falsy phải là chuỗi, `nonce` như vậy, `expiresIn` nếu có phải là số nguyên 1 đến 604800.
3. Người gọi không đủ thông tin: 401 `Unauthorized`. Super admin hoặc không phải admin: 403 `Forbidden`.
4. Thiếu header: 400 `Pre-authorization requires onBehalfOfMembership`.
5. Tìm membership đích (`ProjectMembership/<id>` hoặc tham chiếu profile): không tồn tại, khác project, hoặc không hoạt động đều là 403 `Forbidden` (không lộ tồn tại).
6. Scope: mặc định `openid` cộng SMART scope của người gọi; người gọi bị giới hạn SMART chỉ mint trong phạm vi của mình, sai là 400 `Invalid scope`.
7. Đọc client (404); client không thuộc project của người gọi: 404.
8. **Ghi:** tạo Login `authMethod=pre-authorized` cho thành viên đích, lưu **SHA-256** của mã, hạn `expiresIn` (mặc định 3600 giây).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | `Cache-Control: no-store`, `Pragma: no-cache` | `{ preAuthorizedCode, expiresAt }` (mã chỉ trả **một lần**) | Tạo Login `pre-authorized` (mã băm SHA-256) |
| C2 | Lỗi nhập liệu | Một hoặc nhiều | 400 | - | `Client ID is required` (`clientId`); `Scope must be a string` (`scope`); `Nonce must be a string` (`nonce`); `expiresIn must be a positive integer not exceeding 604800 seconds` (`expiresIn`) | Không |
| C3 | Thiếu header | Không có `X-Medplum-On-Behalf-Of` | 400 | - | `Pre-authorization requires onBehalfOfMembership` | Không |
| C4 | Scope sai | Ngoài phạm vi người gọi | 400 | - | `Invalid scope`, `expression` `scope` | Không |
| C5 | Không đủ quyền | Không phải admin, hoặc super admin | 403 | - | `Forbidden` | Không |
| C6 | Đích không hợp lệ | Không tồn tại, project khác, không hoạt động | 403 | - | `Forbidden` | Không |
| C7 | Client không có | `clientId` UUID lạ, hoặc thuộc project khác | 404 | - | `Not found` | Không |
| C8 | Thiếu/sai Bearer | N5 | 401 | `WWW-Authenticate` | `Unauthorized` | Không |
| C9 | Ghi Login lỗi | Kho dữ liệu lỗi | 500 | - | `Internal server error` | Không có mã nào được phát |

**Ví dụ**

```http
POST /auth/preauthorize HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
X-Medplum-On-Behalf-Of: ProjectMembership/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55
Content-Type: application/json

{ "clientId": "a3b1c2d4-0000-4000-8000-000000000001", "scope": "openid", "expiresIn": 3600 }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Cache-Control: no-store
Pragma: no-cache

{ "preAuthorizedCode": "4f9c2e7a1b3d5f60a8c1e2d3b4a596874f9c2e7a1b3d5f60a8c1e2d3b4a59687", "expiresAt": "2026-11-24T11:15:00.000Z" }
```

**Lưu ý tích hợp:** mã chỉ hiển thị một lần; dùng lại mã ở `/oauth2/token` thu hồi cả token đã cấp (`Token already granted`). Token đổi được là của **thành viên đích**, không có refresh token. Validator chạy trước kiểm quyền nên người không phải admin vẫn thấy lỗi `400` nhập liệu trước `403`.

---

<a id="post-auth-profile"></a>
## `POST /auth/profile`

Chọn một ProjectMembership (profile) cho login đang dở rồi nhận LoginResult. [Tham chiếu trường](../reference/auth.md#post-auth-profile)

**Tiền điều kiện:** login có `memberships` (LoginResult dạng 5) hoặc `{login}`. Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator chỉ kiểm **có mặt** (`"login": ""` vẫn qua), gom: `Missing login`, `Missing profile`.
3. Đọc Login (404; id không phải UUID: chưa kiểm).
4. Đọc membership: **mọi lỗi** (không tồn tại, id sai dạng, lỗi đọc) thành `Profile not found`.
5. `SetLoginMembership` theo thứ tự: `Login revoked`; `Login granted`; `Login profile already set`; membership không thuộc user của login: `Invalid profile`; login đã gắn project thật mà membership thuộc project khác: `Invalid profile`; membership không có project: 404; đọc project (404); `Profile not active`/`Project not active`; `Google authentication is required` (feature `google-auth-required` mà login không phải google); launch (404); `SMART scopes are not supported for super admin logins`; AccessPolicy hỏng; `Missing patient context for patient/ scope`; `IP address not allowed`.
6. **Ghi:** gắn membership và project (so-sánh-và-đổi theo phiên bản; thua cuộc đua: `Login profile already set`).
7. Dựng LoginResult (dạng code ghi code/cookie băm; có thể ra MFA nếu project mới bắt buộc MFA).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, code | Chọn xong, MFA không chặn | 200 | `Set-Cookie: medplum-<clientId>=...` nếu login thuộc client | `LoginResult.code` | Gắn membership; phát code |
| C2 | Cần MFA | Login chưa xác minh MFA | 200 | - | `LoginResult.mfaRequired` / `.mfaEnrollRequired` (project vừa chọn có thể bắt buộc MFA) | Gắn membership (đã ghi) nhưng không phát code |
| C3 | Thiếu trường | `login`/`profile` không có trong body | 400 | - | `Missing login` (`login`), `Missing profile` (`profile`) | Không |
| C4 | Login không có | UUID lạ | 404 | - | `Not found` | Không |
| C5 | Profile không có | Membership không đọc được | 400 | - | `Profile not found` | Không |
| C6 | Login đã dùng | `Login revoked` / `Login granted` / `Login profile already set` | 400 | - | tương ứng | Không |
| C7 | Profile không hợp lệ | Membership của user khác hoặc project khác với project của login | 400 | - | `Invalid profile` | Không |
| C8 | Không hoạt động | Membership hoặc project tắt | 400 | - | `Profile not active` / `Project not active` | Không |
| C9 | Chính sách/IP/SMART | `Google authentication is required`; `IP address not allowed`; `SMART scopes are not supported for super admin logins`; `Missing patient context for patient/ scope`; `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` | 400 | - | tương ứng | Không |
| C10 | Fail đóng MFA | MFA hỏng | 500 | - | `Internal server error` | Membership có thể đã gắn; không trả code |
| C11 | Hạn mức đăng nhập | N1 | 429 | `Retry-After: 1` | như N1 | Không |

**Ví dụ**

```http
POST /auth/profile HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "profile": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687" }
```

**Lưu ý tích hợp:** `profile` là id của **ProjectMembership** (`memberships[].id`), không phải id Practitioner. **Không idempotent**: gọi lần hai là `Login profile already set`. Với người dùng nhiều project, MFA theo project chỉ lộ ra ở bước này.

---

<a id="post-auth-resetpassword"></a>
## `POST /auth/resetpassword`

Yêu cầu thư đặt lại mật khẩu; **không bao giờ lộ** email có tài khoản hay không. [Tham chiếu trường](../reference/auth.md#post-auth-resetpassword)

**Tiền điều kiện:** gửi thư thật cần SMTP và `AUTH_APP_BASE_URL` (link `<AppBaseURL>/setpassword/<id>/<secret>`); thiếu thì vẫn 200 nhưng không gửi (chỉ log). reCAPTCHA khi cấu hình. Chỉ hạn mức chung.

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1), JSON (N3).
2. Validator gom: `email` hợp lệ, `projectId` (nếu không falsy phải là UUID).
3. reCAPTCHA (không kiểm project): `Invalid recaptchaSiteKey`, `Invalid recaptchaSecretKey`, `Recaptcha token is required`, `Recaptcha failed`.
4. `sendEmail`: chỉ JSON `false` mới tắt gửi (mọi giá trị khác, kể cả `0`, vẫn gửi).
5. Email thuộc domain đăng nhập ngoài: 400 (đây là chỗ duy nhất báo lộ domain).
6. Tìm user **đúng phạm vi**: có `projectId` thì chỉ user của project đó; không có thì chỉ user toàn cục. Không có user: 200 im lặng.
7. `sendEmail=false`: 200, không tạo request. Thiếu SMTP/`AUTH_APP_BASE_URL`: 200 im lặng.
8. **Ghi:** vô hiệu các request `reset` chưa dùng của user, tạo UserSecurityRequest mới (lưu SHA-256 của secret, hạn **1 giờ**).
9. Gửi thư; lỗi gửi chỉ log, vẫn 200.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Có tài khoản, đã gửi | User tồn tại, thư cấu hình | 200 | - | OperationOutcome `All OK` | Vô hiệu request cũ; tạo request mới (1 giờ); gửi thư |
| C2 | Không có tài khoản | Email lạ | 200 | - | `All OK` giống hệt C1 | Không |
| C3 | Im lặng khác | `sendEmail:false`; thiếu SMTP/`AUTH_APP_BASE_URL`; lỗi gửi thư | 200 | - | `All OK` | Không gửi (C3 lỗi gửi: request đã tạo, thư không đến) |
| C4 | Email sai | Không hợp lệ/thiếu | 400 | - | `Valid email address is required`, `expression` `email` | Không |
| C5 | projectId sai | Có giá trị không phải UUID | 400 | - | `Invalid projectId`, `expression` `projectId` | Không |
| C6 | reCAPTCHA | khóa lạ / thiếu token / sai | 400 | - | `Invalid recaptchaSiteKey` / `Invalid recaptchaSecretKey` / `Recaptcha token is required` / `Recaptcha failed` | Không gửi thư |
| C7 | Domain đăng nhập ngoài | Email thuộc domain có IdP | 400 | - | `Cannot reset password for external auth. Contact your system administrator.` | Không |
| C8 | Fail đóng | reCAPTCHA lỗi/không lắp; lỗi ghi request | 500 | - | `Internal server error` | Không |
| C9 | JSON/lớn/hạn mức | N1, N3, N4 | như N | như N | như N | Không |

**Ví dụ**

```http
POST /auth/resetpassword HTTP/1.1
Content-Type: application/json

{ "email": "an@example.org", "projectId": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a11", "redirectUri": "https://app.example.org/setpassword" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** user thuộc project **phải** gửi `projectId`, nếu không route chỉ tìm user toàn cục và (im lặng) không gửi gì. 200 không có nghĩa là thư đã gửi. Link trong thư dùng một lần, hết hạn sau 1 giờ; gọi lại route này vô hiệu link cũ. Email chuẩn hóa chữ thường.

---

<a id="post-auth-revoke"></a>
## `POST /auth/revoke`

Thu hồi một phiên đăng nhập (login) của **chính mình** theo `loginId`. [Tham chiếu trường](../reference/auth.md#post-auth-revoke)

**Tiền điều kiện:** Bearer của login do `User`, không SMART scope.

**Thứ tự kiểm tra:**
1. Bearer (N5), hạn mức chung (N1), JSON (N3).
2. `loginId` phải là UUID: `Login ID is required.` (chú ý dấu chấm cuối).
3. Chủ thể phải là `User` và không SMART: 403 `Forbidden`.
4. Đọc Login (404); login của user khác: 404 `Not found` (không lộ tồn tại).
5. Phiên gọi không phải password/google: login đích phải thuộc membership trong project hiện tại, không thì 404.
6. **Ghi:** đặt `revoked=true` (đã thu hồi sẵn thì không ghi, vẫn 200).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Login của chính mình | 200 | - | OperationOutcome `All OK` | Login `revoked`; token của login đó chết ngay (kể cả chính phiên đang gọi) |
| C2 | Đã thu hồi sẵn | Login đã `revoked` | 200 | - | `All OK` | Không ghi |
| C3 | loginId sai | Thiếu, không phải UUID | 400 | - | `Login ID is required.`, `expression` `loginId` | Không |
| C4 | Không được phép | ClientApplication, SMART | 403 | - | `Forbidden` | Không |
| C5 | Login không có / của người khác | UUID lạ, user khác, hoặc ngoài project (phiên không phải password/google) | 404 | - | `Not found` | Không |
| C6 | Bearer | N5 | 401 | `WWW-Authenticate` | `Unauthorized` | Không |

**Ví dụ**

```http
POST /auth/revoke HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
Content-Type: application/json

{ "loginId": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** idempotent (gọi lại vẫn 200). Danh sách `loginId` lấy từ `security.sessions[].id` của `GET /auth/me`. Thu hồi login của chính phiên đang dùng là cách đăng xuất (token sau đó 401).

---

<a id="post-auth-scope"></a>
## `POST /auth/scope`

Thu hẹp scope của login đã có membership rồi nhận LoginResult. [Tham chiếu trường](../reference/auth.md#post-auth-scope)

**Tiền điều kiện:** login đã có membership (đã qua `/auth/profile` hoặc đăng nhập một membership). Hạn mức đăng nhập.

**Thứ tự kiểm tra:**
1. Hạn mức đăng nhập (N1), JSON (N3).
2. Validator chỉ kiểm **có mặt**, gom: `Missing login`, `Missing scope`.
3. Đọc Login (404; id không phải UUID: chưa kiểm).
4. Theo thứ tự: `Login revoked`; `Login granted`; `Login profile not set` (chưa có membership).
5. Scope mới phải thu hẹp: login có SMART scope mà scope mới bỏ hết SMART, hoặc term không SMART ngoài scope cũ, hoặc SMART term không được scope cũ che phủ: `Invalid scope`.
6. **Ghi:** cập nhật `scope` lên bản mới nhất của Login (giữ thu hồi xảy ra song song).
7. Dựng LoginResult (dạng code **phát code mới**, code cũ mất hiệu lực).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Scope hợp lệ | 200 | `Set-Cookie: medplum-<clientId>=...` nếu login thuộc client | `LoginResult.code` (hoặc dạng MFA nếu còn thiếu MFA) | Ghi `scope`; **code mới**, cookie mới |
| C2 | Thiếu trường | `login`/`scope` không có | 400 | - | `Missing login` (`login`), `Missing scope` (`scope`) | Không |
| C3 | Login không có | UUID lạ | 404 | - | `Not found` | Không |
| C4 | Login đã dùng | `Login revoked` / `Login granted` | 400 | - | tương ứng | Không |
| C5 | Chưa chọn profile | Chưa có membership | 400 | - | `Login profile not set` | Không |
| C6 | Scope mở rộng | Không phải tập con | 400 | - | `Invalid scope` | Không |
| C7 | Fail đóng MFA | MFA hỏng | 500 | - | `Internal server error` | Không |
| C8 | Hạn mức đăng nhập | N1 | 429 | `Retry-After: 1` | như N1 | Không |

**Ví dụ**

```http
POST /auth/scope HTTP/1.1
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "scope": "openid" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{ "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66", "code": "00112233445566778899aabbccddeeff" }
```

**Lưu ý tích hợp:** chỉ **thu hẹp**, thường dùng sau `/auth/profile` khi người dùng đồng ý ít quyền hơn. Mỗi lần thành công trả **code mới**: dùng code của lần gọi cuối, không dùng code của `/auth/profile`.

---

<a id="post-auth-setpassword"></a>
## `POST /auth/setpassword`

Đặt mật khẩu mới bằng link trong thư (`id` và `secret` từ `/auth/resetpassword` hoặc thư mời). [Tham chiếu trường](../reference/auth.md#post-auth-setpassword)

**Tiền điều kiện:** không (công khai, chỉ hạn mức chung).

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1), JSON (N3).
2. Validator gom: `id` phải UUID (`Invalid request ID`), `secret` không rỗng (`Missing secret`), `password` 8 ký tự đến 72 byte.
3. Đọc UserSecurityRequest (404).
4. Theo thứ tự: đã dùng `Already used`; là loại `verify-email` `Invalid request type`; hết hạn `Expired`; secret sai `Incorrect secret` (so băm SHA-256).
5. Đọc User (404).
6. Kiểm mật khẩu rò rỉ (400 hoặc fail đóng 500) **trước** khi tiêu thụ request (lỗi này không làm mất link); kiểm 72 byte.
7. **Transaction:** tiêu thụ request (so-sánh-và-đổi; thua đua: `Already used`), vô hiệu mọi request chưa dùng không phải `verify-email`, ghi băm mật khẩu, đặt `emailVerified=true`.
8. Sau transaction: thu hồi **mọi login** của user (không nằm trong transaction).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | Mọi bước qua | 200 | - | OperationOutcome `All OK` | Request thành đã dùng; đổi mật khẩu; `emailVerified=true`; thu hồi mọi login |
| C2 | Lỗi nhập liệu | Một hoặc nhiều | 400 | - | `Invalid request ID` (`id`); `Missing secret` (`secret`); `Password must be at least 8 characters` / `Password must be no more than 72 characters` (`password`) | Không |
| C3 | Request không có | `id` UUID lạ | 404 | - | `Not found` | Không |
| C4 | Link đã dùng | - | 400 | - | `Already used` | Không |
| C5 | Sai loại | Link xác minh email | 400 | - | `Invalid request type` | Không |
| C6 | Hết hạn | Quá 1 giờ (reset) | 400 | - | `Expired` | Không |
| C7 | Sai secret | - | 400 | - | `Incorrect secret` | Không |
| C8 | Mật khẩu rò rỉ | HIBP > 0 | 400 | - | `Password found in breach database` (không `expression`) | Không tiêu thụ request |
| C9 | Fail đóng | HIBP lỗi; lỗi giữa transaction | 500 | - | `Internal server error` | Request không bị tiêu thụ |
| C10 | Hạn mức/JSON | N1, N3, N4 | như N | như N | như N | Không |

**Ví dụ**

```http
POST /auth/setpassword HTTP/1.1
Content-Type: application/json

{ "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a99", "secret": "9f86d081884c7d659a2feaa0c55ad015", "password": "mat-khau-moi-456" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** sau thành công người dùng phải đăng nhập lại (mọi phiên chết). Link dùng một lần. Không có bộ đếm thử `secret` riêng (chỉ hạn mức IP chung), nhưng secret là 32 ký tự hex ngẫu nhiên.

---

<a id="post-auth-verifyemail"></a>
## `POST /auth/verifyemail`

Xác minh email bằng link trong thư (`id` và `secret`); nếu request có lưu `redirectUri` thì trả kèm header `Location`. [Tham chiếu trường](../reference/auth.md#post-auth-verifyemail)

**Tiền điều kiện:** không (công khai, chỉ hạn mức chung). Link do `POST /auth/newuser` (khi `emailVerificationRequired`) hoặc `POST /auth/newproject` gửi.

**Thứ tự kiểm tra:**
1. Hạn mức chung (N1), JSON (N3).
2. Validator gom: `Invalid request ID` (`id`), `Missing secret` (`secret`).
3. Đọc UserSecurityRequest (404).
4. Theo thứ tự (khác `setpassword`): **loại trước**: không phải `verify-email`: `Invalid user security request type`; đã dùng `Already used`; hết hạn (7 ngày) `Expired`; secret sai `Incorrect secret`.
5. `redirectUri` đã lưu mà không phải URL đầy đủ: `Invalid redirectUri` (**trước** khi tiêu thụ, link không bị mất).
6. **Transaction:** tiêu thụ request và đặt `emailVerified=true` (idempotent).

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công, có redirect | Request có `redirectUri` | 200 (không phải 302) | `Location: <redirectUri>` | OperationOutcome `All OK` | Request đã dùng; `emailVerified=true` |
| C2 | Thành công, không redirect | Không có `redirectUri` | 200 | - | `All OK` | Như C1 |
| C3 | Lỗi nhập liệu | `id` không UUID / thiếu secret | 400 | - | `Invalid request ID` (`id`); `Missing secret` (`secret`) | Không |
| C4 | Request không có | UUID lạ | 404 | - | `Not found` | Không |
| C5 | Sai loại | Link đặt lại mật khẩu | 400 | - | `Invalid user security request type` | Không |
| C6 | Đã dùng | - | 400 | - | `Already used` | Không |
| C7 | Hết hạn | Quá 7 ngày | 400 | - | `Expired` | Không |
| C8 | Sai secret | - | 400 | - | `Incorrect secret` | Không |
| C9 | Redirect hỏng | `redirectUri` đã lưu thiếu scheme/host | 400 | - | `Invalid redirectUri` | **Không** tiêu thụ request |
| C10 | Hạn mức/JSON/lỗi nội bộ | N1, N3, N4, N6 | như N | như N | như N | Không |

**Ví dụ**

```http
POST /auth/verifyemail HTTP/1.1
Content-Type: application/json

{ "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a98", "secret": "2c26b46b68ffc68ff99b453c1d304134" }
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
Location: https://app.example.org/register?login=0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66

{ "resourceType": "OperationOutcome", "id": "ok", "issue": [ { "severity": "information", "code": "informational", "details": { "text": "All OK" } } ] }
```

**Lưu ý tích hợp:** `Location` đi cùng **200**, FE tự chuyển hướng (đọc header, trình duyệt không tự đi). Chuyển hướng về `.../register?login=<login>` để tiếp tục `POST /auth/newproject` với login đó. Link chỉ dùng một lần: tải lại trang cho `Already used` dù email đã được xác minh.

---

## Phát hiện lệch với common.md

1. **Dòng 39 (bảng "Header phản hồi", `Cache-Control`)** và dòng đầu `reference/auth.md` ("Header phản hồi chung"): ghi `Cache-Control: no-store`. Theo mã (`pkg/httpsec/httpsec.go` dòng 37) middleware đặt **`Cache-Control: no-store, no-cache, must-revalidate`** cùng `Pragma: no-cache` (và `Permissions-Policy`, `X-XSS-Protection: 1; mode=block`) cho mọi route; test `TestHTTPResponsesAreNotCacheableAndAreSecured` khẳng định đúng chuỗi này. Chỉ các phản hồi mà handler tự đặt lại (`/auth/exchange`, `/auth/preauthorize`, token endpoint, `admin` một số) mới là `no-store`.
2. **Dòng 181 (hạn mức đăng nhập)** liệt kê "`newuser`, `newproject`" nhưng không nêu rõ `exchange` và `GET /auth/external*`, `/auth/google` đã gắn hạn mức này (có, đúng theo mã) và `newpatient` **không** thuộc (đúng theo mã, test `TestHTTPNewPatientIsNotInTheLoginRateLimit`); điểm này khớp, chỉ lưu ý đọc kỹ danh sách ở đầu tệp này.
3. **Dòng 135 và mục 3 (429 MFA)**: "thông điệp cụ thể của từng route ở cases": theo mã không có thông điệp riêng theo route, mọi 429 (IP, bộ đếm lượt thử, bộ đếm gửi mã, 429 của IdP) đều là `Too many requests`. Riêng 429 của IdP trong callback/exchange có `Retry-After: 0` (giá trị `RetryAfter = 0`), khác "giây còn lại" của hạn mức MFA.
4. **Dòng 53 (`auth-external-state`, "Hết hạn: TTL của state")**: đúng; bổ sung theo mã: cookie bị **xóa ở mọi phản hồi của callback** (kể cả lỗi), `Max-Age` mặc định 600 giây (`AUTH_EXTERNAL_STATE_TTL`).
5. **Dòng 62 (`medplum-<clientId>` đặt bởi "... `google`, `newproject`, `newpatient`...")**: không liệt kê nhưng theo mã callback `GET /auth/external[/{clientId}]` **không** đặt cookie này (chỉ 302); `POST /auth/exchange` cũng không đặt (trả token).
6. **auth-dto.md (tài liệu cũ, không phải common.md)**: mục "Đăng nhập" ghi lỗi `"Login already granted"`/`"Login revoked"` cho `GET /auth/login/{login}` (đúng); mục `POST /auth/revoke` ghi "Login không thuộc user gọi hoặc ngoài project: `404`" (đúng) nhưng bỏ sót `403 Forbidden` cho ClientApplication/SMART. Mục `POST /auth/preauthorize` ghi "header on-behalf-of không hợp lệ là 403" (đúng) và lỗi `404` client; mục "Việc còn lại" nói `external.ts` "chưa đọc" đã lỗi thời (callback đã cài P6).
