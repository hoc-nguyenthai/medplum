package fhirsub_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirsub"
)

func unbindMsg(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "unbind-from-token", "payload": map[string]any{"token": token}})
	return b
}

// bindExpectingClose bind bằng một kết nối mới và trả lý do đóng (rỗng nếu bind thành công).
func (w *wsEnv) bindExpectingClose(id string, p *fhirauth.Principal) (websocket.StatusCode, string, *client) {
	w.t.Helper()
	c := w.dial()
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(w.tokenAs(id, p)))
	b, err := read(w.t, c, 2*time.Second)
	if err == nil {
		if statusOf(b)["type"] != "handshake" {
			w.t.Fatalf("không phải handshake: %v", b)
		}
		return 0, "", c
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		w.t.Fatalf("lỗi không phải đóng kết nối: %v", err)
	}
	return ce.Code, ce.Reason, c
}

func (w *wsEnv) waitBindings(n int) {
	w.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.hub.Stats().Bindings == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.t.Fatalf("Bindings = %d, muốn %d", w.hub.Stats().Bindings, n)
}

func wsObs(id string) string {
	return `{"resourceType":"Observation","id":"` + id + `","status":"final","code":{"text":"x"}}`
}

// Ca 13 (Medplum `Subscription removed after unbind-from-token`): gỡ bind thì thôi nhận; kết nối vẫn mở và bind lại được.
func TestWebSocketUnbindFromToken(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	w.subscribe("s2", "Observation")
	c := w.dial()
	t1, t2 := w.token("s1"), w.token("s2")
	for _, tok := range []string{t1, t2} {
		_ = c.Write(context.Background(), websocket.MessageText, bindMsg(tok))
		if hs, err := read(t, c, 2*time.Second); err != nil || statusOf(hs)["type"] != "handshake" {
			t.Fatalf("handshake: %v %v", hs, err)
		}
	}
	w.create(wsObs("o1"))
	for i := 0; i < 2; i++ {
		if _, err := read(t, c, 2*time.Second); err != nil {
			t.Fatalf("trước unbind phải nhận cả hai: %v", err)
		}
	}

	_ = c.Write(context.Background(), websocket.MessageText, unbindMsg(t1))
	w.waitBindings(1)
	w.create(wsObs("o2"))
	b, err := read(t, c, 2*time.Second)
	if err != nil || statusOf(b)["subscription"].(map[string]any)["reference"] != "Subscription/s2" {
		t.Fatalf("chỉ s2 còn nhận: %v %v", b, err)
	}
	wantNothing(t, c, "s1 đã gỡ")

	// kết nối còn sống: ping/pong, và bind lại s1 được
	_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`))
	if b, err := read(t, c, 2*time.Second); err != nil || b["type"] != "pong" {
		t.Fatalf("pong: %v %v", b, err)
	}
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(t1))
	if hs, err := read(t, c, 2*time.Second); err != nil || statusOf(hs)["type"] != "handshake" {
		t.Fatalf("bind lại: %v %v", hs, err)
	}
	// gỡ s1 lần nữa rồi gỡ lại khi đã gỡ: bỏ qua im lặng
	_ = c.Write(context.Background(), websocket.MessageText, unbindMsg(t1))
	_ = c.Write(context.Background(), websocket.MessageText, unbindMsg(t1))
	w.waitBindings(1)
	if _, err := read(t, c, 300*time.Millisecond); err == nil || errors.Is(err, context.DeadlineExceeded) == false {
		t.Errorf("unbind lặp không được đóng kết nối hay trả lời: %v", err)
	}
}

// Gỡ bind chỉ ảnh hưởng chính kết nối gửi nó; token hết hạn vẫn gỡ được (không cấp thêm quyền nào).
func TestWebSocketUnbindIsScopedToTheConnectionAndAcceptsExpiredToken(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	a := w.dial()
	tok := w.token("s1")
	_ = a.Write(context.Background(), websocket.MessageText, bindMsg(tok))
	read(t, a, 2*time.Second)
	b := w.bound("s1")
	w.waitBindings(2)

	w.now.Store(int64((10 * time.Minute).Seconds())) // token đã quá hạn bind (5 phút)
	_ = a.Write(context.Background(), websocket.MessageText, unbindMsg(tok))
	w.waitBindings(1)
	w.create(wsObs("o1"))
	if _, err := read(t, b, 2*time.Second); err != nil {
		t.Fatalf("kết nối khác vẫn phải nhận: %v", err)
	}
	wantNothing(t, a, "kết nối đã gỡ")
}

func TestWebSocketUnbindRejectsBadTokensAndEarlyUnbind(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")

	early := w.dial() // chưa bind mà đã gỡ: như mọi thông điệp khác trước bind
	_ = early.Write(context.Background(), websocket.MessageText, unbindMsg(w.token("s1")))
	if _, err := read(t, early, 2*time.Second); closeCode(err) != websocket.StatusPolicyViolation {
		t.Errorf("unbind trước bind: %v", err)
	}

	for name, tok := range map[string]string{"rác": "abc.def", "rỗng": "", "chữ ký sai": strings.Split(w.token("s1"), ".")[0] + ".AAAA"} {
		c := w.bound("s1")
		_ = c.Write(context.Background(), websocket.MessageText, unbindMsg(tok))
		if _, err := read(t, c, 2*time.Second); closeCode(err) != websocket.StatusPolicyViolation {
			t.Errorf("%s: mã đóng = %v", name, closeCode(err))
		}
	}
	// token của Subscription khác không gỡ được binding của s1
	w.subscribe("s2", "Observation")
	c := w.bound("s1")
	_ = c.Write(context.Background(), websocket.MessageText, unbindMsg(w.token("s2")))
	w.create(wsObs("o1"))
	if _, err := read(t, c, 2*time.Second); err != nil {
		t.Errorf("s1 phải còn nhận: %v", err)
	}
}

// Ca 31, 33 (hạn mức bind theo người): đếm trên mọi kết nối, giảm khi unbind hoặc đóng kết nối, người khác không bị ảnh hưởng.
func TestWebSocketBindQuotaPerUser(t *testing.T) {
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.MaxBindingsPerUser = 2 })
	for _, id := range []string{"s1", "s2", "s3"} {
		w.subscribe(id, "Observation")
	}
	alice := &fhirauth.Principal{Subject: "alice"}
	bob := &fhirauth.Principal{Subject: "bob"}

	code, _, c1 := w.bindExpectingClose("s1", alice)
	if code != 0 {
		t.Fatalf("bind 1: %v", code)
	}
	if code, _, _ = w.bindExpectingClose("s2", alice); code != 0 { // kết nối khác của cùng người vẫn tính chung
		t.Fatalf("bind 2: %v", code)
	}
	code, reason, _ := w.bindExpectingClose("s3", alice)
	if code != websocket.StatusPolicyViolation || !strings.Contains(reason, "exceeded allotted maximum number of concurrent WebSocket subscriptions: 2") {
		t.Fatalf("bind 3 phải bị từ chối: %v %q", code, reason)
	}
	if code, _, _ = w.bindExpectingClose("s3", bob); code != 0 {
		t.Fatalf("người khác không bị ảnh hưởng: %v", code)
	}
	// bind lại cùng Subscription trên cùng kết nối không tiêu thêm hạn mức
	_ = c1.Write(context.Background(), websocket.MessageText, bindMsg(w.tokenAs("s1", alice)))
	if hs, err := read(t, c1, 2*time.Second); err != nil || statusOf(hs)["type"] != "handshake" {
		t.Fatalf("bind lại: %v %v", hs, err)
	}
	if code, _, _ = w.bindExpectingClose("s3", alice); code == 0 {
		t.Fatal("bind lại không được làm hạn mức nới ra")
	}

	// unbind trả lại hạn mức
	_ = c1.Write(context.Background(), websocket.MessageText, unbindMsg(w.token("s1")))
	w.waitBindings(2) // s2 của alice, s3 của bob
	code, _, c3 := w.bindExpectingClose("s3", alice)
	if code != 0 {
		t.Fatalf("sau unbind phải bind được: %v", code)
	}
	// đóng kết nối trả lại hạn mức
	_ = c3.Close(websocket.StatusNormalClosure, "")
	w.waitBindings(2)
	if code, _, _ = w.bindExpectingClose("s1", alice); code != 0 {
		t.Fatalf("sau đóng kết nối phải bind được: %v", code)
	}
}

// Token không có ảnh chụp quyền (không có người) không có khóa đếm nên không bị hạn mức.
func TestWebSocketBindQuotaDoesNotApplyWithoutPrincipal(t *testing.T) {
	w := newWSEnv(t, func(c *fhirsub.WSConfig) { c.MaxBindingsPerUser = 1 })
	w.subscribe("s1", "Observation")
	for i := 0; i < 3; i++ {
		w.bound("s1")
	}
	w.waitBindings(3)
}

// Ca 20 (hiện trạng): auth dịch rule có hiddenFields thành `deny` cả loại; một Subscription websocket không được lộ loại đó
// dù chính sách có rule "*". (Lọc từng trường theo hiddenFields là việc A-05 của fhir-parity-audit, chung cho REST và ws.)
func TestWebSocketNeverDeliversDeniedTypeEvenWithWildcard(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	w.subscribe("s2", "Patient")
	pol := policyOf(t, []any{map[string]any{"resourceType": "*"}, map[string]any{"resourceType": "Observation", "deny": true}})
	p := &fhirauth.Principal{Subject: "a", Policy: pol}
	co := w.boundAs("s1", p)
	cp := w.boundAs("s2", p)

	w.create(wsObs("o1"))
	w.create(`{"resourceType":"Patient","id":"p1"}`)
	wantNothing(t, co, "Observation bị deny")
	if b, err := read(t, cp, 2*time.Second); err != nil || statusOf(b)["type"] != "event-notification" {
		t.Fatalf("Patient được phép phải nhận: %v %v", b, err)
	}
}

// Manager chỉ khớp tiêu chí khi có socket đang bind Subscription (B.1.2 mục 6): không có người nghe thì không tốn công.
func TestManagerMatchesWebSocketSubscriptionOnlyWhileBound(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	w.subscribe("s2", "Observation")
	sentinel := w.bound("s2") // luôn có người nghe: chứng minh sự kiện đã được xử lý xong
	settle := func(id string) {
		w.create(wsObs(id))
		if _, err := read(t, sentinel, 2*time.Second); err != nil {
			t.Fatalf("sentinel: %v", err)
		}
	}

	settle("o1") // s1 chưa ai bind: chỉ s2 khớp
	if m := w.mgr.Stats().Matched; m != 1 {
		t.Fatalf("Matched = %d, muốn 1 (chỉ s2)", m)
	}
	c := w.bound("s1")
	settle("o2")
	if m := w.mgr.Stats().Matched; m != 3 {
		t.Fatalf("Matched = %d, muốn 3 (o2 khớp cả s1 và s2)", m)
	}
	if _, err := read(t, c, 2*time.Second); err != nil {
		t.Fatalf("s1 đã bind phải nhận: %v", err)
	}
	_ = c.Write(context.Background(), websocket.MessageText, unbindMsg(w.token("s1")))
	w.waitBindings(1)
	settle("o3")
	if m := w.mgr.Stats().Matched; m != 4 {
		t.Errorf("Matched = %d, muốn 4 (sau unbind chỉ s2 khớp)", m)
	}
}
