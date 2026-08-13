package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	collectorMetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectorTrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	metricProto "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestNewRuntimeDisabledDoesNotCreateExporters(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	f := factories{
		newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			t.Fatal("trace factory called in disabled mode")
			return nil, nil, nil
		},
		newMetric: func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
			t.Fatal("metric factory called in disabled mode")
			return nil, nil, nil
		},
	}
	cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	runtime, err := newRuntime(t.Context(), cfg, io.Discard, f)
	if err != nil {
		t.Fatalf("newRuntime: %v", err)
	}
	if runtime.tracerProvider == nil || runtime.meterProvider == nil || runtime.propagator == nil {
		t.Fatalf("disabled runtime providers = %#v, %#v, %#v", runtime.tracerProvider, runtime.meterProvider, runtime.propagator)
	}
	if runtime.httpDuration == nil || runtime.httpRequestSize == nil || runtime.httpResponseSize == nil {
		t.Fatalf("disabled HTTP instruments = %#v, %#v, %#v", runtime.httpDuration, runtime.httpRequestSize, runtime.httpResponseSize)
	}
	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewRuntimeResourceHasOnlyApprovedServiceAttributes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	var resources []*resource.Resource
	life := &recordingLifecycle{}
	f := factories{
		newTrace: func(_ context.Context, _ Config, res *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			resources = append(resources, res)
			return tracenoop.NewTracerProvider(), life, nil
		},
		newMetric: func(_ context.Context, _ Config, res *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
			resources = append(resources, res)
			return metricnoop.NewMeterProvider(), life, nil
		},
	}
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":  "otlp",
		"OTEL_METRICS_EXPORTER": "otlp",
		"OTEL_SERVICE_NAME":     "test-service",
	}), rejectRead, "v1.2.3")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if _, err := newRuntime(t.Context(), cfg, io.Discard, f); err != nil {
		t.Fatalf("newRuntime: %v", err)
	}
	if len(resources) != 2 || resources[0] != resources[1] {
		t.Fatalf("resources = %#v, want one shared resource", resources)
	}
	want := []attribute.KeyValue{
		attribute.String("service.name", "test-service"),
		attribute.String("service.version", "v1.2.3"),
	}
	if got := resources[0].Attributes(); !equalAttributes(got, want) {
		t.Fatalf("resource attributes = %#v, want %#v", got, want)
	}
	if got, want := resources[0].SchemaURL(), "https://opentelemetry.io/schemas/1.43.0"; got != want {
		t.Fatalf("resource schema = %q, want %q", got, want)
	}
}

func TestNewRuntimeResourceOmitsDevelopmentVersion(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	var got *resource.Resource
	life := &recordingLifecycle{}
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER": "otlp",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	_, err = newRuntime(t.Context(), cfg, io.Discard, factories{
		newTrace: func(_ context.Context, _ Config, res *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			got = res
			return tracenoop.NewTracerProvider(), life, nil
		},
	})
	if err != nil {
		t.Fatalf("newRuntime: %v", err)
	}
	want := []attribute.KeyValue{attribute.String("service.name", "maiden-lane")}
	if attributes := got.Attributes(); !equalAttributes(attributes, want) {
		t.Fatalf("resource attributes = %#v, want %#v", attributes, want)
	}
}

func TestNewRuntimeRejectsAmbientResourceAttributesAfterValidation(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER": "otlp",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "tenant.secret=hostile-value")

	_, err = newRuntime(t.Context(), cfg, io.Discard, factories{})
	assertSafeFieldError(t, err, "OTEL_RESOURCE_ATTRIBUTES", "tenant.secret", "hostile-value")
}

