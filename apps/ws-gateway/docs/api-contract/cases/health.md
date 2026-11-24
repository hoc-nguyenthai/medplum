# Trường hợp phản hồi: nhóm `health`

Tham chiếu trường: [../reference/health.md](../reference/health.md). Quy ước chung (header, định dạng lỗi): [../common.md](../common.md). Bốn route công khai, không xác thực, **không chịu hạn mức mở kết nối** (hạn mức chỉ gắn vào route WebSocket: `TestRateLimitAppliesToWebSocketOnly` trong `apps/ws-gateway/internal/presentation/httpapi/httpapi_test.go`).

## Liveness chung (`/health/live`, `/health/startup`, `/healthz`)

Ba route cùng một handler: luôn `200` với `{"status":"ok"}` khi tiến trình còn nhận được yêu cầu. Không kiểm Redis, không phụ thuộc trạng thái tắt, nên orchestrator không khởi động lại tiến trình khi Redis chết. `/healthz` là đích của `HEALTHCHECK` Docker.

| Điều kiện | HTTP | Body | Tác dụng phụ |
|---|---|---|---|
| Tiến trình còn sống | 200 | `{"status":"ok"}` | không |
| Đang tắt (đã `BeginShutdown`) | 200 | như trên | không (liveness không đổi khi tắt) (chưa kiểm bằng test riêng) |
| Redis chết | 200 | như trên | không |

<a id="get-health-live"></a>
## `GET /health/live`

Xem [Liveness chung](#liveness-chung-healthlive-healthstartup-healthz). Ví dụ:

```http
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
```

<a id="get-health-startup"></a>
## `GET /health/startup`

Trả y hệt `GET /health/live` (cùng handler). Dùng làm startup probe; vì handler chỉ chạy sau khi cổng đã lắng nghe nên probe đạt ngay khi server nhận kết nối.

<a id="get-healthz"></a>
## `GET /healthz`

Trả y hệt `GET /health/live`; dành cho `HEALTHCHECK` của Docker (`pkg/httpserve.HealthCheck`).

<a id="get-health-ready"></a>
## `GET /health/ready`

Readiness: nhận được lưu lượng mới hay không. `Container.Readiness` (`apps/ws-gateway/internal/bootstrap/container.go`) ping Redis với thời hạn `READINESS_TIMEOUT`, và xét cờ đang tắt.

| Điều kiện | HTTP | Body | Tác dụng phụ |
|---|---|---|---|
| Redis ping được và chưa tắt | 200 | `{"ready":true,"checks":{"accepting_traffic":true,"redis":true}}` | không |
| Redis không ping được (hoặc quá `READINESS_TIMEOUT`) | 503 | `{"ready":false,"checks":{"accepting_traffic":true,"redis":false}}` | ghi log cảnh báo `readiness check lỗi` (check=redis) |
| Đã gọi `BeginShutdown` (SIGTERM) | 503 | `{"ready":false,"checks":{"accepting_traffic":false,"redis":true}}` | không; orchestrator ngừng gửi kết nối mới trong khi các socket cũ được đóng êm (1001) |
| Không cấu hình hàm `Ready` (chỉ trong test dựng `httpapi.New`) | 200 | `{"ready":true,"checks":{}}` | không |

Thông báo test: `TestHealthEndpoints`. Giá trị `checks` ở dạng thật lấy từ mã (`accepting_traffic`, `redis`); các ví dụ body 503 là suy từ mã, `(chưa kiểm)` bằng test chạy thật với Redis chết ngoài `tests/integration` của service.

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{"ready":false,"checks":{"accepting_traffic":true,"redis":false}}
```
