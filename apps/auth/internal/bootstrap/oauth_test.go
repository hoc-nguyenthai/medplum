package bootstrap

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

func (a *api) form(path string, v url.Values, hdr map[string]string) resp {
	a.t.Helper()
	req, err := http.NewRequest("POST", a.srv.URL+path, strings.NewReader(v.Encode()))
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, val := range hdr {
		req.Header.Set(k, val)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp{res.StatusCode, res.Header, sb.String()}
}

func basic(id, secret string) map[string]string {
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(id)+":"+url.QueryEscape(secret)))}
}

func (a *api) clientToken(extra url.Values) resp {
	v := url.Values{"grant_type": {"client_credentials"}}
	for k, vals := range extra {
		v[k] = vals
	}
	return a.form("/oauth2/token", v, basic(a.seed.Client.ID, a.seed.Client.Secret))
}

func (a *api) clientMembership() *fhir.ProjectMembership {
	a.t.Helper()
	list, err := a.c.Auth.Memberships.ListForUser(context.Background(), "ClientApplication/"+a.seed.Client.ID, "", 5)
	if err != nil || len(list) != 1 {
		a.t.Fatalf("membership của client: %v %v", list, err)
	}
	return &list[0]
}

func (a *api) updateMembership(fn func(*fhir.ProjectMembership)) {
	a.t.Helper()
	m := a.clientMembership()
	fn(m)
	if _, err := a.c.Store.UpdateResource(context.Background(), m, fhirrouter.UpdateOptions{}); err != nil {
		a.t.Fatal(err)
	}
}

func (a *api) loginCount() int {
	a.t.Helper()
	ids, err := a.c.Auth.Logins.ListIDsByUser(context.Background(), "ClientApplication/"+a.seed.Client.ID)
	if err != nil {
		a.t.Fatal(err)
	}
	return len(ids)
}

// verifier dựng fhirauth.Verifier đúng như fhir-server sẽ cấu hình: JWKS của auth, issuer, audience.
func (a *api) verifier() *fhirauth.Verifier {
	a.t.Helper()
	v, err := fhirauth.NewVerifier(fhirauth.Config{
		Issuer: a.c.Settings.IssuerURL(), Audience: a.c.Settings.AccessAudience(), JWKSURL: a.srv.URL + "/.well-known/jwks.json",
		ProjectClaim: "fhir_project", ScopeClaim: "scope", PolicyClaim: "fhir_access_policy",
	})
	if err != nil {
		a.t.Fatal(err)
	}
	return v
}

func TestClientCredentialsIssuesTokenThatFhirServerAccepts(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.clientToken(nil)
	if r.status != 200 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", r.status, r.hdr, r.body)
	}
	body := r.json()
	if body["token_type"] != "Bearer" || body["expires_in"].(float64) != 900 || body["scope"] != "openid" ||
		body["id_token"] == nil || body["refresh_token"] != nil {
		t.Fatalf("body: %v", body)
	}
	if body["project"].(map[string]any)["reference"] != "Project/"+a.seed.Project.ID {
		t.Errorf("project: %v", body["project"])
	}

	// token đi qua đúng bộ xác minh của fhir-server (pkg/fhirauth) với JWKS do auth công bố
	p, err := a.verifier().Verify(context.Background(), body["access_token"].(string))
	if err != nil {
		t.Fatalf("fhirauth từ chối token: %v", err)
	}
	if p.Project != a.seed.Project.ID || p.Subject != a.seed.Client.ID || p.Role != fhirauth.RoleWrite {
		t.Errorf("principal: %+v", p)
	}
	// chính sách mặc định của client: ghi dữ liệu lâm sàng, không đọc được ClientApplication (secret) và loại quản trị
	if !p.CanReadResource("Patient", map[string]any{}) || p.CanReadResource("ClientApplication", map[string]any{}) ||
		p.CanReadResource("User", map[string]any{}) || p.CanReadResource("Login", map[string]any{}) {
		t.Error("quyền đọc theo chính sách sai")
	}
	if !p.Expires.After(time.Now()) || p.Expires.After(time.Now().Add(16*time.Minute)) {
		t.Errorf("hạn token: %v", p.Expires)
	}
	if a.loginCount() != 1 {
		t.Errorf("số Login = %d", a.loginCount())
	}
}

func TestClientCredentialsBodyCredentialsAndNoRefreshToken(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {a.seed.Client.ID}, "client_secret": {a.seed.Client.Secret}, "scope": {"openid offline_access"}}, nil)
	if r.status != 200 || r.json()["refresh_token"] != nil {
		t.Fatalf("client_credentials không bao giờ có refresh token: %d %s", r.status, r.body)
	}
}

