# Kế hoạch port `scim`, `ws`, `fhircast` (Medplum v5.2.18 sang Go)

Trạng thái: **kế hoạch chi tiết; `ws` (`subscriptions-r4`) và `fhircast` (FC-1..FC-7) đã cài trong `apps/ws-gateway`, `pkg/fhircast` và `apps/auth` (2026-11-22, xem [split-scim-ws-services.md](split-scim-ws-services.md)); `scim` chưa có code.** Phần dưới là kế hoạch gốc nên còn câu "chưa có code" cho `fhircast`; số liệu về hiện trạng Go ở đó là của lúc lập kế hoạch. Nguồn đọc: `~/Downloads/medplum/packages/server/src` (viết tắt `S/`), kèm `packages/core/src` khi cần. Mọi `file:dòng` là của Medplum nếu không ghi khác; `go:` là của repo này. Câu nào ghi "chưa xác nhận" là chưa đọc tới nguồn. Số cỡ S/M/L là ước lượng thô của người viết, chưa ai kiểm.

Ba module khác nhau về bản chất, nên đọc theo thứ tự:

| Module | Dòng source (không test) | Vị trí đề xuất ở Go | Cỡ thô | Phụ thuộc chặn |
|---|---|---|---|---|
| `scim` | 694 (`routes.ts` 106, `utils.ts` 430, `types.ts` 158) | `apps/auth` (domain/application/presentation) | M (nếu đã có `InviteUser` của P7.4), L (nếu chưa) | auth P3 (Bearer, Basic), P7.1 (`RequireProjectAdmin`), P7.3/7.4 (xóa member, mời user), D-10 (hồ sơ) |
| `ws` | 1965 (6 file) | `pkg/fhirsub` (đã có `subscriptions-r4`) + `pkg/wsgroup` (mới) gắn ở `apps/fhir-server` | S đến M cho phần làm được; `agent`, `ai-realtime` không làm | Không (phần làm được) |
| `fhircast` | 930 (`routes.ts` 626, `utils.ts` 304) + `ws/fhircast.ts` 253 | `pkg/fhircast` (mới) + một file presentation ở `apps/fhir-server` | M đến L | Redis (hoặc kho khóa-giá trị thay thế), `pkg/wsgroup`; auth P3 cho `hub.topic` ở token |

## 0. Phát hiện đáng chú ý (đọc trước)

1. **FHIRcast của Medplum không có callback nào.** `hub.channel.type` bắt buộc là `websocket` (`fhircast/routes.ts:219-222`), cấu hình công bố `webhookSupport: false` (`routes.ts:129,139`). Do đó **không có xác minh callback, không có `hub.secret`, không có HTTP đi ra, không có SSRF**; yêu cầu "xác minh callback chống SSRF" của đề bài không có đối tượng ở nguồn. Kế hoạch ghi rõ điều này, chỉ giữ một test "kênh webhook bị từ chối và không có request ra ngoài" để không ai thêm webhook mà quên `pkg/safehttp` (mục C.8).
2. **`scim` của Medplum dùng `readResource` theo id mà không kiểm loại profile.** Admin SCIM của một project đọc, tắt, hoặc xóa được membership của Bot, ClientApplication, **chủ project và chính mình** trong project đó (`scim/utils.ts:127-135, 151-165, 269-276`); route xóa của `admin` thì chặn chủ project (`apps/auth/docs/plans/p6-p7-external-admin.md:618`). Bản Go phải chặn (mục A.5, A.8).
3. **`scim` không phân trang và `filter` lạ bị bỏ qua im lặng**: `count: 1000` cố định, `totalResults` bằng số trả về, `startIndex`/`count` bị bỏ qua, filter không khớp regex `userName eq "..."` thì trả **toàn bộ user** (`utils.ts:39, 54-64, 373-381`).
4. **`scim` bỏ qua `userName` khi PUT/PATCH** (không có dòng nào ghi `membership.userName` ngoài lúc tạo, `utils.ts:334-366`), dùng **id trong body** thay vì id trong đường dẫn khi PUT (`routes.ts:62`), `meta.location` trỏ `scim/2.0/Users/` trong khi route là `/scim/v2/` (`utils.ts:312` so với `app.ts:289`), và 401/403 trả OperationOutcome chứ không phải SCIM Error (`routes.ts:24-25`, `scimWrap` chỉ bọc handler).
5. **Okta đăng nhập SCIM bằng Basic (client id, secret)** (`scim/README.md:46-53`), trong khi kế hoạch `auth` ghi "Basic vào API FHIR: không port" (`p3-oauth2-tokens.md:90`) và để Basic cho `/admin` là câu hỏi mở Q14 (`p6-p7-external-admin.md:887`). SCIM không chạy được với Okta nếu không chốt điểm này (Q-SC-1).
6. **`ws`: `subscriptions-r4` ở Go thiếu bốn thứ quan trọng** so với Medplum: kiểm quyền đọc từng sự kiện theo người nhận (không có, `grep Principal pkg/fhirsub` rỗng), gắn token với login và kiểm lại mỗi sự kiện, `unbind-from-token`, hạn mức Subscription websocket theo user. Chi tiết ở B.1.2.
7. **`ws/echo` của Medplum không xác thực và mỗi kết nối mở một kết nối Redis** (`ws/echo.ts:39-87`, không có giới hạn): tấn công ẩn danh tiêu hao kết nối Redis. Bản Go nếu làm thì tắt mặc định ở staging/production.
8. **FHIRcast lưu ngữ cảnh không hạn dùng**: `setTopicCurrentContext` dùng `SET` không `EX` (`fhircast/utils.ts:178-183`), `hset` cũng không hạn (`routes.ts:581`): tài nguyên lâm sàng (có thể là PHI) do bất kỳ thành viên project nào gửi sống mãi trong Redis. Hub cũng **không kiểm quyền theo topic hay scope**: mọi thành viên có token trong project đọc/ghi được mọi topic (`routes.ts:601-626`; `hub.topic` chỉ được cấp khi scope có `fhircast/`, `oauth/token.ts:1141-1152`, nhưng route Hub không đòi scope đó).
9. **Heartbeat FHIRcast trùng khi nhiều instance** (mỗi instance có socket của topic đều publish, `ws/fhircast.ts:55-63`) và cắt sai topic chứa dấu `:` (`ws/fhircast.ts:60`, `projectAndTopic.split(':')[1]`).
10. **Sai khác đã kiểm của đề bài**: `docs/plans/modules-survey.md` ghi "đóng êm 30 giây" cho `ws`; ở Go thực tế mỗi kết nối đóng tối đa khoảng 5 giây (`coder/websocket close.go:185,199`) và toàn bộ bị chặn bởi `SHUTDOWN_TIMEOUT` mặc định 25 giây (`apps/fhir-server/internal/config/config.go:98`). Xem B.5.

Thứ tự đề xuất: `pkg/wsgroup` + vá `subscriptions-r4` (B) trước, vì sửa lỗ hổng đã có; rồi SCIM (A) cùng đợt P7; FHIRcast (C) cuối, ưu tiên thấp (cùng kết luận của khảo sát).

---

# A. `scim`

## A.1 Mục tiêu, phạm vi, hiện trạng Go

**Mục tiêu**: cho Okta (hoặc IdP SCIM 2.0 khác) tạo, cập nhật, vô hiệu hóa, xóa thành viên của **một project** qua `/scim/v2/Users`. Phạm vi chỉ có tài nguyên `Users` (không `Groups`, không `/ServiceProviderConfig`, `/Schemas`, `/ResourceTypes`, không `/Bulk`: nguồn không có route nào ngoài sáu route ở `routes.ts:27-85`).

Hiện trạng Go, dựa trên lệnh grep (chạy từ gốc repo):

| Lệnh | Kết quả |
|---|---|
| `grep -rniE 'scim' apps pkg --include='*.go'` | rỗng: chưa có gì |
| `grep -rnE 'InviteUser\|func .*Invite' apps/auth --include='*.go'` (bỏ test) | rỗng: chưa có `InviteUser` (là P7.4) |
| `grep -rnE 'RequireProjectAdmin\|IsProjectAdmin' apps/auth --include='*.go'` | rỗng |
| `grep -rnE 'RequireBearer\|AuthenticateBearer' apps/auth --include='*.go'` | rỗng (README auth: `/auth/me` trả 401 vì chưa có `Authenticator`) |
| `grep -rnE 'ListByProject\|ListForProject\|Delete\(' apps/auth --include='*.go'` | rỗng: kho `MembershipRepository` chỉ có `Get`, `ListForUser` (`apps/auth/internal/application/ports.go`) |
| `grep -rnE 'RetireAllSecurity\|RevokeUserLogins' apps/auth --include='*.go'` | **có**: `application/securityrequest.go:65`, `application/password.go:82` (dùng lại cho đổi email) |
| `grep -rnE 'DefaultAccessPolic' apps/auth --include='*.go'` | **có**: `domain/accesspolicy_defaults.go`, `application/admin.go:99-108` (6 chính sách mặc định khi seed) |
| `grep -n 'profile-type\|user-name' pkg/fhir/searchparams_gen.go` | **có**: `:1009` `profile-type` (token, biểu thức `ProjectMembership.profile.resolve().resourceType`), `:1484` `user-name` (string) |
| `grep -rn 'func applyJSONPatch' pkg` | có nhưng **không xuất khẩu**: `pkg/fhirrouter/patch.go:11` (application không import được `fhirrouter`) |
| `Store.Mutate`, `InTx`, `FindUserInScope` | có (`apps/auth/internal/infrastructure/store/store.go`, `application/lookup.go:59`); `TxRepos` hiện chỉ có `Users`, `SecurityRequests`, `Resources` (không có `Memberships`, `Logins`) |

Kết luận: **toàn bộ SCIM chưa có**; các viên gạch có sẵn là kho `User` với `Mutate`, đổi email (retire security request + revoke login), chính sách mặc định.

## A.2 Hành vi cần giữ (từng route)

Mọi route nằm dưới `/scim/v2/` (`app.ts:289`), sau hai middleware: `authenticateRequest` (`scim/routes.ts:24`) rồi `verifyProjectAdmin` (`routes.ts:25`; `admin/utils.ts:13-20`: cho qua khi `project.superAdmin` hoặc `membership.admin`, ngược lại 403 `forbidden`). Lỗi của handler đi qua `scimWrap` (`routes.ts:87-94`) rồi `sendScimError` (`routes.ts:96-106`): `{schemas:["urn:ietf:params:scim:api:messages:2.0:Error"], status:"<mã dạng chuỗi>", detail:<chuỗi lỗi>}`. Lỗi thường (`Error` JS) thành 400 vì `normalizeOperationOutcome` đổi nó thành `badRequest` (`core/src/outcomes.ts:537-545`).

| Route | Hành vi | Mã và thông điệp | Nguồn |
|---|---|---|---|
| `GET /Users` | Tìm `ProjectMembership` có `project = project của người gọi` và `profile-type ∈ Patient,Practitioner,RelatedPerson`, `count=1000`; nếu `filter` khớp `^userName eq "([^"]+)"$` thêm `user-name = escapeSearchValue(...)`; đọc `User` của từng membership; trả ListResponse | 200. Test Okta #9: so khớp `userName` **không phân biệt hoa thường** | `utils.ts:32-76`; `routes.ts:27-34`; `okta.test.ts:169-174` |
| `POST /Users` | Cần `login.user` là tham chiếu `User` (ClientApplication không tạo được) rồi `createScimUser` | 201; `400 Disallowed login user type`; `400 Missing default patient access policy` (Patient mà project không có chính sách mặc định); `409 User is already a member of this project`; `400 Email domain is not allowed for this project` (Practitioner, từ `inviteUser`) | `routes.ts:36-46`; `utils.ts:88-115`; `admin/invite.ts:107-115, 513` |
| `GET /Users/{id}` | `id` là **id membership**; membership phải cùng project, nếu không `forbidden` | 200; 403; 404 với `detail:"Not found"` và schema Error (id lạ) | `utils.ts:127-135`; `okta.test.ts:97-102` |
| `PUT /Users/{id}` | **Dùng id trong body** (`req.body.id`), không dùng id đường dẫn; đọc membership và user; chép trường; ghi trong **một transaction** | 200; 403 (membership ngoài project, hoặc đổi email của User không thuộc project); 404 | `routes.ts:58-66` (dòng 62); `utils.ts:177-192, 238-257` |
| `PATCH /Users/{id}` | Đổi membership+user sang SCIM, áp JSON Patch (đã đổi từ SCIM patch), chép ngược, ghi như PUT | 200; 400 với các thông điệp `Invalid SCIM patch: ...` (mục A.5) | `routes.ts:67-75`; `utils.ts:206-224` |
| `DELETE /Users/{id}` | **Chỉ xóa membership** (`deleteResource('ProjectMembership', id)`), không xóa `User` hay hồ sơ | 204 (`res.sendStatus(204)`, `routes.ts:83`); 403 ngoài project | `utils.ts:269-276` |

Biểu diễn SCIM trả ra (`convertToScimUser`, `utils.ts:302-324`): `schemas:["urn:ietf:params:scim:schemas:core:2.0:User"]`; `id = membership.id`; `meta{resourceType:"User", version = membership.meta.lastUpdated, lastModified = cùng giá trị, location}`; `userType` = loại tài nguyên của `membership.profile` (phần trước dấu `/`); `userName = membership.userName` hoặc id của profile; `externalId = membership.externalId` hoặc `user.externalId`; `name{givenName = user.firstName, familyName = user.lastName}`; `emails:[{value:user.email}]` (luôn đúng một phần tử); `active = membership.active ?? true`.

Hành vi cần giữ và bản vá đề xuất (cột "Go"):

| Hành vi Medplum | Go |
|---|---|
| Quyền: admin của project (hoặc `superAdmin`) | Giữ `membership.admin`; `superAdmin` theo D-22 (không giữ): bỏ test `routes.test.ts:386` |
| `userType` mặc định `Practitioner` khi thiếu hoặc lạ (`utils.ts:287-294`) | Giữ |
| Email lưu chữ thường (`invite.ts:102-104`; `utils.ts:346`) | Giữ |
| Tạo: `sendEmail:false`, `membership.invitedBy = người gọi`, `membership.userName = userName` (`utils.ts:100-112`) | Giữ; chỉ truyền đúng các trường này (A.8) |
| `meta.location` | **Sửa** thành `<baseUrl>/scim/v2/Users/<id>` |
| `PUT` dùng id trong body | **Sửa**: dùng id đường dẫn; id body có và khác thì 400 `invalidValue` |
| Filter lạ trả tất cả | **Sửa**: 400 với `scimType: invalidFilter` |
| 401 và 403 dạng OperationOutcome | **Sửa**: mọi lỗi dưới `/scim/v2` dùng SCIM Error (RFC 7644 mục 3.12) |
| Membership ngoài project trả 403 (lộ sự tồn tại) | Đề xuất 404 (Q-SC-4); test Medplum kỳ vọng 403 (`routes.test.ts:277, 333`) nên là sai khác có chủ ý |

## A.3 Ánh xạ sang Go

