package fhircast

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventCategory(t *testing.T) { // ca 36
	for in, want := range map[string]Category{
		"DiagnosticReport-open": CategoryOpen, "Patient-open": CategoryOpen, "PATIENT-OPEN": CategoryOpen,
		"Patient-close": CategoryClose, "DiagnosticReport-close": CategoryClose,
		"DiagnosticReport-update": CategoryUpdate, "diagnosticreport-UPDATE": CategoryUpdate,
		"DiagnosticReport-select": CategorySelect,
		"Home-open":               CategoryOther, "home-OPEN": CategoryOther,
		"syncerror": CategoryOther, "userlogout": CategoryOther, "heartbeat": CategoryOther, "Patient-update": CategoryOther,
	} {
		if got := EventCategory(in); got != want {
			t.Errorf("EventCategory(%q)=%q, muốn %q", in, got, want)
		}
	}
}

func TestParseEvents(t *testing.T) { // ca 38
	want := []string{"Patient-open", "Patient-close"}
	for _, in := range []string{"Patient-open,Patient-close", "Patient-open, Patient-close", " Patient-open , Patient-close "} {
		if got := ParseEvents(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseEvents(%q)=%q", in, got)
		}
	}
	if got := ParseEvents("Patient-open Patient-close"); len(got) != 1 || got[0] != "Patient-open Patient-close" {
		t.Errorf("khoảng trắng không được ngăn cách hai sự kiện: %q", got)
	}
	for _, in := range []string{"", " ", ",", " , "} {
		if got := ParseEvents(in); len(got) != 0 {
			t.Errorf("ParseEvents(%q)=%q, muốn rỗng", in, got)
		}
	}
}

func TestExtractEndpoint(t *testing.T) { // ca 39
	for in, want := range map[string]string{
		"ws://localhost:8103/ws/fhircast/abc-123": "abc-123", "abc-123": "abc-123", "wss://h/ws/fhircast/abc?token=xyz": "abc",
		"wss://h/ws/fhircast/abc#frag": "abc", "wss://h/ws/fhircast/abc/": "abc", "wss://h/ws/fhircast/abc//": "abc",
		"": "", "?token=xyz": "", "#x": "", "/": "",
	} {
		if got := ExtractEndpoint(in); got != want {
			t.Errorf("ExtractEndpoint(%q)=%q, muốn %q", in, got, want)
		}
	}
}

func TestExtractAnchorType(t *testing.T) {
	for in, want := range map[string]string{"Patient-open": "Patient", "imagingstudy-close": "ImagingStudy", "ENCOUNTER-open": "Encounter", "DiagnosticReport-update": "DiagnosticReport"} {
		if got, err := ExtractAnchorType(in); err != nil || got != want {
			t.Errorf("ExtractAnchorType(%q)=%q,%v", in, got, err)
		}
	}
	for _, in := range []string{"Observation-open", "syncerror", "", "-open"} {
		if _, err := ExtractAnchorType(in); err != ErrInvalidAnchor || err.Error() != "Invalid anchor resource type" {
			t.Errorf("ExtractAnchorType(%q) phải lỗi, được %v", in, err)
		}
	}
}

func TestSupportedEventsAreTheFourteen(t *testing.T) {
	if len(SupportedEvents) != 14 {
		t.Fatalf("%d sự kiện", len(SupportedEvents))
	}
	if !IsSupportedEvent("patient-OPEN") || IsSupportedEvent("Observation-open") || IsSupportedEvent("") {
		t.Error("IsSupportedEvent sai")
	}
}

func TestTopicHashAndValidation(t *testing.T) {
	if TopicHash("a:b") == TopicHash("a") || TopicHash("x") != TopicHash("x") {
		t.Error("TopicHash phải xác định và phân biệt")
	}
	if strings.ContainsAny(TopicHash("a:b/c"), ":/+=") {
		t.Error("TopicHash phải an toàn trong khóa")
	}
	if !ValidTopic("abc") || !ValidTopic(strings.Repeat("a", MaxTopicBytes)) {
		t.Error("topic hợp lệ bị từ chối")
	}
	for _, bad := range []string{"", strings.Repeat("a", MaxTopicBytes+1), "a\nb", "a\x00", "a\x7f"} {
		if ValidTopic(bad) {
			t.Errorf("topic %q phải bị từ chối", bad)
		}
	}
}

