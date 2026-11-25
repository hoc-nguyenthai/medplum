# fhir-server

Máy chủ **FHIR R4** viết bằng Go: REST đầy đủ (create, read, vread, update, patch, delete, history, search, batch, transaction),
lưu trên **PostgreSQL** hoặc **SQLite**, kiểm tra dữ liệu khi ghi theo định nghĩa chính thức. Là bản Go của phần lõi `medplum-server`.

Toàn bộ nghiệp vụ FHIR nằm trong các thư viện dùng chung; app này chỉ lắp ráp chúng và lo cấu hình, xác thực, vận hành:

| Thư viện | Việc |
|---|---|
| [`pkg/fhir`](../../pkg/fhir) | kiểu Go cho 171 resource R4 + Medplum, schema, bảng search parameter (sinh bởi `tools/fhirgen`) |
| [`pkg/fhirpath`](../../pkg/fhirpath) | engine FHIRPath (679/686 ca của bộ test chuẩn HL7) |
| [`pkg/fhirvalidate`](../../pkg/fhirvalidate) | kiểm tra cấu trúc, kiểu, cardinality và invariant |
| [`pkg/fhirsearch`](../../pkg/fhirsearch) | phân tích và đối sánh yêu cầu search |
| [`pkg/fhirrouter`](../../pkg/fhirrouter) | các interaction REST trên interface `Repository`, adapter `net/http` |
| [`pkg/fhirstore`](../../pkg/fhirstore) | `Repository` SQL (PostgreSQL, SQLite), chỉ mục search |
| [`pkg/fhirsub`](../../pkg/fhirsub) | Subscription rest-hook: phát sự kiện sau commit, khớp criteria, giao có retry và chống SSRF |

Vì vậy app không có `domain/` và `application/` riêng: `internal/` chỉ gồm `config`, `presentation/httpapi` và `bootstrap`.

Tài liệu của service (kiến trúc, cấu hình sinh từ mã, kiểm thử, vận hành, hợp đồng API): [docs/README.md](docs/README.md).

## Chạy

```bash
cp apps/fhir-server/.env.example apps/fhir-server/.env     # đặt FHIR_WRITE_TOKENS (hoặc FHIR_AUTH_DISABLED=true cho dev)
make run-fhir-server                                       # :8005, SQLite ./fhir.db (tự migrate)
curl -s localhost:8005/fhir/R4/metadata                    # CapabilityStatement (công khai)
```

Lệnh khác: `fhir-server migrate up|down|status`, `fhir-server healthcheck` (cho HEALTHCHECK của Docker).
Với PostgreSQL: đặt `DATABASE_URL=postgres://...`; nhiều replica thì đặt `DATABASE_AUTO_MIGRATE=false` và chạy `migrate up` ở bước deploy
(app từ chối khởi động nếu còn migration chưa chạy).

## Endpoint

Gốc: `/fhir/R4`. Ví dụ đầy đủ trong [`test_main.http`](test_main.http). Hợp đồng API của service: [`docs/api-contract.md`](docs/api-contract.md) (bảng route sinh từ swagger); chi tiết (REST, operation, ví dụ JSON): [`docs/api-contract/`](docs/api-contract/) (`fhir-rest.md`, `fhir-operations.md`, `fhir-server-go.md`).

| Method và đường dẫn | Việc |
|---|---|
| `GET /metadata` | CapabilityStatement (công khai, không cần token) |
| `POST /{type}` | create (hỗ trợ `If-None-Exist`); `201` + `Location`, `ETag` |
| `GET /{type}/{id}`, `/{type}/{id}/_history/{vid}` | read, vread (`410` nếu đã xóa) |
| `PUT /{type}/{id}` | update (hỗ trợ `If-Match: W/"versionId"` -> `412`) |
| `PUT /{type}?search` | update có điều kiện (0 khớp: tạo; 1: sửa; nhiều: `412`) |
| `PATCH /{type}/{id}` và `PATCH /{type}?search` | JSON Patch (RFC 6902), `Content-Type: application/json-patch+json` |
| `DELETE /{type}/{id}` và `DELETE /{type}?search` | xóa (`204`) |
| `GET /{type}/{id}/_history` | lịch sử phiên bản |
| `GET /{type}?...`, `POST /{type}/_search`, `GET /?_type=A,B&...` | search |
| `POST /` | batch hoặc transaction (Bundle) |

Search hỗ trợ kiểu `string`, `token`, `reference`, `date`, `number`, `quantity`, `uri`; modifier `:exact :contains :text :not :missing`;
prefix `eq ne gt lt ge le sa eb ap`; `_count _offset _total _sort _summary _elements _include _revinclude _type`.
Tìm chuỗi không phân biệt hoa thường và bỏ dấu (`nguyen` khớp `Nguyễn`).

### Operation và job nền

| Method và đường dẫn | Quyền | Việc |
|---|---|---|
| `POST /{type}/$validate` | READ | kiểm tra resource (body là resource hoặc `Parameters` có `resource`) mà không lưu; `200` hoặc `400` kèm vị trí lỗi |
| `GET\|POST /Patient/{id}/$everything` | READ | Patient và mọi resource trong compartment; `_type`, `_since`, `_count` (mặc định 1000, trần 10000; vượt thì cắt và có entry cảnh báo) |
| `POST /{type}/$reindex` | ADMIN | lập lại chỉ mục search của cả kiểu, **chạy nền**: `202` + `Content-Location: .../job/{id}/status` |
| `POST /{type}/{id}/$reindex` | ADMIN | lập lại chỉ mục một resource (đồng bộ) |
| `POST /{type}/{id}/$expunge` | ADMIN | xóa vĩnh viễn resource và toàn bộ lịch sử (không hoàn tác) |
| `GET /job/{id}/status` | READ | `202` đang chạy, `200` + `AsyncJob` (có `output`) khi xong, `500` + `OperationOutcome` khi lỗi |
| `POST /` (Bundle `batch`) + `Prefer: respond-async` | WRITE | khi bật `FHIR_ASYNC_BATCH`: batch **chạy nền**, `202` + `Content-Location: .../job/{id}/status`; xem dưới |
| `DELETE /job/{id}/status` | WRITE | hủy batch async đang chạy (dừng ở entry kế tiếp, dọn kết quả tạm) hoặc xóa job đã xong cùng Binary kết quả |

