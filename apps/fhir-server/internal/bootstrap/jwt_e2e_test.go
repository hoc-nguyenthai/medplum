package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/fhir-server/internal/config"
)

const (
	jwtSecret = "jwt-shared-secret-0123456789abcdef-xyz"
	jwtIssuer = "https://id.test"
)

func jwtSettings(mutate ...func(*config.Settings)) config.Settings {
	return settings(append([]func(*config.Settings){func(s *config.Settings) {
		s.WriteTokens, s.ReadTokens = "", ""
		s.JWTIssuer, s.JWTAudience, s.JWTHMACSecret = jwtIssuer, "fhir", jwtSecret
	}}, mutate...)...)
}

func tok(t *testing.T, project, scope string, over ...func(jwt.MapClaims)) string {
	t.Helper()
	c := jwt.MapClaims{"iss": jwtIssuer, "aud": "fhir", "sub": "user@" + project, "exp": time.Now().Add(time.Hour).Unix(), "scope": scope}
	if project != "" {
		c["fhir_project"] = project
	}
	for _, o := range over {
		o(c)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(jwtSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestJWTAuthenticationAndRoles(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	rd, wr, ad := tok(t, "A", "fhir.read"), tok(t, "A", "openid fhir.write"), tok(t, "A", "fhir.admin")

	want(t, call(t, srv, "GET", "/fhir/R4/metadata", "", nil), 200) // công khai
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", "", nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", "garbage", nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "A", "fhir.read", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }), nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "A", "fhir.read", func(c jwt.MapClaims) { c["aud"] = "other" }), nil), 401)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "", "fhir.read"), nil), 403)     // thiếu project
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "A", "openid email"), nil), 403) // thiếu scope fhir
	r := call(t, srv, "GET", "/fhir/R4/Patient", "garbage", nil)
	if !strings.Contains(r.header.Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("WWW-Authenticate: %q", r.header.Get("WWW-Authenticate"))
	}

	want(t, call(t, srv, "GET", "/fhir/R4/Patient", rd, nil), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", rd, patient("X")), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", wr, patient("X")), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient/$reindex", wr, ""), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient/$reindex", ad, ""), 202)
}

func TestJWTProjectIsolationOverHTTP(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")

	// project do client gửi trong meta bị bỏ qua
	pa := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, map[string]any{
		"resourceType": "Patient", "name": []any{map[string]any{"family": "Alpha"}}, "meta": map[string]any{"project": "B"}}), 201)
	if pa.str(t, "meta", "project") != "A" {
		t.Fatalf("meta.project = %q", pa.str(t, "meta", "project"))
	}
	idA := pa.str(t, "id")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", b, patient("Beta")), 201)

	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+idA, b, nil), 404)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+idA, b, map[string]any{"resourceType": "Patient", "id": idA}), 404)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+idA, b, nil), 404)
	if n := want(t, call(t, srv, "GET", "/fhir/R4/Patient", b, nil), 200).json(t)["entry"].([]any); len(n) != 1 {
		t.Errorf("B thấy %d Patient, muốn 1", len(n))
	}
	// batch của B cố đọc/xóa dữ liệu của A
	bat := want(t, call(t, srv, "POST", "/fhir/R4", b, map[string]any{"resourceType": "Bundle", "type": "batch", "entry": []any{
		map[string]any{"request": map[string]any{"method": "GET", "url": "Patient/" + idA}},
		map[string]any{"request": map[string]any{"method": "DELETE", "url": "Patient/" + idA}},
	}}), 200)
	for _, e := range bat.json(t)["entry"].([]any) {
		if st := e.(map[string]any)["response"].(map[string]any)["status"].(string); !strings.HasPrefix(st, "404") {
			t.Errorf("batch chéo project: %s", st)
		}
	}
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+idA, a, nil), 200) // A vẫn nguyên
}

