# P3 — Token và OAuth2/OIDC cho `apps/auth`

Trạng thái: **D-12 đến D-17 đã duyệt (2026-11-21). Đã cài B0 (phần cần thiết), B1 (khóa, JWKS, well-known, `auth keys`), B2 (`client_credentials`, Bearer, claim tương thích `fhirauth`) và B3 (`authorization_code` + PKCE), B4 (`refresh_token`) và B5 (`userinfo`, `introspect`, `logout`) và B6 (`/oauth2/authorize`) B7 (`private_key_jwt`, `pre-authorized_code`, `register`, `pkg/safehttp`) và B8 (tích hợp `fhir-server`, test hai tiến trình, hợp đồng `oauth.md`). Chưa cài B9.** Chi tiết bản cài và các điểm lệch so với bản nháp ở mục 11. Phạm vi file: kế hoạch chi tiết cho pha P3 của [apps-auth.md](../apps-auth.md).

Nguồn đã đọc (chỉ đọc, không sửa): `/Users/hocnt84/Downloads/medplum/packages/server/src/` gồm `oauth/{token,authorize,utils,keys,cert,clients,introspect,userinfo,logout,register,middleware,routes}.ts`, `oauth/README.md`, `wellknown.ts`, `fhir/smart.ts` (phần SMART configuration), `api-catalog.ts`, `util/url.ts`, `constants.ts`, `config/utils.ts`, `auth/utils.ts` (cookie), và các test `oauth/*.test.ts`, `wellknown.test.ts`. Phía Go đã đọc: `pkg/fhirauth/{jwt,principal,policy,grants}.go`, `apps/fhir-server/internal/config/config.go`, `pkg/fhirrouter/router.go` (`UpdateOptions.IfMatch`), `docs/api-contract/oauth.md`, `../echo-oauth2-server/README.md`.

Quy ước trích dẫn: `token.ts:223` là `oauth/token.ts` dòng 223; file khác ghi đủ đường dẫn tính từ `src/`. Chỗ chưa đọc hoặc chưa kiểm chứng ghi **"chưa xác nhận"**.

Quyết định nền (từ `apps-auth.md`): D1 tự port flow Medplum, chỉ mượn ý tưởng bảo mật của `echo-oauth2-server` (không bọc thư viện đó); D2 dữ liệu ở `pkg/fhirstore`, kho riêng; D6 JWT RS256, JWKS ở `/.well-known/jwks.json`, claim tương thích `pkg/fhirauth`.

---

## 1. Mục tiêu và phạm vi

### 1.1 Mục tiêu

1. `apps/auth` phát hành access token (JWT) mà `pkg/fhirauth.Verifier` của `fhir-server` chấp nhận (cấu hình `FHIR_JWT_ISSUER`, `FHIR_JWT_AUDIENCE`, `FHIR_JWKS_URL`), kèm claim `fhir_project`, `scope`, `fhir_access_policy` (mục 4).
2. Cài đúng hành vi Medplum cho các grant: `authorization_code` (PKCE), `refresh_token` (xoay secret), `client_credentials`, `pre-authorized_code`; `token-exchange` để P6.
3. Có `/oauth2/authorize`, `userinfo`, `introspect`, `logout`, `register` và các `/.well-known/*`.
4. Những chỗ Medplum yếu về an toàn (mục 8) được cài chặt hơn, mượn kỹ thuật của echo-oauth2-server, và ghi rõ là lệch so với Medplum.

### 1.2 Danh sách endpoint

Nguồn route: `oauth/routes.ts:14-22`, `wellknown.ts:14-89`, mount ở `app.ts:276` (`/.well-known/`) và `app.ts:288` (`/oauth2/`). Medplum còn mount thêm theo `/projects/{id}/...` và `/fhir/R4/.well-known/*` (`wellknown.ts:86`, `util/url.ts:60-68`); P3 không port hai dạng này (mục 9).

| Method | Path | Xác thực | Ghi chú |
|---|---|---|---|
| GET | `/.well-known/jwks.json` | không | `wellknown.ts:14` |
| GET | `/.well-known/openid-configuration` | không | `wellknown.ts:82` |
| GET | `/.well-known/oauth-authorization-server` | không | `wellknown.ts:81`, cùng handler với trên |
| GET | `/.well-known/oauth-protected-resource` | không | `wellknown.ts:83` |
| GET | `/.well-known/smart-configuration` | không | `wellknown.ts:88`, handler ở `fhir/smart.ts:62` |
| GET | `/.well-known/smart-styles.json` | không | `wellknown.ts:89`, `fhir/smart.ts:120` |
| GET | `/.well-known/api-catalog` | không | `wellknown.ts:79`; **không port** (mục 9) |
| GET, POST | `/oauth2/authorize` | không; đọc cookie `medplum-<clientId>` | `routes.ts:14-15` |
| POST | `/oauth2/token` | xác thực client ngay trong handler (Basic, body, `private_key_jwt`) | `routes.ts:16`; chỉ nhận `application/x-www-form-urlencoded` |
| GET, POST | `/oauth2/userinfo` | Bearer | `routes.ts:17-18` |
| GET, POST | `/oauth2/logout` | Bearer | `routes.ts:19-20` |
| POST | `/oauth2/introspect` | Bearer | `routes.ts:21` |
| POST | `/oauth2/register` | không | `routes.ts:22` |

Cải chính hợp đồng: `docs/api-contract/oauth.md` ghi `/oauth2/token` nhận "hoặc JSON". Source không như vậy: `token.ts:97-100` trả `400` văn bản thuần `Unsupported content type` nếu không phải form-urlencoded (test `token.test.ts:259-265`). Cập nhật `oauth.md` ở bước B8 (mục 10).

---

## 2. Bảng ánh xạ source → Go

Layering theo `apps-auth.md` mục 4. Tên gói đề xuất, đặt dưới `apps/auth/internal/`:

- `domain/oauth`: luật thuần, chỉ stdlib (PKCE, scope, redirect URI, lỗi OAuth, claim, thời hạn).
- `application/oauth`: use case và cổng (`ports.go`).
- `infrastructure/...`: adapter (kho `fhirstore`, khóa, JWT, JWKS client, HTTP an toàn).
- `presentation/httpapi`: Echo handler, đọc form, ghi JSON/redirect.

| Hàm / route Medplum (`file:dòng`) | Hành vi | Đích Go |
|---|---|---|
| `tokenHandler` `token.ts:96-127` | Kiểm content-type, `grant_type`, rẽ nhánh theo grant | `presentation/httpapi/oauth_token.go`: `TokenHandler`; rẽ nhánh gọi `application/oauth.TokenService` |
| `sendTokenError` `token.ts:1183-1188` | JSON `{error, error_description}`, mặc định `400` | `domain/oauth/errors.go`: `type Error{Code, Description string; Status int}`; `presentation`: `writeOAuthError` |
| `sendTokenResponse` `token.ts:1105-1173` | Đọc `User`, `ProjectMembership`; phát token; thêm `patient`, `encounter`, `fhirContext` từ `SmartAppLaunch`; ghi body | `application/oauth/token_response.go`: `TokenService.respond(ctx, login, client)` |
| `handleClientCredentials` `token.ts:135-215` | Grant `client_credentials` | `application/oauth/grant_client_credentials.go`: `ClientCredentials` |
| `handleAuthorizationCode` `token.ts:223-321` | Đổi code lấy token, PKCE | `application/oauth/grant_authcode.go`: `AuthorizationCode` |
| `verifyCode`, `hashCode` `token.ts:1197-1207`, `utils.ts:1706-1714` | So khớp PKCE `plain`/`S256` (base64url của SHA-256, bỏ `=`) | `domain/oauth/pkce.go`: `VerifyChallenge(challenge, method, verifier) bool` (so sánh hằng thời gian), `ChallengeS256` |
| `handleRefreshToken` `token.ts:329-437` | Grant `refresh_token`, xoay, grace, phát hiện dùng lại | `application/oauth/grant_refresh.go`: `Refresh` |
| `isWithinRefreshGracePeriod` `token.ts:447-460`, `REFRESH_GRACE_PERIOD_MS=30_000` `token.ts:82` | Cửa sổ ân hạn 30 giây, cùng IP | `domain/oauth/refresh.go`: `WithinGrace(login, presented, ip, now, grace) bool` |
| `rotateLoginRefreshSecret` `token.ts:490-523` | So-và-đổi nguyên tử secret (`test` + `replace` + `previousRefreshSecret` + `refreshSecretRotatedAt` + `remoteAddress` + `userAgent`) | cổng `LoginStore.RotateRefreshSecret(ctx, id, expected, newSecret, meta) (*Login, error)`; adapter `infrastructure/store/login_store.go` dùng `UpdateOptions{IfMatch}` |
| `handleTokenExchange`, `exchangeExternalAuthToken` `token.ts:532-646` | RFC 8693, đổi token IdP ngoài | **P6**: `application/oauth/grant_exchange.go` sau cổng `ExternalIdentityResolver`; P3 chỉ để chỗ gắn |
| `handlePreAuthorizedCode` `token.ts:741-825` | Grant `pre-authorized_code` | `application/oauth/grant_preauth.go`: `PreAuthorizedCode` (nhận Login do P6 `/auth/preauthorize` tạo; test dựng Login trực tiếp) |
| `getClientIdAndSecret` `token.ts:839-865` | Chọn cách xác thực client theo thứ tự: assertion, Basic, mTLS, body | `application/oauth/clientauth.go`: `ClientAuthenticator.Authenticate(ctx, req) (*AuthenticatedClient, *Error)` |
| `parseAuthorizationHeader` `token.ts:1019-1027` | Giải mã Basic | `presentation`: `parseBasic` (sửa lỗi cắt secret, mục 8) |
| `parseClientAssertion` `token.ts:945-1012`, `verifyMultipleMatchingException` `utils.ts:1170-1190` | `private_key_jwt`: aud, iss=sub, JWKS của client, thử nhiều khóa trùng kid | `infrastructure/clientassertion/verifier.go`; HTTP qua `infrastructure/safehttp` |
| `parseMtlsClientCertificate` `token.ts:1035-1064`, `validateClientCert` `cert.ts:14-24` | `tls_client_auth` qua header | **Không port đợt này** (mục 9) |
| `validateClientIdAndSecret` `token.ts:1066-1096` | So secret hằng thời gian, kèm `retiringSecret` | `domain/oauth/clientsecret.go`: `MatchSecret(client, presented) bool` |
| `logPkceOptionalCodeRedemption` `token.ts:877-923` | Chỉ ghi log | Thay bằng audit + từ chối (mục 8) |
| `getAuthTokens` `utils.ts:773-843` | Đặt `granted=true`, ký id/access/refresh | `application/oauth/token_issuer.go`: `TokenIssuer.Issue(ctx, in) (Tokens, error)`; **tách việc đặt `granted` ra khỏi đây** (mục 8, ô 2) |
| `generateIdToken/AccessToken/RefreshToken/generateJwt` `keys.ts:239-312`, `generateSecret` `keys.ts:229` | Ký JWT, `jti`, `iat`, `nbf`, `iss`, `aud`, `exp`; secret = hex của N byte ngẫu nhiên | `infrastructure/jwt/signer.go`: `Signer.Sign(claims)`; `domain/oauth/secret.go`: `NewSecret(n)` (`crypto/rand`, hex) |
| `verifyJwt`, `getKeyForHeader` `keys.ts:320-354` | Xác minh token do chính server ký: thuật toán giới hạn, bắt buộc `kid` | `infrastructure/jwt/verifier.go`: `Verifier.Verify(raw, iss)` |
| `initKeys`, `getJwks`, `getSigningKey` `keys.ts:118-222` | Nạp khóa, dựng JWKS, chọn khóa ký | `infrastructure/keys/keystore.go`: cổng `KeyStore` (mục 4, 6) |
| `getClientApplication` `utils.ts:146-153`, `getStandardClientById` `clients.ts:37-39` | Client chuẩn dựng sẵn hoặc đọc từ DB | cổng `ClientStore.Get(ctx, id)`; client chuẩn nạp từ cấu hình |
| `getClientRedirectUri`, `getClientRedirectUris` `clients.ts:60-105` | So `redirect_uri` khớp **chính xác** với `redirectUri` + `redirectUris`; nhánh `allowPartial` | `domain/oauth/redirect.go`: `MatchRedirectURI(client, uri) (string, bool)`; **không port nhánh partial** (chỉ P6 dùng, `auth/external.ts:175`) |
| `getClientApplicationMembership` `utils.ts:483-499` | Tìm `ProjectMembership` có `user = ClientApplication/<id>` | cổng `MembershipStore.ForClient(ctx, client)` (mục 4.4, câu hỏi Q5) |
| `getInactiveReason` `fhir/projectmembership.ts:33-44` | `Profile not active` / `Project not active` | `domain/oauth/inactive.go`: `InactiveReason(project, membership) string` |
| `checkIpAccessRules` `utils.ts:628-642` | Allow/block theo `AccessPolicy.ipAccessRule` | `domain/oauth/ipaccess.go`; cần `AccessPolicy` reader (mục 4.4) |
| `isSmartScopeSubset`, `isNonSmartScopeSubset`, `matchesScope`, `matchesPermissionType` `utils.ts:644-729`, `parseSmartScopes` `fhir/smart.ts` | Luật thu hẹp scope | `domain/oauth/scope.go` (P2 `/auth/scope` dùng chung; ai làm trước thì viết) |
| `setLoginScope` `utils.ts:739-771` | Chọn scope con khi đồng ý | **P2** (`/auth/scope`), dùng `domain/oauth/scope.go` |
| `revokeLogin` `utils.ts:845-850` | Ghi `revoked=true` bằng cập nhật cả resource | `LoginStore.Revoke(ctx, id)`: dùng patch/CAS-retry, không ghi đè bản chụp cũ (mục 8) |
| `timingSafeEqualStr` `utils.ts:984-993` | So chuỗi hằng thời gian (lộ độ dài) | `domain/oauth/ctcompare.go`: `Equal(a, b string) bool` so hai SHA-256 qua `crypto/subtle` |
| `includeRefreshToken` `utils.ts:1001-1020` | Có `refresh_token` khi: cờ `remember` (cũ), scope `offline` (cũ), scope `offline_access`, hoặc `client.grantType` chứa `refresh_token` | **P2** (lúc tạo Login); P3 chỉ đọc `Login.refreshSecret` |
| `getLoginForAccessToken` `utils.ts:1201-1252` | Xác minh Bearer, kiểm `aud`, `refresh_secret` vắng, Login chưa thu hồi, membership/project active | `application/oauth/bearer.go`: `Authenticator.AuthenticateBearer(ctx, raw) (*AuthState, error)` |
| `authenticateRequest` `middleware.ts:42-57`, `authenticateTokenImpl` `middleware.ts:59-79` | Middleware Bearer; `401` kèm `WWW-Authenticate: Bearer realm=...` | `presentation/httpapi/middleware.go`: `RequireBearer` |
| `getLoginForBasicAuth` `utils.ts:1261-1313` | Basic auth trực tiếp vào API FHIR | **Không port** (`fhir-server` không dùng) |
| `authorizeGetHandler/PostHandler`, `validateAuthorizeRequest` `authorize.ts:27-169` | Kiểm tra và chuyển hướng; tạo `Login` mới khi đã đăng nhập | `application/oauth/authorize.go`: `Authorize.Handle`; `presentation/httpapi/oauth_authorize.go` |
| `isValidAudience` `authorize.ts:176-191` | `aud` cùng protocol và host với `baseUrl`; thiếu thì cho qua | `domain/oauth/audience.go` |
| `getExistingLogin*` `authorize.ts:214-302` | Tìm Login cũ qua `id_token_hint` hoặc cookie; kiểm `max_age` | `application/oauth/session.go`: `ExistingLogin` |
| `sendErrorRedirect`, `sendSuccessRedirect` `authorize.ts:312-338` | Chuyển lỗi về `redirect_uri`; chuyển người dùng sang `appBaseUrl + 'oauth'` | `presentation/httpapi/oauth_authorize.go` |
| `tokenIntrospectHandler`, `canIntrospectToken` `introspect.ts:22-106` | RFC 7662 | `application/oauth/introspect.go` + `presentation/httpapi/oauth_introspect.go` |
| `userInfoHandler`, `buildProfile/Email/Phone/Address` `userinfo.ts:24-119` | OIDC userinfo theo scope | `application/oauth/userinfo.go` + cổng `ProfileReader` (mục 6) |
| `logoutHandler` `logout.ts:10-27` | Thu hồi Login, xoá cookie | `application/oauth/logout.go` |
| `registerHandler` `register.ts:16-40` | Tra client chuẩn theo `redirect_uris[0]` | `presentation/httpapi/oauth_register.go` |
| `handleOAuthConfig` `wellknown.ts:18-51` | `openid-configuration` / `oauth-authorization-server` | `presentation/httpapi/wellknown.go` + `application/oauth/discovery.go` |
| `handleOauthProtectedResource` `wellknown.ts:53-75` | RFC 9728 | cùng file |
| `smartConfigurationHandler` `fhir/smart.ts:62-112`, `smartStylingHandler` `fhir/smart.ts:120-133` | SMART discovery và style | cùng file |

---

## 3. Từng grant của `/oauth2/token`

### 3.0 Phần chung

**Định dạng lỗi** (`token.ts:1183-1188`): HTTP `400` (mặc định), body `{"error": "...", "error_description": "..."}`. `Cache-Control: no-store` có sẵn vì Medplum đặt cho mọi response (`app.ts:86-89`); Go phải đặt bằng middleware toàn cục tương đương.

**Bước 0 (mọi grant, `token.ts:96-127`):**

1. Content-Type không phải `application/x-www-form-urlencoded` (so loại media, bỏ tham số): `400`, **văn bản thuần** `Unsupported content type` (không phải JSON; test `token.test.ts:259-265`).
2. `grant_type` rỗng: `400` `invalid_request` / `Missing grant_type` (test `token.test.ts:267-275`).
3. `grant_type` không thuộc 5 giá trị (`client_credentials`, `authorization_code`, `refresh_token`, `urn:ietf:params:oauth:grant-type:token-exchange`, `pre-authorized_code`; tên enum `OAuthGrantType` ở `@medplum/core`, **giá trị chuỗi chưa đọc trực tiếp**, riêng `token-exchange` được xác nhận qua comment `token.ts:550`): `400` `invalid_request` / `Unsupported grant_type` (RFC 6749 §5.2 yêu cầu `unsupported_grant_type`; Medplum lệch, mục 9).

**Cách client xác thực** (`getClientIdAndSecret`, `token.ts:839-865`), thứ tự ưu tiên:

1. Có `client_assertion_type` trong body: `private_key_jwt` (`token.ts:840-845`, chi tiết ở 3.7).
2. Có header `Authorization`: phải bắt đầu `Basic ` (nếu không: `invalid_request` / `Invalid authorization header`, `token.ts:1020-1022`). Giải base64, **decode `ascii`**, tách theo `:` bằng `split(':')` rồi lấy hai phần tử đầu (`token.ts:1023-1026`): secret chứa `:` bị cắt cụt, ký tự ngoài ASCII hỏng, không URL-decode theo RFC 6749 §2.3.1. Go: `SplitN(…, ":", 2)`, decode UTF-8, URL-decode từng phần (mục 8, ô 11).
3. Có cấu hình `mtlsCertHeader` và header đó: `tls_client_auth` (`token.ts:852-858`); **không port** (mục 9). Mặc định không bật; chỉ test đặt `x-mtls-cert` (`config/loader.ts:240`).
4. Còn lại: `client_id` và `client_secret` trong body (`client_secret_post` nếu có secret, không thì `none`) (`token.ts:860-864`).

**So secret** (`validateClientIdAndSecret`, `token.ts:1066-1096`):

1. Client không có secret (rỗng hoặc vắng): `invalid_request` / `Invalid client`.
2. So hằng thời gian với `secret`; luôn so thêm với `retiringSecret ?? secret` để không lộ việc có secret cũ; khớp một trong hai là đạt.
3. Sai: `invalid_request` / `Invalid secret`.

Secret lưu và so **dạng rõ** (không băm), khác echo-oauth2-server (scrypt). Xem mục 8, ô 8.

**Thời hạn token** (`keys.ts:107-108`, `keys.ts:240`, `keys.ts:287-312`):

| Token | Mặc định | Ghi đè | Ghi chú |
|---|---|---|---|
| Access | `1h` | `ClientApplication.accessTokenLifetime` | `expires_in = exp - iat` (`token.ts:1155-1159`) nên là `3600` (test `token.test.ts:738`) |
| Refresh | `2w` | `ClientApplication.refreshTokenLifetime` | cấp lại hạn đầy mỗi lần xoay, không có trần tuyệt đối (mục 8, ô 7) |
| ID | `1h` cố định | không | `keys.ts:240` |
| Auth code | 10 phút | không | `AUTH_CODE_EXPIRATION_MS`, `constants.ts:49` |
| Pre-authorized code | theo `Login.expiresAt` | do P6 đặt | `token.ts:794` |

