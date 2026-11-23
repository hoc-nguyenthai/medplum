package application_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// openProject là project nhận đăng ký mở, kèm khóa reCAPTCHA riêng.
func (e *env) openProject(siteKey, secretKey string) *fhir.Project {
	return e.project(func(p *fhir.Project) {
		p.Setting = []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptr(true)}}
		p.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/pat"}}}
		if siteKey != "" {
			p.Site = []fhir.ProjectSite{{Name: "web", Domain: []string{"example.org"}, RecaptchaSiteKey: siteKey, RecaptchaSecretKey: secretKey}}
		}
	})
}

func TestValidateRecaptchaServerLevel(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// không có khóa: bỏ qua hoàn toàn, kể cả khi thiếu token
		if err := e.svc.ValidateRecaptcha(ctx, "", "", "", true); err != nil || len(e.captcha.tokens) != 0 {
			t.Fatalf("không khóa: %v", err)
		}
		e.svc.RecaptchaKeys = application.RecaptchaConfig{SiteKey: "site-server", SecretKey: "secret-server"}
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "", "", "", true), domain.KindBadRequest, "Recaptcha token is required")
		e.captcha.ok = false
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "", "", "tok", true), domain.KindBadRequest, "Recaptcha failed")
		e.captcha.ok, e.captcha.err = true, errors.New("mạng đứt")
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "", "", "tok", true), domain.KindInternal, "") // fail đóng: không cho qua khi không hỏi được
		e.captcha.err = nil
		if err := e.svc.ValidateRecaptcha(ctx, "site-server", "", "tok", true); err != nil {
			t.Fatal(err)
		}
		if last := e.captcha.secrets[len(e.captcha.secrets)-1]; last != "secret-server" {
			t.Errorf("khóa máy chủ phải dùng secret máy chủ: %q", last)
		}
		// có secret nhưng chưa có bộ xác minh: lỗi, không bỏ qua
		s := e.serviceWithMail(nil, application.MailConfig{})
		s.RecaptchaKeys = application.RecaptchaConfig{SecretKey: "x"}
		wantErr(t, s.ValidateRecaptcha(ctx, "", "", "tok", true), domain.KindInternal, "")
	})
}

func TestValidateRecaptchaProjectKeys(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		open := e.openProject("site-p", "secret-p")
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "site-la", "", "tok", true), domain.KindBadRequest, "Invalid recaptchaSiteKey")
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "site-p", domain.NewID(), "tok", true), domain.KindBadRequest, "Invalid recaptchaSiteKey")
		if err := e.svc.ValidateRecaptcha(ctx, "site-p", open.ID, "tok", true); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.ValidateRecaptcha(ctx, "site-p", "", "tok", true); err != nil {
			t.Fatal(err)
		}
		if got := e.captcha.secrets[len(e.captcha.secrets)-1]; got != "secret-p" {
			t.Errorf("phải dùng secret của project: %q", got)
		}
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "site-p", "", "", true), domain.KindBadRequest, "Recaptcha token is required")

		nosecret := e.openProject("site-ns", "")
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "site-ns", nosecret.ID, "tok", true), domain.KindBadRequest, "Invalid recaptchaSecretKey")

		closed := e.project(func(p *fhir.Project) {
			p.Site = []fhir.ProjectSite{{Name: "w", Domain: []string{"a.org"}, RecaptchaSiteKey: "site-c", RecaptchaSecretKey: "s"}}
		})
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "site-c", closed.ID, "tok", true), domain.KindBadRequest, "Project does not allow open registration")
		if err := e.svc.ValidateRecaptcha(ctx, "site-c", closed.ID, "tok", false); err != nil {
			t.Errorf("không kiểm project (resetpassword): %v", err)
		}
		inactive := e.project(func(p *fhir.Project) {
			p.Active = ptr(false)
			p.Setting = []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptr(true)}}
			p.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/pat"}}}
			p.Site = []fhir.ProjectSite{{Name: "w", Domain: []string{"a.org"}, RecaptchaSiteKey: "site-i", RecaptchaSecretKey: "s"}}
		})
		wantErr(t, e.svc.ValidateRecaptcha(ctx, "site-i", inactive.ID, "tok", true), domain.KindBadRequest, "Project not active")
	})
}

