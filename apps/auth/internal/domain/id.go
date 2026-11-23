package domain

import (
	"crypto/rand"
	"fmt"
)

// NewID sinh một UUID v4 an toàn mật mã dùng làm id resource (khi cần biết id trước khi ghi, ví dụ Project để các
// AccessPolicy mặc định tham chiếu được qua meta.project).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("domain: không đọc được nguồn ngẫu nhiên: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40 // phiên bản 4
	b[8] = b[8]&0x3f | 0x80 // biến thể RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
