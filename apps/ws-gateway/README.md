# ws-gateway

Giữ các kết nối WebSocket của Subscription (`/ws/subscriptions-r4`) và đẩy thông báo tới đúng người nhận. Tách khỏi `fhir-server` vì
kết nối sống lâu và scale theo số kết nối, còn `fhir-server` scale theo yêu cầu; deploy `fhir-server` không còn ngắt websocket.
Kế hoạch và quyết định: [docs/plans/modules/split-scim-ws-services.md](../../docs/plans/modules/split-scim-ws-services.md) (phương án B).

**Không có cơ sở dữ liệu, không có nguồn Subscription, không có token dịch vụ.** Gateway chỉ cần Redis và khóa ký token.

```
 client ──$get-ws-binding-token──> fhir-server  (kiểm Subscription đọc được/active/websocket, chụp quyền người xin, ký token)
 client ──ws /ws/subscriptions-r4, bind-with-token──> ws-gateway  (xác minh token, lọc theo quyền từng người nhận)

 fhir-server: ghi tài nguyên -> khớp tiêu chí -> sổ hiện diện có gateway nghe? -> PublishChannel(fhirws.sub.<id>) ──Redis──> ws-gateway
```

- `fhir-server` khớp tiêu chí **một lần cho cả cụm** và chỉ phát cho Subscription đang có socket (`fhirsub.Manager.AttachRemote`).
- Gateway chỉ nghe kênh của những Subscription đang có kết nối bind (`fhirsub.Feed`), ghi sổ hiện diện có TTL để bên phát biết.
  Nó không bao giờ thấy sự kiện của Subscription không có người nghe.
- Token chứa ảnh chụp quyền (scope SMART, AccessPolicy, hạn access token) của người xin: gateway kiểm lại **từng sự kiện, từng
  kết nối**; hết hạn access token thì binding thôi nhận.

## Chạy

```
make run-ws-gateway        # :8007, đọc apps/ws-gateway/.env nếu có
docker compose up --build ws-gateway redis   # xem docker-compose.yml
```

Cần: Redis dùng chung với `fhir-server`, và `FHIR_WS_SIGNING_KEY` **giống hệt** bên cấp token. `fhir-server` phải chạy ở chế độ
`FHIR_WS_MODE=issuer` (cùng Redis, `BROKER_DRIVER=redis`, `FHIR_WS_URL` = địa chỉ websocket công khai của gateway). Chạy cả bộ bằng
`docker-compose.ws.yml` (xem [docker-compose.ws.yml](../../docker-compose.ws.yml)):

```
FHIR_WS_SIGNING_KEY=$(openssl rand -hex 24) FHIR_WRITE_TOKENS=ops:$(openssl rand -hex 16) \
docker compose -f docker-compose.yml -f docker-compose.ws.yml up --build fhir-server ws-gateway redis postgres
```

## Giao thức

Giống Medplum và `fhir-server` (xem `pkg/fhirsub/ws.go`):

| Thông điệp client gửi | Hiệu quả |
|---|---|
| `{"type":"bind-with-token","payload":{"token":"..."}}` | bind một Subscription (nhiều Subscription trên một kết nối, tối đa 50); trả `handshake` |
| `{"type":"unbind-from-token","payload":{"token":"..."}}` | gỡ bind (token hết hạn vẫn gỡ được); kết nối giữ nguyên |
| `{"type":"ping"}` | `{"type":"pong"}` |

Server gửi Bundle `history` chứa `SubscriptionStatus`: `handshake`, `heartbeat` (định kỳ) và `event-notification` (kèm tài nguyên).

