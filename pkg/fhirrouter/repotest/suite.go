// Package repotest là bộ test hành vi dùng chung cho mọi fhirrouter.Repository: chạy toàn bộ interaction REST
// qua HTTP thật (httptest) trên repository được cung cấp. Dùng trong test của từng cài đặt.
package repotest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

const base = "http://test/fhir/R4"

// Factory tạo một repository rỗng cho mỗi test.
type Factory func(t *testing.T) fhirrouter.Repository

type env struct {
	t    *testing.T
	repo fhirrouter.Repository
	r    *fhirrouter.Router
	srv  http.Handler
}

func newEnv(t *testing.T, mk Factory) *env {
	t.Helper()
	repo := mk(t)
	r := fhirrouter.New(repo, fhirrouter.Options{BaseURL: base})
	return &env{t, repo, r, http.StripPrefix("/fhir/R4", r.HTTPHandler())}
}

type reply struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (e *env) do(method, path string, body any, headers ...string) reply {
	e.t.Helper()
	var rd io.Reader
	ct := "application/fhir+json"
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, "/fhir/R4/"+strings.TrimPrefix(path, "/"), rd)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if req.Header.Get("Content-Type") == "" && rd != nil {
		req.Header.Set("Content-Type", ct)
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, req)
	res := w.Result()
	raw, _ := io.ReadAll(res.Body)
	rp := reply{status: res.StatusCode, header: res.Header, raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &rp.body)
	}
	return rp
}

func (e *env) mustDo(want int, method, path string, body any, headers ...string) reply {
	e.t.Helper()
	r := e.do(method, path, body, headers...)
	if r.status != want {
		e.t.Fatalf("%s %s: muốn %d, được %d: %s", method, path, want, r.status, r.raw)
	}
	return r
}

