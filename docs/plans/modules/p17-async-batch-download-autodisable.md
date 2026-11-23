# Kế hoạch việc #17: batch async, tự tải attachment, tự tắt Subscription lỗi

Trạng thái: **bản nháp chờ duyệt**, chưa có code. Việc #17 trong [task-list.md](../task-list.md) (Ưu tiên 1b), nguồn ở [coverage-matrix.md](../coverage-matrix.md) mục 5.4.

Nguồn Medplum (chỉ đọc): `/Users/hoc.nguyenthai/Documents/med-vita/medplum/packages/server/src` (gọi tắt `S/`), bản `v5.2.17-5-ge7e7e4019`, cũ hơn tag `v5.2.18` ghi ở [UPSTREAM.md](../../UPSTREAM.md); hành vi dưới đây có thể đã đổi ở 5.2.18. Đường dẫn Go tính từ gốc repo. Quy ước như các kế hoạch khác: điều đã đọc ghi kèm `file:dòng`; điều chưa kiểm ghi "chưa xác nhận" và gom ở mục 7.

## 0. Tóm tắt

| Phần | Medplum | Go hiện có | Cỡ | Chặn bởi |
|---|---|---|---|---|
| A. Batch async (`POST /fhir/R4` + `Prefer: respond-async`) | `S/async-batch.ts` (51 dòng), `S/workers/batch.ts` (543 dòng, có checkpoint) | chỉ `$export` nhận `respond-async` (`pkg/fhirrouter/bulk.go:60`); `batch()` chạy đồng bộ (`pkg/fhirrouter/batch.go:50`) | M | Q-17.1, Q-17.2 |
| B. Tự tải attachment URL ngoài về Binary (worker `download`) | `S/workers/download.ts` (345 dòng), kích hoạt từ `S/workers/dispatch.ts:186` | chưa có | M | Q-17.4, Q-17.5; cần `FHIR_BINARY_STORAGE` |
| C. Tự tắt Subscription lỗi kéo dài | `S/workers/subscription-failure-tracker.ts` (137 dòng), `S/workers/subscription.ts:733,1020-1091` | giao rest-hook thử lại rồi **bỏ cuộc, chỉ ghi log** (`pkg/fhirsub/deliver.go:96-101`) | S | Q-17.6 |

Thứ tự đề xuất: **C → A → B**. C nhỏ, không đổi API và giảm tải ngay (endpoint chết không bị gọi mãi). A cần mở rộng cơ chế job (trạng thái checkpoint). B nặng nhất về bảo mật (gọi URL do người dùng đưa) và cần quyết định cấu hình theo project.

---

## Phần A. Batch async

### A.1 Hành vi Medplum

- Handler đứng **trước** router FHIR, chỉ cho `POST` vào gốc FHIR (`S/app.ts:242-247`), chỉ khi header `Prefer` **đúng bằng** `respond-async` (`S/async-batch.ts:21`).
- Cần project có feature `async-batch`, không có thì 400 (`S/async-batch.ts:26-28`).
- Body phải là `Bundle` (`:31-33`). `transaction` bị từ chối (400) **khi project bật transaction thật** (`transaction-bundles`, `:36-40`); nghĩa là transaction chỉ chạy async khi nó vốn không nguyên tử.
- Tạo `AsyncJob`, lưu Bundle đầu vào vào kho đối tượng, đưa job vào hàng đợi `BatchQueue` (`S/workers/batch.ts:193-197`); trả 202 + `Content-Location` của AsyncJob.
- Worker (`execBatchJob`, `:215`): lần đầu tiền xử lý Bundle (thứ tự, cấp id cho POST) và lưu trạng thái đầu; sau đó chạy **từng entry**, checkpoint mỗi 10 entry hoặc 5 giây (`:95-96`), ghi kết quả từng đoạn vào kho; khi tắt êm thì checkpoint rồi đẩy job về hàng đợi trễ; sau mỗi checkpoint đọc lại AsyncJob, bị hủy thì dừng.
- Ngữ nghĩa **ít nhất một lần cho mỗi entry** (comment đầu `S/workers/batch.ts`): entry đã commit nhưng chưa checkpoint sẽ chạy lại khi tiếp tục; POST giữ id đã cấp lúc tiền xử lý.
- Kết quả: Binary chứa Bundle `batch-response`; `AsyncJob.output = Parameters{results: Reference(Binary)}`; lỗi thì `failJob` kèm kết quả từng phần và `error`.
- Worker chạy với quyền của người gọi (`runJobInAuthenticatedContext`, `authState` trong dữ liệu job), concurrency 15 (`:124`), `attempts: 1`.

