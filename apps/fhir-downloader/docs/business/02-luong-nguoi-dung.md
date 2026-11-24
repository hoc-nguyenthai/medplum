# 2. Luồng người dùng

`fhir-downloader` **không có màn hình hay route cho người dùng**. Người dùng chỉ thấy tác dụng qua `fhir-server`: ghi một resource có `attachment.url` ngoài, rồi (sau ít giây) thấy `url` đổi thành đường dẫn của hệ thống. Vận hành thấy nó qua cấu hình, log, probe và dead-letter. Cú pháp phía API: [fhir-server business](../../../fhir-server/docs/business/03-luong-nguoi-dung.md).

## 2.1 Nhân vật: ứng dụng ghi tài liệu

| Bước | Gọi gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1. Ghi | `POST DocumentReference` (hoặc loại khác có `Attachment`) với `content[].attachment.url` = `https://máy-chủ-ngoài/...` | `201`; `url` vẫn là địa chỉ ngoài; việc tải chạy nền | `400` do kiểm dữ liệu của API (không phải của service này) |
| 2. Chờ | `GET DocumentReference/{id}` sau ít giây | `url` thành `Binary/<id>` (đọc ra là URL ký của kho `/storage/...`), `contentType` được điền nếu trống, `versionId` tăng một | `url` vẫn là địa chỉ ngoài sau thời gian dài: tải thất bại hoặc bị chặn (xem bảng 2.3), báo vận hành |
| 3. Dùng | `GET Binary/{id}` hoặc theo URL ký | nội dung tệp | `404`: khác project hoặc đã bị xóa |

Không có API hỏi tiến độ, không có thông báo lỗi trả về cho người đã ghi: lần ghi gốc đã thành công từ trước.

## 2.2 Nhân vật: vận hành

| Việc | Cách | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| Khởi động | `fhir-downloader` (hoặc image) với cấu hình trùng `fhir-server` | log `fhir-downloader đang nghe sự kiện ghi resource`, `/health/ready` `200` | từ chối khởi động nêu tên biến: sửa cấu hình; `schema chưa được migrate`: chạy `fhir-server migrate up` |
| Kiểm | `GET /healthz`, `/health/live`, `/health/startup`, `/health/ready` | ready: `{"ready":..., "checks":{"broker":..,"database":..}}`, `503` khi DB hoặc Redis hỏng hoặc đang tắt | liveness luôn `200` khi tiến trình sống |
| Attachment không đổi | đọc log (có `resource`, `host`, **không có** URL đầy đủ) | `URL bị chặn`, `quá lớn`, `loại nội dung bị cấm`, hoặc lỗi tải | xem bảng 2.3; sau khi sửa, ghi lại resource để kích hoạt lại |
| Sự kiện vào dead-letter | stream `trivita:events:fhir.resource.changed:dead` | các message vượt 5 lần giao | xử lý tay (chưa có công cụ); ghi lại resource để tải lại |
| Binary mồ côi | `fhir-server sweep-binary` (chỉ dọn tệp ở kho, không dọn dòng) | | |
| Tắt | `SIGTERM` | `/health/ready` `503`, chờ `SHUTDOWN_DRAIN_DELAY`, đợi việc đang tải, thoát mã 0 | việc chưa ACK được replica khác nhận lại |

## 2.3 Vì sao `url` không đổi

| Triệu chứng trong log | Nguyên nhân | Hành động |
|---|---|---|
| `URL bị chặn` hoặc `địa chỉ bị chặn` | đích là địa chỉ nội bộ (chống SSRF) | dùng URL công khai; chỉ dev mới `FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE=true` |
| không có log nào cho URL đó | URL thuộc hệ thống, khớp tiền tố bỏ qua, không khớp danh sách cho phép, không phải `http(s)` tuyệt đối, hoặc loại resource bị bỏ qua | kiểm `FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES`, `..._IGNORED_PREFIXES` |
| `quá lớn` | tệp lớn hơn `FHIR_AUTO_DOWNLOAD_MAX_BYTES` (mặc định theo `FHIR_STORAGE_MAX_UPLOAD_BYTES`, 256 MiB) | tăng trần hoặc không đính kèm trực tiếp |
| `loại nội dung bị cấm` | `Content-Type` nằm trong danh sách chặn (ví dụ `application/x-sh`) | không tải được loại này |
| lỗi tải (`HTTP 404`, `HTTP 500`, lỗi mạng) | máy chủ ngoài lỗi hoặc URL sai | sự kiện thử lại tới 5 lần rồi vào dead-letter; sửa URL ở resource |
| không có gì cả và service chạy bình thường | sự kiện chưa tới (Redis, nhóm consumer) hoặc service tắt | kiểm `/health/ready`, stream và nhóm consumer |

## 2.4 Bảng độ trễ

| Sự kiện | Độ trễ |
|---|---|
| Ghi resource tới khi `url` đổi | thời gian tải (tới `FHIR_AUTO_DOWNLOAD_TIMEOUT`, mặc định 30 giây mỗi lần) cộng độ trễ nhận sự kiện (dưới 1 giây khi service đang chạy) |
| Lỗi tạm tới lần thử lại | tối thiểu `ClaimIdle` (30 giây, cố định) cộng chu kỳ quét 10 giây |
| Service dừng tới khi bù việc | khi chạy lại, xử lý ngay các sự kiện còn trong stream |