func TestNewIDIsUUIDv4AndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewID()
		if len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) || seen[id] {
			t.Fatalf("UUID v4 sai hoặc trùng: %q", id)
		}
		seen[id] = true
	}
}

// ---- lọc thông điệp theo người nhận

func msg(t *testing.T, payload map[string]any, target string) []byte {
	t.Helper()
	b, err := MarshalChannelMessage(payload, target)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func eventPayload(name string) map[string]any {
	return map[string]any{"id": "1", "event": map[string]any{"hub.event": name, "hub.topic": "t"}}
}

func TestMessageForSubscriber(t *testing.T) {
	sub := Subscription{Topic: "t", Events: []string{"Patient-open", "DiagnosticReport-select"}}
	cases := []struct {
		name string
		raw  []byte
		ep   string
		ok   bool
	}{
		{"đã đăng ký", msg(t, eventPayload("Patient-open"), ""), "e1", true},
		{"không phân biệt hoa thường", msg(t, eventPayload("patient-OPEN"), ""), "e1", true},
		{"chưa đăng ký bị lọc", msg(t, eventPayload("Patient-close"), ""), "e1", false},
		{"heartbeat luôn tới", msg(t, eventPayload("heartbeat"), ""), "e1", true},
		{"syncerror luôn tới", msg(t, eventPayload("SyncError"), ""), "e1", true},
		{"điều khiển Hub (không hub.event) luôn tới", msg(t, Denied("t", nil, "x"), ""), "e1", true},
		{"target đúng", msg(t, Denied("t", nil, "x"), "e1"), "e1", true},
		{"target của người khác bị bỏ", msg(t, Denied("t", nil, "x"), "e2"), "e1", false},
		{"target chặn cả sự kiện đã đăng ký", msg(t, eventPayload("Patient-open"), "e2"), "e1", false},
		{"không có payload", []byte(`{"target":"e1"}`), "e1", false},
		{"payload null", []byte(`{"payload":null}`), "e1", false},
		{"không phải JSON", []byte(`not json`), "e1", false},
		{"hub.event không phải chuỗi thì coi như điều khiển", []byte(`{"payload":{"event":{"hub.event":42}}}`), "e1", true},
	}
	for _, c := range cases {
		p, ok := MessageForSubscriber(c.raw, c.ep, sub)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, muốn %v", c.name, ok, c.ok)
		}
		if ok && p == nil {
			t.Errorf("%s: payload nil", c.name)
		}
	}
}

func TestTargetNeverReachesTheWire(t *testing.T) { // F-6
	p, ok := MessageForSubscriber(msg(t, Denied("t", []string{"a"}, "r"), "secret-endpoint"), "secret-endpoint", Subscription{})
	if !ok {
		t.Fatal("phải tới")
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "secret-endpoint") || strings.Contains(string(b), "target") {
		t.Errorf("payload lên dây lộ địa chỉ: %s", b)
	}
}

func TestConfirmationShapes(t *testing.T) {
	sub := Subscription{Topic: "t", Events: []string{"Patient-open", "Patient-close"}, Version: STU3}
	want3 := map[string]any{"hub.mode": "subscribe", "hub.topic": "t", "hub.events": "Patient-open,Patient-close", "hub.lease_seconds": 3600}
	if got := Confirmation(sub); !reflect.DeepEqual(got, want3) {
		t.Errorf("STU3: %v", got)
	}
	sub.Version = STU2
	got := Confirmation(sub)
	if len(got) != 8 {
		t.Fatalf("STU2 phải có 8 trường: %v", got)
	}
	for _, k := range []string{"hub.callback", "hub.channel", "hub.secret", "hub.subscriber"} {
		if v, ok := got[k]; !ok || v != "" {
			t.Errorf("STU2 thiếu %s rỗng", k)
		}
	}
	for k, v := range want3 {
		if got[k] != v {
			t.Errorf("STU2 lệch %s", k)
		}
	}
	if d := InvalidEndpoint(); d["hub.mode"] != "denied" || d["hub.topic"] != "" || d["hub.events"] != "" || d["hub.reason"] != "invalid endpoint" {
		t.Errorf("InvalidEndpoint: %v", d)
	}
}

