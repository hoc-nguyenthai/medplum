// Package httpapi là tầng presentation của fhir-server: Echo v4, xác thực bearer token, gắn router FHIR
// (pkg/fhirrouter) dưới /fhir/R4 và lỗi dạng OperationOutcome.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echoswagger "github.com/swaggo/echo-swagger"
	"golang.org/x/time/rate"

	_ "trivita-fhir/apps/fhir-server/docs" // đăng ký spec OpenAPI do `make swagger` sinh
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/httpsec"
)

// Prefix là gốc của API FHIR R4.
const Prefix = "/fhir/R4"

// StoragePath là gốc của route lưu trữ nội dung Binary bằng URL ký (giống Medplum).
const StoragePath = "/storage"

// ownsBodyLimit: yêu cầu mà handler Binary tự đặt trần kích thước (tải nội dung lớn): /storage và POST/PUT Binary.
func ownsBodyLimit(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, StoragePath+"/") {
		return true
	}
	return (r.Method == http.MethodPost || r.Method == http.MethodPut) && (p == Prefix+"/Binary" || strings.HasPrefix(p, Prefix+"/Binary/"))
}

// WebSocketPath là endpoint websocket của Subscription (giống Medplum). Không qua xác thực bearer: token nằm trong thông điệp bind.
const WebSocketPath = "/ws/subscriptions-r4"

// Deps là mọi thứ presentation cần; được lắp ở bootstrap.
type Deps struct {
	// FHIR là handler của fhirrouter (đường dẫn tính từ sau Prefix).
	FHIR http.Handler
	// WebSocket (có thể nil) là handler của WebSocketPath.
	WebSocket http.Handler
	// Storage (có thể nil) là handler GET/PUT /storage/* bằng URL ký (không Bearer; quyền do chữ ký). Có Storage thì POST/PUT Binary thô
	// và /storage không bị giới hạn 16M của BodyLimit chung (handler tự đặt trần riêng).
	Storage http.Handler
	Auth    *Authenticator
	Ready   func(ctx context.Context) (ready bool, checks map[string]bool)
	// Instrument gắn observability vào echo TRƯỚC mọi middleware khác để span bao trùm tất cả (có thể nil).
	Instrument func(e *echo.Echo) error
	Debug      bool
	// BaseURL quyết định có gửi HSTS không (chỉ khi https).
	BaseURL string
	// AllowedOrigins là origin được gọi API từ trình duyệt (CORS, chỉ dưới Prefix); rỗng: không origin nào.
	AllowedOrigins []string
	// IPExtractor xác định IP người gọi (hạn mức, log). Mặc định KHÔNG tin header proxy.
	IPExtractor echo.IPExtractor
	// RateLimit là số yêu cầu mỗi giây mỗi IP dưới Prefix (0: tắt), RateBurst là cỡ đợt (<=0: gấp đôi RateLimit, tối thiểu 1).
	RateLimit float64
	RateBurst int
}

