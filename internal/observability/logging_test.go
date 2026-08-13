package observability

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestNewLoggerWritesJSONAtConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelWarn)
	logger.Info("hidden")
	logger.Warn("visible", "code", "safe")
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), `"level":"WARN"`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestOTelErrorHandlerDropsSuppliedText(t *testing.T) {
	var output bytes.Buffer
	handler := newOTelErrorHandler(NewLogger(&output, slog.LevelDebug))
	handler.Handle(errors.New("secret endpoint and token"))
	assertContainsOnlyCode(t, output.String(), "otel_async_error", "secret endpoint", "token")
}

func TestOTelLogSinkDropsSuppliedTextAndFields(t *testing.T) {
	var output bytes.Buffer
	sink := newOTelLogSink(NewLogger(&output, slog.LevelDebug))
	sink.Info(0, "secret message", "authorization", "secret token")
	sink.Error(errors.New("secret error"), "secret message", "path", "/secret")
	assertContainsOnlyCodes(t, output.String(), []string{"otel_internal_message", "otel_internal_error"},
		"secret message", "authorization", "secret token", "secret error", "/secret")
}

func TestOTelLogSinkWithValuesAndNameDropSuppliedValues(t *testing.T) {
	var output bytes.Buffer
	var sink logr.LogSink = newOTelLogSink(NewLogger(&output, slog.LevelDebug))
	sink = sink.WithValues("secret key", "secret value").WithName("secret logger name")
	sink.Info(0, "secret message")

	assertContainsOnlyCode(t, output.String(), "otel_internal_message", "secret key", "secret value", "secret logger name", "secret message")
}

func assertContainsOnlyCode(t *testing.T, output, code string, prohibited ...string) {
	t.Helper()
	assertContainsOnlyCodes(t, output, []string{code}, prohibited...)
}

func assertContainsOnlyCodes(t *testing.T, output string, codes []string, prohibited ...string) {
	t.Helper()
	for _, code := range codes {
		if !strings.Contains(output, `"code":"`+code+`"`) {
			t.Errorf("output missing code %q: %s", code, output)
		}
	}
	for _, value := range prohibited {
		if strings.Contains(output, value) {
			t.Errorf("output leaked %q: %s", value, output)
		}
	}
}
