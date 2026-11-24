// Package httpapi là tầng presentation của dicomweb: Echo v4, xác thực JWT, các tiền tố DICOMweb, probe và lỗi dạng {"error": "..."}.
//
// D1 chỉ dựng khung: mọi route DICOMweb đã được khai báo, qua xác thực và cô lập project, nhưng trả 501 cho tới khi D3 (STOW-RS) và D4
// (QIDO-RS, WADO-RS) cài handler (docs/plans/cds-dicom/dicom.md). Route chưa cài mãi mãi trả 501, không phải 200 rỗng như Medplum (H5).
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echoswagger "github.com/swaggo/echo-swagger"
	"golang.org/x/time/rate"

	_ "trivita-fhir/apps/dicomweb/docs" // đăng ký spec OpenAPI do `make swagger` sinh
	"trivita-fhir/pkg/httpsec"
)

// Prefixes là bốn tiền tố mà Medplum phục vụ (app.ts:223-226); hai tiền tố sau chứa project và phải khớp project của token.
var Prefixes = []string{"/dicomweb", "/api/dicomweb", "/projects/:projectId/dicomweb", "/api/projects/:projectId/dicomweb"}

// Deps là mọi thứ presentation cần; được lắp ở bootstrap.
type Deps struct {
	Verifier TokenVerifier
	Ready    func(ctx context.Context) (ready bool, checks map[string]bool)
	// Instrument gắn observability vào echo TRƯỚC mọi middleware khác để span bao trùm tất cả (có thể nil).
	Instrument func(e *echo.Echo) error
	Debug      bool
	// BaseURL quyết định có gửi HSTS không (chỉ khi https).
	BaseURL string
	// AllowedOrigins là origin được gọi API từ trình duyệt (CORS); rỗng: không origin nào.
	AllowedOrigins []string
	// IPExtractor xác định IP người gọi (hạn mức, log). Mặc định KHÔNG tin header proxy.
	IPExtractor echo.IPExtractor
	// RateLimit là số yêu cầu mỗi giây mỗi IP (0: tắt), RateBurst là cỡ đợt (<=0: gấp đôi RateLimit, tối thiểu 1).
	RateLimit float64
	RateBurst int
	// MaxRequestBytes là trần body của một yêu cầu (vượt thì 413, đo trong lúc đọc nên không cần Content-Length).
	MaxRequestBytes int64
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
	e.Use(echo.WrapMiddleware(httpsec.CORS(httpsec.CORSOptions{AllowedOrigins: dep.AllowedOrigins, PathPrefixes: []string{"/dicomweb", "/api/", "/projects/"}})))

	// Probe (docs/service-principles.md mục 6.3): live và startup không phụ thuộc gì ngoài tiến trình; ready kiểm phụ thuộc chính và 503 khi đang tắt.
	ok := func(c echo.Context) error { return c.JSON(http.StatusOK, HealthResponse{Status: "ok"}) }
	e.GET("/healthz", ok)
	e.GET("/health/live", ok)
	e.GET("/health/startup", ok)
	e.GET("/health/ready", func(c echo.Context) error {
		ready, checks := dep.Ready(c.Request().Context())
		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, ReadyResponse{Ready: ready, Checks: checks})
	})
	// Swagger UI và spec (/swagger/index.html, /swagger/doc.json); công khai, chỉ mô tả API.
	e.GET("/swagger/*", echoswagger.WrapHandler)

	// Hạn mức đứng TRƯỚC xác thực để cả việc dò token cũng bị chặn; không áp cho health và swagger.
	mws := []echo.MiddlewareFunc{limitBody(dep.MaxRequestBytes), guard(dep.Verifier)}
	if dep.RateLimit > 0 {
		mws = append([]echo.MiddlewareFunc{rateLimiter(dep.RateLimit, dep.RateBurst)}, mws...)
	}
	for _, prefix := range Prefixes {
		g := e.Group(prefix, mws...)
		g.POST("/studies", notImplemented)
		g.POST("/studies/:studyUid", notImplemented)
		g.GET("/studies", notImplemented)
		g.GET("/studies/:studyUid/series", notImplemented)
		g.GET("/studies/:studyUid/series/:seriesUid/metadata", notImplemented)
		g.GET("/studies/:studyUid/series/:seriesUid/instances/:instanceUid/frames/:frame", notImplemented)
		// Mọi route DICOMweb khác (instances, rendered, bulkdata, delete...): 501 sau xác thực, không phải 200 rỗng.
		g.Any("", notImplemented)
		g.Any("/*", notImplemented)
	}
	return e, nil
}

// notImplemented là chỗ giữ cho handler của D3 và D4.
func notImplemented(c echo.Context) error {
	return echo.NewHTTPError(http.StatusNotImplemented, "Chưa cài đặt")
}

// limitBody đặt trần kích thước body: Content-Length đã vượt trần thì 413 ngay (không đọc), còn không (chunked) thì lỗi 413 từ http.MaxBytesReader
// khi đọc quá trần.
func limitBody(max int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if max > 0 && c.Request().ContentLength > max {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "Body quá lớn")
			}
			if max > 0 && c.Request().Body != nil {
				c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, max)
			}
			return next(c)
		}
	}
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

// errorHandler trả {"error": "..."} cho mọi lỗi (DICOMweb không dùng OperationOutcome). Lỗi 5xx chỉ nói mô tả trạng thái chuẩn, không lộ chi tiết nội bộ.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	code, msg := http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
	var he *echo.HTTPError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &he):
		code = he.Code
		switch s, _ := he.Message.(string); {
		case code == http.StatusNotFound:
			msg = "Không có route cho " + c.Request().Method + " " + c.Request().URL.Path
		case code < 500 || code == http.StatusNotImplemented:
			if s != "" {
				msg = s
			} else {
				msg = http.StatusText(code)
			}
		default:
			msg = http.StatusText(code)
		}
	case errors.As(err, &mbe):
		code, msg = http.StatusRequestEntityTooLarge, "Body quá lớn"
	}
	_ = c.JSON(code, ErrorBody{Error: msg})
}
