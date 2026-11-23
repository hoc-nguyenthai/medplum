package bootstrap

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/mailer"
)

var mailLink = regexp.MustCompile(`https://app\.example\.org/(setpassword|verifyemail)/([0-9a-f-]{36})/([0-9a-f]{32})`)

// newCredAPI dựng API có thư giả; AUTH_APP_BASE_URL là nơi link trong thư trỏ tới.
func newCredAPI(t *testing.T) (*api, *mailer.Fake) {
	t.Helper()
	a := newAPI(t, func(s *config.Settings) { s.AppBaseURL = "https://app.example.org" }, nil)
	f := &mailer.Fake{}
	a.c.Auth.Mailer = f
	return a, f
}

func linkOf(t *testing.T, f *mailer.Fake, kind string) (id, secret string) {
	t.Helper()
	sent := f.Sent()
	if len(sent) == 0 {
		t.Fatal("chưa có thư")
	}
	m := mailLink.FindStringSubmatch(sent[len(sent)-1].Text)
	if m == nil || m[1] != kind {
		t.Fatalf("thư không có link %s: %q", kind, sent[len(sent)-1].Text)
	}
	return m[2], m[3]
}

func firstIssue(t *testing.T, r resp) (text, expr string) {
	t.Helper()
	i := issues(r)[0]
	text, _ = i["details"].(map[string]any)["text"].(string)
	if e, ok := i["expression"].([]any); ok && len(e) > 0 {
		expr, _ = e[0].(string)
	}
	return
}

func TestHTTPResetThenSetPasswordThenLogin(t *testing.T) {
	a, f := newCredAPI(t)
	if r := a.do("POST", "/auth/resetpassword", `{"email":"Admin@Example.org"}`, nil); r.status != 200 || r.json()["id"] != "ok" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id, secret := linkOf(t, f, "setpassword")
	// phiên cũ phải chết sau khi đặt lại mật khẩu
	old := a.login(loginBody(""), nil).json()["login"].(string)
	body := `{"id":"` + id + `","secret":"` + secret + `","password":"mat-khau-moi-456"}`
	r := a.do("POST", "/auth/setpassword", body, nil)
	if r.status != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/fhir+json") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got, _ := a.c.Auth.Logins.Get(context.Background(), old); !domain.IsTrue(got.Revoked) {
		t.Error("setpassword phải thu hồi mọi phiên")
	}
	if r := a.login(loginBody(""), nil); r.status == 200 && r.json()["code"] != nil {
		t.Errorf("mật khẩu cũ không còn dùng được: %s", r.body)
	}
	if r := a.login(`{"email":"admin@example.org","password":"mat-khau-moi-456"}`, nil); r.status != 200 {
		t.Errorf("mật khẩu mới phải đăng nhập được: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/setpassword", body, nil); r.status != 400 {
		t.Errorf("link dùng lại: %d %s", r.status, r.body)
	} else if txt, _ := firstIssue(t, r); txt != "Already used" {
		t.Errorf("thông báo: %q", txt)
	}
}

func TestHTTPResetPasswordResponsesAreIdenticalForUnknownEmail(t *testing.T) {
	a, f := newCredAPI(t)
	known := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org"}`, nil)
	unknown := a.do("POST", "/auth/resetpassword", `{"email":"khong-co@example.org"}`, nil)
	if known.status != unknown.status || known.body != unknown.body {
		t.Errorf("phản hồi phải giống hệt:\n%d %s\n%d %s", known.status, known.body, unknown.status, unknown.body)
	}
	if len(f.Sent()) != 1 {
		t.Errorf("chỉ tài khoản có thật nhận thư: %d", len(f.Sent()))
	}
	// lỗi gửi thư cũng không đổi phản hồi
	f.Err = context.DeadlineExceeded
	if r := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org"}`, nil); r.status != known.status || r.body != known.body {
		t.Errorf("lỗi gửi thư làm đổi phản hồi: %d %s", r.status, r.body)
	}
}

