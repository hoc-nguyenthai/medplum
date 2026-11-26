# auth: các trường hợp của nhóm `well-known`

Sáu tài liệu công khai để client và fhir-server tự cấu hình: JWKS, discovery OIDC/OAuth 2.0, protected resource (RFC 9728), SMART. Quy ước chung (header, envelope, hạn mức) ở [../common.md](../common.md); bảng trường ở [../reference/well-known.md](../reference/well-known.md). Nguồn: `internal/presentation/httpapi/oauth.go` (`jwks`, `serveJSONCached`, `openidConfiguration`, `smartConfiguration`, `smartStyles`, `protectedResource`, `serveJSON`), `internal/infrastructure/keys/keys.go`, test `internal/bootstrap/oauth_test.go` (`TestWellKnownAndJWKS`), `oauth_authorize_test.go`, `oauth_preauth_test.go`.

**Điểm chung của cả sáu route**

- Không xác thực, không tham số bắt buộc, chỉ `GET`. Luôn có mặt khi service chạy (đăng ký cùng `/oauth2/token`).
- Thuộc phạm vi hạn mức IP `/.well-known/` (`AUTH_RATE_LIMIT`) và CORS: vượt hạn mức là `429` `{"error":"Too many requests"}` kèm `Retry-After: 1` (xem common.md mục 7); sai method là `405` `{"error":"Method Not Allowed"}` (chưa kiểm header `Allow`).
- Hai kiểu cache khác nhau: **JWKS** có `ETag` và hỗ trợ `If-None-Match` (304); các tài liệu còn lại chỉ có `Cache-Control: public, max-age=300`, **không** có `ETag`, `If-None-Match` bị bỏ qua (luôn `200`).

| Route | `Cache-Control` | `ETag` / 304 | Content-Type | Phụ thuộc cấu hình |
|---|---|---|---|---|
| `jwks.json` | `public, max-age=300` | có / có | `application/json; charset=UTF-8` | thư mục khóa `AUTH_SIGNING_KEY_PATH` |
| `openid-configuration`, `oauth-authorization-server` | `public, max-age=300` | không | `application/json` | `AUTH_BASE_URL`, `AUTH_ISSUER`, `AUTH_APP_BASE_URL`, `AUTH_OAUTH_REGISTER_ENABLED`, `AUTH_ALLOW_PLAIN_PKCE` |
| `smart-configuration` | như trên | không | `application/json` | như trên |
| `smart-styles.json` | như trên | không | `application/json` | không (giá trị cố định) |
| `oauth-protected-resource` | như trên (riêng `400` giữ `no-store`) | không | `application/json`; `400` là `text/plain` | `AUTH_BASE_URL`, `AUTH_ISSUER` |

<a id="get-well-known-jwks-json"></a>
## `GET /.well-known/jwks.json`

