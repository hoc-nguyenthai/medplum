package bootstrap_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"trivita-fhir/apps/fhir-notifier/internal/bootstrap"
	"trivita-fhir/apps/fhir-notifier/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

// api mô phỏng PHÍA fhir-server: cùng cơ sở dữ liệu, ghi resource qua kho phát sự kiện lên broker Redis (group mặc định "trivita"), không giao gì.
// fhir-notifier chỉ biết hai thứ chung với fhir-server: schema và sự kiện trên Redis (hợp đồng dây C-6: fhirsub.EventType, ChangeEvent).
type api struct {
	t      *testing.T
	store  *fhirstore.Store
	repo   fhirrouter.Repository
	broker *messaging.Redis
}

type env struct {
	t        *testing.T
	redis    *miniredis.Miniredis
	dbURL    string
	api      *api
	settings config.Settings
}

// newEnv dựng database SQLite (tệp, đã migrate như `fhir-server migrate up`), miniredis và cấu hình hợp lệ cho fhir-notifier (cho phép địa chỉ nội bộ vì máy nhận thử ở 127.0.0.1).
func newEnv(t *testing.T, mut ...func(map[string]string)) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, redis: miniredis.RunT(t), dbURL: "sqlite://" + filepath.Join(dir, "fhir.db")}
	e.api = newAPI(t, e.dbURL, "redis://"+e.redis.Addr())
	vars := map[string]string{
		"DATABASE_URL": e.dbURL, "REDIS_URL": "redis://" + e.redis.Addr(), "FHIR_WEBHOOK_ALLOW_PRIVATE": "true", "SHUTDOWN_DRAIN_DELAY": "0s",
		"FHIR_WEBHOOK_TIMEOUT": "2s",
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
	br, err := messaging.NewRedis(redisURL, messaging.RedisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Stop(context.Background()) })
	return &api{t: t, store: st, repo: fhirsub.Wrap(st, br), broker: br}
}

func (a *api) create(js string) fhir.Resource {
	a.t.Helper()
	r, err := fhir.UnmarshalResource([]byte(js))
	if err != nil {
		a.t.Fatal(err)
	}
	out, err := a.repo.CreateResource(context.Background(), r)
	if err != nil {
		a.t.Fatal(err)
	}
	return out
}

func (a *api) subscribe(id, project, criteria, endpoint string) {
	a.t.Helper()
	meta := ""
	if project != "" {
		meta = `,"meta":{"project":"` + project + `"}`
	}
	a.create(`{"resourceType":"Subscription","id":"` + id + `"` + meta + `,"status":"active","reason":"test","criteria":"` + criteria +
		`","channel":{"type":"rest-hook","endpoint":"` + endpoint + `","header":["X-Tenant: acme"]}}`)
}

func (a *api) patient(project, family string) {
	a.t.Helper()
	meta := ""
	if project != "" {
		meta = `"meta":{"project":"` + project + `"},`
	}
	a.create(`{"resourceType":"Patient",` + meta + `"name":[{"family":"` + family + `"}]}`)
}

func (a *api) subscriptionStatus(id string) (string, string) {
	a.t.Helper()
	r, err := a.store.ReadResource(context.Background(), "Subscription", id)
	if err != nil {
		a.t.Fatal(err)
	}
	s := r.(*fhir.Subscription)
	return s.Status, s.Error
}

// receiver là máy nhận webhook; script là các mã trả lời lần lượt (hết thì 200); block (nếu có) giữ phản hồi tới khi đóng.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	script []int
	calls  []map[string]any
	hdr    []http.Header
	block  chan struct{}
}

