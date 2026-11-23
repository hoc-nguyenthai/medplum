package observability

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Data Prepper defaults: otlp_traces listens on 21890, otlp_metrics on 21891, otlp_logs on
// 21892. gRPC, because a Data Prepper source with default settings accepts nothing else:
// OTLP/HTTP needs `unframed_requests: true` on the source (see docs/data-prepper.md).
const (
	DefaultTracesEndpoint  = "grpc://localhost:21890"
	DefaultMetricsEndpoint = "grpc://localhost:21891"
	DefaultLogsEndpoint    = "grpc://localhost:21892"
)

// SemconvMode selects the OpenTelemetry HTTP semantic conventions for server spans and metrics.
type SemconvMode string

const (
	// SemconvStable emits the stable names: http.route, http.request.method,
	// http.response.status_code, http.server.request.duration in seconds.
	SemconvStable SemconvMode = "http"
	// SemconvDup emits old and stable names side by side, while queries and dashboards migrate.
	SemconvDup SemconvMode = "http/dup"
	// SemconvOld emits the old names only: http.method, http.target, http.status_code,
	// http.server.duration in milliseconds.
	SemconvOld SemconvMode = "old"
)

// Settings is everything the library needs. Build it with FromEnv (or DefaultSettings) and
// change fields in code afterwards: values set in code win over the environment.
type Settings struct {
	ServiceName      string
	ServiceVersion   string
	ServiceNamespace string
	Environment      string

	// Enabled is the master switch: tracing, OTLP logs and metrics are no-ops unless true.
	Enabled           bool
	TracesEndpoint    string
	Headers           map[string]string
	TracesSampleRatio float64
	BSPScheduleDelay  time.Duration
	BSPMaxExportBatch int
	Semconv           SemconvMode
	// ExcludedURLs are regular expressions matched anywhere in the request URI; matching
	// requests get no span, no metrics and no access log (probes, Prometheus scrapes).
	ExcludedURLs []string
	// ShutdownTimeout bounds Shutdown(); what is not exported by then is dropped, so a dead
	// backend cannot stall a rolling deploy.
	ShutdownTimeout time.Duration

	LogsEnabled bool
	// No fallback to TracesEndpoint: Data Prepper receives logs on a different port, so
	// sharing the traces endpoint silently sends logs to the trace source.
	LogsEndpoint string
	LogsLevel    string

	// MetricsEnabled is off by default because services may already be scraped by Prometheus.
	MetricsEnabled        bool
	MetricsEndpoint       string
	MetricsExportInterval time.Duration

	// stdout logging, always configured (even when OTel is disabled).
	LogLevel string
	LogJSON  bool
	// AccessLog writes one "http.access" log line per instrumented HTTP request.
	AccessLog bool
}

// DefaultSettings returns the defaults used when no environment variable is set.
func DefaultSettings() Settings {
	return Settings{
		ServiceName:           "unknown-service",
		ServiceVersion:        "0.0.0",
		ServiceNamespace:      "trivita",
		Environment:           "local",
		TracesEndpoint:        DefaultTracesEndpoint,
		Headers:               map[string]string{},
		TracesSampleRatio:     1.0,
		BSPScheduleDelay:      5 * time.Second,
		BSPMaxExportBatch:     512,
		Semconv:               SemconvStable,
		ExcludedURLs:          []string{"health", "metrics"},
		ShutdownTimeout:       5 * time.Second,
		LogsEndpoint:          DefaultLogsEndpoint,
		LogsLevel:             "INFO",
		MetricsEndpoint:       DefaultMetricsEndpoint,
		MetricsExportInterval: 60 * time.Second,
		LogLevel:              "INFO",
		LogJSON:               true,
		AccessLog:             true,
	}
}

