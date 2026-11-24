# fhir-downloader: ERD và từ điển dữ liệu

Trả lời **"fhir-downloader đọc và ghi gì, ở đâu, nối với nhau thế nào, nhạy cảm cỡ nào, xóa và mất dữ liệu thì sao"**. Kiến trúc ở [../architecture.md](../architecture.md); nghiệp vụ ở [../business/](../business/README.md); cấu hình ở [../config.md](../config.md).

| # | Tài liệu | Nội dung |
|---|---|---|
| 1 | [01-bang-vat-ly.md](01-bang-vat-ly.md) | Service dùng bảng nào, schema `fhir`, role `fhir_app`, không migrate, một lần tải để lại bao nhiêu dòng, bảng lớn nhanh. **Bảng và cột chi tiết viết một lần** ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| 2 | [02-mo-hinh-logic.md](02-mo-hinh-logic.md) | `Binary` do service ghi (từng trường), resource nguồn có `Attachment` mà service đọc và cập nhật, mức nhạy cảm, ERD logic |
| 3 | [03-quan-he-va-toan-ven.md](03-quan-he-va-toan-ven.md) | Quan hệ, thứ tự ghi và bù trừ, đồng thời (`If-Match`), xóa mềm của `Binary` bị dọn, mồ côi, vòng đời một lần tải |
| 4 | [04-kho-khac.md](04-kho-khac.md) | Redis (stream, nhóm consumer `fhir-downloader`, PEL, dead-letter, **không có khóa theo dõi thất bại**), kho Binary `file:`/`s3:`, bộ nhớ, bí mật |

## Kiểm kê kho lưu trữ

| Kho | Loại | Vị trí | Ghi bởi | Đọc bởi | Bắt buộc ở staging/production |
|---|---|---|---|---|---|
| Resource nguồn (có `Attachment`) và `Binary` | PostgreSQL | schema **`fhir`**, role `fhir_app` (DML); `fhir_migrate` chỉ `fhir-server migrate up` dùng | `fhir-server` (resource nguồn); `fhir-downloader` (tạo `Binary`, **cập nhật resource nguồn**, xóa mềm `Binary` khi bù trừ) | `fhir-server`, `fhir-downloader` | có (SQLite bị cấm ở staging/production, `config.go`) |
| Dữ liệu của dev | SQLite | `DATABASE_URL=sqlite://...` | cả hai | cả hai | không |
| Nội dung tệp tải về | kho Binary | `FHIR_BINARY_STORAGE`: `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`, khóa `binary/<id>/<versionId>` | `fhir-downloader` | `fhir-server` (`GET Binary/{id}`) | có (config bắt buộc; **phải trùng** `fhir-server`) |
| Sự kiện ghi resource | Redis Streams | stream `trivita:events:fhir.resource.changed`, nhóm consumer `fhir-downloader` (`BROKER_GROUP`) | `fhir-server` và `fhir-downloader` (`XADD` lần cập nhật attachment) | `fhir-downloader` (đọc bằng nhóm), `fhir-server` (nhóm `trivita`), `fhir-notifier` | có (service chỉ chạy với driver `redis`) |
| Dead-letter | Redis Streams | `trivita:events:fhir.resource.changed:dead` | broker (lần giao thứ 5 vẫn lỗi) | không ai (chưa có công cụ) | tự phát sinh |
| Khóa theo dõi thất bại | Redis | **không có** cho service này (mục 4.2) | | | |

**Không có** bảng riêng, migration riêng, hàng đợi ngoài Redis, bộ đệm bền hay tệp cục bộ ngoài kho `file:` (nếu dùng).

## Mô hình sở hữu và quy tắc

