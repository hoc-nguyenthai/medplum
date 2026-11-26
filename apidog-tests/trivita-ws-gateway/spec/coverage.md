# Coverage — Trivita WS Gateway (module 1533161)

## 1. Nguồn đã đọc
- `apps/ws-gateway/docs/swagger.json` (import 2026-11-25 lên nhánh `ai/20261125-from-main-trivita-platform-tests`), export OpenAPI 3.1 ở `spec/openapi.json`, chi tiết endpoint ở `spec/endpoint-details/`.
- Hợp đồng viết tay: `apps/ws-gateway/docs/api-contract/{common.md,cases/fhircast.md,cases/health.md,cases/websocket.md}` (đã đối chiếu mã `pkg/fhircast`, `internal/presentation/httpapi`, `internal/config/config.go`).
- Chưa có server chạy: KHÔNG đọc discovery/health trực tiếp.

## 2. Quy ước phản hồi
- Thành công Hub: `application/json` — đăng ký/hủy `202 {"hub.channel.endpoint":"ws(s)://.../ws/fhircast/<uuid>"}`; đổi ngữ cảnh `202 {"success":true,"event":{...}}`; GET ngữ cảnh `200` (STU3 và `/hub`: đối tượng `{context.type, context, context.versionId}`; STU2: mảng).
- Lỗi Hub: `application/fhir+json` `OperationOutcome`, thông điệp ở `issue[0].details.text`. Lỗi định tuyến (405/404): `{"error":"..."}` JSON thường.
- 401 kèm `WWW-Authenticate: Bearer realm="fhircast"`; 403 khi thiếu claim project hoặc scope `fhircast/*` (`FHIRCAST_REQUIRE_SCOPE=true`).
- `/hub` là **bí danh của STU3**; ba tiền tố dùng chung một handler. Health: `/health/live`, `/health/startup`, `/healthz` luôn 200 `{"status":"ok"}`; `/health/ready` kiểm Redis (200/503).

## 3. Bảng endpoint (id trên nhánh AI)
| endpointId | Route |
|---|---|
| 47548546 | POST /fhircast/STU2 |
| 47548558 | GET /health/live |
| 47548562 | GET /ws/fhircast/{endpoint} |
| 47548547 | GET /fhircast/STU2/.well-known/fhircast-configuration |
| 47548559 | GET /health/ready |
| 47548563 | GET /ws/subscriptions-r4 |
| 47548548 | GET /fhircast/STU2/{topic} |
| 47548560 | GET /health/startup |
| 47548549 | POST /fhircast/STU2/{topic} |
| 47548561 | GET /healthz |
| 47548550 | POST /fhircast/STU3 |
| 47548551 | GET /fhircast/STU3/.well-known/fhircast-configuration |
| 47548552 | GET /fhircast/STU3/{topic} |
| 47548553 | POST /fhircast/STU3/{topic} |
| 47548554 | POST /hub |
| 47548555 | GET /hub/.well-known/fhircast-configuration |
| 47548556 | GET /hub/{topic} |
| 47548557 | POST /hub/{topic} |

Phủ: 16/18 route bằng scenario (xem `scenarios/scenarios.md`); 2 route WebSocket (`/ws/subscriptions-r4`, `/ws/fhircast/{endpoint}`) chỉ thiết kế.

## 4. Mismatch spec ↔ mã (M)
- M1: swagger khai security scheme là **apiKey** (header `Authorization`, biến `{{apiKey}}`) thay vì Bearer; builder thay bằng `Bearer {{access_token}}` tường minh. Nên sửa swagger (`securityDefinitions` kiểu bearer) cho Apidog hiển thị đúng.
- M2: `GET {v}/{topic}` swagger thiếu `400` (Invalid hub.topic); `POST {v}` swagger thiếu `409` (xung đột update) — đã ghi trong `cases/fhircast.md`. Test vẫn khẳng định 400 theo mã.
- M3: `GET /health/startup` swagger mô tả "Liveness probe" (đúng handler nhưng sai nhãn).

## 5. Câu hỏi mở (G)
- G1: Stack `docker-compose.full.yml` **không bật FHIRcast** cho ws-gateway (không có `FHIRCAST_ENABLED`, `FHIRCAST_JWKS_URL`, `FHIRCAST_JWT_ISSUER/AUDIENCE`, `FHIRCAST_PUBLIC_URL`): mọi route Hub trả 404. Muốn chạy cần file override (xem `dryrun/plan.md`).
- G2: `auth` có cấp được token `client_credentials` với scope `fhircast/*` (và scope khác để thử 403) cho client seed không? `invalid_scope` chỉ chặn `fhir.*`; cần thử thật. `AUTH_FHIRCAST_HUB_URL` chỉ ảnh hưởng claim `hub.url`.
- G3: Bước "Chuẩn bị" dùng `pm.sendRequest`, `pm.variables.set`, `pm.environment.set` trong script của Apidog CLI: cần xác nhận ở lần chạy đầu (smoke) rằng biến `access_token`/`topic` sang được các bước sau.
- G4: Hình dạng bundle `updates` hợp lệ của `DiagnosticReport-update` (bước "Update hợp lệ" ở `hub-diagnosticreport-flow`) và vị trí `context.versionId` (đặt trong `event`) suy từ tài liệu, chưa đo bằng server: xem là thiết kế cần kiểm ở lần chạy đầu.
- G5: Thân JSON `null`/mảng ở `application/json` (SV-23, SV-24) → 400 `Invalid request body` theo tài liệu; Apidog có thể gửi thân rỗng cho `null`: nếu thất bại, phân loại là test defect.
