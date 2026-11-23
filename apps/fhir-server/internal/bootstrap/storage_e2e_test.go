package bootstrap_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/binarystore"
)

// storageSettings bật kho Binary với thư mục tạm và khóa ký cố định (tệp 0600), như production.
func storageSettings(t *testing.T, mutate ...func(*config.Settings)) (func(*config.Settings), string) { // trả (mutator, đường dẫn khóa)
	t.Helper()
	dir := t.TempDir()
	key, err := binarystore.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pem, _ := binarystore.MarshalPrivateKeyPEM(key)
	keyPath := filepath.Join(dir, "signing.pem")
	if err := os.WriteFile(keyPath, pem, 0o600); err != nil {
		t.Fatal(err)
	}
	return func(s *config.Settings) {
		s.BinaryStorage = "file:" + filepath.Join(dir, "blobs")
		s.StorageSigningKeyPath = keyPath
		for _, m := range mutate {
			m(s)
		}
	}, keyPath
}

// testSigner dựng Signer cùng khóa và gốc URL với server để test tự ký URL (PUT).
func testSigner(t *testing.T, keyPath string) *binarystore.Signer {
	t.Helper()
	b, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	k, err := binarystore.ParsePrivateKeyPEM(b)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := binarystore.NewSigner(binarystore.SignerConfig{BaseURL: "http://fhir.test/storage", Key: k})
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

func rawCall(t *testing.T, srv *httptest.Server, method, path, token string, hdr map[string]string, body []byte) resp {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, raw}
}

func pathOf(u string) string { return strings.TrimPrefix(u, "http://fhir.test") }

func TestStorageEndToEnd(t *testing.T) {
	mut, keyPath := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")

	// tải lên thô: 201, có url ký, project gắn đúng
	up := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "text/plain"}, []byte("Hello world")), 201)
	id := up.str(t, "id")
	if up.str(t, "meta", "project") != "A" || up.str(t, "contentType") != "text/plain" {
		t.Fatalf("resource: %s", up.body)
	}
	signed := pathOf(up.str(t, "url"))
	if !strings.HasPrefix(signed, "/storage/"+id+"/") || !strings.Contains(signed, "Project=A") || !strings.Contains(signed, "Signature=") {
		t.Fatalf("url ký: %q", signed)
	}

	// đọc thô cần xác thực và tách project
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, a, nil), 200); string(g.body) != "Hello world" || g.header.Get("Content-Type") != "text/plain" {
		t.Fatalf("GET thô: %q %v", g.body, g.header)
	}
	want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, "", nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, b, nil), 404)
	if j := want(t, rawCall(t, srv, "GET", "/fhir/R4/Binary/"+id, a, map[string]string{"Accept": "application/fhir+json"}, nil), 200); j.str(t, "resourceType") != "Binary" || j.json(t)["data"] != nil {
		t.Fatalf("GET JSON: %s", j.body)
	}

	// URL ký dùng được không cần token (quyền do chữ ký); sửa một ký tự thì 401
	if g := want(t, call(t, srv, "GET", signed, "", nil), 200); string(g.body) != "Hello world" {
		t.Fatalf("GET url ký: %q", g.body)
	}
	want(t, call(t, srv, "GET", strings.Replace(signed, "Project=A", "Project=B", 1), "", nil), 401)
	want(t, call(t, srv, "GET", strings.Replace(signed, "/storage/"+id, "/storage/other", 1), "", nil), 401)

	// PUT qua URL ký (ký cho PUT) ghi nội dung mới vào đúng phiên bản
	ver := rawCall(t, srv, "GET", "/fhir/R4/Binary/"+id, a, map[string]string{"Accept": "application/fhir+json"}, nil).str(t, "meta", "versionId")
	pu, err := testSigner(t, keyPath).Sign("PUT", id, ver, "A")
	if err != nil {
		t.Fatal(err)
	}
	signedPut := pathOf(pu)
	want(t, rawCall(t, srv, "PUT", signedPut, "", map[string]string{"Content-Type": "text/plain"}, []byte("through url")), 200)
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, a, nil), 200); string(g.body) != "through url" {
		t.Fatalf("sau PUT url: %q", g.body)
	}
	// PUT bằng URL GET: 401
	want(t, rawCall(t, srv, "PUT", signed, "", map[string]string{"Content-Type": "text/plain"}, []byte("x")), 401)

	// JSON Binary có data: nội dung ra kho, đọc thô được, JSON không còn data
	j := want(t, call(t, srv, "POST", "/fhir/R4/Binary", a, map[string]any{"resourceType": "Binary", "contentType": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte("from json"))}), 201)
	jid := j.str(t, "id")
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+jid, a, nil), 200); string(g.body) != "from json" {
		t.Fatalf("GET: %q", g.body)
	}
	if g := want(t, rawCall(t, srv, "GET", "/fhir/R4/Binary/"+jid, a, map[string]string{"Accept": "application/fhir+json"}, nil), 200); g.json(t)["data"] != nil {
		t.Fatalf("data còn trong CSDL: %s", g.body)
	}

	// loại/đuôi bị chặn: 400 và không tạo Binary
	want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "application/x-msdownload"}, []byte("MZ")), 400)
	want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary?_filename=a.exe", a, map[string]string{"Content-Type": "text/plain"}, []byte("MZ")), 400)

	// bảo mật phản hồi của /storage: header chống thực thi, không cache
	g := want(t, call(t, srv, "GET", signed, "", nil), 200)
	if g.header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(g.header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("header: %v", g.header)
	}
	if !strings.Contains(g.header.Get("Cache-Control"), "no-store") {
		t.Errorf("Cache-Control: %q", g.header.Get("Cache-Control"))
	}
}

