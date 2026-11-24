package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"trivita-fhir/apps/fhir-downloader/internal/bootstrap"
	"trivita-fhir/apps/fhir-downloader/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

// api mô phỏng PHÍA fhir-server: cùng cơ sở dữ liệu, ghi resource qua kho phát sự kiện lên broker Redis (group mặc định "trivita"), không giao hay tải gì.
// fhir-downloader chỉ biết hai thứ chung với fhir-server: schema và sự kiện trên Redis (hợp đồng dây C-6: fhirsub.EventType, ChangeEvent).
type api struct {
	t      *testing.T
	store  *fhirstore.Store
	repo   fhirrouter.Repository
	broker messaging.Broker
}

type env struct {
	t        *testing.T
	redis    *miniredis.Miniredis
	dbURL    string
	blobDir  string
	api      *api
	settings config.Settings
}

// newEnv dựng database SQLite (tệp, đã migrate như `fhir-server migrate up`), miniredis và cấu hình hợp lệ cho fhir-downloader (cho phép địa chỉ nội bộ vì máy chủ thử ở 127.0.0.1).
func newEnv(t *testing.T, mut ...func(map[string]string)) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, redis: miniredis.RunT(t), dbURL: "sqlite://" + filepath.Join(dir, "fhir.db"), blobDir: filepath.Join(dir, "blobs")}
	e.api = newAPI(t, e.dbURL, "redis://"+e.redis.Addr())
	vars := map[string]string{
		"DATABASE_URL": e.dbURL, "REDIS_URL": "redis://" + e.redis.Addr(), "FHIR_BINARY_STORAGE": "file:" + e.blobDir,
		"FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE": "true", "SHUTDOWN_DRAIN_DELAY": "0s", "FHIR_BASE_URL": "http://fhir.test/fhir/R4",
	}
	for _, m := range mut {
		m(vars)
	}
	s, err := config.FromEnv(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	e.settings = s
	return e
}

func newAPI(t *testing.T, dbURL, redisURL string) *api {
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
	br, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: redisURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Stop(context.Background()) })
	return &api{t: t, store: st, repo: fhirsub.Wrap(st, br), broker: br}
}

func (a *api) createDoc(project string, urls ...string) *fhir.DocumentReference {
	a.t.Helper()
	d := &fhir.DocumentReference{ID: "doc-" + project + "-" + time.Now().Format("150405.000000"), Status: "current", Meta: &fhir.Meta{Project: project}}
	for _, u := range urls {
		d.Content = append(d.Content, fhir.DocumentReferenceContent{Attachment: &fhir.Attachment{URL: u}})
	}
	if _, err := a.repo.CreateResource(context.Background(), d); err != nil {
		a.t.Fatal(err)
	}
	return d
}

func (a *api) read(id string) *fhir.DocumentReference {
	a.t.Helper()
	r, err := a.store.ReadResource(context.Background(), "DocumentReference", id)
	if err != nil {
		a.t.Fatal(err)
	}
	return r.(*fhir.DocumentReference)
}

func (a *api) binaries() []*fhir.Binary {
	a.t.Helper()
	sr, err := fhirrouterSearch(a.store, "Binary")
	if err != nil {
		a.t.Fatal(err)
	}
	var out []*fhir.Binary
	for _, m := range sr {
		out = append(out, m.(*fhir.Binary))
	}
	return out
}

