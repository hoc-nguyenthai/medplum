package application

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// mfaReady: có đủ phụ thuộc để chạy MFA thật. Thiếu thì mọi nhánh cần MFA từ chối (FailClosedMFA), không có đường bỏ qua.
func (s *Service) mfaReady() bool {
	return s.TOTP != nil && s.QR != nil && s.MFAState != nil && s.MFASecrets != nil
}

func (s *Service) requireMFA() error {
	if !s.mfaReady() {
		return domain.Internal("MFA chưa được cấu hình")
	}
	return nil
}

// mfaGate cài MFAGate (cổng mà BuildLoginResult gọi) bằng Service.
type mfaGate struct{ s *Service }

func (g mfaGate) BuildEnrollment(ctx context.Context, login *fhir.Login, user *fhir.User, project *fhir.Project) (string, string, error) {
	return g.s.buildEnrollment(ctx, login, user, project)
}

func (g mfaGate) SendEmailCode(ctx context.Context, login *fhir.Login, user *fhir.User, project *fhir.Project) error {
	return g.s.sendEmailCode(ctx, login, user, project)
}

// ---- secret được mời (offer)

// offer trả secret TOTP được mời cho cặp (user, login), tạo mới nếu chưa có (auth/utils.ts:229). Mỗi cặp có secret riêng, giữ EnrollmentExpiration.
func (s *Service) offer(ctx context.Context, user *fhir.User, login *fhir.Login) (string, error) {
	cand, err := s.TOTP.GenerateSecret()
	if err != nil {
		return "", domain.InternalErr("sinh secret TOTP", err)
	}
	v, err := s.MFAState.OfferGetOrCreate(ctx, user.ID, login.ID, cand, s.MFACfg.EnrollmentExpiration)
	if err != nil {
		return "", domain.InternalErr("lưu secret TOTP được mời", err)
	}
	return v, nil
}

func (s *Service) offered(ctx context.Context, user *fhir.User, login *fhir.Login) (string, error) {
	v, err := s.MFAState.OfferGet(ctx, user.ID, login.ID)
	if err != nil {
		return "", domain.InternalErr("đọc secret TOTP được mời", err)
	}
	return v, nil
}

// buildEnrollment dựng URI otpauth và mã QR. Secret CHƯA thành User.mfaSecret: chỉ thành khi chính login này xác nhận bằng mã.
func (s *Service) buildEnrollment(ctx context.Context, login *fhir.Login, user *fhir.User, project *fhir.Project) (uri, qr string, err error) {
	if err := s.requireMFA(); err != nil {
		return "", "", err
	}
	secret, err := s.offer(ctx, user, login)
	if err != nil {
		return "", "", err
	}
	account := user.Email
	if account == "" {
		account = user.ID
	}
	uri = s.TOTP.KeyURI(domain.MFAIssuer(project, s.Mail.appName()), account, secret)
	if qr, err = s.QR.DataURL(uri); err != nil {
		return "", "", domain.InternalErr("tạo mã QR", err)
	}
	return uri, qr, nil
}

// ---- secret lưu trong User

func (s *Service) openSecret(user *fhir.User) (string, error) {
	plain, err := s.MFASecrets.Open(user.MfaSecret)
	if err != nil {
		return "", domain.InternalErr("giải mã mfaSecret", err)
	}
	return plain, nil
}

// enrollTotp ghi secret vào User bằng Mutate (đọc bản mới nhất, ghi kèm điều kiện phiên bản): hai login cùng đăng ký một lúc thì đúng một cái thắng,
// cái còn lại nhận "Already enrolled". notEnrolledAtAll: yêu cầu user chưa đăng ký MỘT phương thức nào (đăng nhập), ngược lại chỉ cần chưa có TOTP.
func (s *Service) enrollTotp(ctx context.Context, userID, secret string, notEnrolledAtAll bool) error {
	sealed, err := s.MFASecrets.Seal(secret)
	if err != nil {
		return domain.InternalErr("mã hóa mfaSecret", err)
	}
	_, err = s.Users.Mutate(ctx, userID, func(u *fhir.User) error {
		if (notEnrolledAtAll && domain.IsTrue(u.MfaEnrolled)) || domain.HasMFAMethod(u, domain.MFATotp) {
			return domain.BadRequest("Already enrolled")
		}
		u.MfaSecret, u.MfaEnrolled, u.MfaMethod = sealed, ptrTrue(), domain.AddMFAMethod(u, domain.MFATotp)
		return nil
	})
	return err
}

