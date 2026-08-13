# Observability Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish privacy-safe structured logging, OpenTelemetry trace and metric plumbing, explicit HTTP span status, and bounded lifecycle flushing without introducing transformation-semantic telemetry.

**Architecture:** `cmd/maiden-lane` remains the composition root. A new `internal/observability` package owns validated operational configuration, sanitized OTel diagnostics, explicit trace and metric providers, and a Maiden Lane-owned route wrapper whose contract is narrower than standard OTel HTTP middleware. The chi transport registers instrumentation only around matched non-health handlers; semantic packages never import observability.

**Tech Stack:** Go 1.26.5, standard-library `log/slog`, OpenTelemetry Go v1.37.0 with semantic conventions v1.34.0, OTLP/HTTP protobuf exporters v1.37.0, `github.com/go-logr/logr` v1.4.4, `github.com/felixge/httpsnoop` v1.1.0, chi v5.3.1.

## Global Constraints

- Ratified [Inviolates](../../../Inviolates.md), the [High-Level Design](../specs/2026-08-11-maiden-lane-high-level-design.md), [AGENTS.md](../../../AGENTS.md), and the approved [observability design](../specs/2026-08-12-observability-foundation-design.md) remain authoritative in that order.
- Operational telemetry configuration must not participate in semantic input, identity, execution, readiness, comparison, promotion, or publication.
- Use `slog` JSON records on standard output. Do not introduce Zap, OTel logs, an OTel log bridge, or an application logging abstraction.
- Traces and metrics are independently disabled by default; disabled mode performs no exporter network work.
- Support only OTLP over HTTP/protobuf. Malformed selected configuration fails startup without echoing its value.
- Do not consume arbitrary `OTEL_RESOURCE_ATTRIBUTES`, sampler policy, batch-processor policy, metric reader timing, exemplars, or cardinality limits from the environment in this slice.
- Do not use `otelhttp.NewHandler` or `otelhttp.NewMiddleware`: v0.70.0 records raw request-derived attributes and uses span-status semantics that violate the approved design.
- The HTTP wrapper may export only a registered route template, normalized method, terminal status, bounded protocol attributes, and the closed failure-only `error.type` values.
- Never export or log raw paths, query strings, headers, bodies, peer/client addresses, user agents, arbitrary methods, customer data, semantic journals, identifiers, OTLP headers, certificate paths, or supplied OTel diagnostic text.
- Accept W3C `tracecontext`; remove and never propagate W3C baggage.
- Every completed Maiden Lane-owned span ends with explicit `codes.Ok` or `codes.Error`; none may remain `codes.Unset`.
- Metric exemplars are explicitly disabled. Metric SDK views enforce the exact three-attribute allowlist in `METRICS.md`.
- Initialization rollback and terminal shutdown use fresh bounded contexts, attempt every applicable cleanup, preserve causes with `errors.Join`, and are safe under concurrent repeated shutdown.
- Preserve health/readiness and OpenAPI contracts. Do not add a fake public route to generate telemetry.
- `ERRORS.md`, HLD, Inviolates, glossary, progressive-completeness amendment, and OpenAPI do not change.
- Use RED -> GREEN -> REFACTOR for behavioral work. Run targeted tests before broad verification and never weaken assertions to clear failures.

---

## Planned Repository Shape

```text
internal/observability/config.go       operational environment validation
internal/observability/config_test.go  closed config and hostile-input tests
internal/observability/logging.go      slog construction and sanitized OTel globals
internal/observability/logging_test.go sanitizer unit and subprocess tests
internal/observability/runtime.go      provider construction, rollback, shutdown
internal/observability/runtime_test.go provider lifecycle and disabled-mode tests
internal/observability/http.go         safe registered-route instrumentation
internal/observability/http_test.go    span, metric, propagation, privacy tests
internal/httpapi/router.go              unchanged health-only transport
internal/httpapi/router_test.go         unchanged health contract verification
cmd/maiden-lane/main.go                process composition and lifecycle ordering
cmd/maiden-lane/main_test.go           safe logging and ordering tests
go.mod / go.sum                         pinned runtime dependencies
README.md                               operational configuration
METRICS.md                              exact metric registry
docs/implementation/implementation-guide.md current runtime description
```

The cross-task interfaces are fixed for this slice:

```go
package observability

type LookupEnv func(string) (string, bool)
type ReadFile func(string) ([]byte, error)
type ExporterMode string

const (
	ExporterNone ExporterMode = "none"
	ExporterOTLP ExporterMode = "otlp"
)

type Config struct {
	LogLevel        slog.Level
	TracesExporter  ExporterMode
	MetricsExporter ExporterMode
	ServiceName     string
	ServiceVersion  string
	validated       bool
	// Validated signal-specific OTLP settings remain private.
}

func LoadConfig(LookupEnv, ReadFile, string) (Config, error)
func NewLogger(io.Writer, slog.Level) *slog.Logger

type Runtime struct {
	Logger *slog.Logger
	// Providers, propagator, instruments, and shutdown state remain private.
}

func New(context.Context, Config, io.Writer) (*Runtime, error)
func (r *Runtime) InstrumentHTTPRoute(string, string, http.Handler) http.Handler
func (r *Runtime) Shutdown(context.Context) error
```

`Config` is intentionally opaque outside this internal package: exported fields are readable for bounded operational logging, but only `LoadConfig` can set its private validation marker and private OTLP settings. Callers cannot authorize a hand-built config for `Runtime.New`.

`internal/httpapi.NewRouter()` remains unchanged. The current API has no non-health route, so adding an unused instrumentation seam would be speculative. A private chi fixture proves route wrapping; the first real non-health route will receive the narrow wrapper at registration.

### Task 1: Closed Operational Configuration

**Files:**
- Create: `internal/observability/config.go`
- Create: `internal/observability/config_test.go`

