package ws_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/fhircast"
	"trivita-fhir/pkg/fhircast/ws"
)

type env struct {
	t     *testing.T
	store fhircast.Store
	bus   fhircast.Bus
	hub   *fhircast.Hub
	srv   *ws.Server
	http  *httptest.Server
}

// forEachBackend chạy fn trên bộ nhớ và Redis (miniredis, hoặc Redis THẬT nếu đặt FHIRCAST_TEST_REDIS_ADDR).
func forEachBackend(t *testing.T, mut func(*ws.Config), fn func(t *testing.T, e *env)) {
	for _, name := range []string{"memory", "redis"} {
		t.Run(name, func(t *testing.T) {
			var store fhircast.Store
			var bus fhircast.Bus
			if name == "memory" {
				store, bus = fhircast.NewMemoryStore(fhircast.StoreOptions{}), fhircast.NewMemoryBus()
			} else {
				addr := ""
				if v := envAddr(); v != "" {
					addr = v
				} else {
					addr = miniredis.RunT(t).Addr()
				}
				c := goredis.NewClient(&goredis.Options{Addr: addr})
				_ = c.FlushDB(context.Background()).Err()
				rb := fhircast.NewRedisBus(c)
				t.Cleanup(func() { _ = rb.Close(); _ = c.FlushDB(context.Background()).Err(); _ = c.Close() })
				store, bus = fhircast.NewRedisStore(c, "", fhircast.StoreOptions{}), rb
			}
			cfg := ws.Config{Store: store, Bus: bus, Heartbeat: time.Hour, CloseTimeout: 500 * time.Millisecond}
			if mut != nil {
				mut(&cfg)
			}
			srv, err := ws.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e := &env{t: t, store: store, bus: bus, hub: &fhircast.Hub{Store: store, Bus: bus}, srv: srv, http: httptest.NewServer(srv.Handler())}
			t.Cleanup(func() {
				e.http.CloseClientConnections()
				e.http.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = srv.Close(ctx)
			})
			fn(t, e)
		})
	}
}

func (e *env) subscribe(project, user string, v fhircast.Version, topic, events string) string {
	e.t.Helper()
	u, err := e.hub.Subscribe(context.Background(), project, user, v, "ws://x", fhircast.SubscribeRequest{ChannelType: "websocket", Mode: "subscribe", Topic: topic, Events: events})
	if err != nil {
		e.t.Fatal(err)
	}
	return fhircast.ExtractEndpoint(u)
}

func (e *env) wsURL(endpoint string) string {
	return "ws" + strings.TrimPrefix(e.http.URL, "http") + "/ws/fhircast/" + endpoint
}

// cl là client có goroutine đọc nền: Read có timeout của thư viện đóng luôn kết nối, nên "không nhận gì" phải đo bằng hàng đợi.
type cl struct {
	*websocket.Conn
	ch chan res
}

type res struct {
	typ websocket.MessageType
	b   []byte
	err error
}

func newCl(c *websocket.Conn) *cl {
	k := &cl{Conn: c, ch: make(chan res, 1024)}
	go func() {
		for {
			typ, b, err := c.Read(context.Background())
			k.ch <- res{typ, b, err}
			if err != nil {
				return
			}
		}
	}()
	return k
}

func (e *env) dial(endpoint string) *cl {
	e.t.Helper()
	c, _, err := websocket.Dial(ctx(), e.wsURL(endpoint), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.CloseNow() })
	return newCl(c)
}

func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancel
	return c
}

func read(t *testing.T, c *cl) map[string]any {
	t.Helper()
	select {
	case r := <-c.ch:
		if r.err != nil {
			t.Fatalf("đọc: %v", r.err)
		}
		if r.typ != websocket.MessageText {
			t.Fatalf("khung phải là văn bản (FHIRcast dùng JSON văn bản), được %v", r.typ)
		}
		var m map[string]any
		if err := json.Unmarshal(r.b, &m); err != nil {
			t.Fatalf("không phải JSON: %q", r.b)
		}
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("không nhận được khung nào")
		return nil
	}
}

func expectNone(t *testing.T, c *cl, d time.Duration) {
	t.Helper()
	select {
	case r := <-c.ch:
		t.Fatalf("không được nhận: %s %v", r.b, r.err)
	case <-time.After(d):
	}
}

