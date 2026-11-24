package fhirrouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
)

// Batch async (như S/async-batch.ts và S/workers/batch.ts của Medplum): POST Bundle batch vào gốc FHIR kèm
// "Prefer: respond-async" khi Options.AsyncBatch bật thì trả 202 + Content-Location job/{id}/status, và Bundle chạy trong
// job nền "batch" với quyền của người gọi. Kết quả là Binary chứa Bundle batch-response, AsyncJob.output =
// Parameters{results: Reference(Binary)}.
//
// Ngữ nghĩa ít nhất một lần cho mỗi entry: job lưu checkpoint (vị trí và kết quả từng đoạn) mỗi BatchCheckpointEntries entry
// hoặc BatchCheckpointInterval; bị gián đoạn thì chạy tiếp từ checkpoint cuối, các entry sau checkpoint đó chạy lại. POST
// không điều kiện được cấp id từ lần chạy đầu nên chạy lại không tạo bản trùng.

const batchJobKind = "batch"

// batchState là trạng thái checkpoint của job batch (lưu ở AsyncJob, xem JobStateURL).
type batchState struct {
	Position int      `json:"position"`         // số entry đã có kết quả trong Chunks
	Chunks   []string `json:"chunks,omitempty"` // id Binary chứa kết quả từng đoạn, theo thứ tự
	IDs      []string `json:"ids"`              // id cấp sẵn cho POST không điều kiện, theo vị trí entry ("" nếu không có)
}

func (r *Router) registerAsyncBatch() {
	r.RegisterScopedJob(batchJobKind, r.batchJob)
	r.Add("DELETE", "job/:id/status", r.jobCancel)
}

func wantsAsync(req *Request) bool {
	return strings.Contains(strings.ToLower(req.Header.Get("Prefer")), "respond-async")
}

