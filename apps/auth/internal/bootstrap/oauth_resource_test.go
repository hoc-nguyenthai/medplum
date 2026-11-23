package bootstrap

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func (a *codeAPI) get(path, tok string) resp  { return a.do("GET", path, "", bearer(tok)) }
func (a *codeAPI) post(path, tok string) resp { return a.do("POST", path, "", bearer(tok)) }

func (a *codeAPI) introspect(caller, subject string) resp {
	a.t.Helper()
	req := a.form("/oauth2/introspect", url.Values{"token": {subject}}, bearer(caller))
	return req
}

func (a *codeAPI) signIn(scope string) map[string]any {
	a.t.Helper()
	code, _, _ := a.loginCode(loginOpts{scope: scope, challenge: s256(), method: "S256"})
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
	if r.status != 200 {
		a.t.Fatalf("%d %s", r.status, r.body)
	}
	return r.json()
}

func TestUserInfoFollowsLoginScopeByWord(t *testing.T) {
	a := newCodeAPI(t, nil)
	check := func(scope string, want map[string]any, absent ...string) {
		t.Helper()
		tk := a.signIn(scope)
		r := a.get("/oauth2/userinfo", tk["access_token"].(string))
		if r.status != 200 {
			t.Fatalf("%s: %d %s", scope, r.status, r.body)
		}
		got := r.json()
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %v, muốn %v (%v)", scope, k, got[k], v, got)
			}
		}
		for _, k := range absent {
			if _, has := got[k]; has {
				t.Errorf("%s: không được có %s: %v", scope, k, got)
			}
		}
	}
	// chỉ openid: chỉ có sub
	only := a.signIn("openid")
	r := a.get("/oauth2/userinfo", only["access_token"].(string))
	if m := r.json(); len(m) != 1 || m["sub"] == nil {
		t.Errorf("chỉ openid: %v", m)
	}
	check("openid profile", map[string]any{"locale": "en-US", "given_name": "An", "family_name": "Nguyen", "name": "An Nguyen",
		"profile": "Practitioner/" + a.seed.Profile.ID}, "email", "email_verified", "phone_number", "address")
	check("openid email", map[string]any{"email": "admin@example.org", "email_verified": false}, "profile", "name", "locale")
	check("openid profile email", map[string]any{"email": "admin@example.org", "given_name": "An"})
	// khớp theo TỪ: "emailx" không phải "email" (Medplum dùng chuỗi con ở access token)
	check("openid emailx", nil, "email", "email_verified")
	// POST cũng được
	if r := a.post("/oauth2/userinfo", only["access_token"].(string)); r.status != 200 {
		t.Errorf("POST: %d", r.status)
	}
}

func TestUserInfoOfClientLoginAndAuthentication(t *testing.T) {
	a := newCodeAPI(t, nil)
	cc := a.clientToken(url.Values{"scope": {"openid profile email"}}).json()
	r := a.get("/oauth2/userinfo", cc["access_token"].(string))
	m := r.json()
	if r.status != 200 || m["sub"] != a.seed.Client.ID || m["email"] != nil || m["given_name"] != nil || m["profile"] == nil {
		t.Errorf("client login: %d %s", r.status, r.body)
	}
	if r := a.do("GET", "/oauth2/userinfo", "", nil); r.status != 401 || !strings.HasPrefix(r.hdr.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("không token: %d %v", r.status, r.hdr)
	}
	tk := a.signIn("openid")
	for name, bad := range map[string]string{"id token": tk["id_token"].(string), "refresh token": a.tokens()["refresh_token"].(string), "rác": "abc"} {
		if r := a.get("/oauth2/userinfo", bad); r.status != 401 {
			t.Errorf("%s: %d", name, r.status)
		}
	}
}

