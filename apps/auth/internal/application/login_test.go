package application_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

const goodPassword = "mat-khau-dung-123"

// fixture là một project với user có mật khẩu, một membership và một client của project.
type fixture struct {
	p      *fhir.Project
	u      *fhir.User
	m      *fhir.ProjectMembership
	client *fhir.ClientApplication
}

func (e *env) pwUser(email string, mut func(*fhir.User)) *fhir.User {
	h, err := e.hasher.Hash(goodPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.user(email, func(u *fhir.User) {
		u.PasswordHash = h
		if mut != nil {
			mut(u)
		}
	})
}

func (e *env) fixture() fixture {
	p := e.project(nil)
	u := e.pwUser("an@example.org", nil)
	return fixture{
		p: p, u: u, m: e.membership(u, p, nil),
		client: put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "App", Meta: &fhir.Meta{Project: p.ID}, RedirectUris: []string{"https://app/cb"}}),
	}
}

func req(mut func(*domain.LoginRequest)) domain.LoginRequest {
	r := domain.LoginRequest{AuthMethod: domain.AuthPassword, Email: "an@example.org", Password: goodPassword, Scope: "openid", Nonce: "n"}
	if mut != nil {
		mut(&r)
	}
	return r
}

func TestTryLoginSingleMembership(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		l, err := e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) {
			r.RemoteAddress = "1.2.3.4"
			r.UserAgent = "UA"
			r.ResourceType = "Practitioner"
		}))
		if err != nil {
			t.Fatal(err)
		}
		if l.Membership == nil || l.Membership.Reference != "ProjectMembership/"+f.m.ID || l.Project == nil || l.Project.Reference != "Project/"+f.p.ID {
			t.Fatalf("phải tự chọn membership duy nhất: %+v", l)
		}
		if l.Code != "" || l.Cookie != "" || l.RefreshSecret != "" || l.AuthMethod != "password" || l.Scope != "openid" || l.Nonce != "n" ||
			l.RemoteAddress != "1.2.3.4" || l.UserAgent != "UA" || l.ProfileType != "Practitioner" || l.User.Reference != "User/"+f.u.ID || l.AuthTime == "" {
			t.Errorf("%+v", l)
		}
		if domain.IsTrue(l.Granted) || domain.IsTrue(l.Revoked) {
			t.Error("chưa cấp token, chưa thu hồi")
		}
		stored, _ := e.r.Logins.Get(ctx, l.ID)
		if stored.Membership == nil {
			t.Error("phải ghi vào kho")
		}
		// email không phân biệt hoa thường
		if _, err := e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Email = "AN@Example.ORG" })); err != nil {
			t.Errorf("%v", err)
		}
	})
}

func TestTryLoginRefreshSecret(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		for scope, want := range map[string]bool{"openid": false, "openid offline_access": true, "openid offline": true} {
			l, err := e.svc.TryLogin(context.Background(), req(func(r *domain.LoginRequest) { r.Scope = scope }))
			if err != nil || (l.RefreshSecret != "") != want || (want && len(l.RefreshSecret) != 64) {
				t.Errorf("%q: %v %q", scope, err, l.RefreshSecret)
			}
		}
		l, _ := e.svc.TryLogin(context.Background(), req(func(r *domain.LoginRequest) { r.Remember = true }))
		if l.RefreshSecret == "" {
			t.Error("remember (cũ) cũng cấp refresh secret")
		}
	})
}

