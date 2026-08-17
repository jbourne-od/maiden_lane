package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

func newWorkerFixture(t *testing.T) (*Runtime, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
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
		sdkmetric.WithView(executionMetricViews()...),
	)
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider Shutdown: %v", err)
		}
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider Shutdown: %v", err)
		}
	})
	runtime := &Runtime{tracerProvider: tracerProvider, meterProvider: meterProvider}
	if err := runtime.registerExecutionInstruments(); err != nil {
		t.Fatalf("register execution instruments: %v", err)
	}
	return runtime, spanRecorder, metricReader
}

const (
	fixtureExecutionID = "sha256:execution"
	fixtureRunID       = "sha256:run"
	fixturePlanID      = "sha256:plan"
)

func beginFixtureExecution(t *testing.T, runtime *Runtime) func(worker.ExecutionOutcome) {
	t.Helper()
	_, end := runtime.WorkerTracer().BeginExecution(context.Background(), worker.ExecutionObservation{
		PlanID:      fixturePlanID,
		RunID:       fixtureRunID,
		ExecutionID: fixtureExecutionID,
	})
	return end
}

// The worker span must carry the identities the spine's phase spans carry, under
// the same names, or an operator holding an executionID needs two different
// queries to see the work and the phases inside it.
func TestWorkerSpanCarriesTheSameIdentityNamesAsThePhases(t *testing.T) {
	runtime, spans, _ := newWorkerFixture(t)
	beginFixtureExecution(t, runtime)(worker.ExecutionOutcome{Kind: worker.OutcomeAnswered})

	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("spans ended = %d, want 1", len(ended))
	}
	span := ended[0]
	if span.Name() != executionSpanName {
		t.Fatalf("span name = %q, want %q", span.Name(), executionSpanName)
	}
	// Consumer, because the work arrived from a queue rather than from a caller
	// waiting on the response.
	if span.SpanKind() != trace.SpanKindConsumer {
		t.Fatalf("span kind = %v, want consumer", span.SpanKind())
	}
	attributes := spanAttributeMap(span)
	for key, want := range map[string]string{
		attributePlanID:      fixturePlanID,
		attributeRunID:       fixtureRunID,
		attributeExecutionID: fixtureExecutionID,
	} {
		if attributes[key] != want {
			t.Fatalf("attribute %s = %v, want %q", key, attributes[key], want)
		}
	}
}

// Production break caught: an abandoned execution is coming back when its lease
// lapses. Recording it as an error would show an operator a red span for an
// ordinary shutdown, training them to ignore the colour that matters.
func TestOnlyRecordedFailuresAreErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		outcome    worker.ExecutionOutcome
		wantCode   codes.Code
		wantResult string
		wantReason any
	}{
		{
			name:       "answered",
			outcome:    worker.ExecutionOutcome{Kind: worker.OutcomeAnswered},
			wantCode:   codes.Ok,
			wantResult: executionResultAnswered,
		},
		{
			name:       "abandoned",
			outcome:    worker.ExecutionOutcome{Kind: worker.OutcomeAbandoned},
			wantCode:   codes.Ok,
			wantResult: executionResultAbandoned,
		},
		{
			name: "failed",
			outcome: worker.ExecutionOutcome{
				Kind: worker.OutcomeFailed, Reason: worker.ReasonPlanAbsent,
			},
			wantCode:   codes.Error,
			wantResult: executionResultFailed,
			wantReason: worker.ReasonPlanAbsent,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime, spans, _ := newWorkerFixture(t)
			beginFixtureExecution(t, runtime)(testCase.outcome)

			span := spans.Ended()[0]
			if span.Status().Code != testCase.wantCode {
				t.Fatalf("status = %v, want %v", span.Status().Code, testCase.wantCode)
			}
			attributes := spanAttributeMap(span)
			if attributes[attributeExecutionResult] != testCase.wantResult {
				t.Fatalf("result = %v, want %q",
					attributes[attributeExecutionResult], testCase.wantResult)
			}
			if got := attributes[attributeFailureReason]; got != testCase.wantReason {
				t.Fatalf("failure reason = %v, want %v", got, testCase.wantReason)
			}
		})
	}
}

// An outcome this package does not recognize must not be labelled, and must not
// be counted under an invented bucket. The ratified rule for optional dimensions
// is that unadmitted values fail closed by omission rather than being relabeled.
func TestUnadmittedOutcomeIsNeitherLabelledNorCounted(t *testing.T) {
	runtime, spans, reader := newWorkerFixture(t)
	beginFixtureExecution(t, runtime)(worker.ExecutionOutcome{Kind: worker.OutcomeKind(200)})

	span := spans.Ended()[0]
	if _, present := spanAttributeMap(span)[attributeExecutionResult]; present {
		t.Fatal("an unadmitted outcome was given a result label")
	}
	if span.EndTime().IsZero() {
		t.Fatal("the span was left open")
	}
	if metrics := collectMetrics(t, reader); len(metrics) != 0 {
		t.Fatalf("unadmitted outcome recorded metric points: %+v", metrics)
	}
}

