package fhirsearch_test

import (
	"encoding/json"
	"testing"

	"trivita-fhir/pkg/fhirsearch"
)

const bpObs = `{"resourceType":"Observation","status":"final",
 "code":{"coding":[{"system":"http://loinc.org","code":"85354-9"}]},
 "valueQuantity":{"value":120,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"},
 "component":[
  {"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":140,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"}},
  {"code":{"coding":[{"system":"http://loinc.org","code":"8462-4"}]},"valueQuantity":{"value":90,"unit":"mmHg","system":"http://unitsofmeasure.org","code":"mm[Hg]"}}
 ]}`

func TestCompositeMatch(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(bpObs), &doc); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		q    string
		want bool
	}{
		// thành phần phải khớp trên CÙNG một phần tử
		{"component-code-value-quantity=http://loinc.org|8480-6$gt130", true},
		{"component-code-value-quantity=http://loinc.org|8480-6$lt130", false}, // 8480-6 có 140, còn 90 là của 8462-4
		{"component-code-value-quantity=http://loinc.org|8462-4$lt100", true},
		{"component-code-value-quantity=8462-4$gt100", false},
		{"component-code-value-quantity=8480-6$140||mm[Hg]", true},
		// OR giữa các giá trị
		{"component-code-value-quantity=8480-6$lt100,8462-4$lt100", true},
		{"component-code-value-quantity=8480-6$lt100,8462-4$gt100", false},
		// code và value ở mức resource
		{"code-value-quantity=85354-9$ge120", true},
		{"code-value-quantity=85354-9$gt120", false},
		{"code-value-quantity=8480-6$140", false},
		// combo gồm cả resource lẫn component
		{"combo-code-value-quantity=8480-6$gt100", true},
		{"combo-code-value-quantity=85354-9$120", true},
		{"combo-code-value-quantity=8462-4$gt100", false},
		// kết hợp với tham số thường
		{"status=final&component-code-value-quantity=8480-6$gt130", true},
		{"status=amended&component-code-value-quantity=8480-6$gt130", false},
	}
	for _, c := range cases {
		req, err := fhirsearch.ParseQuery("Observation", c.q)
		if err != nil {
			t.Errorf("%s: %v", c.q, err)
			continue
		}
		got, err := fhirsearch.Match(doc, req)
		if err != nil || got != c.want {
			t.Errorf("%s: %v %v, muốn %v", c.q, got, err, c.want)
		}
	}
}

func TestCompositeParseErrors(t *testing.T) {
	for _, q := range []string{
		"component-code-value-quantity=8480-6",           // thiếu thành phần
		"component-code-value-quantity=a$b$c",            // thừa thành phần
		"component-code-value-quantity:missing=true",     // không hỗ trợ modifier
		"component-code-value-quantity:not=8480-6$gt100", // không hỗ trợ modifier
		"component-code-value-quantity=8480-6$gtx",       // giá trị thành phần sai
		"code-value-date=85354-9$not-a-date",
	} {
		if _, err := fhirsearch.ParseQuery("Observation", q); err == nil {
			t.Errorf("%s: muốn lỗi", q)
		}
	}
	// Query() giữ nguyên tham số composite (link phân trang)
	req, err := fhirsearch.ParseQuery("Observation", "code-value-quantity=85354-9$gt1")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Query(0).Get("code-value-quantity"); got != "85354-9$gt1" {
		t.Errorf("Query: %q", got)
	}
}
