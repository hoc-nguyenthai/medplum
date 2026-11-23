package application

import (
	"context"
	"time"
)

// TOTP sinh và kiểm mã một lần theo thời gian (RFC 6238: SHA-1, 6 chữ số, bước 30 giây, tương thích ứng dụng xác thực phổ biến).
type TOTP interface {
	// GenerateSecret sinh secret ngẫu nhiên (20 byte, base32 không đệm).
	GenerateSecret() (string, error)
	// KeyURI dựng URI otpauth để ứng dụng xác thực nhập (nhãn "<issuer>:<account>").
	KeyURI(issuer, account, secret string) string
	// Verify cho biết token đúng trong now ± window bước. Token sai dạng là false, không phải lỗi.
	Verify(secret, token string, now time.Time, window int) bool
}

// QR mã hóa văn bản thành ảnh QR dạng data URL ("data:image/png;base64,...").
type QR interface {
	DataURL(text string) (string, error)
}

// SecretBox mã hóa User.mfaSecret khi lưu (Q7). Open nhận cả giá trị cũ chưa mã hóa (tương thích dữ liệu nhập từ Medplum).
type SecretBox interface {
	Seal(plain string) (string, error)
	Open(stored string) (string, error)
}

// AttemptResult là kết quả đặt chỗ một lượt thử MFA.
type AttemptResult struct {
	Allowed bool
	// LoginCount là số lượt đã đặt cho login sau lần này (khi Allowed) hoặc hiện có (khi bị từ chối).
	LoginCount int
	// RetryAfter là thời gian tới khi bộ đếm bị chặn được đặt lại (chỉ có khi !Allowed).
	RetryAfter time.Duration
}

// MFAState là trạng thái phụ của MFA, tách khỏi tài nguyên FHIR: secret được mời, hạn mức thử, và mã TOTP đã dùng. Nhiều instance phải dùng Redis; bộ nhớ
// chỉ đúng với MỘT instance. Mọi lỗi là lỗi (fail đóng): không có đường nào bỏ qua hạn mức hay chống dùng lại khi kho trạng thái hỏng.
type MFAState interface {
	// OfferGetOrCreate trả secret đang được mời cho cặp (user, login); chưa có thì lưu candidate trong ttl rồi trả nó (nguyên tử: hai lần gọi đồng thời nhận
	// cùng một secret).
	OfferGetOrCreate(ctx context.Context, userID, loginID, candidate string, ttl time.Duration) (string, error)
	// OfferGet trả secret được mời, rỗng nếu không có hoặc đã hết hạn.
	OfferGet(ctx context.Context, userID, loginID string) (string, error)
	OfferDelete(ctx context.Context, userID, loginID string) error
	// ReserveAttempt đặt chỗ NGUYÊN TỬ một lượt thử cho login và user (cửa sổ cố định bắt đầu từ lượt đầu). Đặt chỗ trước khi biết mã đúng hay sai.
	ReserveAttempt(ctx context.Context, userID, loginID string, loginLimit, userLimit int, window time.Duration) (AttemptResult, error)
	// ReleaseAttempt trả lại lượt sau khi thành công: xóa bộ đếm của login, và bớt đúng MỘT lượt của user (không xóa các lần sai của login khác).
	ReleaseAttempt(ctx context.Context, userID, loginID string) error
	// ClaimTOTP ghi nhận mã TOTP đã dùng cho user; false nếu mã này đã được dùng trong ttl (chống dùng lại).
	ClaimTOTP(ctx context.Context, userID, token string, ttl time.Duration) (bool, error)
	// Hit tăng bộ đếm cửa sổ cố định theo key; ok=false (và không tăng) khi đã đạt limit. Dùng cho giới hạn gửi mã email.
	Hit(ctx context.Context, key string, limit int, window time.Duration) (count int, retryAfter time.Duration, ok bool, err error)
}

// MFAConfig là cấu hình MFA; giá trị 0 nhận mặc định của Medplum (domain.DefaultMFA*).
type MFAConfig struct {
	// Window là số bước TOTP chấp nhận mỗi bên (mặc định 1).
	Window int
	// LoginAttemptLimit và UserAttemptLimit là số lượt thử tối đa mỗi login (5) và mỗi user (20) trong LoginExpiration.
	LoginAttemptLimit, UserAttemptLimit int
	// LoginExpiration là tuổi tối đa của login chờ MFA và cũng là cửa sổ đếm lượt thử (10 phút).
	LoginExpiration time.Duration
	// EmailCodeExpiration là hạn mã email (20 phút); EnrollmentExpiration là hạn secret được mời (20 phút).
	EmailCodeExpiration, EnrollmentExpiration time.Duration
	// EmailSendLimit là số lần gửi mã email tối đa mỗi login trong EmailCodeExpiration (5): chặn việc dùng route gửi thư để làm phiền hộp thư.
	EmailSendLimit int
}

func (c MFAConfig) withDefaults() MFAConfig {
	if c.Window <= 0 {
		c.Window = 1
	}
	if c.LoginAttemptLimit <= 0 {
		c.LoginAttemptLimit = 5
	}
	if c.UserAttemptLimit <= 0 {
		c.UserAttemptLimit = 20
	}
	if c.LoginExpiration <= 0 {
		c.LoginExpiration = 10 * time.Minute
	}
	if c.EmailCodeExpiration <= 0 {
		c.EmailCodeExpiration = 20 * time.Minute
	}
	if c.EnrollmentExpiration <= 0 {
		c.EnrollmentExpiration = 20 * time.Minute
	}
	if c.EmailSendLimit <= 0 {
		c.EmailSendLimit = 5
	}
	return c
}
