package fhirsearch_test

import (
	"encoding/json"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirsearch"
)

func TestFilterExprMatch(t *testing.T) {
	people := map[string]string{
		"smith": `{"resourceType":"Patient","gender":"male","birthDate":"1970-05-01","name":[{"family":"Smith","given":["John"]}],"telecom":[{"system":"email","value":"s@x.org"}]}`,
		"jones": `{"resourceType":"Patient","gender":"female","birthDate":"1990-02-03","name":[{"family":"Jones","given":["Ann"]}]}`,
		"lee":   `{"resourceType":"Patient","gender":"male","birthDate":"1985-07-07","name":[{"family":"Leeson"}]}`,
	}
	cases := []struct {
		q    string
		want string // các khóa khớp, sắp xếp
	}{
		{`name co "mit"`, "smith"},
		{`name sw "Jon"`, "jones"},
		{`name eq "Smith"`, "smith"},
		{`name eq "smith"`, ""}, // eq của string phân biệt hoa thường
		{`name ne "Smith"`, "jones,lee"},
		{`gender eq male`, "lee,smith"},
		{`gender ne male`, "jones"},
		{`gender eq male and birthdate lt 1980-01-01`, "smith"},
		{`gender eq female or name co "mit"`, "jones,smith"},
		{`not (gender eq male)`, "jones"},
		{`NOT (gender eq male) OR name sw Lee`, "jones,lee"},
		{`(gender eq male and birthdate ge 1980) or name eq "Jones"`, "jones,lee"},
		{`gender eq male and (name co "mit" or name co "lee")`, "lee,smith"},
		{`birthdate gt 1985-01-01`, "jones,lee"},
		{`birthdate le 1985-07-07 and gender eq male`, "lee,smith"},
		{`birthdate eb 1980 and email pr true`, "smith"},
		{`email pr false`, "jones,lee"},
		{`name eq "Sm\"ith"`, ""},                                               // escape trong chuỗi
		{`name co "mit" and name co "Smi" and not (gender eq female)`, "smith"}, // nhiều nhánh
	}
	for _, c := range cases {
		req, err := fhirsearch.ParseQuery("Patient", "_filter="+urlEscape(c.q))
		if err != nil {
			t.Errorf("%s: %v", c.q, err)
			continue
		}
		var got []string
		for _, k := range []string{"jones", "lee", "smith"} {
			var doc map[string]any
			if err := json.Unmarshal([]byte(people[k]), &doc); err != nil {
				t.Fatal(err)
			}
			ok, err := fhirsearch.Match(doc, req)
			if err != nil {
				t.Errorf("%s: %v", c.q, err)
			}
			if ok {
				got = append(got, k)
			}
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("%s = [%s], muốn [%s]", c.q, strings.Join(got, ","), c.want)
		}
	}
	// _filter AND với tham số thường và với _filter khác; Query() giữ nguyên để phân trang
	req, err := fhirsearch.ParseQuery("Patient", "gender=male&_filter="+urlEscape(`name co "mit"`)+"&_filter="+urlEscape(`birthdate lt 1980`))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Exprs) != 2 || !req.HasCriteria() {
		t.Fatalf("Exprs = %d", len(req.Exprs))
	}
	if got := req.Query(0)["_filter"]; len(got) != 2 || got[0] != `name co "mit"` {
		t.Errorf("Query: %v", got)
	}
}

func urlEscape(s string) string {
	return strings.NewReplacer(" ", "%20", `"`, "%22", "(", "%28", ")", "%29", "|", "%7C").Replace(s)
}

func TestFilterExprErrors(t *testing.T) {
	for name, q := range map[string]string{
		"rỗng":                   ``,
		"thiếu giá trị":          `name eq`,
		"toán tử lạ":             `name zz "x"`,
		"ew chưa hỗ trợ":         `name ew "x"`,
		"in chưa hỗ trợ":         `gender in http://vs`,
		"re chưa hỗ trợ":         `name re "x.*"`,
		"co trên token":          `gender co "x"`,
		"gt trên string":         `name gt "x"`,
		"tham số lạ":             `nope eq 1`,
		"modifier trong tham số": `name:exact eq "x"`,
		"đường dẫn chained":      `subject.name eq "x"`,
		"composite":              `code-value-quantity eq 1`,
		"pr không phải boolean":  `name pr maybe`,
		"thiếu ngoặc đóng":       `(name eq "x"`,
		"thừa ngoặc đóng":        `name eq "x")`,
		"not không có ngoặc":     `not name eq "x"`,
		"and thiếu vế":           `name eq "x" and`,
		"chuỗi không đóng":       `name eq "x`,
		"thừa token":             `name eq "x" gender eq male`,
		"quá nhiều điều kiện":    strings.TrimSuffix(strings.Repeat(`name eq "x" or `, 51), ` or `),
		"quá sâu":                strings.Repeat("(", 14) + `name eq "x"` + strings.Repeat(")", 14),
		"ngày sai":               `birthdate gt not-a-date`,
	} {
		if _, err := fhirsearch.ParseQuery("Patient", "_filter="+urlEscape(q)); err == nil {
			// ngày sai chỉ lộ ra khi đối sánh
			if name == "ngày sai" {
				req, _ := fhirsearch.ParseQuery("Patient", "_filter="+urlEscape(q))
				if _, err := fhirsearch.Match(map[string]any{"resourceType": "Patient", "birthDate": "2000"}, req); err == nil {
					t.Errorf("%s: muốn lỗi khi đối sánh", name)
				}
				continue
			}
			t.Errorf("%s: muốn lỗi", name)
		}
	}
	// search hệ thống không có loại resource
	if _, err := fhirsearch.ParseQuery("", "_type=Patient&_filter="+urlEscape(`gender eq male`)); err == nil {
		t.Error("_filter ở search hệ thống phải bị từ chối")
	}
}
