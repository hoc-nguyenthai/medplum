package fhirrouter_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// AsyncJob hết hạn (hoặc bị chủ xóa sau khi xong) phải bị Expunge, không chỉ xóa mềm: xóa mềm giữ nguyên mọi phiên bản trong lịch sử, mà mỗi job ghi nhiều
// phiên bản (nhận, lease, gia hạn, kết quả; batch lớn còn hơn), nên lịch sử AsyncJob phình mãi dù job đã "dọn".

type recRepo struct {
	*fhirrouter.MemoryRepository
	mu       sync.Mutex
	expunged []string
	deleted  []string
}

func (r *recRepo) Expunge(ctx context.Context, rt, id string) error {
	r.mu.Lock()
	r.expunged = append(r.expunged, rt+"/"+id)
	r.mu.Unlock()
	return r.MemoryRepository.Expunge(ctx, rt, id)
}

func (r *recRepo) DeleteResource(ctx context.Context, rt, id string) error {
	r.mu.Lock()
	r.deleted = append(r.deleted, rt+"/"+id)
	r.mu.Unlock()
	return r.MemoryRepository.DeleteResource(ctx, rt, id)
}

func (r *recRepo) has(list []string, v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestExpireJobsExpungesTheJobRow(t *testing.T) {
	repo := &recRepo{MemoryRepository: fhirrouter.NewMemoryRepository()}
	r := newRouter(t, repo, fhirrouter.Options{ExportRetention: 30 * time.Millisecond})
	for _, j := range []*fhir.AsyncJob{
		{ID: "e", Status: "completed", Request: "export?_level=system"},
		{ID: "b", Status: "error", Request: "batch?input=x"},
		{ID: "r", Status: "cancelled", Request: "reindex?resourceType=Patient"},
		{ID: "other", Status: "completed", Request: "something-else?x=1"},
	} {
		if _, err := repo.CreateResource(context.Background(), j); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(80 * time.Millisecond)
	if n, err := r.ExpireJobs(context.Background()); err != nil || n != 3 {
		t.Fatalf("ExpireJobs = %d, %v; muốn 3", n, err)
	}
	for _, id := range []string{"e", "b", "r"} {
		if !repo.has(repo.expunged, "AsyncJob/"+id) {
			t.Errorf("AsyncJob/%s phải bị Expunge, expunged=%v", id, repo.expunged)
		}
		if repo.has(repo.deleted, "AsyncJob/"+id) {
			t.Errorf("AsyncJob/%s không được chỉ xóa mềm (giữ lịch sử)", id)
		}
	}
	if repo.has(repo.expunged, "AsyncJob/other") || repo.has(repo.deleted, "AsyncJob/other") {
		t.Error("job khác loại không được đụng tới")
	}
}

// Repository không hỗ trợ Expunge thì vẫn dọn được bằng xóa mềm (không làm job nằm lại mãi).
func TestExpireJobsFallsBackToSoftDeleteWithoutExpunger(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	repo := struct{ fhirrouter.Repository }{mem} // ẩn Expunger
	r := newRouter(t, repo, fhirrouter.Options{ExportRetention: 30 * time.Millisecond})
	if _, err := mem.CreateResource(context.Background(), &fhir.AsyncJob{ID: "e", Status: "completed", Request: "export?_level=system"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if n, err := r.ExpireJobs(context.Background()); err != nil || n != 1 {
		t.Fatalf("ExpireJobs = %d, %v", n, err)
	}
	if _, err := mem.ReadResource(context.Background(), "AsyncJob", "e"); err == nil {
		t.Error("job phải bị xóa (mềm) khi không có Expunger")
	}
}

// Chủ xóa job đã xong qua DELETE bulkdata/{id}/status cũng Expunge dòng job (không chỉ file kết quả).
func TestOwnerDeleteOfFinishedJobExpungesTheRow(t *testing.T) {
	repo := &recRepo{MemoryRepository: fhirrouter.NewMemoryRepository()}
	h := asUser(newRouter(t, repo, ownerOpts()).HTTPHandler())
	id := startExport(t, h, "alice")
	waitFor(t, "export xong", func() bool {
		res, err := repo.ReadResource(context.Background(), "AsyncJob", id)
		return err == nil && res.(*fhir.AsyncJob).Status == "completed"
	})
	if c, _ := putTo(h, "DELETE", "bulkdata/"+id+"/status", "", "X-User", "alice"); c != http.StatusAccepted {
		t.Fatalf("DELETE: %d", c)
	}
	if !repo.has(repo.expunged, "AsyncJob/"+id) || repo.has(repo.deleted, "AsyncJob/"+id) {
		t.Errorf("dòng job phải bị Expunge: expunged=%v deleted=%v", repo.expunged, repo.deleted)
	}
	if c, _ := putTo(h, "GET", "bulkdata/"+id+"/status", "", "X-User", "root"); c != 404 {
		t.Errorf("job đã xóa hẳn: %d, muốn 404", c)
	}
}

// Expunge lỗi vì lý do khác 404/410 (ví dụ không đủ quyền) thì xóa mềm để job không nằm lại; Expunge báo 404 (replica khác đã xóa) thì không xóa mềm lần nữa.
type failingExpunge struct {
	*recRepo
	err error
}

func (f failingExpunge) Expunge(ctx context.Context, rt, id string) error {
	if rt == "AsyncJob" {
		return f.err
	}
	return f.recRepo.Expunge(ctx, rt, id)
}

func TestExpireJobsExpungeFailureHandling(t *testing.T) {
	for name, c := range map[string]struct {
		err       error
		wantN     int
		wantSoft  bool
		wantAfter bool // job còn đọc được sau đó
	}{
		"không đủ quyền: xóa mềm":     {fhirrouter.Forbidden("không được"), 1, true, false},
		"đã bị xóa hẳn (404): bỏ qua": {fhirrouter.NotFound(), 0, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recRepo{MemoryRepository: fhirrouter.NewMemoryRepository()}
			repo := failingExpunge{rec, c.err}
			r := newRouter(t, repo, fhirrouter.Options{ExportRetention: 30 * time.Millisecond})
			if _, err := rec.CreateResource(context.Background(), &fhir.AsyncJob{ID: "e", Status: "completed", Request: "export?_level=system"}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(80 * time.Millisecond)
			n, err := r.ExpireJobs(context.Background())
			if err != nil || n != c.wantN {
				t.Fatalf("ExpireJobs = %d, %v; muốn %d", n, err, c.wantN)
			}
			if got := rec.has(rec.deleted, "AsyncJob/e"); got != c.wantSoft {
				t.Errorf("xóa mềm = %v, muốn %v", got, c.wantSoft)
			}
			if _, err := rec.ReadResource(context.Background(), "AsyncJob", "e"); (err == nil) != c.wantAfter {
				t.Errorf("job còn đọc được = %v, muốn %v", err == nil, c.wantAfter)
			}
		})
	}
}
