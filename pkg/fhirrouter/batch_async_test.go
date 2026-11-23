package fhirrouter_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

const asyncBundle = `{"resourceType":"Bundle","type":"batch","entry":[
 {"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient","name":[{"family":"A"}]}},
 {"request":{"method":"POST","url":"Patient","ifNoneExist":"family=Cond"},"resource":{"resourceType":"Patient","name":[{"family":"Cond"}]}},
 {"request":{"method":"GET","url":"Patient/missing"}},
 {"request":{"method":"PUT","url":"Patient/fixed"},"resource":{"resourceType":"Patient","id":"fixed","name":[{"family":"F"}]}},
 {"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient","name":[{"family":"B"}]}}
]}`

func entryStatuses(b *fhir.Bundle) []string {
	var out []string
	for _, e := range b.Entry {
		out = append(out, e.Response.Status)
	}
	return out
}

func binaryJSON(t *testing.T, repo fhirrouter.Repository, id string, v any) {
	t.Helper()
	res, err := repo.ReadResource(context.Background(), "Binary", id)
	if err != nil {
		t.Fatalf("Binary/%s: %v", id, err)
	}
	data, err := base64.StdEncoding.DecodeString(res.(*fhir.Binary).Data)
	if err != nil || json.Unmarshal(data, v) != nil {
		t.Fatalf("Binary/%s hỏng: %v", id, err)
	}
}

func count(t *testing.T, repo fhirrouter.Repository, rt, query string) int {
	t.Helper()
	q, _ := url.ParseQuery(query)
	sr, err := fhirsearch.Parse(rt, q)
	if err != nil {
		t.Fatal(err)
	}
	sr.Count = 1000
	res, err := repo.Search(context.Background(), sr)
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Matches)
}

// waitJob chờ job xong và trả AsyncJob cùng Bundle kết quả.
func waitJob(t *testing.T, repo fhirrouter.Repository, id string) (*fhir.AsyncJob, *fhir.Bundle) {
	t.Helper()
	waitFor(t, "job xong", func() bool { return jobOf(t, repo, id).Status != "accepted" })
	job := jobOf(t, repo, id)
	if job.Status != "completed" || job.Output == nil || len(job.Output.Parameter) != 1 {
		t.Fatalf("job: %+v", job)
	}
	p := job.Output.Parameter[0]
	if p.Name != "results" || p.ValueReference == nil || !strings.HasPrefix(p.ValueReference.Reference, "Binary/") {
		t.Fatalf("output: %+v", p)
	}
	for _, e := range job.Extension {
		if e.URL == fhirrouter.JobStateURL {
			t.Error("trạng thái checkpoint phải bị bỏ khi job xong")
		}
	}
	var b fhir.Bundle
	binaryJSON(t, repo, strings.TrimPrefix(p.ValueReference.Reference, "Binary/"), &b)
	return job, &b
}

func jobIDFrom(t *testing.T, w *http.Response) string {
	t.Helper()
	loc := w.Header.Get("Content-Location")
	id, ok := strings.CutSuffix(loc[strings.LastIndex(loc, "/job/")+len("/job/"):], "/status")
	if !strings.Contains(loc, "/job/") || !ok {
		t.Fatalf("Content-Location: %q", loc)
	}
	return id
}

func httptestRequest(method, path, body string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/fhir+json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return req
}

func serve(h http.Handler, req *http.Request) *http.Response {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Result()
}

func TestAsyncBatchOffRunsSynchronously(t *testing.T) {
	r := newRouter(t, fhirrouter.NewMemoryRepository(), fhirrouter.Options{})
	code, body := putTo(r.HTTPHandler(), "POST", "", asyncBundle, "Prefer", "respond-async")
	if code != 200 || !strings.Contains(body, `"batch-response"`) {
		t.Fatalf("tắt AsyncBatch thì chạy đồng bộ: %d %s", code, body)
	}
}