func str(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		switch c := cur.(type) {
		case map[string]any:
			cur = c[k]
		case []any:
			i, _ := strconv.Atoi(k)
			if i >= len(c) {
				return ""
			}
			cur = c[i]
		default:
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

func entries(r reply) []any { e, _ := r.body["entry"].([]any); return e }

func ids(r reply) []string {
	var out []string
	for _, e := range entries(r) {
		out = append(out, str(e.(map[string]any), "resource", "id"))
	}
	return out
}

func patient(family string, extra map[string]any) map[string]any {
	p := map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": family, "given": []any{"X"}}}}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// ---------------------------------------------------------------- CRUD

func testCRUDAndHistory(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	created := e.mustDo(201, "POST", "Patient", patient("Smith", nil))
	id := str(created.body, "id")
	vid1 := str(created.body, "meta", "versionId")
	if id == "" || vid1 == "" || str(created.body, "meta", "lastUpdated") == "" {
		t.Fatalf("thiếu id/meta: %s", created.raw)
	}
	if loc := created.header.Get("Location"); loc != base+"/Patient/"+id+"/_history/"+vid1 {
		t.Errorf("Location: %s", loc)
	}
	if created.header.Get("ETag") != `W/"`+vid1+`"` || created.header.Get("Last-Modified") == "" {
		t.Errorf("header: %v", created.header)
	}
	if !strings.HasPrefix(created.header.Get("Content-Type"), "application/fhir+json") {
		t.Errorf("content-type: %s", created.header.Get("Content-Type"))
	}

	read := e.mustDo(200, "GET", "Patient/"+id, nil)
	if str(read.body, "name", "0", "family") != "Smith" {
		t.Errorf("read: %s", read.raw)
	}

	// update: If-Match đúng
	upd := patient("Jones", map[string]any{"id": id})
	u1 := e.mustDo(200, "PUT", "Patient/"+id, upd, "If-Match", `W/"`+vid1+`"`)
	vid2 := str(u1.body, "meta", "versionId")
	if vid2 == vid1 || str(u1.body, "name", "0", "family") != "Jones" {
		t.Errorf("update: %s", u1.raw)
	}
	// If-Match cũ -> 412
	e.mustDo(412, "PUT", "Patient/"+id, upd, "If-Match", `W/"`+vid1+`"`)
	// id sai -> 400
	e.mustDo(400, "PUT", "Patient/"+id, patient("X", map[string]any{"id": "other"}))
	// PUT tới id chưa có -> 404 (không update-as-create)
	e.mustDo(404, "PUT", "Patient/none", patient("X", map[string]any{"id": "none"}))

	// vread
	old := e.mustDo(200, "GET", "Patient/"+id+"/_history/"+vid1, nil)
	if str(old.body, "name", "0", "family") != "Smith" {
		t.Errorf("vread: %s", old.raw)
	}
	e.mustDo(404, "GET", "Patient/"+id+"/_history/nope", nil)

	// history: mới nhất trước
	h := e.mustDo(200, "GET", "Patient/"+id+"/_history", nil)
	if str(h.body, "type") != "history" || len(entries(h)) != 2 {
		t.Fatalf("history: %s", h.raw)
	}
	if str(h.body, "entry", "0", "response", "etag") != `W/"`+vid2+`"` || str(h.body, "entry", "0", "request", "method") != "PUT" {
		t.Errorf("history[0]: %s", h.raw)
	}

	// delete
	e.mustDo(204, "DELETE", "Patient/"+id, nil)
	e.mustDo(410, "GET", "Patient/"+id, nil)
	e.mustDo(204, "DELETE", "Patient/"+id, nil) // idempotent
	e.mustDo(404, "DELETE", "Patient/never", nil)
	h = e.mustDo(200, "GET", "Patient/"+id+"/_history", nil)
	if len(entries(h)) != 3 || str(h.body, "entry", "0", "request", "method") != "DELETE" {
		t.Errorf("history sau xóa: %s", h.raw)
	}
	if _, ok := h.body["entry"].([]any)[0].(map[string]any)["resource"]; ok {
		t.Errorf("phiên bản xóa không được có resource")
	}
	// search không thấy resource đã xóa
	if n := len(entries(e.mustDo(200, "GET", "Patient", nil))); n != 0 {
		t.Errorf("search thấy resource đã xóa: %d", n)
	}
}

func testErrorsAndRouting(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	e.mustDo(404, "GET", "Nope/1", nil)
	e.mustDo(404, "GET", "Patient/1/unknown/path", nil)
	e.mustDo(405, "DELETE", "metadata", nil)
	e.mustDo(405, "PUT", "Patient/1/_history", nil)
	e.mustDo(400, "POST", "$graphql", "{}")                                     // thiếu query (GraphQL: xem testGraphQL)
	e.mustDo(400, "POST", "Patient", `{"resourceType":"Observation"}`)          // sai resourceType
	e.mustDo(400, "POST", "Patient", `[1,2]`)                                   // không phải object
	e.mustDo(400, "POST", "Patient", `{`)                                       // JSON hỏng
	e.mustDo(415, "POST", "Patient", `<x/>`, "Content-Type", "application/xml") // Content-Type lạ
	r := e.mustDo(400, "POST", "Patient", map[string]any{"resourceType": "Patient", "active": "yes", "bogus": 1})
	if str(r.body, "resourceType") != "OperationOutcome" || len(r.body["issue"].([]any)) != 2 {
		t.Errorf("outcome: %s", r.raw)
	}
	if str(r.body, "issue", "0", "severity") != "error" || str(r.body, "issue", "0", "expression", "0") == "" {
		t.Errorf("issue: %s", r.raw)
	}
	// cảnh báo (dom-6) không chặn ghi
	e.mustDo(201, "POST", "Patient", map[string]any{"resourceType": "Patient"})
	// invariant lỗi chặn ghi
	e.mustDo(400, "POST", "Patient", map[string]any{"resourceType": "Patient", "contact": []any{map[string]any{"gender": "male"}}})
}

func testSkipValidationOption(t *testing.T, mk Factory) {
	r := fhirrouter.New(mk(t), fhirrouter.Options{SkipValidation: true})
	res, err := r.Handle(context.Background(), &fhirrouter.Request{Method: "POST", Path: "Patient",
		Body: map[string]any{"resourceType": "Patient", "gender": "male"}, Header: http.Header{}})
	if err != nil || res.Status != 201 {
		t.Fatalf("%v %+v", err, res)
	}
}

func testPreferHeader(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	r := e.mustDo(201, "POST", "Patient", patient("A", nil), "Prefer", "return=minimal")
	if r.raw != "" || r.header.Get("Location") == "" {
		t.Errorf("minimal: %q %v", r.raw, r.header)
	}
	r = e.mustDo(201, "POST", "Patient", patient("A", nil), "Prefer", "return=OperationOutcome")
	if str(r.body, "resourceType") != "OperationOutcome" {
		t.Errorf("outcome: %s", r.raw)
	}
}

func testCapabilities(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	r := e.mustDo(200, "GET", "metadata", nil)
	if str(r.body, "resourceType") != "CapabilityStatement" || str(r.body, "fhirVersion") != "4.0.1" {
		t.Fatalf("cs: %.200s", r.raw)
	}
	rest := r.body["rest"].([]any)[0].(map[string]any)
	var patientRes map[string]any
	for _, x := range rest["resource"].([]any) {
		if x.(map[string]any)["type"] == "Patient" {
			patientRes = x.(map[string]any)
		}
	}
	if patientRes == nil || len(patientRes["interaction"].([]any)) < 8 || len(patientRes["searchParam"].([]any)) < 20 {
		t.Errorf("Patient: %v", patientRes)
	}
}

func testCustomOperation(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	e.r.Add("GET", "Patient/:id/$echo", func(_ context.Context, req *fhirrouter.Request) (*fhirrouter.Response, error) {
		return &fhirrouter.Response{Status: 200, Resource: &fhir.Parameters{Parameter: []fhir.ParametersParameter{{Name: "id", ValueString: req.Params["id"]}}}}, nil
	})
	r := e.mustDo(200, "GET", "Patient/abc/$echo", nil)
	if str(r.body, "parameter", "0", "valueString") != "abc" {
		t.Errorf("op: %s", r.raw)
	}
	// route cố định không bị route tham số che
	e.mustDo(404, "GET", "Patient/abc", nil)
}

// ---------------------------------------------------------------- search

func seed(e *env) {
	for i := 0; i < 25; i++ {
		e.mustDo(201, "POST", "Patient", patient(fmt.Sprintf("Fam%02d", i), map[string]any{
			"gender": []string{"male", "female"}[i%2], "birthDate": fmt.Sprintf("19%02d-06-15", 50+i),
		}))
	}
}

func testSearchPaging(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	seed(e)
	r := e.mustDo(200, "GET", "Patient?_count=10&_total=accurate&_sort=family", nil)
	if str(r.body, "type") != "searchset" || int(r.body["total"].(float64)) != 25 || len(entries(r)) != 10 {
		t.Fatalf("trang 1: total=%v n=%d", r.body["total"], len(entries(r)))
	}
	if str(r.body, "entry", "0", "search", "mode") != "match" || !strings.HasPrefix(str(r.body, "entry", "0", "fullUrl"), base+"/Patient/") {
		t.Errorf("entry: %v", entries(r)[0])
	}
	if str(r.body, "entry", "0", "resource", "name", "0", "family") != "Fam00" {
		t.Errorf("sort: %v", ids(r))
	}
	links := map[string]string{}
	for _, l := range r.body["link"].([]any) {
		lm := l.(map[string]any)
		links[lm["relation"].(string)] = lm["url"].(string)
	}
	if links["self"] == "" || links["next"] == "" || links["previous"] != "" {
		t.Fatalf("links: %v", links)
	}
	// đi theo next tới hết
	pages, seen := 1, len(entries(r))
	for next := links["next"]; next != ""; {
		u, _ := url.Parse(next)
		rr := e.mustDo(200, "GET", "Patient?"+u.RawQuery, nil)
		pages++
		seen += len(entries(rr))
		next = ""
		for _, l := range rr.body["link"].([]any) {
			if lm := l.(map[string]any); lm["relation"] == "next" {
				next = lm["url"].(string)
			}
		}
		if pages > 5 {
			t.Fatal("phân trang không dừng")
		}
	}
	if pages != 3 || seen != 25 {
		t.Errorf("pages=%d seen=%d", pages, seen)
	}
	// total không yêu cầu thì không có
	r = e.mustDo(200, "GET", "Patient", nil)
	if _, ok := r.body["total"]; ok || len(entries(r)) != 20 {
		t.Errorf("mặc định: total=%v n=%d", r.body["total"], len(entries(r)))
	}
	// _summary=count
	r = e.mustDo(200, "GET", "Patient?_summary=count&gender=male", nil)
	if int(r.body["total"].(float64)) != 13 || len(entries(r)) != 0 {
		t.Errorf("count: %s", r.raw)
	}
	// sort giảm dần theo ngày sinh
	r = e.mustDo(200, "GET", "Patient?_sort=-birthdate&_count=2", nil)
	if str(r.body, "entry", "0", "resource", "birthDate") != "1974-06-15" {
		t.Errorf("sort desc: %s", r.raw)
	}
	// bộ lọc
	r = e.mustDo(200, "GET", "Patient?gender=female&birthdate=ge1970-01-01&_total=accurate", nil)
	// female là i lẻ; birthDate >= 1970 là i >= 20 -> i = 21, 23
	if n := int(r.body["total"].(float64)); n != 2 {
		t.Errorf("kết quả lọc: muốn 2, được %d", n)
	}
	// _elements
	r = e.mustDo(200, "GET", "Patient?_elements=gender&_count=1", nil)
	res := entries(r)[0].(map[string]any)["resource"].(map[string]any)
	if _, hasName := res["name"]; hasName || res["gender"] == nil || res["id"] == nil {
		t.Errorf("_elements: %v", res)
	}
	// lỗi tham số
	e.mustDo(400, "GET", "Patient?nope=1", nil)
	e.mustDo(400, "GET", "Patient?_count=abc", nil)
	e.mustDo(400, "GET", "Patient?name.family=x", nil) // name không phải reference
}

func testSearchPostAndSystem(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	p := e.mustDo(201, "POST", "Patient", patient("Smith", nil))
	e.mustDo(201, "POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "c"}})
	r := e.mustDo(200, "POST", "Patient/_search", "family=smith", "Content-Type", "application/x-www-form-urlencoded")
	if len(entries(r)) != 1 {
		t.Errorf("POST _search: %s", r.raw)
	}
	r = e.mustDo(200, "GET", "?_type=Patient,Observation", nil)
	if len(entries(r)) != 2 {
		t.Errorf("system: %s", r.raw)
	}
	r = e.mustDo(200, "GET", "?_type=Patient&_id="+str(p.body, "id"), nil)
	if len(entries(r)) != 1 {
		t.Errorf("system _id: %s", r.raw)
	}
	e.mustDo(400, "GET", "?name=x", nil)
}

func testIncludes(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	p := e.mustDo(201, "POST", "Patient", patient("Smith", nil))
	pid := str(p.body, "id")
	o := e.mustDo(201, "POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final",
		"code": map[string]any{"text": "c"}, "subject": map[string]any{"reference": "Patient/" + pid}})
	oid := str(o.body, "id")
	// _include: Observation kéo theo Patient
	r := e.mustDo(200, "GET", "Observation?_include=Observation:subject", nil)
	if len(entries(r)) != 2 || str(r.body, "entry", "1", "search", "mode") != "include" || str(r.body, "entry", "1", "resource", "id") != pid {
		t.Errorf("_include: %s", r.raw)
	}
	// _revinclude: Patient kéo theo Observation
	r = e.mustDo(200, "GET", "Patient?_revinclude=Observation:subject", nil)
	if len(entries(r)) != 2 || str(r.body, "entry", "1", "resource", "id") != oid {
		t.Errorf("_revinclude: %s", r.raw)
	}
	// tìm theo tham chiếu
	r = e.mustDo(200, "GET", "Observation?subject=Patient/"+pid, nil)
	if len(entries(r)) != 1 {
		t.Errorf("reference: %s", r.raw)
	}
}

// ---------------------------------------------------------------- điều kiện

func testConditionalCreate(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	body := patient("Cond", map[string]any{"identifier": []any{map[string]any{"system": "http://s", "value": "1"}}})
	first := e.mustDo(201, "POST", "Patient", body, "If-None-Exist", "identifier=http://s|1")
	second := e.mustDo(200, "POST", "Patient", body, "If-None-Exist", "identifier=http://s|1")
	if str(first.body, "id") != str(second.body, "id") {
		t.Errorf("tạo trùng")
	}
	if n := len(entries(e.mustDo(200, "GET", "Patient", nil))); n != 1 {
		t.Errorf("số patient: %d", n)
	}
	// hai resource cùng khớp -> 412
	e.mustDo(201, "POST", "Patient", body)
	e.mustDo(412, "POST", "Patient", body, "If-None-Exist", "identifier=http://s|1")
	e.mustDo(400, "POST", "Patient", body, "If-None-Exist", "nope=1")
}

func testConditionalUpdateDeletePatch(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	mkP := func(v string) map[string]any {
		return patient("N"+v, map[string]any{"identifier": []any{map[string]any{"system": "http://s", "value": v}}})
	}
	// 0 khớp -> tạo mới (201); không được kèm id
	c := e.mustDo(201, "PUT", "Patient?identifier=http://s|1", mkP("1"))
	id := str(c.body, "id")
	e.mustDo(400, "PUT", "Patient?identifier=http://s|zzz", patient("A", map[string]any{"id": "x"}))
	// 1 khớp -> cập nhật (200)
	u := e.mustDo(200, "PUT", "Patient?identifier=http://s|1", mkP("1"))
	if str(u.body, "id") != id || str(u.body, "meta", "versionId") == str(c.body, "meta", "versionId") {
		t.Errorf("conditional update: %s", u.raw)
	}
	// 2 khớp -> 412
	e.mustDo(201, "POST", "Patient", mkP("1"))
	e.mustDo(412, "PUT", "Patient?identifier=http://s|1", mkP("1"))
	e.mustDo(412, "DELETE", "Patient?identifier=http://s|1", nil)
	e.mustDo(412, "PATCH", "Patient?identifier=http://s|1", `[{"op":"add","path":"/active","value":true}]`, "Content-Type", "application/json-patch+json")
	// thiếu tiêu chí -> 400
	e.mustDo(400, "PUT", "Patient", mkP("1"))
	e.mustDo(400, "DELETE", "Patient", nil)

	// conditional patch (1 khớp)
	e.mustDo(201, "POST", "Patient", mkP("7"))
	p := e.mustDo(200, "PATCH", "Patient?identifier=http://s|7", `[{"op":"add","path":"/active","value":true}]`, "Content-Type", "application/json-patch+json")
	if p.body["active"] != true {
		t.Errorf("conditional patch: %s", p.raw)
	}
	e.mustDo(404, "PATCH", "Patient?identifier=http://s|none", `[{"op":"add","path":"/active","value":true}]`, "Content-Type", "application/json-patch+json")
	// conditional delete: 0 khớp -> 204; 1 khớp -> xóa
	e.mustDo(204, "DELETE", "Patient?identifier=http://s|none", nil)
	e.mustDo(204, "DELETE", "Patient?identifier=http://s|7", nil)
	if n := len(entries(e.mustDo(200, "GET", "Patient?identifier=http://s|7", nil))); n != 0 {
		t.Errorf("conditional delete: %d", n)
	}
}

// ---------------------------------------------------------------- patch

func testJSONPatch(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	c := e.mustDo(201, "POST", "Patient", patient("Orig", map[string]any{"gender": "male"}))
	id := str(c.body, "id")
	hdr := []string{"Content-Type", "application/json-patch+json"}
	p := e.mustDo(200, "PATCH", "Patient/"+id, `[
	  {"op":"test","path":"/gender","value":"male"},
	  {"op":"replace","path":"/gender","value":"female"},
	  {"op":"add","path":"/name/0/given/-","value":"Y"},
	  {"op":"add","path":"/name/0/given/0","value":"Z"},
	  {"op":"add","path":"/active","value":true},
	  {"op":"copy","from":"/gender","path":"/name/0/text"},
	  {"op":"remove","path":"/name/0/family"}
	]`, hdr...)
	if str(p.body, "gender") != "female" || p.body["active"] != true || str(p.body, "name", "0", "text") != "female" {
		t.Errorf("patch: %s", p.raw)
	}
	given := p.body["name"].([]any)[0].(map[string]any)["given"].([]any)
	if len(given) != 3 || given[0] != "Z" || given[2] != "Y" {
		t.Errorf("given: %v", given)
	}
	if _, ok := p.body["name"].([]any)[0].(map[string]any)["family"]; ok {
		t.Errorf("family chưa bị xóa")
	}
	// lỗi: test thất bại, path không tồn tại, op lạ, thiếu value
	for name, ops := range map[string]string{
		"test sai":        `[{"op":"test","path":"/gender","value":"male"}]`,
		"path thiếu":      `[{"op":"remove","path":"/nope"}]`,
		"op lạ":           `[{"op":"frob","path":"/x"}]`,
		"thiếu value":     `[{"op":"add","path":"/active"}]`,
		"đổi id":          `[{"op":"replace","path":"/id","value":"other"}]`,
		"hỏng validate":   `[{"op":"add","path":"/active","value":"yes"}]`,
		"không phải mảng": `{"op":"add"}`,
	} {
		if r := e.do("PATCH", "Patient/"+id, ops, hdr...); r.status != 400 {
			t.Errorf("%s: muốn 400, được %d (%s)", name, r.status, r.raw)
		}
	}
	e.mustDo(415, "PATCH", "Patient/"+id, `[]`, "Content-Type", "application/json")
	e.mustDo(404, "PATCH", "Patient/none", `[]`, hdr...)
	// If-Match
	r := e.mustDo(200, "GET", "Patient/"+id, nil)
	e.mustDo(412, "PATCH", "Patient/"+id, `[{"op":"add","path":"/active","value":false}]`, append(hdr, "If-Match", `W/"stale"`)...)
	e.mustDo(200, "PATCH", "Patient/"+id, `[{"op":"add","path":"/active","value":false}]`, append(hdr, "If-Match", r.header.Get("ETag"))...)
}

// ---------------------------------------------------------------- batch

func bundle(typ string, entries ...map[string]any) map[string]any {
	es := make([]any, len(entries))
	for i, e := range entries {
		es[i] = e
	}
	return map[string]any{"resourceType": "Bundle", "type": typ, "entry": es}
}

func entry(method, url string, resource map[string]any, extra ...string) map[string]any {
	req := map[string]any{"method": method, "url": url}
	for i := 0; i+1 < len(extra); i += 2 {
		req[extra[i]] = extra[i+1]
	}
	en := map[string]any{"request": req}
	if resource != nil {
		en["resource"] = resource
	}
	return en
}

func testBatch(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	p := e.mustDo(201, "POST", "Patient", patient("B", nil))
	pid := str(p.body, "id")
	r := e.mustDo(200, "POST", "", bundle("batch",
		entry("POST", "Patient", patient("New", nil)),
		entry("GET", "Patient/"+pid, nil),
		entry("GET", "Patient/missing", nil),
		entry("POST", "Patient", map[string]any{"resourceType": "Patient", "bogus": 1}),
		entry("PUT", "Patient/"+pid, patient("Upd", map[string]any{"id": pid})),
		entry("DELETE", "Patient/"+pid, nil),
		entry("GET", "Patient?family=new", nil),
	))
	if str(r.body, "type") != "batch-response" {
		t.Fatalf("type: %s", r.raw)
	}
	var statuses []string
	for _, en := range entries(r) {
		statuses = append(statuses, strings.Fields(str(en.(map[string]any), "response", "status"))[0])
	}
	if got := strings.Join(statuses, ","); got != "201,200,404,400,200,204,200" {
		t.Fatalf("statuses: %s\n%s", got, r.raw)
	}
	if str(r.body, "entry", "0", "resource", "name", "0", "family") != "New" || str(r.body, "entry", "0", "response", "location") == "" {
		t.Errorf("entry 0: %v", entries(r)[0])
	}
	if str(r.body, "entry", "3", "response", "outcome", "resourceType") != "OperationOutcome" {
		t.Errorf("entry 3 thiếu outcome")
	}
	if n := len(entries(r)[6].(map[string]any)["resource"].(map[string]any)["entry"].([]any)); n != 1 {
		t.Errorf("search trong batch: %d", n)
	}
	e.mustDo(400, "POST", "", bundle("collection"))
	e.mustDo(400, "POST", "", `{"resourceType":"Patient"}`)
	e.mustDo(400, "POST", "", `{"resourceType":"Bundle","type":"batch","entry":[{"resource":{}}]}`)
}

func testTransactionUUIDReferences(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	tx := bundle("transaction",
		// Observation đứng trước Patient nhưng tham chiếu bằng urn:uuid
		withFull(entry("POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final",
			"code": map[string]any{"text": "c"}, "subject": map[string]any{"reference": "urn:uuid:pat"}}), "urn:uuid:obs"),
		withFull(entry("POST", "Patient", patient("Tx", nil)), "urn:uuid:pat"),
	)
	r := e.mustDo(200, "POST", "", tx)
	if str(r.body, "type") != "transaction-response" {
		t.Fatalf("type: %s", r.raw)
	}
	obsRes := entries(r)[0].(map[string]any)
	patRes := entries(r)[1].(map[string]any)
	pid := str(patRes, "resource", "id")
	if str(obsRes, "resource", "subject", "reference") != "Patient/"+pid {
		t.Errorf("tham chiếu chưa thay: %v", obsRes)
	}
	if str(obsRes, "response", "status") != "201 Created" {
		t.Errorf("status: %s", str(obsRes, "response", "status"))
	}
	// đọc lại từ kho
	o := e.mustDo(200, "GET", "Observation?subject=Patient/"+pid, nil)
	if len(entries(o)) != 1 {
		t.Errorf("observation: %s", o.raw)
	}
}

func withFull(en map[string]any, full string) map[string]any { en["fullUrl"] = full; return en }

func testTransactionRollback(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	keep := e.mustDo(201, "POST", "Patient", patient("Keep", nil))
	kid := str(keep.body, "id")
	r := e.mustDo(400, "POST", "", bundle("transaction",
		entry("POST", "Patient", patient("Ghost", nil)),
		entry("DELETE", "Patient/"+kid, nil),
		entry("POST", "Patient", map[string]any{"resourceType": "Patient", "bogus": 1}),
	))
	if str(r.body, "resourceType") != "OperationOutcome" || !strings.Contains(r.raw, "Bundle.entry[2]") {
		t.Errorf("outcome: %s", r.raw)
	}
	// không có gì thay đổi: Ghost không được tạo, Keep chưa bị xóa
	e.mustDo(200, "GET", "Patient/"+kid, nil)
	if n := len(entries(e.mustDo(200, "GET", "Patient?family=ghost", nil))); n != 0 {
		t.Errorf("rollback thất bại: %d ghost", n)
	}
}

func testTransactionOrderAndConditional(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	old := e.mustDo(201, "POST", "Patient", patient("Old", map[string]any{"identifier": []any{map[string]any{"system": "http://s", "value": "k"}}}))
	oid := str(old.body, "id")
	// POST có ifNoneExist trùng -> dùng lại resource cũ (200), tham chiếu urn:uuid trỏ vào id cũ
	r := e.mustDo(200, "POST", "", bundle("transaction",
		withFull(entry("POST", "Patient", patient("Dup", map[string]any{"identifier": []any{map[string]any{"system": "http://s", "value": "k"}}}),
			"ifNoneExist", "identifier=http://s|k"), "urn:uuid:p"),
		entry("POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final",
			"code": map[string]any{"text": "c"}, "subject": map[string]any{"reference": "urn:uuid:p"}}),
		// DELETE được xử lý trước POST dù đứng sau
		entry("DELETE", "Patient/"+oid, nil),
	))
	if !strings.HasPrefix(str(r.body, "entry", "0", "response", "status"), "200") {
		t.Errorf("ifNoneExist: %s", r.raw)
	}
	// vì DELETE chạy trước, Patient cũ đã bị xóa nhưng Observation vẫn tham chiếu id cũ (đã tính từ trước)
	if str(r.body, "entry", "1", "resource", "subject", "reference") != "Patient/"+oid {
		t.Errorf("subject: %s", r.raw)
	}
	e.mustDo(410, "GET", "Patient/"+oid, nil)
}

type plainRepo struct{ fhirrouter.Repository }

func testTransactionRequiresTransactionalRepo(t *testing.T, mk Factory) {
	r := fhirrouter.New(plainRepo{mk(t)}, fhirrouter.Options{})
	_, err := r.Handle(context.Background(), &fhirrouter.Request{Method: "POST", Path: "", Header: http.Header{}, Body: bundle("transaction")})
	if e, ok := err.(*fhirrouter.Error); !ok || e.Status != 501 {
		t.Fatalf("muốn 501, được %v", err)
	}
}

// ---------------------------------------------------------------- Handle không qua HTTP

func testHandleDirect(t *testing.T, mk Factory) {
	r := fhirrouter.New(mk(t), fhirrouter.Options{})
	ctx := context.Background()
	res, err := r.Handle(ctx, &fhirrouter.Request{Method: "POST", Path: "Patient", Header: http.Header{},
		Body: map[string]any{"resourceType": "Patient", "gender": "male"}})
	if err != nil || res.Status != 201 || res.Resource.GetID() == "" {
		t.Fatalf("%v %+v", err, res)
	}
	// Location không có BaseURL là đường dẫn tương đối
	if loc := res.Header["Location"]; !strings.HasPrefix(loc, "Patient/"+res.Resource.GetID()+"/_history/") {
		t.Errorf("location: %s", loc)
	}
	_, err = r.Handle(ctx, &fhirrouter.Request{Method: "GET", Path: "Patient/zzz", Header: http.Header{}})
	if e, ok := err.(*fhirrouter.Error); !ok || e.Status != 404 {
		t.Fatalf("muốn 404, được %v", err)
	}
}

// Run chạy toàn bộ bộ test hành vi trên repository do mk tạo (mỗi test một repository rỗng).
func Run(t *testing.T, mk Factory) {
	t.Run("CRUDAndHistory", func(t *testing.T) { testCRUDAndHistory(t, mk) })
	t.Run("ErrorsAndRouting", func(t *testing.T) { testErrorsAndRouting(t, mk) })
	t.Run("SkipValidationOption", func(t *testing.T) { testSkipValidationOption(t, mk) })
	t.Run("PreferHeader", func(t *testing.T) { testPreferHeader(t, mk) })
	t.Run("Capabilities", func(t *testing.T) { testCapabilities(t, mk) })
	t.Run("CustomOperation", func(t *testing.T) { testCustomOperation(t, mk) })
	t.Run("SearchPaging", func(t *testing.T) { testSearchPaging(t, mk) })
	t.Run("SearchPostAndSystem", func(t *testing.T) { testSearchPostAndSystem(t, mk) })
	t.Run("Includes", func(t *testing.T) { testIncludes(t, mk) })
	t.Run("ConditionalCreate", func(t *testing.T) { testConditionalCreate(t, mk) })
	t.Run("ConditionalUpdateDeletePatch", func(t *testing.T) { testConditionalUpdateDeletePatch(t, mk) })
	t.Run("JSONPatch", func(t *testing.T) { testJSONPatch(t, mk) })
	t.Run("Batch", func(t *testing.T) { testBatch(t, mk) })
	t.Run("TransactionUUIDReferences", func(t *testing.T) { testTransactionUUIDReferences(t, mk) })
	t.Run("TransactionRollback", func(t *testing.T) { testTransactionRollback(t, mk) })
	t.Run("TransactionOrderAndConditional", func(t *testing.T) { testTransactionOrderAndConditional(t, mk) })
	t.Run("TransactionRequiresTransactionalRepo", func(t *testing.T) { testTransactionRequiresTransactionalRepo(t, mk) })
	t.Run("HandleDirect", func(t *testing.T) { testHandleDirect(t, mk) })
	t.Run("ValidateOperation", func(t *testing.T) { testValidateOperation(t, mk) })
	t.Run("Everything", func(t *testing.T) { testEverything(t, mk) })
	t.Run("ReindexAndJobs", func(t *testing.T) { testReindexAndJobs(t, mk) })
	t.Run("JobFailure", func(t *testing.T) { testJobFailure(t, mk) })
	t.Run("Expunge", func(t *testing.T) { testExpunge(t, mk) })
	t.Run("ChainedSearchAndHas", func(t *testing.T) { testChainedSearchAndHas(t, mk) })
	t.Run("CompositeSearch", func(t *testing.T) { testCompositeSearch(t, mk) })
	t.Run("TokenTerminology", func(t *testing.T) { testTokenTerminology(t, mk) })
	t.Run("GroupExport", func(t *testing.T) { testGroupExport(t, mk) })
	t.Run("SubsumesAndTranslate", func(t *testing.T) { testSubsumesAndTranslate(t, mk) })
	t.Run("FilterParam", func(t *testing.T) { testFilterParam(t, mk) })
	t.Run("GraphQL", func(t *testing.T) { testGraphQL(t, mk) })
	t.Run("ExportTypeFilter", func(t *testing.T) { testExportTypeFilter(t, mk) })
	t.Run("ConditionalRead", func(t *testing.T) { testConditionalRead(t, mk) })
	t.Run("BulkExport", func(t *testing.T) { testBulkExport(t, mk) })
	t.Run("Terminology", func(t *testing.T) { testTerminology(t, mk) })
	t.Run("CodeSystemImport", func(t *testing.T) { testCodeSystemImport(t, mk) })
	t.Run("ConcurrentIfMatch", func(t *testing.T) { testConcurrentIfMatch(t, mk) })
	t.Run("ConcurrentConditionalCreate", func(t *testing.T) { testConcurrentConditionalCreate(t, mk) })
	t.Run("ConcurrentUpdateAsCreate", func(t *testing.T) { testConcurrentUpdateAsCreate(t, mk) })
	t.Run("ConcurrentConditionalUpdate", func(t *testing.T) { testConcurrentConditionalUpdate(t, mk) })
	t.Run("ConcurrentConditionalDelete", func(t *testing.T) { testConcurrentConditionalDelete(t, mk) })
}

// ---------------------------------------------------------------- operation và job

func testValidateOperation(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	ok := e.mustDo(200, "POST", "Patient/$validate", patient("Valid", nil))
	if str(ok.body, "resourceType") != "OperationOutcome" {
		t.Errorf("valid: %s", ok.raw)
	}
	for _, i := range ok.body["issue"].([]any) {
		if i.(map[string]any)["severity"] == "error" {
			t.Errorf("không được có lỗi: %s", ok.raw)
		}
	}
	// dạng Parameters
	e.mustDo(200, "POST", "Patient/$validate", map[string]any{"resourceType": "Parameters",
		"parameter": []any{map[string]any{"name": "resource", "resource": patient("InParams", nil)}}})
	// không hợp lệ -> 400 kèm vị trí lỗi
	bad := e.mustDo(400, "POST", "Patient/$validate", map[string]any{"resourceType": "Patient", "active": "yes", "bogus": 1})
	if len(bad.body["issue"].([]any)) < 2 || str(bad.body, "issue", "0", "expression", "0") == "" {
		t.Errorf("invalid: %s", bad.raw)
	}
	e.mustDo(400, "POST", "Patient/$validate", map[string]any{"resourceType": "Observation"}) // sai kiểu
	e.mustDo(400, "POST", "Patient/$validate", "")                                            // thiếu body
	e.mustDo(400, "POST", "Patient/$validate", map[string]any{"resourceType": "Parameters"})  // thiếu tham số resource
	// kiểm tra không lưu gì
	if n := len(entries(e.mustDo(200, "GET", "Patient", nil))); n != 0 {
		t.Errorf("$validate không được lưu dữ liệu: %d", n)
	}
}

func testEverything(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	create := func(rt string, body map[string]any) string {
		return str(e.mustDo(201, "POST", rt, body).body, "id")
	}
	p1 := create("Patient", patient("One", nil))
	p2 := create("Patient", patient("Two", nil))
	obs := func(pid string) map[string]any {
		return map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "c"},
			"subject": map[string]any{"reference": "Patient/" + pid}}
	}
	o1 := create("Observation", obs(p1))
	create("Observation", obs(p2))
	enc1 := create("Encounter", map[string]any{"resourceType": "Encounter", "status": "finished",
		"class": map[string]any{"code": "AMB"}, "subject": map[string]any{"reference": "Patient/" + p1}})
	create("Practitioner", map[string]any{"resourceType": "Practitioner", "name": []any{map[string]any{"family": "Doc"}}})

	r := e.mustDo(200, "GET", "Patient/"+p1+"/$everything", nil)
	got := []string{}
	for _, en := range entries(r) {
		m := en.(map[string]any)["resource"].(map[string]any)
		got = append(got, str(m, "resourceType")+"/"+str(m, "id"))
	}
	want := []string{"Patient/" + p1, "Encounter/" + enc1, "Observation/" + o1} // Patient trước, rồi theo tên kiểu
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("everything:\n  muốn %v\n  được %v", want, got)
	}
	if r.body["total"] != float64(3) || str(r.body, "type") != "searchset" || str(r.body, "entry", "0", "search", "mode") != "match" {
		t.Errorf("bundle: %s", r.raw)
	}
	// POST cũng được
	e.mustDo(200, "POST", "Patient/"+p1+"/$everything", "")
	// _type
	r = e.mustDo(200, "GET", "Patient/"+p1+"/$everything?_type=Observation", nil)
	if len(entries(r)) != 1 || str(r.body, "entry", "0", "resource", "id") != o1 {
		t.Errorf("_type: %s", r.raw)
	}
	// _since
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if n := len(entries(e.mustDo(200, "GET", "Patient/"+p1+"/$everything?_since="+past, nil))); n != 3 {
		t.Errorf("_since quá khứ: %d", n)
	}
	if n := len(entries(e.mustDo(200, "GET", "Patient/"+p1+"/$everything?_since="+url.QueryEscape(future), nil))); n != 0 {
		t.Errorf("_since tương lai: %d", n)
	}
	// _count cắt bớt và cảnh báo
	r = e.mustDo(200, "GET", "Patient/"+p1+"/$everything?_count=2", nil)
	last := entries(r)[len(entries(r))-1].(map[string]any)
	if len(entries(r)) != 3 || str(last, "search", "mode") != "outcome" || r.body["total"] != float64(2) {
		t.Errorf("_count: %s", r.raw)
	}
	// tham số sai
	for _, q := range []string{"_count=0", "_count=abc", "_count=999999", "_since=hôm-qua", "_type=Nope"} {
		e.mustDo(400, "GET", "Patient/"+p1+"/$everything?"+q, nil)
	}
	e.mustDo(404, "GET", "Patient/none/$everything", nil)
	e.mustDo(204, "DELETE", "Patient/"+p2, nil)
	e.mustDo(410, "GET", "Patient/"+p2+"/$everything", nil)
}