Định dạng thời hạn: regex `^\d+[smhdwy]$` (`keys.ts:292`), sai thì lỗi nội bộ `Invalid token duration`. Ràng buộc `clapp-1` của resource ClientApplication chặn giá trị sai khi tạo (test `token.test.ts:1685-1706`, `1749-1770`).

### 3.1 `authorization_code` + PKCE (`token.ts:223-321`)

Giả định P2 đã tạo `Login` (có `code`, `codeChallenge`, `codeChallengeMethod`, `membership`) qua `/auth/login`. Thuật toán Medplum, từng bước:

1. Xác thực client theo 3.0. Có lỗi: `400` `invalid_request` / `<mô tả lỗi>`. (Chưa có client nào bị bắt buộc xác thực ở bước này.)
2. `code` rỗng: `invalid_request` / `Missing code`.
3. Tìm `Login` có `code` bằng giá trị gửi lên (so bằng nhau, code lưu dạng rõ). Không có: `invalid_request` / `Invalid code`.
4. Nếu request có `client_id` mà `login.client.reference` khác `ClientApplication/<client_id>`: `invalid_request` / `Invalid client`. Nếu không gửi `client_id` thì **không** kiểm (client suy từ Login ở bước 9).
5. `login.membership` rỗng: `invalid_request` / `Invalid profile`.
6. `login.granted` đã `true`: **thu hồi Login** (`revokeLogin`) rồi `400` `invalid_grant` / `Token already granted` (đây là phát hiện dùng lại code).
7. `login.revoked`: `invalid_grant` / `Token revoked`. (Vì bước 6 trước bước 7, Login vừa `granted` vừa `revoked` báo `Token already granted`.)
8. Hết hạn: `now - Date.parse(login.meta.lastUpdated) >= 10 phút` (hoặc không parse được): `invalid_grant` / `Authorization code expired`. Mốc là `meta.lastUpdated`, nên mọi lần ghi Login (chọn profile, đổi scope) đều **kéo dài** hạn code (test `token.test.ts:624-661` dùng đúng tính chất này). Go dùng mốc phát hành tường minh (mục 8, ô 5).
9. Nạp client: có `client_id` thì `getClientApplication(client_id)`, không thì `login.client`; lỗi: `invalid_request` / `Invalid client`. Login không có client thì `client` là `undefined`.
10. Nhánh PKCE:
    - Nếu `!client?.pkceOptional` (kể cả client `undefined`): nếu `login.codeChallenge` có: `code_verifier` rỗng thì `invalid_request` / `Missing code verifier`; sai (`plain`: bằng nhau; `S256`: `challenge === hashCode(verifier)`, **so thường, không hằng thời gian**) thì `invalid_request` / `Invalid code verifier`. Nếu Login không có `codeChallenge`: `invalid_request` / `Missing verification context` (test `token.test.ts:686-700`). **`client_secret` gửi kèm không được kiểm ở nhánh này.**
    - Nếu `client.pkceOptional`: **không kiểm PKCE** dù Login có `codeChallenge`; chỉ kiểm secret nếu client có gửi secret (`token.ts:310-314`). Test `token.test.ts:770-792` xác nhận: client `pkceOptional`, có challenge, không verifier, không secret: vẫn `200`.
11. `sendTokenResponse` → `getAuthTokens`: nếu `!login.granted` ghi `granted=true` bằng `updateResource({...login, granted:true})` **không có `ifMatch`** (`utils.ts:791-797`), sau đó ký token. Hai request song song cùng code có thể cùng qua bước 6 và cùng nhận token.

Không có bước kiểm `redirect_uri` ở token endpoint, và `redirect_uri` không được lưu vào `Login` (`utils.ts:200-219`; `Login` ở `pkg/fhir/resource_login.go` cũng không có trường này). Chỉ có kiểm lúc tạo Login (`utils.ts:166-168`).

Lỗi tóm tắt (đều `400`):

| Điều kiện | error | error_description |
|---|---|---|
| Lỗi xác thực client | `invalid_request` | theo `parseClientAssertion`/`parseAuthorizationHeader` |
| Thiếu code | `invalid_request` | `Missing code` |
| Code lạ | `invalid_request` | `Invalid code` |
| `client_id` không khớp Login | `invalid_request` | `Invalid client` |
| Login chưa chọn profile | `invalid_request` | `Invalid profile` |
| Code dùng lại | `invalid_grant` | `Token already granted` |
| Login đã thu hồi | `invalid_grant` | `Token revoked` |
| Code hết hạn | `invalid_grant` | `Authorization code expired` |
| Không lấy được client | `invalid_request` | `Invalid client` |
| Thiếu verifier | `invalid_request` | `Missing code verifier` |
| Verifier sai | `invalid_request` | `Invalid code verifier` |
| Login không có challenge | `invalid_request` | `Missing verification context` |
| Secret sai (chỉ nhánh `pkceOptional`) | `invalid_request` | `Invalid secret` |

**Thuật toán Go đề xuất** (khác Medplum ở các bước đánh dấu `[+]`; lý do ở mục 8):

1. Như bước 1 trên, nhưng dùng bộ phân tích Basic đã sửa.
2. `code` rỗng thì `invalid_request` / `Missing code`. `[+]` Nếu thiếu `redirect_uri` mà Login có lưu `redirectUri` thì `invalid_request` / `Missing redirect_uri`.
3. Tra Login theo `code`. `[+]` `Login.code` lưu SHA-256 hex của code, tra bằng băm (câu hỏi Q6).
4. Kiểm `client_id` (nếu có) khớp Login; `[+]` nếu client có secret/khác public thì **bắt buộc xác thực client** (Q4).
5. Kiểm `membership` có.
6. **`[+]` Chiếm code nguyên tử:** `UpdateResource` đặt `granted=true` với `IfMatch = login.meta.versionId` ngay tại đây, *trước* khi ký token. `PreconditionFailed` thì đọc lại: nếu `granted` thì chạy nhánh dùng lại (bước 7); nếu chưa thì thử lại tối đa N lần.
7. Nếu Login đã `granted` (đọc ban đầu hoặc sau xung đột): thu hồi Login bằng patch/CAS-retry; `invalid_grant` / `Token already granted`. Nếu `revoked`: `invalid_grant` / `Token revoked`. Đổi thứ tự để `revoked` được kiểm trước `granted`.
8. Hết hạn theo `Login.expiresAt` hoặc trường phát hành tường minh (Q7), không dùng `meta.lastUpdated`. `invalid_grant` / `Authorization code expired`.
9. Nạp client như Medplum.
10. `[+]` Nếu Login có `codeChallenge`: **luôn** kiểm verifier (kể cả client `pkceOptional`). Nếu Login không có `codeChallenge`: chỉ cho qua khi client là confidential **và** đã xác thực bằng secret/assertion (đúng ý "PKCE tuỳ chọn cho client bí mật" của echo-oauth2-server); còn lại `invalid_request` / `Missing verification context`. `S256` mặc định, `plain` chỉ khi `AUTH_ALLOW_PLAIN_PKCE=true`.
11. `[+]` Nếu Login có `redirectUri`: `redirect_uri` của request phải khớp chính xác (so chuỗi), sai thì `invalid_grant` / `Invalid redirect_uri`.
12. Ký token, trả response (3.8).

### 3.2 `refresh_token` (`token.ts:329-437`)

Thuật toán Medplum:

1. `refresh_token` rỗng: `invalid_request` / `Invalid refresh token`.
2. `verifyJwt(refresh_token, issuer)`: thuật toán chỉ `ES256`, `ES384`, `RS256` (`keys.ts:330`), phải có `kid` thuộc khóa nội bộ (`keys.ts:342-353`), `iss` khớp, `exp`/`nbf` được thư viện `jose` kiểm. Lỗi: `invalid_request` / `Invalid refresh token`. Chữ ký `alg:none`, HS256, payload sửa: đều rơi vào nhánh này (test `refresh-token-grant.test.ts:137-168`).
3. Đọc `Login` theo `claims.login_id` **không bọc try/catch** (`token.ts:346`): Login không tồn tại làm handler ném lỗi (cách phản hồi **chưa xác nhận**). Go phải trả `invalid_grant`.
4. `login.refreshSecret` vắng hoặc `claims.refresh_secret` vắng: `invalid_request` / `Invalid refresh token`. Đây là chỗ chặn access token và id token dùng làm refresh token (test `refresh-token-grant.test.ts:170-182`).
5. `login.revoked`: `invalid_grant` / `Token revoked`.
6. Nếu có `login.membership`: đọc membership và project; `getInactiveReason` có giá trị thì `access_denied` / `Profile not active` hoặc `Project not active`.
7. Nếu `login.client`: đọc `ClientApplication` bằng `readResource` (**không** qua `getClientApplication`, nên client chuẩn dựng sẵn như `medplum-cli` không đọc được ở đây, suy từ code `token.ts:373` so với `token.ts:285`; chưa có test xác nhận). Lỗi: `invalid_request` / `Invalid client`.
8. Nếu có header `Authorization`: không phải Basic thì `invalid_request` / `Invalid authorization header`; `clientId` khác `login.client` thì `invalid_grant` / `Incorrect client`; secret rỗng thì `invalid_grant` / `Incorrect client secret`; so secret sai thì `invalid_request` / `Invalid secret`. **Không có header thì không xác thực client**, kể cả `client_secret` trong body bị bỏ qua (test "Known gaps" `refresh-token-grant.test.ts:341-351`: client có secret vẫn refresh được không cần secret).
9. **Xoay:** nếu `timingSafeEqual(login.refreshSecret, claims.refresh_secret)`: so-và-đổi nguyên tử (`rotateLoginRefreshSecret`): `test /refreshSecret = expected`, `replace /refreshSecret = 32 byte hex mới`, `add /previousRefreshSecret = expected`, `add /refreshSecretRotatedAt = now`, `add /remoteAddress`, `add /userAgent`. Thành công: phát token với secret mới (200). Nếu patch lỗi `400` (test sai) mà secret hiện tại ≠ expected: coi như không xoay được (sang bước 10); các lỗi khác ném ra, **không** coi là dùng lại (`token.ts:509-522`, test `refresh-token-grant.test.ts:313-338`).
10. **Ân hạn:** đọc lại Login (bản cũ có thể lỗi thời). Nếu `!revoked`, có `refreshSecret`, `previousRefreshSecret`, `refreshSecretRotatedAt`, IP request khác rỗng và bằng `login.remoteAddress` (IP của lần xoay), `previousRefreshSecret` bằng secret trình lên (hằng thời gian) và `0 <= now - rotatedAt < 30 giây`: phát token cho secret **hiện tại**, không xoay (200). Đây là cách 5 request song song cùng token đều thành công (test `refresh-token-grant.test.ts:268-283`).
11. **Dùng lại:** còn lại: `patchResource(Login, id, [add /revoked true])` (patch, không ghi cả bản chụp, comment `token.ts:429-430`), log cảnh báo, `400` `invalid_grant` / `Token revoked`. Mọi token của Login chết ngay vì Medplum xác minh Login mỗi request (`utils.ts:1234-1243`); test `refresh-token-grant.test.ts:203-231`.

Ghi chú hành vi:

- Cơ chế này tương đương "family" của echo-oauth2-server nhưng **một Login = một family** và chỉ giữ **một** secret trước đó.
- Tham số `scope` trong request refresh bị bỏ qua (không thu hẹp scope).
- Refresh không gọi `checkIpAccessRules`, và IP bị chặn vẫn được ghi vào `login.remoteAddress` (test `refresh-token-grant.test.ts:353-368`).
- IP lấy từ `req.ip`, mà Medplum đặt `trust proxy` = 1 (`app.ts:211`). Go phải cấu hình danh sách proxy tin cậy, nếu không IP ân hạn giả được.

Thuật toán Go: giữ nguyên bước 1 đến 11 với các sửa `[+]`:

- `[+]` Bước 3 lỗi đọc Login: `invalid_grant` / `Invalid refresh token`.
- `[+]` Bước 8: yêu cầu xác thực client khi client là confidential (Q4); dùng chung `ClientAuthenticator` (Basic, body, assertion). Giữ `Incorrect client` khi `client_id` không khớp Login.
- `[+]` Gọi `checkIpAccessRules`; ghi `remoteAddress` chỉ sau khi qua kiểm tra.
- `[+]` Ân hạn cấu hình được (`AUTH_REFRESH_GRACE`, 0 là tắt), đồng thời ghi audit mỗi lần dùng ân hạn và mỗi lần phát hiện dùng lại.
- `[+]` Trần tuyệt đối của phiên (`AUTH_SESSION_MAX_AGE`, 0 là không giới hạn) tính từ `authTime`.
- `[+]` Dùng lại thì thu hồi Login bằng CAS-retry và trả `invalid_grant` / `Token revoked`; propagation sang `fhir-server` xem 4.5.
- Xử lý song song bằng `IfMatch` trên `versionId` (`pkg/fhirrouter/router.go:74-76`, đã có test cạnh tranh `pkg/fhirrouter/repotest/suite.go:709`), kèm test 16 goroutine như echo-oauth2-server.

### 3.3 `client_credentials` (`token.ts:135-215`)

Thuật toán Medplum:

1. Xác thực client (3.0); lỗi: `invalid_request` / `<mô tả>`.
2. `clientId` rỗng: `invalid_request` / `Missing client_id`. `clientSecret` rỗng: `invalid_request` / `Missing client_secret`.
3. Đọc `ClientApplication` (qua `readResource`, **không** qua client chuẩn); lỗi: `invalid_request` / `Invalid client`.
4. `client.status` có giá trị và khác `active`: `invalid_request` / `Invalid client` (mọi trạng thái khác `active`, kể cả `error`, đều bị chặn).
5. So secret (3.0): `Invalid client` hoặc `Invalid secret`.
6. Tìm `ProjectMembership` có `user = ClientApplication/<id>` (và theo `projects/{id}` của URL nếu có); không thấy: `invalid_request` / `Invalid client`.
7. Đọc project; `getInactiveReason` có giá trị: **`access_denied`** / `Profile not active` hoặc `Project not active` (test `token.test.ts:302-318`).
8. `scope = body.scope || 'openid'`. Nếu `scope` chứa chuỗi con `patient/`: ném `badRequest('Cannot use client credentials with patient scope')`, tức `400` **OperationOutcome**, không phải JSON OAuth (`token.ts:185-187`). Go trả `400` `invalid_scope` (không có test Medplum cho ca này).
9. Tạo `Login` với `authMethod='client'`, `user=client`, `client=client`, `project`, `membership`, `authTime=now`, **`granted=true`**, `scope`, `remoteAddress`, `userAgent`. **Không có** `refreshSecret`, `code`, `nonce`, nên không cấp refresh token.
10. Dựng `userConfig`, `accessPolicy`, rồi `checkIpAccessRules`; lỗi: `invalid_request` / `IP address not allowed` (test `token.test.ts:565-600`). Login đã được ghi trước bước này (Login `granted:true` còn lại dù bị chặn IP).
11. Phát token (3.8).

Điểm lưu ý cho Go:

- `scope` do client tự khai và được lưu nguyên, không đối chiếu với quyền của membership; **nghiêm trọng khi chuyển sang `fhirauth`** vì `fhirauth` coi `fhir.admin` là quyền (mục 4.3). Go phải lọc scope.
- Với `private_key_jwt`, Medplum trả `clientSecret: client.secret` rồi so với chính nó (`token.ts:1011`, `1071`); hệ quả là client dùng assertion vẫn **phải có `secret`** (nếu không: `Invalid client`). Go không bắt chước: kết quả xác thực là `AuthenticatedClient{ID, Method}`.
- Membership chọn bằng `searchOne` không xác định thứ tự nếu client có nhiều membership (`utils.ts:498`); xem Q5.
- Chọn thứ tự: tạo Login *sau* khi kiểm IP để không để lại Login rác (khác Medplum).

### 3.4 `urn:ietf:params:oauth:grant-type:token-exchange` (`token.ts:532-733`) — hoãn sang P6

Medplum (ghi lại để P6 dùng; P3 trả `Unsupported grant_type` và không quảng bá trong discovery):

1. `client_id` rỗng: `invalid_request` / `Invalid client`. `subject_token` rỗng: `Invalid subject_token`. `subject_token_type` khác `urn:ietf:params:oauth:token-type:access_token` (theo comment `token.ts:550`; giá trị enum chưa đọc trực tiếp): `Invalid subject_token_type`.
2. Chọn nhà cung cấp ngoài: ưu tiên `config.externalAuthProviders` khớp `clientId` (`token.ts:717-733`), sau đó `ClientApplication.identityProvider`. Không có: `Invalid client`.
3. Dùng provider từ cấu hình máy chủ thì `membership_id` (nếu có) quyết định project; ngược lại project suy từ client.
4. Gọi `userInfoUrl` của IdP bằng `subject_token` (`getExternalUserInfo`); thiếu URL: `Missing user info URL`; lỗi IdP: mã HTTP của lỗi.
5. Phân giải danh tính, `tryLogin({authMethod:'exchange', forceUseFirstMembership:true, scope: body.scope || 'openid offline_access', nonce: body.nonce || uuid})`.
6. Đặt `User.emailVerified=true`; phát token.

Không yêu cầu xác thực client; bằng chứng duy nhất là token IdP. SSRF qua `userInfoUrl` được chặn bằng `safeFetch` (`util/url.ts:70-100`).

### 3.5 `pre-authorized_code` (`token.ts:741-825`)

Thuật toán Medplum:

1. `client_id` không phải chuỗi: `invalid_request` / `Missing client_id`. `pre-authorized_code` không phải chuỗi: `invalid_request` / `Missing pre-authorized_code` (tên tham số có dấu gạch ngang, `token.ts:748`).
2. `getClientApplication(client_id)` lỗi: **`invalid_client`** / `Invalid client` (nhánh duy nhất của Medplum dùng `invalid_client`, vẫn `400`). `status` khác `active`: `invalid_request` / `Invalid client`.
3. Tìm Login có `preAuthorizedCodeHash` = SHA-256 hex của code (`token.ts:768-776`); không có hoặc `authMethod !== 'pre-authorized'`: `invalid_request` / `Invalid pre-authorized_code`.
4. `login.client.reference` khác `ClientApplication/<client_id>`: `invalid_request` / `Invalid client`. Không có `membership`: `invalid_request` / `Invalid profile`.
5. `expiresAt` vắng hoặc đã qua: `invalid_grant` / `Pre-authorized code expired`.
6. `granted`: thu hồi Login; `invalid_grant` / `Token already granted`. `revoked`: `invalid_grant` / `Token revoked`.
7. Gán `remoteAddress`, `userAgent` (chỉ trong bộ nhớ), kiểm IP qua access policy; lỗi: `invalid_request` / `<mô tả>`.
8. Phát token. **Không** có PKCE, không có `tx_code`, không xác thực client ngoài `client_id`; code là bearer.

Điểm tốt cần giữ: mã được lưu **dạng băm**. Go áp dụng cùng chiếm nguyên tử như 3.1 bước 6. Việc tạo Login pre-authorized (`/auth/preauthorize`) thuộc P6; P3 làm grant này ở bước muộn (B7) với Login dựng trong test.

### 3.6 Grant khác

Medplum không có grant `password`, `implicit`, `device_code`. `authorize.ts:94` chỉ nhận `response_type=code`, dù discovery khai thêm `id_token`, `token id_token` (`wellknown.ts:40`); Go khai báo đúng cái làm được (mục 5).

### 3.7 Xác thực client `private_key_jwt` (`token.ts:945-1012`)

1. `client_assertion_type` khác `urn:ietf:params:oauth:client-assertion-type:jwt-bearer` (tên enum `OAuthClientAssertionType.JwtBearer`, giá trị chưa đọc trực tiếp): `Unsupported client assertion type`.
2. `client_assertion` rỗng hoặc không phải JWT (`isJwt`): `Invalid client assertion`.
3. Giải payload **chưa xác minh**; `aud` phải bằng chuỗi `tokenUrl` (so `!==`, nên mảng bị từ chối): `Invalid client assertion audience`; `iss !== sub`: `Invalid client assertion issuer`.
4. Đọc `ClientApplication(iss)`: lỗi `Client not found`; thiếu `jwksUri`: `Client must have a JWK Set URL`.
5. Xác minh chữ ký bằng JWKS từ xa (`createRemoteJWKSet`, tải qua `safeFetch`: chỉ HTTPS, chặn `localhost` và IP không an toàn, kiểm DNS, `util/url.ts:70-100`, `172-181`); `iss = clientId`, `aud = tokenUrl`, thuật toán `RS256/384/512`, `ES256/384/512`. Sai: `Invalid client assertion signature`. Nếu nhiều khóa cùng `kid` (`ERR_JWKS_MULTIPLE_MATCHING_KEYS`): thử từng khóa, khóa đầu xác minh được là đạt, lỗi chữ ký thì sang khóa kế, lỗi khác thì trả mã lỗi đó (`utils.ts:1170-1190`).
6. Tất cả lỗi trên là `400` `invalid_request`.

