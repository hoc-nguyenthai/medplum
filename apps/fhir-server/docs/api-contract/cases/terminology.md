# Trường hợp phản hồi: nhóm `terminology`

Hai thao tác thuật ngữ chỉ đọc trên CodeSystem và ValueSet LƯU TRONG CHÍNH SERVER: `$lookup` (tra một mã) và `$expand` (mở rộng ValueSet thành danh sách mã). Không có máy chủ thuật ngữ ngoài: SNOMED, LOINC, ICD... chỉ dùng được khi đã nạp CodeSystem tương ứng vào server. Tham chiếu trường: [../reference/terminology.md](../reference/terminology.md). Định dạng lỗi, xác thực, hạn mức dùng chung: [../common.md](../common.md#3-định-dạng-lỗi).

Phạm vi tài liệu này là hai route trong swagger (`GET`). Mã còn đăng ký thêm (không có mục riêng ở đây): `POST` cùng đường dẫn (tham số trong body `Parameters`, hỗ trợ `coding`, `valueSet` nhúng), dạng theo id (`CodeSystem/{id}/$lookup`, `ValueSet/{id}/$expand`), `$validate-code`, `$subsumes`, `$translate`. Các dạng đó dùng chung hàm xử lý nên phần "Thứ tự kiểm tra" và thông điệp lỗi bên dưới đúng cho cả chúng.

**Quyền:** token cần vai trò đọc (`RequiredRole`: mọi đường dẫn kết thúc `/$lookup`, `/$expand`, `/$validate-code`, `/$subsumes`, `/$translate` là `read`, kể cả POST). Thiếu token 401, vai trò thấp hơn read 403 `Token không đủ quyền cho thao tác này`. Tra cứu chạy bằng repository của người gọi nên bị tách theo project: CodeSystem/ValueSet của project khác không thấy được (404 như không tồn tại). Token SMART/AccessPolicy không có quyền search trên `CodeSystem` hoặc `ValueSet` nhận 403 từ repository (thông điệp dạng `Token thiếu quyền ... (SMART) trên CodeSystem` hoặc `Chính sách truy cập không cho phép CodeSystem`, chưa kiểm nguyên văn cho nhánh search).

**Tra theo kho nào:** CodeSystem tìm theo canonical `url` (và `version` nếu có) bằng search `url=` trong kho resource của project; nhiều phiên bản cùng url thì lấy phiên bản có chuỗi `version` lớn nhất (so sánh chuỗi, không phải semver). Có hai đường:

| CodeSystem | Nguồn mã | Dùng khi |
|---|---|---|
| `content` khác `not-present` (hoặc repository không cài `ConceptStore`) | mảng `concept` nằm trong resource CodeSystem, lập chỉ mục trong bộ nhớ mỗi request | bảng mã nhỏ lưu cả trong resource |
| `content = not-present` và repository có `ConceptStore` | bảng concept riêng (truy vấn `GetConcept`, `QueryConcepts`), không đọc resource | bảng mã lớn nạp riêng; thiếu `definition` trong kết quả `$lookup`, không hỗ trợ filter `regex` trong `$expand` (422) |

Giới hạn chung: ValueSet mở rộng tối đa 100000 mã (`maxExpansion`), quá thì 422; `$lookup` trả tối đa 1000 mã `child` cho một concept ở kho riêng (`maxLookupChildren`). Không hỗ trợ: CodeSystem ngoài server, `$closure`, filter `other`, phiên bản theo ngày.

Hai route đều là thao tác đọc: không ghi dữ liệu, không sinh history, không phát sự kiện Redis `fhir.resource.changed`, không kích hoạt Subscription.

<a id="get-fhir-r4-codesystem-lookup"></a>
## `GET /fhir/R4/CodeSystem/$lookup`

Tra một mã trong một CodeSystem và trả `Parameters` gồm tên, display, phiên bản, định nghĩa, designation, quan hệ cha con và property. [Tham chiếu trường](../reference/terminology.md#get-fhir-r4-codesystem-lookup)

**Tiền điều kiện / cờ bật route:** luôn bật. Cần CodeSystem đã có trong project.

**Thứ tự kiểm tra** (theo `Router.lookup`):
1. Hạn mức IP (429), xác thực Bearer (401), vai trò read (403): xem common.
2. `ParseInput` đọc query (GET không có body). Body không phải object (chỉ xảy ra với POST) -> 400 `Body phải là một resource hoặc Parameters`.
3. `code` rỗng: thử `coding` trong body Parameters (chỉ POST); vẫn rỗng -> 400 `Thiếu tham số code (hoặc coding)`.
4. `system` rỗng -> 400 `Thiếu tham số system`. `version` (nếu có và `system` chưa chứa `|`) ghép thành `system|version`.
5. Tìm CodeSystem theo canonical: không có -> 404 `CodeSystem không tồn tại: <system>` (có `|version` nếu đã ghép).
6. Chọn kho: `content=not-present` và có `ConceptStore` -> đường kho riêng; ngược lại lập chỉ mục từ resource.
7. Tìm mã: không có -> 404 `Không có mã <code> trong <url CodeSystem>`.
8. Dựng `Parameters`, lọc theo `property` nếu có. Không ghi gì.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| L1 | Thành công, CodeSystem trong resource | `system`, `code` hợp lệ, không có `property` | 200 | `Content-Type: application/fhir+json` | `Parameters`: `name` (title, không có thì name), `display`, `version` (nếu có), `definition` (nếu có), `designation` (mỗi cái một param, part `value` và `language`), `parent` (nếu có), `child` (mỗi con một param), `property` còn lại của concept | không ghi gì |
| L2 | Thành công, lọc `property` | có `property=...` (lặp tham số hoặc phân cách bằng dấu phẩy) | 200 | như L1 | như L1 nhưng `parent`, `child` và property chỉ giữ tên được liệt kê; `name`, `display`, `version`, `definition`, `designation` luôn có | không |
| L3 | Thành công, kho concept riêng | `content=not-present` + `ConceptStore` | 200 | như L1 | như L1 nhưng KHÔNG có `definition`; property thuộc thuộc tính phân cấp (`hierarchyMeaning`, mặc định `parent`) được đổi tên thành `parent`; `child` tối đa 1000 | không |
| L4 | Thiếu `code` | không có `code` và không có `coding` | 400 | - | OperationOutcome `invalid`, `Thiếu tham số code (hoặc coding)` | không |
| L5 | Thiếu `system` | có `code`, không có `system` | 400 | - | `Thiếu tham số system` | không |
| L6 | CodeSystem không tồn tại | không có CodeSystem khớp `system` (và `version`) | 404 | - | `CodeSystem không tồn tại: <system>` | không |
| L7 | Mã không có | CodeSystem có, mã không có trong đó | 404 | - | `Không có mã <code> trong <url>` | không |
| L8 | Thiếu hoặc sai token | - | 401 | `WWW-Authenticate: Bearer realm="fhir"` | OperationOutcome `login`, `Thiếu hoặc sai bearer token` | không |
| L9 | Vai trò không đủ | không thể xảy ra với token hợp lệ có vai trò read; chỉ khi token bị chính sách chặn | 403 | - | `Token không đủ quyền cho thao tác này` hoặc thông điệp từ repository | không |
| L10 | Hết hạn mức | vượt `FHIR_RATE_LIMIT` theo IP | 429 | `Retry-After: 1` | OperationOutcome `quá nhiều yêu cầu, thử lại sau` | không |
| L11 | Lỗi kho | lỗi DB khi tìm CodeSystem hoặc concept | 500 | - | OperationOutcome `exception` (thông điệp lỗi kho, chưa kiểm nguyên văn) | không |

**Ví dụ**

```http
GET /fhir/R4/CodeSystem/$lookup?system=http://example.org/fhir/CodeSystem/icd-demo&code=A00&property=parent HTTP/1.1
Authorization: Bearer eyJhbGciOi...
Accept: application/fhir+json
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json

{
  "resourceType": "Parameters",
  "parameter": [
    { "name": "name", "valueString": "ICD Demo" },
    { "name": "display", "valueString": "Cholera" },
    { "name": "version", "valueString": "2026.1" },
    { "name": "property", "part": [
        { "name": "code", "valueCode": "parent" },
        { "name": "value", "valueString": "A00-A09" } ] }
  ]
}
```

Lỗi (cùng cấu trúc OperationOutcome cho mọi 4xx của route này):

```http
HTTP/1.1 404 Not Found
Content-Type: application/fhir+json

{ "resourceType": "OperationOutcome",
  "issue": [ { "severity": "error", "code": "not-found",
    "details": { "text": "Không có mã Z99 trong http://example.org/fhir/CodeSystem/icd-demo" },
    "diagnostics": "Không có mã Z99 trong http://example.org/fhir/CodeSystem/icd-demo" } ] }
```

**Lưu ý tích hợp:** `system` có thể kèm `|version` trực tiếp. Khi truyền cả `version` lẫn `system` đã có `|` thì `version` bị bỏ qua. `date`, `displayLanguage` không được hỗ trợ (bỏ qua im lặng). Đây là cuộc gọi đọc, gọi lại tùy ý (idempotent). Nhớ mã hóa `|` thành `%7C` trong URL thực tế.

<a id="get-fhir-r4-valueset-expand"></a>
## `GET /fhir/R4/ValueSet/$expand`

Mở rộng một ValueSet (theo canonical `url`) thành danh sách mã, hỗ trợ lọc `filter`, phân trang `count` và `offset`. Kết quả là chính ValueSet đó với `compose` bị bỏ và `expansion` được điền. [Tham chiếu trường](../reference/terminology.md#get-fhir-r4-valueset-expand)

**Tiền điều kiện / cờ bật route:** luôn bật. Cần ValueSet (và CodeSystem mà nó tham chiếu) đã có trong project.

**Thứ tự kiểm tra** (theo `Router.expand`, `loadValueSet`, `expandMembers`):
1. Hạn mức IP (429), xác thực (401), vai trò read (403).
2. `ParseInput` (body không phải object -> 400 `Body phải là một resource hoặc Parameters`, chỉ POST).
3. Nạp ValueSet: theo id (dạng `ValueSet/{id}/$expand`), hoặc tham số `valueSet` nhúng (POST; sai kiểu -> 400 `valueSet không hợp lệ: ...` hoặc `valueSet phải là ValueSet`), hoặc `url`. Thiếu cả `url` lẫn `valueSet` -> 400 `Thiếu tham số url hoặc valueSet`. `valueSetVersion` ghép thành `url|version` khi `url` chưa chứa `|`. Không tìm thấy -> 404 `ValueSet không tồn tại: <url>`.
4. Đọc `count` và `offset`: không phải số nguyên -> 400 `Tham số count phải là số nguyên` (hoặc `offset`). Âm -> 400 `count và offset phải >= 0`.
5. Mở rộng thành tập mã (nhiều lần truy vấn kho). Các lỗi 422 ở bảng dưới phát sinh ở bước này.
6. Áp `filter` (không phân biệt hoa thường, khớp chuỗi con trên `display` HOẶC `code`).
7. Tính `total` (sau filter, trước phân trang), cắt theo `offset` (vượt quá thì về cuối, trả rỗng), cắt theo `count` (0 hoặc vắng: không giới hạn).
8. Trả ValueSet với `expansion.timestamp` (RFC3339 UTC lúc gọi), `total`, `offset`, `contains`. Không ghi gì.

Cách mở rộng `compose` (đúng theo mã): mỗi `include` lấy mã theo thứ tự ưu tiên: danh sách `concept` > `filter` > toàn bộ CodeSystem; `valueSet` trong include là phần GIAO (giao với `system` nếu có, hoặc giao giữa các ValueSet nếu không có `system`); gộp các include theo hợp, loại trùng theo `system|code`; `exclude` loại bỏ theo cùng cách. ValueSet không có `compose` nhưng có sẵn `expansion` thì dùng lại `expansion` có sẵn. Filter hỗ trợ: `is-a`, `descendent-of` (property `concept` hoặc `code`), `regex` (neo toàn chuỗi; trên code hoặc property), `=` (code hoặc property). Kho concept riêng chỉ hỗ trợ `is-a`, `descendent-of`, `=`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| E1 | Thành công, không phân trang | `url` hợp lệ, không `count` | 200 | `Content-Type: application/fhir+json` | ValueSet, `compose` bỏ, `expansion.total`, `offset`=0, `contains[]` đủ mã | không ghi gì |
| E2 | Thành công, phân trang | `count`, `offset` hợp lệ | 200 | như E1 | `expansion.total` là tổng sau filter; `contains` chỉ gồm trang; không có link next, client tự tăng `offset` | không |
| E3 | Thành công, có `filter` | `filter=...` | 200 | như E1 | `total` đếm sau filter | không |
| E4 | Thành công, rỗng | `offset` >= total hoặc filter không khớp | 200 | như E1 | `contains` vắng, `total` đúng | không |
| E5 | Thiếu `url` | không `url`, không `valueSet` | 400 | - | `Thiếu tham số url hoặc valueSet` | không |
| E6 | `count`/`offset` không phải số | - | 400 | - | `Tham số count phải là số nguyên` (hoặc `offset`) | không |
| E7 | `count`/`offset` âm | - | 400 | - | `count và offset phải >= 0` | không |
| E8 | ValueSet không tồn tại | không khớp `url` (và `valueSetVersion`) | 404 | - | `ValueSet không tồn tại: <url>` | không |
| E9 | ValueSet không có `compose` và không có `expansion` | - | 422 | - | `ValueSet không có compose` | không |
| E10 | Tham chiếu vòng | ValueSet tham chiếu lại chính nó qua `include.valueSet` | 422 | - | `ValueSet tham chiếu vòng: <url>` | không |
| E11 | CodeSystem được include không có trên server | `include.system` không tìm thấy | 422 | - | `CodeSystem không có trên server: <system>` (có `|version` nếu include nêu version) | không |
| E12 | ValueSet được include không có | `include.valueSet` không tìm thấy | 422 | - | `ValueSet không có trên server: <canonical>` | không |
| E13 | CodeSystem `not-present` không có nội dung | `content=not-present` mà không có kho concept, hoặc kho chưa nạp mã nào | 422 | - | `CodeSystem <url> không có nội dung trên server` | không |
| E14 | Filter không hỗ trợ | op/property lạ, hoặc `regex` trên kho concept riêng | 422 | - | `filter không hỗ trợ: <property> <op>` | không |
| E15 | Regex lỗi | `filter.regex` không biên dịch được | 400 | - | `filter regex không hợp lệ: ...` | không |
| E16 | Quá lớn | tập mã > 100000 | 422 | - | `ValueSet quá lớn để mở rộng (tối đa 100000 mã)` | không |
| E17 | Thiếu hoặc sai token | - | 401 | `WWW-Authenticate: Bearer realm="fhir"` | `Thiếu hoặc sai bearer token` | không |
| E18 | Hết hạn mức | - | 429 | `Retry-After: 1` | `quá nhiều yêu cầu, thử lại sau` | không |

**Ví dụ**

```http
GET /fhir/R4/ValueSet/$expand?url=http://example.org/fhir/ValueSet/gender-demo&filter=male&count=10&offset=0 HTTP/1.1
Authorization: Bearer eyJhbGciOi...
Accept: application/fhir+json
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json

{
  "resourceType": "ValueSet",
  "id": "9b0f4a1e-5c1d-4e9b-8a77-2f6f1c0d3e11",
  "url": "http://example.org/fhir/ValueSet/gender-demo",
  "status": "active",
  "expansion": {
    "timestamp": "2026-11-24T09:15:00Z",
    "total": 2,
    "offset": 0,
    "contains": [
      { "system": "http://hl7.org/fhir/administrative-gender", "code": "male", "display": "Male" },
      { "system": "http://hl7.org/fhir/administrative-gender", "code": "female", "display": "Female" }
    ]
  }
}
```

**Lưu ý tích hợp:** `filter=male` khớp cả `female` (chuỗi con). Server mở rộng TOÀN BỘ tập mã rồi mới cắt trang, nên `count` nhỏ không làm rẻ hơn việc mở rộng một ValueSet lớn (chi phí tính theo kích thước tập, trần 100000). Nên thu hẹp bằng `filter` hoặc danh sách `concept` trong ValueSet. Kết quả giữ nguyên `meta` và các trường khác của ValueSet gốc, không đổi `meta.versionId`; không ghi lại vào kho. Thứ tự `contains` là thứ tự include rồi thứ tự concept, ổn định giữa các lần gọi khi dữ liệu không đổi (trừ nhánh giao ValueSet, sắp theo `system|code`).

## Phát hiện lệch với common.md

`common.md` chưa có lúc viết (chưa đối chiếu). So với tài liệu cũ và mô tả sinh:

- `reference/terminology.md` ghi `$expand` trả 200/400/401/403/404 nhưng mã còn trả 422 cho nhiều lỗi nội dung ValueSet (bảng E9-E14, E16) và 400 cho `count`, `offset`, regex lỗi; swagger chưa liệt kê.
- `reference/terminology.md` ghi `$lookup` bắt buộc `system` và `code` (đúng cho GET); `coding` chỉ nhận qua POST body, không có ở GET.
- `$expand` swagger mô tả "lọc code/display chứa chuỗi": đúng, không phân biệt hoa thường.
