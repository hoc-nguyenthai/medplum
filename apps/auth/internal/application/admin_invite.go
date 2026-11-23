package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Mời user vào project (POST /admin/projects/:id/invite, auth/admin/invite.ts). Khác Medplum (đã duyệt/đề xuất): không có super admin (chỉ project của người gọi),
// DTO chặt kiểu, bỏ ba trường deprecated (accessPolicy, access, admin ở mức gốc: dùng membership.*), lỗi gửi thư trả lỗi rõ ràng, và không tạo hồ sơ theo
// conditionalCreate theo email (mỗi lời mời tạo hồ sơ mới; mời lại cùng user bị chặn bởi kiểm trùng membership).

// InviteMembership là phần ProjectMembership mà lời mời được đặt. profile (nếu có) phải thuộc project và đúng resourceType.
type InviteMembership struct {
	Profile    *fhir.Reference
	Admin      *bool
	Active     *bool
	ExternalID string
	// UserName là ProjectMembership.userName (SCIM userName); chỉ đặt khi tạo.
	UserName     string
	AccessPolicy *fhir.Reference
	Access       []fhir.ProjectMembershipAccess
}

// InviteRequest là yêu cầu mời (đã qua kiểm kiểu ở presentation).
type InviteRequest struct {
	ResourceType        string // Patient | Practitioner | RelatedPerson
	FirstName, LastName string
	Email, ExternalID   string
	Patient             *fhir.Reference // bắt buộc với RelatedPerson mới
	Password            string
	Scope               string // "project" | "server" | "" (mặc định theo loại)
	SendEmail           *bool
	Membership          InviteMembership
	Upsert              bool
	ForceNewMembership  bool
	MFARequired         *bool
}

var inviteResourceTypes = map[string]bool{"Patient": true, "Practitioner": true, "RelatedPerson": true}

// emailDomain trả phần sau @ (chữ thường).
func emailDomain(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return strings.ToLower(email[i+1:])
	}
	return ""
}

