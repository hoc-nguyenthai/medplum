# Tham chiếu API: nhóm `oauth`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/oauth.md](../cases/oauth.md).

OAuth2/OIDC: authorize, token, userinfo, introspect (RFC 7662), logout, register (tra client dựng sẵn theo redirect URI). Lỗi /oauth2/token theo RFC 6749 §5.2.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /oauth2/authorize`](#get-oauth2-authorize) | Bắt đầu luồng authorization code (OAuth2/OIDC) |
| [`POST /oauth2/authorize`](#post-oauth2-authorize) | Bắt đầu luồng authorization code (OAuth2/OIDC) |
| [`POST /oauth2/introspect`](#post-oauth2-introspect) | Kiểm tra một token (RFC 7662) |
| [`GET /oauth2/logout`](#get-oauth2-logout) | Đăng xuất (thu hồi login của access token) |
| [`POST /oauth2/logout`](#post-oauth2-logout) | Đăng xuất (thu hồi login của access token) |
| [`POST /oauth2/register`](#post-oauth2-register) | Tra client dựng sẵn theo redirect URI |
| [`POST /oauth2/token`](#post-oauth2-token) | Đổi thông tin lấy token (OAuth2) |
| [`GET /oauth2/userinfo`](#get-oauth2-userinfo) | OIDC userinfo |
| [`POST /oauth2/userinfo`](#post-oauth2-userinfo) | OIDC userinfo |

---

<a id="get-oauth2-authorize"></a>
## `GET /oauth2/authorize`

**Bắt đầu luồng authorization code (OAuth2/OIDC)**

Kiểm tra tham số rồi chuyển hướng (302). Không có phiên: sang trang đăng nhập của ứng dụng (<AUTH_APP_BASE_URL>/oauth) kèm tham số; trang đó gọi /auth/login, /auth/profile, /auth/scope rồi chuyển về redirect_uri kèm code.
Có phiên (cookie `medplum-<client_id>` hoặc id_token_hint): tạo Login mới; prompt=none thì trả code ngay, không thì sang trang ứng dụng để xác nhận scope.
Lỗi về client_id hoặc redirect_uri hiển thị trực tiếp (400 văn bản); lỗi khác chuyển về redirect_uri kèm error, error_description, state và iss (RFC 9207).
Client không có secret bắt buộc có code_challenge (S256). PKCE không được kế thừa từ phiên cũ.
POST nhận cùng tham số ở form `application/x-www-form-urlencoded`.

Các trường hợp phản hồi: [../cases/oauth.md#get-oauth2-authorize](../cases/oauth.md#get-oauth2-authorize)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | không có body |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `response_type` | query | string | có | code |
| `client_id` | query | string | có | client_id |
| `redirect_uri` | query | string | có | khớp chính xác một redirect URI đã đăng ký |
| `scope` | query | string | không | mặc định defaultScope của client |
| `state` | query | string | không | trả nguyên |
| `nonce` | query | string | không | vào id token |
| `code_challenge` | query | string | không | PKCE |
| `code_challenge_method` | query | string | không | S256 |
| `aud` | query | string | không | cùng scheme và host với auth hoặc fhir-server |
| `launch` | query | string | không | id SmartAppLaunch |
| `prompt` | query | string | không | none \| login |
| `max_age` | query | integer | không | tuổi tối đa của phiên (giây), mặc định 3600 |
| `id_token_hint` | query | string | không | id token đã cấp cho client này |

**Phản hồi**

**`302`** Found

Không có body.

**`400`** Bad Request

---

<a id="post-oauth2-authorize"></a>
## `POST /oauth2/authorize`

**Bắt đầu luồng authorization code (OAuth2/OIDC)**

Kiểm tra tham số rồi chuyển hướng (302). Không có phiên: sang trang đăng nhập của ứng dụng (<AUTH_APP_BASE_URL>/oauth) kèm tham số; trang đó gọi /auth/login, /auth/profile, /auth/scope rồi chuyển về redirect_uri kèm code.
Có phiên (cookie `medplum-<client_id>` hoặc id_token_hint): tạo Login mới; prompt=none thì trả code ngay, không thì sang trang ứng dụng để xác nhận scope.
Lỗi về client_id hoặc redirect_uri hiển thị trực tiếp (400 văn bản); lỗi khác chuyển về redirect_uri kèm error, error_description, state và iss (RFC 9207).
Client không có secret bắt buộc có code_challenge (S256). PKCE không được kế thừa từ phiên cũ.
POST nhận cùng tham số ở form `application/x-www-form-urlencoded`.

Các trường hợp phản hồi: [../cases/oauth.md#post-oauth2-authorize](../cases/oauth.md#post-oauth2-authorize)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | không có body |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `response_type` | query | string | có | code |
| `client_id` | query | string | có | client_id |
| `redirect_uri` | query | string | có | khớp chính xác một redirect URI đã đăng ký |
| `scope` | query | string | không | mặc định defaultScope của client |
| `state` | query | string | không | trả nguyên |
| `nonce` | query | string | không | vào id token |
| `code_challenge` | query | string | không | PKCE |
| `code_challenge_method` | query | string | không | S256 |
| `aud` | query | string | không | cùng scheme và host với auth hoặc fhir-server |
| `launch` | query | string | không | id SmartAppLaunch |
| `prompt` | query | string | không | none \| login |
| `max_age` | query | integer | không | tuổi tối đa của phiên (giây), mặc định 3600 |
| `id_token_hint` | query | string | không | id token đã cấp cho client này |

**Phản hồi**

**`302`** Found

Không có body.

**`400`** Bad Request

---

<a id="post-oauth2-introspect"></a>
## `POST /oauth2/introspect`

**Kiểm tra một token (RFC 7662)**

Cần Bearer. Chỉ hỏi được token của cùng project và cùng client với người gọi; refresh token đã xoay là không hoạt động. Mọi lý do không dùng được đều ra `{"active": false}`.

Các trường hợp phản hồi: [../cases/oauth.md#post-oauth2-introspect](../cases/oauth.md#post-oauth2-introspect)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/x-www-form-urlencoded`, `application/json` |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/x-www-form-urlencoded`, `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `token` | form | string | có | token cần kiểm tra |

