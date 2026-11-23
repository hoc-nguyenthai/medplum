package application_test

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// S3: không so sánh secret bằng == hay != (so sánh thời gian cố định qua domain.SecurityRequestSecretMatches/ConstantTimeEqual). Kiểm tĩnh trên mã
// không phải test của application và domain: mọi phép == / != mà một vế là định danh hay trường có tên chứa "secret" phải so với "" hoặc nil.
func TestSecretsAreNeverComparedWithEqualityOperators(t *testing.T) {
	for _, dir := range []string{".", "../domain"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, f, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				be, ok := n.(*ast.BinaryExpr)
				if !ok || (be.Op != token.EQL && be.Op != token.NEQ) {
					return true
				}
				name := func(e ast.Expr) string {
					switch v := e.(type) {
					case *ast.Ident:
						return v.Name
					case *ast.SelectorExpr:
						return v.Sel.Name
					}
					return ""
				}
				isEmpty := func(e ast.Expr) bool {
					if l, ok := e.(*ast.BasicLit); ok {
						return l.Value == `""`
					}
					id, ok := e.(*ast.Ident)
					return ok && id.Name == "nil"
				}
				for _, side := range [][2]ast.Expr{{be.X, be.Y}, {be.Y, be.X}} {
					if strings.Contains(strings.ToLower(name(side[0])), "secret") && !isEmpty(side[1]) {
						t.Errorf("%s: so sánh secret bằng toán tử %s (dùng ConstantTimeEqual)", fset.Position(be.Pos()), be.Op)
					}
				}
				return true
			})
		}
	}
}

// S8 (mức use case): oldPassword của changepassword không có kiểm độ dài ở validator, nên mật khẩu 72 byte đúng cộng đuôi bất kỳ không được qua.
func TestChangePasswordRejectsOldPasswordOverTheByteLimit(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		base := strings.Repeat("a", 72)
		h, err := e.hasher.Hash(base)
		if err != nil {
			t.Fatal(err)
		}
		u := e.user("an@example.org", func(x *fhir.User) { x.PasswordHash = h })
		a := &application.AuthState{Login: e.login(u, nil), Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		wantErr(t, e.svc.ChangePassword(ctx, a, base+"X", "mat-khau-moi-123"), domain.KindBadRequest, "Incorrect password")
		if got := e.getUser(u.ID); got.PasswordHash != h {
			t.Error("mật khẩu không được đổi")
		}
	})
}

// S10: lỗi dịch vụ kiểm tra mật khẩu rò rỉ là lỗi (fail đóng) ở mọi đường đổi mật khẩu; không đổi mật khẩu, không thu hồi phiên.
func TestChangePasswordFailsClosedWhenPwnedCheckFails(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		l := e.login(u, nil)
		a := &application.AuthState{Login: l, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		e.pwned.err = errors.New("HIBP down")
		wantErr(t, e.svc.ChangePassword(ctx, a, "mat-khau-cu-123", "mat-khau-moi-123"), domain.KindInternal, "")
		if got := e.getUser(u.ID); !e.svc.VerifyPassword(got, "mat-khau-cu-123") || e.revoked(l.ID) {
			t.Error("lỗi pwned không được đổi mật khẩu hay thu hồi phiên")
		}
		// cùng lỗi ở đường link: link không bị tiêu thụ (người dùng thử lại được)
		sr, secret, _ := e.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestReset, "")
		if err := e.svc.SetPasswordWithRequest(ctx, sr.ID, secret, "mat-khau-moi-123"); err == nil || e.used(sr.ID) {
			t.Errorf("link phải còn nguyên khi pwned lỗi: err=%v used=%v", err, e.used(sr.ID))
		}
	})
}

