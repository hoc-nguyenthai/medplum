# FHIR server Go — request và response (DTO)

Nguồn chuẩn là OpenAPI sinh từ annotation swaggo: `/swagger/index.html` (UI) và `/swagger/doc.json`; file nằm ở `apps/fhir-server/docs/`, sinh lại bằng `make swagger`. Kiểu FHIR (Bundle, OperationOutcome, Parameters, CapabilityStatement, ValueSet) lấy trực tiếp từ `pkg/fhir`. Bảng dưới tóm tắt để đọc nhanh; chỉ các route fhir-server Go đã cài (các route Medplum ở bảng trên chưa cài thì chưa có DTO).

Body gửi lên dùng `application/fhir+json`. Mọi lỗi dưới `/fhir/R4` là `OperationOutcome`; lỗi ngoài `/fhir/R4` là `{"error": "<thông điệp>"}`.

| Route | Request | Response thành công | Lỗi thường gặp |
|---|---|---|---|
| `GET /healthz` | không | 200 `{"status":"ok"}` | |
| `GET /readyz` | không | 200 `{"ready":true,"checks":{"<tên>":true}}` | 503 cùng dạng, `ready:false` |
| `GET /fhir/R4/metadata` | không (công khai) | 200 `CapabilityStatement` | |
| `GET /fhir/R4/{type}` | query search FHIR (`_count`, `_sort`, `_include`...) | 200 `Bundle` (searchset) | 400 tham số sai |
| `POST /fhir/R4/{type}/_search` | form-urlencoded, cùng tham số | 200 `Bundle` | 400 |
| `POST /fhir/R4/{type}` | resource; header `If-None-Exist` (tùy chọn) | 201 resource kèm `Location`, `ETag` | 400 (sai cấu trúc/kiểu; code ngoài ValueSet chưa bị từ chối) |
| `GET /fhir/R4/{type}/{id}` | không | 200 resource | 404, 410 (đã xóa) |
| `PUT /fhir/R4/{type}/{id}` | resource; `If-Match` (tùy chọn) | 200 (hoặc 201 nếu update-as-create) | 400, 412 |
| `PATCH /fhir/R4/{type}/{id}` | JSON Patch `[{"op","path","value"}]` (`application/json-patch+json`) | 200 resource | 400, 404 |
| `DELETE /fhir/R4/{type}/{id}` | không | 204 | 404 |
| `PUT/PATCH/DELETE /fhir/R4/{type}?<search>` | như trên, điều kiện là query | như trên | 412 nếu khớp nhiều |
| `GET /fhir/R4/{type}/{id}/_history` | không | 200 `Bundle` (history) | 404 |
| `GET /fhir/R4/{type}/{id}/_history/{vid}` | không | 200 resource | 404, 410 |
| `POST /fhir/R4` | `Bundle` type `batch` hoặc `transaction` | 200 `Bundle` (`*-response`) | 400 |
| `POST /fhir/R4/{type}/$validate` | resource hoặc `Parameters` | 200 `OperationOutcome` khi hợp lệ hoặc chỉ có cảnh báo (`severity: warning`); **400** `OperationOutcome` khi sai cấu trúc hoặc kiểu dữ liệu (đã kiểm bằng server thật) | |
| `GET/POST /fhir/R4/Patient/{id}/$everything` | không / `Parameters` | 200 `Bundle` | 404 |
| `POST /fhir/R4/{type}/$reindex` (admin) | không | 202 `OperationOutcome` + `Content-Location` job | 403 |
| `POST /fhir/R4/{type}/{id}/$reindex` (admin) | không | 200 `OperationOutcome` | 403, 404 |
| `POST /fhir/R4/{type}/{id}/$expunge` (admin) | không | 200 `OperationOutcome` | 403 |
| `GET /fhir/R4/job/{id}/status` | không | trạng thái job (`Parameters`/`AsyncJob`) | 404 |
| `GET /fhir/R4/$export`, `Patient/$export`, `Group/{id}/$export` | header `Prefer: respond-async` (bắt buộc), query `_type`, `_since`... | 202 + `Content-Location` tới `bulkdata/{id}/status` | 400 (thiếu `Prefer: respond-async`) |
| `GET /fhir/R4/bulkdata/{id}/status` | không | 202 (đang chạy, `Retry-After`) hoặc 200 manifest JSON | 404 |
| `DELETE /fhir/R4/bulkdata/{id}/status` | không | 202 | 404 |
| `POST /fhir/R4/$graphql` | `{"query","variables","operationName"}` | 200 `{"data":{...},"errors":[{"message"}]}` | |
| `GET/POST /fhir/R4/ValueSet/$expand`, `CodeSystem/$lookup` ... | query hoặc `Parameters` | 200 `ValueSet` hoặc `Parameters` | 400, 404 |

Quyền tối thiểu (`RequiredRole`): `read` cho GET, `_search` và các operation không ghi; `write` cho ghi và batch/transaction; `admin` cho `$reindex`, `$expunge`. Thiếu token: 401; thiếu quyền: 403.

## Ví dụ request và response

Các ví dụ lấy từ phản hồi thật của fhir-server (SQLite, `FHIR_AUTH_DISABLED=true`); `id`, `versionId` và thời gian thay đổi theo lần chạy. Thêm `Authorization: Bearer <token>` khi bật xác thực. Host trong `Location`, `fullUrl` và `link` lấy từ cấu hình base URL của server, không lấy từ request.

### Tạo resource — `POST /fhir/R4/Patient`

