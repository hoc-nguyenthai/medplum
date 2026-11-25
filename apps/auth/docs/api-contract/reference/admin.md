# Tham chiếu API: nhóm `admin`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/admin.md](../cases/admin.md).

Quản trị project qua HTTP (tắt mặc định, AUTH_ADMIN_ENABLED): project, client, thành viên, mời user, setting, site. Chỉ Bearer của admin project.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`POST /admin/projects/setpassword`](#post-admin-projects-setpassword) | Đặt mật khẩu cho user của project |
| [`GET /admin/projects/{projectId}`](#get-admin-projects-projectid) | Đọc project của chính mình (admin) |
| [`POST /admin/projects/{projectId}/client`](#post-admin-projects-projectid-client) | Tạo ClientApplication trong project |
| [`POST /admin/projects/{projectId}/client/{clientId}/rotate-secret`](#post-admin-projects-projectid-client-clientid-rotate-secret) | Xoay secret của client (ClientApplication/$rotate-secret) |
| [`POST /admin/projects/{projectId}/client/{clientId}/smart-launch`](#post-admin-projects-projectid-client-clientid-smart-launch) | Tạo launch SMART cho client (ClientApplication/$smart-launch) |
| [`POST /admin/projects/{projectId}/invite`](#post-admin-projects-projectid-invite) | Mời user vào project |
| [`GET /admin/projects/{projectId}/members/{membershipId}`](#get-admin-projects-projectid-members-membershipid) | Đọc một membership của project |
| [`POST /admin/projects/{projectId}/members/{membershipId}`](#post-admin-projects-projectid-members-membershipid) | Sửa membership |
| [`DELETE /admin/projects/{projectId}/members/{membershipId}`](#delete-admin-projects-projectid-members-membershipid) | Xóa membership |
| [`POST /admin/projects/{projectId}/members/{membershipId}/mfa/reset`](#post-admin-projects-projectid-members-membershipid-mfa-reset) | Gỡ một yếu tố MFA của member |
| [`POST /admin/projects/{projectId}/members/{membershipId}/resetpassword`](#post-admin-projects-projectid-members-membershipid-resetpassword) | Gửi thư đặt lại mật khẩu cho một member |
| [`POST /admin/projects/{projectId}/members/{membershipId}/update-email`](#post-admin-projects-projectid-members-membershipid-update-email) | Đổi email đăng nhập của member (User/$update-email) |
| [`POST /admin/projects/{projectId}/secrets`](#post-admin-projects-projectid-secrets) | Đặt các secret của project |
| [`POST /admin/projects/{projectId}/settings`](#post-admin-projects-projectid-settings) | Đặt các setting của project (allowlist) |
| [`POST /admin/projects/{projectId}/sites`](#post-admin-projects-projectid-sites) | Đặt các site của project (Google, reCAPTCHA) |
| [`POST /admin/super/projects`](#post-admin-super-projects) | Tạo project mới (Project/$init) |
| [`POST /admin/super/projects/{projectId}/archive`](#post-admin-super-projects-projectid-archive) | Lưu trữ mềm project (Project/$archive) |
| [`POST /admin/super/projects/{projectId}/restore`](#post-admin-super-projects-projectid-restore) | Khôi phục project đã lưu trữ (Project/$restore) |
| [`POST /admin/super/users/{userId}/rescope`](#post-admin-super-users-userid-rescope) | Chuyển user toàn cục sang phạm vi một project (User/$rescope) |

---

<a id="post-admin-projects-setpassword"></a>
## `POST /admin/projects/setpassword`

**Đặt mật khẩu cho user của project**

Chỉ user thuộc project của người gọi (user ở phạm vi server là 400 `User not found`). Kiểm mật khẩu bị rò rỉ, thu hồi mọi login của user.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-setpassword](../cases/admin.md#post-admin-projects-setpassword)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (email và mật khẩu mới)

Schema: `httpapi.AdminSetPasswordBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `email` | string | có |  | email của user cần đặt mật khẩu; bắt buộc, phải là email hợp lệ | `"an@example.org"` |
| `password` | string | có |  | mật khẩu mới; bắt buộc, phải qua kiểm tra độ mạnh/rò rỉ (domain.ValidatePassword) | `"Matkhau-Moi-2026!"` |

```json
{
  "email": "an@example.org",
  "password": "Matkhau-Moi-2026!"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-admin-projects-projectid"></a>
## `GET /admin/projects/{projectId}`

**Đọc project của chính mình (admin)**

Chỉ admin của project; projectId khác project của người gọi là 403. Giá trị `secret` và khóa bí mật trong `site` bị che.

Các trường hợp phản hồi: [../cases/admin.md#get-admin-projects-projectid](../cases/admin.md#get-admin-projects-projectid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Phản hồi**

**`200`** OK

Schema: `httpapi.AdminProjectResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `project` | object `application.AdminProjectView` | có |  | project của người gọi (secret và khóa bí mật của site đã bị che) |  |
| `project.id` | string | có |  | id của Project (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` |
| `project.name` | string | có |  | tên Project | `"Bệnh viện A"` |
| `project.secret` | mảng `fhir.ProjectSetting` | không |  | các secret của Project; giá trị luôn bị che (maskValue), chỉ có khi có secret |  |
| `project.setting` | mảng `fhir.ProjectSetting` | không |  | cấu hình Project; giá trị bí mật bị che, chỉ có khi Project có cấu hình |  |
| `project.site` | mảng `fhir.ProjectSite` | không |  | các site của Project; googleClientSecret bị che, chỉ có khi Project có site |  |

```json
{
  "project": {
    "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44",
    "name": "Bệnh viện A",
    "secret": [],
    "setting": [],
    "site": []
  }
}
```

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-client"></a>
## `POST /admin/projects/{projectId}/client`

**Tạo ClientApplication trong project**

Tạo client và membership của nó. `secret` (tối thiểu 32 ký tự) do người gọi đặt hoặc server sinh (64 ký tự hex); chỉ được trả trong phản hồi này. Trường ngoài danh sách (identityProvider, signInForm, status, meta...) là 400. accessPolicy phải thuộc project.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-client](../cases/admin.md#post-admin-projects-projectid-client)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Body yêu cầu** (Client)

Schema: `httpapi.AdminClientBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `name` | string | có |  | tên client; bắt buộc, rỗng hoặc chỉ khoảng trắng là 400 | `"Ứng dụng bệnh nhân"` |
| `accessPolicy` | object `fhir.Reference` | không |  | AccessPolicy áp cho client; phải thuộc project |  |
| `description` | string | không |  | mô tả client (tùy chọn) | `"Ứng dụng di động cho bệnh nhân"` |
| `grantType` | mảng string | không | mỗi phần tử một trong: `authorization_code`, `client_credentials`, `refresh_token` | các grant client được dùng (ngoài ba giá trị này server còn nhận token-exchange và pre-authorized_code); giá trị khác là 400 | `["client_credentials"]` |
| `launchUri` | string | không |  | URL khởi chạy EHR launch (http hoặc https tuyệt đối, không fragment, tối đa 2048 ký tự); cần để dùng smart-launch; sai là 400 | `"https://app.example.org/launch"` |
| `pkceOptional` | boolean | không |  | true: không bắt buộc PKCE cho client này (mặc định false) | `false` |
| `redirectUris` | mảng string | không |  | các redirect URI hợp lệ cho luồng authorization_code; mỗi URI phải hợp lệ, không thì 400 | `["https://app.example.org/callback"]` |
| `secret` | string | không | độ dài tối thiểu 32; độ dài tối đa 256 | secret do người gọi đặt (32-256 ký tự); bỏ trống thì server sinh 64 ký tự hex; chỉ trả trong phản hồi tạo | `"0f3c9a1e7b5d4c2a8e6f1b3d5a7c9e0f2b4d6f8a1c3e5b7d9f0a2c4e6b8d0f1a"` |

```json
{
  "description": "Ứng dụng di động cho bệnh nhân",
  "grantType": [
    "client_credentials"
  ],
  "launchUri": "https://app.example.org/launch",
  "name": "Ứng dụng bệnh nhân",
  "pkceOptional": false,
  "redirectUris": [
    "https://app.example.org/callback"
  ],
  "secret": "0f3c9a1e7b5d4c2a8e6f1b3d5a7c9e0f2b4d6f8a1c3e5b7d9f0a2c4e6b8d0f1a"
}
```

**Phản hồi**

**`201`** Created

Schema: `fhir.ClientApplication`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `accessTokenLifetime` | string | không |  | accessTokenLifetime |  |
| `allowedOrigin` | mảng string | không |  | allowedOrigin |  |
| `certificateTrustStore` | string | không |  | certificateTrustStore |  |
| `defaultScope` | mảng string | không |  | defaultScope |  |
| `description` | string | không |  | description |  |
| `grantType` | mảng string | không |  | grantType |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identityProvider` | object `fhir.IdentityProvider` | không |  | identityProvider |  |
| `implicitRules` | string | không |  | implicitRules |  |
| `jwksUri` | string | không |  | jwksUri |  |
| `language` | string | không |  | language |  |
| `launchIdentifierSystems` | mảng `fhir.ClientApplicationLaunchIdentifierSystems` | không |  | launchIdentifierSystems |  |
| `launchUri` | string | không |  | launchUri |  |
| `meta` | object `fhir.Meta` | không |  | meta |  |
| `name` | string | không |  | name |  |
| `pkceOptional` | boolean | không |  | pkceOptional |  |
| `redirectUri` | string | không |  | redirectUri |  |
| `redirectUris` | mảng string | không |  | redirectUris |  |
| `refreshTokenLifetime` | string | không |  | refreshTokenLifetime |  |
| `resourceType` | string | không |  |  |  |
| `responseType` | mảng string | không |  | responseType |  |
| `retiringSecret` | string | không |  | retiringSecret |  |
| `secret` | string | không |  | secret |  |
| `signInForm` | object `fhir.ClientApplicationSignInForm` | không |  | signInForm |  |
| `status` | string | không |  | active \| error \| off |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |

```json
{
  "allowedOrigin": [],
  "defaultScope": [],
  "grantType": [],
  "launchIdentifierSystems": [],
  "redirectUris": [],
  "responseType": []
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-client-clientid-rotate-secret"></a>
## `POST /admin/projects/{projectId}/client/{clientId}/rotate-secret`

**Xoay secret của client (ClientApplication/$rotate-secret)**

Secret hiện tại thành `retiringSecret` (vẫn dùng được để các nơi triển khai kịp đổi), secret mới (do người gọi đặt hoặc server sinh) là `secret`; `retiringSecret` cũ hơn bị bỏ. `retireOnly=true` chỉ bỏ `retiringSecret` (idempotent). Body tùy chọn; trường lạ là 400. Client ngoài project, không tồn tại hoặc dựng sẵn đều là 403 chung. Client không có secret là 400. Không thu hồi token đã cấp. `clientSecret` chỉ hiện một lần.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-client-clientid-rotate-secret](../cases/admin.md#post-admin-projects-projectid-client-clientid-rotate-secret)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `clientId` | đường dẫn | string | có | id client |

**Body yêu cầu** (Tùy chọn xoay)

Schema: `httpapi.RotateSecretBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `retireOnly` | boolean | không |  | true: chỉ bỏ secret cũ đang chờ hết hạn (retiringSecret), không đổi secret hiện tại; loại trừ với secret | `false` |
| `secret` | string | không | độ dài tối thiểu 32; độ dài tối đa 256 | secret mới do người gọi đặt (32-256 ký tự); bỏ trống thì server sinh 64 ký tự hex; phải khác secret hiện tại | `"7d1e5b9a3c6f2e4a8b0d9c1f3e5a7b2d4c6e8f0a1b3d5f7e9c2a4b6d8e0f1a3c"` |

```json
{
  "retireOnly": false,
  "secret": "7d1e5b9a3c6f2e4a8b0d9c1f3e5a7b2d4c6e8f0a1b3d5f7e9c2a4b6d8e0f1a3c"
}
```

**Phản hồi**

**`200`** OK

Schema: `httpapi.RotateSecretResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `clientId` | string | có |  | id client |  |
| `retiring` | boolean | có |  | true: secret cũ vẫn dùng được song song (retiringSecret) cho tới lần xoay kế tiếp hoặc retireOnly |  |
| `clientSecret` | string | không |  | secret MỚI, chỉ hiện trong phản hồi này; không có khi retireOnly |  |

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-client-clientid-smart-launch"></a>
## `POST /admin/projects/{projectId}/client/{clientId}/smart-launch`

**Tạo launch SMART cho client (ClientApplication/$smart-launch)**

Tạo SmartAppLaunch mang bệnh nhân, lượt khám, ngữ cảnh FHIR khác và trả `launchUrl` (launchUri của client kèm `iss` và `launch`). Client phải thuộc project và có launchUri; cần AUTH_FHIR_BASE_URL; phải có ít nhất một ngữ cảnh. Tham chiếu chỉ được kiểm dạng, không kiểm resource tồn tại. Launch không gắn client, không hết hạn. Client ngoài project hoặc không tồn tại là 403 chung. Trường lạ là 400.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-client-clientid-smart-launch](../cases/admin.md#post-admin-projects-projectid-client-clientid-smart-launch)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `clientId` | đường dẫn | string | có | id client |

**Body yêu cầu** (Ngữ cảnh launch)

Schema: `httpapi.SmartLaunchBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `encounter` | string | không |  | lượt khám của ngữ cảnh, dạng Encounter/<id> | `"Encounter/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a46"` |
| `fhirContext` | mảng string | không |  | ngữ cảnh FHIR khác dạng Type/<id>, tối đa 20 | `["DiagnosticReport/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a47"]` |
| `patient` | string | không |  | bệnh nhân của ngữ cảnh, dạng Patient/<id> | `"Patient/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a45"` |

```json
{
  "encounter": "Encounter/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a46",
  "fhirContext": [
    "DiagnosticReport/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a47"
  ],
  "patient": "Patient/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a45"
}
```

**Phản hồi**

**`201`** Created

Schema: `httpapi.SmartLaunchResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `launchId` | string | có |  | id SmartAppLaunch vừa tạo (giá trị `launch` của /oauth2/authorize) |  |
| `launchUrl` | string | có |  | launchUri của client kèm iss (AUTH_FHIR_BASE_URL) và launch; ứng dụng mở URL này (chuyển hướng trình duyệt) để bắt đầu EHR launch |  |

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-invite"></a>
## `POST /admin/projects/{projectId}/invite`

**Mời user vào project**

Tạo (hoặc dùng lại) User, hồ sơ Patient/Practitioner/RelatedPerson và ProjectMembership trong một transaction, rồi gửi thư (trừ sendEmail=false). Cần email hoặc externalId. Practitioner bị chặn theo AUTH_BLOCKED_EMAIL_DOMAINS và setting allowedPractitionerEmailDomain. 409 nếu user đã là member (trừ upsert) hoặc externalId trùng. Gửi thư lỗi là 400 nhưng dữ liệu đã tạo (thông báo nêu rõ membership). Trả ProjectMembership.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-invite](../cases/admin.md#post-admin-projects-projectid-invite)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Body yêu cầu** (Lời mời)

Schema: `httpapi.InviteBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `firstName` | string | có |  | tên; bắt buộc, không được chỉ khoảng trắng | `"An"` |
| `lastName` | string | có |  | họ; bắt buộc, không được chỉ khoảng trắng | `"Nguyễn"` |
| `resourceType` | string | có | một trong: `Patient`, `Practitioner`, `RelatedPerson` | loại hồ sơ tạo cho user; bắt buộc | `"Practitioner"` |
| `email` | string | không |  | email của user (hạ chữ thường); phải có email hoặc externalId, nếu có thì phải hợp lệ | `"an@example.org"` |
| `externalId` | string | không |  | id ngoài; phải có email hoặc externalId, nếu có thì không được rỗng/chỉ khoảng trắng | `"hr-10234"` |
| `forceNewMembership` | boolean | không |  | true: luôn tạo membership mới thay vì dùng lại membership hiện có | `false` |
| `membership` | object `httpapi.InviteMembershipBody` | không |  | tùy chọn của ProjectMembership được tạo |  |
| `membership.access` | mảng `fhir.ProjectMembershipAccess` | không |  | các AccessPolicy tham số hóa gắn thêm cho membership |  |
| `membership.accessPolicy` | object `fhir.Reference` | không |  | AccessPolicy áp cho membership; phải thuộc project |  |
| `membership.active` | boolean | không |  | trạng thái hoạt động của membership; bỏ trống là mặc định của server | `true` |
| `membership.admin` | boolean | không |  | true: cấp quyền admin project cho membership | `false` |
| `membership.externalId` | string | không |  | id ngoài của membership (từ hệ thống nhân sự/IdP); nếu có thì không được rỗng/chỉ khoảng trắng; trùng là 409 | `"hr-10234"` |
| `membership.profile` | object `fhir.Reference` | không |  | hồ sơ có sẵn để gắn vào membership (dùng thay cho việc tạo hồ sơ mới) |  |
| `mfaRequired` | boolean | không |  | đặt User.mfaRequired cho user mới; bỏ trống là không đặt | `false` |
| `password` | string | không |  | mật khẩu ban đầu; bỏ trống thì user tự đặt qua thư mời; nếu có phải qua ValidatePassword | `"Matkhau-Moi-2026!"` |
| `patient` | object `fhir.Reference` | không |  | Patient/<id> mà RelatedPerson liên quan; bắt buộc khi resourceType=RelatedPerson và không có membership.profile |  |
| `scope` | string | không | một trong: `project`, `server` | phạm vi của User; bỏ trống thì server chọn (thường server, hoặc project khi project yêu cầu) | `"project"` |
| `sendEmail` | boolean | không |  | false: không gửi thư mời; bỏ trống là gửi khi có email (cần cấu hình thư, không thì 400) | `true` |
| `upsert` | boolean | không |  | true: user đã là member thì cập nhật thay vì 409 | `false` |

```json
{
  "email": "an@example.org",
  "externalId": "hr-10234",
  "firstName": "An",
  "forceNewMembership": false,
  "lastName": "Nguyễn",
  "membership": {
    "access": [],
    "active": true,
    "admin": false,
    "externalId": "hr-10234"
  },
  "mfaRequired": false,
  "password": "Matkhau-Moi-2026!",
  "resourceType": "Practitioner",
  "scope": "project",
  "sendEmail": true,
  "upsert": false
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.ProjectMembership`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `access` | mảng `fhir.ProjectMembershipAccess` | không |  | access |  |
| `accessPolicy` | object `fhir.Reference` | không |  | accessPolicy |  |
| `active` | boolean | không |  | Whether this patient's record is in active use |  |
| `admin` | boolean | không |  | Whether this user is a project administrator. |  |
| `externalId` | string | không |  | externalId |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | mảng `fhir.Identifier` | không |  | An identifier for this ProjectMembership |  |
| `implicitRules` | string | không |  | implicitRules |  |
| `invitedBy` | object `fhir.Reference` | không |  | invitedBy |  |
| `language` | string | không |  | language |  |
| `meta` | object `fhir.Meta` | không |  | meta |  |
| `profile` | object `fhir.Reference` | không |  | profile (bắt buộc) |  |
| `project` | object `fhir.Reference` | không |  | project (bắt buộc) |  |
| `resourceType` | string | không |  |  |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `user` | object `fhir.Reference` | không |  | user (bắt buộc) |  |
| `userConfiguration` | object `fhir.Reference` | không |  | userConfiguration |  |
| `userName` | string | không |  | userName |  |

```json
{
  "access": [],
  "identifier": []
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`409`** Conflict

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="get-admin-projects-projectid-members-membershipid"></a>
## `GET /admin/projects/{projectId}/members/{membershipId}`

**Đọc một membership của project**

Membership không thuộc project hoặc không tồn tại đều là 403.

Các trường hợp phản hồi: [../cases/admin.md#get-admin-projects-projectid-members-membershipid](../cases/admin.md#get-admin-projects-projectid-members-membershipid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `membershipId` | đường dẫn | string | có | id membership |

**Phản hồi**

**`200`** OK

Schema: `fhir.ProjectMembership`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `access` | mảng `fhir.ProjectMembershipAccess` | không |  | access |  |
| `accessPolicy` | object `fhir.Reference` | không |  | accessPolicy |  |
| `active` | boolean | không |  | Whether this patient's record is in active use |  |
| `admin` | boolean | không |  | Whether this user is a project administrator. |  |
| `externalId` | string | không |  | externalId |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | mảng `fhir.Identifier` | không |  | An identifier for this ProjectMembership |  |
| `implicitRules` | string | không |  | implicitRules |  |
| `invitedBy` | object `fhir.Reference` | không |  | invitedBy |  |
| `language` | string | không |  | language |  |
| `meta` | object `fhir.Meta` | không |  | meta |  |
| `profile` | object `fhir.Reference` | không |  | profile (bắt buộc) |  |
| `project` | object `fhir.Reference` | không |  | project (bắt buộc) |  |
| `resourceType` | string | không |  |  |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `user` | object `fhir.Reference` | không |  | user (bắt buộc) |  |
| `userConfiguration` | object `fhir.Reference` | không |  | userConfiguration |  |
| `userName` | string | không |  | userName |  |

```json
{
  "access": [],
  "identifier": []
}
```

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-members-membershipid"></a>
## `POST /admin/projects/{projectId}/members/{membershipId}`

**Sửa membership**

Body là toàn bộ ProjectMembership (resourceType và id khớp đường dẫn, nếu không 403). Chỉ admin, active, externalId, accessPolicy, access được đổi; đổi user, profile hoặc project là 400. accessPolicy phải thuộc project. Không hạ quyền hay vô hiệu hóa admin cuối cùng (400).

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-members-membershipid](../cases/admin.md#post-admin-projects-projectid-members-membershipid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `membershipId` | đường dẫn | string | có | id membership |

**Body yêu cầu** (Membership)

Schema: `fhir.ProjectMembership`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `access` | mảng `fhir.ProjectMembershipAccess` | không |  | access |  |
| `accessPolicy` | object `fhir.Reference` | không |  | accessPolicy |  |
| `active` | boolean | không |  | Whether this patient's record is in active use |  |
| `admin` | boolean | không |  | Whether this user is a project administrator. |  |
| `externalId` | string | không |  | externalId |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | mảng `fhir.Identifier` | không |  | An identifier for this ProjectMembership |  |
| `implicitRules` | string | không |  | implicitRules |  |
| `invitedBy` | object `fhir.Reference` | không |  | invitedBy |  |
| `language` | string | không |  | language |  |
| `meta` | object `fhir.Meta` | không |  | meta |  |
| `profile` | object `fhir.Reference` | không |  | profile (bắt buộc) |  |
| `project` | object `fhir.Reference` | không |  | project (bắt buộc) |  |
| `resourceType` | string | không |  |  |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `user` | object `fhir.Reference` | không |  | user (bắt buộc) |  |
| `userConfiguration` | object `fhir.Reference` | không |  | userConfiguration |  |
| `userName` | string | không |  | userName |  |

```json
{
  "access": [],
  "identifier": []
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.ProjectMembership`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `access` | mảng `fhir.ProjectMembershipAccess` | không |  | access |  |
| `accessPolicy` | object `fhir.Reference` | không |  | accessPolicy |  |
| `active` | boolean | không |  | Whether this patient's record is in active use |  |
| `admin` | boolean | không |  | Whether this user is a project administrator. |  |
| `externalId` | string | không |  | externalId |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | mảng `fhir.Identifier` | không |  | An identifier for this ProjectMembership |  |
| `implicitRules` | string | không |  | implicitRules |  |
| `invitedBy` | object `fhir.Reference` | không |  | invitedBy |  |
| `language` | string | không |  | language |  |
| `meta` | object `fhir.Meta` | không |  | meta |  |
| `profile` | object `fhir.Reference` | không |  | profile (bắt buộc) |  |
| `project` | object `fhir.Reference` | không |  | project (bắt buộc) |  |
| `resourceType` | string | không |  |  |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `user` | object `fhir.Reference` | không |  | user (bắt buộc) |  |
| `userConfiguration` | object `fhir.Reference` | không |  | userConfiguration |  |
| `userName` | string | không |  | userName |  |

```json
{
  "access": [],
  "identifier": []
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`409`** Conflict

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="delete-admin-projects-projectid-members-membershipid"></a>
## `DELETE /admin/projects/{projectId}/members/{membershipId}`

**Xóa membership**

Không xóa chủ project hay admin cuối cùng (400). User thuộc project mà hết membership thì bị xóa cùng; user dùng chung nhiều project thì giữ. Token của membership bị từ chối ngay.

Các trường hợp phản hồi: [../cases/admin.md#delete-admin-projects-projectid-members-membershipid](../cases/admin.md#delete-admin-projects-projectid-members-membershipid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `membershipId` | đường dẫn | string | có | id membership |

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-members-membershipid-mfa-reset"></a>
## `POST /admin/projects/{projectId}/members/{membershipId}/mfa/reset`

**Gỡ một yếu tố MFA của member**

Body tùy chọn {method: totp|email} (mặc định totp). User phải đang có yếu tố đó (400). Gỡ totp thì secret bị thay. Chỉ user thuộc project.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-members-membershipid-mfa-reset](../cases/admin.md#post-admin-projects-projectid-members-membershipid-mfa-reset)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `membershipId` | đường dẫn | string | có | id membership |

**Body yêu cầu** (Phương thức MFA cần gỡ (tùy chọn))

Schema: `httpapi.MFAResetBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `method` | string | không | một trong: `totp`, `email` | yếu tố MFA cần gỡ; bỏ trống hoặc null là totp; khác totp/email là 400; user phải đang có yếu tố đó | `"totp"` |

```json
{
  "method": "totp"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-members-membershipid-resetpassword"></a>
## `POST /admin/projects/{projectId}/members/{membershipId}/resetpassword`

**Gửi thư đặt lại mật khẩu cho một member**

Chỉ user thuộc project (user phạm vi server là 403). Khác /auth/resetpassword công khai, lỗi được báo rõ: thiếu email 400, chưa cấu hình thư 400, gửi thư lỗi 500.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-members-membershipid-resetpassword](../cases/admin.md#post-admin-projects-projectid-members-membershipid-resetpassword)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `membershipId` | đường dẫn | string | có | id membership |

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-members-membershipid-update-email"></a>
## `POST /admin/projects/{projectId}/members/{membershipId}/update-email`

**Đổi email đăng nhập của member (User/$update-email)**

Chỉ user thuộc đúng project của người gọi (user ở phạm vi server hay project khác là 403 chung). Email mới không trùng trong project (400); Practitioner chịu danh sách chặn và allowedPractitionerEmailDomain. Ghi email, đặt emailVerified=false, vô hiệu mọi security request và thu hồi mọi login trong một transaction; thư báo gửi tới địa chỉ cũ nếu đã cấu hình thư. Không đổi telecom của hồ sơ.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-members-membershipid-update-email](../cases/admin.md#post-admin-projects-projectid-members-membershipid-update-email)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |
| `membershipId` | đường dẫn | string | có | id membership |

**Body yêu cầu** (Email mới)

Schema: `httpapi.UpdateEmailBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `email` | string | có |  | email đăng nhập mới; hạ chữ thường, phải đúng định dạng, khác email hiện tại, không trùng trong project | `"an.nguyen@benhvien-a.vn"` |

```json
{
  "email": "an.nguyen@benhvien-a.vn"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-secrets"></a>
## `POST /admin/projects/{projectId}/secrets`

**Đặt các secret của project**

Body là mảng ProjectSetting; thay TOÀN BỘ Project.secret (`[]` xóa hết). Mỗi mục có tên (chữ cái đầu, chữ, số, `_.-`, tối đa 64), không trùng, đúng một value[x] (string tối đa 4096). Phản hồi che giá trị (`********`); gửi lại đúng giá trị che nghĩa là giữ giá trị hiện có của secret cùng tên. Secret `smtp*` chỉ nhận khi máy chủ bật SMTP riêng của project.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-secrets](../cases/admin.md#post-admin-projects-projectid-secrets)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Body yêu cầu** (Danh sách secret)

Mảng các phần tử `fhir.ProjectSetting`.

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `name` | string | không |  | name (bắt buộc) |  |
| `valueBoolean` | boolean | không |  | value[x] (bắt buộc) |  |
| `valueDecimal` | number | không |  | value[x] (bắt buộc) |  |
| `valueInteger` | integer | không |  | value[x] (bắt buộc) |  |
| `valueString` | string | không |  | value[x] (bắt buộc) |  |

**Phản hồi**

**`200`** OK

Schema: `httpapi.AdminProjectResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `project` | object `application.AdminProjectView` | có |  | project của người gọi (secret và khóa bí mật của site đã bị che) |  |
| `project.id` | string | có |  | id của Project (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` |
| `project.name` | string | có |  | tên Project | `"Bệnh viện A"` |
| `project.secret` | mảng `fhir.ProjectSetting` | không |  | các secret của Project; giá trị luôn bị che (maskValue), chỉ có khi có secret |  |
| `project.setting` | mảng `fhir.ProjectSetting` | không |  | cấu hình Project; giá trị bí mật bị che, chỉ có khi Project có cấu hình |  |
| `project.site` | mảng `fhir.ProjectSite` | không |  | các site của Project; googleClientSecret bị che, chỉ có khi Project có site |  |

```json
{
  "project": {
    "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44",
    "name": "Bệnh viện A",
    "secret": [],
    "setting": [],
    "site": []
  }
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-settings"></a>
## `POST /admin/projects/{projectId}/settings`

**Đặt các setting của project (allowlist)**

Body là mảng ProjectSetting. Chỉ nhận mfaRequired (boolean), allowedMfaMethods (totp,email) và appName; tên khác là 400. Thay các setting thuộc allowlist, giữ nguyên setting ngoài allowlist đang có (ví dụ do vận hành đặt).

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-settings](../cases/admin.md#post-admin-projects-projectid-settings)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Body yêu cầu** (Danh sách setting)

Mảng các phần tử `fhir.ProjectSetting`.

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `name` | string | không |  | name (bắt buộc) |  |
| `valueBoolean` | boolean | không |  | value[x] (bắt buộc) |  |
| `valueDecimal` | number | không |  | value[x] (bắt buộc) |  |
| `valueInteger` | integer | không |  | value[x] (bắt buộc) |  |
| `valueString` | string | không |  | value[x] (bắt buộc) |  |

**Phản hồi**

**`200`** OK

Schema: `httpapi.AdminProjectResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `project` | object `application.AdminProjectView` | có |  | project của người gọi (secret và khóa bí mật của site đã bị che) |  |
| `project.id` | string | có |  | id của Project (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` |
| `project.name` | string | có |  | tên Project | `"Bệnh viện A"` |
| `project.secret` | mảng `fhir.ProjectSetting` | không |  | các secret của Project; giá trị luôn bị che (maskValue), chỉ có khi có secret |  |
| `project.setting` | mảng `fhir.ProjectSetting` | không |  | cấu hình Project; giá trị bí mật bị che, chỉ có khi Project có cấu hình |  |
| `project.site` | mảng `fhir.ProjectSite` | không |  | các site của Project; googleClientSecret bị che, chỉ có khi Project có site |  |

```json
{
  "project": {
    "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44",
    "name": "Bệnh viện A",
    "secret": [],
    "setting": [],
    "site": []
  }
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-projects-projectid-sites"></a>
## `POST /admin/projects/{projectId}/sites`

**Đặt các site của project (Google, reCAPTCHA)**

Body là mảng ProjectSite; thay toàn bộ Project.site. googleClientId và recaptchaSiteKey phải duy nhất giữa các project (409) và googleClientId không được trùng Google client chính của máy chủ; cặp id/secret phải đi cùng nhau. Phản hồi che khóa bí mật (`********`); gửi lại đúng giá trị che nghĩa là giữ secret hiện có của site cùng tên.

Các trường hợp phản hồi: [../cases/admin.md#post-admin-projects-projectid-sites](../cases/admin.md#post-admin-projects-projectid-sites)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Body yêu cầu** (Danh sách site)

Mảng các phần tử `fhir.ProjectSite`.

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `domain` | mảng string | không |  | domain (bắt buộc) |  |
| `googleClientId` | string | không |  | googleClientId |  |
| `googleClientSecret` | string | không |  | googleClientSecret |  |
| `name` | string | không |  | name (bắt buộc) |  |
| `recaptchaSecretKey` | string | không |  | recaptchaSecretKey |  |
| `recaptchaSiteKey` | string | không |  | recaptchaSiteKey |  |

```json
{
  "domain": []
}
```

**Phản hồi**

**`200`** OK

Schema: `httpapi.AdminProjectResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `project` | object `application.AdminProjectView` | có |  | project của người gọi (secret và khóa bí mật của site đã bị che) |  |
| `project.id` | string | có |  | id của Project (UUID) | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` |
| `project.name` | string | có |  | tên Project | `"Bệnh viện A"` |
| `project.secret` | mảng `fhir.ProjectSetting` | không |  | các secret của Project; giá trị luôn bị che (maskValue), chỉ có khi có secret |  |
| `project.setting` | mảng `fhir.ProjectSetting` | không |  | cấu hình Project; giá trị bí mật bị che, chỉ có khi Project có cấu hình |  |
| `project.site` | mảng `fhir.ProjectSite` | không |  | các site của Project; googleClientSecret bị che, chỉ có khi Project có site |  |

```json
{
  "project": {
    "id": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44",
    "name": "Bệnh viện A",
    "secret": [],
    "setting": [],
    "site": []
  }
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`409`** Conflict

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-super-projects"></a>
## `POST /admin/super/projects`

**Tạo project mới (Project/$init)**

Chỉ admin của project được đánh dấu superAdmin (`auth project super`). Tạo project cùng AccessPolicy mặc định, client mặc định và membership admin cho chủ sở hữu trong một transaction. `ownerEmail` phải là user toàn cục đã tồn tại (không gắn project); bỏ trống là chính người gọi. Trường ngoài danh sách là 400. `clientSecret` chỉ hiện một lần. Ghi log kiểm toán. Tắt mặc định (AUTH_SUPER_ADMIN_ENABLED).

Các trường hợp phản hồi: [../cases/admin.md#post-admin-super-projects](../cases/admin.md#post-admin-super-projects)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Project)

Schema: `httpapi.SuperProjectBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `name` | string | có |  | tên project, 1 đến 72 ký tự; bắt buộc | `"Bệnh viện B"` |
| `features` | mảng string | không |  | tính năng bật sẵn cho project (tên chữ thường, số, gạch ngang; tối đa 20) | `["scim"]` |
| `ownerEmail` | string | không |  | email của user TOÀN CỤC đã tồn tại sẽ là admin đầu tiên; bỏ trống là chính người gọi | `"owner@example.org"` |

```json
{
  "features": [
    "scim"
  ],
  "name": "Bệnh viện B",
  "ownerEmail": "owner@example.org"
}
```

**Phản hồi**

**`201`** Created

Schema: `httpapi.SuperProjectResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `clientId` | string | có |  | id client mặc định của project |  |
| `clientSecret` | string | có |  | secret của client mặc định; CHỈ hiện trong phản hồi này |  |
| `membershipId` | string | có |  | id ProjectMembership admin của chủ sở hữu trong project mới |  |
| `name` | string | có |  | tên project | `"Bệnh viện B"` |
| `owner` | string | có |  | tham chiếu user chủ sở hữu | `"User/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a45"` |
| `projectId` | string | có |  | id project mới | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` |

```json
{
  "name": "Bệnh viện B",
  "owner": "User/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a45",
  "projectId": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"
}
```

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-super-projects-projectid-archive"></a>
## `POST /admin/super/projects/{projectId}/archive`

**Lưu trữ mềm project (Project/$archive)**

Chỉ admin của project superAdmin. Đặt Project.active=false: đăng nhập, cấp và làm mới token, userinfo, bearer của /admin và SCIM đều bị chặn ngay (`Project not active`, kể cả token đang dùng). Dữ liệu FHIR giữ nguyên; login không bị thu hồi nên `restore` phục hồi nguyên trạng. Không archive project của chính người gọi (400); đã lưu trữ rồi vẫn 200; project không tồn tại 404. Body bỏ qua. Ghi log kiểm toán. Tắt mặc định (AUTH_SUPER_ADMIN_ENABLED).

Các trường hợp phản hồi: [../cases/admin.md#post-admin-super-projects-projectid-archive](../cases/admin.md#post-admin-super-projects-projectid-archive)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Phản hồi**

**`200`** OK

Schema: `httpapi.SuperProjectStateResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `projectId` | string | có |  | id project |  |
| `active` | boolean | không |  | false sau archive, true sau restore |  |

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-super-projects-projectid-restore"></a>
## `POST /admin/super/projects/{projectId}/restore`

**Khôi phục project đã lưu trữ (Project/$restore)**

Chỉ admin của project superAdmin. Đặt Project.active=true; token và login cũ dùng lại được nếu chưa hết hạn. Project đang hoạt động vẫn 200; project không tồn tại 404. Body bỏ qua. Ghi log kiểm toán. Tắt mặc định (AUTH_SUPER_ADMIN_ENABLED).

Các trường hợp phản hồi: [../cases/admin.md#post-admin-super-projects-projectid-restore](../cases/admin.md#post-admin-super-projects-projectid-restore)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `projectId` | đường dẫn | string | có | id project |

**Phản hồi**

**`200`** OK

Schema: `httpapi.SuperProjectStateResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `projectId` | string | có |  | id project |  |
| `active` | boolean | không |  | false sau archive, true sau restore |  |

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

---

<a id="post-admin-super-users-userid-rescope"></a>
## `POST /admin/super/users/{userId}/rescope`

**Chuyển user toàn cục sang phạm vi một project (User/$rescope)**

Chỉ admin của project superAdmin. User phải không gắn project nào, có ít nhất một membership và MỌI membership đều thuộc project đích (còn membership ở project khác là 400); không chuyển chính người gọi; project đích đã có user khác cùng email là 409. Ghi User.project, thu hồi mọi login trong một transaction. Chỉ chiều server sang project. Ghi log kiểm toán. Tắt mặc định (AUTH_SUPER_ADMIN_ENABLED).

Các trường hợp phản hồi: [../cases/admin.md#post-admin-super-users-userid-rescope](../cases/admin.md#post-admin-super-users-userid-rescope)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `userId` | đường dẫn | string | có | id user |

**Body yêu cầu** (Project đích)

Schema: `httpapi.SuperRescopeBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `projectId` | string | có |  | project đích: user trở thành user của project này | `"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` |

```json
{
  "projectId": "0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"
}
```

**Phản hồi**

**`200`** OK

Schema: `httpapi.SuperRescopeResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `projectId` | string | có |  | project mà user giờ thuộc về |  |
| `userId` | string | có |  | id user vừa chuyển |  |

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`404`** Not Found

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`409`** Conflict

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`429`** Too Many Requests

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).