| Việc | Vị trí | Lý do |
|---|---|---|
| Kiểu SCIM, chuyển đổi, bộ phân tích filter, bộ áp patch | `apps/auth/internal/domain/scim/` (gói thuần: stdlib + `pkg/fhir`) | Luật `architecture_test.go`: `domain` chỉ stdlib và `pkg/fhir`. Bộ áp patch tự viết (không dùng `fhirrouter.applyJSONPatch` vì không xuất khẩu và `application` không được import `fhirrouter`) |
| Use case `ListScimUsers`, `CreateScimUser`, `ReadScimUser`, `ReplaceScimUser`, `PatchScimUser`, `DeleteScimUser` | `apps/auth/internal/application/scim.go` | Dữ liệu (User, ProjectMembership, Login, UserSecurityRequest) thuộc `auth`; ứng dụng không được import app khác |
| Cổng mới: `MembershipRepository.ListByProject`, `Mutate`, `Delete`; `TxRepos` thêm `Memberships`, `Logins`; `ProfileWriter` (D-10) | `application/ports.go` | `Mutate` thay `patchResource` như P1; transaction để đổi email atomic |
| Adapter kho | `infrastructure/store/repos.go` | Chỉ nơi này dùng `fhirstore`/`fhirsearch` |
| Route `/scim/v2/Users[/{id}]`, nhóm Echo gắn `RequireBearer` (hoặc Basic) và `RequireProjectAdmin`, bộ ghi lỗi SCIM | `apps/auth/internal/presentation/httpapi/scim.go` | Echo chỉ ở presentation; **không** dùng `c.Bind` vì Echo chỉ nhận `application/json` theo tiền tố (`labstack/echo/v4@v4.16.4/bind.go:95,142`) nên `application/scim+json` thành 415; dùng `json.NewDecoder` trên `c.Request().Body` có `http.MaxBytesReader` |
| Cấu hình | `apps/auth/internal/config` | `AUTH_SCIM_ENABLED` (mặc định `false`) |

Không cần sửa `architecture_test.go` cho SCIM: `domain/scim` nằm trong `apps/auth/internal/domain` (quy tắc tiền tố đã có); tầng `presentation` không cần thêm import ngoài `pkg/fhir`, `pkg/httpsec`, echo.

Ranh giới D-10 (hồ sơ `Patient`/`Practitioner`/`RelatedPerson` ở kho lâm sàng, kho `auth` tách riêng): xem A.4 và Q-SC-2.

## A.4 Mô hình dữ liệu (đọc/ghi từng trường)

| Tài nguyên | Đọc | Ghi (khi nào) |
|---|---|---|
| `ProjectMembership` | `id`, `project`, `user`, `profile`, `userName`, `externalId`, `active`, `meta.lastUpdated` | **Tạo**: `project`, `user`, `profile`, `userName` (từ SCIM `userName`), `externalId`, `invitedBy`, `accessPolicy` (mặc định) hoặc `access` (RelatedPerson). **PUT/PATCH**: chỉ `active` (nếu có trong SCIM) và `externalId` (**luôn ghi, kể cả `undefined`: xóa**, `utils.ts:360-364`). **Không bao giờ**: `admin`, `accessPolicy`, `access`, `profile`, `project`, `user`, `userName` |
| `User` | `id`, `email`, `firstName`, `lastName`, `externalId`, `project` | **Tạo** (qua mời): `firstName`, `lastName`, `email`, `passwordHash` (ngẫu nhiên 16 ký tự, người dùng không biết), `project` (xem dưới). **PUT/PATCH khi `userWritable`**: `firstName`, `lastName`, `externalId`, `email`, `emailVerified=false` nếu email đổi |
| Hồ sơ (`Patient`/`Practitioner`/`RelatedPerson`) | không đọc ở PUT/PATCH/GET (chỉ lấy loại và id từ `membership.profile`) | **Tạo** (khi mời): `name[0]{given:[firstName],family:lastName}`, `telecom[{system:email,use:work,value:email}]`, `meta.project`; tìm sẵn theo `_project`+`email` (`conditionalCreate`, `invite.ts:252-360`); RelatedPerson cần `patient` |
| `UserSecurityRequest` | | Tạo loại `invite` khi tạo User mới (`invite.ts:174-176`); **retire hết** khi email đổi |
| `Login` | | **Revoke hết** của user khi email đổi (`utils.ts:248-251`) |
| `Project` | `defaultPatientAccessPolicy`, `defaultAccessPolicies`, `setting[allowedPractitionerEmailDomain]` | không ghi |

Phạm vi `User` (`userWritable`): `user.project == project của người gọi` (`utils.ts:164`). `User` toàn cục (không có `project`, ví dụ người đăng ký tự phục vụ rồi được mời bằng email) **không ghi được** qua SCIM: đổi email thì 403, các trường khác bị bỏ qua và không có lần ghi nào (test `routes.test.ts:235-303, 339-384`: `versionId` không đổi). Quy tắc chọn phạm vi khi tạo (`invite.ts:232-238`): Patient mặc định gắn project; Practitioner/RelatedPerson mặc định toàn cục; **có `externalId` thì luôn gắn project**. Hệ quả: Okta tạo Practitioner **không gửi `externalId`** thì User là toàn cục, nên mọi cập nhật tên/email sau đó bị bỏ qua (chỉ `active` và `externalId` của membership ghi được).

Chính sách truy cập khi tạo (`invite.ts:451-487`): Patient: `Project.defaultPatientAccessPolicy` (thiếu thì SCIM từ chối `400 Missing default patient access policy`, `utils.ts:95-98`); RelatedPerson: `access:[{policy:defaultRelatedPerson, parameter:[{name:patient, valueReference:profile.patient}]}]`; Practitioner: chính sách mặc định `Practitioner` (hoặc `Admin` nếu `admin`, nhưng SCIM không bao giờ đặt `admin`). **Practitioner không có chính sách mặc định thì membership không có `accessPolicy`** (`invite.ts:480-487` chỉ gán khi `defaultPolicy` tồn tại); hệ quả quyền của membership "không có chính sách" ở Medplum chưa xác nhận (cần đọc `fhir/accesspolicy.ts`); Go nên từ chối (A.5, A.8).

RelatedPerson qua SCIM: `createScimUser` không truyền `patient` (`utils.ts:100-112`), nên chỉ thành công khi tìm thấy RelatedPerson có sẵn theo email (`invite.ts:319-329`, lỗi thiếu patient ở `:327` và `:349`), nếu không `400 Patient is required to create a RelatedPerson`; nhiều kết quả thì `412` (`multipleMatches`). Tức SCIM **thực tế không tạo được RelatedPerson mới**.

## A.5 Thuật toán phần khó

**Chuyển SCIM user, User, membership, profile**

Tạo (`createScimUser`): (1) `resourceType = userType` nếu thuộc ba loại, ngược lại `Practitioner`; (2) Patient mà project thiếu chính sách mặc định: `400`; (3) gọi `InviteUser` với `{project, resourceType, firstName=name.givenName, lastName=name.familyName, email=emails[0].value, externalId, sendEmail:false, membership:{invitedBy:người gọi, userName}}`. Các bước trong mời (đã có kế hoạch ở P7 B3.3; SCIM phụ thuộc vào đó): hạ chữ thường email; Practitioner kiểm miền email; nếu có email thì kiểm membership trùng (`conflict`) rồi `conditionalCreate` User theo `email`+phạm vi; User mới thì tạo `UserSecurityRequest` loại `invite`; tạo hồ sơ (`conditionalCreate` theo `_project`+`email`); tạo membership (`conditionalCreate` theo `user`+`project`, đã có thì 409).

Cập nhật (PUT) và (PATCH): đọc membership (phải cùng project), đọc `User`; **PUT**: áp thẳng body; **PATCH**: dựng SCIM từ (user, membership), áp patch, rồi chép ngược. Chép ngược (`utils.ts:334-366`): nếu `userWritable`: `firstName`, `lastName` lấy từ `name`, `externalId`; `email = emails[0].value.toLowerCase()` (nếu có): `emailChanged = email != user.email`, đặt `user.email`, và nếu đổi thì `emailVerified=false`; nếu không `userWritable` mà email (chữ thường) khác `user.email` thì **403**, các trường khác bỏ qua. Cả hai nhánh: `active` có thì ghi vào membership; `membership.externalId = scimUser.externalId`. Lưu (`saveScimUser`, `utils.ts:238-257`) trong **một transaction** gồm các tài nguyên `Login, ProjectMembership, User, UserSecurityRequest`: ghi `User` nếu `userWritable`; nếu `userWritable && emailChanged` thì `retireAllSecurityRequests` rồi `revokeUserLogins`; ghi membership; trả SCIM mới. Test PUT chữ hoa cùng email (`routes.test.ts:128-140`): **không** retire, **không** revoke vì so sánh sau khi hạ chữ thường.

Lưu ý cho Go: Medplum đọc rồi ghi **cả tài nguyên** không `If-Match`, nên ghi đồng thời từ nơi khác (ví dụ MFA vừa đăng ký, mật khẩu vừa đổi) có thể bị đè. Go dùng `Mutate` (đọc bản mới nhất, áp `fn`, ghi kèm `IfMatch`, thử lại khi xung đột, P1) cho từng tài nguyên trong transaction, và `fn` chỉ đặt đúng các trường ở bảng A.4.

**Chuyển SCIM patch sang JSON Patch** (`convertScimToJsonPatch`, `utils.ts:394-430`):

1. `schemas[0] != "urn:ietf:params:scim:api:messages:2.0:PatchOp"` thì lỗi `Invalid SCIM patch: missing required schema`.
2. Với mỗi operation: `op` ∉ {add, remove, replace} thì `Invalid SCIM patch: unsupported operation`.
3. Có `path`: bắt đầu bằng `/` thì `Invalid SCIM patch: path must not start with "/"`; ngược lại thêm `/` đầu (`active` thành `/active`), giữ nguyên `op` và `value`.
4. Không có `path`: `remove` thì `Invalid SCIM patch: missing required path`; `value` không phải object (hoặc là mảng, hoặc rỗng) thì `Invalid SCIM patch: value must be an object if path is missing`; ngược lại mỗi cặp (khóa, giá trị) thành một thao tác `{op, path:"/<khóa>", value}`. Ví dụ Okta `{op:replace, value:{active:false}}` thành `[{replace /active false}]`.

Áp patch (`util/patch.ts:6-24`): `applyPatch(..., {implicitArrayCreation:true})`, lỗi thì 400. **Chú ý**: `path` dạng `name.givenName` (kiểu Azure AD) thành con trỏ `/name.givenName`, tức một khóa literal chứ không phải đường dẫn lồng; kết quả (không báo lỗi hay tạo khóa thừa rồi bị bỏ qua khi chép ngược) **chưa xác nhận bằng test**. Go: bộ áp patch trong `domain/scim` hỗ trợ đúng tập thuộc tính SCIM đã biết: `active`, `externalId`, `userName`, `name.givenName`, `name.familyName`, `name` (object), `emails` (mảng, thay nguyên), `userType`, và các `value` object không `path`; đường dẫn lạ thì 400 `invalidPath` (sai khác chấp nhận được và an toàn hơn: không có thuộc tính nào được ghi mà không nằm trong bảng A.4). Ngữ nghĩa `add` trên thuộc tính đa trị là **thay nguyên** như Medplum (JSON Patch `add` vào thành viên đối tượng), không nối thêm như RFC 7644 mục 3.5.2.1; ghi rõ ở tài liệu API.

**Lọc và phân trang**: Medplum chỉ hiểu `userName eq "<giá trị không có dấu nháy kép>"` (`utils.ts:56`), chuyển sang tìm kiếm `user-name=<escapeSearchValue>` (`:61`). Dưới `string` mặc định của `fhirsearch` (`pkg/fhirsearch/match.go:200`, `HasPrefix(fold(s), fold(q))`) đây là **khớp tiền tố không phân biệt hoa thường**, nên `userName eq "a"` sẽ khớp mọi `userName` bắt đầu bằng `a`; hành vi thật ở Medplum (tiền tố hay đúng) **chưa xác nhận** (test chỉ đòi không phân biệt hoa thường, `okta.test.ts:169-174`). Go: đọc theo `project` rồi **so sánh bằng nhau sau khi hạ chữ thường** ở tầng application (không dựa vào `string` search), và ghi nhận khác biệt. Phân trang Go (RFC 7644 mục 3.4.2.4): `startIndex` (mặc định 1), `count` (mặc định và trần 100, cấu hình `AUTH_SCIM_MAX_PAGE`), `totalResults` là tổng thật, `itemsPerPage` là số trả về; sắp xếp ổn định theo `membership.id`. `filter` khác dạng trên thì 400 `scimType: invalidFilter`. Không hỗ trợ `sortBy`, `attributes`, `excludedAttributes` (không có ở nguồn).

`profile-type` ở Go: biểu thức `ProjectMembership.profile.resolve().resourceType` cần `resolve()` (`fhirsearch.NeedsResolution`), và hồ sơ có thể nằm kho khác; thay bằng lọc `strings.HasPrefix(profile.reference, "Patient/"|"Practitioner/"|"RelatedPerson/")` sau khi lấy membership theo `project`.

**Xóa**: Medplum chỉ xóa membership. Go dùng lại use case xóa member của P7.3 (xóa luôn `User` gắn project khi đó là membership cuối, chặn chủ project: `400 Cannot delete the owner of the project`, `p6-p7-external-admin.md:618`), và thêm hai chặn: không xóa/tắt **chính membership của người gọi**, không động tới membership có profile `Bot`/`ClientApplication`. Xóa membership của `User` toàn cục thì chỉ xóa membership (giống Medplum).

## A.6 Cổng, thư viện, cấu hình

Cổng của `auth` (`:8006`); không thêm thư viện ngoài (JSON stdlib). Cấu hình mới: `AUTH_SCIM_ENABLED` (mặc định `false`: route trả 404), `AUTH_SCIM_MAX_PAGE` (100), `AUTH_SCIM_MAX_BODY_BYTES` (1 MiB; Medplum dùng `maxJsonSize` = `1mb`, `config/utils.ts:32`), `AUTH_SCIM_MAX_PATCH_OPS` (50, Medplum không giới hạn), `AUTH_SCIM_ALLOW_BASIC` (xem Q-SC-1), `AUTH_SCIM_ALLOWED_IPS` (tùy chọn, danh sách CIDR). `AUTH_BLOCKED_EMAIL_DOMAINS` đã được kế hoạch P7 dự kiến (`p6-p7-external-admin.md:728`). Content-Type phản hồi: `application/scim+json` (RFC 7644 mục 3.1; Medplum trả `application/json`, chấp nhận được, test cả hai giá trị `Accept`).

## A.7 Danh sách ca test từ `*.test.ts`

Đánh dấu: [GIỮ] port nguyên; [ĐỔI] port với kỳ vọng khác (ghi rõ); [BỎ] không port (kèm lý do).

`scim/utils.test.ts`:
1. `Okta example` (`:13`): `{replace, value:{active:false}}` thành `[{replace,/active,false}]` [GIỮ]
2. `Valid operation passthrough` (`:36`): `{add, path:"active", true}` thành `/active` [GIỮ]
3. `Invalid schema` (`:57`): `Invalid SCIM patch: missing required schema` [GIỮ]
4. `Invalid path prefix` (`:72`): `path must not start with "/"` [GIỮ]
5. `Invalid operation` (`:87`): `unsupported operation` [GIỮ]
6. `Remove op requires path` (`:102`): `missing required path` [GIỮ]
7. `Add op without path must be object` (`:115`): `value must be an object if path is missing` [GIỮ]
8. `Creates a Practitioner` (`:149`): `userType` mặc định [GIỮ]
9. `Creating a Patient fails without a patient access policy` (`:161`): lỗi [GIỮ]
10. `Creating a Patient applies the Patient default access policy` (`:180`) [GIỮ]

