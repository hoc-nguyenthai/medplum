package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/ws-gateway/internal/presentation/httpapi"
	"trivita-fhir/pkg/fhircast"
)

var fcWS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fcws:" + r.URL.Path)) })

// Token giả: "tok:<project>:<subject>"; "bad" là 401; "noscope" là 403.
func fakeAuth(_ context.Context, tok string) (httpapi.Caller, error) {
	switch {
	case tok == "bad":
		return httpapi.Caller{}, httpapi.ErrUnauthorized
	case tok == "noscope":
		return httpapi.Caller{}, httpapi.ErrForbidden
	case tok == "weird":
		return httpapi.Caller{}, errors.New("redis://secret-host down")
	case strings.HasPrefix(tok, "tok:"):
		p := strings.Split(tok, ":")
		return httpapi.Caller{Project: p[1], Subject: p[2]}, nil
	}
	return httpapi.Caller{}, httpapi.ErrUnauthorized
}

type fcEnv struct {
	e     *echo.Echo
	store *fhircast.MemoryStore
	bus   *fhircast.MemoryBus
}

func newFC(t *testing.T, mut func(*httpapi.FhircastDeps)) *fcEnv {
	t.Helper()
	st, bus := fhircast.NewMemoryStore(fhircast.StoreOptions{}), fhircast.NewMemoryBus()
	d := &httpapi.FhircastDeps{Hub: &fhircast.Hub{Store: st, Bus: bus}, Authenticate: fakeAuth, WSBase: "ws://gw.test:8007", MaxBody: 64 << 10, WebSocket: fcWS}
	if mut != nil {
		mut(d)
	}
	e, err := httpapi.New(httpapi.Deps{WebSocket: wsHandler, Fhircast: d})
	if err != nil {
		t.Fatal(err)
	}
	return &fcEnv{e: e, store: st, bus: bus}
}

type resp struct {
	*httptest.ResponseRecorder
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatalf("không phải JSON object: %q", r.Body.String())
	}
	return m
}

func (r resp) text(t *testing.T) string { // issue[0].details.text của OperationOutcome
	t.Helper()
	m := r.json(t)
	if m["resourceType"] != "OperationOutcome" {
		t.Fatalf("không phải OperationOutcome: %v", m)
	}
	return m["issue"].([]any)[0].(map[string]any)["details"].(map[string]any)["text"].(string)
}

func (f *fcEnv) do(method, path, tok, ctype, body string) resp {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, req)
	return resp{rec}
}

func (f *fcEnv) postJSON(path, tok string, v any) resp {
	b, _ := json.Marshal(v)
	return f.do("POST", path, tok, "application/json", string(b))
}

const tokA = "tok:projA:alice"

var allPaths = []struct {
	prefix  string
	version fhircast.Version
}{{"/fhircast/STU2", fhircast.STU2}, {"/fhircast/STU3", fhircast.STU3}, {"/hub", fhircast.STU3}}

func subReq(topic, events string) map[string]any {
	return map[string]any{"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": topic, "hub.events": events}
}

func TestFhircastWellKnownIsPublic(t *testing.T) { // ca 1, 4
	f := newFC(t, nil)
	for _, p := range allPaths {
		r := f.do("GET", p.prefix+"/.well-known/fhircast-configuration", "", "", "")
		if r.Code != 200 {
			t.Fatalf("%s: %d", p.prefix, r.Code)
		}
		m := r.json(t)
		if m["websocketSupport"] != true || m["webhookSupport"] != false || m["fhircastVersion"] != string(p.version) || len(m["eventsSupported"].([]any)) != 14 {
			t.Errorf("%s: %v", p.prefix, m)
		}
		_, has := m["getCurrentSupport"]
		if has != (p.version == fhircast.STU3) {
			t.Errorf("%s: getCurrentSupport=%v", p.prefix, has)
		}
	}
}

