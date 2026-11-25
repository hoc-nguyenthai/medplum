# Bản kiểm kê tương đương module `fhir`: Medplum v5.2.18 so với bản Go (`trivita-fhir`)

Trạng thái: **bản kiểm kê, chưa phải kế hoạch chi tiết.** Ngày viết: 2026-11-21. Nguồn Medplum: `~/Downloads/medplum/packages/server/src/fhir` (chỉ đọc) và `packages/fhir-router/src` cho phần router dùng chung. Mọi `file:dòng` của Medplum là đường dẫn tương đối so với `packages/server/src/fhir/` trừ khi ghi khác; mọi đường dẫn Go là tương đối so với gốc repo. Cột "Giá trị" và "Cỡ" là **phán đoán của người kiểm kê**, không phải số đo.

## 1. Phương pháp và giới hạn

### 1.1 Đã đọc những gì

- Bối cảnh bắt buộc: `docs/plans/modules-survey.md` (mục 2 `fhir`, mục 7), `docs/plans/coverage-matrix.md`, `apps/fhir-server/docs/api-contract/fhir-operations.md`, `fhir-rest.md`, `fhir-server-go.md`, `docs/BASELINE.md`, `docs/go-port-status.md`, `docs/go-port-checklist.md`, `README.md` gốc, `apps/fhir-server/README.md`, `architecture_test.go`.
- Medplum: toàn bộ `routes.ts` (575 dòng, từ dòng 60), `packages/fhir-router/src/fhirrouter.ts` (dòng 60-340), `bulkdata.ts`, `job.ts`, `binary.ts`, `response.ts`, `references.ts`, `resource-cap.ts`, `structure.ts`, `searchunique.ts`, `presence.ts`; một phần có chọn lọc của `repo.ts` (3484 dòng: `createResource`, `updateResourceImpl`, `deleteResource`, `removeHiddenFields`, `logEvent`, các điểm vào), `search.ts` (danh sách hàm và các `case`), `accesspolicy.ts`, `smart.ts` (dòng 28-62, 196-335), `precommit.ts`, `fhirquota.ts`, `rewrite.ts` (đầu file), `sharding.ts` (đầu file), `repository/validation.ts`, `operations/export.ts`, `expunge.ts`, `custom.ts`, `claimsubmit.ts`, `launch.ts`, `rotatesecret.ts`, `refresh-reference-display.ts`, `patienteverything.ts` (đầu file), `packages/fhir-router/src/batch.ts` (đoạn xử lý PATCH và `isTransaction`), `async-batch.ts`, `batch/checkpoint-store.ts` (đầu file), `packages/core/src/search/search.ts` (danh sách modifier).
- **Không đọc thân hàm** của đa số file trong `operations/`: với chúng tôi chỉ đọc danh sách `export`, dòng handler, chú thích đầu hàm, danh sách `import` (để suy ra phụ thuộc) và số dòng. Ghi chú "chưa đọc thân hàm" nghĩa là mô tả hành vi chỉ dựa vào tên, chú thích và import.
- Go: `pkg/fhirrouter` (toàn bộ file đã liệt kê bằng `grep -n "^func"`, đọc `router.go`, `handlers.go`, `http.go`, `operation.go`, `batch.go`, `jobs.go` (`jobStatus`), `bulk.go` (đoạn đăng ký và trạng thái), `capability.go`, `patch.go` (đầu file)), `pkg/fhirauth/grants.go`, `policy.go` (đầu file), `scope.go` (hàm `stamp`), `pkg/fhirsearch/request.go` (phần `apply`, `parseFilter`), `pkg/fhirvalidate/validate.go` (đầu file), `apps/fhir-server/internal/presentation/httpapi/httpapi.go`, `apps/fhir-server/internal/bootstrap/container.go` (đoạn đăng ký `$get-ws-binding-token`).
- Số liệu Medplum do tôi đếm lại: `fhir/` có **142 file không tính test** (không tính `__test__/`, không tính `cms1500.png.ts` là dữ liệu ảnh; 143 nếu tính), **37419 dòng** (khảo sát ghi 37352; chênh nhẹ, không ảnh hưởng kết luận). `operations/` có 75 file `.ts` không tính test, cộng thư mục `operations/utils/` 18 file (9353 dòng, không tính test; trong đó `scheduling.ts` 1088, `cms1500pdf.ts` 462, `cms-patient-match.ts` 425).

### 1.2 Kiểm Go bằng cách nào

Mọi trạng thái "CÓ" đều dựa vào một trong ba thứ: (a) mã đọc được ở đường dẫn ghi trong cột bằng chứng, (b) test cụ thể (tên test, file:dòng), (c) kết quả chạy thử (probe) ghi ở mục 1.3. Không kết luận "CÓ" khi không tìm thấy mã hay test. Lệnh đã dùng:

```
grep -rn 'Add("' pkg apps --include='*.go'                              # danh sách route đã đăng ký
grep -rn "smart-configuration|well-known|TerminologyCapabilities|\$versions" apps/fhir-server pkg/fhirrouter pkg/fhirauth   # 0 kết quả
grep -rniE "ratelimit|rate limit|429|quota|resource.?cap" pkg/fhir* apps/fhir-server    # không có cơ chế giới hạn (chỉ 429 trong retry của webhook)
grep -rn "httpsec" apps/fhir-server --include='*.go'                    # 0 kết quả: chưa gắn header bảo mật/CORS
grep -rniE "onBehalfOf|AuditEvent" pkg apps/fhir-server --include='*.go'   # chỉ trong kiểu sinh sẵn của pkg/fhir
grep -rn "of-type|OperationDefinition|_summary|_cursor|_pretty|_account" pkg/fhirrouter pkg/fhirsearch pkg/fhirstore
go build ./...                                                          # sạch
go test ./pkg/fhirrouter/... ./pkg/fhirsearch/... ./pkg/fhirauth/... ./pkg/fhirvalidate/... ./pkg/fhirsub/... ./pkg/fhirstore/... .   # tất cả ok (gồm architecture_test.go ở gốc, package trivita-fhir)
```

Bộ test `pkg/fhirstore` chạy trên SQLite; phần PostgreSQL chỉ chạy khi có `FHIRSTORE_TEST_POSTGRES_URL` nên **không chạy ở đợt này** (theo `docs/go-port-status.md` giai đoạn 24a đã chạy thật một lần trước đó, tôi không kiểm lại).

### 1.3 Probe chạy thử (không sửa repo)

Để kiểm các nghi vấn về hành vi, tôi biên dịch một chương trình nhỏ bằng `go run -overlay` (tệp nằm ở thư mục tạm của phiên làm việc, `go` ghép nó vào module ảo; không tệp nào trong repo bị tạo hay sửa), gọi `fhirrouter.New(fhirrouter.NewMemoryRepository(), ...).HTTPHandler()` qua `httptest`. Kết quả (MemoryRepository, không xác thực; phần router dùng chung với bản SQL nên hành vi tầng router giống nhau; riêng hành vi của kho SQL **chưa kiểm bằng probe**):

| Probe | Kết quả quan sát |
|---|---|
| `PUT /Patient/{id}` hai lần với nội dung y hệt bản hiện tại | cả hai trả 200; `GET /Patient/{id}/_history` sau đó có **3 phiên bản** (create + 2 lần không đổi) |
| `GET /Patient/{id}/_history?_count=1` | trả cả 3 entry, `total` 3 (bỏ qua `_count`) |
| `DELETE /Patient/{id}` với `If-Match: W/"zzz"` (sai) | **204** (xóa thành công, bỏ qua `If-Match`) |
| `GET /Patient?identifier:of-type=t\|MR\|v` trên Patient có đúng identifier đó | 200, **bundle rỗng** (đáng lẽ khớp 1); `:of-type=t\|XX\|v` cũng rỗng |
| `GET /Patient?identifier:present=true` | 400 `modifier không hợp lệ: present` |
| `GET /Patient?_summary=true` và `_summary=text` | 200, vẫn trả nguyên resource (bỏ qua) |
| `GET /Patient?_cursor=abc`, `?_total=estimate` | `_cursor` bị bỏ qua; `_total=estimate` trả tổng đúng |
| `GET /Patient?_deleted=true`, `?_compartment=Patient/1`, `?_list=x`, `?_fields=id` | cả bốn trả 400 `tham số ... không hợp lệ` |
| `PATCH` với `Content-Type: application/fhir+json` và body `Parameters` (FHIRPath Patch) | 415 `PATCH cần Content-Type application/json-patch+json` |
| `POST /` Bundle batch có entry `PATCH` mang `Binary` base64 (dạng chuẩn FHIR) | 200, entry trả `400 Bad Request` |
| `POST /` batch với `Prefer: respond-async` | 200 đồng bộ (bỏ qua header) |
| `POST /$export`, `POST /Patient` với `Content-Type: application/pdf` | 405 và 415 |
| `GET /$versions`, `GET /_history`, `GET /Patient/_history` | 404 (không có route) |
| `POST /Patient/$validate` với `active: "x"` | 400 (không phải 200 như ví dụ trong `apps/fhir-server/docs/api-contract/fhir-server-go.md`) |

### 1.4 Quy ước cột

- **Go**: `CÓ` (có mã và test, hành vi tương đương trong phạm vi đã so), `MỘT PHẦN` (có nhưng thiếu so với Medplum, ghi rõ thiếu gì), `CHƯA`, `KHÔNG ÁP DỤNG` (gắn với hạ tầng Medplum hoặc Medplum cũng không có). Một vài mục `CÓ` ghi thêm "chưa so từng ca": nghĩa là tôi tìm thấy mã và test nhưng không đối chiếu từng trường hợp biên với Medplum.
- **Cỡ** chỉ ghi cho `MỘT PHẦN` và `CHƯA` và là cỡ của **phần còn thiếu**: S (dưới khoảng 150 dòng nguồn TS, không cần thành phần nền mới), M (khoảng 150 đến 600 dòng hoặc cần thiết kế riêng), L (trên 600 dòng, hoặc cần thành phần nền mới chưa có, hoặc nhiều operation chia sẻ hạ tầng). `—` với `CÓ` và `KHÔNG ÁP DỤNG`. Cỡ port Go thường nhỏ hơn số dòng TS; số dòng chỉ là thước đo thô.
- **Phụ thuộc**: Bot (runtime Bot, quyết định M1/H3), Agent (module Agent, M3), AI (nhà cung cấp AI, M4), Storage (`pkg/binarystore`, M5), Redis, auth (app `auth`), ngoài (dịch vụ bên ngoài). `—` là không có phụ thuộc đáng kể.
- **Giá trị**: cao/vừa/thấp, **phán đoán** theo mức cần thiết để một hệ thống y tế dùng được hoặc để client Medplum chạy; chưa có yêu cầu sản phẩm làm căn cứ, xem câu hỏi mở ở mục 6.

## 2. Bảng kiểm kê

Mỗi dòng một tính năng hoặc operation. Cột: ID, tên và vị trí Medplum, trạng thái Go, bằng chứng Go, phụ thuộc, giá trị (phán đoán), cỡ phần còn thiếu. Bảng chia theo nhóm; số đếm ở mục 3 tính từ chính các dòng này.