func TestLogoutRevokesLoginAndClearsCookie(t *testing.T) {
	a := newCodeAPI(t, nil)
	if r := a.do("POST", "/oauth2/logout", "", nil); r.status != 401 {
		t.Fatalf("không token: %d", r.status)
	}
	tk := a.signIn("openid offline_access")
	access, refresh := tk["access_token"].(string), tk["refresh_token"].(string)
	if a.get("/oauth2/userinfo", access).status != 200 {
		t.Fatal("userinfo trước logout")
	}
	r := a.post("/oauth2/logout", access)
	if r.status != 200 || r.json()["resourceType"] != "OperationOutcome" || r.json()["id"] != "ok" {
		t.Fatalf("logout: %d %s", r.status, r.body)
	}
	cookie := ""
	for _, c := range r.hdr.Values("Set-Cookie") {
		if strings.HasPrefix(c, "medplum-"+a.seed.Client.ID+"=") {
			cookie = c
		}
	}
	if cookie == "" || !strings.Contains(cookie, "Max-Age=0") || !strings.Contains(cookie, "HttpOnly") {
		t.Errorf("phải xóa cookie đăng nhập: %q", cookie)
	}
	// sau đó: access token, logout lần hai, và refresh token của login đó đều bị từ chối
	if a.get("/oauth2/userinfo", access).status != 401 || a.post("/oauth2/logout", access).status != 401 {
		t.Error("access token phải bị từ chối sau logout")
	}
	wantOAuthError(t, a.refresh(refresh, nil), "invalid_grant", "Token revoked")
	// GET cũng được
	tk2 := a.signIn("openid")
	if r := a.get("/oauth2/logout", tk2["access_token"].(string)); r.status != 200 {
		t.Errorf("GET logout: %d", r.status)
	}
	// logout của login client_credentials
	cc := a.clientToken(nil).json()
	if r := a.post("/oauth2/logout", cc["access_token"].(string)); r.status != 200 {
		t.Errorf("logout client login: %d", r.status)
	}
}

func TestIntrospectActiveAndInactive(t *testing.T) {
	a := newCodeAPI(t, nil)
	caller := a.signIn("openid offline_access")
	callerAccess := caller["access_token"].(string)
	other := a.signIn("openid offline_access") // cùng người dùng, cùng client, login khác

	// yêu cầu xác thực và tham số
	if r := a.form("/oauth2/introspect", url.Values{"token": {"x"}}, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.form("/oauth2/introspect", url.Values{}, bearer(callerAccess)); r.status != 400 || r.body != "Token to introspect must be provided" {
		t.Errorf("thiếu token: %d %q", r.status, r.body)
	}
	// access token: hoạt động, đúng các trường của Medplum
	r := a.introspect(callerAccess, other["access_token"].(string))
	m := r.json()
	if r.status != 200 || m["active"] != true || m["iss"] != a.c.Settings.IssuerURL() || m["client_id"] != a.seed.Client.ID || m["sub"] == nil ||
		m["iat"] == nil || m["exp"] == nil || !strings.Contains(m["scope"].(string), "openid") || m["aud"] != nil || m["jti"] != nil || m["token_type"] != nil {
		t.Fatalf("access token: %s", r.body)
	}
	// body JSON cũng được
	if r := a.do("POST", "/oauth2/introspect", `{"token":"`+other["access_token"].(string)+`"}`, bearer(callerAccess)); r.json()["active"] != true {
		t.Errorf("JSON: %s", r.body)
	}
	// refresh token hiện hành: hoạt động; id token, rác, token không dùng được: không hoạt động
	if a.introspect(callerAccess, other["refresh_token"].(string)).json()["active"] != true {
		t.Error("refresh token hiện hành phải hoạt động")
	}
	for name, bad := range map[string]string{"id token": other["id_token"].(string), "rác": "abc.def.ghi", "chữ ký sai": other["access_token"].(string)[:len(other["access_token"].(string))-3] + "AAA"} {
		if m := a.introspect(callerAccess, bad).json(); len(m) != 1 || m["active"] != false {
			t.Errorf("%s: %v", name, m)
		}
	}
	// refresh token đã xoay: token cũ không hoạt động, token mới hoạt động
	old := other["refresh_token"].(string)
	rotated := a.refresh(old, nil).json()["refresh_token"].(string)
	if a.introspect(callerAccess, old).json()["active"] != false || a.introspect(callerAccess, rotated).json()["active"] != true {
		t.Error("sau xoay: token cũ inactive, token mới active")
	}
	// login bị thu hồi: inactive
	if err := a.c.Auth.RevokeLogin(context.Background(), a.loginOfToken(rotated)); err != nil {
		t.Fatal(err)
	}
	if a.introspect(callerAccess, rotated).json()["active"] != false || a.introspect(callerAccess, other["access_token"].(string)).json()["active"] != false {
		t.Error("login thu hồi: mọi token của nó phải inactive")
	}
}

func TestIntrospectOnlyTokensOfSameProjectAndClient(t *testing.T) {
	a := newCodeAPI(t, nil)
	ctx := context.Background()
	caller := a.signIn("openid")["access_token"].(string)

	// cùng project, client khác
	second, err := a.c.Auth.CreateClient(ctx, a.seed.Project.ID, application.NewClient{Name: "Client hai"})
	if err != nil {
		t.Fatal(err)
	}
	t2 := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}}, basic(second.ID, second.Secret)).json()["access_token"].(string)
	if m := a.introspect(caller, t2).json(); m["active"] != false {
		t.Errorf("client khác: %v", m)
	}
	// project khác
	other, err := a.c.Auth.SeedProject(ctx, application.SeedProjectRequest{Name: "Bệnh viện B", AdminEmail: "b@example.org", AdminFirst: "B", AdminLast: "B", AdminPassword: adminPassword})
	if err != nil {
		t.Fatal(err)
	}
	tb := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}}, basic(other.Client.ID, other.Client.Secret)).json()["access_token"].(string)
	if m := a.introspect(caller, tb).json(); m["active"] != false {
		t.Errorf("project khác: %v", m)
	}
	// ngược lại: chính token đó hỏi về mình thì hoạt động
	if m := a.introspect(t2, t2).json(); m["active"] != true {
		t.Errorf("tự hỏi: %v", m)
	}
	if m := a.introspect(tb, tb).json(); m["active"] != true {
		t.Errorf("tự hỏi project khác: %v", m)
	}
}

