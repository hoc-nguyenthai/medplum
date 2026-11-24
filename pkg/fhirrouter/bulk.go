package fhirrouter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Bulk Data Export (FHIR Bulk Data Access, định dạng ndjson):
//
//	GET [base]/$export            toàn hệ thống (mọi loại resource, hoặc _type)
//	GET [base]/Patient/$export    mọi Patient và resource trong compartment Patient
//	GET [base]/Group/{id}/$export Patient là thành viên đang hoạt động của Group và resource trong compartment của họ
//
// Yêu cầu có header "Prefer: respond-async". Server trả 202 + Content-Location tới
// [base]/bulkdata/{id}/status; khi xong trạng thái trả 200 kèm danh sách file. Mỗi file là một Binary
// (application/fhir+ndjson, tải bằng GET Binary/{id} với Accept: application/fhir+ndjson), giống Medplum.
//
// Job chạy với quyền của người gọi (Options.ScopedJobRepository) nên chỉ xuất dữ liệu người đó được thấy.
//
// Group/$export chỉ nhận Group liệt kê thành viên (actual=true), bỏ thành viên inactive và thành viên không phải
// Patient, tối đa maxGroupMembers thành viên.
//
// _typeFilter=Type?query (lặp tham số, hoặc nối bằng dấu phẩy ngay trước "Type?"): chỉ xuất resource của loại đó khớp điều
// kiện (OR giữa các điều kiện cùng loại, loại không có điều kiện xuất đủ); thiếu _type thì xuất đúng các loại được nêu; điều
// kiện chấp nhận mọi tham số lọc kể cả chained và :in/:below, trừ tham số điều khiển (_count, _sort, _include...).
//
// Giới hạn: phân trang bằng offset nên
// dữ liệu ghi trong lúc xuất có thể lặp hoặc sót.
const (
	ndjsonType      = "application/fhir+ndjson"
	exportPageSize  = 1000
	exportFileBytes = 8 << 20
)

// exportSkip là các loại không bao giờ xuất: nội bộ hoặc chính là file kết quả.
var exportSkip = map[string]bool{"AsyncJob": true, "Binary": true}

func (r *Router) registerBulk() {
	r.Add("GET", "$export", r.exportStart("system"))
	r.Add("GET", "Patient/$export", r.exportStart("patient"))
	r.Add("GET", "Group/:id/$export", r.exportStart("group"))
	r.Add("GET", "bulkdata/:id/status", r.exportStatus)
	r.Add("DELETE", "bulkdata/:id/status", r.exportCancel)
	r.RegisterScopedJob("export", r.exportJob)
}

func (r *Router) exportStart(level string) Handler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		if !strings.Contains(strings.ToLower(req.Header.Get("Prefer")), "respond-async") {
			return nil, BadRequest("Bulk export cần header Prefer: respond-async")
		}
		in, err := ParseInput(req)
		if err != nil {
			return nil, err
		}
		switch f := in.String("_outputFormat"); f {
		case "", "ndjson", "application/ndjson", "application/fhir+ndjson", "text/ndjson":
		default:
			return nil, BadRequest("_outputFormat chỉ hỗ trợ ndjson, nhận " + f)
		}
		for _, t := range in.List("_type") {
			if !fhir.IsResourceType(t) {
				return nil, BadRequest("_type không hợp lệ: " + t)
			}
		}
		q := url.Values{"_level": {level}}
		if level == "group" {
			// kiểm tra sớm (404/400 ngay khi gọi) bằng quyền của người gọi; job đọc lại Group khi chạy
			if _, err := r.groupPatients(ctx, r.repoOf(ctx), req.Params["id"]); err != nil {
				return nil, err
			}
			q.Set("_group", req.Params["id"])
		}
		ts := in.List("_type")
		_, ftypes, err := parseTypeFilters(in.values["_typeFilter"])
		if err != nil {
			return nil, err
		}
		for _, ft := range ftypes {
			if len(ts) > 0 && !slices.Contains(ts, ft) {
				return nil, BadRequest("_typeFilter có loại " + ft + " không nằm trong _type")
			}
		}
		if len(ts) == 0 && len(ftypes) > 0 {
			ts = ftypes // chỉ có _typeFilter: xuất đúng các loại được nêu
		}
		if len(ts) > 0 {
			q.Set("_type", strings.Join(ts, ","))
		}
		for _, qs := range in.values["_typeFilter"] {
			q.Add("_typeFilter", qs)
		}
		if since := in.String("_since"); since != "" {
			if _, err := time.Parse(time.RFC3339, since); err != nil {
				return nil, BadRequest("_since phải là instant RFC3339")
			}
			q.Set("_since", since)
		}
		job, err := r.StartJob(ctx, "export", q)
		if err != nil {
			return nil, err
		}
		return &Response{
			Status: 202, Resource: informational("Accepted"),
			Header: map[string]string{"Content-Location": r.baseWithSlash() + "bulkdata/" + job.ID + "/status"},
		}, nil
	}
}