### A.2 Hiện trạng Go

- `Router.batch` (`pkg/fhirrouter/batch.go:50-94`): batch chạy tuần tự từng entry; transaction chạy trong `RunInTransaction` (luôn nguyên tử). Route `POST ""` (`:279`). Body chung bị chặn 16 MB (`apps/fhir-server/internal/presentation/httpapi/httpapi.go:92`).
- Job nền (`pkg/fhirrouter/jobs.go`):
  - `JobFunc(ctx, repo, *Input) (*fhir.Parameters, error)` (`:17`); đầu vào chỉ là query string lưu trong `AsyncJob.request` dạng `kind?query` (`:82`), không có chỗ cho Bundle.
  - `RegisterScopedJob` chạy với quyền người gọi qua chữ ký `JobSeal` (`:33`, `:86-91`, `:139-150`): **tái dùng được nguyên** cho batch.
  - Lease + heartbeat + `RecoverJobs`; khi tắt êm thì nhả lease để chạy lại **từ đầu** (`:197-203`). Chưa có checkpoint (đã ghi ở `docs/go-port-status.md`, giai đoạn 9a).
  - `ExpireJobs` chỉ dọn job `export?` (`pkg/fhirrouter/jobs_maint.go:181`); `outputBinaryIDs` chỉ đọc `part.valueUri` dạng manifest export (`:96-111`), không đọc `valueReference`.

### A.3 Thiết kế

1. **Điểm vào**: trong `Router.batch`, nếu `Prefer` chứa `respond-async` (khớp cách `bulk.go:60` đang làm, không so khớp tuyệt đối như Medplum):
   - Kiểm như đường đồng bộ (Bundle, `type`, parse từng entry) **trước** khi tạo job, để lỗi cú pháp trả 400 ngay.
   - `transaction` → 400 "Transaction không chạy async được" (Go luôn chạy transaction nguyên tử; tương đương nhánh `transaction-bundles` của Medplum). Xem Q-17.2.
   - Lưu Bundle đầu vào thành `Binary` (qua `JobRepository`, gán `meta.project` của người gọi), rồi `StartJob(ctx, "batch", {input: [binaryID]})`. Trả 202 + `Content-Location: job/{id}/status` (`startJobResponse`, `jobs.go:101`).
   - Đăng ký bằng `RegisterScopedJob("batch", ...)` để mỗi entry chạy với đúng quyền, AccessPolicy và project của người gọi.
2. **Trạng thái checkpoint của job** (mới, dùng chung, cũng giải được giới hạn "chưa có checkpoint" của 9a):
   - Thêm `Input.JobID` và hàm `Router.SaveJobState(ctx, jobID, state []byte)` / đọc lại khi job bắt đầu. Lưu ở extension `AsyncJob` (`.../job-state`, `valueString` JSON) bằng `UpdateResource` có `IfMatch` để không ghi đè trạng thái hủy.
   - Trạng thái batch: `{position, chunks: [binaryID...], ids: {entryIndex: id cấp sẵn cho POST}}`.
   - Checkpoint mỗi `FHIR_BATCH_CHECKPOINT_ENTRIES` (mặc định 10) entry hoặc 5 giây; sau checkpoint đọc lại AsyncJob, `cancelled` thì dừng và dọn Binary đoạn.
   - Kết quả từng đoạn ghi thành Binary riêng; khi xong ghép thành một Bundle `batch-response`, ghi Binary kết quả, xóa các Binary đoạn và Binary đầu vào.
   - **POST giữ id**: cấp id cho entry POST lúc bắt đầu (lưu trong trạng thái), tạo bằng `postWithID` (`batch.go:248`) để chạy lại không sinh bản trùng.
