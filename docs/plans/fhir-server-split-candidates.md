# Kế hoạch: tách `apps/fhir-server` thành các service, mỗi service một vai trò

Trạng thái: **đề xuất; hướng tách đã chốt theo yêu cầu của người dùng (2026-11-24): các service riêng, mỗi service một vai trò, không dùng vai trò `FHIR_ROLE` trong cùng image**. Mã chưa đổi. Nguyên tắc chung: [../service-principles.md](../service-principles.md) (điều kiện A/B/C/D, danh sách kiểm service mới mục 7). Bổ sung cho [microservices-shared-db.md](microservices-shared-db.md) mục 14 (P3a: điều tra `fhir-worker`, D-DB-3 đã chốt "chưa tách, chờ đo tải") và [modules/split-scim-ws-services.md](modules/split-scim-ws-services.md) (đã tách `ws-gateway`).

> **Thay đổi so với bản trước của tài liệu này:** bản trước đề xuất bậc 2 của thang tách (cùng binary, vai trò `FHIR_ROLE`). Theo chỉ đạo, kế hoạch nay đi thẳng bậc 3 (`apps/<tên>` riêng, image riêng). Đây là **lệch có chủ ý** khỏi mục 1 của nguyên tắc ("chỉ lên bậc 3 khi bậc 2 không đủ"); hệ quả về chi phí ở mục 6 và 8.

**Phạm vi bằng chứng:** đã đọc mã lắp ráp (`internal/bootstrap/container.go`), `pkg/fhirrouter`, `pkg/fhirsub`, `pkg/fhirbinary`, `pkg/messaging`, `pkg/fhirdownload`, các kế hoạch hiện có. **Chưa có số đo tải thật** (mục 9): nhận định "đáng tách" là suy luận từ mã. Câu ghi "(chưa kiểm)" là chưa chạy hoặc chưa đọc tới mã.

## 0. Kết luận

| Service mới | Thư mục | Cổng (đề xuất) | Vai trò | Điều kiện tách | Gắn kết | Thứ tự |
|---|---|---|---|---|---|---|
| **`fhir-downloader`** | `apps/fhir-downloader` | `:8009` (chỉ probe) | tự tải attachment URL ngoài về kho Binary (`pkg/fhirdownload`) | **C mạnh** (bề mặt SSRF, ra Internet), A | thấp | 1 |
| **`fhir-notifier`** | `apps/fhir-notifier` | `:8010` (chỉ probe) | giao rest-hook, khớp sự kiện cho websocket (`fhirsub.Manager`) | C, A | trung bình | 2 (kèm làm rest-hook bền) |
| **`fhir-worker`** | `apps/fhir-worker` | `:8011` (chỉ probe) | job nền: bulk export, `$reindex`, batch async, vòng bảo trì job | C một phần, A chưa đo | trung bình đến thấp | 3 (sau khi sửa P0-2, P0-3) |
| `fhir-server` (giữ nguyên tên) | `apps/fhir-server` | `:8005` | **API FHIR**: REST, tìm kiếm, xác thực, terminology, Binary, GraphQL; sở hữu migration; **phát** sự kiện | lõi | | không đổi |

**Không tách:** terminology (nằm trong đường tìm kiếm), Binary và `/storage` (chưa), REST, kho, tìm kiếm, xác thực (không tách theo loại resource). Websocket đã là `ws-gateway`.

Điểm chung: **không service nào đạt điều kiện B** (dữ liệu hoặc bí mật riêng): cùng schema `fhir`; vì bảng của `fhirstore` là kho tài liệu chung (`fhir_resource` chứa mọi loại), **không thể cấp quyền DB hẹp theo loại resource**, nên chia role DB không thu hẹp được gì. Lợi ích thật của việc tách là: (1) **chính sách mạng** (chỉ `fhir-downloader` ra Internet; chỉ `fhir-notifier` gọi webhook), (2) **tách tải và miền lỗi**, (3) **tách bí mật** (`fhir-notifier` và `fhir-downloader` không cần `FHIR_JOB_SIGNING_KEY` hay `FHIR_WS_SIGNING_KEY` của job). Phải ghi trung thực như vậy ở mục "Vì sao tách" của từng service (DoD mục 7.1).

