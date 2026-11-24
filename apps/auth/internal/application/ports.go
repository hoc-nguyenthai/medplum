package application

import (
	"context"
	"errors"
	"time"

	"trivita-fhir/pkg/fhir"
)

// Clock cho phép test điều khiển thời gian (hết hạn mã, cửa sổ hạn mức...).
type Clock interface{ Now() time.Time }

// SystemClock là Clock thật.
type SystemClock struct{}

// Now trả thời điểm hiện tại.
func (SystemClock) Now() time.Time { return time.Now() }

// ErrNoChange: fn của Mutate trả lỗi này để báo "không cần ghi"; Mutate trả bản hiện tại và nil (tránh tăng phiên bản vô ích).
var ErrNoChange = errors.New("application: không có thay đổi")

// Quy ước chung của các kho (repository): đọc một resource không có thì trả *domain.Error NotFound; cập nhật có điều
// kiện (IfMatch) mà phiên bản đã đổi thì trả domain.ErrVersionConflict; lỗi hạ tầng là *domain.Error Internal. Các hàm
// Mutate đọc bản MỚI NHẤT, áp fn rồi ghi kèm IfMatch và tự thử lại khi xung đột, nên không bao giờ ghi đè một bản cũ
// (Medplum dùng patchResource cho việc này; pkg/fhirrouter.Repository không có PatchResource).

// UserRepository là kho User.
type UserRepository interface {
	// FindInProject tìm user theo email (chữ thường) gắn với project; nil nếu không có.
	FindInProject(ctx context.Context, email, projectID string) (*fhir.User, error)
	// FindGlobal tìm user theo email không gắn project nào; nil nếu không có.
	FindGlobal(ctx context.Context, email string) (*fhir.User, error)
	Get(ctx context.Context, id string) (*fhir.User, error)
	Mutate(ctx context.Context, id string, fn func(*fhir.User) error) (*fhir.User, error)
	// Delete xóa user (đánh dấu xóa của FHIR; lịch sử còn).
	Delete(ctx context.Context, id string) error
}

// LoginRepository là kho Login.
type LoginRepository interface {
	Create(ctx context.Context, l *fhir.Login) (*fhir.Login, error)
	Get(ctx context.Context, id string) (*fhir.Login, error)
	// FindByPreAuthorizedCodeHash tìm login theo SHA-256 hex của pre-authorized_code; nil nếu không có.
	FindByPreAuthorizedCodeHash(ctx context.Context, hash string) (*fhir.Login, error)
	// FindByCookie tìm login theo giá trị đang lưu ở Login.cookie (băm SHA-256 của cookie); nil nếu không có.
	FindByCookie(ctx context.Context, cookieHash string) (*fhir.Login, error)
	// FindByCode tìm login theo giá trị đang lưu ở Login.code (băm SHA-256 của code, không phải code gốc); nil nếu không có.
	FindByCode(ctx context.Context, codeHash string) (*fhir.Login, error)
	// UpdateIfMatch ghi l kèm điều kiện versionID; xung đột thì domain.ErrVersionConflict.
	UpdateIfMatch(ctx context.Context, l *fhir.Login, versionID string) (*fhir.Login, error)
	Mutate(ctx context.Context, id string, fn func(*fhir.Login) error) (*fhir.Login, error)
	// ListIDsByUser trả id MỌI Login của user (gom hết trước khi người gọi sửa từng cái, để phân trang không bỏ sót).
	ListIDsByUser(ctx context.Context, userRef string) ([]string, error)
	// ListByUserSince trả các Login của user có lastUpdated > since.
	ListByUserSince(ctx context.Context, userRef string, since time.Time) ([]fhir.Login, error)
	// ListIDsUpdatedBefore trả id mọi Login có lastUpdated < before.
	ListIDsUpdatedBefore(ctx context.Context, before time.Time) ([]string, error)
	// Expunge xóa vĩnh viễn một Login cùng lịch sử (không hoàn tác được).
	Expunge(ctx context.Context, id string) error
}

// ResourceWriter tạo resource bất kỳ (project, access policy, client, profile, membership...). Dùng cho các thao tác quản trị
// cần ghi nhiều loại resource trong cùng một transaction.
type ResourceWriter interface {
	// Create ghi resource mới; nếu r đã có id thì dùng id đó (trùng id là Conflict).
	Create(ctx context.Context, r fhir.Resource) (fhir.Resource, error)
	// Delete xóa resource theo loại và id.
	Delete(ctx context.Context, resourceType, id string) error
	// Get đọc resource theo loại và id (không có thì NotFound).
	Get(ctx context.Context, resourceType, id string) (fhir.Resource, error)
}