Medplum **không** kiểm `exp` bắt buộc, `jti` dùng một lần, hay trần thời hạn assertion (`jwtVerify` chỉ kiểm `exp` nếu có). Go bổ sung (mục 8, ô 13).

### 3.8 Nội dung token và response

**Access token** (`utils.ts:813-824`, `keys.ts:252-262`):

| Claim | Giá trị |
|---|---|
| `iss` | `config.issuer` (có thể kèm `projects/{id}/` ở Medplum; Go không) |
| `aud` | chính `iss` (`keys.ts:259`) |
| `sub`, `username` | `user.id` (id `User`, hoặc id `ClientApplication` với `client_credentials`) |
| `client_id` | `login.client` (có thể vắng) |
| `login_id` | id `Login` |
| `scope` | `login.scope` |
| `profile` | `membership.profile.reference`, ví dụ `Practitioner/123` |
| `email` | chỉ khi `login.scope?.includes('email')` (so **chuỗi con**) và user là `User` |
| `jti`, `iat`, `nbf`, `exp` | `keys.ts:297-311` (`nbf = now`) |
| header | `alg`, `kid` (id resource `JsonWebKey`), `typ=JWT` |

**ID token** (`utils.ts:799-811`): `client_id`, `login_id`, `fhirUser` (= profile reference), `email` (điều kiện như trên), `aud = client_id` (vắng nếu Login không có client thì không có `aud`, `keys.ts:307-310`), `sub = user.id`, `nonce`, `auth_time` (giây), cùng `iss`, `jti`, `iat`, `nbf`, `exp=1h`. Không có `at_hash`, `azp`. ID token **luôn** được phát, kể cả khi scope không có `openid`.

**Refresh token** (`utils.ts:826-836`): `client_id`, `login_id`, `refresh_secret`, `aud = iss`, `jti`, `iat`, `nbf`, `exp`. Chỉ có khi `login.refreshSecret` có.

**Response** (`token.ts:1157-1172`):

```json
{
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "openid offline_access",
  "id_token": "<jwt>",
  "access_token": "<jwt>",
  "refresh_token": "<jwt>",
  "project": { "reference": "Project/<id>" },
  "profile": { "reference": "Practitioner/<id>" },
  "patient": "<id hoặc identifier.value>",
  "encounter": "<id hoặc identifier.value>",
  "fhirContext": [],
  "smart_style_url": "<baseUrl>fhir/R4/.well-known/smart-styles.json",
  "need_patient_banner": true
}
```

- `refresh_token`, `patient`, `encounter`, `fhirContext` bị bỏ khỏi JSON khi `undefined`.
- `patient`/`encounter` lấy từ `SmartAppLaunch` của Login, ưu tiên `identifier.value` (nếu có) rồi mới tới id tham chiếu (`token.ts:1128-1135`; test `token.test.ts:2159-2214`); nếu profile của membership là `Patient/...` thì `patient` bị ghi đè bằng id đó (`token.ts:1137-1139`; test `token.test.ts:1772-1814`). `need_patient_banner = !!patient`.
- Khi scope chứa `fhircast/`: thêm `hub.url` và `hub.topic` (`token.ts:1142-1153`). **Ban đầu không port (mục 9); đã làm sau ở WS-9 / FC-6 (2026-11-22)**: xem `application/fhircast_hub.go`, `AUTH_FHIRCAST_HUB_URL`, [docs/api-contract/oauth.md](../../api-contract/oauth.md).
- `smart_style_url` trỏ vào `fhir/R4/.well-known/smart-styles.json`. Auth và fhir-server là hai app; Go dùng URL của chính auth (`<baseUrl>.well-known/smart-styles.json`), ghi vào mục lệch.

---

## 4. Mô hình khóa và claim tương thích `pkg/fhirauth`

### 4.1 Medplum làm gì với khóa

- Khóa ký là resource `JsonWebKey` có `active=true` trong DB, **kèm tham số riêng `d`** (`keys.ts:135-156`: tạo bằng `generateKeyPair`, `exportJWK(privateKey)`, lưu nguyên). Không tìm thấy khóa nào lúc khởi động thì tự sinh một khóa mới.
- Thuật toán mặc định cho khóa mới: **ES384** (`keys.ts:105`); khóa cũ không ghi `alg` coi là RS256 (`keys.ts:106`, `160`). Chấp nhận khi xác minh: `ES256`, `ES384`, `RS256` (`keys.ts:330`).
- `kid` = id resource `JsonWebKey` (UUID; test `wellknown.test.ts:31-33`).
- Khóa ký = phần tử **đầu** của kết quả search (`keys.ts:190-191`), thứ tự search không được đảm bảo trong code đã đọc. Khóa nạp **một lần** ở `initKeys`; hàm xoay khóa **không có** trong `keys.ts` (xoay nghĩa là tạo/tắt `JsonWebKey` rồi khởi động lại; hot reload **chưa xác nhận**).
- JWKS công khai gồm mọi khóa active, chỉ phần công khai: RSA có `n,e`; EC có `x,y,crv`; thêm `kid`, `alg`, `kty`, `use:"sig"` (`keys.ts:162-179`). Test khẳng định không lộ `d`, `p`, `q`, `dp`, `dq`, `qi` (`wellknown.test.ts:35-63`).

### 4.2 Đề xuất Go

| Hạng mục | Quyết định |
|---|---|
| Thuật toán mặc định | **RS256** (D6). Cấu hình `AUTH_SIGNING_ALG` ∈ `RS256`, `ES256`, `ES384`. `fhirauth` chấp nhận cả ES256/ES384 (`pkg/fhirauth/jwt.go:109-110`), nên chọn RS256 chỉ vì tương thích rộng; ES384 cũng chạy. Khoá RSA ≥ 2048 bit (khuyến nghị 3072). |
| Nơi giữ khóa riêng | **Không** lưu trong `pkg/fhirstore` (D2: tránh lộ `d` qua `/fhir/R4`; Medplum lưu thẳng). Adapter đầu tiên đọc **PEM từ thư mục/secret mount** (`AUTH_SIGNING_KEYS_DIR`, mỗi file một khóa, `kid` = tên file hoặc SHA-256 thumbprint RFC 7638). Adapter DB (mã hoá phong bì, cùng ý với `pkg/fhirauth/seal.go`) để sau, nếu cần. Lệnh `auth keys generate` sinh khóa. |
| `kid` | Thumbprint RFC 7638 của khóa công khai (ổn định, không phụ thuộc DB). |
| Chọn khóa ký | Khóa có `signing=true` (tên file hậu tố hoặc cấu hình `AUTH_ACTIVE_KID`); **tường minh**, không "phần tử đầu". |
| Xoay khóa | (1) Thêm khóa mới vào thư mục, **chưa** đặt làm khóa ký; (2) triển khai để JWKS chứa cả hai; (3) đợi quá TTL cache của người xác minh (`fhirauth`: 10 phút, làm mới khi gặp `kid` lạ nhưng tối đa 30 giây một lần, `jwt.go:200-215`) rồi đặt khóa mới là khóa ký; (4) giữ khóa cũ ở chế độ chỉ xác minh trong JWKS đến khi mọi token cũ hết hạn (access ≤ TTL; refresh ≤ `refresh TTL` vì `auth` tự xác minh refresh token bằng khóa nội bộ); (5) gỡ khóa cũ. Test: token ký bằng khóa cũ vẫn xác minh được sau khi đổi khóa ký. |
| JWKS | `GET /.well-known/jwks.json`: `{"keys":[{"kty","use":"sig","alg","kid","n","e"}]}` hoặc `x,y,crv`; chỉ phần công khai; `Cache-Control: public, max-age=300`, `ETag`. Test không lộ `d,p,q,dp,dq,qi` (port `wellknown.test.ts:21-63`). |
| Xác minh token nội bộ | Chỉ chấp nhận thuật toán trong tập khóa nạp; bắt buộc `kid` có thật; từ chối `none` và HS* (port `refresh-token-grant.test.ts:137-168`). |
| Phân biệt loại token | Thêm claim `token_use` ∈ `access`, `id`, `refresh` (Medplum chỉ phân biệt gián tiếp qua `aud` và `refresh_secret`, `utils.ts:1228-1232`); Go kiểm cả hai. Cho refresh token một `aud` riêng (ví dụ `<issuer>/oauth2/token`) để `fhirauth` chắc chắn từ chối nó (hiện nó cũng bị từ chối vì thiếu `fhir_project`, nhưng không nên dựa vào đó). |

### 4.3 Ánh xạ claim Medplum → `fhirauth`

`fhirauth.Verifier.Verify` (`pkg/fhirauth/jwt.go:106-172`) yêu cầu: chữ ký theo JWKS với `kid`; `iss` khớp; `aud` chứa `Config.Audience`; `exp` bắt buộc; `sub` không rỗng; `fhir_project` không rỗng (trừ super); ít nhất một scope cấp quyền (`fhir.read|write|admin`, hoặc SMART `system|user/<Loại|*>.<read|write|*|cruds>`); `fhir_access_policy` (nếu có) là mảng hợp lệ.

| Claim `fhirauth` | Medplum có không | Đề xuất Go |
|---|---|---|
| `iss` | có (`config.issuer`, mặc định = `baseUrl`, **có dấu `/` cuối**, `config/utils.ts:24`) | Đặt `AUTH_ISSUER` rõ ràng; `FHIR_JWT_ISSUER` phải bằng đúng chuỗi này từng ký tự. |
| `aud` | `= iss` (`keys.ts:259`) | Mặc định `aud = [issuer]` và đặt `FHIR_JWT_AUDIENCE = issuer`; cho phép cấu hình thêm audience riêng (`AUTH_ACCESS_AUDIENCES`, mảng; `golang-jwt` `WithAudience` chấp nhận mảng chứa). Kiểm `aud` trong `introspect`, `userinfo` vẫn dùng phần tử `issuer`. |
| `sub` | `user.id` | Giữ. |
| `exp` | có | Giữ. |
| `scope` | `login.scope` (ví dụ `openid`), **không** có `fhir.*` | **Lệch nghiêm trọng.** Medplum hiểu Login chỉ có `openid` là "toàn quyền theo membership". `fhirauth` thì với scope `openid` trả `403` (thiếu scope FHIR, test `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go` ca `tok(..., "openid email")` → 403). Đề xuất: scope claim = scope đã lọc của Login + **scope vai trò do auth tự thêm** (xem dưới). |
| `fhir_project` | không có; project suy ra từ `Login` → `ProjectMembership` mỗi request | Thêm: `fhir_project = resolveId(membership.project)` (chuỗi id). **Chưa xác nhận** định dạng `Meta.project` mà `fhirauth.Scope` so với claim trong kho Go (e2e dùng chuỗi tuỳ ý như `"A"`); kiểm ở B2. |
| `fhir_access_policy` | không có; Medplum dựng `AccessPolicy` mỗi request từ membership (`fhir/accesspolicy.ts:174-203`) | Thêm khi membership có chính sách: mảng `{resourceType, criteria, readonly}` lấy từ `AccessPolicy.resource` của membership. Không có chính sách thì **bỏ claim** (`fhirauth`: vắng nghĩa là không giới hạn theo loại; mảng rỗng nghĩa là từ chối tất cả). |

**Scope vai trò auth tự thêm** (`fhirauth` coi các scope này là quyền):

1. Nếu scope đã lọc của Login chứa **bất kỳ** scope SMART (`system/`, `user/`, `patient/`): **không** thêm `fhir.*`. Lý do: `grantsFromScopes` cộng dồn, `fhir.write` cấp `*` ghi (`pkg/fhirauth/grants.go:100-114`) sẽ phá việc SMART thu hẹp quyền.
2. Ngược lại: thêm `fhir.admin` nếu `membership.admin == true`; không thì `fhir.write`; và `fhir.read` thay cho `fhir.write` nếu mọi rule của chính sách là `readonly`. (Đề xuất; `fhirauth.RoleAdmin` gating chỉ thấy ở `auth.go:93-102` của fhir-server cho `$reindex`, `$expunge`; **chưa xác nhận** có map nào khác.)
3. `fhir.super`: chỉ thêm khi `Project.superAdmin` **và** `fhir-server` bật `AllowSuperAdmin`. Mặc định không phát (Q8).
4. `patient/...`: `fhirauth` bỏ qua, không cấp quyền (`grants.go:75-77`). Hệ quả: app SMART bệnh nhân chưa có truy cập cho tới khi `fhir-server` hỗ trợ compartment. Ghi vào rủi ro.

**Lọc scope do client khai (bắt buộc, không có ở Medplum):** Medplum lưu nguyên `scope` từ request (`token.ts:183`, `utils.ts:212`). Nếu Go làm vậy thì một client `client_credentials` chỉ cần gửi `scope=fhir.admin` là nhận token admin từ góc nhìn `fhirauth`. Quy tắc:

- Không bao giờ chấp nhận `fhir.*` trong scope do client yêu cầu (tiền tố `fhir.` thuộc không gian tên dành riêng của auth): `invalid_scope` hoặc loại bỏ lặng lẽ (RFC 6749 §3.3 cho phép), chọn `invalid_scope` cho `client_credentials`.
- Scope SMART yêu cầu phải **không rộng hơn** quyền của membership: dựng chính sách từ membership, nếu `system/*.write` mà mọi rule chỉ `readonly` thì claim `fhir_access_policy` (giao với Grants ở `fhirauth`) đã chặn ghi; vẫn thêm test "SMART ghi + chính sách chỉ đọc → `fhir-server` từ chối ghi".
- Kích thước claim: JWT chứa `fhir_access_policy` có thể lớn; đặt trần (ví dụ 8 KiB), vượt thì từ chối cấp token và ghi log (chưa xác nhận giới hạn header của `fhir-server`).

**Hạn chế của `fhir_access_policy` so với Medplum** (`pkg/fhirauth/policy.go:12-18`, `92-99`): không hỗ trợ `compartment`, `hiddenFields`, tham số hoá (`%profile`...), `interaction` chi tiết, chained/`_has`/`_include` trong `criteria`. Quy tắc: nếu AccessPolicy của membership dùng tính năng mà `fhirauth` không hiểu thì **từ chối cấp token** (`access_denied` / `Access policy not supported`) thay vì cấp quyền rộng hơn dự định (fail-closed). Mức độ phổ biến của các tính năng đó trong dữ liệu Trivita: chưa xác nhận (Q9).

### 4.4 Khác biệt kiến trúc: token có trạng thái hay không

Medplum xác minh Login **mỗi request** (`getLoginForAccessToken`, `utils.ts:1234-1251`): thu hồi, dùng lại code, đăng xuất, tắt membership hay project đều có hiệu lực ngay. `fhirauth` xác minh **chỉ bằng JWKS**, không biết Login. Hệ quả cần chấp nhận hoặc giải quyết:

- Cửa sổ vô hiệu hoá = TTL access token (mặc định Medplum 1 giờ). Đề xuất `AUTH_ACCESS_TTL` mặc định `1h` để khớp test `expires_in=3600`, khuyến nghị production `15m`.
- Test Medplum phụ thuộc tính chất có trạng thái cần **sửa kỳ vọng**: `token.test.ts:504-525` (sau khi tắt membership, `/auth/me` trả 401), `refresh-token-grant.test.ts:203-231` (access token chết sau khi thu hồi, dòng 226-230), `logout.test.ts:28-67` (dòng 53-56: `userinfo` sau logout trả 401). Trong Go: `userinfo`, `introspect`, `logout` do **auth** phục vụ nên vẫn có trạng thái (xác minh Login mỗi request) và giữ kỳ vọng 401; chỉ `fhir-server` mới có cửa sổ trễ.
- Phương án tăng cường (ngoài P3, hỏi ở Q2): `fhir-server` gọi `introspect` cho thao tác nhạy cảm, hoặc auth đẩy danh sách thu hồi ngắn hạn (`jti`/`login_id`) qua Redis.

### 4.5 Nguồn dữ liệu hồ sơ (profile)

`userinfo` và `sendTokenResponse` cần đọc `Patient`/`Practitioner` (và `Bot`/`ClientApplication`) mà membership trỏ tới (`userinfo.ts:27`, `token.ts:1137`). Ở Medplum chúng nằm chung DB. Với D2 (kho auth tách khỏi kho lâm sàng), `profile` nằm ở kho của `fhir-server`. Cần cổng `ProfileReader` (đọc chung database ở chế độ chỉ đọc, hoặc gọi HTTP `fhir-server` bằng token dịch vụ). Chưa quyết (Q10); P3 mặc định chỉ cần `membership.profile` (tham chiếu) cho token, còn `userinfo` đầy đủ trả thiếu nếu cổng chưa có.

---

## 5. `/oauth2/authorize`, consent, userinfo, introspect, logout, register, well-known

### 5.1 `/oauth2/authorize` (GET và POST)

Điểm quan trọng: **`/oauth2/authorize` của Medplum không tự phát code và không hiện màn hình đăng nhập**. Nó kiểm tra tham số rồi chuyển người dùng (302) sang `appBaseUrl + 'oauth'` kèm toàn bộ tham số (`authorize.ts:332-338`), nơi ứng dụng web Medplum (gói khác, không ở `server`) hiển thị đăng nhập, chọn profile, chọn scope, bằng cách gọi `/auth/login`, `/auth/profile`, `/auth/scope` (P2), rồi ứng dụng web tự chuyển về `redirect_uri` kèm `code`. Chỉ hai trường hợp server tự trả code: `prompt=none` với phiên sẵn có, và tạo Login mới từ phiên sẵn có (bên dưới). Với Go: giữ nguyên cách chia này (cấu hình `AUTH_APP_BASE_URL`); ai làm giao diện đăng nhập là câu hỏi mở Q1.

GET đọc `req.query`, POST đọc `req.body` (form), cùng một hàm (`authorize.ts:27-48`). Thuật toán `validateAuthorizeRequest` (`authorize.ts:60-169`), theo thứ tự:

*Lỗi hiển thị trực tiếp (không chuyển hướng, `400` văn bản thuần):*

1. `getClientApplication(client_id)` lỗi: `Client not found`.
2. `redirect_uri` vắng: `Missing redirect URI`.
3. `redirect_uri` không parse được thành URL: `Invalid redirect URI`.
4. `redirect_uri` không **bằng chính xác** một phần tử của `redirectUri` + `redirectUris` (`clients.ts:60-75`, không partial): `Invalid redirect URI`.

*Lỗi chuyển hướng về `redirect_uri` (302, thêm `error`, `error_description`, `state`; `authorize.ts:312-324`):*

5. `scope` vắng: dùng `client.defaultScope.join(' ')`; vẫn rỗng: `invalid_request` / `Missing scope`.
6. `response_type !== 'code'`: `unsupported_response_type` / `Invalid response type`.
7. Có tham số `request`: `request_not_supported` / `Unsupported request parameter`.
8. `aud` có nhưng không cùng protocol+host với `baseUrl` hoặc không parse được: `invalid_request` / `Invalid audience` (vắng thì cho qua, `authorize.ts:176-182`; test `authorize.test.ts:171-250`).
9. Có `code_challenge` mà thiếu `code_challenge_method`: `invalid_request` / `Missing code challenge method`. (Không kiểm giá trị method và **không bắt buộc** PKCE ở đây.)
10. Có `launch` mà `SmartAppLaunch` không tồn tại: `invalid_request` / `Invalid launch`.
11. Tìm phiên sẵn có: ưu tiên `id_token_hint`, sau đó cookie.
    - `id_token_hint`: `verifyJwt` (issuer đúng), `aud === client.id`, **không** có `refresh_secret`, Login tồn tại và chưa thu hồi (`authorize.ts:236-273`; test `authorize.test.ts:531-590` và `592-637` cho ca dùng refresh token làm hint bị từ chối).
    - Cookie `medplum-<client.id>` (`authorize.ts:281-302`): tìm Login có `cookie` bằng giá trị đó, phải `granted && !revoked`.
    - Tuổi phiên: `authTime` so với `max_age` (mặc định 3600 giây, đọc từ `req.query.max_age` kể cả với POST, `authorize.ts:222`); quá hạn thì coi như không có phiên. `max_age=abc` cho `NaN` làm phép so luôn sai nên **bỏ qua kiểm tuổi** (lỗi; Go phải validate).
