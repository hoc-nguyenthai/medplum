# ws-gateway: quy ước chung của API

Áp dụng cho **mọi route**; các tệp `cases/` chỉ nêu điều khác. Bảng route và từng trường: [../api-contract.md](../api-contract.md) và [reference/](reference/). Giao thức thông điệp của hai kênh WebSocket (khung, mã đóng, đảm bảo giao hàng) ở [../api-contract.md](../api-contract.md) mục 3 và 4.

## 1. Địa chỉ và điều kiện có route

| Mục | Giá trị |
|---|---|
| Cổng | `HTTP_ADDR` (mặc định `:8007`) |
| Route luôn có | `GET /healthz`, `/health/live`, `/health/startup`, `/health/ready`, `GET /ws/subscriptions-r4` |
| Route chỉ có khi `FHIRCAST_ENABLED=true` | `GET /ws/fhircast/{endpoint}` và Hub FHIRcast (`/fhircast/STU2`, `/fhircast/STU3`, `/hub` cùng các route con); tắt thì `404` `{"error":"Not Found"}` |
| Ba tiền tố của Hub | `/fhircast/STU2` (STU2), `/fhircast/STU3` (STU3), `/hub` (**bí danh của STU3**): cùng một xử lý, khác phiên bản phản hồi của `GET {tiền tố}/{topic}` và `.well-known` (xem `cases/fhircast.md`) |
| Phiên bản API | không có tiền tố phiên bản riêng; phiên bản FHIRcast nằm trong đường dẫn |

## 2. Header phản hồi chung

Mọi phản hồi (kể cả `401`, `403`, `429`, `404`, `503`) có: `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-Xss-Protection: 1; mode=block`, `Permissions-Policy` (tắt các tính năng trình duyệt), `X-Request-Id` (sinh mới nếu client không gửi). **Không** có `Strict-Transport-Security` (TLS kết thúc ở proxy) và **không có CORS** (không `Access-Control-*`; kết nối WebSocket kiểm `Origin` riêng, mục 5).

## 3. Định dạng lỗi

Có hai dạng, theo nhóm route:

| Nhóm | `Content-Type` | Thân | Ghi chú |
|---|---|---|---|
| Hub FHIRcast (`/fhircast/...`, `/hub...`) | `application/fhir+json` | `OperationOutcome` với `issue[0].severity="error"`, `issue[0].code` (`login` cho 401, `forbidden`, `invalid`, `conflict`, `too-costly`, `exception`), thông điệp ở `issue[0].details.text` | `401` kèm `WWW-Authenticate: Bearer realm="fhircast"`; lỗi `5xx` không lộ chi tiết nội bộ (`Internal server error`) |
| Mọi route khác (probe, trước khi nâng cấp WebSocket, route lạ) | `application/json` | `{"error":"<thông điệp>"}` | lỗi `5xx` là `{"error":"Internal Server Error"}` |

## 4. Xác thực

| Route | Cách |
|---|---|
| Probe, `.well-known` của Hub | công khai |
| Hub FHIRcast (mọi route trừ `.well-known`) | `Authorization: Bearer <JWT của auth>`; kiểm bằng JWKS (`FHIRCAST_JWKS_URL`, HMAC chỉ dev); cần claim project và scope `fhircast/*` (không cần `fhir.*`). Thiếu hoặc sai token (kể cả khi không tải được JWKS: mọi lỗi kiểm chữ ký đều thành `401`): `401` kèm `WWW-Authenticate`; đủ token nhưng thiếu project hoặc scope: `403`; lỗi xác thực không phân loại được: `500`. `FHIRCAST_REQUIRE_SCOPE=false` bỏ kiểm scope (bị cấm ở staging/production) |
| `GET /ws/subscriptions-r4` | **không có header xác thực**: token bind (do `fhir-server` cấp, ký HMAC bằng `FHIR_WS_SIGNING_KEY`) gửi trong khung đầu tiên sau khi nối |
| `GET /ws/fhircast/{endpoint}` | **không có header xác thực**: `endpoint` là bí mật 122 bit do `POST {tiền tố}/` cấp |

## 5. Hạn mức và giới hạn kết nối (chỉ hai route WebSocket)

Hạn mức đứng **trước** handler nâng cấp; probe và Hub HTTP **không** bị hạn mức (chưa có hạn mức theo IP cho Hub).

| Tình huống | Mã | Thân |
|---|---|---|
| Quá `FHIR_WS_RATE_LIMIT` (số lần mở mỗi giây mỗi IP, `0`: tắt) | `429` | `{"error":"quá nhiều yêu cầu, thử lại sau"}` kèm `Retry-After: 1` |
| Không xác định được IP người gọi | `403` | `{"error":"không xác định được người gọi"}` |
| `Origin` không được phép (`FHIR_WS_ALLOWED_ORIGINS`; mặc định chỉ cùng host) | `403` | do bộ nâng cấp WebSocket trả (không phải JSON của gateway; xem `cases/websocket.md`) |
| Quá `FHIR_WS_MAX_CONNS_PER_IP` | `429` | xem `cases/websocket.md` |
| Hết `FHIR_WS_MAX_CONNECTIONS` hoặc đang tắt | `503` | xem `cases/websocket.md` |

IP người gọi lấy trực tiếp từ kết nối; chỉ tin `X-Forwarded-For` từ proxy khai trong `FHIR_WS_TRUSTED_PROXIES` (không khai thì mọi người sau proxy chung một hạn mức).

## 6. Mã HTTP chung

| Mã | Khi nào |
|---|---|
| `200` | probe, `.well-known`, đọc ngữ cảnh |
| `101` | nâng cấp WebSocket thành công |
| `202` | Hub nhận yêu cầu (đăng ký, hủy, đổi ngữ cảnh) |
| `400` | thân hỏng hoặc Hub từ chối vì dữ liệu sai (`invalid`) |
| `400` (GET) | `GET {v}/{topic}` với topic không hợp lệ: `Invalid hub.topic` (mã 400 cũng có ở GET) |
| `401` / `403` | thiếu hoặc sai Bearer / thiếu quyền (Hub) |
| `405` | sai method trên route Hub, xảy ra **trước** xác thực, dạng `{"error":"Method Not Allowed"}`, không có `WWW-Authenticate` |
| `404` | route lạ, hoặc route FHIRcast khi `FHIRCAST_ENABLED` tắt |
| `409` | xung đột ngữ cảnh (Hub) |
| `413` | thân Hub quá lớn (`Request body too large`) |
| `429`, `503` | hạn mức và giới hạn kết nối (mục 5) |
| `500` | lỗi hạ tầng (Redis...), không lộ chi tiết |

## 7. Quy ước trong `cases/`

Mỗi route có mục `<a id="slug">` với bảng "điều kiện → mã → header → thân → tác dụng phụ"; dùng các tên mục trên thay vì lặp lại. Thông điệp lỗi trích nguyên văn từ mã.
