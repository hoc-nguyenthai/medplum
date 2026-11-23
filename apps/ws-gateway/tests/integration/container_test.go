package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/apps/ws-gateway/internal/bootstrap"
	"trivita-fhir/apps/ws-gateway/internal/config"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/testkit"
)

var signingKey = strings.Repeat("k", 40)

// rds là một Redis THẬT (testcontainers) riêng cho mỗi test; Stop/Start mô phỏng Redis chết rồi sống lại.
type rds struct{ ri *testkit.RedisInstance }

func (r *rds) Addr() string { return r.ri.Addr }

func newRedisT(t *testing.T) *rds { return &rds{ri: testkit.StartRedis(t)} }

// fhirSide là phía fhir-server ở "tiến trình khác": kho, Manager khớp tiêu chí (AttachRemote) và bộ cấp token. Hai phía chỉ chung
// Redis và khóa token.
type fhirSide struct {
	t      *testing.T
	mem    *fhirrouter.MemoryRepository
	repo   *fhirsub.Repo
	broker *messaging.Redis
	issuer *fhirsub.Tokens
}

func newFhirSide(t *testing.T, mr *rds, key string) *fhirSide {
	t.Helper()
	ctx := context.Background()
	mem := fhirrouter.NewMemoryRepository()
	broker, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{Block: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	mgr := fhirsub.NewManager(mem, fhirsub.Config{AllowPrivate: true, BaseDelay: 5 * time.Millisecond, Timeout: 2 * time.Second, CacheTTL: time.Hour})
	mgr.Register(broker)
	if err := mgr.AttachRemote(broker, broker); err != nil {
		t.Fatal(err)
	}
	if err := broker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	mgr.Start()
	t.Cleanup(func() { _ = mgr.Stop(ctx); _ = broker.Stop(ctx) })
	issuer, err := fhirsub.NewTokens(fhirsub.TokenConfig{SigningKey: []byte(key)})
	if err != nil {
		t.Fatal(err)
	}
	return &fhirSide{t: t, mem: mem, repo: fhirsub.Wrap(mem, broker), broker: broker, issuer: issuer}
}

func (f *fhirSide) create(js string) {
	f.t.Helper()
	r, err := fhir.UnmarshalResource([]byte(js))
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.repo.CreateResource(context.Background(), r); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fhirSide) subscription(id, criteria string) {
	f.create(`{"resourceType":"Subscription","id":"` + id + `","status":"active","reason":"t","criteria":"` + criteria + `","channel":{"type":"websocket"}}`)
}

func obs(id string) string {
	return `{"resourceType":"Observation","id":"` + id + `","status":"final","code":{"text":"x"}}`
}

func (f *fhirSide) token(id string, p *fhirauth.Principal) string {
	return f.tokenWith(f.issuer, id, p)
}

func (f *fhirSide) tokenWith(tk *fhirsub.Tokens, id string, p *fhirauth.Principal) string {
	f.t.Helper()
	ctx := context.Background()
	if p != nil {
		ctx = fhirauth.WithPrincipal(ctx, p)
	}
	res, err := tk.TokenOperation(f.mem)(ctx, &fhirrouter.Request{Params: map[string]string{"id": id}})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, q := range res.Resource.(*fhir.Parameters).Parameter {
		if q.Name == "token" {
			return q.ValueString
		}
	}
	f.t.Fatal("không có token")
	return ""
}

func settingsFor(mr *rds, mut func(*config.Settings)) config.Settings {
	s := config.Default()
	s.RedisURL = "redis://" + mr.Addr()
	s.WSSigningKey = signingKey
	s.WSHeartbeat = time.Hour
	s.ShutdownDrainDelay = 0
	s.ReadinessTimeout = 300 * time.Millisecond
	if mut != nil {
		mut(&s)
	}
	return s
}

type gateway struct {
	c   *bootstrap.Container
	srv *httptest.Server
}

func newGateway(t *testing.T, s config.Settings) *gateway {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &gateway{c: c, srv: httptest.NewServer(e)}
	t.Cleanup(func() {
		g.srv.CloseClientConnections()
		g.srv.Close()
		_ = c.Stop(context.Background())
	})
	return g
}

func (g *gateway) wsURL() string {
	return "ws" + strings.TrimPrefix(g.srv.URL, "http") + "/ws/subscriptions-r4"
}

func bindMsg(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]any{"token": token}})
	return b
}

