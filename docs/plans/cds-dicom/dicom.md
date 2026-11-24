# Kế hoạch port DICOMweb

Nguồn Medplum (v5.2.18, `~/Downloads/medplum/packages/server/src`): `dicom/` gồm `routes.ts` (43 dòng), `stow-rs.ts` (238), `qido-rs.ts` (78), `wado-rs.ts` (164), `utils.ts` (411), `README.md`, test (`routes.test.ts` 32 ca, `utils.test.ts` 19 ca); worker xử lý pixel data `workers/dicom.ts` (246) và `workers/dicom.test.ts`. Phụ thuộc: `dcmjs` 0.52.3 (đọc DICOM và chuyển DICOM JSON), `dicomweb-multipart-parser`, kho Binary (`getBinaryStorage`, `uploadBinaryData`), hàng đợi BullMQ. Tổng quan và quyết định chung: [README.md](README.md).

## 1. Hiện trạng và phạm vi

Medplum chỉ cài **một tập con** DICOMweb (`dicom/README.md`); mọi route còn lại là `notImplementedStub` trả **`200` với body rỗng** (`routes.ts:15-43`), tức một client có thể tưởng đã thành công. Bản Go **không có gì**: không có route, không có thư viện đọc DICOM, và không có kho đối tượng cho Binary (`fhir.Binary` hiện chỉ giữ `data` base64 nội tuyến trong CSDL; thư mục `storage/` ở gốc repo rỗng; cấu hình `STORAGE_PATH` trong `Dockerfile` là di sản của các app đã tách ra, không có mã Go dùng).

Trong repo Go đã có sẵn: kiểu `DicomStudy`, `DicomSeries`, `DicomInstance` (`pkg/fhir/resource_dicom*.go`; `DicomInstance` có `raw`, `pixelData[]`, `metadata`), các search parameter `study-instance-uid`, `series-instance-uid`, `sop-instance-uid`, `study`, `series` (`searchparams_gen.go`), kho `pkg/fhirstore`, xác thực `pkg/fhirauth` (`Verifier`, `Principal`, `Scope(repo)` cô lập project), hàng đợi `pkg/workflow` (asynq, Temporal).

| Nhóm | Route Medplum đã cài | Tình trạng khác |
|---|---|---|
| STOW-RS | `POST /studies`, `POST /studies/{studyUid}` | |
| QIDO-RS | `GET /studies` (chỉ `limit`, `offset`, **không lọc**), `GET /studies/{studyUid}/series` | các cấp instance, bộ lọc, `includefield`: chưa có |
| WADO-RS | `GET .../series/{seriesUid}/metadata`, `GET .../instances/{instanceUid}/frames/{frame}` (một frame) | `rendered`, `instances`, instance, study metadata, bulkdata: stub 200 rỗng |
| Worker | tách pixel data sau khi lưu instance | |

Tiền tố URL: `/dicomweb`, `/api/dicomweb`, `/projects/{projectId}/dicomweb`, `/api/projects/{projectId}/dicomweb` (`app.ts:223-226`); có ca test "Cannot use another project scope".

## 2. Luồng và hành vi cần giữ

**STOW-RS** (`stow-rs.ts`):
1. Phải là `multipart/related`, không thì `415 {"error":"Expected multipart/related"}`.
2. Mỗi phần (part) được **tách đôi luồng**: một luồng ghi thẳng vào kho Binary (`contentType application/dicom`, tên `instance.dcm`, `securityContext = DicomInstance/<id>`), một luồng cho bộ đọc DICOM chỉ đọc **100 KB đầu** để lấy metadata (`untilOffset: 100*1024`). Id instance được **đặt trước** (`repo.generateId()`).
3. `cleanDicomJsonDict`: bỏ nhóm 0002, `PixelData` (7FE00010) và overlay (60xx3000); phần tử nhị phân VR `OB/OD/OF/OL/OV/OW/UN` lớn hơn **10 KB** bị bỏ, nhỏ hơn thì thành `InlineBinary` base64; đệ quy vào `SQ` (`utils.ts:238-320`).
4. Tuần tự từng instance (do mỗi lần ghi mở transaction): `conditionalCreate` `DicomStudy` theo `study-instance-uid`, `DicomSeries` theo `series-instance-uid`, rồi `DicomInstance` theo `sop-instance-uid` với `assignedId` (**tải lại cùng instance không tạo bản trùng**, `routes.test.ts:472`). `instanceNumber` mặc định `"1"`.
5. Sau khi lưu hết: cập nhật số liệu tổng hợp của từng study (`modalitiesInStudy` đã sắp xếp, số series, số instance) và series (số instance) bằng đọc-sửa-ghi **có điều kiện, thử tối đa 3 lần**, lỗi chỉ ghi log (instance đã lưu, lần tải sau tính lại) (`utils.ts:73-203`).
6. Phản hồi `200` JSON (`ReferencedSOPSequence` kèm `RetrieveURL`); lỗi xử lý thì `400` văn bản `Error processing DICOM upload`.

