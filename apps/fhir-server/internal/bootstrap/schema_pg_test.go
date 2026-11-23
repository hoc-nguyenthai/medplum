package bootstrap_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/testkit"
)

func countTables(t *testing.T, adminURL, schema string) int {
	t.Helper()
	db, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_schema = $1", schema).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Mô hình một database nhiều schema (docs/plans/microservices-shared-db.md): fhir-server tự migrate vào schema riêng; search_path rơi vào
// `public` thì từ chối khởi động và `migrate` không được tạo bảng ở đó.
func TestStartsOnOwnSchemaAndRefusesPublic(t *testing.T) {
	pg := testkit.Postgres(t)
	ctx := context.Background()
	dsn, schema, _ := pg.NewServiceRole(t)
	good := func(s *config.Settings) { s.DatabaseURL, s.DatabaseRequireSchema = dsn, true }
	bad := func(s *config.Settings) { s.DatabaseURL, s.DatabaseRequireSchema = pg.AdminURL(), true }

	c, err := bootstrap.New(ctx, settings(good))
	if err != nil {
		t.Fatalf("role và schema đúng phải khởi động được: %v", err)
	}
	if c.DB.Schema != schema {
		t.Errorf("Schema = %q, muốn %q", c.DB.Schema, schema)
	}
	_ = c.Stop(ctx)
	if n := countTables(t, pg.AdminURL(), schema); n == 0 {
		t.Error("migrate phải tạo bảng trong schema riêng")
	}

	if c, err := bootstrap.New(ctx, settings(bad)); err == nil {
		_ = c.Stop(ctx)
		t.Fatal("search_path rơi vào public mà vẫn khởi động")
	} else if !strings.Contains(err.Error(), "search_path") {
		t.Errorf("thông báo phải nói về search_path: %v", err)
	}
	if n := countTables(t, pg.AdminURL(), "public"); n != 0 {
		t.Errorf("khởi động bị từ chối mà vẫn tạo %d bảng ở public", n)
	}

	var out bytes.Buffer
	if err := bootstrap.Migrate(ctx, settings(bad), "up", &out); err == nil {
		t.Error("migrate trên search_path sai phải bị từ chối")
	}
	if n := countTables(t, pg.AdminURL(), "public"); n != 0 {
		t.Errorf("migrate bị từ chối mà vẫn tạo %d bảng ở public", n)
	}
	if err := bootstrap.Migrate(ctx, settings(good), "status", &out); err != nil || !strings.Contains(out.String(), "chưa áp dụng: 0") {
		t.Errorf("status: %v %q", err, out.String())
	}
}

func initScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../../../deploy/postgres-init/01-schemas.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func separateRolesPG(t *testing.T) *testkit.PG {
	t.Helper()
	return testkit.PostgresWithInit(t, testkit.InitSpec{Scripts: []string{initScript(t)}, Env: map[string]string{
		"DB_SEPARATE_MIGRATION_ROLES": "true", "IDENTITY_DB_PASSWORD": "ia", "FHIR_DB_PASSWORD": "fa",
		"IDENTITY_MIGRATE_DB_PASSWORD": "im", "FHIR_MIGRATE_DB_PASSWORD": "fm",
	}})
}

// D-DB-4: migrate bằng role fhir_migrate (có DDL), app chạy bằng fhir_app (chỉ DML).
func TestSeparateMigrationRolesFlow(t *testing.T) {
	pg := separateRolesPG(t)
	ctx := context.Background()
	appURL, migURL := pg.URLAs("fhir_app", "fa"), pg.URLAs("fhir_migrate", "fm")
	app := func(auto bool) func(*config.Settings) {
		return func(s *config.Settings) {
			s.DatabaseURL, s.DatabaseMigrateURL, s.DatabaseAutoMigrate, s.DatabaseRequireSchema = appURL, migURL, auto, true
		}
	}

	if c, err := bootstrap.New(ctx, settings(app(false))); err == nil {
		_ = c.Stop(ctx)
		t.Fatal("schema chưa migrate mà app vẫn khởi động")
	} else if !strings.Contains(err.Error(), "chưa được migrate") {
		t.Errorf("thông báo: %v", err)
	}
	if c, err := bootstrap.New(ctx, settings(func(s *config.Settings) { app(true)(s); s.DatabaseMigrateURL = "" })); err == nil {
		_ = c.Stop(ctx)
		t.Fatal("role app (không DDL) tự migrate được: D-DB-4 bị phá")
	}

	var out bytes.Buffer
	if err := bootstrap.Migrate(ctx, settings(app(false)), "up", &out); err != nil {
		t.Fatalf("migrate bằng role migrate: %v", err)
	}
	if err := bootstrap.Migrate(ctx, settings(app(false)), "status", &out); err != nil || !strings.Contains(out.String(), "chưa áp dụng: 0") {
		t.Errorf("status: %v %q", err, out.String())
	}

	c, err := bootstrap.New(ctx, settings(app(false)))
	if err != nil {
		t.Fatalf("app khởi động sau migrate: %v", err)
	}
	defer c.Stop(ctx)
	if _, err := c.Store.CreateResource(ctx, &fhir.Patient{ResourceType: "Patient", ID: "p-dml"}); err != nil {
		t.Fatalf("ghi resource bằng role chỉ DML: %v", err)
	}
	if r, err := c.Store.ReadResource(ctx, "Patient", "p-dml"); err != nil || r == nil {
		t.Fatalf("đọc lại: %v", err)
	}
	db, err := sql.Open("pgx", appURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"DROP TABLE fhir_resource", "ALTER TABLE fhir_resource ADD COLUMN x int", "CREATE TABLE y (id int)", "TRUNCATE fhir_resource"} {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("role app thực hiện được %q", q)
		}
	}
}
