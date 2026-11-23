// Package ws là kênh WebSocket /ws/fhircast/{endpoint} của Hub FHIRcast: đọc đăng ký của endpoint, nghe kênh của topic, gửi
// xác nhận rồi chuyển tiếp những thông điệp tới đúng người nhận.
//
// Mỗi socket có hàng đợi gửi riêng (chậm thì bị đóng 1013), heartbeat cục bộ (không qua Bus nên không trùng giữa các
// instance), hạn lease thật (hết hạn thì gửi denied rồi đóng) và giới hạn số socket mỗi endpoint. Gói này chỉ nhận kết nối vào:
// không có đường gọi ra ngoài (xem TestFhircastNeverDialsOut).
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhircast"
	"trivita-fhir/pkg/wsgroup"
)

// Config cấu hình Server; giá trị 0 dùng mặc định trong ngoặc.
type Config struct {
	Store fhircast.Store
	Bus   fhircast.Bus

	MaxConns              int // số socket tối đa của instance (1000)
	MaxConnsPerIP         int // số socket tối đa từ một IP (0: không giới hạn)
	MaxSocketsPerEndpoint int // số socket đồng thời tối đa của một endpoint, tính trên instance này (4)
	ClientIP              func(*http.Request) string
	OriginPatterns        []string
	CloseTimeout          time.Duration // chờ client đáp khung đóng khi tắt (5s)
	Heartbeat             time.Duration // chu kỳ heartbeat của từng socket (fhircast.HeartbeatPeriod = 10s)
	SendBuffer            int           // cỡ hàng đợi gửi mỗi socket (64)
	WriteTimeout          time.Duration // hạn một lần ghi (10s)
	CallTimeout           time.Duration // hạn mỗi lời gọi Store/Bus (3s)
	Now                   func() time.Time
}

func (c *Config) defaults() {
	if c.MaxSocketsPerEndpoint <= 0 {
		c.MaxSocketsPerEndpoint = 4
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = fhircast.HeartbeatPeriod
	}
	if c.SendBuffer <= 0 {
		c.SendBuffer = 64
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.CallTimeout <= 0 {
		c.CallTimeout = 3 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Stats là bộ đếm của Server.
type Stats struct {
	Conns    int   // socket đang mở
	Topics   int   // số (project, topic) có socket
	Sent     int64 // khung đã gửi
	Received int64 // khung nhận từ client
	Slow     int64 // socket bị đóng vì hàng đợi đầy
	Denied   int64 // socket bị đóng vì denied (hủy hoặc hết lease hoặc endpoint sai)
	Expired  int64 // socket bị đóng vì hết lease
}

// Server nhận kết nối WebSocket của FHIRcast.
type Server struct {
	cfg   Config
	group *wsgroup.Group

	mu       sync.Mutex
	perEP    map[string]int
	perTopic map[string]int

	sent, received, slow, denied, expired atomic.Int64
}

// New tạo Server.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil || cfg.Bus == nil {
		return nil, errors.New("fhircast/ws: cần Store và Bus")
	}
	cfg.defaults()
	return &Server{
		cfg: cfg, perEP: map[string]int{}, perTopic: map[string]int{},
		group: wsgroup.New(wsgroup.Config{
			MaxConns: cfg.MaxConns, MaxConnsPerIP: cfg.MaxConnsPerIP, ClientIP: cfg.ClientIP,
			CloseTimeout: cfg.CloseTimeout, OriginPatterns: cfg.OriginPatterns,
		}),
	}, nil
}

// Handler là http.Handler của đường dẫn kết thúc bằng /{endpoint} (query và dấu "/" cuối được bỏ).
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep := Endpoint(r)
		// wsgroup.Handler không giữ trạng thái riêng cho mỗi lần gọi: bọc theo yêu cầu để ServeFunc biết endpoint.
		s.group.Handler(func(ctx context.Context, c *websocket.Conn) { s.serve(ctx, c, ep) }).ServeHTTP(w, r)
	})
}