### Nhóm R. Giao diện REST, định dạng, tiện ích HTTP

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| R-01 | `GET /metadata` CapabilityStatement, có `security`, danh sách `operation` (`routes.ts:171`, `metadata.ts:194-330`) | MỘT PHẦN | `pkg/fhirrouter/capability.go:26-74`: bỏ composite/special dù composite đã cài (dòng 47-49), `UpdateCreate` cố định false (dòng 31), không có `security`, không liệt kê operation; test `repotest/suite.go:249` | — | vừa | S |
| R-02 | `metadata?mode=terminology` TerminologyCapabilities (`routes.ts:172`, `terminology-capabilities.ts:1-97`) | CHƯA | grep `TerminologyCapabilities` trong `pkg/fhirrouter`, `apps/fhir-server`: 0 kết quả | — | thấp | S |
| R-03 | `GET /$versions` (`routes.ts:177`) | CHƯA | grep `$versions`: 0 route; probe 404 | — | thấp | S |
| R-04 | `/.well-known/smart-configuration`, `smart-styles.json` (`routes.ts:184-185`, `smart.ts:62,120`) | CHƯA | grep `smart-configuration` và `well-known` trong `apps/fhir-server`, `pkg/fhir*`: 0; `coverage-matrix.md` xếp `wellknown` vào `auth` P3 | auth P3 | vừa | S |
| R-05 | create `POST /Type`, `If-None-Exist`, `Location`, `ETag`, `Last-Modified` (`fhirrouter.ts:161-208`, `response.ts:23-37`) | CÓ | `pkg/fhirrouter/handlers.go:150-216`; `repotest/suite.go:133,409`. Lệch nhỏ: `Location` của Go có `/_history/{vid}` (`handlers.go:30`), Medplum không có (`response.ts:32-34`) | — | cao | — |
| R-06 | read, vread (`fhirrouter.ts:210-232`, `repo.ts:686,998`); đã xóa trả 410 | CÓ | `handlers.go:264-289`; `suite.go:133,171-175`. Go thêm conditional read `If-None-Match`/`If-Modified-Since` (`handlers.go:237-262`, `suite.go:1223`); Medplum khai `conditionalRead: not-supported` (`metadata.ts:114`) | — | cao | — |
| R-07 | update `PUT /Type/id`, `If-Match` cho 412 (`fhirrouter.ts:233-258`, `repo.ts:1047,1103-1160`) | CÓ | `handlers.go:290-342`; `suite.go:133`, `testConcurrentIfMatch` `suite.go:1827`. Lệch: update không đổi vẫn tạo phiên bản mới (xem mục 3.2, Lệch-1) | — | cao | — |
| R-08 | delete `DELETE /Type/id` trả 200 và hỗ trợ `If-Match` 412, đã xóa thì idempotent (`fhirrouter.ts:277-285`, `repo.ts:1618-1700`) | MỘT PHẦN | `handlers.go:372-381`: trả 204, không đọc `If-Match` (probe: `If-Match` sai vẫn 204); idempotent trên 410 có | — | vừa | S |
| R-09 | Client chọn id (update-as-create / `assignedId`) (`repo.ts:634-650,2456-2458`: chỉ super admin) | CÓ | `handlers.go:305-329`, tùy chọn `FHIR_UPDATE_AS_CREATE` mặc định tắt; `suite.go:1922`; cơ chế khác Medplum (Go cho mọi người gọi có quyền ghi khi bật cờ, Medplum chỉ super admin) | — | thấp | — |
| R-10 | `Prefer: return=minimal` (`routes.ts:115-127`) | CÓ | `pkg/fhirrouter/http.go:106-133` (còn hỗ trợ `return=OperationOutcome`); `suite.go:237` | — | vừa | — |
| R-11 | `_pretty=true` (`routes.ts:129`) | CHƯA | `pkg/fhirsearch/request.go:95` chỉ liệt kê `_pretty` vào danh sách tham số điều khiển bị bỏ qua | — | thấp | S |
| R-12 | `?_account=` khi create để gán `meta.account` (`fhirrouter.ts:167-171`) | CHƯA | grep `_account` trong `pkg/fhirrouter`: 0 | — | thấp | S |
| R-13 | Đọc Binary theo `Accept` trả nội dung thô (`response.ts:67-80`) | CÓ | `handlers.go:274-278`, `bulk.go:357` (`binaryRaw`, lấy từ base64 nhúng); không stream | — | vừa | — |
| R-14 | Ghi Binary thô `POST/PUT /fhir/R4/Binary` mọi content-type, `_filename`, `X-Security-Context`, gzip/deflate (`binary.ts:19-161`, `app.ts:228-234`) | CHƯA | `http.go:78-79` trả 415 cho Content-Type ngoài JSON, form, graphql; probe `application/pdf` -> 415; không có route Binary riêng | Storage | cao | M |
| R-15 | Binary lưu ngoài CSDL, `Binary.url` ký sẵn, giới hạn `base64BinaryMaxBytes` (`repo.ts:1229-1272`) | CHƯA | Binary giữ base64 trong CSDL (`modules-survey.md` mục 1; `go-port-status.md` 8a: file export cũng ghi vào bảng resource) | Storage (M5) | cao | L |
| R-16 | Viết lại URL tệp đính kèm: về dạng `Binary/id` khi ghi, về URL ký khi trả (`rewrite.ts:41-249`, `repo.ts:1139`, `response.ts:96`) | CHƯA | grep `rewriteAttachments`, `presigned` trong Go: 0 | Storage | vừa | M |
| R-17 | `GET /Type/$csv?_fields=` xuất CSV (`routes.ts:192`, `operations/csv.ts:31-176`) | CHƯA | grep `$csv` trong Go: 0 | — | vừa | S |
| R-18 | `POST /$graphql`: schema đầy đủ, introspection tùy cấu hình, **mutation** `TypeCreate/Update/Patch/Delete` (`fhir-router/src/graphql/graphql.ts:127-131,234-252`) | MỘT PHẦN | `pkg/fhirrouter/graphql.go:45-522`, `gqlparse.go`; `suite.go:1080`. Chỉ đọc; không introspection, không validate theo schema, không mutation, không `@include/@skip` (README `fhir-server`, mục GraphQL). Go thêm `GET` và `application/graphql` | — | vừa | L |
| R-19 | Operation tùy biến theo `OperationDefinition` + extension trỏ tới `Bot` (`operations/custom.ts:28-135`, `routes.ts:533`) | CHƯA | Go chỉ cho thêm route bằng mã (`Router.Add`, test `suite.go:267`); không đọc `OperationDefinition` | Bot (M1) | cao nếu dùng Bot | M |
| R-20 | Tiền tố URL `/projects/{id}/fhir/R4`, `/api/fhir/R4` (`app.ts:281`, theo `modules-survey.md` 7.1) | CHƯA | `httpapi.go:69` chỉ gắn `/fhir/R4` | quyết định tiền tố | vừa | S |
| R-21 | CORS, header bảo mật, header `RateLimit` trên mọi phản hồi (`cors.ts`, `ratelimit.ts`, `routes.ts:150-160`) | CHƯA | grep `httpsec` trong `apps/fhir-server`: 0 (`pkg/httpsec` đã gắn vào `auth`) | `pkg/httpsec` | cao | S |
| R-22 | Phản hồi thường **ẩn** `meta.author/project/account/accounts/compartment/deleted`; `X-Medplum: extended` để thấy (`repo.ts:2658-2665`, `oauth/middleware.ts:82`) | CHƯA | Go trả `meta.project` ra client (test khẳng định ở `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go:78`); không có chế độ extended | — | vừa | S |
| R-23 | Ánh xạ lỗi tầng HTTP: JSON lỗi -> 400, quá lớn -> 413, media type lạ -> 415 (`modules-survey.md` 7.2) | CÓ | `http.go:42-82` (400, 413 mã `too-costly`, 415 ở dòng 78-79); `httpapi.go:76-117`; probe 415 | — | vừa | — |
| R-24 | Dạng lỗi `OperationOutcome` thống nhất (`outcomes.ts:1-61`) | CÓ | `router.go:289-335` (`Error`, hàm dựng lỗi, `asError`); `repotest/suite.go:204` | — | cao | — |

### Nhóm S. Search

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| S-01 | Tìm chuỗi: mặc định tiền tố, `:exact`, `:contains` (`search.ts:1230-1266`) | CÓ | `pkg/fhirsearch/match.go`, `pkg/fhirstore/search.go`; không phân biệt hoa thường và bỏ dấu (README `fhir-server`); test `suite.go:290`, bộ đối chiếu Memory/SQL (`pkg/fhirstore/differential_test.go`). Chưa so từng ca biên với `lookups/humanname.ts` | — | cao | — |
| S-02 | Token: `system`, `code`, `:not`, `:text` (`search.ts:1313-1322`, `tokens.ts`) | CÓ | `match.go:93-120,288`, `search.go:183-205`; `suite.go:1473` | — | cao | — |
| S-03 | Token terminology: `:in`, `:not-in`, `:above`, `:below` (Operator có trong `core/src/search/search.ts:76-82`) | CÓ | `pkg/fhirrouter/termsearch.go:25-161` (thay bằng danh sách mã, tối đa 5000 mã mỗi điều kiện), `suite.go:1473`; chỉ dùng CodeSystem/ValueSet trong server | — | vừa | — |
| S-04 | Token `:of-type` (`core/src/search/search.ts:82,110`) | CHƯA | `request.go:242` **chấp nhận** modifier nhưng `match.go`, `search.go` không xử lý (grep `of-type`: chỉ trúng `request.go`); probe: bundle rỗng dù có dữ liệu khớp, tức kết quả **sai im lặng** | — | vừa | S |
| S-05 | Reference `:identifier` và tham chiếu canonical (`search.ts:1348-1425,1773-1850`, `core/.../search.ts:89`) | CHƯA | `request.go:271` trả 400 `modifier :identifier chưa được hỗ trợ`; canonical: chưa xác nhận (không thấy xử lý riêng) | — | vừa | M |
| S-06 | Reference literal, `:Type` (`search.ts:1348-1425`) | CÓ | `match.go:361-362` (`TypeModifier`), `suite.go:383,1745` | — | cao | — |
| S-07 | `:missing` và `:present` (`presence.ts:6-37`) | MỘT PHẦN | `:missing` CÓ (`request.go:276`, `search.go:183`); `:present` không (probe 400) | — | thấp | S |
| S-08 | Date: tiền tố `eq ne gt lt ge le sa eb ap` (`search.ts:1444-1508`, `range-column.ts`) | CÓ | `match.go`, `search.go`; README `fhir-server`; chưa so độ chính xác theo precision (năm, tháng) với `range-column.ts` | — | cao | — |
| S-09 | Number, quantity, uri (`search.ts:1510-1540`) | CÓ | README `fhir-server` (kiểu `number`, `quantity`, `uri`); chưa so quy đổi đơn vị | — | vừa | — |
| S-10 | Chained search thuận và lồng nhiều cấp (`search.ts:1644-1960`) | CÓ | `pkg/fhirrouter/chain.go:29-145`; `suite.go:1745`; giới hạn: mỗi sub-search tối đa 5000 resource (`go-port-status.md` 10a); không chained trong `_sort`/`_include` | — | cao | — |
| S-11 | `_has` (lồng, nhiều điều kiện) (`search.ts:1887,1964`) | CÓ | `chain.go`, `suite.go:1745`; chỉ một kiểu resource mỗi `_has` | — | vừa | — |
| S-12 | `_include`, `_revinclude`, `:iterate` (`fhir-router/src/search-include.ts:135,217`; độ sâu mặc định 5) | CÓ | `request.go:172-203` (parse), `suite.go:383`; giới hạn độ sâu và `*`: chưa xác nhận | — | cao | — |
| S-13 | `_filter` (biểu thức logic) (`search.ts:1130-1230`) | MỘT PHẦN | `pkg/fhirsearch/filterexpr.go:35-304`; thiếu toán tử `ew ss sb in re po`, composite, modifier, chained trong tham số, search hệ thống (`go-port-status.md` 21a) | — | thấp | M |
| S-14 | Composite (`row-builder.ts:304-306` ghi rõ **Medplum không hỗ trợ** special và composite trong CSDL) | CÓ | Go **vượt** Medplum: `pkg/fhirsearch/composite.go`, `suite.go:1691` | — | thấp | — |
| S-15 | `_sort` nhiều khóa, tăng/giảm (`search.ts:1542-1600`) | CÓ | `request.go:170,215-240`; `go-port-status.md` 4: sắp xếp theo tham số nhiều giá trị với offset lớn chậm (~377 ms ở 5000 Patient) | — | cao | — |
| S-16 | Phân trang `_count`, `_offset`, **cursor** `_cursor` (`search.ts:546-700`) | MỘT PHẦN | offset CÓ (`request.go:136-149`, `suite.go:290`); `_cursor` bị bỏ qua (`request.go:95`, probe) | — | vừa | M |
| S-17 | `_total=none/estimate/accurate`, ước lượng có ngưỡng (`search.ts:733-860`) | MỘT PHẦN | `request.go:150-155`; `pkg/fhirstore/search.go:31-36`: `estimate` luôn đếm chính xác (đúng nhưng chậm với bảng lớn) | — | thấp | S |
| S-18 | `_summary=true/text/data/count` (`search.ts:482-503`) | MỘT PHẦN | chỉ `count` (`handlers.go:510-512`); `true/text/data` bị bỏ qua (probe) | — | thấp | S |
| S-19 | `_elements` và `_fields` (`search.ts:484-492`) | MỘT PHẦN | `_elements` CÓ (`handlers.go:542,552`); `_fields` (Medplum) trả 400 (probe) | — | thấp | S |
| S-20 | Tham số chung `_id`, `_lastUpdated`, `_tag`, `_profile`, `_security`, `_source` | CÓ | có trong `pkg/fhir/searchparams_gen.go` (mỗi mã một định nghĩa), probe: nhận và trả 200; **chưa kiểm đúng đắn từng tham số** | — | vừa | — |
| S-21 | Tham số đặc biệt `_deleted`, `_compartment` (`search.ts:1113-1135`) | CHƯA | probe: cả hai trả 400; `_project` được thêm riêng cho tách project (`go-port-status.md` 7a) | — | thấp | S |
| S-22 | `GET /?_type=A,B` search hệ thống (`fhirrouter.ts:113-127`) | CÓ | `handlers.go:497-503`, `suite.go:364` | — | vừa | — |
| S-23 | `POST /Type/_search` (`fhirrouter.ts:129-146`) | CÓ | `router.go:273`, `http.go:55-65`; `suite.go:364` | — | vừa | — |
| S-24 | Bảng lookup tên, địa chỉ, liên lạc, coding (`lookups/humanname.ts` 261 dòng, `address.ts` 219, `coding.ts` 211, `conceptmapping.ts` 125) | MỘT PHẦN | Go dùng bảng chỉ mục chung theo kiểu (`pkg/fhirsearch/index.go`, `pkg/fhirstore/search.go`) thay vì lookup riêng; ngữ nghĩa khớp tên có cùng kết quả hay không: **chưa xác nhận** | — | vừa | M |
| S-25 | `searchUnique` (`searchunique.ts:12-27`): đúng một kết quả, nhiều thì lỗi `multipleMatches` | CÓ | `handlers.go:129-146` (`conditionalMatches`, `Count=2`), 412 khi nhiều khớp (`handlers.go:211,368,401,461`) | — | vừa | — |
| S-26 | Cài đặt lưu trữ tìm kiếm: `token-column.ts` (525), `range-column.ts` (421), `tokens.ts` (113), `sql.ts` (1491), `lookups/reference.ts`, `row-builder.ts` | KHÔNG ÁP DỤNG | gắn với lược đồ PostgreSQL của Medplum; Go dùng `fhir_idx_<kiểu>` và goose riêng (`pkg/fhirstore/store.go:1-9`); ngữ nghĩa nằm ở S-01 đến S-23 | — | — | — |

