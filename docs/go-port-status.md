# Tiến độ port medplum sang Go

Cập nhật: 2026-11-21. Hướng đi hiện tại: **port toàn bộ backend sang Go**, không giữ TypeScript. Kế hoạch giữ TypeScript trong
[migration-plan.md](migration-plan.md) không còn áp dụng; `modules/`, `features/`, `diagrams/`, `api-contract/` vẫn là đặc tả nguồn.
`medplum/` chỉ là bản tham chiếu, nằm trong `.gitignore`.

| Giai đoạn | Package | Trạng thái | Kiểm chứng |
|---|---|---|---|
| 1. Kiểu FHIR | `pkg/fhir` (sinh bởi `tools/fhirgen`) | Xong | 171 resource, 41 kiểu; round-trip 70MB dữ liệu thật |
| 2a. FHIRPath | `pkg/fhirpath` | Xong | 679/686 bộ test chuẩn HL7; 1.481 biểu thức search param |
| 2b. Validator | `pkg/fhirvalidate` | Xong | 0 lỗi trên 14 file dữ liệu thật của medplum |
| 3a. Search | `pkg/fhirsearch` | Xong | string, token, reference, date, number, quantity, uri; sort; include |
| 3b. Router REST | `pkg/fhirrouter` | Xong | CRUD, history, search, conditional, patch, batch, transaction, metadata; adapter net/http; MemoryRepository |
| 4. Repository SQL | `pkg/fhirstore` | Xong | PostgreSQL 16 và SQLite; bộ test hành vi REST dùng chung (`repotest`) + 206 truy vấn đối chiếu với `MemoryRepository`, 0 khác biệt trên cả hai DB |
| 5. App `fhir-server` | `apps/fhir-server` | Xong | Echo, xác thực bearer 2 mức, readiness/graceful shutdown, migrate CLI; e2e qua HTTP + binary thật (SIGTERM, healthcheck); `make test` toàn repo xanh |
| 6a. Operation, job nền, subscription | `pkg/fhirrouter` (operation, job), `pkg/fhirsub` | Xong (lát đầu) | `$validate`, `Patient/$everything`, `$reindex` (job `AsyncJob`), `$expunge`, vai trò admin; Subscription rest-hook; test chạy trên Memory, SQLite và PostgreSQL |
| 8a. Bulk export, terminology | `pkg/fhirrouter` (bulk, terminology) | Xong (lát đầu) | `$export` (system, Patient) NDJSON qua job + Binary, `$expand`, `$lookup`, `$validate-code`; chạy trên Memory, SQLite; export tách theo project qua HTTP |
| 9a. Độ bền job nền, vòng đời export | `pkg/fhirrouter` (jobs, jobs_maint) | Xong | lease + heartbeat, khôi phục job mồ côi (kể cả nhiều replica, không chạy trùng), nhả lease khi tắt êm, hủy/xóa export, hết hạn export; test race, Memory, SQLite, app e2e khởi động lại |
| 10a. Chained search, `_has` | `pkg/fhirsearch` (parse), `pkg/fhirrouter/chain.go` | Xong | `subject:Patient.name=x`, `patient.family=x`, lồng nhiều cấp, `_has:Type:param:filter` (lồng, nhiều điều kiện); parity Memory/SQLite qua 14 truy vấn đối chiếu mới + bộ test dùng chung; tách project |
| 6b. Operation còn lại, subscription websocket | chưa | | |
| 7a. Xác thực JWT, tách dữ liệu theo project | `pkg/fhirauth` | Xong (lát đầu) | JWT HS256 và JWKS (RS256) gồm xoay khóa, alg confusion, IdP sập; bộ test hành vi REST chạy qua `Scope` trên Memory và SQLite; cô lập chéo project qua REST, batch, transaction, include, job, Subscription |
| 7b. Bot, agent, DICOM, CLI | chưa | Kế tiếp | | |

## Giới hạn đã biết (giai đoạn 1 đến 3)

- Validator: chưa có profile/slicing, fixed/pattern, binding terminology, kiểm tra đích Reference.
- Search: chưa có special, `_contained` (`_filter` có từ 21a).
- Router: GraphQL tập con từ 20a (update-as-create có từ 17a, mặc định tắt; conditional read từ 18a).
- FHIRPath: số thập phân là `float64`; `conformsTo()` và kiểm tra kiểu tĩnh chưa có (7 ca trong `pkg/fhirpath/testdata/known-failures.txt`).

## Giới hạn của giai đoạn 4

- Chưa hỗ trợ MySQL. Test PostgreSQL chỉ chạy khi đặt `FHIRSTORE_TEST_POSTGRES_URL` (mỗi test một schema riêng, tự dọn); đã chạy thật trên PostgreSQL 16.14.
- Hiệu năng (5.000 Patient, PostgreSQL): ghi ~5 ms/resource (do ghi chỉ mục); tìm theo định danh, họ, ngày, tham chiếu 1 đến 14 ms; sắp xếp theo tham số nhiều giá trị với offset 4000 mất ~377 ms (khóa sắp xếp là truy vấn con tương quan, cần cột khóa phi chuẩn hóa nếu dữ liệu lớn).
- Chỉ mục search được lập khi ghi; khi định nghĩa search parameter đổi cần job reindex (chưa có, thuộc giai đoạn 6).
- Tiền tố chuỗi dùng `substr(...)` nên chưa tận dụng được chỉ mục B-tree; cần `text_pattern_ops` hoặc cột tiền tố nếu dữ liệu lớn.
- `:missing` dựa vào có dòng chỉ mục hay không, khác `Match` ở dữ liệu lạ (giá trị FHIRPath có nhưng không phân tích được, ví dụ ngày sai định dạng).

## Giới hạn của giai đoạn 5

- Xác thực chỉ là bearer token tĩnh (READ/WRITE), chưa phải OAuth2/SMART; chưa phân tách dữ liệu theo project.
- Chưa build/chạy image Docker thật: `Dockerfile` và compose dùng đường dẫn `trivita-codebase-go/` trong context cha, trong khi thư mục hiện tại tên `trivita-codebase-v1`.
- `docker-compose.yml` có lỗi cú pháp sẵn từ trước (khối `agent-gateway` bị chèn vào giữa `depends_on` của `app`); đã sửa khi thêm `fhir-server` (bản gốc lưu ở scratchpad của phiên làm việc, không còn trong repo).

## Giới hạn của giai đoạn 6a

- Job chạy bằng `workflow.Inline` (trong process): job đang chạy mất khi process chết và AsyncJob ở trạng thái `accepted` không tự hoàn tất. Chưa có cơ chế khôi phục.
- Subscription: chỉ `rest-hook`; hàng đợi giao nằm trong bộ nhớ (retry đang hẹn mất khi process chết); chưa kiểm thử với broker Redis ở mức app (chỉ `pkg/messaging` có test Redis riêng); chưa tắt Subscription tự động khi giao lỗi kéo dài.
- `$everything` cắt ở `_count` (trần 10000) và chưa có phân trang; `$expunge` chỉ xóa một resource (chưa có `everything=true`).
- Chưa có: `$export`, terminology, `$graph`, `$summary`, `$meta*`, `$resend`, GraphQL; 100 trong số 113 operation của Medplum vẫn chưa port.

