package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// FHIRcast end-to-end qua HAI tiến trình thật và hai "ứng dụng lâm sàng" giả: auth cấp token (đăng nhập thật, PKCE) kèm hub.url và
// hub.topic; ws-gateway xác minh Bearer bằng JWKS CỦA auth (đây là lần duy nhất đường JWKS của gateway được chạy thật), giữ đăng ký,
// ngữ cảnh và websocket. Hai ứng dụng của CÙNG một người dùng nhận cùng topic và đồng bộ ngữ cảnh với nhau.

type castStack struct {
	t                          *testing.T
	auth, gateway, gatewayWS   string
	clientA, projectA, clientB string
	hubURL                     string
}

func newCastStack(t *testing.T) *castStack {
	t.Helper()
	if os.Getenv("TRIVITA_E2E") != "1" {
		t.Skip("đặt TRIVITA_E2E=1 (make test-e2e) để chạy test nhiều tiến trình")
	}
	root, dir := repoRoot(t), t.TempDir()
	authBin, gatewayBin := build(t, root, dir, "auth"), build(t, root, dir, "ws-gateway")
	aPort, gPort := freePort(t), freePort(t)
	s := &castStack{t: t, auth: fmt.Sprintf("http://127.0.0.1:%d", aPort), gateway: fmt.Sprintf("http://127.0.0.1:%d", gPort)}
	s.gatewayWS = fmt.Sprintf("ws://127.0.0.1:%d", gPort)
	s.hubURL = s.gateway + "/fhircast/STU3"

	authEnv := map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", aPort), "DATABASE_URL": "sqlite://" + filepath.Join(dir, "auth.db"), "ENVIRONMENT": "local",
		"AUTH_BASE_URL": s.auth, "AUTH_SIGNING_KEY_PATH": filepath.Join(dir, "keys"), "AUTH_PWNED_ENABLED": "false",
		"AUTH_COOKIE_SECURE": "false", "AUTH_APP_BASE_URL": "http://localhost:3000", "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "false",
		"AUTH_FHIRCAST_HUB_URL": s.hubURL,
	}
	gen := exec.Command(authBin, "keys", "generate", "-alg", "ES256")
	gen.Env = envList(authEnv)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("keys generate: %v\n%s", err, out)
	}
	a := seed(t, authBin, authEnv, "Alpha", "a@example.org")
	b := seed(t, authBin, authEnv, "Beta", "b@example.org")
	s.clientA, s.projectA, s.clientB = a["clientId"], a["projectId"], b["clientId"]
	start(t, authBin, authEnv)
	waitReady(t, s.auth+"/health/live")

	start(t, gatewayBin, map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", gPort), "ENVIRONMENT": "local", "REDIS_URL": "redis://" + startRedis(t) + "/0",
		"FHIR_WS_SIGNING_KEY": randomHex(24), "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "false", "FHIR_WS_CLOSE_TIMEOUT": "2s",
		"FHIRCAST_ENABLED": "true", "FHIRCAST_PUBLIC_URL": s.gatewayWS, "FHIRCAST_HEARTBEAT": "1s",
		"FHIRCAST_JWT_ISSUER": s.auth, "FHIRCAST_JWT_AUDIENCE": s.auth, "FHIRCAST_JWKS_URL": s.auth + "/.well-known/jwks.json",
	})
	waitReady(t, s.gateway+"/health/ready")
	return s
}

// app là một ứng dụng lâm sàng đã đăng nhập: token của người dùng và hub.url/hub.topic nhận ở phản hồi token.
type app struct {
	name, token, hubURL, topic string
}

// login đăng nhập người dùng (PKCE) với scope và đổi code lấy token, như một SPA/ứng dụng nhúng.
func (s *castStack) login(name, email, clientID, scope string) app {
	s.t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": adminPassword, "clientId": clientID, "scope": scope,
		"codeChallenge": base64.RawURLEncoding.EncodeToString(sum[:]), "codeChallengeMethod": "S256",
	})
	r := do(s.t, "POST", s.auth+"/auth/login", "", "application/json", bytes.NewReader(body))
	code, _ := r.json(s.t)["code"].(string)
	if r.status != 200 || code == "" {
		s.t.Fatalf("%s: login %d %s", name, r.status, r.body)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID}}
	tr := do(s.t, "POST", s.auth+"/oauth2/token", "", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if tr.status != 200 {
		s.t.Fatalf("%s: đổi code %d %s", name, tr.status, tr.body)
	}
	m := tr.json(s.t)
	a := app{name: name, token: m["access_token"].(string)}
	a.hubURL, _ = m["hub.url"].(string)
	a.topic, _ = m["hub.topic"].(string)
	return a
}

func (a app) post(s *castStack, path string, body any) resp {
	b, _ := json.Marshal(body)
	return do(s.t, "POST", a.hubURL+path, a.token, "application/json", bytes.NewReader(b))
}

