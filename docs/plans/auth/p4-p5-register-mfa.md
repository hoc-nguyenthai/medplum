# P4 và P5 — Đăng ký, mật khẩu, email và MFA cho `apps/auth`

Thuộc kế hoạch tổng [../apps-auth.md](../apps-auth.md) (quyết định D1 đến D6; D4: cổng `Mailer`). Hợp đồng DTO: [../../api-contract/auth-dto.md](../../api-contract/auth-dto.md). Khung thư mục theo [p0-scaffold.md](p0-scaffold.md).

Trạng thái: **P4 và P5 đã duyệt (2026-11-22) và XONG** (P4: 4.1 đến 4.7; P5: 5.1 đến 5.6, xem mục "Tiến độ P5" cuối tài liệu). Chỉ ghi điều đã đọc trong source Medplum (bản `v5.2.18`, commit `98c2d22d`, xem [../../UPSTREAM.md](../../UPSTREAM.md)). Chỗ chưa đọc được ghi **"chưa xác nhận"**.

Quy ước trích dẫn: `file:dòng` tính từ `/Users/hocnt84/Downloads/medplum/packages/server/src/` (ví dụ `auth/newuser.ts:41`). Test cũng tính từ thư mục đó (`auth/newuser.test.ts:57`). Mã nguồn `@medplum/core` ghi đầy đủ `packages/core/src/...`.

---

## 0. Phần chung cho P4 và P5

### 0.1 Phụ thuộc phải có trước (chặn)

| Cần | Thuộc pha | Dùng ở | Ghi chú |
|---|---|---|---|
| Repository cho `User`, `Login`, `UserSecurityRequest`, `Project`, `ProjectMembership`, `ClientApplication`, `AccessPolicy`, `DomainConfiguration`, hồ sơ (`Practitioner`, `Patient`) trên `pkg/fhirstore` | P1 | cả hai | `Store` hiện có `CreateResource`, `UpdateResource` (kèm `IfMatch`), `Search`, `RunInTransaction`, `LockKey` (`pkg/fhirstore/store.go:96,118`, `crud.go:71,142`). **Chưa có `PatchResource`** |
| "Cập nhật theo trường trên bản mới nhất" (thay `patchResource` của Medplum) | P1 | P4 (đổi mật khẩu, verify email, revoke), P5 (mọi ghi vào `Login`/`User`) | Cài bằng đọc rồi `UpdateResource` với `IfMatch=versionId`, thử lại khi 412 (tối đa N lần), đóng gói thành `application.Store.Mutate(ctx, type, id, fn)`. Lý do cần: `auth/mfa.test.ts:860-945` và `auth/setpassword.test.ts:404-441` kiểm tra rằng ghi đè bản cũ làm mất `emailMfa` hoặc làm sống lại login đã revoke |
| `tryLogin`, `validateLoginRequest`, `validatePkce`, `setLoginMembership`, `getMembershipsForLogin`, `sendLoginResult` | P2 | P4 (`newuser`, `newproject`, `newpatient`), P5 (`sendLoginResult` có 2 nhánh MFA) | P2 phải khai báo cổng `MfaPolicy` (xem 5.x) để P5 cắm vào, tránh P2 phụ thuộc ngược vào P5 |
| Ngữ cảnh đã xác thực (Bearer): `Login`, `Project`, `Membership`, `onBehalfOfMembership`, `systemRepo` | P3 | `changepassword`, `/auth/mfa/{status,enroll,send-email-challenge,disable}` | `auth/utils.ts:69-75` đọc `login`, `membership`, `onBehalfOfMembership` |
| Kiểm tra `Login.revoked` ở **mọi** lần dùng access token | P3 | P4 (`setpassword` thu hồi login), P5 (login bị thu hồi sau 5 lần sai) | `auth/setpassword.test.ts:59-139` kỳ vọng token cũ trả 401 sau khi đổi mật khẩu |
| Phân tích scope SMART (`parseSmartScopes`) | P2/P3 | `readUserForAccountManagement` | `fhir/smart.ts:41,142-171`; regex `^(patient|user|system)\/(\w+|\*)\.(read|write|c?r?u?d?s?|\*)$`; ca test `auth/utils.test.ts:202-213` |
| Redis (cache, bộ đếm) | hạ tầng | P5 | go-redis đã có trong `go.mod`; `miniredis` đã có cho test. Chưa xác nhận miniredis hỗ trợ `SET ... NX GET` và `EVAL` đủ cho script ở mục P5.9 |

### 0.2 Mã lỗi và dạng trả về (đã đọc trong `packages/core/src/outcomes.ts`)

| Tên Medplum | HTTP | Ghi chú |
|---|---|---|
| `allOk` (`id: "ok"`) | 200 | `outcomes.ts:27`, `getStatus` dòng 471-509 |
| `badRequest(msg, expression?)` (không có `id`) | 400 | `outcomes.ts:279-293`; `issue[0].details.text = msg`, `expression: [field]` nếu có |
| `notFound` (`id: "not-found"`) | 404 | `readResource` thiếu resource: `setpassword.test.ts:829`, `newuser.test.ts` ca `res6` |
| `forbidden` | 403 | `readUserForAccountManagement` |
| `tooManyRequests` (`id: "too-many-requests"`) | 429 | kèm extension `https://medplum.com/fhir/StructureDefinition/rate-limit-reset`, `valueUnsignedInt` = `ceil(ms/1000)` (`outcomes.ts:22,209-236`) |
| Lỗi validator | 400 | `fhir/outcomes.ts:10-29`: `id` ngẫu nhiên, `code: invalid`, `expression: [tên trường]`, `details.text = thông báo` |
| `redirectOk(url)` | 200 | `id: "ok"` cộng `Location` header (`outcomes.ts:384-386`, `fhir/outcomes.ts:43-47`) |
| Lỗi không biết | 500 `{"msg":"Internal Server Error"}` | `app.ts:132-190` |
| Lỗi thường (không phải `OperationOutcomeError`) bắt trong `try/catch` của handler | 400 | `normalizeOperationOutcome` bọc thành `badRequest(message)` (`outcomes.ts:537-545`); chỉ xảy ra ở `newuser` (xem P4.7) |

Go: lỗi miền (`domain.Error{Kind, Message, Expression}` với `Kind` thuộc `BadRequest`, `NotFound`, `Forbidden`, `TooManyRequests`) và một hàm ánh xạ duy nhất ở `presentation/httpapi/errors.go` (đã có trong P0, bước 0.3).

### 0.3 Hạn mức theo IP (đặt bởi `ratelimit.ts`, không nằm trong `auth/*.ts`)

Đã đọc `ratelimit.ts:34-66,126-132` và `config/utils.ts:58-60`. Cửa sổ 60 giây, khóa `IP:loại`, lưu Redis, trả 429.

| Loại | Đường dẫn | Mặc định/phút/IP |
|---|---|---|
| `login` | `/auth/login`, `/auth/newuser`, `/auth/newproject` | 5 (`ratelimit.ts:41-47`; `config/utils.ts` không đặt `defaultLoginRateLimit`) |
| `mfa` | `/auth/mfa/verify`, `/auth/mfa/login-enroll` | 10 (`ratelimit.ts:48-53`, `config/utils.ts:60`) |
| `auth` | mọi `/auth/*` và `/oauth2/*` còn lại, trừ `/auth/me` | 160 (`config/utils.ts:59`; mặc định trong `ratelimit.ts:59` là 600 nhưng cấu hình ghi đè) |

Hệ quả cần ghi vào kế hoạch: `/auth/resetpassword`, `/auth/setpassword`, `/auth/verifyemail`, `/auth/newpatient`, `/auth/mfa/send-email`, `/auth/mfa/enroll`, `/auth/mfa/disable` chỉ bị giới hạn chung 160 lần/phút/IP. Không có hạn mức theo email hoặc theo tài khoản ngoài `mfalimit` (chỉ áp cho `verify` và `login-enroll`). Việc cài middleware hạn mức theo IP là việc riêng (kế hoạch tổng mục 1) nhưng **bảng đường dẫn và hạn mức trên là một phần hợp đồng bảo mật** và phải được khai báo ở `presentation/httpapi` (xem P4.5, P5.5).

### 0.4 Quy ước xác thực đầu vào cần giữ

- `express-validator` của Medplum chạy **mọi** rule của mọi trường rồi mới trả lỗi; `issue[0]` là lỗi đầu tiên theo thứ tự khai báo (`util/validator.ts:8-20`). Ca test chỉ kiểm tra `issue[0]`. Đề xuất Go: gom lỗi theo thứ tự trường, mỗi trường một lỗi đầu tiên (lệch nhỏ chấp nhận được: Medplum có thể trả hai lỗi trùng thông báo cho cùng một trường, ví dụ `email` ở `auth/newuser.ts:24-28`).
- `isUUID()` của express-validator (không tham số) là kiểm tra dạng 8-4-4-4-12 chữ số hex, không ràng buộc phiên bản (chưa xác nhận từ thư viện; node_modules không có trong máy). Go: dùng regexp tương đương, không dùng `uuid.Parse` (nhận cả dạng `urn:`, ngoặc, không gạch).
- `isEmail()` của `validator.js` (mặc định, chưa xác nhận chi tiết). Test cho thấy `"x,other@example.com,y"@example.com` qua được validator (`auth/resetpassword.test.ts:103-110`). Go: viết bộ kiểm tra riêng (không dùng `net/mail.ParseAddress` thuần vì nó chấp nhận dạng có tên hiển thị), kèm test bảng và property test so với tập mẫu.
- Độ dài: `isLength` đếm ký tự (code point), `isByteLength` đếm byte. Mật khẩu: tối thiểu 8 **ký tự**, tối đa 72 **byte** (`constants.ts:71,81`; `auth/newuser.test.ts:154-185`).

---

# P4 — Đăng ký, mật khẩu, email

## P4.1 Mục tiêu và phạm vi

Port các route công khai và route Bearer sau (đúng như `auth/routes.ts:33-42`):

| Route | Middleware theo thứ tự | Handler |
|---|---|---|
| `POST /auth/newuser` | hạn mức `login`, `newUserValidator`, `validateRecaptcha(projectRegistrationAllowed)` | `newUserHandler` (`auth/newuser.ts:41`) |
| `POST /auth/newproject` | hạn mức `login`, `newProjectValidator` | `newProjectHandler` (`auth/newproject.ts:34`) |
| `POST /auth/newpatient` | `newPatientValidator` | `newPatientHandler` (`auth/newpatient.ts:25`) |
| `POST /auth/changepassword` | Bearer, `changePasswordValidator` | `changePasswordHandler` (`auth/changepassword.ts:26`) |
| `POST /auth/resetpassword` | `resetPasswordValidator`, `validateRecaptcha()` (không kiểm tra project) | `resetPasswordHandler` (`auth/resetpassword.ts:26`) |
| `POST /auth/setpassword` | `setPasswordValidator` | `setPasswordHandler` (`auth/setpassword.ts:36`) |
| `POST /auth/verifyemail` | `verifyEmailValidator` | `verifyEmailHandler` (`auth/verifyemail.ts:26`) |

Hàm dùng chung cần port: `createUser`, `sendVerificationEmail`, `createProject`, `createDefaultAccessPolicies`, `createClient`, `createProfile`, `createProjectMembership`, `getOpenRegistrationAccessPolicy`, `projectRegistrationAllowed`, `validateRecaptcha`, `verifyRecaptcha`, `getProjectByRecaptchaSiteKey`, `bcryptHashPassword`, `setPassword`, `revokeUserLogins`, `resetPassword`, `verifyEmail` (tạo yêu cầu), các hàm `securityrequest.ts`, `sendWelcomeEmail`, `registerNew` (chỉ cho seed, không có route).

Ngoài phạm vi P4 (xem mục P4.8): `auth/method.ts` (`isExternalAuth` chỉ dùng như hàm đọc ở `resetpassword`; route `/auth/method` thuộc P2), `/admin/*` (mời user, đặt lại MFA của người khác), project SMTP, đổi `mfa`.

## P4.2 Bảng ánh xạ source sang Go

Đích Go theo layering của [p0-scaffold.md](p0-scaffold.md) mục 5: `domain` và `application` chỉ dùng stdlib và `pkg/fhir`; adapter ở `infrastructure`.

### Domain (thuần, không I/O)

| Medplum | Hành vi | Go |
|---|---|---|
| `constants.ts:71,81` | Mật khẩu 8 ký tự tối thiểu, 72 byte tối đa | `domain/password/policy.go`: `Validate(pw)` trả thông báo đúng như `Password must be at least 8 characters`, `Password must be no more than 72 characters`; `ByteLimit` |
| `constants.ts:62-66`, `auth/securityrequest.ts:21-33,45-51` | Thời hạn theo loại, tính hết hạn, đường lùi cho bản ghi cũ | `domain/securityrequest/securityrequest.go`: `Type` (`reset`, `invite`, `verify-email`), `Window(type)`, `ExpiresAt(now, type)`, `IsExpired(req, now)`, `MaxWindow` |
| `auth/utils.ts:171-176`, `fhir/default-access-policies.ts:91-106` | Chính sách đăng ký mở: `Project.setting[openPatientRegistration].valueBoolean === true`, rồi `defaultAccessPolicies[Patient]`, rồi `defaultPatientAccessPolicy` | `domain/registration/registration.go`: `OpenRegistrationAccessPolicy(project)` |
| `auth/utils.ts:183-192`, `fhir/projectmembership.ts:33-44` | Project có cho đăng ký không: `active === false` thì `Project not active`; thiếu chính sách thì `Project does not allow open registration` | `domain/registration`: `ProjectRegistrationAllowed(project)`, `InactiveReason(project, membership)` |
| `fhir/default-access-policies.ts:23-83,127-191`, `packages/core/src/access.ts:15,21-31` | Sáu chính sách mặc định: `Patient`, `RelatedPerson`, `Admin`, `Practitioner`, `Bot`, `ClientApplication` (đúng thứ tự khóa của `DEFAULT_ACCESS_POLICIES`) | `domain/accesspolicy/defaults.go`: dựng `[]fhir.AccessPolicy` thuần. Go **chưa có** danh sách `projectAdminResourceTypes` (`AccessPolicy, Cron, Package, PackageRelease, PackageInstallation, Project, ProjectMembership, User, UserSecurityRequest`) và `protectedResourceTypes` (`DomainConfiguration, Enterprise, JsonWebKey, Login`); phải định nghĩa trong gói này, kèm test khớp với Medplum |
| `auth/utils.ts:397-427` | Dựng hồ sơ: `name[0].given=[first]`, `family=last`, `telecom=[{system:email,use:work,value}]` nếu có email, `meta.project` | `domain/registration`: `NewProfile(type, project, first, last, email)` |
| `auth/utils.ts:429-448` | Dựng membership: `project`, `user`, `profile` (kèm `details`) | `domain/registration`: `NewMembership(...)` |

### Application (`application/registration`, `application/credentials`)

| Medplum | Hành vi | Go |
|---|---|---|
| `auth/newuser.ts:41-124` | Luồng đăng ký, xem P4.4 mục A | `application/registration.Service.NewUser` |
| `auth/newuser.ts:126-150` | `createUser`: kiểm tra pwned, băm, tạo `User` | `application/registration.Service.createUser` (dùng lại cho seed) |
| `auth/newuser.ts:152-179` | `sendVerificationEmail`: tạo yêu cầu `verify-email`, gửi thư | `application/credentials.Service.SendVerificationEmail` (đặt ở `credentials` để `newproject` dùng chung, tránh vòng phụ thuộc) |
| `auth/newproject.ts:34-96` | Xem P4.4 mục B | `Service.NewProject` |
| `auth/newpatient.ts:25-86` | Xem P4.4 mục C | `Service.NewPatient` |
| `fhir/operations/projectinit.ts:107-190,192-209` | `createProject` trong một transaction: 6 chính sách, `Project`, client mặc định, hồ sơ `Practitioner`, membership admin | `application/registration.ProjectInit.Create` dùng `UnitOfWork` (`Store.RunInTransaction`) |
| `admin/client.ts:39-66` | `createClient`: `ClientApplication` với `secret = generateSecret(32)` cộng membership client | `ProjectInit.createClient` |
| `auth/register.ts:45-95` | `registerNew`: chỉ dùng cho seeder và test | `application/registration.Seeder.RegisterNew`, gọi từ lệnh `auth seed` (P0 bước 0.4), **không** có route |
| `auth/welcomeemail.ts:36-127` | Thư chào cho chủ project lần đầu, lỗi gửi chỉ ghi log | `application/mailtext` (văn bản) và `credentials`/`registration` gọi `Mailer` |
| `auth/resetpassword.ts:26-100,111-133` | Xem P4.4 mục D | `credentials.Service.ResetPassword`, `IssueSecurityRequest(type)` (tái dùng cho `invite` của P7) |
| `auth/setpassword.ts:36-65,83-120,128-149` | Xem P4.4 mục E | `credentials.Service.SetPassword`, `RevokeUserLogins` |
| `auth/verifyemail.ts:26-70,80-98` | Xem P4.4 mục F | `credentials.Service.VerifyEmail` |
| `auth/changepassword.ts:26-58` | Xem P4.4 mục G | `credentials.Service.ChangePassword` |
| `auth/securityrequest.ts:61-156` | `consume`, `supersede`, `retire`, `retirePassword`, `retireAll` | `credentials.SecurityRequests` (dùng cổng `UnitOfWork`, `Store.Mutate`) |
| `auth/utils.ts:649-690`, `566-577`, `609-630` | `validateRecaptcha`, `verifyRecaptcha`, tìm project theo site key | logic quyết định ở `application/registration.RecaptchaPolicy.Resolve(siteKey, projectID)` trả `(secretKey, *Project, error)`; middleware mỏng ở `presentation/httpapi/recaptcha.go` gọi cổng `Recaptcha.Verify` |
| `auth/utils.ts:642-647` | `bcryptHashPassword`, chặn >72 byte tại điểm băm duy nhất | cổng `PasswordHasher` (adapter `infrastructure/bcrypt.go`); chốt chặn >72 byte nằm **trong adapter** để mọi đường gọi đều bị chặn |
| `auth/utils.ts:93-104`, `69-75`, `83-85` | `readUserForAccountManagement` (403 khi On-Behalf-Of, không phải `User/`, có scope SMART; user toàn cục chỉ quản lý được khi `authMethod` là `password` hoặc `google`) | `application/credentials.AccountGuard.ReadUser(principal)` |
| `oauth/utils.ts:908-970` | `getUserByEmail`, `...InProject`, `...WithoutProject` (khớp `email` chính xác, project có hoặc thiếu) | `UserRepo.FindByEmail(email, projectID)` và `FindGlobalByEmail(email)` trong P1 |
| `oauth/utils.ts:984-993` | `timingSafeEqualStr` | `infrastructure/secure.go`: `Equal(a,b string) bool` dựa `crypto/subtle.ConstantTimeCompare` |
| `oauth/keys.ts:229-231` | `generateSecret(n)` = `n` byte ngẫu nhiên, mã hex | cổng `Random.Secret(n)`; adapter `crypto/rand`. `generateSecret(16)` cho `secret` của yêu cầu và `code`; `generateSecret(32)` cho secret client |
| `email/email.ts:34-64` | Chọn đường gửi; **không cấu hình thì chỉ log cảnh báo và bỏ qua** (`email.ts:62`) | cổng `Mailer.Send(ctx, Message)` (xem P4.5) |

### Presentation (`presentation/httpapi`)

