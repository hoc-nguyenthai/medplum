# Kế hoạch tổ chức microservice theo nghiệp vụ, dùng chung một PostgreSQL

Trạng thái: **P1, P2 và P3a (điều tra, mục 14) đã xong (2026-11-22); P3b trở đi chưa làm** (xem mục 12, 13, 14). D-DB-1 (một database nhiều schema) và D-DB-2 (`identity` chung database, kèm role riêng và test quyền) đã chốt; D-DB-3 đến D-DB-5 còn mở. Yêu cầu của người dùng: tách service theo nghiệp vụ nhưng **vẫn dùng chung DB**. Tài liệu này trả lời: service nào, mỗi service sở hữu dữ liệu nào, dùng chung DB bằng cách nào mà không biến thành một khối dính nhau, và làm theo thứ tự nào. Nguyên tắc tổng quát: [../service-principles.md](../service-principles.md). Bổ sung cho [modules/split-scim-ws-services.md](modules/split-scim-ws-services.md) (đã tách `ws-gateway`). Mọi `file:dòng` và mọi câu "hiện nay" là của repo này, đã đọc ngày 2026-11-22; câu nào ghi "chưa kiểm" là chưa đọc tới mã.

## 0. Kết luận

| Điều | Đề xuất |
|---|---|
| Cách dùng chung | **Một cụm PostgreSQL, một database, mỗi service một schema và một role.** Không phải "mọi service chạm mọi bảng". |
| Vì sao schema riêng là bắt buộc, không phải tùy chọn | `auth` đã dùng **cùng bộ bảng `fhir_*` và cùng bảng phiên bản `fhirstore_schema_version`** của `pkg/fhirstore` (mục 1). Gộp hai app vào một schema thì trùng tên bảng. |
| Service theo nghiệp vụ | `identity` (= `apps/auth`), `clinical` (= `apps/fhir-server`), `realtime` (= `apps/ws-gateway`), và `fhir-worker` (cùng binary `fhir-server`, chế độ khác). Các dịch vụ nền (`mcp`, `email`, `storage`, `keyvalue`) chỉ tách khi có hơn một nơi gọi. |
| Không tách nhỏ hơn | `clinical` **không** chia theo loại resource: search, reference và transaction của FHIR xuyên qua mọi loại. |
| Giữ tên thư mục `apps/*` | Không đổi tên `auth`, `fhir-server`, `ws-gateway` (đổi tên chỉ tốn công: import, Dockerfile, compose, K8s, tài liệu). Tên "identity/clinical/realtime" là tên nghiệp vụ trong tài liệu. |
| Được gì khi dùng chung | Một cụm để backup/PITR, theo dõi và nâng cấp; một chỗ đặt PgBouncer; migration dữ liệu không cần đồng bộ nhiều cụm. |
| Mất gì | Chung miền lỗi và chung tài nguyên (truy vấn nặng của service này làm chậm service kia); migration của service này có thể khóa bảng gây ảnh hưởng nơi khác; muốn scale hoặc deploy hoàn toàn độc lập sau này thì vẫn phải tách DB lần nữa. Các quy tắc ở mục 4 làm cho lần tách đó rẻ. |
| Việc này thay đổi một quy tắc đang có | `apps/auth/internal/infrastructure/store/store.go:3-4` ghi "phải nằm trong database RIÊNG" và `deploy/postgres-init/01-auth.sql` tạo database `auth`. Kế hoạch này **thay thế** quy tắc đó bằng "schema riêng + role riêng + test quyền" nếu D-DB-1 và D-DB-2 được chọn như đề xuất. |

Một sự thật làm kế hoạch rẻ hơn tưởng: **hiện không có service nào đọc dữ liệu của service khác qua DB.** `fhir-server` xác minh JWT bằng JWKS của `auth`, `ws-gateway` không có DB. Vì vậy cách ly nghiêm ngặt (role không có quyền trên schema khác) không làm hỏng chức năng nào đang chạy.

## 1. Hiện trạng (bằng chứng)

| Điều | Vị trí |
|---|---|
| Hai app, hai database, cùng một máy chủ PostgreSQL trong compose: `trivita` (fhir-server) và `auth` | `docker-compose.yml:24,49`, `deploy/postgres-init/01-auth.sql` |
| `auth` lưu User, Login, UserSecurityRequest... bằng `fhirrouter.Repository` chạy trên `pkg/fhirstore` (cùng bộ bảng với kho lâm sàng) | `apps/auth/internal/infrastructure/store/store.go:1-4`, `apps/auth/internal/bootstrap/cli.go:23` |
| Bộ bảng: `fhir_resource`, `fhir_history`, `fhir_idx_string/token/reference/number/quantity/uri/date` (9 `CREATE TABLE`), tạo không kèm tên schema, không có `CREATE EXTENSION`, type hay function | `pkg/fhirstore/migrations/postgres/00001_init.sql` |
| Một bảng phiên bản goose cho mỗi app, tên cố định `fhirstore_schema_version` | `pkg/fhirstore/store.go:25-26`, `pkg/database/database.go:202-225` |
| Migration chạy lúc khởi động nếu `DATABASE_AUTO_MIGRATE=true`, hoặc bằng lệnh `<app> migrate up` | `apps/*/internal/bootstrap/container.go`, `cli.go` |
| Đã có test dùng `search_path` trong DSN để cô lập schema trên PostgreSQL thật (pgx truyền tham số này lên server) | `pkg/fhirstore/postgres_test.go:22,51`, `pkg/fhirauth/postgres_test.go:44` |
| Mỗi app mở tối đa **20** kết nối mặc định (`DATABASE_MAX_OPEN_CONNS`) | `pkg/database/database.go:112-118`, `apps/*/internal/config/config.go` |
| Job nền (`AsyncJob`, `$reindex`, `$export`) chạy bằng `pkg/workflow`, mặc định trong process; có adapter `asynq` và `temporal`; lease cho phép replica khác tiếp quản job mồ côi | `apps/fhir-server/README.md` (mục "Operation và job nền"), `pkg/workflow/` |
| Giao Subscription rest-hook nằm trong `pkg/fhirsub.Manager`, chạy trong `fhir-server` sau cờ `FHIR_SUBSCRIPTIONS` | `apps/fhir-server/README.md`, `pkg/fhirsub/manager.go` |
| `pkg/database` hỗ trợ PostgreSQL, MySQL và SQLite | `pkg/database/database.go:79-145` |

