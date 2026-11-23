package fhirbinary_test

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
)

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

func zl(s string) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

func TestRawCreateAndRead(t *testing.T) {
	e := newEnv(t, nil)
	r := e.post("text/plain", []byte("Hello world"))
	if r.status != 201 || r.h.Get("Location") == "" {
		t.Fatalf("POST = %d %s %v", r.status, r.body, r.h)
	}
	m := r.json()
	id := m["id"].(string)
	if m["contentType"] != "text/plain" || m["data"] != nil {
		t.Fatalf("resource: %v", m)
	}
	if u, _ := m["url"].(string); !strings.HasPrefix(u, e.srv.URL+"/storage/"+id+"/") {
		t.Fatalf("url: %v", m["url"])
	}
	g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil)
	if g.status != 200 || string(g.body) != "Hello world" || g.h.Get("Content-Type") != "text/plain" {
		t.Fatalf("GET thô = %d %q %v", g.status, g.body, g.h)
	}
	if g.h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(g.h.Get("Content-Security-Policy"), "sandbox") || g.h.Get("Content-Disposition") != "attachment" {
		t.Fatalf("header tải xuống: %v", g.h)
	}
	for _, accept := range []string{"application/fhir+json", "application/json", "application/fhir+json; fhirVersion=4.0"} {
		j := e.do("GET", "/fhir/R4/Binary/"+id, map[string]string{"Accept": accept}, nil)
		if j.status != 200 || j.json()["resourceType"] != "Binary" || j.json()["contentType"] != "text/plain" || j.json()["data"] != nil {
			t.Fatalf("Accept %q: %d %s", accept, j.status, j.body)
		}
	}
	if j := e.do("GET", "/fhir/R4/Binary/"+id+"?_format=json", nil, nil); j.json()["resourceType"] != "Binary" {
		t.Fatalf("_format: %s", j.body)
	}
}

func TestRawReadNotFoundAndWithoutContent(t *testing.T) {
	e := newEnv(t, nil)
	if r := e.do("GET", "/fhir/R4/Binary/nope", nil, nil); r.status != 404 || r.json()["resourceType"] != "OperationOutcome" {
		t.Fatalf("không có Binary: %d %s", r.status, r.body)
	}
	if _, err := e.wrapped.CreateResource(t.Context(), binaryNoContent("nc")); err != nil {
		t.Fatal(err)
	}
	r := e.do("GET", "/fhir/R4/Binary/nc", nil, nil)
	if r.status != 404 || r.json()["resourceType"] != "OperationOutcome" || !strings.Contains(r.h.Get("Content-Type"), "fhir+json") || r.h.Get("ETag") != "" || r.h.Get("Last-Modified") != "" {
		t.Fatalf("không có nội dung: %d %s %v", r.status, r.body, r.h)
	}
}

func TestRawReadLegacyDataInDB(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.repo.CreateResource(t.Context(), binaryWithData("old", "application/fhir+ndjson", base64.StdEncoding.EncodeToString([]byte("{\"a\":1}\n")))); err != nil {
		t.Fatal(err)
	}
	r := e.do("GET", "/fhir/R4/Binary/old", map[string]string{"Accept": "application/fhir+ndjson"}, nil)
	if r.status != 200 || string(r.body) != "{\"a\":1}\n" || r.h.Get("Content-Type") != "application/fhir+ndjson" {
		t.Fatalf("%d %q %v", r.status, r.body, r.h)
	}
	// data hỏng thì 404 (như Medplum), không rò nội dung
	if _, err := e.repo.CreateResource(t.Context(), binaryWithData("bad", "text/plain", "!!!")); err != nil {
		t.Fatal(err)
	}
	if r := e.do("GET", "/fhir/R4/Binary/bad", nil, nil); r.status != 404 {
		t.Fatalf("data hỏng: %d", r.status)
	}
}

