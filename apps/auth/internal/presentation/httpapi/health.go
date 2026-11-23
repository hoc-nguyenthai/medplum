package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// HealthResponse là body của các probe còn sống.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// ReadyResponse là body của /health/ready; Checks là kết quả từng phụ thuộc (true = sẵn sàng).
type ReadyResponse struct {
	Ready  bool            `json:"ready" example:"true"`
	Checks map[string]bool `json:"checks"`
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
//	@Summary	Liveness probe
//	@Tags		health
//	@Produce	json
//	@Success	200	{object}	HealthResponse
//	@Router		/health/live [get]
func live(c echo.Context) error { return ok(c) }

// ready godoc
//
//	@Summary	Readiness probe (kiểm tra phụ thuộc)
//	@Tags		health
//	@Produce	json
//	@Success	200	{object}	ReadyResponse
//	@Failure	503	{object}	ReadyResponse
//	@Router		/health/ready [get]
func ready() {}
