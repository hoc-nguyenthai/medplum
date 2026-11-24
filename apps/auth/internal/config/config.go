// Package config đọc cấu hình auth từ biến môi trường (và file .env nếu có).
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"trivita-fhir/pkg/envconfig"
	"trivita-fhir/pkg/httpsec"
)

// Settings là toàn bộ cấu hình. Biến chung (HTTP_ADDR, DATABASE_*...) giống fhir-server; biến riêng của auth có tiền tố
// AUTH_. Biến của trivita-observability-go (OTEL_*, LOG_LEVEL, LOG_JSON) do thư viện đó tự đọc.
type Settings struct {
	AppName     string
	Addr        string
	Debug       bool
	Environment string // ENVIRONMENT: local | development | test | staging | production

	ReadinessTimeout   time.Duration
	ShutdownDrainDelay time.Duration
	ShutdownTimeout    time.Duration
	ReadHeaderTimeout  time.Duration

	// DatabaseURL phải trỏ tới schema RIÊNG của auth (User, Login, UserSecurityRequest chứa bí mật): role riêng với search_path là schema `identity`
	// trong database dùng chung (bật DATABASE_REQUIRE_SCHEMA), hoặc một database riêng. Không dùng chung bảng với dữ liệu lâm sàng của fhir-server.
	DatabaseURL         string
	DatabaseEcho        bool
	DatabaseAutoMigrate bool
	// DatabaseRequireSchema (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng của service
	// (mô hình một database nhiều schema, docs/plans/microservices-shared-db.md). Mặc định tắt để không phá cách chạy cũ (database
	// riêng, schema public); BẬT khi nhiều service chung một database.
	DatabaseRequireSchema bool
	// DatabaseMigrateURL (chỉ PostgreSQL) là URL của role *_migrate (có DDL) dùng riêng cho lệnh `migrate`; app chạy bằng DATABASE_URL (role *_app chỉ
	// có DML, D-DB-4). Đặt thì app KHÔNG tự migrate (DATABASE_AUTO_MIGRATE phải là false). Rỗng: `migrate` dùng DATABASE_URL.
	DatabaseMigrateURL   string
	DatabaseMaxOpenConns int
	DatabaseMaxIdleConns int
	DatabaseConnLifetime time.Duration

	// BaseURL là địa chỉ công khai của auth (dùng cho issuer mặc định, link trong email, redirect).
	BaseURL string
	// Issuer (claim iss) và Audience (claim aud) của token; dùng từ P3. Rỗng thì chưa phát token.
	Issuer   string
	Audience string
	// SigningKeyPath là thư mục chứa khóa riêng *.pem (tạo bằng `auth keys generate`). Bắt buộc ở staging/production; ngoài đó để trống
	// thì dùng khóa tạm trong bộ nhớ (token mất hiệu lực khi khởi động lại, chỉ một instance).
	SigningKeyPath string
	// ActiveKID chọn khóa ký khi thư mục có nhiều khóa (xoay khóa); rỗng chỉ hợp lệ khi có đúng một khóa.
	ActiveKID string
	// SigningAlg là thuật toán của khóa do `auth keys generate` sinh và của khóa tạm: RS256 (mặc định), ES256, ES384.
	SigningAlg string
	// AccessTTL, RefreshTTL, IDTTL là thời hạn token (dạng Go: 15m, 1h, 336h). Client ghi đè access và refresh bằng
	// accessTokenLifetime, refreshTokenLifetime. AccessTTL là cửa sổ thu hồi ở fhir-server (không tra Login mỗi yêu cầu), quyết định D-13: 15 phút.
	AccessTTL, RefreshTTL, IDTTL time.Duration
	// AppBaseURL là địa chỉ ứng dụng đăng nhập (D-12): /oauth2/authorize chuyển người dùng tới <AppBaseURL>/oauth để đăng nhập, chọn profile và
	// scope bằng /auth/*. Rỗng thì KHÔNG có /oauth2/authorize (mặc định ENVIRONMENT=local: http://localhost:3000). FHIRBaseURL (tùy chọn) là
	// địa chỉ fhir-server, được chấp nhận làm `aud` (SMART) ở /oauth2/authorize.
	AppBaseURL, FHIRBaseURL string
	// OAuthRegisterEnabled bật POST /oauth2/register (tra client dựng sẵn theo redirect_uri, mặc định tắt). AllowPrivateJWKS cho phép jwks_uri của
	// client là http hoặc trỏ vào dải nội bộ (chỉ dev/test, cấm ở staging/production). RedisURL (tùy chọn) là nơi nhớ jti của client assertion
	// dùng chung nhiều instance; rỗng thì nhớ trong bộ nhớ (chỉ đúng với một instance).
	OAuthRegisterEnabled, AllowPrivateJWKS bool
	RedisURL                               string
	// FHIRcastHubURL là địa chỉ Hub FHIRcast công khai (thường <ws-gateway>/fhircast/STU3) trả ở `hub.url` khi token có scope `fhircast/`. Rỗng: tắt.
	// Dùng chung AUTH_REDIS_URL để topic của một chủ thể ổn định giữa các instance (bắt buộc ở staging/production).
	FHIRcastHubURL string
	// AuthCodeTTL là hạn của authorization code tính từ lúc phát. AllowPlainPKCE cho phép code_challenge_method=plain (mặc định chỉ S256).
	AuthCodeTTL    time.Duration
	AllowPlainPKCE bool
	// RefreshGrace là cửa sổ chấp nhận lại refresh token vừa xoay từ cùng IP (30s như Medplum; 0 là tắt). SessionMaxAge là trần tuyệt đối
	// của một phiên refresh tính từ lúc đăng nhập (0: không giới hạn; mỗi lần xoay Medplum cấp lại hạn đầy nên phiên có thể sống vô hạn).
	RefreshGrace, SessionMaxAge time.Duration
	// MaxPolicyClaimBytes là trần kích thước claim fhir_access_policy; vượt thì từ chối cấp token.
	MaxPolicyClaimBytes int
	// PatientCompartmentEnabled bật dịch chính sách có `%patient`/`%profile` và nhiều rule cùng loại sang claim (compartment Patient, P8 M3): token bệnh nhân
	// và người thân đọc được dữ liệu trong compartment của mình. Mặc định TẮT (các loại đó bị cấm cả loại). CHỈ bật sau khi mọi fhir-server đã nâng cấp: bản
	// cũ từ chối claim dạng này nên token bị 403. Thứ tự deploy: fhir-server trước, rồi bật biến này ở auth.
	PatientCompartmentEnabled bool
	// PolicyFieldRulesEnabled bật dịch hiddenFields, readonlyFields và interaction của AccessPolicy sang claim fhir_access_policy: fhir-server gỡ trường ẩn khỏi mọi phản hồi đọc, chặn ghi
	// trường chỉ đọc (khôi phục từ bản đang lưu) và giới hạn interaction. Mặc định TẮT (entry có hiddenFields hoặc interaction cấm cả loại, readonlyFields làm loại chỉ đọc). CHỈ bật sau
	// khi mọi fhir-server đã nâng cấp: bản cũ từ chối khóa lạ trong claim nên token bị 403. Thứ tự deploy: fhir-server trước, rồi bật biến này ở auth.
	PolicyFieldRulesEnabled bool

	// RegisterEnabled=false tắt mọi đăng ký; NewProjectRegistrationEnabled=false chỉ tắt tạo project mới.
	RegisterEnabled               bool
	NewProjectRegistrationEnabled bool

	// MailProvider chọn kênh gửi thư đặt lại mật khẩu và xác minh email: log (in ra log, KÈM link có secret) hoặc none (bỏ qua). Mặc định
	// log ở local/development/test, none ở staging/production; log bị cấm ở staging/production. Kênh gửi thật chưa có (quyết định D4). MailAppName là
	// tên trong thư (mặc định Trivita).
	MailProvider, MailAppName string
	// SMTP (AUTH_MAIL_PROVIDER=smtp): SMTPHost/SMTPPort (587), SMTPUsername/SMTPPassword (bỏ trống username nếu máy chủ không cần đăng nhập; mật khẩu chỉ qua biến môi trường,
	// không bao giờ in ra log), SMTPFrom là người gửi mặc định ("a@b" hoặc "Tên <a@b>"), SMTPApprovedSenders các địa chỉ trần khác được đề nghị làm người gửi, SMTPSecure
	// ("" suy ra theo cổng: 465 là TLS ngay từ đầu; "true" TLS ngay từ đầu; "false" STARTTLS), SMTPAllowPlaintext (chỉ dev: cho gửi không mã hóa khi máy chủ thiếu STARTTLS;
	// cấm ở staging/production), SMTPCAFile (tùy chọn) là tệp PEM CA riêng được tin thêm cho relay nội bộ dùng chứng chỉ tự cấp, SMTPTimeout là thời gian chờ nối và nhận lời chào. SMTP của server là cấu hình quản trị nên được tin (có thể là relay nội bộ).
	SMTPHost, SMTPUsername, SMTPPassword, SMTPFrom, SMTPApprovedSenders, SMTPSecure, SMTPCAFile string
	SMTPPort                                                                                    int
	SMTPAllowPlaintext                                                                          bool
	SMTPTimeout                                                                                 time.Duration

	// RecaptchaSiteKey/RecaptchaSecretKey là reCAPTCHA mức máy chủ cho newuser và resetpassword; SecretKey rỗng thì không kiểm (project vẫn có thể
	// đặt khóa riêng trong Project.site). RequireVerifiedEmailForProjectCreation: newuser với projectId=new gửi thư xác minh.
	RecaptchaSiteKey, RecaptchaSecretKey   string
	RecaptchaTimeout                       time.Duration
	RequireVerifiedEmailForProjectCreation bool
	// SupportEmail là địa chỉ hỗ trợ trong thư chào (địa chỉ thuần); rỗng thì bỏ dòng này.
	SupportEmail string

	// MFA (P5). MFASecretKeys: danh sách "kid:base64(32 byte)" cách nhau bằng dấu phẩy để mã hóa User.mfaSecret khi lưu (khóa đầu tiên dùng để mã hóa, mọi khóa dùng để
	// giải mã nên xoay khóa bằng cách thêm khóa mới lên đầu); bắt buộc ở staging/production, ngoài đó để trống thì lưu dạng thô (cảnh báo). Hạn mức thử: MFALoginAttemptLimit
	// mỗi login (5) và MFAUserAttemptLimit mỗi user (20) trong MFALoginExpiration (10 phút, cũng là tuổi tối đa của login chờ MFA); MFAWindow là số bước TOTP chấp nhận mỗi bên (1).
	// Hạn mã email và hạn secret được mời mặc định 20 phút; MFAEmailSendLimit là số lần gửi mã email mỗi login trong thời hạn mã.
	MFASecretKeys string
	// Khóa tài khoản theo số lần đăng nhập MẬT KHẨU sai (không đổi theo IP): tối đa LockoutMaxFailures lượt thử mỗi LockoutWindow (cố định, từ lượt đầu), lượt kế tiếp
	// khóa tài khoản LockoutDuration; thành công xóa bộ đếm, đặt lại mật khẩu hoặc `auth user unlock` mở khóa. Khi khóa, trả đúng lỗi "sai thông tin" như sai mật khẩu
	// (không lộ khóa, không lộ email có tồn tại). 0 là tắt. Trạng thái dùng AUTH_REDIS_URL (nhiều instance); không có thì nhớ trong bộ nhớ (một instance).
	LockoutMaxFailures                                   int
	LockoutWindow, LockoutDuration                       time.Duration
	MFAWindow, MFALoginAttemptLimit, MFAUserAttemptLimit int
	MFALoginExpiration, MFAEmailCodeExpiration           time.Duration
	MFAEnrollmentExpiration                              time.Duration
	MFAEmailSendLimit                                    int

	// Đăng nhập ngoài (P6). ExternalStateKey: khóa ký `state` (base64, tối thiểu 32 byte); bắt buộc ở staging/production khi có AUTH_APP_BASE_URL (nhiều instance dùng chung
	// một khóa); ngoài đó để trống thì dùng khóa tạm trong bộ nhớ (chỉ một instance). ExternalStateTTL: hạn của state và cookie liên kết. ExternalAuthProviders: JSON mảng
	// {issuer, clientId?, userInfoUrl?, identityProvider?} tra theo issuer của state. AllowPrivateJWKS (có sẵn) cũng cho phép IdP http/nội bộ và id_token không jwksUrl: chỉ dev/test.
	ExternalStateKey      string
	ExternalStateTTL      time.Duration
	ExternalAuthProviders string
	// GoogleClientID là Google client chính cho /auth/google (rỗng: chỉ nhận googleClientId khai ở Project.site). GoogleEnabled bật route. GoogleJWKSURL chỉ để test.
	GoogleClientID, GoogleJWKSURL string
	GoogleEnabled                 bool
	// AdminEnabled bật /admin/projects/* (P7); mặc định tắt.
	AdminEnabled bool
	// Route gửi thư POST /email/v1/send (E4): EmailEnabled mặc định tắt (route 404; project còn cần tính năng `email` và người gọi là admin). EmailAllowProjectSMTP cho dùng
	// SMTP riêng khai ở Project.secret (mặc định tắt: mật khẩu nằm dạng rõ trong Project.secret). EmailAllowUnsafeOutbound tắt chặn địa chỉ nội bộ của SMTP project và
	// của tệp đính kèm URL (chỉ dev/test, cấm ở staging/production). EmailMaxRecipients là tổng To, Cc, Bcc mỗi thư; EmailMaxBytes là cỡ tối đa của thư và của mỗi tệp tải theo URL.
	EmailEnabled, EmailAllowProjectSMTP, EmailAllowUnsafeOutbound bool
	// EmailFHIRURL (AUTH_EMAIL_FHIR_URL) là gốc API FHIR (ví dụ https://fhir.internal/fhir/R4) để đọc tệp đính kèm Binary/<id> bằng token của
	// người gọi (E5). Rỗng: Binary/<id> bị từ chối là chưa hỗ trợ. Địa chỉ nội bộ được phép (do vận hành viên đặt); production bắt buộc https vì token được gửi qua đó.
	EmailFHIRURL                      string
	EmailFHIRTimeout                  time.Duration
	EmailMaxRecipients, EmailMaxBytes int
	// SCIM 2.0 (P7, /scim/v2/Users). ScimEnabled mặc định tắt (route 404); ScimAllowBasic cho Authorization: Basic của ClientApplication (Okta chỉ có Basic) và ở
	// staging/production BẮT BUỘC kèm ScimAllowedIPs (danh sách CIDR được gọi /scim/v2); ScimMaxPage là cỡ trang mặc định và trần của count; ScimMaxBodyBytes cỡ body
	// tối đa; ScimMaxPatchOps số thao tác tối đa của một PATCH.
	ScimEnabled, ScimAllowBasic  bool
	ScimMaxPage, ScimMaxPatchOps int
	ScimMaxBodyBytes             int
	ScimAllowedIPs               string
	// AdminBlockedEmailDomains: các tên miền email bị chặn khi mời Practitioner (AUTH_BLOCKED_EMAIL_DOMAINS, cách nhau bằng dấu phẩy).
	AdminBlockedEmailDomains string

	// BcryptCost là cost của bcrypt (Medplum mặc định 10).
	BcryptCost int
	// PwnedEnabled bật kiểm tra mật khẩu rò rỉ (HIBP). Chỉ được tắt ở local, development, test: lỗi dịch vụ là lỗi (fail đóng).
	PwnedEnabled bool
	PwnedURL     string // rỗng = API chính thức
	PwnedTimeout time.Duration

	// AllowedOrigins là danh sách origin (cách nhau bằng dấu phẩy) được gọi API từ trình duyệt qua CORS, dạng http(s)://host[:port],
	// ví dụ ứng dụng web. Chưa đặt và ENVIRONMENT=local thì mặc định http://localhost:3000. Rỗng: không cho origin nào. Phần tử `*`
	// (mọi origin, vẫn kèm credentials) chỉ để dev, bị cấm ở staging/production, nơi chỉ nhận https.
	AllowedOrigins string
	// CookieSecure đặt cờ Secure cho cookie đăng nhập (mặc định true; chỉ tắt ở dev dùng HTTP thuần).
	CookieSecure bool
	// TrustedProxies là danh sách CIDR của reverse proxy được tin khi đọc IP người gọi từ X-Forwarded-For (cách nhau bằng dấu
	// phẩy). Rỗng: không tin header nào, IP là địa chỉ kết nối trực tiếp. IP quyết định ipAccessRule nên đặt sai là lỗ hổng.
	TrustedProxies string
	// RateLimit là số yêu cầu mỗi giây mỗi IP trên /auth, /oauth2, /.well-known, /admin (0: tắt); RateBurst là cỡ đợt (<=0: gấp đôi).
	// LoginRateLimit chặt hơn, riêng các bước nhập thông tin đăng nhập (POST /auth/login, profile, scope, method), để chậm việc dò
	// mật khẩu. Không đặt thì staging/production dùng DefaultRateLimit và DefaultLoginRateLimit; ngoài đó tắt. Đặt 0 để tắt hẳn.
	RateLimit, LoginRateLimit float64
	RateBurst, LoginRateBurst int
	// RateLimitShared: đếm hạn mức (chung và đăng nhập) trên Redis (AUTH_REDIS_URL) để N replica dùng chung một hạn mức. Redis lỗi: tạm rơi về bộ đếm riêng của replica.
	RateLimitShared bool

	// DefaultProjectFeatures là danh sách tính năng (cách nhau bằng dấu phẩy) bật sẵn cho project mới.
	DefaultProjectFeatures string

	// CLIClientEnabled bật client OAuth dựng sẵn `medplum-cli` (public, pkceOptional, redirect http://localhost:9615).
	CLIClientEnabled bool
	// DefaultOAuthClients là mảng JSON các ClientApplication dựng sẵn thêm (rỗng = không có).
	DefaultOAuthClients string
}

