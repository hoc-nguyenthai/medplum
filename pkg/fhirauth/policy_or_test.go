package fhirauth_test

import (
	"fmt"
	"slices"
	"sort"
	"testing"

	"trivita-fhir/pkg/fhirauth"
)

// Nhiều rule cùng loại là OR (P8 M2): ví dụ Communication mà bệnh nhân là người gửi HOẶC người nhận.

func TestParsePolicyAlternatives(t *testing.T) {
	comm := func(c string) map[string]any { return map[string]any{"resourceType": "Communication", "criteria": c} }
	if _, err := fhirauth.ParsePolicy([]any{comm("sender=Patient/1"), comm("recipient=Patient/1")}); err != nil {
		t.Errorf("hai rule cùng loại phải hợp lệ: %v", err)
	}
	if _, err := fhirauth.ParsePolicy([]any{"Communication?sender=Patient/1", "Communication?recipient=Patient/1", "Communication?_compartment=Patient/1"}); err != nil {
		t.Errorf("chuỗi và compartment lẫn nhau: %v", err)
	}
	var many []any
	for i := 0; i < 17; i++ {
		many = append(many, fmt.Sprintf("Communication?sender=Patient/%d", i))
	}
	for name, v := range map[string]any{
		"quá nhiều rule":               []any(many),
		"readonly khác nhau":           []any{comm("sender=Patient/1"), map[string]any{"resourceType": "Communication", "criteria": "recipient=Patient/1", "readonly": true}},
		"deny kèm rule":                []any{map[string]any{"resourceType": "Communication", "deny": true}, comm("sender=Patient/1")},
		"rule không criteria kèm rule": []any{"Communication", comm("sender=Patient/1")},
		"rule thứ hai có _filter":      []any{comm("sender=Patient/1"), comm("_filter=status eq completed")},
		"rule thứ hai chained":         []any{comm("sender=Patient/1"), comm("subject:Patient.name=x")},
		"wildcard trùng":               []any{"*", "*"},
	} {
		if _, err := fhirauth.ParsePolicy(v); err == nil {
			t.Errorf("%s: phải bị từ chối", name)
		}
	}
}

func TestPolicyAlternativesEnforced(t *testing.T) { onEachBackend(t, policyAlternativesEnforced) }

