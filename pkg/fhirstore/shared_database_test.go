package fhirstore_test

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/testkit"
)

// Mô hình "một database nhiều schema" của docs/plans/microservices-shared-db.md: auth và fhir-server dùng CÙNG bộ bảng và CÙNG tên bảng
// phiên bản của pkg/fhirstore. Chạy chung một database với role và schema riêng thì không đụng nhau, và role này không đọc được schema kia.
func TestTwoServicesShareOneDatabase(t *testing.T) {
	pg := testkit.Postgres(t)
	ctx := context.Background()
	idDSN, idSchema, _ := pg.NewServiceRole(t)
	fhirDSN, fhirSchema, _ := pg.NewServiceRole(t)

	open := func(dsn string) *fhirstore.Store {
		t.Helper()
		db, err := database.Open(ctx, database.Config{URL: dsn, RequireSchema: true, MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		st, err := fhirstore.New(db)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	identity, clinical := open(idDSN), open(fhirDSN)
	for name, st := range map[string]*fhirstore.Store{"identity": identity, "clinical": clinical} {
		if n, err := st.Pending(ctx); err != nil || n == 0 {
			t.Fatalf("%s: schema mới phải còn migration chờ: n=%d err=%v", name, n, err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("%s: migrate: %v", name, err)
		}
		if err := st.Migrate(ctx); err != nil { // chạy lại không đổi gì
			t.Fatalf("%s: migrate lần hai: %v", name, err)
		}
		if n, _ := st.Pending(ctx); n != 0 {
			t.Errorf("%s: còn %d migration sau khi migrate", name, n)
		}
	}

	// mỗi schema có ĐỦ bộ bảng, kể cả bảng phiên bản riêng; `public` không có bảng nào của hai service
	admin, err := sql.Open("pgx", pg.AdminURL())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	tables := func(schema string) []string {
		rows, err := admin.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema = $1 ORDER BY 1", schema)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			out = append(out, n)
		}
		return out
	}
	idTables, fhirTables := tables(idSchema), tables(fhirSchema)
	if strings.Join(idTables, ",") != strings.Join(fhirTables, ",") {
		t.Errorf("hai schema phải có cùng bộ bảng:\n%v\n%v", idTables, fhirTables)
	}
	for _, want := range []string{"fhir_resource", "fhir_history", "fhirstore_schema_version"} {
		i := sort.SearchStrings(idTables, want)
		if i == len(idTables) || idTables[i] != want {
			t.Errorf("schema thiếu bảng %s: %v", want, idTables)
		}
	}
	if pub := tables("public"); len(pub) != 0 {
		t.Errorf("`public` không được có bảng nào của service: %v", pub)
	}

	// dữ liệu cách ly: ghi ở clinical, identity không thấy
	if _, err := clinical.CreateResource(ctx, &fhir.Patient{ResourceType: "Patient", ID: "p-clinical"}); err != nil {
		t.Fatal(err)
	}
	if r, err := clinical.ReadResource(ctx, "Patient", "p-clinical"); err != nil || r == nil {
		t.Fatalf("clinical không đọc lại được dữ liệu của chính nó: %v", err)
	}
	if _, err := identity.ReadResource(ctx, "Patient", "p-clinical"); err == nil {
		t.Error("identity thấy dữ liệu của clinical")
	}

	// quyền DB: role của clinical không đọc, ghi hay phá bảng của identity, và ngược lại
	clinicalSQL, err := sql.Open("pgx", fhirDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer clinicalSQL.Close()
	for name, q := range map[string]string{
		"đọc":      "SELECT count(*) FROM " + idSchema + ".fhir_resource",
		"ghi":      "DELETE FROM " + idSchema + ".fhir_resource",
		"phá":      "DROP TABLE " + idSchema + ".fhir_resource",
		"tạo":      "CREATE TABLE " + idSchema + ".x (id int)",
		"ở public": "CREATE TABLE public.x (id int)",
	} {
		if _, err := clinicalSQL.ExecContext(ctx, q); err == nil {
			t.Errorf("role của clinical %s được schema khác", name)
		}
	}

	// migration chạy độc lập: schema thứ ba chưa migrate thì chỉ nó còn chờ
	thirdDSN, _, _ := pg.NewServiceRole(t)
	third := open(thirdDSN)
	if n, _ := third.Pending(ctx); n == 0 {
		t.Error("schema thứ ba chưa migrate phải còn migration chờ")
	}
	if n, _ := identity.Pending(ctx); n != 0 {
		t.Errorf("migrate schema khác không được ảnh hưởng identity: %d", n)
	}
}
