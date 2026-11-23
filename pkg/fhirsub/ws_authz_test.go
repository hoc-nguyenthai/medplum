package fhirsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

// tokenAs lấy token binding với tư cách người gọi p (như qua router đã xác thực).
func (w *wsEnv) tokenAs(id string, p *fhirauth.Principal) string {
	w.t.Helper()
	ctx := fhirauth.WithPrincipal(context.Background(), p)
	res, err := w.hub.TokenOperation(w.mem)(ctx, &fhirrouter.Request{Params: map[string]string{"id": id}})
	if err != nil {
		w.t.Fatalf("token: %v", err)
	}
	for _, q := range res.Resource.(*fhir.Parameters).Parameter {
		if q.Name == "token" {
			return q.ValueString
		}
	}
	w.t.Fatal("không có token")
	return ""
}

func (w *wsEnv) boundAs(id string, p *fhirauth.Principal) *client {
	w.t.Helper()
	c := w.dial()
	if err := c.Write(context.Background(), websocket.MessageText, bindMsg(w.tokenAs(id, p))); err != nil {
		w.t.Fatal(err)
	}
	if hs, err := read(w.t, c, 2*time.Second); err != nil || statusOf(hs)["type"] != "handshake" {
		w.t.Fatalf("handshake: %v %v", hs, err)
	}
	return c
}

func policyOf(t *testing.T, v any) *fhirauth.Policy {
	t.Helper()
	p, err := fhirauth.ParsePolicy(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func wantNothing(t *testing.T, c *client, why string) {
	t.Helper()
	if b, err := read(t, c, 300*time.Millisecond); err == nil {
		t.Fatalf("%s: nhận được %v", why, b)
	}
}

func TestWebSocketDeliveryChecksReadPermissionPerRecipient(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	noRead := w.boundAs("s1", &fhirauth.Principal{Subject: "a", Grants: fhirauth.Grants{"Patient": fhirauth.PermRead, "Observation": fhirauth.PermCreate | fhirauth.PermSearch}})
	reader := w.boundAs("s1", &fhirauth.Principal{Subject: "b", Grants: fhirauth.Grants{"Observation": fhirauth.PermRead}})
	open := w.boundAs("s1", &fhirauth.Principal{Subject: "c"}) // không giới hạn theo loại

	w.create(`{"resourceType":"Observation","id":"o1","status":"final","code":{"text":"x"}}`)
	for name, c := range map[string]*client{"reader": reader, "open": open} {
		b, err := read(t, c, 2*time.Second)
		if err != nil || statusOf(b)["type"] != "event-notification" {
			t.Fatalf("%s phải nhận: %v %v", name, b, err)
		}
	}
	wantNothing(t, noRead, "người không có chữ r")
	// xóa cũng không được lộ tham chiếu tới người không có quyền đọc
	if err := w.repo.DeleteResource(context.Background(), "Observation", "o1"); err != nil {
		t.Fatal(err)
	}
	wantNothing(t, noRead, "xóa: người không có chữ r")
	if _, err := read(t, reader, 2*time.Second); err != nil {
		t.Fatalf("reader phải nhận thông báo xóa: %v", err)
	}
	if s := w.hub.Stats(); s.Denied != 2 {
		t.Errorf("Denied = %d, muốn 2", s.Denied)
	}
}

func TestWebSocketDeliveryHonorsAccessPolicy(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	byCriteria := w.boundAs("s1", &fhirauth.Principal{Subject: "a", Policy: policyOf(t, []any{map[string]any{"resourceType": "Observation", "criteria": "status=final"}})})
	otherType := w.boundAs("s1", &fhirauth.Principal{Subject: "b", Policy: policyOf(t, []any{map[string]any{"resourceType": "Patient"}})})

	w.create(`{"resourceType":"Observation","id":"o1","status":"preliminary","code":{"text":"x"}}`)
	wantNothing(t, byCriteria, "không thỏa tiêu chí")
	w.create(`{"resourceType":"Observation","id":"o2","status":"final","code":{"text":"x"}}`)
	b, err := read(t, byCriteria, 2*time.Second)
	if err != nil || b["entry"].([]any)[1].(map[string]any)["resource"].(map[string]any)["id"] != "o2" {
		t.Fatalf("phải nhận o2 (và chỉ o2): %v %v", b, err)
	}
	wantNothing(t, otherType, "policy không liệt kê loại Observation")
}

func TestWebSocketBindingStopsWhenAccessTokenExpires(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	p := &fhirauth.Principal{Subject: "a", Expires: time.Now().Add(2 * time.Minute)}
	c := w.boundAs("s1", p)

	w.create(`{"resourceType":"Observation","id":"o1","status":"final","code":{"text":"x"}}`)
	if _, err := read(t, c, 2*time.Second); err != nil {
		t.Fatalf("trước hạn phải nhận: %v", err)
	}
	w.now.Store(int64((10 * time.Minute).Seconds())) // token binding (5 phút) lẫn access token đều đã hết hạn
	w.create(`{"resourceType":"Observation","id":"o2","status":"final","code":{"text":"x"}}`)
	wantNothing(t, c, "sau khi access token hết hạn")
	if s := w.hub.Stats(); s.Bindings != 0 || s.Denied != 1 {
		t.Errorf("stats: %+v (muốn binding đã gỡ, Denied=1)", s)
	}
}

func TestWebSocketRejectsBindWithExpiredAccessToken(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	c := w.dial()
	tok := w.tokenAs("s1", &fhirauth.Principal{Subject: "a", Expires: time.Now().Add(-time.Second)})
	_ = c.Write(context.Background(), websocket.MessageText, bindMsg(tok))
	b, err := read(t, c, 2*time.Second)
	if err == nil {
		t.Fatalf("không được bind: %v", b)
	}
	if closeCode(err) != websocket.StatusPolicyViolation {
		t.Errorf("mã đóng = %v", closeCode(err))
	}
}

func TestWebSocketRebindReplacesPrincipal(t *testing.T) {
	w := newWSEnv(t)
	w.subscribe("s1", "Observation")
	c := w.boundAs("s1", &fhirauth.Principal{Subject: "a", Grants: fhirauth.Grants{"Patient": fhirauth.PermRead}})
	w.create(`{"resourceType":"Observation","id":"o1","status":"final","code":{"text":"x"}}`)
	wantNothing(t, c, "chưa có quyền")
	// bind lại với token của người có quyền: quyền mới có hiệu lực ngay trên cùng kết nối
	if err := c.Write(context.Background(), websocket.MessageText, bindMsg(w.tokenAs("s1", &fhirauth.Principal{Subject: "b", Grants: fhirauth.Grants{"Observation": fhirauth.PermRead}}))); err != nil {
		t.Fatal(err)
	}
	if hs, err := read(t, c, 2*time.Second); err != nil || statusOf(hs)["type"] != "handshake" {
		t.Fatalf("handshake lại: %v %v", hs, err)
	}
	w.create(`{"resourceType":"Observation","id":"o2","status":"final","code":{"text":"x"}}`)
	if _, err := read(t, c, 2*time.Second); err != nil {
		t.Fatalf("phải nhận sau khi bind lại: %v", err)
	}
}
