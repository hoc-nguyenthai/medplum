package fhirrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirvalidate"
)

// ---------------------------------------------------------------- tiện ích

func (r *Router) fullURL(rt, id string) string {
	p := rt + "/" + id
	if r.opts.BaseURL == "" {
		return p
	}
	return strings.TrimRight(r.opts.BaseURL, "/") + "/" + p
}

func versionedURL(base, rt, id, vid string) string {
	return base + rt + "/" + id + "/_history/" + vid
}

func etag(vid string) string { return `W/"` + vid + `"` }

// parseIfMatch bỏ W/"..." quanh versionId.
func parseIfMatch(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "W/")
	return strings.Trim(h, `"`)
}

func httpDate(lastUpdated string) string {
	t, err := time.Parse(time.RFC3339Nano, lastUpdated)
	if err != nil {
		return ""
	}
	return t.UTC().Format(http.TimeFormat)
}

func toMap(res fhir.Resource) (map[string]any, error) {
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func toResource(body map[string]any) (fhir.Resource, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	res, err := fhir.UnmarshalResource(raw)
	if err != nil {
		return nil, BadRequest(err.Error())
	}
	return res, nil
}

// writeBody kiểm tra body của create/update: là object, đúng resourceType, hợp lệ theo fhirvalidate.
func (r *Router) writeBody(req *Request, rt string) (map[string]any, error) {
	body, ok := req.Body.(map[string]any)
	if !ok {
		return nil, BadRequest("Body phải là một resource JSON")
	}
	if got, _ := body["resourceType"].(string); got != rt {
		return nil, BadRequest(fmt.Sprintf("resourceType không khớp: đường dẫn là %s, body là %q", rt, got))
	}
	if !r.opts.SkipValidation {
		res := fhirvalidate.Validate(body, &fhirvalidate.Options{SkipConstraints: r.opts.SkipConstraints})
		if !res.OK() {
			out := res.Outcome()
			errs := res.Errors()
			// chỉ báo lỗi (cảnh báo không chặn việc ghi)
			out.Issue = out.Issue[:0]
			for _, i := range errs {
				out.Issue = append(out.Issue, fhir.OperationOutcomeIssue{
					Severity: "error", Code: i.Code, Diagnostics: i.Message,
					Details: &fhir.CodeableConcept{Text: i.Message}, Expression: []string{i.Path},
				})
			}
			return nil, &Error{Status: 400, Outcome: out}
		}
	}
	return body, nil
}

// resourceResponse dựng phản hồi kèm ETag, Last-Modified (và Location khi tạo mới).
func (r *Router) resourceResponse(status int, res fhir.Resource) *Response {
	h := map[string]string{}
	if m := res.GetMeta(); m != nil {
		if m.VersionId != "" {
			h["ETag"] = etag(m.VersionId)
		}
		if lm := httpDate(m.LastUpdated); lm != "" {
			h["Last-Modified"] = lm
		}
		if status == 201 && m.VersionId != "" {
			h["Location"] = versionedURL(r.baseWithSlash(), res.GetResourceType(), res.GetID(), m.VersionId)
		}
	}
	return &Response{Status: status, Resource: res, Header: h}
}

func (r *Router) baseWithSlash() string {
	if r.opts.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(r.opts.BaseURL, "/") + "/"
}

// conditionalMatches thực hiện tìm kiếm cho thao tác có điều kiện; tối đa 2 kết quả để phát hiện đa khớp.
func (r *Router) conditionalMatches(ctx context.Context, rt string, q url.Values) ([]fhir.Resource, error) {
	if len(q) == 0 {
		return nil, BadRequest("Thao tác có điều kiện cần tiêu chí search")
	}
	sr, err := fhirsearch.Parse(rt, q)
	if err != nil {
		return nil, err
	}
	if !sr.HasCriteria() {
		return nil, BadRequest("Thao tác có điều kiện cần tiêu chí search")
	}
	sr.Count, sr.Total = 2, "none"
	res, err := r.searchRepo(ctx, r.repoOf(ctx), sr)
	if err != nil {
		return nil, err
	}
	return res.Matches, nil
}

// ---------------------------------------------------------------- create / read / update / delete

func (r *Router) create(ctx context.Context, req *Request) (*Response, error) {
	rt := req.Params["resourceType"]
	body, err := r.writeBody(req, rt)
	if err != nil {
		return nil, err
	}
	if cond := req.Header.Get("If-None-Exist"); cond != "" {
		q, perr := url.ParseQuery(cond)
		if perr != nil {
			return nil, BadRequest("If-None-Exist không hợp lệ")
		}
		return r.createIfNoneExist(ctx, rt, q, body)
	}
	return r.createFrom(ctx, body)
}

// withKeyLock chạy fn trong MỘT transaction và giữ khóa theo key (nếu Repository hỗ trợ KeyLocker) trước khi gọi fn: các thao tác
// "tìm rồi tạo/sửa" với cùng tiêu chí được tuần tự hóa, nên không cùng thấy "chưa có" rồi cùng tạo. Đã trong transaction (Bundle)
// thì khóa giữ đến hết transaction đó; Repository không hỗ trợ transaction thì chạy fn trực tiếp.
func (r *Router) withKeyLock(ctx context.Context, key string, fn func(ctx context.Context) (*Response, error)) (*Response, error) {
	step := func(ctx context.Context) (*Response, error) {
		if kl, ok := r.repoOf(ctx).(KeyLocker); ok {
			if err := kl.LockKey(ctx, key); err != nil {
				return nil, err
			}
		}
		return fn(ctx)
	}
	if _, inTx := ctx.Value(ctxKey{}).(Repository); inTx {
		return step(ctx)
	}
	tx, ok := r.repo.(Transactional)
	if !ok {
		return step(ctx)
	}
	var resp *Response
	err := tx.RunInTransaction(ctx, func(rp Repository) error {
		var err error
		resp, err = step(context.WithValue(ctx, ctxKey{}, rp))
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// createIfNoneExist tìm theo tiêu chí rồi tạo, khóa theo (loại, tiêu chí): hai request đồng thời với cùng tiêu chí không cùng
// thấy "chưa có" rồi cùng tạo.
func (r *Router) createIfNoneExist(ctx context.Context, rt string, q url.Values, body map[string]any) (*Response, error) {
	return r.withKeyLock(ctx, "if-none-exist:"+rt+"?"+q.Encode(), func(ctx context.Context) (*Response, error) {
		matches, err := r.conditionalMatches(ctx, rt, q)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 1:
			return r.resourceResponse(200, matches[0]), nil
		case 0:
			return r.createFrom(ctx, body)
		default:
			return nil, PreconditionFailed("Nhiều resource khớp If-None-Exist")
		}
	})
}

func (r *Router) createFrom(ctx context.Context, body map[string]any) (*Response, error) {
	return r.createWithID(ctx, body, newID())
}

func (r *Router) createWithID(ctx context.Context, body map[string]any, id string) (*Response, error) {
	body["id"] = id // server cấp id; id do client gửi bị bỏ qua
	if meta, ok := body["meta"].(map[string]any); ok {
		delete(meta, "versionId")
		delete(meta, "lastUpdated")
	}
	res, err := toResource(body)
	if err != nil {
		return nil, err
	}
	created, err := r.repoOf(ctx).CreateResource(ctx, res)
	if err != nil {
		return nil, err
	}
	return r.resourceResponse(201, created), nil
}

// notModified xử lý conditional read: If-None-Match (danh sách ETag yếu hoặc "*") thắng If-Modified-Since. Trả 304 khi
// phiên bản hiện tại trùng ETag đã có, hoặc resource không đổi từ thời điểm If-Modified-Since (độ phân giải giây).
func notModified(req *Request, res fhir.Resource) bool {
	m := res.GetMeta()
	if m == nil {
		return false
	}
	if inm := req.Header.Get("If-None-Match"); inm != "" {
		for _, tag := range strings.Split(inm, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" || (m.VersionId != "" && parseIfMatch(tag) == m.VersionId) {
				return true
			}
		}
		return false
	}
	if ims := req.Header.Get("If-Modified-Since"); ims != "" {
		since, err := http.ParseTime(ims)
		if err != nil {
			return false // ngày sai thì bỏ qua điều kiện
		}
		lu, err := time.Parse(time.RFC3339Nano, m.LastUpdated)
		return err == nil && !lu.Truncate(time.Second).After(since)
	}
	return false
}

func (r *Router) read(ctx context.Context, req *Request) (*Response, error) {
	res, err := r.repoOf(ctx).ReadResource(ctx, req.Params["resourceType"], req.Params["id"])
	if err != nil {
		return nil, err
	}
	if notModified(req, res) {
		out := r.resourceResponse(304, res)
		out.Resource = nil // 304 không có body, giữ ETag và Last-Modified
		return out, nil
	}
	if rt := req.Params["resourceType"]; rt == "Binary" {
		if raw := binaryRaw(res, req.Header.Get("Accept")); raw != nil {
			return raw, nil
		}
	}
	return r.resourceResponse(200, res), nil
}

func (r *Router) vread(ctx context.Context, req *Request) (*Response, error) {
	res, err := r.repoOf(ctx).ReadVersion(ctx, req.Params["resourceType"], req.Params["id"], req.Params["vid"])
	if err != nil {
		return nil, err
	}
	return r.resourceResponse(200, res), nil
}

func (r *Router) update(ctx context.Context, req *Request) (*Response, error) {
	rt, id := req.Params["resourceType"], req.Params["id"]
	body, err := r.writeBody(req, rt)
	if err != nil {
		return nil, err
	}
	if bid, ok := body["id"].(string); ok && bid != id {
		return nil, BadRequest("id trong body không khớp với đường dẫn")
	}
	return r.updateOrCreate(ctx, body, id, parseIfMatch(req.Header.Get("If-Match")))
}

// validID là dạng id hợp lệ của FHIR (1-64 ký tự chữ, số, '-', '.').
var validID = regexp.MustCompile(`^[A-Za-z0-9\-.]{1,64}$`)

// updateOrCreate là PUT Type/id: cập nhật, hoặc (Options.UpdateAsCreate) tạo mới với id do client chọn khi chưa có
// resource (trả 201). Resource đã xóa (410) không được tạo lại. Nếu id đã bị resource khác chiếm mà người gọi không
// thấy được (project khác) thì trả 404 như thể không tồn tại, để không lộ sự tồn tại của id đó.
func (r *Router) updateOrCreate(ctx context.Context, body map[string]any, id, ifMatch string) (*Response, error) {
	resp, err := r.updateFrom(ctx, body, id, ifMatch)
	var he *Error
	if !r.opts.UpdateAsCreate || err == nil || !errors.As(err, &he) || he.Status != 404 {
		return resp, err
	}
	if ifMatch != "" {
		return nil, err // If-Match yêu cầu một phiên bản đang có
	}
	if !validID.MatchString(id) {
		return nil, BadRequest("id không hợp lệ cho update-as-create (1-64 ký tự chữ, số, '-', '.')")
	}
	resp, err = r.createWithID(ctx, body, id)
	if errors.As(err, &he) && he.Status == 409 {
		// id vừa bị chiếm. Nếu bởi một PUT đồng thời trong cùng project thì resource đã thấy được: cập nhật nó (kết quả như
		// thể request này đến sau). Nếu vẫn không thấy (thuộc project khác) thì coi như không tồn tại.
		if resp, err = r.updateFrom(ctx, body, id, ""); err != nil && errors.As(err, &he) && he.Status == 404 {
			return nil, NotFound()
		}
	}
	return resp, err
}

func (r *Router) updateFrom(ctx context.Context, body map[string]any, id, ifMatch string) (*Response, error) {
	body["id"] = id
	res, err := toResource(body)
	if err != nil {
		return nil, err
	}
	updated, err := r.repoOf(ctx).UpdateResource(ctx, res, UpdateOptions{IfMatch: ifMatch})
	if err != nil {
		return nil, err
	}
	return r.resourceResponse(200, updated), nil
}

func (r *Router) conditionalUpdate(ctx context.Context, req *Request) (*Response, error) {
	rt := req.Params["resourceType"]
	body, err := r.writeBody(req, rt)
	if err != nil {
		return nil, err
	}
	return r.withKeyLock(ctx, "conditional-update:"+rt+"?"+req.Query.Encode(), func(ctx context.Context) (*Response, error) {
		matches, err := r.conditionalMatches(ctx, rt, req.Query)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 0:
			if id, ok := body["id"].(string); ok && id != "" {
				return nil, BadRequest("PUT có điều kiện không khớp resource nào thì không được kèm id")
			}
			return r.createFrom(ctx, body)
		case 1:
			id := matches[0].GetID()
			if bid, ok := body["id"].(string); ok && bid != id {
				return nil, BadRequest("id trong body không khớp với resource tìm thấy")
			}
			return r.updateFrom(ctx, body, id, parseIfMatch(req.Header.Get("If-Match")))
		}
		return nil, PreconditionFailed("Nhiều resource khớp tiêu chí")
	})
}

func (r *Router) delete(ctx context.Context, req *Request) (*Response, error) {
	err := r.repoOf(ctx).DeleteResource(ctx, req.Params["resourceType"], req.Params["id"])
	if e, ok := err.(*Error); ok && e.Status == 410 {
		err = nil // đã xóa: xóa lặp lại là idempotent
	}
	if err != nil {
		return nil, err
	}
	return &Response{Status: 204}, nil
}

func (r *Router) conditionalDelete(ctx context.Context, req *Request) (*Response, error) {
	rt := req.Params["resourceType"]
	matches, err := r.conditionalMatches(ctx, rt, req.Query)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return &Response{Status: 204}, nil
	case 1:
		// đã bị xóa (410) hoặc vừa biến mất giữa lúc tìm và xóa: mục tiêu "không còn resource khớp" đã đạt
		if err := r.repoOf(ctx).DeleteResource(ctx, rt, matches[0].GetID()); err != nil {
			if e, ok := err.(*Error); !ok || (e.Status != 410 && e.Status != 404) {
				return nil, err
			}
		}
		return &Response{Status: 204}, nil
	}
	return nil, PreconditionFailed("Nhiều resource khớp tiêu chí")
}

