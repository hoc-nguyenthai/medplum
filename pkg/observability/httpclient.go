package observability

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// HTTPTransport wraps base (http.DefaultTransport when nil) so every outgoing request gets a
// CLIENT span, HTTP client metrics and the traceparent header of the caller's context.
// Build requests with http.NewRequestWithContext(ctx, ...) using the request context.
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

// HTTPClient returns an *http.Client using HTTPTransport.
func HTTPClient() *http.Client {
	return &http.Client{Transport: HTTPTransport(nil)}
}
