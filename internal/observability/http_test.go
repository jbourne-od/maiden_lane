package observability

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const fixturePattern = "/fixtures/{fixtureID}"

func TestInstrumentHTTPRouteSpanContract(t *testing.T) {
	_, router, spans, _ := newHTTPFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	}))

	request := httptest.NewRequest(http.MethodPost, "/fixtures/hostile-path-value?secret=hostile-query-value", strings.NewReader("hostile-body-value"))
	request.Host = "hostile-host-value"
	request.RemoteAddr = "hostile-peer-value:4318"
	request.Header.Set("User-Agent", "hostile-agent-value")
	request.Header.Set("X-Customer-Secret", "hostile-header-value")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	span := ended[0]
	if got := span.Name(); got != "POST "+fixturePattern {
		t.Fatalf("span name = %q", got)
	}
	if got := span.SpanKind(); got != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want server", got)
	}
	if got := span.Status().Code; got != codes.Ok {
		t.Fatalf("status = %v, want OK", span.Status())
	}
	assertExactSpanAttributes(t, span, map[string]any{
		"http.request.method":       "POST",
		"http.route":                fixturePattern,
		"http.response.status_code": int64(http.StatusCreated),
		"network.protocol.name":     "http",
		"network.protocol.version":  "1.1",
	})
	assertAbsentFromSpan(t, span,
		"hostile-path-value", "hostile-query-value", "hostile-body-value",
		"hostile-host-value", "hostile-peer-value", "hostile-agent-value", "hostile-header-value",
	)
}

func TestInstrumentHTTPRouteTraceParentWithoutBaggage(t *testing.T) {
	var handlerSpanContext trace.SpanContext
	var handlerBaggage baggage.Baggage
	_, router, spans, _ := newHTTPFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlerSpanContext = trace.SpanContextFromContext(request.Context())
		handlerBaggage = baggage.FromContext(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil)
	request.Header.Set("Traceparent", "00-00000000000000000000000000000001-0000000000000002-01")
	request.Header.Set("Baggage", "secret=hostile-baggage-value")
	member, err := baggage.NewMember("local-secret", "hostile-local-baggage-value")
	if err != nil {
		t.Fatalf("NewMember: %v", err)
	}
	localBaggage, err := baggage.New(member)
	if err != nil {
		t.Fatalf("New baggage: %v", err)
	}
	request = request.WithContext(baggage.ContextWithBaggage(request.Context(), localBaggage))
	router.ServeHTTP(httptest.NewRecorder(), request)

	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	remoteParent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{15: 1},
		SpanID:     trace.SpanID{7: 2},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	if got := ended[0].Parent(); got.SpanID() != remoteParent.SpanID() || got.TraceID() != remoteParent.TraceID() || !got.IsRemote() {
		t.Fatalf("parent = %v, want remote %v", got, remoteParent)
	}
	if !handlerSpanContext.IsValid() || handlerSpanContext.TraceID() != remoteParent.TraceID() {
		t.Fatalf("handler span context = %v, want valid child of %v", handlerSpanContext, remoteParent)
	}
	if got := handlerBaggage.Len(); got != 0 {
		t.Fatalf("handler baggage members = %d, want 0: %v", got, handlerBaggage)
	}
	assertAbsentFromSpan(t, ended[0], "hostile-baggage-value", "hostile-local-baggage-value")
}

func TestDisabledRuntimeRouteIsHarmless(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	runtime, err := newRuntime(t.Context(), cfg, io.Discard, factories{})
	if err != nil {
		t.Fatalf("newRuntime: %v", err)
	}
	if runtime.tracerProvider == nil || runtime.meterProvider == nil || runtime.propagator == nil {
		t.Fatalf("disabled providers = %#v, %#v, %#v", runtime.tracerProvider, runtime.meterProvider, runtime.propagator)
	}

	called := false
	handler := runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		if !trace.SpanContextFromContext(request.Context()).IsValid() {
			t.Fatal("handler did not receive extracted trace context")
		}
		if baggage.FromContext(request.Context()).Len() != 0 {
			t.Fatal("handler received baggage")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil)
	request.Header.Set("Traceparent", "00-00000000000000000000000000000001-0000000000000002-01")
	request.Header.Set("Baggage", "secret=hostile-baggage-value")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !called {
		t.Fatal("wrapped handler was not served")
	}
}

