package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhir"
)

const (
	writeSecret = "write-secret-0123456789abcdef"
	readSecret  = "read-secret-0123456789abcdef"
)

func settings(mutate ...func(*config.Settings)) config.Settings {
	s := config.Default()
	s.DatabaseURL = testDatabaseURL()
	s.WriteTokens = "ops:" + writeSecret
	s.ReadTokens = "viewer:" + readSecret
	s.BaseURL = "http://fhir.test/fhir/R4"
	s.ShutdownDrainDelay = 0
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func newServer(t *testing.T, s config.Settings) (*bootstrap.Container, *httptest.Server) {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e)
	t.Cleanup(func() { srv.Close(); _ = c.Stop(context.Background()) })
	return c, srv
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("body không phải JSON (%d): %s", r.status, r.body)
	}
	return m
}

func (r resp) str(t *testing.T, path ...string) string {
	t.Helper()
	var cur any = r.json(t)
	for _, k := range path {
		switch c := cur.(type) {
		case map[string]any:
			cur = c[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(c) {
				return ""
			}
			cur = c[i]
		default:
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

func call(t *testing.T, srv *httptest.Server, method, path, token string, body any) resp {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/fhir+json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, raw}
}

func want(t *testing.T, r resp, status int) resp {
	t.Helper()
	if r.status != status {
		t.Fatalf("muốn %d, được %d: %s", status, r.status, r.body)
	}
	return r
}

func patient(family string) map[string]any {
	return map[string]any{"resourceType": "Patient", "gender": "male", "name": []any{map[string]any{"family": family}}}
}

func TestHealthAndPublicMetadata(t *testing.T) {
	_, srv := newServer(t, settings())
	want(t, call(t, srv, "GET", "/healthz", "", nil), 200)
	r := want(t, call(t, srv, "GET", "/readyz", "", nil), 200)
	if r.json(t)["ready"] != true {
		t.Errorf("readyz: %s", r.body)
	}
	m := want(t, call(t, srv, "GET", "/fhir/R4/metadata", "", nil), 200)
	if m.str(t, "resourceType") != "CapabilityStatement" {
		t.Errorf("metadata: %.120s", m.body)
	}
}

func TestAuthentication(t *testing.T) {
	_, srv := newServer(t, settings())
	for _, tok := range []string{"", "wrong"} {
		r := want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok, nil), 401)
		if r.header.Get("WWW-Authenticate") == "" || r.str(t, "resourceType") != "OperationOutcome" || r.str(t, "issue", "0", "code") != "login" {
			t.Errorf("401 phải là OperationOutcome kèm WWW-Authenticate: %v %s", r.header, r.body)
		}
		if !strings.HasPrefix(r.header.Get("Content-Type"), "application/fhir+json") {
			t.Errorf("content-type: %s", r.header.Get("Content-Type"))
		}
	}
	// token chỉ đọc: được đọc và tìm kiếm (kể cả POST _search), không được ghi
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", readSecret, nil), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient/_search", readSecret, nil), 200)
	f := want(t, call(t, srv, "POST", "/fhir/R4/Patient", readSecret, patient("X")), 403)
	if f.str(t, "issue", "0", "code") != "forbidden" {
		t.Errorf("403: %s", f.body)
	}
	want(t, call(t, srv, "POST", "/fhir/R4", readSecret, map[string]any{"resourceType": "Bundle", "type": "batch"}), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/x", readSecret, nil), 403)
}