12. `prompt=none` và không có phiên: `login_required` / `Login required`.
13. Có phiên và `prompt !== 'login'`: tạo **Login mới** (`authorize.ts:127-149`): `basedOn=existing`, sao `client`, `project`, `profileType`, `user`, `membership`, `scope`, `authMethod`, `authTime`, `mfaVerified`, `pictureUrl`; `nonce` từ request; `codeChallenge`/`method` lấy từ request **hoặc kế thừa từ Login cũ** (lỗi tiềm ẩn: kế thừa challenge của lần ủy quyền khác; Go không kế thừa); `code=generateSecret(16)`; `refreshSecret` mới nếu Login cũ có; `launch` từ request hoặc kế thừa; `remoteAddress`, `userAgent`; `granted=false`. Login cũ không bị đụng (test `authorize.test.ts:489`).
    - `prompt=none`: redirect thẳng `redirect_uri?code=...&state=...` (nối `state` kể cả rỗng). Không cho đổi scope. Không có tham số `iss`.
    - Còn lại: `params.login = newLogin.id` (và `scope` = scope Login nếu thiếu) rồi 302 sang trang `oauth` của ứng dụng để người dùng xác nhận scope (test `authorize.test.ts:329-376`).
14. Còn lại (không có phiên, không `prompt=none`): 302 sang `appBaseUrl + 'oauth'` kèm mọi tham số (`sendSuccessRedirect`), test `authorize.test.ts:251-262`.

**Cookie đăng nhập** (do `/auth/login` đặt, P2; `auth/utils.ts:548-558`): tên `medplum-<clientId>`; giá trị `login.cookie` (16 byte hex); `maxAge` 1 giờ; `httpOnly`, `secure`, `sameSite=none`. Chỉ đặt khi Login có client và đã có membership. `logout` xoá cùng thuộc tính (`logout.ts:16-24`). Go: cùng thuộc tính nhưng `Secure` cấu hình được cho dev; `SameSite=None` giữ vì ứng dụng web ở origin khác (chưa xác nhận hạ tầng Trivita); lưu băm cookie (Q6).

**Consent/scope:** chọn scope con do `/auth/scope` (P2, `setLoginScope`, `utils.ts:739-771`): scope chọn phải là tập con theo `matchesScope` (loại quyền `system` ⊃ `user` ⊃ `patient` nếu có ngữ cảnh bệnh nhân, loại resource, chữ cái `c,r,u,d,s`, tham số lọc), và Login không được `granted`/`revoked`/thiếu membership. `ClientApplication.showScopeSelection` quyết định có hiện màn chọn scope hay không (trường có ở `pkg/fhir`; chỗ dùng ở phía ứng dụng web **chưa xác nhận**). P3 chỉ cung cấp thư viện scope (`domain/oauth/scope.go`) cho P2.

**Launch/SMART:** `launch` = id `SmartAppLaunch`; Login lưu `launch`; token response trả `patient`, `encounter`, `fhirContext`, `need_patient_banner`, `smart_style_url` (3.8). Scope `patient/` ở `client_credentials` bị chặn. `launch` + `Project.superAdmin` bị chặn khi dựng access policy (`fhir/accesspolicy.ts:176-179`); thuộc P2.

**Đề xuất Go cho authorize:** giữ 14 bước trên và bổ sung (lý do ở mục 8): thêm tham số `iss` vào mọi redirect (RFC 9207); validate `max_age`; không kế thừa PKCE từ Login cũ; kiểm giá trị `code_challenge_method` ∈ {`S256`, `plain` nếu cho phép} và **bắt buộc** `code_challenge` với client công khai/không có secret; lưu `redirect_uri` vào Login mới để token endpoint đối chiếu; `Referrer-Policy: no-referrer` và `X-Frame-Options: DENY` cho response `authorize`.

### 5.2 `/oauth2/userinfo` (GET, POST, Bearer)

1. Middleware `authenticateRequest` (`middleware.ts:42-57`): chưa xác thực thì `401`, header `WWW-Authenticate: Bearer realm="<baseUrl>"` (hoặc `Basic` khi có `?_medplum-prompt-basic-auth`), body OperationOutcome `unauthorized`. Go: `401` với `WWW-Authenticate: Bearer error="invalid_token"` (khớp cách `fhir-server` đã làm).
2. `Bearer`: `getLoginForAccessToken` (3.8): `aud === issuer`, không có `refresh_secret`, Login tồn tại có `membership`, chưa `revoked`, membership/project active (`utils.ts:1201-1252`). ID token và refresh token bị từ chối (`middleware.test.ts:231-268`).
3. Đọc `User` theo `login.user` và profile theo `membership.profile`. Body theo scope tách bằng khoảng trắng (khớp **từng từ**, khác với `email` trong token dùng chuỗi con):
   - luôn: `sub = user.id`.
   - `profile`: `profile` (tham chiếu), `locale:"en-US"`, `updated_at` (giây, theo `meta.lastUpdated`); với `Patient`/`Practitioner` thêm `name`, `family_name`, `given_name`, `middle_name`, `birthdate`, `gender`, `picture` (`photo[0].url`), `nickname`, `website`, `preferred_username` (phần trước `@` của email trong `telecom`), `zoneinfo` (extension timezone).
   - `email`: `email`, `email_verified = Boolean(user.emailVerified)` nếu `user.email`; không thì lấy `telecom` email của profile với `email_verified=false`.
   - `phone`: `phone_number`, `phone_number_verified=false`.
   - `address`: `address{formatted, street_address, locality, region, postal_code, country}`.
   - Chỉ `openid`: chỉ có `sub` (test `userinfo.test.ts:198-228`).

### 5.3 `/oauth2/introspect` (POST, Bearer)

1. Bearer bắt buộc (`401` nếu thiếu, test `introspect.test.ts:125-145`).
2. `token` (đọc `req.body.token`, form hoặc JSON) rỗng: `400` văn bản thuần `Token to introspect must be provided`.
3. `verifyJwt(token, issuer)`; mọi ngoại lệ: `200 {"active": false}`.
4. `claims.aud !== issuer`: `{"active": false}` (loại ID token; `introspect.test.ts:95-123`).
5. Đọc Login; không có, hoặc `!granted || revoked`: `{"active": false}`.
6. `canIntrospectToken` (`introspect.ts:78-106`): super admin thì được; ngược lại Login phải thuộc project của người gọi (nếu `Login.project` vắng, như `client_credentials`, lấy từ membership), **và** `claims.client_id === client của người gọi`. Sai: `{"active": false}` (test chéo project `introspect.test.ts:229-300`).
7. Nếu là refresh token (`refresh_secret` có): chỉ `active` khi bằng secret hiện tại của Login (hằng thời gian); token đã xoay: `{"active": false}` (`introspect.test.ts:147-191`).
8. Đang hoạt động: `200 {"active":true, "iat", "exp", "iss", "sub", "client_id", "scope", "patient"}`. `patient` lấy từ `SmartAppLaunch.patient.reference` bỏ tiền tố `Patient/`, hoặc từ `profile` nếu bắt đầu bằng `Patient/`. Không có `aud`, `jti`, `token_type`, `username`.

### 5.4 `/oauth2/logout` (GET, POST, Bearer)

1. Bearer bắt buộc (`logout.test.ts:23-26`).
2. `revokeLogin(ctx.login)` = `updateResource({...login, revoked:true})`: ghi cả bản chụp (có thể ghi đè secret mới xoay; Go dùng patch/CAS-retry).
3. Nếu Login có client: xoá cookie `medplum-<clientId>` (`maxAge` 1 giờ, `sameSite=none`, `secure`, `httpOnly`).
4. `200` OperationOutcome `allOk`. Sau đó cùng access token gọi `userinfo` hay `logout` đều `401` (`logout.test.ts:28-67`).

Chỉ thu hồi **một** Login; các Login tạo từ `authorize` (`basedOn`) và có cùng phiên trình duyệt vẫn sống. Không phải RP-initiated logout của OIDC (không có `end_session_endpoint`, `id_token_hint`, `post_logout_redirect_uri`). Go: thu hồi cả chuỗi `basedOn` nếu `fhirstore` tra được (chưa xác nhận có search param `based-on` cho Login; Q11).

### 5.5 `/oauth2/register` (POST)

Không phải đăng ký động thật (RFC 7591): chỉ tra **client chuẩn** có sẵn theo `redirect_uris[0]` (`register.ts:16-40`):

1. `redirect_uris[0]` vắng: `400` `{"error":"invalid_request","error_description":"Redirect URI is required"}`.
2. Không client chuẩn nào có URI đó **bằng chính xác**: `400` `invalid_request` / `Invalid redirect URI`.
3. `201` `{"client_id": "<id client chuẩn>", "client_id_issued_at": <giây>, "redirect_uris": [...]}`; không trả `client_secret` (test `register.test.ts:49-60`).

Client chuẩn: `medplum-cli` với `redirectUris:['http://localhost:9615']`, `pkceOptional:true` (`clients.ts:20-26`) cộng `config.defaultOAuthClients` (`clients.ts:29-32`; thêm cho MCP của Claude/ChatGPT theo comment `clients.ts:13`). Go: cấu hình `AUTH_STANDARD_CLIENTS` (JSON), **mặc định rỗng**, không dựng sẵn `medplum-cli` (Q12).

### 5.6 `/.well-known/*`

`issuer` Medplum = `config.issuer` (mặc định = `baseUrl`, có `/` cuối); các URL điểm cuối mặc định `concatUrls(baseUrl, '/oauth2/authorize')` v.v. (`config/utils.ts:24-30`). JSON mẫu dưới đây theo đúng source với `baseUrl = https://auth.example.com/`.

**`GET /.well-known/openid-configuration` và `/oauth-authorization-server`** (`wellknown.ts:18-51`):

```json
{
  "issuer": "https://auth.example.com/",
  "authorization_endpoint": "https://auth.example.com/oauth2/authorize",
  "token_endpoint": "https://auth.example.com/oauth2/token",
  "userinfo_endpoint": "https://auth.example.com/oauth2/userinfo",
  "jwks_uri": "https://auth.example.com/.well-known/jwks.json",
  "introspection_endpoint": "https://auth.example.com/oauth2/introspect",
  "registration_endpoint": "https://auth.example.com/oauth2/register",
  "id_token_signing_alg_values_supported": ["ES256", "ES384", "HS256", "RS256"],
  "grant_types_supported": [
    "client_credentials", "authorization_code", "refresh_token",
    "urn:ietf:params:oauth:grant-type:token-exchange"
  ],
  "response_types_supported": ["code", "id_token", "token id_token"],
  "subject_types_supported": ["pairwise", "public"],
  "scopes_supported": ["openid", "profile", "email", "phone", "address"],
  "token_endpoint_auth_methods_supported": ["client_secret_basic", "client_secret_post", "private_key_jwt"],
  "request_object_signing_alg_values_supported": ["none"],
  "code_challenge_methods_supported": ["S256", "plain"]
}
```

(Giá trị chuỗi của `OAuthGrantType.*`, `OAuthTokenAuthMethod.*` lấy theo tên chuẩn RFC, chưa đọc từ `@medplum/core`.)

Điểm Medplum khai không đúng việc làm được: `id_token`, `token id_token` (authorize chỉ nhận `code`, `authorize.ts:94`); `HS256` (không bao giờ ký HS256, `keys.ts`); `pairwise` (chỉ có `sub` công khai); `pre-authorized_code` thì có xử lý nhưng không khai. Go khai đúng:

```json
{
  "issuer": "<issuer>",
  "authorization_endpoint": "<base>/oauth2/authorize",
  "token_endpoint": "<base>/oauth2/token",
  "userinfo_endpoint": "<base>/oauth2/userinfo",
  "jwks_uri": "<base>/.well-known/jwks.json",
  "introspection_endpoint": "<base>/oauth2/introspect",
  "registration_endpoint": "<base>/oauth2/register",
  "id_token_signing_alg_values_supported": ["RS256"],
  "grant_types_supported": ["authorization_code", "refresh_token", "client_credentials"],
  "response_types_supported": ["code"],
  "subject_types_supported": ["public"],
  "scopes_supported": ["openid", "profile", "email", "phone", "address"],
  "token_endpoint_auth_methods_supported": ["client_secret_basic", "client_secret_post", "private_key_jwt"],
  "token_endpoint_auth_signing_alg_values_supported": ["RS256", "RS384", "RS512", "ES256", "ES384", "ES512"],
  "code_challenge_methods_supported": ["S256"]
}
```

(`registration_endpoint` chỉ khai nếu bật `register`; `pre-authorized_code` thêm khi P6 xong; `plain` thêm khi `AUTH_ALLOW_PLAIN_PKCE=true`.)

**`GET /.well-known/smart-configuration`** (`fhir/smart.ts:62-112`):

```json
{
  "issuer": "https://auth.example.com/",
  "jwks_uri": "https://auth.example.com/.well-known/jwks.json",
  "authorization_endpoint": "https://auth.example.com/oauth2/authorize",
  "grant_types_supported": [
    "client_credentials", "authorization_code", "refresh_token",
    "urn:ietf:params:oauth:grant-type:token-exchange"
  ],
  "token_endpoint": "https://auth.example.com/oauth2/token",
  "token_endpoint_auth_methods_supported": ["client_secret_basic", "client_secret_post", "private_key_jwt"],
  "token_endpoint_auth_signing_alg_values_supported": ["RS256", "RS384", "ES384"],
  "scopes_supported": [
    "patient/*.rs", "user/*.cruds", "openid", "fhirUser", "launch",
    "launch/patient", "offline_access", "online_access"
  ],
  "response_types_supported": ["code"],
  "introspection_endpoint": "https://auth.example.com/oauth2/introspect",
  "capabilities": [
    "authorize-post", "permission-v1", "permission-v2",
    "client-confidential-asymmetric", "client-confidential-symmetric", "client-public",
    "context-banner", "context-ehr-patient", "context-ehr-encounter", "context-standalone-patient",
    "context-style", "launch-ehr", "launch-standalone", "permission-offline",
    "permission-patient", "permission-user", "sso-openid-connect"
  ],
  "code_challenge_methods_supported": ["S256"]
}
```

Go: bỏ `token-exchange` đến P6; bỏ `patient/*.rs`, `permission-patient`, `context-*patient*`, `context-banner`, `launch-ehr`, `launch-standalone` cho đến khi `fhirauth` hỗ trợ scope `patient/` và `fhir-server` phục vụ ngữ cảnh (hiện `grants.go:75-77` bỏ qua `patient/`); danh sách capability còn lại phải được rà lại ở B1. Medplum chỉ khai `S256` ở đây dù `token.ts:1198` vẫn nhận `plain` (không nhất quán).

**`GET /.well-known/smart-styles.json`** (`fhir/smart.ts:120-132`):

```json
{
  "color_background": "#edeae3", "color_error": "#9e2d2d", "color_highlight": "#69b5ce",
  "color_modal_backdrop": "", "color_success": "#498e49", "color_text": "#303030",
  "dim_border_radius": "6px", "dim_font_size": "13px", "dim_spacing_size": "20px",
  "font_family_body": "Georgia, Times, 'Times New Roman', serif",
  "font_family_heading": "'HelveticaNeue-Light', Helvetica, Arial, 'Lucida Grande', sans-serif;"
}
```

(Dấu `;` thừa ở cuối `font_family_heading` có trong source `smart.ts:130`; Go bỏ.)

**`GET /.well-known/oauth-protected-resource`** (`wellknown.ts:53-75`): tham số `resource` tuỳ chọn; không phải chuỗi hoặc không bắt đầu bằng `baseUrl`: `400` văn bản `Invalid resource URL`. Body:

```json
{
  "resource": "https://auth.example.com/",
  "issuer": "https://auth.example.com/",
  "authorization_servers": ["https://auth.example.com/"],
  "scopes_supported": ["openid", "profile", "email", "phone", "address"],
  "bearer_methods_supported": ["header"],
  "introspection_endpoint": "https://auth.example.com/oauth2/introspect"
}
```

Với Trivita, `resource` nên là URL gốc của `fhir-server` (tài nguyên được bảo vệ thật sự), không phải của auth (Q13). Test `wellknown.test.ts:121-163` (`resource` tùy chỉnh, không hợp lệ, mảng).

**`GET /.well-known/jwks.json`:** xem 4.2. **`/.well-known/api-catalog`:** không port (mục 9).

---

## 6. Cổng (ports), adapter, thư viện, cấu hình

### 6.1 Cổng ở `application/oauth/ports.go`

| Cổng | Phương thức chính | Adapter | Ghi chú |
|---|---|---|---|
| `LoginStore` | `GetByID`, `FindByCode(codeHash)`, `FindByCookieHash`, `FindByPreAuthHash`, `Create`, `ClaimGrant(id, versionID)` (CAS `granted=true`), `RotateRefreshSecret(id, expected, new, meta)`, `Revoke(id)`, `SetRemote(id, ip, ua)` | `infrastructure/store` trên `pkg/fhirstore` (kho auth riêng, D2) với `UpdateOptions{IfMatch}` | Các thao tác CAS phải có test cạnh tranh |
| `ClientStore` | `Get(id)`; client chuẩn nạp từ cấu hình | như trên | tách client chuẩn khỏi DB |
| `MembershipStore` | `Get(ref)`, `ForClient(client, projectHint)` | như trên | Q5 |
| `ProjectStore`, `UserStore`, `LaunchStore`, `AccessPolicyStore` | đọc | như trên | `AccessPolicyStore` để dựng claim và kiểm IP |
| `ProfileReader` | `Read(ref)` (Patient, Practitioner, Bot...) | Q10 | `userinfo`, `patient` trong response |
| `KeyStore` | `Signer()`, `PublicJWKS()`, `Verifier(kid)` | `infrastructure/keys` (thư mục PEM) | mục 4.2 |
| `Clock`, `Random` | `Now()`, `Bytes(n)` | hệ thống / giả trong test | không dùng `time.Now` trực tiếp trong domain |
| `ClientAssertionVerifier` | `Verify(assertion, tokenURL)` | `infrastructure/clientassertion` + `safehttp` + bộ nhớ đệm JWKS | cộng `JTIStore` |
| `JTIStore` | `UseOnce(jti, exp) bool` | Redis (đã có `go-redis` trong `go.mod`) hoặc bộ nhớ trong test | chống phát lại assertion |
| `AuditLog` | `Record(event)` | slog + observability hiện có | thay `logPkceOptionalCodeRedemption`, ghi dùng lại refresh/code |
| `ExternalIdentityResolver` | `Resolve(idp, subjectToken)` | **P6**, P3 chỉ khai báo | `token-exchange` |
| `RateLimiter` | `Allow(key)` | dùng thành phần có sẵn của `pkg/httpserve` nếu có (**chưa xác nhận** nội dung `pkg/httpserve`; kiểm ở P0) | Medplum giới hạn tiền tố `/auth/`, `/oauth2/` (`ratelimit.ts:60`) |

`architecture_test.go` thêm luật: `domain/oauth` chỉ stdlib; `application/oauth` chỉ `domain` và `pkg/fhir*` cần thiết; `presentation` chỉ gọi `application`.

### 6.2 Thư viện

| Việc | Thư viện | Có sẵn trong `go.mod`? |
|---|---|---|
| Ký/xác minh JWT, đọc claim | `github.com/golang-jwt/jwt/v5` (cùng thư viện `pkg/fhirauth`) | có (`go.mod:66`) |
| JWK/JWKS (đọc khóa công khai, JWKS client assertion) | `github.com/go-jose/go-jose/v4` | có (`go.mod:61`) |
| HTTP, JSON | `github.com/labstack/echo/v4` | có |
| Băm, ngẫu nhiên, so hằng thời gian | stdlib `crypto/sha256`, `crypto/rand`, `crypto/subtle` | stdlib |
| Redis (`JTIStore`, tuỳ chọn limiter) | `github.com/redis/go-redis/v9`, test `miniredis` | có |
| Swagger | `swaggo/swag`, `echo-swagger` | có |
| X.509 cho mTLS | stdlib `crypto/x509` | stdlib (chỉ khi làm mTLS; hoãn) |

`bcrypt` không cần ở P3 (thuộc P1/P2).

