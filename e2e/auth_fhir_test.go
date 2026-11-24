// Package e2e chạy các TIẾN TRÌNH thật: auth (cấp token), fhir-server (nhận token qua JWKS của auth) và, ở ws_split_test.go,
// ws-gateway giữ websocket của Subscription (cùng một Redis). Các test đơn vị đã kiểm từng phần; test này kiểm chỗ nối giữa chúng
// (issuer, audience, JWKS qua mạng, claim fhir_project, scope, fhir_access_policy, token bind websocket, kênh Redis) bằng các binary
// đúng như triển khai.
//
// Chậm (build binary) nên chỉ chạy khi TRIVITA_E2E=1: `make test-e2e`. Redis: miniredis chạy trong tiến trình test (cổng TCP thật cho
// các binary), hoặc Redis THẬT nếu đặt MESSAGING_TEST_REDIS_ADDR.
package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const adminPassword = "mat-khau-admin-123"

type proc struct {
	cmd  *exec.Cmd
	out  *lockedBuf
	done chan struct{} // đóng khi tiến trình đã thoát
	name string
}

// stop gửi SIGINT (tắt êm như SIGTERM của K8s) rồi chờ; quá 8 giây thì kill. Gọi nhiều lần an toàn. Trả true nếu tắt êm.
func (p *proc) stop() bool {
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
		return true
	case <-time.After(8 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		return false
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Binary được build MỘT lần cho cả package (mỗi lần build mất cỡ 15 giây).
var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func build(t *testing.T, root, _, name string) string {
	t.Helper()
	buildOnce.Do(func() {
		if binDir, buildErr = os.MkdirTemp("", "trivita-e2e-bin-"); buildErr != nil {
			return
		}
		for _, n := range []string{"auth", "fhir-server", "ws-gateway", "dicomweb"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, n), "./apps/"+n+"/cmd/"+n)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v\n%s", n, err, out)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(binDir, name)
}

func envList(m map[string]string) []string {
	out := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// start chạy tiến trình nền và đảm bảo dừng nó (chỉ tiến trình do test này tạo) khi test kết thúc.
func start(t *testing.T, bin string, env map[string]string, args ...string) *proc {
	t.Helper()
	p := &proc{cmd: exec.Command(bin, args...), out: &lockedBuf{}, done: make(chan struct{}), name: filepath.Base(bin)}
	p.cmd.Env = envList(env)
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			t.Logf("---- log %s ----\n%s", p.name, p.out.String())
		}
	})
	return p
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(url); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s không sẵn sàng", url)
}

type stack struct {
	t                  *testing.T
	auth, fhir         string // địa chỉ gốc
	clientA, secretA   string
	clientB, secretB   string
	projectA, projectB string
	authProc, fhirProc *proc
	dir                string // thư mục tạm của test (chứa auth.db, fhir.db, khóa...)

	// chỉ có khi newStackWS
	gateway, gatewayWS  string // địa chỉ http và websocket của ws-gateway
	gatewayProc         *proc
	gatewayEnv, fhirEnv map[string]string
	fhirBin, gatewayBin string
	redisAddr           string
	wsKey               string
}

var seedField = regexp.MustCompile(`(?m)^(\w+): (.+)$`)

func seed(t *testing.T, bin string, env map[string]string, name, email string) map[string]string {
	t.Helper()
	cmd := exec.Command(bin, "seed", "project", "-name", name, "-admin-email", email, "-admin-first", "Admin", "-admin-last", name)
	e := map[string]string{"AUTH_SEED_ADMIN_PASSWORD": adminPassword}
	for k, v := range env {
		e[k] = v
	}
	cmd.Env = envList(e)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("seed %s: %v\n%s", name, err, out)
	}
	m := map[string]string{}
	for _, f := range seedField.FindAllStringSubmatch(string(out), -1) {
		m[f[1]] = strings.TrimSpace(f[2])
	}
	if m["clientId"] == "" || m["clientSecret"] == "" || m["projectId"] == "" {
		t.Fatalf("seed %s: không đọc được kết quả:\n%s", name, out)
	}
	return m
}

