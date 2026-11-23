// Command auth chạy dịch vụ xác thực và cấp token (port từ Medplum).
//
//	auth                   chạy HTTP server
//	auth migrate up|down|status
//	auth healthcheck       GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
//	auth seed project | client create | user set-password | purge-logins   lệnh quản trị (xem `auth help`)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo/v4"

	observability "github.com/TRIVITAAI/trivita-observability-go"
	"github.com/TRIVITAAI/trivita-observability-go/echo4obs"

	"trivita-fhir/apps/auth/internal/bootstrap"
	"trivita-fhir/apps/auth/internal/config"
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
	case len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		fmt.Println("auth [migrate up|down|status | healthcheck]\n" + bootstrap.AdminUsage + "\n" + bootstrap.KeysUsage)
		return nil
	case len(args) >= 1 && args[0] == "keys":
		return bootstrap.RunKeys(settings, args[1:], os.Stdout)
	case bootstrap.IsAdminCommand(args):
		return bootstrap.RunAdmin(ctx, settings, args, os.Getenv, os.Stdin, os.Stdout)
	case len(args) == 0:
		return serve(ctx, settings)
	case len(args) == 1 && args[0] == "healthcheck":
		return httpserve.HealthCheck(ctx, settings.Addr)
	case len(args) == 2 && args[0] == "migrate":
		return bootstrap.Migrate(ctx, settings, args[1], os.Stdout)
	}
	return fmt.Errorf("cách dùng: auth [migrate up|down|status | healthcheck | help]")
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
