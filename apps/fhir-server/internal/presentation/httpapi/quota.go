package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/ratelimit"
)

// Điểm mỗi yêu cầu của hạn mức theo người gọi và project (FHIR_QUOTA_USER, FHIR_QUOTA_PROJECT). Tính theo HÌNH DẠNG yêu cầu (method và đường dẫn), không theo thời gian
// xử lý, để đoán trước được và rẻ; hiệu chỉnh theo tải thật là việc còn lại (bảng điểm cố định, chưa cấu hình được).
const (
	CostRead      = 1  // đọc, vread, đọc tệp theo id
	CostWrite     = 2  // create, update, patch, delete
	CostSearch    = 5  // tìm kiếm, history, đọc phiên bản
	CostOperation = 10 // operation `$...`
	CostBundle    = 20 // POST Bundle batch/transaction ở gốc
)

// rateLimitsSuffix là đường dẫn đọc tình trạng hạn mức: GET /Project/{id}/$rate-limits.
const rateLimitsSuffix = "/$rate-limits"

// QuotaCost trả số điểm của một yêu cầu; 0 cho yêu cầu không tính điểm (đọc tình trạng hạn mức, CapabilityStatement, OPTIONS).
func QuotaCost(method, path string) int {
	rel := strings.Trim(strings.TrimPrefix(strings.TrimRight(path, "/"), Prefix), "/")
	if rel == "" {
		if method == http.MethodPost {
			return CostBundle
		}
		return CostSearch
	}
	if rel == "metadata" || strings.HasSuffix("/"+rel, rateLimitsSuffix) || method == http.MethodOptions {
		return 0
	}
	segs := strings.Split(rel, "/")
	if strings.Contains(rel, "/$") || strings.HasPrefix(rel, "$") {
		return CostOperation
	}
	if segs[len(segs)-1] == "_search" || (len(segs) >= 3 && segs[2] == "_history") || segs[len(segs)-1] == "_history" {
		return CostSearch
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		if len(segs) == 1 {
			return CostSearch // GET /Patient là tìm kiếm
		}
		return CostRead
	}
	return CostWrite
}

// QuotaOptions cấu hình hạn mức điểm; User và Project <= 0 là không áp.
type QuotaOptions struct {
	Store         ratelimit.Quota
	User, Project int
	Window        time.Duration
}

func (o QuotaOptions) enabled() bool { return o.Store != nil && (o.User > 0 || o.Project > 0) }

func userKey(p *fhirauth.Principal) string { return "u:" + p.Project + ":" + p.Subject }
func projectKey(project string) string     { return "p:" + project }

func seconds(d time.Duration) int { return int(math.Ceil(d.Seconds())) }

// quotaMiddleware trừ điểm của người gọi rồi của project (đứng SAU Guard để có Principal), gắn `RateLimit-Limit|Remaining|Reset` của hạn mức còn ít điểm hơn vào
// mọi phản hồi và trả 429 kèm `Retry-After` khi hết điểm. Không có Principal (metadata, chế độ không xác thực) hoặc lỗi kho thì cho qua (fail-open). Điểm của người gọi đã
// trừ khi project hết điểm không được hoàn lại (sai lệch nhỏ, chấp nhận).
func quotaMiddleware(o QuotaOptions) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			p, ok := fhirauth.PrincipalFrom(req.Context())
			cost := QuotaCost(req.Method, req.URL.Path)
			if !ok || cost == 0 {
				return next(c)
			}
			var tightest *ratelimit.Status
			for _, lim := range []struct {
				key   string
				limit int
				skip  bool
			}{
				{userKey(p), o.User, o.User <= 0},
				{projectKey(p.Project), o.Project, o.Project <= 0 || p.Project == ""},
			} {
				if lim.skip {
					continue
				}
				st, err := o.Store.Consume(lim.key, cost, lim.limit, o.Window)
				if err != nil {
					continue
				}
				if tightest == nil || st.Remaining < tightest.Remaining {
					s := st
					tightest = &s
				}
				if !st.Allowed {
					setQuotaHeaders(c, st)
					c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, seconds(st.Reset))))
					return echo.NewHTTPError(http.StatusTooManyRequests, "vượt hạn mức, thử lại sau")
				}
			}
			if tightest != nil {
				setQuotaHeaders(c, *tightest)
			}
			return next(c)
		}
	}
}

func setQuotaHeaders(c echo.Context, st ratelimit.Status) {
	h := c.Response().Header()
	h.Set("RateLimit-Limit", strconv.Itoa(st.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(st.Remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(seconds(st.Reset)))
}

func intParam(name string, v int) map[string]any {
	return map[string]any{"name": name, "valueInteger": v}
}

// rateLimitsHandler trả tình trạng hạn mức (Project/{id}/$rate-limits) dạng Parameters, KHÔNG trừ điểm: `enabled`, `windowSeconds`, và với mỗi hạn mức đang bật
// `{user,project}Limit|Remaining|ResetSeconds` (user: của chính người gọi). Chỉ quản trị của đúng project đó (RequiredRole đã đòi RoleAdmin); project khác là 403.
func rateLimitsHandler(o QuotaOptions) echo.HandlerFunc {
	return func(c echo.Context) error {
		p, ok := fhirauth.PrincipalFrom(c.Request().Context())
		if !ok || p.Project == "" || c.Param("id") != p.Project {
			return echo.NewHTTPError(http.StatusForbidden, "Chỉ quản trị của chính project này được xem hạn mức")
		}
		params := []map[string]any{{"name": "enabled", "valueBoolean": o.enabled()}}
		if o.enabled() {
			params = append(params, intParam("windowSeconds", seconds(o.Window)))
			add := func(prefix, key string, limit int) {
				if limit <= 0 {
					return
				}
				st, err := o.Store.Peek(key, limit, o.Window)
				if err != nil {
					return
				}
				params = append(params, intParam(prefix+"Limit", st.Limit), intParam(prefix+"Remaining", st.Remaining), intParam(prefix+"ResetSeconds", seconds(st.Reset)))
			}
			add("user", userKey(p), o.User)
			add("project", projectKey(p.Project), o.Project)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		c.Response().Header().Set(echo.HeaderContentType, "application/fhir+json; charset=utf-8")
		return c.JSON(http.StatusOK, map[string]any{"resourceType": "Parameters", "parameter": params})
	}
}
