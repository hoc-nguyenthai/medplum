// Package config đọc cấu hình fhir-downloader từ biến môi trường (và file .env nếu có).
//
// Cấu hình dùng chung với fhir-server (cơ sở dữ liệu, kho Binary, Redis, địa chỉ API, các giới hạn tải) giữ NGUYÊN tên biến của fhir-server
// (`DATABASE_*`, `REDIS_URL`, `FHIR_BINARY_STORAGE`, `FHIR_S3_*`, `FHIR_BASE_URL`, `FHIR_AUTO_DOWNLOAD_*`) để hai service dùng chung một tệp môi trường.
// fhir-downloader không có cờ `FHIR_AUTO_DOWNLOAD`: tiến trình này chạy là đã bật; đặt cờ đó ở fhir-server (cùng `FHIR_AUTO_DOWNLOAD_WORKER=false`) để
// API chỉ phát sự kiện.
package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"trivita-fhir/pkg/envconfig"
)

// Settings là toàn bộ cấu hình. Mặc định an toàn cho local; ở staging/production các ràng buộc chặt hơn (xem validate).
// Biến của trivita-observability-go (OTEL_*, LOG_LEVEL, LOG_JSON) do thư viện đó tự đọc.
type Settings struct {
	AppName     string // tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt)
	Addr        string // địa chỉ nghe của các probe, ví dụ :8009 (service không có route nghiệp vụ)
	Environment string // ENVIRONMENT: local | development | staging | production

	ReadinessTimeout   time.Duration // hạn tối đa của /health/ready khi kiểm database và Redis
	ShutdownDrainDelay time.Duration // sau SIGTERM: readiness 503 nhưng vẫn chạy, để orchestrator gỡ instance khỏi danh sách
	ShutdownTimeout    time.Duration // thời gian tối đa chờ việc tải đang chạy xong sau khi hết drain; terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị
	ReadHeaderTimeout  time.Duration // hạn đọc phần đầu HTTP của mỗi yêu cầu probe (chống kết nối treo)

	// DatabaseURL phải trỏ tới CÙNG schema `fhir` với fhir-server (đọc resource nguồn, ghi Binary, cập nhật attachment). fhir-downloader KHÔNG chạy migration
	// (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động.
	DatabaseURL string
	// DatabaseEcho ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số).
	DatabaseEcho bool
	// DatabaseRequireSchema (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md).
	DatabaseRequireSchema bool
	DatabaseMaxOpenConns  int           // số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL
	DatabaseMaxIdleConns  int           // số kết nối nhàn rỗi giữ lại
	DatabaseConnLifetime  time.Duration // tuổi tối đa của một kết nối trước khi được thay

	// RedisURL (bắt buộc): broker Redis Streams mà fhir-server phát sự kiện ghi resource lên (BROKER_DRIVER=redis, FHIR_SUBSCRIPTIONS hoặc FHIR_AUTO_DOWNLOAD bật ở API).
	// Broker bộ nhớ không đến được tiến trình khác nên service này không có lựa chọn đó.
	RedisURL string
	// BrokerGroup là consumer group của service này trên stream sự kiện. KHÁC group của fhir-server ("trivita") để cả hai cùng nhận MỌI sự kiện (cùng group thì chia nhau);
	// nhiều replica của fhir-downloader dùng cùng group để chia việc.
	BrokerGroup string

	// BaseURL là địa chỉ công khai của API FHIR (FHIR_BASE_URL của fhir-server): URL bắt đầu bằng nó không bị tải (đã là của chính hệ thống).
	BaseURL string
	// StorageBaseURL (tùy chọn) là địa chỉ công khai của /storage nếu khác mặc định (BaseURL bỏ /fhir/R4, thêm /storage); URL bắt đầu bằng nó cũng không bị tải.
	StorageBaseURL string

	// BinaryStorage là kho nội dung tệp tải về (bắt buộc), PHẢI trùng với FHIR_BINARY_STORAGE của fhir-server vì Binary do service này ghi được fhir-server đọc:
	// `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`.
	BinaryStorage string
	// S3 (BinaryStorage dạng `s3:`). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log.
	S3Region          string // bắt buộc (vd eu-west-1)
	S3Endpoint        string // kho tương thích S3 (MinIO, RustFS...), http(s)://host[:port]; trống: AWS
	S3AccessKeyID     string // bắt buộc khi dùng s3:
	S3SecretAccessKey string // bắt buộc khi dùng s3:; không có trong log
	S3SessionToken    string // tùy chọn, cho khóa tạm
	S3SSE             string // "", "AES256" hoặc "aws:kms"

	// StorageMaxUploadBytes là trần mặc định của một tệp tải về khi FHIR_AUTO_DOWNLOAD_MAX_BYTES là 0 (cùng biến với fhir-server).
	StorageMaxUploadBytes int
	// AutoDownloadMaxBytes là trần kích thước một tệp tải về; 0: dùng FHIR_STORAGE_MAX_UPLOAD_BYTES.
	AutoDownloadMaxBytes int
	// AutoDownloadTimeout là hạn của mỗi lần tải.
	AutoDownloadTimeout time.Duration
	// AutoDownloadAllowedPrefixes (cách nhau bằng dấu phẩy, không rỗng) chỉ cho tải URL bắt đầu bằng một tiền tố trong danh sách; rỗng: mọi URL ngoài.
	AutoDownloadAllowedPrefixes string
	// AutoDownloadIgnoredPrefixes (cách nhau bằng dấu phẩy) là các tiền tố URL KHÔNG bao giờ tải; thắng danh sách cho phép. Địa chỉ API và /storage luôn bị bỏ qua, không cần khai.
	AutoDownloadIgnoredPrefixes string
	// AutoDownloadAllowPrivate CHỈ cho dev: cho phép tải từ địa chỉ nội bộ (chống SSRF bị tắt). Cấm ở staging/production.
	AutoDownloadAllowPrivate bool
}