func TestFhircastRequiresAuth(t *testing.T) { // ca 5, F-2
	f := newFC(t, nil)
	for _, p := range allPaths {
		for _, c := range []struct{ method, path string }{{"POST", p.prefix}, {"POST", p.prefix + "/"}, {"POST", p.prefix + "/t"}, {"GET", p.prefix + "/t"}} {
			r := f.do(c.method, c.path, "", "application/json", "{}")
			if r.Code != 401 || r.text(t) != "Unauthorized" || !strings.Contains(r.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("%s %s không token: %d %s", c.method, c.path, r.Code, r.Body)
			}
			if r := f.do(c.method, c.path, "bad", "application/json", "{}"); r.Code != 401 {
				t.Errorf("%s %s token sai: %d", c.method, c.path, r.Code)
			}
			if r := f.do(c.method, c.path, "noscope", "application/json", "{}"); r.Code != 403 || r.text(t) != "Forbidden" {
				t.Errorf("%s %s thiếu quyền: %d", c.method, c.path, r.Code)
			}
			r = f.do(c.method, c.path, "weird", "application/json", "{}")
			if r.Code != 500 || r.text(t) != "Internal server error" || strings.Contains(r.Body.String(), "secret") {
				t.Errorf("%s %s lỗi lạ phải 500 chung: %d %s", c.method, c.path, r.Code, r.Body)
			}
		}
	}
	// scheme khác Bearer
	req := httptest.NewRequest("GET", "/hub/t", nil)
	req.Header.Set("Authorization", "Basic "+tokA)
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("Basic: %d", rec.Code)
	}
}

func TestFhircastSubscribeEveryPathAndEncoding(t *testing.T) { // ca 2, 3, 10
	f := newFC(t, nil)
	for _, p := range allPaths {
		forms := url.Values{"hub.channel.type": {"websocket"}, "hub.mode": {"subscribe"}, "hub.topic": {"t1"}, "hub.events": {"Patient-open, Patient-close"}}
		rs := map[string]resp{
			"json": f.postJSON(p.prefix, tokA, subReq("t1", "Patient-open, Patient-close")),
			"form": f.do("POST", p.prefix, tokA, "application/x-www-form-urlencoded", forms.Encode()),
			"/":    f.postJSON(p.prefix+"/", tokA, subReq("t1", "Patient-open")),
		}
		seen := map[string]bool{}
		for name, r := range rs {
			if r.Code != 202 {
				t.Fatalf("%s %s: %d %s", p.prefix, name, r.Code, r.Body)
			}
			u := r.json(t)["hub.channel.endpoint"].(string)
			if !strings.HasPrefix(u, "ws://gw.test:8007/ws/fhircast/") || strings.Contains(u, "t1") || seen[u] {
				t.Errorf("%s %s: endpoint %q", p.prefix, name, u)
			}
			seen[u] = true
			sub, ok, _ := f.store.GetSubscription(context.Background(), fhircast.ExtractEndpoint(u))
			if !ok || sub.Version != p.version || sub.ProjectID != "projA" || sub.UserSubject != "alice" || sub.Topic != "t1" {
				t.Errorf("%s %s: đăng ký lưu %+v", p.prefix, name, sub)
			}
		}
	}
}

