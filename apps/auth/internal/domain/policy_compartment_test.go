package domain

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
)

// P8 M3: giải %patient/%profile và nhiều rule cùng loại khi dịch AccessPolicy sang claim.

func TestResolvePolicyCriteria(t *testing.T) {
	vars := map[string]string{"patient": "Patient/p-1.x", "profile": "Practitioner/dr-9"}
	for in, want := range map[string]string{
		"Observation?_compartment=%patient":   "Observation?_compartment=Patient/p-1.x",
		"Patient?_id=%patient.id":             "Patient?_id=p-1.x",
		"Communication?sender=%patient":       "Communication?sender=Patient/p-1.x",
		"Task?owner=%profile":                 "Task?owner=Practitioner/dr-9",
		"Task?owner=%profile.id&status=ready": "Task?owner=dr-9&status=ready",
	} {
		got, ok := ResolvePolicyCriteria(in, vars)
		if !ok || got != want {
			t.Errorf("%q: got %q ok=%v, want %q", in, got, ok, want)
		}
	}
	// không giải được: biến ngoài danh sách, đường dẫn khác .id, giá trị thiếu hoặc sai định dạng
	for in, v := range map[string]map[string]string{
		"Observation?x=%foo":                    vars,
		"Patient?name=%patient.name":            vars,
		"Patient?_id=%patient.id.x":             vars,
		"Patient?_id=%patient.idx":              vars,
		"Observation?x=%":                       vars,
		"Observation?x=%%":                      vars,
		"Observation?_compartment=%patient":     {},
		"Observation?_compartment=%patient ":    {"patient": ""},
		"Observation?_compartment=%patient  ":   {"patient": "Practitioner/1"}, // sai loại
		"Observation?_compartment=%patient   ":  {"patient": "Patient/"},
		"Observation?_compartment=%patient    ": {"patient": "Patient/" + strings.Repeat("a", 65)},
	} {
		if got, ok := ResolvePolicyCriteria(in, v); ok {
			t.Errorf("%q với %v phải thất bại, được %q", in, v, got)
		}
	}
	// chèn: giá trị có ký tự nới criteria bị từ chối, không bao giờ lọt vào chuỗi criteria
	for _, evil := range []string{"Patient/1&status=x", "Patient/1,Patient/2", "Patient/1|y", "Patient/1 or 1", "Patient/1\n", "Patient/1$x", "Patient/1?x", "Patient/1#", "Patient/1%2C", "Patient/../2", "http://x/Patient/1", "Patient/1/_history/2"} {
		if got, ok := ResolvePolicyCriteria("Observation?_compartment=%patient", map[string]string{"patient": evil}); ok {
			t.Errorf("giá trị %q phải bị từ chối, được %q", evil, got)
		}
	}
}

func resolveOpts(patient string) PolicyOptions {
	return PolicyOptions{Compartment: true, Vars: []map[string]string{{"patient": patient, "profile": "Patient/p1"}}}
}