func TestHTTPCredentialValidation(t *testing.T) {
	a, _ := newCredAPI(t)
	uid := domain.NewID()
	for _, tc := range []struct{ name, path, body, text, expr string }{
		{"reset email", "/auth/resetpassword", `{"email":"khong-phai-email"}`, "Valid email address is required", "email"},
		{"reset thiếu email", "/auth/resetpassword", `{}`, "Valid email address is required", "email"},
		{"reset projectId", "/auth/resetpassword", `{"email":"a@example.org","projectId":"x"}`, "Invalid projectId", "projectId"},
		{"set id", "/auth/setpassword", `{"id":"x","secret":"s","password":"mat-khau-123"}`, "Invalid request ID", "id"},
		{"set secret", "/auth/setpassword", `{"id":"` + uid + `","password":"mat-khau-123"}`, "Missing secret", "secret"},
		{"set mật khẩu ngắn", "/auth/setpassword", `{"id":"` + uid + `","secret":"s","password":"ngan"}`, "Password must be at least 8 characters", "password"},
		{"set mật khẩu dài", "/auth/setpassword", `{"id":"` + uid + `","secret":"s","password":"` + strings.Repeat("a", 73) + `"}`, "Password must be no more than 72 characters", "password"},
		{"verify id", "/auth/verifyemail", `{"id":"x","secret":"s"}`, "Invalid request ID", "id"},
		{"verify secret", "/auth/verifyemail", `{"id":"` + uid + `"}`, "Missing secret", "secret"},
		{"không phải JSON", "/auth/setpassword", `không phải json`, "Content could not be parsed", ""},
	} {
		r := a.do("POST", tc.path, tc.body, nil)
		text, expr := firstIssue(t, r)
		if r.status != 400 || text != tc.text || expr != tc.expr {
			t.Errorf("%s: %d %q %q, mong %q %q", tc.name, r.status, text, expr, tc.text, tc.expr)
		}
	}
	// id hợp lệ nhưng không có request: 404, không phải 500
	if r := a.do("POST", "/auth/setpassword", `{"id":"`+uid+`","secret":"s","password":"mat-khau-123"}`, nil); r.status != 404 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func TestHTTPVerifyEmailRedirectIsLocationHeaderNot302(t *testing.T) {
	ctx := context.Background()
	a, _ := newCredAPI(t)
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	for _, tc := range []struct{ redirect, location string }{{"https://app.example.org/register?login=1", "https://app.example.org/register?login=1"}, {"", ""}} {
		sr, secret, err := a.c.Auth.IssueSecurityRequest(ctx, admin, domain.SecurityRequestVerifyEmail, tc.redirect)
		if err != nil {
			t.Fatal(err)
		}
		r := a.do("POST", "/auth/verifyemail", `{"id":"`+sr.ID+`","secret":"`+secret+`"}`, nil)
		if r.status != 200 || r.hdr.Get("Location") != tc.location || r.json()["id"] != "ok" {
			t.Errorf("redirect=%q: %d Location=%q %s", tc.redirect, r.status, r.hdr.Get("Location"), r.body)
		}
	}
	if got, _ := a.c.Auth.Users.Get(ctx, admin.ID); !domain.IsTrue(got.EmailVerified) {
		t.Error("email phải được đánh dấu đã xác minh")
	}
}

func TestHTTPChangePassword(t *testing.T) {
	ctx := context.Background()
	a, _ := newCredAPI(t)
	l := a.login(loginBody(""), nil).json()
	login, _ := a.c.Auth.Logins.Get(ctx, l["login"].(string))
	login, _ = a.c.Auth.GrantLogin(ctx, login)
	a.c.Authenticator = fakeAuthenticator{&application.AuthState{Login: login, Membership: a.seed.Membership, Project: a.seed.Project}}
	e, err := a.c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e)
	defer srv.Close()
	a.srv = srv
	auth := map[string]string{"Authorization": "Bearer token-hop-le"}

	if r := a.do("POST", "/auth/changepassword", `{"oldPassword":"x","newPassword":"mat-khau-moi-456"}`, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	for _, tc := range []struct{ name, body, text, expr string }{
		{"thiếu cũ", `{"newPassword":"mat-khau-moi-456"}`, "Missing oldPassword", "oldPassword"},
		{"mới ngắn", `{"oldPassword":"x","newPassword":"ngan"}`, "Password must be at least 8 characters", "newPassword"},
		{"cũ sai", `{"oldPassword":"sai-mat-khau","newPassword":"mat-khau-moi-456"}`, "Incorrect password", "oldPassword"},
	} {
		r := a.do("POST", "/auth/changepassword", tc.body, auth)
		if text, expr := firstIssue(t, r); r.status != 400 || text != tc.text || expr != tc.expr {
			t.Errorf("%s: %d %q %q", tc.name, r.status, text, expr)
		}
	}
	r := a.do("POST", "/auth/changepassword", `{"oldPassword":"`+adminPassword+`","newPassword":"mat-khau-moi-456"}`, auth)
	if r.status != 200 || r.json()["id"] != "ok" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got, _ := a.c.Auth.Logins.Get(ctx, login.ID); !domain.IsTrue(got.Revoked) {
		t.Error("phiên đang dùng cũng phải bị thu hồi")
	}
	if r := a.login(`{"email":"admin@example.org","password":"mat-khau-moi-456"}`, nil); r.status != 200 {
		t.Errorf("mật khẩu mới: %d %s", r.status, r.body)
	}
}

func TestHTTPResetPasswordSendEmailFalseSendsNothing(t *testing.T) {
	a, f := newCredAPI(t)
	for _, v := range []string{"false", "true", "0"} {
		r := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org","sendEmail":`+v+`}`, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", v, r.status, r.body)
		}
	}
	// chỉ sendEmail=false bị bỏ qua; true và giá trị khác false vẫn gửi (như Medplum: chỉ `=== false` mới tắt)
	if n := len(f.Sent()); n != 2 {
		t.Errorf("mong 2 thư (true và 0), được %d", n)
	}
}
