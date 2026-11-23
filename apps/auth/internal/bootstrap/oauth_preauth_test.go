package bootstrap

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

const preAuthGrant = application.GrantTypePreAuthorizedCode

// preAuthorized dựng Login pre-authorized như /auth/preauthorize (P6) sẽ làm, rồi trả code rõ.
func (a *codeAPI) preAuthorized(mut func(*fhir.Login)) (code string, login *fhir.Login) {
	a.t.Helper()
	code = domain.GenerateSecret(16)
	l := &fhir.Login{
		ResourceType: "Login", AuthMethod: "pre-authorized", PreAuthorizedCodeHash: domain.HashSecret(code),
		User: a.seed.Membership.User, Client: &fhir.Reference{Reference: "ClientApplication/" + a.seed.Client.ID},
		Project: &fhir.Reference{Reference: "Project/" + a.seed.Project.ID}, Membership: &fhir.Reference{Reference: "ProjectMembership/" + a.seed.Membership.ID},
		Scope: "openid", AuthTime: a.clock.Now().UTC().Format(time.RFC3339Nano), ExpiresAt: a.clock.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano),
	}
	if mut != nil {
		mut(l)
	}
	created, err := a.c.Auth.Logins.Create(context.Background(), l)
	if err != nil {
		a.t.Fatal(err)
	}
	return code, created
}

func (a *codeAPI) preAuthExchange(clientID, code string) resp {
	a.t.Helper()
	v := url.Values{"grant_type": {preAuthGrant}}
	if clientID != "" {
		v.Set("client_id", clientID)
	}
	if code != "" {
		v.Set("pre-authorized_code", code)
	}
	return a.form("/oauth2/token", v, nil)
}

func TestPreAuthorizedCodeIssuesTokenOnce(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, login := a.preAuthorized(nil)
	if login.PreAuthorizedCodeHash == code {
		t.Fatal("kho chỉ giữ băm")
	}
	r := a.preAuthExchange(a.seed.Client.ID, code)
	if r.status != 200 || r.json()["refresh_token"] != nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
	p, err := a.verifier().Verify(context.Background(), r.json()["access_token"].(string))
	if err != nil || p.Project != a.seed.Project.ID || p.Role != fhirauth.RoleAdmin {
		t.Errorf("fhirauth: %+v %v", p, err)
	}
	wantOAuthError(t, a.preAuthExchange(a.seed.Client.ID, code), "invalid_grant", "Token already granted")
	if !domain.IsTrue(a.storedLogin(login.ID).Revoked) {
		t.Error("dùng lại mã phải thu hồi Login")
	}
	if a.get("/oauth2/userinfo", r.json()["access_token"].(string)).status != 401 {
		t.Error("token cấp ở lần 1 phải bị thu hồi")
	}
	wantOAuthError(t, a.preAuthExchange(a.seed.Client.ID, code), "invalid_grant", "Token revoked")
}

func TestPreAuthorizedCodeErrors(t *testing.T) {
	a := newCodeAPI(t, nil)
	id := a.seed.Client.ID
	code, _ := a.preAuthorized(nil)
	wantOAuthError(t, a.preAuthExchange("", code), "invalid_request", "Missing client_id")
	wantOAuthError(t, a.preAuthExchange(id, ""), "invalid_request", "Missing pre-authorized_code")
	wantOAuthError(t, a.preAuthExchange("khong-co", code), "invalid_client", "Invalid client")
	wantOAuthError(t, a.preAuthExchange(id, "sai-ma"), "invalid_request", "Invalid pre-authorized_code")
	// Login có băm đúng nhưng không phải pre-authorized không được dùng làm mã
	pw := domain.GenerateSecret(16)
	a.preAuthorized(func(l *fhir.Login) { l.AuthMethod = "password"; l.PreAuthorizedCodeHash = domain.HashSecret(pw) })
	wantOAuthError(t, a.preAuthExchange(id, pw), "invalid_request", "Invalid pre-authorized_code")
	// client khác
	other, err := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "Hai"})
	if err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.preAuthExchange(other.ID, code), "invalid_request", "Invalid client")
	// không có membership
	nm, _ := a.preAuthorized(func(l *fhir.Login) { l.Membership = nil })
	wantOAuthError(t, a.preAuthExchange(id, nm), "invalid_request", "Invalid profile")
	// các lỗi trên không đốt mã
	if r := a.preAuthExchange(id, code); r.status != 200 {
		t.Errorf("mã vẫn phải dùng được: %d %s", r.status, r.body)
	}
	// client bị tắt
	cl := *a.seed.Client
	cl.Status = "off"
	if _, err := a.c.Store.UpdateResource(context.Background(), &cl, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	c2, _ := a.preAuthorized(nil)
	wantOAuthError(t, a.preAuthExchange(id, c2), "invalid_request", "Invalid client")
}