func TestProviderFactoriesRejectAmbientResourceImmediatelyBeforeSDKConstruction(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":                "otlp",
		"OTEL_METRICS_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "http://127.0.0.1:4318/v1/traces",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:4318/v1/metrics",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	res := resource.NewWithAttributes("", attribute.String("service.name", "maiden-lane"))
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "tenant.secret=hostile-inner-value")

	_, _, traceErr := newTraceProvider(t.Context(), cfg, res)
	assertSafeFieldError(t, traceErr, "OTEL_RESOURCE_ATTRIBUTES", "tenant.secret", "hostile-inner-value")
	_, _, metricErr := newMetricProvider(t.Context(), cfg, res)
	assertSafeFieldError(t, metricErr, "OTEL_RESOURCE_ATTRIBUTES", "tenant.secret", "hostile-inner-value")
}

func TestNewRuntimePreservesSafeAmbientResourceFactoryError(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	cfg := enabledTraceConfigWithExplicitEndpoint(t, "http://127.0.0.1:4318/v1/traces")
	_, err := newRuntime(t.Context(), cfg, io.Discard, factories{
		newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			return nil, nil, errAmbientResource
		},
	})
	assertSafeFieldError(t, err, "OTEL_RESOURCE_ATTRIBUTES")
}

func TestNewRuntimeRejectsUnvalidatedConfig(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	_, err := newRuntime(t.Context(), Config{}, io.Discard, factories{})
	if err == nil {
		t.Fatal("newRuntime accepted an unvalidated Config")
	}
}

func TestNewRuntimeAllowsAmbientServiceName(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	t.Setenv("OTEL_SERVICE_NAME", "real-process-service")
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_SERVICE_NAME":                     "real-process-service",
		"OTEL_TRACES_EXPORTER":                  "otlp",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":    "http://127.0.0.1:4318/v1/traces",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT":     "100",
		"OTEL_EXPORTER_OTLP_TRACES_COMPRESSION": "none",
		"OTEL_EXPORTER_OTLP_TRACES_INSECURE":    "true",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL":    "http/protobuf",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	runtime, err := newRuntime(t.Context(), cfg, io.Discard, defaultFactories())
	if err != nil {
		t.Fatalf("newRuntime: %v", err)
	}
	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewRuntimeRejectsExporterModeMutationToDisabled(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	cfg := enabledConfig(t)
	cfg.TracesExporter = ExporterNone
	cfg.MetricsExporter = ExporterNone
	_, err := newRuntime(t.Context(), cfg, io.Discard, factories{})
	assertSafeFieldError(t, err, "observability Config")
}

func TestNewRuntimeRejectsMutatedValidatedConfig(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"log level", func(cfg *Config) { cfg.LogLevel = slog.LevelDebug }},
		{"trace mode", func(cfg *Config) { cfg.TracesExporter = ExporterMode("hostile-trace-mode") }},
		{"metric mode", func(cfg *Config) { cfg.MetricsExporter = ExporterOTLP }},
		{"service name", func(cfg *Config) { cfg.ServiceName = "hostile-mutated-service" }},
		{"service version", func(cfg *Config) { cfg.ServiceVersion = "hostile-mutated-version" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			test.mutate(&cfg)
			_, err = newRuntime(t.Context(), cfg, io.Discard, factories{
				newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
					t.Fatal("trace factory called for mutated config")
					return nil, nil, nil
				},
				newMetric: func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
					t.Fatal("metric factory called for mutated config")
					return nil, nil, nil
				},
			})
			assertSafeFieldError(t, err, "observability Config", "hostile-trace-mode", "hostile-mutated-service", "hostile-mutated-version")
		})
	}
}

