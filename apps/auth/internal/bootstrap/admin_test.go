package bootstrap

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

func devSettings(t *testing.T) config.Settings {
	return settings(t, func(s *config.Settings) {
		s.PwnedEnabled = false
		s.DefaultProjectFeatures = "bots, google-auth-required"
	})
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func run(t *testing.T, s config.Settings, getenv func(string) string, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := RunAdmin(context.Background(), s, args, getenv, strings.NewReader(stdin), &out)
	return out.String(), err
}

func field(out, key string) string {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, key+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func count(t *testing.T, s config.Settings, rt string) int {
	t.Helper()
	c, err := New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	res, err := c.Store.Search(context.Background(), &fhirsearch.Request{ResourceType: rt, Count: 1000, Total: "none"})
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Matches)
}

func TestIsAdminCommand(t *testing.T) {
	for _, a := range [][]string{{"seed", "project"}, {"client", "create"}, {"user", "set-password"}, {"purge-logins"}} {
		if !IsAdminCommand(a) {
			t.Errorf("%v", a)
		}
	}
	for _, a := range [][]string{nil, {"migrate", "up"}, {"healthcheck"}, {"help"}} {
		if IsAdminCommand(a) {
			t.Errorf("%v không phải lệnh quản trị", a)
		}
	}
	if _, err := run(t, devSettings(t), env(nil), "", "seed"); err == nil || !strings.Contains(err.Error(), "lệnh quản trị") {
		t.Errorf("thiếu lệnh con phải in cách dùng: %v", err)
	}
}

func TestSeedProjectCommand(t *testing.T) {
	s := devSettings(t)
	out, err := run(t, s, env(map[string]string{"AUTH_SEED_ADMIN_PASSWORD": "mat-khau-tot-123"}), "",
		"seed", "project", "-name", "Bệnh viện A", "-admin-email", "Admin@Example.org", "-admin-first", "Hoc", "-admin-last", "Nguyen")
	if err != nil {
		t.Fatal(err)
	}
	if field(out, "projectName") != "Bệnh viện A" || field(out, "adminEmail") != "admin@example.org" || field(out, "adminReused") != "false" ||
		len(field(out, "clientSecret")) != 64 || field(out, "projectId") == "" || field(out, "clientId") == "" {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(out, "chỉ hiện một lần") {
		t.Error("phải nhắc secret chỉ hiện một lần")
	}
	if strings.Contains(out, "mat-khau-tot-123") {
		t.Error("không được in mật khẩu")
	}
	// features cấu hình được áp vào project
	c, _ := New(context.Background(), s)
	defer c.Stop(context.Background())
	p, err := c.Auth.Projects.Get(context.Background(), field(out, "projectId"))
	if err != nil || len(p.Features) != 2 || p.Features[0] != "bots" || p.Features[1] != "google-auth-required" || !domain.IsTrue(p.StrictMode) {
		t.Errorf("%+v %v", p, err)
	}

	// chạy lần hai: dùng lại admin, không cần mật khẩu
	out2, err := run(t, s, env(nil), "", "seed", "project", "-name", "Bệnh viện B", "-admin-email", "admin@example.org")
	if err != nil || field(out2, "adminReused") != "true" || field(out2, "projectId") == field(out, "projectId") {
		t.Fatalf("lần hai: %v\n%s", err, out2)
	}
	if count(t, s, "User") != 1 || count(t, s, "Project") != 2 {
		t.Error("một admin, hai project")
	}
}

func TestSeedProjectPasswordFromStdin(t *testing.T) {
	s := devSettings(t)
	out, err := run(t, s, env(nil), "mat-khau-tu-stdin-1\r\nphan-sau-bi-bo-qua\n", "seed", "project", "-name", "P", "-admin-email", "a@example.org", "-admin-first", "A", "-admin-last", "B")
	if err != nil || field(out, "projectId") == "" {
		t.Fatalf("%v\n%s", err, out)
	}
	c, _ := New(context.Background(), s)
	defer c.Stop(context.Background())
	u, _ := c.Auth.FindUserByEmail(context.Background(), "a@example.org", "")
	if u == nil || !c.Auth.VerifyPassword(u, "mat-khau-tu-stdin-1") {
		t.Error("mật khẩu đọc từ dòng đầu của stdin, bỏ \\r\\n")
	}
	// stdin không có newline cuối vẫn đọc được
	s2 := devSettings(t)
	if _, err := run(t, s2, env(nil), "mat-khau-khong-newline", "seed", "project", "-name", "P", "-admin-email", "b@example.org"); err != nil {
		t.Errorf("%v", err)
	}
}

func TestSeedProjectErrorsLeaveNothingBehind(t *testing.T) {
	s := devSettings(t)
	for name, tc := range map[string]struct {
		args  []string
		stdin string
		want  string
	}{
		"thiếu -name":         {[]string{"seed", "project", "-admin-email", "a@example.org"}, "mat-khau-tot-123\n", "thiếu -name"},
		"thiếu -admin-email":  {[]string{"seed", "project", "-name", "P"}, "mat-khau-tot-123\n", "thiếu -name hoặc -admin-email"},
		"không có mật khẩu":   {[]string{"seed", "project", "-name", "P", "-admin-email", "a@example.org"}, "", "thiếu mật khẩu"},
		"mật khẩu ngắn":       {[]string{"seed", "project", "-name", "P", "-admin-email", "a@example.org"}, "ngan\n", "at least 8"},
		"cờ lạ":               {[]string{"seed", "project", "-bogus"}, "", "bogus"},
		"tên project quá dài": {[]string{"seed", "project", "-name", strings.Repeat("a", 80), "-admin-email", "a@example.org"}, "mat-khau-tot-123\n", "between 1 and 72"},
	} {
		if _, err := run(t, s, env(nil), tc.stdin, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if count(t, s, "User")+count(t, s, "Project")+count(t, s, "AccessPolicy") != 0 {
		t.Error("lỗi thì không được tạo gì")
	}
}

func TestPwnedPasswordIsRejectedWithFieldName(t *testing.T) {
	s := devSettings(t)
	// bật kiểm tra rò rỉ trỏ vào một dịch vụ lỗi: fail đóng, không tạo gì
	s.PwnedEnabled, s.PwnedURL, s.PwnedTimeout = true, "http://127.0.0.1:1/", 200*time.Millisecond
	_, err := run(t, s, env(map[string]string{"AUTH_SEED_ADMIN_PASSWORD": "mat-khau-tot-123"}), "", "seed", "project", "-name", "P", "-admin-email", "a@example.org")
	if err == nil {
		t.Fatal("dịch vụ kiểm tra rò rỉ lỗi phải khiến lệnh thất bại")
	}
	if count(t, s, "User") != 0 {
		t.Error("không được tạo user")
	}
}

func seed(t *testing.T, s config.Settings) (projectID string) {
	t.Helper()
	out, err := run(t, s, env(map[string]string{"AUTH_SEED_ADMIN_PASSWORD": "mat-khau-tot-123"}), "",
		"seed", "project", "-name", "P", "-admin-email", "a@example.org", "-admin-first", "A", "-admin-last", "B")
	if err != nil {
		t.Fatal(err)
	}
	return field(out, "projectId")
}

func TestClientCreateCommand(t *testing.T) {
	s := devSettings(t)
	pid := seed(t, s)
	out, err := run(t, s, env(nil), "", "client", "create", "-project", pid, "-name", "SPA", "-description", "web",
		"-redirect-uri", "https://a/cb", "-redirect-uri", "https://b/cb", "-grant-type", "refresh_token", "-pkce-optional")
	if err != nil || len(field(out, "clientSecret")) != 64 || field(out, "clientId") == "" || !strings.Contains(out, "chỉ hiện một lần") {
		t.Fatalf("%v\n%s", err, out)
	}
	c, _ := New(context.Background(), s)
	defer c.Stop(context.Background())
	got, err := c.Auth.Clients.Get(context.Background(), field(out, "clientId"))
	if err != nil || got.Name != "SPA" || len(got.RedirectUris) != 2 || got.GrantType[0] != "refresh_token" || !domain.IsTrue(got.PkceOptional) || got.Meta.Project != pid {
		t.Errorf("%+v %v", got, err)
	}
	// policy: thuộc project thì được
	proj, _ := c.Auth.Projects.Get(context.Background(), pid)
	pol := proj.DefaultAccessPolicies[3].AccessPolicy.Reference
	if _, err := run(t, s, env(nil), "", "client", "create", "-project", pid, "-name", "X", "-access-policy", pol); err != nil {
		t.Errorf("%v", err)
	}
	for name, args := range map[string][]string{
		"thiếu -project":   {"client", "create", "-name", "X"},
		"thiếu -name":      {"client", "create", "-project", pid},
		"project không có": {"client", "create", "-project", "khong-co", "-name", "X"},
		"policy sai dạng":  {"client", "create", "-project", pid, "-name", "X", "-access-policy", pol[len("AccessPolicy/"):]},
		"policy không có":  {"client", "create", "-project", pid, "-name", "X", "-access-policy", "AccessPolicy/khong-co"},
	} {
		if _, err := run(t, s, env(nil), "", args...); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
}

func TestUserSetPasswordCommand(t *testing.T) {
	s := devSettings(t)
	seed(t, s)
	out, err := run(t, s, env(map[string]string{"AUTH_NEW_PASSWORD": "mat-khau-moi-xyz"}), "", "user", "set-password", "-email", "A@example.org")
	if err != nil || !strings.Contains(out, "thu hồi") {
		t.Fatalf("%v\n%s", err, out)
	}
	c, _ := New(context.Background(), s)
	defer c.Stop(context.Background())
	u, _ := c.Auth.FindUserByEmail(context.Background(), "a@example.org", "")
	if !c.Auth.VerifyPassword(u, "mat-khau-moi-xyz") || c.Auth.VerifyPassword(u, "mat-khau-tot-123") {
		t.Error("mật khẩu phải đổi")
	}
	if _, err := run(t, s, env(nil), "tu-stdin-mat-khau-1\n", "user", "set-password", "-email", "a@example.org"); err != nil {
		t.Errorf("stdin: %v", err)
	}
	for name, tc := range map[string]struct {
		args  []string
		stdin string
	}{
		"thiếu -email":        {[]string{"user", "set-password"}, "mat-khau-moi-xyz\n"},
		"user không có":       {[]string{"user", "set-password", "-email", "khong-co@example.org"}, "mat-khau-moi-xyz\n"},
		"không có mật khẩu":   {[]string{"user", "set-password", "-email", "a@example.org"}, ""},
		"mật khẩu ngắn":       {[]string{"user", "set-password", "-email", "a@example.org"}, "ngan\n"},
		"project của user lạ": {[]string{"user", "set-password", "-email", "a@example.org", "-project", "khac"}, "mat-khau-moi-xyz\n"},
	} {
		if _, err := run(t, s, env(nil), tc.stdin, tc.args...); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
}

func TestPurgeLoginsCommand(t *testing.T) {
	s := devSettings(t)
	c, _ := New(context.Background(), s)
	for range 3 {
		if _, err := c.Store.CreateResource(context.Background(), &fhir.Login{ResourceType: "Login", AuthMethod: "password", AuthTime: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = c.Stop(context.Background())
	future := time.Now().Add(time.Hour)
	_ = future

	// không có -dry-run lẫn -yes: từ chối
	if _, err := run(t, s, env(nil), "", "purge-logins", "-before", "2000-01-01"); err == nil || !strings.Contains(err.Error(), "-yes") {
		t.Errorf("phải đòi xác nhận: %v", err)
	}
	if _, err := run(t, s, env(nil), "", "purge-logins", "-dry-run"); err == nil {
		t.Error("thiếu -before phải lỗi")
	}
	if _, err := run(t, s, env(nil), "", "purge-logins", "-before", "khong-phai-ngay", "-dry-run"); err == nil {
		t.Error("ngày sai phải lỗi")
	}
	if _, err := run(t, s, env(nil), "", "purge-logins", "-before", time.Now().Add(time.Hour).Format(time.RFC3339), "-dry-run"); err == nil {
		t.Error("mốc ở tương lai phải lỗi")
	}
	out, err := run(t, s, env(nil), "", "purge-logins", "-before", "2000-01-01", "-dry-run")
	if err != nil || !strings.Contains(out, "0 login") || !strings.Contains(out, "dry-run") {
		t.Errorf("%v %s", err, out)
	}
	if count(t, s, "Login") != 3 {
		t.Fatal("dry-run không được xóa")
	}
	// đợi để login cũ hơn mốc rồi xóa thật
	// RFC3339 chỉ chính xác tới giây (cắt xuống), nên mốc phải lùi ít nhất một giây sau khi login được tạo: đợi 2 giây rồi lùi 1 giây.
	time.Sleep(2 * time.Second)
	cut := time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	out, err = run(t, s, env(nil), "", "purge-logins", "-before", cut, "-yes")
	if err != nil || !strings.Contains(out, "đã xóa vĩnh viễn 3 login") {
		t.Fatalf("%v %s", err, out)
	}
	if count(t, s, "Login") != 0 {
		t.Error("login phải bị xóa vĩnh viễn")
	}
}

func TestMainHelpers(t *testing.T) {
	if got := splitList(" a, b ,,c "); len(got) != 3 || got[2] != "c" {
		t.Errorf("%v", got)
	}
	if splitList("") != nil {
		t.Error("rỗng thì nil")
	}
	var l stringList
	_ = l.Set("x")
	_ = l.Set("y")
	if l.String() != "x,y" {
		t.Error(l.String())
	}
	if _, err := parseTime("2026-11-21T10:00:00Z"); err != nil {
		t.Error(err)
	}
	if _, err := parseTime("2026-11-21"); err != nil {
		t.Error(err)
	}
}

func TestCLIDomainAdd(t *testing.T) {
	ctx := context.Background()
	s := settings(t, func(s *config.Settings) { s.PwnedEnabled = false })
	env := func(k string) string {
		if k == "AUTH_DOMAIN_IDP_CLIENT_SECRET" {
			return "bi-mat-idp"
		}
		return ""
	}
	args := []string{"domain", "add", "-domain", "Corp.Example.ORG", "-authorize-url", "https://idp.example.org/authorize", "-token-url", "https://idp.example.org/token",
		"-client-id", "cid", "-jwks-url", "https://idp.example.org/jwks", "-issuer", "https://idp.example.org", "-use-pkce", "-allowed-redirect-url", "https://app.example.org/signin"}
	var out strings.Builder
	if err := RunAdmin(ctx, s, args, env, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "domain: corp.example.org") || !strings.Contains(out.String(), "domainConfigurationId: ") || strings.Contains(out.String(), "bi-mat-idp") {
		t.Errorf("đầu ra (không được in secret): %q", out.String())
	}
	// trùng tên miền
	if err := RunAdmin(ctx, s, args, env, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "Domain already configured") {
		t.Errorf("trùng: %v", err)
	}
	// secret từ stdin; không có secret thì lỗi (không nhận qua tham số)
	args2 := append([]string(nil), args...)
	args2[3] = "stdin.example.org"
	if err := RunAdmin(ctx, s, args2, func(string) string { return "" }, strings.NewReader("tu-stdin\n"), &out); err != nil {
		t.Errorf("secret từ stdin: %v", err)
	}
	args3 := append([]string(nil), args...)
	args3[3] = "none.example.org"
	if err := RunAdmin(ctx, s, args3, func(string) string { return "" }, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "AUTH_DOMAIN_IDP_CLIENT_SECRET") {
		t.Errorf("thiếu secret: %v", err)
	}
	// URL nội bộ bị chặn ngay khi lưu; thiếu tham số bắt buộc
	bad := append([]string(nil), args...)
	bad[3], bad[7] = "bad.example.org", "https://127.0.0.1/token"
	if err := RunAdmin(ctx, s, bad, env, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "tokenUrl") {
		t.Errorf("URL nội bộ: %v", err)
	}
	if err := RunAdmin(ctx, s, []string{"domain", "add", "-domain", "x.example.org"}, env, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "thiếu") {
		t.Errorf("thiếu tham số: %v", err)
	}
	if !IsAdminCommand([]string{"domain", "add"}) {
		t.Error("domain là lệnh quản trị")
	}
	// kết quả dùng được: /auth/method nhận ra tên miền
	c, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(ctx)
	if got, _ := c.Auth.DomainConfigs.FindByDomain(ctx, "corp.example.org"); got == nil || got.IdentityProvider.JwksUrl != "https://idp.example.org/jwks" || !domain.IsTrue(got.IdentityProvider.UsePkce) || len(got.AllowedPostLoginRedirectUrls) != 1 {
		t.Errorf("%+v", got)
	}
}

func TestCLIProjectFeature(t *testing.T) {
	ctx := context.Background()
	s := settings(t, func(s *config.Settings) { s.PwnedEnabled = false })
	var out strings.Builder
	seed := []string{"seed", "project", "-name", "Bệnh viện A", "-admin-email", "a@example.org", "-admin-first", "An", "-admin-last", "Nguyen"}
	if err := RunAdmin(ctx, s, seed, func(k string) string {
		if k == "AUTH_SEED_ADMIN_PASSWORD" {
			return "mat-khau-admin-123"
		}
		return ""
	}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^projectId: (\S+)`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("không đọc được projectId: %s", out.String())
	}
	run := func(args ...string) (string, error) {
		var o strings.Builder
		err := RunAdmin(ctx, s, append([]string{"project", "feature"}, args...), func(string) string { return "" }, strings.NewReader(""), &o)
		return o.String(), err
	}
	if o, err := run("-project", m[1], "-feature", "scim"); err != nil || !strings.Contains(o, "state: enabled") {
		t.Fatalf("bật: %q %v", o, err)
	}
	// bật lần hai không đổi; tắt; tắt tính năng không có
	for _, args := range [][]string{{"-project", m[1], "-feature", "scim"}, {"-project", m[1], "-feature", "scim", "-disable"}, {"-project", m[1], "-feature", "khong-co", "-disable"}} {
		if _, err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for name, args := range map[string][]string{"thiếu project": {"-feature", "scim"}, "thiếu feature": {"-project", m[1]}, "tên sai": {"-project", m[1], "-feature", "Co Dau!"}, "project lạ": {"-project", "khong-co", "-feature", "scim"}} {
		if _, err := run(args...); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
}
