# P6 (đăng nhập ngoài) và P7 (quản trị project) — port từ Medplum sang `apps/auth`

Thuộc kế hoạch tổng [apps-auth.md](apps-auth.md) (quyết định D1 đến D6; D3: `/admin/*` để đợt sau). Hợp đồng liên quan: [auth-dto.md](../api-contract/auth-dto.md), [admin.md](../api-contract/admin.md).

Trạng thái: **P6 đã duyệt (2026-11-22, Q1 đến Q11 bộ an toàn); P6 XONG 6.0 đến 6.6 (xem mục "Tiến độ P6" cuối phần A); còn P7**. Mọi điều dưới đây là điều đã đọc thấy trong source Medplum `/Users/hocnt84/Downloads/medplum/packages/server/src` (viết tắt `S/`), kèm `file:dòng`. Chỗ không chắc ghi **chưa xác nhận**. Tên package/hàm Go là **đề xuất**, chưa có trong repo. Chưa chạy thử server Medplum thật, mọi kỳ vọng lấy từ `*.test.ts` (test của Medplum mock `fetch` và `jose`, không gọi IdP thật).

## 0. Điều phát hiện khi đọc, lệch so với tài liệu hiện có

| # | Phát hiện | Bằng chứng | Hệ quả |
|---|---|---|---|
| 0.1 | `POST /auth/exchange` **không** trả `LoginResult` như `auth-dto.md` ghi, mà trả **body token** (`token_type`, `access_token`, `id_token`, `refresh_token`, `project`, `profile`, ...) | `S/auth/exchange.ts:21-28` gọi `exchangeExternalAuthToken` → `sendTokenResponse` (`S/oauth/token.ts:553-646`, `1105-1181`); test `S/auth/exchange.test.ts:170-179` đọc `res.body.access_token` | Sửa `auth-dto.md` mục `/auth/exchange` khi vào P6 |
| 0.2 | `/auth/exchange` là **deprecated**, thay bằng grant `urn:ietf:params:oauth:grant-type:token-exchange` ở `/oauth2/token`; cả hai dùng chung một hàm | `S/auth/exchange.ts:9-14`, `S/oauth/token.ts:118-119`, `532-542` | Cài hàm dùng chung một lần; `/auth/exchange` là vỏ mỏng |
| 0.3 | `admin.md` ghi "không guard ở route (xem middleware cha)". Thực tế guard nằm ở router: project router `use(authenticateRequest)` + `use(verifyProjectAdmin)`; super router `use(authenticateRequest)` và mỗi handler gọi `requireSuperAdmin()` | `S/admin/project.ts:25-27`, `S/admin/utils.ts:13-20`, `S/admin/super.ts:104`, `S/context.ts:374-380` | Cập nhật `admin.md` cột "Xác thực" |
| 0.4 | `verifyMultipleMatchingException` (user nêu trong P6) **không** thuộc đăng nhập ngoài: nó chỉ dùng khi xác minh `client_assertion` (private_key_jwt) của client OAuth ở `/oauth2/token` | định nghĩa `S/oauth/utils.ts:1170-1189`, nơi gọi duy nhất `S/oauth/token.ts:1005` (trong `parseClientAssertion`, `token.ts:945-1017`) | Thuộc P3 (client_credentials). Không đưa vào P6 |
| 0.5 | `tryExternalAuth` / `tryExternalAuthLogin` (bearer token của IdP ngoài gọi thẳng FHIR API) là cơ chế **bên resource server**, nằm trong `getLoginForAccessToken` | `S/oauth/utils.ts:1201-1260` (fallback khi JWT Medplum không hợp lệ), `1439-1704` | Không thuộc `auth` (xem mục A8); thuộc `pkg/fhirauth`/`fhir-server` nếu cần |
| 0.6 | `state` của `/auth/external` là **JSON do client tự dựng, không ký, không gắn phiên server**. `/auth/method` trả `authorizeUrl` **không có `state`**; client React tự thêm `state` | `S/auth/method.ts:54-60`; client `packages/react/src/auth/AuthenticationForm.tsx:65-72`, `packages/core/src/client.ts:1719-1746` | Không có kiểm tra CSRF/login fixation phía server. Xem mục A7 và câu hỏi mở Q1 |
| 0.7 | PKCE phía IdP: server dùng `body.codeChallenge` làm `code_verifier` gửi IdP | `S/auth/external.ts:104` truyền `body.codeChallenge`, `external.ts:349-351` gắn vào `code_verifier` khi `idp.usePkce` | Chỉ nhất quán với phương thức `plain` (test dùng `plain`/`xyz`, `external.test.ts:851-902`). **Chưa xác nhận** hành vi với IdP thật dùng S256 |
| 0.8 | Nhiều chỗ đặt cờ `emailVerified=true` dựa trên lời IdP, kể cả khi IdP báo `email_verified=false` (callback chỉ ghi cảnh báo) | `S/auth/external.ts:117-124` (cảnh báo), `162-165` (đặt `true`); `S/oauth/token.ts:617-622`, `636-639` | Xem mục A7 (đề xuất lệch có chủ đích) |

---

# PHẦN A. P6 — Đăng nhập ngoài

## A1. Mục tiêu và phạm vi

Mục tiêu: cho phép người dùng đăng nhập vào `auth` bằng IdP ngoài (OIDC chuẩn, Google, GCIP) hoặc đổi token của IdP ngoài lấy token của `auth`, theo hành vi của Medplum, để client Medplum (`@medplum/react`, `@medplum/core`) không phải đổi.

| Route | Trong P6 | Ghi chú |
|---|---|---|
| `POST /auth/method` | Có | Đã mô tả ở `auth-dto.md`; P2 có thể làm trả `{}`, P6 hoàn thiện nhánh `DomainConfiguration` |
| `GET /auth/external`, `GET /auth/external/{clientId}` | Có | Callback OIDC (302) |
| `POST /auth/google` | Có | |
| `POST /auth/exchange` | Có (deprecated) | Vỏ mỏng của hàm trao đổi token |
| grant `token-exchange` ở `POST /oauth2/token` | Có, **phần đăng nhập ngoài** | Router `/oauth2/token` thuộc P3; P6 thêm nhánh grant |
| `GET /auth/clientinfo/{clientId}` | Có | |
| `POST /auth/preauthorize` | Có | Kèm grant `pre-authorized_code` (phần cấp token, xem A3.9) vì không test được riêng |
| Bearer IdP ngoài gọi FHIR API (`tryExternalAuth`) | **Không** | Mục A8 |
| `client_assertion` / `verifyMultipleMatchingException` | **Không** | P3, mục 0.4 |

Điều kiện tiên quyết (từ pha trước): `TryLogin` và `SetLoginMembership` (P2), `sendLoginResult` (P2), `sendTokenResponse`, router `/oauth2/token` và middleware xác thực (gồm xử lý `X-Medplum-On-Behalf-Of`, `S/oauth/utils.ts:1369-1427`) (P3), `Mailer` (P4, chỉ cho `sendVerificationEmail` ở google).

## A2. Bảng ánh xạ source → Go

Quy ước đích: `internal/domain/<x>`, `internal/application/<x>`, `internal/infrastructure/<x>`, `internal/presentation/httpapi/<file>.go` trong `apps/auth/` (theo `p0-scaffold.md`).

| Source (`S/...`) | Hành vi | Đích Go (đề xuất) |
|---|---|---|
| `auth/method.ts:19-35` `methodValidator`, `methodHandler` | Kiểm `email` hợp lệ; trả `{}` hoặc `{domain, authorizeUrl}` | `presentation/httpapi/method.go`; `application/external.AuthMethod(ctx, email)` |
| `auth/method.ts:42-65` `isExternalAuth` | Tách `domain = email.split('@')[1]`; tìm `DomainConfiguration`; cần `identityProvider.authorizeUrl` và `clientId`; dựng URL với `client_id`, `redirect_uri=<baseUrl>auth/external`, `response_type=code`, `scope=openid profile email`; URL hỏng → 400 `Failed to construct URL for the domain` | `application/external.ResolveExternalAuth`; hàm thuần dựng URL ở `domain/external.BuildAuthorizeURL` |
| `auth/method.ts:72-85` `getDomainConfiguration` | Tìm theo `domain` (chữ thường, `escapeSearchValue`) | cổng `DomainConfigurations.ByDomain(ctx, domain)` |
| `auth/external.ts:34-46` `ExternalAuthState` | Cấu trúc state | `domain/external.State` (JSON tag y hệt, xem A3.1) |
| `auth/external.ts:56-215` `externalCallbackHandler` | Callback | `application/external.HandleCallback`; `presentation/httpapi/external.go` (302) |
| `auth/external.ts:224-265` `getIdentityProvider` | Chọn IdP | `application/external.selectIdentityProvider` |
| `auth/external.ts:274-314` `resolveExternalLoginIdentity`, `getIdentitySource/MappingMode` | Ánh xạ claim → `email`/`externalId` | `domain/external.ResolveIdentity(idp, claims)` (thuần, không I/O) |
| `auth/external.ts:324-388` `verifyExternalCode` | Đổi `code` ở `tokenUrl` | cổng `OIDCClient.ExchangeCode`; adapter `infrastructure/oidc` |
| `auth/external.ts:404-422` `verifyExternalIdToken` | Xác minh id_token bằng JWKS (nếu có `jwksUrl`) hoặc chỉ parse | cổng `IDTokenVerifier`; adapter `infrastructure/oidc` (JWKS cache) |
| `auth/external.ts:430-456` `isValidReturnToUrl` | Kiểm `returnTo` theo `allowedPostLoginRedirectUrls` | `domain/external.ValidReturnTo(returnTo, allowed)` (thuần) |
| `auth/external.ts:465-482` `isLegacyExternalAuthAllowed`, `isDangerousRedirectUriPartialMatchAllowed` | Đọc `Project.systemSetting.legacyExternalAuth` và `Project.setting.allow-dangerous-redirect` | cổng `Projects.ByID`; hàm thuần `domain/project.Setting(name)` |
| `oauth/clients.ts:50-97` `getClientRedirectUri(s)`, `isAllowedPartialRedirectUri` | So khớp redirect URI (chính xác; một phần nếu `allowPartial`) | `domain/oauthclient.MatchRedirectURI` (đã có từ P3 `authorize`; **chưa xác nhận** P3 đã tạo) |
| `auth/google.ts:50-175` `googleHandler` | Đăng nhập Google | `application/external.GoogleLogin`; `presentation/httpapi/google.go` |
| `auth/google.ts:181-203` `getProjectsByGoogleClientId` | Tìm project theo `Project.site.googleClientId` (search param `google-client-id`) | cổng `Projects.ByGoogleClientID(ctx, id, projectID?)` |
| `auth/google.ts:31`, `88-100` | JWKS Google `https://www.googleapis.com/oauth2/v3/certs`, `iss=https://accounts.google.com`, `alg=RS256`, `aud=googleClientId` | cổng `GoogleVerifier.Verify`; adapter `infrastructure/googleid` |
| `auth/exchange.ts:16-29` | Validator + gọi hàm trao đổi | `presentation/httpapi/exchange.go` |
| `oauth/token.ts:532-646` `handleTokenExchange`, `exchangeExternalAuthToken` | Trao đổi | `application/external.ExchangeToken` |
| `oauth/token.ts:648-733` | Validate, đọc client, chọn IdP | cùng package |
| `oauth/utils.ts:1030-1143` `getExternalUserInfo`, `buildExternalUserInfoRequest`, `normalizeExternalUserInfo` | Gọi `userInfoUrl`; chế độ `gcip` | cổng `OIDCClient.UserInfo`; adapter `infrastructure/oidc` |
| `oauth/utils.ts:1145-1148` `isExternalEmailUnverified` | `email_verified === false` hoặc `'false'` | `domain/external.IsEmailUnverified` |
| `oauth/utils.ts:155-256`, `258-307`, `315-332`, `423-481`, `510-625` | `tryLogin`, validate, `authenticate`, memberships, `setLoginMembership` | **P2** cung cấp; P6 chỉ dùng và thêm nhánh (A3.2) |
| `oauth/utils.ts:860-896` `getUserByExternalId` | Tìm user qua `ProjectMembership.externalId` + project; dự phòng `User.externalId` (deprecated) | cổng `Users.ByExternalID` (P2 hoặc P6, xem A10 bước 6.0) |
| `oauth/utils.ts:908-945` `getUserByEmail(InProject)` | Ưu tiên user có `project`, sau đó user không `project` | P2 |
| `auth/clientinfo.ts:24-39` | Trả `ClientApplication.signInForm`, ký lại attachment | `presentation/httpapi/clientinfo.go`; `application/external.ClientInfo` |
| `auth/preauthorize.ts:14-80` | Tạo `Login` loại `pre-authorized` | `application/preauth.Mint`; `presentation/httpapi/preauthorize.go` |
| `oauth/token.ts:741-835` `handlePreAuthorizedCode` | Đổi mã thành token | `application/preauth.Redeem`; nối vào router `/oauth2/token` của P3 |
| `oauth/utils.ts:644-736` `isSmartScopeSubset`, `isNonSmartScopeSubset` | Tập con scope | P3/P2 (`scope`); P6 chỉ gọi |

## A3. Thuật toán từng bước

### A3.1 `State` (JSON trong query `state`)

Trường (đều tùy chọn): `domain`, `issuer`, `projectId`, `clientId`, `scope`, `nonce`, `launch`, `codeChallenge`, `codeChallengeMethod`, `redirectUri`, `returnTo` (`S/auth/external.ts:34-46`). Go: struct với `json:"...,omitempty"`, `json.Unmarshal` với `DisallowUnknownFields` **không** dùng (client có thể gửi thêm trường như `scope`, `remember`; cần khoan dung). Giới hạn độ dài `state` (đề xuất 8 KiB; Medplum không giới hạn, **lệch có chủ đích**).

### A3.2 Callback `GET /auth/external[/{clientId}]` (`external.ts:56-215`)

Thứ tự và mã lỗi chính xác (mọi lỗi dạng `OperationOutcome` `badRequest`, HTTP 400, trừ ghi chú):

| # | Bước | Điều kiện sai → kết quả | Dòng |
|---|---|---|---|
| 1 | Đọc `callbackClientId` từ path (nếu có) | | 58 |
| 2 | `code` query rỗng/thiếu | 400 `Missing code` | 60-64 |
| 3 | `state` rỗng/thiếu | 400 `Missing state` | 66-70 |
| 4 | `JSON.parse(state)` | lỗi → 400 `Invalid state` | 72-78 |
| 5 | Nếu có `callbackClientId`: `state.clientId` phải bằng nó | khác → 400 `Invalid client` (chưa gọi mạng, test `external.test.ts:686-697`) | 80-83 |
| 6 | Nếu `state.domain` và **không** có `callbackClientId`: đọc `DomainConfiguration` theo `domain` (lowercase) | | 86-89 |
| 7 | Chọn IdP (A3.3). Không có IdP | 400 `Identity provider not found` | 91-95 |
| 8 | RFC 9207: nếu query `iss` có mặt **và** `idp.issuer` có cấu hình thì phải bằng nhau | khác → 400 `Invalid issuer`. Nếu `iss` vắng mặt hoặc `idp.issuer` rỗng thì **bỏ qua** | 97-102 |
| 9 | `verifyExternalCode` (A3.4) → claims id_token | lỗi → 400 (các thông điệp ở A3.4) | 104 |
| 10 | `resolveExternalLoginIdentity` (A3.5) | lỗi → 400 thông điệp riêng | 106-115 |
| 11 | Nếu có `email` mà claim `email_verified` là `false`/`'false'` → chỉ ghi log cảnh báo | không chặn | 117-124 |
| 12 | Nếu `state.domain`: `identity.email` phải kết thúc bằng `'@' + state.domain` (so chuỗi nguyên văn, không lowercase `state.domain`) | sai → 400 `Email address does not match domain`. Nếu IdP theo `externalId` (không có email) thì `identity.email` rỗng → cũng sai | 126-129 |
| 13 | Nếu có `client`: `state.projectId` (nếu có) phải bằng `client.meta.project` | khác → 400 `Invalid project`; sau đó `projectId = client.meta.project` | 131-138 |
| 14 | `tryLogin` với `authMethod: 'external'`, `email`, `externalId`, `projectId`, `clientId=state.clientId`, `scope = state.scope ?? 'openid offline_access'`, `nonce = state.nonce ?? uuid`, `launchId=state.launch`, `codeChallenge(+Method)`, `remoteAddress`, `userAgent`. **Không** truyền `redirectUri`, `origin`, `resourceType`, `membershipId` | lỗi `tryLogin` → 400 (vd `User not found`, `Invalid projectId`, `Invalid code challenge`, `Project ID is required for external ID`) | 140-153 |
| 15 | Nếu đi qua callback chung và `client.identityProvider` có: log cảnh báo | | 155-160 |
| 16 | `patch User /emailVerified = true` (`systemRepo`) | idempotent: nếu đã `true` thì không tạo version mới (`external.test.ts:310-344`) | 162-165 |
| 17a | Nếu `login.membership` **và** `state.redirectUri` **và** `client`: tìm redirect URI khớp **chính xác** trong client (`redirectUri` + `redirectUris`); nếu không khớp và `Project.setting.allow-dangerous-redirect = true` thì cho khớp một phần (cùng `origin`, `pathname` bắt đầu bằng đường dẫn đã đăng ký) và log cảnh báo | không khớp → 400 `Invalid redirect URI`. Nếu khớp: `302` tới `redirectUri?login=<id>&code=<login.code>` | 167-193; `oauth/clients.ts:50-97` |
| 17b | Còn lại: `signInPage = state.returnTo` nếu hợp lệ theo `domainConfig.allowedPostLoginRedirectUrls`, ngược lại `<appBaseUrl>/signin` (hoặc `/oauth` nếu `login.launch`). `302` tới đó kèm `login`, `scope`, `nonce`, `code_challenge`, `code_challenge_method` | | 195-214 |

`isValidReturnToUrl` (`external.ts:430-456`): cả `returnTo` và từng URL cho phép phải parse được; yêu cầu **cùng protocol, hostname, port**, và `pathname` của `returnTo` bắt đầu bằng pathname của URL cho phép. Chỉ áp dụng khi có `domainConfig` (tức luồng domain, không áp dụng cho luồng client). Test `external.test.ts:1077-1181`.

