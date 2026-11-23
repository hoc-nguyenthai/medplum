// Command echo is a minimal Echo v5 service wired with trivita-observability-go.
//
//	OTEL_ENABLED=true OTEL_SERVICE_NAME=orders-api go run ./examples/echo
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"

	observability "trivita-fhir/pkg/observability"
	"trivita-fhir/pkg/observability/echoobs"
)

var (
	logger           = observability.Logger("orders")
	ordersCreated, _ = observability.Meter("orders").Int64Counter("orders.created")
	client           = observability.HTTPClient()
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	e := echo.New()
	if err := echoobs.Setup(ctx, e, observability.FromEnv()); err != nil {
		slog.Error("observability setup failed", "error", err)
	}
	defer observability.Shutdown(0)

	e.GET("/health", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
	e.POST("/orders/:id", createOrder)

	srv := &http.Server{Addr: ":8080", Handler: e}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "error", err)
	}
}

func createOrder(c *echo.Context) error {
	ctx := c.Request().Context() // carries the request span
	logger.InfoContext(ctx, "order created", "order_id", c.Param("id"))
	ordersCreated.Add(ctx, 1)

	// Outgoing call: CLIENT span + traceparent header, because the request uses ctx.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
	return c.JSON(http.StatusCreated, map[string]string{"id": c.Param("id")})
}
