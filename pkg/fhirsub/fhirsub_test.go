package fhirsub_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

type got struct {
	header http.Header
	body   string
}

// hook là máy nhận webhook; script là các mã trả lời lần lượt (hết thì 200).
type hook struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []got
	script []int
	block  chan struct{}
}

func newHook(t *testing.T, script ...int) *hook {
	h := &hook{script: script}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.calls = append(h.calls, got{r.Header.Clone(), string(b)})
		status := 200
		if n := len(h.calls); n <= len(h.script) {
			status = h.script[n-1]
		}
		block := h.block
		h.mu.Unlock()
		if block != nil {
			<-block
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) count() int     { h.mu.Lock(); defer h.mu.Unlock(); return len(h.calls) }
func (h *hook) call(i int) got { h.mu.Lock(); defer h.mu.Unlock(); return h.calls[i] }

type env struct {
	t      *testing.T
	repo   *fhirsub.Repo
	mem    *fhirrouter.MemoryRepository
	mgr    *fhirsub.Manager
	broker *messaging.InMemory
}

func newEnv(t *testing.T, cfg fhirsub.Config) *env {
	t.Helper()
	mem := fhirrouter.NewMemoryRepository()
	broker := messaging.NewInMemory()
	mgr := fhirsub.NewManager(mem, cfg)
	mgr.Register(broker)
	mgr.Start()
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })
	return &env{t, fhirsub.Wrap(mem, broker), mem, mgr, broker}
}

func fastCfg() fhirsub.Config {
	return fhirsub.Config{AllowPrivate: true, BaseDelay: 5 * time.Millisecond, Timeout: 2 * time.Second, CacheTTL: time.Hour}
}

