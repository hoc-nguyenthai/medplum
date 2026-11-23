# ws-gateway: runbook vận hành

Cập nhật 2026-11-23. Dành cho người trực vận hành riêng `ws-gateway` (kết nối WebSocket của Subscription R4 và Hub FHIRcast); tự chứa đủ để vận hành mà không cần mở runbook khác. Mọi tên biến, lệnh và giá trị lấy từ mã hiện tại (`internal/config`, `pkg/httpserve`, `pkg/wsgroup`, `pkg/fhircast`) và từ các runbook chung của repo mà phần này được tách ra. Số đo định cỡ ở mục 4 đo **thật** trên binary (`make bench-ws`); các mục còn lại chưa diễn tập trên cụm triển khai thật, ngoài những chỗ ghi rõ "đã kiểm". Cấu hình từng biến ở [config.md](config.md); giao thức và mã đóng ở [api-contract.md](api-contract.md); lý do thiết kế ở [architecture.md](architecture.md).

## 1. Kiểm tra sức khỏe

| Endpoint | Ý nghĩa |
|---|---|
| `GET /health/live`, `/health/startup`, `/healthz` | Process còn sống (liveness). `/healthz` dùng cho `HEALTHCHECK` của Docker. **Không** kiểm Redis nên orchestrator không khởi động lại tiến trình vì lỗi Redis |
| `GET /health/ready` | `200` khi nhận được lưu lượng; `503` khi đang tắt (drain) hoặc Redis không kết nối được. Body `{"ready":bool,"checks":{...}}` cho biết từng phụ thuộc |

Redis chưa lên lúc khởi động **không** làm tiến trình thoát: readiness giữ `503` cho tới khi kết nối được. Mỗi lần kiểm Redis chờ tối đa `READINESS_TIMEOUT` (2s).

## 2. Khởi động, tắt, triển khai

- **Tắt êm** (SIGTERM): readiness `503` ngay → chờ `SHUTDOWN_DRAIN_DELAY` (5s) để load balancer rút instance → đóng mã `1001` mọi socket (chờ tối đa `FHIR_WS_CLOSE_TIMEOUT`, 5s) → gỡ mục hiện diện (bên phát thôi gửi ngay) → đóng Redis → thoát mã 0. Client im lặng có thể giữ thêm tối đa khoảng 5 giây ở nền (giới hạn của thư viện).
- **Thời gian chờ của orchestrator** (`stop_grace_period`, `terminationGracePeriodSeconds`...) PHẢI lớn hơn `SHUTDOWN_DRAIN_DELAY` (5s) + `SHUTDOWN_TIMEOUT` (25s) + `OTEL_SHUTDOWN_TIMEOUT_MS` (5s). Ví dụ đặt 45s. Đã kiểm trên cụm thật: khi instance bị gỡ, readiness báo 503 ngay, client nhận `1001` sau ~5 giây (drain delay), instance biến mất trước 12 giây.
- **Cuốn chiếu** (rolling update): cho thêm instance mới trước, không bớt instance cũ cho tới khi instance mới sẵn sàng, đợi khoảng 10 giây sau khi sẵn sàng. Mỗi instance cũ đóng `1001` cho socket của nó; **client phải bind lại có backoff ngẫu nhiên** (lấy token mới nếu token cũ quá 5 phút) để không dồn cả đàn một lúc. Đã kiểm: số endpoint sẵn sàng không xuống dưới 2 (với 2 replica), thay xong trong ~26 giây.
- **Cấu hình sai** được gom và báo một lượt khi khởi động, nêu tên biến ([config.md](config.md)); sửa theo thông báo, **không hạ `ENVIRONMENT`** để né.
- **Thứ tự khi nâng cấp cả cụm:** `auth`, rồi `fhir-server`, rồi `ws-gateway`. `ws-gateway` không có database nên không có migration.
- **Scale:** stateless, không cần sticky session (token tự chứa); scale theo **số kết nối**. Hạn mức bind theo người và theo IP đếm **theo instance**: cả cụm cho tối đa N x số replica.

## 3. Cấu hình production, triển khai lần đầu và smoke test

### 3.1 Cấu hình bắt buộc và nên đặt

Danh sách đầy đủ ở [config.md](config.md). Ở `staging`/`production` service **từ chối khởi động** nếu: `REDIS_URL` không đặt tường minh, `FHIR_WS_ALLOWED_ORIGINS` có `*`, và (khi bật FHIRcast) `FHIRCAST_PUBLIC_URL` không phải `wss://`, `FHIRCAST_JWKS_URL` không phải `https://`, dùng `FHIRCAST_JWT_HMAC_SECRET`, hoặc tắt `FHIRCAST_REQUIRE_SCOPE`.

