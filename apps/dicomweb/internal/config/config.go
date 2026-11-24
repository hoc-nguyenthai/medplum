// Package config đọc cấu hình dicomweb từ biến môi trường (và file .env nếu có).
//
// Cấu hình dùng chung với fhir-server (cơ sở dữ liệu, JWT, kho Binary) giữ NGUYÊN tên biến của fhir-server (`DATABASE_*`, `FHIR_JWT_*`,
// `FHIR_BINARY_STORAGE`, `FHIR_S3_*`) để hai service dùng chung một tệp môi trường; cấu hình riêng của dicomweb có tiền tố `DICOMWEB_`.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"trivita-fhir/pkg/envconfig"
	"trivita-fhir/pkg/httpsec"
)

// DevAllowedOrigin là origin của ứng dụng web chạy ở máy dev, được cho phép mặc định khi ENVIRONMENT=local.
const DevAllowedOrigin = "http://localhost:3000"

// DefaultProdRateLimit là hạn mức mỗi IP (yêu cầu/giây) khi staging/production không đặt DICOMWEB_RATE_LIMIT.
const DefaultProdRateLimit = 20

// Settings là toàn bộ cấu hình. Mặc định an toàn cho local; ở staging/production các ràng buộc chặt hơn (xem validate).
// Biến của trivita-observability-go (OTEL_*, LOG_LEVEL, LOG_JSON) do thư viện đó tự đọc.
type Settings struct {
	AppName     string // tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt)
	Addr        string // địa chỉ nghe, ví dụ :8008
	Debug       bool   // bật chế độ gỡ lỗi của Echo (không dùng ở production)
	Environment string // ENVIRONMENT: local | development | staging | production

	ReadinessTimeout   time.Duration // hạn tối đa của /health/ready khi kiểm database
	ShutdownDrainDelay time.Duration // sau SIGTERM: readiness 503 nhưng vẫn phục vụ, để orchestrator gỡ instance khỏi tải
	ShutdownTimeout    time.Duration // thời gian tối đa chờ yêu cầu đang chạy xong sau khi hết drain; terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị
	ReadHeaderTimeout  time.Duration // hạn đọc phần đầu HTTP của mỗi yêu cầu (chống kết nối treo)

	// DatabaseURL phải trỏ tới CÙNG schema `fhir` với fhir-server: DicomStudy, DicomSeries, DicomInstance là resource FHIR, tìm được qua API của
	// fhir-server. dicomweb KHÔNG chạy migration (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động.
	DatabaseURL string
	// DatabaseEcho ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số).
	DatabaseEcho bool
	// DatabaseRequireSchema (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md).
	DatabaseRequireSchema bool
	DatabaseMaxOpenConns  int           // số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL
	DatabaseMaxIdleConns  int           // số kết nối nhàn rỗi giữ lại
	DatabaseConnLifetime  time.Duration // tuổi tối đa của một kết nối trước khi được thay

	// BaseURL là địa chỉ công khai của dicomweb (không gồm tiền tố /dicomweb), dùng cho RetrieveURL trong phản hồi STOW,
	// ví dụ https://dicom.example.org.
	BaseURL string

	// JWT (bắt buộc): access token do auth cấp, kiểm bằng JWKS (production) hoặc HS256 dùng chung (chỉ dev/test). Dữ liệu tách theo project lấy từ claim project.
	JWTIssuer       string
	JWTAudience     string // audience mà token phải có (bắt buộc)
	JWKSURL         string // JWKS của auth (production: https), được cache; đã tải khóa thì vẫn xác minh khi auth tạm tắt
	JWTHMACSecret   string // CHỈ cho dev/test: khóa HS256 dùng chung thay cho JWKS (tối thiểu 32 ký tự)
	JWTProjectClaim string // tên claim chứa project của token
	JWTScopeClaim   string // tên claim chứa scope
	JWTPolicyClaim  string // tên claim chứa chính sách truy cập (AccessPolicy) đã dịch
	JWTSuperAdmin   bool   // cho phép token super-admin hành động trong project chọn bằng header X-Fhir-Project

	// BinaryStorage là kho nội dung DICOM (bắt buộc), PHẢI trùng với FHIR_BINARY_STORAGE của fhir-server vì Binary do dicomweb ghi được fhir-server đọc:
	// `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`.
	BinaryStorage string
	// S3 (BinaryStorage dạng `s3:`). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log.
	S3Region          string // bắt buộc (vd eu-west-1)
	S3Endpoint        string // kho tương thích S3 (MinIO...), http(s)://host[:port]; trống: AWS
	S3AccessKeyID     string // bắt buộc khi dùng s3:
	S3SecretAccessKey string // bắt buộc khi dùng s3:; không có trong log
	S3SessionToken    string // tùy chọn, cho khóa tạm
	S3SSE             string // "", "AES256" hoặc "aws:kms"

	// MaxInstanceBytes là trần kích thước một phần (một instance) của STOW: vượt thì instance đó bị từ chối riêng (FailureReason 0xA700) và không để lại gì;
	// MaxRequestBytes là trần cả yêu cầu (nhiều phần): vượt thì 413 (các instance đã xong trước đó vẫn được giữ).
	MaxInstanceBytes int
	MaxRequestBytes  int // trần cả yêu cầu STOW (nhiều phần); xem MaxInstanceBytes
	// MaxHeaderBytes là trần phần đầu (mọi phần tử trước PixelData) được nạp vào bộ nhớ để đọc metadata; vượt thì instance bị từ chối (0xA700). Cùng với giới hạn độ sâu SQ
	// (16 lớp, cố định) chống tệp độc (docs/plans/cds-dicom/d0-spike.md 3.1).
	MaxHeaderBytes int

	// AllowedOrigins là origin được gọi API từ trình duyệt (CORS); rỗng: không origin nào. Mặc định http://localhost:3000 ở local.
	AllowedOrigins string
	// TrustedProxies là CIDR của proxy được tin header X-Forwarded-For (cách nhau bằng dấu phẩy); rỗng: không tin header nào.
	TrustedProxies string
	// RateLimit là số yêu cầu mỗi giây mỗi IP (0: tắt). Không đặt: tắt ở local, 20 ở staging/production. Đếm theo từng instance.
	RateLimit float64
	// RateBurst là cỡ đợt của hạn mức (<=0: gấp đôi RateLimit, tối thiểu 1).
	RateBurst int
}

