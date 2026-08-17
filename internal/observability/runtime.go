package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type providerLifecycle interface {
	ForceFlush(context.Context) error
	Shutdown(context.Context) error
}

type factories struct {
	newTrace  func(context.Context, Config, *resource.Resource) (trace.TracerProvider, providerLifecycle, error)
	newMetric func(context.Context, Config, *resource.Resource) (metric.MeterProvider, providerLifecycle, error)
}

// Runtime owns the process logger and explicit OpenTelemetry dependencies.
type Runtime struct {
	Logger *slog.Logger

	tracerProvider   trace.TracerProvider
	meterProvider    metric.MeterProvider
	propagator       propagation.TextMapPropagator
	httpDuration     metric.Float64Histogram
	httpRequestSize  metric.Int64Histogram
	httpResponseSize metric.Int64Histogram

	// Semantic instruments back the app-owned observer contract. They are
	// registered because the internal use case exists; with no public caller
	// the production process records no semantic points yet.
	semanticPhaseDuration     metric.Float64Histogram
	semanticOperations        metric.Int64Counter
	semanticCheckpoints       metric.Int64Counter
	semanticInvariantFailures metric.Int64Counter
	semanticAssessments       metric.Int64Counter

	// Execution instruments cover the worker's handling of claimed executions.
	// They are the only view of the asynchronous path that survives a restart of
	// whatever was watching it, because a trace answers "what happened to this
	// one" while these answer "is the queue being worked".
	executionOutcomes metric.Int64Counter
	executionDuration metric.Float64Histogram

	traceLifecycle  providerLifecycle
	metricLifecycle providerLifecycle
	shutdownOnce    sync.Once
	shutdownErr     error
}

// New validates that Config is unchanged before constructing the logger. It
// then installs sanitized process-wide OTel diagnostics before creating any
// exporter that might report through them.
func New(ctx context.Context, cfg Config, output io.Writer) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if err := rejectAmbientOTelExperimental(); err != nil {
		return nil, err
	}
	logger := NewLogger(output, cfg.LogLevel)
	installOTelGlobals(logger)
	return newRuntimeWithLogger(ctx, cfg, logger, defaultFactories())
}

func newRuntime(ctx context.Context, cfg Config, output io.Writer, f factories) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return newRuntimeWithLogger(ctx, cfg, NewLogger(output, cfg.LogLevel), f)
}

