# ws-gateway: kiến trúc

## Vai trò

Giữ các kết nối WebSocket dài và phục vụ Hub FHIRcast. Tách khỏi `fhir-server` vì tải khác nhau: gateway scale theo **số kết nối**, `fhir-server` scale theo **số yêu cầu**; deploy `fhir-server` không còn làm rớt websocket. Quyết định và lý do: [docs/plans/modules/split-scim-ws-services.md](../../../docs/plans/modules/split-scim-ws-services.md) (phương án B).

## Không làm gì

- **Không có cơ sở dữ liệu** và không có nguồn Subscription: token bind do `fhir-server` ký đã chứng minh Subscription hợp lệ lúc cấp.
- Không khớp tiêu chí Subscription (việc của `fhir-server`, một lần cho cả cụm).
- Không cấp token bind (`fhir-server` cấp) và không cấp access token (`auth` cấp).
- FHIRcast **không** gọi ra ngoài (không webhook, không `hub.callback`): `pkg/fhircast` cấm import `net` và client HTTP (kiểm bằng `architecture_test.go`).

## Phụ thuộc

| Phụ thuộc | Dùng để | Khi vắng |
|---|---|---|
| Redis (bắt buộc) | kênh sự kiện, sổ hiện diện; với FHIRcast: đăng ký, ngữ cảnh, kênh theo topic | Khởi động được nhưng `/health/ready` trả 503 cho tới khi kết nối được; `/health/live` vẫn 200 (K8s không restart vì lỗi của Redis); tự hồi phục khi Redis lên |
| `FHIR_WS_SIGNING_KEY` (bắt buộc) | xác minh token bind của `fhir-server` | Từ chối khởi động |
| JWKS của `auth` (chỉ khi bật FHIRcast) | xác minh Bearer của route HTTP FHIRcast | JWKS được cache nên đã tải khóa thì vẫn xác minh khi `auth` tạm tắt |
| Redis của `auth` (`AUTH_REDIS_URL`) | `auth` nhớ topic FHIRcast ổn định cho từng người dùng; **gateway không đọc khóa này** | `auth` không cấp token có `fhircast/` (500) khi kho topic lỗi; token thường không bị ảnh hưởng |

## Thành phần (đều ở `pkg/`, gateway chỉ lắp ráp trong `internal/bootstrap`)

| Thành phần | Gói | Việc |
|---|---|---|
| `wsgroup.Group` | `pkg/wsgroup` | nhận upgrade, kiểm Origin, giới hạn kết nối (tổng và theo IP, đặt chỗ trước khi Accept), bắt panic, đóng êm |
| `fhirsub.Hub` + `Feed` | `pkg/fhirsub` | websocket Subscription: xác minh token, bind/unbind, lọc theo quyền từng người nhận; `Feed` nghe kênh Redis của Subscription có socket và ghi sổ hiện diện có TTL |
| `fhircast.Hub` | `pkg/fhircast` | máy trạng thái FHIRcast: đăng ký, hủy, đổi ngữ cảnh, đọc ngữ cảnh (Redis) |
| `ws.Server` | `pkg/fhircast/ws` | kênh `/ws/fhircast/{endpoint}`: xác nhận, lọc theo sự kiện đã đăng ký, heartbeat cục bộ, lease, giới hạn socket mỗi endpoint |
| HTTP | `internal/presentation/httpapi` | Echo: probe, route websocket, route FHIRcast (`/fhircast/STU2`, `/fhircast/STU3`, `/hub`) |

## Luồng

```
Subscription:  client --$get-ws-binding-token--> fhir-server (kiểm Subscription, chụp quyền, ký token)
               client --ws /ws/subscriptions-r4, bind-with-token--> gateway (xác minh, ghi sổ hiện diện)
               fhir-server: ghi tài nguyên -> khớp tiêu chí -> sổ hiện diện có gateway nghe? -> PUBLISH fhirws.sub.<id> --Redis--> gateway -> socket

FHIRcast:      client --đăng nhập--> auth: token có scope fhircast/* kèm hub.url và hub.topic (ổn định theo người dùng)
               client --POST /hub (Bearer JWT của auth)--> gateway: đăng ký -> hub.channel.endpoint (bí mật 122 bit)
               client --ws /ws/fhircast/{endpoint}--> gateway: xác nhận
               ứng dụng khác --POST /hub (đổi ngữ cảnh)--> gateway: cập nhật ngữ cảnh (CAS) -> PUBLISH kênh topic --Redis--> mọi gateway -> socket
```

## Hành vi khi lỗi

| Tình huống | Hành vi |
|---|---|
| Redis chết | `ready` 503 (rút khỏi Service), `live` 200; socket đang mở giữ nguyên nhưng không nhận sự kiện mới (at-most-once, sự kiện lúc đứt bị mất); tự nối lại |
| Redis lỗi lúc bind | bind thất bại với 1013 (thử lại), hoàn tác binding và hạn mức |
| Token bind giả hoặc ký bằng khóa lạ | đóng 1008 |
| Hết lease FHIRcast | gửi `denied` "lease expired" rồi đóng 1000; đăng ký lại để tiếp tục |
| Client chậm | hàng đợi gửi đầy thì đóng 1013 (không để bộ nhớ phình) |
| SIGTERM | `ready` 503 -> chờ `SHUTDOWN_DRAIN_DELAY` -> 1001 cho mọi socket (tối đa `FHIR_WS_CLOSE_TIMEOUT`) -> gỡ sổ hiện diện -> đóng Redis; mã thoát 0 |
| Cấu hình sai | từ chối khởi động, nói rõ biến nào sai, mã thoát khác 0 |

## Giới hạn đã biết

Hạn mức bind theo người, theo IP và số socket mỗi endpoint đếm **theo instance**: cả cụm cho tối đa N x số replica. Sự kiện không bền (at-most-once). Chưa có số liệu OTLP cho kênh FHIRcast.
