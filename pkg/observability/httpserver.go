package observability

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Bucket boundaries recommended by the HTTP semantic conventions for request durations.
var (
	durationBucketsSeconds = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	durationBucketsMillis  = []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
)

var accessLogger = Logger("http.access")

// ServerRequest is one instrumented HTTP server request: a SERVER span, HTTP server metrics
// and an access log line. The Echo packages use it; it works with any net/http router.
type ServerRequest struct {
	start    time.Time
	req      *http.Request
	ctx      context.Context
	span     trace.Span
	route    string
	clientIP string
	mode     SemconvMode
	inst     *httpInstruments
	active   []attribute.KeyValue
}

// StartServerRequest starts instrumenting r. route is the route template ("/orders/:id"),
// empty when no route matched; clientIP may be empty (RemoteAddr is used). It returns nil
// when the request URI matches Settings.ExcludedURLs: such requests are not instrumented.
func StartServerRequest(r *http.Request, route, clientIP string) *ServerRequest {
	rs := current()
	for _, re := range rs.excluded {
		if re.MatchString(r.RequestURI) || re.MatchString(r.URL.Path) {
			return nil
		}
	}
	if clientIP == "" {
		clientIP = remoteHost(r.RemoteAddr)
	}
	sr := &ServerRequest{
		start:    time.Now(),
		req:      r,
		route:    route,
		clientIP: clientIP,
		mode:     rs.settings.Semconv,
		inst:     instruments(rs),
	}

	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	tracer := Tracer(instrumentationName)
	sr.ctx, sr.span = tracer.Start(ctx, sr.spanName(),
		trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(sr.spanAttributes()...))

	sr.active = sr.activeAttributes()
	sr.inst.active.Add(sr.ctx, 1, metric.WithAttributes(sr.active...))
	return sr
}

// Context returns the request context carrying the server span; pass it down (and log with
// it) so child spans and log lines belong to the request's trace.
func (sr *ServerRequest) Context() context.Context { return sr.ctx }

// SetRoute changes the route template once it is known (or clears it for unmatched requests);
// it renames the span accordingly.
func (sr *ServerRequest) SetRoute(route string) {
	sr.route = route
	sr.span.SetName(sr.spanName())
	if route != "" {
		sr.span.SetAttributes(attribute.String("http.route", route))
	}
}

// End records the response: status code, response body size and the handler error, if any.
func (sr *ServerRequest) End(status int, responseSize int64, err error) {
	elapsed := time.Since(sr.start)
	stable, old := sr.mode != SemconvOld, sr.mode != SemconvStable

	var attrs []attribute.KeyValue
	if stable {
		attrs = append(attrs, attribute.Int("http.response.status_code", status))
	}
	if old {
		attrs = append(attrs, attribute.Int("http.status_code", status))
	}
	sr.span.SetAttributes(attrs...)
	if err != nil {
		sr.span.RecordError(err)
	}
	if status >= 500 {
		desc := http.StatusText(status)
		if err != nil {
			desc = err.Error()
		}
		sr.span.SetStatus(codes.Error, desc)
	}

	sr.inst.active.Add(sr.ctx, -1, metric.WithAttributes(sr.active...))
	if stable {
		m := metric.WithAttributes(sr.stableMetricAttributes(status, err)...)
		sr.inst.duration.Record(sr.ctx, elapsed.Seconds(), m)
		sr.inst.bodySize.Record(sr.ctx, responseSize, m)
	}
	if old {
		m := metric.WithAttributes(sr.oldMetricAttributes(status)...)
		sr.inst.durationOld.Record(sr.ctx, float64(elapsed)/float64(time.Millisecond), m)
		sr.inst.bodySizeOld.Record(sr.ctx, responseSize, m)
	}

	if current().settings.AccessLog {
		args := []any{
			"method", sr.req.Method, "path", sr.req.URL.Path, "route", sr.route, "status", status,
			"duration_ms", float64(elapsed.Microseconds()) / 1000, "client_ip", sr.clientIP,
		}
		if err != nil {
			args = append(args, "error", err)
		}
		accessLogger.InfoContext(sr.ctx, fmt.Sprintf("%s %s %d", sr.req.Method, sr.req.RequestURI, status), args...)
	}
	sr.span.End()
}

func (sr *ServerRequest) spanName() string {
	method := normalizeMethod(sr.req.Method)
	if method == "_OTHER" {
		method = "HTTP"
	}
	if sr.route == "" {
		return method
	}
	return method + " " + sr.route
}

