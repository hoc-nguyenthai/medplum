package fhirrouter

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/workflow"
)

// JobFunc là một loại job nền. Kết quả (Parameters) được ghi vào AsyncJob.output khi hoàn tất.
type JobFunc func(ctx context.Context, repo Repository, in *Input) (*fhir.Parameters, error)

// jobs quản lý các loại job và orchestrator chạy chúng. Trạng thái của job nằm trong resource AsyncJob
// (lưu qua Repository) nên orchestrator phân tán (asynq, Temporal) chạy được ở tiến trình khác dùng chung DB.
type jobs struct {
	mu        sync.RWMutex
	funcs     map[string]JobFunc
	scope     map[string]bool               // loại job chạy bằng ScopedJobRepository
	cancels   map[string]context.CancelFunc // job đang chạy trong process này
	stop      chan struct{}                 // dừng vòng bảo trì
	stopOnce  sync.Once
	maintDone chan struct{}
	orch      workflow.JobOrchestrator
}

// RegisterScopedJob đăng ký loại job chạy với quyền của người gọi (xem Options.ScopedJobRepository).
func (r *Router) RegisterScopedJob(kind string, fn JobFunc) {
	r.RegisterJob(kind, fn)
	r.jobs.mu.Lock()
	r.jobs.scope[kind] = true
	r.jobs.mu.Unlock()
}

// RegisterJob đăng ký một loại job; kind xuất hiện trong AsyncJob.request ("kind?query").
func (r *Router) RegisterJob(kind string, fn JobFunc) {
	r.jobs.mu.Lock()
	defer r.jobs.mu.Unlock()
	r.jobs.funcs[kind] = fn
}

func (r *Router) startOrchestrator() {
	run := workflow.JobRunner(r.runJob)
	if r.opts.NewOrchestrator != nil {
		r.jobs.orch = r.opts.NewOrchestrator(run)
		return
	}
	r.jobs.orch = workflow.NewInline(run, 2)
}

// Shutdown ngừng nhận job và chờ job đang chạy tới khi ctx hết hạn.
func (r *Router) Shutdown(ctx context.Context) error {
	r.jobs.stopOnce.Do(func() { close(r.jobs.stop) })
	if r.jobs.maintDone != nil {
		select {
		case <-r.jobs.maintDone:
		case <-ctx.Done():
		}
	}
	if r.jobs.orch == nil {
		return nil
	}
	return r.jobs.orch.Shutdown(ctx)
}

// StartJob tạo AsyncJob (status=accepted) và đưa vào orchestrator.
func (r *Router) StartJob(ctx context.Context, kind string, q url.Values) (*fhir.AsyncJob, error) {
	return r.startJob(ctx, kind, q, nil)
}

// startJob như StartJob; prepare (nếu có) chạy sau khi AsyncJob đã được tạo (có meta.project của người gọi) và trước khi
// đưa vào orchestrator, ví dụ để lưu dữ liệu đầu vào. prepare lỗi thì AsyncJob bị xóa.
func (r *Router) startJob(ctx context.Context, kind string, q url.Values, prepare func(*fhir.AsyncJob) error) (*fhir.AsyncJob, error) {
	r.jobs.mu.RLock()
	_, ok := r.jobs.funcs[kind]
	r.jobs.mu.RUnlock()
	if !ok {
		return nil, Internal("loại job không tồn tại: " + kind)
	}
	job := &fhir.AsyncJob{
		ID:          newID(),
		Status:      "accepted",
		RequestTime: time.Now().UTC().Format(time.RFC3339),
		Request:     kind + "?" + q.Encode(),
	}
	if r.opts.JobSeal != nil {
		sealed, err := r.opts.JobSeal(ctx, job.ID, job.Request)
		if err != nil {
			return nil, Internal("không ký được quyền của job: " + err.Error())
		}
		job.Extension = append(job.Extension, fhir.Extension{URL: JobSealURL, ValueString: sealed})
	}
	created, err := r.repo.CreateResource(ctx, job)
	if err != nil {
		return nil, err
	}
	if prepare != nil {
		if c, ok := created.(*fhir.AsyncJob); ok {
			job = c
		}
		if err := prepare(job); err != nil {
			_ = r.opts.JobRepository.DeleteResource(context.WithoutCancel(ctx), "AsyncJob", job.ID)
			return nil, err
		}
	}
	if err := r.jobs.orch.Submit(ctx, job.ID); err != nil {
		return nil, Internal("không đưa được job vào hàng đợi: " + err.Error())
	}
	return job, nil
}