**Phản hồi**

**`200`** OK

Schema: `httpapi.IntrospectResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `active` | boolean | có |  | true khi token còn dùng được; false thì không có trường nào khác | `true` |
| `client_id` | string | không |  | id ClientApplication đã xin token; chỉ có khi có | `"b4d1e2f3-0000-4000-8000-000000000000"` |
| `exp` | integer | không |  | thời điểm hết hạn (Unix giây); chỉ có khi active=true | `1763903600` |
| `iat` | integer | không |  | thời điểm cấp token (Unix giây); chỉ có khi active=true | `1763900000` |
| `iss` | string | không |  | issuer đã cấp token; chỉ có khi active=true | `"https://auth.example.org"` |
| `patient` | string | không |  | Patient của ngữ cảnh SMART launch; chỉ có khi có | `"Patient/3f2a8c1e-0000-4000-8000-000000000000"` |
| `scope` | string | không |  | các scope của token, cách nhau bằng khoảng trắng; chỉ có khi có | `"openid fhirUser"` |
| `sub` | string | không |  | claim sub của token (với refresh token là id của User); chỉ có khi active=true | `"3f2a8c1e-0000-4000-8000-000000000000"` |

```json
{
  "active": true,
  "client_id": "b4d1e2f3-0000-4000-8000-000000000000",
  "exp": 1763903600,
  "iat": 1763900000,
  "iss": "https://auth.example.org",
  "patient": "Patient/3f2a8c1e-0000-4000-8000-000000000000",
  "scope": "openid fhirUser",
  "sub": "3f2a8c1e-0000-4000-8000-000000000000"
}
```

**`400`** Token to introspect must be provided

**`401`** Unauthorized

Object tự do (khóa và giá trị tùy ý).

---

<a id="get-oauth2-logout"></a>
## `GET /oauth2/logout`

**Đăng xuất (thu hồi login của access token)**

Cần Bearer. Thu hồi login và xóa cookie đăng nhập của client. Sau đó chính access token này bị từ chối ở các route của auth (fhir-server chỉ biết khi token hết hạn).

Các trường hợp phản hồi: [../cases/oauth.md#get-oauth2-logout](../cases/oauth.md#get-oauth2-logout)

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

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-oauth2-logout"></a>
## `POST /oauth2/logout`

**Đăng xuất (thu hồi login của access token)**

Cần Bearer. Thu hồi login và xóa cookie đăng nhập của client. Sau đó chính access token này bị từ chối ở các route của auth (fhir-server chỉ biết khi token hết hạn).

Các trường hợp phản hồi: [../cases/oauth.md#post-oauth2-logout](../cases/oauth.md#post-oauth2-logout)

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

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-oauth2-register"></a>
## `POST /oauth2/register`

**Tra client dựng sẵn theo redirect URI**

Không phải đăng ký động theo RFC 7591: không tạo client mới và không trả client_secret. Chỉ có khi bật AUTH_OAUTH_REGISTER_ENABLED. redirect_uris[0] phải bằng chính xác redirect URI của một client dựng sẵn.

Các trường hợp phản hồi: [../cases/oauth.md#post-oauth2-register](../cases/oauth.md#post-oauth2-register)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (redirect_uris)

Schema: `httpapi.RegisterBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `redirect_uris` | mảng string | có |  | danh sách redirect URI; phần tử đầu phải khớp chính xác một client dựng sẵn (không thì 400 invalid_client_metadata) | `["https://app.example.org/callback"]` |