func (sr *ServerRequest) spanAttributes() []attribute.KeyValue {
	r := sr.req
	var attrs []attribute.KeyValue
	if sr.route != "" {
		attrs = append(attrs, attribute.String("http.route", sr.route))
	}
	if sr.mode != SemconvOld {
		attrs = append(attrs,
			attribute.String("http.request.method", normalizeMethod(r.Method)),
			attribute.String("url.path", r.URL.Path),
			attribute.String("url.scheme", scheme(r)),
			attribute.String("server.address", remoteHost(r.Host)),
			attribute.String("network.protocol.version", protocolVersion(r)),
		)
		if r.URL.RawQuery != "" {
			attrs = append(attrs, attribute.String("url.query", r.URL.RawQuery))
		}
		if sr.clientIP != "" {
			attrs = append(attrs, attribute.String("client.address", sr.clientIP))
		}
		if ua := r.UserAgent(); ua != "" {
			attrs = append(attrs, attribute.String("user_agent.original", ua))
		}
	}
	if sr.mode != SemconvStable {
		attrs = append(attrs,
			attribute.String("http.method", r.Method),
			attribute.String("http.target", r.RequestURI),
			attribute.String("http.scheme", scheme(r)),
			attribute.String("http.host", r.Host),
			attribute.String("http.flavor", protocolVersion(r)),
		)
		if sr.clientIP != "" {
			attrs = append(attrs, attribute.String("http.client_ip", sr.clientIP))
		}
		if ua := r.UserAgent(); ua != "" {
			attrs = append(attrs, attribute.String("http.user_agent", ua))
		}
	}
	return attrs
}

func (sr *ServerRequest) activeAttributes() []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if sr.mode != SemconvOld {
		attrs = append(attrs,
			attribute.String("http.request.method", normalizeMethod(sr.req.Method)),
			attribute.String("url.scheme", scheme(sr.req)))
	}
	if sr.mode != SemconvStable {
		attrs = append(attrs,
			attribute.String("http.method", sr.req.Method),
			attribute.String("http.scheme", scheme(sr.req)))
	}
	return attrs
}

func (sr *ServerRequest) stableMetricAttributes(status int, err error) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", normalizeMethod(sr.req.Method)),
		attribute.String("url.scheme", scheme(sr.req)),
		attribute.Int("http.response.status_code", status),
		attribute.String("network.protocol.version", protocolVersion(sr.req)),
	}
	if sr.route != "" {
		attrs = append(attrs, attribute.String("http.route", sr.route))
	}
	if status >= 500 {
		errorType := strconv.Itoa(status)
		if err != nil {
			errorType = fmt.Sprintf("%T", err)
		}
		attrs = append(attrs, attribute.String("error.type", errorType))
	}
	return attrs
}

func (sr *ServerRequest) oldMetricAttributes(status int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("http.method", sr.req.Method),
		attribute.String("http.scheme", scheme(sr.req)),
		attribute.String("http.flavor", protocolVersion(sr.req)),
		attribute.Int("http.status_code", status),
	}
	if sr.route != "" {
		attrs = append(attrs, attribute.String("http.target", sr.route))
	}
	return attrs
}

type httpInstruments struct {
	provider    metric.MeterProvider
	duration    metric.Float64Histogram
	durationOld metric.Float64Histogram
	active      metric.Int64UpDownCounter
	bodySize    metric.Int64Histogram
	bodySizeOld metric.Int64Histogram
}

var instrumentsCache atomic.Pointer[httpInstruments]

// instruments returns the HTTP server instruments of the current meter provider, creating
// them again after a Setup/Shutdown changed the provider.
func instruments(rs *runtimeState) *httpInstruments {
	if c := instrumentsCache.Load(); c != nil && c.provider == rs.meterProvider {
		return c
	}
	m := meterOrNoop(instrumentationName)
	inst := &httpInstruments{provider: rs.meterProvider}
	inst.duration, _ = m.Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(durationBucketsSeconds...))
	inst.durationOld, _ = m.Float64Histogram("http.server.duration", metric.WithUnit("ms"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(durationBucketsMillis...))
	inst.active, _ = m.Int64UpDownCounter("http.server.active_requests", metric.WithUnit("{request}"),
		metric.WithDescription("Number of active HTTP server requests."))
	inst.bodySize, _ = m.Int64Histogram("http.server.response.body.size", metric.WithUnit("By"),
		metric.WithDescription("Size of HTTP server response bodies."))
	inst.bodySizeOld, _ = m.Int64Histogram("http.server.response.size", metric.WithUnit("By"),
		metric.WithDescription("Size of HTTP server response bodies."))
	instrumentsCache.Store(inst)
	return inst
}

func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	}
	return "_OTHER"
}

func scheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
		return proto
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func protocolVersion(r *http.Request) string {
	if r.ProtoMajor >= 2 {
		return strconv.Itoa(r.ProtoMajor)
	}
	return fmt.Sprintf("%d.%d", r.ProtoMajor, r.ProtoMinor)
}

func remoteHost(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return host
	}
	return hostPort
}
