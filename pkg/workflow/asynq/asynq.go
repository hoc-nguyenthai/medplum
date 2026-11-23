// Package asynq là adapter job nền bền vững bằng [asynq] (hàng đợi trên Redis): lựa chọn nhẹ hơn Temporal khi đã có Redis
// và không cần workflow nhiều bước. Mỗi job là một task `Config.TaskType` mang `job_id` (và `traceparent` để trace của job
// nối tiếp trace của request đã xếp nó), chạy JobRunner của app; các bước bên trong job do app quyết định (chạy bằng
// workflow.InlineStepExecutor).
//
// Đảm bảo: task nằm trong Redis nên job còn nguyên khi API restart và chạy được ở worker riêng; worker chết hoặc tắt
// giữa chừng thì task được giao lại (at-least-once, **chạy lại từ đầu**) nên JobRunner phải idempotent và tác dụng phụ
// (webhook) có thể xảy ra nhiều hơn một lần. Task lỗi quá MaxRetry sang kho lưu trữ (archive) của asynq để kiểm tra bằng
// `asynq` CLI / asynqmon, không bị mất.
//
// [asynq]: https://github.com/hibiken/asynq
package asynq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	observability "trivita-fhir/pkg/observability"

	"trivita-fhir/pkg/workflow"
)

// Config cấu hình orchestrator và worker.
type Config struct {
	RedisURL string // redis://host:6379/0 (bắt buộc khi dùng New)
	Queue    string // tên hàng đợi; API và worker phải trùng. Mặc định "default"
	TaskType string // loại task; mặc định "workflow:run_job"

	MaxRetry      int           // số lần chạy lại tối đa sau lần đầu khi job lỗi; mặc định 3, số âm = không retry
	Timeout       time.Duration // thời gian tối đa của MỘT lần chạy job; mặc định 30m
	Retention     time.Duration // giữ task đã xong để Submit lặp lại là no-op; mặc định 24h
	RetryInterval time.Duration // chờ trước lần chạy lại đầu tiên (gấp đôi mỗi lần); mặc định 5s

	WorkerConcurrency int           // số job đồng thời mỗi worker; mặc định 2
	WorkerStopTimeout time.Duration // chờ job đang chạy khi tắt worker, quá hạn thì hủy ctx và giao lại; mặc định 20s
	PollInterval      time.Duration // chu kỳ hỏi Redis khi hàng đợi rỗng; mặc định 1s
}

func (c *Config) defaults() {
	if c.Queue == "" {
		c.Queue = "default"
	}
	if c.TaskType == "" {
		c.TaskType = "workflow:run_job"
	}
	if c.MaxRetry == 0 {
		c.MaxRetry = 3
	} else if c.MaxRetry < 0 {
		c.MaxRetry = 0 // âm = không retry
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Minute
	}
	if c.Retention <= 0 {
		c.Retention = 24 * time.Hour
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
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
}

// TaskID là ID task của một job: duy nhất theo (TaskType, jobID) nên Submit lặp lại không tạo job thứ hai.
func (c Config) TaskID(jobID string) string { return c.TaskType + ":" + jobID }

// payload là nội dung task. Traceparent nối trace của request vào trace của job.
type payload struct {
	JobID       string `json:"job_id"`
	Traceparent string `json:"traceparent,omitempty"`
}

// Orchestrator cài workflow.JobOrchestrator bằng asynq. Submit chỉ xếp task vào Redis; việc chạy do worker (cùng
// process qua StartWorker, hoặc process riêng) đảm nhiệm.
type Orchestrator struct {
	cfg    Config
	run    workflow.JobRunner
	rdb    redis.UniversalClient
	client *asynq.Client

	mu     sync.Mutex
	server *asynq.Server
	closed bool
}

var _ workflow.JobOrchestrator = (*Orchestrator)(nil)

// New nối tới Redis (lazy: không lỗi khi Redis chưa sẵn sàng lúc khởi động; Ping/Submit sẽ báo lỗi).
func New(run workflow.JobRunner, cfg Config) (*Orchestrator, error) {
	if cfg.RedisURL == "" {
		return nil, errors.New("asynq: RedisURL là bắt buộc")
	}
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, errors.New("asynq: RedisURL không hợp lệ")
	}
	return NewWithClient(redis.NewClient(opt), run, cfg), nil
}

// NewWithClient dùng client Redis có sẵn (vd đã gắn redisotel). Orchestrator sở hữu và sẽ đóng client.
func NewWithClient(rdb redis.UniversalClient, run workflow.JobRunner, cfg Config) *Orchestrator {
	cfg.defaults()
	return &Orchestrator{cfg: cfg, run: run, rdb: rdb, client: asynq.NewClientFromRedisClient(rdb)}
}