## 2. Quyết định cần chốt

| Mã | Câu hỏi | Đề xuất | Hệ quả nếu chọn khác |
|---|---|---|---|
| D-DB-1 **[đã chốt 2026-11-22: một database nhiều schema]** | Một database nhiều schema, hay giữ hai database riêng trên cùng máy chủ như hiện nay? | **Một database nhiều schema.** | Hai database riêng cách ly mạnh hơn và không cần thay mã, nhưng không chung được backup một khối, không chung một lần PITR giữa hai nơi, và vẫn phải tạo từng database. Nếu chọn phương án này thì kế hoạch rút còn P2 (role) và P3. |
| D-DB-2 **[đã chốt 2026-11-22: chấp nhận, kèm role riêng, không `GRANT` chéo, test quyền trong CI]** | `identity` (chứa bí mật) có ở chung database với `clinical` không? | **Có**, với role riêng, không `GRANT` chéo, và test quyền trong CI. | Không chấp nhận được thì dừng ở "hai database riêng, một máy chủ" (D-DB-1 phương án 2). |
| D-DB-3 **[đã chốt 2026-11-22: chưa tách `fhir-worker`, chờ đo tải]** | Tách `fhir-worker` ngay hay chờ số liệu tải? | **Điều tra ở P3a rồi mới quyết**; không tách trước khi có lý do đo được. | Tách sớm tốn công nối `asynq`/Redis mà chưa chắc có lợi. |
| D-DB-4 **[áp dụng 2026-11-22 theo đúng đề xuất, tùy chọn bật; chưa có xác nhận bằng lời của người dùng]** | Role migration tách khỏi role chạy ứng dụng ở production? | **Có ở staging/production**: `<svc>_migrate` có DDL, `<svc>_app` chỉ DML. Dev giữ một role. | Một role có DDL thì lỗ hổng SQL injection trong app cũng có thể `DROP TABLE`. |
| D-DB-5 | Chế độ dev bằng SQLite và MySQL | **Giữ nguyên, ngoài phạm vi**: SQLite là mỗi app một tệp (không có schema); MySQL coi schema là database (mỗi app một database). Chỉ PostgreSQL có chế độ "dùng chung". | Ép SQLite dùng chung là không thể (không có schema). |

## 3. Ranh giới service

| Service nghiệp vụ | Thư mục | Sở hữu dữ liệu | Ghi chú |
|---|---|---|---|
| `identity` | `apps/auth` | schema `identity`: User, Login, ProjectMembership, ClientApplication, UserSecurityRequest, khóa ký... (và SCIM khi làm, vì SCIM ghi chính các bảng này: xem `split-scim-ws-services.md` mục 0) | Chứa bí mật: **không bảng nào của schema này được lộ ra schema khác, kể cả dạng view**, trừ khi có quyết định riêng. |
| `clinical` | `apps/fhir-server` | schema `fhir`: resource FHIR, lịch sử, chỉ mục search, `AsyncJob`, Binary (hiện nhúng base64) | Gồm cả search, batch/transaction, terminology, bulk export, rest-hook. Không chia nhỏ theo resource. |
| `realtime` | `apps/ws-gateway` | **không có DB** (Redis: kênh, sổ hiện diện, và Hub FHIRcast lưu đăng ký/ngữ cảnh có hạn) | Đã tách (WS-1 đến WS-9). |
| `fhir-worker` | `apps/fhir-server`, chế độ `FHIR_ROLE=worker` (đề xuất) | schema `fhir` (cùng với `clinical`) | Chỉ làm nếu P3a cho thấy cần. Cùng schema vì job đọc và ghi chính các resource. |
| `fhir-cli` | `apps/fhir-cli` | không | Gọi API, không chạm DB. |
| Dịch vụ nền: `mcp`, `email`, `storage`, `keyvalue` | thư viện `pkg/*` cho tới khi có hơn một nơi gọi | `mcp`: không DB (gọi API của `clinical`); `keyvalue`: Redis; `email`, `storage`: schema nhỏ riêng nếu thành service | Xem [modules/README.md](modules/README.md) và `email-storage.md`. |

