package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	goredis "github.com/redis/go-redis/v9"
)

// Websocket của Subscription tách riêng, ba tiến trình thật + một Redis:
//
//	auth ──JWT/JWKS──> fhir-server (FHIR_WS_MODE=issuer: cấp token bind, khớp sự kiện, phát lên Redis)
//	                         └─Redis─> ws-gateway (giữ socket, kiểm quyền từng người nhận)
//
// Mỗi test đi qua đường thật: đăng nhập ở auth, tạo Subscription và lấy token bind ở fhir-server, bind và nhận sự kiện ở gateway.

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startRedis: miniredis trong tiến trình test (cổng TCP thật cho các binary), hoặc Redis THẬT nếu đặt MESSAGING_TEST_REDIS_ADDR.
func startRedis(t *testing.T) string {
	t.Helper()
	if addr := os.Getenv("MESSAGING_TEST_REDIS_ADDR"); addr != "" {
		c := goredis.NewClient(&goredis.Options{Addr: addr})
		if err := c.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("Redis test %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = c.FlushDB(context.Background()).Err(); _ = c.Close() })
		return addr
	}
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	return mr.Addr()
}

// addWebSocketSplit thêm cấu hình issuer vào fhir-server và chạy ws-gateway. Gọi trước khi chạy fhir-server.
func (s *stack) addWebSocketSplit(t *testing.T, dir string, fhirEnv map[string]string) {
	t.Helper()
	s.redisAddr, s.wsKey = startRedis(t), randomHex(24)
	gPort := freePort(t)
	s.gateway, s.gatewayWS = fmt.Sprintf("http://127.0.0.1:%d", gPort), fmt.Sprintf("ws://127.0.0.1:%d/ws/subscriptions-r4", gPort)
	for k, v := range map[string]string{
		"FHIR_SUBSCRIPTIONS": "true", "FHIR_WS_MODE": "issuer", "BROKER_DRIVER": "redis", "REDIS_URL": "redis://" + s.redisAddr + "/0",
		"FHIR_WS_SIGNING_KEY": s.wsKey, "FHIR_WS_URL": s.gatewayWS,
	} {
		fhirEnv[k] = v
	}
	s.gatewayBin = build(t, repoRoot(t), dir, "ws-gateway")
	s.gatewayEnv = map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", gPort), "ENVIRONMENT": "local", "REDIS_URL": "redis://" + s.redisAddr + "/0",
		"FHIR_WS_SIGNING_KEY": s.wsKey, "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "false",
		"FHIR_WS_PRESENCE_TTL": "3s", "FHIR_WS_CLOSE_TIMEOUT": "2s",
	}
	s.gatewayProc = start(t, s.gatewayBin, s.gatewayEnv)
}

func (s *stack) restartFhirServer() {
	s.t.Helper()
	if !s.fhirProc.stop() {
		s.t.Fatal("fhir-server không tắt êm")
	}
	s.fhirProc = start(s.t, s.fhirBin, s.fhirEnv)
	waitReady(s.t, s.fhir+"/healthz")
}

func (s *stack) restartGateway() {
	s.t.Helper()
	if !s.gatewayProc.stop() {
		s.t.Fatal("ws-gateway không tắt êm")
	}
	s.gatewayProc = start(s.t, s.gatewayBin, s.gatewayEnv)
	waitReady(s.t, s.gateway+"/health/ready")
}

func (s *stack) createSubscription(token, criteria string) string {
	s.t.Helper()
	r := s.fhirPost(token, "/Subscription", `{"resourceType":"Subscription","status":"active","reason":"e2e","criteria":"`+criteria+`","channel":{"type":"websocket"}}`)
	if r.status != 201 {
		s.t.Fatalf("tạo Subscription: %d %s", r.status, r.body)
	}
	return r.json(s.t)["id"].(string)
}

// bindingToken gọi $get-ws-binding-token; trả token, websocket-url và mã HTTP.
func (s *stack) bindingToken(access, sub string) (token, wsURL string, status int) {
	s.t.Helper()
	r := s.fhirGet(access, "/Subscription/"+sub+"/$get-ws-binding-token")
	if r.status != 200 {
		return "", "", r.status
	}
	for _, p := range r.json(s.t)["parameter"].([]any) {
		switch pm := p.(map[string]any); pm["name"] {
		case "token":
			token, _ = pm["valueString"].(string)
		case "websocket-url":
			wsURL, _ = pm["valueUrl"].(string)
		}
	}
	return token, wsURL, 200
}