### 6.3 Cấu hình (env, tiền tố `AUTH_`)

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `AUTH_BASE_URL` | bắt buộc | URL gốc công khai của auth, có `/` cuối |
| `AUTH_ISSUER` | = `AUTH_BASE_URL` | khớp từng ký tự với `FHIR_JWT_ISSUER` |
| `AUTH_ACCESS_AUDIENCES` | `[issuer]` | danh sách `aud` của access token |
| `AUTH_APP_BASE_URL` | bắt buộc nếu bật authorize | trang `oauth` của ứng dụng đăng nhập |
| `AUTH_SIGNING_ALG` | `RS256` | `RS256`, `ES256`, `ES384` |
| `AUTH_SIGNING_KEYS_DIR`, `AUTH_ACTIVE_KID` | bắt buộc | khóa PEM, khóa ký |
| `AUTH_ACCESS_TTL`, `AUTH_REFRESH_TTL`, `AUTH_ID_TTL` | `1h`, `2w`, `1h` | dùng cùng cú pháp `\d+[smhdwy]`; client ghi đè bằng `accessTokenLifetime`, `refreshTokenLifetime` |
| `AUTH_AUTH_CODE_TTL` | `10m` | |
| `AUTH_REFRESH_GRACE` | `30s` | `0` là tắt |
| `AUTH_SESSION_MAX_AGE` | `0` | trần tuyệt đối của phiên refresh |
| `AUTH_ALLOW_PLAIN_PKCE` | `false` | cho `plain` (cần để chạy lại test Medplum dùng `plain`, nên đổi test sang S256 thay vì bật) |
| `AUTH_REQUIRE_CLIENT_AUTH_ON_REFRESH` | quyết ở Q4 | |
| `AUTH_TRUSTED_PROXIES` | rỗng | để `echo.IPExtractor` lấy IP thật (ân hạn, kiểm IP) |
| `AUTH_COOKIE_SECURE`, `AUTH_COOKIE_SAMESITE` | `true`, `none` | |
| `AUTH_STANDARD_CLIENTS` | rỗng | JSON client chuẩn cho `register` |
| `AUTH_ENABLE_REGISTER` | `false` | |
| `AUTH_MAX_POLICY_CLAIM_BYTES` | `8192` | trần claim `fhir_access_policy` |
| `AUTH_REDIS_URL` | rỗng | cho `JTIStore`/limiter; trống thì bộ nhớ (chỉ dev) |

---

## 7. Danh sách ca test

Ký hiệu cột "Xử lý": **Giữ** (port nguyên kỳ vọng), **Sửa** (port nhưng đổi kỳ vọng, nêu rõ), **Hoãn** (làm ở pha sau), **Bỏ** (không port, kèm lý do), **Mới** (thêm, không có ở Medplum). `T:` = `token.test.ts`. Tên ca giữ nguyên tiếng Anh của Medplum để đối chiếu. Viết tắt file: `RT` = `refresh-token-grant.test.ts`, `AS` = `auth-strategies.test.ts`, `AZ` = `authorize.test.ts`, `K` = `keys.test.ts`, `W` = `wellknown.test.ts` (ở `src/`), `C` = `clients.test.ts`, `I` = `introspect.test.ts`, `UI` = `userinfo.test.ts`, `L` = `logout.test.ts`, `R` = `register.test.ts`, `M` = `middleware.test.ts`, `RO` = `routes.test.ts` (đều trong `oauth/` trừ `W`). Test Medplum dùng `codeChallengeMethod: 'plain'` (ví dụ `T:690`, `T:724`, `T:1590`) phải đổi sang `S256` khi `AUTH_ALLOW_PLAIN_PKCE=false`.

### 7.1 `/oauth2/token` chung

| Ca (file:dòng) | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Token with wrong Content-Type (T:259) | gửi JSON → `400`, text `Unsupported content type` | Giữ |
| Token with missing grant type (T:267) | `400 invalid_request` `Missing grant_type` | Giữ |
| Token with unsupported grant type (T:277) | `400 invalid_request` `Unsupported grant_type` | Giữ (lệch RFC, Q3) |
| Too many requests (T:2619) | giới hạn tốc độ | Hoãn: thuộc `pkg/httpserve`/hạ tầng, test riêng |

### 7.2 `client_credentials`

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Token for client credentials success (T:287) | `200`, có `access_token`, Login ghi `meta.project` | Giữ |
| Token for client credentials with inactive ProjectMembership / Project (T:302) | `400 access_denied` `Profile not active` / `Project not active` | Giữ |
| Token for super admin client credentials with scope openid / `openid system/*.rs` (T:320) | `200` / `400` `SMART scopes are not supported for super admin logins` | Hoãn: cần super admin (Q8) |
| …with missing client_id (T:337) | `400 invalid_request` `Missing client_id` | Giữ |
| …with missing client_secret (T:348) | `Missing client_secret` | Giữ |
| …with wrong client_id (T:359) | `Invalid client` | Giữ |
| …with wrong client_secret (T:370) | `Invalid secret` | Giữ |
| …with secondary client_secret (T:381) | `retiringSecret` được chấp nhận → `200` | Giữ |
| …authentication header success (T:392) | Basic → `200` | Giữ |
| …wrong authentication header (T:405) | `Bearer xyz` → `400 invalid_request` `Invalid authorization header` | Giữ |
| Client credentials auth header empty secret (T:414) | Basic với secret rỗng gọi thẳng `GET /fhir/R4/Patient` → `401` | Bỏ: Basic trực tiếp vào API FHIR không port |
| Token for client empty secret (T:435) | client có `secret:''` → `Invalid client` | Giữ |
| Token for client without project membership (T:457) | `Invalid client` | Giữ |
| Token for client in "off" status (T:476) | `Invalid client` | Giữ |
| Token for client in "active" status (T:490) | `200` | Giữ |
| Token for client with inactive ProjectMembership (T:504) | `200`, sau khi tắt membership `/auth/me` trả `401` | Sửa: kiểm `introspect` trả `{active:false}` và `userinfo` `401` (access token stateless với `fhir-server`, mục 4.4) |
| Client credentials on project-scoped URL uses membership for that project (T:527) | `/projects/{id}/oauth2/token` | Bỏ: không port URL theo project (mục 9); thay bằng test chọn membership (Q5) |
| Client credentials IP address restriction (T:565) | IP bị chặn → `400 invalid_request` `IP address not allowed`; IP cho phép → `200` | Giữ (cần `AccessPolicyStore`, có thể làm cuối pha) |
| FHIRcast scopes added to client credentials flow (T:2685), FHIRcast scopes NOT added (T:2699) | `hub.topic`, `hub.url` | Ban đầu bỏ; **đã làm ở FC-6** (scope đã cấp có `fhircast/` thì thêm `hub.url`, `hub.topic`) |
| Client credentials: scope `fhir.admin` (mới) | request `scope=fhir.admin` → `400 invalid_scope`, không có token | Mới |
| Client credentials: scope `patient/*.rs` (mới) | `400 invalid_scope` `Cannot use client credentials with patient scope` | Mới (Medplum trả OperationOutcome) |
| Client credentials: Login không rác khi bị chặn IP (mới) | sau lỗi IP không còn Login `granted` | Mới |

### 7.3 `authorization_code`

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Token for authorization_code with missing code (T:602) | `Missing code` | Giữ |
| …with bad code (T:613) | `Invalid code` | Giữ |
| …with expired code (T:624) | code quá 10 phút → `400 invalid_grant` `Authorization code expired` | Sửa: dựng bằng `Clock` giả/`expiresAt`, không dùng `meta.lastUpdated` |
| …with invalid client ID (T:663) | thiếu code → `Missing code` | Giữ |
| …with invalid authorization header (T:675) | `Invalid authorization header` | Giữ |
| Authorization code missing verification (T:686) | Login không challenge, không client → `Missing verification context` | Giữ |
| Authorization code missing code_verifier (T:702) | `Missing code verifier` | Giữ |
| Authorization code token with code verifier success (T:720) | `200`, `expires_in 3600`, scope `openid profile email`, có `id_token` với `email`, không `refresh_token` | Giữ (đổi sang S256) |
| …with incorrect code verifier and client secret (T:747) | `400`, không có `access_token` | Giữ |
| …with code challenge and PKCE optional (T:770) | client `pkceOptional`, có challenge, không verifier, không secret → `200` | **Sửa (đảo)**: `400 invalid_request` `Missing code verifier`; chỉ `200` khi gửi verifier đúng hoặc xác thực bằng secret nếu Login không có challenge (Q4) |
| Logs pkceOptional code redemption without PKCE or client authentication (T:793) | chỉ kiểm log theo từng tình huống | Sửa: thay bằng kiểm `AuditLog` và kết quả từ chối |
| …with wrong client secret (T:866) | `Invalid secret` | Giữ |
| …with secondary client secret (T:885) | `200` | Giữ |
| …with client secret success (T:904) | `200`, scope `openid`, không refresh | Giữ |
| Authorization code revoked (T:927) | Login `revoked` → `400 invalid_grant` `Token revoked` | Giữ |
| …success with refresh using legacy remember flag (T:959) | `refresh_token` có | Hoãn: cờ `remember` thuộc P2/`includeRefreshToken`; P3 chỉ kiểm khi Login có `refreshSecret` |
| …using scope=offline (T:983) | scope `offline` cũ → có refresh, scope trả `openid offline` | Hoãn: như trên (đã deprecated, có thể bỏ hẳn) |
| …using scope=offline_access (T:1007) | có refresh | Giữ |
| Authorization code token success with client ID (T:1031) | gửi `client_id` khớp → `200` | Giữ |
| Authorization code token failure with client ID (T:1055) | `client_id` khác → `Invalid client` | Giữ |
| Authorization code token failure already granted (T:1075) | lần 2 → `400 invalid_grant` `Token already granted`, và Login bị thu hồi | Giữ (kiểm thêm: token lần 1 bị `introspect` báo `inactive`) |
| Smart App Launch tokens (T:2127) | có `patient` và `encounter` | Giữ |
| Smart App Launch with patient / encounter identifier returns identifier value (T:2159) | trả `identifier.value` thay cho id tham chiếu | Giữ |
| Patient in token response (T:1772) | membership profile là `Patient/x` → `patient = x` | Giữ |
| IP address allow (T:2216), IP address block (T:2236) | kiểm IP ở `/auth/login` | Bỏ khỏi P3: thuộc P2 |
| Authorization code: hai request song song cùng code (mới) | 16 goroutine → đúng 1 thành công, phần còn lại `invalid_grant`, Login bị thu hồi | Mới |
| Authorization code: `redirect_uri` khác lúc authorize (mới) | `invalid_grant` `Invalid redirect_uri` | Mới |
| Authorization code: verifier sai không tiêu thụ code (mới) | thử verifier sai rồi đúng vẫn được, hoặc quyết định ngược lại và ghi rõ (Q14) | Mới |
| Authorization code: client `pkceOptional` có challenge phải xác minh verifier (mới) | verifier sai → `400` | Mới |
| Authorization code: code chỉ lưu dạng băm (mới) | đọc Login trực tiếp không thấy code gốc | Mới (nếu Q6 chốt) |

### 7.4 `refresh_token`

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Refresh token without token (T:1106) | `invalid_request` `Invalid refresh token` | Giữ |
| Refresh token with malformed token (T:1116) | như trên | Giữ |
| Refresh token success (T:1126) | `200`, scope `openid offline_access`, có `refresh_token` mới; dùng token mới tiếp tục được | Giữ |
| Refresh token failed for no refresh secret (T:1162) | Login không có `refreshSecret` → `400 invalid_request` | Giữ |
| Refresh token failed for no refresh_secret claim (T:1195) | JWT thiếu claim `refresh_secret` → `400` | Giữ |
| Refresh token failure with S256 code (T:1234) | gửi băm làm verifier → `400` | Giữ |
| Refresh token success with S256 code (T:1256) | `200` | Giữ |
| Refresh token revoked (T:1296) | Login thu hồi → `400 invalid_grant` `Token revoked` | Giữ |
| Refresh token with inactive ProjectMembership / Project (T:1341) | `400 access_denied` | Giữ |
| Refresh token Basic auth success (T:1392) | `200` | Giữ |
| Refresh token Basic auth failure wrong auth type (T:1433) | `Bearer` → `invalid_request` `Invalid authorization header` | Giữ |
| Refresh token Basic auth failure wrong client ID (T:1470) | `400 invalid_grant` `Incorrect client` | Giữ |
| Refresh token Basic auth failure wrong secret (T:1507) | secret rỗng → `invalid_grant` `Incorrect client secret` (tên ca nói "wrong secret", kỳ vọng ở T:1539-1541 là `Incorrect client secret`) | Giữ |
| Refresh token Basic auth failure incorrect secret (T:1544) | secret ngẫu nhiên → `invalid_request` `Invalid secret` | Giữ |
| Refresh token rotation (T:1576) | A→B→C đều `200`; dùng lại A sau cùng → `400 invalid_grant` `Token revoked` | Giữ |
| Rejects a refresh token signed with "alg": "none" (RT:137) | `400 invalid_request` `Invalid refresh token` | Giữ |
| Rejects a refresh token re-signed with HS256 (RT:147) | như trên | Giữ |
| Rejects a refresh token with a tampered payload (RT:159) | như trên | Giữ |
| Rejects an access token presented as a refresh token (RT:170) | như trên | Giữ |
| Rejects an ID token presented as a refresh token (RT:177) | như trên | Giữ |
| Rejects a refresh token presented as a bearer access token (RT:184) | gọi API bằng refresh token → `401` | Sửa: kiểm `userinfo` `401` và `fhirauth.Verify` từ chối (thiếu `fhir_project`/scope, `aud` riêng) |
| Rejects a refresh token redeemed under a different project scope (RT:190) | URL theo project | Bỏ (mục 9) |
| Replaying a rotated refresh token revokes the login and its access tokens (RT:203) | ngoài ân hạn → `400 invalid_grant` `Token revoked`, Login `revoked`, token xoay sau cũng bị từ chối | Giữ; phần "access token cũ cho `401`" đổi sang `userinfo` `401` (mục 4.4) |
| Rotation loses a write conflict and reports reuse rather than rotating again (RT:233) | cạnh tranh ghi → không xoay lần hai | Giữ (dựng bằng `IfMatch` xung đột) |
| Concurrent refreshes with the same token rotate once and all succeed (RT:268) | 5 request song song đều `200`, một lần xoay, Login không `revoked`, `previousRefreshSecret` = secret cũ | Giữ |
| The grace period accepts a retry of the previous token only from the same IP (RT:285) | retry cùng IP `200` không đổi `versionId`; khác IP trong cửa sổ → `400 Token revoked` | Giữ |
| A rotation rejected for a reason other than the secret does not revoke the login (RT:313) | lỗi không liên quan secret → không `Token revoked`, token vẫn dùng được | Giữ |
| Known gaps: Confidential client can refresh without client authentication (RT:341) | hiện `200` | **Sửa (đảo)** theo Q4 |
| Known gaps: IP access rules are not enforced on refresh (RT:353) | hiện `200` và ghi IP bị chặn | **Sửa (đảo)**: `400 invalid_request` `IP address not allowed`, không ghi IP bị chặn |
| accessTokenLifetime / refreshTokenLifetime: Valid duration (T:1644, T:1708) | `expires_in 60`, `exp = iat + 60`; refresh `exp = iat + 60` | Giữ |
| accessTokenLifetime / refreshTokenLifetime: Invalid duration (T:1685, T:1749) | lỗi `clapp-1` khi tạo client | Hoãn: thuộc validate `ClientApplication` ở P1 |
| Grant types refresh_token should include refresh token (T:2751) | client có `grantType:['refresh_token']` → có refresh dù scope `openid` | Hoãn: logic `includeRefreshToken` thuộc P2 |
| Refresh tokens disabled for super admins (T:2712) | không có `refresh_token` | Hoãn: super admin (Q8) |
| Refresh: Login không tồn tại (mới) | `400 invalid_grant` `Invalid refresh token` | Mới |
| Refresh: xoay 16 goroutine (mới) | đúng 1 xoay, phần còn lại ân hạn cùng IP | Mới |
| Refresh: `AUTH_REFRESH_GRACE=0` (mới) | dùng lại lập tức bị thu hồi | Mới |
| Refresh: vượt `AUTH_SESSION_MAX_AGE` (mới) | `400 invalid_grant` | Mới |
| Refresh: thu hồi → `fhir-server` (mới, e2e) | token cũ còn hiệu lực đến hết TTL, `introspect` báo `inactive` ngay | Mới |

### 7.5 `private_key_jwt`

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Client assertion success (T:1816) | `200` | Giữ |
| Client assertion client not found (T:1848) | `400 invalid_request` `Client not found` | Giữ |
| Client assertion missing jwks URL (T:1876) | `Client must have a JWK Set URL` | Giữ |
| Client assertion invalid audience (T:1902) | `Invalid client assertion audience` | Giữ |
| Client assertion invalid issuer (T:1936) | `Invalid client assertion issuer` | Giữ |
| Client assertion invalid signature (T:1970) | `Invalid client assertion signature` | Giữ |
| Client assertion multiple matching 3rd check success (T:2004) | thử tới khóa thứ 3 → `200` | Giữ |
| Client assertion multiple inner error (T:2036) | lỗi khác chữ ký → `400`, dừng sớm | Giữ |
| Client assertion invalid assertion type (T:2067) | `Unsupported client assertion type` | Giữ |
| Client assertion missing JWT (T:2101), invalid JWT (T:2114) | `Invalid client assertion` | Giữ |
| Client credentials via private_key_jwt issues a token usable against the FHIR API (AS:596) | e2e | Giữ (token dùng được với `fhirauth.Verifier`) |
| Client assertion: không có `exp` (mới) | từ chối | Mới |
| Client assertion: `jti` dùng lại (mới) | lần hai `400`, `JTIStore` | Mới |
| Client assertion: `jwks_uri` trỏ `localhost`/IP nội bộ/HTTP (mới) | bị chặn, `400` | Mới |
| Client assertion: client không có `secret` (mới) | vẫn `200` (không bắt chước ràng buộc `token.ts:1071`) | Mới |

### 7.6 `pre-authorized_code`

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Pre-authorized code with missing client_id (T:2849) | `Missing client_id` | Giữ |
| …with missing code (T:2859) | `Missing pre-authorized_code` | Giữ |
| …with invalid client_id (T:2869) | `400 invalid_client` `Invalid client` | Giữ |
| …with invalid code (T:2880) | `Invalid pre-authorized_code` | Giữ |
| Pre-authorized code expired (T:2891) | `400 invalid_grant` `Pre-authorized code expired` | Giữ |
| Pre-authorized code IP address restriction (T:2920) | IP chặn → `invalid_request` `IP address not allowed`; IP cho phép → `200` | Giữ |
| Pre-authorized code success (T:2965) | `200`, không `refresh_token`; dùng lại → `400 invalid_grant` `Token already granted` | Giữ (tạo Login bằng fixture, không qua `/auth/preauthorize`) |
| Pre-authorized code issues a token usable against the FHIR API (AS:459) | e2e | Giữ |
| Pre-authorized code: hai request song song (mới) | đúng 1 thành công | Mới |

### 7.7 `token-exchange`, mTLS, Basic, strategy khác

| Ca | Xử lý |
|---|---|
| Toàn bộ `Token exchange ...` (T:2246-2683: JSON/JWT success, chọn provider, từ chối client lạ, membership, GCIP, content type, thiếu client_id/subject_token/type, URL ngoài không hợp lệ), `JWT bearer token exchange` (AS:253-321) | Hoãn: P6 (chưa đọc chi tiết từng ca; chỉ dùng tên) |
| `mTLS ...` (T:2795, 2809, 2823), `Client credentials via mTLS` (AS:527), toàn bộ `cert.test.ts` | Bỏ: không port mTLS (mục 9) |
| `External/OIDC login` (AS:165), `Google login` (AS:360), `external.test.ts` | Hoãn: P6 |
| `Native password login` (AS:95-122) | Giữ ở P2; P3 chỉ dùng để dựng e2e |
| `Direct HTTP Basic auth against the FHIR API` (AS:490) | Bỏ |