Ghi chú hành vi cần giữ hoặc quyết định:
- (a) Tác dụng phụ xảy ra **trước** kiểm redirect URI: `Login` đã được tạo và `emailVerified` đã được đặt `true` rồi mới trả 400 `Invalid redirect URI` (bước 14, 16 trước 17a). Đề xuất Go: kiểm redirect URI **trước** `tryLogin` (lệch an toàn hơn, không đổi kết quả với client hợp lệ).
- (b) Lỗi trả JSON `OperationOutcome` (trình duyệt hiển thị JSON) thay vì chuyển hướng về app kèm lỗi. Giữ nguyên để khớp (Q4).
- (c) Nếu không có membership (nhiều profile) và có `redirectUri`: rơi xuống 17b, app gọi `/auth/profile` (P2).
- (d) `getClientApplication` không tìm thấy → ném `notFound` → **404** (không bắt trong handler: `external.ts:234`, `239`). `getDomainConfiguration` không có → bước 7.
- (e) `domain` trong `/auth/method` trả về **nguyên văn** phần sau `@` của email (không lowercase; `method.ts:43,60`), trong khi `getDomainConfiguration` lowercase khi tìm (`method.ts:80`) và callback so `endsWith('@' + state.domain)` với email đã lowercase (`external.ts:283-285`, `126`). Nếu client gửi lại `domain` có chữ hoa thì bước 12 sai. **Chưa xác nhận** client có lowercase. Đề xuất Go: lowercase `domain` ở cả `/auth/method` và bước 12.

### A3.3 Chọn IdP (`getIdentityProvider`, `external.ts:224-265`)

1. Có `callbackClientId`: đọc client (404 nếu không có); trả `client.identityProvider` (có thể rỗng → bước 7 ở A3.2). **Không** thử nguồn khác (`external.test.ts:699-710`).
2. Có `state.clientId`: đọc client. Nếu `client.identityProvider` có: chỉ cho dùng qua callback chung khi `Project.systemSetting.legacyExternalAuth = true` (hoặc client không có project: `meta.project` rỗng → luôn cho). Ngược lại ném 400 `Client identity provider must use the per-client callback <baseUrl>auth/external/<clientId>`, **không** gọi mạng (`external.test.ts:626-662`). Quan trọng: `systemSetting` chỉ super admin sửa được; admin project đặt `Project.setting.legacyExternalAuth` **không** có tác dụng (test `external.test.ts:645-657`).
3. `domainConfig.identityProvider` nếu có.
4. `state.issuer`: tìm trong cấu hình máy chủ `externalAuthProviders` theo `provider.issuer === state.issuer` → `identityProvider`.
5. Không có → `undefined`.

Lưu ý: `client` (nếu có `state.clientId` mà client không có IdP) vẫn được trả về và dùng ở bước 13, 17a (khi đó IdP lấy từ domain/issuer). Test `Invalid client` (`external.test.ts:571-584`): `clientId` của client mặc định không có IdP, state không có domain → 400 `Identity provider not found`.

### A3.4 `verifyExternalCode` (`external.ts:324-388`)

1. `idp.tokenUrl` rỗng → 400 `Missing token URL for external identity provider`.
2. Header: `Accept: application/json`, `Accept-Encoding: identity`, `Content-Type: application/x-www-form-urlencoded`.
3. Form: `grant_type=authorization_code`, `redirect_uri = <baseUrl>auth/external` (+ `/<callbackClientId>` nếu có), `code`; nếu `idp.usePkce` và `state.codeChallenge` có: `code_verifier = state.codeChallenge` (mục 0.7).
4. `tokenAuthMethod == client_secret_post`: bắt buộc `clientId` và `clientSecret` (thiếu → 400 `Missing client ID or client secret for external identity provider`); thêm `client_id`, `client_secret` vào form. Mặc định (kể cả giá trị khác): `Authorization: Basic base64(clientId:clientSecret)`. Lưu ý: nhánh Basic **không** kiểm `clientId`/`clientSecret` rỗng; chuỗi `"undefined:undefined"` có thể bị gửi (`external.ts:362`, vì nối `undefined`). Go: kiểm rỗng ở cả hai nhánh (lệch an toàn).
5. POST qua `safeFetch` (chống SSRF, A5.2). `response.json()`; `!response.ok` → log cảnh báo (kèm **body**), ném 400 `Failed to verify code - check your identity provider configuration`.
6. Mọi lỗi khác (mạng, JSON hỏng) → cùng thông điệp 400 (`external.ts:380-387`; test `Invalid token request` `external.test.ts:784-797`).
7. `id_token` không phải chuỗi → 400 `Missing id_token in external identity provider response`.
8. Nếu `idp.jwksUrl`: bắt buộc `idp.issuer` (thiếu → 400 `Missing issuer for external identity provider`); xác minh chữ ký bằng JWKS từ xa (qua `safeFetch`), `iss == idp.issuer`, `aud == idp.audience ?? idp.clientId`; thất bại → `jose` ném lỗi **không phải** `OperationOutcomeError` nên bị bắt ở nhánh 6 và thành 400 `Failed to verify code - check your identity provider configuration` (test `external.test.ts:471-569`: token không ký, hoặc `aud` khác → cùng thông điệp). `alg` không bị giới hạn ở đây (jose chọn theo JWKS).
9. Nếu **không** có `jwksUrl`: chỉ `parseJWTPayload(idToken)` — **không kiểm chữ ký, `exp`, `iss`, `aud`, `nonce`** (`external.ts:421`). Tin vào kênh TLS tới `tokenUrl`.

### A3.5 Ánh xạ danh tính (`resolveExternalLoginIdentity`, `external.ts:274-314`)

| `identitySource` (mặc định theo `useSubject`: `subject` nếu true, ngược lại `email`) | `identityMappingMode` (mặc định: `project-membership-external-id` nếu `useSubject`, ngược lại `user-email`) | Kết quả |
|---|---|---|
| `email` | `user-email` | `email = claims.email` (phải là string, `toLowerCase()`); thiếu → 400 `External token does not contain email address` |
| `subject` | `project-membership-external-id` | `externalId = claims.sub` (string); thiếu → 400 `External token does not contain subject` |
| tổ hợp khác (vd `fhir-user`, `subject`+`user-email`, `email`+`project-membership-external-id`) | | 400 `Unsupported identity provider configuration` (test `exchange.test.ts:310-345`) |

Lưu ý quan trọng: email dạng danh sách (`a@x.com,b@y.com`, `"x,y"@d`) **không** được tách: giữ nguyên chuỗi và tìm user chính xác nên ra `User not found` (test `external.test.ts:222-256`). Go phải tìm khớp **chính xác** (không `LIKE`, không tách dấu phẩy).

### A3.6 `tryLogin` với `authMethod: external` / `exchange` / `google` (`utils.ts:155-256`)

P2 cài `TryLogin` đầy đủ. Phần P6 cần P2 hỗ trợ (xác nhận khi P2 xong, đề xuất `LoginRequest` có các trường này):
- `validateLoginRequest` (`utils.ts:258-279`): `projectId` nếu có phải UUID hoặc `new` (400 `Invalid projectId`, trường `projectId`). Với `external`/`exchange`: cần `externalId` hoặc `email` (400 `Missing email or externalId`, trường `externalId`); có `externalId` mà không có `projectId` → 400 `Project ID is required for external ID`. Các `authMethod` khác: cần `email` (`Invalid email`), `google` cần `googleCredentials` (`Invalid google credentials`), `scope` rỗng → `Invalid scope`.
- `validatePkce` (`utils.ts:281-307`): bỏ qua nếu `client.pkceOptional`. Có `codeChallengeMethod` mà không có `codeChallenge` → 400 `Invalid code challenge` (trường `code_challenge`); ngược lại → `Invalid code challenge method`; method khác `plain`/`S256` → cùng lỗi.
- Tìm user: `externalId` → `getUserByExternalId(externalId, projectId)` (`utils.ts:860-896`): ưu tiên `ProjectMembership` có `externalId` (so khớp chính xác) và `project`, rồi đọc `membership.user`; dự phòng `User.externalId` + `User.project` (deprecated, test `external.test.ts:1183-1273`). Ngược lại `email` → `getUserByEmail(email, projectId)` (`utils.ts:908-918`): nếu `projectId` (khác `new`) thì tìm user có `project = Project/<id>` trước, **rồi** rơi về user **không có** `project`.
- Không tìm thấy user → log cảnh báo + 400 `User not found` (với `password` mới là `Email or password is invalid`).
- `authenticate` (`utils.ts:315-332`): `google` (đã xác minh JWT) và `external`/`exchange` không kiểm mật khẩu ("Verified by external auth provider").
- Tạo `Login` (`utils.ts:194-220`): `code` (16), `cookie` (16), `refreshSecret` (32) nếu `includeRefreshToken` (`utils.ts:1001-1019`: scope có `offline_access`, hoặc `client.grantType` có `refresh_token`, cờ cũ `remember` hoặc scope `offline`), `authMethod`, `authTime`, `scope`, `nonce`, `codeChallenge(+Method)`, `remoteAddress`, `userAgent`, `pictureUrl`, `project` (nếu có `projectId`), `profileType`.
- Lấy memberships (`utils.ts:423-481`): lọc `user`, `active != false`, `project`, `profileType`, loại project không active; có membership nhưng mọi project đều không active → 400 `Project not active`. Không có membership và không `allowNoMembership` → 400 `User not found` (**Login đã được tạo trước đó**: rác tích lũy, cần job purge, xem `super.ts` `purge`).
- Đúng 1 membership, hoặc `forceUseFirstMembership` (chỉ `exchange`): `setLoginMembership` (`utils.ts:510-625`): kiểm `revoked`/`granted`/đã có membership, user và project khớp, project/membership active, tính năng `google-auth-required` (400 `Google authentication is required` nếu `authMethod != google`), cập nhật ảnh đại diện từ Google nếu `Project.setting.googleAuthProfilePictures`, quy tắc IP, audit, bỏ `refreshSecret` nếu project là super admin, cập nhật với `ifMatch` (so sánh và hoán đổi; 412 → 400 `Login profile already set`).

### A3.7 `POST /auth/google` (`google.ts`; bổ sung chi tiết còn thiếu so với `auth-dto.md`)

Validator (`google.ts:38-42`): `googleClientId` không rỗng → `Missing googleClientId`; `googleCredential` không rỗng → `Missing googleCredential`; `redirectUri` tùy chọn phải là string → `Invalid redirectUri` (rỗng được bỏ qua `values: 'falsy'`). Lỗi validator: 400 `OperationOutcome` (`invalidRequest(errors)`).

Các bước (`google.ts:50-175`):
1. `resourceType` từ body (tùy chọn, lọc membership theo loại profile).
2. `projectId = validateProjectId(body.projectId)`: chỉ nhận string là UUID hoặc `new`, **giá trị khác bị bỏ qua âm thầm** (`google.ts:177-179`). Rồi `getProjectIdByClientId(clientId, projectId)` (`S/auth/utils.ts:586-601`): có `clientId` → đọc client (404 nếu không có); `projectId` đã có mà khác `client.meta.project` → 400 `Invalid projectId`; trả `client.meta.project`.
3. Nếu `googleClientId` khác `config.googleClientId`: tìm `Project` có `site.googleClientId = googleClientId` (và `_id = projectId` nếu có). 0 kết quả → 400 `Invalid googleClientId`; đúng 1 → `projectId = project.id`; nhiều → log cảnh báo, `projectId` giữ nguyên (có thể rỗng).
4. Xác minh JWT Google: `iss = https://accounts.google.com`, `alg = RS256`, `aud = googleClientId`, JWKS Google. Lỗi → 400 với **`err.message` của jose** (test mock cho `Verification failed`, `google.test.ts:92-100`). Chưa kiểm `exp`/`iat` thủ công: jose mặc định kiểm `exp` và `nbf` nếu có.
5. `email = claims.email.toLowerCase()` (**không** kiểm claim `email` có mặt: thiếu → lỗi runtime 500; **không** kiểm `email_verified` trước khi cho đăng nhập, xem A7).
6. `isExternalAuth(email)`: domain của email có `DomainConfiguration` với `authorizeUrl` + `clientId` → trả 200 `{domain, authorizeUrl}` và **dừng** (không đăng nhập; test `google.test.ts:389-421` chỉ kiểm project bắt buộc Google, chưa có ca riêng cho nhánh này: **chưa xác nhận** có test).
7. `getUserByEmail(email, projectId)`:
   - Không có user: nếu không `createUser` → 400 `User not found`. Nếu `createUser`: `config.registerEnabled === false` **và** `projectId` rỗng hoặc `new` → 400 `Registration is disabled` (so sánh tường minh với `false`; nếu `projectId` là project cụ thể thì **vẫn tạo user dù đăng ký bị tắt**). Tạo `User` (`firstName=given_name`, `lastName=family_name`, `email`, `emailVerified = claims.email_verified`, `project` và `meta.project` nếu `projectId` cụ thể và khác `new`).
   - Có user, `claims.email_verified` đúng, user chưa verified: `patch /emailVerified=true` (chỉ nâng, không bao giờ hạ; test `google.test.ts:278-332`).
8. `tryLogin` với `authMethod: google`, `googleCredentials=claims`, `scope = body.scope ?? 'openid offline_access'` (lưu ý `??`, chuỗi rỗng không được thay), `nonce = body.nonce || uuid`, `redirectUri` (được kiểm với client trong `tryLogin`: 400 `Invalid redirect URI`), `allowNoMembership = createUser || projectId === 'new'`, `pictureUrl = claims.picture`.
9. Nếu `config.requireVerifiedEmailForProjectCreation` **và** `createUser` **và** `projectId === 'new'` **và** `!user.emailVerified`: gửi mail xác minh (`sendVerificationEmail`, P4) và trả 200 `{login, emailVerificationRequired: true}`.
10. `sendLoginResult(login)` (P2): trả `LoginResult` (dạng 3 `{login}` khi `Project/new`, dạng 4 `{login, code}`, dạng 5 danh sách membership; MFA không áp dụng vì `authMethod != password`).

Ghi chú hành vi từ test (`google.test.ts`): `createUser` + không `projectId` → `{login}` không có `code` (không membership, `google.test.ts:148-164`); nhiều project cùng `googleClientId` → `{login, memberships(2)}` (`507-551`); `googleClientId` tùy biến + `projectId` sai → 400 `Invalid googleClientId` (`595-635`); `clientId` + `projectId` lệch → 400 `Invalid projectId` (`664-705`); ảnh đại diện chỉ cập nhật khi `Project.setting.googleAuthProfilePictures` (`423-464`, logic ở `utils.ts:555-575`).

### A3.8 Trao đổi token (`/auth/exchange` và grant `token-exchange`)

Đầu vào: `/auth/exchange` — JSON `{externalAccessToken (bắt buộc: 'Missing externalAccessToken'), clientId (bắt buộc: 'Missing clientId')}`, loại token cố định `access_token` (`exchange.ts:16-29`). Grant — form `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`, `client_id`, `subject_token`, `subject_token_type=urn:ietf:params:oauth:token-type:access_token`, tùy chọn `membership_id`, `scope`, `nonce` (`token.ts:532-542`). Lưu ý hai điểm vào có **dạng lỗi khác nhau** (A3.10).

Các bước (`token.ts:553-646`):
1. Validate (`648-670`): thiếu `clientId` → `invalid_request` `Invalid client`; thiếu `subject_token` → `Invalid subject_token`; `subject_token_type` khác `...:access_token` → `Invalid subject_token_type` (HTTP 400 mọi trường hợp).
2. `idp = resolveExternalAuthProvider(clientId)` (`717-733`): tìm trong cấu hình máy chủ `externalAuthProviders` theo `(provider.clientId ?? provider.identityProvider.clientId) === clientId`; nếu có `identityProvider` dùng nó, nếu chỉ có `userInfoUrl` (deprecated) dùng `{userInfoUrl}`. `useServerExternalAuth = !!idp`.
3. Không có: đọc `ClientApplication` (`tryReadTokenExchangeClient`, lỗi/không có → `invalid_request` `Invalid client`), `idp = client.identityProvider`; không có → `Invalid client`. (Không gọi mạng; test `token.test.ts:2372-2407`.)
4. `projectId`: nếu `useServerExternalAuth`: có `membership_id` thì đọc `ProjectMembership` (không có → `invalid_request` `Invalid membership`), `projectId = membership.project`, không có project → `Invalid membership`; **không** có `membership_id` → `projectId` rỗng. Nếu dùng client: `projectId = getProjectIdByClientId(clientId)` = `client.meta.project`.
5. `idp.userInfoUrl` rỗng → `invalid_request` `Missing user info URL`. Gọi `getExternalUserInfo` (A3.8.1); lỗi → `invalid_request` + thông điệp lỗi, HTTP theo outcome (400, hoặc **429** `Too Many Requests` khi IdP trả 429).
6. `resolveExternalLoginIdentity` (A3.5); lỗi → `invalid_request` + thông điệp (400).
7. `email_verified` false: chỉ cảnh báo (`617-622`).
8. `tryLogin` với `authMethod: exchange`, `email`/`externalId`, `projectId`, `clientId = client?.id` (rỗng nếu IdP máy chủ), `scope = body.scope || 'openid offline_access'`, `nonce = body.nonce || uuid`, `forceUseFirstMembership: true`, `membershipId`. Lỗi `tryLogin` **không bị bắt** → ra lỗi `OperationOutcome` 400 (test `exchange.test.ts:159-168`: `res.body.issue[0].details.text = 'User not found'`).
9. `patch User /emailVerified=true`.
10. `sendTokenResponse` (P3): body token; `aud` là issuer khi không có client (`token.test.ts:2274-2301`).

Rủi ro đã thấy: khi IdP lấy từ cấu hình máy chủ và **không** có `membership_id`, `projectId` rỗng và `forceUseFirstMembership` chọn membership **đầu tiên** của user trong **mọi** project (`token.ts:576-586`, `628-639`). Thứ tự "đầu tiên" do kết quả tìm kiếm quyết định (**chưa xác nhận** có sắp xếp ổn định). Đề xuất Go: bắt buộc `membership_id` hoặc từ chối khi user có nhiều membership (Q5).

#### A3.8.1 `getExternalUserInfo` (`utils.ts:1030-1143`)

