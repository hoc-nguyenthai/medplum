package fhirrouter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func okJob(counter *atomic.Int32) fhirrouter.JobFunc {
	return func(context.Context, fhirrouter.Repository, *fhirrouter.Input) (*fhir.Parameters, error) {
		counter.Add(1)
		return &fhir.Parameters{}, nil
	}
}

func seedJob(t *testing.T, repo fhirrouter.Repository, id, request, lease string) {
	t.Helper()
	_, err := repo.CreateResource(context.Background(), &fhir.AsyncJob{ID: id, Status: "accepted", Request: request,
		RequestTime: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), TransactionTime: lease})
	if err != nil {
		t.Fatal(err)
	}
}

func jobOf(t *testing.T, repo fhirrouter.Repository, id string) *fhir.AsyncJob {
	t.Helper()
	r, err := repo.ReadResource(context.Background(), "AsyncJob", id)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*fhir.AsyncJob)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("quá hạn chờ: %s", what)
}

func newRouter(t *testing.T, repo fhirrouter.Repository, o fhirrouter.Options) *fhirrouter.Router {
	t.Helper()
	if o.BaseURL == "" {
		o.BaseURL = "http://test/fhir/R4"
	}
	r := fhirrouter.New(repo, o)
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	return r
}

func TestRecoverOrphanedJobRunsOnce(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	seedJob(t, repo, "orphan", "work?x=1", "")
	var runs atomic.Int32
	// hai replica dùng chung một kho, cùng khôi phục
	var routers []*fhirrouter.Router
	for i := 0; i < 2; i++ {
		r := newRouter(t, repo, fhirrouter.Options{JobRecoveryGrace: time.Millisecond})
		r.RegisterJob("work", okJob(&runs))
		routers = append(routers, r)
	}
	for _, r := range routers {
		if _, err := r.RecoverJobs(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "job hoàn tất", func() bool { return jobOf(t, repo, "orphan").Status == "completed" })
	time.Sleep(100 * time.Millisecond)
	if runs.Load() != 1 {
		t.Errorf("job chạy %d lần, muốn đúng 1", runs.Load())
	}
	// job đã xong không bị chạy lại
	for _, r := range routers {
		if n, _ := r.RecoverJobs(context.Background()); n != 0 {
			t.Errorf("job đã xong vẫn được đưa vào hàng đợi: %d", n)
		}
	}
}

func TestRecoverRespectsLeaseAndGrace(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	var runs atomic.Int32
	r := newRouter(t, repo, fhirrouter.Options{JobRecoveryGrace: 30 * time.Minute}) // seedJob tạo job cũ 1 giờ
	r.RegisterJob("work", okJob(&runs))

	seedJob(t, repo, "leased", "work?", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)) // replica khác đang giữ
	// job vừa tạo (trong thời gian ân hạn) không bị giành
	_, _ = repo.CreateResource(context.Background(), &fhir.AsyncJob{ID: "fresh", Status: "accepted", Request: "work?",
		RequestTime: time.Now().UTC().Format(time.RFC3339)})
	if n, err := r.RecoverJobs(context.Background()); err != nil || n != 0 {
		t.Fatalf("RecoverJobs = %d, %v; muốn 0", n, err)
	}
	time.Sleep(80 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatalf("job có lease hợp lệ hoặc còn mới đã bị chạy")
	}

	// lease hết hạn thì được tiếp quản
	seedJob(t, repo, "expired", "work?", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))
	if n, _ := r.RecoverJobs(context.Background()); n != 1 {
		t.Fatalf("job hết lease: n = %d, muốn 1", n)
	}
	waitFor(t, "job hết lease hoàn tất", func() bool { return jobOf(t, repo, "expired").Status == "completed" })
	if jobOf(t, repo, "leased").Status != "accepted" {
		t.Error("job có lease không được đổi trạng thái")
	}
}