// ---------------------------------------------------------------- patch

func (r *Router) patchBody(ctx context.Context, req *Request, rt, id string) (*Response, error) {
	if ct := req.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "json-patch+json") {
		return nil, newError(415, "not-supported", "PATCH cần Content-Type application/json-patch+json")
	}
	ops, ok := req.Body.([]any)
	if !ok {
		return nil, BadRequest("Body của PATCH phải là một mảng JSON Patch")
	}
	cur, err := r.repoOf(ctx).ReadResource(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	doc, err := toMap(cur)
	if err != nil {
		return nil, Internal(err.Error())
	}
	patched, err := applyJSONPatch(doc, ops)
	if err != nil {
		return nil, BadRequest("JSON Patch không áp dụng được: " + err.Error())
	}
	pm, ok := patched.(map[string]any)
	if !ok {
		return nil, BadRequest("Kết quả của JSON Patch phải là một resource")
	}
	if got, _ := pm["resourceType"].(string); got != rt {
		return nil, BadRequest("Không được đổi resourceType bằng PATCH")
	}
	if pid, _ := pm["id"].(string); pid != id {
		return nil, BadRequest("Không được đổi id bằng PATCH")
	}
	if !r.opts.SkipValidation {
		sub := &Request{Body: pm}
		if _, err := r.writeBody(sub, rt); err != nil {
			return nil, err
		}
	}
	return r.updateFrom(ctx, pm, id, parseIfMatch(req.Header.Get("If-Match")))
}

