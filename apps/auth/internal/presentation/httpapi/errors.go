package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// ErrorBody là lỗi JSON thường (ngoài /auth và /admin).
type ErrorBody struct {
	Error string `json:"error" example:"Not Found"`
}

// fieldError là một lỗi kiểm tra đầu vào gắn với một trường (mỗi lỗi thành một issue của OperationOutcome).
type fieldError struct{ Message, Expression string }

// validationError gom mọi lỗi của một request (Medplum chạy hết các validator rồi mới trả).
type validationError struct{ Errors []fieldError }

func (v *validationError) Error() string { return v.Errors[0].Message }

func (v *validationError) add(msg, expr string) { v.Errors = append(v.Errors, fieldError{msg, expr}) }

func (v *validationError) orNil() error {
	if len(v.Errors) == 0 {
		return nil
	}
	return v
}

func issue(severity, code, msg, expr string) fhir.OperationOutcomeIssue {
	i := fhir.OperationOutcomeIssue{Severity: severity, Code: code, Diagnostics: msg, Details: &fhir.CodeableConcept{Text: msg}}
	if expr != "" {
		i.Expression = []string{expr}
	}
	return i
}

func outcomeOf(issues ...fhir.OperationOutcomeIssue) *fhir.OperationOutcome {
	return &fhir.OperationOutcome{ResourceType: "OperationOutcome", Issue: issues}
}

// outcome dựng OperationOutcome một issue, đúng dạng Medplum dùng cho lỗi của /auth.
func outcome(severity, code, msg string) *fhir.OperationOutcome {
	return outcomeOf(issue(severity, code, msg, ""))
}

// allOK là phản hồi thành công không có nội dung của Medplum (OperationOutcome "All OK").
func allOK() *fhir.OperationOutcome {
	o := outcomeOf(fhir.OperationOutcomeIssue{Severity: "information", Code: "informational", Details: &fhir.CodeableConcept{Text: "All OK"}})
	o.ID = "ok"
	return o
}

// rateLimitResetURL là extension của Medplum báo số giây tới khi hạn mức được đặt lại.
const rateLimitResetURL = "https://medplum.com/fhir/StructureDefinition/rate-limit-reset"

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid"
	case http.StatusUnauthorized:
		return "login"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not-found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too-long"
	case http.StatusUnsupportedMediaType:
		return "not-supported"
	case http.StatusTooManyRequests:
		return "throttled"
	}
	return "exception"
}

func statusFor(kind domain.Kind) int {
	switch kind {
	case domain.KindBadRequest:
		return http.StatusBadRequest
	case domain.KindUnauthorized:
		return http.StatusUnauthorized
	case domain.KindForbidden:
		return http.StatusForbidden
	case domain.KindNotFound:
		return http.StatusNotFound
	case domain.KindConflict:
		return http.StatusConflict
	case domain.KindTooManyRequests:
		return http.StatusTooManyRequests
	case domain.KindBadGateway:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// writeOutcome ghi OperationOutcome với Content-Type application/fhir+json.
func writeOutcome(c echo.Context, status int, o *fhir.OperationOutcome) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/fhir+json; charset=utf-8")
	c.Response().WriteHeader(status)
	return json.NewEncoder(c.Response()).Encode(o)
}

// writeError ánh xạ lỗi sang phản hồi: lỗi kiểm tra nhiều trường thành nhiều issue; lỗi nghiệp vụ giữ mã và thông điệp; mọi lỗi
// còn lại là 500 với thông điệp chung (chi tiết chỉ vào log). Medplum biến lỗi lạ thành 400 kèm thông điệp lỗi, nhưng làm vậy
// sẽ lộ chi tiết nội bộ.
func writeError(c echo.Context, err error) error {
	var ve *validationError
	if errors.As(err, &ve) {
		issues := make([]fhir.OperationOutcomeIssue, len(ve.Errors))
		for i, fe := range ve.Errors {
			issues[i] = issue("error", "invalid", fe.Message, fe.Expression)
		}
		return writeOutcome(c, http.StatusBadRequest, outcomeOf(issues...))
	}
	var de *domain.Error
	if errors.As(err, &de) && de.Kind != domain.KindInternal {
		status := statusFor(de.Kind)
		o := outcomeOf(issue("error", codeFor(status), de.Message, de.Expression))
		if de.Kind == domain.KindTooManyRequests {
			// giống Medplum: extension rate-limit-reset (giây, làm tròn lên) cùng header Retry-After chuẩn
			secs := int((de.RetryAfter + time.Second - 1) / time.Second)
			if secs < 0 {
				secs = 0
			}
			o.Extension = []fhir.Extension{{URL: rateLimitResetURL, ValueUnsignedInt: &secs}}
			c.Response().Header().Set("Retry-After", strconv.Itoa(secs))
		}
		return writeOutcome(c, status, o)
	}
	slog.ErrorContext(c.Request().Context(), "lỗi nội bộ", "error", err, "method", c.Request().Method, "path", c.Request().URL.Path,
		"request_id", c.Response().Header().Get(echo.HeaderXRequestID))
	return writeOutcome(c, http.StatusInternalServerError, outcome("error", "exception", "Internal server error"))
}

// fhirPath cho biết đường dẫn trả lỗi dạng OperationOutcome (các route kiểu Medplum).
func fhirPath(p string) bool {
	return p == "/auth" || strings.HasPrefix(p, "/auth/") || p == "/admin" || strings.HasPrefix(p, "/admin/")
}

// errorHandler trả OperationOutcome cho /auth và /admin, JSON {"error"} cho phần còn lại. Lỗi 5xx không lộ chi tiết nội bộ.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	code, msg := http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
	var he *echo.HTTPError
	if errors.As(err, &he) {
		code = he.Code
		msg = http.StatusText(code)
		if s, ok := he.Message.(string); ok && code < 500 {
			msg = s
		}
		if code == http.StatusRequestEntityTooLarge {
			msg = "Request body too large" // như Medplum (contentTooLarge)
		}
	}
	if !fhirPath(c.Request().URL.Path) {
		_ = c.JSON(code, ErrorBody{Error: msg})
		return
	}
	_ = writeOutcome(c, code, outcome("error", codeFor(code), msg))
}
