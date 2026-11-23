# Kế hoạch chi tiết P1 và P2: lõi dữ liệu, an toàn và đăng nhập lõi của `apps/auth`

Trạng thái: **bản nháp chờ duyệt**. Chưa có code. Tiếp nối `docs/plans/apps-auth.md` (mục 3 D1 đến D6, mục 5 lộ trình) và `docs/api-contract/auth-dto.md`.

Quy ước trích dẫn:
- `file:dòng` không có tiền tố là đường dẫn trong `/Users/hocnt84/Downloads/medplum/packages/server/src` (ví dụ `oauth/utils.ts:155` là `.../server/src/oauth/utils.ts` dòng 155). `core/...` là `/Users/hocnt84/Downloads/medplum/packages/core/src`.
- Đường dẫn Go tính từ gốc monorepo (`trivita-codebase-v1`), module `trivita-fhir`. Đường dẫn dạng `internal/...` nghĩa là `apps/auth/internal/...`.
- Mọi điều ghi ở đây là đã đọc thấy trong source Medplum hoặc trong repo Go. Chỗ chưa đọc hoặc chưa kiểm chứng ghi **"chưa xác nhận"**. Thư viện Go đề xuất là kiến thức bên ngoài source, ghi rõ ở mục 5.

## 0. Phát hiện chung ảnh hưởng cả hai pha (đọc trước)

1. **`fhirrouter.Repository` không có `PatchResource`** (`pkg/fhirrouter/router.go:79-92`). Medplum dùng patch ở nhiều chỗ then chốt (`oauth/utils.ts:233`, `:770`, `auth/utils.ts:333,356`, `auth/setpassword.ts:103-117`). Test của Medplum buộc patch phải **đọc lại từ kho rồi chỉ ghi trường cần đổi** (`auth/setpassword.test.ts:404-442`: login đọc trước khi đổi mật khẩu vẫn phải giữ `revoked=true` sau `setLoginScope`). Go cần một helper "đọc, sửa, `UpdateResource` với `IfMatch`, thử lại khi 412" (mục P1.4). `UpdateResource` đã hỗ trợ `IfMatch` (`pkg/fhirstore/crud.go:142-160`).
2. **`/auth/me` đọc resource lâm sàng** (`Practitioner`, `Patient`... qua `membership.profile`, `auth/me.ts:44-50`). D2 khuyến nghị kho riêng cho `auth`, trong khi profile nằm ở kho của `fhir-server`. Đây là câu hỏi mở chặn việc làm `me` (mục P2.9).
3. **`setLoginMembership` cần resolver access policy** (chỉ để lấy `ipAccessRule` và hai lỗi SMART) (`oauth/utils.ts:580-584`). Phải port một phần `fhir/accesspolicy.ts` ngay từ P1; phần còn lại (dựng policy đầy đủ cho claim `fhir_access_policy`) thuộc P3.
4. **`/auth/me` và `/auth/revoke` cần middleware Bearer** (`auth/routes.ts:32,45`, `oauth/middleware.ts:42-57`) mà P3 mới làm (khóa, JWKS, xác minh token). Đề xuất P2 viết handler qua cổng `Authenticator` + fake, nối thật khi P3 xong (mục P2.10 bước 2.6).
5. **`sendLoginResult` có hai nhánh MFA** (`auth/utils.ts:479-502`) dựa vào TOTP enrollment (Redis, otplib, qrcode) và gửi email mã MFA; đó là việc của P5. P2 phải cài đúng thứ tự nhánh nhưng **fail đóng** (không bao giờ bỏ qua MFA) khi chưa có adapter P5 (mục P2.4 bước 4).
6. **Khác biệt auth-dto.md cần sửa** (đã đối chiếu source): (a) nhánh 1 của `LoginResult` điều kiện là `isMfaRequired(user, project)` = project bắt buộc **hoặc** `User.mfaRequired` (`auth/utils.ts:205-208`), tài liệu mới ghi "project bắt buộc"; (b) `GET /auth/login/{login}` kiểm tra `granted` **trước** `revoked` (`auth/status.ts:25-33`); (c) `projects` trong `memberships` của dạng 5 là `Reference` có `display` = tên project, lấy lại từ DB (`auth/utils.ts:524-536`); (d) `/auth/revoke` thông điệp validator là `Login ID is required.` có dấu chấm (`auth/revoke.ts:16`), còn `/auth/login/{login}` là `Login ID is required` không dấu chấm (`auth/status.ts:17`).

---

# PHẦN P1: Lõi dữ liệu và an toàn

## P1.1 Mục tiêu và phạm vi

Mục tiêu: có đủ **tầng domain + application + adapter kho** để P2 chỉ còn là nối HTTP. Không có route mới nào ở P1 (chỉ test và mã thư viện nội bộ).

Hàm Medplum trong phạm vi (đều ở `oauth/utils.ts` trừ khi ghi khác):
- Tra cứu: `getClientApplication` (:146), `getStandardClientById`/`getClientRedirectUri(s)` (`oauth/clients.ts:37,60,96`), `getProjectIdByClientId` (`auth/utils.ts:586`), `getUserByEmail`/`InProject`/`WithoutProject` (:908,:926,:952), `getMembershipsForLogin` (:423), `getLoginProject` (`auth/utils.ts:455`), `getInactiveReason` (`fhir/projectmembership.ts:33`).
- Đổi trạng thái Login: `setLoginMembership` (:510), `setLoginScope` (:739) cùng các hàm tập con scope (:644-729), `revokeLogin` (:845), phần "đánh dấu granted" của `getAuthTokens` (:791-797) (ký token để P3).
- An toàn: `checkIpAccessRules` (:628) + `matchesIpAccessRule` (`util/url.ts:208`), `timingSafeEqualStr` (:984), `generateSecret` (`oauth/keys.ts:229`), `includeRefreshToken` (:1001), `bcryptHashPassword` (`auth/utils.ts:642`) và `MIN/MAX_PASSWORD_LENGTH` (`constants.ts:71,81`), kiểm tra pwned (`auth/setpassword.ts:89`, `auth/newuser.ts:129`), `verifyPassword`/`authenticate` (:309-334, đặc biệt hash giả chống dò email).
- `UserSecurityRequest`: toàn bộ `auth/securityrequest.ts` (:21-156).
- Mật khẩu lõi (đề xuất, người duyệt chốt có kéo vào P1 hay để P4): `setPassword` (`auth/setpassword.ts:83-122`) và `revokeUserLogins` (:128-149), vì test race của `tryLogin` cần chúng (`auth/setpassword.test.ts:317-402`).
- Resolver access policy tối thiểu (`fhir/accesspolicy.ts:174-312` phần liên quan IP, SMART).
- Chặn lộ dữ liệu nhạy cảm qua `/fhir/R4` (từ `apps-auth.md` mục 5).

Ngoài phạm vi P1: mọi route HTTP, `verifyMfaToken` (:347, P5), `getUserByExternalId` (:860, P6), `getClientApplicationMembership` (:483, dùng ở token, P3), ký JWT (P3).

## P1.2 Bảng ánh xạ source sang Go

Package đích (theo kiến trúc `apps-auth.md` mục 4). Ký hiệu: **D** = `internal/domain`, **A** = `internal/application`, **I** = `internal/infrastructure`.