func TestJWTJobsAndSubscriptionsAcrossProjects(t *testing.T) {
	hookA, recA := newReceiver(t)
	hookB, recB := newReceiver(t)
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.Subscriptions, s.WebhookAllowPrivate = true, true }))
	a, b := tok(t, "A", "fhir.admin"), tok(t, "B", "fhir.admin")
	for _, x := range []struct{ tok, url string }{{a, hookA.URL}, {b, hookB.URL}} {
		want(t, call(t, srv, "POST", "/fhir/R4/Subscription", x.tok, map[string]any{
			"resourceType": "Subscription", "status": "active", "reason": "t", "criteria": "Patient",
			"channel": map[string]any{"type": "rest-hook", "endpoint": x.url}}), 201)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("OnlyA")), 201)
	for i := 0; i < 300 && recA.count() < 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	if recA.count() != 1 || recB.count() != 0 {
		t.Fatalf("A nhận %d, B nhận %d; muốn 1 và 0 (Subscription không được rò giữa project)", recA.count(), recB.count())
	}

	// job nền chạy được dù không có người gọi; trạng thái chỉ chủ sở hữu thấy
	acc := want(t, call(t, srv, "POST", "/fhir/R4/Patient/$reindex", a, ""), 202)
	path := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	var st resp
	for i := 0; i < 300; i++ {
		if st = call(t, srv, "GET", path, a, nil); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.status != 200 || st.str(t, "status") != "completed" {
		t.Fatalf("job: %d %s", st.status, st.body)
	}
	want(t, call(t, srv, "GET", path, b, nil), 404)
}

// exportAs chạy bulk export bằng token và trả các dòng ndjson theo loại resource cùng URL các file.
func exportAs(t *testing.T, base, token, path string, do func(method, path, token string, headers ...string) resp) (map[string][]map[string]any, []string) {
	t.Helper()
	acc := do("GET", path, token, "Prefer", "respond-async")
	if acc.status != 202 {
		t.Fatalf("export: %d %s", acc.status, acc.body)
	}
	status := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	var st resp
	for i := 0; i < 300; i++ {
		if st = do("GET", status, token); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.status != 200 {
		t.Fatalf("trạng thái export: %d %s", st.status, st.body)
	}
	out := map[string][]map[string]any{}
	var urls []string
	for _, f := range st.json(t)["output"].([]any) {
		fm := f.(map[string]any)
		u := strings.TrimPrefix(fm["url"].(string), "http://fhir.test")
		urls = append(urls, u)
		r := do("GET", u, token, "Accept", "application/fhir+ndjson")
		if r.status != 200 {
			t.Fatalf("tải file: %d %s", r.status, r.body)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(r.body)), "\n") {
			var doc map[string]any
			if err := json.Unmarshal([]byte(line), &doc); err != nil {
				t.Fatalf("ndjson: %q", line)
			}
			out[fm["type"].(string)] = append(out[fm["type"].(string)], doc)
		}
	}
	return out, urls
}

func TestBulkExportIsScopedToCallerProject(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")
	for i := 0; i < 3; i++ {
		want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("A")), 201)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", b, patient("B")), 201)

	do := doer(t, srv)
	gotA, urlsA := exportAs(t, "", a, "/fhir/R4/$export", do)
	gotB, _ := exportAs(t, "", b, "/fhir/R4/$export", do)
	if len(gotA["Patient"]) != 3 || len(gotB["Patient"]) != 1 {
		t.Fatalf("A xuất %d Patient, B xuất %d; muốn 3 và 1", len(gotA["Patient"]), len(gotB["Patient"]))
	}
	for _, p := range gotB["Patient"] {
		if p["meta"].(map[string]any)["project"] != "B" {
			t.Errorf("B xuất dữ liệu của project khác: %v", p)
		}
	}
	// file của A không tải được bằng token của B, và không tải được khi không có token
	for _, u := range urlsA {
		if r := do("GET", u, b, "Accept", "application/fhir+ndjson"); r.status != 404 {
			t.Errorf("B tải file của A: %d", r.status)
		}
		if r := call(t, srv, "GET", u, "", nil); r.status != 401 {
			t.Errorf("tải file không token: %d", r.status)
		}
	}
	if r := do("GET", "/fhir/R4/$export", b); r.status != 400 {
		t.Errorf("thiếu Prefer: %d", r.status)
	}
}

