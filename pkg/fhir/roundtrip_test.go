package fhir_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
)

// diff trả về các khác biệt (tối đa max) giữa hai cây JSON đã giải mã bằng UseNumber.
func diff(path string, a, b any, out *[]string, max int) {
	if len(*out) >= max {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, path+": object != "+fmt.Sprintf("%T", b))
			return
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			x, okA := av[k]
			y, okB := bv[k]
			switch {
			case !okB:
				*out = append(*out, fmt.Sprintf("%s.%s: mất sau khi ghi lại", path, k))
			case !okA:
				*out = append(*out, fmt.Sprintf("%s.%s: xuất hiện thêm", path, k))
			default:
				diff(path+"."+k, x, y, out, max)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			*out = append(*out, path+": độ dài mảng khác")
			return
		}
		for i := range av {
			diff(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i], out, max)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, fmt.Sprintf("%s: %v != %v", path, a, b))
		}
	}
}

func decode(t *testing.T, data []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// roundTripResource đọc một resource, ghi lại và so sánh với bản gốc.
func roundTripResource(t *testing.T, raw json.RawMessage, label string) {
	t.Helper()
	r, err := fhir.UnmarshalResource(raw)
	if err != nil {
		t.Errorf("%s: %v", label, err)
		return
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Errorf("%s: %v", label, err)
		return
	}
	var diffs []string
	diff(label, decode(t, raw), decode(t, out), &diffs, 5)
	for _, d := range diffs {
		t.Errorf("%s", d)
	}
}

func roundTripFile(t *testing.T, file string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		ResourceType string `json:"resourceType"`
		Entry        []struct {
			Resource json.RawMessage `json:"resource"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	roundTripResource(t, raw, filepath.Base(file))
	_ = probe
}

func TestRoundTripFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.json")
	if len(files) == 0 {
		t.Fatal("không có fixture")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) { roundTripFile(t, f) })
	}
}

// TestRoundTripDefinitions chạy trên các file định nghĩa lớn của Medplum nếu có (không bắt buộc).
func TestRoundTripDefinitions(t *testing.T) {
	dir := "../../medplum/packages/definitions/src/fhir/r4"
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		t.Skip("không có medplum/ để lấy dữ liệu định nghĩa")
	}
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, "fhir.schema") || base == "dataelements.json" {
			continue
		}
		t.Run(base, func(t *testing.T) { roundTripFile(t, f) })
	}
}

func TestPatientTyped(t *testing.T) {
	raw, err := os.ReadFile("testdata/patient-example.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := fhir.UnmarshalResource(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := r.(*fhir.Patient)
	if !ok {
		t.Fatalf("kiểu %T, muốn *fhir.Patient", r)
	}
	if p.GetResourceType() != "Patient" || p.GetID() == "" || len(p.Name) == 0 {
		t.Fatalf("patient đọc chưa đúng: %+v", p)
	}
}