func exportTypes(level string, in *Input) []string {
	if level == "group" {
		level = "patient" // cùng tập loại với mức patient; khác ở bộ lọc thành viên
	}
	if ts := in.List("_type"); len(ts) > 0 {
		if level == "patient" {
			comp := fhir.PatientCompartment()
			var out []string
			for _, t := range ts {
				if _, ok := comp[t]; ok || t == "Patient" {
					out = append(out, t)
				}
			}
			return out
		}
		return ts
	}
	var out []string
	if level == "patient" {
		comp := fhir.PatientCompartment()
		for _, t := range fhir.ResourceTypes {
			if _, ok := comp[t]; (ok || t == "Patient") && !exportSkip[t] {
				out = append(out, t)
			}
		}
		return out
	}
	for _, t := range fhir.ResourceTypes {
		if !exportSkip[t] {
			out = append(out, t)
		}
	}
	return out
}

// exportJob ghi mỗi loại resource thành một hoặc nhiều Binary ndjson và trả danh sách file trong Parameters.
func (r *Router) exportJob(ctx context.Context, repo Repository, in *Input) (res *fhir.Parameters, err error) {
	level := in.String("_level")
	since := in.String("_since")
	var groupIDs []string
	if level == "group" {
		if groupIDs, err = r.groupPatients(ctx, repo, in.String("_group")); err != nil {
			return nil, err
		}
	}
	var owner *fhir.Reference
	if job, jerr := r.readJob(ctx, in.JobID); jerr == nil {
		owner = ownerRef(job)
	}
	out := &fhir.Parameters{}
	defer func() { // lỗi hoặc bị hủy giữa chừng: không để lại file mồ côi
		if err != nil {
			deleteBinaries(context.WithoutCancel(ctx), repo, outputBinaryIDs(out))
		}
	}()
	var buf bytes.Buffer
	lines := 0
	flush := func(rt string) error {
		if lines == 0 {
			return nil
		}
		b := &fhir.Binary{ID: newID(), ContentType: ndjsonType, Data: base64.StdEncoding.EncodeToString(buf.Bytes()), SecurityContext: owner}
		if _, err := repo.CreateResource(ctx, b); err != nil {
			return fmt.Errorf("ghi file %s: %w", rt, err)
		}
		n := lines
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "output", Part: []fhir.ParametersParameter{
			{Name: "type", ValueCode: rt},
			{Name: "url", ValueUri: r.baseWithSlash() + "Binary/" + b.ID},
			{Name: "count", ValueInteger: &n},
		}})
		buf.Reset()
		lines = 0
		return nil
	}
	filters, _, err := parseTypeFilters(in.values["_typeFilter"])
	if err != nil {
		return nil, err
	}