func (r *Router) patch(ctx context.Context, req *Request) (*Response, error) {
	return r.patchBody(ctx, req, req.Params["resourceType"], req.Params["id"])
}

func (r *Router) conditionalPatch(ctx context.Context, req *Request) (*Response, error) {
	rt := req.Params["resourceType"]
	matches, err := r.conditionalMatches(ctx, rt, req.Query)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, NotFound()
	case 1:
		return r.patchBody(ctx, req, rt, matches[0].GetID())
	}
	return nil, PreconditionFailed("Nhiều resource khớp tiêu chí")
}

// ---------------------------------------------------------------- history

func (r *Router) history(ctx context.Context, req *Request) (*Response, error) {
	rt, id := req.Params["resourceType"], req.Params["id"]
	entries, err := r.repoOf(ctx).ReadHistory(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, NotFound()
	}
	b := &fhir.Bundle{Type: "history", Timestamp: time.Now().UTC().Format(time.RFC3339)}
	total := len(entries)
	b.Total = &total
	for _, e := range entries {
		be := fhir.BundleEntry{
			FullUrl: r.fullURL(rt, id),
			Request: &fhir.BundleEntryRequest{Method: e.Method, URL: rt + "/" + id},
			Response: &fhir.BundleEntryResponse{
				Status: map[string]string{"POST": "201", "PUT": "200", "DELETE": "204"}[e.Method],
				Etag:   etag(e.VersionID), LastModified: e.LastUpdated,
			},
		}
		if !e.Deleted && e.Resource != nil {
			be.Resource = &fhir.AnyResource{Value: e.Resource}
		}
		b.Entry = append(b.Entry, be)
	}
	return &Response{Status: 200, Resource: b}, nil
}

