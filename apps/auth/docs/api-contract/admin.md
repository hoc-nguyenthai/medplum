# Admin API

> **Hợp đồng thật của `apps/auth` (P7, 2026-11-22)** nằm ngay dưới đây; phần sau dòng `---` là mô tả server Medplum TypeScript gốc để đối chiếu, KHÔNG phải hợp đồng. DTO chi tiết ở swagger của auth (`/swagger/index.html`, tag `admin`).

Nhóm `/admin/projects/*` mặc định **TẮT**, bật bằng `AUTH_ADMIN_ENABLED=true`. Không có `/admin/super/*` (hạ tầng, thuộc vận hành/CLI) và không có `bot`, `secrets` (không cho auth thuần). Quyết định đã duyệt (Q12 đến Q22):

- Xác thực: chỉ `Authorization: Bearer` của người dùng (KHÔNG Basic client). Thiếu hoặc sai là 401; người gọi phải là admin hoạt động của project (`membership.admin`), nếu không 403. Không có siêu quyền xuyên project: `:projectId` khác project của người gọi là 403.
- Membership không tồn tại hoặc thuộc project khác đều là **403 chung** (không cho dò id).
- `GET` project che giá trị `secret` và khóa bí mật trong `site` (`********`).
- Không xóa chủ project, không xóa/hạ quyền/vô hiệu hóa admin cuối cùng (400).
- Token của member bị xóa bị từ chối ngay (auth đọc membership mỗi lần gọi).

| Method và path | Việc | Lỗi chính |
|---|---|---|
| `GET /admin/projects/{projectId}` | `{project:{id,name,setting,secret(che),site(che)}}` | 401, 403 |
| `POST /admin/projects/{projectId}/settings` | body mảng `ProjectSetting`; chỉ allowlist `mfaRequired`, `allowedMfaMethods`, `allowedPractitionerEmailDomain` (nhiều mục), `appName`; thay các setting thuộc allowlist, GIỮ setting ngoài allowlist (ví dụ `allow-dangerous-redirect`) | 400 tên ngoài allowlist/sai kiểu/trùng |
| `POST /admin/projects/{projectId}/client` | tạo client + membership; `secret` tối thiểu 32 ký tự hoặc server sinh (64 hex); chỉ nhận `name`, `description`, `redirectUris`, `pkceOptional`, `grantType`, `secret`, `accessPolicy`; trường khác (ví dụ `identityProvider`, `meta`) là 400; `accessPolicy` phải thuộc project; trả 201 kèm `secret` một lần | 400 |
| `POST /admin/projects/{projectId}/sites` | body mảng `ProjectSite` (`name`, `domain[]`, `googleClientId`+`googleClientSecret`, `recaptchaSiteKey`+`recaptchaSecretKey`); thay toàn bộ `Project.site`; tối đa 20 site; mỗi cặp id/secret phải đi cùng nhau; `googleClientId` và `recaptchaSiteKey` phải **duy nhất giữa các project** (409, kiểm trong transaction giữ khóa theo giá trị) và `googleClientId` không được trùng Google client chính của máy chủ; phản hồi che secret, gửi lại `********` nghĩa là giữ secret hiện có của site cùng tên | 400, 403, 409 |
| `POST /admin/projects/{projectId}/invite` | mời Patient/Practitioner/RelatedPerson (xem dưới) | 400, 403, 409 |
| `GET/POST/DELETE /admin/projects/{projectId}/members/{membershipId}` | đọc; sửa chỉ `admin`, `active`, `externalId`, `accessPolicy`, `access` (đổi `user`/`profile`/`project` là 400; `id`/`resourceType` lệch là 403; policy ngoài project 400; `externalId` trùng 409); xóa (user thuộc project hết membership thì bị xóa theo) | 400, 403, 409 |
| `POST /admin/projects/{projectId}/members/{membershipId}/resetpassword` | gửi thư đặt lại mật khẩu; chỉ user thuộc project (user phạm vi server là 403); thiếu email/chưa cấu hình thư 400, gửi lỗi 500 | 400, 403 |
| `POST /admin/projects/{projectId}/members/{membershipId}/mfa/reset` | gỡ yếu tố MFA `method` (`totp` mặc định, hoặc `email`); gỡ totp thì đổi secret; chỉ user thuộc project | 400, 403 |
| `POST /admin/projects/setpassword` | `{email,password}`; chỉ user thuộc project (user phạm vi server: 400 `User not found`); kiểm pwned; thu hồi mọi login | 400, 403 |

