package fhirsearch_test

import (
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirpath"
	"trivita-fhir/pkg/fhirsearch"
)

func decode(t *testing.T, js string) map[string]any {
	t.Helper()
	v, err := fhirpath.Decode([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

const patient = `{
 "resourceType":"Patient","id":"p1","active":true,"gender":"male","birthDate":"1974-12-25",
 "meta":{"lastUpdated":"2024-03-15T10:30:00Z","tag":[{"system":"http://t","code":"vip"}]},
 "identifier":[{"system":"http://hospital/mrn","value":"12345"},{"value":"no-system"}],
 "name":[{"use":"official","family":"Chalmers","given":["Peter","James"]},{"use":"usual","given":["Jim"]}],
 "telecom":[{"system":"phone","value":"555-1234"}],
 "address":[{"city":"Hà Nội","line":["1 Phố Huế"],"postalCode":"10000"}],
 "managingOrganization":{"reference":"Organization/org1"},
 "generalPractitioner":[{"reference":"https://example.org/fhir/Practitioner/dr1/_history/2"}],
 "deceasedBoolean":false,"multipleBirthInteger":2
}`

const observation = `{
 "resourceType":"Observation","id":"o1","status":"final",
 "code":{"coding":[{"system":"http://loinc.org","code":"29463-7","display":"Body Weight"}],"text":"Weight"},
 "subject":{"reference":"Patient/p1"},
 "effectiveDateTime":"2016-03-28T10:30:00+02:00",
 "valueQuantity":{"value":185,"unit":"lbs","system":"http://unitsofmeasure.org","code":"[lb_av]"}
}`

const encounter = `{"resourceType":"Encounter","id":"e1","status":"finished","class":{"code":"IMP"},"period":{"start":"2020-05-01","end":"2020-05-10"}}`

func TestMatch(t *testing.T) {
	cases := []struct {
		rt, res, query string
		want           bool
	}{
		// string
		{"Patient", patient, "family=chal", true},
		{"Patient", patient, "family=halmers", false},
		{"Patient", patient, "family:exact=Chalmers", true},
		{"Patient", patient, "family:exact=chalmers", false},
		{"Patient", patient, "family:contains=alm", true},
		{"Patient", patient, "given=jim", true},
		{"Patient", patient, "name=peter", true},
		{"Patient", patient, "address-city=ha%20noi", true}, // bỏ qua dấu
		{"Patient", patient, "address-city=há", true},
		{"Patient", patient, "address-postalcode=100", true},
		// OR trong một tham số, AND giữa các tham số
		{"Patient", patient, "given=nobody,Jim", true},
		{"Patient", patient, "given=nobody,other", false},
		{"Patient", patient, "given=peter&given=jim", true},
		{"Patient", patient, "given=peter&given=nobody", false},
		// token
		{"Patient", patient, "gender=male", true},
		{"Patient", patient, "gender=female", false},
		{"Patient", patient, "gender:not=female", true},
		{"Patient", patient, "gender:not=male", false},
		{"Patient", patient, "active=true", true},
		{"Patient", patient, "identifier=12345", true},
		{"Patient", patient, "identifier=http://hospital/mrn|12345", true},
		{"Patient", patient, "identifier=http://hospital/mrn|", true},
		{"Patient", patient, "identifier=http://other|12345", false},
		{"Patient", patient, "identifier=|no-system", true},
		{"Patient", patient, "identifier=|12345", false},
		{"Patient", patient, "_tag=http://t|vip", true},
		{"Patient", patient, "phone=555-1234", true},
		{"Observation", observation, "code=http://loinc.org|29463-7", true},
		{"Observation", observation, "code=29463-7", true},
		{"Observation", observation, "code:text=weight", true},
		{"Observation", observation, "code:text=height", false},
		{"Observation", observation, "status=final,amended", true},
		{"Patient", patient, "gender:missing=false", true},
		{"Patient", patient, "gender:missing=true", false},
		{"Patient", patient, "death-date:missing=true", true},
		// _id và _lastUpdated
		{"Patient", patient, "_id=p1", true},
		{"Patient", patient, "_id=p2", false},
		{"Patient", patient, "_lastUpdated=gt2024-01-01", true},
		{"Patient", patient, "_lastUpdated=lt2024-01-01", false},
		// reference
		{"Patient", patient, "organization=Organization/org1", true},
		{"Patient", patient, "organization=org1", true},
		{"Patient", patient, "organization=org2", false},
		{"Patient", patient, "organization:Organization=org1", true},
		{"Patient", patient, "general-practitioner=Practitioner/dr1", true},
		{"Observation", observation, "subject=Patient/p1", true},
		{"Observation", observation, "patient=p1", true},
		{"Observation", observation, "subject=Patient/other", false},
		// date
		{"Patient", patient, "birthdate=1974-12-25", true},
		{"Patient", patient, "birthdate=1974", true},
		{"Patient", patient, "birthdate=1975", false},
		{"Patient", patient, "birthdate=ne1975", true},
		{"Patient", patient, "birthdate=gt1970", true},
		{"Patient", patient, "birthdate=gt1974-12-25", false},
		{"Patient", patient, "birthdate=ge1974-12-25", true},
		{"Patient", patient, "birthdate=lt1980-01-01", true},
		{"Patient", patient, "birthdate=le1974-12-24", false},
		{"Patient", patient, "birthdate=sa1974-12-24", true},
		{"Patient", patient, "birthdate=eb1974-12-26", true},
		{"Patient", patient, "birthdate=gt1970&birthdate=lt1980", true},
		{"Observation", observation, "date=2016-03-28", true},
		{"Observation", observation, "date=2016-03-28T08:30:00Z", true},
		{"Observation", observation, "date=lt2016-03-28T08:00:00Z", false},
		// date với Period (Encounter.date)
		{"Encounter", encounter, "date=2020-05", true},     // khoảng tìm kiếm chứa trọn Period
		{"Encounter", encounter, "date=2020-05-05", false}, // Period dài hơn khoảng tìm kiếm: eq không khớp
		{"Encounter", encounter, "date=2020-06-01", false},
		{"Encounter", encounter, "date=gt2020-05-05", true},
		{"Encounter", encounter, "date=lt2020-05-05", true},
		// quantity
		{"Observation", observation, "value-quantity=185", true},
		{"Observation", observation, "value-quantity=gt180", true},
		{"Observation", observation, "value-quantity=lt180", false},
		{"Observation", observation, "value-quantity=185|http://unitsofmeasure.org|[lb_av]", true},
		{"Observation", observation, "value-quantity=185||[lb_av]", true},
		{"Observation", observation, "value-quantity=185||kg", false},
		{"Observation", observation, "value-quantity=185||lbs", true},
		{"Observation", observation, "value-quantity=18", false},
		{"Observation", observation, "value-quantity=1.85e2", true},
		// choice type không khớp
		{"Observation", observation, "value-string=abc", false},
	}
	for _, tc := range cases {
		t.Run(tc.rt+"?"+tc.query, func(t *testing.T) {
			req, err := fhirsearch.ParseQuery(tc.rt, tc.query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, err := fhirsearch.Match(decode(t, tc.res), req)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if got != tc.want {
				t.Fatalf("muốn %v, được %v", tc.want, got)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ rt, query, contains string }{
		{"Patient", "nope=1", "không hợp lệ"},
		{"Patient", "name:bogus=x", "modifier"},
		{"Patient", "_count=abc", "_count"},
		{"Patient", "_count=-1", "_count"},
		{"Patient", "_offset=x", "_offset"},
		{"Patient", "_total=maybe", "_total"},
		{"Patient", "_sort=nope", "sắp xếp"},
		{"Patient", "_include=Nope", "_include"},
		{"Patient", "_has:Observation:code=x", "_has không hợp lệ"},
		{"Patient", "_has:Nope:patient:code=x", "_has không hợp lệ"},
		{"Patient", "_has:Observation:status:code=x", "không phải tham số reference"},
		{"Patient", "_has:Observation:patient:nope=x", "không hợp lệ"},
		{"Observation", "_has:Observation:patient:code=x", "không trỏ tới"},
		{"", "_type=Patient,Organization&_has:Observation:patient:code=x", "đúng một kiểu"},
		{"Patient", "name.family=x", "cần tham số reference"},
		{"Patient", "organization.nope=x", "không hợp lệ"},
		{"Patient", "organization:Patient.name=x", "kiểu đích"},
		{"Patient", "gender.x=1", "cần tham số reference"},
		{"Patient", "gender:missing=maybe", "missing"},
		{"Nope", "", "resource type"},
		{"Observation", "combo-code-value-quantity=x", "composite"},
		{"Patient", "organization:in=http://vs", "chưa được hỗ trợ"}, // :in chỉ dùng cho token
		{"Patient", "identifier:identifier=x", "chưa được hỗ trợ"},
		{"", "name=x", "_type"},
	} {
		t.Run(tc.rt+"?"+tc.query, func(t *testing.T) {
			_, err := fhirsearch.ParseQuery(tc.rt, tc.query)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("muốn lỗi chứa %q, được %v", tc.contains, err)
			}
		})
	}
}

func TestParseControlParams(t *testing.T) {
	req, err := fhirsearch.ParseQuery("Patient", "_count=5000&_offset=10&_total=accurate&_sort=-birthdate,family&_include=Patient:organization&_revinclude:iterate=Observation:subject:Patient&_elements=id,name&_summary=true&_format=json")
	if err != nil {
		t.Fatal(err)
	}
	if req.Count != fhirsearch.MaxCount || req.Offset != 10 || req.Total != "accurate" {
		t.Fatalf("paging: %+v", req)
	}
	if len(req.Sort) != 2 || !req.Sort[0].Descending || req.Sort[0].Code != "birthdate" || req.Sort[1].Descending {
		t.Fatalf("sort: %+v", req.Sort)
	}
	if len(req.Include) != 1 || req.Include[0].Param != "organization" {
		t.Fatalf("include: %+v", req.Include)
	}
	if len(req.Revinclude) != 1 || !req.Revinclude[0].Iterate || req.Revinclude[0].Target != "Patient" {
		t.Fatalf("revinclude: %+v", req.Revinclude)
	}
	if len(req.Elements) != 2 {
		t.Fatalf("elements: %v", req.Elements)
	}
	def, err := fhirsearch.ParseQuery("Patient", "")
	if err != nil || def.Count != fhirsearch.DefaultCount {
		t.Fatalf("mặc định: %+v %v", def, err)
	}
}

func TestEscapedComma(t *testing.T) {
	req, err := fhirsearch.ParseQuery("Patient", `family=a\,b,c`)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Filters[0].Values; len(got) != 2 || got[0] != "a,b" || got[1] != "c" {
		t.Fatalf("values: %q", got)
	}
}

func TestSystemSearchTypes(t *testing.T) {
	req, err := fhirsearch.ParseQuery("", "_type=Patient,Observation&_id=p1")
	if err != nil {
		t.Fatal(err)
	}
	for js, want := range map[string]bool{patient: true, observation: false, encounter: false} {
		got, err := fhirsearch.Match(decode(t, js), req)
		if err != nil || got != want {
			t.Errorf("%.40s: muốn %v, được %v (%v)", js, want, got, err)
		}
	}
}

func TestSort(t *testing.T) {
	mk := func(js string) map[string]any { return decode(t, js) }
	rs := []map[string]any{
		mk(`{"resourceType":"Patient","id":"b","birthDate":"1990-01-01","name":[{"family":"Zed"}]}`),
		mk(`{"resourceType":"Patient","id":"a","birthDate":"1970-01-01","name":[{"family":"Ann"}]}`),
		mk(`{"resourceType":"Patient","id":"n"}`),
		mk(`{"resourceType":"Patient","id":"c","birthDate":"1980-01-01","name":[{"family":"Mia"}]}`),
	}
	order := func() string {
		var ids []string
		for _, r := range rs {
			ids = append(ids, r["id"].(string))
		}
		return strings.Join(ids, "")
	}
	req, _ := fhirsearch.ParseQuery("Patient", "_sort=birthdate")
	fhirsearch.Sort(rs, req.Sort)
	if order() != "acbn" {
		t.Errorf("birthdate asc: %s", order())
	}
	req, _ = fhirsearch.ParseQuery("Patient", "_sort=-birthdate")
	fhirsearch.Sort(rs, req.Sort)
	if order() != "bcan" {
		t.Errorf("birthdate desc: %s", order())
	}
	req, _ = fhirsearch.ParseQuery("Patient", "_sort=family")
	fhirsearch.Sort(rs, req.Sort)
	if order() != "acbn" {
		t.Errorf("family: %s", order())
	}
}

func TestReferencesAndQueryRoundTrip(t *testing.T) {
	def, ok := fhirsearch.Lookup("Observation", "subject")
	if !ok {
		t.Fatal("thiếu subject")
	}
	refs, err := fhirsearch.References(decode(t, observation), def)
	if err != nil || len(refs) != 1 || refs[0] != "Patient/p1" {
		t.Fatalf("refs: %v %v", refs, err)
	}
	if got := fhirsearch.NormalizeReference("https://h/fhir/Patient/9/_history/3"); got != "Patient/9" {
		t.Errorf("normalize: %s", got)
	}
	req, _ := fhirsearch.ParseQuery("Patient", "family:exact=Smith&_sort=-birthdate&_count=5&_total=accurate")
	q := req.Query(10)
	if q.Get("family:exact") != "Smith" || q.Get("_sort") != "-birthdate" || q.Get("_count") != "5" || q.Get("_offset") != "10" || q.Get("_total") != "accurate" {
		t.Errorf("query: %v", q)
	}
}

func TestParseChainedAndHas(t *testing.T) {
	r, err := fhirsearch.ParseQuery("Observation", "subject:Patient.family=smith&patient.gender=male&status=final")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Chained) != 2 || len(r.Filters) != 1 {
		t.Fatalf("chained=%d filters=%d", len(r.Chained), len(r.Filters))
	}
	c := r.Chained[0]
	if c.Reverse || c.Code != "patient" && c.Code != "subject" || len(c.Types) != 1 || c.Types[0] != "Patient" {
		t.Errorf("chained: %+v", c)
	}
	h, err := fhirsearch.ParseQuery("Patient", "_has:Observation:patient:code=x,y&_has:Observation:patient:_has:Provenance:target:agent=z")
	if err != nil || len(h.Chained) != 2 || !h.Chained[0].Reverse || h.Chained[0].Types[0] != "Observation" {
		t.Fatalf("_has: %+v %v", h, err)
	}
	// dựng lại query cho link phân trang
	q := h.Query(0)
	if len(q["_has:Observation:patient:code"]) != 1 || q.Get("_has:Observation:patient:code") != "x,y" {
		t.Errorf("Query: %v", q)
	}
	// Match từ chối chained (phải giải trước)
	if _, err := fhirsearch.Match(map[string]any{"resourceType": "Patient"}, h); err == nil {
		t.Error("Match phải từ chối chained chưa giải")
	}
}

func TestTerminologyModifiersParseButCannotMatchDirectly(t *testing.T) {
	for _, q := range []string{"gender:in=http://vs", "gender:not-in=http://vs", "gender:above=http://cs|male", "gender:below=http://cs|male"} {
		req, err := fhirsearch.ParseQuery("Patient", q)
		if err != nil {
			t.Errorf("%s: %v", q, err)
			continue
		}
		if !fhirsearch.NeedsResolution(req) || !req.Filters[0].IsTerminology() {
			t.Errorf("%s: phải cần giải trước", q)
		}
		if _, err := fhirsearch.Match(map[string]any{"resourceType": "Patient", "gender": "male"}, req); err == nil {
			t.Errorf("%s: Match phải báo lỗi vì chưa giải", q)
		}
		if got := req.Query(0).Get(req.Filters[0].Code + ":" + req.Filters[0].Modifier); got == "" {
			t.Errorf("%s: Query() mất modifier", q)
		}
	}
	// uri :below/:above không phải terminology
	req, err := fhirsearch.ParseQuery("ValueSet", "url:below=http://x")
	if err != nil || fhirsearch.NeedsResolution(req) {
		t.Errorf("url:below: %v", err)
	}
}
