package fhirstore_test

import (
	"context"
	"testing"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirrouter/repotest"
	"trivita-fhir/pkg/fhirstore"
)

// newSQLite trả về Store trên SQLite in-memory đã migrate (mỗi test một DB riêng).
func newSQLite(t testing.TB) *fhirstore.Store {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: "sqlite://:memory:"})
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

// Toàn bộ bộ test hành vi REST (CRUD, history, search, conditional, patch, batch, transaction...) chạy trên SQL.
func TestSQLiteRepository(t *testing.T) {
	repotest.Run(t, func(t *testing.T) fhirrouter.Repository { return newSQLite(t) })
}