**Interfaces:**
- Consumes: standard-library `log/slog`, `crypto/tls`, `crypto/x509`, `net/url`, and `os.LookupEnv`/`os.ReadFile`-compatible functions.
- Produces: `ExporterMode`, `Config`, `LookupEnv`, `ReadFile`, and `LoadConfig` for provider construction and process composition.

- [ ] **Step 1: Write failing tests for defaults and closed values**

Create package-internal tests so private resolved OTLP settings remain testable without widening the API:

```go
func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
	if err != nil { t.Fatalf("LoadConfig: %v", err) }
	if cfg.LogLevel != slog.LevelInfo || cfg.TracesExporter != ExporterNone ||
		cfg.MetricsExporter != ExporterNone || cfg.ServiceName != "maiden-lane" ||
		cfg.ServiceVersion != "" {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func TestLoadConfigRejectsClosedValueViolations(t *testing.T) {
	tests := []struct { name string; env map[string]string; field string }{
		{"log level", map[string]string{"LOG_LEVEL": "verbose"}, "LOG_LEVEL"},
		{"trace mode", map[string]string{"OTEL_TRACES_EXPORTER": "console"}, "OTEL_TRACES_EXPORTER"},
		{"metric mode", map[string]string{"OTEL_METRICS_EXPORTER": "prometheus"}, "OTEL_METRICS_EXPORTER"},
		{"empty service", map[string]string{"OTEL_SERVICE_NAME": ""}, "OTEL_SERVICE_NAME"},
		{"control service", map[string]string{"OTEL_SERVICE_NAME": "safe\nunsafe"}, "OTEL_SERVICE_NAME"},
		{"resource injection", map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "tenant.secret=value"}, "OTEL_RESOURCE_ATTRIBUTES"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadConfig(mapLookup(test.env), rejectRead, "v1.2.3")
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("error = %v, want field %q", err, test.field)
			}
			for _, secret := range test.env {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked configured value %q: %v", secret, err)
				}
			}
		})
	}
}
```

Add positive cases for all four log levels, independent exporter enablement, a 128-byte service name, and a meaningful build version. Implement `mapLookup`, `emptyEnv`, and `rejectRead` with exact production function signatures.

- [ ] **Step 2: Run the top-level tests and observe RED**

Run: `go test ./internal/observability -run 'TestLoadConfig(Defaults|RejectsClosedValueViolations)$' -count=1`

Expected: compilation fails because `LoadConfig`, `Config`, and exporter constants do not exist.

- [ ] **Step 3: Implement top-level parsing and safe validation errors**

Add the planned public types and private settings:

```go
type otlpHTTPConfig struct {
	endpoint    *url.URL
	headers     map[string]string
	timeout     time.Duration
	compression string
	tlsConfig   *tls.Config
}

func invalidField(name, allowed string) error {
	return fmt.Errorf("invalid %s: expected %s", name, allowed)
}
```

Parse `LOG_LEVEL` and exporter selectors from closed maps. Reject present-but-empty values. Validate service name as 1-128 UTF-8 bytes without Unicode control characters. Reject non-empty `OTEL_RESOURCE_ATTRIBUTES`. Include `serviceVersion` only when non-empty and not `devel`. Resolve private signal settings only when that signal selects `otlp`. Set private `validated` only after all checks succeed; `Runtime.New` rejects manually assembled/unvalidated `Config` values. Never put the offending value in an error.

- [ ] **Step 4: Write failing tests for enabled OTLP/HTTP resolution**

Cover global defaults, signal precedence, and every supported setting. Pure parser tests use `mapLookup`. Any provider test that sets supported `OTEL_*` variables uses `t.Setenv` and must not call `t.Parallel`, because exporter constructors read the real process environment before explicit validated options override it:

```go
func TestLoadConfigResolvesEnabledOTLPHTTP(t *testing.T) {
	env := map[string]string{
		"OTEL_TRACES_EXPORTER": "otlp", "OTEL_METRICS_EXPORTER": "otlp",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example/base/",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://trace.example/custom",
		"OTEL_EXPORTER_OTLP_HEADERS": "authorization=Bearer%20redacted,x-safe=value",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS": "x-safe=metric",
		"OTEL_EXPORTER_OTLP_TIMEOUT": "12000", "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT": "7000",
		"OTEL_EXPORTER_OTLP_COMPRESSION": "gzip", "OTEL_EXPORTER_OTLP_METRICS_COMPRESSION": "none",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
	}
	cfg, err := LoadConfig(mapLookup(env), rejectRead, "v1.2.3")
	if err != nil { t.Fatalf("LoadConfig: %v", err) }
	if got := cfg.traces.endpoint.String(); got != "https://trace.example/custom" { t.Fatalf("trace endpoint = %q", got) }
	if got := cfg.metrics.endpoint.String(); got != "https://collector.example/base/v1/metrics" { t.Fatalf("metric endpoint = %q", got) }
	if cfg.traces.timeout != 7*time.Second || cfg.metrics.timeout != 12*time.Second { t.Fatalf("timeouts = %v, %v", cfg.traces.timeout, cfg.metrics.timeout) }
}
```

Add positive cases for `http` and `https` endpoint schemes, `*_INSECURE` values consistent with those schemes, CA loading, paired client certificate/key loading, percent-decoded header values, and default `https://localhost:4318/v1/{traces,metrics}` endpoints. Add rejection cases for unknown/grpc protocol, relative/credential-bearing/query-bearing/fragment endpoints, invalid or scheme-conflicting insecure values, invalid timeout/compression, malformed/duplicate/control-character headers, invalid/unreadable CA, unpaired client certificate/key, and invalid/unreadable client keypairs. Every failure assertion requires the field name and rejects the hostile value, header secret, and certificate path from the error text.

