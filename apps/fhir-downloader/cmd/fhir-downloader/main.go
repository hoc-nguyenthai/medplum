// Command fhir-downloader tự tải attachment có URL NGOÀI về kho Binary: nghe sự kiện ghi resource do fhir-server phát lên Redis, tải qua pkg/safehttp (chặn địa chỉ
// nội bộ), ghi Binary vào đúng project rồi đổi `url` của attachment thành `Binary/<id>`. Dùng chung schema `fhir` và kho Binary với fhir-server.
//
//	fhir-downloader               chạy worker và cổng probe (:8009)
//	fhir-downloader healthcheck   GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
//
// Không có lệnh migrate: schema do fhir-server sở hữu (chạy `fhir-server migrate up`). Phía fhir-server đặt FHIR_AUTO_DOWNLOAD=true và
// FHIR_AUTO_DOWNLOAD_WORKER=false (API chỉ phát sự kiện, không gọi ra Internet) cùng BROKER_DRIVER=redis.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	observability "trivita-fhir/pkg/observability"

	"trivita-fhir/apps/fhir-downloader/internal/bootstrap"
	"trivita-fhir/apps/fhir-downloader/internal/config"
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
	return fmt.Errorf("cách dùng: fhir-downloader [healthcheck]")
}

func serve(ctx context.Context, settings config.Settings) error {
	obs := observability.FromEnv()
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		obs.ServiceName = settings.AppName
	}
	if err := observability.Setup(ctx, obs); err != nil {
		slog.Error("observability setup lỗi, service vẫn chạy tiếp", "error", err)
	}
	return bootstrap.Serve(ctx, settings, bootstrap.ServeOptions{Flush: func() { observability.Shutdown(0) }})
}
