# 4. Kho khác ngoài SQL, bộ nhớ và bí mật

Tất cả khóa dưới đây nằm trên **một Redis** (`REDIS_URL`, bắt buộc). Tiền tố `trivita:events` là mặc định của `messaging.NewRedis` (`pkg/messaging/redis.go`); notifier gọi `messaging.New` không đặt `Prefix`, `ws-gateway` gọi `messaging.NewRedis(..., RedisOptions{})` cũng không đặt, nên hai bên trùng; **không có biến môi trường đổi tiền tố** (chưa kiểm ở `fhir-server`, bên phát stream).

## 4.1 Stream sự kiện và nhóm consumer `fhir-notifier`

| Khóa | Giá trị | TTL | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| stream `trivita:events:fhir.resource.changed` (`prefix:topic`, topic = `fhirsub.EventType`) | mỗi entry một trường `data` = JSON `Envelope` (`id`, `type`, `occurred_at`, `payload`=`ChangeEvent` kèm **resource đầy đủ**, xem 02 mục 2.3) | **không có** (không `MAXLEN`, không hết hạn; chưa kiểm phía `XADD` ở `fhir-server`, worker đã xác nhận không có) | `fhir-server`, `fhir-worker` (`XADD` sau commit) | `fhir-notifier`, `fhir-downloader`, `fhir-server` (mỗi bên một nhóm) | sự kiện chưa xử lý không tới; **dữ liệu không mất** (resource đã ở CSDL), nhưng thông báo không được giao |
| nhóm consumer `fhir-notifier` trên stream trên (`BROKER_GROUP`) | trạng thái nhóm: vị trí đã đọc, **PEL** (danh sách message đã giao cho consumer nhưng chưa `XACK`) | không có | tạo bởi `XGROUP CREATE ... MKSTREAM` với vị trí **`0`** lúc `Broker.Start` (đã tồn tại thì bỏ qua `BUSYGROUP`) | `fhir-notifier` | nhóm mất (Redis trắng): tạo lại ở vị trí `0` và **đọc lại toàn bộ stream còn lại** (giao lại mọi thông báo còn trong stream) |
| consumer `fhir-notifier-<8 ký tự id>` | tên consumer của **một tiến trình** (`Group + "-" + core.NewID()[:8]`), đổi mỗi lần khởi động | không có | `XREADGROUP` | `fhir-notifier` | tiến trình chết: consumer cũ ở lại trong nhóm, message của nó nằm trong PEL tới khi bị claim |
| stream dead-letter `trivita:events:fhir.resource.changed:dead` | bản sao entry gốc kèm `original_id`, `deliveries` | không có | `fhir-notifier` (`reclaim` → `deadLetter`, rồi `XACK` entry gốc) | không ai trong mã (xem kiểm tay) | mất: không còn dấu vết thông báo bị bỏ |

**Đọc, ACK, claim (từ `pkg/messaging/redis.go`, `apps/fhir-notifier/internal/bootstrap/container.go`):**

- `XREADGROUP` (`Count 10`, `Block 1s`, song song `Concurrency = FHIR_WEBHOOK_WORKERS`). Handler là `Manager.handle` (chế độ `Durable`): giao đồng bộ trong handler, **`XACK` chỉ khi handler trả `nil`** (mọi lần giao xong hoặc đã bỏ cuộc). Handler trả lỗi (ngữ cảnh bị hủy lúc tắt, hoặc không decode được sự kiện: lỗi `Decode` trả về nên **không ACK**) thì message ở lại PEL.
- `reclaim` (mỗi `ClaimInterval = min(max(ClaimIdle/3, 500ms), 10s)`): `XPENDING` các message idle `>= ClaimIdle`, `XCLAIM` về consumer này, chạy lại. `ClaimIdle` mặc định `Config.DurableClaimIdle()` = `max(2 x (MaxAttempts x Timeout + tổng backoff), 1 phút)`, hoặc `BROKER_CLAIM_IDLE` (phải `>= MaxAttempts x Timeout`).
- Giao quá `MaxDeliveries` (mặc định **5**, notifier không đổi) lần mà vẫn lỗi: chép sang `...:dead` rồi ACK (message độc không chặn hàng đợi).
- Entry bị xóa khỏi stream mà còn trong PEL: ACK để dọn. Message có `data` không phải JSON `Envelope` hợp lệ: **ACK và bỏ** (log lỗi).
- **Giao ít nhất một lần:** claim lại giao **toàn bộ sự kiện** kể cả Subscription đã được giao ở lần trước (webhook không có mã sự kiện để khử trùng lặp).

**Bắt buộc?** Có: `REDIS_URL` bắt buộc ở mọi môi trường; không có broker bộ nhớ.

**Rủi ro dữ liệu:** (a) stream chứa **dữ liệu lâm sàng S3 dạng rõ, mọi project, không giới hạn độ dài và thời gian** (notifier chỉ đọc nên không trim); (b) tên nhóm `trivita` bị cấm (`config.go`: cùng nhóm với `fhir-server` thì chia nhau sự kiện); (c) nhóm mới ở vị trí `0` nên bật notifier lần đầu hoặc đổi `BROKER_GROUP` sẽ **phát lại mọi sự kiện còn trong stream** tới webhook (giao trùng hàng loạt).

