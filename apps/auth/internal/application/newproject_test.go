package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// registered đăng ký một user mới (projectId=new) và trả user cùng login chưa có membership.
func (e *env) registered(email string) (*fhir.User, string) {
	e.t.Helper()
	res, err := e.svc.NewUser(context.Background(), e.newUserReq(email, nil))
	if err != nil {
		e.t.Fatal(err)
	}
	u, _ := e.svc.FindUserInScope(context.Background(), email, "")
	return u, res.LoginID
}

func (e *env) welcomeMails() []application.Message {
	var out []application.Message
	for _, m := range e.mail.Sent() {
		if strings.HasPrefix(m.Subject, "Welcome to") {
			out = append(out, m)
		}
	}
	return out
}

func TestNewProjectCreatesEverythingAndReturnsCode(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.svc.Registration.DefaultProjectFeatures = []string{"bots"}
		u, loginID := e.registered("an@example.org")
		res, login, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: loginID, ProjectName: "Bệnh viện A"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Code == "" || res.Cookie == "" || res.Login != loginID || login.Membership == nil || !e.codeStoredAs(t, loginID, res) {
			t.Fatalf("kết quả: %+v", res)
		}
		pid := strings.TrimPrefix(refOf(login.Project), "Project/")
		p, err := e.r.Projects.Get(ctx, pid)
		if err != nil || p.Name != "Bệnh viện A" || refOf(p.Owner) != "User/"+u.ID || !domain.IsTrue(p.StrictMode) || len(p.DefaultAccessPolicies) != 6 || len(p.Features) != 1 {
			t.Fatalf("project: %+v %v", p, err)
		}
		m, err := e.r.Memberships.Get(ctx, strings.TrimPrefix(refOf(login.Membership), "ProjectMembership/"))
		if err != nil || !domain.IsTrue(m.Admin) || refOf(m.User) != "User/"+u.ID || refOf(m.Project) != "Project/"+pid || !strings.HasPrefix(refOf(m.Profile), "Practitioner/") {
			t.Fatalf("membership: %+v %v", m, err)
		}
		// 6 chính sách, 1 client mặc định (có secret), 1 hồ sơ Practitioner, 2 membership (admin và client)
		if e.count("AccessPolicy") != 6 || e.count("ClientApplication") != 1 || e.count("Practitioner") != 1 || e.count("ProjectMembership") != 2 {
			t.Errorf("số resource: policy=%d client=%d prac=%d mem=%d", e.count("AccessPolicy"), e.count("ClientApplication"), e.count("Practitioner"), e.count("ProjectMembership"))
		}
		// dùng lại login đã có membership
		_, _, err = e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: loginID, ProjectName: "Dự án thứ hai"})
		wantErr(t, err, domain.KindBadRequest, "Login already has a membership")
		if e.count("Project") != 1 {
			t.Errorf("dùng lại login không được tạo thêm project: %d", e.count("Project"))
		}
	})
}

func TestNewProjectWelcomeEmailOnlyForFirstProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.svc.Mail.SupportEmail = "ho-tro@example.org"
		u, l1 := e.registered("an@example.org")
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l1, ProjectName: "Dự án đầu"}); err != nil {
			t.Fatal(err)
		}
		w := e.welcomeMails()
		if len(w) != 1 || w[0].To != "an@example.org" || w[0].Subject != "Welcome to Trivita" {
			t.Fatalf("thư chào: %+v", w)
		}
		for _, want := range []string{"Hi An,\n", "Your new project \"Dự án đầu\" is ready to go.", "https://app.example.org/signin", "https://app.example.org/admin/users",
			"ho-tro@example.org", "Thank you,\nThe Trivita Team\n"} {
			if !strings.Contains(w[0].Text, want) {
				t.Errorf("thiếu %q trong:\n%s", want, w[0].Text)
			}
		}
		for _, bad := range []string{"**", "<", "undefined", "Medplum", "medplum.com", "discord"} {
			if strings.Contains(strings.ToLower(w[0].Text), strings.ToLower(bad)) {
				t.Errorf("thư chào chứa %q", bad)
			}
		}
		// user đã sở hữu project: tạo project thứ hai không gửi thư chào nữa
		login2 := e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"} })
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: login2.ID, ProjectName: "Dự án hai"}); err != nil {
			t.Fatal(err)
		}
		if n := len(e.welcomeMails()); n != 1 {
			t.Errorf("không gửi thư chào lần hai: %d", n)
		}
	})
}

