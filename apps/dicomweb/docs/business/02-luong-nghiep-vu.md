# 2. Luồng nghiệp vụ đầu-cuối

Mã F-xx ở [04-chuc-nang.md](04-chuc-nang.md), BR-xx ở [05-luat-va-phan-quyen.md](05-luat-va-phan-quyen.md).

```mermaid
flowchart LR
    A["BF-1<br/>Nhận ảnh từ thiết bị"] --> B["BF-2<br/>Tìm và mở ca chụp"]
    B --> C["BF-3<br/>Xem ảnh nhanh<br/>(rendered)"]
    B --> D["BF-4<br/>Tải về nguyên văn<br/>và bulkdata"]
    E["BF-5<br/>Client cũ<br/>(WADO-URI)"] -.-> C
    A --> F["BF-6<br/>Gửi ảnh dạng JSON"]
    G["BF-7<br/>Sửa chỉ mục cho ảnh cũ"] -.-> B
    J["BF-10<br/>Dựng pixelData tùy chọn"] -.-> A
    H["BF-8<br/>Triển khai và tắt êm"] -.-> A
    I["BF-9<br/>Truy cập đa tổ chức"] -.-> A
    I -.-> B
```

---

## BF-1. Nhận ảnh từ thiết bị (STOW-RS tệp)

**Mục tiêu:** tệp DICOM từ máy chụp được lưu an toàn, tìm được ngay, không mất, không trùng.
**Các bên:** thiết bị (A1), dicomweb, CSDL, kho Binary.

```mermaid
sequenceDiagram
    autonumber
    participant M as Thiết bị
    participant D as dicomweb
    participant K as Kho Binary
    participant B as CSDL (schema fhir)
    M->>D: POST /dicomweb/studies (multipart/related, application/dicom) + Bearer
    D->>D: Kiểm token, project, quyền ghi
    loop Mỗi phần (một instance), độc lập nhau
        D->>D: Đọc phần đầu (≤ 1 MiB), quét: SQ ≤ 16 lớp, transfer syntax, UID, kích thước
        D->>D: Đọc metadata (bỏ PixelData)
        alt SOPInstanceUID đã có
            D->>D: Bỏ qua (không ghi lại), tính là thành công
        else Mới
            D->>B: Tạo Binary
            D->>K: Ghi luồng nội dung (đồng thời lập chỉ mục frame)
            D->>D: Kiểm tệp không bị cắt, kích thước khớp
            D->>B: 1 transaction (khóa theo UID): study, series, instance
        end
    end
    D->>B: Tính lại số liệu tổng hợp study và series
    D-->>M: 200 tất cả OK, 202 một phần, 409 không instance nào (FailedSOPSequence + FailureReason)
```

**Rẽ nhánh lỗi từng instance:**

```mermaid
flowchart TD
    I["Một instance"] --> Q{"Kiểm tra"}
    Q -->|"Không phải DICOM, bị cắt giữa PixelData,<br/>SQ quá sâu, UID thiếu/sai, transfer syntax không hỗ trợ"| R1["Từ chối: 0xC000"]
    Q -->|"Vượt DICOMWEB_MAX_INSTANCE_BYTES<br/>hoặc phần đầu > MAX_HEADER_BYTES"| R2["Từ chối: 0xA700"]
    Q -->|"Study khác study trong URL"| R3["Từ chối: 0xA900"]
    Q -->|"Lỗi kho / CSDL"| R4["Từ chối: 0x0110, dọn Binary và nội dung đã ghi"]
    Q -->|OK| OK["Lưu"]
    R1 & R2 & R3 & R4 --> N["Không để lại resource, Binary hay nội dung"]
```

**Lỗi cả yêu cầu:** `415` không phải `multipart/related`; `400` thân hỏng; `403` thiếu quyền ghi; `413` vượt `DICOMWEB_MAX_REQUEST_BYTES` (instance đã xong trước đó **vẫn được giữ**).
**Kết quả:** instance chỉ hiện với người dùng khác khi `DicomInstance` được tạo, tức nội dung đã đủ và đúng. Tải lại cùng instance an toàn (idempotent). **Chức năng:** F-01, F-10, F-13.

---

## BF-2. Tìm và mở một ca chụp (QIDO-RS + WADO-RS metadata)

```mermaid
sequenceDiagram
    autonumber
    actor BS as Bác sĩ
    participant V as Viewer
    participant D as dicomweb
    BS->>V: Nhập mã bệnh nhân, ngày chụp, modality
    V->>D: GET /dicomweb/studies?PatientID=...&StudyDate=a-b
    D-->>V: Danh sách study (DICOM JSON), Warning 299 nếu còn trang sau / tham số bị bỏ qua
    BS->>V: Chọn study
    V->>D: GET /dicomweb/studies/{s}/series
    D-->>V: Danh sách series
    V->>D: GET .../series/{r}/metadata
    D-->>V: Metadata các instance (theo luồng, trang 500)
    V->>D: GET .../frames/{n} (xem BF-3)
```