### 7.8 `/oauth2/authorize` (`authorize.test.ts`, mã AZ)

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Client not found (AZ:54) | `400` text `Client not found` | Giữ |
| Missing redirect (AZ:68) | `400` `Missing redirect URI` | Giữ |
| Cannot parse redirect (AZ:81), Wrong redirect (AZ:95) | `400` `Invalid redirect URI` | Giữ |
| Invalid response_type (AZ:109) | `302`, `error=unsupported_response_type` | Giữ |
| Unsupported request (AZ:125) | `error=request_not_supported` | Giữ |
| Missing scope (AZ:142) | `error=invalid_request` | Giữ |
| Missing code_challenge_method (AZ:156) | `error=invalid_request` | Giữ |
| Invalid audience (AZ:171), Malformed audience (AZ:203) | `error=invalid_request` | Giữ |
| Server URL audience (AZ:219), FHIR URL audience (AZ:235) | không lỗi | Giữ (URL FHIR đổi thành URL `fhir-server`; cần cấu hình audience hợp lệ, Q13) |
| Invalid launch (AZ:187) | `error=invalid_request` | Giữ |
| Success (AZ:251) | `302` sang `appBaseUrl/oauth` | Giữ |
| prompt=none and no existing login (AZ:264) | `302` về `redirect_uri` với `error=login_required` | Giữ |
| prompt=none success (AZ:282) | có cookie → `302` về `redirect_uri` với `code`, không lỗi | Giữ |
| Show scope screen on subsequent authorize (AZ:329) | có cookie, không `prompt=none` → `302` sang `/oauth` với `login=<id mới>`, scope chứa `user/Patient.rs` | Giữ |
| Revoked cookie (AZ:378) | Login đã thu hồi + `prompt=none` → `login_required` | Giữ |
| Subsequent authorization creates a new login (AZ:435) | Login mới: `basedOn`, `granted=false`, không `cookie`, `refreshSecret` mới; Login cũ không đổi `versionId`; đổi code được cả hai | Giữ; **Sửa** `codeChallenge` (AZ:502): không kế thừa từ Login cũ khi request thiếu challenge |
| Using id_token_hint (AZ:531) | `prompt=none` + hint hợp lệ → có `code` | Giữ |
| Invalid id_token_hint (AZ:574) | hint sai → rơi về bước kế (không có `code`) | Giữ |
| Rejects expired refresh token as id_token_hint (AZ:592) | refresh token làm hint → `login_required` | Giữ |
| Post success (AZ:639), Post client not found (AZ:656) | POST form tương đương GET | Giữ |
| Missing ClientApplication.redirectUri (AZ:670), Invalid ClientApplication.redirectUri (AZ:691) | `400` `Invalid redirect URI` | Giữ |
| Authorize: lỗi redirect có tham số `iss` (mới) | RFC 9207 | Mới |
| Authorize: `max_age=abc` (mới) | `400 invalid_request` hoặc chuyển về đăng nhập, không bỏ qua kiểm tuổi | Mới |
| Authorize: client công khai không gửi `code_challenge` (mới) | `invalid_request` | Mới |
| Authorize: `redirect_uri` trong query chuỗi khớp tiền tố nhưng không bằng chính xác (mới) | `400` | Mới |

### 7.9 Khóa, JWKS, well-known, redirect (`keys.test.ts`, `wellknown.test.ts`, `clients.test.ts`)

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Missing issuer (K:28, K:56) | `initKeys` thiếu issuer → lỗi `Missing issuer` | Giữ (ở bước nạp cấu hình) |
| Generate before initialized (K:39) | cấu hình `undefined` → lỗi; ký trước khi khởi tạo → `Signing key not initialized` | Giữ |
| Missing kid (K:79), Key not found (K:101) | xác minh JWT không `kid` / `kid` lạ → lỗi | Giữ |
| Generate ID token / access token / refresh token (K:124, 140, 179) | xác minh lại được, có `jti`, `login_id` | Giữ |
| Generate access token with project-scoped issuer (K:158) | issuer theo project | Bỏ (mục 9) |
| Tokens include NotBefore claim (K:195) | `nbf` ≈ `iat` | Giữ |
| Generate secret (K:237) | độ dài hex | Giữ |
| Get signing key by algorithm (K:242) | chọn khóa theo `alg` | Sửa: chọn theo `AUTH_ACTIVE_KID` |
| Access / ID token with / without email (K:251, 271, 289, 307; `utils.test.ts:629-828`) | `email` chỉ khi scope có `email` và user là `User`; không có cho ClientApplication hay user không có email | Giữ; **Sửa** so khớp theo **từ scope** (không chuỗi con) cho chắc chắn, nếu chấp nhận lệch (Q15) |
| Get /.well-known/jwks.json (W:21) | `kid` UUID (Go: thumbprint), `use:"sig"`, `alg`, `kty`; không lộ tham số riêng | Giữ (đổi kiểm `kid`) |
| Get /.well-known/openid-configuration (W:65), oauth-authorization-server (W:108) | có các trường bắt buộc | Giữ (kiểm thêm nội dung chính xác ở mục 5.6) |
| Get project-scoped OpenID/SMART configuration (W:78, W:94), project-scoped protected resource (W:132) | URL theo project | Bỏ (mục 9) |
| Get /.well-known/oauth-protected-resource (W:121), custom request (W:145), invalid resource (W:153), resource array (W:159) | `400` `Invalid resource URL` với mảng/giá trị ngoài | Giữ |
| getClientRedirectUri (C:7), with deprecated redirectUri (C:27) | khớp chính xác; nhánh `allowPartial` | Sửa: bỏ các ca `allowPartial` (không port nhánh partial) |
| `fhirauth.Verifier` chấp nhận token auth phát (mới, e2e) | JWKS qua `httptest`, claim `fhir_project`, scope, policy | Mới |
| Xoay khóa: token ký bằng khóa cũ vẫn xác minh (mới) | | Mới |

### 7.10 `userinfo`, `introspect`, `logout`, `register`, `middleware`

| Ca | Mô tả → kỳ vọng | Xử lý |
|---|---|---|
| Get userinfo with user email / profile email / phone / address / only openid / Profile with empty resource / full resource (UI:30, 60, 106, 155, 198, 230, 256) | theo mục 5.2 | Giữ (cần `ProfileReader`, Q10) |
| Token introspection (I:65) | `active:true`, có `iss`, `sub` | Giữ |
| Token introspection rejects ID tokens (I:95) | `{active:false}` | Giữ |
| Introspection requires authentication (I:125) | `401` | Giữ |
| Token introspection on rotated refresh token (I:147) | token cũ `inactive`, token mới `active` | Giữ |
| Token introspection on revoked token (I:193) | sau logout `inactive` | Giữ |
| Token introspection rejects cross-Project token (I:229) | project khác hoặc client khác → `inactive` | Giữ |
| Token parameter required (I:301) | `400` | Giữ |
| Revoke: Unauthenticated (L:23) | `401` | Giữ |
| Revoke: Success (L:28) | `userinfo` 200 → logout 200 → `userinfo` 401 → logout 401 | Giữ |
| OAuth2 register: Missing redirect_uri (R:30), Unknown redirect_uri (R:37), Success (R:49) | `400`, `400`, `201` không `client_secret` | Giữ (client chuẩn lấy từ cấu hình) |
| Auth middleware: Login not found (M:32), Login revoked (M:48), Inactive project (M:78) | Bearer → `401` | Giữ |
| …No auth header (M:95), unrecognized header (M:107), unrecognized token type (M:112), Invalid bearer token (M:117) | `401` | Giữ |
| …ID token rejected as access token (M:231), Refresh token rejected as access token (M:255) | `401` | Giữ |
| …Basic auth * (M:123-229, M:270-375), "magic param" (M:101), "Bearer auth AuditEvent uses current request IP" (M:312) | Basic không port; AuditEvent ngoài P3 | Bỏ |
| OAuth Routes: Get token with client credentials (RO:24) | | Giữ |
| Logout: thu hồi chuỗi `basedOn` (mới) | | Mới (Q11) |
| `fhirauth.Verifier`: scope do client khai (`fhir.admin`) không bao giờ có trong token (mới) | | Mới |

Chưa đọc chi tiết các ca còn lại của `utils.test.ts` (validate login, MFA, membership): thuộc P2/P5; chỉ dùng tên. `external.test.ts` (729 dòng) chưa đọc: P6.

---

## 8. Kiểm soát bảo mật bắt buộc có test

Cột "Medplum" ghi điều đã đọc thấy; "echo-oauth2-server" theo `README.md` của nó; "Go" là yêu cầu cho `apps/auth`.

| # | Kiểm soát | Medplum làm gì (`file:dòng`) | echo-oauth2-server | Yêu cầu Go và test |
|---|---|---|---|---|
| 1 | **PKCE bắt buộc?** | Bắt buộc ở token trừ client `pkceOptional`; với client đó **không kiểm PKCE dù có challenge** (`token.ts:294-314`, test `T:770`). `authorize` không bắt buộc `code_challenge` (`authorize.ts:106`). Chấp nhận `plain` (`token.ts:1198`) nhưng SMART config chỉ khai `S256` (`fhir/smart.ts:110`) | Bắt buộc cho client công khai và mặc định cho mọi client; nới cho client bí mật bằng cờ; chỉ `S256` trừ khi `AllowPlainPKCE` | Luôn kiểm verifier nếu Login có challenge. Client công khai bắt buộc có challenge. `S256` mặc định. Test mục 7.3 (đảo ca T:770, thêm "pkceOptional phải xác minh") |
| 2 | **Code dùng một lần, nguyên tử** | Cờ `granted`; đọc rồi ghi `updateResource({...login, granted:true})` **không `ifMatch`** (`utils.ts:791-797`); hai request song song có thể cùng thành công | Chiếm code nguyên tử (`FOR UPDATE`), test đua 16 goroutine | `ClaimGrant` bằng `IfMatch` trước khi ký token. Test 16 goroutine: đúng 1 thành công |
| 3 | **Phát hiện dùng lại code → thu hồi** | Có: thu hồi Login, `invalid_grant` `Token already granted` (`token.ts:265-269`); thu hồi bằng ghi cả bản chụp (`utils.ts:845-850`) | Có, kèm thu hồi token đã cấp | Giữ hành vi; thu hồi bằng patch/CAS-retry. Test: token lần 1 `inactive` ở `introspect`. Với `fhir-server`: cửa sổ ≤ TTL access token (mục 4.4) |
| 4 | **Code: entropy, lưu trữ, hạn** | 16 byte hex, lưu rõ trong `Login.code`, tra bằng bằng nhau (`utils.ts:209`, `token.ts:237-246`). Hạn tính từ `meta.lastUpdated` (`token.ts:276-280`) nên bị kéo dài bởi mọi lần ghi Login | Không nêu chi tiết trong README (chưa xác nhận) | Lưu băm SHA-256 (như Medplum đã làm cho pre-authorized, `token.ts:773`); hạn từ mốc phát hành tường minh (Q6, Q7). Test: Login đọc ra không có code gốc; ghi Login không gia hạn code |
| 5 | **`redirect_uri` khớp chính xác** | Ở `authorize`: khớp chính xác (`clients.ts:60-75`) ✓. Ở `token`: **không kiểm**, không lưu vào Login (`utils.ts:200-219`) ✗ (RFC 6749 §4.1.3) | zitadel kiểm ở cả hai điểm (chưa xác nhận chi tiết) | Lưu `redirectUri` vào Login, so chính xác ở token. Test mục 7.3 (ca "redirect_uri khác") |
| 6 | **Thu hồi** | Login `revoked`; có hiệu lực ngay vì mỗi request tra Login (`utils.ts:1234-1243`); logout chỉ thu hồi một Login (`logout.ts:14`) | Thu hồi theo family và theo thiết bị | Giữ cho `userinfo`/`introspect`/`refresh`; với `fhir-server` là TTL (4.4). Logout thu hồi cả chuỗi `basedOn` (Q11). Test e2e |
| 7 | **Xoay refresh token, phát hiện dùng lại** | Có, CAS (`token.ts:490-523`), ân hạn 30 giây cùng IP (`token.ts:447-460`), dùng lại thì thu hồi (`token.ts:431-436`). Nhưng: không trần tuyệt đối của phiên (mỗi lần xoay cấp hạn đầy, `utils.ts:826-836`); chỉ nhớ **1** secret cũ | Family, xoay, chiếm đồng thời an toàn | Giữ thuật toán; ân hạn cấu hình/tắt được; `SESSION_MAX_AGE`; audit. Test 7.4 |
| 8 | **So sánh thời gian cố định, lưu secret** | `timingSafeEqual` nhưng thoát sớm khi khác độ dài (`utils.ts:984-993`); secret client lưu và so **dạng rõ** (`token.ts:1071-1087`); so PKCE bằng `===` (`token.ts:1198-1204`) | Secret băm bằng scrypt | So bằng `subtle.ConstantTimeCompare` trên SHA-256 của hai vế (độ dài cố định). Băm secret client là câu hỏi mở (đổi mô hình P1, Q16). PKCE so hằng thời gian. Test đơn vị cho `Equal` |
| 9 | **Xác thực client ở token** | Code grant: **không kiểm secret** khi PKCE qua (`token.ts:294-314`). Refresh: chỉ kiểm khi có header Basic (`token.ts:380-400`, test RT:341). Basic cắt secret ở `:` và decode `ascii` (`token.ts:1023-1026`) | `client_secret_basic/post`, JWT profile | Bộ phân tích Basic đúng RFC; yêu cầu xác thực theo cấu hình client (Q4). Test: secret chứa `:`, ký tự UTF-8 |
| 10 | **Thông báo lỗi / lộ sự tồn tại client** | `Invalid client` (không tồn tại) khác `Invalid secret` (sai secret) → liệt kê được id client (`token.ts:157`, `1088`); lỗi client là `400 invalid_request`, không `401 invalid_client` (RFC 6749 §5.2); chỉ một nhánh dùng `invalid_client` (`token.ts:758`) | Mã lỗi theo RFC qua zitadel (chưa xác nhận chi tiết) | Giữ như Medplum cho tương thích test/client (lệch RFC chấp nhận được, Q3); ghi nhận trong mục 9 |
| 11 | **Client assertion** | Kiểm `aud`, `iss=sub`, chữ ký, thuật toán; **không** bắt `exp`, không `jti` một lần, không trần thời hạn (`token.ts:945-1012`). `jwks_uri` tải qua `safeFetch` (HTTPS, chặn IP nội bộ) | `jti` dùng một lần, ghi sau khi xác minh chữ ký | Bắt buộc `exp` và `exp - iat ≤ 5 phút`; `jti` một lần qua `JTIStore` (ghi **sau** khi xác minh chữ ký); `safehttp`. Test 7.5 |
| 12 | **Scope do client khai** | Lưu nguyên, không so với quyền (`token.ts:183`, `utils.ts:212`); Medplum thu hẹp quyền bằng access policy mỗi request | `invalid_scope` cho scope client không được dùng (`client_credentials`) | Cấm `fhir.*` do client khai; giao với quyền membership; thêm scope vai trò do auth cấp (4.3). Test: không bao giờ có `fhir.admin` từ request |
| 13 | **Tách loại token** | ID token `aud=client`, access/refresh `aud=issuer`, refresh có `refresh_secret` (`utils.ts:1228-1232`, `introspect.ts:36`) | Qua zitadel | Thêm `token_use`; refresh có `aud` riêng. Test chéo loại token |
| 14 | **Cookie phiên** | `HttpOnly`, `Secure`, `SameSite=None`, 1 giờ; giá trị lưu rõ (`auth/utils.ts:548-558`, `utils.ts:209`) | Cookie ký HMAC, CSRF gắn với phiên (ví dụ `examples/webapp`) | Lưu băm; đặt thuộc tính; xoá khi logout. Ngoài ra `authorize` đặt `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` |
| 15 | **`state`, `iss` trong redirect** | `state` trả nguyên (nối cả khi rỗng); **không** có `iss` (`authorize.ts:85`, `155`, `319-323`) | Thêm `iss` (RFC 9207) | Thêm `iss`; test AZ mới |
| 16 | **`max_age`** | `parseInt` có thể ra `NaN` làm bỏ kiểm (`authorize.ts:222`) | Qua zitadel | Validate số nguyên không âm. Test mới |
| 17 | **PKCE kế thừa khi tạo Login mới** | `codeChallenge ?? existingLogin.codeChallenge` (`authorize.ts:141-142`) | Không áp dụng | Không kế thừa. Test AZ:502 đổi kỳ vọng |
| 18 | **IP và proxy** | `trust proxy`=1 (`app.ts:211`); refresh không kiểm IP (`token.ts` thiếu `checkIpAccessRules`) | Không áp dụng | `AUTH_TRUSTED_PROXIES`; kiểm IP ở refresh. Test |
| 19 | **Giới hạn tốc độ `/token`** | Giới hạn ở tầng app cho `/auth/`, `/oauth2/` (`ratelimit.ts:60`) | Khuyến nghị rate-limit `/token` và trang đăng nhập (mỗi lần kiểm secret tốn một lần scrypt) | `RateLimiter` trên `/oauth2/token` và `/oauth2/authorize` |
| 20 | **Audit** | Log cảnh báo khi dùng lại refresh (`token.ts:432-435`); AuditEvent đăng nhập ở `setLoginMembership` (`utils.ts:586-594`) | Bảng audit (`oauth_audit_events`) chỉ ghi định danh, IP mã hoá | `AuditLog`: ghi dùng lại code/refresh, ân hạn, từ chối PKCE, assertion lặp; **không** ghi secret/token |
| 21 | **Không lộ `passwordHash`, `Login.code`, `refreshSecret`, `JsonWebKey.d`** | Medplum chỉ cho super admin đọc chúng qua FHIR API (theo `apps-auth.md` D2) | Không áp dụng | Kho auth tách khỏi kho lâm sàng; test chặn ở P1; khóa riêng không vào `fhirstore` |

Ở echo-oauth2-server có thêm `X-Device-Id` (theo thiết bị): không port.

---

## 9. Phần Medplum có nhưng KHÔNG port đợt này, điểm lệch chấp nhận được, rủi ro, câu hỏi mở

### 9.1 Không port (kèm lý do)

| Hạng mục | Lý do |
|---|---|
| URL theo project `/projects/{id}/oauth2/*`, `/projects/{id}/.well-known/*`, issuer theo project (`util/url.ts:60-68`, `wellknown.ts:21-24`) | `fhir-server` cấu hình **một** `Issuer`; issuer theo project phá việc khớp `iss`. Nhiều project vẫn hoạt động nhờ claim `fhir_project` (D5). |
| `tls_client_auth` / mTLS (`token.ts:1035-1064`, `cert.ts`) | Tin header chứng chỉ do proxy chuyền (`x-mtls-cert`) chỉ an toàn khi chắc chắn không ai gọi thẳng; cần quyết định hạ tầng. Mặc định Medplum cũng tắt (`mtlsCertHeader` chỉ bật trong test, `config/loader.ts:240`). |
| Basic auth trực tiếp vào API (`getLoginForBasicAuth`, `utils.ts:1261-1313`) | `fhir-server` không có đường này. |
| FHIRcast (`hub.url`, `hub.topic`, `token.ts:1142-1153`) | Ngoài phạm vi của P3 ban đầu; **đã làm ở WS-9 / FC-6**. |
| `/.well-known/api-catalog` (`api-catalog.ts`) | Liệt kê DICOMweb, SCIM... không có ở Trivita. |
| `on-behalf-of` (`utils.ts:1369+`, `x-medplum-on-behalf-of`) | Tính năng API FHIR, không thuộc auth. |
| Đăng nhập ngoài, `token-exchange`, `external`, `google` (`token.ts:532-733`, `utils.ts:1030-1160`, `1439-1650`) | P6. |
| Client chuẩn dựng sẵn `medplum-cli`, MCP (`clients.ts:17-35`) | Cấu hình tuỳ chọn, mặc định rỗng. |
| `super admin` đầy đủ (không refresh, chặn SMART) | Q8. |
| `/oauth2/register` kiểu "đăng ký động" thật | Medplum cũng không làm (chỉ tra client chuẩn); port kèm cấu hình. |
| Nhánh `allowPartial` của `getClientRedirectUri` (`clients.ts:69-72`, chỉ dùng ở `auth/external.ts:175`) | Không an toàn, comment của chính Medplum khuyên bỏ. |

### 9.2 Điểm lệch chấp nhận được (so với Medplum, ghi để reviewer biết)

1. Claim bổ sung `fhir_project`, `fhir_access_policy`, `token_use`, và scope vai trò `fhir.*` do auth thêm (mục 4.3); access token **stateless với `fhir-server`** (mục 4.4).
2. Discovery khai đúng cái làm được (mục 5.6), `smart_style_url` trỏ vào auth.
3. `invalid_scope` thay cho OperationOutcome ở `client_credentials` với scope `patient/` (`token.ts:186`).
4. `S256` mặc định, `plain` tắt (`AUTH_ALLOW_PLAIN_PKCE`); PKCE luôn xác minh khi có challenge.
5. Mã lỗi giữ kiểu Medplum (`invalid_request` 400 cho lỗi client; `Unsupported grant_type` thay `unsupported_grant_type`; `Invalid secret` khác `Invalid client`) để khớp test và SDK client; lệch RFC 6749 §5.2, ghi vào hợp đồng.
6. Khóa ký ở PEM/secret thay vì resource `JsonWebKey` trong DB; `kid` là thumbprint; chọn khóa ký tường minh.
7. Mốc hết hạn code tường minh thay `meta.lastUpdated`.

