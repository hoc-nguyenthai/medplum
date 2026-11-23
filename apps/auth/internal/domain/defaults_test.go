package domain

import (
	"regexp"
	"slices"
	"testing"

	"trivita-fhir/pkg/fhir"
)

func TestNewID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 200 {
		id := NewID()
		if !re.MatchString(id) || seen[id] {
			t.Fatalf("id không phải UUID v4 duy nhất: %s", id)
		}
		seen[id] = true
	}
}

func entry(res []fhir.AccessPolicyResource, rt string) []fhir.AccessPolicyResource {
	var out []fhir.AccessPolicyResource
	for _, e := range res {
		if e.ResourceType == rt {
			out = append(out, e)
		}
	}
	return out
}

func TestDefaultAccessPolicyProfileTypes(t *testing.T) {
	want := []string{"Patient", "RelatedPerson", "Admin", "Practitioner", "Bot", "ClientApplication"}
	if !slices.Equal(DefaultPolicyProfileTypes, want) {
		t.Fatalf("thứ tự: %v", DefaultPolicyProfileTypes)
	}
	for _, pt := range want {
		p, ok := DefaultAccessPolicy(pt)
		if !ok || p.Name != "Default "+pt+" Access Policy" || len(p.Resource) == 0 {
			t.Errorf("%s: %+v %v", pt, p.Name, ok)
		}
	}
	if _, ok := DefaultAccessPolicy("Lạ"); ok {
		t.Error("loại lạ không có policy")
	}
}

func TestAdminPolicyIsWriteWildcard(t *testing.T) {
	p, _ := DefaultAccessPolicy("Admin")
	if len(p.Resource) != 1 || p.Resource[0].ResourceType != "*" || p.Resource[0].Readonly != nil {
		t.Errorf("Admin chỉ có wildcard ghi: %+v", p.Resource)
	}
}

func TestPractitionerPolicy(t *testing.T) {
	p, _ := DefaultAccessPolicy("Practitioner")
	res := p.Resource
	// entry có hiddenFields đứng ĐẦU (server lấy hiddenFields từ entry đầu tiên khớp)
	first := res[0]
	if first.ResourceType != "ClientApplication" || !IsTrue(first.Readonly) || !slices.Equal(first.HiddenFields, []string{"secret", "retiringSecret"}) {
		t.Fatalf("entry đầu: %+v", first)
	}
	if w := res[1]; w.ResourceType != "*" || !IsTrue(w.Readonly) {
		t.Errorf("sau đó là wildcard readonly: %+v", w)
	}
	if got := entry(res, "ClientApplication"); len(got) != 1 {
		t.Errorf("ClientApplication chỉ có đúng một entry (không có entry ghi không ẩn): %+v", got)
	}
	// loại readonly không có entry ghi riêng
	for _, rt := range []string{"ValueSet", "StructureDefinition", "CodeSystem", "PlanDefinition", "Bot", "MedicationKnowledge", "ActivityDefinition", "ObservationDefinition"} {
		if got := entry(res, rt); len(got) != 0 {
			t.Errorf("%s readonly không được liệt kê là ghi được: %+v", rt, got)
		}
	}
	// loại project admin và protected KHÔNG bao giờ được liệt kê là ghi được
	for _, rt := range append(slices.Clone(projectAdminResourceTypes), protectedResourceTypes...) {
		if got := entry(res, rt); len(got) != 0 {
			t.Errorf("%s không được liệt kê: %+v", rt, got)
		}
	}
	for _, rt := range []string{"Patient", "Observation", "Encounter", "Bot" + "x"} {
		got := entry(res, rt)
		if rt == "Botx" {
			if len(got) != 0 {
				t.Error("loại không tồn tại")
			}
			continue
		}
		if len(got) != 1 || got[0].Readonly != nil || got[0].Criteria != "" {
			t.Errorf("%s phải ghi được: %+v", rt, got)
		}
	}
	// mỗi loại tồn tại chỉ có một entry ghi được
	seen := map[string]int{}
	for _, e := range res[2:] {
		seen[e.ResourceType]++
	}
	for rt, n := range seen {
		if n != 1 {
			t.Errorf("%s có %d entry", rt, n)
		}
	}
}

func TestBotAndClientApplicationPolicies(t *testing.T) {
	for _, pt := range []string{"Bot", "ClientApplication"} {
		p, _ := DefaultAccessPolicy(pt)
		res := p.Resource
		if res[0].ResourceType != "ClientApplication" || len(res[0].HiddenFields) != 2 || !IsTrue(res[0].Readonly) {
			t.Errorf("%s: entry đầu %+v", pt, res[0])
		}
		if res[1].ResourceType != "*" || !IsTrue(res[1].Readonly) {
			t.Errorf("%s: wildcard readonly %+v", pt, res[1])
		}
		// AccessPolicy là loại project admin: '*' không khớp nên có entry readonly riêng
		ap := entry(res, "AccessPolicy")
		if len(ap) != 1 || !IsTrue(ap[0].Readonly) || res[2].ResourceType != "AccessPolicy" {
			t.Errorf("%s: AccessPolicy phải readonly riêng ngay sau wildcard: %+v", pt, ap)
		}
		if len(entry(res, "Bot")) != 0 {
			t.Errorf("%s: Bot readonly", pt)
		}
		if got := entry(res, "Patient"); len(got) != 1 || got[0].Readonly != nil {
			t.Errorf("%s: Patient ghi được: %+v", pt, got)
		}
	}
}

func TestPatientAndRelatedPersonPolicies(t *testing.T) {
	p, _ := DefaultAccessPolicy("Patient")
	if p.Resource[0].ResourceType != "Patient" || p.Resource[0].Criteria != "Patient?_id=%patient.id" {
		t.Errorf("Patient đầu: %+v", p.Resource[0])
	}
	for _, e := range p.Resource {
		if e.Criteria == "" {
			t.Errorf("mọi entry của Patient phải có criteria giới hạn theo bệnh nhân: %+v", e)
		}
	}
	if rp := entry(p.Resource, "RelatedPerson"); len(rp) != 1 || !IsTrue(rp[0].Readonly) {
		t.Errorf("RelatedPerson trong compartment chỉ đọc: %+v", rp)
	}
	if got := entry(p.Resource, "Communication"); len(got) != 2 || got[0].Criteria != "Communication?sender=%patient" || got[1].Criteria != "Communication?recipient=%patient" {
		t.Errorf("Communication: %+v", got)
	}
	if len(p.Resource) != 22 {
		t.Errorf("số entry: %d", len(p.Resource))
	}

	r, _ := DefaultAccessPolicy("RelatedPerson")
	first := r.Resource[0]
	if first.ResourceType != "RelatedPerson" || first.Criteria != "RelatedPerson?_id=%profile.id&patient=%patient" || !slices.Equal(first.Interaction, []string{"update"}) {
		t.Errorf("RelatedPerson được sửa hồ sơ của mình: %+v", first)
	}
	if len(r.Resource) != 23 {
		t.Errorf("RelatedPerson = 1 + 22: %d", len(r.Resource))
	}
}
