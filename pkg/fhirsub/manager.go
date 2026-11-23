package fhirsub

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/messaging"
)

// Các extension của Subscription (cùng URL với Medplum).
const (
	extInteraction = "https://medplum.com/fhir/StructureDefinition/subscription-supported-interaction"
	extSecret      = "https://www.medplum.com/fhir/StructureDefinition/subscription-secret"
	extSecretOld   = "https://www.medplum.com/fhir/StructureDefinition-subscriptionSecret"
)

// Config cấu hình Manager; giá trị 0 dùng mặc định trong ngoặc.
type Config struct {
	Workers      int           // số worker giao song song (4)
	QueueSize    int           // sức chứa hàng đợi giao (1024); đầy thì bỏ và log
	MaxAttempts  int           // số lần thử tối đa cho mỗi lần giao (5)
	BaseDelay    time.Duration // chờ trước lần thử lại đầu tiên, nhân đôi mỗi lần, trần 1 phút (1s)
	Timeout      time.Duration // timeout mỗi request (10s)
	CacheTTL     time.Duration // thời gian giữ danh sách Subscription đang hoạt động (30s)
	AllowPrivate bool          // cho phép giao tới địa chỉ nội bộ (chỉ dev/test)
}

func (c *Config) defaults() {
	if c.Workers <= 0 {
		c.Workers = 4
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 1024
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.CacheTTL <= 0 {
		c.CacheTTL = 30 * time.Second
	}
}

// Stats là bộ đếm hoạt động của Manager.
type Stats struct {
	Matched   int64 // lượt khớp (sự kiện x subscription)
	Delivered int64 // giao thành công
	Retried   int64 // lượt hẹn thử lại
	Failed    int64 // bỏ cuộc (hết lượt thử hoặc lỗi không thử lại được)
	Dropped   int64 // bỏ vì hàng đợi đầy
	Published int64 // (AttachRemote) thông điệp websocket đã phát lên kênh của Subscription
	Unheard   int64 // (AttachRemote) khớp nhưng không có gateway nào đang nghe nên không phát
}

type entry struct {
	sub          *fhir.Subscription
	resourceType string
	req          *fhirsearch.Request
	interactions map[string]bool // rỗng = mọi interaction
	secret       string
	project      string // meta.project của Subscription
	ws           bool   // kênh websocket: thông báo đi qua Hub thay vì HTTP
	headers      [][2]string
	end          time.Time
}

// Manager khớp sự kiện thay đổi với các Subscription đang hoạt động và giao thông báo.
type Manager struct {
	repo   fhirrouter.Repository // repository GỐC (không bọc), để nạp Subscription
	cfg    Config
	client *http.Client

	mu      sync.RWMutex
	cache   []*entry
	loaded  time.Time
	invalid bool

	hub *Hub // nil nếu không bật websocket trong process

	// remote/presence: giao websocket qua tiến trình khác (AttachRemote); loại trừ với hub.
	remote   messaging.Channels
	presence messaging.Presence

	queue  chan *delivery
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	stats  Stats
}

// NewManager tạo Manager. repo là repository gốc dùng để đọc Subscription.
func NewManager(repo fhirrouter.Repository, cfg Config) *Manager {
	cfg.defaults()
	m := &Manager{repo: repo, cfg: cfg, queue: make(chan *delivery, cfg.QueueSize)}
	m.client = newClient(cfg.Timeout, cfg.AllowPrivate)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	return m
}

// Register đăng ký handler với broker (gọi trước khi Start broker).
func (m *Manager) Register(b interface {
	Subscribe(topic string, h messaging.Handler)
}) {
	b.Subscribe(EventType, m.handle)
}

// AttachHub gắn Hub websocket: Subscription kênh websocket được khớp và đẩy qua Hub. Gọi trước khi Start broker, cùng
// RegisterBroadcast.
func (m *Manager) AttachHub(h *Hub) {
	m.hub = h
	h.lookup = m.wsLookup
}

// RegisterBroadcast đăng ký handler websocket nhận MỌI sự kiện trên instance này (mỗi instance tự đẩy tới kết nối của
// mình). Cần broker hỗ trợ messaging.Broadcaster. Gọi trước khi Start broker.
func (m *Manager) RegisterBroadcast(b interface {
	SubscribeBroadcast(topic string, h messaging.Handler)
}) {
	b.SubscribeBroadcast(EventType, m.handleBroadcast)
}

// wsLookup cho Hub biết Subscription websocket còn hoạt động và project của nó.
func (m *Manager) wsLookup(ctx context.Context, subID string) (string, bool) {
	for _, e := range m.active(ctx) {
		if e.ws && e.sub.ID == subID && (e.end.IsZero() || time.Now().Before(e.end)) {
			return e.project, true
		}
	}
	return "", false
}

// Start chạy các worker giao.
func (m *Manager) Start() {
	for i := 0; i < m.cfg.Workers; i++ {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			for {
				select {
				case d := <-m.queue:
					m.attempt(d)
				case <-m.ctx.Done():
					return
				}
			}
		}()
	}
}