func newStack(t *testing.T) *stack { return newStackWith(t, false) }

// newStackWS dựng cả ba tiến trình: auth, fhir-server ở FHIR_WS_MODE=issuer và ws-gateway, cùng một Redis.
func newStackWS(t *testing.T) *stack { return newStackWith(t, true) }

func newStackWith(t *testing.T, withWS bool) *stack { return newStackCustom(t, withWS, nil, nil) }

// newStackCustom cho phép thêm biến môi trường cho auth và chạy một hook SAU khi seed, TRƯỚC khi auth khởi động (ví dụ `auth domain add`).
func newStackCustom(t *testing.T, withWS bool, extraAuthEnv map[string]string, before func(authBin string, authEnv map[string]string)) *stack {
	t.Helper()
	return newStackFull(t, withWS, extraAuthEnv, nil, before)
}

// newStackFull thêm biến môi trường cho cả fhir-server. Giá trị của extraAuthEnv có thể chứa "{FHIR}" (địa chỉ gốc của fhir-server, chỉ biết
// sau khi cấp cổng) và "{DIR}" (thư mục tạm của test); extraFhirEnv cũng nhận "{DIR}".
func newStackFull(t *testing.T, withWS bool, extraAuthEnv, extraFhirEnv map[string]string, before func(authBin string, authEnv map[string]string)) *stack {
	t.Helper()
	if os.Getenv("TRIVITA_E2E") != "1" {
		t.Skip("đặt TRIVITA_E2E=1 (make test-e2e) để chạy test nhiều tiến trình")
	}
	root, dir := repoRoot(t), t.TempDir()
	authBin, fhirBin := build(t, root, dir, "auth"), build(t, root, dir, "fhir-server")
	aPort, fPort := freePort(t), freePort(t)
	s := &stack{t: t, dir: dir, auth: fmt.Sprintf("http://127.0.0.1:%d", aPort), fhir: fmt.Sprintf("http://127.0.0.1:%d", fPort), fhirBin: fhirBin}
	subst := func(m map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			out[k] = strings.NewReplacer("{FHIR}", s.fhir, "{DIR}", dir).Replace(v)
		}
		return out
	}
	extraAuthEnv, extraFhirEnv = subst(extraAuthEnv), subst(extraFhirEnv)

	keys := filepath.Join(dir, "keys")
	authEnv := map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", aPort), "DATABASE_URL": "sqlite://" + filepath.Join(dir, "auth.db"), "ENVIRONMENT": "local",
		"AUTH_BASE_URL": s.auth, "AUTH_SIGNING_KEY_PATH": keys, "AUTH_PWNED_ENABLED": "false", "AUTH_COOKIE_SECURE": "false",
		"AUTH_APP_BASE_URL": "http://localhost:3000", "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "false",
	}
	gen := exec.Command(authBin, "keys", "generate", "-alg", "ES256")
	gen.Env = envList(authEnv)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("keys generate: %v\n%s", err, out)
	}
	for k, v := range extraAuthEnv { // cấu hình bổ sung cũng áp cho lệnh seed (ví dụ AUTH_DEFAULT_PROJECT_FEATURES)
		authEnv[k] = v
	}
	a := seed(t, authBin, authEnv, "Alpha", "a@example.org")
	b := seed(t, authBin, authEnv, "Beta", "b@example.org")
	s.clientA, s.secretA, s.projectA = a["clientId"], a["clientSecret"], a["projectId"]
	s.clientB, s.secretB, s.projectB = b["clientId"], b["clientSecret"], b["projectId"]
	for k, v := range extraAuthEnv {
		authEnv[k] = v
	}
	if before != nil {
		before(authBin, authEnv)
	}

	s.authProc = start(t, authBin, authEnv)
	waitReady(t, s.auth+"/health/live")

	key := sha256.Sum256([]byte("khoa-ky-job-e2e"))
	fhirEnv := map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", fPort), "DATABASE_URL": "sqlite://" + filepath.Join(dir, "fhir.db"), "ENVIRONMENT": "local",
		"FHIR_BASE_URL": s.fhir + "/fhir/R4", "FHIR_JWT_ISSUER": s.auth, "FHIR_JWT_AUDIENCE": s.auth, "FHIR_JWKS_URL": s.auth + "/.well-known/jwks.json",
		"FHIR_JOB_SIGNING_KEY": base64.RawURLEncoding.EncodeToString(key[:]), "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "false",
	}
	if withWS {
		s.addWebSocketSplit(t, dir, fhirEnv)
	}
	for k, v := range extraFhirEnv {
		fhirEnv[k] = v
	}
	if _, set := extraFhirEnv["DATABASE_URL"]; !set {
		var rnd [6]byte
		_, _ = rand.Read(rnd[:])
		if u := pgSchemaURL(t, "e2e_fhir_"+hex.EncodeToString(rnd[:])); u != "" { // E2E_POSTGRES_URL: chạy fhir-server trên PostgreSQL thật thay cho SQLite
			fhirEnv["DATABASE_URL"] = u
		}
	}
	s.fhirEnv = fhirEnv
	s.fhirProc = start(t, fhirBin, fhirEnv)
	waitReady(t, s.fhir+"/healthz")
	if withWS {
		waitReady(t, s.gateway+"/health/ready")
	}
	return s
}