func TestShutdownReleasesLeaseAndNextProcessResumes(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	started := make(chan struct{})
	r1 := fhirrouter.New(repo, fhirrouter.Options{})
	r1.RegisterJob("long", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	job, err := r1.StartJob(context.Background(), "long", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if j := jobOf(t, repo, job.ID); j.TransactionTime == "" {
		t.Fatal("job đang chạy phải giữ lease")
	}
	sctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = r1.Shutdown(sctx) // process tắt giữa chừng
	j := jobOf(t, repo, job.ID)
	if j.Status != "accepted" || j.TransactionTime != "" {
		t.Fatalf("sau khi tắt: status=%s lease=%q; muốn accepted và lease đã nhả", j.Status, j.TransactionTime)
	}

	// process mới tiếp quản và hoàn tất
	var runs atomic.Int32
	r2 := newRouter(t, repo, fhirrouter.Options{JobRecoveryGrace: time.Millisecond})
	r2.RegisterJob("long", okJob(&runs))
	if n, _ := r2.RecoverJobs(context.Background()); n != 1 {
		t.Fatalf("RecoverJobs = %d", n)
	}
	waitFor(t, "hoàn tất ở process mới", func() bool { return jobOf(t, repo, job.ID).Status == "completed" })
}

func TestHeartbeatKeepsLeaseWhileRunning(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	release := make(chan struct{})
	var runs atomic.Int32
	lease := 90 * time.Millisecond
	mk := func() *fhirrouter.Router {
		r := newRouter(t, repo, fhirrouter.Options{JobLease: lease, JobRecoveryGrace: time.Millisecond})
		r.RegisterJob("slow", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
			runs.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return &fhir.Parameters{}, nil
		})
		return r
	}
	r1, r2 := mk(), mk()
	var once atomic.Bool
	releaseOnce := func() {
		if once.CompareAndSwap(false, true) {
			close(release)
		}
	}
	t.Cleanup(releaseOnce) // chạy trước Shutdown (LIFO): lỗi giữa chừng không làm treo
	job, _ := r1.StartJob(context.Background(), "slow", nil)
	waitFor(t, "job bắt đầu", func() bool { return runs.Load() == 1 })
	// chờ gấp vài lần lease: nếu không gia hạn, r2 sẽ tiếp quản
	for i := 0; i < 6; i++ {
		time.Sleep(lease / 2)
		if n, _ := r2.RecoverJobs(context.Background()); n != 0 {
			// được phép đưa vào hàng đợi nhưng runJob phải từ chối vì lease còn hạn
			time.Sleep(20 * time.Millisecond)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("job chạy %d lần dù replica đầu còn sống", runs.Load())
	}
	releaseOnce()
	waitFor(t, "job hoàn tất", func() bool { return jobOf(t, repo, job.ID).Status == "completed" })
}

func TestMaintenanceLoopRecoversAutomatically(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	seedJob(t, repo, "orphan", "work?", "")
	var runs atomic.Int32
	r := fhirrouter.New(repo, fhirrouter.Options{JobMaintenanceInterval: 20 * time.Millisecond, JobRecoveryGrace: time.Millisecond})
	r.RegisterJob("work", okJob(&runs)) // đăng ký sau New: vòng bảo trì chạy ngay nhưng job chỉ được nhận khi có hàm
	waitFor(t, "vòng bảo trì chạy job", func() bool { return jobOf(t, repo, "orphan").Status == "completed" })
	done := make(chan struct{})
	go func() { _ = r.Shutdown(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown không dừng vòng bảo trì")
	}
}

// ---------------------------------------------------------------- bulk export: hủy, hết hạn

func do(h http.Handler, method, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/fhir/R4/"+path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestExportCancelWhileRunningAndDelete(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{JobLease: 60 * time.Millisecond})
	started := make(chan struct{}, 1)
	var cancelled atomic.Bool
	r.RegisterJob("export", func(ctx context.Context, repo fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		// tạo một file rồi chờ tới khi bị hủy: file phải bị dọn
		_, _ = repo.CreateResource(ctx, &fhir.Binary{ID: "partial", ContentType: "application/fhir+ndjson", Data: "e30K"})
		started <- struct{}{}
		<-ctx.Done()
		cancelled.Store(true)
		return &fhir.Parameters{Parameter: []fhir.ParametersParameter{{Name: "output", Part: []fhir.ParametersParameter{
			{Name: "url", ValueUri: "http://test/fhir/R4/Binary/partial"}}}}}, nil
	})
	h := http.StripPrefix("/fhir/R4", r.HTTPHandler())

	acc := do(h, "GET", "$export", "Prefer", "respond-async")
	if acc.Code != 202 {
		t.Fatalf("export: %d %s", acc.Code, acc.Body)
	}
	status := strings.TrimPrefix(acc.Header().Get("Content-Location"), "http://test/fhir/R4/")
	<-started
	if w := do(h, "DELETE", status); w.Code != 202 {
		t.Fatalf("hủy: %d %s", w.Code, w.Body)
	}
	waitFor(t, "job nhận tín hiệu hủy", cancelled.Load)
	time.Sleep(100 * time.Millisecond)
	if w := do(h, "GET", status); w.Code != 410 {
		t.Errorf("trạng thái sau khi hủy: %d, muốn 410", w.Code)
	}
	if _, err := repo.ReadResource(context.Background(), "Binary", "partial"); err == nil {
		t.Error("file của job bị hủy phải được dọn")
	}
	// xóa hẳn
	if w := do(h, "DELETE", status); w.Code != 202 {
		t.Errorf("xóa job đã hủy: %d", w.Code)
	}
	if w := do(h, "GET", status); w.Code != 410 {
		t.Errorf("trạng thái sau khi xóa: %d, muốn 410", w.Code)
	}
	if w := do(h, "DELETE", "bulkdata/none/status"); w.Code != 404 {
		t.Errorf("hủy job không có: %d", w.Code)
	}
}

func TestExportDeleteRemovesCompletedFiles(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{})
	h := http.StripPrefix("/fhir/R4", r.HTTPHandler())
	_, _ = repo.CreateResource(context.Background(), &fhir.Patient{ID: "p1"})
	acc := do(h, "GET", "$export", "Prefer", "respond-async")
	status := strings.TrimPrefix(acc.Header().Get("Content-Location"), "http://test/fhir/R4/")
	waitFor(t, "export xong", func() bool { return do(h, "GET", status).Code == 200 })
	res, _ := repo.Search(context.Background(), mustSearch(t, "Binary"))
	if len(res.Matches) != 1 {
		t.Fatalf("muốn 1 file, được %d", len(res.Matches))
	}
	if w := do(h, "DELETE", status); w.Code != 202 {
		t.Fatalf("xóa: %d", w.Code)
	}
	res, _ = repo.Search(context.Background(), mustSearch(t, "Binary"))
	if len(res.Matches) != 0 {
		t.Errorf("còn %d file sau khi xóa export", len(res.Matches))
	}
}

func TestExpireJobs(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{ExportRetention: 50 * time.Millisecond})
	h := http.StripPrefix("/fhir/R4", r.HTTPHandler())
	_, _ = repo.CreateResource(context.Background(), &fhir.Patient{ID: "p1"})
	acc := do(h, "GET", "$export", "Prefer", "respond-async")
	status := strings.TrimPrefix(acc.Header().Get("Content-Location"), "http://test/fhir/R4/")
	waitFor(t, "export xong", func() bool { return do(h, "GET", status).Code == 200 })
	// job không phải export không bị xóa
	_, _ = repo.CreateResource(context.Background(), &fhir.AsyncJob{ID: "other", Status: "completed", Request: "reindex?"})

	if n, _ := r.ExpireJobs(context.Background()); n != 0 {
		t.Fatalf("chưa hết hạn mà đã xóa %d", n)
	}
	time.Sleep(120 * time.Millisecond)
	if n, err := r.ExpireJobs(context.Background()); err != nil || n != 1 {
		t.Fatalf("ExpireJobs = %d, %v; muốn 1", n, err)
	}
	if w := do(h, "GET", status); w.Code != 410 {
		t.Errorf("export hết hạn: %d, muốn 410", w.Code)
	}
	if res, _ := repo.Search(context.Background(), mustSearch(t, "Binary")); len(res.Matches) != 0 {
		t.Error("file của export hết hạn phải bị xóa")
	}
	if _, err := repo.ReadResource(context.Background(), "AsyncJob", "other"); err != nil {
		t.Error("job khác loại không được xóa")
	}
	keep := newRouter(t, repo, fhirrouter.Options{ExportRetention: -1})
	if n, _ := keep.ExpireJobs(context.Background()); n != 0 {
		t.Error("retention âm là không bao giờ xóa")
	}
}

func mustSearch(t *testing.T, rt string) *fhirsearch.Request {
	t.Helper()
	r, err := fhirsearch.ParseQuery(rt, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestChainedSearchLimit(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{})
	h := http.StripPrefix("/fhir/R4", r.HTTPHandler())
	for i := 0; i < 5001; i++ {
		_, err := repo.CreateResource(context.Background(), &fhir.Patient{ID: "p" + strings.Repeat("x", 0) + itoaT(i), Gender: "male"})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := do(h, "GET", "Observation?subject:Patient.gender=male")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "quá nhiều") {
		t.Errorf("muốn 400 báo khớp quá nhiều, được %d %s", w.Code, w.Body)
	}
	// thu hẹp thì chạy được
	if w := do(h, "GET", "Observation?subject:Patient.gender=female"); w.Code != 200 {
		t.Errorf("thu hẹp: %d", w.Code)
	}
}

func itoaT(n int) string { return strconv.Itoa(n) }
