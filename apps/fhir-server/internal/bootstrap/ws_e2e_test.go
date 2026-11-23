package bootstrap_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/fhir-server/internal/config"
)

type wsClient struct {
	*websocket.Conn
	in   chan map[string]any
	done chan error
}

func dialWS(t *testing.T, srv *httptest.Server) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/subscriptions-r4", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	c := &wsClient{Conn: ws, in: make(chan map[string]any, 100), done: make(chan error, 1)}
	go func() {
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				c.done <- err
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			c.in <- m
		}
	}()
	return c
}

func (c *wsClient) next(t *testing.T, wait time.Duration) (map[string]any, bool) {
	t.Helper()
	select {
	case m := <-c.in:
		return m, true
	case err := <-c.done:
		c.done <- err
		return nil, false
	case <-time.After(wait):
		return nil, false
	}
}

func bindFrame(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]any{"token": token}})
	return b
}

func wsSubscription(criteria string) map[string]any {
	return map[string]any{"resourceType": "Subscription", "status": "active", "reason": "ws test", "criteria": criteria, "channel": map[string]any{"type": "websocket"}}
}

// bindTo lấy token qua REST rồi bind một kết nối websocket mới; trả client sau handshake.
func bindTo(t *testing.T, srv *httptest.Server, restToken, subID string) *wsClient {
	t.Helper()
	r := call(t, srv, "GET", "/fhir/R4/Subscription/"+subID+"/$get-ws-binding-token", restToken, nil)
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
	if token == "" || url != "ws://fhir.test/ws/subscriptions-r4" {
		t.Fatalf("token=%q url=%q", token, url)
	}
	c := dialWS(t, srv)
	if err := c.Write(context.Background(), websocket.MessageText, bindFrame(token)); err != nil {
		t.Fatal(err)
	}
	hs, ok := c.next(t, 2*time.Second)
	if !ok || hs["entry"].([]any)[0].(map[string]any)["resource"].(map[string]any)["type"] != "handshake" {
		t.Fatalf("handshake: %v", hs)
	}
	return c
}

func wsSettings(mutate ...func(*config.Settings)) config.Settings {
	return settings(append([]func(*config.Settings){func(s *config.Settings) {
		s.Subscriptions, s.WebSocket = true, true
		s.WSSigningKey = strings.Repeat("w", 40)
	}}, mutate...)...)
}

func TestWebSocketSubscriptionEndToEnd(t *testing.T) {
	c, srv := newServer(t, wsSettings())
	if c.Hub == nil {
		t.Fatal("Hub phải được tạo")
	}
	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient?gender=female")), 201).str(t, "id")

	// không có token hoặc chỉ token đọc: token đọc được lấy binding token (chỉ đọc), không token là 401
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/"+sub+"/$get-ws-binding-token", "", nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/missing/$get-ws-binding-token", readSecret, nil), 404)
	ws := bindTo(t, srv, readSecret, sub)

	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, map[string]any{"resourceType": "Patient", "gender": "male"}), 201) // không khớp
	pid := want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, map[string]any{"resourceType": "Patient", "gender": "female", "name": []any{map[string]any{"family": "Live"}}}), 201).str(t, "id")
	m, ok := ws.next(t, 3*time.Second)
	if !ok {
		t.Fatal("không nhận được thông báo")
	}
	entries := m["entry"].([]any)
	st := entries[0].(map[string]any)["resource"].(map[string]any)
	res := entries[1].(map[string]any)["resource"].(map[string]any)
	if st["type"] != "event-notification" || res["id"] != pid || res["resourceType"] != "Patient" {
		t.Fatalf("thông báo: %v", m)
	}
	if extra, ok := ws.next(t, 200*time.Millisecond); ok {
		t.Fatalf("nhận thừa: %v", extra)
	}
	// Subscription không phải websocket: không cấp token
	rest := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{"resourceType": "Subscription", "status": "active", "reason": "x",
		"criteria": "Patient", "channel": map[string]any{"type": "rest-hook", "endpoint": "http://127.0.0.1:1/hook"}}), 201).str(t, "id")
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/"+rest+"/$get-ws-binding-token", readSecret, nil), 400)
}