- Chế độ chuẩn: `GET userInfoUrl`, `Authorization: Bearer <token>`, `Accept: application/json`, `Accept-Encoding: identity`.
- Chế độ `userInfoMode = gcip` (Google Cloud Identity Platform): thiếu `userInfoApiKey` → 400 `Missing user info API key - check your identity provider configuration` (không gọi mạng); `POST userInfoUrl?key=<apiKey>` body JSON `{"idToken": <token>}`; trả về `users[0]`; rỗng/không phải object → 400 `Failed to verify code - invalid user info response`; thiếu `localId` → 400 `Failed to verify code - missing localId in user info response`; kết quả chuẩn hóa `{...user, sub: localId, email_verified: user.emailVerified}`.
- Lỗi gọi (mạng) → 400 `Failed to verify code - check your identity provider configuration`; HTTP 429 → `tooManyRequests` (429); status khác 200 → cùng 400.
- `Content-Type` chứa `application/json` → parse JSON; chứa `application/jwt` → `parseJWTPayload` **không kiểm chữ ký**; loại khác → 400 `Failed to verify code - unsupported content type: <ct>`.

### A3.9 `POST /auth/preauthorize` và grant `pre-authorized_code`

Mint (`preauthorize.ts:27-80`), thứ tự:
1. Validator (`preauthorize.ts:17-25`) chạy **trước** mọi thứ khác (kể cả quyền): `clientId` phải là UUID (400 `Client ID is required`), `scope` string tùy chọn (`Scope must be a string`), `nonce` string tùy chọn (`Nonce must be a string`), `expiresIn` nguyên 1..604800 (400 `expiresIn must be a positive integer not exceeding 604800 seconds`). Test `preauthorize.test.ts:87-109` xác nhận validator chạy trước kiểm `onBehalfOf`.
2. Chưa xác thực → 401 (`authenticateRequest`, `preauthorize.test.ts:42-45`).
3. `project.superAdmin` hoặc `!membership.admin` → 403.
4. Không có `X-Medplum-On-Behalf-Of` (`authState.onBehalfOfMembership`) → 400 `Pre-authorization requires onBehalfOfMembership`. Middleware: header phải do admin/super admin gửi; dạng `ProjectMembership/<id>` hoặc tham chiếu profile (tìm membership theo `profile` + project của caller); khác project (không phải super admin) hoặc membership/project không active → 403 (`utils.ts:1369-1427`).
5. Scope: `callerScope = login.scope`; mặc định `scope = body.scope || (callerSmartTerms ? 'openid ' + callerSmartTerms : 'openid')`; `hasUnheldTerm` (người gọi bị giới hạn SMART mà xin thêm token phi-SMART không giữ) hoặc không phải tập con (`isSmartScopeSubset(callerScope, scope, hasPatientContext)`, `hasPatientContext` = profile đích bắt đầu `Patient/`) → 400 `Invalid scope` (trường `scope`). Bảng ca ở `preauthorize.test.ts:153-223`.
6. Đọc `ClientApplication` bằng system repo (không phụ thuộc quyền đọc của user đích, `test:225-246`); `client.meta.project != project.id` → 404.
7. `preAuthorizedCode = generateSecret(128)`; lưu `sha256(code)` hex vào `Login.preAuthorizedCodeHash`; `expiresIn` mặc định 3600; tạo `Login{authMethod:'pre-authorized', project, client, membership=onBehalfOf, user, authTime, scope, nonce(body||uuid), remoteAddress, userAgent, preAuthorizedCodeHash, expiresAt}`. Trả 200 `{preAuthorizedCode, expiresAt}` (ISO). Mã **chỉ trả một lần**, DB chỉ giữ hash.

Redeem (`token.ts:741-835`, lỗi OAuth dạng `{error, error_description}` HTTP 400): `client_id` không phải string → `invalid_request` `Missing client_id`; `pre-authorized_code` thiếu → `Missing pre-authorized_code`; client không đọc được → `invalid_client` `Invalid client`; `client.status` có và khác `active` → `invalid_request` `Invalid client`; tìm `Login` theo hash, `authMethod != pre-authorized` → `Invalid pre-authorized_code`; `login.client != ClientApplication/<client_id>` → `Invalid client`; không có `membership` → `Invalid profile`; `expiresAt` rỗng hoặc qua hạn → `invalid_grant` `Pre-authorized code expired`; `granted` đã `true` → **thu hồi login** rồi `invalid_grant` `Token already granted`; `revoked` → `invalid_grant` `Token revoked`; kiểm quy tắc IP (lỗi → `invalid_request` + thông điệp); cuối cùng `sendTokenResponse` (đặt `granted`). So khớp hash dùng tìm kiếm theo cột (không `timingSafeEqual`), chấp nhận được vì mã 128 ký tự ngẫu nhiên và chỉ so hash.

### A3.10 Dạng lỗi (tổng hợp)

| Endpoint | Dạng lỗi | HTTP |
|---|---|---|
| `/auth/method`, `/auth/google`, `/auth/external`, `/auth/clientinfo`, `/auth/preauthorize`, `/auth/exchange` (lỗi `tryLogin`) | `OperationOutcome` (`issue[0].details.text`) | 400 (validator, nghiệp vụ), 401, 403, 404 (không thấy client/resource), 429 chỉ từ `getExternalUserInfo` |
| `/auth/exchange` (lỗi khác: validate request, IdP, danh tính) | OAuth `{error:'invalid_request', error_description}` | 400 (429 khi IdP 429) |
| `/oauth2/token` mọi grant | OAuth `{error, error_description}` | 400 (429 như trên); `Content-Type` không phải `application/x-www-form-urlencoded` → 400 text `Unsupported content type` (`token.ts:101-104`) |
| Ngoại lệ không dự kiến | 500 | |

`/auth/exchange` validator thiếu trường trả `OperationOutcome` (`exchange.test.ts:132-148`) nhưng `Invalid client` (không có IdP) trả OAuth `error_description` (`exchange.test.ts:150-157`). Giữ y hệt để khớp client.

### A3.11 `GET /auth/clientinfo/{clientId}` (`clientinfo.ts:24-39`)

Không xác thực. `clientId` = tham số đầu của path. Đọc client qua `getClientApplication` (có client chuẩn dựng sẵn `medplum-cli` với `redirectUris: ['http://localhost:9615']`, `pkceOptional`; cộng `config.defaultOAuthClients`, `S/oauth/clients.ts:12-37`; không có → 404). Nếu query `redirect_uri` **có mặt** (kể cả rỗng): phải là string và khớp **chính xác** một URI đăng ký, sai → 400 `Invalid redirect URI`. Trả `client.signInForm ?? {}`; nếu client có `meta.project`: thay `Attachment`/URL `Binary/<id>` bằng URL ký sẵn chỉ khi `Binary` thuộc cùng project (test `clientinfo.test.ts:55-87`); khác project thì giữ nguyên `Binary/<id>`. **Mọi trường `signInForm` là công khai** (comment `clientinfo.ts:14-18`); `secret` nằm ngoài `signInForm` nên không lộ. Go cần `Storage.PresignRead` (cổng); nếu `auth` chưa có lưu trữ nhị phân thì **cắt bước ký** và trả `signInForm` nguyên văn (Q6).

### A3.12 `POST /auth/method`

Validator `email` hợp lệ (400 khi thiếu/rỗng/sai, `method.test.ts:25-38`). Trả `{}` nếu không có `DomainConfiguration` hoặc nó thiếu `authorizeUrl`/`clientId`. `authorizeUrl` không có scheme (vd `example.com/oauth2/authorize`) → `new URL` ném → 400 `Failed to construct URL for the domain` (`method.test.ts:95-122`). **Tiết lộ**: phản hồi cho biết domain có cấu hình IdP (comment `method.ts:12-17`, chấp nhận). Tên miền so khớp không phân biệt hoa thường (`method.test.ts:71-93`).

## A4. Mô hình dữ liệu: trường đọc/ghi

Kiểu có sẵn trong `pkg/fhir` (đã kiểm: `resource_clientapplication.go`, `resource_domainconfiguration.go`, `resource_login.go`, `resource_user.go`, `resource_project.go`, `resource_projectmembership.go`, `types_gen.go:1708-1750` cho `IdentityProvider`).

| Resource.trường | Đọc (R) / Ghi (W) | Nơi |
|---|---|---|
| `DomainConfiguration.domain` | R (tìm exact, lowercase) | method, external |
| `DomainConfiguration.identityProvider.{authorizeUrl, tokenUrl, tokenAuthMethod, userInfoUrl, userInfoMode, userInfoApiKey, jwksUrl, issuer, audience, clientId, clientSecret, usePkce, useSubject, identitySource, identityMappingMode}` | R | method, external |
| `DomainConfiguration.allowedPostLoginRedirectUrls` | R | external (`returnTo`) |
| `ClientApplication.identityProvider` (cùng kiểu), `redirectUri`, `redirectUris`, `signInForm`, `status`, `pkceOptional`, `allowedOrigin`, `grantType`, `accessTokenLifetime`, `refreshTokenLifetime`, `meta.project`, `secret` (chỉ lưu; không lộ) | R | external, exchange, clientinfo, preauthorize, tryLogin |
| `Project.setting[allow-dangerous-redirect]`, `Project.setting[googleAuthProfilePictures]`, `Project.systemSetting[legacyExternalAuth]`, `Project.features['google-auth-required']`, `Project.active`, `Project.superAdmin`, `Project.site[].googleClientId` | R | external, google, setLoginMembership |
| `User.email`, `externalId` (deprecated), `project`, `emailVerified` | R; **W**: `emailVerified=true` (patch); tạo `User` mới ở google (`firstName`, `lastName`, `email`, `emailVerified`, `project`, `meta.project`) | external, exchange, google |
| `ProjectMembership.externalId`, `user`, `project`, `profile`, `active`, `admin` | R | getUserByExternalId, memberships, preauthorize, exchange (`membership_id`) |
| `Login` | **W** (tạo): `client`, `launch`, `project`, `profileType`, `user`, `authMethod ∈ {external, exchange, google, pre-authorized}`, `authTime`, `code`, `cookie`, `refreshSecret`, `scope`, `nonce`, `codeChallenge`, `codeChallengeMethod`, `remoteAddress`, `userAgent`, `pictureUrl`, `membership`, `preAuthorizedCodeHash`, `expiresAt`; W: `granted`, `revoked` | tryLogin, preauthorize, redeem |
| `Practitioner/Patient.photo` | W (best-effort, chỉ khi chưa có `photo`) | setLoginMembership |

## A5. Cổng (ports), adapter, thư viện, cấu hình

### A5.1 Cổng cần có (`application/ports.go`, thêm vào danh sách P0)

| Cổng | Chữ ký (đề xuất) | Adapter | Fake cho test |
|---|---|---|---|
| `OIDCClient` | `ExchangeCode(ctx, idp, ExchangeCodeInput) (idToken string, err)`; `UserInfo(ctx, idp, token) (Claims, err)` | `infrastructure/oidc` (`net/http`, SSRF-safe, giới hạn kích thước/timeout) | `httptest.Server` hoặc fake in-memory (không gọi mạng) |
| `IDTokenVerifier` | `VerifyJWKS(ctx, jwt, jwksURL, issuer, audience) (Claims, err)`; `ParseUnsigned(jwt) (Claims, err)` | `infrastructure/oidc` (cache JWKS theo URL, TTL) | fake có khóa ES256 sinh trong test |
| `GoogleVerifier` | `Verify(ctx, credential, audience) (GoogleClaims, err)` | `infrastructure/googleid` (JWKS Google cố định) | fake |
| `DomainConfigurations` | `ByDomain(ctx, domain) (*DomainConfiguration, err)` | `fhirstore` | in-memory |
| `Clients` | `ByID(ctx, id)` (gồm client chuẩn dựng sẵn + `defaultOAuthClients`) | `fhirstore` + cấu hình | in-memory |
| `Projects` | `ByID`, `ByGoogleClientID` | `fhirstore` | in-memory |
| `Users`, `Memberships`, `Logins` | theo P1/P2 | `fhirstore` | in-memory |
| `ExternalProviders` | `ByIssuer(issuer)`, `ByClientID(clientID)` | đọc từ cấu hình `AUTH_EXTERNAL_AUTH_PROVIDERS` | struct |
| `Clock`, `Rand` (sinh secret) | P1 | | |
| `Mailer` | P4 | | |
| `Storage.PresignRead` (tùy chọn, A3.11) | | | |

### A5.2 Chống SSRF (adapter `infrastructure/oidc`, bắt buộc)

Medplum: chỉ chặn **tại thời điểm kết nối** (không kiểm URL khi lưu cấu hình): `safeAgent` yêu cầu HTTPS, chặn hostname `localhost`, `localhost.localdomain`, `*.localhost` và mọi IP không phải `unicast` toàn cục; resolve DNS rồi **kiểm mọi địa chỉ** trả về, kết nối thẳng tới địa chỉ đã resolve với `servername` gốc (`S/util/url.ts:81-114`, `166-195`; thêm `198.18.0.0/15`, `url.ts:176-182`). `config.allowUnsafeOutbound` tắt toàn bộ kiểm tra (dùng cho test/dev, `url.ts:109-114`). Đề xuất Go:
- `http.Transport.DialContext` dùng `net.Dialer{Control: ...}` kiểm `netip.Addr` **sau** resolve (chống DNS rebinding): từ chối loopback, private (`IsPrivate`), link-local (kể cả `169.254.169.254`), multicast, unspecified, CGNAT `100.64.0.0/10`, `198.18.0.0/15`, IPv4-mapped IPv6 (gỡ ánh xạ trước), `fc00::/7`, `fe80::/10`.
- Chỉ HTTPS (trừ khi `AllowUnsafeOutbound`, cấm ở `staging`/`production` như `fhir-server`).
- `CheckRedirect`: kiểm lại từng bước (hoặc tắt redirect cho `tokenUrl`/`userInfoUrl`).
- Thêm (Medplum **không** có, ghi rõ là bổ sung): timeout tổng (đề xuất 10 s), giới hạn kích thước body (1 MiB), giới hạn số kết nối, không gửi `Authorization` sang host khác sau redirect.
- Cùng `http.Client` an toàn dùng cho JWKS của client assertion (P3) và JWKS Google.

### A5.3 Thư viện đề xuất

| Việc | Đề xuất | Ghi chú |
|---|---|---|
| Parse/verify JWT, JWKS | `github.com/go-jose/go-jose/v4` (`jose.ParseSigned`, `jwt`) hoặc `golang-jwt/jwt/v5`: **đã có trong `go.mod`** (`go-jose v4.1.4`, `golang-jwt v5.3.1`) | Tự cài cache JWKS đơn giản (map URL → bộ khóa + hết hạn, singleflight); thay thế: `github.com/coreos/go-oidc/v3` (RemoteKeySet, nhận `http.Client` qua context) hoặc `MicahParks/keyfunc` — **chưa có trong `go.mod`, cần duyệt thêm dependency** |
| Giới hạn `alg` | luôn truyền danh sách `alg` cho phép (Google: `RS256`; IdP: `RS256/384/512`, `ES256/384/512`), **cấm `none` và HS\*** | Medplum không giới hạn `alg` ở `verifyExternalIdToken` (A3.4 mục 8): lệch an toàn hơn |
| Địa chỉ IP | `net/netip` (stdlib) | |
| Rate limit | `golang.org/x/time/rate` (đã gián tiếp trong `go.mod`) hoặc `pkg/httpserve` | |
| Hash, so sánh | `crypto/sha256`, `crypto/subtle` (đặt `ConstantTimeCompare`) | |

### A5.4 Cấu hình (env, tiền tố `AUTH_` theo `p0-scaffold.md`; tên **đề xuất**)

| Biến | Ý nghĩa | Nguồn Medplum |
|---|---|---|
| `AUTH_BASE_URL` | gốc URL công khai của `auth` (dựng `redirect_uri=<base>auth/external`) | `getConfig().baseUrl`, `method.ts:57`, `external.ts:345` |
| `AUTH_APP_BASE_URL` | gốc URL app đăng nhập (`/signin`, `/oauth`) | `external.ts:201-204` |
| `AUTH_GOOGLE_CLIENT_ID` | Google client chính | `google.ts:66` |
| `AUTH_REGISTER_ENABLED` | `registerEnabled` (đã có ở P0) | `google.ts:117` |
| `AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION` | | `google.ts:163-164` |
| `AUTH_EXTERNAL_AUTH_PROVIDERS` | JSON mảng `{issuer, clientId?, userInfoUrl?, identityProvider?}` | `config/types.ts:423-430` |
| `AUTH_DEFAULT_OAUTH_CLIENTS` | JSON mảng `ClientApplication` dựng sẵn | `oauth/clients.ts:28-31` |
| `AUTH_ALLOW_UNSAFE_OUTBOUND` | chỉ dev/test; **cấm** ở `staging`/`production` | `util/url.ts:110` |
| `AUTH_OUTBOUND_TIMEOUT`, `AUTH_OUTBOUND_MAX_BODY` | bổ sung (Medplum không có) | |
| `AUTH_EXTERNAL_RATE_LIMIT` | bổ sung, giới hạn theo IP cho endpoint công khai gọi ra ngoài | |

## A6. Danh sách ca test (từ `*.test.ts`)

Quy ước: `[GIỮ]` port nguyên; `[SỬA]` port có điều chỉnh (ghi lý do); `[BỎ]` không port. Test Medplum mock `fetch`; Go dùng fake cổng hoặc `httptest.Server` với `AllowUnsafeOutbound=true` **chỉ trong test bộ nối** (kèm thêm ca SSRF thật ở mục A7).

### A6.1 `GET /auth/external` (`auth/external.test.ts`)

