# fhir-downloader: vận hành

## Bật service thay cho worker trong API

Thứ tự (không tắt trước khi bật, không bật hai bên cùng lúc lâu):

1. Chạy `fhir-server migrate up` (schema `fhir` do `fhir-server` sở hữu).
2. Bảo đảm `fhir-server` dùng `BROKER_DRIVER=redis` và `REDIS_URL` (cùng Redis với service này).
3. Dựng `fhir-downloader` với cùng `DATABASE_URL` (schema `fhir`), `REDIS_URL`, `FHIR_BINARY_STORAGE` (và `FHIR_S3_*`), `FHIR_BASE_URL`. Kiểm `/health/ready` trả 200 và log "đang nghe sự kiện ghi resource".
4. Đặt ở `fhir-server`: `FHIR_AUTO_DOWNLOAD=true` (giữ, để API còn phát sự kiện) và `FHIR_AUTO_DOWNLOAD_WORKER=false`, rồi rolling restart. Từ lúc này API không gọi ra Internet.
5. Chặn đường ra Internet của `fhir-server` bằng chính sách mạng (đây là lý do chính của việc tách); chỉ mở cho `fhir-downloader`.

Quay lại: đặt `FHIR_AUTO_DOWNLOAD_WORKER=true` ở `fhir-server`, rolling restart, rồi dừng service này.

## Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Từ chối khởi động: `schema chưa được migrate` | chạy trước `fhir-server migrate up` | chạy migrate rồi khởi động lại |
| Từ chối khởi động: `BROKER_GROUP ... "trivita"` | dùng chung group với `fhir-server` | đặt group khác (mặc định `fhir-downloader`) |
| `/health/ready` 503, `broker=false` | Redis không tới được | kiểm `REDIS_URL`, mạng; liveness vẫn 200 nên không bị restart; tự nối lại khi Redis lên |
| Attachment giữ URL ngoài mãi | URL bị chặn (địa chỉ nội bộ, ngoài `ALLOWED_PREFIXES`, trong `IGNORED_PREFIXES`), quá `MAX_BYTES`, loại nội dung cấm, máy chủ ngoài lỗi quá 5 lần | đọc log (host, không có URL đầy đủ), kiểm stream `:dead`; ghi lại resource để kích hoạt lại sau khi sửa |
| Có Binary mồ côi | chết giữa chừng | `fhir-server sweep-binary` |
| Tải hai lần | `fhir-server` còn `FHIR_AUTO_DOWNLOAD_WORKER=true` | tắt worker ở API (bước 4) |
| Stream sự kiện lớn dần | service ngừng chạy lâu | chạy lại service; chưa có giới hạn độ dài stream |

## Định cỡ

Chưa có số đo (điều kiện A, xem architecture.md). Scale theo số sự kiện ghi có attachment ngoài và băng thông tới máy chủ ngoài: thêm replica cùng `BROKER_GROUP` để chia việc. Tổng kết nối DB của mọi replica của mọi service phải nhỏ hơn `max_connections` (mỗi service mặc định 20, D-7).
