# Các trường hợp: nhóm `health`

Probe cho orchestrator và Docker. **Công khai** (không cần token), **không chịu hạn mức**, không bị `limitBody`, không qua `guard`. Header chung ở [../common.md](../common.md) (có `X-Request-Id`, header bảo mật). Tham chiếu trường: [../reference/health.md](../reference/health.md). Nguồn: `httpapi.go` (route), `internal/bootstrap/container.go` (`ready`), `pkg/httpserve` (tắt êm).

| Probe | Kiểm gì | Dùng cho |
|---|---|---|
| `/health/live`, `/healthz` | Chỉ tiến trình còn trả lời; **không** chạm DB để orchestrator không restart vì lỗi database | liveness, `HEALTHCHECK` của Docker |
| `/health/startup` | Route đã dựng xong; không kiểm phụ thuộc ngoài | startup probe |
| `/health/ready` | Database trả lời trong `READINESS_TIMEOUT` (mặc định 2s) và không đang tắt | readiness: gỡ khỏi tải khi `503` |

<a id="get-health-live"></a>
## `GET /health/live`

Liveness. [Tham chiếu trường](../reference/health.md#get-health-live)

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Sống | tiến trình nhận được yêu cầu | 200 | - | `{"status":"ok"}` | không |

Luôn `200` kể cả khi DB lỗi hoặc đang tắt (đúng chủ đích). Ví dụ:

```http
GET /health/live HTTP/1.1

HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8

{"status":"ok"}
```

<a id="get-health-ready"></a>
## `GET /health/ready`

Readiness. [Tham chiếu trường](../reference/health.md#get-health-ready)

**Thứ tự kiểm tra:** (1) đang tắt (`draining`, đặt sau SIGTERM) ⇒ không ping DB, trả ngay; (2) ping DB với `READINESS_TIMEOUT`.

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Sẵn sàng | DB trả lời trong hạn, không đang tắt | 200 | - | `{"ready":true,"checks":{"database":true}}` | ping DB |
| C2 | DB lỗi hoặc quá hạn | `Ping` lỗi hoặc vượt `READINESS_TIMEOUT` | 503 | - | `{"ready":false,"checks":{"database":false}}` | ping DB |
| C3 | Đang tắt | nhận SIGTERM, trong `SHUTDOWN_DRAIN_DELAY` và sau đó | 503 | - | `{"ready":false,"checks":{"draining":true}}` | không ping DB |

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json; charset=UTF-8

{"ready":false,"checks":{"draining":true}}
```

**Lưu ý tích hợp:** không restart pod chỉ vì `ready` 503 (liveness độc lập). Khi tắt êm, orchestrator thấy `503` và gỡ instance khỏi tải trong `SHUTDOWN_DRAIN_DELAY` (mặc định 5s) trong khi các yêu cầu đang chạy vẫn được phục vụ; `terminationGracePeriodSeconds` phải lớn hơn `SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT`.

<a id="get-health-startup"></a>
## `GET /health/startup`

Startup. [Tham chiếu trường](../reference/health.md#get-health-startup)

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Đã khởi động | server đã lắng nghe (khởi động từ chối khi cấu hình sai hoặc schema chưa migrate nên tới đây là đủ) | 200 | - | `{"status":"ok"}` | không |

<a id="get-healthz"></a>
## `GET /healthz`

Healthcheck của Docker, hành vi như `live`. Lệnh `dicomweb healthcheck` (dùng trong `HEALTHCHECK` của image distroless, không có `curl`) gọi route này tại `HTTP_ADDR`. [Tham chiếu trường](../reference/health.md#get-healthz)

| # | Trường hợp | Điều kiện | HTTP | Header riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Sống | như `live` | 200 | - | `{"status":"ok"}` | không |