Job lưu dưới dạng resource `AsyncJob`, chạy bằng `pkg/workflow` (mặc định trong process, `FHIR_JOB_CONCURRENCY` job đồng thời).
Adapter asynq/Temporal của `pkg/workflow` cắm được qua `fhirrouter.Options.NewOrchestrator`.

Độ bền: job đang chạy giữ một lease (`AsyncJob.transactionTime` là hạn của lease, tự gia hạn mỗi `FHIR_JOB_LEASE/3`). Nếu process chết
hoặc bị tắt giữa chừng, vòng bảo trì (`FHIR_JOB_MAINTENANCE_INTERVAL`, chạy cả lúc khởi động) đưa lại các job `accepted` đã hết lease
vào hàng đợi, nên job mồ côi tự chạy tiếp, kể cả ở replica khác; việc nhận job qua cập nhật có điều kiện nên không chạy trùng. Tắt êm
(SIGTERM) nhả lease để job được tiếp quản ngay. Job phải idempotent (`$reindex` và `$export` đều vậy). Vòng bảo trì cũng xóa kết quả
bulk export và batch async cũ hơn `FHIR_EXPORT_RETENTION` (cùng các file Binary).

**Batch async** (như `Prefer: respond-async` của Medplum, mặc định tắt bằng `FHIR_ASYNC_BATCH=false`; tắt thì header bị bỏ qua và batch chạy
đồng bộ). Bundle được kiểm cú pháp ngay (lỗi trả `400`, không tạo job); `transaction` kèm `respond-async` bị từ chối `400` vì transaction luôn
chạy nguyên tử. Job chạy với quyền và project của người gọi, kết quả giống batch đồng bộ: entry ngoài quyền là `403` của riêng entry đó. Xong
thì `AsyncJob.output` là `Parameters{results: valueReference Binary/<id>}`, Binary chứa Bundle `batch-response`. Checkpoint mỗi
`FHIR_BATCH_CHECKPOINT_ENTRIES` entry (mặc định 10) hoặc 5 giây; bị gián đoạn thì chạy tiếp từ checkpoint cuối, các entry sau đó chạy lại
(ít nhất một lần mỗi entry). POST không điều kiện được cấp id từ đầu nên chạy lại không tạo bản trùng; PUT, PATCH, DELETE chạy lại thì có
thể thêm một phiên bản. Bundle đầu vào, các đoạn kết quả và kết quả là Binary nằm trong CSDL (base64, như bulk export), không qua kho Binary.

### Subscription (rest-hook)

Bật bằng `FHIR_SUBSCRIPTIONS=true`. Khi một resource được create, update hoặc delete, mọi `Subscription` đang `active` có `criteria`
(ví dụ `Observation?code=http://loinc.org|29463-7`) khớp sẽ nhận một `POST` tới `channel.endpoint`. Hành vi tương thích Medplum:

- body là resource (`{}` khi xóa); `Content-Type: application/fhir+json`;
- header `X-Medplum-Subscription`, `X-Medplum-Interaction` (`create|update|delete`), `X-Medplum-Deleted-Resource` (khi xóa), và các header trong `channel.header` (`Tên: giá trị`);
- `X-Signature` = HMAC-SHA256(body) nếu Subscription có extension `https://www.medplum.com/fhir/StructureDefinition/subscription-secret`;
- chỉ giao các interaction khai báo bằng extension `https://medplum.com/fhir/StructureDefinition/subscription-supported-interaction` (mặc định cả ba); bỏ qua Subscription quá hạn `end`.

Sự kiện chỉ được phát **sau khi commit** (transaction bị rollback thì không phát gì). Giao thất bại (lỗi mạng, `408`, `429`, `5xx`) được thử lại
`FHIR_WEBHOOK_MAX_ATTEMPTS` lần với thời gian chờ tăng gấp đôi (trần 1 phút); các `4xx` khác không thử lại. Không theo redirect.

**Tự tắt Subscription lỗi kéo dài** (như `subscriptionAutoDisable` của Medplum, mặc định tắt): `FHIR_SUBSCRIPTION_AUTO_DISABLE` là JSON mảng
trigger, ví dụ `[{"maxConsecutiveFailures":10,"timeWindowSeconds":3600}]`. Mỗi sự kiện giao đã bỏ cuộc (hết lượt thử hoặc lỗi không thử lại)
được đếm; một lần giao thành công xóa bộ đếm. Khi số lần bỏ cuộc trong cửa sổ của một trigger đạt ngưỡng, Subscription đang `active` được
đổi thành `status=off` với `error` = `Automatically disabled after N consecutive failed events in the last W seconds` (ghi có `If-Match`,
Subscription đã bị đổi trạng thái thì giữ nguyên) và log WARN `tự tắt Subscription`. Với `BROKER_DRIVER=redis` bộ đếm nằm ở Redis
(`trivita:sub:failures:<id>`) nên các replica cộng dồn; với `memory` mỗi tiến trình đếm riêng. Chưa có: ghi đè theo project, `AuditEvent`.
Replica khác thấy Subscription đã tắt sau tối đa 30 giây (hạn cache danh sách Subscription).

**Chống SSRF:** mặc định *không* giao tới loopback, mạng riêng, link-local (kể cả `169.254.169.254` của metadata đám mây), CGNAT và multicast.
Kiểm tra làm ở bước kết nối trên địa chỉ IP thật nên chống được DNS rebinding; không dùng proxy từ môi trường. `FHIR_WEBHOOK_ALLOW_PRIVATE=true` chỉ cho dev/test và bị từ chối ở staging/production.

Mục này nói về kênh `rest-hook`; kênh `websocket` có ở mục "Subscription websocket" bên dưới; `email` và `sms` chưa hỗ trợ. Hàng đợi giao nằm trong bộ nhớ: process chết giữa chừng thì các lần thử lại đang hẹn bị mất.
Với nhiều replica đặt `BROKER_DRIVER=redis`: mỗi sự kiện chỉ một replica xử lý; danh sách Subscription được làm mới tức thì ở replica nhận sự kiện của chính Subscription
và theo chu kỳ 30 giây ở các replica khác.