// ---- mã TOTP

// verifyTotp kiểm mã và TIÊU THỤ nó: mỗi mã chỉ được chấp nhận một lần cho mỗi user, nhớ (2*window+2) bước. Lỗi kho trạng thái là lỗi (fail đóng).
func (s *Service) verifyTotp(ctx context.Context, userID, secret, token string) (bool, error) {
	cfg := s.MFACfg
	if !s.TOTP.Verify(secret, token, s.Clock.Now(), cfg.Window) {
		return false, nil
	}
	ok, err := s.MFAState.ClaimTOTP(ctx, userID, token, time.Duration(2*cfg.Window+2)*domain.TOTPStep)
	if err != nil {
		return false, domain.InternalErr("ghi nhận mã TOTP đã dùng", err)
	}
	return ok, nil
}

// ---- mã email

func newCode6() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// sendEmailCode sinh mã 6 chữ số, lưu bcrypt của nó trên login (gửi lại GHI ĐÈ mã cũ) rồi gửi thư (auth/utils.ts:346). Có giới hạn số lần gửi mỗi login.
func (s *Service) sendEmailCode(ctx context.Context, login *fhir.Login, user *fhir.User, project *fhir.Project) error {
	if err := s.requireMFA(); err != nil {
		return err
	}
	if s.Mailer == nil {
		return domain.Internal("không có kênh gửi thư để gửi mã MFA")
	}
	if user.Email == "" {
		return domain.BadRequest("User has no email address")
	}
	cfg := s.MFACfg
	_, retry, ok, err := s.MFAState.Hit(ctx, "send:"+login.ID, cfg.EmailSendLimit, cfg.EmailCodeExpiration)
	if err != nil {
		return domain.InternalErr("giới hạn gửi mã email", err)
	}
	if !ok {
		return domain.TooManyRequests(retry)
	}
	code, err := newCode6()
	if err != nil {
		return domain.InternalErr("sinh mã email", err)
	}
	hash, err := s.Hasher.Hash(code)
	if err != nil {
		return domain.InternalErr("băm mã email", err)
	}
	expires := s.Clock.Now().Add(cfg.EmailCodeExpiration).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		l.EmailMfa = &fhir.LoginEmailMfa{CodeHash: hash, ExpiresAt: expires}
		return nil
	}); err != nil {
		return err
	}
	if err := s.Mailer.Send(ctx, mfaCodeMessage(domain.MFAAppName(project, s.Mail.appName()), user.Email, code, int(cfg.EmailCodeExpiration/time.Minute))); err != nil {
		return domain.InternalErr("gửi mã MFA", err)
	}
	return nil
}

func mfaCodeMessage(app, to, code string, minutes int) Message {
	return Message{To: to, Subject: "Your " + app + " verification code: " + code, Text: "Below is your requested " + app +
		" verification code. You can copy it into the open browser window to confirm your login.\n\n" + code + "\n\n" +
		fmt.Sprintf("This code will expire in %d minutes. If you did not try to sign in, you can safely ignore this email.\n\n", minutes) +
		"Thank you,\nThe " + app + " Team\n"}
}

// emailCodeValid: login có mã email, còn hạn, và token khớp bcrypt.
func (s *Service) emailCodeValid(login *fhir.Login, token string) bool {
	e := login.EmailMfa
	if e == nil || token == "" || emailCodeExpired(e, s.Clock.Now()) {
		return false
	}
	return s.Hasher.Compare(token, e.CodeHash)
}

func emailCodeExpired(e *fhir.LoginEmailMfa, now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, e.ExpiresAt)
	return err != nil || !now.Before(t)
}