3. **Kết quả**: `AsyncJob.output = Parameters{results: valueReference Binary/<id>}` như Medplum; lỗi giữa chừng thì `status=error`, output gồm `results` (từng phần) và `outcome`. `GET job/{id}/status` giữ nguyên hành vi (`jobs.go:229`).
4. **Dọn dẹp**: `ExpireJobs` nhận cả `batch?`; `outputBinaryIDs` đọc thêm `valueReference` `Binary/<id>`.
5. **Bật/tắt**: `FHIR_ASYNC_BATCH` (mặc định `false`) thay feature `async-batch` theo project của Medplum, vì `fhir-server` không có resource `Project` (Q-17.1). Tắt thì `Prefer: respond-async` bị bỏ qua và batch chạy đồng bộ như hiện nay. Chưa xác nhận Medplum trả gì khi thiếu feature mà có header: code là 400 (`S/async-batch.ts:27`); Go chọn chạy đồng bộ để client không gửi header không bị ảnh hưởng — **cần duyệt** (Q-17.1).
6. **Kích thước**: giữ trần 16 MB chung; nếu cần batch lớn hơn thì thêm `FHIR_MAX_BATCH_BYTES` và cho POST gốc vào danh sách tự đặt trần (`ownsBodyLimit`, `httpapi.go:30`), làm sau.

Lưu ý: Binary của job đi qua `JobRepository`, **không** qua `fhirbinary.Wrap` (như bulk export, `apps/fhir-server/internal/bootstrap/container.go:162-163`), nên nằm trong CSDL dạng base64. Batch rất lớn sẽ phình bảng resource; chấp nhận cho đợt đầu, ghi vào giới hạn.

### A.4 Kiểm thử

- `pkg/fhirrouter`: async trả 202 + `Content-Location`; status 202 rồi 200 với `results`; Bundle kết quả giống hệt chạy đồng bộ cùng đầu vào (so từng entry).
- Lỗi đầu vào trả 400 **không** tạo AsyncJob; `transaction` + async trả 400; cờ tắt thì chạy đồng bộ.
- Quyền: token chỉ có scope `Patient.c` thì entry PUT Observation trong kết quả là 403, không phải toàn job lỗi; job chạy đúng project (không ghi sang project khác).
- Checkpoint: giết job sau entry thứ k (hủy context giả lập tắt), chạy lại → không có bản trùng của POST đã cấp id; số entry chạy lại ≤ cadence.
- Hủy (`DELETE job/...`) giữa chừng → dừng ở checkpoint kế tiếp, Binary đoạn bị dọn.
- `ExpireJobs` xóa job batch và Binary kết quả.
- Repo SQLite và PostgreSQL qua `repotest` (PostgreSQL chạy bằng `make test-pg`).

---

## Phần B. Tự tải attachment URL ngoài (`download`)

### B.1 Hành vi Medplum

