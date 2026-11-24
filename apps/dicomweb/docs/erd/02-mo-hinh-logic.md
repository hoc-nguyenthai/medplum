# 2. Mô hình logic

`dicomweb` sở hữu **ba loại resource riêng** của ảnh DICOM: `DicomStudy`, `DicomSeries`, `DicomInstance` (mô hình Medplum, cây study, series, instance), và ghi thêm `Binary` (nội dung tệp DICOM, và tùy chọn từng frame). Tất cả lưu ở `fhir_resource` (`resource_type='DicomStudy'`... ), schema `fhir`; `fhir-server` đọc và tìm cùng các loại này qua `GET /fhir/R4/DicomStudy?...`. Nhãn nhạy cảm S0..S4 theo `references/templates.md` của skill: S1 định danh kỹ thuật, S2 dữ liệu cá nhân, S3 dữ liệu lâm sàng (PHI, gồm ảnh DICOM), S4 bí mật xác thực. **Không có trường nào ở đây là S4.**

Mọi trường dưới đây do **dicomweb ghi lúc STOW** từ header DICOM của tệp tải lên (không có API sửa tay trong dicomweb; `fhir-server` có thể sửa hoặc xóa qua API FHIR chung, xem [03](03-quan-he-va-toan-ven.md) mục 3.4). Ngày và giờ được đổi sang dạng FHIR (`2024-01-02`, `03:04:05`) bởi `dicomweb.DateToFHIR` và `TimeToFHIR`; giá trị không hợp lệ bị bỏ (`pkg/dicomweb/study.go`).

## 2.1 ERD logic

```mermaid
erDiagram
    DICOM_STUDY ||--o{ DICOM_SERIES : "DicomSeries.study Reference"
    DICOM_STUDY ||--o{ DICOM_INSTANCE : "DicomInstance.study Reference"
    DICOM_SERIES ||--o{ DICOM_INSTANCE : "DicomInstance.series Reference"
    DICOM_INSTANCE ||--|| BINARY : "raw: nội dung tệp Part 10"
    DICOM_INSTANCE ||--o{ BINARY : "pixelData: một Binary mỗi frame, tùy chọn"
    BINARY ||--o| DICOM_INSTANCE : "securityContext ngược về instance"
    BINARY }o--o| BLOB : "khóa binary/id/versionId"
    DICOM_STUDY }o--|| PROJECT : "meta.project, chuỗi id"
    DICOM_SERIES }o--|| PROJECT : "meta.project"
    DICOM_INSTANCE }o--|| PROJECT : "meta.project"
    BINARY }o--|| PROJECT : "meta.project"
    DICOM_STUDY }o..o| PATIENT : "KHÔNG có liên kết lưu"
    DICOM_STUDY }o..o| IMAGING_STUDY : "KHÔNG có liên kết lưu"
    DICOM_STUDY {
      string studyInstanceUid UK
      string patientName
      string patientId
      string studyDate
      string modalitiesInStudy
      integer numberOfStudyRelatedInstances
    }
    DICOM_SERIES {
      string study FK_logic
      string seriesInstanceUid UK
      string modality
      integer numberOfSeriesRelatedInstances
    }
    DICOM_INSTANCE {
      string study FK_logic
      string series FK_logic
      string sopInstanceUid UK
      string sopClassUid
      string metadata
      string raw FK_logic
      string pixelData FK_logic
    }
    BINARY {
      string contentType
      string securityContext
      string data_luon_rong
    }
```

Không có khóa ngoại và **không có ràng buộc duy nhất ở mức CSDL**: `UK` ở trên chỉ nghĩa là mã tìm theo UID trước khi tạo (xem [03](03-quan-he-va-toan-ven.md) mục 3.3). `Patient` và `ImagingStudy` **không được dicomweb tạo, đọc hay trỏ tới**: `DicomStudy` chỉ chép tên, mã, ngày sinh, giới tính bệnh nhân từ header DICOM thành chuỗi, không nối với hồ sơ `Patient` (đường chấm là *thiếu* liên kết; đã grep `pkg/dicomstow`, `pkg/dicomretrieve`, `pkg/dicomweb`: không có `Patient` hay `ImagingStudy`).

## 2.2 DicomStudy

