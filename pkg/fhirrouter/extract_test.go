package fhirrouter_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	xTemplate = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-templateExtract"
	xAlloc    = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-extractAllocateId"
	xContext  = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-templateExtractContext"
	xValue    = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-templateExtractValue"
)

// Questionnaire mẫu theo ví dụ của đặc tả SDC: một Observation cho mỗi trả lời của item "bp" (huyết áp, có thể lặp), và một Patient cập nhật từ gốc.
const extractQuestionnaire = `{"resourceType":"Questionnaire","id":"q1","url":"http://example.org/Questionnaire/vitals","status":"active",
 "extension":[
  {"url":"` + xTemplate + `","extension":[{"url":"template","valueReference":{"reference":"#patient"}},{"url":"resourceId","valueString":"%patId.substring(9)"},{"url":"ifNoneExist","valueString":"'identifier=urn:oid:1|' + item.where(linkId='mrn').answer.value"}]},
  {"url":"` + xAlloc + `","valueString":"patId"}],
 "contained":[
  {"resourceType":"Patient","id":"patient","identifier":[{"system":"urn:oid:1","value":"x","_value":{"extension":[{"url":"` + xValue + `","valueString":"item.where(linkId='mrn').answer.value"}]}}],
   "gender":"unknown","_gender":{"extension":[{"url":"` + xValue + `","valueString":"item.where(linkId='gender').answer.value.code"}]},
   "birthDate":"1900-01-01","_birthDate":{"extension":[{"url":"` + xValue + `","valueString":"item.where(linkId='dob').answer.value"}]}},
  {"resourceType":"Observation","id":"bp","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"85354-9"}]},
   "subject":{"reference":"urn:uuid:placeholder","_reference":{"extension":[{"url":"` + xValue + `","valueString":"%patId"}]}},
   "valueQuantity":{"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]","value":0,"_value":{"extension":[{"url":"` + xValue + `","valueString":"answer.value.value"}]}},
   "note":[{"text":"x","extension":[{"url":"` + xContext + `","valueExpression":{"language":"text/fhirpath","expression":"item.where(linkId='note').answer","name":"n"}}],"_text":{"extension":[{"url":"` + xValue + `","valueString":"value"}]}}],
   "component":[{"code":{"text":"phần tử mẫu"},"extension":[{"url":"` + xContext + `","valueString":"item.where(linkId='extra')"}]}]}],
 "item":[
  {"linkId":"mrn","type":"string"},{"linkId":"gender","type":"choice"},{"linkId":"dob","type":"date"},
  {"linkId":"bp","type":"quantity","repeats":true,"extension":[{"url":"` + xTemplate + `","extension":[{"url":"template","valueReference":{"reference":"#bp"}},{"url":"fullUrl","valueString":"%patId"}]}]}]}`

const extractResponse = `{"resourceType":"QuestionnaireResponse","id":"r1","status":"completed","questionnaire":"http://example.org/Questionnaire/vitals",
 "item":[
  {"linkId":"mrn","answer":[{"valueString":"MRN-42"}]},
  {"linkId":"gender","answer":[{"valueCoding":{"system":"http://hl7.org/fhir/administrative-gender","code":"female"}}]},
  {"linkId":"dob","answer":[{"valueDate":"1991-03-04"}]},
  {"linkId":"bp","answer":[{"valueQuantity":{"value":120,"unit":"mmHg"}}]},
  {"linkId":"bp","answer":[{"valueQuantity":{"value":135,"unit":"mmHg"}}]}]}`

