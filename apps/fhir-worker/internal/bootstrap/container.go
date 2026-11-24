// Package bootstrap là composition root của fhir-worker: nơi DUY NHẤT lắp ráp adapter cụ thể (kết nối DB, fhirstore, kho Binary, broker phát sự kiện) thành bộ chạy job nền
// (pkg/fhirrouter: bulk export, `$reindex` cả loại, batch async, vòng bảo trì job) và quản lý vòng đời.
//
// fhir-worker không có domain/application riêng: nghiệp vụ job nằm ở pkg/fhirrouter (dùng chung với fhir-server khi chạy cùng tiến trình); dữ liệu là resource FHIR ở schema `fhir`
// của fhir-server (AsyncJob do fhir-server tạo, fhir-worker nhận qua vòng bảo trì và lease). App chỉ còn cấu hình, lắp ráp và vận hành.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"trivita-fhir/apps/fhir-worker/internal/config"
	"trivita-fhir/apps/fhir-worker/internal/presentation/httpapi"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/s3store"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirbinary"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/httpserve"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/workflow"
)

// Container giữ mọi adapter đã lắp và vòng đời của chúng.
type Container struct {
	Settings config.Settings
	DB       *database.DB
	Store    *fhirstore.Store
	// Broker chỉ để PHÁT sự kiện ghi resource (batch async ghi resource); nil khi không bật Subscription và tự tải.
	Broker messaging.Broker
	// Router chứa các hàm job (export, reindex, batch) và vòng bảo trì nhận job; không có route HTTP nào được phục vụ.
	Router *fhirrouter.Router

	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

// New mở DB (từ chối khởi động nếu schema chưa migrate), kho Binary, broker và bắt đầu nhận job. Lỗi giữa chừng thì đóng những gì đã mở.
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
		return errors.New("schema chưa được migrate: chạy `fhir-server migrate up` (fhir-worker dùng schema của fhir-server và không tự migrate)")
	}

	// Kho gốc, bọc bộ phát sự kiện nếu fhir-server phát (Subscription hoặc tự tải): batch async ghi resource nên sự kiện phải đến fhir-notifier và fhir-downloader.
	var repo fhirrouter.Repository = c.Store
	if s.Publishes() {
		if c.Broker, err = messaging.New(messaging.Config{Driver: "redis", RedisURL: s.RedisURL}); err != nil {
			return err
		}
		c.onStop(c.Broker.Stop) // chỉ Publish, không Start: không nghe gì
		repo = fhirsub.Wrap(c.Store, c.Broker)
	}
	routerRepo, jobRepo := repo, repo
	var binaryContent func(context.Context, *fhir.Binary) ([]byte, error)
	if s.StorageEnabled() {
		blobs, closeBlobs, err := openStore(s)
		if err != nil {
			return err
		}
		if closeBlobs != nil {
			c.onStop(func(context.Context) error { return closeBlobs() })
		}
		rw := fhirbinary.NewRewriter(fhirbinary.RewriteConfig{FHIRBaseURL: strings.TrimRight(s.BaseURL, "/"), StorageBaseURL: s.StoragePublicURL()}, nil, nil)
		// Giống fhir-server: Binary ghi qua entry của batch đi vào kho (có trần base64); kết quả job (file export, đầu vào và đoạn kết quả batch) đi vào kho KHÔNG trần. Wrap nằm DƯỚI sự kiện.
		var inner fhirrouter.Repository = fhirbinary.Wrap(c.Store, blobs, fhirbinary.WrapOptions{Base64MaxBytes: int64(s.Base64BinaryMaxBytes), Rewrite: rw})
		var jobInner fhirrouter.Repository = fhirbinary.Wrap(c.Store, blobs, fhirbinary.WrapOptions{Base64MaxBytes: -1, Rewrite: rw})
		if c.Broker != nil {
			inner, jobInner = fhirsub.Wrap(inner, c.Broker), fhirsub.Wrap(jobInner, c.Broker)
		}
		routerRepo, jobRepo = inner, jobInner
		binaryContent = fhirbinary.ReadContent(blobs, 64<<20) // đọc Bundle đầu vào và đoạn kết quả của batch async đã ra kho
	}

	// Job mang chữ ký quyền của người gọi khi fhir-server bật JWT: chỉ chạy khi chữ ký hợp lệ, bằng đúng quyền và project đã ký (giống fhir-server).
	var scopedJobRepo fhirrouter.Repository
	var jobContext func(context.Context, *fhir.AsyncJob) context.Context
	var sealer *fhirauth.Sealer
	if s.JWTEnabled() {
		if sealer, err = fhirauth.NewSealer(s.JobKey()); err != nil {
			return err
		}
		scopedJobRepo = fhirauth.Scope(jobRepo)
		routerRepo = fhirauth.Scope(routerRepo)
		jobContext = func(ctx context.Context, job *fhir.AsyncJob) context.Context {
			project := ""
			if job.Meta != nil {
				project = job.Meta.Project
			}
			return fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "job:" + job.ID, Project: project, Role: fhirauth.RoleRead})
		}
	}

	c.Router = fhirrouter.New(routerRepo, fhirrouter.Options{
		BinaryContent: binaryContent,
		JobRepository: jobRepo, ScopedJobRepository: scopedJobRepo, JobContext: jobContext, JobOpen: jobOpen(sealer),
		JobLease: s.JobLease, JobCancelPoll: s.JobCancelPoll, JobMaintenanceInterval: s.JobMaintenanceInterval, JobRecoveryGrace: s.JobRecoveryGrace, ExportRetention: s.ExportRetention,
		AsyncBatch: true, BatchCheckpointEntries: s.BatchCheckpointEntries,
		BaseURL:         strings.TrimRight(s.BaseURL, "/"),
		SkipValidation:  s.SkipValidation,
		SkipConstraints: s.SkipConstraints, UpdateAsCreate: s.UpdateAsCreate,
		NewOrchestrator: func(run workflow.JobRunner) workflow.JobOrchestrator {
			return workflow.NewInline(run, s.JobConcurrency)
		},
	})
	c.onStop(c.Router.Shutdown) // đăng ký sau cùng nên dừng đầu tiên: job đang chạy nhả lease cho nơi khác rồi mới đóng broker và DB
	return nil
}

func jobOpen(sl *fhirauth.Sealer) func(context.Context, *fhir.AsyncJob, string) (context.Context, error) {
	if sl == nil {
		return nil
	}
	return func(ctx context.Context, job *fhir.AsyncJob, sealed string) (context.Context, error) {
		project := ""
		if job.Meta != nil {
			project = job.Meta.Project
		}
		p, err := sl.Open(job.ID, project, job.Request, sealed)
		if err != nil {
			return nil, err
		}
		return fhirauth.WithPrincipal(ctx, p), nil
	}
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

// BeginShutdown đặt readiness=503 để orchestrator gỡ instance khỏi danh sách trước khi dừng nhận job.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Stop đóng tài nguyên theo thứ tự ngược với lúc mở: Router (job đang chạy nhả lease), broker, kho, database.
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
	slog.Info("fhir-worker đang nhận job nền (AsyncJob accepted)", "concurrency", s.JobConcurrency, "poll", s.JobMaintenanceInterval, "jwt", s.JWTEnabled(), "storage", s.StorageEnabled())
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: c.Handler(), ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}