`invite` (DTO chặt, trường lạ là 400; bỏ `accessPolicy`/`access`/`admin` ở mức gốc đã deprecated, dùng `membership.*`): `resourceType`, `firstName`, `lastName`, một trong `email`/`externalId`, `patient` (RelatedPerson), `password` (không có thì ngẫu nhiên, người dùng đặt qua link trong thư), `scope` (`project`|`server`; Patient mặc định project, còn lại server; `externalId` luôn project), `sendEmail`, `membership{profile,admin,active,externalId,accessPolicy,access}`, `upsert`, `forceNewMembership`, `mfaRequired`. Practitioner có email bị chặn theo `AUTH_BLOCKED_EMAIL_DOMAINS` và setting `allowedPractitionerEmailDomain`. Ghi user, hồ sơ, membership trong MỘT transaction; thư gửi sau. Chưa cấu hình thư mà không `sendEmail:false` là 400 trước khi ghi. Gửi thư lỗi: 400 kèm id membership (dữ liệu đã tạo; dùng `resetpassword` để gửi lại). Mặc định access policy lấy từ `Project.defaultPatientAccessPolicy`/`defaultAccessPolicies` (Admin cho Practitioner có `admin`). Khác Medplum: không `conditionalCreate` hồ sơ theo email, không có `Project.link`.

---

> **Trạng thái: CHƯA CÀI trong fhir-server Go.** Tài liệu này mô tả server Medplum bản TypeScript gốc, chưa phải hợp đồng của server đang chạy. Route Go thật xem `fhir-rest.md` và `/swagger/index.html`. Không viết DTO cho các route dưới đây cho tới khi chúng được port sang Go.

Gốc: `/admin/`. Nguồn: `server/src/admin/`. Project admin cần quyền admin của project; super admin cần cờ `superAdmin`.

| Method | Path | Xác thực |
|---|---|---|
| GET | `/admin/projects/:projectId` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/bot` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/client` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/invite` | không guard ở route (xem middleware cha) |
| DELETE | `/admin/projects/:projectId/members/:membershipId` | không guard ở route (xem middleware cha) |
| GET | `/admin/projects/:projectId/members/:membershipId` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/members/:membershipId` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/members/:membershipId/mfa/reset` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/members/:membershipId/resetpassword` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/secrets` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/settings` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/:projectId/sites` | không guard ở route (xem middleware cha) |
| POST | `/admin/projects/setpassword` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/drop-invalid-indexes` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/lambda-cleaner` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/migrate` | không guard ở route (xem middleware cha) |
| GET | `/admin/super/migrations` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/purge` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/rebuild-index` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/rebuildprojectid` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/reconcile-db-schema-drift` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/reindex` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/reloadcron` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/removebotidjobsfromqueue` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/searchparameters` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/setdataversion` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/setpassword` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/setschemaversion` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/structuredefinitions` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/tablesettings` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/vacuum` | không guard ở route (xem middleware cha) |
| POST | `/admin/super/valuesets` | không guard ở route (xem middleware cha) |

Nhóm: mời user (`invite`), tạo `bot` và `client`, quản lý `members`, `settings`, `secrets`, `sites`; super admin: `reindex`, `rebuild-index`, `purge`, `vacuum`, `migrate`, `setpassword`, `tablesettings`, `reloadcron`, `valuesets`, `structuredefinitions`, `searchparameters`.

---

## SCIM 2.0 (`/scim/v2/Users`)

Hợp đồng thật của `apps/auth` (P7). Mặc định **TẮT**: cần cả `AUTH_SCIM_ENABLED=true` và tính năng `scim` của project (`auth project feature -project <id> -feature scim`, hoặc `AUTH_DEFAULT_PROJECT_FEATURES=scim` lúc seed); thiếu cái nào cũng không dùng được (route 404, hoặc 403 `SCIM is not enabled for this project`). Chỉ tài nguyên `Users`, không có `Groups`, `Bulk`, `ServiceProviderConfig`. DTO ở swagger (tag `scim`).

