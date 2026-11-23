// Package secretbox mã hóa dữ liệu nhạy cảm khi lưu (AES-256-GCM) với khóa có định danh (kid) để xoay khóa.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// prefix đánh dấu giá trị đã mã hóa: "enc:v1:<kid>:<base64(nonce|ciphertext)>". Giá trị không có tiền tố là bản rõ cũ.
const prefix = "enc:v1:"

// Box giữ các khóa; khóa đầu tiên dùng để mã hóa, mọi khóa dùng để giải mã.
type Box struct {
	activeKID string
	keys      map[string]cipher.AEAD
}

// ParseKeys đọc danh sách "kid:base64key,kid2:base64key2". Mỗi khóa phải đúng 32 byte; kid không được rỗng hay chứa ':' hoặc ','; không trùng kid.
// Khóa đầu tiên là khóa đang dùng để mã hóa.
func ParseKeys(spec string) (*Box, error) {
	b := &Box{keys: map[string]cipher.AEAD{}}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kid, enc, ok := strings.Cut(part, ":")
		if !ok || kid == "" || strings.ContainsAny(kid, ":,") {
			return nil, errors.New("secretbox: mỗi khóa phải có dạng kid:base64")
		}
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(enc)
		}
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("secretbox: khóa %q phải là 32 byte base64", kid)
		}
		if _, dup := b.keys[kid]; dup {
			return nil, fmt.Errorf("secretbox: trùng kid %q", kid)
		}
		blk, err := aes.NewCipher(raw)
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(blk)
		if err != nil {
			return nil, err
		}
		b.keys[kid] = gcm
		if b.activeKID == "" {
			b.activeKID = kid
		}
	}
	if b.activeKID == "" {
		return nil, errors.New("secretbox: chưa có khóa nào")
	}
	return b, nil
}

// Seal mã hóa plain bằng khóa đang dùng; nonce ngẫu nhiên mỗi lần nên hai lần mã hóa cùng giá trị cho kết quả khác nhau.
func (b *Box) Seal(plain string) (string, error) {
	gcm := b.keys[b.activeKID]
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// kid được gắn vào dữ liệu xác thực: đổi kid trên giá trị đã lưu làm giải mã thất bại
	ct := gcm.Seal(nonce, nonce, []byte(plain), []byte(b.activeKID))
	return prefix + b.activeKID + ":" + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open giải mã giá trị của Seal. Giá trị không có tiền tố trả nguyên (bản rõ cũ). Giá trị có tiền tố mà kid lạ, hỏng hay bị sửa là lỗi.
func (b *Box) Open(stored string) (string, error) {
	if !strings.HasPrefix(stored, prefix) {
		return stored, nil
	}
	kid, enc, ok := strings.Cut(strings.TrimPrefix(stored, prefix), ":")
	if !ok {
		return "", errors.New("secretbox: giá trị mã hóa sai dạng")
	}
	gcm, ok := b.keys[kid]
	if !ok {
		return "", fmt.Errorf("secretbox: không có khóa %q", kid)
	}
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("secretbox: giá trị mã hóa hỏng")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(kid))
	if err != nil {
		return "", errors.New("secretbox: giải mã thất bại")
	}
	return string(plain), nil
}

// Plain là SecretBox không mã hóa (chỉ cho dev/test khi chưa cấu hình khóa): Seal trả nguyên, Open trả nguyên.
type Plain struct{}

// Seal trả nguyên giá trị.
func (Plain) Seal(s string) (string, error) { return s, nil }

// Open trả nguyên giá trị.
func (Plain) Open(s string) (string, error) { return s, nil }