Lỗi trả về là `OperationOutcome` (`Content-Type: application/fhir+json`), kể cả `401` và `403`.

## Xác thực

Bearer token tĩnh, ba mức: **READ** (đọc, tìm kiếm kể cả `POST .../_search`, `$validate`, `$everything`), **WRITE** (mọi thao tác ghi, kể cả
batch/transaction vì có thể chứa thao tác ghi) và **ADMIN** (ghi và quản trị: `$reindex`, `$expunge`, `$import`, `$install`, ghi trực tiếp `AsyncJob`). Token dạng `tên:secret`, secret tối thiểu 24 ký tự, so khớp bằng băm SHA-256 với thời gian không phụ thuộc nội dung.
**Scope SMART theo từng chữ.** Scope `system|user/<Loại|*>.<quyền>` cấp ĐÚNG thao tác của từng chữ: `c` tạo, `r` đọc (read, vread, history), `u` sửa (PUT), `d` xóa, `s` tìm kiếm; `.read` = `rs`, `.write` = `cud`, `.*` = `cruds`; scope `fhir.read` = `rs` và `fhir.write`/`fhir.admin` = `cruds` trên mọi loại. PATCH cần `r` và `u`; thao tác có điều kiện (PUT, PATCH, DELETE theo query, `If-None-Exist`) cần thêm `s`; update-as-create cần `c`. Thiếu quyền thì 403 kèm chữ còn thiếu. Scope `patient/`, scope có tham số (`?category=...`) và chữ lạ không cấp quyền gì.
**AccessPolicy trong token (giai đoạn 11b).** Claim `fhir_access_policy` là mảng rule, giống `AccessPolicy.resource` của Medplum:
`[{"resourceType":"Patient","criteria":"_id=123","readonly":true}, "Observation?subject=Patient/123"]`. Token có claim này chỉ truy cập
các loại được liệt kê; resource phải thỏa `criteria` (query FHIR, không chained/`_has`/`_include`) mới thấy hoặc ghi được; ghi ra ngoài tiêu
chí bị 403; rule `readonly` chặn ghi. Mỗi loại có thể có nhiều rule: resource được thấy khi thỏa bất kỳ rule nào (OR; ví dụ `Communication?sender=Patient/1` và `Communication?recipient=Patient/1`), mỗi rule là AND các điều kiện của nó; không được trộn rule không criteria, `deny` hoặc `readonly` khác nhau trong cùng một loại, tối đa 16 rule mỗi loại (bản `fhir-server` cũ từ chối dạng nhiều rule này). Mảng rỗng là từ chối mọi loại; claim sai định dạng làm token bị 403. Chính sách
giao với scope SMART. `criteria` còn nhận `_compartment=Patient/<id>` (đúng một Patient): resource thuộc compartment khi một tham số compartment của loại đó (ví dụ Observation: `subject`, `performer`) trỏ tới Patient ấy; Patient thì thêm chính nó. Điều kiện của chính sách được đẩy xuống kho nên `total` và phân trang chỉ tính phần được thấy. `$reindex` và `$expunge` bị chặn với token có chính sách. `$export` chạy đúng quyền của người gọi (xem dưới). Search hệ thống (không chỉ định loại) lọc kết quả
sau khi truy vấn nên `total` và phân trang có thể lệch.

**Job nền mang quyền của người gọi (giai đoạn 11c).** Khi tạo job, server ký (HMAC-SHA256) ảnh chụp quyền của người gọi (project, scope theo
loại, chính sách) vào `AsyncJob` (extension `job-seal`, ràng buộc id job, project và request). Trước khi chạy, job kiểm chữ ký rồi chạy với
đúng quyền đó; job thiếu hoặc sai chữ ký (bị sửa, tự tạo, đổi khóa) bị đánh dấu `error` và không chạy. Vì vậy `$export` dùng được với token
giới hạn: không chỉ định `_type` thì bỏ qua các loại không được đọc, chỉ định `_type` ngoài quyền thì job lỗi. Người dùng token giới hạn cần
quyền đọc `Binary` (scope `system/Binary.read` hoặc rule `"Binary"`) để tải file; lưu ý quyền này cho đọc mọi `Binary` trong project.
Job cũ tạo trước khi có chữ ký sẽ không chạy lại được.

**Subscription websocket (giai đoạn 15a).** Tạo Subscription với `channel.type = "websocket"` (không cần endpoint) và `criteria` như rest-hook.
Lấy token: `GET|POST /fhir/R4/Subscription/{id}/$get-ws-binding-token` (cần đọc được Subscription; trả `token`, `expiration`, `websocket-url`).
Mở websocket tới `/ws/subscriptions-r4` và gửi `{"type":"bind-with-token","payload":{"token":"..."}}` trong 10 giây (có thể bind nhiều
Subscription, tối đa 50 mỗi kết nối). Server trả Bundle(history) chứa `SubscriptionStatus`: `handshake` sau khi bind, `heartbeat` định kỳ và
`event-notification` (kèm resource, `eventsSinceSubscriptionStart`) cho mỗi thay đổi khớp tiêu chí và cùng project. Token ký HMAC, hạn 5 phút.
**Quyền được kiểm lúc phát, theo từng người nhận**: token binding mang ảnh chụp quyền của người lấy token (scope SMART, AccessPolicy, hạn access token). Mỗi sự kiện chỉ được gửi tới kết nối nào có chữ `r` trên loại resource đó và resource thỏa AccessPolicy; không thì bỏ qua hẳn, không gửi cả tham chiếu (kể cả thông báo xóa). Đếm ở `WSStats.Denied`. Access token gốc hết hạn thì binding đó thôi nhận (phải lấy token mới); quyền đổi giữa chừng chỉ có hiệu lực khi lấy token mới.
Mỗi instance giữ kết nối của riêng mình; sự kiện đến mọi instance qua Redis (đọc trực tiếp, không theo consumer group) nên kết nối ở
instance nào cũng nhận được. Tắt êm đóng kết nối bằng mã 1001 (GoingAway). Giới hạn: không bền (kết nối rớt thì mất thông báo trong lúc
rớt, không replay), token binding dùng lại được tới khi hết hạn, kết nối quá chậm (hàng đợi gửi 256 thông báo đầy) bị ngắt, Subscription bị
xóa hoặc tắt không ngắt kết nối đang mở (chỉ thôi nhận thông báo), endpoint không qua xác thực bearer (token nằm trong thông điệp bind).