func TestIntrospectReportsPatientContextAndInactiveMembership(t *testing.T) {
	a := newCodeAPI(t, nil)
	launch, err := a.c.Store.CreateResource(context.Background(), &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch", Patient: &fhir.Reference{Reference: "Patient/p7"}})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := a.loginCode(loginOpts{scope: "openid", challenge: s256(), method: "S256", launch: launch.GetID()})
	tk := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil).json()
	access := tk["access_token"].(string)
	if m := a.introspect(access, access).json(); m["patient"] != "p7" {
		t.Errorf("patient: %v", m)
	}
	// membership bị tắt: inactive ngay
	m := *a.seed.Membership
	f := false
	m.Active = &f
	if _, err := a.c.Store.UpdateResource(context.Background(), &m, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// người gọi cũng mất quyền (Bearer 401) nên dùng token của client để hỏi
	cc := a.clientToken(nil).json()["access_token"].(string)
	if a.introspect(cc, access).status == 200 && a.introspect(cc, access).json()["active"] == true {
		t.Error("membership tắt: token phải inactive")
	}
}

func TestDiscoveryAdvertisesResourceEndpoints(t *testing.T) {
	a := newCodeAPI(t, nil)
	d := a.do("GET", "/.well-known/openid-configuration", "", nil).json()
	if d["userinfo_endpoint"] != a.c.Settings.BaseURL+"/oauth2/userinfo" || d["introspection_endpoint"] != a.c.Settings.BaseURL+"/oauth2/introspect" {
		t.Errorf("%v", d)
	}
	if a.do("GET", "/.well-known/smart-configuration", "", nil).json()["introspection_endpoint"] == nil || a.do("GET", "/.well-known/oauth-protected-resource", "", nil).json()["introspection_endpoint"] == nil {
		t.Error("smart-configuration và protected-resource phải khai introspection_endpoint")
	}
}

// Logins không có client: chỉ project mới tách được token của người này với người khác.
func TestIntrospectChecksProjectForLoginsWithoutClient(t *testing.T) {
	a := newCodeAPI(t, nil)
	ctx := context.Background()
	other, err := a.c.Auth.SeedProject(ctx, application.SeedProjectRequest{Name: "Bệnh viện B", AdminEmail: "b@example.org", AdminFirst: "B", AdminLast: "B", AdminPassword: adminPassword})
	if err != nil {
		t.Fatal(err)
	}
	noClient := func(email, projectID string) string {
		t.Helper()
		body := `{"email":"` + email + `","password":"` + adminPassword + `","projectId":"` + projectID + `","scope":"openid","codeChallenge":"` + s256() + `","codeChallengeMethod":"S256"}`
		r := a.do("POST", "/auth/login", body, nil)
		code, _ := r.json()["code"].(string)
		if r.status != 200 || code == "" {
			t.Fatalf("login %s: %d %s", email, r.status, r.body)
		}
		tr := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
		if tr.status != 200 {
			t.Fatalf("đổi code %s: %d %s", email, tr.status, tr.body)
		}
		return tr.json()["access_token"].(string)
	}
	ta1, ta2, tb := noClient("admin@example.org", a.seed.Project.ID), noClient("admin@example.org", a.seed.Project.ID), noClient("b@example.org", other.Project.ID)
	if m := a.introspect(ta1, ta2).json(); m["active"] != true || m["client_id"] != nil {
		t.Errorf("cùng project, đều không client: %v", m)
	}
	if m := a.introspect(ta1, tb).json(); m["active"] != false {
		t.Errorf("project khác (đều không client): %v", m)
	}
}
