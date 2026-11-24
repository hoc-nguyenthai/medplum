# Tham chiếu API: nhóm `bulk`

> **Tệp này được SINH từ `swagger.json`** (Trivita FHIR Server API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/bulk.md](../cases/bulk.md).

Bulk Data Access: $export bất đồng bộ, theo dõi trạng thái và hủy; tệp kết quả là NDJSON tải qua Binary.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /fhir/R4/$export`](#get-fhir-r4-export) | Bulk export ($export) |
| [`GET /fhir/R4/bulkdata/{id}/status`](#get-fhir-r4-bulkdata-id-status) | Trạng thái bulk export |
| [`DELETE /fhir/R4/bulkdata/{id}/status`](#delete-fhir-r4-bulkdata-id-status) | Hủy bulk export |

---

<a id="get-fhir-r4-export"></a>
## `GET /fhir/R4/$export`

**Bulk export ($export)**

Cũng có Patient/$export và Group/{id}/$export. Bắt buộc header Prefer: respond-async. Trả 202 kèm Content-Location trỏ tới bulkdata/{id}/status; gọi URL đó (poll) cho tới khi nhận 200 và manifest.

Các trường hợp phản hồi: [../cases/bulk.md#get-fhir-r4-export](../cases/bulk.md#get-fhir-r4-export)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Prefer` | có | Bắt buộc: respond-async |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `_type` | query | string | không | Chỉ export các loại resource này, phân cách bằng dấu phẩy |
| `_since` | query | string | không | Chỉ export resource cập nhật từ thời điểm này |
| `_outputFormat` | query | string | không | Định dạng tệp; chỉ hỗ trợ application/fhir+ndjson |

**Phản hồi**

**`202`** Job đã nhận; header Content-Location tới bulkdata/{id}/status

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`400`** Thiếu Prefer: respond-async hoặc tham số sai

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-bulkdata-id-status"></a>
## `GET /fhir/R4/bulkdata/{id}/status`

**Trạng thái bulk export**

Poll sau khi $export. 202 là đang chạy (header X-Progress, Retry-After); 200 là đã xong kèm manifest các tệp NDJSON; mỗi url trong output tải qua GET (cần token).

Các trường hợp phản hồi: [../cases/bulk.md#get-fhir-r4-bulkdata-id-status](../cases/bulk.md#get-fhir-r4-bulkdata-id-status)

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
| `id` | đường dẫn | string | có | Export id |

**Phản hồi**

**`200`** Đã xong

Schema: `httpapi.ExportManifest`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `error` | mảng object | có |  | các lỗi từng phần (OperationOutcome); rỗng khi không có |  |
| `output` | mảng `httpapi.ExportOutputFile` | có |  | các tệp kết quả |  |
| `output[].type` | string | có |  | loại resource trong tệp NDJSON | `"Patient"` |
| `output[].url` | string | có |  | địa chỉ tải tệp (Binary) | `"http://localhost:8005/fhir/R4/Binary/abc123"` |
| `output[].count` | integer | không |  | số resource trong tệp | `120` |
| `request` | string | có |  | URL yêu cầu export ban đầu | `"http://localhost:8005/fhir/R4/$export?_type=Patient"` |
| `requiresAccessToken` | boolean | có |  | true khi tải tệp cần Bearer token | `true` |
| `transactionTime` | string | có | định dạng date-time | thời điểm chụp dữ liệu của export | `"2026-11-21T10:00:00Z"` |

```json
{
  "error": [],
  "output": [
    {
      "count": 120,
      "type": "Patient",
      "url": "http://localhost:8005/fhir/R4/Binary/abc123"
    }
  ],
  "request": "http://localhost:8005/fhir/R4/$export?_type=Patient",
  "requiresAccessToken": true,
  "transactionTime": "2026-11-21T10:00:00Z"
}
```

**`202`** Đang chạy; header X-Progress: in-progress và Retry-After (giây)

Không có body.

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Export không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="delete-fhir-r4-bulkdata-id-status"></a>
## `DELETE /fhir/R4/bulkdata/{id}/status`

**Hủy bulk export**

Hủy job export đang chạy.

Các trường hợp phản hồi: [../cases/bulk.md#delete-fhir-r4-bulkdata-id-status](../cases/bulk.md#delete-fhir-r4-bulkdata-id-status)

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
| `id` | đường dẫn | string | có | Export id |

**Phản hồi**

**`202`** Đã nhận yêu cầu hủy

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Export không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).