Hiện **không** có nhu cầu `GRANT` chéo: `clinical` biết người gọi qua JWT (JWKS của `identity`), không qua DB.

## 4. Mô hình dùng chung DB

### 4.1 Quy tắc (bắt buộc, kiểm bằng test ở mục 7)

| # | Quy tắc |
|---|---|
| R-1 | Mỗi service sở hữu đúng **một schema**; chỉ role của service đó được tạo, sửa và ghi bảng trong schema ấy. |
| R-2 | **Không có khóa ngoại và không có JOIN giữa các schema.** Tham chiếu chéo chỉ lưu ID (chuỗi). Có FK xuyên schema nghĩa là hai service không còn deploy hay migrate độc lập. |
| R-3 | Đọc dữ liệu của service khác chỉ qua **API của service đó** hoặc một **view đã công bố** trong schema `shared` (chỉ đọc, có phiên bản, do `platform_admin` sở hữu). Không đọc thẳng bảng. Hiện chưa có view nào và không được thêm nếu chưa có lý do ghi vào tài liệu này. |
| R-4 | Mỗi service tự chạy migration của mình bằng role của mình; bảng phiên bản nằm trong **schema của service** (nhờ `search_path`), nên hai service không giẫm nhau dù cùng tên bảng. |
| R-5 | Sự kiện giữa service đi qua Redis (`pkg/messaging`) hoặc API, không qua "poll bảng của nhau". Nếu cần phát sự kiện bền: bảng outbox **trong schema của chính service phát**. |
| R-6 | Mọi service khai báo tổng số kết nối tối đa; tổng (replica x `DATABASE_MAX_OPEN_CONNS`) của mọi service phải nhỏ hơn `max_connections` trừ phần dự phòng (mục 8.1). |
| R-7 | Cột project (tenant) giữ như hiện nay (`Meta.project`); Row-Level Security là bước sau, ngoài kế hoạch này. |
| R-8 | `shared` là schema duy nhất mà hơn một role được đọc. Tạo schema khác dùng chung phải sửa tài liệu này. |

### 4.2 Khởi tạo (thay `deploy/postgres-init/01-auth.sql`)

```sql
-- chạy một lần bằng superuser; mật khẩu lấy từ biến môi trường/secret, không viết cứng
CREATE DATABASE trivita;
\c trivita
REVOKE ALL ON DATABASE trivita FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;

CREATE ROLE identity_app LOGIN PASSWORD :'identity_pw';
CREATE ROLE fhir_app     LOGIN PASSWORD :'fhir_pw';
-- staging/production (D-DB-4): thêm identity_migrate, fhir_migrate có DDL; *_app chỉ DML
GRANT CONNECT ON DATABASE trivita TO identity_app, fhir_app;

CREATE SCHEMA identity AUTHORIZATION identity_app;   -- dev: role sở hữu schema
CREATE SCHEMA fhir     AUTHORIZATION fhir_app;
ALTER ROLE identity_app SET search_path = identity;
ALTER ROLE fhir_app     SET search_path = fhir;
-- không GRANT gì chéo. Schema shared chỉ tạo khi R-3 có ca đầu tiên.
```

`ALTER ROLE ... SET search_path` là cơ chế chính (không phụ thuộc DSN, chạy được sau PgBouncer ở chế độ transaction). DSN **không** cần tham số `search_path`; nếu muốn tường minh thì thêm cũng được ở dev.

## 5. Thay đổi mã

| Nơi | Thay đổi | Ghi chú |
|---|---|---|
| `pkg/database` | Không cần mã mới để chạy: `search_path` đã đi qua DSN (đã có test dùng) và role-level `search_path` có hiệu lực ở phía server. Thêm **kiểm tra lúc khởi động** (PostgreSQL): `SHOW search_path` phải có đúng một schema không phải `public`/`$user`, nếu không thì **từ chối chạy** ở staging/production (tránh app ghi nhầm vào `public`). | Đây là phần mã thật duy nhất của P1. Cờ cấu hình: `DATABASE_REQUIRE_SCHEMA=true` ở staging/production. |
| `pkg/fhirstore` | Không đổi. DDL không ghi tên schema nên rơi vào schema đầu của `search_path`. Bảng phiên bản `fhirstore_schema_version` cũng vào schema đó. | Đã xác nhận: không có `CREATE EXTENSION`, type hay function trong migration. |
| `apps/auth`, `apps/fhir-server` | Chỉ đổi cấu hình: `DATABASE_URL` trỏ cùng database, role riêng (`identity_app`, `fhir_app`). Cập nhật bình luận ở `store.go:3-4`, README, `.env.example`. | Không đổi logic. |
| `docker-compose*.yml`, `deploy/postgres-init` | Thay init script bằng 4.2; `DATABASE_URL` mỗi app dùng role riêng. | |
| `make test-pg` | Thêm test quyền (mục 7). | |
| `docs/runbook.md` | Mục mới: tạo role/schema, migrate theo service, PgBouncer, khôi phục từ backup. | |
| `fhir-worker` (P3) | `FHIR_ROLE=api\|worker\|all` (mặc định `all`, giữ hành vi cũ); `worker` không mở HTTP nghiệp vụ, chỉ chạy vòng job và giao rest-hook; cần `pkg/workflow/asynq` (Redis) để api đẩy job sang worker. | **Chưa kiểm**: `fhir-server` hiện đã nối `asynq` chưa (chỉ thấy `FHIR_JOB_CONCURRENCY`). P3a điều tra. |