| Medplum | Go |
|---|---|
| Validator của 7 route | `presentation/httpapi/validate.go` với DTO riêng: `NewUserRequest`, `NewProjectRequest`, `NewPatientRequest`, `ChangePasswordRequest`, `ResetPasswordRequest`, `SetPasswordRequest`, `VerifyEmailRequest`; thông báo lỗi giữ nguyên chữ (xem P4.4) |
| `verifyemail.ts:64-68` | Chuyển hướng "mềm": trả `200` với `OperationOutcome` `id: ok` kèm header `Location`, **không** trả 302 |
| Hạn mức theo IP | Bảng 0.3 |
| Đọc IP (`req.ip`) | Echo `c.RealIP()` với danh sách proxy tin cậy trong cấu hình; ghi vào `Login.remoteAddress` |

## P4.3 Mô hình dữ liệu

### Trường được đọc và ghi

| Resource | Đọc | Ghi |
|---|---|---|
| `User` (`pkg/fhir/resource_user.go`) | `email`, `project`, `passwordHash`, `emailVerified`, `firstName`, `lastName` | Tạo: `firstName`, `lastName`, `email` (đã hạ chữ thường), `passwordHash`, `project` (chỉ khi `projectId` có và khác `new`), `meta.project` (cùng điều kiện) (`auth/newuser.ts:138-146`). Sau đó chỉ vá `passwordHash` và `emailVerified` (`setpassword.ts:96-101`, `verifyemail.ts:57-59`) |
| `Login` | `membership`, `project`, `user`, `revoked` | `revoked=true` ở `revokeUserLogins` (`setpassword.ts:145`). `project` và `membership` do `setLoginMembership` (P2) |
| `UserSecurityRequest` | `type`, `used`, `expiresAt`, `secret`, `user`, `redirectUri`, `meta.lastUpdated`, `meta.versionId` | Tạo: `type`, `user`, `secret` (hex 32 ký tự), `expiresAt` (ISO), `redirectUri`, `meta.project = resolveId(user.project)` (có thể rỗng) (`resetpassword.ts:121-129`, `verifyemail.ts:89-97`). Sau đó chỉ đặt `used=true` |
| `Project` | `active`, `setting[openPatientRegistration]`, `defaultAccessPolicies`, `defaultPatientAccessPolicy`, `site[].recaptchaSiteKey/recaptchaSecretKey`, `owner` (tìm project đầu tiên của user) | Tạo: `name`, `owner`, `strictMode: true`, `features` (cấu hình `defaultProjectFeatures`), `systemSetting` (cấu hình `defaultProjectSystemSetting`), `defaultAccessPolicies` (`projectinit.ts:149-160`) |
| `ProjectMembership` | `user`, `active` (bởi `getMembershipsForLogin`, P2) | Tạo cho admin: `project`, `user`, `profile`, `admin: true`; cho client: `user=client`, `profile=client`, `accessPolicy` mặc định của `ClientApplication` (`admin/client.ts:54-63`); cho bệnh nhân: `accessPolicy` = chính sách đăng ký mở (`newpatient.ts:83-85`) |
| `ClientApplication` | | `name = <tên project> + " Default Client"`, `description = "Default client for " + <tên project>`, `secret`, `meta.project`, `meta.author` (giá trị `repo.getConfig().author` chưa xác nhận) |
| `AccessPolicy` ×6 | | `name`, `resource[]`, `meta.project` |
| `Practitioner`/`Patient` | | `name`, `telecom`, `meta.project` |

### Vòng đời `UserSecurityRequest`

| Sự kiện | Hành vi | Số liệu chính xác (nguồn) |
|---|---|---|
| Tạo | `type ∈ {reset, invite, verify-email}`; `secret = generateSecret(16)` (16 byte, hex, 32 ký tự); `expiresAt = now + thời hạn` | `reset` 1 giờ; `invite` 7 ngày; `verify-email` 7 ngày (`constants.ts:62-66`) |
| Thay thế (supersede) | Trước khi tạo yêu cầu mới, đánh `used=true` mọi yêu cầu **cùng loại** chưa dùng của user | `securityrequest.ts:87-93`; ca `resetpassword.test.ts:552` (loại `reset` không làm mất hiệu lực `invite`) |
| Phạm vi quét | Quét mọi yêu cầu có `_lastUpdated >= now - 7 ngày` (`MAX_EXPIRATION_MS`, dòng 98-114) của user, sắp theo `_lastUpdated`, trang 1000, theo từng trang chứ không giới hạn số bản ghi | `securityrequest.ts:109-134` |
| Kiểm tra hết hạn | Hết hạn khi `now >= expiresAt` (bằng nhau là hết hạn). Bản ghi cũ không có `expiresAt`: `meta.lastUpdated + thời hạn của type` (thiếu `type` thì dùng thời hạn `reset`). Không có mốc nào, hoặc không đọc được ngày: coi là hết hạn | `securityrequest.ts:21-24,45-51`; ca `securityrequest.test.ts:29-65` |
| Tiêu thụ (consume) | `UpdateResource` với `used=true` và `If-Match = versionId`; lỗi 412 thành `400 Already used`. Hai yêu cầu đồng thời cùng token: đúng một cái qua | `securityrequest.ts:61-76` |
| Thu hồi khi đổi mật khẩu | Đánh `used=true` mọi yêu cầu chưa dùng **trừ** `verify-email` (gồm cả bản ghi không có `type`) | `securityrequest.ts:144-147`; ca `setpassword.test.ts:184-265` (`verify-email` còn nguyên) |
| `retireAllSecurityRequests` | Đánh `used=true` mọi yêu cầu chưa dùng | `securityrequest.ts:154-156`; **không** thấy nơi gọi trong `auth/` (chưa xác nhận dùng ở `admin/`); không port đợt này |

## P4.4 Thuật toán từng bước

### A. `POST /auth/newuser` (rồi `tryLogin`)

Điều kiện tiên quyết theo thứ tự middleware (`auth/routes.ts:33`):

1. Hạn mức IP loại `login` (5/phút). Quá hạn: 429.
2. Validator (`auth/newuser.ts:21-34`), theo thứ tự trường: `firstName` 1 đến 128 ký tự (`First name must be between 1 and 128 characters`); `lastName` (`Last name must be between 1 and 128 characters`); `email` là email hợp lệ và dài 3 đến 72 (`Valid email address between 3 and 72 characters is required`); `password` tối thiểu 8 ký tự (`Password must be at least 8 characters`) và tối đa 72 byte (`Password must be no more than 72 characters`). Lỗi: 400 kiểu validator.
3. `validateRecaptcha(projectRegistrationAllowed)` (`auth/utils.ts:649-690`):
   1. `siteKey = body.recaptchaSiteKey`, `secret = config.recaptchaSecretKey`.
   2. Nếu `siteKey` có và khác `config.recaptchaSiteKey`: tìm một `Project` có `site.recaptchaSiteKey = siteKey` (cộng `_id = body.projectId` nếu có). Không thấy: `400 Invalid recaptchaSiteKey`. Lấy `secret` từ phần tử `site` khớp; thiếu: `400 Invalid recaptchaSecretKey`. Gọi `projectRegistrationAllowed(project)`; có lỗi thì trả lỗi đó (`Project not active` hoặc `Project does not allow open registration`).
   3. Nếu `secret` khác rỗng: thiếu `recaptchaToken`: `400 Recaptcha token is required`; `verifyRecaptcha` sai: `400 Recaptcha failed`. `verifyRecaptcha` gọi `POST https://www.google.com/recaptcha/api/siteverify?secret=..&response=..` và đọc `json.success` (`auth/utils.ts:566-577`). Lỗi mạng: ném lỗi chưa bắt, kết quả 500 (đóng khi lỗi, fail-closed).
   4. Không có `secret` thì **bỏ qua hoàn toàn** reCAPTCHA (ca `newuser.test.ts:644`).

Handler (`auth/newuser.ts:41-124`):

4. `config.registerEnabled === false` (chỉ khi đặt rõ là `false`): `400 Registration is disabled`. Lưu ý: bước này **sau** reCAPTCHA (thứ tự trong route), nên kẻ gọi không có token hợp lệ thấy lỗi reCAPTCHA trước.
5. `projectId = body.projectId`. Nếu có `clientId`: đọc `ClientApplication` (không có: 404). Nếu có cả `projectId`: `client.meta.project !== projectId` thì `400 Client and project do not match`. Nếu không có `projectId`: `projectId = client.meta.project`.
6. `email = body.email.toLowerCase()`.
7. Nếu `projectId` có và khác `new`: đọc `Project` (lỗi thì trả lỗi đọc, 404 nếu không có), rồi `projectRegistrationAllowed(project)`; lỗi thì trả.
8. Tìm user trùng: nếu `body.projectId` (**giá trị thô trong body, không phải `projectId` suy ra từ client**) có và khác `new` thì tìm theo `(email, project)`; ngược lại tìm user **không thuộc project nào** theo `email`. Có: `400 Email already registered`, `expression: ["email"]`.
9. `createUser`: gọi cổng `Pwned`; `count > 0` thì `400 Password found in breach database`, `expression: ["password"]`. Băm mật khẩu. Tạo `User` (bảng P4.3). Lưu ý: `createUser` đọc `projectId` từ **body thô** (`newuser.ts:127,140,145`), nên đăng ký chỉ bằng `clientId` tạo user **không** có `project`, trong khi `Login.project` (bước 10) vẫn là project của client. Ca `newuser.test.ts:436-588` (`res5`) xác nhận luồng này chạy được.
10. `tryLogin` (P2) với `authMethod: password`, `clientId`, `projectId` (đã suy ra), `scope = body.scope || "openid"`, `nonce = body.nonce || uuid`, `codeChallenge`, `codeChallengeMethod`, `email`, `password`, `remember`, `remoteAddress = IP`, `userAgent`, `allowNoMembership: true`. Hệ quả: tạo `Login` chưa có membership.
11. Nếu `config.requireVerifiedEmailForProjectCreation` và `projectId === "new"`: gửi thư xác minh (mục F.5) và `emailVerificationRequired = true`.
12. Trả `200 { login: login.id, emailVerificationRequired }`. **Không** có `code` (ca `newuser.test.ts:57-74`).
13. Mọi lỗi trong bước 9 đến 11 bị bắt và trả qua `normalizeOperationOutcome`: lỗi thường (kể cả lỗi mạng khi hỏi pwned, lỗi SMTP) thành **400** với `message` làm thông báo (`newuser.ts:121-123`).

Điểm lệch có chủ ý đề xuất (xem P4.8 mục "lệch có chủ đích"): thực hiện `validateLoginRequest` và `validatePkce` **trước** bước 9, vì ở source chúng chạy sau khi đã tạo user, nên PKCE sai tạo user rồi trả 400, lần sau bị `Email already registered`.

### B. `POST /auth/newproject`

Validator: `login` là UUID (`Login ID is required.`, có dấu chấm cuối); `projectName` 4 đến 72 ký tự (`Project name must be between 4 and 72 characters`) (`newproject.ts:22-25`).

1. `registerEnabled === false`: `400 Registration is disabled`.
2. `newProjectRegistrationEnabled === false`: `400 New project registration is disabled`.
3. Đọc `Login` (không có: 404).
4. `login.membership` có: `400 Login already has a membership`.
5. `login.project.reference` có và khác `Project/new`: `400 Invalid project`.
6. Đọc `User` từ `login.user`.
7. Nếu `requireVerifiedEmailForProjectCreation` và `!user.emailVerified`: gửi thư xác minh (F.5) rồi `400 Email verification is required to create a project. Check your email for a verification link.`
8. `isFirstProject = không có Project nào có owner = User/<id>`.
9. `createProject(projectName, user)` (một transaction, mục B.1), rồi `setLoginMembership(login, membership)` (P2).
10. Nếu `isFirstProject`: gửi thư chào (lỗi chỉ ghi log).
11. `sendLoginResult(updatedLogin)` (có thể trả `mfaRequired` thay vì `code`, xem P5).

B.1 `createProject` (`projectinit.ts:125-190`), toàn bộ trong một transaction:
1. Sinh `projectId` trước.
2. Tạo 6 `AccessPolicy` (thứ tự `Patient`, `RelatedPerson`, `Admin`, `Practitioner`, `Bot`, `ClientApplication`), `meta.project = projectId`.
3. Tạo `Project` (id gán sẵn) với `owner`, `strictMode: true`, `features`, `systemSetting`, `defaultAccessPolicies`.
4. `createClient`: `ClientApplication` và membership của client.
5. Tạo `Practitioner` (tên và email của admin) và `ProjectMembership` với `admin: true`.

Quan sát cần xử lý (mục P4.8): bước 9 không kiểm tra `login.revoked`, `login.granted`, `login.mfaVerified`, `login.authMethod` **trước** khi tạo project; `setLoginMembership` mới ném `Login revoked` (`oauth/utils.ts:514-520`) sau khi project đã được tạo.

### C. `POST /auth/newpatient`

Validator: `login` không rỗng (`Missing login`); `projectId` không rỗng (`Project ID is required`).

1. Đọc `Login` (404 nếu không có). `login.membership` có: `400 Login already has a membership`.
2. `login.project.reference` có và khác `Project/<projectId>`: `400 Invalid project`.
3. Đọc `User`; `!user.project`: `400 User must be scoped to the project`.
4. `createPatient` (`newpatient.ts:65-86`): đọc `Project` (`projectId`); `getOpenRegistrationAccessPolicy(project)` rỗng: `400 Project does not allow open registration`; tạo `Patient` (`createProfile`); đọc chính sách; tạo membership với `accessPolicy = chính sách`.
5. `setLoginMembership`, rồi `sendLoginResult`.

Lưu ý: không so `user.project` với `projectId` (suy ra an toàn nhờ bước 2 và cách `tryLogin` tìm user, nhưng **chưa xác nhận** bằng test); không có hạn mức `login` (route này thuộc loại `auth`).

### D. `POST /auth/resetpassword`

Validator: `email` như `newuser`. Middleware: `validateRecaptcha()` không kèm kiểm tra project (`routes.ts:40`).

1. `email = body.email.toLowerCase()`.
2. `isExternalAuth(email)` (P2: tra `DomainConfiguration` theo phần sau `@`, cần `identityProvider.authorizeUrl` và `clientId`): có thì `400 Cannot reset password for external auth. Contact your system administrator.`
3. Tìm **một** `User`: `email` khớp chính xác và (`project = Project/<body.projectId>` nếu có `projectId`, ngược lại `project` thiếu).
4. Không thấy: trả `200 allOk` **ngay**, không gửi thư (chống dò tài khoản, `resetpassword.ts:65-70`).
5. `resetPassword(user, "reset", body.redirectUri)`: supersede các yêu cầu `reset` cũ, tạo yêu cầu mới (1 giờ), trả URL `appBaseUrl + "setpassword/<id>/<secret>"` (nối bằng `concatUrls`).
6. Nếu `body.sendEmail !== false`: đọc `Project` của user nếu có, gửi thư (văn bản ở P4.5).
7. `200 allOk`.

### E. `POST /auth/setpassword`

Validator: `id` là UUID (`Invalid request ID`); `secret` không rỗng (`Missing secret`); `password` như `newuser`.

1. Đọc `UserSecurityRequest` theo `id` (không có: 404).
2. `used` có: `400 Already used`.
3. `type === "verify-email"`: `400 Invalid request type`.
4. Hết hạn: `400 Expired`.
5. So `secret` bằng hàm so sánh thời gian cố định: sai thì `400 Incorrect secret`.
6. Đọc `User`, gọi `setPassword(user, password, request)` (E.1), trả `200 allOk`.

Thứ tự kiểm tra ở source: **dùng rồi > loại > hết hạn > secret**. (`verifyemail` thì: loại > dùng rồi > hết hạn > secret.) Hệ quả: người biết `id` (UUID) nhưng không biết `secret` vẫn thấy trạng thái `Already used`/`Expired`. Chấp nhận (UUID 122 bit), ghi trong P4.8.

E.1 `setPassword` (`setpassword.ts:83-120`):
1. Gọi `Pwned`; `count > 0`: `400 Password found in breach database` (không expression).
2. Băm (chặn > 72 byte).
3. Chuẩn bị bản vá `passwordHash`; nếu có `securityRequest`: thêm `emailVerified = true` (đổi mật khẩu bằng liên kết chứng minh người dùng nhận được thư).
4. Một transaction: (a) nếu có `securityRequest`, **tiêu thụ trước** (CAS theo `versionId`; 412 thành `400 Already used`); (b) `retirePasswordSecurityRequests(user)`; (c) vá `User`. Tiêu thụ **sau** khi kiểm tra pwned và băm nên mật khẩu bị từ chối không làm mất liên kết (ca `setpassword.test.ts:567`); và **cùng transaction** với việc áp mật khẩu nên lỗi áp mật khẩu cuộn lại việc tiêu thụ (ca `setpassword.test.ts:611`).
5. **Ngoài** transaction: `revokeUserLogins(user)`: duyệt mọi `Login` của user theo `_lastUpdated` tăng dần, trang tối thiểu bằng `minCursorBasedSearchPageSize`, vá `revoked=true` cho bản chưa revoke (`setpassword.ts:128-149`). Nếu thu hồi dở dang, lần thử lại hoặc lần đổi mật khẩu sau thu hồi nốt (ghi chú dòng 76-78).

### F. `POST /auth/verifyemail`

Validator: như `setpassword` (`id`, `secret`).

1. Đọc yêu cầu (404 nếu không có).
2. `type !== "verify-email"`: `400 Invalid user security request type`.
3. `used`: `400 Already used`. 4. Hết hạn: `400 Expired`. 5. Secret sai: `400 Incorrect secret`.
6. Transaction: tiêu thụ (CAS), vá `User.emailVerified = true` (đọc user bên trong transaction để không ghi đè thay đổi đồng thời).
7. Có `redirectUri`: trả `200` `redirectOk(new URL(redirectUri))` (kèm `Location`); không có: `200 allOk`. `redirectUri` không phải URL hợp lệ làm `new URL` ném lỗi **sau** khi đã tiêu thụ (quan sát, chưa có ca test).

F.5 `sendVerificationEmail(user, login)` (`newuser.ts:152-179`): `redirectUri = appBaseUrl + "register?login=<login.id>"`; `verifyEmail(user, redirectUri)` tạo yêu cầu `verify-email` 7 ngày (supersede loại này); URL `appBaseUrl + "verifyemail/<id>/<secret>"`; đọc `Project` của user nếu có; gửi thư. Lỗi gửi ném ra (khác thư chào).

### G. `POST /auth/changepassword` (Bearer)

Validator: `oldPassword` không rỗng (`Missing oldPassword`); `newPassword` như `newuser`.

1. `readUserForAccountManagement` (403 khi On-Behalf-Of, tài khoản không phải `User/` như `ClientApplication`, có scope SMART, hoặc user toàn cục mà login không phải `password`/`google`).
2. `passwordHash` rỗng: `400 Existing password not set`, `expression: ["oldPassword"]`.
3. `bcrypt.compare(old)` sai: `400 Incorrect password`, `expression: ["oldPassword"]`.
4. `setPassword(user, new)` không kèm yêu cầu (nên **không** đặt `emailVerified`), tức cũng thu hồi **mọi** login, kể cả phiên hiện tại.

### H. Seeder `registerNew` (`auth/register.ts:45-95`, chỉ test và seed)

Hạ chữ thường email; băm mật khẩu; tìm user toàn cục theo email hoặc tạo (không kiểm tra pwned); `tryLogin(allowNoMembership)`; `createProject`; phát token. Go chỉ cần phần tạo user cộng project cho lệnh seed và cho helper test.

## P4.5 Cổng (ports), adapter, thư viện, thư và cấu hình

### Cổng và adapter

