# dicomweb: kiến trúc

Cập nhật 2026-11-24 (D5).

## Vì sao tách (docs/service-principles.md mục 1)

Điều kiện **A, hồ sơ tải khác**. Tải lên DICOM là hàng chục MB đến hàng GB mỗi instance, một yêu cầu STOW có thể kéo dài hàng phút và phải truyền luồng; `fhir-server` đặt `BodyLimit("16M")` cho mọi yêu cầu, co giãn theo số yêu cầu ngắn và có `WriteTimeout` ngắn. Nhét STOW vào `fhir-server` buộc nới giới hạn và timeout cho cả API FHIR, và để rủi ro nhị phân lớn (bộ nhớ, tệp độc: [d0-spike.md](../../../docs/plans/cds-dicom/d0-spike.md) mục 3) ảnh hưởng cả API lâm sàng. Đo được: đọc tệp 120 MB bằng thư viện mặc định tốn 127 MB bộ nhớ, bằng đường STOW của dicomweb (luồng, `SkipPixelData`) tốn 2 MB. Cũng thỏa điều kiện **C** (deploy và sự cố của kênh tải ảnh không được làm rớt API FHIR).

Đã thử bậc rẻ hơn: **cùng binary, vai trò khác** (`fhir-server` với một chế độ `DICOMWEB`) bị loại vì hai hồ sơ bộ nhớ và timeout trái nhau trong một tiến trình, và vì ranh giới deploy (C) không đạt. Không thỏa B (không có bí mật riêng: dùng cùng schema và kho với `fhir-server`) và D.

## Tầng

Như `fhir-server` và `ws-gateway` ("vỏ mỏng", C-5): `config`, `presentation/httpapi`, `bootstrap`, không có `domain`/`application`/`infrastructure`. Nghiệp vụ DICOM nằm ở `pkg/dicomweb` (phần thuần đã có từ D2: DICOM JSON, chuyển đổi, ánh xạ study/series, làm sạch metadata, số liệu tổng hợp; đọc tệp và tách frame thêm ở D3, D5) để dùng lại và test độc lập. Luật phụ thuộc ở `architecture_test.go`.

```
cmd/dicomweb            serve | healthcheck
internal/config         biến môi trường, kiểm hợp lệ, tài liệu sinh từ mã
internal/presentation   Echo: probe, bốn tiền tố, guard JWT, giới hạn body và hạn mức, lỗi JSON, Swagger
internal/bootstrap      lắp DB, fhirstore, kho Binary, Verifier; vòng đời và tắt êm
```

## Dữ liệu

- **Dùng chung schema `fhir` của fhir-server** (quyết định D-DW-1, 2026-11-24): `DicomStudy`, `DicomSeries`, `DicomInstance` là resource FHIR tìm được qua `GET /fhir/R4/DicomStudy?...` của fhir-server (tương thích Medplum), nên không thể nằm ở schema khác. Hệ quả: (a) **fhir-server sở hữu migration**; dicomweb không migrate và từ chối khởi động khi `fhirstore.Pending > 0` (có test); (b) dicomweb kết nối bằng cùng role ứng dụng của fhir-server (`fhir_app`, chỉ DML), không có role riêng; ngoại lệ so với D-1 của `service-principles.md` ("mỗi service một schema và một role") vì hai service là hai cửa vào của **cùng một miền dữ liệu**; (c) cô lập theo project bằng `fhirauth.Scope`, như fhir-server.
- **Kho Binary dùng chung:** nội dung DICOM (`application/dicom`) ghi vào `binarystore` (tệp hoặc S3) với khóa theo Binary; `fhir-server` đọc cùng kho để phục vụ `Binary/{id}` và `/storage`. Hai service phải trỏ cùng `FHIR_BINARY_STORAGE` (và cùng bucket/prefix). Ghi theo thứ tự CSDL trước, kho sau, bù trừ khi lỗi (như `fhirbinary`, S9) sẽ làm ở D3.
- Không có Redis, không có hàng đợi ở D1. Worker pixel data (D5) dùng `pkg/workflow`.

## Xác thực và cô lập project

