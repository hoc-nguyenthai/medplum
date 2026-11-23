package fhirbinary_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
)

func presigned(t *testing.T, e *env, id, query string) (resp, string) {
	t.Helper()
	r := e.do("GET", "/fhir/R4/Binary/"+id+"/$presigned-url"+query, nil, nil)
	if r.status != 200 {
		return r, ""
	}
	if r.json()["resourceType"] != "Parameters" {
		t.Fatalf("không phải Parameters: %s", r.body)
	}
	ps := r.json()["parameter"].([]any)
	p := ps[0].(map[string]any)
	if p["name"] != "url" {
		t.Fatalf("tham số: %v", p)
	}
	return r, p["valueUri"].(string)
}

func TestPresignedURLFullCycle(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "original")
	_, rd := presigned(t, e, id, "")
	if g := e.do("GET", rd, nil, nil); g.status != 200 || string(g.body) != "original" {
		t.Fatalf("GET url đọc = %d %q", g.status, g.body)
	}
	_, up := presigned(t, e, id, "?upload=true")
	if p := e.do("PUT", up, map[string]string{"Content-Type": "text/plain"}, []byte("uploaded")); p.status != 200 {
		t.Fatalf("PUT url tải lên = %d", p.status)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != "uploaded" {
		t.Fatalf("sau PUT: %q", g.body)
	}
	// URL đọc không dùng để ghi và ngược lại
	if p := e.do("PUT", rd, map[string]string{"Content-Type": "text/plain"}, []byte("x")); p.status != 401 {
		t.Fatalf("PUT bằng URL đọc: %d", p.status)
	}
	if g := e.do("GET", up, nil, nil); g.status != 401 {
		t.Fatalf("GET bằng URL tải lên: %d", g.status)
	}
	// upload=false như không có
	if _, u := presigned(t, e, id, "?upload=false"); !strings.Contains(u, "/storage/"+id+"/") {
		t.Fatal(u)
	}
}

func TestPresignedURLCarriesProjectAndCurrentVersion(t *testing.T) {
	e := newEnv(t, nil)
	v1 := e.seed("b1", "text/plain", "one", "proj-a")
	o, _ := e.wrapped.UpdateResource(context.Background(), &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("two"), Meta: &fhir.Meta{Project: "proj-a"}}, updOpts)
	v2 := o.GetMeta().VersionId
	_, u := presigned(t, e, "b1", "")
	pu, _ := url.Parse(u)
	if pu.Query().Get("Project") != "proj-a" || !strings.Contains(pu.Path, "/b1/"+v2) || strings.Contains(pu.Path, v1) {
		t.Fatalf("url: %s", u)
	}
	if g := e.do("GET", u, nil, nil); string(g.body) != "two" {
		t.Fatalf("%q", g.body)
	}
}

func TestPresignedURLErrors(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "x")
	if r, _ := presigned(t, e, "ghost", ""); r.status != 404 {
		t.Fatalf("id lạ: %d", r.status)
	}
	if r, _ := presigned(t, e, id, "?upload=maybe"); r.status != 400 {
		t.Fatalf("upload sai: %d", r.status)
	}
	e.caller.deny["Binary"] = true
	if r, _ := presigned(t, e, id, ""); r.status != 403 {
		t.Fatalf("không có quyền đọc Binary: %d", r.status)
	}
	if r, _ := presigned(t, e, id, "?upload=true"); r.status != 403 {
		t.Fatalf("không có quyền đọc Binary (upload): %d", r.status)
	}
}

func TestPresignedUploadNeedsUpdatePermission(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.cannotUpdate = true })
	id, _ := e.created("text/plain", "x")
	if r, u := presigned(t, e, id, ""); r.status != 200 || u == "" {
		t.Fatalf("URL đọc phải được cấp: %d", r.status)
	}
	if r, _ := presigned(t, e, id, "?upload=true"); r.status != 403 {
		t.Fatalf("URL tải lên khi thiếu quyền cập nhật: %d", r.status)
	}
}

func TestPresignedURLChecksSecurityContext(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.repo.CreateResource(context.Background(), &fhir.Patient{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	id, _ := e.created("text/plain", "secret")
	r := e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "application/fhir+json"},
		[]byte(`{"resourceType":"Binary","id":"`+id+`","contentType":"text/plain","securityContext":{"reference":"Patient/p1"}}`))
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r, _ := presigned(t, e, id, ""); r.status != 200 {
		t.Fatalf("đọc được Patient thì được cấp URL: %d", r.status)
	}
	e.caller.deny["Patient"] = true
	for _, q := range []string{"", "?upload=true"} {
		if r, _ := presigned(t, e, id, q); r.status != 403 {
			t.Fatalf("không đọc được Patient (%q): %d", q, r.status)
		}
	}
	// đọc thô cũng bị chặn
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); g.status != 403 {
		t.Fatalf("GET thô khi không đọc được securityContext: %d", g.status)
	}
	// tham chiếu lạ dạng: mặc định từ chối
	e.caller.deny["Patient"] = false
	r = e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "application/fhir+json"},
		[]byte(`{"resourceType":"Binary","id":"`+id+`","contentType":"text/plain","securityContext":{"reference":"http://evil/Patient/p1"}}`))
	if r.status != 200 {
		t.Fatalf("%d", r.status)
	}
	if r, _ := presigned(t, e, id, ""); r.status != 403 {
		t.Fatalf("tham chiếu không dạng Type/id: %d", r.status)
	}
	// securityContext trỏ tới resource không tồn tại: từ chối
	e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "application/fhir+json"},
		[]byte(`{"resourceType":"Binary","id":"`+id+`","contentType":"text/plain","securityContext":{"reference":"Patient/ghost"}}`))
	if r, _ := presigned(t, e, id, ""); r.status != 403 {
		t.Fatalf("securityContext không tồn tại: %d", r.status)
	}
}
