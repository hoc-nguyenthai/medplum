package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestDefaultEndpointsAreSeparateGRPCPorts(t *testing.T) {
	for _, key := range []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"} {
		t.Setenv(key, "")
	}
	s := FromEnv()
	if s.TracesEndpoint != "grpc://localhost:21890" || s.LogsEndpoint != "grpc://localhost:21892" || s.MetricsEndpoint != "grpc://localhost:21891" {
		t.Fatalf("defaults: %q %q %q", s.TracesEndpoint, s.LogsEndpoint, s.MetricsEndpoint)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "grpc://data-prepper:21890")
	if s := FromEnv(); s.LogsEndpoint != DefaultLogsEndpoint {
		t.Fatalf("changing the traces endpoint changed the logs endpoint: %q", s.LogsEndpoint)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv("OTEL_SERVICE_NAME", "billing-api")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "a=1, b = 2,broken")
	t.Setenv("OTEL_TRACES_SAMPLE_RATIO", "0.2")
	t.Setenv("OTEL_SHUTDOWN_TIMEOUT_MS", "2000")
	t.Setenv("OTEL_EXCLUDED_URLS", "health, ready ,")
	s := FromEnv()
	if !s.Enabled || s.ServiceName != "billing-api" || s.TracesSampleRatio != 0.2 || s.ShutdownTimeout != 2*time.Second {
		t.Fatalf("settings: %+v", s)
	}
	if s.Headers["a"] != "1" || s.Headers["b"] != "2" || len(s.Headers) != 2 {
		t.Fatalf("headers: %v", s.Headers)
	}
	if !slices.Equal(s.ExcludedURLs, []string{"health", "ready"}) {
		t.Fatalf("excluded: %v", s.ExcludedURLs)
	}
}

func TestSemconvFromEnv(t *testing.T) {
	cases := map[string]SemconvMode{"http": SemconvStable, "http/dup": SemconvDup, "": SemconvOld, "database,http": SemconvStable}
	for raw, want := range cases {
		t.Setenv("OTEL_SEMCONV_STABILITY_OPT_IN", raw)
		if got := FromEnv().Semconv; got != want {
			t.Errorf("OTEL_SEMCONV_STABILITY_OPT_IN=%q: got %q, want %q", raw, got, want)
		}
	}
	os.Unsetenv("OTEL_SEMCONV_STABILITY_OPT_IN")
	if got := FromEnv().Semconv; got != SemconvStable {
		t.Errorf("unset: got %q", got)
	}
}

func TestParseEndpoint(t *testing.T) {
	ep, err := parseEndpoint("grpc://data-prepper:21890")
	if err != nil || !ep.grpc || !ep.insecure || ep.hostPort != "data-prepper:21890" {
		t.Fatalf("grpc: %+v %v", ep, err)
	}
	ep, err = parseEndpoint("grpcs://dp:443")
	if err != nil || !ep.grpc || ep.insecure {
		t.Fatalf("grpcs: %+v %v", ep, err)
	}
	ep, err = parseEndpoint("http://dp:21890/opentelemetry.proto.collector.trace.v1.TraceService/Export")
	if err != nil || ep.grpc || ep.url == "" {
		t.Fatalf("http: %+v %v", ep, err)
	}
	if _, err := parseEndpoint("localhost:21890"); err == nil {
		t.Fatal("expected an error for an endpoint without scheme")
	}
}

func TestDisabledSetupOnlyConfiguresStdout(t *testing.T) {
	out := captureStdout(t)
	s := testSettings()
	s.Enabled = false
	if err := Setup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Shutdown(time.Second) })

	if current().tracerProvider != nil || current().meterProvider != nil || st.loggerProvider != nil {
		t.Fatal("providers created while disabled")
	}
	if tp := Traceparent(context.Background()); tp != "" {
		t.Fatalf("traceparent while disabled: %q", tp)
	}
	slog.Info("hello")
	if line := out.find(t, "hello"); line["service"] != "test-svc" || line["level"] != "INFO" {
		t.Fatalf("line: %v", line)
	}
}

func TestJSONLogCarriesTraceContext(t *testing.T) {
	out := captureStdout(t)
	recorder := setup(t, testSettings())

	ctx, span := Tracer("test").Start(context.Background(), "work")
	Logger("app.orders").InfoContext(ctx, "order created", "order_id", 123, "amount", 49.9)
	Logger("app.orders").WarnContext(ctx, "slow", "error", errors.New("boom"))
	span.End()

	line := out.find(t, "order created")
	sc := recorder.Ended()[0].SpanContext()
	if line["trace_id"] != sc.TraceID().String() || line["span_id"] != sc.SpanID().String() {
		t.Fatalf("trace context: %v, span %s/%s", line, sc.TraceID(), sc.SpanID())
	}
	for key, want := range map[string]any{"level": "INFO", "logger": "app.orders", "service": "test-svc", "environment": "test", "order_id": 123.0, "amount": 49.9} {
		if line[key] != want {
			t.Errorf("%s = %v, want %v", key, line[key], want)
		}
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000+00:00", line["timestamp"].(string)); err != nil {
		t.Errorf("timestamp %v: %v", line["timestamp"], err)
	}
	if warn := out.find(t, "slow"); warn["level"] != "WARNING" || warn["error"] != "boom" {
		t.Errorf("warning line: %v", warn)
	}
}