Mã đóng: `1008` token sai/hết hạn/sai phiên bản, vượt hạn mức bind (`User has exceeded allotted maximum number of concurrent WebSocket
subscriptions: <n>`), tin đầu không phải bind (sau `BindTimeout` 10s); `1013` tạm thời không nhận sự kiện được (Redis lỗi lúc bind)
hoặc người nhận quá chậm: **thử lại sau**; `1003` khung không phải văn bản hoặc JSON hỏng; `1009` khung quá lớn (64 KiB); `1001`
gateway đang tắt. HTTP: `503` đang tắt hoặc hết `FHIR_WS_MAX_CONNECTIONS`; `429` quá `FHIR_WS_MAX_CONNS_PER_IP` hoặc
`FHIR_WS_RATE_LIMIT`; `403` `Origin` không được phép.

## Cấu hình

Biến môi trường (hoặc `apps/ws-gateway/.env`). Mẫu: [.env.example](.env.example). Tên giữ tiền tố `FHIR_WS_` như `fhir-server`.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `HTTP_ADDR` | `:8007` | địa chỉ nghe |
| `ENVIRONMENT` | `local` | `local` \| `development` \| `staging` \| `production` (staging/production siết các điều kiện bên dưới) |
| `REDIS_URL` | `redis://localhost:6379/0` | **bắt buộc đặt tường minh ở staging/production**; cùng Redis với `fhir-server` |
| `FHIR_WS_SIGNING_KEY` | (không có) | **bắt buộc**, >= 32 ký tự, trùng bên cấp token; không có giá trị ngẫu nhiên thay thế |
| `FHIR_WS_SIGNING_KEY_PREVIOUS` | (rỗng) | khóa cũ chỉ để xác minh khi xoay khóa (tối đa 3, cách nhau bằng dấu phẩy) |
| `FHIR_WS_MAX_CONNECTIONS` | `1000` | số kết nối tối đa mỗi instance (vượt thì 503) |
| `FHIR_WS_MAX_BINDINGS_PER_USER` | `20` | lượt bind đồng thời tối đa của một người trong một project (mỗi instance) |
| `FHIR_WS_MAX_CONNS_PER_IP` | `0` (tắt) | kết nối tối đa từ một IP (429); **đặt cùng `FHIR_WS_TRUSTED_PROXIES` khi chạy sau proxy** |
| `FHIR_WS_TRUSTED_PROXIES` | (rỗng) | CIDR của proxy được tin khi đọc `X-Forwarded-For` (đếm theo IP và hạn mức); rỗng: dùng địa chỉ kết nối |
| `FHIR_WS_RATE_LIMIT` / `FHIR_WS_RATE_BURST` | prod `10` / local `0`; burst gấp đôi | số lần mở websocket mỗi giây mỗi IP |
| `FHIR_WS_ALLOWED_ORIGINS` | (rỗng) | **mẫu host** của `Origin` (ví dụ `app.example.org`, `*.example.org`), không phải URL; rỗng: chỉ cùng host; `*` bị cấm ở staging/production |
| `FHIR_WS_HEARTBEAT` | `30s` | chu kỳ heartbeat và ping |
| `FHIR_WS_CLOSE_TIMEOUT` | `5s` | thời gian chờ client đáp khung đóng khi tắt (không được lớn hơn `SHUTDOWN_TIMEOUT`) |
| `FHIR_WS_PRESENCE_TTL` | `30s` | hạn mục hiện diện nếu gateway chết (gia hạn mỗi TTL/3); bên phát ngừng gửi sau thời gian này |
| `FHIR_WS_INSTANCE_ID` | ngẫu nhiên | định danh trong sổ hiện diện |
| `SHUTDOWN_DRAIN_DELAY` / `SHUTDOWN_TIMEOUT` | `5s` / `25s` | tắt êm: readiness 503, chờ drain, đóng 1001 mọi socket; tổng nhỏ hơn `terminationGracePeriodSeconds` |
| `READINESS_TIMEOUT` | `2s` | hạn kiểm Redis cho readiness |

## Vận hành

