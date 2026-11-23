package bootstrap_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

func mustJSONString(v any) string { b, _ := json.Marshal(v); return string(b) }

const gatewayWSURL = "wss://ws.example.org/ws/subscriptions-r4"

// issuerRedis trả địa chỉ Redis: miniredis, hoặc Redis THẬT nếu đặt MESSAGING_TEST_REDIS_ADDR (FLUSHDB trước và sau test).
func issuerRedis(t *testing.T) string {
	t.Helper()
	if addr := os.Getenv("MESSAGING_TEST_REDIS_ADDR"); addr != "" {
		c := goredis.NewClient(&goredis.Options{Addr: addr})
		if err := c.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("Redis test %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = c.FlushDB(context.Background()).Err(); _ = c.Close() })
		return addr
	}
	return miniredis.RunT(t).Addr()
}

func issuerSettings(addr string, mutate ...func(*config.Settings)) config.Settings {
	return settings(append([]func(*config.Settings){func(s *config.Settings) {
		s.Subscriptions, s.WSMode = true, config.WSModeIssuer
		s.BrokerDriver, s.RedisURL = "redis", "redis://"+addr
		s.WSSigningKey = strings.Repeat("w", 40)
		s.WSURL = gatewayWSURL
	}}, mutate...)...)
}

// standInGateway dựng phần việc của ws-gateway (Hub chỉ xác minh token + Feed trên Redis) trong test này.
func standInGateway(t *testing.T, addr, key string, previous ...string) (*httptest.Server, *fhirsub.Hub) {
	t.Helper()
	var prev [][]byte
	for _, p := range previous {
		prev = append(prev, []byte(p))
	}
	tokens, err := fhirsub.NewTokens(fhirsub.TokenConfig{SigningKey: []byte(key), Previous: prev})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := fhirsub.NewHub(fhirsub.WSConfig{Tokens: tokens, TokenOnly: true, Heartbeat: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := messaging.NewRedis("redis://"+addr, messaging.RedisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := fhirsub.NewFeed(hub, broker, broker, fhirsub.FeedConfig{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(hub.Handler())
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
		_ = feed.Stop(context.Background())
		_ = hub.Close(context.Background())
		_ = broker.Stop(context.Background())
	})
	return srv, hub
}

// bindOnGateway lấy token ở fhir-server (REST) rồi bind trên gateway; trả client sau handshake và websocket-url được trả về.
func bindOnGateway(t *testing.T, fhirSrv, gw *httptest.Server, restToken, subID string) (*wsClient, string) {
	t.Helper()
	r := call(t, fhirSrv, "GET", "/fhir/R4/Subscription/"+subID+"/$get-ws-binding-token", restToken, nil)
	if r.status != 200 {
		t.Fatalf("token: %d %s", r.status, r.body)
	}
	var token, url string
	for _, p := range r.json(t)["parameter"].([]any) {
		pm := p.(map[string]any)
		switch pm["name"] {
		case "token":
			token, _ = pm["valueString"].(string)
		case "websocket-url":
			url, _ = pm["valueUrl"].(string)
		}
	}
	c := dialWS(t, gw)
	if err := c.Write(context.Background(), websocket.MessageText, bindFrame(token)); err != nil {
		t.Fatal(err)
	}
	hs, ok := c.next(t, 3*time.Second)
	if !ok || hs["entry"].([]any)[0].(map[string]any)["resource"].(map[string]any)["type"] != "handshake" {
		t.Fatalf("handshake: %v", hs)
	}
	return c, url
}

// Đường chính: fhir-server (chế độ issuer, KHÔNG có Hub) cấp token và khớp/phát sự kiện, gateway giữ socket và giao.
func TestIssuerModeDeliversThroughTheGateway(t *testing.T) {
	addr := issuerRedis(t)
	c, srv := newServer(t, issuerSettings(addr))
	if c.Hub != nil || c.Tokens == nil || c.Manager == nil {
		t.Fatalf("issuer: Hub=%v Tokens=%v Manager=%v", c.Hub, c.Tokens, c.Manager)
	}
	gw, _ := standInGateway(t, addr, strings.Repeat("w", 40))

	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient?gender=female")), 201).str(t, "id")
	ws, url := bindOnGateway(t, srv, gw, readSecret, sub)
	if url != gatewayWSURL {
		t.Errorf("websocket-url = %q, muốn địa chỉ của gateway %q", url, gatewayWSURL)
	}

	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, map[string]any{"resourceType": "Patient", "gender": "male"}), 201) // không khớp
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, map[string]any{"resourceType": "Patient", "id": "p1", "gender": "female"}), 201)
	m, ok := ws.next(t, 3*time.Second)
	if !ok || !strings.Contains(mustJSONString(m), "Patient/") || !strings.Contains(mustJSONString(m), "event-notification") {
		t.Fatalf("event: %v", m)
	}
	if st := c.Manager.Stats(); st.Published != 1 {
		t.Errorf("Published = %d, muốn 1", st.Published)
	}
}

