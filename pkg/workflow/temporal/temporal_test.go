package temporal_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/testsuite"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"trivita-fhir/pkg/workflow/temporal"
)

func testCfg() temporal.Config {
	return temporal.Config{TaskQueue: "q", Name: "ocr_pipeline", RetryInterval: time.Second, HeartbeatTimeout: 30 * time.Millisecond}
}

// newEnv dựng môi trường workflow của SDK (không cần server, đồng hồ giả nên retry không phải chờ thật).
func newEnv(cfg temporal.Config, run func(context.Context, string) error) *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(temporal.Workflow(cfg), sdkworkflow.RegisterOptions{Name: cfg.Name})
	env.RegisterActivityWithOptions(temporal.Activity(cfg, run), activity.RegisterOptions{Name: temporal.ActivityName})
	return env
}

func TestWorkflowRunsTheJobRunner(t *testing.T) {
	var got []string
	env := newEnv(testCfg(), func(_ context.Context, id string) error { got = append(got, id); return nil })
	env.ExecuteWorkflow("ocr_pipeline", "job-1")
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("completed=%v err=%v", env.IsWorkflowCompleted(), env.GetWorkflowError())
	}
	if len(got) != 1 || got[0] != "job-1" {
		t.Fatalf("got = %v", got)
	}
}

func TestWorkflowRetriesFailedAttemptsThenSucceeds(t *testing.T) {
	var n atomic.Int32
	env := newEnv(testCfg(), func(context.Context, string) error {
		if n.Add(1) < 3 {
			return errors.New("lỗi tạm")
		}
		return nil
	})
	env.ExecuteWorkflow("ocr_pipeline", "j")
	if env.GetWorkflowError() != nil || n.Load() != 3 {
		t.Fatalf("err=%v attempts=%d", env.GetWorkflowError(), n.Load())
	}
}

func TestWorkflowFailsAfterMaxAttempts(t *testing.T) {
	cfg := testCfg()
	cfg.MaxAttempts = 2
	var n atomic.Int32
	env := newEnv(cfg, func(context.Context, string) error { n.Add(1); return errors.New("hỏng hẳn") })
	env.ExecuteWorkflow("ocr_pipeline", "j")
	if env.GetWorkflowError() == nil || n.Load() != 2 {
		t.Fatalf("err=%v attempts=%d", env.GetWorkflowError(), n.Load())
	}
	cfg.MaxAttempts = 1 // 1 = không retry
	n.Store(0)
	env = newEnv(cfg, func(context.Context, string) error { n.Add(1); return errors.New("x") })
	env.ExecuteWorkflow("ocr_pipeline", "j")
	if env.GetWorkflowError() == nil || n.Load() != 1 {
		t.Fatalf("1 lần: err=%v attempts=%d", env.GetWorkflowError(), n.Load())
	}
}

func TestActivitySendsHeartbeatsWhileJobRuns(t *testing.T) {
	env := newEnv(testCfg(), func(context.Context, string) error { time.Sleep(150 * time.Millisecond); return nil })
	var beats atomic.Int32
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) { beats.Add(1) })
	env.ExecuteWorkflow("ocr_pipeline", "j")
	if env.GetWorkflowError() != nil {
		t.Fatal(env.GetWorkflowError())
	}
	if beats.Load() < 3 {
		t.Fatalf("heartbeat = %d, cần gửi đều đặn (mỗi HeartbeatTimeout/3)", beats.Load())
	}
}

