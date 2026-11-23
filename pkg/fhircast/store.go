package fhircast

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// CurrentContext là ngữ cảnh hiện tại của một topic: {context.type, context, context.versionId}. Context là danh sách
// {key, resource...} giữ dạng tự do. Ngữ cảnh rỗng có Type "" và Context rỗng.
type CurrentContext struct {
	Type      string           `json:"context.type"`
	VersionID string           `json:"context.versionId,omitempty"`
	Context   []map[string]any `json:"context"`
}

// Hạn mức và lỗi của kho.
var (
	ErrTooManyContexts = errors.New("fhircast: quá nhiều ngữ cảnh đã lưu cho topic")
	ErrContextTooLarge = errors.New("fhircast: ngữ cảnh vượt kích thước tối đa")
)

// Store là kho trạng thái của Hub. Mọi khóa có hạn: không có PHI tồn vô hạn.
type Store interface {
	// PutSubscription ghi đăng ký của endpoint, hết hạn sau ttl (không quá lease).
	PutSubscription(ctx context.Context, endpoint string, s Subscription, ttl time.Duration) error
	// GetSubscription đọc; không có hoặc dữ liệu hỏng thì ok=false (không lỗi).
	GetSubscription(ctx context.Context, endpoint string) (s Subscription, ok bool, err error)
	DeleteSubscription(ctx context.Context, endpoint string) error

	// GetContext trả ngữ cảnh hiện tại của (project, topic); ok=false nếu không có.
	GetContext(ctx context.Context, project, topic string) (cc CurrentContext, ok bool, err error)
	// SetContext ghi vô điều kiện (open/close dùng, như Medplum), làm mới hạn.
	SetContext(ctx context.Context, project, topic string, cc CurrentContext) error
	// CompareAndSetContext chỉ ghi nếu ngữ cảnh đang lưu có context.versionId == expected. false: không có hoặc đã đổi.
	CompareAndSetContext(ctx context.Context, project, topic, expected string, cc CurrentContext) (bool, error)
	// ClearContext xóa ngữ cảnh hiện tại.
	ClearContext(ctx context.Context, project, topic string) error

	// Ngữ cảnh đã lưu theo báo cáo (để khôi phục khi mở lại): trường là id báo cáo.
	SaveReportContext(ctx context.Context, project, topic, reportID string, cc CurrentContext) error
	GetReportContext(ctx context.Context, project, topic, reportID string) (cc CurrentContext, ok bool, err error)
	DeleteReportContext(ctx context.Context, project, topic, reportID string) error
	DeleteReportContexts(ctx context.Context, project, topic string) error

	// UserTopic trả topic ổn định của người dùng (tạo nếu chưa có) và gia hạn nó: ổn định chừng nào còn xin ít nhất mỗi lease.
	UserTopic(ctx context.Context, userID string) (string, error)
}

