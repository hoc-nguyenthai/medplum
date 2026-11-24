# 4. Kho khác ngoài SQL, bộ nhớ và bí mật

dicomweb **không dùng Redis** và không có hàng đợi bền (`config.go` không có biến Redis hay broker; [../architecture.md](../architecture.md)). Kho "khác" duy nhất là kho Binary.

## 4.1 Kho Binary (`FHIR_BINARY_STORAGE`)

| Khóa / đường dẫn | Giá trị | Vòng đời | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| `binary/<id>/<versionId>` (`file:<thư mục>`: tệp dưới thư mục; `s3:<bucket>[/<prefix>]`: đối tượng `<prefix>binary/...`) với `<id>` là id `Binary` và `<versionId>` là phiên bản **đầu tiên** | (1) **tệp DICOM Part 10 nguyên văn** của một instance, gồm cả PixelData và **mọi** header (kể cả phần đã bị `CleanDataset` bỏ khỏi `metadata`); (2) tùy chọn nội dung một frame (`pixelData`) | tạo cùng `Binary` (CSDL trước, kho sau); xóa bởi `dropBinary`/`dropFrameBinary` khi lỗi; **không xóa khi xóa resource** ([03](03-quan-he-va-toan-ven.md) mục 3.4); `fhir-server $expunge Binary` xóa (lớp `Wrap`); `sweep-binary` **không bắt** được trường hợp nào của dicomweb | dicomweb (STOW ghi luồng; `pixeldata` ghi frame) | dicomweb (WADO-RS: frame theo khoảng byte, rendered, bulkdata, tải tệp, WADO-URI), `fhir-server` (`GET Binary/{id}`, `/storage`) | `GET .../frames` và tải tệp `404 Instance has no stored content` (Binary mất) hoặc `500` sạch (nội dung mất mà Binary còn); instance và metadata vẫn còn |

- **Bắt buộc ở mọi môi trường** (`validateStorage`: `FHIR_BINARY_STORAGE: bắt buộc`), khác `fhir-server` nơi tùy chọn. **Phải trùng** `fhir-server`: hai service ghi và đọc cùng kho, cùng bucket và prefix.
- Kho `file:` cần thư mục ghi được bởi UID `65532` (image distroless không root) và **dùng chung** (volume `dicom-blobs` ở `docker-compose.dicomweb.yml`); production dùng `s3:`.
- **Đây là nơi PHI nhiều nhất và nặng nhất**: ảnh y tế nguyên văn, kèm toàn bộ tag định danh bệnh nhân bên trong tệp. Nội dung **không được mã hóa ở mức ứng dụng**; bảo vệ dựa vào kho: `FHIR_S3_SSE` (`AES256` hoặc `aws:kms`, tùy chọn, mặc định rỗng = không đặt tiêu đề mã hóa phía máy chủ), quyền bucket, và `file:` dựa vào quyền hệ tệp và mã hóa đĩa của hạ tầng (chưa kiểm cách triển khai thực tế).
- **Ai đọc được kho trực tiếp thì đọc được mọi project**: khóa không chứa project (`binary/<id>/<versionId>`); cô lập project chỉ có ở lớp ứng dụng (`meta.project` ở dòng `Binary`). Bucket nên chỉ cấp cho `fhir-server` và dicomweb, quyền tối thiểu.
- Frame (`pixelData`) **nhân đôi** dung lượng: mỗi frame nằm trong tệp gốc và trong Binary riêng ([02](02-mo-hinh-logic.md) mục 2.4).
- Ghi theo luồng, bộ nhớ hằng số (đo: tải 104 MB tốn đỉnh heap 11 MB, theo architecture); S3 bị tính phí theo số request cho mỗi lượt đọc theo khoảng.
- Không có vòng đời tự động ở bucket do dicomweb đặt (lifecycle rule nếu có là việc hạ tầng; chưa kiểm).

## 4.2 Redis và sự kiện

dicomweb **không phát sự kiện thay đổi resource**: `Container` chỉ lắp `database`, `fhirstore`, `binarystore`, `Verifier` và (tùy chọn) `pixelWorker` (`container.go`); không có `fhirsub` hay bộ phát Redis Streams như ở `fhir-server` và `fhir-worker`. Hệ quả (suy từ mã, chưa kiểm bằng chạy hai service): `Subscription` đăng ký ở `fhir-server` cho `DicomStudy`/`DicomInstance` **không kích hoạt** khi dữ liệu do dicomweb ghi; `fhir-downloader` và `fhir-notifier` không thấy các resource này. Khẳng định này chỉ đúng nếu mọi đường ghi `Dicom*` đều qua dicomweb.

## 4.3 Bộ nhớ và đĩa của tiến trình