func TestInstrumentHTTPRouteMetrics(t *testing.T) {
	const requestBody = "observed request bytes"
	const responseBody = "observed response bytes"
	_, router, _, reader := newHTTPFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.ReadAll(request.Body); err != nil {
			t.Errorf("read request body: %v", err)
		}
		writer.WriteHeader(http.StatusCreated)
		if _, err := io.WriteString(writer, responseBody); err != nil {
			t.Errorf("write response body: %v", err)
		}
	}))

	request := httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", strings.NewReader(requestBody))
	request.Header.Set("Traceparent", "00-00000000000000000000000000000001-0000000000000002-01")
	router.ServeHTTP(httptest.NewRecorder(), request)

	metrics := collectMetrics(t, reader)
	wantUnits := map[string]string{
		"http.server.request.duration":   "s",
		"http.server.request.body.size":  "By",
		"http.server.response.body.size": "By",
	}
	if len(metrics) != len(wantUnits) {
		t.Fatalf("metric count = %d, want %d: %#v", len(metrics), len(wantUnits), metrics)
	}
	for name, wantUnit := range wantUnits {
		measurement, ok := metrics[name]
		if !ok {
			t.Errorf("metric %q was not registered: %#v", name, metrics)
			continue
		}
		if measurement.Unit != wantUnit {
			t.Errorf("metric %q unit = %q, want %q", name, measurement.Unit, wantUnit)
		}
		pointAttributes, count, exemplars := histogramPoint(t, measurement)
		if count != 1 {
			t.Errorf("metric %q point count = %d, want 1", name, count)
		}
		if !reflect.DeepEqual(pointAttributes, map[string]any{
			"http.request.method":       "POST",
			"http.route":                fixturePattern,
			"http.response.status_code": int64(http.StatusCreated),
		}) {
			t.Errorf("metric %q attributes = %#v", name, pointAttributes)
		}
		if exemplars != 0 {
			t.Errorf("metric %q exemplars = %d, want 0", name, exemplars)
		}
	}
	if got := histogramInt64Sum(t, metrics["http.server.request.body.size"]); got != int64(len(requestBody)) {
		t.Errorf("request body size = %d, want %d", got, len(requestBody))
	}
	if got := histogramInt64Sum(t, metrics["http.server.response.body.size"]); got != int64(len(responseBody)) {
		t.Errorf("response body size = %d, want %d", got, len(responseBody))
	}
	if got := histogramFloat64Sum(t, metrics["http.server.request.duration"]); got < 0 {
		t.Errorf("duration = %f, want non-negative", got)
	}
}

