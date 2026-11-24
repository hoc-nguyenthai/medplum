package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// HashPassword kiểm giới hạn 72 byte rồi băm.
func (s *Service) HashPassword(password string) (string, error) {
	if err := domain.PasswordTooLongForHash(password); err != nil {
		return "", err
	}
	h, err := s.Hasher.Hash(password)
	if err != nil {
		return "", domain.InternalErr("băm mật khẩu", err)
	}
	return h, nil
}

// VerifyPassword so khớp mật khẩu. Không có user hoặc user chưa có hash thì vẫn so với hash giả cùng cost, để thời gian trả
// lời không lộ email có tồn tại hay không (oauth/utils.ts:309).
func (s *Service) VerifyPassword(user *fhir.User, password string) bool {
	if user == nil || user.PasswordHash == "" {
		s.Hasher.CompareDummy(password)
		return false
	}
	return s.Hasher.Compare(password, user.PasswordHash)
}

// CheckPwned từ chối mật khẩu có trong cơ sở dữ liệu rò rỉ. Lỗi dịch vụ kiểm tra là lỗi (fail đóng), không cho qua.
func (s *Service) CheckPwned(ctx context.Context, password string) error {
	n, err := s.Pwned.Count(ctx, password)
	if err != nil {
		return domain.InternalErr("kiểm tra mật khẩu rò rỉ", err)
	}
	if n > 0 {
		return domain.BadRequest(domain.PwnedMessage)
	}
	return nil
}

// SetPassword đặt mật khẩu mới (auth/setpassword.ts:83). Kiểm pwned TRƯỚC khi tiêu thụ request (lỗi pwned không làm mất
// request); tiêu thụ request, vô hiệu request đặt lại cũ và ghi mật khẩu trong MỘT transaction (lỗi giữa chừng thì request
// không bị tiêu thụ); sau transaction mới thu hồi mọi login của user. sr (tùy chọn) là request cho phép đặt mật khẩu; có thì
// email cũng được đánh dấu đã xác minh.
func (s *Service) SetPassword(ctx context.Context, user *fhir.User, password string, sr *fhir.UserSecurityRequest) error {
	if err := s.CheckPwned(ctx, password); err != nil {
		return err
	}
	hash, err := s.HashPassword(password)
	if err != nil {
		return err
	}
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		if sr != nil {
			if err := consumeSecurityRequest(ctx, tx.SecurityRequests, sr); err != nil {
				return err
			}
		}
		if err := s.retirePassword(ctx, tx.SecurityRequests, user); err != nil {
			return err
		}
		_, err := tx.Users.Mutate(ctx, user.ID, func(u *fhir.User) error {
			u.PasswordHash = hash
			if sr != nil {
				u.EmailVerified = ptrTrue()
			}
			return nil
		})
		return err
	})
	if err != nil {
		return err
	}
	// đặt lại mật khẩu (qua email hoặc quản trị) chứng minh chủ tài khoản: mở khóa. Lỗi chỉ ghi log, việc đổi mật khẩu đã thành công.
	if err := s.UnlockUser(ctx, user.ID); err != nil {
		s.Logger.ErrorContext(ctx, "khóa tài khoản: không mở được khóa sau khi đặt lại mật khẩu", "user_id", user.ID, "error", err)
	}
	return s.RevokeUserLogins(ctx, user)
}

// RevokeUserLogins thu hồi mọi login của user. Gom hết id trước rồi mới sửa từng cái: phân trang theo offset sẽ bỏ sót bản
// ghi khi lastUpdated đổi giữa chừng (Medplum dùng con trỏ vì lý do này).
func (s *Service) RevokeUserLogins(ctx context.Context, user *fhir.User) error {
	return revokeUserLoginsWith(ctx, s.Logins, user)
}

// revokeUserLoginsWith thu hồi mọi login của user trên kho chỉ định (kho thường hoặc kho của một transaction).
func revokeUserLoginsWith(ctx context.Context, logins LoginRepository, user *fhir.User) error {
	ids, err := logins.ListIDsByUser(ctx, "User/"+user.ID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := logins.Mutate(ctx, id, func(l *fhir.Login) error {
			if domain.IsTrue(l.Revoked) {
				return ErrNoChange
			}
			l.Revoked = ptrTrue()
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
