# 4. Kho khác ngoài SQL, bộ nhớ, khóa ký và bí mật

Mọi dữ kiện đọc từ mã (`apps/fhir-server/internal/bootstrap/container.go`, `config/config.go`, `pkg/messaging`, `pkg/fhirsub`, `pkg/ratelimit`, `pkg/fhirbinary`, `pkg/binarystore`); **chưa kiểm trên Redis và PostgreSQL thật**.

## 4.1 Redis: tổng quan và tiền tố

Redis dùng cho ba việc tách bạch, mỗi việc bật bằng cờ riêng:

| Việc | Bật bởi | Khóa |
|---|---|---|
| Broker sự kiện (stream, nhóm consumer, dead-letter, kênh động, sổ hiện diện) | `FHIR_SUBSCRIPTIONS=true` **hoặc** `FHIR_AUTO_DOWNLOAD=true`, cùng `BROKER_DRIVER=redis` (`container.go`: `if s.Subscriptions \|\| s.AutoDownload`) | `trivita:events:*` |
| Đếm thất bại để tự tắt Subscription | `FHIR_SUBSCRIPTIONS`, `FHIR_SUBSCRIPTION_DELIVERY=true`, broker Redis (`NewRedisFailureTracker(rb.Client(), "", nil)` dùng chung client với broker) | `trivita:sub:failures:*` |
| Hạn mức yêu cầu dùng chung nhiều replica | `FHIR_RATE_LIMIT_SHARED=true` (client riêng từ `ratelimit.NewRedisClient(REDIS_URL)`) | `fhir:rl:*` |

Tiền tố broker mặc định `trivita:events` (`messaging.RedisOptions.Prefix`) và nhóm mặc định `trivita` (`Group`); `fhir-server` **không đặt lại** hai giá trị này (`messaging.Config{Driver, RedisURL}` + tuỳ chọn `Concurrency`, `ClaimIdle`), nên **nhóm của fhir-server là `trivita`**; `fhir-downloader` và `fhir-notifier` dùng nhóm riêng để cùng nhận mọi sự kiện.

## 4.2 Redis: các khóa

