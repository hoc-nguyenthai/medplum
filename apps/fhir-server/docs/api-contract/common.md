# fhir-server: quy ước chung của hợp đồng API

Đọc trước các tệp [reference/](reference/) (bảng trường, sinh từ swagger) và [cases/](cases/) (từng trường hợp phản hồi). Tệp này chứa những điều lặp ở mọi route. Mã thắng khi lệch; tài liệu cũ hơn ([../api-contract.md](../api-contract.md), [fhir-rest.md](fhir-rest.md), [fhir-operations.md](fhir-operations.md), [fhir-server-go.md](fhir-server-go.md)) còn chứa phần tham chiếu Medplum gốc, có phần chưa cài.

## 1. Địa chỉ và điều kiện có route

| Phần | Giá trị |
|---|---|
| Cổng | `:8005` (mặc định) |
| Gốc FHIR | `/fhir/R4` (hằng `Prefix`, `apps/fhir-server/internal/presentation/httpapi`) |
| Ngoài FHIR | `/healthz`, `/readyz`, `/swagger/*`, WebSocket Subscription (khi bật), `/storage/*` (khi bật kho Binary ngoài) |
| Phiên bản | chỉ R4, không có tiền tố phiên bản khác |

`swagger.json` chỉ liệt kê **31 route**; router FHIR (`pkg/fhirrouter`) là một `http.Handler` gắn bằng wildcard nên phục vụ thêm các route không có trong swagger, ví dụ cập nhật, vá và xóa **có điều kiện** theo query (`PUT`, `PATCH`, `DELETE` trên `/fhir/R4/{resourceType}`), `$validate-code`, `$subsumes`, `$translate`, `$get-ws-binding-token` và các operation khác (xem `r.Add(...)` trong `pkg/fhirrouter` và `GET /fhir/R4/metadata`). `cases/` chỉ phủ route có trong swagger, trừ khi mục nói khác.

Route mới chưa tồn tại thì Echo trả `404` dạng OperationOutcome (mục 3), còn route FHIR đúng nhưng không có handler cho phương thức đó do `pkg/fhirrouter` trả lỗi của nó.

## 2. Header phản hồi chung

Mọi phản hồi (trừ `/swagger/*`, được bỏ qua) mang: `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `Permissions-Policy: ...`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection: 1; mode=block`, `X-Request-Id` (Echo `RequestID`). `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload` chỉ có khi `BaseURL` là HTTPS (`pkg/httpsec`).

CORS (`pkg/httpsec.CORS`, chỉ cho đường dẫn dưới `/fhir/R4` và `/storage/`): origin nào trong `FHIR_ALLOWED_ORIGINS` mới được; rỗng thì không origin nào. Header phản hồi trình duyệt đọc được: `Content-Location`, `ETag`, `Last-Modified`, `Location`, `RateLimit`, `X-Request-Id`, `X-Trace-Id`.

`Content-Type` phản hồi: `application/fhir+json; charset=utf-8` cho mọi route dưới `/fhir/R4`, kể cả lỗi; trừ `POST /fhir/R4/$graphql` và manifest 200 của `bulkdata/{id}/status` là `application/json`; probe `/healthz`, `/readyz` là JSON thường.

## 3. Định dạng lỗi

- **Dưới `/fhir/R4`:** `OperationOutcome` với một `issue`:

```json
{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"invalid","details":{"text":"JSON không hợp lệ: ..."},"diagnostics":"JSON không hợp lệ: ..."}]}
```

  `details.text` và `diagnostics` luôn cùng một thông điệp (`newError`, `pkg/fhirrouter/router.go`). Bảng mã `issue.code` theo HTTP: 400 `invalid`, 401 `login`, 403 `forbidden`, 404 `not-found`, 405 `not-supported`, 409 `conflict`, 410 `deleted`, 412 `conflict`, 422 `processing`, 501 `not-supported`, 500 `exception`.
- **Ngoài `/fhir/R4`:** `{"error":"<thông điệp>"}`; lỗi 5xx không lộ chi tiết nội bộ.
- Lỗi do Echo ở tầng ngoài router FHIR (xác thực, hạn mức, 404 không có route, thân quá lớn) được **chuyển thành OperationOutcome** (`errorHandler`): 401 `Unauthorized(msg)`, 403 `Forbidden(msg)`, 404 `Không có route cho <METHOD> <path>` (path có tiền tố `/fhir/R4` khi lỗi do Echo; router FHIR bỏ tiền tố, ví dụ `Không có route cho GET Foo`), 429 với `code=invalid`, 413 `Body quá lớn` (`code=invalid`).

