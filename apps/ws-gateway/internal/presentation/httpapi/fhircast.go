package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/pkg/fhircast"
)

// Lỗi xác thực mà Authenticate trả về; presentation đổi thành 401 và 403.
var (
	ErrUnauthorized = errors.New("httpapi: chưa xác thực")
	ErrForbidden    = errors.New("httpapi: không đủ quyền")
)

// Caller là người gọi đã xác thực: chỉ cần subject và project của token.
type Caller struct{ Subject, Project string }

// FhircastDeps là mọi thứ Hub FHIRcast cần ở tầng HTTP; được lắp ở bootstrap.
type FhircastDeps struct {
	Hub *fhircast.Hub
	// Authenticate kiểm Bearer token và quyền dùng FHIRcast; trả ErrUnauthorized hoặc ErrForbidden (bọc được).
	Authenticate func(ctx context.Context, bearer string) (Caller, error)
	// WSBase là ws(s)://host công khai để dựng hub.channel.endpoint (không suy từ header Host).
	WSBase string
	// MaxBody là kích thước tối đa thân yêu cầu.
	MaxBody int64
	// WebSocket là handler của kênh /ws/fhircast/{endpoint} (bắt buộc; đăng ký cùng hạn mức mở kết nối với websocket Subscription).
	WebSocket http.Handler
	// AllowTopicMismatch: POST /{topic} dùng event["hub.topic"] dù khác topic ở đường dẫn (như Medplum). Mặc định từ chối.
	AllowTopicMismatch bool
}

// FhircastWebSocketPath là tiền tố kênh websocket của FHIRcast; endpoint là đoạn cuối.
const FhircastWebSocketPath = "/ws/fhircast"

// FhircastPaths là các tiền tố của Hub: hai phiên bản và bí danh /hub (STU3).
var FhircastPaths = map[string]fhircast.Version{"/fhircast/STU2": fhircast.STU2, "/fhircast/STU3": fhircast.STU3, "/hub": fhircast.STU3}

const callerKey = "fhircast.caller"

func registerFhircast(e *echo.Echo, d *FhircastDeps) {
	for prefix, version := range FhircastPaths {
		h := &fhircastHandler{FhircastDeps: d, version: version, prefix: prefix}
		e.GET(prefix+"/.well-known/fhircast-configuration", h.wellKnown) // công khai, không token
		e.POST(prefix, h.post, h.auth)
		e.POST(prefix+"/", h.post, h.auth)
		e.POST(prefix+"/:topic", h.postTopic, h.auth)
		e.GET(prefix+"/:topic", h.getContext, h.auth)
	}
}

type fhircastHandler struct {
	*FhircastDeps
	version fhircast.Version
	prefix  string
}

// topic lấy topic từ đường dẫn đã mã hóa và giải mã đúng MỘT lần. Không dùng c.Param: echo trả giá trị đã giải mã khi đường
// dẫn không có %2F nhưng trả nguyên dạng mã hóa khi có, nên topic chứa "/" hay "%" sẽ lệch.
func (h *fhircastHandler) topic(c echo.Context) string {
	seg := strings.TrimPrefix(c.Request().URL.EscapedPath(), h.prefix+"/")
	t, err := url.PathUnescape(seg)
	if err != nil {
		return ""
	}
	return t
}

func (h *fhircastHandler) wellKnown(c echo.Context) error {
	cfg := map[string]any{"eventsSupported": fhircast.SupportedEvents, "websocketSupport": true, "webhookSupport": false, "fhircastVersion": string(h.version)}
	if h.version == fhircast.STU3 {
		cfg["getCurrentSupport"] = true
	}
	return c.JSON(http.StatusOK, cfg)
}

// auth đòi Bearer token hợp lệ có quyền FHIRcast; mọi route trừ .well-known.
func (h *fhircastHandler) auth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tok := bearer(c.Request())
		if tok == "" {
			return unauthorized(c)
		}
		caller, err := h.Authenticate(c.Request().Context(), tok)
		switch {
		case errors.Is(err, ErrUnauthorized):
			return unauthorized(c)
		case errors.Is(err, ErrForbidden):
			return outcome(c, http.StatusForbidden, "forbidden", "Forbidden")
		case err != nil:
			slog.ErrorContext(c.Request().Context(), "fhircast: lỗi xác thực không phân loại được", "error", err)
			return outcome(c, http.StatusInternalServerError, "exception", "Internal server error")
		}
		c.Set(callerKey, caller)
		return next(c)
	}
}

func bearer(r *http.Request) string {
	scheme, tok, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(tok)
}

func unauthorized(c echo.Context) error {
	c.Response().Header().Set("WWW-Authenticate", `Bearer realm="fhircast"`)
	return outcome(c, http.StatusUnauthorized, "login", "Unauthorized")
}

func caller(c echo.Context) Caller { return c.Get(callerKey).(Caller) }