func newReceiver(t *testing.T, script ...int) *receiver {
	r := &receiver{script: script}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		r.mu.Lock()
		r.calls, r.hdr = append(r.calls, body), append(r.hdr, req.Header.Clone())
		n, block := len(r.calls), r.block
		r.mu.Unlock()
		if block != nil {
			<-block
		}
		status := 200
		if n <= len(r.script) {
			status = r.script[n-1]
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

func start(t *testing.T, s config.Settings) *bootstrap.Container {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("quá hạn chờ: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func never(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	time.Sleep(d)
	if cond() {
		t.Fatalf("không được xảy ra: %s", what)
	}
}

// settle chờ Subscription vừa tạo được nạp vào bộ nhớ đệm của notifier (sự kiện Subscription làm mới danh sách).
func settle() { time.Sleep(400 * time.Millisecond) }

func TestDeliversWebhookForMatchingSubscriptionAnnouncedByTheAPI(t *testing.T) {
	e := newEnv(t)
	start(t, e.settings)
	hook := newReceiver(t)
	e.api.subscribe("s1", "", "Patient?family=Hooked", hook.srv.URL)
	settle()

	e.api.patient("", "Hooked")
	e.api.patient("", "Ignored")
	eventually(t, "một thông báo", func() bool { return hook.count() == 1 })
	never(t, "giao cho resource không khớp", 600*time.Millisecond, func() bool { return hook.count() != 1 })
	hook.mu.Lock()
	body, hdr := hook.calls[0], hook.hdr[0]
	hook.mu.Unlock()
	if body["resourceType"] != "Patient" || hdr.Get("X-Medplum-Subscription") != "s1" || hdr.Get("X-Medplum-Interaction") != "create" || hdr.Get("X-Tenant") != "acme" {
		t.Errorf("thông báo: %v %v", body, hdr)
	}
}

func TestSubscriptionOnlyReceivesEventsOfItsOwnProject(t *testing.T) {
	e := newEnv(t)
	start(t, e.settings)
	hook := newReceiver(t)
	e.api.subscribe("s1", "projA", "Patient?family=Hooked", hook.srv.URL)
	settle()

	e.api.patient("projB", "Hooked") // cùng criteria nhưng khác tenant
	never(t, "rò sự kiện sang project khác", 800*time.Millisecond, func() bool { return hook.count() != 0 })
	e.api.patient("projA", "Hooked")
	eventually(t, "sự kiện cùng project được giao", func() bool { return hook.count() == 1 })
}

func TestRetriesInPlaceOnTemporaryFailureThenStops(t *testing.T) {
	e := newEnv(t, func(v map[string]string) { v["FHIR_WEBHOOK_MAX_ATTEMPTS"] = "4" })
	c := start(t, e.settings)
	ok := newReceiver(t, 503, 503) // hai lần đầu 503, lần ba 200
	bad := newReceiver(t, 500, 500, 500, 500, 500, 500)
	e.api.subscribe("a-ok", "", "Patient?family=One", ok.srv.URL)
	e.api.subscribe("b-bad", "", "Patient?family=Two", bad.srv.URL)
	settle()

	e.api.patient("", "One")
	eventually(t, "thử lại tại chỗ rồi giao được", func() bool { return ok.count() == 3 })
	e.api.patient("", "Two")
	eventually(t, "bỏ cuộc sau MaxAttempts", func() bool { return c.Manager.Stats().Failed == 1 })
	never(t, "thử quá MaxAttempts hoặc giao lại sau khi đã ACK", 1500*time.Millisecond, func() bool { return bad.count() != 4 || ok.count() != 3 })
}

// Cả fhir-server (group "trivita") và fhir-notifier (group riêng) phải nhận MỌI sự kiện: cùng group thì chia nhau và việc giao có thể rơi vào bên không giao.
func TestEachConsumerGroupReceivesEveryEvent(t *testing.T) {
	e := newEnv(t)
	var gotByServer atomic.Int32
	other, err := messaging.NewRedis("redis://"+e.redis.Addr(), messaging.RedisOptions{Block: 20 * time.Millisecond}) // group mặc định "trivita" của fhir-server
	if err != nil {
		t.Fatal(err)
	}
	other.Subscribe(fhirsub.EventType, func(context.Context, messaging.Envelope) error { gotByServer.Add(1); return nil })
	if err := other.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Stop(context.Background()) })
	start(t, e.settings)
	hook := newReceiver(t)
	e.api.subscribe("s1", "", "Patient?family=Hooked", hook.srv.URL)
	settle()

	for i := 0; i < 6; i++ {
		e.api.patient("", "Hooked")
	}
	eventually(t, "notifier giao đủ 6", func() bool { return hook.count() == 6 })
	eventually(t, "group của fhir-server nhận đủ 7 sự kiện (1 Subscription + 6 Patient)", func() bool { return gotByServer.Load() == 7 })
}

// Sự kiện nằm trong stream Redis khi service chưa chạy; khởi động sau vẫn giao (service chết hoặc chưa deploy không làm mất việc).
func TestDeliversEventsPublishedWhileTheServiceWasDown(t *testing.T) {
	e := newEnv(t)
	hook := newReceiver(t)
	e.api.subscribe("s1", "", "Patient?family=Hooked", hook.srv.URL)
	e.api.patient("", "Hooked")
	never(t, "giao khi chưa có service nào chạy", 300*time.Millisecond, func() bool { return hook.count() != 0 })
	start(t, e.settings)
	eventually(t, "giao sau khi service chạy", func() bool { return hook.count() == 1 })
}

// Tiến trình giao chết giữa lúc giao: message không được ACK nên replica khác (cùng group) nhận lại sau BROKER_CLAIM_IDLE và giao (ít nhất một lần).
func TestAnotherReplicaDeliversWhatTheStoppedOneLeftUnfinished(t *testing.T) {
	e := newEnv(t, func(v map[string]string) {
		v["FHIR_WEBHOOK_MAX_ATTEMPTS"], v["FHIR_WEBHOOK_TIMEOUT"], v["BROKER_CLAIM_IDLE"] = "1", "1s", "2s"
	})
	hook := newReceiver(t)
	hook.block = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(hook.block) }) }
	t.Cleanup(release)
	a, err := bootstrap.New(context.Background(), e.settings)
	if err != nil {
		t.Fatal(err)
	}
	e.api.subscribe("s1", "", "Patient?family=Hooked", hook.srv.URL)
	settle()
	e.api.patient("", "Hooked")
	eventually(t, "A đã gọi webhook (đang treo)", func() bool { return hook.count() == 1 })
	stopped := make(chan struct{})
	go func() { _ = a.Stop(context.Background()); close(stopped) }() // "chết" giữa lúc giao
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("A không dừng được dù webhook còn treo")
	}
	if a.Manager.Stats().Delivered != 0 {
		t.Fatal("A chưa giao xong")
	}
	release()
	b := start(t, e.settings)
	eventually(t, "B nhận lại message chưa ACK và giao", func() bool { return b.Manager.Stats().Delivered >= 1 })
	if hook.count() < 2 {
		t.Errorf("muốn ít nhất 2 lần gọi (A dở dang, B giao lại), được %d", hook.count())
	}
}

