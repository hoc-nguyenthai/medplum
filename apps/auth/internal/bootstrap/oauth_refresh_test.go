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

	"github.com/golang-jwt/jwt/v5"
)

func (a *codeAPI) refresh(tok string, hdr map[string]string) resp {
	a.t.Helper()
	return a.form("/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok}}, hdr)
}

// tokens đăng nhập (PKCE, client công khai) và đổi code lấy bộ token có refresh token.
func (a *codeAPI) tokens() map[string]any {
	a.t.Helper()
	code, _, _ := a.loginCode(loginOpts{scope: "openid offline_access", challenge: s256(), method: "S256"})
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
	if r.status != 200 || r.json()["refresh_token"] == nil {
		a.t.Fatalf("không lấy được refresh token: %d %s", r.status, r.body)
	}
	return r.json()
}

// confidentialTokens đăng nhập KHÔNG PKCE và đổi code bằng secret của client (client bí mật).
func (a *codeAPI) confidentialTokens() map[string]any {
	a.t.Helper()
	code, _, _ := a.loginCode(loginOpts{scope: "openid offline_access"})
	r := a.exchange(url.Values{"code": {code}}, basic(a.seed.Client.ID, a.seed.Client.Secret))
	if r.status != 200 || r.json()["refresh_token"] == nil {
		a.t.Fatalf("không lấy được refresh token: %d %s", r.status, r.body)
	}
	return r.json()
}

func (a *codeAPI) refreshSecretOf(token string) string {
	a.t.Helper()
	c, err := a.c.Keys.Verify(token)
	if err != nil {
		a.t.Fatal(err)
	}
	return c["refresh_secret"].(string)
}

func (a *codeAPI) loginOfToken(token string) *fhir.Login {
	a.t.Helper()
	c, err := a.c.Keys.Verify(token)
	if err != nil {
		a.t.Fatal(err)
	}
	return a.storedLogin(c["login_id"].(string))
}

func TestRefreshTokenRotatesAndIssuesTokensForFhirServer(t *testing.T) {
	a := newCodeAPI(t, nil)
	first := a.tokens()
	r1 := first["refresh_token"].(string)

	r := a.refresh(r1, nil)
	if r.status != 200 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", r.status, r.hdr, r.body)
	}
	second := r.json()
	if second["token_type"] != "Bearer" || second["scope"] != "openid offline_access" || second["id_token"] == nil || second["refresh_token"] == nil ||
		second["refresh_token"] == r1 || second["access_token"] == first["access_token"] {
		t.Fatalf("body: %v", second)
	}
	if a.refreshSecretOf(second["refresh_token"].(string)) == a.refreshSecretOf(r1) {
		t.Error("refresh secret phải được xoay")
	}
	p, err := a.verifier().Verify(context.Background(), second["access_token"].(string))
	if err != nil || p.Project != a.seed.Project.ID || p.Role != fhirauth.RoleAdmin {
		t.Fatalf("fhirauth: %+v %v", p, err)
	}
	l := a.loginOfToken(r1)
	if l.PreviousRefreshSecret != a.refreshSecretOf(r1) || l.RefreshSecretRotatedAt == "" || l.RefreshSecret != a.refreshSecretOf(second["refresh_token"].(string)) {
		t.Errorf("Login sau xoay: prev=%q rotatedAt=%q", l.PreviousRefreshSecret, l.RefreshSecretRotatedAt)
	}
	// chuỗi A -> B -> C đều dùng được
	r3 := a.refresh(second["refresh_token"].(string), nil)
	if r3.status != 200 {
		t.Fatalf("B -> C: %d %s", r3.status, r3.body)
	}
	// token mới dùng được ở route của auth
	if r := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + r3.json()["access_token"].(string)}); r.status != 200 {
		t.Errorf("access token mới: %d", r.status)
	}
}