`scim/routes.test.ts`:
11. `Search users` (`:85`): 200, có `totalResults`, `Resources` [GIỮ]
12. `Create and update user` (`:96`): POST 201 (Patient), GET 200, thấy trong danh sách, PUT đổi `externalId` và email viết hoa: 200, security request **không** bị dùng, login **không** bị revoke, DELETE 204, hết thấy trong danh sách [GIỮ; DELETE thêm: User gắn project bị xóa nếu là membership cuối]
13. `Create and patch user` (`:156`): PATCH `replace {active:false, emails:[{value:new}]}`: 200, `active=false`, `emails` bằng đúng `[{value:newEmail}]`, sau đó `emailVerified=false`, security request `used=true`, login `revoked=true` [GIỮ]
14. `Create, missing medplum user type, creates a Practitioner` (`:218`) [GIỮ; Practitioner cần chính sách mặc định, đổi seed test]
15. `Reject update of server-scoped user` (`:235`): PUT và PATCH đổi email của User toàn cục: 403; cùng email khác hoa thường: 200; email của User không đổi [GIỮ]
16. `Reject update of user from another project` (`:305`): PUT tới membership project khác: kỳ vọng 403, email không đổi [ĐỔI: 404 theo Q-SC-4; kiểm vẫn không đổi dữ liệu]
17. `Deactivate server-scoped user` (`:339`): PATCH `active=false` 200; `versionId` của User không đổi [GIỮ]
18. `Search users as super admin` (`:386`): user không phải admin nhưng project là `superAdmin` vẫn dùng được SCIM [BỎ: D-22 không giữ `superAdmin`; thay bằng ca "member không admin: 403" ở A.8]

`scim/okta.test.ts` (một test, chín bước theo kịch bản Okta, `:51-175`):
19. #1 `GET /Users`: schema ListResponse, `itemsPerPage`, `startIndex`, `totalResults`, `Resources[0]` có `id`, `name.familyName`, `name.givenName`, `userName`, `active=true`, `emails[0].value` [GIỮ; `startIndex` là 1]
20. #2 `GET /Users/{id}` khớp [GIỮ]
21. #3 filter `userName eq "abcdefgh@atko.com"` không tồn tại: 200, `totalResults=0` [GIỮ]
22. #4 id không tồn tại: 404, `detail:"Not found"`, schema Error [GIỮ]
23. #5 filter tên khác không tồn tại: 200, `totalResults=0` [GIỮ]
24. #6 `POST` (Content-Type `application/json`, `userName`, `name`, `emails` có `primary`/`type`, `displayName`, `active:true`): 201, `active`, `name`, `schemas` chứa `urn:...:User`, `userName` giữ nguyên chữ hoa [GIỮ]
25. #7 đọc lại khớp `userName`, `name`, `emails[0].value` [GIỮ]
26. #8 tạo lại cùng dữ liệu: 409, `detail:"User is already a member of this project"` [GIỮ]
27. #9 filter `userName` **viết hoa toàn bộ**: `totalResults=1` [GIỮ]

## A.8 Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test bắt buộc |
|---|---|---|
| S-1 | Chỉ admin project | Chưa xác thực: 401 (SCIM Error, `WWW-Authenticate`); member không admin: 403 cho cả sáu route; client (Basic) không admin: 403; client có `admin`: 200. **Nguồn không có test 403 nào cho SCIM** (không có trong `routes.test.ts`), phải viết mới |
| S-2 | Cô lập project | GET/PUT/PATCH/DELETE theo id membership của project khác: 404 (hoặc 403), dữ liệu bên kia không đổi; danh sách chỉ thấy project của người gọi; filter `userName` không rò user project khác dù cùng `userName` |
| S-3 | Không nâng quyền | POST và PUT với body chứa `admin`, `accessPolicy`, `access`, `profile`, `project`, `user`, `groups`, `roles`, extension doanh nghiệp, `meta`, `id`: membership tạo ra có `admin != true`, đúng chính sách mặc định, `profile`/`project`/`user` không đổi; PATCH tới `/admin`, `/accessPolicy`, `/profile`, `/project`, `/user`: 400 `invalidPath` và không đổi gì (có test chụp toàn bộ membership trước/sau) |
| S-4 | Không động tới tài khoản máy và chủ | Đọc, PUT, PATCH, DELETE membership có profile `Bot`/`ClientApplication`: 404; xóa hoặc tắt membership của chủ project: 400; xóa hoặc tắt **chính membership của người gọi**: 400 (Medplum cho phép cả bốn, A.0 mục 2) |
| S-5 | Tạo user không vượt quyền | Practitioner/Patient/RelatedPerson thiếu chính sách mặc định: 400 (Go siết thêm cho Practitioner); miền email bị chặn hoặc không nằm trong `allowedPractitionerEmailDomain`: 400 `Email domain is not allowed for this project`; đã có membership: 409; email trùng một User toàn cục có sẵn: membership được gắn vào User đó **không** sửa được User (ca 15); `externalId` trùng trong project: định nghĩa rõ (Q-SC-5) |
| S-6 | Đổi email an toàn | Đổi email: `emailVerified=false`, mọi `UserSecurityRequest` retire, mọi `Login` revoke, atomic: chèn lỗi ở bước revoke thì User và membership không đổi (rollback); email chỉ hoa thường khác: không retire/revoke (ca 12) |
| S-7 | Không lộ bí mật | Struct SCIM khai báo tường minh (không serialize `fhir.User`); test marshal chứng minh không có `passwordHash`, `mfaSecret`, `secret`; lỗi 5xx trả chung chung (D-7), không kèm nội dung lỗi nội bộ (Medplum `detail = normalizeErrorString`) |
| S-8 | Chống chèn cú pháp | `userName eq "a,b"`, `"a\|b"`, `"a\\b"`, `"a$b"`: so sánh bằng ở application nên không bị hiểu thành cú pháp tìm kiếm (Medplum phải `escapeSearchValue`, `utils.ts:61`) |
| S-9 | Giới hạn kích thước | Body trên 1 MiB: 413; PATCH quá số thao tác: 400; `count` quá trần: bị cắt về trần |
| S-10 | Bật có chủ ý | `AUTH_SCIM_ENABLED=false`: mọi `/scim/v2/*` trả 404; test staging/production cấm bật `AUTH_SCIM_ALLOW_BASIC` khi không đặt `AUTH_SCIM_ALLOWED_IPS` (Q-SC-1) |
| S-11 | Content-Type | `application/scim+json` và `application/json` đều được; loại khác 415 |

## A.9 Phần không port và sai khác chấp nhận được

Không port: `superAdmin` (D-22); `Groups`, discovery endpoints, `Bulk` (nguồn không có). Sai khác chấp nhận được (đã nêu ở A.2, A.5, A.8): `meta.location` sửa; PUT dùng id đường dẫn; filter lạ bị từ chối; phân trang thật; 401/403 dạng SCIM Error; chặn Bot, Client, chủ, chính mình; Practitioner cần chính sách mặc định; SCIM patch với đường dẫn lạ bị từ chối; xóa dùng chung use case P7.3 (xóa luôn `User` gắn project mồ côi); 404 thay 403 cho id ngoài project (nếu chốt Q-SC-4). Không sửa (giữ như Medplum): `userName` không cập nhật được sau khi tạo (Okta "Push Profile Updates" đổi `userName` sẽ bị bỏ qua: ghi rõ ở tài liệu và trả `userName` hiện hành, chưa xác nhận Okta có coi đó là lỗi đồng bộ); chỉ `emails[0]` được dùng; `phoneNumbers`, `displayName`, `name.formatted` bị bỏ qua; `meta.version` là `lastUpdated` chứ không phải ETag.

## A.10 Rủi ro và câu hỏi mở

| Mã | Câu hỏi hoặc rủi ro | Đề xuất |
|---|---|---|
| Q-SC-1 | Xác thực SCIM: Okta dùng Basic (client id, secret) (`scim/README.md:52-53`); P3 không port Basic cho API FHIR, P7 Q14 chưa chốt. Okta có chế độ Bearer nào hay không: **chưa xác nhận** | Chỉ riêng `/scim/v2`, nhận `Authorization: Basic` của `ClientApplication` có `membership.admin` và (tùy chọn) giới hạn IP; phụ thuộc `Authenticator` của P3. Hoặc Bearer của `client_credentials` nếu IdP hỗ trợ. Cần quyết định trước bước SC-3 |
| Q-SC-2 | D-10: hồ sơ lâm sàng ở kho nào khi SCIM tạo user | Cổng `ProfileWriter`; adapter `fhirhttp` gọi `fhir-server` bằng token dịch vụ (cần `client_credentials` của P3) theo saga: tạo/tìm hồ sơ rồi tạo User, membership; lỗi giữa chừng thì hoàn tác hồ sơ vừa tạo (không hoàn tác hồ sơ đã có sẵn theo email). Adapter `local` (ghi vào kho `auth`) chỉ cho dev/test. Chưa xác nhận `pkg/fhirstore` hỗ trợ transaction giữa hai kho (không thể) |
| Q-SC-3 | Practitioner không có chính sách mặc định: Medplum cấp membership không `accessPolicy` (quyền thực tế chưa xác nhận) | Go từ chối (400 `Missing default practitioner access policy`) |
| Q-SC-4 | 404 hay 403 cho id ngoài project | 404 (không lộ tồn tại); sửa test 16 |
| Q-SC-5 | `externalId` trùng trong project: Medplum không kiểm | Nên kiểm trùng ở bước tạo (409), cần chỉ mục; chưa quyết |
| Q-SC-6 | SCIM đặt lại `active=true` cho membership mà admin đã tắt thủ công (`utils.ts:360-362`) | Chấp nhận (IdP là nguồn sự thật) nhưng ghi log kiểm toán có `actor` là client SCIM |
| Q-SC-7 | Bật SCIM theo project (tính năng `scim` trong `Project.features`) hay toàn cục | Đề xuất cả hai: cờ toàn cục và `Project.features` chứa `scim`; Medplum không có |
| Rủi ro | `InviteUser` (P7.4, cỡ L) chưa có; SCIM create phụ thuộc nó | Tách "7.4a: InviteUser lõi, không gửi mail" làm trước; nếu chậm, SCIM chỉ làm read/PUT/PATCH/DELETE trước |
| Rủi ro | Giới hạn tốc độ chưa có (nền chung chờ) | Điều kiện trước khi bật SCIM ở production |

## A.11 Các bước làm

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| SC-1 | `domain/scim`: kiểu, `ToScim`, parser filter, `ConvertPatch` + bộ áp patch, test bảng cho ca 1-7 | S | Ca 1-7 xanh; ca đường dẫn lạ; không có `fmt.Errorf` rò thông điệp nội bộ | không |
| SC-2 | Cổng kho: `MembershipRepository.ListByProject/Mutate/Delete`, `TxRepos` thêm `Memberships`, `Logins`; adapter `store` + test kho (bộ nhớ, SQLite) | S đến M | Test kho xanh kể cả đồng thời (CAS) và rollback | P1 |
| SC-3 | Chốt Q-SC-1 và dựng middleware `RequireBearer`/`RequireBasicClient`, `RequireProjectAdmin`; lỗi SCIM | S | S-1, S-10, S-11 xanh | P3, P7.1 |
| SC-4 | Use case đọc, tìm, PUT, PATCH (không cần `InviteUser`) + route; transaction đổi email | M | Ca 11-13, 15-17, 19-23, 25, 27; S-2, S-3 (PUT/PATCH), S-4, S-6, S-7, S-8, S-9 xanh | SC-1..3 |
| SC-5 | `InviteUser` lõi (P7.4a) và `CreateScimUser`; cổng `ProfileWriter` (Q-SC-2) | M đến L | Ca 8-10, 14, 24, 26; S-3 (POST), S-5 xanh | SC-4, P4 (security request), D-10 |
| SC-6 | `DeleteScimUser` dùng use case P7.3 (+ chặn Bot, Client, chủ, chính mình) | S | Ca 12 (phần DELETE), S-4 xanh | P7.3 |
| SC-7 | E2E kịch bản Okta (ca 19-27) trên SQLite; cập nhật `README`, swagger, `docs/api-contract` | S | E2E xanh; tài liệu nêu sai khác A.9 | SC-5, SC-6 |

---

# B. `ws`

## B.1 Mục tiêu, phạm vi, hiện trạng Go

**Mục tiêu**: một máy chủ WebSocket dispatch theo đường dẫn `/ws/{tên}` với đóng êm. Năm kênh (`S/ws/routes.ts:19-24`): `echo`, `agent`, `ai-realtime`, `fhircast`, `subscriptions-r4`.

### B.1.1 Lệnh grep đã dùng

| Lệnh (từ gốc repo, `--include='*.go'`) | Kết quả |
|---|---|
| `grep -rniE 'scim' apps pkg` | rỗng |
| `grep -rniE '/ws/echo\|"echo"' apps pkg` | rỗng: chưa có echo |
| `grep -rniE 'ai-realtime\|aiRealtime' apps pkg` | rỗng |
| `grep -rniE 'fhircast' apps pkg` | rỗng |
| `grep -rniE 'hub\.topic\|hub\.mode' apps pkg` | rỗng |
| `grep -rniE 'unbind-from-token' apps pkg` | rỗng |
| `grep -rniE 'maxUserWebSocket' apps pkg` | rỗng |
| `grep -rniE 'safehttp' apps pkg` | rỗng (chưa trích; kế hoạch ở `cds-dicom/README.md:20`) |
| `grep -rn 'Principal\|fhirauth' pkg/fhirsub/*.go` (bỏ test) | rỗng: Hub không biết người gọi |
| `grep -rln 'websocket' apps` | `apps/fhir-server/internal/bootstrap/{container,ws_e2e_test}.go`, `internal/presentation/httpapi/httpapi.go` |
| `grep -n 'go-redis\|miniredis' go.mod` | có `github.com/redis/go-redis/v9 v9.23.0`, `github.com/alicebob/miniredis/v2 v2.40.0` |

### B.1.2 So sánh từng kênh với `pkg/fhirsub/ws.go`

| Kênh | Medplum | Go |
|---|---|---|
| `echo` | `ws/echo.ts:39-87`: mỗi kết nối đăng ký một kênh Redis ngẫu nhiên, mọi tin nhận được publish lên kênh rồi nhận lại và gửi ngược dạng văn bản. **Không xác thực** | **Chưa có** |
| `agent` | `ws/agent.ts` (368 dòng) | **Chưa có**, không làm đợt này (B.9) |
| `ai-realtime` | `ws/ai-realtime.ts` (232 dòng) | **Chưa có**, không làm đợt này (B.9) |
| `fhircast` | `ws/fhircast.ts` (253 dòng) | **Chưa có**: làm cùng module FHIRcast (phần C) |
| `subscriptions-r4` | `ws/subscriptions.ts` (831 dòng) | **Có một phần** (`pkg/fhirsub/ws.go`, 494 dòng; gắn ở `apps/fhir-server/internal/presentation/httpapi/httpapi.go:23-24,65-66`, `bootstrap/container.go:109-126,175-178,240-242,313-340`) |

