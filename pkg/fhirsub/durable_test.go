package fhirsub_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

// Giao rest-hook BỀN (Config.Durable, P0-1 của docs/plans/fhir-server-split-candidates.md): giao đồng bộ trong handler của Redis Stream, ACK sau khi giao xong hoặc bỏ cuộc.

func durableCfg() fhirsub.Config {
	c := fastCfg()
	c.Durable = true
	c.MaxAttempts = 4
	return c
}

// node là một "tiến trình" notifier: broker Redis riêng (group dùng chung) và Manager Durable đọc Subscription từ kho chung.
type node struct {
	mgr    *fhirsub.Manager
	broker *messaging.Redis
}

func startNode(t *testing.T, addr string, mem *fhirrouter.MemoryRepository, cfg fhirsub.Config, claimIdle time.Duration) *node {
	t.Helper()
	b, err := messaging.NewRedis("redis://"+addr, messaging.RedisOptions{
		Group: "notifier", Block: 20 * time.Millisecond, Concurrency: 4, ClaimIdle: claimIdle, ClaimInterval: claimIdle / 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := fhirsub.NewManager(mem, cfg)
	mgr.Register(b)
	mgr.Start()
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := &node{mgr: mgr, broker: b}
	t.Cleanup(func() { n.stop() })
	return n
}

func (n *node) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = n.mgr.Stop(ctx)
	_ = n.broker.Stop(ctx)
}

// api mô phỏng fhir-server: ghi vào kho và phát sự kiện lên Redis, không giao gì.
func newAPI(t *testing.T, addr string) (*fhirsub.Repo, *fhirrouter.MemoryRepository) {
	t.Helper()
	mem := fhirrouter.NewMemoryRepository()
	b, err := messaging.NewRedis("redis://"+addr, messaging.RedisOptions{Group: "api"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	return fhirsub.Wrap(mem, b), mem
}

func subscribeTo(t *testing.T, repo fhirrouter.Repository, id, criteria, endpoint string) {
	t.Helper()
	js := `{"resourceType":"Subscription","id":"` + id + `","status":"active","reason":"test","criteria":"` + criteria + `","channel":{"type":"rest-hook","endpoint":"` + endpoint + `"}}`
	if _, err := repo.CreateResource(context.Background(), resource(t, js)); err != nil {
		t.Fatal(err)
	}
}

func createObs(t *testing.T, repo fhirrouter.Repository, js string) {
	t.Helper()
	if _, err := repo.CreateResource(context.Background(), resource(t, js)); err != nil {
		t.Fatal(err)
	}
}

func TestDurableDeliversInsideTheHandlerAndRetriesInPlace(t *testing.T) {
	addr := redisAddr(t)
	api, mem := newAPI(t, addr)
	h := newHook(t, 503, 503) // hai lần đầu 503, lần ba 200
	n := startNode(t, addr, mem, durableCfg(), time.Minute)
	subscribeTo(t, api, "s1", "Observation?code=1234", h.srv.URL)
	n.mgr.Invalidate()
	time.Sleep(100 * time.Millisecond)

	createObs(t, api, obs)
	eventually(t, "3 lần gọi (2 lần 503 rồi 200)", func() bool { return h.count() == 3 })
	eventually(t, "giao thành công", func() bool { return n.mgr.Stats().Delivered == 1 })
	st := n.mgr.Stats()
	if st.Retried != 2 || st.Failed != 0 || st.Dropped != 0 {
		t.Errorf("stats: %+v", st)
	}
	stays(t, "giao thêm sau khi đã ACK", func() bool { return h.count() > 3 })
}

func TestDurableGivesUpOnPermanentErrorAndAcks(t *testing.T) {
	addr := redisAddr(t)
	api, mem := newAPI(t, addr)
	h := newHook(t, 400, 400, 400, 400, 400)
	n := startNode(t, addr, mem, durableCfg(), 300*time.Millisecond) // ClaimIdle ngắn: nếu không ACK thì sẽ bị giao lại và test thấy
	subscribeTo(t, api, "s1", "Observation?code=1234", h.srv.URL)
	n.mgr.Invalidate()
	time.Sleep(100 * time.Millisecond)

	createObs(t, api, obs)
	eventually(t, "bỏ cuộc sau một lần (4xx không thử lại)", func() bool { return n.mgr.Stats().Failed == 1 })
	time.Sleep(1200 * time.Millisecond) // vài chu kỳ reclaim
	if h.count() != 1 {
		t.Errorf("4xx không được thử lại hay giao lại sau khi đã ACK: %d lần gọi", h.count())
	}
}

func TestDurableStopsRetryingAfterMaxAttempts(t *testing.T) {
	addr := redisAddr(t)
	api, mem := newAPI(t, addr)
	h := newHook(t, 500, 500, 500, 500, 500, 500)
	n := startNode(t, addr, mem, durableCfg(), 400*time.Millisecond)
	subscribeTo(t, api, "s1", "Observation?code=1234", h.srv.URL)
	n.mgr.Invalidate()
	time.Sleep(100 * time.Millisecond)

	createObs(t, api, obs)
	eventually(t, "bỏ cuộc sau MaxAttempts=4", func() bool { return n.mgr.Stats().Failed == 1 })
	time.Sleep(1500 * time.Millisecond)
	if h.count() != 4 {
		t.Errorf("muốn đúng MaxAttempts=4 lần gọi, được %d", h.count())
	}
}

// Webhook chậm không chặn các sự kiện khác và các Subscription khác của cùng sự kiện.
func TestDurableSlowWebhookDoesNotBlockOthers(t *testing.T) {
	addr := redisAddr(t)
	api, mem := newAPI(t, addr)
	slow, fast := newHook(t), newHook(t)
	slow.block = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(slow.block) }) }
	t.Cleanup(release) // test lỗi giữa chừng cũng không để máy nhận treo (srv.Close chờ handler)
	n := startNode(t, addr, mem, durableCfg(), time.Minute)
	subscribeTo(t, api, "a-slow", "Observation?code=1234", slow.srv.URL) // id đứng đầu thứ tự: nếu giao tuần tự thì fast-same phải chờ nó
	subscribeTo(t, api, "fast-same", "Observation?code=1234", fast.srv.URL)
	subscribeTo(t, api, "fast-other", "Observation?code=9999", fast.srv.URL)
	n.mgr.Invalidate()
	time.Sleep(100 * time.Millisecond)

	createObs(t, api, obs) // khớp slow và fast-same
	eventually(t, "fast-same được giao dù slow đang treo", func() bool { return fast.count() == 1 })
	createObs(t, api, otherObs) // sự kiện khác, trong lúc handler của sự kiện đầu còn treo
	eventually(t, "sự kiện thứ hai được giao dù handler đầu đang treo", func() bool { return fast.count() == 2 })
	release()
	eventually(t, "slow được giao khi mở", func() bool { return n.mgr.Stats().Delivered == 3 })
}

