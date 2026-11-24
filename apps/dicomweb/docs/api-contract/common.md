# dicomweb: quy ước chung của hợp đồng API

Áp dụng cho **mọi route**; [cases/](cases/README.md) và [reference/](reference/README.md) chỉ ghi phần riêng của từng route và trỏ về đây. Nguồn: `internal/presentation/httpapi/{httpapi,auth}.go`, `pkg/httpsec`.

| Thư mục | Nội dung | Cách duy trì |
|---|---|---|
| [reference/](reference/README.md) | Từng route: header, tham số, bảng từng trường, JSON mẫu | **Sinh** từ `swagger.json`, có test chống lệch |
| [cases/](cases/README.md) | Từng route: từng trường hợp (điều kiện → HTTP → header → body → tác dụng phụ), ví dụ | Viết tay; test bắt route thiếu mục |
| `common.md` | Header, định dạng lỗi, hạn mức, mã trạng thái, bốn tiền tố | Viết tay |

## 1. Địa chỉ, tiền tố, định dạng

- Cổng `:8008`; địa chỉ công khai `DICOMWEB_BASE_URL` (không gồm tiền tố), dùng dựng `RetrieveURL` trong phản hồi STOW.
- **Bốn tiền tố tương đương** (cùng tập route): `/dicomweb`, `/api/dicomweb`, `/projects/{projectId}/dicomweb`, `/api/projects/{projectId}/dicomweb`. Swagger chỉ liệt kê `/dicomweb`. Hai tiền tố có `projectId` chịu ràng buộc project (mục 4).
- Body yêu cầu STOW là `multipart/related`; mọi route còn lại là `GET` không có body. Phản hồi: `application/dicom+json` (QIDO, metadata, STOW), `multipart/related` (tải tệp, frame, bulkdata), `image/jpeg` hoặc `image/png` (rendered, WADO-URI), `application/dicom` (WADO-URI tệp), `application/json` (lỗi và probe).
- Mọi route DICOMweb chưa cài (rendered cấp series/study, xóa, đường dẫn lạ **dưới** tiền tố) trả `501`; đường dẫn **ngoài** tiền tố và không khớp route nào trả `404`.

## 2. Header

### Header yêu cầu

| Header | Dùng ở | Ý nghĩa |
|---|---|---|
| `Authorization: Bearer <access_token>` | mọi route DICOMweb (không phải probe, swagger) | JWT do `auth` cấp, kiểm cục bộ bằng JWKS; không có token tĩnh, không có chế độ tắt. Từ khóa `Bearer` không phân biệt hoa thường |
| `Content-Type` | `POST /studies...` | `multipart/related; type="application/dicom"` (STOW tệp) hoặc `multipart/related; type="application/dicom+json"` (STOW metadata); kèm `boundary` |
| `Accept` | rendered, WADO-URI | Chọn `image/jpeg` (mặc định) hoặc `image/png`; có thể kèm `q` |
| `X-Fhir-Project: <projectId>` | super-admin | Chọn project cần thao tác; token thường không được chọn project khác |
| `Origin` | trình duyệt | Phải thuộc `DICOMWEB_ALLOWED_ORIGINS` mới có header CORS (chỉ áp cho `/dicomweb`, `/api/`, `/projects/`) |
| `X-Forwarded-For` | mọi route | **Bị bỏ qua** trừ khi `DICOMWEB_TRUSTED_PROXIES` chỉ rõ proxy tin cậy |

### Header phản hồi (mọi route, kể cả lỗi)

