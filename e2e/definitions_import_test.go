package e2e

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Nạp định nghĩa (#18): PackageRelease/$install, CodeSystem/$import, ConceptMap/$import qua auth + fhir-server thật (SQLite).
// Kiểm quyền admin lấy từ token của auth, cách ly project, và dữ liệu còn sau khi fhir-server khởi động lại.

func (s *stack) adminToken(email, clientID string) string {
	s.t.Helper()
	return s.signIn(email, clientID)["access_token"].(string)
}

// mustStatus báo lỗi nếu mã trả về khác want.
func (s *stack) mustStatus(r resp, want int, what string) resp {
	s.t.Helper()
	if r.status != want {
		s.t.Fatalf("%s: muốn %d, được %d: %s", what, want, r.status, r.body)
	}
	return r
}

func param(name string, kv ...string) string {
	return fmt.Sprintf(`{"name":%q,%s}`, name, strings.Join(kv, ","))
}

func coding(system, code, display string) string {
	return fmt.Sprintf(`"valueCoding":{"system":%q,"code":%q,"display":%q}`, system, code, display)
}

func partParam(name string, parts ...string) string {
	return fmt.Sprintf(`{"name":%q,"part":[%s]}`, name, strings.Join(parts, ","))
}

func parameters(ps ...string) string {
	return `{"resourceType":"Parameters","parameter":[` + strings.Join(ps, ",") + `]}`
}

func (s *stack) createResource(token, js string) string {
	s.t.Helper()
	typ := strings.Split(strings.TrimPrefix(js, `{"resourceType":"`), `"`)[0]
	return s.mustStatus(s.fhirPost(token, "/"+typ, js), 201, "tạo "+typ).json(s.t)["id"].(string)
}

func TestPackageInstallThroughRealProcesses(t *testing.T) {
	s := newStack(t)
	adminA := s.adminToken("a@example.org", s.clientA)
	adminB := s.adminToken("b@example.org", s.clientB)
	client := s.clientCredentials(s.clientA, s.secretA, "") // không phải admin

	bundle := `{"resourceType":"Bundle","type":"transaction","entry":[` +
		`{"resource":{"resourceType":"ValueSet","status":"active","url":"http://example.org/vs/pkg-e2e"},"request":{"method":"POST","url":"ValueSet"}},` +
		`{"resource":{"resourceType":"CodeSystem","status":"active","content":"complete","url":"http://example.org/cs/pkg-e2e","concept":[{"code":"a","display":"A"}]},"request":{"method":"POST","url":"CodeSystem"}}]}`
	bin := s.createResource(adminA, `{"resourceType":"Binary","contentType":"application/fhir+json","data":"`+base64.StdEncoding.EncodeToString([]byte(bundle))+`"}`)
	rel := s.createResource(adminA, `{"resourceType":"PackageRelease","version":"1.0.0","package":{"reference":"Package/p1"},"content":{"url":"Binary/`+bin+`"}}`)
	install := "/PackageRelease/" + rel + "/$install"

	s.mustStatus(s.fhirPost(client, install, ""), 403, "client không phải admin cài gói")
	s.mustStatus(s.fhirPost(adminB, install, ""), 404, "admin project khác cài gói của A")
	if r := s.fhirGet(adminA, "/ValueSet?url=http://example.org/vs/pkg-e2e"); len(entryIDs(t, r, "ValueSet")) != 0 {
		t.Fatalf("chưa cài mà đã có ValueSet: %s", r.body)
	}

	s.mustStatus(s.fhirPost(adminA, install, ""), 200, "admin cài gói")
	for _, q := range []string{"/ValueSet?url=http://example.org/vs/pkg-e2e", "/CodeSystem?url=http://example.org/cs/pkg-e2e"} {
		typ := strings.Split(strings.TrimPrefix(q, "/"), "?")[0]
		if got := entryIDs(t, s.fhirGet(adminA, q), typ); len(got) != 1 {
			t.Errorf("%s sau khi cài: %v", q, got)
		}
		if got := entryIDs(t, s.fhirGet(adminB, q), typ); len(got) != 0 {
			t.Errorf("project B thấy %s của A: %v", typ, got)
		}
	}
	inst := s.fhirGet(adminA, "/PackageInstallation?_sort=-_lastUpdated").json(t)
	if entries, _ := inst["entry"].([]any); len(entries) != 1 || !strings.Contains(fmt.Sprint(entries[0]), "installed") {
		t.Errorf("PackageInstallation: %v", inst)
	}

	// gói hỏng: entry lỗi làm hỏng cả lần cài và không để lại gì
	badBundle := `{"resourceType":"Bundle","type":"transaction","entry":[` +
		`{"resource":{"resourceType":"ValueSet","status":"active","url":"http://example.org/vs/never"},"request":{"method":"POST","url":"ValueSet"}},` +
		`{"resource":{"resourceType":"Patient","active":"khong-phai-boolean"},"request":{"method":"POST","url":"Patient"}}]}`
	bin2 := s.createResource(adminA, `{"resourceType":"Binary","contentType":"application/fhir+json","data":"`+base64.StdEncoding.EncodeToString([]byte(badBundle))+`"}`)
	rel2 := s.createResource(adminA, `{"resourceType":"PackageRelease","version":"2.0.0","package":{"reference":"Package/p1"},"content":{"url":"Binary/`+bin2+`"}}`)
	s.mustStatus(s.fhirPost(adminA, "/PackageRelease/"+rel2+"/$install", ""), 400, "gói có entry lỗi")
	if got := entryIDs(t, s.fhirGet(adminA, "/ValueSet?url=http://example.org/vs/never"), "ValueSet"); len(got) != 0 {
		t.Errorf("transaction lỗi vẫn để lại ValueSet: %v", got)
	}

	// còn nguyên sau khi fhir-server khởi động lại
	s.restartFhirServer()
	if got := entryIDs(t, s.fhirGet(adminA, "/CodeSystem?url=http://example.org/cs/pkg-e2e"), "CodeSystem"); len(got) != 1 {
		t.Errorf("sau restart: %v", got)
	}
}

func TestCodeSystemImportThroughRealProcesses(t *testing.T) {
	s := newStack(t)
	adminA := s.adminToken("a@example.org", s.clientA)
	adminB := s.adminToken("b@example.org", s.clientB)
	client := s.clientCredentials(s.clientA, s.secretA, "")
	const sys = "http://example.org/cs/big-e2e"

	cs := s.createResource(adminA, `{"resourceType":"CodeSystem","url":"`+sys+`","status":"active","content":"not-present","name":"Big","property":[{"code":"kind","type":"string"}]}`)
	imp := "/CodeSystem/" + cs + "/$import"
	body := parameters(
		param("concept", coding(sys, "R", "Root")), param("concept", coding(sys, "A", "Alpha")), param("concept", coding(sys, "B", "Beta")), param("concept", coding(sys, "C", "Gamma")),
		partParam("property", `{"name":"code","valueCode":"A"}`, `{"name":"property","valueCode":"parent"}`, `{"name":"value","valueString":"R"}`),
		partParam("property", `{"name":"code","valueCode":"B"}`, `{"name":"property","valueCode":"parent"}`, `{"name":"value","valueString":"A"}`),
		partParam("property", `{"name":"code","valueCode":"C"}`, `{"name":"property","valueCode":"parent"}`, `{"name":"value","valueString":"R"}`),
		partParam("property", `{"name":"code","valueCode":"A"}`, `{"name":"property","valueCode":"kind"}`, `{"name":"value","valueString":"x"}`),
		partParam("designation", `{"name":"code","valueCode":"A"}`, `{"name":"language","valueCode":"vi"}`, `{"name":"value","valueString":"Anpha"}`))

	s.mustStatus(s.fhirPost(client, imp, body), 403, "client không phải admin nạp concept")
	s.mustStatus(s.fhirPost(adminB, imp, body), 404, "admin project khác nạp vào CodeSystem của A")
	s.mustStatus(s.fhirPost(adminA, imp, parameters(param("concept", coding(sys, "X", "x")), partParam("property", `{"name":"code","valueCode":"X"}`, `{"name":"property","valueCode":"nope"}`, `{"name":"value","valueString":"1"}`))), 400, "property chưa khai báo")
	s.mustStatus(s.fhirPost(adminA, imp, body), 200, "admin nạp")
	s.mustStatus(s.fhirPost(adminA, "/CodeSystem/$import", parameters(param("system", `"valueUri":"`+sys+`"`), param("concept", coding(sys, "D", "Delta")))), 200, "nạp thêm theo system")

	// terminology đọc concept từ kho
	lk := s.mustStatus(s.fhirGet(adminA, "/CodeSystem/$lookup?system="+url.QueryEscape(sys)+"&code=A"), 200, "$lookup").json(t)
	if txt := fmt.Sprint(lk); !strings.Contains(txt, "Alpha") || !strings.Contains(txt, "Anpha") || !strings.Contains(txt, "child") {
		t.Errorf("$lookup: %v", lk)
	}
	s.mustStatus(s.fhirGet(adminA, "/CodeSystem/$lookup?system="+url.QueryEscape(sys)+"&code=NOPE"), 404, "$lookup mã không có")
	if v := s.fhirGet(adminA, "/CodeSystem/$validate-code?url="+url.QueryEscape(sys)+"&code=B").json(t); !strings.Contains(fmt.Sprint(v), "valueBoolean:true") {
		t.Errorf("$validate-code B: %v", v)
	}
	vs := s.createResource(adminA, `{"resourceType":"ValueSet","url":"http://example.org/vs/big-e2e","status":"active","compose":{"include":[{"system":"`+sys+`","filter":[{"property":"concept","op":"is-a","value":"A"}]}]}}`)
	exp := s.mustStatus(s.fhirGet(adminA, "/ValueSet/"+vs+"/$expand"), 200, "$expand is-a").json(t)
	var codes []string
	for _, c := range exp["expansion"].(map[string]any)["contains"].([]any) {
		codes = append(codes, c.(map[string]any)["code"].(string))
	}
	if strings.Join(codes, ",") != "A,B" {
		t.Errorf("$expand is-a A: %v", codes)
	}
	if v := s.fhirGet(adminA, "/ValueSet/"+vs+"/$validate-code?system="+url.QueryEscape(sys)+"&code=C").json(t); !strings.Contains(fmt.Sprint(v), "valueBoolean:false") {
		t.Errorf("C không thuộc is-a A: %v", v)
	}

	// project B không thấy CodeSystem nên không tra được concept
	s.mustStatus(s.fhirGet(adminB, "/CodeSystem/$lookup?system="+url.QueryEscape(sys)+"&code=A"), 404, "project B tra concept của A")

	// bền qua restart: concept nằm trong CSDL
	s.restartFhirServer()
	if exp := s.mustStatus(s.fhirGet(adminA, "/ValueSet/"+vs+"/$expand"), 200, "$expand sau restart").json(t); fmt.Sprint(exp["expansion"].(map[string]any)["total"]) != "2" {
		t.Errorf("sau restart: %v", exp["expansion"])
	}
}

func TestConceptMapImportThroughRealProcesses(t *testing.T) {
	s := newStack(t)
	adminA := s.adminToken("a@example.org", s.clientA)
	adminB := s.adminToken("b@example.org", s.clientB)
	client := s.clientCredentials(s.clientA, s.secretA, "")
	const a, b = "http://example.org/cs/a", "http://example.org/cs/b"

	cm := s.createResource(adminA, `{"resourceType":"ConceptMap","url":"http://example.org/cm/ab-e2e","status":"active"}`)
	imp := "/ConceptMap/" + cm + "/$import"
	mapping := func(sc, tc, rel string) string {
		parts := []string{`{"name":"source",` + coding(a, sc, "") + `}`, `{"name":"target",` + coding(b, tc, "") + `}`}
		if rel != "" {
			parts = append(parts, `{"name":"relationship","valueCode":"`+rel+`"}`)
		}
		return partParam("mapping", parts...)
	}
	body := parameters(mapping("1", "X", ""), mapping("1", "Y", "wider"), mapping("2", "", ""))

	s.mustStatus(s.fhirPost(client, imp, body), 403, "client không phải admin nạp ánh xạ")
	s.mustStatus(s.fhirPost(adminB, imp, body), 404, "admin project khác nạp vào ConceptMap của A")
	s.mustStatus(s.fhirPost(adminA, imp, parameters(partParam("mapping", `{"name":"source",`+coding(a, "1", "")+`}`))), 400, "mapping thiếu target")
	s.mustStatus(s.fhirPost(adminA, imp, body), 200, "admin nạp")
	s.mustStatus(s.fhirPost(adminA, imp, body), 200, "nạp lại cùng dữ liệu")
	s.mustStatus(s.fhirPost(adminA, "/ConceptMap/$import", parameters(param("url", `"valueUri":"http://example.org/cm/ab-e2e"`), mapping("3", "Z", ""))), 200, "nạp theo url")

	tr := func(token, q string) string {
		r := s.fhirGet(token, "/ConceptMap/"+cm+"/$translate?"+q)
		s.mustStatus(r, 200, "$translate "+q)
		return fmt.Sprint(r.json(t))
	}
	if got := tr(adminA, "system="+url.QueryEscape(a)+"&code=1"); !strings.Contains(got, "valueCode:equivalent") || !strings.Contains(got, "valueCode:wider") ||
		!strings.Contains(got, "code:X") || !strings.Contains(got, "code:Y") || strings.Count(got, "name:match") != 2 {
		t.Errorf("$translate 1: %s", got)
	}
	if got := tr(adminA, "system="+url.QueryEscape(b)+"&code=Y&reverse=true"); !strings.Contains(got, "valueCode:narrower") || !strings.Contains(got, "code:1") {
		t.Errorf("$translate ngược: %s", got)
	}
	if got := tr(adminA, "system="+url.QueryEscape(a)+"&code=2"); strings.Contains(got, "name:match") {
		t.Errorf("ánh xạ rỗng không là bản dịch: %s", got)
	}
	s.mustStatus(s.fhirGet(adminB, "/ConceptMap/"+cm+"/$translate?system="+url.QueryEscape(a)+"&code=1"), 404, "project B dịch bằng ConceptMap của A")

	s.restartFhirServer()
	if got := tr(adminA, "system="+url.QueryEscape(a)+"&code=3"); !strings.Contains(got, "code:Z") {
		t.Errorf("sau restart: %s", got)
	}
}
