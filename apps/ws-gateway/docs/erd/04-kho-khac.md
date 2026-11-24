# ws-gateway: kho dữ liệu (không có cơ sở dữ liệu SQL)

`ws-gateway` **không mở kết nối cơ sở dữ liệu**, không có schema, bảng hay migration; vì vậy bộ ERD của nó chỉ có tệp này. Toàn bộ dữ liệu là **Redis** (khóa, kênh pub/sub) và **bộ nhớ tiến trình** (danh sách kết nối, bộ đếm, đăng ký đang nghe). Không có tệp, không có kho Binary.

| Kho | Loại | Tiền tố | Ai ghi | Ai đọc | Bắt buộc ở production? |
|---|---|---|---|---|---|
| Kênh Subscription | Redis pub/sub | `trivita:events:ch:fhirws.sub.<subscriptionId>` | `fhir-server` hoặc `fhir-notifier` (`PublishChannel`) | ws-gateway (Feed) | có (không có Redis thì `/health/ready` trả 503) |
| Sổ hiện diện Subscription | Redis ZSET | `trivita:events:presence:fhirws.bound.<subscriptionId>` | ws-gateway (`Mark`, gia hạn) | phía phát (`Present`) | có |
| Đăng ký FHIRcast | Redis string | `fhircast:ep:<endpoint>` | ws-gateway (Hub) | ws-gateway (socket) | chỉ khi `FHIRCAST_ENABLED=true` |
| Ngữ cảnh hiện tại | Redis string | `fhircast:p:<project>:t:<hash topic>:latest` | ws-gateway | ws-gateway | như trên |
| Ngữ cảnh đã lưu theo báo cáo | Redis hash | `fhircast:p:<project>:t:<hash topic>:ctxs` | ws-gateway | ws-gateway | như trên |
| Topic của người dùng | Redis string | `fhircast:user-topic:<user>` | ws-gateway (`UserTopic`, được `auth` gọi qua cùng kho) | auth, ws-gateway | như trên |
| Kênh FHIRcast | Redis pub/sub | `fhircast:ch:<project>:<hash topic>` | ws-gateway (Hub) | ws-gateway (mọi instance) | như trên |
| Kết nối, bộ đếm, bind | bộ nhớ tiến trình | không | ws-gateway | ws-gateway | không (mất khi khởi động lại) |

Tiền tố `trivita:events` là mặc định của `pkg/messaging` (`Prefix`), **gateway không cấu hình lại được** (mã `pkg/messaging/redis.go:74`); tiền tố `fhircast` là mặc định của `NewRedisStore` khi truyền chuỗi rỗng (`pkg/fhircast/redis_store.go:23`, bootstrap truyền `""`). Kênh FHIRcast không có tiền tố thêm (`RedisBus.Publish` dùng nguyên `Channel(project, topic)`).

## 1. Subscription WebSocket

### Kênh `trivita:events:ch:fhirws.sub.<id>`
- **Nội dung:** `WSMessage` (thông điệp `event-notification` sẽ chuyển cho người nhận, kèm resource) dạng JSON văn bản. Chứa dữ liệu lâm sàng (S3 trở lên: PHI) đi qua Redis dạng chữ thường.
- **Ai nghe:** Feed của ws-gateway **chỉ** đăng ký kênh cho Subscription đang có socket bind; bỏ đăng ký khi không còn socket (dọn theo chu kỳ `Refresh`).
- **Độ bền:** pub/sub của Redis, **at-most-once**: thông điệp phát khi không ai nghe, hoặc trong lúc đứt kết nối, bị mất; go-redis tự nối lại và đăng ký lại (`pkg/messaging/redis_channels.go`). Không có xác nhận, không phát lại.
- **Redis chết:** socket đã bind không bị ngắt; client vẫn nhận `heartbeat` (sinh cục bộ) nhưng không nhận sự kiện (xem `business/04-luat-va-phan-quyen.md` mục 4.4).

### Sổ hiện diện `trivita:events:presence:fhirws.bound.<id>`
- **Kiểu:** ZSET, `member` = `InstanceID` của gateway, `score` = thời điểm hết hạn (ms, theo đồng hồ **Redis** qua Lua `TIME`, nên không lệ thuộc lệch giờ giữa máy).
- **Ghi:** `Mark` (script Lua: xóa mục hết hạn, `ZADD`, đặt `PEXPIRE` nếu TTL còn ngắn hơn). TTL mặc định **30 giây** (`PresenceTTL`), gia hạn mỗi `PresenceTTL/3` (10 giây) trong khi còn socket.
- **Đọc:** `Present` đếm mục chưa hết hạn (`ZCOUNT`); phía phát chỉ phát lên kênh khi `> 0`, để không phát vô ích cho Subscription không ai nghe.
- **Thứ tự bắt buộc:** đăng ký kênh **trước** rồi mới ghi sổ (nếu ngược, sự kiện phát giữa hai bước lọt mất).
- **Xóa:** `Unmark` (`ZREM`) khi socket cuối của Subscription đóng; không thì tự hết hạn sau tối đa 30 giây.
- **Mất sổ (Redis mất dữ liệu):** phía phát coi như không ai nghe cho tới khi chu kỳ gia hạn ghi lại (tối đa 10 giây).

