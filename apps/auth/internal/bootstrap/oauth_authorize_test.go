package bootstrap

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

const verifier2 = "aBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXa" // verifier thứ hai, khác verifier

// nav gửi yêu cầu KHÔNG theo chuyển hướng và trả trạng thái, Location đã parse.
type navResult struct {
	status   int
	body     string
	location *url.URL
	hdr      http.Header
}

func (a *codeAPI) nav(method, path string, form url.Values, hdr map[string]string) navResult {
	a.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, a.srv.URL+path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
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
	out := navResult{status: res.StatusCode, body: sb.String(), hdr: res.Header}
	if loc := res.Header.Get("Location"); loc != "" {
		out.location, _ = url.Parse(loc)
	}
	return out
}

func (a *codeAPI) authorize(q url.Values, hdr map[string]string) navResult {
	a.t.Helper()
	base := url.Values{"response_type": {"code"}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}, "scope": {"openid"},
		"state": {"st-1"}, "code_challenge": {domain.ChallengeS256(verifier2)}, "code_challenge_method": {"S256"}}
	for k, v := range q {
		if len(v) == 1 && v[0] == "-" {
			base.Del(k)
			continue
		}
		base[k] = v
	}
	return a.nav("GET", "/oauth2/authorize?"+base.Encode(), nil, hdr)
}

func (n navResult) toClient(t *testing.T) url.Values {
	t.Helper()
	if n.status != 302 || n.location == nil || n.location.Scheme+"://"+n.location.Host+n.location.Path != redirectURI {
		t.Fatalf("muốn chuyển về redirect_uri: %d %v %s", n.status, n.location, n.body)
	}
	return n.location.Query()
}

func (n navResult) toApp(t *testing.T) url.Values {
	t.Helper()
	if n.status != 302 || n.location == nil || n.location.Scheme+"://"+n.location.Host+n.location.Path != appURL+"/oauth" {
		t.Fatalf("muốn chuyển sang trang ứng dụng: %d %v %s", n.status, n.location, n.body)
	}
	return n.location.Query()
}

func (a *codeAPI) wantRedirectError(n navResult, code, desc string) {
	a.t.Helper()
	q := n.toClient(a.t)
	if q.Get("error") != code || q.Get("error_description") != desc || q.Get("iss") != a.c.Settings.IssuerURL() || q.Get("code") != "" {
		a.t.Errorf("muốn %s/%q kèm iss: %v", code, desc, q)
	}
}

