package fhirstore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirstore"
)

// dataset là tập resource đa dạng để so sánh Store với MemoryRepository (làm chuẩn ngữ nghĩa).
var dataset = []string{
	// Patient
	`{"resourceType":"Patient","id":"p01","active":true,"gender":"male","birthDate":"1974-12-25",
	  "identifier":[{"system":"http://hospital/mrn","value":"12345"},{"value":"no-system"}],
	  "name":[{"use":"official","family":"Chalmers","given":["Peter","James"]},{"use":"usual","given":["Jim"]}],
	  "telecom":[{"system":"phone","value":"555-1234"},{"system":"email","value":"p@x.org"}],
	  "address":[{"city":"Hà Nội","line":["1 Phố Huế"],"postalCode":"10000","country":"VN"}],
	  "managingOrganization":{"reference":"Organization/o1"},
	  "generalPractitioner":[{"reference":"https://example.org/fhir/Practitioner/dr1/_history/2"}],
	  "meta":{"tag":[{"system":"http://t","code":"vip"}],"profile":["http://profile/a"]},
	  "deceasedBoolean":false,"multipleBirthInteger":2}`,
	`{"resourceType":"Patient","id":"p02","active":false,"gender":"female","birthDate":"1990-03",
	  "identifier":[{"system":"http://hospital/mrn","value":"67890"}],
	  "name":[{"family":"Đặng","given":["Thu","Hà"]}],
	  "address":[{"city":"Đà Nẵng","postalCode":"50000"}],
	  "managingOrganization":{"reference":"Organization/o2"},
	  "meta":{"tag":[{"system":"http://t","code":"std"}]},
	  "deceasedDateTime":"2020-01-01T00:00:00Z"}`,
	`{"resourceType":"Patient","id":"p03","gender":"male","birthDate":"1985",
	  "name":[{"family":"Smith","given":["Al"]},{"family":"Smyth"}],
	  "generalPractitioner":[{"reference":"Practitioner/dr2"}]}`,
	`{"resourceType":"Patient","id":"p04","gender":"other","birthDate":"2001-07-04T10:00:00+07:00",
	  "name":[{"text":"Anonymous Person"}],"link":[{"other":{"reference":"Patient/p01"},"type":"seealso"}]}`,
	`{"resourceType":"Patient","id":"p05"}`,
	`{"resourceType":"Patient","id":"p06","gender":"female","birthDate":"1974-12-25","name":[{"family":"Chalmers","given":["Anna"]}],
	  "managingOrganization":{"reference":"Organization/o1"}}`,
	// Organization
	`{"resourceType":"Organization","id":"o1","name":"Burgers University Medical Center","active":true,
	  "identifier":[{"system":"http://hospital/org","value":"BUMC"}],"address":[{"city":"Utrecht"}]}`,
	`{"resourceType":"Organization","id":"o2","name":"Đại học Y","active":false,"partOf":{"reference":"Organization/o1"}}`,
	// Practitioner
	`{"resourceType":"Practitioner","id":"dr1","name":[{"family":"House","given":["Greg"]}]}`,
	// Observation
	`{"resourceType":"Observation","id":"ob01","status":"final",
	  "code":{"coding":[{"system":"http://loinc.org","code":"29463-7","display":"Body Weight"}],"text":"Weight"},
	  "subject":{"reference":"Patient/p01"},"effectiveDateTime":"2016-03-28T10:30:00+02:00",
	  "valueQuantity":{"value":185,"unit":"lbs","system":"http://unitsofmeasure.org","code":"[lb_av]"}}`,
	`{"resourceType":"Observation","id":"ob02","status":"amended",
	  "code":{"coding":[{"system":"http://loinc.org","code":"8302-2","display":"Body Height"}]},
	  "subject":{"reference":"Patient/p01"},"effectiveDateTime":"2016-03-28",
	  "valueQuantity":{"value":70.5,"unit":"in","system":"http://unitsofmeasure.org","code":"[in_i]"}}`,
	`{"resourceType":"Observation","id":"ob03","status":"final",
	  "code":{"coding":[{"system":"http://loinc.org","code":"29463-7"}]},
	  "subject":{"reference":"Patient/p02"},"effectivePeriod":{"start":"2020-05-01","end":"2020-05-10"},
	  "valueString":"abc"}`,
	`{"resourceType":"Observation","id":"ob04","status":"final","code":{"text":"Mood"},
	  "subject":{"reference":"Patient/p03"},"effectiveDateTime":"2024-11-30T23:59:59.123Z",
	  "valueCodeableConcept":{"coding":[{"system":"http://snomed.info/sct","code":"1","display":"Happy"}],"text":"Good"},
	  "performer":[{"reference":"Practitioner/dr1"}]}`,
	`{"resourceType":"Observation","id":"ob05","status":"preliminary","code":{"text":"None"},"valueInteger":7}`,
	// Observation có component (composite search)
	`{"resourceType":"Observation","id":"bp1","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"85354-9"}]},
	  "subject":{"reference":"Patient/p01"},
	  "valueQuantity":{"value":120,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"},
	  "component":[
	   {"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":140,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"}},
	   {"code":{"coding":[{"system":"http://loinc.org","code":"8462-4"}]},"valueQuantity":{"value":90,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"}}]}`,
	`{"resourceType":"Observation","id":"bp2","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"85354-9"}]},
	  "subject":{"reference":"Patient/p02"},
	  "component":[
	   {"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":110,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"}},
	   {"code":{"coding":[{"system":"http://loinc.org","code":"8462-4"}]},"valueQuantity":{"value":150,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"}},
	   {"code":{"text":"note"},"valueString":"dizzy"},
	   {"code":{"coding":[{"system":"http://loinc.org","code":"8310-5"}]},"valueCodeableConcept":{"coding":[{"system":"http://snomed.info/sct","code":"1"}]}}]}`,
	`{"resourceType":"Observation","id":"bp3","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"8302-2"}]},
	  "valueString":"tall","effectiveDateTime":"2022-02-02"}`,
	// Encounter
	`{"resourceType":"Encounter","id":"e1","status":"finished","class":{"system":"http://act","code":"IMP"},
	  "subject":{"reference":"Patient/p01"},"period":{"start":"2020-05-01","end":"2020-05-10"}}`,
	`{"resourceType":"Encounter","id":"e2","status":"in-progress","class":{"code":"AMB"},"subject":{"reference":"Patient/p02"},
	  "period":{"start":"2021-01-01T08:00:00Z"}}`,
	// ValueSet (tham số kiểu uri)
	`{"resourceType":"ValueSet","id":"vs1","status":"active","url":"http://example.org/fhir/ValueSet/a","version":"1","name":"A"}`,
	`{"resourceType":"ValueSet","id":"vs2","status":"draft","url":"http://example.org/fhir/ValueSet/a/b","name":"B"}`,
	`{"resourceType":"ValueSet","id":"vs3","status":"active","url":"http://other.org/vs","name":"C"}`,
}