| Medplum (`file:dòng`) | Hành vi | Go đích |
|---|---|---|
| `constants.ts:62-66`, `securityrequest.ts:21,31` | Cửa sổ hạn: reset 1 giờ, invite và verify-email 7 ngày; thiếu `type` coi như `reset`. `getSecurityRequestExpiration` trả ISO instant | D `securityrequest.go`: `ExpirationFor(type string, now time.Time) time.Time`, `ExpirationMs(type)` |
| `securityrequest.ts:45-51` | Hết hạn nếu `expiresAt` parse lỗi (NaN) hoặc `now >= expiresAt`; thiếu `expiresAt` thì dùng `meta.lastUpdated + cửa sổ theo type`; không có mốc nào thì hết hạn | D `securityrequest.go`: `IsExpired(r *fhir.UserSecurityRequest, now time.Time) bool` |
| `securityrequest.ts:61-76` | `consume`: update `used=true` với `ifMatch=versionId`; 412 thành `400 "Already used"` | A `securityrequest.go`: `ConsumeSecurityRequest`; cổng `SecurityRequestRepository.MarkUsed(ctx, r)` trả `domain.ErrConflict`, A đổi thành BadRequest `Already used` |
| `securityrequest.ts:87-134` | `supersedePrior(user,type)`: đánh `used=true` mọi request chưa dùng cùng `type`, trong cửa sổ `MAX_EXPIRATION_MS` (7 ngày), lọc `user` và `_lastUpdated >= now-7 ngày`, sắp `_lastUpdated`, count 1000 | A `securityrequest.go`: `SupersedePriorSecurityRequests`; I: gom hết id trước rồi mới patch (xem P1.4 mục 7) |
| `securityrequest.ts:144,154` | `retirePassword` (mọi type trừ `verify-email`, kể cả thiếu type), `retireAll` | A cùng file |
| `oauth/clients.ts:17-39` | Client dựng sẵn: `medplum-cli` (`redirectUris:['http://localhost:9615']`, `pkceOptional:true`) + `config.defaultOAuthClients` | D `client.go`: `StandardClients`; config `DefaultOAuthClients` |
| `oauth/clients.ts:60-75,96-105` | `getClientRedirectUri`: so khớp **chính xác** (không partial mặc định) trên `redirectUri` cũ cộng `redirectUris` | D `client.go`: `RedirectURIs(c)`, `MatchRedirectURI(c, uri)` |
| `oauth/utils.ts:146-153` | Client dựng sẵn trước, không có thì đọc `ClientApplication` theo id; id không phải UUID hoặc không có thì 404 `Not found` (`fhir/repo.ts:717-719`) | A `ports.go`: `ClientRepository.Get`; I `store/clients.go` |
| `auth/utils.ts:586-601` | `getProjectIdByClientId(clientId, projectId)`: có clientId thì lấy `client.meta.project`; `projectId !== undefined && projectId !== clientProjectId` thì 400 `Invalid projectId`; trả `clientProjectId` | A `login_project.go`: `ResolveProjectID` |
| `oauth/utils.ts:908-917` | `getUserByEmail`: có `projectId` (khác `'new'`) thì tìm user **gắn project** trước; không có thì tìm user **không có project** | A `users.go`: `FindUserByEmail(ctx, email, projectID)`; cổng `UserRepository.FindInProject`, `FindGlobal` |
| `oauth/utils.ts:926-944`, `:952-970` | Tìm `email` (string, `:exact`, `email.toLowerCase()`) + `project=Project/<id>`; hoặc `project:missing=true`. Lấy phần tử đầu | I `store/users.go` |
| `oauth/utils.ts:423-474` | `getMembershipsForLogin` (thuật toán mục P1.4.2) | A `memberships.go`: `MembershipsForLogin` |
| `fhir/projectmembership.ts:33-44` | `getInactiveReason`: membership `active===false` thì `Profile not active`; project `active===false` thì `Project not active` | D `project.go`: `InactiveReason(p, m)` |
| `oauth/utils.ts:510-618` | `setLoginMembership` (mục P1.4.1) | A `memberships.go`: `SetLoginMembership` |
| `fhir/accesspolicy.ts:174-203,210-261,272-312`; `fhir/smart.ts:196-211` | Gom `ipAccessRule` từ `membership.accessPolicy` và `membership.access[].policy` (có thay biến `%profile`, `%patient`, tham số); hai lỗi SMART (mục P1.4.1 bước 9) | A `accesspolicy_ip.go`: `ResolveIPAccessRules`; cổng `AccessPolicyRepository`; D `ip.go` |
| `oauth/utils.ts:628-642`, `util/url.ts:208-211` | Quy tắc IP: duyệt theo thứ tự, rule khớp đầu tiên quyết định: `allow` thì cho qua, `block` thì 400 `IP address not allowed`; không có `remoteAddress` hoặc không có rule thì cho qua. Khớp: `*`, bằng đúng, hoặc `remoteAddress` bắt đầu bằng `rule` (thêm `.` nếu chưa có) | D `ip.go`: `MatchesIPRule`, `CheckIPAccessRules` |
| `oauth/utils.ts:644-729`, `fhir/smart.ts:41,142-184` | Parse scope SMART (regex `^(patient\|user\|system)/(\w+\|\*)\.(read\|write\|c?r?u?d?s?\|\*)$`, `?query`), chuẩn hóa `*` thành `cruds`, `read` thành `rs`, `write` thành `cud`; `matchesScope`, `isSmartScopeSubset`, `isNonSmartScopeSubset` | D `scope.go` (không dùng lại `pkg/fhirauth/grants.go:parseSMART` vì nó bỏ `patient/` và không có `?params`) |
| `oauth/utils.ts:739-771` | `setLoginScope` (mục P1.4.4) | A `scope.go`: `SetLoginScope` |
| `oauth/utils.ts:773-843` | `getAuthTokens`: chỉ P1 lấy bước kiểm `membership` và đặt `granted=true`; ký token ở P3 | A `grant.go`: `GrantLogin(ctx, login)` |
| `oauth/utils.ts:845-850` | `revokeLogin`: ghi cả đối tượng `{...login, revoked:true}`, không `ifMatch` | A `revoke.go`: `RevokeLogin` (dùng helper patch, xem điểm lệch mục P1.8) |
| `oauth/utils.ts:984-993` | `timingSafeEqualStr`: cả hai `undefined` thì `true`; một bên `undefined` thì `false`; khác độ dài thì `false`; còn lại `timingSafeEqual` | D `secret.go`: `ConstantTimeEqual(a, b *string) bool` (dùng `crypto/subtle`) |
| `oauth/keys.ts:229-231` | `generateSecret(n)` = `randomBytes(n)` dạng hex (n=16 thì 32 ký tự) | D `secret.go`: `GenerateSecret(n int) string` (`crypto/rand`); cổng `SecretGenerator` để test cố định |
| `oauth/utils.ts:1001-1020` | `includeRefreshToken`: `remember`, hoặc scope có `offline`, hoặc `client.grantType` chứa `refresh_token`, hoặc scope có `offline_access` | D `login.go`: `IncludeRefreshToken(req, client)` |
| `oauth/utils.ts:72,309-334` | Hash giả cùng cost khi không có user hoặc không có `passwordHash`; `matches && passwordHash.length` | A `password.go`: `VerifyPassword(pw, hash)`; I `bcrypt.go` |
| `auth/utils.ts:642-647`, `constants.ts:71,81` | Cấm mật khẩu quá 72 **byte** (400 `Password must be no more than 72 characters`) ở điểm băm; bcrypt cost = `config.bcryptHashSalt` (mặc định 10, `config/utils.ts:36`) | D `password.go`: `ValidatePassword`; A `HashPassword`; I `bcrypt.go` |
| `auth/setpassword.ts:89-91`, `auth/newuser.ts:129-131` | `pwnedPassword(pw) > 0` thì 400 `Password found in breach database` (newuser thêm expression `password`) | A `ports.go`: `PwnedChecker.Count(ctx, pw) (int, error)`; I `hibp.go` |
| `auth/setpassword.ts:83-122` | `setPassword` (mục P1.4.6) | A `password.go`: `SetPassword` |
| `auth/setpassword.ts:128-149` | `revokeUserLogins` | A `password.go`: `RevokeUserLogins` |
| `auth/utils.ts:57-59`, `:205-208`, `:114-124`, `:319-324` | `hasSmartResourceScopes`, `isMfaRequired`, `getAllowedMfaMethods`, `getEnrolledMfaMethods` | D `scope.go`, `mfa.go` |
| `auth/utils.ts:455-465` | `getLoginProject`: không có `project.reference` hoặc `Project/new` thì nil; đọc lỗi thì nil | A `memberships.go`: `LoginProject` |

## P1.3 Mô hình dữ liệu

Kiểu Go đã có ở `pkg/fhir/resource_*.go`. Chỉ liệt kê trường **thực sự đọc (R) hoặc ghi (W)** trong phạm vi P1 và P2.

**User** (`resource_user.go`): R `id`, `email`, `passwordHash`, `project`, `mfaEnrolled`, `mfaRequired`, `mfaMethod`, `identifier` (me); W (ở P1/P2): `passwordHash`, `emailVerified` (qua `setPassword`, `auth/setpassword.ts:93-99`). `mfaSecret`, `externalId`, `admin` chưa dùng ở P1/P2 (P5, P6).
- Email lưu **chữ thường** (ràng buộc `user-1` ở `pkg/fhir/schema_gen.go:9238`; handler tạo user hạ chữ thường `auth/newuser.ts:68`). Tra cứu luôn `strings.ToLower`.

**Login** (`resource_login.go`): W khi tạo (`oauth/utils.ts:200-219`): `client`, `launch`, `project`, `profileType`, `user`, `authMethod`, `authTime`, `code`, `cookie`, `refreshSecret`, `scope`, `nonce`, `codeChallenge`, `codeChallengeMethod`, `remoteAddress`, `userAgent`, `pictureUrl`. W sau: `membership`, `project` (làm mới), `refreshSecret` (xóa nếu super admin), `scope`, `granted`, `revoked`. R: `revoked`, `granted`, `membership`, `user`, `project`, `client`, `authMethod`, `mfaVerified`, `emailMfa`, `code`, `cookie`, `scope`, `launch`, `profileType`, `remoteAddress`, `userAgent`, `authTime`, `meta.lastUpdated`, `meta.versionId`. Có sẵn nhưng chưa dùng ở P1/P2: `preAuthorizedCodeHash`, `expiresAt`, `admin`, `superAdmin`, `basedOn`, `previousRefreshSecret` (chưa xác nhận dùng ở đâu, có thể P3).
- `code`, `cookie`, `refreshSecret` là bí mật: không bao giờ vào log (`oauth/utils.ts:187` loại `password`, `codeChallenge` khỏi log).

**ProjectMembership** (`resource_projectmembership.go`): R `id`, `user`, `project`, `profile`, `active`, `identifier`, `admin` (me), `accessPolicy`, `access[].policy`, `access[].parameter`, `userConfiguration`. W: không có ở P1/P2 (tạo membership là P4, `auth/utils.ts:429-448`).

**Project** (`resource_project.go`): R `id`, `active`, `superAdmin`, `features` (`google-auth-required`, `oauth/utils.ts:550`), `setting` (các tên: `mfaRequired` :206, `allowedMfaMethods` :115, `googleAuthProfilePictures` :558), `name`, `description`, `strictMode` (me), `site[].recaptchaSiteKey/recaptchaSecretKey` (`auth/utils.ts:659-664`). W: không.

**ClientApplication** (`resource_clientapplication.go`): R `id`, `meta.project`, `allowedOrigin`, `redirectUri`, `redirectUris`, `pkceOptional`, `grantType`. `secret`/`retiringSecret` chỉ P3, và `/auth/me` phải **xóa** hai trường này (`auth/me.ts:55-59`).

**UserSecurityRequest** (`resource_usersecurityrequest.go`): R `id`, `type`, `user`, `secret`, `used`, `expiresAt`, `redirectUri`, `meta.lastUpdated`, `meta.versionId`; W `used`. Tạo bản ghi (`secret: generateSecret(16)`, `expiresAt`) ở P4 (`auth/resetpassword.ts:126-127`).

**DomainConfiguration** (`resource_domainconfiguration.go`): R `domain`, `identityProvider.authorizeUrl`, `identityProvider.clientId`.

**SmartAppLaunch** (`resource_smartapplaunch.go`): R theo id (`oauth/utils.ts:174-177,582`), dùng `patient` cho ngữ cảnh patient.

**Tham số tìm kiếm cần có**: toàn bộ đã được sinh ở `pkg/fhir/searchparams_gen.go` và `pkg/fhirstore` lập chỉ mục tự động mọi tham số qua `reindex` (`pkg/fhirstore/crud.go:295-315,344`), nên **không cần thêm tham số mới**:

| Resource | code (loại) | Dùng cho | Dòng sinh |
|---|---|---|---|
| User | `email` (string, `:exact`) | tra email | `searchparams_gen.go:480` |
| User | `project` (reference, `:missing`) | user theo project hoặc toàn cục | `:1017` |
| User | `_id` | đọc lại user sau khi tạo Login (`oauth/utils.ts:228-231`) | cột `id` |
| ProjectMembership | `user` (reference), `project` (reference), `active` (token, `:not`) | `getMembershipsForLogin` | `:1482`, `:1016`, `:38` |
| ProjectMembership | `profile` (reference) | P4 (`findProjectMembership`) | `:1008` |
| Login | `user` (reference), `_lastUpdated` (date, `gt`) | sessions của `/auth/me`, `revokeUserLogins` | `:1481`, `:9` |
| UserSecurityRequest | `user` (reference), `_lastUpdated` (date, `ge`) | `supersede`, `retire` | `:1483`, `:9` |
| DomainConfiguration | `domain` (token) | `/auth/method` | `:458` |
| Project | `recaptcha-site-key` (token), `_id` | `validateRecaptcha` | `:1063` |

Chỉ mục SQL hiện có là `(resource_type, code, norm|value|target)` (`pkg/fhirstore/migrations/postgres/00001_init.sql:33,43,53,63`), đủ cho các truy vấn trên. Truy vấn email `:exact` so cột `raw` (`pkg/fhirstore/search.go:337-338`) nhưng tiền tố chỉ mục là `norm`; với số user lớn nên đo (chưa xác nhận tác động hiệu năng).

