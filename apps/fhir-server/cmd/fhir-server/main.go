// Command fhir-server chạy máy chủ FHIR R4 (REST, search, history, batch/transaction) trên PostgreSQL hoặc SQLite.
//
//	fhir-server                   chạy HTTP server
//	fhir-server migrate up|down|status
//	fhir-server assign-project -project <id> [-type Patient,Observation] [-dry-run]
//	                              gán project cho dữ liệu cũ chưa có project (chạy khi bật tách theo project)
//	fhir-server sweep-binary [-grace 24h] [-max-delete 1000] [-dry-run] [-force]
//	                              xóa nội dung Binary mồ côi trong kho (chạy định kỳ bằng cron; cần FHIR_BINARY_STORAGE)
//	fhir-server healthcheck       GET /healthz (cho HEALTHCHECK của Docker, image distroless không có curl)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/labstack/echo/v4"

	observability "github.com/TRIVITAAI/trivita-observability-go"
	"github.com/TRIVITAAI/trivita-observability-go/echo4obs"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhirbinary"
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
	case len(args) == 2 && args[0] == "migrate":
		return bootstrap.Migrate(ctx, settings, args[1], os.Stdout)
	case len(args) > 0 && args[0] == "assign-project":
		fs := flag.NewFlagSet("assign-project", flag.ContinueOnError)
		project := fs.String("project", "", "project gán cho resource chưa có project (bắt buộc)")
		types := fs.String("type", "", "chỉ quét các loại này, cách nhau bằng dấu phẩy (mặc định mọi loại)")
		dry := fs.Bool("dry-run", false, "chỉ đếm, không ghi")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var ts []string
		for _, t := range strings.Split(*types, ",") {
			if t = strings.TrimSpace(t); t != "" {
				ts = append(ts, t)
			}
		}
		return bootstrap.AssignProject(ctx, settings, *project, ts, *dry, os.Stdout)
	case len(args) > 0 && args[0] == "sweep-binary":
		fs := flag.NewFlagSet("sweep-binary", flag.ContinueOnError)
		grace := fs.Duration("grace", fhirbinary.DefaultSweepGrace, "chỉ xét nội dung cũ hơn mức này (tối thiểu 1h; phải dài hơn mọi transaction)")
		max := fs.Int("max-delete", 1000, "trần số khóa xóa mỗi lượt")
		dry := fs.Bool("dry-run", false, "chỉ đếm và liệt kê, không xóa")
		force := fs.Bool("force", false, "bỏ chốt chặn khi quá nửa số khóa là mồ côi (kiểm kỹ DATABASE_URL trước)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return bootstrap.SweepBinary(ctx, settings, fhirbinary.SweepOptions{Grace: *grace, MaxDelete: *max, DryRun: *dry, Force: *force}, os.Stdout)
	}
	return fmt.Errorf("cách dùng: fhir-server [migrate up|down|status | assign-project ... | sweep-binary ... | healthcheck]")
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