// Default trả về cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-fhir-downloader", Addr: ":8009", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		DatabaseURL:       "sqlite://./fhir.db", DatabaseMaxOpenConns: 20, DatabaseMaxIdleConns: 5, DatabaseConnLifetime: 30 * time.Minute,
		RedisURL: "redis://localhost:6379/0", BrokerGroup: "fhir-downloader",
		BaseURL:               "http://localhost:8005/fhir/R4",
		StorageMaxUploadBytes: 256 << 20, AutoDownloadTimeout: 30 * time.Second,
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
	r.String(&s.RedisURL, "REDIS_URL")
	r.String(&s.BrokerGroup, "BROKER_GROUP")
	r.String(&s.BaseURL, "FHIR_BASE_URL")
	r.String(&s.StorageBaseURL, "FHIR_STORAGE_BASE_URL")
	r.String(&s.BinaryStorage, "FHIR_BINARY_STORAGE")
	r.String(&s.S3Region, "FHIR_S3_REGION")
	r.String(&s.S3Endpoint, "FHIR_S3_ENDPOINT")
	r.String(&s.S3AccessKeyID, "FHIR_S3_ACCESS_KEY_ID")
	r.String(&s.S3SecretAccessKey, "FHIR_S3_SECRET_ACCESS_KEY")
	r.String(&s.S3SessionToken, "FHIR_S3_SESSION_TOKEN")
	r.String(&s.S3SSE, "FHIR_S3_SSE")
	r.Int(&s.StorageMaxUploadBytes, "FHIR_STORAGE_MAX_UPLOAD_BYTES")
	r.Int(&s.AutoDownloadMaxBytes, "FHIR_AUTO_DOWNLOAD_MAX_BYTES")
	r.Duration(&s.AutoDownloadTimeout, "FHIR_AUTO_DOWNLOAD_TIMEOUT")
	r.String(&s.AutoDownloadAllowedPrefixes, "FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES")
	r.String(&s.AutoDownloadIgnoredPrefixes, "FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES")
	r.Bool(&s.AutoDownloadAllowPrivate, "FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE")
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