Bearer JWT do `auth` cấp, kiểm cục bộ bằng JWKS (`fhirauth.Verifier`, C-9); không có token tĩnh và không có chế độ tắt xác thực. 401 cho thiếu hoặc sai token, 403 cho đúng token nhưng thiếu project, scope hoặc quyền (fail đóng; lỗi hạ tầng không bị đổi thành 401). Quyền (Q-D1): `GET` cần đọc, `POST` cần ghi. Tiền tố `/projects/{id}/dicomweb` phải khớp project của token (Medplum có ca "Cannot use another project scope"); header `X-Fhir-Project` cũng không được chọn project khác. Super-admin (cờ `FHIR_JWT_SUPER_ADMIN`) hành động trong project chọn, và mọi yêu cầu của họ được ghi nhật ký kiểm toán (cùng cách với fhir-server). Truy cập từng resource đi qua `fhirauth.Scope(store)` (`Container.Repo`), nên quyền theo loại `Dicom*`, `Binary` và chính sách truy cập có hiệu lực khi D3, D4 dùng kho.

Lỗi trả `{"error": "..."}` (JSON thường), không dùng OperationOutcome vì DICOMweb không phải API FHIR. Lỗi 5xx không lộ chi tiết nội bộ (có test: panic trong xác thực chỉ ra "Internal Server Error").

## Độ bền, tắt êm, quan sát

- Probe theo mục 6.3: `/health/live`, `/health/startup` không phụ thuộc ngoài; `/health/ready` kiểm database trong `READINESS_TIMEOUT` và 503 khi đang tắt; `/healthz` cho Docker.
- Tắt êm theo mục 6.4: SIGTERM, ready 503, chờ `SHUTDOWN_DRAIN_DELAY`, đóng cổng và chờ yêu cầu đang chạy tối đa `SHUTDOWN_TIMEOUT`, đóng kho và DB, mã thoát 0 (`pkg/httpserve.Run`; có test). Lưu ý riêng của tải lên lớn: một STOW đang chạy dài hơn `SHUTDOWN_TIMEOUT` sẽ bị cắt khi tắt; STOW phải idempotent (tải lại cùng SOPInstanceUID không tạo bản trùng) để client tải lại được. `terminationGracePeriodSeconds` phải lớn hơn `SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT`.
- Hạn mức theo IP đếm theo từng instance (mục 5 điều 7). Log JSON; telemetry qua `pkg/observability`. Không bao giờ ghi tên bệnh nhân, mã bệnh nhân hay metadata DICOM vào log.

## STOW-RS (D3)

Handler (`httpapi.stowHandler`) chỉ chuyển luồng thân yêu cầu cho `pkg/dicomstow`; bootstrap nối với `Container.Repo` và `Container.Blobs`. Mỗi phần (một instance) đi qua: (1) đọc phần đầu vào bộ nhớ (≤ `DICOMWEB_MAX_HEADER_BYTES`) và quét bằng `dicomweb.Scan` (độ sâu SQ ≤ 16, transfer syntax hỗ trợ, kích thước), (2) đọc metadata bằng `dicomread` (thư viện `suyashkumar/dicom` với `SkipPixelData`, chỉ gọi trên dữ liệu đã qua bước 1 vì thư viện tốn bộ nhớ bậc hai với SQ lồng sâu), (3) bỏ qua nếu SOPInstanceUID đã có, (4) tạo Binary rồi ghi luồng vào kho (phần đầu đã đọc, rồi phần còn lại), kiểm tệp không bị cắt, (5) tạo study, series, instance trong một transaction có `LockKey` theo UID theo thứ tự cố định (study, series, instance). Sau cùng tính lại số liệu tổng hợp. Bộ nhớ mỗi yêu cầu là hằng số (phần đầu + vài bộ đệm 64 KB): tải 104 MB tốn đỉnh heap 11 MB.

**Thứ tự ghi và dọn dẹp:** Binary (CSDL) trước, nội dung (kho) sau, resource DICOM cuối cùng; lỗi ở bước nào thì xóa những gì đã tạo ở các bước trước (nội dung rồi Binary, thử lại vài lần). Tiến trình chết giữa chừng có thể để lại nội dung hoặc Binary mồ côi: công cụ quét `fhir-server sweep-binary` xử lý nội dung mồ côi trong kho. Instance chỉ hiện với người dùng khác khi DicomInstance được tạo, tức là sau khi nội dung đã đủ và đúng.

**Idempotent và song song:** tải lại cùng SOPInstanceUID không ghi lại nội dung và không tạo Binary mới; hai yêu cầu song song cùng instance đều tạo Binary nhưng chỉ một thắng ở bước (5), bên thua tự dọn. Khóa theo UID (`pg_advisory_xact_lock` ở PostgreSQL, khóa ghi ở SQLite, thêm tiền tố project) giữ cho tải song song cùng study không tạo trùng study hay series.

