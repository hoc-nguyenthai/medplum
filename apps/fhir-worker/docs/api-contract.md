# fhir-worker: hợp đồng API

`fhir-worker` **không có route nghiệp vụ**: không có swagger, không có bảng route sinh tự động và không có `cases/` (mục 7.10 của [service-principles.md](../../../docs/service-principles.md): không áp dụng). Cổng HTTP (`HTTP_ADDR`, mặc định `:8011`) chỉ phục vụ 4 probe cho orchestrator và `HEALTHCHECK` của Docker. Hợp đồng thật của service nằm ở các **hợp đồng dây** (mục 3), được ghim bằng test.

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
| `GET /health/live` | không | liveness (K8s không được restart pod vì lỗi của DB) | `200` `{"status":"ok"}` |
| `GET /health/startup` | không | startup probe | `200` `{"status":"ok"}` |
| `GET /health/ready` | **có: database** (trong `READINESS_TIMEOUT`, mặc định 2 giây) | readiness: có nhận được việc không | `200` hoặc `503` với `{"ready":bool,"checks":{...}}` |

Luôn `200` cho ba probe đầu khi tiến trình còn sống, kể cả khi đang tắt hoặc DB hỏng (liveness không phụ thuộc ngoài).

### 2.1 `GET /health/ready`

| Tình huống | Mã | Thân |
|---|---|---|
| Database kết nối được, không đang tắt | `200` | `{"ready":true,"checks":{"database":true}}` |
| Database không phản hồi trong `READINESS_TIMEOUT` hoặc lỗi | `503` | `{"ready":false,"checks":{"database":false}}` |
| Đang tắt (sau `SIGTERM`, trong `SHUTDOWN_DRAIN_DELAY` và sau đó) | `503` | `{"ready":false,"checks":{"draining":true}}` (không kiểm database) |

`checks` chỉ có `database` (không có `broker`: worker chỉ phát sự kiện, không nghe). Đang tắt thì `checks` chỉ có `draining`, không có `database`.

**Tác dụng phụ:** không có. **Luật:** BR-31 ([business/04-luat-va-phan-quyen.md](business/04-luat-va-phan-quyen.md)).

### 2.2 Dòng lệnh `healthcheck`

`fhir-worker healthcheck` (điểm vào của image) gọi `GET /healthz` ở `HTTP_ADDR` và thoát `0` khi `200`, khác `0` khi lỗi; image distroless không có `curl`. Lệnh lạ: thoát khác `0` với `cách dùng: fhir-worker [healthcheck]`.

## 3. Hợp đồng dây với `fhir-server`

Giao diện thật của service (không phải HTTP): các bên chung **schema** và **chữ ký**, không import mã của nhau (C-1, C-6).

| Hợp đồng | Nội dung | Ghim bằng |
|---|---|---|
| Resource `AsyncJob` | `status` (`accepted`, `completed`, `error`, `cancelled`), `request` (`<loại>?<tham số>`: `export?`, `batch?`, `reindex?`), lease ở `transactionTime`, `output` (Parameters), extension chữ ký (`JobSealURL`) và checkpoint (`JobStateURL`) | `pkg/fhirrouter/jobs_durable_test.go`, `apps/fhir-worker/internal/bootstrap/bootstrap_test.go` |
| Chữ ký quyền job (job-seal) | HMAC-SHA256 với `FHIR_JOB_SIGNING_KEY` ràng buộc mã job, project, `request` và ảnh chụp quyền; `sealVersion` hiện là 2 (phiên bản khác bị từ chối) | `apps/fhir-worker/internal/bootstrap/bootstrap_test.go`: `TestJobWithAnInvalidSealIsRejected`; `apps/fhir-worker/tests/e2e/worker_test.go` |
| Kết quả job | `output` của export: `output[].type`, `url`, `count`; batch: `results` = `Binary/<id>`; reindex: `count` | `apps/fhir-server/internal/bootstrap/split_e2e_test.go`, `apps/fhir-worker/internal/bootstrap/bootstrap_test.go` |
| URL kết quả | dựng từ `FHIR_BASE_URL`, phải trùng `fhir-server` | (chưa có kiểm lệch tự động) |
| Sự kiện ghi resource | `fhir.resource.changed` (`fhirsub.ChangeEvent`) phát lên Redis khi `FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD` bật | `apps/fhir-worker/internal/bootstrap/bootstrap_test.go`: `TestBatchAsyncWritesResourcesAndPublishesEvents` |
| Cấu hình dùng chung | tên và ý nghĩa các biến `DATABASE_*`, `FHIR_BASE_URL`, `FHIR_BINARY_STORAGE`, `FHIR_JOB_*`, `FHIR_JWT_ISSUER`... | [config.md](config.md) (sinh từ mã), `apps/fhir-worker/internal/config/config_test.go`: `TestSharedEnvFileWithFhirServerIsAccepted` |

## 4. Giữ cho hợp đồng khỏi lệch

- Hình dạng thân và header của probe được ghim bằng `TestProbeContractIsPinned` (`apps/fhir-worker/internal/presentation/httpapi/httpapi_test.go`); đổi thân hoặc header mà không sửa tài liệu này thì test đỏ.
- `fhir-downloader` và `fhir-notifier` có cùng bộ probe (cùng thân, cùng header, `checks` có thêm `broker`) và tệp `api-contract.md` riêng ([downloader](../../fhir-downloader/docs/api-contract.md), [notifier](../../fhir-notifier/docs/api-contract.md)).
