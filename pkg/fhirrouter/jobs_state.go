package fhirrouter

import (
	"context"
	"errors"
	"time"

	"trivita-fhir/pkg/fhir"
)

// JobStateURL là url của extension trên AsyncJob giữ trạng thái checkpoint của job (valueString, do từng loại job tự định
// dạng). Job bị gián đoạn (tắt êm, replica chết) đọc lại trạng thái này khi chạy lại để tiếp tục thay vì làm lại từ đầu.
const JobStateURL = "https://trivita.ai/fhir/StructureDefinition/job-state"

// ErrJobCancelled: AsyncJob đã bị hủy (status=cancelled) khi job đọc hoặc ghi trạng thái.
var ErrJobCancelled = errors.New("job đã bị hủy")

// JobState đọc trạng thái checkpoint của job ("" nếu chưa có). Trả ErrJobCancelled nếu job đã bị hủy.
func (r *Router) JobState(ctx context.Context, jobID string) (string, error) {
	job, err := r.readJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	if job.Status == "cancelled" {
		return "", ErrJobCancelled
	}
	for _, e := range job.Extension {
		if e.URL == JobStateURL {
			return e.ValueString, nil
		}
	}
	return "", nil
}

// SaveJobState ghi trạng thái checkpoint lên AsyncJob (đọc rồi ghi có If-Match, thử lại khi heartbeat ghi xen vào). Trả
// ErrJobCancelled nếu job đã bị hủy: job nên dừng và dọn dữ liệu tạm.
func (r *Router) SaveJobState(ctx context.Context, jobID, state string) error {
	repo := r.opts.JobRepository
	for attempt := 0; ; attempt++ {
		job, err := r.readJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Status == "cancelled" {
			return ErrJobCancelled
		}
		vid := ""
		if job.Meta != nil {
			vid = job.Meta.VersionId
		}
		job.Extension = append(withoutExtension(job.Extension, JobStateURL), fhir.Extension{URL: JobStateURL, ValueString: state})
		_, err = repo.UpdateResource(ctx, job, UpdateOptions{IfMatch: vid})
		var e *Error
		if err == nil || !errors.As(err, &e) || e.Status != 412 || attempt >= 4 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		}
	}
}

func (r *Router) readJob(ctx context.Context, jobID string) (*fhir.AsyncJob, error) {
	res, err := r.opts.JobRepository.ReadResource(ctx, "AsyncJob", jobID)
	if err != nil {
		return nil, err
	}
	job, ok := res.(*fhir.AsyncJob)
	if !ok {
		return nil, NotFound()
	}
	return job, nil
}

func withoutExtension(in []fhir.Extension, url string) []fhir.Extension {
	out := in[:0:0]
	for _, e := range in {
		if e.URL != url {
			out = append(out, e)
		}
	}
	return out
}