func waitJob(t *testing.T, e *env, id string) reply {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := e.do("GET", "job/"+id+"/status", nil)
		if r.status != 202 || time.Now().After(deadline) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testReindexAndJobs(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	var firstID string
	for i := 0; i < 5; i++ {
		id := str(e.mustDo(201, "POST", "Patient", patient(fmt.Sprintf("R%d", i), nil)).body, "id")
		if i == 0 {
			firstID = id
		}
	}
	// một resource: đồng bộ
	one := e.mustDo(200, "POST", "Patient/"+firstID+"/$reindex", "")
	if str(one.body, "resourceType") != "OperationOutcome" {
		t.Errorf("reindex một: %s", one.raw)
	}
	e.mustDo(404, "POST", "Patient/none/$reindex", "")

	// cả kiểu: chạy nền
	acc := e.mustDo(202, "POST", "Patient/$reindex", "")
	loc := acc.header.Get("Content-Location")
	if !strings.HasPrefix(loc, base+"/job/") || !strings.HasSuffix(loc, "/status") {
		t.Fatalf("Content-Location: %q", loc)
	}
	jobID := strings.TrimSuffix(strings.TrimPrefix(loc, base+"/job/"), "/status")
	done := waitJob(t, e, jobID)
	if done.status != 200 || str(done.body, "resourceType") != "AsyncJob" || str(done.body, "status") != "completed" {
		t.Fatalf("job: %d %s", done.status, done.raw)
	}
	if done.body["output"].(map[string]any)["parameter"].([]any)[0].(map[string]any)["valueInteger"] != float64(5) {
		t.Errorf("output: %s", done.raw)
	}
	// search vẫn đúng sau reindex
	if n := len(entries(e.mustDo(200, "GET", "Patient?family=R3", nil))); n != 1 {
		t.Errorf("search sau reindex: %d", n)
	}
	e.mustDo(404, "GET", "job/none/status", nil)
	if err := e.r.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func testJobFailure(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	e.r.RegisterJob("boom", func(context.Context, fhirrouter.Repository, *fhirrouter.Input) (*fhir.Parameters, error) {
		return nil, errors.New("kaput")
	})
	e.r.RegisterJob("panic", func(context.Context, fhirrouter.Repository, *fhirrouter.Input) (*fhir.Parameters, error) {
		panic("oh no")
	})
	for kind, want := range map[string]string{"boom": "kaput", "panic": "oh no"} {
		job, err := e.r.StartJob(context.Background(), kind, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := waitJob(t, e, job.ID)
		if r.status != 500 || !strings.Contains(str(r.body, "issue", "0", "diagnostics"), want) {
			t.Errorf("%s: %d %s", kind, r.status, r.raw)
		}
	}
	if _, err := e.r.StartJob(context.Background(), "unknown", nil); err == nil {
		t.Error("loại job không tồn tại phải lỗi")
	}
}

func testExpunge(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	c := e.mustDo(201, "POST", "Patient", patient("Gone", nil))
	id := str(c.body, "id")
	for i := 0; i < 2; i++ {
		e.mustDo(200, "PUT", "Patient/"+id, patient(fmt.Sprintf("V%d", i), map[string]any{"id": id}))
	}
	e.mustDo(200, "POST", "Patient/"+id+"/$expunge", "")
	e.mustDo(404, "GET", "Patient/"+id, nil)
	e.mustDo(404, "GET", "Patient/"+id+"/_history", nil)
	if n := len(entries(e.mustDo(200, "GET", "Patient?family=V1", nil))); n != 0 {
		t.Errorf("chỉ mục chưa được dọn: %d", n)
	}
	e.mustDo(404, "POST", "Patient/"+id+"/$expunge", "")
	// resource đã xóa mềm cũng expunge được
	c = e.mustDo(201, "POST", "Patient", patient("Soft", nil))
	id = str(c.body, "id")
	e.mustDo(204, "DELETE", "Patient/"+id, nil)
	e.mustDo(200, "POST", "Patient/"+id+"/$expunge", "")
	e.mustDo(404, "GET", "Patient/"+id, nil)
}

// ---------------------------------------------------------------- bulk export

func (e *env) exportFiles(path string) map[string][]map[string]any {
	e.t.Helper()
	acc := e.mustDo(202, "GET", path, nil, "Prefer", "respond-async")
	loc := acc.header.Get("Content-Location")
	if !strings.HasPrefix(loc, base+"/bulkdata/") || !strings.HasSuffix(loc, "/status") {
		e.t.Fatalf("Content-Location: %q", loc)
	}
	statusPath := strings.TrimPrefix(loc, base+"/")
	var st reply
	for i := 0; i < 500; i++ {
		if st = e.do("GET", statusPath, nil); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.status != 200 {
		e.t.Fatalf("trạng thái export: %d %s", st.status, st.raw)
	}
	if st.body["requiresAccessToken"] != true || st.body["transactionTime"] == "" {
		e.t.Errorf("manifest: %s", st.raw)
	}
	out := map[string][]map[string]any{}
	files, _ := st.body["output"].([]any)
	for _, f := range files {
		fm := f.(map[string]any)
		u := strings.TrimPrefix(fm["url"].(string), base+"/")
		raw := e.mustDo(200, "GET", u, nil, "Accept", "application/fhir+ndjson")
		if ct := raw.header.Get("Content-Type"); ct != "application/fhir+ndjson" {
			e.t.Errorf("Content-Type của file: %q", ct)
		}
		n := 0
		for _, line := range strings.Split(strings.TrimSpace(raw.raw), "\n") {
			if line == "" {
				continue
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(line), &doc); err != nil {
				e.t.Fatalf("dòng ndjson không phải JSON: %q", line)
			}
			if doc["resourceType"] != fm["type"] {
				e.t.Errorf("file type %v chứa %v", fm["type"], doc["resourceType"])
			}
			out[fm["type"].(string)] = append(out[fm["type"].(string)], doc)
			n++
		}
		if float64(n) != fm["count"] {
			e.t.Errorf("count %v khác số dòng %d", fm["count"], n)
		}
	}
	return out
}

func testBulkExport(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	var p1 string
	for i, fam := range []string{"One", "Two", "Three"} {
		id := str(e.mustDo(201, "POST", "Patient", patient(fam, nil)).body, "id")
		if i == 0 {
			p1 = id
		}
	}
	for i := 0; i < 2; i++ {
		e.mustDo(201, "POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "c"},
			"subject": map[string]any{"reference": "Patient/" + p1}})
	}
	e.mustDo(201, "POST", "Organization", map[string]any{"resourceType": "Organization", "name": "Org"})

	e.mustDo(400, "GET", "$export", nil)                                                   // thiếu Prefer
	e.mustDo(400, "GET", "$export?_type=Bogus", nil, "Prefer", "respond-async")            // _type sai
	e.mustDo(400, "GET", "$export?_outputFormat=text/csv", nil, "Prefer", "respond-async") // định dạng sai
	e.mustDo(400, "GET", "$export?_since=yesterday", nil, "Prefer", "respond-async")       // _since sai
	e.mustDo(404, "GET", "bulkdata/does-not-exist/status", nil)                            // job không có
	e.mustDo(405, "DELETE", "$export", nil)                                                // chưa hỗ trợ hủy

	sys := e.exportFiles("$export")
	if len(sys["Patient"]) != 3 || len(sys["Observation"]) != 2 || len(sys["Organization"]) != 1 {
		t.Errorf("system export: Patient=%d Observation=%d Organization=%d", len(sys["Patient"]), len(sys["Observation"]), len(sys["Organization"]))
	}
	for _, skipped := range []string{"Binary", "AsyncJob"} {
		if len(sys[skipped]) != 0 {
			t.Errorf("không được xuất %s", skipped)
		}
	}

	pat := e.exportFiles("Patient/$export")
	if len(pat["Patient"]) != 3 || len(pat["Observation"]) != 2 || len(pat["Organization"]) != 0 {
		t.Errorf("patient export: Patient=%d Observation=%d Organization=%d", len(pat["Patient"]), len(pat["Observation"]), len(pat["Organization"]))
	}

	only := e.exportFiles("$export?_type=Patient,Organization&_outputFormat=ndjson")
	if len(only) != 2 || len(only["Patient"]) != 3 || len(only["Organization"]) != 1 {
		t.Errorf("_type: %v", only)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if got := e.exportFiles("$export?_since=" + url.QueryEscape(future)); len(got) != 0 {
		t.Errorf("_since tương lai phải rỗng: %v", got)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if got := e.exportFiles("$export?_type=Patient&_since=" + url.QueryEscape(past)); len(got["Patient"]) != 3 {
		t.Errorf("_since quá khứ: %v", got)
	}

	// job không phải export không đọc được qua /bulkdata
	acc := e.mustDo(202, "POST", "Patient/$reindex", "")
	reindexID := strings.TrimSuffix(strings.TrimPrefix(acc.header.Get("Content-Location"), base+"/job/"), "/status")
	e.mustDo(404, "GET", "bulkdata/"+reindexID+"/status", nil)

	// Binary không có Accept riêng vẫn là resource JSON
	files := e.mustDo(200, "GET", "Binary", nil)
	if len(entries(files)) == 0 {
		t.Fatal("không có Binary nào sau export")
	}
	one := str(entries(files)[0].(map[string]any), "resource", "id")
	if r := e.mustDo(200, "GET", "Binary/"+one, nil); str(r.body, "resourceType") != "Binary" || str(r.body, "contentType") != "application/fhir+ndjson" {
		t.Errorf("Binary JSON: %s", r.raw)
	}
}

// ---------------------------------------------------------------- terminology

func testFilterParam(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	mkP := func(fam, gender, birth string) string {
		return str(e.mustDo(201, "POST", "Patient", patient(fam, map[string]any{"gender": gender, "birthDate": birth})).body, "id")
	}
	smith := mkP("Smith", "male", "1970-05-01")
	jones := mkP("Jones", "female", "1990-02-03")
	lee := mkP("Leeson", "male", "1985-07-07")
	enc := url.QueryEscape
	check := func(filter string, want ...string) {
		t.Helper()
		got := ids(e.mustDo(200, "GET", "Patient?_filter="+enc(filter), nil))
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("_filter=%s = [%s], muốn [%s]", filter, strings.Join(got, ","), strings.Join(want, ","))
		}
	}
	check(`name co "mit"`, smith)
	check(`gender eq male and birthdate lt 1980`, smith)
	check(`gender eq female or name sw Lee`, jones, lee)
	check(`not (gender eq male)`, jones)
	check(`(gender eq male and birthdate ge 1980) or name eq "Jones"`, jones, lee)
	check(`birthdate ne 1985-07-07 and not (name co "jo")`, smith)
	check(`name eq "smith"`) // phân biệt hoa thường
	// kết hợp với tham số thường và phân trang giữ nguyên _filter
	r := e.mustDo(200, "GET", "Patient?gender=male&_sort=family&_count=1&_filter="+enc(`birthdate gt 1900`), nil)
	next := ""
	for _, l := range r.body["link"].([]any) {
		if str(l.(map[string]any), "relation") == "next" {
			next = str(l.(map[string]any), "url")
		}
	}
	if !strings.Contains(next, "_filter=") {
		t.Fatalf("link next mất _filter: %q", next)
	}
	page2 := e.mustDo(200, "GET", strings.TrimPrefix(next, base+"/"), nil)
	if got := ids(page2); len(got) != 1 || got[0] != smith {
		t.Errorf("trang 2: %v", got)
	}
	// thao tác có điều kiện dùng _filter làm tiêu chí (không bao giờ là "không có tiêu chí")
	e.mustDo(412, "DELETE", "Patient?_filter="+enc(`gender eq male`), nil) // nhiều khớp
	e.mustDo(204, "DELETE", "Patient?_filter="+enc(`name eq "Leeson"`), nil)
	e.mustDo(410, "GET", "Patient/"+lee, nil)
	if got := ids(e.mustDo(200, "GET", "Patient", nil)); len(got) != 2 {
		t.Errorf("conditional delete xóa sai: %v", got)
	}
	// lỗi
	for _, f := range []string{`name ew "x"`, `nope eq 1`, `(name eq "x"`, ``, `name eq`} {
		e.mustDo(400, "GET", "Patient?_filter="+enc(f), nil)
	}
	e.mustDo(400, "GET", "?_type=Patient&_filter="+enc(`gender eq male`), nil)
}

func testGraphQL(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	create := func(rt string, body map[string]any) string { return str(e.mustDo(201, "POST", rt, body).body, "id") }
	org := create("Organization", map[string]any{"resourceType": "Organization", "name": "Burgers"})
	smith := create("Patient", patient("Smith", map[string]any{"gender": "male", "managingOrganization": map[string]any{"reference": "Organization/" + org}}))
	jones := create("Patient", patient("Jones", map[string]any{"gender": "female"}))
	obs := func(pid string, v float64) string {
		return create("Observation", map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "weight"},
			"subject":       map[string]any{"reference": "Patient/" + pid},
			"valueQuantity": map[string]any{"value": v, "unit": "kg"}})
	}
	o1, o2, o3 := obs(smith, 80), obs(smith, 82), obs(jones, 60)

	gql := func(query string, vars map[string]any) reply {
		body := map[string]any{"query": query}
		if vars != nil {
			body["variables"] = vars
		}
		return e.mustDo(200, "POST", "$graphql", body)
	}
	count := func(r reply, path ...string) int {
		var cur any = r.body
		for _, k := range path {
			m, _ := cur.(map[string]any)
			cur = m[k]
		}
		l, _ := cur.([]any)
		return len(l)
	}
	noErrors := func(r reply) reply {
		t.Helper()
		if r.body["errors"] != nil {
			t.Fatalf("lỗi GraphQL: %s", r.raw)
		}
		return r
	}

	// đọc một resource, chọn trường lồng nhau
	r := noErrors(gql(`{ Patient(id: "`+smith+`") { resourceType id gender name { family given } } }`, nil))
	if str(r.body, "data", "Patient", "resourceType") != "Patient" || str(r.body, "data", "Patient", "id") != smith ||
		str(r.body, "data", "Patient", "name", "0", "family") != "Smith" || str(r.body, "data", "Patient", "gender") != "male" {
		t.Errorf("Patient(id): %s", r.raw)
	}
	// biến, alias, giá trị mặc định, operationName
	r = noErrors(gql(`query Q($id: ID!) { who: Patient(id: $id) { id } }`, map[string]any{"id": jones}))
	if str(r.body, "data", "who", "id") != jones {
		t.Errorf("biến: %s", r.raw)
	}
	r = noErrors(gql(`query Q($f: String = "smith") { PatientList(family: $f) { id } }`, nil))
	if count(r, "data", "PatientList") != 1 {
		t.Errorf("giá trị mặc định: %s", r.raw)
	}
	multi := e.mustDo(200, "POST", "$graphql", map[string]any{"operationName": "B", "query": `query A { Patient(id: "` + smith + `") { id } } query B { Patient(id: "` + jones + `") { id } }`})
	if str(multi.body, "data", "Patient", "id") != jones {
		t.Errorf("operationName: %s", multi.raw)
	}
	e.mustDo(400, "POST", "$graphql", map[string]any{"query": `query A { Patient(id: "x") { id } } query B { Patient(id: "y") { id } }`}) // thiếu operationName

	// danh sách: tham số search, sắp xếp, phân trang, Connection
	r = noErrors(gql(`{ PatientList(_sort: "family") { name { family } } }`, nil))
	if count(r, "data", "PatientList") != 2 || str(r.body, "data", "PatientList", "0", "name", "0", "family") != "Jones" {
		t.Errorf("_sort: %s", r.raw)
	}
	r = noErrors(gql(`{ PatientList(_count: 1, _offset: 1, _sort: "family") { name { family } } }`, nil))
	if count(r, "data", "PatientList") != 1 || str(r.body, "data", "PatientList", "0", "name", "0", "family") != "Smith" {
		t.Errorf("phân trang: %s", r.raw)
	}
	r = noErrors(gql(`{ ObservationList(subject: "Patient/`+smith+`", _sort: "_id") { id } }`, nil))
	if count(r, "data", "ObservationList") != 2 {
		t.Errorf("search theo reference: %s", r.raw)
	}
	r = noErrors(gql(`{ PatientConnection(_count: 1) { count pageSize edges { mode resource { ... on Patient { id } } } } }`, nil))
	if r.body["data"].(map[string]any)["PatientConnection"].(map[string]any)["count"] != float64(2) || count(r, "data", "PatientConnection", "edges") != 1 {
		t.Errorf("Connection: %s", r.raw)
	}

	// nạp resource qua Reference, lồng nhiều cấp
	r = noErrors(gql(`{ ObservationList(subject: "Patient/`+smith+`", _sort: "_id") {
	  valueQuantity { value unit }
	  subject { reference resource { ... on Patient { name { family } managingOrganization { resource { ... on Organization { name } } } } } }
	} }`, nil))
	if str(r.body, "data", "ObservationList", "0", "subject", "resource", "name", "0", "family") != "Smith" ||
		str(r.body, "data", "ObservationList", "0", "subject", "resource", "managingOrganization", "resource", "name") != "Burgers" ||
		str(r.body, "data", "ObservationList", "0", "valueQuantity", "unit") != "kg" {
		t.Errorf("Reference.resource: %s", r.raw)
	}
	// tham chiếu ngược trong resource
	r = noErrors(gql(`{ Patient(id: "`+smith+`") { id ObservationList(_reference: subject) { id } } }`, nil))
	if count(r, "data", "Patient", "ObservationList") != 2 {
		t.Errorf("_reference: %s", r.raw)
	}
	r = noErrors(gql(`{ Patient(id: "`+jones+`") { ObservationList(_reference: patient) { id } } }`, nil))
	if count(r, "data", "Patient", "ObservationList") != 1 || str(r.body, "data", "Patient", "ObservationList", "0", "id") != o3 {
		t.Errorf("_reference theo patient: %s", r.raw)
	}
	_ = o1
	_ = o2

	// fragment có tên, inline fragment sai kiểu bị bỏ qua, __typename
	r = noErrors(gql(`query { Patient(id: "`+smith+`") { __typename ...Basic ... on Organization { name } } } fragment Basic on Patient { id gender }`, nil))
	pt := r.body["data"].(map[string]any)["Patient"].(map[string]any)
	if pt["__typename"] != "Patient" || pt["id"] != smith || pt["gender"] != "male" || pt["name"] != nil {
		t.Errorf("fragment: %s", r.raw)
	}

	// không tìm thấy là null, không lỗi; lỗi theo field không làm hỏng field khác
	r = noErrors(gql(`{ Patient(id: "khong-co") { id } }`, nil))
	if v, ok := r.body["data"].(map[string]any)["Patient"]; !ok || v != nil {
		t.Errorf("không tìm thấy: %s", r.raw)
	}
	r = gql(`{ good: Patient(id: "`+smith+`") { id } bad: PatientList(khong_co_tham_so: "x") { id } Bogus(id: "1") { id } }`, nil)
	if str(r.body, "data", "good", "id") != smith || r.body["data"].(map[string]any)["bad"] != nil || len(r.body["errors"].([]any)) != 2 {
		t.Errorf("lỗi theo field: %s", r.raw)
	}
	if path := r.body["errors"].([]any)[0].(map[string]any)["path"].([]any); len(path) == 0 {
		t.Errorf("lỗi thiếu path: %s", r.raw)
	}
	r = gql(`{ Patient(id: "`+smith+`") { name } }`, nil) // đối tượng cần selection
	if r.body["errors"] == nil {
		t.Errorf("field đối tượng không selection phải báo lỗi: %s", r.raw)
	}

	// các cách truyền truy vấn khác
	g := e.mustDo(200, "GET", "$graphql?query="+url.QueryEscape(`query Q($id: ID!){ Patient(id: $id){ id } }`)+"&variables="+url.QueryEscape(`{"id":"`+smith+`"}`), nil)
	if str(g.body, "data", "Patient", "id") != smith {
		t.Errorf("GET: %s", g.raw)
	}
	raw := e.mustDo(200, "POST", "$graphql", `{ Patient(id: "`+jones+`") { id } }`, "Content-Type", "application/graphql")
	if str(raw.body, "data", "Patient", "id") != jones {
		t.Errorf("application/graphql: %s", raw.raw)
	}
	if ct := raw.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	// lỗi cú pháp và tính năng chưa hỗ trợ: 400
	for _, q := range []string{``, `{ Patient(id: "1" { id } }`, `{ }`, `mutation { Patient(id: "1") { id } }`, `{ Patient(id: "1") { id }`, `{ ...Missing }x`, `query { a(b: $c }`} {
		e.mustDo(400, "POST", "$graphql", map[string]any{"query": q})
	}
	e.mustDo(400, "POST", "$graphql", map[string]any{"query": `{ Patient(id:"1") { id } }`, "variables": []any{1}})
	e.mustDo(400, "POST", "$graphql", map[string]any{"query": strings.Repeat("{ a ", 20) + strings.Repeat("}", 20)}) // quá sâu
}

func testConditionalRead(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	id := str(e.mustDo(201, "POST", "Patient", patient("Cond", nil)).body, "id")
	first := e.mustDo(200, "GET", "Patient/"+id, nil)
	tag, lm := first.header.Get("ETag"), first.header.Get("Last-Modified")
	if tag == "" || lm == "" {
		t.Fatalf("thiếu ETag/Last-Modified: %v", first.header)
	}
	// If-None-Match khớp: 304, không body, giữ ETag
	nm := e.mustDo(304, "GET", "Patient/"+id, nil, "If-None-Match", tag)
	if nm.raw != "" || nm.header.Get("ETag") != tag {
		t.Errorf("304: body=%q etag=%q", nm.raw, nm.header.Get("ETag"))
	}
	e.mustDo(304, "GET", "Patient/"+id, nil, "If-None-Match", `"zzz", `+tag) // danh sách
	e.mustDo(304, "GET", "Patient/"+id, nil, "If-None-Match", "*")
	e.mustDo(200, "GET", "Patient/"+id, nil, "If-None-Match", `W/"khac"`)
	// If-Modified-Since
	e.mustDo(304, "GET", "Patient/"+id, nil, "If-Modified-Since", lm)
	e.mustDo(200, "GET", "Patient/"+id, nil, "If-Modified-Since", "Mon, 01 Jan 2001 00:00:00 GMT")
	e.mustDo(200, "GET", "Patient/"+id, nil, "If-Modified-Since", "không phải ngày") // ngày sai: bỏ qua điều kiện
	// If-None-Match thắng If-Modified-Since
	e.mustDo(200, "GET", "Patient/"+id, nil, "If-None-Match", `W/"khac"`, "If-Modified-Since", lm)
	// sau khi sửa, ETag cũ không còn khớp
	cur := e.mustDo(200, "GET", "Patient/"+id, nil).body
	cur["active"] = true
	e.mustDo(200, "PUT", "Patient/"+id, cur)
	upd := e.mustDo(200, "GET", "Patient/"+id, nil, "If-None-Match", tag)
	if upd.header.Get("ETag") == tag || upd.body["active"] != true {
		t.Errorf("sau khi sửa: %s", upd.raw)
	}
	e.mustDo(404, "GET", "Patient/khong-co", nil, "If-None-Match", "*") // lỗi vẫn là lỗi
}

func testExportTypeFilter(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	mk1 := func(fam string, active bool) string {
		return str(e.mustDo(201, "POST", "Patient", patient(fam, map[string]any{"active": active})).body, "id")
	}
	a1, _, a3 := mk1("Alpha", true), mk1("Beta", false), mk1("Gamma", true)
	obs := func(pid, status string) {
		e.mustDo(201, "POST", "Observation", map[string]any{"resourceType": "Observation", "status": status, "code": map[string]any{"text": "c"},
			"subject": map[string]any{"reference": "Patient/" + pid}})
	}
	obs(a1, "final")
	obs(a1, "preliminary")
	obs(a3, "final")
	e.mustDo(201, "POST", "Organization", map[string]any{"resourceType": "Organization", "name": "Org"})
	enc := url.QueryEscape

	// một điều kiện cho một loại; loại không có điều kiện xuất đủ
	got := e.exportFiles("$export?_type=Patient,Observation&_typeFilter=" + enc("Patient?active=true"))
	if len(got["Patient"]) != 2 || len(got["Observation"]) != 3 {
		t.Errorf("Patient?active=true: Patient=%d Observation=%d", len(got["Patient"]), len(got["Observation"]))
	}
	// chỉ có _typeFilter: xuất đúng các loại được nêu
	only := e.exportFiles("$export?_typeFilter=" + enc("Observation?status=final"))
	if len(only) != 1 || len(only["Observation"]) != 2 {
		t.Errorf("chỉ _typeFilter: %v", only)
	}
	// nhiều điều kiện cùng loại là OR (lặp tham số); trùng lặp được khử
	or := e.exportFiles("$export?_typeFilter=" + enc("Observation?status=final") + "&_typeFilter=" + enc("Observation?status=preliminary"))
	if len(or["Observation"]) != 3 {
		t.Errorf("OR hai điều kiện: %d", len(or["Observation"]))
	}
	dup := e.exportFiles("$export?_typeFilter=" + enc("Observation?status=final") + "&_typeFilter=" + enc("Observation?status=final,preliminary"))
	if len(dup["Observation"]) != 3 {
		t.Errorf("điều kiện chồng nhau phải khử trùng: %d", len(dup["Observation"]))
	}
	// dạng danh sách nối bằng dấu phẩy, dấu phẩy trong giá trị được giữ nguyên
	list := e.exportFiles("$export?_typeFilter=" + enc("Patient?family=alpha,gamma,Observation?status=preliminary"))
	if len(list["Patient"]) != 2 || len(list["Observation"]) != 1 {
		t.Errorf("danh sách: Patient=%d Observation=%d", len(list["Patient"]), len(list["Observation"]))
	}
	// mức patient: điều kiện áp trên cả loại trong compartment
	pat := e.exportFiles("Patient/$export?_type=Patient,Observation&_typeFilter=" + enc("Observation?status=final"))
	if len(pat["Patient"]) != 3 || len(pat["Observation"]) != 2 {
		t.Errorf("patient level: Patient=%d Observation=%d", len(pat["Patient"]), len(pat["Observation"]))
	}
	// điều kiện không khớp gì: không có file
	if none := e.exportFiles("$export?_typeFilter=" + enc("Patient?family=nobody")); len(none) != 0 {
		t.Errorf("không khớp: %v", none)
	}
	// kết hợp với chained search trong điều kiện
	ch := e.exportFiles("$export?_typeFilter=" + enc("Observation?subject:Patient.family=alpha"))
	if len(ch["Observation"]) != 2 {
		t.Errorf("chained trong _typeFilter: %d", len(ch["Observation"]))
	}
	// lỗi
	bad := func(q string) { e.mustDo(400, "GET", "$export?"+q, nil, "Prefer", "respond-async") }
	bad("_typeFilter=" + enc("Patient"))                                // thiếu ?
	bad("_typeFilter=" + enc("Bogus?x=1"))                              // loại sai
	bad("_typeFilter=" + enc("Patient?nope=1"))                         // tham số không có
	bad("_typeFilter=" + enc("Patient?_count=1"))                       // tham số điều khiển
	bad("_type=Patient&_typeFilter=" + enc("Observation?status=final")) // loại ngoài _type
}

func testGroupExport(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	mkPatient := func(fam string) string { return str(e.mustDo(201, "POST", "Patient", patient(fam, nil)).body, "id") }
	in1, in2, out, inactive := mkPatient("In1"), mkPatient("In2"), mkPatient("Out"), mkPatient("Inactive")
	obs := func(p string) {
		e.mustDo(201, "POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "c"},
			"subject": map[string]any{"reference": "Patient/" + p}})
	}
	obs(in1)
	obs(in1)
	obs(in2)
	obs(out)
	obs(inactive)
	e.mustDo(201, "POST", "Organization", map[string]any{"resourceType": "Organization", "name": "Org"})
	member := func(ref string, inactive bool) map[string]any {
		m := map[string]any{"entity": map[string]any{"reference": ref}}
		if inactive {
			m["inactive"] = true
		}
		return m
	}
	group := func(actual bool, members ...any) string {
		doc := map[string]any{"resourceType": "Group", "type": "person", "actual": actual}
		if len(members) > 0 {
			doc["member"] = members
		}
		return str(e.mustDo(201, "POST", "Group", doc).body, "id")
	}
	g := group(true, member("Patient/"+in1, false), member("Patient/"+in2, false), member("Patient/"+inactive, true),
		member("Organization/zzz", false), member("Patient/"+in1, false)) // trùng và không phải Patient bị bỏ
	got := e.exportFiles("Group/" + g + "/$export")
	if len(got["Patient"]) != 2 || len(got["Observation"]) != 3 || len(got["Organization"]) != 0 || len(got["Group"]) != 1 { // Group nằm trong compartment Patient qua member
		t.Errorf("group export: Patient=%d Observation=%d Organization=%d Group=%d", len(got["Patient"]), len(got["Observation"]), len(got["Organization"]), len(got["Group"]))
	}
	for _, p := range got["Patient"] {
		if id := str(p, "id"); id != in1 && id != in2 {
			t.Errorf("xuất Patient ngoài nhóm: %s", id)
		}
	}
	if only := e.exportFiles("Group/" + g + "/$export?_type=Observation"); len(only) != 1 || len(only["Observation"]) != 3 {
		t.Errorf("_type: %v", only)
	}
	if empty := e.exportFiles("Group/" + group(true) + "/$export"); len(empty) != 0 {
		t.Errorf("Group rỗng phải không xuất gì: %v", empty)
	}
	e.mustDo(400, "GET", "Group/"+g+"/$export", nil) // thiếu Prefer
	e.mustDo(404, "GET", "Group/missing/$export", nil, "Prefer", "respond-async")
	e.mustDo(400, "GET", "Group/"+group(false)+"/$export", nil, "Prefer", "respond-async") // Group không liệt kê thành viên
}

func testSubsumesAndTranslate(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	const sys = "http://example.org/cs/animals"
	e.mustDo(201, "POST", "CodeSystem", map[string]any{"resourceType": "CodeSystem", "url": sys, "status": "active", "content": "complete",
		"concept": []any{map[string]any{"code": "animal", "concept": []any{
			map[string]any{"code": "dog", "concept": []any{map[string]any{"code": "puppy"}}}, map[string]any{"code": "bird"}}}}})
	csID := str(e.mustDo(200, "GET", "CodeSystem?url="+url.QueryEscape(sys), nil).body, "entry", "0", "resource", "id")
	outcome := func(path string) string {
		r := e.mustDo(200, "GET", path, nil)
		return str(r.body, "parameter", "0", "valueCode")
	}
	q := "CodeSystem/$subsumes?system=" + url.QueryEscape(sys)
	for _, c := range []struct{ a, b, want string }{
		{"animal", "puppy", "subsumes"}, {"puppy", "animal", "subsumed-by"}, {"dog", "dog", "equivalent"},
		{"dog", "bird", "not-subsumed"}, {"bird", "dog", "not-subsumed"}, {"dog", "puppy", "subsumes"},
	} {
		if got := outcome(q + "&codeA=" + c.a + "&codeB=" + c.b); got != c.want {
			t.Errorf("subsumes %s/%s = %q, muốn %q", c.a, c.b, got, c.want)
		}
	}
	if got := outcome("CodeSystem/" + csID + "/$subsumes?codeA=animal&codeB=bird"); got != "subsumes" {
		t.Errorf("theo id: %q", got)
	}
	post := e.mustDo(200, "POST", "CodeSystem/$subsumes", map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "codingA", "valueCoding": map[string]any{"system": sys, "code": "animal"}},
		map[string]any{"name": "codingB", "valueCoding": map[string]any{"system": sys, "code": "puppy"}}}})
	if str(post.body, "parameter", "0", "valueCode") != "subsumes" {
		t.Errorf("POST coding: %s", post.raw)
	}
	e.mustDo(404, "GET", q+"&codeA=animal&codeB=nope", nil)
	e.mustDo(404, "GET", "CodeSystem/$subsumes?system=http://nope&codeA=a&codeB=b", nil)
	e.mustDo(400, "GET", q+"&codeA=animal", nil)

	// ConceptMap
	const src, dst = "http://example.org/cs/animals", "http://example.org/cs/vn"
	cmID := str(e.mustDo(201, "POST", "ConceptMap", map[string]any{"resourceType": "ConceptMap", "url": "http://example.org/cm/animals-vn", "status": "active",
		"group": []any{map[string]any{"source": src, "target": dst,
			"element": []any{
				map[string]any{"code": "dog", "target": []any{
					map[string]any{"code": "cho", "display": "Chó", "equivalence": "equivalent"},
					map[string]any{"code": "thu", "display": "Thú", "equivalence": "wider"}}},
				map[string]any{"code": "bird", "target": []any{map[string]any{"equivalence": "unmatched"}}},
			}}}}).body, "id")
	matches := func(r reply) []string {
		var out []string
		for _, p := range r.body["parameter"].([]any) {
			pm := p.(map[string]any)
			if pm["name"] != "match" {
				continue
			}
			var eq, code string
			for _, part := range pm["part"].([]any) {
				pp := part.(map[string]any)
				switch pp["name"] {
				case "equivalence":
					eq = str(pp, "valueCode")
				case "concept":
					code = str(pp["valueCoding"].(map[string]any), "code")
				}
			}
			out = append(out, eq+":"+code)
		}
		return out
	}
	result := func(r reply) bool { return r.body["parameter"].([]any)[0].(map[string]any)["valueBoolean"] == true }
	tq := "ConceptMap/$translate?url=" + url.QueryEscape("http://example.org/cm/animals-vn") + "&system=" + url.QueryEscape(src)
	r := e.mustDo(200, "GET", tq+"&code=dog", nil)
	if !result(r) || strings.Join(matches(r), ",") != "equivalent:cho,wider:thu" {
		t.Errorf("translate dog: %s", r.raw)
	}
	r = e.mustDo(200, "GET", "ConceptMap/"+cmID+"/$translate?code=dog&targetsystem="+url.QueryEscape(dst), nil)
	if !result(r) || len(matches(r)) != 2 {
		t.Errorf("theo id: %s", r.raw)
	}
	if r = e.mustDo(200, "GET", tq+"&code=bird", nil); result(r) || len(matches(r)) != 0 { // unmatched không phải bản dịch
		t.Errorf("unmatched: %s", r.raw)
	}
	if r = e.mustDo(200, "GET", tq+"&code=nope", nil); result(r) {
		t.Errorf("mã lạ: %s", r.raw)
	}
	if r = e.mustDo(200, "GET", tq+"&code=dog&targetsystem=http://other", nil); result(r) {
		t.Errorf("targetsystem khác: %s", r.raw)
	}
	// không chỉ định ConceptMap: tìm theo source-system
	r = e.mustDo(200, "GET", "ConceptMap/$translate?system="+url.QueryEscape(src)+"&code=dog&targetsystem="+url.QueryEscape(dst), nil)
	if !result(r) || len(matches(r)) != 2 {
		t.Errorf("tìm theo source-system: %s", r.raw)
	}
	// dịch ngược
	r = e.mustDo(200, "GET", "ConceptMap/$translate?system="+url.QueryEscape(dst)+"&code=thu&reverse=true", nil)
	if !result(r) || strings.Join(matches(r), ",") != "narrower:dog" {
		t.Errorf("reverse: %s", r.raw)
	}
	post = e.mustDo(200, "POST", "ConceptMap/$translate", map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "url", "valueUri": "http://example.org/cm/animals-vn"},
		map[string]any{"name": "coding", "valueCoding": map[string]any{"system": src, "code": "dog"}}}})
	if !result(post) {
		t.Errorf("POST: %s", post.raw)
	}
	e.mustDo(404, "GET", "ConceptMap/$translate?url=http://example.org/cm/missing&system="+url.QueryEscape(src)+"&code=dog", nil)
	e.mustDo(400, "GET", "ConceptMap/$translate?code=dog", nil) // thiếu ConceptMap và system
	e.mustDo(400, "GET", "ConceptMap/$translate?url=x", nil)    // thiếu code
}

