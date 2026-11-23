package bootstrap

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
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

// Mô hình một database nhiều schema (docs/plans/microservices-shared-db.md): auth khởi động và tự migrate vào schema riêng của nó;
// nếu cấu hình để rơi vào `public` thì TỪ CHỐI khởi động và `migrate` cũng không được tạo bảng ở đó.
func TestStartsOnOwnSchemaAndRefusesPublic(t *testing.T) {
	pg := testkit.Postgres(t)
	ctx := context.Background()
	dsn, schema, _ := pg.NewServiceRole(t)
	good := func(s *config.Settings) { s.DatabaseURL, s.DatabaseRequireSchema = dsn, true }

	c, err := New(ctx, settings(t, good))
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

	bad := func(s *config.Settings) { s.DatabaseURL, s.DatabaseRequireSchema = pg.AdminURL(), true } // search_path mặc định: "$user", public
	if c, err := New(ctx, settings(t, bad)); err == nil {
		_ = c.Stop(ctx)
		t.Fatal("search_path rơi vào public mà vẫn khởi động")
	} else if !strings.Contains(err.Error(), "search_path") {
		t.Errorf("thông báo phải nói về search_path: %v", err)
	}
	if n := countTables(t, pg.AdminURL(), "public"); n != 0 {
		t.Errorf("khởi động bị từ chối mà vẫn tạo %d bảng ở public", n)
	}

	// `auth migrate up` đi qua cùng cổng kiểm: không ghi nhầm vào public, đúng schema thì chạy được
	var out bytes.Buffer
	if err := Migrate(ctx, settings(t, bad), "up", &out); err == nil {
		t.Error("migrate trên search_path sai phải bị từ chối")
	}
	if n := countTables(t, pg.AdminURL(), "public"); n != 0 {
		t.Errorf("migrate bị từ chối mà vẫn tạo %d bảng ở public", n)
	}
	if err := Migrate(ctx, settings(t, good), "status", &out); err != nil || !strings.Contains(out.String(), "chưa áp dụng: 0") {
		t.Errorf("status: %v %q", err, out.String())
	}

	// không bật cờ thì hành vi cũ giữ nguyên (chạy được trên public) để không phá triển khai database riêng
	schemaless := func(s *config.Settings) { s.DatabaseURL = pg.AdminURL() }
	if c, err := New(ctx, settings(t, schemaless)); err != nil {
		t.Errorf("không bật DATABASE_REQUIRE_SCHEMA thì phải chạy như trước: %v", err)
	} else {
		_ = c.Stop(ctx)
		_ = pg.Exec(ctx, "DROP TABLE IF EXISTS public.fhir_resource, public.fhir_history, public.fhir_idx_string, public.fhir_idx_token, public.fhir_idx_reference, public.fhir_idx_date, public.fhir_idx_number, public.fhir_idx_quantity, public.fhir_idx_uri, public.fhirstore_schema_version, public.goose_db_version CASCADE")
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

// D-DB-4: migrate bằng role identity_migrate (có DDL), app chạy bằng identity_app (chỉ DML). Mọi thứ app cần (kể cả seed project, một chuỗi
// ghi nhiều loại resource) chạy được chỉ với DML.
func TestSeparateMigrationRolesFlow(t *testing.T) {
	pg := separateRolesPG(t)
	ctx := context.Background()
	appURL, migURL := pg.URLAs("identity_app", "ia"), pg.URLAs("identity_migrate", "im")
	app := func(auto bool) func(*config.Settings) {
		return func(s *config.Settings) {
			s.DatabaseURL, s.DatabaseMigrateURL, s.DatabaseAutoMigrate, s.DatabaseRequireSchema = appURL, migURL, auto, true
		}
	}

	// chưa migrate: app (DATABASE_AUTO_MIGRATE=false) từ chối khởi động; nếu cố tự migrate thì role không có DDL nên cũng thất bại
	if c, err := New(ctx, settings(t, app(false))); err == nil {
		_ = c.Stop(ctx)
		t.Fatal("schema chưa migrate mà app vẫn khởi động")
	} else if !strings.Contains(err.Error(), "chưa được migrate") {
		t.Errorf("thông báo: %v", err)
	}
	if c, err := New(ctx, settings(t, func(s *config.Settings) { app(true)(s); s.DatabaseMigrateURL = "" })); err == nil {
		_ = c.Stop(ctx)
		t.Fatal("role app (không DDL) tự migrate được: D-DB-4 bị phá")
	}

	// bước deploy: `auth migrate up` đi bằng DATABASE_MIGRATE_URL
	var out bytes.Buffer
	if err := Migrate(ctx, settings(t, app(false)), "up", &out); err != nil {
		t.Fatalf("migrate bằng role migrate: %v", err)
	}
	if err := Migrate(ctx, settings(t, app(false)), "status", &out); err != nil || !strings.Contains(out.String(), "chưa áp dụng: 0") {
		t.Errorf("status: %v %q", err, out.String())
	}

	// app khởi động và làm việc đầy đủ chỉ với DML
	c, err := New(ctx, settings(t, app(false)))
	if err != nil {
		t.Fatalf("app khởi động sau migrate: %v", err)
	}
	defer c.Stop(ctx)
	seed, err := c.Auth.SeedProject(ctx, application.SeedProjectRequest{Name: "P", AdminEmail: "a@example.org", AdminFirst: "A", AdminLast: "B", AdminPassword: adminPassword})
	if err != nil || seed.Project.ID == "" {
		t.Fatalf("seed project bằng role chỉ DML: %v", err)
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
