# dicomweb: vận hành

Cập nhật 2026-11-24 (D4: STOW-RS, QIDO-RS, WADO-RS). Cấu hình: [config.md](config.md).

## Triển khai

1. `fhir-server migrate up` trước (dicomweb không migrate; chưa migrate thì dicomweb thoát với "schema chưa được migrate").
2. Cùng `DATABASE_URL`, `FHIR_JWT_*` và `FHIR_BINARY_STORAGE` với fhir-server (dùng chung tệp môi trường được).
3. Chạy `trivita-dicomweb` (image từ `apps/dicomweb/Dockerfile`). Kho `file:` cần thư mục ghi được bởi UID 65532; production dùng `s3:`.
4. Sau khi rollout: `/health/ready` 200; `GET /dicomweb/studies` không token phải 401.

## Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Thoát ngay: "schema chưa được migrate" | fhir-server chưa chạy `migrate up` trên database này | chạy `fhir-server migrate up` |
| Thoát ngay: lỗi tên biến (`FHIR_JWT_ISSUER`, `FHIR_BINARY_STORAGE`...) | cấu hình thiếu hoặc sai; mọi lỗi được gom và nêu đủ | sửa theo thông báo, xem [config.md](config.md) |
| 401 với mọi token | `FHIR_JWT_ISSUER` hoặc `FHIR_JWT_AUDIENCE` khác của `auth`; JWKS không tải được (xem log "token bị từ chối") | đối chiếu với `auth` và `fhir-server` |
| 403 "Token không có quyền truy cập FHIR" | token thiếu claim project hoặc scope `fhir.*`/SMART | kiểm token do `auth` cấp |
| 403 "Không được dùng project khác" | URL `/projects/{id}/dicomweb` hoặc header `X-Fhir-Project` khác project của token | dùng đúng project, hoặc tiền tố không có project |
| 501 "Chưa cài đặt" | route DICOMweb chưa cài (instances lẻ, rendered, bulkdata, bộ lọc QIDO: D7) | xem api-contract.md |
| WADO frame 404 "Pixel data not found" | instance không có ảnh (báo cáo có cấu trúc), hoặc Binary chứa nội dung đã bị xóa | kiểm instance; nếu Binary bị xóa, tải lại |
| WADO frame 416 | số frame vượt `NumberOfFrames` của instance | frame đánh số từ 1 |
| WADO frame 403 | token thiếu quyền đọc `Binary` (ngoài `Dicom*`) | cấp scope `system/Binary.r` |
| WADO frame chậm với tệp lớn | instance không có chỉ mục frame (ghi trước D5, chỉ mục hỏng, hoặc ảnh đóng gói nhiều fragment không có Basic Offset Table) nên mỗi yêu cầu quét cả tệp; hoặc kho không đọc theo khoảng | tải lại instance qua STOW để lập chỉ mục (STOW bỏ qua instance đã có nên cần xóa instance trước); xem log `bỏ qua chỉ mục frame hỏng` |
| Phản hồi metadata hoặc frame bị cắt giữa chừng | kho nội dung lỗi sau khi đã bắt đầu ghi (server cố ý ngắt kết nối) | xem log `DICOMweb: lỗi khi ghi`; kiểm kho |
| STOW 409, `FailureReason` 49152 (0xC000) | tệp không phải DICOM, bị cắt giữa PixelData, SQ lồng quá sâu, thiếu hoặc sai UID, transfer syntax không hỗ trợ (Big Endian, Deflated) | kiểm tệp ở phía gửi; thông điệp từng instance nằm trong log (không có dữ liệu bệnh nhân) |
| STOW 409 hoặc 202, `FailureReason` 42752 (0xA700) | instance vượt `DICOMWEB_MAX_INSTANCE_BYTES`, hoặc phần đầu DICOM vượt `DICOMWEB_MAX_HEADER_BYTES` | tăng trần có cân nhắc; phần đầu lớn bất thường thường là tệp độc |
| STOW 403 | token thiếu quyền ghi (`fhir.write`), hoặc chính sách truy cập/SMART không cho tạo `Binary`, `DicomStudy`, `DicomSeries`, `DicomInstance` | cấp scope `system/Binary.c system/Dicom*.cru` hoặc quyền ghi |
| STOW chậm hoặc timeout ở proxy | tải lên lớn qua proxy có `proxy_read_timeout` ngắn hoặc bộ đệm yêu cầu | tắt bộ đệm yêu cầu và tăng timeout cho `/dicomweb` ở proxy |
| `/health/ready` 503 | database không trả lời trong `READINESS_TIMEOUT`, hoặc đang tắt | kiểm database; không restart pod chỉ vì ready (liveness độc lập) |
| 413 | body vượt `DICOMWEB_MAX_REQUEST_BYTES` | chia nhỏ yêu cầu STOW hoặc tăng trần có cân nhắc bộ nhớ và thời gian |
| 429 | hạn mức theo IP của từng instance | đặt `DICOMWEB_RATE_LIMIT` phù hợp, hoặc `DICOMWEB_TRUSTED_PROXIES` nếu IP bị gộp do proxy |

## Định cỡ

Số đo (máy phát triển, tệp tổng hợp, SQLite và kho tệp; chưa phải số của production): tải một tệp 104 MB qua HTTP đỉnh heap 11 MB (đo cả phía client trong cùng tiến trình, `TestStowStreamsLargeFiles`); bộ nhớ mỗi yêu cầu STOW là hằng số, gồm phần đầu DICOM (≤ `DICOMWEB_MAX_HEADER_BYTES`, mặc định 1 MiB) cộng vài bộ đệm 64 KB, không phụ thuộc kích thước tệp. Định cỡ bộ nhớ: số yêu cầu STOW đồng thời × (tối đa khoảng 1,2 MiB) cộng nền. Thời gian tải bị giới hạn bởi băng thông và kho (S3: nhân với độ trễ mỗi lần ghi), không phải CPU. Từ chối tệp độc (SQ lồng 2000 lớp, 60 KB) dưới 1 giây và cấp phát không đáng kể (đưa thẳng vào thư viện sẽ tốn 2,2 GB). Lấy một frame 8 MB từ tệp 104 MB đọc đúng 8 MB từ kho và đỉnh heap 8 MB (`TestWadoFrameOfLargeFileStreams`); thời gian tỉ lệ với kích thước frame, không phụ thuộc vị trí frame trong tệp (nhờ chỉ mục frame và đọc theo khoảng). **Đo tải (D6, `make test-load-dicomweb`; máy phát triển, SQLite và kho tệp tạm, 16 client, 500 instance 215 MB; chỉ để so sánh với chính nó):** STOW 135 instance/s (58 MB/s), p50 245 ms, p95 1,4 s, đỉnh heap 140 MB (16 yêu cầu song song, mỗi yêu cầu 5 instance, tức khoảng 9 MB mỗi yêu cầu đang chạy do các bộ đệm của client và server trong cùng tiến trình); WADO frame 512 KiB 629 yêu cầu/s (83 MB/s), p50 25 ms, p99 31 ms, đỉnh heap 15 MB; QIDO study 489 yêu cầu/s, p50 12 ms; metadata series 25 instance p50 20 ms. STOW chậm ở p95 vì SQLite chỉ có một người ghi; PostgreSQL chưa đo (không có Docker). Cũng nhờ bài đo này phát hiện và sửa lỗi hiệu năng tìm kiếm của SQLite (xem `docs/plans/cds-dicom/dicom.md`).
