# 1. Bảng vật lý của fhir-server

`fhir-server` **không có bảng riêng ngoài bộ bảng "kho tài liệu FHIR" của `pkg/fhirstore`**. Bộ bảng này giống hệt ở mọi service dùng `fhirstore` và được mô tả **một lần, đầy đủ ghi chú chức năng từng cột**, ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md). Tệp này chỉ nêu phần **riêng của fhir-server**: schema, role, cách chạy migration, backend, ai đọc được, và các lệnh CLI chạm DB.

## 1.1 Schema, role, quyền

| Chủ đề | Giá trị | Nguồn |
|---|---|---|
| Database | một database PostgreSQL dùng chung với `auth` (mô hình một database nhiều schema, D-DB-1, D-DB-2) | `deploy/postgres-init/01-schemas.sh` |
| Schema | **`fhir`** (`CREATE SCHEMA IF NOT EXISTS fhir AUTHORIZATION fhir_app`); `search_path` đặt ở mức **role** (`ALTER ROLE fhir_app SET search_path = fhir`), không phụ thuộc DSN, chạy đúng cả sau PgBouncer | `01-schemas.sh` |
| Role chạy ứng dụng | `fhir_app`. Chế độ 1 (mặc định, dev): **sở hữu** schema nên có cả DDL, app tự migrate. Chế độ 2 (`DB_SEPARATE_MIGRATION_ROLES=true`, staging/production, D-DB-4): chỉ `USAGE` schema và `SELECT/INSERT/UPDATE/DELETE` trên bảng, `USAGE/SELECT` trên sequence (cấp qua `ALTER DEFAULT PRIVILEGES FOR ROLE fhir_migrate`) | `01-schemas.sh` |
| Role migration | `fhir_migrate` (chỉ chế độ 2): sở hữu schema và bảng, chạy DDL; **không bao giờ bị đặt `statement_timeout`** (runbook mục 3.7). `DATABASE_MIGRATE_URL` trỏ tới role này | `01-schemas.sh`, `cli.go:Migrate` |
| Timeout câu lệnh | `FHIR_STATEMENT_TIMEOUT` đặt cho `fhir_app` nếu khai; **mặc định không đặt** vì `$reindex` và bulk export chạy lâu dưới role này | `01-schemas.sh` |
| Role bị cấm | `fhir_app` và `fhir_migrate` **không có quyền gì** trên schema `identity` và ngược lại; không ai tạo được bảng ở `public` | `tests/dbmodel/dbmodel_test.go` (`noCrossAccess`) |
| `DATABASE_REQUIRE_SCHEMA` | `true`: `database.Open` đọc `SHOW search_path`, từ chối nếu không phải đúng một schema riêng đã tồn tại (không `public`, không nhiều schema); áp dụng cho cả `serve` và `migrate`, `assign-project`, `sweep-binary`, `seed`. Mặc định `false` (không phá cách chạy cũ); ở staging/production không bật thì chỉ **cảnh báo** lúc khởi động. Bật với SQLite/MySQL là lỗi cấu hình | `pkg/database/database.go`, `container.go`, `config.go:510` |
| Kết nối | `DATABASE_MAX_OPEN_CONNS` mặc định 20 mỗi replica, `DATABASE_MAX_IDLE_CONNS` 5, `DATABASE_CONN_MAX_LIFETIME` 30 phút; tổng mọi replica của mọi service dùng schema này phải nhỏ hơn `max_connections` | `config.md` |

## 1.2 Ai đọc và ghi schema `fhir`