| Khóa | Kiểu | Giá trị | TTL | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|---|
| `trivita:events:fhir.resource.changed` (`prefix:` + `fhirsub.EventType`) | stream | mỗi entry một trường `data` = JSON `Envelope` (`id`, `type`, `occurred_at`, `payload` là `ChangeEvent`: `interaction` (create/update/delete), `resource_type`, `id`, `version_id`, **`resource` = JSON đầy đủ của resource**) | **không có**: `XAdd` không `MAXLEN`, không `XTRIM` (`redis.go:130`) | `fhir-server` qua `fhirsub.Wrap` **sau commit** (sự kiện bị đệm trong transaction rồi phát khi commit); `fhir-worker` (batch async); `fhir-downloader` (đổi `url`) | nhóm `trivita` (`fhir-server`: Subscription, tự tải nếu chạy trong tiến trình), nhóm `fhir-downloader`, nhóm `fhir-notifier`; `XREAD` trực tiếp (không nhóm) cho broadcast websocket nhúng | sự kiện chưa xử lý mất; **không có đối soát lại** |
| nhóm `trivita` trên stream trên | consumer group | vị trí đọc và PEL (message đã giao chưa ACK, số lần giao, thời điểm giao cuối) | không | `XGroupCreateMkStream(..., "0")` khi `Broker.Start` (bỏ qua `BUSYGROUP`) | `XReadGroup` (Count 10, Block 1 giây), `XPENDING`, `XCLAIM` | tạo lại từ `"0"` và **đọc lại toàn bộ stream còn lại** |
| consumer `trivita-<8 ký tự đầu của id>` | consumer trong nhóm | tên mới mỗi lần tiến trình khởi động (`opts.Group + "-" + NewID()[:8]`) | không | broker | | consumer cũ (đã chết) nằm lại trong danh sách nhóm; message của nó được nhận lại sau `ClaimIdle` |
| `trivita:events:fhir.resource.changed:dead` | stream | các trường của message gốc cộng `original_id`, `deliveries` | **không có** | broker khi `RetryCount >= MaxDeliveries` (mặc định 5) | **không ai** (chưa có công cụ liệt kê, phát lại, xóa) | mất dấu việc đã bỏ cuộc |
| `trivita:sub:failures:<subscriptionId>` | sorted set | điểm = mili giây thất bại, thành viên `<ms>:<id ngẫu nhiên>`; mỗi lần giao bỏ cuộc thêm một phần tử (`ZADD`), tỉa theo cửa sổ lớn nhất (`ZREMRANGEBYSCORE`), đếm từng cửa sổ (`ZCOUNT`) | `EXPIRE` = cửa sổ lớn nhất của `FHIR_SUBSCRIPTION_AUTO_DISABLE` mỗi lần ghi | `fhir-server` (khi giao trong tiến trình) hoặc `fhir-notifier` | cùng bên ghi | mất khóa: bộ đếm về 0, Subscription hỏng lâu hơn mới bị tắt. `Clear` (`UNLINK`) sau một lần giao thành công hoặc khi đã tắt. Không có `FHIR_SUBSCRIPTION_AUTO_DISABLE` thì **không ghi** khóa này; broker `memory` thì đếm trong bộ nhớ từng tiến trình |
| `trivita:events:ch:fhirws.sub.<subscriptionId>` | Pub/Sub (không bền) | `WSMessage{Sub, Event}` JSON | không áp dụng | tiến trình khớp sự kiện ở `FHIR_WS_MODE=issuer` (`fhir-server` khi `FHIR_SUBSCRIPTION_DELIVERY=true`; `fhir-notifier` khi `false`), **chỉ khi sổ hiện diện báo có gateway đang giữ socket** | `ws-gateway` (đăng ký kênh khi có kết nối bind) | sự kiện phát lúc đứt hoặc không có người nghe mất (at-most-once) |
| `trivita:events:presence:fhirws.bound.<subscriptionId>` | sorted set | thành viên = `InstanceID` của gateway, điểm = thời điểm hết hạn (đồng hồ Redis `TIME`) | `PEXPIRE` theo `PresenceTTL` mặc định 30 giây, gia hạn mỗi `PresenceTTL/3` | **`ws-gateway`** (`Feed`: `Mark`/`Unmark`) | tiến trình khớp sự kiện (`Present`: `ZCOUNT` chưa hết hạn) | mất: coi như không có gateway, sự kiện đếm vào `Unheard` và **không phát** |
| `fhir:rl:api:<ip>` và `fhir:rl:storage:<ip>` | hash (`tokens`, `ts`) | token bucket; `<ip>` là `c.RealIP()` (phụ thuộc `FHIR_TRUSTED_PROXIES`) | `PEXPIRE` = `burst/rps` + 5 giây | `fhir-server` (script Lua nguyên tử dùng giờ Redis) | cùng | mất khóa: bucket đầy lại (nới hạn mức); chỉ khi `FHIR_RATE_LIMIT_SHARED=true` |

Ghi chú:
- `<ip>` là **dữ liệu cá nhân (S2)** nhưng sống ngắn (vài giây tới vài phút theo `burst/rps`).
- Token bind websocket (JWT-HMAC, `jti` ngẫu nhiên, hạn mặc định 5 phút) **không lưu ở Redis hay DB**: fhir-server không có kho `jti` (không phát hiện dùng lại token trong hạn từ phía fhir-server; kiểm ở `ws-gateway`, ngoài phạm vi tài liệu này, chưa kiểm).
- Claim của job nền **không** ở Redis: lease nằm ở `AsyncJob.transactionTime` trong DB, nhận job bằng `If-Match` (`pkg/fhirrouter/jobs.go`). Orchestrator job là `workflow.NewInline` (trong bộ nhớ, `FHIR_JOB_CONCURRENCY`), **không dùng `asynq`** ở service này.
- **Mọi ghi resource đều phát sự kiện**, kể cả `AsyncJob` (việc gia hạn lease và cập nhật tiến độ cũng là ghi, `jobRepo` cũng được bọc `fhirsub.Wrap` khi có broker): stream mang tải ghi của **toàn hệ thống** (suy từ `container.go`, chưa đo).

