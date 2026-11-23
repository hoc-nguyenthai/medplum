package application

import (
	"context"
	"net/url"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// IssueSecurityRequest phát một UserSecurityRequest: vô hiệu các request cùng loại chưa dùng của user rồi tạo request mới.
// Kho chỉ giữ BĂM của secret; secret gốc (32 ký tự hex) trả về để đưa vào thư và không lưu ở đâu khác.
func (s *Service) IssueSecurityRequest(ctx context.Context, user *fhir.User, typ, redirectURI string) (req *fhir.UserSecurityRequest, rawSecret string, err error) {
	if err := s.SupersedePriorSecurityRequests(ctx, user, typ); err != nil {
		return nil, "", err
	}
	rawSecret = domain.GenerateSecret(16)
	sr := &fhir.UserSecurityRequest{
		ResourceType: "UserSecurityRequest", Type: typ, Secret: domain.HashSecurityRequestSecret(rawSecret),
		User: newRef("User", user.ID, ""), ExpiresAt: domain.SecurityRequestExpiration(typ, s.Clock.Now()), RedirectUri: redirectURI,
	}
	if user.Project != nil {
		sr.Meta = &fhir.Meta{Project: idOf(refOf(user.Project))}
	}
	created, err := s.Resources.Create(ctx, sr)
	if err != nil {
		return nil, "", err
	}
	out, _ := created.(*fhir.UserSecurityRequest)
	if out == nil {
		return nil, "", domain.Internal("tạo UserSecurityRequest: kết quả không đúng kiểu")
	}
	return out, rawSecret, nil
}

// ResetPassword xử lý POST /auth/resetpassword. Luôn trả nil khi email không có user hoặc thư không gửi được (chống dò tài khoản
// qua mã lỗi và độ trễ, lệch L2); lỗi gửi chỉ ghi log. Email thuộc tên miền đăng nhập ngoài thì 400.
func (s *Service) ResetPassword(ctx context.Context, email, projectID, redirectURI string, sendEmail bool) error {
	email = strings.ToLower(email)
	if ext, err := s.ExternalAuthFor(ctx, email); err != nil {
		return err
	} else if ext != nil {
		return domain.BadRequest("Cannot reset password for external auth. Contact your system administrator.")
	}
	user, err := s.FindUserInScope(ctx, email, projectID)
	if err != nil {
		return err
	}
	if user == nil {
		return nil // không lộ email có tồn tại hay không
	}
	if !sendEmail {
		return nil // secret chỉ có trong thư: phát request mà không gửi là vô ích và còn làm mất link đang dùng
	}
	if !s.mailReady() {
		s.Logger.WarnContext(ctx, "chưa cấu hình thư hoặc AUTH_APP_BASE_URL: bỏ qua việc đặt lại mật khẩu")
		return nil
	}
	req, secret, err := s.IssueSecurityRequest(ctx, user, domain.SecurityRequestReset, redirectURI)
	if err != nil {
		return err
	}
	link := s.Mail.link("setpassword/" + req.ID + "/" + secret)
	if err := s.Mailer.Send(ctx, resetPasswordMessage(s.Mail, user.Email, link)); err != nil {
		s.Logger.ErrorContext(ctx, "không gửi được thư đặt lại mật khẩu", "user_id", user.ID, "error", err)
	}
	return nil
}

// checkSecurityRequest kiểm tra request theo đúng thứ tự thông báo của Medplum; thứ tự khác nhau giữa setpassword và verifyemail
// (loại trước hay đã dùng trước) nên người gọi truyền wantVerify.
func (s *Service) checkSecurityRequest(ctx context.Context, id, secret string, wantVerify bool) (*fhir.UserSecurityRequest, error) {
	sr, err := s.SecurityRequests.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	isVerify := sr.Type == domain.SecurityRequestVerifyEmail
	if wantVerify && !isVerify {
		return nil, domain.BadRequest("Invalid user security request type")
	}
	if domain.IsTrue(sr.Used) {
		return nil, domain.BadRequest("Already used")
	}
	if !wantVerify && isVerify {
		return nil, domain.BadRequest("Invalid request type")
	}
	if domain.IsSecurityRequestExpired(sr, s.Clock.Now()) {
		return nil, domain.BadRequest("Expired")
	}
	if !domain.SecurityRequestSecretMatches(sr.Secret, secret) {
		return nil, domain.BadRequest("Incorrect secret")
	}
	return sr, nil
}

// SetPasswordWithRequest xử lý POST /auth/setpassword.
func (s *Service) SetPasswordWithRequest(ctx context.Context, id, secret, password string) error {
	sr, err := s.checkSecurityRequest(ctx, id, secret, false)
	if err != nil {
		return err
	}
	user, err := s.Users.Get(ctx, idOf(refOf(sr.User)))
	if err != nil {
		return err
	}
	return s.SetPassword(ctx, user, password, sr)
}

// VerifyEmail xử lý POST /auth/verifyemail; trả redirectUri của request (rỗng nếu không có) để handler trả Location.
func (s *Service) VerifyEmail(ctx context.Context, id, secret string) (redirect string, err error) {
	sr, err := s.checkSecurityRequest(ctx, id, secret, true)
	if err != nil {
		return "", err
	}
	if sr.RedirectUri != "" {
		if u, perr := url.Parse(sr.RedirectUri); perr != nil || u.Scheme == "" || u.Host == "" {
			return "", domain.BadRequest("Invalid redirectUri") // trước khi tiêu thụ: link hỏng không làm mất request
		}
	}
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		if err := consumeSecurityRequest(ctx, tx.SecurityRequests, sr); err != nil {
			return err
		}
		_, err := tx.Users.Mutate(ctx, idOf(refOf(sr.User)), func(u *fhir.User) error {
			if domain.IsTrue(u.EmailVerified) {
				return ErrNoChange
			}
			u.EmailVerified = ptrTrue()
			return nil
		})
		return err
	})
	if err != nil {
		return "", err
	}
	return sr.RedirectUri, nil
}