func TestLoggerCreatedBeforeSetupFollowsSetup(t *testing.T) {
	early := Logger("early")
	out := captureStdout(t)
	setup(t, testSettings())
	early.Info("from early logger")
	if line := out.find(t, "from early logger"); line["logger"] != "early" || line["service"] != "test-svc" {
		t.Fatalf("line: %v", line)
	}
}

func TestOTLPLogsCarryTraceContextAndLevel(t *testing.T) {
	captureStdout(t)
	logs := &memoryLogProcessor{}
	s := testSettings()
	s.LogsEnabled = true
	s.LogsLevel = "WARNING"
	recorder := setup(t, s, WithLogProcessor(logs))

	ctx, span := Tracer("test").Start(context.Background(), "work")
	slog.InfoContext(ctx, "info is below OTEL_LOGS_LEVEL")
	slog.WarnContext(ctx, "exported", "order_id", 7)
	span.End()

	if _, ok := logs.find("info is below OTEL_LOGS_LEVEL"); ok {
		t.Fatal("record below OTEL_LOGS_LEVEL was exported")
	}
	record, ok := logs.find("exported")
	if !ok {
		t.Fatalf("record not exported; got %v", logs.bodies())
	}
	if record.TraceID() != recorder.Ended()[0].SpanContext().TraceID() {
		t.Fatalf("trace id %s, want %s", record.TraceID(), recorder.Ended()[0].SpanContext().TraceID())
	}
	if name, _ := record.Resource().Set().Value("service.name"); name.AsString() != "test-svc" {
		t.Fatalf("resource service.name = %q", name.AsString())
	}
}

func TestExportPipelineErrorsStayOnStdout(t *testing.T) {
	out := captureStdout(t)
	logs := &memoryLogProcessor{}
	s := testSettings()
	s.LogsEnabled = true
	setup(t, s, WithLogProcessor(logs))

	otel.Handle(errors.New("failed to export"))
	if line := out.find(t, "OpenTelemetry export pipeline error"); line["logger"] != "opentelemetry" {
		t.Fatalf("line: %v", line)
	}
	if _, ok := logs.find("OpenTelemetry export pipeline error"); ok {
		t.Fatal("export pipeline error was exported over OTLP")
	}
}

func TestLogMiddlewareAppliesToStdoutAndOTLP(t *testing.T) {
	out := captureStdout(t)
	logs := &memoryLogProcessor{}
	s := testSettings()
	s.LogsEnabled = true
	setup(t, s, WithLogProcessor(logs), WithLogMiddleware(func(next slog.Handler) slog.Handler {
		return redact{next}
	}))

	slog.Info("login", "password", "hunter2")
	if line := out.find(t, "login"); line["password"] != "***" {
		t.Fatalf("stdout not redacted: %v", line)
	}
	record, _ := logs.find("login")
	var password string
	record.WalkAttributes(func(kv logKeyValue) bool {
		if kv.Key == "password" {
			password = kv.Value.AsString()
		}
		return true
	})
	if password != "***" {
		t.Fatalf("OTLP not redacted: %q", password)
	}
}

type redact struct{ slog.Handler }

func (h redact) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "password" {
			a = slog.String("password", "***")
		}
		out.AddAttrs(a)
		return true
	})
	return h.Handler.Handle(ctx, out)
}

func (h redact) WithAttrs(attrs []slog.Attr) slog.Handler { return redact{h.Handler.WithAttrs(attrs)} }
func (h redact) WithGroup(name string) slog.Handler       { return redact{h.Handler.WithGroup(name)} }

func TestResourceAttributes(t *testing.T) {
	recorder := setup(t, testSettings())
	_, span := Tracer("test").Start(context.Background(), "work")
	span.End()
	attrs := recorder.Ended()[0].Resource().Set()
	for key, want := range map[string]string{"service.name": "test-svc", "service.namespace": "trivita", "deployment.environment": "test"} {
		if v, _ := attrs.Value(attribute.Key(key)); v.AsString() != want {
			t.Errorf("%s = %q, want %q", key, v.AsString(), want)
		}
	}
}

