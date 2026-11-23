#!/bin/sh
# Mô hình MỘT database nhiều schema (docs/plans/microservices-shared-db.md): mỗi service một schema và một role.
#   identity_app -> schema identity  (apps/auth: User, Login, UserSecurityRequest chứa bí mật)
#   fhir_app     -> schema fhir      (apps/fhir-server: dữ liệu lâm sàng)
# Không role nào có quyền gì trên schema của role kia, và không ai tạo được bảng ở `public`. `search_path` của mỗi role được đặt ở
# mức ROLE (không phụ thuộc DSN, chạy đúng cả sau PgBouncer); app bật DATABASE_REQUIRE_SCHEMA=true để từ chối chạy nếu nó cấu hình sai.
#
# HAI CHẾ ĐỘ:
#  1. Mặc định (dev, một role mỗi service): role *_app SỞ HỮU schema nên có cả DDL; app tự migrate (DATABASE_AUTO_MIGRATE=true).
#  2. DB_SEPARATE_MIGRATION_ROLES=true (staging/production, D-DB-4): thêm identity_migrate, fhir_migrate SỞ HỮU schema và chạy DDL; role *_app
#     chỉ có DML (SELECT, INSERT, UPDATE, DELETE) trên bảng do role migrate tạo (ALTER DEFAULT PRIVILEGES), nên một lỗ hổng SQL injection ở app
#     không thể DROP/ALTER bảng. App chạy với DATABASE_AUTO_MIGRATE=false; migrate chạy ở bước deploy riêng với DATABASE_MIGRATE_URL. Ở chế độ
#     này MỌI mật khẩu phải đặt tường minh (không có mặc định). Chuyển từ chế độ 1 sang 2 chạy lại được (REASSIGN OWNED); chiều ngược lại không hỗ trợ.
#
# Chạy MỘT LẦN khi volume postgres khởi tạo lần đầu (docker-entrypoint-initdb.d). Với volume đã có sẵn, chạy tay (script idempotent):
#   docker compose exec postgres sh /docker-entrypoint-initdb.d/01-schemas.sh
# Biến: IDENTITY_DB_PASSWORD, FHIR_DB_PASSWORD (mặc định chỉ để dev: identity, fhir; ĐỔI ở môi trường thật), và ở chế độ 2 còn
# IDENTITY_MIGRATE_DB_PASSWORD, FHIR_MIGRATE_DB_PASSWORD; tùy chọn IDENTITY_STATEMENT_TIMEOUT, FHIR_STATEMENT_TIMEOUT (ví dụ 30s; rỗng = không
# đặt, vì $reindex và bulk export của fhir-server có thể chạy lâu: chỉ đặt sau khi đã đo).
# Dữ liệu đã có trong database `auth` hoặc schema `public` KHÔNG được chuyển tự động: xem docs/plans/microservices-shared-db.md mục 6.
set -eu
: "${POSTGRES_USER:?}" "${POSTGRES_DB:?}"
SEPARATE="${DB_SEPARATE_MIGRATION_ROLES:-false}"
case "$SEPARATE" in true|false) ;; *) echo "DB_SEPARATE_MIGRATION_ROLES phải là true hoặc false" >&2; exit 2;; esac
if [ "$SEPARATE" = true ]; then
  : "${IDENTITY_DB_PASSWORD:?chế độ role migrate riêng: đặt IDENTITY_DB_PASSWORD}" "${FHIR_DB_PASSWORD:?chế độ role migrate riêng: đặt FHIR_DB_PASSWORD}"
  : "${IDENTITY_MIGRATE_DB_PASSWORD:?chế độ role migrate riêng: đặt IDENTITY_MIGRATE_DB_PASSWORD}" "${FHIR_MIGRATE_DB_PASSWORD:?chế độ role migrate riêng: đặt FHIR_MIGRATE_DB_PASSWORD}"