func TestHeartbeatShape(t *testing.T) {
	h := Heartbeat("topic-1", "id-1", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), HeartbeatPeriod)
	ev := h["event"].(map[string]any)
	if ev["hub.event"] != "heartbeat" || ev["hub.topic"] != "topic-1" || h["id"] != "id-1" || h["timestamp"] != "2026-01-02T03:04:05Z" {
		t.Errorf("heartbeat: %v", h)
	}
	c := ev["context"].([]any)[0].(map[string]any)
	if c["key"] != "period" || c["decimal"] != "10" {
		t.Errorf("context heartbeat: %v", c)
	}
}

// ---- Store (bộ nhớ)

type clock struct{ t atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.t.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *clock) now() time.Time          { return time.Unix(0, c.t.Load()).UTC() }
func (c *clock) advance(d time.Duration) { c.t.Add(int64(d)) }

func TestSubscriptionSetGetDelete(t *testing.T) { // ca 40
	ctx := context.Background()
	clk := newClock()
	st := NewMemoryStore(StoreOptions{Now: clk.now})
	sub := Subscription{ProjectID: "p", Topic: "t", Events: []string{"Patient-open"}, Version: STU3, UserSubject: "u"}
	if _, ok, _ := st.GetSubscription(ctx, "e"); ok {
		t.Fatal("chưa có")
	}
	if err := st.PutSubscription(ctx, "e", sub, 10*DefaultLease); err != nil { // xin quá lease thì bị kẹp
		t.Fatal(err)
	}
	got, ok, _ := st.GetSubscription(ctx, "e")
	if !ok || !reflect.DeepEqual(got, sub) {
		t.Fatalf("đọc lại: %+v ok=%v", got, ok)
	}
	if e := st.kv[subKey("e")].exp; e.After(clk.now().Add(DefaultLease)) {
		t.Errorf("hạn %v vượt lease", e)
	}
	clk.advance(DefaultLease - time.Second)
	if _, ok, _ := st.GetSubscription(ctx, "e"); !ok {
		t.Error("chưa hết hạn")
	}
	clk.advance(2 * time.Second)
	if _, ok, _ := st.GetSubscription(ctx, "e"); ok {
		t.Error("phải hết hạn đúng lease")
	}
	_ = st.PutSubscription(ctx, "e", sub, time.Minute)
	_ = st.DeleteSubscription(ctx, "e")
	if _, ok, _ := st.GetSubscription(ctx, "e"); ok {
		t.Error("đã xóa")
	}
}

func TestCorruptSubscriptionReadsAsNone(t *testing.T) { // ca 41
	for _, raw := range []string{
		`not json`, `{"projectId":"p","topic":"t"}`, `{"projectId":"p","topic":"t","events":[42],"version":"STU3"}`,
		`{"projectId":"p","topic":"t","events":[],"version":"STU4"}`, `{"topic":"t","events":[],"version":"STU3"}`, `null`, ``,
	} {
		st := NewMemoryStore(StoreOptions{})
		st.kv[subKey("e")] = memEntry{val: []byte(raw), exp: time.Now().Add(time.Hour)}
		if _, ok, err := st.GetSubscription(context.Background(), "e"); ok || err != nil {
			t.Errorf("%q phải đọc ra không có: ok=%v err=%v", raw, ok, err)
		}
	}
}

func ctxOf(typ, ver string) CurrentContext {
	return CurrentContext{Type: typ, VersionID: ver, Context: []map[string]any{{"key": "patient"}}}
}