## 4. Xác thực và quyền

Chế độ production là JWT do `auth` ký (`FHIR_JWKS_URL`, `FHIR_JWT_ISSUER`, `FHIR_JWT_AUDIENCE`); có chế độ token tĩnh và chế độ tắt xác thực chỉ để dev (actor `anonymous`).

| Tình huống | Kết quả |
|---|---|
| `GET /fhir/R4/metadata` | công khai, không kiểm token |
| Thiếu header `Authorization: Bearer ...` | `401`, `WWW-Authenticate: Bearer realm="fhir"`, thông điệp `Thiếu bearer token` (JWT) hoặc `Thiếu hoặc sai bearer token` (token tĩnh) |
| Token JWT sai chữ ký, hết hạn, sai `iss`/`aud`, lỗi tải JWKS | `401`, `WWW-Authenticate: Bearer realm="fhir", error="invalid_token"`, thông điệp `Token không hợp lệ` (chi tiết chỉ vào log) |
| JWT đúng nhưng thiếu claim project (trừ super-admin) hoặc thiếu mọi scope FHIR | `403` `Token không có quyền truy cập FHIR` |
| Vai trò của token thấp hơn vai trò route cần | `403` `Token không đủ quyền cho thao tác này` |
| Header `X-Fhir-Project` khác project của token (không phải super-admin) | `403` `Token không được chọn project khác` |
| Super-admin không gửi `X-Fhir-Project` hợp lệ (`^[A-Za-z0-9._:-]{1,128}$`) | `400` `Super-admin phải chọn project bằng header X-Fhir-Project` |

**Vai trò tối thiểu theo route** (`RequiredRole`, `apps/fhir-server/internal/presentation/httpapi/auth.go`):

| Vai trò | Route |
|---|---|
| `read` | `GET`/`HEAD`/`OPTIONS`, `POST .../_search`, `$validate`, `$everything`, `$expand`, `$lookup`, `$validate-code`, `$subsumes`, `$translate`, `$graphql`, `$get-ws-binding-token`, mọi method của `bulkdata/{id}/status` (kể cả `DELETE`: hủy export; chỉ người tạo job hoặc admin, xem `cases/bulk.md`) |
| `admin` | `$reindex`, `$expunge`, `$import`, `$install`, và mọi truy cập **trực tiếp vào loại `AsyncJob`** (đọc, tìm kiếm, `POST`/`PUT`/`PATCH`/`DELETE`, kể cả điều kiện). Theo dõi, hủy job dùng `bulkdata/{id}/status` và `job/{id}/status` (vai trò `read`, và phải là **người tạo job hoặc admin**, người khác `404`) |
| `write` | mọi thao tác còn lại,

`HEAD` được `Guard` xếp vào vai trò `read` nhưng router FHIR trả `405` cho `HEAD /{resourceType}/{id}` (chưa có handler). `Location` chỉ có ở `201`, không có ở `PUT`/`PATCH` trả `200`.

Vai trò suy từ scope `fhir.read` / `fhir.write` / `fhir.admin` (hoặc scope SMART `system/*.*`, theo loại; `fhir.super` chỉ khi `AllowSuperAdmin`).

> **Entry của batch/transaction được kiểm vai trò như yêu cầu ngoài (sửa 2026-11-24).** Trước đó `Guard` chỉ xét method và đường dẫn của yêu cầu ngoài (`POST /fhir/R4` chỉ cần `write`), nên token `write` gọi thẳng `POST /fhir/R4/Patient/$reindex` bị `403` nhưng cùng thao tác làm entry của Bundle lọt vào handler (batch `202` tạo `AsyncJob`; transaction `200`; `$expunge` chỉ bị chặn bởi quyền SMART `d`; transaction chứa `$reindex` còn làm treo kho). Nay `fhirrouter.Options.AuthorizeEntry` (nối với `RequiredRole` ở `httpapi.AuthorizeEntry`) kiểm **từng entry** theo method và đường dẫn tương đối ngay khi nhận Bundle: batch đồng bộ trả `403` riêng cho entry bị từ chối và vẫn chạy các entry còn lại; transaction và batch async (`Prefer: respond-async`) từ chối **cả Bundle** với `403` (OperationOutcome có `Bundle.entry[i]`), không entry nào chạy và không tạo job. Test: `TestBatchAndTransactionCannotEscalateRole` (e2e, token tĩnh và JWT), `TestAuthorizeEntry*` (`pkg/fhirrouter`), `TestAuthorizeEntryAppliesRequiredRole`. Cùng bản sửa nâng `$import`, `$install` và ghi trực tiếp `AsyncJob` lên `admin` ở cổng HTTP (cả hai chế độ xác thực), nên chế độ token tĩnh cũng bị chặn (test `TestStaticTokenCannotRunAdminOnlyOperations`).