func policyAlternativesEnforced(t *testing.T, e *compEnv) {
	comm := func(sender, recipient, subject string) string {
		ref := func(p string) string {
			if p == "" {
				return ""
			}
			return `{"reference":"Patient/` + p + `"}`
		}
		s := `{"resourceType":"Communication","status":"completed"`
		if sender != "" {
			s += `,"sender":` + ref(sender)
		}
		if recipient != "" {
			s += `,"recipient":[` + ref(recipient) + `]`
		}
		if subject != "" {
			s += `,"subject":` + ref(subject)
		}
		return s + `}`
	}
	cSend := mustCreate(t, e.repo, e.admin, comm(e.p1, "", ""))    // P1 gửi
	cRecv := mustCreate(t, e.repo, e.admin, comm("", e.p1, ""))    // P1 nhận
	cOther := mustCreate(t, e.repo, e.admin, comm(e.p2, e.p2, "")) // của P2
	cSubj := mustCreate(t, e.repo, e.admin, comm("", "", e.p1))    // chỉ subject P1: ngoài hai rule
	for i := 0; i < 4; i++ {                                       // nhiều resource của P2 mới hơn
		mustCreate(t, e.repo, e.admin, comm(e.p2, "", ""))
	}
	pol, err := fhirauth.ParsePolicy([]any{
		map[string]any{"resourceType": "Communication", "criteria": "sender=Patient/" + e.p1},
		map[string]any{"resourceType": "Communication", "criteria": "recipient=Patient/" + e.p1},
	})
	if err != nil {
		t.Fatal(err)
	}
	pt := &fhirauth.Principal{Subject: "patient1", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"*": fhirauth.PermAll}, Policy: pol}

	for id, want := range map[string]int{cSend: 200, cRecv: 200, cOther: 404, cSubj: 404} {
		if c, _ := e.do(pt, "GET", "Communication/"+id, ""); c != want {
			t.Errorf("GET Communication/%s: %d, muốn %d", id, c, want)
		}
	}
	// search: tổng và phân trang chỉ tính phần được thấy (OR đã được đẩy xuống kho)
	c, b := e.do(pt, "GET", "Communication?_total=accurate&_count=1", "")
	if c != 200 || b["total"] != float64(2) || len(ids(b, "")) != 1 {
		t.Errorf("total phải là 2 và trang đầy: %d total=%v ids=%v", c, b["total"], ids(b, ""))
	}
	_, b = e.do(pt, "GET", "Communication", "")
	want := []string{"Communication/" + cSend, "Communication/" + cRecv}
	sort.Strings(want)
	if got := ids(b, ""); !slices.Equal(got, want) {
		t.Errorf("search: %v, muốn %v", got, want)
	}
	// điều kiện người dùng AND với (rule1 OR rule2)
	_, b = e.do(pt, "GET", "Communication?sender=Patient/"+e.p1, "")
	if got := ids(b, ""); !slices.Equal(got, []string{"Communication/" + cSend}) {
		t.Errorf("sender=P1 chỉ còn cSend: %v", got)
	}
	// history theo từng resource
	if c, _ := e.do(pt, "GET", "Communication/"+cOther+"/_history", ""); c != 404 {
		t.Errorf("history của resource ngoài rule phải 404: %d", c)
	}
	// ghi: sửa resource trong phạm vi được (rule không readonly), nhưng không được sửa nó ra ngoài phạm vi
	inScope := `{"resourceType":"Communication","id":"` + cSend + `","status":"completed","sender":{"reference":"Patient/` + e.p1 + `"},"note":[{"text":"đã đọc"}]}`
	if c, _ := e.do(pt, "PUT", "Communication/"+cSend, inScope); c != 200 {
		t.Errorf("sửa trong phạm vi: %d", c)
	}
	outScope := `{"resourceType":"Communication","id":"` + cSend + `","status":"completed","sender":{"reference":"Patient/` + e.p2 + `"}}`
	if c, _ := e.do(pt, "PUT", "Communication/"+cSend, outScope); c != 403 {
		t.Errorf("sửa ra ngoài phạm vi phải 403: %d", c)
	}
}

// Một rule là AND của các điều kiện của nó; nhiều rule mới là OR.
func TestPolicyAlternativeIsAndWithinRule(t *testing.T) {
	onEachBackend(t, policyAlternativeIsAndWithinRule)
}

func policyAlternativeIsAndWithinRule(t *testing.T, e *compEnv) {
	o := func(subject, status string) string {
		return `{"resourceType":"Observation","status":"` + status + `","code":{"text":"x"},"subject":{"reference":"Patient/` + subject + `"}}`
	}
	own := mustCreate(t, e.repo, e.admin, o(e.p1, "amended"))    // thuộc rule A (compartment P1)
	shared := mustCreate(t, e.repo, e.admin, o(e.p2, "final"))   // thuộc rule B (P2 và final)
	hidden := mustCreate(t, e.repo, e.admin, o(e.p2, "amended")) // P2 nhưng không final: ngoài cả hai rule
	pol, err := fhirauth.ParsePolicy([]any{
		map[string]any{"resourceType": "Observation", "criteria": "_compartment=Patient/" + e.p1},
		map[string]any{"resourceType": "Observation", "criteria": "subject=Patient/" + e.p2 + "&status=final"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pt := &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"*": fhirauth.PermAll}, Policy: pol}
	for id, want := range map[string]int{own: 200, shared: 200, hidden: 404} {
		if c, _ := e.do(pt, "GET", "Observation/"+id, ""); c != want {
			t.Errorf("GET Observation/%s: %d, muốn %d", id, c, want)
		}
	}
	// o1 và o3 (compartment P1 từ dữ liệu nền) + own + shared; không có o2 (P2, không final? o2 final nhưng khác subject điều kiện: P2 và final nên thuộc rule B)
	_, b := e.do(pt, "GET", "Observation?_total=accurate", "")
	seen := map[string]bool{}
	for _, id := range ids(b, "Observation") {
		seen[id] = true
	}
	if seen["Observation/"+hidden] {
		t.Errorf("lộ resource ngoài mọi rule")
	}
	if !seen["Observation/"+own] || !seen["Observation/"+shared] {
		t.Errorf("thiếu resource thuộc rule: %v", seen)
	}
}