func testTokenTerminology(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	const sys = "http://example.org/cs/animals"
	e.mustDo(201, "POST", "CodeSystem", map[string]any{"resourceType": "CodeSystem", "url": sys, "status": "active", "content": "complete",
		"concept": []any{
			map[string]any{"code": "animal", "concept": []any{
				map[string]any{"code": "mammal", "concept": []any{
					map[string]any{"code": "dog", "concept": []any{map[string]any{"code": "puppy"}}},
					map[string]any{"code": "cat"},
				}},
				map[string]any{"code": "bird", "concept": []any{map[string]any{"code": "eagle"}}},
			}},
			map[string]any{"code": "plant"},
		}})
	vsID := str(e.mustDo(201, "POST", "ValueSet", map[string]any{"resourceType": "ValueSet", "url": "http://example.org/vs/pets", "status": "active",
		"compose": map[string]any{"include": []any{map[string]any{"system": sys, "concept": []any{map[string]any{"code": "dog"}, map[string]any{"code": "cat"}, map[string]any{"code": "puppy"}}}}}}).body, "id")
	e.mustDo(201, "POST", "ValueSet", map[string]any{"resourceType": "ValueSet", "url": "http://example.org/vs/empty", "status": "active",
		"compose": map[string]any{"include": []any{map[string]any{"system": sys, "concept": []any{map[string]any{"code": "zzz"}}}}}})

	pat := str(e.mustDo(201, "POST", "Patient", patient("Owner", nil)).body, "id")
	cond := func(system, code string) string {
		coding := map[string]any{"code": code}
		if system != "" {
			coding["system"] = system
		}
		return str(e.mustDo(201, "POST", "Condition", map[string]any{"resourceType": "Condition", "subject": map[string]any{"reference": "Patient/" + pat},
			"code": map[string]any{"coding": []any{coding}}}).body, "id")
	}
	animal, mammal, dog, puppy, cat := cond(sys, "animal"), cond(sys, "mammal"), cond(sys, "dog"), cond(sys, "puppy"), cond(sys, "cat")
	bird, eagle, plant := cond(sys, "bird"), cond(sys, "eagle"), cond(sys, "plant")
	other := cond("http://other/cs", "dog") // cùng mã, khác system
	nosys := cond("", "dog")

	check := func(q string, want ...string) {
		t.Helper()
		got := ids(e.mustDo(200, "GET", "Condition?"+q, nil))
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("Condition?%s = [%s], muốn [%s]", q, strings.Join(got, ","), strings.Join(want, ","))
		}
	}
	esc := url.QueryEscape
	// :below gồm chính mã gốc và mọi con cháu; không nhầm system khác
	check("code:below="+esc(sys+"|mammal"), mammal, dog, puppy, cat)
	check("code:below="+esc(sys+"|dog"), dog, puppy)
	check("code:below="+esc(sys+"|puppy"), puppy)
	check("code:below="+esc(sys+"|mammal")+","+esc(sys+"|bird"), mammal, dog, puppy, cat, bird, eagle) // OR
	check("code:below=" + esc(sys+"|nope"))                                                            // mã lạ: không khớp gì
	// :above gồm chính mã gốc và mọi tổ tiên
	check("code:above="+esc(sys+"|puppy"), puppy, dog, mammal, animal)
	check("code:above="+esc(sys+"|animal"), animal)
	check("code:above="+esc(sys+"|eagle"), eagle, bird, animal)
	// :in / :not-in
	check("code:in="+esc("http://example.org/vs/pets"), dog, puppy, cat)
	check("code:in="+esc("ValueSet/"+vsID), dog, puppy, cat)
	check("code:in=" + esc("http://example.org/vs/empty"))
	check("code:not-in="+esc("http://example.org/vs/pets"), animal, mammal, bird, eagle, plant, other, nosys)
	check("code:not-in="+esc("http://example.org/vs/empty"), animal, mammal, dog, puppy, cat, bird, eagle, plant, other, nosys)
	// kết hợp với điều kiện khác và trong _has
	check("code:below="+esc(sys+"|mammal")+"&code:not-in="+esc("http://example.org/vs/pets"), mammal)
	check("code:below="+esc(sys+"|mammal")+"&_id="+dog, dog)
	if got := ids(e.mustDo(200, "GET", "Patient?_has:Condition:subject:code:below="+esc(sys+"|puppy"), nil)); len(got) != 1 {
		t.Errorf("_has với :below: %v", got)
	}
	if got := ids(e.mustDo(200, "GET", "Patient?_has:Condition:subject:code:below="+esc(sys+"|nope"), nil)); len(got) != 0 {
		t.Errorf("_has với :below mã lạ: %v", got)
	}
	// search hệ thống
	if got := ids(e.mustDo(200, "GET", "?_type=Condition&code:below="+esc(sys+"|dog"), nil)); len(got) != 2 {
		t.Errorf("search hệ thống: %v", got)
	}
	// lỗi
	e.mustDo(404, "GET", "Condition?code:in="+esc("http://example.org/vs/missing"), nil)
	e.mustDo(404, "GET", "Condition?code:below="+esc("http://example.org/cs/missing|x"), nil)
	e.mustDo(400, "GET", "Condition?code:in=not-a-url", nil)
	e.mustDo(400, "GET", "Condition?code:below=dog", nil) // thiếu system
	e.mustDo(400, "GET", "Condition?clinical-status:above=active", nil)
	e.mustDo(400, "GET", "Condition?subject:in=x", nil) // :in chỉ dùng cho token
	// link phân trang giữ nguyên modifier
	r := e.mustDo(200, "GET", "Condition?code:below="+esc(sys+"|mammal")+"&_count=2", nil)
	next := ""
	for _, l := range r.body["link"].([]any) {
		if str(l.(map[string]any), "relation") == "next" {
			next = str(l.(map[string]any), "url")
		}
	}
	if !strings.Contains(next, "code%3Abelow=") && !strings.Contains(next, "code:below=") {
		t.Errorf("link next mất modifier: %q", next)
	}
}

