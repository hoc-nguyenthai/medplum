package application

import (
	"context"
	"errors"
	"sort"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/domain/scim"
	"trivita-fhir/pkg/fhir"
)

// SCIM 2.0 cho tài nguyên Users (P7). Quyết định đã duyệt (Q-SC-1..7): xác thực Basic (client id:secret) hoặc Bearer của client có membership admin, Basic chỉ cho
// /scim/v2; hồ sơ lâm sàng ghi vào kho `auth` như AdminInvite; Practitioner phải có chính sách mặc định; id ngoài project hay thuộc Bot/Client là 404; externalId
// trùng trong project là 409; IdP được đặt lại `active=true` (có log); bật theo cả cờ toàn cục lẫn `Project.features` chứa `scim`.

// ScimConfig là cấu hình SCIM.
type ScimConfig struct {
	// Enabled là công tắc toàn cục (mặc định tắt: route trả 404).
	Enabled bool
	// AllowBasic cho phép Authorization: Basic của ClientApplication (Okta chỉ có Basic).
	AllowBasic bool
	// MaxPage là cỡ trang mặc định và trần của `count`.
	MaxPage int
	// MaxPatchOps là số thao tác tối đa của một PATCH.
	MaxPatchOps int
}

func (c ScimConfig) page() int {
	if c.MaxPage > 0 {
		return c.MaxPage
	}
	return 100
}

var scimNotFound = &scim.ProblemError{Status: 404, Detail: "Not found"}

func scimBad(scimType, detail string) *scim.ProblemError {
	return &scim.ProblemError{Status: 400, ScimType: scimType, Detail: detail}
}

// ScimBasicAuth xác thực `Authorization: Basic` của ClientApplication cho /scim/v2 và trả trạng thái như một Bearer. Mọi thất bại là 401 chung.
func (s *Service) ScimBasicAuth(ctx context.Context, clientID, secret string) (*AuthState, error) {
	if !s.Scim.AllowBasic {
		return nil, domain.Unauthorized("Unauthorized")
	}
	client, oerr := s.AuthenticateClient(ctx, clientID, secret)
	if oerr != nil {
		return nil, domain.Unauthorized("Unauthorized")
	}
	m, oerr := s.clientMembership(ctx, client)
	if oerr != nil {
		return nil, domain.Unauthorized("Unauthorized")
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil || domain.InactiveReason(project, m) != "" {
		return nil, domain.Unauthorized("Unauthorized")
	}
	return &AuthState{Membership: m, Project: project}, nil
}

// scimProject kiểm người gọi là admin hoạt động và project bật SCIM.
func (s *Service) scimProject(ctx context.Context, a *AuthState) (*fhir.Project, error) {
	p, err := adminProject(a, "")
	if err != nil {
		return nil, err
	}
	cur, err := s.Projects.Get(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if !domain.ProjectHasFeature(cur, "scim") {
		return nil, domain.Forbidden("SCIM is not enabled for this project")
	}
	return cur, nil
}

func profileType(m *fhir.ProjectMembership) string {
	t, _, _ := strings.Cut(refOf(m.Profile), "/")
	return t
}

func scimManaged(m *fhir.ProjectMembership) bool {
	t := profileType(m)
	for _, r := range scim.ResourceTypes {
		if t == r {
			return true
		}
	}
	return false
}

// userNameOf là userName của membership: giá trị đã lưu, không thì id của hồ sơ.
func userNameOf(m *fhir.ProjectMembership) string {
	if m.UserName != "" {
		return m.UserName
	}
	return idOf(refOf(m.Profile))
}

func (s *Service) toScim(m *fhir.ProjectMembership, u *fhir.User) scim.User {
	out := scim.User{Schemas: []string{scim.SchemaUser}, ID: m.ID, UserName: userNameOf(m), UserType: profileType(m),
		Name: &scim.Name{GivenName: u.FirstName, FamilyName: u.LastName}}
	ext := m.ExternalId
	if ext == "" {
		ext = u.ExternalId
	}
	if ext != "" {
		out.ExternalID = &ext
	}
	if u.Email != "" {
		out.Emails = []scim.Email{{Value: u.Email}}
	}
	active := m.Active == nil || *m.Active
	out.Active = &active
	version := ""
	if m.Meta != nil {
		version = m.Meta.LastUpdated
	}
	out.Meta = &scim.Meta{ResourceType: "User", Version: version, LastModified: version, Location: strings.TrimRight(s.BaseURL, "/") + "/scim/v2/Users/" + m.ID}
	return out
}

// scimMember đọc membership của project mà SCIM quản lý; không tồn tại, ngoài project hay thuộc Bot/ClientApplication đều là 404 chung.
func (s *Service) scimMember(ctx context.Context, p *fhir.Project, id string) (*fhir.ProjectMembership, *fhir.User, error) {
	m, err := s.Memberships.Get(ctx, id)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, nil, scimNotFound
		}
		return nil, nil, err
	}
	if refOf(m.Project) != "Project/"+p.ID || !scimManaged(m) {
		return nil, nil, scimNotFound
	}
	if !strings.HasPrefix(refOf(m.User), "User/") {
		return nil, nil, scimNotFound
	}
	u, err := s.Users.Get(ctx, idOf(refOf(m.User)))
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, nil, scimNotFound
		}
		return nil, nil, err
	}
	return m, u, nil
}

