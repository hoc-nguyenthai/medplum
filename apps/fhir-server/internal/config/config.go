// Package config đọc cấu hình fhir-server từ biến môi trường (và file .env nếu có).
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"trivita-fhir/pkg/envconfig"
	"trivita-fhir/pkg/httpsec"
)

// DevAllowedOrigin là origin của ứng dụng web chạy ở máy dev, được cho phép mặc định khi ENVIRONMENT=local.
const DevAllowedOrigin = "http://localhost:3000"

// DefaultProdRateLimit là hạn mức mỗi IP (yêu cầu/giây) khi staging/production không đặt FHIR_RATE_LIMIT.
const DefaultProdRateLimit = 20

// Token là một bearer token có tên (tên dùng ghi log, secret không bao giờ được log).
type Token struct{ Name, Secret string }

// MinSecretLen là độ dài tối thiểu của secret.
const MinSecretLen = 24

// Settings là toàn bộ cấu hình. Mặc định an toàn: bắt buộc có token ghi; dữ liệu lưu SQLite (chỉ dev).
// Biến của trivita-observability-go (OTEL_*, LOG_LEVEL, LOG_JSON) do thư viện đó tự đọc.
type Settings struct {
	AppName     string
	Addr        string
	Debug       bool
	Environment string // ENVIRONMENT: local | development | staging | production

	ReadinessTimeout   time.Duration
	ShutdownDrainDelay time.Duration
	ShutdownTimeout    time.Duration
	ReadHeaderTimeout  time.Duration

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

	// BaseURL là địa chỉ công khai của API FHIR (dùng cho Location, fullUrl, link phân trang),
	// ví dụ https://fhir.example.org/fhir/R4.
	BaseURL string
	// SkipValidation bỏ kiểm tra resource khi ghi; SkipConstraints chỉ kiểm tra cấu trúc, bỏ invariant FHIRPath.
	SkipValidation  bool
	SkipConstraints bool
	// UpdateAsCreate: PUT Type/id tạo mới với id do client chọn khi chưa có (mặc định tắt: 404).
	UpdateAsCreate bool

	// Token dạng `tên:secret`, phân tách bằng dấu phẩy (nhiều token để xoay vòng khóa).
	ReadTokens   string // chỉ đọc và tìm kiếm
	WriteTokens  string // đọc và ghi (create, update, patch, delete, batch, transaction)
	AdminTokens  string // ghi và quản trị ($reindex, $expunge)
	AuthDisabled bool   // CHỈ cho dev: tắt xác thực

	// OAuth2/OIDC: bật khi FHIR_JWT_ISSUER được đặt. Access token (JWT) được kiểm tra chữ ký theo JWKS của identity
	// provider, và dữ liệu được tách theo project lấy từ claim project. Loại trừ với token tĩnh ở trên.
	JWTIssuer       string
	JWTAudience     string
	JWKSURL         string
	JobSigningKey   string // khóa ký quyền của job nền (dùng chung mọi replica); bắt buộc với JWKS, dev HMAC tự suy ra
	JWTHMACSecret   string // CHỈ cho dev/test: khóa HS256 dùng chung thay cho JWKS
	JWTProjectClaim string
	JWTScopeClaim   string
	JWTPolicyClaim  string
	JWTSuperAdmin   bool // bật scope fhir.super (super-admin xuyên project, có nhật ký kiểm toán)

	// Job nền (AsyncJob): số job chạy đồng thời trong process.
	JobConcurrency int
	// JobLease: thời gian một replica giữ quyền chạy job trước khi replica khác tiếp quản (tự gia hạn khi job còn chạy).
	// JobMaintenanceInterval: chu kỳ chạy lại job mồ côi và xóa kết quả export hết hạn (0 = tắt).
	// ExportRetention: thời gian giữ kết quả bulk export (âm = không bao giờ xóa).
	JobLease               time.Duration
	JobMaintenanceInterval time.Duration
	ExportRetention        time.Duration

	// Subscription (rest-hook). Sự kiện thay đổi đi qua broker: memory (1 process) | redis (nhiều replica).
	Subscriptions bool
	// WSMode chọn nơi giữ websocket của Subscription: "off" (không có), "embedded" (Hub trong chính tiến trình này) hoặc "issuer"
	// (chỉ cấp token bind và khớp sự kiện; kết nối do ws-gateway giữ, cần Redis). Rỗng: suy từ WebSocket (bí danh cũ
	// FHIR_WEBSOCKET=true là "embedded", còn lại "off"). Dùng WSModeResolved để đọc.
	WSMode               string
	WebSocket            bool          // BÍ DANH CŨ của WSMode="embedded" (FHIR_WEBSOCKET)
	WSSigningKey         string        // khóa ký token websocket (dùng chung mọi instance)
	WSSigningKeyPrevious string        // khóa cũ chỉ để xác minh khi xoay khóa, cách nhau bằng dấu phẩy (tối đa 3)
	WSURL                string        // websocket-url trả cho client (mặc định suy từ FHIR_BASE_URL)
	WSMaxConnections     int           // số kết nối websocket tối đa mỗi instance
	WSMaxBindingsPerUser int           // số lượt bind websocket đồng thời tối đa của một người trong một project (mỗi instance)
	WSAllowedOrigins     string        // origin được phép, cách nhau bằng dấu phẩy (rỗng: chỉ cùng host)
	WSHeartbeat          time.Duration // chu kỳ heartbeat
	// AllowedOrigins là origin (cách nhau bằng dấu phẩy) được gọi API từ trình duyệt qua CORS, dạng http(s)://host[:port]. Rỗng: không
	// origin nào (mặc định ENVIRONMENT=local: http://localhost:3000). Khác FHIR_WS_ALLOWED_ORIGINS (kiểm Origin lúc mở websocket).
	AllowedOrigins string
	// TrustedProxies là CIDR của reverse proxy được tin khi đọc IP người gọi từ X-Forwarded-For (cách nhau bằng dấu phẩy). Rỗng: dùng
	// IP kết nối trực tiếp, nên đứng sau proxy mà không khai thì mọi người gọi dùng chung một IP (và một hạn mức).
	TrustedProxies string
	// RateLimit là số yêu cầu mỗi giây cho mỗi IP trên /fhir/R4 (0: tắt; không đặt thì ở staging/production là DefaultProdRateLimit); RateBurst là cỡ đợt (mặc định gấp đôi RateLimit, tối thiểu 1).
	RateLimit           float64
	RateBurst           int
	BrokerDriver        string
	RedisURL            string
	WebhookWorkers      int
	WebhookMaxAttempts  int
	WebhookTimeout      time.Duration
	WebhookAllowPrivate bool // CHỈ cho dev: cho phép giao tới địa chỉ nội bộ (chống SSRF bị tắt)

	// Kho nội dung Binary (docs/plans/modules/email-storage.md, S3a/S3b). BinaryStorage rỗng: tắt (Binary giữ `data` base64 trong CSDL như
	// trước, không có /storage và không có Binary thô). Dạng `file:<thư mục>` (giữ bố cục Medplum); tiền tố khác bị từ chối.
	BinaryStorage string
	// StorageBaseURL là gốc công khai của URL ký (mặc định: gốc của FHIR_BASE_URL + /storage); host phải là host client nhìn thấy.
	StorageBaseURL string
	// StorageSigningKeyPath là PEM khóa riêng RSA (PKCS#8 không mật khẩu, quyền 0600). Staging/production bắt buộc; dev không đặt thì sinh
	// khóa tạm (URL mất hiệu lực khi khởi động lại, không chạy được nhiều replica). StorageSigningPreviousKeys là các tệp PEM khóa
	// CÔNG KHAI cũ (cách nhau bằng dấu phẩy) chỉ để xác minh khi xoay khóa.
	StorageSigningKeyPath      string
	StorageSigningPreviousKeys string
	StorageURLTTL              time.Duration // hạn URL ký (mặc định 1h, tối đa 24h)
	StorageMaxUploadBytes      int           // trần nội dung tải lên sau giải nén (mặc định 256 MiB)
	StorageLegacyGET           bool          // chấp nhận chữ ký GET kiểu cũ của Medplum (không có tiền tố "GET ")
	// Base64BinaryMaxBytes là trần `data` base64 (đã giải mã) khi ghi resource Binary (mặc định 1 MiB; âm: không trần).
	Base64BinaryMaxBytes int

	// S3 (BinaryStorage dạng `s3:<bucket>[/<prefix>]`, S7). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log.
	S3Region          string // bắt buộc (vd eu-west-1)
	S3Endpoint        string // kho tương thích S3 (MinIO...), http(s)://host[:port]; trống: AWS
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3SessionToken    string
	S3SSE             string // "", "AES256" hoặc "aws:kms"
}

