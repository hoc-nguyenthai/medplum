# OAuth2 / OpenID Connect (`apps/auth`)

> **Trạng thái: ĐÃ CÀI trong `apps/auth`** (P3 B1 đến B8, cập nhật 2026-11-21). Đây là hợp đồng của server Go đang chạy, đã đối chiếu với source Medplum 5.2.18 và với test. Swagger của app: `/swagger/index.html` (chỉ ngoài production). Chi tiết quyết định, điểm lệch so với Medplum: [../plans/auth/p3-oauth2-tokens.md](../plans/p3-oauth2-tokens.md). DTO của `/auth/*` (đăng nhập bằng mật khẩu): [auth-dto.md](auth-dto.md).
>
> Khác hợp đồng của Medplum TypeScript: `/oauth2/token` **chỉ** nhận `application/x-www-form-urlencoded` (JSON bị từ chối bằng `400` văn bản `Unsupported content type`, như source Medplum); `token-exchange`, mTLS, `/.well-known/api-catalog`, đường dẫn theo project (`/projects/{id}/...`) **không có**.

## Endpoint

| Method | Path | Xác thực | Ghi chú |
|---|---|---|---|
| GET, POST | `/oauth2/authorize` | không (đọc cookie `medplum-<client_id>`) | chuyển hướng 302; **chỉ có khi đặt `AUTH_APP_BASE_URL`** |
| POST | `/oauth2/token` | client (Basic, body, hoặc `client_assertion`) | form-urlencoded |
| GET, POST | `/oauth2/userinfo` | Bearer | |
| POST | `/oauth2/introspect` | Bearer | RFC 7662 |
| GET, POST | `/oauth2/logout` | Bearer | thu hồi login và chuỗi `basedOn` tổ tiên |
| POST | `/oauth2/register` | không | tra client dựng sẵn; **chỉ có khi `AUTH_OAUTH_REGISTER_ENABLED=true`** |
| GET | `/.well-known/jwks.json` | không | `Cache-Control: public, max-age=300`, `ETag` |
| GET | `/.well-known/openid-configuration` (và `oauth-authorization-server`) | không | chỉ khai thứ đã cài |
| GET | `/.well-known/smart-configuration`, `/.well-known/smart-styles.json`, `/.well-known/oauth-protected-resource` | không | |

## `POST /oauth2/token`

Content-Type `application/x-www-form-urlencoded`; `grant_type` bắt buộc.

| grant_type | Dùng cho | Tham số |
|---|---|---|
| `authorization_code` | ứng dụng người dùng (PKCE) | `code`, `code_verifier`, `redirect_uri` (bắt buộc nếu lúc đăng nhập đã gửi), `client_id` |
| `refresh_token` | gia hạn (xoay secret) | `refresh_token`, `client_id` |
| `client_credentials` | máy với máy | `scope` (mặc định `openid`) |
| `urn:ietf:params:oauth:grant-type:pre-authorized_code` | mã cấp sẵn | `client_id`, `pre-authorized_code` |

**Xác thực client** (chọn đúng một cách): HTTP Basic (`client_id:client_secret`, tách ở dấu `:` đầu tiên, UTF-8, URL-decode từng phần theo RFC 6749 §2.3.1); hoặc `client_id` + `client_secret` trong body; hoặc `private_key_jwt` (`client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`, `client_assertion`) với `aud` = `<AUTH_BASE_URL>/oauth2/token`, `iss` = `sub` = client, có `exp` (tối đa 5 phút kể từ lúc nhận) và `jti` dùng một lần, ký RS/ES 256/384/512, JWKS tại `jwks_uri` của client (https). Dùng nhiều cách cùng lúc: `400 Multiple client authentication methods`.

Quy tắc theo grant:
- `authorization_code`: Login có `code_challenge` thì `code_verifier` luôn bắt buộc và phải đúng (S256; `plain` chỉ khi `AUTH_ALLOW_PLAIN_PKCE`), kể cả client `pkceOptional`. Login không có challenge chỉ đổi được khi client xác thực bằng secret hoặc assertion. Client đã gửi secret thì secret phải đúng. Code dùng một lần, sống `AUTH_AUTH_CODE_TTL` (10 phút) từ lúc phát; đổi lần hai thu hồi Login (`invalid_grant`/`Token already granted`). Verifier hoặc secret sai không làm mất code.
- `refresh_token`: mỗi lần dùng xoay secret và cấp refresh token mới. Dùng lại token đã xoay trong 30 giây (cùng IP) được chấp nhận; ngoài cửa sổ hoặc từ IP khác thì thu hồi Login (`invalid_grant`/`Token revoked`). Client bí mật (có secret, đăng nhập không PKCE) phải xác thực; client công khai (PKCE) thì không.
- `client_credentials`: không có refresh token; scope `fhir.*` và `patient/` bị từ chối (`invalid_scope`).
- Cả bốn grant kiểm `ipAccessRule` của membership và từ chối cấp token nếu AccessPolicy không dịch được (`access_denied`/`Access policy not supported`).