## Chạy lại sinh mã

```bash
go generate ./pkg/fhir      # cần medplum/ ở gốc repo
go test ./pkg/fhir ./pkg/fhirpath ./pkg/fhirvalidate ./pkg/fhirsearch ./pkg/fhirrouter/... ./pkg/fhirstore .
# PostgreSQL thật:
FHIRSTORE_TEST_POSTGRES_URL="postgres://user:pass@host:5432/db?sslmode=disable" go test ./pkg/fhirstore -run Postgres
```

## Giới hạn của giai đoạn 7a

- Đã có scope SMART theo loại resource (giai đoạn 11a, xem dưới). Chưa có scope `patient/` (compartment), membership/role theo người dùng trong server.
- Project lấy từ claim của identity provider; chưa có resource `Project`/`ProjectMembership`, chưa có đăng nhập, `$token`, `/oauth2/*` trong server này (dùng identity service riêng của Trivita).
- `meta.project` được tìm bằng search parameter `_project` (token, thêm vào `fhir.SearchParams`); dữ liệu ghi trước đó cần `$reindex` và được gán project mới thấy.
- `$reindex` cả loại và `$expunge` của job chạy trên repository không tách (xuyên project).
- Đọc id đã xóa của project khác trả `410` (không đủ nội dung để kiểm project).

## Giới hạn của giai đoạn 8a

- Giai đoạn 8 chưa có định nghĩa trong kế hoạch; được hiểu là phần còn lại của 6b (bulk export, terminology). Lát 8a làm hai việc này; websocket subscription và ~100 operation khác còn lại.
- Bulk export: system, Patient và Group (14a), `_typeFilter` (18a) (hủy và dọn file hết hạn đã có từ giai đoạn 9a). Phân trang bằng offset, dữ liệu ghi giữa chừng có thể lặp hoặc sót. Mỗi file tối đa ~8MB (cắt thành nhiều file).
- Export ghi file vào chính bảng resource (Binary, base64 trong JSON), không phải object storage: hợp với dữ liệu vừa, chưa tối ưu cho hàng chục GB.
- Với Patient export, resource thuộc compartment qua nhiều tham số (ví dụ `subject`, `performer`) được gộp và khử trùng.
- Terminology chỉ dùng CodeSystem/ValueSet lưu trong server; CodeSystem `content=not-present` (SNOMED, LOINC chưa nạp) trả 422. Chưa có `$subsumes`, `$translate`, `$closure`; filter chỉ is-a, descendent-of, `=`, regex. Mở rộng giới hạn 100.000 mã, tính trong bộ nhớ mỗi lần gọi (chưa cache).

## Giới hạn của giai đoạn 9a

- "Giai đoạn 9" cũng không có trong kế hoạch; mình chọn đóng các giới hạn đã ghi ở 6a và 8a (job mất khi process chết, không hủy được export, file export không bao giờ hết hạn) thay vì thêm tính năng mới. Websocket subscription và các operation còn lại chưa làm vì `pkg/messaging` (Redis streams, consumer group) không phát sự kiện tới mọi replica, cần thiết kế riêng.
- Lease dùng trường `AsyncJob.transactionTime` (AsyncJob không có extension): job chưa xong có trường này là hạn lease, khi xong là thời điểm hoàn tất. Đồng hồ các replica cần lệch nhau ít hơn `FHIR_JOB_LEASE`.
- Job phải idempotent vì có thể chạy lại từ đầu (chưa có checkpoint). Một lần chạy dở của `$export` dọn file đã ghi khi bị hủy hoặc lỗi, nhưng nếu process bị kill cứng (SIGKILL) các file đã ghi của lần đó ở lại tới khi hết hạn (không có trong manifest nên `ExpireJobs` không biết chúng).
- Bản chạy lại sau khi lease hết hạn có thể chạy song song với bản cũ nếu replica cũ chỉ bị treo (không chết) lâu hơn lease; chưa có fencing token.
- Hủy cross-replica có độ trễ tối đa `FHIR_JOB_LEASE/3` (heartbeat phát hiện trạng thái cancelled).
- Adapter asynq/Temporal có cơ chế bền vững riêng; lease ở đây là lớp bổ sung, chưa nối với hai adapter đó.

## Giới hạn của giai đoạn 10a

- "Giai đoạn 10" cũng không có trong kế hoạch; mình chọn chained search và `_has`, giới hạn search quan trọng nhất còn lại ghi từ giai đoạn 3.
- Cài đặt bằng sub-search rồi đổi thành điều kiện thường (`subject=Patient/<id>,...` hoặc `_id=...`), nên Memory và SQL không phải đổi và hành xử giống nhau. Đổi lại: mỗi sub-search tối đa 5.000 resource (vượt thì 400 yêu cầu thu hẹp), kết quả là ảnh chụp tại thời điểm tìm, và với chained qua nhiều kiểu đích (ví dụ `subject` trỏ tới Patient/Group) phải chỉ rõ `subject:Patient.x` nếu tham số `x` không có ở mọi kiểu.
- `_has` chỉ dùng với đúng một kiểu resource; chưa có chained trong `_sort`, `_include`; chưa có `:iterate` lồng chained.
- Subscription có criteria chained hoặc `_has` bị bỏ qua (ghi log), vì khớp từng resource không thể chạy sub-search.
- Repository (Memory, SQL) trả 501 nếu nhận Request chưa giải chained; mã gọi trực tiếp Repository phải dùng `fhirrouter.SearchResolved`.

## Giai đoạn 11a: scope SMART theo loại resource

`system/<Type>.read|write|*`, `user/<Type>.…` và dạng SMART v2 (`system/Observation.rs`, `.cud`, `.cruds`); `<Type>` có thể là `*`. Scope vai trò `fhir.read/write/admin` vẫn cấp quyền trên mọi loại.

- `Principal.Grants` (nil = không giới hạn). `fhirauth.Scope` kiểm ở mọi thao tác: đọc, đọc phiên bản, lịch sử, tạo, sửa, xóa, search (403 khi loại không được đọc), transaction/batch (từng entry), `_include`/`_revinclude` và search hệ thống (kết quả chỉ gồm loại đọc được).
- Cổng HTTP cho qua token chỉ có scope theo loại (RoleRead nếu chỉ đọc, RoleWrite nếu có ghi); phần còn lại do `Scope` quyết.
- Giới hạn: scope `patient/` bị bỏ qua (không cấp quyền); chained search và `_has` tới loại không đọc được trả 403; job nền mang quyền của người gọi từ 11c; `AsyncJob` đọc theo quyền của loại `AsyncJob`.

## Giai đoạn 11b: AccessPolicy từ claim token

Claim `fhir_access_policy` (đổi tên bằng `FHIR_JWT_POLICY_CLAIM`): mảng rule `{resourceType, criteria, readonly}` hoặc chuỗi `Type?query`. Cú pháp và ngữ nghĩa ở README của `fhir-server`.