func TestNewRejectsInvalidConfigBeforeInstallingGlobals(t *testing.T) {
	if os.Getenv("MAIDEN_LANE_OTEL_REJECTED_GLOBAL_CHILD") == "1" {
		_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
		cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		cfg.ServiceName = "mutated-service"
		var rejectedOutput bytes.Buffer
		if _, err := New(context.Background(), cfg, &rejectedOutput); err == nil {
			t.Fatal("New accepted mutated Config")
		}

		valid, err := LoadConfig(emptyEnv, rejectRead, "devel")
		if err != nil {
			t.Fatalf("LoadConfig valid: %v", err)
		}
		var acceptedOutput bytes.Buffer
		runtime, err := New(context.Background(), valid, &acceptedOutput)
		if err != nil {
			t.Fatalf("New valid: %v", err)
		}
		otel.Handle(errors.New("hostile-async-value"))
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if rejectedOutput.Len() != 0 {
			t.Fatalf("rejected logger captured global diagnostics: %s", rejectedOutput.String())
		}
		_, _ = os.Stdout.Write(acceptedOutput.Bytes())
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestNewRejectsInvalidConfigBeforeInstallingGlobals$")
	command.Env = append(os.Environ(), "MAIDEN_LANE_OTEL_REJECTED_GLOBAL_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	assertContainsOnlyCode(t, string(output), "otel_async_error", "hostile-async-value", "mutated-service")
}

func TestNewRuntimeIgnoresAmbientSDKPolicy(t *testing.T) {
	for _, name := range []string{
		"OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT",
		"OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT",
		"OTEL_SPAN_EVENT_COUNT_LIMIT",
		"OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT",
		"OTEL_SPAN_LINK_COUNT_LIMIT",
		"OTEL_LINK_ATTRIBUTE_COUNT_LIMIT",
	} {
		t.Setenv(name, "0")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", "base2_exponential_bucket_histogram")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	traceRequests := make(chan *collectorTrace.ExportTraceServiceRequest, 8)
	metricRequests := make(chan *collectorMetric.ExportMetricsServiceRequest, 8)
	collector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/x-protobuf")
		switch request.URL.Path {
		case "/v1/traces":
			message := new(collectorTrace.ExportTraceServiceRequest)
			if err := proto.Unmarshal(body, message); err != nil {
				t.Errorf("decode trace request: %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			traceRequests <- message
		case "/v1/metrics":
			message := new(collectorMetric.ExportMetricsServiceRequest)
			if err := proto.Unmarshal(body, message); err != nil {
				t.Errorf("decode metric request: %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			metricRequests <- message
		default:
			t.Errorf("unexpected collector path %q", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":                "otlp",
		"OTEL_METRICS_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  collector.URL + "/v1/traces",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": collector.URL + "/v1/metrics",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	runtime, err := newRuntime(t.Context(), cfg, io.Discard, defaultFactories())
	if err != nil {
		t.Fatalf("newRuntime: %v", err)
	}

	linkedContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: trace.FlagsSampled,
	})
	ctx, span := runtime.tracerProvider.Tracer("runtime-policy-test").Start(t.Context(), "HTTP GET /v1/test",
		trace.WithLinks(trace.Link{SpanContext: linkedContext, Attributes: []attribute.KeyValue{attribute.String("link.attribute", "retained-link-value")}}))
	span.SetAttributes(
		attribute.String("http.request.method", "retained-method-value"),
		attribute.String("http.route", "retained-route-value"),
		attribute.Int("http.response.status_code", http.StatusNoContent),
	)
	span.AddEvent("retained-event-name", trace.WithAttributes(attribute.String("event.attribute", "retained-event-value")))
	histogram, err := runtime.meterProvider.Meter("runtime-policy-test").Float64Histogram("http.server.request.duration")
	if err != nil {
		t.Fatalf("Float64Histogram: %v", err)
	}
	histogram.Record(ctx, 0.125, metric.WithAttributes(attribute.String("hostile.metric.attribute", "must-not-be-an-exemplar")))
	span.End()

	if err := runtime.traceLifecycle.ForceFlush(t.Context()); err != nil {
		t.Fatalf("trace ForceFlush: %v", err)
	}
	if err := runtime.metricLifecycle.ForceFlush(t.Context()); err != nil {
		t.Fatalf("metric ForceFlush: %v", err)
	}

	traceRequest := <-traceRequests
	spans := traceRequest.GetResourceSpans()[0].GetScopeSpans()[0].GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	gotSpanAttributes := make(map[string]string)
	var gotStatusCode int64
	for _, value := range spans[0].GetAttributes() {
		gotSpanAttributes[value.GetKey()] = value.GetValue().GetStringValue()
		if value.GetKey() == "http.response.status_code" {
			gotStatusCode = value.GetValue().GetIntValue()
		}
	}
	if gotSpanAttributes["http.request.method"] != "retained-method-value" || gotSpanAttributes["http.route"] != "retained-route-value" {
		t.Errorf("span string attributes were dropped or truncated: %#v", spans[0].GetAttributes())
	}
	if gotStatusCode != http.StatusNoContent {
		t.Errorf("span status-code attribute = %d, want %d: %#v", gotStatusCode, http.StatusNoContent, spans[0].GetAttributes())
	}
	if len(spans[0].GetEvents()) != 1 || spans[0].GetEvents()[0].GetName() != "retained-event-name" ||
		spans[0].GetEvents()[0].GetAttributes()[0].GetValue().GetStringValue() != "retained-event-value" {
		t.Errorf("span events were dropped or truncated: %#v", spans[0].GetEvents())
	}
	if len(spans[0].GetLinks()) != 1 || spans[0].GetLinks()[0].GetAttributes()[0].GetValue().GetStringValue() != "retained-link-value" {
		t.Errorf("span links were dropped or truncated: %#v", spans[0].GetLinks())
	}

	metricRequest := <-metricRequests
	metrics := metricRequest.GetResourceMetrics()[0].GetScopeMetrics()[0].GetMetrics()
	if len(metrics) != 1 {
		t.Fatalf("exported metrics = %d, want 1", len(metrics))
	}
	explicit := metrics[0].GetHistogram()
	if explicit == nil || metrics[0].GetExponentialHistogram() != nil {
		t.Fatalf("metric aggregation = %#v, want explicit histogram", metrics[0].GetData())
	}
	if explicit.GetAggregationTemporality() != metricProto.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		t.Fatalf("temporality = %v, want cumulative", explicit.GetAggregationTemporality())
	}
	if len(explicit.GetDataPoints()) != 1 || len(explicit.GetDataPoints()[0].GetExplicitBounds()) == 0 {
		t.Fatalf("histogram points = %#v, want default explicit buckets", explicit.GetDataPoints())
	}
	if attributes := explicit.GetDataPoints()[0].GetAttributes(); len(attributes) != 0 {
		t.Fatalf("metric view retained attributes outside the HTTP allowlist: %#v", attributes)
	}
	if exemplars := explicit.GetDataPoints()[0].GetExemplars(); len(exemplars) != 0 {
		t.Fatalf("exemplars = %#v, want none", exemplars)
	}
	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestExplicitHTTPClientUsesValidatedPolicy(t *testing.T) {
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":           "otlp",
		"OTEL_EXPORTER_OTLP_TIMEOUT":     "1250",
		"OTEL_EXPORTER_OTLP_ENDPOINT":    "https://collector.example",
		"OTEL_EXPORTER_OTLP_COMPRESSION": "none",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	client := explicitHTTPClient(cfg.traces)
	if client.Timeout != 1250*time.Millisecond {
		t.Fatalf("client timeout = %v, want 1.25s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.TLSClientConfig == nil || !transport.DisableKeepAlives {
		t.Fatalf("transport = %#v, want explicit no-proxy cloned TLS policy", client.Transport)
	}
	if transport.TLSClientConfig == cfg.traces.tlsConfig {
		t.Fatal("client reused mutable validated TLS config")
	}
}

func TestNewInstallsSanitizedOTelGlobals(t *testing.T) {
	if os.Getenv("MAIDEN_LANE_OTEL_GLOBAL_CHILD") == "1" {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "%hostile-internal-value")
		_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
		var output bytes.Buffer
		cfg := enabledTraceConfigWithExplicitEndpoint(t, "http://127.0.0.1:4318/v1/traces")
		runtime, err := New(context.Background(), cfg, &output)
		if err == nil {
			_ = runtime.Shutdown(context.Background())
		}
		otel.Handle(errors.New("hostile-async-value"))
		_, _ = os.Stdout.Write(output.Bytes())
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestNewInstallsSanitizedOTelGlobals$")
	command.Env = append(os.Environ(), "MAIDEN_LANE_OTEL_GLOBAL_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	assertContainsOnlyCodes(t, string(output), []string{"otel_internal_error", "otel_async_error"},
		"hostile-internal-value", "hostile-async-value")
	assertNoOtherDiagnosticCodes(t, string(output), "otel_internal_error", "otel_async_error")
}

func TestNewRuntimeRollsBackPartialInitialization(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	constructErr := errors.New("metric construction failed: hostile collector response")
	cleanupErr := errors.New("trace cleanup failed: hostile endpoint and token")
	metricCleanupErr := errors.New("metric cleanup failed: hostile certificate path")
	traceLife := &recordingLifecycle{shutdownErr: cleanupErr}
	metricLife := &recordingLifecycle{shutdownErr: metricCleanupErr}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := newRuntime(ctx, enabledConfig(t), io.Discard, factories{
		newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			return tracenoop.NewTracerProvider(), traceLife, nil
		},
		newMetric: func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
			return nil, metricLife, constructErr
		},
	})
	if !errors.Is(err, constructErr) || !errors.Is(err, cleanupErr) || !errors.Is(err, metricCleanupErr) {
		t.Fatalf("error = %v, want construction and both cleanup causes", err)
	}
	if traceLife.shutdownCalls != 1 || traceLife.sawCanceledContext {
		t.Fatalf("rollback = %#v, want one fresh-context call", traceLife)
	}
	if traceLife.forceFlushCalls != 0 || !traceLife.sawDeadline || traceLife.deadlineRemaining <= 0 || traceLife.deadlineRemaining > 10*time.Second {
		t.Fatalf("rollback context/lifecycle = %#v, want shutdown-only bounded fresh context", traceLife)
	}
	if metricLife.shutdownCalls != 1 || metricLife.forceFlushCalls != 0 || metricLife.sawCanceledContext || !metricLife.sawDeadline {
		t.Fatalf("metric rollback context/lifecycle = %#v, want shutdown-only bounded fresh context", metricLife)
	}
	for _, prohibited := range []string{"hostile collector response", "hostile endpoint", "token", "hostile certificate path"} {
		if strings.Contains(err.Error(), prohibited) {
			t.Fatalf("rollback error leaked %q: %v", prohibited, err)
		}
	}
}

func TestNewRuntimeSanitizesTraceConstructorCause(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	cause := &typedHostileError{text: "hostile header and certificate path"}
	cleanupCause := errors.New("hostile partial trace cleanup response")
	lifecycle := &recordingLifecycle{shutdownErr: cleanupCause}
	_, err := newRuntime(t.Context(), enabledConfig(t), io.Discard, factories{
		newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			return nil, lifecycle, cause
		},
	})
	var got *typedHostileError
	if !errors.Is(err, cause) || !errors.Is(err, cleanupCause) || !errors.As(err, &got) || got != cause {
		t.Fatalf("error = %v, want preserved construction and cleanup causes", err)
	}
	if lifecycle.shutdownCalls != 1 || lifecycle.forceFlushCalls != 0 || !lifecycle.sawDeadline {
		t.Fatalf("partial trace rollback = %#v, want bounded shutdown", lifecycle)
	}
	if strings.Contains(err.Error(), cause.text) || strings.Contains(err.Error(), cleanupCause.Error()) {
		t.Fatalf("constructor error leaked hostile text: %v", err)
	}
}

func TestNewRuntimeRollsBackWhenAmbientResourceAppearsBetweenProviders(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	traceLife := &recordingLifecycle{}
	_, err := newRuntime(t.Context(), enabledConfig(t), io.Discard, factories{
		newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "tenant.secret=hostile-value"); err != nil {
				t.Fatalf("Setenv: %v", err)
			}
			return tracenoop.NewTracerProvider(), traceLife, nil
		},
		newMetric: func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
			t.Fatal("metric factory called after ambient resource appeared")
			return nil, nil, nil
		},
	})
	assertSafeFieldError(t, err, "OTEL_RESOURCE_ATTRIBUTES", "tenant.secret", "hostile-value")
	if traceLife.shutdownCalls != 1 || traceLife.forceFlushCalls != 0 || traceLife.sawCanceledContext || !traceLife.sawDeadline {
		t.Fatalf("rollback = %#v, want one bounded fresh-context shutdown", traceLife)
	}
}

func TestRuntimeShutdownAttemptsAllProvidersOnce(t *testing.T) {
	traceFlushErr := errors.New("trace flush hostile response")
	traceShutdownErr := errors.New("trace shutdown hostile endpoint")
	metricFlushErr := errors.New("metric flush hostile token")
	metricShutdownErr := errors.New("metric shutdown hostile certificate path")
	var operationsMu sync.Mutex
	var operations []string
	traceLife := &recordingLifecycle{name: "trace", operations: &operations, operationsMu: &operationsMu, forceFlushErr: traceFlushErr, shutdownErr: traceShutdownErr}
	metricLife := &recordingLifecycle{name: "metric", operations: &operations, operationsMu: &operationsMu, forceFlushErr: metricFlushErr, shutdownErr: metricShutdownErr}
	runtime := &Runtime{traceLifecycle: traceLife, metricLifecycle: metricLife}

	const callers = 24
	results := make(chan error, callers)
	var start sync.WaitGroup
	start.Add(callers)
	for range callers {
		go func() {
			start.Done()
			start.Wait()
			results <- runtime.Shutdown(t.Context())
		}()
	}
	var first error
	for range callers {
		err := <-results
		if first == nil {
			first = err
		}
		for _, cause := range []error{traceFlushErr, traceShutdownErr, metricFlushErr, metricShutdownErr} {
			if !errors.Is(err, cause) {
				t.Errorf("Shutdown error = %v, missing cause %v", err, cause)
			}
		}
		if err != first {
			t.Errorf("Shutdown error identity changed: first %p, got %p", first, err)
		}
	}
	if traceLife.forceFlushCalls != 1 || traceLife.shutdownCalls != 1 || metricLife.forceFlushCalls != 1 || metricLife.shutdownCalls != 1 {
		t.Fatalf("trace lifecycle = %#v, metric lifecycle = %#v", traceLife, metricLife)
	}
	if got, want := strings.Join(operations, ","), "trace.flush,trace.shutdown,metric.flush,metric.shutdown"; got != want {
		t.Fatalf("shutdown order = %q, want %q", got, want)
	}
	if err := runtime.Shutdown(t.Context()); err != first {
		t.Fatalf("repeated Shutdown error = %p, want original %p", err, first)
	}
	if traceLife.forceFlushCalls != 1 || traceLife.shutdownCalls != 1 || metricLife.forceFlushCalls != 1 || metricLife.shutdownCalls != 1 {
		t.Fatalf("repeated Shutdown invoked lifecycle again: trace %#v, metric %#v", traceLife, metricLife)
	}
	for _, prohibited := range []string{"hostile response", "hostile endpoint", "hostile token", "hostile certificate path"} {
		if strings.Contains(first.Error(), prohibited) {
			t.Fatalf("Shutdown error leaked %q: %v", prohibited, first)
		}
	}
}

func TestRuntimeShutdownUsesCallerContextAndDisabledIsHarmless(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	life := &recordingLifecycle{}
	runtime := &Runtime{traceLifecycle: life}
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !life.sawCanceledContext {
		t.Fatal("provider did not receive caller shutdown context")
	}
	if err := (&Runtime{}).Shutdown(ctx); err != nil {
		t.Fatalf("disabled Shutdown: %v", err)
	}
}

func TestRuntimeExporterFailureIsOperational(t *testing.T) {
	if os.Getenv("MAIDEN_LANE_OTEL_EXPORT_FAILURE_CHILD") == "1" {
		_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
		var output bytes.Buffer
		logger := NewLogger(&output, 0)
		installOTelGlobals(logger)
		exporter := &failingSpanExporter{err: errors.New("hostile exporter body and endpoint")}
		provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		cfg := enabledTraceConfigWithExplicitEndpoint(t, "http://127.0.0.1:4318/v1/traces")
		runtime, err := newRuntime(context.Background(), cfg, &output, factories{
			newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
				return provider, provider, nil
			},
		})
		if err != nil {
			t.Fatalf("newRuntime: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		_, span := runtime.tracerProvider.Tracer("export-failure-test").Start(ctx, "first")
		span.End()
		if ctx.Err() != nil {
			t.Fatalf("application context canceled: %v", ctx.Err())
		}
		_, second := runtime.tracerProvider.Tracer("export-failure-test").Start(ctx, "second")
		second.End()
		cancel()
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		_, _ = os.Stdout.Write(output.Bytes())
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRuntimeExporterFailureIsOperational$")
	command.Env = append(os.Environ(), "MAIDEN_LANE_OTEL_EXPORT_FAILURE_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	assertContainsOnlyCode(t, string(output), "otel_async_error", "hostile exporter body", "endpoint")
	assertNoOtherDiagnosticCodes(t, string(output), "otel_async_error")
}

func assertNoOtherDiagnosticCodes(t *testing.T, output string, allowed ...string) {
	t.Helper()
	allowedSet := make(map[string]bool, len(allowed))
	for _, code := range allowed {
		allowedSet[code] = true
	}
	for _, line := range strings.Split(output, "\n") {
		_, remainder, ok := strings.Cut(line, `"code":"`)
		if !ok {
			continue
		}
		code, _, ok := strings.Cut(remainder, `"`)
		if !ok || !allowedSet[code] {
			t.Errorf("output contained unexpected diagnostic code %q: %s", code, output)
		}
	}
}

type recordingLifecycle struct {
	mu                 sync.Mutex
	name               string
	operations         *[]string
	operationsMu       *sync.Mutex
	forceFlushErr      error
	shutdownErr        error
	forceFlushCalls    int
	shutdownCalls      int
	sawCanceledContext bool
	sawDeadline        bool
	deadlineRemaining  time.Duration
}

func (l *recordingLifecycle) ForceFlush(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.forceFlushCalls++
	l.sawCanceledContext = l.sawCanceledContext || ctx.Err() != nil
	l.recordContext(ctx)
	l.recordOperation("flush")
	return l.forceFlushErr
}

func (l *recordingLifecycle) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.shutdownCalls++
	l.sawCanceledContext = l.sawCanceledContext || ctx.Err() != nil
	l.recordContext(ctx)
	l.recordOperation("shutdown")
	return l.shutdownErr
}

func (l *recordingLifecycle) recordContext(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		l.sawDeadline = true
		l.deadlineRemaining = time.Until(deadline)
	}
}

func (l *recordingLifecycle) recordOperation(operation string) {
	if l.operations == nil {
		return
	}
	l.operationsMu.Lock()
	defer l.operationsMu.Unlock()
	*l.operations = append(*l.operations, l.name+"."+operation)
}

type failingSpanExporter struct {
	err error
}

func (e *failingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return e.err
}

func (*failingSpanExporter) Shutdown(context.Context) error { return nil }

type typedHostileError struct{ text string }

func (e *typedHostileError) Error() string { return e.text }

func enabledConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":  "otlp",
		"OTEL_METRICS_EXPORTER": "otlp",
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func enabledTraceConfigWithExplicitEndpoint(t *testing.T, endpoint string) Config {
	t.Helper()
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": endpoint,
	}), rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func equalAttributes(got, want []attribute.KeyValue) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