**Mục đích nghiệp vụ:** một đợt chụp (study) của một bệnh nhân; nút gốc của cây ảnh, và đơn vị mà QIDO-RS cấp study trả về. **Lưu ở:** `fhir_resource` (`resource_type='DicomStudy'`), schema `fhir`. **Ghi bởi:** `dicomstow` **một lần**, lúc instance đầu tiên của study đến (`persist`: `StudyFromDataset`, `ns.ID = newID()`), rồi `updateStudy` ghi lại **ba trường tổng hợp** sau mỗi yêu cầu STOW có instance mới. **Không bao giờ cập nhật** các trường nhân khẩu từ instance đến sau: instance sau thuộc cùng `StudyInstanceUID` nhưng tên bệnh nhân khác thì bị bỏ qua (study đã có nên không tạo lại). **Đọc bởi:** QIDO-RS (`StudyToDataset`), WADO-RS (kiểm study tồn tại, dựng metadata), `fhir-server` (API FHIR). **Xóa bởi:** không có đường xóa trong dicomweb ([03](03-quan-he-va-toan-ven.md) mục 3.4).

| Trường | Kiểu | Bắt buộc | Chức năng | Nhạy cảm |
|---|---|---|---|---|
| `studyInstanceUid` | string | có | Study Instance UID (0020,000D), dạng `1.2.826.0.1.3680043.8.498.1`; khóa nghiệp vụ của study: `persist` tìm theo tham số `study-instance-uid` (khớp chính xác) trước khi tạo, và mọi URL DICOMweb dùng nó. Bắt buộc, dài tối đa 64, chỉ số và dấu chấm (`uidPattern`) | S3 (UID định danh một ca chụp; liên kết được với ca khác khi lộ) |
| `studyId` | string | không | Study ID (0020,0010), mã ngắn do máy chụp cấp; có chỉ mục `study-id`; QIDO `StudyID` | S2 |
| `studyDate` | string (`YYYY-MM-DD`) | không | Ngày chụp (0008,0020) đổi sang ngày FHIR; chỉ mục `study-date` (khoảng một ngày) phục vụ QIDO `StudyDate` kể cả khoảng `a-b` | S3 |
| `studyTime` | string (`HH:MM:SS`) | không | Giờ chụp (0008,0030); chỉ mục `study-time` kiểu string (không phải date) nên chỉ so khớp chuỗi | S3 |
| `accessionNumber` | string | không | Số tiếp nhận do HIS/RIS cấp (0008,0050): **khóa nối với hệ thống bệnh viện** (y lệnh, kết quả đọc); chỉ mục `accession-number`; QIDO `AccessionNumber` | S3 |
| `instanceAvailability` | string | không | Instance Availability (0008,0056): `ONLINE`, `NEARLINE`, `OFFLINE`, `UNAVAILABLE`; chép từ tệp, **dicomweb không dùng để quyết định gì** | S1 |
| `modalitiesInStudy` | string[] | không | Các modality có trong study (`CT`, `MR`...). **Do dicomweb tính lại**, không lấy từ tệp: `ComputeStudyAggregates` gom `Modality` của mọi series (chuẩn hóa chữ hoa, không trùng, sắp xếp), `updateStudy` ghi sau mỗi STOW. Chỉ mục `modalities` (token) cho QIDO `ModalitiesInStudy` | S1 |
| `referringPhysiciansName` | string | không | Tên bác sĩ chỉ định (0008,0090, PN đổi sang chuỗi `Họ^Tên`). Đọc đúng từ khóa DICOM (Medplum đọc nhầm nên luôn rỗng, ghi ở `study.go`); chỉ mục `referring-physicians-name`, QIDO khớp tiền tố | S2 |
| `timezoneOffsetFromUtc` | string | không | Lệch múi giờ của ngày giờ trong tệp (0008,0201, dạng `+0700`); chỉ chép, không dùng để quy đổi | S1 |
| `patientName` | string | không | Tên bệnh nhân (0010,0010, PN đổi sang chuỗi `Tran^Van A`) **của instance đầu tiên**; chỉ mục `patient-name` (`raw` và `norm` đã bỏ dấu); QIDO `PatientName` khớp tiền tố không phân biệt hoa thường | S3 (PHI trực tiếp) |
| `patientId` | string | không | Mã bệnh nhân (0010,0020), thường là MRN của bệnh viện; chỉ mục `patient-id`; QIDO `PatientID` khớp chính xác. **Không nối với `Patient.id`**: chỉ là chuỗi | S3 (PHI trực tiếp, định danh) |
| `patientBirthDate` | string (`YYYY-MM-DD`) | không | Ngày sinh (0010,0030); **không có tham số tìm kiếm** nên không có dòng chỉ mục (đã đo), chỉ nằm trong JSON và `fhir_history` | S3 |
| `patientSex` | string | không | Giới tính (0010,0040): `M`, `F`, `O`; không có chỉ mục | S3 |
| `numberOfStudyRelatedSeries` | integer | không | Số series của study, **do dicomweb tính** (`len(series)` tìm theo tham chiếu `study`), không lấy từ tệp; ghi bằng `If-Match`, thử tối đa 3 lần (`updateStudy`). Instance không mang trường này nên lúc tạo là rỗng, tới cuối yêu cầu mới có | S1 |
| `numberOfStudyRelatedInstances` | integer | không | Số instance của study, **do dicomweb tính** bằng `Search` `Total=accurate` trên `DicomInstance?study=...` (`countInstances`); idempotent (tính lại từ dữ liệu đang lưu chứ không cộng dồn); mỗi lần đổi là một phiên bản study mới | S1 |