func (s *Service) clearEmailCode(ctx context.Context, loginID string) error {
	_, err := s.Logins.Mutate(ctx, loginID, func(l *fhir.Login) error {
		if l.EmailMfa == nil {
			return ErrNoChange
		}
		l.EmailMfa = nil
		return nil
	})
	return err
}

// ---- hạn mức thử

// reserve đặt chỗ một lượt thử TRƯỚC khi biết mã đúng hay sai; hết hạn mức là 429 kèm thời gian chờ.
func (s *Service) reserve(ctx context.Context, user *fhir.User, login *fhir.Login) (AttemptResult, error) {
	cfg := s.MFACfg
	r, err := s.MFAState.ReserveAttempt(ctx, user.ID, login.ID, cfg.LoginAttemptLimit, cfg.UserAttemptLimit, cfg.LoginExpiration)
	if err != nil {
		return AttemptResult{}, domain.InternalErr("đặt chỗ lượt thử MFA", err)
	}
	if !r.Allowed {
		return r, domain.TooManyRequests(r.RetryAfter)
	}
	return r, nil
}

// failed xử lý một lần sai: từ lần thứ LoginAttemptLimit thì thu hồi login (bằng Mutate: không làm sống lại login khác, không ghi đè bản cũ).
func (s *Service) failed(ctx context.Context, login *fhir.Login, r AttemptResult) {
	if r.LoginCount < s.MFACfg.LoginAttemptLimit {
		return
	}
	if _, err := s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		if domain.IsTrue(l.Revoked) {
			return ErrNoChange
		}
		l.Revoked = ptrTrue()
		return nil
	}); err != nil {
		s.Logger.ErrorContext(ctx, "không thu hồi được login sau khi nhập sai MFA", "login_id", login.ID, "error", err)
	}
}

func (s *Service) release(ctx context.Context, user *fhir.User, login *fhir.Login) {
	if err := s.MFAState.ReleaseAttempt(ctx, user.ID, login.ID); err != nil {
		s.Logger.WarnContext(ctx, "Failed to release MFA attempt", "login_id", login.ID, "error", err)
	}
}

// ---- xác minh khi đăng nhập

