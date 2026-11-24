package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"trivita-fhir/apps/fhir-worker/internal/bootstrap"
	"trivita-fhir/apps/fhir-worker/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

// api mô phỏng PHÍA fhir-server: cùng cơ sở dữ liệu, một Router thật với DisableJobExecution=true (nhận yêu cầu, tạo AsyncJob accepted, KHÔNG chạy). fhir-worker chỉ chung với nó
// schema và chữ ký quyền của job (job-seal), không import mã của nhau (C-1).
type api struct {
	t      *testing.T
	store  *fhirstore.Store
	router *fhirrouter.Router
	srv    *httptest.Server
	jwt    bool
}

type env struct {
	t        *testing.T
	dbURL    string
	jobKey   []byte
	api      *api
	settings config.Settings
	vars     map[string]string
}

// newEnv dựng database SQLite (tệp, đã migrate như `fhir-server migrate up`), API mô phỏng và cấu hình hợp lệ cho fhir-worker. jwt=true: job mang chữ ký quyền của người gọi.
func newEnv(t *testing.T, jwt bool, mut ...func(map[string]string)) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, dbURL: "sqlite://" + filepath.Join(dir, "fhir.db"), jobKey: []byte(strings.Repeat("j", 40))}
	e.vars = map[string]string{
		"DATABASE_URL": e.dbURL, "FHIR_BASE_URL": "http://fhir.test/fhir/R4", "SHUTDOWN_DRAIN_DELAY": "0s",
		"FHIR_JOB_MAINTENANCE_INTERVAL": "100ms", "FHIR_JOB_RECOVERY_GRACE": "1ms",
	}
	if jwt {
		e.vars["FHIR_JWT_ISSUER"], e.vars["FHIR_JOB_SIGNING_KEY"] = "https://id.test", string(e.jobKey)
	}
	e.api = newAPI(t, e.dbURL, jwt, e.jobKey)
	for _, m := range mut {
		m(e.vars)
	}
	s, err := config.FromEnv(func(k string) string { return e.vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	e.settings = s
	return e
}

func newAPI(t *testing.T, dbURL string, jwt bool, key []byte) *api {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: dbURL})
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
	var routerRepo fhirrouter.Repository = st
	opts := fhirrouter.Options{JobRepository: st, DisableJobExecution: true, BaseURL: "http://fhir.test/fhir/R4", AsyncBatch: true, JobLease: time.Minute}
	if jwt {
		sealer, err := fhirauth.NewSealer(key)
		if err != nil {
			t.Fatal(err)
		}
		routerRepo = fhirauth.Scope(st)
		opts.ScopedJobRepository = fhirauth.Scope(st)
		opts.JobSeal = func(ctx context.Context, id, req string) (string, error) {
			p, _ := fhirauth.PrincipalFrom(ctx)
			return sealer.Seal(id, req, p), nil
		}
	}
	r := fhirrouter.New(routerRepo, opts)
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	h := http.StripPrefix("/fhir/R4", r.HTTPHandler())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p := req.Header.Get("X-Project"); jwt && p != "" {
			req = req.WithContext(fhirauth.WithPrincipal(req.Context(), &fhirauth.Principal{Subject: "u-" + p, Project: p, Role: fhirauth.RoleAdmin}))
		}
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	return &api{t: t, store: st, router: r, srv: srv, jwt: jwt}
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (a *api) do(method, path, project string, body any, hdr ...string) resp {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.srv.URL+"/fhir/R4/"+strings.TrimPrefix(path, "/"), rd)
	req.Header.Set("Content-Type", "application/fhir+json")
	if project != "" {
		req.Header.Set("X-Project", project)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}

func (a *api) patients(project string, n int) {
	a.t.Helper()
	for i := 0; i < n; i++ {
		if r := a.do("POST", "Patient", project, map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "P" + project}}}); r.status != 201 {
			a.t.Fatalf("tạo Patient: %d %s", r.status, r.body)
		}
	}
}