| Cổng (`application/ports.go`) | Phương thức (mô tả) | Adapter (`infrastructure`) | Fake (test) |
|---|---|---|---|
| `Clock` | `Now()` | `systemClock` | đồng hồ chỉnh tay |
| `Random` | `Secret(nBytes) string` (hex), `Code6() string`, `UUID()` | `crypto/rand` (`Code6` = số nguyên đều trong [0, 1 000 000), đệm số 0 đến 6 chữ số, `auth/utils.ts:352`) | chuỗi định sẵn |
| `PasswordHasher` | `Hash(pw)`, `Compare(pw, hash) bool`, từ chối > 72 byte | `golang.org/x/crypto/bcrypt` (đã `indirect` trong `go.mod`), cost mặc định 10 (`config/utils.ts:36`) | băm rẻ (cost 4) |
| `Pwned` | `Count(ctx, pw) (int, error)` | HIBP k-anonymity: SHA-1, 5 ký tự đầu gửi lên `https://api.pwnedpasswords.com/range/<prefix>`, so phần đuôi (thư viện `hibp` của Medplum: chi tiết nội bộ như timeout, header padding **chưa xác nhận** vì không có `node_modules`; adapter Go tự viết bằng `net/http`, không thêm dependency) | trả số định sẵn |
| `Recaptcha` | `Verify(ctx, secret, token) (bool, error)` | `POST https://www.google.com/recaptcha/api/siteverify?secret=..&response=..`, đọc `success` | trả định sẵn |
| `Mailer` | `Send(ctx, Message{To, Subject, Text, Project})` | `log` (in link ra log, cho dev), `smtp` (`net/smtp` hoặc `wneessen/go-mail`, chọn khi làm), `none` (bỏ qua như `email.ts:62`) | ghi lại thư để đối chiếu |
| `UnitOfWork` | `Do(ctx, fn(Tx))` | `fhirstore.Store.RunInTransaction` | in-memory |
| `Locker` | `Lock(ctx, key)` | `fhirstore.Store.LockKey` (khóa tư vấn đến hết transaction) | no-op |
| Repo theo tập hợp (`UserRepo`, `LoginRepo`, `SecurityRequestRepo`, `ProjectRepo`, `MembershipRepo`, `ClientRepo`, `PolicyRepo`, `ProfileRepo`) | CRUD, `Mutate(id, fn)` (đọc rồi `IfMatch`, thử lại), `Find...` | `fhirstore` | in-memory |

Danh sách thư viện đề xuất: `golang.org/x/crypto/bcrypt`; stdlib `crypto/rand`, `crypto/subtle`, `crypto/sha1` (chỉ cho HIBP), `net/http`. Không cần thư viện mới ở P4 ngoài thư viện gửi thư (quyết định D4).

### Nội dung thư (đúng chữ trong source)

Chủ đề và nội dung dưới đây giữ nguyên thương hiệu "Medplum" như source; việc đổi thương hiệu là câu hỏi mở (P4.8). Mỗi `Text` là các dòng nối bằng `\n`, dòng cuối rỗng nên kết thúc bằng xuống dòng.

**Xác minh email** (`auth/newuser.ts:158-177`), chủ đề `Medplum Email Verification`:

```
We received a request to create a Medplum account using this email address.

Please click the following link to verify your email address:

<appBaseUrl>verifyemail/<id>/<secret>

If you received this in error, you can safely ignore it.

Thank you,
Medplum
```

**Đặt lại mật khẩu** (`auth/resetpassword.ts:80-93`), chủ đề `Medplum Password Reset`:

```
Someone requested to reset your Medplum password.

Please click on the following link:

<appBaseUrl>setpassword/<id>/<secret>

If you received this in error, you can safely ignore it.

Thank you,
Medplum
```

**Chào mừng** (`auth/welcomeemail.ts:36-62`), chủ đề `Welcome to Medplum`, chỉ văn bản thuần (không `html`, ca `welcomeemail.test.ts:82-93`):

```
Hi <firstName>,                       (không có tên thì chỉ "Hi,")

Welcome to Medplum! Your new project "<projectName>" is ready to go.

Here are a few things to help you get started:

- Sign in to your project: <appBaseUrl>signin
- Plan your application with an AI agent
    Start with a copyable prompt to work through your requirements and save a
    plan for implementation, whether you're customizing Medplum Provider or
    building a custom app.
    Get started: https://www.medplum.com/docs/building-with-ai-coding-assistants#plan-your-application
- Read the docs: https://www.medplum.com/docs
    - Contribute: https://github.com/medplum/medplum#contributing
- Invite your teammates from the Admin panel: <appBaseUrl>admin/users

If you have any questions, just reply to this email or reach out to us at
<supportEmail>. Also, join our community on Discord: https://discord.gg/medplum.

Thank you,
The Medplum Team
```

Quy tắc đi kèm: `projectName` mặc định `your project` khi project chưa có tên (`welcomeemail.ts:114`, ca `welcomeemail.test.ts:124`); `supportEmail` lấy địa chỉ thuần trong `"Tên" <a@b>` (`extractEmailFromAddress`, `email/utils.ts:238-247`); không gửi nếu user không có email; lỗi gửi chỉ ghi log `Failed to send welcome email` kèm `projectId` (`welcomeemail.ts:119-126`). Lưu ý dòng 46 và 54 nối `appBaseUrl + "signin"` thẳng, tức yêu cầu `appBaseUrl` kết thúc bằng `/` (các thư khác dùng `concatUrls` nên không phụ thuộc). Go: chuẩn hóa `AppBaseURL` có dấu `/` cuối ngay lúc nạp cấu hình.

### Cấu hình (biến môi trường, theo kiểu `pkg/envconfig`, không tiền tố)

Giá trị mặc định trong ngoặc là của Medplum nếu đã đọc được; còn lại là đề xuất.

| Biến | Ý nghĩa | Mặc định | Nguồn |
|---|---|---|---|
| `REGISTER_ENABLED` | `false` chặn `newuser`, `newproject` | `true` | `config/types.ts:79`; Medplum chỉ chặn khi đặt rõ `false` (`newuser.ts:43`) |
| `NEW_PROJECT_REGISTRATION_ENABLED` | `false` cho phép đăng ký user nhưng chặn tạo project | `true` | `config/types.ts:80-81` |
| `REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION` | Bắt xác minh email trước khi tạo project | `false` (chưa đặt) | `config/types.ts:244` |
| `APP_BASE_URL` | Gốc URL của ứng dụng web, dùng trong thư | bắt buộc, chuẩn hóa `/` cuối | `config/types.ts:16` |
| `SUPPORT_EMAIL` | Địa chỉ gửi và địa chỉ trong thư chào | bắt buộc | `config/types.ts:23` |
| `BCRYPT_COST` | Số vòng bcrypt | `10` | `config/utils.ts:36` |
| `RECAPTCHA_SITE_KEY`, `RECAPTCHA_SECRET_KEY` | reCAPTCHA mức máy chủ; rỗng thì bỏ qua | rỗng | `config/types.ts:64-65`; `newuser.test.ts:644` |
| `DEFAULT_PROJECT_FEATURES`, `DEFAULT_PROJECT_SYSTEM_SETTING` | Gán cho project mới | rỗng | `config/utils.ts:43-44`; `projectinit.ts:156-157` |
| `PWNED_ENABLED`, `PWNED_API_URL`, `PWNED_TIMEOUT` | Đề xuất mới (Medplum không có) | `true`, URL HIBP, `5s` | đề xuất |
| `MAIL_PROVIDER` (`log`, `smtp`, `none`), `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_SECURE` | Cổng `Mailer` | `log` ở dev, `none` ở test | `email/email.ts:55-63`, `config/types.ts:57-58` |
| `TRUSTED_PROXIES` | Để `RealIP()` đúng | rỗng | đề xuất |
| `RATE_LIMIT_LOGIN`, `RATE_LIMIT_MFA`, `RATE_LIMIT_AUTH` | Hạn mức theo IP, mỗi phút | 5, 10, 160 | mục 0.3 |

Cờ cấu hình ở Medplum được kiểm tra "có chủ đích khác `false`" (`registerEnabled === false`). Trong Go dùng `bool` mặc định `true` để cùng nghĩa.

## P4.6 Danh sách ca test (đặc tả lấy từ `*.test.ts`)

Ký hiệu cột cuối: **Giữ** (port nguyên), **Đổi** (port nhưng thay phụ thuộc), **Bỏ** (không port đợt này, ghi lý do).

### `/auth/newuser` (`auth/newuser.test.ts`)

| Ca (dòng) | Mô tả | Kỳ vọng | |
|---|---|---|---|
| Success (57) | `projectId: "new"`, dữ liệu hợp lệ | 200, có `login`, không có `code`, `emailVerificationRequired: false` | Giữ |
| Register disabled (76) | `registerEnabled=false` | 400 `Registration is disabled` | Giữ |
| Missing recaptcha (93) | có secret, không token | 400 `Recaptcha token is required` | Giữ |
| Incorrect recaptcha (108) | verify trả sai | 400 `Recaptcha failed` | Giữ |
| Password too short (126) | `xyz` | 400 `Password must be at least 8 characters` | Giữ |
| Password too long (140) | 300 ký tự | 400 `Password must be no more than 72 characters` | Giữ |
| Multibyte password too long (154) | `☺️`×40 | 400 `...no more than 72 characters` | Giữ |
| Multibyte password too short (171) | `🔑🔓` (2 ký tự, 8 byte) | 400 `...at least 8 characters` (tối thiểu đếm ký tự) | Giữ |
| Breached password (188) | pwned = 1 | 400, bằng `badRequest('Password found in breach database','password')` | Giữ |
| Email already registered (207) | đăng ký hai lần | lần hai 400 `Email already registered` | Giữ |
| Custom recaptcha client success (224) | project có `site` và `openPatientRegistration` | 200 có `login`, không có `code` | Giữ (cần `Project.site`) |
| Custom recaptcha client with incorrect project ID (275) | site key đúng, `projectId` ngẫu nhiên | 400 `Invalid recaptchaSiteKey` | Giữ |
| Custom recaptcha rejects when open registration disabled (327) | thiếu `openPatientRegistration` | 400 `Project does not allow open registration` | Giữ |
| Recaptcha site key not found (375) | site key lạ | 400 `Invalid recaptchaSiteKey` | Giữ |
| Recaptcha secret key not found (391) | `site` thiếu secret | 400 `Invalid recaptchaSecretKey` | Giữ |
| Isolated projects (436) | 6 kịch bản: đăng ký vào P2, P3; `projectId`+`clientId` khớp (200); không khớp (400 `Client and project do not match`); chỉ `clientId` (200); `clientId` lạ (404) | như nêu | Giữ (dùng helper thay `registerNew`) |
| Self-registered user has meta.project set (590) | có `projectId` | `User.meta.project = projectId` | Giữ |
| Self-registered user without projectId has no meta.project (623) | không `projectId` | `User.meta.project` rỗng | Giữ |
| Success when config has empty recaptchaSecretKey (644) | secret rỗng, không token | 200 | Giữ |
| Require email verification for new project (661) | `requireVerified...=true`, `projectId: "new"` | 200, `emailVerificationRequired: true` | Giữ |

### `/auth/newproject` (`auth/newproject.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Success (37) | `newuser` (kèm PKCE `plain`) rồi `newproject` 200 có `code`; đổi `code` ở `/oauth2/token` 200 (cần P3); đọc `profile` bằng token 200 (cần `fhir-server`); dùng lại `login` 400; thiếu `login` 400 | Đổi: bỏ bước token/FHIR khỏi test đơn vị, giữ trong E2E (P4.10 bước 4.7) |
| Sends welcome email to the new project owner (84) | thư chủ đề `Welcome to Medplum` tới đúng email, nội dung chứa tên project, kèm `Project` | Giữ (dùng `Mailer` giả) |
| Does not send welcome email when the user already owns a project (116) | có sẵn `Project` với `owner` là user, không gửi thư chào | Giữ |
| Default ClientApplication is restricted to project (153) | client mặc định không thấy dữ liệu project khác | Bỏ khỏi P4: phụ thuộc `fhir-server` và chính sách truy cập; chuyển thành ca tích hợp ở P3 |
| GraphQL is restricted to project (273) | | Bỏ: GraphQL không port |
| Require verified email - rejected (369) | 400 `Email verification is required to create a project. Check your email for a verification link.` | Giữ |
| Require verified email - sends a verification link (396) | 400, và thư `Medplum Email Verification` tới email, chứa `verifyemail/` | Giữ |
| Require verified email - allowed (431) | `emailVerified=true` thì 200 | Giữ |
| Login for an existing project (459) | `login.project` đặt vào project khác: 400 `Invalid project` | Giữ |
| Register disabled (487) | 400 `Registration is disabled`, kể cả đường `login` với `projectId: "new"` của user đã có (res3, res4) | Giữ |
| New project registration disabled (528) | 400 `New project registration is disabled`; `newuser` vẫn 200 | Giữ |

### `/auth/newpatient` (`auth/newpatient.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Patient registration (33) | đăng ký bệnh nhân vào project mở; `code` đổi được token; dùng lại `login` 400; thiếu `login` 400; có đúng 1 membership theo `profile`; bệnh nhân chỉ thấy Observation của mình | Đổi: phần quyền đọc (res8 đến res12) cần `fhir-server` và `fhirauth`, đưa vào E2E |
| Uses legacy defaultPatientAccessPolicy when defaultAccessPolicies has no Patient entry (180) | `membership.accessPolicy.reference` = chính sách cũ | Giữ |
| Fails when open registration is not enabled (231) | `newuser` vào project chưa mở: 400 `Project does not allow open registration` | Giữ |

### `/auth/resetpassword` (`auth/resetpassword.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Blank email address (61) | 400 `Valid email address between 3 and 72 characters is required`, `expression[0] = email` | Giữ |
| Missing recaptcha (71) | `recaptchaToken: ""` 400 `Recaptcha token is required` | Giữ |
| Incorrect recaptcha (80) | 400 `Recaptcha failed` | Giữ |
| User not found (91) | 200 và **không** gửi thư | Giữ |
| Email with comma is not a list of users (103) | `"x,other@example.com,y"@example.com`: 200, không thư | Giữ |
| Success (112) | 200, 1 thư tới `email`, chủ đề `Medplum Password Reset` | Giữ |
| Success no send email (139) | `sendEmail:false`: 200, không thư | Giữ |
| Success with no recaptcha secret key and missing recaptchaToken (161) | 200, có thư | Giữ |
| Project-scoped user uses project SMTP (190) | dùng SMTP của project | Bỏ: project SMTP không port |
| External auth (235) | domain có `identityProvider`: 400 `Cannot reset password for external auth. Contact your system administrator.`, không thư | Giữ |
| Custom reCAPTCHA site key success (266) | site key của project: 200, có thư | Giữ |
| Custom reCAPTCHA ... `Invalid recaptchaSecretKey` (312) | 400, không thư | Giữ |
| Custom reCAPTCHA ... `Invalid recaptchaSiteKey` (352) | 400, không thư | Giữ |
| User without project (381) | có `projectId`, user không thuộc project: 200 `All OK`, không thư | Giữ |
| User with the project success (410) | user thuộc project, có `projectId`: 200, 1 thư | Giữ |
| Password change request with redirectUri (456) | `UserSecurityRequest.redirectUri = "http://example.com"` | Giữ |
| Sets expiration and supersedes prior requests (519) | yêu cầu đầu `used` sau khi tạo yêu cầu thứ hai; `expiresAt` ở tương lai; `setpassword` bằng yêu cầu đầu 400 | Giữ |
| Supersession is scoped to the request type (552) | `reset` không làm `invite` thành `used` | Giữ |

### `/auth/setpassword` (`auth/setpassword.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Success (59) | reset, lấy `id` và `secret` từ thư, đặt mật khẩu 200; đăng nhập bằng mật khẩu mới 200; dùng lại yêu cầu 400; token cũ 401; `Login` khác của user `revoked=true` | Giữ (phần 401 cần P3) |
| UserSecurityRequest (141) | yêu cầu tạo tay, không `expiresAt`: 200 | Giữ |
| Setting a password retires earlier links (184) | sau khi dùng `reset`: `invite` 400 `Already used`; `verify-email` còn dùng được | Giữ |
| Setting a password without a link retires earlier links (232) | `setPassword` không kèm yêu cầu cũng làm `invite` thành `Already used` | Giữ |
| Setting a password revokes all logins (267) | 25 login thêm tay đều `revoked`; sau đó 45 login nữa và `revokeUserLogins` với trang nhỏ nhất vẫn thu hồi hết (kiểm tra phân trang) | Giữ |
| Sign-in checked against a password replaced before its login was stored is rejected (317) | đổi mật khẩu giữa lúc `bcrypt.compare`: 400 `Email or password is invalid`, mọi login `revoked`, mật khẩu mới đăng nhập được | Đổi: thuộc P2 (`tryLogin`), nhưng **phụ thuộc** P4; viết ở P2, kiểm lại khi P4 xong |
| Sign-in is rejected when the password is replaced just before its login is stored (357) | như trên, đổi ngay trước khi lưu `Login` | Đổi: như trên |
| Completing a sign-in keeps the revocation from a password change (404) | `verifyMfaToken` và `setLoginScope` không làm sống lại login đã revoke | Đổi: giữ ở P5/P2 (liên quan "Mutate" thay vì ghi đè) |
| Expired UserSecurityRequest (443) | `expiresAt` quá khứ: 400 `Expired`, mật khẩu cũ vẫn dùng được | Giữ |
| UserSecurityRequest without expiresAt expires from lastUpdated (486) | `lastUpdated` cũ hơn 1 giờ + 60 giây: 400 `Expired` | Giữ |
| UserSecurityRequest cannot be used twice (525) | lần hai 400 `Already used` | Giữ |
| Breached password does not consume the UserSecurityRequest (567) | pwned 10: 400; đổi pwned 0: cùng liên kết vẫn 200 | Giữ |
| Failure to apply the password does not consume the UserSecurityRequest (611) | lỗi ghi `User`: không 200, `used` vẫn falsy, thử lại 200 | Giữ (kiểm tra `RunInTransaction` cuộn lại) |
| UserSecurityRequest invalid type (675) | `verify-email`: 400 | Giữ (kỳ vọng thông báo `Invalid request type` theo source) |
| Wrong secret (710) | 400 | Giữ |
| Breached password (744) | 400 `Password found in breach database` | Giữ |
| Missing id (783), Missing secret (795), Missing password (804) | 400 | Giữ |
| Password too long (816) | 73 ký tự: 400 `Password must be no more than 72 characters` | Giữ |
| Not found (829) | id lạ: 404 | Giữ |

### `/auth/verifyemail` (`auth/verifyemail.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Success (62) | secret thiếu ký tự cuối: 400; đúng: 200, yêu cầu `used`, `User.emailVerified=true`; dùng lại 400 | Giữ |
| Incorrect UserSecurityRequest.type (99) | `invite` và `reset`: 400 | Giữ |
| Sets expiration and supersedes prior requests (114) | yêu cầu cũ `used` sau khi tạo mới; dùng yêu cầu cũ 400 | Giữ |
| Expired UserSecurityRequest (131) | 400 `Expired`, `emailVerified` không đổi | Giữ |
| UserSecurityRequest without expiresAt expires from lastUpdated (148) | 400 `Expired` (cửa sổ 7 ngày + 60 giây) | Giữ |

### `/auth/changepassword` (`auth/changepassword.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Success (35) | 200 | Giữ |
| Missing old password (57) | 400 | Giữ |
| ClientApplication cannot change password (79) | token của `ClientApplication`: 403 | Giữ |
| Incorrect old password (94) | 400, bằng `badRequest('Incorrect password','oldPassword')` | Giữ |
| Breached password (117) | 400 `Password found in breach database` | Giữ |

