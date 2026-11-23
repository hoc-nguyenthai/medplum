package domain

import (
	"slices"

	"trivita-fhir/pkg/fhir"
)

// Các loại profile có access policy mặc định, theo thứ tự liệt kê trên Project.defaultAccessPolicies
// (fhir/default-access-policies.ts).
var DefaultPolicyProfileTypes = []string{"Patient", "RelatedPerson", "Admin", "Practitioner", "Bot", "ClientApplication"}

// protectedResourceTypes chỉ super admin được truy cập; projectAdminResourceTypes chỉ project admin (@medplum/core access.ts).
var (
	protectedResourceTypes    = []string{"DomainConfiguration", "Enterprise", "JsonWebKey", "Login"}
	projectAdminResourceTypes = []string{"AccessPolicy", "Cron", "Package", "PackageRelease", "PackageInstallation", "Project", "ProjectMembership", "User", "UserSecurityRequest"}
)

var clientSecretFields = []string{"secret", "retiringSecret"}

// Tri thức, thuật ngữ và đặc tả do admin quản lý, không phải người dùng tuyến đầu.
var practitionerReadonly = []string{"MedicationKnowledge", "PlanDefinition", "ActivityDefinition", "ObservationDefinition", "ValueSet", "StructureDefinition", "CodeSystem"}

// DefaultAccessPolicy dựng nội dung access policy mặc định cho một loại profile (chưa có id, meta.project, resourceType).
// ok=false nếu loại profile không có policy mặc định.
func DefaultAccessPolicy(profileType string) (fhir.AccessPolicy, bool) {
	switch profileType {
	case "Patient":
		return fhir.AccessPolicy{Name: "Default Patient Access Policy", Resource: patientCompartmentResources()}, true
	case "RelatedPerson":
		// %patient là Patient liên kết, cấp qua ProjectMembership.access.parameter khi mời; profile là RelatedPerson nên không
		// điền được %patient.
		res := []fhir.AccessPolicyResource{
			// Người chăm sóc được sửa hồ sơ của mình nhưng không được trỏ sang bệnh nhân khác.
			{ResourceType: "RelatedPerson", Criteria: "RelatedPerson?_id=%profile.id&patient=%patient", Interaction: []string{"update"}},
		}
		return fhir.AccessPolicy{Name: "Default RelatedPerson Access Policy", Resource: append(res, patientCompartmentResources()...)}, true
	case "Admin":
		return fhir.AccessPolicy{Name: "Default Admin Access Policy", Resource: writableExcept(nil, nil)}, true
	case "Practitioner":
		ro := append(slices.Clone(practitionerReadonly), "Bot", "ClientApplication")
		return fhir.AccessPolicy{Name: "Default Practitioner Access Policy",
			Resource: writableExcept(ro, map[string][]string{"ClientApplication": clientSecretFields})}, true
	case "Bot":
		return fhir.AccessPolicy{Name: "Default Bot Access Policy",
			Resource: writableExcept([]string{"AccessPolicy", "Bot", "ClientApplication"}, map[string][]string{"ClientApplication": clientSecretFields})}, true
	case "ClientApplication":
		return fhir.AccessPolicy{Name: "Default ClientApplication Access Policy",
			Resource: writableExcept([]string{"AccessPolicy", "Bot", "ClientApplication"}, map[string][]string{"ClientApplication": clientSecretFields})}, true
	}
	return fhir.AccessPolicy{}, false
}

func yes() *bool { t := true; return &t }

// writableExcept biên dịch "đọc/ghi mọi thứ trừ các hạn chế này" thành các entry của access policy (port của writableExcept,
// default-access-policies.ts). Điểm then chốt:
//   - Quyền cộng dồn nên wildcard ghi sẽ đè entry readonly. Khi có loại không-phải-admin phải readonly, quyền đọc đến từ
//     wildcard readonly và từng loại ghi được phải liệt kê riêng (policy chỉ phủ các loại tồn tại lúc tạo).
//   - Server lấy hiddenFields từ entry ĐẦU TIÊN khớp, nên entry có hiddenFields đứng trước wildcard; các loại đó cũng không có
//     entry ghi-không-ẩn (nếu không, update khớp entry đó và có thể ghi đè trường ẩn).
//   - '*' không bao giờ khớp loại project admin nên loại admin readonly có entry riêng; loại project admin và protected
//     không bao giờ được liệt kê là ghi được.
func writableExcept(readonly []string, hidden map[string][]string) []fhir.AccessPolicyResource {
	var fieldEntries []fhir.AccessPolicyResource
	for _, rt := range sortedKeys(hidden) {
		e := fhir.AccessPolicyResource{ResourceType: rt, HiddenFields: hidden[rt]}
		if slices.Contains(readonly, rt) {
			e.Readonly = yes()
		}
		fieldEntries = append(fieldEntries, e)
	}
	var adminReadonly []fhir.AccessPolicyResource
	for _, rt := range readonly {
		if slices.Contains(projectAdminResourceTypes, rt) {
			adminReadonly = append(adminReadonly, fhir.AccessPolicyResource{ResourceType: rt, Readonly: yes()})
		}
	}
	// Không có loại không-phải-admin nào readonly: wildcard ghi an toàn và còn phủ cả loại thêm sau này.
	allAdmin := true
	for _, rt := range readonly {
		if !slices.Contains(projectAdminResourceTypes, rt) {
			allAdmin = false
		}
	}
	if allAdmin {
		out := append(fieldEntries, adminReadonly...)
		return append(out, fhir.AccessPolicyResource{ResourceType: "*"})
	}
	out := append(fieldEntries, fhir.AccessPolicyResource{ResourceType: "*", Readonly: yes()})
	out = append(out, adminReadonly...)
	for _, rt := range fhir.ResourceTypes {
		if slices.Contains(readonly, rt) || hidden[rt] != nil || slices.Contains(projectAdminResourceTypes, rt) || slices.Contains(protectedResourceTypes, rt) {
			continue
		}
		out = append(out, fhir.AccessPolicyResource{ResourceType: rt})
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func patientCompartmentResources() []fhir.AccessPolicyResource {
	compartment := func(rt string) fhir.AccessPolicyResource {
		return fhir.AccessPolicyResource{ResourceType: rt, Criteria: rt + "?_compartment=%patient"}
	}
	res := []fhir.AccessPolicyResource{
		{ResourceType: "Patient", Criteria: "Patient?_id=%patient.id"},
	}
	for _, rt := range []string{"AllergyIntolerance", "Appointment", "CarePlan", "CareTeam"} {
		res = append(res, compartment(rt))
	}
	res = append(res,
		fhir.AccessPolicyResource{ResourceType: "Communication", Criteria: "Communication?sender=%patient"},
		fhir.AccessPolicyResource{ResourceType: "Communication", Criteria: "Communication?recipient=%patient"},
	)
	for _, rt := range []string{"Condition", "Coverage", "DiagnosticReport", "DocumentReference", "Encounter", "Goal", "Immunization",
		"MedicationRequest", "MedicationStatement", "Observation", "Procedure", "QuestionnaireResponse"} {
		res = append(res, compartment(rt))
	}
	rp := compartment("RelatedPerson")
	rp.Readonly = yes()
	res = append(res, rp, compartment("ServiceRequest"), compartment("Task"))
	return res
}
