// Command fhir-notifier giao thông báo Subscription: nghe sự kiện ghi resource do fhir-server phát lên Redis, khớp với Subscription đang hoạt động, giao rest-hook qua pkg/safehttp
// (chặn địa chỉ nội bộ) BỀN (đồng bộ trong handler, ACK sau khi giao xong hoặc bỏ cuộc, nên restart không mất thông báo) và, nếu bật FHIR_WS_MODE=issuer, phát sự kiện websocket
// lên kênh Redis cho ws-gateway. Dùng chung schema `fhir` với fhir-server.
//
//	fhir-notifier               chạy worker và cổng probe (:8010)
//	fhir-notifier healthcheck   GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
//
// Không có lệnh migrate: schema do fhir-server sở hữu (chạy `fhir-server migrate up`). Phía fhir-server đặt FHIR_SUBSCRIPTIONS=true và
// FHIR_SUBSCRIPTION_DELIVERY=false (API chỉ phát sự kiện, không giao) cùng BROKER_DRIVER=redis.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	observability "trivita-fhir/pkg/observability"

	"trivita-fhir/apps/fhir-notifier/internal/bootstrap"
	"trivita-fhir/apps/fhir-notifier/internal/config"
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
	return fmt.Errorf("cách dùng: fhir-notifier [healthcheck]")
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