### 9.3 Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Client tự khai scope `fhir.*` thành quyền admin ở `fhir-server` | Lọc scope (4.3), test bắt buộc |
| Claim `fhir_access_policy` không biểu diễn hết AccessPolicy của Medplum (compartment, hiddenFields, tham số) | Fail-closed; thống kê tính năng đang dùng (Q9) |
| Thu hồi không có hiệu lực tức thì ở `fhir-server` | TTL ngắn, `introspect` khi cần (Q2) |
| `Login.code` và `refreshSecret` nằm trong kho FHIR | Kho riêng (D2), băm code/cookie, test chặn lộ (P1) |
| IP giả qua `X-Forwarded-For` làm lệch ân hạn refresh và kiểm IP | `AUTH_TRUSTED_PROXIES`, test |
| Phụ thuộc giao diện đăng nhập chưa có | Q1 trước B6 |
| `patient/` scope bị `fhirauth` bỏ qua | Không quảng bá năng lực bệnh nhân trong discovery (5.6) |
| Phiên bản Medplum đổi hành vi | Ghi phiên bản đã port vào `docs/UPSTREAM.md` (kế hoạch tổng) |
| Port test dùng `plain` làm hỏng nhiều ca | Đổi sang helper S256 ngay từ B0 |

### 9.4 Câu hỏi mở

| # | Câu hỏi | Khuyến nghị |
|---|---|---|
| Q1 | Ai xây giao diện đăng nhập/chọn profile/consent mà `/oauth2/authorize` chuyển hướng tới (Medplum dùng ứng dụng web riêng, `authorize.ts:332-338`)? | Dùng chung cách chia của Medplum; P3 chỉ cần URL cấu hình và trang giả cho e2e |
| Q2 | Chấp nhận cửa sổ vô hiệu hoá bằng TTL access token ở `fhir-server`, hay thêm introspect/danh sách thu hồi? | Chấp nhận ở P3, TTL production 15 phút; đánh giá lại sau |
| Q3 | Mã lỗi: giữ kiểu Medplum hay theo RFC 6749 (`invalid_client` 401, `unsupported_grant_type`)? | Giữ Medplum ở P3; thêm cờ chuyển RFC sau nếu có client đòi |
| Q4 | Có bắt buộc xác thực client khi đổi code/refresh với client có `secret`? Nhiều SPA Medplum dùng client có secret nhưng như client công khai | Thêm cờ `confidential` hoặc suy từ `pkceOptional`/`secret`; mặc định yêu cầu khi request **có** gửi credential, và khi client được đánh dấu bí mật; chốt trước B3. Đảo ca T:770 cần đồng ý này. `medplum-cli` (client `pkceOptional`, `clients.ts:20-26`) có gửi PKCE hay không: chưa xác nhận |
| Q5 | Client có nhiều `ProjectMembership` (tests dùng client chia sẻ, `introspect.test.ts:229-234`): chọn membership nào khi bỏ URL theo project? | Ưu tiên membership của `client.meta.project`; không duy nhất thì `invalid_request` `Invalid client` |
| Q6 | Lưu `Login.code` và `cookie` dạng băm? Đổi mô hình P2 | Có, vì D2 coi kho có thể bị đọc |
| Q7 | Mốc hết hạn code: dùng `Login.expiresAt` (đã có ở `pkg/fhir`, Medplum chỉ dùng cho pre-authorized, `token.ts:794`) hay thêm trường mới | Dùng `expiresAt` cho cả hai nếu P2 xác nhận |
| Q8 | Hỗ trợ `Project.superAdmin` (`fhir.super`, `AllowSuperAdmin`)? | Hoãn, mặc định không phát `fhir.super` |
| Q9 | Trong dữ liệu Trivita, AccessPolicy có dùng compartment/hiddenFields/tham số `%profile` không? | Khảo sát trước B2 |
| Q10 | `ProfileReader`: đọc chung DB (chỉ đọc) hay gọi HTTP `fhir-server`? | Đọc chung DB nếu hạ tầng cho phép; ngược lại HTTP với token dịch vụ |
| Q11 | Logout có thu hồi chuỗi `basedOn`? `fhirstore` có search param `based-on` cho Login? | Có nếu tra được; chưa xác nhận |
| Q12 | Có cần client chuẩn `medplum-cli` và `register` không? | Không ở P3 |
| Q13 | `resource` của `oauth-protected-resource` và `aud` hợp lệ của `authorize` là URL auth hay URL `fhir-server` (SMART `aud` là URL FHIR, `authorize.ts:176-191`)? | URL `fhir-server`, cấu hình `AUTH_FHIR_BASE_URL` |
| Q14 | Verifier PKCE sai có tiêu thụ code (RFC 9700 khuyến nghị coi đó là một lần thử)? Medplum không tiêu thụ (không ghi gì trước khi kiểm, `token.ts:294-309`) | Không tiêu thụ nhưng đếm số lần thử và thu hồi sau N lần (đề xuất N=5) |
| Q15 | So khớp `email` trong scope theo từ hay chuỗi con (Medplum dùng chuỗi con, `utils.ts:804`, `821`)? | Theo từ |
| Q16 | Băm `ClientApplication.secret`? Ảnh hưởng quản trị (hiển thị secret) và `retiringSecret` | Hoãn; ghi nhận là yếu hơn echo-oauth2-server |
| Q17 | `Login.remoteAddress` bị ghi đè mỗi lần xoay (`token.ts:502-504`), nên mất IP đăng nhập gốc. Có cần trường riêng cho IP đăng nhập? | Giữ hành vi Medplum (ân hạn cần nó) |

---

## 10. Ước lượng và thứ tự làm

Cỡ chung của pha: **L** (khớp `apps-auth.md`; ước lượng thô 8 đến 12 ngày cho một người). Mỗi bước dưới đây là một PR có thể review độc lập; phụ thuộc P0, P1 (kho `Login`, `ClientApplication`, `ProjectMembership`, `Project`, `User`, `SmartAppLaunch`) và phần tạo Login của P2 (`/auth/login`) cho các bước từ B3. Trước P2 xong, dùng fixture Login dựng trực tiếp.

| Bước | Cỡ | Nội dung | Tiêu chí hoàn thành |
|---|---|---|---|
| B0 | S | `domain/oauth`: PKCE, scope (`isSmartScopeSubset`...), redirect URI chính xác, `Error`, parse thời hạn `\d+[smhdwy]`, so hằng thời gian, sinh secret; luật kiến trúc mới vào `architecture_test.go`; helper test S256 | Test đơn vị xanh (gồm `isSmartScopeSubset` port từ `utils.test.ts:835-850`); `go test ./...` xanh; kiến trúc test chặn `domain` import hạ tầng |
| B1 | M | `KeyStore` (PEM), ký/xác minh JWT, `GET /.well-known/jwks.json`, `openid-configuration`, `oauth-authorization-server`, `smart-configuration`, `smart-styles.json`, `oauth-protected-resource`; lệnh `auth keys generate` | Test `fhirauth.Verifier` (cấu hình bằng JWKS của `httptest.Server` của auth) xác minh token mẫu; test không lộ tham số riêng; test xoay khóa; JSON well-known khớp mục 5.6 |
| B2 | M | `TokenIssuer` (claim, `fhir_project`, scope vai trò, lọc scope, claim policy fail-closed), `ClientAuthenticator` (Basic đúng RFC, body), `POST /oauth2/token` khung và `client_credentials`, Bearer middleware, bộ lỗi OAuth | Ca 7.1, 7.2 xanh (trừ super admin, project-scoped); test "không bao giờ có `fhir.admin` từ request"; token `client_credentials` qua `fhirauth.Verifier` ra `Principal` đúng role/project |
| B3 | M | `authorization_code`: chiếm code nguyên tử, PKCE (luôn xác minh), `redirect_uri`, hạn code, `SmartAppLaunch` (`patient`/`encounter`/`fhirContext`), phát hiện dùng lại | Ca 7.3 xanh gồm 16 goroutine đua, ca "mới"; xác nhận q4/q6/q7 đã chốt |
| B4 | M | `refresh_token`: xoay CAS bằng `IfMatch`, ân hạn, dùng lại → thu hồi, kiểm IP, trần phiên, audit | Ca 7.4 xanh gồm đua 16 goroutine, ân hạn, đảo "Known gaps"; `go test -race` |
| B5 | S–M | `userinfo`, `introspect`, `logout` (patch/CAS-retry, thu hồi chuỗi nếu Q11), `ProfileReader` | Ca 7.10 xanh; `introspect` báo `inactive` ngay sau thu hồi dù access token chưa hết hạn |
| B6 | M | `GET/POST /oauth2/authorize`: 14 bước, cookie phiên, `id_token_hint`, `prompt`, `max_age` hợp lệ, Login mới, `iss` | Ca 7.8 xanh; cần Q1 chốt (trang giả cho e2e) |
| B7 | S–M | `private_key_jwt` (`safehttp`, JWKS cache, thử nhiều khóa, `exp`, `jti`), `pre-authorized_code`, `register` (client chuẩn từ cấu hình) | Ca 7.5, 7.6 và `register` xanh; test SSRF (`localhost`, IP riêng, HTTP) |
| B8 | S | Tích hợp `fhir-server` và tài liệu: cấu hình `FHIR_JWT_ISSUER = AUTH_ISSUER`, `FHIR_JWT_AUDIENCE = <aud>`, `FHIR_JWKS_URL = <base>/.well-known/jwks.json`, `FHIR_JOB_SIGNING_KEY` (bắt buộc khi dùng JWKS, `apps/fhir-server/internal/config/config.go:291`), staging/production phải `https://` (`config.go:307`); e2e; sửa `docs/api-contract/oauth.md` (JSON không được chấp nhận ở `/oauth2/token`, thêm claim, trạng thái "đã cài") | E2E: đăng nhập (P2) → `code` → `/oauth2/token` → gọi `GET /fhir/R4/Patient` của `fhir-server` bằng token đó: `200` đúng project, `403` khi thiếu scope, `401` khi sai `iss`/`aud`/hết hạn; `docker-compose` có hai tiến trình chạy được; hợp đồng cập nhật |
| B9 | S | Rà soát bảo mật (mục 8), swagger khớp, chạy bộ OpenID conformance tuỳ chọn (cần Docker, như `oauth/README.md` mô tả, chưa xác nhận khả năng chạy ở môi trường Trivita), SonarQube | Mọi dòng mục 8 có test tương ứng; không còn mục "Sửa" chưa xử lý; coverage theo chuẩn repo |

**E2E bước B8, cách làm:** `apps/` không import `internal` của nhau, nên test tích hợp đặt trong `apps/auth/internal/bootstrap` dùng `pkg/fhirauth.Verifier` (gói công khai) trỏ vào `httptest.Server` của auth để chứng minh tương thích; thêm một smoke test hai tiến trình (compose) cho `fhir-server` thật. Cấu hình `fhir-server` mẫu cho dev: bật `FHIR_JWT_ISSUER`, **không** dùng đồng thời token tĩnh hay `FHIR_AUTH_DISABLED` (`config.go:301`).

**Thứ tự:** B0 → B1 → B2 → B3 → B4 → B5 → B6 → B7 → B8 → B9. B1 có thể song song với B0 sau khi chốt cổng. B6 chặn bởi Q1; B3/B4 chặn bởi Q4, Q6, Q7. Mốc dùng được tối thiểu (`apps-auth.md` mục 5): hết B5 cộng B8 cho `client_credentials` và `authorization_code` bằng fixture; hết B8 là mốc "dùng được với `fhir-server`".


---

## 11. Tiến độ cài đặt (cập nhật 2026-11-21)

| Bước | Trạng thái | Ghi chú |
|---|---|---|
| B0 | Một phần | Đã có: `domain.OAuthError`, `ParseTokenDuration`, `MatchClientSecret` và `EqualSecret` (so SHA-256 hằng thời gian), `ValidateRequestedScope`, `RoleScopes`, `PolicyClaim`. Chưa có PKCE (làm ở B3). Luật kiến trúc: `application` được import `pkg/fhirauth` (để kiểm claim bằng đúng bộ phân tích của fhir-server) |
| B1 | Xong (trừ `authorization_endpoint`) | `infrastructure/keys` (thư mục PEM, ký, xác minh, JWKS, xoay khóa tường minh bằng `AUTH_ACTIVE_KID`), `auth keys generate\|list`, `/.well-known/{jwks.json, openid-configuration, oauth-authorization-server, smart-configuration, smart-styles.json, oauth-protected-resource}`. Discovery CHỈ khai thứ đã cài (grant `client_credentials`, `client_secret_basic\|post`); khai thêm khi B3 đến B7 xong. `ETag` và `Cache-Control: public, max-age=300` cho JWKS |
| B2 | Xong | `POST /oauth2/token` với `client_credentials`; xác thực client Basic đúng RFC 6749 §2.3.1 (tách ở `:` đầu tiên, UTF-8, URL-decode) hoặc body; lỗi theo Medplum (Q3); cấm `fhir.*` trong scope client khai; scope vai trò do auth thêm; claim `fhir_project`, `fhir_access_policy`, `token_use`, `login_id`; **Login chỉ được tạo sau khi qua kiểm IP và chính sách** (Medplum tạo trước); membership của client theo D-17; `Authenticator` Bearer có trạng thái (kiểm Login chưa thu hồi, membership và project còn hoạt động, `token_use=access`, `iss`, `aud`) nên `/auth/me` và `/auth/revoke` đã dùng được |
| B3 | Xong | `authorization_code`: code lưu băm (D-15, đã trả nợ), hạn tính từ lúc phát (`Login.expiresAt`), PKCE luôn kiểm khi Login có challenge (kể cả client `pkceOptional`), chỉ S256 (plain khi `AUTH_ALLOW_PLAIN_PKCE`), `redirect_uri` lưu lúc đăng nhập phải khớp chính xác, **chiếm code nguyên tử** (cập nhật có điều kiện theo phiên bản; test 16 yêu cầu song song cho đúng 1 token, có đột biến), dùng lại code thì thu hồi Login và log cảnh báo, ngữ cảnh SMART launch (`patient`, `encounter`, `fhirContext`). Chi tiết ở dưới |
| B4 | Xong | `refresh_token`: xoay secret bằng cập nhật có điều kiện theo phiên bản (16 yêu cầu song song: đúng một lần xoay, tất cả đều nhận token), ân hạn cùng IP cấu hình được, dùng lại thì thu hồi Login, kiểm `ipAccessRule`, trần phiên. Chi tiết ở dưới |
| B5 | Xong | `GET\|POST /oauth2/userinfo`, `POST /oauth2/introspect`, `GET\|POST /oauth2/logout` (Bearer). Chi tiết ở dưới |
| B6 | Xong | `GET\|POST /oauth2/authorize`: 14 bước, cookie phiên, `id_token_hint`, `prompt`, `max_age`, Login mới, `iss`, thu hồi chuỗi `basedOn` ở logout. Chi tiết ở dưới |
| B7 | Xong | `private_key_jwt` cho cả ba grant, `pre-authorized_code`, `POST /oauth2/register`; tách `pkg/safehttp` từ `pkg/fhirsub/ssrf.go`. Chi tiết ở dưới |
| B8 | Xong | test `e2e/` chạy hai binary thật (`make test-e2e`); `docs/api-contract/oauth.md` viết lại theo bản cài; `docker-compose.auth.yml` (chưa chạy thử bằng Docker). Chi tiết ở dưới |
| B9 | Xong (2026-11-21) | Rà soát bảo mật mục 8 (21/21 dòng có test), swagger khớp route, test trên PostgreSQL thật, chạy thử compose bằng Docker. Chi tiết ở dưới |

Điểm lệch so với bản nháp (đã quyết khi cài):
1. **`pkg/fhirauth.Policy` được mở rộng** (rule `*`, `deny`) thay vì từ chối mọi chính sách có `*`; xem kết quả khảo sát D-16 ở [README.md](README.md) mục 5.2. Đổi này nằm ở `fhir-server`, có test riêng (`pkg/fhirauth/snapshot_test.go`).
2. Thời hạn mặc định access token **15 phút** (D-13), không phải 1 giờ như Medplum; cấu hình `AUTH_ACCESS_TTL`, `AUTH_REFRESH_TTL`, `AUTH_ID_TTL` theo dạng Go (`15m`, `336h`), không dùng `\d+[smhdwy]` ở cấp cấu hình (client vẫn dùng `accessTokenLifetime` dạng đó).
3. Khóa ký dev: không đặt `AUTH_SIGNING_KEY_PATH` ở local/development/test thì dùng khóa ES256 tạm trong bộ nhớ (có cảnh báo); staging/production bắt buộc có thư mục khóa.
4. **D-15 đã trả (B3)**: `Login.code` và `Login.cookie` giờ chứa SHA-256 hex của giá trị phát ra (dùng luôn search param `code`, không thêm `codeHash`). Code và cookie được phát ở bước dựng kết quả đăng nhập (`issueAuthCode`), không phải lúc tạo Login; mỗi lần dựng lại kết quả (kể cả poll `GET /auth/login/{id}`) phát code MỚI và code cũ mất hiệu lực. `LoginResult.Cookie` mang cookie rõ ra lớp presentation (không vào JSON).
5. `smart_style_url` trỏ vào auth (`<base>/.well-known/smart-styles.json`), không phải `fhir/R4/.well-known/...`.

### B3: quyết định khi cài và điểm lệch so với Medplum

| Chủ đề | Quyết định |
|---|---|
| Xác thực client ở bước đổi code (D-14, Q4) | Client **gửi** secret thì secret phải đúng (Medplum bỏ qua secret ở nhánh PKCE). Client không gửi secret thì chứng minh bằng PKCE. Login **không có** challenge chỉ đổi được khi client xác thực bằng secret, nếu không: `Missing verification context`. Chưa có cờ "client bí mật" riêng nên một client có secret vẫn được đổi code chỉ bằng PKCE; nếu cần ép, thêm cờ trên ClientApplication |
| `pkceOptional` | Không còn tác dụng ở `/oauth2/token`: có challenge thì luôn phải có verifier đúng (đảo test T:770). Hệ quả: client dựng sẵn `medplum-cli` (không secret) phải gửi PKCE |
| Verifier sai, secret sai | **Không tiêu thụ code** (Q14): kẻ lộ code không đốt được code của người dùng thật. Chưa đếm số lần thử; chỉ có hạn mức theo IP trên `/oauth2/token`. Verifier phải đúng RFC 7636 (43 đến 128 ký tự unreserved) |
| Hạn code | `Login.expiresAt` = lúc phát + `AUTH_AUTH_CODE_TTL` (mặc định 10 phút); thiếu `expiresAt` thì coi là hết hạn (fail đóng) |
| Thứ tự kiểm | code lạ, client không khớp, thiếu membership, `revoked` (trước `granted`), `granted` (thu hồi Login), hết hạn, client hoạt động, secret, PKCE, `redirect_uri`, membership/project còn hoạt động, chính sách; **chiếm code ở cuối** để lỗi cấu hình không đốt code |
| Chiếm code | `UpdateIfMatch` đặt `granted=true`; xung đột thì đọc lại: thấy `granted` là dùng lại (thu hồi + `Token already granted`), chưa thì thử lại tối đa 5 lần. Test song song dùng rào chắn để bắt buộc 16 yêu cầu cùng qua bước đọc rồi mới cùng chiếm (nếu không SQLite gần như tuần tự hoá và không bắt được lỗi) |
| `redirect_uri` | Lưu trong extension `https://trivita.ai/fhir/StructureDefinition/login-redirect-uri` của Login (resource `Login` không có trường riêng). Đã lưu thì token phải gửi lại đúng từng ký tự: thiếu: `invalid_request`/`Missing redirect_uri`; khác: `invalid_grant`/`Invalid redirect_uri` |
| Client trạng thái | Client có `status` khác `active` bị từ chối khi đổi code (Medplum chỉ kiểm ở `client_credentials`) |
| Chưa làm | `AuditLog` riêng (hiện chỉ slog cảnh báo); kiểm IP khi đổi code (đã kiểm lúc đăng nhập); `invalid_grant` khi membership bị tắt giữa chừng trả `access_denied`; test PostgreSQL cho B3 (`make test-auth-pg` chưa chạy cho test mới) |

### B4: quyết định khi cài và điểm lệch so với Medplum