- **Probe**: `/health/live`, `/health/startup`, `/health/ready` (Redis + đang nhận traffic; 503 khi đang tắt hoặc Redis lỗi), `/healthz`
  (HEALTHCHECK của Docker). Redis chưa lên lúc khởi động **không** làm tiến trình thoát: readiness 503 cho tới khi kết nối được.
- **Scale**: stateless, không cần sticky session (token tự chứa); scale theo số kết nối (HPA theo `FHIR_WS_MAX_CONNECTIONS`).
  Hạn mức bind theo người và theo IP đếm **theo instance**: cả cụm cho tối đa N x số replica.
- **Tắt êm**: SIGTERM -> readiness 503 -> chờ `SHUTDOWN_DRAIN_DELAY` -> đóng 1001 mọi socket (chờ tối đa `FHIR_WS_CLOSE_TIMEOUT`) ->
  gỡ sổ hiện diện (bên phát thôi gửi ngay) -> đóng Redis. Client im lặng có thể giữ thêm tối đa khoảng 5 giây ở nền (giới hạn của thư viện).
- **Xoay khóa**: đặt khóa mới ở gateway (khóa cũ vào `FHIR_WS_SIGNING_KEY_PREVIOUS`) **trước**, rồi mới đổi bên cấp token, sau >= 5 phút
  bỏ `_PREVIOUS` (xem [docs/runbook.md](docs/runbook.md) mục 6).
- **Ingress**: cần hỗ trợ `Upgrade`/`Connection`, timeout đọc/ghi dài hơn `FHIR_WS_HEARTBEAT`, và truyền `X-Forwarded-For` nếu dùng giới hạn theo IP.

## Metric và triển khai K8s

- Metric OTLP `ws_gateway.*` (kết nối, bind, Subscription đang nghe, tin gửi, kết nối chậm, tin bị từ chối, sự kiện nhận, Redis lên/xuống): xem [docs/runbook.md mục 5](docs/runbook.md). Bật bằng `OTEL_METRICS_ENABLED=true`.
- Manifest K8s ví dụ (Deployment, Service, HPA, PDB, ConfigMap, ingress-nginx, Istio, NetworkPolicy): [deploy/k8s/ws-gateway](../../deploy/k8s/ws-gateway). Định cỡ và hành vi khi sự cố: [docs/runbook.md](docs/runbook.md) mục 4 và 7. Đo tải: `WS_LOAD_CONNS=4000 make bench-ws`.

## FHIRcast (tùy chọn, `FHIRCAST_ENABLED=true`)

Hub FHIRcast chạy cùng gateway: `GET {v}/.well-known/fhircast-configuration` (công khai), `POST {v}/` (đăng ký, hủy hoặc đổi ngữ cảnh), `POST {v}/{topic}`, `GET {v}/{topic}` với `{v}` là `/fhircast/STU2`, `/fhircast/STU3` hoặc `/hub` (STU3).