// Close gửi 1001 tới mọi socket và chờ chúng đáp (tối đa CloseTimeout), xem wsgroup.Group.Close.
func (s *Server) Close(ctx context.Context) error { return s.group.Close(ctx) }

// Stats trả bản chụp bộ đếm.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	n := len(s.perTopic)
	s.mu.Unlock()
	return Stats{Conns: s.group.Conns(), Topics: n, Sent: s.sent.Load(), Received: s.received.Load(), Slow: s.slow.Load(),
		Denied: s.denied.Load(), Expired: s.expired.Load()}
}

// frame là một khung chờ gửi; closeAfter đóng socket (1000) ngay sau khi gửi xong (denied).
type frame struct {
	data       []byte
	closeAfter bool
}

type conn struct {
	s        *Server
	ws       *websocket.Conn
	endpoint string
	sub      fhircast.Subscription
	send     chan frame
	closing  atomic.Bool
}

func (s *Server) serve(ctx context.Context, ws *websocket.Conn, endpoint string) {
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, s.cfg.CallTimeout) }

	cctx, cancel := call()
	sub, ok, err := func() (fhircast.Subscription, bool, error) {
		defer cancel()
		if endpoint == "" {
			return fhircast.Subscription{}, false, nil
		}
		return s.cfg.Store.GetSubscription(cctx, endpoint)
	}()
	if err != nil { // hạ tầng lỗi: client thử lại, không phải endpoint sai
		slog.ErrorContext(ctx, "fhircast ws: không đọc được đăng ký", "error", err)
		_ = ws.Close(websocket.StatusTryAgainLater, "dịch vụ tạm thời không sẵn sàng")
		return
	}
	if !ok || (!sub.ExpiresAt.IsZero() && !s.cfg.Now().Before(sub.ExpiresAt)) {
		s.denied.Add(1)
		s.writeJSON(ctx, ws, fhircast.InvalidEndpoint())
		_ = ws.Close(websocket.StatusNormalClosure, "")
		return
	}

	if !s.acquireEndpoint(endpoint) {
		s.denied.Add(1)
		s.writeJSON(ctx, ws, fhircast.Denied(sub.Topic, sub.Events, "too many connections for this endpoint"))
		_ = ws.Close(websocket.StatusPolicyViolation, "quá nhiều kết nối cho endpoint này")
		return
	}
	defer s.releaseEndpoint(endpoint)
	key := sub.ProjectID + ":" + fhircast.TopicHash(sub.Topic)
	s.mu.Lock()
	s.perTopic[key]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.perTopic[key]--; s.perTopic[key] <= 0 {
			delete(s.perTopic, key)
		}
		s.mu.Unlock()
	}()

	c := &conn{s: s, ws: ws, endpoint: endpoint, sub: sub, send: make(chan frame, s.cfg.SendBuffer)}

	// Nghe kênh của topic TRƯỚC khi gửi xác nhận: sự kiện phát ngay sau đó không bị lọt. Sự kiện tới trong lúc xác nhận
	// đang được ghi nằm trong hàng đợi và chỉ được gửi sau xác nhận (writer chưa chạy).
	cctx, cancel = call()
	stop, err := s.cfg.Bus.Subscribe(cctx, fhircast.Channel(sub.ProjectID, sub.Topic), c.onMessage)
	cancel()
	if err != nil {
		slog.ErrorContext(ctx, "fhircast ws: không nghe được kênh của topic", "error", err)
		_ = ws.Close(websocket.StatusTryAgainLater, "không nghe được kênh")
		return
	}
	defer stop()

	if !s.writeJSON(ctx, ws, fhircast.Confirmation(sub)) {
		return
	}

	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.writer(rctx, rcancel) }()
	go func() { defer wg.Done(); c.ticker(rctx) }()
	c.reader(rctx)
	rcancel()
	wg.Wait()
}

func (s *Server) acquireEndpoint(ep string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perEP[ep] >= s.cfg.MaxSocketsPerEndpoint {
		return false
	}
	s.perEP[ep]++
	return true
}