// MembershipRepository là kho ProjectMembership.
type MembershipRepository interface {
	// Get đọc một membership theo id (không có thì NotFound).
	Get(ctx context.Context, id string) (*fhir.ProjectMembership, error)
	// ListForUser trả membership của user có `active` khác false; projectRef (nếu có) lọc theo project; tối đa limit.
	ListForUser(ctx context.Context, userRef, projectRef string, limit int) ([]fhir.ProjectMembership, error)
	// FindByExternalID tìm membership có externalId KHỚP CHÍNH XÁC trong project (không LIKE, không tách dấu phẩy); nil nếu không có.
	FindByExternalID(ctx context.Context, externalID, projectRef string) (*fhir.ProjectMembership, error)
	// FindByProfile tìm membership có profile khớp chính xác (ví dụ "Practitioner/id") trong project; nil nếu không có. Kể cả membership không hoạt động (người gọi tự kiểm).
	FindByProfile(ctx context.Context, profileRef, projectRef string) (*fhir.ProjectMembership, error)
	// Mutate đọc bản mới nhất, áp fn rồi ghi kèm IfMatch (thử lại khi xung đột); fn trả ErrNoChange để không ghi.
	Mutate(ctx context.Context, id string, fn func(*fhir.ProjectMembership) error) (*fhir.ProjectMembership, error)
	// Delete xóa membership.
	Delete(ctx context.Context, id string) error
	// ListAllForUser trả MỌI membership của user (kể cả không hoạt động), tối đa limit.
	ListAllForUser(ctx context.Context, userRef string, limit int) ([]fhir.ProjectMembership, error)
	// ListByProject trả MỌI membership của project (kể cả không hoạt động).
	ListByProject(ctx context.Context, projectRef string) ([]fhir.ProjectMembership, error)
	// ListActiveAdmins trả các membership admin còn hoạt động của project.
	ListActiveAdmins(ctx context.Context, projectRef string) ([]fhir.ProjectMembership, error)
}

// ProjectRepository là kho Project.
type ProjectRepository interface {
	Get(ctx context.Context, id string) (*fhir.Project, error)
	// FindByRecaptchaSiteKey tìm project có Project.site.recaptchaSiteKey = siteKey; projectID (nếu có) giới hạn đúng project đó. nil nếu không có.
	FindByRecaptchaSiteKey(ctx context.Context, siteKey, projectID string) (*fhir.Project, error)
	// HasOwnedBy cho biết user đã sở hữu (Project.owner) ít nhất một project.
	HasOwnedBy(ctx context.Context, userRef string) (bool, error)
	// ByGoogleClientID tìm project có Project.site.googleClientId = googleClientID; projectID (nếu có) giới hạn đúng project đó.
	ByGoogleClientID(ctx context.Context, googleClientID, projectID string) ([]fhir.Project, error)
	// Mutate đọc bản mới nhất, áp fn rồi ghi kèm IfMatch (thử lại khi xung đột); fn trả ErrNoChange để không ghi.
	Mutate(ctx context.Context, id string, fn func(*fhir.Project) error) (*fhir.Project, error)
}

// ClientRepository là kho ClientApplication (client dựng sẵn được tra trước ở Service).
type ClientRepository interface {
	Get(ctx context.Context, id string) (*fhir.ClientApplication, error)
	// Mutate đọc bản mới nhất, áp fn rồi ghi kèm IfMatch (thử lại khi xung đột); fn trả ErrNoChange để không ghi.
	Mutate(ctx context.Context, id string, fn func(*fhir.ClientApplication) error) (*fhir.ClientApplication, error)
}

// LaunchRepository là kho SmartAppLaunch.
type LaunchRepository interface {
	Get(ctx context.Context, id string) (*fhir.SmartAppLaunch, error)
}

// AccessPolicyRepository là kho AccessPolicy.
type AccessPolicyRepository interface {
	Get(ctx context.Context, id string) (*fhir.AccessPolicy, error)
}

