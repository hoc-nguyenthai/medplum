// Package observability sends traces, logs and metrics of Trivita Go services to OpenSearch
// through Data Prepper over OTLP, with JSON logs on stdout that carry trace_id/span_id.
//
// Call Setup once at startup and Shutdown before the process exits:
//
//	if err := observability.Setup(ctx, observability.FromEnv()); err != nil {
//		slog.Error("observability setup failed", "error", err)
//	}
//	defer observability.Shutdown(0)
//
// For Echo v4, use the echo4obs package.
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Option customizes Setup.
type Option func(*options)

type options struct {
	logMiddlewares []LogMiddleware
	spanProcessor  sdktrace.SpanProcessor
	metricReader   sdkmetric.Reader
	logProcessor   sdklog.Processor
}

// WithLogMiddleware wraps the log handler; middlewares apply to both stdout and OTLP, in the
// order given (the first one sees records first).
func WithLogMiddleware(middlewares ...LogMiddleware) Option {
	return func(o *options) { o.logMiddlewares = append(o.logMiddlewares, middlewares...) }
}

// WithSpanProcessor replaces the OTLP span export (e.g. tracetest.NewSpanRecorder() in tests).
func WithSpanProcessor(processor sdktrace.SpanProcessor) Option {
	return func(o *options) { o.spanProcessor = processor }
}

// WithMetricReader replaces the periodic OTLP metric reader (e.g. sdkmetric.NewManualReader()).
func WithMetricReader(reader sdkmetric.Reader) Option {
	return func(o *options) { o.metricReader = reader }
}

// WithLogProcessor replaces the batched OTLP log export.
func WithLogProcessor(processor sdklog.Processor) Option {
	return func(o *options) { o.logProcessor = processor }
}

// state is owned by Setup/Shutdown (under mu); runtime is the read-only snapshot used on the
// request path.
var (
	mu sync.Mutex
	st struct {
		settings       Settings
		middlewares    []LogMiddleware
		resource       *resource.Resource
		tracerProvider *sdktrace.TracerProvider
		meterProvider  *sdkmetric.MeterProvider
		loggerProvider *sdklog.LoggerProvider
		otlpHandler    slog.Handler
	}
	rt atomic.Pointer[runtimeState]
)

type runtimeState struct {
	settings       Settings
	excluded       []*regexp.Regexp
	tracerProvider trace.TracerProvider // nil: the global one
	meterProvider  metric.MeterProvider // nil: metrics off
}

var defaultRuntime = func() *runtimeState {
	s := DefaultSettings()
	excluded, _ := compileExcluded(s.ExcludedURLs)
	return &runtimeState{settings: s, excluded: excluded}
}()

func current() *runtimeState {
	if r := rt.Load(); r != nil {
		return r
	}
	return defaultRuntime
}

// ActiveSettings returns the settings of the last Setup (DefaultSettings before that).
func ActiveSettings() Settings { return current().settings }

// Setup configures logging and, when s.Enabled, tracing, OTLP logs (s.LogsEnabled) and
// metrics (s.MetricsEnabled).
//
// stdout logging is always configured and becomes slog's default logger (the standard log
// package is routed there too). Each provider is created once per process: calling Setup
// again reuses them and only replaces the stdout handler. On error (invalid endpoint or
// excluded URL pattern) stdout logging is still configured; the service can keep running.
func Setup(ctx context.Context, s Settings, opts ...Option) error {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	s = s.withDefaults()

	mu.Lock()
	defer mu.Unlock()
	st.settings, st.middlewares = s, o.logMiddlewares
	excluded, err := compileExcluded(s.ExcludedURLs)
	if err == nil && s.Enabled {
		err = initProviders(ctx, s, o)
	}
	installHandlers(s, st.otlpHandler, st.middlewares)

	pipeline := pipelineLogger(s, st.middlewares)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		pipeline.Warn("OpenTelemetry export pipeline error", "error", err)
	}))
	publishRuntime(excluded)
	if err != nil {
		return err
	}
	if s.Enabled {
		slog.Info("OpenTelemetry enabled", "logs", st.loggerProvider != nil, "metrics", st.meterProvider != nil)
	}
	return nil
}

