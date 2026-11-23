package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var setPasswordLink = regexp.MustCompile(`http://localhost:3000/setpassword/([0-9a-f-]{36})/([0-9a-f]{32})`)

func (s *stack) postJSON(path string, v any) resp {
	s.t.Helper()
	b, _ := json.Marshal(v)
	return do(s.t, "POST", s.auth+path, "", "application/json", bytes.NewReader(b))
}

// Chuỗi đăng ký đầy đủ qua các tiến trình thật: newuser -> newproject -> đổi code lấy token -> gọi fhir-server bằng token đó. Dùng client
// dựng sẵn không thuộc project nào (medplum-cli), vì project chưa tồn tại lúc đăng ký.
func TestSignUpNewProjectThenUseFhirServer(t *testing.T) {
	s := newStack(t)
	const email, pw = "chu-moi@example.org", "mat-khau-dai-456"

	nu := s.postJSON("/auth/newuser", map[string]any{"firstName": "Chu", "lastName": "Moi", "email": email, "password": pw, "clientId": "medplum-cli", "scope": "openid offline_access",
		"codeChallenge": challengeS256(), "codeChallengeMethod": "S256"})
	login, _ := nu.json(t)["login"].(string)
	if nu.status != 200 || login == "" || nu.json(t)["code"] != nil {
		t.Fatalf("newuser: %d %s", nu.status, nu.body)
	}
	// chưa có project thì chưa có code: không có đường nào lấy token từ newuser
	if r := s.postJSON("/auth/newuser", map[string]any{"firstName": "Chu", "lastName": "Moi", "email": email, "password": pw}); r.status != 400 {
		t.Errorf("trùng email: %d %s", r.status, r.body)
	}

	np := s.postJSON("/auth/newproject", map[string]any{"login": login, "projectName": "Phòng khám Mới"})
	code, _ := np.json(t)["code"].(string)
	if np.status != 200 || code == "" {
		t.Fatalf("newproject: %d %s", np.status, np.body)
	}
	if r := s.postJSON("/auth/newproject", map[string]any{"login": login, "projectName": "Phòng khám Mới"}); r.status != 400 {
		t.Errorf("dùng lại login: %d %s", r.status, r.body)
	}

	tr := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"medplum-cli"}, "code_verifier": {verifier}})
	if tr.status != 200 {
		t.Fatalf("đổi code: %d %s", tr.status, tr.body)
	}
	access := tr.json(t)["access_token"].(string)
	created := s.fhirPost(access, "/Patient", patientJSON)
	if created.status != 201 {
		t.Fatalf("tạo Patient trong project mới: %d %s", created.status, created.body)
	}
	id := created.json(t)["id"].(string)

	// project mới tách biệt: không thấy dữ liệu của Alpha và ngược lại
	alpha := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	alphaPatient := s.fhirPost(alpha, "/Patient", patientJSON).json(t)["id"].(string)
	if r := s.fhirGet(access, "/Patient/"+alphaPatient); r.status != 404 {
		t.Errorf("project mới đọc Patient của Alpha: %d", r.status)
	}
	if r := s.fhirGet(alpha, "/Patient/"+id); r.status != 404 {
		t.Errorf("Alpha đọc Patient của project mới: %d", r.status)
	}
	// admin của project mới đọc được hồ sơ của chính mình nhưng không đụng được project khác qua token
	if r := s.fhirGet(access, "/Patient/"+id); r.status != 200 {
		t.Errorf("đọc lại: %d", r.status)
	}
	// code dùng một lần
	if r := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"medplum-cli"}, "code_verifier": {verifier}}); r.status == 200 {
		t.Errorf("code dùng lại được: %s", r.body)
	}
}

// Quên mật khẩu qua các tiến trình thật. Thư của provider `log` đi vào log của auth (chỉ ở local), nên test đọc link từ đó.
func TestResetPasswordThroughRealProcesses(t *testing.T) {
	s := newStack(t)
	const email = "a@example.org" // admin của project Alpha (seed)

	old := s.signIn(email, s.clientA)
	oldRefresh, oldAccess := old["refresh_token"].(string), old["access_token"].(string)

	// email lạ và email thật trả CÙNG phản hồi
	known := s.postJSON("/auth/resetpassword", map[string]any{"email": email})
	unknown := s.postJSON("/auth/resetpassword", map[string]any{"email": "khong-co@example.org"})
	if known.status != 200 || known.status != unknown.status || string(known.body) != string(unknown.body) {
		t.Fatalf("phản hồi khác nhau:\n%d %s\n%d %s", known.status, known.body, unknown.status, unknown.body)
	}
	var link []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && link == nil; time.Sleep(50 * time.Millisecond) {
		link = lastMatch(setPasswordLink, s.authProc.out.String())
	}
	if link == nil {
		t.Fatalf("không thấy link đặt lại mật khẩu trong log của auth:\n%s", s.authProc.out.String())
	}

	const newPw = "mat-khau-moi-789"
	if r := s.postJSON("/auth/setpassword", map[string]any{"id": link[1], "secret": strings.Repeat("0", 32), "password": newPw}); r.status != 400 {
		t.Errorf("secret sai: %d %s", r.status, r.body)
	}
	if r := s.postJSON("/auth/setpassword", map[string]any{"id": link[1], "secret": link[2], "password": newPw}); r.status != 200 {
		t.Fatalf("setpassword: %d %s", r.status, r.body)
	}
	if r := s.postJSON("/auth/setpassword", map[string]any{"id": link[1], "secret": link[2], "password": "mat-khau-khac-1"}); r.status != 400 {
		t.Errorf("link dùng lại: %d %s", r.status, r.body)
	}

	// phiên cũ bị thu hồi: refresh token chết; access token còn sống tới khi hết hạn (cửa sổ D-13 đã biết, xem test riêng)
	if r := s.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh}, "client_id": {s.clientA}}); r.status == 200 {
		t.Errorf("refresh token cũ vẫn dùng được sau khi đặt lại mật khẩu: %s", r.body)
	}
	_ = oldAccess
	// mật khẩu cũ không đăng nhập được, mật khẩu mới thì được
	if r := s.postJSON("/auth/login", map[string]any{"email": email, "password": adminPassword, "clientId": s.clientA, "scope": "openid"}); r.status == 200 && r.json(t)["code"] != nil {
		t.Errorf("mật khẩu cũ còn đăng nhập được: %s", r.body)
	}
	r := s.postJSON("/auth/login", map[string]any{"email": email, "password": newPw, "clientId": s.clientA, "scope": "openid", "codeChallenge": "x", "codeChallengeMethod": "plain"})
	if r.status != 200 || r.json(t)["code"] == nil {
		t.Errorf("mật khẩu mới: %d %s", r.status, r.body)
	}
}

func challengeS256() string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func lastMatch(re *regexp.Regexp, s string) []string {
	all := re.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}
