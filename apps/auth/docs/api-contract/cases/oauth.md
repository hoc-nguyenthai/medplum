# auth: các trường hợp của nhóm `oauth`

Chín route của OAuth 2.0 / OpenID Connect: `authorize` (GET, POST), `token`, `userinfo` (GET, POST), `introspect`, `logout` (GET, POST), `register`. Quy ước chung (header, cookie, envelope lỗi OAuth `{error,error_description}`, hạn mức IP) ở [../common.md](../common.md): tệp này **không chép lại**, chỉ ghi phần riêng của từng route. Bảng trường ở [../reference/oauth.md](../reference/oauth.md). Diễn giải tổng quan: [../oauth.md](../oauth.md) (khi lệch, **mã thắng**).

Nguồn: `internal/presentation/httpapi/oauth.go`, `oauth_resource.go`; `internal/application/token*.go`, `grant.go`, `client_assertion.go`, `fhircast_hub.go`, `external_exchange.go`; `internal/domain/oauth_error.go`, `pkce.go`, `tokenscope.go`, `authorize.go`; test HTTP `internal/bootstrap/oauth_*_test.go`, `mfa_code_test.go`, `exchange_google_http_test.go`, `internal/presentation/httpapi/oauth_basic_test.go`.

## Chuyển route và kiểu phản hồi

```mermaid
flowchart LR
    A["Ứng dụng client"] -->|"GET hoặc POST /oauth2/authorize"| B["302 sang trang đăng nhập AUTH_APP_BASE_URL/oauth"]
    B -->|"POST /auth/login, /auth/profile, /auth/scope"| C["code trong LoginResult + cookie medplum-clientId"]
    C -->|"302 redirect_uri?code=..."| A
    A -->|"POST /oauth2/token grant authorization_code"| D["access_token, id_token, refresh_token"]
    D -->|"Bearer"| E["GET hoặc POST /oauth2/userinfo"]
    D -->|"Bearer"| F["POST /oauth2/introspect"]
    D -->|"Bearer"| G["GET hoặc POST /oauth2/logout"]
    D -->|"POST /oauth2/token grant refresh_token"| D
    H["POST /oauth2/register"] -.->|"tra client_id dựng sẵn"| A
```

| Route | Dạng phản hồi (khác cấu trúc) |
|---|---|
| `authorize` | `302` + `Location` (sang trang ứng dụng, hoặc về `redirect_uri` kèm `code` hay `error`); `400` văn bản thuần (lỗi `client_id`, `redirect_uri`) |
| `token` | `200` JSON token; `400`/`500` JSON `{error,error_description}`; `400` văn bản `Unsupported content type`; `429`/`413` JSON `{"error":...}` |
| `userinfo`, `logout`, `introspect` khi thiếu hoặc sai Bearer | `401` **OperationOutcome** (`application/fhir+json`, `issue[0].code` = `login`, `details.text` = `Unauthorized`) kèm `WWW-Authenticate`, **không** phải JSON OAuth |
| `userinfo` | `200` JSON claim |
| `introspect` | `200` JSON `{"active":...}`; `400` văn bản thuần |
| `logout` | `200` OperationOutcome `All OK` |
| `register` | `201` JSON; `400` JSON OAuth |

**Bearer dùng chung (userinfo, introspect, logout):** middleware `requireBearer` chạy trước handler. Đúng một trong các nhánh:

| # | Điều kiện | HTTP | `WWW-Authenticate` | Body |
|---|---|---|---|---|
| B1 | không có header `Authorization`, không phải scheme `Bearer`, hoặc token rỗng | 401 | `Bearer realm="auth"` | OperationOutcome `login` / `Unauthorized` |
| B2 | token sai chữ ký, hết hạn, sai `iss`, `aud` hoặc `token_use` (id token, refresh token), thiếu `login_id`, **Login đã thu hồi hoặc chưa cấp**, membership hoặc project tắt | 401 | `Bearer realm="auth", error="invalid_token"` | như B1 |

Access token có trạng thái: thu hồi Login có hiệu lực ngay ở ba route này (nhưng fhir-server chỉ biết khi token hết hạn, xem [../oauth.md](../oauth.md) "Giới hạn đã biết").

---

<a id="get-oauth2-authorize"></a>
## `GET /oauth2/authorize`