**Phản hồi 200** (`Cache-Control: no-store`):

```json
{
  "token_type": "Bearer", "expires_in": 900, "scope": "openid offline_access",
  "access_token": "<jwt>", "id_token": "<jwt>", "refresh_token": "<jwt, nếu Login có>",
  "project": {"reference": "Project/<id>"}, "profile": {"reference": "Practitioner/<id>"},
  "patient": "<id>", "encounter": "<id>", "fhirContext": [], "need_patient_banner": true,
  "smart_style_url": "<base>/.well-known/smart-styles.json",
  "hub.url": "<gateway>/fhircast/STU3", "hub.topic": "<uuid>"
}
```
`patient`, `encounter`, `fhirContext`, `need_patient_banner` chỉ có khi login có SMART launch hoặc profile là `Patient`.

`hub.url` và `hub.topic` (FHIRcast) chỉ có khi **scope đã cấp** có một term bắt đầu bằng `fhircast/` (ví dụ `fhircast/*`) **và** `AUTH_FHIRCAST_HUB_URL` được cấu hình. `hub.url` là địa chỉ Hub trong cấu hình (nguyên văn, bỏ dấu `/` cuối). `hub.topic` là UUID **ổn định theo chủ thể của token** (user hoặc ClientApplication) chừng nào còn xin token ít nhất mỗi giờ (mỗi lần xin gia hạn), nên các ứng dụng của cùng một người dùng nhận cùng topic và tìm thấy nhau. Không lấy được topic (kho lỗi) thì **không cấp token** (`500 server_error`), giống Medplum, để client không nhận token thiếu `hub.topic` mà không biết vì sao không đồng bộ được. Xem [apps/ws-gateway/README.md](../../../ws-gateway/README.md) cho phía Hub.

**Lỗi** (RFC 6749 §5.2, HTTP `400` kể cả lỗi client, như Medplum): `{"error": "...", "error_description": "..."}`. `error`: `invalid_request`, `invalid_grant`, `invalid_scope`, `access_denied`, `invalid_client` (chỉ pre-authorized_code), `server_error` (`500`, không lộ chi tiết).

## Access token (JWT) và tích hợp với fhir-server

Ký RS256 (mặc định), ES256 hoặc ES384; header có `kid` (thumbprint RFC 7638) và `typ=JWT`. Claim:

| Claim | Ý nghĩa |
|---|---|
| `iss` | `AUTH_ISSUER` (mặc định `AUTH_BASE_URL`), **đúng từng ký tự** |
| `aud` | `AUTH_AUDIENCE` (mặc định chính issuer) |
| `sub`, `username` | id `User` (hoặc id `ClientApplication` với `client_credentials`) |
| `client_id`, `login_id`, `profile`, `email` | email chỉ khi scope có từ `email` |
| `token_use` | `access` (id token: `id`; refresh token: `refresh`, `aud` riêng `<base>/oauth2/token`) |
| `fhir_project` | id project của membership; fhir-server dùng để cách ly dữ liệu |
| `scope` | scope của login **cộng** scope vai trò do auth tự thêm: admin project → `fhir.admin`; mọi rule chỉ đọc → `fhir.read`; còn lại `fhir.write`; **không thêm** khi login có scope SMART |
| `fhir_access_policy` | AccessPolicy dịch sang `[{resourceType, criteria, readonly, deny}]` (`*` áp cho loại không liệt kê, trừ loại quản trị) |
| `jti`, `iat`, `nbf`, `exp` | `exp` = `AUTH_ACCESS_TTL` (mặc định 15 phút) |

Scope client khai không bao giờ thành quyền: mọi term `fhir.*` bị từ chối. AccessPolicy được dịch theo nguyên tắc **chỉ thu hẹp** (hiddenFields, interaction, compartment, `%...` làm loại đó bị cấm; readonlyFields, writeConstraint thành chỉ đọc); compartment hoặc basedOn ở mức chính sách thì không cấp token.