type resp struct {
	status int
	body   []byte
	hdr    http.Header
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("không phải JSON (%d): %s", r.status, r.body)
	}
	return m
}

func do(t *testing.T, method, u, token, ctype string, body io.Reader) resp {
	t.Helper()
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b, res.Header}
}

func (s *stack) fhirGet(token, path string) resp {
	return do(s.t, "GET", s.fhir+"/fhir/R4"+path, token, "", nil)
}

func (s *stack) fhirPost(token, path, body string) resp {
	return do(s.t, "POST", s.fhir+"/fhir/R4"+path, token, "application/fhir+json", strings.NewReader(body))
}

func (s *stack) token(form url.Values) resp {
	return do(s.t, "POST", s.auth+"/oauth2/token", "", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
}

func (s *stack) clientCredentials(id, secret, scope string) string {
	s.t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secret}}
	if scope != "" {
		form.Set("scope", scope)
	}
	r := s.token(form)
	if r.status != 200 {
		s.t.Fatalf("client_credentials: %d %s", r.status, r.body)
	}
	return r.json(s.t)["access_token"].(string)
}

const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

// signIn đi hết chuỗi của một SPA: /auth/login (PKCE) rồi đổi code ở /oauth2/token.
func (s *stack) signIn(email, clientID string) map[string]any {
	s.t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": adminPassword, "clientId": clientID, "scope": "openid offline_access",
		"codeChallenge": base64.RawURLEncoding.EncodeToString(sum[:]), "codeChallengeMethod": "S256",
	})
	r := do(s.t, "POST", s.auth+"/auth/login", "", "application/json", bytes.NewReader(body))
	code, _ := r.json(s.t)["code"].(string)
	if r.status != 200 || code == "" {
		s.t.Fatalf("login: %d %s", r.status, r.body)
	}
	tr := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID}})
	if tr.status != 200 {
		s.t.Fatalf("đổi code: %d %s", tr.status, tr.body)
	}
	return tr.json(s.t)
}

const patientJSON = `{"resourceType":"Patient","name":[{"family":"E2E"}],"gender":"female"}`