func dialWith(t *testing.T, url string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if c != nil {
		t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

// bound bind bằng token rồi trả kết nối đã qua handshake; fatal nếu bị đóng.
func (g *gateway) bound(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	c, _, err := dialWith(t, g.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, bindMsg(token)); err != nil {
		t.Fatal(err)
	}
	if _, data, err := c.Read(ctx); err != nil || !strings.Contains(string(data), `"handshake"`) {
		t.Fatalf("handshake: %s %v", data, err)
	}
	return c
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hết hạn chờ: %s", what)
}

func readMsg(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("đọc: %v", err)
	}
	return string(data)
}

func closeStatus(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("kết nối phải đóng")
	}
	return websocket.CloseStatus(err)
}

func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

// Đường chính qua gateway THẬT: fhir-server khớp và phát, gateway giao.
func TestGatewayDeliversEventsMatchedByFhirServer(t *testing.T) {
	mr := newRedisT(t)
	f := newFhirSide(t, mr, signingKey)
	g := newGateway(t, settingsFor(mr, nil))
	f.subscription("s1", "Observation?status=final")
	c := g.bound(t, f.token("s1", &fhirauth.Principal{Subject: "alice"}))

	f.create(obs("o1"))
	if m := readMsg(t, c); !strings.Contains(m, "event-notification") || !strings.Contains(m, "Observation/o1") {
		t.Fatalf("event: %s", m)
	}
	// quyền chụp trong token vẫn được áp ở gateway
	g.bound(t, f.token("s1", &fhirauth.Principal{Subject: "bob", Grants: fhirauth.Grants{"Patient": fhirauth.PermRead}}))
	f.create(obs("o2"))
	if m := readMsg(t, c); !strings.Contains(m, "Observation/o2") {
		t.Fatalf("alice: %s", m)
	}
	if st := g.c.Hub.Stats(); st.Denied != 1 {
		t.Errorf("Denied = %d", st.Denied)
	}
}

func TestGatewayRejectsTokensFromAnotherKeyAndHonorsRotation(t *testing.T) {
	mr := newRedisT(t)
	oldKey, newKey := strings.Repeat("o", 40), strings.Repeat("n", 40)
	f := newFhirSide(t, mr, oldKey) // bên cấp còn khóa cũ
	g := newGateway(t, settingsFor(mr, func(s *config.Settings) { s.WSSigningKey = newKey; s.WSSigningKeyPrevious = oldKey }))
	f.subscription("s1", "Observation")
	g.bound(t, f.token("s1", nil)) // khóa cũ vẫn được nhận trong lúc xoay

	stranger, _ := fhirsub.NewTokens(fhirsub.TokenConfig{SigningKey: []byte(strings.Repeat("z", 40))})
	c, _, err := dialWith(t, g.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(f.tokenWith(stranger, "s1", nil)))
	if code := closeStatus(t, c); code != websocket.StatusPolicyViolation {
		t.Errorf("khóa lạ: mã đóng %v", code)
	}
}

func TestGatewayReadiness(t *testing.T) {
	mr := newRedisT(t)
	g := newGateway(t, settingsFor(mr, nil))
	code, body := httpGet(t, g.srv.URL+"/health/ready")
	if code != 200 || !strings.Contains(body, `"redis":true`) {
		t.Fatalf("ready: %d %s", code, body)
	}
	mr.ri.Stop(t) // Redis chết
	if code, body := httpGet(t, g.srv.URL+"/health/ready"); code != http.StatusServiceUnavailable || !strings.Contains(body, `"redis":false`) {
		t.Errorf("Redis chết phải 503: %d %s", code, body)
	}
	if code, _ := httpGet(t, g.srv.URL+"/health/live"); code != 200 {
		t.Errorf("liveness không phụ thuộc Redis: %d", code)
	}
}