func (r *Router) startJobResponse(ctx context.Context, kind string, q url.Values) (*Response, error) {
	job, err := r.StartJob(ctx, kind, q)
	if err != nil {
		return nil, err
	}
	return &Response{
		Status:   202,
		Resource: informational("Accepted"),
		Header:   map[string]string{"Content-Location": r.baseWithSlash() + "job/" + job.ID + "/status"},
	}, nil
}

// runJob là workflow.JobRunner: nạp AsyncJob, giành quyền chạy (lease), chạy hàm của loại job, ghi kết quả hoặc lỗi.
//
// Lease: trong lúc job chạy, AsyncJob.transactionTime giữ hạn của lease (job chưa hoàn tất thì trường này chưa có nghĩa
// khác) và được gia hạn định kỳ. Replica khác (hoặc lần khởi động sau) chỉ chạy lại job "accepted" khi lease đã hết hạn,
// nên job mồ côi sau khi process chết được tiếp quản (xem RecoverJobs) mà không chạy trùng.
func (r *Router) runJob(ctx context.Context, jobID string) error {
	repoJ := r.opts.JobRepository
	res, err := repoJ.ReadResource(ctx, "AsyncJob", jobID)
	if err != nil {
		return err
	}
	job, ok := res.(*fhir.AsyncJob)
	if !ok {
		return fmt.Errorf("resource %s không phải AsyncJob", jobID)
	}
	if job.Status != "accepted" || leaseActive(job, time.Now()) {
		return nil // đã xong, đã hủy, hoặc replica khác đang chạy
	}
	if !r.claimJob(ctx, repoJ, job) {
		return nil
	}
	kind, query, _ := strings.Cut(job.Request, "?")
	q, _ := url.ParseQuery(query)
	r.jobs.mu.RLock()
	fn := r.jobs.funcs[kind]
	scoped := r.jobs.scope[kind]
	r.jobs.mu.RUnlock()
	jobRepo, jobCtx := repoJ, ctx
	var openErr error
	switch {
	case r.opts.JobOpen != nil:
		var opened context.Context
		opened, openErr = r.opts.JobOpen(ctx, job, jobSeal(job))
		if openErr == nil && scoped && r.opts.ScopedJobRepository != nil {
			jobRepo, jobCtx = r.opts.ScopedJobRepository, opened
		}
	case scoped && r.opts.ScopedJobRepository != nil && r.opts.JobContext != nil:
		jobRepo, jobCtx = r.opts.ScopedJobRepository, r.opts.JobContext(ctx, job)
	}
	runCtx, cancelRun := context.WithCancel(jobCtx)
	defer cancelRun()
	r.jobs.mu.Lock()
	r.jobs.cancels[jobID] = cancelRun
	r.jobs.mu.Unlock()
	stopBeat := r.heartbeat(ctx, repoJ, jobID, cancelRun)
	defer func() {
		r.jobs.mu.Lock()
		delete(r.jobs.cancels, jobID)
		r.jobs.mu.Unlock()
	}()

	var out *fhir.Parameters
	var runErr error
	if openErr != nil {
		runErr = openErr
	} else if fn == nil {
		runErr = fmt.Errorf("loại job không tồn tại: %s", kind)
	} else {
		func() {
			defer func() {
				if p := recover(); p != nil {
					runErr = fmt.Errorf("job panic: %v", p)
				}
			}()
			in := &Input{values: map[string][]string{}, resources: map[string]map[string]any{}, JobID: jobID}
			for k, vs := range q {
				in.values[k] = vs
			}
			out, runErr = fn(runCtx, jobRepo, in)
		}()
	}
	stopBeat()

	// ghi kết quả bằng context độc lập: job đã chạy xong dù ctx của orchestrator có sắp bị hủy
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	cur, err := repoJ.ReadResource(wctx, "AsyncJob", jobID)
	if err != nil {
		return fmt.Errorf("đọc lại job %s: %w", jobID, err)
	}
	job, _ = cur.(*fhir.AsyncJob)
	switch {
	case job.Status == "cancelled": // bị hủy trong lúc chạy: bỏ kết quả
		if out != nil {
			deleteBinaries(context.WithoutCancel(jobCtx), jobRepo, outputBinaryIDs(out))
		}
		return nil
	case runErr != nil && ctx.Err() != nil:
		// process đang tắt: nhả lease để lần khởi động sau (hoặc replica khác) chạy lại, không đánh dấu lỗi
		job.TransactionTime = ""
		if _, err := repoJ.UpdateResource(wctx, job, UpdateOptions{}); err != nil {
			return fmt.Errorf("nhả lease job %s: %w", jobID, err)
		}
		return nil
	}
	job.TransactionTime = time.Now().UTC().Format(time.RFC3339)
	job.Extension = withoutExtension(job.Extension, JobStateURL) // trạng thái chỉ cần khi job còn chạy
	if runErr != nil {
		slog.ErrorContext(ctx, "job lỗi", "job_id", jobID, "kind", kind, "error", runErr)
		job.Status = "error"
		out = &fhir.Parameters{Parameter: []fhir.ParametersParameter{{
			Name: "outcome",
			Resource: &fhir.AnyResource{Value: &fhir.OperationOutcome{Issue: []fhir.OperationOutcomeIssue{{
				Severity: "error", Code: "exception", Diagnostics: runErr.Error(), Details: &fhir.CodeableConcept{Text: runErr.Error()},
			}}}},
		}}}
	} else {
		job.Status = "completed"
	}
	job.Output = out
	if _, err := repoJ.UpdateResource(wctx, job, UpdateOptions{}); err != nil {
		return fmt.Errorf("ghi kết quả job %s: %w", jobID, err)
	}
	return nil
}