// externalSite là "máy chủ ngoài" chứa tệp; hits đếm số lần bị gọi.
func externalSite(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
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

// waitURL chờ attachment đầu tiên của resource đổi thành Binary/<id>, trả id Binary.
func waitBinaryURL(t *testing.T, a *api, docID string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if u := a.read(docID).Content[0].Attachment.URL; strings.HasPrefix(u, "Binary/") {
			return strings.TrimPrefix(u, "Binary/")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("attachment chưa thành Binary/<id>: %q", a.read(docID).Content[0].Attachment.URL)
	return ""
}

func TestDownloadsAttachmentAnnouncedByTheAPI(t *testing.T) {
	pdf := bytes.Repeat([]byte("%PDF nội dung tài liệu "), 300)
	site, hits := externalSite(t, pdf)
	e := newEnv(t)
	start(t, e.settings)

	d := e.api.createDoc("projA", site.URL+"/tai-lieu.pdf?token=bimat")
	binID := waitBinaryURL(t, e.api, d.ID)

	bins := e.api.binaries()
	if len(bins) != 1 || bins[0].ID != binID {
		t.Fatalf("muốn đúng 1 Binary %s, có %d", binID, len(bins))
	}
	b := bins[0]
	if b.Meta == nil || b.Meta.Project != "projA" || b.SecurityContext == nil || b.SecurityContext.Reference != "DocumentReference/"+d.ID || b.ContentType != "application/pdf" {
		t.Errorf("Binary phải thuộc project của resource nguồn và trỏ securityContext về nó: %+v", b)
	}
	if hits.Load() != 1 {
		t.Errorf("máy chủ ngoài bị gọi %d lần, muốn 1", hits.Load())
	}
	if got := e.api.read(d.ID).Content[0].Attachment; got.ContentType != "application/pdf" {
		t.Errorf("contentType attachment: %q", got.ContentType)
	}
	if n := blobCount(t, e.blobDir); n != 1 {
		t.Errorf("kho có %d tệp, muốn 1", n)
	}
}

// Sự kiện nằm trong stream Redis khi service chưa chạy; khởi động sau vẫn xử lý (service chết hoặc chưa deploy không làm mất việc).
func TestProcessesEventsPublishedWhileTheServiceWasDown(t *testing.T) {
	site, hits := externalSite(t, []byte("%PDF một"))
	e := newEnv(t)
	d := e.api.createDoc("projA", site.URL+"/a.pdf")
	time.Sleep(200 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatal("chưa có service nào chạy mà đã tải")
	}
	start(t, e.settings)
	waitBinaryURL(t, e.api, d.ID)
	if hits.Load() != 1 {
		t.Errorf("máy chủ ngoài bị gọi %d lần, muốn 1", hits.Load())
	}
}

// Cả fhir-server (group "trivita") và fhir-downloader (group riêng) phải nhận MỌI sự kiện: cùng group thì chia nhau và việc tải có thể rơi vào bên không tải.
func TestEachConsumerGroupReceivesEveryEvent(t *testing.T) {
	site, _ := externalSite(t, []byte("%PDF hai"))
	e := newEnv(t)
	var gotByServer atomic.Int32
	other, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: "redis://" + e.redis.Addr()}) // group mặc định "trivita" của fhir-server
	if err != nil {
		t.Fatal(err)
	}
	other.Subscribe(fhirsub.EventType, func(context.Context, messaging.Envelope) error { gotByServer.Add(1); return nil })
	if err := other.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Stop(context.Background()) })
	start(t, e.settings)

	var ids []string
	for i := 0; i < 5; i++ {
		d := e.api.createDoc("projA", site.URL+"/f"+string(rune('a'+i))+".pdf")
		ids = append(ids, d.ID)
	}
	for _, id := range ids {
		waitBinaryURL(t, e.api, id)
	}
	// 5 lần tạo, cộng 5 lần cập nhật do chính downloader gây ra (đổi url thành Binary/<id>) cũng được phát lên: server nhận cả 10, downloader không tải thêm
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && gotByServer.Load() < 10 {
		time.Sleep(20 * time.Millisecond)
	}
	if gotByServer.Load() < 10 {
		t.Errorf("group của fhir-server nhận %d sự kiện, muốn >= 10 (5 tạo + 5 cập nhật attachment)", gotByServer.Load())
	}
	if n := len(e.api.binaries()); n != 5 {
		t.Errorf("muốn 5 Binary (không tải lặp), có %d", n)
	}
}

