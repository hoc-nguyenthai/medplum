package fhirrouter_test

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Hủy job do replica khác: AsyncJob chỉ được đánh dấu `cancelled` trong kho, replica đang chạy job phát hiện bằng vòng thăm dò riêng (JobCancelPoll), không
// phải chờ nhịp gia hạn lease (JobLease/3, mặc định 100 giây).

func startLongJob(t *testing.T, opts fhirrouter.Options) (repo *fhirrouter.MemoryRepository, id string, stopped <-chan struct{}) {
	t.Helper()
	repo = fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, opts)
	started, done := make(chan struct{}), make(chan struct{})
	r.RegisterJob("long", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done() // chạy tới khi bị hủy
		close(done)
		return nil, ctx.Err()
	})
	job, err := r.StartJob(context.Background(), "long", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("job không bắt đầu")
	}
	return repo, job.ID, done
}

// cancelElsewhere đánh dấu job cancelled thẳng trong kho: như một replica khác xử lý DELETE (không có hàm hủy trong tiến trình này).
func cancelElsewhere(t *testing.T, repo fhirrouter.Repository, id string) {
	t.Helper()
	cur := jobOf(t, repo, id)
	cur.Status = "cancelled"
	if _, err := repo.UpdateResource(context.Background(), cur, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestJobCancelledElsewhereStopsWithinThePollInterval(t *testing.T) {
	// lease 10 phút nên nhịp gia hạn là 200 giây: chỉ vòng thăm dò mới kịp thấy việc hủy
	repo, id, stopped := startLongJob(t, fhirrouter.Options{JobLease: 10 * time.Minute, JobCancelPoll: 30 * time.Millisecond})
	cancelElsewhere(t, repo, id)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("job không dừng trong 2 giây dù đã bị hủy ở nơi khác (chờ nhịp lease?)")
	}
}

func TestJobCancelPollDefaultsToSecondsNotLeaseFraction(t *testing.T) {
	// không đặt JobCancelPoll: mặc định vài giây, vẫn nhanh hơn nhiều so với lease/3 = 200 giây
	repo, id, stopped := startLongJob(t, fhirrouter.Options{JobLease: 10 * time.Minute})
	cancelElsewhere(t, repo, id)
	select {
	case <-stopped:
	case <-time.After(8 * time.Second):
		t.Fatal("mặc định phải phát hiện việc hủy trong vài giây")
	}
}

// Thăm dò dày hơn không được làm lease gia hạn dày theo: job vẫn chỉ gia hạn mỗi JobLease/3 (nếu không, mỗi lần thăm dò ghi một phiên bản AsyncJob mới).
func TestCancelPollDoesNotRenewTheLeaseEveryPoll(t *testing.T) {
	var writes atomic.Int32
	mem := fhirrouter.NewMemoryRepository()
	repo := &countingUpdates{MemoryRepository: mem, n: &writes}
	r := newRouter(t, repo, fhirrouter.Options{JobLease: 3 * time.Second, JobCancelPoll: 20 * time.Millisecond})
	started := make(chan struct{})
	r.RegisterJob("long", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	job, err := r.StartJob(context.Background(), "long", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	base := writes.Load()              // ghi lúc nhận job (claim)
	time.Sleep(600 * time.Millisecond) // 30 lần thăm dò, nhưng lease/3 = 1 giây nên chưa tới nhịp gia hạn
	if got := writes.Load() - base; got != 0 {
		t.Errorf("job ghi %d lần trong 600ms dù chưa tới nhịp gia hạn (mỗi lần thăm dò đều ghi?)", got)
	}
	cancelElsewhere(t, mem, job.ID)
}

type countingUpdates struct {
	*fhirrouter.MemoryRepository
	n *atomic.Int32
}

func (c *countingUpdates) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	if r.GetResourceType() == "AsyncJob" {
		c.n.Add(1)
	}
	return c.MemoryRepository.UpdateResource(ctx, r, o)
}

// Mặc định thăm dò thưa (vài giây): job chạy lâu không được đọc AsyncJob mỗi vài chục mili giây.
func TestDefaultCancelPollIsNotTight(t *testing.T) {
	var reads atomic.Int32
	mem := fhirrouter.NewMemoryRepository()
	repo := &countingReads{MemoryRepository: mem, n: &reads}
	r := newRouter(t, repo, fhirrouter.Options{JobLease: 10 * time.Minute})
	started := make(chan struct{})
	r.RegisterJob("long", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	job, err := r.StartJob(context.Background(), "long", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	base := reads.Load()
	time.Sleep(700 * time.Millisecond)
	if got := reads.Load() - base; got > 1 {
		t.Errorf("đọc AsyncJob %d lần trong 700ms với cấu hình mặc định (muốn thưa, vài giây một lần)", got)
	}
	cancelElsewhere(t, mem, job.ID)
}

type countingReads struct {
	*fhirrouter.MemoryRepository
	n *atomic.Int32
}

func (c *countingReads) ReadResource(ctx context.Context, rt, id string) (fhir.Resource, error) {
	if rt == "AsyncJob" {
		c.n.Add(1)
	}
	return c.MemoryRepository.ReadResource(ctx, rt, id)
}

// Lease vẫn được gia hạn đều đặn mỗi JobLease/3 trong khi thăm dò dày hơn: không gia hạn quá thưa (job bị tiếp quản oan) cũng không quá dày.
func TestLeaseStillRenewedEveryThirdOfTheLease(t *testing.T) {
	var writes atomic.Int32
	mem := fhirrouter.NewMemoryRepository()
	repo := &countingUpdates{MemoryRepository: mem, n: &writes}
	r := newRouter(t, repo, fhirrouter.Options{JobLease: 300 * time.Millisecond, JobCancelPoll: 10 * time.Millisecond})
	started := make(chan struct{})
	r.RegisterJob("long", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	job, err := r.StartJob(context.Background(), "long", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	base := writes.Load()
	time.Sleep(1000 * time.Millisecond) // lease/3 = 100ms nên khoảng 10 lần gia hạn
	got := writes.Load() - base
	if got < 5 || got > 14 {
		t.Errorf("gia hạn lease %d lần trong 1 giây, muốn khoảng 10 (mỗi JobLease/3 = 100ms)", got)
	}
	cancelElsewhere(t, mem, job.ID)
}
