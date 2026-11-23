package application_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

var linkRe = regexp.MustCompile(`https://app\.example\.org/(setpassword|verifyemail)/([0-9a-f-]{36})/([0-9a-f]{32})`)

// lastLink lấy (id, secret) trong link của thư gần nhất.
func (e *env) lastLink(kind string) (id, secret string) {
	e.t.Helper()
	sent := e.mail.Sent()
	if len(sent) == 0 {
		e.t.Fatal("không có thư nào được gửi")
	}
	m := linkRe.FindStringSubmatch(sent[len(sent)-1].Text)
	if m == nil || m[1] != kind {
		e.t.Fatalf("thư không có link %s: %q", kind, sent[len(sent)-1].Text)
	}
	return m[2], m[3]
}

func (e *env) userWithPassword(email string, mut func(*fhir.User)) *fhir.User {
	h, err := e.hasher.Hash("mat-khau-cu-123")
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

func (e *env) getUser(id string) *fhir.User {
	e.t.Helper()
	u, err := e.r.Users.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) revoked(id string) bool {
	e.t.Helper()
	l, err := e.r.Logins.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return domain.IsTrue(l.Revoked)
}

func TestResetPasswordSendsLinkAndStoresOnlyHash(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		if err := e.svc.ResetPassword(ctx, "An@Example.org", "", "https://app.example.org/done", true); err != nil {
			t.Fatal(err)
		}
		sent := e.mail.Sent()
		if len(sent) != 1 || sent[0].To != "an@example.org" || sent[0].Subject != "Trivita Password Reset" {
			t.Fatalf("thư: %+v", sent)
		}
		if !strings.HasSuffix(sent[0].Text, "Thank you,\nTrivita\n") {
			t.Errorf("chữ ký thư: %q", sent[0].Text)
		}
		id, secret := e.lastLink("setpassword")
		sr, err := e.r.SecurityRequests.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if sr.Secret == secret || sr.Secret != domain.HashSecurityRequestSecret(secret) {
			t.Errorf("kho phải giữ băm của secret, không phải secret: %q", sr.Secret)
		}
		if sr.Type != "reset" || refOf(sr.User) != "User/"+u.ID || sr.RedirectUri != "https://app.example.org/done" {
			t.Errorf("request: %+v", sr)
		}
		if d := time.Until(mustTime(t, sr.ExpiresAt)); d < 59*time.Minute || d > 61*time.Minute {
			t.Errorf("reset phải hết hạn sau 1 giờ, còn %v", d)
		}
	})
}

func refOf(r *fhir.Reference) string {
	if r == nil {
		return ""
	}
	return r.Reference
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestResetPasswordDoesNotRevealAccounts(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.userWithPassword("an@example.org", nil)
		if err := e.svc.ResetPassword(ctx, "khong-co@example.org", "", "", true); err != nil {
			t.Fatalf("email lạ phải trả nil: %v", err)
		}
		if len(e.mail.Sent()) != 0 {
			t.Error("email lạ không được gửi thư")
		}
		// lỗi gửi thư không đổi kết quả: người gọi không phân biệt được tài khoản có thật
		e.mail.Err = errors.New("smtp down")
		if err := e.svc.ResetPassword(ctx, "an@example.org", "", "", true); err != nil {
			t.Fatalf("lỗi gửi thư phải bị nuốt: %v", err)
		}
		if len(e.mail.Sent()) != 1 {
			t.Error("phải thử gửi một lần")
		}
	})
}