## 1. Hiện trạng (bằng chứng)

`fhir-server` là một tiến trình lắp ráp các thành phần sau trong `container.go` (`build`); thành phần có điều kiện bật bằng cờ:

| Thành phần | Bật khi | Cách nối | Bằng chứng |
|---|---|---|---|
| Kho `fhirstore` và router REST | luôn | `fhirrouter.New(routerRepo, Options{...})` | `container.go` |
| Xác thực, tách project | luôn (JWT hoặc token tĩnh) | `fhirauth.Scope` bọc kho; job nền dựng lại `Principal` từ `AsyncJob.Meta.Project` | `container.go` (`scopedJobRepo`, `jobContext`) |
| Job nền | luôn | `NewOrchestrator` luôn trả `workflow.NewInline(run, FHIR_JOB_CONCURRENCY)`; adapter `asynq` có trong `pkg/workflow` nhưng không chỗ nào ngoài test gọi | `container.go`; P3a |
| Broker sự kiện | `FHIR_SUBSCRIPTIONS` **hoặc** `FHIR_AUTO_DOWNLOAD` | `messaging.New`: `memory` hoặc `redis` (Redis Streams, consumer group `trivita`, at-least-once, ACK, poison message sang `{stream}:dead`) | `container.go`; `pkg/messaging/redis.go` |
| Phát sự kiện sau commit | broker có | `fhirsub.Wrap(store, broker)` bọc kho | `container.go` |
| Giao rest-hook | `FHIR_SUBSCRIPTIONS` | `fhirsub.NewManager(store, ...)` đọc Subscription từ **kho gốc**; hàng đợi giao là `chan` trong tiến trình, **sức chứa 1024, đầy thì bỏ (`Dropped`)**, 4 worker mặc định, thử lại `FHIR_WEBHOOK_MAX_ATTEMPTS` (5) bằng hẹn giờ trong bộ nhớ | `pkg/fhirsub/manager.go:106,116,366`; `config.md` |
| Khớp sự kiện websocket | `FHIR_WS_MODE` | `embedded`: Hub trong tiến trình; `issuer`: `Manager.AttachRemote` (khớp một lần cho cả cụm, đẩy lên kênh Redis; cần `BROKER_DRIVER=redis`) | `container.go` |
| Binary thô | `FHIR_BINARY_STORAGE` | `fhirbinary.Wrap(store, blobs, ...)` **bọc ghi Binary, nằm dưới sự kiện Subscription**; `RawBinary` bọc cả router FHIR; `/storage` là handler riêng đọc kho gốc | `container.go` |
| Tự tải attachment | `FHIR_AUTO_DOWNLOAD` (cần `FHIR_BINARY_STORAGE`) | `fhirdownload.New(...)`; `Register(broker)` nghe `fhirsub.EventType`, tải URL ngoài qua `pkg/safehttp`, ghi `Binary` rồi đổi `url` attachment thành `Binary/<id>`; `Async` khi broker là `memory` (hàng đợi trong tiến trình, thử lại 3 lần lùi 1 giây, 2 giây) | `container.go`; `pkg/fhirdownload/download.go` (**đang làm dở, chưa commit khi viết tài liệu này**) |
| Terminology | luôn | `resolveTerminology` nằm **trong đường tìm kiếm** (`:below`, `:above`, `:in`); `$expand`, `$lookup`... dùng bảng dẫn xuất `fhir_coding*` cùng schema | `pkg/fhirrouter/chain.go:37`, `termsearch.go` |
| Kết quả bulk export | luôn | `Binary` base64 **trong CSDL** (không vào kho `file:`/`s3:` dù đã bật), mỗi tệp khoảng 8 MiB | `pkg/fhirrouter/bulk.go:178`; `container.go` |

## 2. Đối chiếu tiêu chí tách

