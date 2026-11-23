package domain

import (
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
)

func TestIsEmail(t *testing.T) {
	good := []string{"a@b.co", "an.nguyen+tag@example.org", "x_y-z@sub.example.co.uk", "A@B.COM", "o'brien@example.org", strings.Repeat("a", 64) + "@example.org"}
	bad := []string{"", "xyz", "@example.org", "an@", "an@example", "an@@example.org", "an @example.org", "an@exam ple.org", ".an@example.org", "an.@example.org",
		"a..b@example.org", "an@-example.org", "an@example-.org", "an@example.c", "an@example.123", "Name <an@example.org>", "an@example.org,b@c.org",
		strings.Repeat("a", 65) + "@example.org", "an@" + strings.Repeat("a", 64) + ".org", strings.Repeat("a", 250) + "@b.org"}
	for _, e := range good {
		if !IsEmail(e) {
			t.Errorf("%q phải hợp lệ", e)
		}
	}
	for _, e := range bad {
		if IsEmail(e) {
			t.Errorf("%q không được hợp lệ", e)
		}
	}
}

func TestIsUUID(t *testing.T) {
	for s, want := range map[string]bool{
		"6f1c2d4e-1234-4abc-9def-0123456789ab": true, "6F1C2D4E-1234-4ABC-9DEF-0123456789AB": true,
		"": false, "new": false, "6f1c2d4e12344abc9def0123456789ab": false, "6f1c2d4e-1234-4abc-9def-0123456789a": false, "g" + "f1c2d4e-1234-4abc-9def-0123456789ab": false,
	} {
		if IsUUID(s) != want {
			t.Errorf("IsUUID(%q) != %v", s, want)
		}
	}
}

