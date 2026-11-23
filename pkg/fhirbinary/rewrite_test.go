package fhirbinary_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirbinary"
)

const (
	fb = "https://h.example/fhir/R4"
	sb = "https://h.example/storage"
)

func unit(t *testing.T) (*env, *fhirbinary.Rewriter) {
	e := newEnv(t, nil)
	return e, fhirbinary.NewRewriter(fhirbinary.RewriteConfig{FHIRBaseURL: fb, StorageBaseURL: sb}, e.caller, e.signer)
}

func parseDoc(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func same(a, b any) bool { // cùng một đối tượng (con trỏ map/slice)
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() != vb.Kind() || (va.Kind() != reflect.Map && va.Kind() != reflect.Slice) {
		return false
	}
	return va.Pointer() == vb.Pointer()
}

func TestToReferenceForms(t *testing.T) {
	_, rw := unit(t)
	for in, want := range map[string]string{
		"Binary/abc":                    "Binary/abc",
		"Binary/abc/_history/2":         "Binary/abc",
		fb + "/Binary/abc":              "Binary/abc",
		fb + "/Binary/abc/_history/v-2": "Binary/abc",
		fb + "/Binary/abc?x=1":          "Binary/abc",
		sb + "/abc/ver1":                "Binary/abc",
		sb + "/abc":                     "Binary/abc",
		sb + "/abc/ver1?Expires=1&Signature=a%2Bb%3D": "Binary/abc",
		sb + "/abc?Expires=1":                         "Binary/abc",
	} {
		out, changed := rw.ToReference(map[string]any{"url": in})
		got := out.(map[string]any)["url"]
		if got != want || changed != (in != want) {
			t.Errorf("%q → %v (changed=%v), muốn %q", in, got, changed, want)
		}
	}
	// không phải URL Binary: giữ nguyên
	for _, in := range []string{"", "http://other.example/Binary/abc", "https://h.example/fhir/R4/Patient/abc", "Binary", "Binary/", "Binary//x", "Binary/a/b",
		"Binary/../x", "Binary/a b", "Binary/abc/_history/..", "Binary/abc/_history/a b", sb + "/abc/..", sb + "/abc/v 1", "XBinary/abc", fb + "/Binary/abc/_foo/1", sb + "/a/b/c", "https://h.example/storage2/abc/v", "binary/abc"} {
		if out, changed := rw.ToReference(map[string]any{"url": in}); changed || out.(map[string]any)["url"] != in {
			t.Errorf("%q bị đổi thành %v", in, out)
		}
	}
}

func TestRewriteKeysAndStructure(t *testing.T) {
	_, rw := unit(t)
	doc := parseDoc(t, `{"resourceType":"DocumentReference","content":[{"attachment":{"url":"`+fb+`/Binary/a1","contentType":"x"}},{"attachment":{"url":"http://other/x"}}],
	  "extra":{"path":"Binary/a2","valueUri":"Binary/a3","fullUrl":"Binary/a4","other":"Binary/a5"}}`)
	out, changed := rw.ToReference(doc)
	if !changed {
		t.Fatal("phải đổi")
	}
	b, _ := json.Marshal(out)
	s := string(b)
	for _, want := range []string{`"url":"Binary/a1"`, `"path":"Binary/a2"`, `"valueUri":"Binary/a3"`, `"fullUrl":"Binary/a4"`, `"other":"Binary/a5"`, `"url":"http://other/x"`} {
		if !strings.Contains(s, want) {
			t.Errorf("thiếu %s trong %s", want, s)
		}
	}
	// chỉ `url` và `path` được viết lại: các khóa khác dù giá trị giống Binary/<id> thì giữ nguyên
	doc2 := parseDoc(t, `{"valueUri":"`+fb+`/Binary/x","fullUrl":"`+fb+`/Binary/y","reference":"`+fb+`/Binary/z"}`)
	if out, changed := rw.ToReference(doc2); changed || !same(out, doc2) {
		t.Fatalf("khóa khác bị đổi: %v", out)
	}
	// giá trị không phải chuỗi
	doc3 := parseDoc(t, `{"url":123,"path":{"url":"`+fb+`/Binary/n1"}}`)
	out3, changed3 := rw.ToReference(doc3)
	if !changed3 || out3.(map[string]any)["url"] != float64(123) || out3.(map[string]any)["path"].(map[string]any)["url"] != "Binary/n1" {
		t.Fatalf("%v", out3)
	}
}

func TestRewriteIdentityAndNoMutation(t *testing.T) {
	_, rw := unit(t)
	if out, changed := rw.ToReference(nil); changed || out != nil {
		t.Fatal("nil")
	}
	none := parseDoc(t, `{"resourceType":"Patient","name":[{"family":"x"}],"photo":[{"url":"http://other/p.png"}]}`)
	if out, changed := rw.ToReference(none); changed || !same(out, none) {
		t.Fatal("không có URL Binary phải trả đúng đối tượng đầu vào")
	}
	in := parseDoc(t, `{"a":[{"url":"`+fb+`/Binary/q1"},{"url":"keep"}],"b":{"url":"Binary/q2/_history/3"}}`)
	before, _ := json.Marshal(in)
	out, changed := rw.ToReference(in)
	after, _ := json.Marshal(in)
	if !changed || string(before) != string(after) {
		t.Fatalf("đầu vào bị sửa:\n%s\n%s", before, after)
	}
	ob, _ := json.Marshal(out)
	if strings.Contains(string(ob), "fhir/R4") {
		t.Fatalf("đầu ra chưa đổi: %s", ob)
	}
}

func TestRewriteSkipsBinaryResourcesAndBundleRequests(t *testing.T) {
	_, rw := unit(t)
	bin := parseDoc(t, `{"resourceType":"Binary","contentType":"x","url":"`+fb+`/Binary/inner"}`)
	if out, changed := rw.ToReference(bin); changed || !same(out, bin) {
		t.Fatal("không viết lại bên trong Binary")
	}
	bundle := parseDoc(t, `{"resourceType":"Bundle","entry":[
	  {"fullUrl":"urn:uuid:1","resource":{"resourceType":"Binary","url":"`+fb+`/Binary/in2"},"request":{"method":"PUT","url":"`+fb+`/Binary/in2"}},
	  {"resource":{"resourceType":"DocumentReference","content":[{"attachment":{"url":"`+fb+`/Binary/in3"}}]},"request":{"method":"POST","url":"DocumentReference"}}]}`)
	out, changed := rw.ToReference(bundle)
	if !changed {
		t.Fatal("DocumentReference trong Bundle phải được viết lại")
	}
	b, _ := json.Marshal(out)
	s := string(b)
	if !strings.Contains(s, `"request":{"method":"PUT","url":"`+fb+`/Binary/in2"}`) || !strings.Contains(s, `"resourceType":"Binary","url":"`+fb+`/Binary/in2"`) {
		t.Fatalf("Binary hoặc request bị đụng: %s", s)
	}
	if !strings.Contains(s, `"url":"Binary/in3"`) {
		t.Fatalf("attachment chưa đổi: %s", s)
	}
}

func TestToPresigned(t *testing.T) {
	e, rw := unit(t)
	id, v1 := e.created("text/plain", "one")
	o, _ := e.wrapped.UpdateResource(context.Background(), &fhir.Binary{ID: id, ContentType: "text/plain", Data: b64("two")}, updOpts)
	v2 := o.GetMeta().VersionId

	signedOK := func(s, wantVer string) {
		t.Helper()
		if !strings.HasPrefix(s, e.srv.URL+"/storage/"+id+"/"+wantVer+"?") {
			t.Fatalf("không phải URL ký của %s/%s: %s", id, wantVer, s)
		}
		if g := e.do("GET", s, nil, nil); g.status != 200 {
			t.Fatalf("URL ký không dùng được: %d", g.status)
		}
	}
	get := func(doc any) any { out, _ := rw.ToPresigned(context.Background(), doc); return out }

	// tham chiếu không version → bản hiện tại
	out := get(map[string]any{"url": "Binary/" + id}).(map[string]any)
	signedOK(out["url"].(string), v2)
	// có version → đúng phiên bản đó
	out = get(map[string]any{"url": "Binary/" + id + "/_history/" + v1}).(map[string]any)
	signedOK(out["url"].(string), v1)
	// URL FHIR và URL kho (không version) cũng được nhận ra
	out = get(map[string]any{"url": fb + "/Binary/" + id}).(map[string]any)
	signedOK(out["url"].(string), v2)
	out = get(map[string]any{"path": sb + "/" + id + "/" + v1}).(map[string]any)
	signedOK(out["path"].(string), v1)

	// nhất quán: cùng Binary thì cùng URL trong một lần viết lại
	two := get(parseDoc(t, `{"a":{"url":"Binary/`+id+`"},"b":[{"url":"Binary/`+id+`"}]}`)).(map[string]any)
	if two["a"].(map[string]any)["url"] != two["b"].([]any)[0].(map[string]any)["url"] {
		t.Fatal("cùng Binary phải ra cùng URL")
	}

	// Binary không tồn tại / đã xóa → Binary/<id>
	if out := get(map[string]any{"url": fb + "/Binary/ghost"}).(map[string]any); out["url"] != "Binary/ghost" {
		t.Fatalf("không tồn tại: %v", out)
	}
	id2, _ := e.created("text/plain", "x")
	_ = e.repo.DeleteResource(context.Background(), "Binary", id2)
	if out := get(map[string]any{"url": fb + "/Binary/" + id2}).(map[string]any); out["url"] != "Binary/"+id2 {
		t.Fatalf("đã xóa: %v", out)
	}
	if out := get(map[string]any{"url": "Binary/" + id + "/_history/nope"}).(map[string]any); out["url"] != "Binary/"+id {
		t.Fatalf("phiên bản không có: %v", out)
	}

	// không đọc được Binary → giữ nguyên (không lộ rằng nó tồn tại)
	e.caller.deny["Binary"] = true
	in := map[string]any{"url": fb + "/Binary/" + id}
	if out, changed := rw.ToPresigned(context.Background(), in); changed || !same(out, in) {
		t.Fatalf("thiếu quyền đọc: %v", out)
	}
	e.caller.deny["Binary"] = false

	// URL không phải Binary, và Binary trong resource Binary: không đổi
	other := parseDoc(t, `{"url":"http://other/x"}`)
	if out, changed := rw.ToPresigned(context.Background(), other); changed || !same(out, other) {
		t.Fatal("URL ngoài bị đổi")
	}
}

// Bốn kiểu người gọi (rewrite.test.ts:292): đọc được cả hai → viết lại; thiếu quyền trên Patient (securityContext) → giữ; thiếu quyền Binary → giữ.
func TestToPresignedWithSecurityContext(t *testing.T) {
	e, rw := unit(t)
	if _, err := e.repo.CreateResource(context.Background(), &fhir.Patient{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.wrapped.CreateResource(context.Background(), &fhir.Binary{ID: "sc", ContentType: "text/plain", Data: b64("x"), SecurityContext: &fhir.Reference{Reference: "Patient/p1"}}); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"url": "Binary/sc"}
	run := func(deny ...string) (any, bool) {
		for k := range e.caller.deny { // sửa tại chỗ: Rewriter giữ bản sao của denyRepo nhưng chung map
			delete(e.caller.deny, k)
		}
		for _, d := range deny {
			e.caller.deny[d] = true
		}
		return rw.ToPresigned(context.Background(), in)
	}
	if out, changed := run(); !changed || !strings.Contains(out.(map[string]any)["url"].(string), "/storage/sc/") {
		t.Fatalf("đọc được cả hai: %v", out)
	}
	if out, changed := run("Patient"); changed || !same(out, in) {
		t.Fatalf("thiếu quyền Patient: %v", out)
	}
	if out, changed := run("Binary"); changed || !same(out, in) {
		t.Fatalf("thiếu quyền Binary: %v", out)
	}
	if out, changed := run("Patient", "Binary"); changed || !same(out, in) {
		t.Fatalf("thiếu cả hai: %v", out)
	}
}

func TestToPresignedWithoutSignerOrRepoIsNoop(t *testing.T) {
	e, _ := unit(t)
	in := map[string]any{"url": "Binary/x"}
	for _, rw := range []*fhirbinary.Rewriter{
		fhirbinary.NewRewriter(fhirbinary.RewriteConfig{FHIRBaseURL: fb}, nil, e.signer),
		fhirbinary.NewRewriter(fhirbinary.RewriteConfig{FHIRBaseURL: fb}, e.caller, nil),
	} {
		if out, changed := rw.ToPresigned(context.Background(), in); changed || !same(out, in) {
			t.Fatal("thiếu phụ thuộc thì không được đổi gì")
		}
	}
}

func docRef(url string) []byte {
	return []byte(`{"resourceType":"DocumentReference","status":"current","content":[{"attachment":{"contentType":"text/plain","url":"` + url + `"}}]}`)
}

func attachmentURL(r resp) string {
	return r.json()["content"].([]any)[0].(map[string]any)["attachment"].(map[string]any)["url"].(string)
}

// Toàn tuyến qua HTTP: ghi lưu `Binary/<id>`, đọc trả URL ký dùng được.
func TestRewriteEndToEndOverHTTP(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "attached file")
	hdr := map[string]string{"Content-Type": "application/fhir+json"}
	w := e.do("POST", "/fhir/R4/DocumentReference", hdr, docRef("Binary/"+id))
	if w.status != 201 {
		t.Fatalf("%d %s", w.status, w.body)
	}
	if u := attachmentURL(w); !strings.HasPrefix(u, e.srv.URL+"/storage/"+id+"/") {
		t.Fatalf("phản hồi tạo phải mang URL ký: %s", u)
	}
	drID := w.json()["id"].(string)
	// CSDL giữ tham chiếu, không giữ URL có hạn
	stored, _ := e.repo.ReadResource(context.Background(), "DocumentReference", drID)
	raw, _ := json.Marshal(stored)
	if !strings.Contains(string(raw), `"url":"Binary/`+id+`"`) || strings.Contains(string(raw), "Signature") {
		t.Fatalf("CSDL: %s", raw)
	}
	// đọc: URL ký dùng được
	r := e.do("GET", "/fhir/R4/DocumentReference/"+drID, nil, nil)
	u := attachmentURL(r)
	if g := e.do("GET", u, nil, nil); g.status != 200 || string(g.body) != "attached file" {
		t.Fatalf("URL ký: %d %q", g.status, g.body)
	}
	// khứ hồi: client gửi lại đúng resource đã đọc (mang URL ký) → CSDL vẫn là Binary/<id>
	upd := e.do("PUT", "/fhir/R4/DocumentReference/"+drID, hdr, docRef(u))
	if upd.status != 200 {
		t.Fatalf("%d %s", upd.status, upd.body)
	}
	stored, _ = e.repo.ReadResource(context.Background(), "DocumentReference", drID)
	raw, _ = json.Marshal(stored)
	if !strings.Contains(string(raw), `"url":"Binary/`+id+`"`) || strings.Contains(string(raw), "Signature") {
		t.Fatalf("sau khứ hồi: %s", raw)
	}
	// search: mỗi entry được viết lại
	s := e.do("GET", "/fhir/R4/DocumentReference", nil, nil)
	ent := s.json()["entry"].([]any)[0].(map[string]any)["resource"].(map[string]any)
	if !strings.Contains(ent["content"].([]any)[0].(map[string]any)["attachment"].(map[string]any)["url"].(string), "/storage/"+id+"/") {
		t.Fatalf("search: %s", s.body)
	}
	// URL ngoài giữ nguyên
	o := e.do("POST", "/fhir/R4/DocumentReference", hdr, docRef("https://cdn.example/file.txt"))
	if attachmentURL(o) != "https://cdn.example/file.txt" {
		t.Fatalf("URL ngoài: %s", o.body)
	}
	// Binary biến mất: đọc ra Binary/<id>
	_ = e.repo.DeleteResource(context.Background(), "Binary", id)
	r = e.do("GET", "/fhir/R4/DocumentReference/"+drID, nil, nil)
	if attachmentURL(r) != "Binary/"+id {
		t.Fatalf("Binary đã xóa: %s", attachmentURL(r))
	}
}

func TestRewriteInTransactionBundle(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "tx file")
	bundle := `{"resourceType":"Bundle","type":"transaction","entry":[{"request":{"method":"POST","url":"DocumentReference"},"resource":` +
		string(docRef(e.srv.URL+"/fhir/R4/Binary/"+id)) + `}]}`
	r := e.do("POST", "/fhir/R4", map[string]string{"Content-Type": "application/fhir+json"}, []byte(bundle))
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	srch, _ := e.repo.ReadHistory(context.Background(), "Binary", id)
	_ = srch
	res := e.do("GET", "/fhir/R4/DocumentReference", nil, nil).json()["entry"].([]any)
	if len(res) != 1 {
		t.Fatalf("%d DocumentReference", len(res))
	}
	got, _ := e.repo.ReadResource(context.Background(), "DocumentReference", res[0].(map[string]any)["resource"].(map[string]any)["id"].(string))
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"url":"Binary/`+id+`"`) {
		t.Fatalf("CSDL: %s", raw)
	}
}

func TestRewriteOffLeavesDataAlone(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.noRewrite = true })
	id, _ := e.created("text/plain", "x")
	w := e.do("POST", "/fhir/R4/DocumentReference", map[string]string{"Content-Type": "application/fhir+json"}, docRef("Binary/"+id))
	if attachmentURL(w) != "Binary/"+id {
		t.Fatalf("tắt rewrite mà vẫn đổi: %s", attachmentURL(w))
	}
}

var _ = http.StatusOK
var _ = httptest.NewRecorder
