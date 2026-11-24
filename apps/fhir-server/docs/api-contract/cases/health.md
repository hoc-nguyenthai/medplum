# Trường hợp phản hồi: nhóm `health`

Hai route probe cho orchestrator (Docker HEALTHCHECK, bộ cân bằng tải, K8s nếu có). Cả hai đứng NGOÀI `/fhir/R4`: công khai, không qua `Guard` xác thực, không qua hạn mức theo IP, lỗi (nếu có) là JSON thường `{"error": "..."}` chứ không phải OperationOutcome. Header bảo mật chung (CSP, `X-Frame-Options`, `Cache-Control: no-store`, `X-Request-Id`...) vẫn được middleware gắn. Định dạng lỗi chung của các route FHIR ở [../common.md](../common.md#3-định-dạng-lỗi); nhóm này không dùng.

Tham chiếu trường: [../reference/health.md](../reference/health.md).

Thứ tự middleware áp cho cả hai route (theo `httpapi.New`): Instrument (observability) -> Recover -> RequestID -> SecurityHeaders -> CORS (chỉ gắn header cho đường dẫn dưới `/fhir/R4` và `/storage`, không gắn cho health) -> BodyLimit 16M -> handler. Hạn mức (`FHIR_RATE_LIMIT`) và `Guard` chỉ gắn vào nhóm `/fhir/R4` và `/storage`, nên health không bao giờ nhận 401, 403 hay 429.

| Route | Kiểm gì | Trả 200 khi | Trả 503 khi |
|---|---|---|---|
| `GET /healthz` | không kiểm gì (tiến trình còn xử lý được request) | luôn | không bao giờ |
| `GET /readyz` | cờ đang tắt, database (Ping), broker (Ping, nếu có) | cả ba đều tốt | đang tắt, hoặc database hỏng, hoặc broker hỏng |

Dùng đúng probe: liveness -> `/healthz` (không khởi động lại tiến trình chỉ vì DB hay Redis chết); readiness -> `/readyz` (ngừng gửi traffic khi chưa sẵn sàng). Lệnh con `fhir-server healthcheck` (HEALTHCHECK của image distroless) gọi `GET /healthz` trên cổng của chính tiến trình.

<a id="get-healthz"></a>
## `GET /healthz`

Liveness probe: trả 200 `{"status":"ok"}` nếu tiến trình còn nhận và xử lý được request. [Tham chiếu trường](../reference/health.md#get-healthz)

**Tiền điều kiện / cờ bật route:** luôn bật, không có cờ.

**Thứ tự kiểm tra** (theo mã `httpapi.New`):
1. Middleware chung (Recover, RequestID, SecurityHeaders, BodyLimit). Không có xác thực, không có hạn mức.
2. Handler trả thẳng 200 với body cố định. Không đụng DB, Redis, broker, kho Binary.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| H1 | Tiến trình sống | mọi lần gọi tới được handler | 200 | `Content-Type: application/json` (echo: `application/json; charset=UTF-8`, chưa kiểm charset), `X-Request-Id` | `{"status":"ok"}` | không ghi gì |
| H2 | Đang tắt êm (drain) | sau `BeginShutdown`, trước khi socket đóng | 200 | như H1 | `{"status":"ok"}` | không: liveness KHÔNG phản ánh drain, chỉ `/readyz` đổi |
| H3 | Database, Redis, broker hỏng | bất kỳ | 200 | như H1 | `{"status":"ok"}` | không: cố ý không kiểm phụ thuộc |
| H4 | Phương thức khác GET (POST, HEAD...) | route chỉ đăng ký GET | 405 | `Allow` do echo gắn (chưa kiểm) | `{"error":"Method Not Allowed"}` (chưa kiểm) | không |
| H5 | Tiến trình treo, hết cổng, bị kill | không tới được handler | không có phản hồi (timeout/kết nối bị từ chối) | - | - | orchestrator coi là chết |

**Ví dụ**

```http
GET /healthz HTTP/1.1
Host: fhir.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Cache-Control: no-store, no-cache, must-revalidate
X-Request-Id: 7c1f0e52-2b6a-4d57-9e0e-3c8a1b1d9a10

{"status":"ok"}
```

**Lưu ý tích hợp:** không thêm header `Authorization`; có cũng bị bỏ qua. Không dùng `/healthz` để quyết định cho traffic vào, vì nó vẫn 200 khi DB đã hỏng và trong lúc drain. Bằng chứng: e2e `apps/fhir-server/tests/e2e/fhir_test.go` (gọi `/healthz` và `/readyz` đều 200, `/app healthcheck` thoát 0).

<a id="get-readyz"></a>
## `GET /readyz`

Readiness probe: 200 khi nhận được lưu lượng, 503 khi đang tắt hoặc một phụ thuộc hỏng. Thân luôn là JSON `{"ready": bool, "checks": {...}}` kể cả khi 503. [Tham chiếu trường](../reference/health.md#get-readyz)

**Tiền điều kiện / cờ bật route:** luôn bật. Cấu hình liên quan: `READINESS_TIMEOUT` (mặc định 2s, phải > 0), `SHUTDOWN_DRAIN_DELAY` (mặc định 5s), `SHUTDOWN_TIMEOUT` (mặc định 25s). Mục `broker` chỉ có khi tiến trình dựng broker: cờ Subscription hoặc worker tự tải attachment bật (`s.Subscriptions || s.AutoDownload` trong `bootstrap`), driver `memory` hoặc `redis` (`BROKER_DRIVER`).

**Thứ tự kiểm tra** (theo `Container.Readiness`):
1. Middleware chung (không xác thực, không hạn mức).
2. Tạo ctx với timeout `READINESS_TIMEOUT`.
3. `Store.Ping` (database) trong ctx đó; lỗi thì log WARN `readiness check lỗi` (`check=database`) và `database=false`.
4. Đọc cờ `draining`: `accepting_traffic = !draining`.
5. Nếu có broker: `Broker.Ping` cùng ctx; kết quả vào `checks.broker`.
6. `ready` = database tốt VÀ đang nhận traffic VÀ (broker tốt nếu có). Không tốt: HTTP 503, vẫn trả body.

Redis dùng cho hạn mức dùng chung (`FHIR_RATE_LIMIT_SHARED`) và Redis không phải broker thì KHÔNG được kiểm. Kho Binary (S3/RustFS) cũng không được kiểm.

| # | Trường hợp | Điều kiện | HTTP | Header phản hồi riêng | Body | Tác dụng phụ |
|---|---|---|---|---|---|---|
| R1 | Sẵn sàng, không có broker | DB ping OK, không draining | 200 | `Content-Type: application/json` | `{"ready":true,"checks":{"accepting_traffic":true,"database":true}}` | không |
| R2 | Sẵn sàng, có broker | thêm broker ping OK | 200 | như R1 | `{"ready":true,"checks":{"accepting_traffic":true,"broker":true,"database":true}}` | không |
| R3 | Database hỏng hoặc quá `READINESS_TIMEOUT` | `Store.Ping` lỗi | 503 | như R1 | `{"ready":false,"checks":{"accepting_traffic":true,"database":false}}` | log WARN `readiness check lỗi` |
| R4 | Broker hỏng | `Broker.Ping` lỗi | 503 | như R1 | `ready:false`, `checks.broker:false` | không log riêng (chưa kiểm) |
| R5 | Đang tắt êm | `BeginShutdown` đã gọi (nhận tín hiệu dừng) | 503 | như R1 | `ready:false`, `checks.accepting_traffic:false` | không; sau `SHUTDOWN_DRAIN_DELAY` server mới đóng listener |
| R6 | Nhiều điều kiện xấu cùng lúc | ví dụ draining và DB hỏng | 503 | như R1 | mọi mục tương ứng `false` | log như R3 |
| R7 | Phương thức khác GET | route chỉ đăng ký GET | 405 | `Allow` (chưa kiểm) | `{"error":"Method Not Allowed"}` (chưa kiểm) | không |

Khóa trong `checks` là map nên thứ tự khóa trong JSON do bộ mã hóa quyết định (sắp theo tên); client không nên dựa vào thứ tự.

**Ví dụ**

```http
GET /readyz HTTP/1.1
Host: fhir.example.org
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Request-Id: 0f4a7a6e-91c3-4c1c-8d52-5f0a6b0e2d11

{"ready":true,"checks":{"accepting_traffic":true,"database":true}}
```

Đang tắt (cùng cấu trúc, khác mã và giá trị):

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{"ready":false,"checks":{"accepting_traffic":false,"database":true}}
```

**Lưu ý tích hợp:** `ready=false` không có thông điệp lỗi, chỉ có cờ từng phụ thuộc; xem log máy chủ để biết lý do DB. Probe nên có timeout lớn hơn `READINESS_TIMEOUT` (2s) để không nhầm "chậm" với "chết". Trình tự tắt: nhận tín hiệu -> `/readyz` 503 ngay -> chờ `SHUTDOWN_DRAIN_DELAY` để bộ cân bằng tải rút -> đóng listener và chờ request đang chạy tối đa `SHUTDOWN_TIMEOUT` -> `Stop` đóng tài nguyên; trong khoảng đó `/healthz` vẫn 200.

## Phát hiện lệch với common.md

`common.md` chưa có lúc viết (chưa đối chiếu). Ghi chú về tài liệu sinh/doc cũ so với mã:

- `reference/health.md` và chú thích swagger nói broker kiểm "chỉ khi bật Subscription". Mã dựng broker khi `Subscriptions || AutoDownload` (worker tự tải attachment cũng cần broker), nên `checks.broker` có thể xuất hiện dù Subscription tắt.
- Swagger mô tả `/readyz` 503 gồm "database trong READINESS_TIMEOUT": đúng, nhưng timeout dùng chung ctx cho cả database và broker (một ngân sách `READINESS_TIMEOUT` cho cả hai, không phải mỗi phụ thuộc một lần).