| Biến | Giá trị |
|---|---|
| `REDIS_URL` | Cùng Redis với `fhir-server`, đặt tường minh (kênh sự kiện và sổ hiện diện của Subscription) |
| `FHIR_WS_SIGNING_KEY` | **Giống hệt** khóa của `fhir-server` (>= 32 ký tự, không có giá trị mặc định: `openssl rand -base64 48`) |
| `FHIR_WS_ALLOWED_ORIGINS` | Mẫu **host** của `Origin` (ví dụ `app.example.org,*.example.org`), không phải URL; rỗng: chỉ cùng host |
| `FHIR_WS_TRUSTED_PROXIES` | CIDR của reverse proxy/ingress; **bắt buộc đúng** nếu dùng `FHIR_WS_MAX_CONNS_PER_IP`, nếu sai thì mọi client bị coi là một IP và bị chặn nhầm tất cả |
| `FHIR_WS_MAX_CONNECTIONS` | Mặc định 1000 mỗi instance (vượt thì `503`); chốt theo mục 4 |
| `FHIR_WS_MAX_CONNS_PER_IP`, `FHIR_WS_RATE_LIMIT`, `FHIR_WS_RATE_BURST` | Hạn mức mỗi IP (`429`); `FHIR_WS_RATE_LIMIT` không đặt thì staging/production là 10 lần mở/giây |
| `FHIRCAST_*` | Chỉ khi bật Hub FHIRcast, xem mục 8 |

`FHIR_WS_URL` (đặt ở `fhir-server`) là địa chỉ websocket **công khai** của gateway (`wss://ws.example.org/ws/subscriptions-r4`), khác địa chỉ nội bộ giữa các container.

### 3.2 Triển khai lần đầu

Thứ tự: Redis → `auth` → `fhir-server` (`FHIR_WS_MODE=issuer`) → `ws-gateway`. Thử trên máy bằng `docker-compose.ws.yml` (gốc repo):

```bash
FHIR_WS_SIGNING_KEY=$(openssl rand -hex 24) FHIR_WRITE_TOKENS=ops:$(openssl rand -hex 16) \
docker compose -f docker-compose.yml -f docker-compose.ws.yml up --build fhir-server ws-gateway redis postgres
```

Điều kiện bên `fhir-server` để gateway nhận sự kiện: `FHIR_WS_MODE=issuer`, cùng Redis (`BROKER_DRIVER=redis`), `FHIR_WS_URL` = địa chỉ công khai của gateway, `FHIR_WS_SIGNING_KEY` trùng. Các file compose là **mẫu chạy thử** (địa chỉ `localhost`); ở production hãy sao chép rồi sửa.

### 3.3 Smoke test sau khi triển khai

```bash
curl -fsS https://ws.example.org/health/ready      # 200, {"ready":true,"checks":{...}}
curl -fsS https://ws.example.org/health/live       # 200
# nếu bật FHIRcast:
curl -fsS https://ws.example.org/fhircast/STU3/.well-known/fhircast-configuration | jq .fhircastVersion
```

Đạt khi cả hai `health` là 200 và `ready` là `true`. Kiểm đầu-cuối (tạo Subscription kênh websocket, xin token bind, nối socket, nhận sự kiện) do `make test-service APP=ws-gateway` và `make test-e2e` đảm nhiệm; xem [testing.md](testing.md). Trước release (trên máy build): `make ci`, `make test-integration APP=ws-gateway`.

## 4. Định cỡ (số đo thật, `make bench-ws`)

Đo bằng binary thật, miniredis, N socket bind vào 10 Subscription, mỗi vòng mỗi socket nhận một thông báo (Observation ~1 KB):

| N socket | RSS nền | RSS sau khi kết nối | RSS sau 3 vòng nhận tin | Mỗi socket | Fan-out p50 / p99 |
|---|---|---|---|---|---|
| 1.000 | 24 MB | 79 MB | 98 MB | 56 KB (kết nối), ~100 KB (đã nhận tin) | 7 ms / 15 ms |
| 4.000 | 24 MB | 230 MB | 272 MB | 53 KB (kết nối), ~68 KB (đã nhận tin) | 18 ms / 48 ms |

