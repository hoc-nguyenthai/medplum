# Trường hợp phản hồi: nhóm `fhir`

Tham chiếu trường: [../reference/fhir.md](../reference/fhir.md). Quy ước chung: [../common.md](../common.md). Tệp gồm hai phần: **ghi và đọc một resource** (CRUD, lịch sử) rồi **tìm kiếm, batch/transaction, GraphQL, metadata**.

Nhóm `fhir` phục vụ FHIR R4 tổng quát qua `pkg/fhirrouter`: mọi `{resourceType}` hợp lệ dùng chung **một** bộ handler (create, read, vread, update, patch, delete, history), không có route riêng theo loại. Khác biệt theo loại resource chỉ đến từ lớp bọc kho (`fhirauth.Scope`, `fhirsub.Wrap`, `fhirbinary.Wrap`) và từ trình xác thực (`fhirvalidate`); xem mục [Loại resource đặc biệt khi ghi/đọc](#loại-resource-đặc-biệt-khi-ghiđọc). Quy ước chung (header bảo mật, định dạng lỗi, xác thực, hạn mức) ở [../common.md](../common.md); bảng trường ở [../reference/fhir.md](../reference/fhir.md). Phần này nêu điều kiện, phản hồi và tác dụng phụ của các route **ghi và đọc một resource**: `POST` và `PUT` trên `/{resourceType}`, `GET`, `PUT`, `PATCH`, `DELETE` trên `/{resourceType}/{id}`, và hai route lịch sử.

Mọi thông điệp lỗi dưới đây là **nguyên văn từ mã**; các ca đánh dấu "đã đo" được chạy trên server thật (`bootstrap.New` + SQLite, cả chế độ token tĩnh lẫn JWT). Ngoài swagger còn các route cùng handler: `PATCH` và `DELETE` có điều kiện trên `/{resourceType}` (xem mục [PUT có điều kiện](#put-fhir-r4-resourcetype)); chúng không có `<a id>` riêng.

**Sơ đồ vòng đời một resource** (các route trong phần này tác động thế nào lên phiên bản):

```mermaid
stateDiagram-v2
    [*] --> V1: "POST (201) hoặc PUT update-as-create (201)"
    V1 --> Vn: "PUT / PATCH (200): mỗi lần ghi sinh versionId mới, kể cả nội dung y hệt"
    Vn --> Vn: "PUT / PATCH"
    Vn --> Xoa: "DELETE (204): bản tombstone, deleted=true"
    V1 --> Xoa: "DELETE (204)"
    Xoa --> Xoa: "DELETE lại (204, không sinh phiên bản)"
    Xoa --> [*]: "GET 410, PUT 410, PATCH 410; chỉ vread bản cũ và history còn đọc được; $expunge mới xóa hẳn"
```

## Các kiểu phản hồi của phần này

| Kiểu | Khi nào | `Content-Type` | Cấu trúc |
|---|---|---|---|
| Resource | `GET`, `PUT`, `PATCH` `200`; `POST`, `PUT` `201`; `POST` `200` (If-None-Exist khớp) | `application/fhir+json; charset=utf-8` | resource đầy đủ kèm `meta.versionId`, `meta.lastUpdated`, (JWT) `meta.project` |
| Rỗng với header | `Prefer: return=minimal` trên `POST`/`PUT`/`PATCH`; `304`; `204` | không có `Content-Type` | chỉ header (`ETag`, `Last-Modified`, `Location`) |
| OperationOutcome thông tin | `Prefer: return=OperationOutcome` | `application/fhir+json` | `issue[0]` `severity=information`, `code=informational`, `details.text` `OK` (cố định, không mô tả kết quả thật) |
| Bundle `history` | `GET .../_history` | `application/fhir+json` | `Bundle.type=history`, `total`, entry mới nhất trước |
| OperationOutcome lỗi | mọi `4xx`/`5xx` | `application/fhir+json` | một hoặc nhiều `issue`; lỗi validate có nhiều `issue` kèm `expression` |
| Nội dung thô của Binary | `GET /Binary/{id}` với `Accept` khớp | `contentType` của Binary | byte của file, không bọc JSON (xem mục Binary) |

## Thứ tự kiểm tra chung của ghi/đọc một resource

Thứ tự thật trong mã, từ ngoài vào trong. Mỗi bước lỗi **dừng** bước sau.

```mermaid
flowchart TD
    A["1. Echo: Recover, RequestID, header bảo mật, CORS, BodyLimit 16 MiB"] --> B["2. Hạn mức theo IP (429)"]
    B --> C["3. Guard: xác thực + vai trò read/write (401, 403)"]
    C --> D["4. RawBinary (chỉ khi bật kho Binary): POST/PUT/GET Binary"]
    D --> E["5. readRequest: đọc thân, giải mã (413, 400, 415)"]
    E --> F["6. Định tuyến: loại resource thật? method có handler? (404, 405)"]
    F --> G["7. Handler: kiểm thân, validate (400)"]
    G --> H["8. Điều kiện: If-None-Exist, tiêu chí query (400, 412)"]
    H --> I["9. fhirauth.Scope (chỉ JWT): project, chính sách, SMART, hiddenFields (403, 404)"]
    I --> J["10. fhirsub.Wrap, fhirbinary.Wrap"]
    J --> K["11. fhirstore: giao dịch, khóa dòng, If-Match (404, 410, 412), ghi, lịch sử, chỉ mục"]
    K --> L["12. Phát sự kiện sau commit; ResponseHook; Prefer"]
```

1. **Echo** (`httpapi.New`): `Recover`, `RequestID`, header bảo mật, CORS, `BodyLimit` 16 MiB theo `Content-Length` (vượt: `413` `Body quá lớn`, `code=invalid`; đã đo). `POST`/`PUT` `Binary` bỏ qua giới hạn này **khi bật kho Binary ngoài** (handler tự đặt trần). Vì các middleware toàn cục đứng trước nhóm route, thân khai `Content-Length` quá lớn bị `413` **trước** cả xác thực (suy từ thứ tự `e.Use` và `Group(..., mws...)`, `(chưa kiểm)` bằng yêu cầu không token).
2. **Hạn mức** theo IP (`429`, mục 5 của common.md): đứng trước xác thực.
3. **Guard** (`Authenticator.Guard`): xác thực rồi so vai trò với `RequiredRole`: `GET`/`HEAD`/`OPTIONS` là `read`; `POST`/`PUT`/`PATCH`/`DELETE` là `write` (ngoại trừ `POST .../_search`). Thiếu hoặc sai token `401`; vai trò thấp `403` `Token không đủ quyền cho thao tác này`. Chế độ JWT: vai trò suy từ scope; token chỉ có scope SMART đọc (`system/Patient.r`) thành vai trò `read` nên bị chặn `403` ngay ở bước này khi ghi (đã đo: `system/Patient.r` + `DELETE` ra `Token không đủ quyền cho thao tác này`, không phải thông điệp SMART).
4. **RawBinary** (chỉ khi `FHIR_BINARY_STORAGE` đặt): chặn `POST /Binary`, `PUT /Binary/{id}`, `GET /Binary/{id}` để nhận byte thô; mọi thứ khác (và thân JSON là resource Binary) chuyển xuống router. Xem mục Binary.
5. **readRequest** (`pkg/fhirrouter/http.go`): đọc thân tối đa 16 MiB bằng `MaxBytesReader` (vượt mà không có `Content-Length`, ví dụ chunked: `413` `Body quá lớn` với `code=too-costly`, khác `code=invalid` của bước 1; đã đo cả hai). Thân trống: `req.Body` rỗng, không lỗi. Thân có nội dung: `Content-Type` rỗng, `application/json` hoặc `*+json` giải mã JSON (`400` `JSON không hợp lệ: <lỗi Go>`); `application/x-www-form-urlencoded` gộp vào query; `application/graphql` là chuỗi; khác `415` `Content-Type không hỗ trợ: <mt>`.
6. **Định tuyến** (`Router.match`): `{resourceType}` phải là tên resource thật (`fhir.IsResourceType`, danh sách sinh `pkg/fhir/registry_gen.go`); lạ thì **không khớp route**: `404` `Không có route cho <METHOD> <đường dẫn sau /fhir/R4>` (ví dụ `POST Foo`, đã đo). Đường dẫn khớp nhưng method không có handler (ví dụ `HEAD Patient/{id}`, `DELETE` trên `/_history`): `405` `<METHOD> không được hỗ trợ cho <đường dẫn>` (`HEAD` đã đo mã `405`).
7. **Handler**: với `POST`/`PUT`: `writeBody` kiểm thân là object (`400` `Body phải là một resource JSON`), `resourceType` khớp đường dẫn (`400` `resourceType không khớp: đường dẫn là Patient, body là "Observation"`; thiếu `resourceType` thì `body là ""`), rồi **validate** bằng `fhirvalidate.Validate` trừ khi `FHIR_SKIP_VALIDATION=true`; `FHIR_SKIP_CONSTRAINTS=true` bỏ riêng invariant FHIRPath. Chỉ lỗi (`severity=error`) chặn ghi; cảnh báo bị bỏ. **Việc này chạy TRƯỚC kiểm quyền theo loại/SMART/chính sách** (bước 9): token không có quyền ghi loại đó nhưng gửi thân sai vẫn nhận `400` mô tả lỗi cấu trúc, và thân đúng mới nhận `403` (đã đo).
8. **Điều kiện** (`If-None-Exist`, `PUT`/`PATCH`/`DELETE` theo query): xem [Điều kiện ghi](#điều-kiện-ghi).
9. **`fhirauth.Scope`** (chỉ chế độ JWT; chế độ token tĩnh **không có** bước này, không tách project, không SMART, không chính sách). Thứ tự trong từng thao tác:
   1. project của người gọi: thiếu Principal hoặc project rỗng `403` `Không xác định được project của người gọi`;
   2. `allow(loại, quyền)`: (a) chính sách truy cập (`AccessPolicy` trong claim `fhir_access_policy`): loại không có rule, rule `readonly` khi ghi, hoặc rule `interaction` không liệt kê thao tác ra `403` `Chính sách truy cập không cho phép <Loại>`; (b) scope SMART theo loại: `403` `Token thiếu quyền <c|r|u|d|s> (SMART) trên <Loại>`; `PermCreate`=`c`, `PermRead`=`r`, `PermUpdate`=`u`, `PermDelete`=`d`, `PermSearch`=`s`. Loại `AsyncJob` được **miễn** bước này;
   3. `vread`/`history`: thêm kiểm interaction riêng `403` `Chính sách truy cập không cho phép vread trên <Loại>` (hoặc `history`);
   4. thao tác trên resource có sẵn (`update`, `patch`, `delete`, đọc): đọc bản hiện tại, **khác project hoặc không thỏa `criteria` của chính sách thì `404` `Not found`** (không lộ sự tồn tại); xem giới hạn `410` ở [GET](#get-fhir-r4-resourcetype-id);
   5. ghi: `protectWrite` (gỡ `hiddenFields` và `readonlyFields` khỏi thân; khi update khôi phục từ bản đang lưu), đóng dấu `meta.project` bằng project của người gọi (**giá trị client gửi bị bỏ**), rồi kiểm lại `criteria` trên bản sẽ ghi: `403` `Resource không thỏa tiêu chí của chính sách truy cập`;
   6. kết quả trả về: gỡ `hiddenFields` (`redact`) khỏi mọi resource đọc, vread, history và cả kết quả ghi.
   `criteria` dùng cú pháp query FHIR (kể cả `_compartment=Patient/<id>`), không chained/_has/_include. Scope `patient/` (compartment theo bệnh nhân) bị bỏ qua, nghĩa là không cấp quyền.
10. **Lớp bọc kho** trong (`fhirsub.Wrap`, rồi `fhirbinary.Wrap` khi bật kho Binary): phát sự kiện, đưa nội dung Binary ra kho đối tượng.
11. **`fhirstore`**: mỗi ghi chạy trong **một giao dịch**: ghi `fhir_resource`, một dòng `fhir_history`, dựng lại các bảng chỉ mục tìm kiếm. `update`/`delete` khóa dòng (`SELECT ... FOR UPDATE` trên PostgreSQL), kiểm tồn tại (`404`/`410`), rồi kiểm `If-Match` (`412`). Lỗi nội bộ: `500` với thông điệp của lỗi Go gốc, không được che (`asError` → `Internal`).
12. **Sau commit**: nếu có broker (`FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD`), phát sự kiện `fhir.resource.changed` (xem [Tác dụng phụ chung](#tham-chiếu-và-meta-do-server-ghi-id-versionid-lastupdated-project-etag-location)); trong transaction Bundle thì giữ đến khi commit. `ResponseHook` (chỉ khi bật kho Binary) viết lại tham chiếu Binary thành URL ký ở mọi phản hồi `< 300` có resource. Cuối cùng áp `Prefer`.

**Bảng quyết định theo vai trò và chế độ** (cùng một thao tác `PUT Patient/{id}`):

| Chế độ | Token | Kết quả |
|---|---|---|
| Token tĩnh | `read` | `403` `Token không đủ quyền cho thao tác này` (Guard) |
| Token tĩnh | `write` | qua; không kiểm loại, không tách project |
| JWT | `fhir.read` | `403` `Token không đủ quyền cho thao tác này` |
| JWT | `fhir.write` (hoặc `system/*.*`) | qua; tách project; `AccessPolicy` nếu có |
| JWT | `system/Observation.cruds` gọi `PUT Patient/{id}` | qua Guard (có quyền ghi ở một loại), thân đúng thì `403` `Token thiếu quyền u (SMART) trên Patient` |
| JWT | `system/Patient.ru` | `PUT` qua (cần `u`); `PATCH` cũng cần `r` |

## Điều kiện ghi

| Header hoặc query | Áp cho | Hành vi (theo mã, đã đo trừ khi ghi khác) |
|---|---|---|
| `If-None-Exist: <query>` | `POST /{rt}` | **create có điều kiện**. Parse bằng `url.ParseQuery` (lỗi `400` `If-None-Exist không hợp lệ`). Tìm trong phạm vi người gọi (project, chính sách). 0 khớp: tạo `201`. 1 khớp: **không ghi**, trả `200` kèm resource sẵn có (không sinh phiên bản, không sự kiện). Nhiều khớp: `412` `Nhiều resource khớp If-None-Exist`. Không tiêu chí (`_count=1` hoặc toàn tham số điều khiển): `400` `Thao tác có điều kiện cần tiêu chí search`. Tham số lạ: `400` `tham số search không hợp lệ: zzz`. Dạng có tiền tố loại (`Patient?identifier=...`) `(chưa kiểm)` |
| `?<tiêu chí>` | `PUT`, `PATCH`, `DELETE` trên `/{rt}` | điều kiện theo kết quả tìm kiếm: 0, 1, nhiều khớp (xem từng route). Không có query: `400` `Thao tác có điều kiện cần tiêu chí search` |
| `If-Match: W/"<versionId>"` | `PUT /{rt}/{id}`, `PATCH /{rt}/{id}`, `PUT /{rt}?...` (khi 1 khớp) | so với `versionId` hiện tại. `parseIfMatch` cắt khoảng trắng, tiền tố `W/` và dấu nháy: `W/"v"`, `"v"` và `v` trần đều dùng được (đã đo). `*`, danh sách nhiều etag (`W/"a", W/"b"`), hoặc giá trị khác: `412` `Phiên bản không khớp: hiện tại <versionId hiện tại>`. Rỗng: không kiểm. So sánh **trong giao dịch có khóa dòng**, nên hai `PUT` đồng thời cùng `If-Match` chỉ một thắng (test `testConcurrentIfMatch`). `404`/`410` ưu tiên hơn `412` |
| `If-Match` trên `DELETE` | `DELETE /{rt}/{id}` | **bị bỏ qua** (đã đo: `If-Match` sai vẫn `204`) |
| `If-None-Match`, `If-Modified-Since` | `GET /{rt}/{id}` | đọc có điều kiện, xem [GET](#get-fhir-r4-resourcetype-id); **không** áp cho `vread`, ghi hay `If-None-Match: *` trên `PUT` |
| `If-Unmodified-Since` | mọi route | không đọc (bị bỏ qua, theo mã) |

**Khóa chống ghi trùng:** create có điều kiện và `PUT` có điều kiện chạy trong một giao dịch và lấy khóa tư vấn theo `(loại, tiêu chí)` (`withKeyLock`; PostgreSQL `pg_advisory_xact_lock`, SQLite chiếm khóa ghi; ở chế độ JWT khóa gắn thêm project nên hai project không chặn nhau). Hai `POST` đồng thời cùng `If-None-Exist` không tạo trùng (test `testConcurrentConditionalCreate`); `PUT` có điều kiện đồng thời: `testConcurrentConditionalUpdate`. `PATCH` có điều kiện và `DELETE` có điều kiện **không** lấy khóa này (theo mã).

## Prefer return

`writeResponse` chỉ xét header `Prefer` cho `POST`, `PUT`, `PATCH` và khi phản hồi có resource (`200`/`201`); đọc `Prefer` bằng `Header.Get` nên chỉ **dòng header đầu tiên**, các tham số cách nhau dấu phẩy được tách, chỉ khóa `return` được dùng.

| `Prefer` | Phản hồi (đã đo) |
|---|---|
| không có, `return=representation`, hoặc giá trị lạ | resource đầy đủ |
| `return=minimal` | cùng mã (`200`/`201`), **thân rỗng, không có `Content-Type`**, vẫn đủ `ETag`, `Last-Modified`, (`201`) `Location` |
| `return=OperationOutcome` | cùng mã, thân `OperationOutcome` cố định (`information`/`informational`/`details.text` `OK`) |
| `handling=...` | không đọc (bỏ qua) |
| `respond-async` | không có tác dụng ở các route phần này (chỉ `$export`, batch async, xem `cases/operation.md`/`cases/bulk.md`) |

`GET`, `DELETE` và mọi lỗi **không** chịu `Prefer`. `POST` If-None-Exist khớp (`200`) cũng chịu `Prefer` (minimal ra thân rỗng kèm `ETag`, đã đo).

## Tham chiếu và meta do server ghi (id, versionId, lastUpdated, project, ETag, Location)

| Thành phần | Quy tắc |
|---|---|
| `id` khi `POST` | router luôn cấp UUID v4 (`newID`); `id` client gửi bị **ghi đè** (đã đo: gửi `"id":"mine"`, nhận UUID lạ) |
| `id` khi `PUT` | lấy từ đường dẫn; thân không có `id` vẫn được (đã đo), có thì phải trùng (`400` `id trong body không khớp với đường dẫn`) |
| `meta.versionId` | UUID v4 mới ở **mỗi lần ghi** (create, update, patch, delete), kể cả khi nội dung y hệt (đã đo: hai `PUT` giống nhau ra hai `versionId` khác nhau; `PATCH` mảng rỗng `[]` cũng sinh bản mới). Không phải số nguyên tăng dần |
| `meta.lastUpdated` | `2006-01-02T15:04:05.000Z` (mili giây, UTC) do server đặt; `versionId`/`lastUpdated` client gửi bị bỏ (create: router xóa; update: kho xóa) |
| `meta.project` | JWT: luôn là project của người gọi, **giá trị client gửi bị ghi đè** (đã đo: gửi `"project":"B"` bởi người của A, lưu `A`); client **thấy** `meta.project` trong mọi phản hồi. Token tĩnh: không đóng dấu, giữ nguyên giá trị client gửi `(chưa kiểm)` |
| `meta.tag`, `meta.security`, `meta.profile` | giữ nguyên như client gửi (đã đo `tag`) |
| Tham chiếu (`Reference`) | **không kiểm đích tồn tại** khi ghi (`fhirvalidate` không có kho); chỉ kiểm cấu trúc. `urn:uuid:` chỉ được giải trong transaction Bundle (`cases` của `POST /fhir/R4`) |
| `ETag` | `W/"<versionId>"`; có trên `200`/`201`/`304` của resource và trên mọi dạng `Prefer`; **không** có trên `204` (`DELETE`) |
| `Last-Modified` | HTTP-date UTC độ phân giải **giây** (`httpDate`); vắng nếu `lastUpdated` không phân tích được |
| `Location` | chỉ `201`: `<BaseURL>/<Loại>/<id>/_history/<versionId>`; `BaseURL` rỗng thì đường dẫn tương đối `Loại/id/_history/versionId`. Gồm cả `PUT` update-as-create và `PUT` có điều kiện tạo mới |

**Tác dụng phụ chung của một lần ghi thành công** (create, update, patch, delete):

| Tác dụng | Chi tiết |
|---|---|
| Lịch sử | thêm dòng `fhir_history` (`method` `POST` cho create, `PUT` cho update **và patch**, `DELETE`); không bao giờ sửa dòng cũ |
| Chỉ mục tìm kiếm | dựng lại các bảng `fhir_idx_*` trong cùng giao dịch (xóa mềm thì xóa chỉ mục) |
| Sự kiện | khi có broker: `fhir.resource.changed` (`ChangeEvent`: `interaction` `create`/`update`/`delete`, `resource_type`, `id`, `version_id`, `resource`; delete mang phiên bản cuối trước khi xóa). Phát **sau commit**, lỗi phát chỉ ghi log (`fhirsub: phát sự kiện lỗi`), không làm hỏng ghi. Không có broker (`FHIR_SUBSCRIPTIONS=false` và không tự tải): không phát gì |
| Subscription | sự kiện của loại `Subscription` làm Manager nạp lại danh sách; thông báo chỉ gửi tới Subscription **cùng project** (`e.project != evProject` bỏ qua) |
| Audit | không có bảng audit cho thao tác FHIR; chỉ nhật ký truy cập của super-admin (`"audit": true`) |
| Nội dung Binary | khi bật kho Binary, `data` của Binary được đưa ra kho đối tượng khóa `binary/<id>/<versionId>` |

## Loại resource đặc biệt khi ghi/đọc

Router **không có danh sách cấm hay cho phép theo loại**: mọi tên trong `fhir.ResourceTypes` (kể cả loại quản trị kiểu Medplum như `Project`, `AccessPolicy`, `ProjectMembership`, `User`) ghi và đọc được như nhau, theo quyền của token. Điểm khác chỉ ở các loại dưới đây.

| Loại | Hành vi (mã) |
|---|---|
| `Binary` | `data` là base64. **Không bật kho** (`FHIR_BINARY_STORAGE` rỗng): `data` nằm trong JSON của CSDL, không trần riêng ngoài 16 MiB; `POST`/`PUT` thân không phải JSON (`text/plain`...) ra `415` `Content-Type không hỗ trợ: text/plain` (đã đo). **Bật kho**: `fhirbinary.Wrap` giải mã `data` (trần `FHIR_BASE64_BINARY_MAX_BYTES` mặc định 1 MiB: `400` `base64Binary exceeds 1048576 bytes`; base64 hỏng `400` `Invalid base64 data`; kiểm `contentType`), ghi vào kho **sau** khi CSDL cấp `versionId` (lỗi kho: `500` `Failed to store binary content` và bù trừ), và bỏ `data` khỏi bản lưu CSDL (thân phản hồi create không còn `data`, theo mã, `(chưa kiểm)` bằng đo). `PUT` không `data` chép nội dung bản trước sang bản mới. `DELETE` là xóa mềm: **nội dung ở kho không bị xóa** (chỉ `$expunge` xóa; nội dung mồ côi cần công cụ quét). `POST`/`PUT` `Binary` thô và `GET` thô do `RawBinary` xử lý (xem [GET](#get-fhir-r4-resourcetype-id)). `validate` đã đo: thiếu `contentType` `400` `Thiếu thuộc tính bắt buộc "contentType"`; `data` sai base64 bị validator bắt trước (`Giá trị không đúng định dạng base64Binary: "!!!"`, `expression` `Binary.data`) |
| `Bundle` | `POST /Bundle` chỉ **lưu** Bundle như mọi resource (đã đo với `type=collection`); batch/transaction chỉ chạy ở `POST /fhir/R4` (gốc). `POST /Bundle` với `type=transaction` không thực thi (theo mã: `create` không xem `type`) |
| `Subscription` | lưu như resource thường, validate cấu trúc (đã đo: `Subscription` rỗng ra `400` nhiều `issue` `required` cho `status`, `reason`, `criteria`, `channel`). **Không kiểm `channel.endpoint` hay `criteria` lúc ghi** (đã đo: endpoint `http://localhost:1/h` được `201`; `criteria` sai cú pháp `(chưa kiểm)`); chống SSRF làm lúc giao thông báo (`FHIR_WEBHOOK_ALLOW_PRIVATE`). Có hiệu lực qua sự kiện `fhir.resource.changed` (cần `FHIR_SUBSCRIPTIONS=true`) |
| `SearchParameter` | chỉ lưu, **không mở rộng tìm kiếm** (không mã nào trong `fhirsearch`/`fhirstore` đọc loại này; đã đo `201` mà không có tham số mới) |
| `CodeSystem`, `ConceptMap`, `ValueSet` | lưu như resource thường. Concept và ánh xạ nằm ở kho riêng, nạp bằng `$import` (route khác); ghi hay xóa mềm qua các route này **không** động tới concept. Chỉ `$expunge` xóa luôn concept/ánh xạ |
| `AsyncJob` | ghi được qua REST (validate đòi `requestTime`, đã đo `Thiếu thuộc tính bắt buộc "requestTime"`). Scope **miễn** kiểm SMART và chính sách (`allow` trả nil khi `rt == "AsyncJob"`), vẫn tách project và cần vai trò `write`. Xem rủi ro ở cuối tệp |
| `Project`, `AccessPolicy`, `User`... | không chặn: token có `fhir.write` tạo được `Project` trong project của mình (đã đo `201`). Chúng chỉ là dữ liệu ở đây (danh tính ở dịch vụ `auth`); riêng rule `"*"` của `AccessPolicy` loại các loại này khỏi wildcard |
| `Parameters`, `OperationOutcome` | lưu được (đã đo `201`) |
| `Patient` và còn lại | không khác biệt |

---

<a id="post-fhir-r4-resourcetype"></a>

## Nhóm tìm kiếm, Bundle, GraphQL và metadata (mảnh B)

Năm route của mảnh này đều đi qua cùng một chuỗi middleware và cùng một `fhirrouter.Router`; điểm khác nhau là handler và cách Bundle/JSON được dựng. Quy ước chung (header bảo mật, `X-Request-Id`, định dạng `OperationOutcome`, 401/403 của `Guard`, hạn mức theo IP, trần thân 16 MiB) ở [../common.md](../common.md#3-định-dạng-lỗi); bảng trường ở [../reference/fhir.md](../reference/fhir.md). Mục này chỉ nêu điều kiện -> phản hồi. Phần đo "bằng test tạm" nghĩa là đã chạy một test Go tạm trên `httpapi.New` + `fhirrouter.New` + `fhirauth.Scope(MemoryRepository)` với bộ xác thực JWT giả (project `p1`, vai trò `write`), rồi xóa test; kho SQL (`pkg/fhirstore`) dùng cùng `Repo.Search` của `fhirauth` nên hành vi phân quyền như nhau, còn chỗ khác biệt kho được ghi rõ.

**Điều kiện có route:** luôn bật. Cùng bộ xử lý còn phục vụ hai route không có trong swagger: `GET /fhir/R4` (tìm kiếm hệ thống, bắt buộc `_type` khi có tham số lọc) và `GET /fhir/R4/$graphql` (cùng handler của POST). Mọi route dưới đây đứng sau `Guard`, trừ `GET /fhir/R4/metadata`.

**Sơ đồ chuyển route** (một ứng dụng khách điển hình):

```mermaid
sequenceDiagram
    participant C as "Ứng dụng khách"
    participant S as "fhir-server"
    C->>S: "GET /fhir/R4/metadata (công khai)"
    S-->>C: "200 CapabilityStatement"
    C->>S: "GET /fhir/R4/Patient?family=Tran&_count=20"
    S-->>C: "200 Bundle searchset, link self và next"
    C->>S: "GET link next"
    S-->>C: "200 Bundle trang kế"
    C->>S: "POST /fhir/R4/Patient/_search (form, khi query dài)"
    S-->>C: "200 Bundle searchset"
    C->>S: "POST /fhir/R4/$graphql (đọc có cấu trúc)"
    S-->>C: "200 data và errors"
    C->>S: "POST /fhir/R4 (batch hoặc transaction)"
    S-->>C: "200 Bundle batch-response hoặc transaction-response"
    C->>S: "POST /fhir/R4 kèm Prefer respond-async (batch, khi bật)"
    S-->>C: "202 Content-Location job/{id}/status"
    C->>S: "GET job/{id}/status (cases/operation.md)"
```

### Các kiểu phản hồi của nhóm

| Kiểu | Khi nào | `Content-Type` | Cấu trúc |
|---|---|---|---|
| Bundle `searchset` | `GET {rt}`, `POST {rt}/_search`, `GET /fhir/R4` | `application/fhir+json; charset=utf-8` | `{resourceType:"Bundle", type:"searchset", timestamp, total?, link[], entry[]?}`; `entry[]` vắng khi không có kết quả |
| Bundle `batch-response` / `transaction-response` | `POST /fhir/R4`: `200` | như trên | `{type, timestamp, entry[]?}`; mỗi entry `{fullUrl?, resource?, response{status, location?, etag?, lastModified?, outcome?}}`; bundle rỗng thì không có `entry` |
| Chấp nhận bất đồng bộ | `POST /fhir/R4` batch + `Prefer: respond-async` khi `FHIR_ASYNC_BATCH=true`: `202` | như trên | `OperationOutcome` thông tin `Accepted`; header `Content-Location` |
| Thân rỗng theo `Prefer: return=minimal` | `POST /fhir/R4` (và mọi POST/PUT/PATCH) có `Prefer: return=minimal` | không có | thân trống, giữ nguyên mã HTTP (xem mục batch) |
| GraphQL | `POST`/`GET $graphql`: `200` | `application/json; charset=utf-8` (KHÔNG phải `fhir+json`) | `{"data":{...}, "errors":[{message, path?}]?}` |
| CapabilityStatement | `GET metadata`: `200` | `application/fhir+json; charset=utf-8` | `CapabilityStatement` R4 `4.0.1`, `kind:instance`, một `rest` mode `server` |
| Lỗi | mọi `4xx`/`5xx` ngoài 200 của GraphQL | `application/fhir+json; charset=utf-8` | `OperationOutcome` một `issue`; `details.text` bằng `diagnostics` |

### Thứ tự xử lý chung (mọi route của mảnh này)

Theo `httpapi.New` rồi `fhirrouter` (mã thắng tài liệu cũ):

1. **Instrument, Recover, RequestID, SecurityHeaders, CORS** (không có quyết định nghiệp vụ).
2. **BodyLimit 16M** của Echo: thân lớn hơn trần (biết qua `Content-Length`) là `413`, thân `OperationOutcome` `code=invalid`, `Body quá lớn`. Nếu không biết độ dài (chunked) thì `readRequest` tự đọc tối đa 16 MiB và trả `413` `code=too-costly`, `Body quá lớn` `(chưa kiểm)`.
3. **Hạn mức theo IP** (`FHIR_RATE_LIMIT`, mặc định staging/production 20 req/s; xem [common.md mục 5](../common.md#5-hạn-mức-kích-thước)): quá mức -> `429`, `Retry-After: 1`, `quá nhiều yêu cầu, thử lại sau` (`code=invalid`); không xác định được IP -> `403` `không xác định được người gọi`. Đứng TRƯỚC xác thực, nên cũng áp cho `metadata`.
4. **`Guard`**: `GET /fhir/R4/metadata` (đúng GET, đúng đường dẫn, bỏ `/` cuối) đi qua, không xét token (kể cả token sai). Còn lại: thiếu Bearer -> `401`; token sai -> `401`; thiếu quyền FHIR -> `403`; vai trò thấp hơn `RequiredRole` -> `403` `Token không đủ quyền cho thao tác này`. Vai trò: `GET {rt}`, `POST {rt}/_search`, `$graphql` (mọi method) cần `read`; `POST /fhir/R4` cần `write` (kể cả batch chỉ chứa `GET`); chế độ super-admin cần `X-Fhir-Project` (xem common.md mục 4).
5. **`readRequest`** (`pkg/fhirrouter/http.go`): đọc toàn bộ thân; thân rỗng hoặc chỉ khoảng trắng thì BỎ QUA kiểm `Content-Type` (đã đo: `POST _search` với `Content-Type: text/plain` và thân rỗng là `200`). Có thân thì: `application/x-www-form-urlencoded` -> gộp vào `Query` (form hỏng `400` `Form không hợp lệ`); `application/graphql` -> thân là chuỗi; không có `Content-Type`, `application/json` hoặc `*+json` -> giải mã JSON (hỏng `400` `JSON không hợp lệ: <lỗi Go>`, ví dụ `JSON không hợp lệ: unexpected EOF`); loại khác `415` `Content-Type không hỗ trợ: <mt>` (đã đo: `text/plain`, `application/fhir+xml`).
6. **`Router.match`**: `resourceType` chỉ nhận tên trong danh sách 171 loại; tên lạ (`Foo`) không khớp route nào -> `404` `Không có route cho GET Foo` (đã đo; thông điệp KHÔNG có tiền tố `/fhir/R4`, và không phải `400`). Đường dẫn có route cho method khác -> `405` `<METHOD> không được hỗ trợ cho <path>` (đã đo: `POST không được hỗ trợ cho metadata`, `PUT không được hỗ trợ cho $graphql`).
7. **Handler**, rồi `ResponseHook` (khi bật kho Binary ngoài: đổi tham chiếu Binary trong Bundle thành URL ký theo quyền người gọi), rồi `writeResponse`.

Không có bước kiểm `Accept` hay `_format`: `_format=xml` được bỏ qua và vẫn trả JSON (đã đo).

## Phân trang và tổng số

Chỉ có phân trang kiểu **offset** (không có con trỏ: `_cursor` được nhận và BỎ QUA). Áp cho `GET {rt}`, `POST {rt}/_search` và `GET /fhir/R4`.

| Tham số | Quy tắc | Lỗi (nguyên văn) |
|---|---|---|
| `_count` | số nguyên `>= 0`, mặc định `20`; lớn hơn `1000` bị **cắt im lặng** về `1000` (đã đo: `_count=5000` ra link `_count=1000`); `0` hợp lệ: không có `entry`, không có `next` | `400` `_count không hợp lệ: "abc"` (cũng cho số âm) |
| `_offset` | số nguyên `>= 0`, mặc định `0`; không có trần (OFFSET lớn quét nhiều dòng) | `400` `_offset không hợp lệ: "-1"` |
| `_total` | `none` (mặc định: không có trường `total`), `estimate`, `accurate` | `400` `_total không hợp lệ: "x"` |
| `_summary=count` | đặt `_count=0` và `_total=accurate`: trả `total`, không có `entry` (đã đo, link `self` thành `_count=0&_total=accurate`) | không có |
| `_summary` khác (`true`, `text`, `data`, `false`) | nhận và BỎ QUA: vẫn trả resource đầy đủ (đã đo `_summary=true`) | không có |
| Tham số lặp | `_count=1&_count=2` -> giá trị CUỐI thắng (đã đo, `_count=2`) | |

- **Thứ tự báo lỗi tham số:** `Parse` duyệt khóa theo thứ tự chữ cái (với `_type` đứng trước) và dừng ở lỗi đầu tiên. Vì `_` đứng trước chữ thường, `?foo=1&_count=abc` báo `_count không hợp lệ`, không phải `foo`.
- **`total`:** với `_total=estimate` hoặc `accurate`, kho chạy `SELECT COUNT(*)` cùng điều kiện lọc nhưng không phân trang; `estimate` cho số CHÍNH XÁC như `accurate` (không có bản ước lượng); trường `total` là số nguyên, đếm theo project và (với tìm theo loại) theo tiêu chí `AccessPolicy` của người gọi. Tìm kiếm HỆ THỐNG không đẩy tiêu chí policy xuống kho nên `total` có thể lớn hơn số resource người gọi thấy (xem mục rủi ro cuối tệp).
- **`link`:** luôn có `self` (offset hiện tại); `next` khi kho báo còn kết quả (đọc `count+1` dòng); `previous` khi `offset > 0` với `offset = max(0, offset - count)`. Bundle không có `first`/`last`. URL link được DỰNG LẠI từ yêu cầu đã phân tích (`Request.Query`): tham số sắp xếp theo thứ tự chữ cái, `_count` luôn có, `_total` chỉ khi khác `none`, giá trị được mã hóa lại. Hai điều cần biết: (a) `_elements`, `_summary`, `_format`, `_pretty` KHÔNG nằm trong link (đã đo: `next` của `_elements=birthDate&_count=1` mất `_elements`); (b) `_include:iterate` thành `_include` (cờ iterate mất).
- Gốc của link: `BASE_URL` (`FHIR_BASE_URL`) cộng `/Patient`; tìm hệ thống dùng chính gốc, ví dụ `http://h/fhir/R4?_count=1&_type=Patient`. `BaseURL` rỗng thì đường dẫn tương đối (`Patient?...`, hoặc `.?...` cho tìm hệ thống) `(chưa kiểm)` ở cấp HTTP.
- **Trang ngắn:** sau khi kho trả một trang, `Repo.Search` còn lọc lại từng resource theo project, SMART và `AccessPolicy` (lớp phòng thủ thứ hai). Nếu lớp này loại bớt thì trang ngắn hơn `_count` dù vẫn có `next`; khách phải đi theo `next` đến khi hết, không dùng "số entry < _count" làm điều kiện dừng.
- `_include`/`_revinclude` KHÔNG tính vào `_count` hay `total` và không phân trang riêng (xem mục Bundle kết quả).

## Tham số tìm kiếm và lỗi của chúng

Mọi lỗi tham số là `400` `code=invalid` (kiểu `*fhirsearch.Error` chuyển thành `BadRequest`), thông điệp nguyên văn dưới đây; định dạng ví dụ ở route `GET {rt}`.

| Nhóm | Tham số | Hành vi | Lỗi (nguyên văn) |
|---|---|---|---|
| Điều khiển | `_count`, `_offset`, `_total`, `_summary` | xem mục Phân trang | như trên |
| | `_sort` | danh sách cách nhau bằng dấu phẩy, tiền tố `-` giảm dần; resource thiếu giá trị xếp CUỐI cả hai chiều; khóa phụ cố định: loại resource (theo thứ tự `_type`) rồi `id` | `tham số sắp xếp không hợp lệ: zzz`; `không sắp xếp được theo tham số <code> kiểu composite` (hoặc `special`) |
| | `_elements` | danh sách phẩy (nhiều lần gộp); giữ `resourceType`, `id`, `meta` + phần tử cấp cao được nêu; áp cho cả `match` lẫn `include`; không gắn thẻ `SUBSETTED` | không có (tên phần tử lạ chỉ không khớp gì) |
| | `_type` | tìm hệ thống: giới hạn loại; ở tìm theo loại được nhận nhưng loại trong đường dẫn thắng | `_type không hợp lệ: Foo` |
| | `_include`, `_include:iterate`, `_revinclude`, `_revinclude:iterate` | dạng `Type:param[:targetType]` (`*` được dùng cho `Type` của `_include`) | `giá trị _include không hợp lệ: "x" (cần Type:param[:target])`; `_include: tham số không tồn tại: <p>`; `_revinclude: tham số không tồn tại: <p>` |
| | `_compartment` | đúng một `Patient/<id>` (regex `^Patient/[A-Za-z0-9.\-]{1,64}$`), AND với các điều kiện khác | `_compartment cần chỉ định loại resource` (tìm hệ thống); `_compartment chỉ được đặt một lần`; `_compartment phải có dạng Patient/<id>: "Patient/p1,Patient/p2"` (đã đo: dấu phẩy bị từ chối); `loại <T> không thuộc compartment Patient` |
| | `_filter` | biểu thức FHIR `_filter` (AND với các điều kiện khác) | lỗi cú pháp theo `ParseFilterExpr` `(chưa kiểm thông điệp)` |
| | `_query`, `_contained`, `_containedType` | không hỗ trợ | `tham số _query chưa được hỗ trợ` (đã đo) |
| | `_format`, `_pretty`, `_cursor` | nhận và bỏ qua | không có |
| Lọc | tham số search của loại resource (kể cả `_id`, `_lastUpdated`, `_tag`, `_security`, `_profile`, `_source`, `_project`) | nhiều tham số khác tên hoặc cùng tên lặp = AND; nhiều giá trị cách nhau dấu phẩy chưa escape = OR; escape `\,` `\|` `\$` `\\`; tiền tố số/ngày `eq ne gt lt ge le sa eb ap` | `tham số search không hợp lệ: foo` (đã đo); tìm hệ thống không `_type`: `search hệ thống có tham số family cần _type` (đã đo) |
| Modifier | `:exact :contains :text :not :missing :below :above :in :not-in :identifier :of-type`, hoặc `:<TênResource>` cho tham số reference | `:missing` cần `true`/`false`; `:in/:not-in/:above/:below` của token được giải thành danh sách mã qua ValueSet/CodeSystem trong kho (giới hạn 5000 mã) | `modifier không hợp lệ: foo` (đã đo); `:missing cần true hoặc false`; `modifier :identifier chưa được hỗ trợ` (cả `:in/:not-in` trên tham số không phải token); `tham số <code> kiểu special chưa được hỗ trợ`; `giá trị :in phải là URL chuẩn hoặc ValueSet/<id>: <v>`; `không phải ValueSet: <ref>`; `giá trị :<mod> phải có dạng system\|code: <v>`; `không phải CodeSystem: <system>`; `ValueSet có quá nhiều mã cho :in/:not-in (> 5000); thu hẹp ValueSet`; `kết quả :<mod> có quá nhiều mã (> 5000); chọn mã gốc hẹp hơn` |
| Giá trị | ngày, số, lượng, token sai cú pháp | `400` khi điều kiện được dựng | `ngày không hợp lệ: "xyz"` (đã đo khi đã có resource). Trên `MemoryRepository` lỗi giá trị chỉ lộ khi có resource để đối sánh (đo: `birthdate=xyz` ra `200` rỗng khi kho trống, `400` khi có dữ liệu); trên kho SQL điều kiện được dựng ngay trong `whereClause` nên báo luôn `(chưa kiểm HTTP)` |
| Composite | tham số kiểu composite (ví dụ `Observation?code-value-quantity=http://loinc.org\|8480-6$gt100`) | được phân tích ngay khi nhận (`CompositeConds`): sai cú pháp là `400` | thông điệp do `CompositeConds` `(chưa kiểm nguyên văn)` |
| Chained | `param[:Type].rest=value` (khóa có dấu chấm) | chạy sub-search rồi đổi thành điều kiện `param=Type/<id>,...`; cùng `Repo` nên giữ cách ly project | `chained search cần tham số reference, <code> là <kiểu>`; `kiểu đích "<T>" không hợp lệ cho <code>`; `chained search qua <code> cần chỉ rõ kiểu đích (<code>:Type.param)`; `không có kiểu đích hợp lệ cho <khóa>`; `chained search lồng quá sâu` (sâu hơn 4 cấp); `chained search/_has khớp quá nhiều resource <T> (> 5000); thu hẹp tiêu chí` |
| `_has` | `_has:Type:refParam:rest=value` | Bundle chỉ gồm resource được `Type` tham chiếu tới | `_has không hợp lệ: "_has:Observation" (cần _has:Type:param:filter)` (đã đo); `_has chỉ dùng với đúng một kiểu resource`; `_has: <p> không phải tham số reference của <T>`; `_has: <T>.<p> không trỏ tới <base>` |

**`Prefer: handling=strict|lenient` KHÔNG được cài**: header bị bỏ qua hoàn toàn, tham số lạ luôn là `400` (đã đo: `Prefer: handling=lenient` kèm `?foo=1` vẫn `400 tham số search không hợp lệ: foo`). Chế độ lenient của FHIR (bỏ qua tham số lạ, trả `self` không có chúng) không tồn tại.

**Giới hạn:** `_count` tối đa 1000 (cắt im lặng); chained/_has sâu tối đa 4 và mỗi sub-search tối đa 5000 resource; terminology `:in/:above/:below` tối đa 5000 mã; không có giới hạn số tham số hay độ dài query ngoài 16 MiB của thân (với `GET` còn giới hạn dòng yêu cầu của `net/http`, mặc định 1 MiB header `(chưa kiểm)`).

**Khác biệt theo loại resource:** mọi loại trong danh sách 171 loại đều tìm được (có chỉ mục theo `fhir.SearchParams`), nhưng ai thấy gì do `fhirauth` quyết định: quyền SMART `s` trên loại (`403` `Token thiếu quyền s (SMART) trên Patient`), `AccessPolicy` (`403` `Chính sách truy cập không cho phép Condition`, đã đo), và `AsyncJob` được miễn kiểm loại (chỉ lọc theo project). Tìm kiếm trên `AccessPolicy`, `Project`, `User`, `ProjectMembership`, `Login`... cần rule riêng khi dùng policy (rule `"*"` không bao trùm các loại này). `Binary` tìm được như loại thường (chỉ metadata; nội dung qua `GET Binary/{id}` ở mảnh A). Tìm `Subscription`, `SearchParameter`, `CodeSystem`, `ValueSet`... không có xử lý riêng ở mảnh này.

## Bundle kết quả (searchset, link, entry.search.mode)

Cấu trúc dựng ở `Router.searchBundle` (`pkg/fhirrouter/handlers.go`):

| Trường | Giá trị |
|---|---|
| `type` | `searchset` |
| `timestamp` | thời điểm dựng, UTC, độ chính xác giây, hậu tố `Z` (ví dụ `2026-11-24T10:10:54Z`) |
| `total` | chỉ khi `_total` khác `none` hoặc `_summary=count` |
| `link[]` | `self`, `next`?, `previous`? (xem Phân trang) |
| `entry[]` | vắng khi rỗng (đã đo: không có khóa `entry`); mỗi entry `{fullUrl, resource, search{mode}}` |
| `entry.fullUrl` | `<BaseURL>/<Type>/<id>`, không kèm `_history` |
| `entry.search.mode` | `match` cho kết quả chính (theo thứ tự kho trả); `include` cho resource kèm theo từ `_include` HOẶC `_revinclude` (mã không phân biệt `_revinclude` với `include`; FHIR cho `include` ở cả hai). Mọi `match` đứng trước mọi `include` |
| Không có | `id`, `meta`, `search.score`, `request`, `response`, link `first`/`last` |

Chi tiết `_include`/`_revinclude` (đã đo trên cặp Patient/Observation):
- `Observation?_include=Observation:subject` -> thêm `Patient/p1` mode `include`, chỉ resource thuộc cùng project, đọc được theo SMART/policy; trùng với `match` thì không lặp.
- `Patient?_id=p1&_revinclude=Observation:subject` -> thêm Observation tham chiếu `Patient/p1` (sắp theo `id`).
- Chỉ đi MỘT cấp: cờ `:iterate` được phân tích nhưng mã kho không lặp (`(chưa kiểm)` bằng test riêng). `_revinclude` trên tham số không phải reference bị bỏ qua im lặng. Resource kèm theo bị lọc project/policy/hiddenFields như `match`. Số resource `include` không bị chặn bởi `_count` (một trang `_count=1000` kèm `_revinclude` có thể rất lớn).
- `_elements` cũng cắt resource `include`.

Mã trả khi một resource đã bị xóa mềm: không xuất hiện trong `match` hay `include` (kho lọc `deleted = false`).

**Ví dụ: tìm theo họ, có trang kế**

```http
GET /fhir/R4/Patient?family=Tran&_count=1&_sort=family&_total=accurate HTTP/1.1
Host: fhir.example.org
Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.e30.c2ln
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
Cache-Control: no-store, no-cache, must-revalidate
X-Request-Id: CoRRGrppYDATLjJSErrZmFtRnAEWnObU

{"resourceType":"Bundle","type":"searchset","timestamp":"2026-11-24T10:10:54Z","total":2,
 "link":[{"relation":"self","url":"https://fhir.example.org/fhir/R4/Patient?_count=1&_sort=family&_total=accurate&family=Tran"},
         {"relation":"next","url":"https://fhir.example.org/fhir/R4/Patient?_count=1&_offset=1&_sort=family&_total=accurate&family=Tran"}],
 "entry":[{"fullUrl":"https://fhir.example.org/fhir/R4/Patient/p1",
           "resource":{"resourceType":"Patient","id":"p1","meta":{"versionId":"7948d425-4df6-4f5a-a751-09d6c10e62c8","lastUpdated":"2026-11-24T10:10:54.327Z","project":"p1"},"name":[{"family":"Tran"}],"gender":"male","birthDate":"1981-01-01"},
           "search":{"mode":"match"}}]}
```

**Ví dụ: không có kết quả** (`200`, không có `entry` và không có `total`)

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"Bundle","type":"searchset","timestamp":"2026-11-24T10:10:54Z","link":[{"relation":"self","url":"https://fhir.example.org/fhir/R4/Patient?_count=20&family=zzz"}]}
```

## Lỗi của entry trong batch/transaction (OperationOutcome từng entry)

Hai chế độ báo lỗi khác hẳn nhau ([POST /fhir/R4](#post-fhir-r4)):

| | batch | transaction |
|---|---|---|
| Phạm vi lỗi | MỘT entry; các entry khác vẫn chạy | cả Bundle: hủy (rollback) mọi thay đổi |
| HTTP của phản hồi | `200` (kể cả khi mọi entry lỗi) | mã HTTP của entry lỗi ĐẦU TIÊN theo thứ tự xử lý (`400`, `404`, `410`, `412`, `403`...), KHÔNG phải `400` cố định |
| Chỗ chứa lỗi | `entry[i].response.status` (`"404 Not Found"`) và `entry[i].response.outcome` (`OperationOutcome` đầy đủ) | thân là MỘT `OperationOutcome`; mỗi `issue.expression` được nối thêm `Bundle.entry[<chỉ số trong Bundle gốc>]` |
| `entry[i].resource` khi lỗi | không có | (không có Bundle phản hồi) |

- `status` có dạng `"<mã> <cụm từ chuẩn của Go>"`: `200 OK`, `201 Created`, `204 No Content`, `400 Bad Request`, `404 Not Found`, `405 Method Not Allowed`, `412 Precondition Failed`.
- Lỗi cấp BUNDLE (trước khi chạy entry nào) luôn là `400` thường, không có `expression` của entry (trừ lỗi do entry cụ thể nêu số thứ tự trong thông điệp): `Body phải là Bundle`; `Bundle.type phải là batch hoặc transaction`; `entry[<i>] không hợp lệ`; `entry[<i>] thiếu request`; `entry[<i>].request cần method và url`.
- Thông điệp của entry là thông điệp của handler tương ứng (như gọi trực tiếp route đó): ví dụ `Not found`, `Gone`, `tham số search không hợp lệ: foo`, `Phiên bản không khớp: hiện tại <versionId>`, `Thuộc tính không hợp lệ "bogus"` (kèm `expression` `Patient.bogus` và `Bundle.entry[1]` trong transaction), `resourceType không khớp: đường dẫn là Patient, body là "Observation"`.
- Entry không có route: `404` `Không có route cho GET Foo/1`; URL tuyệt đối trong `request.url` KHÔNG được hiểu (`404` `Không có route cho GET http://h/fhir/R4/Patient/p1`); `HEAD` là `405` `HEAD không được hỗ trợ cho Patient/p1`; dấu `/` đầu của URL được bỏ (`/Patient/p0` chạy bình thường).
- Quyền theo loại (SMART `cruds`, `AccessPolicy`) và **vai trò** (`read/write/admin`, như `RequiredRole`) được kiểm TỪNG entry ngay khi nhận Bundle (sửa 2026-11-24): batch cho `403` trong entry đó (`Token không đủ quyền cho thao tác này` khi thiếu vai trò, `Token thiếu quyền c (SMART) trên Observation`, `Chính sách truy cập không cho phép X`); transaction và batch async hủy cả Bundle với `403` mà không entry nào chạy.

**Ví dụ: một entry lỗi trong batch** (phần `response.outcome`)

```json
{"response":{"status":"404 Not Found","outcome":{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"not-found","details":{"text":"Not found"},"diagnostics":"Not found"}]}}}
```

**Ví dụ: transaction lỗi ở entry thứ hai**

```http
HTTP/1.1 404 Not Found
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"not-found","details":{"text":"Not found"},"diagnostics":"Not found","expression":["Bundle.entry[1]"]}]}
```

<a id="get-fhir-r4-resourcetype"></a>

## `POST /fhir/R4/{resourceType}`

Tạo resource mới (id do server cấp), tùy chọn **create có điều kiện** bằng `If-None-Exist`. [Tham chiếu trường](../reference/fhir.md#post-fhir-r4-resourcetype)

**Tiền điều kiện:** vai trò `write`; `{resourceType}` là tên resource thật.

**Thứ tự kiểm tra:** chuỗi chung ở [Thứ tự kiểm tra chung](#thứ-tự-kiểm-tra-chung-của-ghiđọc-một-resource), riêng route này:
1. Guard (`write`): 401, 403.
2. Giải mã thân: 413, 400, 415.
3. Định tuyến: 404 loại lạ.
4. `writeBody`: 400 (không phải object, `resourceType` lệch, validate). **Chạy kể cả khi `If-None-Exist` sẽ khớp.**
5. Có `If-None-Exist`: phân tích (400), vào khóa theo `(loại, tiêu chí)` trong giao dịch, tìm tối đa 2 kết quả (400 tiêu chí, 412 nhiều khớp, 200 một khớp).
6. Không điều kiện hoặc 0 khớp: gán `id` mới, bỏ `meta.versionId`/`lastUpdated`, `Scope.CreateResource` (403 theo SMART/chính sách), `Store.CreateResource` (giao dịch: `fhir_resource` + `fhir_history` + chỉ mục), phát sự kiện `create`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| P1 | Tạo thành công | thân hợp lệ, không `If-None-Exist` | 201 | `ETag: W/"<vid>"`, `Last-Modified`, `Location: <base>/<Loại>/<id>/_history/<vid>`, `Content-Type: application/fhir+json; charset=utf-8` | resource (id mới) | thêm resource, 1 dòng lịch sử (`POST`), chỉ mục, sự kiện `create` (nếu có broker) |
| P2 | Tạo, `Prefer: return=minimal` | như P1 | 201 | như P1 trừ `Content-Type` (không có) | rỗng | như P1 |
| P3 | Tạo, `Prefer: return=OperationOutcome` | như P1 | 201 | như P1 | `OperationOutcome` thông tin `OK` | như P1 |
| P4 | Create có điều kiện, 0 khớp | `If-None-Exist` hợp lệ, không resource nào khớp | 201 | như P1 | resource mới | như P1 |
| P5 | Create có điều kiện, 1 khớp | đúng một resource khớp | 200 | `ETag`, `Last-Modified` của resource **có sẵn** (không `Location`) | resource có sẵn (`minimal` thì rỗng) | **không ghi gì**, không sự kiện |
| P6 | Nhiều khớp | `If-None-Exist` khớp hai resource trở lên | 412 | - | `code=conflict`, `Nhiều resource khớp If-None-Exist` | không ghi gì |
| P7 | If-None-Exist không dùng được | không tiêu chí (`_count=1`) | 400 | - | `code=invalid`, `Thao tác có điều kiện cần tiêu chí search` | không ghi gì |
| P8 | If-None-Exist tham số lạ | `zzz=1` | 400 | - | `code=invalid`, `tham số search không hợp lệ: zzz` | không ghi gì |
| P9 | If-None-Exist hỏng | `a=%zz` | 400 | - | `code=invalid`, `If-None-Exist không hợp lệ` | không ghi gì |
| P10 | Thân không phải object | mảng, rỗng, số | 400 | - | `code=invalid`, `Body phải là một resource JSON` | không ghi gì |
| P11 | `resourceType` lệch | thân `Observation` gửi vào `/Patient`; hoặc thiếu | 400 | - | `resourceType không khớp: đường dẫn là Patient, body là "Observation"` (thiếu: `body là ""`) | không ghi gì |
| P12 | JSON hỏng | `{` | 400 | - | `JSON không hợp lệ: unexpected EOF` | không ghi gì |
| P13 | Validate thất bại | thuộc tính lạ, thiếu bắt buộc, sai kiểu, sai invariant | 400 | - | `OperationOutcome` **nhiều `issue`**, mỗi cái có `code` (`structure`, `required`, `value`, `invariant`), `details.text`, `expression` | không ghi gì |
| P14 | `Content-Type` không hỗ trợ | `text/plain` | 415 | - | `code=not-supported`, `Content-Type không hỗ trợ: text/plain` | không ghi gì |
| P15 | Loại resource lạ | `/Foo` | 404 | - | `code=not-found`, `Không có route cho POST Foo` | không ghi gì |
| P16 | Thiếu hoặc sai token | - | 401 | `WWW-Authenticate: Bearer realm="fhir"` | `login`, `Thiếu hoặc sai bearer token` (tĩnh) hoặc `Thiếu bearer token`/`Token không hợp lệ` (JWT) | không ghi gì |
| P17 | Token chỉ đọc | vai trò `read` | 403 | - | `forbidden`, `Token không đủ quyền cho thao tác này` | không ghi gì |
| P18 | Thiếu quyền SMART `c` | JWT, ví dụ `system/Observation.cruds` tạo Patient, thân đúng | 403 | - | `Token thiếu quyền c (SMART) trên Patient` | không ghi gì |
| P19 | Chính sách không cho | JWT, loại không có rule hoặc rule `readonly`/`interaction` không có `create` | 403 | - | `Chính sách truy cập không cho phép Patient` | không ghi gì |
| P20 | Chính sách `criteria` không thỏa | JWT, thân sau khi đóng dấu project không khớp `criteria` (ví dụ `gender=female`, tạo `male`) | 403 | - | `Resource không thỏa tiêu chí của chính sách truy cập` | không ghi gì |
| P21 | Thiếu project | JWT, Principal không có project | 403 | - | `Không xác định được project của người gọi` | không ghi gì |
| P22 | Thân quá lớn | > 16 MiB | 413 | - | `Body quá lớn`; `code=invalid` (khai `Content-Length`) hoặc `too-costly` (chunked) | không ghi gì |
| P23 | Hạn mức | vượt `FHIR_RATE_LIMIT` | 429 | `Retry-After: 1` | `quá nhiều yêu cầu, thử lại sau` | không ghi gì |
| P24 | Lỗi kho | CSDL lỗi | 500 | - | `code=exception`, thông điệp lỗi Go gốc (`(chưa kiểm)` nguyên văn; không được che) | giao dịch hủy |
| P25 | Trường ẩn khi tạo | JWT, rule `hiddenFields`/`readonlyFields`, thân có trường đó | 201 | như P1 | resource đã **gỡ** trường ẩn và trường chỉ-đọc | các trường đó **không được lưu** (`protectWrite` gỡ khỏi input trước khi ghi) |

Ghi chú P25 (đã đo): tạo kèm `birthDate` (ẩn) và `gender` (chỉ-đọc) ra resource không có hai trường đó.

Mã `409` (`Resource đã tồn tại: <Loại>/<id>`) có trong `Store.CreateResource` nhưng route này luôn cấp `id` mới (UUID v4) nên **không thể** tới đây; chỉ gặp trong update-as-create đồng thời (xem `PUT /{rt}/{id}`).

**Ví dụ**

```http
POST /fhir/R4/Patient HTTP/1.1
Authorization: Bearer <write_token>
Content-Type: application/fhir+json
If-None-Exist: identifier=http://hospital.example.org/mrn|MRN-0001

{"resourceType":"Patient","gender":"male","name":[{"family":"Nguyen","given":["Van A"]}],"identifier":[{"system":"http://hospital.example.org/mrn","value":"MRN-0001"}]}
```

```http
HTTP/1.1 201 Created
Content-Type: application/fhir+json; charset=utf-8
ETag: W/"d8950acc-2230-4468-999b-6db1c22017ce"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT
Location: https://fhir.example.org/fhir/R4/Patient/79e36222-b072-4e37-bf61-ef3fc9ea2667/_history/d8950acc-2230-4468-999b-6db1c22017ce

{"resourceType":"Patient","id":"79e36222-b072-4e37-bf61-ef3fc9ea2667","meta":{"versionId":"d8950acc-2230-4468-999b-6db1c22017ce","lastUpdated":"2026-11-24T10:11:08.145Z","project":"clinic-a"},"identifier":[{"system":"http://hospital.example.org/mrn","value":"MRN-0001"}],"name":[{"family":"Nguyen","given":["Van A"]}],"gender":"male"}
```

Gọi lại đúng yêu cầu trên (P5): không `Location`, không ghi gì.

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
ETag: W/"d8950acc-2230-4468-999b-6db1c22017ce"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT

{"resourceType":"Patient","id":"79e36222-b072-4e37-bf61-ef3fc9ea2667", "...": "bản đã có"}
```

`Prefer: return=minimal` (P2), thân rỗng và không `Content-Type`:

```http
HTTP/1.1 201 Created
ETag: W/"c16bdda4-f81a-4acb-99f6-f405051b1707"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT
Location: https://fhir.example.org/fhir/R4/Patient/e9c2dda6-a4b3-4dd5-b573-32a4b0da6deb/_history/c16bdda4-f81a-4acb-99f6-f405051b1707
```

Validate thất bại (P13): nhiều `issue`, mỗi cái một `expression`:

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[
 {"severity":"error","code":"value","details":{"text":"Cần boolean"},"diagnostics":"Cần boolean","expression":["Patient.active"]},
 {"severity":"error","code":"structure","details":{"text":"Thuộc tính không hợp lệ \"foo\""},"diagnostics":"Thuộc tính không hợp lệ \"foo\"","expression":["Patient.foo"]}]}
```

**Lưu ý tích hợp:**
- **Ràng buộc từ vựng (binding) không được kiểm**: `gender":"robot"` vẫn được `201` (đã đo). Validator kiểm cấu trúc, kiểu dữ liệu, bắt buộc và invariant FHIRPath, không kiểm giá trị thuộc ValueSet. Với `FHIR_SKIP_VALIDATION=true` không kiểm gì (cấu hình chặn ở production).
- Thân sai bị `400` **trước** khi biết token có quyền với loại đó hay không (xem bước 7).
- Create có điều kiện an toàn khi đồng thời nhờ khóa tư vấn; vẫn nên đặt `If-None-Exist` theo một định danh duy nhất (`identifier`).
- Không có `Content-Location`.

---

<a id="put-fhir-r4-resourcetype"></a>
## `PUT /fhir/R4/{resourceType}`

**Update có điều kiện** theo query: cập nhật resource khớp tiêu chí, hoặc tạo mới nếu không khớp. (`PATCH` và `DELETE` trên cùng đường dẫn cũng có điều kiện kiểu này nhưng ngoài swagger; nêu ở cuối mục.) [Tham chiếu trường](../reference/fhir.md#put-fhir-r4-resourcetype)

**Tiền điều kiện:** vai trò `write`; query có ít nhất một tham số search thật (không chỉ `_count`, `_sort`...).

**Thứ tự kiểm tra:**
1. Guard, giải mã, định tuyến (như `POST`).
2. `writeBody`: 400. **Thân bị kiểm trước khi biết có khớp hay không.**
3. Vào khóa `conditional-update:<loại>?<query>` trong một giao dịch.
4. Tìm tối đa 2 resource trong phạm vi người gọi (400 nếu không tiêu chí hoặc tham số lạ).
5. 0 khớp: thân có `id` thì 400; không thì tạo (`201`, như `POST`). 1 khớp: `id` thân lệch thì 400; cập nhật (như `PUT /{rt}/{id}`, có `If-Match`). Nhiều khớp: 412.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| U1 | Không khớp, tạo mới | 0 khớp, thân không `id` | 201 | `ETag`, `Last-Modified`, `Location` | resource mới (id do server cấp) | như `POST` |
| U2 | Khớp một, cập nhật | đúng 1 khớp, `id` thân trống hoặc trùng | 200 | `ETag`, `Last-Modified` | resource mới | phiên bản mới, lịch sử `PUT`, chỉ mục, sự kiện `update` |
| U3 | Không khớp nhưng thân có `id` | 0 khớp, `id` khác rỗng | 400 | - | `PUT có điều kiện không khớp resource nào thì không được kèm id` | không ghi gì |
| U4 | `id` lệch với resource khớp | 1 khớp, `id` thân khác id tìm thấy | 400 | - | `id trong body không khớp với resource tìm thấy` | không ghi gì |
| U5 | Nhiều khớp | từ 2 khớp | 412 | - | `code=conflict`, `Nhiều resource khớp tiêu chí` | không ghi gì |
| U6 | `If-Match` lệch | 1 khớp, `If-Match` không bằng `versionId` | 412 | - | `Phiên bản không khớp: hiện tại <vid>` | không ghi gì |
| U7 | Không tiêu chí | không query, hoặc chỉ `_count` | 400 | - | `Thao tác có điều kiện cần tiêu chí search` | không ghi gì |
| U8 | Tham số lạ | `zzz=1` | 400 | - | `tham số search không hợp lệ: zzz` | không ghi gì |
| U9 | Lỗi thân, quyền | như P10-P21 | 400, 401, 403, 413, 415 | - | như `POST` | không ghi gì |
| U10 | Trong phạm vi project | JWT: project B gọi cùng query mà chỉ project A có resource khớp | 201 | - | tạo trong project B (đã đo) | tạo ở B; A không bị chạm |

Không có `404` và không có `410` ở route này (kết quả tìm kiếm không gồm resource đã xóa). Mã `409` không xảy ra.

**Ví dụ**

```http
PUT /fhir/R4/Patient?identifier=http://hospital.example.org/mrn|MRN-0009 HTTP/1.1
Authorization: Bearer <write_token>
Content-Type: application/fhir+json

{"resourceType":"Patient","gender":"other","identifier":[{"system":"http://hospital.example.org/mrn","value":"MRN-0009"}]}
```

Phản hồi U1 (lần đầu) là `201` có `Location`; U2 (lần sau) là `200` không `Location`, id giữ nguyên. Nhiều khớp (U5):

```http
HTTP/1.1 412 Precondition Failed
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"conflict","details":{"text":"Nhiều resource khớp tiêu chí"},"diagnostics":"Nhiều resource khớp tiêu chí"}]}
```

**Cùng đường dẫn, ngoài swagger** (đã đo):

| Route | 0 khớp | 1 khớp | Nhiều khớp | Không tiêu chí |
|---|---|---|---|---|
| `PATCH /{rt}?...` (`json-patch+json`) | 404 `Not found` | vá như `PATCH /{rt}/{id}` (200) | 412 `Nhiều resource khớp tiêu chí` | 400 |
| `DELETE /{rt}?...` | 204 (đã đạt mục tiêu) | xóa mềm, 204; 410/404 giữa lúc tìm và xóa vẫn 204 | 412 `Nhiều resource khớp tiêu chí` | 400 |

Hai route này **không** lấy khóa tư vấn như `PUT` có điều kiện.

**Lưu ý tích hợp:** `PUT` có điều kiện là "upsert theo định danh"; thân **không** được mang `id` khi chưa có bản nào. Khác update-as-create (`PUT /{rt}/{id}` với id client chọn). Vì tiêu chí chạy trong phạm vi project của người gọi, hai project dùng cùng `identifier` không đụng nhau.

---

<a id="get-fhir-r4-resourcetype-id"></a>
## `GET /fhir/R4/{resourceType}/{id}`

Đọc phiên bản hiện tại của một resource, hỗ trợ đọc có điều kiện (`304`). [Tham chiếu trường](../reference/fhir.md#get-fhir-r4-resourcetype-id)

**Tiền điều kiện:** vai trò `read`. Với `Binary` thì có thêm đường thô (xem cuối mục).

**Thứ tự kiểm tra:**
1. Guard (`read`): 401, 403.
2. Định tuyến: 404 loại lạ; `HEAD` ra 405.
3. `Scope.ReadResource` (JWT): project (403), `allow(r)` (403 SMART hoặc chính sách), đọc kho (404 chưa có, **410 đã xóa**), project khác hoặc không thỏa `criteria` (404), gỡ `hiddenFields`.
4. Đọc có điều kiện (`notModified`): `If-None-Match` thắng `If-Modified-Since`.
5. `Binary` + `Accept` khớp: trả thô (200 raw); không thì resource JSON.
6. `ResponseHook` (nếu bật kho Binary).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| R1 | Đọc thành công | resource có, trong phạm vi | 200 | `ETag: W/"<vid>"`, `Last-Modified` | resource (đã gỡ trường ẩn nếu có) | không |
| R2 | `If-None-Match` khớp | một trong các etag (đã bỏ `W/` và nháy) bằng `versionId`, hoặc `*` | 304 | `ETag`, `Last-Modified` | rỗng, không `Content-Type` | không |
| R3 | `If-None-Match` không khớp | etag khác | 200 | như R1 | resource | không |
| R4 | `If-Modified-Since` chưa đổi | `lastUpdated` (cắt về giây) không muộn hơn mốc | 304 | như R2 | rỗng | không |
| R5 | `If-Modified-Since` đã đổi hoặc ngày sai | mốc cũ hơn, hoặc không phân tích được (bỏ qua điều kiện) | 200 | như R1 | resource | không |
| R6 | Cả hai header | `If-None-Match` có mặt | theo `If-None-Match` | - | - | `If-Modified-Since` **không xét** khi đã có `If-None-Match`, kể cả khi `If-None-Match` không khớp |
| R7 | Không có | chưa từng tạo, hoặc id của project khác (JWT) | 404 | - | `code=not-found`, `Not found` | không |
| R8 | Đã xóa | xóa mềm | 410 | - | `code=deleted`, `Gone` | không |
| R9 | Loại lạ | `/Foo/x` | 404 | - | `Không có route cho GET Foo/x` | không |
| R10 | `HEAD` | method `HEAD` | 405 | - | `HEAD không được hỗ trợ cho Patient/<id>` | không |
| R11 | Thiếu quyền SMART `r` | JWT, ví dụ `system/Patient.s` hoặc `.c` | 403 | - | `Token thiếu quyền r (SMART) trên Patient` | không |
| R12 | Chính sách không cho | loại không có rule | 403 | - | `Chính sách truy cập không cho phép Observation` | không |
| R13 | Resource không thỏa `criteria` | JWT, rule có `criteria` | 404 | - | `Not found` | không |
| R14 | Token, hạn mức | như `POST` | 401, 403, 429 | - | như common.md | không |

Đọc luôn đi qua kiểm quyền **trước** điều kiện: người không có quyền đọc không thể dùng `304` để dò sự tồn tại (R2 chỉ đến khi R1 thỏa).

**Không có tác dụng:** `_summary`, `_elements`, `_format`, `_pretty` trên `GET /{rt}/{id}` **bị bỏ qua**: đã đo `?_summary=true&_elements=gender` vẫn trả toàn bộ resource. (`_elements` chỉ có tác dụng ở search.) `swagger.json` không khai các tham số này cho route đọc.

**Giới hạn 410 xuyên project (đã đo):** resource **đã xóa** không còn nội dung để so project, nên đọc id đã xóa của project khác trả `410` `Gone` thay vì `404` (mã ghi nhận trong chú thích của `fhirauth.Repo`; id là UUID nên khó đoán). Với resource chưa xóa là 404.

**Ví dụ**

```http
GET /fhir/R4/Patient/79e36222-b072-4e37-bf61-ef3fc9ea2667 HTTP/1.1
Authorization: Bearer <read_token>
If-None-Match: W/"d8950acc-2230-4468-999b-6db1c22017ce"
```

```http
HTTP/1.1 304 Not Modified
ETag: W/"d8950acc-2230-4468-999b-6db1c22017ce"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT
```

```http
HTTP/1.1 410 Gone
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"deleted","details":{"text":"Gone"},"diagnostics":"Gone"}]}
```

**Đọc `Binary` thô** (đã đo khi không bật kho):

| `Accept` | Kết quả |
|---|---|
| không có, `application/fhir+json`, hoặc loại khác `contentType` (ví dụ `image/png`) | resource Binary JSON (R1), có `data` base64 |
| bằng `contentType` của Binary (ví dụ `text/plain`) hoặc `application/octet-stream` | `200`, `Content-Type: <contentType>`, thân là byte đã giải mã base64, **không** `ETag`/`Last-Modified` (kiểm `304` đi trước nhánh thô) |

Khi bật kho Binary, `RawBinary` chặn `GET /Binary/{id}` **trước** router: trả nội dung thô từ kho trừ khi `Accept` bắt đầu bằng `application/fhir+json`/`application/json` hoặc có `_format`; cùng lỗi 404/403/410 như đường FHIR (nó gọi lại handler); thêm `403` `Không có quyền đọc ngữ cảnh bảo mật của Binary` khi người gọi không đọc được `securityContext`. Header tải về, `If-None-Match` trên đường thô `(chưa kiểm)` (xem `pkg/fhirbinary/raw.go`, `serveContent`).

**Lưu ý tích hợp:** `ETag` là `versionId` UUID, dùng được trực tiếp cho `If-Match` của `PUT`/`PATCH`. Đọc hai lần liên tiếp không đổi `ETag` trừ khi có ghi. `Last-Modified` chỉ có độ phân giải giây; dùng `ETag` cho so sánh chính xác.

---

<a id="put-fhir-r4-resourcetype-id"></a>
## `PUT /fhir/R4/{resourceType}/{id}`

Cập nhật toàn bộ resource (thay thế, không trộn), kiểm khóa lạc quan bằng `If-Match`; tùy cấu hình có thể **tạo mới** với id do client chọn (update-as-create). [Tham chiếu trường](../reference/fhir.md#put-fhir-r4-resourcetype-id)

**Tiền điều kiện:** vai trò `write`. Update-as-create cần `FHIR_UPDATE_AS_CREATE=true` (mặc định **tắt**, thì `PUT` id chưa có ra `404`).

**Thứ tự kiểm tra:**
1. Guard (`write`), giải mã thân, định tuyến.
2. `writeBody`: object, `resourceType`, validate (400). `PUT` thân **không có `id`** vẫn hợp lệ.
3. `id` thân khác id đường dẫn: 400 `id trong body không khớp với đường dẫn`.
4. `updateFrom`: ghi đè `id` bằng id đường dẫn, rồi `Scope.UpdateResource`: project, `allow(u)` (403), `owned` (đọc bản hiện tại: 404/410, hoặc khác project / không thỏa `criteria` là 404), `protectWrite` nếu có quy tắc trường, đóng dấu project, kiểm `criteria` bản mới (403).
5. `Store.UpdateResource` (giao dịch, khóa dòng): 404 chưa có, 410 đã xóa, **rồi** `If-Match` (412), ghi bản mới, lịch sử, chỉ mục.
6. Nếu bước 4-5 ra `404` và `UpdateAsCreate` bật: xem U-nhánh dưới.
7. Sự kiện `update`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| S1 | Cập nhật thành công | resource có, quyền đủ | 200 | `ETag` (mới), `Last-Modified` | resource mới | `versionId` mới, dòng lịch sử `PUT`, chỉ mục, sự kiện `update` |
| S2 | `Prefer: return=minimal` | như S1 | 200 | như S1, không `Content-Type` | rỗng | như S1 |
| S3 | Cập nhật y hệt | thân giống bản hiện tại | 200 | `ETag` **khác** | resource | **vẫn sinh phiên bản mới** (đã đo) |
| S4 | `If-Match` khớp | bằng `versionId` hiện tại (dạng `W/"v"`, `"v"`, `v`) | 200 | như S1 | resource | như S1 |
| S5 | `If-Match` lệch | `If-Match` cũ, `*`, hoặc danh sách | 412 | - | `code=conflict`, `Phiên bản không khớp: hiện tại <vid hiện tại>` | không ghi gì |
| S6 | Không có resource | id chưa từng tạo, `UpdateAsCreate` tắt | 404 | - | `code=not-found`, `Not found` | không ghi gì |
| S7 | Đã xóa | resource xóa mềm | 410 | - | `code=deleted`, `Gone` | không ghi gì (**không** hồi sinh, kể cả khi bật update-as-create) |
| S8 | Project khác | JWT, id của project khác (chưa xóa) | 404 | - | `Not found` | không ghi gì |
| S9 | `id` thân lệch | `id` khác đường dẫn | 400 | - | `id trong body không khớp với đường dẫn` | không ghi gì |
| S10 | Lỗi thân | như P10-P14 (kể cả `resourceType` lệch) | 400, 415 | - | như `POST` | không ghi gì |
| S11 | Thiếu quyền | token đọc: 403 Guard; SMART thiếu `u`: `Token thiếu quyền u (SMART) trên Patient`; chính sách `readonly`: `Chính sách truy cập không cho phép Patient` | 403 | - | như trên | không ghi gì |
| S12 | Chính sách `criteria` | bản mới không thỏa `criteria` | 403 | - | `Resource không thỏa tiêu chí của chính sách truy cập` | không ghi gì |
| S13 | Update-as-create, tạo mới | `UpdateAsCreate` bật, id hợp lệ chưa có, **không** `If-Match` | 201 | `ETag`, `Last-Modified`, `Location` | resource với id do client chọn (thân không `id` vẫn dùng id đường dẫn, đã đo) | như `POST` nhưng id do client chọn, lịch sử `POST` |
| S14 | Update-as-create, `If-Match` | có `If-Match` và id chưa có | 404 | - | `Not found` | không ghi gì (`If-Match` đòi một phiên bản đang có) |
| S15 | Update-as-create, id sai định dạng | không khớp `^[A-Za-z0-9\-.]{1,64}$` (id đường dẫn dài, thân không `id`) | 400 | - | `id không hợp lệ cho update-as-create (1-64 ký tự chữ, số, '-', '.')` | không ghi gì |
| S16 | Update-as-create, id sai định dạng ở thân | thân có `id` như `a_b` | 400 | - | validator bắt trước: `Giá trị không đúng định dạng id: "a_b"`, `expression` `Patient.id` | không ghi gì |
| S17 | Update-as-create, tranh chấp | hai `PUT` đồng thời cùng id mới | 201 rồi 200 | - | bên đến sau cập nhật bản vừa tạo (nếu cùng project) hoặc 404 (nếu id thuộc project khác) | giao dịch tuần tự theo id (test `testConcurrentUpdateAsCreate`) |
| S18 | Update-as-create, thiếu quyền `c` | JWT chỉ có `u` | 403 | - | `Token thiếu quyền c (SMART) trên Patient` | không ghi gì (theo mã) |
| S19 | Update-as-create, id của project khác | JWT, id đang thuộc project A, người của B gọi | 404 | - | `Not found` (không lộ sự tồn tại; đã đo) | không ghi gì |

**Ví dụ**

```http
PUT /fhir/R4/Patient/79e36222-b072-4e37-bf61-ef3fc9ea2667 HTTP/1.1
Authorization: Bearer <write_token>
Content-Type: application/fhir+json
If-Match: W/"d8950acc-2230-4468-999b-6db1c22017ce"

{"resourceType":"Patient","id":"79e36222-b072-4e37-bf61-ef3fc9ea2667","gender":"female"}
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
ETag: W/"d69b45eb-692a-4b0e-86f6-aea67f040a86"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT

{"resourceType":"Patient","id":"79e36222-b072-4e37-bf61-ef3fc9ea2667","meta":{"versionId":"d69b45eb-692a-4b0e-86f6-aea67f040a86","lastUpdated":"2026-11-24T10:11:08.157Z"},"gender":"female"}
```

```http
HTTP/1.1 412 Precondition Failed
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"conflict","details":{"text":"Phiên bản không khớp: hiện tại d69b45eb-692a-4b0e-86f6-aea67f040a86"},"diagnostics":"Phiên bản không khớp: hiện tại d69b45eb-692a-4b0e-86f6-aea67f040a86"}]}
```

**hiddenFields và readonlyFields trên `PUT` (đã đo, JWT):** bản đọc của người có rule `hiddenFields:["birthDate","name.family"]`, `readonlyFields:["gender"]` thiếu hai trường ẩn. Khi người đó `PUT`: các trường này bị gỡ khỏi thân, rồi **chỉ trường cấp cao nhất** được khôi phục từ bản đang lưu (`gender` giữ nguyên `female` dù thân gửi `male`; `birthDate` giữ nguyên). Trường **lồng** (`name.family`) **không** được khôi phục: bản lưu sau đó mất `family` (đã đo: `name` từ `[{"family":"Nguyen","given":["An"]}]` thành `[{"given":["Binh"]}]`). Hành vi này khớp ghi chú trong `fieldrules.go` ("chỉ trường cấp cao nhất mới khôi phục được") nhưng là **mất dữ liệu ẩn** với đường dẫn lồng; xem mục cuối.

**Lưu ý tích hợp:** `PUT` là thay thế toàn bộ: trường không gửi sẽ mất (đã đo: `PUT` thân chỉ có `resourceType` và `id` xóa mọi trường khác). Dùng `PATCH` để đổi một phần. Dùng `If-Match` để tránh ghi đè lẫn nhau; không có `If-Match` thì bên ghi sau thắng.

---

<a id="delete-fhir-r4-resourcetype-id"></a>
## `DELETE /fhir/R4/{resourceType}/{id}`

Xóa **mềm** một resource (đánh dấu `deleted`, giữ lịch sử). [Tham chiếu trường](../reference/fhir.md#delete-fhir-r4-resourcetype-id)

**Tiền điều kiện:** vai trò `write`. Không có điều kiện trên đường này: `If-Match` bị bỏ qua (đã đo); xóa có điều kiện theo query ở mục [PUT có điều kiện](#put-fhir-r4-resourcetype).

**Thứ tự kiểm tra:**
1. Guard (`write`), định tuyến.
2. `Scope.DeleteResource` (JWT): project, `allow(d)` (403), `owned` (404 chưa có / khác project / không thỏa `criteria`; **410 coi như đã xóa**).
3. `fhirsub.Wrap.DeleteResource`: đọc bản cuối (để làm thân sự kiện) rồi xóa.
4. `Store.DeleteResource` (giao dịch, khóa dòng): 404 chưa từng có, 410 đã xóa; ghi bản tombstone (`deleted=true`, nội dung chỉ `resourceType`, `id`, `meta`), dòng lịch sử `DELETE`, xóa chỉ mục.
5. Router đổi `410` thành thành công (xóa lặp lại idempotent).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| D1 | Xóa thành công | resource có | 204 | không `ETag`, không `Content-Type` | rỗng | `versionId` tombstone mới, 1 dòng lịch sử `DELETE`, xóa chỉ mục, sự kiện `delete` (mang bản cuối) |
| D2 | Xóa lại | đã xóa | 204 | - | rỗng | **không** sinh phiên bản, không sự kiện (đã đo: lịch sử không tăng) |
| D3 | Chưa từng tồn tại | id lạ | 404 | - | `code=not-found`, `Not found` | không ghi gì |
| D4 | Project khác, resource còn sống | JWT | 404 | - | `Not found` | không ghi gì |
| D5 | Project khác, resource đã xóa | JWT | 204 | - | rỗng | không ghi gì (không thể thay đổi gì; đã đo) |
| D6 | Token chỉ đọc | vai trò `read`, hoặc chỉ scope `.r` | 403 | - | `Token không đủ quyền cho thao tác này` | không ghi gì |
| D7 | Thiếu SMART `d` | JWT có ghi ở loại khác hoặc chỉ `cu` | 403 | - | `Token thiếu quyền d (SMART) trên Patient` | không ghi gì |
| D8 | Chính sách không cho | rule `readonly`, `interaction` không có `delete`, hoặc loại không có rule | 403 | - | `Chính sách truy cập không cho phép Patient` | không ghi gì |
| D9 | `If-Match` | bất kỳ | như D1/D2 | - | - | **bị bỏ qua** |
| D10 | Token, hạn mức, loại lạ | như `POST` | 401, 403, 404, 429 | - | như common.md | không ghi gì |

Không có `412`, `409`, `200` ở route này.

**Ví dụ**

```http
DELETE /fhir/R4/Patient/79e36222-b072-4e37-bf61-ef3fc9ea2667 HTTP/1.1
Authorization: Bearer <write_token>
```

```http
HTTP/1.1 204 No Content
```

**Sau khi xóa** (đã đo): `GET` ra `410`, `PUT` ra `410`, `PATCH` ra `410`; `GET .../_history` vẫn `200` (có entry `DELETE` ở đầu, không có `resource`); `GET .../_history/{vid cũ}` vẫn `200`; `GET .../_history/{vid của tombstone}` ra `410`. Tìm kiếm không còn thấy resource (chỉ mục đã xóa).

**Lưu ý tích hợp:** xóa **không** xóa nội dung `Binary` ở kho (chỉ `$expunge`) và **không** xóa concept của `CodeSystem` (xem mục loại đặc biệt). Không có "hoàn tác xóa" qua REST (`PUT` ra `410`); cần `$expunge` rồi tạo lại. Tham chiếu tới resource đã xóa không bị kiểm (không có toàn vẹn tham chiếu).

---

<a id="patch-fhir-r4-resourcetype-id"></a>
## `PATCH /fhir/R4/{resourceType}/{id}`

Sửa từng phần bằng **JSON Patch** (RFC 6902, `application/json-patch+json`). **FHIRPath Patch (`Parameters`) không được hỗ trợ.** [Tham chiếu trường](../reference/fhir.md#patch-fhir-r4-resourcetype-id)

**Tiền điều kiện:** vai trò `write`. Cần **cả** quyền đọc (`r`) và cập nhật (`u`) theo loại (đã đo: `system/Patient.cu` ra `Token thiếu quyền r (SMART) trên Patient`).

**Thứ tự kiểm tra:**
1. Guard (`write`), giải mã thân (thân giải theo `+json`), định tuyến.
2. `Content-Type` có đặt mà không chứa `json-patch+json`: 415. (Không đặt `Content-Type` thì qua.)
3. Thân phải là mảng JSON (400).
4. **Đọc** bản hiện tại qua kho (`Scope.ReadResource`: project, `r`, 404/410, **bản đã gỡ `hiddenFields`**).
5. Áp các thao tác trong bộ nhớ lên bản sao (không ghi dần): lỗi dừng ở thao tác đầu tiên hỏng (400).
6. Kiểm kết quả: là object, `resourceType` và `id` không đổi (400).
7. Validate lại toàn bộ kết quả (400), trừ `FHIR_SKIP_VALIDATION`.
8. `updateFrom` với `If-Match` (như `PUT`): `Scope.UpdateResource` (`u`, `criteria`, `protectWrite`), kho (404/410/412), ghi.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| T1 | Vá thành công | mảng thao tác hợp lệ | 200 | `ETag` (mới), `Last-Modified` | resource mới | `versionId` mới, dòng lịch sử **`PUT`** (không có phương thức `PATCH` trong lịch sử), chỉ mục, sự kiện `update` |
| T2 | `Prefer: return=minimal` | như T1 | 200 | như T1, không `Content-Type` | rỗng | như T1 |
| T3 | Mảng rỗng | `[]` | 200 | `ETag` **mới** | resource | vẫn sinh phiên bản mới (đã đo) |
| T4 | `Content-Type` sai | `application/json`, `application/fhir+json` | 415 | - | `code=not-supported`, `PATCH cần Content-Type application/json-patch+json` | không ghi gì |
| T5 | `Content-Type` lạ | `text/plain`, v.v. | 415 | - | `Content-Type không hỗ trợ: text/plain` (từ bước giải mã) | không ghi gì |
| T6 | FHIRPath Patch | `Content-Type: application/fhir+json`, thân `Parameters` | 415 | - | `PATCH cần Content-Type application/json-patch+json` | không ghi gì |
| T7 | Thân không phải mảng | object (kể cả `Parameters` dưới `json-patch+json`), hoặc thân rỗng | 400 | - | `Body của PATCH phải là một mảng JSON Patch` | không ghi gì |
| T8 | Thao tác hỏng | op lạ, thiếu `path`/`value`, khóa không có, chỉ số mảng sai, `test` thất bại | 400 | - | `JSON Patch không áp dụng được: thao tác <i> (<op> <path>): <lý do>` | không ghi gì |
| T9 | Đổi `resourceType` | patch `/resourceType` | 400 | - | `Không được đổi resourceType bằng PATCH` | không ghi gì |
| T10 | Đổi `id` | patch `/id` | 400 | - | `Không được đổi id bằng PATCH` | không ghi gì |
| T11 | Kết quả không phải resource | xóa gốc, hoặc thay gốc bằng giá trị khác | 400 | - | `JSON Patch không áp dụng được: ... không xóa được gốc của tài liệu` hoặc `Kết quả của JSON Patch phải là một resource` | không ghi gì |
| T12 | Kết quả không hợp lệ | thêm thuộc tính lạ, sai kiểu | 400 | - | `OperationOutcome` validate (như P13) | không ghi gì |
| T13 | `If-Match` lệch | - | 412 | - | `Phiên bản không khớp: hiện tại <vid>` | không ghi gì |
| T14 | Không có | id chưa từng tạo / project khác | 404 | - | `Not found` | không ghi gì |
| T15 | Đã xóa | xóa mềm | 410 | - | `Gone` | không ghi gì |
| T16 | Thiếu quyền | token đọc; thiếu SMART `r` hoặc `u`; chính sách | 403 | - | `Token không đủ quyền cho thao tác này`, `Token thiếu quyền r (SMART) trên <Loại>` (đọc trước), `... u ...`, hoặc `Chính sách truy cập không cho phép <Loại>` | không ghi gì |
| T17 | Sửa `meta.versionId` | patch `/meta/versionId` | 200 | `ETag` mới | giá trị gửi bị bỏ, server cấp `versionId` mới (đã đo) | như T1 |

**Lý do trong T8** (nguyên văn, ghép sau `thao tác <i> (<op> <path>): `): `op không hỗ trợ: "bogus"`, `thiếu path`, `thiếu value`, `không có khóa "nothere"`, `chỉ số mảng không hợp lệ: "x"`, `không đi tiếp được tại "x"`, `không thêm được vào giá trị nguyên thủy`, `không xóa được trong giá trị nguyên thủy`, `không xóa được gốc của tài liệu`, `test thất bại`, `JSON Pointer không hợp lệ: "x"`, và `thao tác <i> không phải object`. Hỗ trợ `add`, `remove`, `replace`, `move`, `copy`, `test` (`add` cần cha tồn tại; `replace` cần đích tồn tại; `-` thêm cuối mảng).

**Ví dụ**

```http
PATCH /fhir/R4/Patient/79e36222-b072-4e37-bf61-ef3fc9ea2667 HTTP/1.1
Authorization: Bearer <write_token>
Content-Type: application/json-patch+json
If-Match: W/"d69b45eb-692a-4b0e-86f6-aea67f040a86"

[{"op":"replace","path":"/gender","value":"other"},{"op":"add","path":"/name/-","value":{"family":"Tran"}}]
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
ETag: W/"d6ee91bf-40e9-4f79-8b9e-6ad86009cc9c"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT

{"resourceType":"Patient","id":"79e36222-b072-4e37-bf61-ef3fc9ea2667","meta":{"versionId":"d6ee91bf-40e9-4f79-8b9e-6ad86009cc9c","lastUpdated":"2026-11-24T10:11:08.163Z"},"gender":"other","name":[{"family":"Nguyen"},{"family":"Tran"}]}
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"invalid","details":{"text":"JSON Patch không áp dụng được: thao tác 0 (remove /nothere): không có khóa \"nothere\""},"diagnostics":"JSON Patch không áp dụng được: thao tác 0 (remove /nothere): không có khóa \"nothere\""}]}
```

**Lưu ý tích hợp:**
- **Không có khóa lạc quan tự động**: đọc rồi ghi là hai bước riêng (đọc ở bước 4 ngoài giao dịch của bước 8); hai `PATCH` đồng thời lên cùng phiên bản đều thành công và bên ghi sau đè bên trước (theo mã, chưa đo). Gửi `If-Match` để chặn.
- Người có `hiddenFields` vá trên **bản đã gỡ trường ẩn** (nên `test` trên trường ẩn thất bại: `không có khóa "birthDate"`, đã đo). Trường ẩn cấp cao nhất được khôi phục khi ghi; trường ẩn **lồng** (`name.family`) không được khôi phục (cùng đường `protectWrite` với `PUT`; đã đo bằng `PUT`, `PATCH` suy từ mã).
- Binding và ràng buộc từ vựng không kiểm (`replace /gender` thành `robot` được `200`, đã đo).
- Lịch sử ghi `PUT` cho bản vá; sự kiện là `update`.

---

<a id="get-fhir-r4-resourcetype-id-history"></a>
## `GET /fhir/R4/{resourceType}/{id}/_history`

Lịch sử mọi phiên bản của **một** resource (kể cả bản xóa), mới nhất trước. [Tham chiếu trường](../reference/fhir.md#get-fhir-r4-resourcetype-id-history)

**Tiền điều kiện:** vai trò `read`.

**Thứ tự kiểm tra:**
1. Guard (`read`), định tuyến.
2. `Scope.ReadHistory` (JWT): project, `allow(r)`, `permit(history)` (403), đọc toàn bộ dòng lịch sử (404 nếu không có dòng nào), **mọi bản còn nội dung phải thuộc project và thỏa `criteria`** (không thì 404), gỡ `hiddenFields` ở từng bản.
3. Router dựng `Bundle` (không phân trang).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| H1 | Có lịch sử | resource từng tồn tại (kể cả đã xóa) | 200 | `Content-Type: application/fhir+json` (không `ETag`) | `Bundle` `type=history`, `total`, `entry[]` | không |
| H2 | Chưa từng tồn tại | id lạ | 404 | - | `Not found` | không |
| H3 | Project khác | JWT, có bản còn nội dung thuộc project khác | 404 | - | `Not found` | không |
| H4 | Project khác, đã xóa (không bản nào có nội dung hợp lệ) | JWT | 404 | - | `Not found` | không |
| H5 | Chính sách không cho history | rule có `interaction` không chứa `history` | 403 | - | `Chính sách truy cập không cho phép history trên Patient` | không |
| H6 | Thiếu SMART `r` / chính sách loại | JWT | 403 | - | `Token thiếu quyền r (SMART) trên Patient`, `Chính sách truy cập không cho phép Patient` | không |
| H7 | Token, hạn mức, loại lạ | như `POST` | 401, 403, 404, 429 | - | như common.md | không |

**Cấu trúc entry (đã đo):** mỗi `entry` có `fullUrl` = `<BaseURL>/<Loại>/<id>` (**không** kèm `_history/<vid>`, nên mọi entry cùng `fullUrl`), `request.method` (`POST`/`PUT`/`DELETE`) và `request.url` = `<Loại>/<id>`, `response.status` (`"201"` cho `POST`, `"200"` cho `PUT`, `"204"` cho `DELETE`), `response.etag` = `W/"<vid>"`, `response.lastModified` = `lastUpdated` (mili giây). Bản xóa **không có `resource`**. `total` = số entry của **toàn bộ** lịch sử. `PATCH` hiện là `PUT`.

**Không có phân trang, không có lọc (đã đo, và lệch swagger):** `swagger.json` khai `_count` và `_offset`, nhưng mã **bỏ qua** mọi tham số query (`_count`, `_offset`, `_since`, `_at`, `_sort`, `_type`): `?_count=1&_since=2001-01-01` vẫn trả mọi 10 entry. Không có `link` (`self`, `next`). Lịch sử dài trả nguyên một Bundle (không trần).

**Ví dụ**

```http
GET /fhir/R4/Patient/656e29c5-86c9-41f6-8f7d-81d4d2d901c7/_history HTTP/1.1
Authorization: Bearer <read_token>
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"Bundle","type":"history","timestamp":"2026-11-24T10:13:47Z","total":2,"entry":[
 {"fullUrl":"https://fhir.example.org/fhir/R4/Patient/656e29c5-86c9-41f6-8f7d-81d4d2d901c7","request":{"method":"DELETE","url":"Patient/656e29c5-86c9-41f6-8f7d-81d4d2d901c7"},"response":{"status":"204","etag":"W/\"b2ff63a5-838c-482b-914d-5a37c3009303\"","lastModified":"2026-11-24T10:13:47.986Z"}},
 {"fullUrl":"https://fhir.example.org/fhir/R4/Patient/656e29c5-86c9-41f6-8f7d-81d4d2d901c7","resource":{"resourceType":"Patient","id":"656e29c5-86c9-41f6-8f7d-81d4d2d901c7","meta":{"versionId":"b7944496-277e-43e0-bb03-491a8795a555","lastUpdated":"2026-11-24T10:13:47.985Z"}},"request":{"method":"POST","url":"Patient/656e29c5-86c9-41f6-8f7d-81d4d2d901c7"},"response":{"status":"201","etag":"W/\"b7944496-277e-43e0-bb03-491a8795a555\"","lastModified":"2026-11-24T10:13:47.985Z"}}]}
```

**Lưu ý tích hợp:** lịch sử **cấp loại** (`/{rt}/_history`) và **hệ thống** (`/_history`) không được cài: `GET /Patient/_history` khớp route `/{rt}/{id}` với `id=_history` nên ra `404` `Not found` (theo mã, `(chưa kiểm)` bằng đo). Dùng `versionId` trong `response.etag` làm `{vid}` của vread.

---

<a id="get-fhir-r4-resourcetype-id-history-vid"></a>
## `GET /fhir/R4/{resourceType}/{id}/_history/{vid}`

Đọc **một phiên bản cụ thể** (vread). `vid` là `versionId` UUID, không phải số thứ tự. [Tham chiếu trường](../reference/fhir.md#get-fhir-r4-resourcetype-id-history-vid)

**Tiền điều kiện:** vai trò `read`.

**Thứ tự kiểm tra:**
1. Guard (`read`), định tuyến.
2. `Scope.ReadVersion` (JWT): project, `allow(r)` (403), `permit(vread)` (403), kho tra `fhir_history` theo `(loại, id, vid)` (404 không có; **410 nếu bản đó là tombstone**), project khác hoặc không thỏa `criteria` (404), gỡ `hiddenFields`.
3. `ResponseHook`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| V1 | Đọc được | phiên bản có nội dung, trong phạm vi | 200 | `ETag: W/"<vid>"`, `Last-Modified` (của phiên bản đó) | resource đúng như tại phiên bản đó | không |
| V2 | Bản cũ của resource đã xóa | `vid` là bản trước khi xóa | 200 | như V1 | resource cũ | không (đã đo) |
| V3 | Bản tombstone | `vid` là bản xóa | 410 | - | `code=deleted`, `Gone` | không |
| V4 | Không có `vid` | `vid` lạ, hoặc `vid` thuộc `id` khác | 404 | - | `Not found` | không |
| V5 | Project khác | JWT, phiên bản có nội dung của project khác | 404 | - | `Not found` | không |
| V6 | Project khác, bản tombstone | JWT | 410 | - | `Gone` | không (cùng giới hạn như `GET` đã xóa: tombstone không có nội dung để so project) |
| V7 | Chính sách không cho vread | rule `interaction` không có `vread` | 403 | - | `Chính sách truy cập không cho phép vread trên Patient` | không |
| V8 | Thiếu SMART `r` / chính sách loại | JWT | 403 | - | `Token thiếu quyền r (SMART) trên Patient`, `Chính sách truy cập không cho phép Patient` | không |
| V9 | Token, hạn mức, loại lạ | như `POST` | 401, 403, 404, 429 | - | như common.md | không |

Route này **không** có đọc có điều kiện: `If-None-Match`, `If-Modified-Since` bị bỏ qua (không bao giờ `304`).

**Ví dụ**

```http
GET /fhir/R4/Patient/79e36222-b072-4e37-bf61-ef3fc9ea2667/_history/d8950acc-2230-4468-999b-6db1c22017ce HTTP/1.1
Authorization: Bearer <read_token>
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
ETag: W/"d8950acc-2230-4468-999b-6db1c22017ce"
Last-Modified: Tue, 24 Nov 2026 10:11:08 GMT

{"resourceType":"Patient","id":"79e36222-b072-4e37-bf61-ef3fc9ea2667","meta":{"versionId":"d8950acc-2230-4468-999b-6db1c22017ce","lastUpdated":"2026-11-24T10:11:08.145Z"},"gender":"male"}
```

**Lưu ý tích hợp:** lịch sử được giữ vĩnh viễn (không có thời hạn); chỉ `$expunge` xóa. Resource đã xóa vẫn đọc được các phiên bản trước (V2) bởi người cùng project và đủ quyền: coi đó là hành vi **dữ liệu "đã xóa" vẫn truy xuất được qua vread/history**.

---

## `GET /fhir/R4/{resourceType}`

Tìm kiếm theo loại: dựng `Request` tìm kiếm từ query, chạy trên `Repo` của người gọi và trả Bundle `searchset`. [Tham chiếu trường](../reference/fhir.md#get-fhir-r4-resourcetype)

**Tiền điều kiện / cờ bật route:** luôn bật. Vai trò tối thiểu `read` (`RequiredRole`); token tĩnh: không có project, không Grants/Policy; JWT: tách project, SMART và `AccessPolicy` do `fhirauth.Scope`.

**Thứ tự kiểm tra** (theo mã):
1. Middleware chung và `Guard` (mục "Thứ tự xử lý chung"): `429`, `401`, `403`.
2. `readRequest`: GET không thân thì không có lỗi ở đây.
3. `Router.match`: `resourceType` lạ -> `404`; method khác `GET`/`POST _search`... -> `405`.
4. `fhirsearch.Parse`: duyệt tham số theo thứ tự chữ cái, dừng ở lỗi đầu: mọi `400` ở mục "Tham số tìm kiếm và lỗi của chúng" và "Phân trang". Kiểm `_summary=count`: đổi `Count=0`, `Total=accurate`.
5. `searchRepo`: giải `:in/:not-in/:above/:below` (đọc ValueSet/CodeSystem trong kho), giải chained và `_has` bằng sub-search (có thể `400` do giới hạn 5000/độ sâu 4). Đây là bước duy nhất ngoài `Parse` còn sinh `400` theo dữ liệu.
6. `Repo.Search` (`pkg/fhirauth/scope.go`): `projectOf` (`403` `Không xác định được project của người gọi` nếu principal thiếu project); `allow(PermSearch)` cho loại (`403`); thêm điều kiện của `AccessPolicy` (tiêu chí, `_compartment`) và điều kiện `_project=<project>` vào yêu cầu; chạy kho.
7. Kho SQL: `WHERE deleted=false AND resource_type=? AND ...`, đếm `total` (nếu cần), `ORDER BY` (khóa người dùng, rồi loại, rồi `id`), `LIMIT count+1 OFFSET offset`, nạp `include`/`revinclude`.
8. Lọc lại kết quả theo project, SMART (`CanSee`), `AccessPolicy` rồi gỡ `hiddenFields`; nếu `Repo.Search`/kho lỗi hạ tầng -> `500` `exception` (thông điệp từ `err.Error()` của kho, không chủ ý lộ chi tiết `(chưa kiểm nguyên văn)`).
9. `searchBundle` dựng Bundle; `ResponseHook` (nếu có); `200`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| S1 | Có kết quả, trang đầy | khớp nhiều hơn `_count` | 200 | `Content-Type: application/fhir+json; charset=utf-8` | Bundle `searchset` có `entry[]`, `link self` và `next` | không ghi gì (chỉ đọc) |
| S2 | Trang cuối | số khớp còn lại `<= _count` | 200 | như S1 | `link self` (và `previous` nếu `offset>0`), không `next` | không |
| S3 | Không khớp | không resource nào | 200 | như S1 | Bundle không có `entry`, không `total` (trừ khi yêu cầu `_total`) | không |
| S4 | Chỉ đếm | `_summary=count` | 200 | như S1 | `total` có, không `entry`; `self` có `_count=0&_total=accurate` | không |
| S5 | `_count=0` | | 200 | như S1 | không `entry`, không `next` (kho trả sớm); có `total` nếu `_total` | không |
| S6 | Offset vượt tổng | `_offset >= số khớp` | 200 | như S1 | không `entry`, có `previous` | không |
| S7 | Kèm resource liên quan | `_include`/`_revinclude` hợp lệ | 200 | như S1 | thêm entry `search.mode=include` sau các `match` | không |
| S8 | Cắt phần tử | `_elements=a,b` | 200 | như S1 | resource chỉ còn `resourceType,id,meta,a,b`; `next` mất `_elements` | không |
| S9 | Tham số điều khiển không hỗ trợ | `_query`, `_contained`, `_containedType` | 400 | - | `tham số _query chưa được hỗ trợ` | không |
| S10 | Tham số lạ | tên không là search param của loại | 400 | - | `tham số search không hợp lệ: foo`; `Prefer: handling` KHÔNG đổi kết quả | không |
| S11 | Giá trị/modifier/sort/phân trang sai | xem mục "Tham số tìm kiếm và lỗi của chúng" và "Phân trang" | 400 | - | thông điệp nguyên văn ở các bảng đó | không |
| S12 | Chained/_has/terminology quá lớn | vượt 5000 hoặc sâu hơn 4 | 400 | - | `chained search/_has khớp quá nhiều resource ...`, `chained search lồng quá sâu`, `ValueSet có quá nhiều mã ...` | không |
| S13 | Loại resource lạ | `/fhir/R4/Foo` | 404 | - | `Không có route cho GET Foo` | không |
| S14 | Thiếu quyền loại | SMART thiếu `s`, hoặc policy không có loại | 403 | - | `Token thiếu quyền s (SMART) trên <T>` / `Chính sách truy cập không cho phép <T>` | không |
| S15 | Thiếu project | principal không có project (không phải super-admin) | 403 | - | `Không xác định được project của người gọi` | không |
| S16 | Không đăng nhập / sai quyền | `Guard` | 401 / 403 | `WWW-Authenticate: Bearer realm="fhir"` (401, token thiếu); `Bearer realm="fhir", error="invalid_token"` (token sai) | `Thiếu bearer token` / `Token không hợp lệ` / `Token không đủ quyền cho thao tác này` | không (có log cảnh báo cho token bị từ chối) |
| S17 | Quá hạn mức | vượt `FHIR_RATE_LIMIT` | 429 | `Retry-After: 1` | `quá nhiều yêu cầu, thử lại sau` (`code=invalid`) | đếm hạn mức |
| S18 | `Content-Type` lạ kèm thân | GET có thân `text/plain` | 415 | - | `Content-Type không hỗ trợ: text/plain` | không |
| S19 | Lỗi kho | DB hỏng, kết nối đứt | 500 | - | `OperationOutcome` `code=exception` `(chưa kiểm nguyên văn)` | không |

**Nhánh fail đóng:** tham số lạ, modifier lạ, `_compartment` sai dạng, `:missing` sai giá trị đều là `400` (không bao giờ hiểu thành "không lọc", đặc biệt `_compartment` vì dùng để giới hạn quyền); loại không có rule policy là `403`; điều kiện `_project` luôn được thêm và kết quả lọc lại lần hai.
**Idempotent:** chỉ đọc, gọi lặp cho cùng kết quả; không có `ETag`, `Last-Modified`, hay `Cache-Control` cho phép đệm (`no-store`).
**Điều kiện HTTP (`If-*`, `Prefer`):** không route nào của mảnh này đọc `If-Match`, `If-None-Match`, `If-Modified-Since`, `Prefer: return`, `Prefer: handling` cho tìm kiếm.

**Kết quả theo project và compartment:** người gọi chỉ thấy resource của project mình (`Meta.project` = claim `fhir_project` hoặc `X-Fhir-Project` của super-admin); `AccessPolicy` dạng `Observation?_compartment=Patient/p1` được đẩy xuống kho cho tìm theo loại (đã đo: trong policy đó `GET Observation` chỉ thấy Observation của `Patient/p1`, `total` đếm đúng một). Tham số `_compartment` do người gọi tự gửi chỉ THU HẸP thêm, không bao giờ mở rộng.

**Ví dụ lỗi tham số**

```http
GET /fhir/R4/Patient?foo=1 HTTP/1.1
Host: fhir.example.org
Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.e30.c2ln
Prefer: handling=lenient
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"invalid","details":{"text":"tham số search không hợp lệ: foo"},"diagnostics":"tham số search không hợp lệ: foo"}]}
```

**Ví dụ chỉ đếm**

```http
GET /fhir/R4/Patient?_summary=count HTTP/1.1
```

```http
HTTP/1.1 200 OK

{"resourceType":"Bundle","type":"searchset","timestamp":"2026-11-24T10:10:54Z","total":4,"link":[{"relation":"self","url":"https://fhir.example.org/fhir/R4/Patient?_count=0&_total=accurate"}]}
```

**Ví dụ kèm `_include`**

```http
GET /fhir/R4/Observation?_include=Observation:subject HTTP/1.1
```

```http
HTTP/1.1 200 OK

{"resourceType":"Bundle","type":"searchset","timestamp":"2026-11-24T10:10:54Z","link":[{"relation":"self","url":"https://fhir.example.org/fhir/R4/Observation?_count=20&_include=Observation%3Asubject"}],
 "entry":[{"fullUrl":"https://fhir.example.org/fhir/R4/Observation/fcfa3652-36d0-42e8-bfdc-f235cf6c990a","resource":{"resourceType":"Observation","id":"fcfa3652-36d0-42e8-bfdc-f235cf6c990a","status":"final","code":{"text":"hr"},"subject":{"reference":"Patient/p1"}},"search":{"mode":"match"}},
          {"fullUrl":"https://fhir.example.org/fhir/R4/Patient/p1","resource":{"resourceType":"Patient","id":"p1","name":[{"family":"Tran"}]},"search":{"mode":"include"}}]}
```

<a id="post-fhir-r4-resourcetype-search"></a>
## `POST /fhir/R4/{resourceType}/_search`

Cùng handler `searchType` với `GET {resourceType}`: tìm kiếm với tham số nằm trong thân `application/x-www-form-urlencoded` (để tránh URL dài). [Tham chiếu trường](../reference/fhir.md#post-fhir-r4-resourcetype-search)

**Tiền điều kiện / cờ bật route:** luôn bật; vai trò tối thiểu `read` (nhánh riêng `POST .../_search` trong `RequiredRole`, kể cả dấu `/` cuối).

**Thứ tự kiểm tra:** như `GET {resourceType}`, khác ở bước `readRequest`:
1. Middleware, hạn mức, `Guard` (vai trò `read`).
2. `readRequest`: thân rỗng -> không kiểm `Content-Type`, chạy như `GET` không tham số; `application/x-www-form-urlencoded` -> mỗi cặp form được CỘNG thêm vào `Query` (`Query.Add`), nên tham số trên URL và trong form cùng có hiệu lực (đã đo: `?gender=male&_count=1` + form `family=Tran` cho link `self` gồm cả ba) và là AND (không có "form thắng URL"); `Content-Type` khác (`text/plain`, `multipart/form-data`) -> `415`.
3. Phần còn lại (Parse, searchRepo, Repo.Search, Bundle) giống `GET`.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| P1 | Form hợp lệ | `Content-Type: application/x-www-form-urlencoded`, tham số hợp lệ | 200 | `Content-Type: application/fhir+json; charset=utf-8` | Bundle `searchset` như `GET`; `link` trỏ về `GET {rt}?...` (không có link POST) | không ghi gì |
| P2 | Form + query URL | cả hai chứa tham số | 200 | như P1 | điều kiện của cả hai cộng lại (AND/lặp) | không |
| P3 | Tham số lặp trong form | `_count=1&_count=2` | 200 | như P1 | giá trị CUỐI thắng | không |
| P4 | Thân rỗng | không thân, bất kỳ `Content-Type` | 200 | như P1 | tìm không điều kiện (trang đầu, `_count=20`) | không |
| P5 | Thân JSON | `Content-Type: application/json` | 200 | như P1 | thân JSON được giải mã rồi BỎ QUA: `{"family":"Tran"}` KHÔNG lọc (đã đo: trả mọi resource của project). Không có lỗi báo | không |
| P6 | Form hỏng | `%zz=%` | 400 | - | `Form không hợp lệ` | không |
| P7 | `Content-Type` không hỗ trợ | `text/plain`, `multipart/form-data` kèm thân | 415 | - | `Content-Type không hỗ trợ: text/plain` | không |
| P8 | Mọi lỗi tham số | như `GET` | 400 | - | cùng thông điệp của `GET` | không |
| P9 | Mọi lỗi quyền, project, hạn mức | như `GET` | 401/403/429 | như `GET` | như `GET` | như `GET` |

**Khác gì `GET`:** (1) tham số đến từ thân form (cộng với query URL); (2) thân JSON bị nhận nhưng không dùng (P5), nên khách gửi `application/json` tưởng đang lọc sẽ nhận toàn bộ dữ liệu (không vượt project/policy, nhưng sai ý khách); (3) `Prefer`, `_format` cũng bị bỏ qua như `GET`; (4) link phân trang luôn là `GET` với tham số trong URL, nên trang kế của một tìm kiếm POST lộ tham số lọc trên URL. Mọi thứ khác (giới hạn, `_count` tối đa 1000, quyền, kết quả) y hệt `GET`. Tìm hệ thống dạng POST (`POST /fhir/R4/_search`) không có route: `404` `Không có route cho POST _search` `(chưa kiểm nguyên văn)`.

**Ví dụ**

```http
POST /fhir/R4/Patient/_search HTTP/1.1
Host: fhir.example.org
Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.e30.c2ln
Content-Type: application/x-www-form-urlencoded

family=Tran&_count=5
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"Bundle","type":"searchset","timestamp":"2026-11-24T10:10:54Z","link":[{"relation":"self","url":"https://fhir.example.org/fhir/R4/Patient?_count=5&family=Tran"}],"entry":[{"fullUrl":"https://fhir.example.org/fhir/R4/Patient/p1","resource":{"resourceType":"Patient","id":"p1","name":[{"family":"Tran"}]},"search":{"mode":"match"}}]}
```

<a id="post-fhir-r4"></a>
## `POST /fhir/R4`

Batch hoặc transaction: nhận `Bundle` ở gốc FHIR, chạy từng entry bằng chính `Router.Handle` (cùng handler và cùng kiểm tra quyền như gọi trực tiếp) rồi trả Bundle `batch-response` hoặc `transaction-response`. [Tham chiếu trường](../reference/fhir.md#post-fhir-r4)

**Tiền điều kiện / cờ bật route:** luôn bật. Vai trò tối thiểu `write` cho MỌI Bundle (kể cả chỉ có `GET`): token `read` bị `403` `Token không đủ quyền cho thao tác này`. `FHIR_ASYNC_BATCH=true` mới có nhánh async. Transaction cần repository hỗ trợ `Transactional` (kho SQL và `MemoryRepository` có; `Repo` của `fhirauth` chuyển tiếp).

**Thứ tự kiểm tra** (theo `Router.batch` và `runTransaction`):
1. Middleware, hạn mức, `Guard` (vai trò `write`).
2. `readRequest`: JSON hỏng `400`; `Content-Type` lạ `415`; thân rỗng thì tới bước 3 với `Body` nil.
3. `Body` phải là object có `resourceType=Bundle`: nếu không `400` `Body phải là Bundle` (đã đo với thân rỗng, mảng `[1]`, Patient).
4. `type` phải là `batch` hoặc `transaction`: nếu không `400` `Bundle.type phải là batch hoặc transaction` (đã đo với `collection`).
5. Phân tích MỌI entry trước khi chạy (`parseEntry`), dừng ở entry hỏng đầu tiên: `entry[i] không hợp lệ` (không phải object), `entry[i] thiếu request`, `entry[i].request cần method và url`. Đọc `request.ifMatch`, `request.ifNoneExist`, `fullUrl`, `resource` (phải là object; mảng JSON Patch bị bỏ thành `nil`). `method` được viết hoa; `url` bỏ `/` đầu rồi tách `?` thành path và query.
6. Nhánh async: nếu `AsyncBatch` bật VÀ `Prefer` chứa `respond-async` (không phân biệt hoa thường, chứa chuỗi con): `transaction` -> `400` `Transaction không chạy async được (Prefer: respond-async chỉ dùng cho batch)`; `batch` -> lưu Bundle thành `Binary`, tạo `AsyncJob` (loại `batch`), `202` `Content-Location: <base>/job/<id>/status`. `AsyncBatch` tắt thì header BỊ BỎ QUA, chạy đồng bộ (đã đo: `200` Bundle `batch-response`).
7. **batch** đồng bộ: chạy entry theo thứ tự trong Bundle, tuần tự, độc lập; lỗi của entry thành `response` của entry đó; luôn `200`.
8. **transaction**: `RunInTransaction`; trong giao dịch: (a) mọi `POST` có `resource` được xét `ifNoneExist` (query lỗi `400` `ifNoneExist không hợp lệ`; nhiều khớp `412` `Nhiều resource khớp ifNoneExist`; đúng một khớp -> entry bị bỏ qua, trả `200 OK` kèm resource sẵn có; không khớp -> cấp sẵn `id` mới), (b) gán ánh xạ `fullUrl: urn:uuid:...` -> `Type/id`, (c) viết lại `reference` bằng ánh xạ, (d) kiểm `method`: chỉ `DELETE`, `POST`, `PUT`, `PATCH`, `GET` (khác -> `400` `method không hỗ trợ trong Bundle: HEAD`), (e) sắp xếp ổn định theo `DELETE` -> `POST` -> `PUT`/`PATCH` -> `GET` (giữ thứ tự gốc trong cùng nhóm) và chạy; entry lỗi đầu tiên hủy giao dịch (rollback) và thành phản hồi lỗi cả Bundle.
9. Sau khi commit: các sự kiện `fhir.resource.changed` của MỌI entry ghi được phát một lượt (trong transaction giữ lại tới commit; rollback thì không phát gì); batch phát ngay sau từng entry thành công.

### Chi tiết xử lý entry

| Chủ đề | Quy tắc (đã đo trừ khi ghi khác) |
|---|---|
| Header của entry | chỉ `request.ifMatch` -> `If-Match` và `request.ifNoneExist` -> `If-None-Exist` được chuyển; `ifNoneMatch`, `ifModifiedSince`, `Prefer` của entry BỊ BỎ QUA. `PATCH` luôn được gắn `Content-Type: application/json-patch+json` |
| Tham chiếu `urn:uuid` | chỉ trong transaction; chỉ `fullUrl` bắt đầu `urn:uuid:` của entry `POST` có `resource`; chỉ viết lại giá trị của khóa `reference` (không viết `fullUrl`, `url`, `Reference.reference` trong `contained` cũng được vì duyệt đệ quy). `reference` trỏ tới `urn:uuid` KHÔNG khai báo vẫn giữ nguyên chuỗi và entry vẫn `201` (không lỗi). `fullUrl` trùng nhau: cả hai được tạo, ánh xạ của entry SAU thắng. Trong batch `urn:uuid` KHÔNG được viết lại |
| `ifNoneExist` | transaction: mục 8a; batch: xử lý trong handler tạo (đã đo: `200 OK` kèm resource sẵn có, không tạo mới) |
| `PUT` có điều kiện, `DELETE` có điều kiện | `request.url` dạng `Patient?family=Tran` dùng handler có điều kiện của mảnh A (đã đo trong transaction: `200 OK` cập nhật `Patient/p1`) |
| `PATCH` | KHÔNG dùng được: handler cần thân là mảng JSON Patch nhưng entry chỉ nhận `resource` là object, nên luôn `400` `Body của PATCH phải là một mảng JSON Patch` (đã đo) |
| Thứ tự transaction | `DELETE p0` chạy TRƯỚC `PUT p0` dù đứng sau trong Bundle: kết quả `410` `Gone` với `expression` `Bundle.entry[1]` (đã đo) |
| Entry lồng Bundle | `url` rỗng không hợp lệ (`entry[0].request cần method và url`); không thể gửi Bundle lồng |
| `GET` trong Bundle | được phép; trả resource hoặc Bundle `searchset` trong `entry[i].resource` (đã đo `Patient?family=Tran&_summary=count`, `metadata`, `Patient/$validate`) |
| Kích thước | không giới hạn số entry; chỉ trần thân 16 MiB. Mỗi entry `POST/PUT` được kiểm (`fhirvalidate`) như gọi trực tiếp: một transaction có thể kéo dài với nhiều entry |
| `Prefer: return=...` | áp cho CẢ Bundle phản hồi (vì method là `POST`): `return=minimal` -> `200` thân TRỐNG (đã đo); `return=OperationOutcome` -> thân là `OperationOutcome` `OK` thay Bundle; không phải mức entry |
| `fullUrl` phản hồi | `<BaseURL>/<Type>/<id>`; entry có resource không `id` (vd `$validate`, `$reindex`, `metadata`) cho `fullUrl` dạng `<BaseURL>/OperationOutcome/` hoặc `/CapabilityStatement/` (kết thúc bằng `/`); Bundle lồng không có `fullUrl` |
| `response.location`, `etag`, `lastModified` | `location` (có `_history/<versionId>`) chỉ cho `201`; `etag` `W/"<versionId>"`; `lastModified` là `meta.lastUpdated` |

### Mã HTTP và trường hợp

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| B1 | Batch thành công (đồng bộ) | `type=batch`, không async | 200 | `Content-Type: application/fhir+json; charset=utf-8` | `batch-response`; mỗi entry có `response.status` riêng (kể cả lỗi) | mỗi entry ghi nhận như gọi riêng: version mới, lịch sử, chỉ mục, sự kiện `fhir.resource.changed` (Subscription) ngay sau entry; không rollback |
| B2 | Transaction thành công | `type=transaction` | 200 | như B1 | `transaction-response`, `entry[i]` cùng chỉ số với Bundle gốc (không theo thứ tự xử lý) | tất cả entry cùng một giao dịch DB; sự kiện phát sau commit |
| B3 | Bundle rỗng | `entry` thiếu hoặc `[]` | 200 | như B1 | `{"type":"batch-response"|"transaction-response","timestamp":...}` không có `entry` (đã đo) | không |
| B4 | Batch async được nhận | `FHIR_ASYNC_BATCH=true`, `Prefer: respond-async`, `type=batch`, entry phân tích được | 202 | `Content-Location: https://fhir.example.org/fhir/R4/job/<id>/status` | `OperationOutcome` `Accepted` | ghi `Binary` (Bundle vào) + `AsyncJob accepted` cùng project, đưa job vào hàng đợi (hoặc để `fhir-worker` nhận khi `DisableJobExecution`); `entry` chạy sau, nền, với quyền người gọi; kết quả là `Binary` chứa `batch-response`, `AsyncJob.output = Parameters{results: Reference(Binary/<id>)}`, giữ `FHIR_EXPORT_RETENTION` (mặc định 24 giờ) |
| B5 | Async nhưng là transaction | cờ bật, `type=transaction`, `respond-async` | 400 | - | `Transaction không chạy async được (Prefer: respond-async chỉ dùng cho batch)` | không tạo job |
| B6 | `respond-async` khi cờ tắt | `FHIR_ASYNC_BATCH=false` | 200 hoặc 4xx như đồng bộ | - | header bị bỏ qua (đã đo) | như B1/B2 |
| B7 | Bundle không hợp lệ cấp gốc | thân không phải Bundle, `type` khác `batch`/`transaction`, entry hỏng | 400 | - | `Body phải là Bundle` / `Bundle.type phải là batch hoặc transaction` / `entry[0] không hợp lệ` / `entry[0] thiếu request` / `entry[0].request cần method và url` | không (không entry nào chạy; async: không tạo job) |
| B8 | Transaction lỗi một entry | bất kỳ lỗi của handler | mã của entry (400, 403, 404, 410, 412, 422...) | - | `OperationOutcome` + `expression: Bundle.entry[i]` | rollback toàn bộ; không sự kiện; không `Binary`/`AsyncJob` |
| B9 | `method` không hợp lệ trong transaction | `HEAD`, `OPTIONS`... | 400 | - | `method không hỗ trợ trong Bundle: HEAD` (không có `expression` entry; xảy ra sau khi `ifNoneExist` đã chạy truy vấn đọc, chưa ghi gì) | không |
| B10 | `ifNoneExist` nhiều khớp | transaction, `POST` | 412 | - | `Nhiều resource khớp ifNoneExist` (không `expression`) | rollback |
| B11 | `ifNoneExist` khớp một | transaction hoặc batch | 200 trong entry (`"200 OK"`) | - | entry là resource sẵn có; `urn:uuid` của entry ánh xạ về resource đó | không ghi entry đó |
| B12 | Entry lỗi trong batch | handler của entry lỗi | 200 (cả Bundle) | - | `entry[i].response.status` `4xx/5xx` + `outcome` | các entry khác vẫn ghi |
| B13 | Bundle với thân JSON hỏng | | 400 | - | `JSON không hợp lệ: unexpected EOF` | không |
| B14 | `Content-Type` không hỗ trợ | `application/fhir+xml` kèm thân | 415 | - | `Content-Type không hỗ trợ: application/fhir+xml` | không |
| B15 | Token `read` | | 403 | - | `Token không đủ quyền cho thao tác này` | không |
| B16 | Repository không hỗ trợ transaction | kho không cài `Transactional` | 501 | - | `Repository không hỗ trợ transaction` (`code=not-supported`) | không |
| B17 | Thân vượt 16 MiB | | 413 | - | `Body quá lớn` | không |
| B18 | `Prefer: return=minimal` | bất kỳ kết quả `200`/`202` | giữ mã gốc | - | thân TRỐNG (Bundle bị bỏ) | như trường hợp gốc |
| B19 | Quyền theo loại của entry | SMART/policy chặn loại của entry | 200 (batch, entry `403`) hoặc 403 (transaction) | - | `Token thiếu quyền c (SMART) trên Observation` / `Chính sách truy cập không cho phép X` | transaction: rollback |
| B20 | Lỗi hạ tầng | DB lỗi giữa giao dịch | 500 | - | `OperationOutcome` `exception` `(chưa kiểm nguyên văn)` | rollback; async: job `error` |

**Async: vòng đời của job** (theo `batch_async.go`; test `batch_async_test.go`): `GET job/{id}/status` trả `202` (`AsyncJob` đang `accepted`), `200` (`completed`, `output.parameter[results]` trỏ `Binary` kết quả), lỗi trả `500` với `OperationOutcome` của job, hủy thì `410`; `DELETE job/{id}/status` hủy job đang chờ (`202` `Cancelled`) hoặc xóa job đã xong cùng `Binary` kết quả (`202` `Deleted`), vai trò `write`. Chi tiết ở [cases/operation.md](operation.md#get-fhir-r4-job-id-status). Ngữ nghĩa là "ít nhất một lần cho mỗi entry": bị gián đoạn thì chạy tiếp từ checkpoint (mỗi `FHIR_BATCH_CHECKPOINT_ENTRIES` entry, mặc định 10, hoặc 5 giây), entry sau checkpoint có thể chạy lại; `POST` không điều kiện dùng `id` cấp sẵn nên không tạo trùng. Transaction không bao giờ async.

**Nhánh fail đóng:** Bundle hỏng bị từ chối TRƯỚC khi chạy entry nào; transaction lỗi là rollback toàn bộ; quyền theo loại được kiểm từng entry; role KHÔNG fail đóng (xem rủi ro).
**Idempotent:** không (mỗi `POST` không điều kiện tạo mới); `ifNoneExist` và `PUT` có `id` làm entry idempotent theo ngữ nghĩa của từng thao tác.

**Ví dụ: batch với thành công, 404 và 400**

```http
POST /fhir/R4 HTTP/1.1
Host: fhir.example.org
Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.e30.c2ln
Content-Type: application/fhir+json

{"resourceType":"Bundle","type":"batch","entry":[
  {"request":{"method":"GET","url":"Patient/p1"}},
  {"request":{"method":"GET","url":"Patient/nope"}},
  {"resource":{"resourceType":"Patient","name":[{"family":"New"}]},"request":{"method":"POST","url":"Patient"}},
  {"request":{"method":"DELETE","url":"Patient/p3"}},
  {"request":{"method":"GET","url":"Patient?foo=1"}}]}
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"Bundle","type":"batch-response","timestamp":"2026-11-24T10:11:44Z","entry":[
 {"fullUrl":"https://fhir.example.org/fhir/R4/Patient/p1","resource":{"resourceType":"Patient","id":"p1","name":[{"family":"Tran"}]},"response":{"status":"200 OK","etag":"W/\"98b000dc-192c-45d9-8cb3-6ec2b76d5e19\"","lastModified":"2026-11-24T10:11:19.168Z"}},
 {"response":{"status":"404 Not Found","outcome":{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"not-found","details":{"text":"Not found"},"diagnostics":"Not found"}]}}},
 {"fullUrl":"https://fhir.example.org/fhir/R4/Patient/d422d68a-fa6a-4c31-b71d-f5ac287b75bf","resource":{"resourceType":"Patient","id":"d422d68a-fa6a-4c31-b71d-f5ac287b75bf","name":[{"family":"New"}]},"response":{"status":"201 Created","location":"https://fhir.example.org/fhir/R4/Patient/d422d68a-fa6a-4c31-b71d-f5ac287b75bf/_history/cba95969-d3cd-486b-869d-a311371e4eb6","etag":"W/\"cba95969-d3cd-486b-869d-a311371e4eb6\"","lastModified":"2026-11-24T10:11:19.168Z"}},
 {"response":{"status":"204 No Content"}},
 {"response":{"status":"400 Bad Request","outcome":{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"invalid","details":{"text":"tham số search không hợp lệ: foo"},"diagnostics":"tham số search không hợp lệ: foo"}]}}}]}
```

**Ví dụ: transaction với tham chiếu `urn:uuid`**

```http
POST /fhir/R4 HTTP/1.1
Content-Type: application/fhir+json

{"resourceType":"Bundle","type":"transaction","entry":[
 {"fullUrl":"urn:uuid:aaaaaaaa-0000-4000-8000-000000000001","resource":{"resourceType":"Patient","name":[{"family":"Tx"}]},"request":{"method":"POST","url":"Patient"}},
 {"resource":{"resourceType":"Observation","status":"final","code":{"text":"bp"},"subject":{"reference":"urn:uuid:aaaaaaaa-0000-4000-8000-000000000001"}},"request":{"method":"POST","url":"Observation"}}]}
```

```http
HTTP/1.1 200 OK

{"resourceType":"Bundle","type":"transaction-response","timestamp":"2026-11-24T10:11:19Z","entry":[
 {"fullUrl":"https://fhir.example.org/fhir/R4/Patient/d09e3ce0-7eeb-4fbd-abc5-2d0f46c75817","resource":{"resourceType":"Patient","id":"d09e3ce0-7eeb-4fbd-abc5-2d0f46c75817","name":[{"family":"Tx"}]},"response":{"status":"201 Created","location":"https://fhir.example.org/fhir/R4/Patient/d09e3ce0-7eeb-4fbd-abc5-2d0f46c75817/_history/5078eaae-d072-4f80-a66f-fed78dc498c5","etag":"W/\"5078eaae-d072-4f80-a66f-fed78dc498c5\"","lastModified":"2026-11-24T10:11:19.169Z"}},
 {"fullUrl":"https://fhir.example.org/fhir/R4/Observation/8bf68bf0-9a1b-409d-ad2d-486389cb37e0","resource":{"resourceType":"Observation","id":"8bf68bf0-9a1b-409d-ad2d-486389cb37e0","status":"final","code":{"text":"bp"},"subject":{"reference":"Patient/d09e3ce0-7eeb-4fbd-abc5-2d0f46c75817"}},"response":{"status":"201 Created","location":"https://fhir.example.org/fhir/R4/Observation/8bf68bf0-9a1b-409d-ad2d-486389cb37e0/_history/e0b5558b-0ebd-4ebc-aee7-1993c58857f5","etag":"W/\"e0b5558b-0ebd-4ebc-aee7-1993c58857f5\"","lastModified":"2026-11-24T10:11:19.169Z"}}]}
```

**Ví dụ: batch async được nhận** (khi `FHIR_ASYNC_BATCH=true`)

```http
POST /fhir/R4 HTTP/1.1
Content-Type: application/fhir+json
Prefer: respond-async

{"resourceType":"Bundle","type":"batch","entry":[{"request":{"method":"GET","url":"Patient/p1"}}]}
```

```http
HTTP/1.1 202 Accepted
Content-Type: application/fhir+json; charset=utf-8
Content-Location: https://fhir.example.org/fhir/R4/job/3c67a12f-ef35-4add-8a94-e31dd432eb58/status

{"resourceType":"OperationOutcome","issue":[{"severity":"information","code":"informational","details":{"text":"Accepted"},"diagnostics":"Accepted"}]}
```

<a id="post-fhir-r4-graphql"></a>
## `POST /fhir/R4/$graphql`

GraphQL chỉ đọc (tập con của FHIR GraphQL): đọc một resource, tìm kiếm dạng danh sách hoặc kết nối, đi theo tham chiếu. [Tham chiếu trường](../reference/fhir.md#post-fhir-r4-graphql)

**Tiền điều kiện / cờ bật route:** luôn bật (cùng handler đăng ký cho `GET` và `POST`). Vai trò tối thiểu `read` cho mọi method (nhánh `$graphql` của `RequiredRole`). Mọi đọc đi qua `Repo` của người gọi nên tách project, SMART và `AccessPolicy` áp như REST.

**Thứ tự kiểm tra:**
1. Middleware, hạn mức, `Guard` (vai trò `read`).
2. `readRequest`: `application/json`/`*+json`/không có `Content-Type` -> JSON; `application/graphql` -> thân là truy vấn; `application/x-www-form-urlencoded` -> gộp vào `Query` (nên `query=` bằng form cũng chạy, đã đo); `text/plain` -> `415` `Content-Type không hỗ trợ: text/plain`; JSON hỏng -> `400`.
3. `graphqlInput`: lấy `query`, `operationName`, `variables` từ thân (`map` hoặc chuỗi) rồi từ query URL (chỉ dùng khi thân không có `query`/`operationName`/biến). Thân JSON phải là object, không thì `400` `body phải là JSON {query, variables} hoặc application/graphql`; `variables` là chuỗi thì phải là JSON object (`400` `variables không phải JSON object`), kiểu khác `400` `variables phải là JSON object` (đã đo: mảng `[1]`); `query` rỗng `400` `thiếu query` (đã đo thân `{}`).
4. `parseGraphQL`: truy vấn dài hơn 64 KiB `400` `truy vấn quá dài (> 65536 byte)`; sai cú pháp `400` `lỗi cú pháp GraphQL tại <vị trí> (<gần>): <lý do>` (đã đo `tại 12 (hết đầu vào): cần tên tham số`); `mutation`/`subscription` `400` `lỗi cú pháp GraphQL tại 0 (mutation): mutation chưa được hỗ trợ (chỉ có query)` (đã đo); selection lồng quá 12 cấp `400` `lỗi cú pháp GraphQL tại 64 ({): selection lồng quá sâu (> 12)` (đã đo); không có query `không có thao tác query nào`; fragment trùng `fragment X khai báo hai lần`.
5. Chọn operation: `operationName` không tồn tại `400` `không có operation <tên>`; nhiều operation mà thiếu tên `400` `nhiều operation: cần operationName` (đã đo). Biến chưa truyền dùng giá trị mặc định; biến không khai báo bị bỏ qua.
6. Thực thi từng field gốc (`Type(id:)`, `TypeList(...)`, `TypeConnection(...)`) qua `Repo`; lỗi của từng field gom vào `errors[]`, KHÔNG đổi mã HTTP: luôn `200` (kể cả khi mọi field lỗi).

**Giới hạn chống lạm dụng:** truy vấn tối đa 64 KiB; độ sâu selection 12; tối đa **1000 resource** nạp cho một truy vấn (`truy vấn vượt giới hạn 1000 resource`, hoặc `truy vấn vượt giới hạn 1000 resource, kết quả bị cắt`); `TypeList`/`TypeConnection` mặc định 20, tối đa 100 (`_count` lớn hơn bị cắt im lặng); tham chiếu vòng của fragment bị chặn (`fragment X tham chiếu vòng`). Không có hạn mức riêng ngoài hạn mức IP.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| G1 | Đọc một resource | `{ Patient(id: "p1") { id } }` | 200 | `Content-Type: application/json; charset=utf-8` | `{"data":{"Patient":{"id":"p1"}}}` | không ghi gì |
| G2 | Danh sách | `PatientList(family: "Tran") { id name { family } }` | 200 | như G1 | `{"data":{"PatientList":[{...}]}}`; tham số search là tham số của field, gạch dưới đổi thành gạch ngang | không |
| G3 | Kết nối | `PatientConnection(_count: 2) { count offset pageSize edges { mode resource { ... on Patient { id } } } }` | 200 | như G1 | `count` là tổng chính xác (`_total=accurate`), `offset`, `pageSize`, `edges[]` với `mode:"match"` | không |
| G4 | Theo tham chiếu | `subject { reference resource { ... on Patient { id } } }` hoặc `Patient(id:..) { ObservationList(_reference: subject) { id } }` | 200 | như G1 | resource tham chiếu được nạp (tính vào ngân sách 1000) | không |
| G5 | Không tìm thấy một resource | `Patient(id: "nope")` | 200 | như G1 | `{"data":{"Patient":null}}`, KHÔNG có `errors` (404/410 bị nuốt im lặng) | không |
| G6 | Lỗi cấp field | field gốc lạ, tham số search lạ, `Type()` thiếu `id`, vượt ngân sách, đọc bị 403 | 200 | như G1 | `{"data":{"Foo":null},"errors":[{"message":"field gốc không hợp lệ: Foo (dùng Type, TypeList hoặc TypeConnection)","path":["Foo"]}]}` | không |
| G7 | Lỗi tham số search trong field | `PatientList(zzz: "a")` | 200 | như G1 | `errors[].message` `tham số search không hợp lệ: zzz`, `path:["PatientList"]`, field là `null` | không |
| G8 | Biến | `query Q($id: ID!) { Patient(id: $id) { id } }` + `variables` | 200 | như G1 | như G1 | không |
| G9 | `GET` với `?query=` | `GET /fhir/R4/$graphql?query=...` | 200 | như G1 | như G1 | không |
| G10 | Form | `application/x-www-form-urlencoded` có `query=` | 200 | như G1 | như G1 | không |
| G11 | Thân `application/graphql` | thân là chuỗi truy vấn | 200 | như G1 | như G1 | không |
| G12 | Truy vấn sai cú pháp | xem bước 4 | 400 | `Content-Type: application/fhir+json; charset=utf-8` | `OperationOutcome` `code=invalid` | không |
| G13 | Thiếu `query`, `variables` sai, nhiều operation, operation lạ | xem bước 3 và 5 | 400 | như G12 | thông điệp ở các bước trên | không |
| G14 | `Content-Type` lạ | `text/plain` | 415 | như G12 | `Content-Type không hỗ trợ: text/plain` | không |
| G15 | Method khác | `PUT`/`DELETE`/`PATCH` | 405 | như G12 | `PUT không được hỗ trợ cho $graphql` (đã đo) | không |
| G16 | Không đăng nhập / quyền / hạn mức | `Guard`, hạn mức | 401/403/429 | như `GET {rt}` | như `GET {rt}` | không |

**Quy ước phản hồi:** `data` luôn có (object, có thể `{}`); `errors` chỉ có khi có lỗi cấp field; field lỗi mang `null` trong `data`; alias trùng nhau ghi đè nhau (khóa cuối thắng); `path` là mảng tên field/chỉ số (có thể vắng với lỗi fragment). Không hỗ trợ: introspection (`__schema` trả lỗi `field gốc không hợp lệ: __schema ...` trong `errors` với `200`), directive (`@include`/`@skip` bị bỏ qua), tham số của field thường (`tham số của field <tên> chưa được hỗ trợ`), kiểm kiểu theo schema (trường không tồn tại trả `null`).

**Nhánh fail đóng:** mọi đọc đi qua `Repo` nên tài nguyên ngoài project, ngoài SMART hoặc ngoài policy không bao giờ vào `data` (đọc bị `403` thành `errors` kèm thông điệp quyền, hoặc `null`). **Idempotent:** chỉ đọc.

**Ví dụ thành công**

```http
POST /fhir/R4/$graphql HTTP/1.1
Host: fhir.example.org
Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.e30.c2ln
Content-Type: application/json

{"query":"{ PatientList(family: \"Tran\") { id name { family } } }"}
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"data":{"PatientList":[{"id":"p1","name":[{"family":"Tran"}]}]}}
```

**Ví dụ lỗi cấp field (vẫn 200)**

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"data":{"Foo":null,"Patient":null,"PatientList":null},"errors":[{"message":"field gốc không hợp lệ: Foo (dùng Type, TypeList hoặc TypeConnection)","path":["Foo"]},{"message":"tham số search không hợp lệ: zzz","path":["PatientList"]}]}
```

**Ví dụ lỗi cú pháp (400, `OperationOutcome`)**

```http
HTTP/1.1 400 Bad Request
Content-Type: application/fhir+json; charset=utf-8

{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"invalid","details":{"text":"lỗi cú pháp GraphQL tại 12 (hết đầu vào): cần tên tham số"},"diagnostics":"lỗi cú pháp GraphQL tại 12 (hết đầu vào): cần tên tham số"}]}
```

<a id="get-fhir-r4-metadata"></a>
## `GET /fhir/R4/metadata`

CapabilityStatement của máy chủ, công khai để client tự khám phá. [Tham chiếu trường](../reference/fhir.md#get-fhir-r4-metadata)

**Tiền điều kiện / cờ bật route:** luôn bật, không có cờ. Công khai: `Guard` cho qua đúng `GET /fhir/R4/metadata` (có `/` cuối cũng được) mà KHÔNG xét `Authorization` (kể cả token sai vẫn `200`; actor ghi là `anonymous`). Các method khác trên cùng đường dẫn cần xác thực như route thường.

**Thứ tự kiểm tra:**
1. Middleware chung và hạn mức theo IP (vẫn áp, đứng trước `Guard`): `429`/`403` như mục "Thứ tự xử lý chung".
2. `Guard`: `GET` đúng đường dẫn -> bỏ qua xác thực.
3. `Router.match`: method khác `GET` (đã qua xác thực) -> `405` `POST không được hỗ trợ cho metadata` (đã đo).
4. Handler `capabilities`: dựng `CapabilityStatement`; không đọc kho, không phụ thuộc người gọi, bỏ qua mọi query (`_format`, `mode`, `_summary`).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| M1 | Thành công | `GET`, có hoặc không token | 200 | `Content-Type: application/fhir+json; charset=utf-8` (không `ETag`, không `Last-Modified`; `Cache-Control: no-store`) | `CapabilityStatement` (xem dưới) | không ghi gì, không đọc DB |
| M2 | Quá hạn mức | vượt `FHIR_RATE_LIMIT` theo IP | 429 | `Retry-After: 1` | `quá nhiều yêu cầu, thử lại sau` | đếm hạn mức |
| M3 | Method khác | `POST` (có token đủ quyền) | 405 | - | `POST không được hỗ trợ cho metadata` | không |
| M4 | Method khác, không token | `POST`, `HEAD`, `PUT` không `Authorization` | 401 | `WWW-Authenticate: Bearer realm="fhir"` | `Thiếu bearer token` (JWT) / `Thiếu hoặc sai bearer token` (token tĩnh) | không (test `TestAuthenticateAndGuard`: `POST /fhir/R4/metadata` không token là 401; `HEAD` tương tự `(chưa kiểm)`) |

**Nội dung động hay tĩnh:** chỉ `date` là động (`time.Now()` UTC, `RFC3339`, mỗi lần gọi); phần `rest` được dựng MỘT LẦN mỗi tiến trình (`sync.Once`) từ danh sách loại biên dịch sẵn, nên không đổi theo cấu hình hay người gọi. Cố định: `status:"active"`, `kind:"instance"`, `experimental:false`, `fhirVersion:"4.0.1"`, `format:["application/fhir+json"]`, `patchFormat:["application/json-patch+json"]`, `software:{name:"trivita-fhir"}` (không có `version`), một `rest` với `mode:"server"` và `interaction` ở mức hệ thống `transaction`, `batch`, `search-system`. Mỗi loại trong 171 loại (kể cả `AccessPolicy`, `Project`, `User`...) có: `versioning:"versioned"`, `readHistory:true`, `updateCreate:false`, `conditionalCreate:true`, `conditionalUpdate:true`, `conditionalDelete:"single"`, 8 `interaction` (`read`, `vread`, `update`, `patch`, `delete`, `history-instance`, `create`, `search-type`) và `searchParam[]` sắp xếp theo `name`, không trùng, mỗi phần tử `{name, definition, type}` (ví dụ `Patient` có 36 phần tử đã đo; `_content`, `_id`, `_lastUpdated`, `_profile`, `_project`, `_query`, `_security`, `_source`, `_tag`... xuất hiện ở mọi loại). Không có `operation`, `searchInclude`, `searchRevInclude`, `security`, `implementation`.

**Vì sao KHÔNG phản ánh đúng khả năng thật (điểm lệch, mã thắng):** (1) `updateCreate` luôn `false` dù `FHIR_UPDATE_AS_CREATE=true` làm `PUT Type/id` tạo mới; (2) tham số `composite` và `special` bị loại khỏi `searchParam` với ghi chú "chưa hỗ trợ", nhưng `composite` ĐÃ được phân tích và thực thi ở `fhirsearch`/`fhirstore` (mục Tham số tìm kiếm); (3) mọi loại đều khai đủ 8 interaction dù quyền thật tùy token/policy; (4) không liệt kê operation ($everything, $export, $graphql...); (5) không khai `_include`/`_revinclude`, `_has`, chained.

**Ví dụ**

```http
GET /fhir/R4/metadata HTTP/1.1
Host: fhir.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/fhir+json; charset=utf-8
Cache-Control: no-store, no-cache, must-revalidate
X-Request-Id: UYNIkXbEzgxWmquwHybegPKMtfuOxcxO

{"resourceType":"CapabilityStatement","status":"active","experimental":false,"date":"2026-11-24T10:11:19Z","kind":"instance","software":{"name":"trivita-fhir"},"fhirVersion":"4.0.1","format":["application/fhir+json"],"patchFormat":["application/json-patch+json"],
 "rest":[{"mode":"server","interaction":[{"code":"transaction"},{"code":"batch"},{"code":"search-system"}],
  "resource":[{"type":"AccessPolicy","interaction":[{"code":"read"},{"code":"vread"},{"code":"update"},{"code":"patch"},{"code":"delete"},{"code":"history-instance"},{"code":"create"},{"code":"search-type"}],"versioning":"versioned","readHistory":true,"updateCreate":false,"conditionalCreate":true,"conditionalUpdate":true,"conditionalDelete":"single",
   "searchParam":[{"name":"_content","definition":"http://hl7.org/fhir/SearchParameter/Resource-content","type":"string"},{"name":"_id","definition":"http://hl7.org/fhir/SearchParameter/Resource-id","type":"token"}]}]}]}
```

## Phát hiện lệch với common.md và tài liệu cũ

### Ghi và đọc resource

Không sửa các tệp đó; đây là điều đúng theo mã và đo thật.

| Chỗ | Tài liệu nói | Thực tế |
|---|---|---|
| `common.md` mục 4 (vai trò) | `GET`/`HEAD`/`OPTIONS` là `read` | `HEAD` qua Guard rồi router trả `405` `HEAD không được hỗ trợ cho ...` (đã đo): không route nào phục vụ `HEAD` |
| `swagger.json` `GET .../_history` | khai `_count`, `_offset` | mã bỏ qua mọi query, trả toàn bộ lịch sử không phân trang (đã đo) |
| `swagger.json` `GET /{rt}/{id}` | (không khai `_summary`/`_elements`, đúng) | tài liệu cũ `fhir-rest.md` (tham chiếu Medplum) liệt kê chúng cho read; mã **bỏ qua** (đã đo) |
| `api-contract.md`/`fhir-rest.md` | `PATCH` nhận `application/json-patch+json` và FHIRPath Patch (Medplum) | chỉ JSON Patch; FHIRPath Patch ra `415` (`application/fhir+json`) hoặc `400` (đã đo) |
| `api-contract.md` mục 3 | "Phản hồi tạo/cập nhật kèm `Location` và `ETag`" | `Location` **chỉ** ở `201`; `PUT`/`PATCH` `200` không có `Location` (đã đo) |
| tài liệu cũ | `ETag` có thể do tăng số | `versionId` là UUID v4, mới ở mỗi lần ghi kể cả nội dung y hệt |
| `swagger.json` `DELETE` | liệt kê `204`, `401`, `403`, `404` | đúng, nhưng còn `410` được router đổi thành `204`; `If-Match` bị bỏ qua |
| `swagger.json` `PUT /{rt}` | `200`, `201`, `400`, `401`, `403`, `412` | đúng; mô tả "Update/patch/delete có điều kiện" nhưng `PATCH`/`DELETE` có điều kiện không có trong swagger |
| `common.md` mục 3 (thông điệp 413) | `413` `Body quá lớn` (`code=invalid`) | hai mã `issue.code`: `invalid` (khai `Content-Length`, Echo) và `too-costly` (đọc thân vượt, router); đã đo cả hai |

### Tìm kiếm, batch, GraphQL, metadata

Chưa thấy lệch nghiêm trọng; ba điểm cần bổ sung vào common.md:

1. Mục 4 ghi `write` cho batch/transaction và `admin` cho `$reindex`/`$expunge`. Trước 2026-11-24 hai quyền này không độc lập vì `RequiredRole` chỉ xét đường dẫn ngoài; nay entry được kiểm vai trò riêng (xem mục batch và rủi ro đã sửa).
2. Mục 6 liệt kê `Content-Type` nhận vào nhưng không nói rằng thân rỗng hoặc chỉ khoảng trắng được nhận với MỌI `Content-Type` (không `415`), và không nói `Prefer: return=minimal`/`OperationOutcome` áp cho cả Bundle batch (thân trống hoặc đổi thành OperationOutcome).
3. Mục 3 viết `404` là `Không có route cho <METHOD> <path>`; với tên loại lạ do router FHIR phát hiện (`/fhir/R4/Foo`), `<path>` là đường dẫn đã bỏ tiền tố (`Không có route cho GET Foo`), không phải `/fhir/R4/Foo`; chỉ lỗi 404 do Echo mới mang đường dẫn đầy đủ.

Tài liệu cũ lệch so với mã (đã đối chiếu, mã thắng): [../fhir-rest.md](../fhir-rest.md) nêu `Prefer`, `If-Match`, `If-None-Exist` "hỗ trợ mức router" nhưng `Prefer: handling` không có; hàng `POST /$ai`, `Bot/$execute`, `Agent/$push` trong bảng đó không tồn tại trong `pkg/fhirrouter` (nằm ngoài phạm vi, ghi chú M1-M5); phân trang "`link[rel=next]`" đúng nhưng không có `_cursor`; `GET /$versions` và `/.well-known/smart-configuration` được liệt kê là công khai nhưng không có route (`Guard` chỉ miễn `metadata`).

## Rủi ro và điểm cần lưu ý (hiện thực tế, không sửa)

### Ghi và đọc resource

1. **[ĐÃ SỬA 2026-11-24] Nâng quyền `write` thành `admin` qua `AsyncJob` ở chế độ token tĩnh (đã đo).** Nay ghi trực tiếp `AsyncJob` (và `$import`, `$install`) cần `admin` ở cổng HTTP và ở entry của Bundle (`TestStaticTokenCannotRunAdminOnlyOperations`); mô tả dưới là hiện trạng trước khi sửa. Token tĩnh `write` bị chặn `403` khi gọi `POST /Patient/$reindex` (cần `admin`), nhưng `POST /fhir/R4/AsyncJob` thân `{"status":"accepted","requestTime":"2020-01-01T00:00:00Z","request":"reindex?resourceType=Patient"}` được `201`; vòng bảo trì (`RecoverJobs`, mặc định mỗi 30 giây với `FHIR_JOB_MAINTENANCE_INTERVAL`) nhặt job `accepted` đủ cũ và **chạy** nó (job chuyển `completed`) mà không kiểm chữ ký quyền vì `JobOpen` chỉ có khi bật JWT. Tác động giới hạn ở các loại job đã đăng ký (`reindex`, `export`, `batch`); `reindex` là thao tác admin. Chế độ JWT **an toàn**: job thiếu chữ ký bị từ chối (`job thiếu hoặc sai chữ ký quyền`, log `job lỗi`). Chế độ token tĩnh là chế độ dev/một tenant theo tài liệu, nhưng ranh giới vai trò `write`/`admin` không được thi hành ở nhánh này.
2. **Người chỉ có SMART `system/Patient.cruds` vẫn tạo được `AsyncJob` (đã đo, JWT)**: `AsyncJob` được miễn kiểm SMART và chính sách. Job giả bị từ chối ở bước chạy nên không nâng quyền, nhưng vẫn là ghi dữ liệu ngoài phạm vi quyền (và nhiễu `AsyncJob` của project).
3. **Mất dữ liệu ẩn khi `PUT`/`PATCH` của người có `hiddenFields` lồng (đã đo bằng `PUT`)**: `name.family` bị xóa khỏi bản lưu vì chỉ trường cấp cao nhất được khôi phục. Người có quyền cắt trường ẩn có thể vô tình xóa dữ liệu họ không thấy.
4. **Kiểm thân trước kiểm quyền**: người không có quyền với loại vẫn nhận mô tả lỗi cấu trúc (400) chi tiết của loại đó (mức rò rỉ thấp: chỉ cấu trúc resource chuẩn).
5. **`410` xuyên project** (tombstone không so project): lộ việc một id (UUID) từng tồn tại ở project khác; đã được ghi nhận trong mã.
6. **Không kiểm binding từ vựng** (`gender":"robot"` lưu được): dữ liệu sai miền giá trị vào kho.
7. **Lịch sử không phân trang**: `GET .../_history` của resource có nhiều phiên bản trả nguyên Bundle không trần (rủi ro bộ nhớ và độ trễ).
8. **`PATCH` đọc-rồi-ghi không khóa**: lost update khi không gửi `If-Match` (theo mã, chưa đo).
9. **`Project`, `AccessPolicy`... ghi được bằng `fhir.write` thường** (đã đo `Project` `201`): hiện chỉ là dữ liệu, nhưng nếu sau này có thành phần đọc các loại này để phân quyền thì cần chặn ghi theo loại.

### Tìm kiếm, batch, GraphQL, metadata

Ghi trung thực "hiện thực tế"; không sửa mã.

1. **[ĐÃ SỬA 2026-11-24] Nâng quyền `write` -> `admin` qua batch/transaction.** Trước đó `Guard` chỉ xét `RequiredRole` của yêu cầu ngoài (`POST /fhir/R4` cần `write`) nên entry `POST Patient/$reindex` (batch `202` tạo `AsyncJob`; transaction `200`, và còn làm treo kho vì job chờ khóa của transaction) và `POST Patient/{id}/$expunge` (chỉ bị chặn bởi quyền SMART `d`, không kiểm vai trò) chạy với token `write` (đã tái hiện bằng test tạm, JWT, `Role=write`). Nay `Options.AuthorizeEntry` kiểm từng entry khi nhận Bundle (mục batch ở trên); các biến thể đường dẫn (`/Patient/$reindex`, dấu `/` cuối) bị chặn như nhau; bản sửa có test e2e và đột biến. Điểm còn mở liên quan: `$import`/`$install` ở chế độ token tĩnh không đòi admin; ghi `AsyncJob` thô ở chế độ token tĩnh (mục 1 phía trên).
2. **`hiddenFields` bị dò ngược bằng search/sort (đã tái hiện).** Với `AccessPolicy` `{"resourceType":"Patient","hiddenFields":["birthDate"]}`, trả về đã gỡ `birthDate`, nhưng người gọi vẫn lọc và sắp xếp theo trường đó: `GET Patient?birthdate=1981-01-01` chỉ trả `p1`; `GET Patient?_sort=-birthdate` trả `p2` trước `p1`. Kẻ gọi suy ra giá trị trường bị ẩn bằng nhị phân hóa điều kiện. `Repo.Search` không loại điều kiện/khóa sắp xếp trên trường ẩn.
3. **Tổng số và phân trang của tìm kiếm hệ thống không theo tiêu chí policy (đã tái hiện).** Với policy `Observation?_compartment=Patient/p1` và `Patient` đọc được cả hai bệnh nhân, `GET /fhir/R4/?_type=Observation,Patient&_total=accurate` trả `total: 4` trong khi người gọi chỉ thấy 3 resource (Observation của `p2` bị lọc sau khi kho đã đếm). Với tìm theo loại thì `total` đúng (`1`). Nguyên nhân: `Repo.Search` chỉ đẩy `pol.filters/exprs` xuống kho khi `req.ResourceType != ""`; nhánh hệ thống chỉ giao tập loại. Hậu quả: lộ số lượng resource ngoài tiêu chí, và trang có thể ngắn hơn `_count` dù còn `next`.
4. **`POST _search` nhận JSON nhưng bỏ qua (đã tái hiện)**: không phải lỗ hổng truy cập (vẫn trong project/policy) nhưng khách gửi lọc bằng JSON nhận toàn bộ dữ liệu mà không báo lỗi; nên trả `415` hoặc `400`.
5. **`_revinclude` và `_include` không bị chặn bởi `_count`/ngân sách**: một trang tối đa 1000 `match` có thể kèm số resource `include` không giới hạn (chỉ chặn bởi 16 MiB thân yêu cầu, không chặn thân phản hồi) `(chưa kiểm tải)`.