| Tên ca (dòng) | Mô tả | Kỳ vọng |
|---|---|---|
| Missing code (132) | `code=` rỗng | 400 `Missing code` [GIỮ] |
| Missing state (138) | `state=` rỗng | 400 `Missing state` [GIỮ] |
| Invalid JSON state (144) | `state=xyz` | 400 `Invalid state` [GIỮ] |
| Unknown domain (150) | domain không có cấu hình | 400 `Identity provider not found` [GIỮ] |
| Missing identity provider (162) | domain có cấu hình nhưng không có IdP | 400 `Identity provider not found` [GIỮ] |
| Unknown user (174) | email chưa có user | 400 `User not found` [GIỮ] |
| Missing email (190) | id_token không có email | 400 `External token does not contain email address` [GIỮ] |
| Email does not match domain (206) | email khác domain trong state | 400 `Email address does not match domain` [GIỮ] |
| Email with comma is not a list of users (222, 3 giá trị) | email dạng danh sách/quoted | 400 `User not found` [GIỮ] |
| Domain with comma is not a list of domains (242) | `domain: "example.com,<domain>"` | 400 `Identity provider not found` [GIỮ] |
| DomainConfiguration success (258) | luồng domain thành công | 302 tới `<appBase>/signin?login=...` [GIỮ] |
| Marks the user email verified (281) | user mời chưa verified | sau callback `emailVerified=true` [GIỮ] |
| Does not rewrite an already verified user (310) | gọi hai lần | `versionId` không đổi lần hai [GIỮ, cần patch idempotent ở `fhirstore`] |
| Server config identity provider success (346) | `state.issuer` + cấu hình máy chủ | 302 [GIỮ] |
| Callback iss parameter must match identity provider issuer (374) | `iss` sai/đúng | 400 `Invalid issuer` / 302 [GIỮ] |
| ClientApplication success (397) | `clientId`+`redirectUri` | 302 tới redirectUri kèm `code` [GIỮ] |
| ClientApplication with DomainConfiguration success (416) | domain+client | 302 [GIỮ] |
| Login is scoped to the client project across tenants (435) | user thuộc project khác | 400 `User not found`, không cấp code [GIỮ] |
| id_token verification rejects invalid tokens (471) | thiếu id_token; jwks không issuer; token không ký | 400 `Missing id_token...`; `Missing issuer...`; `Failed to verify code - check ...` [GIỮ] |
| id_token that verifies against the JWKS is accepted (515) | ES256, aud đúng/sai | 302 / 400 [GIỮ; thêm ca `alg` không cho phép] |
| Invalid client (571) | client không có IdP | 400 `Identity provider not found` [GIỮ] |
| Client callback path success (586) | `/auth/external/{clientId}` | 302; `redirect_uri` gửi IdP là `<base>auth/external/<id>` [GIỮ] |
| Shared callback path logs client identity provider sign-ins (609) | callback chung + IdP của client | 302 + log cảnh báo [SỬA: bắt log bằng handler test] |
| Shared callback path requires the legacyExternalAuth system setting (626) | project không bật; admin đặt vào `setting` vô hiệu | 400 `Client identity provider must use the per-client callback ...`, không gọi mạng [GIỮ] |
| Shared callback path allows client identity providers without a project (664) | client không `meta.project` | 302 [GIỮ nếu giữ client không project] |
| Client callback path requires the state client (686) | `state.clientId` khác path | 400 `Invalid client`, không gọi mạng [GIỮ] |
| Client callback path does not fall back to other identity providers (699) | client path không có IdP, state có domain | 400 `Identity provider not found`, không gọi mạng [GIỮ] |
| Missing token URL (712) | thiếu `tokenUrl` | 400 `Missing token URL for external identity provider` [GIỮ] |
| Client secret post requires client credentials (732) | `client_secret_post` thiếu id/secret | 400 `Missing client ID or client secret for external identity provider` [GIỮ; thêm ca nhánh Basic thiếu credential] |
| Invalid project (754) | `state.projectId` khác project của client | 400 `Invalid project` [GIỮ] |
| Invalid redirect URI (769) | redirect lạ | 400 `Invalid redirect URI` [GIỮ; Go kiểm trước `tryLogin`, thêm khẳng định "không tạo Login"] |
| Invalid token request (784) | IdP trả body không phải JSON | 400 `Failed to verify code - check ...` [GIỮ] |
| Token request includes Accept-Encoding: identity (799) | kiểm header gửi IdP | `Accept-Encoding: identity` [SỬA: Go đặt `DisableCompression` và header] |
| Insecure token URL is passed to fetch (822) | `tokenUrl=http://localhost:8080` được gọi | [SỬA: đảo lại — mặc định phải **từ chối** (SSRF); chỉ cho khi `AllowUnsafeOutbound`] |
| Subject auth success (851) | `identitySource=subject`, tra theo `membership.externalId`, PKCE `plain` | 302; token endpoint trả profile `External User` [GIỮ] |
| Block partial redirect URI match (904) | `redirectUri + '/extra'` | 400 `Invalid redirect URI` [GIỮ] |
| Block redirect URI with different host (944) | `redirectUri + '.evil.com'` | 400 `Invalid redirect URI` [GIỮ] |
| Missing subject (984) | `useSubject` nhưng không `sub` | 400 `External token does not contain subject` [GIỮ] |
| Client secret post (1024) | `client_secret_post` | 302, token endpoint trả profile [GIỮ] |
| returnTo URL is followed when explicitly allowed (1077) | `allowedPostLoginRedirectUrls` | 302 tới `returnTo` [GIỮ] |
| returnTo URL is ignored when not in allowedPostLoginRedirectUrls (1119) | returnTo lạ | 302 tới `/signin` [GIỮ] |
| returnTo URL with confused domain is rejected (1138) | `myapp.example.com.evil.com` | 302 tới `/signin` [GIỮ] |
| Legacy User.externalId support (1183) | tra theo `User.externalId` | 302 [SỬA: giữ **chỉ** nếu cần di trú dữ liệu cũ; mặc định `[BỎ]`, xem A8] |

### A6.2 `POST /auth/google` (`auth/google.test.ts`)

| Tên ca (dòng) | Kỳ vọng |
|---|---|
| Missing client ID (56) | 400 `Missing googleClientId` [GIỮ] |
| Invalid client ID (69) | 400 `Invalid googleClientId` [GIỮ] |
| Missing googleCredential (82) | 400 `Missing googleCredential` [GIỮ] |
| Verification failed (92) | 400, thông điệp từ verifier [SỬA: fake `GoogleVerifier` trả lỗi] |
| Success (102) | 200 có `code` [GIỮ] |
| Do not create user (114) | 400, user không được tạo [GIỮ] |
| Register disabled (129) | 400 `Registration is disabled` [GIỮ] |
| Create new user account (148) | 200 `{login}` không `code`; user được tạo [GIỮ] |
| Create new user for new project (166) | 200 `{login}`, không `emailVerificationRequired` [GIỮ] |
| Existing user for new project (186) | 200 `{login}` [GIỮ] |
| Require email verification when Google has not verified email (211) | `emailVerificationRequired:true`; `user.emailVerified=false` [GIỮ] |
| Skip email verification when Google verified email (232) | user `emailVerified=true`, không yêu cầu [GIỮ] |
| Skip email verification for verified user (253) | [GIỮ] |
| Verify existing user when Google verified email (278) | nâng lên `true` [GIỮ] |
| Do not un-verify a user when Google reports unverified (306) | giữ `true` [GIỮ] |
| Skip email verification on sign in (334) | đăng nhập không gửi lại link [GIỮ] |
| Skip email verification for existing project (358) | [GIỮ] |
| Require Google auth (389) | project có `google-auth-required`, đăng nhập Google thành công [GIỮ] |
| Google auth profile picture (423) | `photo[0].url` được đặt [GIỮ] |
| Custom Google client success (466) | `site.googleClientId` tùy biến [GIỮ] |
| Multiple projects same Google client success (507) | `{login, memberships(2)}` [GIỮ] |
| Custom Google client with project success (553) | 200 [GIỮ] |
| Custom Google client wrong projectId (595) | 400 `Invalid googleClientId` [GIỮ] |
| Custom OAuth client success (637) | `clientId`+`projectId` đúng, có `code` [GIỮ] |
| ClientId with incorrect projectId (664) | 400 `Invalid projectId` [GIỮ] |
| (mới) Credential `email_verified=false` đăng nhập user có sẵn | theo quyết định Q2 [THÊM] |
| (mới) Credential thiếu claim `email` | 400 (không 500) [THÊM] |

### A6.3 `POST /auth/exchange` (`auth/exchange.test.ts`) và grant token-exchange (`oauth/token.test.ts`)

| Tên ca (dòng) | Kỳ vọng |
|---|---|
| exchange: Missing externalAccessToken (132) | 400 OperationOutcome `Missing externalAccessToken` [GIỮ] |
| exchange: Missing clientId (141) | 400 `Missing clientId` [GIỮ] |
| exchange: Missing identity provider (150) | 400 OAuth `error_description='Invalid client'` [GIỮ] |
| exchange: Unknown user (159) | 400 OperationOutcome `User not found` [GIỮ] |
| exchange: ClientApplication success (170) | 200 `access_token` [GIỮ] |
| exchange: Marks the user email verified (181) | `emailVerified=true` [GIỮ] |
| exchange: Server external auth provider success (210) | IdP từ cấu hình máy chủ, 200 [GIỮ] |
| exchange: GCIP success (225) | gọi `identitytoolkit...accounts:lookup?key=...` POST `{idToken}`; 200 [GIỮ] |
| exchange: Missing projectId success (250) | 200 [GIỮ] |
| exchange: Invalid token request (261) | content-type `text/plain` → 400 `invalid_request`, `Failed to verify code - unsupported content type: text/plain` [GIỮ] |
| exchange: Subject auth success (273) | 200 [GIỮ] |
| exchange: Identity source and mapping mode are honored without useSubject (284) | `sub` thắng `email`; thiếu `sub` → 400 `External token does not contain subject` [GIỮ] |
| exchange: Unsupported identity provider configuration (310, 3 cấu hình) | 400 `Unsupported identity provider configuration` [GIỮ] |
| exchange: GCIP subject auth success (347) | 200 [GIỮ] |
| exchange: GCIP missing localId (358) | 400 `Failed to verify code - missing localId in user info response` [GIỮ] |
| token: Token exchange JSON success (2246) | 200 [GIỮ] |
| token: Token exchange JWT success (2259) | userinfo `application/jwt` không ký, 200 [SỬA/Q7: cân nhắc từ chối JWT không ký] |
| token: selects server provider by client ID (2274) | gọi `userInfoUrl` cấu hình, `expires_in=3600`, `aud=issuer`, không `client_id` claim [GIỮ] |
| token: defaults selector to identity provider client ID (2303) | [GIỮ] |
| token: server provider with user info URL only (2327) | [GIỮ] |
| token: rejects identity provider without user info URL (2349) | 400 `Missing user info URL`, không gọi mạng [GIỮ] |
| token: rejects unknown client ID (2372) | 400 `Invalid client`, không gọi mạng [GIỮ] |
| token: rejects client without IdP or server config match (2385) | 400 `Invalid client` [GIỮ] |
| token: preserves client IdP fallback with multiple server providers (2409) | [GIỮ] |
| token: membership ID derives project across client project boundary (2434) | `project.reference` theo membership [GIỮ] |
| token: rejects unknown membership ID (2463) | 400 `Invalid membership`, không gọi mạng [GIỮ] |
| token: rejects membership without project (2481) | 400 `Invalid membership` [GIỮ] |
| token: GCIP success / subject success (2509, 2545) | 200 [GIỮ] |
| token: GCIP invalid response (2567) | 400 `Failed to verify code - invalid user info response` [GIỮ] |
| token: GCIP missing localId (2580) | 400 [GIỮ] |
| token: GCIP missing API key (2593) | 400 `Missing user info API key - check your identity provider configuration`, không gọi mạng [GIỮ] |
| token: unsupported content type (2605) | 400 [GIỮ] |
| token: Too many requests (2619) | IdP 429 → 429 `invalid_request` `Too Many Requests` [GIỮ] |
| token: missing client ID (2633) | 400 `Invalid client` [GIỮ] |
| token: missing subject token (2645) | 400 `Invalid subject_token` [GIỮ] |
| token: unknown subject token type (2657) | 400 `Invalid subject_token_type` [GIỮ] |
| token: invalid external URL (2669) | fetch lỗi → 400 `Failed to verify code - check ...` [GIỮ] |
| (mới) exchange không `membership_id`, user có nhiều membership, IdP máy chủ | theo Q5 [THÊM] |

### A6.4 `GET /auth/clientinfo`, `POST /auth/method` (`clientinfo.test.ts`, `method.test.ts`)

| Tên ca (dòng) | Kỳ vọng |
|---|---|
| clientinfo: Success with SignInForm (25) | `welcomeString`, `logo.url` đúng [GIỮ] |
| Returns showScopeSelection (32) / Omits when not configured (42) | [GIỮ] |
| Empty object when SignInForm is not configured (48) | `{}` [GIỮ] |
| Signs logo in the same project (55) | URL có `/storage/<id>/` và `Signature=` [SỬA/Q6 nếu không có storage] |
| Does not sign logo in another project (72) | giữ `Binary/<id>` [SỬA/Q6] |
| Registered redirect URI (89) | 200 [GIỮ] |
| Unregistered redirect URI (95) | 400 `Invalid redirect URI` [GIỮ] |
| medplum-cli redirect URI (103) | `http://localhost:9615` 200, URI khác 400 [GIỮ nếu giữ client chuẩn `medplum-cli`] |
| Invalid client (113) | 404 [GIỮ] |
| method: Missing/Empty/Invalid email (25, 30, 35) | 400 [GIỮ] |
| method: Domain config (40) | 200 (tìm thấy / không) [GIỮ] |
| method: Domain config case sensitivity (71) | `@<DOMAIN HOA>` vẫn tìm thấy, 200 [GIỮ] |
| method: authorize url without protocol (95) | 400 khi domain có cấu hình URL hỏng; 200 khi không có [GIỮ] |

### A6.5 `POST /auth/preauthorize` và grant (`preauthorize.test.ts`, `token.test.ts:2849-2998`)

| Tên ca (dòng) | Kỳ vọng |
|---|---|
| Requires auth (42) | 401 [GIỮ] |
| Requires project admin (47) | 403 [GIỮ] |
| Requires clientId (56) | 400 `Client ID is required` [GIỮ] |
| Requires onBehalfOfMembership (66) | 400 `Pre-authorization requires onBehalfOfMembership` [GIỮ] |
| Client not found (76) | 404 [GIỮ] |
| Expire time too short / too long (87, 99) | 400 `expiresIn must be a positive integer not exceeding 604800 seconds` [GIỮ] |
| Success (111) | 200 `{preAuthorizedCode, expiresAt}`, không có `code` [GIỮ] |
| Grants scope within caller scope (153, 6 dòng) | 200; token trả đúng scope [GIỮ] |
| Rejects scope broader than caller scope (183, 5 dòng) | 400 `Invalid scope` [GIỮ] |
| Grants patient scope under user scope only with patient context (200) | Patient 200; không patient 400 [GIỮ] |
| Succeeds when on-behalf-of user cannot read ClientApplication (225) | 200 [GIỮ] |
| Rejects ClientApplication from another project (248) | 404 [GIỮ] |
| grant: missing client_id / missing code / invalid client_id / invalid code (token.test 2849-2888) | `Missing client_id`, `Missing pre-authorized_code`, `invalid_client`, `Invalid pre-authorized_code` [GIỮ] |
| grant: expired (2891) | `invalid_grant` `Pre-authorized code expired` [GIỮ] |
| grant: IP address restriction (2920) | từ IP bị chặn lỗi, từ IP cho phép OK [GIỮ khi P2 có IP rule] |
| grant: success + reuse (2965) | lần 1 200 (`scope=openid`, `expires_in=3600`, không `refresh_token`); lần 2 400 `invalid_grant` `Token already granted` [GIỮ] |

## A7. Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test bắt buộc (Go) |
|---|---|---|
| S1 | **SSRF khi gọi IdP ngoài** (`tokenUrl`, `userInfoUrl`, `jwksUrl`, GCIP): cấu hình IdP do admin project/client nhập (`ClientApplication.identityProvider`, `DomainConfiguration`) nên là dữ liệu không tin cậy | Mỗi URL trỏ tới `127.0.0.1`, `localhost`, `10.x`, `192.168.x`, `169.254.169.254`, `[::1]`, `100.64.x`, hostname resolve ra IP nội bộ (DNS giả), redirect 302 sang IP nội bộ, IPv4-mapped IPv6: đều **bị từ chối trước khi gửi byte nào**; `http://` bị từ chối khi `AllowUnsafeOutbound=false`; body vượt giới hạn bị cắt; timeout |
| S2 | **`state`**: Medplum không ràng buộc phiên (mục 0.6). Kiểm soát tối thiểu bắt buộc: chặn `state` quá lớn; mọi trường trong `state` coi là không tin cậy (`domain`, `issuer`, `clientId`, `redirectUri`, `returnTo`). Khuyến nghị (cần quyết định Q1): ký (HMAC) `state` do server phát hành kèm cookie `SameSite` chống login fixation | Test: `state` giả với `redirectUri` ngoài danh sách bị từ chối; `returnTo` khác scheme/host/port bị bỏ qua; nếu cài state ký: sửa 1 byte → 400, replay sau khi dùng → 400 |
| S3 | **Khớp issuer** | `iss` query sai → 400 `Invalid issuer`; id_token `iss` khác `idp.issuer` (có `jwksUrl`) bị từ chối; id_token `aud` khác `audience ?? clientId` bị từ chối; `alg=none`/HS256 bị từ chối; token hết hạn bị từ chối |
| S4 | **Id_token không ký** (không `jwksUrl`): Medplum chấp nhận parse không kiểm (`external.ts:421`) | Quyết định Q3. Tối thiểu: test ghi rõ hành vi và cảnh báo khi cấu hình IdP không có `jwksUrl`; khuyến nghị đòi `jwksUrl` ở `staging`/`production` |
| S5 | **Khớp domain–email** | `endsWith('@'+domain)` đúng cả khi email hoa/thường (sau khi lowercase); email dạng danh sách không khớp; domain có dấu phẩy không chọn được IdP |
| S6 | **Email chưa verified do IdP báo**: Medplum chỉ cảnh báo rồi đặt `emailVerified=true` | Quyết định Q2. Khuyến nghị: nếu `email_verified` false thì từ chối (`identitySource=email`) hoặc không đặt cờ; test cả hai |
| S7 | **Chiếm tài khoản qua Google**: đăng nhập user có sẵn không kiểm `claims.email_verified` | Test credential `email_verified=false` cho user có sẵn; theo Q2 |
| S8 | **Cách ly project**: `state.projectId` phải khớp project của client; user không có membership trong project của client không đăng nhập được (`external.test.ts:435-469`); `googleClientId` tùy biến chỉ cho project sở hữu | Test chéo project |
| S9 | **Redirect URI**: khớp chính xác; khớp một phần **chỉ** khi `Project.setting.allow-dangerous-redirect` (admin đặt được, cần ghi vào tài liệu rủi ro) | Test `+ '/extra'`, `.evil.com`, khác host, khác scheme, userinfo trong URL (`https://good@evil`) |
| S10 | **Ai được gọi route nào** | Bảng dưới; test ma trận 401/403 |
| S11 | **Mã dùng một lần**: `login.code` (P3) và `preAuthorizedCode` chỉ đổi được một lần; dùng lại thu hồi login | Test `preauthorize` + reuse |
| S12 | **Mã pre-authorized**: chỉ lưu hash, không log; `expiresIn` trần 7 ngày | Test DB không chứa mã thô; log không chứa mã |
| S13 | **Rò rỉ bí mật vào log**: Medplum log nguyên `responseBody` khi IdP lỗi (`external.ts:375`) | Test: log không chứa `code`, `client_secret`, `id_token`, `access_token`, `subject_token`, `userInfoApiKey` |
| S14 | **Khuếch đại ra ngoài**: endpoint công khai (`/auth/external`, `/auth/exchange`, `/auth/google`) kích hoạt gọi mạng | Rate limit theo IP + timeout; test vượt hạn mức → 429 |
| S15 | **`clientSecret`/`userInfoApiKey` của IdP**: lưu trong `identityProvider`, không được lộ qua API đọc của `ClientApplication` cho user thường (Medplum ẩn qua access policy cho `secret`, `retiringSecret`, `default-access-policies.ts:21-24`; **chưa xác nhận** có ẩn `identityProvider.clientSecret`) | Test quyền đọc; đề xuất ẩn thêm `identityProvider.clientSecret`, `userInfoApiKey` |