func TestGatewayDrainingReportsNotReady(t *testing.T) {
	mr := newRedisT(t)
	g := newGateway(t, settingsFor(mr, nil))
	g.c.BeginShutdown()
	code, body := httpGet(t, g.srv.URL+"/health/ready")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, `"accepting_traffic":false`) {
		t.Errorf("đang tắt phải 503: %d %s", code, body)
	}
}

// Redis chưa lên lúc khởi động không làm tiến trình thoát (readiness 503); cấu hình sai thì thoát ngay.
func TestGatewayStartsWithoutRedisButRefusesBadConfig(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0") // cổng chắc chắn không có Redis
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	s := config.Default()
	s.RedisURL = "redis://" + addr
	s.WSSigningKey = signingKey
	s.ReadinessTimeout = 200 * time.Millisecond
	g := newGateway(t, s)
	if code, _ := httpGet(t, g.srv.URL+"/health/ready"); code != http.StatusServiceUnavailable {
		t.Errorf("không có Redis: %d, muốn 503", code)
	}
	if code, _ := httpGet(t, g.srv.URL+"/health/live"); code != 200 {
		t.Errorf("liveness: %d", code)
	}
	// bind khi Redis chết: 1013 (thử lại), không phải 1008
	f := &fhirSide{t: t, mem: fhirrouter.NewMemoryRepository()}
	f.issuer, _ = fhirsub.NewTokens(fhirsub.TokenConfig{SigningKey: []byte(signingKey)})
	f.repo = fhirsub.Wrap(f.mem, messaging.NewInMemory())
	f.subscription("s1", "Observation")
	c, _, err := dialWith(t, g.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(f.token("s1", nil)))
	if code := closeStatus(t, c); code != websocket.StatusTryAgainLater {
		t.Errorf("bind khi Redis chết: mã đóng %v, muốn 1013", code)
	}

	bad := config.Default()
	bad.RedisURL = "không phải url redis"
	bad.WSSigningKey = signingKey
	if _, err := bootstrap.New(context.Background(), bad); err == nil {
		t.Error("REDIS_URL hỏng phải lỗi ngay")
	}
	short := config.Default()
	short.WSSigningKey = "ngắn"
	if _, err := bootstrap.New(context.Background(), short); err == nil {
		t.Error("khóa ngắn phải lỗi ngay")
	}
}

// Tắt êm: mọi socket nhận 1001 và sổ hiện diện được gỡ ngay (bên phát thôi gửi, không chờ hết hạn).
func TestGatewayStopClosesSocketsAndReleasesPresence(t *testing.T) {
	mr := newRedisT(t)
	f := newFhirSide(t, mr, signingKey)
	g := newGateway(t, settingsFor(mr, nil))
	f.subscription("s1", "Observation")
	c := g.bound(t, f.token("s1", nil))
	present := func() bool {
		ok, err := f.broker.Present(context.Background(), fhirsub.WSPresenceKey("s1"))
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !present() {
		t.Fatal("đã bind thì sổ hiện diện phải có")
	}
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.c.Stop(ctx)
		close(done)
	}()
	if code := closeStatus(t, c); code != websocket.StatusGoingAway {
		t.Errorf("mã đóng = %v, muốn 1001", code)
	}
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Stop treo")
	}
	if present() {
		t.Error("sau Stop sổ hiện diện phải được gỡ")
	}
}

// Origin chỉ nhận mẫu host trong danh sách; client không phải trình duyệt (không có Origin) vẫn vào được.
func TestGatewayOriginPolicy(t *testing.T) {
	mr := newRedisT(t)
	g := newGateway(t, settingsFor(mr, func(s *config.Settings) { s.WSAllowedOrigins = "app.example.org" }))
	for name, c := range map[string]struct {
		origin string
		ok     bool
	}{"không có Origin": {"", true}, "được phép": {"https://app.example.org", true}, "lạ": {"https://evil.example.com", false}} {
		hdr := http.Header{}
		if c.origin != "" {
			hdr.Set("Origin", c.origin)
		}
		_, resp, err := dialWith(t, g.wsURL(), hdr)
		if c.ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !c.ok && (err == nil || resp == nil || resp.StatusCode != http.StatusForbidden) {
			t.Errorf("%s: muốn 403, được %v %v", name, resp, err)
		}
	}
}