## 6. Di chuyển dữ liệu hiện có (chỉ khi đã có dữ liệu thật)

Chỉ áp dụng cho môi trường đã có database `auth` và `trivita` riêng. Môi trường mới chạy thẳng 4.2.

1. Dừng ghi (đặt `auth` và `fhir-server` ở chế độ bảo trì, hoặc scale về 0).
2. Tạo database/schema/role theo 4.2 (database đích `trivita`, hoặc database mới nếu muốn giữ nguyên bản cũ).
3. Chạy `migrate up` bằng từng role để tạo bảng và bảng phiên bản trong **schema của service** (rỗng).
4. Sao dữ liệu **từng bảng** bằng `COPY` (9 bảng, toàn dữ liệu chung dạng jsonb nên đơn giản): `psql -d <nguồn> -c "\copy (select * from <bảng>) to stdout" | psql -d trivita -c "set search_path=<schema>; \copy <bảng> from stdin"`. `fhir_history.seq` là cột `GENERATED ALWAYS AS IDENTITY`: nạp bằng `OVERRIDING SYSTEM VALUE` rồi `setval` theo `max(seq)`.
5. Kiểm: số dòng từng bảng bằng nhau; tổng kiểm (ví dụ `md5` của `string_agg(content::text order by resource_type, id)`) bằng nhau; chạy `make test-e2e`/thử đăng nhập và đọc một resource.
6. Đổi `DATABASE_URL`, bật lại. **Giữ database cũ ít nhất 7 ngày** (chỉ đọc) để quay lại.
7. Không dùng `pg_dump | sed` đổi tên schema: dễ sai với tên có dấu `.` hoặc dữ liệu chứa chuỗi trùng.

Thời gian dừng phụ thuộc kích thước; chưa đo. Cần số liệu dung lượng thật trước khi hứa thời gian.

## 7. Cách bắt buộc quy tắc (không chỉ ghi tài liệu)

| Quy tắc | Kiểm bằng |
|---|---|
| R-1, R-3, R-8 | **Test quyền trên PostgreSQL thật** (`make test-pg`): đăng nhập bằng `fhir_app` rồi thử `SELECT`, `INSERT`, `CREATE TABLE` trên schema `identity`, đều phải bị từ chối; và ngược lại. Cũng kiểm `PUBLIC` không có quyền trên database. |
| R-2 | Quét `information_schema.table_constraints` + `pg_constraint`: không có FK nào trỏ sang schema khác (test trên PostgreSQL thật sau khi migrate cả hai). |
| R-4 | Migrate cả hai service vào cùng database rồi kiểm mỗi schema có bảng `fhirstore_schema_version` riêng và đúng phiên bản. |
| Khởi động an toàn | Test `pkg/database`: ở chế độ `DATABASE_REQUIRE_SCHEMA`, URL/role để `search_path` rơi vào `public` thì app **từ chối chạy**. |
| Ranh giới mã | `architecture_test.go` đã cấm app import app; thêm kiểm: chuỗi SQL thô trong `apps/*` không chứa tên schema của service khác (`identity.`, `fhir.`, `shared.`) ngoài schema của chính nó. |
| Đột biến | Chạy theo hướng dẫn trong bộ nhớ "chạy đột biến đúng cách" (không dùng `timeout`; tách lỗi biên dịch khỏi test đỏ): bỏ `REVOKE`, thêm `GRANT` chéo, bỏ kiểm `search_path` đều phải làm test đỏ. |

## 8. Vận hành

### 8.1 Số kết nối

Mặc định mỗi app giữ tối đa 20 kết nối. Ví dụ 3 replica `clinical` + 2 replica `identity` + 1 `fhir-worker` = (3 + 1) x 20 + 2 x 20 = **120**, vượt `max_connections=100` mặc định của PostgreSQL. Cần một trong hai: hạ `DATABASE_MAX_OPEN_CONNS` theo ngân sách (R-6), hoặc đặt PgBouncer ở chế độ transaction trước cụm. Với PgBouncer transaction pooling, dùng `ALTER ROLE ... SET search_path` (mục 4.2), không dựa vào tham số khởi động của DSN. **Chưa kiểm**: phiên bản PgBouncer sẽ dùng và việc nó có từ chối tham số `search_path` trong DSN hay không; vì vậy kế hoạch không phụ thuộc vào tham số đó.

### 8.2 Migration

- Mỗi service có lệnh `migrate up` riêng (đã có). Với nhiều replica: `DATABASE_AUTO_MIGRATE=false` và chạy `migrate up` bằng một Job (K8s) trước khi rollout, dùng role `*_migrate` (D-DB-4).
- Migration của một service chỉ được chạm schema của chính nó; test ở mục 7 chặn điều ngược lại.
- Migration khóa bảng lớn (`fhir_idx_*`) ảnh hưởng mọi service dùng chung cụm: chạy ngoài giờ cao điểm và dùng `CREATE INDEX CONCURRENTLY` (goose cần chú thích `-- +goose NO TRANSACTION`).

### 8.3 Backup và khôi phục

