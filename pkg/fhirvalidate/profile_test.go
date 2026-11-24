package fhirvalidate_test

import (
	"errors"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirpath"
	"trivita-fhir/pkg/fhirvalidate"
)

const (
	patientProfileURL = "http://example.org/StructureDefinition/vn-patient"
	obsProfileURL     = "http://example.org/StructureDefinition/bp-panel"
	extProfileURL     = "http://example.org/StructureDefinition/ethnicity-ext"
)

// vn-patient: identifier bắt buộc, slice "cccd" (system cố định, đúng 1), slicing đóng theo system;
// birthDate bắt buộc; photo cấm (max 0); gender cố định "female"? không: name.family dài tối đa 10; extension slice theo url.
const patientProfile = `{
 "resourceType":"StructureDefinition","url":"` + patientProfileURL + `","type":"Patient",
 "snapshot":{"element":[
  {"id":"Patient","path":"Patient"},
  {"id":"Patient.photo","path":"Patient.photo","max":"0"},
  {"id":"Patient.birthDate","path":"Patient.birthDate","min":1,"max":"1"},
  {"id":"Patient.name","path":"Patient.name","min":0,"max":"*"},
  {"id":"Patient.name.family","path":"Patient.name.family","maxLength":10},
  {"id":"Patient.extension","path":"Patient.extension","min":0,"max":"*",
   "slicing":{"discriminator":[{"type":"value","path":"url"}],"rules":"open"}},
  {"id":"Patient.extension:ethnicity","path":"Patient.extension","sliceName":"ethnicity","min":1,"max":"1",
   "type":[{"code":"Extension","profile":["` + extProfileURL + `"]}]},
  {"id":"Patient.identifier","path":"Patient.identifier","min":1,"max":"*",
   "slicing":{"discriminator":[{"type":"pattern","path":"system"}],"rules":"closed"}},
  {"id":"Patient.identifier:cccd","path":"Patient.identifier","sliceName":"cccd","min":1,"max":"1"},
  {"id":"Patient.identifier:cccd.system","path":"Patient.identifier.system","min":1,"fixedUri":"http://vn.example/cccd"},
  {"id":"Patient.identifier:bhyt","path":"Patient.identifier","sliceName":"bhyt","min":0,"max":"2"},
  {"id":"Patient.identifier:bhyt.system","path":"Patient.identifier.system","min":1,"patternUri":"http://vn.example/bhyt"}
 ]}}`

const extProfile = `{
 "resourceType":"StructureDefinition","url":"` + extProfileURL + `","type":"Extension",
 "snapshot":{"element":[
  {"id":"Extension","path":"Extension"},
  {"id":"Extension.url","path":"Extension.url","fixedUri":"http://vn.example/ethnicity"}
 ]}}`

// bp-panel: value[x] chỉ Quantity; component slice theo pattern của code (systolic bắt buộc, diastolic bắt buộc), slicing mở.
const obsProfile = `{
 "resourceType":"StructureDefinition","url":"` + obsProfileURL + `","type":"Observation",
 "snapshot":{"element":[
  {"id":"Observation","path":"Observation"},
  {"id":"Observation.value[x]","path":"Observation.value[x]","min":0,"max":"1","type":[{"code":"Quantity"}]},
  {"id":"Observation.component","path":"Observation.component","min":2,"max":"*",
   "slicing":{"discriminator":[{"type":"pattern","path":"code"}],"rules":"open","ordered":false}},
  {"id":"Observation.component:sys","path":"Observation.component","sliceName":"sys","min":1,"max":"1"},
  {"id":"Observation.component:sys.code","path":"Observation.component.code","min":1,
   "patternCodeableConcept":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]}},
  {"id":"Observation.component:dia","path":"Observation.component","sliceName":"dia","min":1,"max":"1"},
  {"id":"Observation.component:dia.code","path":"Observation.component.code","min":1,
   "patternCodeableConcept":{"coding":[{"system":"http://loinc.org","code":"8462-4"}]}}
 ]}}`

