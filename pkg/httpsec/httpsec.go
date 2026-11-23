// Package httpsec chứa middleware net/http dùng chung cho các API của monorepo: header bảo mật và CORS, theo cách của Medplum
// (app.ts `standardHeaders`, cors.ts). Dùng net/http thuần để mọi framework gắn được (Echo: echo.WrapMiddleware); gói này không
// biết framework hay app nào.
package httpsec

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// HeadersOptions cấu hình SecurityHeaders.
type HeadersOptions struct {
	// BaseURL là địa chỉ công khai của dịch vụ; chỉ khi bắt đầu bằng https:// mới đặt HSTS (Strict-Transport-Security).
	BaseURL string
	// Skip (tùy chọn) bỏ qua toàn bộ header cho request khớp, ví dụ giao diện Swagger cần tải script và style nên CSP
	// `default-src 'none'` sẽ làm hỏng nó. Chỉ dùng cho đường dẫn tài liệu ngoài production.
	Skip func(*http.Request) bool
}

// SecurityHeaders đặt các header bảo mật chuẩn cho mọi phản hồi của một API (không có giao diện): tắt cache, HSTS hai năm kèm
// preload khi base URL là https, CSP chặn mọi thứ, tắt tính năng trình duyệt, không gửi Referer, chống đoán kiểu nội dung, cấm
// nhúng iframe.
func SecurityHeaders(o HeadersOptions) func(http.Handler) http.Handler {
	hsts := strings.HasPrefix(o.BaseURL, "https://")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o.Skip != nil && o.Skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			// Không cache bất cứ gì: phản hồi có thể chứa mã đăng nhập hoặc token.
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
			h.Set("Pragma", "no-cache")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
			}
			// Là API, chặn mọi thứ.
			h.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';")
			h.Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=(), interest-cohort=()")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-XSS-Protection", "1; mode=block")
			next.ServeHTTP(w, r)
		})
	}
}

// DefaultExposedHeaders là các header phản hồi mà trình duyệt được phép đọc qua CORS (như Medplum).
var DefaultExposedHeaders = []string{"Content-Location", "ETag", "Last-Modified", "Location", "RateLimit", "X-Request-Id", "X-Trace-Id"}

// CORSOptions cấu hình CORS.
type CORSOptions struct {
	// AllowedOrigins là danh sách origin được phép, dạng scheme://host[:port] (so khớp chính xác, không phân biệt hoa thường).
	// Phần tử "*" cho phép MỌI origin và vẫn kèm credentials (phản chiếu origin, như Medplum `allowedOrigins === '*'`): chỉ để dev.
	AllowedOrigins []string
	// PathPrefixes giới hạn CORS vào các đường dẫn có tiền tố này; rỗng thì áp dụng cho mọi đường dẫn.
	PathPrefixes []string
	// ExposedHeaders mặc định là DefaultExposedHeaders.
	ExposedHeaders []string
	// MaxAge là thời gian trình duyệt cache kết quả preflight (mặc định 10 phút).
	MaxAge time.Duration
}

const preflightMethods = "GET,HEAD,PUT,PATCH,POST,DELETE"

// CORS trả middleware CORS: với origin và đường dẫn được phép thì đặt `Access-Control-Allow-Origin` (phản chiếu origin, không bao
// giờ `*`), `Access-Control-Allow-Credentials: true`, `Vary: Origin` và các header lộ ra. Preflight (OPTIONS có `Origin` và
// `Access-Control-Request-Method`) được trả lời 204 ngay tại đây; preflight của origin hoặc đường dẫn không được phép cũng trả
// 204 nhưng KHÔNG có header CORS nên trình duyệt chặn. Request không có `Origin` đi thẳng qua.
func CORS(o CORSOptions) func(http.Handler) http.Handler {
	allowAll := slices.Contains(o.AllowedOrigins, "*")
	allowed := map[string]bool{}
	for _, origin := range o.AllowedOrigins {
		if origin != "*" {
			allowed[normalizeOrigin(origin)] = true
		}
	}
	exposed := strings.Join(orDefault(o.ExposedHeaders, DefaultExposedHeaders), ", ")
	maxAge := o.MaxAge
	if maxAge <= 0 {
		maxAge = 10 * time.Minute
	}
	maxAgeSeconds := strconv.Itoa(int(maxAge / time.Second))
	originOK := func(origin string) bool { return allowAll || allowed[normalizeOrigin(origin)] }
	pathOK := func(p string) bool {
		if len(o.PathPrefixes) == 0 {
			return true
		}
		for _, prefix := range o.PathPrefixes {
			if strings.HasPrefix(p, prefix) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			// Phản hồi phụ thuộc Origin: cache trung gian phải phân biệt.
			h.Add("Vary", "Origin")
			ok := originOK(origin) && pathOK(r.URL.Path)
			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
			if preflight {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				if ok {
					setAllow(h, origin)
					h.Set("Access-Control-Allow-Methods", preflightMethods)
					if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
						h.Set("Access-Control-Allow-Headers", reqHeaders) // phản chiếu header client xin
					}
					h.Set("Access-Control-Max-Age", maxAgeSeconds)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if ok {
				setAllow(h, origin)
				h.Set("Access-Control-Expose-Headers", exposed)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setAllow(h http.Header, origin string) {
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Credentials", "true")
}

func orDefault(v, def []string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}

// normalizeOrigin hạ chữ thường và bỏ dấu / cuối để "HTTP://Localhost:3000/" khớp "http://localhost:3000".
func normalizeOrigin(s string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(s)), "/")
}

// ParseOrigin kiểm tra một origin cấu hình: phải là scheme://host[:port] (http hoặc https), không có đường dẫn, query, fragment
// hay thông tin đăng nhập. Trả origin đã chuẩn hóa.
func ParseOrigin(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", &OriginError{s, "phải có dạng http(s)://host[:port]"}
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", &OriginError{s, "không được có đường dẫn, query, fragment hay thông tin đăng nhập"}
	}
	return normalizeOrigin(u.Scheme + "://" + u.Host), nil
}

// OriginError là lỗi origin cấu hình sai.
type OriginError struct{ Origin, Reason string }

func (e *OriginError) Error() string { return "origin " + strconv.Quote(e.Origin) + ": " + e.Reason }
