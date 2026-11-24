package fhirrouter

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

func leaseActive(job *fhir.AsyncJob, now time.Time) bool {
	if job.TransactionTime == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, job.TransactionTime)
	return err == nil && t.After(now)
}

func (r *Router) leaseDeadline() string {
	return time.Now().Add(r.opts.JobLease).UTC().Format(time.RFC3339Nano) // có phần thập phân: lease ngắn hơn 1 giây vẫn chính xác
}

// claimJob giành quyền chạy job bằng cập nhật có điều kiện (If-Match): chỉ một replica thắng.
func (r *Router) claimJob(ctx context.Context, repo Repository, job *fhir.AsyncJob) bool {
	c := *job
	c.TransactionTime = r.leaseDeadline()
	vid := ""
	if job.Meta != nil {
		vid = job.Meta.VersionId
	}
	if _, err := repo.UpdateResource(ctx, &c, UpdateOptions{IfMatch: vid}); err != nil {
		if e, ok := err.(*Error); !ok || e.Status != 412 {
			slog.WarnContext(ctx, "không giành được lease của job", "job_id", job.ID, "error", err)
		}
		return false
	}
	return true
}

// heartbeat gia hạn lease định kỳ và hủy job (cancelRun) khi AsyncJob bị đánh dấu cancelled (kể cả từ replica khác).
// Hàm trả về chỉ dừng khi goroutine đã thoát.
func (r *Router) heartbeat(ctx context.Context, repo Repository, id string, cancelRun context.CancelFunc) (stop func()) {
	every := r.opts.JobLease / 3
	if every < 10*time.Millisecond {
		every = 10 * time.Millisecond
	}
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
			}
			res, err := repo.ReadResource(ctx, "AsyncJob", id)
			if err != nil {
				continue
			}
			job, ok := res.(*fhir.AsyncJob)
			if !ok {
				continue
			}
			if job.Status == "cancelled" {
				cancelRun()
				return
			}
			if job.Status == "accepted" {
				job.TransactionTime = r.leaseDeadline()
				vid := ""
				if job.Meta != nil {
					vid = job.Meta.VersionId
				}
				_, _ = repo.UpdateResource(ctx, job, UpdateOptions{IfMatch: vid})
			}
		}
	}()
	var once bool
	return func() {
		if !once {
			once = true
			close(done)
		}
		<-exited
	}
}

// outputBinaryIDs liệt kê id Binary trong Output của job: url trong part của bulk export, và valueReference Binary/<id>
// (kết quả batch async).
func outputBinaryIDs(out *fhir.Parameters) []string {
	if out == nil {
		return nil
	}
	var ids []string
	for _, p := range out.Parameter {
		if p.ValueReference != nil {
			if id, ok := strings.CutPrefix(p.ValueReference.Reference, "Binary/"); ok {
				ids = append(ids, id)
			}
		}
		for _, part := range p.Part {
			if part.Name == "url" {
				if i := strings.LastIndex(part.ValueUri, "/Binary/"); i >= 0 {
					ids = append(ids, part.ValueUri[i+len("/Binary/"):])
				}
			}
		}
	}
	return ids
}

// jobBinaryIDs liệt kê mọi Binary của job: kết quả (outputBinaryIDs), và với batch async cả Bundle đầu vào cùng các đoạn
// kết quả tạm còn ghi trong checkpoint (job bị hủy trước khi chạy hoặc dừng giữa chừng).
func jobBinaryIDs(job *fhir.AsyncJob) []string {
	ids := outputBinaryIDs(job.Output)
	kind, query, _ := strings.Cut(job.Request, "?")
	if kind != batchJobKind {
		return ids
	}
	if q, err := url.ParseQuery(query); err == nil && q.Get("input") != "" {
		ids = append(ids, q.Get("input"))
	}
	for _, e := range job.Extension {
		var st batchState
		if e.URL == JobStateURL && json.Unmarshal([]byte(e.ValueString), &st) == nil {
			ids = append(ids, st.Chunks...)
		}
	}
	return ids
}

// deleteBinaries xóa file kết quả của job. Ưu tiên Expunge (xóa hẳn dòng, lịch sử và nội dung ở kho đối tượng) vì xóa mềm giữ nguyên nội dung: kết quả export
// nằm trong CSDL (base64) hoặc kho đối tượng sẽ không bao giờ được giải phóng. Repository không hỗ trợ hoặc người gọi không đủ quyền Expunge thì xóa mềm.
func deleteBinaries(ctx context.Context, repo Repository, ids []string) {
	ex, canExpunge := repo.(Expunger)
	for _, id := range ids {
		if canExpunge {
			err := ex.Expunge(ctx, "Binary", id)
			if err == nil {
				continue
			}
			if e, ok := err.(*Error); ok && (e.Status == 404 || e.Status == 410) {
				continue
			}
		}
		if err := repo.DeleteResource(ctx, "Binary", id); err != nil {
			if e, ok := err.(*Error); !ok || (e.Status != 404 && e.Status != 410) {
				slog.WarnContext(ctx, "không xóa được file kết quả", "binary", id, "error", err)
			}
		}
	}
}