func TestTerminologyNeedsOnlyReadRole(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	rd, wr := tok(t, "A", "fhir.read"), tok(t, "A", "fhir.write")
	want(t, call(t, srv, "POST", "/fhir/R4/CodeSystem", wr, map[string]any{"resourceType": "CodeSystem", "url": "http://x/cs", "status": "active", "content": "complete",
		"concept": []any{map[string]any{"code": "a", "display": "A"}}}), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/ValueSet", wr, map[string]any{"resourceType": "ValueSet", "url": "http://x/vs", "status": "active",
		"compose": map[string]any{"include": []any{map[string]any{"system": "http://x/cs"}}}}), 201)
	exp := want(t, call(t, srv, "POST", "/fhir/R4/ValueSet/$expand", rd, map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "url", "valueUri": "http://x/vs"}}}), 200)
	if !strings.Contains(string(exp.body), `"code":"a"`) {
		t.Errorf("expand: %s", exp.body)
	}
	want(t, call(t, srv, "GET", "/fhir/R4/CodeSystem/$lookup?system=http://x/cs&code=a", rd, nil), 200)
	// project khác không thấy CodeSystem của A
	other := tok(t, "B", "fhir.read")
	want(t, call(t, srv, "GET", "/fhir/R4/ValueSet/$expand?url=http://x/vs", other, nil), 404)
}

func TestJWTSMARTScopesPerResourceType(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	full := tok(t, "A", "fhir.write")
	pa := want(t, call(t, srv, "POST", "/fhir/R4/Patient", full, patient("Alpha")), 201)
	idA := pa.str(t, "id")

	obsOnly := tok(t, "A", "system/Observation.read")
	want(t, call(t, srv, "GET", "/fhir/R4/Observation", obsOnly, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+idA, obsOnly, nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", obsOnly, nil), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Observation", obsOnly, map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "x"}}), 403)

	// ghi Patient nhưng không đọc được Observation
	patWrite := tok(t, "A", "user/Patient.cruds")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", patWrite, patient("Beta")), 201)
	want(t, call(t, srv, "GET", "/fhir/R4/Observation", patWrite, nil), 403)

	// batch: từng entry bị kiểm theo loại
	bat := want(t, call(t, srv, "POST", "/fhir/R4", patWrite, map[string]any{"resourceType": "Bundle", "type": "batch", "entry": []any{
		map[string]any{"request": map[string]any{"method": "GET", "url": "Patient/" + idA}},
		map[string]any{"request": map[string]any{"method": "GET", "url": "Observation"}},
	}}), 200)
	es := bat.json(t)["entry"].([]any)
	if st := es[0].(map[string]any)["response"].(map[string]any)["status"].(string); !strings.HasPrefix(st, "200") {
		t.Errorf("entry Patient: %s", st)
	}
	if st := es[1].(map[string]any)["response"].(map[string]any)["status"].(string); !strings.HasPrefix(st, "403") {
		t.Errorf("entry Observation: %s", st)
	}
	// scope đọc mọi loại + ghi một loại vẫn đọc được tất cả
	mixed := tok(t, "A", "system/*.read system/Patient.write")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+idA, mixed, nil), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", mixed, patient("Gamma")), 201)
}

func TestJWTAccessPolicyClaim(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	full := tok(t, "A", "fhir.write")
	p1 := want(t, call(t, srv, "POST", "/fhir/R4/Patient", full, patient("One")), 201).str(t, "id")
	p2 := want(t, call(t, srv, "POST", "/fhir/R4/Patient", full, patient("Two")), 201).str(t, "id")
	obs := func(p string) map[string]any {
		return map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "x"},
			"subject": map[string]any{"reference": "Patient/" + p}}
	}
	o2 := want(t, call(t, srv, "POST", "/fhir/R4/Observation", full, obs(p2)), 201).str(t, "id")
	want(t, call(t, srv, "POST", "/fhir/R4/Observation", full, obs(p1)), 201)

	withPolicy := func(pol any) string {
		return tok(t, "A", "fhir.write", func(c jwt.MapClaims) { c["fhir_access_policy"] = pol })
	}
	pt := withPolicy([]any{
		map[string]any{"resourceType": "Patient", "criteria": "_id=" + p1, "readonly": true},
		"Observation?subject=Patient/" + p1,
	})
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+p1, pt, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+p2, pt, nil), 404)
	want(t, call(t, srv, "GET", "/fhir/R4/Observation/"+o2, pt, nil), 404)
	want(t, call(t, srv, "GET", "/fhir/R4/Condition", pt, nil), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+p1, pt, map[string]any{"resourceType": "Patient", "id": p1}), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Observation", pt, obs(p2)), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Observation", pt, obs(p1)), 201)
	if n := want(t, call(t, srv, "GET", "/fhir/R4/Observation", pt, nil), 200).json(t)["entry"].([]any); len(n) != 2 {
		t.Errorf("thấy %d Observation, muốn 2", len(n))
	}
	// chained qua loại ngoài chính sách không lộ dữ liệu
	want(t, call(t, srv, "GET", "/fhir/R4/Observation?subject:Patient._id="+p2, pt, nil), 200)
	// claim sai: token bị từ chối
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", withPolicy("Patient"), nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", withPolicy([]any{}), nil), 403) // chính sách rỗng
}

