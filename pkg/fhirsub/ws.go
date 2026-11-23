package fhirsub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/wsgroup"
)

// Subscription kênh websocket (giao thức R4 "Subscriptions R5 Backport" như Medplum).
//
//  1. Tạo Subscription có channel.type = "websocket" (không cần endpoint) và criteria như rest-hook.
//  2. GET|POST [base]/Subscription/{id}/$get-ws-binding-token -> Parameters {token, expiration, websocket-url}.
//     Người gọi phải đọc được Subscription (đã tách project và quyền theo loại); token ký HMAC-SHA256, hạn ngắn, và mang
//     ảnh chụp quyền của người gọi (scope SMART, AccessPolicy, hạn access token).
//  3. Mở websocket tới websocket-url (mặc định /ws/subscriptions-r4) và gửi
//     {"type":"bind-with-token","payload":{"token":"<token>"}}; có thể bind nhiều Subscription trên một kết nối.
//  4. Server gửi Bundle(history) chứa SubscriptionStatus: type "handshake" ngay sau khi bind, "heartbeat" định kỳ,
//     và "event-notification" (kèm resource) mỗi khi có thay đổi khớp tiêu chí.
//  5. Quyền được kiểm LÚC PHÁT, theo từng kết nối: sự kiện chỉ được gửi nếu ảnh chụp quyền của người bind cho đọc loại
//     resource đó (chữ r của scope SMART) và resource thỏa AccessPolicy; không thì bỏ qua, không gửi cả tham chiếu.
//     Hết hạn access token gốc thì binding đó thôi nhận (phải lấy token mới). Quyền đổi sau khi cấp token không có hiệu
//     lực cho tới khi token access hết hạn.
//
// Mỗi instance giữ kết nối riêng; sự kiện đến mọi instance qua messaging.Broadcaster nên kết nối ở instance nào cũng
// nhận được. Giới hạn: sự kiện không bền (kết nối rớt thì mất thông báo trong lúc rớt, không có replay), token dùng
// lại được tới khi hết hạn, kết nối chậm (bộ đệm gửi đầy) bị ngắt, Subscription bị xóa/tắt không ngắt kết nối đang mở
// (chỉ thôi nhận thông báo).

// WSConfig cấu hình Hub; giá trị 0 dùng mặc định trong ngoặc.
type WSConfig struct {
	SigningKey   []byte   // khóa ký token (>= 32 byte), mọi instance dùng chung
	PreviousKeys [][]byte // khóa cũ chỉ để xác minh khi xoay khóa (xem ws_token.go)
	// Tokens, nếu có, thay SigningKey/PreviousKeys/URL/TokenTTL: Hub chỉ xác minh token của bộ này (ws-gateway dùng khi bên cấp
	// ở tiến trình khác).
	Tokens *Tokens
	// TokenOnly bỏ bước kiểm Subscription với Manager lúc bind (ws-gateway không có Manager hay nguồn Subscription): chỉ tin
	// token. An toàn vì token do fhir-server cấp sau khi kiểm Subscription đọc được, active, channel websocket, ký bằng khóa
	// chung và hạn 5 phút; Subscription bị xóa hay tắt sau đó thì bên khớp ngừng phát nên socket không nhận thêm gì. KHÔNG bật
	// ở chế độ nhúng (có Manager).
	TokenOnly          bool
	URL                string        // websocket-url trả về trong token ("/ws/subscriptions-r4" nếu rỗng)
	TokenTTL           time.Duration // hạn của token (5 phút)
	MaxConns           int           // số kết nối tối đa mỗi instance (1000)
	MaxBindingsPerConn int           // số Subscription tối đa mỗi kết nối (50)
	// MaxBindingsPerUser là số lượt bind đồng thời tối đa của một người (Principal.Subject) trong một project, trên mọi kết nối
	// của instance này (20, như maxUserWebSocketSubscriptions của Medplum; âm: không giới hạn). Chỉ áp cho token có ảnh chụp
	// quyền; token không phân quyền (không có người) không có khóa để đếm.
	MaxBindingsPerUser int
	SendBuffer         int                        // sức chứa hàng đợi gửi mỗi kết nối (256)
	Heartbeat          time.Duration              // chu kỳ heartbeat và ping (30s)
	BindTimeout        time.Duration              // thời gian chờ thông điệp bind đầu tiên (10s)
	WriteTimeout       time.Duration              // timeout mỗi lần ghi (10s)
	OriginPatterns     []string                   // origin được phép (mẫu path.Match); rỗng: chỉ cùng host (trình duyệt gửi Origin)
	CloseTimeout       time.Duration              // thời gian chờ client đáp khung đóng khi Close (5s, xem pkg/wsgroup)
	MaxConnsPerIP      int                        // số kết nối tối đa từ một IP (0: không giới hạn)
	ClientIP           func(*http.Request) string // xác định IP của yêu cầu cho MaxConnsPerIP (mặc định RemoteAddr)
	Now                func() time.Time
}

