// Package database mở kết nối SQL một lần (database/sql + GORM trên cùng pool) và chạy migration goose.
//
// Dialect suy ra từ scheme của URL: postgres:// | postgresql:// | mysql:// | sqlite://<file> | sqlite://:memory:
package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite" // driver "sqlite" (thuần Go, không cần CGO)

	"trivita-fhir/pkg/core"
)

// Dialect là loại cơ sở dữ liệu.
type Dialect string

const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
	SQLite   Dialect = "sqlite"
)

// Config cấu hình Open.
type Config struct {
	URL             string
	Echo            bool // log SQL (có thể lộ dữ liệu cá nhân: chỉ bật khi debug)
	MaxOpenConns    int  // 0 = mặc định 20 (SQLite in-memory luôn là 1)
	MaxIdleConns    int  // 0 = mặc định 5
	ConnMaxLifetime time.Duration
	// RequireSchema (chỉ PostgreSQL) từ chối mở kết nối nếu search_path không phải đúng MỘT schema riêng (không phải public, không phải
	// "$user") đã tồn tại. Dùng cho mô hình một database nhiều schema (docs/plans/microservices-shared-db.md): app ghi nhầm vào `public`
	// hoặc đọc lẫn schema của service khác khi search_path cấu hình sai thì thà không khởi động. Với dialect khác: lỗi (SQLite và MySQL
	// không có schema, không được tạo cảm giác an toàn giả).
	RequireSchema bool
}

// DB gói *sql.DB và *gorm.DB dùng chung một pool.
type DB struct {
	SQL     *sql.DB
	ORM     *gorm.DB
	Dialect Dialect
	// Schema là schema PostgreSQL mà app dùng (chỉ có khi Config.RequireSchema đã kiểm); rỗng nếu không kiểm.
	Schema string

	inMemory bool
}

// DialectOf trả về dialect của URL.
func DialectOf(rawURL string) (Dialect, error) {
	scheme, _, ok := strings.Cut(rawURL, "://")
	if !ok {
		return "", fmt.Errorf("database: URL thiếu scheme: %q", redact(rawURL))
	}
	switch strings.ToLower(scheme) {
	case "postgres", "postgresql":
		return Postgres, nil
	case "mysql":
		return MySQL, nil
	case "sqlite":
		return SQLite, nil
	}
	return "", fmt.Errorf("database: dialect không được hỗ trợ: %q", scheme)
}

// redact che mật khẩu trong URL trước khi đưa vào thông báo lỗi.
func redact(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		return u.Redacted()
	}
	return raw
}

// Open mở kết nối và kiểm tra bằng Ping.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	dialect, err := DialectOf(cfg.URL)
	if err != nil {
		return nil, err
	}
	db := &DB{Dialect: dialect}

	var dialector gorm.Dialector
	switch dialect {
	case SQLite:
		path := strings.TrimPrefix(cfg.URL, cfg.URL[:strings.Index(cfg.URL, "://")+3])
		db.inMemory = path == "" || path == ":memory:"
		if db.SQL, err = sql.Open("sqlite", sqliteDSN(path, db.inMemory)); err != nil {
			return nil, err
		}
		dialector = sqlite.Dialector{Conn: db.SQL}
	case Postgres:
		if db.SQL, err = sql.Open("pgx", cfg.URL); err != nil {
			return nil, fmt.Errorf("database: %s", redact(err.Error()))
		}
		dialector = postgres.New(postgres.Config{Conn: db.SQL})
	case MySQL:
		dsn, derr := mysqlDSN(cfg.URL)
		if derr != nil {
			return nil, derr
		}
		if db.SQL, err = sql.Open("mysql", dsn); err != nil {
			return nil, err
		}
		dialector = mysql.New(mysql.Config{Conn: db.SQL})
	}

	maxOpen, maxIdle := cfg.MaxOpenConns, cfg.MaxIdleConns
	if maxOpen <= 0 {
		maxOpen = 20
	}
	if maxIdle <= 0 {
		maxIdle = 5
	}
	if db.inMemory {
		maxOpen, maxIdle = 1, 1 // giữ 1 connection để DB in-memory không biến mất
	}
	db.SQL.SetMaxOpenConns(maxOpen)
	db.SQL.SetMaxIdleConns(maxIdle)
	if cfg.ConnMaxLifetime > 0 {
		db.SQL.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	level := logger.Silent // GORM log kèm tham số SQL = dữ liệu cá nhân
	if cfg.Echo {
		level = logger.Info
	}
	db.ORM, err = gorm.Open(dialector, &gorm.Config{
		Logger:         logger.Default.LogMode(level),
		TranslateError: true,
		NowFunc:        core.Now,
	})
	if err != nil {
		_ = db.SQL.Close()
		return nil, fmt.Errorf("database: mở GORM: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		_ = db.SQL.Close()
		return nil, fmt.Errorf("database: không kết nối được: %s", redact(err.Error()))
	}
	if cfg.RequireSchema {
		if dialect != Postgres {
			_ = db.SQL.Close()
			return nil, fmt.Errorf("database: RequireSchema chỉ áp dụng cho PostgreSQL (dialect hiện tại: %s)", dialect)
		}
		schema, err := requireSchema(ctx, db.SQL)
		if err != nil {
			_ = db.SQL.Close()
			return nil, err
		}
		db.Schema = schema
	}
	return db, nil
}

// requireSchema đọc search_path của kết nối và đòi đúng một schema riêng, đã tồn tại. Trả tên schema.
func requireSchema(ctx context.Context, db *sql.DB) (string, error) {
	var sp string
	if err := db.QueryRowContext(ctx, "SHOW search_path").Scan(&sp); err != nil {
		return "", fmt.Errorf("database: không đọc được search_path: %w", err)
	}
	schema, err := ownSchema(sp)
	if err != nil {
		return "", err
	}
	var cur sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&cur); err != nil {
		return "", fmt.Errorf("database: không đọc được schema hiện tại: %w", err)
	}
	if !cur.Valid || cur.String != schema {
		return "", fmt.Errorf("database: schema %q của search_path chưa tồn tại (hoặc role không có quyền dùng nó): tạo schema và cấp quyền trước (xem deploy/postgres-init)", schema)
	}
	return schema, nil
}

