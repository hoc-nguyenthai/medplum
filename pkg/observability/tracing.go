package observability

import (
	"context"
	"os"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newResource builds the Resource shared by traces, logs and metrics, so all three carry the
// same service.name/namespace/environment. Precedence, lowest first: namespace and
// environment from settings, then OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME, then
// service.name and service.version from settings (which already read the env).
func newResource(ctx context.Context, s Settings) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			attribute.String("service.namespace", s.ServiceNamespace),
			attribute.String("deployment.environment", s.Environment),
		),
		resource.WithFromEnv(),
		resource.WithAttributes(
			attribute.String("service.name", s.ServiceName),
			attribute.String("service.version", s.ServiceVersion),
		),
	)
}

// newBatchSpanProcessor batches spans for exporter. Standard OTel variables already in the
// real environment (OTEL_BSP_*) win over settings: the SDK reads them as its own defaults,
// so an option is only passed when the variable is absent.
func newBatchSpanProcessor(exporter sdktrace.SpanExporter, s Settings) sdktrace.SpanProcessor {
	var opts []sdktrace.BatchSpanProcessorOption
	if _, ok := os.LookupEnv("OTEL_BSP_SCHEDULE_DELAY"); !ok {
		opts = append(opts, sdktrace.WithBatchTimeout(s.BSPScheduleDelay))
	}
	if _, ok := os.LookupEnv("OTEL_BSP_MAX_EXPORT_BATCH_SIZE"); !ok {
		opts = append(opts, sdktrace.WithMaxExportBatchSize(s.BSPMaxExportBatch))
	}
	return sdktrace.NewBatchSpanProcessor(exporter, opts...)
}

// newTracerProvider creates the TracerProvider. OTEL_TRACES_SAMPLER / OTEL_TRACES_SAMPLER_ARG
// in the real environment win over TracesSampleRatio, as with the batch settings.
func newTracerProvider(res *resource.Resource, processor sdktrace.SpanProcessor, s Settings) *sdktrace.TracerProvider {
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(processor),
	}
	if _, ok := os.LookupEnv("OTEL_TRACES_SAMPLER"); !ok {
		ratio := s.TracesSampleRatio
		if v, err := strconv.ParseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 64); err == nil {
			ratio = v
		}
		// Parent-based: a request arriving with a traceparent keeps the upstream decision.
		opts = append(opts, sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))))
	}
	return sdktrace.NewTracerProvider(opts...)
}