// StorageKind trả "file", "s3" hoặc "" (không nhận ra / tắt).
func (s Settings) StorageKind() string {
	switch {
	case strings.HasPrefix(s.BinaryStorage, "file:"):
		return "file"
	case strings.HasPrefix(s.BinaryStorage, "s3:"):
		return "s3"
	}
	return ""
}

// S3Bucket và S3Prefix tách `s3:<bucket>[/<prefix>]`; prefix trả dạng "a/b/" (hoặc rỗng).
func (s Settings) S3Bucket() string {
	b, _, _ := strings.Cut(strings.TrimPrefix(s.BinaryStorage, "s3:"), "/")
	return b
}

func (s Settings) S3Prefix() string {
	_, p, _ := strings.Cut(strings.TrimPrefix(s.BinaryStorage, "s3:"), "/")
	if p = strings.Trim(p, "/"); p == "" {
		return ""
	}
	return p + "/"
}

// StorageEnabled cho biết kho Binary được bật.
func (s Settings) StorageEnabled() bool { return s.BinaryStorage != "" }

// StorageDir là thư mục của BinaryStorage dạng `file:<dir>`.
func (s Settings) StorageDir() string { return strings.TrimPrefix(s.BinaryStorage, "file:") }

// StoragePublicURL là gốc URL ký: StorageBaseURL, hoặc gốc của FHIR_BASE_URL (bỏ hậu tố /fhir/R4) cộng /storage.
func (s Settings) StoragePublicURL() string {
	if s.StorageBaseURL != "" {
		return strings.TrimRight(s.StorageBaseURL, "/")
	}
	return strings.TrimSuffix(strings.TrimRight(s.BaseURL, "/"), "/fhir/R4") + "/storage"
}