func TestTryLoginBadCredentials(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.fixture()
		_, err := e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Password = "mat-khau-sai-xxx" }))
		wantErr(t, err, domain.KindBadRequest, "Email or password is invalid")
		if e.hasher.compares.Load() != 1 || e.hasher.dummies.Load() != 0 {
			t.Errorf("sai mật khẩu: so thật 1 lần (compares=%d dummies=%d)", e.hasher.compares.Load(), e.hasher.dummies.Load())
		}
		// email không tồn tại: CÙNG thông điệp và vẫn tốn một lần bcrypt giả (không lộ email có tồn tại)
		_, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Email = "khong-co@example.org" }))
		wantErr(t, err, domain.KindBadRequest, "Email or password is invalid")
		if e.hasher.dummies.Load() != 1 || e.hasher.compares.Load() != 1 {
			t.Errorf("user không có: phải so hash giả (compares=%d dummies=%d)", e.hasher.compares.Load(), e.hasher.dummies.Load())
		}
		// user chưa có mật khẩu (tạo qua Google): đăng nhập bằng mật khẩu luôn thất bại, vẫn so hash giả
		e.user("google@example.org", nil)
		_, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Email = "google@example.org" }))
		wantErr(t, err, domain.KindBadRequest, "Email or password is invalid")
		if e.hasher.dummies.Load() != 2 {
			t.Errorf("dummies=%d", e.hasher.dummies.Load())
		}
		// không có mật khẩu và không phải phương thức đã xác minh ở nơi khác
		_, err = e.svc.TryLogin(ctx, domain.LoginRequest{AuthMethod: "external", Email: "an@example.org", Scope: "openid"})
		if err != nil {
			t.Errorf("external được qua bước mật khẩu (đã xác minh ở nơi khác): %v", err)
		}
		_, err = e.svc.TryLogin(ctx, domain.LoginRequest{AuthMethod: "weird", Email: "an@example.org", Scope: "openid"})
		wantErr(t, err, domain.KindBadRequest, "Invalid authentication method")
		_, err = e.svc.TryLogin(ctx, domain.LoginRequest{AuthMethod: domain.AuthGoogle, HasGoogleCredentials: true, Email: "khong-co@example.org", Scope: "openid"})
		wantErr(t, err, domain.KindBadRequest, "User not found")
		// request không hợp lệ bị chặn từ đầu
		_, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Scope = "" }))
		wantErr(t, err, domain.KindBadRequest, "Invalid scope")
	})
}

func TestTryLoginMemberships(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		// chưa có membership nào của user khác: user không membership
		e.pwUser("none@example.org", nil)
		_, err := e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Email = "none@example.org" }))
		wantErr(t, err, domain.KindBadRequest, "User not found")
		l, err := e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.Email = "none@example.org"; r.AllowNoMembership = true }))
		if err != nil || l.Membership != nil || l.ID == "" {
			t.Errorf("cho phép chưa có membership: %+v %v", l, err)
		}
		// hai membership: trả login để chọn
		p2 := e.project(nil)
		m2 := e.membership(f.u, p2, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Patient/pt"} })
		l, err = e.svc.TryLogin(ctx, req(nil))
		if err != nil || l.Membership != nil {
			t.Fatalf("nhiều membership thì chưa chọn: %+v %v", l, err)
		}
		// lọc theo loại profile
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.ResourceType = "Patient" }))
		if err != nil || l.Membership == nil || l.Membership.Reference != "ProjectMembership/"+m2.ID {
			t.Errorf("profileType Patient: %+v %v", l, err)
		}
		// membershipId (đăng nhập ngoài, P6)
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.MembershipID = f.m.ID }))
		if err != nil || l.Membership == nil || l.Membership.Reference != "ProjectMembership/"+f.m.ID {
			t.Errorf("membershipId: %+v %v", l, err)
		}
		// forceUseFirstMembership: chọn cái đầu, và không panic khi danh sách rỗng
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.ForceUseFirstMembership = true }))
		if err != nil || l.Membership == nil {
			t.Errorf("force first: %+v %v", l, err)
		}
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) {
			r.Email = "none@example.org"
			r.ForceUseFirstMembership, r.AllowNoMembership = true, true
		}))
		if err != nil || l.Membership != nil {
			t.Errorf("force first với danh sách rỗng: %+v %v", l, err)
		}
		// project cụ thể: chỉ user của project đó
		inP := e.pwUser("an@example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + p2.ID} })
		e.membership(inP, p2, nil)
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.ProjectID = p2.ID }))
		if err != nil || l.User.Reference != "User/"+inP.ID || l.Membership == nil {
			t.Errorf("project p2: %+v %v", l, err)
		}
		// project lạ: user toàn cục vẫn được tìm thấy (rơi về), nhưng không có membership nào trong project đó
		_, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.ProjectID = domain.NewID() }))
		wantErr(t, err, domain.KindBadRequest, "User not found")
		_, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.ProjectID = domain.NewID(); r.Password = "mat-khau-sai-xxx" }))
		wantErr(t, err, domain.KindBadRequest, "Email or password is invalid")
		// Project/new: không cần membership, login mang Project/new
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) {
			r.Email = "none@example.org"
			r.ProjectID = "new"
			r.AllowNoMembership = true
		}))
		if err != nil || l.Project == nil || l.Project.Reference != "Project/new" || l.Membership != nil {
			t.Errorf("project new: %+v %v", l, err)
		}
	})
}

