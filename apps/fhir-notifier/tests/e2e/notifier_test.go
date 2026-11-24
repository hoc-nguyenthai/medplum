// Package e2e_test chạy fhir-notifier như một service ĐỘC LẬP: dựng image từ Dockerfile của repo rồi chạy cùng PostgreSQL và Redis thật bằng testcontainers, nói chuyện
// với nó chỉ qua mạng và qua sự kiện trên Redis, không import mã bên trong service. Vì dựng bằng Dockerfile nên các test này kiểm luôn cả image (người dùng không
// phải root, HEALTHCHECK, từ chối cấu hình sai, tắt êm).
//
// Phía fhir-server được MÔ PHỎNG trong test (ghi resource qua pkg/fhirstore và pkg/fhirsub lên Redis như fhir-server làm); hai bên chỉ chung schema và định dạng sự kiện
// (hợp đồng dây, C-6). Container tới PostgreSQL, Redis và máy nhận webhook (giả, nằm trên máy chạy test) bằng host.docker.internal (Docker Desktop, OrbStack).
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// image dựng image fhir-notifier từ Dockerfile RIÊNG: context là gốc repo, cùng cách `make docker APP=fhir-notifier`.
func image(t *testing.T) string {
	t.Helper()
	testkit.RequireDocker(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("không tìm được thư mục module: %v", err)
	}
	root := strings.TrimSpace(string(out))
	return testkit.BuildImage(t, testkit.BuildSpec{
		Name: "fhir-notifier", Context: root, Dockerfile: filepath.Join(root, "apps/fhir-notifier/Dockerfile"),
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

// run chạy fhir-notifier; mut chỉnh biến môi trường.
func (s *stack) run(t *testing.T, mut func(env map[string]string)) *testkit.Service {
	t.Helper()
	env := map[string]string{
		"ENVIRONMENT": "local", "LOG_JSON": "true", "SHUTDOWN_DRAIN_DELAY": "0s", "FHIR_WEBHOOK_TIMEOUT": "3s",
		"DATABASE_URL": fromContainer(s.dbURL), "REDIS_URL": fromContainer(s.redisURL),
		"FHIR_WEBHOOK_ALLOW_PRIVATE": "true", // máy nhận giả nằm trên máy chạy test (địa chỉ nội bộ với container)
	}
	if mut != nil {
		mut(env)
	}
	svc := testkit.RunService(t, testkit.ServiceSpec{Image: s.img, Port: "8010/tcp", Health: "/health/ready", Env: env, ReadOnly: true})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("log fhir-notifier:\n%s", svc.Logs(t))
		}
	})
	return svc
}

// receiver là "máy nhận webhook" nghe mọi giao diện để container tới được.
type receiver struct {
	base  string
	mu    sync.Mutex
	calls []map[string]any
	hdr   []http.Header
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		r.mu.Lock()
		r.calls, r.hdr = append(r.calls, body), append(r.hdr, req.Header.Clone())
		r.mu.Unlock()
		w.WriteHeader(200)
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
	r.base = "http://" + dockerHost + ":" + port
	return r
}

func (r *receiver) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

