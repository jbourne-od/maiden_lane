# Observability Foundation Design

**Status:** Implemented foundation

**Date:** 2026-08-12

**Highest repository authority:** [Ratified Maiden Lane Inviolates](../../../Inviolates.md)

**Normative architecture:** [Maiden Lane High-Level Design](2026-08-11-maiden-lane-high-level-design.md)

## 1. Purpose

Maiden Lane needs the operational observability substrate before semantic,
application, worker, persistence, and AWS boundaries multiply. This slice
establishes structured logging, OpenTelemetry traces and metrics, context
propagation, safe HTTP instrumentation, and bounded lifecycle flushing without
instrumenting transformation semantics prematurely.

The distinction is deliberate:

- this design installs the pipes through which later operational telemetry can
  flow;
- it does not make telemetry part of a semantic result, identity, journal,
  readiness assessment, gate, or publication decision;
- it does not add speculative spans or metrics for operations that do not yet
  exist.

Observability configuration is ordinary operational configuration under
Inviolate 0. Changing a log level, exporter endpoint, sampling policy, or
telemetry enablement cannot change semantic output or any semantic identity.

## 2. Decisions

The foundation uses:

- the standard library's `log/slog` for application logs;
- the OpenTelemetry Go SDK for traces and metrics;
- OTLP over HTTP with protobuf payloads for both signals;
- W3C `tracecontext` propagation;
- explicit provider and propagator injection at infrastructure boundaries;
- a small `internal/observability` package composed by
  `cmd/maiden-lane`;
- safe, route-aware HTTP instrumentation for registered non-health routes;
- bounded shutdown after the HTTP server has drained.

The design does not use Zap, OTel log export, the OTel `autoexport` package,
OTLP/gRPC, an AWS-specific exporter, or a vendor SDK. OTel traces and metrics
are stable signals in the Go implementation; OTel logs are not required for
this foundation. Application logging remains useful even when OTLP is disabled
or unavailable.

Direct OTLP/HTTP exporters are preferred to `autoexport` because Maiden Lane
needs only one known transport for two signals. Pulling multiple exporters and
their transitive dependencies into the binary would not improve the current
contract.

## 3. Architectural boundary

Process composition owns operational configuration and all side effects:

```text
cmd/maiden-lane
      │
      ├── load and validate operational configuration
      ├── construct slog JSON logger
      ├── construct OTel Resource
      ├── construct TracerProvider ──> OTLP/HTTP
      ├── construct MeterProvider  ──> OTLP/HTTP
      ├── construct W3C trace-context propagator
      ├── run application with explicit observability dependencies
      ├── drain HTTP server
      └── flush and shut down OTel providers
```

`internal/observability` may depend on OTel, `slog`, and `net/http`. Semantic
packages must not import it. HTTP, future application use cases, workers, and
adapters may receive OTel providers or boundary helpers explicitly. They must
not discover telemetry through a semantic context or use telemetry values to
make domain decisions.

The initial runtime shape is conceptually:

```go
type Runtime struct {
	Logger *slog.Logger
	// OTel providers and shutdown state remain private.
}

func New(ctx context.Context, cfg Config, output io.Writer) (*Runtime, error)
func (r *Runtime) InstrumentHTTPRoute(method, pattern string, next http.Handler) http.Handler
func (r *Runtime) Shutdown(ctx context.Context) error
```

The exact internal file split is an implementation choice. The public behavior
of this internal package is not: initialization is explicit, route
instrumentation is safe by construction, and shutdown is bounded and
idempotent.

Global OTel providers are not required for the current explicit
instrumentation. If a future third-party instrumentation library requires an
OTel global, the composition root may register one as operational process
state; semantic packages still cannot observe it.

The composition root does own two process-global OTel facilities in this
slice: an error handler for asynchronous SDK and exporter failures, and an
internal OTel logger. They are installed once for the process lifetime. The
error handler emits only a stable `otel_async_error` code through `slog`; the
internal logger emits only `otel_internal_message` or `otel_internal_error`.
Neither forwards supplied error text, messages, names, or key-value fields.
This prevents OTel's environment parsing and SDK diagnostics from echoing
endpoints, headers, certificate paths, or other ambient values. Providers and
propagators remain explicit. The sanitizers are unit-tested as ordinary values;
installation is integration-tested in a subprocess because OTel exposes no
getter with which to restore its internal process-global logger safely.

## 4. Logging

`slog` writes one JSON object per line to standard output. The logger is
constructed before OTLP providers so an initialization failure can be reported
without depending on telemetry.

Initial logging records only bounded operational metadata:

- process and HTTP-server startup;
- HTTP-server stopping and stopped;
- configured telemetry mode without endpoints, headers, or credentials;
- provider initialization failure using a safe wrapped cause;
- runtime export failure using bounded diagnostic context;
- provider shutdown or flush failure.