// StartWorker bắt đầu nhận task trên hàng đợi (chạy ở nền, không chặn).
func (o *Orchestrator) StartWorker() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("asynq: orchestrator đã tắt")
	}
	if o.server != nil {
		return errors.New("asynq: worker đã chạy")
	}
	srv := asynq.NewServerFromRedisClient(o.rdb, asynq.Config{
		Concurrency:       o.cfg.WorkerConcurrency,
		Queues:            map[string]int{o.cfg.Queue: 1},
		ShutdownTimeout:   o.cfg.WorkerStopTimeout,
		TaskCheckInterval: o.cfg.PollInterval,
		RetryDelayFunc: func(n int, _ error, _ *asynq.Task) time.Duration {
			return o.cfg.RetryInterval << max(n-1, 0) // gấp đôi mỗi lần
		},
		Logger: logger{},
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(o.cfg.TaskType, o.handle)
	if err := srv.Start(mux); err != nil {
		return fmt.Errorf("asynq: không khởi động được worker: %w", err)
	}
	o.server = srv
	return nil
}

// handle chạy một task: lỗi (kể cả hủy do worker tắt) được trả lại để asynq retry/giao lại. Payload hỏng không bao giờ
// chạy được nên bỏ qua retry.
func (o *Orchestrator) handle(ctx context.Context, t *asynq.Task) error {
	var p payload
	if err := json.Unmarshal(t.Payload(), &p); err != nil || p.JobID == "" {
		return fmt.Errorf("payload task không hợp lệ: %w", asynq.SkipRetry)
	}
	id, _ := asynq.GetTaskID(ctx)
	retried, _ := asynq.GetRetryCount(ctx)
	return observability.RunJob(ctx, observability.Job{
		System: "asynq", Name: t.Type(), ID: id, Try: retried + 1, Traceparent: p.Traceparent,
	}, func(ctx context.Context) error { return o.run(ctx, p.JobID) })
}

// Submit xếp job vào hàng đợi; gọi lại cho cùng jobID là no-op (kể cả khi job đã xong, trong thời gian Retention).
func (o *Orchestrator) Submit(ctx context.Context, jobID string) error {
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return errors.New("asynq: orchestrator đã tắt")
	}
	err := observability.Enqueue(ctx, "asynq", o.cfg.Queue, o.cfg.TaskType,
		func(ctx context.Context, traceparent string) (string, error) {
			raw, err := json.Marshal(payload{JobID: jobID, Traceparent: traceparent})
			if err != nil {
				return "", err
			}
			info, err := o.client.EnqueueContext(ctx, asynq.NewTask(o.cfg.TaskType, raw),
				asynq.Queue(o.cfg.Queue), asynq.TaskID(o.cfg.TaskID(jobID)), asynq.MaxRetry(o.cfg.MaxRetry),
				asynq.Timeout(o.cfg.Timeout), asynq.Retention(o.cfg.Retention))
			if err != nil {
				return "", err
			}
			return info.ID, nil
		})
	if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
		return fmt.Errorf("asynq: không xếp được job: %w", err)
	}
	return nil
}

// QueueDepth là số job hiện có trong hàng đợi của orchestrator: đang chờ, đang chạy, hẹn giờ và chờ chạy lại. Job đã
// xong hoặc đã archive không tính. Dùng để giới hạn số job xếp hàng (back-pressure) phía scheduler.
func (o *Orchestrator) QueueDepth(ctx context.Context) (int, error) {
	insp := asynq.NewInspectorFromRedisClient(o.rdb)
	info, err := insp.GetQueueInfo(o.cfg.Queue)
	if err != nil {
		// GetQueueInfo (v0.26) trả lỗi nội bộ "NOT_FOUND: queue ... does not exist", không bọc ErrQueueNotFound.
		if errors.Is(err, asynq.ErrQueueNotFound) || strings.Contains(err.Error(), "NOT_FOUND") {
			return 0, nil // hàng đợi chưa có task nào
		}
		return 0, fmt.Errorf("asynq: không đọc được độ sâu hàng đợi: %w", err)
	}
	return info.Pending + info.Active + info.Scheduled + info.Retry + info.Aggregating, nil
}

// Ping kiểm tra kết nối tới Redis (cho readiness).
func (o *Orchestrator) Ping(ctx context.Context) error { return o.rdb.Ping(ctx).Err() }

// Shutdown dừng worker (chờ job đang chạy tối đa WorkerStopTimeout; job dở dang được hủy ctx và trả về hàng đợi để worker
// khác nhận) rồi đóng kết nối Redis.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	srv := o.server
	o.mu.Unlock()
	if srv != nil {
		stopped := make(chan struct{})
		go func() { srv.Shutdown(); close(stopped) }()
		select {
		case <-stopped:
		case <-ctx.Done():
			slog.Warn("tắt worker asynq quá hạn chờ")
		}
	}
	// asynq.Client dựng từ client Redis dùng chung thì Close() chỉ báo lỗi; kết nối do ta đóng.
	return o.rdb.Close()
}

// logger đưa log của asynq vào slog (JSON/OTLP của service).
type logger struct{}

func (logger) Debug(args ...any) { slog.Debug(fmt.Sprint(args...), "logger", "asynq") }
func (logger) Info(args ...any)  { slog.Info(fmt.Sprint(args...), "logger", "asynq") }
func (logger) Warn(args ...any)  { slog.Warn(fmt.Sprint(args...), "logger", "asynq") }
func (logger) Error(args ...any) { slog.Error(fmt.Sprint(args...), "logger", "asynq") }
func (logger) Fatal(args ...any) { slog.Error(fmt.Sprint(args...), "logger", "asynq", "fatal", true) }
