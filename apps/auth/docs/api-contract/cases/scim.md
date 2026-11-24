# auth: các trường hợp phản hồi của nhóm `scim`

SCIM 2.0 cho tài nguyên `Users` (6 route, `/scim/v2/Users`): IdP (Okta, Azure AD...) đồng bộ user vào **một project**. `id` của SCIM là **id ProjectMembership**. Không có `Groups`, `Bulk`, `ServiceProviderConfig`. Header, envelope SCIM Error, hạn mức, mã HTTP chung: [../common.md](../common.md) (mục 5 cho SCIM Error). Bảng trường từng route: [../reference/scim.md](../reference/scim.md).

## Điều kiện bật

| Điều kiện | Cấu hình | Khi thiếu |
|---|---|---|
| Công tắc toàn cục | `AUTH_SCIM_ENABLED=true` (mặc định tắt) | mọi route `404` |
| Tính năng của project | `scim` trong `Project.features` (`auth project feature -project <id> -feature scim`, hoặc `AUTH_DEFAULT_PROJECT_FEATURES=scim` lúc seed) | `403` `SCIM is not enabled for this project` |
| Người gọi là admin hoạt động của project | `membership.admin=true` | `403` `Forbidden` |
| Basic | `AUTH_SCIM_ALLOW_BASIC` (mặc định `true`) | mọi `Authorization: Basic` là `401` |
| Danh sách IP | `AUTH_SCIM_ALLOWED_IPS` (CIDR, phân cách dấu phẩy; rỗng: không hạn chế) | IP ngoài danh sách: `403` `Forbidden` |
| Ở staging/production | bật SCIM với Basic **bắt buộc** có `AUTH_SCIM_ALLOWED_IPS` (không thì tiến trình không khởi động), hoặc đặt `AUTH_SCIM_ALLOW_BASIC=false` | lỗi cấu hình lúc khởi động |
| Giới hạn | `AUTH_SCIM_MAX_PAGE` (100), `AUTH_SCIM_MAX_PATCH_OPS` (50), `AUTH_SCIM_MAX_BODY_BYTES` (1 MiB) | xem từng mục |

## Xác thực

| Cách | Header | Quy tắc | Thất bại |
|---|---|---|---|
| Bearer | `Authorization: Bearer <access_token>` | access token của người dùng hoặc `client_credentials`; token **có trạng thái** (login thu hồi, membership tắt/xóa, project tắt: `401`) | `401` |
| Basic | `Authorization: Basic base64(clientId:secret)` | cần `AUTH_SCIM_ALLOW_BASIC=true`; tách ở dấu `:` đầu tiên; xác thực client; membership của client và project phải hoạt động | `401` chung `Unauthorized` (sai id, sai secret, base64 hỏng, thiếu `:`, id hoặc secret rỗng, Basic bị tắt) |
| Không có | | | `401` |

Thứ tự trước khi vào handler (mọi route): **IP guard** (`403` `Forbidden` nếu có danh sách và IP ngoài; IP lấy từ kết nối, `X-Forwarded-For` bị bỏ qua trừ `AUTH_TRUSTED_PROXIES`) → hạn mức bước đăng nhập `AUTH_LOGIN_RATE_LIMIT` (`429`; chung với đăng nhập để không dò được secret client qua Basic) → xác thực (`401`) → (POST/PUT/PATCH) kiểm `Content-Type` (`415`). Sau đó use case kiểm admin (`403` `Forbidden`) rồi tính năng `scim` (`403` `SCIM is not enabled for this project`).

`401` luôn kèm **hai** dòng `WWW-Authenticate`: `Basic realm="scim", charset="UTF-8"` và `Bearer realm="scim"`. Body: SCIM Error `status` `"401"`, `detail` `Unauthorized`.

## SCIM Error và ánh xạ

