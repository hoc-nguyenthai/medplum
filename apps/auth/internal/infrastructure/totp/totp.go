// Package totp cài cổng application.TOTP và application.QR.
package totp

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"net/url"
	"time"

	"github.com/pquerna/otp"
	pq "github.com/pquerna/otp/totp"
	"github.com/skip2/go-qrcode"
)

// secretBytes là độ dài secret (Q11: 20 byte = 160 bit, đúng khuyến nghị của RFC 4226).
const secretBytes = 20

// Authenticator cài application.TOTP bằng pquerna/otp (SHA-1, 6 chữ số, bước 30 giây).
type Authenticator struct{}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret sinh secret ngẫu nhiên an toàn mật mã, base32 viết hoa không đệm (khớp ^[A-Z2-7]+$).
func (Authenticator) GenerateSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// KeyURI dựng otpauth://totp/<issuer>:<account>?secret=..&issuer=..; hai thành phần của nhãn được mã hóa phần trăm, dấu ":" ngăn cách giữ nguyên.
func (Authenticator) KeyURI(issuer, account, secret string) string {
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer) + ":" + url.PathEscape(account) + "?" + q.Encode()
}

// Verify kiểm token trong now ± window bước. Token không phải đúng 6 chữ số, hay secret hỏng, là false (không panic).
func (Authenticator) Verify(secret, token string, now time.Time, window int) bool {
	if len(token) != 6 {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	if window < 0 {
		window = 0
	}
	ok, err := pq.ValidateCustom(token, secret, now, pq.ValidateOpts{Period: 30, Skew: uint(window), Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	return err == nil && ok
}

// QRCode cài application.QR bằng skip2/go-qrcode (PNG 256x256, mức sửa lỗi trung bình).
type QRCode struct{}

// DataURL mã hóa text thành data:image/png;base64,...
func (QRCode) DataURL(text string) (string, error) {
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}