func extractBundle(t *testing.T, out string) (entries []map[string]any) {
	t.Helper()
	var b struct {
		Type  string
		Entry []map[string]any
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil || b.Type != "transaction" {
		t.Fatalf("không phải Bundle transaction: %v %s", err, out)
	}
	return b.Entry
}

func TestExtractTemplateToTransactionBundle(t *testing.T) {
	_, h := clinicalEnv(t)
	mustPut(t, h, "Questionnaire/q1", extractQuestionnaire)
	mustPut(t, h, "QuestionnaireResponse/r1", extractResponse)
	code, out := putTo(h, "POST", "QuestionnaireResponse/r1/$extract", "")
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	entries := extractBundle(t, out)
	if len(entries) != 3 {
		t.Fatalf("%d entry, muốn 3 (1 Patient từ gốc + 2 Observation theo hai trả lời bp):\n%s", len(entries), out)
	}
	pat := entries[0]["resource"].(map[string]any)
	req := entries[0]["request"].(map[string]any)
	patID := pat["id"].(string)
	if pat["resourceType"] != "Patient" || pat["gender"] != "female" || pat["birthDate"] != "1991-03-04" || len(patID) < 30 {
		t.Errorf("Patient: %v", pat)
	}
	if id0 := pat["identifier"].([]any)[0].(map[string]any); id0["value"] != "MRN-42" {
		t.Errorf("identifier: %v", id0)
	}
	if !strings.HasPrefix(entries[0]["fullUrl"].(string), "urn:uuid:") || entries[1]["fullUrl"] == entries[2]["fullUrl"] && false {
		t.Errorf("fullUrl mặc định của Patient: %v", entries[0]["fullUrl"])
	}
	if req["method"] != "PUT" || req["url"] != "Patient/"+patID || req["ifNoneExist"] != "identifier=urn:oid:1|MRN-42" {
		t.Errorf("request của Patient: %v", req)
	}
	if strings.Contains(out, "templateExtract") || strings.Contains(out, "sdc-questionnaire") || strings.Contains(out, `"_gender"`) || strings.Contains(out, `"_value"`) {
		t.Errorf("extension SDC còn sót trong đầu ra: %s", out)
	}
	// hai Observation, giá trị lấy theo từng trả lời, tham chiếu urn:uuid đã cấp cho bệnh nhân
	vals := map[float64]bool{}
	for _, e := range entries[1:] {
		ob := e["resource"].(map[string]any)
		q := ob["valueQuantity"].(map[string]any)
		vals[q["value"].(float64)] = true
		if ob["subject"].(map[string]any)["reference"] != "urn:uuid:"+patID {
			t.Errorf("subject: %v (muốn urn:uuid:%s)", ob["subject"], patID)
		}
		if e["request"].(map[string]any)["method"] != "POST" || e["request"].(map[string]any)["url"] != "Observation" || e["fullUrl"] != "urn:uuid:"+patID {
			t.Errorf("request/fullUrl của Observation: %v", e)
		}
		// note có context mà không có trả lời "note" nên phần tử bị xóa; component có context rỗng nên cũng bị xóa
		if _, has := ob["note"]; has {
			t.Errorf("note phải bị xóa khi không có trả lời: %v", ob["note"])
		}
		if _, has := ob["component"]; has {
			t.Errorf("component phải bị xóa khi context rỗng: %v", ob["component"])
		}
	}
	if !vals[120] || !vals[135] {
		t.Errorf("giá trị: %v", vals)
	}
}

func TestExtractRepeatsArrayElementsPerContextResult(t *testing.T) {
	_, h := clinicalEnv(t)
	q := `{"resourceType":"Questionnaire","id":"q2","url":"http://example.org/Questionnaire/notes","status":"active",
	 "extension":[{"url":"` + xTemplate + `","extension":[{"url":"template","valueReference":{"reference":"#o"}}]}],
	 "contained":[{"resourceType":"Observation","id":"o","status":"final","code":{"text":"ghi chú"},
	  "note":[{"text":"mẫu","extension":[{"url":"` + xContext + `","valueString":"item.where(linkId='n').answer"}],"_text":{"extension":[{"url":"` + xValue + `","valueString":"value"}]}}]}],
	 "item":[{"linkId":"n","type":"string","repeats":true}]}`
	mustPut(t, h, "Questionnaire/q2", q)
	resp := `{"resourceType":"QuestionnaireResponse","status":"completed","questionnaire":"http://example.org/Questionnaire/notes","item":[{"linkId":"n","answer":[{"valueString":"một"},{"valueString":"hai"},{"valueString":"ba"}]}]}`
	code, out := putTo(h, "POST", "QuestionnaireResponse/$extract", `{"resourceType":"Parameters","parameter":[{"name":"questionnaire-response","resource":`+resp+`}]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	e := extractBundle(t, out)
	if len(e) != 1 {
		t.Fatalf("%d entry", len(e))
	}
	notes := e[0]["resource"].(map[string]any)["note"].([]any)
	var texts []string
	for _, n := range notes {
		texts = append(texts, n.(map[string]any)["text"].(string))
	}
	if strings.Join(texts, ",") != "một,hai,ba" {
		t.Errorf("phần tử note lặp theo từng trả lời: %v", texts)
	}
	// questionnaire truyền thẳng trong tham số cũng chạy được (không cần có trong kho)
	_, h2 := clinicalEnv(t)
	code, out = putTo(h2, "POST", "QuestionnaireResponse/$extract", `{"resourceType":"Parameters","parameter":[{"name":"questionnaire-response","resource":`+resp+`},{"name":"questionnaire","resource":`+q+`}]}`)
	if code != 200 || len(extractBundle(t, out)) != 1 {
		t.Errorf("questionnaire trong tham số: %d %s", code, out)
	}
}

func TestExtractErrors(t *testing.T) {
	_, h := clinicalEnv(t)
	wrap := func(resp, q string) string {
		s := `{"resourceType":"Parameters","parameter":[{"name":"questionnaire-response","resource":` + resp + `}`
		if q != "" {
			s += `,{"name":"questionnaire","resource":` + q + `}`
		}
		return s + `]}`
	}
	base := `{"resourceType":"QuestionnaireResponse","status":"completed","questionnaire":"http://x/q","item":[]}`
	tmplQ := func(ext string) string {
		return `{"resourceType":"Questionnaire","status":"active","extension":[` + ext + `],"contained":[{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},
		"valueString":"a","_valueString":{"extension":[{"url":"` + xValue + `","valueString":"item.answer.value"}]}}],"item":[]}`
	}
	for name, c := range map[string]struct{ body, want string }{
		"thiếu response":                   {`{"resourceType":"Parameters","parameter":[]}`, "must be specified"},
		"questionnaire không có":           {wrap(base, ""), "not found"},
		"response không nêu questionnaire": {wrap(`{"resourceType":"QuestionnaireResponse","status":"completed"}`, ""), "must be specified"},
		"mẫu không có":                     {wrap(base, tmplQ(`{"url":"`+xTemplate+`","extension":[{"url":"template","valueReference":{"reference":"#khong-co"}}]}`)), "Missing template resource #khong-co"},
		"FHIRPath hỏng":                    {wrap(`{"resourceType":"QuestionnaireResponse","status":"completed","item":[]}`, `{"resourceType":"Questionnaire","status":"active","extension":[{"url":"`+xTemplate+`","extension":[{"url":"template","valueReference":{"reference":"#o"}}]}],"contained":[{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"valueString":"a","_valueString":{"extension":[{"url":"`+xValue+`","valueString":"((("}]}}],"item":[]}`), "FHIRPath"},
		"nhiều giá trị ở thuộc tính đơn":   {wrap(`{"resourceType":"QuestionnaireResponse","status":"completed","item":[{"linkId":"a","answer":[{"valueString":"1"},{"valueString":"2"}]}]}`, tmplQ(`{"url":"`+xTemplate+`","extension":[{"url":"template","valueReference":{"reference":"#o"}}]}`)), "array not allowed"},
		"context ngôn ngữ lạ":              {wrap(`{"resourceType":"QuestionnaireResponse","status":"completed","item":[]}`, `{"resourceType":"Questionnaire","status":"active","extension":[{"url":"`+xTemplate+`","extension":[{"url":"template","valueReference":{"reference":"#o"}}]}],"contained":[{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"component":[{"code":{"text":"x"},"extension":[{"url":"`+xContext+`","valueExpression":{"language":"application/x-fhir-query","expression":"Patient?name=a"}}]}]}],"item":[]}`), "unsupported language"},
	} {
		code, out := putTo(h, "POST", "QuestionnaireResponse/$extract", c.body)
		if code != 400 || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d %s (muốn 400 chứa %q)", name, code, out, c.want)
		}
	}
	if code, _ := putTo(h, "POST", "QuestionnaireResponse/khong-co/$extract", ""); code != 404 {
		t.Errorf("response không có: %d", code)
	}
}

// Context trên thuộc tính đối tượng (không phải phần tử mảng) dùng kết quả đầu làm ngữ cảnh của phần tử con; thuộc tính mảng nguyên thủy được thay bằng mọi kết quả.
func TestExtractObjectContextAndPrimitiveArrays(t *testing.T) {
	_, h := clinicalEnv(t)
	q := `{"resourceType":"Questionnaire","id":"q3","url":"http://example.org/Questionnaire/p","status":"active",
	 "extension":[{"url":"` + xTemplate + `","extension":[{"url":"template","valueReference":{"reference":"#pt"}}]}],
	 "contained":[
	  {"resourceType":"Patient","id":"pt","name":[{"family":"x","_family":{"extension":[{"url":"` + xValue + `","valueString":"item.where(linkId='fam').answer.value"}]},
	    "given":["x"],"_given":[{"extension":[{"url":"` + xValue + `","valueString":"item.where(linkId='giv').answer.value"}]}]}],
	   "maritalStatus":{"text":"x","extension":[{"url":"` + xContext + `","valueString":"item.where(linkId='ms').answer.value"}],"_text":{"extension":[{"url":"` + xValue + `","valueString":"display"}]}},
	   "managingOrganization":{"extension":[{"url":"` + xContext + `","valueString":"item.where(linkId='none').answer.value"}],"reference":"Organization/x"}}],
	 "item":[{"linkId":"fam","type":"string"},{"linkId":"giv","type":"string","repeats":true},{"linkId":"ms","type":"coding"}]}`
	mustPut(t, h, "Questionnaire/q3", q)
	resp := `{"resourceType":"QuestionnaireResponse","status":"completed","questionnaire":"http://example.org/Questionnaire/p","item":[
	 {"linkId":"fam","answer":[{"valueString":"Phạm"}]},{"linkId":"giv","answer":[{"valueString":"Thị"},{"valueString":"Lan"}]},{"linkId":"ms","answer":[{"valueCoding":{"display":"Đã kết hôn"}}]}]}`
	code, out := putTo(h, "POST", "QuestionnaireResponse/$extract", `{"resourceType":"Parameters","parameter":[{"name":"questionnaire-response","resource":`+resp+`}]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	pt := extractBundle(t, out)[0]["resource"].(map[string]any)
	name := pt["name"].([]any)[0].(map[string]any)
	given := name["given"].([]any)
	if name["family"] != "Phạm" || len(given) != 2 || given[0] != "Thị" || given[1] != "Lan" {
		t.Errorf("name: %v", name)
	}
	if ms := pt["maritalStatus"].(map[string]any); ms["text"] != "Đã kết hôn" {
		t.Errorf("context đối tượng: %v", ms)
	}
	if _, has := pt["managingOrganization"]; has {
		t.Errorf("context đối tượng rỗng phải xóa thuộc tính: %v", pt["managingOrganization"])
	}
	if strings.Contains(out, "sdc-questionnaire") || strings.Contains(out, `"_given"`) {
		t.Errorf("extension SDC còn sót: %s", out)
	}
}

func TestExtractCapsTheNumberOfEntries(t *testing.T) {
	_, h := clinicalEnv(t)
	q := `{"resourceType":"Questionnaire","id":"q4","url":"http://example.org/Questionnaire/many","status":"active",
	 "contained":[{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"}}],
	 "item":[{"linkId":"i","type":"string","repeats":true,"extension":[{"url":"` + xTemplate + `","extension":[{"url":"template","valueReference":{"reference":"#o"}}]}]}]}`
	mustPut(t, h, "Questionnaire/q4", q)
	var items []string
	for i := 0; i < 1001; i++ {
		items = append(items, `{"linkId":"i","answer":[{"valueString":"a"}]}`)
	}
	resp := `{"resourceType":"QuestionnaireResponse","status":"completed","questionnaire":"http://example.org/Questionnaire/many","item":[` + strings.Join(items, ",") + `]}`
	if code, out := putTo(h, "POST", "QuestionnaireResponse/$extract", `{"resourceType":"Parameters","parameter":[{"name":"questionnaire-response","resource":`+resp+`}]}`); code != 400 || !strings.Contains(out, "Quá nhiều") {
		t.Errorf("vượt trần: %d %.200s", code, out)
	}
}