**Tham số tìm kiếm được lập chỉ mục** (9, từ `searchparams_gen.go`): `study-instance-uid`, `study-id`, `accession-number`, `patient-id`, `patient-name`, `referring-physicians-name`, `study-time` (string); `study-date` (date); `modalities` (token). Cộng hai tham số chung của mọi resource: `_project` (token) và `_lastUpdated` (date); QIDO sắp `_sort=-_lastUpdated` nên study mới cập nhật đứng đầu. **Không có** chỉ mục cho `patientBirthDate`, `patientSex`, `numberOfStudyRelated*`, `instanceAvailability`, `timezoneOffsetFromUtc`.

## 2.3 DicomSeries

**Mục đích nghiệp vụ:** một chuỗi ảnh trong study (một lần quét, một sequence MR); cấp trung gian giữa study và instance, và cấp mà QIDO series trả về (kèm thuộc tính của study cha). **Lưu ở:** `fhir_resource` (`resource_type='DicomSeries'`). **Ghi bởi:** `dicomstow` lúc instance đầu tiên của series đến (`SeriesFromDataset("DicomStudy/<id>", ds)`), rồi `updateSeries` ghi lại số instance. **Đọc bởi:** QIDO, WADO-RS (lấy danh sách instance), `fhir-server`. **Xóa bởi:** không có đường xóa trong dicomweb.

| Trường | Kiểu | Bắt buộc | Chức năng | Nhạy cảm |
|---|---|---|---|---|
| `study` | Reference(DicomStudy) | có | Study chứa series, dạng `DicomStudy/<uuid>`: **quan hệ cha con duy nhất lưu được**; chỉ mục `study` (reference) dùng cho `updateStudy` (liệt kê series của study) và QIDO series theo study. Toàn vẹn do `persist` (tạo study trước trong cùng transaction) | S1 |
| `seriesInstanceUid` | string | có | Series Instance UID (0020,000E); `persist` tìm theo `series-instance-uid` (khớp chính xác) trước khi tạo; **không kiểm series thuộc đúng study** nếu cùng UID đến với study khác (xem [03](03-quan-he-va-toan-ven.md)) | S3 |
| `seriesNumber` | string | không | Series Number (0020,0011) đổi thành chuỗi số nguyên (`strconv.Itoa`); chỉ mục `series-number` (string); QIDO `SeriesNumber` | S1 |
| `modality` | string | không | Modality (0008,0060): `CT`, `MR`, `US`...; nguồn để tính `modalitiesInStudy`. **Không có tham số tìm kiếm**: QIDO `Modality` ở cấp series phải **lọc sau khi đọc** (tối đa 10.000 series, `search.go`) | S1 |
| `timezoneOffsetFromUtc` | string | không | Lệch múi giờ (0008,0201), chỉ chép | S1 |
| `seriesDescription` | string | không | Mô tả series (0008,103E), văn bản tự do do kỹ thuật viên nhập (ví dụ "CT NGUC CO THUOC"); không có chỉ mục. **Có thể chứa thông tin cá nhân** do người nhập | S3 |
| `numberOfSeriesRelatedInstances` | integer | không | Số instance của series, **do dicomweb tính** (`countInstances` theo tham chiếu `series`), ghi bằng `If-Match`; mỗi lần đổi là một phiên bản series mới | S1 |
| `performedProcedureStepStartDate` | string | không | Ngày bắt đầu thủ thuật (0040,0244) dạng FHIR; có chỉ mục `performed-procedure-step-start-date` kiểu **string** (không phải date) | S3 |
| `performedProcedureStepStartTime` | string | không | Giờ bắt đầu thủ thuật (0040,0245); chỉ mục kiểu string | S3 |
| `scheduledProcedureStepId` | string | không | **dicomweb không bao giờ ghi** (`SeriesFromDataset` không đặt trường này; đã grep toàn repo ngoài `pkg/fhir`): luôn rỗng, chỉ mục không có dòng. Tồn tại vì mô hình Medplum | S1 |
| `requestedProcedureId` | string | không | **dicomweb không bao giờ ghi**, như trên | S1 |