// DevAllowedOrigin là origin của ứng dụng web chạy ở máy dev, được cho phép mặc định khi ENVIRONMENT=local.
const DevAllowedOrigin = "http://localhost:3000"

// Hạn mức mặc định (mỗi IP) ở staging/production khi không đặt AUTH_RATE_LIMIT / AUTH_LOGIN_RATE_LIMIT.
const (
	DefaultRateLimit      = 20.0
	DefaultLoginRateLimit = 1.0
	DefaultLoginBurst     = 10
)

// Default trả về cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-auth", Addr: ":8006", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		DatabaseURL:       "sqlite://./auth.db", DatabaseAutoMigrate: true,
		DatabaseMaxOpenConns: 20, DatabaseMaxIdleConns: 5, DatabaseConnLifetime: 30 * time.Minute,
		BaseURL:         "http://localhost:8006",
		RegisterEnabled: true, NewProjectRegistrationEnabled: true, RecaptchaTimeout: 3 * time.Second,
		ExternalStateTTL: 10 * time.Minute, SMTPPort: 587, SMTPTimeout: 20 * time.Second, EmailFHIRTimeout: 30 * time.Second, ScimAllowBasic: true, EmailMaxRecipients: 50, EmailMaxBytes: 10 << 20, ScimMaxPage: 100, ScimMaxPatchOps: 50, ScimMaxBodyBytes: 1 << 20, LockoutMaxFailures: 10, LockoutWindow: 15 * time.Minute, LockoutDuration: 15 * time.Minute, MFAWindow: 1, MFALoginAttemptLimit: 5, MFAUserAttemptLimit: 20, MFALoginExpiration: 10 * time.Minute, MFAEmailCodeExpiration: 20 * time.Minute,
		MFAEnrollmentExpiration: 20 * time.Minute, MFAEmailSendLimit: 5,
		AccessTTL: 15 * time.Minute, RefreshTTL: 14 * 24 * time.Hour, IDTTL: time.Hour, AuthCodeTTL: 10 * time.Minute, RefreshGrace: 30 * time.Second, MaxPolicyClaimBytes: 16384, SigningAlg: "RS256",
		BcryptCost: 10, CookieSecure: true, PwnedEnabled: true, PwnedTimeout: 3 * time.Second, CLIClientEnabled: true,
	}
}

