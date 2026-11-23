package fhirsub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

var wsKey = []byte(strings.Repeat("k", 40))

type wsEnv struct {
	*env
	hub *fhirsub.Hub
	srv *httptest.Server
	now atomic.Int64 // dịch giờ của Hub (giây)
}

func newWSEnv(t *testing.T, mut ...func(*fhirsub.WSConfig)) *wsEnv {
	t.Helper()
	w := &wsEnv{}
	cfg := fhirsub.WSConfig{SigningKey: wsKey, Heartbeat: time.Hour, BindTimeout: 300 * time.Millisecond,
		Now: func() time.Time { return time.Now().Add(time.Duration(w.now.Load()) * time.Second) }}
	for _, m := range mut {
		m(&cfg)
	}
	hub, err := fhirsub.NewHub(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.hub = hub
	w.env = newEnv(t, fastCfg())
	w.mgr.AttachHub(hub)
	w.mgr.RegisterBroadcast(w.broker)
	w.srv = httptest.NewServer(hub.Handler())
	t.Cleanup(func() {
		w.srv.CloseClientConnections()
		w.srv.Close()
		_ = hub.Close(context.Background())
	})
	return w
}

func (w *wsEnv) subscribe(id, criteria string, extra ...string) {
	w.t.Helper()
	w.create(`{"resourceType":"Subscription","id":"` + id + `","status":"active","reason":"t","criteria":"` + criteria + `","channel":{"type":"websocket"}` + joinExtra(extra) + `}`)
}

func (w *wsEnv) token(id string) string {
	w.t.Helper()
	res, err := w.hub.TokenOperation(w.mem)(context.Background(), &fhirrouter.Request{Params: map[string]string{"id": id}})
	if err != nil {
		w.t.Fatalf("token: %v", err)
	}
	for _, p := range res.Resource.(*fhir.Parameters).Parameter {
		if p.Name == "token" {
			return p.ValueString
		}
	}
	w.t.Fatal("không có token")
	return ""
}

// client đọc liên tục vào kênh: huỷ một Read bằng context sẽ đóng kết nối nên không đọc kiểu "chờ có hạn" trực tiếp.
type client struct {
	*websocket.Conn
	in   chan []byte
	done chan error
}

func (w *wsEnv) dial() *client {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(w.srv.URL, "http"), nil)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = ws.CloseNow() })
	c := &client{Conn: ws, in: make(chan []byte, 1000), done: make(chan error, 1)}
	go func() {
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				c.done <- err
				return
			}
			c.in <- data
		}
	}()
	return c
}

func bindMsg(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]any{"token": token}})
	return b
}

// read đọc một thông điệp và trả Bundle đã giải mã.
func read(t *testing.T, c *client, wait time.Duration) (map[string]any, error) {
	t.Helper()
	select {
	case data := <-c.in:
		var b map[string]any
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatalf("JSON: %s", data)
		}
		return b, nil
	case err := <-c.done:
		c.done <- err // giữ lỗi cho lần đọc sau
		return nil, err
	case <-time.After(wait):
		return nil, context.DeadlineExceeded
	}
}

func statusOf(b map[string]any) map[string]any {
	return b["entry"].([]any)[0].(map[string]any)["resource"].(map[string]any)
}

func (w *wsEnv) bound(id string) *client {
	w.t.Helper()
	c := w.dial()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, bindMsg(w.token(id))); err != nil {
		w.t.Fatal(err)
	}
	hs, err := read(w.t, c, 2*time.Second)
	if err != nil || statusOf(hs)["type"] != "handshake" {
		w.t.Fatalf("handshake: %v %v", hs, err)
	}
	return c
}

func closeCode(err error) websocket.StatusCode { return websocket.CloseStatus(err) }

