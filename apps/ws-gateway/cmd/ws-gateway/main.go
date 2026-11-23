// Command ws-gateway giữ các kết nối WebSocket của Subscription (subscriptions-r4) và đẩy thông báo do fhir-server khớp và
// phát qua Redis. Không có cơ sở dữ liệu: xem README.
//
//	ws-gateway               chạy HTTP/WebSocket server
//	ws-gateway healthcheck   GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/metric"

	observability "github.com/TRIVITAAI/trivita-observability-go"
	"github.com/TRIVITAAI/trivita-observability-go/echo4obs"

	"trivita-fhir/apps/ws-gateway/internal/bootstrap"
	"trivita-fhir/apps/ws-gateway/internal/config"
	"trivita-fhir/pkg/httpserve"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "lỗi:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		return serve(ctx, settings)
	case len(args) == 1 && args[0] == "healthcheck":
		return httpserve.HealthCheck(ctx, settings.Addr)
	}
	return fmt.Errorf("cách dùng: ws-gateway [healthcheck]")
}

func serve(ctx context.Context, settings config.Settings) error {
	obs := observability.FromEnv()
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		obs.ServiceName = settings.AppName
	}
	return bootstrap.Serve(ctx, settings, bootstrap.ServeOptions{
		Instrument: func(e *echo.Echo) error {
			if err := echo4obs.Setup(ctx, e, obs); err != nil {
				slog.Error("observability setup lỗi, service vẫn chạy tiếp", "error", err)
			}
			return nil
		},
		Meter: func() metric.Meter { return observability.Meter(settings.AppName) },
		Flush: func() { observability.Shutdown(0) },
	})
}