Ma trận quyền P6:

| Route | Xác thực | Quyền |
|---|---|---|
| `POST /auth/method`, `GET /auth/external[/id]`, `POST /auth/google`, `POST /auth/exchange`, `GET /auth/clientinfo/{id}` | Không | Công khai (bằng chứng: id_token/userinfo/credential của IdP) |
| `POST /oauth2/token` (token-exchange, pre-authorized_code) | Không (client chỉ cần `client_id`; không gửi `client_secret`) | Bằng chứng: subject token / mã |
| `POST /auth/preauthorize` | Bearer (hoặc Basic của client) | Admin của project, **không phải** super admin; header `X-Medplum-On-Behalf-Of` bắt buộc |

## A8. Phần Medplum có nhưng KHÔNG nên port đợt này; điểm lệch chấp nhận được; rủi ro; câu hỏi mở

### A8.1 Không port

| Phần | Lý do |
|---|---|
| Bearer IdP ngoài gọi thẳng FHIR API (`tryExternalAuth*`, `getExternalBearerClient`, `isClientSubjectAllowed`, cache Redis `medplum:ext-auth:*`, `S/oauth/utils.ts:1439-1704`) | Là xác thực của resource server, không phải `auth`; cần cache và tra membership theo `fhirUser`/`sub`. Nếu cần, làm ở `pkg/fhirauth`. Ghi nhận nhu cầu ở Q8 |
| `client_assertion`/`verifyMultipleMatchingException` | P3 |
| `Login.cookie` và cookie `medplum-<clientId>` (`sendLoginCookie`) | Thuộc luồng `authorize` (P3); P6 không đặt cookie (external callback không gọi `sendLoginCookie`, `external.ts:140-214`) |
| `User.externalId` (deprecated) tra cứu dự phòng | Chỉ cần nếu có dữ liệu di trú; ngoài ra bỏ (`utils.ts:884-896`) |
| `Project.exportedResourceType`/`link` chia sẻ tài nguyên | Không liên quan P6 |
| Chế độ `gcip` | Giữ vì có test; nếu không dùng GCIP có thể hoãn (`[HOÃN]`, quyết định Q9) |

### A8.2 Điểm lệch chấp nhận được

- Bắt buộc HTTPS và chặn IP nội bộ cho mọi URL IdP; không có công tắc "unsafe" ngoài dev/test.
- Giới hạn `state` 8 KiB; giới hạn kích thước phản hồi IdP; timeout (Medplum không có).
- Lowercase `domain` ở `/auth/method` (khắc phục A3.2(e)).
- Kiểm redirect URI trước khi tạo `Login`/ghi `emailVerified` (A3.2(a)).
- Thông điệp lỗi giữ nguyên chuỗi Medplum để khớp client; riêng lỗi từ thư viện JWT dùng tập thông điệp cố định.

### A8.3 Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Login fixation/CSRF ở callback (mục 0.6) | Quyết định Q1 trước khi mở ra ngoài |
| Tài khoản bị chiếm qua IdP không xác minh email (S6/S7) | Q2 |
| PKCE phía IdP thực chất `plain` (mục 0.7), tương thích IdP thực tế chưa kiểm | Thử với IdP thật (Keycloak/Google) ở môi trường dev trước khi chốt |
| Cấu hình IdP do admin nhập là bề mặt SSRF | A5.2, S1, kèm P7 chặn `identityProvider` có URL nguy hiểm ngay khi lưu (bổ sung) |
| Rác `Login` từ các lần đăng nhập thất bại (`tryLogin` tạo trước khi kiểm membership) | Job purge `Login` (P7 `purge`/CLI); hoặc Go tạo Login sau khi kiểm membership (lệch được, xem Q10) |
| Phụ thuộc P2/P3 chưa xong (TryLogin, middleware `OnBehalfOf`, token) | Thứ tự bước A10 |

### A8.4 Câu hỏi mở

| # | Câu hỏi |
|---|---|
| Q1 | Có chấp nhận lệch hợp đồng để `/auth/method` phát hành `state` đã ký và gắn cookie (chống login fixation), hay giữ y hệt Medplum (client tự dựng `state`)? Chưa xác nhận `@medplum/react` chịu được `state` do server cấp |
| Q2 | Khi IdP báo `email_verified=false` (callback, exchange) hoặc Google credential `email_verified` không đúng: từ chối, hay giữ hành vi Medplum (cảnh báo, vẫn đăng nhập)? |
| Q3 | Có cho phép IdP không có `jwksUrl` (id_token không kiểm chữ ký) ngoài môi trường dev? |
| Q4 | Lỗi ở callback: giữ JSON `OperationOutcome` hay chuyển hướng về app kèm mã lỗi? |
| Q5 | Trao đổi token qua IdP cấu hình máy chủ không kèm `membership_id`: bắt buộc `membership_id`, hay chọn membership đầu tiên như Medplum? |
| Q6 | `auth` có lưu trữ `Binary` để ký URL logo ở `clientinfo` không? Nếu không, bỏ ký và ghi vào hợp đồng |
| Q7 | Userinfo `application/jwt` không ký có chấp nhận không? (Medplum chấp nhận, `utils.ts:1060-1062`) |
| Q8 | Có cần xác thực Bearer IdP ngoài cho `fhir-server` (workload identity)? Nếu có, thiết kế riêng ngoài `auth` |
| Q9 | Có dùng GCIP không? Nếu không, hoãn `userInfoMode=gcip` |
| Q10 | `Login` tạo trước hay sau kiểm membership? (lệch để tránh rác) |
| Q11 | Subject token có kiểm `aud` không? Medplum chỉ gọi userinfo bằng token đó, **không** kiểm token được cấp cho ứng dụng nào (`token.ts:683-699`); token của ứng dụng khác cùng IdP đổi được. **Chưa xác nhận** biện pháp giảm thiểu ở IdP |

## A9. Tạo ClientApplication / DomainConfiguration cho đến khi có quản trị

P6 cần: `DomainConfiguration` (domain + `identityProvider`) và `ClientApplication.identityProvider`. Chưa có API admin để tạo chúng (Medplum cũng tạo `DomainConfiguration` qua FHIR API/super admin, `external.test.ts:60-72`, không có route `admin/` riêng). Dùng CLI/seed ở mục B9 mở rộng: `auth domain add --domain <d> --authorize-url ... --token-url ... --user-info-url ... --client-id ... --jwks-url ... --issuer ...` (secret IdP đọc từ env/stdin, không argv). CLI **phải** chạy cùng bộ kiểm URL của S1 (từ chối `http://`, IP nội bộ) ngay khi ghi.

## A10. Cỡ và thứ tự làm (P6: tổng cỡ M đến L)

Mỗi bước là một PR review được. Cỡ: S dưới 1 ngày, M 2 đến 4 ngày, L 1 đến 2 tuần.

| Bước | Việc | Phụ thuộc | Cỡ | Tiêu chí hoàn thành |
|---|---|---|---|---|
| 6.0 | Hạ tầng ra ngoài: `http.Client` an toàn (S1), `OIDCClient`, `IDTokenVerifier` (JWKS cache, giới hạn `alg`), cổng `DomainConfigurations/Clients/Projects/ExternalProviders` + fake; bổ sung `Users.ByExternalID` và nhánh `external/exchange/google` cho `TryLogin` nếu P2 chưa có | P1, P2 | M | Bộ test SSRF (S1) xanh; test `verify id_token` (đúng/sai `iss`, `aud`, `alg`, hết hạn); không gọi mạng thật |
| 6.1 | `POST /auth/method` + `GET /auth/clientinfo/{id}` (A3.11, A3.12) | 6.0 | S | Test A6.4 xanh; swagger khớp; `auth-dto.md` cập nhật |
| 6.2 | `GET /auth/external[/id]` (A3.2 đến A3.5) | 6.0, P2 (`TryLogin`, `SetLoginMembership`) | M | Toàn bộ A6.1 `[GIỮ]`/`[SỬA]` xanh; S2, S3, S5, S8, S9, S13 có test |
| 6.3 | Hàm `ExchangeToken` + `POST /auth/exchange` + grant `token-exchange` trong router P3 (A3.8) | 6.0, P3 (`sendTokenResponse`, router `/oauth2/token`) | M | Toàn bộ A6.3 xanh; sửa `auth-dto.md` (mục 0.1) |
| 6.4 | `POST /auth/google` (A3.7) | 6.0, P2, P4 (`sendVerificationEmail`) | M | A6.2 xanh; S7 có test |
| 6.5 | `POST /auth/preauthorize` + grant `pre-authorized_code` (A3.9) | P3 (middleware xác thực + `X-Medplum-On-Behalf-Of`, `isSmartScopeSubset`) | M | A6.5 xanh |
| 6.6 | E2E: callback với IdP giả (`httptest`) → `code` → `/oauth2/token` → gọi `fhir-server` bằng token | 6.2, P3 | S | Kịch bản `external.test.ts:851-902` chạy end-to-end |

Thứ tự đề xuất: 6.0 → 6.1 → 6.2 → 6.3 → 6.4 → 6.5 → 6.6. Có thể tách 6.5 sang sau P7 nếu chưa có P3 middleware `OnBehalfOf`.

## Tiến độ P6 (cập nhật 2026-11-22)

User duyệt Q1 đến Q11 theo bộ an toàn ("duyệt hết bộ an toàn, bắt đầu P6"). **Xong 6.0 đến 6.6**.

**Mã:**
- 6.0: `infrastructure/jwks` (tải và nhớ JWKS qua `pkg/safehttp`, tách ra từ `clientassertion` nên cả hai dùng chung), `infrastructure/oidc` (`Client.ExchangeCode`, `IDVerifier.VerifyJWKS` chỉ nhận RS*/ES*, bắt buộc iss, aud, exp; cấm `none` và HS*), `domain/external.go` (`ExternalState`, `ResolveExternalIdentity`, `ValidReturnTo`, `MatchRedirectURIPartial`, `BuildAuthorizeURL`, `IsExternalEmailUnverified`), `TryLogin` hỗ trợ `externalId` (`MembershipRepository.FindByExternalID`, khớp chính xác theo `ProjectMembership.externalId` và project) và **tạo Login sau khi kiểm membership** cho mọi phương thức không phải mật khẩu (Q10), `pkg/safehttp.HostBlocked`.
- 6.1: `GET /auth/clientinfo/{clientId}` (`application/clientinfo.go`), `POST /auth/method` phát `state` đã ký kèm cookie (Q1), tên miền trả về chữ thường.
- 6.2: `GET /auth/external` và `GET /auth/external/{clientId}` (`application/external.go`), `application/external_state.go` (HMAC-SHA256, hạn, gắn cookie), `AddDomainConfiguration` và lệnh CLI `auth domain add` (kiểm URL ngay khi lưu), cấu hình `AUTH_EXTERNAL_STATE_KEY`, `AUTH_EXTERNAL_STATE_TTL`, `AUTH_EXTERNAL_AUTH_PROVIDERS`.

**Quyết định áp dụng:** Q1 state ký + cookie `auth-external-state` (HttpOnly, SameSite=Lax, Path `/auth/external`, xóa sau callback); Q2 từ chối email IdP báo chưa xác minh; Q3 bắt buộc `jwksUrl` (và `issuer`) ngoài dev/test; Q4 giữ lỗi OperationOutcome JSON; Q6 không ký URL logo (không lưu Binary); Q8 và Q9 hoãn; Q10 tạo Login sau khi biết có membership; callback kiểm redirect URI TRƯỚC khi tạo Login hay ghi `emailVerified`. Q5 (bắt buộc `membership_id`), Q7 (userinfo JWT chỉ khi kiểm được chữ ký), Q9 (GCIP báo `Unsupported user info mode`), Q11 (kiểm `aud`/`azp`) đã áp dụng ở 6.3.

**Lệch và bổ sung ngoài kế hoạch:**
- `POST /auth/external/start` (mới): với Q1, luồng "callback riêng của client" (IdP gắn vào `ClientApplication`, ánh xạ theo `sub`) không có cách nào lấy được state ký vì `/auth/method` chỉ đi theo tên miền email. Endpoint này phát state và cookie cho client có `identityProvider`.
- `/auth/method` nhận thêm các trường tùy chọn của luồng đăng nhập (`clientId`, `scope`, `nonce`, `codeChallenge`, `redirectUri`, `returnTo`...) và **ghi chúng vào state ký**; app KHÔNG tự thêm `state` vào `authorizeUrl` nữa (client tự dựng state kiểu Medplum không còn dùng được: tương thích với `@medplum/react` chưa kiểm).
- `domain` trong state luôn do server suy ra từ email (app gửi domain khác bị ghi đè).
- `ClientSecret` và `ClientId` của IdP bị bắt buộc ở cả hai kiểu xác thực (Medplum gửi `undefined:undefined` khi thiếu).
- Đăng nhập ngoài chỉ bật khi có `AUTH_APP_BASE_URL`; thiếu `AUTH_EXTERNAL_STATE_KEY` ngoài staging/production thì dùng khóa tạm trong bộ nhớ (chỉ một instance); staging/production bắt buộc có khóa khi có `AUTH_APP_BASE_URL`.
- Callback và `/auth/external/start` dùng chung hạn mức của bước nhập thông tin đăng nhập (mỗi lần gọi làm auth gọi ra IdP).
- `AUTH_ALLOW_PRIVATE_JWKS` (có từ P3) nay cũng cho phép IdP http/nội bộ và id_token không `jwksUrl`: chỉ dev/test (tên biến chưa đổi).

**Đột biến:** khoảng 150 ca trên mã P6; mọi ca bị bắt sau khi thêm test, trừ các ca tương đương (kiểm tra trùng lặp có chủ ý): `state` với cookie rỗng (so sánh HMAC vẫn lệch), tiền tố miền của MAC, `usePkce` không kèm challenge (verifier rỗng như nhau), hạ chữ thường email ở `externalIdP` (đã hạ trước), hai bước kiểm lại `externalId` và project ở Go sau khi tìm kiếm đã lọc.

**Test:** `domain/external_test.go`, `infrastructure/oidc` (IdP và JWKS bằng `httptest`, kiểm cả tấn công đổi thuật toán HS256 và `alg none`, xoay khóa), `application/external_test.go` (state, chọn IdP, danh tính, project, redirect, `AddDomainConfiguration`), `bootstrap/external_http_test.go` (IdP thật bằng `httptest`: `/auth/method` → callback → đổi code → token), `e2e/external_test.go` (tiến trình thật, gồm `auth domain add`).

**Xong 6.3 và 6.4 (2026-11-22):** `application/external_exchange.go` (`ExchangeToken`, `ExchangeLoginError`, `issueForGrantedLogin`), `OIDCClient.UserInfo` (`infrastructure/oidc`), `domain.SubjectTokenAudienceOK`, grant `token-exchange` + `POST /auth/exchange`; `application/google.go` (`GoogleLogin`), `infrastructure/googleid` (JWKS Google, chỉ RS256), `ProjectRepository.ByGoogleClientID`, `createUserLocked` tách từ `CreateUser`, `POST /auth/google` (bật bằng `AUTH_GOOGLE_ENABLED` hoặc `AUTH_GOOGLE_CLIENT_ID`; `AUTH_GOOGLE_JWKS_URL` chỉ cho test). Test: application (memory/SQLite/PG), adapter oidc/googleid, domain, HTTP (`bootstrap/exchange_google_http_test.go`). Chưa port: cập nhật ảnh đại diện từ Google (`googleAuthProfilePictures`); bước gửi thư xác minh (A3.7 bước 9) không cần vì Q2 bắt buộc email đã xác minh.

**Xong 6.5 và 6.6 (2026-11-22):** `application/preauthorize.go` (`PreAuthorize`, `resolveOnBehalfOf`), `MembershipRepository.FindByProfile`, `POST /auth/preauthorize` (Bearer, header `X-Medplum-On-Behalf-Of`). Giữ hành vi Medplum: admin không phải super admin, scope mặc định `openid` + SMART của người gọi, người gọi bị giới hạn SMART chỉ mint trong phạm vi của mình, `patient/` chỉ khi đích là Patient, client phải thuộc project (404). Header on-behalf-of mọi thất bại là 403 chung (membership project khác không bị lộ). Test: application (memory/SQLite/PG), HTTP (`bootstrap/preauthorize_http_test.go`, gồm cả thu hồi token khi mã bị dùng lại), e2e hai tiến trình `e2e/exchange_google_preauth_test.go` (token-exchange, Google, preauthorize, mỗi luồng đều gọi `fhir-server` bằng token nhận được). Đột biến: mọi mutant của `preauthorize.go` bị bắt; một mutant sống sót ở bước lọc lại `profile` trong `store.FindByProfile` (tương đương: điều kiện tìm kiếm đã lọc, giữ làm phòng thủ).

**P6 xong.** Còn lại của auth: P7 (`/admin/*` HTTP, SCIM; câu hỏi B8.4 chưa duyệt).

---

# PHẦN B. P7 — Quản trị project (`/admin/*`, tùy chọn)

## B1. Mục tiêu và phạm vi