- `pkg/fhirauth/policy.go` (`Policy`, `ParsePolicy`); `Scope` áp ở đọc, đọc phiên bản, lịch sử (mọi phiên bản còn nội dung phải thỏa), tạo, sửa (cả bản cũ lẫn mới), xóa, search (thêm filter của rule; search hệ thống giới hạn theo loại và lọc kết quả), `_include`/`_revinclude`, batch/transaction.
- Ngoài tiêu chí: đọc/sửa/xóa trả 404; tạo hoặc sửa thành resource không thỏa trả 403; loại không có rule trả 403.
- Giới hạn: mỗi loại một rule (chưa có OR giữa nhiều rule); tiêu chí không dùng được chained/`_has`; search hệ thống lọc sau truy vấn nên `total` và phân trang có thể lệch; `$reindex`, `$expunge` bị chặn (`$export` đã dùng được từ 11c); chưa có biến thay thế kiểu `%patient`; chính sách chỉ đến từ token, chưa có resource `AccessPolicy` lưu trong server.

## Giai đoạn 11c: job nền mang quyền của người gọi

- `pkg/fhirauth/seal.go` (`Sealer`): ký ảnh chụp quyền, `Open` kiểm và dựng lại `Principal` (`Job=true`). `fhirrouter.Options.JobSeal/JobOpen` gọi khi tạo và trước khi chạy job; áp cho mọi loại job. Khóa: `FHIR_JOB_SIGNING_KEY` (bắt buộc với JWKS).
- `$export` chạy đúng project, scope theo loại và chính sách của người gọi; loại không đọc được bị bỏ qua nếu không có `_type`. Job ghi/xóa `Binary` (file kết quả) bằng quyền riêng, không đọc được `Binary`.
- `AsyncJob` được miễn kiểm theo loại (vẫn tách project) để router tạo, đọc và hủy job của người gọi.
- Giới hạn: người dùng token giới hạn phải có quyền đọc `Binary` để tải file (cho đọc mọi `Binary` trong project); job tạo trước 11c không chạy lại được; đổi `FHIR_JOB_SIGNING_KEY` làm job đang chờ lỗi; chưa có `Project`/`ProjectMembership` lưu trong server: danh tính, thành viên và vai trò do identity service phát qua claim, nên mục này đề xuất bỏ khỏi kế hoạch trừ khi có yêu cầu quản trị tenant ngay trong server.

## Giai đoạn 11d: super-admin và gán project cho dữ liệu cũ

- Super-admin: scope `fhir.super` (bật bằng `FHIR_JWT_SUPER_ADMIN`), `Principal.Super`, header `X-Fhir-Project` chọn project; cổng HTTP thay project của Principal rồi ghi nhật ký `audit=true` cho mọi yêu cầu (`httpapi/auth.go`). Token thường không được chọn project khác (403).
- Dữ liệu cũ: `fhirauth.AssignProject` và lệnh `fhir-server assign-project -project X [-type ..] [-dry-run]`; quét hai lượt (gom id rồi gán, If-Match), idempotent, không đụng resource đã có project.
- Giới hạn: nhật ký kiểm toán chỉ là dòng log (chưa ghi `AuditEvent` vào kho, không chống sửa); super-admin vẫn chịu `AccessPolicy` nếu token có claim; `assign-project` gán một project cho toàn bộ dữ liệu chưa gán (chưa có bảng ánh xạ theo tiêu chí) và nên chạy khi không có ghi đồng thời; phải tự thu log kiểm toán.

## Giai đoạn 12a: search composite

`Observation?component-code-value-quantity=http://loinc.org|8480-6$gt130`, `code-value-quantity`, `combo-code-value-quantity`, `code-value-concept/date/string`... (49 tham số composite của R4). Các thành phần cách nhau bằng `$`, nhiều giá trị cách nhau bằng dấu phẩy (OR); mọi thành phần phải khớp trên cùng một phần tử của biểu thức cha.

- `pkg/fhirsearch/composite.go`: phân giải thành phần (qua URL định nghĩa), tách và biên dịch giá trị (lỗi cú pháp là 400 ngay khi parse), `matchComposite` cho Memory và Subscription, `ExtractComposite` để lập chỉ mục.
- `pkg/fhirstore`: không thêm bảng hay migration. Mỗi thành phần ghi vào bảng chỉ mục của kiểu thành phần với code `"<code>#<i>"` và `ord` = chỉ số phần tử; điều kiện SQL là chuỗi EXISTS lồng nhau nối theo cùng `(resource, ord)`.
- Kiểm thử: `TestCompositeMatch`, `testCompositeSearch` trong bộ dùng chung (Memory, SQLite, qua `Scope`), 44 truy vấn composite mới trong bộ đối chiếu Memory/SQL, 0 khác biệt.
- Giới hạn: không có modifier (kể cả `:missing`, `:not`), không `_sort` theo tham số composite, thành phần chỉ nhận các kiểu string/token/reference/date/number/quantity/uri; thành phần token không có `:text`. Resource ghi trước 12a chưa có chỉ mục composite trong SQL: chạy `POST [base]/Observation/$reindex` (cần quyền admin) để lập lại. Chưa chạy trên PostgreSQL thật (SQL chỉ dùng cú pháp chuẩn, dùng chung với phần search hiện có).

## Giai đoạn 13a: modifier terminology của token

`code:below=system|code` (con cháu, gồm chính nó), `code:above=system|code` (tổ tiên, gồm chính nó), `code:in=<ValueSet>` và `code:not-in=<ValueSet>` (URL chuẩn có thể kèm `|version`, hoặc `ValueSet/<id>`). Nhiều giá trị cách nhau bằng dấu phẩy là OR.

- `pkg/fhirrouter/termsearch.go`: trước khi search, mỗi điều kiện được thay bằng điều kiện token thường liệt kê các `system|code` (qua `expandMembers` của `$expand` và cây concept của CodeSystem). Memory, SQL và `Scope` không cần biết terminology; chained, `_has`, search hệ thống, conditional create/update/delete và link phân trang đều đi qua cùng đường này.
- `fhirsearch`: parse cho phép các modifier trên tham số token (`Filter.IsTerminology`, `NeedsResolution`); `Match`, Memory, SQL, Subscription và `AccessPolicy.criteria` từ chối điều kiện chưa giải (501/bỏ qua/từ chối chính sách).
- Kiểm thử: `testTokenTerminology` trong bộ dùng chung (Memory, SQLite, qua `Scope`), `TestTerminologyModifiersParseButCannotMatchDirectly`.
- Giới hạn: tối đa 5000 mã mỗi điều kiện (vượt thì 400); `:above/:below` chỉ dùng CodeSystem lưu trong server theo cây concept (chưa có quan hệ khác như `partOf`, chưa có SNOMED/LOINC ngoài); ValueSet và CodeSystem không tìm thấy trả 404, giá trị sai dạng trả 400; kết quả là ảnh chụp lúc tìm; `:in` và `:not-in` không dùng được trong Subscription criteria và AccessPolicy; `:not-in` với ValueSet rỗng bỏ điều kiện; resource không có mã vẫn khớp `:not-in`. Chưa chạy trên PostgreSQL thật.

## Giai đoạn 14a: `$subsumes`, `$translate`, `Group/$export`

