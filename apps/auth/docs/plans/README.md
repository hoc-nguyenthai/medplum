# Kế hoạch `apps/auth`: tổng hợp

Trạng thái (cập nhật 2026-11-23): **P0 đến P7 đã làm** (kể cả đăng ký và mật khẩu, MFA, OAuth2/OIDC, đăng nhập ngoài, `/admin/projects/*`, SCIM, email); **P8 (compartment Patient) đã duyệt 2026-11-23, M0 xong, M1 đến M5 chưa làm**; tiến độ ở cuối [p8-compartment.md](p8-compartment.md). Trạng thái chi tiết từng tính năng: [../../README.md](../../README.md). Bảng pha và ước lượng bên dưới là kế hoạch ban đầu, giữ để tra cứu quyết định và lý do. Các đề xuất ở mục 5 đã được duyệt toàn bộ ngày 2026-11-21. Đây là bản tổng hợp của kế hoạch tổng [apps-auth.md](apps-auth.md) và các kế hoạch chi tiết cùng thư mục. Đọc file này trước, vào file chi tiết khi cần số dòng source, thuật toán hoặc danh sách ca test.

| File | Nội dung | Cỡ (ước lượng của người viết) |
|---|---|---|
| [p0-scaffold.md](p0-scaffold.md) | Dựng khung app, test kiến trúc, swagger, Docker | S |
| [p1-p2-login-core.md](p1-p2-login-core.md) | P1 lõi dữ liệu và an toàn; P2 đăng nhập lõi (`login`, `profile`, `scope`, `me`, `revoke`...) | P1: M; P2: L |
| [p3-oauth2-tokens.md](p3-oauth2-tokens.md) | `/oauth2/*`, khóa và JWKS, token tương thích `fhir-server` | L (10 bước, B0 đến B9) |
| [p4-p5-register-mfa.md](p4-p5-register-mfa.md) | P4 đăng ký, mật khẩu, email; P5 MFA | P4: M (4 đến 5 ngày); P5: M (3 đến 4 ngày) |
| [p6-p7-external-admin.md](p6-p7-external-admin.md) | P6 đăng nhập ngoài; P7 `/admin/*` và CLI vận hành | P6: M đến L; P7: L |

Cách đọc độ tin cậy: các file chi tiết do bốn agent viết từ source Medplum, kèm `file:dòng`. Tôi **chưa đọc lại toàn bộ** chúng và chưa kiểm lại các số dòng. Tôi đã tự đối chiếu với source năm điểm nặng nhất (đánh dấu "đã kiểm" ở mục 3 và 5); phần còn lại là lời của agent.

## 1. Lộ trình đã chỉnh

| Thứ | Pha | Việc | Phụ thuộc | Cỡ |
|---|---|---|---|---|
| 1 | P0 | Khung app | | S |
| 2 | P1 | Kho dữ liệu, `Store.Mutate`, mật khẩu, security request | P0 | M (M đến L nếu kéo `SetPassword` vào) |
| 3 | **7.0** | CLI `auth seed project`, `client create`, `user set-password`, `domain add`, `purge-logins` | P1 | M |
| 4 | P2 | Đăng nhập lõi; cổng `Authenticator` (fake) và `MfaPolicy` (fail đóng) | P1 | L (M đến L nếu `me` rút gọn) |
| 5 | P3 | Khóa, JWKS, token, `authorize`, `userinfo`, `introspect`, tích hợp `fhir-server` | P1, P2 | L (khoảng 8 đến 12 ngày) |
| 6 | P4 | Đăng ký, đặt lại mật khẩu, xác minh email, đổi mật khẩu | P1, P2; P3 cho ca e2e | M |
| 7 | P5 | MFA (TOTP, email), giới hạn thử | P2, P4 (bước 4.2) | M |
| 8 | P6 | Đăng nhập ngoài, `google`, `exchange`, `preauthorize` | P2, P3, P4 | M đến L |
| 9 | P7 HTTP | `/admin/projects/*` | P3, P4, P5 | L, **hoãn** |

Thay đổi so với kế hoạch tổng ban đầu: thêm bước **7.0 (CLI vận hành)** ngay sau P1, vì không có nó thì P2, P3 không có dữ liệu thử (project, user, client). P7 HTTP hoãn; chỉ 7.0 là đủ vận hành tối thiểu.

