# Kế hoạch: ứng viên tách service từ `apps/fhir-server`

Trạng thái: **đề xuất, chưa có quyết định nào được chốt** (2026-11-24); mã chưa đổi. Nguyên tắc chung: [../service-principles.md](../service-principles.md) (điều kiện A/B/C/D, thang tách rẻ đến đắt). Bổ sung cho [microservices-shared-db.md](microservices-shared-db.md) mục 14 (P3a: điều tra `fhir-worker`, D-DB-3 đã chốt "chưa tách, chờ đo tải") và [modules/split-scim-ws-services.md](modules/split-scim-ws-services.md) (đã tách `ws-gateway`). Tài liệu này mở rộng câu hỏi của P3a từ "có tách `fhir-worker` không" thành "cụm nào trong `fhir-server` đáng tách, theo thứ tự nào, và cần sửa gì trước".

**Phạm vi bằng chứng:** đã đọc mã lắp ráp (`internal/bootstrap/container.go`), `pkg/fhirrouter`, `pkg/fhirsub`, `pkg/fhirbinary`, `pkg/messaging`, `pkg/fhirdownload`, và các kế hoạch hiện có. **Chưa có số đo tải thật** (mục 8): mọi nhận định "đáng tách" dưới đây là suy luận từ mã, không phải số đo. Câu nào ghi "(chưa kiểm)" là chưa chạy hoặc chưa đọc tới mã.

## 0. Kết luận

