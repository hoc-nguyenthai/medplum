// Package httpapi là tầng presentation của ws-gateway: Echo v4, health probe và endpoint websocket của Subscription.
// Gateway không có API REST nghiệp vụ nào; mọi xác thực nằm trong token bind (do fhir-server cấp và ký).
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"trivita-fhir/pkg/httpsec"
)

// WebSocketPath là endpoint websocket của Subscription, giống Medplum và fhir-server, để client không phải đổi đường dẫn.
const WebSocketPath = "/ws/subscriptions-r4"

// Deps là mọi thứ presentation cần; được lắp ở bootstrap.
type Deps struct {
	// WebSocket là handler của WebSocketPath (bắt buộc).
	WebSocket http.Handler
	Ready     func(ctx context.Context) (ready bool, checks map[string]bool)
	// Instrument gắn observability vào echo TRƯỚC mọi middleware khác để span bao trùm tất cả (có thể nil).
	Instrument func(e *echo.Echo) error
	Debug      bool
	// IPExtractor xác định IP người gọi (hạn mức, log). Mặc định KHÔNG tin header proxy.
	IPExtractor echo.IPExtractor
	// RateLimit là số lần mở websocket mỗi giây mỗi IP (0: tắt), RateBurst là cỡ đợt (<=0: gấp đôi, tối thiểu 1).
	RateLimit float64
	RateBurst int
	// Fhircast bật Hub FHIRcast (nil: tắt, route không được đăng ký).
	Fhircast *FhircastDeps
}

// New dựng Echo với route, middleware và error handler.
func New(dep Deps) (*echo.Echo, error) {
	if dep.WebSocket == nil {
		return nil, errors.New("httpapi: thiếu handler websocket")
	}
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
	e.Use(echo.WrapMiddleware(httpsec.SecurityHeaders(httpsec.HeadersOptions{})))

	registerHealth(e, dep)

	// Hạn mức đứng trước handler websocket (chặn dò token và bão upgrade); không áp cho health.
	var mws []echo.MiddlewareFunc
	if dep.RateLimit > 0 {
		mws = append(mws, rateLimiter(dep.RateLimit, dep.RateBurst))
	}
	e.GET(WebSocketPath, echo.WrapHandler(dep.WebSocket), mws...)
	if dep.Fhircast != nil {
		if dep.Fhircast.Hub == nil || dep.Fhircast.Authenticate == nil || dep.Fhircast.MaxBody <= 0 || dep.Fhircast.WSBase == "" || dep.Fhircast.WebSocket == nil {
			return nil, errors.New("httpapi: cấu hình FHIRcast thiếu Hub, Authenticate, MaxBody, WSBase hoặc WebSocket")
		}
		registerFhircast(e, dep.Fhircast)
		// Cùng hạn mức mở kết nối với websocket Subscription (chặn bão upgrade); endpoint rỗng cũng vào handler để nhận "invalid endpoint".
		wsh := echo.WrapHandler(dep.Fhircast.WebSocket)
		e.GET(FhircastWebSocketPath, wsh, mws...)
		e.GET(FhircastWebSocketPath+"/*", wsh, mws...)
	}
	return e, nil
}

// HealthResponse là body của các probe còn sống.
type HealthResponse struct {
	Status string `json:"status"`
}

// ReadyResponse là body của /health/ready; Checks là kết quả từng phụ thuộc (true = sẵn sàng).
type ReadyResponse struct {
	Ready  bool            `json:"ready"`
	Checks map[string]bool `json:"checks"`
}

func registerHealth(e *echo.Echo, dep Deps) {
	live := func(c echo.Context) error { return c.JSON(http.StatusOK, HealthResponse{Status: "ok"}) }
	e.GET("/healthz", live) // HEALTHCHECK của Docker (pkg/httpserve.HealthCheck)
	e.GET("/health/live", live)
	e.GET("/health/startup", live)
	e.GET("/health/ready", func(c echo.Context) error {
		if dep.Ready == nil {
			return c.JSON(http.StatusOK, ReadyResponse{Ready: true, Checks: map[string]bool{}})
		}
		ready, checks := dep.Ready(c.Request().Context())
		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, ReadyResponse{Ready: ready, Checks: checks})
	})
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

// errorHandler trả JSON {"error": ...}; lỗi 5xx không lộ chi tiết nội bộ.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	code, msg := http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
	var he *echo.HTTPError
	if errors.As(err, &he) {
		code = he.Code
		if s, ok := he.Message.(string); ok && code < 500 {
			msg = s
		} else if code < 500 {
			msg = http.StatusText(code)
		}
	}
	_ = c.JSON(code, map[string]string{"error": msg})
}