func TestInstrumentHTTPRouteStatus(t *testing.T) {
	t.Run("pure classification", func(t *testing.T) {
		tests := []struct {
			name       string
			panicked   bool
			requestErr error
			status     int
			wantCode   codes.Code
			wantType   string
			wantStatus bool
		}{
			{"informational", false, nil, 100, codes.Ok, "", true},
			{"no content", false, nil, 204, codes.Ok, "", true},
			{"redirect", false, nil, 302, codes.Ok, "", true},
			{"client lower", false, nil, 400, codes.Error, "http.client_error", true},
			{"client upper", false, nil, 499, codes.Error, "http.client_error", true},
			{"server lower", false, nil, 500, codes.Error, "http.server_error", true},
			{"server upper", false, nil, 599, codes.Error, "http.server_error", true},
			{"invalid", false, nil, 600, codes.Error, "invalid_http_status", false},
			{"canceled precedes invalid", false, context.Canceled, 600, codes.Error, "request_canceled", false},
			{"panic precedes cancellation", true, context.Canceled, 600, codes.Error, "handler_panic", false},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				gotCode, gotType, gotStatus := classifyHTTPResult(test.panicked, test.requestErr, test.status)
				if gotCode != test.wantCode || gotType != test.wantType || gotStatus != test.wantStatus {
					t.Fatalf("classification = (%v, %q, %t), want (%v, %q, %t)", gotCode, gotType, gotStatus, test.wantCode, test.wantType, test.wantStatus)
				}
			})
		}
	})

	tests := []struct {
		name            string
		requestContext  func(context.Context) context.Context
		handler         http.Handler
		writer          func() http.ResponseWriter
		wantCode        codes.Code
		wantDescription string
		wantStatus      int
	}{
		{
			name: "informational then final success",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusEarlyHints)
				writer.WriteHeader(http.StatusNoContent)
			}),
			writer:     func() http.ResponseWriter { return newPermissiveResponseWriter() },
			wantCode:   codes.Ok,
			wantStatus: http.StatusNoContent,
		},
		{
			name: "client error",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNotFound)
			}),
			writer:          func() http.ResponseWriter { return httptest.NewRecorder() },
			wantCode:        codes.Error,
			wantDescription: "http.client_error",
			wantStatus:      http.StatusNotFound,
		},
		{
			name: "server error",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusServiceUnavailable)
			}),
			writer:          func() http.ResponseWriter { return httptest.NewRecorder() },
			wantCode:        codes.Error,
			wantDescription: "http.server_error",
			wantStatus:      http.StatusServiceUnavailable,
		},
		{
			name: "cancellation precedes status",
			requestContext: func(ctx context.Context) context.Context {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx
			},
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusServiceUnavailable)
			}),
			writer:          func() http.ResponseWriter { return httptest.NewRecorder() },
			wantCode:        codes.Error,
			wantDescription: "request_canceled",
			wantStatus:      http.StatusServiceUnavailable,
		},
		{
			name: "invalid status is not exported",
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(600)
			}),
			writer:          func() http.ResponseWriter { return newPermissiveResponseWriter() },
			wantCode:        codes.Error,
			wantDescription: "invalid_http_status",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, _, spans, reader := newHTTPFixture(t, test.handler)
			wrapped := runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern, test.handler)
			request := httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil)
			if test.requestContext != nil {
				request = request.WithContext(test.requestContext(request.Context()))
			}
			wrapped.ServeHTTP(test.writer(), request)
			ended := spans.Ended()
			if len(ended) != 1 {
				t.Fatalf("ended spans = %d, want 1", len(ended))
			}
			span := ended[0]
			if span.Status().Code != test.wantCode || span.Status().Description != test.wantDescription {
				t.Fatalf("span status = %v, want code %v description %q", span.Status(), test.wantCode, test.wantDescription)
			}
			attributes := spanAttributeMap(span)
			if test.wantStatus == 0 {
				if _, ok := attributes["http.response.status_code"]; ok {
					t.Errorf("invalid response status was exported: %#v", attributes)
				}
			} else if got := attributes["http.response.status_code"]; got != int64(test.wantStatus) {
				t.Errorf("status attribute = %#v, want %d", got, test.wantStatus)
			}
			if test.wantDescription == "" {
				if _, ok := attributes["error.type"]; ok {
					t.Errorf("successful span has error.type: %#v", attributes)
				}
			} else if got := attributes["error.type"]; got != test.wantDescription {
				t.Errorf("error.type = %#v, want %q", got, test.wantDescription)
			}
			if test.wantStatus == 0 {
				for name, measurement := range collectMetrics(t, reader) {
					pointAttributes, _, _ := histogramPoint(t, measurement)
					if _, ok := pointAttributes["http.response.status_code"]; ok {
						t.Errorf("metric %q exported invalid status: %#v", name, pointAttributes)
					}
				}
			}
		})
	}
}