// New dựng Echo với route, middleware và error handler.
func New(dep Deps) (*echo.Echo, error) {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.Debug = dep.Debug
	e.HTTPErrorHandler = errorHandler
	e.IPExtractor = dep.IPExtractor
	if e.IPExtractor == nil {
		e.IPExtractor = echo.ExtractIPDirect()
	}
	if dep.Instrument != nil {
		if err := dep.Instrument(e); err != nil {
			return nil, err
		}
	}
	e.Use(middleware.Recover(), middleware.RequestID())
	e.Use(echo.WrapMiddleware(httpsec.SecurityHeaders(httpsec.HeadersOptions{
		BaseURL: dep.BaseURL,
		// Swagger UI cần tải script và style nên CSP `default-src 'none'` làm hỏng nó.
		Skip: func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/swagger/") },
	})))
	prefixes := []string{Prefix + "/", Prefix}
	if dep.Storage != nil {
		prefixes = append(prefixes, StoragePath+"/")
	}
	e.Use(echo.WrapMiddleware(httpsec.CORS(httpsec.CORSOptions{AllowedOrigins: dep.AllowedOrigins, PathPrefixes: prefixes})))
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{Limit: "16M", Skipper: func(c echo.Context) bool { return dep.Storage != nil && ownsBodyLimit(c.Request()) }}))

	e.GET("/healthz", func(c echo.Context) error { return c.JSON(http.StatusOK, map[string]string{"status": "ok"}) })
	e.GET("/readyz", func(c echo.Context) error {
		ready, checks := dep.Ready(c.Request().Context())
		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, map[string]any{"ready": ready, "checks": checks})
	})

	// Swagger UI và spec (/swagger/index.html, /swagger/doc.json); công khai, chỉ mô tả API.
	e.GET("/swagger/*", echoswagger.WrapHandler)

	if dep.WebSocket != nil {
		e.GET(WebSocketPath, echo.WrapHandler(dep.WebSocket))
	}
	if dep.Storage != nil {
		// Công khai (chữ ký trong URL thay cho Bearer) nên có hạn mức theo IP nhưng không qua Guard.
		var smw []echo.MiddlewareFunc
		if dep.RateLimit > 0 {
			smw = append(smw, rateLimiter(dep.RateLimit, dep.RateBurst))
		}
		sh := echo.WrapHandler(dep.Storage)
		e.GET(StoragePath+"/*", sh, smw...)
		e.PUT(StoragePath+"/*", sh, smw...)
		// Phương thức khác cũng vào handler để nhận 405 kèm Allow thay vì 404 của router.
		e.POST(StoragePath+"/*", sh, smw...)
		e.DELETE(StoragePath+"/*", sh, smw...)
	}
	fhir := echo.WrapHandler(http.StripPrefix(Prefix, dep.FHIR))
	// Hạn mức đứng TRƯỚC xác thực để cả việc dò token cũng bị chặn; không áp cho health, swagger, websocket.
	mws := []echo.MiddlewareFunc{dep.Auth.Guard()}
	if dep.RateLimit > 0 {
		mws = append([]echo.MiddlewareFunc{rateLimiter(dep.RateLimit, dep.RateBurst)}, mws...)
	}
	g := e.Group(Prefix, mws...)
	g.Any("", fhir)
	g.Any("/*", fhir)
	return e, nil
}

// rateLimiter giới hạn theo IP (bộ nhớ trong một instance: nhiều replica thì mỗi replica đếm riêng).
func rateLimiter(rps float64, burst int) echo.MiddlewareFunc {
	if burst <= 0 {
		burst = int(2 * rps)
	}
	if burst < 1 {
		burst = 1
	}
	store := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
		Rate: rate.Limit(rps), Burst: burst, ExpiresIn: 3 * time.Minute,
	})
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store:               store,
		IdentifierExtractor: func(c echo.Context) (string, error) { return c.RealIP(), nil },
		ErrorHandler: func(c echo.Context, err error) error {
			return echo.NewHTTPError(http.StatusForbidden, "không xác định được người gọi")
		},
		DenyHandler: func(c echo.Context, _ string, _ error) error {
			c.Response().Header().Set("Retry-After", "1")
			return echo.NewHTTPError(http.StatusTooManyRequests, "quá nhiều yêu cầu, thử lại sau")
		},
	})
}

// errorHandler trả OperationOutcome cho lỗi dưới /fhir/R4 (401, 403, 404...), JSON thường cho phần còn lại.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	code, msg := http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
	var he *echo.HTTPError
	if errors.As(err, &he) {
		code = he.Code
		if s, ok := he.Message.(string); ok {
			msg = s
		}
	}
	if !strings.HasPrefix(c.Request().URL.Path, Prefix) {
		_ = c.JSON(code, map[string]string{"error": msg})
		return
	}
	var fe *fhirrouter.Error
	switch code {
	case http.StatusUnauthorized:
		fe = fhirrouter.Unauthorized(msg)
	case http.StatusForbidden:
		fe = fhirrouter.Forbidden(msg)
	case http.StatusNotFound:
		fe = fhirrouter.NotFoundMsg("Không có route cho " + c.Request().Method + " " + c.Request().URL.Path)
	case http.StatusTooManyRequests:
		fe = &fhirrouter.Error{Status: code, Outcome: fhirrouter.BadRequest(msg).Outcome}
	case http.StatusRequestEntityTooLarge:
		fe = &fhirrouter.Error{Status: code, Outcome: fhirrouter.BadRequest("Body quá lớn").Outcome}
	default:
		fe = fhirrouter.Internal(msg)
		fe.Status = code
	}
	c.Response().Header().Set(echo.HeaderContentType, "application/fhir+json; charset=utf-8")
	c.Response().WriteHeader(fe.Status)
	_ = json.NewEncoder(c.Response()).Encode(fe.Outcome)
}