**Điều BA cần biết:** thuộc tính lọc viết theo từ khóa (`PatientID`) hoặc tag hex 8 chữ số (`00100020`); `PatientName` khớp **đầu chuỗi**, không phân biệt hoa thường; nhiều thuộc tính là AND, nhiều giá trị là OR; tham số lạ **không bị bỏ qua lặng lẽ** mà báo `Warning: 299`; không có kết quả thì `200 []` (không phải 204). **Chức năng:** F-03, F-04.

---

## BF-3. Xem ảnh nhanh (frame, rendered)

```mermaid
flowchart TD
    A["Viewer cần hiển thị ảnh"] --> B{"Viewer tự giải mã được<br/>transfer syntax?"}
    B -->|Có| C["GET .../frames/{n}<br/>(byte gốc, đọc đúng khoảng)"]
    B -->|Không| D["GET .../rendered<br/>Accept: image/jpeg hoặc png"]
    C --> C1{"Có chỉ mục frame?"}
    C1 -->|Có| C2["Đọc đúng khoảng byte từ kho:<br/>chi phí tỉ lệ kích thước frame"]
    C1 -->|"Không (ảnh cũ / chỉ mục hỏng)"| C3["Quét tệp tìm vị trí rồi chép:<br/>chậm hơn nhưng vẫn đúng"]
    D --> D1{"Dựng được?"}
    D1 -->|"JPEG baseline, xám 8/16 bit, RGB 8 bit"| D2["Trả JPEG (mặc định) hoặc PNG<br/>window/level theo metadata hoặc tham số"]
    D1 -->|"JPEG 2000, JPEG-LS, RLE, 12 bit, YBR, bảng màu, 32 bit"| D3["406: không trả ảnh sai"]
    D1 -->|"Frame > 64 MiB"| D4["413"]
    C2 & C3 & D2 --> OK(["Hiển thị"])
```

**Lỗi frame:** `404` không có pixel data (báo cáo có cấu trúc, hoặc nội dung đã bị xóa khỏi kho), `416` số frame vượt `NumberOfFrames` (đánh số từ 1), `403` thiếu quyền đọc `Binary`. Lỗi kiểm hợp lệ luôn xảy ra **trước khi ghi byte nào**; lỗi sau đó (kho đứt giữa chừng) làm server **ngắt kết nối** để viewer thấy phản hồi bị cắt, không nhận ảnh dở trông như hợp lệ. **Chức năng:** F-06, F-07.

---

## BF-4. Tải về nguyên văn và bulkdata

| Nhu cầu | Cách | Hành vi |
|---|---|---|
| Sao lưu / chuyển ca sang hệ khác | `GET /studies/{s}`, `/series/{r}`, `/instances/{i}` | `multipart/related; type="application/dicom"`, mỗi instance một phần là tệp Part 10 **nguyên văn**, theo luồng từng trang 100 instance, không `Content-Length` |
| Client DICOMweb đầy đủ cần pixel | `GET .../instances/{i}/bulkdata/7FE00010` | Không nén: MỘT phần chứa mọi frame nối nhau; nén: mỗi frame một phần. Mọi tag khác `404` |

Metadata **không** chứa `BulkDataURI` (PixelData bị bỏ khỏi metadata), nên client phải biết URL theo quy ước. **Chức năng:** F-05, F-08.

---

## BF-5. Client cũ (WADO-URI)

`GET /dicomweb/wado?requestType=WADO&studyUID=&seriesUID=&objectUID=` cho hệ thống chỉ biết giao thức cũ.

```mermaid
flowchart TD
    A["Request WADO-URI"] --> B{"requestType = WADO<br/>và đủ 3 UID?"}
    B -->|Không| E1["400"]
    B -->|Có| C{"anonymize khác no?"}
    C -->|Có| E2["400: không hỗ trợ anonymize"]
    C -->|Không| D{"contentType / Accept"}
    D -->|"application/dicom"| F["Trả tệp nguyên văn (không multipart)"]
    D -->|"image/jpeg (mặc định) hoặc image/png"| G["Trả ảnh như rendered:<br/>frameNumber, windowCenter/Width, imageQuality"]
    D -->|"khác"| E3["406: chỉ hỗ trợ application/dicom, image/jpeg, image/png"]
```

`rows`, `columns`, `region`, `annotation`, `presentation*`, `transferSyntax`, `charset` bị bỏ qua. **Chức năng:** F-09.

---