**Cách tìm theo email/project**:
1. `email = lower(email)`.
2. Nếu có `projectId` và khác `new`: tìm `User` với `email:exact=<email>` và `project=Project/<projectId>`; có thì dùng.
3. Không có thì tìm `User` với `email:exact=<email>` và `project:missing=true`.
4. Lấy phần tử đầu; không đảm bảo duy nhất (Medplum không có ràng buộc duy nhất ở tầng DB, chưa xác nhận ở nơi khác; xem câu hỏi mở).
5. **Dựng `fhirsearch.Filter` trực tiếp** (`Code`, `Def` từ `fhirsearch.Lookup`, `Modifier`, `Values`) thay vì ghép chuỗi query, để email chứa `,` `|` `\` `$` không bị hiểu là cú pháp search (Medplum dùng `escapeSearchValue` cho cùng lý do, `oauth/utils.ts:934`). Cần test riêng cho điều này.

**Cách tìm membership của login**: `user=<login.user.reference>`, `active:not=false`, thêm `project=<login.project.reference>` khi có (`oauth/utils.ts:432-451`), `count=100` (cắt im lặng nếu hơn 100, nên ghi giới hạn này, chưa có test gốc), sau đó lọc `profileType` (`m.profile.reference` bắt đầu bằng `profileType`).

**Cách ly kho** (D2): kho `auth` là database hoặc schema riêng; bảng của `pkg/fhirstore` không có tiền tố schema (`00001_init.sql` tạo `fhir_resource` trần), nên cách ly thực tế là **database riêng hoặc `search_path` riêng** (chưa xác nhận `pkg/database` hỗ trợ schema). Thêm test e2e: `fhir-server` chạy với DB lâm sàng không thấy `User`, `Login`, `UserSecurityRequest` của `auth`.

## P1.4 Thuật toán các hàm khó

### P1.4.1 `SetLoginMembership(login, membership)` (`oauth/utils.ts:510-618`)

Thứ tự kiểm tra, mã HTTP đều 400 (`badRequest`) trừ khi ghi khác:
1. `login.revoked` thì `Login revoked`.
2. `login.granted` thì `Login granted`.
3. `login.membership` đã có thì `Login profile already set`.
4. `membership.user.reference != login.user.reference` thì `Invalid profile`.
5. Nếu `login.project.reference` có, khác `Project/new` và khác `membership.project.reference` thì `Invalid profile`.
6. Đọc `Project` theo `membership.project` (không có thì 404 `Not found`). `InactiveReason(project, membership)` khác rỗng thì lỗi đó (`Profile not active` hoặc `Project not active`).
7. `project.features` chứa `google-auth-required` và `login.authMethod != google` thì `Google authentication is required`.
8. (Cập nhật ảnh Google: `authMethod=google` và `pictureUrl` và setting `googleAuthProfilePictures`; lỗi chỉ ghi log. **Không port đợt này**, mục P1.8.)
9. Kiểm IP: đọc `userConfiguration` (nếu `membership.userConfiguration` có; đọc lỗi thì lỗi lan ra), đọc `SmartAppLaunch` nếu `login.launch`, dựng access policy (`getAccessPolicyForLogin`, `fhir/accesspolicy.ts:174-203`), rồi `checkIpAccessRules`. Trong bước dựng policy có các lỗi có thể xảy ra:
   - project `superAdmin` và (`login.launch` hoặc có scope SMART) thì `SMART scopes are not supported for super admin logins` (`accesspolicy.ts:176-179`);
   - đọc `AccessPolicy` tham chiếu lỗi thì `Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership.` (`accesspolicy.ts:287-299`);
   - có scope SMART `patient/` mà không có ngữ cảnh patient (từ `launch.patient` hoặc `membership.profile` dạng `Patient/<id hợp lệ>`) thì `Missing patient context for patient/ scope` (`fhir/smart.ts:204-207`, `:43`; còn một `throw` cùng loại ở `narrowEntry`, quanh `smart.ts:303`, chưa đọc kỹ);
   - rule IP chặn thì `IP address not allowed`.
   `ipAccessRule` chỉ được gom ở `accesspolicy.ts:222-259` và các bước sau giữ nguyên (`smart.ts:196-211`, `accesspolicy.ts:456-479`), nên resolver Go chỉ cần gom IP rule, thay biến, và hai lỗi trên.
10. Ghi `AuditEvent` thành công (`oauth/utils.ts:586-594`): P1 thay bằng log có cấu trúc (xem P1.8).
11. `updated = {...login, project: ref(project), membership: ref(membership)}`; nếu `project.superAdmin` thì bỏ `refreshSecret`.
12. `UpdateResource(updated, IfMatch = login.meta.versionId)`. Lỗi 412 thì `Login profile already set`. (So sánh-và-đổi: nhiều request đồng thời cho cùng một login thì đúng một cái thắng, `auth/profile.test.ts:172-201`.)

Ghi chú: `createReference(project)` có `display = project.name`; Go phải sinh `display` giống vậy khi lưu `Login.project` và `Login.membership` (chưa xác nhận Medplum có đặt `display` cho membership; `createReference` ở `core` chưa đọc).

### P1.4.2 `MembershipsForLogin(login)` (`oauth/utils.ts:423-474`)
1. `login.project.reference == "Project/new"` thì trả rỗng.
2. `login.user.reference` rỗng thì 400 `User reference is missing`.
3. Tìm membership theo mục P1.3 (user, `active:not=false`, project nếu có), `count=100`.
4. Nếu `login.profileType` có: giữ membership có `profile.reference` bắt đầu bằng `profileType`.
5. Đọc project của từng membership (`readReferences`). Membership được giữ khi project **không đọc được (lỗi)** hoặc `InactiveReason(project, nil)` rỗng.
6. Nếu có membership ban đầu mà sau lọc project còn 0 thì 400 `Project not active`.
7. Trả danh sách đã lọc.

### P1.4.3 `GetUserByEmail`: xem bảng mục P1.2.

### P1.4.4 `SetLoginScope(login, scope)` (`oauth/utils.ts:739-771`)
1. `revoked` thì 400 `Login revoked`. 2. `granted` thì 400 `Login granted`. 3. không có `membership` thì 400 `Login profile not set`.
4. `existing = parse(login.scope)`, `submitted = parse(scope)`. Nếu `len(existing) > 0` và `len(submitted) == 0` thì 400 `Invalid scope` (login có SMART scope không được bỏ hết).
5. `!isNonSmartScopeSubset(login.scope, scope)` thì 400 `Invalid scope` (mọi từ không phải SMART, trừ `openid`, phải có trong scope cũ; từ rỗng bỏ qua).
6. Mỗi `candidate` trong `submitted` phải khớp (`matchesScope`, không có ngữ cảnh patient) ít nhất một `existing`, nếu không 400 `Invalid scope`. Lưu ý quy tắc `matchesScope` (:644-668): `permissionType` khớp (system hẹp thành user; patient chỉ khi có ngữ cảnh, ở bước này luôn không có); `resourceType` khớp hoặc cái cũ là `*`; chuỗi quyền của candidate không rỗng và là tập con của cái cũ; nếu cái cũ có `params` thì candidate phải có **đúng** params (cùng độ dài, từng cặp bằng nhau).
7. **Patch** `/scope` (đọc lại từ kho rồi ghi, giữ `revoked`); trả bản mới.

### P1.4.5 `CheckIPAccessRules`: xem bảng, kèm bảng ca 7 dòng trong `oauth/utils.test.ts:27-64`.

### P1.4.6 `SetPassword(user, password, securityRequest?)` (`auth/setpassword.ts:83-122`)
1. `pwned.Count > 0` thì 400 `Password found in breach database`. (Làm **trước** khi tiêu thụ request: test `auth/setpassword.test.ts:567`.)
2. `HashPassword` (kiểm 72 byte, bcrypt).
3. Trong **một transaction** (`RunInTransaction`, `pkg/fhirrouter/router.go:94-98`): nếu có `securityRequest` thì `Consume` (CAS); `RetirePasswordSecurityRequests(user)`; patch User `passwordHash` (và `emailVerified=true` nếu có `securityRequest`).
4. Sau transaction: `RevokeUserLogins(user)`.
5. Nếu bước 3 thất bại thì request **không** bị tiêu thụ (`auth/setpassword.test.ts:611`).

`RevokeUserLogins`: Medplum duyệt phân trang theo `_lastUpdated` có con trỏ và ghi `revoked` (`:128-149`). Go không có `processAllResources` và phân trang offset sẽ **bỏ sót** bản ghi khi `lastUpdated` đổi (chính Medplum ghi chú điều này trong comment của `revokeUserLogins`, `auth/setpassword.ts:128-149`, và kiểm bằng `auth/setpassword.test.ts:267-315`); giải pháp: **gom hết id Login của user trước** (nhiều trang, `Count` tối đa `fhirsearch.MaxCount=1000`, `pkg/fhirsearch/request.go`), rồi mới patch từng cái.

### P1.4.7 Helper "patch an toàn" (cho mục 0.1)
`PatchResource(rt, id, mutate func(doc) error)`: lặp tối đa N lần: `ReadResource`; áp `mutate` trên bản **vừa đọc**; `UpdateResource(..., IfMatch=versionId)`; gặp `PreconditionFailed` thì đọc lại. Hết N lần thì lỗi 409/500 (chọn khi duyệt). Không bao giờ ghi lại bản đối tượng cũ của người gọi.

## P1.5 Cổng (ports) và adapter

`internal/application/ports.go` (khai báo cổng theo ngôn ngữ miền, **không** để `fhirsearch` lọt vào application):

| Cổng | Phương thức chính | Adapter (I) |
|---|---|---|
| `UserRepository` | `FindInProject`, `FindGlobal`, `Get`, `Reload(id)` (đọc bằng search để không qua cache, `oauth/utils.ts:222-231`), `Patch` | `store/users.go` trên `fhirstore.Store` |
| `LoginRepository` | `Create`, `Get`, `UpdateIfMatch`, `Patch`, `ListIDsByUser` | `store/logins.go` |
| `MembershipRepository` | `Get`, `ListForUser(userRef, projectRef, activeNotFalse)` | `store/memberships.go` |
| `ProjectRepository` | `Get`, `GetMany` (trả lỗi theo từng phần tử) | `store/projects.go` |
| `ClientRepository` | `Get` (client dựng sẵn trước) | `store/clients.go` + `domain.StandardClients` |
| `DomainConfigRepository` | `FindByDomain` | `store/domainconfig.go` |
| `SecurityRequestRepository` | `Get`, `MarkUsed(IfMatch)`, `ListUnusedSince(user, since)`, `PatchUsed` | `store/securityrequests.go` |
| `LaunchRepository` | `Get` | `store/launches.go` |
| `AccessPolicyRepository` | `Get` (cho `ResolveIPAccessRules`) | `store/accesspolicies.go` |
| `PasswordHasher` | `Hash`, `Compare` | `bcrypt.go` (`golang.org/x/crypto/bcrypt`, hiện là gián tiếp ở `go.mod:114`, cần chuyển thành trực tiếp) |
| `PwnedChecker` | `Count(ctx, pw)` | `hibp.go` (HTTP, k-anonymity) + fake |
| `SecretGenerator` | `Secret(n)` | `secrets.go` |
| `Clock` | `Now()` | `clock.go` |
| `Transactor` | `InTx(ctx, fn)` | bọc `fhirstore.Store.RunInTransaction` |

Thư viện Go đề xuất (kiến thức bên ngoài source, cần xác nhận khi cài): `golang.org/x/crypto/bcrypt`; HIBP gọi `https://api.pwnedpasswords.com/range/<5 ký tự đầu SHA-1>` bằng `net/http` chuẩn (không cần thêm thư viện; source Medplum dùng gói npm `hibp`, chưa đọc mã của gói). Hash `$2b$` sinh từ Node phải so khớp được ở Go: **chưa xác nhận**, cần test với một hash `$2b$` thật.

Test dùng `fhirrouter.MemoryRepository` (`pkg/fhirrouter/memory.go`) và bộ test chung `pkg/fhirrouter/repotest` cho hợp đồng adapter (chạy trên cả bộ nhớ lẫn SQLite).

## P1.6 Cấu hình (env)

Đề xuất tiền tố `AUTH_` (chưa xác nhận quy ước đặt tên với `pkg/envconfig`; theo `apps/fhir-server/internal/config/config.go`):

