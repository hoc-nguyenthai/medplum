// Package bootstrap là composition root của ws-gateway: nơi DUY NHẤT lắp ráp adapter cụ thể (Redis, Hub, Feed) thành HTTP
// handler và quản lý vòng đời. Gateway không có domain/application riêng: nghiệp vụ nằm ở pkg/fhirsub và pkg/wsgroup.
//
// Gateway không có cơ sở dữ liệu, không có nguồn Subscription và không có Manager: token bind do fhir-server cấp và ký đã
// chứng minh Subscription hợp lệ lúc cấp (fhirsub.WSConfig.TokenOnly); sự kiện đến qua kênh Redis của từng Subscription
// đang có socket (fhirsub.Feed), do fhir-server khớp tiêu chí và phát (fhirsub.Manager.AttachRemote).
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/metric"

	"trivita-fhir/apps/ws-gateway/internal/config"
	"trivita-fhir/apps/ws-gateway/internal/presentation/httpapi"
	"trivita-fhir/pkg/fhircast"
	fcws "trivita-fhir/pkg/fhircast/ws"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/httpserve"
	"trivita-fhir/pkg/messaging"
)

// Container giữ mọi adapter đã lắp và vòng đời của chúng.
type Container struct {
	Settings   config.Settings
	Redis      *messaging.Redis
	Hub        *fhirsub.Hub
	Feed       *fhirsub.Feed
	Fhircast   *fhircast.Hub // nil khi FHIRCAST_ENABLED=false
	FhircastWS *fcws.Server  // kênh websocket của FHIRcast (nil khi tắt)

	fhircastDeps *httpapi.FhircastDeps

	clientIP func(*http.Request) string
	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

// New dựng Redis, Hub và Feed. Redis chưa chạm được lúc khởi động KHÔNG làm tiến trình thoát (readiness báo 503 cho tới khi
// Redis lên), nhưng cấu hình sai thì thoát ngay. Lỗi giữa chừng thì đóng những gì đã mở.
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
	nets, err := s.TrustedProxyNets()
	if err != nil {
		return err
	}
	c.clientIP = echo.ExtractIPDirect()
	if len(nets) > 0 {
		opts := []echo.TrustOption{echo.TrustLoopback(false), echo.TrustLinkLocal(false), echo.TrustPrivateNet(false)}
		for _, n := range nets {
			opts = append(opts, echo.TrustIPRange(n))
		}
		c.clientIP = echo.ExtractIPFromXFFHeader(opts...)
	}

	// Redis dùng cho kênh sự kiện và sổ hiện diện; không Start broker (gateway không có consumer group nào).
	if c.Redis, err = messaging.NewRedis(s.RedisURL, messaging.RedisOptions{}); err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, s.ReadinessTimeout)
	if err := c.Redis.Ping(pctx); err != nil {
		slog.Warn("Redis chưa dùng được lúc khởi động; readiness sẽ báo 503 cho tới khi kết nối được", "error", err.Error())
	}
	cancel()

	prev := make([][]byte, 0, len(s.PreviousKeys()))
	for _, k := range s.PreviousKeys() {
		prev = append(prev, []byte(k))
	}
	tokens, err := fhirsub.NewTokens(fhirsub.TokenConfig{SigningKey: []byte(s.WSSigningKey), Previous: prev})
	if err != nil {
		return err
	}
	if c.Hub, err = fhirsub.NewHub(fhirsub.WSConfig{
		Tokens: tokens, TokenOnly: true,
		MaxConns: s.WSMaxConnections, MaxBindingsPerUser: s.WSMaxBindingsPerUser, MaxConnsPerIP: s.WSMaxConnsPerIP, ClientIP: c.clientIP,
		Heartbeat: s.WSHeartbeat, CloseTimeout: s.WSCloseTimeout, OriginPatterns: s.OriginPatterns(),
	}); err != nil {
		return err
	}
	if c.Feed, err = fhirsub.NewFeed(c.Hub, c.Redis, c.Redis, fhirsub.FeedConfig{InstanceID: s.InstanceID, PresenceTTL: s.WSPresenceTTL}); err != nil {
		return err
	}
	// Stop chạy ngược thứ tự đăng ký: Hub đóng (1001 cho client) -> Feed gỡ sổ hiện diện và bỏ đăng ký -> Redis đóng.
	c.onStop(c.Redis.Stop)
	c.onStop(c.Feed.Stop)
	c.onStop(c.Hub.Close)
	if s.FhircastEnabled {
		// Đăng ký SAU Redis.Stop để chạy TRƯỚC nó khi tắt (bus mượn client của Redis).
		if c.fhircastDeps, err = c.buildFhircast(); err != nil {
			return err
		}
	}
	return nil
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

// BeginShutdown được gọi khi bắt đầu tắt: /health/ready trả 503 để K8s ngừng gửi kết nối mới.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Readiness kiểm tra Redis với timeout READINESS_TIMEOUT.
func (c *Container) Readiness(ctx context.Context) (bool, map[string]bool) {
	pctx, cancel := context.WithTimeout(ctx, c.Settings.ReadinessTimeout)
	defer cancel()
	redisOK := c.Redis.Ping(pctx) == nil
	if !redisOK {
		slog.WarnContext(ctx, "readiness check lỗi", "check", "redis")
	}
	accepting := !c.draining.Load()
	return redisOK && accepting, map[string]bool{"accepting_traffic": accepting, "redis": redisOK}
}

// Handler dựng HTTP handler. instrument (có thể nil) gắn observability trước mọi middleware khác.
func (c *Container) Handler(instrument func(*echo.Echo) error) (*echo.Echo, error) {
	return httpapi.New(httpapi.Deps{
		WebSocket: c.Hub.Handler(), Ready: c.Readiness, Instrument: instrument, Debug: c.Settings.Debug,
		IPExtractor: c.clientIP, RateLimit: c.Settings.RateLimit, RateBurst: c.Settings.RateBurst, Fhircast: c.fhircastDeps,
	})
}

// ServeOptions là các móc nối của composition root (main hoặc test).
type ServeOptions struct {
	Instrument func(*echo.Echo) error // gắn observability (nil = không)
	Meter      func() metric.Meter    // tạo meter SAU khi Instrument đã chạy (nil = không đăng ký metric); main truyền observability.Meter
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
	if opts.Meter != nil {
		// Sau khi Instrument (Setup observability) đã chạy: metric chỉ ghi khi OTEL_METRICS_ENABLED=true.
		unregister, err := c.RegisterMetrics(opts.Meter())
		if err != nil {
			_ = c.Stop(context.Background())
			return err
		}
		c.onStop(func(context.Context) error { return unregister() })
	}
	if s.RateLimit == 0 && s.IsProd() {
		slog.Warn("FHIR_WS_RATE_LIMIT=0: không giới hạn số lần mở websocket; đặt hạn mức theo IP hoặc giới hạn ở gateway trước khi mở ra Internet")
	}
	if s.WSMaxConnsPerIP == 0 && s.IsProd() {
		slog.Warn("FHIR_WS_MAX_CONNS_PER_IP=0: không giới hạn số kết nối mỗi IP; đặt cùng FHIR_WS_TRUSTED_PROXIES nếu chạy sau proxy")
	}
	if len(s.OriginPatterns()) == 0 && s.IsProd() {
		slog.Warn("FHIR_WS_ALLOWED_ORIGINS rỗng: trình duyệt chỉ vào được từ chính host của gateway; đặt mẫu host của ứng dụng web nếu cần")
	}
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: e, ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}