// SecurityRequestRepository là kho UserSecurityRequest.
type SecurityRequestRepository interface {
	Get(ctx context.Context, id string) (*fhir.UserSecurityRequest, error)
	// MarkUsedIfMatch đặt used=true kèm điều kiện versionID; xung đột thì domain.ErrVersionConflict.
	MarkUsedIfMatch(ctx context.Context, r *fhir.UserSecurityRequest, versionID string) error
	// ListSince trả request của user có lastUpdated >= since (kể cả đã dùng; người gọi lọc).
	ListSince(ctx context.Context, userRef string, since time.Time) ([]fhir.UserSecurityRequest, error)
	// MarkUsed đặt used=true trên bản mới nhất (không ghi đè trường khác).
	MarkUsed(ctx context.Context, id string) error
}

// TxRepos là các kho gắn với một transaction.
type TxRepos struct {
	Users            UserRepository
	SecurityRequests SecurityRequestRepository
	Resources        ResourceWriter
	// Locker giữ khóa tư vấn theo khóa tới hết transaction (tuần tự hóa "kiểm tra trùng rồi tạo"; kho không có ràng buộc duy nhất).
	Locker KeyLocker
	// Memberships dùng trong transaction (xóa member).
	Memberships MembershipRepository
	// Projects dùng trong transaction (đặt site có kiểm duy nhất).
	Projects ProjectRepository
	// Logins dùng trong transaction (thu hồi login khi đổi email).
	Logins LoginRepository
}

// KeyLocker khóa theo key tới hết transaction hiện tại; không hỗ trợ thì phải trả lỗi (không được bỏ qua âm thầm).
type KeyLocker interface {
	Lock(ctx context.Context, key string) error
}

// Transactor chạy fn trong một transaction: lỗi từ fn hủy mọi thay đổi.
type Transactor interface {
	InTx(ctx context.Context, fn func(TxRepos) error) error
}

// PasswordHasher băm và so khớp mật khẩu.
type PasswordHasher interface {
	// Hash băm mật khẩu (đã kiểm độ dài).
	Hash(password string) (string, error)
	// Compare so khớp mật khẩu với hash.
	Compare(password, hash string) bool
	// CompareDummy so với một hash giả cùng cost để thời gian trả lời không lộ email có tồn tại hay không.
	CompareDummy(password string)
}

// PwnedChecker cho biết mật khẩu xuất hiện bao nhiêu lần trong cơ sở dữ liệu rò rỉ. Lỗi mạng là lỗi (fail đóng).
type PwnedChecker interface {
	Count(ctx context.Context, password string) (int, error)
}

// DomainConfigRepository là kho DomainConfiguration (đăng nhập qua identity provider theo tên miền email).
type DomainConfigRepository interface {
	// FindByDomain tìm theo tên miền (chữ thường); nil nếu không có.
	FindByDomain(ctx context.Context, domain string) (*fhir.DomainConfiguration, error)
}

// UserConfigurationRepository là kho UserConfiguration.
type UserConfigurationRepository interface {
	Get(ctx context.Context, id string) (*fhir.UserConfiguration, error)
}

// MFAGate là phần MFA mà việc dựng phản hồi đăng nhập cần (cài đặt thật ở P5). Adapter mặc định của P2 là FailClosedMFA:
// lỗi và KHÔNG BAO GIỜ trả code cho một login còn thiếu bước MFA.
type MFAGate interface {
	// BuildEnrollment sinh đường dẫn và mã QR để đăng ký TOTP.
	BuildEnrollment(ctx context.Context, login *fhir.Login, user *fhir.User, project *fhir.Project) (uri, qr string, err error)
	// SendEmailCode gửi mã MFA qua email (khi email là phương thức duy nhất của user).
	SendEmailCode(ctx context.Context, login *fhir.Login, user *fhir.User, project *fhir.Project) error
}

// FailClosedMFA là MFAGate khi chưa có P5: mọi nhánh cần MFA đều lỗi, nên không có đường nào trả code mà bỏ qua MFA.
type FailClosedMFA struct{}

// BuildEnrollment luôn lỗi.
func (FailClosedMFA) BuildEnrollment(context.Context, *fhir.Login, *fhir.User, *fhir.Project) (string, string, error) {
	return "", "", errMFAUnavailable
}

// SendEmailCode luôn lỗi.
func (FailClosedMFA) SendEmailCode(context.Context, *fhir.Login, *fhir.User, *fhir.Project) error {
	return errMFAUnavailable
}