func TestAsyncBatchMatchesSync(t *testing.T) {
	syncRepo := fhirrouter.NewMemoryRepository()
	code, body := putTo(newRouter(t, syncRepo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler(), "POST", "", asyncBundle)
	if code != 200 {
		t.Fatal(code, body)
	}
	var want fhir.Bundle
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}

	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{AsyncBatch: true, UpdateAsCreate: true, BatchCheckpointEntries: 2})
	h := r.HTTPHandler()
	req := httptestRequest("POST", "/", asyncBundle, "Prefer", "respond-async")
	w := serve(h, req)
	if w.StatusCode != 202 {
		t.Fatalf("muốn 202, được %d", w.StatusCode)
	}
	id := jobIDFrom(t, w)
	_, got := waitJob(t, repo, id)
	if g, w := strings.Join(entryStatuses(got), ","), strings.Join(entryStatuses(&want), ","); g != w {
		t.Fatalf("trạng thái entry khác chạy đồng bộ:\n async %s\n sync  %s", g, w)
	}
	if got.Type != "batch-response" || got.Entry[0].Response.Location == "" || got.Entry[0].Resource == nil {
		t.Errorf("Bundle kết quả: %+v", got.Entry[0])
	}
	if n := count(t, repo, "Patient", ""); n != 4 {
		t.Errorf("số Patient = %d, muốn 4", n)
	}
	// chỉ còn Binary kết quả (đầu vào và các đoạn đã dọn)
	if n := count(t, repo, "Binary", ""); n != 1 {
		t.Errorf("số Binary = %d, muốn 1", n)
	}
	if code, _ := putTo(h, "GET", "job/"+id+"/status", ""); code != 200 {
		t.Errorf("GET status: %d", code)
	}
}

func TestAsyncBatchRejectsBeforeCreatingJob(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	h := newRouter(t, repo, fhirrouter.Options{AsyncBatch: true}).HTTPHandler()
	for name, body := range map[string]string{
		"transaction":       strings.Replace(asyncBundle, `"batch"`, `"transaction"`, 1),
		"entry thiếu url":   `{"resourceType":"Bundle","type":"batch","entry":[{"request":{"method":"GET"}}]}`,
		"không phải Bundle": `{"resourceType":"Patient"}`,
	} {
		if code, out := putTo(h, "POST", "", body, "Prefer", "respond-async"); code != 400 {
			t.Errorf("%s: muốn 400, được %d %s", name, code, out)
		}
	}
	if n := count(t, repo, "AsyncJob", ""); n != 0 {
		t.Errorf("không được tạo AsyncJob, có %d", n)
	}
}

// Job bị gián đoạn sau checkpoint đầu (2 entry) và sau khi entry thứ 5 (POST, id cấp sẵn) đã ghi: chạy lại thì giữ kết quả
// đã lưu, chạy tiếp từ entry 3 và không tạo bản trùng của POST.
func TestAsyncBatchResumesFromCheckpoint(t *testing.T) {
	ctx := context.Background()
	repo := fhirrouter.NewMemoryRepository()
	mustCreate := func(res fhir.Resource) {
		t.Helper()
		if _, err := repo.CreateResource(ctx, res); err != nil {
			t.Fatal(err)
		}
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(asyncBundle), &body)
	data, _ := json.Marshal(body)
	mustCreate(&fhir.Binary{ID: "in", ContentType: "application/fhir+json", Data: base64.StdEncoding.EncodeToString(data)})
	saved, _ := json.Marshal([]fhir.BundleEntry{
		{Response: &fhir.BundleEntryResponse{Status: "201 Created", Location: "Patient/p0/_history/1"}},
		{Response: &fhir.BundleEntryResponse{Status: "201 Created", Location: "Patient/p1/_history/1"}},
	})
	mustCreate(&fhir.Binary{ID: "chunk0", ContentType: "application/fhir+json", Data: base64.StdEncoding.EncodeToString(saved)})
	mustCreate(&fhir.Patient{ID: "pre4", Name: []fhir.HumanName{{Family: "B"}}}) // entry 5 đã ghi trước khi tiến trình chết
	state := `{"position":2,"chunks":["chunk0"],"ids":["pre0","","","","pre4"]}`
	_, err := repo.CreateResource(ctx, &fhir.AsyncJob{ID: "j", Status: "accepted", Request: "batch?input=in",
		RequestTime: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		Extension:   []fhir.Extension{{URL: fhirrouter.JobStateURL, ValueString: state}}})
	if err != nil {
		t.Fatal(err)
	}

	r := newRouter(t, repo, fhirrouter.Options{AsyncBatch: true, UpdateAsCreate: true, JobRecoveryGrace: time.Millisecond})
	if _, err := r.RecoverJobs(ctx); err != nil {
		t.Fatal(err)
	}
	_, got := waitJob(t, repo, "j")
	if g := strings.Join(entryStatuses(got), ","); g != "201 Created,201 Created,404 Not Found,201 Created,201 Created" {
		t.Fatalf("kết quả: %s", g)
	}
	if got.Entry[0].Response.Location != "Patient/p0/_history/1" {
		t.Errorf("kết quả đã lưu ở checkpoint phải giữ nguyên: %+v", got.Entry[0].Response)
	}
	if n := count(t, repo, "Patient", "family=B"); n != 1 {
		t.Errorf("POST chạy lại tạo bản trùng: %d Patient B", n)
	}
	if n := count(t, repo, "Patient", "_id=pre0"); n != 0 {
		t.Errorf("entry trước checkpoint không được chạy lại")
	}
	for _, id := range []string{"in", "chunk0"} {
		if _, err := repo.ReadResource(ctx, "Binary", id); err == nil {
			t.Errorf("Binary/%s phải bị dọn", id)
		}
	}
}