// subscribe đăng ký nhận sự kiện của topic và mở websocket; trả kết nối đã nhận xong xác nhận.
func (a app) subscribe(s *castStack, events string) (*websocket.Conn, string) {
	s.t.Helper()
	r := a.post(s, "", map[string]string{"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": a.topic, "hub.events": events})
	endpoint, _ := r.json(s.t)["hub.channel.endpoint"].(string)
	if r.status != 202 || !strings.HasPrefix(endpoint, s.gatewayWS+"/ws/fhircast/") {
		s.t.Fatalf("%s: đăng ký %d %s", a.name, r.status, r.body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		s.t.Fatalf("%s: mở websocket: %v", a.name, err)
	}
	s.t.Cleanup(func() { _ = c.CloseNow() })
	conf := readFrame(s.t, c)
	if conf["hub.mode"] != "subscribe" || conf["hub.topic"] != a.topic {
		s.t.Fatalf("%s: xác nhận: %v", a.name, conf)
	}
	return c, endpoint
}

func readFrame(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("đọc websocket: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// readEvent đọc tới khi gặp sự kiện hub.event == name (bỏ heartbeat).
func readEvent(t *testing.T, c *websocket.Conn, name string) map[string]any {
	t.Helper()
	for i := 0; i < 30; i++ {
		m := readFrame(t, c)
		if ev, _ := m["event"].(map[string]any); ev != nil && ev["hub.event"] == name {
			return m
		}
	}
	t.Fatalf("không nhận được sự kiện %s", name)
	return nil
}

func patientEvent(topic, event, id string) map[string]any {
	return map[string]any{"id": "e-" + id, "timestamp": time.Now().UTC().Format(time.RFC3339), "event": map[string]any{
		"hub.topic": topic, "hub.event": event, "context": []any{
			map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": id}}}}}
}

func TestFhircastTwoClinicalAppsStayInSync(t *testing.T) {
	s := newCastStack(t)
	const scope = "openid fhircast/*"
	viewer := s.login("viewer", "a@example.org", s.clientA, scope)
	ehr := s.login("ehr", "a@example.org", s.clientA, scope)

	// auth phát hub.url và hub.topic; hai ứng dụng của CÙNG người dùng nhận CÙNG topic (đó là cách chúng tìm thấy nhau)
	if viewer.hubURL != s.hubURL || viewer.topic == "" {
		t.Fatalf("hub.url=%q hub.topic=%q, muốn hub.url=%q", viewer.hubURL, viewer.topic, s.hubURL)
	}
	if ehr.topic != viewer.topic || ehr.hubURL != viewer.hubURL {
		t.Fatalf("hai ứng dụng của một người dùng phải có cùng topic: %q vs %q", viewer.topic, ehr.topic)
	}

	// viewer đăng ký, EHR mở bệnh nhân: viewer nhận sự kiện kèm context.versionId
	conn, _ := viewer.subscribe(s, "Patient-open,Patient-close")
	if r := ehr.post(s, "", patientEvent(viewer.topic, "Patient-open", "p1")); r.status != 202 {
		t.Fatalf("EHR mở bệnh nhân: %d %s", r.status, r.body)
	}
	ev := readEvent(t, conn, "Patient-open")
	if ev["event"].(map[string]any)["context.versionId"] == nil {
		t.Errorf("sự kiện thiếu context.versionId: %v", ev)
	}
	// ngữ cảnh hiện tại đọc được từ mọi ứng dụng cùng topic
	cur := do(t, "GET", viewer.hubURL+"/"+viewer.topic, viewer.token, "", nil).json(t)
	if cur["context.type"] != "Patient" {
		t.Errorf("ngữ cảnh hiện tại: %v", cur)
	}
	// đóng bệnh nhân: viewer nhận sự kiện, ngữ cảnh trống
	if r := ehr.post(s, "/"+viewer.topic, patientEvent(viewer.topic, "Patient-close", "p1")); r.status != 202 {
		t.Fatalf("EHR đóng bệnh nhân: %d %s", r.status, r.body)
	}
	readEvent(t, conn, "Patient-close")
	if cur := do(t, "GET", viewer.hubURL+"/"+viewer.topic, viewer.token, "", nil).json(t); cur["context.type"] != "" {
		t.Errorf("sau close ngữ cảnh phải trống: %v", cur)
	}
}

func TestFhircastTokenWithoutScopeIsRefusedByGateway(t *testing.T) {
	s := newCastStack(t)
	plain := s.login("plain", "a@example.org", s.clientA, "openid")
	if plain.hubURL != "" || plain.topic != "" {
		t.Fatalf("token thường không được có hub.*: %q %q", plain.hubURL, plain.topic)
	}
	if r := do(t, "GET", s.hubURL+"/anything", plain.token, "", nil); r.status != 403 {
		t.Errorf("token đúng nhưng thiếu scope fhircast/* phải 403, được %d %s", r.status, r.body)
	}
	if r := do(t, "GET", s.hubURL+"/anything", "token-gia", "", nil); r.status != 401 {
		t.Errorf("token giả phải 401, được %d", r.status)
	}
	if r := do(t, "GET", s.hubURL+"/.well-known/fhircast-configuration", "", "", nil); r.status != 200 {
		t.Errorf(".well-known phải công khai: %d", r.status)
	}
}

// Hai người dùng ở hai project khác nhau: topic khác nhau, và dù biết topic của người kia vẫn không thấy gì (cô lập project).
func TestFhircastProjectsAreIsolated(t *testing.T) {
	s := newCastStack(t)
	const scope = "openid fhircast/*"
	alice := s.login("alice", "a@example.org", s.clientA, scope)
	bob := s.login("bob", "b@example.org", s.clientB, scope)
	if alice.topic == bob.topic {
		t.Fatalf("hai người dùng không được chung topic: %q", alice.topic)
	}
	// Bob dùng topic CỦA ALICE (đã biết) nhưng token thuộc project khác
	spy := bob
	spy.topic = alice.topic
	conn, _ := spy.subscribe(s, "Patient-open")
	if r := alice.post(s, "", patientEvent(alice.topic, "Patient-open", "secret")); r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			break // hết hạn chờ mà không nhận sự kiện của project khác: đúng
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if ev, _ := m["event"].(map[string]any); ev != nil && ev["hub.event"] == "Patient-open" {
			t.Fatalf("Bob nhận được sự kiện của project khác: %s", b)
		}
	}
	if cur := do(t, "GET", s.hubURL+"/"+alice.topic, bob.token, "", nil).json(t); cur["context.type"] != "" {
		t.Errorf("Bob đọc được ngữ cảnh của project khác: %v", cur)
	}
}