func closeCode(t *testing.T, c *cl) websocket.StatusCode {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		select {
		case r := <-c.ch:
			if r.err == nil {
				continue // khung dữ liệu còn sót
			}
			if s := websocket.CloseStatus(r.err); s != -1 {
				return s
			}
			t.Fatalf("không phải khung đóng: %v", r.err)
		case <-deadline:
			t.Fatal("socket vẫn mở")
		}
	}
}

func patientOpen(topic, id string) map[string]any {
	return map[string]any{"id": "m-" + id, "timestamp": "2026-01-01T00:00:00Z", "event": map[string]any{
		"hub.topic": topic, "hub.event": "Patient-open", "context": []any{map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": id}}}}}
}

func (e *env) change(project string, m map[string]any) {
	e.t.Helper()
	if _, err := e.hub.ChangeContext(context.Background(), project, m); err != nil {
		e.t.Fatal(err)
	}
}

func eventName(m map[string]any) string {
	ev, _ := m["event"].(map[string]any)
	n, _ := ev["hub.event"].(string)
	return n
}

// ---- ca 43, 45-48, 52

func TestConfirmationAndDelivery(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		ep := e.subscribe("p", "u", fhircast.STU3, "t1", "Patient-open, Patient-close ")
		c := e.dial(ep)
		m := read(t, c)
		want := map[string]any{"hub.mode": "subscribe", "hub.topic": "t1", "hub.events": "Patient-open,Patient-close", "hub.lease_seconds": float64(3600)}
		if len(m) != 4 {
			t.Errorf("STU3 phải đúng 4 trường: %v", m)
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("%s = %v, muốn %v", k, m[k], v)
			}
		}
		e.change("p", patientOpen("t1", "p1")) // ca 43
		got := read(t, c)
		if eventName(got) != "Patient-open" || got["event"].(map[string]any)["context.versionId"] == nil {
			t.Errorf("sự kiện: %v", got)
		}
		if _, leaked := got["target"]; leaked {
			t.Error("target không được lên dây")
		}

		// STU2 giữ bốn trường rỗng (ca 46)
		c2 := e.dial(e.subscribe("p", "u", fhircast.STU2, "t1", "Patient-open"))
		m2 := read(t, c2)
		if len(m2) != 8 {
			t.Errorf("STU2 phải 8 trường: %v", m2)
		}
		for _, k := range []string{"hub.callback", "hub.channel", "hub.secret", "hub.subscriber"} {
			if v, ok := m2[k]; !ok || v != "" {
				t.Errorf("STU2 thiếu %s rỗng", k)
			}
		}
		// query string trên endpoint (ca 52)
		qraw, _, err := websocket.Dial(ctx(), e.wsURL(ep)+"?token=xyz", nil)
		if err != nil {
			t.Fatal(err)
		}
		qc := newCl(qraw)
		defer qc.CloseNow()
		if read(t, qc)["hub.topic"] != "t1" {
			t.Error("query string phải bị bỏ")
		}
	})
}

func TestOnlySubscribedEventsAndCaseInsensitive(t *testing.T) { // ca 44, 49, 50
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		c := e.dial(e.subscribe("p", "u", fhircast.STU3, "t", "patient-OPEN,DiagnosticReport-select"))
		read(t, c)
		e.change("p", patientOpen("t", "a")) // đăng ký chữ thường: vẫn nhận
		if eventName(read(t, c)) != "Patient-open" {
			t.Fatal("khớp không phân biệt hoa thường")
		}
		close1 := patientOpen("t", "a")
		close1["event"].(map[string]any)["hub.event"] = "Patient-close"
		e.change("p", close1) // chưa đăng ký: không nhận
		sel := patientOpen("t", "a")
		sel["event"].(map[string]any)["hub.event"] = "DiagnosticReport-select"
		e.change("p", sel)
		if got := read(t, c); eventName(got) != "DiagnosticReport-select" {
			t.Errorf("Patient-close không được tới, được %v", got)
		}
		se := patientOpen("t", "a")
		se["event"].(map[string]any)["hub.event"] = "syncerror"
		e.change("p", se) // syncerror luôn tới dù không đăng ký
		if eventName(read(t, c)) != "syncerror" {
			t.Error("syncerror luôn phải tới")
		}
	})
}

