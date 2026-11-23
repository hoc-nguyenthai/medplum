package database_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/testkit"
)

func TestRequireSchemaRejectsNonPostgres(t *testing.T) {
	_, err := database.Open(context.Background(), database.Config{URL: "sqlite://:memory:", RequireSchema: true})
	if err == nil || !strings.Contains(err.Error(), "chỉ áp dụng cho PostgreSQL") {
		t.Errorf("SQLite với RequireSchema phải lỗi: %v", err)
	}
	// không đòi thì SQLite mở bình thường (không đổi hành vi cũ)
	db, err := database.Open(context.Background(), database.Config{URL: "sqlite://:memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Schema != "" {
		t.Errorf("Schema = %q khi không kiểm", db.Schema)
	}
}

// roleURL tạo role với search_path tùy ý (kể cả sai) và trả URL đăng nhập bằng role đó.
func roleURL(t *testing.T, pg *testkit.PG, searchPath string, schemas ...string) string {
	t.Helper()
	ctx := context.Background()
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	role := "r_" + hex.EncodeToString(rnd[:])
	steps := []string{"CREATE ROLE " + role + " LOGIN PASSWORD 'pw'"}
	for _, s := range schemas {
		steps = append(steps, "CREATE SCHEMA "+s+" AUTHORIZATION "+role)
	}
	if searchPath != "" {
		steps = append(steps, "ALTER ROLE "+role+" SET search_path = "+searchPath)
	}
	for _, q := range steps {
		if err := pg.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() {
		for _, s := range schemas {
			_ = pg.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+s+" CASCADE")
		}
		_ = pg.Exec(context.Background(), "DROP ROLE IF EXISTS "+role)
	})
	u, _ := url.Parse(pg.AdminURL())
	u.User = url.UserPassword(role, "pw")
	return u.String()
}

func TestRequireSchemaOnPostgres(t *testing.T) {
	pg := testkit.Postgres(t)
	ctx := context.Background()
	open := func(u string, require bool) (*database.DB, error) {
		return database.Open(ctx, database.Config{URL: u, RequireSchema: require, MaxOpenConns: 2})
	}

	// role của một service (testkit): search_path = schema của nó
	dsn, schema, _ := pg.NewServiceRole(t)
	db, err := open(dsn, true)
	if err != nil {
		t.Fatalf("role đúng phải mở được: %v", err)
	}
	if db.Schema != schema {
		t.Errorf("Schema = %q, muốn %q", db.Schema, schema)
	}
	_ = db.Close()

	// search_path đặt qua tham số DSN (cách của test cũ) cũng được
	dsnParam, schemaParam := pg.NewSchema(t)
	if db, err := open(dsnParam, true); err != nil || db.Schema != schemaParam {
		t.Errorf("search_path qua DSN: %v", err)
	} else {
		_ = db.Close()
	}

	for name, c := range map[string]struct {
		url  string
		want string
	}{
		"superuser dùng search_path mặc định (\"$user\", public)": {pg.AdminURL(), "nhiều schema"},
		"search_path kèm public":       {roleURL(t, pg, "svc_a, public", "svc_a"), "nhiều schema"},
		"search_path hai schema riêng": {roleURL(t, pg, "svc_b, svc_b2", "svc_b", "svc_b2"), "nhiều schema"},
		"search_path là public":        {roleURL(t, pg, "public"), "không phải schema riêng"},
		"schema chưa tồn tại":          {roleURL(t, pg, "svc_missing"), "chưa tồn tại"},
	} {
		db, err := open(c.url, true)
		if err == nil {
			_ = db.Close()
			t.Errorf("%s: phải bị từ chối", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: lỗi %q, muốn chứa %q", name, err, c.want)
		}
		if strings.Contains(err.Error(), "pw@") || strings.Contains(err.Error(), ":pw") {
			t.Errorf("%s: lỗi lộ mật khẩu: %v", name, err)
		}
	}

	// không đòi thì hành vi cũ giữ nguyên: superuser/public vẫn mở được
	if db, err := open(pg.AdminURL(), false); err != nil {
		t.Errorf("không RequireSchema phải mở được: %v", err)
	} else {
		_ = db.Close()
	}
}

// Pending không có tác dụng phụ: trên PostgreSQL mà bảng phiên bản chưa tồn tại thì báo mọi migration đang chờ và KHÔNG tạo bảng.
func TestPendingHasNoSideEffectsOnPostgres(t *testing.T) {
	pg := testkit.Postgres(t)
	ctx := context.Background()
	dsn, schema := pg.NewSchema(t)
	db, err := database.Open(ctx, database.Config{URL: dsn, RequireSchema: true, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fsys := fstest.MapFS{
		"postgres/00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE a (id int);\n")},
		"postgres/00002_b.sql": {Data: []byte("-- +goose Up\nCREATE TABLE b (id int);\n")},
	}
	count := func() string {
		var n string
		if err := db.SQL.QueryRow("SELECT count(*)::text FROM information_schema.tables WHERE table_schema = $1", schema).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n, err := db.Pending(ctx, fsys, "tk_version"); err != nil || n != 2 {
		t.Fatalf("Pending trước migrate = %d, %v; muốn 2", n, err)
	}
	if c := count(); c != "0" {
		t.Errorf("Pending đã tạo %s bảng trong schema", c)
	}
	if err := db.Migrate(ctx, fsys, "tk_version"); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Pending(ctx, fsys, "tk_version"); err != nil || n != 0 {
		t.Errorf("Pending sau migrate = %d, %v; muốn 0", n, err)
	}
}
