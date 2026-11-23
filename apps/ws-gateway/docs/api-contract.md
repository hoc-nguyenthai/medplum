# ws-gateway: hợp đồng API

Cập nhật 2026-11-23. `ws-gateway` giữ kết nối WebSocket cho Subscription R4 và (tùy chọn) Hub FHIRcast. **Không có OpenAPI**: giao thức chính là WebSocket nên bảng dưới viết tay từ mã (`internal/presentation/httpapi`, `pkg/fhirsub/ws.go`, `pkg/fhircast`). Không có test máy so bảng này với route thật; khi đổi route thì sửa tay và kiểm bằng test của `httpapi` và e2e (`apps/ws-gateway/tests/e2e`). Cấu hình ở [config.md](config.md); kiến trúc ở [architecture.md](architecture.md); chi tiết vận hành ở [../README.md](../README.md).

## 1. Địa chỉ

Cổng mặc định `:8007`. Cần Redis dùng chung với `fhir-server` và khóa `FHIR_WS_SIGNING_KEY` giống bên cấp token.

## 2. Route HTTP và WebSocket

| Route | Giao thức | Xác thực | Điều kiện | Mô tả |
|---|---|---|---|---|
| `GET /ws/subscriptions-r4` | WebSocket | token bind gửi **trong khung đầu tiên** (không có header) | luôn có | Kênh Subscription R4 (Subscriptions R5 Backport), giống Medplum |
| `GET /health/live`, `/health/startup`, `/healthz` | HTTP | công khai | luôn có | Còn sống (`/healthz` cho HEALTHCHECK Docker) |
| `GET /health/ready` | HTTP | công khai | luôn có | Sẵn sàng: Redis và đang nhận traffic; `503` khi đang tắt hoặc Redis lỗi |
| `GET {v}/.well-known/fhircast-configuration` | HTTP | công khai | `FHIRCAST_ENABLED=true` | Khám phá Hub |
| `POST {v}/` | HTTP | Bearer (JWT của auth, scope `fhircast/*`) | `FHIRCAST_ENABLED=true` | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh |
| `POST {v}/{topic}` | HTTP | Bearer | `FHIRCAST_ENABLED=true` | Gửi sự kiện cho topic |
| `GET {v}/{topic}` | HTTP | Bearer | `FHIRCAST_ENABLED=true` | Đọc ngữ cảnh hiện tại |
| `GET /ws/fhircast/{endpoint}` | WebSocket | `endpoint` là bí mật 122 bit do `POST {v}/` cấp (không cần Bearer khi nối) | `FHIRCAST_ENABLED=true` | Kênh sự kiện FHIRcast |

`{v}` là `/fhircast/STU2`, `/fhircast/STU3` hoặc `/hub` (STU3).

Probe và đăng ký FHIRcast ở trên lấy từ mã định tuyến (`httpapi.go`, `fhircast.go`).

## 3. Kênh Subscription: `/ws/subscriptions-r4`

**Cách lấy token bind.** `fhir-server` (chế độ `FHIR_WS_MODE=issuer`) cấp token ở `GET|POST /fhir/R4/Subscription/{id}/$get-ws-binding-token` → `Parameters {token, expiration, websocket-url}`. Token ký HMAC-SHA256, hạn ngắn, mang ảnh chụp quyền của người gọi. Gateway chỉ **xác minh** token bằng `FHIR_WS_SIGNING_KEY` (khóa cũ ở `FHIR_WS_SIGNING_KEY_PREVIOUS` khi xoay khóa).

**Thông điệp client gửi** (JSON văn bản):

| Thông điệp | Hiệu quả |
|---|---|
| `{"type":"bind-with-token","payload":{"token":"..."}}` | Bind một Subscription (nhiều Subscription trên một kết nối, tối đa 50); gateway trả `handshake` |
| `{"type":"unbind-from-token","payload":{"token":"..."}}` | Gỡ bind (token hết hạn vẫn gỡ được); kết nối giữ nguyên |
| `{"type":"ping"}` | Trả `{"type":"pong"}` |

