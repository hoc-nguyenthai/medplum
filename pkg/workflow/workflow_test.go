package workflow_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/workflow"
)

func TestStepExecutorRetries(t *testing.T) {
	ex := workflow.InlineStepExecutor{RetryDelay: time.Millisecond}
	var n int
	err := ex.Run(context.Background(), "s", func(context.Context) error {
		n++
		if n < 3 {
			return errors.New("lỗi tạm")
		}
		return nil
	}, 2)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	boom := errors.New("boom")
	n = 0
	if err := ex.Run(context.Background(), "s", func(context.Context) error { n++; return boom }, 1); !errors.Is(err, boom) || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if err := (workflow.InlineStepExecutor{}).Run(context.Background(), "s", func(context.Context) error { return nil }, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := workflow.InlineStepExecutor{RetryDelay: time.Hour}
	if err := slow.Run(ctx, "s", func(context.Context) error { return boom }, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx hủy khi chờ retry: %v", err)
	}
}

func TestInlineRunsJobsWithConcurrencyLimit(t *testing.T) {
	var running, peak, done atomic.Int32
	var mu sync.Mutex
	o := workflow.NewInline(func(_ context.Context, _ string) error {
		cur := running.Add(1)
		mu.Lock()
		if cur > peak.Load() {
			peak.Store(cur)
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		done.Add(1)
		return nil
	}, 2)
	for i := range 6 {
		if err := o.Submit(context.Background(), string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done.Load() != 6 || peak.Load() > 2 {
		t.Fatalf("done=%d peak=%d", done.Load(), peak.Load())
	}
	if err := o.Submit(context.Background(), "x"); err == nil {
		t.Fatal("sau Shutdown không nhận job")
	}
}

func TestInlineSurvivesErrorsAndPanics(t *testing.T) {
	var ok atomic.Int32
	o := workflow.NewInline(func(_ context.Context, id string) error {
		switch id {
		case "err":
			return errors.New("lỗi")
		case "panic":
			panic("hoảng")
		}
		ok.Add(1)
		return nil
	}, 0)
	for _, id := range []string{"err", "panic", "fine"} {
		_ = o.Submit(context.Background(), id)
	}
	_ = o.Shutdown(context.Background())
	if ok.Load() != 1 {
		t.Fatal("job lỗi/panic không được làm hỏng job khác")
	}
}

func TestInlineShutdownCancelsAfterGrace(t *testing.T) {
	started := make(chan struct{})
	var cancelled atomic.Bool
	o := workflow.NewInline(func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		cancelled.Store(true)
		return ctx.Err()
	}, 1)
	_ = o.Submit(context.Background(), "slow")
	<-started
	_ = o.Submit(context.Background(), "queued") // đang chờ slot, phải thoát khi hủy
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := o.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if !cancelled.Load() {
		t.Fatal("job đang chạy phải bị hủy")
	}
}