- **Xác thực:** Bearer JWT của auth, kiểm bằng JWKS (`FHIRCAST_JWKS_URL`; HMAC chỉ cho dev). Cần claim project và scope `fhircast/*`; không cần quyền `fhir.*` vì Hub không đụng tới dữ liệu FHIR. Thiếu token hoặc token sai là 401, thiếu project hoặc scope là 403. JWKS được cache nên đã tải được khóa thì gateway vẫn xác minh khi auth tạm tắt.
- **Trạng thái:** Redis dùng chung (đăng ký, ngữ cảnh hiện tại, ngữ cảnh đã lưu theo báo cáo, kênh pub/sub), mọi khóa có hạn, project nằm trong khóa nên hai project không thấy nhau. Nhiều gateway chung Redis thì thấy cùng ngữ cảnh.
- **Không có callback:** chỉ kênh `websocket`; `webhook`, `rest-hook` hay `hub.callback` không bao giờ gọi ra ngoài (`pkg/fhircast` cấm import `net`, `net/http`).
- **Khác Medplum:** sự kiện phải thuộc danh sách 14 sự kiện; `POST /{topic}` từ chối topic đường dẫn khác `event["hub.topic"]` (đặt `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` để như Medplum); đòi scope `fhircast/*`; hủy đăng ký chỉ bởi đúng người tạo; ngữ cảnh có hạn và có trần kích thước.
- **Kênh WebSocket `/ws/fhircast/{endpoint}`:** endpoint là bí mật 122 bit do `POST {v}/` cấp (không cần Bearer khi nối), một endpoint nối được tối đa `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` socket mỗi instance. Khung đầu tiên luôn là xác nhận (STU3 bốn trường, STU2 thêm bốn trường rỗng); sau đó là sự kiện đã đăng ký (`heartbeat` và `syncerror` luôn tới), `heartbeat` cục bộ mỗi `FHIRCAST_HEARTBEAT`. Hết lease thì nhận `denied` "lease expired" rồi bị đóng (đăng ký lại để tiếp tục); hủy đăng ký thì nhận `denied` rồi đóng; endpoint sai thì nhận `denied` "invalid endpoint". Client chậm bị đóng 1013. Origin, giới hạn theo IP và hạn mức tốc độ dùng chung với websocket Subscription.
- **Khám phá Hub từ token:** khi `auth` bật `AUTH_FHIRCAST_HUB_URL` (trỏ tới `<gateway>/fhircast/STU3`), token có scope `fhircast/*` kèm `hub.url` và `hub.topic`. Topic ổn định theo chủ thể token (user hoặc client) chừng nào còn xin token ít nhất mỗi giờ, nên mọi ứng dụng của một người dùng (ví dụ trình xem ảnh và hồ sơ bệnh án) nhận **cùng topic** rồi đăng ký và đổi ngữ cảnh trên Hub này bằng chính access token đó. Gateway xác minh token bằng JWKS của `auth` (`FHIRCAST_JWKS_URL`, issuer và audience khớp `auth`).
- **Chưa làm:** số liệu OTLP của kênh FHIRcast; hạn mức tốc độ theo IP chưa áp cho các route HTTP của FHIRcast; giới hạn socket mỗi endpoint đếm theo instance, chưa theo cả cụm.

## Giới hạn đã biết

- Sự kiện **không bền** (at-most-once): mất khi gateway khởi động lại hoặc Redis đứt; client dựa vào `heartbeat` và `eventsSinceSubscriptionStart`.
- Subscription bị xóa hoặc tắt: bên khớp ngừng phát nên socket im lặng, nhưng kết nối vẫn mở cho tới khi client đóng hoặc quyền hết hạn.
- Quyền chụp lúc xin token: đổi quyền sau đó chỉ có hiệu lực khi access token gốc hết hạn.
- Chưa lọc từng trường theo `hiddenFields` (chưa có ở cả REST; `auth` đã dịch rule đó thành `deny` cả loại).
- Mỗi sự kiện khớp tốn một lệnh Redis ở bên phát (đọc sổ hiện diện).

## Kiểm thử

Bốn tầng, chi tiết ở [docs/testing.md](docs/testing.md): đơn vị cạnh mã (`make test-unit`, không cần Docker), tích hợp với Redis thật
(`tests/integration`, `make test-integration`), e2e bằng **image dựng từ Dockerfile** cùng Redis thật (`tests/e2e`, `make test-service`), và đo tải
(`tests/load`, `make test-load`). Dùng testcontainers (`pkg/testkit`); không có Docker thì bỏ qua có thông báo, CI đặt `TRIVITA_REQUIRE_DOCKER=1` để coi là lỗi.
`make test-app` chạy mọi tầng. Test nhiều service (`auth` + `fhir-server` + `ws-gateway`) ở gốc repo: `make test-e2e`.

## Tài liệu

[docs/README.md](docs/README.md): kiến trúc, cấu hình (sinh từ mã), test. Lệnh của service này: `make help` trong thư mục này.
