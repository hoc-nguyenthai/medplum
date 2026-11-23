package fhirbinary_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func (e *env) signed(method, id, ver, project string) string {
	e.t.Helper()
	u, err := e.signer.Sign(method, id, ver, project)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// seed ghi Binary (qua Wrap) với project trong meta; trả id, versionId.
func (e *env) seed(id, ct, content, project string) string {
	e.t.Helper()
	b := &fhir.Binary{ID: id, ContentType: ct, Data: b64(content)}
	if project != "" {
		b.Meta = &fhir.Meta{Project: project}
	}
	out, err := e.wrapped.CreateResource(context.Background(), b)
	if err != nil {
		e.t.Fatal(err)
	}
	if got := out.GetMeta().Project; got != project {
		e.t.Fatalf("project của meta = %q, muốn %q (repo thử không giữ meta.project)", got, project)
	}
	return out.GetMeta().VersionId
}

func TestStorageGetAndPutRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	if g := e.do("GET", e.signed("GET", "b1", v, ""), nil, nil); g.status != 200 || string(g.body) != "foo" || g.h.Get("Content-Type") != "text/plain" {
		t.Fatalf("GET = %d %q %v", g.status, g.body, g.h)
	}
	p := e.do("PUT", e.signed("PUT", "b1", v, ""), map[string]string{"Content-Type": "text/plain"}, []byte("foo bar baz quux"))
	if p.status != 200 || len(p.body) != 0 {
		t.Fatalf("PUT = %d %q", p.status, p.body)
	}
	if g := e.do("GET", e.signed("GET", "b1", v, ""), nil, nil); string(g.body) != "foo bar baz quux" {
		t.Fatalf("GET sau PUT = %q", g.body)
	}
}

func TestStorageSignatureErrors(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	base := e.srv.URL + "/storage/b1/" + v
	exp := fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix())
	for _, c := range []struct {
		name, url string
		want      int
	}{
		{"thiếu Signature", base + "?Expires=" + exp, 401},
		{"thiếu Expires", base + "?Signature=xyz", 410},
		{"chữ ký giả", base + "?Expires=" + exp + "&Signature=xyz", 401},
		{"không query", base, 401},
	} {
		if g := e.do("GET", c.url, nil, nil); g.status != c.want {
			t.Errorf("%s: GET %d, muốn %d", c.name, g.status, c.want)
		}
		if g := e.do("PUT", c.url, map[string]string{"Content-Type": "text/plain"}, []byte("x")); g.status != c.want {
			t.Errorf("%s: PUT %d, muốn %d", c.name, g.status, c.want)
		}
	}
	if got, _ := e.stored("b1", v); got != "foo" {
		t.Fatalf("PUT không hợp lệ đã ghi: %q", got)
	}
}

func TestStorageExpiry(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	u := e.signed("GET", "b1", v, "")
	e.clk.Add(time.Hour)
	if g := e.do("GET", u, nil, nil); g.status != 200 {
		t.Fatalf("đúng hạn: %d", g.status)
	}
	e.clk.Add(time.Second)
	if g := e.do("GET", u, nil, nil); g.status != 410 || string(g.body) != "URL has expired" {
		t.Fatalf("quá hạn: %d %q", g.status, g.body)
	}
}

func TestStorageNotFound(t *testing.T) {
	e := newEnv(t, nil)
	if g := e.do("GET", e.signed("GET", "ghost", "v1", ""), nil, nil); g.status != 404 || g.json()["resourceType"] != "OperationOutcome" {
		t.Fatalf("Binary lạ: %d %s", g.status, g.body)
	}
	if _, err := e.wrapped.CreateResource(context.Background(), binaryNoContent("nc")); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.repo.ReadResource(context.Background(), "Binary", "nc")
	if g := e.do("GET", e.signed("GET", "nc", cur.GetMeta().VersionId, ""), nil, nil); g.status != 404 {
		t.Fatalf("chưa có nội dung: %d", g.status)
	}
	// Binary đã xóa: như không có
	v := e.seed("gone", "text/plain", "x", "")
	_ = e.repo.DeleteResource(context.Background(), "Binary", "gone")
	for _, m := range []string{"GET", "PUT"} {
		if g := e.do(m, e.signed(m, "gone", v, ""), map[string]string{"Content-Type": "text/plain"}, []byte("x")); g.status != 404 {
			t.Errorf("%s Binary đã xóa: %d", m, g.status)
		}
	}
}

func TestStoragePutContentType(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	u := e.signed("PUT", "b1", v, "")
	if p := e.do("PUT", u, map[string]string{"Content-Type": "application/json"}, []byte("x")); p.status != 400 || string(p.body) != "Content-Type mismatch" {
		t.Fatalf("khác loại: %d %q", p.status, p.body)
	}
	for _, ct := range []string{"text/plain; charset=utf-8", "TEXT/PLAIN", ""} {
		hdr := map[string]string{}
		if ct != "" {
			hdr["Content-Type"] = ct
		}
		req, _ := http.NewRequest("PUT", u, strings.NewReader("ok"))
		if ct == "" {
			req.Header["Content-Type"] = nil
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Errorf("%q: %v %v", ct, err, res)
		}
	}
	// Binary có loại bị chặn (dữ liệu cũ): PUT bị từ chối
	if _, err := e.repo.CreateResource(context.Background(), binaryWithData("evil", "application/x-sh", b64("x"))); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.repo.ReadResource(context.Background(), "Binary", "evil")
	if p := e.do("PUT", e.signed("PUT", "evil", cur.GetMeta().VersionId, ""), map[string]string{"Content-Type": "application/x-sh"}, []byte("rm -rf")); p.status != 400 {
		t.Fatalf("loại chặn: %d", p.status)
	}
}

