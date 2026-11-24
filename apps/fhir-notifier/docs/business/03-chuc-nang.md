# 3. Danh mục chức năng

Chức năng của **chính `fhir-notifier`** (không có route; kích hoạt bởi sự kiện trên Redis). Cách tạo Subscription và websocket token ở phía API thuộc tài liệu của `fhir-server` (F-21, F-22).

| Mã | Chức năng | Tác nhân |
|---|---|---|
| [F-01](#f-01) | Nhận sự kiện ghi resource | A3, A7 |
| [F-02](#f-02) | Nạp và làm mới Subscription đang hoạt động | A1 |
| [F-03](#f-03) | Khớp sự kiện với Subscription | A1 |
| [F-04](#f-04) | Giao rest-hook (bền) | A4 |
| [F-05](#f-05) | Thử lại và bỏ cuộc | A6 |
| [F-06](#f-06) | Tự tắt Subscription lỗi lặp lại | A1, A6 |
| [F-07](#f-07) | Phát sự kiện websocket lên kênh Redis | A5 |
| [F-08](#f-08) | Khởi động, probe và tắt êm | A6 |
| [F-09](#f-09) | Nhận lại việc dở khi một replica chết | A7 |

<a id="f-01"></a>
### F-01. Nhận sự kiện ghi resource

| Mục | Nội dung |
|---|---|
| Mục đích | Nhận mọi sự kiện ghi resource của `fhir-server` và xử lý song song. |
| Tiền điều kiện | `REDIS_URL`; nhóm `BROKER_GROUP` (mặc định `fhir-notifier`, khác `trivita`); schema đã migrate. |
| Cấu hình | `FHIR_WEBHOOK_WORKERS` (4): số sự kiện xử lý song song trong một instance. |

**Luồng chính:** nhóm được tạo từ đầu stream (nhận cả sự kiện ghi lúc service chưa chạy); mỗi message giao cho một replica; handler xử lý (F-03, F-04) rồi ACK khi xong; các message của một replica chạy song song tới `FHIR_WEBHOOK_WORKERS`, thứ tự giữa chúng không được bảo đảm.
**Thay thế / lỗi:** payload không giải mã được thành `ChangeEvent` trả lỗi (nhận lại, rồi dead-letter sau 5 lần giao); vỏ message hỏng thì ACK và bỏ qua. **Luật:** BR-01, BR-09, BR-10.

<a id="f-02"></a>
### F-02. Nạp và làm mới Subscription đang hoạt động

**Luồng chính:** đọc `Subscription?status=active` từ kho gốc (không tách project, trang tối đa) và dựng bảng tra: kênh `rest-hook` hoặc `websocket` (kênh khác bị bỏ), `criteria` đã phân tích, interaction hỗ trợ (extension `subscription-supported-interaction`), `secret` (extension `subscription-secret`), header riêng (`Tên: giá trị`), `end` (hạn), `meta.project`. Cache giữ `CacheTTL` (30 giây); sự kiện của **chính Subscription** (create, update, delete) làm mới ngay ở replica nhận nó.
**Thay thế / lỗi:** bị **bỏ qua kèm log WARN**: endpoint không hợp lệ (không `http(s)`, thiếu host, có userinfo), `criteria` không dùng được, `criteria` chained hoặc `_has` hoặc modifier terminology (chưa hỗ trợ). Không có lỗi trả về cho người tạo. **Luật:** BR-04, BR-05.

<a id="f-03"></a>
### F-03. Khớp sự kiện với Subscription

**Luồng chính:** với mỗi Subscription đang hoạt động: cùng loại resource; `interaction` nằm trong danh sách hỗ trợ (rỗng là mọi interaction); chưa quá `end`; **cùng `meta.project`** với resource; resource khớp `criteria` (đối sánh trên JSON của sự kiện, một lần giải mã cho mọi Subscription). Khớp thì thêm một lần giao. **Thay thế / lỗi:** lỗi khi đối sánh một Subscription: log WARN và bỏ Subscription đó, không ảnh hưởng Subscription khác. **Luật:** BR-02, BR-03.

<a id="f-04"></a>
### F-04. Giao rest-hook (bền)

| Mục | Nội dung |
|---|---|
| Mục đích | Gọi webhook của khách cho mỗi lần khớp, không mất khi restart. |
| Cấu hình | `FHIR_WEBHOOK_TIMEOUT` (10 giây mỗi lần), `FHIR_WEBHOOK_MAX_ATTEMPTS` (5), `FHIR_WEBHOOK_ALLOW_PRIVATE` (chỉ dev). |

**Luồng chính:** các lần giao của một sự kiện chạy **song song** (tối đa `FHIR_WEBHOOK_WORKERS`) và **đồng bộ** trong handler; mỗi lần: `POST` tới `channel.endpoint` qua `safehttp` (chặn địa chỉ nội bộ lúc kết nối) với thân là JSON resource (`{}` khi `delete`), header `Content-Type: application/fhir+json`, `X-Medplum-Subscription`, `X-Medplum-Interaction`, khi xóa thêm `X-Medplum-Deleted-Resource: <Loại>/<id>`, header riêng của Subscription (bỏ `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`) và `X-Signature` (hex HMAC-SHA256 của thân bằng `secret`) nếu có. Mọi `2xx` là thành công và xóa bộ đếm thất bại. Handler chỉ trả nil (ACK) khi mọi lần giao đã xong hoặc bỏ cuộc.
**Thay thế / lỗi:** xem F-05. **Luật:** BR-06..BR-11, BR-17.

<a id="f-05"></a>
### F-05. Thử lại và bỏ cuộc

| Kết quả của một lần giao | Xử lý |
|---|---|
| `2xx` | xong |
| lỗi mạng, `408`, `429`, `5xx` | chờ 1 giây nhân đôi mỗi lần (trần 1 phút), thử lại tại chỗ, tối đa `FHIR_WEBHOOK_MAX_ATTEMPTS` lần |
| `4xx` khác, địa chỉ bị chặn, request không dựng được | bỏ cuộc ngay |
| hết số lần thử | bỏ cuộc |
| ngữ cảnh bị hủy giữa chừng (tắt máy) | **không** bỏ cuộc: handler trả lỗi, message ở lại PEL (F-09) |

Bỏ cuộc: log WARN `fhirsub: giao thất bại, bỏ cuộc` (kèm Subscription, resource, số lần, mã, lỗi), ghi một thất bại vào bộ đếm (F-06); thông báo **không** được lưu để giao lại. **Luật:** BR-11..BR-13.

<a id="f-06"></a>
### F-06. Tự tắt Subscription lỗi lặp lại

**Điều kiện:** `FHIR_SUBSCRIPTION_AUTO_DISABLE` (JSON mảng `{maxConsecutiveFailures, timeWindowSeconds}`, rỗng là tắt tính năng). **Luồng chính:** mỗi lần bỏ cuộc ghi một thất bại vào sorted set Redis của Subscription (dùng chung các replica, tự hết hạn theo cửa sổ lớn nhất); một lần giao thành công xóa bộ đếm; đủ ngưỡng của trigger nào thì đọc Subscription: nếu còn `active` thì chuyển `status=off`, `error` = `Automatically disabled after N consecutive failed events in the last S seconds` bằng `If-Match`, xóa bộ đếm, làm mới cache.
**Thay thế / lỗi:** Subscription đã `off` hoặc đã đổi trạng thái: không ghi đè; bị sửa song song (`412`): bỏ qua, lần thất bại sau xét lại; chưa tạo `AuditEvent`. **Luật:** BR-14.

<a id="f-07"></a>
### F-07. Phát sự kiện websocket lên kênh Redis

**Điều kiện:** `FHIR_WS_MODE=issuer`. **Luồng chính:** Subscription kênh `websocket` khớp (một lần cho cả cụm) → kiểm sổ hiện diện `fhirws.bound.<id>`: có member thì phát `{"sub":"<id>","event":<ChangeEvent>}` lên kênh `fhirws.sub.<id>`; không có thì không phát (bộ đếm `Unheard`). Không có Hub, không giữ kết nối, không có khóa ký token. **Thay thế / lỗi:** Redis lỗi khi đọc sổ hoặc phát: log WARN, không thử lại. `FHIR_WS_MODE` tắt thì Subscription websocket bị bỏ qua. **Luật:** BR-15.

<a id="f-08"></a>
### F-08. Khởi động, probe và tắt êm

**Khởi động:** đọc cấu hình, gom mọi lỗi và từ chối; mở DB; kiểm `Pending == 0`; dựng broker với nhóm, song song và `ClaimIdle` theo chuỗi giao xấu nhất; Manager với bộ đếm thất bại Redis; nếu `issuer` thì gắn phát kênh; bắt đầu nghe. **Probe:** `/health/ready` kiểm database và Redis trong `READINESS_TIMEOUT`, `503` khi hỏng hoặc đang tắt; ba probe còn lại luôn `200`. **Tắt êm:** `SIGTERM` → ready `503` → chờ `SHUTDOWN_DRAIN_DELAY` → hủy các lần giao đang chạy (message ở lại PEL) → chờ handler → đóng Redis, DB → thoát mã 0. **Luật:** BR-16..BR-20.

<a id="f-09"></a>
### F-09. Nhận lại việc dở khi một replica chết

**Luồng chính:** message chưa ACK nằm trong PEL; mỗi `ClaimInterval` (tới 10 giây, tính theo `ClaimIdle`) replica nào đó quét và nhận lại message idle quá `BROKER_CLAIM_IDLE` (mặc định tối thiểu 1 phút, tự tính theo chuỗi giao xấu nhất) rồi **giao lại toàn bộ sự kiện** (kể cả Subscription đã giao trước đó: trùng). **Thay thế / lỗi:** giao lỗi vì tắt máy quá 5 lần thì message vào dead-letter; `BROKER_CLAIM_IDLE` đặt ngắn hơn `FHIR_WEBHOOK_MAX_ATTEMPTS` x `FHIR_WEBHOOK_TIMEOUT` bị từ chối (nếu không replica khác nhận lại message còn đang giao). **Luật:** BR-12, BR-18.