func TestAuthorizeDirectErrorsNeverRedirect(t *testing.T) {
	a := newCodeAPI(t, nil)
	cases := map[string]struct {
		q    url.Values
		want string
	}{
		"client lạ":                     {url.Values{"client_id": {"khong-co"}}, "Client not found"},
		"thiếu client_id":               {url.Values{"client_id": {"-"}}, "Client not found"},
		"thiếu redirect_uri":            {url.Values{"redirect_uri": {"-"}}, "Missing redirect URI"},
		"redirect_uri không parse":      {url.Values{"redirect_uri": {"://hong"}}, "Invalid redirect URI"},
		"redirect_uri không có scheme":  {url.Values{"redirect_uri": {"app.example.org/cb"}}, "Invalid redirect URI"},
		"redirect_uri khác":             {url.Values{"redirect_uri": {"https://evil.example.org/cb"}}, "Invalid redirect URI"},
		"khớp tiền tố, không chính xác": {url.Values{"redirect_uri": {redirectURI + "/x"}}, "Invalid redirect URI"},
		"có thêm query":                 {url.Values{"redirect_uri": {redirectURI + "?a=1"}}, "Invalid redirect URI"},
		"khác hoa thường":               {url.Values{"redirect_uri": {strings.ToUpper(redirectURI)}}, "Invalid redirect URI"},
	}
	for name, c := range cases {
		n := a.authorize(c.q, nil)
		if n.status != 400 || n.body != c.want || n.location != nil {
			t.Errorf("%s: %d %q (Location=%v)", name, n.status, n.body, n.location)
		}
	}
	// client bị tắt coi như không có
	cl := *a.seed.Client
	cl.Status = "off"
	if _, err := a.c.Store.UpdateResource(context.Background(), &cl, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := a.authorize(nil, nil); n.status != 400 || n.body != "Client not found" {
		t.Errorf("client tắt: %d %q", n.status, n.body)
	}
}

func TestAuthorizeParameterErrorsRedirectWithStateAndIss(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.FHIRBaseURL = "https://fhir.example.org/fhir/R4" })
	check := func(name string, q url.Values, code, desc string) {
		t.Helper()
		n := a.authorize(q, nil)
		a.wantRedirectError(n, code, desc)
		if n.location.Query().Get("state") != "st-1" {
			t.Errorf("%s: state phải trả nguyên: %v", name, n.location.Query())
		}
	}
	check("scope", url.Values{"scope": {"-"}}, "invalid_request", "Missing scope")
	check("response_type", url.Values{"response_type": {"token"}}, "unsupported_response_type", "Invalid response type")
	check("response_type vắng", url.Values{"response_type": {"-"}}, "unsupported_response_type", "Invalid response type")
	check("request", url.Values{"request": {"jwt"}}, "request_not_supported", "Unsupported request parameter")
	check("aud lạ", url.Values{"aud": {"https://evil.example.org/"}}, "invalid_request", "Invalid audience")
	check("aud hỏng", url.Values{"aud": {"khong-phai-url"}}, "invalid_request", "Invalid audience")
	check("thiếu method", url.Values{"code_challenge_method": {"-"}}, "invalid_request", "Missing code challenge method")
	check("method lạ", url.Values{"code_challenge_method": {"S512"}}, "invalid_request", "Invalid code challenge method")
	check("plain bị tắt", url.Values{"code_challenge_method": {"plain"}}, "invalid_request", "Invalid code challenge method")
	check("launch lạ", url.Values{"launch": {"khong-co"}}, "invalid_request", "Invalid launch")
	check("max_age chữ", url.Values{"max_age": {"abc"}}, "invalid_request", "Invalid max_age")
	check("max_age âm", url.Values{"max_age": {"-5"}}, "invalid_request", "Invalid max_age")
	// aud hợp lệ: auth hoặc fhir-server (cùng scheme và host)
	for _, aud := range []string{a.c.Settings.BaseURL, a.c.Settings.BaseURL + "/x", "https://fhir.example.org/fhir/R4", "https://fhir.example.org/khac"} {
		a.authorize(url.Values{"aud": {aud}}, nil).toApp(t)
	}
	// client không secret bắt buộc có PKCE
	cl := *a.seed.Client
	cl.Secret = ""
	if _, err := a.c.Store.UpdateResource(context.Background(), &cl, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	check("không secret, không PKCE", url.Values{"code_challenge": {"-"}, "code_challenge_method": {"-"}}, "invalid_request", "Missing code challenge")
	a.authorize(nil, nil).toApp(t)
}

