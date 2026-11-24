# Tham chiếu API: nhóm `auth`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/auth.md](../cases/auth.md).

Đăng ký, đăng nhập nhiều bước (login, profile, scope), MFA, mật khẩu, xác minh email, đăng nhập ngoài (IdP, Google), exchange và preauthorize. Công khai trừ khi route ghi Bearer.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`POST /auth/changepassword`](#post-auth-changepassword) | Đổi mật khẩu của chính mình |
| [`GET /auth/clientinfo/{clientId}`](#get-auth-clientinfo-clientid) | Thông tin công khai của form đăng nhập của một client |
| [`POST /auth/exchange`](#post-auth-exchange) | Đổi access token của IdP ngoài lấy token của auth (deprecated) |
| [`GET /auth/external`](#get-auth-external) | Callback đăng nhập qua identity provider ngoài (OIDC) |
| [`POST /auth/external/start`](#post-auth-external-start) | Bắt đầu đăng nhập qua identity provider RIÊNG của một client |
| [`GET /auth/external/{clientId}`](#get-auth-external-clientid) | Callback đăng nhập qua identity provider ngoài (OIDC) |
| [`POST /auth/google`](#post-auth-google) | Đăng nhập bằng Google Identity Services |
| [`POST /auth/login`](#post-auth-login) | Đăng nhập bằng email và mật khẩu |
| [`GET /auth/login/{login}`](#get-auth-login-login) | Trạng thái một login đang dở |
| [`GET /auth/me`](#get-auth-me) | Thông tin người đang đăng nhập |
| [`POST /auth/method`](#post-auth-method) | Cách đăng nhập của một email |
| [`POST /auth/mfa/disable`](#post-auth-mfa-disable) | Tắt MFA (một phương thức hoặc tất cả) |
| [`POST /auth/mfa/enroll`](#post-auth-mfa-enroll) | Đăng ký thêm một phương thức MFA cho chính mình |
| [`POST /auth/mfa/login-enroll`](#post-auth-mfa-login-enroll) | Đăng ký MFA ngay trong lúc đăng nhập |
| [`POST /auth/mfa/send-email`](#post-auth-mfa-send-email) | Gửi (lại) mã MFA qua email cho login đang chờ |
| [`POST /auth/mfa/send-email-challenge`](#post-auth-mfa-send-email-challenge) | Gửi mã email cho phiên đang dùng |
| [`GET /auth/mfa/status`](#get-auth-mfa-status) | Trạng thái MFA của chính mình |
| [`POST /auth/mfa/verify`](#post-auth-mfa-verify) | Xác minh mã MFA khi đăng nhập |
| [`POST /auth/newpatient`](#post-auth-newpatient) | Bệnh nhân tự đăng ký vào project |
| [`POST /auth/newproject`](#post-auth-newproject) | Tạo project mới cho người vừa đăng ký |
| [`POST /auth/newuser`](#post-auth-newuser) | Đăng ký tài khoản |
| [`POST /auth/preauthorize`](#post-auth-preauthorize) | Mint pre-authorized code cho một thành viên khác của project |
| [`POST /auth/profile`](#post-auth-profile) | Chọn profile (ProjectMembership) cho login |
| [`POST /auth/resetpassword`](#post-auth-resetpassword) | Yêu cầu thư đặt lại mật khẩu |
| [`POST /auth/revoke`](#post-auth-revoke) | Thu hồi một phiên đăng nhập của chính mình |
| [`POST /auth/scope`](#post-auth-scope) | Thu hẹp scope của login |
| [`POST /auth/setpassword`](#post-auth-setpassword) | Đặt mật khẩu bằng link trong thư |
| [`POST /auth/verifyemail`](#post-auth-verifyemail) | Xác minh email bằng link trong thư |

---

<a id="post-auth-changepassword"></a>
## `POST /auth/changepassword`

**Đổi mật khẩu của chính mình**

Thu hồi MỌI phiên đăng nhập của user, kể cả phiên đang dùng.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-changepassword](../cases/auth.md#post-auth-changepassword)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Mật khẩu cũ và mới)

Schema: `httpapi.ChangePasswordBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `newPassword` | string | có |  | Mật khẩu mới; bắt buộc, 8 ký tự trở lên và không quá 72 byte | `"mat-khau-moi-123"` |
| `oldPassword` | string | có |  | Mật khẩu hiện tại; bắt buộc, không rỗng | `"mat-khau-cu-123"` |

```json
{
  "newPassword": "mat-khau-moi-123",
  "oldPassword": "mat-khau-cu-123"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-auth-clientinfo-clientid"></a>
## `GET /auth/clientinfo/{clientId}`

**Thông tin công khai của form đăng nhập của một client**

Không cần xác thực. redirect_uri (nếu gửi) phải khớp chính xác một URI đã đăng ký của client. Toàn bộ signInForm là công khai.

Các trường hợp phản hồi: [../cases/auth.md#get-auth-clientinfo-clientid](../cases/auth.md#get-auth-clientinfo-clientid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `clientId` | đường dẫn | string | có | id client |
| `redirect_uri` | query | string | không | URI chuyển hướng |

**Phản hồi**

**`200`** OK

Schema: `fhir.ClientApplicationSignInForm`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `logo` | object `fhir.Attachment` | không |  | logo |  |
| `showScopeSelection` | boolean | không |  | showScopeSelection |  |
| `welcomeString` | string | không |  | welcomeString |  |

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-exchange"></a>
## `POST /auth/exchange`

**Đổi access token của IdP ngoài lấy token của auth (deprecated)**

Vỏ mỏng của grant urn:ietf:params:oauth:grant-type:token-exchange. Trả BODY TOKEN (access_token, id_token, refresh_token...), KHÔNG phải LoginResult như tài liệu cũ ghi. IdP cấu hình ở máy chủ bắt buộc có membershipId. Lỗi validator trả OperationOutcome 400; lỗi IdP và danh tính trả {error, error_description}; lỗi đăng nhập (user không có...) trả OperationOutcome.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-exchange](../cases/auth.md#post-auth-exchange)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Token của IdP và client)

Schema: `httpapi.ExchangeBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `clientId` | string | có |  | Id ClientApplication cấu hình IdP; bắt buộc, không rỗng | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `externalAccessToken` | string | có |  | Access token do IdP ngoài cấp (subject token); bắt buộc, không rỗng | `"ya29.a0AfH6SMB..."` |
| `membershipId` | string | không |  | Id ProjectMembership đích; bắt buộc với IdP cấu hình ở máy chủ, tùy chọn với trường hợp khác | `"a3b1c2d4-0000-4000-8000-000000000001"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000000",
  "externalAccessToken": "ya29.a0AfH6SMB...",
  "membershipId": "a3b1c2d4-0000-4000-8000-000000000001"
}
```

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `httpapi.TokenErrorBody` (cấu trúc dùng chung, xem [common.md mục 4](../common.md#4-lỗi-oauth2)).

---

<a id="get-auth-external"></a>
## `GET /auth/external`

**Callback đăng nhập qua identity provider ngoài (OIDC)**

IdP chuyển trình duyệt về đây với code và state. state phải do /auth/method phát (đã ký) và trình duyệt phải mang cookie auth-external-state tương ứng. Thành công: 302 tới redirectUri của client kèm login và code, hoặc về trang đăng nhập của app kèm login. Lỗi trả OperationOutcome JSON 400 (không chuyển hướng). /auth/external/{clientId} là callback riêng của client có identityProvider.

Các trường hợp phản hồi: [../cases/auth.md#get-auth-external](../cases/auth.md#get-auth-external)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `code` | query | string | có | Authorization code của IdP |
| `state` | query | string | có | state do /auth/method phát |
| `iss` | query | string | không | Issuer (RFC 9207) |

**Phản hồi**

**`302`** Found

Không có body.

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-external-start"></a>
## `POST /auth/external/start`

**Bắt đầu đăng nhập qua identity provider RIÊNG của một client**

Với client có identityProvider (callback /auth/external/{clientId}). Trả {authorizeUrl} đã kèm state do auth ký và đặt cookie liên kết trình duyệt auth-external-state; app chuyển người dùng tới authorizeUrl. Đăng nhập theo tên miền email dùng /auth/method.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-external-start](../cases/auth.md#post-auth-external-start)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Client và tham số đăng nhập)

Schema: `httpapi.ExternalStartBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `clientId` | string | có |  | Id ClientApplication có identityProvider riêng; bắt buộc, không rỗng | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `codeChallenge` | string | không |  | Mã thách thức PKCE; ghi vào state do auth ký | `"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` |
| `codeChallengeMethod` | string | không | một trong: `plain`, `S256` | Phương thức PKCE: plain hoặc S256 | `"S256"` |
| `launch` | string | không |  | Id SMART launch; ghi vào state do auth ký | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `nonce` | string | không |  | Nonce OIDC; ghi vào state do auth ký | `"c1a9f0e2"` |
| `projectId` | string | không |  | Id project; ghi vào state do auth ký | `"a3b1c2d4-0000-4000-8000-000000000001"` |
| `redirectUri` | string | không |  | URI chuyển hướng của client; ghi vào state do auth ký | `"https://app.example.org/callback"` |
| `returnTo` | string | không |  | Trang của app để quay về sau đăng nhập ngoài; ghi vào state do auth ký | `"https://app.example.org/signin"` |
| `scope` | string | không |  | Scope OAuth; ghi vào state do auth ký | `"openid"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000000",
  "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  "codeChallengeMethod": "S256",
  "launch": "a3b1c2d4-0000-4000-8000-000000000000",
  "nonce": "c1a9f0e2",
  "projectId": "a3b1c2d4-0000-4000-8000-000000000001",
  "redirectUri": "https://app.example.org/callback",
  "returnTo": "https://app.example.org/signin",
  "scope": "openid"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.MethodResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `authorizeUrl` | string | không |  | URL authorize của identity provider; vắng mặt nghĩa là đăng nhập bằng mật khẩu | `"https://idp.example.com/oauth2/authorize"` |
| `domain` | string | không |  | tên miền của email, chỉ có khi tên miền cấu hình identity provider ngoài | `"example.com"` |

```json
{
  "authorizeUrl": "https://idp.example.com/oauth2/authorize",
  "domain": "example.com"
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-auth-external-clientid"></a>
## `GET /auth/external/{clientId}`

**Callback đăng nhập qua identity provider ngoài (OIDC)**

IdP chuyển trình duyệt về đây với code và state. state phải do /auth/method phát (đã ký) và trình duyệt phải mang cookie auth-external-state tương ứng. Thành công: 302 tới redirectUri của client kèm login và code, hoặc về trang đăng nhập của app kèm login. Lỗi trả OperationOutcome JSON 400 (không chuyển hướng). /auth/external/{clientId} là callback riêng của client có identityProvider.

Các trường hợp phản hồi: [../cases/auth.md#get-auth-external-clientid](../cases/auth.md#get-auth-external-clientid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `clientId` | đường dẫn | string | có | Client có identityProvider riêng (chỉ ở route /auth/external/{clientId}; route /auth/external không có tham số này) |
| `code` | query | string | có | Authorization code của IdP |
| `state` | query | string | có | state do /auth/method phát |
| `iss` | query | string | không | Issuer (RFC 9207) |

**Phản hồi**

**`302`** Found

Không có body.

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-google"></a>
## `POST /auth/google`

**Đăng nhập bằng Google Identity Services**

Xác minh googleCredential (ID token RS256 của Google, aud là googleClientId) rồi đăng nhập. Credential phải báo email đã xác minh. Nếu tên miền của email có DomainConfiguration thì trả {domain, authorizeUrl} (kèm cookie state) thay vì LoginResult. createUser cho phép tạo user mới từ credential. Thông báo lỗi xác minh cố định (không lộ chi tiết).

Các trường hợp phản hồi: [../cases/auth.md#post-auth-google](../cases/auth.md#post-auth-google)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Credential của Google)

Schema: `httpapi.GoogleBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `googleClientId` | string | có |  | Client id Google (aud của credential); bắt buộc có mặt trong body | `"1234567890-abc.apps.googleusercontent.com"` |
| `googleCredential` | string | có |  | ID token (JWT RS256) của Google Identity Services; bắt buộc có mặt trong body | `"eyJhbGciOiJSUzI1NiIs..."` |
| `clientId` | string | không |  | Id ClientApplication OAuth; có thì project lấy từ client | `"a3b1c2d4-0000-4000-8000-000000000001"` |
| `codeChallenge` | string | không |  | Mã thách thức PKCE | `"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` |
| `codeChallengeMethod` | string | không | một trong: `plain`, `S256` | Phương thức PKCE: plain hoặc S256 | `"S256"` |
| `createUser` | boolean | không |  | true cho phép tạo user mới từ credential nếu chưa có; mặc định false | `false` |
| `nonce` | string | không |  | Nonce OIDC | `"c1a9f0e2"` |
| `projectId` | string | không |  | Id project đăng nhập | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `redirectUri` | string | không |  | URI chuyển hướng của client; nếu có phải là chuỗi | `"https://app.example.org/callback"` |
| `resourceType` | string | không |  | Loại hồ sơ profile cần đăng nhập (ví dụ Practitioner, Patient); lưu vào login làm profileType | `"Practitioner"` |
| `scope` | string | không |  | Scope OAuth yêu cầu, phân tách bằng khoảng trắng | `"openid"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000001",
  "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  "codeChallengeMethod": "S256",
  "createUser": false,
  "googleClientId": "1234567890-abc.apps.googleusercontent.com",
  "googleCredential": "eyJhbGciOiJSUzI1NiIs...",
  "nonce": "c1a9f0e2",
  "projectId": "a3b1c2d4-0000-4000-8000-000000000000",
  "redirectUri": "https://app.example.org/callback",
  "resourceType": "Practitioner",
  "scope": "openid"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-login"></a>
## `POST /auth/login`

**Đăng nhập bằng email và mật khẩu**

Trả đúng một trong các dạng LoginResult: code (đã chọn được profile), memberships (chọn profile), mfaRequired hoặc mfaEnrollRequired. Khi có code và login thuộc một client, đặt cookie medplum-<clientId>.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-login](../cases/auth.md#post-auth-login)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Thông tin đăng nhập)

Schema: `httpapi.LoginBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `email` | string | có |  | Email đăng nhập; bắt buộc, phải là địa chỉ email hợp lệ | `"an@example.org"` |
| `password` | string | có |  | Mật khẩu; bắt buộc, 8 ký tự trở lên và không quá 72 byte | `"mat-khau-dai-123"` |
| `clientId` | string | không |  | Id ClientApplication OAuth; có thì project được lấy từ client và cookie đăng nhập đặt theo client | `"a3b1c2d4-0000-4000-8000-000000000001"` |
| `codeChallenge` | string | không |  | Mã thách thức PKCE gắn vào login để kiểm ở bước đổi code | `"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` |
| `codeChallengeMethod` | string | không | một trong: `plain`, `S256` | Phương thức PKCE của codeChallenge: plain hoặc S256 | `"S256"` |
| `launch` | string | không |  | Id ngữ cảnh SMART launch (nếu có) | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `nonce` | string | không |  | Nonce OIDC; bỏ trống thì máy chủ tự sinh | `"c1a9f0e2"` |
| `projectId` | string | không |  | Project đăng nhập: "new" (chưa thuộc project, cho phép không có membership) hoặc UUID project; bỏ trống thì suy ra từ clientId (nếu gửi cả hai phải khớp project của client) | `"new"` |
| `redirectUri` | string | không |  | URI chuyển hướng của client sau khi đăng nhập; nếu có phải là chuỗi | `"https://app.example.org/callback"` |
| `remember` | boolean | không |  | true để kèm refresh token trong kết quả đăng nhập; mặc định false | `false` |
| `resourceType` | string | không |  | Loại hồ sơ profile cần đăng nhập (ví dụ Practitioner, Patient); lưu vào login làm profileType | `"Practitioner"` |
| `scope` | string | không |  | Scope OAuth yêu cầu, phân tách bằng khoảng trắng; mặc định "openid" | `"openid"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000001",
  "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  "codeChallengeMethod": "S256",
  "email": "an@example.org",
  "launch": "a3b1c2d4-0000-4000-8000-000000000000",
  "nonce": "c1a9f0e2",
  "password": "mat-khau-dai-123",
  "projectId": "new",
  "redirectUri": "https://app.example.org/callback",
  "remember": false,
  "resourceType": "Practitioner",
  "scope": "openid"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-auth-login-login"></a>
## `GET /auth/login/{login}`

**Trạng thái một login đang dở**

Trả LoginResult hiện tại của login (code, memberships, mfaRequired hoặc mfaEnrollRequired tùy bước đang dở) để app tiếp tục luồng đăng nhập. Id login phải là UUID, sai trả 400, không có login trả 404. Có code và login thuộc một client thì đặt cookie medplum-<clientId>.

Các trường hợp phản hồi: [../cases/auth.md#get-auth-login-login](../cases/auth.md#get-auth-login-login)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `login` | đường dẫn | string | có | Login id (UUID) |

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-auth-me"></a>
## `GET /auth/me`

**Thông tin người đang đăng nhập**

profile chỉ có resourceType và id (hồ sơ đầy đủ nằm ở fhir-server); accessPolicy chưa có ở bản này.

Các trường hợp phản hồi: [../cases/auth.md#get-auth-me](../cases/auth.md#get-auth-me)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `application.Me`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `config` | object `fhir.UserConfiguration` | có |  | cấu hình giao diện của người dùng |  |
| `membership` | object `fhir.ProjectMembership` | có |  | ProjectMembership của phiên |  |
| `profile` | object tự do | có |  | profile chỉ gồm resourceType và id (rỗng nếu membership không có profile) |  |
| `project` | object `fhir.Project` | có |  | Project của phiên (id, name, features, ...) |  |
| `security` | object `application.UserSecurity` | không |  | thông tin bảo mật (MFA, phiên, membership); chỉ có khi login không bị giới hạn bởi SMART resource scope |  |
| `security.memberships` | mảng `fhir.ProjectMembership` | có |  | các membership của người dùng |  |
| `security.mfaEnrolled` | boolean | có |  | người dùng đã đăng ký MFA | `false` |
| `security.mfaRequired` | boolean | có |  | project hiện tại bắt buộc MFA | `false` |
| `security.sessions` | mảng `application.Session` | có |  | các phiên đăng nhập của người dùng |  |
| `security.sessions[].authMethod` | string | có | một trong: `password`, `google`, `external`, `exchange`, `client`, `pre-authorized` | phương thức đăng nhập của phiên | `"password"` |
| `security.sessions[].id` | string | có |  | id của Login (UUID) đại diện cho phiên | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `security.sessions[].lastUpdated` | string | có | định dạng date-time | thời điểm cập nhật cuối của Login (meta.lastUpdated), RFC3339 | `"2026-11-23T10:15:00.000Z"` |
| `security.sessions[].browser` | string | không |  | trình duyệt suy ra từ user agent, nếu có | `"Chrome"` |
| `security.sessions[].os` | string | không |  | hệ điều hành suy ra từ user agent, nếu có | `"macOS"` |
| `security.sessions[].project` | object `fhir.Reference` | không |  | tham chiếu Project của phiên, nếu có |  |
| `security.sessions[].remoteAddress` | string | không |  | địa chỉ IP lúc đăng nhập, nếu có | `"203.0.113.10"` |
| `user` | object `fhir.User` | không |  | người dùng (id, email, identifier); chỉ có khi membership trỏ tới User |  |

```json
{
  "security": {
    "memberships": [],
    "mfaEnrolled": false,
    "mfaRequired": false,
    "sessions": [
      {
        "authMethod": "password",
        "browser": "Chrome",
        "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
        "lastUpdated": "2026-11-23T10:15:00.000Z",
        "os": "macOS",
        "remoteAddress": "203.0.113.10"
      }
    ]
  }
}
```

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-method"></a>
## `POST /auth/method`

**Cách đăng nhập của một email**

Trả {} nếu đăng nhập bằng mật khẩu; trả {domain, authorizeUrl} nếu tên miền email cấu hình identity provider ngoài. Khi đăng nhập ngoài đã bật, authorizeUrl đã kèm `state` do auth ký (chứa các tham số đăng nhập trong body) và phản hồi đặt cookie liên kết trình duyệt `auth-external-state`: app KHÔNG tự thêm state, chỉ chuyển người dùng tới authorizeUrl. Tên miền trả về là chữ thường.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-method](../cases/auth.md#post-auth-method)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Email)

Schema: `httpapi.MethodBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `email` | string | có |  | Email cần xác định cách đăng nhập; bắt buộc, phải hợp lệ | `"an@example.org"` |
| `clientId` | string | không |  | Id ClientApplication; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"a3b1c2d4-0000-4000-8000-000000000001"` |
| `codeChallenge` | string | không |  | Mã thách thức PKCE; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` |
| `codeChallengeMethod` | string | không | một trong: `plain`, `S256` | Phương thức PKCE: plain hoặc S256; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"S256"` |
| `launch` | string | không |  | Id SMART launch; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `nonce` | string | không |  | Nonce OIDC; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"c1a9f0e2"` |
| `projectId` | string | không |  | Id project; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `redirectUri` | string | không |  | URI chuyển hướng của client; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"https://app.example.org/callback"` |
| `returnTo` | string | không |  | Trang của app để quay về sau đăng nhập ngoài; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"https://app.example.org/signin"` |
| `scope` | string | không |  | Scope OAuth; chỉ ghi vào state ký khi đăng nhập ngoài đã bật | `"openid"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000001",
  "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  "codeChallengeMethod": "S256",
  "email": "an@example.org",
  "launch": "a3b1c2d4-0000-4000-8000-000000000000",
  "nonce": "c1a9f0e2",
  "projectId": "a3b1c2d4-0000-4000-8000-000000000000",
  "redirectUri": "https://app.example.org/callback",
  "returnTo": "https://app.example.org/signin",
  "scope": "openid"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.MethodResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `authorizeUrl` | string | không |  | URL authorize của identity provider; vắng mặt nghĩa là đăng nhập bằng mật khẩu | `"https://idp.example.com/oauth2/authorize"` |
| `domain` | string | không |  | tên miền của email, chỉ có khi tên miền cấu hình identity provider ngoài | `"example.com"` |

```json
{
  "authorizeUrl": "https://idp.example.com/oauth2/authorize",
  "domain": "example.com"
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-mfa-disable"></a>
## `POST /auth/mfa/disable`

**Tắt MFA (một phương thức hoặc tất cả)**

Phải chứng minh một yếu tố đang có bằng token (TOTP hoặc mã email). Không gỡ được yếu tố cuối khi MFA là bắt buộc. Gỡ TOTP thì secret cũ bị thay bằng secret ngẫu nhiên mới. Chịu hạn mức thử như /auth/mfa/verify.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-mfa-disable](../cases/auth.md#post-auth-mfa-disable)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (phương thức (tùy chọn) và token)

Schema: `httpapi.MFADisableBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `token` | string | có |  | mã chứng minh một yếu tố MFA đang có (TOTP hoặc mã email); rỗng là 400 "Missing token" | `"123456"` |
| `method` | string | không | một trong: `totp`, `email` | phương thức cần gỡ; bỏ trống là gỡ tất cả (cần là một yếu tố đã đăng ký, không thì 400) | `"totp"` |

```json
{
  "method": "totp",
  "token": "123456"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-mfa-enroll"></a>
## `POST /auth/mfa/enroll`

**Đăng ký thêm một phương thức MFA cho chính mình**

method=email: token là mã nhận từ /auth/mfa/send-email-challenge. method=totp (mặc định): token là mã của secret trong enrollUri của /auth/mfa/status. Cũng chịu hạn mức thử như /auth/mfa/verify (lệch có chủ đích so với Medplum).

Các trường hợp phản hồi: [../cases/auth.md#post-auth-mfa-enroll](../cases/auth.md#post-auth-mfa-enroll)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (phương thức và token)

Schema: `httpapi.MFAEnrollBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `token` | string | có |  | mã xác nhận: mã email (method=email) hoặc mã TOTP (method=totp); rỗng là 400 "Missing token" | `"123456"` |
| `method` | string | không | một trong: `totp`, `email` | phương thức cần đăng ký; giá trị khác "email" (hoặc bỏ trống) được hiểu là totp | `"totp"` |

```json
{
  "method": "totp",
  "token": "123456"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-mfa-login-enroll"></a>
## `POST /auth/mfa/login-enroll`

**Đăng ký MFA ngay trong lúc đăng nhập**

Dùng khi MFA bắt buộc mà user chưa đăng ký (phản hồi login có mfaEnrollRequired). method=email: đăng ký và gửi mã tới email (nhập mã qua /auth/mfa/verify). method=totp (mặc định): gửi token của secret trong enrollUri. Cùng hạn mức thử với /auth/mfa/verify.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-mfa-login-enroll](../cases/auth.md#post-auth-mfa-login-enroll)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login, phương thức và token)

Schema: `httpapi.MFALoginEnrollBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id login đang chờ đăng ký MFA; bắt buộc, rỗng là 400 | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `method` | string | không | một trong: `totp`, `email` | phương thức đăng ký; giá trị khác "email" (hoặc bỏ trống) được hiểu là totp | `"totp"` |
| `token` | string | không |  | mã TOTP của secret trong enrollUri; chỉ dùng với method=totp (bắt buộc khi đó), bỏ qua với email | `"123456"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000",
  "method": "totp",
  "token": "123456"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-mfa-send-email"></a>
## `POST /auth/mfa/send-email`

**Gửi (lại) mã MFA qua email cho login đang chờ**

Chỉ cho user đã đăng ký email MFA. Gửi lại ghi đè mã cũ; tối đa 5 lần mỗi login trong 20 phút.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-mfa-send-email](../cases/auth.md#post-auth-mfa-send-email)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login)

Schema: `httpapi.MFASendEmailBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id login đang chờ MFA cần gửi lại mã email; bắt buộc, rỗng là 400 | `"a3b1c2d4-0000-4000-8000-000000000000"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-mfa-send-email-challenge"></a>
## `POST /auth/mfa/send-email-challenge`

**Gửi mã email cho phiên đang dùng**

Để đăng ký email MFA hoặc thỏa yếu tố email khi tắt MFA. Tối đa 5 lần mỗi login trong 20 phút.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-mfa-send-email-challenge](../cases/auth.md#post-auth-mfa-send-email-challenge)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-auth-mfa-status"></a>
## `GET /auth/mfa/status`

**Trạng thái MFA của chính mình**

Danh sách phương thức đã đăng ký và được phép, project có bắt buộc MFA không, và (nếu chưa đăng ký TOTP) enrollUri cùng enrollQrCode. Secret trong enrollUri chỉ thành yếu tố khi chính phiên này xác nhận bằng mã.

Các trường hợp phản hồi: [../cases/auth.md#get-auth-mfa-status](../cases/auth.md#get-auth-mfa-status)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `application.MFAStatusResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `allowedMethods` | mảng string | có | mỗi phần tử một trong: `totp`, `email` | các phương thức MFA project cho phép | `["totp"]` |
| `enrolled` | boolean | có |  | người dùng đã đăng ký ít nhất một phương thức MFA | `false` |
| `enrolledMethods` | mảng string | có | mỗi phần tử một trong: `totp`, `email` | các phương thức MFA đã đăng ký | `["totp"]` |
| `mfaRequired` | boolean | có |  | project có bắt buộc MFA hay không | `false` |
| `email` | string | không |  | email của tài khoản (luôn được điền nếu user có email) | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi người dùng chưa đăng ký TOTP |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi người dùng chưa đăng ký TOTP | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |

```json
{
  "allowedMethods": [
    "totp"
  ],
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "enrolled": false,
  "enrolledMethods": [
    "totp"
  ],
  "mfaRequired": false
}
```

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-mfa-verify"></a>
## `POST /auth/mfa/verify`

**Xác minh mã MFA khi đăng nhập**

Mã TOTP hoặc mã email. Đúng thì login được đánh dấu đã xác minh và phản hồi như /auth/login (code, memberships...). Người dùng bắt buộc MFA chưa đăng ký mà nhập mã cho secret được mời của chính login này thì được đăng ký TOTP luôn. Mỗi lượt thử bị giới hạn: 5 mỗi login và 20 mỗi user trong 10 phút; lần sai thứ 5 thu hồi login; vượt hạn mức trả 429 kèm rate-limit-reset. Mã TOTP chỉ dùng một lần.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-mfa-verify](../cases/auth.md#post-auth-mfa-verify)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login và mã)

Schema: `httpapi.MFAVerifyBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id login đang chờ MFA (lấy từ phản hồi /auth/login); bắt buộc, rỗng là 400 | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `token` | string | có |  | mã TOTP (6 số) hoặc mã email MFA; bắt buộc, rỗng là 400 | `"123456"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000",
  "token": "123456"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-newpatient"></a>
## `POST /auth/newpatient`

**Bệnh nhân tự đăng ký vào project**

Dùng login từ /auth/newuser của một user thuộc project (projectId trong body của newuser). Project phải cho đăng ký mở (setting openPatientRegistration và có chính sách Patient). Tạo hồ sơ Patient và membership kèm chính sách đó, rồi trả code như /auth/login. Không có hạn mức riêng ngoài hạn mức chung của /auth/*.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-newpatient](../cases/auth.md#post-auth-newpatient)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login và projectId)

Schema: `httpapi.NewPatientBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | Id login từ /auth/newuser; bắt buộc, không rỗng | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `projectId` | string | có |  | Id project cho phép bệnh nhân tự đăng ký; bắt buộc, không rỗng | `"a3b1c2d4-0000-4000-8000-000000000001"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000",
  "projectId": "a3b1c2d4-0000-4000-8000-000000000001"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-newproject"></a>
## `POST /auth/newproject`

**Tạo project mới cho người vừa đăng ký**

Dùng login từ /auth/newuser. Tạo project (6 chính sách mặc định, client mặc định, hồ sơ Practitioner và membership admin) trong một transaction rồi trả code như /auth/login. Mọi kiểm tra trạng thái login (đã thu hồi, đã cấp code, đã có membership) chạy trước khi tạo project. Login còn thiếu bước MFA nhận phản hồi mfaRequired hoặc mfaEnrollRequired và chưa tạo gì.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-newproject](../cases/auth.md#post-auth-newproject)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login và tên project)

Schema: `httpapi.NewProjectBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | Id login từ /auth/newuser; bắt buộc, phải là UUID | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `projectName` | string | có |  | Tên project; bắt buộc, 4 đến 72 ký tự | `"Bệnh viện A"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000",
  "projectName": "Bệnh viện A"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-newuser"></a>
## `POST /auth/newuser`

**Đăng ký tài khoản**

Tạo user rồi tạo Login CHƯA có membership; phản hồi không có code (người dùng phải tạo hoặc chọn project/profile tiếp). reCAPTCHA kiểm trước cờ đăng ký, như Medplum. projectId là "new" (tạo project mới), id của project cho phép đăng ký mở, hoặc bỏ trống (user không thuộc project nào); chỉ gửi clientId thì user vẫn không thuộc project nào.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-newuser](../cases/auth.md#post-auth-newuser)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Thông tin đăng ký)

Schema: `httpapi.NewUserBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `email` | string | có |  | Email; bắt buộc, hợp lệ, dài 3 đến 72 ký tự | `"an@example.org"` |
| `firstName` | string | có |  | Tên; bắt buộc, 1 đến 128 ký tự | `"An"` |
| `lastName` | string | có |  | Họ; bắt buộc, 1 đến 128 ký tự | `"Nguyen"` |
| `password` | string | có |  | Mật khẩu; bắt buộc, 8 ký tự trở lên và không quá 72 byte | `"mat-khau-dai-123"` |
| `clientId` | string | không |  | Id ClientApplication; chỉ gửi clientId thì user vẫn không thuộc project nào | `"a3b1c2d4-0000-4000-8000-000000000001"` |
| `codeChallenge` | string | không |  | Mã thách thức PKCE ghi vào login vừa tạo | `"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` |
| `codeChallengeMethod` | string | không | một trong: `plain`, `S256` | Phương thức PKCE: plain hoặc S256 | `"S256"` |
| `nonce` | string | không |  | Nonce OIDC ghi vào login vừa tạo | `"c1a9f0e2"` |
| `projectId` | string | không |  | "new" (sẽ tạo project mới), id project cho đăng ký mở, hoặc bỏ trống (user không thuộc project nào) | `"new"` |
| `recaptchaSiteKey` | string | không |  | Site key reCAPTCHA tương ứng với token; chỉ cần khi bật reCAPTCHA | `"6LdExampleSiteKey"` |
| `recaptchaToken` | string | không |  | Token reCAPTCHA; chỉ cần khi máy chủ hoặc project bật reCAPTCHA | `"03AGdBq25..."` |
| `redirectUri` | string | không |  | URI chuyển hướng của client ghi vào login vừa tạo | `"https://app.example.org/callback"` |
| `remember` | boolean | không |  | true để kèm refresh token cho login vừa tạo; mặc định false | `false` |
| `scope` | string | không |  | Scope OAuth ghi vào login vừa tạo | `"openid"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000001",
  "codeChallenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  "codeChallengeMethod": "S256",
  "email": "an@example.org",
  "firstName": "An",
  "lastName": "Nguyen",
  "nonce": "c1a9f0e2",
  "password": "mat-khau-dai-123",
  "projectId": "new",
  "recaptchaSiteKey": "6LdExampleSiteKey",
  "recaptchaToken": "03AGdBq25...",
  "redirectUri": "https://app.example.org/callback",
  "remember": false,
  "scope": "openid"
}
```

**Phản hồi**

**`200`** OK

Schema: `httpapi.NewUserResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emailVerificationRequired` | boolean | có |  | true khi máy chủ yêu cầu email đã xác minh để tạo project (cấu hình RequireVerifiedEmailForProject) và projectId của yêu cầu là "new"; ngược lại false | `false` |
| `login` | string | có |  | Id login vừa tạo (chưa có membership), dùng cho /auth/newproject hoặc /auth/newpatient | `"a3b1c2d4-0000-4000-8000-000000000000"` |

```json
{
  "emailVerificationRequired": false,
  "login": "a3b1c2d4-0000-4000-8000-000000000000"
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-preauthorize"></a>
## `POST /auth/preauthorize`

**Mint pre-authorized code cho một thành viên khác của project**

Chỉ admin của project (không phải super admin). Header X-Medplum-On-Behalf-Of chỉ ProjectMembership/<id> hoặc tham chiếu profile của thành viên đích (cùng project). scope mặc định là openid cộng các scope SMART của người gọi; người gọi bị giới hạn SMART chỉ mint được trong phạm vi của mình. Mã chỉ trả MỘT lần (kho giữ SHA-256); đổi mã ở /oauth2/token (grant pre-authorized_code).

Các trường hợp phản hồi: [../cases/auth.md#post-auth-preauthorize](../cases/auth.md#post-auth-preauthorize)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `X-Medplum-On-Behalf-Of` | có | ProjectMembership/<id> hoặc profile của đích |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Client và tham số)

Schema: `httpapi.PreAuthorizeBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `clientId` | string | có |  | Id ClientApplication sẽ đổi mã; bắt buộc, phải là UUID | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `expiresIn` | integer | không | tối thiểu 1; tối đa 604800 | Thời hạn mã, tính bằng giây (số nguyên 1 đến 604800); bỏ trống thì dùng mặc định của máy chủ | `3600` |
| `nonce` | string | không |  | Nonce OIDC gắn vào mã; nếu có phải là chuỗi | `"c1a9f0e2"` |
| `scope` | string | không |  | Scope của mã; mặc định openid cộng các scope SMART của người gọi | `"openid"` |

```json
{
  "clientId": "a3b1c2d4-0000-4000-8000-000000000000",
  "expiresIn": 3600,
  "nonce": "c1a9f0e2",
  "scope": "openid"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.PreAuthorizeResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `expiresAt` | string | có | định dạng date-time | thời điểm hết hạn của mã, RFC3339 UTC (now + ttl giây) | `"2026-11-23T10:15:00.000Z"` |
| `preAuthorizedCode` | string | có |  | mã pre-authorized dùng một lần để đổi token; chỉ trả ở lần này (server chỉ lưu băm) | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |

```json
{
  "expiresAt": "2026-11-23T10:15:00.000Z",
  "preAuthorizedCode": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687"
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-profile"></a>
## `POST /auth/profile`

**Chọn profile (ProjectMembership) cho login**

Gắn một ProjectMembership đã chọn vào login đang dở (login và profile đều bắt buộc có mặt trong body), rồi trả LoginResult như /auth/login (thường có code). Membership không tồn tại trả 400 "Profile not found". Có code và login thuộc một client thì đặt cookie medplum-<clientId>.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-profile](../cases/auth.md#post-auth-profile)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login (id login đang dở) và profile (id ProjectMembership))

Schema: `httpapi.ProfileBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | Id login đang dở (từ LoginResult.login); bắt buộc có mặt trong body | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `profile` | string | có |  | Id ProjectMembership chọn làm profile; bắt buộc có mặt trong body (phải thuộc các membership của login) | `"a3b1c2d4-0000-4000-8000-000000000001"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000",
  "profile": "a3b1c2d4-0000-4000-8000-000000000001"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-resetpassword"></a>
## `POST /auth/resetpassword`

**Yêu cầu thư đặt lại mật khẩu**

Luôn trả 200 khi email hợp lệ, dù có tài khoản hay không và dù gửi thư có được hay không (không lộ tài khoản). Email thuộc tên miền đăng nhập ngoài trả 400.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-resetpassword](../cases/auth.md#post-auth-resetpassword)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Email cần đặt lại)

Schema: `httpapi.ResetPasswordBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `email` | string | có |  | Email cần đặt lại mật khẩu; bắt buộc, phải hợp lệ | `"an@example.org"` |
| `projectId` | string | không |  | Id project của user (UUID); bỏ trống nếu user không thuộc project nào | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `recaptchaSiteKey` | string | không |  | Site key reCAPTCHA tương ứng; chỉ cần khi bật reCAPTCHA | `"6LdExampleSiteKey"` |
| `recaptchaToken` | string | không |  | Token reCAPTCHA; chỉ cần khi máy chủ hoặc project bật reCAPTCHA | `"03AGdBq25..."` |
| `redirectUri` | string | không |  | URI app đính vào link trong thư đặt lại mật khẩu | `"https://app.example.org/setpassword"` |
| `sendEmail` | boolean | không |  | false để không gửi thư (chỉ tạo yêu cầu); mặc định true | `true` |

```json
{
  "email": "an@example.org",
  "projectId": "a3b1c2d4-0000-4000-8000-000000000000",
  "recaptchaSiteKey": "6LdExampleSiteKey",
  "recaptchaToken": "03AGdBq25...",
  "redirectUri": "https://app.example.org/setpassword",
  "sendEmail": true
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-revoke"></a>
## `POST /auth/revoke`

**Thu hồi một phiên đăng nhập của chính mình**

Cần Bearer token. Thu hồi một login (phiên) thuộc về chính người gọi theo loginId (UUID, bắt buộc); login của người khác hoặc không có trả lỗi 403/404. Thành công trả OperationOutcome "All OK".

Các trường hợp phản hồi: [../cases/auth.md#post-auth-revoke](../cases/auth.md#post-auth-revoke)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (loginId cần thu hồi)

Schema: `httpapi.RevokeBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `loginId` | string | có |  | Id login (phiên) của chính người gọi cần thu hồi; bắt buộc, phải là UUID | `"a3b1c2d4-0000-4000-8000-000000000000"` |

```json
{
  "loginId": "a3b1c2d4-0000-4000-8000-000000000000"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-scope"></a>
## `POST /auth/scope`

**Thu hẹp scope của login**

Đổi scope của login đang dở sang scope mới (login và scope đều bắt buộc có mặt trong body), rồi trả LoginResult như /auth/login. Scope mới chỉ được thu hẹp so với scope hiện có. Có code và login thuộc một client thì đặt cookie medplum-<clientId>.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-scope](../cases/auth.md#post-auth-scope)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (login và scope mới)

Schema: `httpapi.ScopeBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | Id login đang dở; bắt buộc có mặt trong body | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `scope` | string | có |  | Scope mới, phân tách bằng khoảng trắng; bắt buộc có mặt trong body và chỉ được thu hẹp | `"openid profile"` |

```json
{
  "login": "a3b1c2d4-0000-4000-8000-000000000000",
  "scope": "openid profile"
}
```

**Phản hồi**

**`200`** OK

Schema: `application.LoginResult`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `login` | string | có |  | id của Login (UUID); luôn có, dùng cho các bước tiếp theo | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66"` |
| `allowedMfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA project cho phép đăng ký; chỉ có khi mfaEnrollRequired | `["totp"]` |
| `code` | string | không |  | authorization code (hạn mặc định 10 phút); chỉ có ở dạng "code" và đi kèm cookie đăng nhập | `"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"` |
| `email` | string | không |  | email của người dùng; chỉ có khi mfaRequired | `"user@example.com"` |
| `enrollQrCode` | string | không |  | mã QR của enrollUri dạng data URI; chỉ có khi mfaEnrollRequired |  |
| `enrollUri` | string | không |  | otpauth URI để đăng ký TOTP; chỉ có khi mfaEnrollRequired | `"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` |
| `memberships` | mảng `application.MembershipChoice` | không |  | các profile để chọn khi người dùng có nhiều membership; chỉ có ở dạng "memberships" |  |
| `memberships[].id` | string | có |  | id của ProjectMembership (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55"` |
| `memberships[].identifier` | mảng `fhir.Identifier` | không |  | định danh của membership, nếu có |  |
| `memberships[].profile` | object `fhir.Reference` | không |  | tham chiếu profile (Practitioner/Patient/RelatedPerson) của membership |  |
| `memberships[].project` | object `fhir.Reference` | không |  | tham chiếu Project (display là tên hiện tại); vắng khi không đọc được project |  |
| `mfaEnrollRequired` | boolean | không |  | true ở dạng "mfaEnrollRequired": người dùng phải đăng ký MFA trước | `true` |
| `mfaMethods` | mảng string | không | mỗi phần tử một trong: `totp`, `email` | phương thức MFA người dùng đã đăng ký; chỉ có khi mfaRequired | `["totp"]` |
| `mfaRequired` | boolean | không |  | true ở dạng "mfaRequired": cần xác thực MFA trước khi có code | `true` |

```json
{
  "allowedMfaMethods": [
    "totp"
  ],
  "code": "4f9c2e7a1b3d5f60a8c1e2d3b4a59687",
  "email": "user@example.com",
  "enrollUri": "otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP",
  "login": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a66",
  "memberships": [
    {
      "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a55",
      "identifier": []
    }
  ],
  "mfaEnrollRequired": true,
  "mfaMethods": [
    "totp"
  ],
  "mfaRequired": true
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-setpassword"></a>
## `POST /auth/setpassword`

**Đặt mật khẩu bằng link trong thư**

Thu hồi mọi phiên đăng nhập của user. Link dùng được một lần.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-setpassword](../cases/auth.md#post-auth-setpassword)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (id và secret của link, mật khẩu mới)

Schema: `httpapi.SetPasswordBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `id` | string | có |  | Id yêu cầu (PasswordChangeRequest) lấy từ link trong thư; bắt buộc, phải là UUID | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `password` | string | có |  | Mật khẩu mới; bắt buộc, 8 ký tự trở lên và không quá 72 byte | `"mat-khau-moi-123"` |
| `secret` | string | có |  | Secret trong link thư; bắt buộc, không rỗng, dùng một lần | `"s3cr3t-from-email-link"` |

```json
{
  "id": "a3b1c2d4-0000-4000-8000-000000000000",
  "password": "mat-khau-moi-123",
  "secret": "s3cr3t-from-email-link"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-auth-verifyemail"></a>
## `POST /auth/verifyemail`

**Xác minh email bằng link trong thư**

Có redirectUri trong request thì trả 200 kèm header Location (không phải 302), như Medplum.

Các trường hợp phản hồi: [../cases/auth.md#post-auth-verifyemail](../cases/auth.md#post-auth-verifyemail)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (id và secret của link)

Schema: `httpapi.VerifyEmailBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `id` | string | có |  | Id yêu cầu xác minh lấy từ link trong thư; bắt buộc, phải là UUID | `"a3b1c2d4-0000-4000-8000-000000000000"` |
| `secret` | string | có |  | Secret trong link thư; bắt buộc, không rỗng | `"s3cr3t-from-email-link"` |

```json
{
  "id": "a3b1c2d4-0000-4000-8000-000000000000",
  "secret": "s3cr3t-from-email-link"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).
