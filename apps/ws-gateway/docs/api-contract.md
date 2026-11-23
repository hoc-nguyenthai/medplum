# ws-gateway: hợp đồng API

Cập nhật 2026-11-23. `ws-gateway` giữ kết nối WebSocket cho Subscription R4 và (tùy chọn) Hub FHIRcast. **Chỉ mô tả phần `ws-gateway` đang phục vụ.** Cấu hình ở [config.md](config.md); kiến trúc ở [architecture.md](architecture.md); vận hành ở [../README.md](../README.md).

| Nguồn | Vai trò |
|---|---|
| `swagger.json` / `swagger.yaml` (cùng thư mục) | **Nguồn chuẩn** của route HTTP, tham số, DTO của Hub FHIRcast, probe và hai route nâng cấp WebSocket. Sinh từ annotation ở `internal/presentation/httpapi/swagger.go`: `make swagger APP=ws-gateway` |
| Tệp này | Quy ước chung, bảng route (**sinh từ `swagger.json`**) và phần OpenAPI 2.0 không mô tả được: thông điệp trong kênh WebSocket, mã đóng, đảm bảo giao hàng |

**OpenAPI 2.0 chỉ mô tả được phần HTTP.** Với `GET /ws/subscriptions-r4` và `GET /ws/fhircast/{endpoint}` spec chỉ ghi route nâng cấp (101) và các lỗi trước khi nâng cấp; giao thức thông điệp (mục 3 và mục 4 bên dưới) và mã đóng WebSocket (1008, 1013...) viết tay ở đây. Service **không phục vụ** `/swagger` (không kéo thêm phụ thuộc vào gateway); đọc spec ở file.

**Bốn test giữ hợp đồng không lệch** (đỏ khi lệch):

| Test | Bắt lỗi gì |
|---|---|
| `internal/presentation/httpapi` `TestRoutesMatchSwaggerContract` | Route phục vụ thật (FHIRcast bật) khác `swagger.json`, theo cả hai chiều |
| `docs` `TestAPIContractIsUpToDate` | Bảng route dưới đây khác `swagger.json` (`UPDATE_DOCS=1 go test ./apps/ws-gateway/docs -run TestAPIContractIsUpToDate`) |
| `docs` `TestSwaggerDTOsAreComplete` | DTO thiếu mô tả trường, `required`, ví dụ ở body yêu cầu; route thiếu tóm tắt/mô tả/schema phản hồi/mô tả tham số; tag thiếu mô tả |
| `docs` `TestSwaggerIsStructurallyValid` | Tham số path lệch mẫu hoặc thiếu `required`, `$ref` hỏng, tham số query/path có `example`, `produces` không khớp Content-Type thật |

Các test kiểm độ đầy đủ và khớp route, **không** kiểm hành vi của Hub (do `pkg/fhircast`, `httpapi/fhircast_test.go` và e2e kiểm). Nội dung kênh WebSocket không có test máy so với tài liệu.

## 1. Địa chỉ

Cổng mặc định `:8007`. Cần Redis dùng chung với `fhir-server` và khóa `FHIR_WS_SIGNING_KEY` giống bên cấp token.

## 2. Bảng route

Mỗi nhóm là một `tag` của swagger. `{v}` ở mô tả là `/fhircast/STU2`, `/fhircast/STU3` hoặc `/hub` (bí danh của STU3); mỗi route của Hub xuất hiện ba lần, một lần cho mỗi tiền tố (`POST {v}/` có thêm bí danh dấu `/` cuối và `GET /ws/fhircast` không có endpoint, vào cùng handler, không liệt kê riêng).

Điều kiện có route: nhóm `fhircast` và `GET /ws/fhircast/{endpoint}` chỉ có khi `FHIRCAST_ENABLED=true` (tắt thì `404`); phần còn lại luôn có.

**Xác thực:** `GET /ws/*` không có header Bearer (Subscription: token bind trong khung đầu tiên; FHIRcast: `endpoint` là bí mật 122 bit do `POST {v}/` cấp); Hub FHIRcast dùng Bearer JWT của `auth` (JWKS, scope `fhircast/*`), trừ `.well-known`.

