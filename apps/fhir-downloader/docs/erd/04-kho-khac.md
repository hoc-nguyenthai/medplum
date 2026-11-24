# 4. Kho khác ngoài SQL, bộ nhớ và bí mật

## 4.1 Kho Binary (`FHIR_BINARY_STORAGE`)

| Khóa / đường dẫn | Giá trị | Vòng đời | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|
| `binary/<id>/<versionId>` (`file:<thư mục>/binary/<id>/<versionId>` hoặc `s3:<bucket>/<prefix>binary/<id>/<versionId>`); `<id>` là UUID v4 do downloader sinh, `<versionId>` là version của `Binary` vừa tạo (thường là phiên bản 1) | **nội dung thô của tệp tải về** (PDF, ảnh, văn bản...), ghi theo luồng từ `resp.Body`, trần `MaxBytes` | tạo sau dòng `Binary` (CSDL trước, kho sau); xóa bởi `drop` khi bù trừ; **không có đường dọn** khi nguồn bị xóa; `sweep-binary` **không** dọn được (03 mục 3.5) | `fhir-downloader` (`Blobs.Put`, `Blobs.Delete`) | `fhir-server` (`GET Binary/{id}`, `/storage`) | `GET Binary/{id}` lỗi; attachment đã đổi `url` trỏ vào chỗ trống |
| `file:` chỉ: `_tmp/<hex>` | tệp tạm trong lúc ghi, `rename` nguyên tử sang khóa đích | xóa khi lỗi; tệp bỏ lại khi tiến trình chết dọn bằng `CleanTemp`, do `fhir-server sweep-binary` gọi (`pkg/fhirbinary/sweep.go:127`) | `fhir-downloader` | không | tệp tạm rác (nhỏ) |

- **Bắt buộc ở staging/production?** Có (config từ chối khi `FHIR_BINARY_STORAGE` rỗng, `config.go`), và **phải trùng** `fhir-server`: nếu hai bên mở hai kho khác nhau thì `fhir-server` không đọc được tệp.
- Kho `file:` cần thư mục ghi được bởi UID `65532` (image distroless không root) và phải **dùng chung** (volume) với `fhir-server`; production dùng `s3:` (thường kèm `FHIR_S3_SSE`: `AES256` hoặc `aws:kms`).
- **Kích thước:** một tệp tới `FHIR_AUTO_DOWNLOAD_MAX_BYTES` (mặc định 0, lấy `FHIR_STORAGE_MAX_UPLOAD_BYTES` = 268435456 byte = 256 MiB). `Content-Length` vượt trần thì bỏ **trước** khi tạo `Binary`; không có hoặc sai `Content-Length` thì bị cắt khi ghi (`ErrTooLarge`), dọn và bỏ. **Không có giới hạn số URL mỗi resource hay tổng dung lượng.** Ghi `s3:` đệm nội dung (bộ nhớ, tràn ra tệp tạm) để tính SHA-256 (`s3store.go`); mức dùng bộ nhớ thực tế **chưa đo**.
- **Nhạy cảm:** tệp tải về là **dữ liệu có thể lâm sàng (S3)** (tài liệu, ảnh bệnh nhân) ở dạng nguyên văn; quyền đọc qua API dựa vào `meta.project` và `securityContext` của `Binary`, nhưng **ai đọc được kho trực tiếp đọc được mọi tệp**: giới hạn quyền kho theo nguyên tắc tối thiểu. `Binary` giữ `contentType` do **máy chủ ngoài khai** (đã qua danh sách chặn nhỏ), không quét mã độc.

## 4.2 Redis: sự kiện, nhóm consumer, thất bại

Mọi khóa do `pkg/messaging/redis.go`. Service **chỉ** dùng Redis Streams (không Pub/Sub, không khóa chuỗi, không khóa theo dõi thất bại).