func TestTryLoginClientChecks(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		restricted := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Meta: &fhir.Meta{Project: f.p.ID},
			AllowedOrigin: []string{"https://app.example.org"}, RedirectUris: []string{"https://app.example.org/cb"}})
		withClient := func(id string, mut func(*domain.LoginRequest)) domain.LoginRequest {
			return req(func(r *domain.LoginRequest) {
				r.ClientID = id
				if mut != nil {
					mut(r)
				}
			})
		}
		l, err := e.svc.TryLogin(ctx, withClient(f.client.ID, nil))
		if err != nil || l.Client == nil || l.Client.Reference != "ClientApplication/"+f.client.ID {
			t.Fatalf("%+v %v", l, err)
		}
		_, err = e.svc.TryLogin(ctx, withClient("khong-co", nil))
		wantErr(t, err, domain.KindNotFound, "Not found")
		_, err = e.svc.TryLogin(ctx, withClient(restricted.ID, func(r *domain.LoginRequest) { r.Origin = "https://evil.example.org" }))
		wantErr(t, err, domain.KindBadRequest, "Invalid origin")
		if _, err = e.svc.TryLogin(ctx, withClient(restricted.ID, func(r *domain.LoginRequest) { r.Origin = "https://app.example.org" })); err != nil {
			t.Errorf("%v", err)
		}
		_, err = e.svc.TryLogin(ctx, withClient(restricted.ID, func(r *domain.LoginRequest) { r.RedirectURI = "https://app.example.org/cb/extra" }))
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
		if _, err = e.svc.TryLogin(ctx, withClient(restricted.ID, func(r *domain.LoginRequest) { r.RedirectURI = "https://app.example.org/cb" })); err != nil {
			t.Errorf("%v", err)
		}
		_, err = e.svc.TryLogin(ctx, withClient(f.client.ID, func(r *domain.LoginRequest) { r.CodeChallengeMethod = "S256" }))
		wantErr(t, err, domain.KindBadRequest, "Invalid code challenge")
		if l, err = e.svc.TryLogin(ctx, withClient(f.client.ID, func(r *domain.LoginRequest) { r.CodeChallenge, r.CodeChallengeMethod = "abc", "S256" })); err != nil ||
			l.CodeChallenge != "abc" || l.CodeChallengeMethod != "S256" {
			t.Errorf("%+v %v", l, err)
		}
		// client dựng sẵn medplum-cli (pkceOptional)
		if _, err = e.svc.TryLogin(ctx, withClient(domain.CLIClientID, func(r *domain.LoginRequest) { r.CodeChallengeMethod = "S256" })); err != nil {
			t.Errorf("pkceOptional bỏ qua kiểm tra: %v", err)
		}
		// launch
		_, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.LaunchID = "khong-co" }))
		wantErr(t, err, domain.KindNotFound, "")
		launch := put(e, &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch", Patient: &fhir.Reference{Reference: "Patient/p1"}})
		l, err = e.svc.TryLogin(ctx, req(func(r *domain.LoginRequest) { r.LaunchID = launch.ID }))
		if err != nil || l.Launch == nil || l.Launch.Reference != "SmartAppLaunch/"+launch.ID {
			t.Errorf("%+v %v", l, err)
		}
	})
}

// racyLogins đổi mật khẩu của user NGAY SAU khi Login được tạo, mô phỏng đổi mật khẩu xen vào giữa xác thực và tạo login.
type racyLogins struct {
	application.LoginRepository
	onCreate func()
}

func (r racyLogins) Create(ctx context.Context, l *fhir.Login) (*fhir.Login, error) {
	out, err := r.LoginRepository.Create(ctx, l)
	if err == nil {
		r.onCreate()
	}
	return out, err
}

func TestTryLoginRevokesWhenPasswordChangedMeanwhile(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		svc := *e.svc
		svc.Logins = racyLogins{e.r.Logins, func() {
			_, err := e.r.Users.Mutate(ctx, f.u.ID, func(u *fhir.User) error { u.PasswordHash = "$2b$10$mat-khau-moi-do-nguoi-khac-doi"; return nil })
			if err != nil {
				t.Error(err)
			}
		}}
		_, err := svc.TryLogin(ctx, req(nil))
		wantErr(t, err, domain.KindBadRequest, "Email or password is invalid")
		ids, _ := e.r.Logins.ListIDsByUser(ctx, "User/"+f.u.ID)
		if len(ids) != 1 {
			t.Fatalf("phải có đúng một login được tạo: %d", len(ids))
		}
		l, _ := e.r.Logins.Get(ctx, ids[0])
		if !domain.IsTrue(l.Revoked) || l.Membership != nil {
			t.Errorf("login tạo ra trong cửa sổ đua phải bị thu hồi và không có membership: %+v", l)
		}
	})
}

