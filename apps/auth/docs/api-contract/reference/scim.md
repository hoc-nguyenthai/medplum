# Tham chiếu API: nhóm `scim`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/scim.md](../cases/scim.md).

SCIM 2.0 /scim/v2/Users cho IdP (tắt mặc định, AUTH_SCIM_ENABLED và tính năng scim của project). Lỗi theo RFC 7644.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /scim/v2/Users`](#get-scim-v2-users) | SCIM: liệt kê user của project |
| [`POST /scim/v2/Users`](#post-scim-v2-users) | SCIM: tạo user |
| [`GET /scim/v2/Users/{id}`](#get-scim-v2-users-id) | SCIM: đọc một user |
| [`PUT /scim/v2/Users/{id}`](#put-scim-v2-users-id) | SCIM: thay user (PUT) |
| [`PATCH /scim/v2/Users/{id}`](#patch-scim-v2-users-id) | SCIM: sửa user (PATCH) |
| [`DELETE /scim/v2/Users/{id}`](#delete-scim-v2-users-id) | SCIM: xóa user |

---

<a id="get-scim-v2-users"></a>
## `GET /scim/v2/Users`

**SCIM: liệt kê user của project**

Chỉ admin của project (Bearer, hoặc Basic client:secret khi AUTH_SCIM_ALLOW_BASIC) và project có tính năng `scim`. filter chỉ hỗ trợ `userName eq "x"` (không phân biệt hoa thường), dạng khác là 400 invalidFilter. Phân trang thật: startIndex từ 1, count mặc định và trần là AUTH_SCIM_MAX_PAGE. Lỗi dạng SCIM Error.

Các trường hợp phản hồi: [../cases/scim.md#get-scim-v2-users](../cases/scim.md#get-scim-v2-users)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/scim+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `filter` | query | string | không | userName eq \ |
| `startIndex` | query | integer | không | bắt đầu từ 1 |
| `count` | query | integer | không | số kết quả |

**Phản hồi**

**`200`** OK

Schema: `scim.ListResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `Resources` | mảng `scim.User` | có |  | các User của trang này (mảng rỗng khi không có) |  |
| `Resources[].emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `Resources[].emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `Resources[].schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `Resources[].userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `Resources[].active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `Resources[].externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `Resources[].id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `Resources[].meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `Resources[].meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `Resources[].meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `Resources[].meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `Resources[].meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `Resources[].name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `Resources[].name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `Resources[].name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `Resources[].userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |
| `itemsPerPage` | integer | có |  | số bản ghi trong trang này | `20` |
| `schemas` | mảng string | có |  | luôn chứa urn:ietf:params:scim:api:messages:2.0:ListResponse | `["urn:ietf:params:scim:api:messages:2.0:ListResponse"]` |
| `startIndex` | integer | có |  | chỉ số bắt đầu của trang (tính từ 1) | `1` |
| `totalResults` | integer | có |  | tổng số bản ghi khớp filter (không phụ thuộc phân trang) | `42` |

```json
{
  "Resources": [
    {
      "active": true,
      "emails": [
        {
          "value": "an@example.org"
        }
      ],
      "externalId": "hr-10234",
      "id": "3f2a8c1e-0000-4000-8000-000000000000",
      "meta": {
        "lastModified": "2026-11-23T08:30:00Z",
        "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
        "resourceType": "User",
        "version": "2026-11-23T08:30:00Z"
      },
      "name": {
        "familyName": "Nguyễn",
        "givenName": "An"
      },
      "schemas": [
        "urn:ietf:params:scim:schemas:core:2.0:User"
      ],
      "userName": "an.nguyen@example.org",
      "userType": "Practitioner"
    }
  ],
  "itemsPerPage": 20,
  "schemas": [
    "urn:ietf:params:scim:api:messages:2.0:ListResponse"
  ],
  "startIndex": 1,
  "totalResults": 42
}
```

**`400`** Bad Request

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

**`401`** Unauthorized

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

**`403`** Forbidden

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

---

<a id="post-scim-v2-users"></a>
## `POST /scim/v2/Users`

**SCIM: tạo user**

Chỉ userName, name, emails[0], externalId, userType (Patient|Practitioner|RelatedPerson, mặc định Practitioner) và active được dùng; mọi trường khác (admin, accessPolicy, groups, roles, extension) bị bỏ qua. Patient và Practitioner phải có chính sách truy cập mặc định của project. 409 nếu đã là member hoặc externalId trùng. Không gửi thư.

