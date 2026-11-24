// Package bootstrap là composition root của fhir-server: nơi DUY NHẤT lắp ráp adapter cụ thể
// (kết nối DB, fhirstore, fhirrouter, xác thực) thành HTTP handler và quản lý vòng đời.
//
// fhir-server không có domain/application riêng: toàn bộ nghiệp vụ FHIR nằm trong các thư viện dùng chung
// (pkg/fhirrouter, pkg/fhirstore, pkg/fhirsearch, pkg/fhirvalidate) nên app chỉ còn cấu hình, xác thực và vận hành.
package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/apps/fhir-server/internal/presentation/httpapi"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirbinary"
	"trivita-fhir/pkg/fhirdownload"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/httpserve"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/ratelimit"
	"trivita-fhir/pkg/workflow"
)

// Container giữ mọi adapter đã lắp và vòng đời của chúng.
type Container struct {
	Settings config.Settings
	DB       *database.DB
	Store    *fhirstore.Store
	Router   *fhirrouter.Router
	Auth     *httpapi.Authenticator
	Broker   messaging.Broker // nil khi FHIR_SUBSCRIPTIONS=false
	Manager  *fhirsub.Manager // nil khi FHIR_SUBSCRIPTIONS=false
	Hub      *fhirsub.Hub     // nil trừ khi FHIR_WS_MODE=embedded
	Tokens   *fhirsub.Tokens  // bộ cấp token bind websocket; nil khi FHIR_WS_MODE=off

	storage *storage // nil khi FHIR_BINARY_STORAGE rỗng
	// routerRepo là repository của người gọi (đã bọc Wrap Binary, sự kiện và tách project) mà Router và handler Binary thô dùng.
	routerRepo fhirrouter.Repository

	draining atomic.Bool
	closers  []func(context.Context) error
}

func (c *Container) onStop(f func(context.Context) error) { c.closers = append(c.closers, f) }

func toHTTPTokens(ts []config.Token) []httpapi.Token {
	out := make([]httpapi.Token, len(ts))
	for i, t := range ts {
		out[i] = httpapi.Token{Name: t.Name, Secret: t.Secret}
	}
	return out
}