`subscriptions-r4`, **phần Go đã có** (kèm dòng trong `pkg/fhirsub/ws.go` nếu không ghi khác):

- Đường dẫn `/ws/subscriptions-r4` (hằng `WebSocketPath`, `httpapi.go:24`); không qua Bearer (`httpapi.go:65-66`), token nằm trong thông điệp bind.
- `$get-ws-binding-token` (GET và POST, `container.go:176-178`; vai trò tối thiểu `RoleRead`, `httpapi/auth.go`): đọc Subscription qua repository đã tách project/quyền, bắt `channel.type = websocket`, `status ∈ {active, requested}` (`ws.go:211-238`); trả `Parameters{token, expiration, websocket-url}`.
- Token tự ký HMAC-SHA256 có tiền tố miền `"fhir-ws-binding|"` (`:173-178`), mang `{sub, proj, exp, jti}`, hạn 5 phút (`:42, 57-59`), khóa ≥ 32 byte (`:112-114`), dùng chung giữa các instance (`FHIR_WS_SIGNING_KEY`, `config.go:161,263-265`).
- Bind: thông điệp `{"type":"bind-with-token","payload":{"token":...}}` (`:338-347`); nhiều Subscription trên một kết nối, tối đa 50 (`:44, 63-65, 375-379`); kiểm Subscription còn hoạt động và **cùng project** với token (`:368-371`, `manager.go:131-138`); sau bind gửi `handshake`.
- `ping` thì `{"type":"pong"}` (`:348-349`); frame ping WebSocket định kỳ (`:434-440`).
- Heartbeat mỗi Subscription đã bind, chu kỳ mặc định 30 giây (`:69-71, 421-433`); Bundle `history` có `SubscriptionStatus type=heartbeat`.
- Sự kiện: `event-notification` kèm tài nguyên (trừ xóa), `eventsSinceSubscriptionStart`, `notificationEvent[{eventNumber, timestamp, focus}]`, `request{method,url}` (`:473-494`); khớp tiêu chí qua `Manager` (`manager.go:288-330`), chỉ cùng project (`manager.go:308-310`); đa instance qua `messaging.Broadcaster` (`manager.go:121-127`; test Redis `ws_test.go:401`).
- Giới hạn: `MaxConns` 1000 mỗi instance thì 503 (`:262-266`), đọc tối đa 64 KiB (`:275`), hạn bind 10 giây thì đóng `StatusPolicyViolation` (1008) (`:72-74, 317`), khung không phải văn bản thì 1003 (`:324-327`), JSON hỏng thì 1003 (`:334-337`), tin đầu không phải bind thì 1008 (`:350-354`), bind lỗi thì 1008 kèm lý do (`:340-343`), hàng đợi gửi đầy (256) thì 1013 (`:393-401`), `OriginPatterns` (`:49, 271`; rỗng chỉ cùng host, `websocket.Accept` kiểm `Origin`).
- Đóng êm: `Hub.Close` gửi 1001 song song rồi hủy ngữ cảnh (`:133-162`); từ lúc `closed` thì upgrade mới trả 503 (`:258-261`); test `TestWebSocketLimitsAndShutdown`, `TestWebSocketGracefulShutdownClosesConnections` (`ws_e2e_test.go:163`).
- Test hiện có: `ws_test.go`: `TestWebSocketHandshakeAndNotifications`, `...ManyConnectionsAndSubscriptions`, `...RejectsBadBinding`, `...LimitsAndShutdown`, `...Heartbeat`, `...SlowConsumerIsDropped`, `...OnlySeesOwnProject`, `...AcrossInstancesViaRedis`; e2e: `TestWebSocketSubscriptionEndToEnd`, `...IsolatedByProject`, `...GracefulShutdownClosesConnections`.

`subscriptions-r4`, **phần Medplum có mà Go chưa có**:

| # | Medplum | Go | Mức |
|---|---|---|---|
| 1 | **Kiểm quyền đọc theo người nhận và loại trường ẩn**: mỗi sự kiện, với từng socket: nạp `repo` của token (`subscriptions.ts:185`), `canPerformInteraction(READ, resource)` (`:243-265`), `removeHiddenFields(deepClone)` (`:238`), kiểm `Binary.securityContext` (`:254-263`), viết lại tệp đính kèm (`:240`). Test: `ws/subscriptions.test.ts:684` (hidden fields), `:864` (scope SMART hẹp) | **Không có**: Hub chỉ lọc theo project (`manager.go:308-310`). Người đọc được một Subscription websocket (kể cả Subscription họ tự tạo với `criteria=Observation`) nhận **mọi** `Observation` của project, bỏ qua AccessPolicy/scope | **Cao (rò dữ liệu trong project)** |
| 2 | Token là JWT ký bằng khóa server, `aud = URL websocket`, mang `login_id`, `membership_id`, `subscription_id`, `profile`, hạn 1 giờ (`getwsbindingtoken.ts:204-221`); **kiểm lại mỗi sự kiện** bằng `getLoginForAccessToken(rawToken)` (`subscriptions.ts:184-190`): login thu hồi hoặc hết hạn thì Subscription bị coi là "chết" và gỡ (`:267-312`) | Token HMAC `{sub, proj, exp, jti}` không gắn user, login, membership; dùng được bởi **bất kỳ ai nắm token** trong 5 phút; sau bind không kiểm lại (đã bind thì còn nhận tới khi đóng kết nối, kể cả khi login bị thu hồi) | Cao |
| 3 | `unbind-from-token` (`:556-585, 601-625`) | Bỏ qua im lặng sau bind đầu tiên (`ws.go:350-355`: nhánh `default` chỉ đóng nếu `first`), trước bind thì đóng 1008 | Trung bình |
| 4 | Giao thức lỗi: token sai gửi JSON `OperationOutcome` `badRequest` (`:446`) và **giữ kết nối**; thiếu claim thì gửi lỗi rồi `terminate()` (`:450-457`); sai `aud` thì gửi lỗi (`:459-463`); URL có `/projects/{id}` lệch project thì `forbidden` (`:485-498`) | Mọi lỗi bind đóng kết nối 1008 kèm lý do văn bản; không có tiền tố `/projects/{id}` | Thấp (chọn có chủ ý, B.9) |
| 5 | Hạn mức: tối đa Subscription websocket **đang hoạt động theo user** (mặc định 20, `Project.systemSetting[maxUserWebSocketSubscriptions]`, kiểm lúc **tạo** Subscription: `subscriptions.ts:820-831`; `repo.ts:1166-1180`; `config/utils.ts:62`); cờ tính năng `websocket-subscriptions` theo project (`getwsbindingtoken.ts:167-169`); từ chối `onBehalfOf` (`:163-165`) | Chỉ có `MaxConns` toàn instance và 50 bind mỗi kết nối; không giới hạn theo user hay project | Trung bình |
| 6 | Subscription websocket là **chỉ-bộ-nhớ-đệm, không ghi DB** (`repo.ts:2602-2612`, `isCacheOnly`), chỉ "hoạt động" sau khi bind và bị gỡ khi socket đóng hoặc unbind (`markInMemorySubscriptionsInactive`, `:740-818`; test `subscriptions.test.ts:239`) | Subscription lưu DB, luôn `active`; khớp tiêu chí cho mọi Subscription websocket kể cả khi không ai nghe (tốn công khớp, và thành "bảng đăng ký" tích lũy) | Trung bình |
| 7 | Heartbeat 10 giây (`heartbeat.ts:14`), **một** Bundle chứa mọi Subscription đã bind, và chỉ sau lần bind đầu (`ensureHeartbeatHandler` sau handshake, `:516-520`) | 30 giây, mỗi Subscription một Bundle | Thấp |
| 8 | Hình dạng thông báo: `eventNumber:'0'` cố định, `status` theo cấu hình, tài nguyên chỉ khi `includeResource` (`:685-722`) | Bộ đếm tăng, luôn kèm tài nguyên trừ xóa, có `request.method` (`ws.go:473-494`): gần bản backport R5 hơn | Thấp (giữ Go) |
| 9 | Số liệu otel (`setGauge`), tái tạo subscriber Redis khi mất kết nối | Không áp dụng (Go dùng broker và observability riêng) | Không áp dụng |

## B.2 Hành vi cần giữ (từng kênh)

**Dispatch và vòng đời** (`ws/routes.ts`):

| Hành vi | Nguồn | Mã, thông điệp |
|---|---|---|
| Khóa kênh = phần tử thứ hai của đường dẫn đã chuẩn hóa (`/ws/echo` thành `echo`); chuẩn hóa bỏ tiền tố `/api` và `/projects/{id}` | `routes.ts:128-130`; `util/url.ts:38-51` | |
| `maxPayload` = `maxJsonSize` (mặc định `1mb`) | `routes.ts:46-49`; `config/utils.ts:32` | khung quá lớn thì đóng 1009 (hành vi của thư viện `ws`; chưa xác nhận trong mã) |
| Upgrade tới đường dẫn **không có handler** | `routes.ts:110-113` | ghi `HTTP/1.1 404 Not Found` rồi hủy socket; test `routes.test.ts:50-63` (`/foo` lỗi kết nối; `/fhircast/STU3` thì lỗi chứa `404`) |
| Upgrade khi đang đóng | `routes.ts:98-102` | `HTTP/1.1 503 Service Unavailable`; test `routes.test.ts:137-155` |
| Kết nối tới khi đang đóng (đã qua upgrade) | `routes.ts:74-77` | đóng `1001` |
| Handler ném lỗi | `routes.ts:86-91` | đóng `1011`; test `routes.test.ts:157-181` |
| Lỗi socket | `routes.ts:60-62` | ghi log, không làm sập tiến trình |
| Đóng êm | `routes.ts:141-187`: dừng heartbeat các kênh, `server.close()`, gửi `close(1001)` tới mọi socket, chờ đóng hết hoặc `WS_CLOSE_TIMEOUT_MS = 30_000` (`:32`) rồi `terminate()`; trả ngay nếu không còn socket; sau đó dọn tài nguyên của kênh subscription (`:186`) | test `routes.test.ts:113-135, 183-201` |

**`echo`** (`echo.ts:39-87`): nhận văn bản và trả lại đúng văn bản (qua Redis); hai test (`echo.test.ts:36, 48-101`) kiểm đúng chuỗi trả lại và log lỗi khi Redis lỗi.

**`subscriptions-r4`**: như mục B.1.2 (cột Medplum) và test `ws/subscriptions.test.ts`.

**`fhircast`**: xem phần C (giao thức thuộc FHIRcast, đường dẫn `/ws/fhircast/{endpoint}`).

**`agent`** (chỉ mô tả, không đọc sâu): thông điệp JSON `agent:connect:request` (`accessToken`, `agentId`; loại `connect` cũ) rồi server đăng ký kênh Redis theo `Agent/{id}` và trả `agent:connect:response`; `agent:heartbeat:request/response`; `agent:transmit:request` (`channel`, `body`, `accessToken`) thì **chạy một Bot** (`executeBot`) với login của token; các `*:response` và `agent:error` chuyển qua `publishAgentCallback`; Agent chỉ đọc được trong **project của chính token** (trừ super admin) (`agent.ts:60-75, 83-176, 178-260`). Tệp test 24 ca (`agent.test.ts`, chưa đọc từng ca).

**`ai-realtime`** (chỉ mô tả): tin đầu phải là `ai-realtime:connect` kèm `accessToken` (nếu không `ai-realtime:error` rồi đóng 1008; `ai-realtime.ts:68-120`); kiểm tính năng `ai-realtime` của project, login SMART bị `Forbidden` (đóng 1008, `:103-110`), khóa `OPENAI_API_KEY` trong `project.secret` (thiếu thì đóng 1011), mở websocket **lên nhà cung cấp** (`getConfig().aiRealtimeTranscriptionUrl`, giao thức con `realtime`, header `Authorization: Bearer <khóa>`) và chuyển tiếp hai chiều (`:160-200`). Bốn ca test (`ai-realtime.test.ts:32-115`).

## B.3 Ánh xạ sang Go

| Phần | Vị trí | Lý do |
|---|---|---|
| Dispatch theo đường dẫn | **Không cần tự viết**: mỗi kênh là một `http.Handler`, app gắn bằng Echo (`e.GET(path, echo.WrapHandler(h))`) như `httpapi.go:65-66`; đường dẫn lạ thì Echo trả 404 | Echo đã định tuyến; bộ dispatch kiểu `handlerMap` của Medplum chỉ cần vì Node dùng một cổng upgrade chung |
| Phần dùng chung: theo dõi kết nối, giới hạn tổng, `Origin`, giới hạn kích thước, đóng êm có hạn, 503 khi đang đóng, 1001, 1011 khi handler lỗi | **`pkg/wsgroup`** (mới; thêm vào `pkgRules` với `{}`; chỉ stdlib và `coder/websocket`) | Hiện `fhirsub.Hub` tự làm các việc này (`ws.go:92-162, 254-305`); FHIRcast và echo sẽ lặp lại. Tách ra, rồi `fhirsub.Hub` dùng lại (refactor giữ nguyên test, cỡ S) |
| `echo` | `pkg/wsgroup` có ví dụ handler `Echo()` hoặc file nhỏ trong `apps/fhir-server/internal/presentation/httpapi/` | Không cần Redis: trả lại tại chỗ. Mặc định tắt ngoài `local`/`test` (B.8) |
| `subscriptions-r4` bổ sung (B.1.2 mục 1, 2, 3, 5, 6) | `pkg/fhirsub` (Hub, Manager), nhận một **cổng** `Authorizer` do `apps/fhir-server` cấp (đọc `fhirauth.Principal` đã gắn ở lúc xin token) | `pkg/fhirsub` không được import app; được import `pkg/fhirrouter`, `pkg/messaging`, `pkg/fhir` (`architecture_test.go:pkgRules`). Việc kiểm quyền cần `fhirauth` nên phải thêm `pkg/fhirauth` vào hàng cho phép của `pkg/fhirsub` trong `pkgRules` (sửa luật, có lý do), hoặc truyền hàm `func(ctx, principalSnapshot, resource) (resource, bool)` để không phụ thuộc: **đề xuất truyền hàm** (không đổi luật) |
| Gắn kênh và cấu hình | `apps/fhir-server/internal/bootstrap/container.go` (không chịu luật tầng), `presentation/httpapi` (đã được phép Echo) | |

Không có `ws` nào đặt ở `auth`.

## B.4 Mô hình dữ liệu

- `subscriptions-r4`: Medplum dùng Redis (`Subscription/{id}` cache, hash hoạt động theo project `medplum:subscriptions:r4:project:{pid}:active:v2:{type}` (`pubsub.ts:25-27`), tập Subscription hoạt động theo user, kênh pub/sub `WEBSOCKET_SUB_PUBLISH_CHANNEL`). Go giữ trạng thái trong bộ nhớ mỗi instance (`Hub.conns`, `bySub`) và Subscription trong DB; đề xuất mới cho mục 5 và 6 ở B.1.2: **bản đồ đếm** `user, project -> số Subscription websocket đang bind` trong bộ nhớ mỗi instance cho giới hạn tạm (chính xác theo cụm cần Redis; chưa quyết, Q-WS-3).
- `echo`: không dữ liệu. `agent`, `ai-realtime`: không làm.

## B.5 Thuật toán phần khó