- [ ] **Step 5: Run OTLP tests and observe RED**

Run: `go test ./internal/observability -run 'TestLoadConfig(ResolvesEnabledOTLPHTTP|RejectsMalformedOTLPHTTP|LoadsTLSMaterial)$' -count=1`

Expected: tests fail because enabled signal resolution is absent.

- [ ] **Step 6: Implement deterministic OTLP/HTTP resolution**

Use these helpers:

```go
func resolveOTLPHTTP(LookupEnv, ReadFile, string, string) (otlpHTTPConfig, error)
func firstSignalEnv(LookupEnv, string, string) (value, field string, present bool)
func parseEndpoint(field, raw, signalPath string, global bool) (*url.URL, error)
func parseHeaders(field, raw string) (map[string]string, error)
func buildTLSConfig(LookupEnv, ReadFile, string) (*tls.Config, error)
```

Signal-specific variables override global ones. Global endpoints append the signal path; signal endpoints are exact. Accept only absolute `http`/`https` URLs without userinfo, query, or fragment, protocol `http/protobuf`, compression `none|gzip`, and positive integer millisecond timeouts. Support the standard global and signal-specific `*_INSECURE` variables as booleans, but require consistency with the endpoint scheme (`http`/true, `https`/false) rather than allowing two conflicting authorities. Canonicalize names with `http.CanonicalHeaderKey`, percent-decode values only, reject CR/LF, and reject case-insensitive duplicates. Copy maps and TLS state.

Support global and signal-specific `OTEL_EXPORTER_OTLP_{CERTIFICATE,CLIENT_CERTIFICATE,CLIENT_KEY}`. Read through `ReadFile`, add CA PEM to a new `x509.CertPool`, require the client pair together, and report only field name plus safe classification. Without a configured CA, leave `RootCAs` nil so Go uses system roots. Do not interpret sampler, BSP, metric reader, exemplar, or cardinality environment variables.

- [ ] **Step 7: Run, format, and commit configuration**

```bash
gofmt -w internal/observability/config.go internal/observability/config_test.go
go test ./internal/observability -count=1
go test -race ./internal/observability -count=1
git diff --check
git add internal/observability/config.go internal/observability/config_test.go
git commit -m "feat: validate observability configuration"
```

Expected: package and race tests pass; the commit contains only configuration files.

### Task 2: Sanitized Structured Logging and OTel Diagnostics

**Files:**
- Create: `internal/observability/logging.go`
- Create: `internal/observability/logging_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `Config.LogLevel` and process-standard output.
- Produces: `NewLogger(io.Writer, slog.Level) *slog.Logger` and a private `installOTelGlobals(*slog.Logger)` used by `Runtime.New` before exporter construction.

- [ ] **Step 1: Pin logging-facing dependencies**

Run: `go get github.com/go-logr/logr@v1.4.4 go.opentelemetry.io/otel@v1.37.0`

Expected: direct `logr` and OTel requirements appear; no contrib HTTP instrumentation is added.

- [ ] **Step 2: Write failing JSON logger and sanitizer tests**

```go
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
```

Verify `WithValues` and `WithName` still discard supplied values and names.

- [ ] **Step 3: Run tests and observe RED**

Run: `go test ./internal/observability -run 'Test(NewLogger|OTel)' -count=1`

Expected: compilation fails because logging functions and sanitizer types do not exist.

- [ ] **Step 4: Implement stable-code-only diagnostics**

```go
func NewLogger(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}

func installOTelGlobals(logger *slog.Logger) {
	installGlobalsOnce.Do(func() {
		otel.SetErrorHandler(newOTelErrorHandler(logger))
		otel.SetLogger(logr.New(newOTelLogSink(logger)))
	})
}

type otelErrorHandler struct{ logger *slog.Logger }
func (h otelErrorHandler) Handle(error) {
	h.logger.Error("OpenTelemetry asynchronous failure", "code", "otel_async_error")
}

type otelLogSink struct{ logger *slog.Logger }
func (s *otelLogSink) Init(logr.RuntimeInfo) {}
func (s *otelLogSink) Enabled(int) bool { return true }
func (s *otelLogSink) Info(_ int, _ string, _ ...any) {
	s.logger.Debug("OpenTelemetry internal message", "code", "otel_internal_message")
}
func (s *otelLogSink) Error(_ error, _ string, _ ...any) {
	s.logger.Error("OpenTelemetry internal failure", "code", "otel_internal_error")
}
func (s *otelLogSink) WithValues(...any) logr.LogSink { return &otelLogSink{logger: s.logger} }
func (s *otelLogSink) WithName(string) logr.LogSink { return &otelLogSink{logger: s.logger} }
```

Declare `var installGlobalsOnce sync.Once`. Keep constructors and installation private so unit tests exercise sanitizer values without mutating globals. Never log received error, message, name, key, or value. Task 3 verifies actual global installation in a subprocess after exporter dependencies exist. Tests that invoke public `New` must not run in parallel; ordinary lifecycle tests use private `newRuntime` so process globals remain isolated.

- [ ] **Step 5: Run, format, tidy, and commit logging**

```bash
gofmt -w internal/observability/logging.go internal/observability/logging_test.go
go mod tidy
go test ./internal/observability -run 'Test(NewLogger|OTel)' -count=1
go test -race ./internal/observability -count=1
git diff --check
git add internal/observability/logging.go internal/observability/logging_test.go go.mod go.sum
git commit -m "feat: add safe observability logging"
```

Expected: only stable codes remain in diagnostics, and `go.mod` has no Zap or OTel HTTP middleware.

### Task 3: Explicit Trace and Metric Provider Lifecycle

**Files:**
- Create: `internal/observability/runtime.go`
- Create: `internal/observability/runtime_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `Config`, private `otlpHTTPConfig`, and `NewLogger`.
- Produces: `New(context.Context, Config, io.Writer) (*Runtime, error)`, `(*Runtime).Shutdown(context.Context) error`, and private provider access for HTTP instrumentation.