Tổng thô (cộng ước lượng của các agent, một người, chưa kể P7 HTTP): khoảng **31 đến 44 ngày làm việc** (6 đến 9 tuần). Đây là cộng cơ học các khoảng trong file chi tiết, không phải cam kết.

Mốc dùng được:
- **Hết P3 và tích hợp `fhir-server`**: đăng nhập bằng mật khẩu và gọi được `fhir-server` bằng token do `auth` cấp, với tài khoản và client tạo bằng CLI 7.0.
- **Hết P5**: có đăng ký tự phục vụ và MFA, đủ để mở cho người dùng thật, **với điều kiện đã có rate limit** (mục 4).

## 2. Việc nền phải có trước (chặn nhiều pha)

| Việc | Pha làm | Ai cần | Ghi chú |
|---|---|---|---|
| `Store.Mutate`: đọc, sửa, `UpdateResource` với `IfMatch`, thử lại khi 412 | P1 | P2, P4, P5, P3 | `fhirrouter.Repository` **không có** `PatchResource` (`pkg/fhirrouter/router.go:79-92`). Test Medplum buộc phải ghi theo từng trường, không ghi đè bản cũ |
| `KeyLocker.LockKey` cho đăng ký đồng thời | P1 (có sẵn trong store) | P4 | Không có ràng buộc duy nhất cho `User.email` |
| Luật `architecture_test.go`: `domain` được import `pkg/fhir`, các `pkg/fhir*` khác chỉ ở infrastructure | P0 | mọi pha | Cần chốt (câu hỏi D-5) |
| Cổng `Authenticator` (P2 viết với fake, P3 nối thật) | P2 | `me`, `revoke`, `changepassword`, `/auth/mfa/*` | |
| Cổng `MfaPolicy` (P2 khai báo, P5 cài) | P2 | `sendLoginResult` | Khi chưa có P5: **fail đóng**, không bao giờ trả `code` cho user đã đăng ký MFA |
| Một phần `fhir/accesspolicy.ts` (chỉ `ipAccessRule` và hai lỗi SMART) | P1 | `setLoginMembership` | Phần đầy đủ cho claim `fhir_access_policy` làm ở P3 |
| Redis (bộ đếm thử MFA, offer TOTP, chống phát lại mã) | P5 | P5 | **Kế hoạch trước nói chưa dùng Redis; sai.** P5 cần Redis (go-redis, miniredis đã có trong `go.mod`; chưa xác nhận miniredis chạy được `SET NX GET` và Lua) |
| Rate limit theo IP | trước khi mở ra Internet | mọi route `/auth` | Medplum đặt ở `ratelimit.ts:41-65` (login, newuser, newproject: 5 lần/phút; mfa/verify, login-enroll: 10; còn lại 160), nằm ngoài `auth/*.ts` |
| Cổng `Mailer`, `Pwned`, `Recaptcha`, `Clock` | P2 đến P4 | P4, P5 | Không gọi mạng trong test |

## 3. Phát hiện bảo mật làm đổi thiết kế (xử lý bằng test bắt buộc)