typeLoop:
	for _, rt := range exportTypes(level, in) {
		bases := filters[rt]
		if len(bases) == 0 {
			bases = []url.Values{nil} // không có _typeFilter cho loại này: xuất tất cả
		}
		var reqs []*fhirsearch.Request
		for _, extra := range bases {
			q := url.Values{"_sort": {"_lastUpdated"}}
			if since != "" {
				q.Set("_lastUpdated", "ge"+since)
			}
			for k, vs := range extra { // điều kiện của _typeFilter cộng thêm (AND) vào điều kiện chung
				for _, v := range vs {
					q.Add(k, v)
				}
			}
			rs, err := exportRequests(level, rt, q, groupIDs)
			if err != nil {
				return nil, err
			}
			reqs = append(reqs, rs...)
		}
		seen := map[string]bool{} // một resource có thể thuộc compartment qua nhiều tham số
		for _, sr := range reqs {
			sr.Count, sr.Total = exportPageSize, "none"
			for offset := 0; ; offset += exportPageSize {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				sr.Offset = offset
				res, err := r.searchRepo(ctx, repo, sr)
				if err != nil {
					var he *Error
					if errors.As(err, &he) && he.Status == 403 && in.String("_type") == "" {
						buf.Reset() // không chỉ định _type: bỏ qua loại người gọi không được đọc
						lines = 0
						continue typeLoop
					}
					return nil, fmt.Errorf("tìm %s: %w", rt, err)
				}
				for _, m := range res.Matches {
					if len(reqs) > 1 {
						if seen[m.GetID()] {
							continue
						}
						seen[m.GetID()] = true
					}
					raw, err := json.Marshal(m)
					if err != nil {
						return nil, err
					}
					buf.Write(raw)
					buf.WriteByte('\n')
					lines++
					if buf.Len() >= exportFileBytes {
						if err := flush(rt); err != nil {
							return nil, err
						}
					}
				}
				if !res.More {
					break
				}
			}
		}
		if err := flush(rt); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// exportRequests dựng các search cho một loại: một search tất cả, hoặc (mức patient) mỗi tham số compartment một
// search "tham số có giá trị" (resource thuộc compartment khi bất kỳ tham số nào trỏ tới Patient).
func exportRequests(level, rt string, q url.Values, groupIDs []string) ([]*fhirsearch.Request, error) {
	if level == "group" {
		return groupRequests(rt, q, groupIDs)
	}
	if level != "patient" || rt == "Patient" {
		sr, err := fhirsearch.Parse(rt, q)
		if err != nil {
			return nil, err
		}
		return []*fhirsearch.Request{sr}, nil
	}
	var out []*fhirsearch.Request
	for _, p := range fhir.PatientCompartment()[rt] {
		if _, ok := fhirsearch.Lookup(rt, p); !ok {
			continue
		}
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set(p+":missing", "false")
		sr, err := fhirsearch.Parse(rt, qq)
		if err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, nil
}

// exportStatus: GET /bulkdata/{id}/status.
func (r *Router) exportStatus(ctx context.Context, req *Request) (*Response, error) {
	res, err := r.repoOf(ctx).ReadResource(ctx, "AsyncJob", req.Params["id"])
	if err != nil {
		return nil, err
	}
	job, ok := res.(*fhir.AsyncJob)
	if !ok || !strings.HasPrefix(job.Request, "export?") || !r.jobVisible(ctx, job) {
		return nil, NotFound()
	}
	switch job.Status {
	case "completed":
		var files []map[string]any
		if job.Output != nil {
			for _, p := range job.Output.Parameter {
				if p.Name != "output" {
					continue
				}
				f := map[string]any{}
				for _, part := range p.Part {
					switch part.Name {
					case "type":
						f["type"] = part.ValueCode
					case "url":
						f["url"] = part.ValueUri
					case "count":
						if part.ValueInteger != nil {
							f["count"] = *part.ValueInteger
						}
					}
				}
				files = append(files, f)
			}
		}
		body, _ := json.Marshal(map[string]any{
			"transactionTime":     job.TransactionTime,
			"request":             r.baseWithSlash() + "$export?" + strings.TrimPrefix(job.Request, "export?"),
			"requiresAccessToken": true,
			"output":              orEmpty(files),
			"error":               []any{},
		})
		return &Response{Status: 200, Raw: body, ContentType: "application/json"}, nil
	case "error", "cancelled":
		return r.jobStatus(ctx, req)
	}
	return &Response{Status: 202, Header: map[string]string{"X-Progress": "in-progress", "Retry-After": "2"}}, nil
}

func orEmpty(f []map[string]any) []map[string]any {
	if f == nil {
		return []map[string]any{}
	}
	return f
}

// binaryRaw trả Binary dạng file khi client yêu cầu đúng loại nội dung của nó (hoặc octet-stream); ngược lại nil.
func binaryRaw(res fhir.Resource, accept string) *Response {
	b, ok := res.(*fhir.Binary)
	if !ok || b.ContentType == "" {
		return nil
	}
	mt := strings.TrimSpace(strings.Split(accept, ",")[0])
	mt, _, _ = strings.Cut(mt, ";")
	if mt != b.ContentType && mt != "application/octet-stream" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(b.Data)
	if err != nil {
		return nil
	}
	return &Response{Status: 200, Raw: data, ContentType: b.ContentType}
}

// exportCancel: DELETE /bulkdata/{id}/status. Job đang chạy bị hủy (và bỏ kết quả); job đã xong, lỗi hoặc đã hủy
// được xóa cùng các file. Trả 202 theo đặc tả.
func (r *Router) exportCancel(ctx context.Context, req *Request) (*Response, error) {
	return r.cancelJob(ctx, req.Params["id"], "export?")
}

// exportControlParams là các tham số điều khiển search không được dùng trong _typeFilter (chỉ nhận điều kiện lọc).
var exportControlParams = map[string]bool{"_count": true, "_sort": true, "_include": true, "_revinclude": true, "_elements": true,
	"_summary": true, "_total": true, "_offset": true, "_page_token": true}

// parseTypeFilters đọc các giá trị _typeFilter ("Type?query", có thể lặp tham số hoặc nối nhau bằng dấu phẩy ngay trước
// "Type?") thành điều kiện theo loại (OR giữa các điều kiện cùng loại) và danh sách loại theo thứ tự xuất hiện.
func parseTypeFilters(values []string) (map[string][]url.Values, []string, error) {
	out := map[string][]url.Values{}
	var types []string
	for _, v := range values {
		for _, piece := range splitTypeFilter(v) {
			rt, query, ok := strings.Cut(piece, "?")
			if !ok || !fhir.IsResourceType(rt) {
				return nil, nil, BadRequest("_typeFilter không hợp lệ (cần Type?query): " + piece)
			}
			qv, err := url.ParseQuery(query)
			if err != nil {
				return nil, nil, BadRequest("_typeFilter không hợp lệ: " + piece)
			}
			for k := range qv {
				if exportControlParams[k] {
					return nil, nil, BadRequest("_typeFilter không nhận tham số điều khiển " + k)
				}
			}
			if _, err := fhirsearch.Parse(rt, qv); err != nil {
				return nil, nil, BadRequest("_typeFilter " + piece + ": " + err.Error())
			}
			if _, seen := out[rt]; !seen {
				types = append(types, rt)
			}
			out[rt] = append(out[rt], qv)
		}
	}
	return out, types, nil
}

// splitTypeFilter tách "A?x=1,B?y=2" thành từng "Type?query"; dấu phẩy trong giá trị tham số (x=1,2) được giữ nguyên.
func splitTypeFilter(v string) []string {
	var out []string
	start := 0
	for i := 0; i < len(v); i++ {
		if v[i] != ',' {
			continue
		}
		j := i + 1
		if j < len(v) && v[j] >= 'A' && v[j] <= 'Z' {
			for j < len(v) && ((v[j] >= 'A' && v[j] <= 'Z') || (v[j] >= 'a' && v[j] <= 'z')) {
				j++
			}
			if j < len(v) && v[j] == '?' {
				out = append(out, strings.TrimSpace(v[start:i]))
				start = i + 1
			}
		}
	}
	if p := strings.TrimSpace(v[start:]); p != "" {
		out = append(out, p)
	}
	return out
}

// maxGroupMembers là số thành viên Patient tối đa của một Group khi export.
const maxGroupMembers = 5000

// groupPatients trả id các Patient là thành viên đang hoạt động của Group.
func (r *Router) groupPatients(ctx context.Context, repo Repository, id string) ([]string, error) {
	res, err := repo.ReadResource(ctx, "Group", id)
	if err != nil {
		return nil, err
	}
	g, ok := res.(*fhir.Group)
	if !ok {
		return nil, NotFound()
	}
	if g.Actual != nil && !*g.Actual {
		return nil, BadRequest("Group " + id + " không liệt kê thành viên (actual=false)")
	}
	seen := map[string]bool{}
	var ids []string
	for _, m := range g.Member {
		if m.Entity == nil || (m.Inactive != nil && *m.Inactive) {
			continue
		}
		t, pid, ok := strings.Cut(fhirsearch.NormalizeReference(m.Entity.Reference), "/")
		if !ok || t != "Patient" || pid == "" || seen[pid] {
			continue
		}
		seen[pid] = true
		ids = append(ids, pid)
	}
	if len(ids) > maxGroupMembers {
		return nil, BadRequest("Group có quá nhiều thành viên Patient (> " + itoa(maxGroupMembers) + ")")
	}
	return ids, nil
}

// groupRequests dựng search cho một loại của Group: Patient theo _id, loại khác theo từng tham số compartment trỏ
// tới một trong các Patient thành viên.
func groupRequests(rt string, q url.Values, ids []string) ([]*fhirsearch.Request, error) {
	if len(ids) == 0 {
		ids = []string{noMatch}
	}
	build := func(param string, vals []string) (*fhirsearch.Request, error) {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set(param, strings.Join(vals, ","))
		return fhirsearch.Parse(rt, qq)
	}
	if rt == "Patient" {
		sr, err := build("_id", ids)
		if err != nil {
			return nil, err
		}
		return []*fhirsearch.Request{sr}, nil
	}
	refs := make([]string, len(ids))
	for i, id := range ids {
		refs[i] = "Patient/" + id
	}
	var out []*fhirsearch.Request
	for _, p := range fhir.PatientCompartment()[rt] {
		if _, ok := fhirsearch.Lookup(rt, p); !ok {
			continue
		}
		sr, err := build(p, refs)
		if err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, nil
}
