# fhir-notifier: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

`fhir-notifier` là **service nền không có giao diện nghiệp vụ**: không có route nghiệp vụ, cổng `:8010` chỉ có probe. Việc nó làm (giao thông báo Subscription cho bên nhận của khách) bắt đầu từ một sự kiện ghi resource do `fhir-server` phát lên Redis và kết thúc bằng một lần `POST` tới webhook của khách (hoặc một thông điệp lên kênh websocket). Vì vậy "luồng người dùng" ở đây là *khách tạo Subscription ở API rồi nhận thông báo ở hệ thống của họ*, cộng phần của vận hành. "Hợp đồng" của nó là sự kiện trên Redis, webhook gửi ra, kênh websocket, schema `fhir` và biến môi trường dùng chung với `fhir-server`.

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan-va-luong-nghiep-vu.md](01-tong-quan-va-luong-nghiep-vu.md) | Làm gì, cho ai, ranh giới với `fhir-server` và `ws-gateway`, thuật ngữ, 6 luồng nghiệp vụ (giao rest-hook bền, thử lại và bỏ cuộc, tự tắt Subscription, websocket, dừng và nhận lại, chuyển giao từ API) |
| 2 | [02-luong-nguoi-dung.md](02-luong-nguoi-dung.md) | Bên tích hợp (tạo Subscription, nhận webhook), vận hành đi qua bước nào, thấy gì, lỗi nào thì làm gì; bảng độ trễ |
| 3 | [03-chuc-nang.md](03-chuc-nang.md) | Danh mục F-01..F-09: nhận sự kiện, nạp Subscription, khớp, giao rest-hook, thử lại và bỏ cuộc, tự tắt, websocket, khởi động và tắt êm |
| 4 | [04-luat-va-phan-quyen.md](04-luat-va-phan-quyen.md) | Luật BR-xx, ranh giới an toàn, vòng đời một lần giao và của Subscription, bảng thời hạn, kịch bản chấp nhận AT-xx, truy vết test, điểm lệch và rủi ro |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng nghiệp vụ, `AT-xx` kịch bản chấp nhận. Kiến trúc, cấu hình, vận hành, hợp đồng dây: [../architecture.md](../architecture.md), [../config.md](../config.md), [../runbook.md](../runbook.md), [../api-contract.md](../api-contract.md).

**Nguồn sự thật:** mã (`pkg/fhirsub/{manager,deliver,failure,ws_remote}.go`, `apps/fhir-notifier/internal`) và test (đường dẫn đầy đủ ở mục TRACE của tệp 4). Phía API (cách tạo Subscription, websocket token) mô tả ở [fhir-server business](../../../fhir-server/docs/business/04-chuc-nang.md) (F-21 rest-hook, F-22 websocket); bộ này mô tả service **chạy** việc khớp và giao ở tiến trình riêng và thêm phần riêng của nó (giao bền, nhóm consumer, đếm thất bại dùng chung).

**Đã kiểm với mã (2026-11-24):** các luật được đối chiếu với mã và có test ở `pkg/fhirsub` hoặc `apps/fhir-notifier` (đơn vị, tích hợp PostgreSQL và Redis thật, e2e image).

> **Điểm lệch và rủi ro phát hiện khi viết (chưa sửa mã, chi tiết ở mục 4.7):**
> 1. **Giao ít nhất một lần, có thể trùng**: không có mã sự kiện riêng trong webhook; bên nhận phải tự khử trùng lặp (BR-12).
> 2. **Thông báo đã hết lượt thử chỉ được ghi log, không lưu để giao lại** và không vào dead-letter (BR-13).
> 3. **Subscription lỗi bị bỏ qua âm thầm khi nạp** (endpoint sai, criteria chained hoặc dùng terminology, đã hết hạn): chỉ có log WARN, `Subscription.status` vẫn `active` (BR-05).
> 4. **Cache Subscription 30 giây**: Subscription mới tạo hoặc vừa tắt có thể chậm tới 30 giây ở replica khác (BR-04).
> 5. **Một sự kiện chậm giữ một slot song song và một message PEL** tới khi Subscription chậm nhất xong (tới vài chục giây ở cấu hình mặc định, BR-10).
> 6. **`secret` và header xác thực của Subscription lưu rõ trong resource** (BR-17).
> 7. **Giai đoạn chuyển**: nếu `fhir-server` còn `FHIR_SUBSCRIPTION_DELIVERY=true` thì mỗi Subscription được giao hai lần (BR-21).