**Phản hồi và lỗi (Q-D6, theo chuẩn):** từng instance độc lập: 200 mọi instance thành công; 202 một phần; 409 không instance nào, với `FailedSOPSequence` nêu `FailureReason` (0x0110 lỗi xử lý, 0xA700 quá lớn, 0xA900 sai study, 0xC000 không hiểu được). Lỗi cả yêu cầu: 415, 400, 403 (thiếu quyền ghi kho), 413 (vượt `DICOMWEB_MAX_REQUEST_BYTES`; instance đã xong trước đó vẫn được giữ). Lỗi kho không lộ chi tiết nội bộ trong phản hồi.

## QIDO-RS và WADO-RS (D4)

`pkg/dicomretrieve` đọc lại những gì STOW đã ghi, qua cùng `Container.Repo` (đã tách project, nên mọi tra cứu study/series/instance chỉ thấy dữ liệu của project người gọi) và `Container.Blobs`. Lỗi 404 phân biệt study, series, instance; 416 khi số frame vượt `NumberOfFrames` (kiểm trước khi mở nội dung); instance không có `Rows`/`Columns` (báo cáo có cấu trúc) thì 404 "không có pixel data". **Mọi lỗi xảy ra trước khi ghi byte nào** (kiểm hợp lệ, quét cấu trúc tệp); lỗi sau đó (kho đứt giữa chừng) làm handler ngắt kết nối (`panic(http.ErrAbortHandler)`) để client thấy phản hồi bị cắt chứ không nhận một JSON hay multipart dở dang trông như hợp lệ.

**Frame lấy thẳng từ nội dung đã lưu, theo chỉ mục (D5):** không tách frame ra Binary riêng (sẽ nhân đôi dung lượng và cần hàng đợi). Lúc STOW, trong khi nội dung đang được ghi, dịch vụ lập chỉ mục vị trí từng frame (ảnh không nén: tính từ phần đầu; ảnh đóng gói: một goroutine đọc các fragment qua `io.Pipe` bằng `dicomweb.IndexEncapsulated`) và lưu ở extension `dicomweb.FrameIndexURL` của `DicomInstance`. WADO-RS đọc chỉ mục rồi gọi `binarystore.RangeOpener.OpenRange` cho đúng khoảng của frame (FS: Seek, S3: header Range, kể cả khi máy chủ bỏ qua Range thì vẫn trả đúng byte): chi phí O(kích thước frame), bộ nhớ hằng số. Phần đầu tiên được mở trước khi ghi byte nào nên nội dung mất hay khoảng hỏng trả 500 sạch; khoảng dài hơn nội dung thật thì ngắt kết nối. Không có chỉ mục (instance cũ, chỉ mục hỏng hoặc sai số frame, ảnh đóng gói không tách được, quá 256 KB) hoặc kho không đọc theo khoảng thì quay về cách của D4: quét tìm vị trí rồi chép, O(vị trí frame). Metadata series ghi theo luồng từng trang 500 instance, ghi nguyên văn chuỗi JSON đã làm sạch do STOW lưu.

**Quyền:** đọc frame cần quyền đọc cả `DicomStudy`, `DicomSeries`, `DicomInstance` và `Binary` (Q-D1); thiếu quyền đọc `Binary` thì 403 chứ không phải 500.

## Vượt parity (D7)