func TestClientCredentialsErrors(t *testing.T) {
	a := newAPI(t, nil, nil)
	post := func(v url.Values, hdr map[string]string) resp { return a.form("/oauth2/token", v, hdr) }
	cc := url.Values{"grant_type": {"client_credentials"}}
	errOf := func(r resp) (string, string) {
		m := r.json()
		d, _ := m["error_description"].(string)
		return m["error"].(string), d
	}

	// nội dung request
	if r := a.do("POST", "/oauth2/token", `{"grant_type":"client_credentials"}`, nil); r.status != 400 || r.body != "Unsupported content type" {
		t.Errorf("JSON: %d %q", r.status, r.body)
	}
	if r := post(url.Values{}, nil); r.status != 400 {
		t.Errorf("thiếu grant_type: %d", r.status)
	} else if c, d := errOf(r); c != "invalid_request" || d != "Missing grant_type" {
		t.Errorf("%s %s", c, d)
	}
	if c, d := errOf(post(url.Values{"grant_type": {"password"}}, nil)); c != "invalid_request" || d != "Unsupported grant_type" {
		t.Errorf("%s %s", c, d)
	}
	// xác thực client
	cases := []struct {
		name string
		v    url.Values
		hdr  map[string]string
		desc string
	}{
		{"thiếu client_id", cc, nil, "Missing client_id"},
		{"thiếu secret", url.Values{"grant_type": cc["grant_type"], "client_id": {a.seed.Client.ID}}, nil, "Missing client_secret"},
		{"client không có", cc, basic("khong-co", "x"), "Invalid client"},
		{"sai secret", cc, basic(a.seed.Client.ID, "sai"), "Invalid secret"},
		{"Basic hỏng", cc, map[string]string{"Authorization": "Basic !!!"}, "Invalid authorization header"},
		{"không phải Basic", cc, map[string]string{"Authorization": "Bearer abc"}, "Invalid authorization header"},
		{"client_assertion sai loại", url.Values{"grant_type": cc["grant_type"], "client_assertion_type": {"x"}}, nil, "Unsupported client assertion type"},
	}
	for _, c := range cases {
		r := post(c.v, c.hdr)
		if code, d := errOf(r); r.status != 400 || code != "invalid_request" || d != c.desc {
			t.Errorf("%s: %d %s %s", c.name, r.status, code, d)
		}
	}
	if a.loginCount() != 0 {
		t.Errorf("lỗi xác thực không được tạo Login, có %d", a.loginCount())
	}
}

