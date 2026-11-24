// Package bootstrap là composition root của fhir-downloader: nơi DUY NHẤT lắp ráp adapter cụ thể (kết nối DB, fhirstore, kho Binary, broker Redis) thành
// worker tải attachment và quản lý vòng đời.
//
// fhir-downloader không có domain/application riêng: nghiệp vụ tải nằm ở pkg/fhirdownload (dùng chung với fhir-server khi chạy cùng tiến trình); dữ liệu là
// resource FHIR ở schema `fhir` của fhir-server. App chỉ còn cấu hình, lắp ráp và vận hành.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"

	"trivita-fhir/apps/fhir-downloader/internal/config"
	"trivita-fhir/apps/fhir-downloader/internal/presentation/httpapi"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/s3store"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirdownload"
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
	Broker   messaging.Broker
	// Blobs là kho nội dung tệp tải về (trùng với kho Binary của fhir-server).
	Blobs  binarystore.Store
	Worker *fhirdownload.Worker

	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

// New mở DB (từ chối khởi động nếu schema chưa migrate), kho Binary, broker và bắt đầu nghe sự kiện. Lỗi giữa chừng thì đóng những gì đã mở.
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
		return errors.New("schema chưa được migrate: chạy `fhir-server migrate up` (fhir-downloader dùng schema của fhir-server và không tự migrate)")
	}

	var closeBlobs func() error
	if c.Blobs, closeBlobs, err = openStore(s); err != nil {
		return err
	}
	if closeBlobs != nil {
		c.onStop(func(context.Context) error { return closeBlobs() })
	}

	if c.Broker, err = messaging.New(messaging.Config{Driver: "redis", RedisURL: s.RedisURL, Group: s.BrokerGroup}); err != nil {
		return err
	}
	c.onStop(c.Broker.Stop)

	// Repo ghi resource nguồn qua kho phát sự kiện để fhir-notifier và Subscription thấy lần cập nhật attachment; Binary tạo qua kho GỐC (không phát sự kiện).
	// Redis giao at-least-once, lỗi tạm trả về cho broker thử lại và đưa vào dead-letter, nên worker chạy đồng bộ trong handler (Async=false).
	if c.Worker, err = fhirdownload.New(fhirdownload.Config{
		Repo: fhirsub.Wrap(c.Store, c.Broker), BinaryRepo: c.Store, Blobs: c.Blobs,
		MaxBytes: s.MaxBytes(), Timeout: s.AutoDownloadTimeout, AllowPrivate: s.AutoDownloadAllowPrivate,
		OwnPrefixes: s.OwnPrefixes(),
		Allowed:     config.Prefixes(s.AutoDownloadAllowedPrefixes), Ignored: config.Prefixes(s.AutoDownloadIgnoredPrefixes),
	}); err != nil {
		return err
	}
	c.Worker.Register(c.Broker)
	c.onStop(c.Worker.Shutdown)
	return c.Broker.Start(ctx)
}

// openStore mở kho theo FHIR_BINARY_STORAGE: file:<thư mục> hoặc s3:<bucket>[/<prefix>].
func openStore(s config.Settings) (binarystore.Store, func() error, error) {
	if s.StorageKind() == "s3" {
		st, err := s3store.New(s3store.Config{
			Bucket: s.S3Bucket(), Prefix: s.S3Prefix(), Region: s.S3Region, Endpoint: s.S3Endpoint,
			AccessKeyID: s.S3AccessKeyID, SecretAccessKey: s.S3SecretAccessKey, SessionToken: s.S3SessionToken, ServerSideEncryption: s.S3SSE,
		})
		return st, nil, err
	}
	fs, err := binarystore.NewFS(s.StorageDir())
	if err != nil {
		return nil, nil, err
	}
	return fs, fs.Close, nil
}

// BeginShutdown đặt readiness=503 để orchestrator gỡ instance khỏi danh sách trước khi dừng nhận việc.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Stop đóng tài nguyên theo thứ tự ngược với lúc mở: worker (chờ việc đang tải), broker (ngừng nhận, PEL còn lại cho nơi khác), kho, database.
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
	if s.AutoDownloadAllowPrivate {
		slog.Warn("FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE=true: chống SSRF bị tắt, service có thể tải từ địa chỉ nội bộ; chỉ dùng cho dev")
	}
	slog.Info("fhir-downloader đang nghe sự kiện ghi resource", "group", s.BrokerGroup)
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: c.Handler(), ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}