func TestFHIRFlowOverHTTP(t *testing.T) {
	_, srv := newServer(t, settings())
	c := want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Smith")), 201)
	id, vid := c.str(t, "id"), c.str(t, "meta", "versionId")
	if loc := c.header.Get("Location"); loc != "http://fhir.test/fhir/R4/Patient/"+id+"/_history/"+vid {
		t.Errorf("Location dùng FHIR_BASE_URL: %s", loc)
	}
	// đọc bằng token chỉ đọc
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, readSecret, nil), 200)
	s := want(t, call(t, srv, "GET", "/fhir/R4/Patient?family=smith&_total=accurate", readSecret, nil), 200)
	if s.json(t)["total"] != float64(1) || !strings.HasPrefix(s.str(t, "entry", "0", "fullUrl"), "http://fhir.test/fhir/R4/Patient/") {
		t.Errorf("search: %s", s.body)
	}
	u := patient("Jones")
	u["id"] = id
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, writeSecret, u), 200)
	h := want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id+"/_history", readSecret, nil), 200)
	if h.json(t)["total"] != float64(2) {
		t.Errorf("history: %s", h.body)
	}
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, writeSecret, nil), 204)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, readSecret, nil), 410)

	// dữ liệu sai bị từ chối bằng OperationOutcome kèm vị trí lỗi
	bad := want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, map[string]any{"resourceType": "Patient", "active": "yes"}), 400)
	if bad.str(t, "issue", "0", "expression", "0") != "Patient.active" {
		t.Errorf("validation: %s", bad.body)
	}
	// transaction nguyên tử qua HTTP: entry sau lỗi thì entry trước không được lưu
	tx := map[string]any{"resourceType": "Bundle", "type": "transaction", "entry": []any{
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": patient("Ghost")},
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": map[string]any{"resourceType": "Patient", "bogus": 1}},
	}}
	want(t, call(t, srv, "POST", "/fhir/R4", writeSecret, tx), 400)
	g := want(t, call(t, srv, "GET", "/fhir/R4/Patient?family=ghost&_total=accurate", readSecret, nil), 200)
	if g.json(t)["total"] != float64(0) {
		t.Errorf("rollback: %s", g.body)
	}
}

func TestRoutingErrors(t *testing.T) {
	_, srv := newServer(t, settings())
	r := want(t, call(t, srv, "GET", "/fhir/R4/NotAResource/1", writeSecret, nil), 404)
	if r.str(t, "resourceType") != "OperationOutcome" {
		t.Errorf("404 trong FHIR phải là OperationOutcome: %s", r.body)
	}
	o := want(t, call(t, srv, "GET", "/not-fhir", "", nil), 404)
	if o.json(t)["error"] == nil {
		t.Errorf("404 ngoài FHIR là JSON thường: %s", o.body)
	}
	// body quá lớn bị chặn ở tầng HTTP
	big := `{"resourceType":"Patient","text":{"status":"generated","div":"` + strings.Repeat("a", 17<<20) + `"}}`
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, big), 413)
}

func TestAuthDisabledForDev(t *testing.T) {
	_, srv := newServer(t, settings(func(s *config.Settings) { s.AuthDisabled = true }))
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", "", patient("Dev")), 201)
}

func TestReadinessReflectsDraining(t *testing.T) {
	c, srv := newServer(t, settings())
	want(t, call(t, srv, "GET", "/readyz", "", nil), 200)
	c.BeginShutdown()
	r := want(t, call(t, srv, "GET", "/readyz", "", nil), 503)
	if r.json(t)["ready"] != false {
		t.Errorf("readyz: %s", r.body)
	}
	want(t, call(t, srv, "GET", "/healthz", "", nil), 200) // liveness không đổi
}