## 4.2 Khóa đếm thất bại dùng chung (failure-tracker)

| Khóa | Giá trị | TTL | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| `trivita:sub:failures:<subscriptionId>` (`NewRedisFailureTracker(c, "", nil)`: tiền tố rỗng thành mặc định `trivita:sub:failures`; **không** dùng tiền tố `trivita:events`) | **sorted set (ZSET)**: mỗi thất bại một member `<unix mili>:<id ngẫu nhiên>` với **điểm = unix mili** lúc thất bại | `EXPIRE` = cửa sổ lớn nhất trong `FHIR_SUBSCRIPTION_AUTO_DISABLE`, **đặt lại mỗi lần ghi** | `fhir-notifier` (mọi replica): `Record` khi một lần giao bỏ cuộc | `fhir-notifier` (`ZCOUNT` ngay trong `Record`) | xem dưới |

- **`Record`** (một `MULTI` qua `TxPipeline`): `ZREMRANGEBYSCORE key -inf <now - cửa sổ lớn nhất>` (tỉa), `ZADD`, `ZCOUNT key <now - cửa sổ của từng trigger> +inf` (mỗi trigger một lệnh), `EXPIRE key <cửa sổ lớn nhất>`. Trigger đầu tiên (theo thứ tự cấu hình) có `ZCOUNT >= maxConsecutiveFailures` thì tắt Subscription.
- **`Clear`:** `UNLINK key`, gọi khi **mọi lần giao thành công** (chỉ khi `AutoDisable` khác rỗng) và sau khi tắt thành công.
- **Dùng chung:** mọi replica notifier cùng Redis đếm chung, nên ngưỡng tính trên cả cụm. Nếu `fhir-server` cũng chạy `fhirsub.Manager` giao rest-hook với `RedisFailureTracker` cùng tiền tố, hai bên **dùng chung khóa** (chưa kiểm cấu hình ở `fhir-server`; giai đoạn chuyển BR-21 cũng giao đôi).
- **Khi `FHIR_SUBSCRIPTION_AUTO_DISABLE` rỗng:** không có khóa nào (cả `Record` lẫn `Clear` thoát sớm), notifier không ghi và không đọc khóa này.
- **Khi mất Redis (đứt kết nối):** `Record` trả lỗi, chỉ log `không ghi được thất bại để tự tắt Subscription`; **không tắt Subscription, thất bại đó không được đếm**; giao hàng không bị ảnh hưởng (độc lập). `Clear` lỗi chỉ log `không xóa được bộ đếm thất bại`: bộ đếm cũ có thể còn, thành công sau đó không xóa được nên có thể **tắt nhầm** một Subscription đã hồi phục khi thất bại mới cộng với điểm cũ còn trong cửa sổ.
- **Khi mất dữ liệu (Redis khởi động lại trắng, `FLUSHALL`):** bộ đếm về 0, Subscription hỏng được đếm lại từ đầu (chậm tắt hơn).
- **Nhạy cảm:** S1 (chỉ id Subscription và thời điểm).
- Tên khóa suy từ mã (`failure.go` `key`); chưa kiểm bằng `KEYS` trên Redis thật.

## 4.3 Kênh `fhirws.sub.<id>` và sổ hiện diện `fhirws.bound.<id>` (notifier phát và đọc, ws-gateway ghi sổ)

| Khóa | Giá trị | TTL | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| kênh Pub/Sub `trivita:events:ch:fhirws.sub.<subscriptionId>` (`WSChannel(id)` = `"fhirws.sub." + id`, qua `channelName`) | JSON `WSMessage` `{"sub","event"}` (02 mục 2.4), `event` chứa **resource đầy đủ** | không có (Pub/Sub không lưu) | **`fhir-notifier`** (`PUBLISH`, `Manager.publishRemote`) | `ws-gateway` (`SubscribeChannel` trong `Feed.ensure`) | không người nghe hoặc đứt kết nối: thông điệp **mất** (at-most-once) |
| sổ hiện diện `trivita:events:presence:fhirws.bound.<subscriptionId>` (`WSPresenceKey(id)` = `"fhirws.bound." + id`, qua `presenceKey`) | **ZSET**: member = `InstanceID` của gateway (ngẫu nhiên mỗi process hoặc `s.InstanceID`), **điểm = thời điểm hết hạn (unix mili, giờ của Redis)** | `PEXPIRE` = `FHIR_WS_PRESENCE_TTL` của gateway (mặc định 30 giây), gia hạn mỗi TTL/3; member hết hạn bị xóa khi `Mark` | `ws-gateway` (`Mark` khi có socket bind, `Unmark` khi hết socket hoặc tắt) | **`fhir-notifier` chỉ `Present`** (script Lua `ZCOUNT key (now +inf`, giờ của Redis) | xem dưới |