func TestStoragePutSizeLimitKeepsOldContent(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.maxUpload = 10 })
	v := e.seed("b1", "text/plain", "old", "")
	u := e.signed("PUT", "b1", v, "")
	if p := e.do("PUT", u, map[string]string{"Content-Type": "text/plain"}, bytes.Repeat([]byte("a"), 11)); p.status != 413 {
		t.Fatalf("vượt trần: %d", p.status)
	}
	// chunked, không có Content-Length
	req, _ := http.NewRequest("PUT", u, io_NopReader(strings.NewReader(strings.Repeat("a", 50))))
	req.Header.Set("Content-Type", "text/plain")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 413 {
		t.Fatalf("chunked vượt trần: %v %v", err, res)
	}
	if got, _ := e.stored("b1", v); got != "old" {
		t.Fatalf("nội dung cũ bị đổi: %q", got)
	}
	if p := e.do("PUT", u, map[string]string{"Content-Type": "text/plain"}, bytes.Repeat([]byte("a"), 10)); p.status != 200 {
		t.Fatalf("đúng trần: %d", p.status)
	}
}

func TestStorageStoreErrors(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "old", "")
	e.store.failPut.Store(true)
	p := e.do("PUT", e.signed("PUT", "b1", v, ""), map[string]string{"Content-Type": "text/plain"}, []byte("x"))
	if p.status != 500 || strings.Contains(string(p.body), "secret") {
		t.Fatalf("PUT lỗi kho: %d %q (phải 500, không lộ đường dẫn)", p.status, p.body)
	}
	e.store.failOpen.Store(true)
	g := e.do("GET", e.signed("GET", "b1", v, ""), nil, nil)
	if g.status != 404 || strings.Contains(string(g.body), "secret") {
		t.Fatalf("GET lỗi kho: %d %s", g.status, g.body)
	}
}