func (c *WSConfig) defaults() {
	if c.URL == "" {
		c.URL = "/ws/subscriptions-r4"
	}
	if c.TokenTTL <= 0 {
		c.TokenTTL = 5 * time.Minute
	}
	if c.MaxConns <= 0 {
		c.MaxConns = 1000
	}
	if c.MaxBindingsPerConn <= 0 {
		c.MaxBindingsPerConn = 50
	}
	if c.MaxBindingsPerUser == 0 {
		c.MaxBindingsPerUser = 20
	}
	if c.SendBuffer <= 0 {
		c.SendBuffer = 256
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = 30 * time.Second
	}
	if c.BindTimeout <= 0 {
		c.BindTimeout = 10 * time.Second
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// WSStats là bộ đếm của Hub.
type WSStats struct {
	Conns    int   // kết nối đang mở
	Bindings int   // lượt bind đang hiệu lực
	Sent     int64 // thông báo đã xếp vào hàng đợi gửi
	Slow     int64 // kết nối bị ngắt vì hàng đợi gửi đầy
	Denied   int64 // thông báo bị bỏ vì người nhận không đủ quyền đọc (hoặc token gốc hết hạn)
}

// Hub giữ các kết nối websocket và đẩy thông báo của Manager tới đúng kết nối.
type Hub struct {
	cfg WSConfig

	// lookup do Manager gắn: trả project của Subscription websocket đang hoạt động.
	lookup func(ctx context.Context, subID string) (project string, ok bool)

	tokens *Tokens
	feed   *Feed // nil: giao qua Manager trong process

	// group lo upgrade, Origin, giới hạn kết nối, 503/1001/1011 và đóng êm (pkg/wsgroup).
	group *wsgroup.Group

	mu      sync.Mutex // bảo vệ bySub và perUser
	bySub   map[string]map[*wsConn]struct{}
	perUser map[string]int // "project|subject" -> số lượt bind đang hiệu lực

	sent, slow, denied atomic.Int64
}

// NewHub tạo Hub.
func NewHub(cfg WSConfig) (*Hub, error) {
	cfg.defaults()
	tokens := cfg.Tokens
	if tokens == nil {
		var err error
		if tokens, err = NewTokens(TokenConfig{SigningKey: cfg.SigningKey, Previous: cfg.PreviousKeys, URL: cfg.URL, TTL: cfg.TokenTTL, Now: cfg.Now}); err != nil {
			return nil, err
		}
	}
	h := &Hub{cfg: cfg, tokens: tokens, bySub: map[string]map[*wsConn]struct{}{}, perUser: map[string]int{}}
	h.group = wsgroup.New(wsgroup.Config{MaxConns: cfg.MaxConns, MaxConnsPerIP: cfg.MaxConnsPerIP, ClientIP: cfg.ClientIP, CloseTimeout: cfg.CloseTimeout,
		ReadLimit: 64 << 10, OriginPatterns: cfg.OriginPatterns})
	return h, nil
}

// Stats trả bản chụp bộ đếm.
func (h *Hub) Stats() WSStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, set := range h.bySub {
		n += len(set)
	}
	return WSStats{Conns: h.group.Conns(), Bindings: n, Sent: h.sent.Load(), Slow: h.slow.Load(), Denied: h.denied.Load()}
}

// Close ngắt mọi kết nối (StatusGoingAway) và chờ các handler thoát hoặc ctx hết hạn.
func (h *Hub) Close(ctx context.Context) error { return h.group.Close(ctx) }

// ---------------------------------------------------------------- token

// Tokens trả bộ cấp/xác minh token của Hub (định dạng ở ws_token.go).
func (h *Hub) Tokens() *Tokens { return h.tokens }

// TokenOperation là handler của "Subscription/:id/$get-ws-binding-token" (xem Tokens.TokenOperation).
func (h *Hub) TokenOperation(repo fhirrouter.Repository) fhirrouter.Handler {
	return h.tokens.TokenOperation(repo)
}

// ---------------------------------------------------------------- kết nối

type binding struct {
	events int64
	quota  string              // khóa đếm hạn mức bind theo người ("" nếu không đếm)
	who    *fhirauth.Principal // quyền của người bind (nil: không phân quyền)
}

type wsConn struct {
	hub  *Hub
	ws   *websocket.Conn
	send chan []byte

	mu    sync.Mutex // bảo vệ bound
	bound map[string]*binding
}

// Handler là endpoint websocket (gắn ở /ws/subscriptions-r4, không qua xác thực bearer vì token nằm trong thông điệp bind).
func (h *Hub) Handler() http.Handler { return h.group.Handler(h.serve) }

func (h *Hub) serve(ctx context.Context, ws *websocket.Conn) {
	c := &wsConn{hub: h, ws: ws, send: make(chan []byte, h.cfg.SendBuffer), bound: map[string]*binding{}}
	defer h.drop(c)
	c.run(ctx)
}

// drop gỡ mọi binding của kết nối (việc cắt và gỡ kết nối khỏi nhóm do wsgroup làm).
func (h *Hub) drop(c *wsConn) {
	h.mu.Lock()
	c.mu.Lock()
	for id := range c.bound {
		h.removeLocked(c, id)
	}
	c.mu.Unlock()
	h.mu.Unlock()
}

// removeLocked gỡ binding subID của c khỏi bySub, bộ đếm hạn mức và c.bound. Người gọi giữ h.mu rồi c.mu.
func (h *Hub) removeLocked(c *wsConn, subID string) {
	if b, ok := c.bound[subID]; ok {
		if b.quota != "" {
			if h.perUser[b.quota]--; h.perUser[b.quota] <= 0 {
				delete(h.perUser, b.quota)
			}
		}
		delete(c.bound, subID)
	}
	if set := h.bySub[subID]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(h.bySub, subID)
		}
	}
}

