// Package bootstrap là composition root của auth: nơi DUY NHẤT lắp ráp adapter cụ thể (kết nối DB, kho dữ liệu) thành
// HTTP handler và quản lý vòng đời. Hạ tầng mới (kho, hasher, mailer...) được lắp ở đây từ P1.
package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/attachfetch"
	"trivita-fhir/apps/auth/internal/infrastructure/binaryfetch"
	"trivita-fhir/apps/auth/internal/infrastructure/clientassertion"
	"trivita-fhir/apps/auth/internal/infrastructure/googleid"
	"trivita-fhir/apps/auth/internal/infrastructure/hubtopic"
	"trivita-fhir/apps/auth/internal/infrastructure/jti"
	"trivita-fhir/apps/auth/internal/infrastructure/keys"
	"trivita-fhir/apps/auth/internal/infrastructure/mailer"
	"trivita-fhir/apps/auth/internal/infrastructure/mfastate"
	"trivita-fhir/apps/auth/internal/infrastructure/oidc"
	"trivita-fhir/apps/auth/internal/infrastructure/password"
	"trivita-fhir/apps/auth/internal/infrastructure/pwned"
	"trivita-fhir/apps/auth/internal/infrastructure/recaptcha"
	"trivita-fhir/apps/auth/internal/infrastructure/secretbox"
	"trivita-fhir/apps/auth/internal/infrastructure/store"
	"trivita-fhir/apps/auth/internal/infrastructure/totp"
	"trivita-fhir/apps/auth/internal/infrastructure/useragent"
	"trivita-fhir/apps/auth/internal/presentation/httpapi"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/httpserve"
	pkgmailer "trivita-fhir/pkg/mailer"
	"trivita-fhir/pkg/mailer/smtp"
)

