// Package bootstrap là composition root của fhir-notifier: nơi DUY NHẤT lắp ráp adapter cụ thể (kết nối DB, fhirstore, broker Redis) thành Manager giao
// rest-hook và khớp websocket, và quản lý vòng đời.
//
// fhir-notifier không có domain/application riêng: nghiệp vụ khớp và giao nằm ở pkg/fhirsub (dùng chung với fhir-server khi chạy cùng tiến trình); dữ liệu là resource FHIR
// ở schema `fhir` của fhir-server. App chỉ còn cấu hình, lắp ráp và vận hành.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"trivita-fhir/apps/fhir-notifier/internal/config"
	"trivita-fhir/apps/fhir-notifier/internal/presentation/httpapi"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/httpserve"
	"trivita-fhir/pkg/messaging"
)

// Container giữ mọi adapter đã lắp và vòng đời của chúng.
type Container struct {
	Settings config.Settings
	DB       *database.DB
	Store    *fhirstore.Store
	Broker   *messaging.Redis
	Manager  *fhirsub.Manager

	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

// New mở DB (từ chối khởi động nếu schema chưa migrate), broker và bắt đầu nghe sự kiện. Lỗi giữa chừng thì đóng những gì đã mở.
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
	}
	if c.Store, err = fhirstore.New(c.DB); err != nil {
		return err
	}
	// dùng schema của fhir-server nên không tự migrate: chưa migrate thì từ chối khởi động (không dựng schema ngoài ý muốn)
	n, err := c.Store.Pending(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("schema chưa được migrate: chạy `fhir-server migrate up` (fhir-notifier dùng schema của fhir-server và không tự migrate)")
	}

	triggers, err := s.AutoDisableTriggers()
	if err != nil {
		return err
	}
	mcfg := fhirsub.Config{
		Workers: s.WebhookWorkers, MaxAttempts: s.WebhookMaxAttempts, Timeout: s.WebhookTimeout, AllowPrivate: s.WebhookAllowPrivate,
		AutoDisable: autoDisableTriggers(triggers), Durable: true,
	}
	// Giao bền: handler giao đồng bộ trong sự kiện nên xử lý song song (WebhookWorkers) và ClaimIdle phải dài hơn chuỗi giao xấu nhất, nếu không replica khác nhận lại message còn đang giao.
	claimIdle := s.BrokerClaimIdle
	if claimIdle <= 0 {
		claimIdle = mcfg.DurableClaimIdle()
	}
	br, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: s.RedisURL, Group: s.BrokerGroup, Concurrency: s.WebhookWorkers,
		ClaimIdle: claimIdle, ClaimInterval: min(max(claimIdle/3, 500*time.Millisecond), 10*time.Second)})
	if err != nil {
		return err
	}
	rb, ok := br.(*messaging.Redis)
	if !ok {
		_ = br.Stop(context.Background())
		return errors.New("broker không phải Redis")
	}
	c.Broker = rb
	c.onStop(c.Broker.Stop)

	// Đếm thất bại dùng chung giữa các replica (cùng Redis): ngưỡng tự tắt tính trên cả cụm. Manager đọc và ghi Subscription qua kho GỐC (không phát sự kiện).
	mcfg.Failures = fhirsub.NewRedisFailureTracker(c.Broker.Client(), "", nil)
	c.Manager = fhirsub.NewManager(c.Store, mcfg)
	c.Manager.Register(c.Broker)
	if s.WSIssuer() {
		// Khớp một lần cho cả cụm (handler nhóm) rồi phát lên kênh của Subscription nếu có ws-gateway đang giữ socket bind nó; không có Hub trong tiến trình.
		if err := c.Manager.AttachRemote(c.Broker, c.Broker); err != nil {
			return err
		}
	}
	c.Manager.Start()
	c.onStop(c.Manager.Stop) // đăng ký SAU broker nên dừng TRƯỚC: hủy các lần giao đang chạy rồi mới chờ handler của broker
	return c.Broker.Start(ctx)
}

func autoDisableTriggers(in []config.AutoDisableTrigger) []fhirsub.Trigger {
	out := make([]fhirsub.Trigger, len(in))
	for i, t := range in {
		out[i] = fhirsub.Trigger(t)
	}
	return out
}

// BeginShutdown đặt readiness=503 để orchestrator gỡ instance khỏi danh sách trước khi dừng nhận việc.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Stop đóng tài nguyên theo thứ tự ngược với lúc mở: Manager (hủy lần giao đang chạy, thông báo dở dang ở lại PEL cho nơi khác), broker, database.
func (c *Container) Stop(ctx context.Context) error {
	var errs []error
	for i := len(c.closers) - 1; i >= 0; i-- {
		errs = append(errs, c.closers[i](ctx))
	}
	c.closers = nil
	return errors.Join(errs...)
}

// ready kiểm phụ thuộc chính (database, Redis) trong hạn READINESS_TIMEOUT; đang tắt thì không sẵn sàng.
func (c *Container) ready(ctx context.Context) (bool, map[string]bool) {
	if c.draining.Load() {
		return false, map[string]bool{"draining": true}
	}
	ctx, cancel := context.WithTimeout(ctx, c.Settings.ReadinessTimeout)
	defer cancel()
	dbOK := c.Store.Ping(ctx) == nil
	brokerOK := c.Broker.Ping(ctx) == nil
	return dbOK && brokerOK, map[string]bool{"database": dbOK, "broker": brokerOK}
}

// Handler dựng handler HTTP (chỉ probe).
func (c *Container) Handler() http.Handler { return httpapi.New(httpapi.Deps{Ready: c.ready}) }

// ServeOptions là các móc nối của composition root (main hoặc test).
type ServeOptions struct {
	Flush    func() // flush telemetry sau khi server đã tắt (nil = không)
	OnListen func(addr net.Addr)
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
	if s.WebhookAllowPrivate {
		slog.Warn("FHIR_WEBHOOK_ALLOW_PRIVATE=true: chống SSRF bị tắt, service có thể giao tới địa chỉ nội bộ; chỉ dùng cho dev")
	}
	slog.Info("fhir-notifier đang nghe sự kiện ghi resource", "group", s.BrokerGroup, "websocket", s.WSMode == config.WSModeIssuer)
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: c.Handler(), ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}