func (s *stack) createObservation(token string) string {
	s.t.Helper()
	r := s.fhirPost(token, "/Observation", `{"resourceType":"Observation","status":"final","code":{"text":"e2e"}}`)
	if r.status != 201 {
		s.t.Fatalf("tạo Observation: %d %s", r.status, r.body)
	}
	return r.json(s.t)["id"].(string)
}

type wsClient struct {
	t    *testing.T
	c    *websocket.Conn
	msgs chan string
	end  chan websocket.StatusCode // mã đóng khi kết nối kết thúc
}

func (s *stack) dialGateway() *wsClient {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, s.gatewayWS, nil)
	if err != nil {
		s.t.Fatalf("dial gateway: %v", err)
	}
	w := &wsClient{t: s.t, c: c, msgs: make(chan string, 100), end: make(chan websocket.StatusCode, 1)}
	s.t.Cleanup(func() { _ = c.CloseNow() })
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				w.end <- websocket.CloseStatus(err)
				return
			}
			w.msgs <- string(data)
		}
	}()
	return w
}

func bindFrame(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]any{"token": token}})
	return b
}

// next chờ thông điệp kế tiếp; ok=false nếu hết hạn chờ hoặc kết nối đóng.
func (w *wsClient) next(d time.Duration) (string, bool) {
	select {
	case m := <-w.msgs:
		return m, true
	case code := <-w.end:
		w.end <- code
		return "", false
	case <-time.After(d):
		return "", false
	}
}

// handshake bind rồi chờ handshake; fatal nếu bị đóng hoặc không có.
func (w *wsClient) bind(token string) {
	w.t.Helper()
	if err := w.c.Write(context.Background(), websocket.MessageText, bindFrame(token)); err != nil {
		w.t.Fatal(err)
	}
	m, ok := w.next(5 * time.Second)
	if !ok || !strings.Contains(m, `"handshake"`) {
		w.t.Fatalf("handshake: %q ok=%v", m, ok)
	}
}

func (w *wsClient) wantEventFor(resource string) {
	w.t.Helper()
	m, ok := w.next(8 * time.Second)
	if !ok || !strings.Contains(m, "event-notification") || !strings.Contains(m, resource) {
		w.t.Fatalf("muốn event-notification của %s, được %q ok=%v", resource, m, ok)
	}
}

func (w *wsClient) wantSilence(why string) {
	w.t.Helper()
	if m, ok := w.next(700 * time.Millisecond); ok {
		w.t.Fatalf("%s: nhận được %s", why, m)
	}
}

// closedWith chờ kết nối bị đóng và trả mã đóng.
func (w *wsClient) closedWith() websocket.StatusCode {
	w.t.Helper()
	for {
		select {
		case <-w.msgs:
		case code := <-w.end:
			return code
		case <-time.After(8 * time.Second):
			w.t.Fatal("kết nối không đóng")
		}
	}
}

func (s *stack) presenceKeys() []string {
	s.t.Helper()
	c := goredis.NewClient(&goredis.Options{Addr: s.redisAddr})
	defer c.Close()
	keys, err := c.Keys(context.Background(), "*presence*").Result()
	if err != nil {
		s.t.Fatal(err)
	}
	return keys
}

func (s *stack) waitPresence(want bool) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if (len(s.presenceKeys()) > 0) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("sổ hiện diện: muốn có=%v, hiện %v", want, s.presenceKeys())
}

// Đường chính: người dùng đăng nhập ở auth, tạo Subscription và lấy token bind ở fhir-server, bind và nhận sự kiện ở gateway.
func TestWSSplitDeliversEventsToAnAuthenticatedUser(t *testing.T) {
	s := newStackWS(t)
	access := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	sub := s.createSubscription(access, "Observation")

	token, wsURL, status := s.bindingToken(access, sub)
	if status != 200 || wsURL != s.gatewayWS {
		t.Fatalf("token: %d, websocket-url = %q, muốn địa chỉ gateway %q", status, wsURL, s.gatewayWS)
	}
	c := s.dialGateway()
	c.bind(token)

	id := s.createObservation(access)
	c.wantEventFor("Observation/" + id)
	// sai loại: không nhận
	if r := s.fhirPost(access, "/Patient", patientJSON); r.status != 201 {
		t.Fatalf("tạo Patient: %d", r.status)
	}
	c.wantSilence("Patient không khớp criteria")
	id2 := s.createObservation(access)
	c.wantEventFor("Observation/" + id2)

	// fhir-server issuer không còn giữ websocket
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.fhir, "http")+"/ws/subscriptions-r4", nil); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("fhir-server issuer không được có endpoint websocket: %v %v", resp, err)
	}
}