func TestStorageOffByDefault(t *testing.T) {
	_, srv := newServer(t, settings())
	// không có kho: Binary thô không được hỗ trợ như trước, /storage không tồn tại
	if r := rawCall(t, srv, "POST", "/fhir/R4/Binary", writeSecret, map[string]string{"Content-Type": "text/plain"}, []byte("x")); r.status != 415 {
		t.Fatalf("Binary thô khi tắt kho: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "GET", "/storage/a/b?Expires=1&Signature=x", "", nil); r.status != 404 {
		t.Fatalf("/storage khi tắt kho: %d", r.status)
	}
	// Binary JSON có data vẫn nằm trong CSDL như trước
	p := want(t, call(t, srv, "POST", "/fhir/R4/Binary", writeSecret, map[string]any{"resourceType": "Binary", "contentType": "text/plain", "data": "SGVsbG8="}), 201)
	if got := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+p.str(t, "id"), readSecret, nil), 200); got.str(t, "data") != "SGVsbG8=" {
		t.Fatalf("data phải còn trong resource: %s", got.body)
	}
}

func TestStorageUploadBeyondGlobalBodyLimit(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	a := tok(t, "A", "fhir.write")
	big := bytes.Repeat([]byte("0123456789abcdef"), (20<<20)/16) // 20 MiB, quá BodyLimit 16M của route thường
	up := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "application/octet-stream"}, big), 201)
	g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+up.str(t, "id"), a, nil), 200)
	if sha256.Sum256(g.body) != sha256.Sum256(big) {
		t.Fatalf("nội dung tải về khác (độ dài %d, muốn %d)", len(g.body), len(big))
	}
	// route FHIR thường vẫn bị giới hạn 16M
	huge := `{"resourceType":"Patient","name":[{"family":"` + strings.Repeat("a", 17<<20) + `"}]}`
	if r := rawCall(t, srv, "POST", "/fhir/R4/Patient", a, map[string]string{"Content-Type": "application/fhir+json"}, []byte(huge)); r.status != 413 {
		t.Fatalf("Patient 17M = %d", r.status)
	}
}

func TestStorageUploadLimitIsConfigurable(t *testing.T) {
	mut, _ := storageSettings(t, func(s *config.Settings) { s.StorageMaxUploadBytes = 1 << 20 })
	_, srv := newServer(t, jwtSettings(mut))
	a := tok(t, "A", "fhir.write")
	want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "application/octet-stream"}, make([]byte, 1<<20)), 201)
	want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "application/octet-stream"}, make([]byte, 1<<20+1)), 413)
}

func TestStorageRolesApply(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	rd := tok(t, "A", "fhir.read")
	want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", rd, map[string]string{"Content-Type": "text/plain"}, []byte("x")), 403)
}

