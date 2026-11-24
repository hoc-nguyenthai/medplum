# fhir-notifier: vận hành

## Bật service thay cho phần giao trong API

Thứ tự (không tắt trước khi bật; không để hai bên cùng giao lâu, mỗi Subscription sẽ bị giao hai lần):

1. Chạy `fhir-server migrate up` (schema `fhir` do `fhir-server` sở hữu).
2. Bảo đảm `fhir-server` dùng `BROKER_DRIVER=redis` và `REDIS_URL` (cùng Redis với service này).
3. Dựng `fhir-notifier` với cùng `DATABASE_URL` (schema `fhir`), `REDIS_URL`, và `FHIR_WS_MODE` (nếu có websocket: `issuer`, như `fhir-server`), cùng `FHIR_SUBSCRIPTION_AUTO_DISABLE` nếu dùng. Kiểm `/health/ready` trả 200 và log "đang nghe sự kiện ghi resource".
4. Ở `fhir-server` đặt `FHIR_SUBSCRIPTIONS=true` (giữ, để API còn phát sự kiện) và `FHIR_SUBSCRIPTION_DELIVERY=false`, rồi rolling restart. Từ lúc này API không giao rest-hook và không khớp websocket (token bind websocket vẫn do API cấp, cần `FHIR_WS_MODE=issuer`; `embedded` không dùng được).
5. Chặn đường gọi webhook ra ngoài của `fhir-server` bằng chính sách mạng; chỉ mở cho `fhir-notifier`.

Quay lại: đặt `FHIR_SUBSCRIPTION_DELIVERY=true` ở `fhir-server` (tùy chọn `FHIR_WEBHOOK_DURABLE=true` để giao bền ngay trong API), rolling restart, rồi dừng service này.

## Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Từ chối khởi động: `schema chưa được migrate` | chạy trước `fhir-server migrate up` | chạy migrate rồi khởi động lại |
| Từ chối khởi động: `BROKER_GROUP ... "trivita"` | dùng chung group với `fhir-server` | đặt group khác (mặc định `fhir-notifier`) |
| Từ chối khởi động: `FHIR_WS_MODE=embedded` | dùng chung tệp môi trường với `fhir-server` đang embedded | đổi sang `issuer` hoặc `off` |
| `/health/ready` 503, `broker=false` | Redis không tới được | kiểm `REDIS_URL`, mạng; liveness vẫn 200 nên không bị restart; tự nối lại khi Redis lên |
| Bên nhận không nhận được thông báo | Subscription không `active`, criteria không khớp, khác `meta.project`, endpoint bị chặn (địa chỉ nội bộ), hoặc đã tự tắt | đọc log `fhirsub:`; kiểm `Subscription.status` và `error`; Subscription mới có thể trễ tới 30 giây |
| Bên nhận thấy thông báo trùng | instance chết giữa lúc giao và message được giao lại; hoặc `fhir-server` còn giao (`FHIR_SUBSCRIPTION_DELIVERY=true`) | bên nhận khử trùng lặp; tắt giao ở API (bước 4) |
| Thông báo dở dang chậm được giao lại sau khi một replica chết | `BROKER_CLAIM_IDLE` (mặc định tối thiểu 1 phút) | chấp nhận, hoặc đặt ngắn hơn nhưng không dưới `FHIR_WEBHOOK_MAX_ATTEMPTS` x `FHIR_WEBHOOK_TIMEOUT` |
| Một webhook chậm làm trễ sự kiện khác | hết `FHIR_WEBHOOK_WORKERS` slot song song | tăng workers; hoặc sửa/tắt Subscription chậm |
| Stream sự kiện lớn dần | service ngừng chạy lâu | chạy lại service; chưa có giới hạn độ dài stream |

## Định cỡ

Chưa có số đo (điều kiện A, xem architecture.md). Scale theo số sự kiện x số Subscription và độ chậm của webhook: thêm replica cùng `BROKER_GROUP` để chia việc; mỗi replica xử lý tối đa `FHIR_WEBHOOK_WORKERS` sự kiện song song. Tổng kết nối DB của mọi replica của mọi service phải nhỏ hơn `max_connections` (mỗi service mặc định 20, D-7).
