// Package fhirstore là fhirrouter.Repository trên SQL (PostgreSQL và SQLite) dùng pkg/database.
//
// Lược đồ (xem migrations/): fhir_resource lưu bản hiện tại, fhir_history lưu mọi phiên bản,
// và các bảng fhir_idx_<kiểu> lưu giá trị search parameter đã trích khi ghi. Search được dịch
// thành SQL EXISTS trên các bảng chỉ mục; việc trích giá trị và phân tích điều kiện dùng chung
// với fhirsearch.Match nên ngữ nghĩa giống MemoryRepository (có test đối chiếu).
//
// Tham số composite dùng chỉ mục thành phần (code "<code>#<i>", ord = chỉ số phần tử). Chưa hỗ trợ MySQL. Chained search và _has được fhirrouter giải bằng sub-search trước khi gọi Search
// (Search trả 501 nếu nhận Request còn Chained).
package fhirstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore/migrations"
)

// MigrationTable là bảng goose ghi phiên bản schema của fhirstore.
const MigrationTable = "fhirstore_schema_version"

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store là Repository trên SQL.
type Store struct {
	db  *database.DB
	tx  *sql.Tx // khác nil khi chạy trong RunInTransaction
	d   dialect
	now func() time.Time
}

// New tạo Store trên một kết nối đã mở. Dialect hỗ trợ: Postgres, SQLite.
func New(db *database.DB) (*Store, error) {
	switch db.Dialect {
	case database.Postgres, database.SQLite:
	default:
		return nil, fmt.Errorf("fhirstore: dialect %q chưa được hỗ trợ", db.Dialect)
	}
	return &Store{db: db, d: dialect{pg: db.Dialect == database.Postgres}, now: time.Now}, nil
}

// Migrate áp dụng schema của fhirstore.
func (s *Store) Migrate(ctx context.Context) error {
	return s.db.Migrate(ctx, migrations.FS, MigrationTable)
}

// Pending trả về số migration chưa áp dụng (0 = schema mới nhất).
func (s *Store) Pending(ctx context.Context) (int, error) {
	return s.db.Pending(ctx, migrations.FS, MigrationTable)
}

// Ping kiểm tra kết nối cơ sở dữ liệu.
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

func (s *Store) q() querier {
	if s.tx != nil {
		return s.tx
	}
	return s.db.SQL
}

// inTx chạy fn trong một transaction (dùng transaction hiện có nếu đã ở trong RunInTransaction).
func (s *Store) inTx(ctx context.Context, fn func(q querier) error) (err error) {
	if s.tx != nil {
		return fn(s.tx)
	}
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fhirrouter.Internal(err.Error())
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fhirrouter.Internal(err.Error())
	}
	return nil
}

// RunInTransaction chạy fn trong một transaction SQL; lỗi từ fn hủy mọi thay đổi.
func (s *Store) RunInTransaction(ctx context.Context, fn func(fhirrouter.Repository) error) error {
	if s.tx != nil {
		return fn(s) // đã trong transaction
	}
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fhirrouter.Internal(err.Error())
	}
	ts := &Store{db: s.db, tx: tx, d: s.d, now: s.now}
	if err := fn(ts); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fhirrouter.Internal(err.Error())
	}
	return nil
}

// LockKey khóa theo key tới hết transaction hiện tại (xem fhirrouter.KeyLocker). PostgreSQL: advisory lock theo transaction.
// SQLite: một câu lệnh ghi rỗng để chiếm khóa ghi ngay từ đầu (writer khác chờ theo busy_timeout), tránh lỗi nâng cấp khóa
// SQLITE_BUSY khi hai transaction cùng đọc rồi cùng ghi. Ngoài transaction: không làm gì.
func (s *Store) LockKey(ctx context.Context, key string) error {
	if s.tx == nil {
		return nil
	}
	var err error
	if s.d.pg {
		_, err = s.tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", key)
	} else {
		_, err = s.tx.ExecContext(ctx, "DELETE FROM fhir_resource WHERE 1 = 0")
	}
	if err != nil {
		return internal(err)
	}
	return nil
}

var (
	_ fhirrouter.Repository    = (*Store)(nil)
	_ fhirrouter.Transactional = (*Store)(nil)
	_ fhirrouter.KeyLocker     = (*Store)(nil)
)

func (s *Store) stamp() string { return s.now().UTC().Format("2006-01-02T15:04:05.000Z") }

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ---------------------------------------------------------------- dialect

type dialect struct{ pg bool }

func (d dialect) ph(n int) string {
	if d.pg {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// jsonParam bọc placeholder của cột JSON.
func (d dialect) jsonParam(ph string) string {
	if d.pg {
		return ph + "::jsonb"
	}
	return ph
}

// collateC buộc so sánh theo byte (khớp thứ tự chuỗi của Go); SQLite mặc định đã là BINARY.
func (d dialect) collateC() string {
	if d.pg {
		return ` COLLATE "C"`
	}
	return ""
}

// contains trả về biểu thức SQL "col chứa ph".
func (d dialect) contains(col, ph string) string {
	if d.pg {
		return "position(" + ph + " in " + col + ") > 0"
	}
	return "instr(" + col + ", " + ph + ") > 0"
}

// text ép placeholder về kiểu text.
func (d dialect) text(ph string) string { return "CAST(" + ph + " AS TEXT)" }

// qb gom câu SQL và tham số.
type qb struct {
	d    dialect
	sb   strings.Builder
	args []any
}

func (b *qb) arg(v any) string {
	b.args = append(b.args, v)
	return b.d.ph(len(b.args))
}

func (b *qb) w(s string) { b.sb.WriteString(s) }