// New mở DB, áp dụng migration (hoặc từ chối khởi động nếu còn migration chưa chạy) và dựng router FHIR.
// Lỗi giữa chừng thì đóng những gì đã mở.
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
	} else if c.DB.Dialect == database.Postgres && s.IsProd() {
		slog.Warn("DATABASE_REQUIRE_SCHEMA chưa bật: nếu chung database với service khác, hãy đặt schema riêng cho từng service và bật cờ này (docs/plans/microservices-shared-db.md)")
	}

	if c.Store, err = fhirstore.New(c.DB); err != nil {
		return err
	}
	if s.DatabaseAutoMigrate || c.DB.IsInMemory() {
		if err := c.Store.Migrate(ctx); err != nil {
			return err
		}
	} else {
		n, err := c.Store.Pending(ctx)
		if err != nil {
			return err
		}
		if n > 0 { // chạy nhiều replica: không khởi động trên schema cũ
			return errors.New("schema chưa được migrate: chạy `fhir-server migrate up` hoặc bật DATABASE_AUTO_MIGRATE")
		}
	}

	// Repository dùng cho router: bọc bằng bộ phát sự kiện nếu bật Subscription. Manager đọc Subscription từ
	// store GỐC (không bọc) để tránh vòng lặp.
	var repo fhirrouter.Repository = c.Store
	if s.Subscriptions || s.AutoDownload { // broker cũng phục vụ worker tự tải attachment
		if c.Broker, err = messaging.New(messaging.Config{Driver: s.BrokerDriver, RedisURL: s.RedisURL}); err != nil {
			return err
		}
		c.onStop(c.Broker.Stop)
	}
	if s.Subscriptions {
		triggers, err := s.AutoDisableTriggers()
		if err != nil {
			return err
		}
		var failures fhirsub.FailureTracker // nil: đếm trong bộ nhớ (một replica)
		if rb, ok := c.Broker.(*messaging.Redis); ok {
			failures = fhirsub.NewRedisFailureTracker(rb.Client(), "", nil) // các replica cộng dồn chung
		}
		c.Manager = fhirsub.NewManager(c.Store, fhirsub.Config{
			Workers: s.WebhookWorkers, MaxAttempts: s.WebhookMaxAttempts, Timeout: s.WebhookTimeout, AllowPrivate: s.WebhookAllowPrivate,
			AutoDisable: autoDisableTriggers(triggers), Failures: failures,
		})
		c.Manager.Register(c.Broker)
		switch s.WSModeResolved() {
		case config.WSModeEmbedded:
			bc, ok := c.Broker.(messaging.Broadcaster)
			if !ok {
				return errors.New("FHIR_WS_MODE=embedded: broker không hỗ trợ broadcast")
			}
			if c.Hub, err = newHub(s); err != nil {
				return err
			}
			c.Tokens = c.Hub.Tokens()
			c.Manager.AttachHub(c.Hub)
			c.Manager.RegisterBroadcast(bc)
		case config.WSModeIssuer:
			// Kết nối do ws-gateway giữ: ở đây chỉ cấp token bind và khớp sự kiện (một lần cho cả cụm, qua handler nhóm), rồi
			// phát lên kênh của Subscription nếu có gateway đang nghe. Không RegisterBroadcast (sẽ khớp lặp ở mọi instance).
			ch, okCh := c.Broker.(messaging.Channels)
			pr, okPr := c.Broker.(messaging.Presence)
			if !okCh || !okPr {
				return errors.New("FHIR_WS_MODE=issuer: broker không hỗ trợ kênh động và sổ hiện diện (cần BROKER_DRIVER=redis)")
			}
			if err := c.Manager.AttachRemote(ch, pr); err != nil {
				return err
			}
			if c.Tokens, err = newTokens(s); err != nil {
				return err
			}
		}
	}
	if c.Broker != nil {
		repo = fhirsub.Wrap(c.Store, c.Broker)
	}

	// JWT: tách dữ liệu theo project; job nền chạy ngoài yêu cầu HTTP (không có người gọi) nên dùng repo không tách.
	var verifier *fhirauth.Verifier
	routerRepo, jobRepo := repo, repo
	// Kho Binary: nội dung Binary ghi qua router đi vào kho (Wrap nằm DƯỚI sự kiện Subscription để sự kiện không mang `data`). Job nền
	// (bulk export) giữ repo không bọc Binary: file kết quả lớn hơn trần base64 và vẫn nằm trong CSDL như trước.
	if s.StorageEnabled() {
		if c.storage, err = newStorage(s); err != nil {
			return err
		}
		c.onStop(func(context.Context) error { return c.storage.close() })
		var inner fhirrouter.Repository = fhirbinary.Wrap(c.Store, c.storage.store, fhirbinary.WrapOptions{Base64MaxBytes: int64(s.Base64BinaryMaxBytes), Rewrite: fhirbinary.NewRewriter(rewriteConfig(s), nil, nil)})
		if c.Broker != nil {
			inner = fhirsub.Wrap(inner, c.Broker)
		}
		routerRepo = inner
	}
	if s.AutoDownload {
		maxBytes := int64(s.AutoDownloadMaxBytes)
		if maxBytes == 0 {
			maxBytes = int64(s.StorageMaxUploadBytes)
		}
		w, err := fhirdownload.New(fhirdownload.Config{
			Repo: repo, BinaryRepo: c.Store, Blobs: c.storage.store, MaxBytes: maxBytes, Timeout: s.AutoDownloadTimeout, AllowPrivate: s.AutoDownloadAllowPrivate,
			OwnPrefixes: []string{strings.TrimRight(s.BaseURL, "/") + "/", s.StoragePublicURL()},
			Allowed:     config.AutoDownloadPrefixes(s.AutoDownloadAllowedPrefixes), Ignored: config.AutoDownloadPrefixes(s.AutoDownloadIgnoredPrefixes),
			Async: s.BrokerDriver != "redis", // broker trong bộ nhớ giao đồng bộ trong yêu cầu ghi: việc tải ngoài phải chạy nền; Redis thử lại và dead-letter theo broker
		})
		if err != nil {
			return err
		}
		w.Register(c.Broker)
		c.onStop(w.Shutdown)
	}
	if c.Broker != nil {
		if err := c.Broker.Start(ctx); err != nil {
			return err
		}
		if c.Manager != nil {
			c.Manager.Start()
			c.onStop(c.Manager.Stop)
		}
		if c.Hub != nil {
			c.onStop(c.Hub.Close) // đăng ký sau Broker nên dừng trước: ngắt websocket (GoingAway) trước khi đóng broker
		}
	}
	var scopedJobRepo fhirrouter.Repository
	var jobContext func(context.Context, *fhir.AsyncJob) context.Context
	var sealer *fhirauth.Sealer
	if s.JWTEnabled() {
		if verifier, err = fhirauth.NewVerifier(fhirauth.Config{
			Issuer: s.JWTIssuer, Audience: s.JWTAudience, JWKSURL: s.JWKSURL, HMACSecret: []byte(s.JWTHMACSecret),
			ProjectClaim: s.JWTProjectClaim, ScopeClaim: s.JWTScopeClaim, PolicyClaim: s.JWTPolicyClaim, AllowSuperAdmin: s.JWTSuperAdmin,
		}); err != nil {
			return err
		}
		key := []byte(s.JobSigningKey)
		if len(key) == 0 { // dev HMAC: suy khóa ký job từ khóa JWT (config đã bắt buộc khóa riêng khi dùng JWKS)
			sum := sha256.Sum256([]byte("fhir-job-seal|" + s.JWTHMACSecret))
			key = sum[:]
		}
		if sealer, err = fhirauth.NewSealer(key); err != nil {
			return err
		}
		// job như bulk export chạy với quyền của người gọi: dựng lại Principal từ project đã gắn vào AsyncJob
		scopedJobRepo = fhirauth.Scope(repo)
		routerRepo = fhirauth.Scope(routerRepo)
		jobContext = func(ctx context.Context, job *fhir.AsyncJob) context.Context {
			project := ""
			if job.Meta != nil {
				project = job.Meta.Project
			}
			return fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "job:" + job.ID, Project: project, Role: fhirauth.RoleRead})
		}
	}

	c.routerRepo = routerRepo
	var responseHook func(context.Context, fhir.Resource) fhir.Resource
	var binaryContent func(context.Context, *fhir.Binary) ([]byte, error)
	if c.storage != nil {
		binaryContent = fhirbinary.ReadContent(c.storage.store, 64<<20) // PackageRelease/$install đọc gói từ kho đối tượng // đọc: tham chiếu Binary thành URL ký theo quyền của người gọi (repo đã tách project)
		responseHook = fhirbinary.NewRewriter(rewriteConfig(s), routerRepo, c.storage.signer).Response
	}
	c.Router = fhirrouter.New(routerRepo, fhirrouter.Options{
		ResponseHook:  responseHook,
		BinaryContent: binaryContent,
		JobRepository: jobRepo, ScopedJobRepository: scopedJobRepo, JobContext: jobContext,
		JobSeal: jobSeal(sealer), JobOpen: jobOpen(sealer),
		JobLease: s.JobLease, JobMaintenanceInterval: s.JobMaintenanceInterval, ExportRetention: s.ExportRetention,
		AsyncBatch: s.AsyncBatch, BatchCheckpointEntries: s.BatchCheckpointEntries,
		BaseURL:         strings.TrimRight(s.BaseURL, "/"),
		SkipValidation:  s.SkipValidation,
		SkipConstraints: s.SkipConstraints, UpdateAsCreate: s.UpdateAsCreate,
		NewOrchestrator: func(run workflow.JobRunner) workflow.JobOrchestrator {
			return workflow.NewInline(run, s.JobConcurrency)
		},
	})
	if c.Tokens != nil {
		for _, m := range []string{"GET", "POST"} {
			c.Router.Add(m, "Subscription/:id/$get-ws-binding-token", c.Tokens.TokenOperation(routerRepo))
		}
	}
	if c.storage != nil {
		c.Router.Add("GET", "Binary/:id/$presigned-url", fhirbinary.PresignedURL(fhirbinary.PresignConfig{
			Repo: routerRepo, Signer: c.storage.signer, CanUpdate: canUpdate(s.AuthDisabled)}))
	}
	c.onStop(c.Router.Shutdown) // đăng ký sau cùng nên dừng đầu tiên: chờ job nền đang chạy trước khi đóng broker và DB

	if verifier != nil {
		c.Auth = httpapi.NewJWTAuthenticator(verifier)
		return nil
	}
	read, err := config.ParseTokens(s.ReadTokens)
	if err != nil {
		return err
	}
	write, err := config.ParseTokens(s.WriteTokens)
	if err != nil {
		return err
	}
	admin, err := config.ParseTokens(s.AdminTokens)
	if err != nil {
		return err
	}
	c.Auth = httpapi.NewAuthenticator(toHTTPTokens(read), toHTTPTokens(write), toHTTPTokens(admin), s.AuthDisabled)
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