### `securityrequest.ts` (`auth/securityrequest.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Uses the window for the request type (18) | `expiresAt >= now + cửa sổ` | Giữ |
| Reset expires sooner than invite (24) | cửa sổ `reset` < `invite` | Giữ |
| Unexpired (30), Expired (35) | theo `expiresAt` | Giữ |
| Unparseable expiresAt is expired (40) | `not-a-date`: hết hạn | Giữ |
| Falls back to lastUpdated when expiresAt is absent (44) | gần: chưa hết hạn; quá cửa sổ `reset`: hết hạn | Giữ |
| Fallback uses the window for the request type (52) | `invite`, `verify-email` quá 1 giờ: chưa hết hạn | Giữ |
| Missing type falls back to the reset window (58) | không `type`: dùng 1 giờ | Giữ |
| No timestamps at all is expired (63) | hết hạn | Giữ |

### Thư chào (`auth/welcomeemail.test.ts`) và `register.ts`

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| Includes greeting, quoted project name, and dynamic links (52) | chứa `Hi Alexander`, `Your new project "Hamilton Project" is ready to go.`, `<appBaseUrl>signin`, `<appBaseUrl>admin/users`, email hỗ trợ | Giữ |
| Includes planning as a top-level next step and indents the contribution link (61) | có `\n- Plan your application with an AI agent\n`, link kế hoạch, `\n    - Contribute:` | Giữ |
| Contains no markup or bold markers (68) | không `**`, không `<` | Giữ |
| Omits first name when not provided (74) | không có `undefined`, bắt đầu bằng `Hi,\n` | Giữ |
| Sets recipient, subject, and a plain-text body only (83) | không `html`, không `from` | Giữ |
| Sends to the project owner using server settings (96) | 1 lần gửi, đúng người nhận, chủ đề, kèm project | Giữ |
| Uses the bare support email address in the body (112) | thấy `support@medplum.com`, không thấy dạng `"Medplum" <...>` | Giữ |
| Falls back to a default project name when unnamed (124) | chứa `your project` | Giữ |
| Does not send when the user has no email (134) | không gửi | Giữ |
| Swallows send failures and logs a warning (143) | không ném lỗi, log `Failed to send welcome email` | Giữ |
| `register.test.ts` Success (24) | có `profile`, `accessToken`, `Login.meta.project = project.id` | Đổi: chỉ phần seeder; phần token thuộc P3 |

### Hàm thuần dùng ở P4 (`auth/utils.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| getOpenRegistrationAccessPolicy: Requires openPatientRegistration to be true (63) | thiếu hoặc `false`: rỗng | Giữ |
| ... Prefers the Patient defaultAccessPolicies entry, then defaultPatientAccessPolicy (76) | ưu tiên mục `Patient`, sau đó `defaultPatientAccessPolicy`, không có: rỗng | Giữ |
| bcryptHashPassword: Rejects a password longer than the maximum byte length (180) | ném lỗi `Password must be no more than 72 characters` | Giữ (ở adapter) |
| bcryptHashPassword: Measures length in bytes, not code points (188) | `😀`×19 (76 byte) bị từ chối | Giữ |
| hasSmartResourceScopes (203) | `undefined` và `openid ...` false; `openid user/Observation.rs`, `patient/*.rs`, `user/*.*` true | Giữ |
| getCallerUserReference (227, 231) | không scope SMART: trả user; có scope SMART: bị từ chối | Giữ |
| isPractitionerEmailDomainAllowed (99-176) | | Bỏ: chỉ dùng ở `admin/invite.ts:112` (P7) |

## P4.7 Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Nguồn | Test bắt buộc (Go) |
|---|---|---|---|
| S1 | Không lộ user tồn tại ở `resetpassword`: user không có trả 200 giống user có | `resetpassword.ts:65-70` | Ca "User not found" và "User without project" (200, không thư). **Thêm**: so hình dạng và mã trả về giữa user có và không có (cùng body, cùng header) |
| S2 | Rủi ro còn lại của S1 (không phải do Medplum cố ý): (a) đường "user có" chậm hơn vì ghi DB và gửi thư (`resetpassword.ts:72-97`); (b) nếu gửi thư lỗi (SMTP hỏng) thì "user có" trả 500 còn "không có" trả 200, vì `sendEmail` ném lỗi (`email/email.ts:151-163`) và không có `try/catch` ở `resetpassword.ts:76` | đã đọc | Đề xuất lệch (P4.8 L2): gửi thư không đồng bộ hoặc nuốt lỗi gửi, luôn 200. Test: `Mailer` giả luôn lỗi, vẫn 200 |
| S3 | So sánh thời gian cố định cho `secret` của `setpassword` và `verifyemail` | `oauth/utils.ts:984-993` (khác độ dài trả `false` ngay, giống `ConstantTimeCompare`) | Test: dùng `crypto/subtle`; test tĩnh bằng kiểm tra mã không có `==` trên `secret` |
| S4 | Dùng một lần, an toàn khi đồng thời | `securityrequest.ts:61-76` | Test đua: N yêu cầu song song cùng `id`/`secret`, đúng 1 thành công, các cái còn lại `Already used` |
| S5 | Mật khẩu bị từ chối không làm mất liên kết; lỗi áp mật khẩu cuộn lại | `setpassword.ts:567,611` | Hai ca đã nêu |
| S6 | Đổi mật khẩu thu hồi mọi login, mọi trang | `setpassword.ts:128-149` | Ca "revokes all logins" với > 1 trang |
| S7 | Liên kết cũ không thay được mật khẩu mới | `retirePasswordSecurityRequests` | Ca `setpassword.test.ts:184,232` |
| S8 | bcrypt chỉ dùng 72 byte đầu: cấm > 72 byte ở validator **và** ở bộ băm | `constants.ts:71-81`, `auth/utils.ts:642-647` | Ca byte-vs-ký-tự; Go: `x/crypto/bcrypt` mới trả `ErrPasswordTooLong` khi > 72 byte (kiến thức ngoài source, kiểm bằng test). `Compare` với mật khẩu > 72 byte phải trả "không khớp", không cắt âm thầm (Node cắt, nên khác) |
| S9 | Tương thích hash cũ: Go phải xác minh được hash `$2b$` do Node tạo | không có trong source (kiến thức ngoài) | Test với 1 hash mẫu tạo từ Medplum |
| S10 | Pwned: **đóng khi lỗi** (fail-closed). `newuser`: lỗi mạng thành `400` kèm thông báo lỗi; `setpassword`/`changepassword`: lỗi thành `500`; không có bước nào cho qua khi HIBP không trả lời | `newuser.ts:121-123,129`, `setpassword.ts:89`, `app.ts:189-190` | Test: `Pwned` giả trả lỗi thì không tạo user, không đổi mật khẩu, không tiêu thụ liên kết (vì pwned chạy trước tiêu thụ). Timeout của Medplum chưa xác nhận; đề xuất `PWNED_TIMEOUT=5s` |
| S11 | Không đăng nhập mật khẩu sau khi nó bị thay giữa lúc kiểm tra (đua) | `oauth/utils.ts:221-236` | Thuộc P2; P4 cung cấp `revokeUserLogins` |
| S12 | Không dò email ở `newuser` bị giảm nhẹ bằng hạn mức `login` 5/phút/IP; thông báo `Email already registered` là cố ý (người dùng cần biết) | `newuser.ts:91-94`, `ratelimit.ts:41-47` | Ghi vào hợp đồng; test hạn mức theo IP ở việc hạn mức riêng |
| S13 | `secret` yêu cầu lưu **thô** trong `UserSecurityRequest` (`resetpassword.ts:126`); kẻ đọc được DB dùng được liên kết còn hiệu lực. `Login.code` và `refreshSecret` cũng thô | đã đọc | Giữ (D2: kho riêng, cấm lộ qua `/fhir/R4`); đề xuất lệch tùy chọn: lưu SHA-256 của secret (câu hỏi Q3) |
| S14 | Không log email thô: Medplum ghi email ở `info` (`newuser.ts:134,148`) | đã đọc | Go: log `userId`, không log email, mật khẩu, secret |
| S15 | Thư xác minh và đặt lại chỉ chứa liên kết; không chứa mật khẩu | `resetpassword.ts:80-93` | Test nội dung thư |
| S16 | Tạo user, kiểm tra trùng email có đua (không có ràng buộc duy nhất ở Medplum: kiểm tra rồi tạo, `newuser.ts:85-97`) | đã đọc | Go: bọc "kiểm tra trùng rồi tạo" trong `UnitOfWork` với `Locker.Lock("user-email:<project hoặc global>:<email>")` (`LockKey` đã có). Test đua: 2 đăng ký song song cùng email, đúng 1 thành công |
| S17 | `readUserForAccountManagement`: `changepassword` và `/auth/mfa/*` không dùng được với On-Behalf-Of, `ClientApplication`, scope SMART | `utils.ts:69-104` | Ca `mfa.test.ts:161-218`, `changepassword.test.ts:79` |

## P4.8 Phần Medplum có nhưng không port đợt này, điểm lệch, rủi ro, câu hỏi mở

### Không port

| Mục | Lý do |
|---|---|
| Project SMTP (`email/utils.ts:31-122`, `getProjectSmtpSocket`, `Project.secret[smtp*]`) | Cần chống SSRF và tách cấu hình theo project; cổng `Mailer` đủ cho đợt này. Bỏ ca `resetpassword.test.ts:190` |
| Gửi thư qua AWS SES, MailComposer, xử lý tệp đính kèm (`email/email.ts:71-120`) | Không dùng ở P4/P5; cổng `Mailer` chỉ nhận văn bản thuần |
| `isPractitionerEmailDomainAllowed`, `blockedEmailDomains`, `allowedPractitionerEmailDomain` | Chỉ dùng ở `admin/invite.ts:112` (P7) |
| `retireAllSecurityRequests` | Không thấy nơi gọi trong `auth/` |
| `/admin/*` (mời user, `resetpassword` kiểu `invite`, đặt lại MFA của user khác ở `admin/project.ts:225-250`) | D3 |
| Ca GraphQL, ca "Default ClientApplication is restricted to project" | Không thuộc auth; chuyển sang kiểm thử tích hợp với `fhir-server` |
| Chuẩn hóa tracing (`buildTracingExtension` trong `sendOutcome`) | Quan sát đã có ở `echo4obs` |
| `externalId`/đăng nhập ngoài trong `tryLogin` | P6 |

### Lệch có chủ đích (đề xuất, cần duyệt)

| # | Lệch | Lý do | Hậu quả |
|---|---|---|---|
| L1 | `newuser`: kiểm tra `validateLoginRequest`, `validatePkce`, `clientId`/`redirectUri` **trước** khi tạo user | Source tạo user rồi `tryLogin` mới báo lỗi PKCE (`newuser.ts:97-113`), để lại user mồ côi và lần sau gặp `Email already registered` | Thứ tự lỗi khác nhẹ; mọi thông báo giữ nguyên |
| L2 | `resetpassword`: gửi thư không đồng bộ hoặc nuốt lỗi gửi, luôn trả 200 | Chống phân biệt user qua 500/độ trễ (S2) | Mất khả năng báo lỗi gửi cho người gọi; ghi log và metric |
| L3 | `newproject`/`newpatient`: kiểm tra `login.revoked`, `login.granted`, và (P5) MFA chưa xác minh **trước** khi tạo tài nguyên | Source tạo `Project`, hồ sơ, membership xong mới để `setLoginMembership` ném `Login revoked`/`Login granted` (`newproject.ts:84-86`, `newpatient.ts:49-52`, `oauth/utils.ts:514-520`). Người có mật khẩu nhưng chưa qua MFA gọi được `newproject` bằng `login` id: tạo được project (không lấy được `code`, vì `sendLoginResult` trả `mfaRequired`), và login bị revoke vẫn để lại project mồ côi | Thêm 2 mã lỗi dùng lại thông báo sẵn có (`Login revoked`, `Login granted`) |
| L4 | `UserSecurityRequest.secret` lưu băm | S13 | Khác Medplum; hiển thị `secret` trong thư không đổi; chỉ đổi cách so sánh. Cần quyết định (Q3) |
| L5 | `Mailer` lỗi ở `newuser` không làm hỏng đăng ký (ghi log, vẫn 200 với `emailVerificationRequired: true`) | Source: lỗi gửi thành 400 sau khi đã tạo user và login (`newuser.ts:117-123`) | Người dùng có thể xin gửi lại ở `newproject` (đã có đường này, `newproject.ts:63-74`) |
| L6 | Chuẩn hóa `APP_BASE_URL` có `/` cuối | `welcomeemail.ts:46,54` nối thẳng | không |

### Điểm lệch chấp nhận được

- Lỗi validator: một lỗi mỗi trường (mục 0.4).
- Chuyển hướng: giữ dạng `200` + `Location` như source, không đổi thành 302.
- `meta.author` của `ClientApplication`: giá trị ở Medplum là `repo.getConfig().author` (chưa xác nhận); trong Go đặt cố định `auth` hoặc bỏ.

### Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Chính sách truy cập mặc định (`default-access-policies.ts`, hơn 150 dòng, dựa `getResourceTypes()`) dễ lệch khi Medplum đổi | Test so khớp JSON với bản dựng từ Medplum (chạy một lần, lưu `testdata`); ghi vào `docs/UPSTREAM.md` |
| `Store.Mutate` (thay `patchResource`) sai thì mất ghi đồng thời | Test đua, kiểm ở P1 trước khi P4 bắt đầu |
| Hạn mức theo IP ở 0.3 chưa có trong repo | Việc riêng; P4 chỉ khai báo bảng và gắn thẻ route |
| `validator.js` `isEmail` và `isUUID` khác bộ kiểm tra Go | Test bảng + property test (mục 0.4) |

### Câu hỏi mở

| # | Câu hỏi | Đề xuất |
|---|---|---|
| Q1 | Giữ thương hiệu "Medplum" trong thư, hay đổi thành tên Trivita (cần `APP_NAME` trong mẫu) | Mẫu có tham số `AppName`, mặc định `Trivita` |
| Q2 | Kênh gửi thật (SMTP, SES, nhà cung cấp khác)? | Chốt D4 khi có nhà cung cấp; P4 chỉ cần `log` và `fake` |
| Q3 | Lưu `secret` băm (L4)? Ảnh hưởng: mọi công cụ đọc `UserSecurityRequest` | Có, vì kho `auth` tách riêng (D2) |
| Q4 | Có bắt buộc `PWNED` ở môi trường ngoài `production` (mạng bị chặn) | Cho phép `PWNED_ENABLED=false` chỉ ở `local`/`test` |
| Q5 | Chấp nhận L1, L2, L3, L5? | Có |

Chỉnh `auth-dto.md` sau P4: (1) `newuser` cần ghi tên trường `recaptchaToken` (hiện ghi "token reCAPTCHA"); (2) bổ sung lỗi `Recaptcha token is required`, `Recaptcha failed`, `Invalid recaptchaSiteKey`, `Invalid recaptchaSecretKey`, `Project not active`, `Project does not allow open registration`, `Password found in breach database` (kèm `expression: ["password"]`); (3) `newproject`: thêm mô tả thứ tự kiểm tra ở P4.4 B; (4) `setpassword`: lỗi `Invalid request type` đã có; `verifyemail`: `Invalid user security request type` đã có, giữ.

## P4.10 Ước lượng và các bước

Cỡ chung P4: **M, phần trên của khoảng (4 đến 5 ngày)**, vì cần dựng chính sách truy cập mặc định (bước 4.5) và `Store.Mutate` (P1). Mỗi bước là một commit review được.

| Bước | Việc | Cỡ | Tiêu chí hoàn thành |
|---|---|---|---|
| 4.1 | `domain`: `password`, `securityrequest`, `registration`; hằng thời hạn; kiểm tra dạng email và UUID | S | Test bảng khớp mục P4.6 (`securityrequest`, `utils`); test kiến trúc xanh; không import ngoài `pkg/fhir` và stdlib |
| 4.2 | Cổng và adapter: `Clock`, `Random`, `PasswordHasher` (bcrypt, chặn 72 byte, test với hash `$2b$` mẫu), `Pwned` (HIBP + fake, có timeout), `Recaptcha`, `Mailer` (`log`, `fake`), cấu hình mục P4.5 | M | Test adapter không gọi mạng thật (server `httptest`); `go vet`, `-race` xanh |
| 4.3 | `credentials`: `SecurityRequests` (issue, supersede, consume, retire), `VerifyEmail`, `ResetPassword`, `SetPassword`, `RevokeUserLogins`, `ChangePassword`; handler, DTO và validator 4 route | M | Mọi ca ở bảng `resetpassword`, `setpassword`, `verifyemail`, `changepassword`, `securityrequest` xanh (trừ ca đã đánh Bỏ, ca đợi P2/P3); test đua S4; test S1, S2 |
| 4.4 | `registration.NewUser` + middleware reCAPTCHA (mức máy chủ và theo `Project.site`) + `SendVerificationEmail` | M | Mọi ca `newuser`; test đua S16; test thứ tự lỗi (reCAPTCHA trước `Registration is disabled`) |
| 4.5 | `accesspolicy` mặc định + `ProjectInit` (6 chính sách, project, client, hồ sơ, membership trong một transaction) + `NewProject` + thư chào + `Seeder.RegisterNew` + lệnh `auth seed` | M (phần lớn là chính sách mặc định) | Mọi ca `newproject` (trừ Bỏ); test cuộn lại khi lỗi giữa chừng không để lại project; test JSON chính sách khớp Medplum; bảng hạn mức IP (0.3) khai báo ở route |
| 4.6 | `NewPatient` | S | Ba ca `newpatient`; ca L3 |
| 4.7 | Test bảo mật tổng (S1 đến S17), E2E: `newuser` → `login` → `newproject` → `code` → `/oauth2/token` (cần P3) → `fhir-server`; cập nhật `auth-dto.md` | S | Bảng S đủ test; E2E xanh; swagger khớp hợp đồng |

Thứ tự: 4.1 → 4.2 → 4.3 → 4.4 → 4.5 → 4.6 → 4.7; 4.3 và 4.4 độc lập nhau sau 4.2; 4.5 cần 4.2.

### Tiến độ P4 (cập nhật 2026-11-22)

**Xong bước 4.3** (`resetpassword`, `setpassword`, `verifyemail`, `changepassword`) và phần cần của 4.2 (`Mailer`, config thư):
- Mã: `application/credentials.go` (`IssueSecurityRequest`, `ResetPassword`, `SetPasswordWithRequest`, `VerifyEmail`, `SendVerificationEmail`, `ChangePassword`), `application/mail.go` (cổng `Mailer`, mẫu thư `Trivita`), `infrastructure/mailer` (`Log`, `None`, `Fake`), `presentation/httpapi/credentials.go`, `domain.HashSecurityRequestSecret`. `SetPassword`, vòng đời `UserSecurityRequest` và `RevokeUserLogins` đã có từ P1.
- Cấu hình: `AUTH_MAIL_PROVIDER` (`log` ở local/development/test, `none` ở staging/production; `log` bị cấm ở staging/production vì ghi link có secret ra log), `AUTH_MAIL_APP_NAME`. Link dựng từ `AUTH_APP_BASE_URL`; không có thì không phát request và không gửi thư.
- Quyết định áp dụng: Q3 (secret lưu băm SHA-256; secret gốc chỉ có trong thư), L2 (lỗi gửi thư của reset bị nuốt), L5 một phần (lỗi gửi thư xác minh được trả cho người gọi để `newuser` quyết định ở bước 4.4).
- Điểm lệch thêm, chưa có trong kế hoạch: `sendEmail=false` ở route công khai không phát request (secret chỉ nằm trong thư nên phát mà không gửi chỉ làm mất link đang dùng); `verifyemail` kiểm `redirectUri` TRƯỚC khi tiêu thụ (Medplum ném lỗi sau khi đã tiêu thụ); tìm user ở `resetpassword` đúng phạm vi (không rơi về user toàn cục như đăng nhập).
- Test: application (memory và SQLite, PostgreSQL qua `make test-auth-pg`), có test đua cho `setpassword` và `verifyemail` (đúng một lần thành công), HTTP ở `bootstrap/credentials_http_test.go`, config, mailer. 35 đột biến trên mã mới đều bị bắt sau khi thêm 4 test (lần đầu sống 2 ca: tìm user sai phạm vi, nội dung thư xác minh).
- **Chưa làm:** reCAPTCHA cho `resetpassword` (đi cùng 4.4; hiện route chưa có chặn bot nào ngoài hạn mức chung của `/auth/*`); hạn mức riêng theo email; kênh gửi thư thật (D4: ở production hiện không gửi được thư); `On-Behalf-Of` ở `changepassword` (chưa có khái niệm này trong `auth`).
- Làm thêm: sửa `Makefile` (biến `SWAG_DIRS_authen` còn sót từ lần đổi tên nên `make swagger APP=auth` lỗi), sinh lại `apps/auth/docs`.

