package application

import (
	"errors"
	"log/slog"

	"trivita-fhir/pkg/fhir"
)

// Deps là mọi cổng mà Service cần; được lắp ở bootstrap.
type Deps struct {
	Users            UserRepository
	Logins           LoginRepository
	Memberships      MembershipRepository
	DomainConfigs    DomainConfigRepository
	UserConfigs      UserConfigurationRepository
	Projects         ProjectRepository
	Clients          ClientRepository
	Launches         LaunchRepository
	AccessPolicies   AccessPolicyRepository
	SecurityRequests SecurityRequestRepository
	Tx               Transactor
	Resources        ResourceWriter
	Hasher           PasswordHasher
	Pwned            PwnedChecker
	MFA              MFAGate
	// Mailer gửi thư đặt lại mật khẩu và xác minh email; nil thì các luồng này không gửi thư (như Medplum khi chưa cấu hình email).
	Mailer Mailer
	Mail   MailConfig
	// Recaptcha xác minh token reCAPTCHA; RecaptchaKeys là khóa mức máy chủ (rỗng: không kiểm).
	Recaptcha     Recaptcha
	RecaptchaKeys RecaptchaConfig
	// MFA (P5): TOTP, QR, MFAState (secret được mời, hạn mức, chống dùng lại mã), MFASecrets (mã hóa User.mfaSecret khi lưu) và MFACfg. Thiếu bất kỳ cái
	// nào trong TOTP, QR, MFAState, MFASecrets thì nhánh cần MFA vẫn FAIL ĐÓNG (FailClosedMFA) và các route /auth/mfa/* trả lỗi.
	TOTP       TOTP
	QR         QR
	MFAState   MFAState
	MFASecrets SecretBox
	MFACfg     MFAConfig
	// OIDC, IDVerifier và External: đăng nhập qua IdP ngoài (P6).
	OIDC       OIDCClient
	IDVerifier IDTokenVerifier
	External   ExternalConfig
	// Google xác minh credential của /auth/google; GoogleClientID là Google client chính (rỗng: chỉ nhận client của project). nil: /auth/google tắt.
	Google         GoogleVerifier
	GoogleClientID string
	// OutboundURL kiểm URL của IdP ngoài khi cấu hình (nil: không cấu hình được IdP).
	OutboundURL OutboundURLCheck
	// Registration là các cờ đăng ký (zero value: bật hết, không bắt xác minh email).
	Registration RegistrationConfig
	// BlockedEmailDomains là các tên miền email KHÔNG được mời làm Practitioner (luôn chặn, ưu tiên hơn allowedPractitionerEmailDomain của project).
	BlockedEmailDomains []string
	// Scim là cấu hình SCIM 2.0 (P7, /scim/v2/Users).
	Scim ScimConfig
	// EmailSender, EmailAttachments và Email: route POST /email/v1/send (E4). EmailSender nil: chưa có đường gửi.
	EmailSender      EmailSender
	EmailAttachments AttachmentFetcher
	// EmailBinaries đọc tệp đính kèm Binary/<id> (E5); nil: Binary/<id> bị từ chối là chưa hỗ trợ.
	EmailBinaries BinarySource
	Email         EmailConfig
	UserAgents    UserAgentParser
	// BaseURL là địa chỉ công khai của auth (có hoặc không có dấu / cuối); dùng dựng redirect_uri của IdP.
	BaseURL string
	Clock   Clock
	Logger  *slog.Logger
	// StandardClients là client dựng sẵn, được tra trước khi tìm trong kho.
	StandardClients []fhir.ClientApplication
	// Signer ký và Verifier xác minh token của auth (P3). Nil: chưa phát token, mọi route Bearer trả 401.
	Signer   TokenSigner
	Verifier TokenVerifier
	Tokens   TokenConfig
	// Assertions xác minh client_assertion (private_key_jwt) và JTIs chống phát lại; nil thì private_key_jwt bị từ chối.
	Assertions ClientAssertionVerifier
	JTIs       JTIStore
	// OAuthRegister bật POST /oauth2/register (tra client dựng sẵn theo redirect_uri).
	OAuthRegister bool
	// FHIRcast bật hub.url và hub.topic trong phản hồi token khi scope có `fhircast/` (nil: tắt).
	FHIRcast *HubConfig
}

// Service chứa các use case lõi của auth (P1: tra cứu, membership, scope, thu hồi, mật khẩu, security request).
// P2 đến P6 thêm use case cùng gói này.
type Service struct{ Deps }

// NewService dựng Service, điền Clock và Logger mặc định.
func NewService(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = SystemClock{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	d.MFACfg = d.MFACfg.withDefaults()
	svc := &Service{Deps: d}
	if svc.MFA == nil {
		if svc.mfaReady() {
			svc.MFA = mfaGate{svc}
		} else {
			svc.MFA = FailClosedMFA{}
		}
	}
	return svc
}

// UseMFA bật MFA thật sau khi các phụ thuộc (TOTP, QR, MFAState, MFASecrets, MFACfg) được gán vào Deps lúc lắp ghép muộn (container gán sau NewService).
// Thiếu phụ thuộc thì giữ FailClosedMFA và trả lỗi: không có đường nào bỏ qua MFA vì cấu hình dở.
func (s *Service) UseMFA() error {
	s.MFACfg = s.MFACfg.withDefaults()
	if !s.mfaReady() {
		s.MFA = FailClosedMFA{}
		return errors.New("application: thiếu phụ thuộc MFA (TOTP, QR, MFAState, MFASecrets)")
	}
	s.MFA = mfaGate{s}
	return nil
}