- `CodeSystem/$subsumes` (`pkg/fhirrouter/terminology2.go`): `codeA`/`codeB` hoặc `codingA`/`codingB`, trả `outcome` equivalent, subsumes, subsumed-by hoặc not-subsumed theo cây concept của CodeSystem lưu trong server; mã lạ trả 404.
- `ConceptMap/$translate`: chỉ định bản đồ bằng `ConceptMap/{id}` hoặc `url` (+`conceptMapVersion`); không chỉ định thì tìm theo `source-system` (và `target-system` khi có `targetsystem`). Trả `result` và các `match` (equivalence, concept, source) xếp mạnh trước; `unmatched`/`disjoint` không tính là bản dịch; `unmapped` mode provided/fixed được áp dụng; `reverse=true` dịch ngược và đảo quan hệ wider/narrower.
- `Group/{id}/$export` (`bulk.go`): xuất Patient là thành viên đang hoạt động (bỏ `inactive`, bỏ thành viên không phải Patient, bỏ trùng) cùng resource trong compartment của họ; kiểm Group ngay khi gọi (404; 400 nếu `actual=false` hoặc quá `maxGroupMembers`=5000), job đọc lại Group khi chạy bằng quyền đã ký của người gọi. Resource Group chứa thành viên cũng được xuất vì nằm trong compartment Patient.
- Quyền: `$subsumes`, `$translate` và export Group chỉ cần quyền đọc.
- Kiểm thử: `testGroupExport`, `testSubsumesAndTranslate` (Memory, SQLite, qua `Scope`), e2e JWT (`TestGroupExportIsScopedToCallerProject`, `TestSubsumesAndTranslateNeedOnlyReadRole`).
- Giới hạn: `$translate` chỉ khớp theo mã nguồn (chưa xét `dependsOn`/`product`, chưa dịch theo `valueSet` hay nhiều coding trong CodeableConcept); `$subsumes` chỉ theo cây cha-con của một CodeSystem (không `partOf`); Group có `characteristic` mà không liệt kê thành viên không được hỗ trợ; Group export chưa có `_typeFilter`; chưa chạy trên PostgreSQL thật.

## Giai đoạn 15a: Subscription kênh websocket

Giao thức R4 "Subscriptions R5 Backport" như Medplum: `$get-ws-binding-token` → websocket `/ws/subscriptions-r4` → `bind-with-token` → `handshake`, `heartbeat`, `event-notification`. Cú pháp và cấu hình ở README của `fhir-server`.

- `pkg/messaging`: interface `Broadcaster` (`SubscribeBroadcast`): mọi instance nhận mọi event. Redis đọc stream bằng `XREAD` từ vị trí chốt trước khi `Start` trả về (không consumer group, không ACK, không đọc lại event cũ); bộ nhớ giống `Subscribe`. Handler theo nhóm cũ (rest-hook, at-least-once) giữ nguyên.
- `pkg/fhirsub/ws.go` (`Hub`): token ký HMAC-SHA256 (id Subscription, project, hạn), kết nối, bind nhiều Subscription, heartbeat và ping, giới hạn kết nối/bind/hàng đợi gửi, ngắt kết nối chậm, tắt êm (GoingAway). `Manager.AttachHub`/`RegisterBroadcast`: Subscription websocket được khớp bằng cùng logic (interaction, `end`, criteria, tách project) trên mọi instance, rồi đẩy tới kết nối cục bộ.
- `fhir-server`: `FHIR_WEBSOCKET` và các biến `FHIR_WS_*`; route `/ws/subscriptions-r4` (ngoài xác thực bearer); `$get-ws-binding-token` đi qua repository đã tách project và quyền nên chỉ người đọc được Subscription mới lấy được token; chỉ cần quyền đọc.
- Kiểm thử: Hub (luồng đầy đủ, nhiều kết nối và nhiều Subscription, token sai/hết hạn/khóa khác/Subscription đã tắt, hết hạn bind, giới hạn, heartbeat, kết nối chậm, tắt êm, tách project), hai instance dùng chung Redis (miniredis), broadcast của messaging, e2e qua HTTP thật (token tĩnh và JWT), cấu hình.
- Giới hạn: không bền và không replay (kết nối rớt thì mất thông báo trong lúc rớt; instance tắt thì bỏ lỡ event phát lúc đó); token dùng lại được tới khi hết hạn (không one-time vì không có kho trạng thái dùng chung); Subscription bị xóa/tắt không ngắt kết nối đang mở; mỗi event khớp được marshal lại cho từng kết nối; chỉ có `fhir.Subscription` criteria dạng search (không có SubscriptionTopic R5); Redis chưa kiểm thử với Redis thật (chỉ miniredis).

## Giai đoạn 16a: `fhir-cli`

App mới `apps/fhir-cli` (README riêng): `read`, `search` (`-all -ndjson`), `create`, `update`, `delete`, `import` (NDJSON, resource đơn lẻ hoặc Bundle; batch hoặc transaction; chia lô), `export` (system, patient, group; chờ job, tải file, dọn job), `reindex`. Chỉ dùng thư viện chuẩn.

- `internal/client`: bearer token, `X-Fhir-Project`, đọc OperationOutcome; link của máy chủ (phân trang, `Content-Location`) chỉ lấy path và query rồi áp vào máy chủ của client và phải nằm dưới gốc API, nên token không bao giờ tới host khác.
- Kiểm thử: lệnh chạy với `fhirrouter` + kho bộ nhớ sau lớp kiểm token (CRUD, phân trang, import NDJSON/Bundle/lỗi/transaction hoàn tác, export file và dọn job, `-keep`, group, reindex, lỗi cách dùng và mã thoát, biến môi trường), test `client`; chạy thử với binary `fhir-server` và `fhir-cli` thật trên SQLite (import, search, export, reindex bằng token admin, 404 trả mã 1).
- Giới hạn: `import` chỉ giữ `id` nguồn khi máy chủ đích bật update-as-create (giai đoạn 17a); chưa có lệnh websocket, `$validate`, terminology, quản trị project; không có tự động thử lại khi lỗi mạng.
- Cũng sửa một chỗ có thể panic trong `jobStatus` khi tham số kết quả của job lỗi không có resource.

## Giai đoạn 17a: update-as-create

`FHIR_UPDATE_AS_CREATE=true` (`fhirrouter.Options.UpdateAsCreate`): `PUT Type/id` khi chưa có resource thì tạo mới với đúng id client chọn và trả `201` (cập nhật như thường khi đã có, `200`). Cũng áp cho PUT trong batch/transaction. Mặc định tắt (404, như trước).

- `handlers.go` (`updateOrCreate`): id phải khớp `^[A-Za-z0-9\-.]{1,64}$` (400 nếu sai); resource đã xóa không được tạo lại (410); `If-Match` với id chưa có vẫn là 404; `PATCH` không bao giờ tạo; id trong body khác đường dẫn vẫn là 400.
- Tách project: id đang bị resource của project khác chiếm (người gọi không thấy) trả 404 như chưa tồn tại và không ghi đè gì, nên không lộ sự tồn tại của id; `meta.project` vẫn do server gán. Quyền ghi theo loại và `AccessPolicy` áp như tạo mới (tạo đi qua `CreateResource` của repository đã bọc).
- Kiểm thử: `TestUpdateAsCreate*` (tắt/bật, id sai, If-Match, xóa, PATCH, transaction), `TestUpdateAsCreateAcrossProjects` (Memory và SQLite), e2e HTTP, cấu hình, và `fhir-cli` chuyển dữ liệu export → import giữ nguyên id và tham chiếu.
- Giới hạn: client chọn id nên hai bên có thể tranh cùng một id (bên đến sau nhận 200 cập nhật nếu cùng project); id chọn tay phải duy nhất toàn máy chủ cho cùng một loại resource (id đã dùng ở project khác bị từ chối bằng 404); chưa chạy trên PostgreSQL thật.