Mở 1.000 socket mất ~0,1 giây (≈13.000 socket/giây). **Quy ước định cỡ: 100 KB mỗi socket + 24 MB nền**, tức mặc định `FHIR_WS_MAX_CONNECTIONS=1000` cần ~125 MB; nên cho giới hạn bộ nhớ khoảng gấp ba (ví dụ request 192 MiB, limit 384 MiB) để dư cho tin lớn hơn. Số đo từ máy dev (macOS arm64), tin nhỏ, client cùng máy: **đo lại trên cấu hình triển khai thật** (`WS_LOAD_CONNS=5000 make bench-ws`) trước khi chốt giới hạn bộ nhớ và `FHIR_WS_MAX_CONNECTIONS`. Tin lớn (nhiều trường, resource lớn) làm tăng bộ nhớ hàng đợi gửi của mỗi socket chậm.

## 5. Metric và theo dõi

Bật `OTEL_METRICS_ENABLED=true` (mặc định thư viện observability tắt metric) và `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`; chu kỳ `OTEL_METRIC_EXPORT_INTERVAL` mặc định 60s. Thư viện chỉ **đẩy OTLP**, không có endpoint scrape; muốn scale theo số kết nối thật phải đưa `ws_gateway.connections` vào hệ thống metric của bạn.

| Instrument | Loại | Ý nghĩa |
|---|---|---|
| `ws_gateway.connections`, `ws_gateway.bindings`, `ws_gateway.subscriptions` | gauge | Kết nối, lượt bind, Subscription đang nghe |
| `ws_gateway.messages.sent` | tích lũy | Tin đã gửi |
| `ws_gateway.connections.slow` | tích lũy | Người nhận chậm bị ngắt (`1013`) |
| `ws_gateway.messages.denied` | tích lũy | Tin bỏ vì thiếu quyền hoặc hết hạn |
| `ws_gateway.events.received`, `ws_gateway.events.malformed` | tích lũy | Sự kiện nhận từ Redis; sự kiện hỏng |
| `ws_gateway.redis.up` | 1/0 | Redis đang kết nối |

Gợi ý cảnh báo: `redis.up == 0`; `connections` tiến gần `FHIR_WS_MAX_CONNECTIONS` (vượt thì `503`); `connections.slow` tăng đều (client chậm hoặc tin quá lớn); `events.malformed > 0` (bên phát gửi sai định dạng). **Chưa có số liệu OTLP riêng cho FHIRcast** (`ws_gateway.*` chỉ tính websocket Subscription). Log là nguồn chính khi điều tra; tên telemetry `trivita-ws-gateway`.

## 6. Xoay khóa

`FHIR_WS_SIGNING_KEY` (>= 32 ký tự) xoay **không gián đoạn** bằng `FHIR_WS_SIGNING_KEY_PREVIOUS` (tối đa 3 khóa cũ, cách nhau bằng dấu phẩy, mỗi khóa >= 32 ký tự):

1. Ở `ws-gateway`: đặt `FHIR_WS_SIGNING_KEY` = khóa mới và `FHIR_WS_SIGNING_KEY_PREVIOUS` = khóa cũ, triển khai. Gateway xác minh được token ký bằng cả hai.
2. Ở `fhir-server` (bên cấp token): đổi sang khóa mới, cuốn chiếu.
3. Sau >= 5 phút (hạn token bind) bỏ `FHIR_WS_SIGNING_KEY_PREVIOUS`.

**Thứ tự quan trọng:** gateway **trước**, bên cấp token **sau**; ngược lại token mới bị gateway từ chối. Khóa cũ không nằm trong `_PREVIOUS` thì socket đang mở vẫn sống nhưng lần bind mới bằng token cũ bị đóng `1008`.

## 7. Hành vi khi sự cố (đã kiểm trên cụm thật)

| Sự cố | Hành vi |
|---|---|
| Redis chết | Mọi instance gateway chuyển NotReady sau ~13 giây (readiness kiểm Redis), **không restart** (liveness độc lập), mất khỏi load balancer nên không nhận kết nối mới; socket đang mở không bị cắt nhưng không nhận thêm sự kiện (sự kiện không bền, mất không bù). Redis lên lại thì instance Ready trong ~3-6 giây |
| Instance gateway bị gỡ hoặc nâng cấp | Socket của nó nhận `1001`; client bind lại bằng token mới (mục 2) |
| `fhir-server` restart hoặc deploy | **Không ảnh hưởng socket**; sự kiện tiếp tục tới sau khi `fhir-server` lên lại |
| Gateway chết đột ngột (kill -9, máy chết) | Mục hiện diện hết hạn sau `FHIR_WS_PRESENCE_TTL` (30s); `fhir-server` ngừng phát cho Subscription đó trong lúc chờ |
| Hết `FHIR_WS_MAX_CONNECTIONS` hoặc quá `FHIR_WS_MAX_CONNS_PER_IP` | `503` / `429`; client thử lại có backoff |