func TestInstrumentHTTPRoutePanic(t *testing.T) {
	const hostilePanic = "hostile-panic-value"
	_, router, spans, reader := newHTTPFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(hostilePanic)
	}))
	request := httptest.NewRequest(http.MethodPost, "/fixtures/hostile-path-value", nil)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		router.ServeHTTP(httptest.NewRecorder(), request)
	}()
	if recovered != http.ErrAbortHandler {
		t.Fatalf("recovered panic = %#v, want http.ErrAbortHandler", recovered)
	}
	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	span := ended[0]
	if span.Status().Code != codes.Error || span.Status().Description != "handler_panic" {
		t.Fatalf("panic span status = %v", span.Status())
	}
	attributes := spanAttributeMap(span)
	if attributes["error.type"] != "handler_panic" {
		t.Errorf("panic error.type = %#v", attributes["error.type"])
	}
	if _, ok := attributes["http.response.status_code"]; ok {
		t.Errorf("uncommitted panic exported a default status: %#v", attributes)
	}
	assertAbsentFromSpan(t, span, hostilePanic, "hostile-path-value")
	for name, measurement := range collectMetrics(t, reader) {
		pointAttributes, count, _ := histogramPoint(t, measurement)
		if count != 1 {
			t.Errorf("panic metric %q count = %d, want 1", name, count)
		}
		if _, ok := pointAttributes["http.response.status_code"]; ok {
			t.Errorf("panic metric %q exported default status: %#v", name, pointAttributes)
		}
	}
}

func TestInstrumentHTTPRoutePanicAfterCommit(t *testing.T) {
	_, router, spans, _ := newHTTPFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
		panic("hostile-panic-after-commit")
	}))
	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Errorf("recovered panic = %#v, want http.ErrAbortHandler", recovered)
			}
		}()
		router.ServeHTTP(newPermissiveResponseWriter(), httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil))
	}()
	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attributes := spanAttributeMap(ended[0])
	if got := attributes["http.response.status_code"]; got != int64(http.StatusNoContent) {
		t.Errorf("committed panic status = %#v, want %d", got, http.StatusNoContent)
	}
	if got := attributes["error.type"]; got != "handler_panic" {
		t.Errorf("committed panic error.type = %#v", got)
	}
	assertAbsentFromSpan(t, ended[0], "hostile-panic-after-commit")
}

func TestInstrumentHTTPRouteLegacyPanicNil(t *testing.T) {
	const childEnv = "MAIDEN_LANE_HTTP_PANIC_NIL_CHILD"
	if os.Getenv(childEnv) == "1" {
		_, router, spans, reader := newHTTPFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(nil)
		}))

		returned := false
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil))
			returned = true
		}()
		if returned || recovered != http.ErrAbortHandler {
			t.Fatalf("panic(nil) terminal result = returned %t, recovered %#v; want http.ErrAbortHandler", returned, recovered)
		}
		ended := spans.Ended()
		if len(ended) != 1 {
			t.Fatalf("ended spans = %d, want 1", len(ended))
		}
		span := ended[0]
		if span.Status().Code != codes.Error || span.Status().Description != "handler_panic" {
			t.Fatalf("panic(nil) span status = %v, want handler_panic Error", span.Status())
		}
		attributes := spanAttributeMap(span)
		if _, ok := attributes["http.response.status_code"]; ok {
			t.Fatalf("uncommitted panic(nil) exported status: %#v", attributes)
		}
		if got := attributes["error.type"]; got != "handler_panic" {
			t.Fatalf("panic(nil) error.type = %#v", got)
		}
		assertFinalizedHTTPMetrics(t, collectMetrics(t, reader), 0)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestInstrumentHTTPRouteLegacyPanicNil$")
	command.Env = append(withoutEnvironmentKeys(os.Environ(), "GODEBUG", childEnv),
		"GODEBUG=panicnil=1", childEnv+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("panic(nil) child: %v\n%s", err, output)
	}
}