## Giai đoạn 18a: `_typeFilter` cho bulk export và conditional read

- **`_typeFilter`** (`bulk.go`): `$export?_type=Patient,Observation&_typeFilter=Patient?active=true` (URL-encode giá trị). Lặp tham số, hoặc nối bằng dấu phẩy ngay trước `Type?` (dấu phẩy trong giá trị như `status=final,preliminary` được giữ nguyên). Nhiều điều kiện cùng loại là OR và được khử trùng; loại không có điều kiện xuất đủ; chỉ có `_typeFilter` thì xuất đúng các loại được nêu; loại ngoài `_type` trả 400. Điều kiện được kiểm khi gọi (400 nếu sai loại, thiếu `?`, tham số không có, hoặc tham số điều khiển như `_count`, `_sort`, `_include`) và chạy như search thường nên dùng được chained và `:in/:below`; áp cả mức system, patient và group. `fhir-cli export -filter Type?query` (lặp được).
- **Conditional read** (`handlers.go`): `GET Type/id` với `If-None-Match` (ETag yếu, danh sách, hoặc `*`) hoặc `If-Modified-Since` trả `304` không body (giữ `ETag`, `Last-Modified`) khi chưa đổi; `If-None-Match` thắng `If-Modified-Since`; ngày sai thì bỏ qua điều kiện; 304 chỉ trả sau khi đã qua kiểm quyền và tách project nên không lộ gì thêm.
- Kiểm thử: `testExportTypeFilter`, `testConditionalRead` trong bộ dùng chung (Memory, SQLite, qua `Scope`), `TestExportWithTypeFilter` của CLI.
- Giới hạn: conditional read chỉ cho `read` (chưa cho `vread`, search, và entry GET trong batch); `_typeFilter` không có điều kiện riêng theo từng loại trong mức compartment ngoài việc cộng AND vào điều kiện compartment; chưa chạy trên PostgreSQL thật.

## Giai đoạn 19a: chuẩn bị chạy test trên PostgreSQL (chưa chạy được)

Mục tiêu là chạy toàn bộ bộ test hành vi trên PostgreSQL thật, vì mọi giai đoạn từ 7 đến 18 mới chỉ chạy trên Memory và SQLite. Máy phát triển hiện không có Docker hay PostgreSQL; việc tải và chạy một PostgreSQL nhúng bị chặn bởi cơ chế phân quyền của phiên làm việc, nên **chưa có kết quả nào trên PostgreSQL**.

Đã làm sẵn để chạy được bằng một lệnh:
- `make test-pg`: dựng container `postgres:16-alpine` tạm, đặt `FHIRSTORE_TEST_POSTGRES_URL`, chạy `go test` cho `pkg/fhirstore`, `pkg/fhirauth`, `pkg/fhirrouter` rồi xóa container (cần Docker). Hoặc tự đặt biến này trỏ tới một PostgreSQL có sẵn (mỗi test tạo và xóa schema riêng) rồi chạy `go test ./pkg/fhirstore/... ./pkg/fhirauth/...`.
- `pkg/fhirauth/postgres_test.go`: khi có biến môi trường, bộ hành vi dùng chung qua `Scope` (`TestScopedParity`), cô lập chéo project (`TestProjectIsolation`) và `TestUpdateAsCreateAcrossProjects` chạy thêm trên PostgreSQL (trước đó chỉ Memory và SQLite). Bộ của `pkg/fhirstore` (hành vi dùng chung, đối chiếu Memory/SQL gồm composite, terminology, chained) đã có sẵn bản PostgreSQL.
- Phần còn lại cần chạy tay: `fhir-server` với `DATABASE_URL=postgres://...` và các test e2e của `apps/fhir-server` (hiện dùng SQLite trong bộ nhớ).

## Giai đoạn 20a: GraphQL (tập con của FHIR GraphQL, giống Medplum)

`GET|POST [base]/$graphql`; ví dụ và giới hạn ở README của `fhir-server`.

- `pkg/fhirrouter/gqlparse.go`: bộ phân tích tự viết (query rút gọn hoặc có từ khóa, biến kèm giá trị mặc định, alias, tham số gồm danh sách/đối tượng/enum, fragment có tên và inline, chú thích, chuỗi nhiều dòng); mutation/subscription bị từ chối (400); directive được đọc nhưng bỏ qua; giới hạn độ sâu 12, giá trị lồng 8, truy vấn 64KB.
- `pkg/fhirrouter/graphql.go`: gốc `Type(id)`, `TypeList`, `TypeConnection`; `Reference.resource`; `OtherList(_reference: param)` trong resource; `__typename`; lỗi theo field trong `errors` (kèm `path`) mà không làm hỏng field khác (HTTP 200); lỗi cú pháp là 400. Ngân sách 1000 resource cho cả truy vấn (danh sách bị cắt và báo lỗi), `_count` mặc định 20 tối đa 100, fragment vòng bị phát hiện.
- Mọi đọc đi qua `repoOf(ctx)` và `searchRepo` nên: tách project, scope SMART theo loại, `AccessPolicy` áp y như REST (Reference tới loại không được đọc cho `null` kèm lỗi, không lộ dữ liệu); search dùng được chained, composite và `:below`. `RequiredRole` coi `$graphql` là thao tác đọc (cả POST).
- Kiểm thử: bộ dùng chung `testGraphQL` (Memory, SQLite, qua `Scope`), parser (hợp lệ và 14 trường hợp lỗi), ngân sách, `_count`, fragment vòng, e2e JWT (tách project, scope theo loại, AccessPolicy, token đọc, 401). Bỏ stub trả 501 cũ của `$graphql`.
- Giới hạn: không có introspection (`__schema`, `__type`), không có validate theo schema (trường không có trả `null`, không báo lỗi kiểu), tham số của field thường (`fhirpath`, `format`...) báo lỗi, `@include/@skip` bị bỏ qua, chưa có `TypeConnection` với `first/after` (dùng `_count/_offset`), chưa có `Type(id)` ở mức instance (`Patient/{id}/$graphql`), truy vấn lớn chưa có timeout riêng ngoài context của request.

## Giai đoạn 21a: `_filter`

Biểu thức logic trên điều kiện search: `Patient?_filter=name co "smith" and (birthdate lt 1980-01-01 or gender eq male)`. Cú pháp và bảng toán tử ở README của `fhir-server`.