func TestResetPasswordScopeAndFlags(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		e.userWithPassword("an@example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		// user thuộc project không được tìm khi không nêu projectId (và ngược lại)
		if err := e.svc.ResetPassword(ctx, "an@example.org", "", "", true); err != nil || len(e.mail.Sent()) != 0 {
			t.Fatalf("không có user toàn cục: err=%v thư=%d", err, len(e.mail.Sent()))
		}
		if err := e.svc.ResetPassword(ctx, "an@example.org", p.ID, "", true); err != nil || len(e.mail.Sent()) != 1 {
			t.Fatalf("user trong project phải nhận thư: err=%v thư=%d", err, len(e.mail.Sent()))
		}
		// sendEmail=false: không phát request mới, không làm mất link đang dùng
		id, _ := e.lastLink("setpassword")
		if err := e.svc.ResetPassword(ctx, "an@example.org", p.ID, "", false); err != nil {
			t.Fatal(err)
		}
		if e.used(id) || len(e.mail.Sent()) != 1 {
			t.Error("sendEmail=false không được đụng tới request đang dùng")
		}
	})
}

func TestResetPasswordRefusesExternalAuth(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "corp.example.org",
			IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "https://idp.example.org/authorize", ClientId: "abc"}})
		e.userWithPassword("an@corp.example.org", nil)
		wantErr(t, e.svc.ResetPassword(context.Background(), "an@corp.example.org", "", "", true), domain.KindBadRequest,
			"Cannot reset password for external auth. Contact your system administrator.")
		if len(e.mail.Sent()) != 0 {
			t.Error("không gửi thư cho tài khoản đăng nhập ngoài")
		}
	})
}

func TestResetPasswordSupersedesOnlySameType(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		invite := e.secReq(u, domain.SecurityRequestInvite)
		for i := 0; i < 2; i++ {
			if err := e.svc.ResetPassword(ctx, "an@example.org", "", "", true); err != nil {
				t.Fatal(err)
			}
		}
		first, _ := linkIDs(e.mail.Sent())
		if !e.used(first) {
			t.Error("link reset cũ phải bị vô hiệu khi phát link mới")
		}
		if e.used(invite.ID) {
			t.Error("reset không được vô hiệu request loại invite")
		}
	})
}

func linkIDs(sent []application.Message) (first, last string) {
	var ids []string
	for _, m := range sent {
		if x := linkRe.FindStringSubmatch(m.Text); x != nil {
			ids = append(ids, x[2])
		}
	}
	return ids[0], ids[len(ids)-1]
}

func TestResetPasswordWithoutMailConfigIsNoop(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.userWithPassword("an@example.org", nil)
		s := application.NewService(application.Deps{Users: e.r.Users, Logins: e.r.Logins, DomainConfigs: e.r.DomainConfigs, SecurityRequests: e.r.SecurityRequests,
			Resources: e.r.Resources, Tx: e.r.Tx, Hasher: e.hasher, Pwned: e.pwned, Clock: e.clock})
		if err := s.ResetPassword(context.Background(), "an@example.org", "", "", true); err != nil {
			t.Fatal(err)
		}
		if reqs, _ := e.r.SecurityRequests.ListSince(context.Background(), "User/x", time.Now().Add(-time.Hour)); len(reqs) != 0 {
			t.Error("không phát request khi không có kênh gửi")
		}
	})
}

func TestSetPasswordWithRequestCheckOrder(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		issue := func(typ string) (string, string) {
			sr, secret, err := e.svc.IssueSecurityRequest(ctx, u, typ, "")
			if err != nil {
				t.Fatal(err)
			}
			return sr.ID, secret
		}
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, "00000000-0000-4000-8000-000000000000", "x", "mat-khau-moi-123"), domain.KindNotFound, "")

		vid, vsecret := issue(domain.SecurityRequestVerifyEmail)
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, vid, vsecret, "mat-khau-moi-123"), domain.KindBadRequest, "Invalid request type")

		id, secret := issue(domain.SecurityRequestReset)
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, id, "sai", "mat-khau-moi-123"), domain.KindBadRequest, "Incorrect secret")
		// secret là BĂM đã lưu thì không dùng được (không thể lấy băm từ kho rồi dùng làm secret)
		sr, _ := e.r.SecurityRequests.Get(ctx, id)
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, id, sr.Secret, "mat-khau-moi-123"), domain.KindBadRequest, "Incorrect secret")

		e.clock.t = e.clock.t.Add(2 * time.Hour)
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, id, secret, "mat-khau-moi-123"), domain.KindBadRequest, "Expired")
		e.clock.t = e.clock.t.Add(-2 * time.Hour)

		if err := e.svc.SetPasswordWithRequest(ctx, id, secret, "mat-khau-moi-123"); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, id, secret, "mat-khau-khac-123"), domain.KindBadRequest, "Already used")
		got := e.getUser(u.ID)
		if !e.svc.VerifyPassword(got, "mat-khau-moi-123") || !domain.IsTrue(got.EmailVerified) {
			t.Error("mật khẩu mới phải dùng được và email được đánh dấu đã xác minh")
		}
	})
}

