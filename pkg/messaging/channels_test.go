package messaging_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/messaging"
)

// backend dựng hai "instance" (a, b) chia sẻ cùng một nơi: với bộ nhớ là cùng một đối tượng, với Redis là hai Broker trên cùng máy chủ.
type backend struct {
	name string
	make func(t *testing.T) (a, b interface {
		messaging.Channels
		messaging.Presence
	})
}

func backends() []backend {
	return []backend{
		{"memory", func(t *testing.T) (interface {
			messaging.Channels
			messaging.Presence
		}, interface {
			messaging.Channels
			messaging.Presence
		}) {
			m := messaging.NewInMemory()
			return m, m
		}},
		{"redis", func(t *testing.T) (interface {
			messaging.Channels
			messaging.Presence
		}, interface {
			messaging.Channels
			messaging.Presence
		}) {
			r := newRedis(t)
			mk := func() *messaging.Redis {
				b, err := messaging.NewRedis("redis://"+r.Addr()+"/0", messaging.RedisOptions{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = b.Stop(context.Background()) })
				return b
			}
			return mk(), mk()
		}},
	}
}

func eachBackend(t *testing.T, f func(t *testing.T, a, b interface {
	messaging.Channels
	messaging.Presence
})) {
	for _, be := range backends() {
		t.Run(be.name, func(t *testing.T) {
			a, b := be.make(t)
			f(t, a, b)
		})
	}
}

type collector struct {
	mu  sync.Mutex
	got []string
}

func (c *collector) handler() messaging.ChannelHandler {
	return func(p []byte) { c.mu.Lock(); c.got = append(c.got, string(p)); c.mu.Unlock() }
}
func (c *collector) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.got...)
}
func (c *collector) waitLen(t *testing.T, n int) {
	t.Helper()
	waitFor(t, func() bool { return len(c.list()) >= n })
}

func TestChannelsDeliverToEverySubscriberOnEveryInstanceAndIsolateByName(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		var onA, onB, other collector
		for _, s := range []struct {
			who  messaging.Channels
			ch   string
			into *collector
		}{{a, "c1", &onA}, {b, "c1", &onB}, {a, "c2", &other}} {
			cancel, err := s.who.SubscribeChannel(ctx, s.ch, s.into.handler())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cancel)
		}
		if err := b.PublishChannel(ctx, "c1", []byte("hello")); err != nil {
			t.Fatal(err)
		}
		onA.waitLen(t, 1)
		onB.waitLen(t, 1)
		time.Sleep(50 * time.Millisecond)
		if len(onA.list()) != 1 || len(onB.list()) != 1 || len(other.list()) != 0 {
			t.Errorf("a=%v b=%v other=%v", onA.list(), onB.list(), other.list())
		}
		// kênh không có người nghe: không lỗi
		if err := a.PublishChannel(ctx, "nobody", []byte("x")); err != nil {
			t.Errorf("publish vào kênh trống: %v", err)
		}
	})
}

// Subscribe trả về nghĩa là đã có hiệu lực: phát ngay sau đó không được lọt. Đây là điều kiện của "đăng ký kênh trước, rồi mới ghi sổ hiện diện".
func TestSubscribeChannelIsEffectiveWhenItReturns(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		for i := range 30 {
			name := fmt.Sprintf("race-%d", i)
			var c collector
			cancel, err := a.SubscribeChannel(ctx, name, c.handler())
			if err != nil {
				t.Fatal(err)
			}
			_ = b.PublishChannel(ctx, name, []byte("first"))
			c.waitLen(t, 1)
			cancel()
		}
	})
}

func TestChannelPreservesOrderWithinASubscription(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		var c collector
		cancel, err := a.SubscribeChannel(ctx, "ord", c.handler())
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
		for i := range 100 {
			_ = b.PublishChannel(ctx, "ord", []byte(fmt.Sprint(i)))
		}
		c.waitLen(t, 100)
		for i, g := range c.list() {
			if g != fmt.Sprint(i) {
				t.Fatalf("thứ tự sai ở %d: %s", i, g)
			}
		}
	})
}

func TestCancelStopsDeliveryAndIsIdempotent(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		var keep, drop collector
		cancelKeep, _ := a.SubscribeChannel(ctx, "k", keep.handler())
		defer cancelKeep()
		cancelDrop, err := a.SubscribeChannel(ctx, "k", drop.handler())
		if err != nil {
			t.Fatal(err)
		}
		_ = b.PublishChannel(ctx, "k", []byte("1"))
		keep.waitLen(t, 1)
		drop.waitLen(t, 1)
		cancelDrop()
		cancelDrop() // gọi lại an toàn
		_ = b.PublishChannel(ctx, "k", []byte("2"))
		keep.waitLen(t, 2)
		time.Sleep(50 * time.Millisecond)
		if len(drop.list()) != 1 {
			t.Errorf("đã hủy mà vẫn nhận: %v", drop.list())
		}
	})
}