| Service | A. Hồ sơ tải khác | B. Dữ liệu/bí mật riêng | C. Miền lỗi, vòng đời deploy | D. Đội khác |
|---|---|---|---|---|
| `fhir-downloader` | Có thể: tải tệp lớn, I/O ra ngoài (chưa đo) | không (cùng schema; cần ghi `Binary` và đọc kho) | **Có**: API không nên có đường ra Internet; sự cố máy chủ ngoài không ảnh hưởng API | không |
| `fhir-notifier` | Có thể: tải theo số sự kiện × số Subscription, chờ I/O ngoài (chưa đo) | không (nhưng giữ `subscription-secret` và `channel.header`: tách khỏi API) | **Có một phần**: webhook chậm chiếm worker và bộ nhớ của API; restart API mất hàng đợi | không |
| `fhir-worker` | chưa đo | không | **Một phần**: export lớn có thể làm API chậm (chưa quan sát) | không |

## 3. Từng service

### 3.1 `fhir-downloader` (ưu tiên số 1)

- **Vì sao tách:** tải URL do người dùng cung cấp là bề mặt SSRF và chi phí; mặc định đang tắt vì vậy. Service riêng cho phép **API chạy không có quyền ra Internet** (chính sách mạng egress chỉ mở cho service này). Gắn kết thấp: chỉ nghe sự kiện từ broker, đọc resource, ghi `Binary` và cập nhật resource bằng đúng project của resource.
- **Dữ liệu:** dùng chung schema `fhir`, role `fhir_app`, và kho Binary (tiền lệ `dicomweb`, D-DW-1: hai cửa vào của cùng một miền dữ liệu); không tự migrate, từ chối khởi động khi `fhirstore.Pending > 0`.
- **Phụ thuộc chạy:** PostgreSQL, **Redis (bắt buộc, broker `redis`)**, kho Binary, egress Internet. **Không cần:** `FHIR_JOB_SIGNING_KEY`, `FHIR_WS_SIGNING_KEY`, JWKS của `auth`, không phục vụ token người dùng.
- **Scale theo:** số sự kiện ghi có attachment ngoài, và thời gian tải.
- **Độ bền:** thừa hưởng broker Redis (consumer group, at-least-once, thử lại, dead-letter); cần kiểm idempotency khi nhận lại cùng sự kiện (chưa kiểm trong `download.go`).
- **Việc cần có ở `fhir-server`:** `fhir-server` **vẫn phát** sự kiện (`fhirsub.Wrap`, cần broker) nhưng **không** đăng ký worker tải: một công tắc tắt phía nghe ở API (tên chốt ở S1; mặc định giữ hành vi hiện tại để không đổi gì khi chưa tách).

### 3.2 `fhir-notifier` (ưu tiên số 2)

- **Vì sao tách:** webhook chậm hoặc chết chiếm worker và hàng đợi 1024 trong bộ nhớ của API; khớp `criteria` mỗi sự kiện tốn CPU theo số Subscription. Về mã, bên nghe **có thể ở tiến trình khác** khi dùng Redis: `fhirsub.Wrap` đã phát sự kiện sau commit và Manager đọc Subscription từ kho gốc.
- **Điểm yếu service riêng không sửa hộ:** hàng đợi giao nằm trong bộ nhớ, restart là mất thông báo đang chờ và đang hẹn thử lại; đầy thì bỏ (P3a). Phải **làm bền cùng lúc** (P0-1), nếu không chỉ chuyển nơi mất dữ liệu.
- **Cách làm bền (đề xuất, chưa thử):** (a) giao **đồng bộ trong handler của Redis Stream**, chỉ ACK khi giao xong hoặc bỏ cuộc theo `MaxAttempts` (dùng PEL, `ClaimInterval`, dead-letter có sẵn); (b) bảng outbox trong schema `fhir`; (c) `asynq`. (a) rẻ nhất vì không thêm hạ tầng; đổi lại một webhook chậm giữ một message PEL lâu.
- **Phần `ws`:** `AttachRemote` (khớp một lần cho cả cụm, đẩy lên kênh) chuyển sang `fhir-notifier`. Cấp token bind (`$get-ws-binding-token`) **vẫn ở `fhir-server`** vì cần kiểm quyền người gọi theo kho đã tách project.
- **Dữ liệu:** schema `fhir`, role `fhir_app`; cần đọc Subscription và cập nhật `status=off` khi tự tắt (ghi có `If-Match`).
- **Phụ thuộc chạy:** PostgreSQL, **Redis (bắt buộc)**, egress tới endpoint webhook (qua `pkg/safehttp`), `FHIR_WS_SIGNING_KEY` nếu còn websocket. **Không cần:** JWKS, `FHIR_JOB_SIGNING_KEY`.
- **Việc cần có ở `fhir-server`:** công tắc tắt phía giao và khớp (như 3.1).