Tin đầu tiên **phải** là bind, trong `BindTimeout` (10 giây), nếu không bị đóng `1008`.

**Server gửi:** `Bundle` loại `history` chứa `SubscriptionStatus` với `type` là `handshake` (ngay sau bind), `heartbeat` (định kỳ `FHIR_WS_HEARTBEAT`), hoặc `event-notification` (kèm resource) mỗi khi có thay đổi khớp tiêu chí.

**Mã đóng WebSocket:**

| Mã | Khi nào | Client nên |
|---|---|---|
| `1008` | Token sai, hết hạn hoặc sai phiên bản; vượt hạn mức bind (`User has exceeded allotted maximum number of concurrent WebSocket subscriptions: <n>`); tin đầu không phải bind | Không thử lại nguyên xi; lấy token mới |
| `1013` | Tạm thời không nhận sự kiện được (Redis lỗi lúc bind) hoặc người nhận quá chậm | **Thử lại sau** |
| `1003` | Khung không phải văn bản hoặc JSON hỏng | Sửa client |
| `1009` | Khung quá lớn (64 KiB) | Sửa client |
| `1001` | Gateway đang tắt | Nối lại (vào instance khác) |

**Mã HTTP khi nối (trước khi nâng cấp):** `503` đang tắt hoặc hết `FHIR_WS_MAX_CONNECTIONS`; `429` quá `FHIR_WS_MAX_CONNS_PER_IP` hoặc `FHIR_WS_RATE_LIMIT`; `403` `Origin` không được phép (`FHIR_WS_ALLOWED_ORIGINS`).

**Đảm bảo giao hàng:** sự kiện **không bền** (at-most-once): mất khi gateway khởi động lại hoặc Redis đứt. Client dựa vào `heartbeat` và `eventsSinceSubscriptionStart` để phát hiện thiếu.

## 4. Hub FHIRcast

- **Xác thực HTTP:** Bearer JWT của `auth`, kiểm bằng JWKS (`FHIRCAST_JWKS_URL`; HMAC chỉ cho dev). Cần claim project và scope `fhircast/*`; không cần quyền `fhir.*`. Thiếu hoặc sai token: `401`; thiếu project hoặc scope: `403`.
- **Cô lập:** project nằm trong khóa Redis nên hai project không thấy nhau; nhiều gateway chung Redis thấy cùng ngữ cảnh.
- **Chỉ kênh `websocket`:** không có `webhook`/`rest-hook`/`hub.callback`; gateway không bao giờ gọi mạng ra ngoài.
- **Khác Medplum:** sự kiện phải thuộc danh sách 14 sự kiện; `POST {v}/{topic}` từ chối topic đường dẫn khác `event["hub.topic"]` (đặt `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` để như Medplum); hủy đăng ký chỉ bởi đúng người tạo; ngữ cảnh có hạn và có trần kích thước.
- **Kênh `/ws/fhircast/{endpoint}`:** tối đa `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` socket mỗi endpoint mỗi instance. Khung đầu tiên luôn là xác nhận (STU3 bốn trường, STU2 thêm bốn trường rỗng); sau đó là sự kiện đã đăng ký (`heartbeat` và `syncerror` luôn tới), `heartbeat` cục bộ mỗi `FHIRCAST_HEARTBEAT`.
- **Khám phá Hub từ token:** khi `auth` đặt `AUTH_FHIRCAST_HUB_URL`, token có scope `fhircast/*` kèm `hub.url` và `hub.topic` (xem [auth api-contract](../../auth/docs/api-contract.md)).

Mô tả đầy đủ của Hub (đăng ký, ngữ cảnh, trần kích thước, biến `FHIRCAST_*`) ở [../README.md](../README.md) mục FHIRcast và [config.md](config.md).

## 5. Chưa có trong hợp đồng

- Không có OpenAPI hoặc AsyncAPI cho kênh WebSocket; bảng mã đóng ở trên là nguồn của hợp đồng.
- Số liệu OTLP của kênh FHIRcast; hạn mức theo IP cho route HTTP của FHIRcast; giới hạn socket mỗi endpoint tính theo cả cụm (hiện theo instance).