// StoragePreviousKeyPaths tách StorageSigningPreviousKeys.
func (s Settings) StoragePreviousKeyPaths() []string {
	var out []string
	for _, p := range strings.Split(s.StorageSigningPreviousKeys, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Default trả về cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-fhir-server", Addr: ":8005", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		DatabaseURL:       "sqlite://./fhir.db", DatabaseAutoMigrate: true,
		DatabaseMaxOpenConns: 20, DatabaseMaxIdleConns: 5, DatabaseConnLifetime: 30 * time.Minute,
		BaseURL:        "http://localhost:8005/fhir/R4",
		JobConcurrency: 2,
		JobLease:       5 * time.Minute, JobMaintenanceInterval: 30 * time.Second, ExportRetention: 24 * time.Hour,
		JWTProjectClaim: "fhir_project", JWTScopeClaim: "scope", JWTPolicyClaim: "fhir_access_policy",
		BrokerDriver: "memory", RedisURL: "redis://localhost:6379/0",
		WebhookWorkers: 4, WebhookMaxAttempts: 5, WebhookTimeout: 10 * time.Second,
		StorageURLTTL: time.Hour, StorageMaxUploadBytes: 256 << 20, Base64BinaryMaxBytes: 1 << 20,
		WSMaxConnections: 1000, WSMaxBindingsPerUser: 20, WSHeartbeat: 30 * time.Second,
	}
}