func TestAuthIssuesTokensThatFhirServerAcceptsAndEnforces(t *testing.T) {
	s := newStack(t)

	// discovery của auth khớp cấu hình mà fhir-server đang dùng
	disco := do(t, "GET", s.auth+"/.well-known/openid-configuration", "", "", nil).json(t)
	if disco["issuer"] != s.auth || disco["jwks_uri"] != s.auth+"/.well-known/jwks.json" {
		t.Fatalf("discovery: %v", disco)
	}

	// đăng nhập như một SPA rồi gọi fhir-server bằng chính access token đó
	tk := s.signIn("a@example.org", s.clientA)
	access := tk["access_token"].(string)
	created := s.fhirPost(access, "/Patient", patientJSON)
	if created.status != 201 {
		t.Fatalf("tạo Patient: %d %s", created.status, created.body)
	}
	id := created.json(t)["id"].(string)
	if r := s.fhirGet(access, "/Patient/"+id); r.status != 200 {
		t.Errorf("đọc Patient: %d %s", r.status, r.body)
	}
	if got := s.fhirGet(access, "/Patient?family=E2E").json(t); got["total"] == nil && got["entry"] == nil {
		t.Errorf("tìm Patient: %v", got)
	}

	// cách ly project: client của project B không thấy dữ liệu của A
	b := s.clientCredentials(s.clientB, s.secretB, "")
	if r := s.fhirGet(b, "/Patient/"+id); r.status != 404 {
		t.Errorf("project B đọc Patient của A: %d %s", r.status, r.body)
	}
	if entries, _ := s.fhirGet(b, "/Patient").json(t)["entry"].([]any); len(entries) != 0 {
		t.Errorf("project B thấy %d Patient của A", len(entries))
	}
	if r := s.fhirPost(b, "/Patient", patientJSON); r.status != 201 {
		t.Errorf("project B tạo Patient của mình: %d %s", r.status, r.body)
	}

	// scope SMART do client xin được tôn trọng: chỉ đọc Observation, Patient bị cấm
	smart := s.clientCredentials(s.clientA, s.secretA, "system/Observation.rs")
	if r := s.fhirGet(smart, "/Observation"); r.status != 200 {
		t.Errorf("Observation: %d %s", r.status, r.body)
	}
	if r := s.fhirGet(smart, "/Patient/"+id); r.status != 403 {
		t.Errorf("Patient với scope chỉ Observation: %d %s", r.status, r.body)
	}
	if r := s.fhirPost(smart, "/Observation", `{"resourceType":"Observation","status":"final","code":{"text":"x"}}`); r.status != 403 {
		t.Errorf("ghi với scope chỉ đọc: %d", r.status)
	}

	// scope do client khai không bao giờ thành quyền admin
	if r := s.token(url.Values{"grant_type": {"client_credentials"}, "client_id": {s.clientA}, "client_secret": {s.secretA}, "scope": {"fhir.admin"}}); r.status != 400 {
		t.Errorf("xin fhir.admin: %d %s", r.status, r.body)
	}

	// chính sách mặc định của client: ghi dữ liệu lâm sàng được, nhưng ClientApplication (có secret) bị cấm
	client := s.clientCredentials(s.clientA, s.secretA, "")
	if r := s.fhirPost(client, "/Patient", patientJSON); r.status != 201 {
		t.Errorf("client ghi Patient: %d %s", r.status, r.body)
	}
	if r := s.fhirGet(client, "/ClientApplication"); r.status != 403 {
		t.Errorf("đọc ClientApplication: %d %s", r.status, r.body)
	}

	// refresh token: access token mới cũng được fhir-server nhận
	refreshed := s.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk["refresh_token"].(string)}, "client_id": {s.clientA}})
	if refreshed.status != 200 {
		t.Fatalf("refresh: %d %s", refreshed.status, refreshed.body)
	}
	if r := s.fhirGet(refreshed.json(t)["access_token"].(string), "/Patient/"+id); r.status != 200 {
		t.Errorf("access token sau refresh: %d", r.status)
	}
}