| Biến | Mặc định | Nguồn Medplum |
|---|---|---|
| `AUTH_DATABASE_URL` (+ nhóm `DATABASE_*` như fhir-server) | bắt buộc | kho riêng (D2) |
| `AUTH_BCRYPT_COST` | 10 | `config/utils.ts:36`, `config/types.ts:82` |
| `AUTH_PWNED_ENABLED` | true ở production | `hibp` luôn bật trong source, thêm cờ để test và dev |
| `AUTH_PWNED_URL`, `AUTH_PWNED_TIMEOUT` | api chính thức, 3s | ngoài source |
| `AUTH_PWNED_FAIL_OPEN` | false hoặc true (**câu hỏi mở**) | source không nói gì khi HIBP lỗi (chưa xác nhận hành vi của gói `hibp`) |
| `AUTH_DEFAULT_OAUTH_CLIENTS` (JSON) | rỗng | `oauth/clients.ts:30`, `config/types.ts:174` |
| `AUTH_CLI_CLIENT_ENABLED` | true | `oauth/clients.ts:20-27` (client `medplum-cli` luôn có trong Medplum) |

## P1.7 Danh sách ca test (đặc tả lấy từ test của Medplum)

Dòng có **BỎ** là ca bỏ hoặc hoãn, kèm lý do. Tên ca gốc giữ nguyên để đối chiếu.

**`auth/securityrequest.test.ts`** (9 ca, port hết, đơn vị)
- `getSecurityRequestExpiration` / Uses the window for the request type (`:18`): hạn `invite` >= now + 7 ngày.
- Reset expires sooner than invite (`:24`): hằng số reset < invite.
- `isSecurityRequestExpired` / Unexpired (`:30`): `expiresAt` tương lai thì false.
- Expired (`:35`): quá khứ thì true.
- Unparseable expiresAt is expired (`:40`): `not-a-date` thì true.
- Falls back to lastUpdated when expiresAt is absent (`:44`): lastUpdated 1 phút trước thì chưa hết; quá cửa sổ reset thì hết.
- Fallback uses the window for the request type (`:52`): `invite` và `verify-email` cùng mốc cũ thì chưa hết.
- Missing type falls back to the reset window (`:58`): không `type` thì hết.
- No timestamps at all is expired (`:63`): trống thì hết.

**`auth/utils.test.ts`** (đơn vị)
- isMfaRequired: Falls back to user setting... (`:28`), Project setting requires MFA regardless... (`:35`), Project setting can only tighten... (`:42`): bảng chân trị của `IsMfaRequired`.
- bcryptHashPassword: Rejects a password longer than the maximum byte length (`:180`): 73 byte thì lỗi `Password must be no more than 72 characters`; Measures length in bytes, not code points (`:188`): 19 emoji (76 byte) thì lỗi.
- hasSmartResourceScopes (`:203`): 6 dòng bảng (`undefined`, `openid`, scope không SMART, `user/Observation.rs`, `patient/*.rs`, `user/*.*`).
- getCallerUserReference (`:227`, `:231`): chuyển sang P2 (cần `AuthState`).
- `getOpenRegistrationAccessPolicy`, `isPractitionerEmailDomainAllowed` (`:54-175`): **BỎ ở P1/P2**, thuộc P4 (đăng ký).

**`oauth/utils.test.ts`** (đơn vị, phần P1)
- IP access rules (`:27`): 7 dòng `[rule, address, khớp]`; `block` + khớp thì lỗi `IP address not allowed`; `allow` + `*:block` thì khớp qua, không khớp bị chặn.
- Missing codeChallengeMethod (`:338`): có `codeChallenge` mà thiếu method thì lỗi expression `code_challenge_method` (client `undefined`).
- Missing codeChallenge (`:360`): có method thiếu challenge thì expression `code_challenge`.
- Invalid codeChallengeMethod (`:382`): `xyz` thì expression `code_challenge_method`.
- Plain text / S256 code challenge method (`:405`, `:423`): hợp lệ.
- Client application PKCE optional (`:441`): `pkceOptional` thì bỏ qua mọi kiểm tra.
- getMembershipsForLogin missing user reference (`:493`): `User reference is missing`.
- getMembershipsForLogin excludes inactive memberships (`:503`) và includes memberships without active field (`:552`).
- isSmartScopeSubset (`:835`): 10 dòng bảng.
- CLI client (`:828`): `getClientApplication('medplum-cli')` có `id` đúng.
- getAuthTokens / mọi ca (`:599-826`): **BỎ ở P1** (ký token, P3); riêng `Login missing profile` (`:599`): giữ ở `GrantLogin` (lỗi `Login missing profile` khi thiếu membership, `oauth/utils.ts:787-789`).
- verifyMfaToken ca (`:463-491`): **BỎ**, P5.

**Mật khẩu và pwned** (đơn vị, tách từ các file P4)
- Password too short / too long / Multibyte password too long / Multibyte password too short (`auth/newuser.test.ts:126-186`): `ValidatePassword`: `xyz` thì `Password must be at least 8 characters`; `xyz`x100 thì `Password must be no more than 72 characters`; 40 lần `☺️` (80 byte) thì lỗi "no more than"; `🔑🔓` (2 ký tự, 8 byte) thì "at least 8 characters" (**min tính theo ký tự, max tính theo byte**).
- Breached password (`auth/setpassword.test.ts:744`), Breached password does not consume the UserSecurityRequest (`:567`), Failure to apply the password does not consume the UserSecurityRequest (`:611`): `SetPassword` với fake pwned và fake lỗi ghi.
- Setting a password revokes all logins (`:267`): hơn một trang login (25 rồi 45) đều bị `revoked`.
- Setting a password retires earlier links (`:184`), without a link retires earlier links (`:232`): `RetirePasswordSecurityRequests`.
- UserSecurityRequest cannot be used twice (`:525`): CAS thua thì `Already used`.
- Các ca còn lại của `setpassword.test.ts` (`Success :59`, `UserSecurityRequest :141`, Expired `:443`, `:486`, invalid type `:675`, Wrong secret `:710`, validators `:783-829`, Not found `:829`): **HOÃN sang P4** (handler HTTP).

**Race tập trung vào `tryLogin`** (`auth/setpassword.test.ts`): ba ca ở mục P2.7.