func TestValidateRecaptchaPicksTheSiteWithMatchingKey(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		p := e.openProject("site-1", "secret-1")
		p.Site = append(p.Site, fhir.ProjectSite{Name: "b", Domain: []string{"b.org"}, RecaptchaSiteKey: "site-2", RecaptchaSecretKey: "secret-2"})
		q := e.project(func(x *fhir.Project) {
			x.Setting, x.DefaultAccessPolicies, x.Site = p.Setting, p.DefaultAccessPolicies, p.Site
			x.Site[0].RecaptchaSiteKey = "site-3"
		})
		if err := e.svc.ValidateRecaptcha(context.Background(), "site-2", q.ID, "tok", true); err != nil {
			t.Fatal(err)
		}
		if got := e.captcha.secrets[len(e.captcha.secrets)-1]; got != "secret-2" {
			t.Errorf("phải dùng secret của đúng site khớp khóa: %q", got)
		}
	})
}

func (e *env) newUserReq(email string, mut func(*application.NewUserRequest)) application.NewUserRequest {
	r := application.NewUserRequest{FirstName: "An", LastName: "Nguyen", Email: email, Password: "mat-khau-dai-123", ProjectID: "new"}
	if mut != nil {
		mut(&r)
	}
	return r
}

func TestNewUserCreatesUserAndLoginWithoutMembership(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		res, err := e.svc.NewUser(ctx, e.newUserReq("An@Example.org", nil))
		if err != nil {
			t.Fatal(err)
		}
		u, _ := e.svc.FindUserInScope(ctx, "an@example.org", "")
		if u == nil || u.Project != nil || u.Meta != nil && u.Meta.Project != "" || !e.svc.VerifyPassword(u, "mat-khau-dai-123") || u.PasswordHash == "mat-khau-dai-123" {
			t.Fatalf("user: %+v", u)
		}
		l, err := e.r.Logins.Get(ctx, res.LoginID)
		if err != nil || l.Membership != nil || l.Code != "" || refOf(l.User) != "User/"+u.ID || res.EmailVerificationRequired {
			t.Fatalf("login: %+v %v", l, err)
		}
		if len(e.mail.Sent()) != 0 {
			t.Error("mặc định không gửi thư xác minh")
		}
	})
}

func TestNewUserRejections(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		if _, err := e.svc.NewUser(ctx, e.newUserReq("an@example.org", nil)); err != nil {
			t.Fatal(err)
		}
		_, err := e.svc.NewUser(ctx, e.newUserReq("AN@example.org", nil))
		wantErr(t, err, domain.KindBadRequest, "Email already registered")
		var de *domain.Error
		if errors.As(err, &de) && de.Expression != "email" {
			t.Errorf("expression: %q", de.Expression)
		}
		e.pwned.bad["mat-khau-lo-123"] = 3
		_, err = e.svc.NewUser(ctx, e.newUserReq("b@example.org", func(r *application.NewUserRequest) { r.Password = "mat-khau-lo-123" }))
		wantErr(t, err, domain.KindBadRequest, domain.PwnedMessage)
		e.pwned.err = errors.New("HIBP down")
		_, err = e.svc.NewUser(ctx, e.newUserReq("c@example.org", nil))
		if err == nil {
			t.Fatal("pwned lỗi phải từ chối (fail đóng)")
		}
		e.pwned.err = nil
		for _, em := range []string{"b@example.org", "c@example.org"} {
			if u, _ := e.svc.FindUserInScope(ctx, em, ""); u != nil {
				t.Errorf("%s: không được tạo user khi bị từ chối", em)
			}
		}
		e.svc.Registration.Disabled = true
		_, err = e.svc.NewUser(ctx, e.newUserReq("d@example.org", nil))
		wantErr(t, err, domain.KindBadRequest, "Registration is disabled")
	})
}

