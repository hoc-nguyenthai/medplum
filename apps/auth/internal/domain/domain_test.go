package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
)

func ptr[T any](v T) *T { return &v }

// ---- lỗi

func TestErrors(t *testing.T) {
	cases := []struct {
		err  *Error
		kind Kind
		msg  string
	}{
		{BadRequest("x"), KindBadRequest, "x"},
		{BadRequestAt("x", "password"), KindBadRequest, "x"},
		{Unauthorized("u"), KindUnauthorized, "u"},
		{Forbidden("f"), KindForbidden, "f"},
		{NotFound(), KindNotFound, "Not found"},
		{Conflict("c"), KindConflict, "c"},
		{Internal("i"), KindInternal, "i"},
	}
	for _, c := range cases {
		if c.err.Kind != c.kind || c.err.Error() != c.msg || KindOf(c.err) != c.kind || !Is(c.err, c.kind, c.msg) || !Is(c.err, c.kind, "") {
			t.Errorf("%+v", c.err)
		}
	}
	if BadRequestAt("x", "password").Expression != "password" {
		t.Error("expression")
	}
	if Is(BadRequest("a"), KindBadRequest, "b") || Is(BadRequest("a"), KindNotFound, "") || Is(errors.New("x"), KindBadRequest, "") {
		t.Error("Is phải phân biệt thông điệp, loại và lỗi ngoài")
	}
	wrapped := errors.Join(errors.New("ngoài"), NotFound())
	if KindOf(wrapped) != KindNotFound {
		t.Error("KindOf phải thấy lỗi bọc")
	}
	if KindOf(errors.New("lạ")) != KindInternal || KindOf(ErrVersionConflict) != KindInternal {
		t.Error("lỗi ngoài là Internal")
	}
	if e := InternalErr("đọc", errors.New("boom")); e.Kind != KindInternal || !strings.Contains(e.Error(), "boom") {
		t.Errorf("%v", e)
	}
}

