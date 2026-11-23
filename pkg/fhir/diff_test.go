package fhir_test

import (
	"encoding/json"
	"testing"

	"trivita-fhir/pkg/fhir"
)

// Bảo đảm bài so sánh round-trip không "xanh giả": trường lạ phải bị phát hiện,
// còn các trường nguyên thủy, phần mở rộng, choice type, false và số thập phân phải giữ nguyên.
func TestDiffDetectsLoss(t *testing.T) {
	raw := []byte(`{"resourceType":"Patient","id":"1","bogusField":"x",
	 "name":[{"family":"A","given":["B"],"_family":{"id":"e"}}],
	 "multipleBirthInteger":2,"deceasedBoolean":false,
	 "extension":[{"url":"u","valueDecimal":1.50}]}`)
	r, err := fhir.UnmarshalResource(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var diffs []string
	diff("x", decode(t, raw), decode(t, out), &diffs, 10)
	if len(diffs) != 1 || diffs[0] != "x.bogusField: mất sau khi ghi lại" {
		t.Fatalf("khác biệt bất ngờ: %v\nout=%s", diffs, out)
	}
}

func TestUnmarshalErrors(t *testing.T) {
	for name, in := range map[string]string{
		"thiếu resourceType": `{"id":"1"}`,
		"resourceType lạ":    `{"resourceType":"Nope"}`,
		"JSON hỏng":          `{`,
		"sai kiểu trường":    `{"resourceType":"Patient","active":"yes"}`,
	} {
		if _, err := fhir.UnmarshalResource([]byte(in)); err == nil {
			t.Errorf("%s: muốn lỗi", name)
		}
	}
}
