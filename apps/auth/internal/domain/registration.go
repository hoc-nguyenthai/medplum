package domain

import "trivita-fhir/pkg/fhir"

// OpenPatientRegistrationSetting là tên setting của Project bật đăng ký mở cho bệnh nhân.
const OpenPatientRegistrationSetting = "openPatientRegistration"

// OpenRegistrationPolicy trả chính sách truy cập áp cho người tự đăng ký (Patient) của project, hoặc nil nếu project không cho đăng ký
// mở: setting `openPatientRegistration` phải là true VÀ có chính sách (ưu tiên `defaultAccessPolicies` loại Patient, rồi
// `defaultPatientAccessPolicy`). Thiếu một trong hai là không cho đăng ký (auth/utils.ts:171).
func OpenRegistrationPolicy(p *fhir.Project) *fhir.Reference {
	if p == nil {
		return nil
	}
	if s := ProjectSetting(p, OpenPatientRegistrationSetting); s == nil || !IsTrue(s.ValueBoolean) {
		return nil
	}
	for _, d := range p.DefaultAccessPolicies {
		if d.ProfileType == "Patient" && d.AccessPolicy != nil && d.AccessPolicy.Reference != "" {
			return d.AccessPolicy
		}
	}
	if r := p.DefaultPatientAccessPolicy; r != nil && r.Reference != "" {
		return r
	}
	return nil
}

// ProjectRegistrationAllowed kiểm tra project nhận đăng ký mở: đang hoạt động và có chính sách đăng ký mở (auth/utils.ts:183).
func ProjectRegistrationAllowed(p *fhir.Project) error {
	if r := InactiveReason(p, nil); r != "" {
		return BadRequest(r)
	}
	if OpenRegistrationPolicy(p) == nil {
		return BadRequest("Project does not allow open registration")
	}
	return nil
}