func TestConfirmationIsAlwaysFirst(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		ep := e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open")
		for i := 0; i < 20; i++ {
			raw, _, err := websocket.Dial(ctx(), e.wsURL(ep), nil)
			if err != nil {
				t.Fatal(err)
			}
			c := newCl(raw)
			for j := 0; j < 5; j++ { // phát ngay sau khi upgrade, có thể trước hoặc sau lúc server nghe
				e.change("p", patientOpen("t", "x"))
			}
			if first := read(t, c); first["hub.mode"] != "subscribe" {
				t.Fatalf("khung đầu tiên phải là xác nhận: %v", first)
			}
			_ = c.CloseNow()
		}
	})
}

// ---- ca 53

func TestInvalidEndpoint(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		for _, path := range []string{"/ws/fhircast/does-not-exist", "/ws/fhircast/", "/ws/fhircast"} {
			raw, _, err := websocket.Dial(ctx(), "ws"+strings.TrimPrefix(e.http.URL, "http")+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			c := newCl(raw)
			m := read(t, c)
			if m["hub.mode"] != "denied" || m["hub.topic"] != "" || m["hub.events"] != "" || m["hub.reason"] != "invalid endpoint" {
				t.Errorf("%s: %v", path, m)
			}
			if code := closeCode(t, c); code != websocket.StatusNormalClosure {
				t.Errorf("%s: đóng %d", path, code)
			}
		}
		// đăng ký đã hết hạn (đồng hồ của Server đã qua ExpiresAt) cũng là endpoint sai
		ep := fhircast.NewID()
		_ = e.store.PutSubscription(context.Background(), ep, fhircast.Subscription{ProjectID: "p", Topic: "t", Events: []string{"Patient-open"}, Version: fhircast.STU3, ExpiresAt: time.Now().Add(-time.Second)}, time.Minute)
		c := e.dial(ep)
		if read(t, c)["hub.reason"] != "invalid endpoint" {
			t.Error("đăng ký quá hạn phải bị từ chối")
		}
	})
}

// ---- hủy đăng ký: denied chỉ cho đúng socket rồi đóng (ca 13, 22, F-6)

func TestUnsubscribeDeniesOnlyThatSocket(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		a := e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open")
		b := e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open")
		ca, cb := e.dial(a), e.dial(b)
		read(t, ca)
		read(t, cb)
		if _, err := e.hub.Subscribe(context.Background(), "p", "u", fhircast.STU3, "ws://x", fhircast.SubscribeRequest{
			ChannelType: "websocket", Mode: "unsubscribe", Topic: "t", ChannelEndpoint: "ws://x/ws/fhircast/" + a}); err != nil {
			t.Fatal(err)
		}
		m := read(t, ca)
		if m["hub.mode"] != "denied" || m["hub.reason"] != "Subscriber unsubscribed from topic" || m["hub.topic"] != "t" {
			t.Errorf("denied: %v", m)
		}
		if code := closeCode(t, ca); code != websocket.StatusNormalClosure {
			t.Errorf("sau denied phải đóng 1000, được %d", code)
		}
		expectNone(t, cb, 200*time.Millisecond) // người kia không nhận denied
		e.change("p", patientOpen("t", "x"))
		if eventName(read(t, cb)) != "Patient-open" {
			t.Error("subscriber còn lại phải vẫn nhận sự kiện")
		}
		// kết nối lại vào endpoint đã hủy: endpoint sai
		c := e.dial(a)
		if read(t, c)["hub.reason"] != "invalid endpoint" {
			t.Error("endpoint đã hủy phải bị từ chối")
		}
	})
}

// ---- cô lập project (F-3)

func TestProjectIsolation(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		c := e.dial(e.subscribe("A", "u", fhircast.STU3, "same-topic", "Patient-open"))
		read(t, c)
		e.change("B", patientOpen("same-topic", "b"))
		expectNone(t, c, 250*time.Millisecond)
		e.change("A", patientOpen("same-topic", "a"))
		if eventName(read(t, c)) != "Patient-open" {
			t.Error("project A phải nhận sự kiện của chính nó")
		}
	})
}

// ---- heartbeat cục bộ (ca 54, 55)