// hasBinding cho biết có kết nối nào đang bind Subscription subID (Manager chỉ khớp tiêu chí khi có người nghe).
func (h *Hub) hasBinding(subID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.bySub[subID]) > 0
}

func (c *wsConn) run(parent context.Context) {
	h := c.hub
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	writerDone := make(chan struct{})
	go func() { defer close(writerDone); c.writer(ctx) }()
	defer func() { cancel(); <-writerDone }()

	// Read huỷ theo ctx sẽ đóng kết nối ngay, nên hạn bind dùng đồng hồ riêng đóng kết nối bằng mã policy violation.
	first := true
	bindTimer := time.AfterFunc(h.cfg.BindTimeout, func() { _ = c.ws.Close(websocket.StatusPolicyViolation, "cần bind-with-token") })
	defer bindTimer.Stop()
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			_ = c.ws.Close(websocket.StatusUnsupportedData, "chỉ nhận văn bản")
			return
		}
		var msg struct {
			Type    string `json:"type"`
			Payload struct {
				Token string `json:"token"`
			} `json:"payload"`
		}
		if json.Unmarshal(data, &msg) != nil {
			_ = c.ws.Close(websocket.StatusUnsupportedData, "JSON không hợp lệ")
			return
		}
		switch msg.Type {
		case "bind-with-token":
			if err := c.bind(ctx, msg.Payload.Token); err != nil {
				code := websocket.StatusPolicyViolation
				if errors.Is(err, errUnavailable) {
					code = websocket.StatusTryAgainLater
				}
				_ = c.ws.Close(code, err.Error())
				return
			}
			if first {
				first = false
				bindTimer.Stop()
			}
		case "unbind-from-token":
			if first {
				_ = c.ws.Close(websocket.StatusPolicyViolation, "cần bind-with-token")
				return
			}
			claims, err := h.tokens.parse(msg.Payload.Token)
			if err != nil {
				_ = c.ws.Close(websocket.StatusPolicyViolation, err.Error())
				return
			}
			h.unbind(c, claims.Sub) // chưa bind thì bỏ qua
		case "ping":
			c.enqueue(mustJSON(map[string]any{"type": "pong"}))
		default:
			if first {
				_ = c.ws.Close(websocket.StatusPolicyViolation, "cần bind-with-token")
				return
			}
		}
	}
}