| Service | Quyền thực tế | Migrate? | Ghi chú |
|---|---|---|---|
| `fhir-server` | đọc và ghi mọi loại resource, mọi bảng | **Có (duy nhất)** | `serve` tự migrate khi `DATABASE_AUTO_MIGRATE=true`; hoặc `fhir-server migrate up` |
| `fhir-downloader` | đọc nguồn, ghi `Binary`, cập nhật nguồn | không | từ chối khởi động nếu `Pending > 0` |
| `fhir-notifier` | đọc Subscription; ghi khi tự tắt Subscription | không | cùng schema và role |
| `fhir-worker` | đọc và ghi `AsyncJob`, kết quả export, `$reindex` | không | cùng schema và role |
| `dicomweb` | các loại DICOM | không | cùng schema `fhir` (tiền lệ D-SP-8) |
| `auth` | **không** | không | dùng schema `identity` với role `identity_app`; chỉ gọi `fhir-server` qua HTTP (đọc `Binary`) |

Quyền ở mức DB **không thu hẹp theo service**: mọi service dùng chung `fhir_app` và `fhir_resource` chứa mọi loại; ranh giới nằm ở mã từng service (xem `docs/service-principles.md`). Một lỗ hổng ở một service tách là đọc và ghi được toàn bộ dữ liệu lâm sàng của schema.

## 1.3 Chạy migration thế nào

Bảng phiên bản goose là **`fhirstore_schema_version`** (`fhirstore.MigrationTable`); nó do goose tạo khi chạy migration đầu tiên, **không có trong tệp migration** (có trong [fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md)). Có ba migration (`pkg/fhirstore/migrations/{postgres,sqlite}/00001_init.sql`, `00002_coding.sql`, `00003_concept_mapping.sql`).

| Cách | Khi nào | Hành vi |
|---|---|---|
| `DATABASE_AUTO_MIGRATE=true` (mặc định) | dev, một replica | `container.go` gọi `Store.Migrate` lúc khởi động. SQLite `:memory:` luôn migrate |
| `DATABASE_AUTO_MIGRATE=false` + `fhir-server migrate up` | nhiều replica, rolling update | `Store.Pending > 0` thì **từ chối khởi động** (`schema chưa được migrate: chạy fhir-server migrate up ...`); đó là cơ chế an toàn |
| `fhir-server migrate status` | kiểm trước deploy | in `migration chưa áp dụng: N`; không có tác dụng phụ (goose có thể tạo bảng phiên bản nếu chưa có) |
| `fhir-server migrate down` | **không dùng ở production** | lùi **một** bước, xóa dữ liệu của bảng bị gỡ; quay lại bản app cũ thì khôi phục từ sao lưu (runbook mục 10) |
| `DATABASE_MIGRATE_URL` (role `fhir_migrate`) | staging/production tách role | chỉ lệnh `migrate` dùng URL này; **đặt nó thì `DATABASE_AUTO_MIGRATE` phải `false`** (cấu hình từ chối nếu không); chỉ PostgreSQL |

- Thứ tự deploy: PostgreSQL (và Redis) → `migrate up` (role migrate) → `auth` → `fhir-server` và các service tách (runbook mục 4.2).
- **Nhiều replica cùng bật `DATABASE_AUTO_MIGRATE=true`**: `database.Migrate` gọi `goose.Provider.Up` không khai khóa phiên (không thấy `WithSessionLocker` trong `pkg/database/database.go`), nên hai replica khởi động cùng lúc có thể cùng chạy migration (chưa kiểm hành vi thực tế); vì vậy runbook yêu cầu tắt tự migrate khi chạy nhiều replica.
- Migration mới **tạo bằng role nào thì bảng thuộc role đó**: tạo bằng superuser thì `fhir_app` báo `permission denied for table` (runbook mục 3.4, bảng sự cố).
- Các service tách không migrate nhưng **bị khóa theo** migration của fhir-server: nâng `fhir-server` trước, rồi mới nâng service tách.

## 1.4 Backend