func testTerminology(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	const sys = "http://example.org/cs/animals"
	e.mustDo(201, "POST", "CodeSystem", map[string]any{"resourceType": "CodeSystem", "url": sys, "version": "1.0", "name": "Animals", "status": "active", "content": "complete",
		"concept": []any{
			map[string]any{"code": "animal", "display": "Animal", "concept": []any{
				map[string]any{"code": "dog", "display": "Dog", "designation": []any{map[string]any{"language": "vi", "value": "Chó"}},
					"property": []any{map[string]any{"code": "legs", "valueInteger": 4}},
					"concept":  []any{map[string]any{"code": "puppy", "display": "Puppy"}}},
				map[string]any{"code": "bird", "display": "Bird", "property": []any{map[string]any{"code": "legs", "valueInteger": 2}}},
			}},
			map[string]any{"code": "plant", "display": "Plant"},
		}})
	e.mustDo(201, "POST", "CodeSystem", map[string]any{"resourceType": "CodeSystem", "url": "http://example.org/cs/external", "status": "active", "content": "not-present"})
	vsURL := func(n string) string { return "http://example.org/vs/" + n }
	mkVS := func(name string, compose map[string]any) string {
		return str(e.mustDo(201, "POST", "ValueSet", map[string]any{"resourceType": "ValueSet", "url": vsURL(name), "status": "active", "compose": compose}).body, "id")
	}
	all := mkVS("all", map[string]any{"include": []any{map[string]any{"system": sys}}})
	isA := mkVS("dogs", map[string]any{"include": []any{map[string]any{"system": sys, "filter": []any{map[string]any{"property": "concept", "op": "is-a", "value": "dog"}}}}})
	mkVS("enum", map[string]any{"include": []any{map[string]any{"system": sys, "concept": []any{map[string]any{"code": "bird"}, map[string]any{"code": "plant", "display": "Flora"}}}}})
	mkVS("excl", map[string]any{"include": []any{map[string]any{"system": sys}}, "exclude": []any{map[string]any{"system": sys, "concept": []any{map[string]any{"code": "plant"}, map[string]any{"code": "animal"}}}}})
	mkVS("inter", map[string]any{"include": []any{map[string]any{"valueSet": []any{vsURL("all"), vsURL("dogs")}}}})
	mkVS("legs2", map[string]any{"include": []any{map[string]any{"system": sys, "filter": []any{map[string]any{"property": "legs", "op": "=", "value": "2"}}}}})
	mkVS("regex", map[string]any{"include": []any{map[string]any{"system": sys, "filter": []any{map[string]any{"property": "code", "op": "regex", "value": "p.*"}}}}})
	mkVS("badfilter", map[string]any{"include": []any{map[string]any{"system": sys, "filter": []any{map[string]any{"property": "x", "op": "exists", "value": "true"}}}}})
	mkVS("missingcs", map[string]any{"include": []any{map[string]any{"system": "http://example.org/cs/nope"}}})
	mkVS("notpresent", map[string]any{"include": []any{map[string]any{"system": "http://example.org/cs/external"}}})
	mkVS("loopa", map[string]any{"include": []any{map[string]any{"valueSet": []any{vsURL("loopb")}}}})
	mkVS("loopb", map[string]any{"include": []any{map[string]any{"valueSet": []any{vsURL("loopa")}}}})

	codes := func(r reply) []string {
		var out []string
		exp, _ := r.body["expansion"].(map[string]any)
		for _, c := range exp["contains"].([]any) {
			out = append(out, str(c.(map[string]any), "code"))
		}
		return out
	}
	expand := func(name string, extra ...string) []string {
		q := "url=" + url.QueryEscape(vsURL(name))
		for _, x := range extra {
			q += "&" + x
		}
		return codes(e.mustDo(200, "GET", "ValueSet/$expand?"+q, nil))
	}
	eq := func(what string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, muốn %v", what, got, want)
		}
	}
	eq("all", expand("all"), "animal", "dog", "puppy", "bird", "plant")
	eq("is-a", expand("dogs"), "dog", "puppy")
	eq("enum", expand("enum"), "bird", "plant")
	eq("exclude", expand("excl"), "dog", "puppy", "bird")
	eq("valueSet giao", expand("inter"), "dog", "puppy")
	eq("filter =", expand("legs2"), "bird")
	eq("regex", expand("regex"), "puppy", "plant")
	eq("filter text", expand("all", "filter=DOG"), "dog")
	eq("count+offset", expand("all", "count=2", "offset=1"), "dog", "puppy")
	r := e.mustDo(200, "GET", "ValueSet/$expand?url="+url.QueryEscape(vsURL("all"))+"&count=2", nil)
	if exp := r.body["expansion"].(map[string]any); exp["total"] != float64(5) || r.body["compose"] != nil {
		t.Errorf("total/compose: %s", r.raw)
	}
	if r := e.mustDo(200, "GET", "ValueSet/$expand?url="+url.QueryEscape(vsURL("enum")), nil); !strings.Contains(r.raw, "Flora") {
		t.Errorf("display từ compose: %s", r.raw)
	}
	eq("theo id", codes(e.mustDo(200, "GET", "ValueSet/"+isA+"/$expand", nil)), "dog", "puppy")
	_ = all
	// ValueSet nằm trong body
	eq("valueSet inline", codes(e.mustDo(200, "POST", "ValueSet/$expand", map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "valueSet", "resource": map[string]any{"resourceType": "ValueSet", "status": "active",
			"compose": map[string]any{"include": []any{map[string]any{"system": sys, "concept": []any{map[string]any{"code": "dog"}}}}}}}}})), "dog")
	e.mustDo(400, "GET", "ValueSet/$expand", nil)
	e.mustDo(404, "GET", "ValueSet/$expand?url="+url.QueryEscape(vsURL("none")), nil)
	e.mustDo(404, "GET", "ValueSet/none/$expand", nil)
	for _, bad := range []string{"badfilter", "missingcs", "notpresent", "loopa"} {
		e.mustDo(422, "GET", "ValueSet/$expand?url="+url.QueryEscape(vsURL(bad)), nil)
	}
	e.mustDo(400, "GET", "ValueSet/$expand?url="+url.QueryEscape(vsURL("all"))+"&count=-1", nil)

	// $lookup
	lk := e.mustDo(200, "GET", "CodeSystem/$lookup?system="+url.QueryEscape(sys)+"&code=dog", nil)
	if !strings.Contains(lk.raw, `"Dog"`) || !strings.Contains(lk.raw, "Chó") || !strings.Contains(lk.raw, "puppy") || !strings.Contains(lk.raw, "legs") || !strings.Contains(lk.raw, `"1.0"`) {
		t.Errorf("lookup: %s", lk.raw)
	}
	e.mustDo(200, "POST", "CodeSystem/$lookup", map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "coding", "valueCoding": map[string]any{"system": sys, "code": "bird"}}}})
	e.mustDo(404, "GET", "CodeSystem/$lookup?system="+url.QueryEscape(sys)+"&code=cat", nil)
	e.mustDo(404, "GET", "CodeSystem/$lookup?system=http://nope&code=x", nil)
	e.mustDo(400, "GET", "CodeSystem/$lookup?system="+url.QueryEscape(sys), nil)

	// $validate-code
	result := func(r reply) bool {
		for _, p := range r.body["parameter"].([]any) {
			if pm := p.(map[string]any); pm["name"] == "result" {
				return pm["valueBoolean"] == true
			}
		}
		t.Fatalf("thiếu result: %s", r.raw)
		return false
	}
	vc := func(q string) bool {
		return result(e.mustDo(200, "GET", "ValueSet/$validate-code?url="+url.QueryEscape(vsURL("dogs"))+"&"+q, nil))
	}
	if !vc("code=puppy") || !vc("code=dog&system="+url.QueryEscape(sys)) || vc("code=bird") || vc("code=dog&system=http://other") {
		t.Error("ValueSet/$validate-code")
	}
	if !result(e.mustDo(200, "POST", "ValueSet/"+isA+"/$validate-code", map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "codeableConcept", "valueCodeableConcept": map[string]any{"coding": []any{map[string]any{"system": sys, "code": "dog"}}}}}})) {
		t.Error("codeableConcept")
	}
	if cs := func(c string) bool {
		return result(e.mustDo(200, "GET", "CodeSystem/$validate-code?url="+url.QueryEscape(sys)+"&code="+c, nil))
	}; !cs("plant") || cs("cat") {
		t.Error("CodeSystem/$validate-code")
	}
	if r := e.mustDo(200, "GET", "ValueSet/$validate-code?url="+url.QueryEscape(vsURL("dogs"))+"&code=dog&display=Hund", nil); !strings.Contains(r.raw, "message") {
		t.Errorf("display khác chuẩn phải có message: %s", r.raw)
	}
	e.mustDo(400, "GET", "ValueSet/$validate-code?url="+url.QueryEscape(vsURL("dogs")), nil)
}