func TestRealEnvironmentWinsOverSettings(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=from-env")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	recorder := setup(t, testSettings())
	_, span := Tracer("test").Start(context.Background(), "work")
	span.End()
	if span.SpanContext().IsSampled() || len(recorder.Ended()) != 0 {
		t.Fatal("OTEL_TRACES_SAMPLER=always_off was ignored")
	}

	Shutdown(time.Second)
	os.Unsetenv("OTEL_TRACES_SAMPLER")
	recorder = setup(t, testSettings())
	_, span = Tracer("test").Start(context.Background(), "work")
	span.End()
	if v, _ := recorder.Ended()[0].Resource().Set().Value("deployment.environment"); v.AsString() != "from-env" {
		t.Fatalf("deployment.environment = %q", v.AsString())
	}
}

func TestSampleRatioAndParentDecision(t *testing.T) {
	s := testSettings()
	s.TracesSampleRatio = 0
	recorder := setup(t, s)

	_, span := Tracer("test").Start(context.Background(), "root")
	span.End()
	if len(recorder.Ended()) != 0 {
		t.Fatal("ratio 0 sampled a root span")
	}
	// A sampled upstream traceparent keeps its decision (parent-based sampler).
	ctx := ContinueTrace(context.Background(), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	_, span = Tracer("test").Start(ctx, "child")
	span.End()
	if len(recorder.Ended()) != 1 || recorder.Ended()[0].SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("upstream sampling decision was not kept")
	}
}

func TestSecondSetupReusesProviders(t *testing.T) {
	out := captureStdout(t)
	logs := &memoryLogProcessor{}
	s := testSettings()
	s.LogsEnabled = true
	setup(t, s, WithLogProcessor(logs))
	tp := st.tracerProvider

	s.Environment = "second"
	if err := Setup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if st.tracerProvider != tp {
		t.Fatal("second Setup created a new tracer provider")
	}
	slog.Info("once")
	if n := strings.Count(strings.Join(logs.bodies(), "\n"), "once"); n != 1 {
		t.Fatalf("record exported %d times", n)
	}
	if lines := out.lines(t); lines[len(lines)-1]["environment"] != "second" {
		t.Fatalf("stdout handler not replaced: %v", lines[len(lines)-1])
	}
}

func TestShutdownIsBoundedWhenBackendIsDown(t *testing.T) {
	captureStdout(t)
	s := testSettings()
	s.LogsEnabled = true
	s.MetricsEnabled = true
	if err := Setup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ctx, span := Tracer("test").Start(context.Background(), "work")
	slog.InfoContext(ctx, "pending")
	span.End()

	start := time.Now()
	Shutdown(time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown took %s", elapsed)
	}
	start = time.Now()
	if !Shutdown(time.Second) || time.Since(start) > 100*time.Millisecond {
		t.Fatal("second Shutdown was not an immediate no-op")
	}
}

func TestMetricsAndMeter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	s := testSettings()
	s.MetricsEnabled = true
	setup(t, s, WithMetricReader(reader))

	counter, _ := Meter("app").Int64Counter("orders.created")
	ctx, span := Tracer("test").Start(context.Background(), "work")
	counter.Add(ctx, 1, metricAttr("channel", "web"))
	counter.Add(ctx, 2, metricAttr("channel", "web"))
	span.End()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	m := findMetric(t, rm, "orders.created")
	points := m.Data.(metricdata.Sum[int64]).DataPoints
	if len(points) != 1 || points[0].Value != 3 {
		t.Fatalf("points: %+v", points)
	}
	if len(points[0].Exemplars) == 0 || trace.TraceID(points[0].Exemplars[0].TraceID) != span.SpanContext().TraceID() {
		t.Fatalf("exemplar does not point to the trace: %+v", points[0].Exemplars)
	}
	if v, _ := rm.Resource.Set().Value("deployment.environment"); v.AsString() != "test" {
		t.Fatalf("resource environment = %q", v.AsString())
	}
}

func TestMeterIsNoopWhenMetricsDisabled(t *testing.T) {
	setup(t, testSettings())
	if current().meterProvider != nil {
		t.Fatal("meter provider created with metrics disabled")
	}
	counter, err := Meter("app").Int64Counter("orders.created")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)
}

// TestMeterCreatedBeforeSetup runs in a clean subprocess: the global MeterProvider only
// delegates to the first provider set in a process.
func TestMeterCreatedBeforeSetup(t *testing.T) {
	if os.Getenv("TRIVITA_SUBPROCESS") == "1" {
		counter, _ := Meter("early").Int64Counter("early.counter")
		reader := sdkmetric.NewManualReader()
		s := testSettings()
		s.MetricsEnabled = true
		if err := Setup(context.Background(), s, WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(nil)), WithMetricReader(reader)); err != nil {
			t.Fatal(err)
		}
		counter.Add(context.Background(), 5)
		var rm metricdata.ResourceMetrics
		_ = reader.Collect(context.Background(), &rm)
		if findMetric(t, rm, "early.counter").Data.(metricdata.Sum[int64]).DataPoints[0].Value != 5 {
			t.Fatal("early instrument did not record")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMeterCreatedBeforeSetup$")
	cmd.Env = append(os.Environ(), "TRIVITA_SUBPROCESS=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subprocess: %v\n%s", err, out)
	}
}

