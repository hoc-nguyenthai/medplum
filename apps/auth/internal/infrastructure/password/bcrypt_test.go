package password

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndCompare(t *testing.T) {
	b, err := NewBcrypt(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	h, err := b.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2a$04$") && !strings.HasPrefix(h, "$2b$04$") {
		t.Errorf("hash không phải bcrypt cost 4: %s", h)
	}
	if !b.Compare("correct horse", h) || b.Compare("wrong", h) || b.Compare("correct horse", "") || b.Compare("x", "không-phải-hash") {
		t.Error("Compare")
	}
	h2, _ := b.Hash("correct horse")
	if h == h2 {
		t.Error("mỗi lần băm phải có salt khác")
	}
	b.CompareDummy("anything") // không panic
}

// Hash `$2b$` sinh bởi thư viện bcrypt thật (pyca/bcrypt, cùng thuật toán với gói `bcrypt` của Node): mật khẩu "password123", cost 10.
func TestCompatibleWithNodeStyleHash(t *testing.T) {
	const nodeHash = "$2b$10$2Q/O0b5jOvustZbNDq/gO.4aNS2W.gD.jDCDRA1tYzIYpeFPF2vR2"
	b, _ := NewBcrypt(bcrypt.MinCost)
	if !b.Compare("password123", nodeHash) {
		t.Fatal("hash $2b$ của Node phải so khớp được")
	}
	if b.Compare("password124", nodeHash) {
		t.Fatal("sai mật khẩu không được khớp")
	}
}

func TestInvalidCost(t *testing.T) {
	for _, c := range []int{0, 3, 32, -1} {
		if _, err := NewBcrypt(c); err == nil {
			t.Errorf("cost %d phải lỗi", c)
		}
	}
	if _, err := NewBcrypt(10); err != nil {
		t.Errorf("cost 10 hợp lệ: %v", err)
	}
}

// bcrypt chỉ dùng 72 byte đầu: hai mật khẩu giống nhau ở 72 byte đầu so khớp nhau, nên application phải từ chối > 72 byte.
func TestBcryptTruncatesAt72Bytes(t *testing.T) {
	b, _ := NewBcrypt(bcrypt.MinCost)
	long := strings.Repeat("a", 72)
	h, err := b.Hash(long)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Compare(long, h) {
		t.Error("72 byte phải khớp")
	}
}

// S8: mật khẩu quá 72 byte không bao giờ khớp, kể cả khi 72 byte đầu đúng (Node/bcrypt cắt âm thầm).
func TestCompareNeverMatchesPasswordsOverTheByteLimit(t *testing.T) {
	b, _ := NewBcrypt(bcrypt.MinCost)
	base := strings.Repeat("a", 72)
	h, err := b.Hash(base)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Compare(base, h) {
		t.Fatal("72 byte đúng phải khớp")
	}
	for name, pw := range map[string]string{"thêm 1 ký tự": base + "X", "thêm nhiều": base + strings.Repeat("z", 500)} {
		if b.Compare(pw, h) {
			t.Errorf("%s: mật khẩu quá 72 byte không được khớp", name)
		}
	}
	// 72 BYTE (không phải ký tự) là ranh giới: 24 ký tự 3 byte vừa đủ, thêm 1 ký tự thì vượt
	three := strings.Repeat("ệ", 24)
	h3, err := b.Hash(three)
	if err != nil || !b.Compare(three, h3) || b.Compare(three+"a", h3) {
		t.Errorf("ranh giới theo byte: %v", err)
	}
	// hash Node `$2b$` của mật khẩu đúng 72 byte vẫn so được (tương thích)
	if b.Compare(base+"X", "$2b$04$"+strings.Repeat("x", 53)) {
		t.Error("hash hỏng không được khớp")
	}
}