// ---- BuildLoginResult

type fakeMFA struct {
	enrolls, sends atomic.Int32
	enrollErr      error
	sendErr        error
}

func (f *fakeMFA) BuildEnrollment(context.Context, *fhir.Login, *fhir.User, *fhir.Project) (string, string, error) {
	f.enrolls.Add(1)
	return "otpauth://totp/x?secret=ABC", "data:image/png;base64,QR", f.enrollErr
}

func (f *fakeMFA) SendEmailCode(context.Context, *fhir.Login, *fhir.User, *fhir.Project) error {
	f.sends.Add(1)
	return f.sendErr
}

func (e *env) withMFA(m application.MFAGate) *application.Service {
	s := *e.svc
	s.MFA = m
	return &s
}

func TestBuildLoginResultForms(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		other := e.project(func(p *fhir.Project) { p.Name = "Tên mới" })
		m2 := e.membership(f.u, other, nil)

		// code
		l := e.login(f.u, func(l *fhir.Login) {
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + f.m.ID}
		})
		res, err := e.svc.BuildLoginResult(ctx, l)
		if err != nil || !e.codeStoredAs(t, l.ID, res) || res.Login != l.ID || res.Memberships != nil || res.MfaRequired {
			t.Errorf("code: %+v %v", res, err)
		}
		// chọn profile: project lấy mới từ kho, display = tên hiện tại
		l2 := e.login(f.u, nil)
		res, err = e.svc.BuildLoginResult(ctx, l2)
		if err != nil || res.Code != "" || len(res.Memberships) != 2 {
			t.Fatalf("chọn profile: %+v %v", res, err)
		}
		byID := map[string]application.MembershipChoice{}
		for _, c := range res.Memberships {
			byID[c.ID] = c
		}
		if c := byID[m2.ID]; c.Project == nil || c.Project.Reference != "Project/"+other.ID || c.Project.Display != "Tên mới" || c.Profile.Reference != "Practitioner/p1" {
			t.Errorf("%+v", c)
		}
		// project mới
		l3 := e.login(f.u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"} })
		res, err = e.svc.BuildLoginResult(ctx, l3)
		if err != nil || res.Login != l3.ID || res.Code != "" || res.Memberships != nil {
			t.Errorf("project new: %+v %v", res, err)
		}
		// login không có user
		_, err = e.svc.BuildLoginResult(ctx, &fhir.Login{})
		wantErr(t, err, domain.KindBadRequest, "User reference is missing")
		_, err = e.svc.BuildLoginResult(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/khong-co"}})
		wantErr(t, err, domain.KindNotFound, "")
	})
}

func TestBuildLoginResultMembershipWithUnreadableProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		u := e.pwUser("a@example.org", nil)
		e.membership(u, e.project(nil), nil)
		put(e, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/" + u.ID},
			Project: &fhir.Reference{Reference: "Project/khong-co"}, Profile: &fhir.Reference{Reference: "Practitioner/x"}})
		res, err := e.svc.BuildLoginResult(context.Background(), e.login(u, nil))
		if err != nil || len(res.Memberships) != 2 {
			t.Fatalf("%+v %v", res, err)
		}
		nilProjects := 0
		for _, c := range res.Memberships {
			if c.Project == nil {
				nilProjects++
			}
		}
		if nilProjects != 1 {
			t.Errorf("project không đọc được thì bỏ trường project (như Medplum): %d", nilProjects)
		}
	})
}

