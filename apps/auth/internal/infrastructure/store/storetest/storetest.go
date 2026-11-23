// Package storetest cung cấp các backend (bộ nhớ, SQLite, PostgreSQL tùy chọn) để chạy cùng một bộ test cho store và application.
package storetest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" cho tempPostgres

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
)

// Backend là một repository nền dùng để chạy cùng một bộ test.
type Backend struct {
	Name string
	Repo func(t *testing.T) fhirrouter.Repository
}

// Mọi test của kho chạy trên bộ nhớ, SQLite thật và (nếu đặt AUTH_TEST_POSTGRES_URL) PostgreSQL thật, cùng một bộ hành vi.
// SQLite dùng MỘT kết nối: SQLite trả SQLITE_BUSY khi nhiều transaction đọc-rồi-ghi chạy đồng thời (giới hạn của SQLite,
// chỉ dùng cho dev; production cấm SQLite).
func Backends() []Backend {
	bs := []Backend{
		{"memory", func(*testing.T) fhirrouter.Repository { return fhirrouter.NewMemoryRepository() }},
		{"sqlite", func(t *testing.T) fhirrouter.Repository {
			return OpenStore(t, "sqlite://"+filepath.Join(sqliteDir(t), "s.db"), 1)
		}},
	}
	if admin := os.Getenv("AUTH_TEST_POSTGRES_URL"); admin != "" {
		bs = append(bs, Backend{"postgres", func(t *testing.T) fhirrouter.Repository { return OpenStore(t, tempPostgres(t, admin), 16) }})
	}
	return bs
}

// sqliteDir trả một thư mục tạm riêng cho file SQLite. Không dùng t.TempDir() trực tiếp: tên subtest lặp lại có hậu tố `#01`, và `#` trong URL
// bị coi là fragment nên đường dẫn bị cắt, làm các lần chạy (và các test) dùng chung một file SQLite rò rỉ trạng thái lẫn nhau.
func sqliteDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "authsqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// OpenStore mở và migrate một fhirstore.Store trên URL.
func OpenStore(t *testing.T, url string, conns int) fhirrouter.Repository {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: url, MaxOpenConns: conns})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := fhirstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

// tempPostgres tạo một database tạm riêng cho test (và xóa khi xong), trả URL của nó.
func tempPostgres(t *testing.T, adminURL string) string {
	t.Helper()
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("auth_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		_ = admin.Close()
	})
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
