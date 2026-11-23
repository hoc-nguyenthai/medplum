package e2e_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/testkit"
)

// Image: người dùng không phải root (số, để `runAsNonRoot` của K8s kiểm được), có HEALTHCHECK, HEALTHCHECK chạy được.
func TestImageIsHardenedAndHealthcheckWorks(t *testing.T) {
	s := newStack(t, nil)
	img := image(t)
	out, err := exec.Command("docker", "inspect", "--format", "{{.Config.User}}|{{json .Config.Healthcheck.Test}}|{{json .Config.Volumes}}", img).Output()
	if err != nil {
		t.Fatal(err)
	}
	user, rest, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	hc, vols, _ := strings.Cut(rest, "|")
	if vols != "null" {
		t.Errorf("image không được khai báo VOLUME (service không ghi đĩa, chạy với readOnlyRootFilesystem): %s", vols)
	}
	if user != "65532:65532" {
		t.Errorf("USER = %q, muốn 65532:65532 (UID số để runAsNonRoot kiểm được)", user)
	}
	if !strings.Contains(hc, "healthcheck") {
		t.Errorf("image thiếu HEALTHCHECK dùng `/app healthcheck`: %s", hc)
	}
	if code, out := s.gw.Exec(t, "/app", "healthcheck"); code != 0 {
		t.Errorf("`/app healthcheck` thoát %d: %s", code, out)
	}
}

func TestProbesAndRedisOutage(t *testing.T) {
	s := newStack(t, nil)
	for _, p := range []string{"/healthz", "/health/live", "/health/startup", "/health/ready"} {
		if c := status(s.gw.URL + p); c != 200 {
			t.Fatalf("%s: %d", p, c)
		}
	}
	// Redis chết: ready 503 (K8s rút khỏi Service) nhưng live vẫn 200 (K8s KHÔNG restart pod vì lỗi của Redis)
	s.redis.Stop(t)
	eventually(t, "readiness 503 khi Redis chết", 40*time.Second, func() bool { return status(s.gw.URL+"/health/ready") == 503 })
	if c := status(s.gw.URL + "/health/live"); c != 200 {
		t.Errorf("liveness phải độc lập với Redis: %d", c)
	}
	// Redis sống lại: tự hồi phục, không cần restart
	s.redis.Start(t)
	eventually(t, "readiness 200 sau khi Redis sống lại", 60*time.Second, func() bool { return status(s.gw.URL+"/health/ready") == 200 })
}

// Đóng vai fhir-server: ký token bind bằng khóa chung và phát sự kiện lên kênh Redis của Subscription.
func TestSubscriptionSocketDeliversEventsPublishedViaRedis(t *testing.T) {
	s := newStack(t, nil)
	sub := "sub-" + randHex(4)
	c := dial(t, s.gw.WSURL()+"/ws/subscriptions-r4")
	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageText, bindFrame(bindToken(s.key, sub))); err != nil {
		t.Fatal(err)
	}
	if m := readText(t, c); !strings.Contains(m, `"handshake"`) {
		t.Fatalf("sau bind phải có handshake: %s", m)
	}
	pub := s.publisher(t)
	// gateway đã ghi sổ hiện diện: bên phát (fhir-server) thấy "có người nghe" nên mới phát
	bound, err := pub.Present(ctx, fhirsub.WSPresenceKey(sub))
	if err != nil || !bound {
		t.Fatalf("sổ hiện diện: bound=%v err=%v", bound, err)
	}
	resource := json.RawMessage(`{"resourceType":"Observation","id":"o-e2e","status":"final","code":{"text":"x"},"meta":{"project":"e2e"}}`)
	raw, _ := json.Marshal(fhirsub.WSMessage{Sub: sub, Event: fhirsub.ChangeEvent{Interaction: "create", ResourceType: "Observation", ID: "o-e2e", Resource: resource}})
	if err := pub.PublishChannel(ctx, fhirsub.WSChannel(sub), raw); err != nil {
		t.Fatal(err)
	}
	if m := readUntil(t, c, "event-notification"); !strings.Contains(m, "o-e2e") {
		t.Errorf("thông báo thiếu tài nguyên: %s", m)
	}
	// socket đóng thì gateway gỡ sổ hiện diện (bên phát thôi gửi): không cần chờ TTL
	_ = c.Close(websocket.StatusNormalClosure, "")
	eventually(t, "sổ hiện diện được gỡ sau khi socket cuối đóng", 40*time.Second, func() bool {
		bound, _ := pub.Present(ctx, fhirsub.WSPresenceKey(sub))
		return !bound
	})
}

func TestSubscriptionSocketRejectsForgedToken(t *testing.T) {
	s := newStack(t, nil)
	c := dial(t, s.gw.WSURL()+"/ws/subscriptions-r4")
	if err := c.Write(context.Background(), websocket.MessageText, bindFrame(bindToken("khóa-khác-không-phải-của-gateway-0123456789", "sub-x"))); err != nil {
		t.Fatal(err)
	}
	if code := closeStatus(t, c); code != websocket.StatusPolicyViolation {
		t.Errorf("token ký bằng khóa lạ phải bị đóng 1008, được %d", code)
	}
}

