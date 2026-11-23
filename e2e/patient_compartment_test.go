package e2e

import (
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
)

// P8 M4: compartment Patient qua hai tiến trình thật (auth và fhir-server). Bệnh nhân được mời bằng chính sách mặc định của Patient; token của họ do auth cấp
// (cờ AUTH_PATIENT_COMPARTMENT_ENABLED bật) và fhir-server thi hành. Bệnh nhân A không được đọc dữ liệu của B qua mọi đường đọc.

type patientFixture struct {
	pa, pb                    string // id Patient của A và B
	obsA, obsB, obsShared     string // Observation: của A, của B, subject B nhưng performer A
	condA, condB, commA, comB string
	tokA                      string
}

// invitePatient mời một bệnh nhân (admin API) và trả id Patient được tạo.
func (s *stack) invitePatient(admin, email, first string) string {
	s.t.Helper()
	inv := s.bearerJSON(admin, "/admin/projects/"+s.projectA+"/invite", map[string]any{
		"resourceType": "Patient", "firstName": first, "lastName": "E2E", "email": email, "password": "mat-khau-benh-nhan-123", "sendEmail": false,
	})
	if inv.status != 200 {
		s.t.Fatalf("mời bệnh nhân %s: %d %s", email, inv.status, inv.body)
	}
	profile, _ := inv.json(s.t)["profile"].(map[string]any)
	ref, _ := profile["reference"].(string)
	if !strings.HasPrefix(ref, "Patient/") {
		s.t.Fatalf("membership phải có hồ sơ Patient: %s", inv.body)
	}
	return strings.TrimPrefix(ref, "Patient/")
}

// patientToken đăng nhập một bệnh nhân (email, mật khẩu) vào project A rồi đổi code lấy access token.
func (s *stack) patientToken(email string) string {
	s.t.Helper()
	lg := s.postJSON("/auth/login", map[string]any{"email": email, "password": "mat-khau-benh-nhan-123", "projectId": s.projectA, "clientId": s.clientA,
		"scope": "openid", "codeChallenge": challengeS256(), "codeChallengeMethod": "S256"})
	code, _ := lg.json(s.t)["code"].(string)
	if lg.status != 200 || code == "" {
		s.t.Fatalf("đăng nhập %s: %d %s", email, lg.status, lg.body)
	}
	tr := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {s.clientA}})
	if tr.status != 200 {
		s.t.Fatalf("đổi code của %s: %d %s", email, tr.status, tr.body)
	}
	return tr.json(s.t)["access_token"].(string)
}

func (s *stack) createFHIR(admin, js string) string {
	s.t.Helper()
	r := s.fhirPost(admin, "/"+strings.Split(strings.TrimPrefix(js, `{"resourceType":"`), `"`)[0], js)
	if r.status != 201 {
		s.t.Fatalf("tạo %.60s: %d %s", js, r.status, r.body)
	}
	return r.json(s.t)["id"].(string)
}

// createPatientRecord tạo hồ sơ Patient trong fhir-server với đúng id mà auth đã cấp cho hồ sơ của membership. Hồ sơ trong auth (schema identity) và dữ
// liệu lâm sàng trong fhir-server là hai nơi riêng, auth không kiểm Patient có tồn tại; cần FHIR_UPDATE_AS_CREATE=true để chọn id.
func (s *stack) createPatientRecord(admin, id, family string) {
	s.t.Helper()
	js := `{"resourceType":"Patient","id":"` + id + `","name":[{"family":"` + family + `"}]}`
	if r := do(s.t, "PUT", s.fhir+"/fhir/R4/Patient/"+id, admin, "application/fhir+json", strings.NewReader(js)); r.status != 201 && r.status != 200 {
		s.t.Fatalf("tạo hồ sơ Patient/%s trong fhir-server: %d %s", id, r.status, r.body)
	}
}