// blockingRepo chặn việc tạo Observation tới khi ctx bị hủy.
type blockingRepo struct {
	*fhirrouter.MemoryRepository
	reached chan struct{}
}

func (b *blockingRepo) CreateResource(ctx context.Context, res fhir.Resource) (fhir.Resource, error) {
	if res.GetResourceType() == "Observation" {
		b.reached <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return b.MemoryRepository.CreateResource(ctx, res)
}

func TestAsyncBatchCancelWhileRunning(t *testing.T) {
	repo := &blockingRepo{MemoryRepository: fhirrouter.NewMemoryRepository(), reached: make(chan struct{}, 1)}
	r := newRouter(t, repo, fhirrouter.Options{AsyncBatch: true, BatchCheckpointEntries: 1, JobLease: time.Minute})
	h := r.HTTPHandler()
	bundle := `{"resourceType":"Bundle","type":"batch","entry":[
	 {"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient"}},
	 {"request":{"method":"POST","url":"Observation"},"resource":{"resourceType":"Observation","status":"final","code":{"text":"x"}}},
	 {"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient"}}]}`
	w := serve(h, httptestRequest("POST", "/", bundle, "Prefer", "respond-async"))
	if w.StatusCode != 202 {
		t.Fatal(w.StatusCode)
	}
	id := jobIDFrom(t, w)
	select {
	case <-repo.reached:
	case <-time.After(3 * time.Second):
		t.Fatal("job không chạy tới entry Observation")
	}
	if code, out := putTo(h, "DELETE", "job/"+id+"/status", ""); code != 202 {
		t.Fatal(code, out)
	}
	waitFor(t, "dọn Binary tạm", func() bool { return count(t, repo, "Binary", "") == 0 })
	if s := jobOf(t, repo, id).Status; s != "cancelled" {
		t.Errorf("status = %s", s)
	}
	if n := count(t, repo, "Patient", ""); n != 1 {
		t.Errorf("entry sau khi hủy không được chạy: %d Patient", n)
	}
	// hủy lần nữa: job đã dừng nên bị xóa
	if code, _ := putTo(h, "DELETE", "job/"+id+"/status", ""); code != 202 {
		t.Fatal(code)
	}
	if n := count(t, repo, "AsyncJob", ""); n != 0 {
		t.Errorf("AsyncJob phải bị xóa")
	}
}

func TestAsyncBatchExpires(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{AsyncBatch: true, ExportRetention: time.Millisecond})
	w := serve(r.HTTPHandler(), httptestRequest("POST", "/", asyncBundle, "Prefer", "respond-async"))
	id := jobIDFrom(t, w)
	waitJob(t, repo, id)
	time.Sleep(20 * time.Millisecond)
	if n, err := r.ExpireJobs(context.Background()); err != nil || n != 1 {
		t.Fatalf("ExpireJobs = %d, %v", n, err)
	}
	if n := count(t, repo, "Binary", ""); n != 0 {
		t.Errorf("Binary kết quả phải bị xóa, còn %d", n)
	}
}