func newRuntimeWithLogger(ctx context.Context, cfg Config, logger *slog.Logger, f factories) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if ambientResourcePresent() {
		return nil, invalidField("OTEL_RESOURCE_ATTRIBUTES", "an unset value")
	}
	if err := rejectAmbientOTelExperimental(); err != nil {
		return nil, err
	}

	attributes := []attribute.KeyValue{semconv.ServiceName(cfg.ServiceName)}
	if cfg.ServiceVersion != "" {
		attributes = append(attributes, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	res := resource.NewWithAttributes(semconv.SchemaURL, attributes...)
	runtime := &Runtime{
		Logger:         logger,
		tracerProvider: tracenoop.NewTracerProvider(),
		meterProvider:  metricnoop.NewMeterProvider(),
		propagator:     propagation.TraceContext{},
	}
	if cfg.TracesExporter == ExporterOTLP {
		if ambientResourcePresent() {
			return nil, errAmbientResource
		}
		provider, lifecycle, err := f.newTrace(ctx, cfg, res)
		runtime.traceLifecycle = lifecycle
		if err != nil {
			if errors.Is(err, errAmbientResource) {
				return nil, errors.Join(err, rollbackRuntime(runtime))
			}
			return nil, errors.Join(
				safeCause{message: "trace provider initialization failed", cause: err},
				rollbackRuntime(runtime),
			)
		}
		if provider == nil || lifecycle == nil {
			return nil, errors.Join(
				safeCause{message: "trace provider initialization failed", cause: errInvalidProviderFactory},
				rollbackRuntime(runtime),
			)
		}
		runtime.tracerProvider = provider
	}
	if cfg.MetricsExporter == ExporterOTLP {
		if ambientResourcePresent() {
			return nil, errors.Join(
				errAmbientResource,
				rollbackRuntime(runtime),
			)
		}
		provider, lifecycle, err := f.newMetric(ctx, cfg, res)
		runtime.metricLifecycle = lifecycle
		if err != nil {
			if errors.Is(err, errAmbientResource) {
				return nil, errors.Join(err, rollbackRuntime(runtime))
			}
			constructionErr := safeCause{message: "metric provider initialization failed", cause: err}
			return nil, errors.Join(constructionErr, rollbackRuntime(runtime))
		}
		if provider == nil || lifecycle == nil {
			return nil, errors.Join(
				safeCause{message: "metric provider initialization failed", cause: errInvalidProviderFactory},
				rollbackRuntime(runtime),
			)
		}
		runtime.meterProvider = provider
	}
	if err := runtime.registerHTTPInstruments(); err != nil {
		return nil, errors.Join(
			safeCause{message: "HTTP telemetry initialization failed", cause: err},
			rollbackRuntime(runtime),
		)
	}
	if err := runtime.registerSemanticInstruments(); err != nil {
		return nil, errors.Join(
			safeCause{message: "semantic telemetry initialization failed", cause: err},
			rollbackRuntime(runtime),
		)
	}
	if err := runtime.registerExecutionInstruments(); err != nil {
		return nil, errors.Join(
			safeCause{message: "execution telemetry initialization failed", cause: err},
			rollbackRuntime(runtime),
		)
	}
	return runtime, nil
}

var (
	errAmbientResource        = invalidField("OTEL_RESOURCE_ATTRIBUTES", "an unset value")
	errInvalidProviderFactory = errors.New("provider factory returned an incomplete provider")
)

func rollbackRuntime(runtime *Runtime) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var errs []error
	if runtime.traceLifecycle != nil {
		if err := runtime.traceLifecycle.Shutdown(ctx); err != nil {
			errs = append(errs, safeCause{message: "trace provider rollback failed", cause: err})
		}
	}
	if runtime.metricLifecycle != nil {
		if err := runtime.metricLifecycle.Shutdown(ctx); err != nil {
			errs = append(errs, safeCause{message: "metric provider rollback failed", cause: err})
		}
	}
	return errors.Join(errs...)
}

func ambientResourcePresent() bool {
	raw, present := os.LookupEnv("OTEL_RESOURCE_ATTRIBUTES")
	return present && raw != ""
}

func defaultFactories() factories {
	return factories{
		newTrace:  newTraceProvider,
		newMetric: newMetricProvider,
	}
}

func newTraceProvider(ctx context.Context, cfg Config, res *resource.Resource) (trace.TracerProvider, providerLifecycle, error) {
	if err := rejectAmbientOTelExperimental(); err != nil {
		return nil, nil, err
	}
	exporter, err := otlptracehttp.New(ctx, traceExporterOptions(cfg.traces)...)
	if err != nil {
		return nil, nil, err
	}
	processor := sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithMaxQueueSize(2048),
		sdktrace.WithMaxExportBatchSize(512),
		sdktrace.WithBatchTimeout(5*time.Second),
		sdktrace.WithExportTimeout(30*time.Second),
	)
	if ambientResourcePresent() {
		return nil, nil, errors.Join(errAmbientResource, shutdownOwned(processor, "trace exporter rollback failed"))
	}
	if err := rejectAmbientOTelExperimental(); err != nil {
		return nil, nil, errors.Join(err, shutdownOwned(processor, "trace exporter rollback failed"))
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
			AttributeValueLengthLimit:   sdktrace.DefaultAttributeValueLengthLimit,
			AttributeCountLimit:         sdktrace.DefaultAttributeCountLimit,
			EventCountLimit:             sdktrace.DefaultEventCountLimit,
			LinkCountLimit:              sdktrace.DefaultLinkCountLimit,
			AttributePerEventCountLimit: sdktrace.DefaultAttributePerEventCountLimit,
			AttributePerLinkCountLimit:  sdktrace.DefaultAttributePerLinkCountLimit,
		}),
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithoutPanicRecording(),
	)
	return provider, provider, nil
}

