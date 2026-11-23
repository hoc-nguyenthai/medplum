// Package temporal là adapter workflow bền vững bằng Temporal (thay cho Prefect/Dagster của bản Python, vốn không có
// SDK Go). Mỗi job là một workflow chạy MỘT activity `run_job` gọi JobRunner của app; các bước bên trong job chạy bằng
// workflow.InlineStepExecutor (thứ tự bước vẫn do app quyết định), giống cách adapter Dagster của bản Python.
//
// Khác Inline: job còn nguyên khi API restart (Temporal giữ trạng thái), chạy được ở worker riêng, và tự chạy lại ở worker
// khác khi worker chết giữa chừng (heartbeat timeout). Vì vậy JobRunner phải idempotent (chạy lại từ đầu an toàn) và các
// tác dụng phụ (webhook) có thể xảy ra nhiều hơn một lần (at-least-once).
package temporal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"trivita-fhir/pkg/workflow"
)

// ActivityName là tên activity chạy job.
const ActivityName = "run_job"

// Config cấu hình orchestrator và worker.
type Config struct {
	Address   string // host:port của Temporal frontend (bắt buộc khi dùng New)
	Namespace string // mặc định "default"
	TLS       bool
	TaskQueue string // bắt buộc; API và worker phải dùng cùng task queue
	Name      string // tên workflow type; mặc định "workflow"

	ActivityTimeout   time.Duration // thời gian tối đa của MỘT lần chạy job; mặc định 30m
	HeartbeatTimeout  time.Duration // worker im lặng quá lâu thì job được giao lại; mặc định 30s
	MaxAttempts       int           // số lần chạy tối đa khi activity lỗi; mặc định 3 (1 = không retry)
	RetryInterval     time.Duration // chờ trước lần chạy lại đầu tiên (tăng gấp đôi mỗi lần); mặc định 5s
	WorkerConcurrency int           // số job đồng thời mỗi worker; mặc định 2
	WorkerStopTimeout time.Duration // chờ job đang chạy khi tắt worker; mặc định 20s
}

func (c *Config) defaults() error {
	if c.TaskQueue == "" {
		return errors.New("temporal: TaskQueue là bắt buộc")
	}
	if c.Namespace == "" {
		c.Namespace = "default"
	}
	if c.Name == "" {
		c.Name = "workflow"
	}
	if c.ActivityTimeout <= 0 {
		c.ActivityTimeout = 30 * time.Minute
	}
	if c.HeartbeatTimeout <= 0 {
		c.HeartbeatTimeout = 30 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = 5 * time.Second
	}
	if c.WorkerConcurrency <= 0 {
		c.WorkerConcurrency = 2
	}
	if c.WorkerStopTimeout <= 0 {
		c.WorkerStopTimeout = 20 * time.Second
	}
	return nil
}

// WorkflowID là ID workflow của một job: duy nhất theo (Name, jobID) nên Submit lặp lại không tạo job thứ hai.
func (c Config) WorkflowID(jobID string) string { return c.Name + "-" + jobID }

// Workflow trả về hàm workflow của Temporal: chạy activity run_job với timeout, heartbeat và retry theo cấu hình.
// Hàm chỉ phụ thuộc cấu hình bất biến nên deterministic. Xuất ra để test bằng testsuite.
func Workflow(cfg Config) func(ctx sdkworkflow.Context, jobID string) error {
	_ = cfg.defaults()
	return func(ctx sdkworkflow.Context, jobID string) error {
		ctx = sdkworkflow.WithActivityOptions(ctx, sdkworkflow.ActivityOptions{
			StartToCloseTimeout: cfg.ActivityTimeout,
			HeartbeatTimeout:    cfg.HeartbeatTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval: cfg.RetryInterval, BackoffCoefficient: 2, MaximumInterval: max(time.Minute, cfg.RetryInterval),
				MaximumAttempts: int32(cfg.MaxAttempts),
			},
		})
		return sdkworkflow.ExecuteActivity(ctx, ActivityName, jobID).Get(ctx, nil)
	}
}

// Activity trả về activity chạy job: gọi run và gửi heartbeat định kỳ trong lúc chạy để Temporal biết worker còn sống.
// Hủy ctx (worker tắt, hết hạn) được chuyển nguyên cho run.
func Activity(cfg Config, run workflow.JobRunner) func(ctx context.Context, jobID string) error {
	_ = cfg.defaults()
	return func(ctx context.Context, jobID string) error {
		beat := cfg.HeartbeatTimeout / 3
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(beat)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-t.C:
					activity.RecordHeartbeat(ctx)
				}
			}
		}()
		defer func() { close(done); wg.Wait() }()
		return run(ctx, jobID)
	}
}

