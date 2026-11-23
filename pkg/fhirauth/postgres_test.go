package fhirauth_test

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
	"trivita-fhir/pkg/fhirstore"
)

// postgresEnv giống fhirstore: URL PostgreSQL cho test tích hợp. Có biến này thì các bộ test tách project (hành vi dùng
// chung qua Scope, cô lập chéo project, update-as-create) chạy thêm trên PostgreSQL; mỗi test dùng một schema riêng.
const postgresEnv = "FHIRSTORE_TEST_POSTGRES_URL"

func newPostgres(t *testing.T) *fhirstore.Store {
	t.Helper()
	base := os.Getenv(postgresEnv)
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	schema := "fhirauth_test_" + hex.EncodeToString(rnd[:])
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

// withPostgres thêm backend PostgreSQL vào danh sách khi đặt biến môi trường.
func withPostgres(t *testing.T, m map[string]func() fhirrouter.Repository) map[string]func() fhirrouter.Repository {
	if os.Getenv(postgresEnv) != "" {
		m["postgres"] = func() fhirrouter.Repository { return newPostgres(t) }
	}
	return m
}