// Container giữ mọi adapter đã lắp và vòng đời của chúng.
type Container struct {
	Settings config.Settings
	DB       *database.DB
	Store    *fhirstore.Store
	// Auth là các use case lõi của auth (lắp đủ cổng ở build).
	Auth *application.Service
	// Keys là bộ khóa ký token (nạp khi dựng Handler; lệnh quản trị không cần).
	Keys *keys.Store
	// Authenticator xác minh Bearer token cho các route cần đăng nhập. Chưa có P3 thì nil: mọi route đó trả 401 (fail đóng).
	Authenticator application.Authenticator

	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

// New mở DB, áp dụng migration (hoặc từ chối khởi động nếu còn migration chưa chạy). Lỗi giữa chừng thì đóng những gì đã mở.
func New(ctx context.Context, s config.Settings) (*Container, error) {
	c := &Container{Settings: s}
	if err := c.build(ctx); err != nil {
		_ = c.Stop(context.Background())
		return nil, err
	}
	return c, nil
}

func (c *Container) build(ctx context.Context) (err error) {
	s := c.Settings
	if c.DB, err = database.Open(ctx, database.Config{
		URL: s.DatabaseURL, Echo: s.DatabaseEcho, MaxOpenConns: s.DatabaseMaxOpenConns,
		MaxIdleConns: s.DatabaseMaxIdleConns, ConnMaxLifetime: s.DatabaseConnLifetime, RequireSchema: s.DatabaseRequireSchema,
	}); err != nil {
		return err
	}
	c.onStop(func(context.Context) error { return c.DB.Close() })
	if c.DB.Schema != "" {
		slog.Info("database dùng schema riêng của service", "schema", c.DB.Schema)
	} else if c.DB.Dialect == database.Postgres && s.IsProd() {
		slog.Warn("DATABASE_REQUIRE_SCHEMA chưa bật: nếu chung database với service khác, hãy đặt schema riêng cho từng service và bật cờ này (docs/plans/microservices-shared-db.md)")
	}

	if c.Store, err = fhirstore.New(c.DB); err != nil {
		return err
	}
	if s.DatabaseAutoMigrate || c.DB.IsInMemory() {
		if err := c.Store.Migrate(ctx); err != nil {
			return err
		}
	} else {
		n, err := c.Store.Pending(ctx)
		if err != nil {
			return err
		}
		if n > 0 { // chạy nhiều replica: không khởi động trên schema cũ
			return errors.New("schema chưa được migrate: chạy `auth migrate up` hoặc bật DATABASE_AUTO_MIGRATE")
		}
	}
	c.Auth, err = newAuthService(s, c.Store)
	return err
}

// newAuthService lắp Service từ cấu hình: kho trên repository nền, bcrypt, kiểm tra mật khẩu rò rỉ, client dựng sẵn.
func newAuthService(s config.Settings, repo fhirrouter.Repository) (*application.Service, error) {
	hasher, err := password.NewBcrypt(s.BcryptCost)
	if err != nil {
		return nil, err
	}
	var checker application.PwnedChecker = pwned.Disabled{}
	if s.PwnedEnabled {
		checker = pwned.NewHIBP(s.PwnedURL, s.PwnedTimeout)
	} else {
		slog.Warn("AUTH_PWNED_ENABLED=false: không kiểm tra mật khẩu rò rỉ — chỉ dùng cho dev/test")
	}
	if s.IsProd() && (s.RateLimit == 0 || s.LoginRateLimit == 0) {
		slog.Warn("AUTH_RATE_LIMIT hoặc AUTH_LOGIN_RATE_LIMIT = 0: hạn mức đã bị tắt; đảm bảo có giới hạn ở gateway trước khi mở ra Internet")
	}
	var extra []fhir.ClientApplication
	if s.DefaultOAuthClients != "" {
		if err := json.Unmarshal([]byte(s.DefaultOAuthClients), &extra); err != nil {
			return nil, fmt.Errorf("AUTH_DEFAULT_OAUTH_CLIENTS: %w", err)
		}
		for i := range extra {
			if extra[i].ID == "" {
				return nil, fmt.Errorf("AUTH_DEFAULT_OAUTH_CLIENTS[%d]: thiếu id", i)
			}
			extra[i].ResourceType = "ClientApplication"
		}
	}
	var mail application.Mailer = mailer.None{}
	var emailSender application.EmailSender
	switch s.MailProvider {
	case "log":
		mail = mailer.Log{}
	case "smtp":
		sm, err := newSMTP(s)
		if err != nil {
			return nil, err
		}
		mail, emailSender = mailer.Adapter{Inner: sm}, sm
	}
	if s.MailProvider == "log" {
		emailSender = mailer.LogSender{}
	} else if s.MailProvider == "none" && s.EmailAllowProjectSMTP {
		// không có SMTP server nhưng project được dùng SMTP riêng: chỉ gửi qua cấu hình của project
		sm, err := smtp.New(smtp.Config{AllowProjectSMTP: true, AllowUnsafeOutbound: s.EmailAllowUnsafeOutbound, ConnectTimeout: s.SMTPTimeout, Limits: emailLimits(s), LocalName: smtpLocalName(s)})
		if err != nil {
			return nil, err
		}
		emailSender = sm
	}
	var emailBinaries application.BinarySource // giữ nil (interface) khi chưa cấu hình, để use case báo "chưa hỗ trợ"
	if s.EmailFHIRURL != "" {
		bs, err := binaryfetch.New(s.EmailFHIRURL, s.EmailFHIRTimeout)
		if err != nil {
			return nil, err
		}
		emailBinaries = bs
	}
	r := store.New(repo).Repos()
	return application.NewService(application.Deps{
		Users: r.Users, Logins: r.Logins, Memberships: r.Memberships, DomainConfigs: r.DomainConfigs, UserConfigs: r.UserConfigs, Projects: r.Projects, Clients: r.Clients,
		Launches: r.Launches, AccessPolicies: r.AccessPolicies, SecurityRequests: r.SecurityRequests, Resources: r.Resources, Tx: r.Tx,
		Recaptcha: recaptcha.New("", s.RecaptchaTimeout), RecaptchaKeys: application.RecaptchaConfig{SiteKey: s.RecaptchaSiteKey, SecretKey: s.RecaptchaSecretKey},
		Registration:        application.RegistrationConfig{Disabled: !s.RegisterEnabled, NewProjectDisabled: !s.NewProjectRegistrationEnabled, RequireVerifiedEmailForProject: s.RequireVerifiedEmailForProjectCreation, DefaultProjectFeatures: splitList(s.DefaultProjectFeatures)},
		BlockedEmailDomains: splitList(s.AdminBlockedEmailDomains),
		EmailSender:         emailSender,
		EmailAttachments:    attachfetch.New(30*time.Second, s.EmailAllowUnsafeOutbound),
		EmailBinaries:       emailBinaries,
		Email:               application.EmailConfig{AllowProjectSMTP: s.EmailAllowProjectSMTP, MaxAttachmentBytes: int64(s.EmailMaxBytes)},
		Scim:                application.ScimConfig{Enabled: s.ScimEnabled, AllowBasic: s.ScimAllowBasic, MaxPage: s.ScimMaxPage, MaxPatchOps: s.ScimMaxPatchOps},
		OutboundURL:         oidc.CheckURL,
		Mailer:              mail, Mail: application.MailConfig{AppBaseURL: s.AppBaseURL, AppName: s.MailAppName, SupportEmail: s.SupportEmail},
		Hasher: hasher, Pwned: checker, UserAgents: useragent.Parser{}, BaseURL: s.BaseURL, StandardClients: domain.StandardClients(s.CLIClientEnabled, extra),
	}), nil
}

// Stop đóng mọi tài nguyên theo thứ tự ngược.
func (c *Container) Stop(ctx context.Context) error {
	var errs []error
	for i := len(c.closers) - 1; i >= 0; i-- {
		errs = append(errs, c.closers[i](ctx))
	}
	c.closers = nil
	return errors.Join(errs...)
}

// BeginShutdown được gọi khi bắt đầu tắt: /health/ready trả 503 để K8s ngừng gửi traffic mới.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Readiness kiểm tra DB với timeout READINESS_TIMEOUT.
func (c *Container) Readiness(ctx context.Context) (bool, map[string]bool) {
	pctx, cancel := context.WithTimeout(ctx, c.Settings.ReadinessTimeout)
	defer cancel()
	dbOK := true
	if err := c.Store.Ping(pctx); err != nil {
		slog.WarnContext(ctx, "readiness check lỗi", "check", "database", "error", err.Error())
		dbOK = false
	}
	accepting := !c.draining.Load()
	return dbOK && accepting, map[string]bool{"accepting_traffic": accepting, "database": dbOK}
}

// initTokens nạp khóa ký và gắn Signer, Verifier, cấu hình token vào Service (một lần). Không có AUTH_SIGNING_KEY_PATH (chỉ được ở
// local, development, test) thì dùng khóa ES256 tạm trong bộ nhớ: token mất hiệu lực khi khởi động lại và không chạy được nhiều instance.
func (c *Container) initTokens() error {
	if c.Keys != nil {
		return nil
	}
	s := c.Settings
	var st *keys.Store
	var err error
	if s.SigningKeyPath != "" {
		st, err = keys.Load(s.SigningKeyPath, s.ActiveKID)
	} else {
		slog.Warn("AUTH_SIGNING_KEY_PATH chưa đặt: dùng khóa ký tạm trong bộ nhớ, chỉ cho dev/test")
		st, err = keys.Ephemeral(keys.ES256)
	}
	if err != nil {
		return fmt.Errorf("khóa ký token: %w", err)
	}
	c.Keys = st
	c.Auth.Signer, c.Auth.Verifier = st, st
	c.Auth.Assertions = clientassertion.New(s.AllowPrivateJWKS)
	c.Auth.OAuthRegister = s.OAuthRegisterEnabled
	if s.RedisURL != "" {
		store, err := jti.NewRedis(s.RedisURL)
		if err != nil {
			return fmt.Errorf("AUTH_REDIS_URL: %w", err)
		}
		c.Auth.JTIs = store
		c.onStop(func(context.Context) error { return store.Close() })
	} else {
		if s.IsProd() {
			slog.Warn("AUTH_REDIS_URL chưa đặt: jti của client assertion chỉ nhớ trong bộ nhớ, chạy nhiều instance thì assertion phát lại được giữa các instance")
		}
		c.Auth.JTIs = jti.NewMemory(0)
	}
	if err := c.wireMFA(s); err != nil {
		return err
	}
	if err := c.wireExternal(s); err != nil {
		return err
	}
	if s.FHIRcastHubURL != "" {
		var topics *hubtopic.Topics
		if s.RedisURL != "" {
			if topics, err = hubtopic.NewRedis(s.RedisURL); err != nil {
				return fmt.Errorf("AUTH_FHIRCAST_HUB_URL: %w", err)
			}
		} else {
			slog.Warn("AUTH_REDIS_URL chưa đặt: topic FHIRcast chỉ nhớ trong bộ nhớ, chạy nhiều instance thì cùng một người nhận topic khác nhau")
			topics = hubtopic.NewMemory()
		}
		c.onStop(func(context.Context) error { return topics.Close() })
		c.Auth.FHIRcast = &application.HubConfig{URL: strings.TrimRight(s.FHIRcastHubURL, "/"), Topics: topics}
	}
	c.Auth.Tokens = application.TokenConfig{
		Issuer: s.IssuerURL(), Audiences: []string{s.AccessAudience()}, AccessTTL: s.AccessTTL, RefreshTTL: s.RefreshTTL, IDTTL: s.IDTTL,
		MaxPolicyBytes: s.MaxPolicyClaimBytes, BaseURL: s.BaseURL, AuthCodeTTL: s.AuthCodeTTL, AllowPlainPKCE: s.AllowPlainPKCE, RefreshGrace: s.RefreshGrace, SessionMaxAge: s.SessionMaxAge, AppBaseURL: s.AppBaseURL, FHIRBaseURL: s.FHIRBaseURL,
	}
	if c.Authenticator == nil {
		c.Authenticator = c.Auth
	}
	return nil
}

// Handler dựng HTTP handler. instrument (có thể nil) gắn observability trước mọi middleware khác.
func (c *Container) Handler(instrument func(*echo.Echo) error) (*echo.Echo, error) {
	if err := c.initTokens(); err != nil {
		return nil, err
	}
	nets, err := c.Settings.TrustedProxyNets()
	if err != nil {
		return nil, err
	}
	origins, err := c.Settings.AllowedOriginList()
	if err != nil {
		return nil, err
	}
	scimNets, err := c.Settings.ScimAllowedNets()
	if err != nil {
		return nil, fmt.Errorf("AUTH_SCIM_ALLOWED_IPS: %w", err)
	}
	dep := httpapi.Deps{
		BaseURL: c.Settings.BaseURL, AllowedOrigins: origins,
		Ready: c.Readiness, Instrument: instrument, Debug: c.Settings.Debug, Swagger: c.Settings.SwaggerEnabled(),
		Auth: c.Auth, Authenticator: c.Authenticator, CookieSecure: c.Settings.CookieSecure,
		OAuth:     &httpapi.OAuthInfo{Issuer: c.Settings.IssuerURL(), JWKS: c.Keys.JWKS, SigningAlg: c.Keys.SigningAlg(), AllowPlainPKCE: c.Settings.AllowPlainPKCE, AuthorizeEnabled: c.Settings.AppBaseURL != "", AppBaseURL: c.Settings.AppBaseURL, RegisterEnabled: c.Settings.OAuthRegisterEnabled},
		RateLimit: c.Settings.RateLimit, RateBurst: c.Settings.RateBurst,
		ExternalEnabled: c.Auth.External.Enabled(), AdminEnabled: c.Settings.AdminEnabled,
		ScimEnabled: c.Settings.ScimEnabled, EmailEnabled: c.Settings.EmailEnabled, ScimAllowedNets: scimNets, ScimMaxBodyBytes: int64(c.Settings.ScimMaxBodyBytes),
		LoginRateLimit: c.Settings.LoginRateLimit, LoginRateBurst: c.Settings.LoginRateBurst,
	}
	if len(nets) > 0 {
		opts := []echo.TrustOption{echo.TrustLoopback(false), echo.TrustLinkLocal(false), echo.TrustPrivateNet(false)}
		for _, n := range nets {
			opts = append(opts, echo.TrustIPRange(n))
		}
		dep.IPExtractor = echo.ExtractIPFromXFFHeader(opts...)
	}
	return httpapi.New(dep)
}

// ServeOptions là các móc nối của composition root (main hoặc test).
type ServeOptions struct {
	Instrument func(*echo.Echo) error // gắn observability (nil = không)
	Flush      func()                 // flush telemetry sau khi server đã tắt (nil = không)
	OnListen   func(addr net.Addr)
}

// Serve chạy service tới khi ctx bị hủy (SIGTERM/SIGINT), rồi tắt êm (xem pkg/httpserve.Run).
func Serve(ctx context.Context, s config.Settings, opts ServeOptions) error {
	defer func() {
		if opts.Flush != nil {
			opts.Flush()
		}
	}()
	c, err := New(ctx, s)
	if err != nil {
		return err
	}
	e, err := c.Handler(opts.Instrument)
	if err != nil {
		_ = c.Stop(context.Background())
		return err
	}
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: e, ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}

// wireMFA lắp MFA (P5): kho trạng thái (Redis nếu có AUTH_REDIS_URL, không thì bộ nhớ: chỉ đúng với một instance), mã hóa mfaSecret khi lưu, TOTP và QR.
// Khóa mã hóa sai định dạng là lỗi khởi động (không lưu bí mật thô vì cấu hình hỏng).
func (c *Container) wireMFA(s config.Settings) error {
	var box application.SecretBox = secretbox.Plain{}
	if s.MFASecretKeys != "" {
		b, err := secretbox.ParseKeys(s.MFASecretKeys)
		if err != nil {
			return fmt.Errorf("AUTH_MFA_SECRET_KEYS: %w", err)
		}
		box = b
	} else {
		slog.Warn("AUTH_MFA_SECRET_KEYS chưa đặt: User.mfaSecret lưu dạng thô, chỉ cho dev/test")
	}
	if s.RedisURL != "" {
		st, closeFn, err := mfastate.OpenRedis(s.RedisURL)
		if err != nil {
			return fmt.Errorf("AUTH_REDIS_URL: %w", err)
		}
		c.Auth.MFAState = st
		c.onStop(func(context.Context) error { return closeFn() })
	} else {
		slog.Warn("AUTH_REDIS_URL chưa đặt: hạn mức thử và chống dùng lại mã MFA chỉ nhớ trong bộ nhớ, chạy nhiều instance thì hạn mức nhân lên theo số instance")
		c.Auth.MFAState = mfastate.NewMemory(nil)
	}
	c.Auth.TOTP, c.Auth.QR, c.Auth.MFASecrets = totp.Authenticator{}, totp.QRCode{}, box
	c.Auth.MFACfg = application.MFAConfig{
		Window: s.MFAWindow, LoginAttemptLimit: s.MFALoginAttemptLimit, UserAttemptLimit: s.MFAUserAttemptLimit, LoginExpiration: s.MFALoginExpiration,
		EmailCodeExpiration: s.MFAEmailCodeExpiration, EnrollmentExpiration: s.MFAEnrollmentExpiration, EmailSendLimit: s.MFAEmailSendLimit,
	}
	c.Auth.MFA = nil // dùng cổng của chính Service (mfaGate) thay vì FailClosedMFA
	return c.Auth.UseMFA()
}

// wireExternal lắp đăng nhập qua IdP ngoài (P6): client OIDC và bộ xác minh id_token (SSRF-safe), khóa ký state và danh sách IdP cấu hình ở máy chủ. Chỉ bật khi có
// AUTH_APP_BASE_URL (cần để chuyển người dùng về app). Ngoài staging/production không có khóa thì dùng khóa tạm (cảnh báo: chỉ một instance).
func (c *Container) wireExternal(s config.Settings) error {
	c.Auth.OIDC, c.Auth.IDVerifier = oidc.NewClient(s.AllowPrivateJWKS), oidc.NewIDVerifier(s.AllowPrivateJWKS)
	ext := application.ExternalConfig{TTL: s.ExternalStateTTL, AllowUnsafeOutbound: s.AllowPrivateJWKS}
	if s.ExternalAuthProviders != "" {
		if err := json.Unmarshal([]byte(s.ExternalAuthProviders), &ext.Providers); err != nil {
			return fmt.Errorf("AUTH_EXTERNAL_AUTH_PROVIDERS: %w", err)
		}
		for i, p := range ext.Providers {
			if p.Issuer == "" || p.IdentityProvider == nil {
				return fmt.Errorf("AUTH_EXTERNAL_AUTH_PROVIDERS[%d]: cần issuer và identityProvider", i)
			}
		}
	}
	if s.AppBaseURL != "" {
		switch {
		case s.ExternalStateKey != "":
			key, err := base64.StdEncoding.DecodeString(s.ExternalStateKey)
			if err != nil {
				return fmt.Errorf("AUTH_EXTERNAL_STATE_KEY: %w", err)
			}
			ext.StateKey = key
		default:
			slog.Warn("AUTH_EXTERNAL_STATE_KEY chưa đặt: dùng khóa ký state tạm trong bộ nhớ, chỉ cho dev/test và MỘT instance")
			ext.StateKey = make([]byte, 32)
			if _, err := rand.Read(ext.StateKey); err != nil {
				return fmt.Errorf("sinh khóa state: %w", err)
			}
		}
	}
	c.Auth.External = ext
	if s.GoogleEnabled || s.GoogleClientID != "" {
		c.Auth.Google, c.Auth.GoogleClientID = googleid.New(s.AllowPrivateJWKS, s.GoogleJWKSURL), s.GoogleClientID
	}
	return nil
}

// newSMTP dựng adapter SMTP từ cấu hình (đã được kiểm ở config). SMTP của server là cấu hình quản trị nên được tin; SMTP riêng của project chưa được dùng ở auth
// (cần mã hóa Project.secret, Q-E7), nên AllowProjectSMTP tắt.
func newSMTP(s config.Settings) (*smtp.Mailer, error) {
	cfg := pkgmailer.SMTPConfig{Host: strings.TrimSpace(s.SMTPHost), Port: s.SMTPPort, Username: s.SMTPUsername, Password: s.SMTPPassword}
	if s.SMTPSecure != "" {
		v, err := strconv.ParseBool(s.SMTPSecure)
		if err != nil {
			return nil, fmt.Errorf("AUTH_SMTP_SECURE: %w", err)
		}
		cfg.Secure = &v
	}
	if s.SMTPAllowPlaintext {
		slog.Warn("AUTH_SMTP_ALLOW_PLAINTEXT=true: cho gửi thư (và mật khẩu SMTP) không mã hóa — chỉ dùng cho dev/test")
	}
	var tlsCfg *tls.Config
	if s.SMTPCAFile != "" {
		pemBytes, err := os.ReadFile(s.SMTPCAFile)
		if err != nil {
			return nil, fmt.Errorf("AUTH_SMTP_CA_FILE: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("AUTH_SMTP_CA_FILE: không có chứng chỉ PEM hợp lệ")
		}
		tlsCfg = &tls.Config{RootCAs: pool}
	}
	return smtp.New(smtp.Config{TLSConfig: tlsCfg, Server: &cfg, AllowProjectSMTP: s.EmailAllowProjectSMTP, AllowUnsafeOutbound: s.EmailAllowUnsafeOutbound, Limits: emailLimits(s), SupportEmail: s.SMTPFrom, ApprovedSenders: pkgmailer.SplitApproved(s.SMTPApprovedSenders), AllowPlaintext: s.SMTPAllowPlaintext,
		ConnectTimeout: s.SMTPTimeout, LocalName: smtpLocalName(s)})
}

// smtpLocalName là tên trong EHLO: host của AUTH_BASE_URL nếu có, không thì localhost.
func smtpLocalName(s config.Settings) string {
	if u, err := url.Parse(s.BaseURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "localhost"
}

// emailLimits là giới hạn mỗi thư (số người nhận, cỡ thư).
func emailLimits(s config.Settings) pkgmailer.Limits {
	return pkgmailer.Limits{MaxRecipients: s.EmailMaxRecipients, MaxBytes: int64(s.EmailMaxBytes)}
}