func TestNewUserBadLoginRequestLeavesNoOrphanUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// project MỞ: nếu không, lỗi "không cho đăng ký" xảy ra trước và test này đúng một cách rỗng
		p := e.openProject("", "")
		cli := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "App", Meta: &fhir.Meta{Project: p.ID}, RedirectUris: []string{"https://app.example.org/cb"}})
		for _, tc := range []struct {
			name string
			mut  func(*application.NewUserRequest)
			want string
		}{
			{"challenge không có phương thức", func(r *application.NewUserRequest) { r.ClientID, r.ProjectID, r.CodeChallenge = cli.ID, "", "x" }, "Invalid code challenge method"},
			{"PKCE sai phương thức", func(r *application.NewUserRequest) {
				r.ClientID, r.ProjectID, r.CodeChallenge, r.CodeChallengeMethod = cli.ID, "", "x", "md5"
			}, ""},
			{"redirect lạ", func(r *application.NewUserRequest) {
				r.ClientID, r.ProjectID, r.CodeChallenge, r.CodeChallengeMethod, r.RedirectURI = cli.ID, "", "x", "plain", "https://evil.example.org/cb"
			}, "Invalid redirect URI"},
			{"projectId hỏng", func(r *application.NewUserRequest) { r.ProjectID = "khong-phai-uuid" }, "Invalid projectId"},
		} {
			_, err := e.svc.NewUser(ctx, e.newUserReq("mo-coi@example.org", tc.mut))
			if err == nil || (tc.want != "" && !domain.Is(err, domain.KindBadRequest, tc.want)) {
				t.Errorf("%s: phải bị từ chối bởi kiểm tra yêu cầu đăng nhập (%q), được %v", tc.name, tc.want, err)
			}
			if u, _ := e.svc.FindUserInScope(ctx, "mo-coi@example.org", ""); u != nil {
				t.Fatalf("%s: để lại user mồ côi", tc.name)
			}
		}
		// yêu cầu đúng thì đăng ký được (không bị "Email already registered")
		if _, err := e.svc.NewUser(ctx, e.newUserReq("mo-coi@example.org", func(r *application.NewUserRequest) {
			r.ClientID, r.ProjectID, r.CodeChallenge, r.CodeChallengeMethod = cli.ID, "", "x", "plain"
		})); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNewUserProjectAndClientScoping(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p1, p2 := e.openProject("", ""), e.openProject("", "")
		cli := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "App", Meta: &fhir.Meta{Project: p1.ID}, PkceOptional: ptr(true)})

		// có projectId: user gắn project (cả User.project và meta.project)
		res, err := e.svc.NewUser(ctx, e.newUserReq("an@example.org", func(r *application.NewUserRequest) { r.ProjectID = p1.ID }))
		if err != nil {
			t.Fatal(err)
		}
		u, _ := e.svc.FindUserInScope(ctx, "an@example.org", p1.ID)
		if u == nil || refOf(u.Project) != "Project/"+p1.ID || u.Meta.Project != p1.ID {
			t.Fatalf("user: %+v", u)
		}
		if l, _ := e.r.Logins.Get(ctx, res.LoginID); refOf(l.Project) != "Project/"+p1.ID {
			t.Errorf("login.project: %v", l.Project)
		}
		// cùng email ở project khác và ở phạm vi toàn cục đều được: trùng chỉ tính trong cùng phạm vi
		for _, pid := range []string{p2.ID, "new", ""} {
			if _, err := e.svc.NewUser(ctx, e.newUserReq("an@example.org", func(r *application.NewUserRequest) { r.ProjectID = pid })); err != nil && pid != "" {
				t.Fatalf("projectId=%q: %v", pid, err)
			}
		}
		// chỉ clientId: user KHÔNG gắn project (như Medplum) nhưng login mang project của client
		res, err = e.svc.NewUser(ctx, e.newUserReq("khach@example.org", func(r *application.NewUserRequest) { r.ProjectID, r.ClientID = "", cli.ID }))
		if err != nil {
			t.Fatal(err)
		}
		g, _ := e.svc.FindUserInScope(ctx, "khach@example.org", "")
		if g == nil || g.Project != nil {
			t.Errorf("user theo client phải không thuộc project: %+v", g)
		}
		if l, _ := e.r.Logins.Get(ctx, res.LoginID); refOf(l.Project) != "Project/"+p1.ID || refOf(l.Client) != "ClientApplication/"+cli.ID {
			t.Errorf("login: %+v", l)
		}
		// projectId và clientId khớp: được; lệch: 400; client lạ: 404
		if _, err := e.svc.NewUser(ctx, e.newUserReq("khop@example.org", func(r *application.NewUserRequest) { r.ProjectID, r.ClientID = p1.ID, cli.ID })); err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.NewUser(ctx, e.newUserReq("lech@example.org", func(r *application.NewUserRequest) { r.ProjectID, r.ClientID = p2.ID, cli.ID }))
		wantErr(t, err, domain.KindBadRequest, "Client and project do not match")
		_, err = e.svc.NewUser(ctx, e.newUserReq("la@example.org", func(r *application.NewUserRequest) { r.ProjectID, r.ClientID = "", domain.NewID() }))
		wantErr(t, err, domain.KindNotFound, "")
		// project không có, hoặc không nhận đăng ký mở
		_, err = e.svc.NewUser(ctx, e.newUserReq("x@example.org", func(r *application.NewUserRequest) { r.ProjectID = domain.NewID() }))
		wantErr(t, err, domain.KindNotFound, "")
		closed := e.project(nil)
		_, err = e.svc.NewUser(ctx, e.newUserReq("y@example.org", func(r *application.NewUserRequest) { r.ProjectID = closed.ID }))
		wantErr(t, err, domain.KindBadRequest, "Project does not allow open registration")
		for _, em := range []string{"lech@example.org", "la@example.org", "x@example.org", "y@example.org"} {
			if u, _ := e.svc.FindUserInScope(ctx, em, ""); u != nil {
				t.Errorf("%s: không được tạo user", em)
			}
		}
	})
}