// Load đọc `.env` (nếu có, không ghi đè biến môi trường thật) rồi biến môi trường. Gom mọi lỗi lại.
func Load() (Settings, error) {
	if err := envconfig.LoadDotEnv(".env"); err != nil {
		return Settings{}, err
	}
	return FromEnv(os.Getenv)
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
	r.String(&s.BaseURL, "FHIR_BASE_URL")
	r.Bool(&s.SkipValidation, "FHIR_SKIP_VALIDATION")
	r.Bool(&s.SkipConstraints, "FHIR_SKIP_CONSTRAINTS")
	r.Bool(&s.UpdateAsCreate, "FHIR_UPDATE_AS_CREATE")
	r.String(&s.ReadTokens, "FHIR_READ_TOKENS")
	r.String(&s.WriteTokens, "FHIR_WRITE_TOKENS")
	r.String(&s.AdminTokens, "FHIR_ADMIN_TOKENS")
	r.Bool(&s.AuthDisabled, "FHIR_AUTH_DISABLED")
	r.String(&s.JWTIssuer, "FHIR_JWT_ISSUER")
	r.String(&s.JWTAudience, "FHIR_JWT_AUDIENCE")
	r.String(&s.JWKSURL, "FHIR_JWKS_URL")
	r.String(&s.JWTHMACSecret, "FHIR_JWT_HMAC_SECRET")
	r.String(&s.JobSigningKey, "FHIR_JOB_SIGNING_KEY")
	r.String(&s.JWTProjectClaim, "FHIR_JWT_PROJECT_CLAIM")
	r.String(&s.JWTScopeClaim, "FHIR_JWT_SCOPE_CLAIM")
	r.String(&s.JWTPolicyClaim, "FHIR_JWT_POLICY_CLAIM")
	r.Bool(&s.JWTSuperAdmin, "FHIR_JWT_SUPER_ADMIN")
	r.Int(&s.JobConcurrency, "FHIR_JOB_CONCURRENCY")
	r.Duration(&s.JobLease, "FHIR_JOB_LEASE")
	r.Duration(&s.JobMaintenanceInterval, "FHIR_JOB_MAINTENANCE_INTERVAL")
	r.Duration(&s.ExportRetention, "FHIR_EXPORT_RETENTION")
	r.Bool(&s.Subscriptions, "FHIR_SUBSCRIPTIONS")
	r.String(&s.WSMode, "FHIR_WS_MODE")
	r.Bool(&s.WebSocket, "FHIR_WEBSOCKET")
	r.String(&s.WSSigningKey, "FHIR_WS_SIGNING_KEY")
	r.String(&s.WSSigningKeyPrevious, "FHIR_WS_SIGNING_KEY_PREVIOUS")
	r.String(&s.WSURL, "FHIR_WS_URL")
	r.Int(&s.WSMaxConnections, "FHIR_WS_MAX_CONNECTIONS")
	r.Int(&s.WSMaxBindingsPerUser, "FHIR_WS_MAX_BINDINGS_PER_USER")
	r.String(&s.WSAllowedOrigins, "FHIR_WS_ALLOWED_ORIGINS")
	r.Duration(&s.WSHeartbeat, "FHIR_WS_HEARTBEAT")
	r.String(&s.AllowedOrigins, "FHIR_ALLOWED_ORIGINS")
	if s.AllowedOrigins == "" && s.Environment == "local" {
		s.AllowedOrigins = DevAllowedOrigin
	}
	r.String(&s.TrustedProxies, "FHIR_TRUSTED_PROXIES")
	r.Float(&s.RateLimit, "FHIR_RATE_LIMIT")
	if get("FHIR_RATE_LIMIT") == "" && isProd(s.Environment) {
		s.RateLimit = DefaultProdRateLimit // không đặt thì bật ở staging/production; đặt rõ 0 mới tắt
	}
	r.Int(&s.RateBurst, "FHIR_RATE_BURST")
	r.String(&s.BrokerDriver, "BROKER_DRIVER")
	r.String(&s.RedisURL, "REDIS_URL")
	r.Int(&s.WebhookWorkers, "FHIR_WEBHOOK_WORKERS")
	r.Int(&s.WebhookMaxAttempts, "FHIR_WEBHOOK_MAX_ATTEMPTS")
	r.Duration(&s.WebhookTimeout, "FHIR_WEBHOOK_TIMEOUT")
	r.Bool(&s.WebhookAllowPrivate, "FHIR_WEBHOOK_ALLOW_PRIVATE")
	r.String(&s.BinaryStorage, "FHIR_BINARY_STORAGE")
	r.String(&s.StorageBaseURL, "FHIR_STORAGE_BASE_URL")
	r.String(&s.StorageSigningKeyPath, "FHIR_STORAGE_SIGNING_KEY_PATH")
	r.String(&s.StorageSigningPreviousKeys, "FHIR_STORAGE_SIGNING_PREVIOUS_KEYS")
	r.Duration(&s.StorageURLTTL, "FHIR_STORAGE_URL_TTL")
	r.Int(&s.StorageMaxUploadBytes, "FHIR_STORAGE_MAX_UPLOAD_BYTES")
	r.Bool(&s.StorageLegacyGET, "FHIR_STORAGE_LEGACY_GET_SIGNATURE")
	r.String(&s.S3Region, "FHIR_S3_REGION")
	r.String(&s.S3Endpoint, "FHIR_S3_ENDPOINT")
	r.String(&s.S3AccessKeyID, "FHIR_S3_ACCESS_KEY_ID")
	r.String(&s.S3SecretAccessKey, "FHIR_S3_SECRET_ACCESS_KEY")
	r.String(&s.S3SessionToken, "FHIR_S3_SESSION_TOKEN")
	r.String(&s.S3SSE, "FHIR_S3_SSE")
	r.Int(&s.Base64BinaryMaxBytes, "FHIR_BASE64_BINARY_MAX_BYTES")
	r.Add(s.validateWSEnv(get))
	r.Add(s.validate())
	return s, r.Err()
}

var tokenName = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// ParseTokens đọc `tên:secret,tên2:secret2`. Tên duy nhất, secret tối thiểu MinSecretLen ký tự (chỉ cắt tại dấu `:`
// đầu tiên nên secret có thể chứa `:`; secret không được chứa dấu phẩy). Lỗi không bao giờ chứa secret.
func ParseTokens(raw string) ([]Token, error) {
	var out []Token
	seen := map[string]bool{}
	for i, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, secret, ok := strings.Cut(part, ":")
		switch {
		case !ok || !tokenName.MatchString(name):
			return nil, fmt.Errorf("token #%d: định dạng phải là `tên:secret` với tên [a-z0-9_-]{1,32}", i+1)
		case len(secret) < MinSecretLen:
			return nil, fmt.Errorf("token %q: secret tối thiểu %d ký tự", name, MinSecretLen)
		case seen[name]:
			return nil, fmt.Errorf("token %q bị trùng tên", name)
		}
		seen[name] = true
		out = append(out, Token{name, secret})
	}
	return out, nil
}