// ownSchema phân tích giá trị search_path (ví dụ `identity`, `"$user", public`, `fhir, public`) và đòi đúng MỘT schema riêng. Nếu còn
// `public` hay schema khác trong đường tìm kiếm thì một bảng cùng tên nằm ở đó sẽ che hoặc thay thế bảng của service, nên bị từ chối.
func ownSchema(searchPath string) (string, error) {
	var names []string
	for _, p := range strings.Split(searchPath, ",") {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			names = append(names, p)
		}
	}
	switch {
	case len(names) == 0:
		return "", fmt.Errorf("database: search_path rỗng: đặt `ALTER ROLE <role> SET search_path = <schema>`")
	case len(names) > 1:
		return "", fmt.Errorf("database: search_path %q có nhiều schema: chỉ được đúng một schema riêng của service (không kèm public hay schema khác)", searchPath)
	}
	switch name := names[0]; {
	case name == "public" || name == "pg_catalog" || name == "information_schema" || strings.HasPrefix(name, "$"):
		return "", fmt.Errorf("database: search_path %q không phải schema riêng của service: đặt `ALTER ROLE <role> SET search_path = <schema>`", searchPath)
	default:
		return name, nil
	}
}

func sqliteDSN(path string, inMemory bool) string {
	pragmas := "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if inMemory {
		return "file:" + core.NewID() + "?mode=memory&cache=shared&" + pragmas
	}
	return "file:" + path + "?" + pragmas + "&_pragma=journal_mode(WAL)"
}

// mysqlDSN đổi mysql://user:pass@host:3306/db?x=y thành DSN của go-sql-driver.
func mysqlDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("database: URL MySQL không hợp lệ")
	}
	host := u.Host
	if u.Port() == "" {
		host += ":3306"
	}
	q := u.Query()
	if q.Get("parseTime") == "" {
		q.Set("parseTime", "true")
	}
	if q.Get("loc") == "" {
		q.Set("loc", "UTC")
	}
	cred := ""
	if u.User != nil {
		pw, _ := u.User.Password()
		cred = u.User.Username() + ":" + pw + "@"
	}
	return cred + "tcp(" + host + ")/" + strings.TrimPrefix(u.Path, "/") + "?" + q.Encode(), nil
}

// IsInMemory báo DB SQLite in-memory (dùng cho test).
func (d *DB) IsInMemory() bool { return d.inMemory }

// Ping kiểm tra DB còn dùng được (cho readiness).
func (d *DB) Ping(ctx context.Context) error { return d.SQL.PingContext(ctx) }

// Close đóng pool.
func (d *DB) Close() error { return d.SQL.Close() }

func gooseDialect(d Dialect) (goose.Dialect, error) {
	switch d {
	case Postgres:
		return goose.DialectPostgres, nil
	case MySQL:
		return goose.DialectMySQL, nil
	case SQLite:
		return goose.DialectSQLite3, nil
	}
	return "", fmt.Errorf("database: dialect không được hỗ trợ: %q", d)
}

// provider tạo goose provider từ fsys có cấu trúc `<dialect>/*.sql`.
func (d *DB) provider(fsys fs.FS, table string) (*goose.Provider, error) {
	gd, err := gooseDialect(d.Dialect)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(fsys, string(d.Dialect))
	if err != nil {
		return nil, fmt.Errorf("database: thiếu thư mục migration cho %s: %w", d.Dialect, err)
	}
	return goose.NewProvider(gd, d.SQL, sub, goose.WithTableName(table))
}

// Migrate áp dụng mọi migration chưa chạy. fsys chứa thư mục con theo dialect (postgres, mysql, sqlite);
// table là bảng ghi phiên bản của app (mỗi app một bảng riêng để không đụng nhau).
func (d *DB) Migrate(ctx context.Context, fsys fs.FS, table string) error {
	p, err := d.provider(fsys, table)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}
	return nil
}

// Rollback hoàn tác migration gần nhất.
func (d *DB) Rollback(ctx context.Context, fsys fs.FS, table string) error {
	p, err := d.provider(fsys, table)
	if err != nil {
		return err
	}
	if _, err := p.Down(ctx); err != nil {
		return fmt.Errorf("database: rollback: %w", err)
	}
	return nil
}

// Pending trả về số migration chưa áp dụng (0 = schema mới nhất). KHÔNG có tác dụng phụ: goose tự tạo bảng phiên bản khi chưa có, nhưng một
// role chỉ có DML (D-DB-4) không có quyền tạo bảng, nên với PostgreSQL mà bảng phiên bản chưa tồn tại thì mọi migration đều đang chờ và ta
// trả luôn số đó (để app báo "schema chưa được migrate" thay vì "permission denied").
func (d *DB) Pending(ctx context.Context, fsys fs.FS, table string) (int, error) {
	p, err := d.provider(fsys, table)
	if err != nil {
		return 0, err
	}
	if d.Dialect == Postgres {
		var reg sql.NullString
		if err := d.SQL.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
			return 0, fmt.Errorf("database: kiểm bảng phiên bản: %w", err)
		}
		if !reg.Valid {
			return len(p.ListSources()), nil
		}
	}
	statuses, err := p.Status(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range statuses {
		if s.State == goose.StatePending {
			n++
		}
	}
	return n, nil
}