func TestStorageSurvivesRestartWithSameKey(t *testing.T) {
	file := "sqlite://" + filepath.Join(t.TempDir(), "fhir.db")
	mut, _ := storageSettings(t)
	s := jwtSettings(mut, func(s *config.Settings) { s.DatabaseURL = file })
	a := tok(t, "A", "fhir.write")

	c1, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := c1.Handler(nil)
	srv1 := httptest.NewServer(e1)
	up := want(t, rawCall(t, srv1, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "text/plain"}, []byte("persisted")), 201)
	id, signed := up.str(t, "id"), pathOf(up.str(t, "url"))
	srv1.Close()
	_ = c1.Stop(context.Background())

	_, srv2 := newServer(t, s) // tiến trình mới, cùng DB, thư mục kho và khóa
	if g := want(t, call(t, srv2, "GET", "/fhir/R4/Binary/"+id, a, nil), 200); string(g.body) != "persisted" {
		t.Fatalf("%q", g.body)
	}
	if g := want(t, call(t, srv2, "GET", signed, "", nil), 200); string(g.body) != "persisted" {
		t.Fatalf("URL ký cũ sau khi khởi động lại: %q", g.body)
	}
}

// Khóa tạm (dev, không đặt đường dẫn khóa): URL ký trước khi khởi động lại mất hiệu lực.
func TestStorageEphemeralKeyInvalidatesURLsOnRestart(t *testing.T) {
	file := "sqlite://" + filepath.Join(t.TempDir(), "fhir.db")
	dir := t.TempDir()
	s := jwtSettings(func(s *config.Settings) { s.DatabaseURL = file; s.BinaryStorage = "file:" + dir })
	a := tok(t, "A", "fhir.write")
	c1, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := c1.Handler(nil)
	srv1 := httptest.NewServer(e1)
	up := want(t, rawCall(t, srv1, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "text/plain"}, []byte("x")), 201)
	signed := pathOf(up.str(t, "url"))
	want(t, call(t, srv1, "GET", signed, "", nil), 200)
	srv1.Close()
	_ = c1.Stop(context.Background())
	_, srv2 := newServer(t, s)
	want(t, call(t, srv2, "GET", signed, "", nil), 401)
}

func TestStorageDoesNotBreakBulkExport(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	a := tok(t, "A", "fhir.write")
	for i := 0; i < 2; i++ {
		want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Export")), 201)
	}
	got, urls := exportAs(t, "", a, "/fhir/R4/$export", doer(t, srv))
	if len(got["Patient"]) != 2 || len(urls) == 0 {
		t.Fatalf("export: %v", got)
	}
}

func TestStorageSubscriptionEventsCarryNoBinaryData(t *testing.T) {
	hook, rec := newReceiver(t)
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut, func(s *config.Settings) { s.Subscriptions, s.WebhookAllowPrivate = true, true }))
	a := tok(t, "A", "fhir.admin")
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", a, map[string]any{"resourceType": "Subscription", "status": "active", "reason": "t", "criteria": "Binary",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL}}), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Binary", a, map[string]any{"resourceType": "Binary", "contentType": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte("secret body"))}), 201)
	for i := 0; i < 300 && rec.count() < 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.count() != 1 {
		t.Fatalf("webhook nhận %d", rec.count())
	}
	if strings.Contains(string(mustJSON(rec.calls[0])), "c2VjcmV0IGJvZHk") {
		t.Fatal("sự kiện chứa nội dung Binary")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func docRefBody(url string) map[string]any {
	return map[string]any{"resourceType": "DocumentReference", "status": "current",
		"content": []any{map[string]any{"attachment": map[string]any{"contentType": "text/plain", "url": url}}}}
}

func presignedURL(t *testing.T, srv *httptest.Server, token, id, query string) resp {
	t.Helper()
	return rawCall(t, srv, "GET", "/fhir/R4/Binary/"+id+"/$presigned-url"+query, token, nil, nil)
}

func TestPresignedURLOperation(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	w, rd, b := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read"), tok(t, "B", "fhir.write")
	id := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", w, map[string]string{"Content-Type": "text/plain"}, []byte("v1")), 201).str(t, "id")

	// đọc: người đọc cũng xin được, URL dùng được không cần token
	r := want(t, presignedURL(t, srv, rd, id, ""), 200)
	u := pathOf(r.str(t, "parameter", "0", "valueUri"))
	if g := want(t, call(t, srv, "GET", u, "", nil), 200); string(g.body) != "v1" {
		t.Fatalf("%q", g.body)
	}
	// tải lên: cần quyền ghi
	want(t, presignedURL(t, srv, rd, id, "?upload=true"), 403)
	up := want(t, presignedURL(t, srv, w, id, "?upload=true"), 200)
	want(t, rawCall(t, srv, "PUT", pathOf(up.str(t, "parameter", "0", "valueUri")), "", map[string]string{"Content-Type": "text/plain"}, []byte("v2")), 200)
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, w, nil), 200); string(g.body) != "v2" {
		t.Fatalf("%q", g.body)
	}
	// project khác và không token
	want(t, presignedURL(t, srv, b, id, ""), 404)
	want(t, presignedURL(t, srv, "", id, ""), 401)
	// quyền SMART theo loại: có ghi nhưng không có quyền cập nhật Binary
	noU := tok(t, "A", "system/Binary.rcds")
	want(t, presignedURL(t, srv, noU, id, ""), 200)
	want(t, presignedURL(t, srv, noU, id, "?upload=true"), 403)
	// không đọc được Binary theo SMART
	noBin := tok(t, "A", "system/DocumentReference.cruds")
	want(t, presignedURL(t, srv, noBin, id, ""), 403)
}