- Kích hoạt sau mỗi lần ghi resource (worker `dispatch`, `S/workers/dispatch.ts:186`), khi server bật `autoDownloadEnabled` (mặc định `true`, `S/config/utils.ts:48`).
- Duyệt mọi phần tử kiểu `Attachment` theo schema (`getAttachments`, cuối `S/workers/download.ts`); chỉ lấy URL "ngoài": không phải `Binary/`, không phải URL Binary hay storage của chính server, và qua `isAllowedOutboundUrlForQueue` (`:154-171`).
- Lọc theo project (`:175-194`): setting `autoDownloadEnabled=false` tắt hẳn; `autoDownloadAllowedUrlPrefixes` (danh sách cho phép) và `autoDownloadIgnoredUrlPrefixes`.
- Bỏ qua nếu URL ở đường dẫn đó không đổi so với phiên bản trước (`:126-129`); chấp nhận xếp hàng trùng.
- Job (`execDownloadJob`, `:213`): đọc lại resource (đã xóa thì thôi; không còn chứa URL thì thôi), kiểm lại project; `safeFetch` (`:259`) có `traceparent`; status ≥ 400 là lỗi (để hàng đợi thử lại).
- Tạo `Binary` với `contentType` theo phản hồi, `meta.project` của resource, `securityContext` trỏ về resource; ghi nội dung vào kho; đọc lại resource rồi PATCH mọi attachment có URL đó thành `Binary/<id>` kèm `meta.author = system`, có `ifMatch` (`:319`).

### B.2 Hiện trạng Go

- Không có worker `dispatch`. Sự kiện ghi resource có sẵn: `fhirsub.Wrap` phát `ChangeEvent` lên broker sau khi commit (`pkg/fhirsub/event.go`), **nhưng** chỉ khi `FHIR_SUBSCRIPTIONS=true` (`container.go:113-118`, `:156`, `:171`). `ChangeEvent` mang phiên bản mới, **không** mang phiên bản trước.
- `pkg/messaging` có Redis Streams at-least-once, thử lại, dead-letter: dùng làm hàng đợi bền được.
- `pkg/safehttp` (chống SSRF), `pkg/binarystore` và `pkg/fhirbinary` (ghi nội dung Binary ra kho) đã có.
- `fhirbinary.Rewriter` duyệt cây JSON theo khóa (`pkg/fhirbinary/rewrite.go:82`), **không** theo kiểu `Attachment`.

### B.3 Thiết kế

1. **Gói**: `pkg/fhirbinary/download.go` (cùng chỗ với kho Binary và Rewriter); không cần gói mới, nên không phải thêm luật vào `architecture_test.go` ngoài phụ thuộc `pkg/safehttp` và `pkg/messaging` cho `pkg/fhirbinary` (kiểm lại `pkgRules` khi làm).
2. **Kích hoạt**: handler đăng ký trên topic `fhirsub.EventType`. Tách việc dựng broker khỏi `FHIR_SUBSCRIPTIONS`: dựng broker và bọc `fhirsub.Wrap` khi **hoặc** bật Subscription **hoặc** bật tự tải.
3. **Lọc** (khi nhận sự kiện `create`/`update`):
   - Tìm attachment theo **kiểu** (`Attachment`) bằng schema trong `pkg/fhir`, không theo tên khóa `url` (tránh tải nhầm `Extension.url`, `Reference`...). API schema cần dùng: chưa xác nhận (mục 7).
   - URL ngoài: `http(s)` tuyệt đối, không phải `Binary/...`, không phải gốc FHIR hay `/storage` của chính server (dùng `RewriteConfig` sẵn có).
   - Thiếu phiên bản trước nên không lọc được "URL không đổi"; thay bằng: chỉ tải URL ngoài **còn** trong resource lúc chạy; tải xong thì URL thành `Binary/<id>` nên lần ghi do chính worker gây ra không kích hoạt lại. Hệ quả: một resource có URL ngoài bị sửa trường khác sẽ xếp hàng lại; job thấy URL đã đổi thì bỏ qua.