- `pkg/fhirsearch/filterexpr.go`: phân tích (`and`/`or`/`not`/ngoặc, không phân biệt hoa thường, not > and > or; giá trị trong nháy kép hoặc một từ; giới hạn 50 điều kiện và độ sâu 12), đổi mỗi điều kiện thành `Filter` thường theo kiểu tham số (nên dùng đúng bộ so khớp và chỉ mục sẵn có), `Expr.Match` cho Memory và Subscription. `Request.Exprs` (AND với nhau và với `Filters`), `Query()` giữ `_filter` trong link phân trang, `HasCriteria()` để thao tác có điều kiện coi `_filter` là tiêu chí.
- `pkg/fhirstore`: `exprSQL` ghép các lá bằng AND/OR/NOT trên SQL sẵn có (không cần chỉ mục mới); placeholder cấp theo thứ tự văn bản như các điều kiện khác.
- `AccessPolicy.criteria` từ chối `_filter` (chính sách chỉ cộng được điều kiện AND thường, nên không để biểu thức bị bỏ sót khi chạy SQL).
- Kiểm thử: bộ phân tích và đối sánh (19 biểu thức, 21 trường hợp lỗi), 26 truy vấn mới trong bộ đối chiếu Memory/SQL (0 khác biệt), bộ dùng chung `testFilterParam` (Memory, SQLite, qua `Scope`: kết hợp tham số thường, phân trang, conditional delete bằng `_filter`, lỗi).
- Giới hạn: `ew`, `ss`, `sb`, `in`, `re`, `po`; tham số composite/special; modifier và đường dẫn chained trong tham số; `_filter` ở search hệ thống; `eq` trên string là so khớp chính xác phân biệt hoa thường (không có "bằng không phân biệt hoa thường"); `not` khớp cả resource không có giá trị; chưa chạy trên PostgreSQL thật.

## Giai đoạn 22a: runbook vận hành

[runbook.md](runbook.md): kiểm tra sức khỏe, khởi động/tắt/triển khai, schema và migration, sao lưu và khôi phục, xoay khóa và bí mật, job nền bị kẹt, Subscription (rest-hook, websocket), nhiều project và super-admin, bảng sự cố thường gặp, danh sách việc còn mở trước production. Mọi tên biến, lệnh, giá trị mặc định và thông báo log trong tài liệu được đối chiếu với code hiện tại.

- Giới hạn: tài liệu, không có code mới; **chưa có mục nào được diễn tập** (không có PostgreSQL/Docker ở máy phát triển): sao lưu/khôi phục, xoay khóa, tắt êm trong Kubernetes cần chạy thử ở staging; chưa có bài kiểm tải nên chưa có số liệu dung lượng hay SLO.

## Giai đoạn 23a: xử lý lại message lỗi trên Redis (`pkg/messaging`)

Trước đây message mà handler báo lỗi (hoặc instance chết giữa chừng) nằm trong PEL của consumer group mãi mãi, không bao giờ được thử lại. Điều này ảnh hưởng Subscription (webhook), job nền và mọi consumer dùng `messaging.Redis`.

- Mỗi `ClaimInterval` (mặc định 10s), vòng consume gọi `XPENDING … IDLE` rồi `XCLAIM` các message idle ≥ `ClaimIdle` (mặc định 30s), kể cả của consumer đã chết, và chạy lại handler; thành công thì ACK.
- Sau `MaxDeliveries` lần giao (mặc định 5) mà vẫn lỗi, message được chép sang stream `{prefix}:{topic}:dead` (thêm trường `original_id`, `deliveries`) rồi ACK, nên poison message không chặn hàng đợi.
- Message đã bị xóa khỏi stream nhưng còn trong PEL được ACK để dọn.
- Test (miniredis, `-race`): thử lại đến khi thành công và không giao lại sau đó; poison vào dead-letter đúng 3 lần giao; nhận lại message của consumer đã chết.

Giới hạn: xử lý mỗi lần tối đa 20 message/topic; chưa có công cụ replay dead-letter (hiện đọc bằng `XRANGE` rồi `XADD` lại thủ công); chưa chỉnh được ba tham số qua biến môi trường (chỉ qua `RedisOptions`); chưa chạy với Redis thật (chỉ miniredis). Driver memory không có cơ chế thử lại.

## Giai đoạn 23b: rà soát nguyên tắc monorepo (mỗi `apps/*` là một ứng dụng rõ ràng)

- Chuyển `pkg/agents`, `pkg/llm`, `pkg/cache`, `pkg/storage` (không app nào dùng, thuộc các app đã gỡ) ra `../trivita-codebase-v1-removed-apps/pkg/`; `go mod tidy`.
- `architecture_test.go`: thêm quy tắc cho `apps/fhir-cli` (chỉ stdlib và chính nó); `TestEveryAppIsAClearApplication` (mỗi app có `cmd/<tên>/main.go`, `internal/`, README, quy tắc phụ thuộc; không import app khác); `TestEveryPkgHasARule` (thư viện mới phải có quy tắc, quy tắc không trỏ vào thư mục đã mất). Đã thử cố ý vi phạm để xác nhận test bắt được.
- README gốc viết lại cho khớp (trước đó vẫn mô tả 6 app đã gỡ).
- `apps/fhir-cli` thiếu `.env.example` là đúng (không đọc biến môi trường cấu hình server).

## Giai đoạn 24a: chạy thật trên PostgreSQL (lần đầu)

Chạy bằng PostgreSQL 16 nhúng (thư viện `embedded-postgres` trong chương trình tạm ngoài repo, cổng 54329), theo cho phép của người dùng. Repo không phụ thuộc thư viện này; `make test-pg` (Docker) vẫn là cách chuẩn.

**Bộ test PG** (`FHIRSTORE_TEST_POSTGRES_URL=… go test ./pkg/fhirstore/... ./pkg/fhirauth/... ./pkg/fhirrouter/... ./pkg/fhirsub/... -p 1`): tất cả PASS, không test nào skip (khoảng 360 dòng test liên quan PG). Đây là lần đầu giai đoạn 7 đến 22 chạy trên PostgreSQL.

**Smoke test binary `fhir-server` + `fhir-cli` trên PG** (60 Patient + 60 Observation):
- `migrate status/up/status` (1 → 0 migration chưa áp dụng); `/readyz` báo `database: true`.
- `import` transaction 120 resource, lỗi 0 (cần `FHIR_UPDATE_AS_CREATE=true` vì dữ liệu mang id riêng).
- search: `family` (30), chained `subject:Patient.family` (30), composite `component-code-value-quantity` (50 và 10 với hai điều kiện), `_filter` (10), `_has` (60), `_sort`/`_elements`, GraphQL `Patient(id:)`.
- version/history (2 phiên bản sau update), delete → GET 410, history giữ nguyên; conditional read 304.
- `export` system (120 resource) và `-filter Patient?gender=female` (20); `reindex` Patient/Observation xong, kết quả search không đổi.
- `assign-project -dry-run` đếm đúng 59 (Patient p2 đã xóa).

**Chưa chạy**: websocket Subscription và Redis thật; JWT/JWKS e2e và nhiều project qua HTTP trên PG (chỉ có test mức thư viện `fhirauth/postgres_test.go`); tải lớn; tắt êm khi có job đang chạy; PG nhúng là một database `postgres` mặc định với một kết nối đơn giản, không phải cấu hình production.

## Giai đoạn 25a: build và chạy thử Docker (image + compose)

