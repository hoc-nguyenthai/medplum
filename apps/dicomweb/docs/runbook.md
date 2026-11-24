# dicomweb: vận hành

Cập nhật 2026-11-24 (D1: khung). Cấu hình: [config.md](config.md).

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
| 501 "Chưa cài đặt" | route DICOMweb chưa cài (D1) hoặc không thuộc tập route được hỗ trợ | chờ D3, D4; xem api-contract.md |
| `/health/ready` 503 | database không trả lời trong `READINESS_TIMEOUT`, hoặc đang tắt | kiểm database; không restart pod chỉ vì ready (liveness độc lập) |
| 413 | body vượt `DICOMWEB_MAX_REQUEST_BYTES` | chia nhỏ yêu cầu STOW hoặc tăng trần có cân nhắc bộ nhớ và thời gian |
| 429 | hạn mức theo IP của từng instance | đặt `DICOMWEB_RATE_LIMIT` phù hợp, hoặc `DICOMWEB_TRUSTED_PROXIES` nếu IP bị gộp do proxy |

## Định cỡ

Chưa có số đo cho tải thật (D1 chưa có đường STOW). Số đã có từ spike D0 (tệp tổng hợp, máy phát triển): đường STOW qua luồng với tệp 120 MB tốn khoảng 2 MB bộ nhớ trên mỗi yêu cầu đang chạy (đỉnh), không đọc cả tệp vào bộ nhớ; tệp độc có SQ lồng 2000 lớp (60 KB) làm thư viện mặc định tốn 2,2 GB, nên D3 bắt buộc có chốt chặn độ sâu trước khi định cỡ bộ nhớ. Số đo định cỡ thật sẽ làm ở D6 với `tests/load`.