// checkPractitionerDomain áp danh sách chặn của máy chủ (ưu tiên hơn) và allowedPractitionerEmailDomain của project (rỗng: không hạn chế).
func (s *Service) checkPractitionerDomain(project *fhir.Project, email string) error {
	d := emailDomain(email)
	for _, b := range s.BlockedEmailDomains {
		if strings.EqualFold(strings.TrimSpace(b), d) {
			return domain.BadRequestAt("Email domain is not allowed for this project", "email")
		}
	}
	var allowed []string
	for _, st := range project.Setting {
		if st.Name == "allowedPractitionerEmailDomain" && st.ValueString != "" {
			allowed = append(allowed, strings.ToLower(strings.TrimSpace(st.ValueString)))
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	for _, a := range allowed {
		if a == d {
			return nil
		}
	}
	return domain.BadRequestAt("Email domain is not allowed for this project", "email")
}

// defaultPolicyFor chọn access policy mặc định của project cho loại hồ sơ (nil nếu project không cấu hình).
func defaultPolicyFor(p *fhir.Project, resourceType string, admin bool) *fhir.Reference {
	if resourceType == "Patient" && p.DefaultPatientAccessPolicy != nil {
		return p.DefaultPatientAccessPolicy
	}
	want := resourceType
	if resourceType == "Practitioner" && admin {
		want = "Admin"
	}
	for _, d := range p.DefaultAccessPolicies {
		if d.ProfileType == want {
			return d.AccessPolicy
		}
	}
	return nil
}

// AdminInvite mời user vào project của người gọi và trả membership. Mọi ghi dữ liệu (user, hồ sơ, membership) nằm trong một transaction giữ khóa theo (project, email hoặc
// externalId); thư gửi SAU khi ghi xong. Gửi thư lỗi thì dữ liệu vẫn còn và lỗi nói rõ điều đó (dùng resetpassword để gửi lại).
func (s *Service) AdminInvite(ctx context.Context, a *AuthState, projectID string, req InviteRequest) (*fhir.ProjectMembership, error) {
	caller, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	project, err := s.Projects.Get(ctx, caller.ID)
	if err != nil {
		return nil, err
	}
	if !inviteResourceTypes[req.ResourceType] {
		return nil, domain.BadRequestAt("Resource type is required", "resourceType")
	}
	if strings.TrimSpace(req.FirstName) == "" {
		return nil, domain.BadRequestAt("First name is required", "firstName")
	}
	if strings.TrimSpace(req.LastName) == "" {
		return nil, domain.BadRequestAt("Last name is required", "lastName")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" && req.ExternalID == "" {
		return nil, domain.BadRequest("Either email or externalId is required")
	}
	if email != "" && !domain.IsEmail(email) {
		return nil, domain.BadRequestAt("Valid email address is required", "email")
	}
	if email == "" && strings.TrimSpace(req.ExternalID) == "" {
		return nil, domain.BadRequestAt("External ID cannot be empty", "externalId")
	}
	if req.ExternalID != "" && strings.TrimSpace(req.ExternalID) == "" {
		return nil, domain.BadRequestAt("External ID cannot be empty", "externalId")
	}
	if req.Scope != "" && req.Scope != "project" && req.Scope != "server" {
		return nil, domain.BadRequestAt(`scope must be "project" or "server"`, "scope")
	}
	if req.ResourceType == "RelatedPerson" && req.Membership.Profile == nil {
		if req.Patient == nil || !strings.HasPrefix(req.Patient.Reference, "Patient/") || strings.Count(req.Patient.Reference, "/") != 1 || len(req.Patient.Reference) == len("Patient/") {
			if req.Patient == nil {
				return nil, domain.BadRequestAt("Patient is required to create a RelatedPerson", "patient")
			}
			return nil, domain.BadRequestAt("Patient must be a reference to a Patient resource", "patient")
		}
	}
	if req.Password != "" {
		if err := domain.ValidatePassword(req.Password); err != nil {
			return nil, err
		}
	}
	if req.ResourceType == "Practitioner" && email != "" {
		if err := s.checkPractitionerDomain(project, email); err != nil {
			return nil, err
		}
	}
	sendMail := email != "" && (req.SendEmail == nil || *req.SendEmail)
	if sendMail && !s.mailReady() {
		return nil, domain.BadRequest("Email sending is not configured; set sendEmail to false")
	}
	// phạm vi user: externalId luôn project; Patient mặc định project, còn lại server (invite.ts:232-238)
	scope := req.Scope
	if scope == "" {
		scope = "server"
		if req.ResourceType == "Patient" {
			scope = "project"
		}
	}
	if req.ExternalID != "" {
		scope = "project"
	}
	// kiểm access policy của lời mời: tồn tại và thuộc project
	pid := project.ID
	if req.Membership.AccessPolicy != nil {
		if err := s.checkAccessPolicy(ctx, pid, req.Membership.AccessPolicy, "membership.accessPolicy"); err != nil {
			return nil, err
		}
	}
	for _, ac := range req.Membership.Access {
		if err := s.checkAccessPolicy(ctx, pid, ac.Policy, "membership.access"); err != nil {
			return nil, err
		}
	}
	// mật khẩu: do người gọi đặt hoặc ngẫu nhiên (người dùng phải đặt qua link)
	pw, userSetsPassword := req.Password, req.Password == ""
	if userSetsPassword {
		pw = domain.GenerateSecret(16)
	} else if err := s.CheckPwned(ctx, pw); err != nil {
		return nil, err
	}
	hash, err := s.HashPassword(pw)
	if err != nil {
		return nil, err
	}

	var (
		member  *fhir.ProjectMembership
		user    *fhir.User
		newUser bool
	)
	lockKey := "invite:" + pid + ":" + email
	if email == "" {
		lockKey = "invite:" + pid + ":ext:" + req.ExternalID
	}
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		if err := tx.Locker.Lock(ctx, lockKey); err != nil {
			return err
		}
		projectRef := "Project/" + pid
		if email != "" {
			find := func(proj string) (*fhir.User, error) {
				if proj == "" {
					return tx.Users.FindGlobal(ctx, email)
				}
				return tx.Users.FindInProject(ctx, email, proj)
			}
			wantProj, otherProj := pid, ""
			if scope == "server" {
				wantProj, otherProj = "", pid
			}
			if !req.ForceNewMembership {
				// đã là member của project qua user ở phạm vi KHÁC thì không mời lại (409)
				if other, err := find(otherProj); err != nil {
					return err
				} else if other != nil {
					ms, err := tx.Memberships.ListAllForUser(ctx, "User/"+other.ID, 0)
					if err != nil {
						return err
					}
					for _, m := range ms {
						if refOf(m.Project) == projectRef {
							return domain.Conflict("User is already a member of this project")
						}
					}
				}
			}
			if user, err = find(wantProj); err != nil {
				return err
			}
		}
		if user == nil {
			u := &fhir.User{ResourceType: "User", FirstName: req.FirstName, LastName: req.LastName, Email: email, PasswordHash: hash, MfaRequired: req.MFARequired}
			if scope == "project" {
				u.Project, u.Meta = &fhir.Reference{Reference: projectRef}, &fhir.Meta{Project: pid}
			}
			out, err := tx.Resources.Create(ctx, u)
			if err != nil {
				return err
			}
			user, newUser = out.(*fhir.User), true
		}

		// membership hiện có của user trong project
		existing, err := tx.Memberships.ListAllForUser(ctx, "User/"+user.ID, 0)
		if err != nil {
			return err
		}
		var current *fhir.ProjectMembership
		for i := range existing {
			if refOf(existing[i].Project) == projectRef {
				current = &existing[i]
			}
		}
		reuse := current != nil && !req.ForceNewMembership
		if reuse && !req.Upsert {
			return domain.Conflict("User is already a member of this project")
		}
		// hồ sơ
		var profileRef *fhir.Reference
		if reuse {
			// upsert: giữ hồ sơ của membership hiện có; chỉ định hồ sơ khác là 409
			if p := req.Membership.Profile; p != nil && p.Reference != refOf(current.Profile) {
				return domain.Conflict("User is already a member of this project with a different profile")
			}
			profileRef = current.Profile
		} else if p := req.Membership.Profile; p != nil {
			rt, id, ok := strings.Cut(p.Reference, "/")
			if !ok || rt != req.ResourceType {
				return domain.BadRequestAt("Profile resourceType does not match request", "membership.profile")
			}
			res, err := tx.Resources.Get(ctx, rt, id)
			if err != nil {
				if domain.KindOf(err) == domain.KindNotFound {
					return domain.BadRequestAt("Profile does not belong to project", "membership.profile")
				}
				return err
			}
			if m := res.GetMeta(); m == nil || m.Project != pid {
				return domain.BadRequestAt("Profile does not belong to project", "membership.profile")
			}
			profileRef = &fhir.Reference{Reference: p.Reference}
		} else {
			var patient *fhir.Reference
			if req.ResourceType == "RelatedPerson" {
				res, err := tx.Resources.Get(ctx, "Patient", idOf(req.Patient.Reference))
				if err != nil {
					if domain.KindOf(err) == domain.KindNotFound {
						return domain.BadRequestAt("Patient "+req.Patient.Reference+" does not exist", "patient")
					}
					return err
				}
				if m := res.GetMeta(); m == nil || m.Project != pid {
					return domain.BadRequestAt("Patient "+req.Patient.Reference+" does not belong to project", "patient")
				}
				patient = &fhir.Reference{Reference: req.Patient.Reference}
			}
			name := []fhir.HumanName{{Given: []string{req.FirstName}, Family: req.LastName}}
			var telecom []fhir.ContactPoint
			if email != "" {
				telecom = []fhir.ContactPoint{{System: "email", Use: "work", Value: email}}
			}
			meta := &fhir.Meta{Project: pid}
			var prof fhir.Resource
			switch req.ResourceType {
			case "Patient":
				prof = &fhir.Patient{ResourceType: "Patient", Meta: meta, Name: name, Telecom: telecom}
			case "Practitioner":
				prof = &fhir.Practitioner{ResourceType: "Practitioner", Meta: meta, Name: name, Telecom: telecom}
			default:
				prof = &fhir.RelatedPerson{ResourceType: "RelatedPerson", Meta: meta, Name: name, Telecom: telecom, Patient: patient}
			}
			out, err := tx.Resources.Create(ctx, prof)
			if err != nil {
				return err
			}
			profileRef = &fhir.Reference{Reference: req.ResourceType + "/" + out.GetID()}
		}

		// membership
		ext := req.Membership.ExternalID
		if ext == "" {
			ext = req.ExternalID
		}
		if ext != "" {
			if other, err := tx.Memberships.FindByExternalID(ctx, ext, projectRef); err != nil {
				return err
			} else if other != nil && (current == nil || other.ID != current.ID) {
				return domain.Conflict("externalId already exists in this project")
			}
		}
		policy, access := req.Membership.AccessPolicy, req.Membership.Access
		admin := domain.IsTrue(req.Membership.Admin)
		if policy == nil && len(access) == 0 {
			if def := defaultPolicyFor(project, req.ResourceType, admin); def != nil {
				if req.ResourceType == "RelatedPerson" {
					access = []fhir.ProjectMembershipAccess{{Policy: def, Parameter: []fhir.ProjectMembershipAccessParameter{{Name: "patient", ValueReference: profilePatient(req, profileRef)}}}}
				} else {
					policy = def
				}
			}
		}
		if reuse { // upsert: giữ id, project, user, profile
			member, err = tx.Memberships.Mutate(ctx, current.ID, func(m *fhir.ProjectMembership) error {
				m.Admin, m.Active, m.ExternalId, m.AccessPolicy, m.Access = req.Membership.Admin, req.Membership.Active, ext, policy, access
				return nil
			})
			return err
		}
		nm := &fhir.ProjectMembership{
			ResourceType: "ProjectMembership", Meta: &fhir.Meta{Project: pid}, Project: newRef("Project", pid, project.Name), User: newRef("User", user.ID, ""),
			Profile: profileRef, InvitedBy: a.Membership.User, Admin: req.Membership.Admin, Active: req.Membership.Active, ExternalId: ext, UserName: req.Membership.UserName, AccessPolicy: policy, Access: access,
		}
		out, err := tx.Resources.Create(ctx, nm)
		if err != nil {
			return err
		}
		member = out.(*fhir.ProjectMembership)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sendMail {
		if err := s.sendInviteMail(ctx, user, project, newUser && userSetsPassword); err != nil {
			s.Logger.ErrorContext(ctx, "không gửi được thư mời", "user_id", user.ID, "error", err)
			return nil, domain.BadRequest("Invitation created (membership " + member.ID + ") but the email could not be sent; use resetpassword to send it again")
		}
	}
	return member, nil
}

// profilePatient trả tham chiếu Patient làm tham số của access policy RelatedPerson.
func profilePatient(req InviteRequest, _ *fhir.Reference) *fhir.Reference {
	if req.Patient != nil {
		return &fhir.Reference{Reference: req.Patient.Reference}
	}
	return nil
}

func (s *Service) sendInviteMail(ctx context.Context, user *fhir.User, project *fhir.Project, needsPassword bool) error {
	if needsPassword {
		req, secret, err := s.IssueSecurityRequest(ctx, user, domain.SecurityRequestInvite, "")
		if err != nil {
			return err
		}
		return s.Mailer.Send(ctx, inviteMessage(s.Mail, user.Email, project.Name, s.Mail.link("setpassword/"+req.ID+"/"+secret), true))
	}
	return s.Mailer.Send(ctx, inviteMessage(s.Mail, user.Email, project.Name, s.Mail.link(""), false))
}
