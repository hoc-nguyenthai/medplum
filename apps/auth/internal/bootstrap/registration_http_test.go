package bootstrap

import (
	"context"
	"strings"
	"sync"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

type captchaFake struct {
	mu      sync.Mutex
	ok      bool
	secrets []string
	tokens  []string
}

func (f *captchaFake) Verify(_ context.Context, secret, token string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets, f.tokens = append(f.secrets, secret), append(f.tokens, token)
	return f.ok, nil
}

func newRegAPI(t *testing.T, mut func(*config.Settings)) (*api, *captchaFake) {
	t.Helper()
	a := newAPI(t, func(s *config.Settings) {
		s.AppBaseURL = "https://app.example.org"
		if mut != nil {
			mut(s)
		}
	}, nil)
	f := &captchaFake{ok: true}
	a.c.Auth.Recaptcha = f
	return a, f
}

const newUserJSON = `{"firstName":"Binh","lastName":"Tran","email":"Binh@Example.org","password":"mat-khau-dai-123","projectId":"new"`

func TestHTTPNewUserSuccessHasLoginAndNoCode(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	r := a.do("POST", "/auth/newuser", newUserJSON+`}`, nil)
	if r.status != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	m := r.json()
	if m["code"] != nil || m["cookie"] != nil || m["emailVerificationRequired"] != false || !domain.IsUUID(m["login"].(string)) {
		t.Fatalf("%s", r.body)
	}
	for _, leak := range []string{"passwordHash", "mat-khau-dai-123", "Binh@Example.org"} {
		if strings.Contains(r.body, leak) {
			t.Errorf("phản hồi lộ %q", leak)
		}
	}
	if u, _ := a.c.Auth.FindUserInScope(ctx, "binh@example.org", ""); u == nil {
		t.Fatal("user phải được tạo với email chữ thường")
	}
	// người vừa đăng ký đăng nhập được bằng mật khẩu
	if l := a.login(`{"email":"binh@example.org","password":"mat-khau-dai-123","projectId":"new"}`, nil); l.status != 200 {
		t.Errorf("đăng nhập: %d %s", l.status, l.body)
	}
	if r := a.do("POST", "/auth/newuser", newUserJSON+`}`, nil); r.status != 400 {
		t.Errorf("trùng email: %d", r.status)
	} else if text, expr := firstIssue(t, r); text != "Email already registered" || expr != "email" {
		t.Errorf("%q %q", text, expr)
	}
}

func TestHTTPNewUserValidation(t *testing.T) {
	a, _ := newRegAPI(t, nil)
	long := strings.Repeat("x", 129)
	for _, tc := range []struct{ name, body, text, expr string }{
		{"thiếu tên", `{"lastName":"T","email":"a@example.org","password":"mat-khau-123"}`, "First name must be between 1 and 128 characters", "firstName"},
		{"tên quá dài", `{"firstName":"` + long + `","lastName":"T","email":"a@example.org","password":"mat-khau-123"}`, "First name must be between 1 and 128 characters", "firstName"},
		{"thiếu họ", `{"firstName":"A","email":"a@example.org","password":"mat-khau-123"}`, "Last name must be between 1 and 128 characters", "lastName"},
		{"email sai", `{"firstName":"A","lastName":"T","email":"khong","password":"mat-khau-123"}`, "Valid email address between 3 and 72 characters is required", "email"},
		{"email quá dài", `{"firstName":"A","lastName":"T","email":"` + strings.Repeat("a", 30) + `@` + strings.Repeat("b", 38) + `.org","password":"mat-khau-123"}`, "Valid email address between 3 and 72 characters is required", "email"},
		{"email 72 ký tự vẫn hợp lệ", `{"firstName":"A","lastName":"T","email":"` + strings.Repeat("a", 30) + `@` + strings.Repeat("b", 35) + `.org","password":"xyz"}`, "Password must be at least 8 characters", "password"},
		{"mật khẩu ngắn", `{"firstName":"A","lastName":"T","email":"a@example.org","password":"xyz"}`, "Password must be at least 8 characters", "password"},
		{"mật khẩu 300 ký tự", `{"firstName":"A","lastName":"T","email":"a@example.org","password":"` + strings.Repeat("a", 300) + `"}`, "Password must be no more than 72 characters", "password"},
		{"đa byte vượt 72 byte", `{"firstName":"A","lastName":"T","email":"a@example.org","password":"` + strings.Repeat("☺", 40) + `"}`, "Password must be no more than 72 characters", "password"},
		{"đa byte đủ ký tự mới tính", `{"firstName":"A","lastName":"T","email":"a@example.org","password":"🔑🔓"}`, "Password must be at least 8 characters", "password"},
		{"không phải JSON", `không phải json`, "Content could not be parsed", ""},
	} {
		r := a.do("POST", "/auth/newuser", tc.body, nil)
		if text, expr := firstIssue(t, r); r.status != 400 || text != tc.text || expr != tc.expr {
			t.Errorf("%s: %d %q %q", tc.name, r.status, text, expr)
		}
	}
	// mỗi trường một lỗi, theo thứ tự khai báo
	r := a.do("POST", "/auth/newuser", `{}`, nil)
	var exprs []string
	for _, i := range issues(r) {
		exprs = append(exprs, i["expression"].([]any)[0].(string))
	}
	if strings.Join(exprs, ",") != "firstName,lastName,email,password" {
		t.Errorf("thứ tự lỗi: %v", exprs)
	}
}

func TestHTTPNewUserRecaptchaRunsBeforeRegistrationDisabled(t *testing.T) {
	a, f := newRegAPI(t, func(s *config.Settings) {
		s.RecaptchaSiteKey, s.RecaptchaSecretKey = "site-server", "secret-server"
		s.RegisterEnabled = false
	})
	_ = f
	// thiếu token: báo reCAPTCHA, KHÔNG lộ việc đăng ký đã tắt cho người chưa qua reCAPTCHA
	r := a.do("POST", "/auth/newuser", newUserJSON+`}`, nil)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Recaptcha token is required" {
		t.Errorf("%d %s", r.status, r.body)
	}
	f.ok = false
	if r := a.do("POST", "/auth/newuser", newUserJSON+`,"recaptchaToken":"tok"}`, nil); func() string { x, _ := firstIssue(t, r); return x }() != "Recaptcha failed" {
		t.Errorf("%s", r.body)
	}
	f.ok = true
	if r := a.do("POST", "/auth/newuser", newUserJSON+`,"recaptchaToken":"tok"}`, nil); func() string { x, _ := firstIssue(t, r); return x }() != "Registration is disabled" {
		t.Errorf("%s", r.body)
	}
	if len(f.secrets) == 0 || f.secrets[0] != "secret-server" {
		t.Errorf("secrets: %v", f.secrets)
	}
}

