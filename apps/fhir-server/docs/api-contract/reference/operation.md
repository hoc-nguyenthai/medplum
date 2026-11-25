# Tham chiếu API: nhóm `operation`

> **Tệp này được SINH từ `swagger.json`** (Trivita FHIR Server API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/operation.md](../cases/operation.md).

Operation FHIR ($validate, $everything, $reindex, $expunge, Project/$rate-limits, PackageRelease/$install, CodeSystem/$import, ConceptMap/$import) và trạng thái job nền. $reindex, $expunge, $install và $import cần quyền admin.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`POST /fhir/R4/CodeSystem/$import`](#post-fhir-r4-codesystem-import) | CodeSystem/$import theo system (không cần id) |
| [`POST /fhir/R4/CodeSystem/{id}/$import`](#post-fhir-r4-codesystem-id-import) | CodeSystem/$import (nạp concept cho bảng mã lớn) |
| [`POST /fhir/R4/ConceptMap/$import`](#post-fhir-r4-conceptmap-import) | ConceptMap/$import theo url (không cần id) |
| [`POST /fhir/R4/ConceptMap/{id}/$import`](#post-fhir-r4-conceptmap-id-import) | ConceptMap/$import (nạp ánh xạ cho bảng ánh xạ lớn) |
| [`POST /fhir/R4/PackageRelease/{id}/$install`](#post-fhir-r4-packagerelease-id-install) | PackageRelease/$install (nạp gói định nghĩa) |
| [`GET /fhir/R4/Patient/{id}/$everything`](#get-fhir-r4-patient-id-everything) | Patient/$everything |
| [`GET /fhir/R4/Project/{id}/$rate-limits`](#get-fhir-r4-project-id-rate-limits) | Tình trạng hạn mức điểm của project (Project/$rate-limits) |
| [`GET /fhir/R4/job/{id}/status`](#get-fhir-r4-job-id-status) | Trạng thái job nền |
| [`POST /fhir/R4/{resourceType}/$reindex`](#post-fhir-r4-resourcetype-reindex) | $reindex cả loại resource (job nền) |
| [`POST /fhir/R4/{resourceType}/$validate`](#post-fhir-r4-resourcetype-validate) | $validate |
| [`POST /fhir/R4/{resourceType}/{id}/$expunge`](#post-fhir-r4-resourcetype-id-expunge) | $expunge (xóa vĩnh viễn) |
| [`POST /fhir/R4/{resourceType}/{id}/$reindex`](#post-fhir-r4-resourcetype-id-reindex) | $reindex một resource (đồng bộ) |

---

<a id="post-fhir-r4-codesystem-import"></a>
## `POST /fhir/R4/CodeSystem/$import`

**CodeSystem/$import theo system (không cần id)**

Như CodeSystem/{id}/$import nhưng chọn CodeSystem bằng tham số system (url canonical) trong body.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-codesystem-import](../cases/operation.md#post-fhir-r4-codesystem-import)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Parameters có system)

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

**`400`** Thiếu system, property chưa khai báo hoặc code không xác định

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** CodeSystem không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-codesystem-id-import"></a>
## `POST /fhir/R4/CodeSystem/{id}/$import`

**CodeSystem/$import (nạp concept cho bảng mã lớn)**

Cần quyền admin project. Body Parameters: system (uri; bỏ nếu dùng đường dẫn có id), concept (valueCoding: code, display), property (part code, property, value) và designation (part code, language, value); mỗi loại tối đa 10000 phần tử mỗi lần gọi. Concept lưu ở bảng dẫn xuất, không sửa JSON của CodeSystem. Property phải khai báo trong CodeSystem.property (hoặc là parent/hierarchyMeaning), code phải đã có hoặc có trong cùng lần gọi; lỗi thì hoàn tác cả lần gọi. Gọi lại với cùng dữ liệu là an toàn. Trả Parameters với return là CodeSystem.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-codesystem-id-import](../cases/operation.md#post-fhir-r4-codesystem-id-import)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `id` | đường dẫn | string | có | Id của CodeSystem |

**Body yêu cầu** (Parameters)

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

**`400`** Thiếu CodeSystem, property chưa khai báo hoặc code không xác định

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** CodeSystem không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-conceptmap-import"></a>
## `POST /fhir/R4/ConceptMap/$import`

**ConceptMap/$import theo url (không cần id)**

Như ConceptMap/{id}/$import nhưng chọn ConceptMap bằng tham số url (canonical) trong body.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-conceptmap-import](../cases/operation.md#post-fhir-r4-conceptmap-import)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Parameters có url)

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

**Phản hồi**

**`200`** OK

Schema: `fhir.ConceptMap`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `contact` | mảng `fhir.ContactDetail` | không |  | Contact details for the publisher |  |
| `copyright` | string | không |  | Use and/or publishing restrictions |  |
| `date` | string | không |  | Date last changed |  |
| `description` | string | không |  | Natural language description of the concept map |  |
| `experimental` | boolean | không |  | For testing purposes, not real usage |  |
| `group` | mảng `fhir.ConceptMapGroup` | không |  | Same source and target systems |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | object `fhir.Identifier` | không |  | Additional identifier for the concept map |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `jurisdiction` | mảng `fhir.CodeableConcept` | không |  | Intended jurisdiction for concept map (if applicable) |  |
| `language` | string | không |  | Language of the resource content |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `name` | string | không |  | Name for this concept map (computer friendly) |  |
| `publisher` | string | không |  | Name of the publisher (organization or individual) |  |
| `purpose` | string | không |  | Why this concept map is defined |  |
| `resourceType` | string | không |  |  |  |
| `sourceCanonical` | string | không |  | The source value set that contains the concepts that are being mapped |  |
| `sourceUri` | string | không |  | The source value set that contains the concepts that are being mapped |  |
| `status` | string | không |  | draft \| active \| retired \| unknown (bắt buộc) |  |
| `targetCanonical` | string | không |  | The target value set which provides context for the mappings |  |
| `targetUri` | string | không |  | The target value set which provides context for the mappings |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `title` | string | không |  | Name for this concept map (human friendly) |  |
| `url` | string | không |  | Canonical identifier for this concept map, represented as a URI (globally unique) |  |
| `useContext` | mảng `fhir.UsageContext` | không |  | The context that the content is intended to support |  |
| `version` | string | không |  | Business version of the concept map |  |

```json
{
  "contact": [],
  "group": [],
  "jurisdiction": [],
  "useContext": []
}
```

**`400`** Thiếu url, mapping, source.code hoặc target.system

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** ConceptMap không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-conceptmap-id-import"></a>
## `POST /fhir/R4/ConceptMap/{id}/$import`

**ConceptMap/$import (nạp ánh xạ cho bảng ánh xạ lớn)**

Cần quyền admin project. Body Parameters: mapping (lặp, tối đa 10000 mỗi lần gọi; part source và target kiểu Coding, target.system bắt buộc, để trống target.code cho ánh xạ rỗng; relationship mặc định equivalent; comment; property, dependsOn, product với part code và value[x]). Ánh xạ lưu ở bảng dẫn xuất, không sửa JSON của ConceptMap, và ConceptMap/$translate đọc từ đó. Nạp lại cùng cặp nguồn và đích là ghi đè. Trả ConceptMap.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-conceptmap-id-import](../cases/operation.md#post-fhir-r4-conceptmap-id-import)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `id` | đường dẫn | string | có | Id của ConceptMap |

**Body yêu cầu** (Parameters)

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

**Phản hồi**

**`200`** OK

Schema: `fhir.ConceptMap`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `contact` | mảng `fhir.ContactDetail` | không |  | Contact details for the publisher |  |
| `copyright` | string | không |  | Use and/or publishing restrictions |  |
| `date` | string | không |  | Date last changed |  |
| `description` | string | không |  | Natural language description of the concept map |  |
| `experimental` | boolean | không |  | For testing purposes, not real usage |  |
| `group` | mảng `fhir.ConceptMapGroup` | không |  | Same source and target systems |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | object `fhir.Identifier` | không |  | Additional identifier for the concept map |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `jurisdiction` | mảng `fhir.CodeableConcept` | không |  | Intended jurisdiction for concept map (if applicable) |  |
| `language` | string | không |  | Language of the resource content |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `name` | string | không |  | Name for this concept map (computer friendly) |  |
| `publisher` | string | không |  | Name of the publisher (organization or individual) |  |
| `purpose` | string | không |  | Why this concept map is defined |  |
| `resourceType` | string | không |  |  |  |
| `sourceCanonical` | string | không |  | The source value set that contains the concepts that are being mapped |  |
| `sourceUri` | string | không |  | The source value set that contains the concepts that are being mapped |  |
| `status` | string | không |  | draft \| active \| retired \| unknown (bắt buộc) |  |
| `targetCanonical` | string | không |  | The target value set which provides context for the mappings |  |
| `targetUri` | string | không |  | The target value set which provides context for the mappings |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `title` | string | không |  | Name for this concept map (human friendly) |  |
| `url` | string | không |  | Canonical identifier for this concept map, represented as a URI (globally unique) |  |
| `useContext` | mảng `fhir.UsageContext` | không |  | The context that the content is intended to support |  |
| `version` | string | không |  | Business version of the concept map |  |

```json
{
  "contact": [],
  "group": [],
  "jurisdiction": [],
  "useContext": []
}
```

**`400`** Thiếu mapping, source.code hoặc target.system

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** ConceptMap không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-packagerelease-id-install"></a>
## `POST /fhir/R4/PackageRelease/{id}/$install`

**PackageRelease/$install (nạp gói định nghĩa)**

Cần quyền admin project. Đọc Bundle (batch hoặc transaction) trong Binary mà PackageRelease.content.url trỏ tới (dạng Binary/<id>, tối đa 64 MiB) và chạy với quyền của người gọi. Ghi một PackageInstallation: installing rồi installed, hoặc error khi có entry lỗi (transaction được hoàn tác). Trả Bundle kết quả.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-packagerelease-id-install](../cases/operation.md#post-fhir-r4-packagerelease-id-install)

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
| `id` | đường dẫn | string | có | Id của PackageRelease |

**Phản hồi**

**`200`** OK

Schema: `fhir.Bundle`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `entry` | mảng `fhir.BundleEntry` | không |  | Entry in the bundle - will have a resource or information |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | object `fhir.Identifier` | không |  | Persistent identifier for the bundle |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `language` | string | không |  | Language of the resource content |  |
| `link` | mảng `fhir.BundleLink` | không |  | Links related to this Bundle |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `resourceType` | string | không |  |  |  |
| `signature` | object `fhir.Signature` | không |  | Digital Signature |  |
| `timestamp` | string | không |  | When the bundle was assembled |  |
| `total` | integer | không |  | If search, the total number of matches |  |
| `type` | string | không |  | document \| message \| transaction \| transaction-response \| batch \| batch-response \| history \| searchset \| collection (bắt buộc) |  |

```json
{
  "entry": [],
  "link": []
}
```

**`400`** Gói không phải Bundle JSON, content.url sai dạng hoặc có entry lỗi

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** PackageRelease hoặc Binary không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-patient-id-everything"></a>
## `GET /fhir/R4/Patient/{id}/$everything`

**Patient/$everything**

Trả Bundle gồm hồ sơ Patient và các resource thuộc compartment của bệnh nhân đó.

Các trường hợp phản hồi: [../cases/operation.md#get-fhir-r4-patient-id-everything](../cases/operation.md#get-fhir-r4-patient-id-everything)

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
| `id` | đường dẫn | string | có | Patient id |
| `_count` | query | integer | không | Số resource mỗi trang. Ví dụ: 10 |
| `_since` | query | string | không | Chỉ lấy resource cập nhật từ thời điểm này |
| `_type` | query | string | không | Chỉ lấy các loại resource này, phân cách bằng dấu phẩy |

**Phản hồi**

**`200`** OK

Schema: `fhir.Bundle`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `entry` | mảng `fhir.BundleEntry` | không |  | Entry in the bundle - will have a resource or information |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `identifier` | object `fhir.Identifier` | không |  | Persistent identifier for the bundle |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `language` | string | không |  | Language of the resource content |  |
| `link` | mảng `fhir.BundleLink` | không |  | Links related to this Bundle |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `resourceType` | string | không |  |  |  |
| `signature` | object `fhir.Signature` | không |  | Digital Signature |  |
| `timestamp` | string | không |  | When the bundle was assembled |  |
| `total` | integer | không |  | If search, the total number of matches |  |
| `type` | string | không |  | document \| message \| transaction \| transaction-response \| batch \| batch-response \| history \| searchset \| collection (bắt buộc) |  |

```json
{
  "entry": [],
  "link": []
}
```

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Patient không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-project-id-rate-limits"></a>
## `GET /fhir/R4/Project/{id}/$rate-limits`

**Tình trạng hạn mức điểm của project (Project/$rate-limits)**

Cần quyền admin và id phải là project của chính người gọi (403 nếu khác). KHÔNG trừ điểm. Trả Parameters: `enabled` (boolean) và, khi FHIR_QUOTA_USER hoặc FHIR_QUOTA_PROJECT bật, `windowSeconds` cùng `userLimit`, `userRemaining`, `userResetSeconds` (của chính người gọi) và `projectLimit`, `projectRemaining`, `projectResetSeconds` (cả project) cho hạn mức đang bật.

Các trường hợp phản hồi: [../cases/operation.md#get-fhir-r4-project-id-rate-limits](../cases/operation.md#get-fhir-r4-project-id-rate-limits)

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
| `id` | đường dẫn | string | có | id project (phải là project của người gọi) |

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

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Không phải admin, hoặc id khác project của người gọi

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-job-id-status"></a>
## `GET /fhir/R4/job/{id}/status`

**Trạng thái job nền**

Trạng thái của job tạo bởi $reindex (và các job nền khác): accepted, in-progress, completed hoặc error, kèm kết quả trong Parameters.

Các trường hợp phản hồi: [../cases/operation.md#get-fhir-r4-job-id-status](../cases/operation.md#get-fhir-r4-job-id-status)

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
| `id` | đường dẫn | string | có | Job id |

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

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Job không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-resourcetype-reindex"></a>
## `POST /fhir/R4/{resourceType}/$reindex`

**$reindex cả loại resource (job nền)**

Cần quyền admin. Trả 202 kèm Content-Location tới /fhir/R4/job/{id}/status.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-resourcetype-reindex](../cases/operation.md#post-fhir-r4-resourcetype-reindex)

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
| `resourceType` | đường dẫn | string | có | Loại resource |

**Phản hồi**

**`202`** Job đã nhận; header Content-Location

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-resourcetype-validate"></a>
## `POST /fhir/R4/{resourceType}/$validate`

**$validate**

Kiểm tra resource theo cấu trúc, kiểu, cardinality và invariant FHIRPath mà không lưu. Kết quả luôn là OperationOutcome; lỗi dữ liệu nằm trong issue, không phải mã HTTP 4xx.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-resourcetype-validate](../cases/operation.md#post-fhir-r4-resourcetype-validate)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `resourceType` | đường dẫn | string | có | Loại resource |

**Body yêu cầu** (Resource cần kiểm tra (hoặc Parameters có parameter resource))

Schema: `httpapi.PatientBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `resourceType` | string | có |  | luôn "Patient" với body này | `"Patient"` |
| `active` | boolean | không |  | hồ sơ còn dùng | `true` |
| `birthDate` | string | không |  | ngày sinh, định dạng YYYY[-MM[-DD]] | `"1990-05-20"` |
| `gender` | string | không | một trong: `male`, `female`, `other`, `unknown` | giới tính hành chính | `"male"` |
| `id` | string | không |  | id logic; khi tạo mới server cấp id (bỏ qua giá trị gửi lên, trừ update-as-create) | `"123"` |
| `identifier` | mảng `httpapi.IdentifierBody` | không |  | định danh nghiệp vụ (mã bệnh nhân...) |  |
| `identifier[].system` | string | không |  | không gian tên của giá trị (URI) | `"http://hospital.example.org/mrn"` |
| `identifier[].value` | string | không |  | giá trị định danh trong system | `"MRN-0001"` |
| `name` | mảng `httpapi.HumanNameBody` | không |  | họ tên |  |
| `name[].family` | string | không |  | họ | `"Nguyen"` |
| `name[].given` | mảng string | không |  | tên đệm và tên, mỗi phần tử một từ hoặc cụm | `["Van A"]` |
| `name[].use` | string | không | một trong: `usual`, `official`, `temp`, `nickname`, `anonymous`, `old`, `maiden` | mục đích dùng tên | `"official"` |
| `telecom` | mảng `httpapi.ContactPointBody` | không |  | phương thức liên lạc |  |
| `telecom[].system` | string | không | một trong: `phone`, `fax`, `email`, `pager`, `url`, `sms`, `other` | kênh liên lạc | `"phone"` |
| `telecom[].use` | string | không | một trong: `home`, `work`, `temp`, `old`, `mobile` | mục đích dùng | `"mobile"` |
| `telecom[].value` | string | không |  | số điện thoại, địa chỉ email... | `"0900000000"` |

```json
{
  "active": true,
  "birthDate": "1990-05-20",
  "gender": "male",
  "id": "123",
  "identifier": [
    {
      "system": "http://hospital.example.org/mrn",
      "value": "MRN-0001"
    }
  ],
  "name": [
    {
      "family": "Nguyen",
      "given": [
        "Van A"
      ],
      "use": "official"
    }
  ],
  "resourceType": "Patient",
  "telecom": [
    {
      "system": "phone",
      "use": "mobile",
      "value": "0900000000"
    }
  ]
}
```

**Phản hồi**

**`200`** Các issue tìm thấy (nếu có)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`400`** Body không phải JSON FHIR hợp lệ

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-resourcetype-id-expunge"></a>
## `POST /fhir/R4/{resourceType}/{id}/$expunge`

**$expunge (xóa vĩnh viễn)**

Cần quyền admin. Xóa vĩnh viễn resource cùng mọi phiên bản, không hoàn tác.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-resourcetype-id-expunge](../cases/operation.md#post-fhir-r4-resourcetype-id-expunge)

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
| `resourceType` | đường dẫn | string | có | Loại resource |
| `id` | đường dẫn | string | có | Id |

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-resourcetype-id-reindex"></a>
## `POST /fhir/R4/{resourceType}/{id}/$reindex`

**$reindex một resource (đồng bộ)**

Cần quyền admin. Chạy ngay trong request, trả khi xong.

Các trường hợp phản hồi: [../cases/operation.md#post-fhir-r4-resourcetype-id-reindex](../cases/operation.md#post-fhir-r4-resourcetype-id-reindex)

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
| `resourceType` | đường dẫn | string | có | Loại resource |
| `id` | đường dẫn | string | có | Id |

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền admin)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).
