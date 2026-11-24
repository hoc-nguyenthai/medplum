package fhirrouter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirvalidate"
)

// Reindexer là tùy chọn của Repository: lập lại chỉ mục search (khi định nghĩa search parameter đổi).
// id rỗng nghĩa là mọi resource của kiểu; trả về số resource đã xử lý.
type Reindexer interface {
	Reindex(ctx context.Context, resourceType, id string, progress func(done int)) (int, error)
}

// Expunger là tùy chọn của Repository: xóa vĩnh viễn một resource cùng toàn bộ lịch sử (không hoàn tác được).
type Expunger interface {
	Expunge(ctx context.Context, resourceType, id string) error
}

// ---------------------------------------------------------------- tham số của operation

// Input là tham số của một operation, gom từ query string và từ resource Parameters trong body.
type Input struct {
	values    map[string][]string
	resources map[string]map[string]any
	Body      map[string]any // body nguyên bản nếu là object (resource hoặc Parameters)
	JobID     string         // id của AsyncJob khi Input là đầu vào của job nền (để đọc/ghi trạng thái, xem Router.SaveJobState)
}

// ParseInput đọc tham số: query string trước, rồi Parameters.parameter[] trong body.
func ParseInput(req *Request) (*Input, error) {
	in := &Input{values: map[string][]string{}, resources: map[string]map[string]any{}}
	for k, vs := range req.Query {
		in.values[k] = append(in.values[k], vs...)
	}
	body, ok := req.Body.(map[string]any)
	if !ok {
		if req.Body != nil {
			return nil, BadRequest("Body phải là một resource hoặc Parameters")
		}
		return in, nil
	}
	in.Body = body
	if body["resourceType"] != "Parameters" {
		return in, nil
	}
	params, _ := body["parameter"].([]any)
	for i, p := range params {
		pm, ok := p.(map[string]any)
		name, _ := pm["name"].(string)
		if !ok || name == "" {
			return nil, BadRequest(fmt.Sprintf("Parameters.parameter[%d] thiếu name", i))
		}
		if res, ok := pm["resource"].(map[string]any); ok {
			in.resources[name] = res
			continue
		}
		for k, v := range pm {
			if strings.HasPrefix(k, "value") && k != "valueReference" {
				in.values[name] = append(in.values[name], fmt.Sprint(v))
			}
		}
	}
	return in, nil
}