### 3.3 `fhir-worker` (ưu tiên số 3)

- **Trạng thái:** D-DB-3 đã chốt "chưa tách, chờ đo". Kết luận P3a còn đúng: job `AsyncJob` đã bền nhờ lease trong DB (nhận bằng If-Match, gia hạn, tiếp quản sau `JobRecoveryGrace` và lease hết hạn), nên **thiếu động lực kỹ thuật cấp bách**; điều kiện A chưa có số đo. Theo chỉ đạo, kế hoạch vẫn đặt service này; **thứ tự giữ sau S4 (đo #12c)** để mục "Vì sao tách" có bằng chứng (DoD 7.1, 7.10). Nếu quyết định tách vì lý do vận hành mà không đo, phải ghi rõ đó không phải điều kiện A.
- **Hai việc phải xong trước:**
  1. Kết quả export đang là `Binary` base64 trong CSDL (`bulk.go:178`): đưa job sang service khác **không giảm tải DB**, và CSDL phình theo `FHIR_EXPORT_RETENTION`. Cần ghi kết quả ra kho đối tượng (P0-2).
  2. `$reindex` cả loại dùng kho thô (`operation.go:298`: `reindexType` dùng `r.repo`, trong khi `reindexOne` dùng `repoOf(ctx)`): admin một project khởi động được reindex mọi project. Sửa trước (P0-3), nếu không `fhir-worker` thực thi lỗi này.
- **Cách chạy (suy luận, chưa thử):** `fhir-server` nhận yêu cầu, tạo `AsyncJob` ở `accepted` rồi **không chạy** (cần thêm tùy chọn trong `fhirrouter.Options`, mặc định giữ như cũ: chạy tại chỗ); `fhir-worker` chạy vòng `RecoverJobs` và nhận job bằng lease. Độ trễ nhận job ≥ `JobRecoveryGrace` cộng chu kỳ bảo trì; có thể thêm `asynq` sau (adapter đã có chưa nối).
- **Dữ liệu và bí mật:** schema `fhir`, role `fhir_app`; cần **`FHIR_JOB_SIGNING_KEY` dùng chung** với `fhir-server` (job-seal: job không có chữ ký hợp lệ báo lỗi chứ không chạy); đọc kho Binary nếu export ghi ra kho.
- **Phụ thuộc chạy:** PostgreSQL, kho Binary; Redis không bắt buộc cho việc nhận job (nhận qua DB).

### 3.4 Không tách: Binary và `/storage`, terminology

- **Binary và `/storage`:** `/storage` tách được rẻ, nhưng phần Binary trong đường FHIR gắn chặt: `fhirbinary.Wrap` nằm dưới sự kiện Subscription, `RawBinary` bọc cả router, rewriter đổi tham chiếu thành URL ký ở mọi phản hồi; tách cần một API nội bộ chưa có. Tiền lệ `dicomweb` (dùng chung qua thư viện và DB) là mô hình đã chạy. [microservices-shared-db.md](microservices-shared-db.md) mục 3: dịch vụ nền chỉ tách khi có hơn một nơi gọi; chưa thành lý do. Xem lại nếu đo cho thấy tải lớn làm nghẽn API.
- **Terminology:** `resolveTerminology` chạy **trước mỗi truy vấn có `:below`, `:above`, `:in`** (`chain.go:37`) và đọc `fhir_coding*` cùng schema; tách là thêm một bước mạng vào đường tìm kiếm mà DB vẫn dùng chung. Nếu `$expand` lớn hay `$import` làm nghẽn thật, chỉ đẩy **riêng tác vụ nặng** sang `fhir-worker`.

## 4. Khuôn chung của mỗi service mới

Theo [service-principles.md](../service-principles.md) mục 3, 6, 7; tiền lệ gần nhất là `apps/dicomweb` (dùng chung schema `fhir`, vỏ mỏng) và `apps/ws-gateway` (mẫu DoD):

| Việc | Nội dung |
|---|---|
| Cấu trúc | `cmd/<tên>/main.go`, `internal/{config,presentation,bootstrap}` (vỏ mỏng, C-5); **không import `apps/fhir-server`** (C-1): phần lắp ráp riêng được viết lại trong `bootstrap` của service (khoảng vài chục dòng mỗi cụm), nghiệp vụ dùng chung đã ở `pkg/` |
| Kiến trúc | Thêm luật cho `apps/<tên>` vào `architecture_test.go` (C-4); `go test .` xanh |
| Cấu hình | `internal/config` dùng `pkg/envconfig`; **giữ nguyên tên biến đang có** (`FHIR_WEBHOOK_*`, `FHIR_AUTO_DOWNLOAD*`, `FHIR_BINARY_STORAGE`, `DATABASE_URL`, `REDIS_URL`) để dùng chung một tệp môi trường như `dicomweb`; `docs/config.md` sinh từ mã; `.env.example`; từ chối cấu hình sai và gom mọi lỗi |
| Dữ liệu | Không migration riêng: **`fhir-server` sở hữu migration**; service từ chối khởi động khi `fhirstore.Pending > 0` (như `dicomweb`); `DATABASE_REQUIRE_SCHEMA=true` |
| Probe và tắt êm | `/healthz`, `/health/live`, `/health/startup`, `/health/ready` (kiểm DB và broker); tắt êm có test (SIGTERM, mã thoát 0; nhả lease/PEL để nơi khác tiếp quản) |
| Image | `apps/<tên>/Dockerfile` riêng, người dùng không root, `HEALTHCHECK`, `Dockerfile.dockerignore`; service compose (`docker-compose.<tên>.yml` hoặc gộp) |
| Test | Bốn tầng theo [service-independence.md](service-independence.md): đơn vị cạnh mã, `tests/integration` (Redis và PostgreSQL thật), `tests/e2e` (dựng image, chạy cùng `fhir-server`), đột biến vào đường quan trọng |
| Tài liệu | `README.md`, `docs/{architecture,config,testing,runbook}.md` với mục "Vì sao tách" (mục 3 của tài liệu này là nguyên liệu), mục trong bản đồ service ở `service-principles.md` mục 2 |
| Hợp đồng | Không có route nghiệp vụ nên không có swagger/api-contract; ghim bằng test **hợp đồng dây**: định dạng sự kiện trên broker (`fhirsub.EventType`, C-6), claim và chữ ký `job-seal` (cho `fhir-worker`) |

## 5. Điều kiện tiên quyết

Các việc dưới đây độc lập với chuyện tách nhưng **làm việc tách mất ý nghĩa nếu chưa xong** (đã đối chiếu trong mã, trừ điểm ghi khác):

| Mã | Việc | Vì sao | Cỡ |
|---|---|---|---|
| P0-1 | Rest-hook bền (một trong a, b, c ở 3.2) | Tách `fhir-notifier` không sửa mất dữ liệu khi chết hoặc đầy hàng đợi | M |
| P0-2 | Kết quả bulk export ghi ra kho đối tượng | `fhir-worker` không giảm tải DB; CSDL phình theo retention | M |
| P0-3 | `$reindex` cả loại dùng kho đã tách project, kiểm AccessPolicy ở đường tạo job | `fhir-worker` thực thi lỗi xuyên project | S |
| P0-4 | Cấm broker `memory` khi bật phía nghe bên ngoài | Các service tách chỉ hoạt động qua Redis | S |
| P0-5 | Chốt và commit `pkg/fhirdownload`, mô tả đủ biến cấu hình (`IGNORED_PREFIXES`...) | `fhir-downloader` là việc đầu tiên nhưng mã còn dở | S đến M |

## 6. Quyết định

| Mã | Câu hỏi | Quyết định / đề xuất | Hệ quả nếu chọn khác |
|---|---|---|---|
| D-SP-1 **[đã chốt 2026-11-24 theo yêu cầu của người dùng]** | Tách bằng gì? | **Các service riêng (`apps/fhir-downloader`, `apps/fhir-notifier`, `apps/fhir-worker`), mỗi service một vai trò**; không dùng `FHIR_ROLE` | Vai trò trong cùng image rẻ hơn (một image, một CI) và đủ cho điều kiện C, nhưng không cho image, network policy và vòng đời deploy riêng |
| D-SP-2 | Thứ tự | `fhir-downloader`, rồi `fhir-notifier` (kèm P0-1), rồi `fhir-worker` (sau S4 và P0-2, P0-3) | `fhir-notifier` không có P0-1 chuyển nơi mất dữ liệu; `fhir-worker` trước khi đo thì thiếu bằng chứng điều kiện A |
| D-SP-3 | Redis | **Bắt buộc** cho `fhir-downloader` và `fhir-notifier`; broker `memory` bị cấm khi phía nghe ở ngoài (P0-4) | Broker `memory` chỉ chạy một tiến trình |
| D-SP-4 | Cách làm rest-hook bền | (a) giao đồng bộ trong handler Redis Stream, ACK sau khi giao | (b) outbox (thêm bảng, thêm bảo trì); (c) `asynq` (thêm phụ thuộc vận hành, đã có adapter chưa nối) |
| D-SP-5 | Role DB của các service mới | **Dùng chung `fhir_app`** như `dicomweb` (D-DW-1): kho tài liệu chung nên không cấp hẹp theo loại resource được | Role riêng cùng quyền chỉ giúp thống kê kết nối và thu hồi riêng: có thể làm sau, cần sửa `01-schemas.sh` |
| D-SP-6 | Binary và `/storage`, terminology | Không tách (3.4) | Thêm API nội bộ và một bước mạng vào đường tìm kiếm |
| D-SP-7 | Đo trước khi tách `fhir-worker` (#12c)? | Có (khớp D-DB-3) | Bỏ đo thì phải ghi đó là lý do vận hành, không phải điều kiện A |
| D-SP-8 | Dùng chung schema `fhir` cho 5 app (`fhir-server`, `dicomweb`, `fhir-notifier`, `fhir-downloader`, `fhir-worker`) | Chấp nhận như ngoại lệ D-DW-1 (cùng một miền dữ liệu), kèm quy tắc đổi schema ở mục 8 | Một schema chia cho nhiều app làm mọi thay đổi `fhirstore` ảnh hưởng cả năm |

## 7. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| S0 | P0-3 (sửa `$reindex` xuyên project) và P0-5 (chốt `fhirdownload`) | S | test chặn `$reindex` xuyên project (token một project không ghi lại chỉ mục project khác); `fhirdownload` có test và tài liệu cấu hình đủ | không |
| S1 | Chuẩn bị `fhir-server`: công tắc tắt phía nghe/giao (rest-hook và tự tải) ở API, mặc định như cũ; tùy chọn `fhirrouter.Options` để API không tự chạy job (cho S5); P0-4; thống nhất tên biến dùng chung | M | không bật công tắc thì hành vi và test cũ không đổi; từ chối khởi động nếu tắt phía nghe nội bộ mà broker là `memory` | S0 |
| S2 | **`apps/fhir-downloader`** theo khuôn mục 4 | M | e2e: `fhir-server` (tắt phía tải) và `fhir-downloader` ở tiến trình khác; attachment ngoài thành `Binary/<id>`; giết `fhir-downloader` giữa chừng rồi chạy lại vẫn xử lý phần còn trong PEL; API không có đường ra Internet trong cấu hình e2e | S1 |
| S3 | P0-1, rồi **`apps/fhir-notifier`** theo khuôn mục 4 | L | e2e: giết `fhir-notifier` giữa chừng, sự kiện chưa giao được giao lại sau khi chạy lại; webhook chậm không làm chậm API (đo p95); websocket vẫn nhận thông báo qua `ws-gateway` | S1, P0-1 |
| S4 | Đo tải #12c (export lớn song song với tải API: p95 và p99 của API, CPU, bộ nhớ) | M | số đo ghi vào tài liệu này; chốt D-SP-7 | S1 |
| S5 | P0-2, rồi **`apps/fhir-worker`** theo khuôn mục 4 | L | e2e: tạo export ở `fhir-server`, `fhir-worker` chạy job, API không thực thi job; giết `fhir-worker` giữa chừng thì job được tiếp quản; job-seal sai không chạy | S1, S4, P0-2 |
| S6 | Tài liệu: bản đồ service (`service-principles.md` mục 2 và 8), `fhir-server/docs/architecture.md` ("không còn làm" gì), runbook, compose, Makefile (`run-<tên>`), `docs/erd` nếu đổi dữ liệu; cập nhật `task-list.md` | S | | các bước trên |

## 8. Rủi ro

| Điều | Ghi chú |
|---|---|
| **Chi phí vận hành tăng** | Từ 4 service (`auth`, `fhir-server`, `ws-gateway`, `dicomweb`) lên 7: mỗi service thêm image, Dockerfile, e2e, CI, manifest, mục runbook (DoD 10 mục). Đây là hệ quả trực tiếp của việc chọn bậc 3; ước lượng cỡ L cho cả ba |
| **Một schema, năm app** | Mọi đổi schema `fhirstore` ảnh hưởng cả năm. Quy tắc đề xuất: migration chỉ **mở rộng rồi thu hẹp** (expand/contract); `fhir-server migrate up` trước, rồi `fhir-server`, rồi các service khác; mỗi service từ chối khởi động khi còn migration chưa chạy (đã có cho `dicomweb`) |
| Lệch phiên bản giữa các service | Cùng một module Go nên build cùng lúc, nhưng triển khai có thể lệch; hợp đồng dây (định dạng sự kiện, job-seal) phải chấp nhận cả phiên bản cũ trong giai đoạn chuyển (C-6) |
| Hai tiến trình xử lý cùng một sự kiện | Redis Streams consumer group giao mỗi message cho một consumer nhưng at-least-once: cả `fhir-downloader` và `fhir-notifier` phải idempotent (chưa kiểm cho `fhirdownload`; rest-hook "có thể trùng" theo tài liệu hiện có) |
| Giai đoạn chuyển giao | Khi vừa có `fhir-notifier` vừa còn phía nghe ở API, hai bên cùng nghe một consumer group sẽ chia sự kiện (đúng chức năng nhưng API vẫn gánh tải): phải tắt phía nghe ở API ngay khi bật service mới (công tắc S1) |
| Service nghe sự kiện sập | API vẫn nhận ghi và phát sự kiện lên Redis; sự kiện tích trong stream tới khi service chạy lại. Cần giới hạn độ dài stream và cảnh báo (chưa quy định) |
| Cửa sổ trễ Subscription | Replica khác thấy Subscription mới sau tối đa 30 giây (cache danh sách); `fhir-notifier` giữ nguyên hành vi |
| Không có chính sách mạng riêng | Nếu `fhir-downloader` không được đặt ở vùng mạng có egress khác API thì lợi ích an ninh chính của S2 chưa đạt (chỉ còn tách tải) |
| Không có số đo | Mọi ưu tiên ở mục 0 dựa trên suy luận từ mã; S4 là chỗ duy nhất có số đo |

## 9. Chưa làm hoặc chưa kiểm

- **Chưa đo tải:** export song song với API, webhook chậm, tải Binary lớn, `$expand` lớn (task #12c còn mở).
- **Chưa kiểm:** `fhirdownload` có idempotent khi nhận lại sự kiện; cách (a) giao rest-hook trong handler Stream có chịu được webhook chậm không (PEL, `ClaimInterval`); tùy chọn "API không tự chạy job" chưa tồn tại trong `fhirrouter`; công tắc tắt phía nghe ở `fhir-server` chưa tồn tại; cổng `:8009` đến `:8011` chỉ là đề xuất (chưa kiểm xung đột trong `Makefile` và compose); hành vi `$expunge` có phát sự kiện Subscription không.
- **Tài liệu liên quan chưa cập nhật:** `service-principles.md` mục 2 vẫn liệt kê `fhir-worker` là "chưa quyết"; `apps/fhir-server/docs/business` chưa nói tới `FHIR_AUTO_DOWNLOAD` (mã chưa commit); `task-list.md` chưa có các bước S0 đến S6.