var profiles = map[string]string{
	patientProfileURL: patientProfile, obsProfileURL: obsProfile, extProfileURL: extProfile,
}

func resolver(t *testing.T) fhirvalidate.ProfileResolver {
	t.Helper()
	return func(u string) (map[string]any, error) {
		if u == "http://example.org/broken" {
			return nil, errors.New("kho lỗi")
		}
		src, ok := profiles[u]
		if !ok {
			return nil, nil
		}
		v, err := fhirpath.Decode([]byte(src))
		if err != nil {
			t.Fatalf("profile hỏng: %v", err)
		}
		return v.(map[string]any), nil
	}
}

func check(t *testing.T, doc string, opts *fhirvalidate.Options) fhirvalidate.Result {
	t.Helper()
	if opts == nil {
		opts = &fhirvalidate.Options{}
	}
	opts.Profiles = resolver(t)
	return fhirvalidate.ValidateJSON([]byte(doc), opts)
}

func wantErr(t *testing.T, res fhirvalidate.Result, contains string) {
	t.Helper()
	for _, i := range res.Errors() {
		if strings.Contains(i.Message, contains) {
			return
		}
	}
	t.Fatalf("cần lỗi chứa %q, có: %v", contains, res.Issues)
}

// profileIssues bỏ các khuyến nghị lõi (dom-6...) để chỉ đếm vấn đề do profile và tra cứu.
func profileIssues(res fhirvalidate.Result) []fhirvalidate.Issue {
	var out []fhirvalidate.Issue
	for _, i := range res.Issues {
		if i.Code != fhirvalidate.CodeInvariant {
			out = append(out, i)
		}
	}
	return out
}

func wantOK(t *testing.T, res fhirvalidate.Result) {
	t.Helper()
	if !res.OK() {
		t.Fatalf("cần hợp lệ, có lỗi: %v", res.Errors())
	}
}

const goodPatient = `{"resourceType":"Patient","meta":{"profile":["` + patientProfileURL + `"]},
 "birthDate":"1990-01-02",
 "extension":[{"url":"http://vn.example/ethnicity","valueString":"Kinh"}],
 "identifier":[{"system":"http://vn.example/cccd","value":"001"},{"system":"http://vn.example/bhyt","value":"A"}],
 "name":[{"family":"Nguyen"}]}`

func TestProfilePatientValid(t *testing.T) {
	res := check(t, goodPatient, nil)
	wantOK(t, res)
	if is := profileIssues(res); len(is) != 0 {
		t.Fatalf("không được có cảnh báo: %v", is)
	}
}

func TestProfileWithoutResolverIgnored(t *testing.T) {
	bad := strings.Replace(goodPatient, `"birthDate":"1990-01-02",`, "", 1)
	wantOK(t, fhirvalidate.ValidateJSON([]byte(bad), nil))
}

func TestProfileCardinality(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"thiếu birthDate", strings.Replace(goodPatient, `"birthDate":"1990-01-02",`, "", 1), "Cần ít nhất 1"},
		{"photo bị cấm", strings.Replace(goodPatient, `"birthDate"`, `"photo":[{"contentType":"image/png"}],"birthDate"`, 1), "tối đa 0"},
		{"thiếu identifier", strings.Replace(goodPatient, `"identifier":[{"system":"http://vn.example/cccd","value":"001"},{"system":"http://vn.example/bhyt","value":"A"}],`, "", 1), "Cần ít nhất 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { wantErr(t, check(t, c.doc, nil), c.want) })
	}
}

func TestProfileMaxLength(t *testing.T) {
	wantErr(t, check(t, strings.Replace(goodPatient, `"Nguyen"`, `"Nguyenthaihoc"`, 1), nil), "Độ dài tối đa là 10")
}

func TestProfileMaxLengthBoundary(t *testing.T) {
	wantOK(t, check(t, strings.Replace(goodPatient, `"Nguyen"`, `"Nguyenthai"`, 1), nil))                          // đúng 10 ký tự
	wantOK(t, check(t, strings.Replace(goodPatient, `"Nguyen"`, `"Nguyễnthị"`, 1), nil))                           // 9 ký tự, nhiều byte hơn 10
	wantErr(t, check(t, strings.Replace(goodPatient, `"Nguyen"`, `"Nguyenthaih"`, 1), nil), "Độ dài tối đa là 10") // 11 ký tự
}