// SendVerificationEmail phát request verify-email và gửi thư (auth/newuser.ts:152). redirect là trang người dùng quay lại sau khi
// xác minh. Không cấu hình thư thì bỏ qua (nil). Lỗi gửi được trả để người gọi quyết định.
func (s *Service) SendVerificationEmail(ctx context.Context, user *fhir.User, loginID string) error {
	if !s.mailReady() {
		return nil
	}
	req, secret, err := s.IssueSecurityRequest(ctx, user, domain.SecurityRequestVerifyEmail, s.Mail.link("register")+"?login="+url.QueryEscape(loginID))
	if err != nil {
		return err
	}
	return s.Mailer.Send(ctx, verifyEmailMessage(s.Mail, user.Email, s.Mail.link("verifyemail/"+req.ID+"/"+secret)))
}

// ChangePassword xử lý POST /auth/changepassword (Bearer). Thu hồi MỌI login của user, kể cả phiên hiện tại, và không đặt
// emailVerified (không có request chứng minh quyền nhận thư).
func (s *Service) ChangePassword(ctx context.Context, a *AuthState, oldPassword, newPassword string) error {
	userRef, err := callerUserRef(a)
	if err != nil {
		return err
	}
	user, err := s.Users.Get(ctx, idOf(userRef))
	if err != nil {
		return err
	}
	if user.Project == nil && !domain.CanManageUserAccount(a.Login) {
		return domain.Forbidden("Forbidden") // user toàn cục chỉ tự quản lý khi đăng nhập bằng mật khẩu hoặc Google
	}
	if user.PasswordHash == "" {
		return domain.BadRequestAt("Existing password not set", "oldPassword")
	}
	if !s.VerifyPassword(user, oldPassword) {
		return domain.BadRequestAt("Incorrect password", "oldPassword")
	}
	return s.SetPassword(ctx, user, newPassword, nil)
}