// Cách ly project xuyên cả chuỗi: auth (claim project) -> fhir-server (cấp token, khớp theo project) -> gateway.
func TestWSSplitIsolatesProjects(t *testing.T) {
	s := newStackWS(t)
	a := s.clientCredentials(s.clientA, s.secretA, "")
	b := s.clientCredentials(s.clientB, s.secretB, "")
	subA, subB := s.createSubscription(a, "Observation"), s.createSubscription(b, "Observation")

	// project B không lấy được token bind của Subscription của A (không thấy nó)
	if _, _, st := s.bindingToken(b, subA); st != 404 {
		t.Errorf("B lấy token bind của Subscription của A: %d, muốn 404", st)
	}
	tokA, _, _ := s.bindingToken(a, subA)
	tokB, _, _ := s.bindingToken(b, subB)
	cA, cB := s.dialGateway(), s.dialGateway()
	cA.bind(tokA)
	cB.bind(tokB)

	idA := s.createObservation(a)
	cA.wantEventFor("Observation/" + idA)
	cB.wantSilence("dữ liệu của project A tới socket của project B")
	idB := s.createObservation(b)
	cB.wantEventFor("Observation/" + idB)
	cA.wantSilence("dữ liệu của project B tới socket của project A")
}

// Quyền của người nhận (scope SMART của token auth) được chụp vào token bind và kiểm lại ở gateway cho từng sự kiện.
func TestWSSplitEnforcesRecipientScopesFromAuth(t *testing.T) {
	s := newStackWS(t)
	full := s.clientCredentials(s.clientA, s.secretA, "")
	limited := s.clientCredentials(s.clientA, s.secretA, "system/Subscription.rs")
	if r := s.fhirGet(limited, "/Observation"); r.status != 403 {
		t.Fatalf("token hẹp phải không đọc được Observation: %d", r.status)
	}
	sub := s.createSubscription(full, "Observation")
	tokFull, _, st1 := s.bindingToken(full, sub)
	tokLimited, _, st2 := s.bindingToken(limited, sub) // đọc được Subscription nên lấy được token
	if st1 != 200 || st2 != 200 {
		t.Fatalf("token bind: %d %d", st1, st2)
	}
	cFull, cLimited := s.dialGateway(), s.dialGateway()
	cFull.bind(tokFull)
	cLimited.bind(tokLimited)

	id := s.createObservation(full)
	cFull.wantEventFor("Observation/" + id)
	cLimited.wantSilence("người nhận không có quyền đọc Observation không được nhận sự kiện (và không được lộ tham chiếu)")
}

// Gateway không tin gì ngoài token do fhir-server ký: token rác, ký bằng khóa khác, payload bị sửa, hoặc access token JWT đều bị đóng 1008.
func TestWSSplitGatewayRejectsForgedAndMisusedTokens(t *testing.T) {
	s := newStackWS(t)
	access := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	sub := s.createSubscription(access, "Observation")
	good, _, _ := s.bindingToken(access, sub)

	forge := func(key string, claims map[string]any) string {
		raw, _ := json.Marshal(claims)
		m := hmac.New(sha256.New, []byte(key))
		m.Write([]byte("fhir-ws-binding|"))
		m.Write(raw)
		return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	}
	claims := map[string]any{"v": 1, "sub": sub, "proj": s.projectA, "exp": time.Now().Add(time.Minute).Unix(), "jti": "x"}
	parts := strings.Split(good, ".")
	swapped, _ := json.Marshal(map[string]any{"v": 1, "sub": "subscription-khac", "proj": s.projectA, "exp": time.Now().Add(time.Minute).Unix(), "jti": "x"})
	versioned := map[string]any{"v": 99, "sub": sub, "proj": s.projectA, "exp": time.Now().Add(time.Minute).Unix(), "jti": "x"}
	expired := map[string]any{"v": 1, "sub": sub, "proj": s.projectA, "exp": time.Now().Add(-time.Minute).Unix(), "jti": "x"}
	for name, tok := range map[string]string{
		"rác":                        "abc.def",
		"rỗng":                       "",
		"ký bằng khóa khác":          forge(randomHex(24), claims),
		"payload bị sửa, giữ chữ ký": base64.RawURLEncoding.EncodeToString(swapped) + "." + parts[1],
		"phiên bản lạ (khóa đúng)":   forge(s.wsKey, versioned),
		"hết hạn (khóa đúng)":        forge(s.wsKey, expired),
		"access token JWT của auth":  access,
		"id token/refresh dùng nhầm": s.signIn("a@example.org", s.clientA)["refresh_token"].(string),
	} {
		c := s.dialGateway()
		_ = c.c.Write(context.Background(), websocket.MessageText, bindFrame(tok))
		if code := c.closedWith(); code != websocket.StatusPolicyViolation {
			t.Errorf("%s: mã đóng = %v, muốn 1008", name, code)
		}
	}
	// token thật vẫn bind được sau tất cả những lần bị từ chối
	c := s.dialGateway()
	c.bind(good)
}

