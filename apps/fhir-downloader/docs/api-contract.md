# fhir-downloader: hợp đồng API

`fhir-downloader` **không có route nghiệp vụ**: không có swagger, không có bảng route sinh tự động và không có `cases/` (mục 7.10 của [service-principles.md](../../../docs/service-principles.md): không áp dụng). Cổng HTTP (`HTTP_ADDR`, mặc định `:8009`) chỉ phục vụ 4 probe cho orchestrator và `HEALTHCHECK` của Docker. Hợp đồng thật của service là các **hợp đồng dây** (mục 3), được ghim bằng test.

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

`fhir-downloader healthcheck` (điểm vào của image) gọi `GET /healthz` ở `HTTP_ADDR` và thoát `0` khi `200`, khác `0` khi lỗi; image distroless không có `curl`. Lệnh lạ: thoát khác `0` với `cách dùng: fhir-downloader [healthcheck]`.

## 3. Hợp đồng dây

Giao diện thật của service (không phải HTTP): các bên chung **schema**, **kho Binary** và **sự kiện trên Redis**, không import mã của nhau (C-1, C-6).

| Hợp đồng | Nội dung | Ghim bằng |
|---|---|---|
| Sự kiện nhận | topic `fhir.resource.changed` (`fhirsub.ChangeEvent`: `interaction`, `resourceType`, `id`, `resource`) từ `fhir-server` qua Redis Streams, consumer group `BROKER_GROUP` (mặc định `fhir-downloader`, **khác** `trivita` của `fhir-server`, cấu hình từ chối `trivita`); giao ít nhất một lần, lỗi tạm không ACK rồi thử lại, quá 5 lần sang `trivita:events:fhir.resource.changed:dead` | `apps/fhir-downloader/internal/bootstrap/bootstrap_test.go`: `TestEachConsumerGroupReceivesEveryEvent`, `TestDownloadsAttachmentAnnouncedByTheAPI`; `apps/fhir-downloader/tests/e2e/downloader_test.go` |
| Bỏ qua sự kiện | `delete`; loại `Binary`, `AsyncJob`, `AuditEvent`, `Subscription`; URL không phải `http`/`https` tuyệt đối; URL của chính hệ thống (`FHIR_BASE_URL` và `/storage`); khớp `FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES`; không khớp `FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES` (khi đặt) | `apps/fhir-downloader/internal/bootstrap/bootstrap_test.go`: `TestSkipsOwnAndIgnoredURLs`; `pkg/fhirdownload/download_test.go`: `TestSkipsUrlsThatAreNotExternalOrNotAllowed`, `TestIgnoresDeletesAndUnrelatedTypes` |
| Binary do service tạo | `meta.project` của resource nguồn, `securityContext` = `<Loại>/<id>` của resource nguồn, `contentType` lấy từ phản hồi (rồi `attachment.contentType`, rồi `application/octet-stream`); nội dung ghi vào kho `FHIR_BINARY_STORAGE` (phải trùng `fhir-server`) | `apps/fhir-downloader/internal/bootstrap/bootstrap_test.go`: `TestDownloadsAttachmentAnnouncedByTheAPI`; `apps/fhir-downloader/tests/integration/integration_test.go` |
| Cập nhật resource nguồn | `url` của mọi attachment trùng đổi thành `Binary/<id>` (điền `contentType` nếu trống) bằng `If-Match` phiên bản vừa đọc; là một phiên bản mới, phát sự kiện `update` lên Redis qua kho phát sự kiện | `pkg/fhirdownload/download_test.go`: `TestVersionConflictCleansUpAndRetrySucceeds`, `TestRedeliveredEventDoesNotDownloadTwice` |
| Gọi ra ngoài | `GET` qua `pkg/safehttp` (chặn dải nội bộ gồm `0.0.0.0/8`, chống DNS rebinding), hạn `FHIR_AUTO_DOWNLOAD_TIMEOUT`, trần `FHIR_AUTO_DOWNLOAD_MAX_BYTES`; log chỉ ghi host | `pkg/fhirdownload/download_test.go`: `TestBlocksPrivateAddressesByDefault`; `apps/fhir-downloader/tests/e2e/downloader_test.go`: `TestBlocksInternalAddressesByDefaultInTheImage` |
| Cấu hình dùng chung | `DATABASE_*`, `REDIS_URL`, `FHIR_BASE_URL`, `FHIR_BINARY_STORAGE`, `FHIR_S3_*`, `FHIR_AUTO_DOWNLOAD_*` giữ nguyên tên của `fhir-server` | [config.md](config.md) (sinh từ mã), `apps/fhir-downloader/internal/config/config_test.go` |

## 4. Giữ cho hợp đồng khỏi lệch

- Hình dạng thân và header của probe được ghim bằng `TestProbeContractIsPinned` (`apps/fhir-downloader/internal/presentation/httpapi/httpapi_test.go`); đổi thân hoặc header mà không sửa tài liệu này thì test đỏ.
- Ba service nền (`fhir-downloader`, `fhir-notifier`, `fhir-worker`) có cùng bộ probe; `fhir-worker` chỉ có khóa `database` trong `checks` (không nghe broker).