func TestInstrumentHTTPRouteFlushCommitsStatusBeforePanic(t *testing.T) {
	tests := []struct {
		name    string
		writer  func() http.ResponseWriter
		handler http.Handler
	}{
		{
			name:   "http flusher",
			writer: func() http.ResponseWriter { return newOptionalResponseWriter() },
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.(http.Flusher).Flush()
				panic("hostile-panic-after-flush")
			}),
		},
		{
			name:   "response controller flush error",
			writer: func() http.ResponseWriter { return newFlushErrorResponseWriter() },
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if err := http.NewResponseController(writer).Flush(); err != nil {
					t.Fatalf("ResponseController.Flush: %v", err)
				}
				panic("hostile-panic-after-flush-error")
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, _, spans, reader := newHTTPFixture(t, test.handler)
			handler := runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern, test.handler)
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				handler.ServeHTTP(test.writer(), httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil))
			}()
			if recovered != http.ErrAbortHandler {
				t.Fatalf("recovered panic = %#v, want http.ErrAbortHandler", recovered)
			}
			ended := spans.Ended()
			if len(ended) != 1 {
				t.Fatalf("ended spans = %d, want 1", len(ended))
			}
			span := ended[0]
			if span.Status().Code != codes.Error || span.Status().Description != "handler_panic" {
				t.Fatalf("flush panic span status = %v", span.Status())
			}
			attributes := spanAttributeMap(span)
			if got := attributes["http.response.status_code"]; got != int64(http.StatusOK) {
				t.Fatalf("flush panic status = %#v, want %d", got, http.StatusOK)
			}
			if got := attributes["error.type"]; got != "handler_panic" {
				t.Fatalf("flush panic error.type = %#v", got)
			}
			assertFinalizedHTTPMetrics(t, collectMetrics(t, reader), http.StatusOK)
		})
	}
}

func TestInstrumentHTTPRoutePrivacy(t *testing.T) {
	hostileValues := []string{
		"hostile-path-value", "hostile-query-value", "hostile-header-value",
		"hostile-body-value", "hostile-response-value", "hostile-host-value",
		"hostile-peer-value", "hostile-agent-value", "hostile-baggage-value",
		"HOSTILE_ARBITRARY_METHOD",
	}
	runtime, _, spans, reader := newHTTPFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = io.WriteString(writer, "hostile-response-value")
	}))
	handler := runtime.InstrumentHTTPRoute("HOSTILE_ARBITRARY_METHOD", fixturePattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = io.WriteString(writer, "hostile-response-value")
	}))
	request := httptest.NewRequest("HOSTILE_ARBITRARY_METHOD", "/fixtures/hostile-path-value?secret=hostile-query-value", strings.NewReader("hostile-body-value"))
	request.Host = "hostile-host-value"
	request.RemoteAddr = "hostile-peer-value:1234"
	request.Header.Set("User-Agent", "hostile-agent-value")
	request.Header.Set("X-Secret", "hostile-header-value")
	request.Header.Set("Baggage", "secret=hostile-baggage-value")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	if got := spanAttributeMap(ended[0])["http.request.method"]; got != "OTHER" {
		t.Fatalf("normalized method = %#v, want OTHER", got)
	}
	resourceMetrics := collectResourceMetrics(t, reader)
	representation := fmt.Sprintf("spans=%#v resource-metrics=%#v", ended, resourceMetrics)
	for _, value := range hostileValues {
		if strings.Contains(representation, value) {
			t.Errorf("telemetry exposed hostile value %q: %s", value, representation)
		}
	}
}

func TestInstrumentHTTPRouteExcludedByRegistration(t *testing.T) {
	runtime, _, spans, reader := newHTTPFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	router := chi.NewRouter()
	router.Get("/healthz", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	router.Get("/readyz", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	router.Method(http.MethodPost, fixturePattern, runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })))

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
		httptest.NewRequest(http.MethodGet, "/unknown/hostile-path-value", nil),
		httptest.NewRequest(http.MethodGet, "/fixtures/fixture-1", nil),
	} {
		router.ServeHTTP(httptest.NewRecorder(), request)
	}
	if got := len(spans.Ended()); got != 0 {
		t.Fatalf("excluded requests produced %d spans", got)
	}
	if got := collectMetrics(t, reader); len(got) != 0 {
		t.Fatalf("excluded requests produced metrics: %#v", got)
	}
}

