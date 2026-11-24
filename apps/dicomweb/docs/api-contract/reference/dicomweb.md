# Tham chiếu API: nhóm `dicomweb`

> **Tệp này được SINH từ `swagger.json`** (Trivita DICOMweb API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/dicomweb.md](../cases/dicomweb.md).

DICOMweb. Bốn tiền tố tương đương: /dicomweb, /api/dicomweb, /projects/{projectId}/dicomweb, /api/projects/{projectId}/dicomweb; với hai tiền tố có project, projectId phải khớp project của token (token thường), không thì 403. Spec này liệt kê tiền tố /dicomweb.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `DICOMWEB_BASE_URL` là https. Bốn tiền tố URL, định dạng lỗi `{"error"}`, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /dicomweb/instances`](#get-dicomweb-instances) | QIDO-RS: tìm instance (cả project) |
| [`GET /dicomweb/series`](#get-dicomweb-series) | QIDO-RS: tìm series (cả project) |
| [`GET /dicomweb/studies`](#get-dicomweb-studies) | QIDO-RS: tìm study |
| [`POST /dicomweb/studies`](#post-dicomweb-studies) | STOW-RS: lưu instance |
| [`GET /dicomweb/studies/{studyUid}`](#get-dicomweb-studies-studyuid) | WADO-RS: tải study |
| [`POST /dicomweb/studies/{studyUid}`](#post-dicomweb-studies-studyuid) | STOW-RS: lưu instance vào một study |
| [`GET /dicomweb/studies/{studyUid}/instances`](#get-dicomweb-studies-studyuid-instances) | QIDO-RS: tìm instance của study |
| [`GET /dicomweb/studies/{studyUid}/metadata`](#get-dicomweb-studies-studyuid-metadata) | WADO-RS: metadata của study |
| [`GET /dicomweb/studies/{studyUid}/series`](#get-dicomweb-studies-studyuid-series) | QIDO-RS: tìm series của study |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}`](#get-dicomweb-studies-studyuid-series-seriesuid) | WADO-RS: tải series |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances`](#get-dicomweb-studies-studyuid-series-seriesuid-instances) | QIDO-RS: tìm instance của series |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}`](#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid) | WADO-RS: tải instance |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/bulkdata/{tag}`](#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-bulkdata-tag) | WADO-RS: bulkdata PixelData |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}`](#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame) | WADO-RS: lấy frame |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered`](#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame-rendered) | WADO-RS: ảnh dựng của một frame |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata`](#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-metadata) | WADO-RS: metadata của instance |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered`](#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-rendered) | WADO-RS: ảnh dựng của instance |
| [`GET /dicomweb/studies/{studyUid}/series/{seriesUid}/metadata`](#get-dicomweb-studies-studyuid-series-seriesuid-metadata) | WADO-RS: metadata của series |
| [`GET /dicomweb/wado`](#get-dicomweb-wado) | WADO-URI: lấy một instance bằng tham số truy vấn |

---

<a id="get-dicomweb-instances"></a>
## `GET /dicomweb/instances`

**QIDO-RS: tìm instance (cả project)**

Trả metadata đã lưu của các instance khớp (kèm thuộc tính study, series và instance); lọc theo thuộc tính cấp study, series và instance (SOPInstanceUID, SOPClassUID, InstanceNumber). Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-instances](../cases/dicomweb.md#get-dicomweb-instances)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `limit` | query | string | không | Cỡ trang (mặc định 100, tối đa 1000) |
| `offset` | query | string | không | Số kết quả bỏ qua |
| `attributes` | query | string | không | Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-series"></a>
## `GET /dicomweb/series`

**QIDO-RS: tìm series (cả project)**

Như tìm series của một study nhưng không giới hạn study; lọc được theo thuộc tính cấp study và series (Modality, SeriesInstanceUID, SeriesNumber...). Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-series](../cases/dicomweb.md#get-dicomweb-series)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `limit` | query | string | không | Cỡ trang (mặc định 100, tối đa 1000) |
| `offset` | query | string | không | Số kết quả bỏ qua |
| `attributes` | query | string | không | Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies"></a>
## `GET /dicomweb/studies`

**QIDO-RS: tìm study**

Trả các study của project (mới cập nhật trước) dạng DICOM JSON. Bộ lọc theo thuộc tính cấp study: PatientID, PatientName (khớp đầu chuỗi, dấu * ở cuối), StudyDate (ngày hoặc khoảng YYYYMMDD-YYYYMMDD), StudyInstanceUID, AccessionNumber, ModalitiesInStudy, StudyDescription..., theo từ khóa hoặc tag 8 chữ số hex; nhiều giá trị ngăn bằng dấu \ hoặc dấu phẩy là OR. Tham số không hỗ trợ bị bỏ qua và báo bằng `Warning: 299`, giá trị sai (ngày hỏng, ký tự đại diện ở giữa) là 400. Khi còn kết quả có header `Warning: 299`. Không có kết quả thì 200 với []. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies](../cases/dicomweb.md#get-dicomweb-studies)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `limit` | query | string | không | Cỡ trang (mặc định 100, tối đa 1000); giá trị hỏng dùng mặc định |
| `offset` | query | string | không | Số kết quả bỏ qua; giá trị hỏng coi là 0 |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-dicomweb-studies"></a>
## `POST /dicomweb/studies`

**STOW-RS: lưu instance**

Thân là multipart/related; type="application/dicom" (mỗi phần là một tệp DICOM Part 10), truyền luồng: phần đầu (tối đa DICOMWEB_MAX_HEADER_BYTES) được kiểm tra, nội dung ghi vào kho Binary dùng chung với fhir-server, metadata thành DicomStudy, DicomSeries, DicomInstance (tìm được qua API FHIR). Tải lại cùng SOPInstanceUID là idempotent. Cũng nhận STOW dạng metadata (PS3.18 10.5.1.2): Content-Type `multipart/related; type="application/dicom+json"`, phần đầu là mảng DICOM JSON (mỗi phần tử một instance, tối đa 8 MiB), các phần sau là bulkdata tham chiếu bằng `BulkDataURI` (khớp `Content-Location` nguyên văn hoặc `cid:` với `Content-ID`); service dựng tệp DICOM Part 10 (Explicit VR Little Endian; PixelData nén thì đóng gói một fragment) rồi lưu như STOW thường, nên kiểm tra, idempotent, chỉ mục frame và phản hồi giống hệt. Transfer syntax lấy từ Content-Type của bulkdata PixelData (tham số `transfer-syntax`, hoặc image/jpeg là JPEG baseline, image/jp2, image/jls, image/jphc; octet-stream là không nén). Bulkdata được ghi ra tệp tạm (TMPDIR) nên cần chỗ trống cỡ tổng bulkdata của yêu cầu; bulkdata ngoài PixelData tối đa 16 MiB và chỉ ở cấp ngoài cùng (không trong SQ); không hỗ trợ PixelData đứng trước phần tử khác (tag lớn hơn 7FE00010), VR lạ, giá trị ngoài miền (instance đó thất bại với FailureReason 0xC000). Từng instance được xử lý độc lập: 200 khi mọi instance thành công, 202 khi một phần (kèm FailedSOPSequence), 409 khi không instance nào thành công. Instance hỏng, bị cắt giữa PixelData, SQ lồng quá sâu, vượt DICOMWEB_MAX_INSTANCE_BYTES bị từ chối riêng và không để lại gì. Cần quyền ghi.

Các trường hợp phản hồi: [../cases/dicomweb.md#post-dicomweb-studies](../cases/dicomweb.md#post-dicomweb-studies)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `multipart/related` |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `multipart/related` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Phản hồi**

**`200`** Mọi instance đã lưu

Schema: `httpapi.StowResponse`

Object tự do (khóa và giá trị tùy ý).

**`202`** Một phần: có cả ReferencedSOPSequence và FailedSOPSequence

Schema: `httpapi.StowResponse`

Object tự do (khóa và giá trị tùy ý).

**`400`** Thân multipart sai, thiếu boundary hoặc không có phần nào

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`409`** Không instance nào được lưu (FailedSOPSequence nêu lý do từng instance)

Schema: `httpapi.StowResponse`

Object tự do (khóa và giá trị tùy ý).

**`413`** Body vượt DICOMWEB_MAX_REQUEST_BYTES

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`415`** Content-Type không phải multipart/related

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid"></a>
## `GET /dicomweb/studies/{studyUid}`

**WADO-RS: tải study**

Trả mọi instance của study dạng multipart/related; type="application/dicom", mỗi phần là tệp DICOM Part 10 nguyên văn như lúc STOW, theo luồng (chunked, bộ nhớ hằng số). Không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối. Cần quyền đọc, gồm Binary.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid](../cases/dicomweb.md#get-dicomweb-studies-studyuid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `multipart/related`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |

**Phản hồi**

**`200`** multipart/related: mỗi phần là một tệp DICOM

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-dicomweb-studies-studyuid"></a>
## `POST /dicomweb/studies/{studyUid}`

**STOW-RS: lưu instance vào một study**

Như STOW-RS ở /studies nhưng mọi instance phải thuộc study chỉ định; instance của study khác bị từ chối riêng với FailureReason 0xA900. Cần quyền ghi.

Các trường hợp phản hồi: [../cases/dicomweb.md#post-dicomweb-studies-studyuid](../cases/dicomweb.md#post-dicomweb-studies-studyuid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `multipart/related` |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `multipart/related` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |

**Phản hồi**

**`200`** Mọi instance đã lưu

Schema: `httpapi.StowResponse`

Object tự do (khóa và giá trị tùy ý).

**`202`** Một phần: có cả ReferencedSOPSequence và FailedSOPSequence

Schema: `httpapi.StowResponse`

Object tự do (khóa và giá trị tùy ý).

**`400`** Thân multipart sai, thiếu boundary hoặc không có phần nào

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`409`** Không instance nào được lưu

Schema: `httpapi.StowResponse`

Object tự do (khóa và giá trị tùy ý).

**`413`** Body vượt DICOMWEB_MAX_REQUEST_BYTES

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`415`** Content-Type không phải multipart/related

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-instances"></a>
## `GET /dicomweb/studies/{studyUid}/instances`

**QIDO-RS: tìm instance của study**

Như tìm instance, giới hạn trong một study. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-instances](../cases/dicomweb.md#get-dicomweb-studies-studyuid-instances)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `limit` | query | string | không | Cỡ trang (mặc định 100, tối đa 1000) |
| `offset` | query | string | không | Số kết quả bỏ qua |
| `attributes` | query | string | không | Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-metadata"></a>
## `GET /dicomweb/studies/{studyUid}/metadata`

**WADO-RS: metadata của study**

Mảng metadata DICOM JSON của mọi instance trong study, theo luồng. Study không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-metadata](../cases/dicomweb.md#get-dicomweb-studies-studyuid-metadata)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series"></a>
## `GET /dicomweb/studies/{studyUid}/series`

**QIDO-RS: tìm series của study**

Trả các series của study dạng DICOM JSON, mỗi phần tử gồm cả thuộc tính của study. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `limit` | query | string | không | Cỡ trang (mặc định 100, tối đa 1000); giá trị hỏng dùng mặc định |
| `offset` | query | string | không | Số kết quả bỏ qua; giá trị hỏng coi là 0 |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Study hoặc series không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}`

**WADO-RS: tải series**

Như tải study, cho các instance của một series.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `multipart/related`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |

**Phản hồi**

**`200`** multipart/related: mỗi phần là một tệp DICOM

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances`

**QIDO-RS: tìm instance của series**

Như tìm instance, giới hạn trong một series. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `limit` | query | string | không | Cỡ trang (mặc định 100, tối đa 1000) |
| `offset` | query | string | không | Số kết quả bỏ qua |
| `attributes` | query | string | không | Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}`

**WADO-RS: tải instance**

Như tải study, cho một instance (một phần duy nhất).

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `multipart/related`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `instanceUid` | đường dẫn | string | có | UID của instance (SOPInstanceUID) |

**Phản hồi**

**`200`** multipart/related: một tệp DICOM

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-bulkdata-tag"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/bulkdata/{tag}`

**WADO-RS: bulkdata PixelData**

Trả toàn bộ dữ liệu điểm ảnh (tag 7FE00010) của instance dạng multipart/related, có Content-Length, đọc theo khoảng nhờ chỉ mục frame. Dữ liệu không nén: MỘT phần application/octet-stream gồm mọi frame nối nhau. Dữ liệu đóng gói (nén): mỗi frame một phần, cùng Content-Type như route frame (khác chuẩn, vốn chia theo fragment). Metadata không chứa BulkDataURI (PixelData bị bỏ khỏi metadata) nên URL này chỉ biết theo quy ước. Tag khác 7FE00010 là 404 vì các bulkdata khác bị bỏ lúc STOW. Cần quyền đọc, gồm Binary.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-bulkdata-tag](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-bulkdata-tag)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `multipart/related`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `instanceUid` | đường dẫn | string | có | UID của instance (SOPInstanceUID) |
| `tag` | đường dẫn | string | có | Tag 8 chữ số hex; chỉ 7FE00010 (PixelData) |

**Phản hồi**

**`200`** multipart/related

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có instance, ảnh hoặc bulkdata theo tag

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`416`** Chỉ mục hoặc metadata không khớp số frame

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}`

**WADO-RS: lấy frame**

Trả một hoặc nhiều frame dạng multipart/related (có Content-Length), chép theo luồng từ nội dung đã lưu (bộ nhớ hằng số). Content-Type của phần: application/octet-stream với dữ liệu không nén, image/jpeg, image/jls, image/jp2, image/jpx, image/jphc kèm transfer-syntax với dữ liệu nén. Frame đóng gói nhiều fragment được nối lại. Vị trí frame lấy từ chỉ mục lập lúc STOW và đọc theo khoảng nên chi phí tỉ lệ với kích thước frame (instance cũ chưa có chỉ mục hoặc kho không đọc theo khoảng thì quay về quét tệp). Cần quyền đọc, gồm cả quyền đọc Binary.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `multipart/related`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `instanceUid` | đường dẫn | string | có | UID của instance (SOPInstanceUID) |
| `frame` | đường dẫn | string | có | Số thứ tự frame bắt đầu từ 1, hoặc danh sách cách nhau bằng dấu phẩy (1,2,3; tối đa 50) |

**Phản hồi**

**`200`** multipart/related: mỗi phần là một frame

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Study, series, instance không có, hoặc instance không có ảnh

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`416`** Số frame vượt tổng số frame của instance

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame-rendered"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered`

**WADO-RS: ảnh dựng của một frame**

Như ảnh dựng của instance nhưng cho frame được chọn (một số, từ 1). Frame vượt tổng số frame là 416.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame-rendered](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-frames-frame-rendered)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `image/jpeg`, `image/png`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `instanceUid` | đường dẫn | string | có | UID của instance (SOPInstanceUID) |
| `frame` | đường dẫn | integer | có | Số thứ tự frame bắt đầu từ 1 |
| `window` | query | string | không | center,width[,linear]: cửa sổ ghi đè (width > 0) |
| `quality` | query | integer | không | Chất lượng JPEG 1..100 (mặc định 90) |