func TestHTTPNewUserProjectRecaptcha(t *testing.T) {
	ctx := context.Background()
	a, f := newRegAPI(t, nil)
	p, err := a.c.Auth.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", Name: "Mở",
		Setting:               []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptrBool(true)}},
		DefaultAccessPolicies: []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/p"}}},
		Site:                  []fhir.ProjectSite{{Name: "w", Domain: []string{"example.org"}, RecaptchaSiteKey: "site-p", RecaptchaSecretKey: "secret-p"}}})
	if err != nil {
		t.Fatal(err)
	}
	pid := p.GetID()
	body := func(project, site, token string) string {
		return `{"firstName":"A","lastName":"T","email":"a@example.org","password":"mat-khau-dai-123","projectId":"` + project + `","recaptchaSiteKey":"` + site + `","recaptchaToken":"` + token + `"}`
	}
	r := a.do("POST", "/auth/newuser", body(pid, "site-p", "tok"), nil)
	if r.status != 200 || r.json()["login"] == nil || f.secrets[len(f.secrets)-1] != "secret-p" {
		t.Fatalf("%d %s %v", r.status, r.body, f.secrets)
	}
	if text, _ := firstIssue(t, a.do("POST", "/auth/newuser", body(domain.NewID(), "site-p", "tok"), nil)); text != "Invalid recaptchaSiteKey" {
		t.Errorf("projectId lệch: %q", text)
	}
	if text, _ := firstIssue(t, a.do("POST", "/auth/newuser", body(pid, "site-la", "tok"), nil)); text != "Invalid recaptchaSiteKey" {
		t.Errorf("site key lạ: %q", text)
	}
	// project có khóa riêng nhưng KHÔNG cho đăng ký mở: newuser bị chặn, còn resetpassword (không kiểm project) thì không
	closed, err := a.c.Auth.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", Name: "Đóng",
		Site: []fhir.ProjectSite{{Name: "w", Domain: []string{"example.org"}, RecaptchaSiteKey: "site-c", RecaptchaSecretKey: "secret-c"}}})
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := firstIssue(t, a.do("POST", "/auth/newuser", body(closed.GetID(), "site-c", "tok"), nil)); text != "Project does not allow open registration" {
		t.Errorf("newuser vào project đóng: %q", text)
	}
	if r := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org","projectId":"`+closed.GetID()+`","recaptchaSiteKey":"site-c","recaptchaToken":"tok"}`, nil); r.status != 200 {
		t.Errorf("resetpassword không kiểm project: %d %s", r.status, r.body)
	}
}

