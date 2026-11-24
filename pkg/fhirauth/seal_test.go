package fhirauth_test

import (
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirauth"
)

func TestSealRoundTripAndTamper(t *testing.T) {
	sl, err := fhirauth.NewSealer([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fhirauth.NewSealer([]byte("short")); err == nil {
		t.Fatal("khóa ngắn phải bị từ chối")
	}
	pol, err := fhirauth.ParsePolicy([]any{"Observation?status=final"})
	if err != nil {
		t.Fatal(err)
	}
	orig := &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite,
		Grants: fhirauth.Grants{"Observation": fhirauth.PermAllRead}, Policy: pol}
	sealed := sl.Seal("job1", "export?x=1", orig)

	got, err := sl.Open("job1", "p1", "export?x=1", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "p1" || !got.Job || !got.Grants.CanRead("Observation") || got.Grants.CanRead("Patient") || got.Policy == nil {
		t.Fatalf("%+v", got)
	}
	if !got.Grants.Can("Binary", fhirauth.PermWrite) || got.Grants.CanRead("Binary") || got.Grants.CanSearch("Binary") {
		t.Errorf("job chỉ được ghi Binary: %v", got.Grants)
	}
	// không nới quyền: chính sách giữ nguyên
	if got.Policy.Types()[0] != "Observation" || len(got.Policy.Types()) != 1 {
		t.Errorf("chính sách: %v", got.Policy.Types())
	}
	// mọi thay đổi đều bị phát hiện
	other, _ := fhirauth.NewSealer([]byte(strings.Repeat("z", 32)))
	for name, c := range map[string]struct{ id, project, req, sealed string }{
		"id khác":      {"job2", "p1", "export?x=1", sealed},
		"project khác": {"job1", "p2", "export?x=1", sealed},
		"request khác": {"job1", "p1", "reindex?x=1", sealed},
		"rỗng":         {"job1", "p1", "export?x=1", ""},
		"hỏng":         {"job1", "p1", "export?x=1", "abc.def"},
		"đổi payload":  {"job1", "p1", "export?x=1", "e30." + strings.SplitN(sealed, ".", 2)[1]},
	} {
		if _, err := sl.Open(c.id, c.project, c.req, c.sealed); err == nil {
			t.Errorf("%s: muốn lỗi", name)
		}
	}
	if _, err := other.Open("job1", "p1", "export?x=1", sealed); err == nil {
		t.Error("khóa khác phải bị từ chối")
	}
	// không có người gọi: ảnh chụp không có quyền gì
	none, err := sl.Open("j", "", "r", sl.Seal("j", "r", nil))
	if err != nil || none.Grants == nil || none.Grants.CanRead("Patient") {
		t.Fatalf("%+v %v", none, err)
	}
	// Principal không giới hạn (Grants nil) vẫn không giới hạn
	un, _ := sl.Open("j", "p", "r", sl.Seal("j", "r", &fhirauth.Principal{Project: "p"}))
	if un.Grants != nil {
		t.Errorf("Grants nil phải giữ nil: %v", un.Grants)
	}
}

// Principal dựng lại cho job mang người đã tạo job (đã ký) ở OnBehalfOf để AuditEvent ghi đúng người chịu trách nhiệm; Subject vẫn là "job:<id>".
func TestOpenedPrincipalCarriesTheSealedSubjectAsOnBehalfOf(t *testing.T) {
	sl, _ := fhirauth.NewSealer([]byte(strings.Repeat("k", 32)))
	sealed := sl.Seal("job1", "r", &fhirauth.Principal{Subject: "alice", Project: "p1", Role: fhirauth.RoleRead})
	got, err := sl.Open("job1", "p1", "r", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "job:job1" || got.OnBehalfOf != "alice" {
		t.Fatalf("Subject=%q OnBehalfOf=%q", got.Subject, got.OnBehalfOf)
	}
	anon, err := sl.Open("job2", "", "r", sl.Seal("job2", "r", nil))
	if err != nil {
		t.Fatal(err)
	}
	if anon.OnBehalfOf != "" {
		t.Fatalf("job không có người gọi không có OnBehalfOf: %q", anon.OnBehalfOf)
	}
}