**QIDO-RS có bộ lọc và cấp instance** (`pkg/dicomretrieve/query.go`, `search.go`): `GET /studies`, `/series`, `/instances` và các dạng lồng trong study, series. Thuộc tính viết theo từ khóa (`PatientID`) hoặc tag 8 chữ số hex (`00100020`), ánh xạ sang tham số tìm kiếm FHIR của `DicomStudy`, `DicomSeries`, `DicomInstance`: UID và mã khớp nguyên chuỗi, `PatientName` khớp đầu chuỗi không phân biệt hoa thường (dấu `*` ở cuối được bỏ, ở giữa là 400), ngày `YYYYMMDD` hoặc khoảng `a-b`, `-b`, `a-` (ngày hỏng là 400), nhiều giá trị ngăn bằng `\` hoặc dấu phẩy là OR, nhiều thuộc tính là AND. Truy vấn ở một cấp có thể kèm thuộc tính của cấp trên (series theo `PatientID`): trước hết tìm study khớp (tối đa 1000, nhiều hơn thì 400 vì bộ lọc quá rộng) rồi thu hẹp series. `Modality` của series không có tham số tìm kiếm ở kho nên lọc sau khi đọc (tối đa 10000 series). Cấp instance trả nguyên metadata đã lưu. Tham số không hỗ trợ bị bỏ qua và báo bằng `Warning: 299` (không im lặng). Study ở đường dẫn không có thì 404.

**Tải tệp** (`download.go`): `GET /studies/{s}`, `/series/{r}`, `/instances/{i}` trả `multipart/related; type="application/dicom"`, mỗi instance một phần là tệp Part 10 nguyên văn từ kho, theo luồng từng trang 100 instance (bộ nhớ hằng số, chunked, không `Content-Length`). Kiểm study, series, instance và mở thử nội dung của instance đầu trước khi ghi byte nào; lỗi sau đó ngắt kết nối. Metadata cấp study và instance dùng cùng bộ ghi theo luồng với metadata series.

**`rendered`** (`pkg/dicomrender`, chỉ thư viện chuẩn): instance (frame 1) và `frames/{n}/rendered` ra `image/jpeg` (mặc định) hoặc `image/png` theo `Accept` (có `q`). Dựng được: JPEG baseline (trả nguyên, hoặc giải mã rồi mã hóa PNG), không nén xám 8 hoặc 16 bit (RescaleSlope/Intercept, window từ metadata hoặc `window=center,width`, MONOCHROME1 đảo) và RGB 8 bit (xen kẽ hoặc tách mặt phẳng). Mọi định dạng khác (JPEG 2000, JPEG-LS, RLE, JPEG mở rộng 12 bit, YBR, bảng màu, 32 bit) là 406 chứ không trả ảnh sai. Dữ liệu một frame được đọc hết vào bộ nhớ nên có trần 64 MiB (413) và `MaxPixels`. Không làm: `viewport`, `annotation`, rendered cấp series và study (501 hoặc bị bỏ qua).

**Bulkdata và WADO-URI (D9):** `GET .../instances/{i}/bulkdata/7FE00010` trả toàn bộ dữ liệu điểm ảnh dạng `multipart/related` có `Content-Length`, đọc theo khoảng nhờ chỉ mục frame (không nén: MỘT phần chứa mọi frame nối nhau theo chuẩn; nén: mỗi frame một phần, khác chuẩn vốn chia theo fragment). Chỉ PixelData có bulkdata: các dữ liệu lớn khác (overlay, waveform, nhị phân trên 10 KB) bị bỏ lúc STOW nên mọi tag khác là 404. Metadata không chứa `BulkDataURI` (PixelData bị bỏ khỏi metadata, giữ nguyên cam kết cũ) nên URL chỉ biết theo quy ước. `GET /wado?requestType=WADO&studyUID&seriesUID&objectUID` (WADO-URI cho client cũ): `contentType=application/dicom` trả tệp nguyên văn (không multipart), `image/jpeg` (mặc định) và `image/png` trả cùng ảnh như `rendered` (frameNumber, windowCenter/windowWidth, imageQuality); rows, columns, region, annotation, presentation*, transferSyntax, charset bị bỏ qua, `anonymize` khác `no` là 400. Cả hai cùng cách ly project, quyền đọc Binary và lỗi trước khi ghi byte nào như các route WADO-RS khác.

## STOW dạng metadata (D10)

`multipart/related; type="application/dicom+json"`: phần đầu là mảng DICOM JSON (mỗi phần tử một instance), các phần sau là bulkdata tham chiếu bằng `BulkDataURI` (khớp `Content-Location` nguyên văn hoặc `cid:` với `Content-ID`). Nhận biết bằng Content-Type của phần đầu, không phải của cả yêu cầu. `dicomweb.BuildPart10` (thuần, `pkg/dicomweb/encode.go`) dựng tệp Part 10 từ dataset: file meta (SOPClass, SOPInstance, TransferSyntax, ImplementationClassUID), các phần tử theo tag tăng dần (chuỗi nối bằng `\` và đệm chẵn, UI đệm NUL, số nhị phân little endian có kiểm miền, IS/DS, AT, PN nhiều thành phần, SQ độ dài không xác định, `InlineBinary`), tự thêm `SpecificCharacterSet = ISO_IR 192` khi có ký tự ngoài ASCII (JSON là UTF-8). Transfer syntax suy từ Content-Type của bulkdata PixelData (`transfer-syntax=`, hoặc image/jpeg JPEG baseline, image/jp2, image/jls, image/jphc; octet-stream là không nén; Implicit VR LE được dựng thành Explicit vì cùng byte điểm ảnh; Big Endian và Deflated bị từ chối). Dữ liệu nén thì đóng gói thành một fragment (bảng offset rỗng, đệm chẵn, Sequence Delimitation). Kết quả đi qua đúng đường `storeOne` của STOW tệp thường nên kiểm tra UID, từ chối tệp cắt, idempotent, chỉ mục frame, số liệu tổng hợp và phản hồi giống hệt (đã so tệp lưu bằng bộ quét: frame đúng từng byte).

**Bộ nhớ và đĩa:** để biết độ dài phần tử PixelData, mỗi phần bulkdata được ghi ra tệp tạm (`os.MkdirTemp` trong TMPDIR, xóa khi xong kể cả khi lỗi) rồi nối vào tệp DICOM theo luồng; metadata tối đa 8 MiB (413), bulkdata ngoài PixelData tối đa 16 MiB và chỉ ở cấp ngoài cùng. Đo: gửi 96 MB bulkdata dạng metadata, đỉnh heap dưới 64 MB (test), tệp tạm được dọn. Mỗi instance hỏng bị từ chối riêng (FailureReason 0xC000, hoặc 0xA700 khi bulkdata vượt trần instance) và không để lại gì; JSON hỏng, bulkdata thiếu Content-Location/Content-ID hoặc metadata quá lớn là lỗi cả yêu cầu (400, 400, 413). Không hỗ trợ: phần tử có tag lớn hơn PixelData cùng PixelData bulkdata, VR lạ, bulkdata trong SQ, nhiều frame nén (một BulkDataURI một fragment), `BulkDataURI` tương đối khớp theo hậu tố.

## `DicomInstance.pixelData` (D11, tùy chọn)

Tương thích với client đọc thẳng `pixelData` qua API FHIR như Medplum (D5 ban đầu bỏ qua vì tốn dung lượng và WADO-RS không cần). Mỗi frame thành một Binary riêng (`pkg/dicomretrieve/pixeldata.go`): nội dung chép theo luồng từ tệp gốc (đọc theo khoảng nhờ chỉ mục frame, nối các fragment của frame nén), Content-Type theo transfer syntax (`application/octet-stream`, `image/jpeg`, `image/jp2`...), `securityContext` trỏ về `DicomInstance/{id}`, project giữ theo instance; `pixelData` là danh sách tham chiếu theo thứ tự frame, ghi bằng If-Match theo phiên bản đã đọc. **Dung lượng nhân đôi** (mỗi frame lưu hai lần: trong tệp gốc và trong Binary riêng) và WADO-RS không dùng chúng, nên mặc định tắt.

Hai đường: (1) `DICOMWEB_PIXELDATA_FRAMES=true`: sau khi STOW lưu instance mới (không tính tải lại), một worker nền trong tiến trình (`DICOMWEB_PIXELDATA_WORKERS` goroutine, hàng đợi 1024 trong bộ nhớ, thử lại 3 lần lùi 1 s, 2 s) dựng pixelData bằng quyền và project của người STOW; (2) lệnh `dicomweb pixeldata [--dry-run] [--force] [--limit N]` quét bù mọi project (instance có từ trước khi bật, hàng đợi đầy, tiến trình tắt giữa chừng, lỗi sau 3 lần), hai pha như `reindex`, chạy lại an toàn. Không dùng asynq: dicomweb không có Redis và việc này nhẹ, đổi lại hàng đợi không bền; lệnh quét bù là lưới an toàn. Lỗi giữa chừng (một frame không ghi được, thua tranh chấp phiên bản) xóa các Binary và nội dung đã tạo (đã kiểm không để lại rác); `--force` tạo Binary mới, đổi tham chiếu rồi xóa Binary cũ. Giới hạn: xóa `DicomInstance` không xóa Binary frame của nó (như Medplum); tải lại tệp sai khác cùng SOPInstanceUID không cập nhật pixelData (instance đã có là idempotent); `meta.author` không đặt.

## Chưa có (làm ở các bước sau)

D3 STOW-RS (kèm đọc tệp DICOM thật và đọc-sửa-ghi số liệu tổng hợp trên `pkg/dicomweb` đã có từ D2) (tách luồng ghi kho và đọc metadata, chốt chặn SQ lồng sâu và tệp cắt giữa PixelData);  D6 compose và e2e. Manifest K8s mẫu chưa có (dự án hiện bỏ qua K8s).