func TestProfileSlicingCounts(t *testing.T) {
	// thiếu slice cccd (min 1) nhưng vẫn có identifier khác (bhyt)
	noCCCD := strings.Replace(goodPatient, `{"system":"http://vn.example/cccd","value":"001"},`, "", 1)
	wantErr(t, check(t, noCCCD, nil), `Slice "cccd" cần ít nhất 1`)

	// hai cccd vượt max 1
	two := strings.Replace(goodPatient, `{"system":"http://vn.example/bhyt","value":"A"}`, `{"system":"http://vn.example/cccd","value":"002"}`, 1)
	wantErr(t, check(t, two, nil), `Slice "cccd" cho phép tối đa 1`)

	// ba bhyt vượt max 2
	three := strings.Replace(goodPatient, `{"system":"http://vn.example/bhyt","value":"A"}`,
		`{"system":"http://vn.example/bhyt","value":"A"},{"system":"http://vn.example/bhyt","value":"B"},{"system":"http://vn.example/bhyt","value":"C"}`, 1)
	wantErr(t, check(t, three, nil), `Slice "bhyt" cho phép tối đa 2`)
}

func TestProfileClosedSlicing(t *testing.T) {
	other := strings.Replace(goodPatient, `{"system":"http://vn.example/bhyt","value":"A"}`, `{"system":"http://other","value":"A"}`, 1)
	wantErr(t, check(t, other, nil), "không thuộc slice nào")
}

func TestProfileExtensionSliceViaTypeProfile(t *testing.T) {
	// slice ethnicity khớp theo url cố định nằm trong profile kiểu của Extension
	noExt := strings.Replace(goodPatient, `"extension":[{"url":"http://vn.example/ethnicity","valueString":"Kinh"}],`, "", 1)
	wantErr(t, check(t, noExt, nil), `Slice "ethnicity" cần ít nhất 1`)

	// extension khác url vẫn được (slicing mở) nhưng không tính vào slice
	otherExt := strings.Replace(goodPatient, `http://vn.example/ethnicity`, `http://vn.example/other`, 1)
	wantErr(t, check(t, otherExt, nil), `Slice "ethnicity" cần ít nhất 1`)
}

func TestProfileObservationChoiceTypeAndPatternSlices(t *testing.T) {
	good := `{"resourceType":"Observation","status":"final","code":{"text":"bp"},"meta":{"profile":["` + obsProfileURL + `"]},
	 "valueQuantity":{"value":1},
	 "component":[
	  {"code":{"coding":[{"system":"http://loinc.org","code":"8480-6","display":"Systolic"}]},"valueQuantity":{"value":120}},
	  {"code":{"coding":[{"system":"http://loinc.org","code":"8462-4"}]},"valueQuantity":{"value":80}},
	  {"code":{"coding":[{"system":"http://loinc.org","code":"0000-0"}]},"valueQuantity":{"value":1}}]}`
	wantOK(t, check(t, good, nil))

	wrongType := strings.Replace(good, `"valueQuantity":{"value":1},
	 "component"`, `"valueString":"x",
	 "component"`, 1)
	wantErr(t, check(t, wrongType, nil), "Kiểu String không được profile cho phép")

	noDia := strings.Replace(good, `8462-4`, `1111-1`, 1)
	wantErr(t, check(t, noDia, nil), `Slice "dia" cần ít nhất 1`)

	oneComp := `{"resourceType":"Observation","status":"final","code":{"text":"bp"},"meta":{"profile":["` + obsProfileURL + `"]},
	 "component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]}}]}`
	wantErr(t, check(t, oneComp, nil), "Cần ít nhất 2")
}