**Đóng êm (Medplum 30 giây, Go thực tế)**: Medplum: đánh dấu `closing` (từ lúc này upgrade mới trả 503, kết nối đến muộn bị đóng 1001), gửi `close(1001)` cho từng socket, tạo promise chờ `sockets.size == 0` hoặc hết 30 giây rồi `terminate()` socket còn lại (`routes.ts:141-182`). Go (`Hub.Close`, `ws.go:133-162`): đặt `closed` (upgrade mới 503), gửi `Close(StatusGoingAway)` song song, chờ tất cả xong hoặc `ctx` hết, rồi `cancel()` để handler thoát. `coder/websocket` giới hạn mỗi lần đóng chờ bắt tay tối đa 5 giây (`close.go:185, 199`) và `ctx` của `Stop` là `SHUTDOWN_TIMEOUT` (mặc định 25 giây, tổng với `SHUTDOWN_DRAIN_DELAY` 5 giây phải nhỏ hơn `terminationGracePeriodSeconds`, `pkg/httpserve/httpserve.go:31`). Vì vậy **30 giây không đạt được và không cần**: sai khác chấp nhận được là "tối đa khoảng 5 giây mỗi kết nối, trần 25 giây". `pkg/wsgroup` đặt `CloseTimeout` cấu hình (`FHIR_WS_CLOSE_TIMEOUT`, mặc định 5 giây, kẹp về `SHUTDOWN_TIMEOUT`), và test có ca "client im lặng bị cắt sau hạn" (tương đương `routes.test.ts:137-155`).

**Dispatch theo đường dẫn**: đường dẫn đã bỏ tiền tố `/api`, `/projects/{id}`; khóa là phần tử thứ hai. Go không có tiền tố này (`survey 7.1`: chưa có quyết định chung về tiền tố URL); khi chốt thì `pkg/wsgroup` nhận một hàm chuẩn hóa. Q-WS-4.

**Sửa `subscriptions-r4`** (mục 1 và 2 **đã cài 2026-11-21**: ảnh chụp `Grants`/`Policy`/hạn vào token đã ký, kiểm ở `Hub.notify` từng kết nối; chưa làm ẩn trường, `unbind-from-token`, hạn mức bind; đề xuất ban đầu giữ bên dưới):
1. Token mới: giữ HMAC (không cần JWT) nhưng thêm `uid` (subject của người xin, từ `Principal.Subject`), `jti` đã có, và một **mã nhận dạng phiên** `sid` nếu có (chưa xác nhận `Principal` có `login_id`; xem `pkg/fhirauth/principal.go`: chỉ `Subject`, `Project`, `Role`, `Grants`, `Policy`...). Chụp `Grants` và `Policy` vào token (đã ký) hoặc lưu theo `jti` trong bộ nhớ 5 phút, để lúc phát sự kiện áp lại đúng quyền lúc xin token.
2. Mỗi sự kiện, mỗi socket: áp `Grants` (theo loại tài nguyên) và `Policy` (tiêu chí, trường ẩn) lên bản sao tài nguyên; không đọc được thì **bỏ qua** thông báo (như `subscriptions.ts:243-252`). Cắt bản sao cho từng người nhận (Medplum `deepClone` vì `removeHiddenFields` đổi tại chỗ, `:236-238`).
3. `unbind-from-token`: gỡ bind (và nếu Subscription không còn socket nào thì không còn khớp: xem 4).
4. Chỉ khớp tiêu chí cho Subscription websocket **có ít nhất một socket đã bind** (`Hub.bySub` có phần tử) thay vì mọi Subscription `active`; không cần gỡ resource DB.
5. Hạn mức bind theo (user, project): đếm trong Hub, cấu hình `FHIR_WS_MAX_BINDINGS_PER_USER` (mặc định 20, bằng Medplum `config/utils.ts:62`; Medplum đếm Subscription tạo ra, ta đếm bind: sai khác ghi rõ).

## B.6 Cổng, thư viện, cấu hình

Thư viện: `github.com/coder/websocket v1.8.15` (đã có `go.mod:26`). Cấu hình đã có: `FHIR_WEBSOCKET`, `FHIR_WS_SIGNING_KEY`, `FHIR_WS_URL`, `FHIR_WS_MAX_CONNECTIONS` (1000), `FHIR_WS_ALLOWED_ORIGINS`, `FHIR_WS_HEARTBEAT` (30s) (`config.go:161-165`). Thêm: `FHIR_WS_ECHO_ENABLED` (mặc định `true` ở `local`/`test`, `false` ở staging/production), `FHIR_WS_MAX_MESSAGE_BYTES` (64 KiB hiện cố định ở `ws.go:275`; Medplum 1 MiB), `FHIR_WS_CLOSE_TIMEOUT` (5s), `FHIR_WS_MAX_BINDINGS_PER_USER` (20), `FHIR_WS_MAX_CONNS_PER_IP` (chưa có ở cả hai bên). Cổng: `fhir-server` `:8005`.

## B.7 Danh sách ca test từ `*.test.ts`

`ws/routes.test.ts`:
1. `Echo` (`:38`): `foo` và `bar` được trả lại, đóng sạch [GIỮ, nếu làm echo]
2. `Invalid endpoint` (`:50`): `/foo` lỗi kết nối; `/fhircast/STU3` không có handler thì 404 [GIỮ; thêm `/ws/` và `/ws/unknown`]
3. `Connected sockets are closed with 1001 Going Away on shutdown` (`:113`) [GIỮ; đã có tương đương `TestWebSocketGracefulShutdownClosesConnections`]
4. `Shutdown resolves promptly when all sockets already closed` (`:123`): dưới 20 giây [ĐỔI: dưới 2 giây]
5. `Unresponsive sockets are terminated after the timeout; upgrades are rejected while closing` (`:137`): upgrade lúc đang đóng 503; socket im lặng bị cắt khi hết hạn [GIỮ; hạn 5 giây thay 30]
6. `A handler that throws closes the socket with 1011 Internal Error` (`:157`) [GIỮ trong `pkg/wsgroup`]
7. `A new app can serve WebSockets after a timed-out shutdown` (`:183`) [GIỮ: khởi động lại `Group` sạch]

`ws/echo.test.ts`: 8. `Echo` (`:36`) [GIỮ]; 9. `Logs when subscribe rejects` (`:48`) [BỎ: giả lập `getPubSubRedisSubscriber`, không còn Redis]; 10. `Logs when echoing a message fails` (`:72`) [BỎ: như trên].

`ws/subscriptions.test.ts` (đối chiếu với test Go đã có ở B.1.2):
11. `Valid subscription` (`:137`) [GIỮ: đã có `TestWebSocketHandshakeAndNotifications`]
12. `Subscription removed from active and deleted after WebSocket closed` (`:239`) [ĐỔI: sau khi socket đóng, không còn bind; Subscription trong DB còn nguyên]
13. `Subscription removed after unbind-from-token` (`:264`) [GIỮ khi làm unbind]
14. `Subscriptions with different resource types removed from correct hashes on disconnect` (`:391`) [BỎ: cấu trúc Redis của Medplum]
15. `Should reject if given an invalid binding token` (`:473`): token sai (dùng nhầm access token) [ĐỔI: đóng 1008 thay vì trả OperationOutcome; đã có `TestWebSocketRejectsBadBinding`]
16. test bảng `:536` (nhiều dạng token sai, kết quả OperationOutcome) [ĐỔI như trên]
17. `Rejects binding on a URL scoped to another project` (`:579`) [BỎ khi chưa có tiền tố `/projects/{id}` (Q-WS-4); đã có `...OnlySeesOwnProject`]
18. `Should respond with a pong if sent a ping` (`:608`) [GIỮ]
19. `Receives sub event payload` (`:618`) [GIỮ]
20. `Removes hidden fields per subscriber access policy` (`:684`) [GIỮ: **phải làm** (B.1.2 mục 1)]
21. `Notifies a subscriber whose token scope permits reading the resource` (`:864`) [GIỮ: như trên, với `Grants`]
22. `Payload with multiple subscriptions fires all events` (`:910`) [GIỮ: đã có `...ManyConnectionsAndSubscriptions`]
23. `Attachments are rewritten` (`:1030`) [BỎ: Binary Go nhúng base64; không có URL ký (chưa có `storage`)]
24. `Missing login_id in token` (`:1135`) [ĐỔI: thiếu `uid`/`sub`/`proj` thì đóng 1008]
25. `Token with a different audience` (`:1184`) [ĐỔI: token ký bằng khóa/tiền tố miền khác thì từ chối; đã có test "khóa ký khác"]
26. `Token failed to validate` (`:1218`) [GIỮ dạng hết hạn/chữ ký sai]
27. `Error while rewriting attachments` (`:1250`) [BỎ]
28. `Undefined authState returned` (`:1367`) [ĐỔI: người xin token không còn đọc được Subscription: không bind được]
29. `Dead subscription removed from active set when token fails to validate` (`:1471`) [ĐỔI: login bị thu hồi/token hết hạn thì ngừng gửi và đóng bind (nếu làm B.5 mục 1)]
30. `Dead subscription not notified on subsequent events after purge` (`:1549`) [GIỮ dạng: sau khi bị gỡ không nhận thêm]
31. `User active set decremented when WebSocket closes` (`:1759`) và `...when subscription is deleted` (`:1805`) [ĐỔI: bộ đếm bind theo user giảm khi đóng/gỡ]
32. `Bind is rejected with OperationOutcome when membership claim is absent` (`:1830`) [ĐỔI: 1008]
33. `Error when user exceeds max concurrent WebSocket subscriptions` (`:1874`): lỗi `User has exceeded allotted maximum number of concurrent WebSocket subscriptions: <n>` [ĐỔI: ở bind, đóng 1008 với thông điệp tương tự]
34. `cleanupUserSubs ...` sáu ca (`:1932, 1963, 1989, 2023, 2066, 2128`) [BỎ: dọn tập Redis của Medplum]
35. `Heartbeat received after binding to token` (`:2202`) [GIỮ: đã có `TestWebSocketHeartbeat`]
36. `Heartbeat not received before binding to token` (`:2355`) [GIỮ: thêm]
37. `Recreates Redis subscriber when prior subscriber connection ends` (`:2272`), `Recovers when the subscription handler fails to initialize` (`:2452`) [BỎ: Redis subscriber riêng]
38. `Logs and ignores a client message that is not valid JSON` (`:2374`) [ĐỔI: Go đóng 1003 thay vì bỏ qua]
39. `Logs and ignores a malformed redis subscription event payload` (`:2397`) [GIỮ: dạng event hỏng từ broker không làm sập Hub]

`ws/agent.test.ts` (24 ca, `:519` có `Heartbeat`): [BỎ cả tệp: B.9]. `ws/ai-realtime.test.ts` (bốn ca `:32, :44, :63, :82`: tin đầu sai, token không hợp lệ, login SMART `Forbidden` đóng 1008, chuyển tiếp sau connect): [BỎ cả tệp: B.9]. `ws/fhircast.test.ts`: xem C.7.

## B.8 Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test |
|---|---|---|
| W-1 | Giới hạn kết nối | Vượt `MaxConns` thì 503 (đã có `ws_test.go`); thêm giới hạn theo IP và theo (user, project); kiểm kết nối bị từ chối **không** tiêu tốn tài nguyên (không tạo goroutine tồn) |
| W-2 | `Origin` | `Origin` không trong danh sách thì 403 (`websocket.Accept`, `OriginPatterns`); không có `Origin` (client không phải trình duyệt) thì cho qua; mẫu `*` không được chấp nhận ở staging/production (cấu hình kiểm lúc khởi động) |
| W-3 | Token bind | Hết hạn, chữ ký sai, tiền tố miền sai, project khác, Subscription đã tắt: đóng 1008; token không dùng lại sau khi `jti` bị thu hồi (nếu làm `jti` store); token cấp cho socket này không dùng được ở kênh khác (tiền tố miền `fhir-ws-binding\|`, `ws.go:175`) |
| W-4 | Quyền đọc theo người nhận | Hai user cùng project, một bị AccessPolicy hoặc scope hẹp: chỉ người đọc được mới nhận; trường ẩn bị bỏ (ca 20, 21). **Điều kiện chặn phát hành** `subscriptions-r4` cho môi trường nhiều vai trò |
| W-5 | Kích thước | Khung lớn hơn giới hạn thì đóng 1009; JSON không hợp lệ thì 1003 (đã có) |
| W-6 | Echo | Tắt ở staging/production (route 404); khi bật: có giới hạn kết nối, kích thước tin, và chỉ trả lại cho đúng socket gửi |
| W-7 | Đóng êm | 1001 cho socket mở; 503 cho upgrade mới; socket im lặng bị cắt khi hết hạn; không rò goroutine (`goleak` hoặc đếm) |
| W-8 | Cô lập project | Sự kiện project A không bao giờ tới socket bind Subscription của project B (đã có `...OnlySeesOwnProject`; thêm ca token B bind Subscription id của A: từ chối) |

## B.9 Phần KHÔNG port đợt này và sai khác chấp nhận được

| Phần | Lý do không port | Phụ thuộc |
|---|---|---|
| `ws/agent` | Cần **module Agent** (Medplum Agent, cầu nối HL7/DICOM chạy tại cơ sở y tế; chưa có gì ở Go: `docs/plans/coverage-matrix.md:42`, `modules-survey.md:122`) và **chạy Bot** (`executeBot`, `agent.ts:21, 136`; quyết định M1 chưa chốt); kênh Redis `Agent/{id}` kết hợp 11 route Agent của `fhir` (`modules-survey.md:118`) | Module Agent, runtime Bot (M1), `pkg/safehttp` nếu Agent gọi ra |
| `ws/ai-realtime` | Cần nhà cung cấp AI và khóa `OPENAI_API_KEY` trong `project.secret`, tính năng `ai-realtime` theo project, URL `aiRealtimeTranscriptionUrl` (`ai-realtime.ts:150-165`); quyết định M4 (AI) chưa chốt; thêm rủi ro: chuyển tiếp hai chiều không kiểm nội dung tới dịch vụ ngoài | M4, kho bí mật theo project, `pkg/safehttp` kiểu cho websocket đi ra |
| Số liệu otel của từng kênh | Go dùng `trivita-observability-go` | |
| Tiền tố `/api`, `/projects/{id}` trong URL websocket | Chưa có quyết định chung (khảo sát 7.1) | Q-WS-4 |

Sai khác chấp nhận được: đóng êm tối đa khoảng 5 giây mỗi kết nối thay vì 30 giây (B.5); lỗi bind đóng 1008 thay vì gửi OperationOutcome (đơn giản hơn, an toàn hơn; client Medplum `medplum.subscribeToCriteria` có thể mong đợi OperationOutcome: **chưa xác nhận** hành vi client); heartbeat 30 giây (cấu hình được); hình dạng thông báo gần R5 hơn.

## B.10 Rủi ro và câu hỏi mở

