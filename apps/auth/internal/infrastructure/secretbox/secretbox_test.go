package secretbox

import (
	"encoding/base64"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
)

var _ application.SecretBox = (*Box)(nil)
var _ application.SecretBox = Plain{}

func key(b byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{b}), 32)))
}

func TestSealOpenRoundTripAndRandomNonce(t *testing.T) {
	b, err := ParseKeys("k1:" + key('a'))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := b.Seal("JBSWY3DPEHPK3PXP")
	c, _ := b.Seal("JBSWY3DPEHPK3PXP")
	if a == c {
		t.Error("nonce phải ngẫu nhiên: hai lần mã hóa cùng giá trị phải khác nhau")
	}
	if strings.Contains(a, "JBSWY3DPEHPK3PXP") || !strings.HasPrefix(a, "enc:v1:k1:") {
		t.Errorf("định dạng/rò rỉ: %s", a)
	}
	for _, s := range []string{a, c} {
		if got, err := b.Open(s); err != nil || got != "JBSWY3DPEHPK3PXP" {
			t.Errorf("%q %v", got, err)
		}
	}
	if got, _ := b.Seal(""); got == "" {
		t.Error("chuỗi rỗng vẫn phải được mã hóa")
	}
}

func TestOpenPassesThroughLegacyPlaintext(t *testing.T) {
	b, _ := ParseKeys("k1:" + key('a'))
	if got, err := b.Open("JBSWY3DPEHPK3PXP"); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Errorf("bản rõ cũ (nhập từ Medplum) phải đọc được: %q %v", got, err)
	}
	if got, err := b.Open(""); err != nil || got != "" {
		t.Errorf("rỗng: %q %v", got, err)
	}
}

func TestKeyRotation(t *testing.T) {
	old, _ := ParseKeys("k1:" + key('a'))
	sealedOld, _ := old.Seal("SECRET")
	rotated, err := ParseKeys("k2:" + key('b') + ",k1:" + key('a')) // khóa mới lên đầu, khóa cũ giữ để giải mã
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Open(sealedOld); err != nil || got != "SECRET" {
		t.Errorf("khóa cũ vẫn giải mã được: %q %v", got, err)
	}
	sealedNew, _ := rotated.Seal("SECRET")
	if !strings.HasPrefix(sealedNew, "enc:v1:k2:") {
		t.Errorf("phải mã hóa bằng khóa đầu tiên: %s", sealedNew)
	}
	if _, err := old.Open(sealedNew); err == nil {
		t.Error("bản chỉ có khóa cũ không giải mã được giá trị của khóa mới")
	}
}

func TestOpenRejectsTamperingAndWrongKey(t *testing.T) {
	b, _ := ParseKeys("k1:" + key('a'))
	s, _ := b.Seal("SECRET")
	other, _ := ParseKeys("k1:" + key('z'))
	if _, err := other.Open(s); err == nil {
		t.Error("khóa sai phải lỗi")
	}
	// sửa một ký tự của phần mã hóa
	i := strings.LastIndex(s, ":") + 5
	flipped := s[:i] + map[bool]string{true: "B", false: "A"}[s[i] == 'A'] + s[i+1:]
	if _, err := b.Open(flipped); err == nil {
		t.Error("giá trị bị sửa phải lỗi")
	}
	// đổi kid trên giá trị đã lưu: kid nằm trong dữ liệu xác thực nên giải mã thất bại dù có hai khóa
	both, _ := ParseKeys("k1:" + key('a') + ",k2:" + key('a'))
	relabeled := strings.Replace(s, "enc:v1:k1:", "enc:v1:k2:", 1)
	if _, err := both.Open(relabeled); err == nil {
		t.Error("đổi kid phải làm giải mã thất bại")
	}
	for _, bad := range []string{"enc:v1:k1", "enc:v1:k1:!!!", "enc:v1:k1:AAAA", "enc:v1:khong-co:AAAA"} {
		if _, err := b.Open(bad); err == nil {
			t.Errorf("%q phải lỗi", bad)
		}
	}
}

func TestParseKeysValidation(t *testing.T) {
	for name, spec := range map[string]string{
		"rỗng": "", "chỉ dấu phẩy": " , ", "thiếu kid": key('a'), "kid rỗng": ":" + key('a'), "khóa ngắn": "k1:" + base64.StdEncoding.EncodeToString([]byte("ngan")),
		"không phải base64": "k1:!!!", "trùng kid": "k1:" + key('a') + ",k1:" + key('b'),
	} {
		if _, err := ParseKeys(spec); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
	if _, err := ParseKeys(" k1:" + key('a') + " , k2:" + base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))); err != nil {
		t.Errorf("khoảng trắng và base64 không đệm phải được chấp nhận: %v", err)
	}
}

func TestPlainIsIdentity(t *testing.T) {
	p := Plain{}
	if s, err := p.Seal("X"); err != nil || s != "X" {
		t.Error("Plain.Seal")
	}
	if s, err := p.Open("X"); err != nil || s != "X" {
		t.Error("Plain.Open")
	}
}

// AES chấp nhận khóa 16 và 24 byte, nhưng ở đây BẮT BUỘC 32 byte (AES-256): khóa yếu hơn bị từ chối chứ không âm thầm dùng AES-128/192.
func TestParseKeysRequiresExactly32Bytes(t *testing.T) {
	for _, n := range []int{0, 1, 16, 24, 31, 33, 64} {
		if _, err := ParseKeys("k1:" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", n)))); err == nil {
			t.Errorf("khóa %d byte phải bị từ chối", n)
		}
	}
	if _, err := ParseKeys("k1:" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))); err != nil {
		t.Errorf("32 byte phải hợp lệ: %v", err)
	}
}