- [ ] **Step 1: Pin SDK and exporter modules**

```bash
go get \
  go.opentelemetry.io/otel/sdk@v1.37.0 \
  go.opentelemetry.io/otel/sdk/metric@v1.37.0 \
  go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.37.0 \
  go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp@v1.37.0
```

Expected: every direct OTel module resolves to v1.37.0.

- [ ] **Step 2: Write failing disabled-mode and resource tests**

Use private factory seams:

```go
type providerLifecycle interface {
	ForceFlush(context.Context) error
	Shutdown(context.Context) error
}

type factories struct {
	newTrace  func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error)
	newMetric func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error)
}

func TestNewRuntimeDisabledDoesNotCreateExporters(t *testing.T) {
	f := factories{
		newTrace: func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
			t.Fatal("trace factory called in disabled mode"); return nil, nil, nil
		},
		newMetric: func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
			t.Fatal("metric factory called in disabled mode"); return nil, nil, nil
		},
	}
	cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
	if err != nil { t.Fatalf("LoadConfig: %v", err) }
	runtime, err := newRuntime(t.Context(), cfg, io.Discard, f)
	if err != nil { t.Fatalf("newRuntime: %v", err) }
	if err := runtime.Shutdown(t.Context()); err != nil { t.Fatalf("Shutdown: %v", err) }
}
```

Inspect test-provider resource output and require exactly `service.name` plus optional meaningful `service.version`—never hostile `OTEL_RESOURCE_ATTRIBUTES`.

- [ ] **Step 3: Run tests and observe RED**

Run: `go test ./internal/observability -run 'TestNewRuntime(Disabled|Resource)' -count=1`

Expected: compilation fails because `Runtime` and `newRuntime` do not exist.

- [ ] **Step 4: Implement explicit providers without hidden environment policy**

First reject `cfg.validated == false`. Re-read `resource.Environment()` immediately before provider construction and fail with safe field name `OTEL_RESOURCE_ATTRIBUTES` if `Len() != 0`; this closes the interval between config parsing and SDK `WithResource`, which merges ambient resource attributes internally. Build `resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(cfg.ServiceName))`, adding `semconv.ServiceVersion` only when present. Store `propagation.TraceContext{}`.

Construct traces with fixed policy:

```go
exporter, err := otlptracehttp.New(ctx, traceExporterOptions(cfg.traces)...)
processor := sdktrace.NewBatchSpanProcessor(exporter,
	sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(512),
	sdktrace.WithBatchTimeout(5*time.Second), sdktrace.WithExportTimeout(30*time.Second),
)
provider := sdktrace.NewTracerProvider(
	sdktrace.WithResource(res),
	sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
		AttributeValueLengthLimit: sdktrace.DefaultAttributeValueLengthLimit,
		AttributeCountLimit: sdktrace.DefaultAttributeCountLimit,
		EventCountLimit: sdktrace.DefaultEventCountLimit,
		LinkCountLimit: sdktrace.DefaultLinkCountLimit,
		AttributePerEventCountLimit: sdktrace.DefaultAttributePerEventCountLimit,
		AttributePerLinkCountLimit: sdktrace.DefaultAttributePerLinkCountLimit,
	}),
	sdktrace.WithSpanProcessor(processor),
	sdktrace.WithoutPanicRecording(),
)
```

Construct metrics with fixed policy:

```go
reader := sdkmetric.NewPeriodicReader(exporter,
	sdkmetric.WithInterval(60*time.Second), sdkmetric.WithTimeout(30*time.Second),
)
provider := sdkmetric.NewMeterProvider(
	sdkmetric.WithResource(res), sdkmetric.WithReader(reader),
	sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
)
```

Translate every private setting into an exporter option even when it holds the validated default: explicit endpoint URL, a copied header map including empty, timeout, `NoCompression` or `GzipCompression`, a cloned default/custom TLS config, and `WithInsecure` exactly when the endpoint scheme is `http`. For metrics also pass `otlpmetrichttp.WithTemporalitySelector(sdkmetric.CumulativeTemporalitySelector)` and `otlpmetrichttp.WithAggregationSelector(sdkmetric.DefaultAggregationSelector)` so ambient temporality/histogram variables cannot alter output. In a non-parallel `t.Setenv` test, set each of the six `OTEL_SPAN_*` limit variables to `0`, set metric temporality to `delta`, and set histogram aggregation to `base2_exponential_bucket_histogram`; require the HTTP span still retains every allowlisted attribute and the metric exporter uses cumulative/default explicit-bucket policy. Keep concrete SDK providers private; retain API-level providers and propagator on `Runtime`.

- [ ] **Step 5: Verify global diagnostic installation in a subprocess**

Make `New` construct the logger, immediately call private `installOTelGlobals`, and only then call exporter constructors. Prove the ordering without contaminating the parent test process:

```go
func TestNewInstallsSanitizedOTelGlobals(t *testing.T) {
	if os.Getenv("MAIDEN_LANE_OTEL_GLOBAL_CHILD") == "1" {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "%hostile-internal-value")
		var output bytes.Buffer
		cfg := enabledTraceConfigWithExplicitEndpoint("http://127.0.0.1:4318/v1/traces")
		runtime, err := New(context.Background(), cfg, &output)
		if err == nil { _ = runtime.Shutdown(context.Background()) }
		otel.Handle(errors.New("hostile-async-value"))
		_, _ = os.Stdout.Write(output.Bytes())
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestNewInstallsSanitizedOTelGlobals$")
	command.Env = append(os.Environ(), "MAIDEN_LANE_OTEL_GLOBAL_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil { t.Fatalf("child: %v\n%s", err, output) }
	assertContainsOnlyCodes(t, string(output), []string{"otel_internal_error", "otel_async_error"},
		"hostile-internal-value", "hostile-async-value")
}
```

