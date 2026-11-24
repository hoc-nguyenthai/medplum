# Trường hợp phản hồi: nhóm `websocket`

Tham chiếu: [../reference/websocket.md](../reference/websocket.md). Quy ước chung và khung lỗi: [../common.md](../common.md). Giao thức sau khi nâng cấp (khung `bind-with-token`, `handshake`, `event-notification`, mã đóng): [../../api-contract.md](../../api-contract.md) mục 3 đến 5; khung FHIRcast: [fhircast.md](fhircast.md).

## Thứ tự kiểm tra trước nâng cấp (chung cho hai route)

1. Chỉ khi cấu hình bật (`FHIRCAST_ENABLED=true` cho route FHIRcast; route Subscription luôn có), nếu không route không tồn tại: `404` `{"error":"Not Found"}`.
2. Hạn mức mở kết nối theo IP (`FHIR_WS_RATE_LIMIT`, middleware echo): vượt thì `429` JSON, `Retry-After: 1`; không xác định được IP thì `403` `{"error":"không xác định được người gọi"}`.
3. `pkg/wsgroup` (lỗi là văn bản thuần, `text/plain`, không phải JSON): đang tắt → `503` "đang tắt"; hết `FHIR_WS_MAX_CONNECTIONS` → `503` "quá nhiều kết nối"; vượt `FHIR_WS_MAX_CONNS_PER_IP` → `429` "quá nhiều kết nối từ địa chỉ này" + `Retry-After: 5`.
4. Nâng cấp WebSocket và kiểm Origin (`FHIR_WS_ALLOWED_ORIGINS`; rỗng thì chỉ cùng host khi trình duyệt gửi Origin): Origin lạ → `403` do thư viện websocket sinh (thông điệp nguyên văn của thư viện, `(chưa kiểm)`); thiếu header Upgrade → lỗi HTTP của thư viện (thường `400` hoặc `426`, `(chưa kiểm)`).
5. Thành công: `101 Switching Protocols`. Chỗ đã đặt trước được trả lại ở mọi đường thoát, kể cả Accept thất bại (`TestMaxConnsRejectsWith503AndLeaksNothing`).

Tên test chứng cứ: `TestMaxConnsRejectsWith503AndLeaksNothing`, `TestOriginPolicy`, `TestMaxConnsPerIP`, `TestConcurrentUpgradesNeverExceedMaxConns`, `TestCloseSends1001AndRefusesNewUpgrades` (`pkg/wsgroup/wsgroup_test.go`); `TestRateLimitAppliesToWebSocketOnly`, `TestRateLimitIdentityFollowsIPExtractor` (`apps/ws-gateway/internal/presentation/httpapi/httpapi_test.go`).

| Điều kiện | HTTP | Body | Header | Tác dụng phụ |
|---|---|---|---|---|
| Vượt `FHIR_WS_RATE_LIMIT` | 429 | `{"error":"quá nhiều yêu cầu, thử lại sau"}` | `Retry-After: 1` | không giữ chỗ |
| Không xác định được IP | 403 | `{"error":"không xác định được người gọi"}` | | không |
| Đang tắt | 503 | `đang tắt` (text) | | không |
| Hết tổng số kết nối | 503 | `quá nhiều kết nối` (text) | | không |
| Vượt số kết nối mỗi IP | 429 | `quá nhiều kết nối từ địa chỉ này` (text) | `Retry-After: 5` | không |
| Origin không được phép | 403 | văn bản của thư viện (chưa kiểm) | | chỗ đặt trước được trả |
| Hợp lệ | 101 | không | `Upgrade: websocket` | mở kết nối, đăng ký vào nhóm; handler panic thì đóng 1011 (`TestHandlerPanicClosesWith1011`); khung quá 64 KiB thì đóng 1009 (`TestOversizedFrameClosesWith1009`) |

<a id="get-ws-subscriptions-r4"></a>
## `GET /ws/subscriptions-r4`

Không có header xác thực; xác thực nằm trong khung đầu tiên sau khi nâng cấp.

Sau `101`:

| Tình huống | Kết quả |
|---|---|
| Client gửi `bind-with-token` hợp lệ trong 10 giây | server trả `handshake`, từ đó đẩy `heartbeat` và `event-notification` (xem api-contract.md mục 3) |
| Không gửi khung đầu trong 10 giây, hoặc token sai chữ ký, hết hạn, sai phiên bản | server đóng kết nối (mã đóng ở api-contract.md mục 3; giá trị chính xác `(chưa kiểm)` trong tệp này, tra bảng mã đóng) |
| Khung lớn hơn 64 KiB | đóng 1009 |
| Server tắt | đóng 1001 `server shutting down` |

```http
GET /ws/subscriptions-r4 HTTP/1.1
Host: gateway.example
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Version: 13
Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==
```

```http
HTTP/1.1 101 Switching Protocols
Upgrade: websocket
Connection: Upgrade
```

<a id="get-ws-fhircast-endpoint"></a>
## `GET /ws/fhircast/{endpoint}`

Chỉ có khi `FHIRCAST_ENABLED=true`; ngược lại `404` `{"error":"Not Found"}`. Cũng chịu mọi bước ở mục thứ tự kiểm tra ở trên (cùng hạn mức, cùng `wsgroup`). Đường dẫn có thêm đoạn sau `/ws/fhircast/` (`/ws/fhircast/*`) vào cùng handler.

| Điều kiện | Kết quả |
|---|---|
| `endpoint` đúng, còn hiệu lực | `101`; khung đầu tiên là xác nhận đăng ký (khác nhau STU2/STU3, xem [fhircast.md](fhircast.md)), sau đó sự kiện đã đăng ký và heartbeat mỗi `FHIRCAST_HEARTBEAT` |
| `endpoint` sai hoặc đã hủy | `101` rồi server đóng kết nối với lý do `invalid endpoint` (lỗi xảy ra sau nâng cấp, không phải HTTP 4xx) |
| Đã đủ `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` socket trên instance này | server đóng kết nối ngay sau nâng cấp (mã và lý do `(chưa kiểm)`; xem fhircast.md và mã `pkg/fhircast/ws`) |
| Không cần `Authorization` | endpoint là bí mật 122 bit, ai biết endpoint thì nhận được sự kiện |

```http
GET /ws/fhircast/7f1c2a9e-4b3d-4e8a-9c1f-2d6b0a5e8f34 HTTP/1.1
Host: gateway.example
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Version: 13
Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==
```

## Phát hiện lệch / điểm chưa kiểm

- Thông điệp 403 khi sai Origin và mã đóng chi tiết đến từ thư viện hoặc agent khác, chưa đối chiếu nguyên văn ở tệp này.
- Cần đối chiếu với `common.md` mục 5 (hạn mức WS) khi các agent báo lệch.