// POST {v}/: có id (hoặc event) thì là đổi ngữ cảnh, không thì là đăng ký/hủy.
func (h *fhircastHandler) post(c echo.Context) error {
	body, form, ok := h.readBody(c)
	if !ok {
		return nil // phản hồi lỗi đã được ghi
	}
	cl := caller(c)
	if _, hasID := body["id"]; hasID || body["event"] != nil {
		return h.change(c, cl, body)
	}
	str := func(k string) string {
		if form != nil {
			return form.Get(k)
		}
		s, _ := body[k].(string)
		return s
	}
	url, err := h.Hub.Subscribe(c.Request().Context(), cl.Project, cl.Subject, h.version, h.WSBase, fhircast.SubscribeRequest{
		ChannelType: str("hub.channel.type"), Mode: str("hub.mode"), Topic: str("hub.topic"), Events: str("hub.events"),
		ChannelEndpoint: str("hub.channel.endpoint"), LegacyEndpoint: str("endpoint"),
	})
	if err != nil {
		return hubError(c, err)
	}
	if str("hub.mode") == "unsubscribe" { // Hub trả endpoint đã hủy; phản hồi gương lại URL đã cấp
		url = strings.TrimRight(h.WSBase, "/") + "/ws/fhircast/" + url
	}
	return c.JSON(http.StatusAccepted, map[string]string{"hub.channel.endpoint": url})
}

// POST {v}/{topic}: chỉ dạng đổi ngữ cảnh.
func (h *fhircastHandler) postTopic(c echo.Context) error {
	body, _, ok := h.readBody(c)
	if !ok {
		return nil // phản hồi lỗi đã được ghi
	}
	if !h.AllowTopicMismatch {
		if topic, _, verr := fhircast.ValidateEvent(body); verr == nil && topic != h.topic(c) {
			return outcome(c, http.StatusBadRequest, "invalid", "Topic mismatch")
		}
	}
	return h.change(c, caller(c), body)
}

func (h *fhircastHandler) change(c echo.Context, cl Caller, body map[string]any) error {
	out, err := h.Hub.ChangeContext(c.Request().Context(), cl.Project, body)
	if err != nil {
		return hubError(c, err)
	}
	return c.JSON(http.StatusAccepted, map[string]any{"success": true, "event": out})
}

func (h *fhircastHandler) getContext(c echo.Context) error {
	cc, ok, err := h.Hub.CurrentContext(c.Request().Context(), caller(c).Project, h.topic(c))
	if err != nil {
		return hubError(c, err)
	}
	return c.JSON(http.StatusOK, fhircast.ContextResponse(h.version, cc, ok))
}

// readBody đọc thân JSON (đối tượng) hoặc form; kiểu khác coi như rỗng (rồi Hub báo thiếu trường). Quá MaxBody là 413. ok=false
// nghĩa là đã ghi phản hồi lỗi và handler phải dừng.
func (h *fhircastHandler) readBody(c echo.Context) (body map[string]any, f form, ok bool) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, h.MaxBody)
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
		if err := r.ParseForm(); err != nil {
			_ = bodyError(c, err)
			return nil, nil, false
		}
		m := make(map[string]any, len(r.PostForm))
		for k := range r.PostForm {
			m[k] = r.PostForm.Get(k)
		}
		return m, form(r.PostForm), true
	case strings.Contains(ct, "json"):
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			_ = bodyError(c, err)
			return nil, nil, false
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return map[string]any{}, nil, true
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil || m == nil {
			_ = outcome(c, http.StatusBadRequest, "invalid", "Invalid request body")
			return nil, nil, false
		}
		return m, nil, true
	}
	return map[string]any{}, nil, true
}

type form map[string][]string

func (f form) Get(k string) string {
	if v := f[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func bodyError(c echo.Context, err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return outcome(c, http.StatusRequestEntityTooLarge, "too-costly", "Request body too large")
	}
	return outcome(c, http.StatusBadRequest, "invalid", "Invalid request body")
}

// hubError đổi lỗi của Hub thành OperationOutcome; lỗi lạ là 500 chung (không lộ chi tiết nội bộ).
func hubError(c echo.Context, err error) error {
	var he *fhircast.Error
	if errors.As(err, &he) {
		code := map[int]string{400: "invalid", 409: "conflict", 500: "exception"}[he.Status]
		if code == "" {
			code = "invalid"
		}
		return outcome(c, he.Status, code, he.Message)
	}
	slog.ErrorContext(c.Request().Context(), "fhircast: lỗi không phân loại", "error", err)
	return outcome(c, http.StatusInternalServerError, "exception", "Internal server error")
}

// outcome trả OperationOutcome với thông điệp ở issue[0].details.text (định dạng của Medplum).
func outcome(c echo.Context, status int, code, text string) error {
	b, _ := json.Marshal(map[string]any{
		"resourceType": "OperationOutcome",
		"issue":        []any{map[string]any{"severity": "error", "code": code, "details": map[string]any{"text": text}}},
	})
	return c.Blob(status, "application/fhir+json", b)
}
