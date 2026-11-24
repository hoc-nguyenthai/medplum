# auth: quy ước chung của hợp đồng API

Áp dụng cho **mọi route** của `auth`; các tệp [cases/](cases/) và [reference/](reference/) chỉ ghi phần riêng của từng route và trỏ về đây. Nguồn: `internal/presentation/httpapi/errors.go`, `httpapi.go`, `auth.go`, `external.go`, `pkg/httpsec`.

Bố cục tài liệu hợp đồng:

| Thư mục | Nội dung | Cách duy trì |
|---|---|---|
| [reference/](reference/README.md) | Từng route: header, tham số, **bảng từng trường** (kiểu, bắt buộc, ràng buộc, mô tả, ví dụ), JSON mẫu của yêu cầu và phản hồi | **Sinh** từ `swagger.json`, có test chống lệch |
| [cases/](cases/README.md) | Từng route: **từng trường hợp** (điều kiện → HTTP → header → body, thông điệp lỗi nguyên văn, thứ tự kiểm tra) | Viết tay; test bắt route thiếu mục |
| `common.md` (tệp này) | Header, cookie, envelope lỗi, hạn mức, mã trạng thái dùng chung | Viết tay |

## 1. Địa chỉ và định dạng

- Cổng `:8006` (image Docker `:8000`); địa chỉ công khai là `AUTH_BASE_URL`. Không có tiền tố phiên bản chung (chỉ `/email/v1/*`, `/scim/v2/*`).
- Body yêu cầu `application/json` (UTF-8), **trừ** `POST /oauth2/token` (và `introspect`, `logout`, `userinfo` nhận form) chỉ `application/x-www-form-urlencoded`, SCIM nhận `application/scim+json` hoặc `application/json`.
- Body phản hồi: `/auth/*` và `/admin/*` là `application/json` (dữ liệu) hoặc `application/fhir+json` (OperationOutcome); `/oauth2/*` là `application/json`; `/scim/*` là `application/scim+json`.
- Thời gian: ISO 8601 UTC; thời hạn tính bằng giây trừ khi ghi khác.

## 2. Header

### Header yêu cầu

| Header | Dùng ở | Ý nghĩa |
|---|---|---|
| `Authorization: Bearer <access_token>` | Route ghi "Bearer" | Access token do chính `auth` ký, **có trạng thái**: login bị thu hồi thì `401` ngay |
| `Authorization: Basic base64(client_id:client_secret)` | `POST /oauth2/token`, SCIM | Xác thực client (RFC 6749 §2.3.1: tách ở dấu `:` đầu tiên, URL-decode từng phần) |
| `Content-Type` | Mọi route có body | Xem mục 1; sai kiểu ở `/oauth2/token` là `400` văn bản `Unsupported content type` |
| `Cookie: medplum-<clientId>=...` | `/oauth2/authorize` | Cookie phiên đăng nhập (SSO) |
| `Cookie: auth-external-state=...` | `/auth/external*` | Cookie chống giả mạo callback IdP |
| `X-Medplum-On-Behalf-Of: ProjectMembership/<id>` | `POST /auth/preauthorize` | Membership được cấp mã thay mặt |
| `X-Forwarded-For` | mọi route | **Bị bỏ qua** trừ khi `AUTH_TRUSTED_PROXIES` chỉ rõ proxy tin cậy |
| `Origin` | trình duyệt | Phải thuộc `AUTH_ALLOWED_ORIGINS` mới có header CORS |

### Header phản hồi (mọi route, kể cả 404 và lỗi)

