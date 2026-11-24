package bootstrap

import (
	"context"
	"slices"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/fhir"
)

// AccessPolicy có hiddenFields, readonlyFields và interaction từ đầu đến cuối: token cấp ra mang claim mà fhirauth (bộ phân tích của fhir-server) nhận khi cờ
// AUTH_POLICY_FIELD_RULES_ENABLED bật; khi tắt, loại đó bị cấm cả loại (như trước) nhưng token vẫn hợp lệ.
func fieldRulesMembership(t *testing.T, a *api) {
	t.Helper()
	pol := &fhir.AccessPolicy{Name: "fields", Resource: []fhir.AccessPolicyResource{
		{ResourceType: "Patient", HiddenFields: []string{"birthDate", "name.family"}, ReadonlyFields: []string{"gender"}, Interaction: []string{"read", "search", "update"}},
		{ResourceType: "Observation"},
	}, Meta: &fhir.Meta{Project: a.seed.Project.ID}}
	out, err := a.c.Store.CreateResource(context.Background(), pol)
	if err != nil {
		t.Fatal(err)
	}
	a.updateMembership(func(m *fhir.ProjectMembership) {
		m.Access = nil
		m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + out.GetID()}
	})
}

func TestPolicyFieldRulesIssuedWhenEnabled(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.PolicyFieldRulesEnabled = true }, nil)
	fieldRulesMembership(t, a)
	types := policyTypes(t, a)
	if !slices.Contains(types, "Patient") || !slices.Contains(types, "Observation") {
		t.Errorf("cờ bật: Patient và Observation phải dùng được, có %v", types)
	}
}

func TestPolicyFieldRulesDeniedWhenDisabled(t *testing.T) {
	a := newAPI(t, nil, nil)
	fieldRulesMembership(t, a)
	types := policyTypes(t, a)
	if slices.Contains(types, "Patient") || !slices.Contains(types, "Observation") {
		t.Errorf("cờ tắt: Patient (có hiddenFields) phải bị cấm, Observation còn, có %v", types)
	}
}
