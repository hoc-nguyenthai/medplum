# Tham chiếu API: nhóm `terminology`

> **Tệp này được SINH từ `swagger.json`** (Trivita FHIR Server API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/terminology.md](../cases/terminology.md).

Terminology cơ bản: ValueSet/$expand và CodeSystem/$lookup.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /fhir/R4/CodeSystem/$lookup`](#get-fhir-r4-codesystem-lookup) | CodeSystem/$lookup |
| [`GET /fhir/R4/ValueSet/$expand`](#get-fhir-r4-valueset-expand) | ValueSet/$expand |

---

<a id="get-fhir-r4-codesystem-lookup"></a>
## `GET /fhir/R4/CodeSystem/$lookup`

**CodeSystem/$lookup**

Tra cứu thông tin một code trong CodeSystem: display, definition, property.

Các trường hợp phản hồi: [../cases/terminology.md#get-fhir-r4-codesystem-lookup](../cases/terminology.md#get-fhir-r4-codesystem-lookup)

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
| `system` | query | string | có | URL của CodeSystem |
| `code` | query | string | có | Code cần tra |
| `version` | query | string | không | Phiên bản CodeSystem |
| `property` | query | string | không | Property cần trả, lặp tham số để chọn nhiều |

**Phản hồi**

**`200`** OK

Schema: `fhir.Parameters`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `id` | string | không |  | Logical id of this artifact |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `language` | string | không |  | Language of the resource content |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `parameter` | mảng object | không |  | Operation Parameter |  |
| `resourceType` | string | không |  |  |  |

```json
{
  "parameter": []
}
```

**`400`** Thiếu system hoặc code

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tìm thấy code

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-valueset-expand"></a>
## `GET /fhir/R4/ValueSet/$expand`

**ValueSet/$expand**

Mở rộng ValueSet thành danh sách code. Terminology còn có $lookup, $validate-code, $subsumes, $translate (GET và POST) với cùng dạng đường dẫn.

Các trường hợp phản hồi: [../cases/terminology.md#get-fhir-r4-valueset-expand](../cases/terminology.md#get-fhir-r4-valueset-expand)

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
| `url` | query | string | có | Canonical URL của ValueSet, có thể kèm \|version |
| `valueSetVersion` | query | string | không | Phiên bản ValueSet (nếu url không có \|version) |
| `filter` | query | string | không | Lọc code/display chứa chuỗi này |
| `count` | query | integer | không | Số code tối đa |
| `offset` | query | integer | không | Vị trí bắt đầu |

**Phản hồi**

**`200`** OK

Schema: `fhir.ValueSet`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `compose` | object `fhir.ValueSetCompose` | không |  | Content logical definition of the value set (CLD) |  |
| `contact` | mảng `fhir.ContactDetail` | không |  | Contact details for the publisher |  |
| `copyright` | string | không |  | Use and/or publishing restrictions |  |
| `date` | string | không |  | Date last changed |  |
| `description` | string | không |  | Natural language description of the value set |  |
| `expansion` | object `fhir.ValueSetExpansion` | không |  | Used when the value set is "expanded" |  |
| `experimental` | boolean | không |  | For testing purposes, not real usage |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | mảng `fhir.Identifier` | không |  | Additional identifier for the value set (business identifier) |  |
| `immutable` | boolean | không |  | Indicates whether or not any change to the content logical definition may occur |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `jurisdiction` | mảng `fhir.CodeableConcept` | không |  | Intended jurisdiction for value set (if applicable) |  |
| `language` | string | không |  | Language of the resource content |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `name` | string | không |  | Name for this value set (computer friendly) |  |
| `publisher` | string | không |  | Name of the publisher (organization or individual) |  |
| `purpose` | string | không |  | Why this value set is defined |  |
| `resourceType` | string | không |  |  |  |
| `status` | string | không |  | draft \| active \| retired \| unknown (bắt buộc) |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `title` | string | không |  | Name for this value set (human friendly) |  |
| `url` | string | không |  | Canonical identifier for this value set, represented as a URI (globally unique) |  |
| `useContext` | mảng `fhir.UsageContext` | không |  | The context that the content is intended to support |  |
| `version` | string | không |  | Business version of the value set |  |

```json
{
  "contact": [],
  "identifier": [],
  "jurisdiction": [],
  "useContext": []
}
```

**`400`** Thiếu url hoặc tham số sai

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tìm thấy ValueSet

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).