## 4.3 Kho Binary (`FHIR_BINARY_STORAGE`)

Rỗng: tắt hẳn; `Binary` giữ `data` base64 trong `fhir_resource.content` (trần `FHIR_BASE64_BINARY_MAX_BYTES` = 1 MiB), không có `/storage`, không `$presigned-url`. Bật: nội dung thô ở kho, `Binary` trong DB chỉ giữ metadata.

| Khóa / đường dẫn | Giá trị | Vòng đời | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| `binary/<id>/<versionId>`: `file:<thư mục>/binary/<id>/<versionId>` hoặc `s3:<bucket>/<prefix>binary/<id>/<versionId>`; `id`, `versionId` khớp `^[A-Za-z0-9\-.]{1,64}$`, không `.` và `..` (`binarystore.Key`) | nội dung thô, **mỗi phiên bản Binary một khóa** | ghi **trong** transaction trước commit (`fhirbinary.Wrap`); `Update` không kèm `data` thì **chép** khóa phiên bản trước sang phiên bản mới (`Copy`); bù trừ xóa các khóa đã ghi nếu transaction hủy; **`Expunge`** xóa khóa của mọi phiên bản sau commit; **xóa mềm (`DeleteResource`) không đụng kho** | `fhir-server` (upload qua `POST/PUT /Binary`, `/storage` ký, kết quả job); `fhir-downloader`; `fhir-worker` | `fhir-server` (`GET Binary/{id}`, `/storage`, `PackageRelease/$install` đọc tối đa 64 MiB) | `GET Binary` lỗi (`Failed to store binary content` khi ghi, lỗi đọc khi thiếu) |
| `file:` chỉ: `_tmp/<hex>` | tệp tạm khi ghi, `rename` nguyên tử sang khóa đích | xóa khi lỗi; tệp bỏ lại khi tiến trình chết do `CleanTemp` dọn (cũ hơn `-grace`) | cùng bên ghi | không | rác nhỏ |

**Cấu hình và quyền:**
- `file:` cần thư mục ghi được (tạo `0750`, tệp `0640`), phải **dùng chung** với mọi service ghi/đọc Binary (volume); S3: `FHIR_S3_REGION` bắt buộc, `FHIR_S3_ENDPOINT` https ở staging/production, `FHIR_S3_SSE` `""`/`AES256`/`aws:kms`, khóa chỉ qua biến môi trường.
- **URL ký `/storage` và `$presigned-url`**: URL dạng `<FHIR_STORAGE_BASE_URL>/<id>/<versionId>?Expires=<unix>[&Project=<id>]&Signature=<base64 RSA-PKCS1v15-SHA256>`; ký chuỗi `"<METHOD> <base>?<query>"`; hạn mặc định 1 giờ (`FHIR_STORAGE_URL_TTL`, tối đa 24 giờ). Chữ ký gắn với gốc cấu hình, không lấy từ header `Host`. Chữ ký **không lưu ở đâu** (tự chứng minh); mất khóa ký riêng thì mọi URL đã cấp hết hiệu lực.
- Kích thước: tải lên tới `FHIR_STORAGE_MAX_UPLOAD_BYTES` (256 MiB, sau giải nén); kết quả job không có trần base64.

