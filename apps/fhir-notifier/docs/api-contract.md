# fhir-notifier: hợp đồng API

`fhir-notifier` **không có route nghiệp vụ**: không có swagger, không có bảng route sinh tự động và không có `cases/` (mục 7.10 của [service-principles.md](../../../docs/service-principles.md): không áp dụng). Cổng HTTP (`HTTP_ADDR`, mặc định `:8010`) chỉ phục vụ 4 probe cho orchestrator và `HEALTHCHECK` của Docker. Hợp đồng thật của service là các **hợp đồng dây** (mục 3), được ghim bằng test.

## 1. Quy ước chung

| Mục | Giá trị |
|---|---|
| Xác thực | **Không có.** Probe không cần token và không lộ dữ liệu; đặt sau mạng nội bộ của orchestrator, không công bố ra Internet. |
| Giao thức | HTTP/1.1; chỉ `GET` (và `HEAD` đi theo `GET`) |
| Phản hồi thành công | `Content-Type: application/json`, thân JSON một dòng kết thúc bằng `\n` |
| Header bảo mật (mọi phản hồi, kể cả `404`) | `Cache-Control: no-store, no-cache, must-revalidate`; `Pragma: no-cache`; `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`; `Referrer-Policy: no-referrer`; `X-Content-Type-Options: nosniff`; `X-Frame-Options: DENY`; `X-Xss-Protection: 1; mode=block`; `Permissions-Policy` tắt các tính năng trình duyệt |
| Hạn mức, CORS | Không có (không áp dụng cho probe) |
| Lỗi | mọi đường dẫn hoặc phương thức khác: `404` `{"error":"Not Found"}` (kể cả `POST /healthz`, `OPTIONS`, `/fhir/R4/...`: service này không có route nào khác); panic được chặn thành `500` `{"error":"Internal Server Error"}`, chi tiết chỉ vào log |

## 2. Route

| Route | Phụ thuộc ngoài? | Dùng cho | Phản hồi |
|---|---|---|---|
| `GET /healthz` | không | `HEALTHCHECK` của Docker (`/app healthcheck`) | `200` `{"status":"ok"}` |
| `GET /health/live` | không | liveness (K8s không được restart pod vì lỗi của DB hoặc Redis) | `200` `{"status":"ok"}` |
| `GET /health/startup` | không | startup probe | `200` `{"status":"ok"}` |
| `GET /health/ready` | **có: database và Redis** (trong `READINESS_TIMEOUT`, mặc định 2 giây) | readiness: có nhận được việc không | `200` hoặc `503` với `{"ready":bool,"checks":{...}}` |

Luôn `200` cho ba probe đầu khi tiến trình còn sống, kể cả khi đang tắt, DB hoặc Redis hỏng (liveness không phụ thuộc ngoài).

### 2.1 `GET /health/ready`

| Tình huống | Mã | Thân |
|---|---|---|
| Database và Redis đều sống, không đang tắt | `200` | `{"ready":true,"checks":{"broker":true,"database":true}}` |
| Database không phản hồi trong `READINESS_TIMEOUT` hoặc lỗi | `503` | `{"ready":false,"checks":{"broker":true,"database":false}}` |
| Redis không phản hồi hoặc lỗi | `503` | `{"ready":false,"checks":{"broker":false,"database":true}}` |
| Đang tắt (sau `SIGTERM`) | `503` | `{"ready":false,"checks":{"draining":true}}` (không kiểm database và Redis) |

`checks` có đúng hai khóa `broker` và `database` (theo thứ tự chữ cái); đang tắt thì chỉ có `draining`. **Tác dụng phụ:** không có. **Luật:** xem [business/](business/) hoặc `architecture.md` (probe và tắt êm).

### 2.2 Dòng lệnh `healthcheck`

`fhir-notifier healthcheck` (điểm vào của image) gọi `GET /healthz` ở `HTTP_ADDR` và thoát `0` khi `200`, khác `0` khi lỗi; image distroless không có `curl`. Lệnh lạ: thoát khác `0` với `cách dùng: fhir-notifier [healthcheck]`.

## 3. Hợp đồng dây

Giao diện thật của service (không phải HTTP): các bên chung **schema**, **sự kiện trên Redis**, **webhook gửi ra** và **kênh websocket**, không import mã của nhau (C-1, C-6).

### 3.1 Sự kiện nhận

topic `fhir.resource.changed` (`fhirsub.ChangeEvent`: `interaction`, `resourceType`, `id`, `resource`) từ `fhir-server` qua Redis Streams, consumer group `BROKER_GROUP` (mặc định `fhir-notifier`, **khác** `trivita` của `fhir-server`, cấu hình từ chối `trivita`). Handler giao **đồng bộ**: chỉ ACK khi mọi lần giao của sự kiện đã xong (giao được hoặc bỏ cuộc); tắt giữa chừng thì message ở lại PEL và replica khác nhận lại sau `BROKER_CLAIM_IDLE` (mặc định tối thiểu 1 phút), nên **giao ít nhất một lần** (bên nhận có thể thấy trùng). Ghim: `apps/fhir-notifier/internal/bootstrap/bootstrap_test.go`: `TestEachConsumerGroupReceivesEveryEvent`, `TestAnotherReplicaDeliversWhatTheStoppedOneLeftUnfinished`.

