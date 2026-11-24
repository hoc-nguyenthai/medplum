# fhir-server: hợp đồng API

Cập nhật 2026-11-23. **Chỉ mô tả phần `fhir-server` đang phục vụ.** Tài liệu chi tiết (tham chiếu Medplum gốc: REST, 113 operation, ví dụ JSON; có nhãn "CHƯA CÀI" ở phần chưa port) nằm ở thư mục [api-contract/](api-contract/) cạnh tệp này.

> Hợp đồng chi tiết từng route (quy ước chung, bảng trường sinh từ swagger, từng trường hợp phản hồi và các điểm lệch/rủi ro đã đo): [api-contract/common.md](api-contract/common.md), [api-contract/reference/](api-contract/reference/), [api-contract/cases/](api-contract/cases/). Khi lệch, mã và bộ đó thắng.

| Nguồn | Vai trò |
|---|---|
| `swagger.json` / `swagger.yaml` (cùng thư mục) | **Nguồn chuẩn** của route, tham số, DTO. Sinh từ annotation: `make swagger APP=fhir-server` |
| Tệp này | Quy ước chung và bảng route; bảng **sinh từ `swagger.json`** |
| `GET /fhir/R4/metadata` | `CapabilityStatement` do server phát lúc chạy: danh sách loại resource, search param từng loại, interaction đã hỗ trợ. **Nguồn chuẩn của "server hỗ trợ gì" ở mức FHIR** |
| `/swagger/index.html`, `/swagger/doc.json` | Cùng spec, xem trực tiếp |
| [fhir-server-go.md](api-contract/fhir-server-go.md) | Ví dụ JSON thật của từng route (viết tay) |

**Giới hạn của việc kiểm tự động.** Router FHIR (`/fhir/R4/...`) là một `http.Handler` gắn bằng wildcard vào Echo (`pkg/fhirrouter`), nên Echo không liệt kê được từng route của nó. Vì vậy:

| Test | Kiểm gì |
|---|---|
| `internal/bootstrap` `TestRoutesMatchSwaggerContract` | Mọi route Echo **ngoài** `/fhir/R4` (health...) khớp `swagger.json`, và router FHIR có được gắn |
| `docs` `TestAPIContractIsUpToDate` | Bảng route dưới đây khớp `swagger.json` (`UPDATE_DOCS=1 go test ./apps/fhir-server/docs -run TestAPIContractIsUpToDate`) |
| `docs` `TestSwaggerDTOsAreComplete` | DTO của fhir-server (`httpapi.*`) thiếu mô tả trường, `required`, ví dụ ở body mẫu; route thiếu tóm tắt/mô tả/schema phản hồi/mô tả tham số; tag thiếu mô tả |
| `docs` `TestSwaggerIsStructurallyValid` | Tham số path lệch mẫu hoặc thiếu `required`, `$ref` hỏng, `required` không phải trường thật, tham số query/path có `example` (không hợp lệ ở Swagger 2.0), `produces` không khớp Content-Type thật |
| Test của `pkg/fhirrouter` và e2e | Hành vi thật của các route FHIR |

Nghĩa là **việc swagger liệt kê đúng các route `/fhir/R4/...` do annotation quyết định và chưa có test máy so với router**. Khi thêm hoặc đổi route FHIR, sửa annotation và sinh lại.

> **Hai kiểu FHIR được khai báo rút gọn thành object tự do** trong `swagger.json`/`swagger.yaml`: `fhir.Extension` (mọi `extension`) và `fhir.ParametersParameter` (phần tử `Parameters.parameter`). Override ở `apps/fhir-server/.swaggo`, dùng khi `make swagger APP=fhir-server`. Lý do: `value[x]` của chúng kéo theo khoảng 25 kiểu dữ liệu FHIR (`Address`, `Dosage`, `TimingRepeat`, `DataRequirement`...), làm spec to gần gấp rưỡi và làm IDE chậm khi mở `swagger.yaml`. Cấu trúc thật theo chuẩn FHIR R4 (xem `CapabilityStatement` ở `GET /fhir/R4/metadata`).
>
> **Content-Type thật** (đã kiểm bằng cách gọi server): mọi route `/fhir/R4/...` trả `application/fhir+json`, kể cả lỗi (`OperationOutcome`); trừ `POST /fhir/R4/$graphql` và manifest 200 của `bulkdata/{id}/status` trả `application/json`; `/healthz`, `/readyz` trả `application/json`. `GET /fhir/R4/bulkdata/{id}/status` khi export đang chạy (202) **không có body**, chỉ có header `X-Progress` và `Retry-After`.