func initProviders(ctx context.Context, s Settings, o options) error {
	if st.resource == nil {
		res, err := newResource(ctx, s)
		if err != nil {
			return fmt.Errorf("observability resource: %w", err)
		}
		st.resource = res
	}

	if st.tracerProvider == nil {
		processor := o.spanProcessor
		if processor == nil {
			exporter, err := newSpanExporter(ctx, s.TracesEndpoint, s.Headers)
			if err != nil {
				return err
			}
			processor = newBatchSpanProcessor(exporter, s)
		}
		st.tracerProvider = newTracerProvider(st.resource, processor, s)
		otel.SetTracerProvider(st.tracerProvider)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{}))
	}

	if s.MetricsEnabled && st.meterProvider == nil {
		reader := o.metricReader
		if reader == nil {
			exporter, err := newMetricExporter(ctx, s.MetricsEndpoint, s.Headers)
			if err != nil {
				return err
			}
			reader = sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(s.MetricsExportInterval))
		}
		st.meterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithResource(st.resource), sdkmetric.WithReader(reader))
		// Global so instruments created from otel.Meter() before Setup start recording; the
		// global API only delegates to the first provider of the process, so Meter() below
		// prefers this provider.
		otel.SetMeterProvider(st.meterProvider)
	}

	if s.LogsEnabled && st.loggerProvider == nil {
		providerOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(st.resource)}
		if o.logProcessor != nil {
			st.loggerProvider = sdklog.NewLoggerProvider(append(providerOpts, sdklog.WithProcessor(o.logProcessor))...)
		} else {
			exporter, err := newLogExporter(ctx, s.LogsEndpoint, s.Headers)
			if err != nil {
				return err
			}
			st.loggerProvider = newLoggerProvider(exporter, s, providerOpts...)
		}
		st.otlpHandler = newOTLPHandler(st.loggerProvider, maxLevel(ParseLevel(s.LogLevel), ParseLevel(s.LogsLevel)))
	}
	return nil
}

func publishRuntime(excluded []*regexp.Regexp) {
	r := &runtimeState{settings: st.settings, excluded: excluded}
	if st.tracerProvider != nil {
		r.tracerProvider = st.tracerProvider
	}
	if st.meterProvider != nil {
		r.meterProvider = st.meterProvider
	}
	rt.Store(r)
}

// Shutdown flushes pending log records, metrics and spans, in parallel, and returns within
// timeout (Settings.ShutdownTimeout when timeout <= 0) even when the backend is unreachable.
// Whatever is not exported by then is dropped and Shutdown returns false. It is safe to call
// more than once; the next Setup creates new providers.
func Shutdown(timeout time.Duration) bool {
	mu.Lock()
	if timeout <= 0 {
		timeout = st.settings.withDefaults().ShutdownTimeout
	}
	var shutdowns []func(context.Context) error
	if st.loggerProvider != nil {
		shutdowns = append(shutdowns, st.loggerProvider.Shutdown)
	}
	if st.meterProvider != nil {
		shutdowns = append(shutdowns, st.meterProvider.Shutdown)
	}
	if st.tracerProvider != nil {
		shutdowns = append(shutdowns, st.tracerProvider.Shutdown)
	}
	// Clear the state first: an export still running past the deadline must not affect the
	// next Setup.
	st.loggerProvider, st.meterProvider, st.tracerProvider, st.otlpHandler, st.resource = nil, nil, nil, nil, nil
	if currentHandler.Load() != nil {
		installHandlers(st.settings, nil, st.middlewares)
	}
	if r := rt.Load(); r != nil {
		publishRuntime(r.excluded)
	}
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, shutdown := range shutdowns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = shutdown(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		slog.Warn(fmt.Sprintf("Observability shutdown exceeded %d ms; pending spans/log records dropped", timeout.Milliseconds()))
		return false
	}
}

// Tracer returns a tracer of the library's provider (the global one before Setup).
func Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	if tp := current().tracerProvider; tp != nil {
		return tp.Tracer(name, opts...)
	}
	return otel.Tracer(name, opts...)
}

// Meter returns a meter for app-defined metrics. Instruments created before Setup (at
// package level) start recording once Setup enables metrics; when metrics stay disabled
// they are no-ops.
func Meter(name string, opts ...metric.MeterOption) metric.Meter {
	if mp := current().meterProvider; mp != nil {
		return mp.Meter(name, opts...)
	}
	return otel.Meter(name, opts...)
}

func meterOrNoop(name string) metric.Meter {
	if mp := current().meterProvider; mp != nil {
		return mp.Meter(name)
	}
	return noop.NewMeterProvider().Meter(name)
}

func compileExcluded(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid excluded URL pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}