The malformed ambient endpoint deliberately exercises `otlptracehttp`'s v1.37.0 internal `global.Error` path; the already-validated explicit endpoint still owns exporter behavior. OTel exposes no safe getter/restorer for its internal logger, so this test must remain isolated.

- [ ] **Step 6: Write failing rollback and idempotent-shutdown tests**

```go
func TestNewRuntimeRollsBackPartialInitialization(t *testing.T) {
	constructErr := errors.New("metric construction failed")
	cleanupErr := errors.New("trace cleanup failed")
	traceLife := &recordingLifecycle{shutdownErr: cleanupErr}
	_, err := newRuntime(canceledContext(t), enabledConfig(), io.Discard,
		configuredFactories(traceLife, constructErr))
	if !errors.Is(err, constructErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("error = %v, want both causes", err)
	}
	if traceLife.shutdownCalls != 1 || traceLife.sawCanceledContext {
		t.Fatalf("rollback = %#v, want one fresh-context call", traceLife)
	}
}

func TestRuntimeShutdownAttemptsAllProvidersOnce(t *testing.T) {
	// Call Shutdown concurrently. Every caller receives both sentinel causes;
	// each provider receives exactly one ForceFlush and one Shutdown call.
}
```

Also require ForceFlush failure does not skip Shutdown, trace failure does not skip metrics, disabled mode is harmless, and repeated calls return the same terminal error.

- [ ] **Step 7: Run lifecycle tests and observe RED**

Run: `go test ./internal/observability -run 'Test(NewRuntimeRollsBack|RuntimeShutdown)' -count=1`

Expected: tests fail until transactional ownership exists.

- [ ] **Step 8: Implement transactional ownership and once-only shutdown**

```go
type Runtime struct {
	Logger *slog.Logger
	tracerProvider trace.TracerProvider
	meterProvider metric.MeterProvider
	propagator propagation.TextMapPropagator
	traceLifecycle, metricLifecycle providerLifecycle
	shutdownOnce sync.Once
	shutdownErr error
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		var errs []error
		// Attempt trace ForceFlush, trace Shutdown, metric ForceFlush, and
		// metric Shutdown without early returns, wrapping each cause safely
		// before errors.Join.
		r.shutdownErr = errors.Join(errs...)
	})
	return r.shutdownErr
}
```

Use an unexported causal wrapper for every constructor/flush/shutdown cause:

```go
type safeCause struct { message string; cause error }
func (e safeCause) Error() string { return e.message }
func (e safeCause) Unwrap() error { return e.cause }
```

This keeps `errors.Is`/`errors.As` while preventing `err.Error()` from exposing collector response bodies, endpoint/header values, or certificate paths. If construction fails after owning a provider, use `context.WithTimeout(context.Background(), 10*time.Second)`, attempt every owned shutdown, and join only safe wrappers. Test injected hostile-text causes for both preservation and string redaction. Do not reuse the construction context or export a typed error.

Add `TestRuntimeExporterFailureIsOperational` in a subprocess: a fake span exporter returns a hostile sentinel from export; the installed safe error handler reports only `otel_async_error`; runtime remains usable until explicit shutdown; no application cancellation or exit path is invoked. Isolation is required because `otel.Handle` is process-global and private `newRuntime` deliberately bypasses global installation.

- [ ] **Step 9: Run, format, tidy, and commit runtime**

```bash
gofmt -w internal/observability/runtime.go internal/observability/runtime_test.go
go mod tidy
go test ./internal/observability -count=1
go test -race ./internal/observability -count=1
git diff --check
git add internal/observability/runtime.go internal/observability/runtime_test.go go.mod go.sum
git commit -m "feat: add OpenTelemetry provider lifecycle"
```

Expected: disabled/resource/rollback/concurrency tests pass; direct OTel modules remain v1.37.0.

### Task 4: Privacy-Safe Registered-Route HTTP Telemetry

**Files:**
- Create: `internal/observability/http.go`
- Create: `internal/observability/http_test.go`
- Modify: `internal/observability/runtime.go`
- Modify: `internal/observability/runtime_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `Runtime`'s explicit tracer provider, meter provider, and trace-context propagator.
- Produces: `(*Runtime).InstrumentHTTPRoute(string, string, http.Handler) http.Handler` for the first real matched non-health route.

- [ ] **Step 1: Pin the optional-interface-preserving recorder**

Run: `go get github.com/felixge/httpsnoop@v1.1.0`

Expected: `httpsnoop` is direct; `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` is absent.

- [ ] **Step 2: Write failing span and propagation tests with in-memory providers**

Create a private chi fixture—never a public endpoint:

```go
const fixturePattern = "/fixtures/{fixtureID}"