// ValidateForServe kiểm tra thêm các điều kiện chỉ cần khi chạy HTTP server (lệnh migrate không cần token).
// JWTEnabled cho biết xác thực bằng JWT (OAuth2/OIDC) được bật.
func (s Settings) JWTEnabled() bool { return s.JWTIssuer != "" }

func (s Settings) ValidateForServe() error {
	write, err := ParseTokens(s.WriteTokens)
	if err != nil {
		return err
	}
	admin, err := ParseTokens(s.AdminTokens)
	if err != nil {
		return err
	}
	if !s.AuthDisabled && !s.JWTEnabled() && len(write)+len(admin) == 0 {
		return errors.New("FHIR_WRITE_TOKENS hoặc FHIR_ADMIN_TOKENS: cần ít nhất một token ghi (hoặc FHIR_AUTH_DISABLED=true cho dev)")
	}
	return nil
}

func isProd(env string) bool { return env == "production" || env == "staging" }

// IsProd cho biết môi trường staging hoặc production (áp các ràng buộc chặt).
func (s Settings) IsProd() bool { return isProd(s.Environment) }

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
			if isProd(s.Environment) {
				return nil, errors.New(`"*" không được dùng ở staging/production`)
			}
			out = append(out, part)
			continue
		}
		origin, err := httpsec.ParseOrigin(part)
		if err != nil {
			return nil, err
		}
		if isProd(s.Environment) && !strings.HasPrefix(origin, "https://") {
			return nil, fmt.Errorf("origin %q: chỉ nhận https ở staging/production", part)
		}
		out = append(out, origin)
	}
	return out, nil
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
	if _, err := s.AllowedOriginList(); err != nil {
		errs = append(errs, fmt.Errorf("FHIR_ALLOWED_ORIGINS: %w", err))
	}
	if _, err := s.TrustedProxyNets(); err != nil {
		errs = append(errs, fmt.Errorf("FHIR_TRUSTED_PROXIES: %w", err))
	}
	errs = append(errs, s.validateStorage()...)
	if s.RateLimit < 0 || s.RateBurst < 0 {
		errs = append(errs, errors.New("FHIR_RATE_LIMIT và FHIR_RATE_BURST: không được âm"))
	}
	read, err := ParseTokens(s.ReadTokens)
	if err != nil {
		errs = append(errs, fmt.Errorf("FHIR_READ_TOKENS: %w", err))
	}
	write, err := ParseTokens(s.WriteTokens)
	if err != nil {
		errs = append(errs, fmt.Errorf("FHIR_WRITE_TOKENS: %w", err))
	}
	admin, err := ParseTokens(s.AdminTokens)
	if err != nil {
		errs = append(errs, fmt.Errorf("FHIR_ADMIN_TOKENS: %w", err))
	}
	names := map[string]bool{}
	for _, group := range [][]Token{write, admin, read} {
		for _, t := range group {
			if names[t.Name] {
				errs = append(errs, fmt.Errorf("token %q trùng tên giữa FHIR_READ_TOKENS, FHIR_WRITE_TOKENS và FHIR_ADMIN_TOKENS", t.Name))
			}
		}
		for _, t := range group {
			names[t.Name] = true
		}
	}
	if err := envconfig.OneOf("BROKER_DRIVER", s.BrokerDriver, "memory", "redis"); err != nil {
		errs = append(errs, err)
	}
	if s.Subscriptions && s.BrokerDriver == "redis" && s.RedisURL == "" {
		errs = append(errs, errors.New("REDIS_URL: bắt buộc khi BROKER_DRIVER=redis"))
	}
	if s.WebhookAllowPrivate && isProd(s.Environment) {
		errs = append(errs, errors.New("FHIR_WEBHOOK_ALLOW_PRIVATE: không được bật ở staging/production (tắt chống SSRF)"))
	}
	errs = append(errs, s.validateWS()...)
	if s.JobLease <= 0 || s.JobMaintenanceInterval < 0 {
		errs = append(errs, errors.New("FHIR_JOB_LEASE phải > 0 và FHIR_JOB_MAINTENANCE_INTERVAL phải >= 0"))
	}
	if s.JobConcurrency <= 0 || s.WebhookWorkers <= 0 || s.WebhookMaxAttempts <= 0 || s.WebhookTimeout <= 0 {
		errs = append(errs, errors.New("FHIR_JOB_CONCURRENCY, FHIR_WEBHOOK_WORKERS, FHIR_WEBHOOK_MAX_ATTEMPTS, FHIR_WEBHOOK_TIMEOUT: phải > 0"))
	}
	if u, err := url.Parse(s.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("FHIR_BASE_URL: phải là URL http(s) đầy đủ, ví dụ https://fhir.example.org/fhir/R4"))
	}
	if s.JWTEnabled() {
		switch {
		case s.JWTAudience == "":
			errs = append(errs, errors.New("FHIR_JWT_AUDIENCE: bắt buộc khi đặt FHIR_JWT_ISSUER"))
		case (s.JWKSURL == "") == (s.JWTHMACSecret == ""):
			errs = append(errs, errors.New("FHIR_JWKS_URL hoặc FHIR_JWT_HMAC_SECRET: cần đúng một trong hai khi bật JWT"))
		case s.JWTHMACSecret != "" && len(s.JWTHMACSecret) < 32:
			errs = append(errs, errors.New("FHIR_JWT_HMAC_SECRET: tối thiểu 32 ký tự"))
		}
		if s.JobSigningKey == "" && s.JWKSURL != "" {
			errs = append(errs, errors.New("FHIR_JOB_SIGNING_KEY: bắt buộc khi dùng JWKS (ký quyền của job nền, mọi replica dùng chung)"))
		}
		if s.JobSigningKey != "" && len(s.JobSigningKey) < 32 {
			errs = append(errs, errors.New("FHIR_JOB_SIGNING_KEY: tối thiểu 32 ký tự"))
		}
		if s.JWTProjectClaim == "" || s.JWTScopeClaim == "" || s.JWTPolicyClaim == "" {
			errs = append(errs, errors.New("FHIR_JWT_PROJECT_CLAIM, FHIR_JWT_SCOPE_CLAIM, FHIR_JWT_POLICY_CLAIM: không được rỗng"))
		}
		if len(read)+len(write)+len(admin) > 0 || s.AuthDisabled {
			errs = append(errs, errors.New("FHIR_JWT_ISSUER: không dùng cùng token tĩnh (FHIR_*_TOKENS) hoặc FHIR_AUTH_DISABLED"))
		}
		if isProd(s.Environment) {
			if s.JWTHMACSecret != "" {
				errs = append(errs, errors.New("FHIR_JWT_HMAC_SECRET: không dùng ở staging/production, dùng FHIR_JWKS_URL"))
			}
			if s.JWKSURL != "" && !strings.HasPrefix(s.JWKSURL, "https://") {
				errs = append(errs, errors.New("FHIR_JWKS_URL: phải là https ở staging/production"))
			}
		}
	} else if s.JWTSuperAdmin {
		errs = append(errs, errors.New("FHIR_JWT_SUPER_ADMIN: cần bật JWT (FHIR_JWT_ISSUER)"))
	} else if s.JWKSURL != "" || s.JWTHMACSecret != "" || s.JWTAudience != "" {
		errs = append(errs, errors.New("FHIR_JWT_AUDIENCE, FHIR_JWKS_URL, FHIR_JWT_HMAC_SECRET: cần FHIR_JWT_ISSUER để bật JWT"))
	}
	if s.AuthDisabled && isProd(s.Environment) {
		errs = append(errs, errors.New("FHIR_AUTH_DISABLED: không được tắt xác thực ở staging/production"))
	}
	if s.SkipValidation && isProd(s.Environment) {
		errs = append(errs, errors.New("FHIR_SKIP_VALIDATION: không được tắt kiểm tra dữ liệu ở staging/production"))
	}
	if isProd(s.Environment) && strings.HasPrefix(s.DatabaseURL, "sqlite:") {
		errs = append(errs, errors.New("DATABASE_URL: không dùng SQLite ở staging/production"))
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

// PreviousWSKeys tách FHIR_WS_SIGNING_KEY_PREVIOUS thành danh sách khóa (bỏ phần tử rỗng).
func (s Settings) PreviousWSKeys() []string {
	var out []string
	for _, k := range strings.Split(s.WSSigningKeyPrevious, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Các chế độ websocket.
const (
	WSModeOff      = "off"
	WSModeEmbedded = "embedded"
	WSModeIssuer   = "issuer"
)

// WSModeResolved trả chế độ websocket đang hiệu lực: FHIR_WS_MODE nếu có, không thì suy từ bí danh cũ FHIR_WEBSOCKET.
func (s Settings) WSModeResolved() string {
	switch {
	case s.WSMode != "":
		return s.WSMode
	case s.WebSocket:
		return WSModeEmbedded
	}
	return WSModeOff
}

// gatewayOnlyWSKeys là cấu hình của phía GIỮ kết nối: ở chế độ issuer chúng thuộc ws-gateway, đặt ở fhir-server là nhầm chỗ
// (người vận hành tưởng giới hạn có hiệu lực mà thực ra không).
var gatewayOnlyWSKeys = []string{"FHIR_WS_MAX_CONNECTIONS", "FHIR_WS_MAX_BINDINGS_PER_USER", "FHIR_WS_ALLOWED_ORIGINS", "FHIR_WS_HEARTBEAT"}

func (s Settings) validateWSEnv(get func(string) string) error {
	if s.WSModeResolved() != WSModeIssuer {
		return nil
	}
	var errs []error
	for _, k := range gatewayOnlyWSKeys {
		if get(k) != "" {
			errs = append(errs, fmt.Errorf("%s: thuộc ws-gateway, không có tác dụng ở FHIR_WS_MODE=issuer (đặt ở ws-gateway)", k))
		}
	}
	return errors.Join(errs...)
}

func (s Settings) validateWS() []error {
	var errs []error
	switch s.WSMode {
	case "", WSModeOff, WSModeEmbedded, WSModeIssuer:
	default:
		errs = append(errs, fmt.Errorf("FHIR_WS_MODE: %q không hợp lệ (off | embedded | issuer)", s.WSMode))
		return errs
	}
	if s.WebSocket && s.WSMode != "" && s.WSMode != WSModeEmbedded {
		errs = append(errs, fmt.Errorf("FHIR_WEBSOCKET=true (bí danh của FHIR_WS_MODE=embedded) mâu thuẫn với FHIR_WS_MODE=%s", s.WSMode))
	}
	mode := s.WSModeResolved()
	if mode == WSModeOff {
		if s.WSSigningKey != "" || s.WSSigningKeyPrevious != "" || s.WSURL != "" || s.WSAllowedOrigins != "" {
			errs = append(errs, errors.New("FHIR_WS_*: cần FHIR_WEBSOCKET=true (hoặc FHIR_WS_MODE=embedded|issuer)"))
		}
		return errs
	}
	if !s.Subscriptions {
		errs = append(errs, fmt.Errorf("%s: cần bật FHIR_SUBSCRIPTIONS", s.wsModeName()))
	}
	switch {
	case mode == WSModeIssuer && s.WSSigningKey == "":
		errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY: bắt buộc ở FHIR_WS_MODE=issuer (phải giống khóa của ws-gateway)"))
	case s.WSSigningKey == "" && (isProd(s.Environment) || s.BrokerDriver == "redis"):
		errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY: bắt buộc ở staging/production hoặc khi BROKER_DRIVER=redis (mọi instance phải dùng chung khóa)"))
	case s.WSSigningKey != "" && len(s.WSSigningKey) < 32:
		errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY: tối thiểu 32 ký tự"))
	}
	if s.WSSigningKeyPrevious != "" {
		prev := s.PreviousWSKeys()
		switch {
		case s.WSSigningKey == "":
			errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY_PREVIOUS: cần đặt FHIR_WS_SIGNING_KEY (khóa hiện hành)"))
		case len(prev) > 3:
			errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY_PREVIOUS: tối đa 3 khóa"))
		}
		for _, k := range prev {
			if len(k) < 32 {
				errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY_PREVIOUS: mỗi khóa tối thiểu 32 ký tự"))
				break
			}
		}
	}
	if mode == WSModeIssuer {
		if s.BrokerDriver != "redis" {
			errs = append(errs, errors.New("FHIR_WS_MODE=issuer: cần BROKER_DRIVER=redis (ws-gateway ở tiến trình khác, bộ nhớ trong process không đủ)"))
		}
		u, err := url.Parse(s.WSURL)
		switch {
		case s.WSURL == "":
			errs = append(errs, errors.New("FHIR_WS_URL: bắt buộc ở FHIR_WS_MODE=issuer (địa chỉ websocket công khai của ws-gateway, ví dụ wss://ws.example.org/ws/subscriptions-r4)"))
		case err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "":
			errs = append(errs, errors.New("FHIR_WS_URL: phải là URL ws:// hoặc wss:// đầy đủ"))
		case isProd(s.Environment) && u.Scheme != "wss":
			errs = append(errs, errors.New("FHIR_WS_URL: chỉ nhận wss:// ở staging/production"))
		}
		return errs
	}
	if s.WSMaxConnections <= 0 || s.WSHeartbeat <= 0 || s.WSMaxBindingsPerUser <= 0 {
		errs = append(errs, errors.New("FHIR_WS_MAX_CONNECTIONS, FHIR_WS_MAX_BINDINGS_PER_USER và FHIR_WS_HEARTBEAT phải > 0"))
	}
	return errs
}

// wsModeName là tên biến dùng trong thông báo lỗi (giữ tên cũ cho bí danh để thông báo quen thuộc).
func (s Settings) wsModeName() string {
	if s.WSMode == "" {
		return "FHIR_WEBSOCKET"
	}
	return "FHIR_WS_MODE=" + s.WSMode
}

// validateStorage kiểm cấu hình kho Binary (chỉ khi bật).
func (s Settings) validateStorage() []error {
	if !s.StorageEnabled() {
		return nil
	}
	var errs []error
	switch s.StorageKind() {
	case "file":
		if strings.TrimSpace(s.StorageDir()) == "" {
			errs = append(errs, errors.New("FHIR_BINARY_STORAGE: file:<thư mục> thiếu thư mục"))
		}
	case "s3":
		errs = append(errs, s.validateS3()...)
	default:
		errs = append(errs, errors.New("FHIR_BINARY_STORAGE: chỉ hỗ trợ file:<thư mục> hoặc s3:<bucket>[/<prefix>] (azure:, gs: chưa có)"))
	}
	if u, err := url.Parse(s.StoragePublicURL()); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		errs = append(errs, errors.New("FHIR_STORAGE_BASE_URL: phải là URL http(s) tuyệt đối, không query, fragment, userinfo"))
	}
	if s.StorageURLTTL <= 0 || s.StorageURLTTL > 24*time.Hour {
		errs = append(errs, errors.New("FHIR_STORAGE_URL_TTL: phải trong (0, 24h]"))
	}
	if s.StorageMaxUploadBytes <= 0 {
		errs = append(errs, errors.New("FHIR_STORAGE_MAX_UPLOAD_BYTES: phải dương"))
	}
	if s.Base64BinaryMaxBytes == 0 {
		errs = append(errs, errors.New("FHIR_BASE64_BINARY_MAX_BYTES: phải dương (hoặc âm để không giới hạn)"))
	}
	if s.IsProd() {
		if s.StorageSigningKeyPath == "" {
			errs = append(errs, errors.New("FHIR_STORAGE_SIGNING_KEY_PATH: staging/production bắt buộc có khóa ký cố định (khóa tạm làm URL mất hiệu lực khi khởi động lại và không chạy được nhiều replica)"))
		}
		if u, err := url.Parse(s.StoragePublicURL()); err == nil && u.Scheme != "https" {
			errs = append(errs, errors.New("FHIR_STORAGE_BASE_URL: staging/production phải dùng https"))
		}
	}
	if s.StorageSigningKeyPath == "" && s.StorageSigningPreviousKeys != "" {
		errs = append(errs, errors.New("FHIR_STORAGE_SIGNING_PREVIOUS_KEYS: cần đặt cả khóa hiện tại (FHIR_STORAGE_SIGNING_KEY_PATH)"))
	}
	if len(s.StoragePreviousKeyPaths()) > 3 {
		errs = append(errs, errors.New("FHIR_STORAGE_SIGNING_PREVIOUS_KEYS: tối đa 3 khóa cũ"))
	}
	return errs
}

// validateS3 kiểm phần S3 (kiểm sâu hơn nằm ở s3store.New lúc khởi động).
func (s Settings) validateS3() []error {
	var errs []error
	if s.S3Bucket() == "" {
		errs = append(errs, errors.New("FHIR_BINARY_STORAGE: s3:<bucket>[/<prefix>] thiếu bucket"))
	}
	if s.S3Region == "" {
		errs = append(errs, errors.New("FHIR_S3_REGION: bắt buộc khi dùng s3:"))
	}
	if s.S3AccessKeyID == "" || s.S3SecretAccessKey == "" {
		errs = append(errs, errors.New("FHIR_S3_ACCESS_KEY_ID và FHIR_S3_SECRET_ACCESS_KEY: bắt buộc khi dùng s3: (chưa hỗ trợ khóa tạm từ IMDS/STS)"))
	}
	switch s.S3SSE {
	case "", "AES256", "aws:kms":
	default:
		errs = append(errs, errors.New(`FHIR_S3_SSE: chỉ nhận "", "AES256" hoặc "aws:kms"`))
	}
	if s.S3Endpoint != "" {
		u, err := url.Parse(s.S3Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
			errs = append(errs, errors.New("FHIR_S3_ENDPOINT: phải là http(s)://host[:port], không đường dẫn, query, fragment, userinfo"))
		} else if s.IsProd() && u.Scheme != "https" {
			errs = append(errs, errors.New("FHIR_S3_ENDPOINT: staging/production phải dùng https (khóa truy cập được ký vào mọi yêu cầu)"))
		}
	}
	return errs
}