// String trả về giá trị đầu tiên của tham số (rỗng nếu không có).
func (in *Input) String(name string) string {
	if v := in.values[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// List trả về mọi giá trị của tham số, tách thêm theo dấu phẩy (dạng _type=A,B).
func (in *Input) List(name string) []string {
	var out []string
	for _, v := range in.values[name] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// Int trả về tham số số nguyên, hoặc def nếu vắng; lỗi nếu không phải số.
func (in *Input) Int(name string, def int) (int, error) {
	s := in.String(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, BadRequest(fmt.Sprintf("Tham số %s phải là số nguyên", name))
	}
	return n, nil
}

// Resource trả về tham số kiểu resource.
func (in *Input) Resource(name string) (map[string]any, bool) {
	r, ok := in.resources[name]
	return r, ok
}

func informational(msg string) *fhir.OperationOutcome {
	return &fhir.OperationOutcome{Issue: []fhir.OperationOutcomeIssue{{
		Severity: "information", Code: "informational", Details: &fhir.CodeableConcept{Text: msg}, Diagnostics: msg,
	}}}
}

// ---------------------------------------------------------------- $validate

// validateOp: POST /{type}/$validate. Body là resource cần kiểm tra, hoặc Parameters có tham số "resource".
// Hợp lệ trả 200 + OperationOutcome (có thể kèm cảnh báo); không hợp lệ trả 400 + OperationOutcome các lỗi.
func (r *Router) validateOp(_ context.Context, req *Request) (*Response, error) {
	rt := req.Params["resourceType"]
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	doc := in.Body
	if res, ok := in.Resource("resource"); ok {
		doc = res
	}
	if doc == nil || doc["resourceType"] == "Parameters" {
		return nil, BadRequest("Thiếu resource cần kiểm tra")
	}
	if got, _ := doc["resourceType"].(string); got != rt {
		return nil, BadRequest(fmt.Sprintf("resourceType không khớp: đường dẫn là %s, resource là %q", rt, got))
	}
	res := fhirvalidate.Validate(doc, &fhirvalidate.Options{SkipConstraints: r.opts.SkipConstraints})
	if !res.OK() {
		return nil, &Error{Status: 400, Outcome: res.Outcome()}
	}
	out := res.Outcome()
	if len(res.Issues) == 0 {
		out = informational("All OK")
	}
	return &Response{Status: 200, Resource: out}, nil
}

// ---------------------------------------------------------------- Patient/$everything

const (
	everythingDefaultCount = 1000
	everythingMaxCount     = 10000
)

// everything: GET|POST /Patient/{id}/$everything. Trả Patient và mọi resource trong compartment của Patient đó.
// Tham số: _type (danh sách kiểu), _since (chỉ resource cập nhật từ thời điểm đó), _count (tối đa, mặc định 1000, trần 10000).
// Kết quả vượt _count bị cắt bớt và có một entry OperationOutcome (search.mode=outcome) cảnh báo.
func (r *Router) everything(ctx context.Context, req *Request) (*Response, error) {
	id := req.Params["id"]
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	limit, err := in.Int("_count", everythingDefaultCount)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > everythingMaxCount {
		return nil, BadRequest(fmt.Sprintf("_count phải trong khoảng 1 đến %d", everythingMaxCount))
	}
	since := in.String("_since")
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			return nil, BadRequest("_since phải là thời điểm RFC 3339, ví dụ 2024-01-31T10:00:00Z")
		}
	}
	allowed := map[string]bool{}
	for _, t := range in.List("_type") {
		if !fhir.IsResourceType(t) {
			return nil, BadRequest("_type không hợp lệ: " + t)
		}
		allowed[t] = true
	}
	repo := r.repoOf(ctx)
	patient, err := repo.ReadResource(ctx, "Patient", id)
	if err != nil {
		return nil, err
	}

	b := &fhir.Bundle{Type: "searchset", Timestamp: time.Now().UTC().Format(time.RFC3339)}
	add := func(res fhir.Resource) {
		b.Entry = append(b.Entry, fhir.BundleEntry{
			FullUrl: r.fullURL(res.GetResourceType(), res.GetID()), Resource: &fhir.AnyResource{Value: res},
			Search: &fhir.BundleEntrySearch{Mode: "match"},
		})
	}
	count := 0
	if len(allowed) == 0 || allowed["Patient"] {
		if since == "" || !patientOlderThan(patient, since) {
			add(patient)
			count++
		}
	}
	comp := fhir.PatientCompartment()
	types := make([]string, 0, len(comp))
	for t := range comp {
		if t != "Patient" && (len(allowed) == 0 || allowed[t]) {
			types = append(types, t)
		}
	}
	sort.Strings(types)
	truncated := false
collect:
	for _, t := range types {
		found := map[string]fhir.Resource{}
	collectParams:
		for _, param := range comp[t] {
			q := url.Values{param: {"Patient/" + id}}
			if since != "" {
				q.Set("_lastUpdated", "ge"+since)
			}
			sr, err := fhirsearch.Parse(t, q)
			if err != nil {
				slog.WarnContext(ctx, "compartment: bỏ qua tham số không dùng được", "type", t, "param", param, "error", err)
				continue
			}
			sr.Count, sr.Total = fhirsearch.MaxCount, "none"
			for {
				res, err := repo.Search(ctx, sr)
				if err != nil {
					// Không chỉ định _type: loại người gọi không được tìm kiếm (token giới hạn theo loại hoặc chính sách) bị bỏ qua thay vì làm hỏng cả
					// $everything, giống $export. Có chỉ định _type mà bị cấm thì vẫn lỗi để người gọi biết.
					var re *Error
					if len(allowed) == 0 && errors.As(err, &re) && re.Status == http.StatusForbidden {
						continue collectParams
					}
					return nil, err
				}
				for _, m := range res.Matches {
					found[m.GetID()] = m
				}
				if !res.More || len(found)+count > limit {
					break
				}
				sr.Offset += sr.Count
			}
		}
		ids := make([]string, 0, len(found))
		for fid := range found {
			ids = append(ids, fid)
		}
		slices.Sort(ids)
		for _, fid := range ids {
			if count >= limit {
				truncated = true
				break collect
			}
			add(found[fid])
			count++
		}
	}
	total := count
	b.Total = &total
	if truncated {
		b.Entry = append(b.Entry, fhir.BundleEntry{
			Resource: &fhir.AnyResource{Value: &fhir.OperationOutcome{Issue: []fhir.OperationOutcomeIssue{{
				Severity: "warning", Code: "too-costly",
				Diagnostics: fmt.Sprintf("Kết quả bị cắt ở %d resource; dùng _type hoặc _since để thu hẹp", limit),
				Details:     &fhir.CodeableConcept{Text: "Kết quả bị cắt bớt"},
			}}}},
			Search: &fhir.BundleEntrySearch{Mode: "outcome"},
		})
	}
	return &Response{Status: 200, Resource: b}, nil
}

func patientOlderThan(p fhir.Resource, since string) bool {
	m := p.GetMeta()
	if m == nil || m.LastUpdated == "" {
		return false
	}
	lu, err1 := time.Parse(time.RFC3339Nano, m.LastUpdated)
	s, err2 := time.Parse(time.RFC3339, since)
	return err1 == nil && err2 == nil && lu.Before(s)
}

// ---------------------------------------------------------------- $reindex, $expunge (quản trị)

// reindexType: POST /{type}/$reindex. Chạy nền (202 + Content-Location tới /job/{id}/status).
func (r *Router) reindexType(ctx context.Context, req *Request) (*Response, error) {
	if _, ok := r.repo.(Reindexer); !ok {
		return nil, NotImplemented("Repository không hỗ trợ reindex")
	}
	rt := req.Params["resourceType"]
	return r.startJobResponse(ctx, "reindex", url.Values{"resourceType": {rt}})
}

// reindexOne: POST /{type}/{id}/$reindex. Một resource nên chạy đồng bộ.
func (r *Router) reindexOne(ctx context.Context, req *Request) (*Response, error) {
	ri, ok := r.repoOf(ctx).(Reindexer)
	if !ok {
		return nil, NotImplemented("Repository không hỗ trợ reindex")
	}
	rt, id := req.Params["resourceType"], req.Params["id"]
	if _, err := r.repoOf(ctx).ReadResource(ctx, rt, id); err != nil {
		return nil, err
	}
	if _, err := ri.Reindex(ctx, rt, id, nil); err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: informational("Reindexed " + rt + "/" + id)}, nil
}

