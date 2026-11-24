package fhirrouter

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
)

type ctxKey struct{}

// repoOf trả về repository của giao dịch đang chạy (nếu có) hoặc repository gốc.
func (r *Router) repoOf(ctx context.Context) Repository {
	if rp, ok := ctx.Value(ctxKey{}).(Repository); ok {
		return rp
	}
	return r.repo
}

// newID sinh UUID v4.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// entryRequest là phần request của một entry trong Bundle batch/transaction.
type entryRequest struct {
	method, urlStr       string
	ifMatch, ifNoneExist string
	fullURL              string
	resource             map[string]any
	pathPart, queryPart  string
	index                int    // vị trí trong Bundle gốc
	order                int    // thứ tự xử lý của transaction
	preID                string // id cấp sẵn cho POST (transaction)
	skipCreate           bool   // ifNoneExist đã khớp một resource có sẵn
	existing             fhir.Resource
}

func (r *Router) batch(ctx context.Context, req *Request) (*Response, error) {
	body, ok := req.Body.(map[string]any)
	if !ok || body["resourceType"] != "Bundle" {
		return nil, BadRequest("Body phải là Bundle")
	}
	typ, _ := body["type"].(string)
	if typ != "batch" && typ != "transaction" {
		return nil, BadRequest("Bundle.type phải là batch hoặc transaction")
	}
	raw, _ := body["entry"].([]any)
	entries := make([]*entryRequest, len(raw))
	for i, e := range raw {
		em, ok := e.(map[string]any)
		if !ok {
			return nil, BadRequest(fmt.Sprintf("entry[%d] không hợp lệ", i))
		}
		er, err := parseEntry(i, em)
		if err != nil {
			return nil, err
		}
		entries[i] = er
	}
	// Kiểm quyền từng entry TRƯỚC khi chạy bất cứ gì. Transaction và batch async: một entry bị từ chối là từ chối cả Bundle (không entry
	// nào chạy). Batch đồng bộ: entry bị từ chối nhận 403 riêng, các entry còn lại vẫn chạy.
	var denied map[int]*Error
	if authz := r.opts.AuthorizeEntry; authz != nil {
		for _, e := range entries {
			if err := authz(ctx, e.method, e.pathPart); err != nil {
				er := asError(err)
				if typ == "transaction" || (r.opts.AsyncBatch && wantsAsync(req)) {
					return nil, &Error{Status: er.Status, Outcome: withEntryContext(er.Outcome, e.index)}
				}
				if denied == nil {
					denied = map[int]*Error{}
				}
				denied[e.index] = er
			}
		}
	}
	if r.opts.AsyncBatch && wantsAsync(req) {
		if typ == "transaction" {
			return nil, BadRequest("Transaction không chạy async được (Prefer: respond-async chỉ dùng cho batch)")
		}
		return r.startAsyncBatch(ctx, body)
	}
	out := &fhir.Bundle{Type: typ + "-response", Timestamp: time.Now().UTC().Format(time.RFC3339), Entry: make([]fhir.BundleEntry, len(entries))}
	if typ == "batch" {
		for _, e := range entries {
			if er := denied[e.index]; er != nil {
				out.Entry[e.index] = fhir.BundleEntry{Response: &fhir.BundleEntryResponse{Status: statusLine(er.Status), Outcome: er.Outcome}}
				continue
			}
			out.Entry[e.index] = r.runEntry(ctx, req, e)
		}
		return &Response{Status: 200, Resource: out}, nil
	}
	tx, ok := r.repo.(Transactional)
	if !ok {
		return nil, NotImplemented("Repository không hỗ trợ transaction")
	}
	var results []fhir.BundleEntry
	err := tx.RunInTransaction(ctx, func(rp Repository) error {
		var err error
		results, err = r.runTransaction(context.WithValue(ctx, ctxKey{}, rp), req, entries)
		return err
	})
	if err != nil {
		return nil, err
	}
	out.Entry = results
	return &Response{Status: 200, Resource: out}, nil
}

func parseEntry(i int, em map[string]any) (*entryRequest, error) {
	rq, ok := em["request"].(map[string]any)
	if !ok {
		return nil, BadRequest(fmt.Sprintf("entry[%d] thiếu request", i))
	}
	method, _ := rq["method"].(string)
	u, _ := rq["url"].(string)
	if method == "" || u == "" {
		return nil, BadRequest(fmt.Sprintf("entry[%d].request cần method và url", i))
	}
	e := &entryRequest{method: strings.ToUpper(method), urlStr: u, index: i}
	e.ifMatch, _ = rq["ifMatch"].(string)
	e.ifNoneExist, _ = rq["ifNoneExist"].(string)
	e.fullURL, _ = em["fullUrl"].(string)
	e.pathPart, e.queryPart, _ = strings.Cut(strings.TrimPrefix(u, "/"), "?")
	switch res := em["resource"].(type) {
	case map[string]any:
		e.resource = res
	case nil:
	}
	return e, nil
}

// runEntry xử lý một entry của batch (mỗi entry độc lập, lỗi thành response của entry đó).
func (r *Router) runEntry(ctx context.Context, outer *Request, e *entryRequest) fhir.BundleEntry {
	res, err := r.Handle(ctx, e.toRequest(outer))
	if err != nil {
		er := asError(err)
		return fhir.BundleEntry{Response: &fhir.BundleEntryResponse{Status: statusLine(er.Status), Outcome: er.Outcome}}
	}
	return r.entryResult(res)
}

