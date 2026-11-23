package echoobs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	observability "trivita-fhir/pkg/observability"
	"trivita-fhir/pkg/observability/echoobs"
)

func newApp(t *testing.T, mode observability.SemconvMode) (*echo.Echo, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	s := observability.DefaultSettings()
	s.ServiceName = "echo-test"
	s.Enabled = true
	s.MetricsEnabled = true
	s.AccessLog = false
	s.Semconv = mode
	recorder := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()

	e := echo.New()
	if err := echoobs.Setup(context.Background(), e, s,
		observability.WithSpanProcessor(recorder), observability.WithMetricReader(reader)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { observability.Shutdown(time.Second) })

	e.GET("/orders/:id", func(c *echo.Context) error {
		_, child := observability.Tracer("test").Start(c.Request().Context(), "db query")
		child.End()
		return c.String(http.StatusOK, "order "+c.Param("id"))
	})
	e.GET("/health", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
	e.GET("/boom", func(c *echo.Context) error { return errors.New("db down") })
	e.GET("/teapot", func(c *echo.Context) error { return echo.NewHTTPError(http.StatusTeapot, "no") })
	return e, recorder, reader
}

func serve(e *echo.Echo, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func serverSpans(recorder *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.SpanKind() == trace.SpanKindServer {
			out = append(out, s)
		}
	}
	return out
}

func attr(span sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestOneServerSpanPerRequestNamedByRoute(t *testing.T) {
	e, recorder, _ := newApp(t, observability.SemconvStable)
	serve(e, http.MethodGet, "/orders/42?full=1", nil)
	serve(e, http.MethodGet, "/health", nil)

	spans := serverSpans(recorder)
	if len(spans) != 1 {
		t.Fatalf("got %d server spans, want 1 (/health is excluded)", len(spans))
	}
	span := spans[0]
	if span.Name() != "GET /orders/:id" {
		t.Fatalf("span name %q", span.Name())
	}
	for key, want := range map[string]string{
		"http.route": "/orders/:id", "http.request.method": "GET", "http.response.status_code": "200",
		"url.path": "/orders/42", "url.query": "full=1", "url.scheme": "http",
	} {
		if v, _ := attr(span, key); v.Emit() != want {
			t.Errorf("%s = %q, want %q", key, v.Emit(), want)
		}
	}
	if _, ok := attr(span, "http.method"); ok {
		t.Error("old attribute http.method emitted in stable mode")
	}
	db := recorder.Ended()[0]
	if db.Name() != "db query" || db.Parent().SpanID() != span.SpanContext().SpanID() {
		t.Fatal("handler span is not a child of the server span")
	}
}

func TestIncomingTraceparentIsContinued(t *testing.T) {
	e, recorder, _ := newApp(t, observability.SemconvStable)
	serve(e, http.MethodGet, "/orders/1", map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	})
	span := serverSpans(recorder)[0]
	if span.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || span.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("trace not continued: %s parent %s", span.SpanContext().TraceID(), span.Parent().SpanID())
	}
}

func TestErrorsAndStatusCodes(t *testing.T) {
	e, recorder, _ := newApp(t, observability.SemconvStable)
	if rec := serve(e, http.MethodGet, "/boom", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("/boom status %d", rec.Code)
	}
	if rec := serve(e, http.MethodGet, "/teapot", nil); rec.Code != http.StatusTeapot {
		t.Fatalf("/teapot status %d", rec.Code)
	}
	serve(e, http.MethodGet, "/nope", nil)

	spans := serverSpans(recorder)
	boom, teapot, missing := spans[0], spans[1], spans[2]
	if boom.Status().Code != codes.Error {
		t.Errorf("/boom status %v", boom.Status())
	}
	if v, _ := attr(boom, "http.response.status_code"); v.AsInt64() != 500 {
		t.Errorf("/boom status_code %v", v.Emit())
	}
	if teapot.Status().Code != codes.Unset {
		t.Errorf("4xx marked as error: %v", teapot.Status())
	}
	if v, _ := attr(teapot, "http.response.status_code"); v.AsInt64() != 418 {
		t.Errorf("/teapot status_code %v", v.Emit())
	}
	if missing.Name() != "GET" {
		t.Errorf("unmatched route span name %q, want GET", missing.Name())
	}
	if _, ok := attr(missing, "http.route"); ok {
		t.Error("unmatched route has http.route")
	}
}

func TestStableMetrics(t *testing.T) {
	e, _, reader := newApp(t, observability.SemconvStable)
	serve(e, http.MethodGet, "/orders/1", nil)
	serve(e, http.MethodGet, "/orders/2", nil)
	serve(e, http.MethodGet, "/health", nil)

	rm := collect(t, reader)
	if _, ok := find(rm, "http.server.duration"); ok {
		t.Fatal("old metric emitted in stable mode")
	}
	m, ok := find(rm, "http.server.request.duration")
	if !ok || m.Unit != "s" {
		t.Fatalf("http.server.request.duration missing or wrong unit: %+v", m)
	}
	points := m.Data.(metricdata.Histogram[float64]).DataPoints
	if len(points) != 1 || points[0].Count != 2 {
		t.Fatalf("points: %+v", points)
	}
	for key, want := range map[string]string{"http.route": "/orders/:id", "http.response.status_code": "200", "http.request.method": "GET"} {
		if v, _ := points[0].Attributes.Value(attribute.Key(key)); v.Emit() != want {
			t.Errorf("%s = %q, want %q", key, v.Emit(), want)
		}
	}
	if len(points[0].Exemplars) == 0 {
		t.Error("no exemplar pointing to the trace")
	}
	for _, name := range []string{"http.server.active_requests", "http.server.response.body.size"} {
		if _, ok := find(rm, name); !ok {
			t.Errorf("%s missing", name)
		}
	}
}

func TestOldAndDupSemconv(t *testing.T) {
	e, recorder, reader := newApp(t, observability.SemconvOld)
	serve(e, http.MethodGet, "/orders/1", nil)
	span := serverSpans(recorder)[0]
	if v, _ := attr(span, "http.target"); v.Emit() != "/orders/1" {
		t.Errorf("http.target = %q", v.Emit())
	}
	if _, ok := attr(span, "http.request.method"); ok {
		t.Error("stable attribute in old mode")
	}
	rm := collect(t, reader)
	if m, ok := find(rm, "http.server.duration"); !ok || m.Unit != "ms" {
		t.Fatal("http.server.duration (ms) missing in old mode")
	}
	if _, ok := find(rm, "http.server.request.duration"); ok {
		t.Fatal("stable metric in old mode")
	}
	observability.Shutdown(time.Second)

	e, _, reader = newApp(t, observability.SemconvDup)
	serve(e, http.MethodGet, "/orders/1", nil)
	rm = collect(t, reader)
	for _, name := range []string{"http.server.duration", "http.server.request.duration"} {
		if _, ok := find(rm, name); !ok {
			t.Errorf("dup mode: %s missing", name)
		}
	}
}

func TestPanicEndsSpan(t *testing.T) {
	e, recorder, _ := newApp(t, observability.SemconvStable)
	e.GET("/panic", func(c *echo.Context) error { panic("bad") })
	func() {
		defer func() { _ = recover() }()
		serve(e, http.MethodGet, "/panic", nil)
	}()
	spans := serverSpans(recorder)
	if len(spans) != 1 || spans[0].Status().Code != codes.Error {
		t.Fatalf("panic span: %+v", spans)
	}
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return rm
}

func find(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}