**Xong bước 4.4 (`newuser` + reCAPTCHA), 2026-11-22:**
- Mã: `application/registration.go` (`NewUser`, `RegistrationConfig`), `application/recaptcha.go` (cổng `Recaptcha`, `ValidateRecaptcha`), `infrastructure/recaptcha` (siteverify, secret trong body POST, không theo chuyển hướng, mọi lỗi là lỗi: fail đóng), `domain/registration.go` (`OpenRegistrationPolicy`, `ProjectRegistrationAllowed`), `presentation/httpapi/registration.go`; reCAPTCHA cũng gắn vào `resetpassword` (không kiểm project). `TryLogin` tách `checkLoginRequest` để `newuser` kiểm TRƯỚC khi tạo user (L1).
- S16 (đua trùng email): "kiểm tra trùng rồi tạo" nằm trong transaction với khóa theo (phạm vi, email) (`TxRepos.Locker` qua `KeyLocker` của repository; PostgreSQL dùng advisory lock). Bộ nhớ và SQLite tự tuần tự hóa nên **chỉ test trên PostgreSQL mới bắt được khi bỏ khóa**: đã chạy đột biến "bỏ khóa" trên PostgreSQL thật và bị bắt. Áp cho cả `CreateUser` của CLI.
- Cấu hình mới: `AUTH_RECAPTCHA_SITE_KEY`, `AUTH_RECAPTCHA_SECRET_KEY`, `AUTH_RECAPTCHA_TIMEOUT` (3s), `AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION`; dùng lại `AUTH_REGISTER_ENABLED`, `AUTH_NEW_PROJECT_REGISTRATION_ENABLED`.
- Lệch thêm: lỗi gọi reCAPTCHA/HIBP là 500 (Medplum: 400 kèm message lỗi); user chỉ gắn project khi body nêu `projectId` (chỉ `clientId` thì user toàn cục), đúng Medplum.

**Xong bước 4.5 (`newproject` + thư chào), 2026-11-22:**
- Phần lõi (`CreateProject`: 6 chính sách, project, client mặc định, hồ sơ, membership admin) đã có từ bước CLI; bước này thêm `application/newproject.go` (`NewProject`), thư chào, route `POST /auth/newproject` (hạn mức `login`, đặt cookie nếu login thuộc client), `ProjectRepository.HasOwnedBy`.
- L3 đã cài: kiểm `Login revoked`, `Login granted`, đã có membership, project khác TRƯỚC khi tạo project; login thiếu MFA nhận `mfaRequired`/`mfaEnrollRequired` mà không tạo gì (nhánh cần đăng ký MFA vẫn fail đóng do P5 chưa có). Test xác nhận không để lại project hay chính sách mồ côi.
- Thư chào: chỉ gửi cho chủ project lần đầu, lỗi gửi chỉ ghi log; dùng tên `Trivita` và BỎ các link quảng bá riêng của Medplum (tài liệu, Discord, agent AI); thêm `AUTH_SUPPORT_EMAIL` (tùy chọn). Dòng "your project" khi project không tên không còn (tên luôn 4 đến 72 ký tự).
- **Lỗi thật tìm được nhờ đột biến:** `CreateProject` kiểm độ dài tên bằng BYTE, nên tên tiếng Việt 72 ký tự (216 byte) bị từ chối (cả `auth seed project`). Đã sửa sang đếm ký tự, có test.
- Đua cùng một `login` gọi `newproject` song song vẫn có thể tạo hai project (một cái thắng ở `SetLoginMembership`, cái kia thành project mồ côi do chính người đó sở hữu): giống Medplum, chưa xử lý vì cần đưa `Login` vào transaction; ghi nhận là giới hạn đã biết.
- Đột biến: 38 ca ở 4.4 và 38 ca ở 4.5 (chưa kể các lần chạy lại): đều bị bắt trừ 3 ca tương đương (email hạ chữ thường ở `NewUser` do `PrepareUser` đã làm lại; `checkProject` ở handler `newuser` do `NewUser` kiểm lại; kiểm độ dài tên ở handler HTTP do use case kiểm lại cùng thông báo).

