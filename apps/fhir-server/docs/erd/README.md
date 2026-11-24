# fhir-server: ERD và từ điển dữ liệu

Trả lời **"fhir-server đọc và ghi gì, ở đâu, nối với nhau thế nào, nhạy cảm cỡ nào, xóa và mất dữ liệu thì sao"**. Kiến trúc ở [../architecture.md](../architecture.md); nghiệp vụ ở [../business/](../business/README.md); cấu hình ở [../config.md](../config.md); vận hành ở [../runbook.md](../runbook.md).

`fhir-server` là **chủ sở hữu schema `fhir`**: nó duy nhất chạy migration, và là service duy nhất ghi mọi loại resource của kho tài liệu. Các service tách (`fhir-downloader`, `fhir-notifier`, `fhir-worker`) và `dicomweb` dùng chung schema nhưng **không migrate**.

| # | Tài liệu | Nội dung |
|---|---|---|
| 1 | [01-bang-vat-ly.md](01-bang-vat-ly.md) | Phần riêng của fhir-server: schema `fhir`, role `fhir_app`/`fhir_migrate`, chạy migration thế nào (`DATABASE_AUTO_MIGRATE`, `migrate up`, `DATABASE_MIGRATE_URL`), SQLite dev, MySQL chưa hỗ trợ, ai đọc được schema, CLI chạm DB. **Bảng và cột chi tiết viết một lần** ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| 2 | [02-mo-hinh-logic.md](02-mo-hinh-logic.md) | Các loại resource lưu trong `fhir_resource.content`, trường, mức nhạy cảm, ERD logic (agent khác viết) |
| 2b | [02b-tu-vung.md](02b-tu-vung.md) | Từ vựng, terminology: `fhir_coding*`, `fhir_concept_mapping` (agent khác viết) |
| 3 | [03-quan-he-va-toan-ven.md](03-quan-he-va-toan-ven.md) | Quan hệ logic, toàn vẹn, transaction, xóa (agent khác viết) |
| 4 | [04-kho-khac.md](04-kho-khac.md) | Redis (stream `trivita:events:fhir.resource.changed`, nhóm `trivita`, `trivita:sub:failures`, kênh `fhirws.*`, hạn mức `fhir:rl:`), khóa ký, kho Binary `file:`/`s3:`, bộ nhớ, SQLite, bảng "bí mật nào lưu thế nào", mất Redis thì sao, cờ tách service |

> Ba tệp 02, 02b, 03 do người khác viết song song; liên kết có thể chưa trỏ tới tệp tồn tại ở thời điểm tệp này được viết.

## Kiểm kê kho lưu trữ

| Kho | Loại | Vị trí | Ghi bởi | Đọc bởi | Bắt buộc ở staging/production |
|---|---|---|---|---|---|
| Kho tài liệu FHIR (`fhir_resource`, `fhir_history`, `fhir_idx_*`, `fhir_coding*`, `fhir_concept_mapping`, `fhirstore_schema_version`) | PostgreSQL | schema **`fhir`**, role `fhir_app` (DML), `fhir_migrate` (DDL, chỉ khi tách role) | `fhir-server` (mọi loại); `fhir-downloader` (`Binary`, cập nhật resource nguồn); `fhir-worker` (job, kết quả); `fhir-notifier` (Subscription: đọc, tự tắt là ghi); `dicomweb` (loại DICOM) | `fhir-server`, các service trên | có (SQLite bị cấm: `config.go:656`) |
| Dữ liệu của dev | SQLite | `DATABASE_URL=sqlite://./fhir.db` (mặc định), `sqlite://:memory:` | `fhir-server` | `fhir-server` | không |
| Sự kiện ghi resource | Redis Streams | `trivita:events:fhir.resource.changed`, nhóm consumer `trivita` | `fhir-server` (mọi lần ghi khi `FHIR_SUBSCRIPTIONS` hoặc `FHIR_AUTO_DOWNLOAD` bật) | nhóm `trivita` (`fhir-server`: Subscription, tự tải nếu chạy trong tiến trình), nhóm `fhir-downloader`, nhóm `fhir-notifier` | chỉ khi `BROKER_DRIVER=redis` (bắt buộc khi tách service) |
| Dead-letter | Redis Streams | `trivita:events:fhir.resource.changed:dead` | broker (lần giao thứ 5 vẫn lỗi) | không ai (chưa có công cụ) | tự phát sinh |
| Khóa đếm thất bại | Redis sorted set | `trivita:sub:failures:<subscriptionId>` | `fhir-server` (khi `FHIR_SUBSCRIPTION_DELIVERY=true` và broker Redis) | cùng service | tùy chọn (cần `FHIR_SUBSCRIPTION_AUTO_DISABLE`) |
| Kênh và sổ hiện diện websocket (phía phát) | Redis Pub/Sub + sorted set | `trivita:events:ch:fhirws.sub.<id>`, `trivita:events:presence:fhirws.bound.<id>` | kênh: tiến trình giao (`fhir-server` khi `FHIR_SUBSCRIPTION_DELIVERY=true`, `fhir-notifier` khi `false`) ở chế độ `issuer`; sổ hiện diện: **`ws-gateway`** | gateway nghe kênh; tiến trình giao đọc sổ | chỉ khi `FHIR_WS_MODE=issuer` |
| Bộ đếm hạn mức | Redis hash | `fhir:rl:api:<ip>`, `fhir:rl:storage:<ip>` | `fhir-server` | `fhir-server` | không (`FHIR_RATE_LIMIT_SHARED=false` mặc định: đếm trong bộ nhớ) |
| Nội dung Binary thô | kho đối tượng | `FHIR_BINARY_STORAGE`: `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`, khóa `binary/<id>/<versionId>` | `fhir-server` (upload, kết quả job); `fhir-downloader`; `fhir-worker` (kết quả job) | `fhir-server` (`GET Binary`, `/storage`) | tùy chọn (rỗng: Binary giữ `data` base64 trong DB); bắt buộc khi tách service tải/worker |
| Khóa ký (không phải dữ liệu) | biến môi trường, tệp PEM | `FHIR_WS_SIGNING_KEY`, `FHIR_JOB_SIGNING_KEY`, `FHIR_STORAGE_SIGNING_KEY_PATH` | vận hành | `fhir-server` (và bên kiểm: `ws-gateway`, `fhir-worker`) | có theo điều kiện (04 mục 4.4) |
| Cache Subscription, hàng đợi webhook, Hub websocket, bộ đếm thất bại bộ nhớ | bộ nhớ tiến trình | | `fhir-server` | cùng tiến trình | không bền |

