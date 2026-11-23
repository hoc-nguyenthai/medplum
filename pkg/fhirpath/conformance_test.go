package fhirpath_test

import (
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhirpath"
)

// Bộ test chuẩn HL7 FHIRPath (tests-fhir-r4.xml, public domain).
// Các ca đã biết là chưa đạt nằm trong testdata/known-failures.txt (kèm lý do);
// test fail nếu có ca mới hỏng, hoặc nếu một ca "đã biết" nay đã đạt (để dọn danh sách).
// Chạy với UPDATE_KNOWN=1 để ghi lại danh sách từ kết quả hiện tại.

type output struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

type testCase struct {
	Name       string `xml:"name,attr"`
	InputFile  string `xml:"inputfile,attr"`
	Predicate  string `xml:"predicate,attr"`
	Mode       string `xml:"mode,attr"`
	Expression struct {
		Invalid string `xml:"invalid,attr"`
		Text    string `xml:",chardata"`
	} `xml:"expression"`
	Outputs []output `xml:"output"`
}

type suite struct {
	Groups []struct {
		Name  string     `xml:"name,attr"`
		Tests []testCase `xml:"test"`
	} `xml:"group"`
}

func loadKnown() map[string]string {
	known := map[string]string{}
	raw, err := os.ReadFile("testdata/known-failures.txt")
	if err != nil {
		return known
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, _ := strings.Cut(line, "\t")
		known[name] = reason
	}
	return known
}

func TestConformance(t *testing.T) {
	raw, err := os.ReadFile("testdata/tests-fhir-r4.xml")
	if err != nil {
		t.Fatal(err)
	}
	var s suite
	if err := xml.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]any{}
	input := func(file string) any {
		if v, ok := inputs[file]; ok {
			return v
		}
		b, err := os.ReadFile("testdata/" + strings.TrimSuffix(file, ".xml") + ".json")
		if err != nil {
			b, err = os.ReadFile("testdata/valueset-example-expansion.json")
		}
		if err != nil {
			t.Fatal(err)
		}
		v, err := fhirpath.Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		inputs[file] = v
		return v
	}
	known := loadKnown()
	fixedNow := func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	update := os.Getenv("UPDATE_KNOWN") != ""

	total, passed := 0, 0
	seen := map[string]int{}
	var failed, fixed, newKnown []string
	for _, g := range s.Groups {
		for _, tc := range g.Tests {
			total++
			id := g.Name + "/" + tc.Name
			if seen[id]++; seen[id] > 1 {
				id += "#" + strconv.Itoa(seen[id]) // tên trùng trong bộ test
			}
			msg := runCase(input(tc.InputFile), tc, fixedNow)
			_, isKnown := known[id]
			switch {
			case msg == "":
				passed++
				if isKnown {
					fixed = append(fixed, id)
				}
			case update:
				newKnown = append(newKnown, id+"\t"+strings.TrimSpace(tc.Expression.Text)+" => "+msg)
			case !isKnown:
				failed = append(failed, id+"\t"+strings.TrimSpace(tc.Expression.Text)+"\t=> "+msg)
			}
		}
	}
	if update {
		sort.Strings(newKnown)
		body := "# Ca FHIRPath chưa đạt (tên<TAB>biểu thức => lý do). Sinh bởi UPDATE_KNOWN=1.\n" + strings.Join(newKnown, "\n") + "\n"
		if err := os.WriteFile("testdata/known-failures.txt", []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("đã ghi %d ca chưa đạt trên %d", len(newKnown), total)
		return
	}
	t.Logf("FHIRPath conformance: %d/%d đạt, %d ca đã biết chưa đạt", passed, total, total-passed)
	sort.Strings(failed)
	for _, f := range failed {
		t.Errorf("hỏng: %s", f)
	}
	for _, f := range fixed {
		t.Errorf("ca %s đã đạt, hãy xóa khỏi testdata/known-failures.txt", f)
	}
}

// runCase trả về chuỗi rỗng nếu đạt, ngược lại là lý do.
func runCase(input any, tc testCase, now func() time.Time) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = "panic: " + fmt.Sprint(r)
		}
	}()
	expr := strings.TrimSpace(tc.Expression.Text)
	invalid := tc.Expression.Invalid
	res, err := fhirpath.Evaluate(expr, input, &fhirpath.Options{Strict: tc.Mode == "strict" || invalid != "", Now: now})
	if invalid != "" {
		if err == nil {
			return "muốn lỗi (" + invalid + "), nhưng được " + show(res)
		}
		return ""
	}
	if err != nil {
		return "lỗi: " + err.Error()
	}
	if tc.Predicate == "true" {
		want := len(tc.Outputs) == 1 && strings.TrimSpace(tc.Outputs[0].Text) == "true"
		got := false
		if len(res) == 1 {
			if b, ok := res[0].V.(bool); ok {
				got = b
			} else {
				got = true
			}
		} else if len(res) > 1 {
			got = true
		}
		if got != want {
			return "predicate: muốn " + strconv.FormatBool(want) + ", được " + show(res)
		}
		return ""
	}
	if len(res) != len(tc.Outputs) {
		return "số phần tử: muốn " + strconv.Itoa(len(tc.Outputs)) + ", được " + show(res)
	}
	for i, o := range tc.Outputs {
		if !same(res[i], strings.TrimSpace(o.Type), strings.TrimSpace(o.Text)) {
			return "phần tử " + strconv.Itoa(i) + ": muốn " + strings.TrimSpace(o.Text) + " (" + o.Type + "), được " + show(res)
		}
	}
	return ""
}

func same(v fhirpath.Value, typ, text string) bool {
	switch typ {
	case "boolean":
		b, ok := v.V.(bool)
		return ok && strconv.FormatBool(b) == text
	case "integer":
		n, ok := v.V.(int64)
		return ok && strconv.FormatInt(n, 10) == text
	case "decimal":
		var f float64
		switch x := v.V.(type) {
		case float64:
			f = x
		case int64:
			f = float64(x)
		default:
			return false
		}
		w, err := strconv.ParseFloat(text, 64)
		return err == nil && math.Abs(f-w) < 1e-9
	case "Quantity":
		q, ok := v.V.(fhirpath.Quantity)
		return ok && strings.Contains(text, strconv.FormatFloat(q.Value, 'f', -1, 64))
	}
	s, ok := v.V.(string)
	return ok && s == text
}

func show(res []fhirpath.Value) string {
	parts := make([]string, len(res))
	for i, v := range res {
		switch x := v.V.(type) {
		case map[string]any:
			parts[i] = "{object}:" + v.T
		default:
			parts[i] = fmt.Sprintf("%v:%s", x, v.T)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