// BeginShutdown được gọi khi bắt đầu tắt: /readyz trả 503 để K8s ngừng gửi traffic mới.
func (c *Container) BeginShutdown() { c.draining.Store(true) }

// Readiness kiểm tra DB với timeout READINESS_TIMEOUT.
func (c *Container) Readiness(ctx context.Context) (bool, map[string]bool) {
	pctx, cancel := context.WithTimeout(ctx, c.Settings.ReadinessTimeout)
	defer cancel()
	dbOK := true
	if err := c.Store.Ping(pctx); err != nil {
		slog.WarnContext(ctx, "readiness check lỗi", "check", "database", "error", err.Error())
		dbOK = false
	}
	accepting := !c.draining.Load()
	checks := map[string]bool{"accepting_traffic": accepting, "database": dbOK}
	ready := dbOK && accepting
	if c.Broker != nil {
		brokerOK := c.Broker.Ping(pctx) == nil
		checks["broker"] = brokerOK
		ready = ready && brokerOK
	}
	return ready, checks
}

// Handler dựng HTTP handler. instrument (có thể nil) gắn observability trước mọi middleware khác.
func (c *Container) Handler(instrument func(*echo.Echo) error) (*echo.Echo, error) {
	nets, err := c.Settings.TrustedProxyNets()
	if err != nil {
		return nil, err
	}
	origins, err := c.Settings.AllowedOriginList()
	if err != nil {
		return nil, err
	}
	var fhirHandler http.Handler = c.Router.HTTPHandler()
	var storageHandler http.Handler
	if c.storage != nil {
		max := int64(c.Settings.StorageMaxUploadBytes)
		fhirHandler = fhirbinary.RawBinary(fhirbinary.RawConfig{FHIR: fhirHandler, Repo: c.routerRepo, Store: c.storage.store, Signer: c.storage.signer, MaxUploadBytes: max})
		// /storage đọc Binary bằng repo KHÔNG tách project (quyền do chữ ký): c.Store gốc, không qua Scope.
		storageHandler = fhirbinary.StorageHandler(fhirbinary.StorageConfig{Signer: c.storage.signer, Store: c.storage.store, Repo: c.Store, MaxUploadBytes: max})
	}
	dep := httpapi.Deps{
		FHIR: fhirHandler, Storage: storageHandler, Auth: c.Auth, Ready: c.Readiness, Instrument: instrument, Debug: c.Settings.Debug,
		BaseURL: c.Settings.BaseURL, AllowedOrigins: origins, RateLimit: c.Settings.RateLimit, RateBurst: c.Settings.RateBurst,
	}
	if c.Settings.RateLimitShared {
		cli, err := ratelimit.NewRedisClient(c.Settings.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("REDIS_URL: %w", err)
		}
		c.onStop(func(context.Context) error { return cli.Close() })
		dep.NewRateStore = func(name string, rps float64, burst int) ratelimit.Store {
			return ratelimit.NewRedis(cli, "fhir:rl:"+name+":", rps, burst)
		}
	}
	if len(nets) > 0 {
		opts := []echo.TrustOption{echo.TrustLoopback(false), echo.TrustLinkLocal(false), echo.TrustPrivateNet(false)}
		for _, n := range nets {
			opts = append(opts, echo.TrustIPRange(n))
		}
		dep.IPExtractor = echo.ExtractIPFromXFFHeader(opts...)
	}
	if c.Hub != nil {
		dep.WebSocket = c.Hub.Handler()
	}
	return httpapi.New(dep)
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
	if err := s.ValidateForServe(); err != nil {
		return err
	}
	c, err := New(ctx, s)
	if err != nil {
		return err
	}
	e, err := c.Handler(opts.Instrument)
	if err != nil {
		_ = c.Stop(context.Background())
		return err
	}
	if s.AuthDisabled {
		slog.Warn("FHIR_AUTH_DISABLED=true: mọi endpoint (kể cả ghi dữ liệu) không cần xác thực — chỉ dùng cho dev")
	}
	if s.RateLimit == 0 && (s.Environment == "production" || s.Environment == "staging") {
		slog.Warn("FHIR_RATE_LIMIT=0: không giới hạn tần suất yêu cầu; đặt hạn mức theo IP hoặc giới hạn ở gateway trước khi mở ra Internet")
	}
	if s.SkipValidation {
		slog.Warn("FHIR_SKIP_VALIDATION=true: dữ liệu ghi vào không được kiểm tra")
	}
	return httpserve.Run(ctx, httpserve.Options{
		Addr: s.Addr, Handler: e, ReadHeaderTimeout: s.ReadHeaderTimeout,
		DrainDelay: s.ShutdownDrainDelay, ShutdownTimeout: s.ShutdownTimeout,
		BeginShutdown: c.BeginShutdown, Stop: c.Stop, OnListen: opts.OnListen,
	})
}

