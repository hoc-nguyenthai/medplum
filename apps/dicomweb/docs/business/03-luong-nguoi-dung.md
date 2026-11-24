# 3. Luồng người dùng

dicomweb **không có giao diện**; "màn hình" ở đây là của viewer hoặc phần mềm thiết bị. Tài liệu ghi **bước người dùng → request → điều họ thấy** để UX của viewer, đội tích hợp và QA cùng một bản đồ. Mã F-xx ở [04-chuc-nang.md](04-chuc-nang.md).

## 3.1 Kỹ thuật viên: đẩy ca chụp lên hệ thống (F-01, F-02)

```mermaid
flowchart TD
    A([Chụp xong]) --> B["Thiết bị / gateway gửi STOW"]
    B --> R{"Kết quả"}
    R -->|"200"| OK["Mọi instance đã lưu<br/>(kèm RetrieveURL để mở lại)"]
    R -->|"202"| P["Một phần: một số instance lỗi<br/>đọc FailedSOPSequence, gửi lại các instance đó"]
    R -->|"409"| F["Không instance nào lưu được<br/>đọc FailureReason từng instance"]
    R -->|"401 / 403"| T["Token sai, thiếu quyền ghi, hoặc khác project"]
    R -->|"413"| L["Chia nhỏ yêu cầu (instance đã xong vẫn được giữ)"]
    R -->|"415"| C["Content-Type phải là multipart/related"]
    R -->|"429"| W["Chờ Retry-After 1 giây rồi gửi lại"]
    P --> B
    F --> X{"FailureReason"}
    X -->|"0xC000 (49152)"| X1["Tệp không phải DICOM / cắt / UID sai / transfer syntax không hỗ trợ: sửa ở thiết bị"]
    X -->|"0xA700 (42752)"| X2["Instance hoặc phần đầu quá lớn: kiểm tệp, hoặc đề nghị tăng trần"]
    X -->|"0xA900"| X3["Instance thuộc study khác study trong URL"]
    X -->|"0x0110"| X4["Lỗi xử lý phía server: gửi lại, báo vận hành nếu lặp"]
```

**Thông điệp cho thiết bị:** gửi lại **an toàn** vì STOW idempotent theo `SOPInstanceUID` (không tạo bản trùng). Mất kết nối giữa chừng: gửi lại toàn bộ.

## 3.2 Bác sĩ: tìm và đọc phim (F-03..F-08)

```mermaid
flowchart TD
    A([Mở viewer]) --> B["Màn: danh sách ca<br/>lọc theo mã BN, tên, ngày, modality"]
    B -->|"GET /studies?..."| C{"Kết quả"}
    C -->|"[]"| C0["Màn: không có ca phù hợp"]
    C -->|"Có Warning 299 'additional results'"| C1["Hiện nút trang sau (offset)"]
    C -->|"Warning 299 'Ignored unsupported query parameters'"| C2["Viewer nên bỏ bộ lọc đó khỏi giao diện"]
    C -->|"400 (ngày hỏng, * giữa chuỗi, bộ lọc quá rộng)"| C3["Báo lỗi bộ lọc"]
    C -->|"Chọn study"| D["Màn: series của ca<br/>GET /studies/{s}/series"]
    D --> E["Mở series: metadata<br/>GET .../series/{r}/metadata"]
    E --> F["Hiển thị frame<br/>GET .../frames/{n} hoặc .../rendered"]
    F --> G{"Lỗi"}
    G -->|"404 không có pixel data"| G1["Instance là báo cáo có cấu trúc (không có ảnh): ẩn khỏi lưới ảnh"]
    G -->|"406 rendered"| G2["Định dạng chưa dựng được: viewer tự giải mã frame gốc"]
    G -->|"413 rendered"| G3["Frame quá lớn để dựng: dùng frame gốc"]
    G -->|"416"| G4["Số frame vượt NumberOfFrames"]
    G -->|"Phản hồi bị cắt giữa chừng"| G5["Lỗi kho: tải lại; báo vận hành nếu lặp"]
```

