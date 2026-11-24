# Báo cáo spike D0: thư viện DICOM và đường STOW

Ngày 2026-11-24. Mã và bộ sinh tệp mẫu: [tools/dicom-spike](../../../tools/dicom-spike). Tệp mẫu **tổng hợp** (không dữ liệu bệnh nhân thật), sinh bằng `gen.py` không dùng thư viện DICOM: không nén, đa frame, implicit VR, SQ không rõ độ dài, JPEG baseline và JPEG 2000 đóng gói (frame JPEG thật do Pillow tạo), tệp 120 MB (15 frame 2048×2048 16 bit), tệp hỏng và hai tệp độc (độ dài giả, SQ lồng sâu). Kích thước nhỏ nên mọi thông số bên dưới đo trên máy phát triển (macOS, Go 1.26), chỉ dùng để so sánh tương đối.

## 1. Kết luận (Q-D3)

**Dùng `github.com/suyashkumar/dicom` v1.1.0 (MIT, phát hành 2025-09) để đọc metadata, luôn với `dicom.SkipPixelData()`; tự viết một bộ quét luồng nhỏ để biết vị trí PixelData và tách frame; bọc cả hai bằng các chốt chặn dưới đây.** Không dùng `dicom.Parse` mặc định cho tệp thật.

| Câu hỏi | Kết quả |
|---|---|
| Đọc được các tệp mẫu hợp lệ | Có, cả 8 (explicit, implicit, SQ không rõ độ dài, nén, đa frame). Thư viện tự suy transfer syntax từ meta. |
| Bộ nhớ khi đọc tệp 120 MB | `Parse` mặc định: đỉnh **127 MB** (bằng cỡ tệp, giữ cả PixelData), 930 ms. `SkipPixelData`: đỉnh **1,8 MB**, 19 ms. `SkipProcessingPixelDataValue`: vẫn 127 MB. |
| Đọc đúng 100 KB đầu như Medplum | Được: các phần tử trước PixelData trả bình thường, rồi báo lỗi khi gặp PixelData bị cắt (dùng được, bỏ lỗi đó). Nhưng không cần: `SkipPixelData` qua luồng đầy đủ tốn 2 MB và cho metadata trọn vẹn. |
| Tệp hỏng | Không panic. Cắt giữa header: lỗi, 0 phần tử. Không phải DICOM: lỗi rõ ràng. **Cắt giữa PixelData: không lỗi** khi đọc từ luồng (xem 3.3). |
| Frame | Đóng gói: mỗi frame một `EncapsulatedFrame` (byte JPEG nguyên); không nén: `NativeFrame` đã giải thành số nguyên. Với `SkipPixelData` không có frame (chủ ý). |
| Giấy phép, phụ thuộc | MIT; kéo thêm `golang.org/x/exp` (mới với repo), `golang.org/x/text` (đã có). |

## 2. Cách dùng đã kiểm

**Metadata → DICOM JSON** (`more.go: toJSON`): tag, `RawValueRepresentation` và giá trị đủ để dựng JSON model của PS3.18: PN thành `{"Alphabetic": ...}`, DS/IS thành số, US thành số, nhị phân nhỏ thành `InlineBinary`, SQ đệ quy. Lưu ý API: giá trị SQ là `[]*dicom.SequenceItemValue` (mỗi phần tử `GetValue().([]*Element)`), không phải `[][]*Element`. Quy tắc của Medplum (`cleanDicomJsonDict`) giữ được: bỏ nhóm 0002, PixelData, overlay `60xx3000`, và nhị phân VR lớn hơn 10 KB (phần tử private OB 20 KB mẫu bị bỏ đúng).

**Đường STOW** (`more.go: runStow`): `mime/multipart` đọc luồng phần, `io.MultiWriter` đẩy cùng lúc vào kho (ở đây tệp tạm) và vào `io.Pipe` cho bộ đọc metadata (`ParseUntilEOF` + `SkipPixelData`, rồi xả phần còn lại để không chặn đường ghi). Tệp 120 MB: **338 ms, đỉnh 2 MB**, nhận đủ 125 829 934 byte và 34 phần tử metadata.

**Bộ quét luồng** (`scan.go`, khoảng 250 dòng, không phụ thuộc thư viện): đọc một lượt, chỉ cần `io.Reader`, trả offset và độ dài PixelData và khoảng byte từng frame: không nén theo `Rows×Cols×SamplesPerPixel×Bits/8` và `NumberOfFrames`; đóng gói theo Basic Offset Table, hoặc mỗi fragment một frame. Đã kiểm trên cả bảy tệp hợp lệ: 3 frame JPEG đúng biên (`FFD8…FFD9`), tệp 120 MB 15 frame đúng offset, 33 µs. Cần vì `binarystore.Store` chỉ có `Open` (không đọc theo khoảng), nên worker D5 đọc luồng một lần và tách từng frame ra Binary riêng (cách Medplum làm), thay vì đọc lại cả tệp cho mỗi yêu cầu WADO.