// startExport gọi $export (Prefer: respond-async) và trả đường dẫn trạng thái.
func (a *api) startExport(project string) string {
	a.t.Helper()
	r := a.do("GET", "Patient/$export", project, nil, "Prefer", "respond-async")
	if r.status != 202 {
		a.t.Fatalf("$export: %d %s", r.status, r.body)
	}
	loc := r.header.Get("Content-Location")
	return strings.TrimPrefix(loc, "http://fhir.test/fhir/R4/")
}

// exportOutput chờ job xong và trả danh sách file kết quả (số dòng, id Binary).
func (a *api) waitExport(path, project string) (counts []int, binaries []string) {
	a.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r := a.do("GET", path, project, nil)
		switch r.status {
		case 202:
			time.Sleep(50 * time.Millisecond)
			continue
		case 200:
			var out struct {
				Output []struct {
					URL   string `json:"url"`
					Count int    `json:"count"`
				} `json:"output"`
			}
			if err := json.Unmarshal(r.body, &out); err != nil {
				a.t.Fatal(err)
			}
			for _, o := range out.Output {
				counts = append(counts, o.Count)
				binaries = append(binaries, o.URL[strings.LastIndex(o.URL, "/")+1:])
			}
			return
		default:
			a.t.Fatalf("trạng thái export: %d %s", r.status, r.body)
		}
	}
	a.t.Fatal("export chưa xong sau 20 giây")
	return
}

