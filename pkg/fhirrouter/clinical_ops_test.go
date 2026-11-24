package fhirrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func clinicalEnv(t *testing.T) (fhirrouter.Repository, http.Handler) {
	t.Helper()
	repo := fhirrouter.NewMemoryRepository()
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true, SkipValidation: true, SkipConstraints: true}).HTTPHandler()
	return repo, h
}

func mustPut(t *testing.T, h http.Handler, path, body string) {
	t.Helper()
	if code, out := putTo(h, "PUT", path, body); code != 200 && code != 201 {
		t.Fatalf("PUT %s: %d %s", path, code, out)
	}
}

func csvOf(t *testing.T, h http.Handler, path string) (string, http.Header) {
	t.Helper()
	req := httptest.NewRequest("GET", "/"+path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
	}
	return w.Body.String(), w.Result().Header
}

func TestCsvExportsColumnsFromSearchParamsAndFhirPath(t *testing.T) {
	_, h := clinicalEnv(t)
	mustPut(t, h, "Patient/p1", `{"resourceType":"Patient","id":"p1","gender":"female","birthDate":"1980-02-03",
	 "name":[{"given":["An","Thị"],"family":"Nguyễn","prefix":["Bà"]}],"telecom":[{"system":"email","value":"an@example.org"}],
	 "address":[{"line":["1 Lê Lợi"],"city":"Huế","state":"TTH","postalCode":"530000"}],"managingOrganization":{"reference":"Organization/o1","display":"Bệnh viện Trung ương"},
	 "maritalStatus":{"coding":[{"code":"M","display":"Married"}]},"generalPractitioner":[{"reference":"Practitioner/x"}]}`)
	mustPut(t, h, "Patient/p2", `{"resourceType":"Patient","id":"p2","gender":"male","name":[{"family":"=cmd|' /C calc'!A0","given":["Hack"]}],"maritalStatus":{"text":"Độc thân"},"telecom":[{"system":"phone","value":"+84-900"}]}`)
	body, hdr := csvOf(t, h, "Patient/$csv?_fields=name,gender,birthdate,address,email,organization,maritalStatus,generalPractitioner,id,phone&_sort=_id")
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") || hdr.Get("Content-Type") != "text/csv" || !strings.Contains(hdr.Get("Content-Disposition"), "export.csv") {
		t.Fatalf("tiêu đề phản hồi: %q %v", body[:min(len(body), 20)], hdr)
	}
	lines := strings.Split(strings.TrimPrefix(body, "\xEF\xBB\xBF"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d dòng:\n%s", len(lines), body)
	}
	if lines[0] != `"name","gender","birthdate","address","email","organization","maritalStatus","generalPractitioner","id","phone"` {
		t.Errorf("tiêu đề cột: %s", lines[0])
	}
	want1 := `"Bà An Thị Nguyễn","female","1980-02-03","1 Lê Lợi, Huế, TTH, 530000","an@example.org","Bệnh viện Trung ương","Married","Practitioner/x","p1",`
	if lines[1] != want1 {
		t.Errorf("dòng p1:\n%s\nmuốn:\n%s", lines[1], want1)
	}
	// p2: chống CSV injection (ô bắt đầu bằng + hoặc = thêm dấu nháy đơn), CodeableConcept theo text, ô trống
	if !strings.Contains(lines[2], `,"'+84-900"`) {
		t.Errorf("dòng p2 không chống CSV injection ở số điện thoại: %s", lines[2])
	}
	if !strings.Contains(lines[2], `"Độc thân"`) {
		t.Errorf("CodeableConcept dùng text: %s", lines[2])
	}
	// bộ lọc tìm kiếm áp dụng, biểu thức FHIRPath tự do làm cột
	body, _ = csvOf(t, h, "Patient/$csv?_fields=id,name.family&gender=male")
	if body != "\xEF\xBB\xBF"+`"id","name.family"`+"\n"+`"p2","'=cmd|' /C calc'!A0"` {
		t.Errorf("lọc và FHIRPath: %q", body)
	}
	// lỗi
	for path, want := range map[string]int{
		"Patient/$csv":                    400, // thiếu _fields
		"NotAType/$csv?_fields=id":        404, // loại không có thì không có route
		"Patient/$csv?_fields=id&bogus=1": 400, // tham số tìm kiếm lạ bị từ chối như tìm kiếm thường
		"Patient/$csv?_fields=name.(":     400, // FHIRPath hỏng
	} {
		if code, out := putTo(h, "GET", path, ""); code != want {
			t.Errorf("%s: %d, muốn %d (%s)", path, code, want, out)
		}
	}
}