| Header | Giá trị | Ghi chú |
|---|---|---|
| `Cache-Control` | `no-store` | |
| `Content-Security-Policy` | `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';` | bỏ qua ở `/swagger/*` |
| `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` | `DENY`, `nosniff`, `no-referrer` | |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains; preload` | chỉ khi `DICOMWEB_BASE_URL` là https |
| `X-Request-Id` | id yêu cầu | nêu khi báo lỗi |

### Header phản hồi có điều kiện

| Header | Khi nào | Ý nghĩa |
|---|---|---|
| `WWW-Authenticate` | `401` | `Bearer realm="dicomweb"` (thiếu token) hoặc `Bearer realm="dicomweb", error="invalid_token"` (token sai) |
| `Retry-After: 1` | `429` | |
| `Warning: 299 dicomweb: There are additional results that can be requested` | QIDO còn trang sau | |
| `Warning: 299 dicomweb: Ignored unsupported query parameters: <danh sách>` | QIDO có tham số không hỗ trợ | |
| `Content-Length` | frame, bulkdata | **đặt tường minh** trước khi ghi. Tải nguyên văn và metadata ghi theo luồng nên thường chunked; các route còn lại do Go tự quyết (nội dung nhỏ có thể có `Content-Length`); client không nên dựa vào có/không |
| CORS (`Access-Control-Allow-Origin`, `Access-Control-Allow-Credentials`, `Vary: Origin`, `Access-Control-Expose-Headers`) | `Origin` hợp lệ | phản chiếu đúng origin kèm credentials |

## 3. Định dạng lỗi

Luôn `{"error": "<thông điệp>"}`, `Content-Type: application/json` (DICOMweb không dùng OperationOutcome):

```json
{ "error": "Thiếu bearer token" }
```

- Lỗi `5xx` chỉ là mô tả trạng thái chuẩn (`Internal Server Error`...), **không lộ chi tiết nội bộ** (chi tiết ở log, khớp `X-Request-Id`); ngoại lệ `501` giữ thông điệp `Chưa cài đặt`.
- Route không tồn tại (ngoài tiền tố): `404` `Không có route cho <METHOD> <path>`.
- Body vượt `DICOMWEB_MAX_REQUEST_BYTES`: `413` `Body quá lớn` (kiểm theo `Content-Length` trước khi đọc, hoặc theo luồng khi không có). Thứ tự middleware là hạn mức IP → trần body → xác thực, nên body quá lớn theo `Content-Length` nhận `413` **trước cả `401`** khi không có token.
- Lỗi xảy ra **sau khi đã ghi byte đầu** của phản hồi dài (tải, frame, metadata): server **ngắt kết nối** (không có mã lỗi, không có JSON): client thấy phản hồi bị cắt.

### Thông điệp của tầng xác thực (mọi route DICOMweb)

| HTTP | Thông điệp | Điều kiện |
|---|---|---|
| 401 | `Thiếu bearer token` | không có `Authorization: Bearer ...` |
| 401 | `Token không hợp lệ` | chữ ký, `iss`, `aud`, hạn sai; JWKS không xác minh được |
| 403 | `Token không có quyền truy cập FHIR` | token đúng nhưng thiếu claim project hoặc scope FHIR/SMART |
| 403 | `Token không đủ quyền cho thao tác này` | `GET` cần quyền đọc, `POST` cần quyền ghi |
| 403 | `Không được dùng project khác` | `projectId` ở URL hoặc `X-Fhir-Project` khác project của token (token thường) |
| 400 | `Project ở URL và header X-Fhir-Project khác nhau` | super-admin gửi cả hai và khác nhau |
| 400 | `Super-admin phải chọn project bằng URL hoặc header X-Fhir-Project` | super-admin không chọn, hoặc id không hợp lệ (`^[A-Za-z0-9._:-]{1,128}$`) |
| 403 | `không xác định được người gọi` | hạn mức bật nhưng không xác định được IP |

Ngoài bảng này, lỗi quyền theo loại resource ở tầng kho (thiếu quyền tạo/đọc `Binary`, `Dicom*`) trả `403` thông điệp `Không đủ quyền`; STOW còn có `Chưa xác thực` (401 từ kho). Chi tiết theo route ở [cases/dicomweb.md](cases/dicomweb.md). Lỗi hạ tầng của việc xác minh **không** bị đổi thành `401` (fail đóng). Super-admin: mọi yêu cầu được ghi log kiểm toán (`audit=true`, `home_project`, `target_project`, `method`, `path`, `status`).

## 4. Phân quyền và cô lập project

| Phương thức | Quyền tối thiểu của token |
|---|---|
| `GET` (QIDO, WADO) | đọc |
| `POST` (STOW) | ghi |

Ngoài ra mỗi thao tác trên kho đi qua `fhirauth.Scope(project)`, nên cần quyền theo loại resource: STOW cần tạo `Binary`, `DicomStudy`, `DicomSeries`, `DicomInstance`; lấy nội dung (frame, tải, bulkdata, rendered, WADO-URI) cần đọc cả `Dicom*` **và** `Binary` (thiếu `Binary` ⇒ `403`, không phải `500`). Mọi tra cứu chỉ thấy dữ liệu của project trong token.

## 5. Hạn mức và giới hạn kích thước

| Cơ chế | Cấu hình | Hành vi |
|---|---|---|
| Hạn mức IP (đứng **trước** xác thực, nên cả việc dò token cũng bị chặn) | `DICOMWEB_RATE_LIMIT` (0 tắt; không đặt: tắt ở local, 20 ở staging/production), `DICOMWEB_RATE_BURST` (<=0: gấp đôi) | `429` `quá nhiều yêu cầu, thử lại sau`, `Retry-After: 1`. Chỉ áp cho route DICOMweb; **không** áp cho `/health/*`, `/healthz`, `/swagger/*`. Đếm theo từng instance |
| Trần body | `DICOMWEB_MAX_REQUEST_BYTES` (mặc định 4 GiB) | `413` `Body quá lớn`; instance STOW đã xong trước đó được giữ |
| Trần từng instance | `DICOMWEB_MAX_INSTANCE_BYTES` (1 GiB) | lỗi riêng instance (FailureReason `0xA700`), không phải lỗi cả yêu cầu |
| Trần phần đầu DICOM | `DICOMWEB_MAX_HEADER_BYTES` (1 MiB) | như trên |

## 6. Mã trạng thái dùng chung

| HTTP | Khi nào |
|---|---|
| 200 | Thành công (QIDO rỗng cũng `200 []`) |
| 202 | STOW: một phần instance thành công |
| 400 | Tham số/URL/Content-Type hợp lệ nhưng giá trị sai; super-admin chọn project sai |
| 401 | Thiếu/sai token |
| 403 | Thiếu quyền, project khác |
| 404 | Study/series/instance không có; không có pixel data; route ngoài tiền tố |
| 406 | `rendered`/WADO-URI không dựng được hoặc kiểu yêu cầu không hỗ trợ |
| 409 | STOW: không instance nào thành công |
| 413 | Body hoặc frame quá lớn |
| 415 | STOW không phải `multipart/related` |
| 416 | Số frame vượt `NumberOfFrames` |
| 429 | Hạn mức |
| 500 | Lỗi nội bộ (thông điệp chung) |
| 501 | Route DICOMweb chưa cài |
| 503 | `/health/ready` khi đang tắt hoặc DB lỗi |

## 7. Quy ước ghi trong cases

- **HTTP** và **thông điệp** nguyên văn từ mã/test; không dịch.
- "Thứ tự kiểm tra" theo đúng thứ tự trong mã; lỗi đầu tiên thắng. Mọi lỗi kiểm hợp lệ xảy ra **trước khi ghi byte nào**.
- Ví dụ dùng UID/giá trị giả; `<...>` thay đổi theo yêu cầu.
