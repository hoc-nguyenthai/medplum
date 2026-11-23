package fhirsub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/messaging"
)

// Giao websocket qua tiến trình khác ("khớp ở fhir-server, giao ở gateway").
//
//	fhir-server (Manager.AttachRemote)                       ws-gateway (Hub + Feed)
//	  khớp ChangeEvent với Subscription websocket   ---+
//	  Present(sổ hiện diện của Subscription)? --------+--> chỉ khi có gateway đang giữ socket bind Subscription đó:
//	  PublishChannel("fhirws.sub.<id>", WSMessage)  ------>  Feed nhận -> Hub.notify (lọc theo quyền từng người nhận)
//
// Gateway chỉ nghe kênh của những Subscription đang có kết nối bind (không thấy sự kiện của Subscription khác) và không cần
// cơ sở dữ liệu hay nguồn Subscription: token bind do fhir-server cấp và ký đã chứng minh Subscription hợp lệ lúc cấp.
// Không bền: sự kiện phát lúc gateway khởi động lại hoặc Redis đứt bị mất (at-most-once, như Medplum); client dựa vào
// heartbeat và eventsSinceSubscriptionStart để biết.

// WSChannel là tên kênh của Subscription websocket subID.
func WSChannel(subID string) string { return "fhirws.sub." + subID }

// WSPresenceKey là khóa sổ hiện diện: có member nghĩa là có gateway đang giữ socket bind Subscription subID.
func WSPresenceKey(subID string) string { return "fhirws.bound." + subID }

// WSMessage là thông điệp trên kênh WSChannel.
type WSMessage struct {
	Sub   string      `json:"sub"`
	Event ChangeEvent `json:"event"`
}

// AttachRemote cho Manager giao Subscription websocket qua Channels thay vì Hub trong process: sự kiện khớp được phát lên
// kênh của Subscription khi sổ hiện diện báo có gateway đang nghe. Gọi trước khi Start broker. Loại trừ với AttachHub; KHÔNG
// gọi RegisterBroadcast (khớp một lần cho cả cụm qua handler nhóm, không phải mỗi instance một lần).
func (m *Manager) AttachRemote(ch messaging.Channels, pr messaging.Presence) error {
	if ch == nil || pr == nil {
		return errors.New("fhirsub: AttachRemote cần Channels và Presence")
	}
	if m.hub != nil {
		return errors.New("fhirsub: AttachRemote loại trừ AttachHub")
	}
	m.remote, m.presence = ch, pr
	return nil
}

// publishRemote phát ev lên kênh của Subscription e nếu có gateway đang nghe. Lỗi hạ tầng chỉ được log (không bền, và
// không có chỗ để thử lại mà không làm chậm vòng khớp).
func (m *Manager) publishRemote(ctx context.Context, e *entry, ev ChangeEvent) {
	bound, err := m.presence.Present(ctx, WSPresenceKey(e.sub.ID))
	if err != nil {
		slog.WarnContext(ctx, "fhirsub: không đọc được sổ hiện diện websocket", "subscription", e.sub.ID, "error", err)
		return
	}
	if !bound {
		atomic.AddInt64(&m.stats.Unheard, 1)
		return
	}
	raw, err := json.Marshal(WSMessage{Sub: e.sub.ID, Event: ev})
	if err != nil {
		slog.ErrorContext(ctx, "fhirsub: không mã hóa được thông điệp websocket", "subscription", e.sub.ID, "error", err)
		return
	}
	if err := m.remote.PublishChannel(ctx, WSChannel(e.sub.ID), raw); err != nil {
		slog.WarnContext(ctx, "fhirsub: không phát được thông điệp websocket", "subscription", e.sub.ID, "error", err)
		return
	}
	atomic.AddInt64(&m.stats.Published, 1)
}

// ---------------------------------------------------------------- Feed (phía gateway)

// FeedConfig cấu hình Feed; giá trị 0 dùng mặc định trong ngoặc.
type FeedConfig struct {
	InstanceID  string        // định danh của gateway trong sổ hiện diện (ngẫu nhiên mỗi process)
	PresenceTTL time.Duration // hạn một mục hiện diện nếu không được gia hạn (30s)
	Refresh     time.Duration // chu kỳ gia hạn hiện diện và dọn Subscription không còn socket (PresenceTTL/3)
	Timeout     time.Duration // hạn mỗi lời gọi Redis (2s)
}

func (c *FeedConfig) defaults() {
	if c.InstanceID == "" {
		c.InstanceID = core.NewID()
	}
	if c.PresenceTTL <= 0 {
		c.PresenceTTL = 30 * time.Second
	}
	if c.Refresh <= 0 {
		c.Refresh = c.PresenceTTL / 3
	}
	if c.Timeout <= 0 {
		c.Timeout = 2 * time.Second
	}
}

