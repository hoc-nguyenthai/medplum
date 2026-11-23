package fhirpath_test

import (
	"os"
	"slices"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
)

// Mọi biểu thức của search parameter (R4, Medplum, US Core) phải biên dịch được.
func TestAllSearchParameterExpressionsCompile(t *testing.T) {
	n := 0
	seen := map[string]bool{}
	for _, rt := range fhir.ResourceTypes {
		for _, sp := range fhir.SearchParams(rt) {
			for _, e := range []string{sp.Expression} {
				if e == "" || seen[e] {
					continue
				}
				seen[e] = true
				n++
				if _, err := fhirpath.Compile(e); err != nil {
					t.Errorf("%s (%s): %v\n  %s", sp.Code, sp.URL, err, e)
				}
			}
			for _, c := range sp.Component {
				if c.Expression != "" && !seen[c.Expression] {
					seen[c.Expression] = true
					if _, err := fhirpath.Compile(c.Expression); err != nil {
						t.Errorf("%s component: %v\n  %s", sp.Code, err, c.Expression)
					}
				}
			}
		}
	}
	if n < 1000 {
		t.Fatalf("chỉ có %d biểu thức, nghi ngờ bảng search parameter thiếu", n)
	}
	t.Logf("đã biên dịch %d biểu thức khác nhau", n)
}

func decode(t *testing.T, file string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	v, err := fhirpath.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

// Chạy mọi search parameter áp dụng cho resource ví dụ: không được lỗi; ghi lại số tham số có giá trị.
func TestSearchParameterExpressionsEvaluate(t *testing.T) {
	for _, file := range []string{"testdata/patient-example.json", "testdata/observation-example.json", "testdata/questionnaire-example.json"} {
		res := decode(t, file)
		rt := res["resourceType"].(string)
		withValue := 0
		defs := fhir.SearchParams(rt)
		for _, sp := range defs {
			if sp.Expression == "" {
				continue
			}
			out, err := fhirpath.Evaluate(sp.Expression, res, nil)
			if err != nil {
				t.Errorf("%s %s: %v\n  %s", rt, sp.Code, err, sp.Expression)
				continue
			}
			if len(out) > 0 {
				withValue++
			}
		}
		if withValue == 0 {
			t.Errorf("%s: không search parameter nào cho giá trị", rt)
		}
		t.Logf("%s: %d/%d search parameter có giá trị", rt, withValue, len(defs))
	}
}

// Kiểm tra giá trị cụ thể của một số search parameter tiêu biểu trên Patient và Observation.
func TestSearchParameterValues(t *testing.T) {
	patient := decode(t, "testdata/patient-example.json")
	obs := decode(t, "testdata/observation-example.json")

	get := func(res map[string]any, rt, code string) []fhirpath.Value {
		t.Helper()
		idx := slices.IndexFunc(fhir.SearchParams(rt), func(sp fhir.SearchParamDef) bool {
			return sp.Code == code && slices.Contains(sp.Base, rt)
		})
		if idx < 0 {
			t.Fatalf("không có search parameter %s.%s", rt, code)
		}
		out, err := fhirpath.Evaluate(fhir.SearchParams(rt)[idx].Expression, res, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if bd := get(patient, "Patient", "birthdate"); len(bd) != 1 || bd[0].V != "1974-12-25" {
		t.Errorf("Patient.birthdate = %v", bd)
	}
	if fam := get(patient, "Patient", "family"); len(fam) == 0 {
		t.Errorf("Patient.family rỗng")
	}
	if g := get(patient, "Patient", "gender"); len(g) != 1 || g[0].V != "male" {
		t.Errorf("Patient.gender = %v", g)
	}
	// choice type: Observation.value[x] -> value-quantity chỉ có khi giá trị là Quantity
	if q := get(obs, "Observation", "value-quantity"); len(q) != 1 {
		t.Errorf("Observation.value-quantity = %v", q)
	}
	if s := get(obs, "Observation", "value-string"); len(s) != 0 {
		t.Errorf("Observation.value-string phải rỗng, có %v", s)
	}
	if sub := get(obs, "Observation", "subject"); len(sub) != 1 {
		t.Errorf("Observation.subject = %v", sub)
	}
	// where(resolve() is Patient): dùng resolve() rút gọn từ chuỗi tham chiếu
	if p := get(obs, "Observation", "patient"); len(p) != 1 {
		t.Errorf("Observation.patient = %v", p)
	}
}