// Default trả về cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-dicomweb", Addr: ":8008", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		DatabaseURL:       "sqlite://./fhir.db", DatabaseMaxOpenConns: 20, DatabaseMaxIdleConns: 5, DatabaseConnLifetime: 30 * time.Minute,
		BaseURL:         "http://localhost:8008",
		JWTProjectClaim: "fhir_project", JWTScopeClaim: "scope", JWTPolicyClaim: "fhir_access_policy",
		MaxInstanceBytes: 1 << 30, MaxRequestBytes: 4 << 30, MaxHeaderBytes: 1 << 20,
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
	r.Bool(&s.DatabaseRequireSchema, "DATABASE_REQUIRE_SCHEMA")
	r.Int(&s.DatabaseMaxOpenConns, "DATABASE_MAX_OPEN_CONNS")
	r.Int(&s.DatabaseMaxIdleConns, "DATABASE_MAX_IDLE_CONNS")
	r.Duration(&s.DatabaseConnLifetime, "DATABASE_CONN_MAX_LIFETIME")
	r.String(&s.BaseURL, "DICOMWEB_BASE_URL")
	r.String(&s.JWTIssuer, "FHIR_JWT_ISSUER")
	r.String(&s.JWTAudience, "FHIR_JWT_AUDIENCE")
	r.String(&s.JWKSURL, "FHIR_JWKS_URL")
	r.String(&s.JWTHMACSecret, "FHIR_JWT_HMAC_SECRET")
	r.String(&s.JWTProjectClaim, "FHIR_JWT_PROJECT_CLAIM")
	r.String(&s.JWTScopeClaim, "FHIR_JWT_SCOPE_CLAIM")
	r.String(&s.JWTPolicyClaim, "FHIR_JWT_POLICY_CLAIM")
	r.Bool(&s.JWTSuperAdmin, "FHIR_JWT_SUPER_ADMIN")
	r.String(&s.BinaryStorage, "FHIR_BINARY_STORAGE")
	r.String(&s.S3Region, "FHIR_S3_REGION")
	r.String(&s.S3Endpoint, "FHIR_S3_ENDPOINT")
	r.String(&s.S3AccessKeyID, "FHIR_S3_ACCESS_KEY_ID")
	r.String(&s.S3SecretAccessKey, "FHIR_S3_SECRET_ACCESS_KEY")
	r.String(&s.S3SessionToken, "FHIR_S3_SESSION_TOKEN")
	r.String(&s.S3SSE, "FHIR_S3_SSE")
	r.Int(&s.MaxInstanceBytes, "DICOMWEB_MAX_INSTANCE_BYTES")
	r.Int(&s.MaxRequestBytes, "DICOMWEB_MAX_REQUEST_BYTES")
	r.Int(&s.MaxHeaderBytes, "DICOMWEB_MAX_HEADER_BYTES")
	r.String(&s.AllowedOrigins, "DICOMWEB_ALLOWED_ORIGINS")
	if s.AllowedOrigins == "" && s.Environment == "local" {
		s.AllowedOrigins = DevAllowedOrigin
	}
	r.String(&s.TrustedProxies, "DICOMWEB_TRUSTED_PROXIES")
	r.Float(&s.RateLimit, "DICOMWEB_RATE_LIMIT")
	if get("DICOMWEB_RATE_LIMIT") == "" && isProd(s.Environment) {
		s.RateLimit = DefaultProdRateLimit // không đặt thì bật ở staging/production; đặt rõ 0 mới tắt
	}
	r.Int(&s.RateBurst, "DICOMWEB_RATE_BURST")
	if err := r.Err(); err != nil {
		return Settings{}, err
	}
	if err := s.validate(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

func isProd(env string) bool { return env == "production" || env == "staging" }

// IsProd cho biết môi trường staging hoặc production (áp các ràng buộc chặt).
func (s Settings) IsProd() bool { return isProd(s.Environment) }

// StorageKind trả "file", "s3" hoặc "" (không nhận ra).
func (s Settings) StorageKind() string {
	switch {
	case strings.HasPrefix(s.BinaryStorage, "file:"):
		return "file"
	case strings.HasPrefix(s.BinaryStorage, "s3:"):
		return "s3"
	}
	return ""
}

// S3Bucket tách bucket từ `s3:<bucket>[/<prefix>]`.
func (s Settings) S3Bucket() string {
	b, _, _ := strings.Cut(strings.TrimPrefix(s.BinaryStorage, "s3:"), "/")
	return b
}

// S3Prefix tách prefix, dạng "a/b/" (hoặc rỗng).
func (s Settings) S3Prefix() string {
	_, p, _ := strings.Cut(strings.TrimPrefix(s.BinaryStorage, "s3:"), "/")
	if p = strings.Trim(p, "/"); p == "" {
		return ""
	}
	return p + "/"
}

// StorageDir là thư mục của BinaryStorage dạng `file:<dir>`.
func (s Settings) StorageDir() string { return strings.TrimPrefix(s.BinaryStorage, "file:") }

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

// AllowedOriginList phân tích AllowedOrigins thành danh sách origin đã chuẩn hóa. Ở staging/production cấm "*" và chỉ nhận https.
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
	if s.DatabaseRequireSchema && !strings.HasPrefix(s.DatabaseURL, "postgres:") && !strings.HasPrefix(s.DatabaseURL, "postgresql:") {
		errs = append(errs, errors.New("DATABASE_REQUIRE_SCHEMA: chỉ áp dụng cho PostgreSQL"))
	}
	if isProd(s.Environment) && strings.HasPrefix(s.DatabaseURL, "sqlite:") {
		errs = append(errs, errors.New("DATABASE_URL: không dùng SQLite ở staging/production"))
	}
	if s.DatabaseMaxOpenConns <= 0 || s.DatabaseMaxIdleConns < 0 {
		errs = append(errs, errors.New("DATABASE_MAX_OPEN_CONNS phải > 0 và DATABASE_MAX_IDLE_CONNS >= 0"))
	}
	if u, err := url.Parse(s.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		errs = append(errs, errors.New("DICOMWEB_BASE_URL: phải là URL http(s) tuyệt đối, không query, fragment, userinfo (ví dụ https://dicom.example.org)"))
	} else if isProd(s.Environment) && u.Scheme != "https" {
		errs = append(errs, errors.New("DICOMWEB_BASE_URL: staging/production phải dùng https"))
	}
	errs = append(errs, s.validateJWT()...)
	errs = append(errs, s.validateStorage()...)
	if s.MaxInstanceBytes <= 0 || s.MaxRequestBytes <= 0 || s.MaxHeaderBytes <= 0 {
		errs = append(errs, errors.New("DICOMWEB_MAX_INSTANCE_BYTES, DICOMWEB_MAX_REQUEST_BYTES, DICOMWEB_MAX_HEADER_BYTES: phải dương"))
	} else if s.MaxInstanceBytes > s.MaxRequestBytes {
		errs = append(errs, errors.New("DICOMWEB_MAX_INSTANCE_BYTES: không được lớn hơn DICOMWEB_MAX_REQUEST_BYTES"))
	}
	if _, err := s.AllowedOriginList(); err != nil {
		errs = append(errs, fmt.Errorf("DICOMWEB_ALLOWED_ORIGINS: %w", err))
	}
	if _, err := s.TrustedProxyNets(); err != nil {
		errs = append(errs, fmt.Errorf("DICOMWEB_TRUSTED_PROXIES: %w", err))
	}
	if s.RateLimit < 0 || s.RateBurst < 0 {
		errs = append(errs, errors.New("DICOMWEB_RATE_LIMIT và DICOMWEB_RATE_BURST: không được âm"))
	}
	if s.ReadinessTimeout <= 0 {
		errs = append(errs, errors.New("READINESS_TIMEOUT: phải > 0"))
	}
	if s.ShutdownTimeout <= 0 || s.ShutdownDrainDelay < 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT phải > 0 và SHUTDOWN_DRAIN_DELAY phải >= 0"))
	}
	return errors.Join(errs...)
}