func TestAuthorizeWithoutSessionRedirectsToApp(t *testing.T) {
	a := newCodeAPI(t, nil)
	q := a.authorize(url.Values{"nonce": {"n1"}, "id_token_hint": {"khong-duoc-chuyen"}, "prompt": {"consent"}}, nil).toApp(t)
	if q.Get("client_id") != a.seed.Client.ID || q.Get("redirect_uri") != redirectURI || q.Get("scope") != "openid" || q.Get("state") != "st-1" ||
		q.Get("nonce") != "n1" || q.Get("code_challenge") != domain.ChallengeS256(verifier2) || q.Get("response_type") != "code" || q.Get("login") != "" {
		t.Errorf("tham số: %v", q)
	}
	if q.Has("id_token_hint") || q.Has("error") {
		t.Errorf("không đưa id_token_hint vào URL: %v", q)
	}
	// POST form cho kết quả tương đương
	post := a.nav("POST", "/oauth2/authorize", url.Values{"response_type": {"code"}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI},
		"scope": {"openid"}, "state": {"p"}, "code_challenge": {"c"}, "code_challenge_method": {"S256"}}, nil).toApp(t)
	if post.Get("state") != "p" || post.Get("client_id") != a.seed.Client.ID {
		t.Errorf("POST: %v", post)
	}
	if n := a.nav("POST", "/oauth2/authorize", url.Values{"client_id": {"khong-co"}}, nil); n.status != 400 || n.body != "Client not found" {
		t.Errorf("POST client lạ: %d %q", n.status, n.body)
	}
	// phản hồi không cache, không gửi Referer
	n := a.authorize(nil, nil)
	if n.hdr.Get("Referrer-Policy") != "no-referrer" || n.hdr.Get("Cache-Control") == "" || n.hdr.Get("X-Frame-Options") != "DENY" {
		t.Errorf("header: %v", n.hdr)
	}
	// scope mặc định lấy từ client
	cl := *a.seed.Client
	cl.DefaultScope = []string{"openid", "profile"}
	if _, err := a.c.Store.UpdateResource(context.Background(), &cl, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := a.authorize(url.Values{"scope": {"-"}}, nil).toApp(t).Get("scope"); got != "openid profile" {
		t.Errorf("defaultScope: %q", got)
	}
}

func cookieHdr(clientID, v string) map[string]string {
	return map[string]string{"Cookie": "medplum-" + clientID + "=" + v}
}

// session đăng nhập và đổi code (login đã granted, có cookie) rồi trả cookie, id token và login id.
func (a *codeAPI) session() (cookie, idToken, loginID string, tokens map[string]any) {
	a.t.Helper()
	code, id, ck := a.loginCode(loginOpts{scope: "openid offline_access", challenge: s256(), method: "S256"})
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
	if r.status != 200 || ck == "" {
		a.t.Fatalf("%d %s cookie=%q", r.status, r.body, ck)
	}
	tk := r.json()
	return ck, tk["id_token"].(string), id, tk
}

func (a *codeAPI) loginByCode(code string) *fhir.Login {
	a.t.Helper()
	l, err := a.c.Auth.Logins.FindByCode(context.Background(), domain.HashSecret(code))
	if err != nil || l == nil {
		a.t.Fatalf("không tìm thấy Login theo code: %v", err)
	}
	return l
}

func TestAuthorizePromptNoneRequiresSession(t *testing.T) {
	a := newCodeAPI(t, nil)
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, nil), "login_required", "Login required")
	// cookie của login CHƯA cấp token không phải phiên
	_, _, ck := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, cookieHdr(a.seed.Client.ID, ck)), "login_required", "Login required")
	// cookie lạ
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, cookieHdr(a.seed.Client.ID, "ngau-nhien")), "login_required", "Login required")
	// cookie đúng nhưng đặt cho tên client khác
	cookie, _, _, _ := a.session()
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, cookieHdr("client-khac", cookie)), "login_required", "Login required")
	// login bị thu hồi
	cookie2, _, id2, _ := a.session()
	if err := a.c.Auth.RevokeLogin(context.Background(), a.storedLogin(id2)); err != nil {
		t.Fatal(err)
	}
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, cookieHdr(a.seed.Client.ID, cookie2)), "login_required", "Login required")
}