// verifyToken là lõi của Verify và LoginEnroll (oauth/utils.ts:347). enrollSecret (nếu có) là secret được mời, sẽ thành yếu tố nếu mã đúng.
func (s *Service) verifyToken(ctx context.Context, login *fhir.Login, token, enrollSecret string) (*fhir.Login, error) {
	cfg := s.MFACfg
	now := s.Clock.Now()
	if err := domain.AssertMFALoginActive(login, now, cfg.LoginExpiration); err != nil {
		return nil, err
	}
	user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
	if err != nil {
		return nil, err
	}
	emailMfa := login.EmailMfa
	if enrollSecret != "" {
		emailMfa = nil // mã xác nhận đăng ký TOTP không được là mã email
		if domain.IsTrue(user.MfaEnrolled) {
			return nil, domain.BadRequest("Already enrolled")
		}
	}
	secret := enrollSecret
	if secret == "" && domain.HasMFAMethod(user, domain.MFATotp) {
		if secret, err = s.openSecret(user); err != nil {
			return nil, err
		}
	}
	if emailMfa != nil && emailCodeExpired(emailMfa, now) {
		// Q6: mã email hết hạn chỉ chặn khi nó là yếu tố duy nhất; có TOTP thì bỏ qua nó (Medplum chặn cả mã TOTP đúng tới khi gửi mã mới)
		if secret == "" {
			return nil, domain.BadRequest("MFA code expired")
		}
		emailMfa = nil
	}
	if emailMfa == nil && secret == "" {
		return nil, domain.BadRequest("User not enrolled in MFA")
	}
	r, err := s.reserve(ctx, user, login)
	if err != nil {
		return nil, err
	}
	verified, emailOK := false, false
	if emailMfa != nil {
		emailOK = s.Hasher.Compare(token, emailMfa.CodeHash)
		verified = emailOK
	}
	if !verified && secret != "" {
		if verified, err = s.verifyTotp(ctx, user.ID, secret, token); err != nil {
			return nil, err
		}
	}
	if !verified {
		s.failed(ctx, login, r)
		return nil, domain.BadRequest("Invalid MFA token") // cùng thông báo cho TOTP và email: không lộ yếu tố nào đúng
	}
	if emailOK && !domain.IsTrue(user.EmailVerified) {
		if _, err := s.Users.Mutate(ctx, user.ID, func(u *fhir.User) error {
			if domain.IsTrue(u.EmailVerified) {
				return ErrNoChange
			}
			u.EmailVerified = ptrTrue()
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if enrollSecret != "" {
		// đăng ký TRƯỚC khi đánh dấu login đã xác minh: login thua cuộc đua đăng ký không được mfaVerified
		if err := s.enrollTotp(ctx, user.ID, enrollSecret, true); err != nil {
			return nil, err
		}
		if err := s.MFAState.OfferDelete(ctx, user.ID, login.ID); err != nil {
			s.Logger.WarnContext(ctx, "không xóa được secret được mời", "login_id", login.ID, "error", err)
		}
	}
	updated, err := s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		if domain.IsTrue(l.Revoked) {
			return domain.BadRequest("Login revoked") // không làm sống lại login vừa bị thu hồi
		}
		l.MfaVerified = ptrTrue()
		if emailOK {
			l.EmailMfa = nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.release(ctx, user, login)
	return updated, nil
}

// MFAVerify xử lý POST /auth/mfa/verify (mfa.ts:229).
func (s *Service) MFAVerify(ctx context.Context, loginID, token string) (*LoginResult, *fhir.Login, error) {
	if err := s.requireMFA(); err != nil {
		return nil, nil, err
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return nil, nil, err
	}
	user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
	if err != nil {
		return nil, nil, err
	}
	enrollSecret := ""
	if !domain.IsTrue(user.MfaEnrolled) && domain.MFAMethodAllowed(s.LoginProject(ctx, login), domain.MFATotp) {
		// người dùng bắt buộc MFA chưa đăng ký nhập mã cho secret được mời của CHÍNH login này thì được đăng ký luôn
		if enrollSecret, err = s.offered(ctx, user, login); err != nil {
			return nil, nil, err
		}
	}
	updated, err := s.verifyToken(ctx, login, token, enrollSecret)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.BuildLoginResult(ctx, updated)
	return res, updated, err
}

// MFALoginEnroll xử lý POST /auth/mfa/login-enroll (mfa.ts:105): đăng ký phương thức MFA đầu tiên ngay trong lúc đăng nhập.
func (s *Service) MFALoginEnroll(ctx context.Context, loginID string, method any, token string) (*LoginResult, *fhir.Login, error) {
	if err := s.requireMFA(); err != nil {
		return nil, nil, err
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return nil, nil, err
	}
	user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
	if err != nil {
		return nil, nil, err
	}
	if domain.IsTrue(user.MfaEnrolled) {
		return nil, nil, domain.BadRequest("Already enrolled")
	}
	if err := domain.AssertMFALoginActive(login, s.Clock.Now(), s.MFACfg.LoginExpiration); err != nil {
		return nil, nil, err
	}
	m := domain.ParseMFAMethod(method)
	project := s.LoginProject(ctx, login)
	if !domain.MFAMethodAllowed(project, m) {
		return nil, nil, domain.BadRequest("MFA method not allowed")
	}
	if m == domain.MFAEmail {
		if _, err := s.Users.Mutate(ctx, user.ID, func(u *fhir.User) error {
			if domain.IsTrue(u.MfaEnrolled) {
				return domain.BadRequest("Already enrolled")
			}
			u.MfaEnrolled, u.MfaMethod = ptrTrue(), domain.AddMFAMethod(u, domain.MFAEmail)
			return nil
		}); err != nil {
			return nil, nil, err
		}
		// email là phương thức duy nhất và login chưa có mã: BuildLoginResult tự gửi mã và trả mfaRequired
		res, err := s.BuildLoginResult(ctx, login)
		return res, login, err
	}
	secret, err := s.offered(ctx, user, login)
	if err != nil {
		return nil, nil, err
	}
	if secret == "" {
		return nil, nil, domain.BadRequest("Secret not found")
	}
	if token == "" {
		return nil, nil, domain.BadRequest("Missing token")
	}
	updated, err := s.verifyToken(ctx, login, token, secret)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.BuildLoginResult(ctx, updated)
	return res, updated, err
}

// MFASendEmail xử lý POST /auth/mfa/send-email (mfa.ts:250): gửi (lại) mã email cho login đang chờ MFA.
func (s *Service) MFASendEmail(ctx context.Context, loginID string) error {
	if err := s.requireMFA(); err != nil {
		return err
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return err
	}
	if err := domain.AssertMFALoginActive(login, s.Clock.Now(), s.MFACfg.LoginExpiration); err != nil {
		return err
	}
	user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
	if err != nil {
		return err
	}
	if !domain.IsTrue(user.MfaEnrolled) || !domain.HasMFAMethod(user, domain.MFAEmail) {
		return domain.BadRequest("User not enrolled in email MFA")
	}
	return s.sendEmailCode(ctx, login, user, s.LoginProject(ctx, login))
}

// ---- quản lý yếu tố của chính mình (Bearer)

// accountUser đọc user của người gọi cho các route quản lý chính tài khoản mình (readUserForAccountManagement): chủ thể phải là User, không có scope SMART,
// và user toàn cục chỉ tự quản lý được khi đăng nhập bằng mật khẩu hoặc Google.
func (s *Service) accountUser(ctx context.Context, a *AuthState) (*fhir.User, error) {
	ref, err := callerUserRef(a)
	if err != nil {
		return nil, err
	}
	user, err := s.Users.Get(ctx, idOf(ref))
	if err != nil {
		return nil, err
	}
	if user.Project == nil && !domain.CanManageUserAccount(a.Login) {
		return nil, domain.Forbidden("Forbidden")
	}
	return user, nil
}

// MFAStatusResult là phản hồi GET /auth/mfa/status.
type MFAStatusResult struct {
	Enrolled        bool     `json:"enrolled" binding:"required" example:"false"`                                                   // người dùng đã đăng ký ít nhất một phương thức MFA
	EnrolledMethods []string `json:"enrolledMethods" binding:"required" enums:"totp,email" example:"totp"`                          // các phương thức MFA đã đăng ký
	AllowedMethods  []string `json:"allowedMethods" binding:"required" enums:"totp,email" example:"totp"`                           // các phương thức MFA project cho phép
	MfaRequired     bool     `json:"mfaRequired" binding:"required" example:"false"`                                                // project có bắt buộc MFA hay không
	Email           string   `json:"email,omitempty" example:"user@example.com"`                                                    // email của tài khoản (luôn được điền nếu user có email)
	EnrollURI       string   `json:"enrollUri,omitempty" example:"otpauth://totp/Trivita:user@example.com?secret=JBSWY3DPEHPK3PXP"` // otpauth URI để đăng ký TOTP; chỉ có khi người dùng chưa đăng ký TOTP
	EnrollQRCode    string   `json:"enrollQrCode,omitempty"`                                                                        // mã QR của enrollUri dạng data URI; chỉ có khi người dùng chưa đăng ký TOTP
}

// MFAStatus xử lý GET /auth/mfa/status (mfa.ts:80).
func (s *Service) MFAStatus(ctx context.Context, a *AuthState) (*MFAStatusResult, error) {
	if err := s.requireMFA(); err != nil {
		return nil, err
	}
	user, err := s.accountUser(ctx, a)
	if err != nil {
		return nil, err
	}
	enrolled := domain.EnrolledMFAMethods(user)
	if enrolled == nil {
		enrolled = []string{}
	}
	res := &MFAStatusResult{Enrolled: domain.IsTrue(user.MfaEnrolled), EnrolledMethods: enrolled, AllowedMethods: domain.AllowedMFAMethods(a.Project),
		MfaRequired: domain.IsMFARequired(user, a.Project), Email: user.Email}
	if !domain.HasMFAMethod(user, domain.MFATotp) {
		if res.EnrollURI, res.EnrollQRCode, err = s.buildEnrollment(ctx, a.Login, user, a.Project); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// guardFactor chạy việc kiểm một yếu tố với hạn mức thử (L-M1: enroll và disable cũng bị giới hạn, Medplum thì không): đặt chỗ, kiểm, sai thì có thể thu hồi login,
// đúng thì trả lại lượt. check trả (đúng?, lỗi hạ tầng).
func (s *Service) guardFactor(ctx context.Context, user *fhir.User, login *fhir.Login, check func() (bool, error)) error {
	r, err := s.reserve(ctx, user, login)
	if err != nil {
		return err
	}
	ok, err := check()
	if err != nil {
		return err
	}
	if !ok {
		s.failed(ctx, login, r)
		return domain.BadRequest("Invalid token")
	}
	s.release(ctx, user, login)
	return nil
}

// MFAEnroll xử lý POST /auth/mfa/enroll (mfa.ts:162): thêm một phương thức cho chính mình bằng cách chứng minh đang giữ yếu tố đó.
func (s *Service) MFAEnroll(ctx context.Context, a *AuthState, method any, token string) error {
	if err := s.requireMFA(); err != nil {
		return err
	}
	user, err := s.accountUser(ctx, a)
	if err != nil {
		return err
	}
	m := domain.ParseMFAMethod(method)
	if domain.HasMFAMethod(user, m) {
		return domain.BadRequest("Already enrolled")
	}
	if !domain.MFAMethodAllowed(a.Project, m) {
		return domain.BadRequest("MFA method not allowed")
	}
	login, err := s.Logins.Get(ctx, a.Login.ID)
	if err != nil {
		return err
	}
	if m == domain.MFAEmail {
		if token == "" {
			return domain.BadRequest("Missing token")
		}
		if err := s.guardFactor(ctx, user, login, func() (bool, error) { return s.emailCodeValid(login, token), nil }); err != nil {
			return err
		}
		if _, err := s.Users.Mutate(ctx, user.ID, func(u *fhir.User) error {
			u.MfaEnrolled, u.MfaMethod, u.EmailVerified = ptrTrue(), domain.AddMFAMethod(u, domain.MFAEmail), ptrTrue()
			return nil
		}); err != nil {
			return err
		}
		return s.clearEmailCode(ctx, login.ID)
	}
	secret, err := s.offered(ctx, user, login)
	if err != nil {
		return err
	}
	if secret == "" {
		return domain.BadRequest("Secret not found")
	}
	if token == "" {
		return domain.BadRequest("Missing token")
	}
	// mã này KHÔNG bị tiêu thụ (xác nhận bộ xác thực không cấp quyền gì, mfa.ts:217): vẫn dùng được cho lần đăng nhập trong cùng cửa sổ
	if err := s.guardFactor(ctx, user, login, func() (bool, error) {
		return s.TOTP.Verify(secret, token, s.Clock.Now(), s.MFACfg.Window), nil
	}); err != nil {
		return err
	}
	if err := s.enrollTotp(ctx, user.ID, secret, false); err != nil {
		return err
	}
	if err := s.MFAState.OfferDelete(ctx, user.ID, login.ID); err != nil {
		s.Logger.WarnContext(ctx, "không xóa được secret được mời", "login_id", login.ID, "error", err)
	}
	return nil
}

// MFASendEmailChallenge xử lý POST /auth/mfa/send-email-challenge (mfa.ts:282): gửi mã email cho phiên đang dùng, để đăng ký email hoặc thỏa yếu tố khi tắt MFA.
func (s *Service) MFASendEmailChallenge(ctx context.Context, a *AuthState) error {
	if err := s.requireMFA(); err != nil {
		return err
	}
	user, err := s.accountUser(ctx, a)
	if err != nil {
		return err
	}
	if !domain.HasMFAMethod(user, domain.MFAEmail) && !domain.MFAMethodAllowed(a.Project, domain.MFAEmail) {
		return domain.BadRequest("Email MFA not available")
	}
	login, err := s.Logins.Get(ctx, a.Login.ID)
	if err != nil {
		return err
	}
	return s.sendEmailCode(ctx, login, user, a.Project)
}

// MFADisableRequest là yêu cầu POST /auth/mfa/disable; Method và Token là giá trị thô trong body (nil: không có) để kiểm kiểu SAU khi biết user đã đăng ký.
type MFADisableRequest struct {
	Method, Token any
}

// MFADisable xử lý POST /auth/mfa/disable (mfa.ts:297): gỡ một hoặc mọi yếu tố, phải chứng minh một yếu tố đang có và không gỡ yếu tố cuối khi MFA bắt buộc.
func (s *Service) MFADisable(ctx context.Context, a *AuthState, req MFADisableRequest) error {
	if err := s.requireMFA(); err != nil {
		return err
	}
	user, err := s.accountUser(ctx, a)
	if err != nil {
		return err
	}
	if !domain.IsTrue(user.MfaEnrolled) {
		return domain.BadRequest("User not enrolled in MFA") // kiểm TRƯỚC validator như Medplum
	}
	var method, token string
	if req.Token != nil {
		t, ok := req.Token.(string)
		if !ok {
			return domain.BadRequestAt("Token must be a string", "token")
		}
		token = t
	}
	if req.Method != nil {
		m, ok := req.Method.(string)
		if !ok {
			return domain.BadRequestAt("Method must be a string", "method")
		}
		method = m
	}
	enrolled := domain.EnrolledMFAMethods(user)
	remaining := []string{}
	if req.Method != nil {
		if method != domain.MFATotp && method != domain.MFAEmail {
			return domain.BadRequest("Invalid method")
		}
		if !domain.HasMFAMethod(user, method) {
			return domain.BadRequest("User not enrolled in MFA method")
		}
		for _, x := range enrolled {
			if x != method {
				remaining = append(remaining, x)
			}
		}
	}
	if domain.IsMFARequired(user, a.Project) && len(remaining) == 0 {
		return domain.BadRequest("Cannot remove the last MFA factor because MFA is required")
	}
	if token == "" {
		return domain.BadRequest("Missing token")
	}
	login, err := s.Logins.Get(ctx, a.Login.ID)
	if err != nil {
		return err
	}
	if err := s.guardFactor(ctx, user, login, func() (bool, error) { return s.connectedFactor(ctx, user, login, token) }); err != nil {
		return err
	}
	removeTotp := req.Method == nil || method == domain.MFATotp
	newSecret := ""
	if removeTotp {
		// secret cũ bị thay bằng secret ngẫu nhiên mới (không để lại secret đã lộ ra trong DB), kể cả khi không còn yếu tố nào
		raw, err := s.TOTP.GenerateSecret()
		if err != nil {
			return domain.InternalErr("sinh secret TOTP", err)
		}
		if newSecret, err = s.MFASecrets.Seal(raw); err != nil {
			return domain.InternalErr("mã hóa mfaSecret", err)
		}
	}
	if _, err := s.Users.Mutate(ctx, user.ID, func(u *fhir.User) error {
		left := []string{}
		for _, x := range domain.EnrolledMFAMethods(u) {
			if req.Method != nil && x != method {
				left = append(left, x)
			}
		}
		u.MfaEnrolled, u.MfaMethod = ptrBool(len(left) > 0), left
		if removeTotp {
			u.MfaSecret = newSecret
		}
		return nil
	}); err != nil {
		return err
	}
	return s.clearEmailCode(ctx, login.ID)
}

func ptrBool(b bool) *bool { return &b }

// connectedFactor kiểm token là mã của MỘT yếu tố user đang có (mfa.ts:57): TOTP (tiêu thụ mã) hoặc mã email còn hạn.
func (s *Service) connectedFactor(ctx context.Context, user *fhir.User, login *fhir.Login, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	if domain.HasMFAMethod(user, domain.MFATotp) && user.MfaSecret != "" {
		secret, err := s.openSecret(user)
		if err != nil {
			return false, err
		}
		ok, err := s.verifyTotp(ctx, user.ID, secret, token)
		if err != nil || ok {
			return ok, err
		}
	}
	if domain.HasMFAMethod(user, domain.MFAEmail) && s.emailCodeValid(login, token) {
		return true, nil
	}
	return false, nil
}