func TestClientCredentialsInactiveClientAndProject(t *testing.T) {
	a := newAPI(t, nil, nil)
	ctx := context.Background()
	c := *a.seed.Client
	c.Status = "off"
	if _, err := a.c.Store.UpdateResource(ctx, &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := a.clientToken(nil); r.status != 400 || r.json()["error_description"] != "Invalid client" {
		t.Errorf("client tắt: %d %s", r.status, r.body)
	}
	c.Status = "active"
	if _, err := a.c.Store.UpdateResource(ctx, &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := a.clientToken(nil); r.status != 200 {
		t.Fatalf("client bật lại: %d %s", r.status, r.body)
	}
	a.updateMembership(func(m *fhir.ProjectMembership) { f := false; m.Active = &f })
	if r := a.clientToken(nil); r.status != 400 {
		t.Errorf("membership tắt: %d %s", r.status, r.body) // không còn membership active: Invalid client
	}
}

// Scope do client khai không bao giờ trở thành quyền: fhir.* thuộc không gian tên của auth.
func TestClientCredentialsNeverGrantsFhirAdminFromRequest(t *testing.T) {
	a := newAPI(t, nil, nil)
	for _, scope := range []string{"fhir.admin", "openid fhir.admin", "FHIR.SUPER", "fhir.write"} {
		r := a.clientToken(url.Values{"scope": {scope}})
		if r.status != 400 || r.json()["error"] != "invalid_scope" {
			t.Errorf("%q: %d %s", scope, r.status, r.body)
		}
	}
	if r := a.clientToken(url.Values{"scope": {"patient/Patient.rs"}}); r.status != 400 || r.json()["error"] != "invalid_scope" {
		t.Errorf("patient/: %d %s", r.status, r.body)
	}
	if a.loginCount() != 0 {
		t.Errorf("scope bị từ chối không được tạo Login, có %d", a.loginCount())
	}
	// client không phải admin dù có gửi gì: scope claim chỉ có fhir.write do auth thêm
	r := a.clientToken(url.Values{"scope": {"openid"}})
	p, err := a.verifier().Verify(context.Background(), r.json()["access_token"].(string))
	if err != nil || p.Role != fhirauth.RoleWrite {
		t.Errorf("%+v %v", p, err)
	}
}

func TestClientCredentialsRoleFollowsMembership(t *testing.T) {
	a := newAPI(t, nil, nil)
	a.updateMembership(func(m *fhir.ProjectMembership) { y := true; m.Admin = &y })
	p, err := a.verifier().Verify(context.Background(), a.clientToken(nil).json()["access_token"].(string))
	if err != nil || p.Role != fhirauth.RoleAdmin {
		t.Fatalf("admin của project phải có fhir.admin: %+v %v", p, err)
	}
	// scope SMART thì auth KHÔNG thêm fhir.*: quyền chỉ còn theo SMART
	p, err = a.verifier().Verify(context.Background(), a.clientToken(url.Values{"scope": {"system/Patient.rs"}}).json()["access_token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if p.Role == fhirauth.RoleAdmin || p.Grants == nil || p.Grants.Can("Observation", fhirauth.PermRead) || !p.Grants.CanRead("Patient") || p.Grants.Can("Patient", fhirauth.PermCreate) {
		t.Errorf("SMART system/Patient.rs chỉ được đọc và tìm Patient: %+v", p.Grants)
	}
}

func TestClientCredentialsIPRuleAndUnsupportedPolicyCreateNoLogin(t *testing.T) {
	a := newAPI(t, nil, nil)
	ctx := context.Background()
	mkPolicy := func(p fhir.AccessPolicy) {
		p.ResourceType, p.Meta = "AccessPolicy", &fhir.Meta{Project: a.seed.Project.ID}
		out, err := a.c.Store.CreateResource(ctx, &p)
		if err != nil {
			t.Fatal(err)
		}
		a.updateMembership(func(m *fhir.ProjectMembership) {
			m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + out.GetID()}
		})
	}

	mkPolicy(fhir.AccessPolicy{IpAccessRule: []fhir.AccessPolicyIpAccessRule{{Value: "127.0.0.1", Action: "block"}}, Resource: []fhir.AccessPolicyResource{{ResourceType: "*"}}})
	if r := a.clientToken(nil); r.status != 400 || r.json()["error_description"] != "IP address not allowed" {
		t.Errorf("chặn IP: %d %s", r.status, r.body)
	}
	if a.loginCount() != 0 {
		t.Errorf("bị chặn IP thì không được để lại Login granted, có %d", a.loginCount())
	}

	mkPolicy(fhir.AccessPolicy{Compartment: &fhir.Reference{Reference: "Patient/1"}, Resource: []fhir.AccessPolicyResource{{ResourceType: "*"}}})
	if r := a.clientToken(nil); r.status != 400 || r.json()["error"] != "access_denied" || r.json()["error_description"] != domain.ErrUnsupportedPolicy {
		t.Errorf("chính sách không hỗ trợ: %d %s", r.status, r.body)
	}
	if a.loginCount() != 0 {
		t.Errorf("chính sách bị từ chối thì không tạo Login, có %d", a.loginCount())
	}
}

func TestClientCredentialsPolicyClaimSizeCap(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.MaxPolicyClaimBytes = 200 }, nil)
	if r := a.clientToken(nil); r.status != 400 || r.json()["error"] != "access_denied" {
		t.Errorf("claim quá lớn phải bị từ chối: %d %s", r.status, r.body)
	}
	if a.loginCount() != 0 {
		t.Error("không tạo Login")
	}
}