### Nhóm B. Batch, transaction, patch, history, điều kiện

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| B-01 | Batch: mỗi entry độc lập (`fhir-router/src/batch.ts:102-993`) | CÓ | `pkg/fhirrouter/batch.go:50-172`; `suite.go:534` | — | cao | — |
| B-02 | Transaction: nguyên tử, `urn:uuid`, thứ tự DELETE, POST, PUT/PATCH, GET (`batch.ts:417-523,880-912`) | CÓ | `batch.go:175-290`; `suite.go:571,601,620`. Go luôn nguyên tử; Medplum chỉ nguyên tử khi project có feature `transaction-bundles` (`batch.ts:912-914`, `routes.ts:526`) | — | cao | — |
| B-03 | Tham chiếu có điều kiện `Patient?identifier=x` trong mọi lần ghi và trong Bundle (`references.ts:121-190`, `repo.ts:1140`) | CHƯA | `batch.go:272` chỉ thay `urn:uuid`; grep tham chiếu có `?` trong `pkg/fhirrouter`: không có | — | vừa | M |
| B-04 | Entry `PATCH` trong Bundle: `Binary` base64 chứa JSON Patch, hoặc `Parameters` (`batch.ts:802-880`) | CHƯA | probe: entry trả `400`; `batch.go:129-146` ép `Content-Type` JSON Patch và truyền thẳng `resource` làm body | — | vừa | S |
| B-05 | Batch bất đồng bộ `Prefer: respond-async` + checkpoint (`async-batch.ts:15-51`, `batch/checkpoint-store.ts:1-226`, `workers/batch.ts`) | CHƯA | probe: header bị bỏ qua, chạy đồng bộ 200 | hàng đợi (`pkg/workflow` có asynq/Temporal), kho đối tượng cho checkpoint | vừa | M |
| B-06 | JSON Patch `PATCH /Type/id` (`fhirrouter.ts:295-310`, `repo.ts:1755`) | CÓ | `pkg/fhirrouter/patch.go`, `handlers.go:404-443`; `suite.go:466` | — | cao | — |
| B-07 | FHIRPath Patch (`Parameters`) (`repo.ts:1787-1791`, `fhirrouter.ts:295-308`) | CHƯA | probe 415; `pkg/fhirpath` đã có engine | — | thấp | S |
| B-08 | History của một resource, có `_count` và `_offset` (`fhirrouter.ts:217-224`, `repo.ts:890`) | MỘT PHẦN | `handlers.go:466-503`: luôn trả toàn bộ, bỏ qua `_count`/`_offset` (probe: 3 entry khi `_count=1`); `suite.go:171-195` | — | vừa | S |
| B-09 | History cấp loại và cấp hệ thống | KHÔNG ÁP DỤNG | Medplum cũng không đăng ký (`fhirrouter.ts:344-358` chỉ có history instance) | — | — | — |
| B-10 | Conditional create `If-None-Exist` (`fhir-router/src/repo.ts:292-345`) | CÓ | `handlers.go:150-216`; `suite.go:409,1865` | — | cao | — |
| B-11 | Conditional update (0, 1, nhiều khớp; id không khớp -> 400) (`repo.ts:348-401`) | CÓ | `handlers.go:344-370`; `suite.go:426,1945` | — | cao | — |
| B-12 | Conditional delete (`repo.ts:403-422`) | CÓ | `handlers.go:383-401`; `suite.go:426,1963` | — | vừa | — |
| B-13 | Conditional patch (`repo.ts:424-445`) | CÓ | `handlers.go:449-462`; `suite.go:426` | — | thấp | — |
| B-14 | Không tạo phiên bản mới khi nội dung không đổi (`repo.ts:1359-1370`, `isNotModified`; dùng ở `repo.ts:1189`) | CHƯA | `pkg/fhirstore/crud.go:142-181` luôn `stampDoc` và ghi lịch sử; probe: 3 phiên bản | — | vừa | S |
| B-15 | Xóa mềm giữ lịch sử, đọc trả 410 (`repo.ts:1655-1750`) | CÓ | `pkg/fhirstore/crud.go:184-210`, `suite.go:133` | — | cao | — |
| B-16 | Phục hồi phiên bản (`restoreResourceVersions`, `repo.ts:1446`) và `purgeResources` (`repo.ts:1930`) | KHÔNG ÁP DỤNG | chỉ phục vụ `$restore`/quản trị Medplum (mục 5) | — | — | — |

### Nhóm J. Bulk data, job nền, bảo trì dữ liệu

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| J-01 | `GET/POST /$export` hệ thống, `_type`, `_since`, `_typeFilter`, tham số trong body `Parameters`, xuất cả `deleted` khi `_since` và là admin (`operations/export.ts:68-82,114-150,200-300`, `routes.ts:248-249`) | MỘT PHẦN | `pkg/fhirrouter/bulk.go:49-120`: chỉ `GET` (probe: `POST` 405), bắt buộc `Prefer: respond-async` (`bulk.go:60`, Medplum không bắt buộc), không có đầu ra `deleted`; `suite.go:958,1256` | hàng đợi | cao | M |
| J-02 | `Patient/$export` (`export.ts:83`, `routes.ts:369-370`) | MỘT PHẦN | `bulk.go:51`; cùng giới hạn J-01; resource thuộc compartment qua nhiều tham số được gộp và khử trùng (`go-port-status.md` 8a) | — | cao | S |
| J-03 | `Group/{id}/$export` (`operations/groupexport.ts:35-147`, `routes.ts:365-366`) | MỘT PHẦN | `bulk.go:52,471-539`; `suite.go:1319`; chỉ `GET`; Group có `characteristic` mà không liệt kê thành viên không hỗ trợ; tối đa 5000 thành viên | — | vừa | S |
| J-04 | `GET /bulkdata/export/{id}`: 202 khi đang chạy, 200 manifest, **400** khi lỗi, **404** khi đã hủy; `requiresAccessToken: false` (`bulkdata.ts:33-61`) | MỘT PHẦN | `bulk.go:53,302-347`: URL là `bulkdata/{id}/status`; lỗi 500 và hủy 410 (dòng 343-345 gọi `jobStatus`); `requiresAccessToken: true` (dòng 338); file Binary cần token | — | vừa | S |
| J-05 | `DELETE /bulkdata/export/{id}` hủy (`bulkdata.ts:63-76`) | CÓ | `bulk.go:54,376-410`; `go-port-status.md` 9a | — | vừa | — |
| J-06 | `GET /job/{id}/status`: 202 khi chưa xong; **200 kèm AsyncJob cho cả `completed`, `error`, `cancelled`** (`job.ts:17-40`) | MỘT PHẦN | `pkg/fhirrouter/jobs.go:229-257`: `error` -> 500 kèm OperationOutcome, `cancelled` -> 410; `completed` 200 và chưa xong 202 giống | — | vừa | S |
| J-07 | `DELETE /job/{id}/status` và `POST /AsyncJob/{id}/$cancel` (`job.ts:44-72`, `operations/asyncjobcancel.ts:26`, `routes.ts:338`) | CHƯA | chỉ hủy được export qua `DELETE bulkdata/{id}/status` (`bulk.go:376`); không có route cho job khác (`$reindex`) | — | thấp | S |
| J-08 | Bộ thực thi AsyncJob: khởi tạo, tiến độ, `Content-Location` (`operations/utils/asyncjobexecutor.ts:1-277`) | CÓ | `jobs.go:33-227`, lease và khôi phục job mồ côi `jobs_maint.go`; `suite.go:819,859`, `jobs_durable_test.go`. Go **vượt** về độ bền (lease, nhiều replica) | `pkg/workflow` | cao | — |
| J-09 | `BulkDataExport` resource và ghi file qua kho đối tượng (`utils/bulkexporter.ts:1-206`) | MỘT PHẦN | file NDJSON ghi vào bảng resource dạng `Binary` base64, mỗi file tối đa ~8 MB (`go-port-status.md` 8a) | Storage | vừa | M |
| J-10 | `$reindex` một resource (`routes.ts:455-461`, `repo.ts:1379`) | CÓ | `operation.go:297-311`; `suite.go:819` | — | vừa | — |
| J-11 | Worker `reindex` cho cả loại (`workers/reindex.ts`, qua `admin/super`) | CÓ | `POST /{type}/$reindex` chạy nền (`operation.go:288-295`), `suite.go:819`; Go **vượt**: lộ qua REST cho admin | — | vừa | — |
| J-12 | `$expunge` một resource, `everything=true` và `Project` chạy nền (`operations/expunge.ts:33-72`, `Expunger` dòng 74-219) | MỘT PHẦN | `operation.go:313-323`: chỉ một resource, trả 200; không `everything`, không Project (`go-port-status.md` 6a); `suite.go:882`. Dọn Binary kèm theo chưa có | Storage (cho Binary ngoài) | thấp | M |
| J-13 | `$set-accounts` (có `propagate`, chạy nền, trần 1000 resource) (`operations/set-accounts.ts:82-254`, `workers/set-accounts.ts`) | CHƯA | grep `set-accounts`, `accounts` trong Go: 0 | `meta.account` (H-07), hàng đợi | thấp | M |
| J-14 | `$refresh-reference-display` (`operations/refresh-reference-display.ts:9-48`) | **XONG 2026-11-24** | `pkg/fhirrouter/clinical_ops.go` (`refreshReferenceDisplay`), `display.go` (getDisplayString); test `clinical_ops_test.go` | — | thấp | S |
| J-15 | Worker `download`: tải URL ngoài trong `Attachment` về Binary (`workers/download.ts`) | CHƯA | grep: 0 | safe HTTP (`pkg/fhirsub/ssrf.go` cần trích), Storage | thấp | M |
| J-16 | `$resend` Subscription, bản hiện tại và bản lịch sử (`routes.ts:463,473`, `repo.ts:1553`) | **XONG 2026-11-24** | `fhirrouter.SubscriptionResender`; `pkg/fhirsub/repo.go` (`ResendSubscriptions`), `pkg/fhirauth/scope.go` (chỉ admin, đúng project); cần bật `FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD` (có broker), không thì 501 | `pkg/fhirsub` | thấp | S |
| J-17 | `$graph` theo `GraphDefinition` (`operations/resourcegraph.ts:41-337`) | **XONG 2026-11-24** | `pkg/fhirrouter/graph.go`; test `clinical_ops_test.go` | — | vừa | M |

### Nhóm A. Quyền truy cập, SMART, AccessPolicy

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| A-01 | Resource `AccessPolicy` lưu trong server, dựng chính sách từ `ProjectMembership.access` (`accesspolicy.ts:210-270`) | MỘT PHẦN | chính sách chỉ đến từ claim token `fhir_access_policy` (`pkg/fhirauth/policy.go:12-60`, README `fhir-server`); `go-port-status.md` 11b: "chưa có resource AccessPolicy lưu trong server". `apps/auth` có đọc AccessPolicy nhưng chỉ cho `ipAccessRule` (`apps/auth/internal/application/membership.go:47-60`) | auth | cao | L |
| A-02 | `criteria` theo loại (`repo.ts:2094-2230`) | CÓ | `policy.go:62-80` (không chained/`_has`/`_filter`/`:in`); `policy_test.go`; `suite` qua `Scope`: `scope_test.go` | — | cao | — |
| A-03 | Nhiều rule cho một loại (OR) và biến `%patient`, `%profile` (`accesspolicy.ts:272-326`) | CHƯA | `policy.go:65`: "loại %s có nhiều hơn một rule"; không có thay thế biến | — | vừa | M |
| A-04 | `readonly` và `interaction` theo rule (`accesspolicy.ts:236-238`) | MỘT PHẦN | `readonly` CÓ (`policy.go:26,47,99`); danh sách `interaction` (`read vread history search create update delete`) không có | — | vừa | S |
| A-05 | `hiddenFields`, `readonlyFields`, `writeConstraint` (`repo.ts:2623-2670,2768`, `accesspolicy.ts:387-413`) | ĐÃ PORT hiddenFields/readonlyFields/interaction (2026-11-24, `pkg/fhirauth/fieldrules.go`); `writeConstraint` CHƯA | grep `hidden`, `readonlyField`, `writeConstraint` trong `pkg/fhirauth`: 0 | — | vừa | M |
| A-06 | Quyền theo `compartment` (Patient compartment) (`accesspolicy.ts:174`, `repo.ts:2094-2230`, `search.ts:1127`) | CHƯA | `pkg/fhir/compartment_gen.go` chỉ dùng cho `$everything`/export; scope `patient/` bị bỏ qua (`pkg/fhirauth/grants.go:54-55`) | — | vừa | L |
| A-07 | `ipAccessRule` (`accesspolicy.ts:222,241`) | MỘT PHẦN | `auth` gom rule lúc đăng nhập (`membership.go:47-60`); `fhir-server` không kiểm IP (grep `ipAccess` trong `apps/fhir-server`, `pkg/fhirauth`: 0). Việc thực thi ở `auth`: chưa xác nhận | auth | thấp | S |
| A-08 | Chính sách mặc định theo `profileType` (`default-access-policies.ts:37-191`) | CHƯA | grep: 0 | auth, `Project.defaultPatientAccessPolicy` | vừa | M |
| A-09 | SMART v2 theo từng quyền `c r u d s` -> interaction riêng (`smart.ts:266-272,318-325`) | **CÓ (đã sửa 2026-11-21, WP-2a)**; mô tả dưới đây là lỗi **trước khi sửa** | `pkg/fhirauth/grants.go:55-85` (dòng 74-77) **gộp** `c u d` thành Write và `r s` thành Read: `system/Observation.c` vô tình cho cả sửa và xóa; `.s` cho cả đọc theo id. Xem mục 3.2, Lệch-6 | — | cao (bảo mật) | S |
| A-10 | Scope SMART có tham số tìm kiếm (`Observation.rs?category=x`) (`smart.ts:142-170,334-350`) | CHƯA | `grants.go:55-70` cắt theo `.` rồi so khớp từng chữ cái; scope có `?` không hợp lệ -> bị bỏ qua | — | vừa | M |
| A-11 | Scope `patient/` và ngữ cảnh launch (`SmartAppLaunch`) (`smart.ts:196-235`) | CHƯA | `grants.go:54` (bỏ qua, không cấp quyền) | auth P3 (launch), A-06 | vừa | L |
| A-12 | Admin, super admin, loại resource chỉ project admin (`repo.ts:2839-2843,260-296`, `accesspolicy.ts:361-454`) | MỘT PHẦN | super-admin xuyên project (`FHIR_JWT_SUPER_ADMIN`, scope `fhir.super`, `httpapi/auth.go:189`, `go-port-status.md` 11d); vai trò `ADMIN` cho `$reindex`/`$expunge`; không có danh sách loại chỉ admin project, `Project`/`ProjectMembership` nằm ở `auth` | auth | vừa | M |
| A-13 | Project liên kết (`Project.link`), chia sẻ resource giữa project (`repo.ts:260-296`, `accesspolicy.ts:139-170`) | CHƯA | grep `link` về project: 0; tách project hoàn toàn theo `meta.project` (`scope.go:89-125`) | auth | thấp | L |
| A-14 | `onBehalfOf`: header `x-medplum-on-behalf-of`, `meta.onBehalfOf` (`oauth/utils.ts:1194-1260,1356-1374`, `repo.ts:1131`, `context.ts:92`) | CHƯA | grep `onBehalfOf` trong Go: chỉ kiểu sinh sẵn | auth | thấp | M |
| A-15 | Xác thực Bearer (token opaque tra CSDL, Basic cho ClientApplication) (`oauth/middleware.ts`, ngoài `fhir/`) | MỘT PHẦN | `fhir-server`: token tĩnh 3 mức và JWT HS256/JWKS (`pkg/fhirauth/jwt.go`, `jwt_test.go`, `apps/fhir-server/.../auth.go`); cấp token là `auth` P3 (chưa cài) | auth P3 | cao | L (đã có kế hoạch riêng: `auth` P3) |
| A-16 | Quyết định theo `ProjectMembership`: trạng thái hoạt động, tìm membership (`projectmembership.ts:33-119`) | KHÔNG ÁP DỤNG | thuộc `auth` (`apps/auth/internal/application/membership.go`), `fhir-server` nhận claim | — | — | — |

