// Package dbmodel_test kiểm mô hình "một database nhiều schema" (docs/plans/microservices-shared-db.md) bằng đúng THỨ ĐƯỢC TRIỂN KHAI:
// script deploy/postgres-init/01-schemas.sh chạy trong một PostgreSQL thật (testcontainers), rồi dùng các role mà script tạo ra để migrate,
// ghi dữ liệu bằng pkg/fhirstore và thử vượt quyền. Không dựng lại mô hình trong test: sửa script làm hỏng quy tắc thì test đỏ.
package dbmodel_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/testkit"
)

func script(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../deploy/postgres-init/01-schemas.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("thiếu script init: %v", err)
	}
	return p
}

func sqlOpen(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// store mở kho fhirstore bằng role (qua database.Open với RequireSchema, như app thật).
func store(t *testing.T, dsn string) *fhirstore.Store {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{URL: dsn, RequireSchema: true, MaxOpenConns: 4})
	if err != nil {
		t.Fatalf("mở DB bằng role: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := fhirstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func mustFail(t *testing.T, who string, db *sql.DB, queries map[string]string) {
	t.Helper()
	for what, q := range queries {
		if _, err := db.ExecContext(context.Background(), q); err == nil {
			t.Errorf("%s: %s phải bị từ chối (%s)", who, what, q)
		}
	}
}

func scalar(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func tableSet(t *testing.T, admin *sql.DB, schema string) string {
	t.Helper()
	return scalar(t, admin, "SELECT string_agg(table_name, ',' ORDER BY table_name) FROM information_schema.tables WHERE table_schema = $1", schema)
}

// crossSchemaFKs đếm khóa ngoại có hai đầu ở hai schema khác nhau (R-2: tham chiếu chéo chỉ lưu ID).
func crossSchemaFKs(t *testing.T, admin *sql.DB) int {
	t.Helper()
	n := scalar(t, admin, `SELECT count(*) FROM pg_constraint c
		JOIN pg_class a ON a.oid = c.conrelid JOIN pg_class b ON b.oid = c.confrelid
		WHERE c.contype = 'f' AND a.relnamespace <> b.relnamespace`)
	return atoi(n)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// noCrossAccess kiểm bằng danh mục quyền của PostgreSQL (không chỉ thử vài câu lệnh): không role nào có USAGE hay CREATE trên schema của service
// khác (kể cả USAGE đơn thuần, vốn đủ để dò tên bảng), cũng không có quyền nào trên các bảng của schema đó, và `public` không cho ai tạo bảng.
func noCrossAccess(t *testing.T, admin *sql.DB, owner map[string]string) {
	t.Helper()
	for role, own := range owner {
		for _, other := range []string{"identity", "fhir"} {
			if other == own {
				continue
			}
			for _, priv := range []string{"USAGE", "CREATE"} {
				if scalar(t, admin, "SELECT has_schema_privilege($1, $2, $3)::text", role, other, priv) != "false" {
					t.Errorf("role %s có quyền %s trên schema %s của service khác", role, priv, other)
				}
			}
			rows, err := admin.Query("SELECT quote_ident(schemaname)||'.'||quote_ident(tablename) FROM pg_tables WHERE schemaname = $1", other)
			if err != nil {
				t.Fatal(err)
			}
			var tables []string
			for rows.Next() {
				var n string
				_ = rows.Scan(&n)
				tables = append(tables, n)
			}
			rows.Close()
			for _, tbl := range tables {
				if scalar(t, admin, "SELECT has_table_privilege($1, $2, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')::text", role, tbl) != "false" {
					t.Errorf("role %s có quyền trên bảng %s của service khác", role, tbl)
				}
			}
		}
		if scalar(t, admin, "SELECT has_schema_privilege($1, 'public', 'CREATE')::text", role) != "false" {
			t.Errorf("role %s tạo được bảng ở public", role)
		}
	}
}

// Kiểm chứng chính phép đo R-2: phải phát hiện được khóa ngoại chéo schema nếu có (tránh test "xanh vì không đo được gì").
func TestCrossSchemaForeignKeyDetectorWorks(t *testing.T) {
	pg := testkit.Postgres(t)
	admin := sqlOpen(t, pg.AdminURL())
	for _, q := range []string{
		"CREATE SCHEMA fk_a", "CREATE SCHEMA fk_b", "CREATE TABLE fk_b.u (id int PRIMARY KEY)",
		"CREATE TABLE fk_a.t (id int, u int REFERENCES fk_b.u(id))", "CREATE TABLE fk_a.same (id int PRIMARY KEY, p int REFERENCES fk_a.same(id))",
	} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA fk_a CASCADE"); _, _ = admin.Exec("DROP SCHEMA fk_b CASCADE") })
	if n := crossSchemaFKs(t, admin); n != 1 { // FK cùng schema (fk_a.same) không tính
		t.Errorf("phải phát hiện đúng 1 FK chéo schema, được %d", n)
	}
}

// Chế độ mặc định (dev): một role mỗi service, role sở hữu schema; sau đó chuyển sang chế độ role migrate riêng trên CHÍNH volume đó.
func TestInitScriptSingleRoleModeThenConvertToSeparateRoles(t *testing.T) {
	pg := testkit.PostgresWithInit(t, testkit.InitSpec{Scripts: []string{script(t)}})
	ctx := context.Background()
	admin := sqlOpen(t, pg.AdminURL())
	idDSN, fhirDSN := pg.URLAs("identity_app", "identity"), pg.URLAs("fhir_app", "fhir")
	idSQL, fhirSQL := sqlOpen(t, idDSN), sqlOpen(t, fhirDSN)

	// role đúng mô hình: search_path mức role, không statement_timeout mặc định
	for who, c := range map[string]struct {
		db     *sql.DB
		schema string
	}{"identity_app": {idSQL, "identity"}, "fhir_app": {fhirSQL, "fhir"}} {
		if sp := scalar(t, c.db, "SHOW search_path"); sp != c.schema {
			t.Errorf("%s: search_path = %q, muốn %q", who, sp, c.schema)
		}
		if st := scalar(t, c.db, "SHOW statement_timeout"); st != "0" {
			t.Errorf("%s: statement_timeout = %q, mặc định phải không đặt", who, st)
		}
	}

	// app thật mở được (RequireSchema), migrate và ghi dữ liệu
	identity, clinical := store(t, idDSN), store(t, fhirDSN)
	for name, st := range map[string]*fhirstore.Store{"identity": identity, "clinical": clinical} {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("%s migrate: %v", name, err)
		}
	}
	if _, err := clinical.CreateResource(ctx, &fhir.Patient{ResourceType: "Patient", ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if tableSet(t, admin, "identity") != tableSet(t, admin, "fhir") || !strings.Contains(tableSet(t, admin, "fhir"), "fhirstore_schema_version") {
		t.Errorf("hai schema phải có cùng bộ bảng và bảng phiên bản riêng: %q | %q", tableSet(t, admin, "identity"), tableSet(t, admin, "fhir"))
	}
	if pub := tableSet(t, admin, "public"); pub != "" {
		t.Errorf("public không được có bảng: %q", pub)
	}
	if n := crossSchemaFKs(t, admin); n != 0 {
		t.Errorf("có %d khóa ngoại chéo schema", n)
	}

	// ma trận quyền: không role nào đụng schema của role kia hoặc public; người ngoài không vào được database
	cross := func(other string) map[string]string {
		return map[string]string{
			"đọc": "SELECT count(*) FROM " + other + ".fhir_resource", "ghi": "DELETE FROM " + other + ".fhir_resource",
			"phá": "DROP TABLE " + other + ".fhir_resource", "tạo": "CREATE TABLE " + other + ".x (id int)",
			"tạo ở public": "CREATE TABLE public.x (id int)",
		}
	}
	mustFail(t, "identity_app", idSQL, cross("fhir"))
	mustFail(t, "fhir_app", fhirSQL, cross("identity"))
	owners := map[string]string{"identity_app": "identity", "fhir_app": "fhir"}
	noCrossAccess(t, admin, owners)
	// một bảng dùng sequence kiểu `serial` (khác cột identity): sau khi chuyển chế độ app vẫn phải ghi được (cần quyền sequence)
	if _, err := fhirSQL.Exec("CREATE TABLE serial_t (id serial PRIMARY KEY, v text)"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE ROLE stranger LOGIN PASSWORD 'x'"); err != nil {
		t.Fatal(err)
	}
	if db, err := sql.Open("pgx", pg.URLAs("stranger", "x")); err == nil {
		defer db.Close()
		if err := db.Ping(); err == nil {
			t.Error("role ngoài mô hình kết nối được vào database (PUBLIC còn quyền CONNECT)")
		}
	}

	// chạy lại script trên volume đã có: idempotent, không mất dữ liệu, mật khẩu và search_path giữ nguyên
	if code, out := pg.ExecInContainer(t, "sh", "/docker-entrypoint-initdb.d/01-schemas.sh"); code != 0 {
		t.Fatalf("chạy lại script: mã %d\n%s", code, out)
	}
	if r, err := clinical.ReadResource(ctx, "Patient", "p1"); err != nil || r == nil {
		t.Errorf("chạy lại script làm mất dữ liệu: %v", err)
	}
	if err := fhirSQL.Ping(); err != nil {
		t.Errorf("mật khẩu bị đổi sau khi chạy lại: %v", err)
	}

	// chuyển sang chế độ role migrate riêng trên chính dữ liệu này (REASSIGN OWNED): dữ liệu còn, app mất quyền DDL
	env := "DB_SEPARATE_MIGRATION_ROLES=true IDENTITY_DB_PASSWORD=identity FHIR_DB_PASSWORD=fhir IDENTITY_MIGRATE_DB_PASSWORD=im FHIR_MIGRATE_DB_PASSWORD=fm"
	if code, out := pg.ExecInContainer(t, "sh", "-c", env+" sh /docker-entrypoint-initdb.d/01-schemas.sh"); code != 0 {
		t.Fatalf("chuyển chế độ: mã %d\n%s", code, out)
	}
	for _, c := range [][2]string{{"identity", "identity_migrate"}, {"fhir", "fhir_migrate"}} {
		if owners := scalar(t, admin, "SELECT string_agg(DISTINCT tableowner, ',') FROM pg_tables WHERE schemaname = $1", c[0]); owners != c[1] {
			t.Errorf("schema %s: chủ sở hữu bảng = %q, muốn %q", c[0], owners, c[1])
		}
		if owner := scalar(t, admin, "SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname = $1", c[0]); owner != c[1] {
			t.Errorf("chủ sở hữu schema %s = %q, muốn %q", c[0], owner, c[1])
		}
	}
	if r, err := clinical.ReadResource(ctx, "Patient", "p1"); err != nil || r == nil {
		t.Errorf("app đọc lại dữ liệu sau khi chuyển chế độ: %v", err)
	}
	if _, err := fhirSQL.Exec("INSERT INTO serial_t (v) VALUES ('sau-chuyen')"); err != nil {
		t.Errorf("app ghi bảng dùng sequence sau khi chuyển chế độ (thiếu quyền sequence?): %v", err)
	}
	noCrossAccess(t, admin, map[string]string{"identity_app": "identity", "fhir_app": "fhir", "identity_migrate": "identity", "fhir_migrate": "fhir"})
	mustFail(t, "fhir_app (sau chuyển)", fhirSQL, map[string]string{
		"phá bảng": "DROP TABLE fhir_resource", "sửa bảng": "ALTER TABLE fhir_resource ADD COLUMN x int", "tạo bảng": "CREATE TABLE y (id int)",
		"tạo chỉ mục": "CREATE INDEX z ON fhir_resource (id)", "TRUNCATE": "TRUNCATE fhir_resource",
	})
}

// Chế độ production (D-DB-4): migrate bằng role *_migrate, app chỉ DML.
func TestInitScriptSeparateMigrationRoles(t *testing.T) {
	pg := testkit.PostgresWithInit(t, testkit.InitSpec{Scripts: []string{script(t)}, Env: map[string]string{
		"DB_SEPARATE_MIGRATION_ROLES": "true", "IDENTITY_DB_PASSWORD": "ia", "FHIR_DB_PASSWORD": "fa",
		"IDENTITY_MIGRATE_DB_PASSWORD": "im", "FHIR_MIGRATE_DB_PASSWORD": "fm", "IDENTITY_STATEMENT_TIMEOUT": "2s",
	}})
	ctx := context.Background()
	admin := sqlOpen(t, pg.AdminURL())
	urls := map[string]string{
		"identity_app": pg.URLAs("identity_app", "ia"), "fhir_app": pg.URLAs("fhir_app", "fa"),
		"identity_migrate": pg.URLAs("identity_migrate", "im"), "fhir_migrate": pg.URLAs("fhir_migrate", "fm"),
	}
	sqls := map[string]*sql.DB{}
	for k, u := range urls {
		sqls[k] = sqlOpen(t, u)
	}

	// 0. `Pending` của role chỉ DML KHÔNG tự tạo bảng phiên bản (goose mặc định thì có): schema chưa migrate báo đúng "còn migration chờ"
	if n, err := store(t, urls["fhir_app"]).Pending(ctx); err != nil || n == 0 {
		t.Fatalf("Pending trên schema chưa migrate: n=%d err=%v (phải >0 và không lỗi quyền)", n, err)
	}
	if tbl := tableSet(t, admin, "fhir"); tbl != "" {
		t.Fatalf("Pending để lại bảng trong schema chưa migrate: %q", tbl)
	}

	// 1. app chưa migrate được (không có DDL); role migrate migrate được; sau đó app (chỉ DML) làm việc đầy đủ bằng fhirstore
	if err := store(t, urls["fhir_app"]).Migrate(ctx); err == nil {
		t.Fatal("role app (không DDL) migrate được: D-DB-4 bị phá")
	}
	for _, role := range []string{"identity_migrate", "fhir_migrate"} {
		if err := store(t, urls[role]).Migrate(ctx); err != nil {
			t.Fatalf("%s migrate: %v", role, err)
		}
	}
	app := store(t, urls["fhir_app"])
	if n, err := app.Pending(ctx); err != nil || n != 0 {
		t.Fatalf("app không đọc được bảng phiên bản (Pending=%d): %v", n, err)
	}
	if _, err := app.CreateResource(ctx, &fhir.Patient{ResourceType: "Patient", ID: "p-app"}); err != nil {
		t.Fatalf("app ghi bằng DML: %v", err)
	}
	if r, err := app.ReadResource(ctx, "Patient", "p-app"); err != nil || r == nil {
		t.Fatalf("app đọc: %v", err)
	}
	if err := app.DeleteResource(ctx, "Patient", "p-app"); err != nil {
		t.Errorf("app xóa (DML): %v", err)
	}

	// 2. app chỉ có DML: không DDL, không TRUNCATE; vẫn tự dùng được bảng do migration TƯƠNG LAI tạo
	mustFail(t, "fhir_app", sqls["fhir_app"], map[string]string{
		"phá bảng": "DROP TABLE fhir_resource", "sửa bảng": "ALTER TABLE fhir_resource ADD COLUMN x int", "tạo bảng": "CREATE TABLE y (id int)",
		"tạo chỉ mục": "CREATE INDEX z ON fhir_resource (id)", "TRUNCATE": "TRUNCATE fhir_resource", "xóa bảng phiên bản": "DROP TABLE fhirstore_schema_version",
	})
	for _, c := range []struct{ migrate, app string }{{"fhir_migrate", "fhir_app"}, {"identity_migrate", "identity_app"}} {
		// bảng và sequence do migration TƯƠNG LAI tạo phải dùng được ngay (ALTER DEFAULT PRIVILEGES cho bảng và cho sequence), ở CẢ hai schema
		if _, err := sqls[c.migrate].Exec("CREATE TABLE future_table (id serial PRIMARY KEY, v text)"); err != nil {
			t.Fatalf("%s tạo bảng mới: %v", c.migrate, err)
		}
		for _, q := range []string{"INSERT INTO future_table (v) VALUES ('a')", "INSERT INTO future_table (v) VALUES ('b')", "UPDATE future_table SET v = 'c'", "SELECT * FROM future_table", "DELETE FROM future_table"} {
			if _, err := sqls[c.app].Exec(q); err != nil {
				t.Errorf("%s phải dùng ngay bảng do migration sau này tạo: %s: %v", c.app, q, err)
			}
		}
		if _, err := sqls[c.app].Exec("DROP TABLE future_table"); err == nil {
			t.Errorf("%s xóa được bảng do %s tạo", c.app, c.migrate)
		}
	}

	// 3. cách ly giữa mọi cặp role: không role nào (kể cả migrate) chạm schema của service kia
	for role, other := range map[string]string{"identity_app": "fhir", "identity_migrate": "fhir", "fhir_app": "identity", "fhir_migrate": "identity"} {
		mustFail(t, role, sqls[role], map[string]string{
			"đọc": "SELECT count(*) FROM " + other + ".fhir_resource", "ghi": "DELETE FROM " + other + ".fhir_resource",
			"tạo": "CREATE TABLE " + other + ".x (id int)", "tạo ở public": "CREATE TABLE public.x (id int)",
		})
	}
	noCrossAccess(t, admin, map[string]string{"identity_app": "identity", "fhir_app": "fhir", "identity_migrate": "identity", "fhir_migrate": "fhir"})
	if n := crossSchemaFKs(t, admin); n != 0 {
		t.Errorf("có %d khóa ngoại chéo schema", n)
	}

	// 4. statement_timeout theo role: chỉ identity_app được đặt, và thật sự có tác dụng
	for role, want := range map[string]string{"identity_app": "2s", "fhir_app": "0", "identity_migrate": "0", "fhir_migrate": "0"} {
		if got := scalar(t, sqls[role], "SHOW statement_timeout"); got != want {
			t.Errorf("%s: statement_timeout = %q, muốn %q", role, got, want)
		}
	}
	start := time.Now()
	if _, err := sqls["identity_app"].Exec("SELECT pg_sleep(6)"); err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Errorf("identity_app phải bị ngắt bởi statement_timeout: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("statement_timeout 2s mà chạy %v", d)
	}
	if _, err := sqls["fhir_app"].Exec("SELECT pg_sleep(3)"); err != nil {
		t.Errorf("fhir_app không đặt timeout mà bị ngắt: %v", err)
	}
}

// Script từ chối cấu hình thiếu hoặc sai TRƯỚC khi chạm psql (không cần Docker): chế độ production không có mật khẩu mặc định.
func TestInitScriptRefusesUnsafeOrInvalidConfiguration(t *testing.T) {
	run := func(env ...string) (int, string) {
		cmd := exec.Command("sh", script(t))
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "POSTGRES_USER=postgres", "POSTGRES_DB=trivita"}, env...)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0, string(out)
	}
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"giá trị chế độ sai":          {[]string{"DB_SEPARATE_MIGRATION_ROLES=yes"}, "true hoặc false"},
		"thiếu mật khẩu app":          {[]string{"DB_SEPARATE_MIGRATION_ROLES=true", "IDENTITY_MIGRATE_DB_PASSWORD=a", "FHIR_MIGRATE_DB_PASSWORD=b"}, "IDENTITY_DB_PASSWORD"},
		"thiếu mật khẩu migrate":      {[]string{"DB_SEPARATE_MIGRATION_ROLES=true", "IDENTITY_DB_PASSWORD=a", "FHIR_DB_PASSWORD=b", "FHIR_MIGRATE_DB_PASSWORD=c"}, "IDENTITY_MIGRATE_DB_PASSWORD"},
		"thiếu mật khẩu migrate fhir": {[]string{"DB_SEPARATE_MIGRATION_ROLES=true", "IDENTITY_DB_PASSWORD=a", "FHIR_DB_PASSWORD=b", "IDENTITY_MIGRATE_DB_PASSWORD=c"}, "FHIR_MIGRATE_DB_PASSWORD"},
	} {
		code, out := run(c.env...)
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s: mã thoát %d, đầu ra phải nhắc %q:\n%s", name, code, c.want, out)
		}
	}
}
