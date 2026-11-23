package fhirsub_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

// redisAddr trả địa chỉ Redis cho test: miniredis, hoặc Redis THẬT nếu đặt MESSAGING_TEST_REDIS_ADDR (FLUSHDB trước và sau mỗi test).
func redisAddr(t *testing.T) string {
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

type shared interface {
	messaging.Channels
	messaging.Presence
}

// split dựng hai "tiến trình" chỉ chung khóa token và nơi pub/sub:
//   - fhir-server: kho + Manager (AttachRemote, KHÔNG có Hub) + bộ cấp token;
//   - ws-gateway: Hub (TokenOnly, chỉ xác minh token) + Feed; không có kho, không có Manager.
type split struct {
	t      *testing.T
	mem    *fhirrouter.MemoryRepository
	repo   *fhirsub.Repo
	mgr    *fhirsub.Manager
	issuer *fhirsub.Tokens
	hub    *fhirsub.Hub
	feed   *fhirsub.Feed
	srv    *httptest.Server
	srvCh  shared // Channels/Presence nhìn từ phía fhir-server
}

type splitOpts struct {
	feed fhirsub.FeedConfig
	wrap func(shared) shared // bọc Channels/Presence của phía gateway (chèn lỗi)
}

func newSplit(t *testing.T, backend string, o splitOpts) *split {
	t.Helper()
	ctx := context.Background()
	s := &split{t: t, mem: fhirrouter.NewMemoryRepository()}
	var serverBroker messaging.Broker
	var serverSide, gatewaySide shared
	switch backend {
	case "memory":
		m := messaging.NewInMemory() // một đối tượng đóng vai Redis dùng chung
		serverBroker, serverSide, gatewaySide = m, m, m
	case "redis":
		addr := redisAddr(t)
		mk := func() *messaging.Redis {
			b, err := messaging.NewRedis("redis://"+addr, messaging.RedisOptions{Block: 20 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		srvR, gwR := mk(), mk()
		serverBroker, serverSide, gatewaySide = srvR, srvR, gwR
		t.Cleanup(func() { _ = gwR.Stop(ctx) })
	default:
		t.Fatal(backend)
	}
	if o.wrap != nil {
		gatewaySide = o.wrap(gatewaySide)
	}
	s.srvCh = serverSide

	// fhir-server
	s.mgr = fhirsub.NewManager(s.mem, fastCfg())
	s.mgr.Register(serverBroker)
	if err := s.mgr.AttachRemote(serverSide, serverSide); err != nil {
		t.Fatal(err)
	}
	if err := serverBroker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	s.mgr.Start()
	s.repo = fhirsub.Wrap(s.mem, serverBroker)
	s.issuer = mustTokens(t, fhirsub.TokenConfig{SigningKey: wsKey, URL: "ws://gateway/ws/subscriptions-r4"})

	// ws-gateway
	hub, err := fhirsub.NewHub(fhirsub.WSConfig{Tokens: mustTokens(t, fhirsub.TokenConfig{SigningKey: wsKey}), TokenOnly: true,
		Heartbeat: time.Hour, BindTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s.hub = hub
	if o.feed.Refresh == 0 {
		o.feed.Refresh = time.Hour // các test không cần vòng dọn thì không để nó chạy
	}
	if s.feed, err = fhirsub.NewFeed(hub, gatewaySide, gatewaySide, o.feed); err != nil {
		t.Fatal(err)
	}
	s.srv = httptest.NewServer(hub.Handler())
	t.Cleanup(func() {
		s.srv.CloseClientConnections()
		s.srv.Close()
		_ = s.feed.Stop(ctx)
		_ = hub.Close(ctx)
		_ = s.mgr.Stop(ctx)
		_ = serverBroker.Stop(ctx)
	})
	return s
}

func (s *split) createSub(id, criteria string) {
	s.t.Helper()
	if _, err := s.repo.CreateResource(context.Background(), resource(s.t, `{"resourceType":"Subscription","id":"`+id+`","status":"active","reason":"t","criteria":"`+criteria+`","channel":{"type":"websocket"}}`)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *split) create(js string) {
	s.t.Helper()
	if _, err := s.repo.CreateResource(context.Background(), resource(s.t, js)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *split) token(id string, p *fhirauth.Principal) string {
	s.t.Helper()
	ctx := context.Background()
	if p != nil {
		ctx = fhirauth.WithPrincipal(ctx, p)
	}
	res, err := s.issuer.TokenOperation(s.mem)(ctx, &fhirrouter.Request{Params: map[string]string{"id": id}})
	if err != nil {
		s.t.Fatalf("token: %v", err)
	}
	for _, q := range res.Resource.(*fhir.Parameters).Parameter {
		if q.Name == "token" {
			return q.ValueString
		}
	}
	s.t.Fatal("không có token")
	return ""
}

func (s *split) dial() *client {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.srv.URL, "http"), nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = ws.CloseNow() })
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

// bind bind Subscription id bằng một kết nối mới; trả mã đóng (0 nếu thành công) và client.
func (s *split) bind(id string, p *fhirauth.Principal) (websocket.StatusCode, *client) {
	s.t.Helper()
	c := s.dial()
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(s.token(id, p)))
	b, err := read(s.t, c, 2*time.Second)
	if err == nil {
		if statusOf(b)["type"] != "handshake" {
			s.t.Fatalf("không phải handshake: %v", b)
		}
		return 0, c
	}
	return closeCode(err), c
}

func (s *split) mustBind(id string, p *fhirauth.Principal) *client {
	s.t.Helper()
	code, c := s.bind(id, p)
	if code != 0 {
		s.t.Fatalf("bind %s: mã đóng %v", id, code)
	}
	return c
}

func (s *split) wantEvent(c *client, focus string) {
	s.t.Helper()
	b, err := read(s.t, c, 3*time.Second)
	if err != nil || statusOf(b)["type"] != "event-notification" {
		s.t.Fatalf("phải nhận event-notification: %v %v", b, err)
	}
	if focus != "" && !strings.Contains(mustJSONString(b), focus) {
		s.t.Fatalf("event không chứa %s: %v", focus, b)
	}
}

func mustJSONString(v any) string { b, _ := json.Marshal(v); return string(b) }

func eventuallyTrue(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventually(t, what, cond)
}

func eachSplitBackend(t *testing.T, f func(t *testing.T, backend string)) {
	for _, b := range []string{"memory", "redis"} {
		t.Run(b, func(t *testing.T) { f(t, b) })
	}
}

// Đường chính: gateway không có kho lẫn Manager mà vẫn nhận sự kiện do fhir-server khớp và phát.
func TestRemoteDeliveryEndToEnd(t *testing.T) {
	eachSplitBackend(t, func(t *testing.T, backend string) {
		s := newSplit(t, backend, splitOpts{})
		s.createSub("s1", "Observation?status=final")
		c := s.mustBind("s1", &fhirauth.Principal{Subject: "alice"})

		s.create(wsObs("o1"))
		s.wantEvent(c, "Observation/o1")
		s.create(`{"resourceType":"Observation","id":"o2","status":"preliminary","code":{"text":"x"}}`) // không khớp tiêu chí
		s.create(`{"resourceType":"Patient","id":"p1"}`)                                                // sai loại
		wantNothing(t, c, "không khớp")
		if st := s.mgr.Stats(); st.Published != 1 {
			t.Errorf("Published = %d, muốn 1", st.Published)
		}
		if st := s.feed.Stats(); st.Subscribed != 1 || st.Received != 1 || st.Malformed != 0 {
			t.Errorf("feed: %+v", st)
		}
	})
}

// Chưa ai nghe thì không phát gì (không tốn kênh), nghe rồi thì nhận; sự kiện trước lúc bind không được phát lại.
func TestRemoteNothingIsPublishedWhileNobodyListens(t *testing.T) {
	eachSplitBackend(t, func(t *testing.T, backend string) {
		s := newSplit(t, backend, splitOpts{})
		s.createSub("s1", "Observation")
		s.create(wsObs("before"))
		eventuallyTrue(t, "khớp mà không có người nghe", func() bool { return s.mgr.Stats().Unheard == 1 })
		if st := s.mgr.Stats(); st.Published != 0 {
			t.Fatalf("Published = %d", st.Published)
		}
		c := s.mustBind("s1", nil)
		s.create(wsObs("after"))
		s.wantEvent(c, "Observation/after")
		wantNothing(t, c, "không phát lại sự kiện cũ")
	})
}

// Gateway chỉ nghe kênh của Subscription có kết nối: Subscription còn lại không đi qua nó.
func TestRemoteGatewayOnlyListensToBoundSubscriptions(t *testing.T) {
	eachSplitBackend(t, func(t *testing.T, backend string) {
		s := newSplit(t, backend, splitOpts{})
		s.createSub("s1", "Observation")
		s.createSub("s2", "Observation")
		c := s.mustBind("s1", nil)
		s.create(wsObs("o1"))
		s.wantEvent(c, "Observation/o1")
		eventuallyTrue(t, "s2 không có người nghe", func() bool { return s.mgr.Stats().Unheard == 1 })
		st := s.mgr.Stats()
		if st.Matched != 2 || st.Published != 1 || st.Unheard != 1 {
			t.Errorf("manager: %+v", st)
		}
		if f := s.feed.Stats(); f.Subscribed != 1 || f.Received != 1 {
			t.Errorf("feed: %+v", f)
		}
	})
}

// Quyền của từng người nhận vẫn được kiểm ở gateway (ảnh chụp quyền nằm trong token do fhir-server cấp).
func TestRemoteDeliveryStillChecksEachRecipient(t *testing.T) {
	s := newSplit(t, "memory", splitOpts{})
	s.createSub("s1", "Observation")
	allowed := s.mustBind("s1", &fhirauth.Principal{Subject: "a", Grants: fhirauth.Grants{"Observation": fhirauth.PermRead}})
	denied := s.mustBind("s1", &fhirauth.Principal{Subject: "b", Grants: fhirauth.Grants{"Patient": fhirauth.PermRead}})
	s.create(wsObs("o1"))
	s.wantEvent(allowed, "Observation/o1")
	wantNothing(t, denied, "không có quyền đọc Observation")
	if st := s.hub.Stats(); st.Denied != 1 {
		t.Errorf("Denied = %d", st.Denied)
	}
}

// Hai kết nối cùng Subscription trên gateway dùng chung một đăng ký kênh; đóng một cái không làm cái kia mất sự kiện.
func TestRemoteTwoSocketsShareOneChannelSubscription(t *testing.T) {
	eachSplitBackend(t, func(t *testing.T, backend string) {
		s := newSplit(t, backend, splitOpts{feed: fhirsub.FeedConfig{Refresh: 30 * time.Millisecond}})
		s.createSub("s1", "Observation")
		a := s.mustBind("s1", nil)
		b := s.mustBind("s1", nil)
		if f := s.feed.Stats(); f.Subscribed != 1 {
			t.Fatalf("Subscribed = %d, muốn 1", f.Subscribed)
		}
		s.create(wsObs("o1"))
		s.wantEvent(a, "o1")
		s.wantEvent(b, "o1")
		_ = a.Close(websocket.StatusNormalClosure, "")
		time.Sleep(150 * time.Millisecond) // vài vòng dọn: b còn nên đăng ký phải còn
		s.create(wsObs("o2"))
		s.wantEvent(b, "o2")
	})
}

// Hết socket thì Feed bỏ đăng ký và gỡ sổ hiện diện: bên phát thôi gửi.
func TestRemoteFeedReleasesSubscriptionWhenLastSocketCloses(t *testing.T) {
	eachSplitBackend(t, func(t *testing.T, backend string) {
		s := newSplit(t, backend, splitOpts{feed: fhirsub.FeedConfig{Refresh: 30 * time.Millisecond}})
		s.createSub("s1", "Observation")
		c := s.mustBind("s1", nil)
		present := func() bool {
			ok, err := s.srvCh.Present(context.Background(), fhirsub.WSPresenceKey("s1"))
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		if !present() {
			t.Fatal("đã bind thì sổ hiện diện phải có")
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
		eventuallyTrue(t, "Feed bỏ đăng ký", func() bool { return s.feed.Stats().Subscribed == 0 })
		eventuallyTrue(t, "sổ hiện diện được gỡ", func() bool { return !present() })
		s.create(wsObs("o1"))
		eventuallyTrue(t, "bên phát thấy không có người nghe", func() bool { return s.mgr.Stats().Unheard == 1 })
		if st := s.mgr.Stats(); st.Published != 0 {
			t.Errorf("Published = %d", st.Published)
		}
		// bind lại vẫn được
		c2 := s.mustBind("s1", nil)
		s.create(wsObs("o2"))
		s.wantEvent(c2, "o2")
	})
}

// Feed.Stop gỡ gateway khỏi sổ ngay (tắt êm): bên phát không phải chờ hết hạn.
func TestRemoteFeedStopUnmarksImmediately(t *testing.T) {
	s := newSplit(t, "memory", splitOpts{})
	s.createSub("s1", "Observation")
	s.mustBind("s1", nil)
	if err := s.feed.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.srvCh.Present(context.Background(), fhirsub.WSPresenceKey("s1")); ok {
		t.Error("Stop phải gỡ sổ hiện diện")
	}
	if f := s.feed.Stats(); f.Subscribed != 0 {
		t.Errorf("Subscribed = %d", f.Subscribed)
	}
}

// Gateway sống thì sổ hiện diện được gia hạn quá hạn TTL ban đầu (nếu không bên phát sẽ ngừng gửi sau TTL).
func TestRemoteFeedRenewsPresence(t *testing.T) {
	s := newSplit(t, "memory", splitOpts{feed: fhirsub.FeedConfig{PresenceTTL: 200 * time.Millisecond, Refresh: 40 * time.Millisecond}})
	s.createSub("s1", "Observation")
	c := s.mustBind("s1", nil)
	time.Sleep(600 * time.Millisecond) // gấp ba lần TTL
	s.create(wsObs("o1"))
	s.wantEvent(c, "o1")
}

// Gateway chết im lặng (không Stop, không gia hạn): mục hiện diện tự hết hạn nên bên phát ngừng gửi vào hư không.
func TestRemotePresenceExpiresWhenGatewayDiesSilently(t *testing.T) {
	m := messaging.NewInMemory()
	_ = m.Mark(context.Background(), fhirsub.WSPresenceKey("s1"), "dead-gateway", 100*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	if ok, _ := m.Present(context.Background(), fhirsub.WSPresenceKey("s1")); ok {
		t.Fatal("mục của gateway đã chết phải hết hạn")
	}
}

// --- lỗi hạ tầng ---

type flaky struct {
	shared
	mu       sync.Mutex
	order    []string
	failSub  bool
	failMark bool
	stops    int
}

func (f *flaky) SubscribeChannel(ctx context.Context, ch string, h messaging.ChannelHandler) (func(), error) {
	f.mu.Lock()
	f.order = append(f.order, "subscribe")
	fail := f.failSub
	f.mu.Unlock()
	if fail {
		return nil, errors.New("redis down")
	}
	stop, err := f.shared.SubscribeChannel(ctx, ch, h)
	if err != nil {
		return nil, err
	}
	return func() { f.mu.Lock(); f.stops++; f.mu.Unlock(); stop() }, nil
}

func (f *flaky) Mark(ctx context.Context, k, m string, ttl time.Duration) error {
	f.mu.Lock()
	f.order = append(f.order, "mark")
	fail := f.failMark
	f.mu.Unlock()
	if fail {
		return errors.New("redis down")
	}
	return f.shared.Mark(ctx, k, m, ttl)
}

func (f *flaky) snapshot() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...), f.stops
}

func newFlakySplit(t *testing.T, mut func(*flaky)) (*split, *flaky) {
	var fl *flaky
	s := newSplit(t, "memory", splitOpts{wrap: func(in shared) shared {
		fl = &flaky{shared: in}
		if mut != nil {
			mut(fl)
		}
		return fl
	}})
	return s, fl
}

// Đăng ký kênh PHẢI xong trước khi ghi sổ hiện diện: bên phát thấy "có người nghe" thì người nghe đã sẵn sàng, không lọt sự kiện đầu.
func TestRemoteFeedSubscribesBeforeItMarksPresence(t *testing.T) {
	s, fl := newFlakySplit(t, nil)
	s.createSub("s1", "Observation")
	s.mustBind("s1", nil)
	order, _ := fl.snapshot()
	if len(order) < 2 || order[0] != "subscribe" || order[1] != "mark" {
		t.Errorf("thứ tự = %v, muốn subscribe rồi mark", order)
	}
}

// Redis hỏng lúc đăng ký: bind thất bại với 1013 (thử lại sau), không để lại binding.
func TestRemoteBindFailsWithTryAgainWhenChannelSubscriptionFails(t *testing.T) {
	s, fl := newFlakySplit(t, func(f *flaky) { f.failSub = true })
	s.createSub("s1", "Observation")
	code, _ := s.bind("s1", &fhirauth.Principal{Subject: "alice"})
	if code != websocket.StatusTryAgainLater {
		t.Fatalf("mã đóng = %v, muốn 1013", code)
	}
	eventuallyTrue(t, "không còn binding", func() bool { return s.hub.Stats().Bindings == 0 })
	if f := s.feed.Stats(); f.Subscribed != 0 {
		t.Errorf("Subscribed = %d", f.Subscribed)
	}
	// hạn mức của người dùng được trả lại (không bị rò khi bind thất bại)
	fl.mu.Lock()
	fl.failSub = false
	fl.mu.Unlock()
	s2 := s.mustBind("s1", &fhirauth.Principal{Subject: "alice"})
	s.create(wsObs("o1"))
	s.wantEvent(s2, "o1")
}

// Ghi sổ hiện diện hỏng sau khi đã đăng ký kênh: phải hủy đăng ký vừa tạo (không rò) và bind thất bại.
func TestRemoteBindFailureAfterSubscribeCancelsTheSubscription(t *testing.T) {
	s, fl := newFlakySplit(t, func(f *flaky) { f.failMark = true })
	s.createSub("s1", "Observation")
	if code, _ := s.bind("s1", nil); code != websocket.StatusTryAgainLater {
		t.Fatalf("mã đóng = %v, muốn 1013", code)
	}
	if _, stops := fl.snapshot(); stops != 1 {
		t.Errorf("đăng ký kênh phải bị hủy, stops = %d", stops)
	}
	if f := s.feed.Stats(); f.Subscribed != 0 {
		t.Errorf("Subscribed = %d", f.Subscribed)
	}
}

// Thông điệp hỏng hoặc sai kênh trên kênh của Subscription bị bỏ, không làm hỏng Hub.
func TestRemoteMalformedMessagesAreDropped(t *testing.T) {
	s := newSplit(t, "memory", splitOpts{})
	s.createSub("s1", "Observation")
	c := s.mustBind("s1", nil)
	ctx := context.Background()
	_ = s.srvCh.PublishChannel(ctx, fhirsub.WSChannel("s1"), []byte("không phải json"))
	wrong, _ := json.Marshal(fhirsub.WSMessage{Sub: "s2", Event: fhirsub.ChangeEvent{Interaction: "create", ResourceType: "Observation", ID: "x", Resource: json.RawMessage(wsObs("x"))}})
	_ = s.srvCh.PublishChannel(ctx, fhirsub.WSChannel("s1"), wrong) // thông điệp của Subscription khác lọt vào kênh này
	eventuallyTrue(t, "hai thông điệp hỏng", func() bool { return s.feed.Stats().Malformed == 2 })
	wantNothing(t, c, "thông điệp hỏng")
	s.create(wsObs("ok"))
	s.wantEvent(c, "Observation/ok")
}

// Chế độ "tin token": token lúc cấp đã hợp lệ nên gateway bind được dù không có nguồn Subscription; Subscription bị xóa sau đó
// thì bên khớp ngừng phát nên socket im lặng.
func TestRemoteSubscriptionDeletedAfterBindStopsDelivery(t *testing.T) {
	s := newSplit(t, "memory", splitOpts{})
	s.createSub("s1", "Observation")
	c := s.mustBind("s1", nil)
	s.create(wsObs("o1"))
	s.wantEvent(c, "o1")
	if err := s.repo.DeleteResource(context.Background(), "Subscription", "s1"); err != nil {
		t.Fatal(err)
	}
	s.mgr.Invalidate()
	s.create(wsObs("o2"))
	wantNothing(t, c, "Subscription đã xóa")
}

// Không bật TokenOnly và không có Manager thì Hub vẫn từ chối bind (không vô tình "tin token" ở chế độ nhúng).
func TestHubWithoutTokenOnlyStillRequiresLookup(t *testing.T) {
	w := newWSEnv(t) // Hub nhúng có Manager: dùng token hợp lệ để chắc chắn lỗi không đến từ token
	_ = w
	hub, err := fhirsub.NewHub(fhirsub.WSConfig{SigningKey: wsKey, BindTimeout: 300 * time.Millisecond, Heartbeat: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(hub.Handler())
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close(); _ = hub.Close(context.Background()) })
	tok := mustTokens(t, fhirsub.TokenConfig{SigningKey: wsKey})
	w.subscribe("s9", "Observation")
	token := w.issueWith(tok, "s9", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_ = ws.Write(ctx, websocket.MessageText, bindMsg(token))
	_, _, err = ws.Read(ctx)
	if closeCode(err) != websocket.StatusPolicyViolation {
		t.Errorf("mã đóng = %v, muốn 1008 (websocket chưa sẵn sàng)", closeCode(err))
	}
}

func TestAttachRemoteValidation(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	m := messaging.NewInMemory()
	mgr := fhirsub.NewManager(mem, fastCfg())
	if err := mgr.AttachRemote(nil, m); err == nil {
		t.Error("thiếu Channels phải lỗi")
	}
	if err := mgr.AttachRemote(m, nil); err == nil {
		t.Error("thiếu Presence phải lỗi")
	}
	hub, _ := fhirsub.NewHub(fhirsub.WSConfig{SigningKey: wsKey})
	mgr.AttachHub(hub)
	if err := mgr.AttachRemote(m, m); err == nil {
		t.Error("AttachRemote sau AttachHub phải lỗi")
	}
	if _, err := fhirsub.NewFeed(hub, m, m, fhirsub.FeedConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fhirsub.NewFeed(hub, m, m, fhirsub.FeedConfig{}); err == nil {
		t.Error("Hub chỉ có một Feed")
	}
	if _, err := fhirsub.NewFeed(nil, m, m, fhirsub.FeedConfig{}); err == nil {
		t.Error("thiếu Hub phải lỗi")
	}
}
