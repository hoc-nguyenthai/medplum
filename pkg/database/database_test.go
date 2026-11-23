package database_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"trivita-fhir/pkg/database"
)

var migrations = fstest.MapFS{
	"sqlite/00001_t.sql": {Data: []byte("-- +goose Up\nCREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n-- +goose Down\nDROP TABLE t;\n")},
	"sqlite/00002_u.sql": {Data: []byte("-- +goose Up\nCREATE TABLE u (id INTEGER PRIMARY KEY);\n-- +goose Down\nDROP TABLE u;\n")},
}

func TestDialectOf(t *testing.T) {
	cases := map[string]database.Dialect{
		"postgres://u:p@h/db": database.Postgres, "postgresql://h/db": database.Postgres,
		"mysql://u@h/db": database.MySQL, "sqlite://./a.db": database.SQLite, "SQLITE://:memory:": database.SQLite,
	}
	for in, want := range cases {
		if got, err := database.DialectOf(in); err != nil || got != want {
			t.Errorf("%s -> %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"app.db", "oracle://x", "postgresql+asyncpg"} {
		if _, err := database.DialectOf(bad); err == nil {
			t.Errorf("%q phải lỗi", bad)
		}
	}
	if _, err := database.DialectOf("postgres://user:s3cr3t@h/db"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DialectOf("noscheme://"); err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenSQLiteFileMigrateAndRollback(t *testing.T) {
	ctx := context.Background()
	url := "sqlite://" + filepath.Join(t.TempDir(), "app.db")
	db, err := database.Open(ctx, database.Config{URL: url, MaxOpenConns: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.IsInMemory() || db.Dialect != database.SQLite {
		t.Fatal("phải là sqlite file")
	}

	if n, err := db.Pending(ctx, migrations, "app_schema_version"); err != nil || n != 2 {
		t.Fatalf("pending = %d %v", n, err)
	}
	if err := db.Migrate(ctx, migrations, "app_schema_version"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Pending(ctx, migrations, "app_schema_version"); n != 0 {
		t.Fatalf("pending = %d", n)
	}
	if err := db.ORM.Exec("INSERT INTO t (name) VALUES ('x')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Rollback(ctx, migrations, "app_schema_version"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Pending(ctx, migrations, "app_schema_version"); n != 1 {
		t.Fatalf("sau rollback pending = %d", n)
	}
	var fk int
	db.SQL.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign_keys phải bật")
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOpenInMemoryIsolatedPerInstance(t *testing.T) {
	ctx := context.Background()
	a, err := database.Open(ctx, database.Config{URL: "sqlite://:memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, _ := database.Open(ctx, database.Config{URL: "sqlite://:memory:", Echo: true})
	defer b.Close()
	if !a.IsInMemory() {
		t.Fatal("in-memory")
	}
	if err := a.Migrate(ctx, migrations, "v"); err != nil {
		t.Fatal(err)
	}
	if err := b.ORM.Exec("INSERT INTO t (name) VALUES ('x')").Error; err == nil {
		t.Fatal("hai DB in-memory phải độc lập")
	}
	// nhiều goroutine dùng chung 1 connection vẫn thấy cùng dữ liệu
	if err := a.ORM.Exec("INSERT INTO t (name) VALUES ('x')").Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigrateErrors(t *testing.T) {
	ctx := context.Background()
	db, _ := database.Open(ctx, database.Config{URL: "sqlite://:memory:"})
	defer db.Close()
	if err := db.Migrate(ctx, fstest.MapFS{"postgres/1.sql": {}}, "v"); err == nil {
		t.Fatal("thiếu thư mục dialect phải lỗi")
	}
	if _, err := db.Pending(ctx, fstest.MapFS{}, "v"); err == nil {
		t.Fatal("pending thiếu thư mục phải lỗi")
	}
	if err := db.Rollback(ctx, fstest.MapFS{}, "v"); err == nil {
		t.Fatal("rollback thiếu thư mục phải lỗi")
	}
	bad := fstest.MapFS{"sqlite/00001_x.sql": {Data: []byte("-- +goose Up\nNOT SQL;\n")}}
	if err := db.Migrate(ctx, bad, "v2"); err == nil {
		t.Fatal("SQL hỏng phải lỗi")
	}
}

func TestOpenFailuresDoNotLeakPassword(t *testing.T) {
	ctx := context.Background()
	if _, err := database.Open(ctx, database.Config{URL: "mongodb://x"}); err == nil {
		t.Fatal("dialect lạ")
	}
	_, err := database.Open(ctx, database.Config{URL: "postgres://user:s3cr3t@127.0.0.1:1/db?connect_timeout=1"})
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("lỗi kết nối postgres phải che mật khẩu: %v", err)
	}
	_, err = database.Open(ctx, database.Config{URL: "mysql://user:s3cr3t@127.0.0.1:1/db"})
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("lỗi kết nối mysql phải che mật khẩu: %v", err)
	}
	if _, err := database.Open(ctx, database.Config{URL: "mysql://%zz"}); err == nil {
		t.Fatal("URL mysql hỏng")
	}
	if _, err := database.Open(ctx, database.Config{URL: "sqlite:///nonexistent-dir-xyz/a/b.db"}); err == nil {
		t.Fatal("đường dẫn sqlite không tạo được phải lỗi khi ping")
	}
}
