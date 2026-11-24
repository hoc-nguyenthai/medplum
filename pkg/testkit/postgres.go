package testkit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// PostgresImage là image PostgreSQL (khớp docker-compose.yml); đổi bằng TESTKIT_POSTGRES_IMAGE.
const defaultPostgresImage = "postgres:16-alpine"

// PG là PostgreSQL dùng chung của cả tiến trình test.
type PG struct {
	adminURL string
	admin    *sql.DB
	c        *tcpostgres.PostgresContainer
}

var (
	pgOnce sync.Once
	pg     *PG
	pgErr  error
)

// Postgres trả PostgreSQL dùng chung (khởi động ở lần gọi đầu tiên; container được Ryuk dọn khi tiến trình thoát).
func Postgres(t testing.TB) *PG {
	t.Helper()
	RequireDocker(t)
	pgOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
		defer cancel()
		c, err := tcpostgres.Run(ctx, env("TESTKIT_POSTGRES_IMAGE", defaultPostgresImage),
			tcpostgres.WithDatabase("testkit"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("secret"),
			tcpostgres.BasicWaitStrategies())
		if err != nil {
			pgErr = fmt.Errorf("khởi động PostgreSQL: %w", err)
			return
		}
		u, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			pgErr = err
			return
		}
		db, err := sql.Open("pgx", u)
		if err != nil {
			pgErr = err
			return
		}
		db.SetMaxOpenConns(4)
		pg = &PG{adminURL: u, admin: db, c: c}
	})
	if pgErr != nil {
		t.Fatalf("testkit: %v", pgErr)
	}
	return pg
}

// AdminURL là URL superuser (chỉ để dựng role, schema; ứng dụng dưới test nên dùng NewSchema hoặc NewServiceRole).
func (p *PG) AdminURL() string { return p.adminURL }

func randName(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// NewSchema tạo một schema riêng cho test và trả URL có search_path trỏ vào nó (như pkg/fhirstore/postgres_test.go), cùng tên
// schema. Schema bị xóa (CASCADE) khi test xong.
func (p *PG) NewSchema(t testing.TB) (dsn, schema string) {
	t.Helper()
	schema = randName("t_")
	ctx := context.Background()
	if _, err := p.admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("testkit: tạo schema: %v", err)
	}
	t.Cleanup(func() { _, _ = p.admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	return p.withQuery(p.adminURL, "search_path", schema), schema
}

// NewServiceRole tạo schema riêng VÀ một role chỉ có quyền trên schema đó (mô hình của một service: không quyền gì trên schema
// khác, không quyền tạo thứ gì ở `public`), role có search_path mặc định là schema. Trả URL đăng nhập bằng role đó.
func (p *PG) NewServiceRole(t testing.TB) (dsn, schema, role string) {
	t.Helper()
	ctx := context.Background()
	schema, role = randName("s_"), randName("r_")
	pw := randName("pw")
	stmts := []string{
		"CREATE ROLE " + role + " LOGIN PASSWORD '" + pw + "'",
		"CREATE SCHEMA " + schema + " AUTHORIZATION " + role,
		"ALTER ROLE " + role + " SET search_path = " + schema,
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
	}
	for _, s := range stmts {
		if _, err := p.admin.ExecContext(ctx, s); err != nil {
			t.Fatalf("testkit: %s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = p.admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_, _ = p.admin.ExecContext(context.Background(), "DROP ROLE "+role)
	})
	u, _ := url.Parse(p.adminURL)
	u.User = url.UserPassword(role, pw)
	return u.String(), schema, role
}

func (p *PG) withQuery(raw, k, v string) string {
	u, _ := url.Parse(raw)
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String()
}

// Exec chạy một câu lệnh bằng superuser (dựng dữ liệu hoặc kiểm tra); trả lỗi để test tự kiểm.
func (p *PG) Exec(ctx context.Context, query string, args ...any) error {
	_, err := p.admin.ExecContext(ctx, query, args...)
	return err
}

// InitSpec mô tả một PostgreSQL RIÊNG (không dùng chung với test khác) chạy các script khởi tạo thật của repo, để test kiểm đúng thứ
// được triển khai (ví dụ deploy/postgres-init/01-schemas.sh) thay vì một bản dựng lại trong test.
type InitSpec struct {
	Scripts []string          // đường dẫn tệp .sh/.sql, chạy theo thứ tự tên khi khởi tạo
	Env     map[string]string // biến môi trường của container (mật khẩu role, cờ chế độ...)
}

// PostgresWithInit khởi động một PostgreSQL riêng cho test, chạy Scripts qua docker-entrypoint-initdb.d, và dọn khi test xong. Tên database
// là "testkit", superuser postgres/secret (xem AdminURL).
func PostgresWithInit(t testing.TB, spec InitSpec) *PG {
	t.Helper()
	RequireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
	defer cancel()
	opts := []testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase("testkit"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("secret"),
		tcpostgres.WithInitScripts(spec.Scripts...), tcpostgres.BasicWaitStrategies(),
	}
	if len(spec.Env) > 0 {
		opts = append(opts, testcontainers.WithEnv(spec.Env))
	}
	c, err := tcpostgres.Run(ctx, env("TESTKIT_POSTGRES_IMAGE", defaultPostgresImage), opts...)
	if c != nil {
		testcontainers.CleanupContainer(t, c)
	}
	if err != nil {
		t.Fatalf("testkit: khởi động PostgreSQL với script init: %s", withLogs(context.Background(), c, err))
	}
	u, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testkit: %v", err)
	}
	db, err := sql.Open("pgx", u)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	return &PG{adminURL: u, admin: db, c: c}
}

// URLAs trả URL đăng nhập vào cùng database bằng một role khác (ví dụ role do script init tạo).
func (p *PG) URLAs(user, password string) string {
	u, _ := url.Parse(p.adminURL)
	u.User = url.UserPassword(user, password)
	return u.String()
}

// ExecInContainer chạy một lệnh trong container PostgreSQL (ví dụ chạy lại script init) và trả mã thoát cùng đầu ra.
func (p *PG) ExecInContainer(t testing.TB, cmd ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	code, rd, err := p.c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("testkit: exec %v: %v", cmd, err)
	}
	b, _ := io.ReadAll(rd)
	return code, string(b)
}

// QuietExec chạy một lệnh trong container PostgreSQL và trả đầu ra; lỗi thì trả chuỗi rỗng (dùng cho số đo tùy chọn, không làm hỏng test).
func (p *PG) QuietExec(cmd ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, rd, err := p.c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return ""
	}
	b, _ := io.ReadAll(rd)
	return string(b)
}