// ScimListParams là tham số GET /Users; con trỏ nil là không gửi.
type ScimListParams struct {
	Filter     string
	StartIndex *int
	Count      *int
}

// ScimList liệt kê membership của project (Patient, Practitioner, RelatedPerson), lọc `userName eq` (so bằng không phân biệt hoa thường), sắp theo id, phân trang
// thật (startIndex từ 1, count mặc định và trần là MaxPage).
func (s *Service) ScimList(ctx context.Context, a *AuthState, params ScimListParams) (*scim.ListResponse, error) {
	p, err := s.scimProject(ctx, a)
	if err != nil {
		return nil, err
	}
	want, hasFilter, err := scim.ParseFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	all, err := s.Memberships.ListByProject(ctx, "Project/"+p.ID)
	if err != nil {
		return nil, err
	}
	var rows []fhir.ProjectMembership
	for _, m := range all {
		if !scimManaged(&m) || !strings.HasPrefix(refOf(m.User), "User/") {
			continue
		}
		if hasFilter && strings.ToLower(userNameOf(&m)) != want {
			continue
		}
		rows = append(rows, m)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	start, count := 1, s.Scim.page()
	if params.StartIndex != nil && *params.StartIndex > 1 {
		start = *params.StartIndex
	}
	if params.Count != nil {
		count = *params.Count
		if count < 0 {
			count = 0
		}
		if count > s.Scim.page() {
			count = s.Scim.page()
		}
	}
	out := &scim.ListResponse{Schemas: []string{scim.SchemaList}, TotalResults: len(rows), StartIndex: start, Resources: []scim.User{}}
	for i := start - 1; i < len(rows) && len(out.Resources) < count; i++ {
		u, err := s.Users.Get(ctx, idOf(refOf(rows[i].User)))
		if err != nil {
			if domain.KindOf(err) == domain.KindNotFound {
				continue
			}
			return nil, err
		}
		out.Resources = append(out.Resources, s.toScim(&rows[i], u))
	}
	out.ItemsPerPage = len(out.Resources)
	return out, nil
}

// ScimGet đọc một user theo id membership.
func (s *Service) ScimGet(ctx context.Context, a *AuthState, id string) (*scim.User, error) {
	p, err := s.scimProject(ctx, a)
	if err != nil {
		return nil, err
	}
	m, u, err := s.scimMember(ctx, p, id)
	if err != nil {
		return nil, err
	}
	out := s.toScim(m, u)
	return &out, nil
}

// ScimCreate tạo user bằng đúng đường của AdminInvite (không gửi thư): chỉ firstName, lastName, email, externalId, userName, userType và active được dùng; mọi trường
// khác của body (admin, accessPolicy, groups, roles, extension...) bị bỏ qua. Patient và Practitioner phải có chính sách mặc định của project.
func (s *Service) ScimCreate(ctx context.Context, a *AuthState, in scim.User) (*scim.User, error) {
	p, err := s.scimProject(ctx, a)
	if err != nil {
		return nil, err
	}
	if len(in.Emails) == 0 || strings.TrimSpace(in.Emails[0].Value) == "" {
		return nil, scimBad("invalidValue", "emails[0].value is required")
	}
	if strings.TrimSpace(in.UserName) == "" {
		return nil, scimBad("invalidValue", "userName is required")
	}
	rt := scim.NormalizeUserType(in.UserType)
	switch rt {
	case "Patient":
		if p.DefaultPatientAccessPolicy == nil {
			return nil, scimBad("invalidValue", "Missing default patient access policy")
		}
	case "Practitioner":
		if defaultPolicyFor(p, "Practitioner", false) == nil {
			return nil, scimBad("invalidValue", "Missing default practitioner access policy")
		}
	}
	var first, last string
	if in.Name != nil {
		first, last = in.Name.GivenName, in.Name.FamilyName
	}
	ext := ""
	if in.ExternalID != nil {
		ext = *in.ExternalID
	}
	no := false
	m, err := s.AdminInvite(ctx, a, p.ID, InviteRequest{ResourceType: rt, FirstName: first, LastName: last, Email: in.Emails[0].Value, ExternalID: ext, SendEmail: &no,
		Membership: InviteMembership{UserName: in.UserName, Active: in.Active}})
	if err != nil {
		return nil, err
	}
	u, err := s.Users.Get(ctx, idOf(refOf(m.User)))
	if err != nil {
		return nil, err
	}
	out := s.toScim(m, u)
	return &out, nil
}

// ScimReplace là PUT: dùng id của đường dẫn (id trong body có và khác là 400).
func (s *Service) ScimReplace(ctx context.Context, a *AuthState, id string, in scim.User) (*scim.User, error) {
	p, err := s.scimProject(ctx, a)
	if err != nil {
		return nil, err
	}
	if in.ID != "" && in.ID != id {
		return nil, scimBad("invalidValue", "id in body does not match id in path")
	}
	m, u, err := s.scimMember(ctx, p, id)
	if err != nil {
		return nil, err
	}
	return s.scimApply(ctx, a, p, m, u, in)
}

// ScimPatch là PATCH: dựng SCIM từ trạng thái hiện tại, áp các thao tác (ApplyPatch), rồi ghi như PUT.
func (s *Service) ScimPatch(ctx context.Context, a *AuthState, id string, patch scim.PatchOp) (*scim.User, error) {
	p, err := s.scimProject(ctx, a)
	if err != nil {
		return nil, err
	}
	m, u, err := s.scimMember(ctx, p, id)
	if err != nil {
		return nil, err
	}
	next, err := scim.ApplyPatch(s.toScim(m, u), patch, s.Scim.MaxPatchOps)
	if err != nil {
		return nil, err
	}
	return s.scimApply(ctx, a, p, m, u, next)
}

// scimApply ghi các thay đổi của `want` lên User (nếu thuộc project) và membership trong MỘT transaction. Chỉ ghi: firstName, lastName, externalId, email (User thuộc
// project), active và externalId (membership). Đổi email: emailVerified=false, vô hiệu mọi security request, thu hồi mọi login, cùng transaction (lỗi giữa chừng hoàn tác hết).
// User ở phạm vi server không ghi được: đổi email là 403, các trường khác bị bỏ qua.
func (s *Service) scimApply(ctx context.Context, a *AuthState, p *fhir.Project, m *fhir.ProjectMembership, user *fhir.User, want scim.User) (*scim.User, error) {
	writable := refOf(user.Project) == "Project/"+p.ID
	newEmail := strings.ToLower(user.Email)
	if len(want.Emails) > 0 && strings.TrimSpace(want.Emails[0].Value) != "" {
		newEmail = strings.ToLower(strings.TrimSpace(want.Emails[0].Value))
		if !domain.IsEmail(newEmail) {
			return nil, scimBad("invalidValue", "emails[0].value is not a valid email address")
		}
	}
	emailChanged := newEmail != strings.ToLower(user.Email)
	if emailChanged && !writable {
		return nil, domain.Forbidden("Cannot change the email of a user outside this project")
	}
	if emailChanged {
		if clash, err := s.Users.FindInProject(ctx, newEmail, p.ID); err != nil {
			return nil, err
		} else if clash != nil && clash.ID != user.ID {
			return nil, domain.Conflict("Email already registered")
		}
	}
	ext := ""
	if want.ExternalID != nil {
		ext = *want.ExternalID
	}
	if ext != m.ExternalId && ext != "" {
		if other, err := s.Memberships.FindByExternalID(ctx, ext, "Project/"+p.ID); err != nil {
			return nil, err
		} else if other != nil && other.ID != m.ID {
			return nil, domain.Conflict("externalId already exists in this project")
		}
	}
	if want.Active != nil && !*want.Active {
		if a.Membership != nil && m.ID == a.Membership.ID {
			return nil, scimBad("invalidValue", "Cannot deactivate the membership of the caller")
		}
		if p.Owner != nil && refOf(p.Owner) == refOf(m.User) {
			return nil, scimBad("invalidValue", "Cannot deactivate the owner of the project")
		}
		if domain.IsTrue(m.Admin) && (m.Active == nil || *m.Active) {
			if err := s.lastAdminGuard(ctx, "Project/"+p.ID, m); err != nil {
				return nil, err
			}
		}
	}
	if want.Active != nil && *want.Active && m.Active != nil && !*m.Active {
		s.Logger.WarnContext(ctx, "SCIM kích hoạt lại membership đã tắt", "audit", true, "membership", m.ID, "actor", refOf(a.Membership.User))
	}
	var outM *fhir.ProjectMembership
	var outU *fhir.User
	err := s.Tx.InTx(ctx, func(tx TxRepos) error {
		outU = user
		if writable {
			u, err := tx.Users.Mutate(ctx, user.ID, func(x *fhir.User) error {
				changed := false
				if want.Name != nil && (x.FirstName != want.Name.GivenName || x.LastName != want.Name.FamilyName) {
					x.FirstName, x.LastName, changed = want.Name.GivenName, want.Name.FamilyName, true
				}
				if x.ExternalId != ext {
					x.ExternalId, changed = ext, true
				}
				if emailChanged {
					x.Email, x.EmailVerified, changed = newEmail, ptrBool(false), true
				}
				if !changed {
					return ErrNoChange
				}
				return nil
			})
			if err != nil {
				return err
			}
			outU = u
		}
		mm, err := tx.Memberships.Mutate(ctx, m.ID, func(x *fhir.ProjectMembership) error {
			changed := false
			if want.Active != nil && (x.Active == nil || *x.Active != *want.Active) {
				x.Active, changed = ptrBool(*want.Active), true
			}
			if x.ExternalId != ext {
				x.ExternalId, changed = ext, true
			}
			if !changed {
				return ErrNoChange
			}
			return nil
		})
		if err != nil {
			return err
		}
		outM = mm
		if emailChanged {
			if err := s.retireSecurityRequests(ctx, tx.SecurityRequests, user, func(*fhir.UserSecurityRequest) bool { return true }); err != nil {
				return err
			}
			return revokeUserLoginsWith(ctx, tx.Logins, user)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			return nil, domain.Conflict("Resource was modified concurrently, retry")
		}
		return nil, err
	}
	out := s.toScim(outM, outU)
	return &out, nil
}

// ScimDelete xóa membership bằng use case của P7.3 (xóa luôn User thuộc project nếu là membership cuối; không xóa chủ project hay admin cuối). Thêm: không xóa
// membership của chính người gọi.
func (s *Service) ScimDelete(ctx context.Context, a *AuthState, id string) error {
	p, err := s.scimProject(ctx, a)
	if err != nil {
		return err
	}
	m, _, err := s.scimMember(ctx, p, id)
	if err != nil {
		return err
	}
	if a.Membership != nil && m.ID == a.Membership.ID {
		return scimBad("invalidValue", "Cannot delete the membership of the caller")
	}
	return s.AdminDeleteMember(ctx, a, p.ID, id)
}