// Mục tiêu của việc tách: khởi động lại (deploy) fhir-server KHÔNG ngắt socket, và sự kiện sau đó vẫn tới.
func TestWSSplitSocketsSurviveFhirServerRestart(t *testing.T) {
	s := newStackWS(t)
	access := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	sub := s.createSubscription(access, "Observation")
	token, _, _ := s.bindingToken(access, sub)
	c := s.dialGateway()
	c.bind(token)
	c.wantSilence("trước khi có sự kiện")

	s.restartFhirServer()

	id := s.createObservation(access)
	c.wantEventFor("Observation/" + id)
	// socket mới sau restart cũng bind được bằng token mới
	token2, _, st := s.bindingToken(access, sub)
	if st != 200 {
		t.Fatalf("token sau restart: %d", st)
	}
	c2 := s.dialGateway()
	c2.bind(token2)
	id2 := s.createObservation(access)
	c.wantEventFor("Observation/" + id2)
	c2.wantEventFor("Observation/" + id2)
}

// Gateway tắt êm: socket nhận 1001, sổ hiện diện được gỡ ngay, fhir-server vẫn ghi bình thường; gateway lên lại thì bind lại được.
func TestWSSplitGatewayShutdownIsGracefulAndRecoverable(t *testing.T) {
	s := newStackWS(t)
	access := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	sub := s.createSubscription(access, "Observation")
	token, _, _ := s.bindingToken(access, sub)
	c := s.dialGateway()
	c.bind(token)
	s.waitPresence(true)

	if !s.gatewayProc.stop() {
		t.Fatal("ws-gateway không tắt êm")
	}
	if code := c.closedWith(); code != websocket.StatusGoingAway {
		t.Errorf("mã đóng = %v, muốn 1001", code)
	}
	if keys := s.presenceKeys(); len(keys) != 0 {
		t.Errorf("sổ hiện diện phải được gỡ khi gateway tắt êm: %v", keys)
	}
	// fhir-server không bị ảnh hưởng: ghi vẫn thành công
	s.createObservation(access)

	// gateway lên lại; client bind lại bằng token mới
	s.gatewayProc = start(t, s.gatewayBin, s.gatewayEnv)
	waitReady(t, s.gateway+"/health/ready")
	token2, _, _ := s.bindingToken(access, sub)
	c2 := s.dialGateway()
	c2.bind(token2)
	id := s.createObservation(access)
	c2.wantEventFor("Observation/" + id)
}

// Hết socket thì gateway thôi quan tâm Subscription: sổ hiện diện được gỡ (không cần chờ hết hạn), và bind lại vẫn nhận sự kiện.
func TestWSSplitReleasesInterestWhenTheLastSocketCloses(t *testing.T) {
	s := newStackWS(t)
	access := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	sub := s.createSubscription(access, "Observation")
	token, _, _ := s.bindingToken(access, sub)
	c := s.dialGateway()
	c.bind(token)
	s.waitPresence(true)

	_ = c.c.Close(websocket.StatusNormalClosure, "")
	s.waitPresence(false)
	s.createObservation(access) // không có ai nghe: ghi vẫn thành công

	token2, _, _ := s.bindingToken(access, sub)
	c2 := s.dialGateway()
	c2.bind(token2)
	id := s.createObservation(access)
	c2.wantEventFor("Observation/" + id)
}