### Nhóm V. Kiểm tra dữ liệu và định nghĩa

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| V-01 | Kiểm cấu trúc, kiểu, cardinality, choice type khi ghi (chế độ strict) (`repository/validation.ts:40-69,106-149`) | CÓ | `pkg/fhirvalidate/validate.go`; 0 lỗi trên 14 file dữ liệu thật (`go-port-status.md` 2b); `suite.go:228,718` | — | cao | — |
| V-02 | Invariant FHIRPath (`validateResource` của `@medplum/core`) | CÓ | `validate.go:407-444`, `pkg/fhirpath` 679/686 ca HL7 | — | cao | — |
| V-03 | Kiểm theo profile `meta.profile` -> `StructureDefinition` (`validation.ts:151-179,234-256`) | **MỘT PHẦN (2026-11-24)** | `pkg/fhirvalidate/profile.go`, `pkg/fhirrouter/profiles.go`; bật bằng `FHIR_VALIDATE_PROFILES`, tra StructureDefinition trong kho theo project; cardinality, maxLength, giới hạn kiểu `[x]`, invariant của profile. Chưa: resource lồng, Extension theo profile kiểu, chưa đối chiếu với validator của Medplum trên bộ profile thật | StructureDefinition trong kho (V-12) | cao nếu dùng profile | M |
| V-04 | Slicing, `fixed[x]`, `pattern[x]` | **MỘT PHẦN (2026-11-24)** | như V-03; discriminator `value`, `pattern`, `exists`, `type`; `closed`/`ordered`/`openAtEnd`; discriminator `profile` và đường dẫn có hàm FHIRPath chỉ cảnh báo | — | cao nếu dùng profile | M |
| V-05 | Kiểm binding terminology khi ghi (cờ `validateTerminology`) (`validation.ts:181-232`) | CHƯA | `validate.go:5`; README `fhir-server`: "code ngoài ValueSet chưa bị từ chối" | Terminology (nhóm T) | vừa | M |
| V-06 | Kiểm đích `Reference` theo profile target | CHƯA | `validate.go:5` | — | thấp | M |
| V-07 | `checkReferencesOnWrite`: mọi `Reference` phải đọc được (`references.ts:29-119`, `repo.ts:1183-1187`) | CHƯA | grep `referential`, `checkReferences`: 0 | — | vừa | M |
| V-08 | Meta chỉ-đọc/được bảo vệ: khôi phục `readonlyFields`, `canWriteProtectedMeta` (`repo.ts:2465-2468,2768`) | MỘT PHẦN | Go bỏ `meta.versionId`, `lastUpdated` do client gửi (`pkg/fhirstore/crud.go:148-152`) và ghi đè `meta.project` (`jwt_e2e_test.go:77-79`); các trường khác (`author`, `account`, `compartment`) không có để bảo vệ | — | thấp | S |
| V-09 | `Binary.securityContext` phải đọc được (`repo.ts:1097-1103`) | CHƯA | grep `securityContext`: 0 | — | thấp | S |
| V-10 | Kiểm `Cron`: cron string hợp lệ và tham chiếu đọc được (`validation.ts:71-104`) | CHƯA | `pkg/fhir/resource_cron.go` có kiểu; không có kiểm tra | Bot/cron (M1) | thấp | S |
| V-11 | Trần Subscription websocket theo người dùng (`repo.ts:1160-1181`, `ws/subscriptions.ts`) | CHƯA | Go chỉ giới hạn 50 bind mỗi kết nối và 1000 kết nối mỗi instance (`pkg/fhirsub/ws.go`, README) | Redis | thấp | S |
| V-12 | `StructureDefinition`, `SearchParameter`, `ValueSet` chuẩn có trong kho để client đọc qua REST; nạp từ `@medplum/definitions` (`operations/rebuild-base-definitions.ts:40-187`, `seed.ts:139`) | CHƯA | kiểu và định nghĩa nhúng trong `pkg/fhir` (schema, `searchparams_gen.go`) nhưng **không** được lưu thành resource REST; ứng dụng Medplum đọc `StructureDefinition` từ server để dựng form (phán đoán, chưa kiểm ở `packages/app`) | dữ liệu `definitions` | cao nếu dùng app Medplum | M |
| V-13 | Nạp SearchParameter lúc khởi động và lọc bằng `enabledSearchParameters` (`structure.ts:21-33`, `config/types.ts:265`) | MỘT PHẦN | Go sinh bảng tĩnh bằng `tools/fhirgen` (`pkg/fhir/searchparams_gen.go`); không có cấu hình lọc | — | thấp | S |
| V-14 | SearchParameter/StructureDefinition do project ghi vào CSDL **có hiệu lực lúc chạy** | KHÔNG ÁP DỤNG | không tìm thấy mã đăng ký lúc chạy ở Medplum 5.2.18: `indexSearchParameter` chỉ gọi ở `structure.ts:31`, `migrations/migrate.ts` và test (grep toàn `packages/server/src`); `go-port-status.md` 4 cũng ghi "khi định nghĩa search parameter đổi cần job reindex (chưa có)". Chưa xác nhận Medplum có đường khác ngoài `fhir/`, xem Q-F19 | — | — | — |
| V-15 | Bộ kiểm JSON-schema cũ khi `strictMode=false` (`jsonschema.ts:1-119`, `validation.ts:48-69`) | KHÔNG ÁP DỤNG | Go chỉ có một bộ kiểm; không có chế độ lỏng | — | — | — |
| V-16 | `$validate` (`routes.ts:448-453`) | CÓ | `operation.go:126-161`; `suite.go:718`; trả 400 khi sai (khớp Medplum, ném `OperationOutcomeError`); docs `fhir-server-go.md` ghi 200: xem Lệch-12 | — | vừa | — |
| V-17 | `POST /$rebuild-base-definitions`: nạp lại StructureDefinition, SearchParameter, ValueSet chuẩn vào kho (`operations/rebuild-base-definitions.ts:40-187`, `routes.ts:245`) | CHƯA | grep `rebuild-base`: 0; phụ thuộc V-12 | dữ liệu `definitions` | vừa | M |

### Nhóm H. Hạ tầng của lớp `fhir/` (repository, hạn mức, kiểm toán)

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| H-01 | AuditEvent khi create/read/update/delete/search, ghi vào kho (`saveAuditEvents`) hoặc log (`logAuditEvents`) (`repo.ts:2858-2960`, `util/auditevent.ts`, `config/types.ts:77-78`) | **MỘT PHẦN (2026-11-24)** | `pkg/fhiraudit` (decorator Repository), `FHIR_AUDIT=off\|writes\|all` ở `fhir-server` và `fhir-worker`: create/update/delete (và read/vread/history/search ở `all`), người gọi, IP, project, thực thể; ghi sau commit trong transaction; job nền ghi cả người yêu cầu lẫn job; AuditEvent không sửa/xóa qua API. Chưa: AuditEvent cho đăng nhập (`auth`), tự tắt Subscription, thao tác bị từ chối, lưu giữ tự động, chống sửa ở mức CSDL; chưa đối chiếu hình dạng AuditEvent với Medplum (không còn nguồn) | — | cao nếu cần tuân thủ | M |
| H-02 | Hạn mức FHIR theo người dùng và project, tính điểm theo thao tác, header `RateLimit`, 429 (`fhirquota.ts:22-260`, `repo.ts:525-545`) | **XONG một phần 2026-11-25** (`FHIR_QUOTA_USER/PROJECT/WINDOW`, điểm cố định theo hình dạng yêu cầu, cửa sổ cố định, header `RateLimit-*`, 429; chưa: điểm theo thời gian xử lý, giá trị riêng từng project) | grep `ratelimit`, `quota`, `429` trong `pkg/fhir*`, `apps/fhir-server`: không có (429 chỉ là mã webhook cần retry, `pkg/fhirsub/deliver.go`) | Redis (đã có `pkg/messaging` Redis) | cao (điều kiện trước khi mở Internet, theo `modules-survey.md` 7.2) | M |
| H-03 | Trần số resource mỗi project mỗi ngày (`resource-cap.ts:20-132`, `repo.ts:604-606,636-637`) | CHƯA | grep `resource.?cap`: 0 | Redis | thấp | S |
| H-04 | Pre-commit Bot: Subscription có extension `pre-commit-bot` chạy Bot trước khi ghi, có thể sửa resource (`precommit.ts:30-153`, `repo.ts:1116-1125`) | CHƯA | grep `pre-commit`: 0 | Bot (M1) | thấp | M |
| H-05 | Chặn xóa resource đang được `ProjectMembership` tham chiếu (`profile`, `user`, `access-policy`) (`precommit.ts:155-200`) | CHƯA | `fhir-server` không biết `ProjectMembership`; `auth` lưu chung kho nên xóa qua `fhir-server` không bị chặn | auth | vừa | S |
| H-06 | Việc nền sau commit (`addBackgroundJobs`: subscription, download, cron, dispatch) (`repo.ts:1196-1203,1217-1219`, `workers/utils.ts`) | MỘT PHẦN | chỉ sự kiện Subscription sau commit (`pkg/fhirsub/manager.go`, `event.go`; README `fhir-server`); không có download, cron, dispatch | Bot cho cron/dispatch | vừa | M |
| H-07 | `meta.author`, `meta.onBehalfOf`, `meta.compartment`, `meta.account(s)` do server gán (`repo.ts:1131-1160,2230-2270,2388`) | CHƯA | Go chỉ gán `meta.project` (`pkg/fhirauth/scope.go:98-125`) và `versionId`, `lastUpdated` (`crud.go:47-56`) | auth (danh tính người ghi) | vừa | M |
| H-08 | Giao dịch với hook `preCommit`/`postCommit`, khóa tư vấn cho create có điều kiện (`repo.ts:2970-3050`, `fhir-router/src/repo.ts:292`) | CÓ | `router.go` (`Transactional`, `KeyLocker`), `pkg/fhirstore/store.go`, sự kiện sau commit; `suite.go:601,1865`, `postgres_test.go` | — | cao | — |
| H-09 | Sinh id UUID phía server (`repo.ts:682`) | CÓ | `pkg/fhirstore/store.go` (`newID`), `batch.go:28` | — | cao | — |
| H-10 | Tiện ích compartment Patient (`patient.ts:25-115`) | CÓ | `pkg/fhir/compartment.go`, `compartment_gen.go`; dùng trong `$everything`, export | — | vừa | — |
| H-11 | OperationDefinition cho từng operation, vào/ra theo `Parameters` (`operations/definitions.ts:13-76`, `utils/parameters.ts:1-360`) | CHƯA | Go có bộ đọc tham số tối giản `Input` (`operation.go:40-115`); không phát `OperationDefinition` | — | thấp | M |
| H-12 | Cấu hình theo project: `systemSetting`, `features` (`transaction-bundles`, `async-batch`), giới hạn GraphQL (`routes.ts:511-527`) | CHƯA | cấu hình toàn cục bằng biến môi trường (`apps/fhir-server/internal/config`) | `Project` ở auth | thấp | S |
| H-13 | Số đo theo từng interaction và độ trễ (`repo.ts:2858-2885`, `batch-telemetry.ts:1-40`) | MỘT PHẦN | có instrumentation HTTP qua `dep.Instrument` (`httpapi.go:43-47`, `trivita-observability-go`); không có bộ đếm theo interaction và loại resource | — | thấp | S |
| H-14 | Cụm `sharding.ts` (288 dòng), `assertShardReachable` (`repo.ts:426`) | KHÔNG ÁP DỤNG | chính file ghi giai đoạn "future-proofing" (`sharding.ts:20-33`: shard `placeholder` và `todo` đều quy về `global`) | — | — | — |
| H-15 | Định tuyến reader/writer, nhóm kết nối (`repository/repository-connection.ts` 1049 dòng, `repository-connections.ts`, `repo.ts:398-460`) | KHÔNG ÁP DỤNG | Medplum tối ưu cho bản sao đọc PostgreSQL; Go dùng `pkg/database` một kết nối; có thể cần lại nếu thêm replica đọc (không nằm trong phạm vi hiện tại) | — | — | — |
| H-16 | Bộ nhớ đệm resource Redis (`repository/resource-cache.ts:1-163`) | KHÔNG ÁP DỤNG | tối ưu hiệu năng, không đổi hành vi; chưa có số đo cho thấy cần (không có bài kiểm tải, `go-port-status.md` 22a) | Redis | — | — |
| H-17 | `access-tracker.ts` (296), `transaction-idle-tracker.ts` (150), `row-builder.ts` (365), `field-utils.ts` | KHÔNG ÁP DỤNG | gắn với truy cập SQL của Medplum | — | — | — |
| H-18 | Resource chỉ lưu cache (`isCacheOnly`, ví dụ `Login`) (`repo.ts:2605`) | KHÔNG ÁP DỤNG | thuộc dữ liệu đăng nhập của `auth` | — | — | — |

### Nhóm T. Terminology và định nghĩa

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| T-01 | `ValueSet/$expand`: `filter`, `count`, `offset`, `excludeNotForUI`, `includeDesignations`, `displayLanguage`, `valueSet` nội tuyến (`operations/expand.ts:71-813`, `routes.ts:281-284`) | MỘT PHẦN | `pkg/fhirrouter/terminology.go:544-596`: có `url`, `valueSet`, `filter`, `count`, `offset`; không đọc `excludeNotForUI`, `includeDesignations`, `displayLanguage`; CodeSystem `content=not-present` trả 422; tối đa 100.000 mã, tính trong bộ nhớ mỗi lần (`go-port-status.md` 8a); `suite.go:1565` | — | cao | M |
| T-02 | `ValueSet/$validate-code` (`operations/valuesetvalidatecode.ts:46-215`) | MỘT PHẦN | `terminology.go:597-629`; cùng giới hạn nguồn mã như T-01; chưa so từng ca | — | vừa | S |
| T-03 | `CodeSystem/$lookup` (`codesystemlookup.ts:24-149`) | MỘT PHẦN | `terminology.go:181-260`; chỉ CodeSystem có concept lưu trong server | — | vừa | S |
| T-04 | `CodeSystem/$validate-code` (`codesystemvalidatecode.ts:33-119`) | MỘT PHẦN | `terminology.go:630-667`; cùng giới hạn | — | vừa | S |
| T-05 | `CodeSystem/$subsumes` (`subsumes.ts:31-91`) | CÓ | `pkg/fhirrouter/terminology2.go:23-24`; `suite.go:1369`; theo cây cha-con của một CodeSystem, không `partOf` (`go-port-status.md` 14a); chưa so từng ca | — | vừa | — |
| T-06 | `ConceptMap/$translate` (`conceptmaptranslate.ts:40-255`) | CÓ | `terminology2.go:25-26`; `suite.go:1369`; chưa xét `dependsOn`/`product`; chưa so từng ca | — | vừa | — |
| T-07 | `CodeSystem/$import` nạp hàng loạt mã và thuộc tính (`codesystemimport.ts:93-288`, `routes.ts:287-288`) | CHƯA | grep `$import`: 0 | — | cao nếu cần SNOMED, LOINC | M |
| T-08 | `ConceptMap/$import` (`conceptmapimport.ts:117-390`, `routes.ts:277-278`) | CHƯA | grep: 0 | — | vừa | M |
| T-09 | Bảng tra mã cho CodeSystem lớn (`Coding`, `Coding_Property`, `ConceptMapping`) và `:above/:below` theo thuộc tính (`lookups/coding.ts:1-211`, `lookups/conceptmapping.ts:1-125`, `operations/utils/terminology.ts:1-321`) | CHƯA | Go giữ cây concept trong bộ nhớ từ resource `CodeSystem` (`termsearch.go:116-161`) | cùng T-07 | cao nếu dùng thuật ngữ lớn | L |
| T-10 | `StructureDefinition/$expand-profile` (`structuredefinitionexpandprofile.ts:16-117`) | **XONG 2026-11-24** | `pkg/fhirrouter/clinical_ops.go` (`expandProfile`) | V-12 | thấp | S |
| T-11 | `PackageRelease/$install` (`packageinstall.ts:29-81`) | CHƯA | grep `$install`: 0; kiểu `PackageRelease` có sẵn (`pkg/fhir/resource_packagerelease.go`) | Storage | thấp | M |

