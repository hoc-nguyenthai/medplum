// Package httpapi là tầng presentation của dicomweb: Echo v4, xác thực JWT, các tiền tố DICOMweb, probe và lỗi dạng {"error": "..."}.
//
// D1 chỉ dựng khung: mọi route DICOMweb đã được khai báo, qua xác thực và cô lập project, nhưng trả 501 cho tới khi D3 (STOW-RS) và D4
// (QIDO-RS, WADO-RS) cài handler (docs/plans/cds-dicom/dicom.md). Route chưa cài mãi mãi trả 501, không phải 200 rỗng như Medplum (H5).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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

// StowRequest là yêu cầu STOW-RS đã qua xác thực.
type StowRequest struct {
	ContentType  string    // header Content-Type của yêu cầu
	Body         io.Reader // thân yêu cầu (luồng, không nạp vào bộ nhớ)
	StudyUID     string    // khác rỗng với POST /studies/{studyUid}
	RetrieveBase string    // gốc URL WADO-RS kèm tiền tố đang dùng, ví dụ https://dicom.example.org/dicomweb
}

// StowResult là kết quả STOW-RS: mã trạng thái (200, 202, 409) và phản hồi DICOM JSON.
type StowResult struct {
	Status int
	Body   any
}

// Stower thực hiện STOW-RS (cài ở bootstrap, bằng pkg/dicomstow).
type Stower interface {
	Stow(ctx context.Context, req StowRequest) (StowResult, error)
}

// StatusError là lỗi của cả yêu cầu kèm mã HTTP (415, 400, 403...).
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return e.Msg }

// QueryPage là một trang kết quả QIDO-RS: Items được mã hóa thành mảng JSON (DICOM JSON).
type QueryPage struct {
	Items any
	More  bool // còn kết quả sau trang này
	// Ignored là các tham số truy vấn không hỗ trợ, đã bị bỏ qua (báo bằng header Warning).
	Ignored []string
}

// Rendered là ảnh đã dựng (image/jpeg hoặc image/png) của một frame.
type Rendered struct {
	ContentType string
	Body        []byte
}

// Download là phản hồi tải tệp DICOM (multipart/related; type="application/dicom") ghi theo luồng, độ dài không biết trước (chunked).
type Download struct {
	ContentType string
	Write       func(io.Writer) error
}

// FrameStream là phản hồi frame WADO-RS đã kiểm hợp lệ, sẵn sàng ghi theo luồng.
type FrameStream struct {
	ContentType string // multipart/related kèm boundary
	Length      int64  // tổng số byte (biết trước)
	Write       func(io.Writer) error
}

// Retriever thực hiện QIDO-RS và WADO-RS (cài ở bootstrap, bằng pkg/dicomretrieve). limit, offset và frames là chuỗi thô trên URL: bên cài đặt đọc và báo lỗi 400.
// Mọi lỗi 4xx và 5xx phải xảy ra trước khi trả hàm ghi, vì sau khi bắt đầu ghi thì không đổi được mã trạng thái.
type Retriever interface {
	// Search là QIDO-RS ở cấp "study", "series" hoặc "instance"; studyUID và seriesUID giới hạn phạm vi (rỗng: cả project); params là toàn bộ query của URL.
	Search(ctx context.Context, level, studyUID, seriesUID string, params url.Values) (QueryPage, error)
	// Metadata trả metadata của một instance (instanceUID), một series (seriesUID) hoặc cả study.
	Metadata(ctx context.Context, studyUID, seriesUID, instanceUID string) (func(io.Writer) error, error)
	// Download trả tệp DICOM của một instance, một series hoặc cả study.
	Download(ctx context.Context, studyUID, seriesUID, instanceUID string) (Download, error)
	Frames(ctx context.Context, studyUID, seriesUID, instanceUID, frames string) (FrameStream, error)
	// Rendered dựng ảnh của frame (rỗng: frame 1) theo header Accept và tham số truy vấn (window, quality); 406 khi không dựng được.
	Rendered(ctx context.Context, studyUID, seriesUID, instanceUID, frame, accept string, params url.Values) (Rendered, error)
}