func TestSetPasswordWithRequestRevokesLoginsAndKeepsLinkOnPwned(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		l := e.login(u, nil)
		sr, secret, err := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestReset, "")
		if err != nil {
			t.Fatal(err)
		}
		e.pwned.bad["mat-khau-lo-123"] = 5
		wantErr(t, e.svc.SetPasswordWithRequest(ctx, sr.ID, secret, "mat-khau-lo-123"), domain.KindBadRequest, domain.PwnedMessage)
		if e.used(sr.ID) || e.revoked(l.ID) {
			t.Error("mật khẩu bị từ chối không được làm mất link hay thu hồi phiên")
		}
		if err := e.svc.SetPasswordWithRequest(ctx, sr.ID, secret, "mat-khau-moi-123"); err != nil {
			t.Fatal(err)
		}
		if !e.revoked(l.ID) {
			t.Error("đặt mật khẩu phải thu hồi mọi phiên")
		}
	})
}

func TestSetPasswordWithRequestRace(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		sr, secret, err := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestReset, "")
		if err != nil {
			t.Fatal(err)
		}
		var ok atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if e.svc.SetPasswordWithRequest(ctx, sr.ID, secret, "mat-khau-moi-123") == nil {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 {
			t.Fatalf("link dùng một lần: %d lần thành công", ok.Load())
		}
	})
}

func TestVerifyEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		rid, rsecret, _ := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestReset, "")
		_ = rsecret
		_, err := e.svc.VerifyEmail(ctx, rid.ID, rsecret)
		wantErr(t, err, domain.KindBadRequest, "Invalid user security request type")

		sr, secret, err := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestVerifyEmail, "https://app.example.org/register?login=1")
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.VerifyEmail(ctx, sr.ID, "sai")
		wantErr(t, err, domain.KindBadRequest, "Incorrect secret")
		if domain.IsTrue(e.getUser(u.ID).EmailVerified) {
			t.Error("secret sai không được xác minh email")
		}
		redirect, err := e.svc.VerifyEmail(ctx, sr.ID, secret)
		if err != nil || redirect != "https://app.example.org/register?login=1" {
			t.Fatalf("redirect=%q err=%v", redirect, err)
		}
		if !domain.IsTrue(e.getUser(u.ID).EmailVerified) {
			t.Error("email phải được đánh dấu đã xác minh")
		}
		_, err = e.svc.VerifyEmail(ctx, sr.ID, secret)
		wantErr(t, err, domain.KindBadRequest, "Already used")
	})
}

func TestVerifyEmailExpiredAndBadRedirect(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		bad, bsecret, _ := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestVerifyEmail, "khong-phai-url")
		_, err := e.svc.VerifyEmail(ctx, bad.ID, bsecret)
		wantErr(t, err, domain.KindBadRequest, "Invalid redirectUri")
		if e.used(bad.ID) {
			t.Error("redirectUri hỏng không được làm tiêu thụ request")
		}
		e.clock.t = e.clock.t.Add(8 * 24 * time.Hour)
		_, err = e.svc.VerifyEmail(ctx, bad.ID, bsecret)
		wantErr(t, err, domain.KindBadRequest, "Expired")
	})
}