func TestCompareAndSetContext(t *testing.T) { // ca 42
	ctx := context.Background()
	st := NewMemoryStore(StoreOptions{})
	if ok, _ := st.CompareAndSetContext(ctx, "p", "t", "v1", ctxOf("Patient", "v2")); ok {
		t.Error("không có ngữ cảnh thì không được thiết lập")
	}
	if _, ok, _ := st.GetContext(ctx, "p", "t"); ok {
		t.Error("không được tạo ngữ cảnh")
	}
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v1"))
	if ok, _ := st.CompareAndSetContext(ctx, "p", "t", "v1", ctxOf("Patient", "v2")); !ok {
		t.Fatal("còn ở phiên bản kỳ vọng thì phải thay")
	}
	if cc, _, _ := st.GetContext(ctx, "p", "t"); cc.VersionID != "v2" {
		t.Errorf("sau thay: %v", cc.VersionID)
	}
	if ok, _ := st.CompareAndSetContext(ctx, "p", "t", "v1", ctxOf("Patient", "v3")); ok {
		t.Error("đã đi tiếp thì không được ghi đè")
	}
	if cc, _, _ := st.GetContext(ctx, "p", "t"); cc.VersionID != "v2" {
		t.Errorf("bị ghi đè: %v", cc.VersionID)
	}
}

func TestCompareAndSetRaceExactlyOneWins(t *testing.T) { // F-9
	ctx := context.Background()
	st := NewMemoryStore(StoreOptions{})
	_ = st.SetContext(ctx, "p", "t", ctxOf("DiagnosticReport", "v0"))
	const n = 50
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if ok, _ := st.CompareAndSetContext(ctx, "p", "t", "v0", ctxOf("DiagnosticReport", "w"+string(rune('a'+i%26)))); ok {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d người thắng, muốn đúng 1", wins.Load())
	}
}

func TestContextIsolatedByProjectAndTopic(t *testing.T) { // F-3
	ctx := context.Background()
	st := NewMemoryStore(StoreOptions{})
	_ = st.SetContext(ctx, "A", "same", ctxOf("Patient", "a"))
	_ = st.SetContext(ctx, "B", "same", ctxOf("Patient", "b"))
	a, _, _ := st.GetContext(ctx, "A", "same")
	b, _, _ := st.GetContext(ctx, "B", "same")
	if a.VersionID != "a" || b.VersionID != "b" {
		t.Errorf("hai project ghi độc lập: %v %v", a.VersionID, b.VersionID)
	}
	if _, ok, _ := st.GetContext(ctx, "A", "other"); ok {
		t.Error("topic khác phải trống")
	}
	// topic chứa ":" không va chạm với project/topic khác
	_ = st.SetContext(ctx, "A", "x:t:y", ctxOf("Patient", "c"))
	if _, ok, _ := st.GetContext(ctx, "A:t:x", "y"); ok {
		t.Error("va chạm khóa giữa (project, topic)")
	}
}

func TestContextExpiresAndClose(t *testing.T) { // F-10, F-7
	ctx := context.Background()
	clk := newClock()
	st := NewMemoryStore(StoreOptions{Now: clk.now, ContextTTL: time.Hour})
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v"))
	_ = st.SaveReportContext(ctx, "p", "t", "r1", ctxOf("DiagnosticReport", "v"))
	if e := st.kv[ctxKey("p", "t")].exp; !e.After(clk.now()) {
		t.Error("khóa ngữ cảnh phải có hạn")
	}
	clk.advance(59 * time.Minute)
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v2")) // ghi làm mới hạn
	clk.advance(2 * time.Minute)
	if _, ok, _ := st.GetContext(ctx, "p", "t"); !ok {
		t.Error("ghi phải làm mới hạn")
	}
	if _, ok, _ := st.GetReportContext(ctx, "p", "t", "r1"); ok {
		t.Error("ngữ cảnh đã lưu phải hết hạn theo TTL")
	}
	clk.advance(2 * time.Hour)
	if _, ok, _ := st.GetContext(ctx, "p", "t"); ok {
		t.Error("ngữ cảnh phải biến mất sau TTL")
	}
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v"))
	_ = st.ClearContext(ctx, "p", "t")
	if _, ok, _ := st.GetContext(ctx, "p", "t"); ok {
		t.Error("close phải xóa")
	}
}