func doer(t *testing.T, srv *httptest.Server) func(method, path, token string, headers ...string) resp {
	return func(method, path, token string, headers ...string) resp {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return resp{res.StatusCode, res.Header, raw}
	}
}

func TestExportHonoursGrantsAndPolicy(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	full := tok(t, "A", "fhir.write")
	p1 := want(t, call(t, srv, "POST", "/fhir/R4/Patient", full, patient("One")), 201).str(t, "id")
	p2 := want(t, call(t, srv, "POST", "/fhir/R4/Patient", full, patient("Two")), 201).str(t, "id")
	for _, p := range []string{p1, p2} {
		want(t, call(t, srv, "POST", "/fhir/R4/Observation", full, map[string]any{"resourceType": "Observation", "status": "final",
			"code": map[string]any{"text": "x"}, "subject": map[string]any{"reference": "Patient/" + p}}), 201)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Condition", full, map[string]any{"resourceType": "Condition", "subject": map[string]any{"reference": "Patient/" + p1}}), 201)
	do := doer(t, srv)

	// scope theo loại: chỉ xuất Patient (Binary.read để tải file)
	grants := tok(t, "A", "system/Patient.read system/Binary.read")
	got, _ := exportAs(t, "", grants, "/fhir/R4/$export", do)
	if len(got["Patient"]) != 2 || len(got["Observation"]) != 0 || len(got["Condition"]) != 0 {
		t.Fatalf("xuất theo scope: %v", counts(got))
	}
	// chỉ định _type ngoài quyền: job lỗi (không bỏ qua lặng lẽ)
	acc := do("GET", "/fhir/R4/$export?_type=Condition", grants, "Prefer", "respond-async")
	if acc.status != 202 {
		t.Fatalf("%d %s", acc.status, acc.body)
	}
	status := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	var st resp
	for i := 0; i < 300; i++ {
		if st = do("GET", status, grants); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.status == 200 {
		t.Errorf("_type=Condition ngoài quyền phải lỗi: %s", st.body)
	}

	// chính sách: chỉ dữ liệu của p1
	pol := tok(t, "A", "fhir.write", func(c jwt.MapClaims) {
		c["fhir_access_policy"] = []any{map[string]any{"resourceType": "Patient", "criteria": "_id=" + p1}, "Observation?subject=Patient/" + p1, "Binary"}
	})
	got, _ = exportAs(t, "", pol, "/fhir/R4/$export", do)
	if len(got["Patient"]) != 1 || len(got["Observation"]) != 1 || len(got["Condition"]) != 0 || got["Patient"][0]["id"] != p1 {
		t.Fatalf("xuất theo chính sách: %v", counts(got))
	}
}

func counts(m map[string][]map[string]any) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[k] = len(v)
	}
	return out
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestSuperAdminActsInChosenProjectWithAudit(t *testing.T) {
	logs := &syncBuf{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	_, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.JWTSuperAdmin = true }))
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")
	idA := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Alpha")), 201).str(t, "id")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", b, patient("Beta")), 201)

	super := tok(t, "", "fhir.super") // không cần claim project
	do := doer(t, srv)
	if r := do("GET", "/fhir/R4/Patient/"+idA, super); r.status != 400 {
		t.Fatalf("thiếu header phải 400: %d", r.status)
	}
	if r := do("GET", "/fhir/R4/Patient/"+idA, super, "X-Fhir-Project", "bad value!"); r.status != 400 {
		t.Fatalf("header sai định dạng phải 400: %d", r.status)
	}
	if r := do("GET", "/fhir/R4/Patient/"+idA, super, "X-Fhir-Project", "A"); r.status != 200 {
		t.Fatalf("đọc project A: %d %s", r.status, r.body)
	}
	if r := do("GET", "/fhir/R4/Patient/"+idA, super, "X-Fhir-Project", "B"); r.status != 404 {
		t.Fatalf("project B không có bệnh nhân của A: %d", r.status)
	}
	// ghi vào project B: dữ liệu mang project B
	req, _ := http.NewRequest("POST", srv.URL+"/fhir/R4/Patient", strings.NewReader(`{"resourceType":"Patient","name":[{"family":"Gamma"}]}`))
	req.Header.Set("Authorization", "Bearer "+super)
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Set("X-Fhir-Project", "B")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 201 || !strings.Contains(string(raw), `"project":"B"`) {
		t.Fatalf("ghi vào B: %d %s", res.StatusCode, raw)
	}
	if n := want(t, call(t, srv, "GET", "/fhir/R4/Patient", b, nil), 200).json(t)["entry"].([]any); len(n) != 2 {
		t.Errorf("B thấy %d Patient, muốn 2", len(n))
	}
	// export của super-admin chỉ ra dữ liệu của project đã chọn
	got, _ := exportAs(t, "", super, "/fhir/R4/$export", func(m, p, tk string, h ...string) resp {
		return do(m, p, tk, append(h, "X-Fhir-Project", "A")...)
	})
	if len(got["Patient"]) != 1 {
		t.Errorf("export project A: %v", counts(got))
	}

	// token thường: không được chọn project khác
	if r := do("GET", "/fhir/R4/Patient", a, "X-Fhir-Project", "B"); r.status != 403 {
		t.Errorf("token thường chọn project khác: %d", r.status)
	}
	if r := do("GET", "/fhir/R4/Patient", a, "X-Fhir-Project", "A"); r.status != 200 {
		t.Errorf("token thường chọn project của mình: %d", r.status)
	}
	// nhật ký kiểm toán
	out := logs.String()
	for _, want := range []string{`"audit":true`, `"target_project":"B"`, `"target_project":"A"`, `"method":"POST"`, `"subject":"user@"`} {
		if !strings.Contains(out, want) {
			t.Errorf("nhật ký kiểm toán thiếu %s", want)
		}
	}
	if strings.Contains(out, "Gamma") || strings.Contains(out, super) {
		t.Error("nhật ký không được chứa nội dung dữ liệu hoặc token")
	}
}