func TestAuthorizeWithSessionCreatesNewLoginAndReturnsCode(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, oldID, _ := a.session()
	before := a.storedLogin(oldID)

	q := a.authorize(url.Values{"prompt": {"none"}, "nonce": {"n-moi"}}, cookieHdr(a.seed.Client.ID, cookie)).toClient(t)
	code := q.Get("code")
	if code == "" || q.Get("state") != "st-1" || q.Get("iss") != a.c.Settings.IssuerURL() || q.Has("error") {
		t.Fatalf("muốn code kèm state và iss: %v", q)
	}
	nl := a.loginByCode(code)
	if nl.ID == oldID || nl.BasedOn == nil || nl.BasedOn.Reference != "Login/"+oldID || domain.IsTrue(nl.Granted) || nl.Nonce != "n-moi" ||
		nl.RefreshSecret == "" || nl.RefreshSecret == before.RefreshSecret || nl.Membership == nil || nl.Scope != "openid" ||
		nl.Client == nil || nl.User == nil || domain.LoginRedirectURI(nl) != redirectURI || nl.CodeChallenge != domain.ChallengeS256(verifier2) {
		t.Errorf("Login mới: %+v", nl)
	}
	// Login cũ không bị đụng
	after := a.storedLogin(oldID)
	if after.Meta.VersionId != before.Meta.VersionId || domain.IsTrue(after.Revoked) {
		t.Error("Login cũ không được thay đổi")
	}
	// code đổi được bằng verifier của lần ủy quyền MỚI, không phải verifier của login cũ
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI}}, nil), "invalid_request", "Invalid code verifier")
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier2}, "redirect_uri": {redirectURI}}, nil)
	if r.status != 200 || r.json()["id_token"] == nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if c, _ := a.c.Keys.Verify(r.json()["id_token"].(string)); c["nonce"] != "n-moi" {
		t.Errorf("nonce trong id token: %v", c["nonce"])
	}
}

// Medplum kế thừa codeChallenge của Login cũ khi request không có (lỗi tiềm ẩn); auth không kế thừa.
func TestAuthorizeDoesNotInheritPKCEFromPreviousLogin(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, _, _ := a.session() // login cũ có code_challenge
	q := a.authorize(url.Values{"prompt": {"none"}, "code_challenge": {"-"}, "code_challenge_method": {"-"}}, cookieHdr(a.seed.Client.ID, cookie)).toClient(t)
	nl := a.loginByCode(q.Get("code"))
	if nl.CodeChallenge != "" || nl.CodeChallengeMethod != "" {
		t.Fatalf("không được kế thừa PKCE: %q", nl.CodeChallenge)
	}
	// không PKCE nên chỉ client xác thực bằng secret mới đổi được
	wantOAuthError(t, a.exchange(url.Values{"code": {q.Get("code")}, "code_verifier": {verifier}}, nil), "invalid_request", "Missing verification context")
}

func TestAuthorizeWithSessionShowsScopeScreenUnlessPromptNone(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, oldID, _ := a.session()
	hdr := cookieHdr(a.seed.Client.ID, cookie)

	q := a.authorize(url.Values{"scope": {"openid profile"}}, hdr).toApp(t)
	if q.Get("login") == "" || q.Get("scope") != "openid profile" || q.Get("login") == oldID {
		t.Fatalf("muốn login mới và scope: %v", q)
	}
	nl := a.storedLogin(q.Get("login"))
	if nl.BasedOn == nil || domain.IsTrue(nl.Granted) || nl.Scope != "openid profile" {
		t.Errorf("%+v", nl)
	}
	// prompt=login: bắt đăng nhập lại, không tạo Login mới
	pl := a.authorize(url.Values{"prompt": {"login"}}, hdr).toApp(t)
	if pl.Has("login") {
		t.Errorf("prompt=login không dùng phiên: %v", pl)
	}
}

func TestAuthorizeMaxAgeAndInvalidSessions(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, _, _ := a.session()
	hdr := cookieHdr(a.seed.Client.ID, cookie)
	a.clock.t.Add(1800)
	if q := a.authorize(url.Values{"prompt": {"none"}}, hdr).toClient(t); q.Get("code") == "" {
		t.Errorf("30 phút < max_age mặc định 1 giờ: %v", q)
	}
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}, "max_age": {"600"}}, hdr), "login_required", "Login required")
	a.clock.t.Add(2000) // quá 1 giờ kể từ lúc đăng nhập
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, hdr), "login_required", "Login required")
	// quá hạn nhưng không prompt=none: về trang đăng nhập, không tạo Login mới
	if q := a.authorize(nil, hdr).toApp(t); q.Has("login") {
		t.Errorf("phiên quá hạn: %v", q)
	}
}

