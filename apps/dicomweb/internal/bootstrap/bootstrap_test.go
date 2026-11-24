package bootstrap_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/dicomweb/internal/bootstrap"
	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirstore"
)

const (
	secret = "jwt-shared-secret-0123456789abcdef-xyz"
	issuer = "https://id.test"
)

// settings dựng cấu hình thật trong thư mục tạm: SQLite đã migrate (do "fhir-server", dicomweb không tự migrate) và kho tệp.
func settings(t *testing.T, migrate bool, mut ...func(*config.Settings)) config.Settings {
	t.Helper()
	dir := t.TempDir()
	url := "sqlite://" + filepath.Join(dir, "fhir.db")
	if migrate {
		db, err := database.Open(context.Background(), database.Config{URL: url})
		if err != nil {
			t.Fatal(err)
		}
		st, err := fhirstore.New(db)
		if err == nil {
			err = st.Migrate(context.Background())
		}
		_ = db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := config.Default()
	s.DatabaseURL = url
	s.JWTIssuer, s.JWTAudience, s.JWTHMACSecret = issuer, "fhir", secret
	s.BinaryStorage = "file:" + filepath.Join(dir, "blobs")
	s.Addr, s.ShutdownDrainDelay = "127.0.0.1:0", 200*time.Millisecond
	for _, m := range mut {
		m(&s)
	}
	return s
}

func tok(t *testing.T, project, scope string, over ...func(jwt.MapClaims)) string {
	t.Helper()
	c := jwt.MapClaims{"iss": issuer, "aud": "fhir", "sub": "user@" + project, "exp": time.Now().Add(time.Hour).Unix(), "scope": scope}
	if project != "" {
		c["fhir_project"] = project
	}
	for _, o := range over {
		o(c)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func handler(t *testing.T, s config.Settings) (*bootstrap.Container, http.Handler) {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, e
}

func call(h http.Handler, method, path, token string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// Token do auth cấp (JWT thật, kiểm bằng Verifier thật) đi qua xác thực và cô lập project; route DICOM chưa cài trả 501.
func TestRealJWTAuthentication(t *testing.T) {
	_, h := handler(t, settings(t, true))
	rd, wr := tok(t, "A", "fhir.read"), tok(t, "A", "openid fhir.write")

	for _, c := range []struct {
		name, method, path, token string
		want                      int
	}{
		{"không token", "GET", "/dicomweb/studies", "", 401},
		{"token rác", "GET", "/dicomweb/studies", "garbage", 401},
		{"hết hạn", "GET", "/dicomweb/studies", tok(t, "A", "fhir.read", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }), 401},
		{"sai audience", "GET", "/dicomweb/studies", tok(t, "A", "fhir.read", func(c jwt.MapClaims) { c["aud"] = "other" }), 401},
		{"sai issuer", "GET", "/dicomweb/studies", tok(t, "A", "fhir.read", func(c jwt.MapClaims) { c["iss"] = "https://evil.test" }), 401},
		{"thiếu project", "GET", "/dicomweb/studies", tok(t, "", "fhir.read"), 403},
		{"thiếu scope fhir", "GET", "/dicomweb/studies", tok(t, "A", "openid email"), 403},
		{"đọc", "GET", "/dicomweb/studies", rd, 501},
		{"STOW bằng token đọc", "POST", "/dicomweb/studies", rd, 403},
		{"STOW bằng token ghi nhưng không phải multipart: qua xác thực, vào STOW", "POST", "/dicomweb/studies", wr, 415},
		{"đúng project trong URL", "GET", "/projects/A/dicomweb/studies", rd, 501},
		{"project khác trong URL", "GET", "/projects/B/dicomweb/studies", rd, 403},
		{"scope fhir.super bị bỏ qua khi chưa bật cờ: vẫn là token thường của project A", "GET", "/projects/B/dicomweb/studies", tok(t, "A", "fhir.read fhir.super"), 403},
	} {
		if got := call(h, c.method, c.path, c.token); got != c.want {
			t.Errorf("%s: %d, muốn %d", c.name, got, c.want)
		}
	}
}

// Super-admin (bật bằng FHIR_JWT_SUPER_ADMIN) hành động trong project chọn bằng URL hoặc header; không chọn thì 400.
func TestSuperAdminWhenEnabled(t *testing.T) {
	_, h := handler(t, settings(t, true, func(s *config.Settings) { s.JWTSuperAdmin = true }))
	super := tok(t, "", "fhir.super")
	if got := call(h, "GET", "/dicomweb/studies", super); got != 400 {
		t.Errorf("không chọn project: %d", got)
	}
	if got := call(h, "GET", "/projects/B/dicomweb/studies", super); got != 501 {
		t.Errorf("chọn bằng URL: %d", got)
	}
}

// Kho resource của Container đã tách theo project: project B không đọc được DicomStudy của project A, kể cả khi biết id.
func TestRepoIsolatesProjects(t *testing.T) {
	c, _ := handler(t, settings(t, true))
	ctx := func(project string) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: project, Role: fhirauth.RoleWrite})
	}
	created, err := c.Repo.CreateResource(ctx("A"), &fhir.DicomStudy{ID: "study-a", StudyInstanceUid: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Repo.ReadResource(ctx("A"), "DicomStudy", created.GetID()); err != nil {
		t.Errorf("project A đọc study của mình: %v", err)
	}
	if _, err := c.Repo.ReadResource(ctx("B"), "DicomStudy", created.GetID()); err == nil {
		t.Error("project B đọc được DicomStudy của project A")
	}
	if _, err := c.Repo.ReadResource(context.Background(), "DicomStudy", created.GetID()); err == nil {
		t.Error("không có Principal mà đọc được dữ liệu")
	}
}

// Kho nội dung dùng được ngay sau khi dựng (D3 ghi instance vào đây).
func TestBlobStoreIsReady(t *testing.T) {
	c, _ := handler(t, settings(t, true))
	key, err := binarystore.Key("bin1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Blobs.Put(context.Background(), key, strings.NewReader("DICM"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	r, err := c.Blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if b, _ := io.ReadAll(r); string(b) != "DICM" {
		t.Errorf("nội dung: %q", b)
	}
}

// dicomweb không tự migrate: schema của fhir-server chưa chạy migrate thì từ chối khởi động với thông báo rõ ràng, và không tạo bảng nào.
func TestRefusesToStartOnUnmigratedSchema(t *testing.T) {
	s := settings(t, false)
	_, err := bootstrap.New(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("muốn từ chối vì chưa migrate, được %v", err)
	}
	db, err := database.Open(context.Background(), database.Config{URL: s.DatabaseURL})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, _ := fhirstore.New(db)
	if n, _ := st.Pending(context.Background()); n == 0 {
		t.Error("dicomweb đã tự migrate schema của fhir-server")
	}
}

func TestRefusesBadStorage(t *testing.T) {
	// thư mục kho nằm dưới một tệp thường nên không tạo được
	s := settings(t, true)
	file := filepath.Join(t.TempDir(), "plain")
	if err := writeFile(file); err != nil {
		t.Fatal(err)
	}
	s.BinaryStorage = "file:" + filepath.Join(file, "sub")
	if _, err := bootstrap.New(context.Background(), s); err == nil {
		t.Error("kho không mở được phải làm New lỗi")
	}
}

// SIGTERM: readiness chuyển 503 trong lúc drain nhưng liveness vẫn 200, rồi Serve thoát mã 0 (không lỗi).
func TestServeGracefulShutdown(t *testing.T) {
	s := settings(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- bootstrap.Serve(ctx, s, bootstrap.ServeOptions{OnListen: func(a net.Addr) { addr <- a }})
	}()
	var base string
	select {
	case a := <-addr:
		base = "http://" + a.String()
	case err := <-done:
		t.Fatalf("Serve thoát sớm: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("không nghe cổng")
	}
	get := func(p string) int {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if get("/health/ready") != 200 || get("/health/live") != 200 || get("/healthz") != 200 {
		t.Fatal("chưa sẵn sàng")
	}
	if got := get("/dicomweb/studies"); got != 401 {
		t.Fatalf("route DICOMweb thật phải đòi xác thực: %d", got)
	}
	cancel()
	time.Sleep(60 * time.Millisecond)
	if get("/health/ready") != 503 {
		t.Error("trong lúc drain ready phải 503")
	}
	if get("/health/live") != 200 {
		t.Error("liveness không được đổi khi drain")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tắt êm phải không lỗi: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve không thoát")
	}
}

func writeFile(path string) error {
	return os.WriteFile(path, []byte("x"), 0o600)
}