// startAsyncBatch lưu Bundle đầu vào thành Binary (cùng project với AsyncJob) rồi đưa job vào hàng đợi.
func (r *Router) startAsyncBatch(ctx context.Context, body map[string]any) (*Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, BadRequest("Bundle không mã hóa được: " + err.Error())
	}
	inputID := newID()
	job, err := r.startJob(ctx, batchJobKind, url.Values{"input": {inputID}}, func(job *fhir.AsyncJob) error {
		_, err := r.opts.JobRepository.CreateResource(ctx, &fhir.Binary{ID: inputID, ContentType: "application/fhir+json",
			Meta: &fhir.Meta{Project: projectOf(job)}, Data: base64.StdEncoding.EncodeToString(data), SecurityContext: ownerRef(job)})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Response{
		Status:   202,
		Resource: informational("Accepted"),
		Header:   map[string]string{"Content-Location": r.baseWithSlash() + "job/" + job.ID + "/status"},
	}, nil
}

func projectOf(res fhir.Resource) string {
	if m := res.GetMeta(); m != nil {
		return m.Project
	}
	return ""
}

// batchJob chạy Bundle batch đã lưu. Entry chạy qua Handle với repository của router và context của job (đã mang quyền
// của người gọi), nên quyền, tách project và kho Binary giống hệt batch đồng bộ. Binary tạm và kết quả ghi bằng
// JobRepository (gắn project của AsyncJob) để người gọi không cần quyền tạo Binary.
func (r *Router) batchJob(ctx context.Context, _ Repository, in *Input) (out *fhir.Parameters, err error) {
	repo := r.opts.JobRepository
	job, err := r.readJob(ctx, in.JobID)
	if err != nil {
		return nil, err
	}
	project := projectOf(job)
	inputID := in.String("input")
	bundle, err := r.readJSONBinary(ctx, inputID)
	if err != nil {
		return nil, fmt.Errorf("đọc Bundle đầu vào: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(bundle, &body); err != nil {
		return nil, fmt.Errorf("Bundle đầu vào hỏng: %w", err)
	}
	raw, _ := body["entry"].([]any)
	entries := make([]*entryRequest, len(raw))
	for i, e := range raw {
		em, _ := e.(map[string]any)
		if entries[i], err = parseEntry(i, em); err != nil {
			return nil, err // đã kiểm lúc nhận nên chỉ xảy ra khi Binary bị sửa
		}
	}

	var st batchState
	saved, err := r.JobState(ctx, in.JobID)
	if err != nil {
		return nil, err
	}
	if saved != "" {
		if err := json.Unmarshal([]byte(saved), &st); err != nil || len(st.IDs) != len(entries) || st.Position > len(entries) {
			return nil, fmt.Errorf("trạng thái checkpoint của job hỏng")
		}
	} else {
		st.IDs = make([]string, len(entries))
		for i, e := range entries {
			if e.method == "POST" && e.ifNoneExist == "" {
				st.IDs[i] = newID()
			}
		}
		if err := r.saveBatchState(ctx, in.JobID, &st); err != nil {
			return nil, err
		}
	}
	for i, id := range st.IDs {
		entries[i].preID = id
	}

	// Lỗi hoặc bị hủy: dọn Binary tạm. Tắt êm (ctx hủy mà job không bị hủy): giữ lại để chạy tiếp.
	defer func() {
		if err == nil {
			return
		}
		bg := context.WithoutCancel(ctx)
		if ctx.Err() != nil && !errors.Is(err, ErrJobCancelled) {
			if j, e := r.readJob(bg, in.JobID); e == nil && j.Status != "cancelled" {
				return
			}
		}
		deleteBinaries(bg, repo, append(st.Chunks, inputID))
	}()

	every, interval := r.opts.BatchCheckpointEntries, r.opts.BatchCheckpointInterval
	var pending []fhir.BundleEntry
	last := time.Now()
	flush := func(c context.Context) error {
		if len(pending) == 0 {
			return nil
		}
		id, err := r.writeJSONBinary(c, project, ownerRef(job), pending)
		if err != nil {
			return err
		}
		st.Chunks = append(st.Chunks, id)
		st.Position += len(pending)
		pending, last = nil, time.Now()
		return r.saveBatchState(c, in.JobID, &st)
	}
	outer := &Request{Method: "POST", Header: map[string][]string{}}
	for i := st.Position; i < len(entries); i++ {
		if ctx.Err() != nil {
			break
		}
		res := r.runAsyncEntry(ctx, outer, entries[i])
		if ctx.Err() != nil {
			break // kết quả có thể do bị hủy giữa chừng: không ghi, lần sau chạy lại entry này
		}
		pending = append(pending, res)
		if len(pending) >= every || time.Since(last) >= interval {
			if err := flush(ctx); err != nil {
				return nil, err
			}
		}
	}
	if ctx.Err() != nil {
		if err := flush(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, ErrJobCancelled) {
			slog.WarnContext(ctx, "batch async: không lưu được checkpoint khi dừng", "job_id", in.JobID, "error", err)
		}
		return nil, ctx.Err()
	}
	if err := flush(ctx); err != nil {
		return nil, err
	}

	result := &fhir.Bundle{Type: "batch-response", Timestamp: time.Now().UTC().Format(time.RFC3339)}
	for _, id := range st.Chunks {
		data, err := r.readJSONBinary(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("đọc kết quả đoạn: %w", err)
		}
		var part []fhir.BundleEntry
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, fmt.Errorf("kết quả đoạn hỏng: %w", err)
		}
		result.Entry = append(result.Entry, part...)
	}
	resultID, err := r.writeJSONBinary(ctx, project, ownerRef(job), result)
	if err != nil {
		return nil, err
	}
	deleteBinaries(context.WithoutCancel(ctx), repo, append(st.Chunks, inputID))
	st.Chunks = nil
	return &fhir.Parameters{Parameter: []fhir.ParametersParameter{
		{Name: "results", ValueReference: &fhir.Reference{Reference: "Binary/" + resultID}},
	}}, nil
}

// runAsyncEntry chạy một entry như batch đồng bộ, trừ POST không điều kiện dùng id cấp sẵn: entry chạy lại sau gián đoạn
// thấy resource đã tạo thì trả lại nó thay vì tạo bản thứ hai.
func (r *Router) runAsyncEntry(ctx context.Context, outer *Request, e *entryRequest) fhir.BundleEntry {
	if e.preID == "" {
		return r.runEntry(ctx, outer, e)
	}
	rt := strings.Trim(e.pathPart, "/")
	res, err := func() (*Response, error) {
		if fhir.IsResourceType(rt) {
			existing, err := r.repoOf(ctx).ReadResource(ctx, rt, e.preID)
			switch {
			case err == nil:
				return r.resourceResponse(201, existing), nil
			case asError(err).Status == 410: // đã tạo rồi bị entry sau xóa
				return &Response{Status: 201}, nil
			}
		}
		return r.postWithID(ctx, outer, e)
	}()
	if err != nil {
		er := asError(err)
		return fhir.BundleEntry{Response: &fhir.BundleEntryResponse{Status: statusLine(er.Status), Outcome: er.Outcome}}
	}
	return r.entryResult(res)
}

func (r *Router) saveBatchState(ctx context.Context, jobID string, st *batchState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return r.SaveJobState(ctx, jobID, string(data))
}

func (r *Router) writeJSONBinary(ctx context.Context, project string, owner *fhir.Reference, v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	b := &fhir.Binary{ID: newID(), ContentType: "application/fhir+json", Data: base64.StdEncoding.EncodeToString(data), SecurityContext: owner}
	if project != "" {
		b.Meta = &fhir.Meta{Project: project}
	}
	if _, err := r.opts.JobRepository.CreateResource(ctx, b); err != nil {
		return "", fmt.Errorf("ghi Binary: %w", err)
	}
	return b.ID, nil
}

func (r *Router) readJSONBinary(ctx context.Context, id string) ([]byte, error) {
	res, err := r.opts.JobRepository.ReadResource(ctx, "Binary", id)
	if err != nil {
		return nil, err
	}
	b, ok := res.(*fhir.Binary)
	if !ok {
		return nil, fmt.Errorf("Binary/%s không hợp lệ", id)
	}
	if b.Data == "" && r.opts.BinaryContent != nil {
		return r.opts.BinaryContent(ctx, b) // nội dung đã ra kho đối tượng (Options.BinaryContent)
	}
	return base64.StdEncoding.DecodeString(b.Data)
}

// jobCancel: DELETE job/{id}/status cho job batch async (bulk export dùng bulkdata/{id}/status). Job đang chạy bị hủy và
// dừng ở entry kế tiếp, kết quả tạm bị xóa; job đã xong, lỗi hoặc đã hủy được xóa cùng Binary kết quả. Trả 202.
func (r *Router) jobCancel(ctx context.Context, req *Request) (*Response, error) {
	return r.cancelJob(ctx, req.Params["id"], batchJobKind+"?")
}

// cancelJob hủy hoặc xóa AsyncJob có request bắt đầu bằng prefix (khác prefix thì 404, như không tồn tại).
func (r *Router) cancelJob(ctx context.Context, id, prefix string) (*Response, error) {
	repo := r.repoOf(ctx)
	res, err := repo.ReadResource(ctx, "AsyncJob", id)
	if err != nil {
		return nil, err
	}
	job, ok := res.(*fhir.AsyncJob)
	if !ok || !strings.HasPrefix(job.Request, prefix) || !r.jobVisible(ctx, job) {
		return nil, NotFound()
	}
	if job.Status == "accepted" {
		job.Status = "cancelled"
		if _, err := repo.UpdateResource(ctx, job, UpdateOptions{}); err != nil {
			return nil, err
		}
		r.jobs.mu.RLock()
		cancel := r.jobs.cancels[job.ID]
		r.jobs.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		return &Response{Status: 202, Resource: informational("Cancelled")}, nil
	}
	// Người gọi đã qua kiểm chủ sở hữu (jobVisible) nên được dọn kết quả của chính job này, dù token chỉ có quyền read: xóa bằng repository của job (id lấy từ
	// output của job, không do người gọi chọn), nếu không file bị bỏ lại mà không ai dọn được.
	deleteBinaries(ctx, r.jobRepo(), jobBinaryIDs(job))
	if err := repo.DeleteResource(ctx, "AsyncJob", job.ID); err != nil {
		return nil, err
	}
	return &Response{Status: 202, Resource: informational("Deleted")}, nil
}
