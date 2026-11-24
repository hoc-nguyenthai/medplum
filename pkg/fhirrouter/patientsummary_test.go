package fhirrouter_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPatientSummaryBuildsIPSDocument(t *testing.T) {
	_, h := clinicalEnv(t)
	put := func(path, body string) { mustPut(t, h, path, body) }
	put("Organization/org1", `{"resourceType":"Organization","id":"org1","name":"Bệnh viện A"}`)
	put("Practitioner/dr1", `{"resourceType":"Practitioner","id":"dr1","name":[{"family":"Trần","given":["Bác"]}]}`)
	put("Patient/p1", `{"resourceType":"Patient","id":"p1","name":[{"family":"Nguyễn","given":["An"]}],"managingOrganization":{"reference":"Organization/org1"}}`)
	put("Patient/p2", `{"resourceType":"Patient","id":"p2","name":[{"family":"Khác"}]}`)
	subj := `"subject":{"reference":"Patient/p1"}`
	put("AllergyIntolerance/a1", `{"resourceType":"AllergyIntolerance","id":"a1","patient":{"reference":"Patient/p1"},"code":{"text":"Penicillin <b>"},"clinicalStatus":{"text":"active"},"reaction":[{"manifestation":[{"text":"Nổi mề đay"}],"severity":"severe"}]}`)
	put("Condition/c1", `{"resourceType":"Condition","id":"c1",`+subj+`,"code":{"text":"Tăng huyết áp"},"onsetDateTime":"2020-05-06T00:00:00Z","clinicalStatus":{"text":"active"}}`)
	put("Condition/c2", `{"resourceType":"Condition","id":"c2",`+subj+`,"code":{"text":"Lo âu"},"category":[{"coding":[{"system":"http://loinc.org","code":"75310-3"}]}]}`)
	put("Observation/o-vital", `{"resourceType":"Observation","id":"o-vital","status":"final",`+subj+`,"code":{"text":"Mạch"},"category":[{"coding":[{"system":"http://terminology.hl7.org/CodeSystem/observation-category","code":"vital-signs"}]}],"valueQuantity":{"value":72,"unit":"bpm"},"effectiveDateTime":"2024-01-02T10:00:00Z"}`)
	put("Observation/o-social", `{"resourceType":"Observation","id":"o-social","status":"final",`+subj+`,"code":{"text":"Hút thuốc"},"category":[{"coding":[{"system":"http://terminology.hl7.org/CodeSystem/observation-category","code":"social-history"}]}],"valueString":"không"}`)
	put("Observation/o-lab-child", `{"resourceType":"Observation","id":"o-lab-child","status":"final",`+subj+`,"code":{"text":"Glucose"},"valueQuantity":{"value":5.5,"unit":"mmol/L"}}`)
	put("DiagnosticReport/dr-lab", `{"resourceType":"DiagnosticReport","id":"dr-lab","status":"final",`+subj+`,"code":{"text":"Xét nghiệm máu"},"effectiveDateTime":"2024-02-03T00:00:00Z","result":[{"reference":"Observation/o-lab-child"}]}`)
	put("Observation/o-lab-solo", `{"resourceType":"Observation","id":"o-lab-solo","status":"final",`+subj+`,"code":{"text":"CRP"},"valueQuantity":{"value":3,"unit":"mg/L"}}`)
	put("Immunization/i1", `{"resourceType":"Immunization","id":"i1","status":"completed","patient":{"reference":"Patient/p1"},"vaccineCode":{"text":"BCG"},"occurrenceDateTime":"2000-01-01"}`)
	put("MedicationRequest/m1", `{"resourceType":"MedicationRequest","id":"m1","status":"active","intent":"order",`+subj+`,"medicationCodeableConcept":{"text":"Amlodipine"},"dosageInstruction":[{"text":"5mg mỗi sáng"}]}`)
	put("ServiceRequest/sr-ref", `{"resourceType":"ServiceRequest","id":"sr-ref","status":"active","intent":"order",`+subj+`,"code":{"coding":[{"code":"310449005"}],"text":"Chuyển viện"}}`)
	put("ServiceRequest/sr-plan", `{"resourceType":"ServiceRequest","id":"sr-plan","status":"active","intent":"order",`+subj+`,"code":{"text":"Siêu âm"}}`)
	put("Goal/g1", `{"resourceType":"Goal","id":"g1","lifecycleStatus":"active",`+subj+`,"description":{"text":"Giảm cân"},"startDate":"2024-01-01"}`)
	put("ClinicalImpression/ci-note", `{"resourceType":"ClinicalImpression","id":"ci-note","status":"completed","subject":{"reference":"Patient/p1"},"code":{"coding":[{"code":"34109-9"}]},"summary":"Ghi chú lâm sàng","date":"2024-03-04T00:00:00Z"}`)
	put("ClinicalImpression/ci-assess", `{"resourceType":"ClinicalImpression","id":"ci-assess","status":"completed","subject":{"reference":"Patient/p1"},"summary":"Đánh giá chung","date":"2024-03-05T00:00:00Z"}`)
	put("Observation/o-func", `{"resourceType":"Observation","id":"o-func","status":"final",`+subj+`,"code":{"coding":[{"code":"d5"}],"text":"Tự chăm sóc"},"category":[{"coding":[{"system":"http://terminology.hl7.org/CodeSystem/observation-category","code":"survey"}]}],"valueString":"khó khăn"}`)
	put("Observation/o-survey", `{"resourceType":"Observation","id":"o-survey","status":"final",`+subj+`,"code":{"text":"Khảo sát chung"},"category":[{"coding":[{"system":"http://terminology.hl7.org/CodeSystem/observation-category","code":"survey"}]}],"valueString":"ổn"}`)
	put("Communication/co1", `{"resourceType":"Communication","id":"co1","status":"completed","subject":{"reference":"Patient/p1"}}`)
	put("Observation/o-other-patient", `{"resourceType":"Observation","id":"o-other-patient","status":"final","subject":{"reference":"Patient/p2"},"code":{"text":"Khác"},"valueString":"x"}`)

	code, out := putTo(h, "GET", "Patient/p1/$summary?author=Practitioner/dr1&authoredOn=2024-06-01T00:00:00Z", "")
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	var b struct {
		Type  string
		Entry []struct{ Resource map[string]any }
	}
	_ = json.Unmarshal([]byte(out), &b)
	if b.Type != "document" || len(b.Entry) < 4 {
		t.Fatalf("Bundle: %s %d entry", b.Type, len(b.Entry))
	}
	comp := b.Entry[0].Resource
	if comp["resourceType"] != "Composition" || comp["status"] != "final" || comp["date"] != "2024-06-01T00:00:00Z" || comp["title"] != "Medical Summary" ||
		comp["subject"].(map[string]any)["reference"] != "Patient/p1" || comp["author"].([]any)[0].(map[string]any)["reference"] != "Practitioner/dr1" ||
		comp["custodian"].(map[string]any)["reference"] != "Organization/org1" {
		t.Errorf("Composition: %v", comp)
	}
	if typ := comp["type"].(map[string]any)["coding"].([]any)[0].(map[string]any); typ["code"] != "60591-5" {
		t.Errorf("type: %v", typ)
	}
	if b.Entry[1].Resource["resourceType"] != "Patient" || b.Entry[2].Resource["resourceType"] != "Practitioner" {
		t.Errorf("thứ tự entry: %v %v", b.Entry[1].Resource["resourceType"], b.Entry[2].Resource["resourceType"])
	}
	sections := map[string]map[string]any{}
	for _, s := range comp["section"].([]any) {
		sm := s.(map[string]any)
		sections[sm["title"].(string)] = sm
	}
	refsOf := func(title string) []string {
		var out []string
		for _, e := range sections[title]["entry"].([]any) {
			out = append(out, e.(map[string]any)["reference"].(string))
		}
		return out
	}
	has := func(title, ref string) bool {
		for _, r := range refsOf(title) {
			if r == ref {
				return true
			}
		}
		return false
	}
	divOf := func(title string) string { return sections[title]["text"].(map[string]any)["div"].(string) }
	for title, ref := range map[string]string{
		"Allergies": "AllergyIntolerance/a1", "Immunizations": "Immunization/i1", "Medications": "MedicationRequest/m1", "Problem List": "Condition/c1",
		"Health Concerns": "Condition/c2", "Vital Signs": "Observation/o-vital", "Social History": "Observation/o-social", "Results": "DiagnosticReport/dr-lab",
		"Plan of Treatment": "ServiceRequest/sr-plan", "Reason for Referral": "ServiceRequest/sr-ref", "Goals": "Goal/g1",
	} {
		if sections[title] == nil || !has(title, ref) {
			t.Errorf("mục %q phải chứa %s (có: %v)", title, ref, refsOf(title))
		}
	}
	if has("Results", "Observation/o-lab-child") || !has("Results", "Observation/o-lab-solo") {
		t.Errorf("Observation lồng trong DiagnosticReport không đứng riêng, Observation lẻ thì có: %v", refsOf("Results"))
	}
	if strings.Contains(out, "o-other-patient") || strings.Contains(out, `"p2"`) && strings.Contains(out, "Khác") {
		t.Errorf("không được lẫn dữ liệu của bệnh nhân khác")
	}
	// bảng HTML: thoát ký tự, ngày YYYY-MM-DD, kết quả lồng hiện ở bảng của cha, giá trị quantity
	if d := divOf("Allergies"); !strings.Contains(d, "Penicillin &lt;b&gt;") || !strings.Contains(d, "Nổi mề đay") || !strings.Contains(d, "severe") {
		t.Errorf("bảng dị ứng: %s", d)
	}
	if d := divOf("Results"); !strings.Contains(d, "Xét nghiệm máu") || !strings.Contains(d, "Glucose") || !strings.Contains(d, "5.5 mmol/L") || !strings.Contains(d, "2024-02-03") {
		t.Errorf("bảng kết quả: %s", d)
	}
	if d := divOf("Vital Signs"); !strings.Contains(d, "72 bpm") || !strings.Contains(d, "2024-01-02") {
		t.Errorf("bảng sinh hiệu: %s", d)
	}
	if d := divOf("Medications"); !strings.Contains(d, "Amlodipine") || !strings.Contains(d, "5mg mỗi sáng") {
		t.Errorf("bảng thuốc: %s", d)
	}
	if !has("Notes", "ClinicalImpression/ci-note") || !has("Assessments", "ClinicalImpression/ci-assess") || has("Assessments", "ClinicalImpression/ci-note") {
		t.Errorf("ClinicalImpression: Notes=%v Assessments=%v", refsOf("Notes"), refsOf("Assessments"))
	}
	if sections["Functional Status"] == nil || !has("Functional Status", "Observation/o-func") || has("Social History", "Observation/o-func") || !has("Social History", "Observation/o-survey") {
		t.Errorf("survey: Functional=%v Social=%v", sections["Functional Status"] != nil, refsOf("Social History"))
	}
	if strings.Contains(out, "Communication") || strings.Contains(out, `"timestamp":""`) || !strings.Contains(out, `"timestamp":"`) {
		t.Errorf("loại ngoài 16 loại của IPS không được vào Bundle, Bundle phải có timestamp")
	}
	if !strings.Contains(divOf("Vital Signs"), "<td>2024-01-02</td>") || !strings.Contains(divOf("Notes"), "<td>2024-03-04</td>") {
		t.Errorf("ngày phải rút về YYYY-MM-DD: %s", divOf("Vital Signs"))
	}
	// mục bắt buộc luôn có kể cả khi rỗng; mục tùy chọn rỗng thì vắng
	if sections["Devices"] == nil || sections["Encounters"] == nil || sections["Procedures"] == nil || sections["Assessments"] == nil {
		t.Errorf("mục bắt buộc phải có: %v", keys(sections))
	}
	if sections["Insurance"] != nil {
		t.Errorf("mục tùy chọn rỗng phải vắng: %v", keys(sections))
	}
	if d := divOf("Devices"); d != `<div xmlns="http://www.w3.org/1999/xhtml"></div>` {
		t.Errorf("mục rỗng: %q", d)
	}
	if ev := comp["event"]; ev == nil {
		t.Errorf("thiếu event.period")
	}
	// lỗi
	for path, want := range map[string]int{
		"Patient/p1/$summary":                                        400, // thiếu author
		"Patient/p1/$summary?author=Patient/p2":                      400, // loại author sai
		"Patient/p1/$summary?author=Practitioner/khong-co":           404,
		"Patient/khong-co/$summary?author=Practitioner/dr1":          404,
		"Patient/p1/$summary?author=Practitioner/dr1&_since=hôm-qua": 400,
	} {
		if c, o := putTo(h, "GET", path, ""); c != want {
			t.Errorf("%s: %d, muốn %d (%.150s)", path, c, want, o)
		}
	}
	if c, o := putTo(h, "POST", "Patient/p1/$summary", `{"resourceType":"Parameters","parameter":[{"name":"author","valueReference":{"reference":"Organization/org1"}}]}`); c != 200 || !strings.Contains(o, `"Organization/org1"`) {
		t.Errorf("author qua Parameters valueReference: %d %.120s", c, o)
	}
}

func keys(m map[string]map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
