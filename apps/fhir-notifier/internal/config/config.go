// Package config đọc cấu hình fhir-notifier từ biến môi trường (và file .env nếu có).
//
// Cấu hình dùng chung với fhir-server (cơ sở dữ liệu, Redis, giao webhook, tự tắt Subscription, chế độ websocket) giữ NGUYÊN tên biến của fhir-server (`DATABASE_*`,
// `REDIS_URL`, `FHIR_WEBHOOK_*`, `FHIR_SUBSCRIPTION_AUTO_DISABLE`, `FHIR_WS_MODE`) để hai service dùng chung một tệp môi trường. fhir-notifier không có cờ `FHIR_SUBSCRIPTIONS`
// và luôn giao BỀN (như FHIR_WEBHOOK_DURABLE=true): tiến trình này chạy là đã bật; đặt `FHIR_SUBSCRIPTIONS=true` và `FHIR_SUBSCRIPTION_DELIVERY=false` ở fhir-server để
// API chỉ phát sự kiện.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"trivita-fhir/pkg/envconfig"
)

// Giá trị của FHIR_WS_MODE mà service này hiểu.
const (
	WSModeOff    = "off"
	WSModeIssuer = "issuer"
)

// Settings là toàn bộ cấu hình. Mặc định an toàn cho local; ở staging/production các ràng buộc chặt hơn (xem validate).
// Biến của trivita-observability-go (OTEL_*, LOG_LEVEL, LOG_JSON) do thư viện đó tự đọc.
type Settings struct {
	AppName     string // tên service trong log và telemetry (OTEL_SERVICE_NAME thắng nếu đặt)
	Addr        string // địa chỉ nghe của các probe, ví dụ :8010 (service không có route nghiệp vụ)
	Environment string // ENVIRONMENT: local | development | staging | production

	ReadinessTimeout   time.Duration // hạn tối đa của /health/ready khi kiểm database và Redis
	ShutdownDrainDelay time.Duration // sau SIGTERM: readiness 503 nhưng vẫn chạy, để orchestrator gỡ instance khỏi danh sách
	ShutdownTimeout    time.Duration // thời gian tối đa chờ lần giao đang chạy bị hủy và dừng sau khi hết drain; terminationGracePeriodSeconds phải lớn hơn tổng hai giá trị
	ReadHeaderTimeout  time.Duration // hạn đọc phần đầu HTTP của mỗi yêu cầu probe (chống kết nối treo)

	// DatabaseURL phải trỏ tới CÙNG schema `fhir` với fhir-server (đọc Subscription, tự tắt Subscription khi giao thất bại lặp lại). fhir-notifier KHÔNG chạy migration
	// (fhir-server sở hữu schema); schema chưa migrate thì từ chối khởi động.
	DatabaseURL string
	// DatabaseEcho ghi log mọi câu SQL (chỉ để gỡ lỗi; có thể lộ dữ liệu trong tham số).
	DatabaseEcho bool
	// DatabaseRequireSchema (chỉ PostgreSQL) từ chối khởi động nếu search_path không phải đúng một schema riêng (docs/plans/microservices-shared-db.md).
	DatabaseRequireSchema bool
	DatabaseMaxOpenConns  int           // số kết nối tối đa; tổng của mọi replica của mọi service phải nhỏ hơn max_connections của PostgreSQL
	DatabaseMaxIdleConns  int           // số kết nối nhàn rỗi giữ lại
	DatabaseConnLifetime  time.Duration // tuổi tối đa của một kết nối trước khi được thay

	// RedisURL (bắt buộc): broker Redis Streams mà fhir-server phát sự kiện ghi resource lên (BROKER_DRIVER=redis, FHIR_SUBSCRIPTIONS=true ở API), cũng là nơi đếm
	// thất bại giao chung giữa các replica và kênh websocket. Broker bộ nhớ không đến được tiến trình khác nên service này không có lựa chọn đó.
	RedisURL string
	// BrokerGroup là consumer group của service này trên stream sự kiện. KHÁC group của fhir-server ("trivita") để cả hai cùng nhận MỌI sự kiện (cùng group thì chia nhau);
	// nhiều replica của fhir-notifier dùng cùng group để chia việc.
	BrokerGroup string

	// BrokerClaimIdle là thời gian một message nằm trong PEL không được ACK thì replica khác nhận lại và giao lại (instance chết giữa lúc giao). 0: tự tính, dài hơn gấp đôi chuỗi giao
	// xấu nhất (FHIR_WEBHOOK_MAX_ATTEMPTS, FHIR_WEBHOOK_TIMEOUT và thời gian chờ giữa các lần thử), tối thiểu 1 phút. Đặt ngắn hơn một chuỗi giao thì message đang giao bị giao trùng ở replica khác.
	BrokerClaimIdle time.Duration

	// WebhookWorkers vừa là số sự kiện xử lý song song, vừa là số webhook giao song song cho mỗi sự kiện.
	WebhookWorkers int
	// WebhookMaxAttempts là số lần thử tối đa cho mỗi lần giao (thử lại tại chỗ, chờ 1 giây nhân đôi mỗi lần, trần 1 phút); hết thì bỏ cuộc, ghi log và tính vào ngưỡng tự tắt.
	WebhookMaxAttempts int
	// WebhookTimeout là hạn của mỗi request webhook.
	WebhookTimeout time.Duration
	// WebhookAllowPrivate CHỈ cho dev: cho phép giao tới địa chỉ nội bộ (chống SSRF bị tắt). Cấm ở staging/production.
	WebhookAllowPrivate bool
	// SubscriptionAutoDisable là JSON mảng trigger tự tắt Subscription rest-hook giao thất bại lặp lại, dạng [{"maxConsecutiveFailures":10,"timeWindowSeconds":3600}]
	// (cùng dạng subscriptionAutoDisable của Medplum). Rỗng: không tự tắt. Đếm chung giữa các replica trên Redis.
	SubscriptionAutoDisable string
	// WSMode: "off" (mặc định, không khớp Subscription websocket) hoặc "issuer" (khớp một lần cho cả cụm rồi phát lên kênh Redis cho ws-gateway; token bind vẫn do fhir-server cấp).
	// "embedded" (Hub trong tiến trình) không dùng được ở service này.
	WSMode string
}

