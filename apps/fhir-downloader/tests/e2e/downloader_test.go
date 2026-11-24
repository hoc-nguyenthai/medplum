// Package e2e_test chạy fhir-downloader như một service ĐỘC LẬP: dựng image từ Dockerfile của repo rồi chạy cùng PostgreSQL và Redis thật bằng
// testcontainers, nói chuyện với nó chỉ qua mạng và qua sự kiện trên Redis, không import mã bên trong service. Vì dựng bằng Dockerfile nên các test này
// kiểm luôn cả image (người dùng không phải root, HEALTHCHECK, từ chối cấu hình sai, tắt êm).
//
// Phía fhir-server được MÔ PHỎNG trong test (ghi resource qua pkg/fhirstore và pkg/fhirsub lên Redis như fhir-server làm); hai bên chỉ chung schema và định dạng sự kiện
// (hợp đồng dây, C-6). Container tới PostgreSQL, Redis và máy chủ ngoài (giả, nằm trên máy chạy test) bằng host.docker.internal (Docker Desktop, OrbStack).
package e2e_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/testkit"
)

const dockerHost = "host.docker.internal"

// image dựng image fhir-downloader từ Dockerfile RIÊNG: context là gốc repo, cùng cách `make docker APP=fhir-downloader`.
func image(t *testing.T) string {
	t.Helper()
	testkit.RequireDocker(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("không tìm được thư mục module: %v", err)
	}
	root := strings.TrimSpace(string(out))
	return testkit.BuildImage(t, testkit.BuildSpec{
		Name: "fhir-downloader", Context: root, Dockerfile: filepath.Join(root, "apps/fhir-downloader/Dockerfile"),
	})
}

// fromContainer đổi địa chỉ nghe ở máy chạy test (localhost, 127.0.0.1) thành địa chỉ container thấy được.
func fromContainer(u string) string {
	return strings.NewReplacer("localhost", dockerHost, "127.0.0.1", dockerHost).Replace(u)
}

type stack struct {
	img      string
	dbURL    string // địa chỉ từ máy chạy test
	redisURL string
	store    *fhirstore.Store
	repo     fhirrouter.Repository // ghi như fhir-server: phát sự kiện lên Redis
}

// newStack dựng schema riêng ĐÃ migrate (như `fhir-server migrate up`) và Redis; chưa chạy service nào.
func newStack(t *testing.T) *stack {
	t.Helper()
	img := image(t)
	pg := testkit.Postgres(t)
	dsn, _ := pg.NewSchema(t)
	rds := testkit.StartRedis(t)
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: dsn})
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
	br, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: rds.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Stop(context.Background()) })
	return &stack{img: img, dbURL: dsn, redisURL: rds.URL, store: st, repo: fhirsub.Wrap(st, br)}
}

// run chạy fhir-downloader; mut chỉnh biến môi trường.
func (s *stack) run(t *testing.T, mut func(env map[string]string)) *testkit.Service {
	t.Helper()
	env := map[string]string{
		"ENVIRONMENT": "local", "LOG_JSON": "true", "SHUTDOWN_DRAIN_DELAY": "0s",
		"DATABASE_URL": fromContainer(s.dbURL), "REDIS_URL": fromContainer(s.redisURL), "FHIR_BINARY_STORAGE": "file:/data/blobs",
		"FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE": "true", // máy chủ ngoài giả nằm trên máy chạy test (địa chỉ nội bộ với container)
	}
	if mut != nil {
		mut(env)
	}
	svc := testkit.RunService(t, testkit.ServiceSpec{Image: s.img, Port: "8009/tcp", Health: "/health/ready", Env: env})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("log fhir-downloader:\n%s", svc.Logs(t))
		}
	})
	return svc
}

// site dựng "máy chủ ngoài" nghe mọi giao diện để container tới được; hits đếm số lần bị gọi.
func site(t *testing.T, body []byte) (base string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	}))
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return "http://" + dockerHost + ":" + port, hits
}

var seq atomic.Int32

func (s *stack) createDoc(t *testing.T, project, url string) string {
	t.Helper()
	d := &fhir.DocumentReference{ID: "doc" + strconv.Itoa(int(seq.Add(1))) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36), Status: "current", Meta: &fhir.Meta{Project: project},
		Content: []fhir.DocumentReferenceContent{{Attachment: &fhir.Attachment{URL: url}}}}
	if _, err := s.repo.CreateResource(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func (s *stack) attachmentURL(t *testing.T, id string) string {
	t.Helper()
	r, err := s.store.ReadResource(context.Background(), "DocumentReference", id)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*fhir.DocumentReference).Content[0].Attachment.URL
}