**Gợi ý UX:** viewer nên (a) hiển thị `modalitiesInStudy`, số series, số instance từ danh sách study (đã tính sẵn); (b) tải frame song song được vì mỗi frame chỉ đọc đúng khoảng byte; (c) với JPEG 2000/RLE... dùng frame gốc rồi giải mã phía client vì `rendered` trả 406.

## 3.3 Tích hợp viên: thử và đọc lỗi (F-10, F-11)

```mermaid
flowchart TD
    A([Bắt đầu]) --> B["Lấy token từ auth<br/>(claim project, scope fhir)"]
    B --> C["GET /swagger/index.html<br/>(không phải staging/production)"]
    C --> D["Gọi thử"]
    D --> E{"Mã trả về"}
    E -->|"401"| E1["Thiếu hoặc sai token; kiểm iss, aud, JWKS"]
    E -->|"403 'Token không có quyền truy cập FHIR'"| E2["Token thiếu claim project hoặc scope"]
    E -->|"403 'Không được dùng project khác'"| E3["URL /projects/{id} hoặc X-Fhir-Project khác token"]
    E -->|"403 'Token không đủ quyền...'"| E4["GET cần đọc, POST cần ghi"]
    E -->|"501 'Chưa cài đặt'"| E5["Route chưa hỗ trợ (rendered cấp series/study, xóa...): KHÔNG phải thành công"]
    E -->|"400 / 404 / 416 / 406"| E6["Lỗi tham số hoặc dữ liệu: đọc thông điệp {error}"]
```

Mọi lỗi có dạng `{"error": "..."}` (không phải OperationOutcome). Bốn tiền tố URL tương đương: `/dicomweb`, `/api/dicomweb`, `/projects/{id}/dicomweb`, `/api/projects/{id}/dicomweb`; dùng tiền tố có project khi client Medplum sẵn có.

## 3.4 Client cũ (F-09)

```mermaid
flowchart LR
    A["Hệ thống cũ gọi<br/>/dicomweb/wado?requestType=WADO&..."] --> B{"Trả về"}
    B -->|"200 tệp hoặc ảnh"| C["Dùng"]
    B -->|"400"| D["Thiếu UID, requestType sai, anonymize khác no"]
    B -->|"406"| E["contentType không hỗ trợ"]
    B -->|"404 / 416"| F["Không có instance hoặc frameNumber vượt"]
```

## 3.5 Vận hành (F-12, F-13, F-14)

```mermaid
flowchart TD
    A([Sự cố / nâng cấp]) --> B{"Triệu chứng"}
    B -->|"Thoát ngay 'schema chưa được migrate'"| B1["Chạy fhir-server migrate up"]
    B -->|"Thoát ngay: lỗi tên biến"| B2["Sửa theo thông báo (đã gom đủ)"]
    B -->|"401 mọi token"| B3["Đối chiếu FHIR_JWT_ISSUER/AUDIENCE/JWKS với auth"]
    B -->|"Frame chậm"| B4["reindex --dry-run rồi reindex"]
    B -->|"Client đọc pixelData qua API FHIR thấy trống"| B9["Kiểm DICOMWEB_PIXELDATA_FRAMES, log hàng đợi đầy, rồi pixeldata --dry-run và pixeldata"]
    B -->|"STOW timeout ở proxy"| B5["Tắt đệm yêu cầu, tăng timeout cho /dicomweb"]
    B -->|"429"| B6["Chỉnh DICOMWEB_RATE_LIMIT / TRUSTED_PROXIES"]
    B -->|"ready 503"| B7["Kiểm database; không restart chỉ vì ready"]
    B -->|"Phản hồi bị cắt"| B8["Kiểm kho nội dung, log 'DICOMweb: lỗi khi ghi'"]
```
