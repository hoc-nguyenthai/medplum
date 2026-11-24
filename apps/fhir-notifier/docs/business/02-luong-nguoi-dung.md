# 2. Luồng người dùng

`fhir-notifier` **không có màn hình hay route cho người dùng**. Người dùng là *bên tích hợp*: tạo Subscription ở `fhir-server`, rồi nhận thông báo ở hệ thống của mình. Vận hành thấy service qua cấu hình, log, probe. Cú pháp tạo Subscription: [fhir-server business](../../../fhir-server/docs/business/03-luong-nguoi-dung.md).

## 2.1 Nhân vật: bên tích hợp (rest-hook)

| Bước | Làm gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1. Tạo | `POST [base]/Subscription` với `criteria` (ví dụ `Patient?family=Smith`), `channel.type=rest-hook`, `channel.endpoint` (https), tuỳ chọn `channel.header` (`Tên: giá trị`), `subscription-secret`, `subscription-supported-interaction` | `201` ngay; **API không kiểm** Subscription (endpoint sai hay criteria không hỗ trợ vẫn `201`) | không có lỗi lúc tạo; xem bảng 2.3 nếu không nhận được gì |
| 2. Chờ hiệu lực | tối đa 30 giây ở replica khác (cache); ngay ở replica nhận sự kiện của chính Subscription | | tạo xong đợi vài chục giây rồi mới thử |
| 3. Nhận | ghi resource khớp ở API | `POST` tới `endpoint` (thân: resource; `{}` khi xóa; header `X-Medplum-Subscription`, `X-Medplum-Interaction`, `X-Signature` nếu có secret, header riêng) | trả `2xx` để xác nhận; lỗi mạng, `408`, `429`, `5xx` được thử lại, `4xx` khác bị bỏ cuộc |
| 4. Khử trùng lặp | so mã resource và `meta.versionId` đã xử lý | cùng thông báo có thể tới hai lần (sau khi dịch vụ chết giữa lúc giao) | bên nhận phải idempotent |
| 5. Khi bị tắt | đọc `GET Subscription/{id}`: `status=off`, `error` = `Automatically disabled after N consecutive failed events in the last S seconds` | thông báo ngừng | sửa bên nhận rồi đặt `status=active` (PUT hoặc PATCH) |

## 2.2 Nhân vật: bên tích hợp (websocket)

Tạo Subscription `channel.type=websocket`, lấy token bind từ API (`$get-ws-binding-token`), mở websocket tới `ws-gateway` với token đó. `fhir-notifier` khớp và phát lên kênh; gateway đẩy xuống socket. Không có gateway nào đang giữ socket bind Subscription thì thông báo không được phát (không bền, không phát lại): client dựa vào heartbeat và `eventsSinceSubscriptionStart` để biết đã bỏ lỡ.

## 2.3 Vì sao không nhận được thông báo

| Triệu chứng | Nguyên nhân | Hành động |
|---|---|---|
| không có gì sau khi tạo | Subscription mới, chưa nạp (tới 30 giây); hoặc `status` không phải `active` | chờ; kiểm `status` |
| không có gì dù chờ | Subscription bị **bỏ qua khi nạp**: endpoint không phải `http(s)` hợp lệ, `criteria` sai hoặc dùng chained, `_has` hoặc modifier terminology (chưa hỗ trợ), `end` đã qua | log WARN `fhirsub: bỏ qua Subscription ...`; sửa Subscription |
| không có gì | khác `meta.project` với resource ghi, loại resource khác, `interaction` không nằm trong `subscription-supported-interaction`, resource không khớp `criteria` | kiểm từng điều kiện |
| log `fhirsub: giao thất bại, bỏ cuộc` | máy nhận lỗi (`4xx` khác, hết lượt thử cho lỗi mạng hoặc `5xx`) hoặc đích bị chặn (địa chỉ nội bộ) | sửa bên nhận; chỉ dev mới `FHIR_WEBHOOK_ALLOW_PRIVATE=true` |
| `status=off` | tự tắt vì thất bại lặp lại | sửa bên nhận, đặt lại `active` |
| nhận trùng | service chết giữa lúc giao rồi giao lại; hoặc `fhir-server` còn giao (`FHIR_SUBSCRIPTION_DELIVERY=true`) | khử trùng lặp; tắt giao ở API |
| websocket không tới | không có gateway giữ socket bind (bộ đếm `Unheard`), `FHIR_WS_MODE` không phải `issuer` | kiểm gateway và chế độ |

## 2.4 Nhân vật: vận hành

| Việc | Cách | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| Khởi động | `fhir-notifier` (hoặc image) với cấu hình trùng `fhir-server` | log `fhir-notifier đang nghe sự kiện ghi resource`, `/health/ready` `200` | từ chối nêu tên biến: sửa; `schema chưa được migrate`: chạy `fhir-server migrate up`; `FHIR_WS_MODE=embedded`: đổi `issuer` hoặc `off` |
| Kiểm | `GET /healthz`, `/health/live`, `/health/startup`, `/health/ready` | ready: `{"ready":..,"checks":{"broker":..,"database":..}}`, `503` khi hỏng hoặc đang tắt | liveness luôn `200` |
| Webhook chậm làm trễ | log, số sự kiện đang xử lý | một sự kiện giữ slot tới khi Subscription chậm nhất xong | tăng `FHIR_WEBHOOK_WORKERS`; sửa hoặc tắt Subscription chậm |
| Tắt | `SIGTERM` | ready `503`, chờ `SHUTDOWN_DRAIN_DELAY`, hủy lần giao đang chạy, thoát mã 0 | việc dở được replica khác giao lại sau `BROKER_CLAIM_IDLE` |

## 2.5 Bảng độ trễ

| Sự kiện | Độ trễ |
|---|---|
| Ghi resource tới khi webhook được gọi | dưới 1 giây khi service chạy bình thường (cộng độ trễ khớp) |
| Subscription mới tới khi có hiệu lực | tới 30 giây (cache `CacheTTL`), ngay ở replica nhận sự kiện của chính Subscription |
| Lần thử lại webhook | chờ 1 giây nhân đôi mỗi lần, trần 1 phút (tối đa `FHIR_WEBHOOK_MAX_ATTEMPTS`, mặc định 5) |
| Service chết tới khi việc dở được giao lại | tối thiểu `BROKER_CLAIM_IDLE` (mặc định tối thiểu 1 phút, tự tính theo chuỗi giao xấu nhất) |
| Subscription bị tự tắt tới khi hết giao | cache 30 giây ở replica khác |