| Cụm trong `fhir-server` | Điều kiện đạt (A/B/C/D) | Mức gắn kết | Đề xuất |
|---|---|---|---|
| **Tự tải attachment** (`pkg/fhirdownload`, `FHIR_AUTO_DOWNLOAD`) | **C mạnh** (bề mặt SSRF, ra Internet), A | thấp | **Tách đầu tiên**, bằng vai trò `downloader` (cùng image) |
| **Giao rest-hook và khớp sự kiện websocket** (`fhirsub.Manager`) | C, A | trung bình | Tách thứ hai, bằng vai trò `notifier`, **cùng lúc với việc làm rest-hook bền** |
| **Job nền** (export, `$reindex`, batch async) = `fhir-worker` | C một phần (chưa quan sát), A chưa đo | trung bình đến thấp | **Chưa tách**; làm sau khi đo (#12c) và sau khi kết quả export không còn nằm trong CSDL |
| **Binary và `/storage`** | A, C | thấp ở `/storage`, **cao** ở đường FHIR | Chưa tách; nếu cần chỉ tách `/storage` |
| **Terminology** | A (có thể) | **cao** (nằm trong đường tìm kiếm) | **Không tách**; chỉ đẩy tác vụ nặng sang vai trò worker nếu cần |
| REST, kho, tìm kiếm, xác thực, GraphQL, `$validate`, `$everything` | không | rất cao | **Lõi, không tách** (không tách theo loại resource) |
| Websocket | A, C | đã tách | Đã xong: `ws-gateway` |

Điểm chung của mọi cụm: **không cụm nào đạt điều kiện B** (không có dữ liệu hay bí mật mà phần còn lại không được đọc): tất cả đọc và ghi cùng schema `fhir` bằng role `fhir_app`, và dùng chung `FHIR_JOB_SIGNING_KEY`. Vì vậy mọi phương án ở đây dừng ở **bậc 2 của thang (cùng binary, vai trò khác)**, không thêm thư mục `apps/<tên>` mới. Điều đó khớp P3a.

## 1. Hiện trạng (bằng chứng)

`fhir-server` là một tiến trình lắp ráp các thành phần sau trong `container.go` (`build`); thành phần có điều kiện được bật bằng cờ:

| Thành phần | Bật khi | Cách nối | Bằng chứng |
|---|---|---|---|
| Kho `fhirstore` và router REST | luôn | `fhirrouter.New(routerRepo, Options{...})` | `container.go` |
| Xác thực, tách project | luôn (JWT hoặc token tĩnh) | `fhirauth.Scope` bọc kho; job nền dựng lại `Principal` từ `AsyncJob.Meta.Project` | `container.go` (`scopedJobRepo`, `jobContext`) |
| Job nền | luôn | `NewOrchestrator` luôn trả `workflow.NewInline(run, FHIR_JOB_CONCURRENCY)`; adapter `asynq` có trong `pkg/workflow` nhưng không có chỗ nào ngoài test gọi tới | `container.go`; P3a |
| Broker sự kiện | `FHIR_SUBSCRIPTIONS` **hoặc** `FHIR_AUTO_DOWNLOAD` | `messaging.New` (`memory` hoặc `redis`: Redis Streams + consumer group `trivita`, giao at-least-once, ACK, chuyển poison message sang `{stream}:dead`) | `container.go`; `pkg/messaging/redis.go` |
| Phát sự kiện sau commit | broker có | `fhirsub.Wrap(store, broker)` bọc kho | `container.go` |
| Giao rest-hook | `FHIR_SUBSCRIPTIONS` | `fhirsub.NewManager(store, ...)`; Manager đọc Subscription từ **kho gốc**; hàng đợi giao là `chan` trong tiến trình, **sức chứa 1024, đầy thì bỏ (`Dropped`)**, 4 worker mặc định, thử lại `FHIR_WEBHOOK_MAX_ATTEMPTS` (5) bằng hẹn giờ trong bộ nhớ | `pkg/fhirsub/manager.go:106,116,366`; `config.md` |
| Khớp sự kiện websocket | `FHIR_WS_MODE` | `embedded`: Hub trong tiến trình; `issuer`: `Manager.AttachRemote` (khớp một lần cho cả cụm, đẩy lên kênh Redis; cần `BROKER_DRIVER=redis`) | `container.go` |
| Binary thô | `FHIR_BINARY_STORAGE` | `fhirbinary.Wrap(store, blobs, ...)` **bọc ghi Binary, nằm dưới sự kiện Subscription**; `RawBinary` bọc cả router FHIR; `/storage` là handler riêng đọc kho gốc | `container.go` |
| Tự tải attachment | `FHIR_AUTO_DOWNLOAD` (cần `FHIR_BINARY_STORAGE`) | `fhirdownload.New(...)`; `Register(broker)` nghe `fhirsub.EventType`, tải URL ngoài qua `pkg/safehttp`, ghi `Binary` rồi đổi `url` của attachment thành `Binary/<id>`; `Async` khi broker là `memory` (hàng đợi trong tiến trình, thử lại 3 lần lùi 1 giây, 2 giây) | `container.go`; `pkg/fhirdownload/download.go` (**đang làm dở, chưa commit khi viết tài liệu này**) |
| Terminology | luôn | `resolveTerminology` nằm **trong đường tìm kiếm** (`:below`, `:above`, `:in`); `$expand`, `$lookup`... dùng bảng dẫn xuất `fhir_coding*` cùng schema | `pkg/fhirrouter/chain.go:37`, `termsearch.go` |
| Kết quả bulk export | luôn | `Binary` base64 **trong CSDL** (không vào kho `file:`/`s3:` dù đã bật), mỗi tệp khoảng 8 MiB | `pkg/fhirrouter/bulk.go:178`; `container.go` (job nền giữ repo không bọc Binary) |

## 2. Đối chiếu tiêu chí tách

| Cụm | A. Hồ sơ tải khác | B. Dữ liệu/bí mật riêng | C. Miền lỗi, vòng đời deploy | D. Đội khác |
|---|---|---|---|---|
| Tự tải attachment | Có thể: tải tệp lớn, I/O ra ngoài (chưa đo) | **Không** (cùng schema; cần quyền ghi `Binary` và đọc kho) | **Có**: API không nên có đường ra Internet; sự cố của máy chủ ngoài không được ảnh hưởng API | không |
| Giao rest-hook | Có thể: tải theo số sự kiện × số Subscription, chờ I/O ngoài (chưa đo) | không | **Có một phần**: webhook chậm chiếm worker và bộ nhớ của API; restart API mất hàng đợi | không |
| Job nền | chưa đo | không | **Một phần**: export lớn có thể làm API chậm (chưa quan sát) | không |
| Binary và `/storage` | Có: tải và tải xuống lớn, kéo dài | không | Có một phần: đã giảm nhẹ bằng trần body riêng (`ownsBodyLimit`) | không |
| Terminology | Có thể (`$expand` tới 100.000 mã, `$import` ghi hàng loạt) | không | không | không |

## 3. Từng ứng viên

### 3.1 `downloader`: tự tải attachment (ưu tiên số 1)

- **Vì sao đáng tách:** tải URL do người dùng cung cấp là bề mặt SSRF và là chi phí; mặc định đang tắt vì lý do đó. Tách thành vai trò riêng cho phép **API chạy không có quyền ra Internet** (chính sách mạng egress chỉ mở cho vai trò này). Gắn kết thấp: chỉ nghe sự kiện từ broker, đọc resource, ghi `Binary` và cập nhật resource bằng đúng project của resource.
- **Cần:** broker `redis` (với `memory` thì `Async` chạy hàng đợi trong tiến trình ghi, không tách được); quyền DML trên schema `fhir`; truy cập kho Binary; **không cần** `FHIR_JOB_SIGNING_KEY`, khóa ký websocket hay JWKS.
- **Độ bền:** broker Redis đã có consumer group, at-least-once, thử lại và dead-letter; vai trò này **thừa hưởng độ bền đó** (khác rest-hook). Cần kiểm idempotency khi nhận lại cùng sự kiện (chưa kiểm trong `download.go`).
- **Rủi ro:** tiến trình đang sửa dở (chưa commit); cần chốt đầy đủ cấu hình trước khi tách (`ALLOWED_PREFIXES`, `IGNORED_PREFIXES` chưa có mô tả trong mã theo `config.md`).

### 3.2 `notifier`: giao rest-hook và khớp sự kiện websocket (ưu tiên số 2)

- **Vì sao đáng tách:** webhook chậm hoặc chết chiếm worker, hàng đợi 1024 trong bộ nhớ của tiến trình API; khớp `criteria` mỗi sự kiện tốn CPU tỉ lệ với số Subscription. `fhirsub.Wrap` đã phát sự kiện sau commit qua broker, và Manager đọc Subscription từ kho gốc, nên về mã **bên nghe có thể nằm ở tiến trình khác** khi dùng Redis.
- **Điểm yếu không được tách sửa hộ:** hàng đợi giao nằm trong bộ nhớ, restart là mất thông báo đang chờ và đang hẹn thử lại; hàng đợi đầy thì bỏ. P3a đã kết luận điều này. Tách sang `notifier` mà **không làm bền** thì chỉ chuyển nơi mất dữ liệu. Phải làm cùng lúc (mục 4, P0-1).
- **Cách làm bền (đề xuất, chưa thử):** (a) giao **đồng bộ trong handler của Redis Stream** và chỉ ACK khi giao xong hoặc bỏ cuộc theo `MaxAttempts` (tận dụng PEL, `ClaimInterval`, dead-letter có sẵn); (b) bảng outbox trong schema `fhir`; (c) `asynq`. (a) rẻ nhất vì không thêm hạ tầng, đổi lại một webhook chậm giữ một message PEL lâu.
- **Phần của `ws`:** `AttachRemote` (khớp một lần cho cả cụm, đẩy lên kênh) đi cùng `notifier`. Cấp token bind (`$get-ws-binding-token`) **vẫn ở API** vì cần kiểm quyền người gọi theo kho đã tách project.
- **Cần:** Redis bắt buộc (`BROKER_DRIVER=redis`; broker `memory` không dùng được khi tách); `FHIR_WS_SIGNING_KEY` nếu còn websocket; quyền đọc Subscription (kho gốc).

### 3.3 `worker`: job nền (chưa tách; mở lại sau khi đo)

- **Trạng thái:** D-DB-3 đã chốt "chưa tách". Kết luận P3a vẫn đúng: job `AsyncJob` đã bền nhờ lease trong DB (nhận bằng If-Match, gia hạn, tiếp quản sau `JobRecoveryGrace` và lease hết hạn), nên **thiếu động lực kỹ thuật cấp bách**; việc cần quyết là có đo cho thấy API bị ảnh hưởng không (task #12c).
- **Hai việc phải xong trước khi `worker` có ý nghĩa:**
  1. Kết quả export đang là `Binary` base64 trong CSDL (`bulk.go:178`): chuyển job sang tiến trình khác **không giảm tải DB**, và CSDL phình theo `FHIR_EXPORT_RETENTION`. Cần ghi kết quả ra kho đối tượng.
  2. `$reindex` cả loại dùng kho thô (`operation.go:298`, `reindexType` dùng `r.repo`, trong khi `reindexOne` dùng `repoOf(ctx)`): admin của một project khởi động được reindex mọi project. Sửa trước, nếu không vai trò `worker` kế thừa lỗi.
- **Cách (b) của P3a** (role `api` để job ở `accepted`, role `worker` chỉ chạy vòng `RecoverJobs`) **vẫn là suy luận, chưa thử**; độ trễ nhận job ≥ `JobRecoveryGrace` cộng chu kỳ bảo trì.
- **Cần:** `FHIR_JOB_SIGNING_KEY` dùng chung (job-seal), cùng role DB `fhir_app`.

### 3.4 Binary và `/storage`

- `/storage` (handler riêng, đọc kho gốc, quyền do chữ ký) tách được rẻ. Phần **Binary trong đường FHIR** thì gắn chặt: `fhirbinary.Wrap` nằm dưới sự kiện Subscription (để sự kiện không mang `data`), `RawBinary` bọc cả router, và bộ rewriter đổi tham chiếu thành URL ký ở mọi phản hồi. Tách phần đó cần một API nội bộ chưa có.
- Tiền lệ: `dicomweb` dùng chung schema `fhir` và kho Binary (thư viện `pkg/binarystore`), nghĩa là "dùng chung qua thư viện + DB" là mô hình đã chạy. [microservices-shared-db.md](microservices-shared-db.md) mục 3 ghi các dịch vụ nền (`mcp`, `email`, `storage`, `keyvalue`) chỉ tách khi có hơn một nơi gọi; nay có `fhir-server`, `dicomweb`, và `auth` (đọc `Binary/<id>` qua API): chưa thành lý do tách một service.
- **Đề xuất:** chưa tách; đo thời gian tải lớn làm nghẽn API trước.

### 3.5 Terminology: không tách

`resolveTerminology` chạy **trước mỗi truy vấn tìm kiếm có `:below`, `:above`, `:in`** (`chain.go:37`) và đọc bảng `fhir_coding*` cùng schema. Tách tức là thêm một bước mạng vào đường tìm kiếm, mà DB vẫn dùng chung. Nếu `$expand` lớn hay `$import` làm nghẽn thật, đẩy **riêng tác vụ nặng** sang vai trò `worker`, **không** đụng đường tra cứu.

### 3.6 Không phải ứng viên

REST, kho, tìm kiếm, xác thực (`fhirauth` là thư viện, không phải service), GraphQL (lớp tính toán trên `Repository`), `$validate`, `$everything`. Nguyên tắc dự án cấm tách theo loại resource: search, reference, transaction xuyên qua mọi loại.

## 4. Điều kiện tiên quyết

Các việc dưới đây độc lập với chuyện tách, nhưng **làm việc tách mất ý nghĩa nếu chưa xong** (mọi điểm đã đối chiếu trong mã, trừ điểm ghi khác):

| Mã | Việc | Vì sao | Cỡ |
|---|---|---|---|
| P0-1 | Rest-hook bền (một trong a, b, c ở 3.2) | Tách `notifier` không sửa mất dữ liệu khi chết hoặc khi hàng đợi đầy | M |
| P0-2 | Kết quả bulk export ghi ra kho đối tượng | Job sang tiến trình khác không giảm tải DB; CSDL phình theo retention | M |
| P0-3 | Sửa `$reindex` cả loại dùng kho đã tách project, kiểm AccessPolicy ở đường tạo job | Vai trò `worker` kế thừa lỗi xuyên project | S |
| P0-4 | Cấm broker `memory` khi chạy vai trò khác `all` | Vai trò tách chỉ hoạt động qua Redis | S |
| P0-5 | Chốt và commit `pkg/fhirdownload`, mô tả đủ biến cấu hình (`IGNORED_PREFIXES`...) | `downloader` là việc nên làm đầu tiên nhưng mã còn dở | S đến M |

## 5. Quyết định cần chốt

| Mã | Câu hỏi | Đề xuất | Hệ quả nếu chọn khác |
|---|---|---|---|
| D-SP-1 | Tách bằng gì? | **Vai trò `FHIR_ROLE` trong cùng image** (bậc 2), mặc định `all` giữ hành vi cũ; **không** thêm thư mục `apps/` mới | Service riêng chỉ khi xuất hiện điều kiện B hoặc D thật; đắt hơn (image, compose, CI, e2e) và chưa có lý do |
| D-SP-2 | Thứ tự | `downloader`, rồi `notifier` (kèm P0-1), rồi `worker` (sau #12c và P0-2, P0-3) | Làm `worker` trước tốn công mà chưa có số đo; làm `notifier` không có P0-1 chuyển nơi mất dữ liệu |
| D-SP-3 | Redis | **Bắt buộc** cho mọi cấu hình có vai trò tách (P0-4) | Broker `memory` chỉ chạy một tiến trình |
| D-SP-4 | Cách làm rest-hook bền | (a) giao đồng bộ trong handler Redis Stream, ACK sau khi giao | (b) outbox: thêm bảng, thêm bảo trì; (c) `asynq`: thêm phụ thuộc vận hành, đã có adapter chưa nối |
| D-SP-5 | `/storage` và Binary | Chưa tách; xem lại khi đo cho thấy tải lớn làm nghẽn API | Tách sớm cần API nội bộ chưa có |
| D-SP-6 | Terminology | Không tách | Thêm một bước mạng vào đường tìm kiếm |
| D-SP-7 | Có đo trước khi tách `worker` không (task #12c)? | Có (đã chốt ở D-DB-3) | |

## 6. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| S0 | P0-3 (sửa `$reindex` xuyên project) và P0-5 (chốt `fhirdownload`) | S | test chống `$reindex` xuyên project (token một project, kiểm chỉ mục project khác không bị ghi lại); `fhirdownload` có test và tài liệu cấu hình đủ | không |
| S1 | Công tắc `FHIR_ROLE=api\|downloader\|notifier\|worker\|all` trong `container.go` (mặc định `all`); vai trò khác `all` không mở HTTP nghiệp vụ, chỉ `/healthz` và `/readyz` kiểm phụ thuộc của vai trò; P0-4 | M | `all` không đổi hành vi (test hiện có xanh); từ chối khởi động nếu vai trò tách mà `BROKER_DRIVER=memory` | S0 |
| S2 | Vai trò `downloader` | S đến M | e2e: API không bật tải, vai trò `downloader` ở tiến trình khác đổi `url` attachment sang `Binary/<id>`; mất tiến trình `downloader` rồi bật lại vẫn xử lý sự kiện còn trong PEL | S1 |
| S3 | P0-1 rồi vai trò `notifier` (rest-hook và `AttachRemote`) | M đến L | e2e: giết `notifier` giữa chừng, sự kiện chưa giao được giao lại sau khi chạy lại; webhook chậm không làm chậm API (đo p95) | S1, P0-1 |
| S4 | Đo tải #12c; nếu API bị ảnh hưởng: P0-2 rồi vai trò `worker` | M đến L | số đo ghi vào tài liệu; quyết định D-SP-7 | S1 |
| S5 | Tài liệu: cập nhật `service-principles.md` mục 2 (bản đồ), `fhir-server/docs/architecture.md` (vì sao tách), runbook (thứ tự triển khai, biến, quyền DB cho từng vai trò), `docs/erd` nếu đổi dữ liệu | S | | các bước trên |

Triển khai mỗi vai trò dùng **cùng image và cùng khóa dùng chung** (`FHIR_JOB_SIGNING_KEY`, `FHIR_WS_SIGNING_KEY` cho vai trò cần), cùng role `fhir_app`. Thứ tự nâng cấp: migration (`fhir-server migrate up`) trước, rồi `all` hoặc `api`, rồi các vai trò nghe sự kiện (chúng chịu được schema mới vì không đổi hợp đồng dây).

## 7. Rủi ro

| Điều | Ghi chú |
|---|---|
| Hai tiến trình xử lý cùng một sự kiện | Redis Streams consumer group giao mỗi message cho một consumer, nhưng at-least-once: `downloader` và `notifier` phải idempotent (chưa kiểm cho `fhirdownload`; rest-hook "có thể trùng" theo tài liệu hiện có) |
| Cửa sổ trễ Subscription | Replica khác thấy Subscription mới sau tối đa 30 giây (cache danh sách); `notifier` tách riêng giữ nguyên hành vi đó |
| Vai trò nghe sự kiện sập | API vẫn nhận ghi và phát sự kiện lên Redis; sự kiện tích trong stream tới khi vai trò chạy lại. Cần giới hạn độ dài stream và cảnh báo (chưa quy định) |
| Mất quyền ra Internet của API | `downloader` phải có chính sách mạng riêng; nếu chưa có, lợi ích an ninh của S2 chưa đạt (chỉ là tách tải) |
| Lệch cấu hình giữa các vai trò | Cùng image nhưng biến khác nhau: `config.md` sinh từ mã cần ghi rõ biến nào có ý nghĩa với vai trò nào |
| Không có số đo | Mọi ưu tiên ở mục 0 dựa trên suy luận từ mã; S4 là chỗ duy nhất có số đo |

## 8. Chưa làm hoặc chưa kiểm

- **Chưa đo tải:** export song song với API, webhook chậm, tải Binary lớn, `$expand` lớn (task #12c còn mở).
- **Chưa kiểm:** `fhirdownload` có idempotent khi nhận lại sự kiện; cách (b) của P3a; cách (a) giao rest-hook trong handler Stream có chịu được webhook chậm không (PEL, `ClaimInterval`); `FHIR_ROLE` chưa tồn tại trong mã; hành vi `$expunge` có phát sự kiện Subscription không (ghi ở bộ tài liệu nghiệp vụ là chưa kiểm).
- **Tài liệu liên quan chưa cập nhật:** `service-principles.md` mục 2 vẫn liệt kê `fhir-worker` là "chưa quyết"; `apps/fhir-server/docs/business` chưa nói tới `FHIR_AUTO_DOWNLOAD` (mã chưa commit).
