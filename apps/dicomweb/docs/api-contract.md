# dicomweb: hợp đồng API

Cập nhật 2026-11-24 (D11). `dicomweb` phục vụ DICOMweb cho kho hình ảnh DICOM: STOW-RS (tệp Part 10 và dạng metadata), QIDO-RS ba cấp có bộ lọc, WADO-RS (tải, metadata, frame, rendered, bulkdata PixelData) và WADO-URI ([kế hoạch](../../../docs/plans/cds-dicom/dicom.md)). Chỉ route DICOMweb chưa cài (rendered cấp series và study, xóa, đường dẫn lạ) trả `501`. Cấu hình ở [config.md](config.md); kiến trúc ở [architecture.md](architecture.md).

> **Chi tiết từng route (header, request, response từng trường hợp)** ở [api-contract/common.md](api-contract/common.md), [api-contract/reference/](api-contract/reference/README.md) (bảng từng trường, sinh từ swagger) và [api-contract/cases/](api-contract/cases/README.md). Tệp này giữ quy ước và bảng route tổng hợp.

| Nguồn | Vai trò |
|---|---|
| `swagger.json` / `swagger.yaml` (cùng thư mục) | **Nguồn chuẩn** của route, tham số, DTO. Sinh từ annotation ở `internal/presentation/httpapi/swagger.go` bằng `make swagger APP=dicomweb`; không sửa tay. |
| Swagger UI | `GET /swagger/index.html` khi service chạy (công khai, chỉ mô tả API). |
| Bảng route bên dưới | Sinh từ `swagger.json`; test `TestAPIContractIsUpToDate` làm đỏ khi lỗi thời. |

## Quy ước chung

- **Bốn tiền tố tương đương:** `/dicomweb`, `/api/dicomweb`, `/projects/{projectId}/dicomweb`, `/api/projects/{projectId}/dicomweb` (giống Medplum). Spec chỉ liệt kê `/dicomweb`. Với hai tiền tố có `projectId`: token thường phải có đúng project đó, không thì `403`; super-admin (khi `FHIR_JWT_SUPER_ADMIN=true`) hành động trong project của URL hoặc header `X-Fhir-Project` (không khớp nhau thì `400`) và mọi yêu cầu được ghi nhật ký kiểm toán. Test `TestAllPrefixesServeTheSameRoutes` giữ bốn tiền tố cùng tập route.
- **Xác thực:** Bearer JWT do `auth` cấp, kiểm cục bộ bằng JWKS (không gọi `auth` mỗi yêu cầu). Không có token tĩnh, không có chế độ tắt xác thực. Thiếu hoặc sai token: `401` (kèm `WWW-Authenticate`); token đúng nhưng thiếu project hoặc scope FHIR, thiếu quyền, hoặc chọn project khác: `403`.
- **Quyền:** đọc (QIDO, WADO: `GET`) cần quyền đọc; mọi phương thức khác (STOW: `POST`) cần quyền ghi. Quyền theo loại resource (`Dicom*`, `Binary`) và chính sách truy cập do `fhirauth.Scope` kiểm ở từng thao tác trên kho: STOW cần tạo `Binary` và `Dicom*`; lấy nội dung (frame, tải, bulkdata, rendered, WADO-URI) cần đọc cả `Dicom*` và `Binary` (thiếu `Binary` là `403`, không phải `500`).
- **Lỗi:** luôn `{"error": "<thông điệp>"}` (`Content-Type: application/json`), không dùng OperationOutcome. Lỗi `5xx` chỉ là mô tả trạng thái chuẩn, không lộ chi tiết nội bộ.
- **Route chưa cài** (rendered cấp series và study, xóa, và mọi đường dẫn DICOMweb khác dưới tiền tố): **`501`** `Chưa cài đặt` sau xác thực; đường dẫn ngoài tiền tố và không khớp route nào là `404`. Bulkdata chỉ có PixelData (`7FE00010`): tag khác là `404`. Khác Medplum, nơi các route này trả `200` với body rỗng khiến client tưởng đã thành công (H5, [dicom.md](../../../docs/plans/cds-dicom/dicom.md) mục 3).
- **Giới hạn kích thước:** body vượt `DICOMWEB_MAX_REQUEST_BYTES` thì `413` (kiểm theo `Content-Length` trước khi đọc, và theo luồng khi không có). Trần từng instance và phần đầu DICOM (`DICOMWEB_MAX_INSTANCE_BYTES`, `DICOMWEB_MAX_HEADER_BYTES`) áp riêng cho từng instance của STOW: vượt thì instance đó bị từ chối với `FailureReason` `0xA700`, không phải `413` cả yêu cầu.
- **Hạn mức:** theo IP, bật mặc định ở staging/production (`DICOMWEB_RATE_LIMIT`); vượt thì `429` kèm `Retry-After: 1`. Đếm theo từng instance.
- **Probe:** `/health/live`, `/health/startup` (không phụ thuộc ngoài), `/health/ready` (kiểm database, `503` khi đang tắt), `/healthz` (HEALTHCHECK của Docker). Không cần xác thực, không bị hạn mức.

## Bảng route

<!-- BEGIN GENERATED ROUTES (từ swagger.json; chạy UPDATE_DOCS=1 go test để sinh lại, đừng sửa tay) -->

### Nhóm `dicomweb` (19 route)

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
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/bulkdata/{tag}` | WADO-RS: bulkdata PixelData | Bearer | - | 200 `file` | 401, 403, 404, 416, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}` | WADO-RS: lấy frame | Bearer | - | 200 `file` | 400, 401, 403, 404, 416, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered` | WADO-RS: ảnh dựng của một frame | Bearer | query: window, quality | 200 `file` | 400, 401, 403, 404, 406, 413, 416, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata` | WADO-RS: metadata của instance | Bearer | - | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered` | WADO-RS: ảnh dựng của instance | Bearer | query: window, quality | 200 `file` | 400, 401, 403, 404, 406, 413, 500 |
| `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/metadata` | WADO-RS: metadata của series | Bearer | - | 200 `[]object` | 400, 401, 403, 404, 500 |
| `GET /dicomweb/wado` | WADO-URI: lấy một instance bằng tham số truy vấn | Bearer | query: requestType, studyUID, seriesUID, objectUID, contentType, frameNumber, windowCenter, windowWidth, imageQuality, anonymize | 200 `file` | 400, 401, 403, 404, 406, 413, 416, 500 |

### Nhóm `health` (4 route)

| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |
|---|---|---|---|---|---|
| `GET /health/live` | Liveness probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /health/ready` | Readiness probe | công khai | - | 200 `httpapi.ReadyResponse` | 503 |
| `GET /health/startup` | Startup probe | công khai | - | 200 `httpapi.HealthResponse` | - |
| `GET /healthz` | Healthcheck của Docker | công khai | - | 200 `httpapi.HealthResponse` | - |

<!-- END GENERATED ROUTES -->
