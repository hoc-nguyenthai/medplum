package application

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Các thao tác quản trị dùng bởi CLI (`auth seed project`, `client create`, `user set-password`, `purge-logins`) cho tới khi
// có /admin/* qua HTTP (P7). Dùng cùng use case với HTTP sau này, không viết lại logic.

// maxProjectNameLength là độ dài tối đa tên project (Medplum chặn 72 ở /auth/newproject).
const maxProjectNameLength = 72

// NewUser là dữ liệu tạo User.
type NewUser struct {
	FirstName, LastName, Email, Password string
	ProjectID                            string // rỗng hoặc "new": user không gắn project
}

// PrepareUser kiểm tra và băm mật khẩu, trả User chưa ghi (auth/newuser.ts: createUser). Email được hạ chữ thường. Mật khẩu có
// trong cơ sở dữ liệu rò rỉ bị từ chối (lỗi gắn trường `password`).
func (s *Service) PrepareUser(ctx context.Context, req NewUser) (*fhir.User, error) {
	if strings.TrimSpace(req.Email) == "" {
		return nil, domain.BadRequestAt("Email is required", "email")
	}
	if err := domain.ValidatePassword(req.Password); err != nil {
		return nil, err
	}
	if err := s.CheckPwned(ctx, req.Password); err != nil {
		if domain.Is(err, domain.KindBadRequest, domain.PwnedMessage) {
			return nil, domain.BadRequestAt(domain.PwnedMessage, "password")
		}
		return nil, err
	}
	hash, err := s.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	u := &fhir.User{ResourceType: "User", FirstName: req.FirstName, LastName: req.LastName, Email: strings.ToLower(req.Email), PasswordHash: hash}
	if req.ProjectID != "" && req.ProjectID != "new" {
		u.Project = &fhir.Reference{Reference: "Project/" + req.ProjectID}
		u.Meta = &fhir.Meta{Project: req.ProjectID}
	}
	return u, nil
}

