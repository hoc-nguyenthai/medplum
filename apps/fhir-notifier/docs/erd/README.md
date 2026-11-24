# fhir-notifier: ERD và từ điển dữ liệu

Trả lời **"fhir-notifier lưu gì, ở đâu, đọc và ghi cái gì, khóa Redis nào là của ai, nhạy cảm cỡ nào, mất Redis thì sao"**. Kiến trúc ở [../architecture.md](../architecture.md); nghiệp vụ ở [../business/](../business/README.md); cấu hình ở [../config.md](../config.md).

| # | Tài liệu | Nội dung |
|---|---|---|
| 1 | [01-bang-vat-ly.md](01-bang-vat-ly.md) | Notifier **không có bảng riêng**: dùng bảng nào, schema `fhir`, role `fhir_app`, không migrate, đọc/ghi gì ở mỗi bảng. **Bảng và cột chi tiết viết một lần** ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| 2 | [02-mo-hinh-logic.md](02-mo-hinh-logic.md) | `Subscription` (từng trường, extension, header, nhạy cảm), phần `ChangeEvent` mà notifier đọc từ stream (resource đầy đủ), `WSMessage`, resource khác chỉ đi qua stream; ERD logic |
| 3 | [03-quan-he-va-toan-ven.md](03-quan-he-va-toan-ven.md) | Quan hệ (Subscription và project, event và Subscription, bộ đếm và Subscription), tự tắt bằng `If-Match`, cache 30 giây, xóa mềm giữ lại secret, sơ đồ trạng thái |
| 4 | [04-kho-khac.md](04-kho-khac.md) | Redis: stream sự kiện và nhóm consumer `fhir-notifier` (PEL, claim, dead-letter), **khóa đếm thất bại**, kênh `fhirws.sub.<id>` và sổ hiện diện `fhirws.bound.<id>`; bộ nhớ; bảng "bí mật nào lưu thế nào" |

## Kiểm kê kho lưu trữ

| Kho | Loại | Vị trí | Ghi bởi | Đọc bởi | Bắt buộc ở staging/production |
|---|---|---|---|---|---|
| `Subscription` | PostgreSQL | schema **`fhir`**, role `fhir_app` (DML), `fhir_migrate` (DDL, chỉ khi tách role; notifier không dùng) | `fhir-server` (tạo, sửa, xóa); **`fhir-notifier` chỉ sửa một lần duy nhất** khi tự tắt (`status=off`, `error=...`) | `fhir-notifier` (nạp `status=active`, đọc lại trước khi tự tắt), `fhir-server` | có (SQLite bị cấm) |
| Resource khác (`Patient`...) | PostgreSQL | cùng schema `fhir` | `fhir-server`, `fhir-worker`, `fhir-downloader` | **notifier không đọc từ CSDL**: nội dung nằm sẵn trong sự kiện trên stream | không áp dụng |
| Sự kiện ghi resource | Redis Streams | `trivita:events:fhir.resource.changed`, nhóm `fhir-notifier` | `fhir-server`, `fhir-worker` (`XADD`) | **`fhir-notifier`** (`XREADGROUP`, `XACK`, `XCLAIM`; ghi dead-letter khi quá số lần giao) | có (`REDIS_URL` bắt buộc, không có broker bộ nhớ) |
| Bộ đếm thất bại giao | Redis ZSET | `trivita:sub:failures:<subscriptionId>` | `fhir-notifier` | `fhir-notifier` | chỉ khi `FHIR_SUBSCRIPTION_AUTO_DISABLE` khác rỗng |
| Kênh websocket | Redis Pub/Sub | `trivita:events:ch:fhirws.sub.<subscriptionId>` | **`fhir-notifier`** (`PUBLISH`, chỉ khi `FHIR_WS_MODE=issuer`) | `ws-gateway` | khi bật websocket |
| Sổ hiện diện websocket | Redis ZSET | `trivita:events:presence:fhirws.bound.<subscriptionId>` | `ws-gateway` (`Mark`/`Unmark`) | **`fhir-notifier`** (chỉ `Present`) | khi bật websocket |

