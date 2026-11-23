package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/ws-gateway/internal/bootstrap"
	"trivita-fhir/apps/ws-gateway/internal/config"
	"trivita-fhir/pkg/fhircast"
)

const (
	fcIssuer = "https://id.test"
	fcAud    = "fhir"
)

var fcSecret = strings.Repeat("h", 40)

func fcSettings(mr *rds, mut func(*config.Settings)) config.Settings {
	return settingsFor(mr, func(s *config.Settings) {
		s.FhircastEnabled = true
		s.FhircastPublicURL = "ws://gw.test:8007"
		s.FhircastJWTIssuer, s.FhircastJWTAudience, s.FhircastJWTHMACSecret = fcIssuer, fcAud, fcSecret
		if mut != nil {
			mut(s)
		}
	})
}

func jwtFor(t *testing.T, project, sub, scope string) string {
	t.Helper()
	c := jwt.MapClaims{"iss": fcIssuer, "aud": fcAud, "sub": sub, "scope": scope, "exp": time.Now().Add(time.Hour).Unix()}
	if project != "" {
		c["fhir_project"] = project
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(fcSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func call(t *testing.T, method, url, tok string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func TestGatewayFhircastEndToEndOverRealJWT(t *testing.T) {
	mr := newRedisT(t)
	g := newGateway(t, fcSettings(mr, nil))
	tok := jwtFor(t, "projA", "alice", "openid fhircast/*")

	if code, _ := call(t, "GET", g.srv.URL+"/fhircast/STU3/.well-known/fhircast-configuration", "", nil); code != 200 {
		t.Fatalf("well-known: %d", code)
	}
	code, m := call(t, "POST", g.srv.URL+"/fhircast/STU3", tok, map[string]any{
		"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "t1", "hub.events": "Patient-open"})
	if code != 202 || !strings.HasPrefix(m["hub.channel.endpoint"].(string), "ws://gw.test:8007/ws/fhircast/") {
		t.Fatalf("subscribe: %d %v", code, m)
	}
	code, m = call(t, "POST", g.srv.URL+"/hub", tok, map[string]any{"id": "e1", "timestamp": "2026-01-01T00:00:00Z",
		"event": map[string]any{"hub.topic": "t1", "hub.event": "Patient-open", "context": []any{
			map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": "p1"}}}}})
	if code != 202 {
		t.Fatalf("change: %d %v", code, m)
	}
	code, m = call(t, "GET", g.srv.URL+"/fhircast/STU3/t1", tok, nil)
	if code != 200 || m["context.type"] != "Patient" {
		t.Fatalf("get: %d %v", code, m)
	}
	// project khác cùng chuỗi topic không thấy
	_, m = call(t, "GET", g.srv.URL+"/fhircast/STU3/t1", jwtFor(t, "projB", "bob", "fhircast/*"), nil)
	if m["context.type"] != "" {
		t.Errorf("project khác thấy ngữ cảnh: %v", m)
	}
}

func TestGatewayFhircastAuthorization(t *testing.T) { // F-2
	g := newGateway(t, fcSettings(newRedisT(t), nil))
	url := g.srv.URL + "/fhircast/STU3/t"
	for name, c := range map[string]struct {
		tok  string
		want int
	}{
		"không token":          {"", 401},
		"rác":                  {"garbage", 401},
		"đúng":                 {jwtFor(t, "p", "a", "fhircast/*"), 200},
		"thiếu scope fhircast": {jwtFor(t, "p", "a", "fhir.write openid"), 403},
		"thiếu project":        {jwtFor(t, "", "a", "fhircast/*"), 403},
		"token fhir.admin vẫn cần scope fhircast": {jwtFor(t, "p", "a", "fhir.admin"), 403},
	} {
		if code, _ := call(t, "GET", url, c.tok, nil); code != c.want {
			t.Errorf("%s: %d, muốn %d", name, code, c.want)
		}
	}
	// token ký bằng khóa khác
	bad, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": fcIssuer, "aud": fcAud, "sub": "a", "fhir_project": "p", "scope": "fhircast/*", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte(strings.Repeat("z", 40)))
	if code, _ := call(t, "GET", url, bad, nil); code != 401 {
		t.Errorf("sai khóa: %d", code)
	}
	// tắt đòi scope (chuyển tiếp): token fhir.* không có scope fhircast vào được
	g2 := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.FhircastRequireScope = false }))
	if code, _ := call(t, "GET", g2.srv.URL+"/fhircast/STU3/t", jwtFor(t, "p", "a", "fhir.write"), nil); code != 200 {
		t.Errorf("REQUIRE_SCOPE=false: %d", code)
	}
}

func TestGatewayFhircastDisabledByDefault(t *testing.T) {
	g := newGateway(t, settingsFor(newRedisT(t), nil))
	if g.c.Fhircast != nil {
		t.Error("Hub không được dựng khi tắt")
	}
	for _, p := range []string{"/fhircast/STU3/.well-known/fhircast-configuration", "/hub/.well-known/fhircast-configuration"} {
		if code, _ := call(t, "GET", g.srv.URL+p, "", nil); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

// Hai instance gateway chung Redis: ngữ cảnh đổi ở instance này đọc được ở instance kia, và sự kiện tới được subscriber của
// instance kia qua Bus.
func TestGatewayFhircastTwoInstancesShareState(t *testing.T) {
	mr := newRedisT(t)
	a, b := newGateway(t, fcSettings(mr, nil)), newGateway(t, fcSettings(mr, nil))
	tok := jwtFor(t, "p", "alice", "fhircast/*")

	got := make(chan fhircast.ChannelMessage, 4)
	cancel, err := b.c.Fhircast.Bus.Subscribe(context.Background(), fhircast.Channel("p", "t"), func(p []byte) {
		var m fhircast.ChannelMessage
		if json.Unmarshal(p, &m) == nil {
			got <- m
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	code, _ := call(t, "POST", a.srv.URL+"/hub", tok, map[string]any{"id": "e", "timestamp": "2026-01-01T00:00:00Z",
		"event": map[string]any{"hub.topic": "t", "hub.event": "Patient-open", "context": []any{
			map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": "p1"}}}}})
	if code != 202 {
		t.Fatalf("%d", code)
	}
	select {
	case m := <-got:
		if m.Payload["event"].(map[string]any)["hub.event"] != "Patient-open" {
			t.Errorf("%v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("instance B không nhận sự kiện của instance A")
	}
	if code, m := call(t, "GET", b.srv.URL+"/hub/t", tok, nil); code != 200 || m["context.type"] != "Patient" {
		t.Errorf("instance B phải đọc được ngữ cảnh: %d %v", code, m)
	}
	// đăng ký ở A, hủy ở B (đăng ký dùng chung qua Redis)
	_, sub := call(t, "POST", a.srv.URL+"/hub", tok, map[string]any{"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open"})
	u := sub["hub.channel.endpoint"].(string)
	if code, _ := call(t, "POST", b.srv.URL+"/hub", tok, map[string]any{"hub.channel.type": "websocket", "hub.mode": "unsubscribe", "hub.topic": "t", "hub.channel.endpoint": u}); code != 202 {
		t.Errorf("hủy ở instance khác: %d", code)
	}
}

func TestGatewayFhircastStopIsClean(t *testing.T) {
	mr := newRedisT(t)
	c, err := bootstrap.New(context.Background(), fcSettings(mr, nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Fhircast == nil {
		t.Fatal("phải có Hub")
	}
	// bus đóng trước Redis: không panic, không lỗi
	if err := c.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func fcDial(t *testing.T, g *gateway, wsURL string) *websocket.Conn {
	t.Helper()
	// hub.channel.endpoint dùng FHIRCAST_PUBLIC_URL (giả); thay phần host bằng địa chỉ test
	ep := wsURL[strings.LastIndex(wsURL, "/")+1:]
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(g.srv.URL, "http")+"/ws/fhircast/"+ep, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func fcRead(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("đọc: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// Đăng ký ở A, socket ở B (chung Redis), sự kiện đổi ở A tới socket ở B, hủy ở A đóng socket ở B.
func TestGatewayFhircastWebSocketAcrossInstances(t *testing.T) {
	mr := newRedisT(t)
	a, b := newGateway(t, fcSettings(mr, nil)), newGateway(t, fcSettings(mr, nil))
	tok := jwtFor(t, "p", "alice", "fhircast/*")
	_, sub := call(t, "POST", a.srv.URL+"/fhircast/STU3", tok, map[string]any{
		"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open"})
	u := sub["hub.channel.endpoint"].(string)

	c := fcDial(t, b, u)
	if m := fcRead(t, c); m["hub.mode"] != "subscribe" || m["hub.topic"] != "t" || m["hub.lease_seconds"] != float64(3600) {
		t.Fatalf("xác nhận: %v", m)
	}
	code, _ := call(t, "POST", a.srv.URL+"/hub", tok, map[string]any{"id": "e", "timestamp": "2026-01-01T00:00:00Z",
		"event": map[string]any{"hub.topic": "t", "hub.event": "Patient-open", "context": []any{
			map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": "p1"}}}}})
	if code != 202 {
		t.Fatalf("%d", code)
	}
	if m := fcRead(t, c); m["event"].(map[string]any)["hub.event"] != "Patient-open" {
		t.Fatalf("sự kiện: %v", m)
	}
	if code, _ := call(t, "POST", a.srv.URL+"/hub", tok, map[string]any{"hub.channel.type": "websocket", "hub.mode": "unsubscribe", "hub.topic": "t", "hub.channel.endpoint": u}); code != 202 {
		t.Fatalf("hủy: %d", code)
	}
	if m := fcRead(t, c); m["hub.mode"] != "denied" || m["hub.reason"] != "Subscriber unsubscribed from topic" {
		t.Fatalf("denied: %v", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Errorf("sau denied phải đóng 1000: %v", err)
	}
}

func TestGatewayFhircastSocketsAreClosedGracefullyOnStop(t *testing.T) {
	g := newGateway(t, fcSettings(newRedisT(t), nil))
	tok := jwtFor(t, "p", "alice", "fhircast/*")
	_, sub := call(t, "POST", g.srv.URL+"/hub", tok, map[string]any{"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open"})
	c := fcDial(t, g, sub["hub.channel.endpoint"].(string))
	fcRead(t, c)
	if g.c.FhircastWS.Stats().Conns != 1 {
		t.Fatalf("Conns = %d", g.c.FhircastWS.Stats().Conns)
	}
	done := make(chan error, 1)
	go func() { done <- g.c.Stop(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("client phải nhận 1001: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestGatewayFhircastWebSocketNeedsNoBearerButNeedsValidEndpoint(t *testing.T) {
	g := newGateway(t, fcSettings(newRedisT(t), nil))
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(g.srv.URL, "http")+"/ws/fhircast/forged", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if m := fcRead(t, c); m["hub.reason"] != "invalid endpoint" {
		t.Errorf("endpoint giả: %v", m)
	}
}

func fcSub(t *testing.T, g *gateway, tok string) string {
	t.Helper()
	code, sub := call(t, "POST", g.srv.URL+"/hub", tok, map[string]any{"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open"})
	if code != 202 {
		t.Fatalf("subscribe: %d %v", code, sub)
	}
	return sub["hub.channel.endpoint"].(string)
}

// Các giá trị cấu hình phải thật sự tới được kênh websocket (không chỉ có trong Settings).
func TestGatewayFhircastWebSocketHonorsSettings(t *testing.T) {
	tok := jwtFor(t, "p", "alice", "fhircast/*")

	t.Run("giới hạn socket mỗi endpoint", func(t *testing.T) {
		g := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.FhircastMaxSocketsPerEndpoint = 1 }))
		u := fcSub(t, g, tok)
		fcRead(t, fcDial(t, g, u))
		c2 := fcDial(t, g, u)
		if m := fcRead(t, c2); m["hub.reason"] != "too many connections for this endpoint" {
			t.Errorf("socket thứ hai phải bị từ chối: %v", m)
		}
	})
	t.Run("số socket tối đa", func(t *testing.T) {
		g := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.FhircastMaxConnections = 1 }))
		u := fcSub(t, g, tok)
		fcRead(t, fcDial(t, g, u))
		_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(g.srv.URL, "http")+"/ws/fhircast/"+u[strings.LastIndex(u, "/")+1:], nil)
		if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("vượt MaxConns phải 503: %v %v", resp, err)
		}
	})
	t.Run("Origin", func(t *testing.T) {
		g := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.WSAllowedOrigins = "app.example.org" }))
		u := fcSub(t, g, tok)
		ep := u[strings.LastIndex(u, "/")+1:]
		_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(g.srv.URL, "http")+"/ws/fhircast/"+ep,
			&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example.com"}}})
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("Origin lạ phải 403: %v %v", resp, err)
		}
		// Origin nằm trong FHIR_WS_ALLOWED_ORIGINS phải vào được (nếu cấu hình không tới nơi thì Origin khác host bị từ chối)
		ok, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(g.srv.URL, "http")+"/ws/fhircast/"+ep,
			&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://app.example.org"}}})
		if err != nil {
			t.Fatalf("Origin được phép phải vào được: %v", err)
		}
		_ = ok.CloseNow()
	})
	t.Run("giới hạn kết nối mỗi IP", func(t *testing.T) {
		g := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.WSMaxConnsPerIP = 1 }))
		u := fcSub(t, g, tok)
		fcRead(t, fcDial(t, g, u))
		_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(g.srv.URL, "http")+"/ws/fhircast/"+u[strings.LastIndex(u, "/")+1:], nil)
		if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("vượt FHIR_WS_MAX_CONNS_PER_IP phải 429: %v %v", resp, err)
		}
	})
	t.Run("heartbeat", func(t *testing.T) {
		g := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.FhircastHeartbeat = 80 * time.Millisecond }))
		c := fcDial(t, g, fcSub(t, g, tok))
		fcRead(t, c)
		if m := fcRead(t, c); m["event"].(map[string]any)["hub.event"] != "heartbeat" {
			t.Errorf("phải có heartbeat theo FHIRCAST_HEARTBEAT: %v", m)
		}
	})
	t.Run("lease", func(t *testing.T) {
		g := newGateway(t, fcSettings(newRedisT(t), func(s *config.Settings) { s.FhircastLease = time.Second }))
		c := fcDial(t, g, fcSub(t, g, tok))
		fcRead(t, c)
		if m := fcRead(t, c); m["hub.reason"] != "lease expired" {
			t.Errorf("hết FHIRCAST_LEASE phải denied: %v", m)
		}
	})
}