func (s *stack) waitBinary(t *testing.T, id string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if u := s.attachmentURL(t, id); strings.HasPrefix(u, "Binary/") {
			return strings.TrimPrefix(u, "Binary/")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("attachment chưa thành Binary/<id>: %q", s.attachmentURL(t, id))
	return ""
}

func (s *stack) binary(t *testing.T, id string) *fhir.Binary {
	t.Helper()
	r, err := s.store.ReadResource(context.Background(), "Binary", id)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*fhir.Binary)
}

func TestImageIsHealthyNonRootAndDownloadsEndToEnd(t *testing.T) {
	s := newStack(t)
	svc := s.run(t, nil)
	if code, out := svc.Exec(t, "/app", "healthcheck"); code != 0 {
		t.Fatalf("/app healthcheck: %d %s", code, out)
	}
	if code, out := svc.Exec(t, "/app", "khong-co-lenh"); code == 0 || !strings.Contains(out, "cách dùng") {
		t.Errorf("lệnh lạ phải bị từ chối: %d %s", code, out)
	}
	base, hits := site(t, []byte("%PDF-1.7 nội dung thật"))

	id := s.createDoc(t, "projA", base+"/tai-lieu.pdf?token=bimat")
	b := s.binary(t, s.waitBinary(t, id))
	if b.Meta == nil || b.Meta.Project != "projA" || b.SecurityContext == nil || b.SecurityContext.Reference != "DocumentReference/"+id || b.ContentType != "application/pdf" {
		t.Errorf("Binary phải thuộc project của resource nguồn: %+v", b)
	}
	if hits.Load() != 1 {
		t.Errorf("máy chủ ngoài bị gọi %d lần, muốn 1", hits.Load())
	}
	if strings.Contains(svc.Logs(t), "bimat") {
		t.Error("log không được chứa token ký trong query của URL")
	}
}

// Chống SSRF mặc định: không bật ALLOW_PRIVATE thì địa chỉ nội bộ bị chặn, tài liệu giữ nguyên URL.
func TestBlocksInternalAddressesByDefaultInTheImage(t *testing.T) {
	s := newStack(t)
	s.run(t, func(env map[string]string) { delete(env, "FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE") })
	base, hits := site(t, []byte("bí mật nội bộ"))
	id := s.createDoc(t, "projA", base+"/noi-bo.pdf")
	time.Sleep(3 * time.Second)
	if hits.Load() != 0 || !strings.HasPrefix(s.attachmentURL(t, id), "http://") {
		t.Fatalf("địa chỉ nội bộ phải bị chặn: gọi %d lần, url=%q", hits.Load(), s.attachmentURL(t, id))
	}
}

// Service dừng (SIGTERM, thoát mã 0), sự kiện ghi trong lúc không chạy nằm lại trên Redis; chạy lại thì xử lý nốt.
func TestStopsGracefullyAndResumesEventsAfterRestart(t *testing.T) {
	s := newStack(t)
	first := s.run(t, nil)
	base, hits := site(t, []byte("%PDF một"))
	id1 := s.createDoc(t, "projA", base+"/1.pdf")
	s.waitBinary(t, id1)

	start := time.Now()
	if code := first.Terminate(t, 20*time.Second); code != 0 {
		t.Fatalf("tắt êm phải thoát mã 0, được %d", code)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("tắt êm mất %s", d)
	}
	id2 := s.createDoc(t, "projA", base+"/2.pdf") // ghi lúc không có service nào chạy
	time.Sleep(time.Second)
	if strings.HasPrefix(s.attachmentURL(t, id2), "Binary/") {
		t.Fatal("chưa có service nào chạy mà đã tải")
	}
	s.run(t, nil)
	s.waitBinary(t, id2)
	if hits.Load() != 2 {
		t.Errorf("máy chủ ngoài bị gọi %d lần, muốn 2 (mỗi tệp một lần, tệp 1 không tải lại)", hits.Load())
	}
}

func TestRefusesBadConfigurationAndUnmigratedSchema(t *testing.T) {
	img := image(t)
	t.Run("thiếu kho Binary", func(t *testing.T) {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{"ENVIRONMENT": "local", "DATABASE_URL": "sqlite://:memory:"}}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, "FHIR_BINARY_STORAGE") {
			t.Fatalf("muốn thoát khác 0 và nêu FHIR_BINARY_STORAGE: %d %s", code, logs)
		}
	})
	t.Run("schema chưa migrate", func(t *testing.T) {
		testkit.RequireDocker(t)
		pg := testkit.Postgres(t)
		dsn, _ := pg.NewSchema(t) // schema trống: chưa chạy `fhir-server migrate up`
		rds := testkit.StartRedis(t)
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{
			"ENVIRONMENT": "local", "DATABASE_URL": fromContainer(dsn), "REDIS_URL": fromContainer(rds.URL), "FHIR_BINARY_STORAGE": "file:/data/blobs",
		}}, 60*time.Second)
		if code == 0 || !strings.Contains(logs, "schema chưa được migrate") {
			t.Fatalf("muốn từ chối vì schema chưa migrate: %d %s", code, logs)
		}
	})
}
