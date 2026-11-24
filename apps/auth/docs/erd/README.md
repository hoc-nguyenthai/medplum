# auth: ERD và từ điển dữ liệu

Trả lời **"auth lưu gì, ở đâu, bảng và cột nào để làm gì, nối với nhau thế nào, nhạy cảm cỡ nào, xóa thì sao"**. Kiến trúc tổng thể ở [../architecture.md](../architecture.md) mục 4; vận hành DB ở [../runbook.md](../runbook.md) mục 3.

| # | Tài liệu | Nội dung |
|---|---|---|
| 1 | [01-bang-vat-ly.md](01-bang-vat-ly.md) | Auth dùng bảng nào (và bảng nào rỗng), schema, role, migration, đường ghi, các bảng lớn nhanh. **Bảng và cột chi tiết viết một lần** ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| 2 | [02-mo-hinh-logic.md](02-mo-hinh-logic.md) | 10 loại resource auth sở hữu: từng trường, chức năng, mức nhạy cảm, tham số tìm kiếm; vòng đời `Login`; ERD logic |
| 3 | [03-quan-he-va-toan-ven.md](03-quan-he-va-toan-ven.md) | Bảng quan hệ (bản số, cách nối, ai giữ toàn vẹn, hành vi khi xóa), transaction, giữ lại dữ liệu nhạy cảm trong lịch sử, dọn dẹp |
| 4 | [04-kho-khac.md](04-kho-khac.md) | Redis (khóa, TTL, hành vi khi mất), tệp khóa ký, khóa mã hóa secret MFA, SQLite dev |

## Kiểm kê kho lưu trữ

| Kho | Loại | Vị trí | Ghi bởi | Đọc bởi | Bắt buộc ở staging/production |
|---|---|---|---|---|---|
| Dữ liệu danh tính | PostgreSQL | schema **`identity`**, role `identity_app` (DML) và `identity_migrate` (DDL) | `auth` (qua `pkg/fhirstore`) | `auth` | có (SQLite bị cấm) |
| Dữ liệu danh tính (dev) | SQLite | tệp `./auth.db` (`DATABASE_URL=sqlite://./auth.db`) | `auth` | `auth` | không (cấm ở production) |
| Trạng thái ngắn hạn (MFA, `jti`, topic FHIRcast) | Redis | tiền tố `auth:mfa:`, `auth:jti:`, `fhircast:user-topic:` | `auth` | `auth` | có (`AUTH_REDIS_URL`) |
| Khóa ký token | tệp PEM | thư mục `AUTH_SIGNING_KEY_PATH`, quyền `0600` | `auth keys generate` | `auth` (và JWKS công khai) | có |
| Khóa mã hóa secret TOTP | biến môi trường | `AUTH_MFA_SECRET_KEYS` (`kid:base64(32 byte)`) | vận hành | `auth` | có |

**Không** có Redis dùng làm kho bền, không có kho Binary, không có hàng đợi.

## Mô hình sở hữu và quy tắc

- Một cụm PostgreSQL, một database, **mỗi service một schema và một role** (D-DB-1, D-DB-2): `auth` ở `identity`, `fhir-server` ở `fhir`. `identity_app` không có quyền gì trên schema `fhir` và ngược lại; `DATABASE_REQUIRE_SCHEMA=true` làm `auth` từ chối khởi động nếu `search_path` không phải đúng một schema riêng. script: `deploy/postgres-init/01-schemas.sh`; test quyền: `tests/dbmodel`, `make test-db`.
- Ở staging/production nên tách role migration (D-DB-4): `identity_migrate` sở hữu schema và chạy `auth migrate up` ở bước deploy (`DATABASE_MIGRATE_URL`), app chạy `DATABASE_AUTO_MIGRATE=false` với `identity_app` chỉ DML.
- **Tên `identity`, `identity_app`, `identity_migrate` không đổi** khi service đổi tên từ `authen` sang `auth`.
- **Không có khóa ngoại** ở bất cứ đâu: thực thể nghiệp vụ là JSON trong một bảng chung, quan hệ là `Reference` trong JSON và cặp `(resource_type, id)` ở bảng chỉ mục (xem [01](01-bang-vat-ly.md) và [03](03-quan-he-va-toan-ven.md)).
- Secret **không được lộ ra schema khác**, kể cả dạng view; `fhir-server` biết người gọi qua JWT (JWKS), không qua DB.

## Nguồn sự thật và cách kiểm

- Bảng và cột: `pkg/fhirstore/migrations/{postgres,sqlite}/*.sql`; trường resource: `pkg/fhir/resource_<loại>.go` và `searchparams_gen.go`; hành vi ghi/xóa: `pkg/fhirstore/crud.go`, `apps/auth/internal/infrastructure/store/*.go`, `internal/application/*.go`.
- Kiểm phủ (mọi bảng, cột, trường có ghi chú): `python3 -I ~/.claude/skills/service-erd-docs/scripts/check_erd_docs.py --docs apps/auth/docs/erd --resources User,Login,UserSecurityRequest,SmartAppLaunch,UserConfiguration,Project,ProjectMembership,ClientApplication,AccessPolicy,DomainConfiguration --repo .`. Bộ này **không có test tự động trong CI**; khi lệch mã, mã đúng.
- **Chưa kiểm** trên PostgreSQL thật (kiểu cột lấy từ migration, chưa chạy `\d+`).

## Điểm cần chú ý (tóm tắt, chi tiết ở các tệp)

1. **Xóa là xóa mềm và lịch sử giữ nguyên dữ liệu cũ**: `User` và `ProjectMembership` bị xóa (`AdminDeleteMember`, SCIM) vẫn còn mọi phiên bản trước trong `fhir_history`, kể cả email và hash mật khẩu; chỉ `Login` được xóa vĩnh viễn (`auth purge-logins`). Xem [03](03-quan-he-va-toan-ven.md) mục 4.
2. **Mỗi lần xoay refresh token là một lần ghi `Login`**, nên sinh một dòng `fhir_history` và ghi lại chỉ mục của `Login`: bảng lớn nhanh nhất của schema `identity`.
3. **Quan hệ tới project của resource không phải `Reference`** mà là chuỗi `meta.project`, **không được lập chỉ mục**: kiểm bằng mã ứng dụng (ví dụ AccessPolicy phải thuộc project).
4. Bảng terminology (`fhir_coding*`, `fhir_concept_mapping`) **tồn tại nhưng rỗng** ở `identity`.