Notifier **không** có Redis làm kho bền của riêng nó (mọi khóa của nó là bộ đếm hoặc kênh tạm), không có kho Binary, không có tệp cục bộ (trừ SQLite khi chạy dev).

## Mô hình sở hữu và quy tắc

- Một cụm PostgreSQL, một database, **mỗi schema một role** (D-DB-1, D-DB-2). `fhir-notifier` **dùng chung schema `fhir` và role `fhir_app` với `fhir-server`** (lợi ích của việc tách không nằm ở ranh giới dữ liệu); role `fhir_app` **không đọc được schema `identity`**; `DATABASE_REQUIRE_SCHEMA=true` từ chối chạy nếu `search_path` không phải đúng một schema riêng.
- **`fhir-server` sở hữu migration**: notifier không migrate; `fhirstore.Pending > 0` thì từ chối khởi động (`schema chưa được migrate`) (`apps/fhir-notifier/internal/bootstrap/container.go`).
- **Không có khóa ngoại**: `Subscription` là JSON trong `fhir_resource`; quan hệ là chuỗi (`meta.project`), id trong khóa Redis, và tên loại resource ở `criteria`; có test cấm FK chéo schema (`tests/dbmodel`).
- Notifier ghi Subscription qua **kho gốc `fhirstore.Store`, không qua `fhirsub.Repo`** nên lần tự tắt **không phát sự kiện** lên stream (xem 03 mục 3.4).

## Nguồn sự thật và cách kiểm

- Mã: `pkg/fhirsub/{manager,deliver,failure,event,ws_remote,repo}.go`, `pkg/messaging/{redis,redis_channels}.go`, `apps/fhir-notifier/internal/{bootstrap,config}`; struct `pkg/fhir/resource_subscription.go`.
- Kiểm: `python3 -I ~/.claude/skills/service-erd-docs/scripts/check_erd_docs.py --docs apps/fhir-notifier/docs/erd --migrations pkg/fhirstore/migrations/sqlite --resources Subscription --repo .`; các dòng "thiếu mục cho bảng `fhir_*`" là **chủ ý** (bảng được mô tả một lần ở tài liệu dùng chung, như `apps/fhir-worker/docs/erd`).
- Chưa chạy trên Redis hoặc PostgreSQL thật khi viết (chưa kiểm): mọi tên khóa, TTL, kiểu giá trị lấy từ mã.

> **Điểm phát hiện khi viết (chưa sửa mã):**
> 1. **`secret` và header xác thực của Subscription lưu bản rõ** trong `fhir_resource` **và mọi phiên bản cũ trong `fhir_history`**; lần tự tắt của notifier chép lại toàn bộ Subscription (kể cả secret) thành một phiên bản mới ([03](03-quan-he-va-toan-ven.md)).
> 2. **Stream chứa JSON đầy đủ của mọi resource** (dữ liệu lâm sàng S3, mọi project) và không có `MAXLEN`; nhóm `fhir-notifier` tạo với vị trí `0` nên **nhóm mới đọc lại toàn bộ lịch sử stream còn lại** ([04](04-kho-khac.md) mục 4.1).
> 3. **Tên kênh/khóa vật lý có tiền tố**: tài liệu nghiệp vụ ghi `fhirws.sub.<id>` và `fhirws.bound.<id>`, trên Redis thật là `trivita:events:ch:fhirws.sub.<id>` và `trivita:events:presence:fhirws.bound.<id>`. Hai phía (notifier, ws-gateway) phải cùng tiền tố `trivita:events` (cả hai đang dùng mặc định, không cấu hình được qua biến môi trường).
> 4. **Tự tắt không phát sự kiện**: replica khác và `fhir-server` chỉ thấy `status=off` sau tối đa `CacheTTL` 30 giây.
> 5. **Tên trường JSON của sự kiện** là `resource_type`, `version_id`, `event_id`, `occurred_at` (snake_case); [../../../fhir-worker/docs/erd/04-kho-khac.md](../../../fhir-worker/docs/erd/04-kho-khac.md) mục 4.2 ghi `resourceType` (lệch nhỏ ở tài liệu của worker, chưa sửa).