- `make docker` build được `trivita-fhir-server:local`. Phát hiện: build context là thư mục cha nên `.dockerignore` trong repo vô tác dụng, `COPY trivita-codebase-v1/` sẽ kéo cả `medplum/` (730 MB, không commit) vào layer. Thêm `Dockerfile.dockerignore` (loại `medplum/`, `docs/`, `bin/`, `*_test.go`, `*.md`, `.env`, `*.db`).
- `docker compose up fhir-server postgres` (cần `apps/fhir-server/.env` có `FHIR_WRITE_TOKENS`; `--env-file` của compose KHÔNG truyền biến vào container, chỉ file `env_file` của service mới làm việc đó, nên thiếu file này container thoát với lỗi "cần ít nhất một token ghi" và restart liên tục):
  - migration tự chạy, `HEALTHCHECK` của image báo healthy, `/readyz` có `database: true`;
  - ghi không token → 401, ghi có token → 201, search thấy bản ghi, bảng `fhir_resource` trong PostgreSQL có dòng;
  - `docker compose stop` mất 5,3 giây (drain 5s) và thoát mã 0.
- Đã dọn: `down --volumes`, xóa `.env` tạm.

Chưa kiểm: nhiều replica, rolling update, image chạy trên K8s, kích thước image và quét lỗ hổng, `docker compose` với `DATABASE_AUTO_MIGRATE=false` + `migrate up` riêng.

## Giai đoạn 26a: e2e HTTP của fhir-server chạy trên PostgreSQL

- `apps/fhir-server/internal/bootstrap/pg_test.go`: có `FHIRSTORE_TEST_POSTGRES_URL` thì `settings()` của mọi test e2e dùng một schema PostgreSQL riêng (xóa khi xong, `TestMain`), thay vì SQLite trong bộ nhớ. Test tự đặt file SQLite (khởi động lại, assign-project, job mồ côi) giữ SQLite. Không có biến thì hành vi cũ.
- Kết quả (PostgreSQL 16 nhúng, `-race`): 32 test e2e PASS, gồm JWT và vai trò, tách project qua HTTP, SMART scope, access policy, super-admin có audit, job/Subscription/bulk export theo project, GraphQL theo grant, websocket, webhook, tắt êm. Cả `go test ./... -p 1` với biến này cũng xanh. Kiểm tra ngược: URL sai thì test panic (nên chắc chắn đang chạy trên PG).
- `make test-pg` nay gồm `fhirsub` và `./apps/fhir-server/...`, chạy `-p 1` (các package dùng chung một database).

Chưa kiểm: JWKS thật (e2e dùng khóa HMAC), Redis thật, nhiều replica cùng một PG, tải lớn.

## Giai đoạn 27a: Redis thật và nhiều replica dùng chung PostgreSQL + Redis

- `pkg/messaging`: các test Redis chạy được trên Redis THẬT khi đặt `MESSAGING_TEST_REDIS_ADDR` (helper `redis_env_test.go`; FLUSHDB trước và sau mỗi test, dùng instance riêng); không đặt thì vẫn miniredis. Trên Redis 7 thật toàn bộ test PASS với `-race`, gồm thử lại bằng XCLAIM, dead-letter và nhận lại message của consumer đã chết (giai đoạn 23a, trước đó chỉ chạy trên miniredis).
- `apps/fhir-server/internal/bootstrap/replicas_e2e_test.go` (cần `FHIRSTORE_TEST_POSTGRES_URL` và `FHIR_TEST_REDIS_URL`, nếu không thì skip): hai replica (hai Container, hai máy chủ HTTP) dùng chung một PostgreSQL và một Redis:
  - ghi ở replica này, đọc ở replica kia;
  - webhook của Subscription được giao đúng một lần dù có hai replica (nhóm consumer chia việc), cho cả ghi ở A và ghi ở B;
  - websocket nối vào B nhận sự kiện của thao tác ghi ở A (broadcast qua Redis), không nhận thừa;
  - job (`$reindex`) tạo ở A xem trạng thái được ở B và chạy đến `completed`.
  - Chạy 3 lần liên tiếp với `-race`: PASS.
- `TestReplicasMigrateConcurrentlyOnEmptyDatabase`: 4 replica khởi động cùng lúc trên database trống với auto-migrate: không lỗi (5 lần, `-race`).
- Toàn bộ `go test ./... -p 1 -race` với PostgreSQL + Redis thật: xanh.
- `make test-pg` nay dựng cả PostgreSQL và Redis bằng Docker và chạy thêm `pkg/messaging` cùng các test hai replica.

Chưa kiểm: tranh job giữa hai replica chạy đồng thời dưới tải (chỉ kiểm job chạy được và xem được chéo replica, chưa kiểm không chạy đúp), Redis cluster/sentinel/TLS, mất kết nối Redis giữa chừng, JWKS thật, tải lớn. Redis chạy một instance, không có persistence (AOF): Redis mất thì mất event chưa giao (dữ liệu FHIR vẫn nằm ở PostgreSQL).

## Giai đoạn 28a: tranh chấp đồng thời (khóa lạc quan, create có điều kiện) và sửa lỗi tạo trùng

Thêm hai test tranh chấp vào bộ test dùng chung `repotest` (chạy trên Memory, SQLite, PostgreSQL, và qua `Scope`):
- **`ConcurrentIfMatch`**: 12 bên cùng `PUT` với cùng `If-Match` → đúng 1 bên thắng (200), 11 bên nhận 412, history có đúng 2 phiên bản. Đây là nền tảng của lease job (`claimJob` dùng `IfMatch`) nên job không chạy đúp. Qua ở cả ba backend ngay lần đầu (PostgreSQL dùng `SELECT … FOR UPDATE`).
- **`ConcurrentConditionalCreate`** (8 vòng × 16 request cùng `If-None-Exist`): **phát hiện lỗi thật**: nhiều request cùng thấy "chưa có" rồi cùng tạo, tạo trùng (Memory: 5 bản; PostgreSQL khi tắt khóa: 4 bản; mã trả về lẫn 201/200/412).

**Sửa**: create có điều kiện chạy trong MỘT transaction và giữ khóa theo (loại, tiêu chí) trước khi tìm rồi tạo.
- `fhirrouter.KeyLocker` (interface tùy chọn của Repository): `LockKey(ctx, key)` giữ khóa đến hết transaction hiện tại. Cài ở `MemoryRepository` (transaction đã tuần tự bằng `txMu`), `fhirstore.Store` (PostgreSQL: `pg_advisory_xact_lock(hashtextextended(key, 0))`; SQLite: một lệnh ghi rỗng để chiếm khóa ghi ngay), và các lớp bọc `fhirauth.Repo` (thêm project vào khóa để hai project không chặn nhau) và `fhirsub.Repo`.
- `Router.createIfNoneExist`; trong Bundle transaction thì dùng luôn transaction hiện có.
- Kiểm tra ngược: tắt khóa trên PostgreSQL thì test fail ngay vòng 1; bật lại thì qua (`-count=3 -race`). E2E hai replica dùng chung PG + Redis (5 vòng × 12 request chia đều vào 2 replica): đúng 1 Patient mỗi vòng.

