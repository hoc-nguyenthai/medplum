package application

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Quản trị project qua HTTP (P7, /admin/projects/*). Quyết định đã duyệt: không có super admin (người gọi chỉ quản trị project của chính mình, :projectId lệch là 403),
// chỉ Bearer của người dùng, membership ngoài project hoặc không tồn tại đều là 403 chung, `secret` bị che khi đọc, không xóa/hạ quyền admin cuối cùng.

// MinClientSecret là độ dài tối thiểu của secret client do người gọi đặt (Q16).
const MinClientSecret = 32

const maskValue = "********"

var errAdminForbidden = domain.Forbidden("Forbidden")

// adminProject kiểm người gọi là admin hoạt động của project và projectID (nếu có) đúng là project đó; trả project của người gọi.
func adminProject(a *AuthState, projectID string) (*fhir.Project, error) {
	if a == nil || a.Project == nil || a.Membership == nil {
		return nil, domain.Unauthorized("Unauthorized")
	}
	if !domain.IsTrue(a.Membership.Admin) {
		return nil, errAdminForbidden
	}
	if projectID != "" && projectID != a.Project.ID {
		return nil, errAdminForbidden
	}
	return a.Project, nil
}

// AdminProjectView là phần project mà admin đọc được; giá trị bí mật bị che.
type AdminProjectView struct {
	ID      string                `json:"id" binding:"required" example:"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"` // id của Project (UUID)
	Name    string                `json:"name" binding:"required" example:"Bệnh viện A"`                        // tên Project
	Setting []fhir.ProjectSetting `json:"setting,omitempty"`                                                    // cấu hình Project; giá trị bí mật bị che, chỉ có khi Project có cấu hình
	Secret  []fhir.ProjectSetting `json:"secret,omitempty"`                                                     // các secret của Project; giá trị luôn bị che (maskValue), chỉ có khi có secret
	Site    []fhir.ProjectSite    `json:"site,omitempty"`                                                       // các site của Project; googleClientSecret bị che, chỉ có khi Project có site
}

func maskSettings(in []fhir.ProjectSetting) []fhir.ProjectSetting {
	out := make([]fhir.ProjectSetting, len(in))
	for i, st := range in {
		out[i] = fhir.ProjectSetting{Name: st.Name}
		if st.ValueString != "" || st.ValueBoolean != nil || st.ValueInteger != nil || st.ValueDecimal != "" {
			out[i].ValueString = maskValue
		}
	}
	return out
}

func maskSites(in []fhir.ProjectSite) []fhir.ProjectSite {
	out := make([]fhir.ProjectSite, len(in))
	copy(out, in)
	for i := range out {
		if out[i].GoogleClientSecret != "" {
			out[i].GoogleClientSecret = maskValue
		}
		if out[i].RecaptchaSecretKey != "" {
			out[i].RecaptchaSecretKey = maskValue
		}
	}
	return out
}

func projectView(p *fhir.Project) *AdminProjectView {
	return &AdminProjectView{ID: p.ID, Name: p.Name, Setting: p.Setting, Secret: maskSettings(p.Secret), Site: maskSites(p.Site)}
}

// AdminGetProject đọc project của người gọi (GET /admin/projects/:id). Đọc lại từ kho để không dùng bản trong token.
func (s *Service) AdminGetProject(ctx context.Context, a *AuthState, projectID string) (*AdminProjectView, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	cur, err := s.Projects.Get(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return projectView(cur), nil
}

// settingRule là cách kiểm một setting cho phép quản trị qua HTTP (Q15: allowlist theo tên). Setting khác (ví dụ allow-dangerous-redirect, legacyExternalAuth) chỉ super admin/CLI đổi được.
type settingRule func(fhir.ProjectSetting) string

var adminSettingRules = map[string]settingRule{
	"mfaRequired": func(st fhir.ProjectSetting) string {
		if st.ValueBoolean == nil {
			return "mfaRequired must be a boolean"
		}
		return ""
	},
	"allowedMfaMethods": func(st fhir.ProjectSetting) string {
		if st.ValueString == "" {
			return "allowedMfaMethods must be a string"
		}
		for _, m := range strings.Split(st.ValueString, ",") {
			if m = strings.TrimSpace(m); m != "totp" && m != "email" {
				return `allowedMfaMethods must list "totp" and/or "email"`
			}
		}
		return ""
	},
	"allowedPractitionerEmailDomain": func(st fhir.ProjectSetting) string {
		d := strings.ToLower(strings.TrimSpace(st.ValueString))
		if d == "" || strings.ContainsAny(d, "@ /") || !strings.Contains(d, ".") || len(d) > 253 {
			return "allowedPractitionerEmailDomain must be a domain name such as example.org"
		}
		return ""
	},
	"appName": func(st fhir.ProjectSetting) string {
		if strings.TrimSpace(st.ValueString) == "" || utf8.RuneCountInString(st.ValueString) > 100 {
			return "appName must be a string of 1 to 100 characters"
		}
		return ""
	},
}

// AdminReplaceSettings thay các setting thuộc allowlist bằng danh sách mới (POST /admin/projects/:id/settings). Setting ngoài allowlist đang có được GIỮ NGUYÊN,
// setting ngoài allowlist trong yêu cầu bị từ chối (400), tên trùng bị từ chối. Trả project đã cập nhật.
func (s *Service) AdminReplaceSettings(ctx context.Context, a *AuthState, projectID string, in []fhir.ProjectSetting) (*AdminProjectView, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, st := range in {
		rule, ok := adminSettingRules[st.Name]
		if !ok {
			return nil, domain.BadRequestAt("Setting is not allowed: "+st.Name, "setting")
		}
		if seen[st.Name] && st.Name != "allowedPractitionerEmailDomain" { // tên miền được phép có thể nhiều mục
			return nil, domain.BadRequestAt("Duplicate setting: "+st.Name, "setting")
		}
		seen[st.Name] = true
		if msg := rule(st); msg != "" {
			return nil, domain.BadRequestAt(msg, "setting")
		}
	}
	out, err := s.Projects.Mutate(ctx, p.ID, func(cur *fhir.Project) error {
		kept := make([]fhir.ProjectSetting, 0, len(cur.Setting)+len(in))
		for _, st := range cur.Setting {
			if _, managed := adminSettingRules[st.Name]; !managed {
				kept = append(kept, st)
			}
		}
		cur.Setting = append(kept, in...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return projectView(out), nil
}

// ---- client

// AdminClientRequest là body của POST /admin/projects/:id/client. Chỉ nhận các trường an toàn: identityProvider (bề mặt SSRF), signInForm và status
// không được đặt qua HTTP ở đợt này.
type AdminClientRequest struct {
	Name         string
	Description  string
	RedirectURIs []string
	PkceOptional bool
	GrantTypes   []string
	AccessPolicy *fhir.Reference
	Secret       string
}

var adminGrantTypes = map[string]bool{
	"authorization_code": true, "client_credentials": true, "refresh_token": true,
	GrantTypeTokenExchange: true, GrantTypePreAuthorizedCode: true,
}

// AdminCreateClient tạo ClientApplication và membership của nó trong project của người gọi. Trả client kèm secret (chỉ lần này).
func (s *Service) AdminCreateClient(ctx context.Context, a *AuthState, projectID string, req AdminClientRequest) (*fhir.ClientApplication, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, domain.BadRequestAt("Client name is required", "name")
	}
	if req.Secret != "" && (len(req.Secret) < MinClientSecret || len(req.Secret) > 256) {
		return nil, domain.BadRequestAt("secret must be between 32 and 256 characters", "secret")
	}
	for _, g := range req.GrantTypes {
		if !adminGrantTypes[g] {
			return nil, domain.BadRequestAt("Unsupported grantType: "+g, "grantType")
		}
	}
	for _, u := range req.RedirectURIs {
		parsed, err := url.Parse(u)
		if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return nil, domain.BadRequestAt("Invalid redirect URI", "redirectUris")
		}
	}
	return s.CreateClient(ctx, p.ID, NewClient{Name: req.Name, Description: req.Description, RedirectURIs: req.RedirectURIs, PkceOptional: req.PkceOptional,
		GrantTypes: req.GrantTypes, AccessPolicy: req.AccessPolicy, Secret: req.Secret})
}

// ---- members

// adminMember đọc membership và bảo đảm nó thuộc project của người gọi; không tồn tại và ngoài project đều là 403 chung (Q17), không cho dò id.
func (s *Service) adminMember(ctx context.Context, a *AuthState, projectID, id string) (*fhir.ProjectMembership, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	m, err := s.Memberships.Get(ctx, id)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, errAdminForbidden
		}
		return nil, err
	}
	if refOf(m.Project) != "Project/"+p.ID {
		return nil, errAdminForbidden
	}
	return m, nil
}

// AdminGetMember đọc một membership của project.
func (s *Service) AdminGetMember(ctx context.Context, a *AuthState, projectID, id string) (*fhir.ProjectMembership, error) {
	return s.adminMember(ctx, a, projectID, id)
}

// lastAdminGuard báo lỗi nếu việc bỏ quyền admin (hoặc vô hiệu hóa/xóa) của `leaving` làm project không còn admin hoạt động nào.
func (s *Service) lastAdminGuard(ctx context.Context, projectRef string, leaving *fhir.ProjectMembership) error {
	admins, err := s.Memberships.ListActiveAdmins(ctx, projectRef)
	if err != nil {
		return err
	}
	for _, m := range admins {
		if m.ID != leaving.ID {
			return nil
		}
	}
	return domain.BadRequest("Cannot remove the last administrator of the project")
}

// checkAccessPolicy bảo đảm tham chiếu AccessPolicy tồn tại và thuộc project.
func (s *Service) checkAccessPolicy(ctx context.Context, projectID string, ref *fhir.Reference, field string) error {
	if ref == nil || ref.Reference == "" {
		return domain.BadRequestAt("Invalid access policy", field)
	}
	pol, err := s.AccessPolicies.Get(ctx, idOf(ref.Reference))
	if err != nil || pol.Meta == nil || pol.Meta.Project != projectID {
		return domain.BadRequestAt("Invalid access policy", field)
	}
	return nil
}

// AdminUpdateMember sửa membership (POST /admin/projects/:id/members/:mid). Chỉ các trường admin, active, externalId, accessPolicy, access được đổi;
// user, profile, project phải giữ nguyên (400 nếu body đổi chúng). accessPolicy phải thuộc project; không hạ quyền/vô hiệu hóa admin cuối cùng.
func (s *Service) AdminUpdateMember(ctx context.Context, a *AuthState, projectID, id string, body *fhir.ProjectMembership) (*fhir.ProjectMembership, error) {
	cur, err := s.adminMember(ctx, a, projectID, id)
	if err != nil {
		return nil, err
	}
	if body == nil || body.ResourceType != "ProjectMembership" || body.ID != id {
		return nil, errAdminForbidden
	}
	if refOf(body.User) != refOf(cur.User) || refOf(body.Profile) != refOf(cur.Profile) || refOf(body.Project) != refOf(cur.Project) {
		return nil, domain.BadRequest("Cannot change user, profile or project of a membership")
	}
	pid := a.Project.ID
	if body.AccessPolicy != nil {
		if err := s.checkAccessPolicy(ctx, pid, body.AccessPolicy, "accessPolicy"); err != nil {
			return nil, err
		}
	}
	for _, ac := range body.Access {
		if err := s.checkAccessPolicy(ctx, pid, ac.Policy, "access"); err != nil {
			return nil, err
		}
	}
	wasAdmin, nowAdmin := domain.IsTrue(cur.Admin), domain.IsTrue(body.Admin)
	wasActive, nowActive := cur.Active == nil || *cur.Active, body.Active == nil || *body.Active
	if wasAdmin && wasActive && (!nowAdmin || !nowActive) {
		if err := s.lastAdminGuard(ctx, "Project/"+pid, cur); err != nil {
			return nil, err
		}
	}
	if body.ExternalId != "" && body.ExternalId != cur.ExternalId {
		if other, err := s.Memberships.FindByExternalID(ctx, body.ExternalId, "Project/"+pid); err != nil {
			return nil, err
		} else if other != nil { // other.ID != id luôn đúng ở đây: externalId mới khác của chính membership này
			return nil, domain.Conflict("externalId already exists in this project")
		}
	}
	return s.Memberships.Mutate(ctx, id, func(m *fhir.ProjectMembership) error {
		m.Admin, m.Active, m.ExternalId, m.AccessPolicy, m.Access = body.Admin, body.Active, body.ExternalId, body.AccessPolicy, body.Access
		return nil
	})
}

// AdminDeleteMember xóa membership; user thuộc project (User.project) mà hết membership thì bị xóa cùng, user ở phạm vi server thì giữ. Không xóa chủ project
// hay admin cuối cùng. Token đã cấp cho membership này bị từ chối ngay (Authenticate đọc membership mỗi lần gọi).
func (s *Service) AdminDeleteMember(ctx context.Context, a *AuthState, projectID, id string) error {
	m, err := s.adminMember(ctx, a, projectID, id)
	if err != nil {
		return err
	}
	pid := a.Project.ID
	project, err := s.Projects.Get(ctx, pid)
	if err != nil {
		return err
	}
	if project.Owner != nil && m.User != nil && refOf(project.Owner) == refOf(m.User) {
		return domain.BadRequest("Cannot delete the owner of the project")
	}
	if domain.IsTrue(m.Admin) && (m.Active == nil || *m.Active) {
		if err := s.lastAdminGuard(ctx, "Project/"+pid, m); err != nil {
			return err
		}
	}
	return s.Tx.InTx(ctx, func(tx TxRepos) error {
		if err := tx.Memberships.Delete(ctx, id); err != nil {
			return err
		}
		userRef := refOf(m.User)
		if !strings.HasPrefix(userRef, "User/") {
			return nil
		}
		user, err := tx.Users.Get(ctx, idOf(userRef))
		if err != nil {
			if domain.KindOf(err) == domain.KindNotFound {
				return nil
			}
			return err
		}
		if refOf(user.Project) != "Project/"+pid {
			return nil // user dùng chung nhiều project: chỉ xóa membership
		}
		rest, err := tx.Memberships.ListAllForUser(ctx, userRef, 1)
		if err != nil {
			return err
		}
		if len(rest) == 0 {
			return tx.Users.Delete(ctx, user.ID)
		}
		return nil
	})
}

// AdminSetPassword đặt mật khẩu cho user của project (POST /admin/projects/setpassword): kiểm pwned, băm, thu hồi mọi login. User không thuộc project (kể cả user
// ở phạm vi server) là 400 `User not found`.
func (s *Service) AdminSetPassword(ctx context.Context, a *AuthState, email, password string) error {
	p, err := adminProject(a, "")
	if err != nil {
		return err
	}
	if err := s.SetUserPassword(ctx, email, p.ID, password); err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return domain.BadRequest("User not found")
		}
		return err
	}
	return nil
}

// adminMemberUser đọc user của membership và bảo đảm nó thuộc ĐÚNG project của người gọi: user ở phạm vi server hay project khác là 403 (admin project không được
// đụng vào tài khoản dùng chung).
func (s *Service) adminMemberUser(ctx context.Context, a *AuthState, projectID, id string) (*fhir.ProjectMembership, *fhir.User, error) {
	m, err := s.adminMember(ctx, a, projectID, id)
	if err != nil {
		return nil, nil, err
	}
	ref := refOf(m.User)
	if !strings.HasPrefix(ref, "User/") {
		return nil, nil, errAdminForbidden
	}
	u, err := s.Users.Get(ctx, idOf(ref))
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, nil, errAdminForbidden
		}
		return nil, nil, err
	}
	if refOf(u.Project) != "Project/"+a.Project.ID {
		return nil, nil, errAdminForbidden
	}
	return m, u, nil
}

// AdminResetMemberPassword gửi thư đặt lại mật khẩu cho user của membership (POST .../members/:mid/resetpassword). Khác `/auth/resetpassword` công khai, đây là hành
// động có chủ đích của admin nên lỗi được báo rõ: thiếu email, chưa cấu hình thư hoặc gửi thư lỗi đều là lỗi (không nuốt).
func (s *Service) AdminResetMemberPassword(ctx context.Context, a *AuthState, projectID, id string) error {
	_, u, err := s.adminMemberUser(ctx, a, projectID, id)
	if err != nil {
		return err
	}
	if u.Email == "" {
		return domain.BadRequest("User does not have an email address")
	}
	if ext, err := s.ExternalAuthFor(ctx, strings.ToLower(u.Email)); err != nil {
		return err
	} else if ext != nil {
		return domain.BadRequest("Cannot reset password for external auth. Contact your system administrator.")
	}
	if !s.mailReady() {
		return domain.BadRequest("Email sending is not configured")
	}
	req, secret, err := s.IssueSecurityRequest(ctx, u, domain.SecurityRequestReset, "")
	if err != nil {
		return err
	}
	if err := s.Mailer.Send(ctx, resetPasswordMessage(s.Mail, u.Email, s.Mail.link("setpassword/"+req.ID+"/"+secret))); err != nil {
		s.Logger.ErrorContext(ctx, "không gửi được thư đặt lại mật khẩu (admin)", "user_id", u.ID, "error", err)
		return domain.Internal("Could not send email")
	}
	return nil
}

// AdminResetMemberMFA gỡ một yếu tố MFA của user (POST .../members/:mid/mfa/reset): method mặc định totp; user phải đang có yếu tố đó. Gỡ totp thì secret bị thay bằng
// secret ngẫu nhiên mới. Không kiểm "yếu tố cuối khi project bắt buộc MFA": đây là đường khôi phục, lần đăng nhập sau user bị buộc đăng ký lại.
func (s *Service) AdminResetMemberMFA(ctx context.Context, a *AuthState, projectID, id string, method any) error {
	if err := s.requireMFA(); err != nil {
		return err
	}
	m := domain.MFATotp
	if method != nil {
		str, ok := method.(string)
		if !ok || (str != domain.MFATotp && str != domain.MFAEmail) {
			return domain.BadRequestAt(`Method must be "totp" or "email"`, "method")
		}
		m = str
	}
	_, u, err := s.adminMemberUser(ctx, a, projectID, id)
	if err != nil {
		return err
	}
	if !domain.HasMFAMethod(u, m) {
		return domain.BadRequest("User is not enrolled in MFA method: " + m)
	}
	newSecret := ""
	if m == domain.MFATotp {
		raw, err := s.TOTP.GenerateSecret()
		if err != nil {
			return domain.InternalErr("sinh secret TOTP", err)
		}
		if newSecret, err = s.MFASecrets.Seal(raw); err != nil {
			return domain.InternalErr("mã hóa mfaSecret", err)
		}
	}
	if _, err := s.Users.Mutate(ctx, u.ID, func(x *fhir.User) error {
		left := []string{}
		for _, e := range domain.EnrolledMFAMethods(x) {
			if e != m {
				left = append(left, e)
			}
		}
		x.MfaEnrolled, x.MfaMethod = ptrBool(len(left) > 0), left
		if m == domain.MFATotp {
			x.MfaSecret = newSecret
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// ---- sites

const (
	maxSites       = 20
	maxSiteDomains = 20
)

// AdminReplaceSites thay toàn bộ Project.site (POST /admin/projects/:id/sites). `googleClientId` quyết định project nào nhận Google client nào (`/auth/google`) và
// `recaptchaSiteKey` quyết định project nào nhận khóa reCAPTCHA nào nên mỗi giá trị phải DUY NHẤT giữa các project (409) và `googleClientId` không được trùng client chính của
// máy chủ. Khóa bí mật bị che khi đọc (`********`): gửi lại đúng giá trị che nghĩa là GIỮ secret hiện có của site cùng tên. Kiểm duy nhất nằm trong transaction giữ khóa theo giá trị.
func (s *Service) AdminReplaceSites(ctx context.Context, a *AuthState, projectID string, in []fhir.ProjectSite) (*AdminProjectView, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	if len(in) > maxSites {
		return nil, domain.BadRequestAt("Too many sites", "site")
	}
	names := map[string]bool{}
	var locks []string
	googleIDs, siteKeys := map[string]bool{}, map[string]bool{}
	for i, st := range in {
		name := strings.TrimSpace(st.Name)
		if name == "" || utf8.RuneCountInString(name) > 100 {
			return nil, domain.BadRequestAt("Site name is required (1 to 100 characters)", "site")
		}
		if names[name] {
			return nil, domain.BadRequestAt("Duplicate site name: "+name, "site")
		}
		names[name] = true
		if len(st.Domain) > maxSiteDomains {
			return nil, domain.BadRequestAt("Too many domains", "site")
		}
		for _, d := range st.Domain {
			if d = strings.ToLower(strings.TrimSpace(d)); d == "" || strings.ContainsAny(d, "@/ :") || !strings.Contains(d, ".") || len(d) > 253 {
				return nil, domain.BadRequestAt("Invalid site domain: "+d, "site")
			}
		}
		if (st.GoogleClientId == "") != (st.GoogleClientSecret == "") {
			return nil, domain.BadRequestAt("googleClientId and googleClientSecret must be set together", "site")
		}
		if (st.RecaptchaSiteKey == "") != (st.RecaptchaSecretKey == "") {
			return nil, domain.BadRequestAt("recaptchaSiteKey and recaptchaSecretKey must be set together", "site")
		}
		for _, v := range []string{st.GoogleClientId, st.GoogleClientSecret, st.RecaptchaSiteKey, st.RecaptchaSecretKey} {
			if len(v) > 512 {
				return nil, domain.BadRequestAt("Site value too long", "site")
			}
		}
		if g := st.GoogleClientId; g != "" {
			if g == s.GoogleClientID {
				return nil, domain.Conflict("googleClientId is reserved by the server")
			}
			if googleIDs[g] {
				return nil, domain.BadRequestAt("Duplicate googleClientId: "+g, "site")
			}
			googleIDs[g] = true
			locks = append(locks, "site-google:"+g)
		}
		if k := st.RecaptchaSiteKey; k != "" {
			if siteKeys[k] {
				return nil, domain.BadRequestAt("Duplicate recaptchaSiteKey: "+k, "site")
			}
			siteKeys[k] = true
			locks = append(locks, "site-recaptcha:"+k)
		}
		in[i].Name = name
	}
	sort.Strings(locks) // thứ tự khóa cố định để hai yêu cầu song song không khóa chéo nhau
	var out *fhir.Project
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		for _, k := range locks {
			if err := tx.Locker.Lock(ctx, k); err != nil {
				return err
			}
		}
		for g := range googleIDs {
			others, err := tx.Projects.ByGoogleClientID(ctx, g, "")
			if err != nil {
				return err
			}
			for _, o := range others {
				if o.ID != p.ID {
					return domain.Conflict("googleClientId is already used by another project")
				}
			}
		}
		for k := range siteKeys {
			o, err := tx.Projects.FindByRecaptchaSiteKey(ctx, k, "")
			if err != nil {
				return err
			}
			if o != nil && o.ID != p.ID {
				return domain.Conflict("recaptchaSiteKey is already used by another project")
			}
		}
		var merr error
		out, merr = tx.Projects.Mutate(ctx, p.ID, func(cur *fhir.Project) error {
			next := make([]fhir.ProjectSite, len(in))
			copy(next, in)
			for i := range next {
				var old *fhir.ProjectSite
				for j := range cur.Site {
					if cur.Site[j].Name == next[i].Name {
						old = &cur.Site[j]
					}
				}
				if isMask(next[i].GoogleClientSecret) {
					if old == nil || old.GoogleClientSecret == "" {
						return domain.BadRequestAt("googleClientSecret has no existing value to keep", "site")
					}
					next[i].GoogleClientSecret = old.GoogleClientSecret
				}
				if isMask(next[i].RecaptchaSecretKey) {
					if old == nil || old.RecaptchaSecretKey == "" {
						return domain.BadRequestAt("recaptchaSecretKey has no existing value to keep", "site")
					}
					next[i].RecaptchaSecretKey = old.RecaptchaSecretKey
				}
			}
			cur.Site = next
			return nil
		})
		return merr
	})
	if err != nil {
		return nil, err
	}
	return projectView(out), nil
}

// isMask cho biết v là giá trị che do chính ta trả ra (không phải secret thật, nên so bằng thường).
func isMask(v string) bool { return v == maskValue }