func TestReportContexts(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore(StoreOptions{MaxReportContexts: 2})
	for _, id := range []string{"r1", "r2"} {
		if err := st.SaveReportContext(ctx, "p", "t", id, ctxOf("DiagnosticReport", id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveReportContext(ctx, "p", "t", "r3", ctxOf("DiagnosticReport", "r3")); err != ErrTooManyContexts {
		t.Errorf("trần số trường: %v", err)
	}
	if err := st.SaveReportContext(ctx, "p", "t", "r1", ctxOf("DiagnosticReport", "r1b")); err != nil {
		t.Errorf("ghi lại trường có sẵn không tính thêm: %v", err)
	}
	if cc, ok, _ := st.GetReportContext(ctx, "p", "t", "r1"); !ok || cc.VersionID != "r1b" {
		t.Errorf("r1: %+v", cc)
	}
	_ = st.DeleteReportContext(ctx, "p", "t", "r1")
	if _, ok, _ := st.GetReportContext(ctx, "p", "t", "r1"); ok {
		t.Error("r1 phải bị xóa")
	}
	if _, ok, _ := st.GetReportContext(ctx, "other", "t", "r2"); ok {
		t.Error("project khác không thấy")
	}
	_ = st.DeleteReportContexts(ctx, "p", "t")
	if _, ok, _ := st.GetReportContext(ctx, "p", "t", "r2"); ok {
		t.Error("xóa hết")
	}
}

func TestContextSizeLimit(t *testing.T) { // F-7
	st := NewMemoryStore(StoreOptions{MaxContextBytes: 200})
	big := CurrentContext{Type: "Patient", VersionID: "v", Context: []map[string]any{{"k": strings.Repeat("x", 500)}}}
	ctx := context.Background()
	if err := st.SetContext(ctx, "p", "t", big); err != ErrContextTooLarge {
		t.Errorf("SetContext: %v", err)
	}
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v"))
	if ok, err := st.CompareAndSetContext(ctx, "p", "t", "v", big); ok || err != ErrContextTooLarge {
		t.Errorf("CAS: %v %v", ok, err)
	}
	if err := st.SaveReportContext(ctx, "p", "t", "r", big); err != ErrContextTooLarge {
		t.Errorf("SaveReportContext: %v", err)
	}
}

func TestEmptyContextEncodesAsArray(t *testing.T) {
	b, _ := json.Marshal(CurrentContext{})
	cc, ok := DecodeContext([]byte(`{"context.type":""}`))
	if !ok || cc.Context == nil || len(cc.Context) != 0 {
		t.Error("Context phải là mảng rỗng, không nil")
	}
	_ = b
	if _, ok := DecodeContext([]byte(`oops`)); ok {
		t.Error("hỏng phải đọc ra không có")
	}
}

func TestUserTopicStableAndRefreshed(t *testing.T) { // ca 37 (dạng bộ nhớ)
	ctx := context.Background()
	clk := newClock()
	st := NewMemoryStore(StoreOptions{Now: clk.now})
	a, _ := st.UserTopic(ctx, "u1")
	b, _ := st.UserTopic(ctx, "u1")
	o, _ := st.UserTopic(ctx, "u2")
	if a == "" || a != b || a == o {
		t.Fatalf("topic: %q %q %q", a, b, o)
	}
	clk.advance(59 * time.Minute)
	if c, _ := st.UserTopic(ctx, "u1"); c != a {
		t.Error("còn trong lease phải ổn định")
	}
	clk.advance(59 * time.Minute) // lần xin trước đã gia hạn
	if c, _ := st.UserTopic(ctx, "u1"); c != a {
		t.Error("mỗi lần xin phải gia hạn")
	}
	clk.advance(61 * time.Minute)
	if c, _ := st.UserTopic(ctx, "u1"); c == a {
		t.Error("quá hạn không xin thì phải sinh topic mới")
	}
}

func TestVersionValid(t *testing.T) {
	if !STU2.Valid() || !STU3.Valid() || Version("STU4").Valid() || Version("").Valid() {
		t.Error("Version.Valid")
	}
}
