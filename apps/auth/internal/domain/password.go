package domain

import "unicode/utf8"

// Giới hạn mật khẩu của Medplum (constants.ts): tối thiểu 8 ký tự (đếm ký tự Unicode), tối đa 72 BYTE (giới hạn của bcrypt).
const (
	MinPasswordLength = 8
	MaxPasswordBytes  = 72
)

// ValidatePassword kiểm tra độ dài mật khẩu, trả lỗi 400 gắn trường "password" giống validator của Medplum.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return BadRequestAt("Password must be at least 8 characters", "password")
	}
	if len(password) > MaxPasswordBytes {
		return BadRequestAt("Password must be no more than 72 characters", "password")
	}
	return nil
}

// PasswordTooLongForHash kiểm tra riêng giới hạn 72 byte tại điểm băm (Medplum chặn cả ở đây, auth/utils.ts:642).
func PasswordTooLongForHash(password string) error {
	if len(password) > MaxPasswordBytes {
		return BadRequest("Password must be no more than 72 characters")
	}
	return nil
}

// PwnedMessage là thông điệp khi mật khẩu có trong cơ sở dữ liệu rò rỉ.
const PwnedMessage = "Password found in breach database"