func TestInstrumentHTTPRoutePreservesOptionalResponseInterfaces(t *testing.T) {
	writer := newOptionalResponseWriter()
	runtime, _, _, _ := newHTTPFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler := runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern, http.HandlerFunc(func(got http.ResponseWriter, _ *http.Request) {
		if _, ok := got.(http.Flusher); !ok {
			t.Error("wrapped writer does not preserve http.Flusher")
		}
		if _, ok := got.(http.Hijacker); !ok {
			t.Error("wrapped writer does not preserve http.Hijacker")
		}
		if _, ok := got.(http.Pusher); !ok {
			t.Error("wrapped writer does not preserve http.Pusher")
		}
		if _, ok := got.(io.ReaderFrom); !ok {
			t.Error("wrapped writer does not preserve io.ReaderFrom")
		}
		got.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/fixtures/fixture-1", nil))
}

func TestHTTPRouteNormalizationAndProtocolAreClosed(t *testing.T) {
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodDelete, http.MethodConnect, http.MethodOptions,
		http.MethodTrace, http.MethodPatch,
	} {
		if got := normalizeMethod(method); got != method {
			t.Errorf("normalizeMethod(%q) = %q", method, got)
		}
	}
	if got := normalizeMethod("hostile-arbitrary-method"); got != "OTHER" {
		t.Errorf("normalized hostile method = %q, want OTHER", got)
	}

	tests := []struct {
		protocol string
		name     string
		version  string
		ok       bool
	}{
		{"HTTP/1.0", "http", "1.0", true},
		{"HTTP/1.1", "http", "1.1", true},
		{"HTTP/2.0", "http", "2", true},
		{"HTTP/3.0", "http", "3", true},
		{"hostile-protocol-value", "", "", false},
	}
	for _, test := range tests {
		name, version, ok := boundedProtocol(test.protocol)
		if name != test.name || version != test.version || ok != test.ok {
			t.Errorf("boundedProtocol(%q) = (%q, %q, %t), want (%q, %q, %t)", test.protocol, name, version, ok, test.name, test.version, test.ok)
		}
	}
}

func newHTTPFixture(t *testing.T, handler http.Handler) (*Runtime, http.Handler, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(spanRecorder),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithResource(resource.Empty()),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
		sdkmetric.WithView(httpMetricViews()...),
	)
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider Shutdown: %v", err)
		}
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider Shutdown: %v", err)
		}
	})
	runtime := &Runtime{
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		propagator:     propagation.TraceContext{},
	}
	if err := runtime.registerHTTPInstruments(); err != nil {
		t.Fatalf("register HTTP instruments: %v", err)
	}
	router := chi.NewRouter()
	router.Method(http.MethodPost, fixturePattern,
		runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern, handler))
	return runtime, router, spanRecorder, metricReader
}

func spanAttributeMap(span sdktrace.ReadOnlySpan) map[string]any {
	attributes := make(map[string]any, len(span.Attributes()))
	for _, value := range span.Attributes() {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	return attributes
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	data := collectResourceMetrics(t, reader)
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			metrics[measurement.Name] = measurement
		}
	}
	return metrics
}

func collectResourceMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return data
}

func assertFinalizedHTTPMetrics(t *testing.T, metrics map[string]metricdata.Metrics, status int) {
	t.Helper()
	if len(metrics) != 3 {
		t.Fatalf("finalized metric count = %d, want 3: %#v", len(metrics), metrics)
	}
	for name, measurement := range metrics {
		attributes, count, _ := histogramPoint(t, measurement)
		if count != 1 {
			t.Errorf("metric %q count = %d, want 1", name, count)
		}
		got, present := attributes["http.response.status_code"]
		if status == 0 && present {
			t.Errorf("metric %q exported an uncommitted status: %#v", name, attributes)
		}
		if status != 0 && got != int64(status) {
			t.Errorf("metric %q status = %#v, want %d", name, got, status)
		}
	}
}

