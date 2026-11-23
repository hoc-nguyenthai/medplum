package asynq_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	hq "github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	wf "trivita-fhir/pkg/workflow/asynq"
)

// ---- Redis thật: asynq dùng nhiều script Lua nên không giả lập bằng miniredis. Bỏ qua nếu không có redis-server. ----

var (
	redisOnce sync.Once
	redisURL  string
	redisCmd  *exec.Cmd
	redisErr  error
)

func startRedis() {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		redisErr = err
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		redisErr = err
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	redisCmd = exec.Command(bin, "--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", os.TempDir())
	if err := redisCmd.Start(); err != nil {
		redisErr = err
		return
	}
	redisURL = fmt.Sprintf("redis://127.0.0.1:%d/0", port)
	c := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
	defer c.Close()
	for range 100 {
		if c.Ping(context.Background()).Err() == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	redisErr = errors.New("redis-server không sẵn sàng")
}

func TestMain(m *testing.M) {
	code := m.Run()
	if redisCmd != nil && redisCmd.Process != nil {
		_ = redisCmd.Process.Kill()
		_ = redisCmd.Wait()
	}
	os.Exit(code)
}

func redisAddr(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("bỏ qua test tích hợp Redis với -short")
	}
	redisOnce.Do(startRedis)
	if redisErr != nil {
		t.Skipf("không có Redis để chạy test tích hợp: %v", redisErr)
	}
	return redisURL
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

// cfg dùng queue riêng cho từng test để các test không đụng nhau trên cùng một Redis.
func cfg(t *testing.T) wf.Config {
	return wf.Config{
		RedisURL: redisAddr(t), Queue: "q-" + t.Name(), TaskType: "run_job", MaxRetry: 2, RetryInterval: 50 * time.Millisecond,
		PollInterval: 20 * time.Millisecond, WorkerStopTimeout: 150 * time.Millisecond, Retention: time.Minute,
	}
}

func mustNew(t *testing.T, run func(context.Context, string) error, c wf.Config) *wf.Orchestrator {
	t.Helper()
	o, err := wf.New(run, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Shutdown(context.Background()) })
	return o
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestSubmitRunsOnceAndIsIdempotent(t *testing.T) {
	rec := newRecorder(nil)
	o := mustNew(t, rec.run, cfg(t))
	if err := o.StartWorker(); err != nil {
		t.Fatal(err)
	}
	if err := o.StartWorker(); err == nil {
		t.Fatal("khởi động worker lần hai phải lỗi")
	}
	if err := o.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 3 { // API retry gửi lặp cho cùng job không được chạy nhiều lần
		if err := o.Submit(context.Background(), "job-1"); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, func() bool { return rec.count("job-1") >= 1 }, "job phải được chạy")
	time.Sleep(300 * time.Millisecond)
	if n := rec.count("job-1"); n != 1 {
		t.Fatalf("job chạy %d lần, want 1", n)
	}
	if err := o.Submit(context.Background(), "job-1"); err != nil { // đã xong, còn trong Retention
		t.Fatalf("Submit job đã hoàn tất: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := rec.count("job-1"); n != 1 {
		t.Fatalf("job hoàn tất bị chạy lại: %d", n)
	}
}

func TestJobSurvivesUntilAWorkerAppears(t *testing.T) {
	c := cfg(t)
	rec := newRecorder(nil)
	api := mustNew(t, rec.run, c) // chỉ gửi job, không chạy worker (API-only pod)
	if err := api.Submit(context.Background(), "queued-job"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if rec.count("queued-job") != 0 {
		t.Fatal("chưa có worker thì job không được chạy")
	}
	w := mustNew(t, rec.run, c) // worker riêng xuất hiện sau
	if err := w.StartWorker(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return rec.count("queued-job") == 1 }, "job đã xếp hàng phải được worker mới nhận")
}

func TestFailedAttemptIsRetried(t *testing.T) {
	rec := newRecorder(func(_ context.Context, _ string, attempt int) error {
		if attempt == 1 {
			return errors.New("lỗi tạm ở lần đầu")
		}
		return nil
	})
	o := mustNew(t, rec.run, cfg(t))
	_ = o.StartWorker()
	_ = o.Submit(context.Background(), "flaky")
	eventually(t, func() bool { return rec.count("flaky") == 2 }, "lần chạy lỗi phải được chạy lại")
	time.Sleep(300 * time.Millisecond)
	if rec.count("flaky") != 2 {
		t.Fatal("thành công rồi thì không chạy thêm")
	}
}

func TestJobIsArchivedAfterMaxRetriesNotLost(t *testing.T) {
	c := cfg(t)
	c.MaxRetry = 1 // 1 lần đầu + 1 lần chạy lại
	rec := newRecorder(func(context.Context, string, int) error { return errors.New("hỏng hẳn") })
	o := mustNew(t, rec.run, c)
	_ = o.StartWorker()
	_ = o.Submit(context.Background(), "doomed")
	eventually(t, func() bool { return rec.count("doomed") == 2 }, "phải chạy đúng 1 + MaxRetry lần")
	time.Sleep(400 * time.Millisecond)
	if n := rec.count("doomed"); n != 2 {
		t.Fatalf("chạy %d lần, want 2", n)
	}
	opt, _ := hq.ParseRedisURI(c.RedisURL)
	insp := hq.NewInspector(opt)
	defer insp.Close()
	eventually(t, func() bool {
		q, err := insp.GetQueueInfo(c.Queue)
		return err == nil && q.Archived == 1
	}, "job lỗi quá số lần retry phải nằm trong archive để kiểm tra, không bị mất")

	// không retry
	c2 := cfg(t)
	c2.Queue += "-noretry"
	c2.MaxRetry = -1
	rec2 := newRecorder(func(context.Context, string, int) error { return errors.New("x") })
	o2 := mustNew(t, rec2.run, c2)
	_ = o2.StartWorker()
	_ = o2.Submit(context.Background(), "once")
	eventually(t, func() bool { return rec2.count("once") == 1 }, "phải chạy một lần")
	time.Sleep(400 * time.Millisecond)
	if rec2.count("once") != 1 {
		t.Fatalf("MaxRetry < 0 là không retry: %d lần", rec2.count("once"))
	}
}

func TestJobIsReassignedWhenWorkerStopsMidRun(t *testing.T) {
	c := cfg(t)
	started := make(chan struct{}, 1)
	recA := newRecorder(func(ctx context.Context, _ string, _ int) error {
		started <- struct{}{}
		<-ctx.Done() // job "chạy mãi" cho tới khi worker bị tắt
		return ctx.Err()
	})
	a := mustNew(t, recA.run, c)
	if err := a.StartWorker(); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(context.Background(), "long-job"); err != nil {
		t.Fatal(err)
	}
	<-started
	recB := newRecorder(nil)
	b := mustNew(t, recB.run, c)
	if err := b.StartWorker(); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil { // A tắt giữa chừng (quá WorkerStopTimeout nên ctx bị hủy)
		t.Fatal(err)
	}
	eventually(t, func() bool { return recB.count("long-job") >= 1 }, "job dở dang phải được giao lại cho worker khác")
}

func TestMalformedPayloadIsNotRetried(t *testing.T) {
	c := cfg(t)
	rec := newRecorder(nil)
	o := mustNew(t, rec.run, c)
	_ = o.StartWorker()
	opt, _ := hq.ParseRedisURI(c.RedisURL)
	raw := hq.NewClient(opt)
	defer raw.Close()
	for _, p := range []string{"không phải json", `{"job_id": ""}`} {
		if _, err := raw.Enqueue(hq.NewTask(c.TaskType, []byte(p)), hq.Queue(c.Queue), hq.MaxRetry(5)); err != nil {
			t.Fatal(err)
		}
	}
	insp := hq.NewInspector(opt)
	defer insp.Close()
	eventually(t, func() bool {
		q, err := insp.GetQueueInfo(c.Queue)
		return err == nil && q.Archived == 2
	}, "payload hỏng phải vào archive ngay (SkipRetry), không retry vô ích")
	if len(rec.calls) != 0 {
		t.Fatal("payload hỏng không được chạy JobRunner")
	}
}

func TestConfigValidationLifecycleAndLazyConnect(t *testing.T) {
	run := func(context.Context, string) error { return nil }
	if _, err := wf.New(run, wf.Config{}); err == nil {
		t.Fatal("thiếu RedisURL phải lỗi")
	}
	if _, err := wf.New(run, wf.Config{RedisURL: "http://sai"}); err == nil {
		t.Fatal("RedisURL sai phải lỗi")
	}
	if id := (wf.Config{TaskType: "ocr"}).TaskID("abc"); id != "ocr:abc" {
		t.Fatalf("id = %q", id)
	}
	// nối lazy: không lỗi khi Redis chưa chạy; Ping và Submit mới báo lỗi
	o, err := wf.New(run, wf.Config{RedisURL: "redis://127.0.0.1:1/0"})
	if err != nil {
		t.Fatalf("không được lỗi lúc khởi tạo: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := o.Ping(ctx); err == nil {
		t.Fatal("Ping tới Redis không tồn tại phải lỗi")
	}
	if err := o.Submit(ctx, "j"); err == nil {
		t.Fatal("Submit khi Redis chết phải lỗi (không báo đã xếp hàng)")
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown lần hai là no-op: %v", err)
	}
	if err := o.Submit(context.Background(), "j"); err == nil {
		t.Fatal("sau Shutdown không nhận job")
	}
	if err := o.StartWorker(); err == nil {
		t.Fatal("sau Shutdown không khởi động worker")
	}
}

func TestQueueDepth(t *testing.T) {
	o, err := wf.New(func(context.Context, string) error { return nil }, wf.Config{RedisURL: redisAddr(t), Queue: "depth-" + t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Shutdown(context.Background())
	ctx := context.Background()
	if n, err := o.QueueDepth(ctx); err != nil || n != 0 { // chưa có hàng đợi
		t.Fatalf("QueueDepth rỗng = %d, %v", n, err)
	}
	for _, id := range []string{"a", "b", "c", "a"} { // "a" lặp lại là no-op
		if err := o.Submit(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := o.QueueDepth(ctx); err != nil || n != 3 {
		t.Fatalf("QueueDepth = %d, %v; muốn 3", n, err)
	}
}