func newHTTPFixture(t *testing.T, handler http.Handler) (*Runtime, http.Handler, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	// Build an SDK tracer provider with tracetest.NewSpanRecorder and a meter
	// provider with sdkmetric.NewManualReader, exact views, exemplars off.
	router := chi.NewRouter()
	router.Method(http.MethodPost, fixturePattern,
		runtime.InstrumentHTTPRoute(http.MethodPost, fixturePattern, handler))
	return runtime, router, spanRecorder, metricReader
}
```

Send one request containing hostile path/query/header/body/host/remote-address/user-agent/baggage values and a valid parent `traceparent`. Require:

```go
if got := span.Name(); got != "POST "+fixturePattern { t.Fatalf("span name = %q", got) }
if span.Parent().SpanID() != remoteParent.SpanID() { t.Fatal("remote parent not preserved") }
if span.Status().Code != codes.Ok { t.Fatalf("status = %v, want OK", span.Status()) }
assertExactSpanAttributes(t, span.Attributes(), map[string]any{
	"http.request.method": "POST",
	"http.route": fixturePattern,
	"http.response.status_code": int64(201),
	"network.protocol.name": "http",
	"network.protocol.version": "1.1",
})
assertAbsentFromSpan(t, span, hostileValues...)
```

Inspect context inside the handler and require valid trace context with no baggage members.

Add `TestDisabledRuntimeRouteIsHarmless`: `LoadConfig` defaults construct explicit `trace/noop.NewTracerProvider()` and `metric/noop.NewMeterProvider()` values; the wrapper serves the handler, propagates trace context, emits nothing, and performs no network work. Provider interface fields must never be nil.

- [ ] **Step 3: Run span tests and observe RED**

Run: `go test ./internal/observability -run 'TestInstrumentHTTPRoute(SpanContract|TraceParentWithoutBaggage)$' -count=1`

Expected: compilation fails because route instrumentation is absent.

- [ ] **Step 4: Implement the closed wrapper and explicit status policy**

```go
func (r *Runtime) InstrumentHTTPRoute(method, pattern string, next http.Handler) http.Handler {
	trustedMethod := normalizeMethod(method)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		start := time.Now()
		ctx := baggage.ContextWithoutBaggage(request.Context())
		ctx = r.propagator.Extract(ctx, propagation.HeaderCarrier(request.Header))
		ctx = baggage.ContextWithoutBaggage(ctx)
		ctx, span := r.tracerProvider.Tracer(instrumentationName).Start(
			ctx, trustedMethod+" "+pattern,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(baseHTTPAttributes(trustedMethod, pattern, request.Proto)...),
		)
		// A single defer recovers, computes time.Since(start), classifies the
		// terminal result, records attributes/metrics, ends the span, and then
		// re-panics only http.ErrAbortHandler for an original handler panic.
		next.ServeHTTP(w, request.WithContext(ctx))
	})
}
```

Normalize to `GET|HEAD|POST|PUT|DELETE|CONNECT|OPTIONS|TRACE|PATCH`, otherwise `OTHER`. Parse only known bounded `request.Proto` forms. Never inspect `request.URL`, `Host`, `RemoteAddr`, arbitrary headers other than trace context, or user-agent for telemetry.

Classify with exact precedence: `handler_panic`, `request_canceled`, `invalid_http_status`, then 100-399 OK, 400-499 `http.client_error`, and 500-599 `http.server_error`. Attach `error.type` only for failures; call `span.SetStatus(codes.Ok, "")` for success and `span.SetStatus(codes.Error, errorType)` for failure so the only status descriptions are closed classifications. Never call `span.RecordError`.

Do not rely on `httpsnoop.Metrics.Duration` during panic unwinding; the wrapper-owned `start` and finalizing defer are authoritative. Recover a handler panic, classify only as `handler_panic`, omit a default status when no header was committed, finish measurements, end the span, then `panic(http.ErrAbortHandler)` so net/http suppresses raw panic/stack output.

- [ ] **Step 5: Write failing metric, status, panic, and privacy tests**

Collect `metricdata.ResourceMetrics` and require exactly:

```go
var wantMetrics = map[string]string{
	"http.server.request.duration": "s",
	"http.server.request.body.size": "By",
	"http.server.response.body.size": "By",
}
var wantMetricKeys = []string{
	"http.request.method", "http.route", "http.response.status_code",
}
```

Require one point per instrument, exact attributes, observed body sizes, non-negative duration, and no exemplars even from a sampled hostile trace. Search every resource, scope, span, event, link, metric point, and exemplar representation for hostile values.

Test the pure classifier directly for 100, 204, 302, 400, 499, 500, 599, and out-of-range status 600. Use wrapper integration tests for 103 followed by final 204, ordinary final statuses, canceled context, a permissive test writer accepting 600, and panic. Every ended span must be `codes.Ok` or `codes.Error`, never `codes.Unset`, with only the specified closed `error.type` on failure. Omit `http.response.status_code` when no valid terminal protocol status exists (panic before commit or invalid status); never export the invalid integer as a span or metric dimension.

- [ ] **Step 6: Run metric/status tests and observe RED**

Run: `go test ./internal/observability -run 'TestInstrumentHTTPRoute(Metrics|Status|Panic|Privacy)' -count=1`

Expected: tests fail until metrics, views, counting, and classification are complete.

- [ ] **Step 7: Register exact instruments and defense-in-depth views**

Create instruments once during runtime construction:

```go
duration, err := meter.Float64Histogram("http.server.request.duration",
	metric.WithUnit("s"), metric.WithDescription("Duration of matched non-health HTTP server requests"))
requestSize, err := meter.Int64Histogram("http.server.request.body.size",
	metric.WithUnit("By"), metric.WithDescription("Request body bytes observed by the server"))
responseSize, err := meter.Int64Histogram("http.server.response.body.size",
	metric.WithUnit("By"), metric.WithDescription("Response body bytes written by the server"))