// Feed nối Hub với kênh của từng Subscription: khi Hub có kết nối bind một Subscription lần đầu, Feed đăng ký nghe kênh của
// nó rồi MỚI ghi sổ hiện diện (thứ tự này bảo đảm bên phát thấy "có người nghe" thì người nghe đã sẵn sàng); định kỳ gia hạn
// sổ và bỏ đăng ký những Subscription không còn socket nào.
type Feed struct {
	hub *Hub
	ch  messaging.Channels
	pr  messaging.Presence
	cfg FeedConfig

	mu   sync.Mutex
	subs map[string]func() // Subscription -> hủy đăng ký kênh

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	received, malformed atomic.Int64
}

// NewFeed gắn một Feed vào hub (Hub chỉ có một Feed) và chạy vòng gia hạn. Phải gọi trước khi Hub nhận kết nối.
func NewFeed(h *Hub, ch messaging.Channels, pr messaging.Presence, cfg FeedConfig) (*Feed, error) {
	if h == nil || ch == nil || pr == nil {
		return nil, errors.New("fhirsub: NewFeed cần Hub, Channels và Presence")
	}
	if h.feed != nil {
		return nil, errors.New("fhirsub: Hub đã có Feed")
	}
	cfg.defaults()
	f := &Feed{hub: h, ch: ch, pr: pr, cfg: cfg, subs: map[string]func(){}}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	h.feed = f
	f.wg.Add(1)
	go f.loop()
	return f, nil
}

// FeedStats là bộ đếm của Feed.
type FeedStats struct {
	Subscribed int   // Subscription đang nghe kênh
	Received   int64 // thông điệp nhận và đã giao cho Hub
	Malformed  int64 // thông điệp hỏng hoặc lệch Subscription, bị bỏ
}

// Stats trả bản chụp bộ đếm.
func (f *Feed) Stats() FeedStats {
	f.mu.Lock()
	n := len(f.subs)
	f.mu.Unlock()
	return FeedStats{Subscribed: n, Received: f.received.Load(), Malformed: f.malformed.Load()}
}

func (f *Feed) call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, f.cfg.Timeout)
}

// ensure bảo đảm Feed đang nghe kênh của subID và sổ hiện diện có mục của gateway này. Hub gọi SAU khi đã ghi binding vào bySub
// (nên vòng dọn không thể bỏ đăng ký trong lúc ensure đang chờ). Trả lỗi nếu Redis không dùng được: bind thất bại thay vì
// chấp nhận kết nối sẽ không bao giờ nhận được gì.
func (f *Feed) ensure(ctx context.Context, subID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.subs[subID]; ok {
		return nil
	}
	cctx, cancel := f.call(ctx)
	defer cancel()
	stop, err := f.ch.SubscribeChannel(cctx, WSChannel(subID), func(p []byte) { f.deliver(subID, p) })
	if err != nil {
		return err
	}
	if err := f.pr.Mark(cctx, WSPresenceKey(subID), f.cfg.InstanceID, f.cfg.PresenceTTL); err != nil {
		stop()
		return err
	}
	f.subs[subID] = stop
	return nil
}

func (f *Feed) deliver(subID string, payload []byte) {
	var m WSMessage
	if err := json.Unmarshal(payload, &m); err != nil || m.Sub != subID {
		f.malformed.Add(1)
		slog.Warn("fhirsub: thông điệp websocket hỏng hoặc sai kênh", "subscription", subID)
		return
	}
	f.received.Add(1)
	f.hub.notify(subID, m.Event)
}

func (f *Feed) loop() {
	defer f.wg.Done()
	t := time.NewTicker(f.cfg.Refresh)
	defer t.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-t.C:
			f.reconcile()
		}
	}
}

// reconcile bỏ đăng ký (và gỡ khỏi sổ) những Subscription không còn socket, gia hạn sổ cho phần còn lại.
func (f *Feed) reconcile() {
	f.mu.Lock()
	var keep []string
	for sub, stop := range f.subs {
		if f.hub.hasBinding(sub) {
			keep = append(keep, sub)
			continue
		}
		stop()
		delete(f.subs, sub)
		ctx, cancel := f.call(f.ctx)
		if err := f.pr.Unmark(ctx, WSPresenceKey(sub), f.cfg.InstanceID); err != nil {
			slog.Warn("fhirsub: không gỡ được sổ hiện diện websocket", "subscription", sub, "error", err)
		}
		cancel()
	}
	f.mu.Unlock()
	for _, sub := range keep { // ngoài khóa: Redis chậm không chặn bind mới
		ctx, cancel := f.call(f.ctx)
		if err := f.pr.Mark(ctx, WSPresenceKey(sub), f.cfg.InstanceID, f.cfg.PresenceTTL); err != nil {
			slog.Warn("fhirsub: không gia hạn được sổ hiện diện websocket", "subscription", sub, "error", err)
		}
		cancel()
	}
}

// Stop dừng vòng gia hạn, bỏ đăng ký mọi kênh và gỡ gateway khỏi sổ để bên phát thôi gửi ngay, không chờ hết hạn.
func (f *Feed) Stop(ctx context.Context) error {
	f.cancel()
	f.wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	var firstErr error
	for sub, stop := range f.subs {
		stop()
		cctx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
		if err := f.pr.Unmark(cctx, WSPresenceKey(sub), f.cfg.InstanceID); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
		delete(f.subs, sub)
	}
	return firstErr
}