func TestProfileUnknownAndErrors(t *testing.T) {
	doc := `{"resourceType":"Patient","meta":{"profile":["http://example.org/none"]}}`
	res := check(t, doc, nil)
	wantOK(t, res)
	if is := profileIssues(res); len(is) != 1 || is[0].Severity != fhirvalidate.Warning {
		t.Fatalf("cần đúng một cảnh báo: %v", is)
	}
	wantErr(t, check(t, doc, &fhirvalidate.Options{UnknownProfile: fhirvalidate.Error}), "Không tìm thấy profile")

	broken := check(t, `{"resourceType":"Patient","meta":{"profile":["http://example.org/broken"]}}`, nil)
	wantOK(t, broken)
	if is := profileIssues(broken); len(is) != 1 || !strings.Contains(is[0].Message, "kho lỗi") {
		t.Fatalf("lỗi tra cứu phải là cảnh báo: %v", is)
	}
}

func TestProfileWrongResourceType(t *testing.T) {
	doc := `{"resourceType":"Observation","status":"final","code":{"text":"x"},"meta":{"profile":["` + patientProfileURL + `"]}}`
	wantErr(t, check(t, doc, nil), "dành cho Patient")
}

func TestProfileSkippedWhenBaseInvalid(t *testing.T) {
	// lỗi cấu trúc cơ bản không kèm lỗi profile chồng lên
	res := check(t, `{"resourceType":"Patient","meta":{"profile":["`+patientProfileURL+`"]},"foo":1}`, nil)
	for _, i := range res.Errors() {
		if strings.Contains(i.Message, "profile "+patientProfileURL) {
			t.Fatalf("không được báo lỗi profile khi cấu trúc hỏng: %v", i)
		}
	}
	if res.OK() {
		t.Fatal("phải còn lỗi cấu trúc")
	}
}

func TestProfileFixedAndPatternValues(t *testing.T) {
	// fixed: toàn bộ giá trị; pattern: tập con
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/fx","type":"Patient","snapshot":{"element":[
	 {"id":"Patient","path":"Patient"},
	 {"id":"Patient.gender","path":"Patient.gender","fixedCode":"female"},
	 {"id":"Patient.maritalStatus","path":"Patient.maritalStatus","patternCodeableConcept":{"coding":[{"system":"http://s","code":"M"}]}}]}}`
	profiles["http://example.org/fx"] = sd
	defer delete(profiles, "http://example.org/fx")
	base := `{"resourceType":"Patient","meta":{"profile":["http://example.org/fx"]},"gender":"female",
	 "maritalStatus":{"coding":[{"system":"http://s","code":"M","display":"x"},{"system":"http://t","code":"Z"}],"text":"t"}}`
	wantOK(t, check(t, base, nil))
	wantErr(t, check(t, strings.Replace(base, `"female"`, `"male"`, 1), nil), "giá trị cố định")
	wantErr(t, check(t, strings.Replace(base, `"code":"M"`, `"code":"S"`, 1), nil), "không khớp mẫu")
}

func TestProfileUnsupportedDiscriminatorWarns(t *testing.T) {
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/dp","type":"Patient","snapshot":{"element":[
	 {"id":"Patient","path":"Patient"},
	 {"id":"Patient.contact","path":"Patient.contact","slicing":{"discriminator":[{"type":"profile","path":"relationship"}],"rules":"closed"}},
	 {"id":"Patient.contact:a","path":"Patient.contact","sliceName":"a","min":5}]}}`
	profiles["http://example.org/dp"] = sd
	defer delete(profiles, "http://example.org/dp")
	res := check(t, `{"resourceType":"Patient","meta":{"profile":["http://example.org/dp"]},"contact":[{"name":{"text":"x"}}]}`, nil)
	wantOK(t, res) // không áp cardinality/closed khi không đánh giá được
	if is := profileIssues(res); len(is) != 1 || !strings.Contains(is[0].Message, "Bỏ qua kiểm slicing") {
		t.Fatalf("cần cảnh báo bỏ qua slicing: %v", is)
	}
}