Một cụm nghĩa là một chính sách backup. Khôi phục **từng schema** (không khôi phục cả cụm) khi chỉ một service hỏng: `pg_dump -n <schema>` hoặc PITR sang cụm tạm rồi chép schema cần thiết. Cần diễn tập trước khi dùng thật: chưa có ai thử.

### 8.4 Cách ly hiệu năng

Chung cụm thì chia tài nguyên bằng cấu hình, không bằng kiến trúc: `statement_timeout` và `idle_in_transaction_session_timeout` đặt theo role (`ALTER ROLE identity_app SET statement_timeout = '5s'`), `$reindex`/`$export` chạy ở `fhir-worker` với role/timeout dài hơn. Giá trị cụ thể cần đo, chưa có số.

## 9. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| P0 | Chốt D-DB-1 đến D-DB-5 | S | Ghi quyết định vào mục 2. **D-DB-1, D-DB-2 và D-DB-3 đã chốt (2026-11-22)**; còn D-DB-4 và D-DB-5 | người dùng |
| P1 **[x]** | Kiểm tra `search_path` lúc khởi động + `DATABASE_REQUIRE_SCHEMA`; init script mới (4.2); cập nhật compose, README, `.env.example`, bình luận `store.go`; chạy `auth` và `fhir-server` cùng một database, mỗi app một schema | S đến M | Cả hai app chạy cùng database; mỗi schema có bảng phiên bản riêng; `make test-e2e` và `make test-pg` xanh | P0 |
| P2 **[x]** | Role riêng (và `*_migrate` ở production); test quyền, test không FK chéo, test khởi động an toàn, lint tên schema; runbook mục 8 | M | Mọi test mục 7 xanh trên PostgreSQL thật; đột biến đều làm test đỏ | P1 |
| P3a | Điều tra `fhir-worker`: `fhir-server` nối `asynq` chưa, những gì chạy nền (job, rest-hook, vòng bảo trì), có tách `FHIR_ROLE` được không, tải thực tế | S | Báo cáo kèm số đo; D-DB-3 được chốt | P1 |
| P3b | `FHIR_ROLE=api\|worker\|all` + `fhir-worker` (nếu P3a cho thấy cần) | M | api đẩy job, worker chạy; job mồ côi được tiếp quản; mặc định `all` không đổi hành vi | P3a |
| P4 | Di chuyển dữ liệu của môi trường đã có dữ liệu thật (mục 6) | M | Số dòng và tổng kiểm khớp; đăng nhập và đọc resource thử được; có đường quay lại | P2 |
| P5 | Dịch vụ nền (`mcp`, `email`, `storage`, `keyvalue`) **khi có người dùng thật**, mỗi cái một schema nhỏ nếu cần DB | tùy | Theo từng kế hoạch trong `modules/` | P2 |

P1 có thể làm trước P0 với giả định đề xuất, nhưng **không chạy P4 trước khi có số liệu dung lượng và diễn tập backup** (mục 6, 8.3).

## 10. Rủi ro và câu hỏi mở

| Mã | Điều | Đề xuất |
|---|---|---|
| Q-DB-1 | Dung lượng dữ liệu thật và thời gian dừng cho P4 | Đo trước; không hứa thời gian dừng khi chưa có số. |
| Q-DB-2 | PgBouncer: phiên bản, chế độ pool, cách xử lý `search_path` | Dùng role-level `search_path`; kiểm bằng test tích hợp có PgBouncer trước khi đưa vào production. |
| Q-DB-3 | Có cần Row-Level Security theo project (R-7) | Ngoài phạm vi; làm riêng nếu yêu cầu bảo mật đòi hỏi. |
| Q-DB-4 | `identity` ở chung database có thỏa chính sách dữ liệu nhạy cảm của tổ chức không | Đây là quyết định của người dùng (D-DB-2), không phải kỹ thuật thuần túy. Nếu không thỏa thì dừng ở hai database riêng một máy chủ. |
| Q-DB-5 | `fhir_history.seq` và các sequence khi nạp dữ liệu (mục 6 bước 4) | Kiểm bằng test nạp thử trên bản sao trước khi làm thật. |
| Rủi ro | Chung miền lỗi: một truy vấn nặng hay migration khóa bảng làm chậm mọi service | Timeout theo role (8.4), `fhir-worker` cho việc nặng, `CREATE INDEX CONCURRENTLY`; chấp nhận phần còn lại. |
| Rủi ro | Cách ly chỉ mạnh bằng quyền DB: nếu `*_app` có thêm quyền (ví dụ ai đó `GRANT` chéo cho tiện) thì ranh giới mất | Test quyền chạy trong `make test-pg`, đỏ khi có `GRANT` chéo. |

## 11. Không làm trong kế hoạch này

- Không đổi tên thư mục `apps/*`, không đổi module Go.
- Không tách `clinical` theo loại resource.
- Không đổi dialect (SQLite/MySQL giữ nguyên, mỗi app một DB riêng).
- Không thêm Row-Level Security, không thêm CDC/Debezium; sự kiện đi qua Redis như hiện nay.
- Không chạm `ws-gateway` (không có DB).

## 12. Đã làm: P1 (2026-11-22)