**Phản hồi**

**`200`** Ảnh JPEG hoặc PNG

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có instance hoặc ảnh

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`406`** Accept không có định dạng hỗ trợ, hoặc dữ liệu điểm ảnh chưa dựng được

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Frame quá lớn để dựng

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`416`** Số frame vượt tổng số frame của instance

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-metadata"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata`

**WADO-RS: metadata của instance**

Mảng một phần tử: metadata DICOM JSON của instance. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-metadata](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-metadata)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `instanceUid` | đường dẫn | string | có | UID của instance (SOPInstanceUID) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-rendered"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered`

**WADO-RS: ảnh dựng của instance**

Trả frame đầu tiên của instance dưới dạng ảnh image/jpeg (mặc định) hoặc image/png theo header Accept. Dựng được: JPEG baseline (trả nguyên khi cần JPEG, giải mã và mã hóa PNG khi cần PNG), dữ liệu không nén xám 8 hoặc 16 bit (window/level từ metadata, hoặc từ tham số `window`; MONOCHROME1 được đảo) và RGB 8 bit. Mọi định dạng khác (JPEG 2000, JPEG-LS, RLE, YBR, bảng màu...) trả 406. Bộ nhớ một frame tối đa 64 MiB (vượt thì 413). Tham số viewport, annotation, iccprofile bị bỏ qua. Cần quyền đọc, gồm Binary.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-rendered](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-instances-instanceuid-rendered)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `image/jpeg`, `image/png`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |
| `instanceUid` | đường dẫn | string | có | UID của instance (SOPInstanceUID) |
| `window` | query | string | không | center,width[,linear]: cửa sổ ghi đè (width > 0) |
| `quality` | query | integer | không | Chất lượng JPEG 1..100 (mặc định 90) |