func TestNewProjectWelcomeEmailEdgeCases(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// lỗi gửi thư chào không làm hỏng việc tạo project
		u, l1 := e.registered("an@example.org")
		e.mail.Err = errors.New("smtp down")
		if res, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l1, ProjectName: "Dự án đầu"}); err != nil || res.Code == "" {
			t.Fatalf("%+v %v", res, err)
		}
		// không có tên: chào "Hi," (không có "undefined")
		e.mail.Err = nil
		u2, l2 := e.registered("vo-danh@example.org")
		if _, err := e.r.Users.Mutate(ctx, u2.ID, func(x *fhir.User) error { x.FirstName = ""; return nil }); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l2, ProjectName: "Dự án vô danh"}); err != nil {
			t.Fatal(err)
		}
		w := e.welcomeMails()
		if len(w) != 2 || !strings.HasPrefix(w[1].Text, "Hi,\n") || strings.Contains(w[1].Text, "If you have any questions") {
			t.Errorf("thư chào không tên: %+v", w)
		}
		// user không có email: không gửi thư chào
		u3, l3 := e.registered("khong-email@example.org")
		if _, err := e.r.Users.Mutate(ctx, u3.ID, func(x *fhir.User) error { x.Email = ""; return nil }); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l3, ProjectName: "Dự án không email"}); err != nil {
			t.Fatal(err)
		}
		if n := len(e.welcomeMails()); n != 2 {
			t.Errorf("user không email không nhận thư chào: %d", n)
		}
		_ = u
	})
}

func TestNewProjectValidationAndFlags(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		_, l := e.registered("an@example.org")
		for name, tc := range map[string]struct {
			name string
			want string
		}{
			"ngắn":              {"abc", "Project name must be between 4 and 72 characters"},
			"dài":               {strings.Repeat("x", 73), "Project name must be between 4 and 72 characters"},
			"toàn khoảng trắng": {"    ", ""},
		} {
			_, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: tc.name})
			if err == nil || (tc.want != "" && !domain.Is(err, domain.KindBadRequest, tc.want)) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// đếm ký tự, không phải byte: 4 ký tự tiếng Việt (nhiều byte) hợp lệ
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: "Việt"}); err != nil {
			t.Errorf("4 ký tự nhiều byte: %v", err)
		}
		// 72 ký tự nhiều byte (216 byte) vẫn hợp lệ: giới hạn tính theo ký tự
		_, l72 := e.registered("c@example.org")
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l72, ProjectName: strings.Repeat("ệ", 72)}); err != nil {
			t.Errorf("72 ký tự nhiều byte: %v", err)
		}
		_, l2 := e.registered("b@example.org")
		_, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: domain.NewID(), ProjectName: "Dự án đầu"})
		wantErr(t, err, domain.KindNotFound, "")
		e.svc.Registration.NewProjectDisabled = true
		_, _, err = e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l2, ProjectName: "Dự án đầu"})
		wantErr(t, err, domain.KindBadRequest, "New project registration is disabled")
		e.svc.Registration.Disabled = true
		_, _, err = e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l2, ProjectName: "Dự án đầu"})
		wantErr(t, err, domain.KindBadRequest, "Registration is disabled")
		if e.count("Project") != 2 { // "Việt" và 72 ký tự
			t.Errorf("chỉ project hợp lệ được tạo: %d", e.count("Project"))
		}
	})
}

