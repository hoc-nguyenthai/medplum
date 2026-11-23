package jti

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestMemoryUseOnce(t *testing.T) {
	m := NewMemory(10)
	now := time.Now()
	m.now = func() time.Time { return now }
	ctx := context.Background()
	if ok, err := m.UseOnce(ctx, "a", time.Minute); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := m.UseOnce(ctx, "a", time.Minute); ok {
		t.Error("lần hai phải bị từ chối")
	}
	if ok, _ := m.UseOnce(ctx, "b", time.Minute); !ok {
		t.Error("key khác dùng được")
	}
	now = now.Add(2 * time.Minute) // hết hạn: dùng lại được
	if ok, _ := m.UseOnce(ctx, "a", time.Minute); !ok {
		t.Error("key hết hạn phải dùng lại được")
	}
}

func TestMemoryIsSafeUnderConcurrencyAndFailsClosedWhenFull(t *testing.T) {
	m := NewMemory(1000)
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, _ := m.UseOnce(context.Background(), "same", time.Minute); got {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d lần thành công, muốn 1", ok.Load())
	}
	full := NewMemory(2)
	_, _ = full.UseOnce(context.Background(), "1", time.Hour)
	_, _ = full.UseOnce(context.Background(), "2", time.Hour)
	if ok, err := full.UseOnce(context.Background(), "3", time.Hour); ok || !errors.As(err, new(ErrFull)) {
		t.Errorf("đầy toàn mục còn hạn phải từ chối dùng mới (fail đóng): %v %v", ok, err)
	}
	// mục hết hạn được dọn để nhường chỗ
	exp := NewMemory(1)
	now := time.Now()
	exp.now = func() time.Time { return now }
	_, _ = exp.UseOnce(context.Background(), "x", time.Second)
	now = now.Add(time.Minute)
	if ok, err := exp.UseOnce(context.Background(), "y", time.Second); !ok || err != nil {
		t.Errorf("phải dọn mục hết hạn: %v %v", ok, err)
	}
}

func TestRedisUseOnceSharedAcrossClients(t *testing.T) {
	mr := miniredis.RunT(t)
	a, err := NewRedis("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, _ := NewRedis("redis://" + mr.Addr()) // một instance khác của auth
	defer b.Close()
	ctx := context.Background()
	if err := a.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.UseOnce(ctx, "c|j1", time.Minute); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := b.UseOnce(ctx, "c|j1", time.Minute); ok {
		t.Error("instance khác phải thấy jti đã dùng")
	}
	mr.FastForward(2 * time.Minute)
	if ok, _ := b.UseOnce(ctx, "c|j1", time.Minute); !ok {
		t.Error("hết hạn thì dùng lại được")
	}
	mr.Close()
	if ok, err := a.UseOnce(ctx, "c|j2", time.Minute); ok || err == nil {
		t.Errorf("Redis hỏng phải là lỗi (fail đóng): %v %v", ok, err)
	}
	if _, err := NewRedis("không-phải-url"); err == nil {
		t.Error("URL hỏng phải lỗi")
	}
}