func (c *wsConn) bind(ctx context.Context, token string) error {
	h := c.hub
	claims, err := h.tokens.verify(token)
	if err != nil {
		return err
	}
	if !h.cfg.TokenOnly {
		if h.lookup == nil {
			return errors.New("websocket chưa sẵn sàng")
		}
		project, ok := h.lookup(ctx, claims.Sub)
		if !ok || project != claims.Project {
			return errors.New("Subscription không còn hoạt động")
		}
	}
	var who *fhirauth.Principal
	if len(claims.Snap) > 0 {
		if who, err = fhirauth.ParseSnapshot(claims.Snap); err != nil {
			return errors.New("token không hợp lệ")
		}
		if who.Expired(h.cfg.Now()) {
			return errors.New("access token đã hết hạn")
		}
	}
	quota := ""
	if who != nil && who.Subject != "" && h.cfg.MaxBindingsPerUser > 0 {
		quota = claims.Project + "|" + who.Subject
	}
	added := false
	h.mu.Lock()
	c.mu.Lock()
	if b, dup := c.bound[claims.Sub]; dup {
		b.who = who // bind lại: quyền mới nhất thắng (người dùng tự chịu trên kết nối của mình)
	} else {
		if len(c.bound) >= h.cfg.MaxBindingsPerConn {
			c.mu.Unlock()
			h.mu.Unlock()
			return errors.New("quá nhiều Subscription trên một kết nối")
		}
		if quota != "" {
			if h.perUser[quota] >= h.cfg.MaxBindingsPerUser {
				c.mu.Unlock()
				h.mu.Unlock()
				return errors.New("User has exceeded allotted maximum number of concurrent WebSocket subscriptions: " + strconv.Itoa(h.cfg.MaxBindingsPerUser))
			}
			h.perUser[quota]++
		}
		c.bound[claims.Sub] = &binding{who: who, quota: quota}
		added = true
		if h.bySub[claims.Sub] == nil {
			h.bySub[claims.Sub] = map[*wsConn]struct{}{}
		}
		h.bySub[claims.Sub][c] = struct{}{}
	}
	c.mu.Unlock()
	h.mu.Unlock()
	if h.feed != nil {
		// Sau khi binding đã vào bySub: vòng dọn của Feed không thể bỏ đăng ký trong lúc ensure chờ. Redis hỏng thì bind thất
		// bại (kết nối sẽ không bao giờ nhận được gì) và hoàn tác binding vừa thêm.
		if err := h.feed.ensure(ctx, claims.Sub); err != nil {
			if added {
				h.unbind(c, claims.Sub)
			}
			slog.WarnContext(ctx, "fhirsub: không đăng ký được kênh nhận sự kiện websocket", "subscription", claims.Sub, "error", err)
			return errUnavailable
		}
	}
	c.enqueue(statusBundle(claims.Sub, "handshake", 0, nil, nil))
	return nil
}

// errUnavailable: bind thất bại vì hạ tầng (không phải lỗi của client); client nên thử lại (đóng 1013).
var errUnavailable = errors.New("tạm thời không nhận sự kiện được, thử lại sau")