// Load đọc `.env` (nếu có, không ghi đè biến môi trường thật) rồi biến môi trường. Gom mọi lỗi lại.
func Load() (Settings, error) {
	if err := envconfig.LoadDotEnv(".env"); err != nil {
		return Settings{}, err
	}
	if err := legacyEnvError(os.Environ()); err != nil {
		return Settings{}, err
	}
	return FromEnv(os.Getenv)
}

// legacyPrefix là tiền tố biến môi trường CŨ của service (trước khi đổi tên authen thành auth). Biến mang tiền tố này không còn được đọc: nếu
// bỏ qua âm thầm thì cấu hình cũ (issuer, khóa, DATABASE...) rơi về mặc định và token ký sai issuer, nên service từ chối khởi động và nói rõ phải đổi gì.
const legacyPrefix = "AUTHEN_"

// legacyEnvError trả lỗi liệt kê (tối đa 10) biến còn dùng tiền tố cũ cùng tên mới tương ứng; nil nếu không còn biến nào.
func legacyEnvError(environ []string) error {
	var names []string
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, legacyPrefix) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	shown := names
	if len(shown) > 10 {
		shown = shown[:10]
	}
	var parts []string
	for _, n := range shown {
		parts = append(parts, n+" -> AUTH_"+strings.TrimPrefix(n, legacyPrefix))
	}
	more := ""
	if len(names) > len(shown) {
		more = fmt.Sprintf(" (và %d biến nữa)", len(names)-len(shown))
	}
	return fmt.Errorf("còn %d biến môi trường dùng tiền tố cũ AUTHEN_ (service đã đổi tên thành auth, tiền tố mới là AUTH_): đổi %s%s", len(names), strings.Join(parts, ", "), more)
}