func TestBuildLoginResultMFA(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		mfaProject := e.project(func(p *fhir.Project) {
			p.Setting = []fhir.ProjectSetting{{Name: "mfaRequired", ValueBoolean: ptr(true)}, {Name: "allowedMfaMethods", ValueString: "totp,email"}}
		})
		pwLogin := func(u *fhir.User, mut func(*fhir.Login)) *fhir.Login {
			return e.login(u, func(l *fhir.Login) {
				l.Project = &fhir.Reference{Reference: "Project/" + mfaProject.ID}
				if mut != nil {
					mut(l)
				}
			})
		}
		fake := &fakeMFA{}
		svc := e.withMFA(fake)

		// bắt buộc MFA và chưa đăng ký: trả thông tin đăng ký, KHÔNG có code
		fresh := e.pwUser("fresh@example.org", nil)
		res, err := svc.BuildLoginResult(ctx, pwLogin(fresh, func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"} }))
		if err != nil || !res.MfaEnrollRequired || res.Code != "" || res.EnrollURI == "" || res.EnrollQRCode == "" ||
			strings.Join(res.AllowedMfaMethods, ",") != "totp,email" || fake.enrolls.Load() != 1 {
			t.Fatalf("enroll: %+v %v", res, err)
		}
		// đã đăng ký TOTP: yêu cầu xác minh, không có code, không gửi email
		totp := e.pwUser("totp@example.org", func(u *fhir.User) { u.MfaEnrolled = ptr(true); u.MfaMethod = []string{"totp"} })
		res, err = svc.BuildLoginResult(ctx, pwLogin(totp, func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"} }))
		if err != nil || !res.MfaRequired || res.Code != "" || res.Email != "totp@example.org" || strings.Join(res.MfaMethods, ",") != "totp" || fake.sends.Load() != 0 {
			t.Fatalf("totp: %+v %v", res, err)
		}
		// chỉ có email: gửi mã ngay, đúng một lần cho mỗi login (emailMfa có nghĩa là đã gửi)
		mailU := e.pwUser("mail@example.org", func(u *fhir.User) { u.MfaEnrolled = ptr(true); u.MfaMethod = []string{"email"} })
		ml := pwLogin(mailU, nil)
		res, err = svc.BuildLoginResult(ctx, ml)
		if err != nil || !res.MfaRequired || fake.sends.Load() != 1 || strings.Join(res.MfaMethods, ",") != "email" {
			t.Fatalf("email: %+v %v sends=%d", res, err, fake.sends.Load())
		}
		ml.EmailMfa = &fhir.LoginEmailMfa{}
		if _, err = svc.BuildLoginResult(ctx, ml); err != nil || fake.sends.Load() != 1 {
			t.Errorf("không gửi lại: sends=%d %v", fake.sends.Load(), err)
		}
		// đã xác minh MFA thì trả code; đăng nhập Google không bị hỏi MFA
		done := pwLogin(totp, func(l *fhir.Login) {
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"}
			l.MfaVerified = ptr(true)
		})
		if res, err = svc.BuildLoginResult(ctx, done); err != nil || res.Code == "" || res.Cookie == "" {
			t.Errorf("%+v %v", res, err)
		}
		g := pwLogin(totp, func(l *fhir.Login) {
			l.AuthMethod = "google"
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"}
		})
		if res, err = svc.BuildLoginResult(ctx, g); err != nil || res.Code == "" {
			t.Errorf("google: %+v %v", res, err)
		}

		// gate lỗi: lỗi 500, không có code (fail đóng)
		bad := e.withMFA(&fakeMFA{enrollErr: errors.New("hỏng"), sendErr: errors.New("hỏng")})
		for name, l := range map[string]*fhir.Login{
			"enroll": pwLogin(fresh, func(l *fhir.Login) {
				l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"}
			}),
			"email": pwLogin(mailU, nil),
		} {
			res, err := bad.BuildLoginResult(ctx, l)
			if res != nil || !domain.Is(err, domain.KindInternal, "") {
				t.Errorf("%s: gate lỗi phải trả lỗi, không phải kết quả: %+v %v", name, res, err)
			}
		}
	})
}

// Không có phụ thuộc MFA (hoặc MFA hỏng): mọi nhánh CẦN gửi/ dựng MFA đều lỗi, không có đường nào trả code mà bỏ qua MFA.
func TestDefaultMFAGateFailsClosed(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		mfaProject := e.project(func(p *fhir.Project) {
			p.Setting = []fhir.ProjectSetting{{Name: "mfaRequired", ValueBoolean: ptr(true)}}
		})
		u := e.pwUser("a@example.org", nil)
		l := e.login(u, func(l *fhir.Login) {
			l.Project = &fhir.Reference{Reference: "Project/" + mfaProject.ID}
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"}
		})
		res, err := e.withMFA(application.FailClosedMFA{}).BuildLoginResult(ctx, l)
		if res != nil || err == nil {
			t.Fatalf("mặc định phải fail đóng: %+v %v", res, err)
		}
		if strings.Contains(err.Error(), "KHONG-DUOC-LO") {
			t.Error("lỗi không được chứa code")
		}
		var gate application.FailClosedMFA
		if _, _, err := gate.BuildEnrollment(ctx, nil, nil, nil); err == nil {
			t.Error("FailClosedMFA.BuildEnrollment")
		}
		if err := gate.SendEmailCode(ctx, nil, nil, nil); err == nil {
			t.Error("FailClosedMFA.SendEmailCode")
		}
	})
}