`Content-Type: application/scim+json; charset=utf-8`, `status` là chuỗi, `scimType` chỉ có khi mã đặt. Lỗi nghiệp vụ dùng chung với `admin` được ánh xạ: `400` thành `scimType` `invalidValue`; `409` thành `uniqueness`; `401`, `403`, `404`, `429` không có `scimType`; lỗi nội bộ: `500` `detail` `Internal server error`.

Lỗi do khung (không phải SCIM Error): `404` khi `AUTH_SCIM_ENABLED` tắt, `413` khi body vượt **1 MiB toàn cục**, `429` (cả hạn mức IP lẫn bước đăng nhập) là JSON `{"error":"..."}` (suy ra từ `errorHandler`, chưa kiểm bằng test; xem "Phát hiện lệch" trong [admin.md](admin.md)).

## Ánh xạ trường

| Trường SCIM | POST | PUT | PATCH |
|---|---|---|---|
| `userName` | bắt buộc, lưu vào `membership.userName` (không kiểm duy nhất) | **bỏ qua** | bỏ qua (`userName` được nhận) |
| `userType` | `Patient`/`Practitioner`/`RelatedPerson`; thiếu hoặc lạ (kể cả `Bot`) là `Practitioner` | bỏ qua | bỏ qua |
| `name.givenName`, `name.familyName` | ghi `firstName`, `lastName` (**bắt buộc ngầm**: thiếu thì `First name is required` / `Last name is required`) | ghi `User.firstName/lastName` nếu `name` có mặt (User thuộc project); vắng thì giữ | `name`, `name.givenName`, `name.familyName` |
| `emails[0].value` | bắt buộc | ghi nếu có và không rỗng; vắng thì giữ; chỉ phần tử đầu | `emails` (thay nguyên mảng) |
| `externalId` | ghi vào membership (và User thuộc project) | ghi; **vắng nghĩa là xóa** | `externalId`; `remove` xóa |
| `active` | ghi `membership.active` (vắng: hoạt động) | ghi nếu có; vắng thì giữ | `active` (không `remove` được) |
| `id`, `meta`, `schemas`, `groups`, `roles`, `admin`, `accessPolicy`, phần mở rộng | bỏ qua (không thể nâng quyền bằng body) | `id` có và khác đường dẫn: `400` | xem bảng path |
| `displayName`, `nickName`, `profileUrl`, `title`, `preferredLanguage`, `locale`, `timezone`, `phoneNumbers`, `addresses`, `ims`, `photos`, `name.formatted`, `name.middleName`, `name.honorificPrefix`, `name.honorificSuffix` | bỏ qua | bỏ qua | **nhận và bỏ qua** (không lỗi) |

**Path của PATCH** (không phân biệt hoa thường, cắt khoảng trắng):

| Path | `add`/`replace` | `remove` |
|---|---|---|
| `active` | boolean, không thì `400` `invalidValue` | `400` `mutability` |
| `externalId` | chuỗi | đặt thành rỗng (xóa) |
| `name` | đối tượng `{givenName, familyName}` thay nguyên | `name` rỗng (xóa họ tên) |
| `name.givenName`, `name.familyName` | chuỗi | đặt thành rỗng |
| `emails` | mảng không rỗng, phần tử đầu có `value` | `400` `mutability` |
| thuộc tính "bỏ qua" ở bảng trên, `userName`, `userType` | không làm gì | không làm gì |
| path khác (`admin`, `accessPolicy`, `profile`, `project`, `user`, `groups`, `meta`, `id`, `emails[type eq "work"].value`...) | `400` `invalidPath` | `400` `invalidPath` |
| không có `path` | `value` phải là đối tượng, mỗi khóa xử lý như một path | `400` `noTarget` |

Mỗi thao tác áp lên bản sao theo thứ tự; lỗi ở bất kỳ thao tác nào hủy cả yêu cầu (không ghi gì).

## Đổi email, `active`, và bảo vệ (dùng chung PUT và PATCH)

Thứ tự trong bước ghi (`scimApply`): email hợp lệ → quyền đổi email → trùng email → trùng `externalId` → bảo vệ `active=false` → **một transaction**.