// Dữ liệu còn nguyên sau khi tắt và khởi động lại (file SQLite), và migrate bằng CLI hoạt động.
func TestPersistenceAndMigrateCLI(t *testing.T) {
	file := "sqlite://" + filepath.Join(t.TempDir(), "fhir.db")
	s := settings(func(s *config.Settings) { s.DatabaseURL = file; s.DatabaseAutoMigrate = false })
	ctx := context.Background()

	// chưa migrate: từ chối khởi động (nhiều replica không được chạy trên schema cũ)
	if _, err := bootstrap.New(ctx, s); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("muốn từ chối vì chưa migrate, được %v", err)
	}
	var out bytes.Buffer
	if err := bootstrap.Migrate(ctx, s, "status", &out); err != nil || !strings.Contains(out.String(), "1") {
		t.Fatalf("status trước: %q %v", out.String(), err)
	}
	if err := bootstrap.Migrate(ctx, s, "up", &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := bootstrap.Migrate(ctx, s, "status", &out); err != nil || !strings.Contains(out.String(), "0") {
		t.Fatalf("status sau: %q %v", out.String(), err)
	}
	if err := bootstrap.Migrate(ctx, s, "bogus", &out); err == nil {
		t.Fatal("lệnh sai phải lỗi")
	}

	c1, srv1 := newServer(t, s)
	id := want(t, call(t, srv1, "POST", "/fhir/R4/Patient", writeSecret, patient("Persist")), 201).str(t, "id")
	srv1.Close()
	_ = c1.Stop(ctx)

	_, srv2 := newServer(t, s)
	want(t, call(t, srv2, "GET", "/fhir/R4/Patient/"+id, readSecret, nil), 200)

	// rollback một bước đưa schema về trạng thái chưa migrate
	if err := bootstrap.Migrate(ctx, s, "down", &out); err != nil {
		t.Fatal(err)
	}
}

// Serve chạy thật trên cổng ngẫu nhiên; hủy ctx (giả lập SIGTERM) -> readyz 503 trong lúc drain -> thoát sạch.
func TestServeGracefulShutdown(t *testing.T) {
	s := settings(func(s *config.Settings) {
		s.Addr = "127.0.0.1:0"
		s.ShutdownDrainDelay = 300 * time.Millisecond
	})
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
	if get("/readyz") != 200 || get("/fhir/R4/metadata") != 200 {
		t.Fatal("chưa sẵn sàng")
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	if get("/readyz") != 503 {
		t.Error("trong lúc drain readyz phải 503")
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

func TestServeRefusesWithoutTokens(t *testing.T) {
	s := settings(func(s *config.Settings) { s.WriteTokens, s.ReadTokens = "", "" })
	if err := bootstrap.Serve(context.Background(), s, bootstrap.ServeOptions{}); err == nil || !strings.Contains(err.Error(), "FHIR_WRITE_TOKENS") {
		t.Fatalf("muốn từ chối khi thiếu token: %v", err)
	}
}

// ---------------------------------------------------------------- giai đoạn 6: operation, job, subscription

const adminSecret = "admin-secret-0123456789abcdef"

func withAdmin(s *config.Settings) { s.AdminTokens = "root:" + adminSecret }

func TestOperationsAndAdminRole(t *testing.T) {
	_, srv := newServer(t, settings(withAdmin))
	p := want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Ops")), 201)
	id := p.str(t, "id")
	want(t, call(t, srv, "POST", "/fhir/R4/Observation", writeSecret, map[string]any{"resourceType": "Observation", "status": "final",
		"code": map[string]any{"text": "c"}, "subject": map[string]any{"reference": "Patient/" + id}}), 201)

	// $validate và $everything chỉ cần quyền đọc
	want(t, call(t, srv, "POST", "/fhir/R4/Patient/$validate", readSecret, patient("Valid")), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient/$validate", readSecret, map[string]any{"resourceType": "Patient", "bogus": 1}), 400)
	ev := want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id+"/$everything", readSecret, nil), 200)
	if ev.json(t)["total"] != float64(2) {
		t.Errorf("everything: %s", ev.body)
	}

	// quản trị: token ghi và token đọc đều bị từ chối, chỉ admin được
	for _, tok := range []string{readSecret, writeSecret} {
		want(t, call(t, srv, "POST", "/fhir/R4/Patient/$reindex", tok, ""), 403)
		want(t, call(t, srv, "POST", "/fhir/R4/Patient/"+id+"/$expunge", tok, ""), 403)
	}
	acc := want(t, call(t, srv, "POST", "/fhir/R4/Patient/$reindex", adminSecret, ""), 202)
	loc := acc.header.Get("Content-Location")
	if !strings.HasPrefix(loc, "http://fhir.test/fhir/R4/job/") {
		t.Fatalf("Content-Location: %q", loc)
	}
	path := strings.TrimPrefix(loc, "http://fhir.test")
	var st resp
	for i := 0; i < 200; i++ {
		if st = call(t, srv, "GET", path, readSecret, nil); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.status != 200 || st.str(t, "status") != "completed" {
		t.Fatalf("job: %d %s", st.status, st.body)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Patient/"+id+"/$expunge", adminSecret, ""), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, readSecret, nil), 404)
}

type recorder struct {
	mu    sync.Mutex
	calls []map[string]any
	hdr   []http.Header
}

func newReceiver(t *testing.T) (*httptest.Server, *recorder) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.calls, rec.hdr = append(rec.calls, body), append(rec.hdr, r.Header.Clone())
		rec.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func (r *recorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

func TestSubscriptionsEndToEnd(t *testing.T) {
	hook, rec := newReceiver(t)
	c, srv := newServer(t, settings(func(s *config.Settings) {
		s.Subscriptions, s.WebhookAllowPrivate = true, true // máy nhận test nằm ở 127.0.0.1
	}))
	if c.Manager == nil || c.Broker == nil {
		t.Fatal("Subscription bật nhưng chưa dựng broker/manager")
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "e2e", "criteria": "Patient?family=Hooked",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL, "header": []any{"X-Tenant: acme"}},
	}), 201)

	created := want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Hooked")), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Ignored")), 201)
	for i := 0; i < 300 && rec.count() < 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.count() != 1 {
		t.Fatalf("muốn đúng 1 thông báo, được %d", rec.count())
	}
	rec.mu.Lock()
	body, hdr := rec.calls[0], rec.hdr[0]
	rec.mu.Unlock()
	if body["id"] != created.str(t, "id") || hdr.Get("X-Medplum-Interaction") != "create" || hdr.Get("X-Tenant") != "acme" {
		t.Errorf("thông báo: %v %v", body, hdr)
	}
	// readyz có thêm check broker
	r := want(t, call(t, srv, "GET", "/readyz", "", nil), 200)
	if r.json(t)["checks"].(map[string]any)["broker"] != true {
		t.Errorf("readyz: %s", r.body)
	}
}

