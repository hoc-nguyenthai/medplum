package binarystore_test

import (
	"bufio"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

type vector struct {
	Method, Base, ID, Ver, Project string
	Expires                        int64
	Signed, URL                    string
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	b, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []vector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	return vs
}

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	b, err := os.ReadFile("testdata/test-signing-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	k, err := binarystore.ParsePrivateKeyPEM(b)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

const storageBase = "https://api.example.com/storage"

func newSigner(t *testing.T, mut func(*binarystore.SignerConfig)) (*binarystore.Signer, *fixedClock) {
	t.Helper()
	clk := &fixedClock{time.Unix(1_800_000_000, 0)}
	cfg := binarystore.SignerConfig{BaseURL: storageBase, Key: testKey(t), Clock: clk}
	if mut != nil {
		mut(&cfg)
	}
	s, err := binarystore.NewSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, clk
}

func req(method, rawurl string) *http.Request {
	u, _ := url.Parse(rawurl)
	r := httptest.NewRequest(method, u.RequestURI(), nil)
	r.Host = u.Host
	return r
}

// Vector sinh bằng Node (cùng thuật toán với Medplum): Go phải xác minh được và ký ra đúng từng byte.
func TestSignerMatchesNodeVectors(t *testing.T) {
	for i, v := range loadVectors(t) {
		t.Run(fmt.Sprintf("%d-%s", i, v.Method), func(t *testing.T) {
			s, clk := newSigner(t, func(c *binarystore.SignerConfig) { c.BaseURL = v.Base })
			clk.t = time.Unix(v.Expires, 0).Add(-time.Hour)
			c, err := s.Verify(v.Method, req(v.Method, v.URL))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if c.ID != v.ID || c.VersionID != v.Ver || c.ProjectID != v.Project || c.Method != v.Method || c.Expires.Unix() != v.Expires {
				t.Fatalf("claims = %+v", c)
			}
			got, err := s.Sign(v.Method, v.ID, v.Ver, v.Project)
			if err != nil {
				t.Fatal(err)
			}
			if got != v.URL {
				t.Fatalf("Sign khác Node:\n go: %s\nnode: %s", got, v.URL)
			}
		})
	}
}

func TestSignerRoundTrip(t *testing.T) {
	s, _ := newSigner(t, nil)
	for _, method := range []string{"GET", "PUT"} {
		for _, project := range []string{"", "proj-1"} {
			u, err := s.Sign(method, "bin1", "ver2", project)
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.Verify(method, req(method, u))
			if err != nil || c.ID != "bin1" || c.VersionID != "ver2" || c.ProjectID != project || c.Method != method {
				t.Fatalf("%s/%q: %+v, %v", method, project, c, err)
			}
		}
	}
}

func TestSignedURLShape(t *testing.T) {
	s, clk := newSigner(t, nil)
	u, _ := s.Sign("GET", "b", "v", "p1")
	exp := clk.t.Add(time.Hour).Unix()
	if !strings.HasPrefix(u, fmt.Sprintf("%s/b/v?Expires=%d&Project=p1&Signature=", storageBase, exp)) {
		t.Fatalf("thứ tự tham số: %s", u)
	}
	u2, _ := s.Sign("GET", "b", "v", "")
	if strings.Contains(u2, "Project") || !strings.HasPrefix(u2, fmt.Sprintf("%s/b/v?Expires=%d&Signature=", storageBase, exp)) {
		t.Fatalf("không project: %s", u2)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	s, _ := newSigner(t, nil)
	get, _ := s.Sign("GET", "bin1", "ver1", "proj1")
	put, _ := s.Sign("PUT", "bin1", "ver1", "proj1")
	cases := []struct {
		name, method, url string
		want              error
	}{
		{"method swap GET→PUT", "PUT", get, binarystore.ErrBadSignature},
		{"method swap PUT→GET", "GET", put, binarystore.ErrBadSignature},
		{"project changed", "GET", strings.Replace(get, "Project=proj1", "Project=proj2", 1), binarystore.ErrBadSignature},
		{"project removed", "GET", strings.Replace(get, "&Project=proj1", "", 1), binarystore.ErrBadSignature},
		{"project added to unscoped", "GET", func() string {
			u, _ := s.Sign("GET", "bin1", "ver1", "")
			return strings.Replace(u, "&Signature", "&Project=x&Signature", 1)
		}(), binarystore.ErrBadSignature},
		{"id changed", "GET", strings.Replace(get, "/bin1/", "/bin2/", 1), binarystore.ErrBadSignature},
		{"version changed", "GET", strings.Replace(get, "/ver1?", "/ver2?", 1), binarystore.ErrBadSignature},
		{"extra param", "GET", get + "&Extra=1", binarystore.ErrBadSignature},
		{"signature garbage", "GET", get[:strings.Index(get, "Signature=")] + "Signature=xyz", binarystore.ErrBadSignature},
		{"signature not base64", "GET", get[:strings.Index(get, "Signature=")] + "Signature=%21%21", binarystore.ErrBadSignature},
		{"signature flipped", "GET", flipSig(get), binarystore.ErrBadSignature},
		{"missing signature", "GET", get[:strings.Index(get, "&Signature=")], binarystore.ErrMissingSignature},
		{"empty signature", "GET", get[:strings.Index(get, "Signature=")] + "Signature=", binarystore.ErrMissingSignature},
		{"duplicate signature", "GET", get + "&Signature=abc", binarystore.ErrBadSignature},
		{"duplicate project", "GET", strings.Replace(get, "&Signature", "&Project=proj1&Signature", 1), binarystore.ErrBadSignature},
		{"duplicate expires", "GET", strings.Replace(get, "&Signature", "&Expires=1&Signature", 1), binarystore.ErrBadSignature},
		{"bad query escape", "GET", get + "&%zz=1", binarystore.ErrBadSignature},
		{"method DELETE", "DELETE", get, binarystore.ErrBadSignature},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.method
			if m == "DELETE" {
				m = "GET"
			}
			_, err := s.Verify(c.method, mustReq(t, m, c.url))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, muốn %v", err, c.want)
			}
		})
	}
}