```

Apply one exact-name view per instrument with `attribute.NewAllowKeysFilter(semconv.HTTPRequestMethodKey, semconv.HTTPRouteKey, semconv.HTTPResponseStatusCodeKey)`. Record seconds and bytes through `httpsnoop.CaptureMetrics`, wrapping the request body with a counting `io.ReadCloser`. Preserve `Flusher`, `Hijacker`, `Pusher`, and `ReaderFrom` via `httpsnoop`.

- [ ] **Step 8: Prove excluded requests stay outside instrumentation**

In the private chi fixture, leave `/healthz` and `/readyz` unwrapped, wrap only the fixture business route, and send health, readiness, unknown-path, and unsupported-method requests through that router. Require zero spans and metric points for each excluded request. Separately run the existing `internal/httpapi` tests unchanged to prove its public health-only surface still returns 204/404/405. Do not change `NewRouter()` until a real non-health route needs the wrapper.

- [ ] **Step 9: Run, format, tidy, and commit HTTP telemetry**

```bash
gofmt -w internal/observability/http.go internal/observability/http_test.go internal/observability/runtime.go internal/observability/runtime_test.go
go mod tidy
go test ./internal/observability ./internal/httpapi -count=1
go test -race ./internal/observability ./internal/httpapi -count=1
! go list -deps ./... | grep -F 'go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp'
git diff --check
git add internal/observability/http.go internal/observability/http_test.go internal/observability/runtime.go internal/observability/runtime_test.go go.mod go.sum
git commit -m "feat: add privacy-safe HTTP telemetry"
```

Expected: span/metric/privacy/status tests pass, excluded routes emit nothing, and `otelhttp` is absent.

### Task 5: Process Composition, Safe HTTP Errors, and Shutdown Ordering

**Files:**
- Modify: `cmd/maiden-lane/main.go`
- Modify: `cmd/maiden-lane/main_test.go`

**Interfaces:**
- Consumes: `observability.LoadConfig`, `New`, and `Runtime.Shutdown`.
- Produces: one process path that drains HTTP before flushing telemetry and calls `os.Exit` only after cleanup returns.

- [ ] **Step 1: Write failing process-composition and order tests**

Introduce private seams, not public fake types:

```go
type observabilityRuntime interface {
	Shutdown(context.Context) error
}

type processDeps struct {
	lookupEnv observability.LookupEnv
	readFile observability.ReadFile
	newRuntime func(context.Context, observability.Config, io.Writer) (observabilityRuntime, *slog.Logger, error)
	serve func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error
}

func execute(context.Context, []string, io.Writer, io.Writer, processDeps) error
```

Test order and causes:

```go
func TestExecuteDrainsApplicationBeforeTelemetryShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	events := []string{}
	// The fake serve appends "server-drained" before return. The runtime
	// appends "telemetry-shutdown", rejects a canceled context, and returns
	// telemetryErr. Compare both elements exactly and require errors.Is.
	err := execute(ctx, []string{"serve"}, io.Discard, io.Discard, deps)
	if len(events) != 2 || events[0] != "server-drained" || events[1] != "telemetry-shutdown" {
		t.Fatalf("events = %v", events)
	}
	if !errors.Is(err, telemetryErr) { t.Fatalf("error = %v", err) }
}
```

Add cases proving configuration failure prevents runtime/server construction; its stdout record contains `observability_configuration_invalid` and the safe field name but not the hostile value; runtime failure prevents server startup; server and telemetry errors both survive `errors.Is`; and shutdown occurs exactly once.

- [ ] **Step 2: Run process tests and observe RED**

Run: `go test ./cmd/maiden-lane -run 'TestExecute' -count=1`

Expected: compilation fails because the process composition seam is absent.

- [ ] **Step 3: Restructure `main` so cleanup precedes exit**

```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr, productionDeps())
	if err != nil {
		observability.NewLogger(os.Stdout, slog.LevelInfo).Error(
			"command failed", "code", "command_failed",
		)
		os.Exit(1)
	}
}
```

Inside `execute`: call `observability.LoadConfig(deps.lookupEnv, deps.readFile, version)` where `var version = "devel"`; on validation failure, use a default bootstrap logger to emit `observability_configuration_invalid` plus the contractually safe field-bearing config error and return before constructing runtime/server; call `observability.New(ctx, cfg, stdout)` (which creates `Runtime.Logger` and installs sanitized globals before exporters); on construction failure, emit only `observability_initialization_failed`; log only the bounded exporter modes on success; run the existing command parser; create `context.WithTimeout(context.Background(), 10*time.Second)` for telemetry shutdown; if shutdown fails, emit only `observability_shutdown_failed`; then join command/server and telemetry failures. The production `newRuntime` dependency returns `(runtime, runtime.Logger, err)` from that single call. Never log any other raw returned error. Release builds may later inject `version`; this slice emits no `service.version` for the development sentinel.

- [ ] **Step 4: Make server dependencies explicit and sanitize stdlib errors**

```go
type serveCommand func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error

func serveListener(
	ctx context.Context,
	listener net.Listener,
	logger *slog.Logger,
	handler http.Handler,
	errorLogger *log.Logger,
) error {
	server := &http.Server{
		Handler: handler, ErrorLog: errorLogger,
		ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout,
	}
	// Preserve the current drain and errors.Join behavior unchanged.
}
```

Create a private `io.Writer` for `http.Server.ErrorLog` that discards bytes and emits only `logger.Error("HTTP server internal failure", "code", "http_server_internal_error")`. Test it with hostile panic/path/peer/stack text; none may appear. Route panics still become `http.ErrAbortHandler`; this writer covers other stdlib diagnostics.

- [ ] **Step 5: Update existing tests without weakening contracts**

Pass the unchanged `httpapi.NewRouter()` and a discard `log.Logger` to current lifecycle tests. Preserve explicit-address, cancellation, joined shutdown/serve causes, `*net.OpError`, health `204`, and empty-body assertions. Add a full `execute` test with both exporters disabled and a fake serve function to prove no network work.

- [ ] **Step 6: Run, format, and commit process integration**

```bash
gofmt -w cmd/maiden-lane/main.go cmd/maiden-lane/main_test.go
go test ./cmd/maiden-lane ./internal/httpapi ./internal/observability -count=1
go test -race ./cmd/maiden-lane ./internal/httpapi ./internal/observability -count=1
git diff --check
git add cmd/maiden-lane/main.go cmd/maiden-lane/main_test.go
git commit -m "feat: compose observability lifecycle"
```

Expected: drain precedes fresh-context flush, all causes remain testable, and normal logs contain no raw error text.

### Task 6: Current-State Documentation and Full Verification

**Files:**
- Modify: `README.md`
- Modify: `METRICS.md`
- Modify: `docs/implementation/implementation-guide.md`

**Interfaces:**
- Consumes: implemented behavior and metric contract from Tasks 1-5.
- Produces: operator configuration, metric registry, and a guide describing only the repository now present.

- [ ] **Step 1: Replace the empty metric registry with the exact contract**

Set status to active and add exactly:

```markdown
| Name | Instrument | Unit | Permitted attributes or labels | Meaning |
|---|---|---|---|---|
| `http.server.request.duration` | `Float64Histogram` | `s` | `http.request.method`, `http.route`, `http.response.status_code` | Duration of a matched non-health request |
| `http.server.request.body.size` | `Int64Histogram` | `By` | `http.request.method`, `http.route`, `http.response.status_code` | Request body bytes observed by the server wrapper |
| `http.server.response.body.size` | `Int64Histogram` | `By` | `http.request.method`, `http.route`, `http.response.status_code` | Response body bytes written by the server wrapper |
```

Document the closed method set plus `OTHER`, trusted templates, status bounds, excluded routes, and disabled exemplars. State that instruments exist although the current health-only API records no request points.

- [ ] **Step 2: Document operational configuration in README**

Add:

```bash
# Local/default: JSON logs only; no collector connection.
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080

