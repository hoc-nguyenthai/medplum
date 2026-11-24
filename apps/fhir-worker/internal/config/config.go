// Package config đọc cấu hình fhir-worker từ biến môi trường (và file .env nếu có).
//
// Cấu hình dùng chung với fhir-server (cơ sở dữ liệu, kho Binary, khóa ký job, giới hạn job, địa chỉ API, tuỳ chọn kiểm dữ liệu của batch) giữ NGUYÊN tên biến của fhir-server
// (`DATABASE_*`, `FHIR_BINARY_STORAGE`, `FHIR_S3_*`, `FHIR_JOB_*`, `FHIR_EXPORT_RETENTION`, `FHIR_BASE_URL`, `FHIR_JWT_ISSUER`...) để hai service dùng chung một tệp môi trường.
// fhir-worker không có cờ bật: tiến trình này chạy là đã bật; đặt `FHIR_JOB_EXECUTION=false` ở fhir-server để API chỉ nhận yêu cầu và tạo job.
package config

import (
	"crypto/sha256"
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
	Addr        string // địa chỉ nghe của các probe, ví dụ :8011 (service không có route nghiệp vụ)
	Environment string // ENVIRONMENT: local | development | staging | production

	ReadinessTimeout   time.Duration // hạn tối đa của /health/ready khi kiểm database
	ShutdownDrainDelay time.Duration // sau SIGTERM: readiness 503 nhưng vẫn chạy, để orchestrator gỡ instance khỏi danh sách
	ShutdownTimeout    time.Duration // thời gian tối đa chờ job đang chạy dừng sau khi hết drain (job chưa xong nhả lease cho nơi khác); terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị
	ReadHeaderTimeout  time.Duration // hạn đọc phần đầu HTTP của mỗi yêu cầu probe (chống kết nối treo)

	// DatabaseURL phải trỏ tới CÙNG schema `fhir` với fhir-server (đọc và ghi AsyncJob, đọc và ghi dữ liệu theo quyền của người đã tạo job). fhir-worker KHÔNG chạy migration
	// (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động.
	DatabaseURL string
	// DatabaseEcho ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số).
	DatabaseEcho bool
	// DatabaseRequireSchema (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md).
	DatabaseRequireSchema bool
	DatabaseMaxOpenConns  int           // số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL
	DatabaseMaxIdleConns  int           // số kết nối nhàn rỗi giữ lại
	DatabaseConnLifetime  time.Duration // tuổi tối đa của một kết nối trước khi được thay

	// BaseURL là địa chỉ công khai của API FHIR (FHIR_BASE_URL của fhir-server): URL file kết quả export (`<base>/Binary/<id>`) và Location của batch async được dựng từ đây, nên
	// PHẢI trùng fhir-server.
	BaseURL string
	// StorageBaseURL (tùy chọn) là địa chỉ công khai của /storage nếu khác mặc định; dùng để nhận ra và viết lại URL kho trong dữ liệu batch (như fhir-server).
	StorageBaseURL string

	// BinaryStorage (tùy chọn) là kho nội dung Binary, PHẢI trùng FHIR_BINARY_STORAGE của fhir-server khi API dùng kho: file kết quả export và đầu vào, kết quả của batch async ghi ra
	// kho thay vì base64 trong CSDL. Rỗng: kết quả nằm base64 trong CSDL (như fhir-server không bật kho): `file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`.
	BinaryStorage string
	// S3 (BinaryStorage dạng `s3:`). Khóa truy cập chỉ đọc từ biến môi trường, không có trong log.
	S3Region          string // bắt buộc (vd eu-west-1)
	S3Endpoint        string // kho tương thích S3 (MinIO, RustFS...), http(s)://host[:port]; trống: AWS
	S3AccessKeyID     string // bắt buộc khi dùng s3:
	S3SecretAccessKey string // bắt buộc khi dùng s3:; không có trong log
	S3SessionToken    string // tùy chọn, cho khóa tạm
	S3SSE             string // "", "AES256" hoặc "aws:kms"

	// JWTIssuer chỉ để biết fhir-server có bật JWT không (fhir-worker không kiểm token): có thì job mang chữ ký quyền của người gọi (job-seal) và chỉ chạy khi chữ ký hợp lệ, bằng
	// đúng quyền và project của người đó; khi đó phải có khóa ký job (FHIR_JOB_SIGNING_KEY, hoặc dev HMAC). Rỗng: token tĩnh, job chạy không tách project như fhir-server ở chế độ đó.
	JWTIssuer string
	// JobSigningKey là khóa ký quyền của job (tối thiểu 32 ký tự), dùng chung với fhir-server; bắt buộc khi bật JWT trừ khi dùng dev HMAC.
	JobSigningKey string
	// JWTHMACSecret CHỈ cho dev: khi FHIR_JWT_HMAC_SECRET của fhir-server dùng HS256 thì khóa ký job được suy từ nó (như fhir-server); cấm ở staging/production.
	JWTHMACSecret string

	// Publish: có phát sự kiện ghi resource lên Redis không. Tự suy từ FHIR_SUBSCRIPTIONS hoặc FHIR_AUTO_DOWNLOAD (cùng điều kiện của fhir-server): batch async ghi resource nên các
	// service nghe (fhir-notifier, fhir-downloader) phải thấy các lần ghi đó. Cần BROKER_DRIVER=redis và REDIS_URL (broker bộ nhớ không đến được tiến trình khác).
	Subscriptions bool
	// AutoDownload: xem Subscriptions.
	AutoDownload bool
	// BrokerDriver: "redis" khi phải phát sự kiện (Subscriptions hoặc AutoDownload); "memory" nếu không (không có gì để phát).
	BrokerDriver string
	// RedisURL: Redis của broker sự kiện (chỉ dùng khi phải phát sự kiện).
	RedisURL string

	// JobConcurrency là số job chạy đồng thời trong một instance.
	JobConcurrency int
	// JobLease: thời gian một instance giữ quyền chạy job trước khi instance khác tiếp quản (tự gia hạn khi job còn chạy).
	JobLease time.Duration
	// JobCancelPoll: Chu kỳ replica đang chạy job kiểm tra job có bị hủy (từ replica khác) không; việc hủy có hiệu lực trong chừng này thay vì chờ JobLease/3. Mỗi job đang chạy tốn một lần đọc mỗi chu kỳ; không vượt JobLease/3. Phải > 0.
	JobCancelPoll time.Duration
	// JobMaintenanceInterval là chu kỳ nhận job mới (accepted), tiếp quản job mồ côi và xóa kết quả export hết hạn; fhir-worker nhận job qua vòng này nên mặc định ngắn (5 giây). Phải > 0.
	JobMaintenanceInterval time.Duration
	// JobRecoveryGrace là tuổi tối thiểu của job accepted mới được nhận (mặc định 2 giây): fhir-server không tự chạy job nên không cần chờ lâu như khi chạy cùng tiến trình.
	JobRecoveryGrace time.Duration
	// ExportRetention là thời gian giữ kết quả bulk export và batch async (âm: không bao giờ xóa); xóa bằng Expunge nên nội dung ở kho cũng bị xóa.
	ExportRetention time.Duration
	// BatchCheckpointEntries: số entry giữa hai lần lưu checkpoint của batch async (ngoài ra lưu ít nhất mỗi 5 giây).
	BatchCheckpointEntries int

	// SkipValidation, SkipConstraints, UpdateAsCreate và Base64BinaryMaxBytes PHẢI trùng fhir-server: batch async chạy từng entry qua cùng bộ xử lý ghi như API.
	SkipValidation       bool // bỏ kiểm tra bằng fhirvalidate khi ghi (chỉ dev/test)
	SkipConstraints      bool // chỉ kiểm cấu trúc, bỏ invariant FHIRPath
	UpdateAsCreate       bool // cho phép PUT Type/id tạo resource mới với id do client chọn
	Base64BinaryMaxBytes int  // trần `data` base64 (đã giải mã) khi batch ghi Binary (khi dùng kho); âm: không trần
}