func TestProfileOwnConstraint(t *testing.T) {
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/cn","type":"Patient","snapshot":{"element":[
	 {"id":"Patient","path":"Patient","constraint":[
	   {"key":"vn-1","severity":"error","human":"Phải có số điện thoại","expression":"telecom.where(system='phone').exists()","source":"http://example.org/cn"},
	   {"key":"dom-2","severity":"error","human":"đã có ở lõi","expression":"false","source":"http://hl7.org/fhir/StructureDefinition/DomainResource"}]}]}}`
	profiles["http://example.org/cn"] = sd
	defer delete(profiles, "http://example.org/cn")
	doc := `{"resourceType":"Patient","meta":{"profile":["http://example.org/cn"]}%s}`
	wantErr(t, check(t, strings.Replace(doc, "%s", "", 1), nil), "vn-1")
	wantOK(t, check(t, strings.Replace(doc, "%s", `,"telecom":[{"system":"phone","value":"1"}]`, 1), nil))
}

func TestProfileDifferentialOnly(t *testing.T) {
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/df","type":"Patient","differential":{"element":[
	 {"id":"Patient","path":"Patient"},
	 {"id":"Patient.birthDate","path":"Patient.birthDate","min":1}]}}`
	profiles["http://example.org/df"] = sd
	defer delete(profiles, "http://example.org/df")
	wantErr(t, check(t, `{"resourceType":"Patient","meta":{"profile":["http://example.org/df"]}}`, nil), "Cần ít nhất 1")
}

func withProfile(t *testing.T, url, sd string) func() {
	t.Helper()
	profiles[url] = sd
	return func() { delete(profiles, url) }
}

// ordered: slice phải xuất hiện theo thứ tự khai báo; openAtEnd: phần tử ngoài slice chỉ được đứng sau các slice.
func TestProfileOrderedAndOpenAtEnd(t *testing.T) {
	mk := func(rules string, ordered bool) string {
		return `{"resourceType":"StructureDefinition","url":"http://example.org/ord","type":"Patient","snapshot":{"element":[
		 {"id":"Patient","path":"Patient"},
		 {"id":"Patient.identifier","path":"Patient.identifier","slicing":{"discriminator":[{"type":"value","path":"system"}],"rules":"` + rules + `","ordered":` + map[bool]string{true: "true", false: "false"}[ordered] + `}},
		 {"id":"Patient.identifier:a","path":"Patient.identifier","sliceName":"a"},
		 {"id":"Patient.identifier:a.system","path":"Patient.identifier.system","fixedUri":"urn:a"},
		 {"id":"Patient.identifier:b","path":"Patient.identifier","sliceName":"b"},
		 {"id":"Patient.identifier:b.system","path":"Patient.identifier.system","fixedUri":"urn:b"}]}}`
	}
	pat := func(systems ...string) string {
		var ids []string
		for _, s := range systems {
			ids = append(ids, `{"system":"`+s+`","value":"1"}`)
		}
		return `{"resourceType":"Patient","meta":{"profile":["http://example.org/ord"]},"identifier":[` + strings.Join(ids, ",") + `]}`
	}
	defer withProfile(t, "http://example.org/ord", mk("open", true))()
	wantOK(t, check(t, pat("urn:a", "urn:b"), nil))
	wantErr(t, check(t, pat("urn:b", "urn:a"), nil), "sai thứ tự")
	wantOK(t, check(t, pat("urn:a", "urn:b", "urn:zzz"), nil)) // open: phần tử lạ được phép

	profiles["http://example.org/ord"] = mk("openAtEnd", false)
	wantOK(t, check(t, pat("urn:a", "urn:b", "urn:zzz"), nil))
	wantErr(t, check(t, pat("urn:zzz", "urn:a"), nil), "openAtEnd")

	profiles["http://example.org/ord"] = mk("open", false)
	wantOK(t, check(t, pat("urn:b", "urn:a"), nil)) // không ordered thì thứ tự tùy ý
	wantOK(t, check(t, pat("urn:zzz", "urn:a"), nil))
}