// FromEnv đọc cấu hình từ hàm lookup (dễ test) và kiểm tra hợp lệ.
func FromEnv(get func(string) string) (Settings, error) {
	s := Default()
	r := envconfig.NewReader(get)
	r.String(&s.AppName, "APP_NAME")
	r.String(&s.Addr, "HTTP_ADDR")
	r.Bool(&s.Debug, "DEBUG")
	r.String(&s.Environment, "ENVIRONMENT")
	r.Duration(&s.ReadinessTimeout, "READINESS_TIMEOUT")
	r.Duration(&s.ShutdownDrainDelay, "SHUTDOWN_DRAIN_DELAY")
	r.Duration(&s.ShutdownTimeout, "SHUTDOWN_TIMEOUT")
	r.Duration(&s.ReadHeaderTimeout, "READ_HEADER_TIMEOUT")
	r.String(&s.DatabaseURL, "DATABASE_URL")
	r.Bool(&s.DatabaseEcho, "DATABASE_ECHO")
	r.Bool(&s.DatabaseAutoMigrate, "DATABASE_AUTO_MIGRATE")
	r.Bool(&s.DatabaseRequireSchema, "DATABASE_REQUIRE_SCHEMA")
	r.String(&s.DatabaseMigrateURL, "DATABASE_MIGRATE_URL")
	r.Int(&s.DatabaseMaxOpenConns, "DATABASE_MAX_OPEN_CONNS")
	r.Int(&s.DatabaseMaxIdleConns, "DATABASE_MAX_IDLE_CONNS")
	r.Duration(&s.DatabaseConnLifetime, "DATABASE_CONN_MAX_LIFETIME")
	r.String(&s.BaseURL, "AUTH_BASE_URL")
	r.String(&s.Issuer, "AUTH_ISSUER")
	r.String(&s.Audience, "AUTH_AUDIENCE")
	r.String(&s.SigningKeyPath, "AUTH_SIGNING_KEY_PATH")
	r.String(&s.ActiveKID, "AUTH_ACTIVE_KID")
	r.String(&s.SigningAlg, "AUTH_SIGNING_ALG")
	r.Duration(&s.AccessTTL, "AUTH_ACCESS_TTL")
	r.Duration(&s.RefreshTTL, "AUTH_REFRESH_TTL")
	r.Duration(&s.IDTTL, "AUTH_ID_TTL")
	r.Bool(&s.OAuthRegisterEnabled, "AUTH_OAUTH_REGISTER_ENABLED")
	r.Bool(&s.AllowPrivateJWKS, "AUTH_ALLOW_PRIVATE_JWKS")
	r.String(&s.RedisURL, "AUTH_REDIS_URL")
	r.String(&s.FHIRcastHubURL, "AUTH_FHIRCAST_HUB_URL")
	r.String(&s.AppBaseURL, "AUTH_APP_BASE_URL")
	if s.AppBaseURL == "" && s.Environment == "local" {
		s.AppBaseURL = DevAllowedOrigin
	}
	r.String(&s.FHIRBaseURL, "AUTH_FHIR_BASE_URL")
	r.String(&s.MailProvider, "AUTH_MAIL_PROVIDER")
	r.String(&s.MailAppName, "AUTH_MAIL_APP_NAME")
	r.String(&s.SMTPHost, "AUTH_SMTP_HOST")
	r.Int(&s.SMTPPort, "AUTH_SMTP_PORT")
	r.String(&s.SMTPUsername, "AUTH_SMTP_USERNAME")
	r.String(&s.SMTPPassword, "AUTH_SMTP_PASSWORD")
	r.String(&s.SMTPFrom, "AUTH_SMTP_FROM")
	r.String(&s.SMTPApprovedSenders, "AUTH_SMTP_APPROVED_SENDERS")
	r.String(&s.SMTPSecure, "AUTH_SMTP_SECURE")
	r.String(&s.SMTPCAFile, "AUTH_SMTP_CA_FILE")
	r.Bool(&s.SMTPAllowPlaintext, "AUTH_SMTP_ALLOW_PLAINTEXT")
	r.Duration(&s.SMTPTimeout, "AUTH_SMTP_TIMEOUT")
	if s.MailProvider == "" {
		s.MailProvider = "none"
		if !s.IsProd() {
			s.MailProvider = "log"
		}
	}
	if s.MailAppName == "" {
		s.MailAppName = "Trivita"
	}
	r.Duration(&s.AuthCodeTTL, "AUTH_AUTH_CODE_TTL")
	r.Bool(&s.AllowPlainPKCE, "AUTH_ALLOW_PLAIN_PKCE")
	r.Duration(&s.RefreshGrace, "AUTH_REFRESH_GRACE")
	r.Duration(&s.SessionMaxAge, "AUTH_SESSION_MAX_AGE")
	r.Int(&s.MaxPolicyClaimBytes, "AUTH_MAX_POLICY_CLAIM_BYTES")
	r.Bool(&s.PatientCompartmentEnabled, "AUTH_PATIENT_COMPARTMENT_ENABLED")
	r.Bool(&s.PolicyFieldRulesEnabled, "AUTH_POLICY_FIELD_RULES_ENABLED")
	r.Bool(&s.RegisterEnabled, "AUTH_REGISTER_ENABLED")
	r.Bool(&s.NewProjectRegistrationEnabled, "AUTH_NEW_PROJECT_REGISTRATION_ENABLED")
	r.String(&s.RecaptchaSiteKey, "AUTH_RECAPTCHA_SITE_KEY")
	r.String(&s.RecaptchaSecretKey, "AUTH_RECAPTCHA_SECRET_KEY")
	r.Duration(&s.RecaptchaTimeout, "AUTH_RECAPTCHA_TIMEOUT")
	r.Bool(&s.RequireVerifiedEmailForProjectCreation, "AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION")
	r.String(&s.SupportEmail, "AUTH_SUPPORT_EMAIL")
	r.String(&s.MFASecretKeys, "AUTH_MFA_SECRET_KEYS")
	r.String(&s.ExternalStateKey, "AUTH_EXTERNAL_STATE_KEY")
	r.Duration(&s.ExternalStateTTL, "AUTH_EXTERNAL_STATE_TTL")
	r.String(&s.GoogleClientID, "AUTH_GOOGLE_CLIENT_ID")
	r.String(&s.GoogleJWKSURL, "AUTH_GOOGLE_JWKS_URL")
	r.Bool(&s.GoogleEnabled, "AUTH_GOOGLE_ENABLED")
	r.Bool(&s.AdminEnabled, "AUTH_ADMIN_ENABLED")
	r.Bool(&s.EmailEnabled, "AUTH_EMAIL_ENABLED")
	r.Bool(&s.EmailAllowProjectSMTP, "AUTH_EMAIL_ALLOW_PROJECT_SMTP")
	r.Bool(&s.EmailAllowUnsafeOutbound, "AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND")
	r.String(&s.EmailFHIRURL, "AUTH_EMAIL_FHIR_URL")
	r.Duration(&s.EmailFHIRTimeout, "AUTH_EMAIL_FHIR_TIMEOUT")
	r.Int(&s.EmailMaxRecipients, "AUTH_EMAIL_MAX_RECIPIENTS")
	r.Int(&s.EmailMaxBytes, "AUTH_EMAIL_MAX_BYTES")
	r.Bool(&s.ScimEnabled, "AUTH_SCIM_ENABLED")
	r.Bool(&s.ScimAllowBasic, "AUTH_SCIM_ALLOW_BASIC")
	r.Int(&s.ScimMaxPage, "AUTH_SCIM_MAX_PAGE")
	r.Int(&s.ScimMaxPatchOps, "AUTH_SCIM_MAX_PATCH_OPS")
	r.Int(&s.ScimMaxBodyBytes, "AUTH_SCIM_MAX_BODY_BYTES")
	r.String(&s.ScimAllowedIPs, "AUTH_SCIM_ALLOWED_IPS")
	r.String(&s.AdminBlockedEmailDomains, "AUTH_BLOCKED_EMAIL_DOMAINS")
	r.String(&s.ExternalAuthProviders, "AUTH_EXTERNAL_AUTH_PROVIDERS")
	r.Int(&s.MFAWindow, "AUTH_MFA_WINDOW")
	r.Int(&s.LockoutMaxFailures, "AUTH_LOCKOUT_MAX_FAILURES")
	r.Duration(&s.LockoutWindow, "AUTH_LOCKOUT_WINDOW")
	r.Duration(&s.LockoutDuration, "AUTH_LOCKOUT_DURATION")
	r.Int(&s.MFALoginAttemptLimit, "AUTH_MFA_LOGIN_ATTEMPT_LIMIT")
	r.Int(&s.MFAUserAttemptLimit, "AUTH_MFA_USER_ATTEMPT_LIMIT")
	r.Duration(&s.MFALoginExpiration, "AUTH_MFA_LOGIN_EXPIRATION")
	r.Duration(&s.MFAEmailCodeExpiration, "AUTH_MFA_EMAIL_CODE_EXPIRATION")
	r.Duration(&s.MFAEnrollmentExpiration, "AUTH_MFA_ENROLLMENT_EXPIRATION")
	r.Int(&s.MFAEmailSendLimit, "AUTH_MFA_EMAIL_SEND_LIMIT")
	r.Int(&s.BcryptCost, "AUTH_BCRYPT_COST")
	r.Bool(&s.PwnedEnabled, "AUTH_PWNED_ENABLED")
	r.String(&s.PwnedURL, "AUTH_PWNED_URL")
	r.Duration(&s.PwnedTimeout, "AUTH_PWNED_TIMEOUT")
	r.String(&s.DefaultProjectFeatures, "AUTH_DEFAULT_PROJECT_FEATURES")
	r.String(&s.AllowedOrigins, "AUTH_ALLOWED_ORIGINS")
	if s.AllowedOrigins == "" && s.Environment == "local" {
		s.AllowedOrigins = DevAllowedOrigin
	}
	r.Bool(&s.CookieSecure, "AUTH_COOKIE_SECURE")
	r.String(&s.TrustedProxies, "AUTH_TRUSTED_PROXIES")
	r.Float(&s.RateLimit, "AUTH_RATE_LIMIT")
	r.Int(&s.RateBurst, "AUTH_RATE_BURST")
	r.Float(&s.LoginRateLimit, "AUTH_LOGIN_RATE_LIMIT")
	r.Int(&s.LoginRateBurst, "AUTH_LOGIN_RATE_BURST")
	r.Bool(&s.RateLimitShared, "AUTH_RATE_LIMIT_SHARED")
	if s.IsProd() {
		if get("AUTH_RATE_LIMIT") == "" {
			s.RateLimit = DefaultRateLimit
		}
		if get("AUTH_LOGIN_RATE_LIMIT") == "" {
			s.LoginRateLimit = DefaultLoginRateLimit
			if get("AUTH_LOGIN_RATE_BURST") == "" {
				s.LoginRateBurst = DefaultLoginBurst
			}
		}
	}
	r.Bool(&s.CLIClientEnabled, "AUTH_CLI_CLIENT_ENABLED")
	r.String(&s.DefaultOAuthClients, "AUTH_DEFAULT_OAUTH_CLIENTS")
	r.Add(s.validate())
	return s, r.Err()
}