// Default trả về cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-fhir-worker", Addr: ":8011", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		DatabaseURL:       "sqlite://./fhir.db", DatabaseMaxOpenConns: 20, DatabaseMaxIdleConns: 5, DatabaseConnLifetime: 30 * time.Minute,
		BaseURL:      "http://localhost:8005/fhir/R4",
		BrokerDriver: "memory", RedisURL: "redis://localhost:6379/0",
		JobConcurrency: 2, JobLease: 5 * time.Minute, JobCancelPoll: 2 * time.Second, JobMaintenanceInterval: 5 * time.Second, JobRecoveryGrace: 2 * time.Second,
		ExportRetention: 24 * time.Hour, BatchCheckpointEntries: 10, Base64BinaryMaxBytes: 1 << 20,
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
	r.String(&s.BaseURL, "FHIR_BASE_URL")
	r.String(&s.StorageBaseURL, "FHIR_STORAGE_BASE_URL")
	r.String(&s.BinaryStorage, "FHIR_BINARY_STORAGE")
	r.String(&s.S3Region, "FHIR_S3_REGION")
	r.String(&s.S3Endpoint, "FHIR_S3_ENDPOINT")
	r.String(&s.S3AccessKeyID, "FHIR_S3_ACCESS_KEY_ID")
	r.String(&s.S3SecretAccessKey, "FHIR_S3_SECRET_ACCESS_KEY")
	r.String(&s.S3SessionToken, "FHIR_S3_SESSION_TOKEN")
	r.String(&s.S3SSE, "FHIR_S3_SSE")
	r.String(&s.JWTIssuer, "FHIR_JWT_ISSUER")
	r.String(&s.JobSigningKey, "FHIR_JOB_SIGNING_KEY")
	r.String(&s.JWTHMACSecret, "FHIR_JWT_HMAC_SECRET")
	r.Bool(&s.Subscriptions, "FHIR_SUBSCRIPTIONS")
	r.Bool(&s.AutoDownload, "FHIR_AUTO_DOWNLOAD")
	r.String(&s.BrokerDriver, "BROKER_DRIVER")
	r.String(&s.RedisURL, "REDIS_URL")
	r.Int(&s.JobConcurrency, "FHIR_JOB_CONCURRENCY")
	r.Duration(&s.JobLease, "FHIR_JOB_LEASE")
	r.Duration(&s.JobCancelPoll, "FHIR_JOB_CANCEL_POLL")
	r.Duration(&s.JobMaintenanceInterval, "FHIR_JOB_MAINTENANCE_INTERVAL")
	r.Duration(&s.JobRecoveryGrace, "FHIR_JOB_RECOVERY_GRACE")
	r.Duration(&s.ExportRetention, "FHIR_EXPORT_RETENTION")
	r.Int(&s.BatchCheckpointEntries, "FHIR_BATCH_CHECKPOINT_ENTRIES")
	r.Bool(&s.SkipValidation, "FHIR_SKIP_VALIDATION")
	r.Bool(&s.SkipConstraints, "FHIR_SKIP_CONSTRAINTS")
	r.Bool(&s.UpdateAsCreate, "FHIR_UPDATE_AS_CREATE")
	r.Int(&s.Base64BinaryMaxBytes, "FHIR_BASE64_BINARY_MAX_BYTES")
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