func newPatientFixture(t *testing.T, s *stack) *patientFixture {
	t.Helper()
	admin := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	f := &patientFixture{}
	f.pa = s.invitePatient(admin, "pa@example.org", "A")
	f.pb = s.invitePatient(admin, "pb@example.org", "B")
	s.createPatientRecord(admin, f.pa, "A")
	s.createPatientRecord(admin, f.pb, "B")
	obs := func(subject, performer string) string {
		js := `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/` + subject + `"}`
		if performer != "" {
			js += `,"performer":[{"reference":"Patient/` + performer + `"}]`
		}
		return js + `}`
	}
	f.obsA = s.createFHIR(admin, obs(f.pa, ""))
	f.obsB = s.createFHIR(admin, obs(f.pb, ""))
	f.obsShared = s.createFHIR(admin, obs(f.pb, f.pa))
	cond := func(p string) string {
		return `{"resourceType":"Condition","subject":{"reference":"Patient/` + p + `"},"clinicalStatus":{"text":"active"}}`
	}
	f.condA, f.condB = s.createFHIR(admin, cond(f.pa)), s.createFHIR(admin, cond(f.pb))
	comm := func(p string) string {
		return `{"resourceType":"Communication","status":"completed","sender":{"reference":"Patient/` + p + `"}}`
	}
	f.commA, f.comB = s.createFHIR(admin, comm(f.pa)), s.createFHIR(admin, comm(f.pb))
	for i := 0; i < 3; i++ { // thêm dữ liệu của B mới hơn, để trang đầu không vô tình đúng
		s.createFHIR(admin, obs(f.pb, ""))
	}
	f.tokA = s.patientToken("pa@example.org")
	return f
}

// entryIDs trả "Loại/id" của các resource trong Bundle, đã sắp xếp (lọc theo loại nếu only khác rỗng).
func entryIDs(t *testing.T, r resp, only string) []string {
	t.Helper()
	var out []string
	b := r.json(t)
	list, _ := b["entry"].([]any)
	for _, e := range list {
		res, _ := e.(map[string]any)["resource"].(map[string]any)
		rt, _ := res["resourceType"].(string)
		id, _ := res["id"].(string)
		if rt != "" && (only == "" || only == rt) {
			out = append(out, rt+"/"+id)
		}
	}
	sort.Strings(out)
	return out
}

func sorted(ids ...string) []string { sort.Strings(ids); return ids }