func TestSuperScopeIgnoredUnlessEnabled(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	super := tok(t, "", "fhir.super")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", super, nil), 403) // không project, scope bị bỏ qua
	withProject := tok(t, "A", "fhir.super")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", withProject, nil), 403) // scope không cấp quyền gì khi tắt
	if r := doer(t, srv)("GET", "/fhir/R4/Patient", withProject, "X-Fhir-Project", "B"); r.status != 403 {
		t.Errorf("tắt super-admin: %d", r.status)
	}
}

func TestGroupExportIsScopedToCallerProject(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")
	pa := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Alpha")), 201).str(t, "id")
	pb := want(t, call(t, srv, "POST", "/fhir/R4/Patient", b, patient("Beta")), 201).str(t, "id")
	ga := want(t, call(t, srv, "POST", "/fhir/R4/Group", a, map[string]any{"resourceType": "Group", "type": "person", "actual": true,
		"member": []any{map[string]any{"entity": map[string]any{"reference": "Patient/" + pa}}, map[string]any{"entity": map[string]any{"reference": "Patient/" + pb}}}}), 201).str(t, "id")
	do := doer(t, srv)

	// B không thấy Group của A
	if r := do("GET", "/fhir/R4/Group/"+ga+"/$export", b, "Prefer", "respond-async"); r.status != 404 {
		t.Fatalf("B export Group của A: %d %s", r.status, r.body)
	}
	// A xuất được, nhưng chỉ Patient của project A dù Group có tham chiếu Patient của B
	got, _ := exportAs(t, "", a, "/fhir/R4/Group/"+ga+"/$export", do)
	if len(got["Patient"]) != 1 || got["Patient"][0]["id"] != pa {
		t.Errorf("A xuất: %v", counts(got))
	}
}