// Deps là mọi thứ presentation cần; được lắp ở bootstrap.
type Deps struct {
	Verifier TokenVerifier
	// Retrieve xử lý QIDO-RS và WADO-RS; nil thì các route đó trả 501.
	Retrieve Retriever
	// Stow xử lý STOW-RS; nil thì các route STOW trả 501.
	Stow  Stower
	Ready func(ctx context.Context) (ready bool, checks map[string]bool)
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
		stow := handler(dep.Stow, stowHandler(dep.Stow, dep.BaseURL))
		g.POST("/studies", stow)
		g.POST("/studies/:studyUid", stow)
		r := dep.Retrieve
		search := func(level string) echo.HandlerFunc { return retrieve(r, searchHandler(r, level)) }
		meta := retrieve(r, metadataHandler(r))
		dl := retrieve(r, downloadHandler(r))
		g.GET("/studies", search("study"))
		g.GET("/series", search("series"))
		g.GET("/instances", search("instance"))
		g.GET("/studies/:studyUid", dl)
		g.GET("/studies/:studyUid/series", search("series"))
		g.GET("/studies/:studyUid/instances", search("instance"))
		g.GET("/studies/:studyUid/metadata", meta)
		g.GET("/studies/:studyUid/series/:seriesUid", dl)
		g.GET("/studies/:studyUid/series/:seriesUid/instances", search("instance"))
		g.GET("/studies/:studyUid/series/:seriesUid/metadata", meta)
		g.GET("/studies/:studyUid/series/:seriesUid/instances/:instanceUid", dl)
		g.GET("/studies/:studyUid/series/:seriesUid/instances/:instanceUid/metadata", meta)
		g.GET("/studies/:studyUid/series/:seriesUid/instances/:instanceUid/frames/:frame", retrieve(r, framesHandler(r)))
		g.GET("/studies/:studyUid/series/:seriesUid/instances/:instanceUid/rendered", retrieve(r, renderedHandler(r)))
		g.GET("/studies/:studyUid/series/:seriesUid/instances/:instanceUid/frames/:frame/rendered", retrieve(r, renderedHandler(r)))
		// Mọi route DICOMweb khác (bulkdata, rendered cấp series và study, delete...): 501 sau xác thực, không phải 200 rỗng.
		g.Any("", notImplemented)
		g.Any("/*", notImplemented)
	}
	return e, nil
}

func retrieve(svc Retriever, h echo.HandlerFunc) echo.HandlerFunc {
	if svc == nil {
		return notImplemented
	}
	return h
}

// retrieveError đổi lỗi của dịch vụ thành lỗi HTTP: StatusError giữ mã và thông điệp, lỗi khác là 500 không lộ chi tiết (ghi log).
func retrieveError(c echo.Context, err error) error {
	var se *StatusError
	if errors.As(err, &se) {
		return echo.NewHTTPError(se.Status, se.Msg)
	}
	slog.ErrorContext(c.Request().Context(), "DICOMweb lỗi", "error", err.Error())
	return err
}

const dicomJSON = "application/dicom+json"

// writePage trả một trang QIDO-RS: mảng DICOM JSON, kèm Warning 299 khi còn kết quả (PS3.18 8.3.4). Không có kết quả thì 200 với [] (như Medplum; chuẩn cho phép cả 204).
func writePage(c echo.Context, p QueryPage) error {
	raw, err := json.Marshal(p.Items)
	if err != nil {
		return err
	}
	if p.More {
		c.Response().Header().Add("Warning", `299 dicomweb: There are additional results that can be requested`)
	}
	if len(p.Ignored) > 0 {
		c.Response().Header().Add("Warning", `299 dicomweb: Ignored unsupported query parameters: `+strings.Join(p.Ignored, ", "))
	}
	return c.Blob(http.StatusOK, dicomJSON, raw)
}

// searchHandler là QIDO-RS ở một cấp. Ba cấp dùng chung vì chỉ khác phạm vi lấy từ đường dẫn.
func searchHandler(svc Retriever, level string) echo.HandlerFunc {
	return func(c echo.Context) error {
		p, err := svc.Search(c.Request().Context(), level, c.Param("studyUid"), c.Param("seriesUid"), c.QueryParams())
		if err != nil {
			return retrieveError(c, err)
		}
		return writePage(c, p)
	}
}

// metadataHandler ghi mảng metadata (study, series hoặc instance tùy tham số đường dẫn) theo luồng. Không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối
// (panic ErrAbortHandler) để client thấy phản hồi bị cắt thay vì một JSON dở dang trông như hợp lệ.
func metadataHandler(svc Retriever) echo.HandlerFunc {
	return func(c echo.Context) error {
		write, err := svc.Metadata(c.Request().Context(), c.Param("studyUid"), c.Param("seriesUid"), c.Param("instanceUid"))
		if err != nil {
			return retrieveError(c, err)
		}
		c.Response().Header().Set(echo.HeaderContentType, dicomJSON)
		c.Response().WriteHeader(http.StatusOK)
		if err := write(c.Response()); err != nil {
			slog.ErrorContext(c.Request().Context(), "DICOMweb: lỗi khi ghi metadata", "error", err.Error())
			panic(http.ErrAbortHandler)
		}
		return nil
	}
}