- Một cụm PostgreSQL, một database, mỗi schema một role (D-DB-1, D-DB-2). `fhir-downloader` **dùng chung schema `fhir` và role `fhir_app` với `fhir-server`** (D-SP-8, tiền lệ `dicomweb`). Role `fhir_app` không đọc được schema `identity`; `DATABASE_REQUIRE_SCHEMA=true` từ chối chạy nếu `search_path` không phải đúng một schema riêng (`container.go` qua `database.Open`).
- **`fhir-server` sở hữu migration**: service không migrate; `fhirstore.Pending > 0` thì từ chối khởi động (`schema chưa được migrate: chạy fhir-server migrate up`, `container.go`).
- **Không có khóa ngoại**: quan hệ là chuỗi trong JSON (`Attachment.url` = `Binary/<id>`, `Binary.securityContext` = `<Loại>/<id>`, `meta.project`); có test cấm FK chéo schema (`tests/dbmodel`).
- Service **không có người gọi** (không token), nên không có kho tách project: nó ghi bằng kho gốc và **tự gán** `meta.project` của `Binary` bằng project của resource nguồn.

## Nguồn sự thật và cách kiểm

- Mã: `pkg/fhirdownload/download.go` (toàn bộ nghiệp vụ), `apps/fhir-downloader/internal/bootstrap/container.go`, `internal/config/config.go`, `pkg/messaging/redis.go`, `pkg/fhirsub/{repo,event,failure}.go`, `pkg/binarystore/{binarystore,fs}.go`, `pkg/binarystore/s3store`; struct `pkg/fhir` (`Binary`, `Attachment`).
- Kiểm: `python3 -I ~/.claude/skills/service-erd-docs/scripts/check_erd_docs.py --docs apps/fhir-downloader/docs/erd --migrations pkg/fhirstore/migrations/sqlite --resources Binary --repo .`. Kết quả mong đợi: 13 dòng "thiếu mục cho bảng `fhir_*`" là **chủ ý** (bảng được mô tả một lần ở tài liệu dùng chung, giống `apps/fhir-worker/docs/erd`).
- Chưa kiểm trên PostgreSQL thật, chưa đo số dòng `fhir_history` của một lần tải bằng chạy thật (mục 1.4 suy từ mã). Chưa render Mermaid.

> **Điểm phát hiện khi viết (chưa sửa mã; chi tiết ở 03 và 04):**
> 1. **Không có "khóa theo dõi thất bại" nào của downloader.** `RedisFailureTracker` (`trivita:sub:failures:<subscriptionId>`) chỉ phục vụ tự tắt Subscription ở `fhir-server` và `fhir-notifier`. Số lần thất bại của downloader là bộ đếm giao (PEL) nằm trong chính Redis Streams; không có bộ đếm theo URL hay theo máy chủ ngoài.
> 2. **Sự kiện mang cả nội dung resource** (`Resource` trong `ChangeEvent`), kể cả URL gốc có thể kèm token ký trong query: nằm trong stream và trong `:dead` **không giới hạn độ dài và thời gian** (không `MAXLEN`/`XTRIM`).
> 3. **URL gốc bị thay bằng `Binary/<id>` nhưng vẫn nằm lại trong `fhir_history`** của resource nguồn (phiên bản trước khi đổi); token ký trong query (nếu có) cũng nằm lại.
> 4. **Bù trừ là xóa mềm** (`drop` gọi `DeleteResource`): mỗi lần thử hỏng sau khi đã tạo `Binary` để lại tombstone và 2 dòng `fhir_history` (tối đa 5 lần giao mỗi sự kiện).
> 5. **`Attachment.size` và `hash` không được cập nhật** sau khi tải: chỉ `url` (và `contentType` nếu trống) đổi, nên giá trị người gửi khai có thể lệch với tệp thật.
> 6. **Nhóm consumer mới tạo từ đầu stream (`"0"`)**: lần đầu bật service sẽ đọc lại toàn bộ lịch sử stream và tải mọi attachment ngoài còn sót (backfill ngầm).
> 7. **`ClaimIdle` mặc định 30 giây bằng hạn tải mặc định 30 giây**: ở nhiều replica, một lần tải chậm có thể bị replica khác nhận lại khi đang chạy (xử lý trùng; `If-Match` và dọn dẹp chặn kết quả sai nhưng có thể tải hai lần).
