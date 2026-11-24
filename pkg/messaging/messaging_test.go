package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/messaging"
)

type thingHappened struct {
	core.BaseEvent
	Name string `json:"name"`
}

func (thingHappened) EventType() string { return "ThingHappened" }

func newEvent(name string) thingHappened {
	return thingHappened{BaseEvent: core.NewBaseEvent(), Name: name}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	e := newEvent("a")
	env, err := messaging.NewEnvelope(e)
	if err != nil || env.Type != "ThingHappened" || env.ID != e.EventID() {
		t.Fatalf("env = %+v err=%v", env, err)
	}
	var got thingHappened
	if err := env.Decode(&got); err != nil || got.Name != "a" {
		t.Fatalf("decode = %+v err=%v", got, err)
	}
}

type badEvent struct {
	core.BaseEvent
	C chan int `json:"c"`
}

func (badEvent) EventType() string { return "Bad" }

func TestPublishRejectsUnserializableEvent(t *testing.T) {
	b := messaging.NewInMemory()
	if err := b.Publish(context.Background(), badEvent{BaseEvent: core.NewBaseEvent(), C: make(chan int)}); err == nil {
		t.Fatal("phải lỗi")
	}
}

func TestInMemoryDispatchesAndSwallowsHandlerErrors(t *testing.T) {
	b := messaging.NewInMemory()
	var got atomic.Int32
	b.Subscribe("ThingHappened", func(context.Context, messaging.Envelope) error { return errors.New("hỏng") })
	b.Subscribe("ThingHappened", func(_ context.Context, env messaging.Envelope) error {
		var e thingHappened
		if err := env.Decode(&e); err != nil || e.Name != "x" {
			t.Errorf("decode: %v %+v", err, e)
		}
		got.Add(1)
		return nil
	})
	ctx := context.Background()
	if err := b.Publish(ctx, newEvent("x")); err != nil || got.Load() != 1 {
		t.Fatalf("publish err=%v got=%d", err, got.Load())
	}
	_ = b.Publish(ctx, badEvent2{core.NewBaseEvent()}) // topic không có handler: bỏ qua
	if b.Ping(ctx) != nil || b.Start(ctx) != nil || b.Stop(ctx) != nil {
		t.Fatal("lifecycle in-memory không được lỗi")
	}
}

type badEvent2 struct{ core.BaseEvent }

func (badEvent2) EventType() string { return "NoHandler" }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("quá thời gian chờ")
}

func TestRedisBrokerDeliversAndAcks(t *testing.T) {
	mr := newRedis(t)
	b, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: "redis://" + mr.Addr(), Group: "g"})
	if err != nil {
		t.Fatal(err)
	}
	var names atomic.Value
	var calls atomic.Int32
	b.Subscribe("ThingHappened", func(_ context.Context, env messaging.Envelope) error {
		var e thingHappened
		_ = env.Decode(&e)
		names.Store(e.Name)
		// lần đầu lỗi -> không ACK; message không bị mất
		if calls.Add(1) == 1 {
			return errors.New("lần đầu lỗi")
		}
		return nil
	})
	ctx := context.Background()
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, newEvent("redis")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return calls.Load() >= 1 })
	if names.Load() != "redis" {
		t.Fatalf("name = %v", names.Load())
	}
	if err := b.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRedisBrokerStartIsIdempotentForGroup(t *testing.T) {
	mr := newRedis(t)
	mk := func() messaging.Broker {
		b, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{Block: 20 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		b.Subscribe("ThingHappened", func(context.Context, messaging.Envelope) error { return nil })
		return b
	}
	ctx := context.Background()
	a, b := mk(), mk()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(ctx); err != nil { // BUSYGROUP phải được bỏ qua
		t.Fatalf("start lần 2: %v", err)
	}
	_ = a.Stop(ctx)
	_ = b.Stop(ctx)
}

func TestRedisBrokerWithoutHandlersAndErrors(t *testing.T) {
	mr := newRedis(t)
	b, _ := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{})
	ctx := context.Background()
	if err := b.Start(ctx); err != nil { // không có handler: không chạy consumer
		t.Fatal(err)
	}
	if err := b.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Ping(ctx); err == nil {
		t.Fatal("client đã đóng phải lỗi")
	}
	if err := b.Publish(ctx, badEvent{BaseEvent: core.NewBaseEvent(), C: make(chan int)}); err == nil {
		t.Fatal("event không mã hóa được phải lỗi")
	}
}