<!-- BEGIN GENERATED ROUTES (từ swagger.json; chạy UPDATE_DOCS=1 go test để sinh lại, đừng sửa tay) -->

### Nhóm `fhircast` (12 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `POST /fhircast/STU2` | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh | Bearer | body `httpapi.FhircastHubRequest` | 202 `httpapi.FhircastHubResponse` | 400, 401, 403, 413, 500 |
| `GET /fhircast/STU2/.well-known/fhircast-configuration` | Cấu hình Hub FHIRcast | công khai | - | 200 `httpapi.FhircastWellKnown` | - |
| `GET /fhircast/STU2/{topic}` | Ngữ cảnh hiện tại (STU2) | Bearer | - | 200 `[]httpapi.FhircastContextItem` | 401, 403, 500 |
| `POST /fhircast/STU2/{topic}` | Đổi ngữ cảnh của một topic | Bearer | body `httpapi.FhircastEventBody` | 202 `httpapi.FhircastChangeResponse` | 400, 401, 403, 409, 413, 500 |
| `POST /fhircast/STU3` | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh | Bearer | body `httpapi.FhircastHubRequest` | 202 `httpapi.FhircastHubResponse` | 400, 401, 403, 413, 500 |
| `GET /fhircast/STU3/.well-known/fhircast-configuration` | Cấu hình Hub FHIRcast | công khai | - | 200 `httpapi.FhircastWellKnown` | - |
| `GET /fhircast/STU3/{topic}` | Ngữ cảnh hiện tại (STU3) | Bearer | - | 200 `httpapi.FhircastContextSTU3` | 401, 403, 500 |
| `POST /fhircast/STU3/{topic}` | Đổi ngữ cảnh của một topic | Bearer | body `httpapi.FhircastEventBody` | 202 `httpapi.FhircastChangeResponse` | 400, 401, 403, 409, 413, 500 |
| `POST /hub` | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh | Bearer | body `httpapi.FhircastHubRequest` | 202 `httpapi.FhircastHubResponse` | 400, 401, 403, 413, 500 |
| `GET /hub/.well-known/fhircast-configuration` | Cấu hình Hub FHIRcast | công khai | - | 200 `httpapi.FhircastWellKnown` | - |
| `GET /hub/{topic}` | Ngữ cảnh hiện tại (STU3) | Bearer | - | 200 `httpapi.FhircastContextSTU3` | 401, 403, 500 |
| `POST /hub/{topic}` | Đổi ngữ cảnh của một topic | Bearer | body `httpapi.FhircastEventBody` | 202 `httpapi.FhircastChangeResponse` | 400, 401, 403, 409, 413, 500 |

### Nhóm `health` (4 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /health/live` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /health/ready` | Readiness probe | công khai | - | 200 `httpapi.ReadyResponse` | 503 |
| `GET /health/startup` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /healthz` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |

### Nhóm `websocket` (2 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /ws/fhircast/{endpoint}` | WebSocket FHIRcast | công khai | - |  | 403, 429, 503 |
| `GET /ws/subscriptions-r4` | WebSocket Subscription R4 | công khai | - |  | 403, 429, 503 |

<!-- END GENERATED ROUTES -->

**Định dạng lỗi:** Hub trả `OperationOutcome` (`application/fhir+json`, thông điệp ở `issue[0].details.text`; `401` kèm `WWW-Authenticate: Bearer realm="fhircast"`); lỗi ngoài Hub (hạn mức, route lạ) trả `{"error": "..."}`; lỗi trước khi nâng cấp WebSocket của `wsgroup` (`403`, `429` theo IP, `503`) là văn bản thường.

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

- Không có AsyncAPI cho nội dung kênh WebSocket (OpenAPI 2.0 chỉ mô tả route nâng cấp); mục 3 và mục 4 cùng bảng mã đóng là nguồn của hợp đồng này, chưa có test máy so với hành vi thật ngoài e2e.
- Số liệu OTLP của kênh FHIRcast; hạn mức theo IP cho route HTTP của FHIRcast; giới hạn socket mỗi endpoint tính theo cả cụm (hiện theo instance).