func TestRefreshTokenRejectsInvalidTokens(t *testing.T) {
	a := newCodeAPI(t, nil)
	tk := a.tokens()
	refresh := tk["refresh_token"].(string)
	claims, _ := a.c.Keys.Verify(refresh)
	clone := func(mut func(map[string]any)) string {
		c := map[string]any{}
		for k, v := range claims {
			c[k] = v
		}
		mut(c)
		raw, err := a.c.Keys.Sign(c)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims))
	hs.Header["kid"] = a.c.Keys.SigningKID()
	hsRaw, _ := hs.SignedString([]byte("bat-ky-khoa-nao-0123456789abcdef"))
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims(claims))
	none.Header["kid"] = a.c.Keys.SigningKID()
	noneRaw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)

	bad := map[string]string{
		"rỗng": "", "rác": "abc.def.ghi", "alg none": noneRaw, "HS256": hsRaw,
		"payload bị sửa":       refresh[:len(refresh)-3] + "AAA",
		"access token":         tk["access_token"].(string),
		"id token":             tk["id_token"].(string),
		"iss lạ":               clone(func(c map[string]any) { c["iss"] = "http://khac" }),
		"aud của access token": clone(func(c map[string]any) { c["aud"] = a.c.Settings.AccessAudience() }),
		"token_use sai":        clone(func(c map[string]any) { c["token_use"] = "access" }),
		"thiếu refresh_secret": clone(func(c map[string]any) { delete(c, "refresh_secret") }),
		"thiếu login_id":       clone(func(c map[string]any) { delete(c, "login_id") }),
	}
	for _, tok := range bad {
		wantOAuthError(t, a.refresh(tok, nil), "invalid_request", "Invalid refresh token")
	}
	// Login không tồn tại: invalid_grant (Medplum làm handler ném lỗi)
	wantOAuthError(t, a.refresh(clone(func(c map[string]any) { c["login_id"] = "khong-co" }), nil), "invalid_grant", "Invalid refresh token")
	// Login không có refreshSecret (client_credentials): không thể refresh dù có token hợp lệ về mặt chữ ký
	a.clientToken(nil)
	ids, _ := a.c.Auth.Logins.ListIDsByUser(context.Background(), "ClientApplication/"+a.seed.Client.ID)
	forged := clone(func(c map[string]any) { c["login_id"] = ids[0]; c["refresh_secret"] = "x" })
	wantOAuthError(t, a.refresh(forged, nil), "invalid_request", "Invalid refresh token")
	// token vẫn dùng được sau tất cả các lần thử hỏng
	if r := a.refresh(refresh, nil); r.status != 200 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func TestRefreshTokenRevokedLoginAndInactiveMembership(t *testing.T) {
	a := newCodeAPI(t, nil)
	tk := a.tokens()
	refresh := tk["refresh_token"].(string)
	m := *a.seed.Membership
	f := false
	m.Active = &f
	if _, err := a.c.Store.UpdateResource(context.Background(), &m, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := a.refresh(refresh, nil); r.status != 400 || r.json()["error"] != "access_denied" || r.json()["access_token"] != nil {
		t.Errorf("membership tắt: %d %s", r.status, r.body)
	}
	t2 := true
	m.Active = &t2
	if _, err := a.c.Store.UpdateResource(context.Background(), &m, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := a.refresh(refresh, nil); r.status != 200 {
		t.Fatalf("bật lại: %d %s", r.status, r.body)
	}
	if err := a.c.Auth.RevokeLogin(context.Background(), a.loginOfToken(refresh)); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.refresh(refresh, nil), "invalid_grant", "Token revoked")
}

func TestRefreshTokenClientAuthentication(t *testing.T) {
	a := newCodeAPI(t, nil)
	id, secret := a.seed.Client.ID, a.seed.Client.Secret

	// client công khai (đăng nhập qua PKCE): không có secret để gửi, refresh token là bằng chứng
	if r := a.refresh(a.tokens()["refresh_token"].(string), nil); r.status != 200 {
		t.Fatalf("client công khai: %d %s", r.status, r.body)
	}
	// ...nhưng nếu đã gửi secret thì phải đúng
	pub := a.tokens()["refresh_token"].(string)
	wantOAuthError(t, a.refresh(pub, basic(id, "sai")), "invalid_request", "Invalid secret")
	if r := a.refresh(pub, basic(id, secret)); r.status != 200 {
		t.Errorf("secret đúng: %d %s", r.status, r.body)
	}

	// client bí mật (đăng nhập không PKCE, đổi code bằng secret): bắt buộc xác thực (đảo "Known gaps" của Medplum)
	conf := a.confidentialTokens()["refresh_token"].(string)
	wantOAuthError(t, a.refresh(conf, nil), "invalid_grant", "Incorrect client secret")
	wantOAuthError(t, a.refresh(conf, basic(id, "sai")), "invalid_request", "Invalid secret")
	wantOAuthError(t, a.refresh(conf, basic("client-khac", secret)), "invalid_grant", "Incorrect client")
	wantOAuthError(t, a.refresh(conf, map[string]string{"Authorization": "Bearer abc"}), "invalid_request", "Invalid authorization header")
	if r := a.refresh(conf, basic(id, secret)); r.status != 200 {
		t.Fatalf("xác thực đúng: %d %s", r.status, r.body)
	}
	// các lần bị từ chối không làm xoay secret hay thu hồi: token vừa dùng ở trên là token đầu tiên, nên chuỗi phải còn liền mạch
	if l := a.loginOfToken(conf); domain.IsTrue(l.Revoked) {
		t.Error("lỗi xác thực client không được thu hồi Login")
	}
}

func TestRefreshTokenReuseRevokesLoginWhenGraceIsOff(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.RefreshGrace = 0 })
	tk := a.tokens()
	old := tk["refresh_token"].(string)
	next := a.refresh(old, nil)
	if next.status != 200 {
		t.Fatalf("%d %s", next.status, next.body)
	}
	newRefresh, newAccess := next.json()["refresh_token"].(string), next.json()["access_token"].(string)
	wantOAuthError(t, a.refresh(old, nil), "invalid_grant", "Token revoked")
	if !domain.IsTrue(a.loginOfToken(old).Revoked) {
		t.Error("dùng lại token cũ phải thu hồi Login")
	}
	// token xoay sau đó cũng chết, và access token chết ở route của auth
	wantOAuthError(t, a.refresh(newRefresh, nil), "invalid_grant", "Token revoked")
	if r := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + newAccess}); r.status != 401 {
		t.Errorf("access token sau thu hồi: %d", r.status)
	}
}

