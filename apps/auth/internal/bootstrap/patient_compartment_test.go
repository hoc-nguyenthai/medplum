package bootstrap

import (
	"context"
	"slices"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// P8 M3 từ đầu đến cuối: membership dùng chính sách mặc định của Patient với tham số `patient` ở ProjectMembership.access; token cấp ra mang claim
// mà fhirauth (cùng bộ phân tích fhir-server dùng) nhận, và loại chỉ đọc được khi cờ AUTH_PATIENT_COMPARTMENT_ENABLED bật.
func patientMembership(t *testing.T, a *api, patientRef string) {
	t.Helper()
	pol, _ := domain.DefaultAccessPolicy("Patient")
	pol.ResourceType, pol.Meta = "AccessPolicy", &fhir.Meta{Project: a.seed.Project.ID}
	out, err := a.c.Store.CreateResource(context.Background(), &pol)
	if err != nil {
		t.Fatal(err)
	}
	a.updateMembership(func(m *fhir.ProjectMembership) {
		m.AccessPolicy = nil
		m.Access = []fhir.ProjectMembershipAccess{{
			Policy:    &fhir.Reference{Reference: "AccessPolicy/" + out.GetID()},
			Parameter: []fhir.ProjectMembershipAccessParameter{{Name: "patient", ValueReference: &fhir.Reference{Reference: patientRef}}},
		}}
	})
}

func policyTypes(t *testing.T, a *api) []string {
	t.Helper()
	r := a.clientToken(nil)
	if r.status != 200 {
		t.Fatalf("cấp token: %d %s", r.status, r.body)
	}
	p, err := a.verifier().Verify(context.Background(), r.json()["access_token"].(string))
	if err != nil {
		t.Fatalf("fhirauth phải nhận claim: %v", err)
	}
	if p.Policy == nil {
		t.Fatal("token phải mang chính sách")
	}
	return p.Policy.Types()
}

func TestPatientPolicyClaimIssuedWhenCompartmentEnabled(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.PatientCompartmentEnabled = true }, nil)
	patientMembership(t, a, "Patient/00000000-0000-4000-8000-000000000001")
	types := policyTypes(t, a)
	for _, want := range []string{"Patient", "Observation", "Condition", "Communication", "Appointment"} {
		if !slices.Contains(types, want) {
			t.Errorf("loại %s phải đọc được, có %v", want, types)
		}
	}
	if slices.Contains(types, "Practitioner") || slices.Contains(types, "User") {
		t.Errorf("không được lộ loại ngoài compartment: %v", types)
	}
}

func TestPatientPolicyClaimDeniedWhenCompartmentDisabled(t *testing.T) {
	a := newAPI(t, nil, nil) // cờ mặc định: tắt
	patientMembership(t, a, "Patient/00000000-0000-4000-8000-000000000001")
	if types := policyTypes(t, a); len(types) != 0 {
		t.Errorf("cờ tắt: mọi loại dùng %%patient phải bị cấm, có %v", types)
	}
}

func TestPatientPolicyClaimRejectsInjectedParameter(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.PatientCompartmentEnabled = true }, nil)
	patientMembership(t, a, "Patient/1&status=final") // ProjectMembership.access.parameter do quản trị đặt: không được nới criteria
	if types := policyTypes(t, a); len(types) != 0 {
		t.Errorf("giá trị tham số sai định dạng: mọi loại phải bị cấm, có %v", types)
	}
}
