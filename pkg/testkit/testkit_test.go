package testkit_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/testkit"
)

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPostgresSchemasAreIsolated(t *testing.T) {
	pg := testkit.Postgres(t)
	dsnA, schemaA := pg.NewSchema(t)
	dsnB, schemaB := pg.NewSchema(t)
	if schemaA == schemaB {
		t.Fatal("hai test phải có hai schema khác nhau")
	}
	a, b := open(t, dsnA), open(t, dsnB)
	ctx := context.Background()
	for _, db := range []*sql.DB{a, b} { // cùng tên bảng ở hai schema: không đụng nhau (đúng tình huống auth và fhir-server)
		if _, err := db.ExecContext(ctx, "CREATE TABLE fhir_resource (id text)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ExecContext(ctx, "INSERT INTO fhir_resource VALUES ('only-in-a')"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRowContext(ctx, "SELECT count(*) FROM fhir_resource").Scan(&n); err != nil || n != 0 {
		t.Errorf("schema B thấy dữ liệu của A: n=%d err=%v", n, err)
	}
	var sp string
	if err := a.QueryRowContext(ctx, "SHOW search_path").Scan(&sp); err != nil || !strings.Contains(sp, schemaA) {
		t.Errorf("search_path = %q, muốn chứa %s", sp, schemaA)
	}
}

func TestPostgresIsSharedAcrossTests(t *testing.T) { // một container cho cả tiến trình
	if testkit.Postgres(t) != testkit.Postgres(t) {
		t.Error("Postgres phải trả cùng một instance")
	}
}

func TestSchemaIsDroppedAfterTest(t *testing.T) {
	pg := testkit.Postgres(t)
	var schema string
	t.Run("create", func(t *testing.T) { _, schema = pg.NewSchema(t) })
	var n int
	db := open(t, pg.AdminURL())
	if err := db.QueryRow("SELECT count(*) FROM information_schema.schemata WHERE schema_name = $1", schema).Scan(&n); err != nil || n != 0 {
		t.Errorf("schema %s còn tồn tại sau khi test con kết thúc: n=%d err=%v", schema, n, err)
	}
}

// Mô hình quyền của docs/plans/microservices-shared-db.md: role của service chỉ làm được việc trong schema của nó.
func TestServiceRoleCannotTouchOtherSchemas(t *testing.T) {
	pg := testkit.Postgres(t)
	idDSN, idSchema, _ := pg.NewServiceRole(t)
	fhirDSN, fhirSchema, _ := pg.NewServiceRole(t)
	ctx := context.Background()
	identity, fhir := open(t, idDSN), open(t, fhirDSN)

	if _, err := identity.ExecContext(ctx, "CREATE TABLE users (id text, secret text)"); err != nil {
		t.Fatalf("role phải tạo được bảng trong schema của mình: %v", err)
	}
	if _, err := identity.ExecContext(ctx, "INSERT INTO users VALUES ('u1','bí mật')"); err != nil {
		t.Fatal(err)
	}
	var sp string
	if err := identity.QueryRowContext(ctx, "SHOW search_path").Scan(&sp); err != nil || !strings.Contains(sp, idSchema) {
		t.Errorf("search_path mặc định của role = %q, muốn %s", sp, idSchema)
	}
	for name, q := range map[string]string{
		"đọc bảng của schema khác": "SELECT * FROM " + idSchema + ".users",
		"ghi bảng của schema khác": "INSERT INTO " + idSchema + ".users VALUES ('x','y')",
		"tạo bảng ở schema khác":   "CREATE TABLE " + idSchema + ".evil (id text)",
		"tạo bảng ở public":        "CREATE TABLE public.evil (id text)",
	} {
		if _, err := fhir.ExecContext(ctx, q); err == nil {
			t.Errorf("%s: role %s phải bị từ chối", name, fhirSchema)
		}
	}
	// và ngược lại: role identity không đọc được schema fhir
	if _, err := fhir.ExecContext(ctx, "CREATE TABLE t (id text)"); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.ExecContext(ctx, "SELECT * FROM "+fhirSchema+".t"); err == nil {
		t.Error("role identity đọc được schema fhir")
	}
}

func TestRedisIsolatedAndRestartable(t *testing.T) {
	a, b := testkit.StartRedis(t), testkit.StartRedis(t)
	if a.Addr == b.Addr {
		t.Fatal("mỗi test phải có Redis riêng")
	}
	ctx := context.Background()
	ca, cb := goredis.NewClient(&goredis.Options{Addr: a.Addr}), goredis.NewClient(&goredis.Options{Addr: b.Addr})
	defer ca.Close()
	defer cb.Close()
	if err := ca.Set(ctx, "k", "va", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cb.Get(ctx, "k").Err(); !errors.Is(err, goredis.Nil) {
		t.Errorf("Redis B thấy khóa của A: %v", err)
	}
	// mô phỏng Redis chết rồi sống lại: lệnh lỗi khi dừng, chạy lại được sau khi bật (địa chỉ có thể đổi)
	a.Stop(t)
	dead, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := ca.Ping(dead).Err(); err == nil {
		t.Error("Redis đã dừng mà vẫn Ping được")
	}
	a.Start(t)
	ca2 := goredis.NewClient(&goredis.Options{Addr: a.Addr})
	defer ca2.Close()
	if err := ca2.Ping(ctx).Err(); err != nil {
		t.Errorf("Redis bật lại không dùng được: %v", err)
	}
}

func TestBuildImageRunServiceAndGracefulStop(t *testing.T) {
	tag := testkit.BuildImage(t, testkit.BuildSpec{Name: "testkit-echo", Context: "testdata/echo", Dockerfile: "testdata/echo/Dockerfile", Args: map[string]string{"GREETING": "xin chào"}})
	if tag != "testkit-testkit-echo:latest" {
		t.Errorf("tag = %s", tag)
	}
	if again := testkit.BuildImage(t, testkit.BuildSpec{Name: "testkit-echo", Context: "testdata/echo", Dockerfile: "testdata/echo/Dockerfile", Args: map[string]string{"GREETING": "xin chào"}}); again != tag {
		t.Errorf("dựng lại phải trả cùng tag: %s", again)
	}
	svc := testkit.RunService(t, testkit.ServiceSpec{Image: tag, Port: "8080/tcp", Health: "/health/ready", Env: map[string]string{"WHO": "testkit"}})
	get := func(path string) string {
		resp, err := http.Get(svc.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return strings.TrimSpace(string(b))
	}
	if g := get("/greeting"); g != "xin chào" {
		t.Errorf("build-arg không tới image: %q", g)
	}
	if w := get("/env"); w != "testkit" {
		t.Errorf("biến môi trường không tới container: %q", w)
	}
	if code, out := svc.Exec(t, "cat", "/www/greeting"); code != 0 || strings.TrimSpace(out) != "xin chào" {
		t.Errorf("Exec: code=%d out=%q", code, out)
	}
	if code, out := svc.ExecEnv(t, map[string]string{"TK_VAR": "giá-trị"}, "sh", "-c", "echo $TK_VAR"); code != 0 || strings.TrimSpace(out) != "giá-trị" {
		t.Errorf("ExecEnv: code=%d out=%q", code, out)
	}
	if code, _ := svc.Exec(t, "sh", "-c", "exit 7"); code != 7 {
		t.Errorf("Exec phải trả mã thoát của lệnh: %d", code)
	}
	if !strings.HasPrefix(svc.WSURL(), "ws://") {
		t.Errorf("WSURL = %s", svc.WSURL())
	}
	start := time.Now()
	code := svc.Terminate(t, 10*time.Second)
	if time.Since(start) > 9*time.Second || code != 0 {
		t.Errorf("Terminate mất %v, mã thoát %d: SIGTERM không được xử lý", time.Since(start), code)
	}
}

func TestRunToExitReportsExitCodeAndLogs(t *testing.T) {
	tag := testkit.BuildImage(t, testkit.BuildSpec{Name: "testkit-echo", Context: "testdata/echo", Dockerfile: "testdata/echo/Dockerfile", Args: map[string]string{"GREETING": "xin chào"}})
	code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: tag, Cmd: []string{"sh", "-c", "echo lỗi cấu hình: thiếu KEY; exit 3"}}, 30*time.Second)
	if code != 3 || !strings.Contains(logs, "thiếu KEY") {
		t.Errorf("code=%d logs=%q", code, logs)
	}
	if code, _ := testkit.RunToExit(t, testkit.ServiceSpec{Image: tag, Cmd: []string{"true"}}, 30*time.Second); code != 0 {
		t.Errorf("thoát thành công phải mã 0: %d", code)
	}
}

// ReadOnly chạy với root filesystem chỉ đọc (như readOnlyRootFilesystem của K8s): ghi đĩa ở đâu cũng phải bị từ chối; không bật thì ghi được.
func TestRunServiceReadOnlyRootFilesystem(t *testing.T) {
	tag := testkit.BuildImage(t, testkit.BuildSpec{Name: "testkit-echo", Context: "testdata/echo", Dockerfile: "testdata/echo/Dockerfile", Args: map[string]string{"GREETING": "xin chào"}})
	cmd := []string{"sh", "-c", "trap 'exit 0' TERM; httpd -f -p 8080 -h /www & wait"} // không ghi đĩa lúc khởi động
	rw := testkit.RunService(t, testkit.ServiceSpec{Image: tag, Port: "8080/tcp", Health: "/health/ready", Cmd: cmd})
	if code, out := rw.Exec(t, "sh", "-c", "echo a > /www/new"); code != 0 {
		t.Errorf("không ReadOnly thì phải ghi được: %d %s", code, out)
	}
	ro := testkit.RunService(t, testkit.ServiceSpec{Image: tag, Port: "8080/tcp", Health: "/health/ready", Cmd: cmd, ReadOnly: true})
	if code, _ := ro.Exec(t, "sh", "-c", "echo a > /www/new"); code == 0 {
		t.Error("ReadOnly mà vẫn ghi được đĩa")
	}
}