func (s *Server) releaseEndpoint(ep string) {
	s.mu.Lock()
	if s.perEP[ep]--; s.perEP[ep] <= 0 {
		delete(s.perEP, ep)
	}
	s.mu.Unlock()
}

// writeJSON ghi một khung văn bản ngay (dùng trước khi writer chạy).
func (s *Server) writeJSON(ctx context.Context, ws *websocket.Conn, v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return s.write(ctx, ws, b)
}

func (s *Server) write(ctx context.Context, ws *websocket.Conn, b []byte) bool {
	wctx, cancel := context.WithTimeout(ctx, s.cfg.WriteTimeout)
	defer cancel()
	if err := ws.Write(wctx, websocket.MessageText, b); err != nil {
		return false
	}
	s.sent.Add(1)
	return true
}

// onMessage nhận thông điệp từ Bus: lọc theo người nhận rồi xếp vào hàng đợi.
func (c *conn) onMessage(raw []byte) {
	payload, ok := fhircast.MessageForSubscriber(raw, c.endpoint, c.sub)
	if !ok {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	// denied kết thúc đăng ký này nên Hub đóng socket sau khi gửi (client vẫn được báo lý do).
	c.enqueue(frame{data: b, closeAfter: payload["hub.mode"] == "denied"})
}

// enqueue xếp khung; hàng đợi đầy là client quá chậm: đóng 1013 thay vì để bộ nhớ phình ra.
func (c *conn) enqueue(f frame) {
	if c.closing.Load() {
		return
	}
	select {
	case c.send <- f:
	default:
		if c.closing.CompareAndSwap(false, true) {
			c.s.slow.Add(1)
			go func() { _ = c.ws.Close(websocket.StatusTryAgainLater, "client quá chậm") }()
		}
	}
}

func (c *conn) writer(ctx context.Context, stop context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-c.send:
			if !c.s.write(ctx, c.ws, f.data) {
				stop()
				return
			}
			if f.closeAfter {
				c.s.denied.Add(1)
				c.closing.Store(true)
				_ = c.ws.Close(websocket.StatusNormalClosure, "")
				stop()
				return
			}
		}
	}
}

// ticker phát heartbeat cục bộ cho socket này và đóng socket khi hết lease.
func (c *conn) ticker(ctx context.Context) {
	hb := time.NewTicker(c.s.cfg.Heartbeat)
	defer hb.Stop()
	var lease <-chan time.Time
	if !c.sub.ExpiresAt.IsZero() {
		d := c.sub.ExpiresAt.Sub(c.s.cfg.Now())
		if d < 0 {
			d = 0
		}
		t := time.NewTimer(d)
		defer t.Stop()
		lease = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-lease:
			// Hết lease: báo rồi đóng (Q-FC-4), subscriber có thể đăng ký lại.
			c.s.expired.Add(1)
			b, _ := json.Marshal(fhircast.Denied(c.sub.Topic, c.sub.Events, "lease expired"))
			c.enqueue(frame{data: b, closeAfter: true})
			lease = nil
		case <-hb.C:
			b, err := json.Marshal(fhircast.Heartbeat(c.sub.Topic, fhircast.NewID(), c.s.cfg.Now(), c.s.cfg.Heartbeat))
			if err == nil {
				c.enqueue(frame{data: b})
			}
		}
	}
}

// reader đọc tin của client: chỉ được phân tích và ghi log, không có tác dụng; tin hỏng không làm đóng socket.
func (c *conn) reader(ctx context.Context) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		c.s.received.Add(1)
		var v any
		if json.Unmarshal(data, &v) != nil {
			slog.WarnContext(ctx, "fhircast ws: bỏ qua tin không phải JSON của client", "topic", c.sub.Topic)
		}
	}
}

// Endpoint lấy endpoint từ yêu cầu: đoạn cuối của đường dẫn, bỏ query, fragment và dấu "/" thừa.
func Endpoint(r *http.Request) string { return fhircast.ExtractEndpoint(r.URL.RequestURI()) }