func TestFhircastSubscribeErrors(t *testing.T) { // ca 6-9, 15
	f := newFC(t, nil)
	cases := map[string]struct {
		body map[string]any
		msg  string
	}{
		"thiếu channel type": {map[string]any{"hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open"}, "Missing hub.channel.type"},
		"channel type lạ":    {map[string]any{"hub.channel.type": "webhook", "hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open"}, "Invalid hub.channel.type"},
		"mode lạ":            {map[string]any{"hub.channel.type": "websocket", "hub.mode": "x", "hub.topic": "t", "hub.events": "Patient-open"}, "Invalid hub.mode"},
		"events rỗng":        {subReq("t", " , "), "Invalid hub.events"},
		"thiếu events":       {map[string]any{"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "t"}, "Missing hub.events"},
		"JSON trống":         {map[string]any{}, "Missing hub.channel.type"},
	}
	for name, c := range cases {
		r := f.postJSON("/fhircast/STU3", tokA, c.body)
		if r.Code != 400 || r.text(t) != c.msg {
			t.Errorf("%s: %d %q, muốn %q", name, r.Code, r.Body, c.msg)
		}
	}
	// thân không phải JSON/form: coi như rỗng
	if r := f.do("POST", "/hub", tokA, "text/plain", "hub.mode=subscribe"); r.Code != 400 || r.text(t) != "Missing hub.channel.type" {
		t.Errorf("text/plain: %d %s", r.Code, r.Body)
	}
	if r := f.do("POST", "/hub", tokA, "application/json", "{not json"); r.Code != 400 || r.text(t) != "Invalid request body" {
		t.Errorf("JSON hỏng: %d %s", r.Code, r.Body)
	}
	if r := f.do("POST", "/hub", tokA, "application/json", "[1,2]"); r.Code != 400 {
		t.Errorf("JSON không phải object: %d", r.Code)
	}
}

func TestFhircastWebhookNeverCallsOut(t *testing.T) { // F-1
	var hits atomic.Int32
	cb := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer cb.Close()
	f := newFC(t, nil)
	for _, typ := range []string{"webhook", "rest-hook", "email", "", "WebSocket"} {
		body := subReq("t", "Patient-open")
		body["hub.channel.type"] = typ
		body["hub.callback"] = cb.URL
		body["hub.secret"] = "s"
		if r := f.postJSON("/fhircast/STU3", tokA, body); r.Code != 400 {
			t.Errorf("%q: %d", typ, r.Code)
		}
	}
	// ngay cả kênh websocket hợp lệ kèm hub.callback cũng không gây gọi ra
	body := subReq("t", "Patient-open")
	body["hub.callback"] = cb.URL
	if r := f.postJSON("/fhircast/STU3", tokA, body); r.Code != 202 {
		t.Fatalf("%d", r.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 0 {
		t.Errorf("máy chủ callback nhận %d request, muốn 0", hits.Load())
	}
}

func TestFhircastSubscribeStoreFailureIs500(t *testing.T) { // ca 12
	f := newFC(t, func(d *httpapi.FhircastDeps) { d.Hub.Store = failingPut{d.Hub.Store} })
	r := f.postJSON("/fhircast/STU3", tokA, subReq("t", "Patient-open"))
	if r.Code != 500 || r.text(t) != "Failed to create subscription for topic" || r.Header().Get("Content-Type") != "application/fhir+json" {
		t.Errorf("%d %s %s", r.Code, r.Body, r.Header().Get("Content-Type"))
	}
}

type failingPut struct{ fhircast.Store }

func (failingPut) PutSubscription(context.Context, string, fhircast.Subscription, time.Duration) error {
	return errors.New("redis down")
}

func TestFhircastUnsubscribeOverHTTP(t *testing.T) { // ca 13, 20, 21, 23
	f := newFC(t, nil)
	u := f.postJSON("/hub", tokA, subReq("t", "Patient-open")).json(t)["hub.channel.endpoint"].(string)
	un := func(tok string, body map[string]any) resp { return f.postJSON("/hub", tok, body) }
	base := map[string]any{"hub.channel.type": "websocket", "hub.mode": "unsubscribe", "hub.topic": "t"}
	with := func(k, v string) map[string]any {
		m := map[string]any{}
		for kk, vv := range base {
			m[kk] = vv
		}
		m[k] = v
		return m
	}

	if r := un(tokA, base); r.Code != 400 || r.text(t) != "Missing endpoint" {
		t.Errorf("thiếu endpoint: %d %s", r.Code, r.Body)
	}
	if r := un(tokA, with("hub.channel.endpoint", "ws://x/ws/fhircast/unknown")); r.Code != 400 || r.text(t) != "Invalid endpoint" {
		t.Errorf("endpoint lạ: %d %s", r.Code, r.Body)
	}
	if r := un("tok:projB:alice", with("hub.channel.endpoint", u)); r.Code != 400 || r.text(t) != "Invalid endpoint" {
		t.Errorf("project khác: %d %s", r.Code, r.Body)
	}
	if _, ok, _ := f.store.GetSubscription(context.Background(), fhircast.ExtractEndpoint(u)); !ok {
		t.Fatal("hủy sai không được xóa đăng ký")
	}
	r := un(tokA, with("hub.channel.endpoint", u+"?x=1"))
	if r.Code != 202 || r.json(t)["hub.channel.endpoint"] != u {
		t.Errorf("hủy: %d %s (muốn gương %s)", r.Code, r.Body, u)
	}
	if _, ok, _ := f.store.GetSubscription(context.Background(), fhircast.ExtractEndpoint(u)); ok {
		t.Error("đăng ký phải biến mất")
	}
	// client cũ dùng trường endpoint (ca 17) và dạng form (ca 16)
	u2 := f.postJSON("/hub", tokA, subReq("t", "Patient-open")).json(t)["hub.channel.endpoint"].(string)
	form := url.Values{"hub.channel.type": {"websocket"}, "hub.mode": {"unsubscribe"}, "hub.topic": {"t"}, "endpoint": {u2}}
	if r := f.do("POST", "/hub", tokA, "application/x-www-form-urlencoded", form.Encode()); r.Code != 202 || r.json(t)["hub.channel.endpoint"] != u2 {
		t.Errorf("trường cũ dạng form: %d %s", r.Code, r.Body)
	}
}

func evt(topic, name string, items ...any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"id": "m1", "timestamp": "2026-01-01T00:00:00Z",
		"event": map[string]any{"hub.topic": topic, "hub.event": name, "context": items}}
}

func patientItem(id string) map[string]any {
	return map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": id}}
}

func TestFhircastContextChangeAndGet(t *testing.T) { // ca 25, 28-31
	f := newFC(t, nil)
	for _, p := range allPaths {
		topic := "topic-" + strings.ReplaceAll(p.prefix, "/", "")
		get := func(tok string) resp { return f.do("GET", p.prefix+"/"+topic, tok, "", "") }

		empty := get(tokA)
		if p.version == fhircast.STU3 {
			if m := empty.json(t); m["context.type"] != "" || len(m["context"].([]any)) != 0 {
				t.Errorf("%s: trống STU3: %v", p.prefix, m)
			}
		} else if strings.TrimSpace(empty.Body.String()) != "[]" {
			t.Errorf("%s: trống STU2 phải là []: %s", p.prefix, empty.Body)
		}

		r := f.postJSON(p.prefix, tokA, evt(topic, "Patient-open", patientItem("p1")))
		if r.Code != 202 || r.json(t)["success"] != true {
			t.Fatalf("%s: %d %s", p.prefix, r.Code, r.Body)
		}
		v := r.json(t)["event"].(map[string]any)["event"].(map[string]any)["context.versionId"].(string)
		g := get(tokA)
		if p.version == fhircast.STU3 {
			m := g.json(t)
			if m["context.type"] != "Patient" || m["context.versionId"] != v || len(m["context"].([]any)) != 1 {
				t.Errorf("%s: GET STU3: %v", p.prefix, m)
			}
		} else {
			var arr []any
			_ = json.Unmarshal(g.Body.Bytes(), &arr)
			if len(arr) != 1 {
				t.Errorf("%s: GET STU2 phải là mảng context: %s", p.prefix, g.Body)
			}
		}
		// project khác không thấy, và ghi độc lập
		if o := get("tok:projB:bob"); strings.Contains(o.Body.String(), "Patient") {
			t.Errorf("%s: project khác thấy ngữ cảnh: %s", p.prefix, o.Body)
		}
		// cùng đổi qua /{topic}
		r = f.postJSON(p.prefix+"/"+topic, tokA, evt(topic, "Patient-close", patientItem("p1")))
		if r.Code != 202 {
			t.Fatalf("%s /topic: %d %s", p.prefix, r.Code, r.Body)
		}
		if strings.Contains(get(tokA).Body.String(), "Patient") {
			t.Errorf("%s: close phải xóa", p.prefix)
		}
	}
}

func TestFhircastEventValidationOverHTTP(t *testing.T) { // ca 24, 27
	f := newFC(t, nil)
	cases := map[string]struct {
		mut func(map[string]any)
		msg string
	}{
		"thiếu id":        {func(m map[string]any) { delete(m, "id") }, "Missing event ID"},
		"thiếu timestamp": {func(m map[string]any) { delete(m, "timestamp") }, "Missing event timestamp"},
		"thiếu event":     {func(m map[string]any) { delete(m, "event") }, "Missing event payload"},
		"thiếu topic":     {func(m map[string]any) { delete(m["event"].(map[string]any), "hub.topic") }, `Missing event["hub.topic"]`},
		"thiếu hub.event": {func(m map[string]any) { delete(m["event"].(map[string]any), "hub.event") }, `Missing event["hub.event"]`},
		"thiếu context":   {func(m map[string]any) { delete(m["event"].(map[string]any), "context") }, "Missing event.context"},
	}
	for name, c := range cases {
		for _, path := range []string{"/fhircast/STU3", "/fhircast/STU3/t"} {
			m := evt("t", "Patient-open", patientItem("p"))
			c.mut(m)
			if r := f.postJSON(path, tokA, m); r.Code != 400 || r.text(t) != c.msg {
				t.Errorf("%s %s: %d %s", name, path, r.Code, r.Body)
			}
		}
	}
}

func TestFhircastTopicMismatchOnPath(t *testing.T) { // ca 26, Q-FC-6
	f := newFC(t, nil)
	r := f.postJSON("/hub/path-topic", tokA, evt("event-topic", "Patient-open", patientItem("p")))
	if r.Code != 400 || r.text(t) != "Topic mismatch" {
		t.Errorf("%d %s", r.Code, r.Body)
	}
	if r := f.do("GET", "/hub/event-topic", tokA, "", ""); strings.Contains(r.Body.String(), "Patient") {
		t.Error("topic lệch không được đổi ngữ cảnh")
	}
	if r := f.postJSON("/hub/event-topic", tokA, evt("event-topic", "Patient-open", patientItem("p"))); r.Code != 202 {
		t.Errorf("topic khớp: %d", r.Code)
	}
	// cờ nới lỏng: như Medplum, bỏ qua topic ở đường dẫn
	g := newFC(t, func(d *httpapi.FhircastDeps) { d.AllowTopicMismatch = true })
	if r := g.postJSON("/hub/path-topic", tokA, evt("event-topic", "Patient-open", patientItem("p"))); r.Code != 202 {
		t.Errorf("AllowTopicMismatch: %d %s", r.Code, r.Body)
	}
	if r := g.do("GET", "/hub/event-topic", tokA, "", ""); !strings.Contains(r.Body.String(), "Patient") {
		t.Error("phải ghi vào event[hub.topic]")
	}
}

func TestFhircastTopicsWithSpecialCharacters(t *testing.T) {
	f := newFC(t, nil)
	// topic có "/", "%", dấu cách, và chuỗi trông như đã mã hóa: phải giải mã đúng một lần, mỗi topic một ngữ cảnh riêng
	topics := []string{"a:b/c d", "100%", "a%2Fb", "x%25y", "ünï"}
	for i, topic := range topics {
		id := string(rune('a' + i))
		if r := f.postJSON("/hub", tokA, evt(topic, "Patient-open", patientItem(id))); r.Code != 202 {
			t.Fatalf("%q: %d %s", topic, r.Code, r.Body)
		}
	}
	for i, topic := range topics {
		id := string(rune('a' + i))
		r := f.do("GET", "/hub/"+url.PathEscape(topic), tokA, "", "")
		if !strings.Contains(r.Body.String(), `"id":"`+id+`"`) {
			t.Errorf("GET %q: %d %s", topic, r.Code, r.Body)
		}
	}
}

func TestFhircastBodyLimit(t *testing.T) { // F-7
	f := newFC(t, func(d *httpapi.FhircastDeps) { d.MaxBody = 200 })
	big := evt("t", "Patient-open", map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": "p", "note": strings.Repeat("x", 500)}})
	for _, path := range []string{"/hub", "/hub/t"} {
		if r := f.postJSON(path, tokA, big); r.Code != 413 || r.text(t) != "Request body too large" {
			t.Errorf("%s json: %d %s", path, r.Code, r.Body)
		}
	}
	if r := f.do("POST", "/hub", tokA, "application/x-www-form-urlencoded", "hub.topic="+strings.Repeat("a", 500)); r.Code != 413 || r.text(t) != "Request body too large" {
		t.Errorf("form: %d %s", r.Code, r.Body) // r.text đòi thân là MỘT OperationOutcome: phản hồi ghi đôi sẽ hỏng JSON
	}
	if r := f.do("POST", "/hub", tokA, "application/x-www-form-urlencoded", "%zz=%"); r.Code != 400 || r.text(t) != "Invalid request body" {
		t.Errorf("form hỏng: %d %s", r.Code, r.Body)
	}
}

func TestFhircastDisabledAndMisconfigured(t *testing.T) {
	e, err := httpapi.New(httpapi.Deps{WebSocket: wsHandler})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allPaths {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest("GET", p.prefix+"/.well-known/fhircast-configuration", nil))
		if rec.Code != 404 {
			t.Errorf("tắt thì route không được tồn tại: %s %d", p.prefix, rec.Code)
		}
	}
	good := httpapi.FhircastDeps{Hub: &fhircast.Hub{}, Authenticate: fakeAuth, WSBase: "ws://x", MaxBody: 1, WebSocket: fcWS}
	for name, mut := range map[string]func(*httpapi.FhircastDeps){
		"thiếu Hub": func(d *httpapi.FhircastDeps) { d.Hub = nil }, "thiếu Authenticate": func(d *httpapi.FhircastDeps) { d.Authenticate = nil },
		"thiếu WSBase": func(d *httpapi.FhircastDeps) { d.WSBase = "" }, "thiếu WebSocket": func(d *httpapi.FhircastDeps) { d.WebSocket = nil }, "MaxBody 0": func(d *httpapi.FhircastDeps) { d.MaxBody = 0 },
	} {
		d := good
		mut(&d)
		if _, err := httpapi.New(httpapi.Deps{WebSocket: wsHandler, Fhircast: &d}); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
}

func TestFhircastDoesNotBypassWebSocketAndHealth(t *testing.T) {
	f := newFC(t, nil)
	if r := f.do("GET", "/health/live", "", "", ""); r.Code != 200 {
		t.Errorf("health: %d", r.Code)
	}
	if r := f.do("GET", httpapi.WebSocketPath, "", "", ""); r.Body.String() != "ws" {
		t.Errorf("websocket subscription: %q", r.Body)
	}
}

func TestFhircastWebSocketRoute(t *testing.T) {
	f := newFC(t, nil)
	for path, want := range map[string]string{
		"/ws/fhircast/abc":   "fcws:/ws/fhircast/abc",
		"/ws/fhircast/":      "fcws:/ws/fhircast/",
		"/ws/fhircast":       "fcws:/ws/fhircast",
		"/ws/fhircast/a/b/c": "fcws:/ws/fhircast/a/b/c",
	} {
		if r := f.do("GET", path, "", "", ""); r.Body.String() != want { // không cần Bearer: endpoint là bí mật
			t.Errorf("%s: %q, muốn %q", path, r.Body, want)
		}
	}
	// Subscription websocket vẫn ở đường dẫn riêng
	if r := f.do("GET", httpapi.WebSocketPath, "", "", ""); r.Body.String() != "ws" {
		t.Errorf("websocket subscription: %q", r.Body)
	}
}

func TestFhircastWebSocketIsRateLimited(t *testing.T) { // cùng hạn mức mở kết nối với websocket Subscription
	d := &httpapi.FhircastDeps{Hub: &fhircast.Hub{}, Authenticate: fakeAuth, WSBase: "ws://x", MaxBody: 1, WebSocket: fcWS}
	e, err := httpapi.New(httpapi.Deps{WebSocket: wsHandler, Fhircast: d, RateLimit: 1, RateBurst: 2})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[int]int{}
	for i := 0; i < 8; i++ {
		req := httptest.NewRequest("GET", "/ws/fhircast/abc", nil)
		req.RemoteAddr = "192.0.2.77:1"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		codes[rec.Code]++
	}
	if codes[200] != 2 || codes[429] != 6 {
		t.Errorf("mong đợi 2 qua và 6 bị 429: %v", codes)
	}
}