func TestNewUserEmailVerification(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.openProject("", "")
		e.svc.Registration.RequireVerifiedEmailForProject = true
		res, err := e.svc.NewUser(ctx, e.newUserReq("an@example.org", nil))
		if err != nil || !res.EmailVerificationRequired || len(e.mail.Sent()) != 1 {
			t.Fatalf("%+v %v thư=%d", res, err, len(e.mail.Sent()))
		}
		id, _ := e.lastLink("verifyemail")
		if sr, _ := e.r.SecurityRequests.Get(ctx, id); sr.RedirectUri != "https://app.example.org/register?login="+res.LoginID {
			t.Errorf("redirectUri: %q", sr.RedirectUri)
		}
		// vào project có sẵn thì không cần xác minh
		res, err = e.svc.NewUser(ctx, e.newUserReq("b@example.org", func(r *application.NewUserRequest) { r.ProjectID = p.ID }))
		if err != nil || res.EmailVerificationRequired || len(e.mail.Sent()) != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		// lỗi gửi thư không làm hỏng đăng ký (lệch L5)
		e.mail.Err = errors.New("smtp down")
		res, err = e.svc.NewUser(ctx, e.newUserReq("c@example.org", nil))
		if err != nil || !res.EmailVerificationRequired {
			t.Fatalf("%+v %v", res, err)
		}
		if u, _ := e.svc.FindUserInScope(ctx, "c@example.org", ""); u == nil {
			t.Error("user phải được tạo")
		}
	})
}

func TestNewUserRaceSameEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		var ok, dup atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.svc.NewUser(ctx, e.newUserReq("dua@example.org", nil))
				switch {
				case err == nil:
					ok.Add(1)
				case domain.Is(err, domain.KindBadRequest, "Email already registered"):
					dup.Add(1)
				default:
					t.Errorf("lỗi lạ: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 || dup.Load() != 7 {
			t.Fatalf("thành công=%d trùng=%d", ok.Load(), dup.Load())
		}
		if n := e.count("User"); n != 1 {
			t.Errorf("số user: %d", n)
		}
	})
}

// openProjectWithPolicy2 là project mở đơn giản dùng cho các test không cần chính sách có thật.
func (e *env) openProjectWithPolicy2() *fhir.Project { return e.openProject("", "") }