// downloadHandler trả tệp DICOM Part 10 của instance, series hoặc study (PS3.18 10.4.1.1.1) dạng multipart/related, chunked, mỗi instance một phần.
func downloadHandler(svc Retriever) echo.HandlerFunc {
	return func(c echo.Context) error {
		d, err := svc.Download(c.Request().Context(), c.Param("studyUid"), c.Param("seriesUid"), c.Param("instanceUid"))
		if err != nil {
			return retrieveError(c, err)
		}
		c.Response().Header().Set(echo.HeaderContentType, d.ContentType)
		c.Response().WriteHeader(http.StatusOK)
		if err := d.Write(c.Response()); err != nil {
			slog.ErrorContext(c.Request().Context(), "DICOMweb: lỗi khi ghi tệp DICOM", "error", err.Error())
			panic(http.ErrAbortHandler)
		}
		return nil
	}
}

// renderedHandler trả ảnh JPEG hoặc PNG của một frame (instance không kèm frame là frame 1). Ảnh dựng trong bộ nhớ nên có Content-Length.
func renderedHandler(svc Retriever) echo.HandlerFunc {
	return func(c echo.Context) error {
		r, err := svc.Rendered(c.Request().Context(), c.Param("studyUid"), c.Param("seriesUid"), c.Param("instanceUid"), c.Param("frame"), c.Request().Header.Get("Accept"), c.QueryParams())
		if err != nil {
			return retrieveError(c, err)
		}
		return c.Blob(http.StatusOK, r.ContentType, r.Body)
	}
}

// framesHandler trả frame dạng multipart/related (PS3.18 8.7.3), có Content-Length vì độ dài biết trước; lỗi sau khi đã ghi thì ngắt kết nối.
func framesHandler(svc Retriever) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, err := svc.Frames(c.Request().Context(), c.Param("studyUid"), c.Param("seriesUid"), c.Param("instanceUid"), c.Param("frame"))
		if err != nil {
			return retrieveError(c, err)
		}
		h := c.Response().Header()
		h.Set(echo.HeaderContentType, f.ContentType)
		h.Set(echo.HeaderContentLength, strconv.FormatInt(f.Length, 10))
		c.Response().WriteHeader(http.StatusOK)
		if err := f.Write(c.Response()); err != nil {
			slog.ErrorContext(c.Request().Context(), "DICOMweb: lỗi khi ghi frame", "error", err.Error())
			panic(http.ErrAbortHandler)
		}
		return nil
	}
}

func handler(svc Stower, h echo.HandlerFunc) echo.HandlerFunc {
	if svc == nil {
		return notImplemented
	}
	return h
}

// stowHandler là STOW-RS (PS3.18 10.5): thân multipart/related được chuyển nguyên dạng luồng cho dịch vụ. Phản hồi là DICOM JSON
// (application/dicom+json); 200 mọi instance thành công, 202 một phần, 409 không instance nào thành công.
func stowHandler(svc Stower, baseURL string) echo.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	return func(c echo.Context) error {
		req := c.Request()
		prefix := req.URL.Path
		if i := strings.Index(prefix, "/studies"); i >= 0 {
			prefix = prefix[:i]
		}
		res, err := svc.Stow(req.Context(), StowRequest{
			ContentType: req.Header.Get(echo.HeaderContentType), Body: req.Body, StudyUID: c.Param("studyUid"), RetrieveBase: base + prefix,
		})
		var se *StatusError
		switch {
		case errors.As(err, &se):
			return echo.NewHTTPError(se.Status, se.Msg)
		case err != nil:
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "Body quá lớn")
			}
			slog.ErrorContext(req.Context(), "STOW-RS lỗi", "error", err.Error())
			return err
		}
		raw, err := json.Marshal(res.Body)
		if err != nil {
			return err
		}
		return c.Blob(res.Status, "application/dicom+json", raw)
	}
}

// notImplemented là chỗ giữ cho route chưa cài.
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
		case code == http.StatusNotFound && (s == "" || s == http.StatusText(http.StatusNotFound)): // route không tồn tại (mặc định của Echo)
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