// An unadmitted failure reason must not reach a span or a metric either, because
// a reason is a claim about why work stopped and nothing here can vouch for a
// token it does not know.
func TestUnadmittedFailureReasonIsOmitted(t *testing.T) {
	runtime, spans, reader := newWorkerFixture(t)
	beginFixtureExecution(t, runtime)(worker.ExecutionOutcome{
		Kind: worker.OutcomeFailed, Reason: "something_invented_upstream",
	})

	if _, present := spanAttributeMap(spans.Ended()[0])[attributeFailureReason]; present {
		t.Fatal("an unadmitted reason reached a span")
	}
	// The execution still counts: it did fail, and dropping the point entirely
	// would understate failures because of a labelling problem.
	counted := counterPoints(t, collectMetrics(t, reader), executionOutcomesName)
	if len(counted) != 1 || counted[0].Value != 1 {
		t.Fatalf("counter points = %+v, want one point of 1", counted)
	}
	if _, present := counted[0].Attributes.Value(dimensionFailureReason); present {
		t.Fatal("an unadmitted reason reached a metric dimension")
	}
}

// The metric dimensions are the documented contract in METRICS.md, and they are
// deliberately not the span attribute names: a dimension is already scoped by its
// instrument, so prefixing it would publish maiden_lane_execution_result as a
// Prometheus label for a series already called maiden_lane_execution_outcomes.
func TestExecutionMetricDimensionsAreTheUnprefixedNames(t *testing.T) {
	runtime, _, reader := newWorkerFixture(t)
	beginFixtureExecution(t, runtime)(worker.ExecutionOutcome{
		Kind: worker.OutcomeFailed, Reason: worker.ReasonPlanAbsent,
	})

	metrics := collectMetrics(t, reader)
	counted := counterPoints(t, metrics, executionOutcomesName)
	if len(counted) != 1 {
		t.Fatalf("counter points = %d, want 1", len(counted))
	}
	for key, want := range map[string]string{
		dimensionExecutionResult: executionResultFailed,
		dimensionFailureReason:   worker.ReasonPlanAbsent,
	} {
		value, present := counted[0].Attributes.Value(attribute.Key(key))
		if !present || value.AsString() != want {
			t.Fatalf("dimension %s = %v (present %t), want %q", key, value, present, want)
		}
	}
	// The prefixed span names must not have leaked onto the metric.
	for _, spanOnly := range []string{attributeExecutionResult, attributeFailureReason} {
		if _, present := counted[0].Attributes.Value(attribute.Key(spanOnly)); present {
			t.Fatalf("span attribute %s reached a metric dimension", spanOnly)
		}
	}
}

// The span and the counter come from one outcome value, so they cannot disagree.
func TestOutcomeIsRecordedOnBothSignals(t *testing.T) {
	runtime, spans, reader := newWorkerFixture(t)
	tracer := runtime.WorkerTracer()
	for _, outcome := range []worker.ExecutionOutcome{
		{Kind: worker.OutcomeAnswered},
		{Kind: worker.OutcomeAnswered},
		{Kind: worker.OutcomeFailed, Reason: worker.ReasonIdentityMismatch},
	} {
		_, end := tracer.BeginExecution(context.Background(), worker.ExecutionObservation{
			ExecutionID: fixtureExecutionID,
		})
		end(outcome)
	}

	if got := len(spans.Ended()); got != 3 {
		t.Fatalf("spans = %d, want 3", got)
	}
	metrics := collectMetrics(t, reader)

	total := 0.0
	for _, point := range counterPoints(t, metrics, executionOutcomesName) {
		total += float64(point.Value)
	}
	if total != 3 {
		t.Fatalf("counted executions = %v, want 3", total)
	}

	duration, present := metrics[executionDurationName]
	if !present {
		t.Fatalf("%s was never recorded", executionDurationName)
	}
	histogram, ok := duration.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%s data = %T, want a float histogram", executionDurationName, duration.Data)
	}
	observed := uint64(0)
	for _, point := range histogram.DataPoints {
		observed += point.Count
		// Every duration histogram in this repository declares explicit
		// boundaries; inheriting the SDK's millisecond-shaped defaults is the
		// defect internal/observability/histograms_test.go exists to prevent.
		if len(point.Bounds) == 0 {
			t.Fatal("the execution histogram has no explicit boundaries")
		}
	}
	if observed != 3 {
		t.Fatalf("duration observations = %d, want 3", observed)
	}
}

func counterPoints(
	t *testing.T, metrics map[string]metricdata.Metrics, name string,
) []metricdata.DataPoint[int64] {
	t.Helper()
	measurement, present := metrics[name]
	if !present {
		return nil
	}
	sum, ok := measurement.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s data = %T, want an integer sum", name, measurement.Data)
	}
	return sum.DataPoints
}