- **Quyết định phát (`publishRemote`):** chỉ khi `FHIR_WS_MODE=issuer`, Subscription `channel.type=websocket` đã khớp (loại, interaction, hạn, project, `criteria`): gọi `Present`; `false` thì chỉ tăng bộ đếm `Unheard` (không phát); `true` thì `json.Marshal(WSMessage)` rồi `PUBLISH`, tăng `Published`. Mỗi sự kiện khớp một lần cho cả cụm (handler nhóm, không broadcast).
- **Notifier không bao giờ ghi sổ hiện diện** (không `Mark`, không `Unmark`).
- **Khi `Present` lỗi** (Redis đứt): log `không đọc được sổ hiện diện websocket`, **không phát** (không đếm `Unheard`). **Khi `PublishChannel` lỗi:** log WARN, không thử lại, không đếm `Published`.
- **Sổ mất hoặc hết hạn trong lúc gateway còn sống:** notifier tưởng không có ai nghe nên ngừng phát tới khi gateway gia hạn (tối đa TTL/3 chu kỳ gia hạn); sự kiện trong khoảng đó mất.
- **Tiền tố:** tài liệu nghiệp vụ gọi tắt `fhirws.sub.<id>` và `fhirws.bound.<id>`; khóa thật có thêm `trivita:events:ch:` và `trivita:events:presence:`. Hai phía phải cùng tiền tố (xem đầu tệp).
- **Nhạy cảm:** kênh mang S3 (resource đầy đủ) trong khi đi trên Redis dạng rõ; sổ hiện diện là S1. Gateway mới lọc theo quyền từng người nhận.
- **Bắt buộc?** Chỉ khi `FHIR_WS_MODE=issuer`; `off` thì Subscription websocket bị bỏ qua khi khớp và notifier không đọc sổ.

## 4.4 Bộ nhớ của tiến trình

| Thứ | Nội dung | Mất khi tiến trình chết? | Hệ quả |
|---|---|---|---|
| Cache Subscription đang hoạt động (`Manager.cache`) | danh sách `entry` đã dựng (kể cả `secret` và `headers` ở bản rõ) từ `Subscription?status=active`, sống `CacheTTL` 30 giây, làm mới ngay khi có sự kiện loại `Subscription` tới replica này | có | nạp lại ở lần khớp kế; lỗi nạp thì **dùng bản cũ** (log `không nạp được Subscription, dùng bản cũ`). Bản cũ của một Subscription đã `off` hoặc xóa vẫn còn giao tới khi nạp được |
| Bản chụp bộ đếm `Stats` | `Matched`, `Delivered`, `Retried`, `Failed`, `Dropped`, `Published`, `Unheard` | có | chỉ số vận hành, không bền |
| Việc giao đang chạy | goroutine giao trong handler (Durable: không có hàng đợi bộ nhớ) | có | không mất việc: message còn trong PEL, nơi khác giao lại |

## 4.5 Bí mật: cái nào lưu thế nào

| Bí mật | Nơi | Dạng lưu | Có trong log? | Ghi chú |
|---|---|---|---|---|
| `Subscription.extension[subscription-secret]` (khóa HMAC) | `fhir_resource.content` và **mọi phiên bản trong `fhir_history`**; cache bộ nhớ của notifier | **bản rõ** | không (không `slog` nào in); xem rủi ro `DATABASE_ECHO` ở 03 | S4. Ai đọc được Subscription (và lịch sử, kể cả sau xóa mềm) đọc được. Lần tự tắt tạo thêm một bản chứa nó |
| `Subscription.channel.header` (thường `Authorization`) | như trên | **bản rõ** | không in header; `error` có thể chứa URL | S4 |
| `Subscription.channel.endpoint` (có thể chứa token ở query) | như trên | bản rõ | **có thể**: `error` của `http.Client.Do` và `url.Parse` chứa URL đầy đủ ở log bỏ cuộc và log endpoint không hợp lệ (03 mục 3.3) | S4 nếu chứa token |
| Chữ ký `X-Signature` | gửi tới bên nhận (HMAC-SHA256 hex của thân bằng secret) | không lưu | không | S4 (dẫn xuất) |
| Mật khẩu CSDL (`DATABASE_URL`) | biến môi trường | bản rõ trong URL | không (không log URL; `DATABASE_ECHO` log SQL kèm tham số, chỉ gỡ lỗi) | S4 |
| Mật khẩu Redis (nếu có trong `REDIS_URL`) | biến môi trường | bản rõ trong URL | không | S4 |
| Nội dung resource trong stream và kênh | Redis | bản rõ | không | S3 |

Notifier **không lưu và không đọc** bí mật xác thực của người dùng (mật khẩu, MFA, refresh token): đó là schema `identity` của `auth`, mà role `fhir_app` không đọc được.

## 4.6 SQLite (dev)

`DATABASE_URL=sqlite://<tệp>`: một tệp dùng chung với `fhir-server` chỉ phù hợp khi chạy cùng máy; không schema, cột `content` là text, không role; **cấm ở staging/production** (`config.go`).