func TestConfigValidationAndDefaults(t *testing.T) {
	if _, err := temporal.New(func(context.Context, string) error { return nil }, temporal.Config{Address: "x:1"}); err == nil {
		t.Fatal("thiếu TaskQueue phải lỗi")
	}
	if _, err := temporal.New(func(context.Context, string) error { return nil }, temporal.Config{TaskQueue: "q"}); err == nil {
		t.Fatal("thiếu Address phải lỗi")
	}
	if _, err := temporal.NewWithClient(&mocks.Client{}, nil, temporal.Config{}); err == nil {
		t.Fatal("thiếu TaskQueue phải lỗi")
	}
	if id := (temporal.Config{Name: "ocr"}).WorkflowID("abc"); id != "ocr-abc" {
		t.Fatalf("id = %q", id)
	}
	// New nối lazy: không lỗi khi server chưa chạy; Ping/Submit mới báo lỗi
	o, err := temporal.New(func(context.Context, string) error { return nil }, temporal.Config{Address: "127.0.0.1:1", TaskQueue: "q", TLS: true})
	if err != nil {
		t.Fatalf("kết nối lazy không được lỗi lúc khởi tạo: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := o.Ping(ctx); err == nil {
		t.Fatal("Ping tới server không tồn tại phải lỗi")
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type fakeRun struct{ client.WorkflowRun }

func TestSubmitStartsIdempotentWorkflowAndMapsErrors(t *testing.T) {
	c := &mocks.Client{}
	var opts client.StartWorkflowOptions
	c.On("ExecuteWorkflow", mock.Anything, mock.Anything, "ocr_pipeline", mock.Anything).
		Run(func(a mock.Arguments) { opts = a.Get(1).(client.StartWorkflowOptions) }).
		Return(fakeRun{}, nil).Once()
	o, err := temporal.NewWithClient(c, nil, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Submit(context.Background(), "job-9"); err != nil {
		t.Fatal(err)
	}
	if opts.ID != "ocr_pipeline-job-9" || opts.TaskQueue != "q" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.WorkflowIDReusePolicy.String() != "RejectDuplicate" || opts.WorkflowIDConflictPolicy.String() != "UseExisting" {
		t.Fatalf("chính sách ID sai: %v %v", opts.WorkflowIDReusePolicy, opts.WorkflowIDConflictPolicy)
	}

	// đã tồn tại (đã chạy xong hoặc đang chạy) => thành công, không tạo job thứ hai
	c.On("ExecuteWorkflow", mock.Anything, mock.Anything, "ocr_pipeline", mock.Anything).
		Return(nil, &serviceerror.WorkflowExecutionAlreadyStarted{}).Once()
	if err := o.Submit(context.Background(), "job-9"); err != nil {
		t.Fatalf("AlreadyStarted phải là thành công: %v", err)
	}
	// lỗi thật => báo lỗi (để use case không báo job đã xếp hàng khi chưa)
	c.On("ExecuteWorkflow", mock.Anything, mock.Anything, "ocr_pipeline", mock.Anything).
		Return(nil, errors.New("server không phản hồi")).Once()
	if err := o.Submit(context.Background(), "job-10"); err == nil {
		t.Fatal("lỗi gửi phải được trả lên")
	}
	c.On("CheckHealth", mock.Anything, mock.Anything).Return(&client.CheckHealthResponse{}, nil).Once()
	if err := o.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.On("Close").Return().Once()
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.Shutdown(context.Background()); err != nil { // lần hai là no-op
		t.Fatal(err)
	}
	if err := o.Submit(context.Background(), "job-11"); err == nil {
		t.Fatal("sau Shutdown không nhận job")
	}
	if err := o.StartWorker(); err == nil {
		t.Fatal("sau Shutdown không khởi động worker")
	}
	c.AssertExpectations(t)
}

// ---- tích hợp với Temporal dev server thật (bỏ qua nếu không tải/khởi động được) ----

var devServer = sync.OnceValues(func() (*testsuite.DevServer, error) {
	return testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
})

func TestMain(m *testing.M) {
	code := m.Run()
	if s, err := devServer(); err == nil {
		_ = s.Stop()
	}
	exit(code)
}

func server(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("bỏ qua test tích hợp Temporal với -short")
	}
	s, err := devServer()
	if err != nil {
		t.Skipf("không khởi động được Temporal dev server: %v", err)
	}
	return s.FrontendHostPort()
}

type recorder struct {
	mu    sync.Mutex
	calls map[string]int
	fn    func(ctx context.Context, id string, attempt int) error
}

func newRecorder(fn func(ctx context.Context, id string, attempt int) error) *recorder {
	return &recorder{calls: map[string]int{}, fn: fn}
}

func (r *recorder) run(ctx context.Context, id string) error {
	r.mu.Lock()
	r.calls[id]++
	n := r.calls[id]
	r.mu.Unlock()
	if r.fn != nil {
		return r.fn(ctx, id, n)
	}
	return nil
}

func (r *recorder) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[id]
}

func cfgFor(addr, queue string) temporal.Config {
	return temporal.Config{
		Address: addr, TaskQueue: queue, Name: "ocr_pipeline-" + queue, RetryInterval: 100 * time.Millisecond,
		HeartbeatTimeout: 600 * time.Millisecond, WorkerStopTimeout: 100 * time.Millisecond, MaxAttempts: 3,
	}
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestIntegrationSubmitRunsOnceAndIsIdempotent(t *testing.T) {
	addr := server(t)
	rec := newRecorder(nil)
	o, err := temporal.New(rec.run, cfgFor(addr, t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Shutdown(context.Background())
	if err := o.StartWorker(); err != nil {
		t.Fatal(err)
	}
	if err := o.StartWorker(); err == nil {
		t.Fatal("khởi động worker lần hai phải lỗi")
	}
	if err := o.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 3 { // Submit lặp lại cho cùng job (API retry) không được chạy job nhiều lần
		if err := o.Submit(context.Background(), "job-1"); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, func() bool { return rec.count("job-1") >= 1 }, "job phải được chạy")
	time.Sleep(300 * time.Millisecond)
	if n := rec.count("job-1"); n != 1 {
		t.Fatalf("job chạy %d lần, want 1", n)
	}
	// job đã xong rồi Submit lại vẫn là no-op
	if err := o.Submit(context.Background(), "job-1"); err != nil {
		t.Fatalf("Submit job đã hoàn tất: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := rec.count("job-1"); n != 1 {
		t.Fatalf("job hoàn tất bị chạy lại: %d", n)
	}
}

func TestIntegrationJobSurvivesUntilAWorkerAppears(t *testing.T) {
	addr := server(t)
	cfg := cfgFor(addr, t.Name())
	rec := newRecorder(nil)
	api, _ := temporal.New(rec.run, cfg) // chỉ gửi job, không chạy worker (như API-only pod)
	defer api.Shutdown(context.Background())
	if err := api.Submit(context.Background(), "queued-job"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if rec.count("queued-job") != 0 {
		t.Fatal("chưa có worker thì job không được chạy")
	}
	w, _ := temporal.New(rec.run, cfg) // worker riêng xuất hiện sau
	defer w.Shutdown(context.Background())
	if err := w.StartWorker(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return rec.count("queued-job") == 1 }, "job đã xếp hàng phải được worker mới nhận")
}

func TestIntegrationFailedAttemptIsRetried(t *testing.T) {
	addr := server(t)
	rec := newRecorder(func(_ context.Context, _ string, attempt int) error {
		if attempt == 1 {
			return errors.New("lỗi tạm ở lần đầu")
		}
		return nil
	})
	o, _ := temporal.New(rec.run, cfgFor(addr, t.Name()))
	defer o.Shutdown(context.Background())
	_ = o.StartWorker()
	_ = o.Submit(context.Background(), "flaky")
	eventually(t, func() bool { return rec.count("flaky") == 2 }, "lần chạy lỗi phải được chạy lại")
}

func TestIntegrationJobIsReassignedWhenWorkerStopsMidRun(t *testing.T) {
	addr := server(t)
	cfg := cfgFor(addr, t.Name())
	started := make(chan struct{}, 1)
	// worker A: job chạy "mãi" cho tới khi ctx bị hủy (worker tắt)
	recA := newRecorder(func(ctx context.Context, _ string, _ int) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	a, _ := temporal.New(recA.run, cfg)
	if err := a.StartWorker(); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(context.Background(), "long-job"); err != nil {
		t.Fatal(err)
	}
	<-started
	// worker B sẵn sàng; A tắt giữa chừng => Temporal giao lại job cho B
	recB := newRecorder(nil)
	b, _ := temporal.New(recB.run, cfg)
	defer b.Shutdown(context.Background())
	if err := b.StartWorker(); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return recB.count("long-job") >= 1 }, "job dở dang phải được giao lại cho worker khác")
}