Logs must not contain raw request or response bodies, URLs containing path
parameters or query strings, customer data, rules, journal content, evidence,
OTLP headers, tokens, or credentials. Existing Inviolate 17 and the logging
rules in `AGENTS.md` remain authoritative.

This slice does not add an application logging interface or an OTel log bridge.
Callers use `*slog.Logger` directly. Trace-log correlation may be added when a
real context-aware application log requires it; it is not simulated with a
framework in this foundation.

## 5. Operational configuration

The supported top-level configuration is:

| Variable | Supported values | Default | Meaning |
|---|---|---|---|
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` | Minimum application log level |
| `OTEL_TRACES_EXPORTER` | `none`, `otlp` | `none` | Trace provider mode |
| `OTEL_METRICS_EXPORTER` | `none`, `otlp` | `none` | Metric provider mode |
| `OTEL_SERVICE_NAME` | 1-128 UTF-8 bytes with no control characters | `maiden-lane` | OTel `service.name` resource attribute |

When a signal selects `otlp`, its OTLP/HTTP exporter uses the standard OTel
environment variables for endpoint, signal-specific endpoint, TLS,
certificate, headers, timeout, and compression. OTLP/gRPC protocol selection
is unsupported and fails configuration validation rather than silently
choosing a different transport.

The resource includes `service.name`. It includes `service.version` only when
the process has a meaningful build version; the development sentinel is not
published as a version. No random service-instance identity is introduced by
this slice. The foundation does not consume arbitrary
`OTEL_RESOURCE_ATTRIBUTES`: unrestricted resource attributes could introduce
customer data, secrets, or unbounded cardinality. A future resource attribute
must receive an explicit safe contract before it is enabled.

The upstream Go SDK's experimental `OTEL_GO_X_*` switches are unsupported.
They can change batching, metric timestamps, resource behavior, cardinality,
exemplars, or SDK self-observability outside this closed configuration. Maiden
Lane rejects known switches when configuration is loaded and rechecks
immediately before provider construction. Production code must not mutate the
process environment after startup; operational environment is an immutable
process input, not a runtime control channel. This boundary is what prevents an
upstream runtime environment lookup from becoming hidden Maiden Lane policy.

The initial trace sampler is fixed to parent-based, always-on sampling when
tracing is enabled. Configurable sampling remains ordinary operational policy,
but it is added only when the repository can state and test its supported
values. This slice does not silently interpret `OTEL_TRACES_SAMPLER` or
`OTEL_TRACES_SAMPLER_ARG`.

Telemetry is disabled by default so local development and tests perform no
network work without an explicit choice. Traces and metrics can be enabled
independently for diagnosis, although the expected deployment configuration
enables both as `otlp`.

Invalid selected configuration fails startup. Error reporting names the
invalid field and its allowed form without echoing headers, credentials, or a
potentially sensitive endpoint value.

## 6. Providers and propagation

Enabled tracing uses an OTel SDK `TracerProvider` with a batch span processor
and an OTLP/HTTP span exporter. Enabled metrics use an OTel SDK
`MeterProvider`, periodic reader, and OTLP/HTTP metric exporter. Provider
resource identity is shared.

Disabled signals use the OTel no-op behavior and allocate no exporter or
background network loop. Callers can use the same runtime surface regardless
of enablement.

Incoming W3C `traceparent` and `tracestate` values are propagated through
`context.Context`. This foundation deliberately does not accept or propagate
W3C baggage: arbitrary caller-supplied baggage is an uncontrolled data channel
that could later be forwarded across an adapter boundary. A future baggage
contract requires a closed key allowlist, bounded values, and an explicit
privacy review before enablement. Future outbound adapters must receive the
request context explicitly before they can propagate trace context.

Exporter failures after successful startup are operational failures. They are
reported through the OTel error path and `slog`, but they do not terminate the
application or change semantic behavior. The OTel SDK may drop telemetry under
its bounded buffering and retry policy; that loss is visible operationally but
does not invalidate a semantic run.

## 7. Safe HTTP instrumentation

HTTP instrumentation is attached only to registered, matched non-health routes
after chi has resolved a trusted route template. The following requests are
excluded:

- `GET /healthz`;
- `GET /readyz`;
- unmatched paths;
- method-not-allowed requests that did not enter a registered handler.

This prevents load-balancer probes and arbitrary attacker-controlled paths
from dominating or contaminating telemetry. Availability monitoring remains
an external platform concern.

The route wrapper may use OTel's HTTP instrumentation internally only if it
enforces Maiden Lane's stricter attribute contract. It passes the original
request and propagated context to the handler while ensuring that exported
telemetry can observe only:

- the trusted route template, never a resolved path;
- a normalized bounded HTTP method;
- the response status class or status code;
- for failures only, a closed `error.type` value;
- the protocol family/version where bounded;
- fixed service resource attributes.

It must not export:

- raw URL paths or path-parameter values;
- query strings;
- arbitrary HTTP methods;
- request or response headers;
- request or response bodies;
- client or peer addresses;
- user-agent strings;
- customer, entity, run, execution, attempt, or other unbounded identifiers.

Metric SDK views enforce the permitted attribute set even if an upstream OTel
instrumentation version later begins producing additional attributes. Tests
inspect exported spans as well as metrics; library defaults are not assumed to
remain safe across upgrades.

Metric exemplars are disabled in this foundation. OTel view filters apply to
metric point attributes but may leave the original attributes attached to an
exemplar; disabling exemplars prevents that side channel until Maiden Lane has
an explicit exemplar privacy contract.

The permitted HTTP `error.type` values are exactly:

- `http.client_error` for 400-499;
- `http.server_error` for 500-599;
- `request_canceled` for cancellation;
- `handler_panic` for a recovered handler panic;
- `invalid_http_status` for an invalid terminal status.

`error.type` is a span attribute only and is not a metric dimension.
When more than one condition is observable, classification precedence is
`handler_panic`, `request_canceled`, `invalid_http_status`, HTTP status class.

Because the current public API consists only of excluded health routes, this
revision emits no production HTTP request telemetry until a non-health route
is implemented. A private test handler proves the instrumentation and
propagation contract without creating a fake public endpoint.

## 8. Span status policy

Maiden Lane deliberately differs from the default OTel convention that often
leaves successful spans with `Unset` status. Every completed Maiden Lane-owned
span has an explicit terminal status:

- successful completion is `codes.Ok`;
- failure is `codes.Error` with a bounded safe classification;
- no completed exported Maiden Lane span remains `codes.Unset`.

For HTTP server spans:

- status codes from 100 through 399 produce `codes.Ok`;
- status codes from 400 through 599 produce `codes.Error`;
- cancellation, panic, or an invalid terminal HTTP status produces
  `codes.Error`;
- panic values and raw error text are not recorded as telemetry attributes,
  and a Maiden Lane recovery boundary records only `handler_panic` before
  terminating the request with `http.ErrAbortHandler` semantics;
- the HTTP server uses a safe error logger that never forwards the standard
  library's raw panic text, peer address, or stack payload into application
  logs.

For future application and adapter spans, a normal returned result sets
`codes.Ok` and a returned error sets `codes.Error`. Normal domain verdicts such
as `needs_input` are successful results and therefore `codes.Ok`; they must not
be mislabeled as operational failures.

Tests reject any completed exported Maiden Lane span whose status is
`codes.Unset`. This project-specific policy is intentional and documented so a
future OTel upgrade cannot silently restore the default behavior.

## 9. Initial metric contract

Installing safe HTTP instrumentation registers exactly three standard OTel
HTTP histograms:

| Name | Instrument | Unit | Permitted attributes | Meaning |
|---|---|---|---|---|
| `http.server.request.duration` | `Float64Histogram` | `s` | `http.request.method`, `http.route`, `http.response.status_code` | Duration of a matched non-health request |
| `http.server.request.body.size` | `Int64Histogram` | `By` | `http.request.method`, `http.route`, `http.response.status_code` | Request body bytes observed by the server wrapper |
| `http.server.response.body.size` | `Int64Histogram` | `By` | `http.request.method`, `http.route`, `http.response.status_code` | Response body bytes written by the server wrapper |

The route value is always the registered template. Methods are normalized to a
closed set. Status codes are finite protocol values. No customer or semantic
identity is a metric dimension.

These instruments are registered in `METRICS.md` in the implementation change.
No custom `maiden_lane.*` metric is introduced until a real implemented
operation gives it stable semantics. Comparison measurements remain semantic
gate artifacts rather than operational metrics.

## 10. Lifecycle and failure behavior

Initialization is transactional at the process boundary:

1. Construct the JSON logger.
2. Validate all observability configuration.
3. Construct the shared resource and propagator.
4. Construct each enabled exporter and provider.
5. If a later step fails, create a fresh bounded rollback context, attempt to
   shut down every component already constructed, and join cleanup failures
   with the construction error while preserving every cause.
6. Start the application only after initialization succeeds.

Shutdown occurs after application work and HTTP draining:

1. Stop accepting new work and drain the HTTP server under its existing
   bounded shutdown policy.
2. Create a fresh bounded telemetry-shutdown context. The canceled process
   context cannot be reused.
3. Flush and shut down the trace provider if enabled.
4. Flush and shut down the metric provider if enabled.
5. Attempt every required shutdown even after one fails.
6. Join application, server, and telemetry failures while preserving causes
   for `errors.Is` and `errors.As`.
7. Log the final safe failure summary and select the process exit status only
   after telemetry shutdown has completed or timed out.

`Runtime.Shutdown` is concurrency-safe and idempotent. A repeated call returns
the original terminal result and performs no second shutdown. Disabled mode
has a harmless shutdown and performs no network access.

The process must not rely on a deferred shutdown that is skipped by
`os.Exit`. Composition is structured so all cleanup completes before the final
exit call.

Configured telemetry initialization failure is not silently downgraded to
disabled mode. Conversely, a collector becoming unavailable after startup
does not make the HTTP service unavailable. This separates configuration
integrity from runtime observability availability.

## 11. Errors and catalogs

This slice does not introduce a stable Maiden Lane application error. Ordinary
configuration, exporter-construction, and shutdown failures are wrapped with
causal context but are consumed at the process boundary. `ERRORS.md` therefore
remains an empty registry.

An implementation must not add a public typed error merely to test failure
paths. Tests use preserved underlying causes. If a later caller needs a stable
machine-actionable observability error contract, that contract and
`ERRORS.md` change together.

`METRICS.md` changes because the three HTTP instruments are an exported
operational contract even when the current health-only surface produces no
measurements.

## 12. Verification

The implementation must establish at least these properties:

1. Disabled mode constructs successfully and performs no exporter network
   activity.
2. Unsupported exporter modes and malformed enabled OTLP/HTTP configuration
   fail startup.
3. Partial initialization uses a fresh bounded rollback context, attempts to
   clean up every component already created, and preserves construction and
   cleanup causes.
4. A matched private fixture route produces one server span with a trusted
   route template and the three registered metrics with exact names and units.
5. An incoming `traceparent` becomes the parent of the server span, while
   caller-supplied baggage is neither accepted nor forwarded.
6. Health, readiness, unmatched, and method-not-allowed requests produce no
   span or metric measurement.
7. Raw paths, query values, headers, bodies, addresses, user agents, arbitrary
   methods, and identifier values never appear in exported attributes.
8. Metric attribute sets exactly match the `METRICS.md` allowlist.
9. HTTP 100-399 results have `codes.Ok`; HTTP 400-599, cancellation, and panic
   have `codes.Error` with the specified closed classification; no completed
   span has `codes.Unset`.
10. HTTP draining finishes before telemetry shutdown begins.
11. Telemetry flushing uses a fresh context rather than the canceled process
    context.
12. Trace and metric shutdown are both attempted, errors remain discoverable,
    and repeated shutdown is harmless.
13. Runtime exporter failure does not terminate the application.
14. Existing health behavior and the OpenAPI contract remain unchanged.
15. Package tests and lifecycle tests pass under the race detector.
16. Hostile `OTEL_RESOURCE_ATTRIBUTES` and baggage inputs cannot add resource,
    span, metric, log, or outbound propagation data.
17. Handler panics expose neither their values, request-derived strings, peer
    addresses, nor standard-library stack payloads in telemetry or logs.
18. The process-global OTel error handler and internal logger emit only their
    registered stable codes and expose none of their supplied text or fields;
    global installation is verified in an isolated subprocess.
19. Metric exemplars remain absent even when a measurement is recorded from a
    sampled trace carrying hostile attributes.
20. Unsupported experimental `OTEL_GO_X_*` policy fails configuration or
    provider construction, and production code contains no environment
    mutation path after startup.

Verification broadens through the repository's authoritative `make verify`
and `make container-check` commands. The container smoke check continues to
run with telemetry disabled and therefore requires no collector.

## 13. Documentation effects

The implementation change updates:

- `README.md` with logging behavior, enablement examples, and supported
  operational variables;
- `METRICS.md` with the exact three HTTP instruments and dimensions;
- the living Implementation Guide with only the package boundaries, runtime
  flow, and capabilities actually implemented;
- code comments around privacy filtering, explicit span status, partial
  initialization cleanup, and shutdown ordering.

It does not change the HLD, the progressive-completeness amendment,
Inviolates, glossary, or OpenAPI surface. Those sources already place
observability at operational boundaries and prohibit customer data and
unbounded metric dimensions.

## 14. Non-goals

This foundation does not implement:

- OTel logs or a `slog`-to-OTel bridge;
- W3C baggage propagation without an approved closed privacy contract;
- metric exemplars without an approved privacy and cardinality contract;
- a custom application logging interface;
- trace-log correlation without a real request log;
- semantic compiler, executor, checkpoint, readiness, comparison, promotion,
  or publication spans;
- custom Maiden Lane metrics;
- AWS resource detectors or CloudWatch-specific export;
- Collector, dashboard, alert, or deployment configuration;
- persistent telemetry buffering or delivery guarantees;
- dynamic runtime reconfiguration;
- profiling;
- a worker command or adapter instrumentation;
- arbitrary automatic instrumentation that can emit raw request metadata.

Those capabilities are added only with the real boundary whose behavior they
measure.
