package bootstrap

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"trivita-fhir/pkg/dicomretrieve"
	"trivita-fhir/pkg/fhir"
)

// pixelJob là một instance cần dựng pixelData; ctx mang Principal của người STOW (đã tách khỏi vòng đời của yêu cầu) để việc dựng đi qua đúng quyền và project của họ.
type pixelJob struct {
	ctx context.Context
	id  string
}

// pixelWorker dựng `DicomInstance.pixelData` (mỗi frame một Binary) cho instance mới lưu, như worker của Medplum nhưng chạy trong tiến trình: hàng đợi trong bộ nhớ, thử lại 3 lần lùi theo cấp số
// nhân từ 1 giây. Hàng đợi đầy thì bỏ (ghi log): `dicomweb pixeldata` bù những instance còn thiếu. Tắt tiến trình thì chờ việc đang chạy xong (trong hạn Stop) và bỏ phần còn trong hàng đợi.
type pixelWorker struct {
	c     *Container
	queue chan pixelJob
	wg    sync.WaitGroup
	once  sync.Once
	stop  chan struct{}
	// backoff là thời gian chờ trước lần thử lại thứ n (n từ 1); đặt được trong test.
	backoff func(n int) time.Duration
}

const pixelQueueSize = 1024

func newPixelWorker(c *Container, workers int) *pixelWorker {
	w := &pixelWorker{c: c, queue: make(chan pixelJob, pixelQueueSize), stop: make(chan struct{}), backoff: func(n int) time.Duration { return time.Duration(1<<(n-1)) * time.Second }}
	for i := 0; i < workers; i++ {
		w.wg.Add(1)
		go w.run()
	}
	return w
}

// enqueue đưa instance vào hàng đợi; false nếu đầy hoặc đã tắt.
func (w *pixelWorker) enqueue(ctx context.Context, id string) bool {
	select {
	case <-w.stop:
		return false
	default:
	}
	select {
	case w.queue <- pixelJob{ctx: context.WithoutCancel(ctx), id: id}:
		return true
	default:
		slog.WarnContext(ctx, "dicomweb: hàng đợi pixelData đầy, bỏ qua (chạy `dicomweb pixeldata` để bù)", "instance", id)
		return false
	}
}

func (w *pixelWorker) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			return
		case job := <-w.queue:
			w.process(job)
		}
	}
}

func (w *pixelWorker) process(job pixelJob) {
	cfg := dicomretrieve.Config{Repo: w.c.Repo, Blobs: w.c.Blobs}
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		var r fhir.Resource
		if r, err = cfg.Repo.ReadResource(job.ctx, "DicomInstance", job.id); err == nil {
			if inst, ok := r.(*fhir.DicomInstance); ok {
				_, err = dicomretrieve.BuildPixelData(job.ctx, cfg, inst, dicomretrieve.PixelDataOptions{})
			}
		}
		if err == nil || err == dicomretrieve.ErrNoPixels {
			return
		}
		if attempt < 3 {
			select {
			case <-time.After(w.backoff(attempt)):
			case <-w.stop:
				return
			}
		}
	}
	slog.ErrorContext(job.ctx, "dicomweb: không dựng được pixelData sau 3 lần (chạy `dicomweb pixeldata` để bù)", "instance", job.id, "error", err.Error())
}

// shutdown ngừng nhận việc, chờ các việc đang chạy xong hoặc hết hạn ctx.
func (w *pixelWorker) shutdown(ctx context.Context) error {
	w.once.Do(func() { close(w.stop) })
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