| # | Phát hiện | Nguồn | Xử lý đề xuất |
|---|---|---|---|
| S1 | **Leo quyền qua scope** (đã kiểm): Medplum lưu nguyên `scope` do client gửi (`oauth/token.ts:183`, grant `client_credentials`), còn `pkg/fhirauth` coi scope `fhir.admin` là quyền admin (`jwt.go:174-190`). Port y nguyên thì một client gửi `scope=fhir.admin` nhận token admin | P3 §0 | Cấm tiền tố `fhir.*` trong scope client khai; chỉ `auth` tự thêm scope vai trò; có test |
| S2 | Access token Medplum không có `fhir_project`, `fhir_access_policy`, scope chỉ `openid`, nên `fhirauth` trả 403 | P3 §4.3 | `auth` thêm claim; AccessPolicy dùng tính năng `fhirauth` chưa hiểu thì **từ chối cấp token** |
| S3 | Thu hồi trễ tới hết TTL access token (mặc định 1 giờ), vì `fhir-server` chỉ kiểm JWKS | P3 §4.4 | Chấp nhận có điều kiện (TTL production 15 phút); `userinfo`, `introspect`, `refresh` vẫn kiểm trạng thái. Nhiều test Medplum phải đổi kỳ vọng |
| S4 | PKCE và xác thực client yếu hơn chuẩn: client `pkceOptional` không bị kiểm verifier; refresh không xác thực client nếu thiếu Basic | P3 §3, §8 | Đảo các ca này; chờ chốt Q4 |
| S5 | Code `authorization_code` được chiếm không nguyên tử, hạn code tính từ `meta.lastUpdated` | P3 §3.1 | CAS bằng `IfMatch`; mốc hết hạn tường minh; lưu `redirectUri` |
| S6 | Ép MFA chỉ dựa vào việc `sendLoginResult` không trả `code`; `oauth/token.ts` không kiểm `mfaVerified` | P4/P5 | Thêm kiểm tra phòng thủ ở P3 (Q8 của P5) |
| S7 | `newproject` và `newpatient` tạo project hoặc hồ sơ trước khi kiểm `revoked`, `granted`, MFA | P4/P5 | Kiểm trước khi tạo |
| S8 | `newuser` tạo user rồi mới `tryLogin`; PKCE sai để lại user mồ côi | P4/P5 | Kiểm trước hoặc rollback; khóa `LockKey` chống đua |
| S9 | `/auth/mfa/enroll` và `disable` không có hạn mức theo login hay user; mã email 6 chữ số sống 20 phút | P4/P5 | Áp `AttemptLimiter` cho cả hai |
| S10 | `secret` của `UserSecurityRequest`, `User.mfaSecret`, `Login.code` lưu dạng thô | P4/P5, P3 | Băm `secret` và `code`; mã hóa `mfaSecret` (AES-GCM, khóa từ biến môi trường) |
| S11 | Đăng nhập ngoài tin IdP quá mức: `state` do client dựng và không ký; id_token và userinfo JWT có thể không kiểm chữ ký nếu IdP thiếu `jwksUrl`; `email_verified=false` vẫn đặt `emailVerified=true`; `/auth/google` không kiểm `email_verified` | P6 §0 | Lệch an toàn hơn, chờ quyết định |
| S12 | SSRF: Medplum chỉ chặn lúc kết nối, không kiểm URL khi lưu cấu hình IdP | P6 §A5.2 | Dialer kiểm IP sau resolve, chỉ HTTPS, kiểm lại mỗi lần redirect, có timeout và giới hạn body; kiểm cả khi CLI ghi cấu hình |
| S13 | Lộ dữ liệu nhạy cảm qua `/fhir/R4` của `fhir-server` nếu dùng chung kho (D2) | P1 | Kho riêng (database riêng hoặc `search_path`; bảng `fhir_resource` không có tiền tố schema); test chặn; cân nhắc từ chối `User`, `Login`, `UserSecurityRequest`, `ClientApplication` ở `fhir-server` làm lớp thứ hai |
| S14 | Lỗi nội bộ: Medplum trả 400 kèm thông điệp lỗi (lộ chi tiết) | P1/P2 | Trả 500 `exception` |

## 4. Sai sót trong tài liệu hợp đồng hiện có

| Tài liệu | Sai gì | Trạng thái |
|---|---|---|
| `auth-dto.md` | `/auth/mfa/verify` và `login-enroll` lỗi là `"Invalid MFA token"` (đã kiểm: `oauth/utils.ts:392`), `"Invalid token"` chỉ ở `enroll` và `disable` (`mfa.ts:191,221,353`) | **Đã sửa** |
| `auth-dto.md` | Nhánh MFA số 1 của `LoginResult` dùng `isMfaRequired(user, project)` (project **hoặc** `User.mfaRequired`) (đã kiểm: `auth/utils.ts:479`) | **Đã sửa** |
| `auth-dto.md` | `/auth/exchange` trả body token, không phải `LoginResult`. Đã kiểm một phần: endpoint deprecated, gọi `exchangeExternalAuthToken(req, res, ...)`; dạng body theo agent (`token.ts:553-646`) | **Đã sửa**, ghi rõ phần chưa tự kiểm |
| `auth-dto.md` | Còn thiếu: tên trường `recaptchaToken`, lỗi reCAPTCHA và `Project not active`, hạn mức IP, `display` trong `memberships.project` | Chưa sửa, làm khi vào P4 |
| `oauth.md` | Ghi `/oauth2/token` nhận JSON; theo agent source trả 400 văn bản thuần | **Chưa kiểm, chưa sửa** (file do generator sinh) |
| `admin.md` | Ghi "không guard ở route"; thực tế guard ở router (`authenticateRequest`, `verifyProjectAdmin`, `requireSuperAdmin()`) | **Chưa kiểm, chưa sửa** (file do generator sinh) |

