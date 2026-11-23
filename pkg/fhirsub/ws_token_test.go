package fhirsub_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
)

func key(c byte) []byte { return []byte(strings.Repeat(string(c), 40)) }

func mustTokens(t *testing.T, cfg fhirsub.TokenConfig) *fhirsub.Tokens {
	t.Helper()
	tk, err := fhirsub.NewTokens(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// issueWith cấp token bằng bộ cấp tk (không phải Hub) cho Subscription id trong kho của w, với tư cách p (nil: không phân quyền).
func (w *wsEnv) issueWith(tk *fhirsub.Tokens, id string, p *fhirauth.Principal) string {
	w.t.Helper()
	ctx := context.Background()
	if p != nil {
		ctx = fhirauth.WithPrincipal(ctx, p)
	}
	res, err := tk.TokenOperation(w.mem)(ctx, &fhirrouter.Request{Params: map[string]string{"id": id}})
	if err != nil {
		w.t.Fatalf("token: %v", err)
	}
	for _, q := range res.Resource.(*fhir.Parameters).Parameter {
		if q.Name == "token" {
			return q.ValueString
		}
	}
	w.t.Fatal("không có token")
	return ""
}

// bindWith bind bằng token đã có; trả mã đóng (0 nếu bind thành công) và client.
func (w *wsEnv) bindWith(token string) (websocket.StatusCode, *client) {
	w.t.Helper()
	c := w.dial()
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(token))
	b, err := read(w.t, c, 2*time.Second)
	if err == nil {
		if statusOf(b)["type"] != "handshake" {
			w.t.Fatalf("không phải handshake: %v", b)
		}
		return 0, c
	}
	return closeCode(err), c
}

// handToken tự dựng token theo đúng định dạng dây (ghim hợp đồng giữa bên cấp và bên nhận ở hai tiến trình khác nhau).
func handToken(k []byte, claims map[string]any) string {
	raw, _ := json.Marshal(claims)
	m := hmac.New(sha256.New, k)
	m.Write([]byte("fhir-ws-binding|"))
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func claimsOf(t *testing.T, token string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewTokensValidatesKeys(t *testing.T) {
	short := []byte("short")
	for name, cfg := range map[string]fhirsub.TokenConfig{
		"không có khóa":     {},
		"khóa ngắn":         {SigningKey: short},
		"khóa cũ ngắn":      {SigningKey: key('a'), Previous: [][]byte{short}},
		"quá nhiều khóa cũ": {SigningKey: key('a'), Previous: [][]byte{key('b'), key('c'), key('d'), key('e')}},
	} {
		if _, err := fhirsub.NewTokens(cfg); err == nil {
			t.Errorf("%s: phải bị từ chối", name)
		}
	}
	if _, err := fhirsub.NewHub(fhirsub.WSConfig{}); err == nil {
		t.Error("Hub không có khóa và không có Tokens phải bị từ chối")
	}
	if _, err := fhirsub.NewHub(fhirsub.WSConfig{SigningKey: key('a'), PreviousKeys: [][]byte{[]byte("short")}}); err == nil {
		t.Error("khóa cũ ngắn ở Hub phải bị từ chối")
	}
}

func TestIssuedTokenCarriesVersionAndExpiry(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	now := time.Now()
	tk := mustTokens(t, fhirsub.TokenConfig{SigningKey: key('a'), TTL: 2 * time.Minute, Now: func() time.Time { return now }, URL: "wss://gw.example.org/ws/subscriptions-r4"})
	token := w.issueWith(tk, "s1", &fhirauth.Principal{Subject: "alice"})
	c := claimsOf(t, token)
	if c["v"] != float64(fhirsub.TokenVersion) || c["sub"] != "s1" || c["exp"] != float64(now.Add(2*time.Minute).Unix()) || c["jti"] == "" || c["snap"] == nil {
		t.Errorf("claims: %v", c)
	}
	// websocket-url trả về là của bên cấp (gateway), không phải của fhir-server
	res, err := tk.TokenOperation(w.mem)(context.Background(), &fhirrouter.Request{Params: map[string]string{"id": "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	var wsURL string
	for _, p := range res.Resource.(*fhir.Parameters).Parameter {
		if p.Name == "websocket-url" {
			wsURL = p.ValueUrl
		}
	}
	if wsURL != "wss://gw.example.org/ws/subscriptions-r4" {
		t.Errorf("websocket-url = %q", wsURL)
	}
}

// Bên cấp và Hub là hai đối tượng khác nhau (như hai tiến trình) chỉ chung khóa: bind được, quyền chụp trong token vẫn được áp.
func TestHubAcceptsTokensFromADifferentIssuerSharingTheKey(t *testing.T) {
	verifier := mustTokens(t, fhirsub.TokenConfig{SigningKey: key('a')})
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.Tokens = verifier })
	w.subscribe("s1", "Observation")
	issuer := mustTokens(t, fhirsub.TokenConfig{SigningKey: key('a')})

	code, ok := w.bindWith(w.issueWith(issuer, "s1", &fhirauth.Principal{Subject: "a"}))
	if code != 0 {
		t.Fatalf("bind: %v", code)
	}
	code, denied := w.bindWith(w.issueWith(issuer, "s1", &fhirauth.Principal{Subject: "b", Grants: fhirauth.Grants{"Patient": fhirauth.PermRead}}))
	if code != 0 {
		t.Fatalf("bind: %v", code)
	}
	w.create(wsObs("o1"))
	if b, err := read(t, ok, 2*time.Second); err != nil || statusOf(b)["type"] != "event-notification" {
		t.Fatalf("phải nhận: %v %v", b, err)
	}
	wantNothing(t, denied, "quyền chụp trong token do bên cấp khác ký vẫn được áp")

	// khóa khác thì không bind được
	stranger := mustTokens(t, fhirsub.TokenConfig{SigningKey: key('z')})
	if code, _ := w.bindWith(w.issueWith(stranger, "s1", nil)); code != websocket.StatusPolicyViolation {
		t.Errorf("khóa lạ: mã đóng %v", code)
	}
}

// Xoay khóa không ngắt ai: Hub nhận cả khóa hiện hành lẫn khóa trước; bỏ khóa trước thì token ký bằng nó bị từ chối.
func TestSigningKeyRotation(t *testing.T) {
	oldKey, newKey := key('o'), key('n')
	issueOld := mustTokens(t, fhirsub.TokenConfig{SigningKey: oldKey})
	issueNew := mustTokens(t, fhirsub.TokenConfig{SigningKey: newKey})

	// bước 1: bên nhận biết khóa mới, giữ khóa cũ ở Previous; bên cấp còn khóa cũ
	rotating := mustTokens(t, fhirsub.TokenConfig{SigningKey: newKey, Previous: [][]byte{oldKey}})
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.Tokens = rotating })
	w.subscribe("s1", "Observation")
	if code, _ := w.bindWith(w.issueWith(issueOld, "s1", nil)); code != 0 {
		t.Errorf("token ký bằng khóa cũ phải còn dùng được trong lúc xoay: %v", code)
	}
	// bước 2: bên cấp đổi sang khóa mới
	if code, _ := w.bindWith(w.issueWith(issueNew, "s1", nil)); code != 0 {
		t.Errorf("token ký bằng khóa mới: %v", code)
	}
	// khóa thứ ba (không nằm trong danh sách) bị từ chối
	if code, _ := w.bindWith(w.issueWith(mustTokens(t, fhirsub.TokenConfig{SigningKey: key('x')}), "s1", nil)); code == 0 {
		t.Error("khóa lạ phải bị từ chối")
	}

	// bước 3: bỏ khóa cũ
	w2 := newWSEnv(t, func(c *fhirsub.WSConfig) { c.Tokens = mustTokens(t, fhirsub.TokenConfig{SigningKey: newKey}) })
	w2.subscribe("s1", "Observation")
	if code, _ := w2.bindWith(w2.issueWith(issueOld, "s1", nil)); code != websocket.StatusPolicyViolation {
		t.Errorf("sau khi bỏ khóa cũ, token cũ phải bị từ chối: %v", code)
	}
	if code, _ := w2.bindWith(w2.issueWith(issueNew, "s1", nil)); code != 0 {
		t.Errorf("token khóa mới: %v", code)
	}
}

// Cấu hình trực tiếp qua WSConfig.PreviousKeys (đường fhir-server nhúng dùng) cũng xoay được.
func TestHubPreviousKeysViaConfig(t *testing.T) {
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.SigningKey = key('n'); c.PreviousKeys = [][]byte{key('o')} })
	w.subscribe("s1", "Observation")
	if code, _ := w.bindWith(w.issueWith(mustTokens(t, fhirsub.TokenConfig{SigningKey: key('o')}), "s1", nil)); code != 0 {
		t.Errorf("khóa cũ: %v", code)
	}
}