func ptrBool(b bool) *bool { return &b }

func TestHTTPNewUserWithClient(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	body := func(client string) string {
		return `{"firstName":"A","lastName":"T","email":"a@example.org","password":"mat-khau-dai-123","clientId":"` + client + `","codeChallenge":"abc","codeChallengeMethod":"plain"}`
	}
	// client của project KHÔNG cho đăng ký mở (project seed): không đăng ký được qua client này
	closed := a.mustClient()
	r := a.do("POST", "/auth/newuser", body(closed), nil)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Project does not allow open registration" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if u, _ := a.c.Auth.FindUserInScope(ctx, "a@example.org", ""); u != nil {
		t.Fatal("không được tạo user khi project không cho đăng ký")
	}
	// project mở: được, và vẫn KHÔNG có code hay cookie
	p, err := a.c.Auth.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", Name: "Mở",
		Setting:               []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptrBool(true)}},
		DefaultAccessPolicies: []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/p"}}}})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := a.c.Auth.Resources.Create(ctx, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Web", Meta: &fhir.Meta{Project: p.GetID()}})
	if err != nil {
		t.Fatal(err)
	}
	r = a.do("POST", "/auth/newuser", body(cl.GetID()), nil)
	if r.status != 200 || r.json()["code"] != nil || r.json()["login"] == nil || r.hdr.Get("Set-Cookie") != "" {
		t.Fatalf("%d %s %v", r.status, r.body, r.hdr)
	}
}

func TestHTTPNewUserRequireVerifiedEmail(t *testing.T) {
	a, _ := newRegAPI(t, func(s *config.Settings) { s.RequireVerifiedEmailForProjectCreation = true })
	m := &mailFake{}
	a.c.Auth.Mailer = m
	r := a.do("POST", "/auth/newuser", newUserJSON+`}`, nil)
	if r.status != 200 || r.json()["emailVerificationRequired"] != true || len(m.sent) != 1 || !strings.Contains(m.sent[0].Text, "https://app.example.org/verifyemail/") {
		t.Fatalf("%d %s %+v", r.status, r.body, m.sent)
	}
}

type mailFake struct{ sent []application.Message }

func (m *mailFake) Send(_ context.Context, msg application.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

func TestHTTPResetPasswordRecaptcha(t *testing.T) {
	a, f := newRegAPI(t, func(s *config.Settings) { s.RecaptchaSiteKey, s.RecaptchaSecretKey = "site-server", "secret-server" })
	m := &mailFake{}
	a.c.Auth.Mailer = m
	if text, _ := firstIssue(t, a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org"}`, nil)); text != "Recaptcha token is required" {
		t.Errorf("%q", text)
	}
	f.ok = false
	if text, _ := firstIssue(t, a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org","recaptchaToken":"t"}`, nil)); text != "Recaptcha failed" {
		t.Errorf("%q", text)
	}
	if len(m.sent) != 0 {
		t.Error("không gửi thư khi chưa qua reCAPTCHA")
	}
	f.ok = true
	if r := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org","recaptchaToken":"t"}`, nil); r.status != 200 || len(m.sent) != 1 {
		t.Errorf("%d thư=%d", r.status, len(m.sent))
	}
}

// newuser dùng chung bộ đếm "login" (chặt hơn): dò email qua "Email already registered" bị giảm nhẹ bằng hạn mức (S12).
func TestHTTPNewUserSharesLoginRateLimit(t *testing.T) {
	a, _ := newRegAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 2 })
	for i := 0; i < 2; i++ {
		if r := a.login(loginBody(""), nil); r.status != 200 {
			t.Fatalf("lần %d: %d %s", i, r.status, r.body)
		}
	}
	if r := a.do("POST", "/auth/newuser", newUserJSON+`}`, nil); r.status != 429 {
		t.Fatalf("newuser phải chung hạn mức đăng nhập: %d %s", r.status, r.body)
	}
}

func newProjectBody(login, name string) string {
	return `{"login":"` + login + `","projectName":"` + name + `"}`
}