func load(t *testing.T, ctx context.Context, repo fhirrouter.Repository) {
	t.Helper()
	for _, js := range dataset {
		r, err := fhir.UnmarshalResource([]byte(js))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateResource(ctx, r); err != nil {
			t.Fatalf("create %s: %v", r.GetID(), err)
		}
		time.Sleep(2 * time.Millisecond) // lastUpdated có độ phân giải ms: tránh trùng làm thứ tự sort phụ thuộc thời gian
	}
	// một bản đã xóa và một bản đã cập nhật (lịch sử)
	gone, _ := fhir.UnmarshalResource([]byte(`{"resourceType":"Patient","id":"gone","name":[{"family":"Chalmers"}],"gender":"male"}`))
	if _, err := repo.CreateResource(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteResource(ctx, "Patient", "gone"); err != nil {
		t.Fatal(err)
	}
	upd, _ := fhir.UnmarshalResource([]byte(`{"resourceType":"Patient","id":"p05","gender":"female","name":[{"family":"Updated"}],"birthDate":"1999-09-09"}`))
	if _, err := repo.UpdateResource(ctx, upd, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

// queries phủ mọi kiểu tham số, modifier, prefix, sort, phân trang, total, include, search hệ thống.
var queries = [][2]string{
	// string
	{"Patient", "family=chal"}, {"Patient", "family:exact=Chalmers"}, {"Patient", "family:exact=chalmers"}, {"Patient", "family:contains=alm"},
	{"Patient", "family=dang"}, {"Patient", "family=đặng"}, {"Patient", "given=ha"}, {"Patient", "given=jim,thu"}, {"Patient", "given=peter&given=james"},
	{"Patient", "given=peter&given=nobody"}, {"Patient", "name=anonymous"}, {"Patient", "name=smy"}, {"Patient", "address-city=ha%20noi"},
	{"Patient", "address-city=da%20nang,utrecht"}, {"Patient", "address-postalcode=100"}, {"Patient", "address=viet"}, {"Patient", "family="},
	{"Organization", "name=burgers"}, {"Organization", "name:contains=medical"}, {"Organization", "name=dai%20hoc"}, {"Organization", "address-city=utrecht"},
	// token
	{"Patient", "gender=male"}, {"Patient", "gender=male,female"}, {"Patient", "gender:not=male"}, {"Patient", "gender:missing=true"},
	{"Patient", "gender:missing=false"}, {"Patient", "active=true"}, {"Patient", "active=false"}, {"Patient", "active:missing=true"},
	{"Patient", "identifier=12345"}, {"Patient", "identifier=http://hospital/mrn|12345"}, {"Patient", "identifier=http://hospital/mrn|"},
	{"Patient", "identifier=|no-system"}, {"Patient", "identifier=|12345"}, {"Patient", "identifier=http://other|12345"},
	{"Patient", "identifier:not=12345"}, {"Patient", "_tag=http://t|vip"}, {"Patient", "_tag=vip"}, {"Patient", "_profile=http://profile/a"},
	{"Patient", "phone=555-1234"}, {"Patient", "email=p@x.org"}, {"Patient", "telecom=555-1234"},
	{"Observation", "code=http://loinc.org|29463-7"}, {"Observation", "code=29463-7"}, {"Observation", "code=http://loinc.org|"},
	{"Observation", "code:text=weight"}, {"Observation", "code:text=body"}, {"Observation", "code:text=happy"}, {"Observation", "code:missing=true"},
	{"Observation", "status=final,amended"}, {"Observation", "status:not=final"}, {"Observation", "value-concept=http://snomed.info/sct|1"},
	{"Encounter", "class=IMP"}, {"Encounter", "class=http://act|IMP"}, {"Encounter", "status=finished"},
	// _id, _lastUpdated
	{"Patient", "_id=p01"}, {"Patient", "_id=p01,p02,zzz"}, {"Patient", "_id:not=p01"}, {"Patient", "_id=gone"},
	{"Patient", "_lastUpdated=gt2000-01-01"}, {"Patient", "_lastUpdated=lt2000-01-01"}, {"Patient", "_lastUpdated:missing=false"},
	// reference
	{"Patient", "organization=Organization/o1"}, {"Patient", "organization=o1"}, {"Patient", "organization=o2"}, {"Patient", "organization:Organization=o1"},
	{"Patient", "organization:missing=true"}, {"Patient", "general-practitioner=Practitioner/dr1"}, {"Patient", "general-practitioner=dr2"},
	{"Patient", "general-practitioner=https://example.org/fhir/Practitioner/dr1/_history/2"}, {"Patient", "link=Patient/p01"},
	{"Observation", "subject=Patient/p01"}, {"Observation", "subject=p01"}, {"Observation", "patient=p01"}, {"Observation", "patient=p02,p03"},
	{"Observation", "subject:missing=true"}, {"Observation", "performer=Practitioner/dr1"}, {"Encounter", "subject=p01"},
	{"Organization", "partof=Organization/o1"},
	// date
	{"Patient", "birthdate=1974-12-25"}, {"Patient", "birthdate=1974"}, {"Patient", "birthdate=1990"}, {"Patient", "birthdate=1990-03"},
	{"Patient", "birthdate=ne1974-12-25"}, {"Patient", "birthdate=gt1980"}, {"Patient", "birthdate=ge1974-12-25"}, {"Patient", "birthdate=lt1980"},
	{"Patient", "birthdate=le1974-12-24"}, {"Patient", "birthdate=sa1985"}, {"Patient", "birthdate=eb1985"}, {"Patient", "birthdate=ap1974"},
	{"Patient", "birthdate=gt1970&birthdate=lt1990"}, {"Patient", "birthdate=1974,1990"}, {"Patient", "birthdate:missing=true"},
	{"Patient", "birthdate=2001-07-04"}, {"Patient", "birthdate=2001-07-04T03:00:00Z"}, {"Patient", "death-date:missing=false"},
	{"Observation", "date=2016-03-28"}, {"Observation", "date=2016-03-28T08:30:00Z"}, {"Observation", "date=lt2016-03-28T08:00:00Z"},
	{"Observation", "date=gt2016"}, {"Observation", "date=2020-05"}, {"Observation", "date=2020-05-05"}, {"Observation", "date=gt2020-05-05"},
	{"Observation", "date=lt2020-05-05"}, {"Observation", "date=2024-11-30T23:59:59.123Z"}, {"Observation", "date=2024-11-30"},
	{"Encounter", "date=2020-05"}, {"Encounter", "date=2020-05-05"}, {"Encounter", "date=ge2021-01-01"}, {"Encounter", "date=gt2022-01-01"},
	// quantity và number
	{"Observation", "value-quantity=185"}, {"Observation", "value-quantity=gt180"}, {"Observation", "value-quantity=lt180"},
	{"Observation", "value-quantity=185|http://unitsofmeasure.org|[lb_av]"}, {"Observation", "value-quantity=185||[lb_av]"},
	{"Observation", "value-quantity=185||kg"}, {"Observation", "value-quantity=185||lbs"}, {"Observation", "value-quantity=70.5"},
	{"Observation", "value-quantity=70.4"}, {"Observation", "value-quantity=ne185"}, {"Observation", "value-quantity=ap180"},
	{"Observation", "value-quantity=1.85e2"}, {"Observation", "value-quantity=ge70.5&value-quantity=le185"}, {"Observation", "value-quantity:missing=true"},
	{"Observation", "value-string=abc"}, {"Observation", "value-string=ab"}, {"Observation", "value-string:exact=ABC"},
	// uri
	{"ValueSet", "url=http://example.org/fhir/ValueSet/a"}, {"ValueSet", "url:below=http://example.org/fhir/ValueSet/a"},
	{"ValueSet", "url:above=http://example.org/fhir/ValueSet/a/b/c"}, {"ValueSet", "url:below=http://example.org"}, {"ValueSet", "status=active"},
	{"ValueSet", "name=b"}, {"ValueSet", "url:missing=false"},
	// sort + phân trang + total
	{"Patient", "_sort=birthdate"}, {"Patient", "_sort=-birthdate"}, {"Patient", "_sort=family"}, {"Patient", "_sort=-family"}, {"Patient", "_sort=given"},
	{"Patient", "_sort=gender,-birthdate"}, {"Patient", "_sort=gender,birthdate"}, {"Patient", "_sort=_id"}, {"Patient", "_sort=-_id"},
	{"Patient", "_sort=name"}, {"Patient", "_sort=organization"}, {"Patient", "_sort=identifier"}, {"Patient", "_sort=address-city"},
	{"Patient", "_sort=birthdate&_count=2"}, {"Patient", "_sort=birthdate&_count=2&_offset=2"}, {"Patient", "_sort=birthdate&_count=2&_offset=4&_total=accurate"},
	{"Patient", "_count=3"}, {"Patient", "_count=3&_offset=3"}, {"Patient", "_count=0&_total=accurate"}, {"Patient", "_total=accurate"},
	{"Patient", "_offset=100"}, {"Patient", "_total=estimate&gender=male"}, {"Patient", "_summary=count"},
	{"Observation", "_sort=date"}, {"Observation", "_sort=-date"}, {"Observation", "_sort=value-quantity"}, {"Observation", "_sort=-value-quantity"},
	{"Observation", "_sort=code"}, {"Observation", "_sort=subject,-date"}, {"Observation", "_sort=status,_id"}, {"Observation", "_sort=_lastUpdated"},
	{"Encounter", "_sort=date"}, {"Encounter", "_sort=-date"}, {"ValueSet", "_sort=url"}, {"ValueSet", "_sort=-name"},
	// include / revinclude
	{"Observation", "_include=Observation:subject"}, {"Observation", "_include=Observation:subject:Patient"}, {"Observation", "_include=Observation:subject&_include=Observation:performer"},
	{"Patient", "_include=Patient:organization"}, {"Patient", "_include=Patient:organization&_include=Patient:general-practitioner"},
	{"Patient", "_revinclude=Observation:subject"}, {"Patient", "_revinclude=Observation:patient&_revinclude=Encounter:subject"},
	{"Patient", "gender=male&_revinclude=Observation:subject&_include=Patient:organization"}, {"Organization", "_revinclude=Patient:organization"},
	{"Organization", "_revinclude=Organization:partof"}, {"Observation", "_include=*:subject"},
	{"Patient", "_count=1&_revinclude=Observation:subject"}, {"Observation", "_include=Observation:nope"},
	// search hệ thống
	{"", "_type=Patient,Observation"}, {"", "_type=Observation,Patient&_sort=_id"}, {"", "_type=Patient&_id=p01"}, {"", "_type=Patient,Organization&name=burgers"},
	{"", "_type=Patient,Observation&_id=p01,ob01"}, {"", "_type=Patient,Organization,Encounter&_count=4&_total=accurate"},
	{"", "_type=Observation,Encounter&subject=p01"}, {"", "_type=Patient,ValueSet&_tag=vip"},
	// kết hợp
	{"Patient", "gender=male&birthdate=ge1980&_sort=-birthdate"}, {"Patient", "family=chal&given=anna"}, {"Patient", "name=chal&gender=female"},
	{"Observation", "subject=p01&status=final&date=ge2016"}, {"Observation", "patient=p01&code=29463-7&value-quantity=gt100"},
	{"Observation", "status=final&_sort=-date&_count=2&_total=accurate"}, {"Patient", "gender:not=male&birthdate:missing=false"},
	// composite: thành phần phải khớp trên cùng một phần tử
	{"Observation", "component-code-value-quantity=http://loinc.org|8480-6$gt130"}, {"Observation", "component-code-value-quantity=8480-6$lt130"},
	{"Observation", "component-code-value-quantity=8462-4$gt100"}, {"Observation", "component-code-value-quantity=8480-6$gt100&component-code-value-quantity=8462-4$gt100"},
	{"Observation", "component-code-value-quantity=8480-6$gt130,8462-4$gt100&_sort=-_lastUpdated"}, {"Observation", "component-code-value-quantity=8480-6$140||mm[Hg]"},
	{"Observation", "component-code-value-quantity=8480-6$140|http://unitsofmeasure.org|mm[Hg]"}, {"Observation", "component-code-value-quantity=8480-6$140|http://x|mm[Hg]"},
	{"Observation", "component-code-value-quantity=http://other|8480-6$gt100"}, {"Observation", "component-code-value-quantity=|8480-6$gt100"},
	{"Observation", "component-code-value-string=note$dizz"}, {"Observation", "component-code-value-string=note$ok"}, {"Observation", "component-code-value-concept=8310-5$http://snomed.info/sct|1"},
	{"Observation", "code-value-quantity=85354-9$ge120"}, {"Observation", "code-value-quantity=85354-9$gt120"}, {"Observation", "code-value-quantity=29463-7$gt100&_sort=_id"},
	{"Observation", "code-value-quantity=8480-6$gt100"}, {"Observation", "code-value-string=8302-2$tall"}, {"Observation", "code-value-string=8302-2$short"},
	{"Observation", "code-value-concept=Mood$1"}, {"Observation", "code-value-date=8302-2$2022-02-02"}, {"Observation", "code-value-date=8302-2$gt2022-01-01"},
	{"Observation", "combo-code-value-quantity=8480-6$gt100"}, {"Observation", "combo-code-value-quantity=85354-9$120"}, {"Observation", "combo-code-value-quantity=8462-4$gt100&status=final"},
	{"Observation", "combo-code-value-concept=8310-5$1"}, {"Observation", "combo-code-value-quantity=29463-7$185||[lb_av]"},
	{"Observation", "component-code-value-quantity=8480-6$gt130&subject:Patient.family=chalmers"}, {"Patient", "_has:Observation:patient:component-code-value-quantity=8480-6$gt130"},
	{"", "component-code-value-quantity=8480-6$gt130&_type=Observation"},
	// _filter: biểu thức logic (AND/OR/NOT), các toán tử theo kiểu tham số
	{"Patient", "_filter=family%20co%20%22alm%22"}, {"Patient", "_filter=family%20eq%20%22Chalmers%22"}, {"Patient", "_filter=family%20ne%20%22Chalmers%22"},
	{"Patient", "_filter=family%20sw%20%22chal%22%20and%20gender%20eq%20male"}, {"Patient", "_filter=gender%20eq%20male%20or%20gender%20eq%20female"},
	{"Patient", "_filter=not%20(gender%20eq%20male)"}, {"Patient", "_filter=not%20(gender%20eq%20male%20or%20family%20co%20%22mit%22)"},
	{"Patient", "_filter=(gender%20eq%20male%20and%20birthdate%20lt%201980)%20or%20family%20eq%20%22%C4%90%E1%BA%B7ng%22"},
	{"Patient", "_filter=birthdate%20gt%201980%20and%20birthdate%20le%202001-07-04"}, {"Patient", "_filter=birthdate%20ne%201974-12-25"},
	{"Patient", "_filter=birthdate%20pr%20true"}, {"Patient", "_filter=birthdate%20pr%20false"}, {"Patient", "_filter=email%20pr%20true%20and%20gender%20eq%20male"},
	{"Patient", "_filter=identifier%20eq%20http://hospital/mrn%7C12345"}, {"Patient", "_filter=identifier%20ne%20http://hospital/mrn%7C12345"},
	{"Patient", "_filter=organization%20eq%20Organization/o1%20or%20organization%20eq%20Organization/o2"}, {"Patient", "_filter=active%20eq%20true%20and%20not%20(gender%20eq%20female)"},
	{"Patient", "gender=male&_filter=family%20co%20%22a%22&_sort=family&_count=1&_total=accurate"}, {"Patient", "_filter=gender%20eq%20male&_filter=family%20co%20%22s%22"},
	{"Observation", "_filter=status%20eq%20final%20and%20code%20eq%20http://loinc.org%7C29463-7"}, {"Observation", "_filter=value-quantity%20gt%2070%20or%20status%20eq%20amended"},
	{"Observation", "_filter=not%20(status%20eq%20final)&_sort=_id"}, {"Observation", "_filter=date%20ge%202016-03-28%20and%20date%20lt%202020-01-01"},
	{"Organization", "_filter=name%20co%20%22hoc%22%20or%20name%20sw%20%22Burg%22"}, {"ValueSet", "_filter=url%20sw%20http://example.org/fhir/ValueSet/a"},
	{"Patient", "_filter=family%20co%20%22a%22&_has:Observation:patient:code=29463-7"},
	// lỗi: cả hai phải báo lỗi
	{"Patient", "_filter=family%20ew%20%22a%22"}, {"Patient", "_filter=nope%20eq%201"}, {"Patient", "_filter=(family%20eq%20%22a%22"}, {"", "_type=Patient&_filter=gender%20eq%20male"},
	{"Observation", "component-code-value-quantity=8480-6"}, {"Observation", "component-code-value-quantity=a$b$c"}, {"Observation", "component-code-value-quantity:missing=true"},
	{"Observation", "component-code-value-quantity=8480-6$gtx"}, {"Observation", "component-code-value-quantity:not=8480-6$gt1"}, {"Observation", "_sort=component-code-value-quantity"},
	{"Patient", "nope=1"}, {"Patient", "family:bogus=x"}, {"Patient", "_sort=nope"}, {"Patient", "birthdate=notadate"}, {"Observation", "value-quantity=abc"},
	{"Observation", "combo-code-value-quantity=x"}, {"Patient", "name.family=x"}, {"Patient", "_has:Observation:code=x"}, {"Patient", "_has:Nope:patient:code=x"},
	// chained search và _has (giải bằng sub-search ở fhirrouter, cùng kết quả trên mọi repository)
	{"Observation", "subject:Patient.family=chalmers"}, {"Observation", "patient.family=chalmers"}, {"Observation", "subject:Patient.gender=male&status=final"},
	{"Observation", "subject:Patient.family=nobody"}, {"Observation", "subject:Patient.family=chalmers,levin&_sort=-date"},
	{"Patient", "organization.name=burgers"}, {"Patient", "organization:Organization.name:contains=medical&_total=accurate"},
	{"Patient", "_has:Observation:patient:code=29463-7"}, {"Patient", "_has:Observation:subject:status=final&gender=male"},
	{"Patient", "_has:Observation:patient:code=nothing"}, {"Patient", "_has:Observation:patient:status=final,preliminary&_sort=-birthdate"},
	{"Encounter", "subject:Patient.gender=male"}, {"Observation", "encounter.subject:Patient.family=chalmers"},
	{"Patient", "_has:Observation:patient:subject:Patient.gender=male"}, {"Patient", "organization.name=burgers&gender=male&_has:Observation:patient:code=29463-7"},
}

func ids(rs []fhir.Resource) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.GetResourceType() + "/" + r.GetID()
	}
	return strings.Join(parts, ",")
}

func TestDifferentialAgainstMemory(t *testing.T) {
	runDifferential(t, newSQLite(t))
}

// runDifferential nạp cùng dữ liệu vào MemoryRepository và store rồi so sánh mọi truy vấn.
func runDifferential(t *testing.T, store *fhirstore.Store) {
	ctx := context.Background()
	mem := fhirrouter.NewMemoryRepository()
	load(t, ctx, mem)
	load(t, ctx, store)

	diffs := 0
	for _, q := range queries {
		name := q[0] + "?" + q[1]
		t.Run(name, func(t *testing.T) {
			req, perr := fhirsearch.ParseQuery(q[0], q[1])
			if perr != nil {
				return // lỗi phân tích do fhirsearch, dùng chung cho cả hai repository
			}
			mres, merr := fhirrouter.SearchResolved(ctx, mem, req)
			sres, serr := fhirrouter.SearchResolved(ctx, store, req)
			if (merr == nil) != (serr == nil) {
				diffs++
				t.Fatalf("lỗi khác nhau: memory=%v sql=%v", merr, serr)
			}
			if merr != nil {
				return
			}
			var problems []string
			if a, b := ids(mres.Matches), ids(sres.Matches); a != b {
				problems = append(problems, fmt.Sprintf("matches:\n  memory: %s\n  sql:    %s", a, b))
			}
			if a, b := ids(mres.Included), ids(sres.Included); a != b {
				problems = append(problems, fmt.Sprintf("included:\n  memory: %s\n  sql:    %s", a, b))
			}
			if (mres.Total == nil) != (sres.Total == nil) || (mres.Total != nil && *mres.Total != *sres.Total) {
				problems = append(problems, fmt.Sprintf("total: memory=%v sql=%v", deref(mres.Total), deref(sres.Total)))
			}
			if mres.More != sres.More {
				problems = append(problems, fmt.Sprintf("more: memory=%v sql=%v", mres.More, sres.More))
			}
			if len(problems) > 0 {
				diffs++
				t.Error(strings.Join(problems, "\n"))
			}
		})
	}
	t.Logf("%d truy vấn, %d khác biệt", len(queries), diffs)
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