func (e *entryRequest) toRequest(outer *Request) *Request {
	q, _ := url.ParseQuery(e.queryPart)
	h := http.Header{}
	if e.ifMatch != "" {
		h.Set("If-Match", e.ifMatch)
	}
	if e.ifNoneExist != "" {
		h.Set("If-None-Exist", e.ifNoneExist)
	}
	req := &Request{Method: e.method, Path: e.pathPart, Query: q, Header: h}
	if e.method == "PATCH" {
		h.Set("Content-Type", "application/json-patch+json")
	}
	if e.resource != nil {
		req.Body = e.resource
	}
	return req
}

func statusLine(code int) string { return strconv.Itoa(code) + " " + http.StatusText(code) }

func (r *Router) entryResult(res *Response) fhir.BundleEntry {
	be := fhir.BundleEntry{Response: &fhir.BundleEntryResponse{Status: statusLine(res.Status)}}
	if res.Resource != nil && res.Status != 204 {
		be.Resource = &fhir.AnyResource{Value: res.Resource}
	}
	be.Response.Location = res.Header["Location"]
	be.Response.Etag = res.Header["ETag"]
	if lm := res.Header["Last-Modified"]; lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			be.Response.LastModified = t.UTC().Format(time.RFC3339)
		}
	}
	if res.Resource != nil && res.Status != 204 {
		if m := res.Resource.GetMeta(); m != nil && m.LastUpdated != "" {
			be.Response.LastModified = m.LastUpdated
		}
		if res.Resource.GetResourceType() != "Bundle" {
			be.FullUrl = r.fullURL(res.Resource.GetResourceType(), res.Resource.GetID())
		}
	}
	return be
}

// runTransaction xử lý transaction theo thứ tự của đặc tả (DELETE, POST, PUT/PATCH, GET)
// và thay tham chiếu urn:uuid bằng id thật. Lỗi ở entry nào thì hủy toàn bộ.
func (r *Router) runTransaction(ctx context.Context, outer *Request, entries []*entryRequest) ([]fhir.BundleEntry, error) {
	uuids := map[string]string{} // urn:uuid:... -> Type/id
	for _, e := range entries {
		if e.method != "POST" || e.resource == nil {
			continue
		}
		rt, _ := e.resource["resourceType"].(string)
		// POST có điều kiện: nếu đã có đúng một resource khớp thì dùng lại, không tạo mới
		if e.ifNoneExist != "" {
			q, err := url.ParseQuery(e.ifNoneExist)
			if err != nil {
				return nil, BadRequest("ifNoneExist không hợp lệ")
			}
			matches, err := r.conditionalMatches(ctx, rt, q)
			if err != nil {
				return nil, err
			}
			if len(matches) > 1 {
				return nil, PreconditionFailed("Nhiều resource khớp ifNoneExist")
			}
			if len(matches) == 1 {
				e.skipCreate, e.existing = true, matches[0]
				if strings.HasPrefix(e.fullURL, "urn:uuid:") {
					uuids[e.fullURL] = rt + "/" + matches[0].GetID()
				}
				continue
			}
		}
		e.preID = newID()
		if strings.HasPrefix(e.fullURL, "urn:uuid:") {
			uuids[e.fullURL] = rt + "/" + e.preID
		}
	}
	for _, e := range entries {
		if e.resource != nil {
			rewriteRefs(e.resource, uuids)
		}
		switch e.method {
		case "DELETE":
			e.order = 0
		case "POST":
			e.order = 1
		case "PUT", "PATCH":
			e.order = 2
		case "GET":
			e.order = 3
		default:
			return nil, BadRequest("method không hỗ trợ trong Bundle: " + e.method)
		}
	}
	ordered := append([]*entryRequest(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
	results := make([]fhir.BundleEntry, len(entries))
	for _, e := range ordered {
		var res *Response
		var err error
		if e.skipCreate {
			res = r.resourceResponse(200, e.existing)
		} else if e.method == "POST" && e.preID != "" {
			res, err = r.postWithID(ctx, outer, e)
		} else {
			res, err = r.Handle(ctx, e.toRequest(outer))
		}
		if err != nil {
			er := asError(err)
			return nil, &Error{Status: er.Status, Outcome: withEntryContext(er.Outcome, e.index)}
		}
		results[e.index] = r.entryResult(res)
	}
	return results, nil
}

// postWithID tạo resource với id đã cấp sẵn (để các entry khác tham chiếu được trước khi tạo).
func (r *Router) postWithID(ctx context.Context, outer *Request, e *entryRequest) (*Response, error) {
	rt := strings.Trim(e.pathPart, "/")
	if !fhir.IsResourceType(rt) {
		return nil, NotFoundMsg("Resource type không tồn tại: " + rt)
	}
	req := &Request{Method: "POST", Path: e.pathPart, Params: map[string]string{"resourceType": rt}, Header: http.Header{}, Body: e.resource}
	body, err := r.writeBody(req, rt)
	if err != nil {
		return nil, err
	}
	return r.createWithID(ctx, body, e.preID)
}

func withEntryContext(o *fhir.OperationOutcome, index int) *fhir.OperationOutcome {
	if o == nil {
		return nil
	}
	for i := range o.Issue {
		o.Issue[i].Expression = append(o.Issue[i].Expression, fmt.Sprintf("Bundle.entry[%d]", index))
	}
	return o
}

// rewriteRefs thay mọi "reference": "urn:uuid:..." đã biết bằng Type/id.
func rewriteRefs(v any, uuids map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok && k == "reference" {
				if repl, ok := uuids[s]; ok {
					x[k] = repl
				}
				continue
			}
			rewriteRefs(e, uuids)
		}
	case []any:
		for _, e := range x {
			rewriteRefs(e, uuids)
		}
	}
}