| Header | Giá trị | Ghi chú |
|---|---|---|
| `Cache-Control` | `no-store, no-cache, must-revalidate` | Ngoại lệ: `POST /oauth2/token`, `/auth/exchange`, `/auth/preauthorize` ghi đè thành `no-store`; các tài liệu `/.well-known/*` (JWKS và discovery) là `public, max-age=300` (JWKS kèm `ETag`); `/swagger/*` |
| `Pragma` | `no-cache` | `POST /oauth2/token` cũng đặt |
| `Permissions-Policy` | `accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=(), interest-cohort=()` | |
| `X-XSS-Protection` | `1; mode=block` | |
| `Content-Security-Policy` | `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';` | bỏ qua ở `/swagger/*` |
| `X-Frame-Options` | `DENY` | |
| `X-Content-Type-Options` | `nosniff` | |
| `Referrer-Policy` | `no-referrer` | |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains; preload` | chỉ khi `AUTH_BASE_URL` là https |
| `X-Request-Id` | id yêu cầu | Nêu id này khi báo lỗi; log 500 mang cùng id |

### Header phản hồi có điều kiện

| Header | Khi nào | Ý nghĩa |
|---|---|---|
| `Retry-After` | `429` | Số giây chờ (hạn mức IP luôn `1`; hạn mức MFA tính theo cửa sổ còn lại) |
| `WWW-Authenticate` | `401` | `/auth`, `/admin`, `/email`: `Bearer realm="auth"` (thiếu token) hoặc `Bearer realm="auth", error="invalid_token"` (token sai). `/scim`: hai dòng `Basic realm="scim", charset="UTF-8"` và `Bearer realm="scim"` |
| `Set-Cookie` | xem mục 3 | Cookie đăng nhập, cookie state |
| `Location` | `302`, `201` (SCIM) | Địa chỉ chuyển hướng / tài nguyên mới |
| `Access-Control-Allow-Origin`, `Access-Control-Allow-Credentials`, `Vary: Origin` | `Origin` hợp lệ, chỉ ở `/auth/`, `/oauth2/`, `/.well-known/`, `/admin/`, `/scim/`, `/email/` | CORS phản chiếu đúng origin kèm credentials |
| `Access-Control-Expose-Headers` | CORS | `Content-Location`, `ETag`, `Last-Modified`, `Location`, `RateLimit`, `X-Request-Id`, `X-Trace-Id` |

## 2.1 Cookie

| Cookie | Đặt bởi | Thuộc tính | Hết hạn |
|---|---|---|---|
| `medplum-<clientId>` | phản hồi có `code` mà login thuộc một client (`login`, `profile`, `scope`, `mfa/verify`, `mfa/login-enroll`, `google`, `newproject`, `newpatient`...) | `Path=/`, `HttpOnly`, `Max-Age=3600`; `Secure` + `SameSite=None` khi `AUTH_COOKIE_SECURE=true`, không thì `SameSite=Lax` | xóa bởi `/oauth2/logout` |
| `auth-external-state` | `POST /auth/method`, `/auth/external/start`, `/auth/google` (khi miền dùng IdP) | `Path=/auth/external`, `HttpOnly`, `SameSite=Lax`, `Secure` theo cấu hình | TTL của state; callback `GET /auth/external*` **xóa** cookie này ở mọi phản hồi, kể cả lỗi. Callback không đặt cookie `medplum-<clientId>` |

Giá trị cookie đăng nhập chỉ là **bí mật dùng một phía**; kho lưu băm.

## 3. OperationOutcome

Dạng lỗi và thành công của `/auth/*`, `/admin/*` và route nghiệp vụ `/email/v1/send` (lỗi do **khung** ở `/email` và `/scim`: 404 route tắt, 413, 429 là JSON `{"error"}`, xem mục 6). `Content-Type: application/fhir+json; charset=utf-8`.

**Thành công không nội dung ("All OK")**, dạng 1 (một số route `/auth`, có `id:"ok"`):

```json
{
  "resourceType": "OperationOutcome",
  "id": "ok",
  "issue": [
    { "severity": "information", "code": "informational", "details": { "text": "All OK" } }
  ]
}
```

Dạng 2 (các route `/admin/*` và `/email/v1/send`): **không có `id`**, issue có thêm `details` và `diagnostics` cùng chữ `All OK`:

```json
{
  "resourceType": "OperationOutcome",
  "issue": [
    { "severity": "information", "code": "informational", "details": { "text": "All OK" }, "diagnostics": "All OK" }
  ]
}
```
Client chỉ nên dựa vào HTTP `200` và `issue[0].severity = information`, không dựa vào `id`.

**Lỗi một issue** (`diagnostics` lặp lại `details.text`; `expression` chỉ có khi lỗi gắn với một trường):

```json
{
  "resourceType": "OperationOutcome",
  "issue": [
    {
      "severity": "error",
      "code": "invalid",
      "details": { "text": "Email already registered" },
      "diagnostics": "Email already registered",
      "expression": ["email"]
    }
  ]
}
```

**Lỗi kiểm tra nhiều trường:** mọi lỗi của một yêu cầu gom vào **nhiều issue** (không dừng ở lỗi đầu), thứ tự theo thứ tự kiểm tra của validator.

```json
{
  "resourceType": "OperationOutcome",
  "issue": [
    { "severity": "error", "code": "invalid", "details": { "text": "Invalid email" },    "diagnostics": "Invalid email",    "expression": ["email"] },
    { "severity": "error", "code": "invalid", "details": { "text": "Invalid password" }, "diagnostics": "Invalid password", "expression": ["password"] }
  ]
}
```

**Ánh xạ mã HTTP → `issue[0].code`:**

| HTTP | `code` | Ý nghĩa chung |
|---|---|---|
| 400 | `invalid` | Dữ liệu sai hoặc vi phạm luật nghiệp vụ |
| 401 | `login` | Thiếu hoặc sai Bearer |
| 403 | `forbidden` | Đúng danh tính nhưng không đủ quyền |
| 404 | `not-found` | Không có (hoặc route tắt: thông điệp `Not Found`) |
| 409 | `conflict` | Trùng dữ liệu |
| 413 | `too-long` | Body quá lớn (thông điệp `Request body too large`) |
| 415 | `not-supported` | Content-Type không hỗ trợ |
| 429 | `throttled` | Vượt hạn mức |
| 500 | `exception` | Lỗi nội bộ: thông điệp **chung** `Internal server error`, không lộ chi tiết (chi tiết ở log, khớp `X-Request-Id`) |
| 502 | `exception` | Chỉ `POST /email/v1/send`: thông điệp **riêng, cố định** `Failed to send email` (SMTP lỗi) hoặc `Failed to read attachment` (fhir-server lỗi khi đọc `Binary/<id>`); không lộ phản hồi của máy chủ SMTP |

**Lỗi 429 của hạn mức MFA** có thêm extension và header:

```json
{
  "resourceType": "OperationOutcome",
  "issue": [ { "severity": "error", "code": "throttled", "details": { "text": "Too many requests" }, "diagnostics": "Too many requests" } ],
  "extension": [ { "url": "https://medplum.com/fhir/StructureDefinition/rate-limit-reset", "valueUnsignedInt": 540 } ]
}
```
kèm `Retry-After: 540`. (`valueUnsignedInt` = số giây làm tròn lên tới khi hạn mức được đặt lại; mọi `429` do hạn mức đều mang thông điệp `Too many requests`; riêng `429` truyền lại từ IdP trong luồng exchange có `Retry-After: 0`, không phải số giây còn lại.)

**JSON không đọc được:** `400` `Content could not be parsed`. **Body quá lớn:** `413` `Request body too large`.

## 4. Lỗi OAuth2

Của `/oauth2/*` (RFC 6749 §5.2), `Content-Type: application/json`, kèm `Cache-Control: no-store`. **HTTP `400` cho cả lỗi client** (như Medplum), riêng `userinfo`, `introspect`, `logout` trả `401` khi thiếu hoặc sai Bearer.

```json
{ "error": "invalid_grant", "error_description": "Token already granted" }
```

| `error` | Dùng khi |
|---|---|
| `invalid_request` | Thiếu hoặc sai tham số |
| `invalid_client` | Chỉ ở `pre-authorized_code` khi client **không tồn tại**; client tắt ở cùng grant và mọi lỗi xác thực client khác là `invalid_request` (xem cases/oauth.md) |
| `invalid_grant` | Code/refresh token/mã cấp sẵn sai, hết hạn, đã dùng; MFA chưa xong |
| `invalid_scope` | Scope chứa term `fhir.*` hoặc term bị cấm |
| `access_denied` | Membership/project bị tắt, hoặc AccessPolicy không dịch được sang token; riêng nhánh phiên của `/oauth2/authorize` cũng dùng mã này. **`ipAccessRule` chặn trả `invalid_request`** (theo cases/oauth.md; chưa tự kiểm lại) |
| `unsupported_grant_type` | **Không bao giờ được phát** (có trong ví dụ swagger); `grant_type` lạ trả `invalid_request` với `error_description` `Unsupported grant_type` |
| `server_error` | HTTP `500`, không lộ chi tiết |

`error_description` dành cho lập trình viên, có thể vắng, **không** dùng để rẽ nhánh logic.

## 5. Lỗi SCIM

`Content-Type: application/scim+json`:

```json
{
  "schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"],
  "status": "404",
  "detail": "Not found"
}
```
`status` là **chuỗi**; `scimType` (`invalidFilter`, `invalidPath`, `invalidSyntax`, `invalidValue`, `uniqueness`...) chỉ có khi RFC 7644 quy định. `401` kèm `WWW-Authenticate` hai dòng (Basic và Bearer).

Lưu ý: `401` của `userinfo`, `introspect`, `logout` (thiếu/sai Bearer) là **OperationOutcome** `application/fhir+json`, không phải JSON OAuth (theo cases/oauth.md).

## 6. Lỗi JSON thường

Ở `/.well-known/*` (riêng `oauth-protected-resource` trả `400` dạng **văn bản thuần**), `/health/*`, `/oauth2/*` (lỗi do khung) và **mọi lỗi do khung ở `/scim/*` và `/email/*`** (404 route tắt, 413 body quá lớn, 429): `{"error": "<thông điệp>"}`, ví dụ `{"error":"Not Found"}`, `{"error":"Too many requests"}`. Lỗi nghiệp vụ của `/scim` vẫn là SCIM Error (mục 5), của `/email/v1/send` vẫn là OperationOutcome (mục 3), vì do handler sinh. Chỉ `/auth/*` và `/admin/*` có khung lỗi OperationOutcome.

## 7. Hạn mức IP

| Phạm vi | Cấu hình | Mặc định (staging/production tự bật) |
|---|---|---|
| `/auth/`, `/oauth2/`, `/.well-known/`, `/admin/`, `/scim/`, `/email/` | `AUTH_RATE_LIMIT` | 20 rps, burst 40 mỗi IP |
| `POST /auth/login`, `/auth/profile`, `/auth/scope`, `/auth/method`, `/auth/newuser`, `/auth/newproject`, `/auth/mfa/verify`, `/auth/mfa/login-enroll`, `/auth/mfa/send-email`, `/auth/exchange`, `/auth/google`, `/auth/external/start`, `GET /auth/external[/{clientId}]`; `POST /admin/projects/setpassword`, `.../invite`, `.../members/{id}/resetpassword`, `.../members/{id}/mfa/reset`; `/scim/v2/*`; `/email/v1/send` | `AUTH_LOGIN_RATE_LIMIT` | 1 rps, burst 10 mỗi IP (bộ đếm riêng theo từng nhóm middleware, cộng thêm hạn mức chung ở dòng trên) |

Vượt ⇒ `429`, `Retry-After: 1`, body theo vùng: `/auth`, `/admin` ⇒ OperationOutcome `throttled` `Too many requests`; `/oauth2`, `/.well-known`, `/scim`, `/email` ⇒ `{"error":"Too many requests"}`. Hạn mức đếm theo từng instance trong bộ nhớ (không chia sẻ giữa instance). `/auth/newpatient` **không** có hạn mức riêng (chỉ hạn mức chung). Hạn mức MFA theo login và theo user là cơ chế riêng (mục MFA trong cases/auth.md).

## 8. Mã trạng thái dùng chung

| HTTP | Khi nào (mọi route) |
|---|---|
| 200 | Thành công |
| 201 | Tạo mới (`POST .../client`, SCIM `POST /Users`, `POST /oauth2/register`) |
| 204 | SCIM `DELETE` |
| 302 | Chuyển hướng (`/oauth2/authorize`, callback IdP) |
| 400 | Dữ liệu sai; luật nghiệp vụ; JSON hỏng; OAuth lỗi client |
| 401 | Thiếu/sai Bearer |
| 403 | Không đủ quyền; `cannot identify caller` (không xác định được IP) |
| 404 | Không có / route tắt |
| 409 | Trùng |
| 413 | Body quá lớn |
| 415 | Content-Type không hỗ trợ |
| 429 | Hạn mức |
| 500 | Lỗi nội bộ (thông điệp chung); nhánh **fail đóng** (thiếu phụ thuộc MFA, HIBP, reCAPTCHA) |
| 502 | `POST /email/v1/send` khi SMTP hoặc fhir-server lỗi |
| 503 | `/health/ready` khi đang tắt hoặc DB lỗi |

## 9. Điều kiện bật route

Xem [../api-contract.md mục 5](../api-contract.md). Route tắt trả `404 Not Found` (không phân biệt với route không tồn tại).

## 10. Quy ước ghi trong cases

- **HTTP** và **thông điệp** (`details.text`, `error_description`, `detail`) là nguyên văn từ mã/test; không dịch.
- "Thứ tự kiểm tra" là thứ tự thực tế: khi một yêu cầu vi phạm nhiều điều, lỗi **đầu tiên** trong thứ tự được trả (trừ validator gom nhiều issue).
- Ví dụ dùng giá trị giả; `<...>` là giá trị thay đổi theo yêu cầu.