Mục tiêu: cho project admin quản trị tài khoản và cấu hình của **chính project mình** (tạo client OAuth, mời user, đặt/reset mật khẩu, quản lý member, lưu `setting`/`secret`/`site`), theo hành vi Medplum. Nhóm super admin (`/admin/super/*`) phần lớn là thao tác hạ tầng CSDL/FHIR và **không thuộc** `auth`.

| Nhóm | Số route | Đề xuất |
|---|---|---|
| `/admin/projects/*` | 13 | Làm theo phân loại B3 |
| `/admin/super/*` | 19 (`super.ts`: `valuesets`, `structuredefinitions`, `searchparameters`, `reindex`, `lambda-cleaner`, `setpassword`, `purge`, `removebotidjobsfromqueue`, `rebuildprojectid`, `migrations`, `migrate`, `reconcile-db-schema-drift`, `rebuild-index`, `drop-invalid-indexes`, `setdataversion`, `setschemaversion`, `tablesettings`, `vacuum`, `reloadcron`) | **Không làm** (B3.2). Chỉ `setpassword` và `purge` (`Login`) liên quan danh tính, thay bằng CLI |

(`admin.md` liệt kê đủ 19 route super; bảng B3.2 liệt kê hết.)

## B2. Bảng ánh xạ source → Go

| Source (`S/admin/...`) | Hành vi | Đích Go (đề xuất) |
|---|---|---|
| `routes.ts:1-9` | gắn `projects/` và `super/` | `presentation/httpapi/admin.go` (nhóm route) |
| `utils.ts:13-20` `verifyProjectAdmin` | cho phép khi `project.superAdmin` hoặc `membership.admin`, ngược lại 403 | middleware `RequireProjectAdmin` (presentation) gọi `domain/access.IsProjectAdmin(ctx)` |
| `project.ts:25-27` | `authenticateRequest` + `verifyProjectAdmin` cho **mọi** route project | nhóm route Echo với 2 middleware |
| `project.ts:31-62` `POST /setpassword` | đặt mật khẩu cho user **thuộc project** | `application/admin.SetUserPassword` |
| `project.ts:64-66`, `bot.ts`, `client.ts`, `invite.ts` | tạo bot/client/mời | `application/admin.{CreateBot,CreateClient,InviteUser}` |
| `project.ts:72-83` | đọc project | `application/admin.GetProject` |
| `project.ts:85-112` | ghi `setting`/`secret`/`site` | `application/admin.ReplaceProjectSettings/Secrets/Sites` |
| `project.ts:114-189` | member: đọc/sửa/xóa | `application/admin.{GetMember,UpdateMember,DeleteMember}` |
| `project.ts:206-272` `mfa/reset` | reset MFA | `application/admin.ResetMemberMFA` (cần P5) |
| `project.ts:280-327` `resetpassword` | gửi mail đặt lại mật khẩu | `application/admin.SendMemberPasswordReset` (cần P4 `resetPassword`, `Mailer`) |
| `super.ts:*` | thao tác hạ tầng | xem B3.2 |

Phụ thuộc: P3 (middleware xác thực Bearer **và Basic** `client_id:secret`, `S/oauth/middleware.ts:59-79`), P2 (membership, login), P4 (`setPassword`, `resetPassword`, `securityrequest`, `Mailer`, pwned), P5 (MFA). Pha P7 **không thể** làm trước P3.

## B3. Bảng đầy đủ mọi route admin (đã đối chiếu source)

Chung cho nhóm `projects`: xác thực bằng `Authorization: Bearer <access_token>` **hoặc** `Basic base64(client_id:client_secret)` (client được nâng `admin` vẫn gọi được, test `invite.test.ts:568-622`); chưa xác thực → 401 kèm `WWW-Authenticate`; không phải admin/super admin → 403 (`utils.ts:13-20`). Với tham số `:projectId` trong URL: **middleware `authenticateRequest` không kiểm** `:projectId` ở nhánh `/admin` (hàm `getProjectIdFromUrl` chỉ nhận `/projects/...` ở đầu đường dẫn hoặc sau `/api`; `/admin/projects/<id>` có `admin` ở đầu nên trả `undefined`, `S/util/url.ts:25-28`, `S/oauth/middleware.ts:45-49`). Hệ quả dùng `:projectId` khác nhau theo route (cột "Dùng `:projectId`").

### B3.1 `/admin/projects/*`

| Method và path | Dùng `:projectId` | Request (kiểu, bắt buộc, ràng buộc) | Response | Lỗi |
|---|---|---|---|---|
| `POST /admin/projects/setpassword` (`project.ts:31-62`) | Không (dùng `ctx.project.id`) | JSON: `email` (string, bắt buộc, email hợp lệ; thông điệp `Valid email address is required`); `password` (string, bắt buộc, ≥ 8 ký tự `Password must be at least 8 characters`, ≤ 72 byte `Password must be no more than 72 characters`) | `200` `allOk` | `400` validator; `400 User not found` (không có user với `email` **và** `User.project = project của caller`; user server-scope cũng không tìm thấy: `project.test.ts:681-716`); `400 Password found in breach database`; `403` không admin. Tác dụng: băm bcrypt, vô hiệu mọi `UserSecurityRequest` mật khẩu cũ, **thu hồi mọi `Login` của user** (`S/auth/setpassword.ts:83-120`) |
| `POST /admin/projects/:projectId/bot` (`bot.ts:11-25`) | Có: `projectId == ctx.project.id` hoặc super admin | JSON `BotInitParameters`: `name` (bắt buộc, không rỗng `Bot name is required`); tùy chọn `description`, `accessPolicy` (Reference), `runtimeVersion` (`awslambda` \| `vmcontext`), `sourceCode`, `executableCode` (Attachment) (`botinit.ts:48-55`; trường lạ không bị chặn ở route) | `201` `Bot`; sinh `Binary` mã nguồn mặc định, `ProjectMembership` cho bot với `accessPolicy ?? default 'Bot'`; nếu có `executableCode` thì triển khai bot | `400` thiếu `name`; `403` project khác; `404` project không tồn tại (super admin) |
| `POST /admin/projects/:projectId/client` (`client.ts:18-66`) | Có (như bot) | JSON: `name` (bắt buộc `Client name is required`); còn lại là **bất kỳ trường `ClientApplication`** (`description`, `redirectUris`, `identityProvider`, `pkceOptional`, `grantType`, `secret`, `signInForm`, `status`, `meta.tag`...) cộng `accessPolicy` (Reference) tùy chọn | `201` `ClientApplication` (kèm `secret`: 64 ký tự hex do `generateSecret(32)`, hoặc đúng `secret` caller gửi, `client.test.ts:155-174`); đồng thời tạo `ProjectMembership{project, user=client, profile=client, accessPolicy ?? default 'ClientApplication'}` | `400` thiếu `name`; `403` project khác; ghi đè `meta.project`, `resourceType`, `meta.author` **không thành công** (`client.test.ts:107-153`); `meta.tag` giữ |
| `POST /admin/projects/:projectId/invite` (`invite.ts:50-193`) | Có: super admin đọc project theo `:projectId` (404 nếu không có); admin thường luôn dùng `ctx.project` (bỏ qua `:projectId`) | JSON `InviteRequest` (xem bảng B3.3) | `200` body là `ProjectMembership` (`sendFhirResponse(allOk, membership)`) | xem B3.3 |
| `GET /admin/projects/:projectId` (`project.ts:72-83`) | Không (dùng `ctx.project`) | không | `200` `{project:{id, name, setting, secret, site}}` (**`secret` trả nguyên văn** cho admin) | `403` không admin |
| `POST /admin/projects/:projectId/settings` (`project.ts:85-93`) | Không | body là **mảng** `ProjectSetting` (`name`, `valueString`/`valueBoolean`/`valueDecimal`/`valueInteger`); **thay thế toàn bộ** `Project.setting`; không có validator ở route | `200` `Project` đã cập nhật; `meta.author` = profile của caller (`project.test.ts:521-561`) | `403`; sai kiểu → lỗi validate của repo (**chưa xác nhận** mã) |
| `POST /admin/projects/:projectId/secrets` (`project.ts:95-103`) | Không | mảng `ProjectSetting`; thay toàn bộ `Project.secret` | `200` `Project` | như trên |
| `POST /admin/projects/:projectId/sites` (`project.ts:105-112`) | Không | mảng `ProjectSite` (`name`, `domain[]`, `googleClientId`, `googleClientSecret`, `recaptchaSiteKey`, `recaptchaSecretKey`); thay toàn bộ `Project.site` | `200` `Project` | như trên |
| `GET /admin/projects/:projectId/members/:membershipId` (`project.ts:114-123`) | Không (so với `ctx.project`) | không | `200` `ProjectMembership` | `403` nếu `membership.project != ctx.project`; `404` không tồn tại |
| `POST /admin/projects/:projectId/members/:membershipId` (`project.ts:125-140`) | Không | body là **toàn bộ** `ProjectMembership` với `resourceType='ProjectMembership'` và `id == :membershipId` | `200` membership đã cập nhật (`meta.author` = caller) | `403` membership ngoài project; `403` `resourceType` khác hoặc `id` khác (`project.test.ts:49-146`); không có kiểm bất biến `user`/`profile`/`project`: **chưa xác nhận** repo có chặn (B7) |
| `DELETE /admin/projects/:projectId/members/:membershipId` (`project.ts:142-189`) | Không | không | `200` `allOk` | `403` ngoài project; `400 Cannot delete the owner of the project` (khi `project.owner == membership.user`); `404` không tồn tại; **nếu `User.project == project` và đây là membership duy nhất thì xóa luôn `User`** (transaction), user server-scope thì chỉ xóa membership (`project.test.ts:310-519`) |
| `POST /admin/projects/:projectId/members/:membershipId/mfa/reset` (`project.ts:206-272`) | Không | tùy chọn `method` ∈ {`totp`, `email`}, mặc định `totp` (`Method must be "totp" or "email"`) | `200` `allOk`; cập nhật `User.mfaEnrolled`, `mfaMethod` (bỏ method), **đổi `mfaSecret`** nếu `totp`; gửi mail báo | `400` validator; `403/404` membership ngoài project (test `project.test.ts:827-870` kỳ vọng **404** vì repo ẩn; Go phải thống nhất mã); `403` nếu `User.project != project` (user server-scope **không** reset được, `project.test.ts:1152-1183`); `400 User is not enrolled in MFA method: <method>`; **không** kiểm "không được gỡ yếu tố cuối khi MFA bắt buộc" |
| `POST /admin/projects/:projectId/members/:membershipId/resetpassword` (`project.ts:280-327`) | Không | không | `200` `allOk`; tạo `UserSecurityRequest` loại `reset` và gửi mail chứa đường dẫn | `403` membership ngoài project hoặc `User.project != project`; `400 User does not have an email address`; `403` không admin |

### B3.2 `/admin/super/*` (toàn bộ 19 route, `S/admin/super.ts`)

Cả nhóm: `authenticateRequest` (`super.ts:104`) + `requireSuperAdmin()` trong từng handler (`ctx.repo.isSuperAdmin()`), không phải super admin → 403. Hầu hết yêu cầu header `Prefer: respond-async` (400 `Operation requires "Prefer: respond-async"`).

| Method và path | Việc làm | Validator chính | Loại | Đề xuất cho `auth` |
|---|---|---|---|---|
| `POST /valuesets` (`109`) | dựng lại bảng terminology | `shardId` | hạ tầng FHIR | **Bỏ** (thuộc `fhir-server`) |
| `POST /structuredefinitions` (`123`) | dựng lại bảng StructureDefinition | `shardId` | hạ tầng FHIR | Bỏ |
| `POST /searchparameters` (`137`) | dựng lại bảng SearchParameter | `shardId` | hạ tầng FHIR | Bỏ |
| `POST /reindex` (`151`) | reindex theo loại resource | `reindexType ∈ {outdated,all,specific}`, `maxResourceVersion`, `batchSize 100..1000`..., `maxIterationAttempts 1..20` | hạ tầng FHIR | Bỏ |
| `POST /lambda-cleaner` (`286`) | dọn phiên bản Lambda cũ | `keepLatest 1..10`, `deleteConcurrency 1..5`, `dryRun` | hạ tầng AWS | Bỏ |
| `POST /setpassword` (`339`) | đặt mật khẩu cho **bất kỳ** user (`email`, tùy chọn `projectId`) | `email`, `password` 8..72 | **danh tính** | Thay bằng CLI `auth user set-password` (B9); không cần HTTP |
| `POST /purge` (`368`) | xóa cũ `AuditEvent`/`Login` trước `before` | `resourceType ∈ {AuditEvent, Login}`, `before` ISO8601 | **danh tính** (Login) | CLI/Job `auth purge-logins --before` (xử lý rác Login, A8.3) |
| `POST /removebotidjobsfromqueue` (`387`) | gỡ job bot khỏi queue | `botId` | hạ tầng bot | Bỏ |
| `POST /rebuildprojectid` (`404`) | cập nhật cột `projectId` | `shardId` | hạ tầng CSDL | Bỏ |
| `GET /migrations` (`422`) | liệt kê migration | `shardId` (query) | hạ tầng CSDL | Bỏ (Go có `auth migrate status`) |
| `POST /migrate` (`440`) | chạy migration dữ liệu | `dataVersion` | hạ tầng CSDL | Bỏ |
| `POST /reconcile-db-schema-drift` (`465`) | đối soát schema | `shardId` | hạ tầng CSDL | Bỏ |
| `POST /rebuild-index` (`498`) | rebuild index PG | `targets[1..10]`, định danh PG hợp lệ | hạ tầng CSDL | Bỏ |
| `POST /drop-invalid-indexes` (`568`) | xóa index lỗi | `targets[{schema,index}]` | hạ tầng CSDL | Bỏ |
| `POST /setdataversion` (`634`) | đặt version dữ liệu (nguy hiểm) | `dataVersion` | hạ tầng CSDL | Bỏ |
| `POST /setschemaversion` (`658`) | đặt version schema | `schemaVersion` | hạ tầng CSDL | Bỏ |
| `POST /tablesettings` (`689`) | `ALTER TABLE ... SET (...)` | `tableName`, `settings` | hạ tầng CSDL | Bỏ |
| `POST /vacuum` (`755`) | `VACUUM`/`ANALYZE` | `tableNames[]`, `analyze`, `vacuum` | hạ tầng CSDL | Bỏ |
| `POST /reloadcron` (`814`) | nạp lại cron bot | | hạ tầng bot | Bỏ |

### B3.3 `POST /admin/projects/:projectId/invite` — DTO và thuật toán

Request (`S/admin/invite.ts:50-71`, kiểu `InviteRequest` ở `packages/core/src/client.ts:612-637`):

| Trường | Kiểu | Bắt buộc | Ràng buộc / ý nghĩa |
|---|---|---|---|
| `resourceType` | `Patient` \| `Practitioner` \| `RelatedPerson` | có | thông điệp `Resource type is required` |
| `firstName`, `lastName` | string | có | không rỗng (`First name is required`, `Last name is required`) |
| `email` | string | một trong `email`/`externalId` | email hợp lệ (`Valid email address is required`); lưu **lowercase** (`invite.ts:102-104`, test `746-782`) |
| `externalId` | string | một trong hai | không rỗng (`External ID cannot be empty`); lỗi chung `Either email or externalId is required` |
| `patient` | `Reference<Patient>` | RelatedPerson mới | `patient.reference` khớp `^Patient/[^/]+$` (`Patient must be a reference to a Patient resource`) |
| `password` | string | không | 8..72 byte; mặc định mật khẩu ngẫu nhiên `generateSecret(16)` (người dùng không biết, phải đặt qua link) |
| `scope` | `project` \| `server` | không | Patient mặc định `project`, Practitioner/RelatedPerson mặc định `server`; có `externalId` luôn `project` (`invite.ts:232-238`) |
| `sendEmail` | boolean | không | `false` thì không gửi mail |
| `membership` | `Partial<ProjectMembership>` | không | trải sau cùng vào membership (ghi đè `admin`, `accessPolicy`...) |
| `accessPolicy`, `access`, `admin` | | không | **deprecated**, thay bằng `membership.*` |
| `upsert` | boolean | không | cập nhật membership có sẵn |
| `forceNewMembership` | boolean | không | luôn tạo membership mới (bỏ kiểm trùng) |
| `mfaRequired` | boolean | không | ghi vào `User.mfaRequired` |

Không có validator cho `scope`, `sendEmail`, `membership`, `upsert`, `forceNewMembership`, `admin`, `accessPolicy`, `access`, `mfaRequired`: Go cần DTO có kiểu chặt (kiểu sai → 400).