// Ở chế độ issuer fhir-server không còn endpoint websocket và không có Hub cục bộ.
func TestIssuerModeHasNoLocalWebSocketEndpoint(t *testing.T) {
	_, srv := newServer(t, issuerSettings(issuerRedis(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/subscriptions-r4", nil)
	if err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("fhir-server issuer không được giữ websocket: %v %v", resp, err)
	}
}

// Quyền của người xin token vẫn được kiểm ở fhir-server: không token là 401, Subscription không có là 404.
func TestIssuerModeKeepsTokenEndpointProtected(t *testing.T) {
	_, srv := newServer(t, issuerSettings(issuerRedis(t)))
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/x/$get-ws-binding-token", "", nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/missing/$get-ws-binding-token", readSecret, nil), 404)
	// Subscription không phải websocket thì không cấp token
	rh := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "t", "criteria": "Patient",
		"channel": map[string]any{"type": "rest-hook", "endpoint": "https://example.org/hook"}}), 201).str(t, "id")
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/"+rh+"/$get-ws-binding-token", readSecret, nil), 400)
}

// Không có gateway nào nghe: ghi vẫn thành công, không phát gì, và Manager đếm là không có người nghe.
func TestIssuerModeWithoutAGatewayStillWritesAndPublishesNothing(t *testing.T) {
	c, srv := newServer(t, issuerSettings(issuerRedis(t)))
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient")), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, map[string]any{"resourceType": "Patient"}), 201)
	deadline := time.Now().Add(3 * time.Second)
	for c.Manager.Stats().Unheard < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st := c.Manager.Stats(); st.Unheard != 1 || st.Published != 0 {
		t.Errorf("stats: %+v", st)
	}
}

// Xoay khóa: fhir-server còn khóa cũ, gateway đã nhận khóa mới và giữ khóa cũ ở Previous nên token vẫn bind được.
func TestIssuerModeTokensSurviveGatewayKeyRotation(t *testing.T) {
	addr := issuerRedis(t)
	oldKey, newKey := strings.Repeat("o", 40), strings.Repeat("n", 40)
	_, srv := newServer(t, issuerSettings(addr, func(s *config.Settings) { s.WSSigningKey = oldKey }))
	gw, _ := standInGateway(t, addr, newKey, oldKey)
	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient")), 201).str(t, "id")
	bindOnGateway(t, srv, gw, readSecret, sub)
}

// Gateway có khóa khác thì không bind được (đóng 1008): chứng minh gateway không tin token lạ.
func TestIssuerModeTokenIsRejectedByAGatewayWithAnotherKey(t *testing.T) {
	addr := issuerRedis(t)
	_, srv := newServer(t, issuerSettings(addr))
	gw, _ := standInGateway(t, addr, strings.Repeat("z", 40))
	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient")), 201).str(t, "id")
	r := call(t, srv, "GET", "/fhir/R4/Subscription/"+sub+"/$get-ws-binding-token", readSecret, nil)
	var token string
	for _, p := range r.json(t)["parameter"].([]any) {
		if pm := p.(map[string]any); pm["name"] == "token" {
			token, _ = pm["valueString"].(string)
		}
	}
	c := dialWS(t, gw)
	_ = c.Write(context.Background(), websocket.MessageText, bindFrame(token))
	if _, ok := c.next(t, 3*time.Second); ok {
		t.Fatal("không được bind với khóa lạ")
	}
	err := <-c.done
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("mã đóng = %v", websocket.CloseStatus(err))
	}
}

// Chế độ cũ (FHIR_WEBSOCKET=true và FHIR_WS_MODE=embedded) vẫn giữ Hub cục bộ và vẫn cấp token từ chính Hub đó.
func TestEmbeddedAliasStillBuildsTheLocalHub(t *testing.T) {
	for name, mut := range map[string]func(*config.Settings){
		"FHIR_WEBSOCKET": func(s *config.Settings) { s.WebSocket = true },
		"FHIR_WS_MODE":   func(s *config.Settings) { s.WSMode = config.WSModeEmbedded },
	} {
		c, _ := newServer(t, wsSettings(func(s *config.Settings) { s.WebSocket = false; mut(s) }))
		if c.Hub == nil || c.Tokens != c.Hub.Tokens() {
			t.Errorf("%s: Hub=%v", name, c.Hub)
		}
	}
	// off: không có Hub, không có Tokens, không có route cấp token
	c, srv := newServer(t, settings(func(s *config.Settings) { s.Subscriptions = true }))
	if c.Hub != nil || c.Tokens != nil {
		t.Errorf("off: Hub=%v Tokens=%v", c.Hub, c.Tokens)
	}
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/x/$get-ws-binding-token", writeSecret, nil), 404)
}