func TestVerifyEmailRace(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		sr, secret, _ := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestVerifyEmail, "")
		var ok atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.svc.VerifyEmail(ctx, sr.ID, secret); err == nil {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 {
			t.Fatalf("link dùng một lần: %d lần thành công", ok.Load())
		}
	})
}

func TestSendVerificationEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		first := e.svc.SendVerificationEmail
		if err := first(ctx, u, "login-1"); err != nil {
			t.Fatal(err)
		}
		if err := first(ctx, u, "login-1"); err != nil {
			t.Fatal(err)
		}
		a, b := linkIDs(e.mail.Sent())
		if !e.used(a) || e.used(b) {
			t.Error("link xác minh cũ phải bị thay thế, link mới còn dùng được")
		}
		sr, _ := e.r.SecurityRequests.Get(ctx, b)
		if sr.RedirectUri != "https://app.example.org/register?login=login-1" || sr.Type != "verify-email" {
			t.Errorf("request: %+v", sr)
		}
		e.mail.Err = errors.New("smtp down")
		if err := first(ctx, u, "login-2"); err == nil {
			t.Error("lỗi gửi thư xác minh phải được trả cho người gọi")
		}
	})
}

func TestChangePassword(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		l1, l2 := e.login(u, nil), e.login(u, nil)
		auth := &application.AuthState{Login: l1, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}

		wantErr(t, e.svc.ChangePassword(ctx, auth, "sai-mat-khau", "mat-khau-moi-123"), domain.KindBadRequest, "Incorrect password")
		e.pwned.bad["mat-khau-lo-123"] = 1
		wantErr(t, e.svc.ChangePassword(ctx, auth, "mat-khau-cu-123", "mat-khau-lo-123"), domain.KindBadRequest, domain.PwnedMessage)
		if e.revoked(l1.ID) {
			t.Fatal("đổi thất bại không được thu hồi phiên")
		}
		if err := e.svc.ChangePassword(ctx, auth, "mat-khau-cu-123", "mat-khau-moi-123"); err != nil {
			t.Fatal(err)
		}
		if !e.revoked(l1.ID) || !e.revoked(l2.ID) {
			t.Error("phải thu hồi MỌI phiên, kể cả phiên đang dùng")
		}
		got := e.getUser(u.ID)
		if !e.svc.VerifyPassword(got, "mat-khau-moi-123") || domain.IsTrue(got.EmailVerified) {
			t.Error("mật khẩu mới dùng được; changepassword không được đặt emailVerified")
		}
	})
}

func TestChangePasswordGuards(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		nopw := e.user("khong-mat-khau@example.org", nil)
		a := &application.AuthState{Login: e.login(nopw, nil), Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + nopw.ID}}}
		err := e.svc.ChangePassword(ctx, a, "bat-ky-123456", "mat-khau-moi-123")
		wantErr(t, err, domain.KindBadRequest, "Existing password not set")

		// tài khoản không phải User (ClientApplication) và login giới hạn scope SMART đều bị 403
		client := &application.AuthState{Login: &fhir.Login{AuthMethod: domain.AuthClient}, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "ClientApplication/c1"}}}
		wantErr(t, e.svc.ChangePassword(ctx, client, "a", "mat-khau-moi-123"), domain.KindForbidden, "")
		u := e.userWithPassword("an@example.org", nil)
		smart := &application.AuthState{Login: &fhir.Login{AuthMethod: domain.AuthPassword, Scope: "patient/Patient.rs"}, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		wantErr(t, e.svc.ChangePassword(ctx, smart, "mat-khau-cu-123", "mat-khau-moi-123"), domain.KindForbidden, "")
		// user toàn cục chỉ tự quản lý khi đăng nhập bằng mật khẩu hoặc Google
		ext := &application.AuthState{Login: &fhir.Login{AuthMethod: domain.AuthExternal}, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		wantErr(t, e.svc.ChangePassword(ctx, ext, "mat-khau-cu-123", "mat-khau-moi-123"), domain.KindForbidden, "")
	})
}