// jobStatus: GET /job/{id}/status. 202 khi đang chờ hoặc chạy, 200 + AsyncJob khi xong, 500 + OperationOutcome khi lỗi.
func (r *Router) jobStatus(ctx context.Context, req *Request) (*Response, error) {
	res, err := r.repoOf(ctx).ReadResource(ctx, "AsyncJob", req.Params["id"])
	if err != nil {
		return nil, err
	}
	job, ok := res.(*fhir.AsyncJob)
	if !ok {
		return nil, NotFound()
	}
	switch job.Status {
	case "completed":
		return &Response{Status: 200, Resource: job}, nil
	case "error":
		if job.Output != nil {
			for _, p := range job.Output.Parameter {
				if p.Resource == nil {
					continue
				}
				if oc, ok := p.Resource.Value.(*fhir.OperationOutcome); ok {
					return nil, &Error{Status: 500, Outcome: oc}
				}
			}
		}
		return nil, Internal("Job lỗi")
	case "cancelled":
		return nil, Gone()
	}
	return &Response{Status: 202, Resource: job}, nil
}

// jobSeal lấy chuỗi ký của job ("" nếu không có).
func jobSeal(job *fhir.AsyncJob) string {
	for _, e := range job.Extension {
		if e.URL == JobSealURL {
			return e.ValueString
		}
	}
	return ""
}