// Giới hạn theo IP: không khai proxy tin cậy thì X-Forwarded-For giả không né được; khai thì mỗi IP thật một bộ đếm.
func TestGatewayPerIPLimitAndTrustedProxies(t *testing.T) {
	mr := newRedisT(t)
	xff := func(ip string) http.Header { return http.Header{"X-Forwarded-For": {ip}} }

	untrusted := newGateway(t, settingsFor(mr, func(s *config.Settings) { s.WSMaxConnsPerIP = 1 }))
	if _, _, err := dialWith(t, untrusted.wsURL(), xff("1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := dialWith(t, untrusted.wsURL(), xff("2.2.2.2")); err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("XFF giả né được hạn mức: %v %v", resp, err)
	}

	trusted := newGateway(t, settingsFor(mr, func(s *config.Settings) { s.WSMaxConnsPerIP = 1; s.TrustedProxies = "127.0.0.0/8" }))
	if _, _, err := dialWith(t, trusted.wsURL(), xff("1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dialWith(t, trusted.wsURL(), xff("2.2.2.2")); err != nil {
		t.Errorf("IP thật khác nhau phải có bộ đếm riêng: %v", err)
	}
	if _, resp, err := dialWith(t, trusted.wsURL(), xff("1.1.1.1")); err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("cùng IP thật lần hai phải 429: %v %v", resp, err)
	}
}

func TestGatewayMaxConnectionsAnswers503(t *testing.T) {
	mr := newRedisT(t)
	g := newGateway(t, settingsFor(mr, func(s *config.Settings) { s.WSMaxConnections = 1 }))
	if _, _, err := dialWith(t, g.wsURL(), nil); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := dialWith(t, g.wsURL(), nil); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("vượt số kết nối: %v %v", resp, err)
	}
}

// Serve thật trên cổng thật: phục vụ websocket và health, tắt êm khi ctx bị hủy (socket nhận 1001, Serve trả nil).
func TestServeEndToEnd(t *testing.T) {
	mr := newRedisT(t)
	f := newFhirSide(t, mr, signingKey)
	f.subscription("s1", "Observation")
	s := settingsFor(mr, func(s *config.Settings) {
		s.Addr = "127.0.0.1:0"
		s.ShutdownTimeout = 5 * time.Second
		s.WSCloseTimeout = time.Second
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan net.Addr, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- bootstrap.Serve(ctx, s, bootstrap.ServeOptions{OnListen: func(a net.Addr) { addrCh <- a }})
	}()
	var addr net.Addr
	select {
	case addr = <-addrCh:
	case err := <-errCh:
		t.Fatalf("Serve thoát sớm: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("không nghe cổng")
	}
	if code, _ := httpGet(t, "http://"+addr.String()+"/health/ready"); code != 200 {
		t.Fatalf("ready: %d", code)
	}
	c, _, err := dialWith(t, "ws://"+addr.String()+"/ws/subscriptions-r4", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, c2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer c2()
	_ = c.Write(ctx2, websocket.MessageText, bindMsg(f.token("s1", nil)))
	if _, data, err := c.Read(ctx2); err != nil || !strings.Contains(string(data), "handshake") {
		t.Fatalf("handshake: %s %v", data, err)
	}
	f.create(obs("o1"))
	if _, data, err := c.Read(ctx2); err != nil || !strings.Contains(string(data), "Observation/o1") {
		t.Fatalf("event: %s %v", data, err)
	}
	cancel() // SIGTERM
	if code := closeStatus(t, c); code != websocket.StatusGoingAway {
		t.Errorf("mã đóng = %v, muốn 1001", code)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Serve trả lỗi khi tắt êm: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Serve không thoát")
	}
}