### 3.2 Webhook rest-hook gửi ra (cho bên nhận của khách)

| Mục | Giá trị |
|---|---|
| Yêu cầu | `POST` tới `Subscription.channel.endpoint` (`http` hoặc `https`; địa chỉ nội bộ bị chặn qua `pkg/safehttp`, chỉ dev mới `FHIR_WEBHOOK_ALLOW_PRIVATE=true`); hạn `FHIR_WEBHOOK_TIMEOUT` mỗi lần |
| Thân | JSON của resource sau khi ghi (`create`, `update`); **`{}`** khi `delete` |
| Header cố định | `Content-Type: application/fhir+json`; `X-Medplum-Subscription: <id Subscription>`; `X-Medplum-Interaction: create`, `update` hoặc `delete`; khi `delete` thêm `X-Medplum-Deleted-Resource: <Loại>/<id>` |
| Header riêng | các dòng `Tên: giá trị` trong `Subscription.channel.header` (bỏ `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`) |
| Chữ ký | khi Subscription có `subscription-secret`: `X-Signature` = hex của HMAC-SHA256(secret, thân) |
| Thành công | mọi mã `2xx` |
| Thử lại tại chỗ | lỗi mạng, `408`, `429` và `5xx`: chờ 1 giây nhân đôi mỗi lần (trần 1 phút), tối đa `FHIR_WEBHOOK_MAX_ATTEMPTS` lần (mặc định 5) |
| Bỏ cuộc | các `4xx` khác, địa chỉ bị chặn, hoặc hết số lần thử: ghi log `fhirsub: giao thất bại, bỏ cuộc`, tính vào ngưỡng tự tắt, **không** lưu để giao lại sau |
| Tự tắt | đủ `FHIR_SUBSCRIPTION_AUTO_DISABLE` thì `Subscription.status=off` và `error` = `Automatically disabled after N consecutive failed events in the last S seconds` (ghi bằng `If-Match`) |
| Phạm vi | Subscription chỉ nhận sự kiện cùng `meta.project`; `interaction` lọc theo extension `subscription-supported-interaction` |

Ghim: `pkg/fhirsub/fhirsub_test.go`: `TestDeliveryForCreateUpdateDelete` (header, chữ ký, thân `{}` khi xóa), `TestRetries`, `TestSSRFGuardBlocksInternalTargets`, `TestSubscriptionOnlySeesOwnProject`; `pkg/fhirsub/durable_test.go`; `apps/fhir-notifier/internal/bootstrap/bootstrap_test.go`: `TestDeliversWebhookForMatchingSubscriptionAnnouncedByTheAPI`, `TestRetriesInPlaceOnTemporaryFailureThenStops`, `TestAutoDisablesSubscriptionAfterRepeatedFailuresSharedOnRedis`.

### 3.3 Websocket (khi `FHIR_WS_MODE=issuer`)

Khớp một lần cho cả cụm rồi, **chỉ khi** sổ hiện diện `fhirws.bound.<id Subscription>` còn member (có `ws-gateway` đang giữ socket bind), phát `{"sub":"<id>","event":<ChangeEvent>}` lên kênh Redis `fhirws.sub.<id>`. Không bền (at-most-once): không có gateway nghe thì không phát (bộ đếm `Unheard`). Token bind do `fhir-server` cấp; service này không có khóa ký token. Ghim: `apps/fhir-notifier/internal/bootstrap/bootstrap_test.go`: `TestWebsocketIssuerPublishesToChannelOnlyWhenAGatewayListens`, `TestWebsocketSubscriptionsAreIgnoredWhenModeIsOff`; `pkg/fhirsub/ws_remote_test.go`.

### 3.4 Cấu hình dùng chung

`DATABASE_*`, `REDIS_URL`, `FHIR_WEBHOOK_*`, `FHIR_SUBSCRIPTION_AUTO_DISABLE`, `FHIR_WS_MODE` giữ nguyên tên của `fhir-server`: [config.md](config.md) (sinh từ mã), `apps/fhir-notifier/internal/config/config_test.go`: `TestSharedEnvFileWithFhirServerIsAccepted`.

## 4. Giữ cho hợp đồng khỏi lệch

- Hình dạng thân và header của probe được ghim bằng `TestProbeContractIsPinned` (`apps/fhir-notifier/internal/presentation/httpapi/httpapi_test.go`); đổi thân hoặc header mà không sửa tài liệu này thì test đỏ.
- Ba service nền (`fhir-downloader`, `fhir-notifier`, `fhir-worker`) có cùng bộ probe; `fhir-worker` chỉ có khóa `database` trong `checks` (không nghe broker).