func TestWebSocketHandshakeAndNotifications(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation?status=final")
	c := w.bound("s1")

	w.create(`{"resourceType":"Observation","id":"o1","status":"preliminary","code":{"text":"x"}}`) // không khớp
	o := w.create(`{"resourceType":"Observation","id":"o2","status":"final","code":{"text":"x"}}`)
	b, err := read(t, c, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	st := statusOf(b)
	if st["type"] != "event-notification" || st["eventsSinceSubscriptionStart"] != "1" || st["subscription"].(map[string]any)["reference"] != "Subscription/s1" {
		t.Fatalf("status: %v", st)
	}
	entries := b["entry"].([]any)
	ev := entries[1].(map[string]any)
	if ev["request"].(map[string]any)["method"] != "POST" || ev["resource"].(map[string]any)["id"] != "o2" || st["notificationEvent"].([]any)[0].(map[string]any)["focus"].(map[string]any)["reference"] != "Observation/o2" {
		t.Fatalf("entry: %v", ev)
	}
	// sửa: eventNumber tăng; xóa: không kèm resource
	upd := o.(*fhir.Observation)
	upd.Status = "amended"
	w.mustUpdate(upd) // amended không còn khớp tiêu chí status=final
	if b, err = read(t, c, 300*time.Millisecond); err == nil {
		t.Fatalf("không được nhận thông báo cho thay đổi không khớp: %v", b)
	}
	w.create(`{"resourceType":"Observation","id":"o3","status":"final","code":{"text":"y"}}`)
	b, err = read(t, c, 2*time.Second)
	if err != nil || statusOf(b)["eventsSinceSubscriptionStart"] != "2" {
		t.Fatalf("sự kiện thứ hai: %v %v", b, err)
	}
	if err := w.repo.DeleteResource(context.Background(), "Observation", "o3"); err != nil {
		t.Fatal(err)
	}
	b, err = read(t, c, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	del := b["entry"].([]any)[1].(map[string]any)
	if del["request"].(map[string]any)["method"] != "DELETE" || del["resource"] != nil {
		t.Fatalf("delete: %v", del)
	}
	if s := w.hub.Stats(); s.Conns != 1 || s.Bindings != 1 || s.Sent < 4 {
		t.Errorf("stats: %+v", s)
	}
}

func (w *wsEnv) mustUpdate(r fhir.Resource) {
	w.t.Helper()
	if _, err := w.repo.UpdateResource(context.Background(), r, fhirrouter.UpdateOptions{}); err != nil {
		w.t.Fatal(err)
	}
}

func TestWebSocketManyConnectionsAndSubscriptions(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("a", "Observation")
	w.subscribe("b", "Patient")
	c1, c2 := w.bound("a"), w.bound("a") // hai kết nối cùng Subscription
	// một kết nối bind thêm Subscription thứ hai
	ctx := context.Background()
	if err := c1.Write(ctx, websocket.MessageText, bindMsg(w.token("b"))); err != nil {
		t.Fatal(err)
	}
	if hs, err := read(t, c1, 2*time.Second); err != nil || statusOf(hs)["type"] != "handshake" {
		t.Fatalf("handshake 2: %v %v", hs, err)
	}
	w.create(`{"resourceType":"Observation","status":"final","code":{"text":"x"}}`)
	w.create(`{"resourceType":"Patient","id":"p1"}`)
	got := map[string]int{}
	for i := 0; i < 2; i++ {
		b, err := read(t, c1, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		got[statusOf(b)["subscription"].(map[string]any)["reference"].(string)]++
	}
	if got["Subscription/a"] != 1 || got["Subscription/b"] != 1 {
		t.Errorf("c1 nhận: %v", got)
	}
	if b, err := read(t, c2, 2*time.Second); err != nil || statusOf(b)["subscription"].(map[string]any)["reference"] != "Subscription/a" {
		t.Errorf("c2: %v %v", b, err)
	}
	if _, err := read(t, c2, 200*time.Millisecond); err == nil {
		t.Error("c2 không bind Subscription b nên không được nhận Patient")
	}
}

func TestWebSocketRejectsBadBinding(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	w.create(`{"resourceType":"Subscription","id":"rest","status":"active","reason":"t","criteria":"Observation","channel":{"type":"rest-hook","endpoint":"http://127.0.0.1:1"}}`)
	expect := func(what string, c *client) {
		t.Helper()
		_, err := read(t, c, 2*time.Second)
		if closeCode(err) != websocket.StatusPolicyViolation {
			t.Errorf("%s: muốn đóng với policy violation, được %v", what, err)
		}
	}
	send := func(c *client, msg []byte) {
		if err := c.Write(context.Background(), websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
	}
	c := w.dial()
	send(c, bindMsg("khong-phai-token"))
	expect("token sai", c)

	good := w.token("s1")
	c = w.dial()
	send(c, bindMsg(good[:len(good)-2]+"xx")) // chữ ký bị sửa
	expect("chữ ký sai", c)

	c = w.dial()
	send(c, []byte(`{"type":"other"}`))
	expect("thông điệp đầu không phải bind", c)

	c = w.dial() // không gửi gì
	expect("quá hạn bind", c)

	w.now.Store(int64(10 * time.Minute / time.Second)) // token (TTL 5 phút) hết hạn
	c = w.dial()
	send(c, bindMsg(good))
	expect("token hết hạn", c)
	w.now.Store(0)

	// Subscription bị tắt sau khi cấp token
	tok := w.token("s1")
	off := w.get("Subscription", "s1").(*fhir.Subscription)
	off.Status = "off"
	w.mustUpdate(off)
	w.mgr.Invalidate()
	c = w.dial()
	send(c, bindMsg(tok))
	expect("Subscription đã tắt", c)

	// TokenOperation từ chối Subscription không phải websocket hoặc không tồn tại
	for _, id := range []string{"rest", "missing"} {
		if _, err := w.hub.TokenOperation(w.mem)(context.Background(), &fhirrouter.Request{Params: map[string]string{"id": id}}); err == nil {
			t.Errorf("%s: phải lỗi", id)
		}
	}
	// token ký bằng khóa khác không dùng được
	other, _ := fhirsub.NewHub(fhirsub.WSConfig{SigningKey: []byte(strings.Repeat("z", 40))})
	w.subscribe("s2", "Observation")
	res, _ := other.TokenOperation(w.mem)(context.Background(), &fhirrouter.Request{Params: map[string]string{"id": "s2"}})
	foreign := res.Resource.(*fhir.Parameters).Parameter[0].ValueString
	c = w.dial()
	send(c, bindMsg(foreign))
	expect("khóa ký khác", c)
}

func (w *wsEnv) get(rt, id string) fhir.Resource {
	w.t.Helper()
	r, err := w.repo.ReadResource(context.Background(), rt, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func TestWebSocketLimitsAndShutdown(t *testing.T) {
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.MaxConns = 1; c.MaxBindingsPerConn = 1 })
	w.subscribe("a", "Observation")
	w.subscribe("b", "Patient")
	c := w.bound("a")

	// vượt số kết nối: 503 ngay khi bắt tay
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(w.srv.URL, "http"), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("kết nối vượt giới hạn: %v %v", resp, err)
	}
	// vượt số Subscription trên một kết nối: bị ngắt
	if err := c.Write(context.Background(), websocket.MessageText, bindMsg(w.token("b"))); err != nil {
		t.Fatal(err)
	}
	if _, err := read(t, c, 2*time.Second); closeCode(err) != websocket.StatusPolicyViolation {
		t.Errorf("vượt số bind: %v", err)
	}

	// tắt êm: Close ngắt kết nối với StatusGoingAway
	w2 := newWSEnv(t)
	w2.subscribe("a", "Observation")
	c2 := w2.bound("a")
	if err := w2.hub.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := read(t, c2, 2*time.Second); closeCode(err) != websocket.StatusGoingAway {
		t.Errorf("tắt êm: %v", err)
	}
	// sau Close không nhận kết nối mới
	_, resp, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(w2.srv.URL, "http"), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("sau Close: %v %v", resp, err)
	}
	if s := w2.hub.Stats(); s.Conns != 0 {
		t.Errorf("còn kết nối sau Close: %+v", s)
	}
}

func TestWebSocketHeartbeat(t *testing.T) {
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.Heartbeat = 50 * time.Millisecond })
	w.subscribe("a", "Observation")
	c := w.bound("a")
	b, err := read(t, c, 2*time.Second)
	if err != nil || statusOf(b)["type"] != "heartbeat" {
		t.Fatalf("heartbeat: %v %v", b, err)
	}
}