func TestPresignedURLWithStaticTokens(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, settings(mut))
	id := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", writeSecret, map[string]string{"Content-Type": "text/plain"}, []byte("x")), 201).str(t, "id")
	want(t, presignedURL(t, srv, readSecret, id, ""), 200)
	want(t, presignedURL(t, srv, readSecret, id, "?upload=true"), 403) // token chỉ đọc không được xin URL ghi
	want(t, presignedURL(t, srv, writeSecret, id, "?upload=true"), 200)
}

func TestAttachmentRewriteAcrossProjectsAndGrants(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")
	id := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "text/plain"}, []byte("A's file")), 201).str(t, "id")

	// A: tham chiếu ra URL ký dùng được; CSDL giữ Binary/<id>
	dr := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRefBody("Binary/"+id)), 201)
	u := dr.str(t, "content", "0", "attachment", "url")
	if !strings.HasPrefix(u, "http://fhir.test/storage/"+id+"/") {
		t.Fatalf("url: %s", u)
	}
	if g := want(t, call(t, srv, "GET", pathOf(u), "", nil), 200); string(g.body) != "A's file" {
		t.Fatalf("%q", g.body)
	}
	// B tham chiếu Binary của A: không đọc được nên giữ nguyên chuỗi, không lộ URL ký
	drB := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", b, docRefBody("Binary/"+id)), 201)
	if got := drB.str(t, "content", "0", "attachment", "url"); got != "Binary/"+id {
		t.Fatalf("B nhận %q (không được có URL ký của project khác)", got)
	}
	// người có quyền SMART chỉ trên DocumentReference: không đọc được Binary nên URL giữ nguyên
	limited := tok(t, "A", "system/DocumentReference.cruds")
	got := want(t, call(t, srv, "GET", "/fhir/R4/DocumentReference/"+dr.str(t, "id"), limited, nil), 200)
	if v := got.str(t, "content", "0", "attachment", "url"); v != "Binary/"+id {
		t.Fatalf("thiếu quyền Binary mà nhận %q", v)
	}
	// khứ hồi: gửi lại URL ký → lưu lại Binary/<id>
	want(t, call(t, srv, "PUT", "/fhir/R4/DocumentReference/"+dr.str(t, "id"), a, withID(docRefBody(u), dr.str(t, "id"))), 200)
	again := want(t, call(t, srv, "GET", "/fhir/R4/DocumentReference/"+dr.str(t, "id"), limited, nil), 200)
	if v := again.str(t, "content", "0", "attachment", "url"); v != "Binary/"+id {
		t.Fatalf("sau khứ hồi CSDL phải giữ Binary/<id>, đọc bằng người thiếu quyền ra %q", v)
	}
}

func withID(m map[string]any, id string) map[string]any { m["id"] = id; return m }

func TestSecurityContextProtectsRawRead(t *testing.T) {
	mut, _ := storageSettings(t)
	_, srv := newServer(t, jwtSettings(mut))
	a := tok(t, "A", "fhir.write")
	pid := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Secret")), 201).str(t, "id")
	id := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "text/plain", "X-Security-Context": "Patient/" + pid}, []byte("patient file")), 201).str(t, "id")
	want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, a, nil), 200)
	// chỉ có quyền Binary (không đọc được Patient): bị chặn ở đọc thô và ở $presigned-url
	binOnly := tok(t, "A", "system/Binary.cruds")
	want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, binOnly, nil), 403)
	want(t, presignedURL(t, srv, binOnly, id, ""), 403)
	// có quyền đọc Patient thì qua
	both := tok(t, "A", "system/Binary.cruds system/Patient.r")
	want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, both, nil), 200)
	want(t, presignedURL(t, srv, both, id, ""), 200)
}