| Mã | Câu hỏi hoặc rủi ro | Đề xuất |
|---|---|---|
| Q-WS-1 | Có chặn phát hành `subscriptions-r4` cho đến khi có kiểm quyền theo người nhận (B.1.2 mục 1) không | Có (rò dữ liệu trong project); đây là lỗ hổng **đã nằm trong code Go hiện có** |
| Q-WS-2 | Token của Hub: giữ HMAC tự ký hay phát hành qua `auth` (JWT, `aud` = URL websocket) | Giữ HMAC trong `pkg/fhirsub` (không cần `auth`), chụp quyền lúc xin token |
| Q-WS-3 | Hạn mức theo user cần chính xác theo cụm (Redis) hay chấp nhận theo instance | Theo instance trước |
| Q-WS-4 | Tiền tố `/api`, `/projects/{id}` cho URL websocket | Theo quyết định chung về tiền tố URL |
| Q-WS-5 | Echo có đáng làm không: nguồn chỉ dùng để kiểm kết nối | Làm nhỏ (một handler), tắt mặc định ở production |
| Q-WS-6 | `FHIR_WS_MAX_MESSAGE_BYTES` 64 KiB (Go) so với 1 MiB (Medplum): client Medplum có gửi thông điệp lớn hơn không | Chưa xác nhận; bind/ping chỉ vài trăm byte; giữ 64 KiB |
| Rủi ro | `pkg/fhirsub` tăng độ phức tạp (quyền, đếm) nên cần tách `ws.go` thành `ws_conn.go`, `ws_token.go`, `ws_authz.go` | Khi làm B-3 |

## B.11 Các bước làm

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| B-1 | `pkg/wsgroup`: nhóm kết nối, `Accept` kèm `Origin`, giới hạn, `Close(ctx)`, 503/1001/1011; thêm `pkgRules`; test ca 3-7 | S | `go test ./pkg/wsgroup` xanh, `architecture_test.go` xanh | không |
| B-2 | Refactor `fhirsub.Hub` dùng `wsgroup` (giữ API và test) | S | Toàn bộ test `pkg/fhirsub`, `ws_e2e_test.go` xanh không sửa | B-1 |
| B-3 | Kiểm quyền theo người nhận (token chụp `Grants`/`Policy`, hàm lọc do `fhir-server` truyền vào, `unbind-from-token`, chỉ khớp khi có bind, hạn mức bind) | M | Ca 13, 20, 21, 29-33; W-3, W-4, W-8 xanh; test cổng ngược (không có hàm lọc thì Hub từ chối khởi động ở staging/production) | B-2 |
| B-4 | `echo` (tùy chọn) + cấu hình `FHIR_WS_ECHO_ENABLED` | S | Ca 1, 8; W-6 xanh | B-1 |
| B-5 | Cập nhật `README`, swagger/`docs/api-contract` (giao thức bind, mã đóng, giới hạn) | S | Tài liệu nêu mọi mã đóng và sai khác B.9 | B-3 |

---

# C. `fhircast`

## C.1 Mục tiêu, phạm vi, hiện trạng Go

**Mục tiêu**: Hub FHIRcast (đồng bộ ngữ cảnh giữa các ứng dụng lâm sàng, ví dụ trình xem DICOM): hai phiên bản `/fhircast/STU2/` và `/fhircast/STU3/`, bí danh `/hub/` (là STU3) (`app.ts:282-286`); đăng ký **chỉ websocket**; sự kiện đẩy qua WebSocket `/ws/fhircast/{endpoint}` và Redis pub/sub. Phạm vi gồm cả hai file `fhircast/routes.ts`, `fhircast/utils.ts` và kênh `ws/fhircast.ts`, cùng bước `oauth/token.ts:1141-1152` (phát `hub.url`, `hub.topic` khi scope có `fhircast/`).

Hiện trạng Go (lệnh grep ở B.1.1):

| Lệnh | Kết quả |
|---|---|
| `grep -rniE 'fhircast' apps pkg --include='*.go'` | rỗng |
| `grep -rniE 'hub\.topic\|hub\.mode' apps pkg --include='*.go'` | rỗng |
| `grep -rniE 'SetNX\|SetNx' apps pkg --include='*.go'` | rỗng |
| `grep -rnE 'func .*(Publish\|Subscribe)' pkg/messaging/*.go` | `pkg/messaging` là **Redis Streams** theo `topic = EventType()`, đăng ký phải **trước `Start`** (`messaging.go:34-44`), không có Pub/Sub kênh động; `grep -n 'MaxLen\|XTrim\|MAXLEN' pkg/messaging/*.go` rỗng nên stream **không bị cắt** (chưa xác nhận chỗ khác cắt) |
| `grep -rn 'redis' go.mod` | `github.com/redis/go-redis/v9 v9.23.0` (trực tiếp), `miniredis/v2 v2.40.0` (chỉ test) |

Kết luận: **chưa có gì**. Hạ tầng Redis dùng được là `go-redis` trực tiếp; `pkg/messaging` không hợp cho kênh động theo topic (C.3).

## C.2 Hành vi cần giữ (từng route, kênh)

Cấu trúc router (`routes.ts:90-107`): mỗi phiên bản có **route công khai** (`.well-known`), rồi **route bảo vệ chung** sau `authenticateRequest` (`:99`), rồi route bảo vệ riêng từng phiên bản. Phiên bản do **đường dẫn gắn** quyết định (`getFhircastVersion`, `:211-213`: `baseUrl` chứa `STU2` thì STU2, ngược lại STU3, nên `/hub/` thành STU3).

| Route | Hành vi | Mã và thông điệp | Nguồn |
|---|---|---|---|
| `GET {v}/.well-known/fhircast-configuration` (công khai, không token) | STU2: `{eventsSupported, websocketSupport:true, webhookSupport:false, fhircastVersion:"STU2"}` (không có `getCurrentSupport`); STU3: thêm `getCurrentSupport:true`, `fhircastVersion:"STU3"` | 200 | `routes.ts:126-143`; danh sách 14 sự kiện `:109-124` |
| `POST {v}/` | Hai dạng: (a) sự kiện: có `id`, `timestamp`, `event`, `event["hub.topic"]`, `event["hub.event"]`, `event.context`; (b) đăng ký: `hub.channel.type`, `hub.mode`, `hub.topic`. `oneOf(..., errorType:'least_errored')`. Có `body.id` thì xử lý như đổi ngữ cảnh, không thì như đăng ký | 400 OperationOutcome với `issue[0].details.text` là một trong `Missing event ID`, `Missing event timestamp`, `Missing event payload`, `Missing event["hub.topic"]`, `Missing event["hub.event"]`, `Missing event.context`, `Missing hub.channel.type`, `Missing hub.mode`, `Missing hub.topic` | `routes.ts:148-182` |
| `POST {v}/{topic}` | Chỉ dạng sự kiện; **topic trên đường dẫn bị bỏ qua**, dùng `event["hub.topic"]` | 202, 400 như trên | `routes.ts:185-201` |
| Đăng ký (`hub.mode=subscribe`) | `hub.channel.type` phải `websocket`; `hub.mode` ∈ {subscribe, unsubscribe}; thiếu `hub.events` thì lỗi; tách `hub.events` theo dấu phẩy, cắt khoảng trắng, bỏ rỗng; không có sự kiện nào thì lỗi. Mỗi lần đăng ký sinh **một `endpoint` mới** (UUID), lưu `{projectId, topic, events, version}` với hạn 3600 giây, trả `{"hub.channel.endpoint": "ws(s)://<base>/ws/fhircast/<endpoint>"}` | 202; 400 `Invalid hub.channel.type`, `Invalid hub.mode`, `Missing hub.events`, `Invalid hub.events`; 500 `Internal server error` kèm `diagnostics: "Error: Failed to create subscription for topic"` khi lưu lỗi | `routes.ts:215-272`; `utils.ts:23, 72-79, 125-133` |
| Hủy (`hub.mode=unsubscribe`) | Lấy `endpoint` từ `hub.channel.endpoint` (phần cuối của URL, bỏ query/fragment/dấu `/` cuối) hoặc, nếu thiếu, từ trường `endpoint` cũ (client trước `@medplum/core` 5.1.29); cần tìm được đăng ký **cùng project và cùng topic**, rồi xóa, trả endpoint đã hủy, và publish lên kênh `{projectId}:{topic}` thông điệp `{hub.mode:"denied", hub.topic, hub.events:<các sự kiện đã đăng ký, nối bằng dấu phẩy>, hub.reason:"Subscriber unsubscribed from topic"}` **chỉ cho đúng socket đó** (`target = endpoint`) | 202 (gương endpoint); 400 `Missing endpoint`; 400 `Invalid endpoint` (không tìm thấy, khác project, khác topic) | `routes.ts:274-331`; `utils.ts:142-147` |
| Đổi ngữ cảnh: phân loại sự kiện | `Home-open` thì `other`; `*-open` thì `open`; `*-close` thì `close`; đúng `diagnosticreport-update` thì `update`; `*-select` thì `select`; còn lại `other`; không phân biệt hoa thường | | `routes.ts:54-80` |
| `*-open` | Nếu ngữ cảnh hiện tại là `DiagnosticReport` thì lưu nó vào hash `contexts` theo id báo cáo (cần có `report` hợp lệ, nếu không `400 No DiagnosticReport currently open for this topic`); nếu `event.context` có `report` đã từng lưu thì khôi phục ngữ cảnh đó (giữ `context.versionId`), ngược lại tạo `context.versionId` mới; đặt ngữ cảnh hiện tại: loại `DiagnosticReport` thì thêm mục `content` = `Bundle` `collection` rỗng; loại khác thì giữ `context`; rồi phát | 202 `{success:true, event:<payload>}` | `routes.ts:339-399, 584-593` |
| `*-close` | **Luôn** xóa ngữ cảnh hiện tại của topic (không kiểm đúng tài nguyên); `report` có thì xóa ngữ cảnh đã lưu của báo cáo đó; rồi phát | 202 | `routes.ts:401-416` |
| `DiagnosticReport-update` | Cần ngữ cảnh hiện tại là `DiagnosticReport`; `event["context.versionId"]` phải bằng phiên bản hiện tại; cần mục `updates` (Bundle); áp từng entry (`PUT` thêm/thay vào `content`, `DELETE` theo `fullUrl`); đặt `context.priorVersionId`, phiên bản mới; ghi **có điều kiện** (so sánh và đặt theo `context.versionId`); rồi phát | 202; 400 `No DiagnosticReport currently open for this topic`; 400 `Expected event.context.versionId to be '<hiện tại>', received '<nhận>'` (hoặc `received empty event.context.versionId`); 400 `Update event requires an update bundle in the context`; 400 `Update bundle contains entry with a missing request.method`; 400 `Update bundle contains bad entry with request.method = '<m>', only 'PUT' and 'DELETE' are supported by an update event`; 400 `Missing resource in a update bundle PUT entry`; 400 `DELETE entry in update bundle missing fullUrl`; 400 `fullUrl in DELETE entry is not a resolvable reference string`; 400 `Cannot delete a resource that is part of the original open context`; 400 `Cannot delete resource not currently in the content bundle`; **409** `Context for this topic changed while the update was being applied, it is no longer at version '<v>'` | `routes.ts:419-559`; `utils.ts:196-266` |
| `*-select`, sự kiện khác (`syncerror`, `userlogout`, `userhibernate`, `heartbeat`, `Home-open`...) | Không đổi ngữ cảnh; chỉ phát | 202 | `routes.ts:473-476` |
| Loại tài nguyên neo | Từ tên sự kiện: `patient`, `imagingstudy`, `encounter`, `diagnosticreport` (thường); lạ thì `400 Invalid anchor resource type` | | `utils.ts:10-15, 157-164` |
| `GET {v}/{topic}` STU2 | Có ngữ cảnh: trả **mảng** `context`; không có: `[]` ("mở rộng không chuẩn cho Nuance PowerCast Hub") | 200 | `routes.ts:601-611` |
| `GET {v}/{topic}` STU3 | Có: trả cả đối tượng `{context.type, context, context.versionId}`; không có: `{"context.type":"", context:[]}` | 200 | `routes.ts:613-626` |
| Xác thực | Tất cả trừ `.well-known` | 401 OperationOutcome `Unauthorized` (`fhircast/routes.test.ts:161-172`) | `routes.ts:99` |

**Kênh `/ws/fhircast/{endpoint}`** (`ws/fhircast.ts:137-253`):

- Lấy `endpoint` = phần cuối đường dẫn (`extractEndpoint(request.url)`, bỏ query: ca "Connect with a query string", `ws/fhircast.test.ts:1084`); đọc đăng ký; **không có thì** gửi `{"hub.mode":"denied","hub.topic":"","hub.events":"","hub.reason":"invalid endpoint"}` rồi `close()` (`:141-156`; test `:1110-1129`).
- Có: đếm tham chiếu theo `projectId:topic`, đăng ký kênh Redis `projectId:topic` (`:164-174`), sau khi đăng ký xong gửi **xác nhận**: STU3 đúng bốn trường `{hub.mode:"subscribe", hub.topic, hub.events:<nối bằng dấu phẩy>, hub.lease_seconds:3600}`; STU2 thêm bốn trường rỗng `hub.callback`, `hub.channel`, `hub.secret`, `hub.subscriber` (`:233-251`; test `:184-247`).
- Thông điệp trên kênh có dạng `{payload, target?}` (`utils.ts:55-70`): không có `payload` thì bỏ và ghi log; có `target` khác endpoint của socket thì bỏ; có `payload.event["hub.event"]` kiểu chuỗi thì chỉ gửi khi sự kiện (chữ thường) nằm trong danh sách đã đăng ký, **trừ** `heartbeat` và `syncerror` luôn được gửi; payload không có `hub.event` (điều khiển Hub như `denied`) luôn được gửi (`:89-130`; test `:307-435`).
- `payload["hub.mode"] === "denied"`: gửi xong thì **hub đóng socket** (`:190`).
- Heartbeat: mỗi chu kỳ (10 giây) mỗi instance publish một heartbeat `{timestamp, id, event:{context:[{key:"period", decimal:"10"}], "hub.event":"heartbeat", "hub.topic":<topic>}}` cho **mỗi topic có socket trên instance đó** (`:43-76`).
- Tin nhắn của client chỉ được phân tích và ghi log, không có tác dụng (`:196-207`).
- Lỗi đăng ký kênh thì đóng socket (`:225-231`).

**Phát `hub.url`/`hub.topic` ở token** (`oauth/token.ts:1141-1152`): khi `login.scope` chứa `fhircast/` thì phản hồi token có `hub.url = <baseUrl>fhircast/STU3` (luôn STU3, ghi chú TODO trong mã) và `hub.topic = getTopicForUser(userId)`: `SET medplum:fhircast:topic:{userId} <uuid> NX`, `GET`, `EXPIRE 3600` trong một MULTI (`utils.ts:282-304`), tức **topic ổn định cho một user chừng nào còn xin token ít nhất mỗi giờ**; lỗi Redis thì token trả `400` (`sendTokenError`).

## C.3 Ánh xạ sang Go