func start(t *testing.T, s config.Settings) *bootstrap.Container {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

func sum(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

func TestRunsJobsThatTheAPICreatedButDoesNotRun(t *testing.T) {
	e := newEnv(t, false)
	e.api.patients("", 3)
	path := e.api.startExport("")
	time.Sleep(600 * time.Millisecond) // API không có bộ chạy job (DisableJobExecution) và chưa có worker: job đứng ở accepted
	if r := e.api.do("GET", path, "", nil); r.status != 202 {
		t.Fatalf("chưa có worker thì job phải còn accepted (202), được %d %s", r.status, r.body)
	}
	start(t, e.settings)
	counts, _ := e.api.waitExport(path, "")
	if sum(counts) != 3 {
		t.Errorf("export phải có 3 Patient, được %v", counts)
	}
}

// Job chạy bằng đúng quyền và project đã ký lúc tạo: export của project A không chứa dữ liệu của project B (kho worker dựng lại Principal từ chữ ký, rồi tách project).
func TestJobsRunWithTheCallersProjectOnly(t *testing.T) {
	e := newEnv(t, true)
	e.api.patients("A", 2)
	e.api.patients("B", 3)
	pa, pb := e.api.startExport("A"), e.api.startExport("B")
	start(t, e.settings)
	ca, _ := e.api.waitExport(pa, "A")
	cb, _ := e.api.waitExport(pb, "B")
	if sum(ca) != 2 || sum(cb) != 3 {
		t.Errorf("project A có 2 Patient, B có 3; export được %v và %v", ca, cb)
	}
}

// Job có chữ ký không hợp lệ (ký bằng khóa khác, hoặc bị đổi) không chạy: bị đánh dấu lỗi, không để lại kết quả.
func TestJobWithAnInvalidSealIsRejected(t *testing.T) {
	e := newEnv(t, true, func(v map[string]string) { v["FHIR_JOB_SIGNING_KEY"] = strings.Repeat("x", 40) }) // worker giữ khóa KHÁC khóa của API
	e.api.patients("A", 2)
	path := e.api.startExport("A")
	start(t, e.settings)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if r := e.api.do("GET", path, "A", nil); r.status == 500 {
			if n := binaryCount(t, e.api.store); n != 0 {
				t.Fatalf("job bị từ chối mà vẫn để lại %d Binary", n)
			}
			return
		} else if r.status != 202 {
			t.Fatalf("job chữ ký sai phải lỗi (500), được %d %s", r.status, r.body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job chữ ký sai không được đánh dấu lỗi")
}

func binaryCount(t *testing.T, st *fhirstore.Store) int {
	t.Helper()
	sr, err := fhirsearch.Parse("Binary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sr.Count = 100
	res, err := st.Search(context.Background(), sr)
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Matches)
}

// Hai worker cùng nhận một job: lease trong CSDL bảo đảm chỉ một bên chạy (không có hai bộ file kết quả).
func TestTwoWorkersRunEachJobOnce(t *testing.T) {
	e := newEnv(t, false)
	e.api.patients("", 5)
	path := e.api.startExport("")
	start(t, e.settings)
	start(t, e.settings)
	counts, _ := e.api.waitExport(path, "")
	time.Sleep(800 * time.Millisecond)
	if sum(counts) != 5 || binaryCount(t, e.api.store) != len(counts) {
		t.Errorf("muốn đúng một bộ kết quả (5 Patient); counts=%v, %d Binary", counts, binaryCount(t, e.api.store))
	}
}

// Có FHIR_BINARY_STORAGE: file kết quả ra kho đối tượng (không base64 trong CSDL); hết hạn thì Expunge xóa cả dòng lẫn tệp.
func TestResultsGoToObjectStorageAndAreExpungedWhenExpired(t *testing.T) {
	blobs := filepath.Join(t.TempDir(), "blobs")
	e := newEnv(t, false, func(v map[string]string) {
		v["FHIR_BINARY_STORAGE"], v["FHIR_EXPORT_RETENTION"] = "file:"+blobs, "2s"
		v["FHIR_BASE64_BINARY_MAX_BYTES"] = "512" // trần của entry batch không áp cho file kết quả của job
	})
	e.api.patients("", 40)
	path := e.api.startExport("")
	start(t, e.settings)
	_, ids := e.api.waitExport(path, "")
	if countFiles(blobs) == 0 {
		t.Fatal("file export phải nằm ở kho đối tượng")
	}
	res, err := e.api.store.ReadResource(context.Background(), "Binary", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if b := res.(*fhir.Binary); b.Data != "" {
		t.Fatalf("Binary trong CSDL không được giữ base64: %d ký tự", len(b.Data))
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && countFiles(blobs) != 0 {
		time.Sleep(100 * time.Millisecond)
	}
	if n := countFiles(blobs); n != 0 {
		t.Fatalf("kết quả hết hạn phải bị xóa khỏi kho, còn %d tệp", n)
	}
	if _, err := e.api.store.ReadResource(context.Background(), "Binary", ids[0]); err == nil {
		t.Error("dòng Binary hết hạn phải bị Expunge")
	}
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// Batch async do worker chạy ghi resource thật và PHÁT sự kiện lên Redis (cùng điều kiện với fhir-server: Subscription hoặc tự tải), để fhir-notifier và fhir-downloader thấy.
func TestBatchAsyncWritesResourcesAndPublishesEvents(t *testing.T) {
	mr := miniredis.RunT(t)
	e := newEnv(t, false, func(v map[string]string) {
		v["FHIR_SUBSCRIPTIONS"], v["BROKER_DRIVER"], v["REDIS_URL"] = "true", "redis", "redis://"+mr.Addr()
	})
	var patients atomic.Int32
	b, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{Group: "listener", Block: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	b.Subscribe(fhirsub.EventType, func(_ context.Context, env messaging.Envelope) error {
		var ev fhirsub.ChangeEvent
		_ = env.Decode(&ev)
		if ev.ResourceType == "Patient" && ev.Interaction == "create" {
			patients.Add(1)
		}
		return nil
	})
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	start(t, e.settings)

	bundle := map[string]any{"resourceType": "Bundle", "type": "batch", "entry": []any{
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "B1"}}}},
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "B2"}}}},
	}}
	r := e.api.do("POST", "", "", bundle, "Prefer", "respond-async")
	if r.status != 202 {
		t.Fatalf("batch async: %d %s", r.status, r.body)
	}
	path := strings.TrimPrefix(r.header.Get("Content-Location"), "http://fhir.test/fhir/R4/")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st := e.api.do("GET", path, "", nil); st.status == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && patients.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if patients.Load() != 2 {
		t.Fatalf("muốn 2 sự kiện create Patient do batch ghi, được %d", patients.Load())
	}
	sr, _ := fhirsearch.Parse("Patient", nil)
	if res, err := e.api.store.Search(context.Background(), sr); err != nil || len(res.Matches) != 2 {
		t.Errorf("batch phải ghi 2 Patient: %v %v", res, err)
	}
}

