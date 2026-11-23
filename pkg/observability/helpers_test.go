package observability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type logKeyValue = attribute.KeyValue

func metricAttr(key, value string) metric.AddOption {
	return metric.WithAttributes(attribute.String(key, value))
}

func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	t.Fatalf("metric %q not found", name)
	return metricdata.Metrics{}
}

func assertAttr(t *testing.T, span sdktrace.ReadOnlySpan, key, want string) {
	t.Helper()
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			if got := kv.Value.Emit(); got != want {
				t.Errorf("%s: %s = %q, want %q", span.Name(), key, got, want)
			}
			return
		}
	}
	t.Errorf("%s: attribute %s missing", span.Name(), key)
}

// unreachable is an endpoint nothing listens on (discard port).
const unreachable = "grpc://127.0.0.1:9"

// testSettings enables everything, pointed at an unreachable backend; tests replace the
// exporters with in-memory ones through options.
func testSettings() Settings {
	s := DefaultSettings()
	s.ServiceName = "test-svc"
	s.Environment = "test"
	s.Enabled = true
	s.TracesEndpoint = unreachable
	s.LogsEndpoint = unreachable
	s.MetricsEndpoint = unreachable
	s.ShutdownTimeout = time.Second
	return s
}

// captureStdout redirects the library's stdout handler into a buffer for the test.
func captureStdout(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := stdout
	stdout = buf
	t.Cleanup(func() { stdout = prev })
	return buf
}

// setup runs Setup with an in-memory span recorder and shuts down at the end of the test.
func setup(t *testing.T, s Settings, opts ...Option) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	if err := Setup(context.Background(), s, append([]Option{WithSpanProcessor(recorder)}, opts...)...); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { Shutdown(time.Second) })
	return recorder
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines returns every JSON line written so far.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("stdout line is not JSON: %q", scanner.Text())
		}
		out = append(out, line)
	}
	return out
}

func (b *syncBuffer) find(t *testing.T, message string) map[string]any {
	t.Helper()
	for _, line := range b.lines(t) {
		if line["message"] == message {
			return line
		}
	}
	t.Fatalf("no stdout line with message %q in:\n%s", message, b.buf.String())
	return nil
}

// memoryLogProcessor keeps every OTLP log record in memory.
type memoryLogProcessor struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (p *memoryLogProcessor) OnEmit(_ context.Context, r *sdklog.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, r.Clone())
	return nil
}

func (p *memoryLogProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (p *memoryLogProcessor) Shutdown(context.Context) error                         { return nil }
func (p *memoryLogProcessor) ForceFlush(context.Context) error                       { return nil }

func (p *memoryLogProcessor) bodies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.records))
	for i, r := range p.records {
		out[i] = r.Body().AsString()
	}
	return out
}

func (p *memoryLogProcessor) find(body string) (sdklog.Record, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.records {
		if r.Body().AsString() == body {
			return r, true
		}
	}
	return sdklog.Record{}, false
}