Bắt đầu luồng authorization code: kiểm tham số, rồi chuyển hướng. Auth **không tự hiện màn đăng nhập**: chưa có phiên thì chuyển sang ứng dụng đăng nhập (`<AUTH_APP_BASE_URL>/oauth`), ứng dụng đó gọi `/auth/login`, `/auth/profile`, `/auth/scope` rồi đưa `code` về `redirect_uri`. Có phiên (cookie hoặc `id_token_hint`) thì tạo Login mới và, với `prompt=none`, trả `code` ngay. [Tham chiếu trường](../reference/oauth.md#get-oauth2-authorize)

**Cờ bật route:** chỉ có khi đặt `AUTH_APP_BASE_URL`. Không đặt: route không tồn tại, trả `404` `{"error":"Not Found"}` và discovery không khai `authorization_endpoint` (test `TestAuthorizeDisabledWithoutAppBaseURL`). `AUTH_ALLOW_PLAIN_PKCE` quyết định có nhận `code_challenge_method=plain`; `AUTH_AUTH_CODE_TTL` (10 phút) là hạn của `code` phát ở nhánh `prompt=none`.

**Thứ tự kiểm tra** (hàm `Service.Authorize`; lỗi đầu tiên trong thứ tự thắng):
1. Hạn mức IP (`429`).
2. Tham số lấy từ query (GET) hoặc form (POST); cookie `medplum-<client_id>` đọc theo `client_id` của yêu cầu.
3. `client_id` không tìm thấy, rỗng, hoặc client có `status` khác `active`: `400` văn bản `Client not found` (**không** chuyển hướng).
4. `redirect_uri` rỗng: `400` `Missing redirect URI`.
5. `redirect_uri` không parse được thành URL tuyệt đối, hoặc không **bằng chính xác** (phân biệt hoa thường, không tiền tố, không thêm query) một redirect URI của client: `400` `Invalid redirect URI`.
6. Từ đây mọi lỗi là `302` về `redirect_uri` kèm `error`, `error_description`, `state` (nếu có), `iss`. Theo thứ tự: scope rỗng (sau khi lấy `defaultScope` của client) `invalid_request`/`Missing scope`; `response_type` khác `code` `unsupported_response_type`/`Invalid response type`; có tham số `request` `request_not_supported`/`Unsupported request parameter`; `aud` sai `invalid_request`/`Invalid audience`; có `code_challenge` mà không có method `Missing code challenge method`; method khác `S256` (và khác `plain` khi bật) `Invalid code challenge method`; không có `code_challenge` mà client **không có secret** `Missing code challenge`.
7. `max_age` không phải số nguyên không âm: `invalid_request`/`Invalid max_age`.
8. `launch` có mà không đọc được SmartAppLaunch: `invalid_request`/`Invalid launch`.
9. Tìm phiên sẵn có (`id_token_hint` trước, rồi cookie): phải granted, chưa thu hồi, đúng client, còn trong `max_age` tính từ `authTime`, membership và project còn hoạt động.
10. Quyết định: không phiên và `prompt=none` thì `login_required`; có phiên và `prompt` khác `login` thì dùng phiên; còn lại sang trang ứng dụng.
11. Dùng phiên: kiểm `ipAccessRule` của membership hiện tại (chặn, hoặc AccessPolicy hỏng) thì `302` `access_denied`/`IP address not allowed`; tạo Login **mới** (`basedOn` = Login cũ, **ghi dữ liệu**); `prompt` khác `none` thì sang trang ứng dụng kèm `login=<id>`, `prompt=none` thì phát `code` (**ghi** `code`, `cookie` đã băm, `expiresAt` vào Login mới).
12. Lỗi hạ tầng (đọc client, tạo Login...) là `500` OperationOutcome `Internal server error`.

```mermaid
flowchart TD
    S(["GET hoặc POST /oauth2/authorize"]) --> C1{"client_id có và client active?"}
    C1 -->|"không"| E1["400 văn bản: Client not found"]
    C1 -->|"có"| C2{"redirect_uri có và khớp chính xác?"}
    C2 -->|"không"| E2["400 văn bản: Missing redirect URI hoặc Invalid redirect URI"]
    C2 -->|"có"| C3{"scope, response_type, request, aud, PKCE hợp lệ?"}
    C3 -->|"không"| E3["302 về redirect_uri kèm error, error_description, state, iss"]
    C3 -->|"có"| C4{"max_age và launch hợp lệ?"}
    C4 -->|"không"| E3
    C4 -->|"có"| C5{"có phiên hợp lệ qua id_token_hint hoặc cookie?"}
    C5 -->|"không"| C6{"prompt = none?"}
    C6 -->|"có"| E4["302 về redirect_uri: error=login_required"]
    C6 -->|"không"| A1["302 sang AUTH_APP_BASE_URL/oauth kèm tham số"]
    C5 -->|"có"| C7{"prompt = login?"}
    C7 -->|"có, bỏ qua phiên"| A1
    C7 -->|"không"| C8{"ipAccessRule cho phép?"}
    C8 -->|"không"| E5["302 về redirect_uri: error=access_denied"]
    C8 -->|"có"| N["tạo Login mới basedOn Login cũ"]
    N --> C9{"prompt = none?"}
    C9 -->|"có"| OK["302 về redirect_uri kèm code, state, iss"]
    C9 -->|"không"| A2["302 sang trang ứng dụng kèm login=id"]
```

**Bảng tham số và hành vi**

| Tham số | Bắt buộc | Hành vi |
|---|---|---|
| `client_id` | có | sai/vắng/client tắt: `400` `Client not found`. Cũng là tên cookie phiên `medplum-<client_id>` |
| `redirect_uri` | có | so khớp chính xác với danh sách của client; vắng `Missing redirect URI`; sai `Invalid redirect URI` (cả hai là `400` văn bản). Được lưu vào Login mới (nhánh phiên) và, khi có, phải gửi lại đúng ở `/oauth2/token` |
| `response_type` | có | chỉ `code`; khác hoặc vắng: `302` `unsupported_response_type` |
| `scope` | không | vắng thì lấy `defaultScope` của client (nối bằng dấu cách); vẫn rỗng: `Missing scope`. Mã **không** lọc term `fhir.*` ở đây (xem "Phát hiện lệch") |
| `state` | không | trả nguyên ở mọi `302` về client (cả khi lỗi) |
| `nonce` | không | chuyển sang trang ứng dụng; nhánh phiên lưu vào Login mới và vào id token |
| `code_challenge` | bắt buộc với client **không có secret** | vắng ở client không secret: `Missing code challenge`. **Không** kế thừa challenge từ Login cũ |
| `code_challenge_method` | cần khi có challenge | vắng `Missing code challenge method`; `S256` luôn nhận; `plain` chỉ khi `AUTH_ALLOW_PLAIN_PKCE=true`; còn lại `Invalid code challenge method` |
| `aud` | không | vắng hợp lệ; có thì phải URL tuyệt đối cùng scheme và host với `AUTH_BASE_URL`, issuer hoặc `FHIR_BASE_URL` của fhir-server; sai `Invalid audience` |
| `launch` | không | id SmartAppLaunch; không đọc được: `Invalid launch`. Có thì gắn vào Login mới (nhánh phiên) |
| `prompt` | không | `none`: không phiên thì `login_required`, có phiên thì trả `code` ngay. `login`: bỏ qua phiên, sang trang ứng dụng. Giá trị khác (ví dụ `consent`) không có tác dụng riêng |
| `max_age` | không | giây, mặc định `3600`, phải số nguyên không âm (khác Medplum: giá trị sai là lỗi `Invalid max_age`). Phiên cũ hơn `max_age` kể từ `authTime` không được dùng (`max_age=0` gần như luôn bỏ phiên) |
| `id_token_hint` | không | id token do auth ký cho **đúng client này** (`aud`=client_id, `token_use=id`, Login còn hiệu lực); access token, refresh token, token client khác, token hỏng bị bỏ qua (coi như không có hint). **Không** được chuyển sang URL trang ứng dụng |
| `request` | không | có mặt (dù giá trị gì): `request_not_supported` |

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Client lạ hoặc tắt | bước 3 | 400 | `Referrer-Policy: no-referrer` | văn bản `Client not found` | không |
| C2 | Thiếu redirect_uri | bước 4 | 400 | như C1 | văn bản `Missing redirect URI` | không |
| C3 | redirect_uri sai | bước 5: không parse được, không có scheme, khác hoa thường, thêm `/` hoặc query, tiền tố | 400 | như C1 | văn bản `Invalid redirect URI` | không |
| C4 | Thiếu scope | `scope` và `defaultScope` đều rỗng | 302 | `Location: <redirect_uri>?error=invalid_request&error_description=Missing+scope&state=..&iss=..` | rỗng | không |
| C5 | response_type sai | khác `code` hoặc vắng | 302 | `Location` với `error=unsupported_response_type`, `error_description=Invalid response type` | rỗng | không |
| C6 | Có tham số `request` | bất kỳ giá trị | 302 | `error=request_not_supported`, `Unsupported request parameter` | rỗng | không |
| C7 | aud sai | không phải URL, hoặc host lạ | 302 | `error=invalid_request`, `Invalid audience` | rỗng | không |
| C8 | Thiếu method | có `code_challenge`, không có method | 302 | `invalid_request`, `Missing code challenge method` | rỗng | không |
| C9 | Method sai | `S512`, hoặc `plain` khi tắt | 302 | `invalid_request`, `Invalid code challenge method` | rỗng | không |
| C10 | Thiếu challenge | client không secret, không `code_challenge` | 302 | `invalid_request`, `Missing code challenge` | rỗng | không |
| C11 | max_age sai | `abc`, `-5` | 302 | `invalid_request`, `Invalid max_age` | rỗng | không |
| C12 | launch sai | không có SmartAppLaunch | 302 | `invalid_request`, `Invalid launch` | rỗng | không |
| C13 | Không phiên, `prompt=none` | không có phiên hợp lệ (cookie lạ, cookie của login chưa cấp token, login thu hồi, cookie đặt dưới tên client khác, quá `max_age`, id_token_hint hỏng, membership tắt) | 302 | `error=login_required`, `Login required` | rỗng | không |
| C14 | Không phiên | không phiên, `prompt` khác `none` | 302 | `Location: <AUTH_APP_BASE_URL>/oauth?response_type=..&client_id=..&redirect_uri=..&scope=..&state=..&nonce=..&code_challenge=..&code_challenge_method=..&aud=..&launch=..&prompt=..&max_age=..` (chỉ tham số có giá trị; **không** có `id_token_hint`) | rỗng | không |
| C15 | `prompt=login` | có hoặc không phiên | 302 | như C14, **không** có `login` | rỗng | không (phiên không dùng) |
| C16 | Có phiên, cần xác nhận scope | phiên hợp lệ, `prompt` khác `none` và `login` | 302 | như C14 kèm `login=<id Login mới>` | rỗng | tạo Login mới `basedOn` Login cũ (chưa granted, `refreshSecret` mới nếu Login cũ có, redirect_uri được lưu); Login cũ không đổi |
| C17 | Có phiên, `prompt=none` | phiên hợp lệ | 302 | `Location: <redirect_uri>?code=..&state=..&iss=..` (giữ query sẵn có của redirect_uri) | rỗng | tạo Login mới, ghi băm `code`, `cookie`, `expiresAt` (10 phút) |
| C18 | Phiên nhưng IP bị chặn | `ipAccessRule` chặn hoặc AccessPolicy hỏng, với phiên hợp lệ | 302 | `error=access_denied`, `IP address not allowed` | rỗng | không tạo Login |
| C19 | Lỗi hạ tầng | DB lỗi khi đọc client, membership, tạo Login | 500 | - | OperationOutcome `exception` `Internal server error` | không (hoặc dở dang) |
| C20 | Route tắt | không có `AUTH_APP_BASE_URL` | 404 | - | `{"error":"Not Found"}` | không |
| C21 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
GET /oauth2/authorize?response_type=code&client_id=a3b1c2d4-0000-4000-8000-000000000001&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb&scope=openid%20profile%20offline_access&state=st-1&nonce=n1&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256 HTTP/1.1
Host: auth.example.org
```

Chưa có phiên (C14):

```http
HTTP/1.1 302 Found
Location: https://app.example.org/oauth?client_id=a3b1c2d4-0000-4000-8000-000000000001&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256&nonce=n1&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb&response_type=code&scope=openid+profile+offline_access&state=st-1
Referrer-Policy: no-referrer
```

Có phiên và `prompt=none` (C17):

```http
GET /oauth2/authorize?response_type=code&client_id=a3b1c2d4-0000-4000-8000-000000000001&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb&scope=openid&state=st-1&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256&prompt=none HTTP/1.1
Host: auth.example.org
Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=7f3c9a1e5b2d4c6f8a0b1c2d3e4f5a6b
```

```http
HTTP/1.1 302 Found
Location: https://app.example.org/cb?code=4f9c2e7a1b3d5f60a8c1e2d3b4a59687&iss=https%3A%2F%2Fauth.example.org&state=st-1
Referrer-Policy: no-referrer
```

Lỗi tham số (C4 đến C13, C18; cùng cấu trúc, đổi `error`):

```http
HTTP/1.1 302 Found
Location: https://app.example.org/cb?error=invalid_request&error_description=Invalid+code+challenge+method&iss=https%3A%2F%2Fauth.example.org&state=st-1
Referrer-Policy: no-referrer
```

Lỗi client hoặc redirect_uri (C1 đến C3), **không chuyển hướng**:

```http
HTTP/1.1 400 Bad Request
Content-Type: text/plain; charset=UTF-8
Referrer-Policy: no-referrer

Invalid redirect URI
```

**Lưu ý tích hợp:**
- Luôn kiểm `iss` ở `redirect_uri` (RFC 9207) và `state`; lỗi `302` vẫn mang cả hai.
- Ứng dụng đăng nhập (`AUTH_APP_BASE_URL`) nhận tham số, gọi `/auth/login` (kèm `codeChallenge`, `redirectUri`, `clientId`, `scope`) rồi chuyển người dùng về `redirect_uri` kèm `code`. `code` sống `AUTH_AUTH_CODE_TTL` (10 phút) kể từ lúc phát và đổi **một lần** ở `POST /oauth2/token`.
- PKCE không được kế thừa từ phiên cũ: nhánh phiên với `prompt=none` mà không gửi `code_challenge` tạo Login không PKCE; khi đó chỉ client xác thực bằng secret/assertion mới đổi được code (`Missing verification context` nếu không).
- Cookie phiên là `medplum-<client_id>` (`HttpOnly`, 1 giờ), xóa bởi `/oauth2/logout`; cookie của client A đặt dưới tên client B không cho B một phiên.

<a id="post-oauth2-authorize"></a>
## `POST /oauth2/authorize`

Cùng handler, cùng logic với [GET](#get-oauth2-authorize); khác nguồn tham số: **form `application/x-www-form-urlencoded`** trong body thay vì query. [Tham chiếu trường](../reference/oauth.md#post-oauth2-authorize)

**Cờ bật route:** giống GET (`AUTH_APP_BASE_URL`).

**Thứ tự kiểm tra:** như GET, thêm bước 0: `ParseForm` thất bại (ví dụ body percent-encoding hỏng) là `400` văn bản `Invalid request body`. Tham số trong query string **bị bỏ qua** ở POST (chỉ đọc `PostForm`); cookie đọc như GET. Body không phải form (ví dụ JSON) không báo lỗi riêng: không có tham số nào được đọc, nên rơi vào `400` `Client not found`.

Bảng trường hợp: dùng **C1 đến C21 của GET**, thêm các dòng sau.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C22 | Body form hỏng | `ParseForm` lỗi | 400 | `Referrer-Policy: no-referrer` | văn bản `Invalid request body` | không |
| C23 | POST client lạ | form có `client_id=khong-co` | 400 | như trên | văn bản `Client not found` | không |
| C24 | Thành công | form hợp lệ, không phiên | 302 | `Location` sang `<AUTH_APP_BASE_URL>/oauth?...` như C14 | rỗng | không |

**Ví dụ**

```http
POST /oauth2/authorize HTTP/1.1
Host: auth.example.org
Content-Type: application/x-www-form-urlencoded

response_type=code&client_id=a3b1c2d4-0000-4000-8000-000000000001&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb&scope=openid&state=p&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256
```

```http
HTTP/1.1 302 Found
Location: https://app.example.org/oauth?client_id=a3b1c2d4-0000-4000-8000-000000000001&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb&response_type=code&scope=openid&state=p
Referrer-Policy: no-referrer
```

**Lưu ý tích hợp:** dùng POST khi URL quá dài hoặc không muốn lộ tham số ở log truy cập. Trình duyệt chuyển hướng theo `302` sẽ gọi `GET` tới `Location`.

---

<a id="post-oauth2-token"></a>
## `POST /oauth2/token`

Đổi thông tin lấy token. Năm grant: `authorization_code`, `refresh_token`, `client_credentials`, `urn:ietf:params:oauth:grant-type:pre-authorized_code`, `urn:ietf:params:oauth:grant-type:token-exchange`. [Tham chiếu trường](../reference/oauth.md#post-oauth2-token)

**Cờ bật route và cấu hình ảnh hưởng:** luôn bật. `AUTH_ISSUER`, `AUTH_AUDIENCE`, `AUTH_ACCESS_TTL` (15 phút), `AUTH_REFRESH_TTL` (14 ngày), `AUTH_ID_TTL` (1 giờ), `AUTH_AUTH_CODE_TTL` (10 phút), `AUTH_REFRESH_GRACE` (30 giây, `0` tắt), `AUTH_SESSION_MAX_AGE` (0: không trần), `AUTH_ALLOW_PLAIN_PKCE`, `AUTH_FHIRCAST_HUB_URL`, `AUTH_REDIS_URL` (jti dùng chung nhiều instance; không đặt thì nhớ trong bộ nhớ), `AUTH_ALLOW_PRIVATE_JWKS` (chỉ dev/test), `AUTH_PATIENT_COMPARTMENT_ENABLED`, `AUTH_MAX_POLICY_CLAIM_BYTES` (16384).

### 1. Bước chung trước khi vào grant

Thứ tự (hàm `oauthHandlers.token`):
1. Hạn mức IP (`429`), giới hạn body 1 MB (`413`).
2. `Content-Type` phải đúng `application/x-www-form-urlencoded` (tham số `charset` được phép): sai hoặc vắng là `400` văn bản `Unsupported content type` (JSON cũng bị từ chối).
3. `ParseForm` lỗi: `400` `invalid_request`/`Invalid request body`. Chỉ đọc **body** (query string bị bỏ qua).
4. `grant_type` vắng: `invalid_request`/`Missing grant_type`; giá trị lạ (ví dụ `password`): `invalid_request`/`Unsupported grant_type` (**không** dùng mã `unsupported_grant_type`).
5. `client_credentials`, `authorization_code`, `refresh_token`: **xác thực client** (mục 2) rồi vào grant. `pre-authorized_code` và `token-exchange` **không** qua bước xác thực client, và header `Authorization` bị bỏ qua.
6. Lỗi từ grant: trạng thái `>= 500` luôn đổi thành `500` `server_error`/`Internal server error` (không lộ chi tiết); còn lại ghi đúng `error` và `error_description` của grant.

Mọi lỗi OAuth của route này kèm `Content-Type: application/json`, `Cache-Control: no-store`, `Pragma: no-cache`; thành công cũng có `Cache-Control: no-store` và `Pragma: no-cache`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| T1 | Sai Content-Type | JSON, `text/plain`, vắng | 400 | `Content-Type: text/plain` | văn bản `Unsupported content type` | không |
| T2 | Body form hỏng | `ParseForm` lỗi | 400 | `Pragma: no-cache` | `invalid_request` / `Invalid request body` | không |
| T3 | Thiếu grant_type | rỗng | 400 | như T2 | `invalid_request` / `Missing grant_type` | không |
| T4 | grant_type lạ | không thuộc năm giá trị | 400 | như T2 | `invalid_request` / `Unsupported grant_type` | không |
| T5 | Body quá lớn | > 1 MB | 413 | - | `{"error":"Request body too large"}` | không |
| T6 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |
| T7 | Lỗi nội bộ | DB, Signer, JTIStore, kho topic hỏng | 500 | như T2 | `server_error` / `Internal server error` | tùy thời điểm lỗi (xem từng grant) |

### 2. Xác thực client (chỉ `client_credentials`, `authorization_code`, `refresh_token`)

Chọn **đúng một** cách. Hàm `clientAuth`:

| # | Cách | Điều kiện | HTTP | Body (`error` / `error_description`) | Tác dụng phụ |
|---|---|---|---|---|---|
| A1 | Secret trong body | không có `Authorization`, không có `client_assertion*`; lấy `client_id`, `client_secret` từ form | - | chuyển cho grant (rỗng được phép ở `authorization_code`/`refresh_token`) | không |
| A2 | HTTP Basic | `Authorization: Basic base64(client_id:client_secret)`; tách ở dấu `:` **đầu tiên**, UTF-8, URL-decode từng phần (RFC 6749 §2.3.1). Header có mặt thì `client_id`, `client_secret` trong body bị bỏ qua | - | như A1 | không |
| A3 | Header không đọc được | scheme khác `Basic`, base64 hỏng, không UTF-8, hoặc không có dấu `:` | 400 | `invalid_request` / `Invalid authorization header` | không |
| A4 | private_key_jwt | có `client_assertion_type` hoặc `client_assertion` (một trong hai đủ để vào nhánh này) | - | thứ tự kiểm ở A5 đến A15 | ghi jti **sau** khi chữ ký đúng |
| A5 | Dùng nhiều cách | có assertion **và** (`Authorization` hoặc `client_secret` trong body) | 400 | `invalid_request` / `Multiple client authentication methods` | không |
| A6 | Sai loại assertion | `client_assertion_type` khác `urn:ietf:params:oauth:client-assertion-type:jwt-bearer` | 400 | `invalid_request` / `Unsupported client assertion type` | không |
| A7 | Assertion không đọc được | không phải JWT ba phần, payload không phải JSON, rỗng | 400 | `invalid_request` / `Invalid client assertion` | không |
| A8 | `aud` sai | `aud` không phải **chuỗi** đúng bằng `<AUTH_BASE_URL>/oauth2/token` (mảng cũng sai) | 400 | `invalid_request` / `Invalid client assertion audience` | không |
| A9 | `iss` khác `sub` | `iss` rỗng hoặc khác `sub` | 400 | `invalid_request` / `Invalid client assertion issuer` | không |
| A10 | Client không có hoặc tắt | `iss` không phải client; `status` khác `active` | 400 | `invalid_request` / `Client not found` hoặc `Invalid client` | không |
| A11 | Không có JWKS | client không có `jwksUri` | 400 | `invalid_request` / `Client must have a JWK Set URL` | không |
| A12 | Chữ ký sai | không khóa nào trong JWKS khớp (kể cả `kid` lạ, thuật toán không khớp khóa) | 400 | `invalid_request` / `Invalid client assertion signature` | không (jti **không** bị đốt) |
| A13 | Lỗi xác minh khác | hết hạn, vắng `exp`, thuật toán không thuộc RS/ES 256, 384, 512 hoặc HS256/none, JWKS không tải được, `jwks_uri` bị chặn SSRF (http, loopback, dải nội bộ, userinfo trong URL) | 400 | `invalid_request` / `Invalid client assertion` | không |
| A14 | `exp` quá xa hoặc `jti` thiếu | `exp` hơn 5 phút kể từ lúc nhận; `jti` rỗng hoặc dài hơn 256 | 400 | `invalid_request` / `Client assertion expiration is too far in the future` hoặc `Client assertion must have a jti` | không |
| A15 | jti dùng lại | `client_id|jti` đã thấy (đến hết `exp` cộng 1 phút) | 400 | `invalid_request` / `Client assertion already used` | không |
| A16 | `client_id` lệch | form có `client_id` khác client trong assertion | 400 | `invalid_request` / `Invalid client` | **jti đã bị ghi** |
| A17 | Lỗi hạ tầng | không có JTIStore, hoặc kho jti lỗi, hoặc không đọc được client | 500 | `server_error` / `Internal server error` | không |

Ghi chú: JWKS của client được cache 5 phút; `kid` lạ chỉ làm tải lại tối đa một lần mỗi 30 giây. Khi nhiều khóa cùng `kid`, thử lần lượt tới khi khớp chữ ký. Client dùng assertion **không cần** có `secret`. Assertion hợp lệ nhưng grant sau đó lỗi vẫn **đã đốt jti** (bước xác thực chạy trước grant): phải sinh assertion mới khi thử lại.

### 3. Ma trận grant x kiểu client

| Grant | Client công khai (không secret, dùng PKCE) | Client bí mật (có secret) | Client private_key_jwt |
|---|---|---|---|
| `authorization_code` | không cần secret; **bắt buộc** `code_verifier` vì `/authorize` đòi challenge với client không secret. Nếu vẫn gửi secret thì phải đúng | Login **có** challenge: `code_verifier` bắt buộc, secret tùy chọn nhưng nếu gửi phải đúng. Login **không** challenge: bắt buộc secret (hoặc assertion), nếu không `Missing verification context` | assertion tính là đã xác thực, thay secret; phải là client của Login |
| `refresh_token` | không cần xác thực khi Login có challenge (refresh token là bằng chứng); gửi secret thì phải đúng | **bắt buộc** secret khi `client.secret` có và Login **không** có challenge (`Incorrect client secret`) | assertion đủ; phải là client của Login (`Incorrect client`) |
| `client_credentials` | **không dùng được** (`Invalid client`/`Missing client_secret`/`Invalid client`) | bắt buộc Basic hoặc secret trong body | assertion thay secret, client không cần có `secret` |
| `pre-authorized_code` | chỉ cần `client_id` + mã; **không** xác thực client | như công khai | như công khai |
| `token-exchange` | chỉ cần `client_id` của IdP + subject token; **không** xác thực client | như công khai | như công khai |

### 4. Grant `authorization_code`

Tham số: `code`, `code_verifier`, `redirect_uri` (bắt buộc nếu lúc đăng nhập đã gửi), `client_id`; xác thực client như mục 2 khi cần.

**Thứ tự kiểm tra** (`Service.AuthorizationCode`; lỗi đầu tiên thắng; điểm ghi dữ liệu đánh dấu **GHI**):
1. `code` rỗng.
2. Tra Login theo băm SHA-256 của `code` (kho chỉ giữ băm); không thấy.
3. `client_id` gửi lên khác client của Login.
4. Login không có membership.
5. **MFA** (phòng thủ chiều sâu): Login `authMethod=password` chưa `mfaVerified` mà user đã đăng ký hoặc bị bắt buộc MFA: từ chối, **không tiêu thụ code**.
6. Login đã thu hồi.
7. Login đã `granted` (code đã dùng): **GHI** thu hồi Login (`revoked=true`) rồi lỗi.
8. Hết hạn (`expiresAt` tính từ lúc phát, không bị kéo dài bởi lần ghi khác).
9. Nạp client của Login; không đọc được hoặc `status` khác `active`. Nếu xác thực bằng assertion mà client assertion khác client của Login.
10. Có `client_secret`: client phải tồn tại, có secret, và khớp (`secret` hoặc `retiringSecret`).
11. PKCE: Login **có** challenge thì cần `code_verifier` và phải khớp (S256; `plain` chỉ khi `AUTH_ALLOW_PLAIN_PKCE`; định dạng RFC 7636: 43 đến 128 ký tự `A-Za-z0-9-._~`), kể cả client `pkceOptional`. Login **không** challenge mà client chưa xác thực (không secret, không assertion): từ chối.
12. `redirect_uri` lưu lúc đăng nhập: nếu có thì request phải gửi lại và **bằng chính xác**.
13. Đọc membership và project; membership hoặc project tắt.
14. Có `launch`: đọc SmartAppLaunch.
15. Dịch AccessPolicy sang claim (fail đóng) và kiểm cỡ claim.
16. **GHI** chiếm code: `granted=true` bằng cập nhật có điều kiện phiên bản (hai yêu cầu song song: đúng một thành công; xung đột thì đọc lại, thấy đã granted thì thu hồi như bước 7).
17. Ký token (và `hub.*` nếu có).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| AC1 | Thành công | mọi bước qua | 200 | `Cache-Control: no-store`, `Pragma: no-cache` | JSON token (mục 8); có `refresh_token` nếu Login có `refreshSecret` (scope `offline_access`, client có grant `refresh_token`, hoặc cờ remember) | Login `granted=true`; ký token |
| AC2 | Thiếu code | `code` rỗng | 400 | - | `invalid_request` / `Missing code` | không |
| AC3 | Code sai | không có Login khớp băm (kể cả code đã bị poll `GET /auth/login/{id}` đổi sang code mới) | 400 | - | `invalid_request` / `Invalid code` | không |
| AC4 | Client lệch | `client_id` khác client của Login | 400 | - | `invalid_request` / `Invalid client` | không |
| AC5 | Không có membership | Login chưa chọn profile | 400 | - | `invalid_request` / `Invalid profile` | không |
| AC6 | MFA chưa xong | `invalid_grant`, xem điều kiện bước 5 | 400 | - | `invalid_grant` / `MFA verification required` | **không** tiêu thụ code; sau khi qua MFA cùng code dùng được |
| AC7 | Login đã thu hồi | `revoked=true` | 400 | - | `invalid_grant` / `Token revoked` | không |
| AC8 | Đổi code lần hai | Login đã `granted` | 400 | - | `invalid_grant` / `Token already granted` | **thu hồi Login** (mọi token đã cấp ở lần 1 bị 401 ở userinfo, introspect, logout, `/auth/me`; refresh token chết); log WARN |
| AC9 | Code hết hạn | quá `expiresAt` (hoặc thiếu hạn) | 400 | - | `invalid_grant` / `Authorization code expired` | không |
| AC10 | Client không dùng được | client không đọc được hoặc `status` tắt; assertion của client khác | 400 | - | `invalid_request` / `Invalid client` | không |
| AC11 | Secret sai | `client_secret` gửi lên không khớp, hoặc client không có secret | 400 | - | `invalid_request` / `Invalid secret` (không có secret: `Invalid client`) | **không** tiêu thụ code |
| AC12 | Thiếu verifier | Login có challenge, không `code_verifier` | 400 | - | `invalid_request` / `Missing code verifier` | không tiêu thụ code |
| AC13 | Verifier sai | không khớp challenge, sai định dạng (quá ngắn, quá dài, ký tự cấm), hoặc `plain` khi tắt | 400 | - | `invalid_request` / `Invalid code verifier` | **không** tiêu thụ code (người lộ code không đốt được code của người thật); log WARN |
| AC14 | Không PKCE, không xác thực | Login không challenge và client không gửi secret hay assertion | 400 | - | `invalid_request` / `Missing verification context` | không; log WARN |
| AC15 | Thiếu redirect_uri | Login đã lưu `redirect_uri` mà request không gửi | 400 | - | `invalid_request` / `Missing redirect_uri` | không |
| AC16 | redirect_uri lệch | khác chính xác (thêm `/`, thêm query, đổi hoa thường, host khác) | 400 | - | `invalid_grant` / `Invalid redirect_uri` | không |
| AC17 | Membership hoặc project lỗi | không đọc được | 400 | - | `invalid_request` / `Invalid profile` | không |
| AC18 | Membership hoặc project tắt | `active=false` | 400 | - | `access_denied` / `Profile not active` hoặc `Project not active` | không |
| AC19 | Launch hỏng | SmartAppLaunch không đọc được | 400 | - | `invalid_request` / `Invalid launch` | không |
| AC20 | AccessPolicy không dịch được | compartment hoặc basedOn ở mức chính sách, claim không qua `fhirauth.ParsePolicy`, claim quá `AUTH_MAX_POLICY_CLAIM_BYTES` | 400 | - | `access_denied` / `Access policy not supported` | không ghi; log WARN |
| AC21 | AccessPolicy trỏ sai | policy không đọc được hoặc thuộc project khác | 400 | - | `access_denied` / `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` | không |
| AC22 | Chiếm code thất bại sau 5 lần xung đột | xung đột phiên bản liên tục | 400 | - | `invalid_grant` / `Authorization code already used` | không |
| AC23 | Không lấy được hub.topic | scope có `fhircast/*`, `AUTH_FHIRCAST_HUB_URL` đặt, kho topic lỗi | 500 | - | `server_error` / `Internal server error` | **code đã bị chiếm** (Login `granted`): phải đăng nhập lại; không cấp token |
| AC24 | Lỗi hạ tầng | DB, Signer | 500 | - | `server_error` / `Internal server error` | tùy thời điểm |

Ca `ipAccessRule`: grant này **không** kiểm `ipAccessRule` của membership (chỉ kiểm lúc đăng nhập và ở `refresh_token`, `client_credentials`, `pre-authorized_code`). Điều này khác [../oauth.md](../oauth.md) ("cả bốn grant kiểm ipAccessRule"): xem "Phát hiện lệch".

```mermaid
sequenceDiagram
    participant App as "Ứng dụng client"
    participant Br as "Trình duyệt"
    participant Az as "auth: /oauth2/authorize"
    participant UI as "Trang đăng nhập AUTH_APP_BASE_URL"
    participant Lg as "auth: /auth/login, /auth/profile, /auth/scope"
    participant Tk as "auth: /oauth2/token"
    App->>App: sinh code_verifier ngẫu nhiên, code_challenge = BASE64URL(SHA256(verifier))
    App->>Br: mở /oauth2/authorize với code_challenge, method S256, state, redirect_uri
    Br->>Az: GET /oauth2/authorize
    Az-->>Br: 302 sang AUTH_APP_BASE_URL/oauth kèm tham số
    Br->>UI: tải trang đăng nhập
    UI->>Lg: POST /auth/login kèm clientId, codeChallenge, redirectUri, scope
    Lg-->>UI: login id, code, Set-Cookie medplum-clientId (sau MFA nếu cần)
    UI-->>Br: chuyển về redirect_uri?code=...&state=...&iss=...
    Br->>App: GET redirect_uri với code
    App->>Tk: POST grant_type=authorization_code, code, code_verifier, redirect_uri, client_id
    Tk->>Tk: kiểm MFA, hạn code, secret, PKCE, redirect_uri, membership, AccessPolicy
    Tk->>Tk: chiếm code nguyên tử: granted=true
    Tk-->>App: 200 access_token, id_token, refresh_token
    App->>Tk: lần hai với cùng code
    Tk-->>App: 400 invalid_grant Token already granted, Login bị thu hồi
```

**Ví dụ**

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&code=4f9c2e7a1b3d5f60a8c1e2d3b4a59687&code_verifier=dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb&client_id=a3b1c2d4-0000-4000-8000-000000000001
```

Client bí mật, Login không PKCE (Basic):

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Authorization: Basic YTNiMWMyZDQtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAxOnMzY3IzdA==
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&code=4f9c2e7a1b3d5f60a8c1e2d3b4a59687&redirect_uri=https%3A%2F%2Fapp.example.org%2Fcb
```

Phản hồi thành công (đủ trường, xem điều kiện ở mục 8):

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{
  "token_type": "Bearer",
  "expires_in": 900,
  "scope": "openid profile email offline_access",
  "id_token": "<jwt id>",
  "access_token": "<jwt access>",
  "refresh_token": "<jwt refresh>",
  "project": { "reference": "Project/3d236789-d8a2-4cdc-87c5-6ca5c443a732" },
  "profile": { "reference": "Practitioner/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77" },
  "smart_style_url": "https://auth.example.org/.well-known/smart-styles.json"
}
```

Phản hồi lỗi OAuth (mọi `AC2` đến `AC22`, cùng cấu trúc):

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{"error":"invalid_grant","error_description":"Token already granted"}
```

### 5. Grant `refresh_token`

Tham số: `refresh_token`; xác thực client như mục 2 khi cần (ma trận mục 3). Mỗi lần dùng **xoay** refresh secret và cấp refresh token mới.

**Thứ tự kiểm tra** (`Service.Refresh`, `refreshClient`, `rotateRefresh`):
1. Xác minh refresh token: JWT do chính auth ký, `iss` đúng, `token_use=refresh`, `aud` = `<issuer>/oauth2/token`, có `login_id` và `refresh_secret`. Access token, id token, `alg` none/HS256, token bị sửa đều không qua.
2. Đọc Login: không có là `invalid_grant`; Login không có `refreshSecret` (ví dụ Login của `client_credentials`); đã thu hồi; không có membership.
3. Đọc membership, project (lỗi: `invalid_grant`); tắt: `access_denied`.
4. Client: `client_id` gửi lên khác client của Login; Login không có client mà request gửi secret hoặc assertion; client không đọc được hoặc tắt; assertion của client khác; Login "bí mật" (`client.secret` có và Login không có `codeChallenge`) mà không gửi secret; secret gửi lên sai.
5. Trần phiên: nếu `AUTH_SESSION_MAX_AGE` > 0 và `authTime` + trần đã qua.
6. `ipAccessRule` của membership (và AccessPolicy phải đọc được).
7. Có `launch`: đọc SmartAppLaunch.
8. Dịch AccessPolicy (như AC20, AC21).
9. **GHI** xoay secret có điều kiện phiên bản (xem sơ đồ dưới), rồi ký token.

```mermaid
flowchart TD
    S(["POST /oauth2/token refresh_token"]) --> V{"refresh token hợp lệ: chữ ký, iss, token_use=refresh, aud, login_id, refresh_secret?"}
    V -->|"không"| E1["400 invalid_request: Invalid refresh token"]
    V -->|"có"| L{"Login tồn tại, có refreshSecret?"}
    L -->|"không tồn tại"| E2["400 invalid_grant: Invalid refresh token"]
    L -->|"không có refreshSecret"| E1
    L -->|"có"| R{"Login đã thu hồi?"}
    R -->|"có"| E3["400 invalid_grant: Token revoked"]
    R -->|"không"| M{"membership và project đang hoạt động?"}
    M -->|"không"| E4["400 access_denied: Profile not active hoặc Project not active"]
    M -->|"có"| C{"client đúng và đã xác thực nếu là client bí mật?"}
    C -->|"không"| E5["400 invalid_grant Incorrect client, Incorrect client secret hoặc invalid_request Invalid secret"]
    C -->|"có"| A{"quá AUTH_SESSION_MAX_AGE từ authTime?"}
    A -->|"có"| E6["400 invalid_grant: Session expired"]
    A -->|"không"| I{"ipAccessRule cho phép IP này?"}
    I -->|"không"| E7["400 invalid_request: IP address not allowed, không ghi gì"]
    I -->|"có"| P{"AccessPolicy dịch được?"}
    P -->|"không"| E8["400 access_denied: Access policy not supported"]
    P -->|"có"| X{"secret trình lên so với Login"}
    X -->|"bằng secret hiện hành"| ROT["GHI: previousRefreshSecret=cũ, refreshSecret mới, rotatedAt, remoteAddress, userAgent"]
    ROT --> OK["200 token mới kèm refresh token mới"]
    X -->|"bằng secret TRƯỚC"| G{"còn trong ân hạn AUTH_REFRESH_GRACE và cùng IP đã xoay?"}
    G -->|"có"| OK2["200 token ký với secret hiện hành, KHÔNG ghi, KHÔNG xoay lần nữa"]
    G -->|"không: quá ân hạn hoặc khác IP"| REV["GHI: revoked=true"]
    REV --> E9["400 invalid_grant: Token revoked"]
    X -->|"khác cả hai"| REV
```

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| RF1 | Thành công, xoay secret | secret trình lên là secret hiện hành | 200 | `Cache-Control: no-store`, `Pragma: no-cache` | JSON token; `refresh_token` mới (khác token cũ), `access_token` mới | **xoay**: `previousRefreshSecret` = secret cũ, `refreshSecret` mới, `refreshSecretRotatedAt`, `remoteAddress`, `userAgent` của yêu cầu này |
| RF2 | Thành công trong ân hạn | secret trình lên là secret **trước**, `now - rotatedAt < AUTH_REFRESH_GRACE` (30 giây) **và** IP của yêu cầu bằng `remoteAddress` đã ghi lúc xoay | 200 | như RF1 | JSON token; `refresh_token` mang secret **hiện hành** (cùng secret với lần xoay trước, không xoay thêm) | **không ghi Login**; log INFO |
| RF3 | Token không hợp lệ | rỗng, rác, `alg` none/HS256, bị sửa, access/id token, `iss` lạ, `aud` của access token, `token_use` sai, thiếu `refresh_secret`/`login_id` | 400 | - | `invalid_request` / `Invalid refresh token` | không |
| RF4 | Login không tồn tại | `login_id` không có trong kho | 400 | - | `invalid_grant` / `Invalid refresh token` | không |
| RF5 | Login không có refresh | Login không có `refreshSecret` (ví dụ `client_credentials`), hoặc không có membership | 400 | - | `invalid_request` / `Invalid refresh token` | không |
| RF6 | Login đã thu hồi | `revoked=true` (logout, dùng lại code, dùng lại refresh) | 400 | - | `invalid_grant` / `Token revoked` | không |
| RF7 | Membership hoặc project lỗi | không đọc được | 400 | - | `invalid_grant` / `Invalid refresh token` | không |
| RF8 | Membership hoặc project tắt | `active=false` | 400 | - | `access_denied` / `Profile not active` hoặc `Project not active` | không; bật lại thì dùng được |
| RF9 | Client lệch | `client_id` khác client của Login; hoặc Login không có client mà request gửi secret/assertion; assertion của client khác | 400 | - | `invalid_grant` / `Incorrect client` | không |
| RF10 | Client bí mật thiếu secret | `client.secret` có, Login không PKCE, request không gửi secret/assertion | 400 | - | `invalid_grant` / `Incorrect client secret` | không |
| RF11 | Secret sai | secret gửi lên không khớp, hoặc client không có secret | 400 | - | `invalid_request` / `Invalid secret` | không (không thu hồi) |
| RF12 | Client không dùng được | client không đọc được hoặc `status` tắt | 400 | - | `invalid_request` / `Invalid client` | không |
| RF13 | Phiên quá trần | `AUTH_SESSION_MAX_AGE` > 0, `authTime` + trần đã qua | 400 | - | `invalid_grant` / `Session expired` | không |
| RF14 | IP bị chặn | `ipAccessRule` chặn IP yêu cầu | 400 | - | `invalid_request` / `IP address not allowed` | **không xoay, không ghi IP** vào Login (kiểm trước khi ghi) |
| RF15 | AccessPolicy hỏng | policy không đọc được | 400 | - | `access_denied` / `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` | không |
| RF16 | AccessPolicy không dịch được | như AC20 | 400 | - | `access_denied` / `Access policy not supported` | không |
| RF17 | Dùng lại ngoài ân hạn | secret trình lên là secret trước nhưng quá 30 giây, hoặc `AUTH_REFRESH_GRACE=0` | 400 | - | `invalid_grant` / `Token revoked` | **thu hồi Login**: mọi token (kể cả access token vừa cấp) bị 401 ở route của auth; log WARN |
| RF18 | Dùng lại từ IP khác | secret trước, trong ân hạn nhưng IP khác IP đã xoay (hoặc thiếu IP) | 400 | - | `invalid_grant` / `Token revoked` | **thu hồi Login** |
| RF19 | Secret lạ | secret không khớp cả hiện hành lẫn trước | 400 | - | `invalid_grant` / `Token revoked` | **thu hồi Login** |
| RF20 | Xoay xung đột 5 lần | 5 lần ghi liên tiếp thua xung đột phiên bản | 400 | - | `invalid_grant` / `Token revoked` | không |
| RF21 | Song song cùng token | 16 yêu cầu cùng token | 200 (cả 16) | như RF1 | cùng secret hiện hành | đúng một lần xoay, Login **không** bị thu hồi (test `TestRefreshTokenConcurrentRefreshRotatesOnce`) |
| RF22 | Lỗi hạ tầng | DB, Signer | 500 | - | `server_error` / `Internal server error` | tùy thời điểm |

**Ví dụ**

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&refresh_token=<jwt refresh>&client_id=a3b1c2d4-0000-4000-8000-000000000001
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{
  "token_type": "Bearer",
  "expires_in": 900,
  "scope": "openid offline_access",
  "id_token": "<jwt id>",
  "access_token": "<jwt access>",
  "refresh_token": "<jwt refresh mới>",
  "project": { "reference": "Project/3d236789-d8a2-4cdc-87c5-6ca5c443a732" },
  "profile": { "reference": "Practitioner/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77" },
  "smart_style_url": "https://auth.example.org/.well-known/smart-styles.json"
}
```

Client bí mật phải gửi secret (RF10 nếu thiếu):

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Authorization: Basic YTNiMWMyZDQtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAxOnMzY3IzdA==
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&refresh_token=<jwt refresh>
```

### 6. Grant `client_credentials`

Máy với máy. Tham số: `scope` (mặc định `openid`); xác thực client **bắt buộc** (Basic, body hoặc assertion). Không có refresh token (cả khi xin `offline_access`).

**Thứ tự kiểm tra** (`Service.ClientCredentials`; chưa ghi gì cho tới bước cuối):
1. Xác thực client: nếu chưa xác thực bằng assertion thì `AuthenticateClient`: thiếu `client_id`; thiếu `client_secret`; client không có (hoặc lỗi đọc là 500); client `status` tắt; client không có secret; secret sai.
2. Membership của client (`ClientApplication/<id>`, active, trong project chứa client): phải có **đúng một**.
3. Đọc project; membership hoặc project tắt.
4. `scope` chứa `patient/`; rồi term `fhir.*` (không phân biệt hoa thường).
5. `ipAccessRule` (và policy đọc được).
6. Dịch AccessPolicy (fail đóng, kiểm cỡ).
7. **GHI** tạo Login (`authMethod=client`, `granted=true`, `scope`, `remoteAddress`, `userAgent`; không có `refreshSecret`) rồi ký token; hub (nếu scope `fhircast/`).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| CC1 | Thành công | mọi bước qua | 200 | `Cache-Control: no-store`, `Pragma: no-cache` | JSON token **không có** `refresh_token`; `scope` = scope đã xin (mặc định `openid`); `sub` của access token = id ClientApplication; `profile` chỉ khi membership có | tạo Login `granted`; ghi jti nếu dùng assertion |
| CC2 | Thiếu client_id | Basic/body không có `client_id` | 400 | - | `invalid_request` / `Missing client_id` | không |
| CC3 | Thiếu secret | có `client_id`, không có secret | 400 | - | `invalid_request` / `Missing client_secret` | không |
| CC4 | Client lạ | `client_id` không tồn tại, hoặc client tắt (`status`), hoặc client không có secret | 400 | - | `invalid_request` / `Invalid client` | không |
| CC5 | Secret sai | không khớp (`secret` hoặc `retiringSecret`) | 400 | - | `invalid_request` / `Invalid secret` | không |
| CC6 | Membership không xác định | client không có membership active, hoặc có nhiều hơn một trong project (không chọn được duy nhất), hoặc project không đọc được | 400 | - | `invalid_request` / `Invalid client` (test chỉ khẳng định `400` khi membership tắt; thông điệp theo mã) | không |
| CC7 | Membership hoặc project tắt | `InactiveReason` khác rỗng | 400 | - | `access_denied` / `Profile not active` hoặc `Project not active` | không |
| CC8 | Scope patient | `scope` có chuỗi `patient/` (ví dụ `patient/Patient.rs`) | 400 | - | `invalid_scope` / `Cannot use client credentials with patient scope` | không tạo Login |
| CC9 | Scope `fhir.*` | term bắt đầu `fhir.` (`fhir.admin`, `FHIR.SUPER`, `fhir.write`) | 400 | - | `invalid_scope` / `Scope fhir.admin is reserved` (tên term nguyên văn như gửi) | không tạo Login |
| CC10 | IP bị chặn | `ipAccessRule` chặn | 400 | - | `invalid_request` / `IP address not allowed` | không tạo Login |
| CC11 | AccessPolicy hỏng hoặc không dịch được | như AC20, AC21 (kể cả claim quá `AUTH_MAX_POLICY_CLAIM_BYTES`) | 400 | - | `access_denied` / `Access policy not supported` hoặc `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` | không tạo Login |
| CC12 | Không lấy được hub.topic | scope `fhircast/*` và kho topic lỗi | 500 | - | `server_error` / `Internal server error` | Login **đã tạo** (granted); không cấp token |
| CC13 | Lỗi hạ tầng | DB, Signer | 500 | - | `server_error` / `Internal server error` | tùy thời điểm |

Ghi chú: scope `fhir.*` bị chặn **chỉ** ở grant này. Access token mang `scope` = scope xin **cộng** scope vai trò do auth thêm (`fhir.admin` nếu membership là admin project; `fhir.read` nếu mọi rule của AccessPolicy chỉ đọc; còn lại `fhir.write`; **không thêm** khi scope có term SMART `system/`, `user/`, `patient/`).

**Ví dụ**

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Authorization: Basic YTNiMWMyZDQtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAxOnMzY3IzdA==
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials&scope=system%2FPatient.rs
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{
  "token_type": "Bearer",
  "expires_in": 900,
  "scope": "system/Patient.rs",
  "id_token": "<jwt id>",
  "access_token": "<jwt access>",
  "project": { "reference": "Project/3d236789-d8a2-4cdc-87c5-6ca5c443a732" },
  "smart_style_url": "https://auth.example.org/.well-known/smart-styles.json"
}
```

private_key_jwt (không cần secret; assertion ký RS256 hoặc ES256, `aud` = `<AUTH_BASE_URL>/oauth2/token`, `exp` trong 5 phút, `jti` dùng một lần):

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials&client_assertion_type=urn%3Aietf%3Aparams%3Aoauth%3Aclient-assertion-type%3Ajwt-bearer&client_assertion=<jwt assertion>
```

### 7. Grant `urn:ietf:params:oauth:grant-type:pre-authorized_code`

Đổi mã cấp sẵn (do `POST /auth/preauthorize` tạo) lấy token. Mã là bearer: **không** PKCE, **không** xác thực client; an toàn nhờ chỉ lưu băm, có hạn, dùng một lần. Tham số: `client_id`, `pre-authorized_code`.

**Thứ tự kiểm tra** (`Service.PreAuthorizedCode`; IP và policy được kiểm **trước** khi chiếm mã nên lỗi không đốt mã):
1. `client_id` rỗng; `pre-authorized_code` rỗng.
2. Đọc client: không có là `invalid_client`; client `status` tắt.
3. Tra Login theo băm mã; không thấy hoặc `authMethod` khác `pre-authorized`.
4. Client của Login khác `client_id`; Login không có membership.
5. Đã thu hồi; đã `granted` (**GHI** thu hồi Login); hết hạn hoặc thiếu `expiresAt`.
6. Đọc membership, project; tắt.
7. `ipAccessRule`; launch; dịch AccessPolicy.
8. **GHI** chiếm mã (`granted=true`, có điều kiện phiên bản); ký token. Không có refresh token.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| PA1 | Thành công | mọi bước qua | 200 | `Cache-Control: no-store`, `Pragma: no-cache` | JSON token, **không có** `refresh_token` | Login `granted=true` |
| PA2 | Thiếu client_id | rỗng | 400 | - | `invalid_request` / `Missing client_id` | không |
| PA3 | Thiếu mã | rỗng | 400 | - | `invalid_request` / `Missing pre-authorized_code` | không |
| PA4 | Client không tồn tại | `client_id` lạ | 400 | - | `invalid_client` / `Invalid client` (HTTP vẫn `400`, **không** `401`) | không |
| PA5 | Client tắt | `status` khác `active` | 400 | - | `invalid_request` / `Invalid client` | không |
| PA6 | Mã sai | không khớp, hoặc Login khớp băm nhưng `authMethod` khác `pre-authorized` | 400 | - | `invalid_request` / `Invalid pre-authorized_code` | không |
| PA7 | Client khác | Login của client khác | 400 | - | `invalid_request` / `Invalid client` | không |
| PA8 | Không có membership | Login thiếu membership | 400 | - | `invalid_request` / `Invalid profile` | không |
| PA9 | Đã thu hồi | `revoked=true` | 400 | - | `invalid_grant` / `Token revoked` | không |
| PA10 | Dùng lại mã | đã `granted` | 400 | - | `invalid_grant` / `Token already granted` | **thu hồi Login** (token đã cấp ở lần 1 bị 401 ở userinfo) |
| PA11 | Mã hết hạn | quá `expiresAt` (mặc định mã sống 3600 giây, tối đa 604800) hoặc thiếu hạn (fail đóng) | 400 | - | `invalid_grant` / `Pre-authorized code expired` | không |
| PA12 | Membership hoặc project lỗi | không đọc được | 400 | - | `invalid_request` / `Invalid profile` | không |
| PA13 | Membership hoặc project tắt | `active=false` | 400 | - | `access_denied` / `Profile not active` hoặc `Project not active` | không |
| PA14 | IP bị chặn | `ipAccessRule` chặn | 400 | - | `invalid_request` / `IP address not allowed` | **không đốt mã** (bỏ chặn thì cùng mã dùng được) |
| PA15 | Launch hỏng | không đọc được | 400 | - | `invalid_request` / `Invalid launch` | không |
| PA16 | AccessPolicy không dịch được hoặc hỏng | như AC20, AC21 | 400 | - | `access_denied` / `Access policy not supported` hoặc `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` | không |
| PA17 | Đua song song | 16 yêu cầu cùng mã | 200 đúng một | - | 15 yêu cầu còn lại bị `invalid_grant` | đúng một Login được cấp |
| PA18 | Lỗi hạ tầng hoặc hub.topic | DB, Signer, kho topic | 500 | - | `server_error` / `Internal server error` | tùy thời điểm |

**Ví dụ**

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Content-Type: application/x-www-form-urlencoded

grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Apre-authorized_code&client_id=a3b1c2d4-0000-4000-8000-000000000001&pre-authorized_code=9b8a7c6d5e4f30211203f4e5d6c7b8a9
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{
  "token_type": "Bearer",
  "expires_in": 900,
  "scope": "openid",
  "id_token": "<jwt id>",
  "access_token": "<jwt access>",
  "project": { "reference": "Project/3d236789-d8a2-4cdc-87c5-6ca5c443a732" },
  "profile": { "reference": "Practitioner/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77" },
  "smart_style_url": "https://auth.example.org/.well-known/smart-styles.json"
}
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{"error":"invalid_client","error_description":"Invalid client"}
```

### 7b. Grant `urn:ietf:params:oauth:grant-type:token-exchange`

Có trong mã và trong discovery (swagger chưa mô tả): đổi access token của IdP ngoài (RFC 8693) lấy token của auth. Tham số: `client_id` (id client IdP đặt ở cấu hình máy chủ `AUTH_EXTERNAL_AUTH_PROVIDERS`, hoặc id ClientApplication có `identityProvider`), `subject_token`, `subject_token_type` (phải là `urn:ietf:params:oauth:token-type:access_token`), `membership_id` (**bắt buộc** với IdP cấu hình ở máy chủ), `scope` (mặc định `openid offline_access`), `nonce` (mặc định sinh ngẫu nhiên). Không xác thực client; `POST /auth/exchange` là vỏ mỏng (deprecated) của grant này.

**Thứ tự kiểm tra** (`Service.ExchangeToken`): `client_id` rỗng; `subject_token` rỗng; `subject_token_type` sai; thiếu bộ OIDC (500); tìm IdP (cấu hình máy chủ, rồi ClientApplication); IdP máy chủ cần `membership_id` đọc được; có `userInfoUrl`; `aud`/`azp` của token (nếu là JWT) thuộc ứng dụng này; gọi userinfo của IdP; xác định email hoặc subject; email chưa xác minh; `TryLogin` (tạo Login); đặt `User.emailVerified=true`; chiếm Login và ký token (như pre-authorized, có kiểm membership, launch, AccessPolicy).

| # | Trường hợp | Điều kiện | HTTP | Body | Tác dụng phụ |
|---|---|---|---|---|---|
| TE1 | Thành công | mọi bước qua | 200 | JSON token (mục 8); có `refresh_token` khi scope có `offline_access` | tạo Login (qua `TryLogin`), `User.emailVerified=true`, Login `granted` |
| TE2 | Thiếu client | `client_id` rỗng, hoặc không phải IdP máy chủ lẫn ClientApplication có IdP, hoặc client tắt | 400 | `invalid_request` / `Invalid client` | không |
| TE3 | Thiếu subject_token | rỗng, hoặc `aud`/`azp` của JWT không thuộc ứng dụng này | 400 | `invalid_request` / `Invalid subject_token` | không |
| TE4 | Sai loại token | `subject_token_type` khác URN access_token | 400 | `invalid_request` / `Invalid subject_token_type` | không |
| TE5 | Thiếu membership_id | IdP máy chủ, không có `membership_id` | 400 | `invalid_request` / `Missing membership_id` | không |
| TE6 | Membership sai | không đọc được hoặc không có project | 400 | `invalid_request` / `Invalid membership` | không |
| TE7 | Thiếu userInfoUrl | IdP không cấu hình | 400 | `invalid_request` / `Missing user info URL` | không |
| TE8 | IdP từ chối | userinfo lỗi | 400 | `invalid_request` / thông điệp cố định của IdP, ví dụ `Failed to verify code - check your identity provider configuration` | không |
| TE9 | IdP bị hạn mức | IdP trả hạn mức | 429 | `invalid_request` / thông điệp của lỗi hạn mức (HTTP **429**, không phải 400) | không |
| TE10 | Không có danh tính | token không có email hoặc subject theo cấu hình | 400 | `invalid_request` / `External token does not contain email address` hoặc `External token does not contain subject` | không |
| TE11 | Email chưa xác minh | `email_verified` là `false` | 400 | `invalid_request` / `Email address is not verified by the identity provider` | không |
| TE12 | Đăng nhập thất bại | `TryLogin` trả lỗi nghiệp vụ (user không có, membership không hợp lệ...) | 400 | `invalid_request` / thông điệp nghiệp vụ của `TryLogin` (xem cases/auth.md); không có membership: `User not found` | có thể đã tạo Login |
| TE13 | Sau đăng nhập | membership tắt, launch hỏng, AccessPolicy không dịch được | 400 | như PA12, PA13, PA15, PA16 | Login đã tạo |
| TE14 | Lỗi nội bộ | thiếu bộ OIDC, DB, lỗi `TryLogin` loại nội bộ | 500 | `server_error` / `Internal server error` | tùy thời điểm |

**Ví dụ**

```http
POST /oauth2/token HTTP/1.1
Host: auth.example.org
Content-Type: application/x-www-form-urlencoded

grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange&client_id=srv-cid&subject_token=ext-access&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aaccess_token&membership_id=99252587-916b-443d-bf16-827be14e1072
```

Phản hồi thành công cùng cấu trúc `200` ở mục 8. Chưa kiểm từng thông điệp `TryLogin` ở nhánh TE12 (chưa kiểm; xem [auth.md](auth.md) khi có).

### 8. Phản hồi 200: trường có điều kiện

| Trường | Kiểu | Khi nào có | Ý nghĩa |
|---|---|---|---|
| `token_type` | string | luôn | `Bearer` |
| `expires_in` | số | luôn | giây của **access token**: `AUTH_ACCESS_TTL` (15 phút) hoặc `accessTokenLifetime` của client (dạng `15m`, `1h`, `2w`; giá trị sai thì dùng mặc định và ghi log) |
| `scope` | string | luôn | `scope` của **Login** (đúng như đã xin), **không** gồm scope vai trò (`fhir.*`) mà access token có |
| `access_token`, `id_token` | JWT | luôn | xem bảng claim |
| `refresh_token` | JWT | chỉ khi Login có `refreshSecret` | `client_credentials` và `pre-authorized_code` không bao giờ có |
| `project` | `{reference}` | luôn | `Project/<id>` của membership |
| `profile` | `{reference}` | khi membership có profile | ví dụ `Practitioner/<id>` |
| `patient` | string (id, không kèm `Patient/`) | Login có SMART launch có `patient` (ưu tiên `identifier.value`, không thì id của tham chiếu), **hoặc** profile của membership là `Patient/<id>` (profile ghi đè launch) | ngữ cảnh bệnh nhân |
| `need_patient_banner` | bool `true` | cùng điều kiện `patient` | gợi ý hiện banner bệnh nhân |
| `encounter` | string | launch có `encounter` (ưu tiên `identifier.value`) | ngữ cảnh lượt khám |
| `fhirContext` | mảng tham chiếu | launch có `fhirContext` không rỗng | ngữ cảnh bổ sung |
| `smart_style_url` | string | khi `AUTH_BASE_URL` có | `<base>/.well-known/smart-styles.json` |
| `hub.url` | string | scope **đã cấp** có term bắt đầu `fhircast/` **và** `AUTH_FHIRCAST_HUB_URL` đặt | địa chỉ Hub FHIRcast, nguyên văn cấu hình, bỏ dấu `/` cuối |
| `hub.topic` | string (UUID) | cùng điều kiện `hub.url` | ổn định theo chủ thể token (user hoặc ClientApplication) chừng nào còn xin token ít nhất mỗi giờ; lưu ở Redis nếu `AUTH_REDIS_URL` đặt. Không lấy được: `500 server_error` và **không cấp token** (mục AC23, CC12, PA18) |

**Claim của ba loại token** (khóa ký: `RS256`, `ES256` hoặc `ES384`; header `kid`, `typ=JWT`)

| Claim | Access token | ID token | Refresh token | Ý nghĩa và khi nào có |
|---|---|---|---|---|
| `iss` | có | có | có | `AUTH_ISSUER` (mặc định `AUTH_BASE_URL`), đúng từng ký tự |
| `aud` | `AUTH_AUDIENCE` (mặc định issuer); chuỗi nếu một audience, mảng nếu nhiều | id client (chỉ khi Login có client) | `<issuer>/oauth2/token` | refresh token có `aud` riêng để không dùng nhầm làm Bearer |
| `sub` | id `User` (hoặc id `ClientApplication` ở `client_credentials`) | như access | **không có** | chủ thể |
| `username` | = `sub` | không | không | chỉ access token |
| `client_id` | khi Login có client | khi Login có client | khi Login có client | id ClientApplication |
| `login_id` | có | có | có | id Login; khóa để tra trạng thái và thu hồi |
| `profile` | khi membership có profile | không (dùng `fhirUser`) | không | tham chiếu hồ sơ, ví dụ `Practitioner/<id>` |
| `fhirUser` | không | khi membership có profile | không | cùng giá trị với `profile` |
| `scope` | scope Login **cộng** scope vai trò (`fhir.admin`, `fhir.read` hoặc `fhir.write`; không thêm khi có scope SMART) | không | không | quyền cho fhir-server |
| `fhir_project` | có | không | không | id project của membership; fhir-server cách ly dữ liệu theo đây |
| `fhir_access_policy` | khi membership có AccessPolicy | không | không | `[{resourceType, criteria, readonly, deny}]`, chỉ **thu hẹp** quyền; nhiều rule cùng loại là OR khi bật `AUTH_PATIENT_COMPARTMENT_ENABLED` |
| `email` | khi scope có term `email` và User có email | như access | không | email của User |
| `nonce` | không | có (có thể rỗng) | không | `nonce` của Login |
| `auth_time` | không | khi parse được | không | thời điểm đăng nhập (Unix giây) |
| `refresh_secret` | không | không | có | bí mật xoay vòng, so với `Login.refreshSecret`; access token có claim này bị từ chối ở Bearer |
| `token_use` | `access` | `id` | `refresh` | loại token; Bearer và `id_token_hint` kiểm riêng |
| `jti` | có | có | có | chuỗi ngẫu nhiên mỗi token |
| `iat`, `nbf` | giây Unix lúc cấp | như access | như access | |
| `exp` | `iat` + `AUTH_ACCESS_TTL` (hoặc `accessTokenLifetime` của client) | `iat` + `AUTH_ID_TTL` (1 giờ) | `iat` + `AUTH_REFRESH_TTL` (14 ngày, hoặc `refreshTokenLifetime` của client) | |

**Lưu ý tích hợp (cả route):**
- Chỉ nhận form. Tham số trong query string không được đọc.
- Mọi refresh phải **thay token cũ bằng token mới ngay** và tuần tự: hai tiến trình cùng dùng một refresh token ở hai IP khác nhau (hoặc cách nhau quá 30 giây) sẽ làm Login bị thu hồi.
- Đổi code hai lần (kể cả do người dùng bấm hai lần hoặc trình duyệt thử lại) thu hồi Login: phải bắt đầu lại từ `/oauth2/authorize`.
- `error_description` chỉ để lập trình viên, không rẽ nhánh logic; rẽ nhánh theo `error` và HTTP.
- Lỗi `500` không lộ chi tiết; nêu `X-Request-Id` khi báo.

---

<a id="get-oauth2-userinfo"></a>
## `GET /oauth2/userinfo`

OIDC userinfo cho login của Bearer. Trả `sub`; thêm theo **từ** trong scope của **Login** (không phải scope claim của access token): `profile`, `email`. Không có phone, address, birthdate, gender, picture (hồ sơ lâm sàng ở fhir-server). [Tham chiếu trường](../reference/oauth.md#get-oauth2-userinfo)

**Cờ bật route:** luôn bật.

**Thứ tự kiểm tra:** hạn mức IP; Bearer (B1, B2); đọc User nếu chủ thể là `User/...`; dựng claim theo scope.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| U1 | Chỉ `openid` | scope không có `profile` hay `email` | 200 | - | `{"sub":"<id>"}` (đúng một khóa) | không |
| U2 | Có `profile` | scope có từ `profile` | 200 | - | thêm `locale` (cố định `en-US`), `profile` (khi membership có), `updated_at` (từ `meta.lastUpdated` của User), `name`, `given_name`, `family_name` (khi có tên) | không |
| U3 | Có `email` | scope có từ `email`, User có email | 200 | - | thêm `email`, `email_verified` (bool) | không |
| U4 | Khớp theo từ | scope `emailx` | 200 | - | **không** có `email` (khớp từ, không khớp chuỗi con) | không |
| U5 | Login của client | token của `client_credentials` | 200 | - | `sub` = id ClientApplication; có `profile` nếu membership có; không có `email`, `given_name` (không có User) | không |
| U6 | Thiếu hoặc sai Bearer | B1, B2 (cả id token, refresh token dùng làm Bearer, Login đã thu hồi) | 401 | `WWW-Authenticate` | OperationOutcome `Unauthorized` | không |
| U7 | Lỗi đọc User | `Users.Get` lỗi | 500 hoặc mã của lỗi nghiệp vụ | - | OperationOutcome (`Internal server error` với lỗi lạ) (chưa kiểm) | không |
| U8 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
GET /oauth2/userinfo HTTP/1.1
Host: auth.example.org
Authorization: Bearer <access token>
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store

{"email":"admin@example.org","email_verified":false,"family_name":"Nguyen","given_name":"An","locale":"en-US","name":"An Nguyen","profile":"Practitioner/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77","sub":"3f2a8c1e-0000-4000-8000-000000000000","updated_at":1763900000}
```

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/fhir+json; charset=utf-8
WWW-Authenticate: Bearer realm="auth", error="invalid_token"

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"login","details":{"text":"Unauthorized"},"diagnostics":"Unauthorized"}]}
```

**Lưu ý tích hợp:** lỗi `401` ở route này là OperationOutcome, không phải JSON OAuth. Claim trả theo scope **Login**: xin `openid profile email` ở `/authorize` mới thấy tên và email.

<a id="post-oauth2-userinfo"></a>
## `POST /oauth2/userinfo`

Cùng handler với [GET](#get-oauth2-userinfo); không đọc body. [Tham chiếu trường](../reference/oauth.md#post-oauth2-userinfo)

**Cờ bật route:** luôn bật.

**Thứ tự kiểm tra và bảng trường hợp:** giống hệt GET (U1 đến U8). Test `TestUserInfoFollowsLoginScopeByWord` khẳng định `POST` trả `200`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| U1 | Thành công | Bearer hợp lệ | 200 | - | như GET | không |
| U6 | Thiếu hoặc sai Bearer | B1, B2 | 401 | `WWW-Authenticate` | OperationOutcome `Unauthorized` | không |

**Ví dụ**

```http
POST /oauth2/userinfo HTTP/1.1
Host: auth.example.org
Authorization: Bearer <access token>
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8

{"sub":"3f2a8c1e-0000-4000-8000-000000000000"}
```

**Lưu ý tích hợp:** dùng POST khi client không gửi được GET kèm header; nội dung không khác.

---

<a id="post-oauth2-introspect"></a>
## `POST /oauth2/introspect`

Kiểm tra một token (RFC 7662). Người gọi phải là Bearer hợp lệ; chỉ hỏi được token của **cùng project và cùng client** với người gọi. Mọi lý do token không dùng được đều ra đúng `{"active": false}`, không phân biệt, để không dò được thông tin về token không phải của mình. [Tham chiếu trường](../reference/oauth.md#post-oauth2-introspect)

**Cờ bật route:** luôn bật.

**Thứ tự kiểm tra:**
1. Hạn mức IP; Bearer của người gọi (B1, B2).
2. Lấy `token`: `Content-Type: application/json` thì từ JSON `{"token": "..."}` (JSON hỏng bị bỏ qua, coi như rỗng); ngược lại từ form body (query string không đọc).
3. `token` rỗng hoặc chỉ khoảng trắng: `400` văn bản `Token to introspect must be provided`.
4. Đánh giá token, mọi nhánh lỗi đều `{"active": false}`: chữ ký/hết hạn; `iss` lạ; loại token (chỉ chấp nhận access token và refresh token; id token là inactive); `aud` sai; access token có `refresh_secret`; thiếu `login_id`; Login không có, chưa `granted`, đã thu hồi, không có membership; membership/project tắt; token khác project hoặc khác client với người gọi; refresh token đã xoay (secret không khớp secret hiện hành).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| I1 | Access token hoạt động | hợp lệ, cùng project và client | 200 | `Cache-Control: no-store` | `{"active":true,"iat","exp","iss","sub","scope","client_id"?,"patient"?}`; `scope` = claim scope của access token (gồm `fhir.*` vai trò) | không |
| I2 | Refresh token hiện hành | secret khớp `Login.refreshSecret` | 200 | như I1 | như I1 nhưng `sub` = id User của Login, `scope` = scope của Login | không (không xoay secret) |
| I3 | Không hoạt động | mọi lý do ở bước 4 | 200 | như I1 | đúng `{"active":false}` (một khóa) | không |
| I4 | Thiếu token | rỗng, khoảng trắng, JSON hỏng, form không có `token` | 400 | `Content-Type: text/plain` | văn bản `Token to introspect must be provided` | không |
| I5 | Thiếu hoặc sai Bearer | B1, B2 | 401 | `WWW-Authenticate` | OperationOutcome `Unauthorized` | không |
| I6 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

`patient` là **id trần** (ví dụ `p7`), không kèm `Patient/` (khác ví dụ của swagger); lấy từ profile `Patient/<id>` của membership hoặc từ SmartAppLaunch của Login. `client_id` chỉ có khi token có `client_id`.

**Ví dụ**

```http
POST /oauth2/introspect HTTP/1.1
Host: auth.example.org
Authorization: Bearer <access token của người gọi>
Content-Type: application/x-www-form-urlencoded

token=<access hoặc refresh token cần hỏi>
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store

{"active":true,"client_id":"a3b1c2d4-0000-4000-8000-000000000001","exp":1763903600,"iat":1763900000,"iss":"https://auth.example.org","patient":"p7","scope":"openid fhir.admin","sub":"3f2a8c1e-0000-4000-8000-000000000000"}
```

```http
POST /oauth2/introspect HTTP/1.1
Host: auth.example.org
Authorization: Bearer <access token của người gọi>
Content-Type: application/json

{"token":"<token đã thu hồi>"}
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store

{"active":false}
```

```http
HTTP/1.1 400 Bad Request
Content-Type: text/plain; charset=UTF-8

Token to introspect must be provided
```

**Lưu ý tích hợp:** `active:false` không cho biết vì sao (hết hạn, thu hồi, khác client...). Resource server của fhir-server **không** gọi route này: nó chỉ xác minh chữ ký bằng JWKS, nên token đã thu hồi vẫn được fhir-server nhận tới khi hết hạn (cần introspect nếu muốn kiểm ngay).

---

<a id="get-oauth2-logout"></a>
## `GET /oauth2/logout`

Đăng xuất: thu hồi Login của access token **và chuỗi Login tổ tiên** theo `basedOn` (tối đa 16), rồi xóa cookie `medplum-<client_id>`. [Tham chiếu trường](../reference/oauth.md#get-oauth2-logout)

**Cờ bật route:** luôn bật.

**Thứ tự kiểm tra:** hạn mức IP; Bearer (B1, B2); **GHI** thu hồi từng Login trong chuỗi (bỏ qua Login đã thu hồi; dừng khi `basedOn` không phải `Login/...`, hoặc Login cha không tồn tại; lỗi đọc cha khác là `500`); xóa cookie nếu Login có client; trả `All OK`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| L1 | Thành công | Bearer hợp lệ | 200 | `Content-Type: application/fhir+json; charset=utf-8`; `Set-Cookie: medplum-<clientId>=; Path=/; Max-Age=0; HttpOnly` (kèm `Secure; SameSite=None` khi `AUTH_COOKIE_SECURE=true`, không thì `SameSite=Lax`) khi Login có client | OperationOutcome `id: ok`, `details.text` `All OK` | **thu hồi** Login của token và các Login tổ tiên (phiên trình duyệt chấm dứt); refresh token của chúng bị `invalid_grant` `Token revoked`; userinfo, introspect, `/auth/me` với access token này bị 401 ngay |
| L2 | Login không có client | Login không có `client` | 200 | không có `Set-Cookie` | như L1 | thu hồi như L1 |
| L3 | Login của client_credentials | Login có `client` | 200 | `Set-Cookie` xóa cookie như L1 | như L1 | thu hồi Login |
| L4 | Gọi lần hai | cùng access token | 401 | `WWW-Authenticate: Bearer realm="auth", error="invalid_token"` | OperationOutcome `Unauthorized` | không (Login đã thu hồi) |
| L5 | Thiếu hoặc sai Bearer | B1, B2 | 401 | `WWW-Authenticate` | OperationOutcome `Unauthorized` | không |
| L6 | Lỗi ghi hoặc đọc cha | DB lỗi | 500 | - | OperationOutcome `exception` `Internal server error` | có thể đã thu hồi một phần chuỗi |
| L7 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
GET /oauth2/logout HTTP/1.1
Host: auth.example.org
Authorization: Bearer <access token>
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=None

{"resourceType":"OperationOutcome","id":"ok","issue":[{"severity":"information","code":"informational","details":{"text":"All OK"}}]}
```

**Lưu ý tích hợp:** các Login **con** của tổ tiên (ứng dụng khác dùng chung phiên) chưa bị thu hồi vì Login chưa có search param `based-on` để liệt kê. Sau logout, xóa token phía client. Cần gọi bằng access token còn hiệu lực: token đã hết hạn thì nhận `401` và không thu hồi được.

<a id="post-oauth2-logout"></a>
## `POST /oauth2/logout`

Cùng handler với [GET](#get-oauth2-logout); không đọc body. [Tham chiếu trường](../reference/oauth.md#post-oauth2-logout)

**Cờ bật route:** luôn bật.

**Thứ tự kiểm tra và bảng trường hợp:** giống hệt GET (L1 đến L7); test `TestLogoutRevokesLoginAndClearsCookie` dùng POST làm đường chính và GET làm đường phụ.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| L1 | Thành công | Bearer hợp lệ | 200 | `Set-Cookie` xóa `medplum-<clientId>` (có `Max-Age=0`, `HttpOnly`) | OperationOutcome `All OK` | thu hồi Login và chuỗi tổ tiên |
| L5 | Thiếu hoặc sai Bearer | B1, B2 | 401 | `WWW-Authenticate` | OperationOutcome `Unauthorized` | không |

**Ví dụ**

```http
POST /oauth2/logout HTTP/1.1
Host: auth.example.org
Authorization: Bearer <access token>
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
Set-Cookie: medplum-a3b1c2d4-0000-4000-8000-000000000001=; Path=/; Max-Age=0; HttpOnly

{"resourceType":"OperationOutcome","id":"ok","issue":[{"severity":"information","code":"informational","details":{"text":"All OK"}}]}
```

**Lưu ý tích hợp:** nên dùng POST (logout bằng GET có thể bị kích hoạt vô ý bởi thẻ ảnh/prefetch, tuy nhiên vẫn cần Bearer nên không thể bị CSRF qua cookie).

---

<a id="post-oauth2-register"></a>
## `POST /oauth2/register`

**Không** phải đăng ký động theo RFC 7591: không tạo client mới, không trả `client_secret`. Chỉ **tra client dựng sẵn** theo redirect URI (cho công cụ như MCP, CLI). [Tham chiếu trường](../reference/oauth.md#post-oauth2-register)

**Cờ bật route:** chỉ có khi `AUTH_OAUTH_REGISTER_ENABLED=true` (mặc định tắt: route không tồn tại, trả `404` `{"error":"Not Found"}`; test chấp nhận `404` hoặc `405`). Khi bật, discovery khai thêm `registration_endpoint`. Client dựng sẵn lấy từ `AUTH_DEFAULT_OAUTH_CLIENTS` (JSON) cộng client CLI dựng sẵn (redirect URI `http://localhost:9615`); client lưu trong kho **không** được tra ở đây.

**Thứ tự kiểm tra:**
1. Hạn mức IP; giới hạn body 1 MB.
2. Đọc body JSON, **không** kiểm `Content-Type`; JSON hỏng hoặc `redirect_uris` có phần tử không phải chuỗi thì coi như không có danh sách.
3. Cờ đăng ký tắt trong service: `invalid_request`/`Registration is not enabled` (không thể thấy khi route bật bằng cùng cờ).
4. Danh sách rỗng hoặc phần tử đầu rỗng: `Redirect URI is required`.
5. Phần tử **đầu** phải bằng chính xác (không `/` cuối, không query, phân biệt hoa thường) redirect URI của một client dựng sẵn: không thì `Invalid redirect URI`.
6. Trả `201` với phần tử đầu (các phần tử sau bị bỏ qua).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| R1 | Thành công | `redirect_uris[0]` khớp chính xác client dựng sẵn | 201 | `Content-Type: application/json` | `{"client_id","client_id_issued_at","redirect_uris":[<một phần tử>]}`, không có `client_secret` | không tạo gì |
| R2 | Thiếu redirect URI | `{}`, `[]`, phần tử không phải chuỗi (`[1]`), JSON hỏng, phần tử đầu rỗng | 400 | `Cache-Control: no-store`, `Pragma: no-cache` | `invalid_request` / `Redirect URI is required` | không |
| R3 | Redirect URI lạ | không khớp client dựng sẵn nào (thêm `/`, thêm query, đổi hoa thường, host lạ, client trong kho) | 400 | như R2 | `invalid_request` / `Invalid redirect URI` | không |
| R4 | Đăng ký tắt trong service | cờ service tắt | 400 | như R2 | `invalid_request` / `Registration is not enabled` | không |
| R5 | Route tắt | `AUTH_OAUTH_REGISTER_ENABLED` không bật | 404 | - | `{"error":"Not Found"}` | không |
| R6 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
POST /oauth2/register HTTP/1.1
Host: auth.example.org
Content-Type: application/json

{"redirect_uris":["https://claude.example/callback"]}
```

```http
HTTP/1.1 201 Created
Content-Type: application/json; charset=UTF-8

{"client_id":"mcp","client_id_issued_at":1763900000,"redirect_uris":["https://claude.example/callback"]}
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
Pragma: no-cache

{"error":"invalid_request","error_description":"Invalid redirect URI"}
```

**Lưu ý tích hợp:** mã lỗi là `invalid_request` (không phải `invalid_client_metadata` như mô tả của trường `redirect_uris` trong swagger). `client_id_issued_at` là thời điểm trả lời, không phải thời điểm tạo client. Client không có `secret` ở đây: dùng PKCE ở `/oauth2/authorize` và `/oauth2/token`.

---

## Phát hiện lệch với common.md

| Nơi | Tài liệu nói | Điều đúng theo mã |
|---|---|---|
| common.md mục 4, bảng `error` | `unsupported_grant_type` dùng khi `grant_type` lạ | `grant_type` lạ trả `invalid_request` / `Unsupported grant_type`; mã `unsupported_grant_type` không bao giờ được phát (`token` trong `oauth.go`) |
| common.md mục 4 | `invalid_scope` khi "scope chứa term `fhir.*` hoặc term bị cấm" (chung cho token) | Chỉ `client_credentials` kiểm (`ValidateRequestedScope`) và chặn `patient/`. `authorization_code`, `refresh_token`, `pre-authorized_code`, `token-exchange` lấy scope từ Login và **không** lọc `fhir.*`; `/oauth2/authorize` và `/auth/login` cũng không lọc. Kiểm bằng test tạm (đã xóa): login với `scope="openid fhir.admin"` rồi đổi code cho `200` với claim `scope` = `openid fhir.admin fhir.admin` (term tự xin được giữ nguyên, cộng scope vai trò). Với user không phải admin project, term `fhir.admin` tự xin đi thẳng vào access token: nghi **lỗ hổng leo quyền**, cần chủ dự án xác nhận |
| common.md mục 4 | `userinfo`, `introspect`, `logout` trả `401` (dạng JSON OAuth theo ngữ cảnh) | Ba route trả `401` dạng **OperationOutcome** `application/fhir+json` (`issue[0].code=login`, `Unauthorized`) kèm `WWW-Authenticate`, không phải `{error,error_description}`. Swagger của `userinfo` và `introspect` ghi `401` là object tự do (thực tế là OperationOutcome) |
| common.md mục 4 | `invalid_client` "chỉ ở `pre-authorized_code`" | Đúng cho client không tồn tại (`PA4`); nhưng client **tắt** ở cùng grant là `invalid_request`/`Invalid client` (`PA5`), và HTTP luôn `400` |
| common.md mục 4 | `access_denied` khi "`ipAccessRule` chặn" | `ipAccessRule` chặn ở mọi grant là `invalid_request` / `IP address not allowed` (HTTP 400); `access_denied` chỉ cho membership/project tắt (`Profile not active`, `Project not active`) và AccessPolicy không dịch được. Riêng nhánh phiên của `/oauth2/authorize` dùng `access_denied` / `IP address not allowed` |
| [../oauth.md](../oauth.md) (đầu tệp, bảng grant và mục "Phần KHÔNG có") | liệt kê bốn grant và ghi `token-exchange` "không có" | Mã **có** grant `urn:ietf:params:oauth:grant-type:token-exchange` (discovery khai, test `TestHTTPTokenExchangeGrant`); swagger `POST /oauth2/token` chưa mô tả nó và `pre-authorized_code` (chỉ ghi ba grant) |
| [../oauth.md](../oauth.md) mục `POST /oauth2/token` | "Cả bốn grant kiểm `ipAccessRule`" | `authorization_code` **không** kiểm `ipAccessRule` ở `/oauth2/token` (kiểm lúc `/auth/login` và ở `authorize` nhánh phiên); ba grant còn lại có kiểm |
| [../oauth.md](../oauth.md) (cuối) | "Phần KHÔNG có: ... `hub.url`/`hub.topic` của FHIRcast" | Mã **có** `hub.url`/`hub.topic` (`addHub`; cùng tệp tự mô tả ở đoạn trên); tệp mâu thuẫn với chính nó |
| swagger, trường `redirect_uris` của `RegisterBody` | lỗi `400 invalid_client_metadata` | mã trả `invalid_request` (`Redirect URI is required`, `Invalid redirect URI`) |
| swagger, ví dụ `patient` của `IntrospectResponse` | `Patient/3f2a...` | mã trả id trần (test: `p7`) |
| common.md mục 2 (header phản hồi) | `Cache-Control: no-store` mọi route | `POST /oauth2/token` còn đặt `Pragma: no-cache` (cả lỗi OAuth lẫn thành công); không nêu ở common.md |

**Chưa kiểm** (không có test hoặc cần môi trường): header `Allow` của `405`; thông điệp `TryLogin` ở `token-exchange` (TE12); thông điệp khi `Users.Get` lỗi ở `userinfo` (U7); `Invalid client` vs thông điệp khác ở CC6 với membership không duy nhất.
