# 2. Luồng người dùng

Gateway không có giao diện: "người dùng" là ứng dụng client (trình duyệt, viewer, hệ thống tích hợp) và người vận hành.

## Phần A: Subscription WebSocket

### Người dùng 1: ứng dụng client của Subscription

| Bước | Làm gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1 | Xin token ở `fhir-server` (`$get-ws-binding-token`) | `token`, `expiration`, `websocket-url` | Lỗi của bên cấp, không thuộc gateway |
| 2 | Mở WebSocket tới `websocket-url` | Nâng cấp thành công (101) | 403: kiểm `Origin` của ứng dụng web; 429: lùi và thử lại sau `Retry-After`; 503: gateway quá tải hoặc đang tắt, thử instance khác sau |
| 3 | Gửi `bind-with-token` trong 10 giây | Một `handshake` | Đóng 1008: lấy token mới (đừng thử lại nguyên xi); đóng 1013: thử lại có backoff ngẫu nhiên |
| 4 | Bind thêm Subscription (tối đa 50 mỗi kết nối) | `handshake` cho mỗi cái | 1008 `quá nhiều Subscription trên một kết nối`: mở kết nối khác; 1008 `User has exceeded ...`: gỡ bớt binding cũ |
| 5 | Chờ thông báo | `event-notification` kèm resource; `heartbeat` mỗi 30 giây | Không thấy gì lâu: không kết luận được là không có sự kiện (xem Lệch); tự đối soát bằng đọc lại |
| 6 | Gửi `ping` khi cần kiểm kết nối | `{"type":"pong"}` | Không có pong: coi như đứt, nối lại |
| 7 | Gỡ Subscription bằng `unbind-from-token` | Không có phản hồi; thôi nhận | Token sai: đóng 1008 |
| 8 | Access token gốc sắp hết hạn | Tới hạn: binding thôi nhận (không báo) khi có sự kiện tiếp | Lấy token bind mới và bind lại trước hạn |

Mã đóng WebSocket dẫn tới hành động:

| Mã | Ý nghĩa | Hành động |
|---|---|---|
| 1001 | Gateway đang tắt | Nối lại có backoff ngẫu nhiên; token mới nếu cũ quá 5 phút |
| 1003 | Khung không phải văn bản hoặc JSON hỏng | Sửa client |
| 1008 | Token hỏng, hết hạn, phiên bản lạ, hết hạn mức, bind trễ | Lấy token mới; không thử lại nguyên xi |
| 1009 | Khung quá 64 KiB | Sửa client |
| 1011 | Lỗi nội bộ gateway | Báo vận hành, nối lại |
| 1013 | Redis lỗi lúc bind hoặc người nhận quá chậm | Thử lại sau; nếu do chậm, đọc nhanh hơn |

Mã HTTP trước khi nâng cấp: 403 (Origin hoặc không xác định IP), 429 (quá số kết nối mỗi IP hoặc quá tốc độ mở), 503 (đủ kết nối hoặc đang tắt).

### Người dùng 2: vận hành

| Bước | Làm gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1 | Đặt biến (khóa ký giống bên cấp, `REDIS_URL`, Origin, proxy tin cậy) | Gateway khởi động | Thoát kèm danh sách biến sai: sửa theo thông báo, không hạ `ENVIRONMENT` để né |
| 2 | Gắn probe: sống `/health/live`, sẵn sàng `/health/ready` | 200 và `{"ready":true,"checks":{...}}` | 503 `redis=false`: kiểm Redis (liveness vẫn 200, không restart pod) |
| 3 | Theo dõi metric `ws_gateway.*` | kết nối, bind, `slow`, `denied`, `malformed`, `redis.up` | `slow` tăng: client chậm; `denied` tăng: quyền; `malformed` tăng: kênh bị ghi sai |
| 4 | Xoay khóa | Token cũ vẫn được nhận trong lúc xoay | Sau một hạn token, bỏ khóa cũ |
| 5 | Triển khai cuốn chiếu | Instance cũ đóng 1001 cho socket của nó | Client phải tự bind lại |