func TestRawReadStoreErrorIsNotFoundWithoutLeak(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "x")
	e.store.failOpen.Store(true)
	r := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil)
	if r.status != 404 || strings.Contains(string(r.body), "secret") || strings.Contains(string(r.body), "disk") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestRawUpdate(t *testing.T) {
	e := newEnv(t, nil)
	id, v1 := e.created("text/plain", "Hello world")
	r := e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "text/plain"}, []byte("Updated"))
	if r.status != 200 {
		t.Fatalf("PUT = %d %s", r.status, r.body)
	}
	v2 := r.json()["meta"].(map[string]any)["versionId"].(string)
	if v1 == v2 {
		t.Fatal("versionId phải đổi")
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != "Updated" {
		t.Fatalf("GET = %q", g.body)
	}
	if old, _ := e.stored(id, v1); old != "Hello world" {
		t.Fatalf("phiên bản cũ bị đổi: %q", old)
	}
	// đổi loại nội dung qua PUT
	r = e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "application/octet-stream"}, []byte{1, 2, 3})
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); g.status != 200 || !bytes.Equal(g.body, []byte{1, 2, 3}) || g.h.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("GET = %d %v", g.status, g.h)
	}
	// PUT id lạ: 404 khi chưa bật update-as-create
	if r := e.do("PUT", "/fhir/R4/Binary/ghost", map[string]string{"Content-Type": "text/plain"}, []byte("x")); r.status != 404 {
		t.Fatalf("PUT id lạ = %d", r.status)
	}
	if _, ok := e.stored("ghost", "1"); ok || e.store.Len() != 3 {
		t.Fatalf("PUT lỗi vẫn ghi kho (len=%d)", e.store.Len())
	}
}

func TestRawDefaultContentType(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do("POST", "/fhir/R4/Binary", nil, []byte{9, 8, 7})
	if r.status != 201 || r.json()["contentType"] != "application/octet-stream" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestRawContentEncodings(t *testing.T) {
	e := newEnv(t, nil)
	for name, tc := range map[string]struct {
		enc  string
		body []byte
	}{"gzip": {"gzip", gz("Hello world")}, "GZIP hoa": {"GZIP", gz("Hello world")}, "deflate": {"deflate", zl("Hello world")}} {
		r := e.do("POST", "/fhir/R4/Binary", map[string]string{"Content-Type": "text/plain", "Content-Encoding": tc.enc}, tc.body)
		if r.status != 201 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
		if g := e.do("GET", "/fhir/R4/Binary/"+r.json()["id"].(string), nil, nil); string(g.body) != "Hello world" {
			t.Fatalf("%s: %q", name, g.body)
		}
	}
	id, _ := e.created("text/plain", "old")
	if r := e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "text/plain", "Content-Encoding": "gzip"}, gz("new")); r.status != 200 {
		t.Fatalf("PUT gzip: %d", r.status)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != "new" {
		t.Fatalf("%q", g.body)
	}
}

func TestRawRejectsBadEncodingWithoutOrphan(t *testing.T) {
	e := newEnv(t, nil)
	n0 := e.binaryCount()
	for _, enc := range []string{"fake", "identity", "br", "gzip, deflate"} {
		if r := e.do("POST", "/fhir/R4/Binary", map[string]string{"Content-Type": "text/plain", "Content-Encoding": enc}, []byte("x")); r.status != 400 || !strings.Contains(string(r.body), "Unsupported content encoding") {
			t.Errorf("%q: %d %s", enc, r.status, r.body)
		}
	}
	// gzip hỏng (header sai) và hỏng giữa chừng
	if r := e.do("POST", "/fhir/R4/Binary", map[string]string{"Content-Type": "text/plain", "Content-Encoding": "gzip"}, []byte("not gzip")); r.status != 400 {
		t.Errorf("header gzip sai: %d", r.status)
	}
	truncated := gz(strings.Repeat("abcdefgh", 5000))
	truncated = truncated[:len(truncated)/2]
	if r := e.do("POST", "/fhir/R4/Binary", map[string]string{"Content-Type": "text/plain", "Content-Encoding": "gzip"}, truncated); r.status != 400 {
		t.Errorf("gzip cụt: %d %s", r.status, r.body)
	}
	if n := e.binaryCount(); n != n0 || e.store.Len() != 0 {
		t.Fatalf("còn Binary mồ côi: %d resource, %d đối tượng kho", n-n0, e.store.Len())
	}
}

func TestRawCompressionBombIsRejected(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.maxUpload = 10_000 })
	bomb := gz(strings.Repeat("A", 5_000_000)) // vài KB nén, 5 MB giải nén
	if len(bomb) > 10_000 {
		t.Fatalf("bom không đủ nhỏ: %d", len(bomb))
	}
	r := e.do("POST", "/fhir/R4/Binary", map[string]string{"Content-Type": "text/plain", "Content-Encoding": "gzip"}, bomb)
	if r.status != 413 {
		t.Fatalf("bom nén = %d %s", r.status, r.body)
	}
	if e.binaryCount() != 0 || e.store.Len() != 0 {
		t.Fatal("bom để lại Binary hoặc nội dung")
	}
	// PUT: phiên bản mới giữ nội dung cũ (không cụt, không rỗng)
	e2 := newEnv(t, func(o *envOpts) { o.maxUpload = 10_000 })
	id, _ := e2.created("text/plain", "keep me")
	if r := e2.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "text/plain", "Content-Encoding": "gzip"}, bomb); r.status != 413 {
		t.Fatalf("PUT bom = %d", r.status)
	}
	if g := e2.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != "keep me" {
		t.Fatalf("nội dung sau PUT lỗi: %q", g.body)
	}
}