// cancel chờ handler đang chạy xong; sau khi cancel trả về không còn lần gọi nào.
func TestCancelWaitsForRunningHandler(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		started, release := make(chan struct{}), make(chan struct{})
		var finished, after atomic.Bool
		cancel, err := a.SubscribeChannel(ctx, "slow", func([]byte) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			if finished.Load() {
				after.Store(true)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = b.PublishChannel(ctx, "slow", []byte("1"))
		<-started
		returned := make(chan struct{})
		go func() { cancel(); close(returned) }()
		select {
		case <-returned:
			t.Fatal("cancel phải chờ handler đang chạy")
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		select {
		case <-returned:
		case <-time.After(3 * time.Second):
			t.Fatal("cancel không trả về sau khi handler xong")
		}
		finished.Store(true)
		_ = b.PublishChannel(ctx, "slow", []byte("2"))
		time.Sleep(100 * time.Millisecond)
		if after.Load() {
			t.Error("handler còn được gọi sau khi cancel trả về")
		}
	})
}

func TestChannelHandlerPanicDoesNotKillTheSubscription(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		var c collector
		h := c.handler()
		cancel, err := a.SubscribeChannel(ctx, "p", func(p []byte) {
			if string(p) == "boom" {
				panic("lỗi cố ý")
			}
			h(p)
		})
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
		_ = b.PublishChannel(ctx, "p", []byte("boom"))
		_ = b.PublishChannel(ctx, "p", []byte("ok"))
		c.waitLen(t, 1)
		if got := c.list(); got[0] != "ok" {
			t.Errorf("got %v", got)
		}
	})
}

func TestPresenceMarkUnmarkAndExpiry(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		present := func(who messaging.Presence, key string) bool {
			t.Helper()
			ok, err := who.Present(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		if present(a, "k") {
			t.Fatal("chưa ai ghi mà đã present")
		}
		if err := a.Mark(ctx, "k", "m1", time.Minute); err != nil {
			t.Fatal(err)
		}
		if !present(b, "k") || present(b, "other") { // thấy được từ instance khác; khóa khác không lẫn
			t.Fatal("present sai")
		}
		if err := b.Mark(ctx, "k", "m2", time.Minute); err != nil {
			t.Fatal(err)
		}
		_ = a.Unmark(ctx, "k", "m1")
		if !present(a, "k") {
			t.Fatal("còn m2 phải present")
		}
		_ = a.Unmark(ctx, "k", "m2")
		_ = a.Unmark(ctx, "k", "m2") // gỡ lại an toàn
		if present(b, "k") {
			t.Fatal("đã gỡ hết")
		}

		// hết hạn
		_ = a.Mark(ctx, "exp", "m", 100*time.Millisecond)
		if !present(b, "exp") {
			t.Fatal("trước hạn phải present")
		}
		time.Sleep(250 * time.Millisecond)
		if present(b, "exp") {
			t.Fatal("quá hạn phải hết present")
		}
		// gia hạn kéo dài mục
		_ = a.Mark(ctx, "ext", "m", 300*time.Millisecond)
		time.Sleep(150 * time.Millisecond)
		_ = a.Mark(ctx, "ext", "m", 300*time.Millisecond)
		time.Sleep(200 * time.Millisecond)
		if !present(b, "ext") {
			t.Fatal("đã gia hạn thì phải còn")
		}
	})
}

// Một member TTL ngắn ghi sau không được cắt ngắn vòng đời của member TTL dài đã có trên cùng khóa.
func TestPresenceShortTTLDoesNotShortenALongerMember(t *testing.T) {
	eachBackend(t, func(t *testing.T, a, b interface {
		messaging.Channels
		messaging.Presence
	}) {
		ctx := context.Background()
		_ = a.Mark(ctx, "k", "long", 5*time.Second)
		_ = b.Mark(ctx, "k", "short", 100*time.Millisecond)
		time.Sleep(300 * time.Millisecond)
		if ok, err := a.Present(ctx, "k"); err != nil || !ok {
			t.Fatalf("member dài phải còn: %v %v", ok, err)
		}
	})
}

// Hàng đợi bộ nhớ không chặn bên phát khi người nghe chậm: thông điệp bị bỏ (không bền), không treo.
func TestInMemoryChannelNeverBlocksThePublisher(t *testing.T) {
	m := messaging.NewInMemory()
	release := make(chan struct{})
	cancel, err := m.SubscribeChannel(context.Background(), "s", func([]byte) { <-release })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(release); cancel() }()
	done := make(chan struct{})
	go func() {
		for range 2000 {
			_ = m.PublishChannel(context.Background(), "s", []byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bên phát bị chặn bởi người nghe chậm")
	}
}

// Stop của Redis hủy mọi đăng ký kênh: cancel gọi sau đó không treo, handler không còn được gọi.
func TestRedisStopClosesChannelSubscriptions(t *testing.T) {
	r := newRedis(t)
	b, err := messaging.NewRedis("redis://"+r.Addr()+"/0", messaging.RedisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var c collector
	cancel, err := b.SubscribeChannel(context.Background(), "x", c.handler())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cancel(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel treo sau Stop")
	}
}

// Khóa Redis chỉ hết hạn khi member sống lâu nhất hết hạn: member TTL ngắn ghi sau không được kéo PEXPIRE của khóa xuống.
// (miniredis chỉ hết hạn khóa khi FastForward, nên ca này phải tiến đồng hồ giả; Redis thật thì chờ thật.)
func TestRedisPresenceKeyExpiryFollowsTheLongestMember(t *testing.T) {
	r := newRedis(t)
	b, err := messaging.NewRedis("redis://"+r.Addr()+"/0", messaging.RedisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	ctx := context.Background()
	_ = b.Mark(ctx, "k", "long", 5*time.Second)
	_ = b.Mark(ctx, "k", "short", 100*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	if r.mr != nil {
		r.mr.FastForward(300 * time.Millisecond)
	}
	if ok, err := b.Present(ctx, "k"); err != nil || !ok {
		t.Fatalf("khóa phải còn vì member dài còn hạn: %v %v", ok, err)
	}
}
