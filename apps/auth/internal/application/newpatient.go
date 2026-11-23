package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// NewPatientRequest là yêu cầu POST /auth/newpatient (đã qua validator).
type NewPatientRequest struct {
	LoginID   string
	ProjectID string
}

// NewPatient xử lý POST /auth/newpatient (auth/newpatient.ts:25): bệnh nhân tự đăng ký vào project cho phép đăng ký mở. Tạo hồ sơ Patient và
// membership kèm chính sách đăng ký mở trong một transaction, rồi chọn membership đó cho login. Như NewProject (lệch L3), mọi kiểm tra
// (trạng thái login, project, chính sách) chạy TRƯỚC khi tạo gì, và login thiếu MFA nhận phản hồi MFA mà chưa tạo gì.
func (s *Service) NewPatient(ctx context.Context, req NewPatientRequest) (*LoginResult, *fhir.Login, error) {
	login, err := s.Logins.Get(ctx, req.LoginID)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case login.Membership != nil:
		return nil, nil, domain.BadRequest("Login already has a membership")
	case refOf(login.Project) != "" && refOf(login.Project) != "Project/"+req.ProjectID:
		return nil, nil, domain.BadRequest("Invalid project")
	case domain.IsTrue(login.Revoked):
		return nil, nil, domain.BadRequest("Login revoked")
	case domain.IsTrue(login.Granted):
		return nil, nil, domain.BadRequest("Login granted")
	case login.User == nil:
		return nil, nil, domain.BadRequest("User reference is missing")
	}
	user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
	if err != nil {
		return nil, nil, err
	}
	// user phải thuộc ĐÚNG project này (Medplum chỉ kiểm có project; kiểm khớp để không phụ thuộc vào cách tryLogin tìm user)
	if refOf(user.Project) != "Project/"+req.ProjectID {
		return nil, nil, domain.BadRequest("User must be scoped to the project")
	}
	switch domain.DecideLoginResult(user, nil, login) {
	case domain.ResultMFAEnroll, domain.ResultMFARequired:
		res, err := s.BuildLoginResult(ctx, login)
		return res, login, err
	}
	project, err := s.Projects.Get(ctx, req.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	if err := domain.ProjectRegistrationAllowed(project); err != nil {
		return nil, nil, err
	}
	policyRef := domain.OpenRegistrationPolicy(project)
	policy, err := s.AccessPolicies.Get(ctx, idOf(policyRef.Reference)) // chính sách phải còn tồn tại trước khi tạo gì
	if err != nil {
		return nil, nil, err
	}

	var membership *fhir.ProjectMembership
	if err := s.Tx.InTx(ctx, func(tx TxRepos) error {
		patient := &fhir.Patient{
			ResourceType: "Patient", Meta: &fhir.Meta{Project: project.ID},
			Name: []fhir.HumanName{{Given: []string{user.FirstName}, Family: user.LastName}},
		}
		if user.Email != "" {
			patient.Telecom = []fhir.ContactPoint{{System: "email", Use: "work", Value: user.Email}}
		}
		profile, err := tx.Resources.Create(ctx, patient)
		if err != nil {
			return err
		}
		m, err := tx.Resources.Create(ctx, &fhir.ProjectMembership{
			ResourceType: "ProjectMembership", Meta: &fhir.Meta{Project: project.ID},
			Project: newRef("Project", project.ID, project.Name), User: newRef("User", user.ID, user.Email),
			Profile:      newRef("Patient", profile.GetID(), ""),
			AccessPolicy: newRef("AccessPolicy", policy.ID, policy.Name),
		})
		membership, _ = m.(*fhir.ProjectMembership)
		return err
	}); err != nil {
		return nil, nil, err
	}
	updated, err := s.SetLoginMembership(ctx, login, membership)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.BuildLoginResult(ctx, updated)
	return res, updated, err
}