## 2. Hub FHIRcast (chỉ khi `FHIRCAST_ENABLED=true`)

| Khóa | Giá trị | TTL | Ghi chú |
|---|---|---|---|
| `fhircast:ep:<endpoint>` | JSON `{project, topic, events, version, user, expiresAt}` | lease (`FHIRCAST_LEASE`, mặc định 1 giờ) | `endpoint` là UUID v4 từ `crypto/rand` (122 bit), **chính là khóa bí mật** vào socket; ai biết endpoint thì nối được |
| `fhircast:p:<project>:t:<sha256 topic, base64url>:latest` | ngữ cảnh hiện tại (JSON, tối đa `FHIRCAST_MAX_CONTEXT_BYTES`, mặc định 256 KiB) | `FHIRCAST_CONTEXT_TTL` (mặc định 24 giờ) | ghi bằng so sánh-rồi-đặt (`WATCH/MULTI/EXEC`, thử lại tối đa `casRetries`); xung đột là `409` |
| `fhircast:p:<project>:t:<hash>:ctxs` | hash `id báo cáo -> ngữ cảnh` | như trên | tối đa 64 mục mỗi topic (`MaxReportContexts`); vượt thì `ErrTooManyContexts` |
| `fhircast:user-topic:<user>` | UUID v4 topic của người dùng | lease | tạo bằng `SETNX`, gia hạn mỗi lần lấy; `auth` gọi khi cấp token FHIRcast |
| kênh `fhircast:ch:<project>:<hash>` | thông điệp sự kiện hoặc `denied` | không (pub/sub) | cô lập theo project nằm **trong tên kênh và khóa**; topic được băm nên tên topic không lộ ra khóa |

- **Cô lập:** chỉ theo project (trong khóa). Gateway không kiểm topic thuộc về người dùng nào; xem `business/04-luat-va-phan-quyen.md` mục 4.4.
- **Nhạy cảm:** ngữ cảnh chứa định danh bệnh nhân/ca khám (S3 trở lên); `ep:` là bí mật truy cập (S3). Mã giữ ngữ cảnh có hạn (24 giờ) khác bản gốc Medplum (không hạn) vì lý do PHI (`pkg/fhircast/events.go:30`).
- **Redis chết:** Hub trả `500` ở thao tác cần Redis (không lộ chi tiết); socket đã nối không bị cắt, tới hết lease.
- **Không bền:** kênh `fhircast:ch:*` at-most-once; hàng đợi đăng ký đầy thì bỏ thông điệp và ghi log `hàng đợi đăng ký đầy` (`pkg/fhircast/bus.go`).

## 3. Bộ nhớ tiến trình

| Dữ liệu | Giới hạn | Mất khi |
|---|---|---|
| Danh sách socket (`wsgroup`) | `FHIR_WS_MAX_CONNECTIONS` (mặc định 1000), `FHIR_WS_MAX_CONNS_PER_IP` | khởi động lại: client phải nối và bind lại |
| Binding mỗi kết nối | 50 Subscription (hằng, không cấu hình), `FHIR_WS_MAX_BINDINGS_PER_USER` | như trên |
| Đăng ký đang nghe trong Feed | theo Subscription có socket | như trên |
| Bộ đếm hạn mức IP (`FHIR_WS_RATE_LIMIT`) | `ExpiresIn` 3 phút | như trên; **mỗi instance đếm riêng** |

Mọi hạn mức tính **theo instance**, không dùng chung qua Redis: N bản sao cho phép gấp N lần.

## 4. Vận hành

- **ACL Redis:** gateway cần quyền publish/subscribe trên `trivita:events:ch:*` và `fhircast:ch:*`, đọc ghi trên `trivita:events:presence:*` và `fhircast:*`, và chạy script Lua (`EVALSHA`). Cấu hình ACL cụ thể (chưa kiểm).
- **Dọn dẹp:** không cần job dọn; mọi khóa đều có TTL. Không có khóa nào không hết hạn trừ kênh (không lưu).
- **Kích thước:** không tăng không giới hạn: ngữ cảnh ≤ 256 KiB nhân số topic nhân 64 báo cáo là trần lý thuyết mỗi topic; không có trần số topic hay số đăng ký mỗi người (đã ghi ở điểm lệch).