**Mã:**
- `pkg/database`: `Config.RequireSchema` (chỉ PostgreSQL) làm `Open` **từ chối** nếu `search_path` không phải đúng MỘT schema riêng đã tồn tại (không `public`, không `"$user"`, không kèm schema khác, không rỗng, không phải `pg_catalog`); lỗi nói rõ lý do và không lộ mật khẩu. Bật cờ với SQLite/MySQL là lỗi. Kiểm tra nằm trong `Open` nên **mọi đường mở DB đều bị chặn**: khởi động, `migrate up|down|status`, `assign-project`; migration không thể ghi nhầm vào `public`. `DB.Schema` cho biết schema đang dùng (được ghi log lúc khởi động).
- `DATABASE_REQUIRE_SCHEMA` ở cả `auth` và `fhir-server` (cấu hình đòi PostgreSQL khi bật). **Khác chữ của kế hoạch ở mục 5:** cờ **mặc định tắt** (kể cả staging/production), không bắt buộc, để không phá triển khai đang chạy bằng database riêng và schema `public`; ở staging/production mà không bật thì app ghi cảnh báo. Compose đã bật. Nếu muốn buộc bật ở production thì đó là một quyết định mới (sẽ làm hỏng mọi triển khai cũ khi nâng cấp).
- `deploy/postgres-init/01-schemas.sh` thay `01-auth.sql`: role `identity_app` và `fhir_app`, schema `identity` và `fhir`, thu quyền `PUBLIC`, `search_path` đặt ở mức role; **idempotent** (chạy lại trên volume có sẵn không mất dữ liệu: đã thử). `docker-compose.yml`: hai app dùng cùng database `trivita`, role riêng, `DATABASE_REQUIRE_SCHEMA=true`, mật khẩu qua `IDENTITY_DB_PASSWORD`, `FHIR_DB_PASSWORD` (mặc định chỉ để dev).
- Cập nhật bình luận `store.go`, `config.go`, README, `.env.example` của hai app, README gốc, kế hoạch `auth` (D-2), `docs/runbook.md` mục 12 (khởi tạo, thông báo lỗi và cách xử lý, kiểm trạng thái, số kết nối).

**Kiểm chứng:**
- Test đơn vị `ownSchema` (17 ca) và test PostgreSQL thật qua `pkg/testkit`: role đúng mở được và `db.Schema` đúng; `search_path` qua DSN cũng được; bị từ chối: superuser mặc định, `svc, public`, hai schema riêng, `public`, schema chưa tồn tại, SQLite; không bật cờ thì hành vi cũ giữ nguyên.
- `pkg/fhirstore.TestTwoServicesShareOneDatabase`: hai role, hai schema, **cùng tên bảng và cùng bảng phiên bản** sống chung một database; mỗi schema đủ 10 bảng kể cả `fhirstore_schema_version`; `public` không có bảng nào; dữ liệu cách ly; role này không đọc, ghi, xóa, phá hay tạo bảng ở schema kia hoặc ở `public`; migrate độc lập.
- Mỗi app có `TestStartsOnOwnSchemaAndRefusesPublic`: khởi động và tự migrate đúng schema riêng; search_path sai thì từ chối khởi động VÀ không tạo bảng nào ở `public`; `migrate up` trên search_path sai bị từ chối; không bật cờ thì chạy như cũ.
- Đột biến 17 chỗ (cho nhiều schema, cho `public`, cho `$user`, không kiểm schema tồn tại, không kiểm dialect, bỏ qua cờ, không truyền cờ ở khởi động và ở `migrate` của từng app, không đọc biến, cho cờ với SQLite...) đều làm test đỏ.
- **Chạy thật bằng Docker Compose** (project riêng, volume mới, cổng và tên image riêng để không đụng stack hiện có): postgres + auth + fhir-server cùng healthy; `identity` và `fhir` mỗi schema đúng 9 bảng `fhir_*` + 1 bảng phiên bản; hai role có `search_path` đúng; log khởi động ghi schema; `fhir_app` đọc `identity.fhir_resource`, `identity_app` đọc `fhir.fhir_resource` và `fhir_app` tạo bảng ở `public` đều bị `permission denied`; seed project ở `auth`, xin token `client_credentials`, tạo `Patient` ở `fhir-server` bằng token đó (HTTP 201): schema `identity` giữ User/Login/Project/ProjectMembership/ClientApplication/AccessPolicy/Practitioner, schema `fhir` chỉ giữ `Patient`.
- `make test-pg`, `make test-auth-pg`, `make test-e2e` (13 test), `go vet`, `gofmt`, `go test -short ./...` đều xanh.

**Chưa làm:** P2 (role `*_migrate` riêng ở production, D-DB-4; test quyền chạy trong CI cho `make test-pg`; lint tên schema trong SQL thô; `statement_timeout` theo role); P3 (`fhir-worker`); P4 (di chuyển dữ liệu của môi trường đã có dữ liệu thật: **chưa đo dung lượng và chưa diễn tập backup**); chưa kiểm PgBouncer. Chế độ SQLite và MySQL không thay đổi.

## 13. Đã làm: P2 (2026-11-22)

