package fhirauth_test

import (
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func mk(t *testing.T, raw string) fhir.Resource {
	t.Helper()
	r, err := fhir.UnmarshalResource([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Ghi (update/patch) không được làm mất trường ẩn hoặc chỉ-đọc ở mọi độ sâu: người gọi không thấy chúng nên không thể gửi lại.

func TestUpdateKeepsNestedHiddenAndReadonlyFields(t *testing.T) {
	e := newFREnv(t)
	pol := policyOf(t, map[string]any{"resourceType": "Patient", "hiddenFields": []any{"name.family", "deceased[x]"}, "readonlyFields": []any{"name.use", "telecom.system"}})
	ctx := e.as(pol)
	e.put(mk(t, `{"resourceType":"Patient","id":"n1","deceasedBoolean":true,
	  "name":[{"use":"official","family":"Nguyễn","given":["An"]},{"use":"nickname","family":"Khác","given":["Bé"]}],
	  "telecom":[{"system":"phone","value":"0901"},{"system":"email","value":"a@x.vn"}]}`))
	cur, err := e.repo.ReadResource(ctx, "Patient", "n1")
	if err != nil {
		t.Fatal(err)
	}
	p := cur.(*fhir.Patient)
	p.Name[0].Given = []string{"Bình"} // sửa phần nhìn thấy
	p.Name[1].Use = "old"              // thử đổi trường chỉ-đọc lồng
	p.Telecom[0].System = "fax"
	if _, err := e.repo.UpdateResource(ctx, p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ := e.repo.ReadResource(e.admin, "Patient", "n1")
	s := jsonOf(t, after)
	for _, keep := range []string{`"family":"Nguyễn"`, `"family":"Khác"`, `"deceasedBoolean":true`, `"use":"official"`, `"use":"nickname"`, `"system":"phone"`, `"system":"email"`, `"Bình"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("update làm mất hoặc đổi %s: %s", keep, s)
		}
	}
	for _, bad := range []string{`"use":"old"`, `"system":"fax"`} {
		if strings.Contains(s, bad) {
			t.Errorf("trường chỉ-đọc bị ghi đè: %s", s)
		}
	}

	// đổi thứ tự phần tử: trường ẩn phải đi theo đúng phần tử (khớp theo phần nhìn thấy), không theo vị trí
	cur, _ = e.repo.ReadResource(ctx, "Patient", "n1")
	p = cur.(*fhir.Patient)
	p.Name[0], p.Name[1] = p.Name[1], p.Name[0]
	if _, err := e.repo.UpdateResource(ctx, p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ = e.repo.ReadResource(e.admin, "Patient", "n1")
	ap := after.(*fhir.Patient)
	if ap.Name[0].Given[0] != "Bé" || ap.Name[0].Family != "Khác" || ap.Name[1].Given[0] != "Bình" || ap.Name[1].Family != "Nguyễn" {
		t.Errorf("trường ẩn không đi theo phần tử khi đổi thứ tự: %s", jsonOf(t, after))
	}

	// xóa phần tử đầu và thêm phần tử mới ở cuối: phần tử còn lại giữ trường ẩn của nó (ghép theo nội dung nhìn thấy), phần tử bị xóa mang trường ẩn đi,
	// phần tử mới (vượt quá số phần tử cũ) không có gì để khôi phục
	cur, _ = e.repo.ReadResource(ctx, "Patient", "n1")
	p = cur.(*fhir.Patient)
	p.Name = append(p.Name[1:], fhir.HumanName{Given: []string{"Mới"}})
	if _, err := e.repo.UpdateResource(ctx, p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ = e.repo.ReadResource(e.admin, "Patient", "n1")
	ap = after.(*fhir.Patient)
	if len(ap.Name) != 2 || ap.Name[0].Family != "Nguyễn" || ap.Name[0].Given[0] != "Bình" || ap.Name[1].Family != "" || ap.Name[1].Given[0] != "Mới" || strings.Contains(jsonOf(t, after), "Khác") {
		t.Errorf("xóa/thêm phần tử: %s", jsonOf(t, after))
	}
}

// Tìm kiếm, sắp xếp và include không được dùng làm đường suy ra giá trị của trường ẩn.

func TestSearchCannotProbeHiddenFields(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p1"))
	ctx := e.as(policyOf(t,
		map[string]any{"resourceType": "Patient", "hiddenFields": []any{"birthDate", "name.family", "address", "generalPractitioner"}},
		map[string]any{"resourceType": "Observation", "hiddenFields": []any{"subject", "value[x]"}},
		map[string]any{"resourceType": "Encounter"}))
	search := func(rt string, q url.Values) error {
		t.Helper()
		sr, err := fhirsearch.Parse(rt, q)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.repo.Search(ctx, sr)
		return err
	}
	blocked := map[string]url.Values{
		"lọc theo trường ẩn":                  {"birthdate": {"1990-01-01"}},
		"lọc ngày với tiền tố":                {"birthdate": {"ge1980-01-01"}},
		"sắp xếp theo trường ẩn":              {"_sort": {"-birthdate"}},
		"family là name.family bị ẩn":         {"family": {"Nguyễn"}},
		"name bao trùm name.family":           {"name": {"Nguyễn"}},
		"address-city thuộc address bị ẩn":    {"address-city": {"Huế"}},
		"_filter nhắc trường ẩn":              {"_filter": {"birthdate eq 1990-01-01"}},
		"_filter lồng not/and":                {"_filter": {"gender eq female and not (birthdate eq 1991-01-01)"}},
		"_content quét cả resource":           {"_content": {"Nguyễn"}},
		"_text quét cả resource":              {"_text": {"Nguyễn"}},
		"_include theo tham chiếu bị ẩn":      {"_include": {"Patient:general-practitioner"}},
		"_revinclude theo subject bị ẩn":      {"_revinclude": {"Observation:subject"}},
		"_revinclude qua patient (cùng path)": {"_revinclude": {"Observation:patient"}},
	}
	for name, q := range blocked {
		if err := search("Patient", q); status(err) != 403 {
			t.Errorf("%s: err=%v (status %d), muốn 403", name, err, status(err))
		}
	}
	// chained và _has đi qua sub-search nên cũng bị chặn ở loại đích
	if err := search("Observation", url.Values{"subject:Patient.birthdate": {"1990-01-01"}}); err == nil {
		t.Errorf("chained theo trường ẩn của loại đích phải bị chặn")
	}
	if err := search("Observation", url.Values{"subject": {"Patient/p1"}}); status(err) != 403 {
		t.Errorf("Observation.subject bị ẩn: lọc theo subject phải 403, được %v", err)
	}
	if err := search("Observation", url.Values{"value-quantity": {"5"}}); status(err) != 403 {
		t.Errorf("value[x] bị ẩn: lọc value-quantity phải 403, được %v", err)
	}
	// tham số không dính trường ẩn vẫn dùng bình thường
	for name, q := range map[string]url.Values{
		"gender":                      {"gender": {"female"}},
		"given (name.given không ẩn)": {"given": {"An"}},
		"_sort theo trường hiện":      {"_sort": {"gender"}},
		"telecom":                     {"phone": {"0901"}},
		"không điều kiện":             {},
		"_id":                         {"_id": {"p1"}},
		"_lastUpdated":                {"_lastUpdated": {"ge2000-01-01"}},
	} {
		if err := search("Patient", q); err != nil {
			t.Errorf("%s bị chặn nhầm: %v", name, err)
		}
	}
	// loại không có quy tắc ẩn không bị ảnh hưởng
	if err := search("Encounter", url.Values{"status": {"finished"}}); err != nil {
		t.Errorf("Encounter không có hiddenFields: %v", err)
	}
	// người gọi không có chính sách (hoặc rule không ẩn gì) tìm bình thường
	sr, _ := fhirsearch.Parse("Patient", url.Values{"birthdate": {"1990-01-01"}})
	if res, err := e.repo.Search(e.admin, sr); err != nil || len(res.Matches) != 1 {
		t.Errorf("không có chính sách thì tìm được: %v %v", err, res)
	}
}

// Rule "*" có hiddenFields áp cho mọi loại: tìm kiếm hệ thống không giới hạn loại cũng không được chạm trường ẩn đó.
func TestSystemSearchRespectsWildcardHiddenFields(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p1"))
	ctx := e.as(policyOf(t, map[string]any{"resourceType": "*", "hiddenFields": []any{"meta.lastUpdated"}}))
	run := func(q url.Values) error {
		sr, err := fhirsearch.Parse("", q)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.repo.Search(ctx, sr)
		return err
	}
	if err := run(url.Values{"_type": {"Patient"}, "_lastUpdated": {"ge2000-01-01"}}); status(err) != 403 {
		t.Errorf("tìm hệ thống theo _lastUpdated bị ẩn: %v", err)
	}
	if err := run(url.Values{"_type": {"Patient"}, "_sort": {"_lastUpdated"}}); status(err) != 403 {
		t.Errorf("sắp xếp hệ thống theo _lastUpdated bị ẩn: %v", err)
	}
	if err := run(url.Values{"_type": {"Patient"}, "_id": {"p1"}}); err != nil {
		t.Errorf("_id không bị ẩn: %v", err)
	}
}

func TestExprPaths(t *testing.T) {
	cases := map[string][]string{
		"Patient.name.family":                                               {"name.family"},
		"Patient.name | Patient.address":                                    {"name", "address"},
		"Observation.value.ofType(Quantity)":                                {"value~Quantity"},
		"(Observation.value as Quantity)":                                   {"value~Quantity"},
		"Patient.telecom.where(system='phone')":                             {"telecom"},
		"Patient.extension.where(url='x').value.ofType(string)":             {"extension"},
		"Observation.subject.where(resolve() is Patient)":                   {"subject"},
		"Patient.deceased.exists() and Patient.deceased != false":           {"deceased", "and", "deceased", "false"},
		"Condition.onset.ofType(dateTime) | Condition.onset.ofType(Period)": {"onset~dateTime", "onset~Period"},
		"Resource.meta.lastUpdated":                                         {"meta.lastUpdated"},
	}
	for expr, want := range cases {
		got := fhirauth.ExprPathStrings(expr)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q => %v, muốn %v", expr, got, want)
		}
	}
}

func TestHiddenPathRelation(t *testing.T) {
	cases := []struct {
		hidden string
		expr   string
		want   bool
	}{
		{"value[x]", "Observation.value.ofType(Quantity)", true},
		{"valueQuantity", "Observation.value.ofType(Quantity)", true},
		{"valueQuantity", "Observation.value.ofType(string)", false},
		{"valueQuantity", "Observation.value", true},
		{"valueString", "Observation.component.value.ofType(Quantity)", false},
		{"name.family", "Patient.name", true},
		{"name.family", "Patient.name.family", true},
		{"name.family", "Patient.name.given", false},
		{"name", "Patient.name.given", true},
		{"telecom.value", "Patient.telecom.where(system='phone')", true},
		{"extension.valueString", "Patient.extension.where(url='x').value.ofType(string)", true},
		{"gender", "Patient.name | Patient.address", false},
		{"address", "Patient.name | Patient.address", true},
		{"birthDate", "Patient.deceased.exists() and Patient.deceased != false", false},
		{"deceased[x]", "Patient.deceased.exists() and Patient.deceased != false", true},
		{"meta.lastUpdated", "Resource.meta.lastUpdated", true},
		{"meta.lastUpdated", "Resource.id", false},
	}
	for _, c := range cases {
		if got := fhirauth.ExprTouches([]string{c.hidden}, c.expr); got != c.want {
			t.Errorf("hidden=%s expr=%q => %v, muốn %v", c.hidden, c.expr, got, c.want)
		}
	}
}

func TestSystemSearchWithoutTypesRespectsWildcard(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p1"))
	ctx := e.as(policyOf(t, map[string]any{"resourceType": "*", "hiddenFields": []any{"id"}}))
	// bộ phân tích chặn tham số ở tìm kiếm hệ thống không _type, nên dựng yêu cầu tay (như các đường nội bộ có thể làm)
	def, ok := fhirsearch.Lookup("Patient", "_id")
	if !ok {
		t.Fatal("không có tham số _id")
	}
	sr := &fhirsearch.Request{Filters: []fhirsearch.Filter{{Code: "_id", Def: def, Values: []string{"p1"}}}}
	if _, err := e.repo.Search(ctx, sr); status(err) != 403 {
		t.Errorf("tìm kiếm hệ thống không _type theo trường ẩn của rule *: %v", err)
	}
}
