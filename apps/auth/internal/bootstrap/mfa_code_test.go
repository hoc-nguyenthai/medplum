package bootstrap

import (
	"context"
	"net/url"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Q8 (phòng thủ chiều sâu): Medplum chỉ ép MFA ở chỗ sendLoginResult không trả code, token endpoint không kiểm mfaVerified. Ở đây, dù một code lọt ra
// bằng đường nào (kho bị sửa, lỗi tương lai), login mật khẩu của user cần MFA mà chưa xác minh thì không đổi được code, và code KHÔNG bị tiêu thụ.
func TestAuthorizationCodeRefusesLoginThatSkippedMFA(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range map[string]func(*fhir.User, *fhir.Project){
		"user đã đăng ký MFA":  func(u *fhir.User, _ *fhir.Project) { u.MfaEnrolled, u.MfaMethod = ptrBool(true), []string{"totp"} },
		"user bị bắt buộc MFA": func(u *fhir.User, _ *fhir.Project) { u.MfaRequired = ptrBool(true) },
	} {
		a := newCodeAPI(t, nil)
		code, loginID, _ := a.loginCode(loginOpts{scope: "openid", challenge: s256(), method: "S256", redirect: redirectURI}) // cấp code TRƯỚC khi có MFA
		admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
		if _, err := a.c.Auth.Users.Mutate(ctx, admin.ID, func(u *fhir.User) error { mutate(u, nil); return nil }); err != nil {
			t.Fatal(err)
		}
		r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}}, nil)
		if r.status != 400 || r.json()["error"] != "invalid_grant" || r.json()["error_description"] != "MFA verification required" || r.json()["access_token"] != nil {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
		if domain.IsTrue(a.storedLogin(loginID).Granted) {
			t.Errorf("%s: code bị từ chối không được bị tiêu thụ", name)
		}
		// khi login đã xác minh MFA thì đổi được
		if _, err := a.c.Auth.Logins.Mutate(ctx, loginID, func(l *fhir.Login) error { l.MfaVerified = ptrBool(true); return nil }); err != nil {
			t.Fatal(err)
		}
		ok := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}}, nil)
		if ok.status != 200 || ok.json()["access_token"] == nil {
			t.Fatalf("%s: login đã xác minh phải đổi được: %d %s", name, ok.status, ok.body)
		}
	}
}

// Project bắt buộc MFA cũng được tính (không chỉ cờ của user).
func TestAuthorizationCodeRefusesWhenProjectRequiresMFA(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, _, _ := a.loginCode(loginOpts{scope: "openid", challenge: s256(), method: "S256", redirect: redirectURI})
	requireMFAInSeedProject(t, a.api)
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}}, nil)
	if r.status != 400 || r.json()["error_description"] != "MFA verification required" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// Login không phải mật khẩu (Google, ngoài) không bị yêu cầu MFA: luật MFA chỉ áp cho đăng nhập mật khẩu.
func TestAuthorizationCodeDoesNotDemandMFAForNonPasswordLogins(t *testing.T) {
	ctx := context.Background()
	a := newCodeAPI(t, nil)
	code, loginID, _ := a.loginCode(loginOpts{scope: "openid", challenge: s256(), method: "S256", redirect: redirectURI})
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	if _, err := a.c.Auth.Users.Mutate(ctx, admin.ID, func(u *fhir.User) error { u.MfaEnrolled, u.MfaMethod = ptrBool(true), []string{"totp"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Auth.Logins.Mutate(ctx, loginID, func(l *fhir.Login) error { l.AuthMethod = domain.AuthGoogle; return nil }); err != nil {
		t.Fatal(err)
	}
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}}, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