// CreateUser tạo User (không trùng email trong cùng phạm vi project).
func (s *Service) CreateUser(ctx context.Context, req NewUser) (*fhir.User, error) {
	u, err := s.PrepareUser(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.createUserLocked(ctx, u, req.ProjectID)
}

// createUserLocked ghi User mới sau khi kiểm trùng email trong cùng phạm vi, trong một transaction giữ khóa theo (phạm vi, email).
func (s *Service) createUserLocked(ctx context.Context, u *fhir.User, projectID string) (*fhir.User, error) {
	// Kho không có ràng buộc duy nhất cho email: kiểm tra trùng rồi tạo phải nằm trong transaction với khóa theo (phạm vi, email),
	// nếu không hai đăng ký song song cùng email đều thành công (S16).
	scope := "global"
	if projectID != "" && projectID != "new" {
		scope = projectID
	}
	var created *fhir.User
	var err error
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		if err := tx.Locker.Lock(ctx, "user-email:"+scope+":"+u.Email); err != nil {
			return err
		}
		var existing *fhir.User
		var ferr error
		if scope == "global" {
			existing, ferr = tx.Users.FindGlobal(ctx, u.Email)
		} else {
			existing, ferr = tx.Users.FindInProject(ctx, u.Email, scope)
		}
		if ferr != nil {
			return ferr
		}
		if existing != nil {
			return domain.BadRequestAt("Email already registered", "email")
		}
		out, err := tx.Resources.Create(ctx, u)
		if err != nil {
			return err
		}
		created = out.(*fhir.User)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// ProjectInitResult là kết quả tạo project.
type ProjectInitResult struct {
	Project *fhir.Project
	// Client là client mặc định của project; Client.Secret chỉ có ở đây (hiển thị một lần cho người vận hành).
	Client     *fhir.ClientApplication
	Profile    *fhir.Practitioner // nil nếu không có admin
	Membership *fhir.ProjectMembership
}

// ProjectInit là tham số tạo project.
type ProjectInit struct {
	Name     string
	Admin    *fhir.User // tùy chọn: thành viên admin đầu tiên
	Features []string
}

// CreateProject tạo project theo đúng trình tự của Medplum (fhir/operations/projectinit.ts), qua w (nên là writer của một
// transaction để thất bại giữa chừng thì hoàn tác hết): (1) sinh trước id project; (2) tạo AccessPolicy mặc định cho từng loại
// profile (meta.project = id đó); (3) tạo Project một lần, đã kèm defaultAccessPolicies, strictMode=true; (4) client mặc định
// và membership của nó; (5) nếu có admin: Practitioner và membership admin.
func (s *Service) CreateProject(ctx context.Context, w ResourceWriter, in ProjectInit) (*ProjectInitResult, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxProjectNameLength { // đếm ký tự, không phải byte: tên tiếng Việt nhiều byte
		return nil, domain.BadRequestAt(fmt.Sprintf("Project name must be between 1 and %d characters", maxProjectNameLength), "projectName")
	}
	projectID := domain.NewID()

	var defaults []fhir.ProjectDefaultAccessPolicies
	for _, profileType := range domain.DefaultPolicyProfileTypes {
		pol, _ := domain.DefaultAccessPolicy(profileType)
		pol.ResourceType = "AccessPolicy"
		pol.Meta = &fhir.Meta{Project: projectID}
		out, err := w.Create(ctx, &pol)
		if err != nil {
			return nil, err
		}
		defaults = append(defaults, fhir.ProjectDefaultAccessPolicies{ProfileType: profileType, AccessPolicy: newRef("AccessPolicy", out.GetID(), pol.Name)})
	}

	project := &fhir.Project{
		ResourceType: "Project", ID: projectID, Name: name, StrictMode: ptrTrue(),
		Features: in.Features, DefaultAccessPolicies: defaults,
	}
	if in.Admin != nil {
		project.Owner = newRef("User", in.Admin.ID, in.Admin.Email)
	}
	created, err := w.Create(ctx, project)
	if err != nil {
		return nil, err
	}
	project = created.(*fhir.Project)

	client, err := s.createClient(ctx, w, NewClient{Project: project, Name: project.Name + " Default Client", Description: "Default client for " + project.Name})
	if err != nil {
		return nil, err
	}
	res := &ProjectInitResult{Project: project, Client: client}
	if in.Admin != nil {
		prac := &fhir.Practitioner{
			ResourceType: "Practitioner", Meta: &fhir.Meta{Project: project.ID},
			Name: []fhir.HumanName{{Given: []string{in.Admin.FirstName}, Family: in.Admin.LastName}},
		}
		if in.Admin.Email != "" {
			prac.Telecom = []fhir.ContactPoint{{System: "email", Use: "work", Value: in.Admin.Email}}
		}
		profile, err := w.Create(ctx, prac)
		if err != nil {
			return nil, err
		}
		m, err := w.Create(ctx, &fhir.ProjectMembership{
			ResourceType: "ProjectMembership", Meta: &fhir.Meta{Project: project.ID},
			Project: newRef("Project", project.ID, project.Name), User: newRef("User", in.Admin.ID, in.Admin.Email),
			Profile: newRef("Practitioner", profile.GetID(), ""), Admin: ptrTrue(),
		})
		if err != nil {
			return nil, err
		}
		res.Profile, res.Membership = profile.(*fhir.Practitioner), m.(*fhir.ProjectMembership)
	}
	return res, nil
}

// SeedProjectRequest là yêu cầu tạo project đầu tiên cùng admin của nó.
type SeedProjectRequest struct {
	Name                              string
	AdminEmail, AdminFirst, AdminLast string
	AdminPassword                     string // chỉ cần khi admin chưa tồn tại
	Features                          []string
}

// SeedProject tạo (nếu chưa có) User admin và project của nó trong MỘT transaction. Admin đã tồn tại (không gắn project) thì được
// dùng lại và mật khẩu không bị đổi.
func (s *Service) SeedProject(ctx context.Context, req SeedProjectRequest) (*ProjectInitResult, error) {
	email := strings.ToLower(strings.TrimSpace(req.AdminEmail))
	if email == "" {
		return nil, domain.BadRequestAt("Admin email is required", "email")
	}
	existing, err := s.FindUserInScope(ctx, email, "")
	if err != nil {
		return nil, err
	}
	var prepared *fhir.User
	if existing == nil {
		// băm và kiểm tra mật khẩu NGOÀI transaction (có gọi mạng)
		prepared, err = s.PrepareUser(ctx, NewUser{FirstName: req.AdminFirst, LastName: req.AdminLast, Email: email, Password: req.AdminPassword})
		if err != nil {
			return nil, err
		}
	}
	var result *ProjectInitResult
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		admin := existing
		if existing == nil {
			// kiểm lại trong transaction để hai lần chạy đồng thời không tạo hai admin
			again, err := tx.Users.FindGlobal(ctx, email)
			if err != nil {
				return err
			}
			if again != nil {
				return domain.Conflict("Admin user was created concurrently")
			}
			out, err := tx.Resources.Create(ctx, prepared)
			if err != nil {
				return err
			}
			admin = out.(*fhir.User)
		}
		result, err = s.CreateProject(ctx, tx.Resources, ProjectInit{Name: req.Name, Admin: admin, Features: req.Features})
		return err
	})
	return result, err
}

// NewClient là tham số tạo ClientApplication.
type NewClient struct {
	Project      *fhir.Project
	Name         string
	Description  string
	RedirectURIs []string
	PkceOptional bool
	GrantTypes   []string
	// AccessPolicy (tùy chọn) là policy của membership client; mặc định là policy ClientApplication của project.
	AccessPolicy *fhir.Reference
	// Secret (tùy chọn) là secret do người gọi đặt; rỗng thì sinh ngẫu nhiên 32 byte. Người gọi phải kiểm độ dài trước (AdminCreateClient làm việc này).
	Secret string
}