# OTLP/HTTP traces and metrics.
OTEL_TRACES_EXPORTER=otlp \
OTEL_METRICS_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.example \
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

List `LOG_LEVEL`, exporter selectors, service name, and supported standard OTLP/HTTP endpoint, protocol, TLS/certificate, headers, timeout, and compression variables. State that arbitrary resource attributes, baggage, OTel logs, OTLP/gRPC, and semantic telemetry are unsupported. Use no real credential.

- [ ] **Step 3: Rewrite the living guide to actual state**

Add `internal/observability` to the map and this current flow:

```text
operational config -> slog JSON stdout -> explicit OTel runtime -> chi router
HTTP drain -> fresh bounded OTel flush/shutdown -> final exit selection
```

Explain explicit providers, context carrying cancellation/trace context but not semantics, excluded health routes, explicit green success, and the privacy reason standard OTel HTTP middleware is absent. Retain real capabilities and gaps; mention no unimplemented package or operation.

- [ ] **Step 4: Run documentation contract searches**

```bash
test "$(rg -c '^\| `http\.server\.' METRICS.md)" -eq 3
rg -n 'http.server.request.duration|http.server.request.body.size|http.server.response.body.size' internal/observability METRICS.md
rg -n 'OTEL_TRACES_EXPORTER|OTEL_METRICS_EXPORTER|OTEL_SERVICE_NAME|LOG_LEVEL' README.md internal/observability/config.go
! rg -n 'Zap|otelzap|otelhttp.New|NewMiddleware|OTEL logs are exported' README.md METRICS.md docs/implementation/implementation-guide.md internal cmd
! git diff -- ERRORS.md Inviolates.md GLOSSARY.md api/openapi.yaml docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md
```

Expected: three entries and matching implementation names exist; forbidden claims and out-of-scope diffs are absent.

- [ ] **Step 5: Run complete verification**

```bash
make verify
make container-check
git diff --check
git status --short
```

Expected: format/tidy/tool pins/vet/Staticcheck/tests/race/govulncheck/build/container smoke pass. Status lists only the three documentation files.

- [ ] **Step 6: Inspect dependency, privacy, and scope boundaries**

```bash
go list -m all | rg 'go.opentelemetry.io/otel|github.com/(go-logr/logr|felixge/httpsnoop)'
! go list -deps ./... | rg 'go.uber.org/zap|otelzap|go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp'
! rg -n 'os\.(Getenv|LookupEnv)|envconfig|viper' internal/observability --glob '*.go' --glob '!*_test.go' --glob '!config.go'
! rg -n 'baggage.New|NewMember|NewKeyValueProperty' internal/observability --glob '!http_test.go'
git diff --stat
git diff
```

Expected: approved dependencies only; environment access stays at configuration composition; production creates no baggage; no semantic/API/AWS/unrelated changes appear.

- [ ] **Step 7: Commit current-state documentation**

```bash
git add README.md METRICS.md docs/implementation/implementation-guide.md
git commit -m "docs: document observability foundation"
git status --short --branch
```

Expected: a clean implementation branch.

## Final Acceptance Checklist

- [ ] Invalid selected OTLP settings fail closed without value leakage; disabled mode creates no exporter or network loop.
- [ ] Only approved service resource fields enter telemetry; ambient arbitrary resource attributes cannot enter.
- [ ] OTel diagnostics expose stable codes only.
- [ ] Rollback/shutdown are fresh-context, bounded, all-attempted, cause-preserving, race-safe, and idempotent.
- [ ] The route wrapper exports only closed span/metric attributes, strips baggage, and accepts trace context.
- [ ] Every completed Maiden Lane span is explicitly `Ok` or `Error`; only five approved `error.type` values exist.
- [ ] Panics expose neither values nor raw stdlib panic/peer/stack text.
- [ ] Metric names/kinds/units/dimensions match `METRICS.md`; exemplars are absent.
- [ ] Health, readiness, unmatched, and 405 requests emit no telemetry and retain wire behavior.
- [ ] HTTP drain precedes telemetry flush; exit selection follows shutdown.
- [ ] No semantic package imports observability or OTel; no observability setting enters semantic identity.
- [ ] `ERRORS.md`, HLD, Inviolates, glossary, completeness amendment, and OpenAPI remain unchanged.
- [ ] `make verify` and `make container-check` pass from the final committed tree.