| Backend | Trạng thái với `fhirstore` | Khác biệt |
|---|---|---|
| PostgreSQL | **hỗ trợ**, dùng cho staging/production | `content` kiểu `jsonb`, `seq` tự tăng, schema riêng, role riêng, `pg_advisory_xact_lock(hashtextextended(key,0))` cho `LockKey` (`store.go:124`) |
| SQLite (`sqlite://<tệp>`, `sqlite://:memory:`) | **hỗ trợ cho dev**; mặc định `sqlite://./fhir.db` (thuần Go `modernc.org/sqlite`, không CGO) | `content` là text, không schema, không role; `LockKey` là một câu `DELETE ... WHERE 1 = 0` để chiếm khóa ghi (`store.go:115`); **bị cấm ở staging/production** (`config.go:656`); `DATABASE_REQUIRE_SCHEMA` với SQLite là lỗi |
| MySQL (`mysql://`) | **chưa hỗ trợ** cho `fhirstore`: `database.Open` mở được nhưng `fhirstore.New` trả `dialect "mysql" chưa được hỗ trợ` (`store.go:42`); `DATABASE_MIGRATE_URL` cũng chỉ nhận `postgres:` | |

Tệp `apps/fhir-server/fhir.db` trong cây làm việc là SQLite dev; đã được `.gitignore` (`*.db`).

## 1.5 Lệnh CLI chạm DB (cùng `database.Open`, cùng `RequireSchema`)

| Lệnh | DB | Điều kiện | Tác dụng |
|---|---|---|---|
| `fhir-server migrate up|down|status` | `DATABASE_MIGRATE_URL` nếu có, không thì `DATABASE_URL` | PostgreSQL hoặc SQLite | chạy hoặc kiểm migration |
| `fhir-server assign-project -project <id> [-type ...] [-dry-run]` | `DATABASE_URL` | không dùng được với bộ nhớ; từ chối khi `Pending > 0` | gán `meta.project` cho resource chưa có (ghi trực tiếp, không qua HTTP, **không phát sự kiện Redis**) |
| `fhir-server sweep-binary [-grace 24h] [-max-delete 1000] [-dry-run] [-force]` | `DATABASE_URL` + kho Binary | cần `FHIR_BINARY_STORAGE`; từ chối khi `Pending > 0` | xóa nội dung mồ côi ở kho (04 mục 4.3) |
| `fhir-server seed definitions [-project] [-dir] [-type] [-dry-run]` | `DATABASE_URL` | từ chối khi `Pending > 0` | nạp `SearchParameter`, `StructureDefinition`, `ValueSet`... vào `fhir_resource` (và lập chỉ mục) |

Các lệnh ghi trực tiếp vào DB **không đi qua `fhirsub.Wrap`**, nên không phát `ChangeEvent`: Subscription, `fhir-notifier`, `fhir-downloader` không thấy các thay đổi này (suy từ `cli.go`: dùng `store` gốc, chưa kiểm bằng chạy).

## 1.6 Hệ quả vận hành riêng của fhir-server

| Chủ đề | Hệ quả |
|---|---|
| Bảng lớn nhanh | `fhir_history` (mỗi lần ghi một dòng kèm cả nội dung, không có đường dọn ngoài `Expunge`) và `fhir_idx_*` (ghi lại toàn bộ chỉ mục của resource ở mỗi lần cập nhật, `reindex`); xem [fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| Job nền | `AsyncJob` (bulk export, `$reindex`, batch async) là **resource trong cùng bảng**; kết quả bulk export là `Binary` (khi có kho Binary: nội dung ở kho, `Expunge` khi hết `FHIR_EXPORT_RETENTION` nên xóa cả dòng, lịch sử và tệp). Lease job nằm ở `AsyncJob.transactionTime`, nhận job bằng `If-Match` (`jobs.go`) |
| `$reindex` cả loại | chạy dưới `fhir_app`, có thể rất lâu; không có `statement_timeout` mặc định (cố ý) |
| Dữ liệu cũ khi đổi mô hình | `fhir_history` giữ mọi phiên bản cũ; xóa mềm (`deleted=true`) vẫn giữ nội dung |
| Tách theo project | `meta.project` là chuỗi trong JSON, không có cột riêng; JWT bật thì `fhirauth.Scope` lọc ở tầng mã, **không ở tầng DB** (không có RLS) |