// Stop ngừng nhận việc, hủy các lần thử lại đang hẹn và chờ worker thoát (hoặc tới khi ctx hết hạn).
func (m *Manager) Stop(ctx context.Context) error {
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats trả về bản chụp bộ đếm.
func (m *Manager) Stats() Stats {
	return Stats{atomic.LoadInt64(&m.stats.Matched), atomic.LoadInt64(&m.stats.Delivered), atomic.LoadInt64(&m.stats.Retried),
		atomic.LoadInt64(&m.stats.Failed), atomic.LoadInt64(&m.stats.Dropped), atomic.LoadInt64(&m.stats.Published), atomic.LoadInt64(&m.stats.Unheard)}
}

// Invalidate buộc nạp lại danh sách Subscription ở lần dùng kế tiếp.
func (m *Manager) Invalidate() {
	m.mu.Lock()
	m.invalid = true
	m.mu.Unlock()
}

func (m *Manager) active(ctx context.Context) []*entry {
	m.mu.RLock()
	if !m.invalid && time.Since(m.loaded) < m.cfg.CacheTTL {
		c := m.cache
		m.mu.RUnlock()
		return c
	}
	m.mu.RUnlock()
	entries, err := m.load(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		slog.ErrorContext(ctx, "fhirsub: không nạp được Subscription, dùng bản cũ", "error", err)
		return m.cache
	}
	m.cache, m.loaded, m.invalid = entries, time.Now(), false
	return entries
}

func (m *Manager) load(ctx context.Context) ([]*entry, error) {
	req, err := fhirsearch.ParseQuery("Subscription", "status=active")
	if err != nil {
		return nil, err
	}
	req.Count, req.Total = fhirsearch.MaxCount, "none"
	var out []*entry
	for {
		res, err := m.repo.Search(ctx, req)
		if err != nil {
			return nil, err
		}
		for _, r := range res.Matches {
			if sub, ok := r.(*fhir.Subscription); ok {
				if e := buildEntry(sub); e != nil {
					out = append(out, e)
				}
			}
		}
		if !res.More {
			return out, nil
		}
		req.Offset += req.Count
	}
}

func buildEntry(sub *fhir.Subscription) *entry {
	if sub.Channel == nil || (sub.Channel.Type != "rest-hook" && sub.Channel.Type != "websocket") {
		return nil // kênh khác chưa hỗ trợ
	}
	isWS := sub.Channel.Type == "websocket"
	if !isWS {
		if _, err := checkEndpoint(sub.Channel.Endpoint); err != nil {
			slog.Warn("fhirsub: bỏ qua Subscription có endpoint không hợp lệ", "subscription", sub.ID, "error", err)
			return nil
		}
	}
	rt, query, _ := strings.Cut(sub.Criteria, "?")
	req, err := fhirsearch.ParseQuery(rt, query)
	if err != nil {
		slog.Warn("fhirsub: bỏ qua Subscription có criteria không dùng được", "subscription", sub.ID, "criteria", sub.Criteria, "error", err)
		return nil
	}
	if fhirsearch.NeedsResolution(req) {
		slog.Warn("fhirsub: bỏ qua Subscription có criteria chained/_has hoặc modifier terminology (chưa hỗ trợ)", "subscription", sub.ID, "criteria", sub.Criteria)
		return nil
	}
	e := &entry{sub: sub, resourceType: rt, req: req, interactions: map[string]bool{}, ws: isWS}
	if sub.Meta != nil {
		e.project = sub.Meta.Project
	}
	for _, x := range sub.Extension {
		switch x.URL {
		case extInteraction:
			e.interactions[x.ValueCode] = true
		case extSecret, extSecretOld:
			if e.secret == "" {
				e.secret = x.ValueString
			}
		}
	}
	for _, h := range sub.Channel.Header {
		if k, v, ok := strings.Cut(h, ":"); ok && strings.TrimSpace(k) != "" {
			e.headers = append(e.headers, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
		}
	}
	if sub.End != "" {
		if t, err := time.Parse(time.RFC3339, sub.End); err == nil {
			e.end = t
		}
	}
	return e
}

// handle là messaging.Handler (theo nhóm, mỗi sự kiện một instance): khớp sự kiện với Subscription rest-hook và xếp việc
// giao vào hàng đợi (không chặn).
func (m *Manager) handle(ctx context.Context, env messaging.Envelope) error {
	return m.dispatch(ctx, env, false)
}

// handleBroadcast là handler của mọi instance: khớp sự kiện với Subscription websocket và đẩy tới kết nối của instance này.
func (m *Manager) handleBroadcast(ctx context.Context, env messaging.Envelope) error {
	return m.dispatch(ctx, env, true)
}

func (m *Manager) dispatch(ctx context.Context, env messaging.Envelope, wantWS bool) error {
	var ev ChangeEvent
	if err := env.Decode(&ev); err != nil {
		return err
	}
	if ev.ResourceType == "Subscription" {
		m.Invalidate() // Subscription vừa đổi: có hiệu lực ngay trong replica này
	}
	var doc map[string]any
	evProject := resourceProject(ev.Resource)
	for _, e := range m.active(ctx) {
		if e.resourceType != ev.ResourceType {
			continue
		}
		// Subscription websocket: handler broadcast (Hub trong process) hoặc handler nhóm khi giao qua AttachRemote;
		// Subscription khác: chỉ handler nhóm.
		if e.ws {
			if (wantWS && m.hub == nil) || (!wantWS && m.remote == nil) {
				continue
			}
		} else if wantWS {
			continue
		}
		if len(e.interactions) > 0 && !e.interactions[ev.Interaction] {
			continue
		}
		if !e.end.IsZero() && time.Now().After(e.end) {
			continue
		}
		if wantWS && !m.hub.hasBinding(e.sub.ID) {
			continue // không có socket nào đang nghe: không tốn công parse và khớp tiêu chí
		}
		if e.project != evProject {
			continue // Subscription chỉ nhận sự kiện của cùng project (chống rò dữ liệu giữa tenant)
		}
		if doc == nil {
			d := json.NewDecoder(bytes.NewReader(ev.Resource))
			d.UseNumber()
			if err := d.Decode(&doc); err != nil {
				return err
			}
		}
		ok, err := fhirsearch.Match(doc, e.req)
		if err != nil {
			slog.WarnContext(ctx, "fhirsub: lỗi khi khớp criteria", "subscription", e.sub.ID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		atomic.AddInt64(&m.stats.Matched, 1)
		if wantWS {
			m.hub.notify(e.sub.ID, ev)
			continue
		}
		if e.ws {
			m.publishRemote(ctx, e, ev)
			continue
		}
		m.enqueue(newDelivery(e, ev))
	}
	return nil
}

func (m *Manager) enqueue(d *delivery) {
	select {
	case m.queue <- d:
	default:
		atomic.AddInt64(&m.stats.Dropped, 1)
		slog.Error("fhirsub: hàng đợi giao đầy, bỏ thông báo", "subscription", d.subID, "resource", d.resource)
	}
}

// resourceProject lấy meta.project từ JSON của resource ("" nếu không có).
func resourceProject(raw json.RawMessage) string {
	var v struct {
		Meta struct {
			Project string `json:"project"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Meta.Project
}