func TestNewFactoryErrors(t *testing.T) {
	if b, err := messaging.New(messaging.Config{}); err != nil || b == nil {
		t.Fatal("mặc định phải là memory")
	}
	for _, cfg := range []messaging.Config{{Driver: "redis"}, {Driver: "redis", RedisURL: "x://"}, {Driver: "kafka"}} {
		if _, err := messaging.New(cfg); err == nil {
			t.Errorf("%+v phải lỗi", cfg)
		}
	}
}

func TestRedisBroadcastReachesEveryInstanceWhileGroupSplits(t *testing.T) {
	mr := newRedis(t)
	ctx := context.Background()
	var groupTotal, bcastA, bcastB atomic.Int64
	mk := func(bcast *atomic.Int64) messaging.Broker {
		b, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{Block: 20 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		b.Subscribe("ThingHappened", func(context.Context, messaging.Envelope) error { groupTotal.Add(1); return nil })
		b.SubscribeBroadcast("ThingHappened", func(context.Context, messaging.Envelope) error { bcast.Add(1); return nil })
		return b
	}
	a, b := mk(&bcastA), mk(&bcastB)
	// event cũ (trước Start) không được broadcast lại
	if err := a.Publish(ctx, newEvent("old")); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	const n = 5
	for i := 0; i < n; i++ {
		if err := a.Publish(ctx, newEvent("x")); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (bcastA.Load() < n || bcastB.Load() < n || groupTotal.Load() < n+1) {
		time.Sleep(10 * time.Millisecond)
	}
	if bcastA.Load() != n || bcastB.Load() != n {
		t.Errorf("broadcast: A=%d B=%d, muốn mỗi bên %d", bcastA.Load(), bcastB.Load(), n)
	}
	if groupTotal.Load() != n+1 { // theo nhóm: mỗi event (kể cả event cũ) được xử lý đúng một lần trên cả hai instance
		t.Errorf("group = %d, muốn %d", groupTotal.Load(), n+1)
	}
	_ = a.Stop(ctx)
	_ = b.Stop(ctx)
}

func TestInMemoryBroadcastIsSubscribe(t *testing.T) {
	b := messaging.NewInMemory()
	var got atomic.Int64
	var _ messaging.Broadcaster = b
	b.SubscribeBroadcast("ThingHappened", func(context.Context, messaging.Envelope) error { got.Add(1); return nil })
	_ = b.Publish(context.Background(), newEvent("x"))
	if got.Load() != 1 {
		t.Fatal("memory broadcast không nhận event")
	}
}

func fastRedis(t *testing.T, mr *testRedis, max int) *messaging.Redis {
	t.Helper()
	b, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{
		Block: 10 * time.Millisecond, ClaimIdle: 30 * time.Millisecond, ClaimInterval: 15 * time.Millisecond, MaxDeliveries: max,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRedisRetriesFailedMessageUntilItSucceeds(t *testing.T) {
	mr := newRedis(t)
	b := fastRedis(t, mr, 5)
	var calls atomic.Int32
	b.Subscribe("ThingHappened", func(context.Context, messaging.Envelope) error {
		if calls.Add(1) < 3 {
			return errors.New("lỗi tạm thời")
		}
		return nil
	})
	ctx := context.Background()
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(ctx)
	if err := b.Publish(ctx, newEvent("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return calls.Load() >= 3 })
	time.Sleep(150 * time.Millisecond)
	if n := calls.Load(); n != 3 {
		t.Fatalf("sau khi thành công message không được giao lại, calls = %d", n)
	}
	if _, err := mr.Stream("trivita:events:ThingHappened:dead"); err == nil {
		if r, _ := mr.Stream("trivita:events:ThingHappened:dead"); len(r) != 0 {
			t.Fatal("không được vào dead-letter")
		}
	}
}

func TestRedisPoisonMessageGoesToDeadLetter(t *testing.T) {
	mr := newRedis(t)
	b := fastRedis(t, mr, 3)
	var calls atomic.Int32
	b.Subscribe("ThingHappened", func(context.Context, messaging.Envelope) error {
		calls.Add(1)
		return errors.New("luôn lỗi")
	})
	ctx := context.Background()
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(ctx)
	if err := b.Publish(ctx, newEvent("poison")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		r, _ := mr.Stream("trivita:events:ThingHappened:dead")
		return len(r) == 1
	})
	if n := calls.Load(); n != 3 {
		t.Fatalf("số lần giao = %d, muốn 3", n)
	}
	r, _ := mr.Stream("trivita:events:ThingHappened:dead")
	if len(r[0].Values) < 6 { // data + original_id + deliveries
		t.Fatalf("dead-letter thiếu trường: %v", r[0].Values)
	}
}

func TestRedisReclaimsMessagesOfDeadConsumer(t *testing.T) {
	mr := newRedis(t)
	ctx := context.Background()
	const stream = "trivita:events:ThingHappened"
	// một consumer đã chết: đọc message qua group rồi không ACK (xảy ra TRƯỚC khi broker thật khởi động)
	ghost := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer ghost.Close()
	if err := ghost.XGroupCreateMkStream(ctx, stream, "trivita", "0").Err(); err != nil {
		t.Fatal(err)
	}
	env, _ := messaging.NewEnvelope(newEvent("orphan"))
	raw, _ := json.Marshal(env)
	if err := ghost.XAdd(ctx, &goredis.XAddArgs{Stream: stream, Values: map[string]any{"data": string(raw)}}).Err(); err != nil {
		t.Fatal(err)
	}
	res, err := ghost.XReadGroup(ctx, &goredis.XReadGroupArgs{Group: "trivita", Consumer: "ghost", Streams: []string{stream, ">"}, Count: 1}).Result()
	if err != nil || len(res) != 1 || len(res[0].Messages) != 1 {
		t.Fatalf("ghost không đọc được: %v %v", res, err)
	}

	b := fastRedis(t, mr, 5)
	var got atomic.Value
	b.Subscribe("ThingHappened", func(_ context.Context, env messaging.Envelope) error {
		var e thingHappened
		_ = env.Decode(&e)
		got.Store(e.Name)
		return nil
	})
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(ctx)
	waitFor(t, func() bool { return got.Load() == "orphan" })
	waitFor(t, func() bool {
		p, err := ghost.XPending(ctx, stream, "trivita").Result()
		return err == nil && p.Count == 0
	})
}

// Concurrency > 1: một handler chậm không chặn các message sau nó (mặc định 1 thì chặn); mỗi message được ACK riêng; Stop chờ handler đang chạy.
func TestRedisConcurrencyDoesNotBlockBehindSlowHandler(t *testing.T) {
	for _, conc := range []int{1, 4} {
		t.Run(map[int]string{1: "lần lượt", 4: "song song"}[conc], func(t *testing.T) {
			mr := newRedis(t)
			b, err := messaging.NewRedis("redis://"+mr.Addr(), messaging.RedisOptions{Group: "g", Block: 20 * time.Millisecond, Concurrency: conc})
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			var fast, slowDone atomic.Int32
			b.Subscribe("ThingHappened", func(_ context.Context, env messaging.Envelope) error {
				var e thingHappened
				_ = env.Decode(&e)
				if e.Name == "slow" {
					<-release
					slowDone.Add(1)
					return nil
				}
				fast.Add(1)
				return nil
			})
			ctx := context.Background()
			if err := b.Start(ctx); err != nil {
				t.Fatal(err)
			}
			_ = b.Publish(ctx, newEvent("slow"))
			time.Sleep(100 * time.Millisecond)
			for i := 0; i < 3; i++ {
				_ = b.Publish(ctx, newEvent("fast"))
			}
			time.Sleep(500 * time.Millisecond)
			if conc == 1 && fast.Load() != 0 {
				t.Errorf("lần lượt: message sau handler chậm phải chờ, đã xử lý %d", fast.Load())
			}
			if conc > 1 && fast.Load() != 3 {
				t.Errorf("song song: 3 message nhanh phải xong trong lúc handler chậm còn chạy, xong %d", fast.Load())
			}
			stopped := make(chan struct{})
			go func() { _ = b.Stop(ctx); close(stopped) }()
			select {
			case <-stopped:
				t.Fatal("Stop phải chờ handler đang chạy")
			case <-time.After(300 * time.Millisecond):
			}
			close(release)
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("Stop không thoát sau khi handler xong")
			}
			if slowDone.Load() != 1 {
				t.Errorf("handler chậm chưa xong: %d", slowDone.Load())
			}
		})
	}
}
