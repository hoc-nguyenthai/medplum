package fhirauth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Ảnh chụp quyền của job do bản cũ ký (Perm hai bit gộp, không có trường v) phải bị từ chối: nếu không, bit 1 và 2 cũ sẽ bị đọc
// thành "create" và "read" của bản mới.
func TestSealOpenRejectsOldPayloadVersion(t *testing.T) {
	sl, err := NewSealer([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(map[string]any{"sub": "u", "grants": map[string]int{"Patient": 3}, "policy": nil}) // không có "v"
	sealed := base64.RawURLEncoding.EncodeToString(old) + "." + base64.RawURLEncoding.EncodeToString(sl.mac("job1", "p1", "req", old))
	if _, err := sl.Open("job1", "p1", "req", sealed); err == nil || !strings.Contains(err.Error(), "phiên bản cũ") {
		t.Fatalf("payload cũ phải bị từ chối: %v", err)
	}
	// bản mới ký và mở bình thường
	p := &Principal{Subject: "u", Project: "p1", Grants: Grants{"Patient": PermCreate}}
	got, err := sl.Open("job1", "p1", "req", sl.Seal("job1", "req", p))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Grants.Can("Patient", PermCreate) || got.Grants.Can("Patient", PermRead) {
		t.Errorf("%v", got.Grants)
	}
	// version khác (tương lai) cũng bị từ chối
	future, _ := json.Marshal(map[string]any{"v": sealVersion + 1, "sub": "u", "grants": map[string]int{}, "policy": nil})
	sealed = base64.RawURLEncoding.EncodeToString(future) + "." + base64.RawURLEncoding.EncodeToString(sl.mac("job1", "p1", "req", future))
	if _, err := sl.Open("job1", "p1", "req", sealed); err == nil {
		t.Error("phiên bản lạ phải bị từ chối")
	}
}