// ---- mật khẩu

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name, pw, want string
	}{
		{"đủ 8", "12345678", ""},
		{"7 ký tự", "1234567", "Password must be at least 8 characters"},
		{"rỗng", "", "Password must be at least 8 characters"},
		{"đếm ký tự, không đếm byte", "😀😀😀😀😀😀😀😀", ""}, // 8 emoji = 8 ký tự
		{"2 emoji chưa đủ", "😀😀", "Password must be at least 8 characters"},
		{"72 byte", strings.Repeat("a", 72), ""},
		{"73 byte", strings.Repeat("a", 73), "Password must be no more than 72 characters"},
		{"72 byte nhiều byte", strings.Repeat("😀", 18), ""}, // 18*4 = 72
		{"76 byte nhiều byte", strings.Repeat("😀", 19), "Password must be no more than 72 characters"},
	}
	for _, c := range cases {
		err := ValidatePassword(c.pw)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var de *Error
		if !errors.As(err, &de) || de.Message != c.want || de.Expression != "password" || de.Kind != KindBadRequest {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if PasswordTooLongForHash(strings.Repeat("a", 72)) != nil || PasswordTooLongForHash(strings.Repeat("a", 73)) == nil {
		t.Error("PasswordTooLongForHash")
	}
}

// ---- secret

func TestSecret(t *testing.T) {
	a, b := GenerateSecret(16), GenerateSecret(16)
	if len(a) != 32 || a == b {
		t.Errorf("secret: %q %q", a, b)
	}
	if len(GenerateSecret(32)) != 64 || GenerateSecret(0) != "" {
		t.Error("độ dài hex = 2n")
	}
	x, y, z := "abc", "abc", "abd"
	e := ""
	for _, c := range []struct {
		a, b *string
		want bool
	}{{nil, nil, true}, {&x, nil, false}, {nil, &x, false}, {&x, &y, true}, {&x, &z, false}, {&x, &e, false}, {&e, &e, true}} {
		if ConstantTimeEqual(c.a, c.b) != c.want {
			t.Errorf("%v %v", c.a, c.b)
		}
	}
}

// ---- security request

func TestSecurityRequestWindowAndExpiration(t *testing.T) {
	if SecurityRequestWindow("reset") != time.Hour || SecurityRequestWindow("invite") != 7*24*time.Hour ||
		SecurityRequestWindow("verify-email") != 7*24*time.Hour {
		t.Error("cửa sổ theo loại")
	}
	if SecurityRequestWindow("") != time.Hour || SecurityRequestWindow("lạ") != time.Hour {
		t.Error("thiếu hoặc lạ coi là reset")
	}
	if MaxSecurityRequestWindow != 7*24*time.Hour {
		t.Error("cửa sổ lớn nhất")
	}
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	if got := SecurityRequestExpiration("reset", now); got != "2026-11-21T11:00:00.000Z" {
		t.Errorf("expiration: %s", got)
	}
	if got := SecurityRequestExpiration("invite", now); got != "2026-11-28T10:00:00.000Z" {
		t.Errorf("expiration invite: %s", got)
	}
}

func TestIsSecurityRequestExpired(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	meta := func(s string) *fhir.Meta { return &fhir.Meta{LastUpdated: s} }
	cases := []struct {
		name string
		r    fhir.UserSecurityRequest
		want bool
	}{
		{"còn hạn", fhir.UserSecurityRequest{ExpiresAt: "2026-11-21T10:00:01Z"}, false},
		{"đúng giờ hết hạn thì hết", fhir.UserSecurityRequest{ExpiresAt: "2026-11-21T10:00:00Z"}, true},
		{"quá hạn", fhir.UserSecurityRequest{ExpiresAt: "2026-11-21T09:59:59.999Z"}, true},
		{"expiresAt hỏng", fhir.UserSecurityRequest{ExpiresAt: "không-phải-ngày"}, true},
		{"không có mốc nào", fhir.UserSecurityRequest{}, true},
		{"lastUpdated hỏng", fhir.UserSecurityRequest{Meta: meta("xxx")}, true},
		{"dùng lastUpdated + reset 1h: còn", fhir.UserSecurityRequest{Type: "reset", Meta: meta("2026-11-21T09:30:00Z")}, false},
		{"dùng lastUpdated + reset 1h: hết", fhir.UserSecurityRequest{Type: "reset", Meta: meta("2026-11-21T08:59:00Z")}, true},
		{"thiếu type = reset", fhir.UserSecurityRequest{Meta: meta("2026-11-21T08:59:00Z")}, true},
		{"invite 7 ngày: còn", fhir.UserSecurityRequest{Type: "invite", Meta: meta("2026-11-15T10:00:01Z")}, false},
		{"invite 7 ngày: hết", fhir.UserSecurityRequest{Type: "invite", Meta: meta("2026-11-14T10:00:00Z")}, true},
		{"expiresAt thắng lastUpdated", fhir.UserSecurityRequest{ExpiresAt: "2026-11-22T00:00:00Z", Meta: meta("2020-01-01T00:00:00Z")}, false},
	}
	for _, c := range cases {
		r := c.r
		if got := IsSecurityRequestExpired(&r, now); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// ---- IP (bảng ca lấy từ oauth/utils.test.ts:27-64)

func TestIPRules(t *testing.T) {
	cases := []struct {
		rule, addr string
		matches    bool
	}{
		{"1.2.3.4", "1.2.3.4", true},
		{"1.2.3.4", "1.2.3.44", false},
		{"1.2.3", "1.2.3.44", true},
		{"1.2.3", "1.2.30.44", false},
		{"8.8.", "8.8.8.8", true},
		{"8.8.", "8.80.8.8", false},
		{"*", "1.2.3.44", true},
	}
	for _, c := range cases {
		if MatchesIPRule(c.addr, c.rule) != c.matches {
			t.Errorf("MatchesIPRule(%s,%s)", c.addr, c.rule)
		}
		blocked := CheckIPAccessRules(c.addr, []fhir.AccessPolicyIpAccessRule{{Value: c.rule, Action: "block"}})
		allowed := CheckIPAccessRules(c.addr, []fhir.AccessPolicyIpAccessRule{{Value: c.rule, Action: "allow"}, {Value: "*", Action: "block"}})
		if c.matches {
			if !Is(blocked, KindBadRequest, IPNotAllowed) || allowed != nil {
				t.Errorf("%s/%s: blocked=%v allowed=%v", c.rule, c.addr, blocked, allowed)
			}
		} else if blocked != nil || !Is(allowed, KindBadRequest, IPNotAllowed) {
			t.Errorf("%s/%s: blocked=%v allowed=%v", c.rule, c.addr, blocked, allowed)
		}
	}
	rules := []fhir.AccessPolicyIpAccessRule{{Value: "1.1.1.1", Action: "block"}}
	if CheckIPAccessRules("", rules) != nil || CheckIPAccessRules("1.1.1.1", nil) != nil {
		t.Error("không có địa chỉ hoặc không có rule thì cho qua")
	}
	if CheckIPAccessRules("1.1.1.1", []fhir.AccessPolicyIpAccessRule{{Value: "1.1.1.1", Action: "audit"}, {Value: "*", Action: "allow"}}) != nil {
		t.Error("action lạ bị bỏ qua, rule sau quyết định")
	}
	if CheckIPAccessRules("9.9.9.9", rules) != nil {
		t.Error("không khớp rule nào thì cho qua")
	}
}

// ---- project, MFA

func TestInactiveReason(t *testing.T) {
	no, yes := ptr(false), ptr(true)
	cases := []struct {
		p    *fhir.Project
		m    *fhir.ProjectMembership
		want string
	}{
		{&fhir.Project{}, &fhir.ProjectMembership{}, ""},
		{&fhir.Project{Active: yes}, &fhir.ProjectMembership{Active: yes}, ""},
		{&fhir.Project{}, &fhir.ProjectMembership{Active: no}, ProfileNotActive},
		{&fhir.Project{Active: no}, &fhir.ProjectMembership{}, ProjectNotActive},
		{&fhir.Project{Active: no}, &fhir.ProjectMembership{Active: no}, ProfileNotActive},
		{&fhir.Project{Active: no}, nil, ProjectNotActive},
		{&fhir.Project{}, nil, ""},
		{nil, nil, ""},
	}
	for i, c := range cases {
		if got := InactiveReason(c.p, c.m); got != c.want {
			t.Errorf("ca %d: %q", i, got)
		}
	}
}

func TestProjectHelpers(t *testing.T) {
	p := &fhir.Project{Features: []string{"bots", "google-auth-required"}, Setting: []fhir.ProjectSetting{{Name: "a", ValueString: "x"}}}
	if !ProjectHasFeature(p, "google-auth-required") || ProjectHasFeature(p, "nope") || ProjectHasFeature(nil, "x") {
		t.Error("ProjectHasFeature")
	}
	if s := ProjectSetting(p, "a"); s == nil || s.ValueString != "x" || ProjectSetting(p, "b") != nil || ProjectSetting(nil, "a") != nil {
		t.Error("ProjectSetting")
	}
	if IsTrue(nil) || IsTrue(ptr(false)) || !IsTrue(ptr(true)) {
		t.Error("IsTrue")
	}
}

func TestMFA(t *testing.T) {
	proj := func(settings ...fhir.ProjectSetting) *fhir.Project { return &fhir.Project{Setting: settings} }
	on := fhir.ProjectSetting{Name: "mfaRequired", ValueBoolean: ptr(true)}
	off := fhir.ProjectSetting{Name: "mfaRequired", ValueBoolean: ptr(false)}
	user := func(req bool) *fhir.User { return &fhir.User{MfaRequired: ptr(req)} }
	for i, c := range []struct {
		u    *fhir.User
		p    *fhir.Project
		want bool
	}{
		{user(false), nil, false}, {user(true), nil, true}, {user(false), proj(on), true}, {user(false), proj(off), false},
		{user(true), proj(off), true}, {&fhir.User{}, proj(), false}, {nil, proj(on), true}, {nil, nil, false},
	} {
		if IsMFARequired(c.u, c.p) != c.want {
			t.Errorf("IsMFARequired ca %d", i)
		}
	}
	allowed := func(v string) []string {
		return AllowedMFAMethods(proj(fhir.ProjectSetting{Name: "allowedMfaMethods", ValueString: v}))
	}
	for v, want := range map[string]string{"": "totp", "totp": "totp", "email": "email", "totp,email": "totp,email", " email , totp ": "email,totp", "sms": "totp", "sms,email": "email"} {
		if got := strings.Join(allowed(v), ","); got != want {
			t.Errorf("allowed(%q)=%s, mong %s", v, got, want)
		}
	}
	if strings.Join(AllowedMFAMethods(nil), ",") != "totp" || strings.Join(AllowedMFAMethods(proj()), ",") != "totp" {
		t.Error("mặc định totp")
	}
	if got := EnrolledMFAMethods(&fhir.User{MfaMethod: []string{"email"}, MfaEnrolled: ptr(true)}); strings.Join(got, ",") != "email" {
		t.Errorf("%v", got)
	}
	if got := EnrolledMFAMethods(&fhir.User{MfaEnrolled: ptr(true)}); strings.Join(got, ",") != "totp" {
		t.Errorf("tương thích ngược: %v", got)
	}
	if EnrolledMFAMethods(&fhir.User{}) != nil || EnrolledMFAMethods(nil) != nil {
		t.Error("chưa đăng ký thì rỗng")
	}
}

// ---- client

func TestStandardClients(t *testing.T) {
	cs := StandardClients(true, []fhir.ClientApplication{{ID: "extra"}})
	if len(cs) != 2 || cs[0].ID != CLIClientID || cs[0].PkceOptional == nil || !*cs[0].PkceOptional || cs[0].RedirectUris[0] != "http://localhost:9615" || cs[1].ID != "extra" {
		t.Errorf("%+v", cs)
	}
	if cs := StandardClients(false, nil); len(cs) != 0 {
		t.Errorf("tắt CLI: %+v", cs)
	}
}

func TestRedirectURIs(t *testing.T) {
	c := &fhir.ClientApplication{RedirectUri: "https://a/cb", RedirectUris: []string{"https://b/cb", "https://c/cb"}}
	if got := strings.Join(RedirectURIs(c), " "); got != "https://a/cb https://b/cb https://c/cb" {
		t.Errorf("%s", got)
	}
	for req, want := range map[string]string{"https://a/cb": "https://a/cb", "https://c/cb": "https://c/cb", "https://a/cb/x": "", "https://a": "", "https://A/cb": "", "": ""} {
		if got := MatchRedirectURI(c, req); got != want {
			t.Errorf("Match(%q)=%q, mong %q", req, got, want)
		}
	}
	if MatchRedirectURI(&fhir.ClientApplication{}, "x") != "" {
		t.Error("client không có URI")
	}
}

func TestIncludeRefreshToken(t *testing.T) {
	rt := &fhir.ClientApplication{GrantType: []string{"authorization_code", "refresh_token"}}
	for i, c := range []struct {
		req  LoginRequest
		c    *fhir.ClientApplication
		want bool
	}{
		{LoginRequest{Remember: true}, nil, true},
		{LoginRequest{Scope: "openid offline"}, nil, true},
		{LoginRequest{Scope: "openid offline_access"}, nil, true},
		{LoginRequest{Scope: "openid"}, rt, true},
		{LoginRequest{Scope: "openid"}, &fhir.ClientApplication{GrantType: []string{"authorization_code"}}, false},
		{LoginRequest{Scope: "openid"}, nil, false},
		{LoginRequest{}, nil, false},
		{LoginRequest{Scope: "offline_accessx"}, nil, false},
	} {
		if IncludeRefreshToken(c.req, c.c) != c.want {
			t.Errorf("ca %d", i)
		}
	}
}

// ---- scope SMART

func TestParseSmartScope(t *testing.T) {
	type want struct {
		perm, rt, scope string
		params          [][2]string
	}
	ok := map[string]want{
		"patient/Observation.rs":            {"patient", "Observation", "rs", nil},
		"user/*.*":                          {"user", "*", "cruds", nil},
		"system/Patient.read":               {"system", "Patient", "rs", nil},
		"system/Patient.write":              {"system", "Patient", "cud", nil},
		"user/Patient.cruds":                {"user", "Patient", "cruds", nil},
		"user/Patient.":                     {"user", "Patient", "", nil},
		"user/Observation.rs?category=lab":  {"user", "Observation", "rs", [][2]string{{"category", "lab"}}},
		"user/Observation.rs?a=1&b=x%20y&c": {"user", "Observation", "rs", [][2]string{{"a", "1"}, {"b", "x y"}, {"c", ""}}},
		"user/Observation.rs?a=1&&b=2":      {"user", "Observation", "rs", [][2]string{{"a", "1"}, {"b", "2"}}},
	}
	for term, w := range ok {
		s, got := ParseSmartScope(term)
		if !got || s.PermissionType != w.perm || s.ResourceType != w.rt || s.Scope != w.scope || len(s.Params) != len(w.params) {
			t.Errorf("%q: %+v %v", term, s, got)
			continue
		}
		for i := range w.params {
			if s.Params[i] != w.params[i] {
				t.Errorf("%q params: %v", term, s.Params)
			}
		}
	}
	for _, term := range []string{"openid", "profile", "offline_access", "launch/patient", "fhirUser", "", "other/Patient.rs", "user/Patient.xyz", "user/.rs", "user/Patient.rs.extra", "User/Patient.rs", "user/Pat-ient.rs"} {
		if _, got := ParseSmartScope(term); got {
			t.Errorf("%q không phải SMART", term)
		}
	}
	// percent-encoding hỏng: giữ nguyên văn
	if s, _ := ParseSmartScope("user/X.rs?a=%zz"); len(s.Params) != 1 || s.Params[0] != [2]string{"a", "%zz"} {
		t.Errorf("%v", s.Params)
	}
	if got := ParseSmartScopes("openid user/Patient.rs  patient/*.read offline_access"); len(got) != 2 {
		t.Errorf("%v", got)
	}
	if len(ParseSmartScopes("")) != 0 || HasSmartResourceScopes("openid profile") || !HasSmartResourceScopes("openid patient/Observation.rs") {
		t.Error("HasSmartResourceScopes")
	}
}

// Bảng ca lấy từ oauth/utils.test.ts:835-850.
func TestIsSmartScopeSubset(t *testing.T) {
	cases := []struct {
		existing, requested string
		patientCtx, want    bool
	}{
		{"system/*.rs", "user/Patient.rs", false, true},
		{"system/*.rs", "patient/*.rs", true, true},
		{"user/*.rs", "patient/Observation.r", true, true},
		{"system/*.rs", "patient/*.rs", false, false},
		{"user/*.rs", "patient/*.rs", false, false},
		{"user/*.rs", "system/Patient.rs", true, false},
		{"patient/*.rs", "user/Patient.rs", true, false},
		{"user/Patient.rs", "patient/Patient.cruds", true, false},
		{"user/Patient.rs", "user/Patient.", false, false},
		{"user/*.rs", "user/*.", false, false},
		// thêm: không giới hạn, thiếu SMART trong yêu cầu, tham số
		{"openid", "user/*.cruds", false, true},
		{"", "anything", false, true},
		{"user/Patient.rs", "openid", false, false},
		{"user/Observation.rs?category=lab", "user/Observation.rs?category=lab", false, true},
		{"user/Observation.rs?category=lab", "user/Observation.rs", false, false},
		{"user/Observation.rs?category=lab", "user/Observation.rs?category=vitals", false, false},
		{"user/Observation.rs", "user/Observation.rs?category=lab", false, true},
		{"user/*.cruds user/Patient.r", "user/Patient.rs user/Observation.u", false, true},
		{"user/Patient.rs", "user/Observation.r", false, false},
	}
	for _, c := range cases {
		if got := IsSmartScopeSubset(c.existing, c.requested, c.patientCtx); got != c.want {
			t.Errorf("%q bao %q (ctx=%v): %v, mong %v", c.existing, c.requested, c.patientCtx, got, c.want)
		}
	}
}

func TestIsNonSmartScopeSubset(t *testing.T) {
	for _, c := range []struct {
		existing, requested string
		want                bool
	}{
		{"openid profile", "openid", true},
		{"openid profile", "profile", true},
		{"openid", "profile", false},
		{"", "openid", true},
		{"", "", true},
		{"openid", "openid  openid", true},
		{"openid profile email", "openid email", true},
		{"openid", "offline_access", false},
		{"openid", "user/Patient.rs", true}, // term SMART không tính ở đây
		{"openid profile", "profile fhirUser", false},
	} {
		if got := IsNonSmartScopeSubset(c.existing, c.requested); got != c.want {
			t.Errorf("%q / %q: %v", c.existing, c.requested, got)
		}
	}
}