func TestBlocksInternalAddressesByDefault(t *testing.T) {
	site, hits := externalSite(t, []byte("bí mật nội bộ"))
	e := newEnv(t, func(v map[string]string) { v["FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE"] = "false" }) // 127.0.0.1 là địa chỉ nội bộ
	start(t, e.settings)
	d := e.api.createDoc("projA", site.URL+"/noi-bo.pdf")
	time.Sleep(600 * time.Millisecond)
	if hits.Load() != 0 || len(e.api.binaries()) != 0 || !strings.HasPrefix(e.api.read(d.ID).Content[0].Attachment.URL, site.URL) {
		t.Fatalf("địa chỉ nội bộ phải bị chặn: gọi %d lần, %d Binary, url=%q", hits.Load(), len(e.api.binaries()), e.api.read(d.ID).Content[0].Attachment.URL)
	}
}

func TestSkipsOwnAndIgnoredURLs(t *testing.T) {
	site, hits := externalSite(t, []byte("x"))
	// địa chỉ API và /storage của hệ thống nằm trên CHÍNH máy chủ thử (tải được nếu bị coi là URL ngoài), nên hits phản ánh việc có tải hay không
	e := newEnv(t, func(v map[string]string) {
		v["FHIR_BASE_URL"] = site.URL + "/fhir/R4"
		v["FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES"] = site.URL + "/bo-qua/"
	})
	start(t, e.settings)
	d := e.api.createDoc("projA", site.URL+"/fhir/R4/Binary/da-co", site.URL+"/storage/binary/x/1", site.URL+"/bo-qua/a.pdf")
	time.Sleep(600 * time.Millisecond)
	if hits.Load() != 0 || len(e.api.binaries()) != 0 {
		t.Fatalf("URL của chính hệ thống hoặc bị bỏ qua không được tải: gọi %d lần, %d Binary", hits.Load(), len(e.api.binaries()))
	}
	if got := e.api.read(d.ID); got.Content[0].Attachment.URL != site.URL+"/fhir/R4/Binary/da-co" {
		t.Errorf("resource bị đổi: %+v", got.Content[0].Attachment)
	}
}

func TestRefusesToStartOnUnmigratedSchema(t *testing.T) {
	e := newEnv(t, func(v map[string]string) { v["DATABASE_URL"] = "sqlite://" + filepath.Join(t.TempDir(), "rong.db") })
	_, err := bootstrap.New(context.Background(), e.settings)
	if err == nil || !strings.Contains(err.Error(), "schema chưa được migrate") {
		t.Fatalf("muốn từ chối vì schema chưa migrate, được %v", err)
	}
}

func TestReadinessReflectsDependenciesAndDraining(t *testing.T) {
	e := newEnv(t)
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
	if code, m := ready(); code != 200 || m["ready"] != true || m["checks"].(map[string]any)["database"] != true || m["checks"].(map[string]any)["broker"] != true {
		t.Fatalf("khi mọi phụ thuộc sống: %d %v", code, m)
	}
	for _, p := range []string{"/healthz", "/health/live", "/health/startup"} {
		if r, err := http.Get(srv.URL + p); err != nil || r.StatusCode != 200 {
			t.Errorf("%s: %v %v", p, r, err)
		}
	}
	e.redis.Close() // Redis chết: không sẵn sàng nhưng liveness vẫn 200 (K8s không restart pod vì lỗi của Redis)
	if code, m := ready(); code != 503 || m["checks"].(map[string]any)["broker"] != false {
		t.Errorf("Redis chết: %d %v", code, m)
	}
	if r, err := http.Get(srv.URL + "/healthz"); err != nil || r.StatusCode != 200 {
		t.Errorf("liveness phụ thuộc Redis: %v %v", r, err)
	}
}

func TestServeShutsDownGracefullyOnSignal(t *testing.T) {
	e := newEnv(t)
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