func TestRawSizeLimits(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.maxUpload = 100 })
	if r := e.post("text/plain", bytes.Repeat([]byte("a"), 100)); r.status != 201 {
		t.Fatalf("đúng trần: %d", r.status)
	}
	n := e.binaryCount()
	if r := e.post("text/plain", bytes.Repeat([]byte("a"), 101)); r.status != 413 {
		t.Fatalf("vượt trần: %d", r.status)
	}
	if e.binaryCount() != n {
		t.Fatal("Content-Length vượt trần vẫn tạo Binary")
	}
}

func TestRawBlockedTypesCreateNoOrphan(t *testing.T) {
	e := newEnv(t, nil)
	if r := e.post("application/x-msdownload", []byte("MZ")); r.status != 400 || r.json()["issue"].([]any)[0].(map[string]any)["code"] != "invalid" {
		t.Fatalf("loại chặn: %d %s", r.status, r.body)
	}
	if r := e.post("application/x-sh; charset=utf-8", []byte("#!")); r.status != 400 {
		t.Fatalf("loại chặn kèm tham số: %d", r.status)
	}
	r := e.do("POST", "/fhir/R4/Binary?_filename=foo.exe", map[string]string{"Content-Type": "text/plain"}, []byte("x"))
	if r.status != 400 {
		t.Fatalf("đuôi chặn: %d", r.status)
	}
	if r := e.do("POST", "/fhir/R4/Binary?_filename=foo.EXE.", map[string]string{"Content-Type": "text/plain"}, []byte("x")); r.status != 400 {
		t.Fatalf("đuôi chặn kèm dấu chấm: %d", r.status)
	}
	if r := e.do("POST", "/fhir/R4/Binary?_filename=report.pdf", map[string]string{"Content-Type": "application/pdf"}, []byte("%PDF")); r.status != 201 {
		t.Fatalf("tệp hợp lệ: %d", r.status)
	}
	if n := e.binaryCount(); n != 1 {
		t.Fatalf("có %d Binary, phải đúng 1", n)
	}
}

func TestRawJSONBinaryResource(t *testing.T) {
	e := newEnv(t, nil)
	data := base64.StdEncoding.EncodeToString([]byte("Hello World!"))
	for _, ct := range []string{"application/fhir+json", "application/json", "application/fhir+json; charset=utf-8"} {
		body := fmt.Sprintf(`{"resourceType":"Binary","contentType":"text/plain","data":%q}`, data)
		r := e.post(ct, []byte(body))
		if r.status != 201 || r.json()["resourceType"] != "Binary" {
			t.Fatalf("%s: %d %s", ct, r.status, r.body)
		}
		id := r.json()["id"].(string)
		if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != "Hello World!" {
			t.Fatalf("%s: GET = %q", ct, g.body)
		}
	}
	// base64 sai thì 400 và không có Binary nào thêm
	n := e.binaryCount()
	if r := e.post("application/fhir+json", []byte(`{"resourceType":"Binary","contentType":"text/plain","data":"!!!"}`)); r.status != 400 {
		t.Fatalf("base64 sai: %d %s", r.status, r.body)
	}
	if e.binaryCount() != n {
		t.Fatal("base64 sai vẫn tạo Binary")
	}
}