fi
IDENTITY_DB_PASSWORD="${IDENTITY_DB_PASSWORD:-identity}"
FHIR_DB_PASSWORD="${FHIR_DB_PASSWORD:-fhir}"
IDENTITY_MIGRATE_DB_PASSWORD="${IDENTITY_MIGRATE_DB_PASSWORD:-}"
FHIR_MIGRATE_DB_PASSWORD="${FHIR_MIGRATE_DB_PASSWORD:-}"
IDENTITY_STATEMENT_TIMEOUT="${IDENTITY_STATEMENT_TIMEOUT:-}"
FHIR_STATEMENT_TIMEOUT="${FHIR_STATEMENT_TIMEOUT:-}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v identity_pw="$IDENTITY_DB_PASSWORD" -v fhir_pw="$FHIR_DB_PASSWORD" -v db="$POSTGRES_DB" \
  -v identity_mpw="$IDENTITY_MIGRATE_DB_PASSWORD" -v fhir_mpw="$FHIR_MIGRATE_DB_PASSWORD" \
  -v separate="$SEPARATE" -v identity_st="$IDENTITY_STATEMENT_TIMEOUT" -v fhir_st="$FHIR_STATEMENT_TIMEOUT" <<'SQL'
SELECT 'CREATE ROLE identity_app LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'identity_app') \gexec
SELECT 'CREATE ROLE fhir_app LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fhir_app') \gexec
ALTER ROLE identity_app PASSWORD :'identity_pw';
ALTER ROLE fhir_app PASSWORD :'fhir_pw';

REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
GRANT CONNECT ON DATABASE :"db" TO identity_app, fhir_app;
REVOKE ALL ON SCHEMA public FROM PUBLIC;

CREATE SCHEMA IF NOT EXISTS identity AUTHORIZATION identity_app;
CREATE SCHEMA IF NOT EXISTS fhir AUTHORIZATION fhir_app;
ALTER ROLE identity_app SET search_path = identity;
ALTER ROLE fhir_app SET search_path = fhir;

\if :separate
  SELECT 'CREATE ROLE identity_migrate LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'identity_migrate') \gexec
  SELECT 'CREATE ROLE fhir_migrate LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fhir_migrate') \gexec
  ALTER ROLE identity_migrate PASSWORD :'identity_mpw';
  ALTER ROLE fhir_migrate PASSWORD :'fhir_mpw';
  GRANT CONNECT ON DATABASE :"db" TO identity_migrate, fhir_migrate;
  ALTER ROLE identity_migrate SET search_path = identity;
  ALTER ROLE fhir_migrate SET search_path = fhir;

  -- Role migrate sở hữu schema và mọi bảng (kể cả bảng đã tạo từ chế độ 1); role app chỉ còn DML.
  REASSIGN OWNED BY identity_app TO identity_migrate;
  REASSIGN OWNED BY fhir_app TO fhir_migrate;
  GRANT USAGE ON SCHEMA identity TO identity_app;
  GRANT USAGE ON SCHEMA fhir TO fhir_app;
  GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity TO identity_app;
  GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA fhir TO fhir_app;
  GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity TO identity_app;
  GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA fhir TO fhir_app;
  -- Bảng mà migration TƯƠNG LAI tạo cũng tự cấp DML cho role app.
  ALTER DEFAULT PRIVILEGES FOR ROLE identity_migrate IN SCHEMA identity GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO identity_app;
  ALTER DEFAULT PRIVILEGES FOR ROLE fhir_migrate IN SCHEMA fhir GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO fhir_app;
  ALTER DEFAULT PRIVILEGES FOR ROLE identity_migrate IN SCHEMA identity GRANT USAGE, SELECT ON SEQUENCES TO identity_app;
  ALTER DEFAULT PRIVILEGES FOR ROLE fhir_migrate IN SCHEMA fhir GRANT USAGE, SELECT ON SEQUENCES TO fhir_app;
\endif

SELECT :'identity_st' <> '' AS has_identity_st, :'fhir_st' <> '' AS has_fhir_st \gset
\if :has_identity_st
  ALTER ROLE identity_app SET statement_timeout = :'identity_st';
\else
  ALTER ROLE identity_app RESET statement_timeout;
\endif
\if :has_fhir_st
  ALTER ROLE fhir_app SET statement_timeout = :'fhir_st';
\else
  ALTER ROLE fhir_app RESET statement_timeout;
\endif
SQL