func TestBearerAuthenticationOnAuthRoutes(t *testing.T) {
	a := newAPI(t, nil, nil)
	token := a.clientToken(nil).json()
	access := token["access_token"].(string)
	get := func(tok string) resp {
		return a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + tok})
	}
	if r := get(access); r.status != 200 {
		t.Fatalf("access token hợp lệ: %d %s", r.status, r.body)
	}
	// id token không phải access token
	if r := get(token["id_token"].(string)); r.status != 401 {
		t.Errorf("id token dùng làm Bearer: %d", r.status)
	}
	for name, bad := range map[string]string{"rỗng": "", "rác": "abc.def.ghi", "bị sửa": access[:len(access)-3] + "AAA"} {
		if r := get(bad); r.status != 401 {
			t.Errorf("%s: %d", name, r.status)
		}
	}
	// thu hồi có hiệu lực ngay ở route của auth (có trạng thái)
	ids, _ := a.c.Auth.Logins.ListIDsByUser(context.Background(), "ClientApplication/"+a.seed.Client.ID)
	login, _ := a.c.Auth.Logins.Get(context.Background(), ids[0])
	if err := a.c.Auth.RevokeLogin(context.Background(), login); err != nil {
		t.Fatal(err)
	}
	if r := get(access); r.status != 401 {
		t.Errorf("sau thu hồi: %d", r.status)
	}
}

func TestBearerRejectsTokenFromAnotherIssuerOrKey(t *testing.T) {
	a := newAPI(t, nil, nil)
	b := newAPI(t, nil, nil) // auth khác: khóa tạm khác, cùng issuer mặc định
	foreign := b.clientToken(nil).json()["access_token"].(string)
	if r := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + foreign}); r.status != 401 {
		t.Errorf("token của khóa khác: %d", r.status)
	}
}

func TestWellKnownAndJWKS(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.do("GET", "/.well-known/jwks.json", "", nil)
	if r.status != 200 || r.hdr.Get("Cache-Control") != "public, max-age=300" || r.hdr.Get("ETag") == "" {
		t.Fatalf("%d %v", r.status, r.hdr)
	}
	for _, private := range []string{`"d"`, `"p"`, `"q"`, `"dp"`, `"dq"`, `"qi"`} {
		if strings.Contains(r.body, private+":") {
			t.Errorf("JWKS lộ tham số riêng %s: %s", private, r.body)
		}
	}
	if r2 := a.do("GET", "/.well-known/jwks.json", "", map[string]string{"If-None-Match": r.hdr.Get("ETag")}); r2.status != 304 {
		t.Errorf("ETag: %d", r2.status)
	}
	disco := a.do("GET", "/.well-known/openid-configuration", "", nil).json()
	if disco["issuer"] != a.c.Settings.IssuerURL() || disco["token_endpoint"] != a.c.Settings.BaseURL+"/oauth2/token" ||
		disco["jwks_uri"] != a.c.Settings.BaseURL+"/.well-known/jwks.json" {
		t.Errorf("discovery: %v", disco)
	}
	if got := a.do("GET", "/.well-known/oauth-authorization-server", "", nil).json(); got["issuer"] != disco["issuer"] {
		t.Errorf("oauth-authorization-server: %v", got)
	}
	// chỉ khai thứ đã cài
	if gt := disco["grant_types_supported"].([]any); len(gt) != 5 || gt[0] != "authorization_code" || gt[1] != "refresh_token" || gt[2] != "client_credentials" || gt[3] != "urn:ietf:params:oauth:grant-type:pre-authorized_code" || gt[4] != "urn:ietf:params:oauth:grant-type:token-exchange" {
		t.Errorf("grant_types_supported: %v", gt)
	}
	if _, has := disco["authorization_endpoint"]; has {
		t.Error("chưa cài /oauth2/authorize thì không được khai")
	}
	if r := a.do("GET", "/.well-known/smart-styles.json", "", nil); r.status != 200 || r.json()["color_text"] == nil {
		t.Errorf("smart-styles: %d", r.status)
	}
	if r := a.do("GET", "/.well-known/smart-configuration", "", nil); r.status != 200 || r.json()["token_endpoint"] == nil {
		t.Errorf("smart-configuration: %d", r.status)
	}
	// oauth-protected-resource
	if r := a.do("GET", "/.well-known/oauth-protected-resource", "", nil); r.status != 200 || r.json()["resource"] == nil {
		t.Errorf("protected-resource: %d", r.status)
	}
	if r := a.do("GET", "/.well-known/oauth-protected-resource?resource=https://evil.example/", "", nil); r.status != 400 || r.body != "Invalid resource URL" {
		t.Errorf("resource lạ: %d %q", r.status, r.body)
	}
	custom := a.c.Settings.BaseURL + "/fhir/R4"
	if r := a.do("GET", "/.well-known/oauth-protected-resource?resource="+url.QueryEscape(custom), "", nil); r.status != 200 || r.json()["resource"] != custom {
		t.Errorf("resource hợp lệ: %d %s", r.status, r.body)
	}
	// địa chỉ NHÁI tiền tố (host dài hơn base) phải bị từ chối; base đứng một mình và base + ?, #, / thì hợp lệ
	base := a.c.Settings.BaseURL
	for _, bad := range []string{base + ".evil.example/", base + "evil", base + "@evil.example/", base + ":1/"} {
		if r := a.do("GET", "/.well-known/oauth-protected-resource?resource="+url.QueryEscape(bad), "", nil); r.status != 400 || r.body != "Invalid resource URL" {
			t.Errorf("resource nhái tiền tố %q: %d %q", bad, r.status, r.body)
		}
	}
	for _, good := range []string{base, base + "/", base + "/fhir/R4?x=1", base + "?x=1", base + "#frag"} {
		if r := a.do("GET", "/.well-known/oauth-protected-resource?resource="+url.QueryEscape(good), "", nil); r.status != 200 || r.json()["resource"] != good {
			t.Errorf("resource hợp lệ %q: %d %s", good, r.status, r.body)
		}
	}
}