// enqueue xếp thông điệp vào hàng đợi gửi; hàng đợi đầy thì ngắt kết nối (người nhận quá chậm).
func (c *wsConn) enqueue(msg []byte) {
	select {
	case c.send <- msg:
		c.hub.sent.Add(1)
	default:
		c.hub.slow.Add(1)
		go func() { _ = c.ws.Close(websocket.StatusTryAgainLater, "người nhận quá chậm") }()
	}
}

func (c *wsConn) writer(ctx context.Context) {
	h := c.hub
	tick := time.NewTicker(h.cfg.Heartbeat)
	defer tick.Stop()
	write := func(msg []byte) bool {
		wctx, cancel := context.WithTimeout(ctx, h.cfg.WriteTimeout)
		defer cancel()
		return c.ws.Write(wctx, websocket.MessageText, msg) == nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			if !write(msg) {
				_ = c.ws.CloseNow()
				return
			}
		case <-tick.C:
			c.mu.Lock()
			ids := make([]string, 0, len(c.bound))
			for id := range c.bound {
				ids = append(ids, id)
			}
			c.mu.Unlock()
			for _, id := range ids {
				if !write(statusBundle(id, "heartbeat", 0, nil, nil)) {
					_ = c.ws.CloseNow()
					return
				}
			}
			pctx, cancel := context.WithTimeout(ctx, h.cfg.WriteTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				_ = c.ws.CloseNow()
				return
			}
		}
	}
}

// notify đẩy một sự kiện tới mọi kết nối đã bind Subscription subID.
func (h *Hub) notify(subID string, ev ChangeEvent) {
	h.mu.Lock()
	cs := make([]*wsConn, 0, len(h.bySub[subID]))
	for c := range h.bySub[subID] {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	var doc map[string]any // parse một lần, chỉ khi có người nhận cần kiểm tiêu chí
	parsed := false
	for _, c := range cs {
		c.mu.Lock()
		b := c.bound[subID]
		if b == nil {
			c.mu.Unlock()
			continue
		}
		if b.who != nil {
			if b.who.Expired(h.cfg.Now()) {
				c.mu.Unlock()
				h.unbind(c, subID)
				h.denied.Add(1)
				continue
			}
			if !parsed {
				parsed = true
				if len(ev.Resource) > 0 {
					_ = json.Unmarshal(ev.Resource, &doc)
				}
			}
			if !b.who.CanReadResource(ev.ResourceType, doc) {
				c.mu.Unlock()
				h.denied.Add(1)
				continue
			}
		}
		b.events++
		n := b.events
		c.mu.Unlock()
		c.enqueue(statusBundle(subID, "event-notification", n, &ev, nil))
	}
}

// unbind gỡ binding subID khỏi kết nối c (quyền hết hạn hoặc client yêu cầu); các binding khác của kết nối giữ nguyên.
func (h *Hub) unbind(c *wsConn, subID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	h.removeLocked(c, subID)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// statusBundle dựng Bundle(history) chứa SubscriptionStatus (và resource của sự kiện nếu có).
func statusBundle(subID, typ string, n int64, ev *ChangeEvent, _ any) []byte {
	status := map[string]any{
		"resourceType": "SubscriptionStatus", "status": "active", "type": typ,
		"subscription": map[string]any{"reference": "Subscription/" + subID},
	}
	entries := []any{map[string]any{"resource": status}}
	if ev != nil {
		focus := ev.ResourceType + "/" + ev.ID
		status["eventsSinceSubscriptionStart"] = strconv.FormatInt(n, 10)
		status["notificationEvent"] = []any{map[string]any{
			"eventNumber": strconv.FormatInt(n, 10), "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"focus": map[string]any{"reference": focus},
		}}
		method := map[string]string{Create: "POST", Update: "PUT", Delete: "DELETE"}[ev.Interaction]
		e := map[string]any{"fullUrl": focus, "request": map[string]any{"method": method, "url": focus}}
		if ev.Interaction != Delete && len(ev.Resource) > 0 {
			e["resource"] = json.RawMessage(ev.Resource)
		}
		entries = append(entries, e)
	}
	return mustJSON(map[string]any{"resourceType": "Bundle", "type": "history", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "entry": entries})
}
