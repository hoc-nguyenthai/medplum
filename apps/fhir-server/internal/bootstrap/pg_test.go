package bootstrap_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"

	_ "trivita-fhir/pkg/database" // đăng ký driver pgx/sqlite cho sql.Open
)

// Đặt FHIRSTORE_TEST_POSTGRES_URL thì mọi test e2e dùng `settings()` chạy trên PostgreSQL (mỗi lần gọi một schema riêng,
// xóa khi chạy xong) thay vì SQLite trong bộ nhớ: JWT, project, grant, policy, job, Subscription, GraphQL... qua HTTP.
// Test tự đặt DatabaseURL (file SQLite để kiểm tra khởi động lại) giữ nguyên SQLite.
const postgresEnv = "FHIRSTORE_TEST_POSTGRES_URL"

var (
	pgMu      sync.Mutex
	pgSchemas []string
)

func testDatabaseURL() string {
	base := os.Getenv(postgresEnv)
	if base == "" {
		return "sqlite://:memory:"
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	schema := "bootstrap_e2e_" + hex.EncodeToString(rnd[:])
	admin, err := sql.Open("pgx", base)
	if err == nil {
		defer admin.Close()
		_, err = admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	}
	if err != nil {
		panic(fmt.Sprintf("tạo schema test PostgreSQL: %v", err))
	}
	pgMu.Lock()
	pgSchemas = append(pgSchemas, schema)
	pgMu.Unlock()
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func TestMain(m *testing.M) {
	code := m.Run()
	if base := os.Getenv(postgresEnv); base != "" {
		if admin, err := sql.Open("pgx", base); err == nil {
			for _, s := range pgSchemas {
				_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+s+" CASCADE")
			}
			_ = admin.Close()
		}
	}
	os.Exit(code)
}