func (e *env) serviceWithMail(m application.Mailer, cfg application.MailConfig) *application.Service {
	return application.NewService(application.Deps{Users: e.r.Users, Logins: e.r.Logins, DomainConfigs: e.r.DomainConfigs, SecurityRequests: e.r.SecurityRequests,
		Resources: e.r.Resources, Tx: e.r.Tx, Hasher: e.hasher, Pwned: e.pwned, Clock: e.clock, Mailer: m, Mail: cfg})
}

func TestMailLinkAndNameDefaults(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.userWithPassword("an@example.org", nil)
		// gốc URL có dấu / cuối: link vẫn chỉ có một dấu / ở giữa; không đặt tên thì dùng Trivita
		svc := e.serviceWithMail(e.mail, application.MailConfig{AppBaseURL: "https://app.example.org/"})
		if err := svc.ResetPassword(ctx, "an@example.org", "", "", true); err != nil {
			t.Fatal(err)
		}
		m := e.mail.Sent()[0]
		if !linkRe.MatchString(m.Text) || strings.Contains(m.Text, "//setpassword") || m.Subject != "Trivita Password Reset" {
			t.Errorf("thư: %q / %q", m.Subject, m.Text)
		}
		svc2 := e.serviceWithMail(e.mail, application.MailConfig{AppBaseURL: "https://app.example.org", AppName: "MedVita"})
		if err := svc2.ResetPassword(ctx, "an@example.org", "", "", true); err != nil {
			t.Fatal(err)
		}
		if m := e.mail.Sent()[1]; m.Subject != "MedVita Password Reset" || !strings.HasSuffix(m.Text, "Thank you,\nMedVita\n") {
			t.Errorf("tên tùy chỉnh: %q %q", m.Subject, m.Text)
		}
	})
}

func TestNoLinkWithoutAppBaseURL(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		svc := e.serviceWithMail(e.mail, application.MailConfig{}) // có Mailer nhưng không biết link trỏ đâu
		if err := svc.ResetPassword(ctx, "an@example.org", "", "", true); err != nil {
			t.Fatal(err)
		}
		if err := svc.SendVerificationEmail(ctx, u, "l1"); err != nil {
			t.Fatal(err)
		}
		if len(e.mail.Sent()) != 0 {
			t.Errorf("không có AppBaseURL thì không gửi thư chứa link hỏng: %+v", e.mail.Sent())
		}
		if reqs, _ := e.r.SecurityRequests.ListSince(ctx, "User/"+u.ID, time.Now().Add(-time.Hour)); len(reqs) != 0 {
			t.Error("cũng không phát request vô dụng")
		}
	})
}

// Tìm user phải đúng phạm vi: không "rơi về" user toàn cục khi người gọi nêu một projectId khác (khác với đăng nhập).
func TestResetPasswordDoesNotFallBackToGlobalUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		p := e.project(nil)
		e.userWithPassword("toan-cuc@example.org", nil)
		if err := e.svc.ResetPassword(context.Background(), "toan-cuc@example.org", p.ID, "", true); err != nil {
			t.Fatal(err)
		}
		if len(e.mail.Sent()) != 0 {
			t.Error("user toàn cục không được nhận thư khi yêu cầu nêu project")
		}
	})
}

func TestVerificationEmailText(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		u := e.userWithPassword("an@example.org", nil)
		if err := e.svc.SendVerificationEmail(context.Background(), u, "l1"); err != nil {
			t.Fatal(err)
		}
		m := e.mail.Sent()[0]
		if m.To != "an@example.org" || m.Subject != "Trivita Email Verification" || !strings.HasSuffix(m.Text, "Thank you,\nTrivita\n") ||
			!strings.HasPrefix(m.Text, "We received a request to create a Trivita account using this email address.\n\n") {
			t.Errorf("thư xác minh: %+v", m)
		}
	})
}
