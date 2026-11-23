package observability

import (
	"context"
	"fmt"
	"net/url"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// endpoint is an OTLP endpoint split by the scheme rules shared by traces, logs and
// metrics: grpc:// -> OTLP/gRPC without TLS, grpcs:// -> OTLP/gRPC with TLS,
// http(s)://host:port/path -> OTLP/HTTP protobuf at that exact URL.
type endpoint struct {
	grpc     bool
	insecure bool
	hostPort string // gRPC only
	url      string // OTLP/HTTP only
}

func parseEndpoint(raw string) (endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return endpoint{}, fmt.Errorf("invalid OTLP endpoint %q: %w", raw, err)
	}
	if u.Host == "" {
		return endpoint{}, fmt.Errorf("invalid OTLP endpoint %q: missing host", raw)
	}
	switch u.Scheme {
	case "grpc", "grpcs":
		return endpoint{grpc: true, insecure: u.Scheme == "grpc", hostPort: u.Host}, nil
	case "http", "https":
		return endpoint{url: raw}, nil
	}
	return endpoint{}, fmt.Errorf("invalid OTLP endpoint %q: scheme must be grpc, grpcs, http or https", raw)
}

func newSpanExporter(ctx context.Context, raw string, headers map[string]string) (sdktrace.SpanExporter, error) {
	ep, err := parseEndpoint(raw)
	if err != nil {
		return nil, err
	}
	if ep.grpc {
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(ep.hostPort), otlptracegrpc.WithHeaders(headers)}
		if ep.insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, opts...)
	}
	return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(ep.url), otlptracehttp.WithHeaders(headers))
}

func newMetricExporter(ctx context.Context, raw string, headers map[string]string) (sdkmetric.Exporter, error) {
	ep, err := parseEndpoint(raw)
	if err != nil {
		return nil, err
	}
	if ep.grpc {
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(ep.hostPort), otlpmetricgrpc.WithHeaders(headers)}
		if ep.insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
	return otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(ep.url), otlpmetrichttp.WithHeaders(headers))
}

func newLogExporter(ctx context.Context, raw string, headers map[string]string) (sdklog.Exporter, error) {
	ep, err := parseEndpoint(raw)
	if err != nil {
		return nil, err
	}
	if ep.grpc {
		opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(ep.hostPort), otlploggrpc.WithHeaders(headers)}
		if ep.insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		return otlploggrpc.New(ctx, opts...)
	}
	return otlploghttp.New(ctx, otlploghttp.WithEndpointURL(ep.url), otlploghttp.WithHeaders(headers))
}
