package fhircast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fx struct {
	t     *testing.T
	hub   *Hub
	store Store
	bus   Bus
}

func hubFixtures(t *testing.T, name string, fn func(t *testing.T, f *fx)) {
	t.Run(name+"/memory", func(t *testing.T) {
		st, bus := NewMemoryStore(StoreOptions{}), NewMemoryBus()
		fn(t, &fx{t: t, hub: &Hub{Store: st, Bus: bus}, store: st, bus: bus})
	})
	t.Run(name+"/redis", func(t *testing.T) {
		r := newRedis(t)
		st, bus := NewRedisStore(r.c, "", StoreOptions{}), NewRedisBus(r.c)
		t.Cleanup(func() { _ = bus.Close() })
		fn(t, &fx{t: t, hub: &Hub{Store: st, Bus: bus}, store: st, bus: bus})
	})
}

// listen đăng ký nghe kênh của (project, topic) và trả hàng nhận thông điệp đã giải mã.
func (f *fx) listen(project, topic string) chan ChannelMessage {
	f.t.Helper()
	ch := make(chan ChannelMessage, 64)
	cancel, err := f.bus.Subscribe(context.Background(), Channel(project, topic), func(p []byte) {
		var m ChannelMessage
		if json.Unmarshal(p, &m) == nil {
			ch <- m
		}
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(cancel)
	return ch
}

func got(t *testing.T, ch chan ChannelMessage) ChannelMessage {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("không nhận được thông điệp")
		return ChannelMessage{}
	}
}

func silent(t *testing.T, ch chan ChannelMessage) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatalf("không được nhận: %+v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

func wantErr(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var he *Error
	if !errors.As(err, &he) {
		t.Fatalf("muốn lỗi %d %q, được %v", status, msg, err)
	}
	if he.Status != status || he.Message != msg {
		t.Fatalf("lỗi %d %q, muốn %d %q", he.Status, he.Message, status, msg)
	}
}

func report(id string) map[string]any {
	return map[string]any{"resourceType": "DiagnosticReport", "id": id, "status": "preliminary"}
}
func patient(id string) map[string]any { return map[string]any{"resourceType": "Patient", "id": id} }
func obs(id string) map[string]any     { return map[string]any{"resourceType": "Observation", "id": id} }

func evt(topic, name string, ctxItems ...map[string]any) map[string]any {
	items := []any{}
	for _, c := range ctxItems {
		items = append(items, c)
	}
	return map[string]any{"id": "m-" + name, "timestamp": "2026-01-01T00:00:00Z",
		"event": map[string]any{"hub.topic": topic, "hub.event": name, "context": items}}
}
func item(key string, res map[string]any) map[string]any {
	return map[string]any{"key": key, "resource": res}
}
func updateEvt(topic, version string, entries ...map[string]any) map[string]any {
	es := []any{}
	for _, e := range entries {
		es = append(es, e)
	}
	e := evt(topic, "DiagnosticReport-update", item("updates", map[string]any{"resourceType": "Bundle", "type": "transaction", "entry": es}))
	if version != "" {
		e["event"].(map[string]any)["context.versionId"] = version
	}
	return e
}
func put(res map[string]any) map[string]any {
	return map[string]any{"request": map[string]any{"method": "PUT"}, "resource": res}
}
func del(full string) map[string]any {
	return map[string]any{"request": map[string]any{"method": "DELETE"}, "fullUrl": full}
}
func evOf(m map[string]any) map[string]any { return m["event"].(map[string]any) }

// ---- đăng ký / hủy (ca 2-23)

func TestHubSubscribe(t *testing.T) {
	hubFixtures(t, "subscribe", func(t *testing.T, f *fx) {
		ctx := context.Background()
		ep := func(r SubscribeRequest) (string, error) { return f.hub.Subscribe(ctx, "p", "u", STU3, "ws://h:1/", r) }
		sub := SubscribeRequest{ChannelType: "websocket", Mode: "subscribe", Topic: "t1", Events: "Patient-open, DiagnosticReport-open"}

		u1, err := ep(sub)
		if err != nil || !strings.HasPrefix(u1, "ws://h:1/ws/fhircast/") {
			t.Fatalf("%q %v", u1, err)
		}
		u2, _ := ep(sub)
		if u1 == u2 {
			t.Error("mỗi lần đăng ký phải có endpoint riêng") // ca 10
		}
		if strings.Contains(u1, "t1") {
			t.Error("endpoint không được chứa topic")
		}
		s, ok, _ := f.store.GetSubscription(ctx, ExtractEndpoint(u1))
		if !ok || s.ProjectID != "p" || s.Topic != "t1" || s.UserSubject != "u" || s.Version != STU3 ||
			len(s.Events) != 2 || s.Events[1] != "DiagnosticReport-open" || s.ExpiresAt.IsZero() {
			t.Errorf("đăng ký đã lưu: %+v", s) // ca 10, 11
		}

		bad := map[string]struct {
			mut func(*SubscribeRequest)
			msg string
		}{
			"thiếu channel type":        {func(r *SubscribeRequest) { r.ChannelType = "" }, "Missing hub.channel.type"},
			"thiếu mode":                {func(r *SubscribeRequest) { r.Mode = "" }, "Missing hub.mode"},
			"thiếu topic":               {func(r *SubscribeRequest) { r.Topic = "" }, "Missing hub.topic"},
			"webhook":                   {func(r *SubscribeRequest) { r.ChannelType = "webhook" }, "Invalid hub.channel.type"},
			"rest-hook":                 {func(r *SubscribeRequest) { r.ChannelType = "rest-hook" }, "Invalid hub.channel.type"},
			"email":                     {func(r *SubscribeRequest) { r.ChannelType = "email" }, "Invalid hub.channel.type"},
			"hoa thường lạ":             {func(r *SubscribeRequest) { r.ChannelType = "WebSocket" }, "Invalid hub.channel.type"},
			"mode lạ":                   {func(r *SubscribeRequest) { r.Mode = "renew" }, "Invalid hub.mode"},
			"thiếu events":              {func(r *SubscribeRequest) { r.Events = "" }, "Missing hub.events"},
			"events rỗng 1":             {func(r *SubscribeRequest) { r.Events = " " }, "Invalid hub.events"},
			"events rỗng 2":             {func(r *SubscribeRequest) { r.Events = "," }, "Invalid hub.events"},
			"events rỗng 3":             {func(r *SubscribeRequest) { r.Events = " , " }, "Invalid hub.events"},
			"sự kiện lạ (F-8)":          {func(r *SubscribeRequest) { r.Events = "Patient-open,Observation-open" }, "Invalid hub.events"},
			"topic có ký tự điều khiển": {func(r *SubscribeRequest) { r.Topic = "a\nb" }, "Invalid hub.topic"},
			"topic quá dài":             {func(r *SubscribeRequest) { r.Topic = strings.Repeat("a", MaxTopicBytes+1) }, "Invalid hub.topic"},
		}
		for name, c := range bad {
			r := sub
			c.mut(&r)
			_, err := ep(r)
			if err == nil {
				t.Errorf("%s: phải lỗi", name)
				continue
			}
			wantErr(t, err, 400, c.msg)
		}
	})
}

func TestHubUnsubscribe(t *testing.T) {
	hubFixtures(t, "unsub", func(t *testing.T, f *fx) {
		ctx := context.Background()
		sub := func(project, user, topic string) string {
			u, err := f.hub.Subscribe(ctx, project, user, STU3, "ws://h", SubscribeRequest{ChannelType: "websocket", Mode: "subscribe", Topic: topic, Events: "Patient-open"})
			if err != nil {
				t.Fatal(err)
			}
			return u
		}
		un := func(project, user, topic, url, legacy string) (string, error) {
			return f.hub.Subscribe(ctx, project, user, STU3, "ws://h", SubscribeRequest{ChannelType: "websocket", Mode: "unsubscribe", Topic: topic, ChannelEndpoint: url, LegacyEndpoint: legacy})
		}
		listen := f.listen("p", "t")

		a, b := sub("p", "u", "t"), sub("p", "u", "t")
		got1, err := un("p", "u", "t", a+"?x=1#frag", "")
		if err != nil || got1 != ExtractEndpoint(a) {
			t.Fatalf("hủy: %q %v", got1, err) // ca 13: gương endpoint
		}
		m := got(t, listen)
		if m.Target != ExtractEndpoint(a) || m.Payload["hub.mode"] != "denied" || m.Payload["hub.reason"] != "Subscriber unsubscribed from topic" ||
			m.Payload["hub.topic"] != "t" || m.Payload["hub.events"] != "Patient-open" {
			t.Errorf("denied: %+v", m)
		}
		if _, ok, _ := f.store.GetSubscription(ctx, ExtractEndpoint(a)); ok {
			t.Error("đăng ký phải biến mất")
		}
		if _, ok, _ := f.store.GetSubscription(ctx, ExtractEndpoint(b)); !ok { // ca 22
			t.Error("đăng ký khác của topic phải còn")
		}
		silent(t, listen)

		// trường endpoint cũ (ca 17), và trường mới thắng khi có cả hai (ca 18)
		c := sub("p", "u", "t")
		if got, err := un("p", "u", "t", "", c); err != nil || got != ExtractEndpoint(c) {
			t.Errorf("trường cũ: %q %v", got, err)
		}
		d, e := sub("p", "u", "t"), sub("p", "u", "t")
		if got, _ := un("p", "u", "t", d, e); got != ExtractEndpoint(d) {
			t.Errorf("cả hai trường phải hủy cái đã cấp: %q", got)
		}
		if _, ok, _ := f.store.GetSubscription(ctx, ExtractEndpoint(e)); !ok {
			t.Error("endpoint ở trường cũ không được bị hủy")
		}

		_, err = un("p", "u", "t", "", "")
		wantErr(t, err, 400, "Missing endpoint") // ca 20
		_, err = un("p", "u", "t", "ws://h/ws/fhircast/unknown", "")
		wantErr(t, err, 400, "Invalid endpoint") // ca 21
		_, err = un("other", "u", "t", b, "")
		wantErr(t, err, 400, "Invalid endpoint") // ca 23: khác project
		_, err = un("p", "u", "other-topic", b, "")
		wantErr(t, err, 400, "Invalid endpoint") // khác topic
		_, err = un("p", "intruder", "t", b, "")
		wantErr(t, err, 400, "Invalid endpoint") // người khác
		if _, ok, _ := f.store.GetSubscription(ctx, ExtractEndpoint(b)); !ok {
			t.Error("hủy sai không được xóa đăng ký")
		}
		_, err = un("p", "u", "t", a, "") // đã hủy rồi
		wantErr(t, err, 400, "Invalid endpoint")
	})
}

func TestHubSubscribeStoreFailure(t *testing.T) { // ca 12
	h := &Hub{Store: failStore{NewMemoryStore(StoreOptions{})}, Bus: NewMemoryBus()}
	_, err := h.Subscribe(context.Background(), "p", "u", STU3, "ws://h", SubscribeRequest{ChannelType: "websocket", Mode: "subscribe", Topic: "t", Events: "Patient-open"})
	wantErr(t, err, 500, "Failed to create subscription for topic")
}

type failStore struct{ Store }

func (failStore) PutSubscription(context.Context, string, Subscription, time.Duration) error {
	return errors.New("redis down")
}

// ---- hình dạng thông điệp (ca 24, 27) và sự kiện lạ (F-8)

func TestHubChangeContextValidation(t *testing.T) {
	hubFixtures(t, "validate", func(t *testing.T, f *fx) {
		ctx := context.Background()
		base := func() map[string]any { return evt("t", "Patient-open", item("patient", patient("p1"))) }
		cases := map[string]struct {
			mut func(map[string]any)
			msg string
		}{
			"thiếu id":            {func(m map[string]any) { delete(m, "id") }, "Missing event ID"},
			"thiếu timestamp":     {func(m map[string]any) { delete(m, "timestamp") }, "Missing event timestamp"},
			"thiếu event":         {func(m map[string]any) { delete(m, "event") }, "Missing event payload"},
			"thiếu topic":         {func(m map[string]any) { delete(evOf(m), "hub.topic") }, `Missing event["hub.topic"]`},
			"thiếu hub.event":     {func(m map[string]any) { delete(evOf(m), "hub.event") }, `Missing event["hub.event"]`},
			"thiếu context":       {func(m map[string]any) { delete(evOf(m), "context") }, "Missing event.context"},
			"context sai kiểu":    {func(m map[string]any) { evOf(m)["context"] = "x" }, "Invalid event.context"},
			"phần tử context sai": {func(m map[string]any) { evOf(m)["context"] = []any{1} }, "Invalid event.context"},
			"sự kiện lạ (F-8)":    {func(m map[string]any) { evOf(m)["hub.event"] = "Observation-open" }, `Invalid event["hub.event"]`},
			"topic xấu":           {func(m map[string]any) { evOf(m)["hub.topic"] = "a\x00" }, `Invalid event["hub.topic"]`},
		}
		listen := f.listen("p", "t")
		for name, c := range cases {
			m := base()
			c.mut(m)
			_, err := f.hub.ChangeContext(ctx, "p", m)
			if err == nil {
				t.Errorf("%s: phải lỗi", name)
				continue
			}
			wantErr(t, err, 400, c.msg)
		}
		silent(t, listen) // lỗi thì không phát gì
		if _, ok, _ := f.store.GetContext(ctx, "p", "t"); ok {
			t.Error("lỗi thì không được đổi ngữ cảnh")
		}
	})
}

// ---- mở / đóng / lấy ngữ cảnh (ca 28-32)

func TestHubOpenCloseGet(t *testing.T) {
	hubFixtures(t, "openclose", func(t *testing.T, f *fx) {
		ctx := context.Background()
		listen := f.listen("p", "t")
		cc, ok, _ := f.hub.CurrentContext(ctx, "p", "t")
		if ok || fmt3(ContextResponse(STU3, cc, ok)) != `{"context":[],"context.type":""}` || fmt3(ContextResponse(STU2, cc, ok)) != `[]` {
			t.Errorf("ca 28: %s", fmt3(ContextResponse(STU3, cc, ok)))
		}

		out, err := f.hub.ChangeContext(ctx, "p", evt("t", "Patient-open", item("patient", patient("p1"))))
		if err != nil {
			t.Fatal(err)
		}
		v := evOf(out)["context.versionId"].(string)
		if v == "" {
			t.Fatal("phải có versionId")
		}
		m := got(t, listen)
		if evOf(m.Payload)["context.versionId"] != v || m.Target != "" {
			t.Errorf("thông điệp phát: %+v", m)
		}
		cc, ok, _ = f.hub.CurrentContext(ctx, "p", "t")
		if !ok || cc.Type != "Patient" || cc.VersionID != v || len(cc.Context) != 1 {
			t.Errorf("ca 29: %+v", cc)
		}
		if r := ContextResponse(STU3, cc, ok).(map[string]any); r["context.type"] != "Patient" || r["context.versionId"] != v {
			t.Errorf("STU3: %v", r)
		}
		if arr := ContextResponse(STU2, cc, ok).([]map[string]any); len(arr) != 1 {
			t.Errorf("STU2 phải là mảng context: %v", arr)
		}

		// cô lập project (ca 30): project khác cùng chuỗi topic thấy trống và ghi độc lập
		if _, ok, _ := f.hub.CurrentContext(ctx, "other", "t"); ok {
			t.Error("project khác thấy ngữ cảnh")
		}
		_, _ = f.hub.ChangeContext(ctx, "other", evt("t", "Patient-open", item("patient", patient("zz"))))
		cc2, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		if cc2.VersionID != v {
			t.Error("ghi của project khác làm đổi ngữ cảnh")
		}
		silent(t, listen) // kênh của project khác không tới

		// close luôn xóa, kể cả không đúng tài nguyên (ca 31)
		if _, err := f.hub.ChangeContext(ctx, "p", evt("t", "Patient-close", item("patient", patient("khác")))); err != nil {
			t.Fatal(err)
		}
		got(t, listen)
		if _, ok, _ := f.hub.CurrentContext(ctx, "p", "t"); ok {
			t.Error("close phải xóa ngữ cảnh")
		}
		// select và sự kiện khác chỉ phát, không đổi ngữ cảnh
		_, _ = f.hub.ChangeContext(ctx, "p", evt("t", "Patient-open", item("patient", patient("p2"))))
		got(t, listen)
		before, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		for _, n := range []string{"DiagnosticReport-select", "syncerror", "userlogout", "userhibernate", "heartbeat"} {
			if _, err := f.hub.ChangeContext(ctx, "p", evt("t", n)); err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			got(t, listen)
		}
		after, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		if after.VersionID != before.VersionID {
			t.Error("sự kiện khác không được đổi ngữ cảnh")
		}
	})
}

func fmt3(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestHubDiagnosticReportOpenHasEmptyContentBundle(t *testing.T) { // ca 29, 32
	hubFixtures(t, "drOpen", func(t *testing.T, f *fx) {
		ctx := context.Background()
		out, err := f.hub.ChangeContext(ctx, "p", evt("t", "DiagnosticReport-open", item("report", report("r1")), item("patient", patient("p1"))))
		if err != nil {
			t.Fatal(err)
		}
		cc, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		if cc.Type != "DiagnosticReport" || cc.VersionID != evOf(out)["context.versionId"] {
			t.Errorf("phiên bản phản hồi và kho phải khớp: %+v", cc)
		}
		content := findResource(cc.Context, "content")
		if content == nil || content["resourceType"] != "Bundle" || content["type"] != "collection" || content["id"] == "" || content["entry"] != nil {
			t.Errorf("content: %v", content)
		}
		if len(cc.Context) != 3 {
			t.Errorf("giữ context gốc và thêm content: %d", len(cc.Context))
		}
		// phản hồi/thông điệp KHÔNG chứa content do Hub thêm (chỉ context.versionId)
		if n := len(evOf(out)["context"].([]any)); n != 2 {
			t.Errorf("thông điệp phát phải giữ nguyên context của client: %d", n)
		}
	})
}

// ---- update (ca 33-35, F-9)

func openReport(t *testing.T, f *fx, topic, id string) string {
	t.Helper()
	out, err := f.hub.ChangeContext(context.Background(), "p", evt(topic, "DiagnosticReport-open", item("report", report(id))))
	if err != nil {
		t.Fatal(err)
	}
	return evOf(out)["context.versionId"].(string)
}

func TestHubUpdate(t *testing.T) {
	hubFixtures(t, "update", func(t *testing.T, f *fx) {
		ctx := context.Background()
		listen := f.listen("p", "t")
		v0 := openReport(t, f, "t", "r1")
		got(t, listen)

		out, err := f.hub.ChangeContext(ctx, "p", updateEvt("t", v0, put(obs("o1"))))
		if err != nil {
			t.Fatal(err)
		}
		ev := evOf(out)
		v1 := ev["context.versionId"].(string)
		if ev["context.priorVersionId"] != v0 || v1 == v0 { // ca 33
			t.Errorf("chuỗi phiên bản: %v -> %v", v0, v1)
		}
		if evOf(got(t, listen).Payload)["context.priorVersionId"] != v0 {
			t.Error("thông điệp phát phải mang priorVersionId")
		}
		cc, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		entries := findResource(cc.Context, "content")["entry"].([]any)
		if cc.VersionID != v1 || len(entries) != 1 {
			t.Fatalf("sau update: %+v", cc)
		}

		// PUT thay cùng id, thêm id mới; DELETE theo fullUrl
		o1b := obs("o1")
		o1b["status"] = "final"
		out, err = f.hub.ChangeContext(ctx, "p", updateEvt("t", v1, put(o1b), put(obs("o2"))))
		if err != nil {
			t.Fatal(err)
		}
		got(t, listen)
		v2 := evOf(out)["context.versionId"].(string)
		cc, _, _ = f.hub.CurrentContext(ctx, "p", "t")
		entries = findResource(cc.Context, "content")["entry"].([]any)
		if len(entries) != 2 || entries[0].(map[string]any)["resource"].(map[string]any)["status"] != "final" {
			t.Errorf("PUT phải thay chỗ: %v", entries)
		}
		out, err = f.hub.ChangeContext(ctx, "p", updateEvt("t", v2, del("Observation/o1")))
		if err != nil {
			t.Fatal(err)
		}
		got(t, listen)
		v3 := evOf(out)["context.versionId"].(string)
		cc, _, _ = f.hub.CurrentContext(ctx, "p", "t")
		if entries = findResource(cc.Context, "content")["entry"].([]any); len(entries) != 1 {
			t.Errorf("DELETE: %v", entries)
		}

		errs := []struct {
			name string
			ev   map[string]any
			msg  string
		}{
			{"phiên bản cũ", updateEvt("t", v0, put(obs("x"))), fmt.Sprintf("Expected event.context.versionId to be '%s', received '%s'", v3, v0)},
			{"thiếu phiên bản", updateEvt("t", "", put(obs("x"))), fmt.Sprintf("Expected event.context.versionId to be '%s', received empty event.context.versionId", v3)},
			{"thiếu updates", evtWithVersion(evt("t", "DiagnosticReport-update"), v3), "Update event requires an update bundle in the context"},
			{"thiếu method", updateEvt("t", v3, map[string]any{"resource": obs("x")}), "Update bundle contains entry with a missing request.method"},
			{"method lạ", updateEvt("t", v3, map[string]any{"request": map[string]any{"method": "POST"}, "resource": obs("x")}), "Update bundle contains bad entry with request.method = 'POST', only 'PUT' and 'DELETE' are supported by an update event"},
			{"PUT thiếu resource", updateEvt("t", v3, map[string]any{"request": map[string]any{"method": "PUT"}}), "Missing resource in a update bundle PUT entry"},
			{"DELETE thiếu fullUrl", updateEvt("t", v3, map[string]any{"request": map[string]any{"method": "DELETE"}}), "DELETE entry in update bundle missing fullUrl"},
			{"fullUrl không phân giải", updateEvt("t", v3, del("abc")), "fullUrl in DELETE entry is not a resolvable reference string"},
			{"fullUrl rỗng id", updateEvt("t", v3, del("Observation/")), "fullUrl in DELETE entry is not a resolvable reference string"},
			{"xóa thứ thuộc ngữ cảnh gốc", updateEvt("t", v3, del("DiagnosticReport/r1")), "Cannot delete a resource that is part of the original open context"},
			{"xóa thứ không có", updateEvt("t", v3, del("Observation/nope")), "Cannot delete resource not currently in the content bundle"},
		}
		for _, c := range errs {
			_, err := f.hub.ChangeContext(ctx, "p", c.ev)
			if err == nil {
				t.Errorf("%s: phải lỗi", c.name)
				continue
			}
			wantErr(t, err, 400, c.msg)
		}
		// lỗi giữa chừng không lưu gì: entry PUT đứng trước entry hỏng không được lọt vào
		_, err = f.hub.ChangeContext(ctx, "p", updateEvt("t", v3, put(obs("lọt")), del("Observation/nope")))
		if err == nil {
			t.Fatal("phải lỗi")
		}
		cc, _, _ = f.hub.CurrentContext(ctx, "p", "t")
		if cc.VersionID != v3 || len(findResource(cc.Context, "content")["entry"].([]any)) != 1 {
			t.Errorf("update lỗi đã để lại dấu: %+v", cc)
		}
		silent(t, listen)
	})
}

func evtWithVersion(m map[string]any, v string) map[string]any {
	evOf(m)["context.versionId"] = v
	return m
}

func TestHubUpdateNeedsOpenDiagnosticReport(t *testing.T) { // ca 35
	hubFixtures(t, "updateNoDR", func(t *testing.T, f *fx) {
		ctx := context.Background()
		_, err := f.hub.ChangeContext(ctx, "p", updateEvt("t", "v", put(obs("x"))))
		wantErr(t, err, 400, "No DiagnosticReport currently open for this topic")
		_, _ = f.hub.ChangeContext(ctx, "p", evt("t", "Patient-open", item("patient", patient("p1"))))
		_, err = f.hub.ChangeContext(ctx, "p", updateEvt("t", "v", put(obs("x"))))
		wantErr(t, err, 400, "No DiagnosticReport currently open for this topic")
	})
}

func TestHubUpdateEntryLimit(t *testing.T) { // F-7
	hubFixtures(t, "limit", func(t *testing.T, f *fx) {
		f.hub.MaxUpdateEntries = 3
		v := openReport(t, f, "t", "r1")
		var es []map[string]any
		for i := 0; i < 4; i++ {
			es = append(es, put(obs(fmt.Sprint("o", i))))
		}
		_, err := f.hub.ChangeContext(context.Background(), "p", updateEvt("t", v, es...))
		wantErr(t, err, 400, "Update bundle has too many entries (maximum 3)")
		_, err = f.hub.ChangeContext(context.Background(), "p", updateEvt("t", v, es[:3]...))
		if err != nil {
			t.Errorf("đúng trần phải qua: %v", err)
		}
	})
}

func TestHubConcurrentUpdatesExactlyOneWins(t *testing.T) { // ca 34, F-9
	hubFixtures(t, "race", func(t *testing.T, f *fx) {
		ctx := context.Background()
		v0 := openReport(t, f, "t", "r1")
		const n = 20
		var ok, mismatch, conflict atomic.Int32
		var wg sync.WaitGroup
		var winner atomic.Value
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				out, err := f.hub.ChangeContext(ctx, "p", updateEvt("t", v0, put(obs(fmt.Sprint("o", i)))))
				var he *Error
				switch {
				case err == nil:
					ok.Add(1)
					winner.Store(evOf(out)["context.versionId"])
				case errors.As(err, &he) && he.Status == 409:
					conflict.Add(1)
					if !strings.Contains(he.Message, "no longer at version '"+v0+"'") {
						t.Errorf("409: %s", he.Message)
					}
				case errors.As(err, &he) && he.Status == 400 && strings.HasPrefix(he.Message, "Expected event.context.versionId"):
					mismatch.Add(1)
				default:
					t.Errorf("lỗi lạ: %v", err)
				}
			}(i)
		}
		wg.Wait()
		if ok.Load() != 1 || ok.Load()+mismatch.Load()+conflict.Load() != n {
			t.Fatalf("thắng=%d lệch=%d 409=%d", ok.Load(), mismatch.Load(), conflict.Load())
		}
		cc, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		if cc.VersionID != winner.Load() {
			t.Error("ngữ cảnh phải ở bản của người thắng")
		}
		if e := findResource(cc.Context, "content")["entry"].([]any); len(e) != 1 {
			t.Errorf("chỉ thay đổi của người thắng được giữ: %d entry", len(e))
		}
	})
}

// ---- chuyển sang bệnh nhân khác rồi quay lại: ca 51 (suspend-resume)

func TestHubSuspendResume(t *testing.T) {
	hubFixtures(t, "resume", func(t *testing.T, f *fx) {
		ctx := context.Background()
		listen := f.listen("p", "t")
		v1 := openReport(t, f, "t", "r1")
		out, err := f.hub.ChangeContext(ctx, "p", updateEvt("t", v1, put(obs("o1"))))
		if err != nil {
			t.Fatal(err)
		}
		v1b := evOf(out)["context.versionId"].(string)
		got(t, listen)
		got(t, listen)

		// mở bệnh nhân khác: báo cáo 1 được cất lại
		if _, err := f.hub.ChangeContext(ctx, "p", evt("t", "Patient-open", item("patient", patient("p2")))); err != nil {
			t.Fatal(err)
		}
		if cc, _, _ := f.hub.CurrentContext(ctx, "p", "t"); cc.Type != "Patient" {
			t.Errorf("ngữ cảnh hiện tại: %+v", cc)
		}
		// mở báo cáo khác rồi mở lại báo cáo 1: khôi phục đúng phiên bản và nội dung
		openReport(t, f, "t", "r2")
		out, err = f.hub.ChangeContext(ctx, "p", evt("t", "DiagnosticReport-open", item("report", report("r1"))))
		if err != nil {
			t.Fatal(err)
		}
		if evOf(out)["context.versionId"] != v1b {
			t.Errorf("khôi phục phải giữ versionId: %v != %v", evOf(out)["context.versionId"], v1b)
		}
		cc, _, _ := f.hub.CurrentContext(ctx, "p", "t")
		if cc.VersionID != v1b || len(findResource(cc.Context, "content")["entry"].([]any)) != 1 {
			t.Errorf("nội dung đã cập nhật phải còn: %+v", cc)
		}
		// đóng báo cáo 1 xóa bản đã lưu: mở lại là bản mới
		if _, err := f.hub.ChangeContext(ctx, "p", evt("t", "DiagnosticReport-close", item("report", report("r1")))); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := f.store.GetReportContext(ctx, "p", "t", "r1"); ok {
			t.Error("close báo cáo phải xóa bản đã lưu")
		}
		out, _ = f.hub.ChangeContext(ctx, "p", evt("t", "DiagnosticReport-open", item("report", report("r1"))))
		if evOf(out)["context.versionId"] == v1b {
			t.Error("sau close phải là phiên bản mới")
		}
		cc, _, _ = f.hub.CurrentContext(ctx, "p", "t")
		if e := findResource(cc.Context, "content")["entry"]; e != nil {
			t.Errorf("content phải trống: %v", e)
		}
	})
}

func TestHubOpenFailsWhenStoredDiagnosticReportIsBroken(t *testing.T) {
	hubFixtures(t, "brokenDR", func(t *testing.T, f *fx) {
		ctx := context.Background()
		// ngữ cảnh DiagnosticReport không có tài nguyên report hợp lệ
		_ = f.store.SetContext(ctx, "p", "t", CurrentContext{Type: "DiagnosticReport", VersionID: "v", Context: []map[string]any{{"key": "patient", "resource": patient("p1")}}})
		_, err := f.hub.ChangeContext(ctx, "p", evt("t", "Patient-open", item("patient", patient("p2"))))
		wantErr(t, err, 400, "No DiagnosticReport currently open for this topic")
		if cc, _, _ := f.hub.CurrentContext(ctx, "p", "t"); cc.VersionID != "v" {
			t.Error("lỗi thì không được đổi ngữ cảnh")
		}
	})
}

func TestHubInfrastructureErrorsAre500AndDoNotLeak(t *testing.T) {
	h := &Hub{Store: brokenStore{NewMemoryStore(StoreOptions{})}, Bus: NewMemoryBus()}
	_, err := h.ChangeContext(context.Background(), "p", evt("t", "Patient-open", item("patient", patient("p1"))))
	wantErr(t, err, 500, "Internal server error")
	_, _, err = h.CurrentContext(context.Background(), "p", "t")
	wantErr(t, err, 500, "Internal server error")
	_, err = h.Subscribe(context.Background(), "p", "u", STU3, "ws://h", SubscribeRequest{ChannelType: "websocket", Mode: "unsubscribe", Topic: "t", ChannelEndpoint: "x"})
	wantErr(t, err, 500, "Internal server error")
}

type brokenStore struct{ Store }

func (brokenStore) GetContext(context.Context, string, string) (CurrentContext, bool, error) {
	return CurrentContext{}, false, errors.New("secret redis address 10.0.0.1")
}
func (brokenStore) GetSubscription(context.Context, string) (Subscription, bool, error) {
	return Subscription{}, false, errors.New("secret")
}

func TestHubTooLargeContextIs400(t *testing.T) { // F-7
	st := NewMemoryStore(StoreOptions{MaxContextBytes: 300})
	h := &Hub{Store: st, Bus: NewMemoryBus()}
	big := patient("p1")
	big["note"] = strings.Repeat("x", 1000)
	_, err := h.ChangeContext(context.Background(), "p", evt("t", "Patient-open", item("patient", big)))
	wantErr(t, err, 400, "Context exceeds the maximum size")
}

func TestHubDoesNotMutateCallerPayload(t *testing.T) {
	h := &Hub{Store: NewMemoryStore(StoreOptions{}), Bus: NewMemoryBus()}
	in := evt("t", "Patient-open", item("patient", patient("p1")))
	if _, err := h.ChangeContext(context.Background(), "p", in); err != nil {
		t.Fatal(err)
	}
	if _, has := evOf(in)["context.versionId"]; has {
		t.Error("payload của người gọi bị sửa")
	}
}