func TestValidateLoginRequest(t *testing.T) {
	base := LoginRequest{Email: "a@b.co", AuthMethod: AuthPassword, Password: "x", Scope: "openid"}
	uuid := "6f1c2d4e-1234-4abc-9def-0123456789ab"
	cases := []struct {
		name string
		mut  func(*LoginRequest)
		msg  string
		expr string
	}{
		{"hợp lệ", func(*LoginRequest) {}, "", ""},
		{"project new", func(r *LoginRequest) { r.ProjectID = "new" }, "", ""},
		{"project uuid", func(r *LoginRequest) { r.ProjectID = uuid }, "", ""},
		{"project sai", func(r *LoginRequest) { r.ProjectID = "abc" }, "Invalid projectId", "projectId"},
		{"thiếu email", func(r *LoginRequest) { r.Email = "" }, "Invalid email", "email"},
		{"thiếu phương thức", func(r *LoginRequest) { r.AuthMethod = "" }, "Invalid authentication method", "authMethod"},
		{"mật khẩu rỗng", func(r *LoginRequest) { r.Password = "" }, "Invalid password", "password"},
		{"google thiếu thông tin", func(r *LoginRequest) { r.AuthMethod = AuthGoogle; r.Password = "" }, "Invalid google credentials", "googleCredentials"},
		{"google đủ", func(r *LoginRequest) { r.AuthMethod = AuthGoogle; r.Password = ""; r.HasGoogleCredentials = true }, "", ""},
		{"thiếu scope", func(r *LoginRequest) { r.Scope = "" }, "Invalid scope", "scope"},
		{"external thiếu cả hai", func(r *LoginRequest) { r.AuthMethod = AuthExternal; r.Email = "" }, "Missing email or externalId", "externalId"},
		{"exchange có externalId nhưng thiếu project", func(r *LoginRequest) { r.AuthMethod = AuthExchange; r.Email = ""; r.ExternalID = "x" }, "Project ID is required for external ID", "projectId"},
		{"external có email", func(r *LoginRequest) { r.AuthMethod = AuthExternal; r.Password = ""; r.Scope = "" }, "", ""},
	}
	for _, c := range cases {
		r := base
		c.mut(&r)
		err := ValidateLoginRequest(r)
		if c.msg == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		de, ok := err.(*Error)
		if !ok || de.Message != c.msg || de.Expression != c.expr || de.Kind != KindBadRequest {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestValidatePKCE(t *testing.T) {
	optional := &fhir.ClientApplication{PkceOptional: ptr(true)}
	required := &fhir.ClientApplication{}
	cases := []struct {
		name      string
		ch, m     string
		client    *fhir.ClientApplication
		msg, expr string
	}{
		{"không dùng PKCE", "", "", required, "", ""},
		{"plain", "abc", "plain", required, "", ""},
		{"S256", "abc", "S256", nil, "", ""},
		{"method không có challenge", "", "S256", required, "Invalid code challenge", "code_challenge"},
		{"challenge không có method", "abc", "", required, "Invalid code challenge method", "code_challenge_method"},
		{"method lạ", "abc", "S512", required, "Invalid code challenge method", "code_challenge_method"},
		{"pkceOptional bỏ qua mọi kiểm tra", "", "S512", optional, "", ""},
		{"pkceOptional bỏ qua thiếu challenge", "", "S256", optional, "", ""},
	}
	for _, c := range cases {
		err := ValidatePKCE(LoginRequest{CodeChallenge: c.ch, CodeChallengeMethod: c.m}, c.client)
		if c.msg == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		de, _ := err.(*Error)
		if de == nil || de.Message != c.msg || de.Expression != c.expr {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	list := &fhir.ClientApplication{AllowedOrigin: []string{"https://app.example.org"}}
	empty := &fhir.ClientApplication{AllowedOrigin: []string{}}
	for _, c := range []struct {
		client *fhir.ClientApplication
		origin string
		want   bool
	}{
		{nil, "https://x", true}, {&fhir.ClientApplication{}, "https://x", true}, // không khai allowedOrigin: không giới hạn
		{list, "https://app.example.org", true}, {list, "https://evil.example.org", false}, {list, "", true}, // không gửi Origin: bỏ qua
		{empty, "https://x", false}, // mảng rỗng vẫn tính là có (như JS), nên chặn mọi Origin
		{empty, "", true},
	} {
		if OriginAllowed(c.client, c.origin) != c.want {
			t.Errorf("%+v %q", c.client, c.origin)
		}
	}
}

func TestDecideLoginResult(t *testing.T) {
	mfaProj := &fhir.Project{Setting: []fhir.ProjectSetting{{Name: "mfaRequired", ValueBoolean: ptr(true)}}}
	plain := &fhir.Project{}
	pw := func(mut func(*fhir.Login)) *fhir.Login {
		l := &fhir.Login{AuthMethod: AuthPassword}
		if mut != nil {
			mut(l)
		}
		return l
	}
	withMember := func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"} }
	newProj := func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"} }
	verified := func(l *fhir.Login) { l.MfaVerified = ptr(true) }
	enrolled := &fhir.User{MfaEnrolled: ptr(true)}
	user := &fhir.User{}
	for i, c := range []struct {
		u    *fhir.User
		p    *fhir.Project
		l    *fhir.Login
		want ResultKind
	}{
		{user, mfaProj, pw(nil), ResultMFAEnroll},
		{&fhir.User{MfaRequired: ptr(true)}, plain, pw(nil), ResultMFAEnroll}, // user tự bắt buộc
		{user, mfaProj, pw(verified), ResultChooseProfile},
		{user, mfaProj, &fhir.Login{AuthMethod: AuthGoogle}, ResultChooseProfile}, // MFA chỉ áp cho mật khẩu
		{enrolled, plain, pw(nil), ResultMFARequired},
		{enrolled, mfaProj, pw(nil), ResultMFARequired},
		{enrolled, plain, pw(verified), ResultChooseProfile},
		{enrolled, plain, &fhir.Login{AuthMethod: AuthGoogle}, ResultChooseProfile},
		{user, plain, pw(newProj), ResultNewProject},
		{user, plain, pw(withMember), ResultCode},
		{user, plain, pw(func(l *fhir.Login) { newProj(l); withMember(l) }), ResultNewProject}, // new thắng membership
		{enrolled, plain, pw(withMember), ResultMFARequired},                                   // MFA thắng code
		{user, nil, pw(nil), ResultChooseProfile},
	} {
		if got := DecideLoginResult(c.u, c.p, c.l); got != c.want {
			t.Errorf("ca %d: %d, mong %d", i, got, c.want)
		}
	}
	if !CanManageUserAccount(&fhir.Login{AuthMethod: AuthPassword}) || !CanManageUserAccount(&fhir.Login{AuthMethod: AuthGoogle}) ||
		CanManageUserAccount(&fhir.Login{AuthMethod: AuthClient}) || CanManageUserAccount(&fhir.Login{AuthMethod: "external"}) {
		t.Error("CanManageUserAccount")
	}
}

func TestDefaultMenu(t *testing.T) {
	titles := func(m []fhir.UserConfigurationMenu) string {
		var out []string
		for _, g := range m {
			out = append(out, g.Title)
		}
		return strings.Join(out, ",")
	}
	targets := func(g fhir.UserConfigurationMenu) string {
		var out []string
		for _, l := range g.Link {
			out = append(out, l.Target)
		}
		return strings.Join(out, " ")
	}
	user := DefaultMenu(&fhir.Project{}, &fhir.ProjectMembership{})
	if titles(user) != "Favorites" || len(user[0].Link) != 6 || user[0].Link[0].Name != "Patient" || user[0].Link[0].Target != "/Patient" {
		t.Errorf("%+v", user)
	}
	admin := DefaultMenu(&fhir.Project{}, &fhir.ProjectMembership{Admin: ptr(true)})
	if titles(admin) != "Favorites,Admin" || !strings.Contains(targets(admin[1]), "/admin/config") {
		t.Errorf("%s %s", titles(admin), targets(admin[1]))
	}
	super := DefaultMenu(&fhir.Project{SuperAdmin: ptr(true)}, &fhir.ProjectMembership{Admin: ptr(true)})
	if titles(super) != "Favorites,Admin,Super Admin" || strings.Contains(targets(super[1]), "/admin/config") || !strings.Contains(targets(super[2]), "/admin/super/db") {
		t.Errorf("%s", titles(super))
	}
	if titles(DefaultMenu(nil, nil)) != "Favorites" {
		t.Error("nil")
	}
}