- Xác thực: `Authorization: Bearer` (access token của người dùng hoặc `client_credentials`) **hoặc** `Basic base64(clientId:secret)` của ClientApplication (Okta chỉ có Basic; tắt bằng `AUTH_SCIM_ALLOW_BASIC=false`). Người gọi phải có `membership.admin` (client phải được nâng admin, ví dụ qua `POST /admin/projects/{id}/members/{mid}`). Ở staging/production, Basic bắt buộc kèm `AUTH_SCIM_ALLOWED_IPS` (CIDR của IdP); danh sách này áp cho mọi lời gọi `/scim/v2`.
- Mọi lỗi (kể cả 401, 403) là SCIM Error `{schemas:[...Error], status:"<mã chuỗi>", scimType?, detail}`, Content-Type `application/scim+json`; 401 kèm `WWW-Authenticate: Basic` và `Bearer`. Nhận `application/scim+json` hoặc `application/json` (loại khác 415); body tối đa `AUTH_SCIM_MAX_BODY_BYTES` (413). Lỗi nội bộ trả chung chung. Hạn mức chung với bước đăng nhập (`AUTH_LOGIN_RATE_LIMIT`) nên không dò được secret của client.
- `id` là id của ProjectMembership. Membership ngoài project, thuộc Bot/ClientApplication hay không tồn tại đều **404 `Not found`**.

| Route | Hành vi |
|---|---|
| `GET /Users` | `ListResponse`; `filter` chỉ `userName eq "x"` (không phân biệt hoa thường; dạng khác 400 `invalidFilter`); `startIndex` (từ 1), `count` (mặc định và trần `AUTH_SCIM_MAX_PAGE`), `totalResults` là tổng thật, sắp theo id |
| `POST /Users` | 201 + `Location`. Chỉ dùng `userName` (bắt buộc), `name`, `emails[0]` (bắt buộc), `externalId`, `userType` (Patient/Practitioner/RelatedPerson, mặc định Practitioner), `active`; mọi thứ khác bị bỏ qua. Patient và Practitioner cần chính sách mặc định của project (400 `Missing default patient/practitioner access policy`). RelatedPerson không tạo mới được (cần patient). Đi qua cùng đường `AdminInvite` (không gửi thư): 409 nếu đã là member hoặc `externalId` trùng, 400 nếu miền email bị chặn |
| `GET /Users/{id}` | 200 |
| `PUT /Users/{id}` | dùng id đường dẫn (id body khác là 400). Chỉ ghi `name`, `externalId` (kể cả xóa khi vắng), `emails[0]`, `active`. User thuộc project: đổi email thì `emailVerified=false`, vô hiệu mọi security request, thu hồi mọi login, cùng một transaction. User ở phạm vi server: đổi email 403, các trường khác bị bỏ qua; `active` vẫn ghi vào membership. `userName` không đổi được sau khi tạo |
| `PATCH /Users/{id}` | `add`/`replace`/`remove`; chỉ `active`, `externalId`, `name`, `name.givenName`, `name.familyName`, `emails`; `userName`, `userType`, `displayName`, `phoneNumbers`... được nhận và bỏ qua; đường dẫn khác (`admin`, `accessPolicy`, `profile`, `project`, `user`, `groups`...) là 400 `invalidPath`; tối đa `AUTH_SCIM_MAX_PATCH_OPS` thao tác; `add` trên thuộc tính đa trị thay nguyên |
| `DELETE /Users/{id}` | 204; dùng lại use case xóa member (User thuộc project mà hết membership thì bị xóa theo) |

Không bao giờ tắt hay xóa: chủ project, membership của chính người gọi, admin cuối cùng (400). Kích hoạt lại `active=true` cho membership đã tắt được chấp nhận (IdP là nguồn sự thật) và được ghi log kiểm toán (`audit=true`).

Khác Medplum (có chủ ý): id ngoài project là 404 (Medplum 403), PUT dùng id đường dẫn, filter lạ bị từ chối, phân trang thật, mọi lỗi dạng SCIM Error, `meta.location` là `<AUTH_BASE_URL>/scim/v2/Users/<id>`, chặn Bot/Client/chủ/chính mình, Practitioner cần chính sách mặc định, đường dẫn PATCH lạ bị từ chối, không có `superAdmin`.