Các trường hợp phản hồi: [../cases/scim.md#post-scim-v2-users](../cases/scim.md#post-scim-v2-users)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/scim+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (User)

Schema: `scim.User`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |

```json
{
  "active": true,
  "emails": [
    {
      "value": "an@example.org"
    }
  ],
  "externalId": "hr-10234",
  "id": "3f2a8c1e-0000-4000-8000-000000000000",
  "meta": {
    "lastModified": "2026-11-23T08:30:00Z",
    "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
    "resourceType": "User",
    "version": "2026-11-23T08:30:00Z"
  },
  "name": {
    "familyName": "Nguyễn",
    "givenName": "An"
  },
  "schemas": [
    "urn:ietf:params:scim:schemas:core:2.0:User"
  ],
  "userName": "an.nguyen@example.org",
  "userType": "Practitioner"
}
```

**Phản hồi**

**`201`** Created

Schema: `scim.User`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |

```json
{
  "active": true,
  "emails": [
    {
      "value": "an@example.org"
    }
  ],
  "externalId": "hr-10234",
  "id": "3f2a8c1e-0000-4000-8000-000000000000",
  "meta": {
    "lastModified": "2026-11-23T08:30:00Z",
    "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
    "resourceType": "User",
    "version": "2026-11-23T08:30:00Z"
  },
  "name": {
    "familyName": "Nguyễn",
    "givenName": "An"
  },
  "schemas": [
    "urn:ietf:params:scim:schemas:core:2.0:User"
  ],
  "userName": "an.nguyen@example.org",
  "userType": "Practitioner"
}
```

**`400`** Bad Request

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

**`409`** Conflict

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

---

<a id="get-scim-v2-users-id"></a>
## `GET /scim/v2/Users/{id}`

**SCIM: đọc một user**

id là id của ProjectMembership. Membership ngoài project, thuộc Bot/ClientApplication hoặc không tồn tại đều là 404.

Các trường hợp phản hồi: [../cases/scim.md#get-scim-v2-users-id](../cases/scim.md#get-scim-v2-users-id)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/scim+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `id` | đường dẫn | string | có | id membership |

**Phản hồi**

**`200`** OK

Schema: `scim.User`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |

```json
{
  "active": true,
  "emails": [
    {
      "value": "an@example.org"
    }
  ],
  "externalId": "hr-10234",
  "id": "3f2a8c1e-0000-4000-8000-000000000000",
  "meta": {
    "lastModified": "2026-11-23T08:30:00Z",
    "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
    "resourceType": "User",
    "version": "2026-11-23T08:30:00Z"
  },
  "name": {
    "familyName": "Nguyễn",
    "givenName": "An"
  },
  "schemas": [
    "urn:ietf:params:scim:schemas:core:2.0:User"
  ],
  "userName": "an.nguyen@example.org",
  "userType": "Practitioner"
}
```

**`404`** Not Found

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

---

<a id="put-scim-v2-users-id"></a>
## `PUT /scim/v2/Users/{id}`

**SCIM: thay user (PUT)**

Dùng id của đường dẫn (id trong body có và khác là 400). Chỉ ghi name, externalId, emails[0], active. Đổi email của User thuộc project: emailVerified=false, vô hiệu mọi security request và thu hồi mọi login (một transaction); User ở phạm vi server không ghi được (đổi email là 403). userName không đổi được.

Các trường hợp phản hồi: [../cases/scim.md#put-scim-v2-users-id](../cases/scim.md#put-scim-v2-users-id)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/scim+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `id` | đường dẫn | string | có | id membership |

**Body yêu cầu** (User)

Schema: `scim.User`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |

```json
{
  "active": true,
  "emails": [
    {
      "value": "an@example.org"
    }
  ],
  "externalId": "hr-10234",
  "id": "3f2a8c1e-0000-4000-8000-000000000000",
  "meta": {
    "lastModified": "2026-11-23T08:30:00Z",
    "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
    "resourceType": "User",
    "version": "2026-11-23T08:30:00Z"
  },
  "name": {
    "familyName": "Nguyễn",
    "givenName": "An"
  },
  "schemas": [
    "urn:ietf:params:scim:schemas:core:2.0:User"
  ],
  "userName": "an.nguyen@example.org",
  "userType": "Practitioner"
}
```

**Phản hồi**

**`200`** OK

Schema: `scim.User`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |

```json
{
  "active": true,
  "emails": [
    {
      "value": "an@example.org"
    }
  ],
  "externalId": "hr-10234",
  "id": "3f2a8c1e-0000-4000-8000-000000000000",
  "meta": {
    "lastModified": "2026-11-23T08:30:00Z",
    "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
    "resourceType": "User",
    "version": "2026-11-23T08:30:00Z"
  },
  "name": {
    "familyName": "Nguyễn",
    "givenName": "An"
  },
  "schemas": [
    "urn:ietf:params:scim:schemas:core:2.0:User"
  ],
  "userName": "an.nguyen@example.org",
  "userType": "Practitioner"
}
```

**`400`** Bad Request

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

**`403`** Forbidden

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

**`404`** Not Found

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

---

<a id="patch-scim-v2-users-id"></a>
## `PATCH /scim/v2/Users/{id}`

**SCIM: sửa user (PATCH)**

PatchOp với add, replace, remove. Chỉ active, externalId, name, name.givenName, name.familyName, emails ghi được; userName, userType và thuộc tính chuẩn không lưu (displayName...) bị bỏ qua; đường dẫn khác (admin, accessPolicy, profile...) là 400 invalidPath. add trên thuộc tính đa trị thay nguyên.

Các trường hợp phản hồi: [../cases/scim.md#patch-scim-v2-users-id](../cases/scim.md#patch-scim-v2-users-id)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/scim+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `id` | đường dẫn | string | có | id membership |

**Body yêu cầu** (PatchOp)

Schema: `scim.PatchOp`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `Operations` | mảng `scim.PatchOperation` | có |  | danh sách thao tác, áp theo thứ tự; vượt giới hạn số thao tác là 400 tooMany |  |
| `Operations[].op` | string | có | một trong: `add`, `remove`, `replace` | loại thao tác (không phân biệt hoa thường) | `"replace"` |
| `Operations[].path` | string | không |  | thuộc tính đích: active, externalId, name, name.givenName, name.familyName, emails; bỏ trống thì value là đối tượng nhiều thuộc tính (không dùng với remove) | `"active"` |
| `Operations[].value` | object | không |  | giá trị mới (kiểu theo thuộc tính: boolean, chuỗi, đối tượng hoặc mảng); không cần với remove |  |
| `schemas` | mảng string | có |  | phần tử đầu phải là urn:ietf:params:scim:api:messages:2.0:PatchOp, không thì 400 | `["urn:ietf:params:scim:api:messages:2.0:PatchOp"]` |

```json
{
  "Operations": [
    {
      "op": "replace",
      "path": "active"
    }
  ],
  "schemas": [
    "urn:ietf:params:scim:api:messages:2.0:PatchOp"
  ]
}
```

**Phản hồi**

**`200`** OK

Schema: `scim.User`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `emails` | mảng `scim.Email` | có |  | danh sách email; bắt buộc khi tạo, chỉ phần tử đầu được dùng |  |
| `emails[].value` | string | có |  | địa chỉ email; khi tạo/PATCH, phần tử đầu phải có value không rỗng | `"an@example.org"` |
| `schemas` | mảng string | có |  | danh sách schema URN; chứa urn:ietf:params:scim:schemas:core:2.0:User | `["urn:ietf:params:scim:schemas:core:2.0:User"]` |
| `userName` | string | có |  | tên đăng nhập SCIM; bắt buộc khi tạo, không đổi được sau khi tạo (PUT/PATCH bỏ qua) | `"an.nguyen@example.org"` |
| `active` | boolean | không |  | trạng thái hoạt động của membership; ghi được | `true` |
| `externalId` | string | không |  | id do IdP quản lý; ghi được (POST/PUT/PATCH); chuỗi rỗng là xóa | `"hr-10234"` |
| `id` | string | không |  | id ProjectMembership do server cấp; chỉ đọc (bị bỏ qua khi tạo/PUT) | `"3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta` | object `scim.Meta` | không |  | siêu dữ liệu do server sinh; chỉ đọc |  |
| `meta.resourceType` | string | có |  | loại tài nguyên SCIM, luôn là "User" | `"User"` |
| `meta.lastModified` | string | không | định dạng date-time | thời điểm sửa gần nhất (RFC 3339); chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `meta.location` | string | không |  | URL của tài nguyên; chỉ đọc | `"https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000"` |
| `meta.version` | string | không |  | phiên bản của tài nguyên: hiện bằng thời điểm cập nhật của membership (cùng giá trị lastModified), không phải ETag; chỉ đọc | `"2026-11-23T08:30:00Z"` |
| `name` | object `scim.Name` | không |  | họ tên (chỉ givenName, familyName được dùng) |  |
| `name.familyName` | string | không |  | họ (ghi vào lastName của User) | `"Nguyễn"` |
| `name.givenName` | string | không |  | tên (ghi vào firstName của User) | `"An"` |
| `userType` | string | không | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ; chỉ có tác dụng khi tạo, thiếu hoặc giá trị khác là Practitioner; không đổi được sau đó | `"Practitioner"` |

```json
{
  "active": true,
  "emails": [
    {
      "value": "an@example.org"
    }
  ],
  "externalId": "hr-10234",
  "id": "3f2a8c1e-0000-4000-8000-000000000000",
  "meta": {
    "lastModified": "2026-11-23T08:30:00Z",
    "location": "https://auth.example.org/scim/v2/Users/3f2a8c1e-0000-4000-8000-000000000000",
    "resourceType": "User",
    "version": "2026-11-23T08:30:00Z"
  },
  "name": {
    "familyName": "Nguyễn",
    "givenName": "An"
  },
  "schemas": [
    "urn:ietf:params:scim:schemas:core:2.0:User"
  ],
  "userName": "an.nguyen@example.org",
  "userType": "Practitioner"
}
```

**`400`** Bad Request

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

---

<a id="delete-scim-v2-users-id"></a>
## `DELETE /scim/v2/Users/{id}`

**SCIM: xóa user**

Xóa membership (User thuộc project mà hết membership thì bị xóa theo). Không xóa chủ project, admin cuối cùng hay membership của chính người gọi (400).

Các trường hợp phản hồi: [../cases/scim.md#delete-scim-v2-users-id](../cases/scim.md#delete-scim-v2-users-id)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/scim+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `id` | đường dẫn | string | có | id membership |

**Phản hồi**

**`204`** No Content

Không có body.

**`400`** Bad Request

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).

**`404`** Not Found

Schema: `scim.Error` (cấu trúc dùng chung, xem [common.md mục 5](../common.md#5-lỗi-scim)).