// Tiến trình giao tắt giữa lúc đang giao: message không được ACK nên instance khác (cùng group) nhận lại sau ClaimIdle và giao (ít nhất một lần).
func TestDurableRedeliversAfterTheDeliveringProcessStops(t *testing.T) {
	addr := redisAddr(t)
	api, mem := newAPI(t, addr)
	h := newHook(t)
	h.block = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(h.block) }) }
	t.Cleanup(release)
	a := startNode(t, addr, mem, durableCfg(), 400*time.Millisecond)
	subscribeTo(t, api, "s1", "Observation?code=1234", h.srv.URL)
	a.mgr.Invalidate()
	time.Sleep(100 * time.Millisecond)

	createObs(t, api, obs)
	eventually(t, "A đã gọi webhook (đang treo)", func() bool { return h.count() == 1 })
	var stopped atomic.Bool
	go func() { a.stop(); stopped.Store(true) }() // "chết" giữa lúc giao
	eventually(t, "A dừng được dù webhook còn treo (hủy lần giao đang chạy)", func() bool { return stopped.Load() })
	if a.mgr.Stats().Delivered != 0 {
		t.Fatal("A chưa giao xong")
	}

	release() // máy nhận hoạt động lại
	b := startNode(t, addr, mem, durableCfg(), 400*time.Millisecond)
	eventually(t, "B nhận lại message chưa ACK và giao", func() bool { return b.mgr.Stats().Delivered == 1 })
	if h.count() < 2 {
		t.Errorf("muốn ít nhất 2 lần gọi (A dở dang, B giao lại), được %d", h.count())
	}
}

func TestDurableClaimIdleCoversTheWorstDeliveryChain(t *testing.T) {
	c := fhirsub.Config{} // mặc định: 5 lần, hạn 10s mỗi lần, chờ 1s nhân đôi
	want := 2 * (5*10*time.Second + (1+2+4+8)*time.Second)
	if got := c.DurableClaimIdle(); got != want {
		t.Errorf("mặc định: %s, muốn %s", got, want)
	}
	if got := (fhirsub.Config{MaxAttempts: 1, Timeout: time.Second, BaseDelay: time.Millisecond}).DurableClaimIdle(); got != time.Minute {
		t.Errorf("tối thiểu 1 phút: %s", got)
	}
}
