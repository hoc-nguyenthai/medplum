# dicomweb: hợp đồng API

Cập nhật 2026-11-24. `dicomweb` phục vụ DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM. **Ở bước D1 mới có khung**: mọi route DICOMweb đã qua xác thực và cô lập project nhưng trả `501` cho tới khi D3 (STOW-RS) và D4 (QIDO-RS, WADO-RS) cài handler ([kế hoạch](../../../docs/plans/cds-dicom/dicom.md)). Cấu hình ở [config.md](config.md); kiến trúc ở [architecture.md](architecture.md).

| Nguồn | Vai trò |
|---|---|
| `swagger.json` / `swagger.yaml` (cùng thư mục) | **Nguồn chuẩn** của route, tham số, DTO. Sinh từ annotation ở `internal/presentation/httpapi/swagger.go` bằng `make swagger APP=dicomweb`; không sửa tay. |
| Swagger UI | `GET /swagger/index.html` khi service chạy (công khai, chỉ mô tả API). |
| Bảng route bên dưới | Sinh từ `swagger.json`; test `TestAPIContractIsUpToDate` làm đỏ khi lỗi thời. |

## Quy ước chung

- **Bốn tiền tố tương đương:** `/dicomweb`, `/api/dicomweb`, `/projects/{projectId}/dicomweb`, `/api/projects/{projectId}/dicomweb` (giống Medplum). Spec chỉ liệt kê `/dicomweb`. Với hai tiền tố có `projectId`: token thường phải có đúng project đó, không thì `403`; super-admin (khi `FHIR_JWT_SUPER_ADMIN=true`) hành động trong project của URL hoặc header `X-Fhir-Project` (không khớp nhau thì `400`) và mọi yêu cầu được ghi nhật ký kiểm toán. Test `TestAllPrefixesServeTheSameRoutes` giữ bốn tiền tố cùng tập route.
- **Xác thực:** Bearer JWT do `auth` cấp, kiểm cục bộ bằng JWKS (không gọi `auth` mỗi yêu cầu). Không có token tĩnh, không có chế độ tắt xác thực. Thiếu hoặc sai token: `401` (kèm `WWW-Authenticate`); token đúng nhưng thiếu project hoặc scope FHIR, thiếu quyền, hoặc chọn project khác: `403`.
- **Quyền:** đọc (QIDO, WADO: `GET`) cần quyền đọc; mọi phương thức khác (STOW: `POST`) cần quyền ghi. Quyền theo loại resource (`Dicom*`, `Binary`) và chính sách truy cập do `fhirauth.Scope` kiểm ở từng thao tác trên kho khi D3, D4 cài.
- **Lỗi:** luôn `{"error": "<thông điệp>"}` (`Content-Type: application/json`), không dùng OperationOutcome. Lỗi `5xx` chỉ là mô tả trạng thái chuẩn, không lộ chi tiết nội bộ.
- **Route chưa cài** (instances, rendered, bulkdata, xóa, và mọi đường dẫn DICOMweb khác): **`501`** sau xác thực. Khác Medplum, nơi các route này trả `200` với body rỗng khiến client tưởng đã thành công (H5, [dicom.md](../../../docs/plans/cds-dicom/dicom.md) mục 3).
- **Giới hạn kích thước:** body vượt `DICOMWEB_MAX_REQUEST_BYTES` thì `413` (kiểm theo `Content-Length` trước khi đọc, và theo luồng khi không có). Trần từng instance và phần đầu DICOM (`DICOMWEB_MAX_INSTANCE_BYTES`, `DICOMWEB_MAX_HEADER_BYTES`) được áp ở D3.
- **Hạn mức:** theo IP, bật mặc định ở staging/production (`DICOMWEB_RATE_LIMIT`); vượt thì `429` kèm `Retry-After: 1`. Đếm theo từng instance.
- **Probe:** `/health/live`, `/health/startup` (không phụ thuộc ngoài), `/health/ready` (kiểm database, `503` khi đang tắt), `/healthz` (HEALTHCHECK của Docker). Không cần xác thực, không bị hạn mức.

## Bảng route

<!-- BEGIN GENERATED ROUTES (từ swagger.json; chạy UPDATE_DOCS=1 go test để sinh lại, đừng sửa tay) -->

### Nhóm `dicomweb` (17 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /dicomweb/instances` | QIDO-RS: tìm instance (cả project) | Bearer | query: limit, offset, attributes | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/series` | QIDO-RS: tìm series (cả project) | Bearer | query: limit, offset, attributes | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies` | QIDO-RS: tìm study | Bearer | query: limit, offset | 200 `[]object` | 400, 401, 403, 500 |
| `POST /dicomweb/studies` | STOW-RS: lưu instance | Bearer | - | 200 `httpapi.StowResponse`, 202 `httpapi.StowResponse` | 400, 401, 403, 409, 413, 415 |
| `GET /dicomweb/studies/{studyUid}` | WADO-RS: tải study | Bearer | - | 200 `file` | 400, 401, 403, 404, 500 |
| `POST /dicomweb/studies/{studyUid}` | STOW-RS: lưu instance vào một study | Bearer | - | 200 `httpapi.StowResponse`, 202 `httpapi.StowResponse` | 400, 401, 403, 409, 413, 415 |
| `GET /dicomweb/studies/{studyUid}/instances` | QIDO-RS: tìm instance của study | Bearer | query: limit, offset, attributes | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/metadata` | WADO-RS: metadata của study | Bearer | - | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series` | QIDO-RS: tìm series của study | Bearer | query: limit, offset | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}` | WADO-RS: tải series | Bearer | - | 200 `file` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances` | QIDO-RS: tìm instance của series | Bearer | query: limit, offset, attributes | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}` | WADO-RS: tải instance | Bearer | - | 200 `file` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}` | WADO-RS: lấy frame | Bearer | - | 200 `file` | 400, 401, 403, 404, 416, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered` | WADO-RS: ảnh dựng của một frame | Bearer | query: window, quality | 200 `file` | 400, 401, 403, 404, 406, 413, 416, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata` | WADO-RS: metadata của instance | Bearer | - | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered` | WADO-RS: ảnh dựng của instance | Bearer | query: window, quality | 200 `file` | 400, 401, 403, 404, 406, 413, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/metadata` | WADO-RS: metadata của series | Bearer | - | 200 `[]object` | 400, 401, 403, 404, 500 |

### Nhóm `health` (4 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /health/live` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /health/ready` | Readiness probe | công khai | - | 200 `httpapi.ReadyResponse` | 503 |
| `GET /health/startup` | Startup probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /healthz` | Healthcheck của Docker | công khai | - | 200 `httpapi.HealthResponse` | - |

<!-- END GENERATED ROUTES -->