func TestAuthorizeIDTokenHint(t *testing.T) {
	a := newCodeAPI(t, nil)
	_, idToken, _, tokens := a.session()
	with := func(h string) navResult {
		q := url.Values{"prompt": {"none"}, "id_token_hint": {h}}
		return a.authorize(q, nil)
	}
	if q := with(idToken).toClient(t); q.Get("code") == "" {
		t.Errorf("id token hợp lệ: %v", q)
	}
	for name, hint := range map[string]string{
		"refresh token": tokens["refresh_token"].(string), "access token": tokens["access_token"].(string), "rác": "abc.def.ghi",
		"bị sửa": idToken[:len(idToken)-3] + "AAA",
	} {
		if q := with(hint); q.status != 302 || q.location.Query().Get("error") != "login_required" {
			t.Errorf("%s: %d %v", name, q.status, q.location)
		}
	}
	// id token của client khác không dùng được cho client này
	other, err := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "Hai", RedirectURIs: []string{redirectURI}})
	if err != nil {
		t.Fatal(err)
	}
	otherQ := url.Values{"response_type": {"code"}, "client_id": {other.ID}, "redirect_uri": {redirectURI}, "scope": {"openid"}, "prompt": {"none"}, "id_token_hint": {idToken}}
	a.wantRedirectError(a.nav("GET", "/oauth2/authorize?"+otherQ.Encode(), nil, nil), "login_required", "Login required")
	// hint hợp lệ nhưng Login bị thu hồi
	c, _ := a.c.Keys.Verify(idToken)
	if err := a.c.Auth.RevokeLogin(context.Background(), a.storedLogin(c["login_id"].(string))); err != nil {
		t.Fatal(err)
	}
	a.wantRedirectError(with(idToken), "login_required", "Login required")
}

func TestAuthorizeRefusesSessionWhenMembershipOrIPNoLongerAllowed(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, _, _ := a.session()
	hdr := cookieHdr(a.seed.Client.ID, cookie)
	// IP bị chặn bởi access policy hiện tại của membership
	pol, err := a.c.Store.CreateResource(context.Background(), &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "chặn", Meta: &fhir.Meta{Project: a.seed.Project.ID},
		IpAccessRule: []fhir.AccessPolicyIpAccessRule{{Value: "127.0.0.1", Action: "block"}}, Resource: []fhir.AccessPolicyResource{{ResourceType: "*"}}})
	if err != nil {
		t.Fatal(err)
	}
	m := *a.seed.Membership
	m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + pol.GetID()}
	updated, err := a.c.Store.UpdateResource(context.Background(), &m, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, hdr), "access_denied", "IP address not allowed")
	// membership bị tắt: không còn phiên
	mm := *(updated.(*fhir.ProjectMembership))
	f := false
	mm.Active = &f
	if _, err := a.c.Store.UpdateResource(context.Background(), &mm, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, hdr), "login_required", "Login required")
}