**Ca mới bắt buộc (không có ở Medplum)**:
- Cách ly kho: `fhir-server` không đọc được `User`/`Login` của `auth`.
- `Reload(id)` đọc từ kho, không từ cache.
- Filter email chứa `,` `|` `\` `$` không bị hiểu thành cú pháp search.
- Hash `$2b$` từ Node so khớp được.
- Helper patch: hai goroutine patch hai trường khác nhau của cùng Login, cả hai thay đổi còn.

## P1.8 Không port đợt này và điểm lệch chấp nhận được

Không port (kèm lý do):
- Cập nhật ảnh profile từ Google (`oauth/utils.ts:555-574`): cần đọc và ghi resource lâm sàng, thuộc P6 (Google).
- `getUserByExternalId` (:860-900), `getClientApplicationMembership` (:483-499): thuộc P6 và P3.
- Ghi `AuditEvent` bằng `createAuditEvent`/`logAuditEvent` (`oauth/utils.ts:586-594`; mã `util/auditevent` **chưa đọc**): P1 thay bằng log có cấu trúc (ghi đủ `loginId`, `projectId`, `profile`, `remoteAddress`, kết quả); quyết định có ghi `AuditEvent` thật sau.
- Shard (`getProjectSystemRepo`, `fhir/repo.ts:3363-3370` luôn trả shard toàn cục): bỏ.
- Cache đọc (`Reload` qua search thay cho `readResource` nhằm tránh cache, `oauth/utils.ts:226-227`): Go không có cache nên đọc thẳng DB là đủ.

Lệch chấp nhận được (đề xuất, người duyệt chốt):
1. `revokeLogin` (`oauth/utils.ts:845-850`) ghi cả đối tượng (có thể ghi đè thay đổi đồng thời như xoay `refreshSecret`); Go dùng helper patch, chỉ đặt `revoked=true`. Lệch an toàn hơn.
2. Bỏ đọc `userConfiguration` trong `SetLoginMembership` (`oauth/utils.ts:580`): Medplum làm đăng nhập thất bại nếu `membership.userConfiguration` trỏ vào resource không đọc được; Go chỉ đọc nó ở `/auth/me`. Lệch nhỏ, ghi để người duyệt xác nhận.
3. Thứ tự tham số query của `authorizeUrl` ở `/auth/method` (Go `url.Values.Encode` sắp xếp theo khóa; JS giữ thứ tự chèn): P2.
4. Thông điệp lỗi, mã HTTP, id UUID dạng v4 (`pkg/fhirstore/store.go:142-148`) giữ nguyên Medplum để client không đổi.

## P1.9 Rủi ro và câu hỏi mở

1. **Email trùng**: Medplum không có ràng buộc duy nhất ở tầng DB cho `User.email` (chưa xác nhận ở `auth/newuser.ts` ngoài phần đã đọc). Go dùng `fhirstore` không có chỉ mục duy nhất; hai request đăng ký đồng thời có thể tạo hai user. Có chấp nhận ở P1 và xử lý ở P4 bằng `KeyLocker.LockKey` (`pkg/fhirrouter/router.go:103-105`) không?
2. Hành vi khi HIBP lỗi mạng: chặn đăng ký/đổi mật khẩu (fail đóng) hay cho qua (fail mở)? Source không nói (gói `hibp` chưa đọc).
3. `SetPassword` có vào P1 (đề xuất) hay đợi P4?
4. Ranh giới domain: cho `internal/domain` import `pkg/fhir` (kiểu thuần) hay định nghĩa kiểu riêng? Đề xuất cho phép `pkg/fhir` ở domain/application/infrastructure, còn `pkg/fhirrouter`, `pkg/fhirsearch`, `pkg/fhirstore` chỉ ở infrastructure. Cần thêm luật vào `architecture_test.go` (hiện chỉ có luật cho fhir-server ở `architecture_test.go:34-42`).
5. Cách ly kho: dùng database riêng hay `search_path`? `pkg/database` có hỗ trợ không (chưa xác nhận).
6. Có cần chặn thêm ở `fhir-server` (từ chối các loại `User`, `Login`, `UserSecurityRequest`, `ClientApplication`) như lớp phòng thủ thứ hai? Việc này sửa ngoài `apps/auth`.
7. Số lần thử lại của helper patch và mã lỗi khi hết lần thử.

## P1.10 Ước lượng và thứ tự

Cỡ: **M** (2 đến 4 ngày) nếu `SetPassword` để P4; **M đến L** nếu kéo vào.

| Bước | Nội dung | Tiêu chí hoàn thành |
|---|---|---|
| 1.1 | Domain thuần: lỗi, `ValidatePassword`, `secret`, `ConstantTimeEqual`, IP, PKCE, scope SMART, MFA helper, security-request expiry, `InactiveReason`, `StandardClients`, redirect URI | Mọi ca test đơn vị mục P1.7 (securityrequest, utils, oauth/utils đơn vị, password) xanh; `architecture_test.go` có luật `domain` chỉ import stdlib và `pkg/fhir` |
| 1.2 | Cổng, adapter `fhirstore`, fake bộ nhớ, helper patch an toàn | Bộ test hợp đồng chạy trên `MemoryRepository` và SQLite; ca patch song song xanh; ca email có ký tự đặc biệt xanh |
| 1.3 | Application: tra cứu user, `MembershipsForLogin`, `SetLoginMembership` (+ resolver IP), `SetLoginScope`, `RevokeLogin`, `GrantLogin`, `ResolveProjectID` | Test cấp application tái hiện các ca `profile.test.ts` (không cần HTTP), gồm CAS đồng thời |
| 1.4 | Mật khẩu: `HashPassword`, `VerifyPassword` (hash giả), `PwnedChecker` (+ HIBP adapter, test không gọi mạng), security request consume/supersede/retire, `SetPassword`, `RevokeUserLogins` | Ca `setpassword.test.ts` đã chọn xanh; hash `$2b$` Node so khớp; không có lời gọi mạng trong test |
| 1.5 | Cách ly kho: cấu hình DB riêng, test e2e `fhir-server` không thấy resource nhạy cảm | Test e2e xanh; ghi quyết định D2 vào tài liệu |

---

# PHẦN P2: Đăng nhập lõi

## P2.1 Mục tiêu và phạm vi

Route (đăng ký ở `auth/routes.ts:27-48`):

| Route | Medplum | Xác thực |
|---|---|---|
| `POST /auth/login` | `auth/login.ts:28-67` | công khai |
| `POST /auth/profile` | `auth/profile.ts:23-40` | công khai |
| `POST /auth/scope` | `auth/scope.ts:21-26` | công khai |
| `POST /auth/method` | `auth/method.ts:23-35` | công khai |
| `GET /auth/login/:login` | `auth/status.ts:19-36` | công khai |
| `GET /auth/me` | `auth/me.ts:41-140` | Bearer (`authenticateRequest`) |
| `POST /auth/revoke` | `auth/revoke.ts:19-58` | Bearer |

Hàm: `tryLogin` (`oauth/utils.ts:155-256`), `validateLoginRequest` (:258), `validatePkce` (:281), `sendLoginResult` (`auth/utils.ts:474-541`), `sendLoginCookie` (:548), `validateRecaptcha` (:649-690) và `verifyRecaptcha` (:566), `getProjectIdByClientId` (:586), `isExternalAuth` (`auth/method.ts:42`), `getDomainConfiguration` (:72), `getCallerUserReference` (`auth/utils.ts:69`), `canManageUserAccount` (:83), `getUserConfiguration` và `getUserConfigurationMenu` (`auth/me.ts:142-201`), `getSessions` (:210-256), `isMfaRequired` (đã ở P1).

`validateRecaptcha` được port ở P2 nhưng chỉ gắn vào route ở P4 (`newuser`, `resetpassword`: `auth/routes.ts:33,40`).

## P2.2 Bảng ánh xạ source sang Go

| Medplum | Hành vi | Go đích (P = `internal/presentation/httpapi`) |
|---|---|---|
| `auth/login.ts:14-26` | Validator `email`, `projectId`, `password`, `redirectUri` (mục P2.4.1) | P `login.go`: DTO `LoginRequest` + `validateLogin` |
| `auth/login.ts:28-67` | Gọi `getProjectIdByClientId` ngoài try, `tryLogin`, ghi log, `sendLoginResult` | P `login.go`: handler; A `login.go`: `Login(ctx, in) (LoginResult, error)` |
| `oauth/utils.ts:155-256` | `tryLogin` (mục P2.4.2) | A `trylogin.go`: `TryLogin(ctx, domain.LoginRequest) (fhir.Login, error)` |
| `oauth/utils.ts:258-279` | `validateLoginRequest` | D `loginrequest.go`: `ValidateLoginRequest` |
| `oauth/utils.ts:281-301` | `validatePkce` | D `pkce.go` |
| `auth/utils.ts:474-541` | `sendLoginResult` (mục P2.4.3): tách thành quyết định thuần và dựng response | D `loginresult.go`: `Decide(user, project, login) Kind`; A `loginresult.go`: `BuildLoginResult(ctx, login)` |
| `auth/utils.ts:548-558` | Cookie `medplum-<clientId>` nếu có `login.client` | P `cookie.go`: `setLoginCookie(c, login)` |
| `auth/profile.ts:18-40` | Validator `Missing login`/`Missing profile`, đọc login, đọc membership (mọi lỗi thành `Profile not found`), `setLoginMembership`, `sendLoginResult` | P `profile.go`; A `profile.go`: `SelectProfile` |
| `auth/scope.ts:16-26` | Validator `Missing login`/`Missing scope`, đọc login, `setLoginScope`, `sendLoginResult` | P `scope.go`; A `scope.go` (dùng `SetLoginScope` của P1) |
| `auth/status.ts:17-36` | Param `login` là UUID, `granted` rồi `revoked`, `sendLoginResult` | P `status.go`; A `status.go`: `LoginStatus` |
| `auth/method.ts:19-85` | `isExternalAuth` + `getDomainConfiguration` | P `method.go`; A `method.go`: `LoginMethod(email)` |
| `auth/revoke.ts:15-58` | Revoke session (mục P2.4.5) | P `revoke.go`; A `revoke.go`: `RevokeSession` |
| `auth/utils.ts:57,69-75,83-85` | `hasSmartResourceScopes`, `getCallerUserReference` (403 nếu on-behalf-of, membership không phải `User/`, hoặc có SMART scope), `canManageUserAccount` (`password` hoặc `google`) | D `caller.go` |
| `auth/me.ts:41-140` | `/auth/me` (mục P2.4.6) | P `me.go`; A `me.go`: `GetMe` |
| `auth/me.ts:142-201` | `getUserConfiguration` (mặc định `menu`) | A `userconfig.go`: `UserConfigurationFor`; D `menu.go`: `DefaultMenu(project, membership)` |
| `auth/me.ts:210-256` | `getSessions`: Login của user, `_lastUpdated > now-1h`; bỏ login chưa `granted`, đã `revoked`, hoặc không có `membership`; lọc theo tập membership nếu không `canManageUserAccount`; thêm `browser`, `os` từ User-Agent | A `sessions.go`; I `useragent.go` |
| `auth/utils.ts:566-577` | `verifyRecaptcha`: POST siteverify, đọc `success` | I `recaptcha.go` (cổng `Recaptcha.Verify`) |
| `auth/utils.ts:609-630` | `getProjectByRecaptchaSiteKey`: `recaptcha-site-key` (+ `_id` nếu có `projectId`) | A `recaptcha.go`; I `store/projects.go` |
| `auth/utils.ts:649-690` | Middleware `validateRecaptcha` (mục P2.4.7) | P `recaptcha_mw.go` |
| `fhir/outcomes.ts:10-29,39-61`; `core/outcomes.ts:279-293,471-505,537-545` | Lỗi trả `OperationOutcome` `application/fhir+json`; `getStatus` theo `id`; mặc định 400 (500 nếu `code=exception`); lỗi lạ thành `badRequest(normalizeErrorString)` | P `errors.go`: `writeOutcome`, `fromError` |

## P2.3 Mô hình dữ liệu

Trường đọc/ghi: xem P1.3 (P2 dùng lại nguyên). Bổ sung riêng cho P2:
- `LoginRequest` (`oauth/utils.ts:76-100`): `email`, `externalId`, `authMethod`, `password`, `scope`, `nonce`, `resourceType`, `projectId`, `membershipId`, `clientId`, `launchId`, `codeChallenge`, `codeChallengeMethod`, `redirectUri`, `googleCredentials`, `remoteAddress`, `userAgent`, `allowNoMembership`, `origin`, `pictureUrl`, `forceUseFirstMembership`, `remember`. P2 chỉ cần nhóm mật khẩu; giữ các trường `externalId`, `membershipId`, `googleCredentials`, `forceUseFirstMembership` trong struct để P6 không phải đổi chữ ký, nhưng chưa nối.
- Nguồn của từng trường từ HTTP (`auth/login.ts:28-60`): `resourceType`←body, `clientId`←body, `projectId`←`getProjectIdByClientId`, `scope`←`body.scope || 'openid'`, `nonce`←`body.nonce || randomUUID()`, `launchId`←`body.launch`, `remoteAddress`←`req.ip`, `userAgent`←header `User-Agent`, `origin`←header `Origin`, `allowNoMembership`←`body.projectId === 'new'`, `authMethod='password'`.
- Response `LoginResult` (5 dạng): xem `auth-dto.md` và mục P2.4.3. JSON thành công là `application/json` (Express `res.json`), còn lỗi là `application/fhir+json` (`fhir/outcomes.ts:57-60`).
- Tìm Login của user cho sessions: `user=<ref>` + `_lastUpdated gt <now-1h>` (`auth/me.ts:216-230`).
- Cần Binary/attachment: **không** (mục P2.8).

## P2.4 Thuật toán các hàm khó

### P2.4.1 Validator `/auth/login` (`auth/login.ts:14-26`, `util/validator.ts:8-20`)
Mọi runner chạy rồi gom lỗi; có lỗi thì trả `400` `OperationOutcome` với mỗi lỗi một `issue {severity:'error', code:'invalid', expression:[<trường>], details.text:<thông điệp>}` (`fhir/outcomes.ts:10-29`).
1. `email`: phải là email hợp lệ, thông điệp `Valid email address is required` (rỗng cũng lỗi này, `auth/login.test.ts:109-129`).
2. `projectId`: bỏ qua nếu falsy; còn lại phải là chuỗi bằng `new` hoặc UUID; sai thì `Invalid projectId`.
3. `password`: độ dài tối thiểu **8 ký tự** (đếm ký tự, `🔑🔓` bị loại, `auth/newuser.test.ts:171-186`) thông điệp `Password must be at least 8 characters`; tối đa **72 byte** thông điệp `Password must be no more than 72 characters`. Thiếu hoặc rỗng thì lỗi "at least" (`auth/login.test.ts:131-140`).
4. `redirectUri`: bỏ qua nếu falsy; còn lại phải là chuỗi, sai (ví dụ object) thì `Invalid redirectUri` (`auth/login.test.ts:176-183`).
Chưa xác nhận: thứ tự nhiều lỗi cùng lúc (test chỉ kiểm `issue[0]`); tương đương của `isEmail` của `validator.js` so với `net/mail` hoặc thư viện Go (cần bảng ca khác biệt).

### P2.4.2 `TryLogin(request)` (`oauth/utils.ts:155-256`)
1. `ValidateLoginRequest` (:258-279), kiểm theo chuỗi else-if, lỗi 400 kèm expression:
   a. `projectId` có, khác `new`, không phải UUID thì `Invalid projectId` (expression `projectId`);
   b. nếu `authMethod` là `external`/`exchange`: thiếu cả `externalId` và `email` thì `Missing email or externalId` (`externalId`); có `externalId` mà thiếu `projectId` thì `Project ID is required for external ID` (`projectId`) (chưa nối ở P2);
   c. else: `!email` thì `Invalid email` (`email`); `!authMethod` thì `Invalid authentication method` (`authMethod`); `password` mà `!password` thì `Invalid password` (`password`); `google` mà thiếu thông tin thì `Invalid google credentials` (`googleCredentials`); `!scope` thì `Invalid scope` (`scope`).
2. Nếu có `clientId`: `GetClientApplication` (không có thì 404 `Not found`). Nếu `client.allowedOrigin` **tồn tại** (mảng rỗng vẫn tính là tồn tại trong JS, `oauth/utils.ts:161`) và `request.origin` có và không nằm trong danh sách thì 400 `Invalid origin`. Nếu `redirectUri` có và không khớp **chính xác** URI đã đăng ký thì 400 `Invalid redirect URI`.
3. `ValidatePKCE(request, client)` (:281-301): nếu `client.pkceOptional` thì bỏ qua; `!codeChallenge && codeChallengeMethod` thì `Invalid code challenge` (`code_challenge`); `codeChallenge && !codeChallengeMethod` thì `Invalid code challenge method` (`code_challenge_method`); method có mà khác `plain` và `S256` thì `Invalid code challenge method` (`code_challenge_method`).
4. Có `launchId` thì đọc `SmartAppLaunch` (không có hoặc id sai thì 404 `Not found`).
5. Tìm user: có `externalId` thì theo externalId (P6); ngược lại có `email` thì `FindUserByEmail(email, projectId)`.
6. Không thấy user: ghi log cảnh báo (bỏ `password`, `codeChallenge`); nếu có `password` thì **chạy một lần bcrypt với hash giả** rồi 400 `Email or password is invalid`; nếu không có `password` thì 400 `User not found`.
7. `authenticate` (:315-334): có `password` thì so bcrypt (hash giả nếu `user.passwordHash` rỗng; kết quả đúng chỉ khi khớp **và** hash không rỗng), sai thì 400 `Email or password is invalid`; không có `password` mà có `googleCredentials` hoặc `authMethod` là `external`/`exchange` thì cho qua (đã xác minh ở nơi khác); còn lại 400 `Invalid authentication method`.
8. `refreshSecret = GenerateSecret(32)` nếu `IncludeRefreshToken`, ngược lại rỗng.
9. Tạo `Login` với các trường ở mục P1.3 (`code=GenerateSecret(16)`, `cookie=GenerateSecret(16)`, `authTime=now` ISO, `project={reference:'Project/'+projectId}` nếu có `projectId` kể cả `new`).
10. Nếu có `password`: **đọc lại User từ kho** (search theo `_id`, không dùng bản đã đọc); nếu `passwordHash` khác bản đã dùng để xác thực thì patch `revoked=true` lên Login vừa tạo và 400 `Email or password is invalid`. (Phải sau bước 9, xem chú thích :221-227: nhờ đó, hoặc việc đổi mật khẩu thấy login này để thu hồi, hoặc login này thấy mật khẩu mới.)
11. `MembershipsForLogin(login)`; có `membershipId` thì lọc theo id (P6).
12. Không còn membership và `!allowNoMembership` thì 400 `User not found`. (Login đã tạo ở bước 9 không bị thu hồi và không trả `code` ra ngoài; sẽ tích tụ rác, xem P2.9.)
13. Đúng một membership (hoặc `forceUseFirstMembership`) thì trả `SetLoginMembership(login, memberships[0])`; ngược lại trả `login` (nhiều membership, hoặc `projectId=new`). Lưu ý: `forceUseFirstMembership` với danh sách rỗng sẽ truy cập `memberships[0]` rỗng (lỗi runtime ở source); Go phải chặn (chỉ liên quan P6).

### P2.4.3 `BuildLoginResult(login)` (`auth/utils.ts:474-541`)
Đọc `user` (qua `login.user`), `project` qua `LoginProject` (lỗi thì nil). Xét theo thứ tự, dạng đầu khớp thì trả:
1. `IsMfaRequired(user, project)` **và** `!user.mfaEnrolled` **và** `login.authMethod == 'password'` **và** `!login.mfaVerified`: trả `{login:id, mfaEnrollRequired:true, enrollUri, enrollQrCode, allowedMfaMethods}`. `enroll*` do `buildTotpEnrollment` (`auth/utils.ts:229-248`, Redis + otplib + qrcode, thuộc P5).
2. `user.mfaEnrolled` **và** `authMethod == 'password'` **và** `!login.mfaVerified`: `mfaMethods = EnrolledMfaMethods(user)`; nếu chỉ có `email` và `login.emailMfa` chưa có thì gửi mã email ngay (`sendMfaEmailCode`, :346-380, P5); trả `{login, mfaRequired:true, mfaMethods, email:user.email}`.
3. `login.project.reference == 'Project/new'`: trả `{login:id}`.
4. `login.membership` có: gắn cookie (P2.4.4), trả `{login:id, code:login.code}`.
5. Còn lại: `MembershipsForLogin(login)`; gom project duy nhất theo `m.project.reference`; đọc lại các project; project đọc lỗi thì không có trong bản đồ; trả `{login:id, memberships:[{id, project, profile, identifier}]}` với `project` = `Reference{reference, display=project.name}` lấy mới từ DB (nếu project không đọc được, source gán `undefined`, đồng nghĩa trường `project` biến mất; Go nên giữ y hệt để không đổi hợp đồng, chưa có test gốc cho ca này).

**Quyết định cho P2** (nhánh 1 và 2 khi chưa có P5): cổng `MFAGate` với hai phương thức `BuildEnrollment` và `SendEmailCode`. Adapter mặc định của P2 trả lỗi 500 và **không** trả `code` (fail đóng). Test cho hai nhánh dùng fake. Người duyệt xác nhận.

### P2.4.4 Cookie (`auth/utils.ts:548-558`)
Chỉ khi `login.client` có: tên `medplum-` + `resolveId(login.client)`, giá trị `login.cookie`, `maxAge` 3600 giây (Express nhận mili giây: `3600*1000`), `SameSite=None`, `Secure`, `HttpOnly`. Go (Echo) đặt `MaxAge=3600` (giây) và `Path="/"` tường minh. Cookie `Secure` không được lưu trên HTTP thuần: dev cần cờ cấu hình (P2.6).

### P2.4.5 `/auth/revoke` (`auth/revoke.ts:19-58`)
1. Không có xác thực thì `401` (`oauth/middleware.ts:42-57`, kèm header `WWW-Authenticate`).
2. Validator `loginId` phải là UUID, thông điệp `Login ID is required.` (400).
3. `getCallerUserReference`: on-behalf-of, hoặc `membership.user` không bắt đầu `User/`, hoặc login của người gọi có scope SMART thì `403 Forbidden`.
4. Đọc `Login` theo `loginId` (không có thì 404 `Not found`).
5. `login.user.reference != userRef.reference` thì **404** (`sendOutcome(notFound)`, không ném lỗi).
6. Nếu `authMethod` của login người gọi **không** phải `password`/`google`: phải tìm được `ProjectMembership` có `_id=login.membership` và `project=<project của người gọi>`; không có (hoặc `login.membership` rỗng) thì ghi log cảnh báo `Revoke denied` và 404.
7. `RevokeLogin`; trả `200` `allOk`.
Ghi chú: ca "Different user" ở `auth/revoke.test.ts:106-201` cần `X-Medplum-On-Behalf-Of` và `preauthorize` (P6, mục P2.7 BỎ).

### P2.4.6 `/auth/me` (`auth/me.ts:41-140`)
1. Từ `AuthState`: `login`, `membership`, `project`, `onBehalfOfMembership`.
2. Đọc `profile` qua `readMembershipReference(membership.profile, [Bot, ClientApplication, Patient, Practitioner, RelatedPerson], allowLinkedProjects)`: kiểu ngoài danh sách thì 404; profile phải thuộc project của membership (hoặc project liên kết, nếu không thì 404, `fhir/projectmembership.ts:89-119`).
3. Login có scope SMART và người gọi không đọc được kiểu profile theo access policy thì thu gọn profile còn `{resourceType, id}`; sau đó `removeHiddenFields` theo access policy; `ClientApplication` thì xóa `secret`, `retiringSecret`.
4. `config = getUserConfiguration`; `accessPolicy = getAccessPolicyForLogin`.
5. `user` chỉ có khi không on-behalf-of và `membership.user` bắt đầu `User/`; `security` chỉ có khi có `user` và login **không** có SMART scope: `memberships` (user + project hiện tại, bỏ `active=false`, chỉ `resourceType`, `id`, `identifier`, `profile`, `admin`), `sessions`, `mfaEnrolled`, `mfaRequired = IsMfaRequired`.
6. Body: `{user:{resourceType,id,email,identifier}, project:{resourceType,id,name,features,description,strictMode,superAdmin}, membership:{resourceType,id,identifier,user,profile,admin}, profile, config, accessPolicy, security}`, qua `rewriteAttachments(PRESIGNED_URL)`.

Phụ thuộc chưa có ở Go: dựng access policy đầy đủ, `removeHiddenFields`, presigned URL, `X-Medplum-On-Behalf-Of`, project liên kết. Xem P2.8 và P2.9.

### P2.4.7 `validateRecaptcha(projectValidation?)` (`auth/utils.ts:649-690`)
1. `secretKey = config.recaptchaSecretKey`.
2. Nếu `body.recaptchaSiteKey` có và khác `config.recaptchaSiteKey`: tìm project theo `recaptcha-site-key` (+`_id=body.projectId`); không có thì 400 `Invalid recaptchaSiteKey`; lấy `secretKey` từ `project.site[].recaptchaSecretKey` khớp site key, không có thì 400 `Invalid recaptchaSecretKey`; chạy `projectValidation(project)` (ví dụ `projectRegistrationAllowed`) nếu có, trả outcome của nó khi khác rỗng.
3. Nếu `secretKey` rỗng thì bỏ qua kiểm tra (xem test `auth/newuser.test.ts:644`).
4. Ngược lại: thiếu `recaptchaToken` thì 400 `Recaptcha token is required`; `Verify` sai thì 400 `Recaptcha failed`.

### P2.4.8 `/auth/method` (`auth/method.ts:23-85`)
1. Validator: `email` là email hợp lệ, thông điệp `Valid email address is required`.
2. `domain = email.split('@')[1]` (không hạ chữ thường để **trả về**), tìm `DomainConfiguration` với `domain` token = `lower(domain)`.
3. Không thấy, hoặc `identityProvider.authorizeUrl` hoặc `clientId` rỗng thì `200 {}`.
4. Dựng URL từ `authorizeUrl`: nếu không phân tích được (ví dụ thiếu giao thức) thì `400 "Failed to construct URL for the domain"`; đặt `client_id`, `redirect_uri = baseUrl + 'auth/external'`, `response_type=code`, `scope='openid profile email'`; trả `200 {domain, authorizeUrl}`.
Go: `url.Parse` chấp nhận `example.com/oauth2/authorize`, nên phải yêu cầu `Scheme` và `Host` không rỗng để khớp (`auth/method.test.ts:95-123`).

### P2.4.9 `GET /auth/login/{login}` (`auth/status.ts:17-36`)
`login` phải là UUID (`Login ID is required`, 400); đọc login (404 nếu không có); `granted` thì 400 `Login already granted`; `revoked` thì 400 `Login revoked`; còn lại `BuildLoginResult`. (Router trả 404 khi thiếu tham số: `auth/status.test.ts:36`.)

## P2.5 Cổng (ports) và adapter

Ngoài các cổng ở P1.5, P2 thêm:

| Cổng | Dùng cho | Adapter |
|---|---|---|
| `MFAGate` (`BuildEnrollment`, `SendEmailCode`) | nhánh 1, 2 của `BuildLoginResult` | P2: adapter fail đóng; P5: TOTP + Mailer thật |
| `Authenticator` (`Authenticate(ctx, req) (AuthState, error)`) | `/auth/me`, `/auth/revoke` | P2: fake trong test; P3: xác minh JWT bằng khóa của `auth` |
| `Recaptcha` (`Verify(ctx, secret, token) (bool, error)`) | `validateRecaptcha` | `recaptcha.go` (`net/http`, `https://www.google.com/recaptcha/api/siteverify`, `auth/utils.ts:567-574`) + fake |
| `UserAgentParser` (`Parse(ua) (browser, os)`) | `sessions` của `/auth/me` | `useragent.go`; Medplum dùng gói `bowser` (`auth/me.ts:15,243-251`); gợi ý thư viện Go `github.com/mileusna/useragent` (ngoài source, cần xác nhận giấy phép) hoặc tự viết tối thiểu |
| `AccessPolicyResolver` (`ForLogin(ctx, state) (AccessPolicy, error)`) | `/auth/me.accessPolicy` | P1 có phần IP; phần còn lại cần quyết định (P2.9) |
| `ProfileReader` (`Read(ctx, membership, ref)`) | `/auth/me.profile` | phụ thuộc quyết định kho (P2.9) |