**QIDO-RS**: `GET /studies` trả mảng DICOM JSON (`application/dicom+json`) của mọi `DicomStudy` (có `limit`, `offset`); `GET /studies/{uid}/series` trả `404 {"error":"Study not found"}` nếu không có study, còn lại mảng series (mỗi phần tử gộp thuộc tính study + series) (`qido-rs.ts`). Chuyển đổi: ngày `YYYYMMDD` ⇄ `YYYY-MM-DD`, giờ `HHMMSS` ⇄ `HH:MM:SS`, tên người (PN) chỉ lấy dạng `Alphabetic` đầu tiên (`utils.ts:321-372`).

**WADO-RS**: metadata series trả mảng `JSON.parse(instance.metadata)` (tối đa 1000 instance); frame: kiểm `frame` là số nguyên ≥ 1 (400), study/series/instance tồn tại (404), instance có `pixelData` (404), `frame ≤ số phần tử pixelData` (416), rồi đọc Binary frame và trả `multipart/related; type=<contentType>; boundary=medplum-<ms>` (`wado-rs.ts`).

**Worker pixel data** (`workers/dicom.ts`): kích hoạt khi `DicomInstance` được tạo hoặc `raw` đổi (`addDicomJobs`, gắn ở lớp ghi của repository, nên cả ghi qua FHIR API cũng kích hoạt); hàng đợi `DicomQueue`, thử lại 3 lần, lùi theo cấp số nhân từ 1 giây. Đọc `raw` đầy đủ, lấy `PixelData`, **mỗi `ArrayBuffer` thành một Binary** (`contentType` theo transfer syntax: JPEG `4.50/4.57/4.70`, JPEG 2000 `4.90/4.91`, JPEG XL `4.201/4.202`, còn lại `application/octet-stream`), rồi patch `pixelData` (và `meta.author = system`).

## 3. Lệch và lỗi của Medplum nên sửa khi port

| # | Điều quan sát | Nguồn | Đề xuất cho bản Go |
|---|---|---|---|
| 1 | Route chưa cài trả `200` rỗng | `routes.ts:35-42` | Trả `501 Not Implemented` (JSON `{"error"}`) |
| 2 | `dcmjsStudyToMedplumStudy` đọc `ReferringPhysiciansName` (thừa chữ `s`), trong khi chiều ngược lại ghi `ReferringPhysicianName` (đúng từ khóa DICOM) | `utils.ts:21-40` và `52-69` | Đọc đúng `ReferringPhysicianName`; hiện bác sĩ chỉ định không bao giờ được lưu. **Chưa xác nhận** tên khóa do `dcmjs` sinh, cần thử với một tệp mẫu |
| 3 | `RetrieveURL` trong phản hồi STOW là `/instances/{id}/raw`, không có route nào như vậy | `stow-rs.ts` (`sendStowResponse`) | Trả URL WADO-RS thật; đủ `ReferencedSOPSequence` và `FailedSOPSequence`, mã `200`/`202`/`409` theo chuẩn (Q-D6) |
| 4 | Phản hồi STOW không có `Content-Type: application/dicom+json` | như trên | Đặt đúng |
| 5 | `pixelData` mỗi phần tử là một `ArrayBuffer` của `PixelData`; với ảnh không nén nhiều frame, dcmjs có thể trả một khối duy nhất nên "frame N" sai | `workers/dicom.ts` (`processPixelData`) | **Chưa xác nhận.** Nếu đúng, tách frame theo `Rows × Columns × SamplesPerPixel × BitsAllocated/8`; nén đóng gói thì mỗi fragment/frame một Binary |
| 6 | Frame được đọc hết vào bộ nhớ rồi mới gửi | `wado-rs.ts` | Truyền luồng |
| 7 | Metadata chỉ lấy từ 100 KB đầu | `stow-rs.ts` | Giữ giới hạn, nhưng ghi log khi bị cắt |
| 8 | Lỗi worker chỉ ghi log mức info | `workers/dicom.ts` | Log error + metric; cần chỗ báo instance chưa có pixelData |

## 4. Quyết định kiến trúc