### Nhóm P. Operation lâm sàng

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| P-01 | `Patient/{id}/$everything` (`patienteverything.ts:68-434`, `routes.ts:394-395`) | MỘT PHẦN | `operation.go:163-273`; `suite.go:746`. Có `_type`, `_since`, `_count` (mặc định 1000, trần 10000, vượt thì cắt và thêm entry cảnh báo). Thiếu `start`/`end` (ngày chăm sóc, `utils/caredate.ts`), `_offset`, `_cursor`, `_inlineAttachments` | — | cao | M |
| P-02 | `Patient/{id}/$summary` (International Patient Summary) (`patientsummary.ts:79-872`) | **XONG 2026-11-24 (thiếu start/end/profile/identifier, mức loại)** | `pkg/fhirrouter/patientsummary.go`; `author` bắt buộc | — | vừa | L |
| P-03 | `Patient/$match` chấm điểm ứng viên (`patientmatch.ts:75-365`, `utils/cms-patient-match.ts:1-425`) | **XONG 2026-11-24, khác Medplum có chủ ý (không port bảng CMS Mỹ)** | `pkg/fhirrouter/patientmatch.go` | — | vừa | M |
| P-04 | `Patient/{id}/$ccda-export` (`ccdaexport.ts:39-62`, gọi `@medplum/ccda`, `packages/ccda/src` khoảng 4000 dòng) | CHƯA | grep `ccda`: 0 | thư viện C-CDA (TS, chưa có bản Go) | thấp | L |
| P-05 | `QuestionnaireResponse/$extract` (`extract.ts:131-562`) | **XONG 2026-11-24 (templateExtract; context application/x-fhir-query chưa hỗ trợ)** | `pkg/fhirrouter/extract.go` | — | vừa | L |
| P-06 | `Measure/{id}/$evaluate-measure` (`evaluatemeasure.ts:45-160`) | CHƯA | grep `evaluate-measure`: 0 | — | thấp | M |
| P-07 | `PlanDefinition/{id}/$apply` (`plandefinitionapply.ts:97-548`) | CHƯA | grep `$apply`: 0; `pkg/fhirpath` đã có engine để tính biểu thức động | — | vừa | L |
| P-08 | `$generate-smart-health-card` (`smarthealthcards.ts:62-438`) | CHƯA | grep `smart-health`: 0 | khóa ký JWS, QR | thấp | M |
| P-09 | `$verify-smart-health-card` (`smarthealthcards.ts:82`) | CHƯA | như P-08 | — | thấp | S |
| P-10 | `Patient/{id}/$generate-smart-health-link` (`smarthealthlinks.ts:72`, `routes.ts:410`) | CHƯA | grep: 0 | Storage, mã băm | thấp | M |
| P-11 | `$resolve-smart-health-link` (`smarthealthlinks.ts`, `routes.ts:411`) | CHƯA | grep: 0 | như P-10 | thấp | S |
| P-12 | Router công khai `/shl/{id}/manifest`, `/shl/{id}/payload` (`smarthealthlinks.ts:68`, `app.ts:290`) | CHƯA | grep `/shl/`: 0; endpoint công khai, cần rà bảo mật riêng | Storage | thấp | M |

### Nhóm L. Lịch hẹn (bộ Scheduling, đánh dấu `@experimental` ở `find.ts`)

Tất cả dùng chung `operations/utils/scheduling.ts` (1088 dòng), `scheduling-parameters.ts` (529), `recurrence.ts` (206), `alignment.ts` (156); chưa đọc thân hàm. Cỡ L của cả nhóm nằm ở phần dùng chung, từng operation chỉ là S đến M khi phần chung có sẵn.

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| L-01 | `Appointment/$find` (`find.ts:361-429`, `routes.ts:437`) | CHƯA | grep `$find`: 0 | `Schedule`, `Slot` | cao nếu có đặt lịch | L |
| L-02 | `Appointment/$book` (`book.ts:41-54`) | CHƯA | grep `$book`: 0 | như L-01 | cao nếu có đặt lịch | M |
| L-03 | `Appointment/$hold` (`hold.ts:41-61`) | CHƯA | grep `$hold`: 0 | như L-01 | vừa | S |
| L-04 | `Appointment/{id}/$cancel` (`cancel.ts:39-73`) | CHƯA | grep `$cancel` (ngoài hủy export): 0 | như L-01 | vừa | S |
| L-05 | `Appointment/{id}/$confirm` (`confirm.ts:201-240`) | CHƯA | grep `$confirm`: 0 | như L-01 | vừa | S |
| L-06 | `Appointment/{id}/$reschedule` (`reschedule.ts:105-304`) | CHƯA | grep `$reschedule`: 0 | như L-01 | vừa | M |

### Nhóm F. Thanh toán, bảo hiểm

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| F-01 | `Claim/$submit`, `Claim/{id}/$submit`: chuyển tới `OperationDefinition` tùy biến (Bot) theo cài đặt project (`claimsubmit.ts:59-86`, `utils/submit.ts:1-51`) | CHƯA | grep `$submit`: 0 | Bot hoặc dịch vụ clearinghouse ngoài (M1) | vừa | M |
| F-02 | `Claim/$export` PDF mẫu CMS-1500 (`claimexport.ts:114-156`, `utils/cms1500pdf.ts:1-462`, ảnh nền `cms1500.png.ts`) | CHƯA | grep: 0 | thư viện PDF, Storage | thấp (mẫu của Mỹ, phán đoán) | M |
| F-03 | `CoverageEligibilityRequest/$submit` (`coverageeligibilityrequestsubmit.ts:59-90`) | CHƯA | grep `$submit`: 0 | Bot hoặc dịch vụ bảo hiểm ngoài | vừa | M |
| F-04 | `ChargeItemDefinition/{id}/$apply` (`chargeitemdefinitionapply.ts:25-128`) | CHƯA | grep `$apply`: 0 | — | vừa | M |

### Nhóm G. Bot, Agent, AI, dịch vụ đám mây

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| G-01 | `Bot/$execute` (GET, POST, kèm đường dẫn phụ) (`execute.ts:57-173`, `routes.ts:201-214`) | CHƯA | grep `$execute`: 0 | Bot runtime (M1) | cao nếu dùng Bot | L |
| G-02 | `Bot/{id}/$deploy` (Lambda hoặc Fission) (`deploy.ts:30-172`, `cloud/aws/deploy.ts` 388, `cloud/fission/deploy`) | CHƯA | grep `$deploy`: 0 | Bot runtime (M1) | cao nếu dùng Bot | L |
| G-03 | `Bot/$init` tạo Bot, membership, mã mẫu (`botinit.ts:58-188`) | CHƯA | grep `$init`: 0; kiểu `Bot` có (`pkg/fhir/resource_bot.go`) | Bot, auth (membership) | vừa | M |
| G-04 | `Agent/$status` (`agentstatus.ts:37-91`) | CHƯA | grep `Agent` trong route: 0 (kiểu `Agent` có, `pkg/fhir/resource_agent.go`) | Agent, Redis pub/sub | cao nếu dùng Agent | M |
| G-05 | `Agent/$bulk-status` (`agentbulkstatus.ts:32-49`) | CHƯA | như G-04 | Agent | thấp | S |
| G-06 | `Agent/$reload-config` (`agentreloadconfig.ts:29-35`) | CHƯA | như G-04 | Agent | thấp | S |
| G-07 | `Agent/$upgrade` (`agentupgrade.ts:38-68`) | CHƯA | như G-04 | Agent | thấp | S |
| G-08 | `Agent/$fetch-logs` (`agentfetchlogs.ts:34-73`) | CHƯA | như G-04 | Agent | thấp | S |
| G-09 | `Agent/$stats` (`agentstats.ts:29-47`) | CHƯA | như G-04 | Agent | thấp | S |
| G-10 | `Agent/$push` (GET không có; POST, đầu ra HL7 hoặc DICOM) (`agentpush.ts:33-136`, `routes.ts:198-199`) | CHƯA | grep `$push`: 0 | Agent, HL7, DICOM | cao nếu dùng Agent | M |
| G-11 | `POST /$ai` (hỗ trợ stream) (`ai.ts:87-311`, `ai/openai.ts` 652 dòng) | CHƯA | grep `$ai`: 0 | AI (M4) | vừa | M |
| G-12 | `$aws-textract` (`routes.ts:434`, `cloud/aws/textract.ts` 181) | KHÔNG ÁP DỤNG | gắn với AWS Textract; `modules-survey.md` đã chọn bỏ AWS SDK. Đề xuất, xem Q-F9 | AWS | — | — |
| G-13 | `Binary/{id}/$scan` quét mã độc bằng GuardDuty (`binary-scan.ts:27-184`, `routes.ts:344`) | KHÔNG ÁP DỤNG | gắn với AWS GuardDuty và S3; nếu cần quét tệp thì chọn công cụ khác (đề xuất, Q-F9) | AWS | — | — |
| G-14 | `Binary/{id}/$presigned-url` (`binary-presigned-url.ts:27-39`, `routes.ts:341`) | CHƯA | grep `presigned`: 0 | Storage | vừa | S |

### Nhóm W. Subscription và WebSocket

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| W-01 | Giao Subscription rest-hook: HMAC `X-Signature`, header `X-Medplum-*`, retry, chống SSRF (`workers/subscription.ts:289-319,433-445,945-962,1078`) | MỘT PHẦN | `pkg/fhirsub/manager.go`, `deliver.go`, `ssrf.go`, `fhirsub_test.go`, `ssrf_test.go`. Thiếu: endpoint `Bot/{id}` (`subscription.ts:775`), extension `subscription-max-attempts` (`:1078`), bộ theo dõi lỗi kéo dài (`workers/subscription-failure-tracker.ts`); criteria chained hoặc `_has` bị bỏ qua (`go-port-status.md` 10a); hàng đợi retry nằm trong bộ nhớ | Bot cho `Bot/{id}` | cao | M |
| W-02 | Subscription websocket (R4 backport) và `GET .../$get-ws-binding-token` (`operations/getwsbindingtoken.ts:159-231`, `routes.ts:422`, `ws/subscriptions-r4`) | CÓ | `pkg/fhirsub/ws.go`, `ws_test.go`, `apps/fhir-server/internal/bootstrap/ws_e2e_test.go`; `container.go:175-178` (GET và POST). Giới hạn đã ghi: không bền, không replay, token dùng lại được tới hết hạn (`go-port-status.md` 15a) | Redis (nhiều instance) | cao | — |
| W-03 | `GET /$get-ws-sub-stats` (`getwssubstats.ts:69-140`, `routes.ts:486`) | KHÔNG ÁP DỤNG | thống kê tập "đang hoạt động" trong Redis của Medplum; Go có `Hub.Stats()` (`ws.go:122`) cho vận hành | — | — | — |
| W-04 | `GET /$get-ws-sub-project-stats` (`getwssubprojectstats.ts:62-137`, `routes.ts:487`) | KHÔNG ÁP DỤNG | như W-03 | — | — | — |
| W-05 | `POST /$clear-all-ws-subs` (`clearallwssubs.ts:51-200`, `routes.ts:488`) | KHÔNG ÁP DỤNG | dọn tập Redis riêng của Medplum | — | — | — |

### Nhóm Q. Quản trị project, người dùng, client

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| Q-01 | `Project/$init` (`projectinit.ts:65-209`, `routes.ts:262`) | CHƯA | grep `$init`: 0; `auth` có CLI `seed project` (`apps/auth/docs/plans/README.md`, bước 7.0) | auth | vừa | M |
| Q-02 | `Project/{id}/$clone` (`projectclone.ts:18-175`, `routes.ts:252`) | CHƯA | grep `$clone`: 0 | auth, Storage | thấp | M |
| Q-03 | `Project/{id}/$archive` (`projectarchive.ts:54-209`, `routes.ts:255`) | KHÔNG ÁP DỤNG cho lưu trữ dữ liệu (bản MỀM xem Q-04) | dùng `workers/bulk-export`, kho đối tượng và CSDL Medplum; sao lưu của Go là sao lưu PostgreSQL (`docs/runbook.md`, `go-port-status.md` 22a, chưa diễn tập). Đề xuất, xem Q-F14 | — | — | — |
| Q-04 | `POST /$restore` (`projectrestore.ts:35-149`, `routes.ts:256`) | KHÔNG ÁP DỤNG cho lưu trữ dữ liệu; bản MỀM đã làm ở `auth` 2026-11-25 (`POST /admin/super/projects/{id}/restore` đặt `active=true`) | như Q-03 | — | — | — |
| Q-05 | `Project/{id}/$rate-limits` (`project-rate-limits.ts:54-198`, `routes.ts:259`) | **XONG 2026-11-25** (`GET /fhir/R4/Project/{id}/$rate-limits`, Parameters tự thiết kế) | phụ thuộc H-02 (chưa có hạn mức) | H-02 | thấp | S |
| Q-06 | `User/{id}/$update-email` (`update-user-email.ts:90-239`, `routes.ts:265`) | XONG ở `auth` 2026-11-25 (route khác đường dẫn, theo membership) | `POST /admin/projects/{projectId}/members/{membershipId}/update-email` | auth, Mailer | thấp | M |
| Q-07 | `User/{id}/$rescope` (`rescope.ts:43-237`, `routes.ts:268`) | XONG ở `auth` 2026-11-25 (route khác đường dẫn; chỉ server → project, điều kiện là thiết kế của port) | `POST /admin/super/users/{userId}/rescope` | auth | thấp | M |
| Q-08 | `ClientApplication/{id}/$rotate-secret` (`rotatesecret.ts:38-78`, `routes.ts:431`) | CHƯA | grep `rotate-secret`: 0 | auth | vừa | S |
| Q-09 | `ClientApplication/{id}/$smart-launch` tạo `SmartAppLaunch`, chuyển hướng (`launch.ts:28-59`, `routes.ts:428`) | **XONG ở `auth` 2026-11-25** (`POST /admin/projects/{projectId}/client/{clientId}/smart-launch`, trả JSON thay vì chuyển hướng) | grep `smart-launch`: 0; kiểu `SmartAppLaunch` có (`pkg/fhir/resource_smartapplaunch.go`) | auth P3 | vừa | S |