// createClient tạo client với secret ngẫu nhiên 32 byte và membership của nó (user = profile = client), như createClient của
// Medplum (admin/client.ts).
func (s *Service) createClient(ctx context.Context, w ResourceWriter, req NewClient) (*fhir.ClientApplication, error) {
	c := &fhir.ClientApplication{
		ResourceType: "ClientApplication", Meta: &fhir.Meta{Project: req.Project.ID},
		Name: req.Name, Description: req.Description, Secret: clientSecret(req.Secret),
		RedirectUris: req.RedirectURIs, GrantType: req.GrantTypes,
	}
	if req.PkceOptional {
		c.PkceOptional = ptrTrue()
	}
	out, err := w.Create(ctx, c)
	if err != nil {
		return nil, err
	}
	c = out.(*fhir.ClientApplication)
	policy := req.AccessPolicy
	if policy == nil {
		for _, d := range req.Project.DefaultAccessPolicies {
			if d.ProfileType == "ClientApplication" {
				policy = d.AccessPolicy
			}
		}
	}
	ref := newRef("ClientApplication", c.ID, c.Name)
	_, err = w.Create(ctx, &fhir.ProjectMembership{
		ResourceType: "ProjectMembership", Meta: &fhir.Meta{Project: req.Project.ID},
		Project: newRef("Project", req.Project.ID, req.Project.Name), User: ref, Profile: ref, AccessPolicy: policy,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// CreateClient tạo ClientApplication cho một project có sẵn, kiểm project tồn tại và (nếu chỉ định) access policy thuộc đúng
// project đó. Client và membership được ghi trong một transaction.
func (s *Service) CreateClient(ctx context.Context, projectID string, req NewClient) (*fhir.ClientApplication, error) {
	project, err := s.Projects.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	req.Project = project
	if strings.TrimSpace(req.Name) == "" {
		return nil, domain.BadRequestAt("Client name is required", "name")
	}
	if req.AccessPolicy != nil {
		pol, err := s.AccessPolicies.Get(ctx, idOf(req.AccessPolicy.Reference))
		if err != nil || pol.Meta == nil || pol.Meta.Project != project.ID {
			return nil, domain.BadRequestAt("Invalid access policy", "accessPolicy")
		}
	}
	var client *fhir.ClientApplication
	err = s.Tx.InTx(ctx, func(tx TxRepos) (e error) {
		client, e = s.createClient(ctx, tx.Resources, req)
		return e
	})
	return client, err
}

// SetUserPassword đặt lại mật khẩu của một user (thay /admin/*/setpassword): kiểm pwned, băm, ghi và thu hồi mọi login.
func (s *Service) SetUserPassword(ctx context.Context, email, projectID, password string) error {
	user, err := s.FindUserInScope(ctx, email, projectID) // lệnh quản trị: đúng phạm vi được chỉ định, không rơi về phạm vi khác
	if err != nil {
		return err
	}
	if user == nil {
		return domain.NotFound()
	}
	if err := domain.ValidatePassword(password); err != nil {
		return err
	}
	return s.SetPassword(ctx, user, password, nil)
}

// PurgeLogins xóa vĩnh viễn các Login có lastUpdated < before (dryRun: chỉ đếm). before phải ở quá khứ. Trả số login khớp.
func (s *Service) PurgeLogins(ctx context.Context, before time.Time, dryRun bool) (int, error) {
	if !before.Before(s.Clock.Now()) {
		return 0, domain.BadRequest("before phải ở quá khứ")
	}
	ids, err := s.Logins.ListIDsUpdatedBefore(ctx, before)
	if err != nil {
		return 0, err
	}
	if dryRun {
		return len(ids), nil
	}
	done := 0
	for _, id := range ids {
		if err := s.Logins.Expunge(ctx, id); err != nil && !domain.Is(err, domain.KindNotFound, "") {
			return done, err
		}
		done++
	}
	return done, nil
}

func clientSecret(given string) string {
	if given != "" {
		return given
	}
	return domain.GenerateSecret(32)
}

var featureName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// SetProjectFeature bật hoặc tắt một tính năng trong Project.features (lệnh quản trị). Bật hai lần hoặc tắt tính năng không có là không làm gì.
func (s *Service) SetProjectFeature(ctx context.Context, projectID, feature string, enable bool) error {
	if !featureName.MatchString(feature) {
		return domain.BadRequestAt("Invalid feature name", "feature")
	}
	_, err := s.Projects.Mutate(ctx, projectID, func(p *fhir.Project) error {
		has := false
		kept := make([]string, 0, len(p.Features)+1)
		for _, f := range p.Features {
			if f == feature {
				has = true
				if !enable {
					continue
				}
			}
			kept = append(kept, f)
		}
		if has == enable {
			return ErrNoChange
		}
		if enable {
			kept = append(kept, feature)
		}
		p.Features = kept
		return nil
	})
	return err
}