## Mô hình sở hữu và quy tắc

- Một cụm PostgreSQL, một database, mỗi schema một role (D-DB-1, D-DB-2). Role `fhir_app` không đọc được schema `identity`; `DATABASE_REQUIRE_SCHEMA=true` từ chối chạy nếu `search_path` không phải đúng một schema riêng (`pkg/database/database.go`, `requireSchema`). Chi tiết ở [01](01-bang-vat-ly.md).
- **Không có khóa ngoại**: quan hệ là logic; `tests/dbmodel` cấm FK chéo schema (`crossSchemaFKs`).
- **Các cờ tách service quyết định ai ghi kho nào**: `FHIR_SUBSCRIPTION_DELIVERY=false` (giao và khớp sang `fhir-notifier`), `FHIR_AUTO_DOWNLOAD_WORKER=false` (tải sang `fhir-downloader`), `FHIR_JOB_EXECUTION=false` (job sang `fhir-worker`). Bảng đầy đủ ở [04 mục 4.7](04-kho-khac.md).

## Nguồn sự thật và cách kiểm

- Mã: `apps/fhir-server/internal/bootstrap/{container,cli,storage}.go`, `internal/config/config.go`, `pkg/database/database.go`, `pkg/fhirstore/{store,migrations}`, `pkg/messaging/{messaging,redis,redis_channels}.go`, `pkg/fhirsub/{repo,failure,manager,ws_remote,ws_token}.go`, `pkg/ratelimit`, `pkg/fhirbinary/{wrap,sweep,presign}.go`, `pkg/binarystore/{fs,signer}.go`, `deploy/postgres-init/01-schemas.sh`, `tests/dbmodel`.
- Kiểm: `python3 -I ~/.claude/skills/service-erd-docs/scripts/check_erd_docs.py --docs apps/fhir-server/docs/erd --migrations pkg/fhirstore/migrations/sqlite --resources <danh sách của 02> --repo .` (các dòng "thiếu mục cho bảng `fhir_*`" là chủ ý vì bảng viết ở tài liệu dùng chung).
- **Chưa kiểm trên PostgreSQL và Redis thật** khi viết 01 và 04; chưa render Mermaid. Mọi dữ kiện đọc từ mã.

> **Điểm phát hiện khi viết (chưa sửa mã; chi tiết ở 01 và 04):**
> 1. **Stream sự kiện không có giới hạn độ dài và thời gian** (`XAdd` không `MAXLEN`, không `XTRIM`) và mỗi entry mang **cả JSON resource** (S3): stream và `:dead` là bản sao lâm sàng thứ hai trong Redis.
> 2. **`fhirbinary.Sweep` coi khóa là "còn sống" nếu có bất kỳ phiên bản Binary nào, kể cả bản xóa mềm (HTTP 410)**: xóa mềm `Binary` không giải phóng nội dung; chỉ `Expunge` mới xóa tệp (`wrap.go`: `DeleteResource` không được ghi đè, chỉ `Expunge`).
> 3. **`config.md` và mô tả `FHIR_BINARY_STORAGE` trong `config.go` chỉ nói `file:`** trong khi mã hỗ trợ cả `s3:` (`storage.go`, `StorageKind`); chỉ là chú thích cũ.
> 4. **Mất Redis lúc ghi resource là mất sự kiện**: `fhirsub.Wrap` chỉ log `phát sự kiện lỗi` và vẫn trả thành công cho client (không outbox).
> 5. **Hạn mức dùng chung fail-open có giới hạn**: Redis lỗi thì mỗi replica đếm riêng (tổng hạn mức nhân với số replica).
> 6. **Nhóm consumer `trivita` tạo từ `"0"`** nên lần đầu bật broker đọc lại toàn bộ stream còn tồn tại.
> 7. **Khóa ký job dev HMAC được suy từ `FHIR_JWT_HMAC_SECRET`** (`sha256("fhir-job-seal|"+secret)`, `container.go`); ở staging/production bị cấm nên chỉ là dev.