**Super-admin xuyên project (giai đoạn 11d).** Đặt `FHIR_JWT_SUPER_ADMIN=true` thì token có scope `fhir.super` được quyền admin trong mọi
project và không cần claim project. Mỗi yêu cầu phải chọn project bằng header `X-Fhir-Project` (thiếu hoặc sai định dạng: 400); server xử lý
như người gọi thuộc project đó (dữ liệu ghi ra mang project đó, job nền chạy trong project đó). Mọi yêu cầu của super-admin, kể cả bị từ chối,
ghi một dòng nhật ký `audit=true` (subject, home_project, target_project, method, path, status; không có nội dung dữ liệu hay token). Khi
tắt, scope `fhir.super` bị bỏ qua. Token thường gửi `X-Fhir-Project` khác project của mình bị 403. Chỉ cấp scope này cho tài khoản vận hành
được kiểm soát chặt, và thu nhật ký `audit=true` vào kho log không sửa được.

**Gán project cho dữ liệu cũ.** Dữ liệu ghi trước khi bật tách project không có `meta.project` nên không ai thấy. Chạy (khi không có ghi
đồng thời, sau `migrate up`):
`fhir-server assign-project -project <id> [-type Patient,Observation] [-dry-run]`. Lệnh quét database trực tiếp, gán project cho resource chưa
có project (mỗi resource thêm một phiên bản), không đụng resource đã có project, chạy lại an toàn; lỗi từng resource được đếm và báo cuối cùng.

Đăng nhập và cấp token do service `auth` đảm nhiệm: `fhir-server` nhận access token OAuth2 của `auth` qua JWKS (mục "Xác thực JWT và tách dữ liệu theo project" bên dưới), tách dữ liệu theo project, phân quyền theo loại bằng scope SMART và AccessPolicy trong claim. **Chưa có:** scope `patient/<Loại>.read` và `%patient` (compartment Patient), đang ở kế hoạch P8 [`apps/auth/docs/plans/p8-compartment.md`](../auth/docs/plans/p8-compartment.md).

## Cấu hình

