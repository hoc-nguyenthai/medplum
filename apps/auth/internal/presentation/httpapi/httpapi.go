// Package httpapi là tầng presentation của auth: Echo v4, route, DTO, validator và ánh xạ lỗi.
// Chỉ gọi application/domain; không chứa nghiệp vụ.
package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echoswagger "github.com/swaggo/echo-swagger"

	_ "trivita-fhir/apps/auth/docs" // đăng ký spec OpenAPI do `make swagger APP=auth` sinh
	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/pkg/httpsec"
	"trivita-fhir/pkg/ratelimit"
)

// Deps là mọi thứ presentation cần; được lắp ở bootstrap.
type Deps struct {
	// Ready trả trạng thái sẵn sàng và kết quả từng phụ thuộc (true = sẵn sàng).
	Ready func(ctx context.Context) (ready bool, checks map[string]bool)
	// Instrument gắn observability vào echo TRƯỚC mọi middleware khác để span bao trùm tất cả (có thể nil).
	Instrument func(e *echo.Echo) error
	// Swagger bật Swagger UI (chỉ ngoài staging/production).
	Swagger bool
	Debug   bool

	// Auth là các use case của auth; nil thì không đăng ký route /auth (dùng cho test của khung).
	Auth *application.Service
	// Authenticator xác minh Bearer token cho /auth/me và /auth/revoke; nil thì mọi yêu cầu có Bearer đều bị 401.
	Authenticator application.Authenticator
	// BaseURL là địa chỉ công khai của auth: chỉ để biết có đặt HSTS hay không (khi là https).
	BaseURL string
	// AllowedOrigins là các origin được gọi API từ trình duyệt (CORS); rỗng là không cho origin nào.
	AllowedOrigins []string
	// ExternalEnabled đăng ký callback đăng nhập ngoài (GET /auth/external).
	ExternalEnabled bool
	// AdminEnabled đăng ký /admin/projects/* (mặc định tắt: bề mặt quản trị lớn).
	AdminEnabled bool
	// ScimEnabled đăng ký /scim/v2/Users (mặc định tắt). ScimAllowedNets: CIDR được gọi (rỗng: không hạn chế); ScimMaxBodyBytes: cỡ body tối đa.
	ScimEnabled bool
	// EmailEnabled đăng ký POST /email/v1/send (mặc định tắt).
	EmailEnabled     bool
	ScimAllowedNets  []*net.IPNet
	ScimMaxBodyBytes int64
	// CookieSecure đặt cờ Secure cho cookie đăng nhập (mặc định nên true; chỉ tắt ở dev dùng HTTP).
	CookieSecure bool
	// IPExtractor xác định IP người gọi (dùng cho ipAccessRule và log). Mặc định KHÔNG tin header proxy.
	IPExtractor echo.IPExtractor
	// OAuth (nil: chưa phát token) cung cấp JWKS và thông tin discovery; có thì đăng ký /oauth2/token và /.well-known/*.
	OAuth *OAuthInfo
	// RateLimit (yêu cầu/giây mỗi IP, 0: tắt) áp cho mọi đường dẫn trong corsPrefixes; RateBurst <= 0: gấp đôi.
	RateLimit float64
	RateBurst int
	// LoginRateLimit áp thêm, chặt hơn, cho các bước nhập thông tin đăng nhập (xem loginRoutes); 0: tắt.
	LoginRateLimit float64
	LoginRateBurst int
	// NewRateStore tạo kho đếm cho một bộ giới hạn (name phân biệt các bộ); nil: bộ nhớ trong từng replica.
	// Truyền kho Redis để hạn mức dùng chung mọi replica.
	NewRateStore func(name string, rps float64, burst int) ratelimit.Store
}

func (d Deps) rateStore(name string, rps float64, burst int) ratelimit.Store {
	if d.NewRateStore != nil {
		return d.NewRateStore(name, rps, burst)
	}
	return ratelimit.NewMemory(rps, burst)
}

// corsPrefixes là các đường dẫn của auth được gọi từ trình duyệt (như danh sách của Medplum, cors.ts).
var corsPrefixes = []string{"/auth/", "/oauth2/", "/.well-known/", "/admin/", "/scim/", "/email/"}

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
		// Giao diện Swagger (chỉ ngoài production) cần tải script và style nên CSP `default-src 'none'` làm hỏng nó.
		Skip: func(r *http.Request) bool { return dep.Swagger && strings.HasPrefix(r.URL.Path, "/swagger/") },
	})))
	e.Use(echo.WrapMiddleware(httpsec.CORS(httpsec.CORSOptions{AllowedOrigins: dep.AllowedOrigins, PathPrefixes: corsPrefixes})))
	e.Use(middleware.BodyLimit("1M"))
	if dep.RateLimit > 0 {
		e.Use(rateLimiter(dep.rateStore("global", dep.RateLimit, dep.RateBurst), func(c echo.Context) bool { return !hasPrefix(c.Request().URL.Path, corsPrefixes) }))
	}

	registerHealth(e, dep)
	if dep.Auth != nil {
		registerAuth(e, dep)
		if dep.AdminEnabled {
			registerAdmin(e, dep)
		}
		if dep.ScimEnabled {
			registerScim(e, dep)
		}
		if dep.EmailEnabled {
			registerEmail(e, dep)
		}
		if dep.OAuth != nil {
			registerOAuth(e, dep)
		}
	}
	if dep.Swagger {
		// Swagger UI và spec (/swagger/index.html, /swagger/doc.json); chỉ mô tả API, không chứa dữ liệu.
		e.GET("/swagger/*", echoswagger.WrapHandler)
	}
	return e, nil
}

func hasPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// rateLimiter giới hạn theo IP bằng store (bộ nhớ: mỗi replica đếm riêng; Redis: dùng chung). Mỗi middleware có store riêng.
func rateLimiter(store ratelimit.Store, skip func(echo.Context) bool) echo.MiddlewareFunc {
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper:             skip,
		Store:               store,
		IdentifierExtractor: func(c echo.Context) (string, error) { return c.RealIP(), nil },
		ErrorHandler: func(c echo.Context, err error) error {
			return echo.NewHTTPError(http.StatusForbidden, "cannot identify caller")
		},
		DenyHandler: func(c echo.Context, _ string, _ error) error {
			c.Response().Header().Set("Retry-After", "1")
			return echo.NewHTTPError(http.StatusTooManyRequests, "Too many requests")
		},
	})
}

func ok(c echo.Context) error { return c.JSON(http.StatusOK, HealthResponse{Status: "ok"}) }