Thư viện HTTP: Echo v4 (`go.mod:9`), swaggo cho OpenAPI như `fhir-server`. Validator: tự viết hàm thuần trong `presentation/httpapi` để ra đúng thông điệp và `expression` (thư viện `go-playground/validator` không cho thông điệp từng quy tắc theo ý; chưa xác nhận lựa chọn cuối).

## P2.6 Cấu hình (env) cần thêm

| Biến | Mặc định | Nguồn |
|---|---|---|
| `AUTH_BASE_URL` | bắt buộc | `getConfig().baseUrl` dùng ở `auth/method.ts:57` (`baseUrl + 'auth/external'`, nên cần dấu `/` cuối hoặc dùng nối URL chuẩn) |
| `AUTH_TRUSTED_PROXIES` / extractor IP | không tin proxy | `req.ip` ở `auth/login.ts:56`; cách Medplum cấu hình `trust proxy`: **chưa xác nhận** (không tìm thấy trong `src` bằng grep `trust proxy`) |
| `AUTH_RECAPTCHA_SITE_KEY`, `AUTH_RECAPTCHA_SECRET_KEY` | rỗng (tắt) | `config/types.ts:64-65`, `auth/utils.ts:651-655` |
| `AUTH_RECAPTCHA_URL`, `AUTH_HTTP_TIMEOUT` | URL Google, 5s | ngoài source (để test không gọi mạng) |
| `AUTH_COOKIE_SECURE` | true | `auth/utils.ts:554`; cho phép tắt ở môi trường dev HTTP |
| `AUTH_LOGIN_RETENTION` (tùy chọn) | rỗng | dọn Login rác (P2.9 câu 4), ngoài source |