// expunge: POST /{type}/{id}/$expunge. Xóa vĩnh viễn resource và lịch sử.
func (r *Router) expunge(ctx context.Context, req *Request) (*Response, error) {
	ex, ok := r.repoOf(ctx).(Expunger)
	if !ok {
		return nil, NotImplemented("Repository không hỗ trợ expunge")
	}
	rt, id := req.Params["resourceType"], req.Params["id"]
	if err := ex.Expunge(ctx, rt, id); err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: informational("Expunged " + rt + "/" + id)}, nil
}

func (r *Router) registerOperations() {
	r.Add("POST", ":resourceType/$validate", r.validateOp)
	r.Add("GET", "Patient/:id/$everything", r.everything)
	r.Add("POST", "Patient/:id/$everything", r.everything)
	r.Add("POST", ":resourceType/$reindex", r.reindexType)
	r.Add("POST", ":resourceType/:id/$reindex", r.reindexOne)
	r.Add("POST", ":resourceType/:id/$expunge", r.expunge)
	r.Add("GET", "job/:id/status", r.jobStatus)
	r.registerBulk()
	r.registerAsyncBatch()
	r.registerTerminology()
	r.registerPackages()
	r.registerCodeSystemImport()
	r.registerConceptMapImport()
	r.registerGraphQL()
	r.RegisterJob("reindex", func(ctx context.Context, repo Repository, in *Input) (*fhir.Parameters, error) {
		ri, ok := repo.(Reindexer)
		if !ok {
			return nil, fmt.Errorf("repository không hỗ trợ reindex")
		}
		n, err := ri.Reindex(ctx, in.String("resourceType"), "", nil)
		if err != nil {
			return nil, err
		}
		return &fhir.Parameters{Parameter: []fhir.ParametersParameter{{Name: "count", ValueInteger: &n}}}, nil
	})
}