func newMetricProvider(ctx context.Context, cfg Config, res *resource.Resource) (metric.MeterProvider, providerLifecycle, error) {
	if err := rejectAmbientOTelExperimental(); err != nil {
		return nil, nil, err
	}
	exporter, err := otlpmetrichttp.New(ctx, metricExporterOptions(cfg.metrics)...)
	if err != nil {
		return nil, nil, err
	}
	reader := sdkmetric.NewPeriodicReader(exporter,
		sdkmetric.WithInterval(60*time.Second),
		sdkmetric.WithTimeout(30*time.Second),
	)
	if ambientResourcePresent() {
		return nil, nil, errors.Join(errAmbientResource, shutdownOwned(reader, "metric exporter rollback failed"))
	}
	if err := rejectAmbientOTelExperimental(); err != nil {
		return nil, nil, errors.Join(err, shutdownOwned(reader, "metric exporter rollback failed"))
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
		sdkmetric.WithCardinalityLimit(2000),
		sdkmetric.WithView(allMetricViews()...),
	)
	return provider, provider, nil
}

func rejectAmbientOTelExperimental() error {
	return rejectUnsupportedOTelExperimental(os.LookupEnv)
}

func shutdownOwned(owned interface{ Shutdown(context.Context) error }, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := owned.Shutdown(ctx); err != nil {
		return safeCause{message: message, cause: err}
	}
	return nil
}

func traceExporterOptions(cfg otlpHTTPConfig) []otlptracehttp.Option {
	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.endpoint.String()),
		otlptracehttp.WithHeaders(cloneHeaders(cfg.headers)),
		otlptracehttp.WithTimeout(cfg.timeout),
		otlptracehttp.WithHTTPClient(explicitHTTPClient(cfg)),
	}
	if cfg.compression == "gzip" {
		options = append(options, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	} else {
		options = append(options, otlptracehttp.WithCompression(otlptracehttp.NoCompression))
	}
	if cfg.endpoint.Scheme == "http" {
		options = append(options, otlptracehttp.WithInsecure())
	}
	return options
}

func metricExporterOptions(cfg otlpHTTPConfig) []otlpmetrichttp.Option {
	options := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.endpoint.String()),
		otlpmetrichttp.WithHeaders(cloneHeaders(cfg.headers)),
		otlpmetrichttp.WithTimeout(cfg.timeout),
		otlpmetrichttp.WithTemporalitySelector(sdkmetric.CumulativeTemporalitySelector),
		otlpmetrichttp.WithAggregationSelector(sdkmetric.DefaultAggregationSelector),
		otlpmetrichttp.WithHTTPClient(explicitHTTPClient(cfg)),
	}
	if cfg.compression == "gzip" {
		options = append(options, otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
	} else {
		options = append(options, otlpmetrichttp.WithCompression(otlpmetrichttp.NoCompression))
	}
	if cfg.endpoint.Scheme == "http" {
		options = append(options, otlpmetrichttp.WithInsecure())
	}
	return options
}

// explicitHTTPClient carries the validated TLS value through exporter
// configuration even for an HTTP endpoint, where OTel v1.45 rejects combining
// WithTLSClientConfig and WithInsecure. The transport deliberately has no
// ambient proxy function; the endpoint URL is the complete network policy.
func explicitHTTPClient(cfg otlpHTTPConfig) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   cloneTLSConfig(cfg.tlsConfig),
			DisableKeepAlives: true,
		},
		Timeout: cfg.timeout,
	}
}

// Shutdown releases runtime telemetry resources.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		var errs []error
		if r.traceLifecycle != nil {
			if err := r.traceLifecycle.ForceFlush(ctx); err != nil {
				errs = append(errs, safeCause{message: "trace provider flush failed", cause: err})
			}
			if err := r.traceLifecycle.Shutdown(ctx); err != nil {
				errs = append(errs, safeCause{message: "trace provider shutdown failed", cause: err})
			}
		}
		if r.metricLifecycle != nil {
			if err := r.metricLifecycle.ForceFlush(ctx); err != nil {
				errs = append(errs, safeCause{message: "metric provider flush failed", cause: err})
			}
			if err := r.metricLifecycle.Shutdown(ctx); err != nil {
				errs = append(errs, safeCause{message: "metric provider shutdown failed", cause: err})
			}
		}
		r.shutdownErr = errors.Join(errs...)
	})
	return r.shutdownErr
}

// safeCause preserves causal identity for errors.Is/errors.As while exposing
// only a fixed message at the process boundary.
type safeCause struct {
	message string
	cause   error
}

func (e safeCause) Error() string { return e.message }
func (e safeCause) Unwrap() error { return e.cause }