// FromEnv reads Settings from environment variables, falling back to DefaultSettings.
func FromEnv() Settings {
	d := DefaultSettings()
	return Settings{
		ServiceName:           envStr("OTEL_SERVICE_NAME", d.ServiceName),
		ServiceVersion:        envStr("OTEL_SERVICE_VERSION", d.ServiceVersion),
		ServiceNamespace:      envStr("OTEL_SERVICE_NAMESPACE", d.ServiceNamespace),
		Environment:           envStr("ENVIRONMENT", d.Environment),
		Enabled:               envBool("OTEL_ENABLED", d.Enabled),
		TracesEndpoint:        envStr("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", d.TracesEndpoint),
		Headers:               parseHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
		TracesSampleRatio:     envFloat("OTEL_TRACES_SAMPLE_RATIO", d.TracesSampleRatio),
		BSPScheduleDelay:      envMillis("OTEL_BSP_SCHEDULE_DELAY", d.BSPScheduleDelay),
		BSPMaxExportBatch:     envInt("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", d.BSPMaxExportBatch),
		Semconv:               semconvFromEnv(d.Semconv),
		ExcludedURLs:          envList("OTEL_EXCLUDED_URLS", d.ExcludedURLs),
		ShutdownTimeout:       envMillis("OTEL_SHUTDOWN_TIMEOUT_MS", d.ShutdownTimeout),
		LogsEnabled:           envBool("OTEL_LOGS_ENABLED", d.LogsEnabled),
		LogsEndpoint:          envStr("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", d.LogsEndpoint),
		LogsLevel:             envStr("OTEL_LOGS_LEVEL", d.LogsLevel),
		MetricsEnabled:        envBool("OTEL_METRICS_ENABLED", d.MetricsEnabled),
		MetricsEndpoint:       envStr("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", d.MetricsEndpoint),
		MetricsExportInterval: envMillis("OTEL_METRIC_EXPORT_INTERVAL", d.MetricsExportInterval),
		LogLevel:              envStr("LOG_LEVEL", d.LogLevel),
		LogJSON:               envBool("LOG_JSON", d.LogJSON),
		AccessLog:             envBool("LOG_ACCESS", d.AccessLog),
	}
}

// withDefaults fills zero values, so a Settings literal built in code works too.
func (s Settings) withDefaults() Settings {
	d := DefaultSettings()
	if s.ServiceName == "" {
		s.ServiceName = d.ServiceName
	}
	if s.ServiceVersion == "" {
		s.ServiceVersion = d.ServiceVersion
	}
	if s.ServiceNamespace == "" {
		s.ServiceNamespace = d.ServiceNamespace
	}
	if s.Environment == "" {
		s.Environment = d.Environment
	}
	if s.TracesEndpoint == "" {
		s.TracesEndpoint = d.TracesEndpoint
	}
	if s.BSPScheduleDelay <= 0 {
		s.BSPScheduleDelay = d.BSPScheduleDelay
	}
	if s.BSPMaxExportBatch <= 0 {
		s.BSPMaxExportBatch = d.BSPMaxExportBatch
	}
	if s.Semconv == "" {
		s.Semconv = d.Semconv
	}
	if s.ShutdownTimeout <= 0 {
		s.ShutdownTimeout = d.ShutdownTimeout
	}
	if s.LogsEndpoint == "" {
		s.LogsEndpoint = d.LogsEndpoint
	}
	if s.LogsLevel == "" {
		s.LogsLevel = d.LogsLevel
	}
	if s.MetricsEndpoint == "" {
		s.MetricsEndpoint = d.MetricsEndpoint
	}
	if s.MetricsExportInterval <= 0 {
		s.MetricsExportInterval = d.MetricsExportInterval
	}
	if s.LogLevel == "" {
		s.LogLevel = d.LogLevel
	}
	return s
}

// semconvFromEnv reads OTEL_SEMCONV_STABILITY_OPT_IN. An explicitly empty value means the
// old conventions, as with the OTel instrumentations; unset means the stable ones.
func semconvFromEnv(def SemconvMode) SemconvMode {
	raw, ok := os.LookupEnv("OTEL_SEMCONV_STABILITY_OPT_IN")
	if !ok {
		return def
	}
	mode := SemconvOld
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case string(SemconvDup):
			return SemconvDup
		case string(SemconvStable):
			mode = SemconvStable
		}
	}
	return mode
}

func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil {
		return v
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64); err == nil {
		return v
	}
	return def
}

func envMillis(name string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return def
}

func envList(name string, def []string) []string {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseHeaders parses OTEL_EXPORTER_OTLP_HEADERS (k1=v1,k2=v2).
func parseHeaders(raw string) map[string]string {
	headers := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if key = strings.TrimSpace(key); ok && key != "" {
			headers[key] = strings.TrimSpace(value)
		}
	}
	return headers
}