| Hành vi | Quy tắc |
|---|---|
| Đổi email (so sánh không phân biệt hoa thường) | User phải **thuộc project** (`User.project` = project). Sau đổi: `emailVerified=false`, **vô hiệu mọi security request** của user, **thu hồi mọi login** của user, tất cả **cùng một transaction** (lỗi giữa chừng hoàn tác hết) |
| User phạm vi server | đổi email là `403` `Cannot change the email of a user outside this project`; các trường của User bị bỏ qua (tên, externalId của User); `active` và `externalId` của **membership** vẫn ghi |
| `active=false` | không với chính người gọi, không với chủ project, không với admin hoạt động cuối cùng (đều `400` `invalidValue`). Tắt xong token của user bị từ chối ngay (`401`) nhưng login **không** bị thu hồi |
| `active=true` trên membership đang tắt | được chấp nhận (IdP là nguồn sự thật), ghi log kiểm toán `audit=true` |
| Xung đột ghi đồng thời | `409` `uniqueness` `Resource was modified concurrently, retry` |
| Không có gì thay đổi | vẫn `200`, không ghi |

---

<a id="get-scim-v2-users"></a>
## `GET /scim/v2/Users`

Liệt kê user (membership Patient/Practitioner/RelatedPerson có User) của project, có lọc `userName eq` và phân trang. [Tham chiếu trường](../reference/scim.md#get-scim-v2-users)

**Cờ bật:** `AUTH_SCIM_ENABLED` + tính năng `scim` của project.

**Thứ tự kiểm tra:** IP guard (403) → hạn mức (429) → xác thực (401) → parse `startIndex`, `count` (400 nếu không phải số nguyên; **xảy ra trước kiểm quyền**) → admin (403) → tính năng `scim` (403) → parse `filter` (400) → đọc và lọc → sắp theo `id` → phân trang. Không ghi.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | | 200 | `Content-Type: application/scim+json` | ListResponse: `totalResults` = tổng thật khớp filter, `startIndex`, `itemsPerPage` = số phần tử trang này, `Resources` | không |
| C2 | Phân trang | `startIndex` < 1 coi là 1; `count` < 0 coi là 0; `count` > `AUTH_SCIM_MAX_PAGE` bị cắt về trần; không gửi `count` dùng trần; `startIndex` vượt tổng cho `Resources: []` | 200 | - | như C1 (`totalResults` không đổi) | không |
| C3 | Lọc | `filter=userName eq "x"` (không phân biệt hoa thường ở tên thuộc tính, toán tử, giá trị; `userName` là giá trị đã lưu, không có thì id hồ sơ) | 200 | - | như C1 | không |
| C4 | Filter không hỗ trợ | dạng khác | 400 | - | SCIM Error `invalidFilter`: `Unsupported filter: only \`userName eq "value"\` is supported` | không |
| C5 | Filter giá trị sai | | 400 | - | `invalidFilter`: `Unsupported filter: value must be a quoted string` / `Unsupported filter: unescaped quote in value` / `Unsupported filter: invalid escape in value` / `Unsupported filter: empty value` | không |
| C6 | Tham số không phải số | `startIndex=x`, `count=abc` | 400 | - | `invalidValue`: `startIndex must be an integer` / `count must be an integer` | không |
| C7 | Chưa xác thực | xem mục Xác thực | 401 | hai dòng `WWW-Authenticate` | `Unauthorized` | không |
| C8 | IP ngoài danh sách | `AUTH_SCIM_ALLOWED_IPS` | 403 | - | `Forbidden` | không |
| C9 | Không phải admin | | 403 | - | `Forbidden` | không |
| C10 | Project chưa bật SCIM | thiếu feature `scim` | 403 | - | `SCIM is not enabled for this project` | không |
| C11 | Quá hạn mức | | 429 | `Retry-After: 1` | JSON `{"error":"Too many requests"}` (chưa kiểm body) | không |
| C12 | Tắt toàn cục | | 404 | - | JSON `{"error":"Not Found"}` (chưa kiểm body) | không |

**Ví dụ**

```http
GET /scim/v2/Users?filter=userName%20eq%20%22an.nguyen%40example.org%22&startIndex=1&count=20 HTTP/1.1
Authorization: Basic MDE5MmYzYjA6czNjcjN0
```

```http
HTTP/1.1 200 OK
Content-Type: application/scim+json; charset=utf-8

{
  "schemas": ["urn:ietf:params:scim:api:messages:2.0:ListResponse"],
  "totalResults": 1, "itemsPerPage": 1, "startIndex": 1,
  "Resources": [ { "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"], "id": "0192f3c0-2222-7000-8000-0000000000aa", "userName": "an.nguyen@example.org", "userType": "Practitioner",
    "name": { "givenName": "An", "familyName": "Nguyễn" }, "emails": [ { "value": "an.nguyen@example.org" } ], "active": true,
    "meta": { "resourceType": "User", "version": "2026-11-23T08:30:00Z", "lastModified": "2026-11-23T08:30:00Z", "location": "https://auth.example.org/scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa" } } ]
}
```

SCIM Error (cấu trúc dùng cho mọi lỗi của nhóm):

```http
HTTP/1.1 400 Bad Request
Content-Type: application/scim+json; charset=utf-8

{ "schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"], "status": "400", "scimType": "invalidFilter", "detail": "Unsupported filter: only `userName eq \"value\"` is supported" }
```

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/scim+json; charset=utf-8
WWW-Authenticate: Basic realm="scim", charset="UTF-8"
WWW-Authenticate: Bearer realm="scim"

{ "schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"], "status": "401", "detail": "Unauthorized" }
```