**Quét mồ côi `fhir-server sweep-binary` (`fhirbinary.Sweep`):**
- Duyệt mọi khóa của kho (cần `binarystore.Lister`), bỏ khóa mới hơn `-grace` (mặc định 24 giờ, tối thiểu 1 giờ vì `Wrap` ghi kho trước commit), tra `ReadVersion(Binary, id, versionId)` trên repo **không tách project và không bọc Binary**.
- **Khóa được coi là "còn sống" khi có bất kỳ phiên bản Binary nào, kể cả bản xóa mềm**: mã coi `err == nil` **hoặc HTTP 410 (Gone)** là `Live`; chỉ HTTP 404 dứt khoát mới là mồ côi (`sweep.go`). Hệ quả: `Binary` bị xóa mềm (`DELETE Binary/<id>`) **giữ nguyên tệp ở kho vô thời hạn**; sweep không bao giờ dọn; chỉ `$expunge` mới xóa tệp. Tệp của `Binary` đã xóa mềm vẫn nằm đúng chỗ.
- Mồ côi thật chỉ là khóa mà dòng DB **không bao giờ tồn tại** (tiến trình chết giữa ghi kho và commit, hoặc bù trừ không xóa được).
- An toàn: lỗi tra cứu khác 404 thì giữ khóa và đếm `Errors`; trần `-max-delete` (mặc định 1000) mỗi lượt; **chốt chặn** `ErrSweepAborted` khi từ 20 khóa trở lên và quá nửa là mồ côi (nghi sai CSDL; vượt bằng `-force`); `-dry-run` chỉ liệt kê.
- Không chạy theo lịch trong mã: vận hành tự gọi (cron/job).
- **Nhạy cảm:** nội dung thô là **S3** (có thể PHI); ai đọc được kho trực tiếp đọc được mọi tệp, bỏ qua kiểm quyền `meta.project`. `Binary.contentType` do người gửi khai.

## 4.4 Khóa ký (không phải dữ liệu, ghi ai giữ)