| Chủ đề | Quyết định |
|---|---|
| Loại token | Chỉ nhận `token_use=refresh` có `aud` = `<issuer>/oauth2/token`, ký bằng khóa của auth. Access token, id token, `alg: none`, HS256, payload sửa, `iss` lạ: `invalid_request`/`Invalid refresh token` |
| Login không tồn tại | `invalid_grant`/`Invalid refresh token` (Medplum để handler ném lỗi) |
| Xác thực client (đảo "Known gaps" RT:341) | **Client bí mật = client có secret và Login không đi qua PKCE**: bắt buộc xác thực (Basic hoặc body), thiếu thì `invalid_grant`/`Incorrect client secret`. Client công khai (Login có PKCE) không có secret để gửi nên refresh token là bằng chứng. Credential đã gửi thì luôn phải đúng (`Invalid secret`), `client_id` lạ: `Incorrect client`. Không dùng được "có secret thì phải gửi" vì client mặc định nào cũng có secret mà SPA không giữ nó |
| Xoay | `UpdateIfMatch` đặt secret mới, `previousRefreshSecret`, `refreshSecretRotatedAt`, `remoteAddress`, `userAgent`. Xung đột thì đọc lại và đánh giá lại (secret trình lên giờ là secret trước thì vào ân hạn). Mỗi lần xoay cấp lại hạn đầy cho refresh token; trần tuyệt đối là `AUTH_SESSION_MAX_AGE` (mặc định 0 = không trần) tính từ `authTime`: `invalid_grant`/`Session expired` |
| Ân hạn | `AUTH_REFRESH_GRACE` (mặc định 30s, 0 là tắt): chấp nhận lại secret TRƯỚC trong cửa sổ, chỉ từ đúng IP đã xoay, không xoay thêm, token trả về mang secret hiện hành. IP khác hoặc quá cửa sổ là dùng lại: thu hồi Login, log cảnh báo. IP lấy theo `AUTH_TRUSTED_PROXIES` (không khai thì không tin `X-Forwarded-For`) |
| `ipAccessRule` | Được kiểm; IP bị chặn không được ghi vào Login và không xoay secret (đảo RT:353) |
| Thứ tự | mọi kiểm tra không ghi (token, Login, membership/project, client, trần phiên, IP, chính sách) trước; **xoay là bước ghi duy nhất, ở cuối** |
| Chưa làm | `AuditLog` riêng (hiện slog); kiểm test PostgreSQL; `scope` trong request refresh vẫn bị bỏ qua (không thu hẹp scope) như Medplum; propagation thu hồi sang `fhir-server` vẫn theo TTL access token |

### B5: quyết định khi cài và điểm lệch so với Medplum

| Chủ đề | Quyết định |
|---|---|
| `userinfo` | Scope khớp theo TỪ trên scope của Login (access token Medplum dùng chuỗi con). Chỉ `openid`: chỉ `sub`. `profile`: `profile`, `locale`, `updated_at`, `name`, `given_name`, `family_name` (lấy từ `User`). `email`: `email`, `email_verified`. **Không có** `phone_number`, `address`, `birthdate`, `gender`, `picture`, `nickname`, `website`, `zoneinfo`: hồ sơ Patient/Practitioner nằm ở kho lâm sàng của `fhir-server` (D-10, Q10 chưa chốt); cần `ProfileReader` nếu muốn có. Login của client: `sub` = id client, không có email/tên |
| `introspect` | Bearer bắt buộc; `token` từ form hoặc JSON, rỗng thì `400` văn bản `Token to introspect must be provided`. Mọi lý do token không dùng được ra đúng `{"active": false}` (chữ ký, hết hạn, id token, Login chưa cấp hoặc đã thu hồi, **membership hoặc project ngừng hoạt động** (Medplum không kiểm), refresh token đã xoay, khác project hoặc khác client với người gọi). Có cả refresh token (hoạt động khi bằng secret hiện hành). Trường: `active, iat, exp, iss, sub, client_id, scope, patient`. Chưa có siêu quản trị xuyên project (Q8) |
| `logout` | Thu hồi Login của access token bằng patch/CAS-retry (`RevokeLogin`), xóa cookie `medplum-<clientId>` (`Max-Age` âm, cùng thuộc tính lúc đặt; Medplum đặt `maxAge` 1 giờ). `200` OperationOutcome `All OK`. Sau đó access token và refresh token của Login đó bị từ chối ở các route của auth |
| Q11 (thu hồi chuỗi `basedOn`) | **Hoãn sang B6**: `Login` không có search param `based-on` và chỉ B6 (`/oauth2/authorize`) mới tạo Login có `basedOn`. Hiện logout chỉ thu hồi một Login |
| Chưa làm | Discovery khai `userinfo_endpoint` và `introspection_endpoint` (đã làm) nhưng chưa có `registration_endpoint`; `ProfileReader` (Q10); AuditLog |

### B6: quyết định khi cài và điểm lệch so với Medplum

| Chủ đề | Quyết định |
|---|---|
| D-12 (trang đăng nhập) | Giữ cách chia của Medplum: `auth` không có giao diện. Không có phiên thì 302 sang `<AUTH_APP_BASE_URL>/oauth` kèm tham số (danh sách trắng: `response_type, client_id, redirect_uri, scope, state, nonce, code_challenge(_method), aud, launch, prompt, max_age`, và `login` khi có Login mới; **không** chuyển `id_token_hint` vào URL). Trang đó gọi `/auth/login`, `/auth/profile`, `/auth/scope` rồi chuyển về `redirect_uri` kèm code. Mặc định `ENVIRONMENT=local` là `http://localhost:3000`. **Không đặt `AUTH_APP_BASE_URL` thì KHÔNG có `/oauth2/authorize`** (404, không khai `authorization_endpoint`). Test dùng chuyển hướng không theo, không cần trang giả |
| Lỗi hiển thị trực tiếp (400 văn bản) | client không có hoặc bị tắt, `redirect_uri` thiếu, không parse được, hoặc không bằng CHÍNH XÁC redirect URI đã đăng ký (không khớp tiền tố, không thêm query, phân biệt hoa thường) |
| Lỗi chuyển về `redirect_uri` | kèm `error`, `error_description`, `state` và **`iss`** (RFC 9207, Medplum không có `iss`): `invalid_request` (scope, aud, method, launch, `max_age`), `unsupported_response_type`, `request_not_supported`, `login_required`, `access_denied` |
| Kiểm thêm so với Medplum | `code_challenge_method` phải là `S256` (`plain` khi bật); client **không có secret** bắt buộc có `code_challenge`; `max_age` sai (không phải số nguyên không âm) là `invalid_request`, không bị bỏ qua kiểm tuổi phiên như Medplum (`parseInt` ra NaN); `aud` hợp lệ khi cùng scheme và host với auth hoặc `AUTH_FHIR_BASE_URL` (Q13) |
| Phiên sẵn có | Ưu tiên `id_token_hint` (phải là id token `token_use=id` do auth ký, `aud` = client này; refresh và access token bị từ chối), rồi cookie `medplum-<client_id>` tra bằng băm. Phiên phải: Login đã cấp token (`granted`), chưa thu hồi, **thuộc đúng client**, trong `max_age` (mặc định 3600s) tính từ `authTime`, membership và project còn hoạt động. Cookie của client A đặt dưới tên cookie client B không có tác dụng |
| Login mới từ phiên | `basedOn` = Login cũ (Login cũ không bị đụng), sao `client, project, profileType, user, membership, authMethod, authTime, mfaVerified, pictureUrl`, `scope` của request, `nonce` của request, `refreshSecret` mới nếu Login cũ có, `launch` của request hoặc kế thừa, `remoteAddress/userAgent` của request, lưu `redirect_uri`. **Không kế thừa `codeChallenge`** (Medplum kế thừa). `ipAccessRule` của membership được kiểm với IP hiện tại (`access_denied`). `prompt=none`: trả `code` ngay (kèm `state`, `iss`); khác: sang trang ứng dụng kèm `login=<id mới>` để xác nhận scope; `prompt=login`: bỏ qua phiên |
| Q11 (thu hồi chuỗi `basedOn`) | **Đã làm một nửa**: logout thu hồi Login và mọi Login TỔ TIÊN theo `basedOn` (tối đa 16 bậc), nên đăng xuất ở ứng dụng nào cũng chấm dứt phiên trình duyệt giữ cookie. Các Login CON của tổ tiên (ứng dụng khác dùng chung phiên) chưa bị thu hồi vì Login không có search param `based-on` để liệt kê |
| Chưa làm | Màn chọn scope và `showScopeSelection` (phía ứng dụng đăng nhập); `AuditLog` riêng; `register`, `private_key_jwt`, `pre-authorized_code` (B7); `fhir-server` biết thu hồi theo TTL |

### B7: quyết định khi cài và điểm lệch so với Medplum

| Chủ đề | Quyết định |
|---|---|
| `pkg/safehttp` (mới) | Tách từ `pkg/fhirsub/ssrf.go` (`fhirsub` giờ dùng lại, hành vi webhook không đổi): `Blocked(ip)`, `CheckURL(raw, httpsOnly)`, `NewClient(timeout, allowPrivate)`. Chặn ở bước KẾT NỐI trên IP đã phân giải (chống DNS rebinding), không proxy, không theo redirect. **Mở rộng dải chặn** so với bản cũ: `198.18.0.0/15`, `192.0.0.0/24`, `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `240.0.0.0/4`, NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`), 6to4 (`2002::/16`), `100::/64`, `2001:db8::/32` |
| `private_key_jwt` | Dùng được ở `client_credentials`, `authorization_code`, `refresh_token`; xác thực bằng assertion được tính là client đã xác thực (không cần `secret`; Medplum bắt client vẫn phải có `secret`). Thuật toán `RS256/384/512`, `ES256/384/512` (HS*, `none` bị từ chối); `aud` là chuỗi bằng đúng `<base>/oauth2/token`; `iss` = `sub` = client. **Bổ sung so với Medplum**: bắt buộc `exp` và `exp` không quá 5 phút kể từ lúc nhận; bắt buộc `jti` dùng một lần, chỉ ghi SAU khi chữ ký đúng (kẻ không có khóa không làm cháy `jti` của client thật); JWKS qua `safehttp` (https, không dải nội bộ), tối đa 256 KiB và 20 khóa, cache 5 phút, gặp `kid` lạ thì tải lại tối đa 30 giây một lần; nhiều khóa trùng `kid` thử từng cái (lỗi khác chữ ký dừng ngay); không gộp với Basic hoặc `client_secret` (`Multiple client authentication methods`) |
| `JTIStore` | `AUTH_REDIS_URL` đặt thì dùng Redis (`SET NX EX`, chung nhiều instance), không thì bộ nhớ (chỉ đúng với MỘT instance; production không đặt Redis sẽ có cảnh báo). Redis hỏng hoặc bộ nhớ đầy là lỗi (fail đóng) |
| `pre-authorized_code` | `grant_type=urn:ietf:params:oauth:grant-type:pre-authorized_code` (giá trị `core/client.ts` của Medplum), tham số `client_id` và `pre-authorized_code`. Login `authMethod=pre-authorized`, mã lưu BĂM, hạn `expiresAt` (thiếu là hết hạn), chiếm mã bằng ghi có điều kiện phiên bản, dùng lại thì thu hồi Login. **Kiểm IP và chính sách TRƯỚC khi chiếm mã** (lỗi không đốt mã). Không PKCE, không `tx_code`: mã là bearer. Việc TẠO Login này (`/auth/preauthorize`) thuộc P6, test dựng Login trực tiếp |
| `POST /oauth2/register` | Không phải đăng ký động RFC 7591: chỉ tra client dựng sẵn (`medplum-cli` nếu bật và `AUTH_DEFAULT_OAUTH_CLIENTS`) theo `redirect_uris[0]` khớp CHÍNH XÁC; `201` không có `client_secret`. **Mặc định tắt** (`AUTH_OAUTH_REGISTER_ENABLED`); tắt thì không có route và không khai `registration_endpoint` |
| Cấu hình mới | `AUTH_ALLOW_PRIVATE_JWKS` (dev/test, cấm ở staging/production vì tắt chống SSRF), `AUTH_REDIS_URL`, `AUTH_OAUTH_REGISTER_ENABLED` |
| Discovery | `token_endpoint_auth_methods_supported` thêm `private_key_jwt`, thêm `token_endpoint_auth_signing_alg_values_supported`, grant `pre-authorized_code` |
| Chưa làm | `token-exchange` và mTLS (P6/bỏ); `AuditLog` riêng; `smart-configuration` chưa khai `client-confidential-asymmetric` |

### B8: kết quả tích hợp

| Chủ đề | Kết quả |
|---|---|
| Test hai tiến trình | `e2e/authen_fhir_test.go` (`make test-e2e`, đặt `TRIVITA_E2E=1`, không cần Docker): build `auth` và `fhir-server` một lần, `auth keys generate`, `auth seed project` hai project, chạy cả hai tiến trình với SQLite tạm và cổng ngẫu nhiên, rồi điều khiển qua HTTP. Mặc định `go test ./...` bỏ qua (chậm vì build) |
| Đã kiểm | đăng nhập SPA (PKCE) → code → token → `POST/GET /fhir/R4/Patient` của fhir-server `201`/`200`; cách ly project (project B: `404` và bundle rỗng cho dữ liệu của A); scope SMART `system/Observation.rs` (`Observation` `200`, `Patient` `403`, ghi `403`); xin `fhir.admin` bị `400`; chính sách mặc định của client (ghi Patient `201`, đọc ClientApplication `403`); refresh token ra access token dùng được; discovery khớp cấu hình fhir-server |
| Token giả | `401` cho: không token, rác, ký bằng khóa lạ mượn `kid` thật (kể cả khi issuer, aud và `scope=fhir.admin` đều đúng), payload sửa giữ chữ ký, `alg: none`, HS256, id token và refresh token dùng làm access token |
| Đã kiểm độ nhạy | đổi `FHIR_JWT_AUDIENCE` của fhir-server lệch với auth thì test đỏ |
| Giới hạn đã biết, có test ghi lại | `TestKnownRevocationWindowBetweenAuthenAndFhirServer`: sau logout auth từ chối token ngay (`userinfo` `401`, `introspect` `inactive`) nhưng fhir-server vẫn nhận cho tới khi hết hạn (D-13). Nếu sau này đóng cửa sổ này thì phải sửa test và `oauth.md` có chủ đích |
| Không kiểm ở e2e | token hết hạn (fhirauth có chênh lệch đồng hồ 30 giây nên cần chờ hơn 30 giây; đã có test đơn vị ở `pkg/fhirauth`); `docker compose` (chưa chạy thử); PostgreSQL |
| Cách khớp địa chỉ | `FHIR_JWKS_URL` có thể là địa chỉ nội bộ (`http://auth:8000/...`) khác `AUTH_BASE_URL` công khai; `FHIR_JWT_ISSUER` phải bằng `AUTH_ISSUER` từng ký tự. Staging/production: `FHIR_JWKS_URL` bắt buộc https (kiểm ở `fhir-server/config`) |

### B9: kết quả rà soát (2026-11-21)

| Chủ đề | Kết quả |
|---|---|
| PostgreSQL thật | `make test-auth-pg` xanh với `go test -race` (Postgres 16): 0 ca SKIP, 136 ca con chạy trên `postgres`. Bao gồm B3-B7 (trước đó chưa từng chạy trên PG) và các test đua 16 yêu cầu |
| Mục 8, 21 dòng | Mỗi dòng có ít nhất một test (bảng dưới). Hai dòng thiếu test trực tiếp đã được thêm ở `bootstrap/security_controls_test.go`: **#19** (`TestRateLimitCoversOAuthTokenAndAuthorize`: hạn mức chung phủ `/oauth2/token`, `/oauth2/authorize`, `/oauth2/introspect`; trước đó test chỉ chứng minh cho `/auth/*`) và **#20** (`TestSecurityEventsAreLoggedWithoutSecrets`: PKCE sai, dùng lại code, dùng lại refresh, phát lại assertion đều có log WARN, và log không chứa code, verifier, refresh token, refresh secret, access token, assertion, secret client) |
| Swagger | Sinh lại bằng `make swagger APP=auth`. Thiếu 6 route `.well-known` và các biến thể `POST /oauth2/userinfo`, `GET /oauth2/logout`, `POST /oauth2/authorize`: đã bổ sung, mọi route nghiệp vụ nay có trong `swagger.json` (trừ `/healthz`, `/health/startup`, `/swagger/*`) |
| Docker compose | `docker compose -f docker-compose.yml -f docker-compose.auth.yml up --build` chạy được: 3 container healthy, seed project trong container, `client_credentials` lấy token, `POST/GET /fhir/R4/Patient` qua JWKS `201`/`200`, token sửa `401`, không token `401`. Đã dọn (`down -v`) |

Đối chiếu mục 8 với test:

| # | Test chính |
|---|---|
| 1 PKCE | `TestAuthorizationCodePKCE`, `...PkceOptionalClientStillVerifiesChallenge`, `...WithoutChallengeRequiresClientAuthentication` |
| 2 code một lần, nguyên tử | `TestAuthorizationCodeConcurrentRedemptionYieldsExactlyOneToken` |
| 3 dùng lại code thì thu hồi | `TestAuthorizationCodeReuseRevokesLoginAndTokens` |
| 4 code lưu băm, hạn | `TestAuthorizationCodeExpiresFromIssueTimeNotFromLastWrite`, kiểm `Login.Code` là băm ở `oauth_code_test.go:129` |
| 5 `redirect_uri` | `TestAuthorizationCodeRedirectURIMustMatchLoginExactly` |
| 6 thu hồi | `TestLogoutRevokesLoginAndClearsCookie`, `TestLogoutRevokesSessionChain`, `e2e` |
| 7 xoay refresh | `TestRefreshToken*` (xoay, ân hạn, dùng lại, đua 16 yêu cầu, trần phiên) |
| 8, 9 so sánh hằng thời gian, xác thực client | `TestSecret`, `TestMatchClientSecret`, `TestClientCredentialsParsingFollowsRFC6749` (secret có `:`, UTF-8, URL-encode), `TestRefreshTokenClientAuthentication` |
| 10 thông báo lỗi | `TestClientCredentialsErrors`, `TestAuthorizationCodeBasicErrors` |
| 11 client assertion | `TestPrivateKeyJWTRejectsBadAssertions` (thiếu `exp`, `exp` quá xa, thiếu `jti`, HS256), `...JTIIsSingleUse...`, `...JWKSURLIsProtectedAgainstSSRF` |
| 12 scope client khai | `TestClientCredentialsNeverGrantsFhirAdminFromRequest` |
| 13 tách loại token | `TestBearerRequiresAccessTokenUse`, `TestRefreshTokenRejectsInvalidTokens`, `TestAuthorizeIDTokenHintRequiresIDTokenUse` |
| 14 cookie | `TestHTTPLoginCookie`, `TestAuthorizeSessionCookieIsBoundToClient`, kiểm `Login.Cookie` là băm, header ở `oauth_authorize_test.go:195` |
| 15, 16, 17 `iss`, `max_age`, PKCE không kế thừa | `TestAuthorizeParameterErrorsRedirectWithStateAndIss`, `TestAuthorizeMaxAgeAndInvalidSessions`, `TestAuthorizeDoesNotInheritPKCEFromPreviousLogin` |
| 18 IP, proxy | `TestHTTPClientIPIsNotSpoofableViaXForwardedFor`, `TestRefreshTokenEnforcesIPRulesAndDoesNotRecordBlockedIP` |
| 19 giới hạn tốc độ | `TestRateLimitCoversOAuthTokenAndAuthorize` (mới), `TestHTTPLoginRateLimitIsStricterAndPerIP` |
| 20 audit | `TestSecurityEventsAreLoggedWithoutSecrets` (mới); chưa có bảng audit riêng |
| 21 không lộ bí mật | `TestHTTPLoginWrongCredentialsLeakNothing`, `TestAuthDataIsIsolatedFromClinicalStore`, `http_test.go:114,350` |

Rủi ro còn lại đã ghi nhận (không sửa ở B9, cần quyết định):
1. **Kích thước token.** Access token của client mặc định dài khoảng **8,3 KB** (chữ ký RS256 + claim `fhir_access_policy`). Trần cấu hình là 16 KB (`AUTH_MAX_POLICY_CLAIM_BYTES`), nhưng nginx và ingress-nginx mặc định chỉ nhận một dòng header 8 KB (`large_client_header_buffers`): ở sau proxy mặc định, `Authorization: Bearer ...` có thể bị `400`/`414`. Hướng xử lý: nâng `large_client_header_buffers`, hoặc rút gọn claim policy, hoặc đổi sang ES256 (chữ ký ngắn hơn nhiều).
2. **Hạn mức `/oauth2/token` dùng chung 20 yêu cầu/giây/IP** (`AUTH_RATE_LIMIT`), lỏng hơn 1 yêu cầu/giây của đăng nhập mặc dù cũng kiểm secret client. Siết cho riêng `/oauth2/token` sẽ cản các client sau NAT dùng chung IP, nên chưa đổi.
3. **Chưa có `AuditLog` riêng** (chỉ slog, mất khi log xoay vòng); cửa sổ thu hồi D-13 giữa auth và `fhir-server` (đã có test ghi lại).
