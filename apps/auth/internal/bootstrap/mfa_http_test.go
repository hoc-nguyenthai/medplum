package bootstrap

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/pquerna/otp"
	pq "github.com/pquerna/otp/totp"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/mfastate"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := pq.GenerateCodeCustom(secret, at, pq.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func uriSecret(t *testing.T, uri string) string {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil || u.Query().Get("secret") == "" {
		t.Fatalf("enrollUri: %q %v", uri, err)
	}
	return u.Query().Get("secret")
}

// requireMFAInSeedProject bật setting mfaRequired cho project seed.
func requireMFAInSeedProject(t *testing.T, a *api, extra ...fhir.ProjectSetting) {
	t.Helper()
	proj, err := a.c.Store.ReadResource(context.Background(), "Project", a.seed.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := proj.(*fhir.Project)
	p.Setting = append([]fhir.ProjectSetting{{Name: "mfaRequired", ValueBoolean: ptrBool(true)}}, extra...)
	if _, err := a.c.Store.UpdateResource(context.Background(), p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPMFAEnrollOnLoginThenVerifyOnNextLogin(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, func(s *config.Settings) { s.MFASecretKeys = "k1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" })
	requireMFAInSeedProject(t, a)

	r := a.login(loginBody(""), nil)
	m := r.json()
	if r.status != 200 || m["mfaEnrollRequired"] != true || m["code"] != nil || m["enrollUri"] == nil || m["enrollQrCode"] == nil {
		t.Fatalf("phải đòi đăng ký MFA, không có code: %d %s", r.status, r.body)
	}
	login, secret := m["login"].(string), uriSecret(t, m["enrollUri"].(string))
	if methods := m["allowedMfaMethods"].([]any); len(methods) != 1 || methods[0] != "totp" {
		t.Errorf("allowedMfaMethods: %v", methods)
	}

	// mã sai: 400 "Invalid MFA token"; thiếu token: "Missing token"
	bad := a.do("POST", "/auth/mfa/login-enroll", `{"login":"`+login+`","method":"totp","token":"000000"}`, nil)
	if text, _ := firstIssue(t, bad); bad.status != 400 || text != "Invalid MFA token" {
		t.Errorf("%d %s", bad.status, bad.body)
	}
	miss := a.do("POST", "/auth/mfa/login-enroll", `{"login":"`+login+`","method":"totp"}`, nil)
	if text, _ := firstIssue(t, miss); miss.status != 400 || text != "Missing token" {
		t.Errorf("%d %s", miss.status, miss.body)
	}
	at := time.Now()
	ok := a.do("POST", "/auth/mfa/login-enroll", `{"login":"`+login+`","method":"totp","token":"`+totpCode(t, secret, at)+`"}`, nil)
	if ok.status != 200 || ok.json()["code"] == nil || ok.json()["mfaRequired"] != nil {
		t.Fatalf("đăng ký xong phải có code: %d %s", ok.status, ok.body)
	}
	if lg, _ := a.c.Auth.Logins.Get(ctx, login); !domain.IsTrue(lg.MfaVerified) {
		t.Error("login phải mfaVerified")
	}
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	if !domain.IsTrue(admin.MfaEnrolled) || !strings.HasPrefix(admin.MfaSecret, "enc:v1:k1:") || strings.Contains(admin.MfaSecret, secret) {
		t.Errorf("secret phải lưu dạng mã hóa: %+v", admin)
	}
	// đăng nhập lần sau: chỉ thấy yêu cầu MFA, KHÔNG có code, rồi verify bằng mã của BƯỚC KẾ TIẾP (mã vừa dùng đã bị tiêu thụ)
	next := a.login(loginBody(""), nil)
	nm := next.json()
	if next.status != 200 || nm["mfaRequired"] != true || nm["code"] != nil || nm["email"] != "admin@example.org" {
		t.Fatalf("%d %s", next.status, next.body)
	}
	nl := nm["login"].(string)
	if r := a.do("POST", "/auth/mfa/verify", `{"login":"`+nl+`","token":"`+totpCode(t, secret, at)+`"}`, nil); r.status != 400 {
		t.Errorf("mã đã dùng ở login-enroll không được dùng lại: %d %s", r.status, r.body)
	}
	v := a.do("POST", "/auth/mfa/verify", `{"login":"`+nl+`","token":"`+totpCode(t, secret, at.Add(30*time.Second))+`"}`, nil)
	if v.status != 200 || v.json()["code"] == nil {
		t.Fatalf("verify: %d %s", v.status, v.body)
	}
}

func TestHTTPMFAValidationAndBearerRequirement(t *testing.T) {
	a, _ := newRegAPI(t, nil)
	for _, tc := range []struct{ path, body, text, expr string }{
		{"/auth/mfa/verify", `{"token":"123456"}`, "Missing login", "login"},
		{"/auth/mfa/verify", `{"login":"x"}`, "Missing token", "token"},
		{"/auth/mfa/login-enroll", `{}`, "Missing login", "login"},
		{"/auth/mfa/send-email", `{}`, "Missing login", "login"},
		{"/auth/mfa/verify", `không phải json`, "Content could not be parsed", ""},
	} {
		r := a.do("POST", tc.path, tc.body, nil)
		if text, expr := firstIssue(t, r); r.status != 400 || text != tc.text || expr != tc.expr {
			t.Errorf("%s %s: %d %q %q", tc.path, tc.body, r.status, text, expr)
		}
	}
	if r := a.do("POST", "/auth/mfa/verify", `{}`, nil); len(issues(r)) != 2 {
		t.Errorf("hai trường thiếu phải có hai lỗi: %s", r.body)
	}
	// login không có: 404 (không phải 500)
	if r := a.do("POST", "/auth/mfa/verify", `{"login":"`+domain.NewID()+`","token":"123456"}`, nil); r.status != 404 {
		t.Errorf("login lạ: %d %s", r.status, r.body)
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/auth/mfa/status"}, {"POST", "/auth/mfa/enroll"}, {"POST", "/auth/mfa/send-email-challenge"}, {"POST", "/auth/mfa/disable"},
	} {
		r := a.do(tc.method, tc.path, `{}`, nil)
		if r.status != 401 || !strings.Contains(r.hdr.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s %s phải cần Bearer: %d", tc.method, tc.path, r.status)
		}
	}
}

func TestHTTPMFABearerRoutes(t *testing.T) {
	ctx := context.Background()
	a, f := newRegAPI(t, nil)
	requireMFAInSeedProject(t, a, fhir.ProjectSetting{Name: "allowedMfaMethods", ValueString: "totp,email"})
	// phiên đã đăng nhập (bằng mật khẩu, trước khi project bắt buộc MFA ở thực tế): dựng AuthState như P3 sẽ cấp
	l := a.login(loginBody(""), nil).json()["login"].(string)
	login, _ := a.c.Auth.Logins.Get(ctx, l)
	login, _ = a.c.Auth.Logins.Mutate(ctx, login.ID, func(x *fhir.Login) error { x.MfaVerified = ptrBool(true); return nil })
	login, _ = a.c.Auth.GrantLogin(ctx, login)
	project, _ := a.c.Auth.Projects.Get(ctx, a.seed.Project.ID)
	a.c.Authenticator = fakeAuthenticator{&application.AuthState{Login: login, Membership: a.seed.Membership, Project: project}}
	e, err := a.c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	a.srv = srv
	auth := map[string]string{"Authorization": "Bearer token-hop-le"}
	_ = f

	st := a.do("GET", "/auth/mfa/status", "", auth)
	sm := st.json()
	if st.status != 200 || sm["enrolled"] != false || sm["mfaRequired"] != true || sm["enrollUri"] == nil || len(sm["allowedMethods"].([]any)) != 2 {
		t.Fatalf("%d %s", st.status, st.body)
	}
	secret := uriSecret(t, sm["enrollUri"].(string))
	if r := a.do("POST", "/auth/mfa/enroll", `{"method":"totp","token":"000000"}`, auth); r.status != 400 {
		t.Errorf("mã sai: %d", r.status)
	} else if text, _ := firstIssue(t, r); text != "Invalid token" {
		t.Errorf("%q", text)
	}
	if r := a.do("POST", "/auth/mfa/enroll", `{"token":"`+totpCode(t, secret, time.Now())+`"}`, auth); r.status != 200 || r.json()["id"] != "ok" {
		t.Fatalf("enroll: %d %s", r.status, r.body)
	}
	if r := a.do("GET", "/auth/mfa/status", "", auth); r.json()["enrolled"] != true || r.json()["enrollUri"] != nil {
		t.Errorf("sau khi đăng ký: %s", r.body)
	}
	// tắt: MFA bắt buộc nên không gỡ được yếu tố cuối
	r := a.do("POST", "/auth/mfa/disable", `{"token":"`+totpCode(t, secret, time.Now().Add(30*time.Second))+`"}`, auth)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Cannot remove the last MFA factor because MFA is required" {
		t.Errorf("%d %s", r.status, r.body)
	}
	// kiểm kiểu của body chỉ sau khi biết user đã đăng ký
	if r := a.do("POST", "/auth/mfa/disable", `{"token":123}`, auth); r.status != 400 {
		t.Errorf("%d", r.status)
	} else if text, expr := firstIssue(t, r); text != "Token must be a string" || expr != "token" {
		t.Errorf("%q %q", text, expr)
	}
	// email challenge + enroll email
	if r := a.do("POST", "/auth/mfa/send-email-challenge", "", auth); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestHTTPMFATooManyAttemptsReturns429WithRateLimitReset(t *testing.T) {
	a, _ := newRegAPI(t, func(s *config.Settings) { s.MFALoginAttemptLimit = 100; s.MFAUserAttemptLimit = 2 })
	ctx := context.Background()
	// user đã đăng ký MFA qua kho
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	if _, err := a.c.Auth.Users.Mutate(ctx, admin.ID, func(u *fhir.User) error {
		u.MfaEnrolled, u.MfaMethod, u.MfaSecret = ptrBool(true), []string{"totp"}, "JBSWY3DPEHPK3PXP"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var logins []string
	for i := 0; i < 3; i++ {
		logins = append(logins, a.login(loginBody(""), nil).json()["login"].(string))
	}
	for i := 0; i < 2; i++ {
		if r := a.do("POST", "/auth/mfa/verify", `{"login":"`+logins[i]+`","token":"000000"}`, nil); r.status != 400 {
			t.Fatalf("lần %d: %d %s", i, r.status, r.body)
		}
	}
	r := a.do("POST", "/auth/mfa/verify", `{"login":"`+logins[2]+`","token":"000000"}`, nil)
	if r.status != 429 || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("%d %v %s", r.status, r.hdr, r.body)
	}
	ext := r.json()["extension"].([]any)[0].(map[string]any)
	if ext["url"] != "https://medplum.com/fhir/StructureDefinition/rate-limit-reset" || ext["valueUnsignedInt"].(float64) <= 0 || ext["valueUnsignedInt"].(float64) > 600 {
		t.Errorf("extension: %v", ext)
	}
	if issues(r)[0]["code"] != "throttled" {
		t.Errorf("code: %v", issues(r)[0])
	}
}

// enrollAdmin đăng ký TOTP cho admin seed bằng secret cố định (kho không cấu hình khóa: lưu thô).
func enrollAdmin(t *testing.T, a *api, methods ...string) string {
	t.Helper()
	const secret = "JBSWY3DPEHPK3PXP"
	ctx := context.Background()
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	if _, err := a.c.Auth.Users.Mutate(ctx, admin.ID, func(u *fhir.User) error {
		u.MfaEnrolled, u.MfaMethod, u.MfaSecret = ptrBool(true), methods, secret
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return secret
}

// verify phản hồi như /auth/login: login thuộc client thì đặt cookie đăng nhập của client đó (và cookie không nằm trong JSON).
func TestHTTPMFAVerifySetsTheClientCookie(t *testing.T) {
	a, _ := newRegAPI(t, nil)
	secret := enrollAdmin(t, a, "totp")
	cid := a.seed.Client.ID
	body := `,"clientId":"` + cid + `","codeChallenge":"` + s256() + `","codeChallengeMethod":"S256"`
	first := a.login(loginBody(body), nil)
	if first.status != 200 || first.json()["mfaRequired"] != true || first.json()["code"] != nil || first.hdr.Get("Set-Cookie") != "" {
		t.Fatalf("%d %s %v", first.status, first.body, first.hdr)
	}
	login := first.json()["login"].(string)
	v := a.do("POST", "/auth/mfa/verify", `{"login":"`+login+`","token":"`+totpCode(t, secret, time.Now())+`"}`, nil)
	if v.status != 200 || v.json()["code"] == nil || !strings.Contains(v.hdr.Get("Set-Cookie"), "medplum-"+cid+"=") || strings.Contains(v.body, "cookie") {
		t.Fatalf("%d %s Set-Cookie=%q", v.status, v.body, v.hdr.Get("Set-Cookie"))
	}
}

// Ba route nhập bằng id login dùng chung hạn mức IP của các bước nhập thông tin đăng nhập (chặt hơn mức chung của /auth/*).
func TestHTTPMFAPublicRoutesShareTheLoginRateLimit(t *testing.T) {
	for _, path := range []string{"/auth/mfa/verify", "/auth/mfa/login-enroll", "/auth/mfa/send-email"} {
		a, _ := newRegAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 2 })
		for i := 0; i < 2; i++ {
			if r := a.login(loginBody(""), nil); r.status != 200 {
				t.Fatalf("%s lần %d: %d %s", path, i, r.status, r.body)
			}
		}
		if r := a.do("POST", path, `{"login":"`+domain.NewID()+`","token":"123456"}`, nil); r.status != 429 {
			t.Errorf("%s phải chung hạn mức đăng nhập: %d %s", path, r.status, r.body)
		}
	}
}

func TestHTTPMFADisableOneMethodByName(t *testing.T) {
	ctx := context.Background()
	a, _ := newRegAPI(t, nil)
	secret := enrollAdmin(t, a, "totp", "email")
	l := a.login(loginBody(""), nil).json()["login"].(string)
	login, _ := a.c.Auth.Logins.Get(ctx, l)
	login, _ = a.c.Auth.Logins.Mutate(ctx, login.ID, func(x *fhir.Login) error { x.MfaVerified = ptrBool(true); return nil })
	login, _ = a.c.Auth.GrantLogin(ctx, login)
	project, _ := a.c.Auth.Projects.Get(ctx, a.seed.Project.ID)
	a.c.Authenticator = fakeAuthenticator{&application.AuthState{Login: login, Membership: a.seed.Membership, Project: project}}
	e, err := a.c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	a.srv = srv
	auth := map[string]string{"Authorization": "Bearer token-hop-le"}

	if r := a.do("POST", "/auth/mfa/disable", `{"method":"sms","token":"123456"}`, auth); r.status != 400 {
		t.Fatalf("%d %s", r.status, r.body)
	} else if text, _ := firstIssue(t, r); text != "Invalid method" {
		t.Errorf("%q", text)
	}
	r := a.do("POST", "/auth/mfa/disable", `{"method":"email","token":"`+totpCode(t, secret, time.Now())+`"}`, auth)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	st := a.do("GET", "/auth/mfa/status", "", auth).json()
	if methods := st["enrolledMethods"].([]any); len(methods) != 1 || methods[0] != "totp" || st["enrolled"] != true {
		t.Errorf("chỉ gỡ email, còn totp: %v", st)
	}
}

func TestWireMFAChoosesStoreAndBoxFromConfig(t *testing.T) {
	ctx := context.Background()
	const key = "k1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	// mặc định (không Redis, không khóa): bộ nhớ và lưu thô, chỉ cho dev/test
	c := newContainer(t, nil)
	if _, err := c.Handler(nil); err != nil { // MFA được lắp khi dựng handler (cùng chỗ với khóa ký và jti)
		t.Fatal(err)
	}
	if _, ok := c.Auth.MFAState.(*mfastate.Memory); !ok {
		t.Errorf("mặc định phải dùng bộ nhớ: %T", c.Auth.MFAState)
	}
	if sealed, _ := c.Auth.MFASecrets.Seal("X"); sealed != "X" {
		t.Errorf("không khóa thì lưu thô: %q", sealed)
	}
	if c.Auth.MFACfg.LoginAttemptLimit != 5 || c.Auth.MFACfg.UserAttemptLimit != 20 || c.Auth.MFACfg.Window != 1 {
		t.Errorf("cấu hình mặc định: %+v", c.Auth.MFACfg)
	}
	// có Redis và khóa
	mr := miniredis.RunT(t)
	c2 := newContainer(t, func(s *config.Settings) {
		s.RedisURL, s.MFASecretKeys = "redis://"+mr.Addr()+"/0", key
		s.MFAWindow, s.MFALoginAttemptLimit, s.MFAUserAttemptLimit, s.MFAEmailSendLimit = 2, 3, 7, 4
		s.MFALoginExpiration, s.MFAEmailCodeExpiration, s.MFAEnrollmentExpiration = 5*time.Minute, 6*time.Minute, 7*time.Minute
	})
	if _, err := c2.Handler(nil); err != nil {
		t.Fatal(err)
	}
	st, ok := c2.Auth.MFAState.(*mfastate.Redis)
	if !ok {
		t.Fatalf("có AUTH_REDIS_URL phải dùng Redis: %T", c2.Auth.MFAState)
	}
	if r, err := st.ReserveAttempt(ctx, "U", "L", 3, 7, time.Minute); err != nil || !r.Allowed {
		t.Errorf("Redis phải dùng được: %+v %v", r, err)
	}
	if sealed, err := c2.Auth.MFASecrets.Seal("X"); err != nil || !strings.HasPrefix(sealed, "enc:v1:k1:") {
		t.Errorf("có khóa thì mã hóa: %q %v", sealed, err)
	}
	cfg := c2.Auth.MFACfg
	if cfg.Window != 2 || cfg.LoginAttemptLimit != 3 || cfg.UserAttemptLimit != 7 || cfg.EmailSendLimit != 4 || cfg.LoginExpiration != 5*time.Minute ||
		cfg.EmailCodeExpiration != 6*time.Minute || cfg.EnrollmentExpiration != 7*time.Minute {
		t.Errorf("cấu hình phải truyền xuống Service: %+v", cfg)
	}
	// khóa sai định dạng: không khởi động (không lưu bí mật thô vì cấu hình hỏng)
	for _, bad := range []string{"không có kid", "k1:ngan", "k1:" + "!!!"} {
		cb, err := New(ctx, settings(t, func(s *config.Settings) { s.MFASecretKeys = bad }))
		if err == nil {
			_, err = cb.Handler(nil)
			_ = cb.Stop(ctx)
		}
		if err == nil || !strings.Contains(err.Error(), "AUTH_MFA_SECRET_KEYS") {
			t.Errorf("khóa %q phải làm khởi động lỗi: %v", bad, err)
		}
	}
	cr, err := New(ctx, settings(t, func(s *config.Settings) { s.RedisURL = "không phải url" }))
	if err == nil {
		_, err = cr.Handler(nil)
		_ = cr.Stop(ctx)
	}
	if err == nil || !strings.Contains(err.Error(), "AUTH_REDIS_URL") {
		t.Errorf("URL Redis sai: %v", err)
	}
	// MFA đã được bật thật (không còn fail đóng): user cần đăng ký nhận enrollUri
	need, err := c.Auth.CreateUser(ctx, application.NewUser{FirstName: "A", LastName: "B", Email: "can@example.org", Password: "mat-khau-dai-123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Auth.Users.Mutate(ctx, need.ID, func(u *fhir.User) error { u.MfaRequired = ptrBool(true); return nil }); err != nil {
		t.Fatal(err)
	}
	lg, err := c.Auth.Logins.Create(ctx, &fhir.Login{ResourceType: "Login", User: &fhir.Reference{Reference: "User/" + need.ID}, AuthMethod: domain.AuthPassword, AuthTime: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := c.Auth.BuildLoginResult(ctx, lg); err != nil || !res.MfaEnrollRequired || res.EnrollURI == "" {
		t.Errorf("MFA thật phải hoạt động sau khi lắp ghép: %+v %v", res, err)
	}
}
