package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "trivita-fhir/pkg/observability"

// LogMiddleware wraps the library's log handler. It sees every record before it reaches both
// stdout and OTLP, so both outputs stay equal (PII redaction, request context, ...).
type LogMiddleware func(next slog.Handler) slog.Handler

// stdout resolves os.Stdout on every write instead of keeping the *os.File it saw at setup,
// so swapping os.Stdout (tests, process managers) never leaves a handler on a closed file.
var stdout io.Writer = stdoutWriter{}

type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

// Logger returns a logger that adds a "logger" field (like Python's logging.getLogger). It is
// safe to create at package level, before Setup: records always go to the handler that is
// current when they are written.
func Logger(name string) *slog.Logger {
	return slog.New(&dynamicHandler{}).With("logger", name)
}

// ParseLevel maps DEBUG, INFO, WARN/WARNING, ERROR and CRITICAL to a slog level.
func ParseLevel(name string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	case "CRITICAL", "FATAL":
		return slog.LevelError + 4
	}
	return slog.LevelInfo
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError+4:
		return "CRITICAL"
	case l == slog.LevelWarn:
		return "WARNING"
	}
	return l.String()
}

// newStdoutHandler writes one JSON object (or text line) per record, with trace_id/span_id
// of the span in the record's context.
func newStdoutHandler(s Settings) slog.Handler {
	opts := &slog.HandlerOptions{Level: ParseLevel(s.LogLevel)}
	if !s.LogJSON {
		return traceContextHandler{slog.NewTextHandler(stdout, opts)}
	}
	opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) > 0 {
			return a
		}
		switch a.Key {
		case slog.TimeKey:
			return slog.String("timestamp", a.Value.Time().UTC().Format("2006-01-02T15:04:05.000+00:00"))
		case slog.LevelKey:
			level, _ := a.Value.Any().(slog.Level)
			return slog.String("level", levelName(level))
		case slog.MessageKey:
			return slog.Attr{Key: "message", Value: a.Value}
		}
		return a
	}
	h := slog.NewJSONHandler(stdout, opts).WithAttrs([]slog.Attr{
		slog.String("service", s.ServiceName),
		slog.String("environment", s.Environment),
	})
	return traceContextHandler{h}
}

// traceContextHandler adds trace_id/span_id when the record's context carries a valid span.
// Log with the request context (logger.InfoContext(ctx, ...)) to get them.
type traceContextHandler struct{ slog.Handler }

func (h traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceContextHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceContextHandler) WithGroup(name string) slog.Handler {
	return traceContextHandler{h.Handler.WithGroup(name)}
}

// newOTLPHandler bridges slog records to the OTel LoggerProvider; the bridge attaches the
// trace context of the record's context itself.
func newOTLPHandler(provider *sdklog.LoggerProvider, minLevel slog.Level) slog.Handler {
	return minLevelHandler{
		Handler: otelslog.NewHandler(instrumentationName, otelslog.WithLoggerProvider(provider)),
		min:     minLevel,
	}
}

type minLevelHandler struct {
	slog.Handler
	min slog.Level
}

func (h minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minLevelHandler{h.Handler.WithAttrs(attrs), h.min}
}

func (h minLevelHandler) WithGroup(name string) slog.Handler {
	return minLevelHandler{h.Handler.WithGroup(name), h.min}
}

// fanoutHandler sends each record to every child that accepts its level.
type fanoutHandler []slog.Handler

func (f fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanoutHandler, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	out := make(fanoutHandler, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

// applyMiddlewares wraps h so that middlewares[0] sees records first.
func applyMiddlewares(h slog.Handler, middlewares []LogMiddleware) slog.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// currentHandler is the handler chain built by the last Setup/Shutdown; nil before Setup.
var currentHandler atomic.Pointer[handlerGeneration]

type handlerGeneration struct{ h slog.Handler }

// dynamicHandler resolves the current handler chain on every record (cached per generation),
// so loggers created before Setup, or kept across Shutdown, follow the active configuration.
type dynamicHandler struct {
	ops   []func(slog.Handler) slog.Handler
	cache atomic.Pointer[resolvedHandler]
}

type resolvedHandler struct {
	gen *handlerGeneration
	h   slog.Handler
}

func (d *dynamicHandler) resolve() slog.Handler {
	gen := currentHandler.Load()
	if gen == nil {
		// Before Setup: whatever slog's default is (never this handler, see Setup).
		h := slog.Default().Handler()
		for _, op := range d.ops {
			h = op(h)
		}
		return h
	}
	if c := d.cache.Load(); c != nil && c.gen == gen {
		return c.h
	}
	h := gen.h
	for _, op := range d.ops {
		h = op(h)
	}
	d.cache.Store(&resolvedHandler{gen: gen, h: h})
	return h
}

func (d *dynamicHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return d.resolve().Enabled(ctx, l)
}

func (d *dynamicHandler) Handle(ctx context.Context, r slog.Record) error {
	return d.resolve().Handle(ctx, r)
}

func (d *dynamicHandler) with(op func(slog.Handler) slog.Handler) *dynamicHandler {
	ops := make([]func(slog.Handler) slog.Handler, len(d.ops), len(d.ops)+1)
	copy(ops, d.ops)
	return &dynamicHandler{ops: append(ops, op)}
}

func (d *dynamicHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return d.with(func(h slog.Handler) slog.Handler { return h.WithAttrs(attrs) })
}

func (d *dynamicHandler) WithGroup(name string) slog.Handler {
	return d.with(func(h slog.Handler) slog.Handler { return h.WithGroup(name) })
}

// installHandlers swaps in a new chain: stdout always, OTLP when otlp is non-nil.
func installHandlers(s Settings, otlp slog.Handler, middlewares []LogMiddleware) {
	outputs := fanoutHandler{newStdoutHandler(s)}
	if otlp != nil {
		outputs = append(outputs, otlp)
	}
	currentHandler.Store(&handlerGeneration{h: applyMiddlewares(outputs, middlewares)})
	slog.SetDefault(slog.New(&dynamicHandler{}))
}

// pipelineLogger logs errors of the export pipeline itself (OTel SDK, exporters) to stdout
// only: exporting "failed to export" records would loop while the backend is unreachable.
func pipelineLogger(s Settings, middlewares []LogMiddleware) *slog.Logger {
	return slog.New(applyMiddlewares(newStdoutHandler(s), middlewares)).With("logger", "opentelemetry")
}

func newLoggerProvider(exporter sdklog.Exporter, s Settings, opts ...sdklog.LoggerProviderOption) *sdklog.LoggerProvider {
	processor := sdklog.NewBatchProcessor(exporter,
		sdklog.WithExportInterval(s.BSPScheduleDelay),
		sdklog.WithExportMaxBatchSize(s.BSPMaxExportBatch),
	)
	return sdklog.NewLoggerProvider(append(opts, sdklog.WithProcessor(processor))...)
}

// maxLevel is used for the OTLP output: a record must pass both LOG_LEVEL and OTEL_LOGS_LEVEL.
func maxLevel(a, b slog.Level) slog.Level {
	if a > b {
		return a
	}
	return b
}
