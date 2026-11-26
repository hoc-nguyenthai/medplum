# trivita-dicomweb: độ phủ spec ↔ mã (module Apidog 1533160, nhánh `ai/20261125-from-main-trivita-platform-tests`)

## 1. Nguồn đã đọc
- `apps/dicomweb/docs/swagger.json` (Swagger 2.0, 21 route; import thành 23 endpoint vì /health/* và /healthz) → export OpenAPI 3.1 ở `spec/openapi.json`, chi tiết từng endpoint ở `spec/endpoint-details/`.
- Hợp đồng viết tay, đã đối chiếu mã/test: `apps/dicomweb/docs/api-contract/common.md`, `cases/dicomweb.md` (1268 dòng), `cases/health.md`.
- Mã/test tham chiếu: `apps/dicomweb/internal/bootstrap/d10_test.go` (mẫu STOW dạng metadata), `pkg/dicomtest`, `pkg/dicomweb/encode.go` (`BuildPart10`), `pkg/dicomstow`, `tests/full_smoke/smoke.py` (luồng token → dicomweb).
- **Chưa** đọc được máy chủ đang chạy (chưa dựng docker/stack theo yêu cầu "chỉ local + đọc").

## 2. Quy ước phản hồi
- Lỗi luôn `{"error": "<thông điệp>"}` (`application/json`), không có `code`/OperationOutcome. Assertion thông điệp dùng cột `expected_message` (nguyên văn từ mã/test).
- Thành công: QIDO/metadata/STOW `application/dicom+json` (mảng), tải tệp/frame/bulkdata `multipart/related`, rendered `image/jpeg|png`, WADO-URI tệp `application/dicom`.
- Thứ tự middleware: hạn mức IP → trần body (413 trước cả 401) → xác thực (401/403/400) → handler. Ngoài tiền tố → 404 không qua xác thực.
- Bốn tiền tố tương đương: `/dicomweb`, `/api/dicomweb`, `/projects/{projectId}/dicomweb`, `/api/projects/{projectId}/dicomweb` (swagger chỉ liệt kê `/dicomweb`).
- Auth: JWT của `auth` (`fhir_project` + scope `fhir.read|write`); security scheme trong Apidog là apiKey header `Authorization` (`{{apiKey}}`) nên step dùng **bearer tường minh** `{{accessToken}}`.

## 3. Bảng endpoint
| id | Method Path | Tên | Scenario phủ |
|---|---|---|---|
| 47548523 | GET /dicomweb/instances | QIDO-RS: tìm instance (cả project) | lifecycle |
| 47548542 | GET /health/live | Liveness probe | health |
| 47548524 | GET /dicomweb/series | QIDO-RS: tìm series (cả project) | lifecycle |
| 47548543 | GET /health/ready | Readiness probe | health |
| 47548525 | GET /dicomweb/studies | QIDO-RS: tìm study | lifecycle, stow-validation, qido-matrix, authz, routing |
| 47548544 | GET /health/startup | Startup probe | health |
| 47548526 | POST /dicomweb/studies | STOW-RS: lưu instance | lifecycle, stow-validation, qido-matrix, retrieve-matrix, authz, routing |
| 47548545 | GET /healthz | Healthcheck của Docker | health |
| 47548527 | GET /dicomweb/studies/{studyUid} | WADO-RS: tải study | lifecycle (STOW vào study + tải study), retrieve-matrix |
| 47548528 | POST /dicomweb/studies/{studyUid} | STOW-RS: lưu instance vào một study | lifecycle (study đúng 200 / study sai 409) |
| 47548529 | GET /dicomweb/studies/{studyUid}/instances | QIDO-RS: tìm instance của study | lifecycle, retrieve-matrix |
| 47548530 | GET /dicomweb/studies/{studyUid}/metadata | WADO-RS: metadata của study | lifecycle, retrieve-matrix |
| 47548531 | GET /dicomweb/studies/{studyUid}/series | QIDO-RS: tìm series của study | lifecycle, retrieve-matrix |
| 47548532 | GET /dicomweb/studies/{studyUid}/series/{seriesUid} | WADO-RS: tải series | lifecycle, retrieve-matrix |
| 47548533 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances | QIDO-RS: tìm instance của series | lifecycle, retrieve-matrix |
| 47548534 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid} | WADO-RS: tải instance | lifecycle, retrieve-matrix |
| 47548535 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/bulkdata/{tag} | WADO-RS: bulkdata PixelData | lifecycle, retrieve-matrix |
| 47548536 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame} | WADO-RS: lấy frame | lifecycle, retrieve-matrix |
| 47548537 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered | WADO-RS: ảnh dựng của một frame | lifecycle, retrieve-matrix |
| 47548538 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata | WADO-RS: metadata của instance | lifecycle, retrieve-matrix |
| 47548539 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered | WADO-RS: ảnh dựng của instance | lifecycle, retrieve-matrix |
| 47548540 | GET /dicomweb/studies/{studyUid}/series/{seriesUid}/metadata | WADO-RS: metadata của series | lifecycle, retrieve-matrix |
| 47548541 | GET /dicomweb/wado | WADO-URI: lấy một instance bằng tham số truy vấn | lifecycle, retrieve-matrix, authz |

Quy tắc theo nhóm (từ `cases/dicomweb.md`, đã có test trong repo):
- **STOW** (`POST /studies`, `/studies/{uid}`): 415 `Cần Content-Type multipart/related`; 400 `Thiếu boundary` / `Thân multipart không hợp lệ` / `Yêu cầu không có instance nào`; dạng metadata: phần đầu `application/dicom+json` ≤ 8 MiB, bulkdata khớp `Content-Location` hoặc `cid:`; lỗi từng instance không làm hỏng cả yêu cầu: 200 (tất cả ok, kể cả đã có), 202 (một phần), 409 (không instance nào) kèm `FailedSOPSequence` + `FailureReason` 272/42752/43264/49152. UID: `^[0-9]+(\.[0-9]+)*$`, ≤ 64 ký tự. Instance đã có (cùng SOPInstanceUID) → idempotent 200.
- **QIDO**: thuộc tính theo từ khóa hoặc tag hex; nhiều giá trị `\` hoặc `,` = OR, nhiều thuộc tính = AND; `PatientName`/`ReferringPhysicianName` tiền tố (`*` chỉ ở cuối); `StudyDate` một ngày/khoảng; `limit` mặc định 100 tối đa 1000, `limit`/`offset` hỏng → mặc định, không 400; `Warning: 299` hai dạng; rỗng vẫn `200 []`; lỗi giá trị 400 `<tham số>: <chi tiết>`.
- **WADO-RS/URI**: 404 phân biệt `Study/Series/Instance not found`, `Pixel data not found for instance`, `Bulk data not found`, `Instance has no stored content`; frame ≤ 50 phần tử, mỗi phần tử ≤ 9 ký tự số ≥ 1 (400 `Invalid frame number` / `Too many frames in one request`), 416 vượt số frame; rendered: Accept với `q`, `quality` 1..100, `window=c,w[,linear]`; 501 `Chưa cài đặt` cho rendered cấp series/study, xóa, route lạ dưới tiền tố.

## 4. STOW không cần tệp nhị phân
Dạng **metadata** của STOW (PS3.18 10.5.1.2) gửi DICOM JSON + bulkdata; `pkg/dicomweb.BuildPart10` dựng Part 10 phía server từ đó. Vì bulkdata là *bất kỳ byte nào*, dùng chuỗi ASCII (`ABCDEFGHIJKL` = 3 frame 2×2 8 bit, MONOCHROME2, không nén) → body văn bản `multipart/related` (CRLF) gửi bằng bodyType `raw` + header `Content-Type` ghi trong step. Đọc lại bằng frame/bulkdata so từng byte. Dạng tệp (`application/dicom`) chỉ phủ phần lỗi (text/plain, nội dung không phải DICOM).

## 5. Lệch spec ↔ mã (M#)
| # | Lệch | Ghi chú |
|---|---|---|
| M1 | swagger chỉ có `/dicomweb`; mã còn 3 tiền tố khác (`/api/dicomweb`, `/projects/{id}/dicomweb`, `/api/projects/{id}/dicomweb`) | Scenario `routing-isolation` dùng endpoint tổng hợp 9000003-9000005 |
| M2 | Apidog import gắn security scheme **apiKey** `Authorization: {{apiKey}}` (swagger 2.0 `BearerAuth`) thay vì HTTP bearer | Step dùng bearer tường minh; không cần biến `apiKey` |
| M3 | Route 501 (DELETE study, POST /series, GET /dicomweb/foo, rendered cấp series/study) không có trong swagger | Phủ bằng endpoint tổng hợp 9000007-9000010 và dòng RM-66/67 |
| M4 | Probe `/health/*`, `/healthz` trùng method+path với module khác trong project | Chỉ cảnh báo của import; mỗi module giữ bản riêng |

## 6. Câu hỏi mở (G#)
| # | Câu hỏi | Ảnh hưởng |
|---|---|---|
| G1 | Làm sao có token **chỉ đọc**/thiếu quyền `Binary`? `client_credentials` cấm scope `fhir.*`; scope SMART `system/*.rs` có làm `POST /studies` ra 403 `Token không đủ quyền cho thao tác này`? | 403 phân quyền ghi/đọc, C9/C11 của STOW/WADO chỉ thiết kế, chưa dựng |
| G2 | Giá trị `Accept` rỗng (`Accept:` không giá trị) mà step gửi khi cột `accept` trống: Apidog bỏ header hay gửi rỗng? Cả hai đều phải ra cùng kết quả như không có Accept | Dòng RM với `accept` trống |
| G3 | UID rất dài / chứa `%27` trong **đường dẫn**: router có chặn trước handler (400) hay trả 404 `Study not found`? Kỳ vọng hiện tại 404 (suy ra, không có test) | RM-80, RM-81 |
| G4 | Body `raw` của Apidog có giữ nguyên CRLF và có ghi đè `Content-Type` bằng header step không? | Mọi step STOW; kiểm ở lần chạy đầu (smoke) |
| G5 | `atob` có trong sandbox script của Apidog không (giải mã JWT lấy `fhir_project`)? Nếu không, `projectId` rỗng và các bước tiền tố project sai | Scenario routing-isolation |
| G6 | `ModalitiesInStudy` có được gom lại sau STOW (trường study) như tài liệu ngầm định? | QD-10 |
| G7 | Nên thêm client seed có scope đọc-ghi riêng cho test (hiện dùng client của `seed project`)? | Môi trường chạy |
