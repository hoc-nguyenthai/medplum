// Package bootstrap là composition root của dicomweb: nơi DUY NHẤT lắp ráp adapter cụ thể (kết nối DB, fhirstore, kho Binary, xác thực) thành
// HTTP handler và quản lý vòng đời.
//
// dicomweb không có domain/application riêng: nghiệp vụ DICOM (đọc, chuyển đổi, tách frame) sẽ nằm ở pkg/dicomweb (D2 trở đi), còn dữ liệu là
// resource FHIR ở schema `fhir` của fhir-server. App chỉ còn cấu hình, xác thực và vận hành.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/apps/dicomweb/internal/presentation/httpapi"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/s3store"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/httpserve"
)

// Container giữ mọi adapter đã lắp và vòng đời của chúng.
type Container struct {
	Settings config.Settings
	DB       *database.DB
	Store    *fhirstore.Store
	// Repo là kho resource FHIR đã tách theo project (fhirauth.Scope): mọi truy cập DicomStudy, DicomSeries, DicomInstance và Binary của người gọi đi qua đây.
	Repo fhirrouter.Repository
	// Blobs là kho nội dung DICOM (trùng với kho Binary của fhir-server).
	Blobs    binarystore.Store
	Verifier *fhirauth.Verifier

	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

// New mở DB (từ chối khởi động nếu schema chưa migrate), kho Binary và bộ kiểm JWT. Lỗi giữa chừng thì đóng những gì đã mở.
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
	// dicomweb dùng schema của fhir-server nên không tự migrate: chưa migrate thì từ chối khởi động (không dựng schema ngoài ý muốn)
	n, err := c.Store.Pending(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("schema chưa được migrate: chạy `fhir-server migrate up` (dicomweb dùng schema của fhir-server và không tự migrate)")
	}

	var closeBlobs func() error
	if c.Blobs, closeBlobs, err = openStore(s); err != nil {
		return err
	}
	if closeBlobs != nil {
		c.onStop(func(context.Context) error { return closeBlobs() })
	}

	if c.Verifier, err = fhirauth.NewVerifier(fhirauth.Config{
		Issuer: s.JWTIssuer, Audience: s.JWTAudience, JWKSURL: s.JWKSURL, HMACSecret: []byte(s.JWTHMACSecret),
		ProjectClaim: s.JWTProjectClaim, ScopeClaim: s.JWTScopeClaim, PolicyClaim: s.JWTPolicyClaim, AllowSuperAdmin: s.JWTSuperAdmin,
	}); err != nil {
		return err
	}
	c.Repo = fhirauth.Scope(c.Store)
	return nil
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

// BeginShutdown đặt readiness=503 để orchestrator gỡ instance khỏi tải trước khi đóng cổng.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Stop đóng tài nguyên theo thứ tự ngược với lúc mở.
func (c *Container) Stop(ctx context.Context) error {
	var errs []error
	for i := len(c.closers) - 1; i >= 0; i-- {
		errs = append(errs, c.closers[i](ctx))
	}
	c.closers = nil
	return errors.Join(errs...)
}

// ready kiểm phụ thuộc chính (database) trong hạn READINESS_TIMEOUT; đang tắt thì không sẵn sàng.
func (c *Container) ready(ctx context.Context) (bool, map[string]bool) {
	if c.draining.Load() {
		return false, map[string]bool{"draining": true}
	}
	ctx, cancel := context.WithTimeout(ctx, c.Settings.ReadinessTimeout)
	defer cancel()
	dbOK := c.Store.Ping(ctx) == nil
	return dbOK, map[string]bool{"database": dbOK}
}

// Handler dựng Echo từ cấu hình và adapter.
func (c *Container) Handler(instrument func(*echo.Echo) error) (*echo.Echo, error) {
	s := c.Settings
	origins, err := s.AllowedOriginList()
	if err != nil {
		return nil, err
	}
	nets, err := s.TrustedProxyNets()
	if err != nil {
		return nil, err
	}
	ipx := echo.ExtractIPDirect()
	if len(nets) > 0 {
		var opts []echo.TrustOption
		opts = append(opts, echo.TrustLoopback(false), echo.TrustLinkLocal(false), echo.TrustPrivateNet(false))
		for _, n := range nets {
			opts = append(opts, echo.TrustIPRange(n))
		}
		ipx = echo.ExtractIPFromXFFHeader(opts...)
	}
	return httpapi.New(httpapi.Deps{
		Verifier: c.Verifier, Ready: c.ready, Instrument: instrument, Debug: s.Debug, BaseURL: s.BaseURL,
		AllowedOrigins: origins, IPExtractor: ipx, RateLimit: s.RateLimit, RateBurst: s.RateBurst, MaxRequestBytes: int64(s.MaxRequestBytes),
	})
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
	if s.RateLimit == 0 && s.IsProd() {
		slog.Warn("DICOMWEB_RATE_LIMIT=0: không giới hạn tần suất yêu cầu; đặt hạn mức theo IP hoặc giới hạn ở gateway trước khi mở ra Internet")
	}
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: e, ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}

var _ http.Handler = (*echo.Echo)(nil)
