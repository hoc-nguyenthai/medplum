// Package config đọc cấu hình ws-gateway từ biến môi trường (và file .env nếu có).
//
// Tên biến cho phần websocket giữ tiền tố FHIR_WS_ như fhir-server để chuyển cấu hình giữa hai bên dễ dàng; khóa ký token
// (FHIR_WS_SIGNING_KEY) PHẢI giống bên cấp token (fhir-server).
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
)

// DefaultRedisURL là địa chỉ Redis mặc định; ở staging/production phải đặt REDIS_URL tường minh.
const DefaultRedisURL = "redis://localhost:6379/0"

// DefaultProdRateLimit là số lần mở websocket mỗi giây mỗi IP khi staging/production không đặt FHIR_WS_RATE_LIMIT.
const DefaultProdRateLimit = 10

// MinKeyLen là độ dài tối thiểu của khóa ký token.
const MinKeyLen = 32

// MaxPreviousKeys là số khóa cũ tối đa (khớp fhirsub.MaxPreviousKeys).
const MaxPreviousKeys = 3

// Settings là toàn bộ cấu hình. Biến của trivita-observability-go (OTEL_*, LOG_LEVEL, LOG_JSON) do thư viện đó tự đọc.
type Settings struct {
	AppName     string // tên service trong log và telemetry
	Addr        string // địa chỉ HTTP lắng nghe (HTTP_ADDR), ví dụ :8007
	Debug       bool   // bật chế độ debug của Echo (không dùng ở production)
	Environment string // ENVIRONMENT: local | development | staging | production

	ReadinessTimeout   time.Duration // hạn mỗi lần kiểm Redis của /health/ready
	ShutdownDrainDelay time.Duration // thời gian báo "chưa sẵn sàng" trước khi đóng cổng để K8s rút khỏi Service (pkg/httpserve)
	ShutdownTimeout    time.Duration // thời gian tối đa của cả quá trình tắt êm (pkg/httpserve)
	ReadHeaderTimeout  time.Duration // hạn đọc header của một yêu cầu HTTP (chống kết nối chậm)

	// RedisURL là Redis dùng chung với fhir-server: kênh sự kiện và sổ hiện diện của Subscription (bắt buộc ở staging/production).
	RedisURL string

	// WSSigningKey là khóa HMAC xác minh token bind do fhir-server cấp (>= 32 ký tự, bắt buộc, KHÔNG có giá trị ngẫu nhiên
	// thay thế vì phải trùng bên cấp). WSSigningKeyPrevious là khóa cũ chỉ để xác minh khi xoay khóa, cách nhau bằng dấu phẩy.
	WSSigningKey         string
	WSSigningKeyPrevious string        // khóa cũ chỉ để xác minh khi xoay khóa (tối đa 3, cách nhau bằng dấu phẩy); đặt khóa mới ở gateway trước, đổi bên cấp sau
	WSMaxConnections     int           // số kết nối tối đa mỗi instance
	WSMaxBindingsPerUser int           // số lượt bind đồng thời tối đa của một người trong một project (mỗi instance)
	WSMaxConnsPerIP      int           // số kết nối tối đa từ một IP (0: không giới hạn)
	WSHeartbeat          time.Duration // chu kỳ heartbeat và ping
	WSCloseTimeout       time.Duration // thời gian chờ client đáp khung đóng khi tắt
	WSAllowedOrigins     string        // mẫu host của Origin được phép, cách nhau bằng dấu phẩy (rỗng: chỉ cùng host)
	WSPresenceTTL        time.Duration // hạn mục hiện diện nếu gateway không gia hạn (bên phát ngừng gửi sau thời gian này khi gateway chết)
	InstanceID           string        // định danh trong sổ hiện diện (rỗng: ngẫu nhiên mỗi process)

	// TrustedProxies là các CIDR của proxy được tin khi đọc X-Forwarded-For (để đếm theo IP và hạn mức). Rỗng: dùng địa chỉ kết nối.
	TrustedProxies string
	// RateLimit là số lần mở websocket mỗi giây mỗi IP (0: tắt; không đặt thì ở staging/production là DefaultProdRateLimit);
	// RateBurst là cỡ đợt (mặc định gấp đôi, tối thiểu 1).
	RateLimit float64
	RateBurst int // cỡ đợt của hạn mức trên (0: gấp đôi RateLimit, tối thiểu 1)

	// ---- FHIRcast (tắt mặc định) ----
	// FhircastEnabled bật Hub FHIRcast (/fhircast/STU2, /fhircast/STU3, /hub). Cần Redis (dùng chung) và xác thực JWT.
	FhircastEnabled bool
	// FhircastPublicURL là địa chỉ WebSocket công khai của gateway (ws:// hoặc wss://host[:port]) dùng để dựng
	// hub.channel.endpoint trả cho client; KHÔNG suy từ header Host của yêu cầu (tránh giả mạo).
	FhircastPublicURL string
	// Xác thực JWT: cùng nhà phát hành token với fhir-server (auth). JWKS là đường production; HMAC chỉ cho dev/test.
	FhircastJWTIssuer     string // claim iss mà token phải có (bắt buộc khi bật FHIRcast)
	FhircastJWTAudience   string // claim aud mà token phải có (bắt buộc khi bật FHIRcast)
	FhircastJWKSURL       string // địa chỉ JWKS của auth (https ở staging/production); đặt đúng một trong JWKS và HMAC
	FhircastJWTHMACSecret string // khóa HS256 dùng chung, tối thiểu 32 ký tự; CHỈ dev/test, cấm ở staging/production
	// FhircastRequireScope đòi token có scope fhircast/* (mặc định true; tắt chỉ để chuyển tiếp cho client cũ).
	FhircastRequireScope bool
	// FhircastAllowTopicMismatch cho phép POST /{topic} dùng event["hub.topic"] khác topic trên đường dẫn (như Medplum, bỏ
	// qua topic đường dẫn). Mặc định false: từ chối 400 "Topic mismatch".
	FhircastAllowTopicMismatch bool
	FhircastLease              time.Duration // hạn một đăng ký (mặc định 1 giờ)
	FhircastContextTTL         time.Duration // hạn ngữ cảnh của topic (mặc định 24 giờ)
	FhircastMaxContextBytes    int           // kích thước tối đa một ngữ cảnh lưu (mặc định 256 KiB)
	FhircastMaxUpdateEntries   int           // số entry tối đa của một update bundle (mặc định 200)
	FhircastMaxBodyBytes       int64         // kích thước tối đa thân yêu cầu HTTP (mặc định 1 MiB)
	// Kênh WebSocket /ws/fhircast/{endpoint}. IP, Origin và thời gian đóng dùng chung FHIR_WS_MAX_CONNS_PER_IP,
	// FHIR_WS_TRUSTED_PROXIES, FHIR_WS_ALLOWED_ORIGINS, FHIR_WS_CLOSE_TIMEOUT, FHIR_WS_RATE_LIMIT.
	FhircastMaxConnections        int           // số socket FHIRcast tối đa mỗi instance (mặc định 1000; tách khỏi FHIR_WS_MAX_CONNECTIONS)
	FhircastMaxSocketsPerEndpoint int           // số socket đồng thời tối đa của một endpoint, mỗi instance (mặc định 4)
	FhircastHeartbeat             time.Duration // chu kỳ heartbeat của từng socket (mặc định 10s)
}

