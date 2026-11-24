// Command dicomweb phục vụ DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM; dùng chung schema và kho Binary với fhir-server.
//
//	dicomweb               chạy HTTP server
//	dicomweb reindex [--dry-run] [--force] [--limit N]
//	                       lập chỉ mục frame cho instance chưa có (lưu trước D5, chỉ mục hỏng); an toàn khi chạy lại
//	dicomweb healthcheck   GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
//
// dicomweb không có lệnh migrate: schema do fhir-server sở hữu (chạy `fhir-server migrate up`).
package main

import (
	"context"
	"flag"
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
	"trivita-fhir/pkg/dicomretrieve"
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
	case args[0] == "reindex":
		return reindex(ctx, settings, args[1:])
	}
	return fmt.Errorf("cách dùng: dicomweb [healthcheck | reindex [--dry-run] [--force] [--limit N]]")
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

func reindex(ctx context.Context, settings config.Settings, args []string) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "chỉ đếm, không ghi")
	force := fs.Bool("force", false, "lập lại cả chỉ mục đã khớp")
	limit := fs.Int("limit", 0, "dừng sau chừng ấy instance cần lập (0: không giới hạn)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rep, err := bootstrap.Reindex(ctx, settings, dicomretrieve.ReindexOptions{DryRun: *dry, Force: *force, Limit: *limit})
	fmt.Printf("đã duyệt %d, lập chỉ mục %d, đã đủ %d, không có ảnh %d, không lập được %d, lỗi %d (dry-run=%v)\n", rep.Scanned, rep.Indexed, rep.UpToDate, rep.NoPixels, rep.Unindexed, rep.Failed, *dry)
	if err == nil && rep.Failed > 0 {
		err = fmt.Errorf("%d instance lỗi: chạy lại sau khi xem log", rep.Failed)
	}
	return err
}
