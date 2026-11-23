package domain

import (
	"reflect"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
)

func TestParseTokenDuration(t *testing.T) {
	ok := map[string]time.Duration{"30s": 30 * time.Second, "15m": 15 * time.Minute, "1h": time.Hour, "2d": 48 * time.Hour, "2w": 14 * 24 * time.Hour, "1y": 365 * 24 * time.Hour}
	for in, want := range ok {
		if got, err := ParseTokenDuration(in); err != nil || got != want {
			t.Errorf("%s = %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "1", "h", "1.5h", "-1h", "0h", "1H", "1 h", "99999999999h", "1hh"} {
		if _, err := ParseTokenDuration(bad); err == nil {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}
}

func TestMatchClientSecret(t *testing.T) {
	c := &fhir.ClientApplication{Secret: "s3cret", RetiringSecret: "old"}
	for _, ok := range []string{"s3cret", "old"} {
		if !MatchClientSecret(c, ok) {
			t.Errorf("%q phải khớp", ok)
		}
	}
	for _, bad := range []string{"", "s3cre", "s3cret ", "OLD", "s3cretold"} {
		if MatchClientSecret(c, bad) {
			t.Errorf("%q không được khớp", bad)
		}
	}
	if MatchClientSecret(&fhir.ClientApplication{}, "") || MatchClientSecret(&fhir.ClientApplication{}, "x") {
		t.Error("client không secret không bao giờ khớp")
	}
	if !MatchClientSecret(&fhir.ClientApplication{Secret: "a"}, "a") || MatchClientSecret(&fhir.ClientApplication{Secret: "a"}, "b") {
		t.Error("không có retiringSecret")
	}
	if !EqualSecret("", "") || EqualSecret("a", "") || EqualSecret("abc", "abd") {
		t.Error("EqualSecret")
	}
}

func TestValidateRequestedScope(t *testing.T) {
	for _, ok := range []string{"", "openid", "openid profile", "system/Patient.rs", "user/*.cruds", "offline_access", "fhirUser"} {
		if e := ValidateRequestedScope(ok); e != nil {
			t.Errorf("%q: %v", ok, e)
		}
	}
	for _, bad := range []string{"fhir.admin", "openid fhir.write", "FHIR.ADMIN", "fhir.super", "fhir.read", "x fhir.anything"} {
		if e := ValidateRequestedScope(bad); e == nil || e.Code != "invalid_scope" {
			t.Errorf("%q phải bị từ chối: %v", bad, e)
		}
	}
	if !HasScopeTerm("openid email", "email") || HasScopeTerm("openid emailx", "email") || HasScopeTerm("openid", "pen") {
		t.Error("HasScopeTerm phải so theo từ")
	}
}

func TestRoleScopes(t *testing.T) {
	yes := true
	admin := &fhir.ProjectMembership{Admin: &yes}
	plain := &fhir.ProjectMembership{}
	ro := []fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{{ResourceType: "Patient", Readonly: &yes}, {ResourceType: "*", Readonly: &yes}}}}
	mixed := []fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{{ResourceType: "Patient", Readonly: &yes}, {ResourceType: "Observation"}}}}
	cases := []struct {
		name  string
		scope string
		m     *fhir.ProjectMembership
		pol   []fhir.AccessPolicy
		want  []string
	}{
		{"admin", "openid", admin, nil, []string{"fhir.admin"}},
		{"thường", "openid", plain, nil, []string{"fhir.write"}},
		{"chỉ đọc", "openid", plain, ro, []string{"fhir.read"}},
		{"hỗn hợp", "openid", plain, mixed, []string{"fhir.write"}},
		{"admin nhưng chính sách chỉ đọc vẫn là admin", "openid", admin, ro, []string{"fhir.admin"}},
		{"SMART thì không thêm", "system/Patient.rs", admin, nil, nil},
		{"SMART user", "openid user/*.cruds", plain, mixed, nil},
	}
	for _, c := range cases {
		if got := RoleScopes(c.scope, c.m, c.pol); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, muốn %v", c.name, got, c.want)
		}
	}
}

func claimByType(t *testing.T, claim []map[string]any) map[string]map[string]any {
	t.Helper()
	m := map[string]map[string]any{}
	for _, e := range claim {
		rt := e["resourceType"].(string)
		if _, dup := m[rt]; dup {
			t.Fatalf("hai rule cho %s", rt)
		}
		m[rt] = e
	}
	return m
}