func TestHeartbeat(t *testing.T) {
	forEachBackend(t, func(c *ws.Config) { c.Heartbeat = 60 * time.Millisecond }, func(t *testing.T, e *env) {
		c := e.dial(e.subscribe("p", "u", fhircast.STU3, "topic-9", "Patient-open"))
		read(t, c)
		ids := map[string]bool{}
		for i := 0; i < 3; i++ {
			m := read(t, c)
			ev := m["event"].(map[string]any)
			if ev["hub.event"] != "heartbeat" || ev["hub.topic"] != "topic-9" { // đủ topic, không bị cắt
				t.Fatalf("heartbeat: %v", m)
			}
			if p := ev["context"].([]any)[0].(map[string]any); p["key"] != "period" {
				t.Errorf("context: %v", p)
			}
			id := m["id"].(string)
			if ids[id] {
				t.Errorf("id heartbeat trùng: %s", id)
			}
			ids[id] = true
		}
		// hai socket cùng topic: mỗi socket đúng một heartbeat mỗi nhịp (không nhân đôi theo số socket hay số instance)
		c2 := e.dial(e.subscribe("p", "u", fhircast.STU3, "topic-9", "Patient-open"))
		read(t, c2)
		n1, n2 := 0, 0
		for _, k := range []*cl{c, c2} { // làm sạch hàng đợi rồi đếm trong 400ms
			for len(k.ch) > 0 {
				<-k.ch
			}
		}
		end := time.After(400 * time.Millisecond)
	count:
		for {
			select {
			case <-c.ch:
				n1++
			case <-c2.ch:
				n2++
			case <-end:
				break count
			}
		}
		if n1 < 4 || n1 > 8 || n2 < 4 || n2 > 8 {
			t.Errorf("mỗi socket phải có ~6 heartbeat trong 400ms (nhịp 60ms), có %d và %d", n1, n2)
		}
	})
}

// ---- lease (F-4)

func TestLeaseExpiryDeniesAndCloses(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		ep := fhircast.NewID()
		_ = e.store.PutSubscription(context.Background(), ep, fhircast.Subscription{ProjectID: "p", Topic: "t", Events: []string{"Patient-open"}, Version: fhircast.STU3, ExpiresAt: time.Now().Add(400 * time.Millisecond)}, time.Minute)
		c := e.dial(ep)
		read(t, c)
		m := read(t, c)
		if m["hub.mode"] != "denied" || m["hub.reason"] != "lease expired" || m["hub.topic"] != "t" || m["hub.events"] != "Patient-open" {
			t.Errorf("denied hết lease: %v", m)
		}
		if code := closeCode(t, c); code != websocket.StatusNormalClosure {
			t.Errorf("đóng %d", code)
		}
		if e.srv.Stats().Expired != 1 {
			t.Errorf("Expired = %d", e.srv.Stats().Expired)
		}
	})
}

// ---- giới hạn socket mỗi endpoint (F-4)