## 5. Sổ quyết định

Cột "Chặn" ghi bước bị chặn nếu chưa chốt. Cột "Đề xuất" là khuyến nghị của người viết kế hoạch, **chưa phải quyết định**.

### 5.1 Chốt trước khi viết code P0 và P1

| # | Quyết định | Đề xuất | Chặn |
|---|---|---|---|
| D-1 | Engine OAuth2: tự port flow Medplum (mượn kỹ thuật bảo mật của echo-oauth2-server) hay bọc thư viện | Tự port | P3 |
| D-2 | Kho dữ liệu: `pkg/fhirstore`, **database riêng** (đổi 2026-11-22: **schema riêng + role riêng** trong database dùng chung, xem [microservices-shared-db.md](../../../../docs/plans/microservices-shared-db.md), D-DB-1 và D-DB-2) | Có | P1 |
| D-3 | Làm `/admin/*` HTTP: hoãn, chỉ CLI 7.0 | Hoãn | P7 |
| D-4 | Email, `Mailer` adapter đầu tiên | `log` và `fake`; nhà cung cấp thật chốt sau | P4 |
| D-5 | Cho `internal/domain` import `pkg/fhir`; luật mới trong `architecture_test.go` | Cho phép | P0 |
| D-6 | `SetPassword` làm ở P1 hay P4 | P1 (cỡ P1 thành M đến L) | P1 |
| D-7 | Lỗi nội bộ trả 500 thay vì 400 như Medplum | 500 | P1 |
| D-8 | HIBP lỗi mạng: fail đóng hay mở | Đóng ở production; `PWNED_ENABLED=false` chỉ ở `local` và `test` | P1 |
| D-9 | Swagger chỉ bật ở dev và test; cổng `:8006`; cách ly kho bằng database riêng | Có | P0 |

### 5.2 Chốt trước P2 và P3

| # | Quyết định | Đề xuất | Chặn |
|---|---|---|---|
| D-10 | Nguồn `profile` cho `/auth/me` khi kho `auth` tách khỏi kho lâm sàng: (A) trả `{resourceType, id}`, (B) gọi HTTP `fhir-server`, (C) đọc chung DB chỉ đọc | A trước, B hoặc C sau | P2 bước 2.6 |
| D-11 | Nhánh MFA fail đóng tạm thời khi chưa có P5 | Chấp nhận | P2 |
| D-12 | Ai xây giao diện đăng nhập, chọn profile, consent mà `/oauth2/authorize` chuyển hướng tới | Giữ cách chia của Medplum; P3 chỉ cần URL cấu hình và trang giả cho e2e | P3 B6 |
| | **Đã duyệt D-12 đến D-17 theo đề xuất, 2026-11-21 ("chốt D-12 đến D-17, bắt đầu P3").** Kết quả khảo sát D-16 ở dưới bảng | | |
| D-13 | Chấp nhận cửa sổ thu hồi theo TTL access token ở `fhir-server` | Chấp nhận, TTL 15 phút | P3 |
| D-14 | Bắt buộc xác thực client khi đổi code hoặc refresh với client có `secret` (đảo ca `pkceOptional`) | Bắt buộc khi request có gửi credential và với client bí mật | P3 B3 |
| D-15 | Lưu `Login.code` và cookie dạng băm; mốc hết hạn code dùng `Login.expiresAt` | Có | P3 B3 |
| D-16 | Khảo sát AccessPolicy thực tế (compartment, hiddenFields, tham số) để biết `fhirauth` còn thiếu gì | Làm trước B2 | P3 B2 |
| D-17 | Client có nhiều `ProjectMembership`: ưu tiên membership của `client.meta.project`, mơ hồ thì lỗi | Có | P3 |