func TestPolicyClaimNarrowsNeverWidens(t *testing.T) {
	yes := true
	claim, narrowed, err := PolicyClaim([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{
		{ResourceType: "ClientApplication", HiddenFields: []string{"secret"}, Readonly: &yes}, // không dịch được -> cấm cả loại
		{ResourceType: "*", Readonly: &yes},
		{ResourceType: "Observation"},
		{ResourceType: "Condition", ReadonlyFields: []string{"code"}},              // -> chỉ đọc
		{ResourceType: "Communication", Criteria: "Communication?sender=%patient"}, // tham số hoá -> cấm
		{ResourceType: "Task", Criteria: "Task?status=ready"},
		{ResourceType: "Task", Criteria: "Task?status=draft"}, // hai criteria, không có toàn loại -> cấm
		{ResourceType: "Encounter", Criteria: "Encounter?status=planned"},
		{ResourceType: "Encounter"}, // có toàn loại: giữ toàn loại, bỏ phần criteria (hẹp hơn hợp)
		{ResourceType: "Goal", Readonly: &yes},
		{ResourceType: "Goal"}, // gộp: có entry ghi được thì ghi được (cộng dồn như Medplum)... nhưng chỉ đọc nếu MỌI entry chỉ đọc
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(narrowed) == 0 {
		t.Error("phải báo các chỗ thu hẹp")
	}
	m := claimByType(t, claim)
	want := map[string]map[string]any{
		"ClientApplication": {"resourceType": "ClientApplication", "deny": true},
		"*":                 {"resourceType": "*", "readonly": true},
		"Observation":       {"resourceType": "Observation"},
		"Condition":         {"resourceType": "Condition", "readonly": true},
		"Communication":     {"resourceType": "Communication", "deny": true},
		"Task":              {"resourceType": "Task", "deny": true},
		"Encounter":         {"resourceType": "Encounter"},
		"Goal":              {"resourceType": "Goal"},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("claim = %v\nmuốn   %v", m, want)
	}
}

func TestPolicyClaimWildcardWithUnsupportedFeatureIsDropped(t *testing.T) {
	claim, _, err := PolicyClaim([]fhir.AccessPolicy{{Resource: []fhir.AccessPolicyResource{
		{ResourceType: "*", HiddenFields: []string{"x"}}, {ResourceType: "Patient"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	m := claimByType(t, claim)
	if _, has := m["*"]; has || m["Patient"] == nil {
		t.Errorf("phải bỏ * (không dịch được) và giữ Patient: %v", claim)
	}
}

func TestPolicyClaimRejectsPolicyLevelFeatures(t *testing.T) {
	for name, p := range map[string]fhir.AccessPolicy{
		"compartment": {Compartment: &fhir.Reference{Reference: "Patient/1"}},
		"basedOn":     {BasedOn: []fhir.Reference{{Reference: "AccessPolicy/2"}}},
		"thiếu loại":  {Resource: []fhir.AccessPolicyResource{{Criteria: "x"}}},
	} {
		if _, _, err := PolicyClaim([]fhir.AccessPolicy{p}); err == nil {
			t.Errorf("%s phải bị từ chối", name)
		}
	}
	if c, n, err := PolicyClaim(nil); c != nil || n != nil || err != nil {
		t.Errorf("không có chính sách: %v %v %v", c, n, err)
	}
}

// Chính sách mặc định do chính auth tạo (seed project, client create) phải dịch được, và KHÔNG được lộ secret của ClientApplication.
func TestPolicyClaimOfDefaultPolicies(t *testing.T) {
	for _, profile := range []string{"Admin", "Practitioner", "Bot", "ClientApplication", "Patient", "RelatedPerson"} {
		pol, ok := DefaultAccessPolicy(profile)
		if !ok {
			t.Fatalf("thiếu policy mặc định %s", profile)
		}
		claim, _, err := PolicyClaim([]fhir.AccessPolicy{pol})
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		m := claimByType(t, claim)
		switch profile {
		case "Admin":
			if len(m) != 1 || m["*"] == nil || m["*"]["readonly"] != nil {
				t.Errorf("Admin chỉ có * ghi được: %v", claim)
			}
		case "Practitioner", "Bot", "ClientApplication":
			if m["ClientApplication"]["deny"] != true {
				t.Errorf("%s: ClientApplication phải bị cấm (secret): %v", profile, m["ClientApplication"])
			}
			if m["*"] == nil || m["*"]["readonly"] != true {
				t.Errorf("%s: * phải chỉ đọc", profile)
			}
			if m["Patient"] == nil || m["Patient"]["readonly"] != nil || m["Patient"]["deny"] != nil {
				t.Errorf("%s: Patient phải ghi được: %v", profile, m["Patient"])
			}
			if profile == "Practitioner" {
				if m["AccessPolicy"] != nil { // loại quản trị không có entry riêng thì không khớp "*"
					t.Errorf("Practitioner không có quyền với AccessPolicy: %v", m["AccessPolicy"])
				}
			} else if m["AccessPolicy"] == nil || m["AccessPolicy"]["readonly"] != true {
				t.Errorf("%s: AccessPolicy chỉ đọc: %v", profile, m["AccessPolicy"])
			}
		case "Patient", "RelatedPerson":
			if m["Observation"]["deny"] != true || m["Patient"]["deny"] != true {
				t.Errorf("%s: loại theo compartment phải bị cấm: %v", profile, m["Observation"])
			}
		}
	}
}
