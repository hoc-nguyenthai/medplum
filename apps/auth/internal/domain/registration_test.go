package domain

import (
	"testing"

	"trivita-fhir/pkg/fhir"
)

func projectWith(setting *bool, policies []fhir.ProjectDefaultAccessPolicies, legacy string) *fhir.Project {
	p := &fhir.Project{ResourceType: "Project", DefaultAccessPolicies: policies}
	if setting != nil {
		p.Setting = []fhir.ProjectSetting{{Name: OpenPatientRegistrationSetting, ValueBoolean: setting}}
	}
	if legacy != "" {
		p.DefaultPatientAccessPolicy = &fhir.Reference{Reference: legacy}
	}
	return p
}

func TestOpenRegistrationPolicy(t *testing.T) {
	yes, no := true, false
	pat := []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/p"}}}
	other := []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Practitioner", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/x"}}}
	for _, tc := range []struct {
		name string
		p    *fhir.Project
		want string
	}{
		{"nil", nil, ""},
		{"không có setting", projectWith(nil, pat, ""), ""},
		{"setting false", projectWith(&no, pat, ""), ""},
		{"setting true có chính sách Patient", projectWith(&yes, pat, ""), "AccessPolicy/p"},
		{"chính sách Patient thắng chính sách cũ", projectWith(&yes, pat, "AccessPolicy/old"), "AccessPolicy/p"},
		{"rơi về defaultPatientAccessPolicy", projectWith(&yes, other, "AccessPolicy/old"), "AccessPolicy/old"},
		{"setting true nhưng không có chính sách", projectWith(&yes, other, ""), ""},
		{"chính sách Patient không có tham chiếu", projectWith(&yes, []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient"}}, ""), ""},
	} {
		got := OpenRegistrationPolicy(tc.p)
		switch {
		case tc.want == "" && got != nil, tc.want != "" && (got == nil || got.Reference != tc.want):
			t.Errorf("%s: %+v, mong %q", tc.name, got, tc.want)
		}
	}
}

func TestProjectRegistrationAllowed(t *testing.T) {
	yes, no := true, false
	pat := []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/p"}}}
	if err := ProjectRegistrationAllowed(projectWith(&yes, pat, "")); err != nil {
		t.Errorf("phải cho phép: %v", err)
	}
	if err := ProjectRegistrationAllowed(projectWith(nil, nil, "")); !Is(err, KindBadRequest, "Project does not allow open registration") {
		t.Errorf("%v", err)
	}
	inactive := projectWith(&yes, pat, "")
	inactive.Active = &no
	// project không hoạt động bị báo trước, kể cả khi có chính sách
	if err := ProjectRegistrationAllowed(inactive); !Is(err, KindBadRequest, "Project not active") {
		t.Errorf("%v", err)
	}
	inactiveClosed := projectWith(nil, nil, "")
	inactiveClosed.Active = &no
	if err := ProjectRegistrationAllowed(inactiveClosed); !Is(err, KindBadRequest, "Project not active") {
		t.Errorf("thứ tự: không hoạt động trước: %v", err)
	}
}