Các bước (`invite.ts:73-193`, `226-574`):
1. Chọn project: super admin → đọc theo `:projectId` (404 nếu không có); khác → `ctx.project`.
2. Practitioner có `email` và người gọi **không** phải super admin: kiểm miền email: `config.blockedEmailDomains` (luôn chặn, ưu tiên hơn cho phép) và `Project.setting[allowedPractitionerEmailDomain]` (nhiều mục; rỗng nghĩa là không hạn chế) → 400 `Email domain is not allowed for this project` (trường `email`) (`invite.ts:106-115`, `S/auth/utils.ts:132-163`; test `invite.test.ts:2248-2444`). `externalId` không bị hạn chế miền; email dạng danh sách không khớp user ngoài miền (`2301-2337`).
3. Dựng `User`: `firstName`, `lastName`, `email`, `passwordHash` (bcrypt), `project` (theo `scope`), `mfaRequired`.
4. Có `email`: nếu **không** `forceNewMembership`: tìm membership của user cùng email **cùng project** nhưng khác phạm vi (project-scope/server-scope) → 409 `User is already a member of this project` (test `871-949`). Rồi `conditionalCreate(User)` theo `email` + `project` (hoặc thiếu `project`): có sẵn thì dùng lại (`existingUser`).
5. Không có `email`: tạo `User` mới luôn (theo `externalId`).
6. User mới: `resetPassword(user, 'invite')` tạo `UserSecurityRequest` và URL (P4).
7. Hồ sơ (`upsertProfileResource`): nếu `membership.profile` có: phải cùng project (`meta.project`) và cùng `resourceType`, sai → 400 `Profile does not belong to project` / `Profile resourceType does not match request`. Ngược lại: tạo `Patient/Practitioner/RelatedPerson` (`name`, `telecom[email]`); có `email` thì `conditionalCreate` theo `_project`+`email`; RelatedPerson cần `patient` tồn tại và cùng project (400 `Patient ... does not exist`, `Patient does not belong to project`), thiếu → 400 `Patient is required to create a RelatedPerson`; RelatedPerson theo email nhiều kết quả → `412` (`multipleMatches`, test `1923-1970`).
8. Membership (`upsertProjectMembership`): trước hết `validateAccessPolicies`: mọi `accessPolicy`, `access[].policy`, `membership.accessPolicy`, `membership.access[].policy` phải tồn tại (400 `Access policy <ref> does not exist`) và thuộc project hoặc project liên kết (`Project.link` với `exportedResourceType: ['AccessPolicy']`), nếu không 400 `... does not belong to this project` (`invite.ts:370-430`; test `1271-1558`). Gộp `externalId`, `accessPolicy`, `access`, `admin`, `invitedBy = caller.user`, rồi `...request.membership`. Áp chính sách mặc định khi không có `accessPolicy`/`access`: Patient → `defaultPatientAccessPolicy` (hoặc `defaultAccessPolicies[Patient]`); RelatedPerson → `access:[{policy, parameter:[{name:'patient', valueReference}]}]`; Practitioner → `Admin` nếu `admin` ngược lại `Practitioner` (`invite.ts:442-487`; test `1673-2246`).
9. `forceNewMembership` → luôn tạo; không `upsert` → `conditionalCreate` theo `user`+`project`, đã có → 409 `User is already a member of this project`; `upsert` → trong transaction `serializable`: có sẵn mà profile khác → 409 `User is already a member of this project with a different profile`, ngược lại cập nhật (giữ `id`, `project`, `user`, `profile`).
10. Có `email` và `sendEmail != false`: gửi mail (user mới: link đặt mật khẩu; user cũ: thông báo). Gửi lỗi → trả `200` **kèm** `OperationOutcome` lỗi (`id: 'ok'`, chi tiết `Could not send email. Make sure you have AWS SES set up.`), theo quy ước lịch sử (`invite.ts:613-633`, test `687-716`). **Lưu ý**: dữ liệu (user/membership) **đã được tạo** dù mail lỗi.
11. `externalId` trùng trong project → 409 (`details.text` khớp `/already exists/`; ràng buộc duy nhất ở tầng lưu trữ, test `475-516`), xóa rồi dùng lại được (`518-566`).

### B3.4 Phân loại theo rủi ro và theo nhu cầu vận hành

Rủi ro: **Cao** = có thể nâng quyền, lộ bí mật, hoặc chiếm tài khoản; **TB** = thay đổi dữ liệu danh tính; **Thấp** = đọc/ít tác động.

| Route | Rủi ro | Lý do | Vận hành tối thiểu? | Quyết định |
|---|---|---|---|---|
| `POST /projects/:id/client` | Cao | tạo client có `secret` và cấu hình tùy ý (`identityProvider` = bề mặt SSRF; `redirectUris`; `accessPolicy` không được xác thực thuộc project, B7) | **Có** (tạo client OAuth) | Làm (7.2) |
| `POST /projects/:id/invite` | Cao | tạo user, đặt `admin`, `accessPolicy`, tự cấp quyền | **Có** (mời user) | Làm (7.4) |
| `POST /projects/setpassword` | Cao | đặt mật khẩu thay user, thu hồi mọi login | **Có** (đặt mật khẩu) | Làm (7.5) |
| `POST/GET/DELETE /projects/:id/members/:mid` | Cao (POST), TB (DELETE), Thấp (GET) | POST sửa nguyên membership (`admin`, `accessPolicy`, `access`, `externalId`, `active`) | **Có** (quản lý member) | Làm (7.3) |
| `POST /members/:mid/resetpassword` | TB | gửi mail; phụ thuộc `Mailer` | Tùy chọn (có thể thay bằng `setpassword`) | Làm sau 7.5 |
| `POST /members/:mid/mfa/reset` | Cao | gỡ yếu tố xác thực, không cần chứng minh sở hữu | Chỉ khi có MFA (P5) | Làm sau P5 |
| `GET /projects/:id` | TB | trả `secret` nguyên văn | Có (xem cấu hình) | Làm; cân nhắc ẩn giá trị `secret` (B7) |
| `POST /projects/:id/settings` | Cao | `setting` chứa cờ an ninh (`allow-dangerous-redirect`, `allowedPractitionerEmailDomain`, `allowedMfaMethods`, `googleAuthProfilePictures`, `openPatientRegistration`...) | Tùy chọn | Làm với allowlist tên (Q15) |
| `POST /projects/:id/secrets` | TB | ghi bí mật project (dùng cho bot) | Không cho auth thuần | Hoãn (thuộc bot) |
| `POST /projects/:id/sites` | Cao | `site.googleClientId` quyết định project nào chấp nhận Google client nào (`google.ts:181-203`) | Chỉ nếu dùng Google đa project | Hoãn/Làm sau với kiểm duy nhất |
| `POST /projects/:id/bot` | TB | Bot cần runtime (Lambda/VM) và `Binary` | Không | **Bỏ** (không có runtime bot ở `auth`) |
| `/admin/super/*` | — | B3.2 | Không | **Bỏ**, trừ hai việc thay bằng CLI |

## B4. Mô hình dữ liệu: trường đọc/ghi

| Resource.trường | R/W | Route |
|---|---|---|
| `Project`: `id`, `name`, `owner`, `setting`, `secret`, `site`, `defaultAccessPolicies`, `defaultPatientAccessPolicy`, `link`, `exportedResourceType`, `superAdmin`, `active` | R; **W** thay thế `setting`/`secret`/`site` | project, invite |
| `ProjectMembership`: tất cả trường; `admin`, `accessPolicy`, `access[]`, `externalId`, `active`, `invitedBy`, `user`, `profile`, `project`, `meta.author` | R; W (POST, invite, client, bot); D (DELETE) | members, invite |
| `User`: `email`, `firstName`, `lastName`, `passwordHash`, `project`, `mfaRequired`, `mfaEnrolled`, `mfaMethod`, `mfaSecret`, `emailVerified`, `externalId` | R; W: tạo (invite), `passwordHash` (setpassword), `mfa*` (reset), D (delete member khi project-scope) | |
| `ClientApplication` (mọi trường), `secret` | W (tạo) | client |
| `Bot`, `Binary` | W | bot (bỏ) |
| `Practitioner`/`Patient`/`RelatedPerson` (`name`, `telecom`, `patient`) | W (tạo/đọc) | invite |
| `AccessPolicy` | R (kiểm thuộc project) | invite, client |
| `UserSecurityRequest` | W (`reset`/`invite`), vô hiệu hóa | invite, resetpassword, setpassword |
| `Login` | W (`revoked=true` hàng loạt) | setpassword |
| `AuditEvent`/`Login` | D (`purge`) | super (CLI) |

## B5. Cổng (ports), adapter, thư viện, cấu hình

| Cổng | Việc | Pha |
|---|---|---|
| `Mailer` | mail mời, reset mật khẩu, báo reset MFA | P4 (đã định nghĩa); adapter SMTP/log |
| `Pwned` | kiểm mật khẩu bị rò rỉ khi `setpassword` | P1/P4 |
| `PasswordHasher` (bcrypt) | băm mật khẩu mời/đặt | P1 |
| `Store` (transaction): `WithTx(ctx, fn)` | invite (upsert serializable), delete member, create client (2 ghi), set password (3 ghi) | P1 — **yêu cầu mới**: `pkg/fhirstore` phải có transaction nhiều resource (**chưa xác nhận**) |
| `AccessPolicies`, `Projects`, `Memberships`, `Users`, `Profiles`, `Clients` | CRUD | P1 |
| `Clock`, `Rand` | `generateSecret`, hạn secret | P1 |

Thư viện: `golang.org/x/crypto/bcrypt` (đã dự kiến ở P1), `pquerna/otp` (reset MFA, P5), Echo validator hiện có. Cấu hình: `AUTH_BLOCKED_EMAIL_DOMAINS` (danh sách phẩy, `S/config` `blockedEmailDomains`, dùng ở `invite.ts:112`; **chưa xác nhận** tên khóa trong `config/types.ts`), `AUTH_ADMIN_ENABLED` (công tắc bật nhóm `/admin`, đề xuất, mặc định tắt), `AUTH_PUBLIC_APP_URL` (đã có ở `AppBaseUrl`) cho link trong mail.

## B6. Danh sách ca test (từ `*.test.ts`)

Chỉ port những ca thuộc route được làm (7.1 đến 7.5). `[BỎ]` cho bot/super/hạ tầng.

### B6.1 `client` (`admin/client.test.ts`)

| Ca (dòng) | Kỳ vọng |
|---|---|
| Create new client (26) | 201 `ClientApplication`, `secret` dài 64; đọc lại qua FHIR 200; body thiếu `name` → 400 [GIỮ] |
| Create client as superadmin - verify projectID (69) | membership của client có `project` = project đích [GIỮ nếu giữ super admin; nếu không thì BỎ] |
| Cannot override server-controlled fields (107) | `resourceType`, `meta.project` bị ghi đè đúng; `meta.tag` giữ [GIỮ] |
| Create client with caller-supplied secret (155) | `secret` đúng như gửi [SỬA: đề xuất yêu cầu độ dài tối thiểu, Q16] |
| Applies defaultAccessPolicies when no access policy is provided (176) | membership dùng `defaultAccessPolicies[ClientApplication]`; có `accessPolicy` thì dùng nó [GIỮ; thêm ca policy ngoài project → 400] |
| Project admin cannot create client in another project (236) | 403 [GIỮ] |

### B6.2 `bot` (`admin/bot.test.ts`): **[BỎ]** cả 4 ca (Create new bot 26, Super admin creates bot in target project 76, ... missing project 102, Project admin cannot create bot in another project 112).

### B6.3 `project` (`admin/project.test.ts`)

| Ca (dòng) | Kỳ vọng |
|---|---|
| Get project and promote admin (49) | mời Practitioner 200; 3 membership; GET member 200 và mang `accessPolicy` mặc định; POST `{resourceType:'Patient'}` 403; POST với `id` khác 403; POST `{...member, admin:true}` 200 và `admin` thật; không đổi `accessPolicy` [GIỮ] |
| Get project access denied (148) | user không admin: GET project, GET/POST/DELETE member, POST client/bot/secrets/sites/settings đều 403; admin đúng project 200/201 [GIỮ, bỏ phần bot] |
| Delete membership (310) | xóa member 200; xóa chủ project 400; xóa member không tồn tại 404 [GIỮ] |
| Delete project-scoped user membership also deletes User resource (417) | `User` bị xóa [GIỮ] |
| Delete server-scoped user membership does not delete User resource (468) | `User` còn [GIỮ] |
| Save project secrets (521) | `project.secret` có 1 mục; `meta.author` là profile caller [GIỮ nếu làm secrets] |
| Save project sites (563) | [GIỮ nếu làm sites] |
| Save project settings (596) | [GIỮ] |
| Set password access denied (632) | user thường 403 [GIỮ] |
| Set password missing password (653) | 400 `Password must be at least 8 characters` [GIỮ] |
| Set password user not found (667) | 400 `User not found` [GIỮ] |
| Set password user not associated with project (681) | 400 `User not found` [GIỮ] |
| Set password for global scoped user (704) | 400 `User not found` [GIỮ] |
| Set password for project scoped user (718) | 200 [GIỮ; thêm: mọi `Login` của user bị `revoked`] |
| Reset MFA - success (741) | 200; `mfaEnrolled=false`; `mfaSecret` đổi [GIỮ, sau P5] |
| Reset MFA - user not enrolled (794) | 400 `User is not enrolled in MFA method: totp` [GIỮ] |
| Reset MFA - membership from different project is rejected (827) | 404 [GIỮ, thống nhất mã 403/404, Q17] |
| Reset MFA - non-admin is rejected (872) | 403 [GIỮ] |
| Reset MFA - invalid method is rejected (907) | 400 [GIỮ] |
| Reset MFA - email method leaves TOTP and secret intact (938) | `mfaMethod=['totp']`, `mfaSecret` giữ [GIỮ] |
| Reset MFA - TOTP method leaves email enrolled and rotates secret (990) | `mfaMethod=['email']`, `mfaSecret` đổi [GIỮ] |
| Reset MFA - method not enrolled is rejected (1041) | 400 `User is not enrolled in MFA method: email` [GIỮ] |
| Reset password - sends email to member (1087) | 200, có gửi mail [GIỮ] |
| Reset password - non-admin is rejected (1119) | 403 [GIỮ] |
| Server-scoped member is rejected (mfa/reset, resetpassword) (1152, 2 route) | 403 [GIỮ] |

### B6.4 `invite` (`admin/invite.test.ts`)

| Ca (dòng) | Kỳ vọng |
|---|---|
| New user to project (92) | 200, user + profile + membership [GIỮ] |
| Existing user to project (136) | 200, không gửi link tạo tài khoản mà thông báo [GIỮ] |
| Logs a membership granted to an existing user with an unverified email (188) | log cảnh báo `Membership granted to existing user with unverified email` có giới hạn tần suất [SỬA: kiểm log; tùy chọn] |
| Existing practitioner to project (248) | tái dùng Practitioner có sẵn, 200 [GIỮ] |
| Specified practitioner to project (295) | `membership.profile` chỉ định, 200 [GIỮ] |
| Access denied (344) | 403 [GIỮ] |
| Input validation (377) | 400 [GIỮ, mở rộng theo bảng B3.3] |
| Do not send email (408) | `sendEmail:false` → không gửi [GIỮ] |
| Invite by externalId (438) | 200 [GIỮ] |
| Duplicate externalId (475) | 409 `/already exists/` [GIỮ] |
| Reuse deleted externalId (518) | xóa rồi mời lại 200 [GIỮ] |
| Invite as client (568) | client (Basic) đã nâng `admin` gọi invite 200, profile `Patient/` [GIỮ] |
| Invite user as admin (624) / admin flag false (657) | `admin` đúng như gửi [GIỮ] |
| Email sending error due to SES not being set up (687) | 200 + OperationOutcome lỗi `Could not send email...`; dữ liệu đã tạo [GIỮ hoặc SỬA theo Q18] |
| Super admin invite to different project (718) | 200 [GIỮ nếu giữ super admin] |
| Convert capitalized email to lower case (746) | email lưu lowercase [GIỮ] |
| Invite user with existing membership (784) | lần 2 409 `User is already a member of this project`; `upsert` 200 cùng `id`; profile khác 409 `... with a different profile`; `forceNewMembership` 200 khác `id` [GIỮ] |
| Inviting a project-scoped user when there is already a server-scoped member (871) / ngược lại (911) | 409 [GIỮ] |
| Invite user with forceNewMembership in different cases (951) | 8 tổ hợp, đều 200 [GIỮ] |
| Invite project scoped user (1107) / server scoped user not accessible from the project (1141) | phạm vi và khả năng đọc [GIỮ; ca thứ hai cần kho riêng D2] |
| End-to-end invite and require MFA (1174) | mời với `mfaRequired`, đặt mật khẩu, đăng nhập đòi MFA [GIỮ, sau P5] |
| Invite with valid access policy (1271) | 200 [GIỮ] |
| Invite with invalid access policy (does not exist) (1313) | 400 [GIỮ] |
| Invite with access policy from different project (1345) | 400 [GIỮ] |
| Invite with access policy from linked project (1415) | policy từ project liên kết 200; policy không thuộc project 400 `does not belong to this project` [HOÃN nếu không làm `Project.link`] |
| Invite without access policy (backwards compatibility) (1450) | 200 [GIỮ] |
| Invite with access array containing invalid policy (1479) / membership.access ... (1526) | 400 [GIỮ] |
| Invite Patient with scope: server creates server-scoped user (1560) | [GIỮ] |
| Invite Patient without scope defaults to project-scoped (1596) | [GIỮ] |
| Invite Patient with scope: server - conflict with existing project-scoped Patient (1631) | 409 [GIỮ] |
| Invite Patient applies defaultPatientAccessPolicy when no explicit policy (1673) / uses explicit over default (1700) / no explicit and no default (1734) | [GIỮ] |
| Invite Practitioner does not apply defaultPatientAccessPolicy (1750) / RelatedPerson does not apply (1777) | [GIỮ] |
| Invite RelatedPerson provisions profile with patient (1818) | [GIỮ] |
| Invite RelatedPerson without patient throws (1842) | `Patient is required to create a RelatedPerson` [GIỮ] |
| Invite RelatedPerson with non-existent patient throws badRequest (1858) | `Patient Patient/<id> does not exist` [GIỮ] |
| Invite existing RelatedPerson by email does not require patient (1876) | 200 [GIỮ] |
| Invite RelatedPerson by email with duplicate matches throws (1923) | 412 [GIỮ] |
| Invite RelatedPerson with patient from another project throws (1972) | `Patient does not belong to project` [GIỮ] |
| Invite Practitioner without patient does not error (1996) | [GIỮ] |
| Invite Patient prefers defaultAccessPolicies over legacy defaultPatientAccessPolicy (2012) / falls back to legacy (2044) | [GIỮ] |
| Invite RelatedPerson applies defaultAccessPolicies with patient parameter (2076) | `access[0].parameter[patient]` [GIỮ] |
| Invite admin Practitioner applies Admin defaultAccessPolicies (2112) / non-admin applies Practitioner (2139) / no Admin default (2173) / no Practitioner default (2211) | [GIỮ] |
| Invite with allowed email domain succeeds (2248) | 200 [GIỮ] |
| Invite with disallowed email domain is rejected (2274) | 400 `Email domain is not allowed for this project` [GIỮ] |
| Email with comma does not match a user outside the allowed domains (2301) | 200 (không khớp user nào, tạo mới) [GIỮ] |
| Invite by externalId is unaffected by allowed domain restrictions (2339) | 200 [GIỮ] |
| Super admin invite bypasses allowed domain restriction (2365) | 200 [GIỮ nếu giữ super admin] |
| Invite with blocked email domain is rejected even without project restrictions (2392) / takes precedence over allowed (2419) | 400 [GIỮ] |

### B6.5 `super` (`admin/super.test.ts`)