func TestHTTPNewUserThenNewProjectReturnsCodeAndCookie(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	m := &mailFake{}
	a.c.Auth.Mailer = m
	cid := a.mustClient()
	r := a.do("POST", "/auth/newuser", `{"firstName":"Binh","lastName":"Tran","email":"binh@example.org","password":"mat-khau-dai-123","projectId":"new","clientId":"`+cid+`"}`, nil)
	// client của project seed không cho đăng ký mở nên đăng ký bằng projectId=new + clientId là lệch; đăng ký không kèm client
	if r.status == 200 {
		t.Fatalf("client của project khác mà projectId=new phải bị từ chối: %s", r.body)
	}
	r = a.do("POST", "/auth/newuser", newUserJSON+`,"codeChallenge":"abc","codeChallengeMethod":"plain"}`, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	login := r.json()["login"].(string)
	p := a.do("POST", "/auth/newproject", newProjectBody(login, "Phòng khám Bình"), nil)
	if p.status != 200 || p.json()["code"] == nil || p.json()["login"] != login {
		t.Fatalf("%d %s", p.status, p.body)
	}
	if got, _ := a.c.Auth.Logins.Get(ctx, login); got.Membership == nil || got.Project == nil || got.Project.Reference == "Project/new" {
		t.Errorf("login phải có project và membership thật: %+v", got)
	}
	if len(m.sent) != 1 || !strings.HasPrefix(m.sent[0].Subject, "Welcome to") {
		t.Errorf("thư chào: %+v", m.sent)
	}
	// dùng lại login: 400; code đổi được token (Authenticator thật cần P3: kiểm ở e2e)
	if p2 := a.do("POST", "/auth/newproject", newProjectBody(login, "Phòng khám Bình"), nil); p2.status != 400 {
		t.Errorf("dùng lại login: %d", p2.status)
	} else if text, _ := firstIssue(t, p2); text != "Login already has a membership" {
		t.Errorf("%q", text)
	}
}

func TestHTTPNewProjectValidation(t *testing.T) {
	a, _ := newRegAPI(t, nil)
	uid := domain.NewID()
	for _, tc := range []struct{ name, body, text, expr string }{
		{"thiếu login", `{"projectName":"Phòng khám"}`, "Login ID is required.", "login"},
		{"login không phải UUID", `{"login":"x","projectName":"Phòng khám"}`, "Login ID is required.", "login"},
		{"tên ngắn", `{"login":"` + uid + `","projectName":"abc"}`, "Project name must be between 4 and 72 characters", "projectName"},
		{"tên dài", `{"login":"` + uid + `","projectName":"` + strings.Repeat("x", 73) + `"}`, "Project name must be between 4 and 72 characters", "projectName"},
		{"thiếu tên", `{"login":"` + uid + `"}`, "Project name must be between 4 and 72 characters", "projectName"},
	} {
		r := a.do("POST", "/auth/newproject", tc.body, nil)
		if text, expr := firstIssue(t, r); r.status != 400 || text != tc.text || expr != tc.expr {
			t.Errorf("%s: %d %q %q", tc.name, r.status, text, expr)
		}
	}
	// tên 4 ký tự nhiều byte hợp lệ (đếm ký tự), login không có thì 404
	if r := a.do("POST", "/auth/newproject", newProjectBody(uid, "Việt"), nil); r.status != 404 {
		t.Errorf("login lạ: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/newproject", newProjectBody(uid, strings.Repeat("ệ", 72)), nil); r.status != 404 {
		t.Errorf("72 ký tự nhiều byte: %d %s", r.status, r.body)
	}
}

func TestHTTPNewProjectSharesLoginRateLimit(t *testing.T) {
	a, _ := newRegAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 2 })
	for i := 0; i < 2; i++ {
		a.login(loginBody(""), nil)
	}
	if r := a.do("POST", "/auth/newproject", newProjectBody(domain.NewID(), "Phòng khám"), nil); r.status != 429 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestHTTPNewProjectDisabledFlags(t *testing.T) {
	a, _ := newRegAPI(t, func(s *config.Settings) { s.NewProjectRegistrationEnabled = false })
	r := a.do("POST", "/auth/newuser", newUserJSON+`}`, nil)
	if r.status != 200 {
		t.Fatalf("newuser vẫn chạy khi chỉ tắt tạo project: %d %s", r.status, r.body)
	}
	p := a.do("POST", "/auth/newproject", newProjectBody(r.json()["login"].(string), "Phòng khám"), nil)
	if text, _ := firstIssue(t, p); p.status != 400 || text != "New project registration is disabled" {
		t.Errorf("%d %s", p.status, p.body)
	}
}

// Login thuộc một client OAuth thì newproject đặt cookie đăng nhập của client đó (như /auth/login), vì phản hồi có code.
func TestHTTPNewProjectSetsClientCookie(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	p, err := a.c.Auth.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", Name: "Mở",
		Setting:               []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptrBool(true)}},
		DefaultAccessPolicies: []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/p"}}}})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := a.c.Auth.Resources.Create(ctx, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Web", Meta: &fhir.Meta{Project: p.GetID()}, PkceOptional: ptrBool(true)})
	if err != nil {
		t.Fatal(err)
	}
	// client của project mở nhưng đăng ký với projectId=new: lệch; đăng ký bằng client rồi để login mang project của client
	r := a.do("POST", "/auth/newuser", `{"firstName":"A","lastName":"T","email":"a@example.org","password":"mat-khau-dai-123","clientId":"`+cl.GetID()+`"}`, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// login này đã gắn project thật của client, nên newproject bị từ chối ("Invalid project"); dùng login mới cho project "new"
	if p := a.do("POST", "/auth/newproject", newProjectBody(r.json()["login"].(string), "Phòng khám"), nil); p.status != 400 {
		t.Fatalf("login của project khác: %d %s", p.status, p.body)
	}
	u, _ := a.c.Auth.FindUserInScope(ctx, "a@example.org", "")
	lg, err := a.c.Auth.Logins.Create(ctx, &fhir.Login{ResourceType: "Login", User: &fhir.Reference{Reference: "User/" + u.ID},
		Client: &fhir.Reference{Reference: "ClientApplication/" + cl.GetID()}, Project: &fhir.Reference{Reference: "Project/new"}, AuthMethod: domain.AuthPassword, AuthTime: "2026-01-01T00:00:00Z", Scope: "openid"})
	if err != nil {
		t.Fatal(err)
	}
	res := a.do("POST", "/auth/newproject", newProjectBody(lg.ID, "Phòng khám"), nil)
	if res.status != 200 || res.json()["code"] == nil || !strings.Contains(res.hdr.Get("Set-Cookie"), "medplum-"+cl.GetID()+"=") {
		t.Fatalf("%d %s Set-Cookie=%q", res.status, res.body, res.hdr.Get("Set-Cookie"))
	}
	if strings.Contains(res.body, "cookie") {
		t.Errorf("cookie không được vào JSON: %s", res.body)
	}
}