func TestWebSocketSlowConsumerIsDropped(t *testing.T) {
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.SendBuffer = 2; c.WriteTimeout = time.Minute })
	w.subscribe("a", "Observation")
	// kết nối thô, không bao giờ đọc: bộ đệm mạng đầy thì writer kẹt, hàng đợi gửi đầy rồi kết nối bị ngắt
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(w.srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if err := ws.Write(ctx, websocket.MessageText, bindMsg(w.token("a"))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bind xong", func() bool { return w.hub.Stats().Bindings == 1 })
	big := strings.Repeat("x", 1<<20)
	for i := 0; i < 200 && w.hub.Stats().Slow == 0; i++ {
		w.create(`{"resourceType":"Observation","status":"final","code":{"text":"` + big + `"}}`)
	}
	eventually(t, "kết nối chậm bị ngắt", func() bool { return w.hub.Stats().Slow > 0 })
}

func TestWebSocketOnlySeesOwnProject(t *testing.T) {
	w := newWSEnv(t)
	w.create(`{"resourceType":"Subscription","id":"sa","status":"active","reason":"t","criteria":"Observation","channel":{"type":"websocket"},"meta":{"project":"A"}}`)
	c := w.bound("sa")
	w.create(`{"resourceType":"Observation","status":"final","code":{"text":"x"},"meta":{"project":"B"}}`)
	if b, err := read(t, c, 300*time.Millisecond); err == nil {
		t.Fatalf("nhận dữ liệu của project khác: %v", b)
	}
	w.create(`{"resourceType":"Observation","status":"final","code":{"text":"x"},"meta":{"project":"A"}}`)
	if _, err := read(t, c, 2*time.Second); err != nil {
		t.Fatalf("không nhận dữ liệu cùng project: %v", err)
	}
}

// Hai instance (mỗi cái có Broker Redis, Manager và Hub riêng) dùng chung Redis và kho dữ liệu: ghi ở instance A, kết nối
// websocket ở instance B vẫn nhận được thông báo nhờ broadcast.
func TestWebSocketAcrossInstancesViaRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	mem := fhirrouter.NewMemoryRepository()
	ctx := context.Background()
	type inst struct {
		hub    *fhirsub.Hub
		srv    *httptest.Server
		repo   *fhirsub.Repo
		broker *messaging.Redis
	}
	mk := func() *inst {
		broker, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{Block: 20 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		hub, err := fhirsub.NewHub(fhirsub.WSConfig{SigningKey: wsKey, Heartbeat: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		mgr := fhirsub.NewManager(mem, fastCfg())
		mgr.Register(broker)
		mgr.AttachHub(hub)
		mgr.RegisterBroadcast(broker)
		if err := broker.Start(ctx); err != nil {
			t.Fatal(err)
		}
		mgr.Start()
		srv := httptest.NewServer(hub.Handler())
		t.Cleanup(func() {
			srv.CloseClientConnections()
			srv.Close()
			_ = hub.Close(ctx)
			_ = mgr.Stop(ctx)
			_ = broker.Stop(ctx)
		})
		return &inst{hub, srv, fhirsub.Wrap(mem, broker), broker}
	}
	a, b := mk(), mk()
	if _, err := a.repo.CreateResource(ctx, resource(t, `{"resourceType":"Subscription","id":"s1","status":"active","reason":"t","criteria":"Patient","channel":{"type":"websocket"}}`)); err != nil {
		t.Fatal(err)
	}
	res, err := b.hub.TokenOperation(mem)(ctx, &fhirrouter.Request{Params: map[string]string{"id": "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	token := res.Resource.(*fhir.Parameters).Parameter[0].ValueString

	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(b.srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if err := ws.Write(dctx, websocket.MessageText, bindMsg(token)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(dctx); err != nil { // handshake
		t.Fatal(err)
	}
	// instance A (không có kết nối nào) ghi dữ liệu; kết nối ở B phải nhận
	if _, err := a.repo.CreateResource(ctx, resource(t, `{"resourceType":"Patient","id":"p1"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := ws.Read(dctx)
	if err != nil || !strings.Contains(string(data), `"event-notification"`) || !strings.Contains(string(data), `"Patient/p1"`) {
		t.Fatalf("B nhận: %s %v", data, err)
	}
	if s := a.hub.Stats(); s.Sent != 0 {
		t.Errorf("A không có kết nối nên không gửi gì: %+v", s)
	}
}