func TestAutoDisablesSubscriptionAfterRepeatedFailuresSharedOnRedis(t *testing.T) {
	e := newEnv(t, func(v map[string]string) {
		v["FHIR_WEBHOOK_MAX_ATTEMPTS"] = "1"
		v["FHIR_SUBSCRIPTION_AUTO_DISABLE"] = `[{"maxConsecutiveFailures":2,"timeWindowSeconds":60}]`
	})
	start(t, e.settings)
	bad := newReceiver(t, 500, 500, 500, 500)
	e.api.subscribe("s1", "", "Patient?family=Hooked", bad.srv.URL)
	settle()
	e.api.patient("", "Hooked")
	eventually(t, "lần giao thất bại đầu", func() bool { return bad.count() == 1 })
	if st, _ := e.api.subscriptionStatus("s1"); st != "active" {
		t.Fatalf("mới 1 thất bại: status=%s", st)
	}
	e.api.patient("", "Hooked")
	eventually(t, "tự tắt sau 2 thất bại", func() bool { st, _ := e.api.subscriptionStatus("s1"); return st == "off" })
	if _, msg := e.api.subscriptionStatus("s1"); !strings.Contains(msg, "Automatically disabled after 2 consecutive failed events") {
		t.Errorf("thông báo lỗi: %q", msg)
	}
}