**Chưa kiểm thật:** scale tự động theo tải, thực thi chính sách mạng, định tuyến qua từng loại ingress cụ thể, nhiều node.

## 8. Hub FHIRcast (tùy chọn, `FHIRCAST_ENABLED=true`)

Chạy trong cùng tiến trình; token do `auth` cấp. Giao thức, tiền tố `/fhircast/STU2`, `/fhircast/STU3`, `/hub`, lỗi: [api-contract.md](api-contract.md) mục 4.

**Cấu hình hai phía (phải khớp):**

| `auth` | `ws-gateway` | Ghi chú |
|---|---|---|
| `AUTH_FHIRCAST_HUB_URL=https://<gateway>/fhircast/STU3` | `FHIRCAST_PUBLIC_URL=wss://<gateway>` | Địa chỉ công khai: `hub.url` ở token và `hub.channel.endpoint` ở đăng ký |
| `AUTH_ISSUER` (mặc định `AUTH_BASE_URL`) | `FHIRCAST_JWT_ISSUER` | Phải giống từng ký tự |
| audience của access token (`AUTH_AUDIENCE`, mặc định = issuer) | `FHIRCAST_JWT_AUDIENCE` | |
| `/.well-known/jwks.json` | `FHIRCAST_JWKS_URL` | https ở staging/production; **không** dùng `FHIRCAST_JWT_HMAC_SECRET` ngoài dev |
| `AUTH_REDIS_URL` (bắt buộc ở staging/production) | `REDIS_URL` | **Hai Redis không cần chung**: `auth` chỉ nhớ topic của người dùng, gateway chỉ nhớ đăng ký và ngữ cảnh |

**Kiểm nhanh:**

```bash
# 1. đăng nhập xin scope fhircast/*: phản hồi token phải có hub.url và hub.topic
curl -s -X POST https://<auth>/oauth2/token -d grant_type=client_credentials -d client_id=... -d client_secret=... -d 'scope=fhircast/*' | jq '."hub.url", ."hub.topic"'
# 2. Hub công khai
curl -s https://<gateway>/fhircast/STU3/.well-known/fhircast-configuration
# 3. đăng ký (Bearer là access_token của bước 1)
curl -s -X POST https://<gateway>/fhircast/STU3 -H "Authorization: Bearer $TOKEN" -d hub.channel.type=websocket -d hub.mode=subscribe -d hub.topic=$TOPIC -d hub.events=Patient-open
```

**Sự cố thường gặp:**

| Triệu chứng | Nguyên nhân hay gặp | Cách xử lý |
|---|---|---|
| Token không có `hub.url`/`hub.topic` | Scope đã cấp không có term `fhircast/`; hoặc `AUTH_FHIRCAST_HUB_URL` chưa đặt | Xin scope `fhircast/*`; kiểm cấu hình `auth` |
| `auth` trả `500 server_error` khi xin scope `fhircast/*` | `auth` không lấy được topic (Redis của `auth` lỗi hoặc treo quá 2 giây); token thường vẫn cấp được | Kiểm `AUTH_REDIS_URL`; log `không lấy được topic FHIRcast` |
| Hub trả `401` | Token sai chữ ký/iss/aud/hết hạn; gateway không tải được JWKS lần đầu | Kiểm issuer/audience hai phía và `FHIRCAST_JWKS_URL` truy cập được từ gateway |
| Hub trả `403` | Token đúng nhưng thiếu scope `fhircast/*` hoặc không có claim project | Xin lại scope; `FHIRCAST_REQUIRE_SCOPE=false` chỉ để chuyển tiếp ở local |
| Hai ứng dụng cùng người dùng nhận topic khác nhau | `auth` chạy nhiều instance mà không chung `AUTH_REDIS_URL`; hoặc quá 1 giờ không xin token (topic hết hạn) | Đặt Redis chung (production đã bắt buộc); xin token lại |
| Socket tự đóng sau khoảng `FHIRCAST_LEASE` (1 giờ) | Hết lease: nhận `denied` "lease expired" rồi đóng | Đăng ký lại (`POST hub.url` với `hub.mode=subscribe`) |
| `denied` "invalid endpoint" ngay khi nối | Endpoint không tồn tại, đã hủy hoặc đã hết hạn | Đăng ký lại; endpoint là bí mật dùng một lần cho mỗi đăng ký |
| Socket bị đóng `1008` "too many connections for this endpoint" | Quá `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` (4) socket cùng một endpoint trên một instance | Mỗi ứng dụng nên đăng ký endpoint riêng |
| Socket bị đóng `1013` | Client đọc quá chậm (hàng đợi đầy) hoặc Redis lỗi lúc đăng ký | Client đọc nhanh hơn / thử lại có backoff |