## 1. Địa chỉ và phiên bản

- Cổng mặc định `:8005`. Gốc FHIR là `/fhir/R4` (FHIR R4). Không có tiền tố phiên bản khác.
- Chạy sau `auth`: token do `auth` ký, `fhir-server` xác minh bằng JWKS (`FHIR_JWKS_URL`, `FHIR_JWT_ISSUER`, `FHIR_JWT_AUDIENCE`; xem [README](../README.md)).

## 2. Xác thực và quyền

`Authorization: Bearer <token>`. Quyền tối thiểu theo route (`RequiredRole`):

| Vai trò | Cho |
|---|---|
| `read` | `GET`, `_search`, operation không ghi |
| `write` | ghi (`POST`, `PUT`, `PATCH`, `DELETE`) và batch/transaction |
| `admin` | `$reindex`, `$expunge` |

`GET /fhir/R4/metadata` và probe công khai. Thiếu hoặc sai token: `401`; thiếu quyền: `403`. Dữ liệu được tách theo project theo claim `fhir_project` của token (`pkg/fhirauth`); hai project không thấy dữ liệu nhau.

## 3. Định dạng

- Body và phản hồi FHIR: `application/fhir+json`. `PATCH` dùng `application/json-patch+json`. `POST .../_search` dùng form-urlencoded.
- Lỗi dưới `/fhir/R4`: `OperationOutcome`. Lỗi ngoài `/fhir/R4`: `{"error": "<thông điệp>"}`.
- Điều kiện: `If-None-Exist` (create có điều kiện), `If-Match` (update theo phiên bản), update/patch/delete có điều kiện qua query (`412` nếu khớp nhiều).
- Phản hồi tạo/cập nhật kèm `Location` và `ETag: W/"<versionId>"`.
- Bulk export bất đồng bộ: `Prefer: respond-async` bắt buộc; `202` + `Content-Location` tới `bulkdata/{id}/status`; trạng thái `202` (đang chạy, `Retry-After`) hoặc `200` kèm manifest.

## 4. Bảng route

Mỗi nhóm là một `tag` của swagger. Loại resource là tham số đường dẫn `{resourceType}`; danh sách loại và search param từng loại ở `GET /fhir/R4/metadata`.

<!-- BEGIN GENERATED ROUTES (từ swagger.json; chạy UPDATE_DOCS=1 go test để sinh lại, đừng sửa tay) -->

### Nhóm `bulk` (3 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /fhir/R4/$export` | Bulk export ($export) | Bearer | query: _type, _since, _outputFormat | 202 `fhir.OperationOutcome` | 400, 401, 403 |
| `GET /fhir/R4/bulkdata/{id}/status` | Trạng thái bulk export | Bearer | - | 200 `httpapi.ExportManifest`, 202 | 401, 403, 404 |
| `DELETE /fhir/R4/bulkdata/{id}/status` | Hủy bulk export | Bearer | - | 202 `fhir.OperationOutcome` | 401, 403, 404 |

### Nhóm `fhir` (13 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `POST /fhir/R4` | Batch / transaction | Bearer | body `httpapi.BundleBody` | 200 `fhir.Bundle` | 400, 401, 403 |
| `POST /fhir/R4/$graphql` | GraphQL | Bearer | body `httpapi.GraphQLRequest` | 200 `httpapi.GraphQLResponse` | 401, 403 |
| `GET /fhir/R4/metadata` | CapabilityStatement | công khai | - | 200 `fhir.CapabilityStatement` | - |
| `GET /fhir/R4/{resourceType}` | Tìm kiếm theo loại resource | Bearer | query: _count, _offset, _sort, _total, _summary, _elements, _include, _revinclude, _id, _compartment, _lastUpdated, _filter | 200 `fhir.Bundle` | 400, 401, 403 |
| `POST /fhir/R4/{resourceType}` | Tạo resource | Bearer | body `httpapi.PatientBody` | 200 `httpapi.FHIRResource`, 201 `httpapi.FHIRResource` | 400, 401, 403, 412 |
| `PUT /fhir/R4/{resourceType}` | Update/patch/delete có điều kiện | Bearer | body `httpapi.PatientBody` | 200 `httpapi.FHIRResource`, 201 `httpapi.FHIRResource` | 400, 401, 403, 412 |
| `POST /fhir/R4/{resourceType}/_search` | Tìm kiếm dạng form (POST _search) | Bearer | form: _count, _offset, _sort, _total, _summary, _elements, _include, _revinclude, _id, _lastUpdated, _filter | 200 `fhir.Bundle` | 400, 401, 403 |
| `GET /fhir/R4/{resourceType}/{id}` | Đọc resource | Bearer | - | 200 `httpapi.FHIRResource`, 304 | 401, 403, 404, 410 |
| `PUT /fhir/R4/{resourceType}/{id}` | Cập nhật resource | Bearer | body `httpapi.PatientBody` | 200 `httpapi.FHIRResource`, 201 `httpapi.FHIRResource` | 400, 401, 403, 404, 412 |
| `PATCH /fhir/R4/{resourceType}/{id}` | Patch resource (JSON Patch) | Bearer | body `[]httpapi.JSONPatchOp` | 200 `httpapi.FHIRResource` | 400, 401, 403, 404, 412, 415 |
| `DELETE /fhir/R4/{resourceType}/{id}` | Xóa resource | Bearer | - | 204 | 401, 403, 404 |
| `GET /fhir/R4/{resourceType}/{id}/_history` | Lịch sử các phiên bản | Bearer | query: _count, _offset | 200 `fhir.Bundle` | 401, 403, 404 |
| `GET /fhir/R4/{resourceType}/{id}/_history/{vid}` | Đọc một phiên bản | Bearer | - | 200 `httpapi.FHIRResource` | 401, 403, 404, 410 |