// ---------------------------------------------------------------- chained search và _has

func testCompositeSearch(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	comp := func(code string, v float64) map[string]any {
		return map[string]any{"code": map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": code}}},
			"valueQuantity": map[string]any{"value": v, "unit": "mmHg", "system": "http://unitsofmeasure.org", "code": "mm[Hg]"}}
	}
	bp := func(sys, dia float64) string {
		return str(e.mustDo(201, "POST", "Observation", map[string]any{"resourceType": "Observation", "status": "final",
			"code":      map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": "85354-9"}}},
			"component": []any{comp("8480-6", sys), comp("8462-4", dia)}}).body, "id")
	}
	high := bp(150, 80) // tâm thu cao
	low := bp(110, 95)  // tâm trương cao
	norm := bp(115, 75)

	set := func(path string) string {
		got := ids(e.mustDo(200, "GET", path, nil))
		sort.Strings(got)
		return strings.Join(got, ",")
	}
	check := func(path string, want ...string) {
		t.Helper()
		sort.Strings(want)
		if got := set(path); got != strings.Join(want, ",") {
			t.Errorf("%s = [%s], muốn [%s]", path, got, strings.Join(want, ","))
		}
	}
	// các thành phần phải khớp trên CÙNG một component (không trộn tâm thu của bản này với tâm trương của bản khác)
	check("Observation?component-code-value-quantity=http://loinc.org|8480-6$gt140", high)
	check("Observation?component-code-value-quantity=8462-4$gt90", low)
	check("Observation?component-code-value-quantity=8480-6$gt140&component-code-value-quantity=8462-4$gt90") // AND hai tham số: không bản nào cả hai
	check("Observation?component-code-value-quantity=8480-6$gt140,8462-4$gt90", high, low)                    // OR trong một tham số
	check("Observation?component-code-value-quantity=8462-4$gt140")                                           // 8462-4 không có giá trị > 140
	check("Observation?component-code-value-quantity=8480-6$lt120", low, norm)                                // cận dưới
	check("Observation?component-code-value-quantity=8480-6$150||mm[Hg]", high)                               // đơn vị
	check("Observation?component-code-value-quantity=8480-6$150|http://wrong|mm[Hg]")                         // sai system đơn vị
	check("Observation?component-code-value-quantity=8480-6$gt100&code=85354-9", high, low, norm)             // kết hợp tham số thường
	check("Observation?combo-code-value-quantity=8480-6$gt140", high)                                         // combo: resource hoặc component
	check("Observation?code-value-quantity=85354-9$gt1")                                                      // không có giá trị ở mức resource
	check("Patient?_has:Observation:subject:component-code-value-quantity=8480-6$gt140")                      // composite trong _has
	e.mustDo(400, "GET", "Observation?component-code-value-quantity=8480-6", nil)                             // thiếu thành phần
	e.mustDo(400, "GET", "Observation?component-code-value-quantity=8480-6$gt1$x", nil)                       // thừa thành phần
	e.mustDo(400, "GET", "Observation?component-code-value-quantity:missing=true", nil)                       // modifier
	e.mustDo(400, "GET", "Observation?_sort=component-code-value-quantity", nil)                              // sort
	// sửa resource thì chỉ mục composite được lập lại
	cur := e.mustDo(200, "GET", "Observation/"+norm, nil).body
	cur["component"] = []any{comp("8480-6", 190), comp("8462-4", 70)}
	e.mustDo(200, "PUT", "Observation/"+norm, cur)
	check("Observation?component-code-value-quantity=8480-6$gt180", norm)
	check("Observation?component-code-value-quantity=8480-6$gt140", high, norm)
	e.mustDo(204, "DELETE", "Observation/"+norm, nil)
	check("Observation?component-code-value-quantity=8480-6$gt140", high)
}