| Khóa | Kiểu | Giá trị | TTL | Ghi bởi | Đọc bởi | Khi mất |
|---|---|---|---|---|---|---|
| `trivita:events:fhir.resource.changed` (tiền tố mặc định `trivita:events` + `:` + topic `fhirsub.EventType`) | stream | mỗi entry một trường `data` = JSON `Envelope` (`id`, `type`, `occurred_at`, `payload` là `ChangeEvent`: `interaction`, `resource_type`, `id`, `version_id`, **`resource` = JSON đầy đủ của resource**) | **không có** (không `MAXLEN`, không `XTRIM`) | `fhir-server` (mọi lần ghi), `fhir-worker` (batch async), **`fhir-downloader`** (`XADD` mỗi lần đổi `url`) | `fhir-downloader` (nhóm `fhir-downloader`), `fhir-server` (nhóm `trivita`), `fhir-notifier` | sự kiện chưa xử lý mất; **không có đối soát**: attachment chưa tải **không bao giờ được tải** trừ khi resource nguồn được ghi lại |
| nhóm consumer `fhir-downloader` (cấu hình `BROKER_GROUP`; config từ chối `trivita`) trên stream trên | consumer group | vị trí đọc và **PEL** (danh sách message đã giao chưa ACK, kèm số lần giao và thời điểm giao cuối) | không | tạo bởi `XGROUP CREATE ... MKSTREAM` từ vị trí **`"0"`** khi `Start` (bỏ qua `BUSYGROUP`) | `XREADGROUP` (Count 10, Block 1 giây, `>`), `XPENDING`, `XCLAIM` | mất nhóm: tạo lại từ `"0"` và **đọc lại toàn bộ stream**; mất PEL: mất bộ đếm thất bại |
| consumer `fhir-downloader-<8 ký tự đầu của UUID>` | consumer trong nhóm | tên mới mỗi lần tiến trình khởi động | không | `Redis` (`NewRedisFromClient`) | | consumer cũ (đã chết) **nằm lại** trong danh sách của nhóm (không có `XGROUP DELCONSUMER`); message của nó được nhận lại qua `XCLAIM` sau `ClaimIdle` |
| `trivita:events:fhir.resource.changed:dead` | stream | các trường của message gốc (`data`) cộng `original_id` và `deliveries` | **không có** | broker (`deadLetter`) khi `RetryCount >= MaxDeliveries` (5) | **không ai** (chưa có công cụ liệt kê, phát lại hay xóa) | mất dead-letter: không ảnh hưởng chạy tiếp; mất dấu việc đã bỏ cuộc |

**Khóa theo dõi thất bại (failure-tracker keys): downloader không có.** Cái tên gần nhất là `trivita:sub:failures:<subscriptionId>` (sorted set, điểm là mili giây, thành viên `<ms>:<id>`, `EXPIRE` bằng cửa sổ lớn nhất) của `fhirsub.RedisFailureTracker`, nhưng nó chỉ do **`fhir-server`** (`container.go:143`) và **`fhir-notifier`** (`container.go:101`) dựng, để tự tắt `Subscription`; `fhir-downloader` không tạo `fhirsub.Manager` và không bao giờ ghi hay đọc khóa này. Việc "theo dõi thất bại" của downloader là:

| Cơ chế | Ở đâu | Hành vi | Hạn chế |
|---|---|---|---|
| Số lần giao (`RetryCount`) của message trong PEL | Redis, nhóm `fhir-downloader` | handler trả lỗi thì **không ACK**; cứ mỗi `ClaimInterval` (10 giây) một replica quét `XPENDING` các message idle ≥ `ClaimIdle` (30 giây), `XCLAIM` và chạy lại; tới `MaxDeliveries` (5) thì chép sang `:dead` rồi ACK | bộ đếm theo **message**, không theo URL hay máy chủ ngoài; một resource ghi lại (sự kiện mới) bắt đầu đếm lại từ 0 |
| Log | `slog` (không lưu) | `bỏ cuộc`, `message vượt số lần giao tối đa, chuyển vào dead-letter` kèm `stream`, `id`, `deliveries`; log URL chỉ ghi **host** (`hostOf`), không ghi query | không có thông báo hay chỉ số riêng cho dead-letter (chưa kiểm telemetry) |

Số lần thử thực tế mỗi sự kiện: lần giao đầu, rồi tối đa 4 lần nhận lại cách nhau ít nhất 30 giây (khoảng 2 phút trở lên), tổng 5. Các tham số `ClaimIdle`, `ClaimInterval`, `MaxDeliveries`, `Concurrency` **không có biến cấu hình** ở downloader (`messaging.Config` chỉ nhận `Group`; còn lại dùng mặc định: `Concurrency=1` nên một lần tải chậm chặn mọi sự kiện sau nó trong replica đó).

