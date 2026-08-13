package observability

import (
	"io"
	"log/slog"
	"sync"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
)

var installGlobalsOnce sync.Once

// NewLogger constructs the process logger with the explicitly selected level.
// JSON preserves structured operational diagnostics without encoding semantic
// payloads or configured values into log records.
func NewLogger(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
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