func testChainedSearchAndHas(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	create := func(rt string, body map[string]any) string { return str(e.mustDo(201, "POST", rt, body).body, "id") }
	org := create("Organization", map[string]any{"resourceType": "Organization", "name": "Burgers Hospital"})
	other := create("Organization", map[string]any{"resourceType": "Organization", "name": "Elsewhere"})
	smith := create("Patient", patient("Smith", map[string]any{"gender": "male", "managingOrganization": map[string]any{"reference": "Organization/" + org}}))
	jones := create("Patient", patient("Jones", map[string]any{"gender": "female", "managingOrganization": map[string]any{"reference": "Organization/" + other}}))
	create("Patient", patient("Lonely", nil))
	obs := func(pid, code, status string) string {
		return create("Observation", map[string]any{"resourceType": "Observation", "status": status,
			"code":    map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": code}}},
			"subject": map[string]any{"reference": "Patient/" + pid}})
	}
	o1 := obs(smith, "1111", "final")
	o2 := obs(smith, "2222", "preliminary")
	o3 := obs(jones, "1111", "final")

	set := func(r reply) string {
		got := ids(r)
		sort.Strings(got)
		return strings.Join(got, ",")
	}
	sorted := func(xs ...string) string { sort.Strings(xs); return strings.Join(xs, ",") }
	check := func(path string, want ...string) {
		t.Helper()
		if got := set(e.mustDo(200, "GET", path, nil)); got != sorted(want...) {
			t.Errorf("%s = [%s], muốn [%s]", path, got, sorted(want...))
		}
	}
	// chained thuận
	check("Observation?subject:Patient.family=smith", o1, o2)
	check("Observation?patient.family=jones", o3) // tham số patient chỉ trỏ tới Patient nên không cần kiểu
	check("Observation?subject:Patient.family=smith&status=final", o1)
	check("Observation?subject:Patient.family=smith,jones", o1, o2, o3)
	check("Observation?subject:Patient.family=nobody")
	check("Observation?subject:Patient.gender=female&code=1111", o3)
	check("Patient?organization.name=burgers", smith)
	check("Patient?organization:Organization.name:contains=e&gender=female", jones)
	// chained lồng hai cấp: Observation -> Patient -> Organization
	check("Observation?subject:Patient.organization.name=burgers", o1, o2)
	check("Observation?subject:Patient.organization:Organization.name=elsewhere", o3)
	// _has
	check("Patient?_has:Observation:subject:code=1111", smith, jones)
	check("Patient?_has:Observation:subject:code=2222", smith)
	check("Patient?_has:Observation:patient:status=final&gender=female", jones)
	check("Patient?_has:Observation:subject:code=9999")
	check("Organization?_has:Patient:organization:family=smith", org)
	check("Organization?_has:Patient:organization:_has:Observation:subject:code=2222", org)
	check("Patient?_has:Observation:subject:code=1111&_has:Observation:subject:status=preliminary", smith)
	// kết hợp với phân trang và total
	r := e.mustDo(200, "GET", "Observation?subject:Patient.family=smith&_count=1&_total=accurate", nil)
	if len(entries(r)) != 1 || r.body["total"] != float64(2) {
		t.Errorf("phân trang chained: %s", r.raw)
	}
	var next string
	for _, l := range r.body["link"].([]any) {
		if lm := l.(map[string]any); lm["relation"] == "next" {
			next = strings.TrimPrefix(lm["url"].(string), base+"/")
		}
	}
	if next == "" {
		t.Fatalf("thiếu link next: %s", r.raw)
	}
	if page2 := e.mustDo(200, "GET", next, nil); len(entries(page2)) != 1 || ids(page2)[0] == ids(r)[0] {
		t.Errorf("trang 2 của chained: %s", page2.raw)
	}
	// POST _search và search có điều kiện dùng chained
	if len(entries(e.mustDo(200, "POST", "Observation/_search", "subject:Patient.family=jones", "Content-Type", "application/x-www-form-urlencoded"))) != 1 {
		t.Error("POST _search với chained")
	}
	e.mustDo(204, "DELETE", "Observation?subject:Patient.family=jones", nil)
	e.mustDo(410, "GET", "Observation/"+o3, nil)
	// sub-search khớp quá nhiều thì báo lỗi rõ ràng, không cắt âm thầm: bỏ qua ở đây (xem unit test giới hạn)
	// lỗi cú pháp
	e.mustDo(400, "GET", "Patient?_has:Observation:subject=1", nil)
	e.mustDo(400, "GET", "Observation?subject:Patient.nope=1", nil)
}