**Ghi chú về D-DB-4:** người dùng nói "tiếp tục P2" mà không nêu ý kiến riêng về D-DB-4, nên tôi làm đúng đề xuất trong kế hoạch (`*_migrate` có DDL, `*_app` chỉ DML) và để **tùy chọn bật** (mặc định dev vẫn một role, không phá cách chạy cũ). Nếu bạn muốn khác (ví dụ không tách role), phần này không ảnh hưởng chế độ mặc định và có thể bỏ.

**Mã và cấu hình:**
- `deploy/postgres-init/01-schemas.sh` có hai chế độ. Chế độ tách (`DB_SEPARATE_MIGRATION_ROLES=true`): thêm `identity_migrate`, `fhir_migrate` **sở hữu** schema và bảng; `*_app` chỉ `USAGE` và `SELECT/INSERT/UPDATE/DELETE` (kể cả bảng và sequence do migration TƯƠNG LAI tạo, qua `ALTER DEFAULT PRIVILEGES`), không DDL, không `TRUNCATE`. Chế độ này **không có mật khẩu mặc định** (thiếu thì script dừng trước khi chạm psql). Chạy lại trên database đang ở chế độ một role sẽ chuyển chủ sở hữu bằng `REASSIGN OWNED` và giữ dữ liệu; chiều ngược lại không hỗ trợ. Tùy chọn `IDENTITY_STATEMENT_TIMEOUT`, `FHIR_STATEMENT_TIMEOUT` (**không có mặc định**: `$reindex` và bulk export chạy dưới `fhir_app` và có thể chạy lâu; chưa đo).
- `DATABASE_MIGRATE_URL` ở cả hai app: lệnh `migrate` dùng role này, app dùng `DATABASE_URL`. Cấu hình **bắt buộc** `DATABASE_AUTO_MIGRATE=false` khi đặt nó và chỉ chấp nhận PostgreSQL.
- `docker-compose.db-roles.yml`: overlay thử chế độ tách; hai tác vụ một lần `auth-migrate`, `fhir-migrate` chạy `migrate up` bằng role migrate, app chỉ khởi động sau khi chúng thành công (`service_completed_successfully`).
- **Lỗi thật tìm ra:** `database.Pending` (goose) tự cố tạo bảng phiên bản; với role chỉ DML, app chưa migrate bị từ chối khởi động bằng `permission denied for schema` khó hiểu. Sửa: trên PostgreSQL mà bảng phiên bản chưa tồn tại thì `Pending` trả luôn số migration chờ, **không có tác dụng phụ**; thông báo trở lại "schema chưa được migrate".
- `testkit.PostgresWithInit` (PostgreSQL riêng chạy script thật), `URLAs`, `ExecInContainer`.
- `make test-db` gom mọi test của mô hình DB.

**Kiểm chứng** (mục 7 của kế hoạch):
- `tests/dbmodel` chạy **script init thật** trong PostgreSQL thật (không dựng lại trong test): mặc định một role; chế độ tách; chuyển chế độ trên dữ liệu có sẵn; chạy lại idempotent và không đổi mật khẩu; người ngoài không kết nối được; không role nào (kể cả `*_migrate`) có `USAGE`/`CREATE` trên schema kia hay quyền trên bảng kia (kiểm bằng danh mục quyền `has_schema_privilege`, `has_table_privilege`, không chỉ thử vài lệnh) và `public` không cho ai tạo bảng; bảng và sequence `serial` do migration tương lai tạo dùng được ngay ở CẢ hai schema; app không DROP/ALTER/CREATE/TRUNCATE; `statement_timeout` đúng role và có tác dụng thật; script từ chối cấu hình thiếu hoặc sai trước khi chạm psql (không cần Docker).
- **R-2:** không khóa ngoại chéo schema; bộ đo được kiểm chứng bằng một FK chéo cố ý (không "xanh vì không đo được gì").
- **Lint** (`architecture_test.go`): SQL trong mã và migration không gắn tên schema (`FROM identity.x`, `JOIN`, `INSERT INTO`, `REFERENCES`...) và không `SET search_path` trần; chỉ quét giá trị chuỗi trong mã Go (AST) nên không báo nhầm `fhir.Patient` hay chú thích; `ALTER ROLE ... SET search_path` được phép; đã thử chèn vi phạm vào cả mã lẫn migration, cả hai đều làm test đỏ.
- Mỗi app: `TestSeparateMigrationRolesFlow` (chưa migrate thì từ chối khởi động đúng thông báo; role không DDL không tự migrate được; `migrate up` bằng role migrate; app khởi động và làm việc, kể cả seed project ở `auth`, chỉ với DML); test cấu hình cho `DATABASE_MIGRATE_URL`.
- Đột biến: 19 chỗ ở script và 11 chỗ ở mã mới. **Lần chạy đầu để lọt 5 đột biến** (test chỉ thử vài lệnh trên bảng nên một `GRANT USAGE` chéo và thiếu quyền sequence lọt qua; bảng tương lai chỉ thử ở `fhir`); đã thêm kiểm bằng danh mục quyền, bảng và sequence tương lai ở cả hai schema, rồi chạy lại: tất cả đều làm test đỏ. (Một kết quả "sống" ở lần chạy đầu do tôi can thiệp tay vào script khi nó đang bị đột biến, đã chạy lại riêng và xác nhận.)
- **Chạy thật bằng Docker Compose** (`docker-compose.db-roles.yml`, project riêng, cổng và tên image riêng): hai tác vụ migrate thoát mã 0 trước khi app khởi động; bảng và schema thuộc `*_migrate`; `fhir_app` bị `must be owner` khi DROP và ALTER, `permission denied` khi CREATE và TRUNCATE nhưng SELECT được; `fhir_migrate` bị từ chối trên schema `identity`; seed project, xin token, tạo `Patient` (HTTP 201) và tìm lại (200) chỉ bằng role DML; khởi động lại cả hai app vẫn healthy.
- `make test-db`, `make test-pg`, `make test-auth-pg`, `make test-e2e` (13 test), test `ws-gateway` và `testkit` với `-race`, `go vet`, `gofmt`, `go test -short ./...` đều xanh.