**Kết quả khảo sát D-16 (AccessPolicy mặc định do chính auth tạo).** `fhirauth` chưa hiểu `*`, `hiddenFields`, `interaction`, `compartment`, tham số `%...`, mà chính sách mặc định của Admin, Practitioner, Bot, ClientApplication dùng `*` (và hiddenFields cho secret của ClientApplication), còn Patient, RelatedPerson dùng compartment. Đề xuất ban đầu (từ chối cấp token) làm client mặc định do `auth seed project` tạo KHÔNG BAO GIỜ lấy được token. Cách xử lý đã cài: (1) `pkg/fhirauth.Policy` hỗ trợ rule `*` (mọi loại không có rule riêng, trừ loại quản trị của Medplum) và `"deny": true`; (2) `domain.PolicyClaim` dịch chính sách sang claim theo nguyên tắc **chỉ thu hẹp, không bao giờ nới**: loại có hiddenFields, interaction, compartment hay `%` bị `deny` (kể cả khỏi `*`), readonlyFields và writeConstraint thành chỉ đọc, nhiều criteria khác nhau thì giữ phần toàn loại hoặc cấm; compartment hoặc basedOn ở mức chính sách thì từ chối cấp token; (3) mọi chỗ thu hẹp được ghi log. Hệ quả: token của Patient hoặc RelatedPerson hiện không đọc được dữ liệu lâm sàng (cần compartment ở `fhirauth`, việc riêng); client và Practitioner mặc định dùng được và không đọc được secret của ClientApplication. Rủi ro còn lại: claim của chính sách mặc định khoảng 5 đến 6 KB (token khoảng 8 KB), có thể vượt giới hạn header 8 KB của một số ingress; trần mặc định `AUTH_MAX_POLICY_CLAIM_BYTES` là 16384.

### 5.3 Chốt trước P4 đến P7

| # | Quyết định | Đề xuất | Chặn |
|---|---|---|---|
| D-18 | Thương hiệu trong thư (giữ "Medplum" hay đổi); băm `UserSecurityRequest.secret` | Tham số `AppName`, mặc định `Trivita`; băm | P4 |
| D-19 | Mã email hết hạn đang chặn cả TOTP hợp lệ: sửa hay giữ; mã hóa `mfaSecret`; `mfaVerified` khi đổi `code` | Sửa; mã hóa AES-GCM; thêm kiểm tra | P5 |
| D-20 | Redis dùng chung máy chủ với `fhir-server` (DB logic riêng) | Chung máy chủ | P5 |
| D-21 | Đăng nhập ngoài: phát `state` đã ký; từ chối `email_verified=false`; cấm IdP không có `jwksUrl` ngoài dev; bắt buộc `membership_id` khi trao đổi token | An toàn hơn Medplum ở cả bốn | P6 |
| D-22 | Giữ khái niệm super admin (`Project.superAdmin`)? Mặc định không phát `fhir.super` | Không giữ ở đợt này | P7, P3 |

## 6. Việc kế tiếp

1. Bạn xem mục 5 và cho biết quyết định nào đổi; các mục "Đề xuất" không có phản đối sẽ coi là đồng ý.
2. Chốt xong 5.1 thì làm **P0 + P1** (một đến hai PR) rồi **7.0**.
3. Trước P2: chốt D-10, D-11. Trước P3: D-12 đến D-17.
4. Sau khi có code, cập nhật `auth-dto.md`, `fhir-server-go.md`, swagger theo từng pha; sửa `oauth.md` và `admin.md` sau khi tự kiểm hai điểm ở mục 4.

## 7. Điều chỉnh khi cài đặt (khác kế hoạch)

| Việc | Thực tế |
|---|---|
| Tra user theo email | Kế hoạch P1 ghi mơ hồ. Medplum **rơi về user toàn cục** khi không có user trong project (`getUserByEmail`); bản đầu của tôi sai (loại trừ), bị test HTTP bắt được và đã sửa. Có thêm `FindUserInScope` (loại trừ) cho kiểm tra trùng email và lệnh quản trị. |
| `reCAPTCHA` | **Hoãn sang P4** (chỉ gắn vào route ở P4); kế hoạch P2 có nêu port sớm nhưng chưa có route nào dùng. |
| `/auth/me` | `profile` rút gọn (D-10), chưa `accessPolicy`; `Authenticator` chưa có nên trả 401 (P3). |
| `domain add` (CLI 7.0) | Hoãn sang P6 (cần chống SSRF khi lưu URL IdP). |
| IP người gọi | Thêm `AUTH_TRUSTED_PROXIES`; mặc định không tin `X-Forwarded-For` (có test chống giả mạo). |
| Echo | `Group("", middleware)` làm đường dẫn lạ dưới `/auth` thành 401; middleware gắn theo từng route. |