func TestPatientCompartmentAcrossRealProcesses(t *testing.T) {
	s := newStackFull(t, false, map[string]string{"AUTH_ADMIN_ENABLED": "true", "AUTH_PATIENT_COMPARTMENT_ENABLED": "true"}, map[string]string{"FHIR_UPDATE_AS_CREATE": "true"}, nil)
	f := newPatientFixture(t, s)
	tok := f.tokA

	// đọc trực tiếp
	for path, want := range map[string]int{
		"/Patient/" + f.pa:            200,
		"/Patient/" + f.pb:            404,
		"/Observation/" + f.obsA:      200,
		"/Observation/" + f.obsShared: 200, // performer là A: thuộc compartment của A
		"/Observation/" + f.obsB:      404,
		"/Condition/" + f.condA:       200,
		"/Condition/" + f.condB:       404,
		"/Communication/" + f.commA:   200,
		"/Communication/" + f.comB:    404,
	} {
		if r := s.fhirGet(tok, path); r.status != want {
			t.Errorf("GET %s: %d, muốn %d (%.120s)", path, r.status, want, r.body)
		}
	}
	// loại ngoài compartment (policy mặc định không liệt kê): bị cấm
	if r := s.fhirGet(tok, "/Practitioner"); r.status != 403 {
		t.Errorf("loại ngoài chính sách: %d", r.status)
	}

	// search: kết quả, total và phân trang chỉ tính phần được thấy
	r := s.fhirGet(tok, "/Observation?_total=accurate&_count=1")
	if r.status != 200 || r.json(t)["total"] != float64(2) {
		t.Fatalf("search Observation: %d total=%v (project có 6 Observation, A chỉ thấy 2): %.200s", r.status, r.json(t)["total"], r.body)
	}
	if got := entryIDs(t, s.fhirGet(tok, "/Observation"), "Observation"); !slices.Equal(got, sorted("Observation/"+f.obsA, "Observation/"+f.obsShared)) {
		t.Errorf("search Observation: %v", got)
	}
	if got := entryIDs(t, s.fhirGet(tok, "/Observation?subject=Patient/"+f.pb), "Observation"); !slices.Equal(got, []string{"Observation/" + f.obsShared}) {
		t.Errorf("subject=B chỉ còn resource thuộc cả hai compartment: %v", got)
	}
	// _include, _revinclude không lộ Patient/Observation của B
	inc := s.fhirGet(tok, "/Observation?_include=Observation:subject")
	if got := entryIDs(t, inc, "Patient"); !slices.Equal(got, []string{"Patient/" + f.pa}) {
		t.Errorf("_include chỉ được kèm Patient của A: %v", got)
	}
	rev := s.fhirGet(tok, "/Patient?_revinclude=Observation:subject")
	if got := entryIDs(t, rev, "Patient"); !slices.Equal(got, []string{"Patient/" + f.pa}) {
		t.Errorf("search Patient: %v", got)
	}
	for _, id := range entryIDs(t, rev, "Observation") {
		if id == "Observation/"+f.obsB || id == "Observation/"+f.obsShared {
			t.Errorf("_revinclude lộ %s", id)
		}
	}

	// history
	if r := s.fhirGet(tok, "/Observation/"+f.obsB+"/_history"); r.status != 404 {
		t.Errorf("history của obs B: %d", r.status)
	}
	if r := s.fhirGet(tok, "/Observation/"+f.obsA+"/_history"); r.status != 200 {
		t.Errorf("history của obs A: %d", r.status)
	}

	// $everything
	if r := s.fhirGet(tok, "/Patient/"+f.pb+"/$everything"); r.status != 404 {
		t.Errorf("$everything của B: %d", r.status)
	}
	ev := s.fhirGet(tok, "/Patient/"+f.pa+"/$everything")
	if ev.status != 200 {
		t.Fatalf("$everything của A: %d %s", ev.status, ev.body)
	}
	have := entryIDs(t, ev, "")
	for _, want := range []string{"Patient/" + f.pa, "Observation/" + f.obsA, "Observation/" + f.obsShared, "Condition/" + f.condA, "Communication/" + f.commA} {
		if !slices.Contains(have, want) {
			t.Errorf("$everything của A thiếu %s: %v", want, have)
		}
	}
	for _, leak := range []string{"Patient/" + f.pb, "Observation/" + f.obsB, "Condition/" + f.condB, "Communication/" + f.comB} {
		if slices.Contains(have, leak) {
			t.Errorf("$everything của A lộ %s", leak)
		}
	}

	// batch: từng entry theo cùng quyền
	b := s.fhirPost(tok, "", `{"resourceType":"Bundle","type":"batch","entry":[
		{"request":{"method":"GET","url":"Observation/`+f.obsA+`"}},
		{"request":{"method":"GET","url":"Observation/`+f.obsB+`"}}]}`)
	var statuses []string
	list, _ := b.json(t)["entry"].([]any)
	for _, e := range list {
		resp, _ := e.(map[string]any)["response"].(map[string]any)
		st, _ := resp["status"].(string)
		statuses = append(statuses, strings.Fields(st + " ")[0])
	}
	if b.status != 200 || !slices.Equal(statuses, []string{"200", "404"}) {
		t.Errorf("batch: %d %v", b.status, statuses)
	}

	// ghi: chính sách mặc định của Patient không đặt readonly (trừ RelatedPerson) nên bệnh nhân ghi được TRONG compartment của mình (như chính sách mặc định
	// của Medplum): tạo Observation cho chính mình và sửa hồ sơ Patient của mình; nhưng không đụng được hồ sơ của B.
	own := `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/` + f.pa + `"}}`
	if r := s.fhirPost(tok, "/Observation", own); r.status != 201 {
		t.Errorf("tạo Observation cho chính A: %d %s", r.status, r.body)
	}
	putPatient := func(id string) int {
		return do(t, "PUT", s.fhir+"/fhir/R4/Patient/"+id, tok, "application/fhir+json", strings.NewReader(`{"resourceType":"Patient","id":"`+id+`","name":[{"family":"Sua"}]}`)).status
	}
	if c := putPatient(f.pa); c != 200 {
		t.Errorf("sửa hồ sơ Patient của chính A: %d", c)
	}
	if c := putPatient(f.pb); c != 403 && c != 404 {
		t.Errorf("sửa hồ sơ Patient của B phải bị chặn (403 hoặc 404), được %d", c)
	}
	// không tạo được resource trỏ sang bệnh nhân khác (ngoài compartment của A)
	other := `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/` + f.pb + `"}}`
	if r := s.fhirPost(tok, "/Observation", other); r.status != 403 {
		t.Errorf("tạo Observation cho B bằng token A phải 403, được %d %s", r.status, r.body)
	}
}

// Cờ tắt (mặc định): chính sách mặc định của Patient dùng %patient nên bị cấm cả loại, bệnh nhân không đọc được gì ngoài những loại không dùng tham số.
func TestPatientCompartmentDisabledByDefault(t *testing.T) {
	s := newStackFull(t, false, map[string]string{"AUTH_ADMIN_ENABLED": "true"}, map[string]string{"FHIR_UPDATE_AS_CREATE": "true"}, nil)
	f := newPatientFixture(t, s)
	for _, path := range []string{"/Observation/" + f.obsA, "/Observation", "/Patient/" + f.pa} {
		if r := s.fhirGet(f.tokA, path); r.status != 403 && r.status != 404 {
			t.Errorf("cờ tắt: GET %s phải bị chặn, được %d", path, r.status)
		}
	}
}