| Phần | Vị trí | Lý do |
|---|---|---|
| Logic Hub (đăng ký, phân loại sự kiện, máy trạng thái ngữ cảnh, xử lý update bundle, lọc thông điệp theo người nhận, xác nhận STU2/STU3) | **`pkg/fhircast`** (mới; thêm `pkgRules["pkg/fhircast"]`; đề xuất `{}`: không phụ thuộc `pkg/*` khác; tài nguyên FHIR giữ dạng `map[string]any`/`json.RawMessage` và chỉ cấu trúc nhỏ cho entry của Bundle) | Dùng chung giữa `fhir-server` (HTTP) và `auth` (chỉ cần `TopicForUser`) mà **không để app import app** (`architecture_test.go`: `TestEveryAppIsAClearApplication`) |
| Kho trạng thái (đăng ký, ngữ cảnh hiện tại, hash ngữ cảnh đã lưu, topic của user) | Cổng `fhircast.Store` (bộ nhớ và Redis) cùng gói | Redis cần cho đa instance; bộ nhớ cho dev/test một instance |
| Bus thông điệp theo topic | Cổng `fhircast.Bus`: `Publish(ctx, channel, msg)`, `Subscribe(channel) (<-chan, cancel)`; adapter **Redis Pub/Sub** (`go-redis` `PubSub`, **một** kết nối dùng chung cho mỗi instance, đăng ký/hủy kênh động theo đếm tham chiếu) và bộ nhớ | `pkg/messaging` không hợp: Redis Streams không cắt, đăng ký trước `Start`, giao "ít nhất một lần" không cần cho tin tạm thời; Medplum mở **một kết nối Redis cho mỗi socket** (`ws/fhircast.ts:162`), không nên lặp lại |
| Kết nối WebSocket, giới hạn, đóng êm | `pkg/wsgroup` (B-1) | Dùng chung với `subscriptions-r4` |
| HTTP: `.well-known`, `POST /`, `POST /{topic}`, `GET /{topic}` cho STU2, STU3 và `/hub/` | `apps/fhir-server/internal/presentation/httpapi/fhircast.go` (Echo mỏng) gọi `pkg/fhircast` | `fhir-server` đã có Bearer (`httpapi/auth.go`) và `fhirauth.Principal.Project`; Echo chỉ ở presentation; **cần thêm `mod+"/pkg/fhircast"` vào danh sách `allow` của `apps/fhir-server/internal/presentation` trong `architecture_test.go`** (hiện chỉ `pkg/fhir`, `pkg/fhirrouter`, `pkg/fhirauth`) |
| `Guard` cho nhóm `/fhircast` và `/hub` | Mở rộng `httpapi.Authenticator.Guard` (hiện cố định tiền tố `/fhir/R4`, `auth.go:121-130`) hoặc thêm `GuardFor(requiredRole func)`; `.well-known` công khai | `RequiredRole` hiện suy ra từ đường dẫn FHIR; FHIRcast: `GET /{topic}` cần Read, các `POST` cần Write |
| `hub.url`, `hub.topic` ở phản hồi token | `apps/auth` (P3 token endpoint) gọi `fhircast.TopicForUser(store, userID)`; `hub.url` lấy từ cấu hình `AUTH_FHIRCAST_HUB_URL` | `auth` và `fhir-server` dùng **chung một Redis** (D-20) và chung khóa `fhircast:topic:{user}`; xem Q-FC-3 (phương án không Redis) |
| Lựa chọn app | `fhir-server` (khuyến nghị), có thể tách `apps/fhircast` sau vì logic ở `pkg/` | Cùng Verifier JWT, cùng vòng đời đóng êm; WebSocket dài hạn có thể cần co giãn riêng sau này |

## C.4 Mô hình dữ liệu

Khóa Redis của Medplum và đề xuất Go (thêm tiền tố cấu hình `fhircast:`, và **luôn đưa project vào khóa**):

| Mục | Medplum | Go | Hạn |
|---|---|---|---|
| Đăng ký theo endpoint | `medplum:fhircast:endpoint:{endpoint}:subscription` = JSON `{projectId, topic, events[], version}` (`utils.ts:44-46, 72-79`) | `fhircast:ep:{endpoint}` cùng nội dung (kèm `userSubject`, `expiresAt`) | `EX 3600` (`FHIRCAST_LEASE_SECONDS`, `utils.ts:23`) |
| Ngữ cảnh hiện tại | `medplum:fhircast:project:{pid}:topic:{topic}:latest` = JSON `CurrentContext` (`utils.ts:149-151, 178-183`) | `fhircast:p:{pid}:t:{topicHash}:latest` | **Medplum: không hạn**; Go: `EX` cấu hình (`FHIR_FHIRCAST_CONTEXT_TTL`, mặc định 24 giờ) và làm mới mỗi lần ghi |
| Ngữ cảnh đã lưu theo báo cáo | hash `medplum:fhircast:project:{pid}:topic:{topic}:contexts`, trường = id báo cáo (`:153-155`; `routes.ts:561-582`) | `fhircast:p:{pid}:t:{topicHash}:ctxs` | Medplum không hạn; Go: cùng TTL, trần số trường |
| Topic của user | `medplum:fhircast:topic:{userId}` = UUID, `SET NX` + `EXPIRE 3600` mỗi lần (`utils.ts:282-304`) | `fhircast:user-topic:{userId}` | 3600 giây, làm mới |
| Kênh pub/sub | `{projectId}:{topic}` (`routes.ts:589`; `ws/fhircast.ts:165`) | `fhircast:ch:{pid}:{topicHash}` | |

`topicHash`: topic là chuỗi tự do từ client (có thể chứa `:`, ký tự điều khiển, rất dài); Medplum đưa thẳng vào khóa và vào heartbeat (`split(':')[1]`, `ws/fhircast.ts:60`, cắt sai). Go: giới hạn độ dài topic (256 byte) và khóa theo `base64url(SHA-256(topic))`, giữ topic gốc trong giá trị.

`CurrentContext` (kiểu của `@medplum/core`, **chưa đọc định nghĩa**): `{ "context.type": <loại neo>, context: [{key, resource}...], "context.versionId": <chuỗi> }` (suy ra từ `routes.ts:376-396`, `utils.ts:196-207`).

So sánh và đặt ngữ cảnh (`compareAndSetTopicCurrentContext`, `utils.ts:196-266`): Lua `GET`, giải mã bằng `cjson`, so `context['context.versionId']` với kỳ vọng, rồi `SET`; dùng `EVALSHA` với dự phòng `EVAL` khi `NOSCRIPT`. Go: **không dùng Lua** (chưa xác nhận `miniredis` hỗ trợ `cjson`); dùng `WATCH`/`MULTI`/`EXEC` của `go-redis` (`Watch`) và thử lại bằng số lần cố định; bộ nhớ dùng mutex. Vẫn phải qua ca `Replaces the context when it is still at the expected version`, `Leaves a context that has already moved on`, `Establishes no context for a topic that has none`.

## C.5 Thuật toán phần khó

**STU2 và STU3 khác nhau ở đâu (đầy đủ, theo mã đã đọc)**: chỉ ba chỗ cộng một chỗ gắn phiên bản; mọi thứ còn lại dùng chung bộ xử lý:

1. `/.well-known/fhircast-configuration`: STU3 có `getCurrentSupport:true`, STU2 không có trường này; `fhircastVersion` khác (`routes.ts:126-143`).
2. `GET /{topic}`: STU2 trả mảng `context` (hoặc `[]`); STU3 trả đối tượng đầy đủ (hoặc `{context.type:"", context:[]}`) (`routes.ts:601-626`).
3. Xác nhận trên websocket: STU3 bốn trường; STU2 thêm `hub.callback`, `hub.channel`, `hub.secret`, `hub.subscriber` đều `''` (`ws/fhircast.ts:233-251`).
4. Phiên bản chọn bởi đường dẫn gắn, lưu vào đăng ký (`version`), và dùng ở bước 3; `/hub/` luôn STU3; `hub.url` ở token luôn STU3 (`token.ts:1151`).
Những gì **không** khác (có thể nhầm): cách đăng ký, hủy, đổi ngữ cảnh, `context.versionId`, bộ lọc sự kiện, hạn 3600 giây.

**Xác minh subscriber**: Medplum **không có** (không có `hub.callback`, không gọi lại, `webhookSupport:false`). "Xác minh" duy nhất là: token Bearer hợp lệ (`routes.ts:99`), `hub.channel.type == websocket`, và endpoint khó đoán gắn với project. Endpoint là UUID v4 ngẫu nhiên (`generateId` = `crypto.randomUUID`, `core/src/crypto.ts:28-31`), **dùng lại được bởi nhiều socket** (test `ws/fhircast.test.ts:1195-1272` mở hai kết nối cùng endpoint) và không gắn với user hay token: ai nắm URL websocket (thường bị log) là đọc được sự kiện của topic trong tối đa 1 giờ. Go: giữ UUID v4 từ `crypto/rand`, thêm `userSubject` vào đăng ký (để ghi log và để cho phép hủy bởi đúng người), và giới hạn số socket đồng thời trên mỗi endpoint (mặc định 4; Medplum không giới hạn).

**Đẩy sự kiện qua WebSocket và Redis**: `publish(channel, serialize({payload, target?}))`; mọi instance có socket của topic đăng ký kênh và lọc tại chỗ theo `messageForSubscriber`. Go: một goroutine đọc `Bus` mỗi instance; bản đồ `channel -> tập socket` (đếm tham chiếu để `Subscribe/Unsubscribe` kênh Redis); socket có hàng đợi gửi giới hạn (chậm thì đóng 1013, như `fhirsub`); heartbeat sinh **cục bộ mỗi instance cho socket của chính nó** (không publish lên Bus), nên hết trùng lặp và hết lỗi cắt topic (ca `Make sure that we only get one heartbeat per tick for a given topic`, `ws/fhircast.test.ts:1195`, vẫn đúng).

**Hạn lease**: Medplum chỉ dùng 3600 giây làm hạn khóa Redis; **không buộc** socket đang mở phải đóng khi hết hạn và không có đường gia hạn (`hub.lease_seconds` luôn 3600 dù kết nối ở phút 59). Go: đặt `expiresAt` trong đăng ký, socket tự đóng khi tới hạn (1000 hoặc thông điệp `denied` với `hub.reason: "lease expired"`; chưa chốt, Q-FC-4) và cho phép đăng ký lại (`subscribe` lại với topic cũ tạo endpoint mới). Sai khác chấp nhận được và an toàn hơn.

**Máy trạng thái ngữ cảnh**: như bảng C.2 (`open`, `close`, `update`); thêm trong Go: `Update` luôn qua CAS (Medplum đã làm) và `Open` cũng nên qua CAS hoặc khóa theo topic (Medplum dùng `SET` thường: hai `open` đồng thời, ai ghi sau thắng, nhưng cả hai đều phát sự kiện; ghi nhận, không sửa).

## C.6 Cổng, thư viện, cấu hình

Thư viện: `go-redis/v9` (có), `coder/websocket` (có), `crypto/rand`; **không cần** `pkg/safehttp` (không có HTTP đi ra). Cấu hình (`apps/fhir-server`): `FHIR_FHIRCAST_ENABLED` (mặc định `false`), dùng `REDIS_URL` (`config.go:167`) và bắt buộc Redis khi `BROKER_DRIVER=redis`; `FHIR_FHIRCAST_LEASE` (3600s), `FHIR_FHIRCAST_CONTEXT_TTL` (24h), `FHIR_FHIRCAST_MAX_CONTEXT_BYTES` (256 KiB; Medplum chỉ có giới hạn 1 MiB chung của body), `FHIR_FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` (4), `FHIR_FHIRCAST_MAX_SOCKETS_PER_PROJECT`, `FHIR_FHIRCAST_REQUIRE_SCOPE` (mặc định `true`: đòi scope `fhircast/*`). `auth`: `AUTH_FHIRCAST_HUB_URL`. Cổng: `:8005`.

## C.7 Danh sách ca test từ `*.test.ts`

`fhircast/routes.test.ts` (tất cả dùng hai phiên bản trừ khi ghi khác):
1. `Get well known` (`:77`): STU2 không có `getCurrentSupport`, STU3 có `true`; `websocketSupport:true`, `webhookSupport:false` [GIỮ]
2. `New subscription success` (`:99`): 202, có `hub.channel.endpoint` [GIỮ]
3. `New subscription with url-encoded body` (`:116`): STU2, STU3 và `/hub` [GIỮ: Go phải đọc `application/x-www-form-urlencoded`; Echo bind form dùng tag `form`, nên đọc `Request.PostForm` thủ công theo khóa phẳng `hub.channel.type`...]
4. `Hub alias serves the STU3 router` (`:135`): `.well-known` STU3; endpoint khớp `ws://.../ws/fhircast/*`; `GET /hub/{topic}` trống là `{context.type:"", context:[]}` [GIỮ]
5. `New subscription no auth` (`:161`): 401, `issue[0].details.text = Unauthorized` [GIỮ]
6. `New subscription missing channel type` (`:174`): 400 `Missing hub.channel.type` [GIỮ]
7. `New subscription invalid channel type` (`:190`): 400 `Invalid hub.channel.type` [GIỮ; thêm `webhook`, `rest-hook`]
8. `New subscription invalid mode` (`:207`): 400 `Invalid hub.mode` [GIỮ]
9. `New subscription naming no events` (`:224`): `' '`, `','`, `' , '` thì 400 `Invalid hub.events` [GIỮ]
10. `Each subscription to a topic yields its own endpoint and remembers its events` (`:242`): hai endpoint khác nhau, **không chứa topic**, lưu `{projectId, topic, events, version}` [GIỮ]
11. `Whitespace around hub.events is not tracked as part of the event name` (`:279`) [GIỮ]
12. `Redis fails to store the subscription` (`:299`): 500, `OperationOutcome`, `details.text = Internal server error`, `diagnostics = Error: Failed to create subscription for topic` [ĐỔI: bỏ `diagnostics` có tiền tố `Error:` của JS; giữ 500 và thông điệp `Failed to create subscription for topic`]
13. `Unsubscribe` (`:332`): socket nhận xác nhận, hủy bằng `hub.channel.endpoint` nhận 202 gương endpoint, socket nhận `denied` rồi bị đóng, đăng ký biến mất [GIỮ]
14. `Unsubscribe without naming any events` (`:384`) [GIỮ]
15. `New subscription missing hub.events` (`:429`): 400 `Missing hub.events` [GIỮ]
16. `Unsubscribe with a request serialized by the client` (`:447`) [GIỮ: dạng form do client serialize]
17. `Unsubscribe with a request from a pre-5.1.29 client` (`:490`): dùng trường `endpoint` cũ [GIỮ (tương thích client cũ); ghi log mức info]
18. `Unsubscribe naming an endpoint in both fields cancels the issued one` (`:531`) [GIỮ]
19. `Unsubscribe with the legacy endpoint field still has to name a subscription the caller holds` (`:571`) [GIỮ]
20. `Unsubscribe without an endpoint is rejected` (`:609`): 400 `Missing endpoint`, subscriber vẫn nhận sự kiện kế tiếp [GIỮ]
21. `Unsubscribe with an unknown endpoint is rejected` (`:660`): 400 `Invalid endpoint` [GIỮ]
22. `Unsubscribe leaves the topic's other subscribers connected` (`:676`) [GIỮ]
23. `Unsubscribe cannot cancel a subscription from another project` (`:751`): 400 `Invalid endpoint`, đăng ký còn nguyên [GIỮ]
24. `Publish event missing timestamp` (`:809`): 400 `Missing event timestamp` [GIỮ]
25. `Context change request on hub.url` (`:825`) [GIỮ]
26. `Context change request on /:topic` (`:867`) [GIỮ; thêm ca topic đường dẫn khác `hub.topic`]
27. `Context change -- missing "hub.topic"` (`:909`), `missing "hub.event"` (`:950`), `missing context` (`:992`) [GIỮ: thông điệp `Missing event["hub.topic"]`, `Missing event["hub.event"]`, `Missing event.context`]
28. `Get context` (`:1012`) [GIỮ]
29. `Get context after *-open event` (`:1029`): có `context.type`, `context.versionId`, mục `content` là Bundle `collection` [GIỮ]
30. `Get context cannot read from cross-project topic` (`:1070`): project khác cùng chuỗi topic thấy ngữ cảnh trống, hai project ghi độc lập [GIỮ]
31. `Get context after *-close event` (`:1153`) [GIỮ]
32. `Check for context.versionId on DiagnosticReport-open` (`:1224`): phản hồi và kho cùng `context.versionId` [GIỮ]
33. `DiagnosticReport-update: context.priorVersionId matches prior context.versionId` (`:1254`) [GIỮ]
34. `DiagnosticReport-update is rejected when another update lands while it is applied` (`:1319`): 409, thông điệp ở C.2; ngữ cảnh còn ở bản của người thắng [GIỮ]
35. `DiagnosticReport-update returns 400 when current context is not a DiagnosticReport` (`:1387`) [GIỮ]
36. `getEventCategory` (`:1419`): bảng `DiagnosticReport-open`, `Patient-open`, ... `Home-open` thành `other`, `PATIENT-OPEN` thành `open`, `home-OPEN` thành `other`, `diagnosticreport-UPDATE` thành `update` [GIỮ]