// ---------------------------------------------------------------- search

func (r *Router) searchType(ctx context.Context, req *Request) (*Response, error) {
	return r.search(ctx, req, req.Params["resourceType"])
}

func (r *Router) searchSystem(ctx context.Context, req *Request) (*Response, error) {
	return r.search(ctx, req, "")
}

func (r *Router) search(ctx context.Context, req *Request, rt string) (*Response, error) {
	sr, err := fhirsearch.Parse(rt, req.Query)
	if err != nil {
		return nil, err
	}
	if sr.Summary == "count" {
		sr.Count, sr.Total = 0, "accurate"
	}
	res, err := r.searchRepo(ctx, r.repoOf(ctx), sr)
	if err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: r.searchBundle(sr, res, rt)}, nil
}

func (r *Router) searchBundle(sr *fhirsearch.Request, res *SearchResult, rt string) *fhir.Bundle {
	b := &fhir.Bundle{Type: "searchset", Timestamp: time.Now().UTC().Format(time.RFC3339), Total: res.Total}
	path := strings.TrimRight(r.opts.BaseURL, "/")
	if rt != "" {
		path += "/" + rt
	} else if path == "" {
		path = "."
	}
	link := func(rel string, offset int) {
		b.Link = append(b.Link, fhir.BundleLink{Relation: rel, URL: path + "?" + sr.Query(offset).Encode()})
	}
	link("self", sr.Offset)
	if res.More {
		link("next", sr.Offset+sr.Count)
	}
	if sr.Offset > 0 {
		link("previous", max(0, sr.Offset-sr.Count))
	}
	add := func(rs []fhir.Resource, mode string) {
		for _, m := range rs {
			b.Entry = append(b.Entry, fhir.BundleEntry{
				FullUrl:  r.fullURL(m.GetResourceType(), m.GetID()),
				Resource: &fhir.AnyResource{Value: subset(m, sr.Elements)},
				Search:   &fhir.BundleEntrySearch{Mode: mode},
			})
		}
	}
	add(res.Matches, "match")
	add(res.Included, "include")
	return b
}

// subset áp dụng _elements: giữ resourceType, id, meta và các phần tử được chọn.
func subset(res fhir.Resource, elements []string) fhir.Resource {
	if len(elements) == 0 {
		return res
	}
	m, err := toMap(res)
	if err != nil {
		return res
	}
	keep := map[string]bool{"resourceType": true, "id": true, "meta": true}
	for _, e := range elements {
		keep[e] = true
	}
	for k := range m {
		if !keep[k] && !keep[strings.TrimPrefix(k, "_")] {
			delete(m, k)
		}
	}
	out, err := toResource(m)
	if err != nil {
		return res
	}
	return out
}