**Tham số tìm kiếm được lập chỉ mục** (7): `study` (reference → `DicomStudy`), `series-instance-uid`, `series-number`, `performed-procedure-step-start-date`, `performed-procedure-step-start-time`, `requested-procedure-id`, `scheduled-procedure-step-id` (string), cộng `_project` và `_lastUpdated`. Hai tham số cuối không bao giờ có dòng.

## 2.4 DicomInstance

**Mục đích nghiệp vụ:** một đối tượng DICOM (một ảnh, một báo cáo có cấu trúc); **chứa metadata đã làm sạch và con trỏ tới nội dung tệp**. Là đơn vị lớn nhất về số lượng. **Lưu ở:** `fhir_resource` (`resource_type='DicomInstance'`). **Ghi bởi:** `dicomstow` (`persist`, tạo), `dicomretrieve.BuildPixelData` (đặt `pixelData`, mỗi lần một phiên bản mới), `dicomretrieve.Reindex` (đặt lại extension chỉ mục frame). **Đọc bởi:** QIDO cấp instance (trả nguyên `metadata`), WADO-RS (metadata, frame, rendered, bulkdata, tải tệp), `fhir-server`. **Xóa bởi:** không có đường xóa trong dicomweb; **xóa instance không xóa `Binary`, nội dung hay frame của nó** ([03](03-quan-he-va-toan-ven.md) mục 3.4).