// openPatientProject dựng project cho đăng ký mở kèm chính sách có thật.
func openPatientProject(t *testing.T, a *api) (projectID, policyID string) {
	t.Helper()
	ctx := context.Background()
	pol, err := a.c.Auth.Resources.Create(ctx, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "Patient mở"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.c.Auth.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", Name: "Mở",
		Setting:               []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptrBool(true)}},
		DefaultAccessPolicies: []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + pol.GetID()}}}})
	if err != nil {
		t.Fatal(err)
	}
	return p.GetID(), pol.GetID()
}

func TestHTTPNewUserThenNewPatient(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	pid, polID := openPatientProject(t, a)
	r := a.do("POST", "/auth/newuser", `{"firstName":"Chi","lastName":"Le","email":"chi@example.org","password":"mat-khau-dai-123","projectId":"`+pid+`"}`, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	login := r.json()["login"].(string)
	p := a.do("POST", "/auth/newpatient", `{"login":"`+login+`","projectId":"`+pid+`"}`, nil)
	if p.status != 200 || p.json()["code"] == nil || p.json()["login"] != login {
		t.Fatalf("%d %s", p.status, p.body)
	}
	lg, _ := a.c.Auth.Logins.Get(ctx, login)
	m, err := a.c.Auth.Memberships.Get(ctx, strings.TrimPrefix(lg.Membership.Reference, "ProjectMembership/"))
	if err != nil || m.AccessPolicy == nil || m.AccessPolicy.Reference != "AccessPolicy/"+polID {
		t.Fatalf("membership: %+v %v", m, err)
	}
	if p2 := a.do("POST", "/auth/newpatient", `{"login":"`+login+`","projectId":"`+pid+`"}`, nil); p2.status != 400 {
		t.Errorf("dùng lại login: %d", p2.status)
	}
	if p3 := a.do("POST", "/auth/newpatient", `{"login":"`+domain.NewID()+`","projectId":"`+pid+`"}`, nil); p3.status != 404 {
		t.Errorf("login lạ: %d", p3.status)
	}
}

func TestHTTPNewPatientValidation(t *testing.T) {
	a, _ := newRegAPI(t, nil)
	for _, tc := range []struct{ name, body, text, expr string }{
		{"thiếu login", `{"projectId":"x"}`, "Missing login", "login"},
		{"login rỗng", `{"login":"","projectId":"x"}`, "Missing login", "login"},
		{"thiếu projectId", `{"login":"x"}`, "Project ID is required", "projectId"},
		{"không phải JSON", `không phải json`, "Content could not be parsed", ""},
	} {
		r := a.do("POST", "/auth/newpatient", tc.body, nil)
		if text, expr := firstIssue(t, r); r.status != 400 || text != tc.text || expr != tc.expr {
			t.Errorf("%s: %d %q %q", tc.name, r.status, text, expr)
		}
	}
	r := a.do("POST", "/auth/newpatient", `{}`, nil)
	if is := issues(r); len(is) != 2 {
		t.Errorf("hai trường thiếu phải có hai lỗi: %s", r.body)
	}
}

// newpatient không có hạn mức riêng (Medplum: chỉ hạn mức chung của /auth/*): không dùng chung bộ đếm đăng nhập.
func TestHTTPNewPatientIsNotInTheLoginRateLimit(t *testing.T) {
	a, _ := newRegAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 1 })
	a.login(loginBody(""), nil)
	if r := a.login(loginBody(""), nil); r.status != 429 {
		t.Fatalf("bộ đếm đăng nhập phải đã cạn: %d", r.status)
	}
	if r := a.do("POST", "/auth/newpatient", `{"login":"`+domain.NewID()+`","projectId":"`+domain.NewID()+`"}`, nil); r.status == 429 {
		t.Errorf("newpatient không thuộc hạn mức đăng nhập")
	}
}