// Q11: logout thu hồi cả chuỗi basedOn, nên chấm dứt phiên trình duyệt (Login gốc giữ cookie).
func TestLogoutRevokesSessionChain(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, rootID, _ := a.session()
	hdr := cookieHdr(a.seed.Client.ID, cookie)
	code := a.authorize(url.Values{"prompt": {"none"}}, hdr).toClient(t).Get("code")
	tk := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier2}, "redirect_uri": {redirectURI}}, nil).json()
	childID := a.loginByCode(code).ID
	// phiên còn sống trước khi đăng xuất
	if q := a.authorize(url.Values{"prompt": {"none"}}, hdr).toClient(t); q.Get("code") == "" {
		t.Fatalf("phiên phải còn: %v", q)
	}
	if r := a.post("/oauth2/logout", tk["access_token"].(string)); r.status != 200 {
		t.Fatalf("logout: %d %s", r.status, r.body)
	}
	if !domain.IsTrue(a.storedLogin(childID).Revoked) || !domain.IsTrue(a.storedLogin(rootID).Revoked) {
		t.Error("phải thu hồi cả Login con lẫn Login gốc")
	}
	a.wantRedirectError(a.authorize(url.Values{"prompt": {"none"}}, hdr), "login_required", "Login required")
}

func TestAuthorizeDisabledWithoutAppBaseURL(t *testing.T) {
	a := newAPI(t, nil, nil) // mặc định không có AUTH_APP_BASE_URL ngoài local
	a2 := &codeAPI{api: a}
	if n := a2.nav("GET", "/oauth2/authorize?client_id=x", nil, nil); n.status != 404 {
		t.Errorf("không có trang đăng nhập thì không có /oauth2/authorize: %d", n.status)
	}
	if _, has := a.do("GET", "/.well-known/openid-configuration", "", nil).json()["authorization_endpoint"]; has {
		t.Error("không được khai authorization_endpoint")
	}
}

func TestDiscoveryAdvertisesAuthorizeWhenEnabled(t *testing.T) {
	a := newCodeAPI(t, nil)
	d := a.do("GET", "/.well-known/openid-configuration", "", nil).json()
	if d["authorization_endpoint"] != a.c.Settings.BaseURL+"/oauth2/authorize" || len(d["response_types_supported"].([]any)) != 1 {
		t.Errorf("%v", d)
	}
	if a.do("GET", "/.well-known/smart-configuration", "", nil).json()["authorization_endpoint"] == nil {
		t.Error("smart-configuration phải khai authorization_endpoint")
	}
}

// Cookie của client A đặt dưới tên cookie của client B không được cho B một phiên (xác thực chéo client).
func TestAuthorizeSessionCookieIsBoundToClient(t *testing.T) {
	a := newCodeAPI(t, nil)
	cookie, _, _, _ := a.session()
	other, err := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "Hai", RedirectURIs: []string{redirectURI}})
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"response_type": {"code"}, "client_id": {other.ID}, "redirect_uri": {redirectURI}, "scope": {"openid"}, "prompt": {"none"}}
	a.wantRedirectError(a.nav("GET", "/oauth2/authorize?"+q.Encode(), nil, cookieHdr(other.ID, cookie)), "login_required", "Login required")
}

// id_token_hint phải là id token: token ký bởi auth, aud đúng client, login hợp lệ nhưng token_use khác vẫn bị từ chối.
func TestAuthorizeIDTokenHintRequiresIDTokenUse(t *testing.T) {
	a := newCodeAPI(t, nil)
	_, idToken, _, _ := a.session()
	claims, err := a.c.Keys.Verify(idToken)
	if err != nil {
		t.Fatal(err)
	}
	hint := func(use any) navResult {
		c := map[string]any{}
		for k, v := range claims {
			c[k] = v
		}
		if use == nil {
			delete(c, "token_use")
		} else {
			c["token_use"] = use
		}
		raw, _ := a.c.Keys.Sign(c)
		return a.authorize(url.Values{"prompt": {"none"}, "id_token_hint": {raw}}, nil)
	}
	if q := hint("id").toClient(t); q.Get("code") == "" {
		t.Fatalf("id token đúng: %v", q)
	}
	for _, use := range []any{"access", "refresh", "", nil} {
		if n := hint(use); n.status != 302 || n.location.Query().Get("error") != "login_required" {
			t.Errorf("token_use=%v: %d %v", use, n.status, n.location)
		}
	}
}
