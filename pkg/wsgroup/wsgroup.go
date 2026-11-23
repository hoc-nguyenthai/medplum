// Package wsgroup là phần dùng chung của mọi endpoint WebSocket: nhận upgrade (kiểm Origin), giới hạn số kết nối, theo
// dõi kết nối đang mở, bắt panic của handler và đóng êm có hạn. Nó không biết giao thức bên trên (Subscription, FHIRcast,
// echo): mỗi kênh là một ServeFunc.
//
// Mã trả về và mã đóng:
//   - đang đóng: upgrade mới nhận HTTP 503; kết nối lọt qua đúng lúc đóng bị đóng 1001 (going away);
//   - quá MaxConns: HTTP 503, KHÔNG tạo goroutine hay gọi Accept (kết nối bị từ chối không tốn tài nguyên); chỗ được đặt
//     TRƯỚC khi Accept nên nhiều upgrade đồng thời không vượt được MaxConns;
//   - quá MaxConnsPerIP kết nối đang mở từ cùng một IP: HTTP 429;
//   - Origin không nằm trong danh sách: HTTP 403 (do websocket.Accept); không có Origin (client không phải trình duyệt)
//     thì qua;
//   - handler panic: đóng 1011 và ghi log, tiến trình không sập;
//   - khung vượt ReadLimit: thư viện đóng 1009.
//
// Đóng êm: Close gửi 1001 tới mọi kết nối song song và chờ tối đa CloseTimeout (và ctx) để các client đáp lại, rồi huỷ
// ngữ cảnh để handler thoát. Client im lặng không giữ Close quá CloseTimeout: thư viện websocket tự bỏ bắt tay đóng sau
// 5 giây và việc dọn kết nối đó chạy nền (không cắt sớm hơn được vì CloseNow không ngắt được một Close đang chờ). Trần
// thực tế là vài giây, không phải 30 giây như Medplum; trần tổng do ctx của người gọi (SHUTDOWN_TIMEOUT của pkg/httpserve).
package wsgroup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Config cấu hình Group; giá trị 0 dùng mặc định trong ngoặc.
type Config struct {
	MaxConns      int // số kết nối tối đa của Group (1000)
	MaxConnsPerIP int // số kết nối tối đa từ một IP (0: không giới hạn)
	// ClientIP xác định IP của yêu cầu để đếm MaxConnsPerIP. Mặc định là host của RemoteAddr; sau proxy PHẢI cấp hàm tin
	// đúng proxy đã khai (nếu không mọi kết nối cùng một IP của proxy).
	ClientIP       func(*http.Request) string
	ReadLimit      int64         // kích thước tối đa của một khung nhận (64 KiB)
	CloseTimeout   time.Duration // thời gian chờ client đáp khung đóng khi Close (5s)
	OriginPatterns []string      // mẫu path.Match của Origin được phép; rỗng: chỉ cùng host
}

func (c *Config) defaults() {
	if c.MaxConns <= 0 {
		c.MaxConns = 1000
	}
	if c.ReadLimit <= 0 {
		c.ReadLimit = 64 << 10
	}
	if c.CloseTimeout <= 0 {
		c.CloseTimeout = 5 * time.Second
	}
	if c.ClientIP == nil {
		c.ClientIP = func(r *http.Request) string {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				return r.RemoteAddr
			}
			return host
		}
	}
}

// ServeFunc chạy một kết nối đã được chấp nhận. ctx bị huỷ khi Group đóng xong giai đoạn đóng êm. Khi hàm trả về, Group
// cắt và gỡ kết nối. Hàm KHÔNG được giữ c sau khi trả về.
type ServeFunc func(ctx context.Context, c *websocket.Conn)

// Group quản lý các kết nối của một hoặc nhiều endpoint.
type Group struct {
	cfg Config

	mu       sync.Mutex
	conns    map[*websocket.Conn]struct{}
	reserved int            // chỗ đã đặt (kể cả upgrade đang Accept): MaxConns kiểm theo số này
	perIP    map[string]int // chỗ đã đặt theo IP
	closed   bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // mỗi handler đang chạy (kể cả đang Accept)
}

