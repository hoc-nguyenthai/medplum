package httpserve_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/httpserve"
)

func handler(draining *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if draining.Load() {
			w.WriteHeader(503)
		}
	})
	return mux
}

func status(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRunGracefulShutdownOrder(t *testing.T) {
	var draining atomic.Bool
	var order []string
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- httpserve.Run(ctx, httpserve.Options{
			Addr: "127.0.0.1:0", Handler: handler(&draining),
			DrainDelay: 100 * time.Millisecond, ShutdownTimeout: 2 * time.Second,
			BeginShutdown: func() { order = append(order, "begin"); draining.Store(true) },
			Stop:          func(context.Context) error { order = append(order, "stop"); return nil },
			OnListen:      func(a net.Addr) { addr <- a },
		})
	}()
	a := <-addr
	base := "http://" + a.String()
	if status(t, base+"/readyz") != 200 {
		t.Fatal("readyz")
	}
	if err := httpserve.HealthCheck(context.Background(), a.String()); err != nil {
		t.Fatalf("healthcheck: %v", err)
	}

	cancel() // giả lập SIGTERM
	time.Sleep(30 * time.Millisecond)
	if status(t, base+"/readyz") != 503 { // đang drain vẫn phục vụ nhưng báo not ready
		t.Fatal("trong lúc drain readyz phải 503")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run không thoát")
	}
	if len(order) != 2 || order[0] != "begin" || order[1] != "stop" {
		t.Fatalf("thứ tự = %v", order)
	}
}

func TestRunListenFailureStillStops(t *testing.T) {
	var stopped bool
	err := httpserve.Run(context.Background(), httpserve.Options{
		Addr: "256.0.0.1:1", Handler: http.NotFoundHandler(),
		Stop: func(context.Context) error { stopped = true; return nil },
	})
	if err == nil || !stopped {
		t.Fatalf("err=%v stopped=%v", err, stopped)
	}
}

func TestRunReportsStopAndShutdownErrors(t *testing.T) {
	boom := errors.New("đóng lỗi")
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- httpserve.Run(ctx, httpserve.Options{
			Addr: "127.0.0.1:0", Handler: http.NotFoundHandler(), ShutdownTimeout: time.Second,
			Stop: func(context.Context) error { return boom }, OnListen: func(a net.Addr) { addr <- a },
		})
	}()
	<-addr
	cancel()
	if err := <-done; !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestHealthCheckFailures(t *testing.T) {
	if err := httpserve.HealthCheck(context.Background(), "không-có-cổng"); err == nil {
		t.Fatal("addr sai")
	}
	if err := httpserve.HealthCheck(context.Background(), "127.0.0.1:1"); err == nil {
		t.Fatal("không có server phải lỗi")
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	defer srv.Close()
	if err := httpserve.HealthCheck(context.Background(), ln.Addr().String()); err == nil {
		t.Fatal("500 phải lỗi")
	}
}
