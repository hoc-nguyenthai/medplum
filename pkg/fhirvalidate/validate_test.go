package fhirvalidate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhirvalidate"
)

func read(t *testing.T, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Các ví dụ chính thức của FHIR phải hợp lệ hoàn toàn (không lỗi, không cảnh báo).
func TestOfficialExamplesAreValid(t *testing.T) {
	files, _ := filepath.Glob("../fhir/testdata/*.json")
	files = append(files, "../fhirpath/testdata/observation-example.json", "../fhirpath/testdata/questionnaire-example.json", "../fhirpath/testdata/valueset-example-expansion.json")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			res := fhirvalidate.ValidateJSON(read(t, f), nil)
			warnings := 0
			for _, i := range res.Issues {
				if i.Severity == fhirvalidate.Warning && i.Code == fhirvalidate.CodeInvariant {
					warnings++ // khuyến nghị của đặc tả (dom-6, spd-0, ...), không phải lỗi
					continue
				}
				t.Errorf("%s", i)
			}
			t.Logf("%d cảnh báo khuyến nghị", warnings)
		})
	}
}

// Toàn bộ dữ liệu định nghĩa của Medplum (hàng chục nghìn resource thật) phải hợp lệ.
func TestMedplumDefinitionsAreValid(t *testing.T) {
	if testing.Short() {
		t.Skip("bỏ qua với -short")
	}
	dir := "../../medplum/packages/definitions/src/fhir/r4"
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		t.Skip("không có medplum/")
	}
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, "fhir.schema") || base == "dataelements.json" {
			continue
		}
		t.Run(base, func(t *testing.T) {
			start := time.Now()
			res := fhirvalidate.ValidateJSON(read(t, f), nil)
			shown := 0
			for _, i := range res.Issues {
				if i.Severity == fhirvalidate.Warning && i.Code == fhirvalidate.CodeInvariant {
					continue
				}
				if shown++; shown <= 8 {
					t.Errorf("%s", i)
				}
			}
			if shown > 8 {
				t.Errorf("... và %d vấn đề nữa", shown-8)
			}
			t.Logf("%s: %v", base, time.Since(start).Round(time.Millisecond))
		})
	}
}

func TestInvalidResources(t *testing.T) {
	const patient = `{"resourceType":"Patient","id":"p1",`
	cases := []struct {
		name, json string
		code, path string // vấn đề kỳ vọng
		contains   string
	}{
		{"không có resourceType", `{"id":"1"}`, "structure", "", "resourceType"},
		{"resourceType lạ", `{"resourceType":"Nope"}`, "structure", "", "resourceType"},
		{"thuộc tính lạ", patient + `"bogus":1}`, "structure", "Patient.bogus", ""},
		{"sai kiểu boolean", patient + `"active":"yes"}`, "value", "Patient.active", "boolean"},
		{"ngày sai định dạng", patient + `"birthDate":"1974-13-45"}`, "value", "Patient.birthDate", "date"},
		{"id sai ký tự", `{"resourceType":"Patient","id":"a b"}`, "value", "Patient.id", "id"},
		{"mảng thay vì đơn", patient + `"gender":["male"]}`, "structure", "Patient.gender", "đơn"},
		{"đơn thay vì mảng", patient + `"name":{"family":"A"}}`, "structure", "Patient.name", "mảng"},
		{"mảng rỗng", patient + `"name":[]}`, "structure", "Patient.name", "rỗng"},
		{"chuỗi rỗng", patient + `"gender":""}`, "value", "Patient.gender", "rỗng"},
		{"object rỗng", patient + `"name":[{}]}`, "structure", "Patient.name[0]", "rỗng"},
		{"null", patient + `"gender":null}`, "structure", "Patient.gender", "null"},
		{"hai biến thể choice", patient + `"deceasedBoolean":false,"deceasedDateTime":"2020-01-01"}`, "structure", "Patient.deceased", "một giá trị"},
		{"lỗi lồng sâu", patient + `"name":[{"given":["A",1]}]}`, "value", "Patient.name[0].given[1]", "chuỗi"},
		{"backbone thuộc tính lạ", patient + `"contact":[{"nope":true}]}`, "structure", "Patient.contact[0].nope", ""},
		{"thiếu thuộc tính bắt buộc", `{"resourceType":"Observation","id":"o"}`, "required", "Observation.status", "status"},
		{"thiếu code", `{"resourceType":"Observation","status":"final"}`, "required", "Observation.code", "code"},
		{"integer sai", patient + `"multipleBirthInteger":1.5}`, "value", "Patient.multipleBirthInteger", "integer"},
		{"extension thiếu url", patient + `"extension":[{"valueString":"x"}]}`, "required", "Patient.extension[0].url", "url"},
		{"extension có cả value và extension", patient + `"extension":[{"url":"u","valueString":"x","extension":[{"url":"v"}]}]}`, "invariant", "Patient.extension[0]", "ext-1"},
		{"invariant pat-1", patient + `"contact":[{"gender":"male"}]}`, "invariant", "Patient.contact[0]", "pat-1"},
		{"invariant obs-6", `{"resourceType":"Observation","status":"final","code":{"text":"c"},"valueString":"v","dataAbsentReason":{"text":"x"}}`, "invariant", "Observation", "obs-6"},
		{"dom-2 contained lồng nhau", patient + `"contained":[{"resourceType":"Basic","id":"b","code":{"text":"c"},"contained":[{"resourceType":"Basic","id":"c","code":{"text":"c"}}]}]}`, "invariant", "Patient", "dom-2"},
		{"resource lồng sai kiểu", `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"id":"x"}}]}`, "structure", "Bundle.entry[0].resource", "resourceType"},
		{"phần tử trong bundle sai", `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Patient","active":"no"}}]}`, "value", "Bundle.entry[0].resource.active", "boolean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := fhirvalidate.ValidateJSON([]byte(tc.json), nil)
			if res.OK() {
				t.Fatalf("muốn lỗi, nhưng hợp lệ")
			}
			for _, i := range res.Errors() {
				if i.Code == tc.code && (tc.path == "" || strings.HasSuffix(i.Path, tc.path) || strings.Contains(i.Path, tc.path)) &&
					(tc.contains == "" || strings.Contains(i.Message, tc.contains)) {
					return
				}
			}
			t.Fatalf("không thấy vấn đề %s tại %q chứa %q; có: %v", tc.code, tc.path, tc.contains, res.Issues)
		})
	}
}