### Bảng độ trễ

| Sự việc | Độ trễ | Nguồn |
|---|---|---|
| Hạn gửi bind đầu tiên | 10 giây | hằng `BindTimeout` |
| Hạn token bind | 5 phút (do bên cấp) | `TokenTTL` mặc định |
| Heartbeat và ping | 30 giây | `FHIR_WS_HEARTBEAT` |
| Hạn mục sổ hiện diện | 30 giây, gia hạn mỗi 10 giây | `FHIR_WS_PRESENCE_TTL` |
| Gỡ đăng ký sau khi socket cuối đóng | tối đa 10 giây (một vòng dọn) | TTL/3 |
| Hạn mỗi lời gọi Redis của Feed | 2 giây | hằng |
| Phát hiện access token hết hạn | khi có sự kiện kế tiếp (không theo đồng hồ) | `Hub.notify` |
| Tắt êm | 5 giây drain, đóng 1001 tối đa 5 giây, tổng tối đa 25 giây | `SHUTDOWN_*` |

---

## Phần B: FHIRcast

Hub không có giao diện; "màn hình" là của ứng dụng lâm sàng (client) và của người vận hành. Mỗi bảng dưới đây mô tả điều ứng dụng gọi, thấy và nên làm.

### Nhân vật: ứng dụng lâm sàng nhận sự kiện (subscriber)

| Bước | Làm gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1 | Lấy token từ auth (scope `fhircast/...`), ghi lại `hub.url` và `hub.topic` | Phản hồi token có `hub.url`, `hub.topic` | Không có hai trường: kiểm cấu hình auth; token thiếu scope sẽ bị 403 ở Hub |
| 2 | `GET {hub.url}/.well-known/fhircast-configuration` (không cần token) | `eventsSupported`, `websocketSupport: true`, `webhookSupport: false` | Không có `websocketSupport`: không dùng Hub này |
| 3 | `POST {hub.url}` subscribe với `hub.channel.type=websocket`, `hub.topic`, `hub.events` | 202 và `hub.channel.endpoint` | 400 `Invalid hub.channel.type`: chỉ dùng websocket; 400 `Invalid hub.events`: bỏ tên lạ; 401: lấy token mới; 403: thiếu scope hoặc project |
| 4 | Nối `GET` endpoint bằng WebSocket (không gửi Bearer) | Khung xác nhận có `hub.lease_seconds` | Khung `denied` `invalid endpoint` rồi đóng 1000: đăng ký đã hết hạn hoặc đã hủy, hãy làm lại bước 3; khung `denied` `too many connections for this endpoint` rồi 1008: đóng bớt socket cũ |
| 5 | `GET {hub.url}/{hub.topic}` để biết ngữ cảnh hiện tại | STU3 có `context.type`, `context`, `context.versionId`; STU2 chỉ mảng | 400 `Invalid hub.topic`: sửa topic |
| 6 | Nhận sự kiện và heartbeat | Mỗi 10 giây một heartbeat (`context` `period`) | Không thấy heartbeat quá hai chu kỳ: coi như mất kết nối, đăng ký lại và `GET` ngữ cảnh |
| 7 | Cần dừng: `POST {hub.url}` unsubscribe với `hub.channel.endpoint` | 202; socket nhận `denied` rồi đóng 1000 | 400 `Invalid endpoint`: sai project, topic, hoặc người; đã hủy |

### Nhân vật: ứng dụng lâm sàng phát sự kiện (publisher)