## BF-6. Gửi ảnh dạng JSON (STOW metadata)

Dành cho hệ thống tạo ảnh từ dữ liệu có cấu trúc (không có tệp Part 10 sẵn).

```mermaid
flowchart TD
    A["POST multipart/related<br/>phần đầu application/dicom+json"] --> B["Phần đầu: mảng DICOM JSON<br/>(mỗi phần tử một instance)"]
    A --> C["Phần sau: bulkdata,<br/>tham chiếu BulkDataURI khớp Content-Location hoặc cid:"]
    B & C --> D["Mỗi bulkdata ghi ra tệp tạm<br/>(để biết độ dài PixelData)"]
    D --> E["BuildPart10: dựng tệp DICOM<br/>(file meta, thứ tự tag, đệm chẵn,<br/>transfer syntax suy từ Content-Type)"]
    E --> F["Đi đúng đường STOW tệp (BF-1):<br/>kiểm UID, idempotent, chỉ mục frame, tổng hợp"]
    F --> G["Xóa tệp tạm, kể cả khi lỗi"]
```

**Giới hạn:** metadata ≤ 8 MiB (413), bulkdata ngoài PixelData ≤ 16 MiB và chỉ cấp ngoài cùng. Big Endian và Deflated bị từ chối; Implicit VR LE được dựng thành Explicit. Instance hỏng bị từ chối riêng (0xC000, hoặc 0xA700 khi bulkdata vượt trần); JSON hỏng, bulkdata thiếu `Content-Location`/`Content-ID` là lỗi cả yêu cầu (400). **Chức năng:** F-02.

---

## BF-7. Sửa chỉ mục cho ảnh cũ (reindex)

```mermaid
flowchart TD
    S["Phát hiện WADO frame chậm<br/>(ảnh lưu trước D5, chỉ mục hỏng)"] --> D["dicomweb reindex --dry-run<br/>đếm instance cần lập"]
    D --> R{"Chạy?"}
    R -->|"Kho lớn"| L["reindex --limit N<br/>(chia đợt, ngoài giờ cao điểm)"]
    R -->|"Kho nhỏ"| A["reindex"]
    R -->|"Sau khi sửa lỗi bộ lập chỉ mục"| F["reindex --force"]
    L & A & F --> O["In: đã duyệt, lập chỉ mục, đã đủ,<br/>không có ảnh, không lập được, lỗi"]
    O --> X{"lỗi > 0?"}
    X -->|Có| E["Thoát khác 0: xem log, mất nội dung / tệp hỏng,<br/>chạy lại là xong"]
    X -->|Không| OK(["Xong, chạy lại bao nhiêu lần cũng an toàn"])
```

Chạy được khi server đang phục vụ; ghi dùng `If-Match` theo phiên bản đã đọc nên không ghi đè thay đổi song song. **Chức năng:** F-12.

---

## BF-8. Triển khai, khởi động và tắt êm

```mermaid
sequenceDiagram
    autonumber
    participant O as Vận hành
    participant F as fhir-server
    participant D as dicomweb
    O->>F: migrate up (fhir-server sở hữu schema)
    O->>D: Khởi động (cùng DATABASE_URL, FHIR_JWT_*, FHIR_BINARY_STORAGE)
    alt Chưa migrate hoặc cấu hình sai
        D-->>O: Thoát ngay, nêu mọi lỗi một lượt
    else Hợp lệ
        D-->>O: /health/ready 200
    end
    O->>D: SIGTERM (khi nâng cấp)
    D->>D: ready 503, giữ SHUTDOWN_DRAIN_DELAY, chờ yêu cầu đang chạy tới SHUTDOWN_TIMEOUT
    Note over D: STOW chạy dài hơn SHUTDOWN_TIMEOUT bị cắt, client tải lại được vì STOW idempotent. Worker pixelData (nếu bật) ngừng nhận việc, chờ việc đang chạy, bỏ hàng đợi
```

**Chức năng:** F-13.

---

## BF-9. Truy cập đa tổ chức (project)

```mermaid
flowchart TD
    A["Request + Bearer"] --> B{"Token hợp lệ?<br/>(chữ ký JWKS, iss, aud, hạn)"}
    B -->|Không| E1["401 (WWW-Authenticate)"]
    B -->|Có| C{"Có claim project và scope FHIR?"}
    C -->|Không| E2["403 Token không có quyền truy cập FHIR"]
    C -->|Có| D{"URL có /projects/{id}/?"}
    D -->|"Có, khác project token, không phải super-admin"| E3["403 Không được dùng project khác"]
    D -->|"Có và khớp, hoặc không có"| G{"Quyền cho phương thức?<br/>GET đọc, POST ghi"}
    G -->|Thiếu| E4["403 Token không đủ quyền cho thao tác này"]
    G -->|Đủ| H["Xử lý; mọi truy cập kho đi qua Scope(project)"]
```