func TestValidMinimalResources(t *testing.T) {
	for name, js := range map[string]string{
		"patient rỗng":             `{"resourceType":"Patient"}`,
		"extension nguyên thủy":    `{"resourceType":"Patient","birthDate":"1974-12-25","_birthDate":{"extension":[{"url":"http://x","valueString":"y"}]}}`,
		"extension thay giá trị":   `{"resourceType":"Patient","_birthDate":{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]}}`,
		"số thập phân":             `{"resourceType":"Observation","status":"final","code":{"text":"c"},"valueQuantity":{"value":1.50,"unit":"kg"}}`,
		"contained hợp lệ":         `{"resourceType":"Patient","contained":[{"resourceType":"Organization","id":"o","name":"Org"}],"managingOrganization":{"reference":"#o"}}`,
		"mảng nguyên thủy có null": `{"resourceType":"Patient","name":[{"given":["A",null],"_given":[null,{"id":"e"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			res := fhirvalidate.ValidateJSON([]byte(js), nil)
			for _, i := range res.Errors() {
				t.Errorf("%s", i)
			}
		})
	}
}

func TestSkipConstraints(t *testing.T) {
	js := []byte(`{"resourceType":"Patient","contact":[{"gender":"male"}]}`)
	if fhirvalidate.ValidateJSON(js, nil).OK() {
		t.Fatal("pat-1 phải bị phát hiện")
	}
	if !fhirvalidate.ValidateJSON(js, &fhirvalidate.Options{SkipConstraints: true}).OK() {
		t.Fatal("SkipConstraints phải bỏ qua invariant")
	}
}

func TestOutcome(t *testing.T) {
	ok := fhirvalidate.ValidateJSON([]byte(`{"resourceType":"Patient","text":{"status":"generated","div":"<div xmlns=\"http://www.w3.org/1999/xhtml\">x</div>"}}`), nil).Outcome()
	if len(ok.Issue) != 1 || ok.Issue[0].Severity != "information" {
		t.Fatalf("outcome hợp lệ: %+v", ok.Issue)
	}
	bad := fhirvalidate.ValidateJSON([]byte(`{"resourceType":"Patient","x":1}`), nil).Outcome()
	if len(bad.Issue) != 1 || bad.Issue[0].Severity != "error" || bad.Issue[0].Expression[0] != "Patient.x" {
		t.Fatalf("outcome lỗi: %+v", bad.Issue)
	}
}