**Chỗ đặt: ứng dụng riêng `apps/dicomweb`** (khuyến nghị), không nhét vào `fhir-server`, vì: (a) tải lên có thể hàng trăm MB đến GB, cần truyền luồng và không bị `BodyLimit("16M")` của `fhir-server`; (b) co giãn và timeout khác hẳn; (c) rủi ro nhị phân lớn tách khỏi API FHIR. Dùng chung CSDL clinical với `fhir-server` qua `pkg/fhirstore`, xác thực bằng `pkg/fhirauth.Verifier` (token do `auth` cấp, claim `fhir_project`) và cô lập project bằng `fhirauth.Scope(repo)`. Quyền: STOW cần quyền ghi (`fhirauth.Role` write) và `Grants.CanWrite` cho `DicomStudy`, `DicomSeries`, `DicomInstance`, `Binary`; QIDO/WADO cần quyền đọc tương ứng (Q-D1).

Cần dựng trước (xem README.md): `pkg/binarystore` (ghi/đọc/xóa luồng, adapter hệ thống tệp trước, S3 sau), helper đọc-sửa-ghi có điều kiện dùng chung (`auth` đã có `Mutate`, nên chuyển lên một gói chung), chọn thư viện đọc DICOM.

**Thư viện đọc DICOM (ngoài source, cần xác nhận ở bước D0):** ứng viên `github.com/suyashkumar/dicom` (Go thuần, MIT; **chưa kiểm** khả năng dừng đọc sớm ở 100 KB, đọc luồng, hay xử lý transfer syntax nén). Cách chắc chắn nhất: bước D0 là một spike dùng 3 đến 4 tệp mẫu thật (không nén, JPEG baseline, JPEG 2000, nhiều frame) để đo.
Phân tích `multipart/related`: `mime/multipart` của stdlib đọc được mọi subtype multipart (cần thử với `type=application/dicom`, boundary có dấu ngoặc kép).