func resource(t *testing.T, js string) fhir.Resource {
	t.Helper()
	r, err := fhir.UnmarshalResource([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *env) create(js string) fhir.Resource {
	e.t.Helper()
	r, err := e.repo.CreateResource(context.Background(), resource(e.t, js))
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) subscribe(id, criteria, endpoint string, extra ...string) {
	e.t.Helper()
	js := `{"resourceType":"Subscription","id":"` + id + `","status":"active","reason":"test","criteria":"` + criteria +
		`","channel":{"type":"rest-hook","endpoint":"` + endpoint + `","header":["X-Custom: abc"]}` + joinExtra(extra) + `}`
	e.create(js)
}

func joinExtra(extra []string) string {
	s := ""
	for _, x := range extra {
		s += "," + x
	}
	return s
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("quá hạn chờ: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stays khẳng định điều kiện không xảy ra trong một khoảng ngắn (kiểm tra "không giao").
func stays(t *testing.T, what string, cond func() bool) {
	t.Helper()
	time.Sleep(120 * time.Millisecond)
	if cond() {
		t.Fatalf("không được xảy ra: %s", what)
	}
}

const obs = `{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"1234"}]}}`
const otherObs = `{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"9999"}]}}`

func TestDeliveryForCreateUpdateDelete(t *testing.T) {
	h := newHook(t)
	e := newEnv(t, fastCfg())
	secret := "s3cret"
	e.subscribe("sub1", "Observation?code=http://loinc.org|1234", h.srv.URL+"/hook",
		`"extension":[{"url":"https://www.medplum.com/fhir/StructureDefinition/subscription-secret","valueString":"`+secret+`"}]`)

	created := e.create(obs)
	e.create(otherObs) // không khớp criteria
	eventually(t, "giao create", func() bool { return h.count() >= 1 })
	stays(t, "giao thêm cho resource không khớp", func() bool { return h.count() != 1 })

	c := h.call(0)
	if c.header.Get("Content-Type") != "application/fhir+json" || c.header.Get("X-Medplum-Subscription") != "sub1" ||
		c.header.Get("X-Medplum-Interaction") != "create" || c.header.Get("X-Custom") != "abc" {
		t.Errorf("header: %v", c.header)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(c.body), &body); err != nil || body["id"] != created.GetID() || body["resourceType"] != "Observation" {
		t.Errorf("body: %s", c.body)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(c.body))
	if c.header.Get("X-Signature") != hex.EncodeToString(mac.Sum(nil)) {
		t.Errorf("chữ ký sai: %s", c.header.Get("X-Signature"))
	}

	upd := resource(t, obs)
	upd.(*fhir.Observation).ID = created.GetID()
	if _, err := e.repo.UpdateResource(context.Background(), upd, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "giao update", func() bool { return h.count() >= 2 })
	if h.call(1).header.Get("X-Medplum-Interaction") != "update" {
		t.Errorf("update: %v", h.call(1).header)
	}

	if err := e.repo.DeleteResource(context.Background(), "Observation", created.GetID()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "giao delete", func() bool { return h.count() >= 3 })
	d := h.call(2)
	if d.header.Get("X-Medplum-Interaction") != "delete" || d.header.Get("X-Medplum-Deleted-Resource") != "Observation/"+created.GetID() || d.body != "{}" {
		t.Errorf("delete: %v %q", d.header, d.body)
	}
	if st := e.mgr.Stats(); st.Delivered != 3 || st.Failed != 0 || st.Matched != 3 {
		t.Errorf("stats: %+v", st)
	}
}

func TestInteractionFilter(t *testing.T) {
	h := newHook(t)
	e := newEnv(t, fastCfg())
	e.subscribe("only-create", "Observation", h.srv.URL,
		`"extension":[{"url":"https://medplum.com/fhir/StructureDefinition/subscription-supported-interaction","valueCode":"create"}]`)
	r := e.create(obs)
	eventually(t, "create", func() bool { return h.count() == 1 })
	upd := resource(t, obs)
	upd.(*fhir.Observation).ID = r.GetID()
	if _, err := e.repo.UpdateResource(context.Background(), upd, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = e.repo.DeleteResource(context.Background(), "Observation", r.GetID())
	stays(t, "update/delete không nằm trong interaction được hỗ trợ", func() bool { return h.count() != 1 })
}

func TestRetries(t *testing.T) {
	t.Run("thử lại tới khi thành công", func(t *testing.T) {
		h := newHook(t, 500, 503)
		e := newEnv(t, fastCfg())
		e.subscribe("s", "Observation", h.srv.URL)
		e.create(obs)
		eventually(t, "3 lần gọi", func() bool { return h.count() == 3 })
		eventually(t, "giao xong", func() bool { return e.mgr.Stats().Delivered == 1 })
		if st := e.mgr.Stats(); st.Retried != 2 || st.Failed != 0 {
			t.Errorf("stats: %+v", st)
		}
	})
	t.Run("429 và 408 cũng thử lại", func(t *testing.T) {
		h := newHook(t, 429, 408)
		e := newEnv(t, fastCfg())
		e.subscribe("s", "Observation", h.srv.URL)
		e.create(obs)
		eventually(t, "giao xong", func() bool { return e.mgr.Stats().Delivered == 1 })
	})
	t.Run("lỗi 4xx của người nhận không thử lại", func(t *testing.T) {
		h := newHook(t, 400, 200)
		e := newEnv(t, fastCfg())
		e.subscribe("s", "Observation", h.srv.URL)
		e.create(obs)
		eventually(t, "bỏ cuộc", func() bool { return e.mgr.Stats().Failed == 1 })
		stays(t, "thử lại sau 400", func() bool { return h.count() != 1 })
	})
	t.Run("hết số lần thử thì bỏ cuộc", func(t *testing.T) {
		h := newHook(t, 500, 500, 500, 500, 500, 500, 500)
		cfg := fastCfg()
		cfg.MaxAttempts = 3
		e := newEnv(t, cfg)
		e.subscribe("s", "Observation", h.srv.URL)
		e.create(obs)
		eventually(t, "bỏ cuộc", func() bool { return e.mgr.Stats().Failed == 1 })
		stays(t, "vượt MaxAttempts", func() bool { return h.count() != 3 })
	})
	t.Run("không theo redirect", func(t *testing.T) {
		target := newHook(t)
		redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.srv.URL, http.StatusFound)
		}))
		t.Cleanup(redir.Close)
		e := newEnv(t, fastCfg())
		e.subscribe("s", "Observation", redir.URL)
		e.create(obs)
		eventually(t, "bỏ cuộc", func() bool { return e.mgr.Stats().Failed == 1 })
		stays(t, "đích redirect bị gọi", func() bool { return target.count() != 0 })
	})
}

func TestSSRFGuardBlocksInternalTargets(t *testing.T) {
	h := newHook(t) // nghe trên 127.0.0.1
	cfg := fastCfg()
	cfg.AllowPrivate = false
	e := newEnv(t, cfg)
	e.subscribe("s", "Observation", h.srv.URL)
	e.create(obs)
	eventually(t, "bị chặn và bỏ cuộc", func() bool { return e.mgr.Stats().Failed == 1 })
	if st := e.mgr.Stats(); st.Retried != 0 || st.Delivered != 0 {
		t.Errorf("địa chỉ bị chặn không được thử lại: %+v", st)
	}
	if h.count() != 0 {
		t.Errorf("máy nhận nội bộ không được bị gọi: %d", h.count())
	}
}

func TestSubscriptionChangesApplyImmediately(t *testing.T) {
	h := newHook(t)
	e := newEnv(t, fastCfg()) // cache TTL 1 giờ: chỉ sự kiện của chính Subscription mới làm mới cache
	e.create(obs)             // chưa có Subscription
	e.subscribe("s", "Observation", h.srv.URL)
	stays(t, "giao cho resource tạo trước Subscription", func() bool { return h.count() != 0 })
	e.create(obs)
	eventually(t, "giao sau khi có Subscription", func() bool { return h.count() == 1 })

	off := resource(t, `{"resourceType":"Subscription","id":"s","status":"off","reason":"t","criteria":"Observation","channel":{"type":"rest-hook","endpoint":"`+h.srv.URL+`"}}`)
	if _, err := e.repo.UpdateResource(context.Background(), off, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	e.create(obs)
	stays(t, "giao khi Subscription đã tắt", func() bool { return h.count() != 1 })
}

func TestSkippedSubscriptions(t *testing.T) {
	h := newHook(t)
	e := newEnv(t, fastCfg())
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	e.create(`{"resourceType":"Subscription","id":"expired","status":"active","reason":"t","criteria":"Observation","end":"` + past + `","channel":{"type":"rest-hook","endpoint":"` + h.srv.URL + `"}}`)
	e.create(`{"resourceType":"Subscription","id":"ws","status":"active","reason":"t","criteria":"Observation","channel":{"type":"websocket"}}`)
	e.create(`{"resourceType":"Subscription","id":"badcrit","status":"active","reason":"t","criteria":"Observation?nope=1","channel":{"type":"rest-hook","endpoint":"` + h.srv.URL + `"}}`)
	e.create(`{"resourceType":"Subscription","id":"badurl","status":"active","reason":"t","criteria":"Observation","channel":{"type":"rest-hook","endpoint":"ftp://x/y"}}`)
	e.create(`{"resourceType":"Subscription","id":"otherType","status":"active","reason":"t","criteria":"Patient","channel":{"type":"rest-hook","endpoint":"` + h.srv.URL + `"}}`)
	e.subscribe("good", "Observation", h.srv.URL)
	e.create(obs)
	eventually(t, "chỉ Subscription tốt được giao", func() bool { return h.count() == 1 })
	stays(t, "giao thêm", func() bool { return h.count() != 1 })
	if h.call(0).header.Get("X-Medplum-Subscription") != "good" {
		t.Errorf("sai subscription: %v", h.call(0).header)
	}
}

func TestEventsOnlyAfterCommit(t *testing.T) {
	h := newHook(t)
	e := newEnv(t, fastCfg())
	e.subscribe("s", "Observation", h.srv.URL)

	boom := errors.New("rollback")
	err := e.repo.RunInTransaction(context.Background(), func(rp fhirrouter.Repository) error {
		if _, err := rp.CreateResource(context.Background(), resource(t, obs)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("muốn lỗi rollback, được %v", err)
	}
	stays(t, "giao sự kiện của transaction đã rollback", func() bool { return h.count() != 0 })

	err = e.repo.RunInTransaction(context.Background(), func(rp fhirrouter.Repository) error {
		if _, err := rp.CreateResource(context.Background(), resource(t, obs)); err != nil {
			return err
		}
		time.Sleep(60 * time.Millisecond)
		if h.count() != 0 {
			t.Error("sự kiện phải chờ commit")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "giao sau commit", func() bool { return h.count() == 1 })
}

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, core.Event) error {
	return errors.New("broker chết")
}

func TestPublishFailureDoesNotFailWrites(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	repo := fhirsub.Wrap(mem, failingPublisher{})
	r, err := repo.CreateResource(context.Background(), resource(t, obs))
	if err != nil {
		t.Fatalf("ghi phải thành công dù không phát được sự kiện: %v", err)
	}
	if _, err := mem.ReadResource(context.Background(), "Observation", r.GetID()); err != nil {
		t.Fatal(err)
	}
}

func TestQueueOverflowDropsInsteadOfBlocking(t *testing.T) {
	h := newHook(t)
	h.block = make(chan struct{})
	cfg := fastCfg()
	cfg.Workers, cfg.QueueSize = 1, 1
	e := newEnv(t, cfg)
	e.subscribe("s", "Observation", h.srv.URL)
	start := time.Now()
	for i := 0; i < 6; i++ {
		e.create(obs)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("ghi bị chặn bởi hàng đợi giao")
	}
	eventually(t, "có thông báo bị bỏ", func() bool { return e.mgr.Stats().Dropped >= 1 })
	close(h.block)
}

func TestPassThroughAndStop(t *testing.T) {
	e := newEnv(t, fastCfg())
	r := e.create(`{"resourceType":"Patient","name":[{"family":"P"}]}`)
	if n, err := e.repo.Reindex(context.Background(), "Patient", "", nil); err != nil || n != 1 {
		t.Errorf("reindex: %d %v", n, err)
	}
	if err := e.repo.Expunge(context.Background(), "Patient", r.GetID()); err != nil {
		t.Errorf("expunge: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.mgr.Stop(ctx); err != nil {
		t.Errorf("stop: %v", err)
	}
}

// Subscription chỉ nhận sự kiện của cùng project: project khác không được nhận dữ liệu của tenant này.
func TestSubscriptionOnlySeesOwnProject(t *testing.T) {
	hA, hB, hNone := newHook(t), newHook(t), newHook(t)
	e := newEnv(t, fastCfg())
	sub := func(id, project, endpoint string) {
		meta := ""
		if project != "" {
			meta = `,"meta":{"project":"` + project + `"}`
		}
		e.create(`{"resourceType":"Subscription","id":"` + id + `","status":"active","reason":"t","criteria":"Observation",` +
			`"channel":{"type":"rest-hook","endpoint":"` + endpoint + `"}` + meta + `}`)
	}
	sub("sa", "A", hA.srv.URL)
	sub("sb", "B", hB.srv.URL)
	sub("sn", "", hNone.srv.URL)

	e.create(`{"resourceType":"Observation","status":"final","code":{"text":"x"},"meta":{"project":"A"}}`)
	eventually(t, "Subscription của A nhận sự kiện của A", func() bool { return hA.count() == 1 })
	stays(t, "B hoặc Subscription không project nhận dữ liệu của A", func() bool { return hB.count() != 0 || hNone.count() != 0 })

	e.create(obs) // không có project
	eventually(t, "Subscription không project nhận sự kiện không project", func() bool { return hNone.count() == 1 })
	stays(t, "A hoặc B nhận dữ liệu không project", func() bool { return hA.count() != 1 || hB.count() != 0 })
}
