// Package password là adapter băm mật khẩu bằng bcrypt, tương thích hash `$2b$` do Node (Medplum) sinh.
package password

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Bcrypt cài application.PasswordHasher.
type Bcrypt struct {
	cost  int
	dummy []byte // hash giả cùng cost, dùng khi không có user
}

// NewBcrypt dựng hasher với cost cho trước (mặc định của Medplum là 10).
func NewBcrypt(cost int) (*Bcrypt, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("password: bcrypt cost phải trong [%d, %d], được %d", bcrypt.MinCost, bcrypt.MaxCost, cost)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("password: không đọc được nguồn ngẫu nhiên: %w", err)
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), cost)
	if err != nil {
		return nil, err
	}
	return &Bcrypt{cost: cost, dummy: dummy}, nil
}

// Hash băm mật khẩu. bcrypt từ chối mật khẩu quá 72 byte; người gọi (application) đã kiểm trước.
func (b *Bcrypt) Hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), b.cost)
	return string(h), err
}

// maxPasswordBytes là giới hạn của bcrypt: phần vượt quá bị bỏ qua khi băm, nên mật khẩu dài hơn sẽ khớp với hash của 72 byte đầu.
const maxPasswordBytes = 72

// Compare so khớp mật khẩu với hash; hash rỗng hoặc hỏng thì false. Mật khẩu quá 72 byte KHÔNG BAO GIỜ khớp (S8): thư viện bcrypt cắt âm thầm
// ở 72 byte, nên không chặn thì "<72 ký tự đúng>+bất kỳ" đăng nhập được, và `changepassword` (chỉ kiểm oldPassword không rỗng) bị ảnh hưởng.
func (b *Bcrypt) Compare(password, hash string) bool {
	return len(password) <= maxPasswordBytes && hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// CompareDummy so với hash giả cùng cost để tốn thời gian như so thật.
func (b *Bcrypt) CompareDummy(password string) {
	_ = bcrypt.CompareHashAndPassword(b.dummy, []byte(password))
}