### Nhóm D. Vận hành CSDL của Medplum (đề xuất KHÔNG ÁP DỤNG, xem mục 5)

| ID | Tính năng (Medplum) | Go | Bằng chứng Go | Phụ thuộc | Giá trị | Cỡ |
|---|---|---|---|---|---|---|
| D-01 | `POST /$db-stats` (`dbstats.ts:23-93`, `routes.ts:482`) | KHÔNG ÁP DỤNG | thống kê PostgreSQL của Medplum; Go có `/readyz` kiểm DB (`httpapi.go:53-61`) | PostgreSQL | — | — |
| D-02 | `GET /$db-index-bloat` (`db-index-bloat.ts:81-305`) | KHÔNG ÁP DỤNG | bloat chỉ mục theo lược đồ Medplum | PostgreSQL | — | — |
| D-03 | `POST /$db-schema-diff` (`dbschemadiff.ts:23-41`) | KHÔNG ÁP DỤNG | so lược đồ với migration Medplum; Go dùng goose (`pkg/fhirstore/migrations`) | — | — | — |
| D-04 | `POST /$db-invalid-indexes` (`dbinvalidindexes.ts:22-121`) | KHÔNG ÁP DỤNG | như D-02 | PostgreSQL | — | — |
| D-05 | `GET /$db-indexes` (`dbindexes.ts:48-131`) | KHÔNG ÁP DỤNG | như D-02 | PostgreSQL | — | — |
| D-06 | `POST /$db-configure-indexes` (`db-configure-indexes.ts:56-227`) | KHÔNG ÁP DỤNG | chỉnh chỉ mục GIN của Medplum | PostgreSQL | — | — |
| D-07 | `GET /$db-column-statistics` (`db-column-statistics.ts:53-194`) | KHÔNG ÁP DỤNG | như D-02 | PostgreSQL | — | — |
| D-08 | `POST /$db-configure-column-statistics` (`db-configure-column-statistics.ts:26-87`) | KHÔNG ÁP DỤNG | như D-02 | PostgreSQL | — | — |
| D-09 | `POST /$explain` (`explain.ts:39-134`, `routes.ts:489`) | KHÔNG ÁP DỤNG | EXPLAIN truy vấn Medplum | PostgreSQL | — | — |

## 3. Tổng hợp

### 3.1 Số đếm theo nhóm

Tổng **204 mục**. Số đếm tính tự động từ các dòng của mục 2 (mỗi dòng đúng một trạng thái).

| Nhóm | Nội dung | Số mục | CÓ | MỘT PHẦN | CHƯA | KHÔNG ÁP DỤNG |
|---|---|---|---|---|---|---|
| R | REST, định dạng, tiện ích HTTP | 24 | 8 | 3 | 13 | 0 |
| S | Search | 26 | 15 | 7 | 3 | 1 |
| B | Batch, transaction, patch, history, điều kiện | 16 | 8 | 1 | 5 | 2 |
| J | Bulk data, job nền, bảo trì dữ liệu | 17 | 4 | 7 | 6 | 0 |
| A | Quyền truy cập, SMART, AccessPolicy | 16 | 1 | 6 | 8 | 1 |
| V | Kiểm tra dữ liệu và định nghĩa | 17 | 3 | 2 | 10 | 2 |
| H | Hạ tầng lớp `fhir/` | 18 | 3 | 3 | 7 | 5 |
| T | Terminology và định nghĩa | 11 | 2 | 4 | 5 | 0 |
| P | Operation lâm sàng | 12 | 0 | 1 | 11 | 0 |
| L | Lịch hẹn | 6 | 0 | 0 | 6 | 0 |
| F | Thanh toán, bảo hiểm | 4 | 0 | 0 | 4 | 0 |
| G | Bot, Agent, AI, đám mây | 14 | 0 | 0 | 12 | 2 |
| W | Subscription, WebSocket | 5 | 1 | 1 | 0 | 3 |
| Q | Quản trị project, user, client | 9 | 0 | 0 | 7 | 2 |
| D | Vận hành CSDL Medplum | 9 | 0 | 0 | 0 | 9 |
| **Tổng** | | **204** | **45** | **35** | **97** | **27** |

Ghi chú về cách đếm: (1) mỗi **operation** là một dòng, không đếm theo method và đường dẫn (ví dụ `ValueSet/$expand` là một dòng cho cả bốn route GET/POST, có và không có `{id}`); `routes.ts` có 113 lời gọi `router.add`, 9 lời gọi `protectedRoutes.get/post/use` và 4 route công khai, `apps/fhir-server/docs/api-contract/fhir-operations.md` liệt kê 115 cặp method-đường dẫn. (2) Các dòng `CÓ` của nhóm S, B, R là tính năng lõi đã có từ giai đoạn 1 đến 30 của `docs/go-port-status.md`. (3) Mục `KHÔNG ÁP DỤNG` là **đề xuất của người kiểm kê**, chưa được duyệt (xem mục 5 và câu hỏi mở).

Điều tóm lược đáng đọc: về **tính năng lõi** (CRUD, search, chained, `_has`, `_include`, `_filter`, composite, batch, transaction, patch, history, conditional, bulk export, job bền, subscription rest-hook và websocket, GraphQL đọc, terminology cơ bản) Go đã phủ gần đủ phần cốt lõi của Medplum; phần **chưa có** tập trung ở năm khối: (a) operation nghiệp vụ (P, L, F, Q), (b) khối chạy mã và thiết bị (G: Bot, Agent), (c) kiểm tra dữ liệu mức profile và terminology (V), (d) kiểm soát truy cập mịn (A), (e) hạ tầng vận hành (H-01 AuditEvent, H-02 hạn mức, R-21 header bảo mật/CORS, R-14 đến R-16 Binary ngoài CSDL).

### 3.2 Chỗ Go ĐÃ có nhưng lệch hành vi so với Medplum

Mỗi mục có bằng chứng ở hai phía; "probe" là kết quả mục 1.3.

| # | Lệch | Medplum | Go | Hệ quả (phán đoán) |
|---|---|---|---|---|
| Lệch-1 | `PUT` với nội dung không đổi vẫn tạo phiên bản mới | trả bản hiện có, không ghi (`repo.ts:1189`, `isNotModified` `repo.ts:1359-1370`) | luôn ghi và thêm lịch sử (`pkg/fhirstore/crud.go:142-181`); probe: 3 phiên bản sau 2 lần PUT trùng | lịch sử phình, sự kiện Subscription thừa; client dựa vào `versionId` ổn định bị ảnh hưởng |
| Lệch-2 | `DELETE` | 200 kèm OperationOutcome; `If-Match` sai -> 412 (`repo.ts:1636-1645`, `fhirrouter.ts:277-285`) | 204; `If-Match` bị bỏ qua (`handlers.go:372-381`; probe: 204) | mất bảo vệ ghi đồng thời cho xóa; mã trạng thái khác |
| Lệch-3 | History bỏ `_count`, `_offset` | có phân trang (`fhirrouter.ts:217-224`) | trả toàn bộ (`handlers.go:466-503`; probe) | resource nhiều phiên bản trả bundle rất lớn |
| Lệch-4 | `identifier:of-type` **kết quả sai im lặng** | khớp theo kiểu (`core/src/search/search.ts:82,110`) | parser nhận (`request.go:242`), không xử lý (`match.go`, `search.go`); probe: rỗng dù có dữ liệu | truy vấn trả thiếu mà không báo lỗi |
| Lệch-5 | `_summary=true/text/data` bị bỏ qua | thu gọn resource (`search.ts:482-503`) | chỉ `count` (`handlers.go:510-512`) | nhẹ; client nhận dữ liệu dư |
| Lệch-6 | SMART v2 gộp quyền | từng chữ `c r u d s` tương ứng interaction riêng (`smart.ts:266-272,318-325`) | `c u d` -> Write, `r s` -> Read (`pkg/fhirauth/grants.go:74-77`) | **bảo mật**: scope chỉ `create` (`system/Observation.c`) cấp luôn sửa và xóa; scope `s` cấp luôn đọc theo id; test hiện có chỉ khẳng định `rs` là đọc và `cud` là ghi (`pkg/fhirauth/grants_test.go:34-37`), không có test phân biệt từng chữ cái |
| Lệch-7 | Mã trạng thái của `GET /job/{id}/status` | trạng thái cuối (`completed`, `error`, `cancelled`) đều 200 kèm AsyncJob (`job.ts:23-35`) | `error` -> 500, `cancelled` -> 410 (`jobs.go:238-255`) | khác với client Medplum; CLI `apps/fhir-cli/internal/cli/bulk.go:161-180` coi mọi mã khác 202 là kết thúc, cần kiểm trước khi đổi |
| Lệch-8 | Bulk export: URL và mã trạng thái | `GET /bulkdata/export/{id}`; lỗi 400, hủy 404; `requiresAccessToken:false` với URL ký (`bulkdata.ts:33-61`); không bắt buộc `Prefer: respond-async`; có `POST` và `deleted` (`export.ts:68-82,200-300`) | `bulkdata/{id}/status` (`bulk.go:53`); lỗi 500, hủy 410; `requiresAccessToken:true` (`bulk.go:338`); `Prefer` bắt buộc (`bulk.go:60`); `POST` 405 (probe) | client bulk-data chuẩn gọi `POST` kèm `Parameters` sẽ thất bại |
| Lệch-9 | Phản hồi lộ `meta.project` | ẩn `author, project, account, accounts, compartment, deleted` (`repo.ts:2658-2665`) | trả `meta.project` (`jwt_e2e_test.go:78`) | lộ id project nội bộ; `auth` đọc `Meta.Project` trực tiếp từ kho (`apps/auth/internal/application/lookup.go:37`) nên việc ẩn ở tầng REST không ảnh hưởng nó |
| Lệch-10 | Batch `Prefer: respond-async` | 202 và chạy nền, hoặc 400 nếu project thiếu feature (`async-batch.ts:21-28`) | bỏ qua, chạy đồng bộ (probe) | bundle lớn giữ kết nối lâu |
| Lệch-11 | Entry `PATCH` trong Bundle (`Binary` hoặc `Parameters`) | hỗ trợ (`batch.ts:802-880`) | entry lỗi 400 (probe; `batch.go:129-146`) | ghi patch qua batch/transaction chuẩn FHIR thất bại |
| Lệch-12 | Tài liệu nói khác mã | | `apps/fhir-server/docs/api-contract/fhir-server-go.md` ghi `$validate` sai trả 200 (bảng và ví dụ), nhưng mã trả 400 (`operation.go:150-152`; probe). `apps/fhir-server/README.md` mục "Giới hạn hiện tại" còn ghi "Chưa có: Group/$export, `$subsumes`, `$translate`" trong khi đã cài ở giai đoạn 14a, và mục JWT ghi "chưa có scope theo từng loại resource hay AccessPolicy" trong khi đã cài ở 11a, 11b. `docs/go-port-status.md` giai đoạn 6a còn câu "100 trong số 113 operation chưa port" | người đọc tài liệu kết luận sai về hành vi |
| Lệch-13 | CapabilityStatement | liệt kê tham số, có `security` (`metadata.ts:204-330`) | bỏ tham số composite dù đã cài (`capability.go:47-49`), `updateCreate` luôn false (`capability.go:31`) | client dựa vào capability bỏ qua composite |
| Lệch-14 | Header `Location` của create | `.../Type/id` (`response.ts:32-34`) | `.../Type/id/_history/vid` (`handlers.go:30`; `suite.go:141`) | hợp lệ theo FHIR nhưng khác Medplum |
| Lệch-15 | `_total=estimate` | ước lượng có ngưỡng (`search.ts:733-860`) | luôn đếm chính xác (`pkg/fhirstore/search.go:31-36`) | đúng nhưng chậm với bảng lớn |
| Lệch-16 | Terminology bỏ qua tham số | `excludeNotForUI`, `includeDesignations`, `displayLanguage` (`expand.ts:50-60`) | không đọc (`terminology.go:544-596`) | thiếu thông tin trong expansion |

### 3.3 Chỗ Go **vượt** Medplum hoặc làm khác theo hướng chủ động (để không bị "sửa về" nhầm khi sửa lệch)

- Composite search (S-14): Medplum không hỗ trợ (`row-builder.ts:304-306`).
- Conditional read `If-None-Match`/`If-Modified-Since` (R-06): Medplum khai `not-supported` (`metadata.ts:114`).
- Transaction luôn nguyên tử (B-02): Medplum chỉ khi project có feature `transaction-bundles`.
- Job nền bền: lease, khôi phục job mồ côi giữa replica, chữ ký quyền của người gọi trên job (`pkg/fhirauth/seal.go`, `jobs_maint.go`).
- `POST /{type}/$reindex` chạy nền qua REST cho admin (J-11); `GET` và `application/graphql` cho `$graphql` (R-18); `Prefer: return=OperationOutcome` (R-10).
- Super-admin xuyên project kèm nhật ký (`httpapi/auth.go:189`), CLI `assign-project` (`pkg/fhirauth/assign.go`).
- Update-as-create bật bằng cờ, cho mọi người ghi (R-09) thay vì chỉ super admin.

## 4. Đề xuất các gói công việc (work packages)

Phạm vi: 132 mục `CHƯA` và `MỘT PHẦN` (97 + 35), mỗi mục thuộc **đúng một** gói (đã kiểm bằng script: không trùng, không sót). Cỡ là cỡ thô của cả gói theo phán đoán (cộng cỡ từng mục không phải cách tính chính xác); chưa quy ra ngày công vì chưa có số đo năng suất. "Chặn bởi" là quyết định chưa chốt (theo `coverage-matrix.md` mục 3: M1 runtime Bot, M2 chuyển dữ liệu từ DB Medplum, M3 phạm vi Agent, M4 nhà cung cấp AI, M5 lưu trữ đối tượng) hoặc câu hỏi mở ở mục 6.

