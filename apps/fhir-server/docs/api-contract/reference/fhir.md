# Tham chiếu API: nhóm `fhir`

> **Tệp này được SINH từ `swagger.json`** (Trivita FHIR Server API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/fhir.md](../cases/fhir.md).

FHIR R4 REST: CRUD, search, history, batch/transaction, GraphQL, metadata (CapabilityStatement). Lỗi trả OperationOutcome (application/fhir+json).

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`POST /fhir/R4`](#post-fhir-r4) | Batch / transaction |
| [`POST /fhir/R4/$graphql`](#post-fhir-r4-graphql) | GraphQL |
| [`GET /fhir/R4/metadata`](#get-fhir-r4-metadata) | CapabilityStatement |
| [`GET /fhir/R4/{resourceType}`](#get-fhir-r4-resourcetype) | Tìm kiếm theo loại resource |
| [`POST /fhir/R4/{resourceType}`](#post-fhir-r4-resourcetype) | Tạo resource |
| [`PUT /fhir/R4/{resourceType}`](#put-fhir-r4-resourcetype) | Update/patch/delete có điều kiện |
| [`POST /fhir/R4/{resourceType}/_search`](#post-fhir-r4-resourcetype-search) | Tìm kiếm dạng form (POST _search) |
| [`GET /fhir/R4/{resourceType}/{id}`](#get-fhir-r4-resourcetype-id) | Đọc resource |
| [`PUT /fhir/R4/{resourceType}/{id}`](#put-fhir-r4-resourcetype-id) | Cập nhật resource |
| [`PATCH /fhir/R4/{resourceType}/{id}`](#patch-fhir-r4-resourcetype-id) | Patch resource (JSON Patch) |
| [`DELETE /fhir/R4/{resourceType}/{id}`](#delete-fhir-r4-resourcetype-id) | Xóa resource |
| [`GET /fhir/R4/{resourceType}/{id}/_history`](#get-fhir-r4-resourcetype-id-history) | Lịch sử các phiên bản |
| [`GET /fhir/R4/{resourceType}/{id}/_history/{vid}`](#get-fhir-r4-resourcetype-id-history-vid) | Đọc một phiên bản |

---

<a id="post-fhir-r4"></a>
## `POST /fhir/R4`

**Batch / transaction**

POST Bundle (type batch hoặc transaction) vào gốc. Cần quyền write. Transaction là tất cả hoặc không: một entry lỗi thì rollback toàn bộ. Batch xử lý từng entry độc lập, lỗi nằm trong response của entry đó.

Các trường hợp phản hồi: [../cases/fhir.md#post-fhir-r4](../cases/fhir.md#post-fhir-r4)

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

**Body yêu cầu** (Bundle batch hoặc transaction)

Schema: `httpapi.BundleBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `entry` | mảng `httpapi.BundleEntryBody` | có |  | danh sách entry, xử lý theo thứ tự quy tắc FHIR |  |
| `entry[].request` | object `httpapi.BundleRequestBody` | có |  | phương thức và URL của entry |  |
| `entry[].request.method` | string | có | một trong: `GET`, `POST`, `PUT`, `PATCH`, `DELETE` | phương thức HTTP của entry | `"POST"` |
| `entry[].request.url` | string | có |  | đường dẫn tương đối tới gốc FHIR, ví dụ Patient hoặc Patient/123 | `"Patient"` |
| `entry[].resource` | object `httpapi.PatientBody` | có |  | resource của entry (bỏ qua với GET và DELETE); ví dụ này dùng Patient |  |
| `entry[].resource.resourceType` | string | có |  | luôn "Patient" với body này | `"Patient"` |
| `entry[].resource.active` | boolean | không |  | hồ sơ còn dùng | `true` |
| `entry[].resource.birthDate` | string | không |  | ngày sinh, định dạng YYYY[-MM[-DD]] | `"1990-05-20"` |
| `entry[].resource.gender` | string | không | một trong: `male`, `female`, `other`, `unknown` | giới tính hành chính | `"male"` |
| `entry[].resource.id` | string | không |  | id logic; khi tạo mới server cấp id (bỏ qua giá trị gửi lên, trừ update-as-create) | `"123"` |
| `entry[].resource.identifier` | mảng `httpapi.IdentifierBody` | không |  | định danh nghiệp vụ (mã bệnh nhân...) |  |
| `entry[].resource.identifier[].system` | string | không |  | không gian tên của giá trị (URI) | `"http://hospital.example.org/mrn"` |
| `entry[].resource.identifier[].value` | string | không |  | giá trị định danh trong system | `"MRN-0001"` |
| `entry[].resource.name` | mảng `httpapi.HumanNameBody` | không |  | họ tên |  |
| `entry[].resource.name[].family` | string | không |  | họ | `"Nguyen"` |
| `entry[].resource.name[].given` | mảng string | không |  | tên đệm và tên, mỗi phần tử một từ hoặc cụm | `["Van A"]` |
| `entry[].resource.name[].use` | string | không | một trong: `usual`, `official`, `temp`, `nickname`, `anonymous`, `old`, `maiden` | mục đích dùng tên | `"official"` |
| `entry[].resource.telecom` | mảng `httpapi.ContactPointBody` | không |  | phương thức liên lạc |  |
| `entry[].resource.telecom[].system` | string | không | một trong: `phone`, `fax`, `email`, `pager`, `url`, `sms`, `other` | kênh liên lạc | `"phone"` |
| `entry[].resource.telecom[].use` | string | không | một trong: `home`, `work`, `temp`, `old`, `mobile` | mục đích dùng | `"mobile"` |
| `entry[].resource.telecom[].value` | string | không |  | số điện thoại, địa chỉ email... | `"0900000000"` |
| `entry[].fullUrl` | string | không |  | định danh tạm của entry, để entry khác tham chiếu trong transaction | `"urn:uuid:11111111-1111-1111-1111-111111111111"` |
| `resourceType` | string | có |  | luôn "Bundle" | `"Bundle"` |
| `type` | string | có | một trong: `batch`, `transaction` | batch: từng entry độc lập; transaction: tất cả hoặc không entry nào | `"transaction"` |

```json
{
  "entry": [
    {
      "fullUrl": "urn:uuid:11111111-1111-1111-1111-111111111111",
      "request": {
        "method": "POST",
        "url": "Patient"
      },
      "resource": {
        "active": true,
        "birthDate": "1990-05-20",
        "gender": "male",
        "id": "123",
        "identifier": [],
        "name": [],
        "resourceType": "Patient",
        "telecom": []
      }
    }
  ],
  "resourceType": "Bundle",
  "type": "transaction"
}
```

**Phản hồi**

**`200`** Bundle batch-response hoặc transaction-response

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

**`400`** Bundle sai cấu trúc hoặc type không hỗ trợ

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền write)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-graphql"></a>
## `POST /fhir/R4/$graphql`

**GraphQL**

Truy vấn FHIR bằng GraphQL. Lỗi cú pháp hoặc truy vấn nằm trong trường errors của body, HTTP vẫn 200.

Các trường hợp phản hồi: [../cases/fhir.md#post-fhir-r4-graphql](../cases/fhir.md#post-fhir-r4-graphql)

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

**Body yêu cầu** ({query, variables, operationName})

Schema: `httpapi.GraphQLRequest`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `query` | string | có |  | truy vấn GraphQL | `"{ Patient(id: \"123\") { id name { family } } }"` |
| `operationName` | string | không |  | tên operation cần chạy khi query có nhiều operation | `"GetPatient"` |
| `variables` | object tự do | không |  | giá trị biến của truy vấn |  |

```json
{
  "operationName": "GetPatient",
  "query": "{ Patient(id: \"123\") { id name { family } } }"
}
```

**Phản hồi**

**`200`** OK

Schema: `httpapi.GraphQLResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `data` | object tự do | không |  | kết quả truy vấn; vắng khi lỗi cú pháp |  |
| `errors` | mảng `httpapi.GraphQLError` | không |  | lỗi GraphQL; vắng khi thành công hoàn toàn |  |
| `errors[].message` | string | có |  | thông điệp lỗi | `"Cannot query field \"foo\" on type \"Patient\""` |

```json
{
  "errors": [
    {
      "message": "Cannot query field \"foo\" on type \"Patient\""
    }
  ]
}
```

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-metadata"></a>
## `GET /fhir/R4/metadata`

**CapabilityStatement**

Mô tả server: các loại resource, interaction, search param và operation được hỗ trợ. Công khai, không cần token.

Các trường hợp phản hồi: [../cases/fhir.md#get-fhir-r4-metadata](../cases/fhir.md#get-fhir-r4-metadata)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `fhir.CapabilityStatement`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `contact` | mảng `fhir.ContactDetail` | không |  | Contact details for the publisher |  |
| `copyright` | string | không |  | Use and/or publishing restrictions |  |
| `date` | string | không |  | Date last changed (bắt buộc) |  |
| `description` | string | không |  | Natural language description of the capability statement |  |
| `document` | mảng `fhir.CapabilityStatementDocument` | không |  | Document definition |  |
| `experimental` | boolean | không |  | For testing purposes, not real usage |  |
| `fhirVersion` | string | không |  | FHIR Version the system supports (bắt buộc) |  |
| `format` | mảng string | không |  | formats supported (xml \| json \| ttl \| mime type) (bắt buộc) |  |
| `id` | string | không |  | Logical id of this artifact |  |
| `implementation` | object `fhir.CapabilityStatementImplementation` | không |  | If this describes a specific instance |  |
| `implementationGuide` | mảng string | không |  | Implementation guides supported |  |
| `implicitRules` | string | không |  | A set of rules under which this content was created |  |
| `imports` | mảng string | không |  | Canonical URL of another capability statement this adds to |  |
| `instantiates` | mảng string | không |  | Canonical URL of another capability statement this implements |  |
| `jurisdiction` | mảng `fhir.CodeableConcept` | không |  | Intended jurisdiction for capability statement (if applicable) |  |
| `kind` | string | không |  | instance \| capability \| requirements (bắt buộc) |  |
| `language` | string | không |  | Language of the resource content |  |
| `messaging` | mảng `fhir.CapabilityStatementMessaging` | không |  | If messaging is supported |  |
| `meta` | object `fhir.Meta` | không |  | Metadata about the resource |  |
| `name` | string | không |  | Name for this capability statement (computer friendly) |  |
| `patchFormat` | mảng string | không |  | Patch formats supported |  |
| `publisher` | string | không |  | Name of the publisher (organization or individual) |  |
| `purpose` | string | không |  | Why this capability statement is defined |  |
| `resourceType` | string | không |  |  |  |
| `rest` | mảng `fhir.CapabilityStatementRest` | không |  | If the endpoint is a RESTful one |  |
| `software` | object `fhir.CapabilityStatementSoftware` | không |  | Software that is covered by this capability statement |  |
| `status` | string | không |  | draft \| active \| retired \| unknown (bắt buộc) |  |
| `text` | object `fhir.Narrative` | không |  | Text summary of the resource, for human interpretation |  |
| `title` | string | không |  | Name for this capability statement (human friendly) |  |
| `url` | string | không |  | Canonical identifier for this capability statement, represented as a URI (globally unique) |  |
| `useContext` | mảng `fhir.UsageContext` | không |  | The context that the content is intended to support |  |
| `version` | string | không |  | Business version of the capability statement |  |

```json
{
  "contact": [],
  "document": [],
  "format": [],
  "implementationGuide": [],
  "imports": [],
  "instantiates": [],
  "jurisdiction": [],
  "messaging": [],
  "patchFormat": [],
  "rest": [],
  "useContext": []
}
```

---

<a id="get-fhir-r4-resourcetype"></a>
## `GET /fhir/R4/{resourceType}`

**Tìm kiếm theo loại resource**

Trả Bundle searchset. Ngoài các tham số điều khiển bên dưới, mọi search param của loại resource (ví dụ Patient: family, given, birthdate, identifier; Observation: code, subject, date) truyền qua query string theo chuẩn FHIR, kèm modifier (:exact, :missing...), prefix so sánh, chuỗi chained (subject.name=...) và _has. Danh sách đầy đủ theo từng loại ở GET /fhir/R4/metadata.

Các trường hợp phản hồi: [../cases/fhir.md#get-fhir-r4-resourcetype](../cases/fhir.md#get-fhir-r4-resourcetype)

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
| `resourceType` | đường dẫn | string | có | Loại resource, ví dụ Patient |
| `_count` | query | integer | không | Số kết quả mỗi trang. Ví dụ: 10 |
| `_offset` | query | integer | không | Vị trí bắt đầu (phân trang) |
| `_sort` | query | string | không | Sắp xếp, phân cách bằng dấu phẩy; tiền tố - để giảm dần. Ví dụ: -_lastUpdated,family |
| `_total` | query | string | không | Cách tính tổng |
| `_summary` | query | string | không | Rút gọn kết quả |
| `_elements` | query | string | không | Chỉ trả các phần tử này, phân cách bằng dấu phẩy |
| `_include` | query | string | không | Kèm resource được tham chiếu. Ví dụ: Observation:subject |
| `_revinclude` | query | string | không | Kèm resource tham chiếu tới kết quả. Ví dụ: Observation:subject |
| `_id` | query | string | không | Lọc theo id (nhiều giá trị cách nhau bằng dấu phẩy) |
| `_compartment` | query | string | không | Giới hạn trong compartment của một bệnh nhân. Dạng Patient/<id>, chỉ loại thuộc compartment Patient. Ví dụ: Patient/123 |
| `_lastUpdated` | query | string | không | Lọc theo thời điểm cập nhật, hỗ trợ tiền tố ge/le/gt/lt. Ví dụ: ge2026-01-01 |
| `_filter` | query | string | không | Biểu thức _filter của FHIR |

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

**`400`** Search param không hợp lệ hoặc không hỗ trợ

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-resourcetype"></a>
## `POST /fhir/R4/{resourceType}`

**Tạo resource**

Hỗ trợ If-None-Exist (create có điều kiện). Trả 201 kèm Location và ETag. Sai cấu trúc/kiểu dữ liệu trả 400; giá trị code không thuộc ValueSet (ví dụ gender sai) hiện chưa bị từ chối.

Các trường hợp phản hồi: [../cases/fhir.md#post-fhir-r4-resourcetype](../cases/fhir.md#post-fhir-r4-resourcetype)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `If-None-Exist` | không | Điều kiện search; nếu đã có resource khớp thì không tạo mới. Ví dụ: identifier=123 |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `resourceType` | đường dẫn | string | có | Loại resource |

**Body yêu cầu** (Resource (mẫu là Patient))

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

**`200`** If-None-Exist khớp đúng một resource, trả resource đó

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`201`** Đã tạo; header Location và ETag

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`400`** Sai cấu trúc, kiểu dữ liệu hoặc invariant

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền write)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`412`** If-None-Exist khớp nhiều resource

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="put-fhir-r4-resourcetype"></a>
## `PUT /fhir/R4/{resourceType}`

**Update/patch/delete có điều kiện**

Dùng query search làm điều kiện: PUT, PATCH và DELETE trên /{resourceType}?... (cùng đường dẫn, khác method). Khớp 0 resource: PUT tạo mới, PATCH/DELETE trả 404; khớp nhiều resource trả 412.

Các trường hợp phản hồi: [../cases/fhir.md#put-fhir-r4-resourcetype](../cases/fhir.md#put-fhir-r4-resourcetype)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

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

**Body yêu cầu** (Resource (PUT, mẫu là Patient) hoặc JSON Patch (PATCH))

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

**`200`** Đã cập nhật

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`201`** Không có resource khớp, đã tạo mới (PUT)

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`400`** Thiếu điều kiện search hoặc body sai

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền write)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`412`** Điều kiện khớp nhiều resource

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhir-r4-resourcetype-search"></a>
## `POST /fhir/R4/{resourceType}/_search`

**Tìm kiếm dạng form (POST _search)**

Tương đương GET /fhir/R4/{resourceType} nhưng tham số đi trong body form-urlencoded, dùng khi query string quá dài. Cũng nhận mọi search param của loại resource.

Các trường hợp phản hồi: [../cases/fhir.md#post-fhir-r4-resourcetype-search](../cases/fhir.md#post-fhir-r4-resourcetype-search)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/x-www-form-urlencoded` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/x-www-form-urlencoded` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `resourceType` | đường dẫn | string | có | Loại resource |
| `_count` | form | integer | không | Số kết quả mỗi trang. Ví dụ: 10 |
| `_offset` | form | integer | không | Vị trí bắt đầu (phân trang) |
| `_sort` | form | string | không | Sắp xếp, phân cách bằng dấu phẩy; tiền tố - để giảm dần. Ví dụ: -_lastUpdated,family |
| `_total` | form | string | không | Cách tính tổng |
| `_summary` | form | string | không | Rút gọn kết quả |
| `_elements` | form | string | không | Chỉ trả các phần tử này, phân cách bằng dấu phẩy |
| `_include` | form | string | không | Kèm resource được tham chiếu. Ví dụ: Observation:subject |
| `_revinclude` | form | string | không | Kèm resource tham chiếu tới kết quả. Ví dụ: Observation:subject |
| `_id` | form | string | không | Lọc theo id (nhiều giá trị cách nhau bằng dấu phẩy) |
| `_lastUpdated` | form | string | không | Lọc theo thời điểm cập nhật, hỗ trợ tiền tố ge/le/gt/lt. Ví dụ: ge2026-01-01 |
| `_filter` | form | string | không | Biểu thức _filter của FHIR |

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

**`400`** Search param không hợp lệ

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-resourcetype-id"></a>
## `GET /fhir/R4/{resourceType}/{id}`

**Đọc resource**

Hỗ trợ If-None-Match (ETag) và If-Modified-Since: trả 304 nếu không đổi.

Các trường hợp phản hồi: [../cases/fhir.md#get-fhir-r4-resourcetype-id](../cases/fhir.md#get-fhir-r4-resourcetype-id)

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
| `resourceType` | đường dẫn | string | có | Loại resource |
| `id` | đường dẫn | string | có | Id |

**Phản hồi**

**`200`** Header ETag = W/\"versionId\

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`304`** Không đổi so với If-None-Match / If-Modified-Since

Không có body.

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`410`** Đã bị xóa

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="put-fhir-r4-resourcetype-id"></a>
## `PUT /fhir/R4/{resourceType}/{id}`

**Cập nhật resource**

Hỗ trợ If-Match (versionId) và update-as-create nếu bật. Body phải có id khớp với đường dẫn.

Các trường hợp phản hồi: [../cases/fhir.md#put-fhir-r4-resourcetype-id](../cases/fhir.md#put-fhir-r4-resourcetype-id)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/fhir+json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/fhir+json` |
| `If-Match` | không | ETag phiên bản đang sửa, ví dụ W/\ |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `resourceType` | đường dẫn | string | có | Loại resource |
| `id` | đường dẫn | string | có | Id |

**Body yêu cầu** (Resource (mẫu là Patient))

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

**`200`** Đã cập nhật; header ETag mới

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`201`** Tạo mới (update-as-create)

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`400`** Sai cấu trúc hoặc id không khớp

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền write)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tồn tại và update-as-create đang tắt

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`412`** If-Match không khớp phiên bản hiện tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="patch-fhir-r4-resourcetype-id"></a>
## `PATCH /fhir/R4/{resourceType}/{id}`

**Patch resource (JSON Patch)**

Body là mảng thao tác RFC 6902; Content-Type bắt buộc application/json-patch+json. Hỗ trợ If-Match.

Các trường hợp phản hồi: [../cases/fhir.md#patch-fhir-r4-resourcetype-id](../cases/fhir.md#patch-fhir-r4-resourcetype-id)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json-patch+json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json-patch+json` |
| `If-Match` | không | ETag phiên bản đang sửa |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `resourceType` | đường dẫn | string | có | Loại resource |
| `id` | đường dẫn | string | có | Id |

**Body yêu cầu** (Các thao tác JSON Patch)

Mảng các phần tử `httpapi.JSONPatchOp`.

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `op` | string | có | một trong: `add`, `remove`, `replace`, `move`, `copy`, `test` | thao tác RFC 6902 | `"replace"` |
| `path` | string | có |  | JSON Pointer tới vị trí cần sửa | `"/gender"` |
| `from` | string | không |  | nguồn, bắt buộc với move và copy | `"/telecom/0"` |
| `value` | object | không |  | giá trị mới, bắt buộc với add, replace và test |  |

```json
{
  "from": "/telecom/0",
  "op": "replace",
  "path": "/gender"
}
```

**Phản hồi**

**`200`** Đã patch; header ETag mới

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`400`** Patch sai cú pháp hoặc kết quả không hợp lệ

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền write)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`412`** If-Match không khớp phiên bản hiện tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`415`** Content-Type không phải application/json-patch+json

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="delete-fhir-r4-resourcetype-id"></a>
## `DELETE /fhir/R4/{resourceType}/{id}`

**Xóa resource**

Xóa mềm: resource vẫn nằm trong _history, đọc lại trả 410. Xóa vĩnh viễn dùng $expunge.

Các trường hợp phản hồi: [../cases/fhir.md#delete-fhir-r4-resourcetype-id](../cases/fhir.md#delete-fhir-r4-resourcetype-id)

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

**`204`** No Content

Không có body.

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền write)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-resourcetype-id-history"></a>
## `GET /fhir/R4/{resourceType}/{id}/_history`

**Lịch sử các phiên bản**

Trả Bundle type history, phiên bản mới nhất trước. Gồm cả phiên bản đã xóa.

Các trường hợp phản hồi: [../cases/fhir.md#get-fhir-r4-resourcetype-id-history](../cases/fhir.md#get-fhir-r4-resourcetype-id-history)

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
| `_count` | query | integer | không | Số phiên bản mỗi trang. Ví dụ: 10 |
| `_offset` | query | integer | không | Vị trí bắt đầu |

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

**`404`** Không tồn tại

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhir-r4-resourcetype-id-history-vid"></a>
## `GET /fhir/R4/{resourceType}/{id}/_history/{vid}`

**Đọc một phiên bản**

Đọc đúng phiên bản vid của resource.

Các trường hợp phản hồi: [../cases/fhir.md#get-fhir-r4-resourcetype-id-history-vid](../cases/fhir.md#get-fhir-r4-resourcetype-id-history-vid)

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
| `resourceType` | đường dẫn | string | có | Loại resource |
| `id` | đường dẫn | string | có | Id |
| `vid` | đường dẫn | string | có | versionId |

**Phản hồi**

**`200`** OK

Schema: `httpapi.FHIRResource`

Object tự do (khóa và giá trị tùy ý).

**`401`** Thiếu hoặc sai Bearer token

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền (cần quyền read)

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có phiên bản này

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`410`** Phiên bản là bản xóa

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).
