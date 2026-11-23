package echo4obs_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	observability "trivita-fhir/pkg/observability"
	"trivita-fhir/pkg/observability/echo4obs"
)

// captureStdout swaps os.Stdout for a pipe and returns a function that restores it and
// returns the JSON lines written meanwhile.
func captureStdout(t *testing.T) func() []map[string]any {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	return func() []map[string]any {
		os.Stdout = prev
		w.Close()
		data, _ := io.ReadAll(r)
		var lines []map[string]any
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			var line map[string]any
			if json.Unmarshal(scanner.Bytes(), &line) == nil {
				lines = append(lines, line)
			}
		}
		return lines
	}
}

func TestEchoV4(t *testing.T) {
	done := captureStdout(t)
	s := observability.DefaultSettings()
	s.ServiceName = "echo4-test"
	s.Enabled = true
	recorder := tracetest.NewSpanRecorder()

	e := echo.New()
	if err := echo4obs.Setup(context.Background(), e, s, observability.WithSpanProcessor(recorder)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { observability.Shutdown(time.Second) })
	e.GET("/orders/:id", func(c echo.Context) error {
		c.Logger().Infof("loading order %s", c.Param("id"))
		return c.String(http.StatusOK, "ok")
	})
	e.GET("/health", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	e.GET("/boom", func(c echo.Context) error { return errors.New("db down") })

	for _, target := range []string{"/orders/7", "/health", "/boom", "/nope"} {
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}
	lines := done()

	var names []string
	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindServer {
			names = append(names, span.Name())
		}
	}
	if len(names) != 3 || names[0] != "GET /orders/:id" || names[1] != "GET /boom" || names[2] != "GET" {
		t.Fatalf("server spans: %v", names)
	}
	if boom := recorder.Ended()[1]; boom.Status().Code != codes.Error {
		t.Fatalf("/boom status %v", boom.Status())
	}

	orderTrace := recorder.Ended()[0].SpanContext().TraceID().String()
	var sawEchoLog, sawAccessLog bool
	for _, line := range lines {
		switch {
		case line["logger"] == "echo" && line["message"] == "loading order 7":
			sawEchoLog = true
		case line["logger"] == "http.access" && line["route"] == "/orders/:id":
			sawAccessLog = line["status"] == 200.0 && line["trace_id"] == orderTrace
		case line["logger"] == "http.access" && line["path"] == "/health":
			t.Error("access log written for an excluded URL")
		}
	}
	if !sawEchoLog || !sawAccessLog {
		t.Fatalf("echo log %v, access log %v in %v", sawEchoLog, sawAccessLog, lines)
	}
}