**Xong bước 4.6 (`newpatient`), 2026-11-22:**
- Mã: `application/newpatient.go` (`NewPatient`), route `POST /auth/newpatient` (không hạn mức riêng như Medplum; đặt cookie nếu login thuộc client).
- Thứ tự: đọc login và kiểm trạng thái (membership, project, revoked, granted), kiểm user thuộc ĐÚNG project, nhánh MFA, kiểm project hoạt động và cho đăng ký mở, đọc chính sách; chỉ sau đó mới tạo Patient và membership (một transaction) rồi `SetLoginMembership`. Test xác nhận mọi trường hợp bị từ chối không để lại Patient hay membership.
- Ca đối chiếu: chính sách Patient trong `defaultAccessPolicies`, rơi về `defaultPatientAccessPolicy` khi không có mục Patient (`newpatient.test.ts:180`), project chưa mở (`:231`), dùng lại login, thiếu `login`. Phần quyền đọc dữ liệu của bệnh nhân (`newpatient.test.ts` res8 đến res12) cần `fhir-server` và `fhirauth` (task #16): thuộc e2e 4.7.
- Đột biến: 22 ca, đều bị bắt (một ca đầu trúng nhầm handler của `newproject`, đã viết lại và thêm test cookie riêng).

**Xong bước 4.7 (bộ kiểm soát bảo mật và e2e), 2026-11-22 — P4 đóng.** Đối chiếu S1 đến S17 của mục P4.7 với test (mọi ô đều có test; ô đánh * chỉ chạy được đầy đủ trên PostgreSQL):

| # | Test |
|---|---|
| S1 | `bootstrap/credentials_http_test.go` `TestHTTPResetPasswordResponsesAreIdenticalForUnknownEmail` (cùng mã và body); e2e `TestResetPasswordThroughRealProcesses` |
| S2 | `application/credentials_test.go` `TestResetPasswordDoesNotRevealAccounts` (lỗi gửi thư bị nuốt) |
| S3 | `application/security_p4_test.go` `TestSecretsAreNeverComparedWithEqualityOperators` (kiểm tĩnh bằng AST: cấm `==`/`!=` trên định danh có chữ "secret", trừ so với `""`/`nil`) |
| S4 | `TestSetPasswordWithRequestRace`, `TestVerifyEmailRace` (đúng một lần thành công) |
| S5 | `application/password_test.go` `TestSetPasswordRejectsBeforeConsuming`; lỗi áp mật khẩu cuộn lại việc tiêu thụ: P1 (`SetPassword` trong một transaction) |
| S6 | `infrastructure/store` test phân trang (`pageSize` = 4) cho `ListIDsByUser`; `TestRevokeUserLogins` |
| S7 | `application/securityrequest_test.go` (vô hiệu request cùng loại, giữ `verify-email`) |
| S8 | `infrastructure/password` `TestCompareNeverMatchesPasswordsOverTheByteLimit`; `TestChangePasswordRejectsOldPasswordOverTheByteLimit` |
| S9 | `infrastructure/password` `TestCompatibleWithNodeStyleHash` |
| S10 | `TestNewUserRejections` (HIBP lỗi), `TestChangePasswordFailsClosedWhenPwnedCheckFails`, `TestSetPasswordRejectsBeforeConsuming` |
| S11 | P2 (`TryLogin` đọc lại user sau khi tạo Login) |
| S12 | `TestHTTPNewUserSharesLoginRateLimit`, `TestHTTPNewProjectSharesLoginRateLimit` |
| S13 | `TestResetPasswordSendsLinkAndStoresOnlyHash` (lưu băm SHA-256) |
| S14 | `TestP4FlowsNeverLogEmailsPasswordsOrSecrets` (log JSON ở mức debug trên mọi luồng P4, kể cả đường lỗi) |
| S15 | `TestMailsContainOnlyTheLink` (đúng một liên kết, không mật khẩu, không băm) |
| S16 | `TestNewUserRaceSameEmail`* (khóa theo (phạm vi, email); đã chạy đột biến "bỏ khóa" trên PostgreSQL và bị bắt) |
| S17 | `TestChangePasswordGuards` (ClientApplication, scope SMART, user toàn cục); phần `/auth/mfa/*` thuộc P5 |

E2E mới ở `e2e/signup_test.go` (`make test-e2e`, tiến trình thật): chuỗi `newuser` → `newproject` → đổi code → gọi `fhir-server` (project mới tách biệt khỏi Alpha, code dùng một lần) và chuỗi quên mật khẩu (link đọc từ log của provider `log`, setpassword thu hồi refresh token cũ, mật khẩu cũ hết dùng).

**Hai lỗi thật tìm được ở bước này (đã sửa, có test và đột biến):**
1. `Bcrypt.Compare` cắt âm thầm ở 72 byte nên "mật khẩu đúng 72 byte + đuôi bất kỳ" khớp; `changepassword` chỉ kiểm `oldPassword` không rỗng nên bị ảnh hưởng (S8). Nay mật khẩu quá 72 byte không bao giờ khớp.
2. (P3) Đường `authorization_code` tra client thẳng từ kho nên client dựng sẵn như `medplum-cli` luôn nhận "Invalid client" khi đổi code; chuỗi đăng ký không dùng được với client dựng sẵn. Nay dùng `GetClientApplication` (tra client dựng sẵn trước). `AuthenticateClient` và client assertion vẫn chỉ tra kho: client dựng sẵn không có membership nên không dùng được cho `client_credentials`.
Lưu ý khi đăng ký bằng client công khai: phải gửi `codeChallenge` ngay từ `newuser` (nếu không, đổi code báo "Missing verification context").

**Còn lại của P4 (không chặn):** kênh gửi thư thật (D4), hạn mức riêng theo email, đua `newproject` cùng một login, `On-Behalf-Of` ở `changepassword`. Chỉnh `auth-dto.md` theo mục P4.8 đã làm ở từng bước.

---

# P5 — MFA

## P5.1 Mục tiêu và phạm vi

Port `auth/mfa.ts`, `auth/mfalimit.ts`, phần MFA của `auth/utils.ts` và `oauth/utils.ts`:

| Route | Xác thực | Handler (`auth/mfa.ts`) |
|---|---|---|
| `GET /auth/mfa/status` | Bearer | dòng 80-103 |
| `POST /auth/mfa/login-enroll` | công khai (theo `login` id), hạn mức `mfa` 10/phút | 105-160 |
| `POST /auth/mfa/enroll` | Bearer | 162-227 |
| `POST /auth/mfa/verify` | công khai (theo `login` id), hạn mức `mfa` 10/phút | 229-248 |
| `POST /auth/mfa/send-email` | công khai (theo `login` id) | 250-271 |
| `POST /auth/mfa/send-email-challenge` | Bearer | 282-295 |
| `POST /auth/mfa/disable` | Bearer | 297-373 |

Hàm cần port: `verifyConnectedFactor` (`mfa.ts:57-78`), `parseMfaMethod` (43-45), `verifyMfaToken` (`oauth/utils.ts:347-413`), `isMfaRequired`, `getAllowedMfaMethods`, `getEnrolledMfaMethods`, `addMfaMethod`, `buildTotpEnrollment`, `getTotpEnrollmentSecret`, `clearTotpEnrollmentSecret`, `enrollTotp`, `sendMfaEmailCode`, `verifyEmailMfaCode`, `clearMfaEmailCode`, `getLoginProject` (`auth/utils.ts:114-124,205-208,229-335,346-395,455-465`), toàn bộ `mfalimit.ts`, và **hai nhánh MFA của `sendLoginResult`** (`auth/utils.ts:479-502`; hàm này thuộc P2, P5 cắm vào qua cổng `MfaPolicy`).

Không có route MFA nào cấp `code` trực tiếp: `code` chỉ lộ qua `sendLoginResult`. **`oauth/token.ts` không kiểm tra `mfaVerified`** (grep `mfa` trong file chỉ ra hai dòng chú thích ở dòng 478-481, không có mã kiểm tra; `oauth/authorize.ts:138` chỉ sao chép `mfaVerified` sang login mới). Toàn bộ việc ép MFA dựa vào chỗ duy nhất là `sendLoginResult` không trả `code` khi cần MFA. Đề xuất phòng thủ chiều sâu ở P3: khi đổi `code`, từ chối nếu `Login.authMethod=password` và (người dùng đã đăng ký MFA hoặc MFA bắt buộc) mà `!mfaVerified` (câu hỏi Q8).

## P5.2 Bảng ánh xạ source sang Go

### Domain (`domain/mfa`, thuần)

| Medplum | Hành vi | Go |
|---|---|---|
| `auth/utils.ts:44` | `MfaMethod = totp | email` | `mfa.Method` |
| `auth/utils.ts:114-124` | `getAllowedMfaMethods`: đọc `Project.setting[allowedMfaMethods].valueString` (danh sách phân cách dấu phẩy, bỏ khoảng trắng, chỉ giữ `totp`/`email`); thiếu hoặc rỗng hoặc không hợp lệ: `["totp"]` | `mfa.AllowedMethods(project)` |
| `auth/utils.ts:205-208` | `isMfaRequired`: `setting[mfaRequired].valueBoolean` **hoặc** `User.mfaRequired`; project chỉ siết, không nới | `mfa.IsRequired(user, project)` |
| `auth/utils.ts:319-324` | `getEnrolledMfaMethods`: `User.mfaMethod` nếu không rỗng, ngược lại `["totp"]` nếu `mfaEnrolled`, ngược lại `[]` | `mfa.EnrolledMethods(user)` |
| `auth/utils.ts:297-303` | `addMfaMethod`: hợp của phương thức hiện có và phương thức mới | `mfa.AddMethod(user, m)` |
| `auth/mfa.ts:43-45` | `parseMfaMethod`: chỉ `"email"` thành email, mọi giá trị khác (kể cả thiếu) thành `totp` | `mfa.ParseMethod(any)` |
| `auth/mfalimit.ts:152-169` | `assertMfaLoginActive`: kiểm tra theo thứ tự `revoked`, `granted`, `mfaVerified`, `authMethod`, `authTime` | `mfa.AssertLoginActive(login, now)` |
| `constants.ts:37-53` | Hằng số: mã email 20 phút; offer TOTP 20 phút; login MFA 10 phút; 5 lần mỗi login; 20 lần mỗi user | `mfa/const.go` |
| `auth/utils.ts:305-310` | Khóa offer: `medplum:mfa:totp-enroll:<userId thường>:<loginId thường>` | `mfa.OfferKey(userID, loginID)` (đề xuất đổi tiền tố thành `auth:`) |
| `auth/utils.ts:365-376` | Mẫu thư mã xác minh | `application/mailtext` |

### Application (`application/mfa`)

| Medplum | Go |
|---|---|
| `mfa.ts` các route | `mfa.Service`: `Status`, `LoginEnroll`, `Enroll`, `Verify`, `SendEmail`, `SendEmailChallenge`, `Disable` |
| `oauth/utils.ts:347-413` `verifyMfaToken` | `mfa.Service.VerifyToken(login, token, enrollSecret)` (lõi của `Verify` và `LoginEnroll`) |
| `mfa.ts:57-78` `verifyConnectedFactor` | `mfa.Service.VerifyConnectedFactor` |
| `auth/utils.ts:229-248` `buildTotpEnrollment` | `mfa.Service.BuildEnrollment(login, user, project)` |
| `auth/utils.ts:256-289` | `Service.OfferedSecret`, `ClearOffer`, `EnrollTotp` |
| `auth/utils.ts:346-395` | `Service.SendEmailCode`, `VerifyEmailCode`, `ClearEmailCode` |
| `auth/utils.ts:479-502` | Cổng `MfaPolicy` do P2 khai báo: `EnrollmentRequired(user, project, login)`, `Challenge(login, user, project) (methods, error)` (gồm tự gửi mã khi email là phương thức duy nhất và `login.emailMfa` rỗng); P5 là bên cài |
| `mfalimit.ts:61-91` | Cổng `AttemptLimiter`: `Reserve(login) (attempts int, error)`, `Release(login)` |
| `mfalimit.ts:102-120` | Cổng `TOTPReplayGuard`: `Claim(userID, token) bool` (đặt `NX`, hết hạn 4 bước) |
| `auth/utils.ts:93-104` | `application/credentials.AccountGuard` (từ P4) |

### Infrastructure

| Cổng | Adapter |
|---|---|
| `TOTP` (`GenerateSecret`, `KeyURI`, `Verify(secret, token, now, window)`) | Dựa `github.com/pquerna/otp/totp` (đề xuất; chưa có trong `go.mod`) |
| `QR` (`DataURL(text)`) | `github.com/skip2/go-qrcode` hoặc `boombuler/barcode` (đi kèm pquerna); xuất PNG, mã hóa base64 dạng `data:image/png;base64,...` (định dạng của thư viện `qrcode` Node: chưa xác nhận vì không có `node_modules`) |
| `OfferStore` (`GetOrCreate(key, candidate, ttl) string`, `Get`, `Delete`) | Redis; Medplum dùng một lệnh `SET key v PX ttl NX GET` (`auth/utils.ts:236-244`); go-redis hỗ trợ `SetArgs{Mode:"NX", Get:true}`; nếu miniredis không hỗ trợ thì thay bằng `SETNX` rồi `GET`, hoặc script Lua |
| `AttemptLimiter`, `TOTPReplayGuard` | Redis + Lua (xem P5.9) |
| `Mailer` | như P4 |

## P5.3 Mô hình dữ liệu

| Resource | Trường đọc | Trường ghi |
|---|---|---|
| `User` | `mfaEnrolled`, `mfaMethod[]`, `mfaSecret`, `mfaRequired`, `email`, `emailVerified`, `project` | `mfaSecret`, `mfaEnrolled`, `mfaMethod` (`enrollTotp`, `login-enroll` email, `enroll`, `disable`); `emailVerified=true` (xác minh mã email, vá riêng); `mfaSecret` bị **thay bằng secret ngẫu nhiên mới** khi gỡ TOTP (`mfa.ts:357-366`) dù đã hết đăng ký |
| `Login` | `revoked`, `granted`, `mfaVerified`, `authMethod`, `authTime`, `emailMfa{codeHash,expiresAt}`, `user`, `project` | `mfaVerified=true` (vá); `emailMfa` đặt khi gửi mã, gỡ khi xác minh thành công hoặc `disable`/`enroll`; `revoked=true` ở lần sai thứ 5 |
| `Project` | `setting[allowedMfaMethods]`, `setting[mfaRequired]`, `setting[appName]` | không |

Trạng thái kho phụ (Redis), tách khỏi tài nguyên FHIR:

| Khóa (Medplum) | Giá trị | TTL | Dùng cho |
|---|---|---|---|
| `medplum:mfa:totp-enroll:<userId>:<loginId>` (cache Redis) | secret TOTP được mời | 20 phút (`TOTP_ENROLLMENT_EXPIRATION_MS`) | `buildTotpEnrollment`, `getTotpEnrollmentSecret` |
| `medplum:mfa:{<userId>}:login:<loginId>` (Redis hạn mức) | số lần thử của login | 10 phút kể từ lần tăng đầu tiên | `mfalimit` |
| `medplum:mfa:{<userId>}:user` | số lần thử của user (qua mọi login) | 10 phút kể từ lần tăng đầu tiên | `mfalimit` |
| `medplum:mfa:{<userId>}:totp:<token>` | `1` | 4 bước TOTP = 120 giây khi `window=1` | chống dùng lại mã |

Vòng đời mã email (`Login.emailMfa`): mã 6 chữ số sinh bằng `randomInt(0, 1_000_000)` rồi đệm số 0 (`auth/utils.ts:352`); lưu bcrypt của mã (cost cấu hình) cùng `expiresAt = now + 20 phút`; gửi lại **ghi đè** mã cũ; xóa khi dùng thành công (`verifyMfaToken` gỡ `/emailMfa`; `enroll` và `disable` gọi `clearMfaEmailCode`).

Tương thích ngược (ca `mfa.test.ts:2527-2862`): người dùng cũ có `mfaEnrolled=true` và `mfaSecret` mà không có `mfaMethod` được coi là `["totp"]`; `login-enroll` ghi bổ sung `mfaMethod=["totp"]`.

## P5.4 Thuật toán từng bước

### A. `buildTotpEnrollment(login, user, project)` (`auth/utils.ts:229-248`)

1. `offered = secret ngẫu nhiên mới` (`authenticator.generateSecret()`; độ dài mặc định của otplib 12.0.1 chưa xác nhận từ source; kiểm tra ca `mfa.test.ts:2009` yêu cầu khớp `^[A-Z2-7]+$`, tức base32. Go đề xuất 20 byte).
2. `mfaSecret = SET key offered PX 20 phút NX GET`; nếu khóa đã có thì lấy giá trị cũ, ngược lại dùng `offered`. Hệ quả: mỗi **cặp (user, login)** có secret riêng và URL ổn định trong 20 phút (`mfa.test.ts:332-460`: hai lần gọi `/status` cho cùng `enrollUri`; `2216-2277`: mỗi login, mỗi phiên một secret khác nhau).
3. `issuer = (appName của project) ?? "medplum.com"`, bỏ mọi ký tự `:` (`replaceAll(':', '')`) vì ứng dụng xác thực tách nhãn ở dấu `:` đầu tiên (ca `mfa.test.ts:2965-2988`: `Acme: Health` thành `Acme Health`). `appName` lấy từ `Project.setting[appName].valueString` đã `trim`, rỗng thì coi như không có (`email/utils.ts:172-174`).
4. `enrollUri = keyuri(user.email ?? user.id, issuer, mfaSecret)` dạng `otpauth://totp/<issuer>:<tài khoản>?secret=<secret>&issuer=<issuer>`; test chỉ khẳng định `issuer` trong query và nhãn `"<issuer>:<email>"` sau khi giải mã đường dẫn (`mfa.test.ts:91-98,2863-3016`); các tham số khác (`algorithm`, `digits`, `period`) chưa xác nhận.
5. `enrollQrCode = QR data URL của enrollUri`.
6. Trả `{enrollUri, enrollQrCode}`. Secret **chưa** thành `User.mfaSecret`; chỉ trở thành khi chính login đó xác nhận bằng mã (`enrollTotp`).

### B. `verifyMfaToken(login, token, enrollSecret?)` (`oauth/utils.ts:347-413`) — lõi của `verify` và `login-enroll`

1. `assertMfaLoginActive(login)`: theo thứ tự, `revoked` → `400 Login revoked`; `granted` → `400 Login granted`; `mfaVerified` → `400 Login already verified`; `authMethod !== password` → `400 Invalid login authentication method`; `authTime` không hợp lệ hoặc `now - authTime >= 10 phút` → `400 Login expired` (`mfalimit.ts:152-169`; ca `mfa.test.ts:507-530` đặt `authTime = now - 10 phút` thì hết hạn).
2. `emailMfa = enrollSecret ? undefined : login.emailMfa` (mã xác nhận đăng ký TOTP **không** được là mã email). Nếu `emailMfa` có và `expiresAt < now`: `400 MFA code expired` (ca `mfa.test.ts:991-1030`). Quan sát: bước này chạy trước khi tính lượt thử, nên mã email hết hạn còn nằm trên login sẽ chặn cả mã TOTP hợp lệ cho đến khi gửi mã mới (chưa có ca test; câu hỏi Q6).
3. Đọc `User`. `enrollSecret` có và `user.mfaEnrolled`: `400 Already enrolled`.
4. `secret = enrollSecret ?? (TOTP ∈ EnrolledMethods(user) ? user.mfaSecret : none)`. Không có `emailMfa` và không có `secret`: `400 User not enrolled in MFA` (ca `mfa.test.ts:2279-2304`: secret lưu sẵn của user chưa đăng ký bị bỏ qua).
5. `attempts = reserveMfaAttempt(login)` (xem P5.9). Vượt hạn mức: `429` kèm `rate-limit-reset`.
6. Nếu có `emailMfa`: `verified = bcrypt.compare(token, codeHash)`.
7. Nếu chưa `verified` và có `secret`: `verified = verifyTotpToken(user.id, secret, token)` (xác thực và **tiêu thụ** mã, xem P5.9).
8. Không `verified`: nếu `attempts >= 5` thì vá `Login.revoked=true`; ném `400 Invalid MFA token`. (Lần sai thứ 5 thu hồi login; lần gọi thứ 6 gặp `Login revoked` ở bước 1.)
9. Nếu mã email đúng và `!user.emailVerified`: vá `User.emailVerified=true` (nhập mã email chứng minh quyền kiểm soát hộp thư).
10. Nếu `enrollSecret`: `enrollTotp(login, user, secret)`: cập nhật `User` với `mfaSecret`, `mfaEnrolled=true`, `mfaMethod=AddMethod(user,'totp')`, điều kiện `If-Match = user.meta.versionId`; 412 thành `400 Already enrolled`; rồi xóa offer. Đăng ký **trước** khi xác minh login để login thua cuộc đua không bị đánh dấu `mfaVerified` (ca `mfa.test.ts:2186-2214`).
11. Vá `Login.mfaVerified=true` (và gỡ `/emailMfa` nếu mã email đúng). Vá, không ghi đè, để không làm sống lại login vừa bị thu hồi (ca `setpassword.test.ts:404-441`).
12. `releaseMfaAttempt(login)` (lỗi chỉ ghi log). Trả login đã cập nhật.

### C. `POST /auth/mfa/verify` (`mfa.ts:229-248`)

Validator: `login` không rỗng (`Missing login`), `token` không rỗng (`Missing token`).
1. Đọc `Login` (404 nếu không có), đọc `User`.
2. `secret = user.mfaEnrolled ? none : offer(login,user)`; `enrollSecret = secret` chỉ khi `totp ∈ AllowedMethods(project của login)`.
3. `result = verifyMfaToken(login, token, enrollSecret)`; `sendLoginResult(result)`. Hệ quả: người dùng bắt buộc MFA chưa đăng ký mà nhập mã cho **offer của chính login** thì được đăng ký luôn (ca `mfa.test.ts:2134-2160`).

### D. `POST /auth/mfa/login-enroll` (`mfa.ts:105-160`)

Validator: `login` không rỗng (`Missing login`).
1. Đọc `Login`, `User`. `user.mfaEnrolled`: `400 Already enrolled`.
2. `assertMfaLoginActive(login)`.
3. `method = ParseMethod(body.method)`; không thuộc `AllowedMethods(getLoginProject(login))`: `400 MFA method not allowed`. `getLoginProject` trả rỗng khi không có project cụ thể hoặc `Project/new` (khi đó chỉ `totp` được phép).
4. `method === email`: cập nhật `User` (`mfaEnrolled=true`, `mfaMethod=AddMethod(user,'email')`; ghi đè từ bản đã đọc, không `If-Match`), rồi `sendLoginResult(login)`: vì email là phương thức duy nhất và `login.emailMfa` rỗng, hàm tự gửi mã và trả `{login, mfaRequired:true, mfaMethods:["email"], email}`; người dùng phải nhập mã qua `/verify` (đồng thời xác minh email).
5. `method === totp`: `secret = offer(login,user)`, không có: `400 Secret not found`; `body.token` rỗng: `400 Missing token`; `verifyMfaToken(login, token, secret)`; `sendLoginResult(kết quả)`. Mã sai ở đây là `Invalid MFA token`, **không** phải `Invalid token`.
Thứ tự: `Secret not found` đến **trước** `Missing token` (ca `mfa.test.ts:2045-2072` và `2075-2099`).

### E. `POST /auth/mfa/enroll` (Bearer, `mfa.ts:162-227`)

1. `readUserForAccountManagement` (403 như P4 G).
2. `method = ParseMethod(body.method)`.
3. `method ∈ EnrolledMethods(user)`: `400 Already enrolled`.
4. `method ∉ AllowedMethods(ctx.project)`: `400 MFA method not allowed`.
5. `email`: thiếu `token`: `400 Missing token`; `verifyEmailMfaCode(ctx.login, token)` sai (chưa có mã, mã hết hạn, hoặc không khớp bcrypt): `400 Invalid token`; cập nhật `User` (`mfaEnrolled=true`, `mfaMethod`, `emailVerified=true`); `clearMfaEmailCode(ctx.login)`; `200 allOk`.
6. `totp`: `secret = offer(ctx.login, user)`, không có: `400 Secret not found`; thiếu `token`: `400 Missing token`; `authenticator.verify(token, secret, window = cấu hình mfaAuthenticatorWindow ?? 1)` sai: `400 Invalid token`; `enrollTotp(ctx.login, user, secret)`; `200 allOk`. Mã này **không** bị tiêu thụ ("xác nhận bộ xác thực không cấp quyền gì", chú thích `mfa.ts:217`) nên có thể dùng lại để đăng nhập trong cùng cửa sổ.

### F. `GET /auth/mfa/status` (Bearer, `mfa.ts:80-103`)

1. `readUserForAccountManagement`. 2. `enrolled = EnrolledMethods(user)`; `allowed = AllowedMethods(ctx.project)`. 3. Nếu `totp ∉ enrolled`: `BuildEnrollment(ctx.login, user, ctx.project)`. 4. Trả `{enrolled: bool(user.mfaEnrolled), enrolledMethods, allowedMethods, mfaRequired: IsRequired(user, project), email: user.email, enrollUri?, enrollQrCode?}`.

### G. Gửi mã email

`POST /auth/mfa/send-email` (`mfa.ts:250-271`): validator `login` (`Missing login`); đọc `Login`; `assertMfaLoginActive`; đọc `User`; không đăng ký hoặc không có phương thức email: `400 User not enrolled in email MFA`; `sendMfaEmailCode(login, user, project của login)`; `200 allOk`.

`POST /auth/mfa/send-email-challenge` (Bearer, `mfa.ts:282-295`): `readUserForAccountManagement`; nếu email chưa đăng ký **và** project không cho phép email: `400 Email MFA not available`; gửi mã trên `ctx.login`; `200 allOk`. Dùng cho hai việc: xác minh lại email để đăng ký (`enroll`) và thỏa yếu tố email khi `disable`.

`sendMfaEmailCode` (`auth/utils.ts:346-380`): sinh mã, băm, vá `Login.emailMfa={codeHash, expiresAt}`, rồi gửi thư (nội dung P5.5). Gửi lại ghi đè mã cũ. Không có chặn tần suất riêng ngoài hạn mức IP loại `auth`.

### H. `POST /auth/mfa/disable` (Bearer, `mfa.ts:297-373`)

Validator tùy chọn: `token` phải là chuỗi nếu có, `method` phải là chuỗi nếu có (`token: 123` thì 400, ca `mfa.test.ts:1869-1901`).
1. `readUserForAccountManagement`. 2. `!user.mfaEnrolled`: `400 User not enrolled in MFA` (kiểm **trước** validator). 3. Lỗi validator: 400.
4. Nếu `body.method !== undefined`: không thuộc `{totp,email}` thì `400 Invalid method`; không có trong `EnrolledMethods(user)` thì `400 User not enrolled in MFA method`. `remaining = enrolled - method`; không có `method` thì `remaining = []`.
5. `IsRequired(user, project)` và `remaining` rỗng: `400 Cannot remove the last MFA factor because MFA is required` (không phụ thuộc `allowedMfaMethods`; ca `mfa.test.ts:2351-2458`).
6. Thiếu `token`: `400 Missing token`.
7. `verifyConnectedFactor(user, ctx.login, token)` sai: `400 Invalid token`. Hàm này (`mfa.ts:57-78`): token rỗng thì sai; nếu `totp ∈ enrolled` và có `mfaSecret` và `verifyTotpToken` đúng (tiêu thụ mã) thì đúng; nếu `email ∈ enrolled` và `verifyEmailMfaCode` đúng thì đúng.
8. Cập nhật `User`: `mfaEnrolled = remaining.length > 0`, `mfaMethod = remaining`, và nếu TOTP bị gỡ (không có `method` hoặc `method = totp`) thì `mfaSecret = secret ngẫu nhiên mới`.
9. `clearMfaEmailCode(ctx.login)`; `200 allOk`.
Ghi chú: mã email chỉ dùng được một lần (ca `mfa.test.ts:1482-1526`); mã TOTP đã dùng ở `verify` cũng không dùng lại được ở `disable` (ca `461-506`: `Invalid token`).

### I. Hai nhánh của `sendLoginResult` (P2 gọi, P5 cài `MfaPolicy`) — `auth/utils.ts:479-502`

1. Cần đăng ký: `IsRequired(user, project)` **và** `!user.mfaEnrolled` **và** `authMethod=password` **và** `!login.mfaVerified`: trả `{login, mfaEnrollRequired:true, enrollUri, enrollQrCode, allowedMfaMethods}` (offer theo login).
2. Đã đăng ký: `user.mfaEnrolled` và `authMethod=password` và `!mfaVerified`: `mfaMethods = EnrolledMethods(user)`; nếu đúng một phương thức và là `email` và `login.emailMfa` rỗng thì gửi mã ngay (chỉ một lần mỗi login); trả `{login, mfaRequired:true, mfaMethods, email}`.
Ma trận quyết định `project` ↔ `user` (9 ca, `mfa.test.ts:1691-1701`): bắt buộc nếu `user.mfaRequired=true` hoặc `setting[mfaRequired]=true`. Với user có nhiều membership, project chưa cụ thể ở bước mật khẩu nên chỉ yêu cầu của **user** hoạt động ở bước đó; yêu cầu của project áp dụng khi chọn profile (ca `mfa.test.ts:1760-1840`). Người dùng đã đăng ký luôn bị thử thách ngay cả khi `mfaRequired` sai (ca `2460-2505`).

## P5.5 Cổng, thư viện, thư và cấu hình

### Thư viện Go đề xuất

| Việc | Đề xuất | Ghi chú |
|---|---|---|
| TOTP | `github.com/pquerna/otp/totp` | `ValidateCustom` nhận thời điểm (để dùng `Clock`) và `Skew`; `Skew=1` tương ứng `window=1` (hai bên một bước) của otplib (ca `mfa.test.ts:461-506` khẳng định mặc định chấp nhận bước hiện tại và bước kế tiếp). Hành vi của otplib với token không phải 6 chữ số: test cho `INVALID_TOKEN` và `1234567890` trả sai; Go phải trả sai, không panic |
| Dựng URI | tự dựng từ secret có sẵn: `otpauth://totp/<issuer>:<account>?secret=..&issuer=..`, mã hóa phần trăm hai thành phần, giữ `:` | Tránh dùng hàm sinh secret kèm URI của thư viện vì secret ở đây đến từ kho offer |
| QR | `github.com/skip2/go-qrcode` | Mức sửa lỗi và kích thước của Node `qrcode` chưa xác nhận; hợp đồng chỉ nói là data URL |
| Redis | `github.com/redis/go-redis/v9` (đã có), `miniredis` (đã có) | Chưa xác nhận miniredis chạy đủ Lua ở P5.9; nếu không, test hạn mức bằng Redis thật (chỉ khi có `REDIS_TEST_URL`) và fake trong bộ nhớ cài cùng tập ca |
| bcrypt | như P4 | Mã email 6 chữ số: chặn `len > 72` thành "không khớp", không panic |

### Nội dung thư mã MFA (`auth/utils.ts:359-376`)

Chủ đề: `Your <appName> verification code: <code>`; `appName = Project.setting[appName] ?? "Medplum"`; `expirationMinutes = floor(20 phút / 60 000 ms) = 20`.

```
Below is your requested <appName> verification code. You can copy it into the open browser window to confirm your login.

<code>

This code will expire in 20 minutes. If you did not try to sign in, you can safely ignore this email.

Thank you,
The <appName> Team
```

Ca `mfa.test.ts:2893-2962`: chủ đề khớp `^Your Acme Health verification code: \d{6}$`; không chứa `Medplum` khi đã đặt `appName`; mặc định `Your Medplum verification code: ...` và `The Medplum Team`.

### Cấu hình

| Biến | Ý nghĩa | Mặc định | Nguồn |
|---|---|---|---|
| `MFA_AUTHENTICATOR_WINDOW` | số bước TOTP chấp nhận mỗi bên | `1` | `config/types.ts:207`, `mfa.ts:219`, `mfalimit.ts:103` |
| `MFA_LOGIN_ATTEMPT_LIMIT` | lần thử mỗi login | `5` | `constants.ts:52` |
| `MFA_USER_ATTEMPT_LIMIT` | lần thử mỗi user | `20` | `constants.ts:53` |
| `MFA_LOGIN_EXPIRATION` | tuổi tối đa của login chờ MFA và cửa sổ đếm | `10m` | `constants.ts:43` |
| `MFA_EMAIL_CODE_EXPIRATION` | hạn mã email | `20m` | `constants.ts:37` |
| `MFA_TOTP_ENROLLMENT_EXPIRATION` | hạn offer secret | `20m` | `constants.ts:40` |
| `REDIS_URL` | kho offer và hạn mức | bắt buộc ở môi trường thật | hạ tầng |
| `RATE_LIMIT_MFA` | hạn mức IP của `verify`, `login-enroll` | `10` mỗi phút | mục 0.3 |

Phụ thuộc: `User.mfaSecret` và `Login.emailMfa.codeHash` lưu trong kho FHIR của `auth`. Cấm lộ qua `/fhir/R4` (D2).

## P5.6 Danh sách ca test (đặc tả lấy từ `auth/mfa.test.ts`, 3016 dòng; `auth/utils.test.ts` cho hàm thuần)

Không có `mfalimit.test.ts`; hạn mức được kiểm trong `mfa.test.ts`. Cột cuối: **Giữ**, **Đổi**, **Bỏ**. Với ca chưa đọc hết thân, mô tả theo tên ca và các assert đã đọc.

### Hàm thuần (`auth/utils.test.ts`)

| Ca (dòng) | Kỳ vọng | |
|---|---|---|
| isMfaRequired: user unset, project unset (28) | `undefined` dự án hoặc không có `setting`: theo `user.mfaRequired` (đúng, sai, thiếu cho true, false, false) | Giữ |
| ... Project setting requires MFA regardless of user setting (35) | project `true`: luôn `true`, kể cả user `false` | Giữ |
| ... Project setting can only tighten (42) | user `true` + project `false`/thiếu: `true`; user thiếu + project `false`: `false` | Giữ |

### `/auth/mfa/*` (`auth/mfa.test.ts`)

| Ca (dòng) | Mô tả | Kỳ vọng | |
|---|---|---|---|
| `%s %s requires a user login` (161) | 6 route (`status`, `enroll`, `send-email-challenge`, `disable`, `changepassword`, `revoke`) với Basic auth của client và On-Behalf-Of; và token do project cấp cho user có dự án riêng | 403 (revoke: 404); không có `enrollUri` | Đổi: dùng chủ thể giả (client, On-Behalf-Of) thay `preauthorize` (P3/P7) |
| Login with SMART resource scopes cannot read status (220) | scope `openid user/Observation.rs` | 403, không có `enrollUri` | Giữ |
| Login without SMART resource scopes can read status (237) | scope `openid profile email fhirUser launch/patient offline_access` | 200, có `enrollUri` | Giữ |
| SMART app login cannot read MFA status (254) | đăng nhập SMART của user đã đăng ký TOTP | không đọc được status | Bỏ: cần SMART launch (P3/P6), kiểm lại sau |
| Enroll end-to-end (332) | `enroll` trước `status`: 400 `Secret not found`; `verify` trước đăng ký: 400 `User not enrolled in MFA`; `status`: `enrolled=false`, có `enrollUri`; gọi lại `enrollUri` y hệt; `enroll` token `1234567890`: 400 `Invalid token`; token đúng: 200; lặp lại: 400 `Already enrolled`; `status` `enrolled=true`; đăng nhập mới không có `code`; `verify` token rỗng: 400 `Missing token`; sai: 400 `Invalid MFA token`; đúng: 200 có `code` | như nêu | Giữ |
| Authenticator code is single-use (461) | cùng mã dùng cho hai login | lần hai 400 `Invalid MFA token`; TTL khóa dùng lại trong khoảng (3×30 s, 4×30 s]; `disable` với mã đó: 400 `Invalid token`; mã bước kế tiếp: 200 có `code` | Giữ |
| Rejects an expired MFA login (507) | `authTime = now - 10 phút` | 400 `Login expired` | Giữ |
| Revokes a login after five failed MFA attempts (532) | 5 lần mã sai | cả 5 lần 400; `Login.revoked=true` | Giữ |
| Limits MFA failures across a user's logins (552) | 20 login, mỗi login 1 lần sai; login thứ 21 | 20 lần 400; lần 21: 429 | Giữ |
| Disable end-to-end (581) | `disable` khi chưa đăng ký: 400 `User not enrolled in MFA`; đăng ký; không token: 400 `Missing token`; sai: 400 `Invalid token`; đúng: 200 `allOk`; sau đó `status` không đăng ký, secret mới; `disable` lại: 400 `User not enrolled in MFA` | như nêu | Giữ |
| Email MFA not allowed by project (688) | project không đặt `allowedMfaMethods`, `enroll` email | 400 `MFA method not allowed` | Giữ |
| Email MFA enroll and login end-to-end (712) | project cho `totp,email`; `status` có `email` trong allowed; đăng ký qua `send-email-challenge` + `enroll`; đăng nhập trả `mfaRequired` và tự gửi mã; mã sai: 400; mã đúng: 200 | như nêu | Giữ |
| Enrolling in email MFA requires reverifying the email (773) | không token: 400 `Missing token`; mã sai: 400 `Invalid token`; user vẫn chưa đăng ký và `emailVerified` falsy; mã đúng: 200 `allOk`, `emailVerified=true`; lặp lại: 400 `Already enrolled` | như nêu | Giữ |
| Email MFA enrollment survives a concurrent refresh-secret rotation (860) | xoay `refreshSecret` từ ảnh cũ không làm mất `emailMfa` | `enroll` 200 | Đổi: chuyển thành ca tích hợp P3+P5 (cần `rotateLoginRefreshSecret`) |
| Email MFA writes do not revert a concurrent refresh-secret rotation (913) | `sendMfaEmailCode`/`clearMfaEmailCode` từ ảnh cũ giữ `refreshSecret` mới | | Đổi: như trên |
| Verifying email code marks the user emailVerified (946) | đặt `emailVerified=false`; `verify` bằng mã email | 200; `emailVerified=true` | Giữ |
| Expired email code is rejected (991) | ép `expiresAt` quá khứ | 400 `MFA code expired` | Giữ |
| Enroll in both TOTP and email; login defaults to TOTP with email fallback (1032) | hai phương thức; đăng nhập không tự gửi mã | `mfaRequired`, hai phương thức; `verify` bằng TOTP 200 | Giữ |
| Switch to email code via send-email endpoint (1082) | `send-email` rồi `verify` bằng mã email | 200 `allOk`; `verify` 200 | Giữ |
| send-email requires a login (1127) | | 400 `Missing login` | Giữ |
| send-email rejects a revoked login (1133) | | 400 `Login revoked` | Giữ |
| send-email rejects a granted login (1164) | | 400 `Login granted` | Giữ |
| send-email rejects an already-verified login (1195) | | 400 `Login already verified` | Giữ |
| send-email requires email enrollment (1226) | chỉ đăng ký TOTP | 400 `User not enrolled in email MFA` | Giữ |
| Disable email-only MFA requires an emailed code (1257) | không token: 400 `Missing token`; mã sai: 400 `Invalid token`; mã email: 200 `allOk` | | Giữ |
| send-email-challenge requires email to be enrolled or allowed (1315) | chỉ TOTP, project không cho email | 400 `Email MFA not available` | Giữ |
| Remove a single factor keeps the others (1368) | gỡ email, còn TOTP | không token 400 `Missing token`; gỡ email 200 `allOk`; vẫn đăng ký TOTP | Giữ |
| Removing TOTP regenerates the secret and keeps email (1414) | gỡ TOTP | 200; còn email; secret đổi | Giữ |
| Removing the last factor disables MFA (1445) | gỡ yếu tố cuối (không bắt buộc MFA) | 200; không còn đăng ký | Giữ |
| A connected factor can be removed using the emailed code (1482) | xác minh bằng mã email để gỡ TOTP; dùng lại mã email | 200; lần hai 400 `Invalid token` | Giữ |
| Removing an invalid method fails (1528) | `method` lạ | 400 `Invalid method` | Giữ |
| project mfaRequired setting forces MFA enrollment at login (1602) | project `true`; user đặt rõ `false` | `mfaEnrollRequired: true` (project chỉ siết) | Giữ |
| login-enroll rejects an MFA method the project does not allow (1635) | project mặc định, `mfaEnrollRequired`, `allowedMfaMethods=["totp"]`, `login-enroll` email | 400 `MFA method not allowed` | Giữ |
| project mfaRequired setting only tightens, never relaxes a user requirement (1663) | user `true` + project `false` | `mfaEnrollRequired: true` | Giữ |
| Single-project matrix (1703) | 9 tổ hợp (xem P5.4 I) | `mfaEnrollRequired` đúng/sai và có/không có `code` | Giữ |
| Multi-project: a project requirement does not leak (1760) | chọn project không yêu cầu | không cần đăng ký | Giữ |
| Multi-project: logging into the MFA-required project is gated at profile selection (1787) | chọn project yêu cầu | `mfaEnrollRequired: true` ở `/auth/profile` | Giữ (cần P2) |
| Multi-project: a user-level requirement gates at the password step (1807) | user `mfaRequired` | bị chặn trước danh sách membership | Giữ |
| Multi-project: a project requirement is enforced even when the user has opted out (1822) | user `false` + project `true` | `mfaEnrollRequired: true` | Giữ |
| enroll requires a token for TOTP (1842) | | 400 `Missing token` | Giữ |
| disable rejects a non-string token (1869) | `token: 123` | 400 có `issue` | Giữ |
| verifyConnectedFactor returns false without a token (1903) | | `false` | Giữ |
| login-enroll requires a login (1912) | | 400 `Missing login` | Giữ |
| login-enroll enrolls in email-based MFA (1918) | không token; email thành phương thức duy nhất | 200 `mfaRequired`, tự gửi mã; mã đúng ở `verify` hoàn tất | Giữ |
| login-enroll rejects an already-enrolled user (1974) | | 400 `Already enrolled` | Giữ |
| login-enroll generates a secret for a user who does not have one (2009) | `enrollUri` có `secret` khớp `^[A-Z2-7]+$`; đăng ký bằng mã sinh từ secret đó | 200, có `code`; `mfaEnrolled=true`, `mfaMethod=["totp"]` | Giữ |
| login-enroll rejects TOTP enrollment once the offered secret has expired (2045) | xóa offer sau khi cấp | 400 `Secret not found` | Giữ |
| login-enroll requires a token for TOTP enrollment (2075) | | 400 `Missing token` | Giữ |
| login-enroll enrolls in TOTP with a valid token (2101) | | 200, có `code`, user đăng ký | Giữ |
| verify with a code for the login offer enrolls the user (2134) | `verify` với mã của offer | 200 có `code`; `User` có `mfaEnrolled`, `mfaMethod=["totp"]`, `mfaSecret` = secret mời | Giữ |
| An emailed code does not confirm an authenticator enrollment (2162) | `login.emailMfa` có sẵn, gửi mã đó ở `login-enroll` và `verify` | `Invalid MFA token`; user vẫn chưa đăng ký | Giữ |
| Only one of two logins enrolling at once stores its secret (2186) | hai login đăng ký song song | đúng 1 thành công; bên thua `Already enrolled`, `mfaVerified` falsy; secret lưu là của bên thắng | Giữ (kiểm CAS `If-Match`) |
| An offered TOTP secret is only stored when its own login confirms it (2216) | secret của login khác và của phiên khác bị từ chối | `Invalid MFA token`; mã dùng để đăng ký đã bị tiêu thụ | Giữ |
| verify ignores the stored TOTP secret of a user not enrolled / enrolled in email only (2279) | `mfaSecret` có sẵn nhưng chưa đăng ký TOTP | 400 `Invalid MFA token` | Giữ |
| Removing a method the user is not enrolled in fails (2306) | | 400 `User not enrolled in MFA method` | Giữ |
| MFA-required accounts cannot remove their last factor (2351-2458) | ma trận theo phương thức và cấu hình `allowedMfaMethods` | 400 `Cannot remove the last MFA factor because MFA is required`; user không đổi; `status.mfaRequired` | Giữ |
| enrolled user is prompted for MFA at login even when mfaRequired is false (2460) | | `mfaRequired`, không có `code`; mã đúng: 200 | Giữ |
| Legacy backward compatibility (2527-2862) | 10 ca: `mfaEnrolled=true` + `mfaSecret` không có `mfaMethod`: `getEnrolledMfaMethods` suy ra `["totp"]`; `status` `enrolled=true`, giữ nguyên secret; `verify` không có `method` (thiếu token: `Missing token`; sai: `Invalid MFA token`; đúng: 200); đăng nhập không gửi thư; `enroll` lại: `Already enrolled`; `login-enroll` ghi bổ sung `mfaMethod=["totp"]`; `disable` bằng token trần gỡ hết và xoay secret (`mfaEnrolled=false`); `disable` trước đăng ký: `User not enrolled in MFA`; project mặc định chỉ `totp` (`allowedMethods=["totp"]`, email: `MFA method not allowed`) | như nêu | Giữ |
| Project branding (2863-3016) | `appName` đặt tên issuer và chủ đề; `Acme: Health` thành `Acme Health`; không `appName`: `medplum.com` và `Medplum`; user không có email: nhãn bằng `user.id`, không bao giờ `undefined` | như nêu | Giữ |

Thiếu ca ở Medplum cần **bổ sung** ở Go (không có trong source): (1) 6 yêu cầu đồng thời ở một login: chỉ 5 lượt được đặt chỗ, lượt thứ 6 trả 429 và không tăng đếm; (2) `release` khi thành công chỉ trả lại một lượt của bộ đếm user (không xóa lần sai của login khác); (3) hết hạn bộ đếm sau 10 phút; (4) `enroll` và `disable` không có hạn mức theo login/user (xem P5.7 mục S-M5).

## P5.7 Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Nguồn | Test bắt buộc |
|---|---|---|---|
| S-M1 | Hạn mức thử MFA: 5 lần mỗi login, 20 lần mỗi user trong 10 phút; lần sai thứ 5 thu hồi login; vượt hạn mức trả 429 kèm `rate-limit-reset`; đặt chỗ và kiểm tra là **nguyên tử** | `mfalimit.ts:19-75`, `oauth/utils.ts:368,386-391` | Ca `mfa.test.ts:532,552`, thêm các ca bổ sung (1) đến (3) ở trên; test đua với nhiều goroutine |
| S-M2 | Mã TOTP dùng một lần cho mỗi user, nhớ 4 bước (với `window=1`), kể cả khi chênh lệch đồng hồ giữa máy chủ | `mfalimit.ts:102-120` | Ca `mfa.test.ts:461-506`; kiểm TTL khóa |
| S-M3 | Secret TOTP: lưu trong `User.mfaSecret` **dạng thô** (`auth/utils.ts:279`); chỉ thành yếu tố khi đã đăng ký; offer mỗi login một secret, 20 phút, chỉ lưu khi chính login đó xác nhận | `auth/utils.ts:229-289` | Ca `mfa.test.ts:2216-2277`, `2279-2304`. Đề xuất lệch tùy chọn: mã hóa `mfaSecret` khi lưu (AES-GCM, khóa từ biến môi trường). Medplum không làm; cần quyết định (Q7) |
| S-M4 | Mã email: ngẫu nhiên mật mã (6 chữ số), lưu bcrypt, hết hạn 20 phút, một lần dùng, gửi lại ghi đè | `auth/utils.ts:352-358,390-395` | Ca hết hạn, một lần (`mfa.test.ts:991,1482`); test `Random.Code6` phân bố và đệm số 0; mã trong thư không vào log |
| S-M5 | **Lỗ hổng so với hạn mức**: `/auth/mfa/enroll` (TOTP và email) và `/auth/mfa/disable` không gọi `reserveMfaAttempt` (`mfa.ts:162-227,297-373`); chỉ chịu hạn mức IP loại `auth` (160/phút). Mã email 6 chữ số sống 20 phút, mỗi IP thử được tối đa khoảng 3200 lần trong cửa sổ đó; kẻ gọi cần Bearer hợp lệ. `/auth/mfa/send-email` cũng không có hạn mức theo login, chỉ IP | đã đọc | Đề xuất lệch (L-M1): dùng chung `AttemptLimiter` cho `enroll` và `disable`; thêm hạn mức gửi mã mỗi login. Test: 6 lần `disable` sai liên tiếp thì lần sau 429 |
| S-M6 | Không dùng secret của người khác: `enroll` luôn lấy secret theo `(user, login hiện tại)`; không nhận secret từ client | `auth/utils.ts:305-310`, `mfa.ts:206` | Ca "Only one of two logins..." và ca khác phiên (`mfa.test.ts:2216`) |
| S-M7 | Chỉ login mật khẩu mới qua MFA: `assertMfaLoginActive` từ chối `authMethod != password`; ngoài ra `sendLoginResult` chỉ yêu cầu MFA khi `authMethod = password` (đăng nhập Google và ngoài không bị yêu cầu) | `mfalimit.ts:162-164`, `utils.ts:479,491` | Test bảng `authMethod` |
| S-M8 | Chỉ một đường cấp `code`: `sendLoginResult`; kiểm tra thêm ở đổi `code` (Q8) | đã đọc | Ca `mfa.test.ts:332-460`: `login` mới của user đã đăng ký không có `code` trước `verify` |
| S-M9 | `readUserForAccountManagement`: cấm On-Behalf-Of, chủ thể không phải `User`, scope SMART; user toàn cục cần login `password`/`google` | `auth/utils.ts:69-104` | Ca `mfa.test.ts:161-260` |
| S-M10 | Không phân biệt "mã sai" giữa TOTP và email: cùng thông báo `Invalid MFA token`; không tiết lộ phương thức nào đúng | `oauth/utils.ts:392` | Test đơn giản |
| S-M11 | Mã email hết hạn chặn cả mã TOTP (quan sát P5.4 B.2) | đã đọc | Ca mới: có `emailMfa` hết hạn trên login, gửi mã TOTP đúng; ghi kết quả thực tế vào ca (Q6) |
| S-M12 | Gỡ MFA phải chứng minh một yếu tố đang có; không gỡ yếu tố cuối khi bắt buộc | `mfa.ts:335-355` | Ca `mfa.test.ts:2351-2458`, `1257` |
| S-M13 | Không log mã, secret, `enrollUri`, `enrollQrCode` | đề xuất | Test log: bắt log trong ca đăng ký |
| S-M14 | Thư gửi mã có nội dung cố định, không lộ thông tin khác ngoài mã và hạn | `auth/utils.ts:365-376` | Ca branding |

## P5.8 Phần Medplum có nhưng không port, điểm lệch, rủi ro, câu hỏi mở

### Không port đợt này

| Mục | Lý do |
|---|---|
| `admin/project.ts:225-250`: quản trị viên đặt lại MFA của user khác | P7 (`/admin/*`) |
| Mã khôi phục, WebAuthn | Medplum không có trong source đã đọc |
| Tự động gửi mã email khi đăng nhập Google/ngoài | MFA chỉ áp cho đăng nhập mật khẩu |
| Ca xoay `refreshSecret` (hai ca ở `mfa.test.ts:860-945`) | Cần P3; để thành ca tích hợp, **nhưng nguyên tắc "ghi bằng Mutate, không ghi đè ảnh cũ" phải có ngay từ P5** |

### Lệch có chủ đích (đề xuất)

| # | Lệch | Lý do |
|---|---|---|
| L-M1 | Dùng `AttemptLimiter` cho `enroll`/`disable` và giới hạn gửi mã email theo login | S-M5 |
| L-M2 | Đổi tiền tố khóa Redis từ `medplum:` thành `auth:` | Không có dữ liệu cần chuyển |
| L-M3 | (Tùy chọn, Q7) mã hóa `User.mfaSecret` khi lưu | S-M3 |
| L-M4 | (Tùy chọn, Q6) mã email hết hạn không chặn mã TOTP | P5.4 B.2 |
| L-M5 | (Tùy chọn, Q8) từ chối đổi `code` khi MFA chưa xác minh | S-M8 |

### Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Lua và `SET NX GET` trên miniredis | Kiểm tra sớm ở bước 5.2; có phương án fake và test Redis thật có điều kiện |
| Khác biệt otplib và pquerna (độ dài secret, tham số URI, xử lý token sai định dạng) | Test tương thích: sinh mã bằng otplib (một lần, lưu vector) và xác minh bằng Go |
| `sendLoginResult` (P2) và `MfaPolicy` (P5) vòng phụ thuộc | P2 khai báo cổng, P5 cài; ghi trong P2 |
| Đồng hồ máy chủ trôi làm TOTP lệch | `Clock` tiêm vào; `window` cấu hình được |
| Hai nơi lưu (FHIR và Redis) lệch nhau (offer mất khi Redis khởi động lại) | Chấp nhận như Medplum: offer 20 phút, người dùng xin mã QR lại |

### Câu hỏi mở

| # | Câu hỏi | Đề xuất |
|---|---|---|
| Q6 | Mã email hết hạn chặn TOTP (quan sát): giữ như Medplum hay sửa? | Sửa (bỏ qua mã email hết hạn khi người dùng có TOTP), có ca test |
| Q7 | Mã hóa `User.mfaSecret` khi lưu? Chọn khóa ở đâu (biến môi trường, KMS)? | Có, AES-GCM, khóa từ biến môi trường, có `kid` để xoay khóa |
| Q8 | Thêm kiểm tra `mfaVerified` khi đổi `code`? | Có, rẻ và là phòng thủ chiều sâu |
| Q9 | Redis dùng chung với `fhir-server` hay riêng? Cần cụm (hash tag `{userId}` trong khóa đã sẵn)? | Chung máy chủ, DB logic riêng |
| Q10 | Cho `email` làm phương thức MFA mặc định ở project mới? Medplum mặc định chỉ `totp` (`utils.ts:114-124`) | Giữ mặc định `totp` |
| Q11 | Độ dài secret TOTP (otplib mặc định chưa xác nhận) | 20 byte (160 bit) |

## P5.9 Bổ sung chi tiết `mfalimit.ts` (trước đây "chưa đọc")

Đã đọc đủ 169 dòng `auth/mfalimit.ts`, toàn bộ nằm dưới đây.

### Hằng số (`constants.ts:42-53`)

| Tên | Giá trị |
|---|---|
| `MFA_LOGIN_EXPIRATION_MS` | 10 phút (tuổi tối đa của login chờ MFA **và** cửa sổ đếm lần thử) |
| `MFA_LOGIN_ATTEMPT_LIMIT` | 5 |
| `MFA_USER_ATTEMPT_LIMIT` | 20 |
| `EMAIL_MFA_CODE_EXPIRATION_MS` | 20 phút (`constants.ts:37`) |
| `TOTP_ENROLLMENT_EXPIRATION_MS` | 20 phút (`constants.ts:40`) |

### Hàm

| Hàm (`mfalimit.ts`) | Hành vi |
|---|---|
| `reserveMfaAttempt(login)` (61-75) | Chạy script Lua `RESERVE_MFA_ATTEMPT` (19-33) trên hai khóa: login (KEYS[1]) và user (KEYS[2]); ARGV = giới hạn login 5, giới hạn user 20, TTL `600000` ms. Script: đọc hai bộ đếm; nếu bộ đếm login `>= 5` trả `{0, loginCount, userCount, PTTL(KEYS[1])}`; nếu bộ đếm user `>= 20` trả `{0, ..., PTTL(KEYS[2])}`; nếu không `INCR` cả hai, đặt `PEXPIRE` chỉ khi giá trị vừa tăng bằng 1 (cửa sổ cố định, không trượt), trả `{1, loginCount, userCount, 0}`. Kết quả `[0]!==1`: ném `429` (`tooManyRequests` kèm `rate-limit-reset = ceil(max(PTTL,0)/1000)` giây). Trả số lần thử của login. **Mỗi lần gọi `reserve` tăng cả hai bộ đếm, trước khi biết mã đúng hay sai.** |
| `releaseMfaAttempt(login)` (81-83) | Script Lua `RELEASE_MFA_ATTEMPT` (36-43): xóa khóa login; với khóa user: nếu `<= 1` thì xóa, ngược lại `DECR`. Mục đích: đăng nhập thành công trả lại **đúng một lượt** của bộ đếm user, không xóa các lần sai đồng thời của login khác |
| `tryReleaseMfaAttempt(logger, login)` (85-91) | Gọi `release`, lỗi chỉ ghi log `Failed to release MFA attempt` |
| `verifyTotpToken(userId, secret, token)` (102-120) | `window = config.mfaAuthenticatorWindow ?? 1`; `authenticator.verify` sai thì `false`. Đúng: `SET medplum:mfa:{userId}:totp:<token> 1 PX ((2*window+2) * step * 1000) NX` với `step` = 30 giây (mặc định otplib; số đúng chưa xác nhận từ source, nhưng ca `mfa.test.ts:488-492` kiểm TTL trong (90 s, 120 s]), trả `true` chỉ khi `SET` thành công (`OK`). Tức mỗi mã được chấp nhận đúng một lần cho mỗi user; thêm một bước cho chênh lệch đồng hồ, vì khóa hết hạn theo đồng hồ Redis |
| `getMfaAttemptKeys(login)` (122-129) | `[<prefix>:login:<login.id>, <prefix>:user]`; thiếu `login.id` hoặc `login.user`: `400 Invalid login` |
| `getMfaKeyPrefix(userId)` (132-134) | `medplum:mfa:{<userId>}`; dấu `{}` là hash tag để mọi khóa của một user cùng slot cụm Redis (script cần cả hai khóa cùng slot) |
| `getMfaRedis()` (138-146) | Đăng ký hai lệnh Lua một lần cho mỗi client Redis |
| `assertMfaLoginActive(login)` (152-169) | Thứ tự kiểm tra và thông báo: `Login revoked`, `Login granted`, `Login already verified`, `Invalid login authentication method`, `Login expired` (khi `authTime` không hợp lệ hoặc `now - authTime >= 10 phút`). Mọi lỗi là 400 |

### Cách dùng trong `verifyMfaToken` (`oauth/utils.ts:347-413`)

`assertMfaLoginActive` → (mã email hết hạn thì `MFA code expired`) → `reserveMfaAttempt` → so mã (email rồi TOTP) → sai: nếu số lần thử của login `>= 5` thì vá `revoked=true`, rồi ném `Invalid MFA token` → đúng: `releaseMfaAttempt`. Ca xác nhận: `mfa.test.ts:532` (5 lần sai thì `revoked`), `552` (21 login mỗi login 1 lần sai thì lần 21 trả 429), `461` (dùng lại mã).

Hệ quả toán học: vì lần sai thứ 5 thu hồi login và `assertMfaLoginActive` chạy trước `reserve`, nhánh "login `>= 5`" của script chỉ chạm tới khi có nhiều yêu cầu đang chạy đồng thời; chính vì vậy việc đặt chỗ phải nguyên tử. Lần sai thứ 20 của user chặn cả các login khác của user đó 10 phút (từ lần tăng đầu tiên). Mã đăng ký offer (`enrollSecret`) cũng đi qua cùng bộ đếm.

### Đề xuất cập nhật `docs/api-contract/auth-dto.md` (thay các mục "chưa đọc")

Thay dòng "Giới hạn thử MFA (`mfalimit.ts`) và cách sinh secret TOTP: **chưa đọc**." bằng:

> **Giới hạn thử MFA** (`mfalimit.ts`, đã đọc): `/auth/mfa/verify` và `/auth/mfa/login-enroll` đặt chỗ một lượt trước khi so mã: tối đa 5 lượt cho mỗi login và 20 lượt cho mỗi user trong 10 phút (cửa sổ cố định, bắt đầu từ lượt đầu). Lượt sai thứ 5 thu hồi login (`Login revoked` ở các lần gọi sau); vượt hạn mức trả `429` kèm extension `rate-limit-reset` (giây); đăng nhập thành công trả lại một lượt của bộ đếm user. Mã TOTP chỉ dùng được một lần cho mỗi user (nhớ 4 bước). Login chỉ dùng được để xác minh MFA trong 10 phút kể từ `authTime`. Secret TOTP do server sinh cho **mỗi (user, login)** và giữ 20 phút (Redis), chỉ thành `User.mfaSecret` khi chính login đó xác nhận bằng mã. `enroll` và `disable` không có hạn mức theo login/user (chỉ hạn mức IP).

Chỉnh bảng MFA trong `auth-dto.md`:
- `POST /verify`: lỗi `400` là `"Invalid MFA token"` (không phải `"Invalid token"`), kèm `"Login revoked"`, `"Login granted"`, `"Login already verified"`, `"Invalid login authentication method"`, `"Login expired"`, `"MFA code expired"`, `"Already enrolled"`, `"User not enrolled in MFA"`, `"Missing login"`, `"Missing token"`; `429` khi vượt hạn mức.
- `POST /login-enroll`: lỗi thực tế gồm `"Missing login"`, `"Already enrolled"`, các lỗi `Login ...` ở trên, `"MFA method not allowed"`, `"Secret not found"`, `"Missing token"`, `"Invalid MFA token"` (không phải `"Invalid token"`), và `429`.
- `POST /send-email`: thêm `"Missing login"`, `"Login revoked"`, `"Login granted"`, `"Login already verified"`, `"Invalid login authentication method"`, `"Login expired"`.
- `POST /enroll` và `POST /disable`: giữ `"Invalid token"`.
- Thêm ghi chú về hạn mức IP: `verify` và `login-enroll` 10 lần/phút/IP; mọi `/auth/*` khác 160 lần/phút/IP; `login`, `newuser`, `newproject` 5 lần/phút/IP.
- `/auth/resetpassword`: lỗi `Cannot reset password for external auth...` không phụ thuộc user tồn tại.

## P5.10 Ước lượng và các bước

Cỡ chung P5: **M (3 đến 4 ngày)**, phụ thuộc P2 (`sendLoginResult`) và P4 (`AccountGuard`, `Mailer`, `PasswordHasher`).

| Bước | Việc | Cỡ | Tiêu chí hoàn thành |
|---|---|---|---|
| 5.1 | `domain/mfa`: phương thức, `AllowedMethods`, `IsRequired`, `EnrolledMethods`, `AddMethod`, `ParseMethod`, `AssertLoginActive`, hằng số, khóa offer | S | Test bảng khớp `utils.test.ts` và ca ma trận 9 tổ hợp; không import ngoài stdlib và `pkg/fhir` |
| 5.2 | Cổng và adapter: `TOTP` (pquerna), `QR`, `OfferStore`, `AttemptLimiter`, `TOTPReplayGuard` (Redis + Lua hoặc fake), thư mã MFA; kiểm tra tương thích otplib bằng vector mẫu | M | Test adapter với miniredis hoặc Redis thật; test vector otplib; `-race` xanh |
| 5.3 | `VerifyToken` + hạn mức: 12 bước ở P5.4 B | M | Ca: `Authenticator code is single-use`, `Rejects an expired MFA login`, `Revokes a login after five failed MFA attempts`, `Limits MFA failures across a user's logins`, `Only one of two logins enrolling at once`, `An offered TOTP secret...`, `verify ignores the stored TOTP secret...`; thêm ca bổ sung (1) đến (3); test đua |
| 5.4 | Đăng ký: `status`, `login-enroll`, `enroll`, `verify`; `MfaPolicy` cho `sendLoginResult` (P2) | M | Mọi ca `Enroll end-to-end`, `login-enroll ...`, `verify with a code for the login offer...`, ma trận single/multi project, `Legacy backward compatibility`, `Project branding` |
| 5.5 | Mã email: `send-email`, `send-email-challenge`, `disable`, `verifyConnectedFactor`; hạn mức IP đã khai báo cho `verify`, `login-enroll`; áp L-M1 nếu được duyệt | S-M | Mọi ca email MFA và `disable`; test S-M5 nếu duyệt L-M1 |
| 5.6 | Kiểm tra bảo mật tổng (S-M1 đến S-M14), cập nhật `auth-dto.md` theo mục P5.9, ghi Q6 đến Q11 vào quyết định | S | Bảng S đủ test; hợp đồng cập nhật; swagger khớp |

Thứ tự: 5.1 → 5.2 → 5.3 → 5.4 → 5.5 → 5.6. P5 bắt đầu được khi 4.2 xong (cổng dùng chung); không cần 4.4 và 4.5.

---

# Tiến độ P5 (cập nhật 2026-11-22)

**P5 (MFA) XONG.** User duyệt Q6 đến Q11 và L-M1 đến L-M5 theo đề xuất ("duyệt hết, bắt đầu P5").

**Mã:**
- `domain/mfa.go` (`ParseMFAMethod`, `AddMFAMethod`, `HasMFAMethod`, `MFAMethodAllowed`, `AssertMFALoginActive`, `MFAAppName`, `MFAIssuer`, hằng số), `domain.KindTooManyRequests` (429 kèm `RetryAfter`).
- `application/mfa.go` (toàn bộ dịch vụ: `MFAVerify`, `MFALoginEnroll`, `MFASendEmail`, `MFAStatus`, `MFAEnroll`, `MFASendEmailChallenge`, `MFADisable`, lõi `verifyToken`, `mfaGate` cho `BuildLoginResult`), `application/mfa_ports.go` (cổng `TOTP`, `QR`, `SecretBox`, `MFAState`, `MFAConfig`).
- `infrastructure/totp` (pquerna/otp: SHA-1, 6 chữ số, bước 30 giây; QR bằng skip2/go-qrcode), `infrastructure/mfastate` (Memory và Redis + Lua, chạy chung một bộ test, tiền tố khóa `auth:mfa:`), `infrastructure/secretbox` (AES-256-GCM, `enc:v1:<kid>:...`, xoay khóa).
- `presentation/httpapi/mfa.go` (7 route), `errors.go` (429 + `Retry-After` + `rate-limit-reset`), `application/token_code.go` (Q8), `bootstrap/container.go` (`wireMFA`), cấu hình `AUTH_MFA_*`.

**Quyết định áp dụng:** Q6 (mã email hết hạn không chặn TOTP), Q7 (mã hóa `mfaSecret`, bắt buộc có khóa ở staging/production), Q8 (token endpoint từ chối đổi `code` khi login mật khẩu của user cần MFA chưa xác minh, không tiêu thụ code), Q9 (Redis chung máy chủ; **bắt buộc `AUTH_REDIS_URL` ở staging/production**), Q10 (mặc định chỉ `totp`), Q11 (secret 20 byte), L-M1 (hạn mức thử cho `enroll`/`disable`, giới hạn gửi mã email 5 lần mỗi login trong 20 phút), L-M2 (`auth:mfa:`).

**Bảng đối chiếu S-M1 đến S-M14 với test:**

| # | Test |
|---|---|
| S-M1 | `application` `TestMFAFifthWrongCodeRevokesTheLogin`, `TestMFAAttemptLimitAcrossLoginsOfOneUser`, `TestMFASuccessReturnsOneUserSlot`, `TestMFAAttemptsAreAtomicUnderConcurrency`; `mfastate` (hạn mức theo login và user, cửa sổ cố định, nguyên tử, release đúng một lượt, trên Memory và Redis); HTTP `TestHTTPMFATooManyAttemptsReturns429WithRateLimitReset` |
| S-M2 | `TestMFAVerifyTotpSingleUseAndWindow`, `TestMFAPassesTheDocumentedConstantsToTheStateStore` (TTL nhớ mã = (2*window+2) bước), `mfastate` `ClaimTOTP`; e2e dùng lại mã bị từ chối |
| S-M3 / Q7 | `secretbox` (mã hóa, nonce ngẫu nhiên, xoay khóa, AAD theo kid, từ chối khóa không đúng 32 byte), `TestMFALoginEnrollTotpEndToEnd` (kho chỉ có bản mã hóa), `TestMFALegacyUserWithPlainSecretAndNoMethodList` |
| S-M4 | `TestMFAEmailCodesAreSixDigitsPaddedAndVaried`, `TestMFALoginEnrollEmailSendsCodeThenVerifyConfirmsEmail` (lưu bcrypt, một lần dùng), `TestMFAEmailCodeValidityIsCheckedEverywhere` (hết hạn hoặc hạn hỏng), `TestMFASendEmail` (gửi lại ghi đè) |
| S-M5 / L-M1 | `TestMFAEnrollAndDisableAreAttemptLimited`, `TestMFAEmailSendIsRateLimitedPerLogin` |
| S-M6 | `TestMFAEnrollTotpAsLoggedInUser` (secret của login khác bị từ chối), `TestMFAVerifyEnrollsWithTheLoginOwnOffer`, `TestMFAOnlyOneOfTwoLoginsEnrollsAtOnce` |
| S-M7 | `TestMFALoginMustBeActive` và `TestMFALoginEnrollChecksTheLoginIsActive` (bảng `authMethod`, `revoked`, `granted`, hết hạn), `TestAuthorizationCodeDoesNotDemandMFAForNonPasswordLogins` |
| S-M8 / Q8 | `TestAuthorizationCodeRefusesLoginThatSkippedMFA`, `TestAuthorizationCodeRefusesWhenProjectRequiresMFA` |
| S-M9 | `TestMFAAccountGuards` |
| S-M10 | mọi ca sai mã đều `Invalid MFA token`; `TestMFAEnrollmentConfirmationCannotBeSatisfiedByAnEmailCode` |
| S-M11 / Q6 | `TestMFAEmailCodeExpiryAndQ6` |
| S-M12 | `TestMFADisable`, `TestMFADisableRefusesTheLastFactorWhenRequired`, `TestMFADisableOneOfTwoFactors`, `TestMFADisableCannotReuseACodeAlreadyConsumed` |
| S-M13 | `TestMFAFlowsNeverLogCodesSecretsOrURIs` |
| S-M14 | `TestMFALoginEnrollEmailSendsCodeThenVerifyConfirmsEmail` (nội dung thư cố định), `TestMFAEnrollmentURIIsStablePerUserAndLoginAndBranded` |

**Test khác:** e2e tiến trình thật `e2e/mfa_test.go` (đăng ký → token thật → `status`/`enroll` qua Bearer → đăng nhập lại bị buộc qua MFA → `verify` → đổi code → mã dùng lại bị từ chối → sai 5 lần thu hồi login); HTTP ở `bootstrap/mfa_http_test.go`; `bootstrap` kiểm lắp ghép từ cấu hình (`TestWireMFAChoosesStoreAndBoxFromConfig`).

**Đột biến:** khoảng 190 ca trên mã P5 (dịch vụ, miền, TOTP, secretbox, kho trạng thái trên Memory và Redis, handler, 429, Q8, `UseMFA`, lắp ghép). Mọi ca đều bị bắt sau khi thêm test, trừ các ca tương đương (kiểm tra trùng lặp có chủ ý): kiểm `Already enrolled` trước khi đăng ký trong `verifyToken` (Mutate nguyên tử ở dưới kiểm lại), kiểm token rỗng ở `connectedFactor` (đã kiểm ở `MFADisable`), kiểm độ dài và chữ số của token ở adapter TOTP (thư viện cũng kiểm), kid chứa `:` (tách ở dấu `:` đầu làm base64 hỏng), `Q8` cho login không phải mật khẩu hay đã xác minh (`DecideLoginResult` đã xét), `UseMFA` không đặt `FailClosedMFA` (cổng tự kiểm lại phụ thuộc), URL Redis sai ở `wireMFA` (`jti.NewRedis` báo trước).

**Lưu ý vận hành:**
- MFA được lắp khi dựng handler (cùng chỗ với khóa ký và jti), không phải ở `bootstrap.New`: các lệnh CLI không có MFA.
- `AUTH_MFA_SECRET_KEYS` bắt buộc ở staging/production; mất khóa thì mọi user đã đăng ký TOTP không xác minh được (giải mã lỗi là 500, không phải "mã sai"). Xoay khóa bằng cách thêm khóa mới lên đầu danh sách và giữ khóa cũ.
- Chạy nhiều instance mà không có Redis thì hạn mức thử và chống dùng lại mã nhân lên theo số instance: vì vậy cấu hình production bắt buộc `AUTH_REDIS_URL`.
- Hạn mức IP của `verify`, `login-enroll`, `send-email` dùng chung bộ đếm của `login` (không tách loại `mfa` 10/phút như Medplum); hạn mức theo login và theo user là lớp bảo vệ chính.

**Chưa làm (không chặn):** quản trị viên đặt lại MFA của user khác (P7, `/admin/*`); mã khôi phục và WebAuthn (Medplum không có); kênh gửi thư thật (D4: ở production chưa gửi được mã email, nên chỉ nên bật phương thức `totp` cho tới khi có kênh gửi); ca xoay `refreshSecret` của `mfa.test.ts` (thuộc P3, đã có test refresh riêng); hạn mức IP riêng loại `mfa`.