func withoutEnvironmentKeys(environment []string, keys ...string) []string {
	prefixes := make([]string, len(keys))
	for index, key := range keys {
		prefixes[index] = key + "="
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		keep := true
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func histogramPoint(t *testing.T, measurement metricdata.Metrics) (map[string]any, uint64, int) {
	t.Helper()
	var attributes []attribute.KeyValue
	var count uint64
	var exemplarCount int
	switch data := measurement.Data.(type) {
	case metricdata.Histogram[int64]:
		if len(data.DataPoints) != 1 {
			t.Fatalf("metric %q data points = %d, want 1", measurement.Name, len(data.DataPoints))
		}
		attributes = data.DataPoints[0].Attributes.ToSlice()
		count = data.DataPoints[0].Count
		exemplarCount = len(data.DataPoints[0].Exemplars)
	case metricdata.Histogram[float64]:
		if len(data.DataPoints) != 1 {
			t.Fatalf("metric %q data points = %d, want 1", measurement.Name, len(data.DataPoints))
		}
		attributes = data.DataPoints[0].Attributes.ToSlice()
		count = data.DataPoints[0].Count
		exemplarCount = len(data.DataPoints[0].Exemplars)
	default:
		t.Fatalf("metric %q data = %T, want histogram", measurement.Name, measurement.Data)
	}
	got := make(map[string]any, len(attributes))
	for _, value := range attributes {
		got[string(value.Key)] = value.Value.AsInterface()
	}
	return got, count, exemplarCount
}

func histogramInt64Sum(t *testing.T, measurement metricdata.Metrics) int64 {
	t.Helper()
	data, ok := measurement.Data.(metricdata.Histogram[int64])
	if !ok || len(data.DataPoints) != 1 {
		t.Fatalf("metric %q data = %T, want one int64 histogram point", measurement.Name, measurement.Data)
	}
	return data.DataPoints[0].Sum
}

func histogramFloat64Sum(t *testing.T, measurement metricdata.Metrics) float64 {
	t.Helper()
	data, ok := measurement.Data.(metricdata.Histogram[float64])
	if !ok || len(data.DataPoints) != 1 {
		t.Fatalf("metric %q data = %T, want one float64 histogram point", measurement.Name, measurement.Data)
	}
	return data.DataPoints[0].Sum
}

type permissiveResponseWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func newPermissiveResponseWriter() *permissiveResponseWriter {
	return &permissiveResponseWriter{header: make(http.Header)}
}

func (w *permissiveResponseWriter) Header() http.Header { return w.header }

func (w *permissiveResponseWriter) WriteHeader(status int) {
	if status >= 100 && status <= 199 {
		return
	}
	if w.status == 0 {
		w.status = status
	}
}

func (w *permissiveResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

type optionalResponseWriter struct{ *permissiveResponseWriter }

func newOptionalResponseWriter() *optionalResponseWriter {
	return &optionalResponseWriter{permissiveResponseWriter: newPermissiveResponseWriter()}
}

func (w *optionalResponseWriter) Flush() { w.WriteHeader(http.StatusOK) }

func (*optionalResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}

func (*optionalResponseWriter) Push(string, *http.PushOptions) error { return http.ErrNotSupported }

func (w *optionalResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(&w.body, reader)
}

type flushErrorResponseWriter struct{ *permissiveResponseWriter }

func newFlushErrorResponseWriter() *flushErrorResponseWriter {
	return &flushErrorResponseWriter{permissiveResponseWriter: newPermissiveResponseWriter()}
}

func (w *flushErrorResponseWriter) FlushError() error {
	w.WriteHeader(http.StatusOK)
	return nil
}

func assertExactSpanAttributes(t *testing.T, span sdktrace.ReadOnlySpan, want map[string]any) {
	t.Helper()
	got := make(map[string]any, len(span.Attributes()))
	for _, value := range span.Attributes() {
		got[string(value.Key)] = value.Value.AsInterface()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("span attributes = %#v, want %#v", got, want)
	}
}

func assertAbsentFromSpan(t *testing.T, span sdktrace.ReadOnlySpan, values ...string) {
	t.Helper()
	representation := fmt.Sprintf("%#v", span)
	for _, value := range values {
		if strings.Contains(representation, value) {
			t.Errorf("span exposed hostile value %q: %s", value, representation)
		}
	}
}