// JWTEnabled cho biết fhir-server có bật JWT (job mang chữ ký quyền của người gọi).
func (s Settings) JWTEnabled() bool { return s.JWTIssuer != "" }

// Publishes cho biết có phát sự kiện ghi resource lên Redis không (cùng điều kiện với fhir-server: Subscriptions hoặc AutoDownload).
func (s Settings) Publishes() bool { return s.Subscriptions || s.AutoDownload }

// JobKey trả khóa ký job: FHIR_JOB_SIGNING_KEY, hoặc (dev HMAC) suy từ FHIR_JWT_HMAC_SECRET đúng như fhir-server. nil khi không bật JWT.
func (s Settings) JobKey() []byte {
	if !s.JWTEnabled() {
		return nil
	}
	if s.JobSigningKey != "" {
		return []byte(s.JobSigningKey)
	}
	sum := sha256.Sum256([]byte("fhir-job-seal|" + s.JWTHMACSecret))
	return sum[:]
}

// StorageEnabled cho biết có dùng kho Binary không.
func (s Settings) StorageEnabled() bool { return s.BinaryStorage != "" }

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

// StoragePublicURL là gốc URL công khai của /storage (giống fhir-server).
func (s Settings) StoragePublicURL() string {
	if s.StorageBaseURL != "" {
		return strings.TrimRight(s.StorageBaseURL, "/")
	}
	return strings.TrimSuffix(strings.TrimRight(s.BaseURL, "/"), "/fhir/R4") + "/storage"
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
		errs = append(errs, errors.New("FHIR_BASE_URL: phải là URL http(s) tuyệt đối, không query, fragment, userinfo (ví dụ https://fhir.example.org/fhir/R4; phải trùng fhir-server)"))
	}
	if s.StorageBaseURL != "" {
		if u, err := url.Parse(s.StorageBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("FHIR_STORAGE_BASE_URL: phải là URL http(s) tuyệt đối"))
		}
	}
	errs = append(errs, s.validateStorage()...)
	errs = append(errs, s.validateJobs()...)
	errs = append(errs, s.validateBroker()...)
	if s.ReadinessTimeout <= 0 {
		errs = append(errs, errors.New("READINESS_TIMEOUT: phải > 0"))
	}
	if s.ShutdownTimeout <= 0 || s.ShutdownDrainDelay < 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT phải > 0 và SHUTDOWN_DRAIN_DELAY phải >= 0"))
	}
	return errors.Join(errs...)
}