// token_use phải được kiểm riêng, không chỉ dựa vào aud: token có aud và login_id hợp lệ nhưng không phải loại access vẫn bị từ chối.
func TestBearerRequiresAccessTokenUse(t *testing.T) {
	a := newAPI(t, nil, nil)
	access := a.clientToken(nil).json()["access_token"].(string)
	claims, err := a.c.Keys.Verify(access)
	if err != nil {
		t.Fatal(err)
	}
	me := func(c map[string]any) int {
		raw, err := a.c.Keys.Sign(c)
		if err != nil {
			t.Fatal(err)
		}
		return a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + raw}).status
	}
	if me(claims) != 200 {
		t.Fatal("claims gốc phải hợp lệ")
	}
	for _, use := range []any{"id", "refresh", "", nil} {
		c := map[string]any{}
		for k, v := range claims {
			c[k] = v
		}
		if use == nil {
			delete(c, "token_use")
		} else {
			c["token_use"] = use
		}
		if got := me(c); got != 401 {
			t.Errorf("token_use=%v: %d, muốn 401", use, got)
		}
	}
	for name, mut := range map[string]func(map[string]any){
		"iss lạ":            func(c map[string]any) { c["iss"] = "http://khac" },
		"aud lạ":            func(c map[string]any) { c["aud"] = "khac" },
		"có refresh_secret": func(c map[string]any) { c["refresh_secret"] = "x" },
		"login_id lạ":       func(c map[string]any) { c["login_id"] = "khong-co" },
		"thiếu login_id":    func(c map[string]any) { delete(c, "login_id") },
	} {
		c := map[string]any{}
		for k, v := range claims {
			c[k] = v
		}
		mut(c)
		if got := me(c); got != 401 {
			t.Errorf("%s: %d, muốn 401", name, got)
		}
	}
}

func TestKeysCommand(t *testing.T) {
	dir := t.TempDir()
	s := config.Default()
	s.SigningKeyPath = dir
	var out strings.Builder
	if err := RunKeys(s, []string{"generate", "-alg", "ES256"}, &out); err != nil || !strings.Contains(out.String(), "AUTH_ACTIVE_KID=") {
		t.Fatalf("%v %s", err, out.String())
	}
	out.Reset()
	if err := RunKeys(s, []string{"list"}, &out); err != nil || !strings.Contains(out.String(), "1 khóa") || !strings.Contains(out.String(), "ES256") {
		t.Fatalf("%v %s", err, out.String())
	}
	// khóa sinh ra dùng được để chạy service
	c := newContainer(t, func(st *config.Settings) { st.SigningKeyPath = dir })
	if _, err := c.Handler(nil); err != nil || c.Keys.SigningAlg() != "ES256" {
		t.Fatalf("%v", err)
	}
	for _, bad := range [][]string{nil, {"generate", "-alg", "HS256"}, {"nope"}, {"generate", "-dir", ""}} {
		s2 := s
		if len(bad) > 0 && bad[0] == "generate" && len(bad) == 3 && bad[1] == "-dir" {
			s2.SigningKeyPath = ""
		}
		if err := RunKeys(s2, bad, &out); err == nil {
			t.Errorf("%v phải lỗi", bad)
		}
	}
	// thư mục khóa hỏng: Handler từ chối khởi động thay vì chạy không có khóa
	bad := t.TempDir()
	c2 := newContainer(t, func(st *config.Settings) { st.SigningKeyPath = bad })
	if _, err := c2.Handler(nil); err == nil {
		t.Error("thư mục không có khóa phải làm Handler lỗi")
	}
}