Cấu hình `fhir-server` để nhận token của auth:

| fhir-server | Giá trị |
|---|---|
| `FHIR_JWT_ISSUER` | = `AUTH_ISSUER` |
| `FHIR_JWT_AUDIENCE` | = `AUTH_AUDIENCE` (hoặc issuer nếu không đặt) |
| `FHIR_JWKS_URL` | địa chỉ mà fhir-server **với tới được**: `<auth>/.well-known/jwks.json` (có thể là địa chỉ nội bộ khác `AUTH_BASE_URL`); staging/production bắt buộc `https://` |
| `FHIR_JOB_SIGNING_KEY` | bắt buộc khi dùng JWKS, ≥ 32 ký tự, dùng chung mọi replica |
| `FHIR_WRITE_TOKENS`, `FHIR_READ_TOKENS`, `FHIR_ADMIN_TOKENS`, `FHIR_AUTH_DISABLED` | **phải để trống** (không dùng cùng JWT) |

**Giới hạn đã biết (quyết định D-13):** fhir-server chỉ xác minh chữ ký, không tra Login, nên token đã thu hồi (logout, dùng lại code hoặc refresh token, tắt membership) vẫn được fhir-server nhận cho tới khi hết hạn (tối đa `AUTH_ACCESS_TTL`, kèm 30 giây chênh lệch đồng hồ). Các route của auth (`userinfo`, `introspect`, `/auth/me`) từ chối ngay. Test: `e2e` `TestKnownRevocationWindowBetweenAuthenAndFhirServer`.

## `/oauth2/authorize`

Tham số (GET query hoặc POST form): `response_type=code`, `client_id`, `redirect_uri` (khớp CHÍNH XÁC redirect URI của client), `scope`, `state`, `nonce`, `code_challenge`, `code_challenge_method` (`S256`), `aud`, `launch`, `prompt` (`none`|`login`), `max_age` (số nguyên không âm, mặc định 3600), `id_token_hint`.

- Lỗi về `client_id`/`redirect_uri`: `400` văn bản (`Client not found`, `Missing redirect URI`, `Invalid redirect URI`), không chuyển hướng.
- Lỗi khác: `302` về `redirect_uri` kèm `error`, `error_description`, `state`, `iss` (RFC 9207): `invalid_request`, `unsupported_response_type`, `request_not_supported`, `login_required`, `access_denied`.
- Client không có secret bắt buộc có `code_challenge`.
- Chưa có phiên: `302` sang `<AUTH_APP_BASE_URL>/oauth?...`; trang đó đăng nhập qua `/auth/*` rồi chuyển về `redirect_uri` kèm `code`.
- Có phiên (cookie hoặc `id_token_hint` là id token của client này): tạo Login mới; `prompt=none` trả `code` ngay, không thì sang trang ứng dụng kèm `login=<id>`; `prompt=login` bỏ qua phiên.

## `userinfo`, `introspect`, `logout`, `register`

- `userinfo`: `sub`; thêm theo TỪ trong scope của login: `profile` (`profile`, `locale`, `updated_at`, `name`, `given_name`, `family_name`), `email` (`email`, `email_verified`). Không có trường lấy từ hồ sơ lâm sàng (điện thoại, địa chỉ, ngày sinh) vì hồ sơ nằm ở fhir-server.
- `introspect` (form hoặc JSON `{"token": ...}`): chỉ hỏi được token của cùng project và cùng client với người gọi; `{"active": true, iat, exp, iss, sub, client_id, scope, patient}` hoặc đúng `{"active": false}` vì mọi lý do khác (kể cả refresh token đã xoay, membership/project bị tắt). Thiếu `token`: `400` văn bản `Token to introspect must be provided`.
- `logout`: `200` OperationOutcome `All OK`; thu hồi login, xóa cookie `medplum-<client_id>`.
- `register` (`{"redirect_uris": ["..."]}`): `201 {client_id, client_id_issued_at, redirect_uris}` nếu `redirect_uris[0]` bằng chính xác redirect URI của một client dựng sẵn; không tạo client, không trả `client_secret`.

## Phần KHÔNG có (so với Medplum)

`urn:ietf:params:oauth:grant-type:token-exchange` (hoãn sang P6), mTLS (`tls_client_auth`), Basic auth trực tiếp vào API FHIR, `/.well-known/api-catalog`, đường dẫn theo project, `hub.url`/`hub.topic` của FHIRcast, `Project.superAdmin` (`fhir.super`).
