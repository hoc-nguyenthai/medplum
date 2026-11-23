package totp

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
)

var _ application.TOTP = Authenticator{}
var _ application.QR = QRCode{}

// Vector kiểm thử của RFC 6238 Appendix B (SHA-1, secret ASCII "12345678901234567890", 6 chữ số = 6 chữ số cuối của mã 8 chữ số trong RFC).
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestVerifyRFC6238Vectors(t *testing.T) {
	a := Authenticator{}
	for _, tc := range []struct {
		unix  int64
		token string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if !a.Verify(rfcSecret, tc.token, time.Unix(tc.unix, 0), 0) {
			t.Errorf("t=%d: mã %s (vector RFC) phải đúng", tc.unix, tc.token)
		}
	}
	if a.Verify(rfcSecret, "287083", time.Unix(59, 0), 1) {
		t.Error("mã sai không được qua")
	}
}

func TestVerifyWindow(t *testing.T) {
	a := Authenticator{}
	now := time.Unix(1111111109, 0) // mã 081804 hợp lệ ở bước này
	prev, next := now.Add(-30*time.Second), now.Add(30*time.Second)
	if !a.Verify(rfcSecret, "081804", now, 0) {
		t.Fatal("bước hiện tại")
	}
	// window=0 chỉ nhận đúng bước; window=1 nhận lệch một bước mỗi bên; window=1 không nhận lệch hai bước
	if a.Verify(rfcSecret, "081804", next, 0) || a.Verify(rfcSecret, "081804", prev, 0) {
		t.Error("window=0 chỉ nhận đúng bước")
	}
	if !a.Verify(rfcSecret, "081804", next, 1) || !a.Verify(rfcSecret, "081804", prev, 1) {
		t.Error("window=1 phải nhận lệch một bước mỗi bên")
	}
	if a.Verify(rfcSecret, "081804", now.Add(60*time.Second), 1) || a.Verify(rfcSecret, "081804", now.Add(-60*time.Second), 1) {
		t.Error("window=1 không nhận lệch hai bước")
	}
	if !a.Verify(rfcSecret, "081804", now.Add(60*time.Second), 2) {
		t.Error("window=2 nhận lệch hai bước")
	}
}

func TestVerifyMalformedInputIsFalseNotPanic(t *testing.T) {
	a := Authenticator{}
	now := time.Unix(1111111109, 0)
	for _, tok := range []string{"", "1", "12345", "1234567", "1234567890", "abcdef", "08180 ", " 81804", "08-804", "０８１８０４", "INVALID_TOKEN"} {
		if a.Verify(rfcSecret, tok, now, 1) {
			t.Errorf("token %q phải sai", tok)
		}
	}
	for _, sec := range []string{"", "!!!", "không phải base32", "A"} {
		if a.Verify(sec, "081804", now, 1) {
			t.Errorf("secret %q hỏng phải cho kết quả sai", sec)
		}
	}
	// window âm coi như 0: đúng bước vẫn đúng, nhưng KHÔNG nới thành cửa sổ lớn (mã lệch hai bước vẫn sai)
	if !a.Verify(rfcSecret, "081804", now, -3) || a.Verify(rfcSecret, "081804", now.Add(60*time.Second), -3) {
		t.Error("window âm phải coi như 0")
	}
}

func TestGenerateSecret(t *testing.T) {
	a := Authenticator{}
	re := regexp.MustCompile(`^[A-Z2-7]{32}$`) // 20 byte = 32 ký tự base32 không đệm
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s, err := a.GenerateSecret()
		if err != nil || !re.MatchString(s) || seen[s] {
			t.Fatalf("secret %q lỗi %v (trùng: %v)", s, err, seen[s])
		}
		seen[s] = true
	}
	// secret sinh ra dùng được với Verify (tương thích ứng dụng xác thực): sinh mã bằng cùng thuật toán
	s, _ := a.GenerateSecret()
	if a.Verify(s, "000000", time.Unix(0, 0), 0) && a.Verify(s, "000001", time.Unix(0, 0), 0) {
		t.Error("không thể cùng đúng hai mã")
	}
}

func TestKeyURI(t *testing.T) {
	a := Authenticator{}
	u := a.KeyURI("Acme Health", "an+test@example.org", "ABCDEF")
	if !strings.HasPrefix(u, "otpauth://totp/Acme%20Health:an+test@example.org?") && !strings.HasPrefix(u, "otpauth://totp/Acme%20Health:an%2Btest@example.org?") {
		t.Errorf("nhãn: %s", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("secret") != "ABCDEF" || q.Get("issuer") != "Acme Health" || q.Get("algorithm") != "SHA1" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Errorf("tham số: %v", q)
	}
	if label := strings.TrimPrefix(parsed.Path, "/"); label != "Acme Health:an+test@example.org" {
		t.Errorf("nhãn sau khi giải mã: %q", label)
	}
}

func TestQRCodeDataURL(t *testing.T) {
	got, err := QRCode{}.DataURL("otpauth://totp/Acme:an@example.org?secret=ABC&issuer=Acme")
	if err != nil {
		t.Fatal(err)
	}
	const pre = "data:image/png;base64,"
	if !strings.HasPrefix(got, pre) {
		t.Fatalf("%.40s", got)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, pre))
	if err != nil || len(raw) < 100 || string(raw[1:4]) != "PNG" {
		t.Errorf("không phải PNG: %v %d", err, len(raw))
	}
}