func TestRawJSONUpdateKeepsSecurityContext(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "content stays")
	if _, err := e.repo.CreateResource(t.Context(), &fhir.Patient{ID: "123"}); err != nil { // securityContext phải trỏ tới resource đọc được
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"resourceType":"Binary","id":%q,"contentType":"text/plain","securityContext":{"reference":"Patient/123"}}`, id)
	r := e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "application/fhir+json"}, []byte(body))
	if r.status != 200 || r.json()["securityContext"].(map[string]any)["reference"] != "Patient/123" {
		t.Fatalf("PUT JSON = %d %s", r.status, r.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != "content stays" {
		t.Fatalf("nội dung sau cập nhật metadata: %q", g.body)
	}
}

func TestRawNonBinaryJSONIsStoredRaw(t *testing.T) {
	e := newEnv(t, nil)
	doc := `{"resourceType":"Patient"}`
	r := e.post("application/fhir+json", []byte(doc))
	if r.status != 201 || r.json()["resourceType"] != "Binary" {
		t.Fatalf("POST = %d %s", r.status, r.body)
	}
	id := r.json()["id"].(string)
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); string(g.body) != doc {
		t.Fatalf("GET = %q", g.body)
	}
	// JSON hỏng cũng là dữ liệu thô
	r = e.post("application/json", []byte(`{not json`))
	if r.status != 201 {
		t.Fatalf("JSON hỏng: %d", r.status)
	}
	// PUT thân là Binary nhưng id khác đường dẫn: lưu thô (như Medplum)
	id2, _ := e.created("text/plain", "x")
	other := `{"resourceType":"Binary","id":"zzz","contentType":"text/plain"}`
	if r := e.do("PUT", "/fhir/R4/Binary/"+id2, map[string]string{"Content-Type": "application/json"}, []byte(other)); r.status != 200 {
		t.Fatalf("PUT id khác: %d %s", r.status, r.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+id2, nil, nil); string(g.body) != other {
		t.Fatalf("GET = %q", g.body)
	}
}

func TestRawHugeJSONIsRawNotParsed(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.jsonMax = 64 })
	big := `{"resourceType":"Binary","contentType":"text/plain","data":"` + strings.Repeat("QQ==", 50) + `"}`
	r := e.post("application/json", []byte(big))
	if r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+r.json()["id"].(string), nil, nil); string(g.body) != big {
		t.Fatal("JSON lớn phải được lưu nguyên văn như dữ liệu thô")
	}
}

func TestRawSecurityContextHeader(t *testing.T) {
	e := newEnv(t, nil)
	r := e.post("text/plain", []byte("x"), "X-Security-Context", "Patient/abc-1.2")
	if r.status != 201 || r.json()["securityContext"].(map[string]any)["reference"] != "Patient/abc-1.2" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	n := e.binaryCount()
	for _, bad := range []string{"Patient", "Patient/", "../x", "Patient/a b", "Patient/a/b", "http://evil/x", "Patient/" + strings.Repeat("a", 65)} {
		if r := e.post("text/plain", []byte("x"), "X-Security-Context", bad); r.status != 400 {
			t.Errorf("%q: %d", bad, r.status)
		}
	}
	if e.binaryCount() != n {
		t.Fatal("header sai vẫn tạo Binary")
	}
}

func TestRawStoreFailureOnPost(t *testing.T) {
	e := newEnv(t, nil)
	e.store.failPut.Store(true)
	r := e.post("text/plain", []byte("x"))
	if r.status != 500 || strings.Contains(string(r.body), "secret") || strings.Contains(string(r.body), "disk") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if e.binaryCount() != 0 {
		t.Fatal("POST lỗi kho phải xóa resource vừa tạo")
	}
}

func TestRawStoreFailureOnPutKeepsOldContent(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "old content")
	e.store.failPut.Store(true)
	if r := e.do("PUT", "/fhir/R4/Binary/"+id, map[string]string{"Content-Type": "text/plain"}, []byte("new")); r.status != 500 {
		t.Fatalf("PUT = %d", r.status)
	}
	e.store.failPut.Store(false)
	// Wrap đã chép nội dung cũ sang phiên bản mới nên không mất dữ liệu
	if g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); g.status != 200 || string(g.body) != "old content" {
		t.Fatalf("GET = %d %q", g.status, g.body)
	}
}

func TestRawDownloadHeadersByType(t *testing.T) {
	e := newEnv(t, nil)
	for ct, inline := range map[string]bool{"image/png": true, "image/jpeg": true, "Image/PNG; x=y": true, "image/svg+xml": false, "text/html": false, "application/pdf": false, "application/octet-stream": false} {
		id, _ := e.created(ct, "x")
		g := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil)
		if got := g.h.Get("Content-Disposition") == ""; got != inline {
			t.Errorf("%s: inline=%v, header %q", ct, got, g.h.Get("Content-Disposition"))
		}
		if !strings.Contains(g.h.Get("Content-Security-Policy"), "sandbox") || g.h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: thiếu header bảo vệ", ct)
		}
	}
}

func TestRawDelegatesEverythingElse(t *testing.T) {
	e := newEnv(t, nil)
	id, v1 := e.created("text/plain", "x")
	if r := e.do("GET", "/fhir/R4/Binary/"+id+"/_history/"+v1, nil, nil); r.status != 200 || r.json()["resourceType"] != "Binary" {
		t.Fatalf("vread: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/fhir/R4/Binary/"+id+"/_history", nil, nil); r.status != 200 || r.json()["resourceType"] != "Bundle" {
		t.Fatalf("history: %d", r.status)
	}
	if r := e.do("GET", "/fhir/R4/Patient", nil, nil); r.status != 200 || r.json()["resourceType"] != "Bundle" {
		t.Fatalf("search: %d", r.status)
	}
	if r := e.do("DELETE", "/fhir/R4/Binary/"+id, nil, nil); r.status != 204 && r.status != 200 {
		t.Fatalf("delete: %d", r.status)
	}
	if r := e.do("GET", "/fhir/R4/Binary/"+id, nil, nil); r.status != 410 {
		t.Fatalf("đọc Binary đã xóa: %d", r.status)
	}
}

func TestRawWithoutSignerOmitsURL(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.noSigner = true })
	r := e.post("text/plain", []byte("x"))
	if r.status != 201 || r.json()["url"] != nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestRawSignedURLRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	r := e.post("text/plain", []byte("through signed url"))
	u := r.json()["url"].(string)
	if g := e.do("GET", u, nil, nil); g.status != 200 || string(g.body) != "through signed url" {
		t.Fatalf("GET url ký = %d %q", g.status, g.body)
	}
}

var _ = flate.BestSpeed
var _ = json.Marshal

func TestRawPutOnNestedPathIsNotAnUpload(t *testing.T) {
	e := newEnv(t, nil)
	id, _ := e.created("text/plain", "keep")
	n := e.binaryCount()
	for _, p := range []string{"/fhir/R4/Binary/" + id + "/extra", "/fhir/R4/Binary/" + id + "/_history/1"} {
		if r := e.do("PUT", p, map[string]string{"Content-Type": "text/plain"}, []byte("hijack")); r.status < 400 {
			t.Errorf("%s = %d", p, r.status)
		}
	}
	if e.binaryCount() != n || e.store.Len() != 1 {
		t.Fatalf("PUT lồng đường dẫn đã tạo/ghi thứ gì đó (resource %d, kho %d)", e.binaryCount(), e.store.Len())
	}
}

func TestRawGzippedJSONBinaryResource(t *testing.T) {
	e := newEnv(t, nil)
	body := fmt.Sprintf(`{"resourceType":"Binary","contentType":"text/plain","data":%q}`, base64.StdEncoding.EncodeToString([]byte("zipped resource")))
	r := e.do("POST", "/fhir/R4/Binary", map[string]string{"Content-Type": "application/fhir+json", "Content-Encoding": "gzip"}, gz(body))
	if r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+r.json()["id"].(string), nil, nil); string(g.body) != "zipped resource" {
		t.Fatalf("%q", g.body)
	}
}

// Biên của JSONMaxBytes: đúng bằng trần thì còn được nhận là resource, hơn 1 byte thì là dữ liệu thô.
func TestRawJSONMaxBytesBoundary(t *testing.T) {
	prefix := `{"resourceType":"Binary","contentType":"text/plain","note":"`
	suffix := `"}`
	mk := func(n int) string { return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix }
	e := newEnv(t, func(o *envOpts) { o.jsonMax = 100 })
	exact := e.post("application/json", []byte(mk(100)))
	if exact.status != 201 || exact.json()["resourceType"] != "Binary" {
		t.Fatalf("đúng trần: %d %s", exact.status, exact.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+exact.json()["id"].(string), map[string]string{"Accept": "application/json"}, nil); g.json()["note"] != nil {
		// trường lạ bị router loại: điều cần kiểm là nó đi qua router (resource), không phải dữ liệu thô
		t.Logf("resource: %s", g.body)
	}
	over := e.post("application/json", []byte(mk(101)))
	if over.status != 201 {
		t.Fatalf("hơn trần: %d %s", over.status, over.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+over.json()["id"].(string), nil, nil); string(g.body) != mk(101) {
		t.Fatalf("hơn trần phải được lưu thô nguyên văn, có %q", g.body)
	}
	if g := e.do("GET", "/fhir/R4/Binary/"+exact.json()["id"].(string), nil, nil); string(g.body) == mk(100) {
		t.Fatal("đúng trần phải được coi là resource, không phải dữ liệu thô")
	}
}
