package observability

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// receiver is an in-process OTLP/gRPC backend standing in for Data Prepper.
type receiver struct {
	coltrace.UnimplementedTraceServiceServer
	collogs.UnimplementedLogsServiceServer
	colmetrics.UnimplementedMetricsServiceServer

	mu      sync.Mutex
	traces  []*coltrace.ExportTraceServiceRequest
	logs    []*collogs.ExportLogsServiceRequest
	metrics []*colmetrics.ExportMetricsServiceRequest
}

func (r *receiver) Export(_ context.Context, req *coltrace.ExportTraceServiceRequest) (*coltrace.ExportTraceServiceResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.traces = append(r.traces, req)
	return &coltrace.ExportTraceServiceResponse{}, nil
}

type logsReceiver struct{ *receiver }

func (r logsReceiver) Export(_ context.Context, req *collogs.ExportLogsServiceRequest) (*collogs.ExportLogsServiceResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, req)
	return &collogs.ExportLogsServiceResponse{}, nil
}

type metricsReceiver struct{ *receiver }

func (r metricsReceiver) Export(_ context.Context, req *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, req)
	return &colmetrics.ExportMetricsServiceResponse{}, nil
}

// startReceiver listens on three ports, like Data Prepper's three sources.
func startReceiver(t *testing.T) (r *receiver, traces, logs, metrics string) {
	t.Helper()
	r = &receiver{}
	listen := func(register func(*grpc.Server)) string {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := grpc.NewServer()
		register(srv)
		go srv.Serve(lis)
		t.Cleanup(srv.Stop)
		return "grpc://" + lis.Addr().String()
	}
	traces = listen(func(s *grpc.Server) { coltrace.RegisterTraceServiceServer(s, r) })
	logs = listen(func(s *grpc.Server) { collogs.RegisterLogsServiceServer(s, logsReceiver{r}) })
	metrics = listen(func(s *grpc.Server) { colmetrics.RegisterMetricsServiceServer(s, metricsReceiver{r}) })
	return r, traces, logs, metrics
}

func resourceAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

func intAttr(attrs []*commonpb.KeyValue, key string) int64 {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetIntValue()
		}
	}
	return 0
}

func TestExportOverGRPC(t *testing.T) {
	captureStdout(t)
	r, traces, logs, metrics := startReceiver(t)
	s := testSettings()
	s.TracesEndpoint, s.LogsEndpoint, s.MetricsEndpoint = traces, logs, metrics
	s.LogsEnabled, s.MetricsEnabled = true, true
	s.Headers = map[string]string{"x-tenant": "trivita"}
	if err := Setup(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	ctx, span := Tracer("test").Start(context.Background(), "GET /it")
	slog.InfoContext(ctx, "order created", "order_id", 123)
	counter, _ := Meter("app").Int64Counter("orders.created")
	counter.Add(ctx, 1)
	span.End()
	if !Shutdown(5 * time.Second) {
		t.Fatal("Shutdown timed out with a healthy backend")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.traces) == 0 || len(r.logs) == 0 || len(r.metrics) == 0 {
		t.Fatalf("received traces=%d logs=%d metrics=%d", len(r.traces), len(r.logs), len(r.metrics))
	}
	rs := r.traces[0].ResourceSpans[0]
	if resourceAttr(rs.Resource.Attributes, "service.name") != "test-svc" || resourceAttr(rs.Resource.Attributes, "deployment.environment") != "test" {
		t.Fatalf("trace resource: %v", rs.Resource.Attributes)
	}
	gotSpan := rs.ScopeSpans[0].Spans[0]

	var found bool
	for _, rl := range r.logs[0].ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				if lr.Body.GetStringValue() == "order created" {
					found = string(lr.TraceId) == string(gotSpan.TraceId) && intAttr(lr.Attributes, "order_id") == 123 &&
						resourceAttr(rl.Resource.Attributes, "service.name") == "test-svc"
				}
			}
		}
	}
	if !found {
		t.Fatalf("log record with the span's trace id not received: %v", r.logs)
	}
	if resourceAttr(r.metrics[0].ResourceMetrics[0].Resource.Attributes, "service.namespace") != "trivita" {
		t.Fatal("metrics resource lacks service.namespace")
	}
}

func TestExportOverHTTP(t *testing.T) {
	captureStdout(t)
	var mu sync.Mutex
	var got []*coltrace.ExportTraceServiceRequest
	var path, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var msg coltrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		got, path, contentType = append(got, &msg), req.URL.Path, req.Header.Get("Content-Type")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		out, _ := proto.Marshal(&coltrace.ExportTraceServiceResponse{})
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)

	const exportPath = "/opentelemetry.proto.collector.trace.v1.TraceService/Export"
	s := testSettings()
	s.TracesEndpoint = srv.URL + exportPath
	if err := Setup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	_, span := Tracer("test").Start(context.Background(), "GET /it")
	span.End()
	if !Shutdown(5 * time.Second) {
		t.Fatal("Shutdown timed out")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || path != exportPath || contentType != "application/x-protobuf" {
		t.Fatalf("received %d requests at %q (%s)", len(got), path, contentType)
	}
	if name := got[0].ResourceSpans[0].ScopeSpans[0].Spans[0].Name; name != "GET /it" {
		t.Fatalf("span name %q", name)
	}
}