| Thứ | Nội dung | Mất khi tiến trình chết? | Hệ quả |
|---|---|---|---|
| Hàng đợi `pixelData` | kênh 1.024 việc, `DICOMWEB_PIXELDATA_WORKERS` goroutine (mặc định 2), thử lại 3 lần lùi 1 s, 2 s; **chỉ khi** `DICOMWEB_PIXELDATA_FRAMES=true` | có | instance thiếu `pixelData` tới khi chạy `dicomweb pixeldata`; hàng đợi đầy thì bỏ qua và log `hàng đợi pixelData đầy` |
| Bộ đệm STOW | phần đầu DICOM (tối đa `DICOMWEB_MAX_HEADER_BYTES`, mặc định 1 MiB) và vài bộ đệm 64 KB mỗi yêu cầu | có | bộ nhớ hằng số theo yêu cầu (đo: heap đỉnh 11 MB cho 104 MB) |
| Tệp tạm STOW dạng metadata | bulkdata ghi ra `os.MkdirTemp` dưới `TMPDIR` (thư mục `dicomstow-*`), xóa bằng `defer os.RemoveAll` | tệp **ở lại** nếu bị `kill -9` giữa chừng (chưa kiểm có dọn khi khởi động) | **chứa dữ liệu ảnh (S3) trên đĩa cục bộ** với quyền tiến trình; cần chỗ cho tổng bulkdata một yêu cầu (tối đa `DICOMWEB_MAX_REQUEST_BYTES`, mặc định 4 GiB) |
| Hạn mức theo IP | bộ đếm trong bộ nhớ theo từng instance | có | không chia sẻ giữa các bản sao |
| Cache JWKS | khóa công khai của `auth` | có | đã tải khóa thì vẫn xác minh khi `auth` tạm tắt |

## 4.4 Bí mật: cái nào lưu thế nào

dicomweb **không lưu bí mật nào ở CSDL hay kho**: mọi bí mật là biến môi trường của tiến trình. Nội dung nhạy cảm nằm ở dữ liệu ảnh, xem [02](02-mo-hinh-logic.md) và mục 4.5.

| Bí mật | Nơi | Dạng lưu | Có trong log? | Ghi chú |
|---|---|---|---|---|
| Mật khẩu CSDL (`DATABASE_URL`) | biến môi trường | bản rõ trong URL | không | S4. `DATABASE_ECHO=true` ghi mọi câu SQL **kèm tham số** (tên, mã bệnh nhân, metadata): chỉ để gỡ lỗi, mặc định `false` |
| Khóa truy cập S3 (`FHIR_S3_ACCESS_KEY_ID`, `FHIR_S3_SECRET_ACCESS_KEY`, `FHIR_S3_SESSION_TOKEN`) | biến môi trường | bản rõ | không (config.md: "không có trong log") | S4. **Cấp được đọc mọi ảnh của mọi project** |
| `FHIR_JWT_HMAC_SECRET` | biến môi trường | bản rõ (tối thiểu 32 ký tự) | không | S4. **Chỉ dev và test**: `validate` cấm ở staging/production, dùng `FHIR_JWKS_URL` (https) |
| Token truy cập người dùng (JWT) | tiêu đề `Authorization` mỗi yêu cầu | không lưu | `auth.go` chỉ log **lỗi** của việc kiểm token (`err.Error()`), không log token | S4. Nhật ký kiểm toán super-admin ghi `subject` và `home_project` (`auth.go`) |
| Khóa công khai JWKS | tải từ `FHIR_JWKS_URL` | trong bộ nhớ | không | không phải bí mật |

Đã grep mọi lời gọi `slog`/`fmt.Fprint` trong `apps/dicomweb`, `pkg/dicomstow`, `pkg/dicomretrieve`, `pkg/dicomweb`, `pkg/dicomread`: chỉ ghi id (`binary`, `instance`, `study`, `series`), thông điệp lỗi và `subject` của super-admin; **không** ghi tên bệnh nhân, mã bệnh nhân, UID DICOM hay metadata. Hai chỗ cần nhìn thêm (chưa kiểm): (1) `error` có thể chứa chuỗi từ thư viện đọc DICOM hoặc SQL driver; (2) `DATABASE_ECHO=true`.

## 4.5 Mức nhạy cảm và nơi PHI nằm (tổng hợp)

| Nơi | PHI gì | Nhãn | Thời gian giữ |
|---|---|---|---|
| Kho Binary (tệp nguyên văn) | ảnh, mọi tag header của bệnh nhân | S3 | tới khi ai đó xóa tay; **xóa resource không xóa** |
| `DicomInstance.metadata` (`fhir_resource`) | toàn bộ header trừ PixelData: tên, mã, ngày sinh, giới tính, bác sĩ, cơ sở, ghi chú | S3 | tới Expunge |
| `fhir_history` của `DicomInstance` | **mọi bản cũ chép lại `metadata`**; sau `pixeldata` hoặc `reindex` mỗi instance có thêm một bản | S3 | **vĩnh viễn** nếu chỉ xóa mềm |
| `DicomStudy` (`fhir_resource` và `fhir_history`) | `patientName`, `patientId`, `patientBirthDate`, `patientSex`, `studyDate`, `accessionNumber`, `referringPhysiciansName`, `studyInstanceUid` | S3 | như trên |
| `fhir_idx_string` (`raw`, `norm`) | tên và mã bệnh nhân, accession, bác sĩ: **bản rõ và bản bỏ dấu** để tìm | S3 | tới khi resource xóa (xóa mềm xóa chỉ mục) |
| `fhir_idx_date` | `study-date` | S3 | như trên |
| Tiêu đề, URL và log | `StudyInstanceUID` có trong URL WADO/QIDO (`/studies/{uid}/...`) nên có trong **access log của proxy và gateway**; log của dicomweb không in UID | S3 (UID) | phụ thuộc hạ tầng log |
| Đĩa tạm | bulkdata của STOW dạng metadata | S3 | tới hết yêu cầu (hoặc mãi nếu bị kill) |
| Phản hồi QIDO | `PatientName`, `PatientID`... của mọi study khớp, trong project của người gọi | S3 | không lưu |