func TestJobTraceContinuesRequestTrace(t *testing.T) {
	recorder := setup(t, testSettings())

	ctx, request := Tracer("test").Start(context.Background(), "GET /signup")
	var stored string
	err := Enqueue(ctx, "asynq", "default", "send_email", func(_ context.Context, traceparent string) (string, error) {
		stored = traceparent
		return "job-1", nil
	})
	request.End()
	if err != nil || stored == "" {
		t.Fatalf("enqueue: %v, traceparent %q", err, stored)
	}

	var jobCtx context.Context
	err = RunJob(context.Background(), Job{System: "asynq", Name: "send_email", ID: "job-1", Try: 1, Traceparent: stored},
		func(ctx context.Context) error {
			jobCtx = ctx
			_, child := Tracer("test").Start(ctx, "db query")
			child.End()
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range recorder.Ended() {
		spans[s.Name()] = s
	}
	enqueue, job, child := spans["enqueue send_email"], spans["job send_email"], spans["db query"]
	traceID := request.SpanContext().TraceID()
	for _, s := range []sdktrace.ReadOnlySpan{enqueue, job, child} {
		if s.SpanContext().TraceID() != traceID {
			t.Fatalf("%s is in another trace", s.Name())
		}
	}
	if enqueue.Parent().SpanID() != request.SpanContext().SpanID() || job.Parent().SpanID() != enqueue.SpanContext().SpanID() {
		t.Fatal("wrong parent chain request -> enqueue -> job")
	}
	if enqueue.SpanKind() != trace.SpanKindProducer || job.SpanKind() != trace.SpanKindConsumer {
		t.Fatal("wrong span kinds")
	}
	if trace.SpanFromContext(jobCtx).SpanContext().SpanID() != job.SpanContext().SpanID() {
		t.Fatal("job context does not carry the job span")
	}
	assertAttr(t, job, "messaging.message.id", "job-1")
	assertAttr(t, job, "asynq.job.try", "1")
	assertAttr(t, enqueue, "messaging.destination.name", "default")
}

func TestJobErrorsAndRetries(t *testing.T) {
	recorder := setup(t, testSettings())
	errRetry := errors.New("retry later")
	isRetry := func(err error) bool { return errors.Is(err, errRetry) }

	err := RunJob(context.Background(), Job{System: "asynq", Name: "flaky", Try: 1, IsRetry: isRetry},
		func(context.Context) error { return errRetry })
	if !errors.Is(err, errRetry) {
		t.Fatalf("error not returned: %v", err)
	}
	_ = RunJob(context.Background(), Job{System: "asynq", Name: "broken", IsRetry: isRetry},
		func(context.Context) error { return errors.New("db down") })

	flaky, broken := recorder.Ended()[0], recorder.Ended()[1]
	if flaky.Status().Code != codes.Unset || !slices.ContainsFunc(flaky.Events(), func(e sdktrace.Event) bool { return e.Name == "asynq.retry_requested" }) {
		t.Fatalf("retry: status %v events %v", flaky.Status(), flaky.Events())
	}
	if broken.Status().Code != codes.Error {
		t.Fatalf("error: status %v", broken.Status())
	}

	func() {
		defer func() { _ = recover() }()
		_ = RunJob(context.Background(), Job{System: "asynq", Name: "panics"}, func(context.Context) error { panic("bad") })
	}()
	if panicked := recorder.Ended()[2]; panicked.Status().Code != codes.Error {
		t.Fatalf("panic: status %v", panicked.Status())
	}
}

func TestEnqueueWithoutTracingSendsNoTraceparent(t *testing.T) {
	s := testSettings()
	s.Enabled = false
	if err := Setup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Shutdown(time.Second) })
	otel.SetTracerProvider(tracenoop.NewTracerProvider())

	var stored = "unset"
	_ = Enqueue(context.Background(), "asynq", "", "send_email", func(_ context.Context, traceparent string) (string, error) {
		stored = traceparent
		return "", nil
	})
	if stored != "" {
		t.Fatalf("traceparent = %q", stored)
	}
	ran := false
	_ = RunJob(context.Background(), Job{System: "asynq", Name: "x"}, func(context.Context) error { ran = true; return nil })
	if !ran {
		t.Fatal("job did not run")
	}
}