```json
{
  "redirect_uris": [
    "https://app.example.org/callback"
  ]
}
```

**Phản hồi**

**`201`** Created

Schema: `application.RegisteredClient`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `client_id` | string | có |  | id của client dựng sẵn khớp redirect_uri | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77"` |
| `client_id_issued_at` | integer | có |  | thời điểm trả về, Unix epoch tính bằng giây | `1795428900` |
| `redirect_uris` | mảng string | có |  | redirect URI khớp (đúng một phần tử) | `["https://app.example.com/callback"]` |

```json
{
  "client_id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77",
  "client_id_issued_at": 1795428900,
  "redirect_uris": [
    "https://app.example.com/callback"
  ]
}
```

**`400`** Bad Request

Schema: `httpapi.TokenErrorBody` (cấu trúc dùng chung, xem [common.md mục 4](../common.md#4-lỗi-oauth2)).

---

<a id="post-oauth2-token"></a>
## `POST /oauth2/token`

**Đổi thông tin lấy token (OAuth2)**

Chỉ nhận application/x-www-form-urlencoded. Grant hiện có: client_credentials, authorization_code và refresh_token (xoay secret; dùng lại thì thu hồi Login) (PKCE bắt buộc khi Login có challenge; client xác thực bằng HTTP Basic hoặc client_id + client_secret trong body).
Scope do client khai không được chứa `fhir.*`; access token mang `fhir_project`, `scope` (kèm scope vai trò do auth thêm) và `fhir_access_policy` để fhir-server nhận.

Các trường hợp phản hồi: [../cases/oauth.md#post-oauth2-token](../cases/oauth.md#post-oauth2-token)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | `application/x-www-form-urlencoded` |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Content-Type` | có | `application/x-www-form-urlencoded` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `grant_type` | form | string | có | client_credentials \| authorization_code \| refresh_token |
| `code` | form | string | không | authorization_code |
| `code_verifier` | form | string | không | authorization_code (PKCE) |
| `redirect_uri` | form | string | không | authorization_code: bắt buộc nếu lúc đăng nhập đã gửi |
| `refresh_token` | form | string | không | refresh_token |
| `client_id` | form | string | không | nếu không dùng Basic |
| `client_secret` | form | string | không | nếu không dùng Basic |
| `scope` | form | string | không | mặc định openid |

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

**`400`** Bad Request

Schema: `httpapi.TokenErrorBody` (cấu trúc dùng chung, xem [common.md mục 4](../common.md#4-lỗi-oauth2)).

---

<a id="get-oauth2-userinfo"></a>
## `GET /oauth2/userinfo`

**OIDC userinfo**

Cần Bearer access token. Trả `sub`; thêm theo scope của login (khớp từng từ): `profile` (profile, locale, updated_at, name, given_name, family_name), `email` (email, email_verified).
Không có phone, address, birthdate, gender, picture: hồ sơ Patient/Practitioner nằm ở fhir-server, auth không đọc được (quyết định D-10).

Các trường hợp phản hồi: [../cases/oauth.md#get-oauth2-userinfo](../cases/oauth.md#get-oauth2-userinfo)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

**`401`** Unauthorized

Object tự do (khóa và giá trị tùy ý).

---

<a id="post-oauth2-userinfo"></a>
## `POST /oauth2/userinfo`

**OIDC userinfo**

Cần Bearer access token. Trả `sub`; thêm theo scope của login (khớp từng từ): `profile` (profile, locale, updated_at, name, given_name, family_name), `email` (email, email_verified).
Không có phone, address, birthdate, gender, picture: hồ sơ Patient/Practitioner nằm ở fhir-server, auth không đọc được (quyết định D-10).

Các trường hợp phản hồi: [../cases/oauth.md#post-oauth2-userinfo](../cases/oauth.md#post-oauth2-userinfo)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

**`401`** Unauthorized

Object tự do (khóa và giá trị tùy ý).
