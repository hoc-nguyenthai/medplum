package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// HealthResponse là body của các probe còn sống.
type HealthResponse struct {
	Status string `json:"status" binding:"required" example:"ok"` // luôn là "ok" khi tiến trình còn sống
}

// ReadyResponse là body của /health/ready; Checks là kết quả từng phụ thuộc (true = sẵn sàng).
type ReadyResponse struct {
	Ready  bool            `json:"ready" binding:"required" example:"true"` // true khi mọi phụ thuộc sẵn sàng (HTTP 200); false kèm HTTP 503
	Checks map[string]bool `json:"checks" binding:"required"`               // kết quả từng phụ thuộc theo tên (true = sẵn sàng); rỗng khi chưa khai báo phụ thuộc nào
}

func registerHealth(e *echo.Echo, dep Deps) {
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

// live godoc
//
//	@Summary		Liveness probe
//	@Description	Trả {"status":"ok"} khi tiến trình còn sống; không kiểm tra phụ thuộc. /healthz và /health/startup trả cùng nội dung.
//	@Tags		health
//	@Produce		application/json
//	@Success	200	{object}	HealthResponse
//	@Router		/health/live [get]
func live(c echo.Context) error { return ok(c) }

// ready godoc
//
//	@Summary		Readiness probe (kiểm tra phụ thuộc)
//	@Description	Kiểm tra từng phụ thuộc đã khai báo (ví dụ DB, Redis); 200 khi tất cả sẵn sàng, 503 kèm cùng body khi có phụ thuộc chưa sẵn sàng.
//	@Tags		health
//	@Produce		application/json
//	@Success	200	{object}	ReadyResponse
//	@Failure	503	{object}	ReadyResponse
//	@Router		/health/ready [get]
func ready() {}