Công bố **chỉ phần công khai** của mọi khóa ký trong thư mục khóa, để fhir-server, ws-gateway và client xác minh chữ ký token do auth cấp. [Tham chiếu trường](../reference/well-known.md#get-well-known-jwks-json)

**Cờ bật route:** luôn bật. Khóa nạp từ `AUTH_SIGNING_KEY_PATH` (mọi tệp `*.pem`) **một lần khi khởi động**; không có đường dẫn này (chỉ dev/test) thì dùng một khóa ES256 tạm trong bộ nhớ.

**Thứ tự kiểm tra:**
1. Hạn mức IP (429).
2. Dựng body từ bộ khóa hiện có (`Store.JWKS`: sắp theo `kid`, mỗi khóa có `kty`, `kid`, `alg`, `use=sig` và tham số công khai; không bao giờ có `d`, `p`, `q`, `dp`, `dq`, `qi`).
3. Tính `ETag` = `"` + 16 ký tự hex đầu của SHA-256 của body + `"`; đặt `Cache-Control` và `ETag`.
4. `If-None-Match` **bằng đúng chuỗi** `ETag` thì `304` không body; ngược lại `200` kèm body.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Lấy khóa | không có `If-None-Match` hoặc giá trị khác ETag hiện tại | 200 | `Cache-Control: public, max-age=300`, `ETag: "<16 hex>"` | `{"keys":[{kty,kid,alg,use,...}]}` | không |
| C2 | Chưa đổi | `If-None-Match` đúng bằng ETag hiện tại | 304 | `Cache-Control: public, max-age=300`, `ETag` | rỗng | không |
| C3 | `If-None-Match` dạng khác | `W/"..."`, danh sách phân cách dấu phẩy, hoặc `*` | 200 | như C1 | như C1 | không (mã chỉ so bằng chuỗi nguyên văn, không hiểu các dạng này) |
| C4 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm hạn mức |

**Ví dụ**

```http
GET /.well-known/jwks.json HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: public, max-age=300
ETag: "3f9a1c2b7d4e5a60"

{"keys":[{"use":"sig","kty":"EC","kid":"mQ1x0v8Zr3bK9lT4uW2yN7pD5sHf6aJcEoGiBtXkLwU","crv":"P-256","alg":"ES256","x":"f83OJ3D2xF1Bg8vYvZ3m1yq0nT6dKQ5hRk2sLwXaP7c","y":"x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0"}]}
```

```http
GET /.well-known/jwks.json HTTP/1.1
Host: auth.example.org
If-None-Match: "3f9a1c2b7d4e5a60"
```

```http
HTTP/1.1 304 Not Modified
Cache-Control: public, max-age=300
ETag: "3f9a1c2b7d4e5a60"
```

**Xoay khóa** (theo [runbook mục 6](../../runbook.md)): `auth keys generate` (khóa mới vào cùng thư mục) rồi triển khai để JWKS có **cả hai** khóa (ETag đổi vì body đổi); chờ quá TTL cache của người xác minh (runbook ghi 10 phút; riêng cache HTTP của JWKS là 300 giây); đặt `AUTH_ACTIVE_KID` = kid mới (nhiều khóa mà không đặt thì service từ chối khởi động: "có nhiều khóa: đặt AUTH_ACTIVE_KID để chọn khóa ký") và triển khai lại; khi mọi token cũ hết hạn mới xóa tệp khóa cũ. Mỗi token mang `kid` trong header (thumbprint RFC 7638) nên người xác minh chọn đúng khóa. Refresh token sống tới `AUTH_REFRESH_TTL` (14 ngày) vẫn cần khóa cũ: xóa sớm sẽ buộc đăng nhập lại.

**Lưu ý tích hợp:**
- Thuật toán ký thực tế là một trong `RS256`, `ES256`, `ES384` (khóa đang ký); `AUTH_SIGNING_ALG` mặc định `RS256`. Giữ `kid` làm khóa tra, đừng mặc định `alg`.
- Gặp `kid` lạ: tải lại JWKS một lần (không dò liên tục), vì khóa mới chỉ xuất hiện sau khi triển khai.
- `FHIR_JWKS_URL` của fhir-server phải trỏ tới địa chỉ nó với tới được (có thể là địa chỉ nội bộ).

<a id="get-well-known-openid-configuration"></a>
## `GET /.well-known/openid-configuration`

Tài liệu discovery OIDC/OAuth 2.0. **Chỉ khai những gì đã cài** (khác Medplum, vốn khai thêm `id_token`, HS256, pairwise). [Tham chiếu trường](../reference/well-known.md#get-well-known-openid-configuration)

**Cờ bật route và cờ đổi nội dung:**
- `AUTH_APP_BASE_URL` đặt: thêm `authorization_endpoint` và đổi `response_types_supported` từ `[]` thành `["code"]`.
- `AUTH_OAUTH_REGISTER_ENABLED=true`: thêm `registration_endpoint`.
- `AUTH_ALLOW_PLAIN_PKCE=true`: `code_challenge_methods_supported` thành `["S256","plain"]` (mặc định `["S256"]`).

**Khóa thực sự được trả** (mọi giá trị URL dựng từ `AUTH_BASE_URL` đã bỏ dấu `/` cuối, riêng `issuer` là `AUTH_ISSUER`, mặc định bằng `AUTH_BASE_URL`):

| Khóa | Giá trị |
|---|---|
| `issuer` | `AUTH_ISSUER` (đúng từng ký tự, cũng là `iss` của token) |
| `token_endpoint` | `<base>/oauth2/token` |
| `userinfo_endpoint` | `<base>/oauth2/userinfo` |
| `introspection_endpoint` | `<base>/oauth2/introspect` |
| `jwks_uri` | `<base>/.well-known/jwks.json` |
| `id_token_signing_alg_values_supported` | một phần tử: thuật toán của khóa đang ký (`RS256`, `ES256` hoặc `ES384`) |
| `grant_types_supported` | `authorization_code`, `refresh_token`, `client_credentials`, `urn:ietf:params:oauth:grant-type:pre-authorized_code`, `urn:ietf:params:oauth:grant-type:token-exchange` (đúng thứ tự này, năm phần tử) |
| `response_types_supported` | `[]`, hoặc `["code"]` khi có `AUTH_APP_BASE_URL` |
| `subject_types_supported` | `["public"]` |
| `scopes_supported` | `openid`, `profile`, `email`, `phone`, `address` (danh sách tĩnh; xem lưu ý) |
| `token_endpoint_auth_methods_supported` | `client_secret_basic`, `client_secret_post`, `private_key_jwt`, `none` |
| `token_endpoint_auth_signing_alg_values_supported` | `RS256`, `RS384`, `RS512`, `ES256`, `ES384`, `ES512` |
| `code_challenge_methods_supported` | `["S256"]` hoặc `["S256","plain"]` |
| `authorization_endpoint` | `<base>/oauth2/authorize`; **chỉ khi** có `AUTH_APP_BASE_URL` |
| `registration_endpoint` | `<base>/oauth2/register`; **chỉ khi** `AUTH_OAUTH_REGISTER_ENABLED=true` |

Không khai (chưa cài): `end_session_endpoint`, `revocation_endpoint`, `claims_supported`, `request_parameter_supported`, `device_authorization_endpoint`, mTLS.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Có `/oauth2/authorize` | `AUTH_APP_BASE_URL` đặt | 200 | `Cache-Control: public, max-age=300` | có `authorization_endpoint`, `response_types_supported=["code"]` | không |
| C2 | Không có `/oauth2/authorize` | không đặt `AUTH_APP_BASE_URL` | 200 | như C1 | không có `authorization_endpoint`, `response_types_supported=[]` | không |
| C3 | Có đăng ký client dựng sẵn | `AUTH_OAUTH_REGISTER_ENABLED=true` | 200 | như C1 | thêm `registration_endpoint` | không |
| C4 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
GET /.well-known/openid-configuration HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: public, max-age=300

{
  "issuer": "https://auth.example.org",
  "token_endpoint": "https://auth.example.org/oauth2/token",
  "userinfo_endpoint": "https://auth.example.org/oauth2/userinfo",
  "introspection_endpoint": "https://auth.example.org/oauth2/introspect",
  "jwks_uri": "https://auth.example.org/.well-known/jwks.json",
  "id_token_signing_alg_values_supported": ["RS256"],
  "grant_types_supported": ["authorization_code", "refresh_token", "client_credentials", "urn:ietf:params:oauth:grant-type:pre-authorized_code", "urn:ietf:params:oauth:grant-type:token-exchange"],
  "response_types_supported": ["code"],
  "subject_types_supported": ["public"],
  "scopes_supported": ["openid", "profile", "email", "phone", "address"],
  "token_endpoint_auth_methods_supported": ["client_secret_basic", "client_secret_post", "private_key_jwt", "none"],
  "token_endpoint_auth_signing_alg_values_supported": ["RS256", "RS384", "RS512", "ES256", "ES384", "ES512"],
  "code_challenge_methods_supported": ["S256"],
  "authorization_endpoint": "https://auth.example.org/oauth2/authorize"
}
```

(Thứ tự khóa trong JSON thật là chữ cái vì Go sắp xếp map; ví dụ trên sắp theo nghĩa cho dễ đọc.)

**Lưu ý tích hợp:**
- `scopes_supported` là danh sách tĩnh: `phone` và `address` có trong danh sách nhưng `userinfo` **không** trả `phone_number` hay `address` (hồ sơ lâm sàng nằm ở fhir-server). Đừng suy ra claim từ danh sách này.
- `grant_types_supported` có `token-exchange` dù swagger của `POST /oauth2/token` chỉ mô tả ba grant: xem mục "Phát hiện lệch" của [oauth.md](oauth.md#phát-hiện-lệch-với-commonmd).
- Client nên cache theo `max-age`, không cần `If-None-Match` (không có `ETag`).

<a id="get-well-known-oauth-authorization-server"></a>
## `GET /.well-known/oauth-authorization-server`

Cùng handler và **cùng nội dung** với `openid-configuration` (RFC 8414 dùng đường dẫn này). [Tham chiếu trường](../reference/well-known.md#get-well-known-oauth-authorization-server)

**Cờ bật route:** như `openid-configuration`.

**Thứ tự kiểm tra và bảng trường hợp:** giống hệt [GET /.well-known/openid-configuration](#get-well-known-openid-configuration) (C1 đến C4), không có tham số riêng. Test `TestWellKnownAndJWKS` khẳng định `issuer` của hai đường dẫn bằng nhau.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | mọi yêu cầu | 200 | `Cache-Control: public, max-age=300` | giống openid-configuration | không |
| C2 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
GET /.well-known/oauth-authorization-server HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: public, max-age=300

{"issuer":"https://auth.example.org","token_endpoint":"https://auth.example.org/oauth2/token", "...":"như openid-configuration"}
```

**Lưu ý tích hợp:** chọn một trong hai đường dẫn, nội dung không khác nhau.

<a id="get-well-known-smart-configuration"></a>
## `GET /.well-known/smart-configuration`

Discovery SMART on FHIR, khai cùng endpoint với `openid-configuration` nhưng đúng tập khóa SMART. [Tham chiếu trường](../reference/well-known.md#get-well-known-smart-configuration)

**Cờ bật route và cờ đổi nội dung:** như `openid-configuration`: `AUTH_APP_BASE_URL` thêm `authorization_endpoint` và đổi `response_types_supported` thành `["code"]`; `AUTH_OAUTH_REGISTER_ENABLED` thêm `registration_endpoint`; `AUTH_ALLOW_PLAIN_PKCE` thêm `plain`.

**Khóa thực sự được trả:**

| Khóa | Giá trị |
|---|---|
| `issuer` | `AUTH_ISSUER` |
| `jwks_uri` | `<base>/.well-known/jwks.json` |
| `token_endpoint` | `<base>/oauth2/token` |
| `introspection_endpoint` | `<base>/oauth2/introspect` |
| `grant_types_supported` | giống openid-configuration (năm grant) |
| `token_endpoint_auth_methods_supported` | `client_secret_basic`, `client_secret_post`, `private_key_jwt`, `none` |
| `token_endpoint_auth_signing_alg_values_supported` | `RS256`, `RS384`, `RS512`, `ES256`, `ES384`, `ES512` |
| `code_challenge_methods_supported` | `["S256"]` (+ `"plain"` khi bật) |
| `scopes_supported` | `user/*.cruds`, `system/*.cruds`, `openid` |
| `response_types_supported` | `[]` hoặc `["code"]` |
| `capabilities` | `client-confidential-symmetric`, `permission-v1`, `permission-v2` |
| `authorization_endpoint` | chỉ khi có `AUTH_APP_BASE_URL` |
| `registration_endpoint` | chỉ khi `AUTH_OAUTH_REGISTER_ENABLED=true` |

Không có `userinfo_endpoint` ở tài liệu này (chỉ có ở openid-configuration).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | mọi yêu cầu | 200 | `Cache-Control: public, max-age=300` | như bảng trên | không |
| C2 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

**Ví dụ**

```http
GET /.well-known/smart-configuration HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: public, max-age=300

{
  "issuer": "https://auth.example.org",
  "jwks_uri": "https://auth.example.org/.well-known/jwks.json",
  "token_endpoint": "https://auth.example.org/oauth2/token",
  "introspection_endpoint": "https://auth.example.org/oauth2/introspect",
  "grant_types_supported": ["authorization_code", "refresh_token", "client_credentials", "urn:ietf:params:oauth:grant-type:pre-authorized_code", "urn:ietf:params:oauth:grant-type:token-exchange"],
  "token_endpoint_auth_methods_supported": ["client_secret_basic", "client_secret_post", "private_key_jwt", "none"],
  "token_endpoint_auth_signing_alg_values_supported": ["RS256", "RS384", "RS512", "ES256", "ES384", "ES512"],
  "code_challenge_methods_supported": ["S256"],
  "scopes_supported": ["user/*.cruds", "system/*.cruds", "openid"],
  "response_types_supported": ["code"],
  "capabilities": ["client-confidential-symmetric", "permission-v1", "permission-v2"],
  "authorization_endpoint": "https://auth.example.org/oauth2/authorize"
}
```

**Lưu ý tích hợp:** `capabilities` do mã khai tĩnh; khai `permission-v1`/`v2` chỉ nói auth nhận scope SMART dạng `user/*.cruds`, việc thực thi quyền nằm ở fhir-server (pkg/fhirauth). Không có `launch-standalone`, `launch-ehr`, `context-*` trong danh sách (chưa cài ở discovery).

<a id="get-well-known-smart-styles-json"></a>
## `GET /.well-known/smart-styles.json`

Màu và font gợi ý cho ứng dụng SMART nhúng; `smart_style_url` trong phản hồi `POST /oauth2/token` trỏ tới đây. [Tham chiếu trường](../reference/well-known.md#get-well-known-smart-styles-json)

**Cờ bật route:** luôn bật. Giá trị cố định, không cấu hình được.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | mọi yêu cầu | 200 | `Cache-Control: public, max-age=300` | 11 khóa chuỗi (bảng dưới) | không |
| C2 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

Các khóa: `color_background` `#edeae3`, `color_error` `#9e2d2d`, `color_highlight` `#69b5ce`, `color_modal_backdrop` (chuỗi rỗng), `color_success` `#498e49`, `color_text` `#303030`, `dim_border_radius` `6px`, `dim_font_size` `13px`, `dim_spacing_size` `20px`, `font_family_body`, `font_family_heading`.

**Ví dụ**

```http
GET /.well-known/smart-styles.json HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: public, max-age=300

{"color_background":"#edeae3","color_error":"#9e2d2d","color_highlight":"#69b5ce","color_modal_backdrop":"","color_success":"#498e49","color_text":"#303030","dim_border_radius":"6px","dim_font_size":"13px","dim_spacing_size":"20px","font_family_body":"Georgia, Times, 'Times New Roman', serif","font_family_heading":"'HelveticaNeue-Light', Helvetica, Arial, 'Lucida Grande', sans-serif"}
```

**Lưu ý tích hợp:** `color_modal_backdrop` rỗng là có chủ đích (khớp Medplum); client dùng màu mặc định của mình khi gặp chuỗi rỗng.

<a id="get-well-known-oauth-protected-resource"></a>
## `GET /.well-known/oauth-protected-resource`

Metadata của tài nguyên được bảo vệ theo RFC 9728: cho client biết authorization server nào cấp token cho tài nguyên này. [Tham chiếu trường](../reference/well-known.md#get-well-known-oauth-protected-resource)

**Cờ bật route:** luôn bật. Nội dung dựng từ `AUTH_BASE_URL` và `AUTH_ISSUER`.

**Thứ tự kiểm tra:**
1. Hạn mức IP (429).
2. Có tham số `resource`? Không: `resource` = `<base>/` (có dấu `/` cuối).
3. Có: phải đúng **một** giá trị **và** là `AUTH_BASE_URL` hoặc nằm dưới nó (bắt đầu bằng `AUTH_BASE_URL` và sau đó hết chuỗi hoặc là `/`, `?`, `#`); sai một trong hai: `400` văn bản `Invalid resource URL`. Địa chỉ nhái tiền tố (`<base>.evil.example/`, `<base>@evil.example/`, `<base>:1/`, `<base>evil`) bị từ chối (sửa 2026-11-25; trước đó so tiền tố chuỗi trần nên lọt).
4. Trả tài liệu với `resource` đã chọn.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Mặc định | không có `resource` | 200 | `Cache-Control: public, max-age=300` | `resource` = `<base>/` | không |
| C2 | `resource` hợp lệ | một giá trị, là `<base>` hoặc nằm dưới `<base>` (ví dụ `<base>/fhir/R4`, `<base>?x=1`) | 200 | như C1 | `resource` = giá trị đã gửi, nguyên văn | không |
| C3 | `resource` lạ | giá trị không phải `<base>` hay nằm dưới `<base>` (ví dụ `https://evil.example/`, hoặc nhái tiền tố `<base>.evil.example/`), hoặc rỗng (`resource=`) | 400 | - (`Content-Type: text/plain`, `Cache-Control: no-store`) | văn bản `Invalid resource URL` | không |
| C4 | `resource` lặp | `?resource=a&resource=b` | 400 | như C3 | `Invalid resource URL` | không |
| C5 | Vượt hạn mức IP | quá `AUTH_RATE_LIMIT` | 429 | `Retry-After: 1` | `{"error":"Too many requests"}` | tăng bộ đếm |

Khóa trả về: `resource`, `issuer`, `authorization_servers` (một phần tử: `issuer`), `scopes_supported` (`openid`, `profile`, `email`, `phone`, `address`), `bearer_methods_supported` (`["header"]`), `introspection_endpoint` (`<base>/oauth2/introspect`).

**Ví dụ**

```http
GET /.well-known/oauth-protected-resource?resource=https%3A%2F%2Fauth.example.org%2Ffhir%2FR4 HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: public, max-age=300

{"authorization_servers":["https://auth.example.org"],"bearer_methods_supported":["header"],"introspection_endpoint":"https://auth.example.org/oauth2/introspect","issuer":"https://auth.example.org","resource":"https://auth.example.org/fhir/R4","scopes_supported":["openid","profile","email","phone","address"]}
```

```http
GET /.well-known/oauth-protected-resource?resource=https%3A%2F%2Fevil.example%2F HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 400 Bad Request
Content-Type: text/plain; charset=UTF-8
Cache-Control: no-store

Invalid resource URL
```

**Lưu ý tích hợp:** kiểm `resource` chỉ là so tiền tố chuỗi, nên `https://auth.example.org.evil.example` cũng bắt đầu bằng `https://auth.example.org` và được chấp nhận rồi phản chiếu lại trong `resource` (tài liệu này không chứa dữ liệu nhạy cảm, nhưng đừng coi giá trị phản chiếu là đã được xác thực). Client nên so `issuer` với giá trị mình tin cậy.

## Phát hiện lệch với common.md

| Dòng trong common.md | Điều đúng theo mã |
|---|---|
| Mục 2 "Header phản hồi": ngoại lệ `no-store` chỉ là `/.well-known/jwks.json` và `/swagger/*` | Cả năm tài liệu còn lại của `/.well-known/*` (`openid-configuration`, `oauth-authorization-server`, `smart-configuration`, `smart-styles.json`, `oauth-protected-resource` khi `200`) cũng là `Cache-Control: public, max-age=300` (hàm `serveJSON`), nhưng không có `ETag`. |
| Mục 6 "Lỗi JSON thường ở `/.well-known/*`: `{"error": ...}`" | `GET /.well-known/oauth-protected-resource` trả `400` là **văn bản thuần** `Invalid resource URL`, không phải JSON. |