// Default trả cấu hình mặc định.
func Default() Settings {
	return Settings{
		AppName: "trivita-ws-gateway", Addr: ":8007", Environment: "local",
		ReadinessTimeout: 2 * time.Second, ShutdownDrainDelay: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		RedisURL:          DefaultRedisURL,
		WSMaxConnections:  1000, WSMaxBindingsPerUser: 20, WSHeartbeat: 30 * time.Second, WSCloseTimeout: 5 * time.Second,
		WSPresenceTTL: 30 * time.Second,
		RateLimit:     -1,

		FhircastRequireScope: true, FhircastLease: time.Hour, FhircastContextTTL: 24 * time.Hour,
		FhircastMaxContextBytes: 256 << 10, FhircastMaxUpdateEntries: 200, FhircastMaxBodyBytes: 1 << 20,
		FhircastMaxConnections: 1000, FhircastMaxSocketsPerEndpoint: 4, FhircastHeartbeat: 10 * time.Second,
	}
}

func isProd(env string) bool { return env == "production" || env == "staging" }

// IsProd cho biết đang chạy ở staging/production.
func (s Settings) IsProd() bool { return isProd(s.Environment) }

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
	r.String(&s.RedisURL, "REDIS_URL")
	r.String(&s.WSSigningKey, "FHIR_WS_SIGNING_KEY")
	r.String(&s.WSSigningKeyPrevious, "FHIR_WS_SIGNING_KEY_PREVIOUS")
	r.Int(&s.WSMaxConnections, "FHIR_WS_MAX_CONNECTIONS")
	r.Int(&s.WSMaxBindingsPerUser, "FHIR_WS_MAX_BINDINGS_PER_USER")
	r.Int(&s.WSMaxConnsPerIP, "FHIR_WS_MAX_CONNS_PER_IP")
	r.Duration(&s.WSHeartbeat, "FHIR_WS_HEARTBEAT")
	r.Duration(&s.WSCloseTimeout, "FHIR_WS_CLOSE_TIMEOUT")
	r.String(&s.WSAllowedOrigins, "FHIR_WS_ALLOWED_ORIGINS")
	r.Duration(&s.WSPresenceTTL, "FHIR_WS_PRESENCE_TTL")
	r.String(&s.InstanceID, "FHIR_WS_INSTANCE_ID")
	r.String(&s.TrustedProxies, "FHIR_WS_TRUSTED_PROXIES")
	r.Float(&s.RateLimit, "FHIR_WS_RATE_LIMIT")
	r.Int(&s.RateBurst, "FHIR_WS_RATE_BURST")
	r.Bool(&s.FhircastEnabled, "FHIRCAST_ENABLED")
	r.String(&s.FhircastPublicURL, "FHIRCAST_PUBLIC_URL")
	r.String(&s.FhircastJWTIssuer, "FHIRCAST_JWT_ISSUER")
	r.String(&s.FhircastJWTAudience, "FHIRCAST_JWT_AUDIENCE")
	r.String(&s.FhircastJWKSURL, "FHIRCAST_JWKS_URL")
	r.String(&s.FhircastJWTHMACSecret, "FHIRCAST_JWT_HMAC_SECRET")
	r.Bool(&s.FhircastRequireScope, "FHIRCAST_REQUIRE_SCOPE")
	r.Bool(&s.FhircastAllowTopicMismatch, "FHIRCAST_ALLOW_TOPIC_MISMATCH")
	r.Duration(&s.FhircastLease, "FHIRCAST_LEASE")
	r.Duration(&s.FhircastContextTTL, "FHIRCAST_CONTEXT_TTL")
	r.Int(&s.FhircastMaxContextBytes, "FHIRCAST_MAX_CONTEXT_BYTES")
	r.Int(&s.FhircastMaxUpdateEntries, "FHIRCAST_MAX_UPDATE_ENTRIES")
	var maxBody int
	maxBody = int(s.FhircastMaxBodyBytes)
	r.Int(&maxBody, "FHIRCAST_MAX_BODY_BYTES")
	s.FhircastMaxBodyBytes = int64(maxBody)
	r.Int(&s.FhircastMaxConnections, "FHIRCAST_MAX_CONNECTIONS")
	r.Int(&s.FhircastMaxSocketsPerEndpoint, "FHIRCAST_MAX_SOCKETS_PER_ENDPOINT")
	r.Duration(&s.FhircastHeartbeat, "FHIRCAST_HEARTBEAT")
	if err := r.Err(); err != nil {
		return Settings{}, err
	}
	if s.RateLimit < 0 && get("FHIR_WS_RATE_LIMIT") == "" && isProd(s.Environment) {
		s.RateLimit = DefaultProdRateLimit // không đặt thì bật ở staging/production; đặt rõ 0 mới tắt
	} else if s.RateLimit < 0 && get("FHIR_WS_RATE_LIMIT") == "" {
		s.RateLimit = 0
	}
	if err := s.validate(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// PreviousKeys tách FHIR_WS_SIGNING_KEY_PREVIOUS thành danh sách khóa (bỏ phần tử rỗng).
func (s Settings) PreviousKeys() []string { return splitList(s.WSSigningKeyPrevious) }

// OriginPatterns tách FHIR_WS_ALLOWED_ORIGINS thành danh sách mẫu host.
func (s Settings) OriginPatterns() []string { return splitList(s.WSAllowedOrigins) }

// TrustedProxyNets phân tích TrustedProxies thành các mạng CIDR.
func (s Settings) TrustedProxyNets() ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range splitList(s.TrustedProxies) {
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("%q không phải CIDR", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s Settings) validate() error {
	var errs []error
	switch {
	case s.WSSigningKey == "":
		errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY: bắt buộc (phải giống khóa của fhir-server, tối thiểu 32 ký tự)"))
	case len(s.WSSigningKey) < MinKeyLen:
		errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY: tối thiểu 32 ký tự"))
	}
	if s.WSSigningKeyPrevious != "" {
		prev := s.PreviousKeys()
		if len(prev) > MaxPreviousKeys {
			errs = append(errs, fmt.Errorf("FHIR_WS_SIGNING_KEY_PREVIOUS: tối đa %d khóa", MaxPreviousKeys))
		}
		for _, k := range prev {
			if len(k) < MinKeyLen {
				errs = append(errs, errors.New("FHIR_WS_SIGNING_KEY_PREVIOUS: mỗi khóa tối thiểu 32 ký tự"))
				break
			}
		}
	}
	if s.RedisURL == "" || (isProd(s.Environment) && s.RedisURL == DefaultRedisURL) {
		errs = append(errs, errors.New("REDIS_URL: bắt buộc đặt tường minh ở staging/production (Redis dùng chung với fhir-server)"))
	}
	if s.WSMaxConnections <= 0 || s.WSMaxBindingsPerUser <= 0 || s.WSHeartbeat <= 0 || s.WSCloseTimeout <= 0 {
		errs = append(errs, errors.New("FHIR_WS_MAX_CONNECTIONS, FHIR_WS_MAX_BINDINGS_PER_USER, FHIR_WS_HEARTBEAT và FHIR_WS_CLOSE_TIMEOUT phải > 0"))
	}
	if s.WSMaxConnsPerIP < 0 {
		errs = append(errs, errors.New("FHIR_WS_MAX_CONNS_PER_IP: không được âm"))
	}
	if s.WSPresenceTTL < 3*time.Second {
		errs = append(errs, errors.New("FHIR_WS_PRESENCE_TTL: tối thiểu 3s (gia hạn mỗi một phần ba TTL)"))
	}
	if s.WSCloseTimeout > s.ShutdownTimeout {
		errs = append(errs, errors.New("FHIR_WS_CLOSE_TIMEOUT không được lớn hơn SHUTDOWN_TIMEOUT"))
	}
	for _, o := range s.OriginPatterns() {
		switch {
		case o == "*" && isProd(s.Environment):
			errs = append(errs, errors.New(`FHIR_WS_ALLOWED_ORIGINS: "*" không được dùng ở staging/production`))
		case strings.Contains(o, "://") || strings.Contains(o, "/"):
			errs = append(errs, fmt.Errorf("FHIR_WS_ALLOWED_ORIGINS: %q phải là mẫu host (ví dụ app.example.org hoặc *.example.org), không phải URL", o))
		}
	}
	if _, err := s.TrustedProxyNets(); err != nil {
		errs = append(errs, fmt.Errorf("FHIR_WS_TRUSTED_PROXIES: %w", err))
	}
	if s.RateLimit < 0 || s.RateBurst < 0 {
		errs = append(errs, errors.New("FHIR_WS_RATE_LIMIT và FHIR_WS_RATE_BURST: không được âm"))
	}
	errs = append(errs, s.validateFhircast()...)
	return errors.Join(errs...)
}

// validateFhircast kiểm tra nhóm FHIRCAST_*. Khi tắt mà vẫn đặt biến của nhóm này thì báo lỗi (cấu hình bị bỏ quên).
func (s Settings) validateFhircast() []error {
	var errs []error
	if !s.FhircastEnabled {
		if s.FhircastPublicURL != "" || s.FhircastJWTIssuer != "" || s.FhircastJWTAudience != "" || s.FhircastJWKSURL != "" || s.FhircastJWTHMACSecret != "" {
			errs = append(errs, errors.New("FHIRCAST_*: có đặt cấu hình nhưng FHIRCAST_ENABLED chưa bật"))
		}
		return errs
	}
	u, err := url.Parse(s.FhircastPublicURL)
	switch {
	case s.FhircastPublicURL == "":
		errs = append(errs, errors.New("FHIRCAST_PUBLIC_URL: bắt buộc khi bật FHIRCAST (ws://host hoặc wss://host)"))
	case err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "":
		errs = append(errs, fmt.Errorf("FHIRCAST_PUBLIC_URL: %q phải là ws:// hoặc wss:// với host, không có đường dẫn", s.FhircastPublicURL))
	case isProd(s.Environment) && u.Scheme != "wss":
		errs = append(errs, errors.New("FHIRCAST_PUBLIC_URL: phải là wss:// ở staging/production"))
	}
	if s.FhircastJWTIssuer == "" || s.FhircastJWTAudience == "" {
		errs = append(errs, errors.New("FHIRCAST_JWT_ISSUER và FHIRCAST_JWT_AUDIENCE: bắt buộc khi bật FHIRCAST"))
	}
	if (s.FhircastJWKSURL == "") == (s.FhircastJWTHMACSecret == "") {
		errs = append(errs, errors.New("FHIRCAST_JWKS_URL hoặc FHIRCAST_JWT_HMAC_SECRET: cần đúng một trong hai"))
	}
	if s.FhircastJWTHMACSecret != "" && len(s.FhircastJWTHMACSecret) < MinKeyLen {
		errs = append(errs, errors.New("FHIRCAST_JWT_HMAC_SECRET: tối thiểu 32 ký tự"))
	}
	if isProd(s.Environment) {
		if s.FhircastJWTHMACSecret != "" {
			errs = append(errs, errors.New("FHIRCAST_JWT_HMAC_SECRET: không dùng ở staging/production, dùng FHIRCAST_JWKS_URL"))
		}
		if s.FhircastJWKSURL != "" && !strings.HasPrefix(s.FhircastJWKSURL, "https://") {
			errs = append(errs, errors.New("FHIRCAST_JWKS_URL: phải là https ở staging/production"))
		}
		if !s.FhircastRequireScope {
			errs = append(errs, errors.New("FHIRCAST_REQUIRE_SCOPE=false: không dùng ở staging/production"))
		}
	}
	if s.FhircastLease <= 0 || s.FhircastLease > 24*time.Hour {
		errs = append(errs, errors.New("FHIRCAST_LEASE: phải trong (0, 24h]"))
	}
	if s.FhircastContextTTL <= 0 {
		errs = append(errs, errors.New("FHIRCAST_CONTEXT_TTL: phải > 0 (ngữ cảnh không được tồn vô hạn)"))
	}
	if s.FhircastMaxContextBytes <= 0 || s.FhircastMaxUpdateEntries <= 0 || s.FhircastMaxBodyBytes <= 0 {
		errs = append(errs, errors.New("FHIRCAST_MAX_CONTEXT_BYTES, FHIRCAST_MAX_UPDATE_ENTRIES và FHIRCAST_MAX_BODY_BYTES phải > 0"))
	}
	if s.FhircastMaxConnections <= 0 || s.FhircastMaxSocketsPerEndpoint <= 0 || s.FhircastHeartbeat <= 0 {
		errs = append(errs, errors.New("FHIRCAST_MAX_CONNECTIONS, FHIRCAST_MAX_SOCKETS_PER_ENDPOINT và FHIRCAST_HEARTBEAT phải > 0"))
	}
	return errs
}