// FHIRcast end-to-end qua image: HTTP (đăng ký, đổi ngữ cảnh, đọc ngữ cảnh) và WebSocket.
func TestFhircastOverHTTPAndWebSocket(t *testing.T) {
	s := newStack(t, nil)
	tok := s.jwtToken(t, "p-e2e", "alice", "openid fhircast/*")

	if code, _ := call(t, "GET", s.gw.URL+"/fhircast/STU3/.well-known/fhircast-configuration", "", nil); code != 200 {
		t.Fatalf("well-known: %d", code)
	}
	if code, _ := call(t, "GET", s.gw.URL+"/hub/t", "", nil); code != 401 {
		t.Errorf("không token phải 401: %d", code)
	}
	if code, _ := call(t, "GET", s.gw.URL+"/hub/t", s.jwtToken(t, "p-e2e", "alice", "fhir.write"), nil); code != 403 {
		t.Errorf("thiếu scope fhircast phải 403: %d", code)
	}

	code, sub := call(t, "POST", s.gw.URL+"/fhircast/STU3", tok, map[string]any{
		"hub.channel.type": "websocket", "hub.mode": "subscribe", "hub.topic": "topic-e2e", "hub.events": "Patient-open,Patient-close"})
	endpoint, _ := sub["hub.channel.endpoint"].(string)
	if code != 202 || !strings.HasPrefix(endpoint, publicWS+"/ws/fhircast/") {
		t.Fatalf("đăng ký: %d %v", code, sub)
	}
	c := dial(t, s.gw.WSURL()+"/ws/fhircast/"+endpoint[strings.LastIndex(endpoint, "/")+1:])
	conf := readText(t, c)
	if !strings.Contains(conf, `"hub.mode":"subscribe"`) || !strings.Contains(conf, `"hub.topic":"topic-e2e"`) || !strings.Contains(conf, `"hub.lease_seconds":3600`) {
		t.Fatalf("xác nhận: %s", conf)
	}

	open := map[string]any{"id": "m1", "timestamp": time.Now().UTC().Format(time.RFC3339), "event": map[string]any{
		"hub.topic": "topic-e2e", "hub.event": "Patient-open", "context": []any{
			map[string]any{"key": "patient", "resource": map[string]any{"resourceType": "Patient", "id": "p1"}}}}}
	if code, body := call(t, "POST", s.gw.URL+"/hub", tok, open); code != 202 || body["success"] != true {
		t.Fatalf("đổi ngữ cảnh: %d %v", code, body)
	}
	if m := readUntil(t, c, "Patient-open"); !strings.Contains(m, "context.versionId") {
		t.Errorf("sự kiện thiếu context.versionId: %s", m)
	}
	if code, ctxBody := call(t, "GET", s.gw.URL+"/hub/topic-e2e", tok, nil); code != 200 || ctxBody["context.type"] != "Patient" {
		t.Errorf("đọc ngữ cảnh: %d %v", code, ctxBody)
	}
	// project khác không thấy ngữ cảnh và không nhận sự kiện
	if _, other := call(t, "GET", s.gw.URL+"/hub/topic-e2e", s.jwtToken(t, "p-khac", "bob", "fhircast/*"), nil); other["context.type"] != "" {
		t.Errorf("project khác thấy ngữ cảnh: %v", other)
	}
	// hủy đăng ký: socket nhận denied rồi bị đóng
	if code, _ := call(t, "POST", s.gw.URL+"/hub", tok, map[string]any{
		"hub.channel.type": "websocket", "hub.mode": "unsubscribe", "hub.topic": "topic-e2e", "hub.channel.endpoint": endpoint}); code != 202 {
		t.Fatalf("hủy: %d", code)
	}
	if m := readUntil(t, c, `"hub.mode":"denied"`); !strings.Contains(m, "Subscriber unsubscribed from topic") {
		t.Errorf("denied: %s", m)
	}
	if code := closeStatus(t, c); code != websocket.StatusNormalClosure {
		t.Errorf("sau denied phải đóng 1000, được %d", code)
	}
}

// Tắt êm: SIGTERM thì socket nhận 1001 và tiến trình thoát mã 0 trong hạn.
func TestGracefulShutdownSendsGoingAwayAndExitsZero(t *testing.T) {
	s := newStack(t, func(e map[string]string) { e["SHUTDOWN_DRAIN_DELAY"] = "1s" })
	c := dial(t, s.gw.WSURL()+"/ws/subscriptions-r4")
	if err := c.Write(context.Background(), websocket.MessageText, bindFrame(bindToken(s.key, "sub-term"))); err != nil {
		t.Fatal(err)
	}
	readText(t, c) // handshake
	done := make(chan int, 1)
	go func() { done <- s.gw.Terminate(t, 20*time.Second) }()
	if code := closeStatus(t, c); code != websocket.StatusGoingAway {
		t.Errorf("client phải nhận 1001, được %d", code)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("mã thoát = %d, muốn 0", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("service không thoát sau SIGTERM")
	}
}

// Cấu hình sai thì từ chối chạy ngay (không nghe cổng), nói rõ biến nào sai, mã thoát khác 0.
func TestRefusesToStartOnBadConfiguration(t *testing.T) {
	img := image(t)
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"thiếu khóa ký":                  {map[string]string{"REDIS_URL": "redis://r:6379/0"}, "FHIR_WS_SIGNING_KEY"},
		"khóa ngắn":                      {map[string]string{"REDIS_URL": "redis://r:6379/0", "FHIR_WS_SIGNING_KEY": "short"}, "FHIR_WS_SIGNING_KEY"},
		"production dùng Redis mặc định": {map[string]string{"ENVIRONMENT": "production", "FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40)}, "REDIS_URL"},
		"FHIRcast thiếu xác thực":        {map[string]string{"REDIS_URL": "redis://r:6379/0", "FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40), "FHIRCAST_ENABLED": "true"}, "FHIRCAST_"},
	} {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: c.env}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, c.want) {
			t.Errorf("%s: mã thoát %d, log phải nhắc %q:\n%s", name, code, c.want, logs)
		}
	}
}