// RecoverJobs đưa lại vào hàng đợi các job "accepted" mà lease đã hết hạn (process chết giữa chừng, hoặc job chưa
// từng được nhận) và đủ cũ (JobRecoveryGrace). Việc nhận job vẫn qua lease nên gọi nhiều lần hoặc từ nhiều replica
// không làm job chạy trùng. Trả về số job đã đưa vào hàng đợi.
func (r *Router) RecoverJobs(ctx context.Context) (int, error) {
	repo := r.opts.JobRepository
	n := 0
	for offset := 0; ; {
		sr, err := fhirsearch.Parse("AsyncJob", url.Values{"status": {"accepted"}, "_sort": {"_lastUpdated"}})
		if err != nil {
			return n, err
		}
		sr.Count, sr.Offset, sr.Total = 100, offset, "none"
		res, err := repo.Search(ctx, sr)
		if err != nil {
			return n, err
		}
		now := time.Now()
		for _, m := range res.Matches {
			job, ok := m.(*fhir.AsyncJob)
			if !ok || leaseActive(job, now) {
				continue
			}
			if t, err := time.Parse(time.RFC3339, job.RequestTime); err == nil && now.Sub(t) < r.opts.JobRecoveryGrace {
				continue
			}
			if err := r.jobs.orch.Submit(ctx, job.ID); err != nil {
				return n, err
			}
			n++
		}
		if !res.More {
			return n, nil
		}
		offset += 100
	}
}

// ExpireJobs xóa kết quả bulk export và batch async (AsyncJob và các Binary) đã quá ExportRetention. Trả về số job đã xóa.
func (r *Router) ExpireJobs(ctx context.Context) (int, error) {
	if r.opts.ExportRetention < 0 {
		return 0, nil
	}
	repo := r.opts.JobRepository
	cutoff := time.Now().Add(-r.opts.ExportRetention).UTC().Format("2006-01-02T15:04:05.000Z")
	n := 0
	for page := 0; page < 1000; page++ {
		sr, err := fhirsearch.Parse("AsyncJob", url.Values{"status": {"completed,error,cancelled"}, "_lastUpdated": {"lt" + cutoff}})
		if err != nil {
			return n, err
		}
		sr.Count, sr.Total = 100, "none"
		res, err := repo.Search(ctx, sr)
		if err != nil {
			return n, err
		}
		removed := 0
		for _, m := range res.Matches {
			job, ok := m.(*fhir.AsyncJob)
			if !ok || !expirableJob(job) {
				continue
			}
			deleteBinaries(ctx, repo, jobBinaryIDs(job))
			if err := repo.DeleteResource(ctx, "AsyncJob", job.ID); err == nil {
				removed++
			}
		}
		n += removed
		if removed == 0 || !res.More && len(res.Matches) <= removed {
			return n, nil
		}
	}
	return n, nil
}

// expirableJob cho biết job thuộc loại do router này tạo và dọn được khi hết hạn: export, batch async và reindex. Loại khác (không biết) không bị đụng tới.
// Job reindex không có file kết quả nhưng vẫn là một dòng AsyncJob: không dọn thì mỗi `$reindex` cả loại để lại một dòng mãi mãi.
func expirableJob(job *fhir.AsyncJob) bool {
	for _, p := range []string{"export?", batchJobKind + "?", "reindex?"} {
		if strings.HasPrefix(job.Request, p) {
			return true
		}
	}
	return false
}

// startMaintenance chạy RecoverJobs và ExpireJobs ngay khi khởi động rồi định kỳ cho tới Shutdown.
func (r *Router) startMaintenance() {
	every := r.opts.JobMaintenanceInterval
	if every <= 0 {
		return
	}
	r.jobs.maintDone = make(chan struct{})
	go func() {
		defer close(r.jobs.maintDone)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), max(every, 30*time.Second))
			if r.opts.DisableJobExecution {
				// không chạy job ở đây: không tiếp quản job mồ côi
			} else if n, err := r.RecoverJobs(ctx); err != nil {
				slog.Warn("khôi phục job lỗi", "error", err)
			} else if n > 0 {
				slog.Info("đã đưa lại job mồ côi vào hàng đợi", "count", n)
			}
			if n, err := r.ExpireJobs(ctx); err != nil {
				slog.Warn("xóa kết quả export hết hạn lỗi", "error", err)
			} else if n > 0 {
				slog.Info("đã xóa kết quả export hết hạn", "count", n)
			}
			cancel()
			select {
			case <-time.After(every):
			case <-r.jobs.stop:
				return
			}
		}
	}()
}
