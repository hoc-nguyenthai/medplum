package application

import (
	"context"
	"time"

	"trivita-fhir/pkg/fhir"
)

// LockResult là kết quả đặt chỗ một lượt thử mật khẩu.
type LockResult struct {
	// Allowed: được phép kiểm mật khẩu ở lượt này.
	Allowed bool
	// Locked: lượt này làm tài khoản vừa bị khóa (chỉ khi !Allowed).
	Locked bool
	// RetryAfter là thời gian khóa còn lại (khi !Allowed).
	RetryAfter time.Duration
}

// LoginGuard là trạng thái khóa tài khoản theo số lần đăng nhập mật khẩu sai, tách khỏi tài nguyên FHIR (như MFAState): không ghi thêm phiên bản User mỗi lần
// sai. Nhiều instance phải dùng Redis; bộ nhớ chỉ đúng với MỘT instance.
//
// Đặt chỗ TRƯỚC khi kiểm mật khẩu (nguyên tử) để các lượt thử song song không vượt hạn mức: cho tối đa `limit` lượt trong cửa sổ cố định bắt đầu từ lượt
// đầu; lượt thứ limit+1 bị từ chối và khóa tài khoản `lockFor`. Khi đang khóa mọi lượt bị từ chối và KHÔNG kéo dài khóa (kẻ tấn công không giữ khóa mãi
// bằng cách thử liên tục).
type LoginGuard interface {
	Reserve(ctx context.Context, userID string, limit int, window, lockFor time.Duration) (LockResult, error)
	// Release xóa bộ đếm lượt sai sau khi đăng nhập thành công (không mở khóa).
	Release(ctx context.Context, userID string) error
	// Unlock xóa cả khóa lẫn bộ đếm (đặt lại mật khẩu, lệnh quản trị).
	Unlock(ctx context.Context, userID string) error
}

// LockoutConfig là cấu hình khóa tài khoản. MaxFailures <= 0 (hoặc không có LoginGuard) là tắt.
type LockoutConfig struct {
	// MaxFailures là số lượt thử tối đa mỗi cửa sổ trước khi khóa.
	MaxFailures int
	// Window là cửa sổ đếm (cố định, bắt đầu từ lượt đầu); Duration là thời gian khóa.
	Window, Duration time.Duration
}

func (c LockoutConfig) enabled() bool { return c.MaxFailures > 0 && c.Window > 0 && c.Duration > 0 }

// lockoutAllow đặt chỗ một lượt thử mật khẩu cho user. false nghĩa là tài khoản đang bị khóa: người gọi KHÔNG được kiểm mật khẩu thật và phải trả đúng lỗi
// "sai thông tin" như khi sai mật khẩu (không lộ việc khóa, nên cũng không lộ email có tồn tại). Lỗi kho trạng thái chỉ ghi log và cho qua (mở khi lỗi: kho
// hỏng không được khóa mọi người đăng nhập; hạn mức theo IP vẫn còn).
func (s *Service) lockoutAllow(ctx context.Context, user *fhir.User, remoteAddr string) bool {
	if s.LoginGuard == nil || !s.Lockout.enabled() {
		return true
	}
	res, err := s.LoginGuard.Reserve(ctx, user.ID, s.Lockout.MaxFailures, s.Lockout.Window, s.Lockout.Duration)
	if err != nil {
		s.Logger.ErrorContext(ctx, "khóa tài khoản: không đặt chỗ được lượt thử, cho qua", "user_id", user.ID, "error", err)
		return true
	}
	if res.Allowed {
		return true
	}
	if res.Locked {
		s.Logger.WarnContext(ctx, "tài khoản bị khóa tạm do đăng nhập sai nhiều lần", "audit", true, "user_id", user.ID,
			"remote_address", remoteAddr, "locked_for", res.RetryAfter.String())
	} else {
		s.Logger.WarnContext(ctx, "đăng nhập bị chặn: tài khoản đang bị khóa", "audit", true, "user_id", user.ID,
			"remote_address", remoteAddr, "retry_after", res.RetryAfter.String())
	}
	return false
}

// lockoutSucceeded xóa bộ đếm lượt sai sau khi đăng nhập thành công.
func (s *Service) lockoutSucceeded(ctx context.Context, userID string) {
	if s.LoginGuard == nil || !s.Lockout.enabled() {
		return
	}
	if err := s.LoginGuard.Release(ctx, userID); err != nil {
		s.Logger.ErrorContext(ctx, "khóa tài khoản: không xóa được bộ đếm sau đăng nhập thành công", "user_id", userID, "error", err)
	}
}

// UnlockUser mở khóa tài khoản và xóa bộ đếm (đặt lại mật khẩu gọi tự động; lệnh quản trị `auth user unlock`). Lỗi được trả để người gọi biết.
func (s *Service) UnlockUser(ctx context.Context, userID string) error {
	if s.LoginGuard == nil {
		return nil
	}
	return s.LoginGuard.Unlock(ctx, userID)
}