## 5. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành |
|---|---|---|---|
| D0 | Spike: thư viện DICOM (đọc 100 KB đầu, luồng, tách PixelData, nhiều frame), `multipart/related` bằng stdlib; chốt Q-D1 đến Q-D4; dựng `pkg/binarystore` (giao diện + hệ thống tệp, có test) | M | Báo cáo spike + `binarystore` có test; quyết định ghi vào README |
| D1 | Khung `apps/dicomweb`: cấu hình, health, swagger, xác thực `Verifier` + `Scope`, tiền tố URL, lỗi dạng JSON `{"error"}`, test kiến trúc | S | Chạy được, 401/403 đúng, cô lập project có test |
| D2 | `pkg/dicomweb` (thuần): chuyển đổi ngày/giờ/PN, `cleanDicomJsonDict`, ánh xạ study/series ⇄ DICOM JSON | M | Port `utils.test.ts`; sửa lỗi #2 (có test mẫu) |
| D3 | STOW-RS: nhận multipart, tách luồng (ghi Binary + đọc metadata), `conditionalCreate` Study/Series/Instance (khóa theo UID để tránh trùng khi tải song song), số liệu tổng hợp có thử lại, phản hồi đúng chuẩn | L | Các ca STOW của `routes.test.ts:359-519` (kể cả nhiều instance, tải lại không trùng) xanh |
| D4 | QIDO-RS (`/studies`, `/studies/{uid}/series`) + WADO-RS (metadata series, frame truyền luồng) | M | Ca `routes.test.ts:130-352` xanh; 501 cho route chưa cài |
| D5 | Worker pixel data (asynq trong `pkg/workflow`): bảng content-type theo transfer syntax, thử lại, tách frame đúng (#5), kích hoạt từ STOW (và từ hook ghi nếu làm) | M | Ca `workers/dicom.test.ts`; frame trả về đúng nội dung với tệp mẫu nhiều frame |
| D6 | Tích hợp: docker/compose, `docs/api-contract`, e2e `auth` → STOW → QIDO → WADO bằng tệp mẫu thật; thử với viewer OHIF nếu có | S đến M | e2e xanh |
| D7 | (Vượt parity, tùy chọn) bộ lọc QIDO, cấp instance, `rendered`, study metadata, bulkdata, WADO-URI | L | Ngoài kế hoạch; Medplum cũng chưa có |

Cỡ tổng D0 đến D6: **L** (khoảng 2 đến 3 tuần cho một người), rủi ro lớn nhất ở D0 (thư viện DICOM) và D5.

## 6. Ca test

Nguồn: `dicom/routes.test.ts` (32 ca: studies, tiền tố `/api` và theo project, project khác, series, metadata, frame với 6 lỗi, bulk, STOW một và nhiều instance, tải lại, lỗi xác thực đầu vào) và `utils.test.ts` (19 ca), `workers/dicom.test.ts` (15 ca). Port theo nhóm tương ứng các bước D2 đến D5. Ca bỏ: các ca phụ thuộc `Bot`/`$deploy` không có; ca stub (200 rỗng) đổi sang kỳ vọng 501. Ca mới cần có: tệp DICOM thật (không nén, nén, nhiều frame), tải trùng đồng thời, tệp hỏng, multipart sai, tệp rất lớn (kiểm không đọc hết vào bộ nhớ), cô lập project, quyền (token chỉ đọc không được STOW).

## 7. Bảo mật và dữ liệu nhạy cảm

- **Dữ liệu y tế có định danh**: `metadata` chứa tên và mã bệnh nhân; `DicomStudy` có `patientName`, `patientId`, `patientBirthDate`. Không ghi vào log (Medplum cũng không). Cô lập project và quyền theo loại resource là bắt buộc.
- Tệp DICOM là đầu vào không tin cậy: giới hạn kích thước mỗi phần và tổng, giới hạn độ sâu `SQ`, thời gian đọc; tệp hỏng không được làm sập tiến trình.
- Kho Binary: không đặt đường dẫn theo dữ liệu người dùng (chống `..`), quyền thư mục, xóa khi Binary bị xóa.
- `securityContext = DicomInstance/<id>`: Medplum dùng để suy quyền đọc Binary từ instance; bản Go phải có cơ chế tương đương khi Binary không còn nằm trong CSDL.

## 8. Không port đợt này

WADO-URI; `rendered`; bộ lọc QIDO; bulkdata; tích hợp OHIF phía server ngoài các route đã nêu (Medplum README chỉ nhắc tới).

## 9. Quyết định đã chốt và câu hỏi mở

Chốt 2026-11-24: H2 = app riêng `apps/dicomweb`; H4 = `pkg/binarystore` (tệp + S3, đã có); H5 = sửa các lỗi của Medplum ở mục 3 (kéo theo Q-D6 = theo chuẩn). Q-D2 coi như xong (tệp và S3). **D0 xong 2026-11-24**: báo cáo [d0-spike.md](d0-spike.md): Q-D3 = `suyashkumar/dicom` v1.1.0 với `SkipPixelData` + bộ quét luồng riêng + chốt chặn SQ lồng sâu (tệp 60 KB làm thư viện dùng 2,2 GB) và tệp cắt giữa PixelData. **D1 xong 2026-11-24**: khung `apps/dicomweb` (cấu hình, probe, xác thực JWT, cô lập project, bốn tiền tố, giới hạn body, lỗi `{"error"}`, swagger, Dockerfile, test), mọi route DICOMweb trả 501. Quyết định kèm theo: **D-DW-1** dicomweb dùng chung schema `fhir` và role ứng dụng của fhir-server, không tự migrate ([architecture.md](../../../apps/dicomweb/docs/architecture.md)). Bước tiếp theo: D2 (`pkg/dicomweb`).


| # | Câu hỏi | Khuyến nghị |
|---|---|---|
| Q-D1 | Ánh xạ quyền token sang DICOM: STOW cần `fhir.write`, QIDO/WADO cần `fhir.read`, cộng `Grants` theo loại `Dicom*` và `Binary` | Có |
| Q-D2 | Kho đối tượng cho Binary: hệ thống tệp (dev) và S3 (prod)? Dung lượng dự kiến | Giao diện + tệp trước; S3 khi có nhu cầu |
| Q-D3 | Chọn thư viện DICOM sau spike D0 (`suyashkumar/dicom` hay khác) | **Chốt:** `suyashkumar/dicom` v1.1.0 + `SkipPixelData` + bộ quét riêng (d0-spike.md) |
| Q-D4 | Binary của DICOM có lưu qua cùng `fhir.Binary` (id, `securityContext`) hay mô hình riêng | Giữ `fhir.Binary` (metadata trong CSDL, nội dung ở `binarystore`) để tương thích Medplum |
| Q-D5 | Có cần ghi `pixelData` khi ghi `DicomInstance` qua FHIR API (hook ghi) hay chỉ từ STOW | Chỉ STOW trước |
| Q-D6 | Phản hồi STOW lỗi một phần: theo chuẩn (`FailedSOPSequence`, 202/409) hay giữ hành vi Medplum (400 toàn bộ) | Theo chuẩn |
| Q-D7 | Tiền tố `/projects/{projectId}/dicomweb`: giữ để tương thích client Medplum hay chỉ dùng claim `fhir_project` | Giữ nếu client cần; tiền tố phải khớp project của token (có ca test) |
| Q-D8 | Giới hạn kích thước tải lên và thời gian | Cấu hình được, mặc định thận trọng |