func TestStorageVersions(t *testing.T) {
	e := newEnv(t, nil)
	v1 := e.seed("b1", "text/plain", "version one", "")
	v2o, err := e.wrapped.UpdateResource(context.Background(), &fhir.Binary{ID: "b1", ContentType: "text/html", Data: b64("<b>two</b>")}, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v2 := v2o.GetMeta().VersionId
	// URL phiên bản cũ phục vụ đúng bản cũ (nội dung và loại)
	if g := e.do("GET", e.signed("GET", "b1", v1, ""), nil, nil); string(g.body) != "version one" || g.h.Get("Content-Type") != "text/plain" {
		t.Fatalf("GET v1 = %q %v", g.body, g.h)
	}
	if g := e.do("GET", e.signed("GET", "b1", v2, ""), nil, nil); string(g.body) != "<b>two</b>" || g.h.Get("Content-Type") != "text/html" {
		t.Fatalf("GET v2 = %q %v", g.body, g.h)
	}
	// PUT lên URL phiên bản cũ: 409, không ghi đè bản mới
	if p := e.do("PUT", e.signed("PUT", "b1", v1, ""), map[string]string{"Content-Type": "text/plain"}, []byte("stale")); p.status != 409 {
		t.Fatalf("PUT URL cũ = %d", p.status)
	}
	if got, _ := e.stored("b1", v2); got != "<b>two</b>" {
		t.Fatalf("bản mới bị ghi đè: %q", got)
	}
	if got, _ := e.stored("b1", v1); got != "version one" {
		t.Fatalf("bản cũ bị ghi đè: %q", got)
	}
	// phiên bản không có thật
	if g := e.do("GET", e.signed("GET", "b1", "nonexistent", ""), nil, nil); g.status != 404 {
		t.Fatalf("phiên bản lạ: %d", g.status)
	}
	// PUT lên URL bản hiện tại thì được
	if p := e.do("PUT", e.signed("PUT", "b1", v2, ""), map[string]string{"Content-Type": "text/html"}, []byte("new")); p.status != 200 {
		t.Fatalf("PUT bản hiện tại: %d", p.status)
	}
}

func TestStorageProjectIsolation(t *testing.T) {
	e := newEnv(t, nil)
	vp := e.seed("inproj", "text/plain", "project data", "proj-a")
	vn := e.seed("noproj", "text/plain", "system data", "")
	if g := e.do("GET", e.signed("GET", "inproj", vp, "proj-a"), nil, nil); g.status != 200 || string(g.body) != "project data" {
		t.Fatalf("đúng project: %d", g.status)
	}
	if p := e.do("PUT", e.signed("PUT", "inproj", vp, "proj-a"), map[string]string{"Content-Type": "text/plain"}, []byte("x")); p.status != 200 {
		t.Fatalf("PUT đúng project: %d", p.status)
	}
	for name, u := range map[string]string{
		"project khác (ký hợp lệ)":       e.signed("GET", "inproj", vp, "proj-b"),
		"không Project, Binary có":       e.signed("GET", "inproj", vp, ""),
		"có Project, Binary không có":    e.signed("GET", "noproj", vn, "proj-a"),
		"đổi Project sau khi ký":         strings.Replace(e.signed("GET", "inproj", vp, "proj-a"), "Project=proj-a", "Project=proj-b", 1),
		"bỏ Project sau khi ký":          strings.Replace(e.signed("GET", "inproj", vp, "proj-a"), "&Project=proj-a", "", 1),
		"thêm Project sau khi ký":        strings.Replace(e.signed("GET", "noproj", vn, ""), "&Signature", "&Project=proj-a&Signature", 1),
		"dùng chữ ký Binary này cho kia": strings.Replace(e.signed("GET", "inproj", vp, "proj-a"), "/inproj/", "/noproj/", 1),
	} {
		if g := e.do("GET", u, nil, nil); g.status != 401 {
			t.Errorf("%s: GET %d, muốn 401 (body %q)", name, g.status, g.body)
		}
	}
	if p := e.do("PUT", e.signed("PUT", "inproj", vp, "proj-b"), map[string]string{"Content-Type": "text/plain"}, []byte("hijack")); p.status != 401 {
		t.Fatalf("PUT project khác: %d", p.status)
	}
	if got, _ := e.stored("inproj", vp); got != "x" {
		t.Fatalf("PUT trái phép đã ghi: %q", got)
	}
	if g := e.do("GET", e.signed("GET", "noproj", vn, ""), nil, nil); g.status != 200 {
		t.Fatalf("Binary hệ thống không project: %d", g.status)
	}
}

func TestStorageMethodNotAllowed(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	for _, m := range []string{"POST", "DELETE", "PATCH"} {
		if r := e.do(m, e.signed("GET", "b1", v, ""), nil, nil); r.status != 405 || r.h.Get("Allow") != "GET, PUT" {
			t.Errorf("%s = %d %v", m, r.status, r.h)
		}
	}
}

func TestStorageSignatureIsMethodBound(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	if p := e.do("PUT", e.signed("GET", "b1", v, ""), map[string]string{"Content-Type": "text/plain"}, []byte("x")); p.status != 401 {
		t.Fatalf("PUT bằng URL GET = %d", p.status)
	}
	if g := e.do("GET", e.signed("PUT", "b1", v, ""), nil, nil); g.status != 401 {
		t.Fatalf("GET bằng URL PUT = %d", g.status)
	}
}

// Gửi đường dẫn thô (client Go chuẩn hóa nên dùng net.Dial): mọi biến thể thoát đường dẫn đều 401.
func TestStorageRawPathTraversalIs401(t *testing.T) {
	e := newEnv(t, nil)
	v := e.seed("b1", "text/plain", "foo", "")
	good := e.signed("GET", "b1", v, "")
	q := good[strings.Index(good, "?"):]
	u, _ := url.Parse(good)
	do := func(method, path string) int {
		c, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintf(c, "%s %s HTTP/1.1\r\nHost: %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", method, path, u.Host)
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, path := range []string{
		"/storage/b1/" + v + "/..\\", "/storage/b1/..\\", "/storage/b1/%2e%2e\\", "/storage/b1/" + v + "/../" + v,
		"/api/storage/b1/" + v + "/..\\..\\..\\storage\\", "/storage/b1/%2e%2e/b1/" + v, "/storage/./b1/" + v, "/storage//b1/" + v,
	} {
		for _, m := range []string{"GET", "PUT"} {
			// 401 từ handler; ServeMux của test có thể chặn sớm hơn (307 làm sạch đường dẫn, 404 không gắn): miễn không bao giờ được phục vụ
			if got := do(m, path+q); got != 401 && got != 307 && got != 404 {
				t.Errorf("%s %s = %d", m, path, got)
			}
		}
	}
	if got := do("GET", "/storage/b1/"+v+q); got != 200 {
		t.Fatalf("đường dẫn đúng = %d", got)
	}
}

func TestStorageReadErrorIs500NotLeak(t *testing.T) {
	e := newEnv(t, nil)
	_ = e
	// repo hệ thống lỗi bất ngờ (không phải 404): 500 không lộ chi tiết
	e2 := newEnvWithSystemRepo(t, errRepo{errors.New("db password=hunter2")})
	g := e2.do("GET", e2.signed("GET", "b1", "v1", ""), nil, nil)
	if g.status != 500 || strings.Contains(string(g.body), "hunter2") {
		t.Fatalf("%d %q", g.status, g.body)
	}
}

func io_NopReader(r *strings.Reader) *chunkedReader { return &chunkedReader{r} }

// chunkedReader che kiểu để net/http không đoán được Content-Length (gửi chunked).
type chunkedReader struct{ r *strings.Reader }

func (c *chunkedReader) Read(p []byte) (int, error) { return c.r.Read(p) }