// L3: mọi kiểm tra trạng thái login chạy TRƯỚC khi tạo project, nên bị từ chối không để lại project mồ côi.
func TestNewProjectChecksLoginStateBeforeCreatingAnything(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, _ := e.registered("an@example.org")
		other := e.project(nil)
		for _, tc := range []struct {
			name string
			mut  func(*fhir.Login)
			want string
		}{
			{"đã thu hồi", func(l *fhir.Login) { l.Revoked = ptr(true) }, "Login revoked"},
			{"đã cấp code", func(l *fhir.Login) { l.Granted = ptr(true) }, "Login granted"},
			{"thuộc project khác", func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + other.ID} }, "Invalid project"},
			{"đã có membership", func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/x"} }, "Login already has a membership"},
		} {
			l := e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"}; tc.mut(l) })
			before := e.count("Project")
			_, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l.ID, ProjectName: "Dự án đầu"})
			wantErr(t, err, domain.KindBadRequest, tc.want)
			if e.count("Project") != before || e.count("AccessPolicy") != 0 {
				t.Errorf("%s: để lại project mồ côi", tc.name)
			}
		}
	})
}

func TestNewProjectWithPendingMFACreatesNothing(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// đã đăng ký MFA nhưng login chưa xác minh: trả mfaRequired, KHÔNG tạo project
		u, l := e.registered("an@example.org")
		if _, err := e.r.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { x.MfaEnrolled = ptr(true); x.MfaSecret = "SECRET"; return nil }); err != nil {
			t.Fatal(err)
		}
		res, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: "Dự án đầu"})
		if err != nil || !res.MfaRequired || res.Code != "" {
			t.Fatalf("%+v %v", res, err)
		}
		// user bị bắt buộc MFA nhưng chưa đăng ký: phản hồi yêu cầu đăng ký (có enrollUri), KHÔNG có code, chưa tạo project
		u2, l2 := e.registered("b@example.org")
		if _, err := e.r.Users.Mutate(ctx, u2.ID, func(x *fhir.User) error { x.MfaRequired = ptr(true); return nil }); err != nil {
			t.Fatal(err)
		}
		res, _, err = e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l2, ProjectName: "Dự án hai"})
		if err != nil || !res.MfaEnrollRequired || res.Code != "" || !strings.HasPrefix(res.EnrollURI, "otpauth://totp/") {
			t.Fatalf("phải yêu cầu đăng ký MFA và không có code: %+v %v", res, err)
		}
		// MFA hỏng (cổng lỗi): fail đóng, không có code
		res, _, err = e.withMFA(application.FailClosedMFA{}).NewProject(ctx, application.NewProjectRequest{LoginID: l2, ProjectName: "Dự án hai"})
		if err == nil || (res != nil && res.Code != "") {
			t.Fatalf("phải lỗi và không có code: %+v %v", res, err)
		}
		if e.count("Project") != 0 {
			t.Errorf("không được tạo project khi chưa qua MFA: %d", e.count("Project"))
		}
	})
}

func TestNewProjectRequireVerifiedEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.svc.Registration.RequireVerifiedEmailForProject = true
		u, l := e.registered("an@example.org")
		before := len(e.mail.Sent()) // newuser đã gửi một thư xác minh
		_, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: "Dự án đầu"})
		wantErr(t, err, domain.KindBadRequest, application.EmailVerificationRequiredMessage)
		if len(e.mail.Sent()) != before+1 || !strings.Contains(e.mail.Sent()[before].Text, "/verifyemail/") || e.count("Project") != 0 {
			t.Fatalf("thư=%d project=%d", len(e.mail.Sent()), e.count("Project"))
		}
		// lỗi gửi thư xác minh không đổi kết quả: vẫn báo cần xác minh, không tạo project
		e.mail.Err = errors.New("smtp down")
		_, _, err = e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: "Dự án đầu"})
		wantErr(t, err, domain.KindBadRequest, application.EmailVerificationRequiredMessage)
		e.mail.Err = nil
		if _, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: "Dự án đầu"}); err == nil {
			t.Fatal("vẫn chưa xác minh")
		}
		// xác minh rồi thì tạo được
		id, secret := e.lastLink("verifyemail")
		if _, err := e.svc.VerifyEmail(ctx, id, secret); err != nil {
			t.Fatal(err)
		}
		if res, _, err := e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: l, ProjectName: "Dự án đầu"}); err != nil || res.Code == "" {
			t.Fatalf("%+v %v", res, err)
		}
		_ = u
	})
}
