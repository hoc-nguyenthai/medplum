# auth: các trường hợp của nhóm `health`

Hai probe cho orchestrator (Docker, Docker Compose, load balancer). Quy ước chung (header, envelope lỗi, hạn mức) ở [../common.md](../common.md); bảng trường ở [../reference/health.md](../reference/health.md). Nguồn: `internal/presentation/httpapi/health.go`, `internal/bootstrap/container.go` (`Readiness`, `BeginShutdown`), test `internal/bootstrap/container_test.go`.

**Probe nào phụ thuộc gì**

| Đường dẫn | Phụ thuộc kiểm | Trả khi nào |
|---|---|---|
| `GET /health/live` (và hai bí danh không có trong swagger: `/health/startup`, `/healthz` dùng cho `HEALTHCHECK` của Docker) | không kiểm gì | luôn `200` khi tiến trình còn nhận được kết nối, kể cả khi đang tắt êm (draining) hoặc DB hỏng |
| `GET /health/ready` | DB (`Store.Ping` với hạn `READINESS_TIMEOUT`, mặc định 2 giây) và cờ "đang nhận traffic" (`accepting_traffic`) | `200` khi cả hai đúng, `503` khi một trong hai sai |

Redis, SMTP, JWKS của client, fhir-server **không** được kiểm: Redis hỏng không làm `ready` đỏ (các nhánh dùng Redis tự fail đóng ở route tương ứng).

Hai route health nằm **ngoài** hạn mức IP và CORS (chỉ áp cho `/auth/`, `/oauth2/`, `/.well-known/`, `/admin/`, `/scim/`, `/email/`), nên probe dày không bị `429`.

<a id="get-health-live"></a>
## `GET /health/live`

Báo tiến trình còn sống. Không kiểm phụ thuộc. [Tham chiếu trường](../reference/health.md#get-health-live)

**Cờ bật route:** luôn bật, không cấu hình.

**Thứ tự kiểm tra:** không có (không xác thực, không tham số, không đọc DB).

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Còn sống | mọi yêu cầu | 200 | - (`Content-Type: application/json`, các header chung ở common.md mục 2) | `{"status":"ok"}` | không |
| C2 | Sai method | `POST /health/live` | 405 | - | `{"error":"Method Not Allowed"}` (chưa kiểm header `Allow`) | không |

**Ví dụ**

```http
GET /health/live HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store
X-Request-Id: 7c1d0f5e-2a4b-4c11-9a77-1f0e6d3b2a10

{"status":"ok"}
```

**Lưu ý tích hợp:** dùng làm liveness (khởi động lại khi không phản hồi). Không dùng làm readiness: nó vẫn `200` khi DB hỏng hoặc đang tắt êm.

<a id="get-health-ready"></a>
## `GET /health/ready`

Báo instance có nên nhận traffic hay không. [Tham chiếu trường](../reference/health.md#get-health-ready)

**Cờ bật route:** luôn bật. `READINESS_TIMEOUT` (mặc định `2s`) là hạn ping DB; `SHUTDOWN_DRAIN_DELAY` (mặc định `5s`) là thời gian giữ tiến trình sống nhưng báo `503` trước khi đóng.

**Thứ tự kiểm tra** (hàm `Container.Readiness`):
1. Ping DB với context có hạn `READINESS_TIMEOUT`; lỗi hoặc quá hạn: `database=false` và ghi log `WARN readiness check lỗi` (không lộ lỗi ra body).
2. Đọc cờ `draining` (bật bởi `BeginShutdown` khi nhận SIGTERM): `accepting_traffic = !draining`.
3. `ready = database && accepting_traffic`; `200` nếu đúng, `503` nếu sai. Body luôn cùng cấu trúc.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| C1 | Sẵn sàng | DB ping thành công và chưa tắt | 200 | - | `{"ready":true,"checks":{"accepting_traffic":true,"database":true}}` | không ghi dữ liệu (một lần ping DB) |
| C2 | DB lỗi hoặc ping quá hạn | `Ping` lỗi trong `READINESS_TIMEOUT` | 503 | - | `{"ready":false,"checks":{"accepting_traffic":true,"database":false}}` | log WARN |
| C3 | Đang tắt êm | đã `BeginShutdown` (SIGTERM), trong `SHUTDOWN_DRAIN_DELAY` | 503 | - | `{"ready":false,"checks":{"accepting_traffic":false,"database":true}}` | không |
| C4 | Cả hai sai | vừa tắt vừa DB lỗi | 503 | - | `{"ready":false,"checks":{"accepting_traffic":false,"database":false}}` | log WARN |
| C5 | Khung chưa khai báo phụ thuộc | `Deps.Ready == nil` (chỉ test của khung, không xảy ra ở service thật) | 200 | - | `{"ready":true,"checks":{}}` | không |

`checks` là map; JSON Go sắp khóa theo thứ tự chữ cái (`accepting_traffic` trước `database`).

**Ví dụ**

```http
GET /health/ready HTTP/1.1
Host: auth.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store

{"ready":true,"checks":{"accepting_traffic":true,"database":true}}
```

Phản hồi `503` có cùng cấu trúc (khác `ready` và giá trị trong `checks`):

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json; charset=UTF-8
Cache-Control: no-store

{"ready":false,"checks":{"accepting_traffic":false,"database":true}}
```

**Lưu ý tích hợp:**
- Cấu hình readiness của load balancer theo mã HTTP (200/503), đọc `checks` để chẩn đoán. Khi rolling update, load balancer cần rút instance trong `SHUTDOWN_DRAIN_DELAY` (5 giây mặc định) trước khi tiến trình đóng.
- `READINESS_TIMEOUT` ngắn hơn timeout probe của orchestrator, nếu không probe hết giờ trước khi nhận được `503`.
- Mỗi lần gọi là một lần ping DB: đừng đặt chu kỳ quá dày (hạn mức IP không áp cho route này).

## Phát hiện lệch với common.md

- common.md mục 7 liệt kê vùng áp hạn mức IP không gồm `/health/*` và mục 8 nói `503` ở `/health/ready` "khi đang tắt hoặc DB lỗi": khớp mã. Không có lệch ở nhóm này.
- Swagger chỉ mô tả hai route `live` và `ready`; mã còn đăng ký `/health/startup` và `/healthz` (cùng nội dung `live`).
