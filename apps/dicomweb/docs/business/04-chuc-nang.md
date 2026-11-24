# 4. Danh mục chức năng

Khuôn: **Mục đích · Tác nhân · Route · Luồng · Lỗi · Luật**. Lỗi trả `{"error": "..."}`. Bảng route đầy đủ ở [../api-contract.md](../api-contract.md). Luật `BR-xx` ở [05-luat-va-phan-quyen.md](05-luat-va-phan-quyen.md).

| Nhóm | Chức năng |
|---|---|
| **Lưu** | [F-01 STOW tệp](#f-01) · [F-02 STOW metadata](#f-02) |
| **Tìm** | [F-03 QIDO-RS](#f-03) |
| **Lấy** | [F-04 Metadata](#f-04) · [F-05 Tải nguyên văn](#f-05) · [F-06 Frame](#f-06) · [F-07 Rendered](#f-07) · [F-08 Bulkdata](#f-08) · [F-09 WADO-URI](#f-09) |
| **Nền tảng** | [F-10 Xác thực, phân quyền, cô lập project](#f-10) · [F-11 Route chưa cài](#f-11) |
| **Vận hành** | [F-12 Reindex](#f-12) · [F-13 Kiểm soát chung](#f-13) · [F-14 pixelData tùy chọn](#f-14) |

---

<a id="f-01"></a>
### F-01. STOW-RS tệp Part 10

| | |
|---|---|
| **Mục đích** | Lưu một hoặc nhiều instance DICOM từ thiết bị |
| **Tác nhân** | A1 |
| **Route** | `POST /dicomweb/studies`, `POST /dicomweb/studies/{studyUid}` |
| **Đầu vào** | `multipart/related; type="application/dicom"`, mỗi phần một tệp |
| **Tiền điều kiện** | Quyền ghi; quyền tạo `Binary`, `DicomStudy`, `DicomSeries`, `DicomInstance` |

**Luồng chính** (từng instance độc lập, sơ đồ ở [BF-1](02-luong-nghiep-vu.md)): đọc phần đầu vào bộ nhớ (≤ `DICOMWEB_MAX_HEADER_BYTES`) và quét (`dicomweb.Scan`: SQ ≤ 16, transfer syntax hỗ trợ, kích thước) → đọc metadata bằng `dicomread` (`SkipPixelData`) → bỏ qua nếu SOPInstanceUID đã có → tạo Binary, ghi luồng vào kho kèm lập chỉ mục frame, kiểm tệp không bị cắt → một transaction có khóa theo UID (study, series, instance) → tính lại số liệu tổng hợp.
**Phản hồi:** `200` mọi instance thành công; `202` một phần; `409` không instance nào; kèm `ReferencedSOPSequence` (có `RetrieveURL`) và `FailedSOPSequence` (`FailureReason`: 0x0110, 0xA700, 0xA900, 0xC000).
**Lỗi cả yêu cầu:** `415`, `400`, `403`, `413`.
**Sau khi lưu:** nếu `DICOMWEB_PIXELDATA_FRAMES=true`, instance **mới** (không tính tải lại) được đưa vào hàng đợi dựng pixelData chạy nền: **không làm chậm phản hồi** và lỗi dựng không đổi kết quả STOW (xem F-14).
**Luật:** BR-07..BR-16, BR-31, BR-32.

<a id="f-02"></a>
### F-02. STOW-RS dạng metadata

**Route:** như F-01. **Đầu vào:** phần đầu `application/dicom+json` (mảng, mỗi phần tử một instance), các phần sau là bulkdata tham chiếu bằng `BulkDataURI` (khớp `Content-Location` nguyên văn hoặc `cid:` với `Content-ID`). Nhận biết bằng Content-Type của **phần đầu**.
**Luồng:** bulkdata → tệp tạm → `BuildPart10` dựng tệp → đi đúng đường F-01. Transfer syntax suy từ Content-Type bulkdata PixelData (`transfer-syntax=`, `image/jpeg` baseline, `image/jp2`, `image/jls`, `image/jphc`; `octet-stream` = không nén). Nén: đóng gói một fragment. Tự thêm `SpecificCharacterSet = ISO_IR 192` khi có ký tự ngoài ASCII.
**Lỗi:** `413` metadata > 8 MiB; `400` JSON hỏng, bulkdata thiếu `Content-Location`/`Content-ID`; từ chối riêng: 0xC000 (VR lạ, Big Endian, Deflated...), 0xA700 (bulkdata vượt trần instance). Không hỗ trợ: tag lớn hơn PixelData, bulkdata trong SQ, nhiều frame nén, `BulkDataURI` tương đối khớp hậu tố.
**Luật:** BR-26, và các luật của F-01.

<a id="f-03"></a>
### F-03. QIDO-RS tìm study, series, instance

| | |
|---|---|
| **Route** | `GET /dicomweb/studies`, `/series`, `/instances`; lồng: `/studies/{s}/series`, `/studies/{s}/instances`, `/studies/{s}/series/{r}/instances` |
| **Tham số** | `limit`, `offset`, `attributes`, và thuộc tính lọc (từ khóa hoặc tag hex 8 số) |

**Quy tắc khớp:** UID và mã khớp nguyên chuỗi; `PatientName` khớp đầu chuỗi không phân biệt hoa thường (`*` cuối được bỏ, `*` giữa là `400`); ngày `YYYYMMDD` hoặc khoảng `a-b`, `-b`, `a-` (ngày hỏng `400`); giá trị ngăn bằng `\` hoặc dấu phẩy là OR; nhiều thuộc tính là AND. Truy vấn một cấp kèm thuộc tính cấp trên: tìm study khớp trước (tối đa 1000, nhiều hơn `400`), rồi thu hẹp. `Modality` của series lọc sau khi đọc (tối đa 10000 series). Cấp instance trả nguyên metadata đã lưu.
**Phản hồi:** `200` mảng DICOM JSON (`application/dicom+json`), `[]` khi rỗng; header `Warning: 299 dicomweb: There are additional results that can be requested` khi còn trang; `Warning: 299 dicomweb: Ignored unsupported query parameters: ...` khi bỏ qua tham số lạ. Study ở đường dẫn không có: `404`.
**Luật:** BR-20..BR-22.

<a id="f-04"></a>
### F-04. WADO-RS metadata

`GET .../studies/{s}/metadata`, `.../series/{r}/metadata`, `.../instances/{i}/metadata`. Trả nguyên văn chuỗi JSON đã làm sạch do STOW lưu, **ghi theo luồng từng trang 500 instance**. `404` phân biệt study, series, instance. **Luật:** BR-16, BR-18.

<a id="f-05"></a>
### F-05. WADO-RS tải nguyên văn

`GET .../studies/{s}`, `.../series/{r}`, `.../instances/{i}`. Trả `multipart/related; type="application/dicom"`, mỗi instance một phần là tệp Part 10 nguyên văn từ kho, theo luồng từng trang 100 instance, chunked, không `Content-Length`. Kiểm study/series/instance và mở thử nội dung instance đầu trước khi ghi byte nào; lỗi sau đó ngắt kết nối. **Luật:** BR-03, BR-18.

<a id="f-06"></a>
### F-06. WADO-RS frame

`GET .../instances/{i}/frames/{frame}` (danh sách frame được chấp nhận). Có chỉ mục: đọc đúng khoảng byte (`binarystore.RangeOpener`: tệp Seek, S3 header Range kể cả khi máy chủ bỏ qua Range); không có chỉ mục hoặc kho không đọc theo khoảng: quét rồi chép. Content-Type theo transfer syntax. **Lỗi:** `400` frame không hợp lệ, `404` (study/series/instance, hoặc "Pixel data not found"), `416` vượt `NumberOfFrames` (kiểm **trước** khi mở nội dung), `403` thiếu quyền đọc `Binary`. **Luật:** BR-03, BR-17..BR-19.

<a id="f-07"></a>
### F-07. WADO-RS rendered

`GET .../instances/{i}/rendered` (frame 1) và `.../frames/{n}/rendered`; `Accept` chọn `image/jpeg` (mặc định) hoặc `image/png` (có `q`); tham số `window=center,width`, `quality`. Dựng được: JPEG baseline (trả nguyên, hoặc giải mã rồi PNG), không nén xám 8/16 bit (RescaleSlope/Intercept, window từ metadata hoặc tham số, MONOCHROME1 đảo), RGB 8 bit (xen kẽ hoặc tách mặt phẳng). **`406`** cho mọi định dạng khác (JPEG 2000, JPEG-LS, RLE, JPEG mở rộng 12 bit, YBR, bảng màu, 32 bit): không bao giờ trả ảnh sai. **`413`** khi frame > 64 MiB hoặc quá `MaxPixels`. Không làm: `viewport`, `annotation`, rendered cấp series/study. **Luật:** BR-23.

<a id="f-08"></a>
### F-08. WADO-RS bulkdata PixelData

`GET .../instances/{i}/bulkdata/7FE00010`. Có `Content-Length`, đọc theo khoảng nhờ chỉ mục. Không nén: **một** phần chứa mọi frame nối nhau (đúng chuẩn); nén: **mỗi frame một phần** (khác chuẩn vốn chia theo fragment). Tag khác `7FE00010`, instance không có ảnh, project khác: `404`. `416` khi vượt. **Luật:** BR-24.

<a id="f-09"></a>
### F-09. WADO-URI

`GET /dicomweb/wado?requestType=WADO&studyUID=&seriesUID=&objectUID=` + tùy chọn `contentType`, `frameNumber`, `windowCenter`, `windowWidth`, `imageQuality`, `anonymize`. Luồng quyết định ở [BF-5](02-luong-nghiep-vu.md). **Lỗi:** `400` "requestType phải là WADO", "Thiếu studyUID, seriesUID hoặc objectUID", "Không hỗ trợ anonymize"; `406` "Chỉ hỗ trợ application/dicom, image/jpeg và image/png"; `404`, `416`, `413`. **Luật:** BR-25.

<a id="f-10"></a>
### F-10. Xác thực, phân quyền, cô lập project

Bearer JWT do `auth` cấp, kiểm cục bộ bằng JWKS (`fhirauth.Verifier`); không có token tĩnh, không có chế độ tắt xác thực. Bốn tiền tố tương đương. Chuỗi kiểm tra và thông điệp ở [BF-9](02-luong-nghiep-vu.md). **Lỗi:** `401` "Thiếu bearer token" / "Token không hợp lệ" (+ `WWW-Authenticate`); `403` "Token không có quyền truy cập FHIR", "Không được dùng project khác", "Token không đủ quyền cho thao tác này", "không xác định được người gọi"; `400` "Super-admin phải chọn project bằng URL hoặc header", "Project ở URL và header ... khác nhau". Lỗi hạ tầng không bị đổi thành `401` (fail đóng). **Luật:** BR-02..BR-06.

<a id="f-11"></a>
### F-11. Route chưa cài

Rendered cấp series/study, xóa và mọi đường dẫn DICOMweb khác: **`501`** `{"error":"Chưa cài đặt"}` sau xác thực. Khác Medplum (trả `200` rỗng khiến client tưởng thành công). **Luật:** BR-27.

<a id="f-12"></a>
### F-12. Reindex (lập lại chỉ mục frame)

`dicomweb reindex [--dry-run] [--force] [--limit N]`. Cùng cấu hình với server, duyệt mọi project, kết nối DB trực tiếp (không qua token), từ chối chạy nếu schema chưa migrate. In `đã duyệt, lập chỉ mục, đã đủ, không có ảnh, không lập được, lỗi`; thoát khác 0 nếu có `lỗi`. Luồng ở [BF-7](02-luong-nghiep-vu.md). **Luật:** BR-17, BR-30.

<a id="f-13"></a>
### F-13. Kiểm soát chung và vận hành

Probe `/health/live`, `/health/startup`, `/health/ready` (kiểm DB trong `READINESS_TIMEOUT`, `503` khi đang tắt), `/healthz`, `dicomweb healthcheck`; hạn mức IP (`429` + `Retry-After: 1`); trần body (`413`); CORS theo `DICOMWEB_ALLOWED_ORIGINS`; từ chối khởi động khi cấu hình sai (gom mọi lỗi) hoặc schema chưa migrate; tắt êm; log JSON, không ghi dữ liệu bệnh nhân. **Luật:** BR-01, BR-05, BR-06, BR-11, BR-28, BR-29.

<a id="f-14"></a>
### F-14. pixelData tùy chọn (`DicomInstance.pixelData`, D11)

| | |
|---|---|
| **Mục đích** | Client đọc frame thẳng qua API FHIR (`DicomInstance.pixelData` → `Binary/{id}`) như với Medplum |
| **Tác nhân** | A5 vận hành (bật, quét bù); A8 `fhir-server` và client FHIR (đọc) |
| **Tiền điều kiện** | `DICOMWEB_PIXELDATA_FRAMES=true` cho đường nền; lệnh `dicomweb pixeldata` không cần cờ |
| **Cấu hình** | `DICOMWEB_PIXELDATA_FRAMES` (mặc định `false`: gấp đôi dung lượng ảnh), `DICOMWEB_PIXELDATA_WORKERS` (mặc định `2`) |
| **Lệnh** | `dicomweb pixeldata [--dry-run] [--force] [--limit N]` |

**Luồng chính** (sơ đồ ở [BF-10](02-luong-nghiep-vu.md)): mỗi frame thành một `Binary` riêng (nội dung chép theo luồng từ tệp gốc nhờ chỉ mục frame, nối các fragment của frame nén; `Content-Type` theo transfer syntax: `application/octet-stream`, `image/jpeg`, `image/jp2`...; `securityContext` trỏ `DicomInstance/{id}`; giữ project của instance) rồi `pixelData` là danh sách tham chiếu theo thứ tự frame, ghi bằng `If-Match`.
- **Đường nền:** sau STOW instance mới; hàng đợi 1024 trong bộ nhớ; `DICOMWEB_PIXELDATA_WORKERS` goroutine; thử lại tối đa 3 lần (lùi 1 giây, 2 giây); dựng bằng **quyền và project của người STOW**; instance không có ảnh bị bỏ qua, không phải lỗi.
- **Quét bù** `dicomweb pixeldata`: duyệt mọi project, hai pha như `reindex`, in `đã duyệt, dựng (số frame), đã đủ, không có ảnh, lỗi`, thoát khác 0 nếu có `lỗi`, chạy lại an toàn, chạy được khi server đang phục vụ. `--force` tạo Binary mới, đổi tham chiếu rồi xóa Binary cũ.

**Lỗi và giới hạn:** hàng đợi đầy hoặc đang tắt ⇒ bỏ qua, log `hàng đợi pixelData đầy` (không ảnh hưởng STOW); hết 3 lần ⇒ log `không dựng được pixelData`; một frame không ghi được hoặc thua tranh chấp phiên bản ⇒ **xóa mọi Binary và nội dung đã tạo**. Xóa `DicomInstance` không xóa Binary frame; tải lại tệp khác cùng SOPInstanceUID không cập nhật pixelData; `meta.author` không đặt.
**Luật:** BR-33..BR-37.