**Chưa làm:** P3 (`fhir-worker`, cần điều tra và D-DB-3); P4 (di chuyển dữ liệu thật: chưa đo dung lượng, chưa diễn tập backup); P5; PgBouncer; `migrate up` bằng `Job` trên K8s thật (chỉ có đoạn mô tả trong runbook, chưa kiểm); chưa đặt `statement_timeout` mặc định vì chưa đo; test quyền chưa nằm trong một pipeline CI cụ thể (repo không có cấu hình CI), mới có `make test-db`.

## 14. Kết quả P3a: điều tra `fhir-worker` (2026-11-22)

Đã đọc mã, **chưa đo tải** (repo không có số liệu tải thật của `fhir-server`; mọi nhận định về "có đáng tách không" dưới đây là suy luận từ mã, không phải số đo).

**Có hai thứ chạy nền, độ bền khác nhau:**

| Việc nền | Nơi chạy | Bền? | Bằng chứng |
|---|---|---|---|
| Job `AsyncJob` (hiện chỉ có loại `export`, `pkg/fhirrouter/bulk.go:110`) | `workflow.NewInline` trong process (`container.go:193`) | **Có, nhờ DB**: trạng thái ở resource `AsyncJob`, giành quyền bằng lease (`claimJob`, If-Match), gia hạn bằng heartbeat, job mồ côi được `RecoverJobs` đưa lại hàng đợi sau `JobRecoveryGrace` (30s) và lease hết hạn (mặc định 5 phút); chạy nhiều replica không chạy trùng | `pkg/fhirrouter/jobs_maint.go` |
| Giao rest-hook (`fhirsub.Manager`) | hàng đợi `chan` trong process, retry bằng `time.AfterFunc` | **Không**: restart mất thông báo đang chờ/retry; hàng đợi đầy thì bỏ (`Dropped`, log lỗi); tối đa `FHIR_WEBHOOK_MAX_ATTEMPTS` lần | `pkg/fhirsub/manager.go:359`, `deliver.go:70` |

**Trả lời các câu hỏi của P3a:**
- `asynq` **chưa được nối** vào `fhir-server`: adapter `pkg/workflow/asynq` có sẵn và có test nhưng không có chỗ nào ngoài test gọi tới (`NewOrchestrator` luôn trả `Inline`).
- Tách được bằng `FHIR_ROLE`: có thể, vì orchestrator là cổng (`NewOrchestrator`) và trạng thái job nằm trong DB dùng chung. Có **hai** cách đẩy job sang worker: (a) `asynq` qua Redis (nhanh, thêm phụ thuộc vận hành); (b) không thêm gì: role `api` không chạy job (Submit để job ở `accepted`), role `worker` chỉ chạy vòng `RecoverJobs`; độ trễ nhận job ≥ `JobRecoveryGrace` + chu kỳ bảo trì. Cách (b) **là suy luận, chưa thử**; `RecoverJobs` hiện bỏ qua job mới hơn `JobRecoveryGrace`.
- Tách worker **không sửa** điểm yếu thật nhất: rest-hook không bền. Muốn bền phải có hàng đợi bền cho rest-hook (asynq hoặc bảng outbox), việc này độc lập với chuyện tách tiến trình.

**Đối chiếu nguyên tắc tách ([../service-principles.md](../service-principles.md)):** A (tải khác nhau) chưa có số đo; B (ranh giới dữ liệu/secret) không đạt: worker đọc/ghi cùng schema `fhir` và dùng cùng `FHIR_JOB_SIGNING_KEY`; C (miền lỗi) chỉ đạt một phần: export nặng có thể làm API chậm, nhưng chưa quan sát thấy; D (đội sở hữu) không.

**Đề xuất cho D-DB-3: chưa tách `fhir-worker`.** Việc đáng làm trước, rẻ hơn tách tiến trình và không đụng thứ tự triển khai:
1. Đo thật: chạy `export` lớn song song với tải API, ghi độ trễ p95 của API và bộ nhớ (cần quyết định có môi trường đo hay không).
2. Quyết định rest-hook có cần bền không (nếu có: outbox/asynq là việc riêng, gọi là P3c).
3. Chỉ khi bước 1 cho thấy API bị ảnh hưởng mới làm P3b (ưu tiên cách (b), thêm `asynq` sau).

**D-DB-3 đã chốt (2026-11-22): chưa tách `fhir-worker`.** P3b chỉ mở lại khi phép đo (bước 1) cho thấy API bị ảnh hưởng; P3c (rest-hook bền) là quyết định riêng.

**Chưa làm:** đo tải; thử cách (b); P3b/P3c.