func (s *stack) create(t *testing.T, js string) {
	t.Helper()
	res, err := fhir.UnmarshalResource([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.CreateResource(context.Background(), res); err != nil {
		t.Fatal(err)
	}
}

func (s *stack) subscribe(t *testing.T, id, criteria, endpoint string) {
	t.Helper()
	s.create(t, `{"resourceType":"Subscription","id":"`+id+`","meta":{"project":"projA"},"status":"active","reason":"e2e","criteria":"`+criteria+
		`","channel":{"type":"rest-hook","endpoint":"`+endpoint+`","header":["X-Tenant: acme"]}}`)
}

func (s *stack) patient(t *testing.T, family string) {
	t.Helper()
	s.create(t, `{"resourceType":"Patient","meta":{"project":"projA"},"name":[{"family":"`+family+`"}]}`)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("quá hạn chờ: %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestImageIsHealthyNonRootAndDeliversEndToEnd(t *testing.T) {
	s := newStack(t)
	svc := s.run(t, nil) // ReadOnly: service không ghi đĩa
	if code, out := svc.Exec(t, "/app", "healthcheck"); code != 0 {
		t.Fatalf("/app healthcheck: %d %s", code, out)
	}
	if code, out := svc.Exec(t, "/app", "khong-co-lenh"); code == 0 || !strings.Contains(out, "cách dùng") {
		t.Errorf("lệnh lạ phải bị từ chối: %d %s", code, out)
	}
	rcv := newReceiver(t)
	s.subscribe(t, "s1", "Patient?family=Hooked", rcv.base)
	time.Sleep(time.Second) // Subscription được nạp vào bộ nhớ đệm
	s.patient(t, "Hooked")
	s.patient(t, "Ignored")
	eventually(t, "một thông báo", func() bool { return rcv.count() == 1 })
	time.Sleep(time.Second)
	rcv.mu.Lock()
	body, hdr := rcv.calls[0], rcv.hdr[0]
	n := len(rcv.calls)
	rcv.mu.Unlock()
	if n != 1 || body["resourceType"] != "Patient" || hdr.Get("X-Medplum-Subscription") != "s1" || hdr.Get("X-Tenant") != "acme" {
		t.Errorf("thông báo: %d %v %v", n, body, hdr)
	}
}

// Chống SSRF mặc định: không bật ALLOW_PRIVATE thì địa chỉ nội bộ bị chặn (Subscription còn bị bỏ qua khi nạp).
func TestBlocksInternalAddressesByDefaultInTheImage(t *testing.T) {
	s := newStack(t)
	s.run(t, func(env map[string]string) { delete(env, "FHIR_WEBHOOK_ALLOW_PRIVATE") })
	rcv := newReceiver(t)
	s.subscribe(t, "s1", "Patient?family=Hooked", rcv.base)
	time.Sleep(time.Second)
	s.patient(t, "Hooked")
	time.Sleep(4 * time.Second)
	if rcv.count() != 0 {
		t.Fatalf("địa chỉ nội bộ phải bị chặn: gọi %d lần", rcv.count())
	}
}

// Service dừng (SIGTERM, thoát mã 0), sự kiện ghi trong lúc không chạy nằm lại trên Redis; chạy lại thì giao nốt.
func TestStopsGracefullyAndResumesEventsAfterRestart(t *testing.T) {
	s := newStack(t)
	first := s.run(t, nil)
	rcv := newReceiver(t)
	s.subscribe(t, "s1", "Patient?family=Hooked", rcv.base)
	time.Sleep(time.Second)
	s.patient(t, "Hooked")
	eventually(t, "thông báo thứ nhất", func() bool { return rcv.count() == 1 })

	start := time.Now()
	if code := first.Terminate(t, 20*time.Second); code != 0 {
		t.Fatalf("tắt êm phải thoát mã 0, được %d", code)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("tắt êm mất %s", d)
	}
	s.patient(t, "Hooked") // ghi lúc không có service nào chạy
	time.Sleep(time.Second)
	if rcv.count() != 1 {
		t.Fatal("chưa có service nào chạy mà đã giao")
	}
	s.run(t, nil)
	eventually(t, "giao nốt sau khi chạy lại", func() bool { return rcv.count() == 2 })
	time.Sleep(time.Second)
	if rcv.count() != 2 {
		t.Errorf("thông báo thứ nhất không được giao lại: %d", rcv.count())
	}
}

func TestRefusesBadConfigurationAndUnmigratedSchema(t *testing.T) {
	img := image(t)
	t.Run("group trùng fhir-server", func(t *testing.T) {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{"ENVIRONMENT": "local", "DATABASE_URL": "sqlite://:memory:", "BROKER_GROUP": "trivita"}}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, "BROKER_GROUP") {
			t.Fatalf("muốn thoát khác 0 và nêu BROKER_GROUP: %d %s", code, logs)
		}
	})
	t.Run("websocket embedded", func(t *testing.T) {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{"ENVIRONMENT": "local", "DATABASE_URL": "sqlite://:memory:", "FHIR_WS_MODE": "embedded"}}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, "FHIR_WS_MODE=embedded") {
			t.Fatalf("muốn thoát khác 0 và nêu FHIR_WS_MODE=embedded: %d %s", code, logs)
		}
	})
	t.Run("schema chưa migrate", func(t *testing.T) {
		testkit.RequireDocker(t)
		pg := testkit.Postgres(t)
		dsn, _ := pg.NewSchema(t) // schema trống: chưa chạy `fhir-server migrate up`
		rds := testkit.StartRedis(t)
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{
			"ENVIRONMENT": "local", "DATABASE_URL": fromContainer(dsn), "REDIS_URL": fromContainer(rds.URL),
		}}, 60*time.Second)
		if code == 0 || !strings.Contains(logs, "schema chưa được migrate") {
			t.Fatalf("muốn từ chối vì schema chưa migrate: %d %s", code, logs)
		}
	})
}