// discriminator exists: slice chia theo việc có hay không có một phần tử.
func TestProfileExistsDiscriminator(t *testing.T) {
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/ex","type":"Patient","snapshot":{"element":[
	 {"id":"Patient","path":"Patient"},
	 {"id":"Patient.identifier","path":"Patient.identifier","slicing":{"discriminator":[{"type":"exists","path":"period"}],"rules":"open"}},
	 {"id":"Patient.identifier:ended","path":"Patient.identifier","sliceName":"ended","max":"1"},
	 {"id":"Patient.identifier:ended.period","path":"Patient.identifier.period","min":1},
	 {"id":"Patient.identifier:current","path":"Patient.identifier","sliceName":"current","min":1},
	 {"id":"Patient.identifier:current.period","path":"Patient.identifier.period","max":"0"}]}}`
	defer withProfile(t, "http://example.org/ex", sd)()
	doc := func(ids string) string {
		return `{"resourceType":"Patient","meta":{"profile":["http://example.org/ex"]},"identifier":[` + ids + `]}`
	}
	cur := `{"value":"1"}`
	old := `{"value":"2","period":{"end":"2020-01-01"}}`
	wantOK(t, check(t, doc(cur+","+old), nil))
	wantErr(t, check(t, doc(old), nil), `Slice "current" cần ít nhất 1`)
	wantErr(t, check(t, doc(cur+","+old+","+old), nil), `Slice "ended" cho phép tối đa 1`)
}

// discriminator type: Observation.value[x] chia theo kiểu.
func TestProfileTypeDiscriminator(t *testing.T) {
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/ty","type":"Observation","snapshot":{"element":[
	 {"id":"Observation","path":"Observation"},
	 {"id":"Observation.value[x]","path":"Observation.value[x]","type":[{"code":"Quantity"},{"code":"string"}],
	  "slicing":{"discriminator":[{"type":"type","path":"$this"}],"rules":"closed"}},
	 {"id":"Observation.value[x]:valueQuantity","path":"Observation.value[x]","sliceName":"valueQuantity","min":1,"max":"1","type":[{"code":"Quantity"}]},
	 {"id":"Observation.value[x]:valueString","path":"Observation.value[x]","sliceName":"valueString","max":"0","type":[{"code":"string"}]}]}}`
	defer withProfile(t, "http://example.org/ty", sd)()
	doc := func(v string) string {
		return `{"resourceType":"Observation","status":"final","code":{"text":"x"},"meta":{"profile":["http://example.org/ty"]}` + v + `}`
	}
	wantOK(t, check(t, doc(`,"valueQuantity":{"value":1}`), nil))
	wantErr(t, check(t, doc(``), nil), `Slice "valueQuantity" cần ít nhất 1`)
	wantErr(t, check(t, doc(`,"valueString":"x"`), nil), `Slice "valueString" cho phép tối đa 0`)
}

func TestProfileValueDiscriminatorAcrossArrays(t *testing.T) {
	// path nhiều bước (code.coding.code) trên mảng lồng nhau
	const sd = `{"resourceType":"StructureDefinition","url":"http://example.org/ar","type":"Observation","snapshot":{"element":[
	 {"id":"Observation","path":"Observation"},
	 {"id":"Observation.category","path":"Observation.category","slicing":{"discriminator":[{"type":"value","path":"coding.code"}],"rules":"open"}},
	 {"id":"Observation.category:vital","path":"Observation.category","sliceName":"vital","min":1},
	 {"id":"Observation.category:vital.coding","path":"Observation.category.coding","min":1},
	 {"id":"Observation.category:vital.coding.code","path":"Observation.category.coding.code","fixedCode":"vital-signs"}]}}`
	defer withProfile(t, "http://example.org/ar", sd)()
	doc := func(cats string) string {
		return `{"resourceType":"Observation","status":"final","code":{"text":"x"},"meta":{"profile":["http://example.org/ar"]},"category":[` + cats + `]}`
	}
	wantOK(t, check(t, doc(`{"coding":[{"code":"other"}]},{"coding":[{"code":"vital-signs"}]}`), nil))
	wantErr(t, check(t, doc(`{"coding":[{"code":"other"}]}`), nil), `Slice "vital" cần ít nhất 1`)
}