**[BỎ]** toàn bộ nhóm hạ tầng (valuesets, structuredefinitions, searchparameters, reindex, lambda-cleaner, rebuildprojectid, migrations, tablesettings, vacuum, reloadcron, sharding: `super.test.ts:272-842`, `985-1511`) và `removebotidjobsfromqueue` (949-983). Giữ làm ca test **CLI**: `Set password access denied` (843 → đổi thành "CLI không chạy khi thiếu quyền"), `Set password missing password` (856), `Set password user not found` (870), `Set password success` (884), `Purge access denied` (909), `Purge invalid resource type` (922), `Purge logins success` (936).

## B7. Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test bắt buộc |
|---|---|---|
| A1 | **Ai gọi route nào**: mọi route `/admin/projects/*` cần xác thực (401) và admin (403) | Ma trận (user thường, client không admin, admin project khác, admin đúng project, super admin) × từng route |
| A2 | **Cách ly project**: `:projectId` trong URL **không** được tin; dùng project của token. Với `bot`/`client` (Medplum đọc `:projectId`): admin chỉ thao tác project của mình, khác → 403; super admin tạo ở project khác phải kiểm tồn tại → 404 | Test chéo project cho **mọi** route (kể cả GET project, settings, secrets, sites, members). Đặc biệt: gọi `/admin/projects/<project khác>/settings` bằng token admin A phải **không** sửa project A hoặc B theo cách nhầm lẫn; thống nhất hành vi (Go đề xuất: `:projectId != ctx.project.id` → 403 cho mọi route, trừ super admin) |
| A3 | **Nâng quyền qua invite**: người gọi chỉ được cấp `admin`, `accessPolicy`, `access` trong phạm vi project của mình; `accessPolicy` phải thuộc project (Medplum kiểm ở invite nhưng **không** kiểm ở `client`, `client.ts:54-63`) | Test `client` với `accessPolicy` của project khác → 400 (bổ sung); `invite` cho `membership.admin=true` bởi admin → cho phép (tài liệu hóa), bởi non-admin → 403 |
| A4 | **Ghi đè trường do server kiểm soát**: `meta.project`, `meta.author`, `resourceType`, `id`, `project`, `user`, `profile` của membership; `membership` trong invite trải sau cùng (`invite.ts:448`) nên có thể ghi đè `externalId`, `admin`... | Test: `membership.project/user/profile` trong body invite **không** được thay; POST members không đổi `user`/`profile`/`project` (Medplum chỉ kiểm `resourceType` và `id`, `project.ts:134`; **chưa xác nhận** repo chặn phần còn lại) |
| A5 | **Xóa chủ project**: 400; xóa membership của chính mình nếu là admin cuối cùng (Medplum chỉ chặn `owner`) | Test; quyết định có thêm chặn "admin cuối cùng" (Q19) |
| A6 | **Thu hồi sau đổi mật khẩu**: `setpassword` thu hồi mọi `Login`; hết hiệu lực link `reset` cũ | Test `Login.revoked=true` cho mọi login; `UserSecurityRequest` cũ không dùng được |
| A7 | **Không lộ bí mật**: `passwordHash`, `mfaSecret`, `Login.code`, `refreshSecret`, `ClientApplication.secret` (ngoài lần tạo), `Project.secret`, `identityProvider.clientSecret` | Test phản hồi và log; `GET /admin/projects/:id` trả `secret` project cho admin (Medplum có): quyết định Q20 |
| A8 | **Giá trị cấu hình nguy hiểm do admin nhập**: `identityProvider.*Url` (SSRF), `redirectUris` (open redirect khi `allow-dangerous-redirect`), `Project.setting` | Kiểm URL IdP khi lưu (HTTPS, không IP nội bộ — bổ sung so với Medplum chỉ kiểm lúc gọi); allowlist tên `setting` (Q15) |
| A9 | **Mật khẩu**: 8..72 byte; kiểm pwned; invite không đặt mật khẩu → ngẫu nhiên không lộ | Test biên 7/8/72/73 byte |
| A10 | **Đồng thời**: invite song song cùng email/externalId không tạo trùng; `upsert` serializable | Test chạy song song (`-race`) |
| A11 | **Nhật ký kiểm toán** hành động admin (tạo client, đổi `admin`, xóa member, reset MFA/mật khẩu) | Test có sự kiện log/audit (Medplum tạo `AuditEvent` ở `setLoginMembership`; cho `admin/` **chưa xác nhận**) |
| A12 | **Basic auth bằng client**: client có thể được nâng admin và gọi `/admin/*` | Test; khuyến nghị giới hạn bằng IP hoặc không cho client admin dùng cho người |

## B8. Phần Medplum có nhưng KHÔNG nên port đợt này; điểm lệch; rủi ro; câu hỏi mở

### B8.1 Không port

| Phần | Lý do |
|---|---|
| Toàn bộ `/admin/super/*` trừ hai việc danh tính | Hạ tầng CSDL/FHIR/AWS/bot (B3.2); thuộc `fhir-server`/vận hành |
| `POST /projects/:id/bot` | Cần runtime bot (`deployBot`, Lambda/VM), lưu `Binary` |
| `POST /projects/:id/secrets` | Phục vụ bot; không cần cho xác thực |
| Gửi mail qua AWS SES (`sendEmail`) | `auth` dùng cổng `Mailer` (D4) |
| `Project.link`/`exportedResourceType` (chính sách từ project liên kết) | Cần mô hình project liên kết; HOÃN (ca test 1415) |
| `shardId`, `Prefer: respond-async`, `AsyncJob` | Chỉ cho `super` |
| Chế độ `X-Medplum: extended` | Thuộc lớp FHIR/middleware (P3); `admin/` chỉ đọc kết quả |

### B8.2 Điểm lệch chấp nhận được

- `:projectId` khác `ctx.project.id` → 403 thống nhất (Medplum: bỏ qua ở nhiều route, 403 ở `bot`/`client`).
- DTO kiểu chặt cho `invite`, `settings`, `sites` (Medplum chỉ validator một phần rồi để repo kiểm).
- `invite`: lỗi gửi mail trả lỗi rõ ràng thay vì `200` + lỗi; hoặc giữ (Q18).
- `client`: kiểm `accessPolicy` thuộc project; kiểm độ mạnh `secret` (Q16).

### B8.3 Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Bề mặt lớn (13 route project, DTO mở như `ClientApplication` và `ProjectMembership`) | Làm theo bước nhỏ, mặc định tắt `AUTH_ADMIN_ENABLED` |
| Admin sửa nguyên `ProjectMembership`/`Project.setting` có thể bật cờ nguy hiểm | A4, A8, allowlist |
| `fhirstore` chưa chắc có transaction nhiều resource (invite/delete member/create client) | Xác nhận ở P1; nếu không, thiết kế bù trừ và test lỗi giữa chừng |
| Phụ thuộc P3, P4, P5 | Thứ tự bước B10 |

### B8.4 Câu hỏi mở

| # | Câu hỏi |
|---|---|
| Q12 | Có làm P7 đợt này không (D3 nói đợt sau), hay chỉ CLI (mục B9)? Khuyến nghị: **CLI trước, HTTP sau** |
| Q13 | Có giữ khái niệm super admin (`Project.superAdmin`) trong `auth`? Nếu không, bỏ `invite` cho project khác và `client` cho project khác |
| Q14 | `Basic` (client credentials) có được dùng để gọi `/admin/*` không? |
| Q15 | `POST /settings` có allowlist tên `setting` (chặn `allow-dangerous-redirect`...)? |
| Q16 | `secret` do caller gửi có yêu cầu độ dài tối thiểu (ví dụ 32 ký tự)? |
| Q17 | Membership ngoài project: 403 (như `GET members`) hay 404 (như `mfa/reset` do repo ẩn)? Chọn một cho mọi route |
| Q18 | `invite` khi mail lỗi: giữ `200` + OperationOutcome lỗi (parity) hay trả lỗi? |
| Q19 | Có chặn xóa/hạ quyền admin cuối cùng của project? |
| Q20 | `GET /admin/projects/:id` có che giá trị `secret`? |
| Q21 | **Chưa xác nhận**: tên khóa cấu hình `blockedEmailDomains` và danh sách đầy đủ `DEFAULT_ACCESS_POLICIES` (đã thấy `Patient`, `RelatedPerson`, `Admin`, `Practitioner`, `Bot`, `ClientApplication`, `default-access-policies.ts:35-80`; phần sau dòng 80 chưa đọc) |
| Q22 | **Chưa xác nhận**: sau khi xóa membership, access token đã cấp còn hiệu lực đến hạn hay bị từ chối ngay (phụ thuộc cách `getLoginForAccessToken` đọc membership, P3) |

## B9. Tạo ClientApplication và tài khoản đầu tiên cho đến khi P7 xong

### B9.1 Điều `admin/` và `projectinit` thực sự làm (căn cứ)

Tạo project đầu tiên (`S/fhir/operations/projectinit.ts:125-198`, gọi bởi `registerNew` `S/auth/register.ts:45-95` và `newproject`), trong **một transaction**:
1. Tạo `AccessPolicy` mặc định cho từng loại profile (`createDefaultAccessPolicies`, `projectinit.ts:200-218`, nội dung `default-access-policies.ts:35-80`: `Patient`, `RelatedPerson`, `Admin`, `Practitioner`, `Bot`, `ClientApplication`...), mỗi cái `meta.project = projectId`.
2. Tạo `Project{id, name, owner=user, strictMode:true, features=config.defaultProjectFeatures, systemSetting=config.defaultProjectSystemSetting, defaultAccessPolicies}` (id sinh trước, `assignedId`).
3. `createClient(project, name = '<project> Default Client')` (`client.ts:39-66`): `ClientApplication` (`meta.project`, `secret = generateSecret(32)` → 64 hex) + `ProjectMembership{project, user=client, profile=client, accessPolicy=default ClientApplication}`.
4. Nếu có admin: `Practitioner` (tên, email) + `ProjectMembership{project, user, profile, admin:true}`.
5. `User` tạo trước bằng `registerNew` (`email` lowercase, `passwordHash` bcrypt) nếu chưa có.

Tạo client sau này (`client.ts:18-66`): `ClientApplication{...body, meta:{...meta, project, author}, resourceType, secret ?? generateSecret(32)}` + `ProjectMembership` như bước 3. Mời user (`invite.ts`): xem B3.3.

### B9.2 Đề xuất (không cần HTTP admin)

Lệnh con của `cmd/auth/main.go` (cạnh `serve | healthcheck | migrate`, theo `p0-scaffold.md`), dùng **cùng `application` use case** với HTTP sau này (không viết lại logic):

| Lệnh | Việc | Ghi chú |
|---|---|---|
| `auth seed project --name <n> --admin-email <e> --admin-first <..> --admin-last <..>` | Làm đúng bước 1 đến 5 ở B9.1 trong **một transaction**; mật khẩu admin đọc từ biến môi trường `AUTH_SEED_ADMIN_PASSWORD` hoặc stdin (không nhận qua argv); kiểm pwned và độ dài 8..72; in `projectId`, `clientId`, `secret` **một lần** ra stdout | Idempotent theo cờ `--if-absent` (tìm `Project` theo `name` hoặc `User` theo email); từ chối chạy khi `Environment=production` nếu không có `--allow-production` |
| `auth client create --project <id> --name <n> [--redirect-uri ...] [--pkce-optional] [--grant-type ...] [--access-policy <ref>] [--admin]` | Làm đúng `createClient` (client + membership); kiểm `accessPolicy` thuộc project; in `secret` một lần | Đây là việc "tạo client OAuth" mà D3 nói tạm bằng seed/CLI |
| `auth user set-password --email <e> [--project <id>]` | Thay `super/setpassword` và `project/setpassword` | Mật khẩu từ stdin/env; thu hồi mọi `Login` |
| `auth user invite ...` (tùy chọn) | Thay `invite` cho tới 7.4 | |
| `auth domain add ...` | Tạo `DomainConfiguration` cho P6 (A9) | Kiểm URL IdP (S1) khi ghi |
| `auth purge-logins --before <iso>` | Thay `super/purge` cho `Login` | |

Mốc: lệnh `seed project` và `client create` làm **ngay sau P1** (chỉ cần repository và bcrypt), đủ để P2/P3 có dữ liệu thử và để E2E ở P3 chạy (kế hoạch tổng nói "mốc dùng được tối thiểu hết P3 với tài khoản và client tạo bằng seed").

## B10. Cỡ và thứ tự làm (P7: tổng cỡ L)

| Bước | Việc | Phụ thuộc | Cỡ | Tiêu chí hoàn thành |
|---|---|---|---|---|
| 7.0 | CLI `seed project`, `client create`, `user set-password`, `domain add`, `purge-logins` (B9.2); dùng use case dùng chung | P1 (repo, bcrypt, transaction) | M | Test bảng cho từng lệnh (kể cả lỗi giữa chừng: rollback); `secret` chỉ in một lần; không nhận mật khẩu qua argv; **P7 HTTP chưa cần** |
| 7.1 | Khung `/admin/projects`: middleware `RequireProjectAdmin`, ma trận quyền (A1, A2); `GET /admin/projects/:id`, `POST settings` (có allowlist nếu Q15) | P3 (xác thực Bearer/Basic) | S | Ma trận 401/403 xanh cho mọi route đã khai báo; `:projectId` lệch → 403 |
| 7.2 | `POST /admin/projects/:id/client` | 7.1, 7.0 | S | Mục B6.1 xanh; A3, A4, A8 có test |
| 7.3 | Members: `GET`, `POST`, `DELETE` | 7.1 | M | Mục B6.3 (nhóm members) xanh; A4, A5 có test |
| 7.4 | `POST /admin/projects/:id/invite` | 7.1, P4 (`resetPassword`, `Mailer`), transaction | L | Mục B6.4 xanh (trừ ca HOÃN); A3, A10 có test |
| 7.5 | `POST /admin/projects/setpassword`, `members/:id/resetpassword`, `members/:id/mfa/reset` | 7.1, P4, P5 | M | Mục B6.3 (nhóm password/MFA) xanh; A6 có test |
| 7.6 | `sites`, `secrets` (nếu quyết định làm) | 7.1 | S | Test `Save project sites/secrets`; kiểm duy nhất `googleClientId` |
| 7.7 | Cập nhật `admin.md` thành hợp đồng thật (DTO ở B3), đồng bộ swagger | 7.1-7.5 | S | `admin.md` khớp swagger sinh ra; bỏ ghi chú "không guard" sai (mục 0.3) |

Thứ tự: 7.0 (có thể chạy ngay sau P1, **không** phụ thuộc quyết định D3) → 7.1 → 7.2 → 7.3 → 7.5 → 7.4 → 7.6/7.7. Nếu hoãn P7 hoàn toàn, chỉ làm 7.0 là đủ để vận hành tối thiểu.

## Tiến độ P7 (cập nhật 2026-11-22)

User duyệt bộ Q12 đến Q22 theo đề xuất ("duyệt hết, bắt đầu P7"): làm HTTP nhưng mặc định tắt (`AUTH_ADMIN_ENABLED`), bỏ super admin, chỉ Bearer (không Basic), allowlist `setting`, `secret` client tối thiểu 32 ký tự, membership ngoài project là 403 chung, mail lỗi báo rõ, chặn xóa/hạ quyền admin cuối cùng, che `secret` khi đọc. Bỏ `bot`, `secrets`, `super/*`; hoãn `sites`.

**Xong 7.1 đến 7.5 và 7.7:** `application/admin_project.go` (guard, `AdminGetProject`, `AdminReplaceSettings`, `AdminCreateClient`, member get/update/delete, `AdminSetPassword`, `AdminResetMemberPassword`, `AdminResetMemberMFA`), `application/admin_invite.go` (`AdminInvite`), `presentation/httpapi/admin.go`; kho thêm `Mutate`/`Delete`/`ListAllForUser`/`ListActiveAdmins` (membership), `Mutate` (project), `Delete` (user), `ResourceWriter.Delete/Get`. Cấu hình `AUTH_ADMIN_ENABLED`, `AUTH_BLOCKED_EMAIL_DOMAINS`. Test: application (memory/SQLite/PG), HTTP `bootstrap/admin_http_test.go`, e2e `e2e/admin_test.go`; đột biến chạy trên `admin_project.go` và `admin_invite.go` (mọi mutant bị bắt sau khi bổ sung ca biên). Hợp đồng ở `apps/auth/docs/api-contract/admin.md`.

**Lệch có chủ đích so với Medplum:** `:projectId` lệch là 403 (Medplum bỏ qua ở nhiều route); DTO chặt (trường lạ là 400, client không nhận `identityProvider`/`signInForm`/`status`); update member chỉ đổi 5 trường; invite bỏ `accessPolicy`/`access`/`admin` ở mức gốc, không conditionalCreate hồ sơ, không có `Project.link`; lỗi gửi thư là lỗi (không 200 kèm OperationOutcome); 404 của member lạ thành 403.

**Xong 7.6 `sites` và SCIM (2026-11-22):** `AdminReplaceSites` (kiểm duy nhất `googleClientId`/`recaptchaSiteKey` giữa các project trong transaction giữ khóa theo giá trị, che secret, giữ secret khi gửi lại `********`); SCIM theo kế hoạch `docs/plans/modules/scim-ws-fhircast.md` mục A với Q-SC-1..7 đã duyệt (Basic/Bearer của client admin, hồ sơ ghi vào kho auth, Practitioner cần chính sách mặc định, 404 cho id ngoài project, 409 externalId trùng, log kích hoạt lại, cờ toàn cục + `Project.features` chứa `scim`). Gói mới `domain/scim` (kiểu, filter, patch), `application/scim.go`, `presentation/httpapi/scim.go`, lệnh `auth project feature`; kho thêm `ListByProject`, `TxRepos.Projects/Logins`. Hợp đồng ở `apps/auth/docs/api-contract/admin.md` (mục SCIM). Test: domain, application (memory/SQLite/PG, gồm hoàn tác atomic trên SQLite/PG), HTTP, config, e2e `e2e/admin_test.go`; đột biến chạy trên toàn bộ phần mới, mọi mutant bị bắt.

**Chưa làm/ghi chú:** `secrets`/`bot` không làm (không cho auth thuần); vai trò `auth serve -role=scim` (phương án B của split-scim-ws-services.md) chưa làm; `userName` không đổi được sau khi tạo (giống Medplum); Q21 vẫn chưa xác nhận. Q21 (tên khóa `blockedEmailDomains` của Medplum, danh sách `DEFAULT_ACCESS_POLICIES` đầy đủ) vẫn chưa xác nhận: ta dùng tên riêng `AUTH_BLOCKED_EMAIL_DOMAINS` và chỉ các policy mặc định do project cấu hình.