## P2.7 Danh sách ca test

**`auth/login.test.ts`** (`POST /auth/login`)
- Invalid client UUID (`:87`): `clientId='123'` thì 404.
- Invalid client ID (`:97`): UUID lạ thì 404, `issue[0].details.text='Not found'`.
- Missing email (`:109`): `email=''` thì 400 `Valid email address is required`.
- Invalid email (`:120`): `xyz` thì cùng lỗi.
- Missing password (`:131`): `password=''` thì 400 `Password must be at least 8 characters`.
- Wrong password (`:142`): 400 `Email or password is invalid`.
- Wrong projectId (`:153`): `clientId` hợp lệ + `projectId` khác project của client thì 400 `Invalid projectId` (lỗi xảy ra **trước** khi tra user).
- Success with custom client (`:165`): 200 có `code`.
- Invalid redirect URI type (`:176`): `redirectUri` là object thì 400 `Invalid redirectUri`.
- Success default client (`:185`): không `clientId` thì 200 có `code`.
- Success new project (`:195`): `projectId='new'` thì 200 có `login`, không có `code`.
- Login with access policy (`:207`): **BỎ** (cần `registerNew`, `invite`, `/fhir/R4/AccessPolicy`, `/oauth2/token` và `/fhir/R4`; phần login đã được các ca khác phủ; phần token thuộc P3).
- Require Google auth (`:363`): giữ, **seed** project có `features=['google-auth-required']`; kỳ vọng 400 `Google authentication is required`.
- Specify resourceType (`:407`): **BỎ** (`test.skip` trong source).
- Case insensitive email (`:468`): email hoa thường lẫn đều đăng nhập được, cả bản hạ chữ thường.
- No membership (`:509`): `projectId` của project khác thì 400 `User not found`, không có `login`, `code`, `memberships`.
- Inactive membership (`:525`): 400 `User not found`.
- Inactive project (`:560`): user có hai project, một cái `active=false`: đăng nhập không `projectId` thì 200 có `code` (chọn project còn lại); có `projectId` của project ngưng thì 400 `Project not active`.
- Success with no origin (`:585`): client có `allowedOrigin`, không gửi `Origin` thì 200.
- Success with matching origin (`:596`): 200.
- Failure with unknown origin (`:607`): 400 `Invalid origin`.

**`oauth/utils.test.ts`** (`tryLogin`, `:65-306`, mức ứng dụng)
- Login with invalid client ID (`:76`): `Not found`.
- Login with missing email (`:94`): `Invalid email`.
- Login with missing password (`:112`): `Invalid password`.
- Unknown email and wrong password are indistinguishable (`:130`): cả hai thông điệp `Email or password is invalid` và **đúng một lần** so bcrypt, hash đưa vào khớp `^\$2b\$\d{2}\$.{53}$` (Go: kiểm bằng cổng `PasswordHasher` đếm số lần gọi, không cần kiểm định dạng `$2b$`).
- Blank authentication method (`:153`), Invalid authentication method (`:171`), Invalid google credentials (`:188`), Invalid scope (`:205`): lỗi tương ứng.
- Login with externalId and missing projectId (`:223`), Login with externalId not found (`:239`): **HOÃN P6**.
- Invalid projectId (`:256`): `not-a-project-id` thì `Invalid projectId`.
- Login successfully (`:269`), Login with registered redirect URI (`:281`), Login with unregistered redirect URI (`:294`): lỗi `Invalid redirect URI`.
- External auth without email or externalId (`:308`), External auth without projectId (`:323`): giữ ở mức `ValidateLoginRequest` (rẻ).

**`auth/profile.test.ts`** (`POST /auth/profile`)
- Missing login (`:61`): 400 `Missing login`. Missing profile (`:70`): 400 `Missing profile` (kể cả `login='123'`).
- Login not found (`:79`): 404 `Not found`.
- Login revoked (`:92`): 400 `Login revoked`. Login granted (`:118`): 400 `Login granted`.
- Login profile already set (`:144`): 400 `Login profile already set`.
- Concurrent requests set the login profile only once (`:172`): một thắng có `code`, một thua 400 `Login profile already set`; membership đã lưu là của người thắng.
- Membership not found (`:203`): 400 `Profile not found`.
- Membership for different user (`:227`): 400 `Invalid profile`.
- Membership for different project (`:258`): login gắn project 1, chọn membership project 2 thì 400 `Invalid profile`; chọn membership project 1 thì 200 có `code`.
- Success (`:294`): chọn `memberships[0].id` thì 200 có `code`.
- Memberships with identifiers can be differentiated in the login response (`:317`): `identifier[0].value` của membership có mặt.
- Memberships in the login response do not sign attachments (`:344`): `identifier[0].extension[0].url` giữ `Binary/<id>`. Ở Go: **giữ** (chỉ cần không ký/viết lại URL), ca trở thành ca không có presigned.

**`auth/scope.test.ts`** (`POST /auth/scope`)
- Missing login (`:55`), Missing scope (`:64`), Login not found (`:73`, 404), Login revoked (`:83`), Login granted (`:109`).
- Missing membership (`:135`): 400 `Login profile not set`.
- Success (`:161`): 200 có `code`.
- Invalid scope (`:179`): thêm scope `patient/Condition.rs?category=...` vào login không có SMART thì 400 `Invalid scope`.
- Allow selection of restricted scopes (`:198`): `patient/Condition.crs` chọn `patient/Condition.read` (=`rs`) thì 200 (cần `launch` có Patient).
- Allow selection of more granular scope (`:217`): `rs` chọn `rs?category=health-concern` thì 200.
- Disallow dropping all restricted scopes (`:236`): bỏ hết SMART thì 400 `Invalid scope`.
- Disallow adding non-SMART scopes (`:255`): thêm `offline_access` thì 400 `Invalid scope`.
- Disallow selection of conflicting granular scope (`:273`): `?encounter=...` đổi `?category=...` thì 400 `Invalid scope`.
- MFA-pending login / Does not leak auth code when MFA is enrolled but not verified (`:320`): `mfaRequired:true`, không có `code` (ở cả `/auth/login` và `/auth/scope`); dùng fake `MFAGate` và User có `mfaEnrolled`, `mfaSecret`.
- Releases auth code once MFA is verified (`:340`): **HOÃN P5** (cần `/auth/mfa/verify`); thay bằng ca đặt `login.mfaVerified=true` thủ công rồi `/auth/scope` thì có `code`, không có `mfaRequired`.

**`auth/status.test.ts`** (`GET /auth/login/{login}`)
- Missing login parameter (`:36`): 404. Invalid login parameter (`:41`, `x`): 400. Login not found (`:46`): 404.
- Success (`:51`): 200, `body.login == id`. Granted (`:66`): 400. Revoked (`:81`): 400.

**`auth/method.test.ts`** (`POST /auth/method`)
- Missing email parameter (`:25`), Empty email parameter (`:30`), Invalid email parameter (`:35`): 400.
- Domain config (`:40`): có cấu hình thì 200 (kỳ vọng thêm của Go: body `{domain, authorizeUrl}`; test gốc chỉ kiểm 200); không có thì 200 `{}`.
- Domain config case sensitivity (`:71`): domain viết hoa vẫn tìm thấy (200).
- Domain config authorize url without protocol (`:95`): 400 (và domain lạ vẫn 200).

**`auth/revoke.test.ts`** (`POST /auth/revoke`)
- Unauthenticated (`:26`): 401.
- Revoke session (`:31`): thiếu `loginId` thì 400; `loginId` lạ thì 404; thu hồi login thứ hai của chính mình thì 200 và danh sách sessions của `/auth/me` còn một. Phần "sessions" dùng `getAuthTokens` để cấp `granted`: ở Go dùng `GrantLogin` của P1.
- Different user (`:106`): bước 1 (Alice không thu hồi được login của Bob, 404) và 5-7 (Bob thu hồi login của mình ở project khác, thấy 2 session) giữ ở mức ứng dụng. Các bước `preauthorize`, `X-Medplum-On-Behalf-Of`, `/oauth2/token` và bước 8 (token bị thu hồi thì 401): **BỎ/HOÃN** (P6 và P3).