4. **Thực thi** (trong handler, broker lo thử lại):
   - Đọc lại resource bằng repo hệ thống (không tách project); 404/410 thì thôi.
   - Tải qua `pkg/safehttp` với timeout, giới hạn redirect, **trần kích thước** `FHIR_AUTO_DOWNLOAD_MAX_BYTES` (mặc định bằng `FHIR_STORAGE_MAX_UPLOAD_BYTES`); kiểm `Content-Type` bằng `binarystore.CheckMetadata` (bỏ tham số trước khi kiểm, đúng phát hiện ở `email-storage.md`).
   - Tạo Binary (`meta.project` của resource, `securityContext` trỏ về resource), ghi nội dung qua kho Binary.
   - Đọc lại resource, PATCH các attachment có đúng URL đó với `If-Match`; xung đột phiên bản thì trả lỗi để broker thử lại; không còn attachment khớp thì xóa Binary vừa tạo (Medplum để mồ côi; Go dọn luôn).
   - Log: ghi host và resource, **không** ghi query string của URL (có thể chứa token ký).
5. **Cấu hình**:
   - `FHIR_AUTO_DOWNLOAD` mặc định **`false`** (khác Medplum mặc định bật): gọi URL do người dùng cung cấp là bề mặt SSRF và chi phí, nên bật có chủ ý (Q-17.4).
   - Bắt buộc `FHIR_BINARY_STORAGE`; thiếu thì từ chối khởi động khi bật.
   - `FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES`, `FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES` cấp server, thay cho setting theo project (Q-17.5).

### B.4 Kiểm thử

- Tạo DocumentReference có `content.attachment.url` trỏ tới `httptest` → sau khi worker chạy, URL thành `Binary/<id>`, nội dung khớp, `meta.project` và `securityContext` đúng.
- Không tải: URL `Binary/...`, URL storage của chính server, `Extension.url`, URL bị `safehttp` chặn (127.0.0.1, 169.254.169.254), URL ngoài danh sách cho phép, URL trong danh sách bỏ qua.
- Phản hồi 404/500 → thử lại theo broker rồi vào dead-letter; vượt trần kích thước → bỏ, không tạo Binary.
- Resource bị xóa hoặc đổi URL trước khi job chạy → không tạo Binary; ghi đè song song → `If-Match` xung đột rồi thử lại thành công.
- Không vòng lặp: lần PATCH của worker không xếp thêm job tải.
- Redis Streams (miniredis) cho đường hai instance; e2e với `fhir-server` thật.

---

## Phần C. Tự tắt Subscription lỗi kéo dài

### C.1 Hành vi Medplum

- Cấu hình server `subscriptionAutoDisable`: mảng trigger `{maxConsecutiveFailures, timeWindowSeconds}` (`S/config/types.ts:205-207`); không có giá trị mặc định trong `S/config/utils.ts` (nên mặc định tắt). Ghi đè theo project bằng `Project.systemSetting[name=subscriptionAutoDisable]` (JSON), sai định dạng thì quay về cấu hình server.
- Một "thất bại" chỉ được đếm khi một sự kiện **đã hết lượt thử** (`S/workers/subscription.ts:1031-1038`); giao thành công thì xóa bộ đếm (`:733`).
- Đếm bằng Redis sorted set `medplum:sub:failures:<id>`, điểm là thời gian: tỉa phần cũ, thêm mục, `ZCOUNT` cho từng cửa sổ, `EXPIRE` theo cửa sổ lớn nhất. Trigger nào đạt ngưỡng thì tắt. (Tên "consecutive" nhưng thực chất là "số lần trong cửa sổ, kể từ lần thành công gần nhất".)
- Tắt (`autoDisableSubscription`, `:1052-1091`): trong một transaction, PATCH `test /status = active`, `replace /status = off`, `add /error = "Automatically disabled after N consecutive failed events in the last W seconds"`, và tạo `AuditEvent`; rồi xóa bộ đếm. Lỗi khi tắt chỉ ghi log.
- Số lượt thử của một sự kiện ghi đè được bằng extension `subscription-max-attempts` trên Subscription (`:1027-1029`).

### C.2 Hiện trạng Go