func TestSubsumesAndTranslateNeedOnlyReadRole(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	rd, wr := tok(t, "A", "fhir.read"), tok(t, "A", "fhir.write")
	want(t, call(t, srv, "POST", "/fhir/R4/CodeSystem", wr, map[string]any{"resourceType": "CodeSystem", "url": "http://x/cs", "status": "active", "content": "complete",
		"concept": []any{map[string]any{"code": "a", "concept": []any{map[string]any{"code": "b"}}}}}), 201)
	want(t, call(t, srv, "GET", "/fhir/R4/CodeSystem/$subsumes?system=http://x/cs&codeA=a&codeB=b", rd, nil), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/ConceptMap/$translate", rd, map[string]any{"resourceType": "Parameters", "parameter": []any{
		map[string]any{"name": "system", "valueUri": "http://x/cs"}, map[string]any{"name": "code", "valueCode": "a"}}}), 200)
}

func TestGraphQLRespectsProjectGrantsAndPolicy(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")
	pid := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Alpha")), 201).str(t, "id")
	want(t, call(t, srv, "POST", "/fhir/R4/Observation", a, map[string]any{"resourceType": "Observation", "status": "final",
		"code": map[string]any{"text": "x"}, "subject": map[string]any{"reference": "Patient/" + pid}}), 201)
	gql := func(token, query string) map[string]any {
		t.Helper()
		return want(t, call(t, srv, "POST", "/fhir/R4/$graphql", token, map[string]any{"query": query}), 200).json(t)
	}
	data := func(m map[string]any, key string) any { return m["data"].(map[string]any)[key] }

	// token chỉ đọc dùng được GraphQL (chỉ có query)
	rd := tok(t, "A", "fhir.read")
	if data(gql(rd, `{ Patient(id: "`+pid+`") { id } }`), "Patient") == nil {
		t.Error("token đọc phải đọc được")
	}
	want(t, call(t, srv, "POST", "/fhir/R4/$graphql", "", map[string]any{"query": "{ PatientList { id } }"}), 401)

	// project B không thấy dữ liệu của A, kể cả qua Reference.resource và danh sách
	if data(gql(b, `{ Patient(id: "`+pid+`") { id } }`), "Patient") != nil {
		t.Error("B đọc được Patient của A")
	}
	if l, _ := data(gql(b, `{ PatientList { id } }`), "PatientList").([]any); len(l) != 0 {
		t.Errorf("B thấy %d Patient của A", len(l))
	}

	// scope theo loại: đọc được Observation nhưng Reference.resource tới Patient bị chặn (lỗi, không lộ dữ liệu)
	obsOnly := tok(t, "A", "system/Observation.read")
	r := gql(obsOnly, `{ ObservationList { id subject { reference resource { ... on Patient { id name { family } } } } } }`)
	obs := data(r, "ObservationList").([]any)
	if len(obs) != 1 || obs[0].(map[string]any)["subject"].(map[string]any)["resource"] != nil || r["errors"] == nil {
		t.Errorf("scope theo loại: %v", r)
	}
	if strings.Contains(string(mustMarshal(t, r)), "Alpha") {
		t.Error("dữ liệu Patient bị lộ qua GraphQL")
	}
	// loại không được đọc ở gốc: lỗi, data null
	r = gql(obsOnly, `{ PatientList { id } }`)
	if data(r, "PatientList") != nil || r["errors"] == nil {
		t.Errorf("PatientList ngoài quyền: %v", r)
	}
	// AccessPolicy: chỉ Patient, Observation bị loại khỏi chính sách
	pol := tok(t, "A", "fhir.write", func(c jwt.MapClaims) { c["fhir_access_policy"] = []any{"Patient"} })
	r = gql(pol, `{ ObservationList { id } Patient(id: "`+pid+`") { id } }`)
	if data(r, "ObservationList") != nil || data(r, "Patient") == nil || r["errors"] == nil {
		t.Errorf("AccessPolicy: %v", r)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