`fhircast/utils.test.ts`:
37. `getTopicForUser`: chưa có thì tạo; có rồi thì trả cũ; `Failed to get key from Redis`; `Error during Redis transaction` (`:34-134`) [GIỮ dạng; bỏ phần giả lập `multi()` của ioredis]
38. `parseFhircastEvents` (`:135`): bảng `Patient-open, Patient-close` giống nhau với hoặc không khoảng trắng; `'Patient-open Patient-close'` là **một** tên; `''` và `undefined` thành `[]` [GIỮ]
39. `extractEndpoint` (`:150`): URL đầy đủ, bare, bỏ `?`, `#`, `/` cuối; `''`, `'?token=xyz'`, `undefined` thành `undefined` [GIỮ]
40. `Set, get, and delete a subscription` (`:167`): TTL không vượt lease [GIỮ]
41. `A cached subscription of %j reads as no subscription` (`:190`): bốn giá trị hỏng (không phải JSON; thiếu trường; `events:[42]`; `version:'STU4'`) đọc ra "không có" [GIỮ]
42. `compareAndSetTopicCurrentContext` (`:202`): `Replaces ...`, `Leaves ...`, `Establishes no context ...` [GIỮ]; `Replaces the context when the script is no longer cached by Redis` (`:251`) và `Sends only the script digest once Redis has the script cached` (`:263`) [BỎ: không dùng Lua]

`ws/fhircast.test.ts`:
43. `Send message to subscriber` (`:52`) [GIỮ]
44. `Send DiagnosticReport-select and syncerror to subscriber` (`:117`) [GIỮ]
45. `STU3 connection verification is the four fields STU3 defines` (`:184`): đúng `hub.mode`, `hub.topic`, `hub.events`, `hub.lease_seconds:3600` [GIỮ]
46. `STU2 connection verification keeps the fields STU3 dropped` (`:214`): thêm `hub.callback`, `hub.channel`, `hub.secret`, `hub.subscriber` rỗng [GIỮ]
47. `Whitespace in hub.events is absent from the confirmation` (`:249`) [GIỮ]
48. `Hub alias connection verification is STU3 shaped` (`:278`) [GIỮ]
49. `Only events this subscriber asked for are delivered` (`:307`) [GIỮ]
50. `Event names are matched case-insensitively` (`:376`) [GIỮ]
51. `Advanced suspend-resume scenario` (`:437`, `:437-1083`, kịch bản dài với Patient, DiagnosticReport, Observation: mở, cập nhật, chuyển sang bệnh nhân khác, quay lại và khôi phục ngữ cảnh đã lưu) [GIỮ: ca tích hợp quan trọng nhất; chưa đọc hết từng bước]
52. `Connect with a query string on the endpoint` (`:1084`) [GIỮ]
53. `Invalid endpoint` (`:1110`): `denied`, `hub.topic:""`, `hub.events:""`, `hub.reason:"invalid endpoint"` rồi đóng [GIỮ]
54. `Check that we get a heartbeat` (`:1152`) [GIỮ]
55. `Make sure that we only get one heartbeat per tick for a given topic` (`:1195`) [GIỮ: với heartbeat cục bộ thì tự nhiên đúng]
56. `Closes socket and logs when subscribe rejects` (`:1274`) [ĐỔI: `Bus` lỗi thì đóng socket]
57. `Logs and ignores a client message that is not valid JSON` (`:1308`) [GIỮ: tin client hỏng bị bỏ qua, không đóng]
58. `Logs and drops a topic message that is not a channel message` (`:1348`) [GIỮ: thông điệp hỏng từ Bus bị bỏ]

## C.8 Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test |
|---|---|---|
| F-1 | **Không có callback, không SSRF** (đối tượng "xác minh callback" của đề bài không tồn tại ở nguồn) | `hub.channel.type` ∈ {`webhook`, `rest-hook`, `email`, rỗng, hoa thường lạ} thì 400 `Invalid hub.channel.type`; kèm `hub.callback=http://127.0.0.1:<cổng httptest>` thì **máy chủ httptest nhận 0 request**; test kiến trúc cấm `pkg/fhircast` import `net/http` client (`http.Client`, `http.Get`) hoặc `net.Dial` |
| F-2 | Xác thực và scope | Không token thì 401 trừ `.well-known`; token không có scope `fhircast/*` thì 403 (Go siết thêm: Medplum không đòi, `token.ts:1142` chỉ dùng scope để phát topic) |
| F-3 | Cô lập project | Đăng ký, ngữ cảnh, kênh pub/sub đều theo project (ca 23, 30); project B không đăng ký được vào topic của A; sự kiện của B không tới socket của A; khóa Redis và tên kênh chứa project (không thể va chạm bằng topic tự chọn nhờ `topicHash`) |
| F-4 | Endpoint là bí mật | 128 bit ngẫu nhiên (`crypto/rand`); không chứa topic (ca 10); hết hạn đúng; socket bị đóng khi hết lease (Go); giới hạn số socket mỗi endpoint; log **không** in endpoint đầy đủ |
| F-5 | `Origin` của WebSocket | Dùng `OriginPatterns` của `pkg/wsgroup`: `Origin` lạ thì 403 |
| F-6 | Không rò tin nhắn giữa subscriber | `target` không bao giờ lên dây (payload gốc); `denied` của người này không tới người kia (ca 22) |
| F-7 | Giới hạn tài nguyên | Body tối đa; ngữ cảnh tối đa `FHIR_FHIRCAST_MAX_CONTEXT_BYTES`; độ dài topic; số entry của update bundle; số trường hash `ctxs`; **mọi khóa có TTL** (test kiểm `TTL > 0` sau khi ghi ngữ cảnh) |
| F-8 | Sự kiện hợp lệ | Tên sự kiện ngoài danh sách 14 sự kiện (`eventsSupported`) thì 400 (Go siết thêm: Medplum chuyển tiếp mọi chuỗi, `routes.ts:473-476`); `hub.events` khi đăng ký cũng phải thuộc danh sách (chưa nghiêm ở Medplum, `routes.ts:244-248`) |
| F-9 | Chống đua | `Update` song song cùng topic: đúng một thắng, một 409 (ca 34) với `-race` |
| F-10 | Không tồn PHI vô hạn | Ngữ cảnh hết hạn theo TTL; `close` xóa; test xác nhận khóa biến mất sau TTL (đồng hồ giả) |

## C.9 Phần KHÔNG port đợt này và sai khác chấp nhận được

Không port: kênh webhook (`hub.callback`, `hub.secret`, xác minh ý định): nguồn không có; nếu thêm sau thì **bắt buộc** `pkg/safehttp` (HTTPS, chặn dải nội bộ ở bước kết nối, không theo redirect) và xác minh ý định theo FHIRcast; không làm trong đợt này. Chưa làm: số liệu otel của kênh (`medplum.fhircast.*`).

Sai khác chấp nhận được: Redis không dùng Lua (WATCH/MULTI); heartbeat cục bộ mỗi instance (không trùng); lease thực sự được áp (socket đóng khi hết hạn); thêm TTL cho ngữ cảnh; yêu cầu scope `fhircast/*`; kiểm sự kiện theo danh sách; topic băm trong khóa; giới hạn socket mỗi endpoint; `hub.url` ở token theo cấu hình thay vì cố định STU3. Giữ nguyên như Medplum: STU2 giữ bốn trường rỗng; `GET /{topic}` STU2 trả mảng (cho Nuance PowerCast); `POST /{topic}` bỏ qua topic đường dẫn (có test ca 26, đề xuất thêm kiểm khớp là bước siết có thể chọn: **chưa quyết**, Q-FC-6); `*-close` luôn xóa ngữ cảnh bất kể tài nguyên (`routes.ts:409`).

## C.10 Rủi ro và câu hỏi mở

| Mã | Câu hỏi hoặc rủi ro | Đề xuất |
|---|---|---|
| Q-FC-1 | Bus: Redis Pub/Sub riêng (đề xuất) hay `pkg/messaging` Broadcaster | Pub/Sub riêng; Streams không cắt và không hợp tin tạm thời. Cần xác nhận `pkg/messaging` không cắt stream ở chỗ khác |
| Q-FC-2 | Kho trạng thái khi không có Redis (một instance) | Adapter bộ nhớ, cấm ở staging/production nếu có hơn một replica (kiểm bằng cấu hình giống `FHIR_WS_SIGNING_KEY`, `config.go:263`) |
| Q-FC-3 | `hub.topic` ở token: dùng Redis chung (D-20) hay suy ra không trạng thái (`HMAC(khóa, userId)`) | Đề xuất Redis chung (giữ đúng ngữ nghĩa Medplum: topic ổn định tới 1 giờ sau lần xin token cuối); phương án HMAC không xoay vòng được nên không chọn |
| Q-FC-4 | Cách báo lease hết hạn: đóng 1000 hay gửi `denied` trước | `denied` với `hub.reason` rồi đóng, nhất quán với hủy đăng ký |
| Q-FC-5 | Có đòi scope `fhircast/*` ở Hub không (siết so với Medplum; client Medplum cũ không có scope này có thể hỏng) | Đòi, có cờ tắt `FHIR_FHIRCAST_REQUIRE_SCOPE` cho giai đoạn chuyển tiếp |
| Q-FC-6 | `POST /{topic}` khi topic đường dẫn khác `hub.topic` | Từ chối 400 `Topic mismatch` (khác Medplum); cần kiểm client OHIF |
| Q-FC-7 | `CurrentContext` kiểu `@medplum/core`: chưa đọc định nghĩa; trường `context.type` cho `Encounter`, `ImagingStudy` | Đọc `packages/core/src/fhircast` trước bước FC-3 |
| Q-FC-8 | Topic không gắn người sở hữu: mọi thành viên project đọc/ghi mọi topic (`routes.ts:601-626`) | Chấp nhận trong project (topic là bí mật), ghi vào tài liệu; không thêm bảng sở hữu ở đợt này |
| Rủi ro | `architecture_test.go` cần hai sửa: `pkgRules["pkg/fhircast"]`, `pkgRules["pkg/wsgroup"]`, và thêm `pkg/fhircast` vào `allow` của `apps/fhir-server/internal/presentation` | Làm cùng bước FC-1 |
| Rủi ro | `fhir-server` hiện không có `Guard` cho đường dẫn ngoài `/fhir/R4` | FC-4 mở rộng `Guard` |
| Rủi ro | Hub nhận tài nguyên trong ngữ cảnh mà **không kiểm** (PHI tuỳ ý, không xác thực đối với kho FHIR) | Giới hạn kích thước; không ghi vào kho FHIR; cân nhắc validate JSON là tài nguyên có `resourceType` hợp lệ |

## C.11 Các bước làm

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| FC-1 | `pkg/fhircast`: kiểu, `ParseEvents`, `ExtractEndpoint`, `EventCategory`, `ExtractAnchorType`, lọc thông điệp theo người nhận, xác nhận STU2/STU3, khung `Store` + bộ nhớ; sửa `architecture_test.go` | S | Ca 36, 38, 39, 40, 41, 42 (không Lua) xanh trên bộ nhớ | không |
| FC-2 | Adapter Redis cho `Store` (WATCH/MULTI) và `Bus` (Pub/Sub, một kết nối, đếm tham chiếu) | M | Ca 37, 40-42 xanh trên miniredis; test Redis thật bằng biến môi trường (như `redis_env_test.go`); F-7, F-10 | FC-1 |
| FC-3 | Máy trạng thái ngữ cảnh: `open`/`close`/`update`/khác, update bundle, CAS | M | Ca 28-35 ở mức pkg xanh; F-9 với `-race` | FC-1, Q-FC-7 |
| FC-4 | Lớp HTTP: Echo mỏng, `GuardFor`, `.well-known`, hai phiên bản và `/hub/`, đọc form và JSON, thông điệp lỗi đúng chuỗi | M | Ca 1-12, 14-27 xanh (HTTP); F-1, F-2, F-3 | FC-3, B-1 |
| FC-5 | Kênh `/ws/fhircast/{endpoint}`: kết nối, xác nhận, lọc, `denied` rồi đóng, heartbeat cục bộ, lease, giới hạn socket | M | Ca 43-58 xanh; F-4, F-5, F-6 | FC-2, FC-4, B-1 |
| FC-6 | Phát `hub.url`/`hub.topic` ở token (`auth`) | S | Test token: scope `fhircast/` thì có hai trường, không thì không; topic ổn định giữa hai lần xin trong 1 giờ | auth P3, FC-2, Q-FC-3 |
| FC-7 | E2E hai ứng dụng giả (đăng ký, mở bệnh nhân, nhận sự kiện, hủy), `-race`, hai instance chung Redis; tài liệu và `docs/api-contract` | S đến M | E2E xanh; tài liệu nêu sai khác C.9 | FC-5, FC-6 |

---

# D. Hạng mục dùng chung và thứ tự toàn cục

| Thứ | Việc | Chặn |
|---|---|---|
| 1 | B-1, B-2 (`pkg/wsgroup`, refactor `fhirsub.Hub`) | không |
| 2 | B-3 (kiểm quyền người nhận, `unbind`, hạn mức): vá lỗ hổng đang có | B-2 |
| 3 | Chốt Q-SC-1 (Basic cho SCIM) cùng Q14 của P7 và D-10 | quyết định, không phải code |
| 4 | SC-1, SC-2 (không chặn bởi P3); SC-3 trở đi chờ P3 và P7.1 | P3, P7 |
| 5 | FC-1 đến FC-5 (cần `pkg/wsgroup`; FC-6 chờ P3) | B-1 |
| 6 | Cập nhật `docs/plans/modules-survey.md` mục 2 (đính chính "đóng êm 30 giây" và "FHIRcast không có callback") và `coverage-matrix.md` (B thành A cho ba dòng này) | sau khi duyệt tài liệu này |

Điều khiển chất lượng chung: mọi bước có test `-race`; `architecture_test.go` xanh; không gọi mạng thật trong test; SonarQube theo chuẩn của repo (chưa kiểm cấu hình cụ thể ở đây).