func TestHTTPNewPatientSetsClientCookie(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	pid, _ := openPatientProject(t, a)
	cl, err := a.c.Auth.Resources.Create(ctx, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Web", Meta: &fhir.Meta{Project: pid}, PkceOptional: ptrBool(true)})
	if err != nil {
		t.Fatal(err)
	}
	r := a.do("POST", "/auth/newuser", `{"firstName":"Chi","lastName":"Le","email":"chi@example.org","password":"mat-khau-dai-123","projectId":"`+pid+`","clientId":"`+cl.GetID()+`"}`, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	p := a.do("POST", "/auth/newpatient", `{"login":"`+r.json()["login"].(string)+`","projectId":"`+pid+`"}`, nil)
	if p.status != 200 || p.json()["code"] == nil || !strings.Contains(p.hdr.Get("Set-Cookie"), "medplum-"+cl.GetID()+"=") || strings.Contains(p.body, "cookie") {
		t.Fatalf("%d %s Set-Cookie=%q", p.status, p.body, p.hdr.Get("Set-Cookie"))
	}
}

// Hồi quy: client dựng sẵn (medplum-cli, không có trong kho) phải đổi được code. Trước đây đường code grant tra client thẳng từ kho nên luôn
// trả "Invalid client", và chuỗi newuser -> newproject -> token không dùng được với client dựng sẵn.
func TestHTTPStandardClientCanExchangeCodeAfterNewProject(t *testing.T) {
	a, _ := newRegAPI(t, nil)
	r := a.do("POST", "/auth/newuser", `{"firstName":"A","lastName":"T","email":"cli@example.org","password":"mat-khau-dai-123","clientId":"`+domain.CLIClientID+`","codeChallenge":"`+s256()+`","codeChallengeMethod":"S256"}`, nil)
	if r.status != 200 {
		t.Fatalf("newuser: %d %s", r.status, r.body)
	}
	p := a.do("POST", "/auth/newproject", newProjectBody(r.json()["login"].(string), "Phòng khám CLI"), nil)
	code, _ := p.json()["code"].(string)
	if p.status != 200 || code == "" {
		t.Fatalf("newproject: %d %s", p.status, p.body)
	}
	form := "grant_type=authorization_code&client_id=" + domain.CLIClientID + "&code=" + code + "&code_verifier=" + verifier
	tk := a.do("POST", "/oauth2/token", form, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if tk.status != 200 || tk.json()["access_token"] == nil {
		t.Fatalf("đổi code: %d %s", tk.status, tk.body)
	}
	// client khác không đổi được code của login này
	other := a.do("POST", "/oauth2/token", "grant_type=authorization_code&client_id="+a.mustClient()+"&code="+code, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if other.status == 200 {
		t.Errorf("client khác đổi được code: %s", other.body)
	}
}
