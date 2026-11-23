package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TraceparentKey is the conventional name of the job field/header carrying the enqueuing
// request's W3C trace context (asynq task header, message attribute, JSON field, ...).
const TraceparentKey = "traceparent"

var traceContext = propagation.TraceContext{}

// Traceparent returns the W3C traceparent of the span in ctx, to send along with a job so the
// job's trace continues the request's trace. Empty when tracing is off or no span is active.
func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	traceContext.Inject(ctx, carrier)
	return carrier.Get(TraceparentKey)
}

// ContinueTrace returns ctx carrying the remote span context decoded from traceparent, so
// spans started from it share the request's trace_id. It returns ctx unchanged when
// traceparent is empty or invalid.
func ContinueTrace(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return traceContext.Extract(ctx, propagation.MapCarrier{TraceparentKey: traceparent})
}

// Enqueue runs enqueue inside an "enqueue <name>" PRODUCER span and hands it the span's
// traceparent to store with the job. system is the messaging system ("asynq", "nats", ...),
// queue may be empty. enqueue returns the job id ("" when unknown) and its error.
//
//	err := observability.Enqueue(ctx, "asynq", "default", "send_email",
//		func(ctx context.Context, traceparent string) (string, error) {
//			info, err := client.EnqueueContext(ctx, task(traceparent))
//			if err != nil {
//				return "", err
//			}
//			return info.ID, nil
//		})
func Enqueue(ctx context.Context, system, queue, name string, enqueue func(ctx context.Context, traceparent string) (string, error)) error {
	attrs := []attribute.KeyValue{
		attribute.String("messaging.system", system),
		attribute.String("messaging.operation.type", "send"),
	}
	if queue != "" {
		attrs = append(attrs, attribute.String("messaging.destination.name", queue))
	}
	ctx, span := Tracer(instrumentationName).Start(ctx, "enqueue "+name,
		trace.WithSpanKind(trace.SpanKindProducer), trace.WithAttributes(attrs...))
	defer span.End()

	id, err := enqueue(ctx, Traceparent(ctx))
	if id != "" {
		span.SetAttributes(attribute.String("messaging.message.id", id))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// Job describes one execution of a background job, for RunJob.
type Job struct {
	System      string // messaging system, e.g. "asynq"
	Name        string // job/task type
	ID          string // job id, optional
	Try         int    // 1-based attempt number, 0 when unknown
	Traceparent string // from the job, as stored by Enqueue; empty starts a new trace
	// IsRetry reports errors that are an expected retry request rather than a failure
	// (like arq's Retry). They add a "<system>.retry_requested" event instead of marking the
	// span as an error. Optional.
	IsRetry func(error) bool
}

// RunJob runs fn inside a "job <name>" CONSUMER span that continues the trace of the request
// that enqueued the job. Errors and panics are recorded on the span and returned/re-panicked
// unchanged, so the queue library still sees them.
func RunJob(ctx context.Context, job Job, fn func(ctx context.Context) error) (err error) {
	attrs := []attribute.KeyValue{
		attribute.String("messaging.system", job.System),
		attribute.String("messaging.operation.type", "process"),
	}
	if job.ID != "" {
		attrs = append(attrs, attribute.String("messaging.message.id", job.ID))
	}
	if job.Try > 0 {
		attrs = append(attrs, attribute.Int(job.System+".job.try", job.Try))
	}
	ctx, span := Tracer(instrumentationName).Start(ContinueTrace(ctx, job.Traceparent), "job "+job.Name,
		trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attrs...))
	defer span.End()
	defer func() {
		if p := recover(); p != nil {
			perr, ok := p.(error)
			if !ok {
				perr = fmt.Errorf("panic: %v", p)
			}
			span.RecordError(perr, trace.WithStackTrace(true))
			span.SetStatus(codes.Error, perr.Error())
			panic(p)
		}
	}()

	err = fn(ctx)
	switch {
	case err == nil:
	case job.IsRetry != nil && job.IsRetry(err):
		span.RecordError(err)
		span.AddEvent(job.System + ".retry_requested")
	default:
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
