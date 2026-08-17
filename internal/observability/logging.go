package observability

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var installGlobalsOnce sync.Once

// NewLogger constructs the process logger with the explicitly selected level.
// JSON preserves structured operational diagnostics without encoding semantic
// payloads or configured values into log records.
func NewLogger(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(traceContextHandler{
		next: slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}),
	})
}

// traceContextHandler adds the active trace and span identity to records logged
// with a context.
//
// slog does not do this on its own: a Handler is given the context, and reading
// the span out of it is the Handler's job. Nothing here is OTel-specific beyond
// that read, and no exporter needs to be configured for it to be correct -- with
// tracing disabled there is no valid span context and nothing is added.
//
// Records logged through Info or Error rather than their Context variants carry
// no identity, because slog passes context.Background() for those. That is right
// rather than unfortunate: process lifecycle logging happens outside any span,
// and a log line claiming to belong to a trace it is not part of would be worse
// than one that stays silent.
//
// Only the two identifiers are added. They are the trace's own address: not
// customer data, not semantic identity, and not a dimension the metric allowlist
// governs. Exemplars remain disabled, so this is the only place the two signals
// are connected, and it connects them in the direction that cannot inflate a
// metric's cardinality.
type traceContextHandler struct {
	next slog.Handler
}

func (h traceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h traceContextHandler) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return h.next.Handle(ctx, record)
	}
	// A Record's attributes share backing storage with copies of it, so adding
	// to one that was handed to us can corrupt another handler's view. Cloning
	// is the documented way to modify a record, and it happens only on the path
	// where something is actually added.
	record = record.Clone()
	record.AddAttrs(
		slog.String("trace_id", spanContext.TraceID().String()),
		slog.String("span_id", spanContext.SpanID().String()),
	)
	return h.next.Handle(ctx, record)
}

func (h traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceContextHandler{next: h.next.WithAttrs(attrs)}
}

func (h traceContextHandler) WithGroup(name string) slog.Handler {
	return traceContextHandler{next: h.next.WithGroup(name)}
}

// installOTelGlobals installs process-wide OpenTelemetry diagnostics exactly
// once. OTel diagnostics may carry exporter endpoints, headers, or other
// sensitive values, so the installed handlers emit only fixed stable codes.
func installOTelGlobals(logger *slog.Logger) {
	installGlobalsOnce.Do(func() {
		otel.SetErrorHandler(newOTelErrorHandler(logger))
		otel.SetLogger(logr.New(newOTelLogSink(logger)))
	})
}

type otelErrorHandler struct {
	logger *slog.Logger
}

func newOTelErrorHandler(logger *slog.Logger) otelErrorHandler {
	return otelErrorHandler{logger: logger}
}

// Handle intentionally discards OTel-supplied error text. The error can
// include credentials, endpoint URLs, or customer values.
func (h otelErrorHandler) Handle(error) {
	h.logger.Error("OpenTelemetry asynchronous failure", "code", "otel_async_error")
}

type otelLogSink struct {
	logger *slog.Logger
}

func newOTelLogSink(logger *slog.Logger) *otelLogSink {
	return &otelLogSink{logger: logger}
}

func (s *otelLogSink) Init(logr.RuntimeInfo) {}

func (s *otelLogSink) Enabled(int) bool { return true }

// Info intentionally discards OTel-supplied message text and fields.
func (s *otelLogSink) Info(_ int, _ string, _ ...any) {
	s.logger.Debug("OpenTelemetry internal message", "code", "otel_internal_message")
}

// Error intentionally discards OTel-supplied error text, message text, and
// fields. These values are not safe to include in ordinary process logs.
func (s *otelLogSink) Error(_ error, _ string, _ ...any) {
	s.logger.Error("OpenTelemetry internal failure", "code", "otel_internal_error")
}

// WithValues preserves the sink while deliberately discarding supplied values.
func (s *otelLogSink) WithValues(...any) logr.LogSink {
	return &otelLogSink{logger: s.logger}
}

// WithName preserves the sink while deliberately discarding supplied names.
func (s *otelLogSink) WithName(string) logr.LogSink {
	return &otelLogSink{logger: s.logger}
}