| Bước | Làm gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1 | `POST {hub.url}` hoặc `POST {hub.url}/{topic}` thân `{id, timestamp, event{hub.topic, hub.event, context}}` | 202 `{"success": true, "event": ...}` có `context.versionId` | 400 theo bảng F-11: sửa thân; 400 `Topic mismatch`: topic đường dẫn phải bằng `hub.topic` |
| 2 | `DiagnosticReport-update` kèm `context.versionId` mới nhất | 202, có `context.priorVersionId` | 400 `Expected event.context.versionId to be ...`: đọc lại ngữ cảnh (F-13) rồi gửi lại; 409: có người cập nhật chen vào, đọc lại rồi thử lại |
| 3 | Đóng: `...-close` | 202; ngữ cảnh bị xóa | Không lỗi riêng (Hub không kiểm đúng tài nguyên) |

### Nhân vật: vận hành

| Bước | Làm gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1 | Đặt `FHIRCAST_ENABLED=true`, `FHIRCAST_PUBLIC_URL`, issuer, audience, JWKS | Gateway khởi động và các route `/fhircast/*`, `/hub`, `/ws/fhircast/*` có mặt | Từ chối khởi động kèm danh sách lỗi (BR-75): sửa cấu hình |
| 2 | Kiểm `GET /hub/.well-known/fhircast-configuration` | 200 `fhircastVersion: STU3` | 404: Hub chưa bật |
| 3 | Theo dõi số socket, heartbeat, lỗi 1013 | Đóng 1013 hàng loạt: Redis hoặc client chậm | Kiểm Redis; tăng `FHIRCAST_MAX_CONNECTIONS` nếu nhận 503 |
| 4 | Staging/production | Cấu hình `ws://`, HMAC, JWKS http, tắt scope bị từ chối khi khởi động | Dùng `wss://`, JWKS https |

### Mã đóng WebSocket và mã HTTP dẫn tới hành động

| Mã | Khi nào | Hành động của client |
|---|---|---|
| 1000 sau khung `denied` | Hủy, hết lease, endpoint không hợp lệ | Đăng ký lại (endpoint mới) rồi `GET` ngữ cảnh |
| 1008 | Quá số socket mỗi endpoint | Đóng socket thừa, không đăng ký lại ngay |
| 1009 | Gửi khung lớn hơn 64 KiB | Đừng gửi gì lên (Hub không đọc nội dung) |
| 1011 | Handler panic (hiếm) | Thử lại |
| 1013 | Redis lỗi, hoặc client quá chậm | Thử lại với trễ tăng dần; xử lý sự kiện nhanh hơn |
| 1001 | Tắt êm | Nối lại sau khi dịch vụ lên |
| HTTP 401 | Token sai hoặc thiếu | Lấy token mới ở auth |
| HTTP 403 | Thiếu project hoặc scope (HTTP) hoặc Origin lạ (nâng cấp) | Kiểm scope `fhircast/*` hoặc Origin |
| HTTP 409 | Update tranh chấp phiên bản | Đọc lại ngữ cảnh, gửi lại |
| HTTP 413 | Thân quá lớn | Giảm kích thước ngữ cảnh |
| HTTP 429 / 503 khi nâng cấp | Hạn mức IP/tốc độ, quá số kết nối, đang tắt | Chờ rồi thử lại (429 có `Retry-After: 5`) |

### Độ trễ và bất đồng bộ

| Sự kiện | Hiệu lực |
|---|---|
| Đăng ký xong | Có hiệu lực ngay cho lần nối websocket sau |
| Đổi ngữ cảnh | Ghi Redis rồi phát; tới socket gần như tức thì, không bền (at-most-once) |
| Hủy đăng ký | Socket đang nối nhận `denied` ngay; nếu không phát được thì đến hết lease |
| Thu hồi token ở auth | Không có tác dụng với socket đã nối; token hết hạn chỉ chặn các lệnh HTTP tiếp theo |
| Hết lease | Socket đóng đúng hạn (đồng hồ của instance giữ socket) |
| Ngữ cảnh không hoạt động | Tự mất sau `FHIRCAST_CONTEXT_TTL` (24 giờ) |