- `Manager.attempt` (`pkg/fhirsub/deliver.go:70-112`): thành công thì đếm `Delivered`; lỗi không thử lại được, hoặc hết `MaxAttempts` (`FHIR_WEBHOOK_MAX_ATTEMPTS`, mặc định 5, `apps/fhir-server/internal/config/config.go:211,289`) thì **bỏ cuộc và chỉ ghi log** (`:96-101`). Địa chỉ bị chặn SSRF bỏ cuộc ngay (`:93-95`).
- Hàng đợi giao và lượt thử nằm trong bộ nhớ; chưa đọc extension `subscription-max-attempts`.
- Không lưu `AuditEvent` vào kho (đã ghi ở `docs/go-port-status.md`, 11d).
- PATCH hỗ trợ `test` (`pkg/fhirrouter/patch.go:75`).

### C.3 Thiết kế

1. **Cổng đếm** trong `pkg/fhirsub`: `FailureTracker { Record(ctx, subID, triggers) (fired *Trigger, count int, err error); Clear(ctx, subID) error }`, hai bản:
   - bộ nhớ (một replica, dev, test);
   - Redis: sorted set, cùng thuật toán và cùng dạng khóa (`trivita:sub:failures:<id>`), chạy trong một pipeline như Medplum. Dùng Redis khi `REDIS_URL` có và broker là `redis`; nhiều replica mà dùng bản bộ nhớ thì mỗi replica đếm riêng (ghi vào giới hạn).
2. **Gắn vào `attempt`**: nhánh bỏ cuộc (`deliver.go:96`) gọi `Record`; nhánh 2xx (`:89`) gọi `Clear`. Lỗi của chính tracker chỉ ghi log, không ảnh hưởng việc giao.
3. **Tắt**: PATCH Subscription bằng repo hệ thống với `test /status = active`, `replace /status = off`, `add /error = <cùng câu Medplum>`; xung đột hoặc `test` sai (đã bị tắt hay đổi) thì bỏ qua. Sau đó `Clear` và đánh dấu cache Subscription cần nạp lại (`Manager.invalid`). Ghi log cảnh báo có `subscription`, `project`, `failureCount`, `timeWindowSeconds`. **Không** tạo `AuditEvent` vì Go chưa lưu AuditEvent; làm cùng việc AuditEvent sau (ghi ở mục 7).
4. **Cấu hình**: `FHIR_SUBSCRIPTION_AUTO_DISABLE` = JSON mảng trigger như Medplum, rỗng = tắt (mặc định). Kiểm định dạng lúc khởi động (`maxConsecutiveFailures` và `timeWindowSeconds` là số nguyên dương), sai thì từ chối khởi động.
5. **Không làm đợt này**: ghi đè theo project (`fhir-server` không có `Project`, Q-17.6); extension `subscription-max-attempts` (việc nhỏ, có thể làm kèm nếu được duyệt).

### C.4 Kiểm thử

- Endpoint luôn 500, trigger `{3, 60}` → sau sự kiện thứ 3 hết lượt thử, Subscription `status=off`, `error` đúng câu; sự kiện sau không còn được giao.
- Xen một lần 2xx → bộ đếm về 0, không tắt.
- Lỗi ngoài cửa sổ thời gian không tính (đồng hồ giả trong tracker).
- Hai trigger khác cửa sổ: trigger nào đạt trước thì tắt.
- Subscription đã bị người dùng đổi `status` trước lúc tắt → `test` sai, không ghi đè.
- Bản Redis với miniredis: hai `Manager` dùng chung Redis cộng dồn đúng.
- Cấu hình sai định dạng → `config` báo lỗi.
- Không đổi hành vi khi không cấu hình trigger (test hiện có của `pkg/fhirsub` vẫn xanh).

---

## 4. Thứ tự, cỡ, phụ thuộc