### 4.1 Bảng tổng

| Gói | Tên | Mục thuộc gói | Số mục | Phụ thuộc | Cỡ | Chặn bởi |
|---|---|---|---|---|---|---|
| WP-1 | Sửa lệch hành vi REST và search | R-01 R-02 R-03 R-08 R-11 R-22 S-04 S-07 S-17 S-18 S-19 S-21 B-04 B-07 B-08 B-14 J-01 J-02 J-03 J-04 J-06 J-07 V-08 | 23 | — | M | Q-F1 (mức tương thích), Q-F21, Q-F22 |
| WP-2a | Sửa SMART v2 gộp quyền | A-09 | 1 | — | S | **ĐÃ LÀM** (Q-F7 được duyệt) |
| WP-2b | AccessPolicy trong server và kiểm soát mịn | A-01 A-03 A-04 A-05 A-07 A-08 A-12 A-13 A-14 H-05 H-07 R-12 V-09 | 13 | auth (P3, P7) | L | Q-F6 |
| WP-2c | SMART mở rộng: scope có tham số, `patient/`, compartment | A-06 A-10 A-11 | 3 | auth P3 (launch), WP-2b | L | Q-F6 |
| WP-3 | Vận hành trước khi mở Internet | R-20 R-21 H-01 H-02 H-03 H-13 Q-05 V-11 | 8 | Redis, `pkg/httpsec` (đã có) | M | Q-F10, Q-F11, Q-F16 |
| WP-4 | Storage và Binary | R-14 R-15 R-16 G-14 J-09 J-12 J-15 T-11 Q-02 | 9 | `pkg/binarystore`, `pkg/safehttp` | L | M5 (Q-F8) |
| WP-5 | Kiểm tra dữ liệu mức profile và định nghĩa | V-03 V-04 V-05 V-06 V-07 V-12 V-13 V-17 B-03 T-10 H-11 | 11 | WP-6 cho V-05; dữ liệu `definitions` | L | Q-F2, Q-F19 |
| WP-6 | Terminology đầy đủ | T-01 T-02 T-03 T-04 T-07 T-08 T-09 | 7 | kho mã lớn | L | Q-F17 |
| WP-7 | Async và job | B-05 J-13 J-14 J-16 | 4 | hàng đợi (`pkg/workflow` có asynq, Temporal), WP-4 cho checkpoint | M | Q-F18 |
| WP-8 | Search nâng cao, GraphQL, tiện ích dữ liệu | S-05 S-13 S-16 S-24 R-17 R-18 J-17 | 7 | — | L | Q-F2 |
| WP-9 | Operation lâm sàng | P-01 đến P-12 | 12 | `pkg/fhirpath` (P-07), thư viện C-CDA (P-04), WP-4 (P-10 đến P-12) | L | Q-F3 |
| WP-10 | Lịch hẹn | L-01 đến L-06 | 6 | — | L | Q-F4 |
| WP-11 | Thanh toán và bảo hiểm | F-01 F-02 F-03 F-04 | 4 | Bot hoặc dịch vụ ngoài; WP-4 cho F-02 | M | **M1**, Q-F5 |
| WP-12 | Bot, operation tùy biến, pre-commit | G-01 G-02 G-03 R-19 H-04 H-06 W-01 V-10 | 8 | runtime Bot | L | **M1** (Q-F12) |
| WP-13 | Agent | G-04 đến G-10 | 7 | module Agent, Redis pub/sub, `ws/agent` | L | **M3** (Q-F13) |
| WP-14 | AI | G-11 | 1 | nhà cung cấp AI | M | **M4** (Q-F15) |
| WP-15 | Quản trị project, user, client (thuộc `auth`) | Q-01 Q-06 Q-07 Q-08 Q-09 R-04 H-12 A-15 | 8 | `auth` P3 và P7, `pkg/mailer` | L | `auth` P3 chưa cài |
| (ngoài kiểm kê) | Chuyển dữ liệu từ DB Medplum | không có mục trong bảng; liên quan Q-03, Q-04 (đề xuất không áp dụng) | 0 | ánh xạ bảng, băm mật khẩu, resource, binary | L | **M2** (Q-F14) |

### 4.2 Chi tiết từng gói

**WP-1 Sửa lệch hành vi REST và search (cỡ M, làm đầu tiên).** Gồm các lệch đã xác nhận ở mục 3.2 (Lệch-1 đến Lệch-5, 7, 8, 9, 11, 13) và các bổ sung nhỏ (`$versions`, `_pretty`, `:present`, `_summary`, `_fields`, `_deleted`/`_compartment`, `POST $export`). Rủi ro: (1) đổi hành vi đang có: B-14 (không ghi khi không đổi) đổi số phiên bản và có thể đổi việc phát sự kiện Subscription, cần quyết định rõ (Q-F21); R-22 (ẩn `meta.project`) làm hỏng test `jwt_e2e_test.go:78` và có thể client nội bộ đang đọc `meta.project` qua REST (chưa kiểm hết); J-04, J-06 đổi mã trạng thái, phải kiểm `apps/fhir-cli/internal/cli/bulk.go:161-180` và `apps/fhir-cli` trước. (2) S-04 (`:of-type`) nếu chưa kịp cài đầy đủ thì ít nhất nên trả 400 thay vì kết quả rỗng sai (S-nhỏ, an toàn). (3) Mục J-01 đến J-03 (POST, `Parameters`, `deleted`) là phần M của gói. Không cần quyết định bên ngoài ngoài Q-F1.

**WP-2a Sửa SMART v2 gộp quyền (cỡ S, làm trước mọi thứ vì là rủi ro bảo mật).** Mở rộng `Grants` từ hai bit (Read, Write) lên năm interaction (`create`, `read`, `update`, `delete`, `search`; `vread`, `history` đi cùng `read` như `smart.ts:266-268`) và sửa chỗ dùng: `pkg/fhirauth/grants.go:55-85`, `scope.go:45-67` (`allow`), `roleFromGrants`. Rủi ro: token hiện hữu cấp `.c`/`.d`/`.s` riêng lẻ sẽ mất quyền mà trước đây vô tình có; cần thêm test cho từng chữ cái và đối chiếu test hiện có (`grants_test.go:34-37`). Nên duyệt Q-F7 trước.

**WP-2b AccessPolicy trong server (cỡ L).** `AccessPolicy` lưu thành resource, nhiều rule mỗi loại, biến `%patient`/`%profile`, `interaction`, `hiddenFields`/`readonlyFields`/`writeConstraint`, chính sách mặc định theo loại hồ sơ, `onBehalfOf`, project liên kết, `meta.author`/`compartment`/`account` (H-07, R-12), chặn xóa resource đang được membership tham chiếu (H-05). Phụ thuộc chặt vào `auth` (nguồn `ProjectMembership`, quyết định D-10 về nguồn hồ sơ trong `apps/auth/docs/plans`). Rủi ro: lõi bảo mật, ảnh hưởng hiệu năng mọi truy vấn (thêm điều kiện), dữ liệu cũ thiếu `meta.compartment` cần `$reindex`. Chặn bởi Q-F6.

**WP-2c SMART mở rộng (cỡ L).** Scope kèm tham số (`Observation.rs?category=x`), scope `patient/` với ngữ cảnh launch (`SmartAppLaunch`) và quyền theo compartment: đòi hỏi lọc compartment trong SQL và Memory (`pkg/fhirsearch`, `pkg/fhirstore`) và nhận ngữ cảnh từ token (`auth` P3). Làm sau WP-2a và WP-2b.

**WP-3 Vận hành trước khi mở Internet (cỡ M).** Gắn `pkg/httpsec` (đã có, đã dùng ở `auth`) vào `fhir-server` (R-21), hạn mức theo người dùng và project có header `RateLimit` (H-02; `modules-survey.md` 7.4 đã nêu "gói rate limit Redis có fallback bộ nhớ, cỡ M"), trần resource (H-03), AuditEvent (H-01), số đo theo interaction (H-13), `$rate-limits` (Q-05), trần Subscription websocket theo người dùng (V-11), quyết định tiền tố URL (R-20). Rủi ro: AuditEvent cho mỗi lần đọc làm tăng mạnh khối lượng ghi (Medplum cho bật/tắt bằng cấu hình); hạn mức tính điểm cần hiệu chỉnh theo tải thật (chưa có bài kiểm tải, `go-port-status.md` 22a).

**WP-4 Storage và Binary (cỡ L, là nền cho nhiều gói).** Ghi và đọc Binary thô, kho ngoài (hệ thống tệp, về sau đối tượng), viết lại URL tệp đính kèm, `$presigned-url`, file export ra kho ngoài thay vì bảng resource (J-09), dọn Binary khi `$expunge` (J-12), tải URL ngoài về Binary (J-15, cần `pkg/safehttp` trích từ `pkg/fhirsub/ssrf.go` theo `modules-survey.md` mục 4), `PackageRelease/$install` (T-11), `Project/$clone` (Q-02). Rủi ro: di trú dữ liệu Binary base64 hiện có; tương thích URL ký với client Medplum; khóa ký phải tách khỏi khóa JWT (`modules-survey.md` mục 2 `storage`). Chặn bởi M5.

**WP-5 Kiểm tra dữ liệu mức profile và định nghĩa (cỡ L).** Profile, slicing, `fixed`/`pattern`, binding terminology, đích Reference, `checkReferencesOnWrite`, tham chiếu có điều kiện, StructureDefinition/SearchParameter/ValueSet chuẩn thành resource đọc được qua REST (V-12) và `$rebuild-base-definitions` (V-17), `$expand-profile`, OperationDefinition (H-11). Rủi ro: độ chính xác so với validator của Medplum (`@medplum/core`) là khó nhất; bộ dữ liệu thật đã có (14 file) nhưng chưa có bộ dữ liệu có profile; thêm độ trễ ghi. Giá trị tùy việc có dùng ứng dụng Medplum và profile hay không (Q-F2, phán đoán).

**WP-6 Terminology đầy đủ (cỡ L).** Bổ sung tham số cho `$expand`, nạp hàng loạt `$import` cho CodeSystem và ConceptMap, bảng tra mã cho CodeSystem lớn và `:above/:below` theo thuộc tính. Rủi ro: giấy phép SNOMED CT/LOINC, bộ nhớ (hiện tối đa 100.000 mã, tính mỗi lần), thiết kế bảng (khác `fhir_idx_*`). Chặn bởi Q-F17.

**WP-7 Async và job (cỡ M).** Batch bất đồng bộ có checkpoint, `$set-accounts` chạy nền, `$refresh-reference-display`, `$resend`. `pkg/workflow` đã có adapter asynq và Temporal nhưng "lease chưa nối với hai adapter đó" (`go-port-status.md` 9a). Rủi ro: checkpoint cần kho đối tượng (WP-4) vì kết quả có thể hàng chục MB (`batch/checkpoint-store.ts` ghi chú này).

**WP-8 Search nâng cao, GraphQL, tiện ích dữ liệu (cỡ L).** `:identifier` và canonical, `_filter` đầy đủ, cursor, lookup tên, `$csv`, GraphQL (introspection, validate theo schema, mutation), `$graph`. Rủi ro: GraphQL mutation và introspection cần schema đầy đủ; bộ phân tích hiện tại tự viết (`gqlparse.go`, 562 dòng) có thể không đủ, thay bằng thư viện cần kiểm ràng buộc import ở `architecture_test.go` (chưa kiểm). Đây là gói tùy chọn theo nhu cầu client.

**WP-9 Operation lâm sàng (cỡ L, gồm 12 dự án nhỏ độc lập).** Mỗi operation tách riêng; thứ tự theo yêu cầu sản phẩm (Q-F3). Ghi chú phụ thuộc: P-07 dùng FHIRPath (độ phủ 679/686 ca HL7, 7 ca đã biết lỗi trong `pkg/fhirpath/testdata/known-failures.txt`), P-04 cần bản Go của thư viện C-CDA (khoảng 4000 dòng TS), P-10 đến P-12 cần Storage và có router công khai cần rà bảo mật. Độ đúng lâm sàng cần người có chuyên môn kiểm.

**WP-10 Lịch hẹn (cỡ L, không có phụ thuộc ngoài).** Sáu operation dùng chung lõi tính khung giờ, tham số lịch, lặp, căn chỉnh (khoảng 2000 dòng TS ở `utils/`). Nguồn tự đánh dấu `@experimental` ở `find.ts`, nên API có thể còn đổi ở bản Medplum sau. Rủi ro: múi giờ, quy tắc lặp, đồng thời khi giữ và đặt chỗ. Chặn bởi Q-F4.

**WP-11 Thanh toán, bảo hiểm (cỡ M).** `Claim/$submit` và `CoverageEligibilityRequest/$submit` chỉ chuyển tới một operation tùy biến theo cài đặt project (`claimsubmit.ts:30-57`), tức giá trị nằm ở dịch vụ phía sau (Bot hoặc clearinghouse), nên chặn bởi M1 và Q-F5; `ChargeItemDefinition/$apply` độc lập; `Claim/$export` tạo PDF mẫu CMS-1500 (mẫu của Mỹ).

**WP-12 Bot, operation tùy biến, pre-commit (cỡ L, bị chặn bởi M1).** `Bot/$execute`, `$deploy`, `$init`, operation tùy biến theo `OperationDefinition` (R-19), pre-commit bot, cron/dispatch, Subscription tới `Bot/{id}`, kiểm tra `Cron`. Cùng chặn với `cds` và `webhook` của `modules-survey.md`. Không thể ước lượng cho tới khi chọn phương án runtime (M1).

**WP-13 Agent (cỡ L, bị chặn bởi M3).** Bảy operation Agent và kênh `ws/agent` (module khác trong `modules-survey.md`). Cần module Agent (cầu nối HL7/DICOM) chưa có ở Go.

**WP-14 AI (cỡ M, bị chặn bởi M4).** `POST /$ai` hỗ trợ stream, dùng `ai/openai.ts` (652 dòng). Chưa chọn nhà cung cấp.

**WP-15 Quản trị project, user, client (cỡ L, thuộc `auth`).** `Project/$init`, `User/$update-email`, `$rescope`, `ClientApplication/$rotate-secret`, `$smart-launch`, `smart-configuration` và cài đặt theo project. Làm cùng `auth` P3 và P7 (P7 đang hoãn), cần `pkg/mailer` chung. Mục A-15 (Bearer đầy đủ) thực chất là `auth` P3.