func TestFhirServerRejectsForgedAndForeignTokens(t *testing.T) {
	s := newStack(t)
	good := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	if r := s.fhirGet(good, "/Patient"); r.status != 200 {
		t.Fatalf("token thật: %d", r.status)
	}
	claims := func() jwt.MapClaims {
		parsed, _, _ := jwt.NewParser().ParseUnverified(good, jwt.MapClaims{})
		return parsed.Claims.(jwt.MapClaims)
	}
	header, _, _ := jwt.NewParser().ParseUnverified(good, jwt.MapClaims{})
	kid := header.Header["kid"].(string)

	rogue, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sign := func(m jwt.SigningMethod, key any, mut func(jwt.MapClaims)) string {
		c := claims()
		if mut != nil {
			mut(c)
		}
		tok := jwt.NewWithClaims(m, c)
		tok.Header["kid"] = kid
		raw, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"`+s.auth+`","aud":"`+s.auth+`","sub":"x","fhir_project":"khac","scope":"fhir.admin","exp":4102444800}`)) + "." + parts[2]
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims())
	none.Header["kid"] = kid
	noneRaw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)

	for name, tok := range map[string]string{
		"không token": "",
		"rác":         "abc.def.ghi",
		"ký bằng khóa lạ, mượn kid thật":   sign(jwt.SigningMethodES256, rogue, nil),
		"payload bị sửa, giữ chữ ký":       tampered,
		"alg none":                         noneRaw,
		"HS256 ký bằng chuỗi bất kỳ":       sign(jwt.SigningMethodHS256, []byte("bat-ky-khoa-nao-0123456789abcdef"), nil),
		"khóa lạ nhưng issuer và aud đúng": sign(jwt.SigningMethodES256, rogue, func(c jwt.MapClaims) { c["scope"] = "fhir.admin" }),
	} {
		if r := s.fhirGet(tok, "/Patient"); r.status != 401 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// token thật nhưng loại khác (id token, refresh token) không dùng được làm access token của fhir-server
	tk := s.signIn("a@example.org", s.clientA)
	for name, other := range map[string]string{"id token": tk["id_token"].(string), "refresh token": tk["refresh_token"].(string)} {
		if r := s.fhirGet(other, "/Patient"); r.status == 200 {
			t.Errorf("%s dùng được ở fhir-server: %d", name, r.status)
		}
	}
}

// Giới hạn ĐÃ BIẾT (quyết định D-13): fhir-server chỉ xác minh chữ ký, không tra Login, nên sau khi đăng xuất auth từ chối token ngay
// nhưng fhir-server vẫn nhận nó cho tới khi access token hết hạn (AUTH_ACCESS_TTL, mặc định 15 phút). Test này ghi lại hành vi đó
// để nếu sau này đóng cửa sổ (introspect hoặc danh sách thu hồi) thì phải sửa test và tài liệu có chủ đích.
func TestKnownRevocationWindowBetweenAuthAndFhirServer(t *testing.T) {
	s := newStack(t)
	access := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	if r := do(t, "POST", s.auth+"/oauth2/logout", access, "", nil); r.status != 200 {
		t.Fatalf("logout: %d %s", r.status, r.body)
	}
	if r := do(t, "GET", s.auth+"/oauth2/userinfo", access, "", nil); r.status != 401 {
		t.Errorf("auth phải từ chối token đã đăng xuất: %d", r.status)
	}
	if r := s.fhirGet(access, "/Patient"); r.status != 200 {
		t.Errorf("fhir-server vẫn nhận token đã đăng xuất cho tới khi hết hạn (cửa sổ thu hồi đã biết): %d", r.status)
	}
	// introspect của auth báo đúng ngay (dành cho nơi cần kiểm chặt hơn)
	other := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	body := url.Values{"token": {access}}.Encode()
	r := do(t, "POST", s.auth+"/oauth2/introspect", other, "application/x-www-form-urlencoded", strings.NewReader(body))
	if r.json(t)["active"] != false {
		t.Errorf("introspect phải báo inactive: %s", r.body)
	}
}