**`auth/me.test.ts`** (`GET /auth/me`)
- Unauthenticated (`:28`): 401.
- User configuration (`:33`) và Set default menu (`:118`): giữ phần đọc `config` (mặc định `menu` theo `getUserConfigurationMenu`, `auth/me.ts:165-201`); bỏ các lời gọi `/fhir/R4/UserConfiguration` và `/admin/projects/.../members` (thay bằng seed); kiểm `user.identifier`, `security.sessions[0].browser`, `os`.
- Get me as ClientApplication without exposing its secrets (`:182`): **HOÃN P3** (cần xác thực Basic); riêng việc xóa `secret`, `retiringSecret` giữ ở mức đơn vị.
- Get me on behalf of a Patient (`:215`): **BỎ** (on-behalf-of).
- Profile in another project (`:258`): giữ, kỳ vọng 404 và không lộ `secret`.
- UserConfiguration from a linked project (`:278`): **BỎ** (project liên kết).
- AccessPolicy.basedOn (`:307`): chỉ giữ nếu quyết định có `accessPolicy` trong `me` (P2.9).
- Get user memberships (`:325`): membership theo `(user, project)`, bỏ `active=false`; đổi project khác không tính. Dùng `patchResource` (Go: helper patch).
- Logins in progress are not listed as sessions (`:406`): login chưa `granted` không thành session; sau `GrantLogin` thì có.
- SMART-scoped login does not get security details (`:446`): `profile` thu gọn còn `{resourceType, id}`, có `user`, **không** có `security`.
- Profile hidden fields are removed (`:468`) và Profile returned when access policy does not grant the profile type (`:487`): chỉ giữ nếu có `removeHiddenFields` (P2.9), nếu không thì **BỎ**.
- Project features are returned in /auth/me (`:500`): `project.features` có mặt.
- Security mfaRequired reflects project setting (`:532`): `security.mfaRequired` theo project setting và `User.mfaRequired=false` vẫn `true` khi project bắt buộc.
- Only signs attachments that the user can read (`:577`): **BỎ** (presigned URL).

**Race của `tryLogin`** (`auth/setpassword.test.ts`)
- Sign-in checked against a password replaced before its login was stored is rejected (`:317`): mật khẩu bị đổi giữa lúc so bcrypt thì 400 `Email or password is invalid`; mọi Login của user đều `revoked`; đăng nhập lại bằng mật khẩu mới thì 200. Go: dùng `PasswordHasher` giả chạy `SetPassword` trong lúc `Compare`.
- Sign-in is rejected when the password is replaced just before its login is stored (`:357`): đổi mật khẩu ngay trước `CreateResource(Login)` thì 400 và mọi Login `revoked`. Go: `LoginRepository` giả chèn `SetPassword` trước `Create`.
- Completing a sign-in keeps the revocation from a password change (`:404`): `setLoginScope` và `verifyMfaToken` trên login đọc trước không làm mất `revoked`. Go: giữ phần `setLoginScope` (P5 cho `verifyMfaToken`).

**`validateRecaptcha`** (`auth/newuser.test.ts`, `auth/resetpassword.test.ts`): ở P2 chỉ test middleware ở mức đơn vị với fake `Recaptcha`:
- Missing recaptcha (`newuser.test.ts:93`; `resetpassword.test.ts:71`): 400 `Recaptcha token is required` (khi có secret).
- Incorrect recaptcha (`newuser.test.ts:108`; `resetpassword.test.ts:80`): 400 `Recaptcha failed`.
- Recaptcha site key not found (`newuser.test.ts:375`): 400 `Invalid recaptchaSiteKey`.
- Recaptcha secret key not found (`newuser.test.ts:391`): 400 `Invalid recaptchaSecretKey` (theo `auth/utils.ts:668`; test chi tiết chưa đọc hết).
- Success when config has empty recaptchaSecretKey and missing recaptcha token (`newuser.test.ts:644`): qua.
- Custom recaptcha client success (`:224`), with incorrect project ID (`:275`), rejects registration when open registration is disabled (`:327`): phụ thuộc `projectRegistrationAllowed` (P4); **HOÃN**.

## P2.8 Không port đợt này và điểm lệch chấp nhận được

Không port (kèm lý do):
- `X-Medplum-On-Behalf-Of` và xác thực `Basic` cho `/auth/me` và `/auth/revoke`: chỉ dùng với client/bot (P3 và P6).
- `rewriteAttachments` presigned URL (`auth/me.ts:139`): `auth` không có kho Binary. Trả resource nguyên trạng (test `:577` BỎ).
- `readMembershipReference` với project liên kết (`auth/me.ts:44-50,150-155`; `Project.link`): đề xuất chưa hỗ trợ liên kết, chỉ chấp nhận profile cùng project.
- `removeHiddenFields` theo access policy: phụ thuộc engine access policy (xem P2.9 câu 2).
- `preauthorize`, `external`, `google`, `exchange`, `clientinfo`: P6. MFA đầy đủ: P5. Đăng ký, mật khẩu, email: P4 (ngoài `validateRecaptcha` và `SetPassword`).
- `Bowser` (phân tích User-Agent): có thể rút gọn (trả `browser`, `os` rỗng) nếu không muốn thêm phụ thuộc; ghi ở P2.9.

Lệch chấp nhận được (đề xuất):
1. Thứ tự tham số trong `authorizeUrl` (`/auth/method`) khác JS (Go sắp xếp theo khóa).
2. Header, thông điệp và mã giữ nguyên; riêng `extension` tracing trong `OperationOutcome` (`fhir/outcomes.ts:49-60`) **bỏ** (chưa xác nhận client Medplum có đọc trường này).
3. Cookie `Path` đặt tường minh `/` (Express mặc định `/`).
4. `Login` hết hạn: Medplum không xóa Login rác; Go cũng không xóa ở P2 (nhưng xem câu hỏi mở).

## P2.9 Rủi ro và câu hỏi mở

1. **Profile ở kho nào?** `/auth/me` (và `createProfile`, P4) đọc `Practitioner`/`Patient`... Nếu kho `auth` tách khỏi kho lâm sàng (D2), `me` phải gọi `fhir-server` qua HTTP hoặc `auth` phải nhân bản profile tối thiểu. Phương án: (A) `me` trả `profile` chỉ gồm `{resourceType, id}` ở đợt này (lệch hợp đồng), (B) gọi `fhir-server` bằng token dịch vụ, (C) chia sẻ kho. Cần quyết định trước bước 2.5.
2. **`accessPolicy` trong `/auth/me`**: trả về cần dựng policy đầy đủ (`fhir/accesspolicy.ts:174-263`, thay biến, project admin, SMART, Bot) và P3 cũng cần cho claim `fhir_access_policy`. Đề xuất port một lần ở P3 và để `me.accessPolicy` hoàn thiện sau; trong P2 chỉ trả khi resolver có.
3. **Nhánh MFA khi chưa có P5**: fail đóng như P2.4.3 có được chấp nhận làm hành vi tạm? Với user chưa dùng MFA, hai nhánh không kích hoạt.
4. **Login rác**: mỗi đăng nhập sai ở bước 12 hoặc không bao giờ đổi `code` đều để lại `Login` (chứa `code` hợp lệ nhưng chưa từng trả cho client). Cần chính sách dọn (TTL theo `AUTH_CODE_EXPIRATION_MS = 10 phút`, `constants.ts:49`, áp ở P3) và chỉ mục.
5. **`req.ip` và proxy**: nếu không cấu hình đúng, IP sai làm lệch quy tắc `ipAccessRule` (kiểm tra bảo mật). Cần quyết định `IPExtractor` của Echo.
6. **Lỗi lạ trả 400**: Medplum chuyển mọi lỗi không phải `OperationOutcomeError` thành `badRequest(message)` (`core/outcomes.ts:537-545`), tức có thể lộ thông điệp lỗi nội bộ và trả 400 thay vì 500. Go đề xuất trả 500 `exception` cho lỗi nội bộ (lệch có chủ đích, cần người duyệt đồng ý). Ngoài ra middleware lỗi của Express chưa đọc (chưa xác nhận).
7. **Giới hạn `count=100` membership** (`oauth/utils.ts:456`): cắt im lặng; giữ hay nâng?
8. **Tương đương `isEmail`** giữa `validator.js` và Go (tên miền không có TLD, ký tự Unicode, độ dài): cần bảng ca khác biệt; test gốc chỉ dùng `xyz` và chuỗi rỗng.
9. **Brute-force**: `/auth/login` không có giới hạn tần suất trong phạm vi file đã đọc (rate limit nằm ngoài, `apps-auth.md` mục 1 hàng "Rate limit"). P2 không được mở ra Internet trước khi có rate limit.
10. **Thời gian bcrypt giả**: hash giả cùng cost; test gốc kiểm đúng 1 lần `compare` (`oauth/utils.test.ts:130-151`). Go không kiểm đo thời gian được ổn định trong test, chỉ kiểm số lần gọi.
11. **`req.get('Origin')`**: Medplum chỉ kiểm khi cả `client.allowedOrigin` và `Origin` cùng có. Request không gửi `Origin` (curl, server-to-server) luôn qua; có chấp nhận?

## P2.10 Ước lượng và thứ tự

Cỡ: **L** (khoảng 1 tuần) nếu `me` làm đầy đủ; **M đến L** nếu `me` rút gọn (phương án A ở câu 1).

| Bước | Nội dung | Tiêu chí hoàn thành |
|---|---|---|
| 2.1 | Khung HTTP: Echo route nhóm `/auth`, DTO, validator ra đúng thông điệp và `expression`, `writeOutcome` (`application/fhir+json`, `getStatus` theo loại lỗi, 404 cho id không hợp lệ), swagger | Test bảng validator (`login`, `profile`, `scope`, `status`) xanh; mọi ca "Missing/Invalid" mục P2.7 xanh |
| 2.2 | `ValidateLoginRequest`, `ValidatePKCE`, `TryLogin` (không có membership logic thêm ngoài P1), hash giả | Ca `oauth/utils.test.ts` mục tryLogin xanh; ba ca race (`setpassword.test.ts:317,357`; `:404` phần scope) xanh |
| 2.3 | `BuildLoginResult` dạng 3, 4, 5 + cookie; route `POST /auth/login`, `/auth/profile`, `/auth/scope`, `GET /auth/login/{id}` | Ca `login`, `profile`, `scope`, `status` trong mục P2.7 (trừ MFA, Google) xanh; JSON response khớp `auth-dto.md` đã sửa các điểm ở mục 0.6 |
| 2.4 | Nhánh MFA 1 và 2 qua `MFAGate` (fail đóng), test bằng fake | Ca "Does not leak auth code..." xanh; có test khẳng định adapter mặc định không bao giờ trả `code` cho user `mfaEnrolled` |
| 2.5 | `POST /auth/method` | Bốn ca `method` xanh, gồm URL không có giao thức thì 400 |
| 2.6 | `Authenticator` + `POST /auth/revoke` + `GET /auth/me` (theo quyết định P2.9 câu 1 và 2); `sessions` | Các ca revoke và me đã chọn xanh với `Authenticator` giả; ghi rõ ca nào chờ P3 |
| 2.7 | `validateRecaptcha` middleware + fake `Recaptcha` (chưa gắn route) | Các ca recaptcha mức đơn vị xanh; không có lời gọi mạng |
| 2.8 | E2E (kiểu `apps/fhir-server/internal/bootstrap/e2e_test.go`): seed project, user, membership, client, profile; `login` thì có `code`; nhiều membership thì `profile` thì có `code`; thu hồi; cập nhật `docs/api-contract/auth-dto.md` | E2E xanh trên SQLite; `auth-dto.md` sửa 4 điểm ở mục 0.6; swagger khớp |

Thứ tự phụ thuộc trong P2: 2.1 trước hết; 2.2 cần P1.3 và P1.4; 2.3 cần 2.2; 2.4 cần 2.3; 2.5 độc lập; 2.6 cần P3 (hoặc fake) và quyết định P2.9; 2.7 độc lập; 2.8 cuối.