func TestWebsocketIssuerPublishesToChannelOnlyWhenAGatewayListens(t *testing.T) {
	e := newEnv(t, func(v map[string]string) { v["FHIR_WS_MODE"] = "issuer" })
	c := start(t, e.settings)
	e.api.create(`{"resourceType":"Subscription","id":"ws1","status":"active","reason":"test","criteria":"Patient?family=Hooked","channel":{"type":"websocket"}}`)
	settle()

	var got atomic.Int32
	var mu sync.Mutex
	var msgs [][]byte
	cancel, err := e.api.broker.SubscribeChannel(context.Background(), fhirsub.WSChannel("ws1"), func(p []byte) { got.Add(1); mu.Lock(); msgs = append(msgs, p); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)

	e.api.patient("", "Hooked") // chưa có gateway nào ghi sổ hiện diện: không phát
	eventually(t, "khớp nhưng không ai nghe", func() bool { return c.Manager.Stats().Unheard == 1 })
	never(t, "phát khi không có gateway", 300*time.Millisecond, func() bool { return got.Load() != 0 })

	if err := e.api.broker.Mark(context.Background(), fhirsub.WSPresenceKey("ws1"), "gw1", time.Minute); err != nil { // gateway ghi sổ hiện diện
		t.Fatal(err)
	}
	e.api.patient("", "Hooked")
	eventually(t, "phát lên kênh khi có gateway", func() bool { return got.Load() == 1 })
	mu.Lock()
	var m fhirsub.WSMessage
	_ = json.Unmarshal(msgs[0], &m)
	mu.Unlock()
	if m.Sub != "ws1" || m.Event.ResourceType != "Patient" {
		t.Errorf("thông điệp kênh: %+v", m)
	}
}

func TestWebsocketSubscriptionsAreIgnoredWhenModeIsOff(t *testing.T) {
	e := newEnv(t)
	c := start(t, e.settings)
	e.api.create(`{"resourceType":"Subscription","id":"ws1","status":"active","reason":"test","criteria":"Patient?family=Hooked","channel":{"type":"websocket"}}`)
	settle()
	e.api.patient("", "Hooked")
	never(t, "xử lý Subscription websocket khi FHIR_WS_MODE tắt", 800*time.Millisecond, func() bool {
		st := c.Manager.Stats()
		return st.Published != 0 || st.Unheard != 0
	})
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
	c.BeginShutdown()
	if code, m := ready(); code != 503 || m["checks"].(map[string]any)["draining"] != true {
		t.Errorf("đang tắt: %d %v", code, m)
	}
	if r, err := http.Get(srv.URL + "/healthz"); err != nil || r.StatusCode != 200 {
		t.Errorf("liveness khi đang tắt: %v %v", r, err)
	}
}

func TestRedisOutageMakesItNotReadyButStillAlive(t *testing.T) {
	e := newEnv(t)
	c := start(t, e.settings)
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	e.redis.Close()
	get := func(p string) int {
		r, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if get("/health/ready") != 503 {
		t.Error("Redis chết: readiness phải 503")
	}
	if get("/healthz") != 200 || get("/health/live") != 200 {
		t.Error("liveness không được phụ thuộc Redis (K8s không restart pod vì lỗi của Redis)")
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

// Webhook chậm không chặn các sự kiện khác: FHIR_WEBHOOK_WORKERS sự kiện được xử lý song song (messaging Concurrency); nếu chỉ một thì sự kiện sau chờ sau webhook treo.
func TestSlowWebhookDoesNotBlockOtherEvents(t *testing.T) {
	e := newEnv(t)
	start(t, e.settings)
	slow, fast := newReceiver(t), newReceiver(t)
	slow.block = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(slow.block) }) }
	t.Cleanup(release) // test lỗi giữa chừng cũng không để máy nhận treo (srv.Close chờ handler)
	e.api.subscribe("a-slow", "", "Patient?family=Slow", slow.srv.URL)
	e.api.subscribe("b-fast", "", "Patient?family=Fast", fast.srv.URL)
	settle()

	e.api.patient("", "Slow")
	eventually(t, "webhook chậm đã bị gọi (đang treo)", func() bool { return slow.count() == 1 })
	e.api.patient("", "Fast")
	eventually(t, "sự kiện sau được giao dù webhook trước còn treo", func() bool { return fast.count() == 1 })
	release()
}