func TestTokenVersioning(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	exp := time.Now().Add(time.Minute).Unix()
	base := func(v any) map[string]any {
		m := map[string]any{"sub": "s1", "proj": "", "exp": exp, "jti": "j"}
		if v != nil {
			m["v"] = v
		}
		return m
	}
	// token của bản trước WS-3 (không có v) vẫn được nhận
	if code, _ := w.bindWith(handToken(wsKey, base(nil))); code != 0 {
		t.Errorf("không có v: %v", code)
	}
	if code, _ := w.bindWith(handToken(wsKey, base(1))); code != 0 {
		t.Errorf("v=1: %v", code)
	}
	// phiên bản mới hơn mà bên nhận chưa biết: từ chối thay vì đọc sai
	if code, _ := w.bindWith(handToken(wsKey, base(fhirsub.TokenVersion+1))); code != websocket.StatusPolicyViolation {
		t.Errorf("phiên bản lạ: %v", code)
	}
	// chữ ký phải đúng dù phiên bản hợp lệ
	if code, _ := w.bindWith(handToken(key('z'), base(1))); code != websocket.StatusPolicyViolation {
		t.Errorf("chữ ký sai: %v", code)
	}
	// thiếu sub
	m := base(1)
	delete(m, "sub")
	if code, _ := w.bindWith(handToken(wsKey, m)); code != websocket.StatusPolicyViolation {
		t.Errorf("thiếu sub: %v", code)
	}
}

// Tiền tố miền: chữ ký trên cùng claims nhưng không có tiền tố "fhir-ws-binding|" (ví dụ HMAC dùng cho việc khác) bị từ chối.
func TestTokenSignatureIsDomainSeparated(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	raw, _ := json.Marshal(map[string]any{"v": 1, "sub": "s1", "proj": "", "exp": time.Now().Add(time.Minute).Unix(), "jti": "j"})
	m := hmac.New(sha256.New, wsKey)
	m.Write(raw) // không có tiền tố
	tok := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if code, _ := w.bindWith(tok); code != websocket.StatusPolicyViolation {
		t.Errorf("thiếu tiền tố miền: %v", code)
	}
}