func TestMaxSocketsPerEndpoint(t *testing.T) {
	forEachBackend(t, func(c *ws.Config) { c.MaxSocketsPerEndpoint = 2 }, func(t *testing.T, e *env) {
		ep := e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open")
		c1, c2 := e.dial(ep), e.dial(ep) // dùng lại endpoint bởi nhiều socket là hợp lệ
		read(t, c1)
		read(t, c2)
		c3 := e.dial(ep)
		m := read(t, c3)
		if m["hub.mode"] != "denied" || m["hub.reason"] != "too many connections for this endpoint" {
			t.Errorf("socket thứ ba: %v", m)
		}
		if code := closeCode(t, c3); code != websocket.StatusPolicyViolation {
			t.Errorf("đóng %d", code)
		}
		// hai socket đầu không bị ảnh hưởng, và nhả chỗ thì vào lại được
		e.change("p", patientOpen("t", "x"))
		read(t, c1)
		_ = c1.Close(websocket.StatusNormalClosure, "")
		deadline := time.Now().Add(2 * time.Second)
		for {
			c4 := e.dial(ep)
			if read(t, c4)["hub.mode"] == "subscribe" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("chỗ không được nhả sau khi socket đóng")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

// ---- Origin (F-5)

func TestOriginPolicy(t *testing.T) {
	forEachBackend(t, func(c *ws.Config) { c.OriginPatterns = []string{"app.example.org"} }, func(t *testing.T, e *env) {
		ep := e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open")
		_, resp, err := websocket.Dial(ctx(), e.wsURL(ep), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example.com"}}})
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Errorf("Origin lạ phải 403: %v %v", resp, err)
		}
		raw, _, err := websocket.Dial(ctx(), e.wsURL(ep), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://app.example.org"}}})
		if err != nil {
			t.Fatalf("Origin được phép: %v", err)
		}
		c := newCl(raw)
		defer c.CloseNow()
		read(t, c)
		c2, _, err := websocket.Dial(ctx(), e.wsURL(ep), nil) // không có Origin (client không phải trình duyệt)
		if err != nil {
			t.Fatal(err)
		}
		c2.CloseNow()
	})
}

// ---- tin hỏng ở hai phía (ca 57, 58)

func TestMalformedMessagesAreIgnored(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		c := e.dial(e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open"))
		read(t, c)
		_ = c.Write(ctx(), websocket.MessageText, []byte("không phải JSON")) // ca 57: bỏ qua, không đóng
		_ = c.Write(ctx(), websocket.MessageText, []byte(`{"hello":"world"}`))
		_ = c.Write(ctx(), websocket.MessageBinary, []byte{1, 2, 3})
		for _, bad := range []string{"không phải JSON", `{"target":"x"}`, `{"payload":null}`, `[]`} { // ca 58
			_ = e.bus.Publish(context.Background(), fhircast.Channel("p", "t"), []byte(bad))
		}
		e.change("p", patientOpen("t", "ok"))
		if got := read(t, c); eventName(got) != "Patient-open" {
			t.Errorf("sau tin hỏng vẫn phải nhận sự kiện thật: %v", got)
		}
		// tin của client do goroutine reader đếm, độc lập với luồng giao sự kiện: nhận được sự kiện chưa có nghĩa là reader đã đọc xong, nên chờ
		deadline := time.Now().Add(2 * time.Second)
		for e.srv.Stats().Received < 2 {
			if time.Now().After(deadline) {
				t.Fatalf("Received = %d sau 2 giây, muốn >= 2", e.srv.Stats().Received)
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// ---- Bus hỏng (ca 56) và Store hỏng

type failBus struct{ fhircast.Bus }

func (failBus) Subscribe(context.Context, string, fhircast.Handler) (func(), error) {
	return nil, errors.New("redis down")
}

type failStore struct{ fhircast.Store }

func (failStore) GetSubscription(context.Context, string) (fhircast.Subscription, bool, error) {
	return fhircast.Subscription{}, false, errors.New("redis down")
}

func TestInfrastructureFailuresCloseWith1013(t *testing.T) {
	st, bus := fhircast.NewMemoryStore(fhircast.StoreOptions{}), fhircast.NewMemoryBus()
	hub := &fhircast.Hub{Store: st, Bus: bus}
	u, _ := hub.Subscribe(context.Background(), "p", "u", fhircast.STU3, "ws://x", fhircast.SubscribeRequest{ChannelType: "websocket", Mode: "subscribe", Topic: "t", Events: "Patient-open"})
	ep := fhircast.ExtractEndpoint(u)
	for name, cfg := range map[string]ws.Config{
		"bus lỗi":   {Store: st, Bus: failBus{bus}},
		"store lỗi": {Store: failStore{st}, Bus: bus},
	} {
		srv, _ := ws.New(cfg)
		ts := httptest.NewServer(srv.Handler())
		raw, _, err := websocket.Dial(ctx(), "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/fhircast/"+ep, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := newCl(raw)
		if code := closeCode(t, c); code != websocket.StatusTryAgainLater {
			t.Errorf("%s: đóng %d, muốn 1013", name, code)
		}
		ts.Close()
	}
}

func TestNewRequiresStoreAndBus(t *testing.T) {
	if _, err := ws.New(ws.Config{}); err == nil {
		t.Error("thiếu Store và Bus phải lỗi")
	}
}

// ---- client chậm (F-7)

func TestSlowClientIsClosed(t *testing.T) {
	forEachBackend(t, func(c *ws.Config) { c.SendBuffer = 2; c.WriteTimeout = 2 * time.Second }, func(t *testing.T, e *env) {
		ep := e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open")
		raw, _, err := websocket.Dial(ctx(), e.wsURL(ep), nil) // không dùng cl: không có goroutine đọc nền
		if err != nil {
			t.Fatal(err)
		}
		defer raw.CloseNow()
		if _, _, err := raw.Read(ctx()); err != nil { // xác nhận, rồi KHÔNG đọc nữa
			t.Fatal(err)
		}
		big := patientOpen("t", "big")
		big["event"].(map[string]any)["context"].([]any)[0].(map[string]any)["resource"].(map[string]any)["note"] = strings.Repeat("x", 200<<10)
		deadline := time.Now().Add(15 * time.Second)
		for e.srv.Stats().Slow == 0 && time.Now().Before(deadline) {
			e.change("p", big)
			time.Sleep(5 * time.Millisecond)
		}
		if e.srv.Stats().Slow == 0 {
			t.Fatal("client không đọc phải bị đóng vì quá chậm")
		}
	})
}

// ---- tắt êm

func TestGracefulCloseSends1001(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		c := e.dial(e.subscribe("p", "u", fhircast.STU3, "t", "Patient-open"))
		read(t, c)
		go func() {
			rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = e.srv.Close(rctx)
		}()
		if code := closeCode(t, c); code != websocket.StatusGoingAway {
			t.Errorf("đóng %d, muốn 1001", code)
		}
		if _, _, err := websocket.Dial(ctx(), e.wsURL("x"), nil); err == nil {
			t.Error("sau khi đóng không nhận kết nối mới")
		}
	})
}

func TestStatsTrackConnectionsAndTopics(t *testing.T) {
	forEachBackend(t, nil, func(t *testing.T, e *env) {
		c1 := e.dial(e.subscribe("p", "u", fhircast.STU3, "t1", "Patient-open"))
		c2 := e.dial(e.subscribe("p", "u", fhircast.STU3, "t1", "Patient-open"))
		c3 := e.dial(e.subscribe("p", "u", fhircast.STU3, "t2", "Patient-open"))
		for _, c := range []*cl{c1, c2, c3} {
			read(t, c)
		}
		if st := e.srv.Stats(); st.Conns != 3 || st.Topics != 2 || st.Sent < 3 {
			t.Errorf("%+v", st)
		}
		_ = c1.CloseNow()
		_ = c2.CloseNow()
		deadline := time.Now().Add(2 * time.Second)
		for e.srv.Stats().Topics != 1 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if st := e.srv.Stats(); st.Topics != 1 || st.Conns != 1 {
			t.Errorf("sau khi đóng: %+v", st)
		}
	})
}

// countingBus đếm đăng ký đang sống: socket đóng mà không hủy đăng ký thì đăng ký rò (và Bus giữ kênh Redis mãi).
type countingBus struct {
	fhircast.Bus
	mu     sync.Mutex
	active int
}

func (b *countingBus) Subscribe(ctx context.Context, ch string, h fhircast.Handler) (func(), error) {
	cancel, err := b.Bus.Subscribe(ctx, ch, h)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.active++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() { b.mu.Lock(); b.active--; b.mu.Unlock() })
		cancel()
	}, nil
}

func (b *countingBus) Active() int { b.mu.Lock(); defer b.mu.Unlock(); return b.active }

func TestEverySocketReleasesItsBusSubscription(t *testing.T) {
	store := fhircast.NewMemoryStore(fhircast.StoreOptions{})
	cb := &countingBus{Bus: fhircast.NewMemoryBus()}
	srv, _ := ws.New(ws.Config{Store: store, Bus: cb, Heartbeat: time.Hour})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	hub := &fhircast.Hub{Store: store, Bus: cb}
	u, _ := hub.Subscribe(context.Background(), "p", "u", fhircast.STU3, "ws://x", fhircast.SubscribeRequest{ChannelType: "websocket", Mode: "subscribe", Topic: "t", Events: "Patient-open"})
	ep := fhircast.ExtractEndpoint(u)
	var cs []*cl
	for i := 0; i < 3; i++ {
		raw, _, err := websocket.Dial(ctx(), "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/fhircast/"+ep, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := newCl(raw)
		read(t, c)
		cs = append(cs, c)
	}
	if cb.Active() != 3 {
		t.Fatalf("Active = %d, muốn 3", cb.Active())
	}
	for _, c := range cs {
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
	deadline := time.Now().Add(3 * time.Second)
	for cb.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if cb.Active() != 0 {
		t.Errorf("còn %d đăng ký bus sau khi mọi socket đóng", cb.Active())
	}
}