// MaxBytes là trần một tệp tải về: FHIR_AUTO_DOWNLOAD_MAX_BYTES, hoặc FHIR_STORAGE_MAX_UPLOAD_BYTES khi là 0.
func (s Settings) MaxBytes() int64 {
	if s.AutoDownloadMaxBytes > 0 {
		return int64(s.AutoDownloadMaxBytes)
	}
	return int64(s.StorageMaxUploadBytes)
}

// OwnPrefixes là các gốc URL của chính hệ thống (API FHIR và /storage): không tải.
func (s Settings) OwnPrefixes() []string {
	return []string{strings.TrimRight(s.BaseURL, "/") + "/", s.StoragePublicURL()}
}

// StoragePublicURL là gốc URL công khai của /storage (giống fhir-server).
func (s Settings) StoragePublicURL() string {
	if s.StorageBaseURL != "" {
		return strings.TrimRight(s.StorageBaseURL, "/")
	}
	return strings.TrimSuffix(strings.TrimRight(s.BaseURL, "/"), "/fhir/R4") + "/storage"
}

// Prefixes tách danh sách tiền tố cách nhau bằng dấu phẩy (bỏ khoảng trắng và phần tử rỗng).
func Prefixes(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

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
	if s.RedisURL == "" {
		errs = append(errs, errors.New("REDIS_URL: bắt buộc (fhir-downloader nhận sự kiện từ broker Redis do fhir-server phát)"))
	} else if u, err := url.Parse(s.RedisURL); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
		errs = append(errs, errors.New("REDIS_URL: phải dạng redis://host:port/db hoặc rediss://..."))
	}
	if strings.TrimSpace(s.BrokerGroup) == "" || strings.ContainsAny(s.BrokerGroup, " \t\n") {
		errs = append(errs, errors.New("BROKER_GROUP: không được rỗng hoặc có khoảng trắng"))
	} else if s.BrokerGroup == "trivita" {
		errs = append(errs, errors.New(`BROKER_GROUP: không được là "trivita" (group của fhir-server; cùng group thì hai service chia nhau sự kiện thay vì cùng nhận)`))
	}
	if u, err := url.Parse(s.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		errs = append(errs, errors.New("FHIR_BASE_URL: phải là URL http(s) tuyệt đối, không query, fragment, userinfo (ví dụ https://fhir.example.org/fhir/R4)"))
	}
	if s.StorageBaseURL != "" {
		if u, err := url.Parse(s.StorageBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("FHIR_STORAGE_BASE_URL: phải là URL http(s) tuyệt đối"))
		}
	}
	errs = append(errs, s.validateStorage()...)
	if s.StorageMaxUploadBytes <= 0 || s.AutoDownloadMaxBytes < 0 {
		errs = append(errs, errors.New("FHIR_STORAGE_MAX_UPLOAD_BYTES phải > 0 và FHIR_AUTO_DOWNLOAD_MAX_BYTES >= 0"))
	}
	if s.AutoDownloadTimeout <= 0 {
		errs = append(errs, errors.New("FHIR_AUTO_DOWNLOAD_TIMEOUT: phải > 0"))
	}
	if s.AutoDownloadAllowPrivate && isProd(s.Environment) {
		errs = append(errs, errors.New("FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE: không được bật ở staging/production (tắt chống SSRF)"))
	}
	if s.ReadinessTimeout <= 0 {
		errs = append(errs, errors.New("READINESS_TIMEOUT: phải > 0"))
	}
	if s.ShutdownTimeout <= 0 || s.ShutdownDrainDelay < 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT phải > 0 và SHUTDOWN_DRAIN_DELAY phải >= 0"))
	}
	return errors.Join(errs...)
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
