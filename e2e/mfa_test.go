package e2e

import (
	"bytes"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/pquerna/otp"
	pq "github.com/pquerna/otp/totp"
)

func e2eTotp(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := pq.GenerateCodeCustom(secret, at, pq.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// MFA trên tiến trình thật: người dùng tự đăng ký, tạo project, lấy token THẬT của auth, đăng ký TOTP qua route Bearer, rồi đăng nhập lại và bị
// buộc qua MFA; mã dùng một lần; sai 5 lần thì login bị thu hồi.
func TestMFAThroughRealProcesses(t *testing.T) {
	s := newStack(t)
	const email, pw = "mfa@example.org", "mat-khau-dai-456"
	cli := map[string]any{"clientId": "medplum-cli", "scope": "openid offline_access", "codeChallenge": challengeS256(), "codeChallengeMethod": "S256"}
	merge := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range cli {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	nu := s.postJSON("/auth/newuser", merge(map[string]any{"firstName": "M", "lastName": "F", "email": email, "password": pw}))
	login, _ := nu.json(t)["login"].(string)
	np := s.postJSON("/auth/newproject", map[string]any{"login": login, "projectName": "Phòng khám MFA"})
	code, _ := np.json(t)["code"].(string)
	if nu.status != 200 || np.status != 200 || code == "" {
		t.Fatalf("đăng ký: %d %s / %d %s", nu.status, nu.body, np.status, np.body)
	}
	tr := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"medplum-cli"}, "code_verifier": {verifier}})
	if tr.status != 200 {
		t.Fatalf("đổi code: %d %s", tr.status, tr.body)
	}
	access := tr.json(t)["access_token"].(string)

	// chưa đăng ký MFA: status cho enrollUri; mã sai bị từ chối
	st := do(t, "GET", s.auth+"/auth/mfa/status", access, "", nil)
	sm := st.json(t)
	if st.status != 200 || sm["enrolled"] != false || sm["enrollUri"] == nil {
		t.Fatalf("status: %d %s", st.status, st.body)
	}
	u, err := url.Parse(sm["enrollUri"].(string))
	if err != nil || u.Query().Get("secret") == "" {
		t.Fatalf("enrollUri: %v", sm["enrollUri"])
	}
	secret := u.Query().Get("secret")
	if r := s.bearerJSON(access, "/auth/mfa/enroll", map[string]any{"method": "totp", "token": "000000"}); r.status != 400 {
		t.Errorf("mã sai: %d %s", r.status, r.body)
	}
	at := time.Now()
	if r := s.bearerJSON(access, "/auth/mfa/enroll", map[string]any{"method": "totp", "token": e2eTotp(t, secret, at)}); r.status != 200 {
		t.Fatalf("enroll: %d %s", r.status, r.body)
	}
	// không có token thì route Bearer từ chối
	if r := s.postJSON("/auth/mfa/disable", map[string]any{"token": "123456"}); r.status != 401 {
		t.Errorf("disable không Bearer: %d", r.status)
	}

	// đăng nhập lại: bị buộc qua MFA, KHÔNG có code
	l2 := s.postJSON("/auth/login", merge(map[string]any{"email": email, "password": pw}))
	if l2.status != 200 || l2.json(t)["mfaRequired"] != true || l2.json(t)["code"] != nil {
		t.Fatalf("login phải đòi MFA: %d %s", l2.status, l2.body)
	}
	id2 := l2.json(t)["login"].(string)
	// mã đã dùng ở bước đăng ký vẫn dùng được (enroll không tiêu thụ), nhưng chỉ MỘT lần
	v := s.postJSON("/auth/mfa/verify", map[string]any{"login": id2, "token": e2eTotp(t, secret, at)})
	if v.status != 200 || v.json(t)["code"] == nil {
		t.Fatalf("verify: %d %s", v.status, v.body)
	}
	tr2 := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {v.json(t)["code"].(string)}, "client_id": {"medplum-cli"}, "code_verifier": {verifier}})
	if tr2.status != 200 {
		t.Fatalf("đổi code sau MFA: %d %s", tr2.status, tr2.body)
	}
	l3 := s.postJSON("/auth/login", merge(map[string]any{"email": email, "password": pw})).json(t)["login"].(string)
	if r := s.postJSON("/auth/mfa/verify", map[string]any{"login": l3, "token": e2eTotp(t, secret, at)}); r.status != 400 {
		t.Errorf("mã đã dùng không được dùng lại: %d %s", r.status, r.body)
	}
	// sai 5 lần: login bị thu hồi, mã đúng cũng không cứu được
	for i := 0; i < 5; i++ {
		if r := s.postJSON("/auth/mfa/verify", map[string]any{"login": l3, "token": "000000"}); r.status != 400 {
			t.Fatalf("lần sai %d: %d %s", i+1, r.status, r.body)
		}
	}
	r := s.postJSON("/auth/mfa/verify", map[string]any{"login": l3, "token": e2eTotp(t, secret, at.Add(30*time.Second))})
	if r.status != 400 || len(r.body) == 0 {
		t.Fatalf("login đã thu hồi: %d %s", r.status, r.body)
	}
	if got := r.json(t)["issue"].([]any)[0].(map[string]any)["details"].(map[string]any)["text"]; got != "Login revoked" {
		t.Errorf("thông báo: %v", got)
	}
}

func (s *stack) bearerJSON(token, path string, v any) resp {
	s.t.Helper()
	b, _ := json.Marshal(v)
	return do(s.t, "POST", s.auth+path, token, "application/json", bytes.NewReader(b))
}