// Default trả về cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-fhir-notifier", Addr: ":8010", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		DatabaseURL:       "sqlite://./fhir.db", DatabaseMaxOpenConns: 20, DatabaseMaxIdleConns: 5, DatabaseConnLifetime: 30 * time.Minute,
		RedisURL: "redis://localhost:6379/0", BrokerGroup: "fhir-notifier",
		WebhookWorkers: 4, WebhookMaxAttempts: 5, WebhookTimeout: 10 * time.Second,
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
	r.Duration(&s.BrokerClaimIdle, "BROKER_CLAIM_IDLE")
	r.Int(&s.WebhookWorkers, "FHIR_WEBHOOK_WORKERS")
	r.Int(&s.WebhookMaxAttempts, "FHIR_WEBHOOK_MAX_ATTEMPTS")
	r.Duration(&s.WebhookTimeout, "FHIR_WEBHOOK_TIMEOUT")
	r.Bool(&s.WebhookAllowPrivate, "FHIR_WEBHOOK_ALLOW_PRIVATE")
	r.String(&s.SubscriptionAutoDisable, "FHIR_SUBSCRIPTION_AUTO_DISABLE")
	r.String(&s.WSMode, "FHIR_WS_MODE")
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

// WSIssuer cho biết có khớp Subscription websocket và phát lên kênh Redis không.
func (s Settings) WSIssuer() bool { return s.WSMode == WSModeIssuer }

// AutoDisableTrigger là một phần tử của FHIR_SUBSCRIPTION_AUTO_DISABLE.
type AutoDisableTrigger struct {
	MaxConsecutiveFailures int `json:"maxConsecutiveFailures"`
	TimeWindowSeconds      int `json:"timeWindowSeconds"`
}