func (s Settings) validateJWT() []error {
	var errs []error
	switch {
	case s.JWTIssuer == "":
		errs = append(errs, errors.New("FHIR_JWT_ISSUER: bắt buộc (dicomweb chỉ nhận JWT do auth cấp, không có token tĩnh)"))
	case s.JWTAudience == "":
		errs = append(errs, errors.New("FHIR_JWT_AUDIENCE: bắt buộc"))
	case (s.JWKSURL == "") == (s.JWTHMACSecret == ""):
		errs = append(errs, errors.New("FHIR_JWKS_URL hoặc FHIR_JWT_HMAC_SECRET: cần đúng một trong hai"))
	case s.JWTHMACSecret != "" && len(s.JWTHMACSecret) < 32:
		errs = append(errs, errors.New("FHIR_JWT_HMAC_SECRET: tối thiểu 32 ký tự"))
	}
	if s.JWTProjectClaim == "" || s.JWTScopeClaim == "" || s.JWTPolicyClaim == "" {
		errs = append(errs, errors.New("FHIR_JWT_PROJECT_CLAIM, FHIR_JWT_SCOPE_CLAIM, FHIR_JWT_POLICY_CLAIM: không được rỗng"))
	}
	if isProd(s.Environment) {
		if s.JWTHMACSecret != "" {
			errs = append(errs, errors.New("FHIR_JWT_HMAC_SECRET: không dùng ở staging/production, dùng FHIR_JWKS_URL"))
		}
		if s.JWKSURL != "" && !strings.HasPrefix(s.JWKSURL, "https://") {
			errs = append(errs, errors.New("FHIR_JWKS_URL: phải là https ở staging/production"))
		}
	}
	return errs
}

func (s Settings) validateStorage() []error {
	var errs []error
	switch s.StorageKind() {
	case "file":
		if strings.TrimSpace(s.StorageDir()) == "" {
			errs = append(errs, errors.New("FHIR_BINARY_STORAGE: file:<thư mục> thiếu thư mục"))
		}
	case "s3":
		errs = append(errs, s.validateS3()...)
	case "":
		if s.BinaryStorage == "" {
			errs = append(errs, errors.New("FHIR_BINARY_STORAGE: bắt buộc (file:<thư mục> hoặc s3:<bucket>[/<prefix>], trùng với fhir-server)"))
		} else {
			errs = append(errs, errors.New("FHIR_BINARY_STORAGE: chỉ hỗ trợ file:<thư mục> hoặc s3:<bucket>[/<prefix>]"))
		}
	}
	return errs
}

// validateS3 kiểm phần S3, cùng luật với fhir-server (kiểm sâu hơn nằm ở s3store.New lúc khởi động).
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
