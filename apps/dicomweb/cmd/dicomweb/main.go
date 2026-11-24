// Command dicomweb phục vụ DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM; dùng chung schema và kho Binary với fhir-server.
//
//	dicomweb               chạy HTTP server
//	dicomweb healthcheck   GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
//
// dicomweb không có lệnh migrate: schema do fhir-server sở hữu (chạy `fhir-server migrate up`).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo/v4"

	observability "trivita-fhir/pkg/observability"
	"trivita-fhir/pkg/observability/echo4obs"

	"trivita-fhir/apps/dicomweb/internal/bootstrap"
	"trivita-fhir/apps/dicomweb/internal/config"
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
	return fmt.Errorf("cách dùng: dicomweb [healthcheck]")
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
		Flush: func() { observability.Shutdown(0) },
	})
}
