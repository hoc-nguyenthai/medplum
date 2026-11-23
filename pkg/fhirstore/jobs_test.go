package fhirstore_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Khôi phục job mồ côi và xóa kết quả export hết hạn dựa vào search AsyncJob (status, _lastUpdated) và cập nhật
// có điều kiện (If-Match): kiểm tra trên SQL thật.
func TestRecoverAndExpireJobsOnSQL(t *testing.T) {
	ctx := context.Background()
	s := newSQLite(t)
	var runs atomic.Int32
	mk := func() *fhirrouter.Router {
		r := fhirrouter.New(s, fhirrouter.Options{JobRecoveryGrace: time.Millisecond, ExportRetention: 50 * time.Millisecond})
		r.RegisterJob("work", func(context.Context, fhirrouter.Repository, *fhirrouter.Input) (*fhir.Parameters, error) {
			runs.Add(1)
			return &fhir.Parameters{}, nil
		})
		t.Cleanup(func() { _ = r.Shutdown(ctx) })
		return r
	}
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, j := range []*fhir.AsyncJob{
		{ID: "orphan", Status: "accepted", Request: "work?", RequestTime: old},
		{ID: "leased", Status: "accepted", Request: "work?", RequestTime: old, TransactionTime: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)},
	} {
		if _, err := s.CreateResource(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	r1, r2 := mk(), mk()
	for _, r := range []*fhirrouter.Router{r1, r2} {
		if _, err := r.RecoverJobs(ctx); err != nil {
			t.Fatal(err)
		}
	}
	state := func(id string) string {
		res, err := s.ReadResource(ctx, "AsyncJob", id)
		if err != nil {
			t.Fatal(err)
		}
		return res.(*fhir.AsyncJob).Status
	}
	for i := 0; i < 300 && state("orphan") != "completed"; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if state("orphan") != "completed" || runs.Load() != 1 || state("leased") != "accepted" {
		t.Fatalf("orphan=%s leased=%s runs=%d; muốn completed, accepted, 1", state("orphan"), state("leased"), runs.Load())
	}

	// hết hạn: export cũ cùng Binary bị xóa, job khác loại giữ lại
	if _, err := s.CreateResource(ctx, &fhir.Binary{ID: "file1", ContentType: "application/fhir+ndjson", Data: "e30K"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateResource(ctx, &fhir.AsyncJob{ID: "exp", Status: "completed", Request: "export?_level=system", Output: &fhir.Parameters{Parameter: []fhir.ParametersParameter{
		{Name: "output", Part: []fhir.ParametersParameter{{Name: "url", ValueUri: "http://x/fhir/R4/Binary/file1"}}}}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	n, err := r1.ExpireJobs(ctx)
	if err != nil || n != 1 {
		t.Fatalf("ExpireJobs = %d, %v; muốn 1", n, err)
	}
	if _, err := s.ReadResource(ctx, "Binary", "file1"); err == nil {
		t.Error("Binary của export hết hạn phải bị xóa")
	}
	if _, err := s.ReadResource(ctx, "AsyncJob", "orphan"); err != nil {
		t.Error("job không phải export không được xóa")
	}
}