var errMFAUnavailable = errors.New("application: MFA chưa được cài đặt (P5), từ chối thay vì bỏ qua")

// AuthState là người gọi đã xác thực bằng Bearer token.
type AuthState struct {
	Login      *fhir.Login
	Membership *fhir.ProjectMembership
	Project    *fhir.Project
}

// Authenticator xác minh Bearer token (P3 cài đặt bằng khóa của auth). Lỗi phải là *domain.Error Unauthorized.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (*AuthState, error)
}

// UserAgentParser tách trình duyệt và hệ điều hành từ User-Agent (cho danh sách phiên của /auth/me).
type UserAgentParser interface {
	Parse(userAgent string) (browser, os string)
}

// TokenSigner ký claims thành JWT (header có kid, alg).
type TokenSigner interface {
	Sign(claims map[string]any) (string, error)
}

// TokenVerifier kiểm chữ ký và hạn của JWT do auth ký. Việc kiểm iss, aud, token_use thuộc Service.
type TokenVerifier interface {
	Verify(raw string) (map[string]any, error)
}

// HubTopics cấp topic FHIRcast ổn định cho một chủ thể (user hoặc ClientApplication): hai lần xin token trong hạn lease trả CÙNG topic,
// nên các ứng dụng của một người dùng cùng tìm thấy nhau. Nhiều instance phải dùng chung kho (Redis); bộ nhớ chỉ đúng với một instance.
type HubTopics interface {
	TopicFor(ctx context.Context, subject string) (string, error)
}

// HubConfig bật việc phát hub.url và hub.topic trong phản hồi token khi scope có `fhircast/`. nil: tắt (token không có hai trường này).
type HubConfig struct {
	// URL là địa chỉ Hub FHIRcast công khai (thường <gateway>/fhircast/STU3); trả nguyên văn cho client.
	URL    string
	Topics HubTopics
}

// TokenConfig là cấu hình phát token.
type TokenConfig struct {
	// Issuer là claim iss; fhir-server phải cấu hình FHIR_JWT_ISSUER đúng từng ký tự.
	Issuer string
	// Audiences là claim aud của access token (không rỗng); fhir-server kiểm FHIR_JWT_AUDIENCE nằm trong danh sách này.
	Audiences []string
	// AccessTTL, RefreshTTL, IDTTL là thời hạn mặc định; client ghi đè access và refresh bằng accessTokenLifetime, refreshTokenLifetime.
	AccessTTL, RefreshTTL, IDTTL time.Duration
	// MaxPolicyBytes là trần kích thước claim fhir_access_policy (0: 8192).
	MaxPolicyBytes int
	// PatientCompartment bật giải `%patient`/`%profile` và nhiều rule cùng loại khi dịch AccessPolicy sang claim (P8 M3). Chỉ bật khi mọi fhir-server đã
	// nâng cấp (P8 M0 đến M2); bản cũ từ chối claim dạng này.
	PatientCompartment bool
	// PolicyFieldRules bật dịch hiddenFields, readonlyFields và interaction của AccessPolicy sang claim (xem domain.PolicyOptions.FieldRules). Chỉ bật khi mọi fhir-server đã nâng cấp.
	PolicyFieldRules bool
	// BaseURL là địa chỉ công khai của auth, để dựng smart_style_url trong phản hồi token.
	BaseURL string
	// AppBaseURL là địa chỉ ứng dụng đăng nhập: /oauth2/authorize chuyển người dùng tới <AppBaseURL>/oauth. Rỗng thì không có /oauth2/authorize.
	AppBaseURL string
	// FHIRBaseURL (tùy chọn) là địa chỉ fhir-server, được chấp nhận làm `aud` của /oauth2/authorize (SMART đặt aud là URL FHIR).
	FHIRBaseURL string
	// AuthCodeTTL là hạn của authorization code tính từ lúc phát (mặc định 10 phút).
	AuthCodeTTL time.Duration
	// RefreshGrace là cửa sổ chấp nhận lại refresh token vừa bị xoay (từ cùng IP); 0 là tắt.
	RefreshGrace time.Duration
	// SessionMaxAge là trần tuyệt đối của phiên refresh tính từ lúc đăng nhập; 0 là không giới hạn.
	SessionMaxAge time.Duration
	// AllowPlainPKCE cho phép code_challenge_method=plain ở /oauth2/token (mặc định tắt: chỉ S256).
	AllowPlainPKCE bool
}