Nhiều replica: ngữ cảnh và đăng ký ở Redis nên mọi gateway thấy như nhau; hạn mức socket mỗi endpoint đếm **theo instance**.

## 9. Reverse proxy và ingress

- Phải hỗ trợ `Upgrade` và `Connection`, và timeout đọc/ghi **dài hơn `FHIR_WS_HEARTBEAT`** (30s). Nhiều proxy mặc định cắt kết nối im lặng sau 60s: đặt tối thiểu 3600s cho đường `/ws/`.
- Phải truyền `X-Forwarded-For` nếu dùng hạn mức theo IP, **và** đặt `FHIR_WS_TRUSTED_PROXIES` đúng CIDR của proxy.
- Với Istio/Envoy: không đặt `timeout` ngắn trên route (mặc định không giới hạn); `idleTimeout` upstream mặc định 1 giờ.
- Mẫu manifest K8s có sẵn ở `deploy/k8s/ws-gateway/` để tham khảo (Deployment, Service, HPA, PDB, ingress-nginx, Istio, NetworkPolicy); dự án triển khai bằng Docker và Docker Compose nên không coi đó là cách triển khai chính.

## 10. Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Từ chối khởi động, in tên biến | Ràng buộc staging/production (mục 3.1) | Đặt đúng biến theo thông báo |
| Client không nhận sự kiện Subscription | `FHIR_WS_SIGNING_KEY` hai bên khác nhau; Redis hai bên khác nhau; `FHIR_WS_URL` ở `fhir-server` sai địa chỉ công khai; `fhir-server` chưa ở `FHIR_WS_MODE=issuer` | So khóa và Redis hai phía; kiểm `FHIR_WS_URL` |
| Client bị đóng `1008` ngay sau khi bind | Token sai, hết hạn hoặc sai phiên bản; vượt `FHIR_WS_MAX_BINDINGS_PER_USER` (20); tin đầu không phải bind trong 10 giây | Lấy token mới (`$get-ws-binding-token`); kiểm khóa và xoay khóa (mục 6) |
| Client bị đóng `1013` | Client quá chậm hoặc Redis lỗi lúc bind | Client thử lại sau có backoff; kiểm Redis |
| `403` khi nối | `Origin` không nằm trong `FHIR_WS_ALLOWED_ORIGINS` | Thêm host của ứng dụng web vào danh sách |
| `429` khi nối | Quá `FHIR_WS_MAX_CONNS_PER_IP` hoặc `FHIR_WS_RATE_LIMIT` | Nếu người dùng thật bị chặn hàng loạt: kiểm `FHIR_WS_TRUSTED_PROXIES` (mục 9) |
| `503` khi nối | Đang tắt (drain) hoặc hết `FHIR_WS_MAX_CONNECTIONS` | Chờ instance mới sẵn sàng; thêm instance hoặc nâng giới hạn (mục 4) |
| Readiness `503` kéo dài | Redis không kết nối được | Kiểm `REDIS_URL`, mạng, và Redis; socket đang mở vẫn sống nhưng không nhận thêm sự kiện |
| Subscription đã xóa nhưng socket vẫn mở | Giới hạn đã biết: bên khớp ngừng phát nên socket im lặng, kết nối mở cho tới khi client đóng hoặc quyền hết hạn | Client tự đóng khi không còn cần |

## 11. Giới hạn đã biết

1. Sự kiện **không bền** (at-most-once): mất khi gateway khởi động lại hoặc Redis đứt; client dựa vào `heartbeat` và `eventsSinceSubscriptionStart`.
2. Quyền chụp lúc xin token: đổi quyền sau đó chỉ có hiệu lực khi access token gốc hết hạn.
3. Hạn mức theo người, theo IP và số socket mỗi endpoint đếm **theo instance**, chưa theo cả cụm.
4. Chưa lọc từng trường theo `hiddenFields` (chưa có ở cả REST).
5. Mỗi sự kiện khớp tốn một lệnh Redis ở bên phát (đọc sổ hiện diện).
6. Chưa có số liệu OTLP cho FHIRcast, chưa áp hạn mức tốc độ theo IP cho các route HTTP của FHIRcast.
7. Chưa kiểm thật: scale tự động, chính sách mạng, ingress cụ thể, nhiều node (mục 7); chưa có CI trong repo.