**Sự cố trong lúc làm (đã khắc phục)**: một lệnh thay thế văn bản của tôi cắt mất phần cuối `pkg/fhirrouter/repotest/suite.go` (khoảng 900 dòng test: ValidateOperation, Everything, ReindexAndJobs, JobFailure, Expunge, ChainedSearchAndHas, CompositeSearch, FilterParam, GraphQL, ExportTypeFilter, ConditionalRead, BulkExport, GroupExport, SubsumesAndTranslate, TokenTerminology, Terminology). Repo có git nhưng chưa có commit nào (chỉ có index) nên không có bản gốc để lấy lại; khôi phục bằng cách phát lại đúng các lệnh đã tạo file từ transcript phiên làm việc, rồi đối chiếu: `Run` gọi 36 test, đều có định nghĩa và không trùng, và toàn bộ bộ test (Memory, SQLite, PostgreSQL, `-race`) xanh như trước. Khuyến nghị: **commit sớm** (đang chờ bạn) để có điểm khôi phục thật.

Chưa kiểm cùng kiểu tranh chấp: conditional update/delete (tìm rồi sửa/xóa), update-as-create song song cùng id, `$export`/job tranh nhau dưới tải thật.

## Giai đoạn 29a: tranh chấp của update-as-create, update có điều kiện, delete có điều kiện

Ba test tranh chấp mới trong bộ test dùng chung (16 request đồng thời, nhiều vòng; Memory, SQLite, PostgreSQL, qua `Scope`) và ba lỗi thật được sửa trong `pkg/fhirrouter/handlers.go` (các test fail trên code cũ, qua sau khi sửa):

| Test | Lỗi trên code cũ | Sửa |
|---|---|---|
| `ConcurrentUpdateAsCreate` (PUT Type/id cùng id mới) | request thua cuộc tranh tạo nhận **404** dù resource vừa được tạo | thua cuộc (409 khi tạo) thì cập nhật bản vừa tạo; chỉ trả 404 nếu id thuộc project khác (vẫn không thấy) |
| `ConcurrentConditionalUpdate` (PUT Type?tiêu-chí chưa có) | **tạo trùng** (2 resource), lẫn 412 | `conditionalUpdate` chạy trong transaction có khóa theo (loại, tiêu chí) |
| `ConcurrentConditionalDelete` | request đến sau nhận **410** | resource đã xóa hoặc biến mất giữa lúc tìm và xóa thì coi là xong (204, idempotent) |

Refactor: `withKeyLock` dùng chung cho create có điều kiện (28a) và update có điều kiện; `createIfNoneExist` gọi lại nó. Kết quả mong đợi được kiểm: đúng 1×201 và còn lại 200 (history có đủ phiên bản), đúng 1 resource sau update có điều kiện, mọi delete đều 204.

Kiểm tra: `go test ./... -p 1 -race` với PostgreSQL xanh, chạy thường xanh, các test tranh chấp chạy 3 lần trên PG không flaky.

Chưa kiểm: PATCH/`If-Match` cùng lúc với update thường (đã có `ConcurrentIfMatch` cho PUT), transaction Bundle chứa nhiều thao tác có điều kiện cùng tiêu chí chạy song song, và tải thật.

## Giai đoạn 30a: benchmark đầu-cuối và tối ưu chained search

`pkg/fhirstore/bench_test.go` (`make bench`): Router + Store (không qua mạng) trên dữ liệu có kích thước thật: 5000 Patient và 10 000 Observation (`BENCH_PATIENTS` đổi cỡ), SQLite và PostgreSQL 16 (nhúng, cùng máy, Apple M2 Max, `-benchtime 30x`). Số liệu mang tính tương đối.

**PostgreSQL, ms/op** (trước khi tối ưu, trừ dòng ghi chú):

| Thao tác | ms | Thao tác | ms |
|---|---|---|---|
| Read theo id | 0,09 | Create Patient | 1,4 |
| History | 0,13 | Update Patient | 1,3 |
| Search `family=` (20 dòng) | 2,7 | Create có điều kiện (đã có) | 1,4 |
| `family=` + `_total=accurate` | 4,8 | Transaction Bundle 10 mục | 9,3 |
| `family:contains` | 2,5 | Đọc song song (12 luồng) | 0,9/lần |
| `given=` theo id | 1,5 | Search song song (12 luồng) | 0,5/lần |
| `gender=` (một nửa số dòng khớp) | 7,3 | `Observation?subject=` | 2,0 |
| `birthdate` khoảng | 17 | `Observation?code=` | 3,4 |
| `_sort=birthdate` | 27 | `_include` | 4,6 |
| `component-code-value-quantity` | 32 | `_filter` | 14 |
| **chained `subject:Patient.family=`** | **211, sau tối ưu 17** | **`_has`** | **135 đến 240 (dao động)** |

**Tối ưu đã làm**: chained search khớp 500 Patient sinh ra điều kiện reference với 500 giá trị nối bằng OR (khoảng 1500 phép so sánh trên mỗi dòng ứng viên). `Store.referenceORs` gom thành `x.target IN (…) OR x.raw IN (…) [OR (x.ref_id IN (…) AND x.ref_type IN (…))]` (tương đương logic, khử trùng giá trị): **211 ms → 17 ms (khoảng 12 lần)**. Test PostgreSQL (differential Memory/SQL, repotest, Scope) xanh.

**Điểm yếu còn lại (chưa sửa)**:
- `_has` (135 đến 240 ms): mỗi sub-search phân trang 1000 dòng bằng OFFSET (mỗi trang chạy lại cả điều kiện và sắp xếp), rồi tuần tự hóa/giải mã JSON từng resource để lấy tham chiếu, rồi `_id IN (…)` tới 5000 id. CPU phía Go chỉ khoảng 23 ms mỗi lần, phần lớn là chờ cơ sở dữ liệu. Hướng sửa: dịch `_has` và chained sang truy vấn con SQL (không qua danh sách id); giới hạn `maxChainMatches` = 5000 vẫn là hàng rào.
- `_sort` theo tham số (27 ms với 5000 dòng): khóa sắp xếp là truy vấn con tương quan chạy hai lần trên mỗi dòng, nên chi phí tỉ lệ với số dòng cả khi chỉ lấy 20. Hướng sửa: JOIN LATERAL hoặc cột sắp xếp đã chuẩn hóa.
- `birthdate` khoảng (17 ms) và composite (32 ms) tăng theo số dòng.
- SQLite: `Observation?subject=` mất 417 ms ở chỉ 1000 Observation (trình tối ưu SQLite không đảo `EXISTS` thành semi-join), `_sort` 226 ms. SQLite chỉ nên dùng cho phát triển/test, không phải production.
- Ghi: mỗi `CreateResource` khoảng 1 ms chủ yếu do lập chỉ mục bằng nhiều câu INSERT riêng lẻ (seed 15 000 resource mất khoảng 15 giây); nạp lượng lớn nên dùng Bundle batch và cân nhắc gom INSERT.

Chưa đo: kích thước 100k trở lên (đường cong thực của các điểm yếu trên), tải nhiều client qua mạng, cache, bulk export lớn, `pg_stat_statements`/`EXPLAIN` từng truy vấn (cần cấu hình PostgreSQL thật, không phải bản nhúng).