func TestPolicyClaimWithCompartmentOfDefaultPatientPolicy(t *testing.T) {
	pol, _ := DefaultAccessPolicy("Patient")
	claim, narrowed, err := PolicyClaimWith([]fhir.AccessPolicy{pol}, resolveOpts("Patient/p1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(narrowed) != 0 {
		t.Errorf("chính sách mặc định của Patient phải dịch trọn vẹn, nhưng bị thu hẹp: %v", narrowed)
	}
	byType := map[string][]map[string]any{}
	for _, e := range claim {
		rt := e["resourceType"].(string)
		byType[rt] = append(byType[rt], e)
		if e["deny"] == true {
			t.Errorf("không loại nào bị cấm khi biến đã giải được: %v", e)
		}
	}
	if got := byType["Patient"]; len(got) != 1 || got[0]["criteria"] != "_id=p1" {
		t.Errorf("Patient: %v", got)
	}
	if got := byType["Observation"]; len(got) != 1 || got[0]["criteria"] != "_compartment=Patient/p1" {
		t.Errorf("Observation: %v", got)
	}
	comm := byType["Communication"]
	if len(comm) != 2 || comm[0]["criteria"] != "recipient=Patient/p1" || comm[1]["criteria"] != "sender=Patient/p1" {
		t.Errorf("Communication phải thành hai rule (OR), sắp theo criteria: %v", comm)
	}
	if got := byType["RelatedPerson"]; len(got) != 1 || got[0]["readonly"] != true {
		t.Errorf("RelatedPerson chỉ đọc: %v", got)
	}
	// claim không bao giờ còn dấu %
	raw, _ := json.Marshal(claim)
	if strings.Contains(string(raw), "%") {
		t.Errorf("claim còn tham số chưa giải: %s", raw)
	}
}

// Hợp đồng claim (C-6) được ghim bằng golden: claim của chính sách mặc định Patient khi bệnh nhân là Patient/00000000-0000-4000-8000-000000000001.
// Cập nhật có chủ ý: UPDATE_GOLDEN=1 go test ./apps/auth/internal/domain -run TestPolicyClaimGolden.
func TestPolicyClaimGolden(t *testing.T) {
	pol, _ := DefaultAccessPolicy("Patient")
	claim, _, err := PolicyClaimWith([]fhir.AccessPolicy{pol}, resolveOpts("Patient/00000000-0000-4000-8000-000000000001"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.MarshalIndent(claim, "", "  ")
	got = append(got, '\n')
	const path = "testdata/patient_policy_claim.golden.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("thiếu %s: chạy với UPDATE_GOLDEN=1 để sinh", path)
	}
	if string(want) != string(got) {
		t.Errorf("claim lệch golden %s (nếu thay đổi là chủ ý, đây là thay đổi hợp đồng với fhir-server: cập nhật golden và thứ tự deploy):\n%s", path, got)
	}
}

func TestPolicyClaimWithCompartmentFailsClosed(t *testing.T) {
	pol, _ := DefaultAccessPolicy("Patient")
	deniedAll := func(name string, opts PolicyOptions) {
		t.Helper()
		claim, narrowed, err := PolicyClaimWith([]fhir.AccessPolicy{pol}, opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(narrowed) == 0 {
			t.Errorf("%s: phải ghi nhận chỗ thu hẹp", name)
		}
		for _, e := range claim {
			if e["deny"] != true {
				t.Errorf("%s: mọi loại dùng %%patient phải bị cấm, được %v", name, e)
			}
		}
	}
	deniedAll("không có biến", PolicyOptions{Compartment: true})
	deniedAll("thiếu patient", PolicyOptions{Compartment: true, Vars: []map[string]string{{"profile": "Practitioner/1"}}})
	deniedAll("patient sai loại", resolveOpts("Practitioner/1"))
	deniedAll("patient chèn criteria", resolveOpts("Patient/1&status=final"))
	deniedAll("patient nhiều giá trị", resolveOpts("Patient/1,Patient/2"))
	// cờ tắt: hành vi cũ, kể cả khi đã có biến hợp lệ
	claim, _, err := PolicyClaimWith([]fhir.AccessPolicy{pol}, PolicyOptions{Vars: []map[string]string{{"patient": "Patient/p1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range claim {
		if e["deny"] != true {
			t.Errorf("cờ tắt: loại dùng %%patient phải bị cấm: %v", e)
		}
	}
}

func TestPolicyClaimWithMultipleRules(t *testing.T) {
	yes := true
	opts := PolicyOptions{Compartment: true}
	r := func(rt, c string, ro *bool) fhir.AccessPolicyResource {
		return fhir.AccessPolicyResource{ResourceType: rt, Criteria: c, Readonly: ro}
	}
	// nhiều criteria khác nhau: thành nhiều rule; một entry chỉ đọc kéo cả loại thành chỉ đọc (mọi rule cùng loại phải cùng readonly)
	claim, _, err := PolicyClaimWith([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{
		r("Task", "Task?status=ready", nil), r("Task", "Task?status=draft", &yes), r("Task", "Task?status=ready", nil),
	}}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"resourceType": "Task", "criteria": "status=draft", "readonly": true},
		{"resourceType": "Task", "criteria": "status=ready", "readonly": true},
	}
	if !reflect.DeepEqual(claim, want) {
		t.Errorf("claim = %v, muốn %v", claim, want)
	}
	// có entry toàn loại: giữ toàn loại (như trước)
	claim, _, _ = PolicyClaimWith([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{r("Task", "Task?status=ready", nil), r("Task", "", nil)}}}, opts)
	if len(claim) != 1 || claim[0]["criteria"] != nil {
		t.Errorf("toàn loại phải thắng: %v", claim)
	}
	// quá nhiều criteria: cấm loại
	var many []fhir.AccessPolicyResource
	for i := 0; i < maxPolicyRules+1; i++ {
		many = append(many, r("Task", "Task?_id=x"+strings.Repeat("a", i), nil))
	}
	claim, narrowed, _ := PolicyClaimWith([]fhir.AccessPolicy{{Resource: many}}, opts)
	if len(claim) != 1 || claim[0]["deny"] != true || len(narrowed) == 0 {
		t.Errorf("quá %d criteria phải cấm loại: %v %v", maxPolicyRules, claim, narrowed)
	}
	// cờ tắt: hành vi cũ (cấm)
	claim, _, _ = PolicyClaimWith([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{r("Task", "Task?status=ready", nil), r("Task", "Task?status=draft", nil)}}}, PolicyOptions{})
	if len(claim) != 1 || claim[0]["deny"] != true {
		t.Errorf("cờ tắt: nhiều criteria phải bị cấm: %v", claim)
	}
}

func TestPolicyClaimWithRelatedPersonUsesAccessParameter(t *testing.T) {
	pol, _ := DefaultAccessPolicy("RelatedPerson")
	claim, _, err := PolicyClaimWith([]fhir.AccessPolicy{pol}, PolicyOptions{Compartment: true, Vars: []map[string]string{{"profile": "RelatedPerson/r1", "patient": "Patient/p9"}}})
	if err != nil {
		t.Fatal(err)
	}
	var obs, rp map[string]any
	for _, e := range claim {
		switch e["resourceType"] {
		case "Observation":
			obs = e
		case "RelatedPerson":
			rp = e
		}
	}
	if obs == nil || obs["criteria"] != "_compartment=Patient/p9" {
		t.Errorf("RelatedPerson đọc compartment của Patient được liên kết: %v", obs)
	}
	// entry tự sửa hồ sơ dùng interaction (chưa hỗ trợ): cả loại RelatedPerson bị cấm, an toàn hơn là nới
	if rp == nil || rp["deny"] != true {
		t.Errorf("RelatedPerson có interaction phải bị cấm: %v", rp)
	}
}

// fhir-server nhận criteria CHỈ gồm query: tiền tố "Loại?" của AccessPolicy phải được bỏ (cả khi cờ tắt), và tiền tố sai loại thì cấm loại.
func TestPolicyClaimCriteriaIsQueryOnly(t *testing.T) {
	for name, opts := range map[string]PolicyOptions{"cờ tắt": {}, "cờ bật": {Compartment: true}} {
		claim, _, err := PolicyClaimWith([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{
			{ResourceType: "Task", Criteria: "Task?status=ready"},
			{ResourceType: "Goal", Criteria: "lifecycle-status=active"}, // đã là query thuần
		}}}, opts)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]any{}
		for _, e := range claim {
			got[e["resourceType"].(string)] = e["criteria"]
		}
		if got["Task"] != "status=ready" || got["Goal"] != "lifecycle-status=active" {
			t.Errorf("%s: %v", name, got)
		}
		// tiền tố khác loại của rule: không dịch được, cấm loại (không đoán)
		claim, narrowed, _ := PolicyClaimWith([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{{ResourceType: "Task", Criteria: "Observation?status=final"}}}}, opts)
		if len(claim) != 1 || claim[0]["deny"] != true || len(narrowed) == 0 {
			t.Errorf("%s: tiền tố sai loại phải cấm: %v %v", name, claim, narrowed)
		}
	}
	// tiền tố sai loại sau khi giải biến
	claim, _, _ := PolicyClaimWith([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{{ResourceType: "Task", Criteria: "Observation?_compartment=%patient"}}}}, resolveOpts("Patient/p1"))
	if len(claim) != 1 || claim[0]["deny"] != true {
		t.Errorf("tiền tố sai loại (có biến) phải cấm: %v", claim)
	}
}