Super-admin: chọn project bằng URL hoặc `X-Fhir-Project` (hai nơi khác nhau ⇒ `400`); không chọn ⇒ `400`; mọi yêu cầu được ghi nhật ký kiểm toán. **Chức năng:** F-10.

## BF-10. Dựng `DicomInstance.pixelData` (tùy chọn, D11)

**Mục tiêu:** client quen Medplum đọc frame **thẳng qua API FHIR** (`DicomInstance.pixelData` → `Binary/{id}`) mà không cần WADO-RS. **Mặc định tắt** vì mỗi frame lưu hai lần (trong tệp gốc và trong Binary riêng) và WADO-RS không dùng.

```mermaid
sequenceDiagram
    autonumber
    participant M as Thiết bị
    participant D as dicomweb
    participant Q as Hàng đợi trong bộ nhớ (1024)
    participant W as Worker (mặc định 2)
    participant K as Kho Binary
    participant B as CSDL
    M->>D: STOW instance mới (BF-1)
    D-->>M: 200 (không chờ pixelData)
    D->>Q: Đưa vào hàng đợi (chỉ khi cờ bật, instance mới, không tính tải lại)
    alt Hàng đợi đầy hoặc đang tắt
        D->>D: Bỏ qua, log hàng đợi pixelData đầy (chạy pixeldata để bù)
    else Nhận được
        W->>Q: Lấy việc, mang quyền và project của người STOW
        loop Tối đa 3 lần, lùi 1 giây rồi 2 giây
            W->>K: Chép từng frame theo khoảng byte (nối fragment nếu nén)
            W->>B: Tạo Binary mỗi frame (Content-Type theo transfer syntax, securityContext DicomInstance)
            W->>B: Ghi pixelData bằng If-Match theo phiên bản đã đọc
        end
        W->>W: Hết 3 lần: log không dựng được pixelData, lệnh pixeldata sẽ bù
    end
```

```mermaid
flowchart TD
    S["Instance thiếu pixelData<br/>(có từ trước khi bật, hàng đợi đầy,<br/>tiến trình tắt giữa chừng, lỗi sau 3 lần)"] --> D["dicomweb pixeldata --dry-run"]
    D --> R{"Chạy?"}
    R -->|"Bù thiếu"| A["pixeldata"]
    R -->|"Chia đợt"| L["pixeldata --limit N"]
    R -->|"Dựng lại"| F["pixeldata --force<br/>(Binary mới, đổi tham chiếu, rồi xóa Binary cũ)"]
    A & L & F --> O["In: đã duyệt, dựng (số frame), đã đủ, không có ảnh, lỗi<br/>Thoát khác 0 nếu có lỗi"]
```

**Điều BA cần biết:** (1) hàng đợi **không bền**: tắt tiến trình hoặc hàng đợi đầy làm instance thiếu pixelData cho tới khi chạy `dicomweb pixeldata` (lưới an toàn); (2) lỗi giữa chừng (một frame không ghi được, thua tranh chấp phiên bản) **xóa mọi Binary và nội dung đã tạo**, không để rác; (3) xóa `DicomInstance` **không** xóa Binary frame của nó (như Medplum); (4) tải lại tệp khác cùng SOPInstanceUID **không** cập nhật pixelData (instance đã có là idempotent); (5) tắt cờ không xóa pixelData đã dựng; (6) mỗi frame cần một lượt đọc và một lượt ghi kho (S3 tính phí theo request). **Chức năng:** F-14.

---

## Bảng sự kiện → hiệu lực

| Sự kiện | Hiệu lực ở dicomweb |
|---|---|
| Token bị thu hồi ở `auth` | dicomweb chỉ xác minh chữ ký nên token còn dùng được tới khi hết hạn (cùng cửa sổ với `fhir-server`, xem tài liệu `auth` D-13) |
| Xoay khóa ký ở `auth` | JWKS được cache; đã tải khóa thì vẫn xác minh khi `auth` tạm tắt |
| Nội dung bị mất khỏi kho | Frame/tải: 404 hoặc 500 sạch; phản hồi đang ghi dở bị ngắt kết nối |
| Tiến trình chết giữa STOW | Có thể để lại Binary hoặc nội dung mồ côi; `fhir-server sweep-binary` quét nội dung mồ côi |
| Tắt tiến trình khi còn việc pixelData trong hàng đợi | Việc đang chạy được chờ xong trong hạn tắt, phần còn trong hàng đợi **bị bỏ**; `dicomweb pixeldata` bù |
| Xóa `DicomInstance` | Binary frame của pixelData **không** bị xóa theo |
