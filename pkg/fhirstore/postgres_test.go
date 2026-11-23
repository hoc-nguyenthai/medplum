package fhirstore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirrouter/repotest"
	"trivita-fhir/pkg/fhirstore"
)

// PostgresEnv là biến môi trường chứa URL PostgreSQL dùng cho test tích hợp, ví dụ
//
//	FHIRSTORE_TEST_POSTGRES_URL=postgres://postgres:secret@localhost:5432/postgres?sslmode=disable
//
// Mỗi test tạo một schema riêng (search_path) và xóa khi xong, nên không đụng dữ liệu có sẵn.
const PostgresEnv = "FHIRSTORE_TEST_POSTGRES_URL"

func newPostgres(t testing.TB) *fhirstore.Store {
	t.Helper()
	base := os.Getenv(PostgresEnv)
	if base == "" {
		t.Skipf("đặt %s để chạy test PostgreSQL", PostgresEnv)
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	schema := "fhirstore_test_" + hex.EncodeToString(rnd[:])
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := database.Open(ctx, database.Config{URL: u.String(), MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := fhirstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPostgresRepository(t *testing.T) {
	repotest.Run(t, func(t *testing.T) fhirrouter.Repository { return newPostgres(t) })
}

func TestPostgresDifferentialAgainstMemory(t *testing.T) {
	runDifferential(t, newPostgres(t))
}
