// Package httpserve chạy http.Handler với vòng đời K8s: readiness=503 → drain → Shutdown → đóng tài nguyên.
package httpserve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Options cấu hình Run.
type Options struct {
	Addr              string
	Handler           http.Handler
	ReadHeaderTimeout time.Duration
	// DrainDelay: chờ sau khi BeginShutdown để K8s gỡ pod khỏi endpoints (vẫn phục vụ request).
	DrainDelay time.Duration
	// ShutdownTimeout: thời gian tối đa chờ request đang chạy và Stop.
	ShutdownTimeout time.Duration
	// BeginShutdown: đặt readiness=503 (có thể nil).
	BeginShutdown func()
	// Stop đóng tài nguyên sau khi server đã dừng (có thể nil).
	Stop func(ctx context.Context) error
	// OnListen được gọi khi đã nghe cổng (test dùng Addr ":0").
	OnListen func(addr net.Addr)
}

// Run phục vụ tới khi ctx bị hủy (SIGTERM/SIGINT) rồi tắt êm. Tổng DrainDelay + ShutdownTimeout phải nhỏ hơn
// terminationGracePeriodSeconds của K8s. Stop luôn được gọi, kể cả khi không nghe được cổng.
func Run(ctx context.Context, o Options) error {
	stop := func(c context.Context) error {
		if o.Stop == nil {
			return nil
		}
		return o.Stop(c)
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return errors.Join(fmt.Errorf("nghe %s: %w", o.Addr, err), stop(context.Background()))
	}
	srv := &http.Server{Handler: o.Handler, ReadHeaderTimeout: o.ReadHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	slog.InfoContext(ctx, "service đã sẵn sàng", "addr", ln.Addr().String())
	if o.OnListen != nil {
		o.OnListen(ln.Addr())
	}

	select {
	case err := <-serveErr:
		return errors.Join(err, stop(context.Background()))
	case <-ctx.Done():
	}

	slog.Info("bắt đầu tắt", "drain_delay", o.DrainDelay.String())
	if o.BeginShutdown != nil {
		o.BeginShutdown()
	}
	time.Sleep(o.DrainDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), o.ShutdownTimeout)
	defer cancel()
	var errs []error
	if err := srv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("shutdown http: %w", err))
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, err)
	}
	if err := stop(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("đóng tài nguyên: %w", err))
	}
	return errors.Join(errs...)
}

// HealthCheck gọi /healthz của chính tiến trình đang chạy trên cùng cổng (cho HEALTHCHECK của Docker;
// image distroless không có curl).
func HealthCheck(ctx context.Context, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz trả %d", resp.StatusCode)
	}
	return nil
}