**Lưu ý tích hợp:** `Resources` luôn có mặt (mảng rỗng khi hết). Membership của Bot/ClientApplication không bao giờ xuất hiện. `totalResults` có thể lớn hơn số phần tử có thể liệt kê nếu một User đã mất (bị bỏ qua khi đọc). Danh sách đọc toàn bộ membership của project rồi lọc trong bộ nhớ: project rất lớn nên dùng `filter`.

---

<a id="post-scim-v2-users"></a>
## `POST /scim/v2/Users`

Tạo user: đi **cùng đường với `invite`** của admin nhưng không gửi thư. [Tham chiếu trường](../reference/scim.md#post-scim-v2-users)

**Cờ bật:** `AUTH_SCIM_ENABLED` + tính năng `scim`; Practitioner chịu `AUTH_BLOCKED_EMAIL_DOMAINS` và `allowedPractitionerEmailDomain`.

**Thứ tự kiểm tra:** IP guard → hạn mức → xác thực → `Content-Type` (415) → đọc body có giới hạn `AUTH_SCIM_MAX_BODY_BYTES` (413, 400) → admin (403) → tính năng `scim` (403) → `emails[0].value` (400) → `userName` (400) → chính sách mặc định theo `userType` (400) → các bước của `invite` (kiểm đầu vào, miền email, policy, **transaction** tạo User/hồ sơ/membership) → đọc lại User, dựng phản hồi. Mật khẩu ngẫu nhiên, không gửi thư.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | | 201 | `Location: <AUTH_BASE_URL>/scim/v2/Users/<id>`; `Content-Type: application/scim+json` | User SCIM (`id` = id membership) | tạo User (phạm vi: Patient hoặc có `externalId` là project, **Practitioner không có `externalId` là server**), hồ sơ, membership (`userName`, `invitedBy` = người gọi, `admin` không đặt, `active` theo body); một transaction; không thư |
| C2 | Content-Type sai | không phải `application/json` hay `application/scim+json` (kể cả thiếu) | 415 | - | `Content-Type must be application/scim+json or application/json` | không |
| C3 | Body quá lớn | vượt `AUTH_SCIM_MAX_BODY_BYTES` | 413 | - | SCIM Error `Request body too large` (không `scimType`). Nếu giới hạn cấu hình lớn hơn 1 MiB thì giới hạn toàn cục 1 MiB ra trước, dạng JSON `{"error":"Request body too large"}` | không |
| C4 | JSON hỏng | | 400 | - | `invalidSyntax`: `Content could not be parsed` | không |
| C5 | Thiếu email | `emails` vắng, rỗng, phần tử đầu rỗng | 400 | - | `invalidValue`: `emails[0].value is required` | không |
| C6 | Thiếu userName | rỗng hoặc chỉ khoảng trắng | 400 | - | `invalidValue`: `userName is required` | không |
| C7 | Thiếu chính sách mặc định | `userType` Patient mà project không có `defaultPatientAccessPolicy`; Practitioner mà không có policy mặc định | 400 | - | `invalidValue`: `Missing default patient access policy` / `Missing default practitioner access policy` | không |
| C8 | Thiếu tên | `name.givenName` hoặc `name.familyName` trống | 400 | - | `invalidValue`: `First name is required` / `Last name is required` | không |
| C9 | Email sai | | 400 | - | `invalidValue`: `Valid email address is required` | không |
| C10 | Miền email bị chặn | Practitioner | 400 | - | `invalidValue`: `Email domain is not allowed for this project` | không |
| C11 | RelatedPerson | `userType` = RelatedPerson (không có `patient` để gửi) | 400 | - | `invalidValue`: `Patient is required to create a RelatedPerson` | không |
| C12 | Đã là member | cùng email đã là member project | 409 | - | `uniqueness`: `User is already a member of this project` | không |
| C13 | externalId trùng | | 409 | - | `uniqueness`: `externalId already exists in this project` | không |
| C14 | Chưa xác thực / IP / quyền / feature / hạn mức / tắt | | 401 / 403 / 403 / 403 / 429 / 404 | như GET list | như GET list | không |

Mật khẩu là ngẫu nhiên nên không có nhánh kiểm pwned.

**Ví dụ**

```http
POST /scim/v2/Users HTTP/1.1
Authorization: Bearer eyJhbGciOi...
Content-Type: application/scim+json

{
  "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
  "userName": "an.nguyen@example.org",
  "name": { "givenName": "An", "familyName": "Nguyễn" },
  "emails": [ { "primary": true, "value": "an.nguyen@example.org", "type": "work" } ],
  "externalId": "hr-10234", "active": true,
  "groups": [ { "value": "g" } ], "admin": true
}
```

```http
HTTP/1.1 201 Created
Content-Type: application/scim+json; charset=utf-8
Location: https://auth.example.org/scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa

{ "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"], "id": "0192f3c0-2222-7000-8000-0000000000aa", "userName": "an.nguyen@example.org", "userType": "Practitioner",
  "name": { "givenName": "An", "familyName": "Nguyễn" }, "externalId": "hr-10234", "emails": [ { "value": "an.nguyen@example.org" } ], "active": true,
  "meta": { "resourceType": "User", "version": "2026-11-23T08:30:00Z", "lastModified": "2026-11-23T08:30:00Z", "location": "https://auth.example.org/scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa" } }
```

```http
HTTP/1.1 409 Conflict
Content-Type: application/scim+json; charset=utf-8

{ "schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"], "status": "409", "scimType": "uniqueness", "detail": "User is already a member of this project" }
```

**Lưu ý tích hợp:** `groups`, `admin`, `roles` bị bỏ qua: quyền admin chỉ cấp qua `POST /admin/projects/{id}/members/{mid}`. Practitioner không `externalId` là User **phạm vi server**, sau này đổi email qua PUT/PATCH sẽ `403`; gửi `externalId` để User thuộc project. Nếu email đã có User ở phạm vi đó (ở project khác), User được dùng lại (không đổi mật khẩu). `userName` không được kiểm duy nhất; `userType` chỉ có tác dụng lúc tạo.

---

<a id="get-scim-v2-users-id"></a>
## `GET /scim/v2/Users/{id}`

Đọc một user theo id membership. [Tham chiếu trường](../reference/scim.md#get-scim-v2-users-id)

**Thứ tự kiểm tra:** IP guard → hạn mức → xác thực → admin (403) → tính năng `scim` (403) → đọc membership và User: không có, ngoài project, thuộc Bot/ClientApplication (hồ sơ không phải Patient/Practitioner/RelatedPerson), không có User: **404 chung**.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | | 200 | `Content-Type: application/scim+json` | User SCIM | không |
| C2 | Không tìm thấy | id không tồn tại / project khác / Bot / Client | 404 | - | SCIM Error `Not found` (không `scimType`) | không |
| C3 | Chưa xác thực / IP / quyền / feature / hạn mức / tắt | | 401 / 403 / 403 / 403 / 429 / 404 | như GET list | như GET list | không |

**Ví dụ**

```http
GET /scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa HTTP/1.1
Authorization: Bearer eyJhbGciOi...
```

```http
HTTP/1.1 404 Not Found
Content-Type: application/scim+json; charset=utf-8

{ "schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"], "status": "404", "detail": "Not found" }
```

Thành công cùng cấu trúc với phần tử của `Resources` ở danh sách (hoặc ví dụ 201 của POST).

**Lưu ý tích hợp:** khác Medplum, id ngoài project là `404` (không phải `403`). `meta.location` = `<AUTH_BASE_URL>/scim/v2/Users/<id>`; `meta.version` là thời điểm cập nhật membership, không phải ETag.

---

<a id="put-scim-v2-users-id"></a>
## `PUT /scim/v2/Users/{id}`

Thay user bằng id ở đường dẫn: chỉ ghi `name`, `externalId`, `emails[0]`, `active`. [Tham chiếu trường](../reference/scim.md#put-scim-v2-users-id)

**Thứ tự kiểm tra:** IP guard → hạn mức → xác thực → `Content-Type` (415) → đọc body (413/400) → admin (403) → tính năng `scim` (403) → `id` trong body khớp đường dẫn (400) → membership (404) → các bước `scimApply` (mục "Đổi email, active, và bảo vệ") → **một transaction** ghi User (nếu thuộc project) và membership; đổi email thì thêm vô hiệu security request và thu hồi login.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | | 200 | `Content-Type: application/scim+json` | User SCIM sau khi ghi | cập nhật `name`, `externalId`, `emails[0]` (User thuộc project), `active`, `externalId` của membership. Đổi email: `emailVerified=false`, vô hiệu mọi security request, thu hồi mọi login |
| C2 | Không đổi gì | body trùng trạng thái hiện tại | 200 | - | User SCIM | không ghi |
| C3 | User phạm vi server | đổi email | 403 | - | `Cannot change the email of a user outside this project` | không |
| C4 | `id` lệch | `id` trong body có và khác đường dẫn | 400 | - | `invalidValue`: `id in body does not match id in path` | không |
| C5 | Email sai | `emails[0].value` không hợp lệ | 400 | - | `invalidValue`: `emails[0].value is not a valid email address` | không |
| C6 | Email trùng | email mới đã có User khác trong project | 409 | - | `uniqueness`: `Email already registered` | không |
| C7 | externalId trùng | | 409 | - | `uniqueness`: `externalId already exists in this project` | không |
| C8 | Không được tắt | `active=false` với chính người gọi / chủ project / admin cuối | 400 | - | `invalidValue`: `Cannot deactivate the membership of the caller` / `Cannot deactivate the owner of the project` / `Cannot remove the last administrator of the project` | không |
| C9 | Ghi đồng thời | | 409 | - | `uniqueness`: `Resource was modified concurrently, retry` | hoàn tác |
| C10 | Không tìm thấy | | 404 | - | `Not found` | không |
| C11 | Content-Type / body / quyền / feature / xác thực / hạn mức / tắt | | 415 / 413 / 400 / 403 / 403 / 401 / 429 / 404 | như POST | như POST | không |

**Ví dụ**

```http
PUT /scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa HTTP/1.1
Authorization: Bearer eyJhbGciOi...
Content-Type: application/scim+json

{ "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"], "userName": "an.nguyen@example.org", "name": { "givenName": "An", "familyName": "Nguyễn" },
  "emails": [ { "value": "an.moi@example.org" } ], "externalId": "hr-10234", "active": true }
```

```http
HTTP/1.1 200 OK
Content-Type: application/scim+json; charset=utf-8

{ "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"], "id": "0192f3c0-2222-7000-8000-0000000000aa", "userName": "an.nguyen@example.org", "userType": "Practitioner",
  "name": { "givenName": "An", "familyName": "Nguyễn" }, "externalId": "hr-10234", "emails": [ { "value": "an.moi@example.org" } ], "active": true,
  "meta": { "resourceType": "User", "version": "2026-11-23T09:00:00Z", "lastModified": "2026-11-23T09:00:00Z", "location": "https://auth.example.org/scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa" } }
```

**Lưu ý tích hợp:** `userName` và `userType` không đổi được (bỏ qua, không lỗi). **Thiếu `externalId` trong body là xóa** `externalId` cũ. Đổi email đăng xuất mọi phiên của user: IdP nên đổi email có chủ ý. PUT không có `emails` thì giữ email cũ; không có `active` thì giữ trạng thái.

---

<a id="patch-scim-v2-users-id"></a>
## `PATCH /scim/v2/Users/{id}`

Sửa một phần theo PatchOp (`add`, `replace`, `remove`), ghi như PUT. [Tham chiếu trường](../reference/scim.md#patch-scim-v2-users-id)

**Thứ tự kiểm tra:** IP guard → hạn mức → xác thực → `Content-Type` (415) → đọc body (413/400) → admin (403) → tính năng `scim` (403) → membership (404, **trước** kiểm patch) → `ApplyPatch` (400) → `scimApply` và **transaction** như PUT.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | | 200 | `Content-Type: application/scim+json` | User SCIM sau khi ghi | như PUT (đổi email: `emailVerified=false`, vô hiệu security request, thu hồi login, một transaction) |
| C2 | Thiếu schema | `schemas[0]` không phải `urn:ietf:params:scim:api:messages:2.0:PatchOp` | 400 | - | `invalidSyntax`: `Invalid SCIM patch: missing required schema` | không |
| C3 | Quá nhiều thao tác | hơn `AUTH_SCIM_MAX_PATCH_OPS` | 400 | - | `tooMany`: `Invalid SCIM patch: too many operations (max <N>)` | không |
| C4 | `op` lạ | khác add/remove/replace | 400 | - | `invalidSyntax`: `Invalid SCIM patch: unsupported operation` | không |
| C5 | Path bắt đầu bằng `/` | | 400 | - | `invalidPath`: `Invalid SCIM patch: path must not start with "/"` | không |
| C6 | Path không hỗ trợ | xem bảng path | 400 | - | `invalidPath`: `Invalid SCIM patch: unsupported path "<path>"` | không |
| C7 | `remove` không path | | 400 | - | `noTarget`: `Invalid SCIM patch: missing required path` | không |
| C8 | Không path, value không phải object | | 400 | - | `invalidValue`: `Invalid SCIM patch: value must be an object if path is missing` | không |
| C9 | Sai kiểu giá trị | | 400 | - | `invalidValue`: `Invalid SCIM patch: active must be a boolean` / `externalId must be a string` / `name must be an object` / `<path> must be a string` (`name.givenName`, `name.familyName`) / `emails must be a non-empty array with a value` | không |
| C10 | Không được `remove` | `active`, `emails` | 400 | - | `mutability`: `Invalid SCIM patch: active cannot be removed` / `Invalid SCIM patch: emails cannot be removed` | không |
| C11 | Lỗi ghi | như PUT: `403` đổi email User phạm vi server; `400` không được tắt; `409` email/externalId trùng, ghi đồng thời | 403 / 400 / 409 | - | như PUT | không |
| C12 | Không tìm thấy, 415, 413, 400 JSON, quyền, feature, xác thực, hạn mức, tắt | | 404 / 415 / 413 / 400 / 403 / 403 / 401 / 429 / 404 | như POST | như POST | không |

Mọi chuỗi `Invalid SCIM patch: ...` ở trên là nguyên văn của `domain/scim`.

**Ví dụ**

```http
PATCH /scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa HTTP/1.1
Authorization: Basic MDE5MmYzYjA6czNjcjN0
Content-Type: application/scim+json

{ "schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
  "Operations": [ { "op": "replace", "path": "active", "value": false }, { "op": "replace", "value": { "name.givenName": "Bình" } } ] }
```

```http
HTTP/1.1 200 OK
Content-Type: application/scim+json; charset=utf-8

{ "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"], "id": "0192f3c0-2222-7000-8000-0000000000aa", "userName": "an.nguyen@example.org", "userType": "Practitioner",
  "name": { "givenName": "Bình", "familyName": "Nguyễn" }, "emails": [ { "value": "an.nguyen@example.org" } ], "active": false,
  "meta": { "resourceType": "User", "version": "2026-11-23T09:10:00Z", "lastModified": "2026-11-23T09:10:00Z", "location": "https://auth.example.org/scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa" } }
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/scim+json; charset=utf-8

{ "schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"], "status": "400", "scimType": "invalidPath", "detail": "Invalid SCIM patch: unsupported path \"admin\"" }
```

**Lưu ý tích hợp:** kiểu Okta (không `path`, `value` là object) được hỗ trợ; `displayName`, `phoneNumbers`... nhận và bỏ qua nên IdP đẩy hồ sơ đầy đủ không lỗi. `add` trên thuộc tính đa trị **thay** cả mảng. `remove` `name` xóa cả họ lẫn tên. Khi `Operations` rỗng nhưng schema đúng: `200`, không đổi gì.

---

<a id="delete-scim-v2-users-id"></a>
## `DELETE /scim/v2/Users/{id}`

Xóa membership (và User nếu thuộc project mà hết membership). [Tham chiếu trường](../reference/scim.md#delete-scim-v2-users-id)

**Thứ tự kiểm tra:** IP guard → hạn mức → xác thực → admin (403) → tính năng `scim` (403) → membership (404) → không xóa membership của chính người gọi (400) → use case xóa member của `admin`: không xóa chủ project (400) → bảo vệ admin cuối cùng (400) → **transaction**: xóa membership, xóa User nếu `User.project` = project và hết membership. Không kiểm `Content-Type`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Thành công | | 204 | - | không có body | xóa membership; xóa User nếu thuộc project và hết membership (User phạm vi server hoặc dùng chung được giữ). Hồ sơ (Patient/Practitioner/RelatedPerson) **không** bị xóa. Token của user bị từ chối ngay |
| C2 | Chính người gọi | | 400 | - | `invalidValue`: `Cannot delete the membership of the caller` | không |
| C3 | Chủ project | | 400 | - | `invalidValue`: `Cannot delete the owner of the project` | không |
| C4 | Admin cuối cùng | | 400 | - | `invalidValue`: `Cannot remove the last administrator of the project` | không |
| C5 | Không tìm thấy | id không có (kể cả đã xóa) / project khác / Bot / Client | 404 | - | `Not found` | không |
| C6 | Xác thực / IP / quyền / feature / hạn mức / tắt | | 401 / 403 / 403 / 403 / 429 / 404 | như GET list | như GET list | không |

**Ví dụ**

```http
DELETE /scim/v2/Users/0192f3c0-2222-7000-8000-0000000000aa HTTP/1.1
Authorization: Bearer eyJhbGciOi...
```

```http
HTTP/1.1 204 No Content
```

**Lưu ý tích hợp:** xóa không idempotent: lần hai nhận `404`. Muốn tạm ngưng mà không mất dữ liệu hãy `PATCH active=false`.