func (s Settings) validateJobs() []error {
	var errs []error
	if s.JobConcurrency <= 0 || s.JobConcurrency > 256 {
		errs = append(errs, errors.New("FHIR_JOB_CONCURRENCY: phải từ 1 đến 256"))
	}
	if s.JobLease <= 0 {
		errs = append(errs, errors.New("FHIR_JOB_LEASE: phải > 0"))
	}
	if s.JobCancelPoll <= 0 {
		errs = append(errs, errors.New("FHIR_JOB_CANCEL_POLL: phải > 0"))
	}
	if s.JobMaintenanceInterval <= 0 {
		errs = append(errs, errors.New("FHIR_JOB_MAINTENANCE_INTERVAL: phải > 0 (fhir-worker nhận job mới qua vòng này)"))
	}
	if s.JobRecoveryGrace < 0 {
		errs = append(errs, errors.New("FHIR_JOB_RECOVERY_GRACE: không được âm"))
	}
	if s.BatchCheckpointEntries <= 0 {
		errs = append(errs, errors.New("FHIR_BATCH_CHECKPOINT_ENTRIES: phải > 0"))
	}
	if s.JWTEnabled() {
		switch {
		case s.JobSigningKey != "" && len(s.JobSigningKey) < 32:
			errs = append(errs, errors.New("FHIR_JOB_SIGNING_KEY: tối thiểu 32 ký tự"))
		case s.JobSigningKey == "" && s.JWTHMACSecret == "":
			errs = append(errs, errors.New("FHIR_JOB_SIGNING_KEY: bắt buộc khi fhir-server bật JWT (FHIR_JWT_ISSUER): ký quyền của job, phải trùng fhir-server; dev HMAC dùng FHIR_JWT_HMAC_SECRET"))
		case s.JobSigningKey == "" && len(s.JWTHMACSecret) < 32:
			errs = append(errs, errors.New("FHIR_JWT_HMAC_SECRET: tối thiểu 32 ký tự"))
		}
		if isProd(s.Environment) {
			if s.JobSigningKey == "" {
				errs = append(errs, errors.New("FHIR_JOB_SIGNING_KEY: bắt buộc ở staging/production (khóa suy từ FHIR_JWT_HMAC_SECRET chỉ cho dev)"))
			}
			if s.JWTHMACSecret != "" {
				errs = append(errs, errors.New("FHIR_JWT_HMAC_SECRET: không dùng ở staging/production, dùng FHIR_JOB_SIGNING_KEY"))
			}
		}
	} else if s.JobSigningKey != "" {
		errs = append(errs, errors.New("FHIR_JOB_SIGNING_KEY: chỉ dùng khi fhir-server bật JWT (đặt FHIR_JWT_ISSUER): job ở chế độ token tĩnh không có chữ ký"))
	}
	return errs
}

func (s Settings) validateBroker() []error {
	var errs []error
	switch s.BrokerDriver {
	case "memory", "redis":
	default:
		return []error{errors.New(`BROKER_DRIVER: chỉ nhận "memory" hoặc "redis"`)}
	}
	if s.Publishes() {
		if s.BrokerDriver != "redis" {
			errs = append(errs, errors.New("BROKER_DRIVER: cần redis khi FHIR_SUBSCRIPTIONS hoặc FHIR_AUTO_DOWNLOAD bật (batch async ghi resource, sự kiện phải đến fhir-notifier và fhir-downloader ở tiến trình khác)"))
		} else if u, err := url.Parse(s.RedisURL); s.RedisURL == "" || err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
			errs = append(errs, errors.New("REDIS_URL: phải dạng redis://host:port/db hoặc rediss://... khi phải phát sự kiện"))
		}
	}
	return errs
}

func (s Settings) validateStorage() []error {
	var errs []error
	if !s.StorageEnabled() {
		return nil
	}
	switch s.StorageKind() {
	case "file":
		if strings.TrimSpace(s.StorageDir()) == "" {
			errs = append(errs, errors.New("FHIR_BINARY_STORAGE: file:<thư mục> thiếu thư mục"))
		}
	case "s3":
		errs = append(errs, s.validateS3()...)
	default:
		errs = append(errs, errors.New("FHIR_BINARY_STORAGE: chỉ hỗ trợ file:<thư mục> hoặc s3:<bucket>[/<prefix>]"))
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