func TestRefreshTokenGracePeriod(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.TrustedProxies = "127.0.0.0/8" })
	from := func(ip string) map[string]string { return map[string]string{"X-Forwarded-For": ip} }
	old := a.tokens()["refresh_token"].(string)

	rotated := a.form("/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}}, from("1.1.1.1"))
	if rotated.status != 200 {
		t.Fatalf("%d %s", rotated.status, rotated.body)
	}
	currentSecret := a.refreshSecretOf(rotated.json()["refresh_token"].(string))
	versionBefore := a.loginOfToken(old).Meta.VersionId

	// cùng IP, trong ân hạn: chấp nhận, KHÔNG xoay lần nữa, token trả về mang secret hiện hành
	retry := a.form("/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}}, from("1.1.1.1"))
	if retry.status != 200 || a.refreshSecretOf(retry.json()["refresh_token"].(string)) != currentSecret {
		t.Fatalf("ân hạn cùng IP: %d %s", retry.status, retry.body)
	}
	if a.loginOfToken(old).Meta.VersionId != versionBefore {
		t.Error("ân hạn không được ghi Login")
	}
	// IP khác trong cửa sổ: coi là bị đánh cắp, thu hồi
	wantOAuthError(t, a.form("/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}}, from("2.2.2.2")), "invalid_grant", "Token revoked")
	if !domain.IsTrue(a.loginOfToken(old).Revoked) {
		t.Error("dùng từ IP khác phải thu hồi Login")
	}

	// quá ân hạn (cùng IP): thu hồi
	b := newCodeAPI(t, func(s *config.Settings) { s.TrustedProxies = "127.0.0.0/8" })
	old2 := b.tokens()["refresh_token"].(string)
	if r := b.form("/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old2}}, from("1.1.1.1")); r.status != 200 {
		t.Fatal(r.body)
	}
	b.clock.t.Add(31)
	wantOAuthError(t, b.form("/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old2}}, from("1.1.1.1")), "invalid_grant", "Token revoked")
}

// 16 yêu cầu song song cùng một refresh token: đúng một lần xoay, tất cả đều nhận token (cùng secret hiện hành), Login không bị thu hồi.
func TestRefreshTokenConcurrentRefreshRotatesOnce(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.DatabaseMaxOpenConns = 1 })
	old := a.tokens()["refresh_token"].(string)
	// rào chắn buộc cả 16 yêu cầu cùng đọc thấy secret chưa xoay rồi mới cùng đi xoay (xem TestAuthorizationCodeConcurrent...)
	a.c.Auth.Logins = &barrierGet{LoginRepository: a.c.Auth.Logins, ready: make(chan struct{}), want: 16}

	var wg sync.WaitGroup
	var ok atomic.Int32
	secrets := make(chan string, 16)
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := a.refresh(old, nil)
			if r.status != 200 {
				t.Errorf("mọi yêu cầu song song phải thành công: %d %s", r.status, r.body)
				return
			}
			ok.Add(1)
			secrets <- a.refreshSecretOf(r.json()["refresh_token"].(string))
		}()
	}
	close(start)
	wg.Wait()
	close(secrets)
	distinct := map[string]bool{}
	for s := range secrets {
		distinct[s] = true
	}
	if ok.Load() != 16 || len(distinct) != 1 {
		t.Errorf("thành công=%d, số secret khác nhau=%d; muốn 16 và 1 (một lần xoay)", ok.Load(), len(distinct))
	}
	l := a.loginOfToken(old)
	if domain.IsTrue(l.Revoked) || l.PreviousRefreshSecret != a.refreshSecretOf(old) {
		t.Errorf("Login: revoked=%v previous khớp=%v", domain.IsTrue(l.Revoked), l.PreviousRefreshSecret == a.refreshSecretOf(old))
	}
}

// barrierGet làm Logins.Get chờ tới khi đủ `want` lời gọi đã đọc xong (chỉ lần đầu), rồi thả tất cả cùng lúc.
type barrierGet struct {
	application.LoginRepository
	ready chan struct{}
	want  int32
	seen  atomic.Int32
}

func (b *barrierGet) Get(ctx context.Context, id string) (*fhir.Login, error) {
	l, err := b.LoginRepository.Get(ctx, id)
	if n := b.seen.Add(1); n == b.want {
		close(b.ready)
	} else if n < b.want {
		select {
		case <-b.ready:
		case <-time.After(5 * time.Second):
		}
	}
	return l, err
}

func TestRefreshTokenEnforcesIPRulesAndDoesNotRecordBlockedIP(t *testing.T) {
	a := newCodeAPI(t, nil)
	tk := a.tokens()
	refresh := tk["refresh_token"].(string)
	before := a.loginOfToken(refresh)

	pol, err := a.c.Store.CreateResource(context.Background(), &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "chặn", Meta: &fhir.Meta{Project: a.seed.Project.ID},
		IpAccessRule: []fhir.AccessPolicyIpAccessRule{{Value: "127.0.0.1", Action: "block"}}, Resource: []fhir.AccessPolicyResource{{ResourceType: "*"}}})
	if err != nil {
		t.Fatal(err)
	}
	m := *a.seed.Membership
	m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + pol.GetID()}
	if _, err := a.c.Store.UpdateResource(context.Background(), &m, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.refresh(refresh, nil), "invalid_request", "IP address not allowed")
	after := a.loginOfToken(refresh)
	if after.Meta.VersionId != before.Meta.VersionId || after.RefreshSecret != before.RefreshSecret {
		t.Error("IP bị chặn không được làm xoay secret hay ghi Login")
	}
}

func TestRefreshTokenSessionMaxAge(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.SessionMaxAge = time.Hour })
	refresh := a.tokens()["refresh_token"].(string)
	a.clock.t.Add(int64((59 * time.Minute).Seconds()))
	if r := a.refresh(refresh, nil); r.status != 200 {
		t.Fatalf("trong trần: %d %s", r.status, r.body)
	}
	a.clock.t.Add(int64((2 * time.Minute).Seconds()))
	wantOAuthError(t, a.refresh(refresh, nil), "invalid_grant", "Session expired")
}

func TestRefreshTokenKeepsSmartLaunchContext(t *testing.T) {
	a := newCodeAPI(t, nil)
	launch, err := a.c.Store.CreateResource(context.Background(), &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch", Patient: &fhir.Reference{Reference: "Patient/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := a.loginCode(loginOpts{scope: "openid offline_access", challenge: s256(), method: "S256", launch: launch.GetID()})
	tk := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil).json()
	if r := a.refresh(tk["refresh_token"].(string), nil); r.status != 200 || r.json()["patient"] != "p1" {
		t.Errorf("%d %s", r.status, r.body)
	}
}