func TestSubscriptionsOffByDefault(t *testing.T) {
	hook, rec := newReceiver(t)
	c, srv := newServer(t, settings())
	if c.Manager != nil || c.Broker != nil {
		t.Fatal("mặc định không được dựng subscription")
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "x", "criteria": "Patient",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL}}), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("X")), 201)
	time.Sleep(150 * time.Millisecond)
	if rec.count() != 0 {
		t.Errorf("không được giao khi FHIR_SUBSCRIPTIONS=false: %d", rec.count())
	}
}

// Subscription chặn địa chỉ nội bộ mặc định: không giao được tới 127.0.0.1 khi không bật AllowPrivate.
func TestWebhookSSRFGuardOnByDefault(t *testing.T) {
	hook, rec := newReceiver(t)
	c, srv := newServer(t, settings(func(s *config.Settings) { s.Subscriptions = true }))
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "x", "criteria": "Patient",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL}}), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Blocked")), 201)
	for i := 0; i < 300 && c.Manager.Stats().Failed < 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Manager.Stats().Failed != 1 || rec.count() != 0 {
		t.Errorf("stats=%+v nhận=%d", c.Manager.Stats(), rec.count())
	}
}

// Tắt êm khi bật Subscription: job và worker giao được dừng đúng thứ tự, không treo.
func TestServeGracefulShutdownWithSubscriptions(t *testing.T) {
	s := settings(withAdmin, func(s *config.Settings) {
		s.Addr, s.Subscriptions, s.ShutdownDrainDelay = "127.0.0.1:0", true, 50*time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- bootstrap.Serve(ctx, s, bootstrap.ServeOptions{OnListen: func(a net.Addr) { addr <- a }})
	}()
	a := <-addr
	res, err := http.Get("http://" + a.String() + "/readyz")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("readyz: %v %v", res, err)
	}
	res.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tắt êm phải không lỗi: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve không thoát")
	}
}