**Phản hồi**

**`200`** Ảnh JPEG hoặc PNG

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có instance hoặc ảnh

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`406`** Accept không có định dạng hỗ trợ, hoặc dữ liệu điểm ảnh chưa dựng được

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Frame quá lớn để dựng

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-studies-studyuid-series-seriesuid-metadata"></a>
## `GET /dicomweb/studies/{studyUid}/series/{seriesUid}/metadata`

**WADO-RS: metadata của series**

Trả mảng metadata DICOM JSON của mọi instance trong series, theo luồng (không bị cắt ở 1000 instance như Medplum). Study hoặc series không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối. Cần quyền đọc.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-metadata](../cases/dicomweb.md#get-dicomweb-studies-studyuid-series-seriesuid-metadata)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom+json`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `studyUid` | đường dẫn | string | có | UID của study (StudyInstanceUID) |
| `seriesUid` | đường dẫn | string | có | UID của series (SeriesInstanceUID) |

**Phản hồi**

**`200`** Mảng DICOM JSON

Mảng các phần tử `object`.

Object tự do (khóa và giá trị tùy ý).

**`400`** Tham số không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Study hoặc series không có

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-dicomweb-wado"></a>
## `GET /dicomweb/wado`

**WADO-URI: lấy một instance bằng tham số truy vấn**

WADO-URI (PS3.18 mục 8.1) cho client cũ. requestType=WADO, studyUID, seriesUID, objectUID bắt buộc. contentType (danh sách có q, hoặc header Accept khi bỏ trống): `application/dicom` trả tệp DICOM Part 10 nguyên văn (không multipart, chunked); `image/jpeg` (mặc định) hoặc `image/png` trả ảnh dựng của frame frameNumber (mặc định 1) với windowCenter, windowWidth, imageQuality, theo cùng giới hạn như `rendered` (định dạng điểm ảnh chưa dựng được: 406). Tham số khác của chuẩn (rows, columns, region, annotation, presentationUID, transferSyntax, charset) bị bỏ qua; anonymize khác "no" là 400. Cần quyền đọc, gồm Binary.

Các trường hợp phản hồi: [../cases/dicomweb.md#get-dicomweb-wado](../cases/dicomweb.md#get-dicomweb-wado)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/dicom`, `image/jpeg`, `image/png`, `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `requestType` | query | string | có | Luôn là WADO |
| `studyUID` | query | string | có | StudyInstanceUID |
| `seriesUID` | query | string | có | SeriesInstanceUID |
| `objectUID` | query | string | có | SOPInstanceUID |
| `contentType` | query | string | không | application/dicom, image/jpeg (mặc định) hoặc image/png; có thể là danh sách kèm q |
| `frameNumber` | query | integer | không | Số thứ tự frame bắt đầu từ 1 (chỉ với ảnh) |
| `windowCenter` | query | number | không | Tâm cửa sổ (chỉ với ảnh; cần cả windowWidth) |
| `windowWidth` | query | number | không | Độ rộng cửa sổ (> 0) |
| `imageQuality` | query | integer | không | Chất lượng JPEG 1..100 |
| `anonymize` | query | string | không | Chỉ chấp nhận no |

**Phản hồi**

**`200`** Tệp DICOM hoặc ảnh

**`400`** Tham số thiếu hoặc không hợp lệ

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token không đủ quyền hoặc chọn project khác

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`404`** Không có study, series, instance hoặc ảnh

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`406`** contentType không hỗ trợ hoặc điểm ảnh chưa dựng được

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Frame quá lớn để dựng

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`416`** Số frame vượt tổng số frame

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi xử lý (không lộ chi tiết nội bộ)

Schema: `httpapi.ErrorBody` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).
