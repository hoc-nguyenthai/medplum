package application

import (
	"context"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// callerUserRef lấy tham chiếu User của người gọi, cho các route quản lý chính tài khoản mình (auth/utils.ts:69): membership phải
// là của một User (không phải ClientApplication), và login không được giới hạn bởi scope SMART; ngược lại 403.
func callerUserRef(a *AuthState) (string, error) {
	ref := refOf(a.Membership.User)
	if !strings.HasPrefix(ref, "User/") || domain.HasSmartResourceScopes(a.Login.Scope) {
		return "", domain.Forbidden("Forbidden")
	}
	return ref, nil
}

// RevokeSession thu hồi một login của chính người gọi (auth/revoke.ts). Login của user khác, hoặc (với login không phải mật khẩu
// hay Google) không thuộc membership của người gọi trong project hiện tại, trả 404 để không lộ sự tồn tại.
func (s *Service) RevokeSession(ctx context.Context, a *AuthState, loginID string) error {
	userRef, err := callerUserRef(a)
	if err != nil {
		return err
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return err
	}
	if refOf(login.User) != userRef {
		return domain.NotFound()
	}
	if !domain.CanManageUserAccount(a.Login) {
		ok := false
		if login.Membership != nil {
			if m, merr := s.Memberships.Get(ctx, idOf(login.Membership.Reference)); merr == nil {
				ok = refOf(m.Project) == "Project/"+a.Project.ID
			}
		}
		if !ok {
			s.Logger.WarnContext(ctx, "revoke denied", "login", login.ID, "project", a.Project.ID, "auth_method", a.Login.AuthMethod)
			return domain.NotFound()
		}
	}
	return s.RevokeLogin(ctx, login)
}

// Session là một phiên đăng nhập còn hiệu lực của user (auth/me security.sessions).
type Session struct {
	ID            string          `json:"id"`
	LastUpdated   string          `json:"lastUpdated"`
	AuthMethod    string          `json:"authMethod"`
	RemoteAddress string          `json:"remoteAddress,omitempty"`
	Browser       string          `json:"browser,omitempty"`
	OS            string          `json:"os,omitempty"`
	Project       *fhir.Reference `json:"project,omitempty"`
}

// UserSecurity là phần `security` của /auth/me.
type UserSecurity struct {
	MfaEnrolled bool                     `json:"mfaEnrolled"`
	MfaRequired bool                     `json:"mfaRequired"`
	Sessions    []Session                `json:"sessions"`
	Memberships []fhir.ProjectMembership `json:"memberships"`
}

// Me là phản hồi của GET /auth/me. Profile chỉ có resourceType và id (quyết định D-10: kho auth tách khỏi kho lâm sàng nên
// không đọc được hồ sơ Practitioner/Patient; client lấy hồ sơ đầy đủ từ fhir-server). accessPolicy chưa dựng ở P2.
type Me struct {
	User       *fhir.User              `json:"user,omitempty"`
	Project    *fhir.Project           `json:"project"`
	Membership *fhir.ProjectMembership `json:"membership"`
	Profile    map[string]string       `json:"profile"`
	Config     *fhir.UserConfiguration `json:"config"`
	Security   *UserSecurity           `json:"security,omitempty"`
}

// GetMe dựng phản hồi /auth/me (auth/me.ts:41).
func (s *Service) GetMe(ctx context.Context, a *AuthState) (*Me, error) {
	m, p := a.Membership, a.Project
	me := &Me{
		Project:    &fhir.Project{ResourceType: "Project", ID: p.ID, Name: p.Name, Features: p.Features, Description: p.Description, StrictMode: p.StrictMode, SuperAdmin: p.SuperAdmin},
		Membership: &fhir.ProjectMembership{ResourceType: "ProjectMembership", ID: m.ID, Identifier: m.Identifier, User: m.User, Profile: m.Profile, Admin: m.Admin},
	}
	profile := map[string]string{}
	if rt, id, ok := strings.Cut(refOf(m.Profile), "/"); ok {
		profile["resourceType"], profile["id"] = rt, id
	}
	me.Profile = profile

	cfg, err := s.userConfiguration(ctx, p, m)
	if err != nil {
		return nil, err
	}
	me.Config = cfg

	if strings.HasPrefix(refOf(m.User), "User/") {
		user, err := s.Users.Get(ctx, idOf(refOf(m.User)))
		if err != nil {
			return nil, err
		}
		me.User = &fhir.User{ResourceType: "User", ID: user.ID, Email: user.Email, Identifier: user.Identifier}
		// Chi tiết bảo mật chỉ cho login quản lý được tài khoản (không bị giới hạn bởi SMART scope).
		if !domain.HasSmartResourceScopes(a.Login.Scope) {
			sec, err := s.userSecurity(ctx, a, user)
			if err != nil {
				return nil, err
			}
			me.Security = sec
		}
	}
	return me, nil
}

func (s *Service) userConfiguration(ctx context.Context, p *fhir.Project, m *fhir.ProjectMembership) (*fhir.UserConfiguration, error) {
	cfg := &fhir.UserConfiguration{ResourceType: "UserConfiguration"}
	if m.UserConfiguration != nil {
		c, err := s.UserConfigs.Get(ctx, idOf(m.UserConfiguration.Reference))
		if err != nil {
			return nil, err
		}
		cfg = c
	}
	if cfg.Menu == nil {
		cfg.Menu = domain.DefaultMenu(p, m)
	}
	return cfg, nil
}

func (s *Service) userSecurity(ctx context.Context, a *AuthState, user *fhir.User) (*UserSecurity, error) {
	userRef := "User/" + user.ID
	all, err := s.Memberships.ListForUser(ctx, userRef, "Project/"+a.Project.ID, maxMemberships)
	if err != nil {
		return nil, err
	}
	sec := &UserSecurity{MfaEnrolled: domain.IsTrue(user.MfaEnrolled), MfaRequired: domain.IsMFARequired(user, a.Project), Memberships: []fhir.ProjectMembership{}}
	for _, m := range all {
		sec.Memberships = append(sec.Memberships, fhir.ProjectMembership{ResourceType: "ProjectMembership", ID: m.ID, Identifier: m.Identifier, Profile: m.Profile, Admin: m.Admin})
	}
	// Login không quản lý được tài khoản chỉ thấy phiên thuộc các membership của mình trong project này.
	var only map[string]bool
	if !domain.CanManageUserAccount(a.Login) {
		only = map[string]bool{}
		for _, m := range all {
			only["ProjectMembership/"+m.ID] = true
		}
	}
	sec.Sessions, err = s.sessions(ctx, userRef, only)
	return sec, err
}

// sessions liệt kê các login cập nhật trong giờ qua đã cấp token và chưa thu hồi (auth/me.ts:210).
func (s *Service) sessions(ctx context.Context, userRef string, onlyMemberships map[string]bool) ([]Session, error) {
	logins, err := s.Logins.ListByUserSince(ctx, userRef, s.Clock.Now().Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	out := []Session{}
	for _, l := range logins {
		if l.Membership == nil || l.Membership.Reference == "" || !domain.IsTrue(l.Granted) || domain.IsTrue(l.Revoked) {
			continue // login chưa cấp token hoặc đã thu hồi không phải phiên
		}
		if onlyMemberships != nil && !onlyMemberships[l.Membership.Reference] {
			continue
		}
		ss := Session{ID: l.ID, AuthMethod: l.AuthMethod, RemoteAddress: l.RemoteAddress, Project: l.Project}
		if l.Meta != nil {
			ss.LastUpdated = l.Meta.LastUpdated
		}
		if l.UserAgent != "" && s.UserAgents != nil {
			ss.Browser, ss.OS = s.UserAgents.Parse(l.UserAgent)
		}
		out = append(out, ss)
	}
	return out, nil
}