// ---------------------------------------------------------------- tranh chấp đồng thời

// Nền tảng của lease job và khóa lạc quan: nhiều bên cùng cập nhật với cùng If-Match thì ĐÚNG MỘT bên thắng, các bên còn
// lại nhận 412 (không phải lỗi 500, không ghi đè lên nhau).
func testConcurrentIfMatch(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	created := e.mustDo(201, "POST", "Patient", patient("Race", nil))
	id := str(created.body, "id")
	vid := str(created.body, "meta", "versionId")
	const n = 12
	statuses := make(chan int, n)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			body := patient("Racer"+strconv.Itoa(i), nil)
			body["id"] = id
			statuses <- e.do("PUT", "Patient/"+id, body, "If-Match", `W/"`+vid+`"`).status
		}(i)
	}
	start.Done()
	done.Wait()
	close(statuses)
	counts := map[int]int{}
	for st := range statuses {
		counts[st]++
	}
	if counts[200] != 1 || counts[412] != n-1 {
		t.Errorf("muốn 1 bên thắng (200) và %d bên nhận 412, được %v", n-1, counts)
	}
	h := e.mustDo(200, "GET", "Patient/"+id+"/_history", nil)
	if got := len(entries(h)); got != 2 {
		t.Errorf("history có %d phiên bản, muốn 2 (tạo + một lần cập nhật thắng)", got)
	}
}

// Create có điều kiện (If-None-Exist) chạy đồng thời với cùng tiêu chí không được tạo trùng. Nhiều vòng, mỗi vòng một tiêu
// chí mới, để cửa sổ tranh chấp (đã tìm thấy "chưa có" nhưng chưa tạo) có cơ hội xảy ra trên cả cơ sở dữ liệu nhanh.
func testConcurrentConditionalCreate(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	const n, rounds = 16, 8
	for round := 0; round < rounds; round++ {
		family := "OnlyOne" + strconv.Itoa(round)
		statuses := make(chan int, n)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := 0; i < n; i++ {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				statuses <- e.do("POST", "Patient", patient(family, nil), "If-None-Exist", "family="+family).status
			}()
		}
		start.Done()
		done.Wait()
		close(statuses)
		counts := map[int]int{}
		for st := range statuses {
			counts[st]++
		}
		total := e.mustDo(200, "GET", "Patient?family="+family+"&_summary=count", nil)
		if got := total.body["total"]; got != float64(1) || counts[201] != 1 || counts[200] != n-1 {
			t.Fatalf("vòng %d: muốn đúng 1 Patient được tạo (201) và %d lần trả bản có sẵn (200); search thấy %v, mã trả về %v", round, n-1, got, counts)
		}
	}
}

// ---------------------------------------------------------------- tranh chấp: update-as-create, update/delete có điều kiện

// concurrently chạy fn(i) cho i < n cùng lúc (xuất phát đồng loạt) và đếm theo mã trả về.
func concurrently(n int, fn func(i int) int) map[int]int {
	out := make(chan int, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			out <- fn(i)
		}(i)
	}
	start.Done()
	done.Wait()
	close(out)
	counts := map[int]int{}
	for st := range out {
		counts[st]++
	}
	return counts
}

// PUT Type/id (update-as-create) song song với cùng id chưa tồn tại: đúng một request tạo (201), các request còn lại cập nhật
// (200); không request nào nhận 404/409/5xx dù thua cuộc tranh tạo.
func testConcurrentUpdateAsCreate(t *testing.T, mk Factory) {
	repo := mk(t)
	r := fhirrouter.New(repo, fhirrouter.Options{BaseURL: base, UpdateAsCreate: true})
	e := &env{t, repo, r, http.StripPrefix("/fhir/R4", r.HTTPHandler())}
	const n, rounds = 16, 6
	for round := 0; round < rounds; round++ {
		id := "client-id-" + strconv.Itoa(round)
		counts := concurrently(n, func(i int) int {
			body := patient("Putter"+strconv.Itoa(i), nil)
			body["id"] = id
			return e.do("PUT", "Patient/"+id, body).status
		})
		if counts[201] != 1 || counts[200] != n-1 {
			t.Fatalf("vòng %d: muốn 1×201 và %d×200, được %v", round, n-1, counts)
		}
		h := e.mustDo(200, "GET", "Patient/"+id+"/_history", nil)
		if got := len(entries(h)); got != n {
			t.Errorf("vòng %d: history có %d phiên bản, muốn %d (1 tạo + %d cập nhật)", round, got, n, n-1)
		}
	}
}

// PUT Type?tiêu-chí song song khi chưa có resource khớp: đúng một request tạo (201), các request còn lại cập nhật (200).
func testConcurrentConditionalUpdate(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	const n, rounds = 16, 6
	for round := 0; round < rounds; round++ {
		family := "CondUpd" + strconv.Itoa(round)
		counts := concurrently(n, func(i int) int {
			body := patient(family, nil)
			body["gender"] = []string{"male", "female"}[i%2]
			return e.do("PUT", "Patient?family="+family, body).status
		})
		total := e.mustDo(200, "GET", "Patient?family="+family+"&_summary=count", nil)
		if total.body["total"] != float64(1) || counts[201] != 1 || counts[200] != n-1 {
			t.Fatalf("vòng %d: muốn 1 Patient (1×201, %d×200); search thấy %v, mã trả về %v", round, n-1, total.body["total"], counts)
		}
	}
}

// DELETE Type?tiêu-chí song song: mọi request đều 204 (xóa lặp lại là idempotent), resource biến mất.
func testConcurrentConditionalDelete(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	const n = 16
	for round := 0; round < 4; round++ {
		family := "CondDel" + strconv.Itoa(round)
		e.mustDo(201, "POST", "Patient", patient(family, nil))
		counts := concurrently(n, func(int) int { return e.do("DELETE", "Patient?family="+family, nil).status })
		total := e.mustDo(200, "GET", "Patient?family="+family+"&_summary=count", nil)
		if counts[204] != n || total.body["total"] != float64(0) {
			t.Fatalf("vòng %d: muốn %d×204 và 0 resource; mã trả về %v, còn %v", round, n, counts, total.body["total"])
		}
	}
}

// ---------------------------------------------------------------- CodeSystem/$import

func importParams(system string, params ...map[string]any) map[string]any {
	ps := []any{}
	if system != "" {
		ps = append(ps, map[string]any{"name": "system", "valueUri": system})
	}
	for _, p := range params {
		ps = append(ps, p)
	}
	return map[string]any{"resourceType": "Parameters", "parameter": ps}
}

func impConcept(code, display string) map[string]any {
	return map[string]any{"name": "concept", "valueCoding": map[string]any{"code": code, "display": display}}
}

func impPart(name string, parts map[string]string) map[string]any {
	var ps []any
	for k, v := range parts {
		pt := map[string]any{"name": k, "valueString": v}
		if k == "code" || k == "property" || k == "language" {
			pt = map[string]any{"name": k, "valueCode": v}
		}
		ps = append(ps, pt)
	}
	return map[string]any{"name": name, "part": ps}
}

func testCodeSystemImport(t *testing.T, mk Factory) {
	e := newEnv(t, mk)
	cs, ok := e.repo.(fhirrouter.ConceptStore)
	if !ok {
		t.Skip("repository không cài ConceptStore")
	}
	ctx := context.Background()
	created := e.mustDo(201, "POST", "CodeSystem", map[string]any{"resourceType": "CodeSystem", "url": "http://example.org/big",
		"status": "active", "content": "not-present", "hierarchyMeaning": "is-a",
		"property": []any{map[string]any{"code": "kind", "type": "string"}}})
	big := str(created.body, "id")

	body := importParams("http://example.org/big",
		impConcept("A", "Alpha"), impConcept("B", "Beta"),
		impPart("property", map[string]string{"code": "B", "property": "is-a", "value": "A"}),
		impPart("property", map[string]string{"code": "A", "property": "kind", "value": "root"}),
		impPart("designation", map[string]string{"code": "A", "language": "vi", "value": "Anpha"}))
	if r := e.mustDo(200, "POST", "CodeSystem/$import", body); str(r.body, "resourceType") != "Parameters" {
		t.Fatalf("kết quả: %v", r.body)
	}
	a, err := cs.GetConcept(ctx, big, "A")
	if err != nil || a == nil || a.Display != "Alpha" || len(a.Properties) != 1 || a.Properties[0].Type != "string" ||
		len(a.Designations) != 1 || a.Designations[0].Language != "vi" {
		t.Fatalf("concept A: %+v %v", a, err)
	}
	if b, _ := cs.GetConcept(ctx, big, "B"); b == nil || len(b.Properties) != 1 || b.Properties[0].Property != "is-a" || b.Properties[0].Type != "code" {
		t.Fatalf("concept B: %+v", b)
	}

	// idempotent, và đổi display thì ghi đè
	body = importParams("", impConcept("A", "Alpha 2"), impPart("property", map[string]string{"code": "A", "property": "kind", "value": "root"}))
	e.mustDo(200, "POST", "CodeSystem/"+big+"/$import", body)
	a, _ = cs.GetConcept(ctx, big, "A")
	if a.Display != "Alpha 2" || len(a.Properties) != 1 || len(a.Designations) != 1 {
		t.Fatalf("sau nạp lại: %+v", a)
	}

	// lỗi: property chưa khai báo, code chưa có, thiếu CodeSystem. Lỗi giữa chừng không để lại concept của lần gọi đó
	e.mustDo(400, "POST", "CodeSystem/$import", importParams("http://example.org/big", impConcept("C", "x"),
		impPart("property", map[string]string{"code": "C", "property": "nope", "value": "1"})))
	e.mustDo(400, "POST", "CodeSystem/$import", importParams("http://example.org/big", impConcept("D", "x"),
		impPart("designation", map[string]string{"code": "ZZ", "value": "1"})))
	if c, _ := cs.GetConcept(ctx, big, "D"); c != nil {
		if _, tx := e.repo.(fhirrouter.Transactional); tx {
			t.Fatalf("concept D còn lại sau lỗi: %+v", c)
		}
	}
	e.mustDo(400, "POST", "CodeSystem/$import", importParams("", impConcept("E", "x")))
	e.mustDo(404, "POST", "CodeSystem/missing/$import", importParams("", impConcept("E", "x")))

	// xóa hẳn CodeSystem thì concept đi theo
	e.mustDo(200, "POST", "CodeSystem/"+big+"/$expunge", map[string]any{"resourceType": "Parameters"})
	if a, _ := cs.GetConcept(ctx, big, "A"); a != nil {
		t.Fatalf("concept còn sau expunge: %+v", a)
	}
}