func TestRefusesToStartOnUnmigratedSchema(t *testing.T) {
	e := newEnv(t, false, func(v map[string]string) { v["DATABASE_URL"] = "sqlite://" + filepath.Join(t.TempDir(), "rong.db") })
	_, err := bootstrap.New(context.Background(), e.settings)
	if err == nil || !strings.Contains(err.Error(), "schema chưa được migrate") {
		t.Fatalf("muốn từ chối vì schema chưa migrate, được %v", err)
	}
}

func TestReadinessReflectsDatabaseAndDraining(t *testing.T) {
	e := newEnv(t, false)
	c := start(t, e.settings)
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	ready := func() (int, map[string]any) {
		r, err := http.Get(srv.URL + "/health/ready")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	if code, m := ready(); code != 200 || m["ready"] != true || m["checks"].(map[string]any)["database"] != true {
		t.Fatalf("khi mọi phụ thuộc sống: %d %v", code, m)
	}
	c.BeginShutdown()
	if code, m := ready(); code != 503 || m["checks"].(map[string]any)["draining"] != true {
		t.Errorf("đang tắt: %d %v", code, m)
	}
	if r, err := http.Get(srv.URL + "/healthz"); err != nil || r.StatusCode != 200 {
		t.Errorf("liveness khi đang tắt: %v %v", r, err)
	}
}

func TestServeShutsDownGracefullyOnSignal(t *testing.T) {
	e := newEnv(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan net.Addr, 1)
	done := make(chan error, 1)
	s := e.settings
	s.Addr = "127.0.0.1:0"
	go func() {
		done <- bootstrap.Serve(ctx, s, bootstrap.ServeOptions{OnListen: func(a net.Addr) { addrCh <- a }})
	}()
	var addr net.Addr
	select {
	case addr = <-addrCh:
	case err := <-done:
		t.Fatalf("Serve thoát sớm: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("không nghe cổng")
	}
	if r, err := http.Get("http://" + addr.String() + "/health/ready"); err != nil || r.StatusCode != 200 {
		t.Fatalf("ready trước khi tắt: %v %v", r, err)
	}
	cancel() // như SIGTERM
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tắt êm phải thoát không lỗi (mã 0), được %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve không thoát sau khi nhận tín hiệu")
	}
}

// FHIR_JOB_CANCEL_POLL tới được router của worker: job API tạo và worker chạy dừng trong chưa tới 1 giây sau khi bị hủy ở nơi khác, dù lease 10 phút (nhịp gia hạn 200 giây).
func TestJobCancelledByTheAPIStopsQuicklyOnTheWorker(t *testing.T) {
	e := newEnv(t, false, func(v map[string]string) { v["FHIR_JOB_LEASE"], v["FHIR_JOB_CANCEL_POLL"] = "10m", "30ms" })
	c := start(t, e.settings)
	started, stopped := make(chan struct{}), make(chan struct{})
	c.Router.RegisterJob("longcancel", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	e.api.router.RegisterJob("longcancel", func(context.Context, fhirrouter.Repository, *fhirrouter.Input) (*fhir.Parameters, error) {
		return nil, nil // API không chạy job (DisableJobExecution); chỉ cần loại job tồn tại để tạo AsyncJob
	})
	job, err := e.api.router.StartJob(context.Background(), "longcancel", map[string][]string{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("worker không nhận job")
	}
	cur, err := e.api.store.ReadResource(context.Background(), "AsyncJob", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	cj := cur.(*fhir.AsyncJob)
	cj.Status = "cancelled"
	if _, err := e.api.store.UpdateResource(context.Background(), cj, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("job không dừng trong 1 giây sau khi bị hủy (FHIR_JOB_CANCEL_POLL không tới router của worker?)")
	}
}