func TestWebSocketSubscriptionIsolatedByProject(t *testing.T) {
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) {
		s.Subscriptions, s.WebSocket, s.WSSigningKey = true, true, strings.Repeat("w", 40)
	}))
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")
	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", a, wsSubscription("Patient")), 201).str(t, "id")

	// project B không lấy được token cho Subscription của A
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/"+sub+"/$get-ws-binding-token", b, nil), 404)
	ws := bindTo(t, srv, a, sub)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", b, patient("Beta")), 201)
	if m, ok := ws.next(t, 300*time.Millisecond); ok {
		t.Fatalf("nhận dữ liệu của project khác: %v", m)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Alpha")), 201)
	if _, ok := ws.next(t, 3*time.Second); !ok {
		t.Fatal("không nhận dữ liệu cùng project")
	}
	// token quyền hạn chế không đọc được Subscription thì không lấy được token
	noSub := tok(t, "A", "system/Patient.read")
	want(t, call(t, srv, "GET", "/fhir/R4/Subscription/"+sub+"/$get-ws-binding-token", noSub, nil), 403)
}

func TestWebSocketGracefulShutdownClosesConnections(t *testing.T) {
	c, srv := newServer(t, wsSettings())
	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient")), 201).str(t, "id")
	ws := bindTo(t, srv, readSecret, sub)
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.next(t, 2*time.Second); ok {
		t.Fatal("không mong nhận thông báo")
	}
	if code := websocket.CloseStatus(<-ws.done); code != websocket.StatusGoingAway {
		t.Errorf("mã đóng = %v, muốn GoingAway", code)
	}
}

// Quyền đọc được kiểm lúc phát, theo từng người nhận: token chỉ đọc được Subscription (đã qua cổng lấy token) nhưng không
// có chữ r trên Patient, hoặc có AccessPolicy loại bớt Patient, không được nhận nội dung (kể cả tham chiếu) của sự kiện.
func TestWebSocketDeliveryIsAuthorizedPerRecipient(t *testing.T) {
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) {
		s.Subscriptions, s.WebSocket, s.WSSigningKey = true, true, strings.Repeat("w", 40)
	}))
	admin := tok(t, "A", "fhir.write")
	sub := want(t, call(t, srv, "POST", "/fhir/R4/Subscription", admin, wsSubscription("Patient")), 201).str(t, "id")

	subOnly := tok(t, "A", "system/Subscription.r system/Observation.r") // đọc được Subscription, không đọc được Patient
	female := tok(t, "A", "system/Subscription.r system/Patient.r", func(c jwt.MapClaims) {
		c["fhir_access_policy"] = []any{"Subscription", "Patient?gender=female"}
	})
	other := tok(t, "A", "system/Subscription.r system/Patient.r", func(c jwt.MapClaims) {
		c["fhir_access_policy"] = []any{"Subscription", "Observation"} // policy không liệt kê Patient
	})
	full := tok(t, "A", "fhir.read")

	denied := []*wsClient{bindTo(t, srv, subOnly, sub), bindTo(t, srv, other, sub)}
	scoped := bindTo(t, srv, female, sub)
	open := bindTo(t, srv, full, sub)

	male := want(t, call(t, srv, "POST", "/fhir/R4/Patient", admin, map[string]any{"resourceType": "Patient", "gender": "male", "name": []any{map[string]any{"family": "M"}}}), 201).str(t, "id")
	f := want(t, call(t, srv, "POST", "/fhir/R4/Patient", admin, map[string]any{"resourceType": "Patient", "gender": "female", "name": []any{map[string]any{"family": "F"}}}), 201).str(t, "id")

	focus := func(m map[string]any) string {
		return m["entry"].([]any)[1].(map[string]any)["resource"].(map[string]any)["id"].(string)
	}
	for _, id := range []string{male, f} { // người không giới hạn thấy cả hai
		m, ok := open.next(t, 3*time.Second)
		if !ok || focus(m) != id {
			t.Fatalf("open: muốn %s, nhận %v", id, m)
		}
	}
	m, ok := scoped.next(t, 3*time.Second)
	if !ok || focus(m) != f {
		t.Fatalf("scoped: muốn đúng %s, nhận %v", f, m)
	}
	if extra, ok := scoped.next(t, 300*time.Millisecond); ok {
		t.Fatalf("scoped nhận thừa (nam bị lọc): %v", extra)
	}
	for i, d := range denied {
		if m, ok := d.next(t, 300*time.Millisecond); ok {
			t.Fatalf("người nhận %d không đủ quyền nhưng nhận: %v", i, m)
		}
	}
}