| Trường | Kiểu | Bắt buộc | Chức năng | Nhạy cảm |
|---|---|---|---|---|
| `study` | Reference(DicomStudy) | có | Study cha (`DicomStudy/<uuid>`); chỉ mục `study`; `countInstances` đếm instance của study qua chỉ mục này; QIDO instance theo study | S1 |
| `series` | Reference(DicomSeries) | có | Series cha (`DicomSeries/<uuid>`); chỉ mục `series`; QIDO instance theo series và `countInstances` của series | S1 |
| `sopClassUid` | string | có | SOP Class UID (0008,0016): loại đối tượng (`1.2.840.10008.5.1.4.1.1.2` là CT Image); thiếu thì lấy từ Media Storage SOP Class (0002,0002). Chỉ mục `sop-class-uid` | S1 |
| `sopInstanceUid` | string | có | SOP Instance UID (0008,0018): **khóa nghiệp vụ và khóa idempotent của STOW**: tìm theo `sop-instance-uid` trước khi ghi nội dung, có rồi thì trả thành công mà không lưu lại (`AlreadyStored`). Chỉ mục `sop-instance-uid` | S3 |
| `instanceAvailability` | string | không | (0008,0056) chép từ tệp; không dùng | S1 |
| `timezoneOffsetFromUtc` | string | không | (0008,0201) chép từ tệp; không dùng | S1 |
| `instanceNumber` | string | không | Instance Number (0020,0013) dạng chuỗi số; **mặc định `"1"` khi tệp không có** (`persist`), nên không phân biệt được "không có" với "bằng 1". Chỉ mục `instance-number` | S1 |
| `rows` | integer | không | Rows (0028,0010): chiều cao ảnh; **dùng để quyết định instance có ảnh hay không**: thiếu `rows`/`columns` thì WADO frame trả 404 "không có pixel data" và `pixeldata`/`reindex` bỏ qua (`NoPixels`) | S1 |
| `columns` | integer | không | Columns (0028,0011): chiều rộng ảnh; xem `rows` | S1 |
| `bitsAllocated` | integer | không | Bits Allocated (0028,0100): 8, 16, 32; `rendered` dùng để chọn cách dựng ảnh | S1 |
| `numberOfFrames` | integer | không | Number of Frames (0028,0008); thiếu nghĩa là 1 (`framesOf`). So khớp với số frame trong chỉ mục (`indexMatches`): lệch thì chỉ mục bị bỏ và WADO quét tệp | S1 |
| `metadata` | string (JSON) | có | **Header DICOM đã làm sạch, ở dạng DICOM JSON, lồng trong JSON như một chuỗi** (`json.Marshal(CleanDataset(ds))`). Bỏ: nhóm 0002, PixelData, overlay (60xx,3000), phần tử nhị phân lớn hơn 10 KB; nhị phân nhỏ thành `InlineBinary`. **Chứa mọi phần tử còn lại của tệp**: tên và mã bệnh nhân, ngày sinh, tên bác sĩ, tên cơ sở, tên người vận hành, ghi chú... (đã đo: `00100010`, `00100020`, `00100030` đều có trong `metadata`). QIDO instance và WADO metadata trả chuỗi này **nguyên văn**. Mỗi phiên bản `DicomInstance` trong `fhir_history` **chép lại cả chuỗi** | S3 (PHI trực tiếp, nhiều nhất) |
| `raw` | Reference(Binary) | có | `Binary/<uuid>` giữ nội dung tệp DICOM Part 10 nguyên văn ở kho Binary; mọi đường đọc ảnh (frame, rendered, bulkdata, tải tệp, WADO-URI) đi qua đây rồi dựng khóa `binary/<id>/<versionId>` từ **phiên bản hiện tại** của Binary (`rawKey`). Không có chỉ mục (không có tham số tìm kiếm) | S1 (con trỏ; nội dung đích là S3) |
| `pixelData` | Reference(Binary)[] | không | **Tùy chọn** (`DICOMWEB_PIXELDATA_FRAMES` hoặc lệnh `dicomweb pixeldata`): danh sách `Binary/<uuid>`, **một Binary riêng cho mỗi frame**, theo thứ tự frame, để client đọc thẳng qua API FHIR như Medplum. WADO-RS không dùng. Nhân đôi dung lượng ảnh. Ghi bằng `If-Match` theo phiên bản đã đọc | S1 (con trỏ; nội dung đích là S3) |

**Extension `https://trivita.ai/fhir/StructureDefinition/dicom-frame-index`** (`valueString`, JSON `{"v":1,"ts":"<transfer syntax>","f":[[offset,length],...]}`): chỉ mục vị trí từng frame trong tệp đã lưu, lập một lần lúc STOW (ảnh không nén: từ header; ảnh đóng gói: đọc fragment song song với việc ghi). WADO-RS dùng nó để đọc **đúng khoảng byte** của một frame (`OpenRange`) thay vì quét tệp. Tối đa 256 KB mã hóa, tối đa 100.000 frame; vượt hoặc hỏng thì không lưu (hoặc bị bỏ) và WADO quay về quét. Ví dụ đã đo: `{"v":1,"ts":"1.2.840.10008.1.2.1","f":[[716,128]]}`. Chỉ chứa vị trí byte, không chứa dữ liệu bệnh nhân. Nằm trong `extension` nên **không có dòng riêng trong `fhir_idx_*`**.

**Tham số tìm kiếm được lập chỉ mục** (5): `study` (reference → `DicomStudy`), `series` (reference → `DicomSeries`), `sop-class-uid`, `sop-instance-uid`, `instance-number` (string), cộng `_project` và `_lastUpdated`. Đã đo: một instance mới có 7 dòng chỉ mục. **Không có** chỉ mục cho `metadata`, `rows`, `columns`, `raw`, `pixelData`.

## 2.5 Binary (nội dung DICOM và frame)