Request (`Content-Type: application/fhir+json`):

```json
{
  "resourceType": "Patient",
  "name": [{ "family": "Nguyen", "given": ["An"] }],
  "gender": "male",
  "birthDate": "1990-01-01"
}
```

Response `201 Created`, header `Location: <base>/Patient/<id>/_history/<versionId>` và `ETag: W/"<versionId>"`:

```json
{
  "resourceType": "Patient",
  "id": "ab284c0a-ed08-42ea-8598-3f3d5bae4dbf",
  "meta": { "versionId": "f152e3e3-a78e-454a-bb04-0a70fafb7eb4", "lastUpdated": "2026-11-21T09:27:44.092Z" },
  "name": [{ "family": "Nguyen", "given": ["An"] }],
  "gender": "male",
  "birthDate": "1990-01-01"
}
```

### Tìm kiếm — `GET /fhir/R4/Patient?family=Nguyen&_count=1`

Response `200` (Bundle `searchset`; mỗi `entry.resource` là một resource đầy đủ như ở trên):

```json
{
  "resourceType": "Bundle",
  "type": "searchset",
  "timestamp": "2026-11-21T09:27:44Z",
  "link": [{ "relation": "self", "url": "<base>/Patient?_count=1&family=Nguyen" }],
  "entry": [
    {
      "fullUrl": "<base>/Patient/ab284c0a-ed08-42ea-8598-3f3d5bae4dbf",
      "resource": { "resourceType": "Patient", "id": "ab284c0a-ed08-42ea-8598-3f3d5bae4dbf", "...": "..." },
      "search": { "mode": "match" }
    }
  ]
}
```

### Patch — `PATCH /fhir/R4/Patient/{id}`

Request (`Content-Type: application/json-patch+json`):

```json
[{ "op": "replace", "path": "/gender", "value": "female" }]
```

Response `200`: resource sau khi sửa, `meta.versionId` đổi sang phiên bản mới.

### Lịch sử — `GET /fhir/R4/Patient/{id}/_history`

Response `200`: Bundle `history`, `total` là số phiên bản, phiên bản mới nhất đứng đầu. Mỗi `entry` có `fullUrl`, `resource` và `request` (method, url) của lần ghi đó.

### Batch — `POST /fhir/R4`

Request:

```json
{
  "resourceType": "Bundle",
  "type": "batch",
  "entry": [
    { "request": { "method": "GET", "url": "Patient/ab284c0a-ed08-42ea-8598-3f3d5bae4dbf" } },
    { "request": { "method": "GET", "url": "Patient/nope" } }
  ]
}
```

Response `200` (Bundle `batch-response`, mỗi entry thành công hay lỗi độc lập):

```json
{
  "resourceType": "Bundle",
  "type": "batch-response",
  "entry": [
    {
      "fullUrl": "<base>/Patient/ab284c0a-ed08-42ea-8598-3f3d5bae4dbf",
      "resource": { "resourceType": "Patient", "...": "..." },
      "response": { "status": "200 OK", "etag": "W/\"<versionId>\"", "lastModified": "2026-11-21T09:27:44.214Z" }
    },
    {
      "response": {
        "status": "404 Not Found",
        "outcome": {
          "resourceType": "OperationOutcome",
          "issue": [{ "severity": "error", "code": "not-found", "details": { "text": "Not found" }, "diagnostics": "Not found" }]
        }
      }
    }
  ]
}
```

### Lỗi — `OperationOutcome`

`GET /fhir/R4/Patient/nope` → `404`, `Content-Type: application/fhir+json`:

```json
{
  "resourceType": "OperationOutcome",
  "issue": [{ "severity": "error", "code": "not-found", "details": { "text": "Not found" }, "diagnostics": "Not found" }]
}
```

Sai kiểu dữ liệu, ví dụ `{"resourceType":"Patient","active":"x"}` gửi tới `POST /fhir/R4/Patient` → `400`; `POST /fhir/R4/Patient/$validate` với cùng body cũng trả `400` (còn resource đúng cấu trúc mà chỉ thiếu narrative thì trả `200` kèm `warning`):

```json
{
  "resourceType": "OperationOutcome",
  "issue": [{ "severity": "error", "code": "value", "details": { "text": "Cần boolean" }, "diagnostics": "Cần boolean", "expression": ["Patient.active"] }]
}
```

### Job nền — `GET /fhir/R4/$export` (header `Prefer: respond-async`) và `POST /fhir/R4/Patient/$reindex`

Cả hai trả `202 Accepted`, header `Content-Location: <base>/bulkdata/<id>/status` (export) hoặc `<base>/job/<id>/status` (reindex), body:

```json
{
  "resourceType": "OperationOutcome",
  "issue": [{ "severity": "information", "code": "informational", "details": { "text": "Accepted" }, "diagnostics": "Accepted" }]
}
```

Gọi tiếp URL trong `Content-Location` để theo dõi.

### GraphQL — `POST /fhir/R4/$graphql`

Request (`Content-Type: application/json`): `{ "query": "{ PatientList { id } }" }`. Response `200`:

```json
{ "data": { "PatientList": [{ "id": "ab284c0a-ed08-42ea-8598-3f3d5bae4dbf" }] } }
```

### Health

`GET /healthz` → `200 {"status":"ok"}`. `GET /readyz` → `200 {"checks":{"accepting_traffic":true,"database":true},"ready":true}`; khi chưa sẵn sàng trả `503` cùng dạng với `ready: false`.