// IsProd cho biết môi trường staging hoặc production (áp các ràng buộc chặt).
func (s Settings) IsProd() bool { return s.Environment == "production" || s.Environment == "staging" }

// SwaggerEnabled: Swagger UI chỉ bật ngoài staging/production (auth có route nhạy cảm).
func (s Settings) SwaggerEnabled() bool { return !s.IsProd() }

func httpURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s Settings) validate() error {
	var errs []error
	if s.DatabaseMigrateURL != "" {
		if !strings.HasPrefix(s.DatabaseMigrateURL, "postgres:") && !strings.HasPrefix(s.DatabaseMigrateURL, "postgresql:") {
			errs = append(errs, errors.New("DATABASE_MIGRATE_URL: chỉ áp dụng cho PostgreSQL (role migrate riêng)"))
		}
		if s.DatabaseAutoMigrate {
			errs = append(errs, errors.New("DATABASE_MIGRATE_URL: đã dùng role migrate riêng thì app không tự migrate: đặt DATABASE_AUTO_MIGRATE=false và chạy `migrate up` ở bước deploy"))
		}
	}
	if s.DatabaseRequireSchema && !strings.HasPrefix(s.DatabaseURL, "postgres:") && !strings.HasPrefix(s.DatabaseURL, "postgresql:") {
		errs = append(errs, errors.New("DATABASE_REQUIRE_SCHEMA: chỉ áp dụng cho PostgreSQL (SQLite và MySQL không có schema riêng cho service)"))
	}
	if s.RateLimitShared && s.RedisURL == "" {
		errs = append(errs, errors.New("AUTH_REDIS_URL: bắt buộc khi AUTH_RATE_LIMIT_SHARED=true"))
	}
	if s.RateLimit < 0 || s.RateBurst < 0 || s.LoginRateLimit < 0 || s.LoginRateBurst < 0 {
		errs = append(errs, errors.New("AUTH_RATE_LIMIT, AUTH_RATE_BURST, AUTH_LOGIN_RATE_LIMIT, AUTH_LOGIN_RATE_BURST: không được âm"))
	}
	if err := envconfig.OneOf("ENVIRONMENT", s.Environment, "local", "development", "test", "staging", "production"); err != nil {
		errs = append(errs, err)
	}
	if !httpURL(s.BaseURL) {
		errs = append(errs, errors.New("AUTH_BASE_URL: phải là URL http(s) đầy đủ, ví dụ https://auth.example.org"))
	}
	if s.Issuer != "" && !httpURL(s.Issuer) {
		errs = append(errs, errors.New("AUTH_ISSUER: phải là URL http(s) đầy đủ"))
	}
	if s.IsProd() {
		if s.Debug {
			errs = append(errs, errors.New("DEBUG: không được bật ở staging/production"))
		}
		if strings.HasPrefix(s.DatabaseURL, "sqlite:") {
			errs = append(errs, errors.New("DATABASE_URL: không dùng SQLite ở staging/production"))
		}
		if !strings.HasPrefix(s.BaseURL, "https://") {
			errs = append(errs, errors.New("AUTH_BASE_URL: phải là https ở staging/production"))
		}
		if s.Issuer != "" && !strings.HasPrefix(s.Issuer, "https://") {
			errs = append(errs, errors.New("AUTH_ISSUER: phải là https ở staging/production"))
		}
		if s.SigningKeyPath == "" {
			errs = append(errs, errors.New("AUTH_SIGNING_KEY_PATH: bắt buộc ở staging/production (chạy `auth keys generate`)"))
		}
	}
	switch s.SigningAlg {
	case "RS256", "ES256", "ES384":
	default:
		errs = append(errs, errors.New("AUTH_SIGNING_ALG: RS256, ES256 hoặc ES384"))
	}
	if s.AllowPrivateJWKS && s.IsProd() {
		errs = append(errs, errors.New("AUTH_ALLOW_PRIVATE_JWKS: không được bật ở staging/production (tắt chống SSRF khi tải JWKS)"))
	}
	if s.RedisURL != "" {
		if u, err := url.Parse(s.RedisURL); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
			errs = append(errs, errors.New("AUTH_REDIS_URL: phải là redis:// hoặc rediss://"))
		}
	}
	if s.FHIRcastHubURL != "" {
		u, err := url.Parse(s.FHIRcastHubURL)
		switch {
		case err != nil || !httpURL(s.FHIRcastHubURL) || u.RawQuery != "" || u.Fragment != "":
			errs = append(errs, errors.New("AUTH_FHIRCAST_HUB_URL: phải là URL http(s) đầy đủ, không có query hay fragment (ví dụ https://ws.example.org/fhircast/STU3)"))
		case s.IsProd() && u.Scheme != "https":
			errs = append(errs, errors.New("AUTH_FHIRCAST_HUB_URL: phải là https ở staging/production"))
		}
		if s.IsProd() && s.RedisURL == "" {
			errs = append(errs, errors.New("AUTH_REDIS_URL: bắt buộc khi đặt AUTH_FHIRCAST_HUB_URL ở staging/production (topic phải ổn định giữa các instance)"))
		}
	}
	if s.AppBaseURL != "" && !httpURL(s.AppBaseURL) {
		errs = append(errs, errors.New("AUTH_APP_BASE_URL: phải là URL http(s) đầy đủ"))
	} else if s.IsProd() && s.AppBaseURL != "" && !strings.HasPrefix(s.AppBaseURL, "https://") {
		errs = append(errs, errors.New("AUTH_APP_BASE_URL: phải là https ở staging/production"))
	}
	if err := envconfig.OneOf("AUTH_MAIL_PROVIDER", s.MailProvider, "log", "none", "smtp"); err != nil {
		errs = append(errs, err)
	} else if s.IsProd() && s.MailProvider == "log" {
		errs = append(errs, errors.New("AUTH_MAIL_PROVIDER=log: cấm ở staging/production (ghi link có secret ra log)"))
	}
	errs = append(errs, s.validateSMTP()...)
	if s.EmailMaxRecipients <= 0 || s.EmailMaxBytes <= 0 {
		errs = append(errs, errors.New("AUTH_EMAIL_MAX_RECIPIENTS, AUTH_EMAIL_MAX_BYTES: phải > 0"))
	}
	if s.EmailFHIRURL != "" {
		u, err := url.Parse(s.EmailFHIRURL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(s.EmailFHIRURL, "?#"):
			errs = append(errs, errors.New("AUTH_EMAIL_FHIR_URL: phải là URL http(s) tuyệt đối, không userinfo, query hay fragment"))
		case s.IsProd() && u.Scheme != "https":
			errs = append(errs, errors.New("AUTH_EMAIL_FHIR_URL: staging/production phải dùng https (Bearer token của người gọi được gửi tới địa chỉ này)"))
		}
		if s.EmailFHIRTimeout <= 0 {
			errs = append(errs, errors.New("AUTH_EMAIL_FHIR_TIMEOUT: phải dương"))
		}
	}
	if s.EmailAllowUnsafeOutbound && s.IsProd() {
		errs = append(errs, errors.New("AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND: cấm ở staging/production (tắt chống SSRF của SMTP project và tệp đính kèm)"))
	}
	if s.FHIRBaseURL != "" && !httpURL(s.FHIRBaseURL) {
		errs = append(errs, errors.New("AUTH_FHIR_BASE_URL: phải là URL http(s) đầy đủ"))
	}
	if s.LockoutMaxFailures < 0 || (s.LockoutMaxFailures > 0 && (s.LockoutWindow <= 0 || s.LockoutDuration <= 0)) {
		errs = append(errs, errors.New("AUTH_LOCKOUT_*: AUTH_LOCKOUT_MAX_FAILURES phải >= 0 (0 là tắt); AUTH_LOCKOUT_WINDOW và AUTH_LOCKOUT_DURATION phải > 0 khi bật"))
	}
	if s.MFAWindow < 0 || s.MFAWindow > 5 || s.MFALoginAttemptLimit <= 0 || s.MFAUserAttemptLimit <= 0 || s.MFAEmailSendLimit <= 0 ||
		s.MFALoginExpiration <= 0 || s.MFAEmailCodeExpiration <= 0 || s.MFAEnrollmentExpiration <= 0 {
		errs = append(errs, errors.New("AUTH_MFA_*: window phải trong [0, 5], các giới hạn và thời hạn phải > 0"))
	}
	if s.IsProd() {
		if s.MFASecretKeys == "" {
			errs = append(errs, errors.New("AUTH_MFA_SECRET_KEYS: bắt buộc ở staging/production (mã hóa secret TOTP khi lưu)"))
		}
		if s.RedisURL == "" {
			errs = append(errs, errors.New("AUTH_REDIS_URL: bắt buộc ở staging/production (hạn mức thử và chống dùng lại mã MFA phải dùng chung giữa các instance)"))
		}
	}
	if s.ScimMaxPage <= 0 || s.ScimMaxPatchOps <= 0 || s.ScimMaxBodyBytes <= 0 {
		errs = append(errs, errors.New("AUTH_SCIM_MAX_PAGE, AUTH_SCIM_MAX_PATCH_OPS, AUTH_SCIM_MAX_BODY_BYTES: phải > 0"))
	}
	if _, err := s.ScimAllowedNets(); err != nil {
		errs = append(errs, fmt.Errorf("AUTH_SCIM_ALLOWED_IPS: %w", err))
	} else if s.ScimEnabled && s.ScimAllowBasic && s.IsProd() && strings.TrimSpace(s.ScimAllowedIPs) == "" {
		errs = append(errs, errors.New("AUTH_SCIM_ALLOWED_IPS: bắt buộc ở staging/production khi bật SCIM với Basic (Basic gửi secret của client: chỉ cho IP của IdP); hoặc đặt AUTH_SCIM_ALLOW_BASIC=false"))
	}
	if s.ExternalStateTTL <= 0 {
		errs = append(errs, errors.New("AUTH_EXTERNAL_STATE_TTL: phải > 0"))
	}
	if s.ExternalStateKey != "" {
		if raw, err := base64.StdEncoding.DecodeString(s.ExternalStateKey); err != nil || len(raw) < 32 {
			errs = append(errs, errors.New("AUTH_EXTERNAL_STATE_KEY: phải là base64 của tối thiểu 32 byte"))
		}
	}
	if s.IsProd() && s.AppBaseURL != "" && s.ExternalStateKey == "" {
		errs = append(errs, errors.New("AUTH_EXTERNAL_STATE_KEY: bắt buộc ở staging/production khi có AUTH_APP_BASE_URL (ký state đăng nhập ngoài, mọi instance dùng chung)"))
	}
	if s.RecaptchaTimeout <= 0 {
		errs = append(errs, errors.New("AUTH_RECAPTCHA_TIMEOUT: phải > 0"))
	}
	if s.RefreshGrace < 0 || s.SessionMaxAge < 0 {
		errs = append(errs, errors.New("AUTH_REFRESH_GRACE, AUTH_SESSION_MAX_AGE: không được âm"))
	}
	if s.AccessTTL <= 0 || s.RefreshTTL <= 0 || s.IDTTL <= 0 || s.AuthCodeTTL <= 0 || s.MaxPolicyClaimBytes <= 0 {
		errs = append(errs, errors.New("AUTH_ACCESS_TTL, AUTH_REFRESH_TTL, AUTH_ID_TTL, AUTH_MAX_POLICY_CLAIM_BYTES: phải > 0"))
	}
	if s.BcryptCost < 4 || s.BcryptCost > 31 {
		errs = append(errs, errors.New("AUTH_BCRYPT_COST: phải trong [4, 31]"))
	}
	if s.PwnedTimeout <= 0 {
		errs = append(errs, errors.New("AUTH_PWNED_TIMEOUT: phải > 0"))
	}
	if s.PwnedURL != "" && !httpURL(s.PwnedURL) {
		errs = append(errs, errors.New("AUTH_PWNED_URL: phải là URL http(s) đầy đủ"))
	}
	if s.DefaultOAuthClients != "" {
		var clients []json.RawMessage
		if err := json.Unmarshal([]byte(s.DefaultOAuthClients), &clients); err != nil {
			errs = append(errs, errors.New("AUTH_DEFAULT_OAUTH_CLIENTS: phải là mảng JSON các ClientApplication"))
		}
	}
	if _, err := s.AllowedOriginList(); err != nil {
		errs = append(errs, fmt.Errorf("AUTH_ALLOWED_ORIGINS: %w", err))
	}
	if _, err := s.TrustedProxyNets(); err != nil {
		errs = append(errs, fmt.Errorf("AUTH_TRUSTED_PROXIES: %w", err))
	}
	if s.IsProd() {
		if !s.CookieSecure {
			errs = append(errs, errors.New("AUTH_COOKIE_SECURE: không được tắt ở staging/production"))
		}
		if s.BcryptCost < 10 {
			errs = append(errs, errors.New("AUTH_BCRYPT_COST: tối thiểu 10 ở staging/production"))
		}
		if !s.PwnedEnabled {
			errs = append(errs, errors.New("AUTH_PWNED_ENABLED: không được tắt kiểm tra mật khẩu rò rỉ ở staging/production"))
		}
	}
	if s.DatabaseMaxOpenConns <= 0 || s.DatabaseMaxIdleConns < 0 {
		errs = append(errs, errors.New("DATABASE_MAX_OPEN_CONNS phải > 0 và DATABASE_MAX_IDLE_CONNS >= 0"))
	}
	if s.ReadinessTimeout <= 0 {
		errs = append(errs, errors.New("READINESS_TIMEOUT: phải > 0"))
	}
	if s.ShutdownTimeout <= 0 || s.ShutdownDrainDelay < 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT phải > 0 và SHUTDOWN_DRAIN_DELAY phải >= 0"))
	}
	return errors.Join(errs...)
}