// jobSeal và jobOpen ký và kiểm quyền của người gọi trên AsyncJob (chỉ khi bật JWT), để job nền chạy đúng quyền đó.
func jobSeal(sl *fhirauth.Sealer) func(context.Context, string, string) (string, error) {
	if sl == nil {
		return nil
	}
	return func(ctx context.Context, id, request string) (string, error) {
		p, _ := fhirauth.PrincipalFrom(ctx)
		return sl.Seal(id, request, p), nil
	}
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

// newTokens dựng bộ cấp token bind cho chế độ issuer: khóa bắt buộc (đã kiểm ở cấu hình, phải trùng ws-gateway) và websocket-url là
// địa chỉ công khai của ws-gateway.
func newTokens(s config.Settings) (*fhirsub.Tokens, error) {
	var previous [][]byte
	for _, k := range s.PreviousWSKeys() {
		previous = append(previous, []byte(k))
	}
	return fhirsub.NewTokens(fhirsub.TokenConfig{SigningKey: []byte(s.WSSigningKey), Previous: previous, URL: s.WSURL})
}

// newHub dựng Hub websocket từ cấu hình. Không có FHIR_WS_SIGNING_KEY (chỉ cho phép ở dev, một instance) thì dùng khóa
// ngẫu nhiên của process: token chỉ có giá trị trên instance đã cấp.
func newHub(s config.Settings) (*fhirsub.Hub, error) {
	key := []byte(s.WSSigningKey)
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		slog.Warn("FHIR_WS_SIGNING_KEY chưa đặt: dùng khóa ngẫu nhiên, chỉ chạy được một instance")
	}
	wsURL := s.WSURL
	if wsURL == "" {
		if u, err := url.Parse(s.BaseURL); err == nil && u.Host != "" {
			scheme := "ws"
			if u.Scheme == "https" {
				scheme = "wss"
			}
			wsURL = scheme + "://" + u.Host + httpapi.WebSocketPath
		}
	}
	var origins []string
	for _, o := range strings.Split(s.WSAllowedOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	var previous [][]byte
	for _, k := range s.PreviousWSKeys() {
		previous = append(previous, []byte(k))
	}
	return fhirsub.NewHub(fhirsub.WSConfig{SigningKey: key, PreviousKeys: previous, URL: wsURL, MaxConns: s.WSMaxConnections, MaxBindingsPerUser: s.WSMaxBindingsPerUser, Heartbeat: s.WSHeartbeat, OriginPatterns: origins})
}

// rewriteConfig là hai gốc công khai để nhận ra URL Binary khi viết lại tham chiếu.
func rewriteConfig(s config.Settings) fhirbinary.RewriteConfig {
	return fhirbinary.RewriteConfig{FHIRBaseURL: strings.TrimRight(s.BaseURL, "/"), StorageBaseURL: s.StoragePublicURL()}
}

// canUpdate quyết định có cấp URL tải lên (quyền ghi gián tiếp): theo Principal của người gọi; không có Principal chỉ khi xác thực bị tắt (dev).
func canUpdate(authDisabled bool) func(context.Context, string) bool {
	return func(ctx context.Context, rt string) bool {
		if _, ok := fhirauth.PrincipalFrom(ctx); !ok {
			return authDisabled
		}
		return fhirauth.CanUpdate(ctx, rt)
	}
}

func autoDisableTriggers(in []config.AutoDisableTrigger) []fhirsub.Trigger {
	out := make([]fhirsub.Trigger, len(in))
	for i, t := range in {
		out[i] = fhirsub.Trigger(t)
	}
	return out
}