func TestPreAuthorizedCodeExpiryAndRevocation(t *testing.T) {
	a := newCodeAPI(t, nil)
	id := a.seed.Client.ID
	code, _ := a.preAuthorized(nil)
	noExpiry, _ := a.preAuthorized(func(l *fhir.Login) { l.ExpiresAt = "" })
	wantOAuthError(t, a.preAuthExchange(id, noExpiry), "invalid_grant", "Pre-authorized code expired") // thiếu hạn: fail đóng
	rev, l := a.preAuthorized(nil)
	if err := a.c.Auth.RevokeLogin(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.preAuthExchange(id, rev), "invalid_grant", "Token revoked")
	a.clock.t.Add(601)
	wantOAuthError(t, a.preAuthExchange(id, code), "invalid_grant", "Pre-authorized code expired")
}

func TestPreAuthorizedCodeEnforcesIPRulesWithoutBurningTheCode(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, _ := a.preAuthorized(nil)
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
	wantOAuthError(t, a.preAuthExchange(a.seed.Client.ID, code), "invalid_request", "IP address not allowed")
	// bỏ chặn: cùng mã vẫn dùng được vì lần bị chặn không tiêu thụ nó
	mm := *(updated.(*fhir.ProjectMembership))
	mm.AccessPolicy = nil
	if _, err := a.c.Store.UpdateResource(context.Background(), &mm, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := a.preAuthExchange(a.seed.Client.ID, code); r.status != 200 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func TestPreAuthorizedCodeConcurrentRedemptionYieldsOneToken(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.DatabaseMaxOpenConns = 1 })
	code, _ := a.preAuthorized(nil)
	a.c.Auth.Logins = &barrierPreAuth{LoginRepository: a.c.Auth.Logins, ready: make(chan struct{}), want: 16}
	var wg sync.WaitGroup
	var ok atomic.Int32
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a.preAuthExchange(a.seed.Client.ID, code).status == 200 {
				ok.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d thành công, muốn 1", ok.Load())
	}
}

type barrierPreAuth struct {
	application.LoginRepository
	ready chan struct{}
	want  int32
	seen  atomic.Int32
}

func (b *barrierPreAuth) FindByPreAuthorizedCodeHash(ctx context.Context, h string) (*fhir.Login, error) {
	l, err := b.LoginRepository.FindByPreAuthorizedCodeHash(ctx, h)
	if b.seen.Add(1) == b.want {
		close(b.ready)
	}
	select {
	case <-b.ready:
	case <-time.After(5 * time.Second):
	}
	return l, err
}

func TestRegisterLooksUpStandardClientsOnly(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) {
		s.OAuthRegisterEnabled = true
		s.DefaultOAuthClients = `[{"id":"mcp","name":"MCP","redirectUris":["https://claude.example/callback"]}]`
	})
	post := func(body string) resp { return a.do("POST", "/oauth2/register", body, nil) }
	r := post(`{"redirect_uris":["https://claude.example/callback"]}`)
	m := r.json()
	if r.status != 201 || m["client_id"] != "mcp" || m["client_secret"] != nil || m["client_id_issued_at"] == nil || len(m["redirect_uris"].([]any)) != 1 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got := post(`{"redirect_uris":["http://localhost:9615"]}`).json()["client_id"]; got != domain.CLIClientID {
		t.Errorf("client CLI dựng sẵn: %v", got)
	}
	for name, body := range map[string]string{
		"thiếu": `{}`, "mảng rỗng": `{"redirect_uris":[]}`, "không phải chuỗi": `{"redirect_uris":[1]}`, "JSON hỏng": `{`,
	} {
		if r := post(body); r.status != 400 || r.json()["error_description"] != "Redirect URI is required" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	for _, uri := range []string{"https://claude.example/callback/", "https://claude.example/callback?x=1", "https://evil.example/callback", "https://CLAUDE.example/callback"} {
		if r := post(`{"redirect_uris":["` + uri + `"]}`); r.status != 400 || r.json()["error_description"] != "Invalid redirect URI" {
			t.Errorf("%s: %d %s", uri, r.status, r.body)
		}
	}
	// client trong kho (không phải dựng sẵn) không được "đăng ký" qua đây
	if r := post(`{"redirect_uris":["` + redirectURI + `"]}`); r.status != 400 {
		t.Errorf("client trong kho: %d %s", r.status, r.body)
	}
	if d := a.do("GET", "/.well-known/openid-configuration", "", nil).json(); d["registration_endpoint"] != a.c.Settings.BaseURL+"/oauth2/register" {
		t.Errorf("registration_endpoint: %v", d["registration_endpoint"])
	}
}

func TestRegisterIsOffByDefault(t *testing.T) {
	a := newCodeAPI(t, nil)
	if r := a.do("POST", "/oauth2/register", `{"redirect_uris":["http://localhost:9615"]}`, nil); r.status != 404 && r.status != 405 {
		t.Errorf("mặc định không có /oauth2/register: %d", r.status)
	}
	if _, has := a.do("GET", "/.well-known/openid-configuration", "", nil).json()["registration_endpoint"]; has {
		t.Error("không được khai registration_endpoint")
	}
}