**Mục đích nghiệp vụ:** một dòng `Binary` là **phiếu lưu trữ** của một đối tượng ở kho (`file:` hoặc `s3:`): nội dung không nằm trong PostgreSQL. dicomweb tạo `Binary` theo hai cách: (1) một Binary mỗi instance cho tệp DICOM nguyên văn (`ContentType: application/dicom`), (2) tùy chọn một Binary mỗi frame (`pixelData`). **Lưu ở:** `fhir_resource` (`resource_type='Binary'`), nội dung ở kho khóa `binary/<id>/<versionId>` với `versionId` của **phiên bản đầu tiên**. **Quan trọng:** dicomweb dựng repo bằng `fhirauth.Scope(store)` **không bọc `fhirbinary.Wrap`** (`container.go`), nên Binary do dicomweb ghi không có `data` (luôn rỗng) và việc ghi nội dung là việc của mã STOW chứ không phải của lớp bọc: lớp bọc chỉ có ở `fhir-server`, và nó là nơi **Expunge Binary xóa luôn nội dung** (xem [03](03-quan-he-va-toan-ven.md) mục 3.4). **Ghi bởi:** `dicomstow` (instance), `dicomretrieve.putFrame` (frame). **Đọc bởi:** dicomweb (`rawKey`, đọc nội dung từ kho), `fhir-server` (`GET Binary/{id}`, `/storage`). **Xóa bởi:** `dropBinary` và `dropFrameBinary` của dicomweb khi **lỗi giữa chừng** (xóa nội dung rồi **xóa mềm** dòng Binary); không có xóa theo vòng đời.

| Trường | Kiểu | Bắt buộc | Chức năng | Nhạy cảm |
|---|---|---|---|---|
| `contentType` | string | có | `application/dicom` cho tệp nguyên văn; cho frame là loại theo transfer syntax (`application/octet-stream`, `image/jpeg`, `image/jp2`... `dicomweb.FrameMediaType`) | S1 |
| `securityContext` | Reference(DicomInstance) | không | Trỏ về `DicomInstance/<id>` (đặt cả cho tệp nguyên văn và frame). **Cơ chế phân quyền của `fhir-server`**: `GET Binary/{id}` chỉ trả khi người gọi đọc được `DicomInstance` này (`canReadSecurityContext`); instance đã xóa mềm thì đọc trả 410 nên **Binary trở nên không đọc được qua API dù nội dung vẫn còn ở kho** | S1 |
| `data` | string (base64) | không | **Luôn rỗng ở Binary do dicomweb ghi**: nội dung luôn ở kho | S3 (nếu có; ở đây không có) |
| `url` | string | không | dicomweb không đặt trường này (nội dung luôn ở kho, không phải URL ngoài); luôn rỗng | S1 |
| `meta.project` | string | có | Project của người STOW (do `fhirauth.Scope` ghi đè theo token): project khác nhận 404. Binary frame do lệnh `pixeldata` quét bù (kho chưa tách project) giữ project của instance (`putFrame`) | S1 |

**Tham số tìm kiếm được lập chỉ mục:** không có riêng (chỉ `_project`, `_lastUpdated` chung). Không thể liệt kê "Binary không thuộc instance nào" bằng chỉ mục (không có tham số trên `securityContext`).

## 2.6 Ghi chú chung cho bốn loại

| Chủ đề | Nội dung |
|---|---|
| `id` | UUID v4 sinh ở dicomweb (`newID`), không lấy từ UID DICOM. UID DICOM nằm ở trường `...Uid` và được tìm qua chỉ mục |
| `meta.versionId` | UUID do `fhirstore` cấp mỗi lần ghi; `If-Match` dùng nó (cập nhật số liệu tổng hợp, `pixelData`, chỉ mục frame) |
| `meta.project` | Chuỗi id project (không phải `Reference`) do `fhirauth.Scope` ghi đè theo token lúc ghi; được **lập chỉ mục token `_project`** (đã đo). Mọi truy cập của người dùng đi qua `Scope` nên chỉ thấy project của mình |
| `meta.author` | **Không ai đặt** (đã grep `pkg/dicomstow`, `pkg/dicomretrieve`, `apps/dicomweb`, `pkg/fhirauth`, `pkg/fhirstore`: không có `Author`): không có dấu vết **ai** đã tải instance nào lên; chỉ có `meta.lastUpdated` và `meta.project` |
| `Patient`, `ImagingStudy` | **Không dùng** (mục 2.1) |
| Dữ liệu ngoài phạm vi | PixelData không nằm trong `metadata`; nằm ở tệp nguyên văn trong kho (S3) |
