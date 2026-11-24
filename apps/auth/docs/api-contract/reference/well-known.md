# Tham chiếu API: nhóm `well-known`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/well-known.md](../cases/well-known.md).

Discovery và khóa công khai: JWKS, openid-configuration, oauth-authorization-server, SMART configuration, protected resource metadata (RFC 9728).

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /.well-known/jwks.json`](#get-well-known-jwks-json) | JWKS (khóa công khai để xác minh token) |
| [`GET /.well-known/oauth-authorization-server`](#get-well-known-oauth-authorization-server) | Discovery OIDC / OAuth 2.0 authorization server |
| [`GET /.well-known/oauth-protected-resource`](#get-well-known-oauth-protected-resource) | Protected resource metadata (RFC 9728) |
| [`GET /.well-known/openid-configuration`](#get-well-known-openid-configuration) | Discovery OIDC / OAuth 2.0 authorization server |
| [`GET /.well-known/smart-configuration`](#get-well-known-smart-configuration) | SMART configuration |
| [`GET /.well-known/smart-styles.json`](#get-well-known-smart-styles-json) | SMART style (màu, font cho ứng dụng nhúng) |

---

<a id="get-well-known-jwks-json"></a>
## `GET /.well-known/jwks.json`

**JWKS (khóa công khai để xác minh token)**

Chỉ khóa công khai; có `ETag` và `Cache-Control: public, max-age=300`.

Các trường hợp phản hồi: [../cases/well-known.md#get-well-known-jwks-json](../cases/well-known.md#get-well-known-jwks-json)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

---

<a id="get-well-known-oauth-authorization-server"></a>
## `GET /.well-known/oauth-authorization-server`

**Discovery OIDC / OAuth 2.0 authorization server**

Cùng một tài liệu ở hai đường dẫn. Chỉ khai những gì đã cài.

Các trường hợp phản hồi: [../cases/well-known.md#get-well-known-oauth-authorization-server](../cases/well-known.md#get-well-known-oauth-authorization-server)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

---

<a id="get-well-known-oauth-protected-resource"></a>
## `GET /.well-known/oauth-protected-resource`

**Protected resource metadata (RFC 9728)**

`resource` tùy chọn phải bắt đầu bằng địa chỉ của auth; mặc định là chính auth.

Các trường hợp phản hồi: [../cases/well-known.md#get-well-known-oauth-protected-resource](../cases/well-known.md#get-well-known-oauth-protected-resource)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `resource` | query | string | không | phải bắt đầu bằng địa chỉ của auth |

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

**`400`** Bad Request

Object tự do (khóa và giá trị tùy ý).

---

<a id="get-well-known-openid-configuration"></a>
## `GET /.well-known/openid-configuration`

**Discovery OIDC / OAuth 2.0 authorization server**

Cùng một tài liệu ở hai đường dẫn. Chỉ khai những gì đã cài.

Các trường hợp phản hồi: [../cases/well-known.md#get-well-known-openid-configuration](../cases/well-known.md#get-well-known-openid-configuration)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

---

<a id="get-well-known-smart-configuration"></a>
## `GET /.well-known/smart-configuration`

**SMART configuration**

SMART on FHIR discovery, khai cùng endpoint với openid-configuration.

Các trường hợp phản hồi: [../cases/well-known.md#get-well-known-smart-configuration](../cases/well-known.md#get-well-known-smart-configuration)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).

---

<a id="get-well-known-smart-styles-json"></a>
## `GET /.well-known/smart-styles.json`

**SMART style (màu, font cho ứng dụng nhúng)**

Giá trị cố định.

Các trường hợp phản hồi: [../cases/well-known.md#get-well-known-smart-styles-json](../cases/well-known.md#get-well-known-smart-styles-json)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Object tự do (khóa và giá trị tùy ý).
