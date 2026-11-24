# Tham chiếu API: nhóm `websocket`

> **Tệp này được SINH từ `swagger.json`** (Trivita WS Gateway API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/websocket.md](../cases/websocket.md).

Route nâng cấp lên WebSocket: phản hồi thành công là 101 Switching Protocols, sau đó là khung dữ liệu (xem docs/api-contract.md). Lỗi trước nâng cấp là HTTP thường.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /ws/fhircast/{endpoint}`](#get-ws-fhircast-endpoint) | WebSocket FHIRcast |
| [`GET /ws/subscriptions-r4`](#get-ws-subscriptions-r4) | WebSocket Subscription R4 |

---

<a id="get-ws-fhircast-endpoint"></a>
## `GET /ws/fhircast/{endpoint}`

**WebSocket FHIRcast**

Chỉ có khi FHIRCAST_ENABLED=true. Nâng cấp lên WebSocket để nhận sự kiện đã đăng ký. endpoint là bí mật 122 bit do POST {v}/ cấp (không cần Bearer khi nối); đường dẫn có nhiều đoạn sau /ws/fhircast/ cũng vào cùng handler và endpoint sai bị đóng với "invalid endpoint". Khung đầu tiên luôn là xác nhận, sau đó là sự kiện đã đăng ký, heartbeat cục bộ mỗi FHIRCAST_HEARTBEAT. Tối đa FHIRCAST_MAX_SOCKETS_PER_ENDPOINT socket mỗi endpoint mỗi instance.

Các trường hợp phản hồi: [../cases/websocket.md#get-ws-fhircast-endpoint](../cases/websocket.md#get-ws-fhircast-endpoint)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `text/plain` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `endpoint` | đường dẫn | string | có | Endpoint bí mật do đăng ký cấp (phần cuối của hub.channel.endpoint) |

**Phản hồi**

**`101`** Switching Protocols: kênh đã mở

Không có body.

**`403`** Origin không nằm trong FHIR_WS_ALLOWED_ORIGINS

**`429`** Quá hạn mức mở kết nối (text hoặc JSON {error}, như WebSocket Subscription)

**`503`** Đang tắt hoặc hết hạn mức kết nối

---

<a id="get-ws-subscriptions-r4"></a>
## `GET /ws/subscriptions-r4`

**WebSocket Subscription R4**

Nâng cấp lên WebSocket (Subscriptions R5 Backport, như Medplum). Không dùng header xác thực: client gửi {"type":"bind-with-token","payload":{"token":"..."}} trong khung đầu tiên với token do fhir-server cấp ($get-ws-binding-token), trong 10 giây. Thông điệp, mã đóng và đảm bảo giao hàng: docs/api-contract.md mục 3. Chịu hạn mức mở kết nối theo IP (FHIR_WS_RATE_LIMIT, FHIR_WS_MAX_CONNS_PER_IP) và giới hạn FHIR_WS_MAX_CONNECTIONS.

Các trường hợp phản hồi: [../cases/websocket.md#get-ws-subscriptions-r4](../cases/websocket.md#get-ws-subscriptions-r4)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `text/plain` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`101`** Switching Protocols: kênh đã mở

Không có body.

**`403`** Origin không nằm trong FHIR_WS_ALLOWED_ORIGINS

**`429`** Quá FHIR_WS_MAX_CONNS_PER_IP (text, Retry-After: 5) hoặc quá FHIR_WS_RATE_LIMIT (JSON {error}, Retry-After: 1)

**`503`** Đang tắt hoặc hết FHIR_WS_MAX_CONNECTIONS