| Bước | Việc | Cỡ | Phụ thuộc |
|---|---|---|---|
| C1 | `FailureTracker` bộ nhớ + gắn vào `attempt` + tắt Subscription + cấu hình | S | Q-17.6 (chỉ để biết có cần ghi đè theo project) |
| C2 | `FailureTracker` Redis | S | C1 |
| A1 | Trạng thái job (`Input.JobID`, `SaveJobState`), `outputBinaryIDs` đọc `valueReference`, `ExpireJobs` nhận `batch?` | S | — |
| A2 | Batch async không checkpoint (chạy lại từ đầu khi tắt) + cờ `FHIR_ASYNC_BATCH` | M | A1, Q-17.1, Q-17.2 |
| A3 | Checkpoint, giữ id POST, hủy giữa chừng | M | A2 |
| B1 | Tách dựng broker khỏi `FHIR_SUBSCRIPTIONS`; lọc attachment theo kiểu | S | Q-17.4 |
| B2 | Worker tải + PATCH + cấu hình + dọn Binary | M | B1, `FHIR_BINARY_STORAGE`, Q-17.5 |

Mỗi bước: test đơn vị trước (TDD như các phần khác), cập nhật `apps/fhir-server/README.md`, `.env.example`, `docs/config.md` sinh từ code (nếu có), `docs/go-port-status.md`, và đánh dấu trong `task-list.md`.

## 5. Câu hỏi cần chốt

| # | Câu hỏi | Đề xuất |
|---|---|---|
| Q-17.1 | Bật batch async thế nào khi `fhir-server` không có `Project.features`; có header mà tắt thì 400 (như Medplum) hay chạy đồng bộ? | Cờ server `FHIR_ASYNC_BATCH`, mặc định tắt; tắt thì chạy đồng bộ |
| Q-17.2 | Transaction async: từ chối (400) hay chạy nguyên tử trong job? | Từ chối, như Medplum khi bật transaction thật |
| Q-17.3 | Nhịp checkpoint mặc định | 10 entry hoặc 5 giây, như Medplum |
| Q-17.4 | Tự tải attachment mặc định bật hay tắt | **Tắt** (Medplum bật) vì là bề mặt SSRF và chi phí |
| Q-17.5 | Danh sách cho phép/bỏ qua theo project hay chỉ cấp server | Cấp server đợt đầu; theo project khi có claim hoặc API lấy cài đặt project từ `auth` |
| Q-17.6 | Ghi đè trigger tự tắt theo project | Chưa làm; cùng cơ chế với Q-17.5 |
| Q-17.7 | Có cần `AuditEvent` khi tự tắt không | Chỉ log đợt này; làm khi có việc lưu AuditEvent |

## 6. Tiêu chí xong

- `go test ./...` xanh, gồm test kiến trúc; các test mới ở mục A.4, B.4, C.4; `make test-pg` chạy bộ `repotest` có batch async.
- Ba tính năng **mặc định tắt**; không bật thì hành vi và test hiện có không đổi.
- Tài liệu cấu hình và `go-port-status.md` ghi giới hạn còn lại (Binary của job nằm trong CSDL, tracker bộ nhớ không dùng chung giữa replica, không có AuditEvent, không có cài đặt theo project).

## 7. Chưa xác nhận

- Hành vi ở Medplum `v5.2.18` (bản so là 5.2.17-5).
- API của `pkg/fhir` để duyệt phần tử theo kiểu `Attachment` (cần đọc `pkg/fhir` schema trước khi làm B1); nếu không có, phải sinh thêm bảng đường dẫn Attachment bằng `tools/fhirgen`.
- `pkgRules` hiện cho `pkg/fhirbinary` phụ thuộc những gói nào (`architecture_test.go`); B2 cần `pkg/safehttp` và `pkg/messaging`.
- Medplum tiền xử lý batch (`BatchProcessor.preprocess`) làm gì ngoài cấp id POST (thứ tự entry, conditional create); chưa đọc `S/fhir/batch*.ts`.
- `S/workers/subscription.ts` có tính lỗi "preamble" (không nạp được Subscription hay resource, `MAX_PREAMBLE_ATTEMPTS`) vào bộ đếm tự tắt hay không; đọc kỹ trước C1.