### 4.3 Thứ tự đề xuất

| Thứ | Việc | Lý do |
|---|---|---|
| 1 | WP-2a, rồi WP-1 | rủi ro bảo mật thật (Lệch-6) và các lệch kết quả sai (Lệch-4); không cần quyết định lớn; chỉ cần Q-F1, Q-F7, Q-F21, Q-F22 |
| 2 | WP-3 | điều kiện trước khi mở `fhir-server` ra Internet (`modules-survey.md` 7.2, 7.4); dựa vào `pkg/httpsec` có sẵn |
| 3 | WP-4 (nền) song song WP-7, WP-5, WP-6 | mở khóa Binary, async batch, profile, thuật ngữ; WP-4 cần M5 |
| 4 | WP-15 cùng `auth` P3 và P7, rồi WP-2b, WP-2c | cần `auth` cấp token và nguồn membership |
| 5 | WP-8, WP-9, WP-10 theo yêu cầu sản phẩm | không có phụ thuộc chặn; thứ tự do giá trị nghiệp vụ (Q-F2, Q-F3, Q-F4) |
| 6 | WP-11, WP-12, WP-13, WP-14 | **bị chặn** bởi M1, M3, M4, và Q-F5; không lập kế hoạch chi tiết trước khi chốt |
| 7 | Gói chuyển dữ liệu từ DB Medplum | **bị chặn** bởi M2; nếu không có DB Medplum cần chuyển thì bỏ hẳn |

## 5. Danh sách "không áp dụng" và lý do

Tất cả là **đề xuất của người kiểm kê**, chờ duyệt (27 mục).

| ID | Mục | Lý do |
|---|---|---|
| D-01 đến D-09 | `$db-stats`, `$db-index-bloat`, `$db-schema-diff`, `$db-invalid-indexes`, `$db-indexes`, `$db-configure-indexes`, `$db-column-statistics`, `$db-configure-column-statistics`, `$explain` | vận hành PostgreSQL theo lược đồ riêng của Medplum (bảng `<Type>`, `<Type>_History`, `*_Token`...); Go dùng lược đồ `fhir_resource`, `fhir_history`, `fhir_idx_<kiểu>` và goose (`pkg/fhirstore/store.go:1-9`); tương đương vận hành là `/readyz` và `docs/runbook.md` |
| Q-03, Q-04 | `Project/$archive`, `$restore` | định dạng lưu trữ gắn `workers/bulk-export`, kho đối tượng và CSDL Medplum; sao lưu và khôi phục của Go là PostgreSQL (`docs/runbook.md`, chưa diễn tập); xem Q-F14 |
| W-03, W-04, W-05 | `$get-ws-sub-stats`, `$get-ws-sub-project-stats`, `$clear-all-ws-subs` | thao tác trên tập "đang hoạt động" trong Redis của Medplum (`getwssubstats.ts:52-69` phân tích khóa Redis); Go có `Hub.Stats()` (`pkg/fhirsub/ws.go:122`) |
| G-12 | `$aws-textract` | dịch vụ AWS; `modules-survey.md` chọn bỏ AWS SDK; xem Q-F9 |
| G-13 | `Binary/$scan` | AWS GuardDuty và S3; xem Q-F9 |
| S-26 | `token-column`, `range-column`, `tokens`, `sql`, `lookups/reference`, `row-builder` | cài đặt lưu trữ tìm kiếm của PostgreSQL Medplum; ngữ nghĩa nằm ở các dòng S-01 đến S-23 |
| B-09 | History cấp loại và hệ thống | Medplum cũng không đăng ký (`fhirrouter.ts:344-358`) |
| B-16 | `restoreResourceVersions`, `purgeResources` | chỉ phục vụ `$restore` và quản trị Medplum |
| A-16 | Tra cứu `ProjectMembership` (`projectmembership.ts`) | thuộc `auth` (`apps/auth/internal/application/membership.go`); `fhir-server` nhận claim |
| V-14 | SearchParameter, StructureDefinition do project ghi có hiệu lực lúc chạy | không tìm thấy mã tương ứng ở Medplum 5.2.18 (xem hàng V-14), nên không có gì để "đạt tương đương"; xem Q-F19 |
| V-15 | Bộ kiểm JSON-schema cũ (`strictMode=false`) | Go chỉ có một bộ kiểm |
| H-14 | `sharding.ts` | chính Medplum đánh dấu giai đoạn "future-proofing", mọi shard quy về `global` (`sharding.ts:20-33`) |
| H-15 | Định tuyến reader/writer, nhóm kết nối | tối ưu cho bản sao đọc PostgreSQL; nhắc lại nếu thêm replica đọc |
| H-16 | Cache resource Redis | tối ưu hiệu năng, không đổi hành vi; chưa có số đo |
| H-17 | `access-tracker`, `transaction-idle-tracker`, `row-builder`, `field-utils` | gắn với truy cập SQL của Medplum |
| H-18 | Resource chỉ lưu cache (`Login`...) | dữ liệu của `auth` |

## 6. Câu hỏi mở cho người duyệt

| # | Câu hỏi | Ảnh hưởng |
|---|---|---|
| Q-F1 | Mục tiêu tương thích: giữ hành vi **y hệt Medplum** (SDK, ứng dụng, client bulk-data) hay chỉ theo chuẩn FHIR R4 và hợp đồng Go? | quyết định cách xử lý Lệch-1, 2, 7, 8, 9, 14 và R-04, R-20 (WP-1) |
| Q-F2 | Có dùng ứng dụng web Medplum (`packages/app`) trên backend Go không? Tôi **chưa kiểm** app gọi những endpoint nào | nếu có: V-12, V-17 (StructureDefinition qua REST), R-17 (`$csv`), R-18 (introspection), R-04, R-21 trở thành bắt buộc |
| Q-F3 | Operation lâm sàng nào cần (P-02 đến P-12, F-04)? Xin danh sách ưu tiên | WP-9 |
| Q-F4 | Có cần bộ đặt lịch (L-01 đến L-06; nguồn đánh dấu `@experimental`)? | WP-10 |
| Q-F5 | Thanh toán, bảo hiểm: có clearinghouse hay dịch vụ bảo hiểm cụ thể không, hay xử lý bằng Bot? | WP-11 |
| Q-F6 | `AccessPolicy`: lưu thành resource trong server (project admin quản lý) hay tiếp tục chỉ qua claim do `auth` phát? Liên quan quyết định D-10 và P7 của `auth` | WP-2b, WP-2c |
| Q-F7 | Duyệt coi Lệch-6 (SMART gộp `c u d` và `r s`) là **lỗi cần sửa ngay**, kể cả khi token hiện hữu mất quyền đã cấp nhầm? | WP-2a |
| Q-F8 | Binary: chuyển sang kho ngoài? Chỉ hệ thống tệp hay S3, Azure, GCP (M5)? Có di trú Binary base64 hiện có không? Khóa ký URL lấy ở đâu (tách khỏi khóa JWT)? | WP-4 |
| Q-F9 | Có chấp nhận bỏ `$aws-textract` và `Binary/$scan`, hay cần thay (OCR, quét mã độc) bằng công cụ khác? | G-12, G-13 |
| Q-F10 | Redis bắt buộc cho hạn mức và trần resource? (Go đã dùng Redis cho broker; có cần chế độ chỉ bộ nhớ cho dev?) | WP-3 |
| Q-F11 | AuditEvent: ghi vào kho (như `saveAuditEvents`), chỉ log, hay chưa làm? Có yêu cầu tuân thủ ghi cả thao tác **đọc**? | H-01 (khối lượng ghi) |
| Q-F12 | Runtime Bot (M1): hạn chốt? Chặn WP-12, F-01, F-03, W-01 phần `Bot/{id}`, H-04, H-06, R-19 | WP-11, WP-12 |
| Q-F13 | Module Agent và cầu nối HL7/DICOM (M3) có thuộc phạm vi migration không? | WP-13 |
| Q-F14 | `$archive`, `$restore`, `$clone` có cần cho vận hành đa tenant không? Có cần chuyển dữ liệu từ DB Medplum đang chạy (M2)? | Q-02, Q-03, Q-04, gói di trú |
| Q-F15 | Nhà cung cấp AI (M4) cho `$ai`? | WP-14 |
| Q-F16 | Tiền tố URL `/projects/{id}/fhir/R4` và đường `bulkdata/export/{id}`: giữ tương thích Medplum hay bỏ? (cùng quyết định chung ở `modules-survey.md` 7.1) | R-20, Lệch-8 |
| Q-F17 | Thuật ngữ lớn (SNOMED CT, LOINC): giấy phép, cách nạp (`$import` hay nạp offline), nơi lưu | WP-6 |
| Q-F18 | Batch bất đồng bộ có cần không; nếu có thì cờ theo project hay toàn cục (Medplum dùng feature `async-batch` theo project) | B-05 (WP-7) |
| Q-F19 | SearchParameter và StructureDefinition do project tạo có hiệu lực lúc chạy: tôi **không tìm thấy** mã làm việc này trong `packages/server/src` của Medplum 5.2.18; có đường nào khác (ví dụ trong `packages/core`) mà tôi bỏ sót, và Go có cần không? | V-14 |
| Q-F20 | Ai sửa các tài liệu lệch mã (Lệch-12), và có tách thành việc riêng ngay không? | docs |
| Q-F21 | `PUT` không đổi (Lệch-1): theo Medplum thì không ghi và không phát sự kiện Subscription. Có chấp nhận đổi hành vi hiện tại (mỗi PUT tạo phiên bản) không? | B-14 |
| Q-F22 | `:of-type` (Lệch-4): trong lúc chưa cài, có đồng ý trả 400 thay vì kết quả rỗng sai không? | S-04 |

## Phụ lục A. Những điểm chưa xác nhận

- Kho SQL: các probe ở mục 1.3 chạy trên `MemoryRepository`; bộ đối chiếu Memory/SQL (`pkg/fhirstore/differential_test.go`) cho thấy hai bên cùng hành vi cho 206 truy vấn theo `go-port-status.md`, nhưng tôi không chạy lại từng probe trên SQLite và PostgreSQL.
- PostgreSQL: không chạy test PostgreSQL ở đợt này (cần `FHIRSTORE_TEST_POSTGRES_URL`).
- Ngữ nghĩa cụ thể của: khớp tên so với `lookups/humanname.ts` (S-01, S-24), tham chiếu canonical (S-05), độ sâu và `*` của `_include` (S-12), độ chính xác theo precision của date (S-08), đúng đắn từng tham số `_tag`, `_profile`, `_security`, `_source` (S-20), các ca biên của `$subsumes`, `$translate`, `$lookup`, `$validate-code` so với Medplum (T-02 đến T-06).
- Thực thi `ipAccessRule` ở `auth` (A-07): chỉ thấy bước gom rule, chưa kiểm nơi áp dụng.
- Ứng dụng Medplum (`packages/app`) gọi những endpoint nào (Q-F2): chưa kiểm.
- Mô tả hành vi của đa số operation ở nhóm P, L, F, G, Q chỉ dựa vào tên, chú thích đầu hàm, `import` và số dòng; chưa đọc thân hàm. Cỡ của các gói này là ước lượng thô.
- Số 94 route "operation" nêu trong yêu cầu không khớp trực tiếp với cách đếm của tôi (113 `router.add`, 9 lời gọi `protectedRoutes`, 4 route công khai, 115 cặp method-đường dẫn trong `fhir-operations.md`); bản kiểm kê này đếm theo operation (mục 3.1), không theo route.

## Tiến độ #19 (2026-11-24): operation lâm sàng

Tám operation của #19 đã port sang `pkg/fhirrouter` (đăng ký ở `clinical_ops.go`, `registerClinical`), mỗi cái có test đơn vị và đã chạy đột biến (mọi nhánh quan trọng đảo đi thì test đỏ):

| Operation | Tệp | Khác Medplum / giới hạn đã biết |
|---|---|---|
| `GET {type}/$csv?_fields=...` | `clinical_ops.go` | Cột là mã search parameter hoặc FHIRPath; tối đa 1000 dòng; chống CSV injection; BOM UTF-8. Loại không tồn tại: 404 (không có route) thay vì 400 |
| `POST {type}/{id}/$refresh-reference-display` | `clinical_ops.go`, `display.go` | getDisplayString port theo JSON; đi qua `fhir.Reference` theo kiểu; ghi bằng If-Match (412 khi ghi song song); Reference không đọc được giữ nguyên |
| `StructureDefinition/$expand-profile?url=` | `clinical_ops.go` | Bundle searchset, đệ quy 10 tầng, mỗi URL một lần, lấy `version` mới nhất; chấp nhận cả POST Parameters |
| `POST {type}/{id}[/_history/{vid}]/$resend` | `clinical_ops.go`, `fhirsub/repo.go`, `fhirauth/scope.go` | Chỉ admin project, đúng project; cần broker (`FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD`) nếu không 501; hỗ trợ create/update/delete |
| `{type}/{id}/$graph?graph=<name>` | `graph.go` | Link `path` (Reference, canonical) và tìm kiếm `{ref}`; chặn vòng và quá sâu (5 tầng, 1000 resource) |
| `POST Patient/$match` | `patientmatch.go` | **Không port bảng CMS Patient Match** (SSN, ITIN, MBI của Mỹ, 22 tổ hợp, nhật ký kiểm toán công bố). Quy tắc chung: ứng viên theo identifier, telecom, ngày sinh ±1 ngày với họ hoặc tên đầu; so khớp exact/fuzzy (bỏ dấu tiếng Việt, cách 1 ký tự), điểm `min((exact+0,5·fuzzy)/6, 0,9)`; certain khi identifier trùng hoặc tên+họ+ngày sinh exact cùng một trong điện thoại/email/địa chỉ; chống nhầm sinh đôi; `onlyCertainMatches`. **Ngưỡng và trọng số là quyết định của tôi, cần người có chuyên môn kiểm trước khi dùng thật** |
| `POST QuestionnaireResponse/{id}/$extract` | `extract.go` | SDC `templateExtract`, `templateExtractContext`, `templateExtractValue`, `extractAllocateId` viết trên JSON (không bộ duyệt kiểu + JSON Patch như Medplum); context `application/x-fhir-query` trả 400 rõ ràng; trần 1000 entry |
| `GET\|POST Patient/{id}/$summary` | `patientsummary.go` | IPS Bundle document theo cách chia mục của Medplum; `author` bắt buộc (router không biết hồ sơ người gọi); thiếu `start`, `end`, `profile`, `identifier`, mức loại. Độ đúng lâm sàng của cách chia mục cần người có chuyên môn rà |