func TestRefreshReferenceDisplay(t *testing.T) {
	repo, h := clinicalEnv(t)
	mustPut(t, h, "Patient/p1", `{"resourceType":"Patient","id":"p1","name":[{"given":["An"],"family":"Nguyễn"}]}`)
	mustPut(t, h, "Practitioner/d1", `{"resourceType":"Practitioner","id":"d1","name":[{"family":"Trần","given":["Bác"],"prefix":["BS"]}]}`)
	mustPut(t, h, "Organization/o1", `{"resourceType":"Organization","id":"o1","name":"Bệnh viện A"}`)
	mustPut(t, h, "Observation/ob1", `{"resourceType":"Observation","id":"ob1","status":"final","code":{"text":"Huyết áp"},
	 "subject":{"reference":"Patient/p1","display":"CŨ"},"performer":[{"reference":"Practitioner/d1"},{"reference":"Organization/o1"},{"reference":"Patient/khong-co","display":"giữ nguyên"},{"reference":"#contained"},{"reference":"urn:uuid:abc"}]}`)
	mustPut(t, h, "Encounter/e1", `{"resourceType":"Encounter","id":"e1","status":"finished","class":{"code":"AMB"},"subject":{"reference":"Observation/ob1"}}`)
	code, body := putTo(h, "POST", "Observation/ob1/$refresh-reference-display", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if got["subject"].(map[string]any)["display"] != "An Nguyễn" {
		t.Errorf("subject: %v", got["subject"])
	}
	perf := got["performer"].([]any)
	wantDisp := []any{"BS Bác Trần", "Bệnh viện A", "giữ nguyên", nil, nil}
	for i, w := range wantDisp {
		if d := perf[i].(map[string]any)["display"]; d != w {
			t.Errorf("performer[%d].display = %v, muốn %v", i, d, w)
		}
	}
	// phiên bản tăng một lần; chạy lại khi không còn gì đổi thì không tạo phiên bản mới
	v1 := got["meta"].(map[string]any)["versionId"]
	_, body2 := putTo(h, "POST", "Observation/ob1/$refresh-reference-display", "")
	var again map[string]any
	_ = json.Unmarshal([]byte(body2), &again)
	if again["meta"].(map[string]any)["versionId"] != v1 {
		t.Errorf("không đổi gì mà tạo phiên bản mới: %v → %v", v1, again["meta"])
	}
	// resource không tham chiếu được dùng "Loại/id" khi không có tên
	mustPut(t, h, "Encounter/e2", `{"resourceType":"Encounter","id":"e2","status":"finished","class":{"code":"AMB"},"subject":{"reference":"Encounter/e1"}}`)
	_, body3 := putTo(h, "POST", "Encounter/e2/$refresh-reference-display", "")
	if !strings.Contains(body3, `"display":"Encounter/e1"`) {
		t.Errorf("fallback Loại/id: %s", body3)
	}
	// lỗi
	for path, want := range map[string]int{"Observation/none/$refresh-reference-display": 404, "NotAType/x/$refresh-reference-display": 404} {
		if c, out := putTo(h, "POST", path, ""); c != want {
			t.Errorf("%s: %d (%s)", path, c, out)
		}
	}
	_ = repo
}

func TestExpandProfileLoadsNestedProfiles(t *testing.T) {
	_, h := clinicalEnv(t)
	sd := func(id, url, version string, profiles ...string) string {
		els := ""
		for _, p := range profiles {
			els += `{"id":"x","path":"X.e","type":[{"code":"Extension","profile":["` + p + `"]}]},`
		}
		return `{"resourceType":"StructureDefinition","id":"` + id + `","url":"` + url + `","version":"` + version + `","name":"` + id + `","status":"active","kind":"resource","abstract":false,"type":"Patient",
		"snapshot":{"element":[` + strings.TrimSuffix(els, ",") + `]}}`
	}
	mustPut(t, h, "StructureDefinition/root", sd("root", "http://x/root", "2", "http://x/a", "http://x/b"))
	mustPut(t, h, "StructureDefinition/root-old", sd("root-old", "http://x/root", "1"))
	mustPut(t, h, "StructureDefinition/a", sd("a", "http://x/a", "1", "http://x/c", "http://x/root")) // vòng về root
	mustPut(t, h, "StructureDefinition/b", sd("b", "http://x/b", "1"))
	mustPut(t, h, "StructureDefinition/c", sd("c", "http://x/c", "1", "http://x/ma"))
	code, body := putTo(h, "GET", "StructureDefinition/$expand-profile?url=http://x/root", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var b struct {
		Type  string `json:"type"`
		Entry []struct {
			Resource struct{ ID, Version string } `json:"resource"`
		} `json:"entry"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	var ids []string
	for _, e := range b.Entry {
		ids = append(ids, e.Resource.ID)
	}
	if b.Type != "searchset" || strings.Join(ids, ",") != "root,a,c,b" {
		t.Errorf("Bundle: %s %v (muốn root theo bản mới nhất, a, c, b; vòng không lặp, profile thiếu bị bỏ qua)", b.Type, ids)
	}
	for path, want := range map[string]int{"StructureDefinition/$expand-profile": 400, "StructureDefinition/$expand-profile?url=http://x/none": 400} {
		if c, out := putTo(h, "GET", path, ""); c != want {
			t.Errorf("%s: %d (%s)", path, c, out)
		}
	}
	if c, out := putTo(h, "POST", "StructureDefinition/$expand-profile", `{"resourceType":"Parameters","parameter":[{"name":"url","valueUri":"http://x/b"}]}`); c != 200 || !strings.Contains(out, `"id":"b"`) {
		t.Errorf("POST Parameters: %d %s", c, out)
	}
}

func TestGraphFollowsPathAndSearchLinks(t *testing.T) {
	_, h := clinicalEnv(t)
	mustPut(t, h, "Patient/p1", `{"resourceType":"Patient","id":"p1","managingOrganization":{"reference":"Organization/o1"},"generalPractitioner":[{"reference":"Practitioner/d1"},{"reference":"Organization/o2"}]}`)
	mustPut(t, h, "Organization/o1", `{"resourceType":"Organization","id":"o1","name":"Cha","partOf":{"reference":"Organization/o0"}}`)
	mustPut(t, h, "Organization/o0", `{"resourceType":"Organization","id":"o0","name":"Gốc"}`)
	mustPut(t, h, "Organization/o2", `{"resourceType":"Organization","id":"o2","name":"Khác"}`)
	mustPut(t, h, "Practitioner/d1", `{"resourceType":"Practitioner","id":"d1"}`)
	for i, subj := range []string{"p1", "p1", "p2"} {
		mustPut(t, h, "Observation/ob"+string(rune('1'+i)), `{"resourceType":"Observation","id":"ob`+string(rune('1'+i))+`","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+subj+`"}}`)
	}
	mustPut(t, h, "GraphDefinition/g1", `{"resourceType":"GraphDefinition","id":"g1","name":"patient-graph","status":"active","start":"Patient","link":[
	 {"path":"Patient.managingOrganization","target":[{"type":"Organization","link":[{"path":"Organization.partOf","target":[{"type":"Organization"}]}]}]},
	 {"path":"Patient.generalPractitioner","target":[{"type":"Practitioner"}]},
	 {"max":"10","target":[{"type":"Observation","params":"subject={ref}"}]}]}`)
	code, body := putTo(h, "GET", "Patient/p1/$graph?graph=patient-graph", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var b struct {
		Type  string `json:"type"`
		Entry []struct {
			Resource struct {
				ResourceType string
				ID           string
			} `json:"resource"`
		} `json:"entry"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	got := map[string]bool{}
	for _, e := range b.Entry {
		got[e.Resource.ResourceType+"/"+e.Resource.ID] = true
	}
	for _, w := range []string{"Patient/p1", "Organization/o1", "Organization/o0", "Practitioner/d1", "Observation/ob1", "Observation/ob2"} {
		if !got[w] {
			t.Errorf("thiếu %s trong %v", w, got)
		}
	}
	if got["Organization/o2"] || got["Observation/ob3"] || len(b.Entry) != 6 || b.Type != "collection" {
		t.Errorf("thừa hoặc trùng: %d entry %v (o2 là Organization nên không khớp target Practitioner, ob3 thuộc bệnh nhân khác)", len(b.Entry), got)
	}
	// lỗi
	for path, want := range map[string]int{
		"Patient/p1/$graph":                           400,
		"Patient/p1/$graph?graph=khong-co":            404,
		"Patient/khong-co/$graph?graph=patient-graph": 404,
		"Observation/ob1/$graph?graph=patient-graph":  400, // start không khớp
	} {
		if c, out := putTo(h, "GET", path, ""); c != want {
			t.Errorf("%s: %d, muốn %d (%s)", path, c, want, out)
		}
	}
	mustPut(t, h, "GraphDefinition/g2", `{"resourceType":"GraphDefinition","id":"g2","name":"bad-search","status":"active","start":"Patient","link":[{"target":[{"type":"Observation","params":"subject=Patient/p1"}]}]}`)
	mustPut(t, h, "GraphDefinition/g3", `{"resourceType":"GraphDefinition","id":"g3","name":"bad-path","status":"active","start":"Patient","link":[{"path":"Patient.id","target":[{"type":"Organization"}]}]}`)
	mustPut(t, h, "GraphDefinition/g4", `{"resourceType":"GraphDefinition","id":"g4","name":"bad-link","status":"active","start":"Patient","link":[{"target":[{"type":"Observation"}]}]}`)
	for _, name := range []string{"bad-search", "bad-path", "bad-link"} {
		if c, out := putTo(h, "GET", "Patient/p1/$graph?graph="+name, ""); c != 400 {
			t.Errorf("%s: %d (%s)", name, c, out)
		}
	}
}

// Vòng tham chiếu và cây quá sâu không làm treo: dừng ở 5 tầng.
func TestGraphStopsOnCyclesAndDepth(t *testing.T) {
	_, h := clinicalEnv(t)
	mustPut(t, h, "Organization/a", `{"resourceType":"Organization","id":"a","partOf":{"reference":"Organization/b"}}`)
	mustPut(t, h, "Organization/b", `{"resourceType":"Organization","id":"b","partOf":{"reference":"Organization/a"}}`)
	link := `{"path":"Organization.partOf","target":[{"type":"Organization","link":[LINK]}]}`
	nested := `{"path":"Organization.partOf","target":[{"type":"Organization"}]}`
	for i := 0; i < 8; i++ {
		nested = strings.Replace(link, "LINK", nested, 1)
	}
	mustPut(t, h, "GraphDefinition/cyc", `{"resourceType":"GraphDefinition","id":"cyc","name":"cyc","status":"active","start":"Organization","link":[`+nested+`]}`)
	code, body := putTo(h, "GET", "Organization/a/$graph?graph=cyc", "")
	if code != 200 || strings.Count(body, `"resourceType":"Organization"`) != 2 {
		t.Errorf("vòng a↔b phải cho đúng 2 resource: %d %s", code, body)
	}
}

type fakeResender struct {
	fhirrouter.Repository
	calls []string
	err   error
}

func (f *fakeResender) ResendSubscriptions(_ context.Context, rt, id, vid, interaction string) error {
	f.calls = append(f.calls, rt+"/"+id+"|"+vid+"|"+interaction)
	return f.err
}

func TestResendDelegatesToRepository(t *testing.T) {
	base := &fakeResender{Repository: fhirrouter.NewMemoryRepository()}
	h := fhirrouter.New(base, fhirrouter.Options{}).HTTPHandler()
	for _, c := range []struct{ path, body, want string }{
		{"Patient/p1/$resend", "", "Patient/p1||update"},
		{"Patient/p1/$resend", `{"interaction":"create"}`, "Patient/p1||create"},
		{"Patient/p1/_history/v7/$resend", `{"interaction":"delete"}`, "Patient/p1|v7|delete"},
	} {
		base.calls = nil
		if code, out := putTo(h, "POST", c.path, c.body); code != 200 || len(base.calls) != 1 || base.calls[0] != c.want {
			t.Errorf("%s: %d %s calls=%v (muốn %s)", c.path, code, out, base.calls, c.want)
		}
	}
	if code, _ := putTo(h, "POST", "Patient/p1/$resend", `{"interaction":"nope"}`); code != 400 {
		t.Errorf("interaction sai: %d", code)
	}
	if code, _ := putTo(h, "POST", "NotAType/p1/$resend", ""); code != 404 && code != 400 {
		t.Errorf("loại sai: %d", code)
	}
	base.err = fhirrouter.Forbidden("không phải admin")
	if code, _ := putTo(h, "POST", "Patient/p1/$resend", ""); code != 403 {
		t.Errorf("lỗi của repository phải được chuyển nguyên: %d", code)
	}
	// repository không hỗ trợ: 501
	plain := fhirrouter.New(fhirrouter.NewMemoryRepository(), fhirrouter.Options{}).HTTPHandler()
	if code, _ := putTo(plain, "POST", "Patient/p1/$resend", ""); code != 501 {
		t.Errorf("không hỗ trợ: %d", code)
	}
	var _ = fhir.Patient{}
}

// Chuỗi profile quá dài: dừng ở 10 tầng. Chuỗi Organization quá sâu: $graph dừng ở 5 tầng.
func TestDepthLimits(t *testing.T) {
	_, h := clinicalEnv(t)
	for i := 0; i < 14; i++ {
		next := ""
		if i < 13 {
			next = `{"id":"x","path":"X.e","type":[{"code":"Extension","profile":["http://x/p` + string(rune('a'+i+1)) + `"]}]}`
		}
		mustPut(t, h, "StructureDefinition/p"+string(rune('a'+i)), `{"resourceType":"StructureDefinition","id":"p`+string(rune('a'+i))+`","url":"http://x/p`+string(rune('a'+i))+`","name":"n","status":"active","kind":"resource","abstract":false,"type":"Patient","snapshot":{"element":[`+next+`]}}`)
	}
	_, body := putTo(h, "GET", "StructureDefinition/$expand-profile?url=http://x/pa", "")
	if n := strings.Count(body, `"resourceType":"StructureDefinition"`); n != 11 { // gốc và 10 tầng
		t.Errorf("expand-profile: %d profile, muốn 11 (gốc + 10 tầng)", n)
	}

	for i := 0; i < 9; i++ {
		partOf := ""
		if i < 8 {
			partOf = `,"partOf":{"reference":"Organization/g` + string(rune('0'+i+1)) + `"}`
		}
		mustPut(t, h, "Organization/g"+string(rune('0'+i)), `{"resourceType":"Organization","id":"g`+string(rune('0'+i))+`","name":"n"`+partOf+`}`)
	}
	link := `{"path":"Organization.partOf","target":[{"type":"Organization"}]}`
	for i := 0; i < 8; i++ {
		link = `{"path":"Organization.partOf","target":[{"type":"Organization","link":[` + link + `]}]}`
	}
	mustPut(t, h, "GraphDefinition/deep", `{"resourceType":"GraphDefinition","id":"deep","name":"deep","status":"active","start":"Organization","link":[`+link+`]}`)
	_, body = putTo(h, "GET", "Organization/g0/$graph?graph=deep", "")
	if n := strings.Count(body, `"resourceType":"Organization"`); n != 6 {
		t.Errorf("graph: %d resource, muốn 6 (gốc + 5 tầng)", n)
	}
}

type bumpOnUpdate struct {
	fhirrouter.Repository
	bumped bool
}

func (b *bumpOnUpdate) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	if !b.bumped {
		b.bumped = true
		cur, _ := b.Repository.ReadResource(ctx, r.GetResourceType(), r.GetID())
		_, _ = b.Repository.UpdateResource(ctx, cur, fhirrouter.UpdateOptions{})
	}
	return b.Repository.UpdateResource(ctx, r, o)
}

// Ghi song song làm đổi phiên bản giữa lúc đọc và ghi: không ghi đè (If-Match), trả 412.
func TestRefreshReferenceDisplayDoesNotOverwriteConcurrentChange(t *testing.T) {
	base := fhirrouter.NewMemoryRepository()
	repo := &bumpOnUpdate{Repository: base, bumped: true}
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true, SkipValidation: true, SkipConstraints: true}).HTTPHandler()
	mustPut(t, h, "Patient/p1", `{"resourceType":"Patient","id":"p1","name":[{"family":"Nguyễn"}]}`)
	mustPut(t, h, "Observation/ob1", `{"resourceType":"Observation","id":"ob1","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/p1","display":"CŨ"}}`)
	repo.bumped = false
	if code, out := putTo(h, "POST", "Observation/ob1/$refresh-reference-display", ""); code != 412 {
		t.Errorf("ghi song song phải 412: %d %s", code, out)
	}
}