// AutoDisableTriggers phân tích SubscriptionAutoDisable (rỗng: không có trigger).
func (s Settings) AutoDisableTriggers() ([]AutoDisableTrigger, error) {
	raw := strings.TrimSpace(s.SubscriptionAutoDisable)
	if raw == "" {
		return nil, nil
	}
	var out []AutoDisableTrigger
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return nil, fmt.Errorf("FHIR_SUBSCRIPTION_AUTO_DISABLE: phải là JSON mảng {maxConsecutiveFailures, timeWindowSeconds}: %w", err)
	}
	for i, t := range out {
		if t.MaxConsecutiveFailures <= 0 || t.TimeWindowSeconds <= 0 {
			return nil, fmt.Errorf("FHIR_SUBSCRIPTION_AUTO_DISABLE: trigger #%d: maxConsecutiveFailures và timeWindowSeconds phải là số nguyên dương", i+1)
		}
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
	if s.RedisURL == "" {
		errs = append(errs, errors.New("REDIS_URL: bắt buộc (fhir-notifier nhận sự kiện từ broker Redis do fhir-server phát)"))
	} else if u, err := url.Parse(s.RedisURL); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
		errs = append(errs, errors.New("REDIS_URL: phải dạng redis://host:port/db hoặc rediss://..."))
	}
	if strings.TrimSpace(s.BrokerGroup) == "" || strings.ContainsAny(s.BrokerGroup, " \t\n") {
		errs = append(errs, errors.New("BROKER_GROUP: không được rỗng hoặc có khoảng trắng"))
	} else if s.BrokerGroup == "trivita" {
		errs = append(errs, errors.New(`BROKER_GROUP: không được là "trivita" (group của fhir-server; cùng group thì hai service chia nhau sự kiện thay vì cùng nhận)`))
	}
	if s.BrokerClaimIdle < 0 {
		errs = append(errs, errors.New("BROKER_CLAIM_IDLE: không được âm (0: tự tính)"))
	} else if s.BrokerClaimIdle > 0 && s.WebhookMaxAttempts > 0 && s.WebhookTimeout > 0 && s.BrokerClaimIdle < time.Duration(s.WebhookMaxAttempts)*s.WebhookTimeout {
		errs = append(errs, errors.New("BROKER_CLAIM_IDLE: ngắn hơn FHIR_WEBHOOK_MAX_ATTEMPTS x FHIR_WEBHOOK_TIMEOUT (replica khác sẽ nhận lại message đang giao và giao trùng)"))
	}
	if s.WebhookWorkers <= 0 || s.WebhookWorkers > 256 {
		errs = append(errs, errors.New("FHIR_WEBHOOK_WORKERS: phải từ 1 đến 256"))
	}
	if s.WebhookMaxAttempts <= 0 || s.WebhookMaxAttempts > 20 {
		errs = append(errs, errors.New("FHIR_WEBHOOK_MAX_ATTEMPTS: phải từ 1 đến 20"))
	}
	if s.WebhookTimeout <= 0 {
		errs = append(errs, errors.New("FHIR_WEBHOOK_TIMEOUT: phải > 0"))
	}
	if s.WebhookAllowPrivate && isProd(s.Environment) {
		errs = append(errs, errors.New("FHIR_WEBHOOK_ALLOW_PRIVATE: không được bật ở staging/production (tắt chống SSRF)"))
	}
	if _, err := s.AutoDisableTriggers(); err != nil {
		errs = append(errs, err)
	}
	switch s.WSMode {
	case "", WSModeOff, WSModeIssuer:
	case "embedded":
		errs = append(errs, errors.New("FHIR_WS_MODE=embedded: không dùng được ở fhir-notifier (Hub nằm trong tiến trình fhir-server); dùng issuer hoặc off"))
	default:
		errs = append(errs, fmt.Errorf("FHIR_WS_MODE: %q không hợp lệ (off | issuer)", s.WSMode))
	}
	if s.ReadinessTimeout <= 0 {
		errs = append(errs, errors.New("READINESS_TIMEOUT: phải > 0"))
	}
	if s.ShutdownTimeout <= 0 || s.ShutdownDrainDelay < 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT phải > 0 và SHUTDOWN_DRAIN_DELAY phải >= 0"))
	}
	return errors.Join(errs...)
}