// S14: log không chứa email, mật khẩu, secret của link hay mã đăng nhập, ở mọi luồng P4 (kể cả đường lỗi).
func TestP4FlowsNeverLogEmailsPasswordsOrSecrets(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		var buf bytes.Buffer
		e.svc.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		ctx := context.Background()
		p := e.openProjectWithPolicy2()
		e.svc.Registration.RequireVerifiedEmailForProject = true
		e.mail.Err = errors.New("smtp down") // đường lỗi gửi thư cũng ghi log

		secrets := []string{"mat-khau-dai-123", "mat-khau-moi-123", "mat-khau-cu-123"}
		emails := []string{"nhat-ky@example.org", "khong-co@example.org", "benh-nhan@example.org"}

		res, err := e.svc.NewUser(ctx, e.newUserReq(emails[0], nil))
		if err != nil {
			t.Fatal(err)
		}
		_, _, _ = e.svc.NewProject(ctx, application.NewProjectRequest{LoginID: res.LoginID, ProjectName: "Dự án nhật ký"}) // 400 cần xác minh
		_ = e.svc.ResetPassword(ctx, emails[0], "", "", true)
		_ = e.svc.ResetPassword(ctx, emails[1], "", "", true)
		_, err = e.svc.NewUser(ctx, e.newUserReq(emails[0], nil)) // trùng email
		if err == nil {
			t.Fatal("phải trùng")
		}
		e.mail.Err = nil
		u := e.userWithPassword(emails[2], func(x *fhir.User) { x.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		_ = e.svc.ResetPassword(ctx, emails[2], p.ID, "", true)
		id, secret := e.lastLink("setpassword")
		secrets = append(secrets, secret)
		_ = e.svc.SetPasswordWithRequest(ctx, id, "sai", "mat-khau-moi-123")
		if err := e.svc.SetPasswordWithRequest(ctx, id, secret, "mat-khau-moi-123"); err != nil {
			t.Fatal(err)
		}
		_ = e.svc.SendVerificationEmail(ctx, u, "login-1")
		vid, vsecret := e.lastLink("verifyemail")
		secrets = append(secrets, vsecret)
		if _, err := e.svc.VerifyEmail(ctx, vid, vsecret); err != nil {
			t.Fatal(err)
		}
		a := &application.AuthState{Login: e.login(u, nil), Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		_ = e.svc.ChangePassword(ctx, a, "sai-sai-sai-123", "mat-khau-moi-123")
		_ = e.svc.ChangePassword(ctx, a, "mat-khau-moi-123", "mat-khau-cu-123")

		log := buf.String()
		if log == "" {
			t.Fatal("test vô nghĩa nếu không có log nào")
		}
		for _, s := range append(secrets, emails...) {
			if strings.Contains(log, s) {
				t.Errorf("log chứa %q", s)
			}
		}
		if h := domain.HashSecurityRequestSecret(secret); strings.Contains(log, h) {
			t.Error("log chứa băm của secret")
		}
	})
}

// S15: thư chỉ chứa MỘT liên kết với id và secret; không chứa mật khẩu, băm mật khẩu hay băm secret.
func TestMailsContainOnlyTheLink(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.userWithPassword("an@example.org", nil)
		_ = e.svc.ResetPassword(ctx, "an@example.org", "", "", true)
		_ = e.svc.SendVerificationEmail(ctx, u, "l1")
		sent := e.mail.Sent()
		if len(sent) != 2 {
			t.Fatalf("thư: %d", len(sent))
		}
		urlRe := regexp.MustCompile(`https?://\S+`)
		for _, m := range sent {
			links := urlRe.FindAllString(m.Text, -1)
			if len(links) != 1 || !linkRe.MatchString(links[0]) {
				t.Errorf("%s: phải có đúng một liên kết hợp lệ: %v", m.Subject, links)
			}
			id, secret := linkRe.FindStringSubmatch(links[0])[2], linkRe.FindStringSubmatch(links[0])[3]
			sr, err := e.r.SecurityRequests.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{"mat-khau-cu-123", u.PasswordHash, sr.Secret} {
				if bad != "" && strings.Contains(m.Text, bad) {
					t.Errorf("%s: thư chứa dữ liệu nhạy cảm %q", m.Subject, bad)
				}
			}
			if m.To != "an@example.org" || strings.Contains(m.Text, secret+secret) {
				t.Errorf("%s: %+v", m.Subject, m)
			}
		}
	})
}
