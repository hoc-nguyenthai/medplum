// Command fhir-worker chạy job nền của fhir-server: bulk export, `$reindex` cả loại, batch async (Prefer: respond-async) và vòng bảo trì (tiếp quản job mồ côi, xóa kết quả hết hạn).
// fhir-server (với FHIR_JOB_EXECUTION=false) chỉ nhận yêu cầu và tạo AsyncJob (202); fhir-worker nhận job qua vòng bảo trì và lease trong CSDL, chạy bằng đúng quyền và project của
// người đã tạo job (chữ ký job-seal với FHIR_JOB_SIGNING_KEY dùng chung). Dùng chung schema `fhir` và kho Binary với fhir-server.
//
//	fhir-worker               chạy bộ chạy job và cổng probe (:8011)
//	fhir-worker healthcheck   GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
//
// Không có lệnh migrate: schema do fhir-server sở hữu (chạy `fhir-server migrate up`).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	observability "trivita-fhir/pkg/observability"

	"trivita-fhir/apps/fhir-worker/internal/bootstrap"
	"trivita-fhir/apps/fhir-worker/internal/config"
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
	return fmt.Errorf("cách dùng: fhir-worker [healthcheck]")
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