### Nhóm `health` (2 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /healthz` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /readyz` | Readiness probe (kiểm tra phụ thuộc) | công khai | - | 200 `httpapi.ReadyResponse` | 503 |

### Nhóm `operation` (11 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `POST /fhir/R4/CodeSystem/$import` | CodeSystem/$import theo system (không cần id) | Bearer | body `fhir.Parameters` | 200 `fhir.Parameters` | 400, 401, 403, 404 |
| `POST /fhir/R4/CodeSystem/{id}/$import` | CodeSystem/$import (nạp concept cho bảng mã lớn) | Bearer | body `fhir.Parameters` | 200 `fhir.Parameters` | 400, 401, 403, 404 |
| `POST /fhir/R4/ConceptMap/$import` | ConceptMap/$import theo url (không cần id) | Bearer | body `fhir.Parameters` | 200 `fhir.ConceptMap` | 400, 401, 403, 404 |
| `POST /fhir/R4/ConceptMap/{id}/$import` | ConceptMap/$import (nạp ánh xạ cho bảng ánh xạ lớn) | Bearer | body `fhir.Parameters` | 200 `fhir.ConceptMap` | 400, 401, 403, 404 |
| `POST /fhir/R4/PackageRelease/{id}/$install` | PackageRelease/$install (nạp gói định nghĩa) | Bearer | - | 200 `fhir.Bundle` | 400, 401, 403, 404 |
| `GET /fhir/R4/Patient/{id}/$everything` | Patient/$everything | Bearer | query: _count, _since, _type | 200 `fhir.Bundle` | 401, 403, 404 |
| `GET /fhir/R4/job/{id}/status` | Trạng thái job nền | Bearer | - | 200 `fhir.Parameters` | 401, 403, 404 |
| `POST /fhir/R4/{resourceType}/$reindex` | $reindex cả loại resource (job nền) | Bearer | - | 202 `fhir.OperationOutcome` | 401, 403 |
| `POST /fhir/R4/{resourceType}/$validate` | $validate | Bearer | body `httpapi.PatientBody` | 200 `fhir.OperationOutcome` | 400, 401, 403 |
| `POST /fhir/R4/{resourceType}/{id}/$expunge` | $expunge (xóa vĩnh viễn) | Bearer | - | 200 `fhir.OperationOutcome` | 401, 403, 404 |
| `POST /fhir/R4/{resourceType}/{id}/$reindex` | $reindex một resource (đồng bộ) | Bearer | - | 200 `fhir.OperationOutcome` | 401, 403, 404 |

### Nhóm `terminology` (2 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /fhir/R4/CodeSystem/$lookup` | CodeSystem/$lookup | Bearer | query: system, code, version, property | 200 `fhir.Parameters` | 400, 401, 403, 404 |
| `GET /fhir/R4/ValueSet/$expand` | ValueSet/$expand | Bearer | query: url, valueSetVersion, filter, count, offset | 200 `fhir.ValueSet` | 400, 401, 403, 404 |

<!-- END GENERATED ROUTES -->

## 5. Ngoài bảng

- `GET /healthz` (liveness), `GET /readyz` (readiness: `{"ready":true,"checks":{...}}`, `503` cùng dạng khi chưa sẵn sàng).
- Subscription websocket do `ws-gateway` phục vụ khi `FHIR_WS_MODE=issuer`; hợp đồng ở [api-contract.md](../../ws-gateway/docs/api-contract.md).