// IssuerURL là claim iss của token: AUTH_ISSUER, mặc định AUTH_BASE_URL (đúng từng ký tự, fhir-server phải cấu hình FHIR_JWT_ISSUER y hệt).
func (s Settings) IssuerURL() string {
	if s.Issuer != "" {
		return s.Issuer
	}
	return s.BaseURL
}

// AccessAudience là aud của access token: AUTH_AUDIENCE, mặc định chính issuer.
func (s Settings) AccessAudience() string {
	if s.Audience != "" {
		return s.Audience
	}
	return s.IssuerURL()
}

// TrustedProxyNets phân tích TrustedProxies thành các mạng CIDR.
func (s Settings) TrustedProxyNets() ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(s.TrustedProxies, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("%q không phải CIDR", part)
		}
		out = append(out, n)
	}
	return out, nil
}

// validateSMTP kiểm cấu hình SMTP: khi provider=smtp cần host, cổng, người gửi hợp lệ, và (nếu có username) mật khẩu; ở staging/production cấm gửi không mã hóa.
func (s Settings) validateSMTP() []error {
	var errs []error
	if s.SMTPAllowPlaintext && s.IsProd() {
		errs = append(errs, errors.New("AUTH_SMTP_ALLOW_PLAINTEXT: cấm ở staging/production (thư và mật khẩu không được đi ở dạng rõ)"))
	}
	if s.SMTPSecure != "" {
		if _, err := strconv.ParseBool(s.SMTPSecure); err != nil {
			errs = append(errs, errors.New("AUTH_SMTP_SECURE: phải là true, false hoặc để trống"))
		}
	}
	if s.MailProvider != "smtp" {
		return errs
	}
	if strings.TrimSpace(s.SMTPHost) == "" {
		errs = append(errs, errors.New("AUTH_SMTP_HOST: bắt buộc khi AUTH_MAIL_PROVIDER=smtp"))
	}
	if s.SMTPPort <= 0 || s.SMTPPort > 65535 {
		errs = append(errs, errors.New("AUTH_SMTP_PORT: phải trong [1, 65535]"))
	}
	if _, err := mail.ParseAddress(s.SMTPFrom); err != nil || strings.ContainsAny(s.SMTPFrom, "\r\n") {
		errs = append(errs, errors.New("AUTH_SMTP_FROM: bắt buộc, dạng a@b hoặc \"Tên <a@b>\""))
	}
	if s.SMTPUsername != "" && s.SMTPPassword == "" {
		errs = append(errs, errors.New("AUTH_SMTP_PASSWORD: bắt buộc khi có AUTH_SMTP_USERNAME"))
	}
	if s.SMTPTimeout <= 0 {
		errs = append(errs, errors.New("AUTH_SMTP_TIMEOUT: phải > 0"))
	}
	return errs
}

// ScimAllowedNets phân tích ScimAllowedIPs thành các mạng CIDR được gọi /scim/v2 (rỗng: không hạn chế).
func (s Settings) ScimAllowedNets() ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(s.ScimAllowedIPs, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("%q không phải CIDR", part)
		}
		out = append(out, n)
	}
	return out, nil
}

// AllowedOriginList phân tích AllowedOrigins thành danh sách origin đã chuẩn hóa ("*" giữ nguyên). Ở staging/production cấm "*" và
// chỉ nhận https.
func (s Settings) AllowedOriginList() ([]string, error) {
	var out []string
	for _, part := range strings.Split(s.AllowedOrigins, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "*" {
			if s.IsProd() {
				return nil, errors.New(`"*" không được dùng ở staging/production`)
			}
			out = append(out, part)
			continue
		}
		origin, err := httpsec.ParseOrigin(part)
		if err != nil {
			return nil, err
		}
		if s.IsProd() && !strings.HasPrefix(origin, "https://") {
			return nil, fmt.Errorf("origin %q: chỉ nhận https ở staging/production", part)
		}
		out = append(out, origin)
	}
	return out, nil
}