- **Mất Redis lúc chạy:** `broker=false` ở `/health/ready` (503) nhưng liveness vẫn 200 nên không bị restart; tự nối lại khi Redis lên (`TestSurvivesRedisOutage`). Sự kiện mà `fhir-server` **phát lúc Redis chết** chỉ bị log (`phát sự kiện lỗi`) và **mất**: không có outbox, nên attachment của lần ghi đó không được tải.
- **Redis mất dữ liệu** (flush, thay instance trống): mất stream, PEL và dead-letter; việc chưa làm mất theo; không có cách tự quét lại các resource còn URL ngoài.
- **Bắt buộc?** Có: service chỉ chạy với driver `redis` (broker bộ nhớ không đến được tiến trình khác, D-SP-3); `REDIS_URL` bắt buộc.
- **Nhạy cảm:** stream và `:dead` chứa **nội dung đầy đủ resource (S3)** và **URL gốc có thể kèm token ký (S4)** ở dạng rõ, không giới hạn độ dài và thời gian. Cần quy định `MAXLEN`/retention, mã hóa Redis và dọn `:dead` ở hạ tầng (chưa có).
- **Khối lượng:** stream mang sự kiện của **mọi** ghi resource của hệ thống (không chỉ loại có attachment); downloader đọc hết rồi bỏ qua phần không liên quan (`delete`, `skipTypes`, không có URL ngoài), nên tải đọc tỉ lệ với tổng lượng ghi. Nhóm mới tạo từ `"0"` đọc lại toàn bộ lịch sử.

## 4.3 Bộ nhớ của tiến trình

| Thứ | Nội dung | Mất khi tiến trình chết? | Hệ quả |
|---|---|---|---|
| Hàng đợi nội bộ `queue` (1024 việc) và `Workers` | **không dùng** (`Async=false`): handler chạy đồng bộ trong vòng đọc của broker | không áp dụng | không có việc nào chỉ nằm trong bộ nhớ: việc đang chạy chưa ACK được nhận lại sau `ClaimIdle` |
| Tải đang chạy | một lần tải một lúc mỗi replica (`Concurrency=1`); đọc `resp.Body` thẳng ra kho | có | mất tiến độ tải dở; có thể để lại `Binary`/blob (03 mục 3.5) |
| Cờ `draining` | readiness 503 sau SIGTERM, chờ `SHUTDOWN_DRAIN_DELAY` rồi `SHUTDOWN_TIMEOUT` cho việc đang chạy | có | message đang xử lý mà chưa ACK nằm trong PEL cho nơi khác |
| Bộ đệm ghi S3 | nội dung đệm trong bộ nhớ rồi tràn ra tệp tạm trước khi tính SHA-256 | có | bộ nhớ đỉnh theo cấu hình đệm của `s3store` (chưa đo) |

## 4.4 Bí mật: cái nào lưu thế nào

| Bí mật | Nơi | Dạng lưu | Có trong log? | Ghi chú |
|---|---|---|---|---|
| Mật khẩu CSDL (`DATABASE_URL`) | biến môi trường | bản rõ trong URL | không (không log URL; `DATABASE_ECHO` log câu SQL kèm tham số, chỉ gỡ lỗi, có thể lộ dữ liệu) | S4 |
| Mật khẩu Redis (nếu có trong `REDIS_URL`) | biến môi trường | bản rõ trong URL | không | S4 |
| Khóa S3 (`FHIR_S3_ACCESS_KEY_ID`, `FHIR_S3_SECRET_ACCESS_KEY`, `FHIR_S3_SESSION_TOKEN`) | biến môi trường | bản rõ | không | S4 |
| Token ký trong query của URL attachment | `Attachment.url` (nguồn, `fhir_history`), `resource` trong sự kiện Redis | **bản rõ** | log chỉ ghi host; `url.Error` bị bóc để không lộ URL (`download.go`) | S4: nằm lại ở lịch sử và stream (mục 4.2) |
| Khóa ký job, JWT, bí mật xác thực người dùng | không dùng | không lưu | không | service **không có** `FHIR_JOB_SIGNING_KEY`, JWKS hay khóa token; `fhir_app` không đọc được schema `identity` |

## 4.5 SQLite (dev)

`DATABASE_URL=sqlite://<tệp>` (mặc định `sqlite://./fhir.db`): một tệp dùng chung với `fhir-server` chỉ phù hợp khi chạy cùng máy; không schema, không role; **cấm ở staging/production** (`config.go`). Redis vẫn bắt buộc ngay cả khi dev.