Quyền chi tiết theo loại resource, compartment, `AccessPolicy` và `hiddenFields` do `pkg/fhirauth` kiểm tiếp ở từng handler; xem mục Thứ tự kiểm tra trong `cases/`.

**Cô lập project:** dữ liệu tách theo claim project (mặc định `fhir_project`, `FHIR_JWT_PROJECT_CLAIM`); hai project không thấy dữ liệu nhau. Super-admin chọn project bằng `X-Fhir-Project` và **mọi yêu cầu của họ được ghi log kiểm toán** (`"audit": true`), kể cả bị từ chối.

## 5. Hạn mức, kích thước

| Giới hạn | Giá trị | Phản hồi |
|---|---|---|
| Hạn mức theo IP dưới `/fhir/R4` | `FHIR_RATE_LIMIT` yêu cầu/giây/IP; staging/production không đặt thì mặc định **20**; `0` tắt; burst mặc định gấp đôi, tối thiểu 1 | `429` OperationOutcome `quá nhiều yêu cầu, thử lại sau`, `Retry-After: 1`; `403` `không xác định được người gọi` nếu không xác định được IP |
| Phạm vi hạn mức | đứng **trước** xác thực (chặn cả việc dò token); không áp cho `/healthz`, `/readyz`, `/swagger/*`, WebSocket; `/storage/*` có bộ đếm riêng ("storage") | |
| Cách đếm | bộ nhớ từng replica; `FHIR_RATE_LIMIT_SHARED=true` thì dùng chung qua Redis (Redis lỗi: tạm rơi về bộ đếm riêng của replica) | |
| Thân yêu cầu | tối đa **16 MiB** (`BodyLimit`); POST/PUT `Binary` và `/storage/*` khi bật kho ngoài tự đặt trần riêng | `413` OperationOutcome `Body quá lớn` |

## 6. Mã HTTP chung

| Mã | Khi nào |
|---|---|
| `200`, `201` | đọc, tìm kiếm / tạo (kèm `Location`, `ETag`) |
| `202` | chấp nhận việc bất đồng bộ (`$export`, `$reindex` cả loại, batch async): `Content-Location` tới trạng thái. `$import` và `$install` chạy **đồng bộ** và trả `200` |
| `400` | JSON/form hỏng, tham số sai (`invalid`) |
| `401`, `403` | mục 4 |
| `404`, `410` | không có / đã xóa (xóa mềm) |
| `405` | phương thức không hỗ trợ (`not-supported`) |
| `409`, `412` | xung đột phiên bản, điều kiện không khớp (`If-Match`, `If-None-Exist` nhiều kết quả) |
| `413`, `415` | thân quá lớn / `Content-Type không hỗ trợ: <mt>` (`PATCH cần Content-Type application/json-patch+json`) |
| `422` | không xử lý được nội dung (`processing`) |
| `429` | hạn mức (mục 5) |
| `500`, `501` | lỗi hạ tầng không lộ chi tiết / chưa cài (`not-supported`) |
| `503` | `/readyz` khi chưa sẵn sàng |

**Content-Type chấp nhận cho thân** (thân rỗng được nhận với mọi `Content-Type`) (`pkg/fhirrouter/http.go`): rỗng, `application/json` hoặc bất kỳ `*+json` (kể cả `application/fhir+json`, `application/json-patch+json`); `application/x-www-form-urlencoded` (gộp vào query); `application/graphql` (thân là chuỗi truy vấn). Khác: `415`.

## 7. Quy ước trong `cases/`

- Một tệp mỗi tag của swagger (`fhir`, `operation`, `bulk`, `terminology`, `health`); mỗi route một `<a id="slug">`, slug = chữ thường, ký tự không phải chữ/số thành `-`, bỏ `-` đầu cuối (`apicontract.Slug`).
- Thông điệp lỗi, mã HTTP, header nguyên văn từ mã/test; không chắc ghi `(chưa kiểm)`.
- Mục chung (không có `<a id>`) liên kết bằng slug tiêu đề kiểu GitHub.
