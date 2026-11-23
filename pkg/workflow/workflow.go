// Package workflow định nghĩa port JobOrchestrator/StepExecutor và adapter inline (goroutine trong process).
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// JobRunner chạy một job theo ID; app truyền vào (vd `pipeline.Run`).
type JobRunner func(ctx context.Context, jobID string) error

// JobOrchestrator nhận job và chạy ở nền.
type JobOrchestrator interface {
	Submit(ctx context.Context, jobID string) error
	// Shutdown chờ job đang chạy tới khi ctx hết hạn, sau đó hủy phần còn lại.
	Shutdown(ctx context.Context) error
}

// StepExecutor chạy một bước của pipeline (có retry); adapter khác có thể bọc mỗi bước thành task theo dõi riêng.
type StepExecutor interface {
	Run(ctx context.Context, name string, fn func(context.Context) error, retries int) error
}

// InlineStepExecutor retry tại chỗ, chờ RetryDelay giữa các lần.
type InlineStepExecutor struct{ RetryDelay time.Duration }

func (e InlineStepExecutor) Run(ctx context.Context, name string, fn func(context.Context) error, retries int) error {
	delay := e.RetryDelay
	if delay <= 0 {
		delay = 2 * time.Second
	}
	var err error
	for attempt := 0; attempt <= retries; attempt++ {
		if err = fn(ctx); err == nil {
			return nil
		}
		if attempt == retries {
			break
		}
		slog.WarnContext(ctx, "step_retry", "step", name, "attempt", attempt+1, "error", err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// Inline chạy job ở nền trong process, giới hạn số job đồng thời. Mất job đang chạy khi process restart.
type Inline struct {
	run JobRunner
	sem chan struct{}

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewInline tạo orchestrator; concurrency <= 0 nghĩa là 2.
func NewInline(run JobRunner, concurrency int) *Inline {
	if concurrency <= 0 {
		concurrency = 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Inline{run: run, sem: make(chan struct{}, concurrency), ctx: ctx, cancel: cancel}
}

// Submit đưa job vào hàng đợi và trả về ngay.
func (o *Inline) Submit(_ context.Context, jobID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return fmt.Errorf("workflow: orchestrator đã tắt")
	}
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		select {
		case o.sem <- struct{}{}:
			defer func() { <-o.sem }()
		case <-o.ctx.Done():
			return
		}
		defer func() {
			if r := recover(); r != nil {
				slog.Error("job_panicked", "job_id", jobID, "panic", r)
			}
		}()
		if err := o.run(o.ctx, jobID); err != nil {
			// job tự ghi lỗi của nó; ở đây chỉ log thêm
			slog.Error("job_crashed", "job_id", jobID, "error", err)
		}
	}()
	return nil
}

// Shutdown ngừng nhận job, chờ job đang chạy tới khi ctx hết hạn rồi hủy phần còn lại.
func (o *Inline) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
	done := make(chan struct{})
	go func() { o.wg.Wait(); close(done) }()
	select {
	case <-done:
		o.cancel()
		return nil
	case <-ctx.Done():
		o.cancel()
		<-done
		slog.Warn("jobs_cancelled_on_shutdown")
		return ctx.Err()
	}
}