// Job mồ côi (process cũ chết giữa chừng) được vòng bảo trì của process mới chạy tiếp.
func TestOrphanedExportResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	mut := func(s *config.Settings) {
		s.DatabaseURL = "sqlite://" + filepath.Join(dir, "fhir.db")
		s.DatabaseAutoMigrate = true
		s.JobMaintenanceInterval = 20 * time.Millisecond
	}
	c1, srv1 := newServer(t, settings(mut, func(s *config.Settings) { s.JobMaintenanceInterval = 0 }))
	want(t, call(t, srv1, "POST", "/fhir/R4/Patient", writeSecret, patient("Persist")), 201)
	// mô phỏng job export mà process cũ chưa kịp chạy: ghi thẳng AsyncJob accepted rồi tắt process
	job := map[string]any{"resourceType": "AsyncJob", "id": "orphan-export", "status": "accepted", "request": "export?_level=system&_type=Patient",
		"requestTime": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	raw, _ := json.Marshal(job)
	res, err := fhirUnmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c1.Store.CreateResource(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	_ = c1.Stop(context.Background())

	_, srv2 := newServer(t, settings(mut))
	var st resp
	for i := 0; i < 300; i++ {
		if st = call(t, srv2, "GET", "/fhir/R4/bulkdata/orphan-export/status", readSecret, nil); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.status != 200 {
		t.Fatalf("job mồ côi không được chạy tiếp: %d %s", st.status, st.body)
	}
	if out := st.json(t)["output"].([]any); len(out) != 1 || out[0].(map[string]any)["count"] != float64(1) {
		t.Errorf("output: %s", st.body)
	}
	want(t, call(t, srv2, "DELETE", "/fhir/R4/bulkdata/orphan-export/status", readSecret, nil), 202) // quyền đọc đủ để dọn
}

func fhirUnmarshal(raw []byte) (fhir.Resource, error) { return fhir.UnmarshalResource(raw) }

func TestAssignProjectCommand(t *testing.T) {
	dir := t.TempDir()
	s := settings(func(s *config.Settings) {
		s.DatabaseURL = "sqlite://" + dir + "/fhir.db"
		s.DatabaseAutoMigrate = false
	})
	ctx := context.Background()
	var out bytes.Buffer
	if err := bootstrap.AssignProject(ctx, s, "A", nil, false, &out); err == nil {
		t.Fatal("schema chưa migrate phải lỗi")
	}
	if err := bootstrap.Migrate(ctx, s, "up", &out); err != nil {
		t.Fatal(err)
	}
	// dữ liệu cũ ghi qua server không bật JWT (không có project)
	c, err := bootstrap.New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.CreateResource(ctx, &fhir.Patient{ResourceType: "Patient", ID: "legacy-1"}); err != nil {
		t.Fatal(err)
	}
	_ = c.Stop(ctx)

	out.Reset()
	if err := bootstrap.AssignProject(ctx, s, "A", nil, true, &out); err != nil || !strings.Contains(out.String(), "dry-run") || !strings.Contains(out.String(), "Patient: 1") {
		t.Fatalf("dry-run: %v %q", err, out.String())
	}
	out.Reset()
	if err := bootstrap.AssignProject(ctx, s, "A", []string{"Patient"}, false, &out); err != nil || !strings.Contains(out.String(), "Patient: 1") {
		t.Fatalf("gán: %v %q", err, out.String())
	}
	out.Reset()
	if err := bootstrap.AssignProject(ctx, s, "A", nil, false, &out); err != nil || !strings.Contains(out.String(), "cho 0 resource") {
		t.Fatalf("idempotent: %v %q", err, out.String())
	}
	if err := bootstrap.AssignProject(ctx, s, "", nil, false, &out); err == nil {
		t.Fatal("thiếu project phải lỗi")
	}
}