// New tạo Group.
func New(cfg Config) *Group {
	cfg.defaults()
	g := &Group{cfg: cfg, conns: map[*websocket.Conn]struct{}{}, perIP: map[string]int{}}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	return g
}

// Conns trả số kết nối đang mở.
func (g *Group) Conns() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.conns)
}

// Handler trả http.Handler nhận upgrade rồi gọi serve.
func (g *Group) Handler(serve ServeFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ""
		if g.cfg.MaxConnsPerIP > 0 {
			ip = g.cfg.ClientIP(r)
		}
		g.mu.Lock()
		switch {
		case g.closed:
			g.mu.Unlock()
			http.Error(w, "đang tắt", http.StatusServiceUnavailable)
			return
		case g.reserved >= g.cfg.MaxConns:
			g.mu.Unlock()
			http.Error(w, "quá nhiều kết nối", http.StatusServiceUnavailable)
			return
		case g.cfg.MaxConnsPerIP > 0 && g.perIP[ip] >= g.cfg.MaxConnsPerIP:
			g.mu.Unlock()
			w.Header().Set("Retry-After", "5")
			http.Error(w, "quá nhiều kết nối từ địa chỉ này", http.StatusTooManyRequests)
			return
		}
		g.reserved++
		if g.cfg.MaxConnsPerIP > 0 {
			g.perIP[ip]++
		}
		g.wg.Add(1)
		g.mu.Unlock()
		defer g.wg.Done()
		defer func() { // trả chỗ ở mọi đường thoát (kể cả Accept thất bại)
			g.mu.Lock()
			g.reserved--
			if g.cfg.MaxConnsPerIP > 0 {
				if g.perIP[ip]--; g.perIP[ip] <= 0 {
					delete(g.perIP, ip)
				}
			}
			g.mu.Unlock()
		}()

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: g.cfg.OriginPatterns})
		if err != nil {
			return // Accept đã trả lỗi HTTP
		}
		c.SetReadLimit(g.cfg.ReadLimit)
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			_ = c.Close(websocket.StatusGoingAway, "server shutting down")
			return
		}
		g.conns[c] = struct{}{}
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			delete(g.conns, c)
			closing := g.closed
			g.mu.Unlock()
			if closing {
				// Close đang gửi 1001; CloseNow sẽ chờ bắt tay đóng (≤ 5s của thư viện): làm nền để handler không giữ Close.
				go func() { _ = c.CloseNow() }()
				return
			}
			_ = c.CloseNow()
		}()

		ctx, cancel := context.WithCancel(g.ctx)
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				slog.ErrorContext(ctx, "wsgroup: handler websocket panic", "panic", fmt.Sprint(p))
				_ = c.Close(websocket.StatusInternalError, "internal error")
			}
		}()
		serve(ctx, c)
	})
}

// Close từ chối kết nối mới, gửi 1001 tới mọi kết nối đang mở, chờ chúng đáp (tối đa CloseTimeout), cắt phần còn lại rồi
// chờ mọi handler thoát hoặc ctx hết hạn. Gọi nhiều lần an toàn.
func (g *Group) Close(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	cs := make([]*websocket.Conn, 0, len(g.conns))
	for c := range g.conns {
		cs = append(cs, c)
	}
	g.mu.Unlock()

	// Gửi khung đóng TRƯỚC khi huỷ ngữ cảnh: huỷ trước thì handler thoát và cắt kết nối, client không nhận được mã 1001.
	var closing sync.WaitGroup
	for _, c := range cs {
		closing.Go(func() { _ = c.Close(websocket.StatusGoingAway, "server shutting down") })
	}
	acked := make(chan struct{})
	go func() { closing.Wait(); close(acked) }()
	timer := time.NewTimer(g.cfg.CloseTimeout)
	defer timer.Stop()
	select {
	case <-acked:
	case <-timer.C: // client im lặng: bỏ chờ, huỷ ngữ cảnh ở dưới
	case <-ctx.Done():
	}
	g.cancel()
	done := make(chan struct{})
	go func() { g.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Join(ctx.Err())
	}
}