// Starter là phần của client.Client mà Orchestrator cần (để test không cần server).
type Starter interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error)
}

// Orchestrator cài workflow.JobOrchestrator bằng Temporal. Submit chỉ khởi tạo workflow; việc chạy job do worker
// (cùng process qua StartWorker, hoặc process riêng) đảm nhiệm.
type Orchestrator struct {
	cfg     Config
	run     workflow.JobRunner
	client  client.Client
	starter Starter

	mu     sync.Mutex
	worker worker.Worker
	closed bool
}

var _ workflow.JobOrchestrator = (*Orchestrator)(nil)

// New nối tới Temporal (lazy: không lỗi khi server chưa sẵn sàng lúc khởi động; Ping/Submit sẽ báo lỗi).
func New(run workflow.JobRunner, cfg Config) (*Orchestrator, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	if cfg.Address == "" {
		return nil, errors.New("temporal: Address là bắt buộc")
	}
	opts := client.Options{HostPort: cfg.Address, Namespace: cfg.Namespace, Logger: slogAdapter{}}
	if cfg.TLS {
		opts.ConnectionOptions.TLS = tlsConfig()
	}
	c, err := client.NewLazyClient(opts)
	if err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}
	return NewWithClient(c, run, cfg)
}

// NewWithClient dùng client có sẵn (test, hoặc client đã gắn interceptor/tracing). Orchestrator sở hữu và sẽ đóng client.
func NewWithClient(c client.Client, run workflow.JobRunner, cfg Config) (*Orchestrator, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	return &Orchestrator{cfg: cfg, run: run, client: c, starter: c}, nil
}

// StartWorker đăng ký workflow + activity và bắt đầu nhận job trên task queue (chạy ở nền, không chặn).
func (o *Orchestrator) StartWorker() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("temporal: orchestrator đã tắt")
	}
	if o.worker != nil {
		return errors.New("temporal: worker đã chạy")
	}
	w := worker.New(o.client, o.cfg.TaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize: o.cfg.WorkerConcurrency,
		WorkerStopTimeout:                  o.cfg.WorkerStopTimeout,
	})
	w.RegisterWorkflowWithOptions(Workflow(o.cfg), sdkworkflow.RegisterOptions{Name: o.cfg.Name})
	w.RegisterActivityWithOptions(Activity(o.cfg, o.run), activity.RegisterOptions{Name: ActivityName})
	if err := w.Start(); err != nil {
		return fmt.Errorf("temporal: không khởi động được worker: %w", err)
	}
	o.worker = w
	return nil
}

// Submit khởi tạo workflow cho job; gọi lại cho cùng jobID là no-op (kể cả khi workflow đã hoàn tất).
func (o *Orchestrator) Submit(ctx context.Context, jobID string) error {
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return errors.New("temporal: orchestrator đã tắt")
	}
	_, err := o.starter.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       o.cfg.WorkflowID(jobID),
		TaskQueue:                o.cfg.TaskQueue,
		WorkflowIDReusePolicy:    enumReuseReject,
		WorkflowIDConflictPolicy: enumConflictUseExisting,
	}, o.cfg.Name, jobID)
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if err != nil && !errors.As(err, &started) {
		return fmt.Errorf("temporal: không gửi được job: %w", err)
	}
	return nil
}

// Ping kiểm tra kết nối tới Temporal (cho readiness).
func (o *Orchestrator) Ping(ctx context.Context) error {
	_, err := o.client.CheckHealth(ctx, &client.CheckHealthRequest{})
	return err
}

// Shutdown dừng worker (chờ job đang chạy tối đa WorkerStopTimeout; job dở dang sẽ được Temporal giao lại cho worker
// khác) rồi đóng client.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	w := o.worker
	o.mu.Unlock()
	if w != nil {
		stopped := make(chan struct{})
		go func() { w.Stop(); close(stopped) }()
		select {
		case <-stopped:
		case <-ctx.Done():
			slog.Warn("tắt worker Temporal quá hạn chờ")
		}
	}
	o.client.Close()
	return nil
}