// DecodeSubscription giải mã và kiểm đăng ký đọc từ kho: dữ liệu hỏng, thiếu trường, sự kiện không phải chuỗi hay phiên bản
// lạ đều đọc ra "không có đăng ký" (caller đã biết xử lý bằng cách từ chối endpoint) thay vì chảy xuống như đối tượng hỏng.
func DecodeSubscription(b []byte) (Subscription, bool) {
	var raw struct {
		ProjectID *string   `json:"projectId"`
		Topic     *string   `json:"topic"`
		Events    []any     `json:"events"`
		Version   Version   `json:"version"`
		User      string    `json:"userSubject"`
		Expires   time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.ProjectID == nil || raw.Topic == nil || raw.Events == nil || !raw.Version.Valid() {
		return Subscription{}, false
	}
	ev := make([]string, 0, len(raw.Events))
	for _, e := range raw.Events {
		s, ok := e.(string)
		if !ok {
			return Subscription{}, false
		}
		ev = append(ev, s)
	}
	return Subscription{ProjectID: *raw.ProjectID, Topic: *raw.Topic, Events: ev, Version: raw.Version, UserSubject: raw.User, ExpiresAt: raw.Expires}, true
}

// DecodeContext giải mã ngữ cảnh đọc từ kho; dữ liệu hỏng đọc ra "không có".
func DecodeContext(b []byte) (CurrentContext, bool) {
	var cc CurrentContext
	if err := json.Unmarshal(b, &cc); err != nil {
		return CurrentContext{}, false
	}
	if cc.Context == nil {
		cc.Context = []map[string]any{}
	}
	return cc, true
}

// StoreOptions cấu hình kho; giá trị 0 dùng mặc định.
type StoreOptions struct {
	ContextTTL        time.Duration // DefaultContextTTL
	Lease             time.Duration // DefaultLease: hạn của topic người dùng
	MaxContextBytes   int           // 256 KiB
	MaxReportContexts int           // 64: số ngữ cảnh đã lưu theo báo cáo mỗi topic
	Now               func() time.Time
}

func (o *StoreOptions) defaults() {
	if o.ContextTTL <= 0 {
		o.ContextTTL = DefaultContextTTL
	}
	if o.Lease <= 0 {
		o.Lease = DefaultLease
	}
	if o.MaxContextBytes <= 0 {
		o.MaxContextBytes = 256 << 10
	}
	if o.MaxReportContexts <= 0 {
		o.MaxReportContexts = 64
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// encodeContext mã hóa và kiểm kích thước.
func (o StoreOptions) encodeContext(cc CurrentContext) ([]byte, error) {
	if cc.Context == nil {
		cc.Context = []map[string]any{}
	}
	b, err := json.Marshal(cc)
	if err != nil {
		return nil, err
	}
	if len(b) > o.MaxContextBytes {
		return nil, ErrContextTooLarge
	}
	return b, nil
}

type memEntry struct {
	val []byte
	exp time.Time
}

// MemoryStore là Store trong bộ nhớ: cho dev/test và một instance duy nhất (không chia sẻ giữa tiến trình).
type MemoryStore struct {
	opt StoreOptions
	mu  sync.Mutex
	kv  map[string]memEntry
	h   map[string]map[string][]byte // hash ngữ cảnh đã lưu: khóa -> trường -> giá trị
	hx  map[string]time.Time
}

// NewMemoryStore tạo kho bộ nhớ.
func NewMemoryStore(opt StoreOptions) *MemoryStore {
	opt.defaults()
	return &MemoryStore{opt: opt, kv: map[string]memEntry{}, h: map[string]map[string][]byte{}, hx: map[string]time.Time{}}
}

func subKey(endpoint string) string   { return "ep:" + endpoint }
func ctxKey(p, t string) string       { return "p:" + p + ":t:" + TopicHash(t) + ":latest" }
func ctxsKey(p, t string) string      { return "p:" + p + ":t:" + TopicHash(t) + ":ctxs" }
func userTopicKey(user string) string { return "user-topic:" + user }

// get trả giá trị còn hạn (gọi khi đã giữ khóa); mục hết hạn bị xóa.
func (m *MemoryStore) get(k string) ([]byte, bool) {
	e, ok := m.kv[k]
	if !ok {
		return nil, false
	}
	if !e.exp.After(m.opt.Now()) {
		delete(m.kv, k)
		return nil, false
	}
	return e.val, true
}

func (m *MemoryStore) put(k string, v []byte, ttl time.Duration) {
	m.kv[k] = memEntry{val: v, exp: m.opt.Now().Add(ttl)}
}

func (m *MemoryStore) PutSubscription(_ context.Context, endpoint string, s Subscription, ttl time.Duration) error {
	if ttl <= 0 || ttl > m.opt.Lease {
		ttl = m.opt.Lease
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.put(subKey(endpoint), b, ttl)
	return nil
}

func (m *MemoryStore) GetSubscription(_ context.Context, endpoint string) (Subscription, bool, error) {
	m.mu.Lock()
	b, ok := m.get(subKey(endpoint))
	m.mu.Unlock()
	if !ok {
		return Subscription{}, false, nil
	}
	s, ok := DecodeSubscription(b)
	return s, ok, nil
}

func (m *MemoryStore) DeleteSubscription(_ context.Context, endpoint string) error {
	m.mu.Lock()
	delete(m.kv, subKey(endpoint))
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) GetContext(_ context.Context, p, t string) (CurrentContext, bool, error) {
	m.mu.Lock()
	b, ok := m.get(ctxKey(p, t))
	m.mu.Unlock()
	if !ok {
		return CurrentContext{}, false, nil
	}
	cc, ok := DecodeContext(b)
	return cc, ok, nil
}

func (m *MemoryStore) SetContext(_ context.Context, p, t string, cc CurrentContext) error {
	b, err := m.opt.encodeContext(cc)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.put(ctxKey(p, t), b, m.opt.ContextTTL)
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) CompareAndSetContext(_ context.Context, p, t, expected string, cc CurrentContext) (bool, error) {
	b, err := m.opt.encodeContext(cc)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.get(ctxKey(p, t))
	if !ok {
		return false, nil
	}
	old, ok := DecodeContext(cur)
	if !ok || old.VersionID != expected {
		return false, nil
	}
	m.put(ctxKey(p, t), b, m.opt.ContextTTL)
	return true, nil
}

func (m *MemoryStore) ClearContext(_ context.Context, p, t string) error {
	m.mu.Lock()
	delete(m.kv, ctxKey(p, t))
	m.mu.Unlock()
	return nil
}

// hash trả bảng trường của khóa còn hạn (đã giữ khóa).
func (m *MemoryStore) hash(k string) map[string][]byte {
	if exp, ok := m.hx[k]; ok && !exp.After(m.opt.Now()) {
		delete(m.h, k)
		delete(m.hx, k)
	}
	return m.h[k]
}

func (m *MemoryStore) SaveReportContext(_ context.Context, p, t, id string, cc CurrentContext) error {
	b, err := m.opt.encodeContext(cc)
	if err != nil {
		return err
	}
	k := ctxsKey(p, t)
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.hash(k)
	if _, exists := f[id]; !exists && len(f) >= m.opt.MaxReportContexts {
		return ErrTooManyContexts
	}
	if f == nil {
		f = map[string][]byte{}
		m.h[k] = f
	}
	f[id] = b
	m.hx[k] = m.opt.Now().Add(m.opt.ContextTTL)
	return nil
}

func (m *MemoryStore) GetReportContext(_ context.Context, p, t, id string) (CurrentContext, bool, error) {
	m.mu.Lock()
	b, ok := m.hash(ctxsKey(p, t))[id]
	m.mu.Unlock()
	if !ok {
		return CurrentContext{}, false, nil
	}
	cc, ok := DecodeContext(b)
	return cc, ok, nil
}

func (m *MemoryStore) DeleteReportContext(_ context.Context, p, t, id string) error {
	k := ctxsKey(p, t)
	m.mu.Lock()
	if f := m.hash(k); f != nil {
		delete(f, id)
		if len(f) == 0 {
			delete(m.h, k)
			delete(m.hx, k)
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) DeleteReportContexts(_ context.Context, p, t string) error {
	k := ctxsKey(p, t)
	m.mu.Lock()
	delete(m.h, k)
	delete(m.hx, k)
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) UserTopic(_ context.Context, userID string) (string, error) {
	k := userTopicKey(userID)
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.get(k)
	if !ok {
		v = []byte(NewID())
	}
	m.put(k, v, m.opt.Lease)
	return string(v), nil
}