| Khóa | Cấu hình | Dạng | Ai giữ | Dùng để | Bắt buộc | Mất hoặc lộ |
|---|---|---|---|---|---|---|
| Token bind websocket | `FHIR_WS_SIGNING_KEY` (+ `FHIR_WS_SIGNING_KEY_PREVIOUS` tối đa 3, chỉ để xác minh) | chuỗi bí mật >= 32 ký tự, HMAC | `fhir-server` (cấp), `fhir-notifier` (cấp khi tách), **`ws-gateway` (kiểm, phải cùng khóa)** | ký token bind (hạn 5 phút) | staging/production khi bật websocket; khi `BROKER_DRIVER=redis` ở mọi môi trường. Không đặt ở dev embedded thì **sinh khóa ngẫu nhiên mỗi tiến trình** (chỉ một instance) | lộ: ai có khóa tự cấp được token bind cho Subscription bất kỳ (kẻ giả danh); mất: token hiện hành hết giá trị |
| Quyền job nền (job-seal) | `FHIR_JOB_SIGNING_KEY` | chuỗi >= 32 ký tự, HMAC-SHA256 | `fhir-server` (ký khi tạo `AsyncJob`), **`fhir-worker` (kiểm khi chạy)** | ràng buộc id job, project, request với ảnh chụp quyền (`grants`, `policy`) trong extension `job-seal` của `AsyncJob`; chặn sửa tay `AsyncJob` để nâng quyền | staging/production khi dùng JWKS; dev HMAC suy `sha256("fhir-job-seal\|"+FHIR_JWT_HMAC_SECRET)` | lộ: giả được quyền của job; mất (đổi khóa): job đang chờ không mở được seal (`Open` lỗi, chưa kiểm hậu quả chi tiết) |
| Ký URL `/storage` | `FHIR_STORAGE_SIGNING_KEY_PATH` (PEM RSA PKCS#8 không mật khẩu, quyền `0600`; bắt buộc ở production, từ chối nếu quyền rộng hơn) + `FHIR_STORAGE_SIGNING_PREVIOUS_KEYS` (khóa **công khai** cũ, tối đa 3) | tệp PEM | `fhir-server` (và mọi service chạy handler `/storage`) | ký và kiểm URL tải lên/xuống | khi bật kho Binary ở staging/production; dev không đặt thì **sinh khóa tạm** (URL mất hiệu lực khi khởi động lại, không chạy nhiều replica) | lộ: ai cũng tự ký được URL đọc/ghi Binary |
| Xác thực người gọi | `FHIR_JWKS_URL` (khóa **công khai** của `auth`); hoặc `FHIR_READ/WRITE/ADMIN_TOKENS` (secret >= 24 ký tự); dev: `FHIR_JWT_HMAC_SECRET` | | `fhir-server` chỉ **kiểm**, không giữ khóa riêng của `auth` | | | token tĩnh lộ: quyền tương ứng |

Không có khóa nào trong số này lưu trong DB hay Redis.

## 4.5 Bộ nhớ của tiến trình

| Thứ | Nội dung | Khi nào tồn tại | Mất khi tiến trình chết? | Hệ quả |
|---|---|---|---|---|
| Cache danh sách Subscription đang `active` (`Manager.cache`) | `[]*entry` của `Subscription?status=active`, **TTL 30 giây** (`CacheTTL`) | khi `FHIR_SUBSCRIPTIONS=true` **và** `FHIR_SUBSCRIPTION_DELIVERY=true` | có | thay đổi Subscription có hiệu lực ngay ở replica đã ghi (`Invalidate`) nhưng **trễ tối đa 30 giây ở replica khác**; nạp lỗi thì dùng bản cũ |
| Hàng đợi giao rest-hook | sức chứa 1024 việc, `FHIR_WEBHOOK_WORKERS` worker | cùng điều kiện; **không dùng** khi `FHIR_WEBHOOK_DURABLE=true` | có | đầy thì bỏ (`Dropped`), restart thì mất lần đang thử lại; `DURABLE=true` giao đồng bộ trong handler stream, ACK sau khi giao nên không mất (giao ít nhất một lần) |
| Bộ đếm thất bại bộ nhớ (`MemoryFailureTracker`) | `map[subscriptionId][]time` | khi broker `memory` | có | nhiều replica thì mỗi nơi đếm riêng, khó đạt ngưỡng tự tắt |
| Hub websocket nhúng | kết nối, bind, token | `FHIR_WS_MODE=embedded` (hoặc bí danh `FHIR_WEBSOCKET=true`) | có | không dùng được khi `FHIR_SUBSCRIPTION_DELIVERY=false`; mỗi instance giữ kết nối của mình, tối đa `FHIR_WS_MAX_CONNECTIONS` (1000) |
| Broker `memory` | hàng đợi sự kiện trong tiến trình | `BROKER_DRIVER=memory` (mặc định) | có | không đến được tiến trình khác; không thể tách service; xử lý đồng bộ trong yêu cầu ghi nên webhook chậm chặn client |
| Bộ đếm hạn mức bộ nhớ | `rate.Limiter` mỗi IP | mặc định; và là **dự phòng** khi Redis lỗi | có | mỗi replica một hạn mức (tổng = hạn mức x số replica) |
| Job đang chạy (`workflow.NewInline`) | tối đa `FHIR_JOB_CONCURRENCY` (2) job song song | `FHIR_JOB_EXECUTION=true` | có | job dở nhả lease khi tắt êm (`SHUTDOWN_TIMEOUT`) hoặc được replica khác tiếp quản sau `FHIR_JOB_LEASE` (5 phút) |
| Cờ `draining` | `/readyz` 503 sau SIGTERM, chờ `SHUTDOWN_DRAIN_DELAY` rồi `SHUTDOWN_TIMEOUT` | | có | |

## 4.6 SQLite (dev)

`DATABASE_URL=sqlite://./fhir.db` (mặc định): một tệp (`apps/fhir-server/fhir.db` trong cây làm việc, `.gitignore` bởi `*.db`), không schema, không role; chứa **toàn bộ** dữ liệu của dev kể cả PHI nếu nạp dữ liệu thật; **cấm ở staging/production**. Các service tách chỉ dùng chung tệp này khi chạy cùng máy; Redis vẫn cần khi tách. `sqlite://:memory:` luôn tự migrate và mất khi tắt.

## 4.7 Cờ tách service: ai ghi kho nào

Mặc định (không đặt cờ) mọi việc chạy trong một tiến trình `fhir-server`. Ba công tắc chuyển việc sang service riêng; **API vẫn phát sự kiện lên Redis** (nếu bật Subscription hoặc tự tải) và vẫn ghi DB như cũ.

| Cờ | `true` (mặc định): `fhir-server` làm | `false`: service nhận việc | Kho bị ảnh hưởng | Điều kiện |
|---|---|---|---|---|
| `FHIR_SUBSCRIPTION_DELIVERY` | giao rest-hook, khớp sự kiện websocket; dựng `Manager` (cache 30 giây, hàng đợi, bộ đếm thất bại); ghi `trivita:sub:failures:*`, đọc sổ hiện diện, phát `…:ch:fhirws.sub.*` | **`fhir-notifier`**: nhóm `fhir-notifier`, ghi khóa thất bại, phát kênh, đọc sổ hiện diện, tự tắt Subscription (ghi `fhir_resource`) | Redis (khóa thất bại, kênh, nhóm consumer), `fhir_resource` (Subscription), bộ nhớ (cache, hàng đợi) | `FHIR_SUBSCRIPTIONS=true`, `BROKER_DRIVER=redis`, không `FHIR_WS_MODE=embedded`; `FHIR_WEBHOOK_DURABLE` vô nghĩa (notifier luôn giao bền). fhir-server vẫn **cấp token bind** nếu `FHIR_WS_MODE=issuer` (cần `FHIR_WS_SIGNING_KEY`) |
| `FHIR_AUTO_DOWNLOAD_WORKER` | tải attachment URL ngoài trong tiến trình (cần `FHIR_AUTO_DOWNLOAD=true`, `FHIR_BINARY_STORAGE`); ghi `Binary`, kho, cập nhật resource nguồn | **`fhir-downloader`**: nhóm `fhir-downloader`; ghi `Binary`, khóa kho, cập nhật nguồn; không gọi Internet từ API | `fhir_resource`/`fhir_history`, kho Binary, stream | `BROKER_DRIVER=redis`; kho Binary **phải trùng** giữa hai bên |
| `FHIR_JOB_EXECUTION` | chạy job nền (bulk export, `$reindex` cả loại, batch async, bảo trì job mồ côi, xóa kết quả export hết hạn) | **`fhir-worker`**: nhận `AsyncJob` bằng `If-Match`, ghi kết quả (`Binary`, kho), xóa export hết hạn | `fhir_resource` (`AsyncJob`, `Binary`), kho Binary, stream (batch async) | `FHIR_JOB_SIGNING_KEY` chung khi JWT; **nếu không có service nào chạy job**, `AsyncJob` ở trạng thái `accepted` mãi (mã chỉ cảnh báo `slog.Warn` lúc khởi động) |

Dù ba cờ ở đâu, **`fhir-server` luôn là bên duy nhất migrate**. Nhóm consumer `trivita` của nó chỉ được tạo cho topic có handler (`Broker.Start` tạo nhóm cho "mọi topic đã Subscribe"): khi giao Subscription và tải attachment đều chuyển đi thì `fhir-server` **không đăng ký handler nào nên không tạo nhóm `trivita`** (suy từ `redis.go:Start` và `container.go`, chưa kiểm bằng chạy); nó chỉ `XADD`.

## 4.8 Khi mất Redis

| Thời điểm | Hành vi | Fail đóng hay mở |
|---|---|---|
| **Lúc khởi động** (broker bật) | `Broker.Start` tạo nhóm consumer, lỗi thì `New` trả lỗi và **từ chối khởi động** | đóng |
| **Lúc chạy**, `/readyz` | `Broker.Ping` lỗi thì `broker: false`, **503**; liveness không đổi nên không bị restart; load balancer rút instance khỏi vòng (kể cả khi API không cần Redis cho yêu cầu đó) | đóng (rút khỏi LB) |
| **Lúc chạy**, ghi resource | DB đã commit, `Publish` lỗi chỉ `slog.Error "fhirsub: phát sự kiện lỗi"`; **client vẫn nhận 2xx**; không outbox, không thử lại: Subscription không kích hoạt, attachment không được tải | **mở, mất sự kiện** |
| **Lúc chạy**, đọc | không dùng Redis | không ảnh hưởng |
| Hạn mức `FHIR_RATE_LIMIT_SHARED=true` | lỗi hoặc quá 200 ms thì dùng bộ đếm của replica trong 5 giây rồi thử lại Redis; log tối đa mỗi 30 giây | **mở có giới hạn** |
| Đếm thất bại (tự tắt Subscription) | lỗi `Record` chỉ log, không tắt | mở (không tắt) |
| Sổ hiện diện websocket | lỗi `Present` chỉ log, không phát | mất thông báo |
| Redis mất dữ liệu (flush, thay instance trống) | mất stream, PEL, dead-letter, khóa thất bại; nhóm tạo lại từ `"0"` | mất việc chưa làm; không có cách tự quét lại resource còn lại |

## 4.9 Bí mật: cái nào lưu thế nào

| Bí mật | Nơi | Dạng lưu | Trong log? | Nhạy cảm |
|---|---|---|---|---|
| Mật khẩu CSDL (`DATABASE_URL`, `DATABASE_MIGRATE_URL`) | biến môi trường | bản rõ trong URL | không log URL (`redact`); `DATABASE_ECHO=true` log SQL kèm tham số, **chỉ gỡ lỗi, có thể lộ PHI** | S4 |
| Mật khẩu Redis (trong `REDIS_URL`) | biến môi trường | bản rõ | không | S4 |
| Khóa S3 (`FHIR_S3_ACCESS_KEY_ID`, `FHIR_S3_SECRET_ACCESS_KEY`, `FHIR_S3_SESSION_TOKEN`) | biến môi trường | bản rõ; không hỗ trợ khóa tạm từ IMDS/STS | không | S4 |
| Token tĩnh (`FHIR_READ/WRITE/ADMIN_TOKENS`) | biến môi trường (`tên:secret`) | bản rõ, secret >= 24 ký tự ở production | không | S4 |
| Khóa ký (mục 4.4) | biến môi trường, tệp PEM | bản rõ | không | S4 |
| Bí mật của Subscription (extension `subscription-secret`, dùng ký `X-Signature` rest-hook) | **trong resource `Subscription`** (`fhir_resource.content`, `fhir_history`) | **bản rõ** (mã chỉ đọc để ký; không thấy băm hay mã hóa, chưa kiểm sâu `pkg/fhirsub`) | không (chưa kiểm) | S4; còn lại ở lịch sử kể cả sau khi xóa mềm |
| Nội dung resource trong stream | Redis | bản rõ, không giới hạn thời gian (mục 4.2) | không | **S3** (và S4 nếu resource chứa bí mật, ví dụ `Subscription`) |
| Chữ ký URL `/storage` | URL trả cho client | không lưu | tùy log truy cập của proxy: query có `Signature` (**S4 tạm thời**, hết hạn theo `Expires`) | S4 ngắn hạn |
| Ảnh chụp quyền trong `job-seal` | extension của `AsyncJob` | JSON base64url + HMAC (không mã hóa: lộ `subject`, `grants`, `policy`) | không | S2, S1 |

> Lưu ý: một `Subscription` mang secret ghi vào `fhir_resource` thì sự kiện `ChangeEvent` của chính nó (`resource` đầy đủ) cũng mang secret lên stream `trivita:events:fhir.resource.changed` (suy từ `emit`, không lọc trường: chưa kiểm bằng chạy).