**`multipart/related`** bằng stdlib: `mime.ParseMediaType` đọc đúng `type="application/dicom"; boundary="ab c"` (nháy kép, boundary có dấu cách), phần không có `Content-Type` vẫn đọc được. Đáp WADO-RS dựng bằng `multipart.Writer` + `mime.FormatMediaType` (đã kiểm). Giới hạn: **`type=application/dicom` không nháy kép bị stdlib từ chối** (`invalid media parameter`) vì `/` không phải ký tự token; chuẩn PS3.18 bắt buộc nháy kép nên client đúng chuẩn không sao, nhưng nên có đường dự phòng tự rút `boundary=` bằng regex khi `ParseMediaType` lỗi.

## 3. Rủi ro tìm thấy và chốt chặn bắt buộc cho D3/D5

### 3.1 Bom SQ lồng sâu: tệp 60 KB làm thư viện dùng 2,2 GB

SQ không rõ độ dài lồng nhau tốn bộ nhớ **bậc hai** theo độ sâu: 200 lớp 16 MB, 500 lớp 144 MB, 1000 lớp 581 MB, 2000 lớp (tệp 60 KB) **2,2 GB**, và thư viện **không báo lỗi**. Giới hạn 100 KB của Medplum không chặn được vì tệp nhỏ hơn. **Chốt chặn:** trước khi đưa vào thư viện, chạy bộ quét có `maxDepth` (mẫu: 32 mức, tức khoảng 16 lớp SQ; tệp thật lồng không quá 5 đến 6) và từ chối tệp quá sâu bằng 400. Cần bộ đệm phần đầu (đến hết header) để quét trước: giới hạn header (đề xuất 1 MiB, cấu hình được), vì PixelData luôn đứng sau mọi nhóm thấp hơn.

### 3.2 Độ dài khai báo giả

Phần tử private OB khai báo 1 GiB nhưng chỉ có 100 byte: thư viện đọc gia tăng, báo `unexpected EOF`, bộ nhớ gần 0. Không cần chốt chặn riêng ngoài trần kích thước phần và tổng đã có ở kế hoạch (mục 7).

### 3.3 Tệp cắt giữa PixelData không bị phát hiện

Đọc luồng không biết tổng cỡ nên `ParseUntilEOF` coi EOF là kết thúc: tệp thiếu 500 byte PixelData vẫn "hợp lệ" (`Parse` với `bytesToRead` biết trước lại báo lỗi). **Chốt chặn:** sau khi ghi kho, so số byte đã ghi với `PixelOffset + PixelLen` của bộ quét (hoặc với dấu kết thúc Sequence Delimitation ở dạng đóng gói); thiếu thì xóa Binary và trả lỗi (`FailedSOPSequence`, Q-D6).

### 3.4 Giới hạn của bộ quét mẫu (cần làm khi vào `pkg/dicomweb`)

Chưa hỗ trợ Explicit VR Big Endian và Deflated (cả hai đã lỗi thời; từ chối bằng lỗi rõ ràng, thư viện có hỗ trợ nhưng chưa thử); frame đóng gói gồm nhiều fragment mà không có BOT thì không tách được (trả lỗi); nhiều fragment của một frame hiện trả khoảng bao thay vì nối nội dung (cần nối khi ghi frame); `PixelData` có trong SQ (ví dụ IconImageSequence) bị bỏ qua đúng. Chưa thử trên tệp thật có độc quyền nhà sản xuất; nên thêm 1 đến 2 tệp công khai (ví dụ bộ mẫu của pydicom hoặc dcm4che) vào fixture khi có mạng, để bổ sung cho tệp tổng hợp.

## 4. Quyết định ghi lại (Q-D1 đến Q-D4)

| # | Quyết định | Căn cứ |
|---|---|---|
| Q-D1 | STOW cần `fhir.write`, QIDO/WADO cần `fhir.read`, cộng `Grants` theo loại `Dicom*` và `Binary` (theo khuyến nghị) | không đổi kiến trúc `fhirauth` |
| Q-D2 | Đã xong: `pkg/binarystore` tệp + S3 (H4) | S1 đến S7 |
| Q-D3 | `suyashkumar/dicom` v1.1.0 với `SkipPixelData` cho metadata + bộ quét luồng riêng cho frame + chốt chặn 3.1 và 3.3 | mục 1 và 3 |
| Q-D4 | Giữ `fhir.Binary` (metadata trong CSDL, nội dung ở `binarystore`, `securityContext = DicomInstance/<id>`) (theo khuyến nghị) | tương thích Medplum; `fhirbinary` đã sẵn |

Q-D1 và Q-D4 là khuyến nghị cũ được giữ nguyên vì spike không phát hiện lý do đổi; nói để chỉnh nếu bạn muốn khác.

## 5. Việc kế theo

D1 (khung `apps/dicomweb`) có thể bắt đầu ngay. Khi làm D2/D3 mang `scan.go` và `toJSON` vào `pkg/dicomweb` kèm test bằng các tệp do `gen.py` sinh (commit fixture nhỏ vào `testdata`, tệp 120 MB sinh lúc chạy test), và thêm test cho hai tệp độc.
