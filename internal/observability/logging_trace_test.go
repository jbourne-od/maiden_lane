package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// spanContextFor builds a valid remote span context without standing up a
// tracer provider. The handler reads only the context, so a synthesized one is
// the honest unit under test: it proves the read works rather than proving the
// SDK produces spans, which is a different assertion.
func spanContextFor(t *testing.T, traceHex, spanHex string) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(traceHex)
	if err != nil {
		t.Fatalf("TraceIDFromHex: %v", err)
	}
	spanID, err := trace.SpanIDFromHex(spanHex)
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, Remote: true,
	})
}

func logRecord(t *testing.T, emit func(*slog.Logger, context.Context)) map[string]any {
	t.Helper()
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelInfo)
	emit(logger, context.Background())
	if output.Len() == 0 {
		t.Fatal("nothing was logged")
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, output.String())
	}
	return record
}

// A log record emitted inside a span must carry that span's address, or a log
// line and the trace covering the same work cannot be connected at all.
func TestLogRecordInsideASpanCarriesItsTraceIdentity(t *testing.T) {
	const (
		traceHex = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanHex  = "00f067aa0ba902b7"
	)
	spanContext := spanContextFor(t, traceHex, spanHex)

	record := logRecord(t, func(logger *slog.Logger, ctx context.Context) {
		logger.InfoContext(trace.ContextWithSpanContext(ctx, spanContext),
			"worker did something", "code", "worker_did_something")
	})

	if got := record["trace_id"]; got != traceHex {
		t.Fatalf("trace_id = %v, want %s", got, traceHex)
	}
	if got := record["span_id"]; got != spanHex {
		t.Fatalf("span_id = %v, want %s", got, spanHex)
	}
	// The wrapper must not disturb what was already being logged.
	if got := record["code"]; got != "worker_did_something" {
		t.Fatalf("code = %v, want the original attribute", got)
	}
	if got := record["msg"]; got != "worker did something" {
		t.Fatalf("msg = %v", got)
	}
}

// Production break caught: process lifecycle logging happens outside any span.
// Emitting empty or zero identifiers there would make every startup line look
// like it belonged to a trace, and a zero trace ID is a valid-looking value that
// a log backend will happily index and correlate against nothing.
func TestLogRecordOutsideASpanCarriesNoTraceIdentity(t *testing.T) {
	for _, testCase := range []struct {
		name string
		emit func(*slog.Logger, context.Context)
	}{
		{
			name: "the context-free variant",
			emit: func(logger *slog.Logger, _ context.Context) {
				logger.Info("HTTP server started", "code", "http_server_started")
			},
		},
		{
			name: "a context with no span",
			emit: func(logger *slog.Logger, ctx context.Context) {
				logger.InfoContext(ctx, "HTTP server started", "code", "http_server_started")
			},
		},
		{
			name: "a context carrying an invalid span context",
			emit: func(logger *slog.Logger, ctx context.Context) {
				ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
				logger.InfoContext(ctx, "HTTP server started", "code", "http_server_started")
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			record := logRecord(t, testCase.emit)
			if _, present := record["trace_id"]; present {
				t.Fatalf("a record outside a span claimed a trace: %+v", record)
			}
			if _, present := record["span_id"]; present {
				t.Fatalf("a record outside a span claimed a span: %+v", record)
			}
		})
	}
}

// Production break caught: slog reuses a Record's attribute storage, so a
// handler that appends without cloning can leak attributes from one record into
// another. Two records logged through the same logger must not contaminate each
// other, and the second must not inherit the first's trace identity.
func TestAddingTraceIdentityDoesNotLeakBetweenRecords(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelInfo)

	spanContext := spanContextFor(t, "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7")
	logger.InfoContext(trace.ContextWithSpanContext(context.Background(), spanContext),
		"inside", "code", "inside")
	logger.Info("outside", "code", "outside")

	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var records []map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if _, present := records[1]["trace_id"]; present {
		t.Fatalf("the second record inherited the first record's trace: %+v", records[1])
	}
	if got := records[1]["code"]; got != "outside" {
		t.Fatalf("second record code = %v, want outside", got)
	}
}

// Level filtering must still be the wrapped handler's decision.
func TestTraceContextHandlerPreservesLevelFiltering(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelWarn)
	logger.Info("below the threshold")
	if output.Len() != 0 {
		t.Fatalf("a filtered record was emitted: %s", output.String())
	}
	logger.Warn("at the threshold")
	if output.Len() == 0 {
		t.Fatal("an admitted record was dropped")
	}
}