func mustReq(t *testing.T, method, rawurl string) *http.Request {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest(method, rawurl, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.RequestURI = u.RequestURI()
	return r
}

func flipSig(u string) string {
	i := strings.Index(u, "Signature=") + len("Signature=")
	b := []byte(u)
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func TestVerifyExpiry(t *testing.T) {
	s, clk := newSigner(t, nil)
	u, _ := s.Sign("GET", "b", "v", "")
	exp := clk.t.Add(time.Hour)
	for _, c := range []struct {
		name string
		now  time.Time
		ok   bool
	}{
		{"đầu", clk.t, true},
		{"sát hạn 1 giây", exp.Add(-time.Second), true},
		{"đúng giây hạn vẫn hợp lệ", exp, true},
		{"quá hạn 1 giây", exp.Add(time.Second), false},
		{"quá hạn nhiều", exp.Add(48 * time.Hour), false},
	} {
		clk.t = c.now
		_, err := s.Verify("GET", req("GET", u))
		if c.ok && err != nil || !c.ok && !errors.Is(err, binarystore.ErrExpired) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestVerifyExpiresEdgeCases(t *testing.T) {
	s, _ := newSigner(t, nil)
	p := storageBase + "/b/v"
	for _, c := range []struct {
		name, url string
		want      error
	}{
		{"không Expires", p + "?Signature=xyz", binarystore.ErrExpired},
		{"Expires rỗng", p + "?Expires=&Signature=xyz", binarystore.ErrExpired},
		{"Expires không phải số", p + "?Expires=abc&Signature=xyz", binarystore.ErrExpired},
		{"Expires NaN", p + "?Expires=NaN&Signature=xyz", binarystore.ErrExpired},
		{"Expires thập phân", p + "?Expires=1900000000.5&Signature=xyz", binarystore.ErrExpired},
		{"Expires đã qua", p + "?Expires=1&Signature=xyz", binarystore.ErrExpired},
		{"Expires xa, chữ ký giả", p + "?Expires=1900000000&Signature=xyz", binarystore.ErrBadSignature},
		{"không Signature", p + "?Expires=1900000000", binarystore.ErrMissingSignature},
		{"không query", p, binarystore.ErrMissingSignature},
	} {
		if _, err := s.Verify("GET", req("GET", c.url)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, muốn %v", c.name, err, c.want)
		}
	}
}

// Đường dẫn gửi thô (không chuẩn hóa) phải khớp đường dẫn đã ký; mọi thứ lạ là ErrBadPath dù chữ ký có hợp lệ với URL gốc.
func TestVerifyRawPathMustMatch(t *testing.T) {
	s, _ := newSigner(t, nil)
	good, _ := s.Sign("GET", "bin1", "ver1", "")
	q := good[strings.Index(good, "?"):]
	for _, path := range []string{
		"/storage/bin1/ver1/extra", "/storage/bin1/ver1/", "/storage/bin1/", "/storage/", "/storage", "/other/bin1/ver1", "/storage/bin1/%2e%2e",
		"/storage/%2e%2e/ver1", "/storage/bin1/..", "/storage/../storage/bin1/ver1", "/storage/bin1/..%5Cx", "/storage/bin1/..\\x", "/storage//ver1",
		"/storage/bin1/ver%31", "/storage/bin1/ver1%2f", "/storage/bin1%2fver1", "/storage/b%69n1/ver1", "/api/storage/bin1/ver1", "//storage/bin1/ver1",
	} {
		r := mustReq(t, "GET", storageBase+"/x"+q)
		r.RequestURI = path + q
		if _, err := s.Verify("GET", r); !errors.Is(err, binarystore.ErrBadPath) {
			t.Errorf("%q: %v", path, err)
		}
	}
}

// Qua máy chủ thật với đường dẫn thô (client Go sẽ chuẩn hóa nên dùng net.Dial): mọi biến thể thoát đường dẫn đều bị từ chối.
func TestVerifyThroughRealServerRawPaths(t *testing.T) {
	s, clk := newSigner(t, func(c *binarystore.SignerConfig) { c.BaseURL = "http://" + "127.0.0.1:1" + "/storage" })
	_ = clk
	good, _ := s.Sign("GET", "bin1", "ver1", "")
	q := good[strings.Index(good, "?"):]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.Verify("GET", r); err != nil {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	do := func(path string) int {
		c, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", path)
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("/storage/bin1/ver1" + q); got != 200 {
		t.Fatalf("đường dẫn đúng → %d", got)
	}
	for _, p := range []string{"/storage/bin1/..\\", "/storage/bin1/%2e%2e\\", "/api/storage/bin1/ver1/..\\..\\..\\storage\\", "/storage/bin1/ver1/.."} {
		if got := do(p + q); got != 401 {
			t.Errorf("%q → %d", p, got)
		}
	}
}

func TestVerifyBindsToConfiguredOriginNotHostHeader(t *testing.T) {
	s, _ := newSigner(t, nil)
	u, _ := s.Sign("GET", "b", "v", "")
	r := req("GET", u)
	r.Host = "attacker.example.net" // Host của yêu cầu không ảnh hưởng: gốc lấy từ BaseURL
	r.Header.Set("X-Forwarded-Host", "attacker.example.net")
	r.Header.Set("X-Forwarded-Proto", "http")
	if _, err := s.Verify("GET", r); err != nil {
		t.Fatalf("Host/Forwarded không được ảnh hưởng: %v", err)
	}
}

func TestVerifyServerRequestUsesRequestURI(t *testing.T) {
	s, _ := newSigner(t, func(c *binarystore.SignerConfig) { c.BaseURL = "http://example.test/storage" })
	var got error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got = s.Verify("GET", r)
	}))
	defer srv.Close()
	u, _ := s.Sign("GET", "b", "v", "")
	http.Get(srv.URL + u[len("http://example.test"):])
	if got != nil {
		t.Fatalf("%v", got)
	}
}

func TestLegacyGETSignature(t *testing.T) {
	// URL cũ: chữ ký trên URL không có tiền tố "GET " (Medplum cũ). Dựng bằng cách ký chuỗi không tiền tố với khóa thử.
	key := testKey(t)
	legacy := signRaw(t, key, "https://api.example.com/storage/b/v?Expires=1900000000")
	u := "https://api.example.com/storage/b/v?Expires=1900000000&Signature=" + url.QueryEscape(legacy)
	off, _ := newSigner(t, nil)
	if _, err := off.Verify("GET", req("GET", u)); !errors.Is(err, binarystore.ErrBadSignature) {
		t.Fatalf("mặc định phải tắt: %v", err)
	}
	on, _ := newSigner(t, func(c *binarystore.SignerConfig) { c.LegacyGET = true })
	if _, err := on.Verify("GET", req("GET", u)); err != nil {
		t.Fatalf("bật: %v", err)
	}
	if _, err := on.Verify("PUT", req("PUT", u)); !errors.Is(err, binarystore.ErrBadSignature) {
		t.Fatalf("PUT không được dùng chữ ký cũ: %v", err)
	}
}

func TestKeyRotationAndVerifyOnly(t *testing.T) {
	oldKey := testKey(t)
	newKey, err := binarystore.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	clk := &fixedClock{time.Unix(1_800_000_000, 0)}
	oldSigner, _ := binarystore.NewSigner(binarystore.SignerConfig{BaseURL: storageBase, Key: oldKey, Clock: clk})
	oldURL, _ := oldSigner.Sign("GET", "b", "v", "")

	rot, err := binarystore.NewSigner(binarystore.SignerConfig{BaseURL: storageBase, Key: newKey, VerifyKeys: []*rsa.PublicKey{&oldKey.PublicKey}, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rot.Verify("GET", req("GET", oldURL)); err != nil {
		t.Fatalf("URL ký bằng khóa cũ: %v", err)
	}
	if u, _ := rot.Sign("GET", "b", "v", ""); u == oldURL {
		t.Fatal("khóa mới phải ký ra chữ ký khác")
	}
	noOld, _ := binarystore.NewSigner(binarystore.SignerConfig{BaseURL: storageBase, Key: newKey, Clock: clk})
	if _, err := noOld.Verify("GET", req("GET", oldURL)); !errors.Is(err, binarystore.ErrBadSignature) {
		t.Fatalf("khóa cũ đã gỡ: %v", err)
	}
	vo, err := binarystore.NewSigner(binarystore.SignerConfig{BaseURL: storageBase, VerifyKeys: []*rsa.PublicKey{&oldKey.PublicKey}, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vo.Verify("GET", req("GET", oldURL)); err != nil {
		t.Fatalf("chỉ xác minh: %v", err)
	}
	if _, err := vo.Sign("GET", "b", "v", ""); !errors.Is(err, binarystore.ErrNoSigningKey) {
		t.Fatalf("Sign không khóa: %v", err)
	}
}

func TestSignRejectsBadInput(t *testing.T) {
	s, _ := newSigner(t, nil)
	for _, c := range [][3]string{{"a/b", "v", ""}, {"a", "..", ""}, {"", "v", ""}, {"a", "v", "p/../q"}, {"a", "v", "p&Signature=x"}} {
		if _, err := s.Sign("GET", c[0], c[1], c[2]); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("%v: %v", c, err)
		}
	}
	for _, m := range []string{"POST", "DELETE", "get", ""} {
		if _, err := s.Sign(m, "a", "v", ""); err == nil {
			t.Errorf("Sign(%q) không lỗi", m)
		}
	}
}

func TestNewSignerValidation(t *testing.T) {
	key := testKey(t)
	small, _ := rsaSmall()
	bad := map[string]binarystore.SignerConfig{
		"không BaseURL":      {Key: key},
		"BaseURL tương đối":  {BaseURL: "/storage", Key: key},
		"BaseURL ftp":        {BaseURL: "ftp://h/storage", Key: key},
		"BaseURL userinfo":   {BaseURL: "https://u:p@h/storage", Key: key},
		"BaseURL query":      {BaseURL: "https://h/storage?x=1", Key: key},
		"BaseURL query rỗng": {BaseURL: "https://h/storage?", Key: key},
		"BaseURL fragment":   {BaseURL: "https://h/storage#f", Key: key},
		"không khóa":         {BaseURL: storageBase},
		"khóa nhỏ":           {BaseURL: storageBase, Key: small},
		"khóa xác minh nhỏ":  {BaseURL: storageBase, VerifyKeys: []*rsa.PublicKey{&small.PublicKey}},
		"khóa xác minh nil":  {BaseURL: storageBase, VerifyKeys: []*rsa.PublicKey{nil}},
		"TTL âm":             {BaseURL: storageBase, Key: key, TTL: -time.Second},
		"TTL âm 1ns":         {BaseURL: storageBase, Key: key, TTL: -time.Nanosecond},
		"TTL quá 24h":        {BaseURL: storageBase, Key: key, TTL: 24*time.Hour + time.Second},
	}
	for name, cfg := range bad {
		if _, err := binarystore.NewSigner(cfg); err == nil {
			t.Errorf("%s: không lỗi", name)
		}
	}
	for name, cfg := range map[string]binarystore.SignerConfig{
		"TTL đúng 24h":      {BaseURL: storageBase, Key: key, TTL: 24 * time.Hour},
		"BaseURL có / cuối": {BaseURL: storageBase + "/", Key: key},
		"BaseURL có cổng":   {BaseURL: "http://localhost:8103/storage", Key: key},
		"BaseURL gốc":       {BaseURL: "https://h", Key: key},
	} {
		if _, err := binarystore.NewSigner(cfg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTTLControlsExpiry(t *testing.T) {
	s, clk := newSigner(t, func(c *binarystore.SignerConfig) { c.TTL = 90 * time.Second })
	u, _ := s.Sign("GET", "b", "v", "")
	clk.t = clk.t.Add(90 * time.Second)
	if _, err := s.Verify("GET", req("GET", u)); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(time.Second)
	if _, err := s.Verify("GET", req("GET", u)); !errors.Is(err, binarystore.ErrExpired) {
		t.Fatal(err)
	}
}

func TestBaseURLWithoutPathAndTrailingSlash(t *testing.T) {
	s, _ := newSigner(t, func(c *binarystore.SignerConfig) { c.BaseURL = "https://h.example/" })
	u, _ := s.Sign("GET", "b", "v", "")
	if !strings.HasPrefix(u, "https://h.example/b/v?") {
		t.Fatal(u)
	}
	if _, err := s.Verify("GET", req("GET", u)); err != nil {
		t.Fatal(err)
	}
}

func TestKeyPEM(t *testing.T) {
	k, err := binarystore.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := binarystore.MarshalPrivateKeyPEM(k)
	if err != nil {
		t.Fatal(err)
	}
	back, err := binarystore.ParsePrivateKeyPEM(pemBytes)
	if err != nil || !back.Equal(k) {
		t.Fatalf("khứ hồi PKCS#8: %v", err)
	}
	for name, in := range map[string]string{
		"không phải PEM": "hello",
		"loại lạ":        "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n",
		"mã hóa kiểu cũ": "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-256-CBC,00\n\nAAAA\n-----END RSA PRIVATE KEY-----\n",
		"mã hóa PKCS#8":  "-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n",
		"PKCS#8 hỏng":    "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n",
		"PKCS#1 hỏng":    "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n",
		"rỗng":           "",
	} {
		_, err := binarystore.ParsePrivateKeyPEM([]byte(in))
		if err == nil {
			t.Errorf("%s: không lỗi", name)
		}
		if strings.HasPrefix(name, "mã hóa") && (err == nil || !strings.Contains(err.Error(), "mật khẩu")) {
			t.Errorf("%s: thông báo phải nói rõ về mật khẩu: %v", name, err)
		}
	}
	pub, err := os.ReadFile("testdata/test-signing-key.pub.pem")
	if err != nil {
		t.Fatal(err)
	}
	pk, err := binarystore.ParsePublicKeyPEM(pub)
	if err != nil || !pk.Equal(&testKey(t).PublicKey) {
		t.Fatalf("khóa công khai: %v", err)
	}
	if _, err := binarystore.ParsePublicKeyPEM(pemBytes); err == nil {
		t.Error("khóa riêng không được nhận là khóa công khai")
	}
	if _, err := binarystore.ParsePublicKeyPEM([]byte("x")); err == nil {
		t.Error("không phải PEM")
	}
}