Biến môi trường (hoặc `.env`); danh sách đầy đủ ở [`.env.example`](.env.example). Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `HTTP_ADDR` | `:8005` | địa chỉ nghe |
| `DATABASE_URL` | `sqlite://./fhir.db` | `sqlite://<file>`, `sqlite://:memory:`, `postgres://...` |
| `DATABASE_AUTO_MIGRATE` | `true` | `false` -> từ chối khởi động nếu còn migration chưa chạy |
| `DATABASE_MIGRATE_URL` | rỗng | (chỉ PostgreSQL) URL của role `fhir_migrate` (có DDL) dùng riêng cho lệnh `fhir-server migrate`; app chạy bằng `DATABASE_URL` là role `fhir_app` chỉ có DML (D-DB-4, xem [runbook mục 12.5](../../docs/runbook.md)). Đặt thì **bắt buộc** `DATABASE_AUTO_MIGRATE=false` |
| `DATABASE_REQUIRE_SCHEMA` | `false` | (chỉ PostgreSQL) `true` -> từ chối khởi động và từ chối `migrate` nếu `search_path` của role không phải đúng MỘT schema riêng đã tồn tại (không phải `public`, không kèm schema khác). Bật khi chung một database với service khác (xem [docs/runbook.md mục 12](../../docs/runbook.md)) |
| `FHIR_BASE_URL` | `http://localhost:8005/fhir/R4` | URL công khai, dùng cho `Location`, `fullUrl`, link phân trang |
| `FHIR_WRITE_TOKENS` / `FHIR_READ_TOKENS` / `FHIR_ADMIN_TOKENS` | trống | token ghi, chỉ đọc và quản trị (cần ít nhất một token ghi hoặc admin) |
| `FHIR_JWT_ISSUER`, `FHIR_JWT_AUDIENCE` | trống | bật xác thực JWT (OAuth2/OIDC) và tách dữ liệu theo project; loại trừ với token tĩnh |
| `FHIR_JWKS_URL` | trống | JWKS của identity provider (RS/PS/ES/EdDSA); khóa được cache 10 phút, làm mới khi gặp `kid` lạ (tối đa 30 giây một lần) |
| `FHIR_JWT_PROJECT_CLAIM` / `FHIR_JWT_SCOPE_CLAIM` | `fhir_project` / `scope` | tên claim chứa project và scope |
| `FHIR_JOB_SIGNING_KEY` | (rỗng) | khóa ký quyền của job nền, tối thiểu 32 ký tự, mọi replica dùng chung. Bắt buộc khi dùng JWKS; dev HMAC tự suy ra từ `FHIR_JWT_HMAC_SECRET` |
| `FHIR_UPDATE_AS_CREATE` | `false` | `PUT Type/id` tạo mới với id do client chọn khi chưa có (trả `201`); tắt thì trả `404`. id phải hợp lệ FHIR (1-64 ký tự chữ, số, `-`, `.`), resource đã xóa không tạo lại được (`410`), id đang bị project khác dùng trả `404` |
| `FHIR_BINARY_STORAGE` | trống (tắt) | bật kho nội dung Binary: `file:<thư mục>` (giữ bố cục Medplum `<dir>/<id>/<versionId>`); hoặc `s3:<bucket>[/<prefix>]` (S3 và kho tương thích S3, xem bảng `FHIR_S3_*`); tiền tố khác bị từ chối khi khởi động. Tắt thì Binary giữ `data` base64 trong CSDL như trước, không có `/storage` và không có Binary thô. **Thư mục cục bộ không dùng chung được giữa nhiều replica**: nhiều replica phải dùng `s3:` |
| `FHIR_S3_REGION` | — | bắt buộc với `s3:` (vd `eu-west-1`) |
| `FHIR_S3_ACCESS_KEY_ID`, `FHIR_S3_SECRET_ACCESS_KEY` | — | bắt buộc với `s3:`; chỉ qua biến môi trường (không argv, không log). Chưa hỗ trợ khóa tạm từ IMDS/STS/IRSA; `FHIR_S3_SESSION_TOKEN` (tùy chọn) cho khóa tạm do vận hành cấp |
| `FHIR_S3_ENDPOINT` | trống (AWS) | gốc `http(s)://host[:port]` của MinIO/Ceph/R2... (kiểu path). Staging/production bắt buộc https |
| `FHIR_S3_SSE` | trống | `AES256` hoặc `aws:kms` (khóa KMS mặc định của bucket) |
| `FHIR_STORAGE_BASE_URL` | gốc của `FHIR_BASE_URL` (bỏ `/fhir/R4`) + `/storage` | gốc công khai của URL ký; host phải là host client nhìn thấy (đổi giá trị này làm mọi URL cũ mất hiệu lực); production bắt buộc https |
| `FHIR_STORAGE_SIGNING_KEY_PATH` | trống | PEM khóa riêng RSA ≥ 2048 bit (PKCS#8 hoặc PKCS#1, **không mật khẩu**), tệp quyền `0600` (production từ chối quyền rộng hơn). Staging/production **bắt buộc**; dev không đặt thì sinh khóa tạm (URL mất hiệu lực khi khởi động lại, không chạy nhiều replica) |
| `FHIR_STORAGE_SIGNING_PREVIOUS_KEYS` | trống | tệp PEM khóa **công khai** cũ (tối đa 3, cách nhau bằng dấu phẩy) chỉ để xác minh khi xoay khóa |
| `FHIR_STORAGE_URL_TTL` | `1h` | hạn URL ký, tối đa `24h` |
| `FHIR_STORAGE_MAX_UPLOAD_BYTES` | `268435456` (256 MiB) | trần nội dung tải lên (PUT `/storage` và POST/PUT Binary thô) **sau giải nén**; vượt thì `413` |
| `FHIR_STORAGE_LEGACY_GET_SIGNATURE` | `false` | GET chấp nhận thêm chữ ký không có tiền tố `GET ` (URL cũ do Medplum phát hành) |
| `FHIR_BASE64_BINARY_MAX_BYTES` | `1048576` | trần `data` base64 (đã giải mã) khi ghi resource Binary; vượt thì `400 base64Binary exceeds N bytes`; âm: không trần |
| `FHIR_WS_MODE` | `off` | websocket của Subscription: `off`; `embedded` (Hub trong chính tiến trình, một tiến trình giữ cả kết nối lẫn REST); `issuer` (**chỉ cấp token bind và khớp sự kiện, kết nối do [ws-gateway](../ws-gateway/README.md) giữ**; cần `FHIR_SUBSCRIPTIONS=true`, `BROKER_DRIVER=redis`, `FHIR_WS_SIGNING_KEY` giống gateway và `FHIR_WS_URL` là địa chỉ websocket công khai của gateway; xem dưới) |
| `FHIR_WEBSOCKET` | `false` | BÍ DANH CŨ của `FHIR_WS_MODE=embedded`; đặt cùng `FHIR_WS_MODE` khác `embedded` là lỗi |
| `FHIR_WS_SIGNING_KEY_PREVIOUS` | (rỗng) | khóa cũ chỉ để XÁC MINH khi xoay khóa (cách nhau bằng dấu phẩy, tối đa 3, mỗi khóa >= 32 ký tự); xem quy trình ở `docs/runbook.md` |
| `FHIR_WS_SIGNING_KEY` | (rỗng) | khóa ký binding token (>= 32 ký tự, mọi instance dùng chung); bắt buộc ở staging/production hoặc khi `BROKER_DRIVER=redis`, dev một instance tự sinh khóa ngẫu nhiên |
| `FHIR_WS_URL` | suy từ `FHIR_BASE_URL` (`embedded`); **bắt buộc ở `issuer`** | websocket-url trả cho client, ví dụ `wss://fhir.example.org/ws/subscriptions-r4`; ở `issuer` là địa chỉ của gateway và staging/production chỉ nhận `wss://` |
| `FHIR_WS_MAX_CONNECTIONS` / `FHIR_WS_HEARTBEAT` | `1000` / `30s` | số kết nối tối đa mỗi instance / chu kỳ heartbeat và ping (chỉ `embedded`; ở `issuer` đặt ở ws-gateway, đặt ở đây là lỗi) |
| `FHIR_WS_MAX_BINDINGS_PER_USER` | `20` | số lượt bind đồng thời tối đa của một người (Principal.Subject) trong một project, đếm trên mọi kết nối của instance; vượt thì đóng 1008 `User has exceeded allotted maximum number of concurrent WebSocket subscriptions: <n>`. Chỉ áp cho token cấp bởi người dùng đã xác thực |
| `FHIR_WS_ALLOWED_ORIGINS` | (rỗng) | origin được phép (mẫu, cách nhau bằng dấu phẩy); rỗng: trình duyệt chỉ được từ cùng host (chỉ `embedded`; `FHIR_WS_MAX_BINDINGS_PER_USER` cũng vậy: ở `issuer` thuộc ws-gateway) |
| `FHIR_ALLOWED_ORIGINS` | (rỗng; `http://localhost:3000` khi `ENVIRONMENT=local`) | origin được gọi API từ trình duyệt (CORS, chỉ dưới `/fhir/R4`), dạng `https://host[:port]`; staging/production cấm `*` và `http` |
| `FHIR_TRUSTED_PROXIES` | (rỗng) | CIDR reverse proxy được tin khi đọc IP từ `X-Forwarded-For`; rỗng: dùng IP kết nối (đứng sau proxy mà không khai thì mọi người gọi chung một hạn mức) |
| `FHIR_RATE_LIMIT` | `0` (tắt); **`20` ở staging/production nếu không đặt** (đặt `0` rõ ràng mới tắt) | số yêu cầu/giây mỗi IP dưới `/fhir/R4`, trước xác thực (chặn cả dò token); vượt thì 429 + `Retry-After`. Đếm trong bộ nhớ từng instance. Đứng sau proxy thì khai `FHIR_TRUSTED_PROXIES`, nếu không mọi người gọi chung một hạn mức 20 rps |
| `FHIR_RATE_BURST` | `2 × FHIR_RATE_LIMIT` | cỡ đợt |
| `FHIR_QUOTA_USER`, `FHIR_QUOTA_PROJECT` | `0` (tắt) | hạn mức ĐIỂM mỗi `FHIR_QUOTA_WINDOW` (mặc định `1m`) cho mỗi người gọi và cho cả project: đọc 1 điểm, ghi 2, tìm kiếm/history 5, operation 10, Bundle 20; hết điểm thì 429 + `Retry-After`, mọi phản hồi có `RateLimit-Limit/Remaining/Reset`. Đứng sau xác thực. Admin xem tình trạng bằng `GET /fhir/R4/Project/{id}/$rate-limits`. Đếm trên Redis khi `FHIR_RATE_LIMIT_SHARED=true` |
| `FHIR_JWT_SUPER_ADMIN` | `false` | bật scope `fhir.super` (super-admin xuyên project, xem dưới) |
| `FHIR_JWT_POLICY_CLAIM` | `fhir_access_policy` | tên claim chứa AccessPolicy (mảng rule, xem bên dưới) |
| `FHIR_JWT_HMAC_SECRET` | trống | CHỈ dev/test: khóa HS256 thay cho JWKS; bị từ chối ở staging/production |
| `FHIR_JOB_CONCURRENCY` | `2` | số job nền chạy đồng thời |
| `FHIR_JOB_LEASE` | `5m` | thời gian giữ quyền chạy job trước khi replica khác tiếp quản |
| `FHIR_JOB_MAINTENANCE_INTERVAL` | `30s` | chu kỳ khôi phục job mồ côi và xóa export hết hạn (`0` = tắt) |
| `FHIR_EXPORT_RETENTION` | `24h` | thời gian giữ kết quả bulk export và batch async (âm = không bao giờ xóa) |
| `FHIR_ASYNC_BATCH` | `false` | cho batch kèm `Prefer: respond-async` chạy nền |
| `FHIR_BATCH_CHECKPOINT_ENTRIES` | `10` | số entry giữa hai checkpoint của batch async |
| `FHIR_SUBSCRIPTIONS` | `false` | bật Subscription rest-hook |
| `BROKER_DRIVER`, `REDIS_URL` | `memory` | broker sự kiện: `memory` hoặc `redis` |
| `FHIR_WEBHOOK_WORKERS`, `FHIR_WEBHOOK_MAX_ATTEMPTS`, `FHIR_WEBHOOK_TIMEOUT` | `4`, `5`, `10s` | giao webhook |
| `FHIR_WEBHOOK_ALLOW_PRIVATE` | `false` | chỉ dev; tắt chống SSRF; bị từ chối ở staging/production |
| `FHIR_SUBSCRIPTION_AUTO_DISABLE` | rỗng (tắt) | JSON mảng `{maxConsecutiveFailures, timeWindowSeconds}`: tự tắt Subscription giao thất bại lặp lại; cần `FHIR_SUBSCRIPTIONS=true` |
| `FHIR_AUTH_DISABLED` | `false` | chỉ dev; bị từ chối ở staging/production |
| `FHIR_SKIP_CONSTRAINTS` | `false` | chỉ kiểm tra cấu trúc, bỏ invariant FHIRPath |
| `FHIR_AUDIT` | `off` | ghi `AuditEvent` vào kho cho thao tác REST của người gọi: `writes` (create, update, delete) hoặc `all` (thêm read, vread, history, search; khối lượng ghi lớn). Không sửa, không xóa `AuditEvent` qua API |
| `FHIR_VALIDATE_PROFILES` | `false` | kiểm thêm theo từng `meta.profile` bằng StructureDefinition trong kho (cardinality, fixed/pattern, slicing) |
| `FHIR_PROFILE_UNKNOWN_ERROR` | `false` | `meta.profile` không có trong kho bị từ chối 400 (mặc định chỉ cảnh báo) |
| `FHIR_SKIP_VALIDATION` | `false` | không kiểm tra dữ liệu; bị từ chối ở staging/production |
| `READINESS_TIMEOUT`, `SHUTDOWN_DRAIN_DELAY`, `SHUTDOWN_TIMEOUT` | `2s`, `5s`, `25s` | vòng đời K8s |

`GET /healthz` là liveness; `GET /readyz` kiểm tra DB và trả `503` khi đang tắt. SIGTERM -> readyz 503 -> chờ drain -> tắt êm.

## Docker

```bash
docker compose --env-file apps/fhir-server/.env up --build fhir-server postgres     # chạy từ thư mục gốc repo
make docker APP=fhir-server
```

## Giới hạn hiện tại

Tham số `_filter` (giai đoạn 21a): biểu thức logic trên điều kiện search, ví dụ
`Patient?_filter=name co "smith" and (birthdate lt 1980-01-01 or gender eq male)`, `not ( ... )`, `email pr true`. Toán tử: string `eq` (chính xác, phân biệt
hoa thường) `ne co sw pr`; token/reference `eq ne pr`; uri `eq ne sw pr`; date/number/quantity `eq ne gt lt ge le sa eb ap pr`. Không có `ew ss sb in re po`,
composite, modifier hoặc chained trong tham số, và không dùng được ở search hệ thống hay trong `AccessPolicy.criteria`; tối đa 50 điều kiện, lồng 12 cấp.
GraphQL (giai đoạn 20a): `POST|GET /fhir/R4/$graphql` (body JSON `{query, variables, operationName}`, `application/graphql`, hoặc `?query=`).
Ví dụ: `{ PatientList(family: "smith", _count: 10) { id name { family } ObservationList(_reference: subject) { code { text } } } }`.
Gốc: `Type(id:)`, `TypeList(...)` (tham số search, gạch dưới thành gạch ngang), `TypeConnection(...)` (`count`, `edges { resource }`);
trong resource có `resource` trên Reference và `OtherList(_reference: param)` cho tham chiếu ngược. Chạy qua cùng repository nên tách project,
quyền theo loại và `AccessPolicy` áp như REST; chỉ cần quyền đọc (không có mutation). Giới hạn: 1000 resource mỗi truy vấn, `_count` tối đa 100,
độ sâu 12; không có introspection, directive (bị bỏ qua) và tham số của field thường.
Operation giai đoạn 14a: `CodeSystem/$subsumes`, `ConceptMap/$translate` (có `reverse`, tìm theo `source-system` khi không chỉ định bản đồ) và `Group/{id}/$export` (thành viên Patient đang hoạt động). Search token theo terminology (`Condition?code:below=http://snomed.info/sct|73211009`, `:above`, `:in=<ValueSet>`, `:not-in`) dùng CodeSystem/ValueSet lưu trong server (giai đoạn 13a). Chained search (`subject:Patient.name=x`) và `_has` đã có (giai đoạn 10a); search composite (`Observation?component-code-value-quantity=8480-6$gt130`) đã có (giai đoạn 12a, dữ liệu ghi trước đó cần `$reindex` để search composite thấy). Chưa có: phần lớn operation `$...` còn lại (danh sách: [`docs/plans/coverage-matrix.md`](../../docs/plans/coverage-matrix.md) mục 5.2), profile/slicing và kiểm tra terminology khi ghi, compartment Patient (`patient/`). Chỉ mục search được lập khi ghi; khi định nghĩa search parameter đổi thì chạy `$reindex` (job nền, ADMIN; xem bảng endpoint).


## Xác thực JWT và tách dữ liệu theo project

Đặt `FHIR_JWT_ISSUER` để dùng access token của identity provider thay cho token tĩnh. Token phải có chữ ký hợp lệ,
`iss`, `aud` (= `FHIR_JWT_AUDIENCE`), `exp`, `sub`, claim project, và ít nhất một scope cấp quyền:

| Scope | Vai trò |
|---|---|
| `fhir.read`, `system/*.read` | đọc, tìm kiếm, `$validate`, `$everything` |
| `fhir.write`, `system/*.write`, `system/*.*` | đọc và ghi |
| `fhir.admin` | thêm `$reindex`, `$expunge` |

Thiếu hoặc sai token: `401` (`WWW-Authenticate: Bearer error="invalid_token"`). Token đúng nhưng thiếu project hoặc scope: `403`.

Mọi resource mang `meta.project` do server gán từ token (giá trị client gửi bị ghi đè). Người gọi chỉ đọc, sửa, xóa, tìm kiếm
và thấy lịch sử của resource cùng project; resource project khác trả `404`. Subscription chỉ nhận sự kiện của cùng project.

Giới hạn: scope theo từng loại (`system|user/<Loại>.<quyền>`) và AccessPolicy trong claim đã có (xem trên) nhưng **chưa có** scope `patient/<Loại>.read` và `%patient` (compartment Patient, kế hoạch P8); `$reindex` cả loại chạy trên mọi
project (chỉ tái lập chỉ mục); dữ liệu ghi trước khi bật JWT không có `meta.project` nên không ai thấy cho tới khi được gán;
đọc id đã xóa của project khác trả `410` thay vì `404`.


## Bulk export và terminology

```bash
# xuất toàn bộ (hoặc Patient/$export, _type=Patient,Observation, _since=2026-01-01T00:00:00Z)
curl -i -H "Authorization: Bearer $TOKEN" -H "Prefer: respond-async" "$BASE/fhir/R4/\$export"
#   -> 202, Content-Location: $BASE/fhir/R4/bulkdata/{id}/status (gọi lại tới khi 200)
#   -> 200 {"transactionTime":..., "output":[{"type":"Patient","url":".../Binary/{id}","count":3}]}
curl -H "Authorization: Bearer $TOKEN" -H "Accept: application/fhir+ndjson" "$BASE/fhir/R4/Binary/{id}"

# terminology (CodeSystem và ValueSet lưu trong server)
curl "$BASE/fhir/R4/ValueSet/\$expand?url=http://example.org/vs/animals&filter=dog&count=20" -H "Authorization: Bearer $TOKEN"
curl "$BASE/fhir/R4/CodeSystem/\$lookup?system=http://example.org/cs/animals&code=dog" -H "Authorization: Bearer $TOKEN"
curl "$BASE/fhir/R4/ValueSet/\$validate-code?url=http://example.org/vs/animals&code=dog" -H "Authorization: Bearer $TOKEN"
```

`DELETE .../bulkdata/{id}/status` hủy export đang chạy (bỏ kết quả) hoặc xóa kết quả đã xong. Export chạy nền với quyền của người gọi (khi bật JWT, chỉ xuất dữ liệu cùng project) và cần quyền đọc. Giới hạn: xem
`docs/go-port-status.md` (giai đoạn 8a).

Vận hành (sao lưu, migrate, xoay khóa, job kẹt, sự cố thường gặp): [docs/runbook.md](../../docs/runbook.md).

**Dùng token do `auth` cấp.** Đặt `FHIR_JWT_ISSUER` = `AUTH_ISSUER` (đúng từng ký tự), `FHIR_JWT_AUDIENCE` = `AUTH_AUDIENCE` (mặc định chính issuer), `FHIR_JWKS_URL` = địa chỉ fhir-server với tới được của `<auth>/.well-known/jwks.json` (https ở staging/production), `FHIR_JOB_SIGNING_KEY` (≥ 32 ký tự), và **để trống** mọi `FHIR_*_TOKENS`. Project của dữ liệu lấy từ claim `fhir_project`, quyền từ `scope` và `fhir_access_policy`. Token đã thu hồi vẫn được nhận cho tới khi hết hạn (`AUTH_ACCESS_TTL`, mặc định 15 phút). Chi tiết: [oauth.md](../auth/docs/api-contract/oauth.md); kiểm bằng `make test-e2e`; compose: `docker-compose.auth.yml`.

**Header bảo mật.** Mọi phản hồi (kể cả 401/429) mang `Cache-Control: no-store`, CSP `default-src 'none'`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, HSTS khi `FHIR_BASE_URL` là https (`pkg/httpsec`). Ngoại lệ: `/swagger/` (UI cần tải script).


## Kho nội dung Binary (`FHIR_BINARY_STORAGE`)

Khi bật (xem bảng cấu hình), nội dung Binary nằm ở kho tệp thay vì cột `data` của CSDL; resource chỉ giữ metadata. Mỗi phiên bản là một đối tượng riêng `binary/<id>/<versionId>`.

- **`POST /fhir/R4/Binary`, `PUT /fhir/R4/Binary/{id}`**: thân thô (hoặc nén `Content-Encoding: gzip|deflate`; giá trị khác kể cả `identity` là `400`) lưu như nội dung, `Content-Type` là loại nội dung (mặc định `application/octet-stream`), `?_filename=` và loại bị kiểm với danh sách chặn (`.exe`, `.sh`... xem `pkg/binarystore/metadata.go`; bỏ tham số `; charset=` và dấu chấm/cách cuối tên trước khi so) **trước** khi tạo resource nên không để lại Binary mồ côi. Header `X-Security-Context: Patient/123` đặt `securityContext`. Phản hồi là resource Binary kèm `url` ký sẵn (không lưu). Thân JSON `application/fhir+json` hoặc `application/json` là resource `Binary` thì xử lý như resource (có `data` base64 thì ra kho); thân JSON khác được lưu thô. Không giới hạn 16M của route thường; trần là `FHIR_STORAGE_MAX_UPLOAD_BYTES` (sau giải nén, chống bom nén).
- **`GET /fhir/R4/Binary/{id}`**: trả **nội dung thô** với `Content-Type` của Binary, trừ khi `Accept` bắt đầu bằng `application/fhir+json` hoặc `application/json` (hoặc có `_format`) thì trả resource. *Khác hành vi cũ* (trước đây không có `Accept` thì ra resource JSON). Phản hồi thô luôn có `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, và `Content-Disposition: attachment` trừ ảnh png/jpeg/gif/webp (SVG và PDF bị buộc tải xuống). Binary kiểu cũ (có `data` trong CSDL) vẫn đọc được.
- **`GET|PUT /storage/{id}/{versionId}?Expires=…&Project=…&Signature=…`**: URL ký RSA-SHA256 tương thích Medplum, không cần Bearer (có hạn mức theo IP). Chữ ký gắn với phương thức, đường dẫn, `Expires`, `Project` và gốc `FHIR_STORAGE_BASE_URL`; quá hạn `410`, thiếu hoặc sai chữ ký `401`, Binary không có hay không có nội dung `404`. Khác Medplum: phục vụ **đúng phiên bản trong URL**; PUT lên URL phiên bản cũ là `409`; Binary thuộc project nào thì URL phải mang đúng `Project` đó; `Content-Type` của PUT so sau khi bỏ tham số; lỗi kho là `500` (không lộ chi tiết).
- **Chuyển `data` base64 ra kho**: Binary ghi qua router (kể cả trong transaction Bundle) có `data` được giải mã, ghi vào kho rồi bỏ `data` khỏi CSDL. Cập nhật không kèm `data` chép nội dung của phiên bản trước. `$expunge` xóa nội dung mọi phiên bản (sau commit). Kết quả bulk export (job nền) giữ nguyên trong CSDL.
- **`GET /fhir/R4/Binary/{id}/$presigned-url[?upload=true]`**: trả `Parameters{url}` với URL ký GET (đọc) hoặc PUT (tải lên, phiên bản hiện tại) hạn `FHIR_STORAGE_URL_TTL`. Đọc cần quyền đọc Binary (đã tách project, SMART, chính sách); `upload=true` cần thêm quyền **cập nhật** Binary (vai trò từ write trở lên, scope có `u`, chính sách không chỉ-đọc), không thì `403`. Token tĩnh chỉ đọc không xin được URL tải lên.
- **`securityContext`**: Binary có `securityContext` (ví dụ `Patient/123`, đặt bằng header `X-Security-Context`) chỉ được đọc thô, xin `$presigned-url` hoặc nhận URL ký khi người gọi **đọc được resource đó** (kiểm bằng repository của người gọi); không thì `403`. Tham chiếu không dạng `Type/id` (URL, urn) hoặc trỏ tới resource không còn thì bị từ chối. Lưu ý: URL ký đã phát ra là khả năng (capability) và hoạt động tới khi hết hạn dù quyền của người nhận URL đổi sau đó.
- **Viết lại tệp đính kèm** (Medplum `rewriteAttachments`): thuộc tính chuỗi tên `url` hoặc `path` mang dạng `Binary/<id>[/_history/<v>]`, `<FHIR_BASE_URL>/Binary/<id>[/_history/<v>]` hoặc `<FHIR_STORAGE_BASE_URL>/<id>[/<v>]` (bỏ `?query`) được đổi:
  - **khi ghi** (qua router, cả batch/transaction): thành `Binary/<id>`, không kiểm quyền, nên CSDL không giữ URL có hạn và client gửi lại resource mang URL ký vẫn lưu đúng;
  - **khi trả về** (mọi interaction thành công, kể cả từng entry của Bundle): thành URL ký GET của đúng phiên bản (hoặc bản hiện tại) **chỉ khi người gọi đọc được Binary** (cả `securityContext`); không đọc được thì giữ nguyên chuỗi (không lộ URL ký của project khác); Binary không còn thì thành `Binary/<id>`.
  - Không viết lại bên trong resource `Binary` và `Bundle.entry.request.url`; chỉ các khóa `url` và `path` (không đụng `fullUrl`, `valueUri`, `reference`). Chỉ chạy khi bật `FHIR_BINARY_STORAGE`. Mỗi phản hồi được mã hóa thêm một lần để dò URL Binary (bỏ qua nhanh nếu không có chuỗi `Binary/` hay gốc storage), nên có chi phí trên đường đọc; chưa đo tải.
- **Quét nội dung mồ côi** (tiến trình chết giữa lúc ghi CSDL và ghi kho): `fhir-server sweep-binary [-grace 24h] [-max-delete 1000] [-dry-run] [-force]` xóa nội dung trong kho mà không còn phiên bản Binary nào trong CSDL (chỉ xét nội dung cũ hơn `-grace`, tối thiểu 1 giờ; chỉ xóa khi CSDL trả 404 dứt khoát; trần `-max-delete` mỗi lượt; hủy lượt nếu quá nửa số khóa là mồ côi trừ khi `-force`). Chạy định kỳ bằng cron, cần `FHIR_BINARY_STORAGE` và CSDL bền; nên chạy `-dry-run` trước.
- **Chưa có**: tiền tố `/api/storage` và `/projects/{id}/storage` của Medplum, nhật ký truy cập chưa được kiểm là không ghi query (có `Signature`), viết lại trong phản hồi của `auth` (`/auth/me`...) và của subscription.
