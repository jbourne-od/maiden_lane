package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

// Ratified worker span name and dimensions. The span is deliberately named for
// what it covers -- handling one claimed execution -- rather than for the queue,
// because the queue is an implementation detail the trace should not pin.
const (
	executionSpanName        = "maiden_lane.execution.process"
	attributeExecutionResult = "maiden_lane.execution.result"
	attributeFailureReason   = "maiden_lane.execution.failure_reason"
)

// Metric dimension keys. These are deliberately unprefixed, matching the
// ratified semantic dimension keys: a span attribute is namespaced because a
// span carries attributes from every layer it passes through, while a metric
// dimension is already scoped by the instrument it belongs to.
const (
	dimensionExecutionResult = "result"
	dimensionFailureReason   = "failure_reason"
)

// Closed worker outcome tokens. These are this package's own vocabulary rather
// than the worker's, for the same reason the semantic dimensions are: telemetry
// admits only values it recognizes, so a token invented upstream cannot reach a
// span by simply being passed along.
const (
	executionResultAnswered  = "answered"
	executionResultAbandoned = "abandoned"
	executionResultFailed    = "failed"
)

// Ratified execution instrument names.
const (
	executionOutcomesName = "maiden_lane.execution.outcomes"
	executionDurationName = "maiden_lane.execution.duration"
)

// executionDurationBoundaries cover the whole handling of one claimed execution,
// which includes reading the plan, recompiling it, re-deriving its identity,
// running the spine, and storing the result. That is milliseconds locally and
// plausibly seconds against a loaded database, so the range is wider than the
// per-phase histogram's and stops at a minute: past that the useful question is
// not which percentile but whether the lease held.
var executionDurationBoundaries = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// WorkerTracer returns the adapter that observes the worker's handling of one
// claimed execution. Each call yields an independent adapter holding no
// per-execution state, so one can serve concurrent workers.
func (r *Runtime) WorkerTracer() worker.ExecutionTracer {
	return workerTracer{runtime: r}
}

func (r *Runtime) registerExecutionInstruments() error {
	meter := r.meterProvider.Meter(instrumentationName)
	var err error
	r.executionOutcomes, err = meter.Int64Counter(
		executionOutcomesName,
		// A braced UCUM annotation, which the Prometheus translation drops rather
		// than appending. An unbraced "executions" would become
		// maiden_lane_execution_outcomes_executions_total, because unlike the
		// semantic counters this name does not already end in its own unit word.
		metric.WithUnit("{execution}"),
		metric.WithDescription("Claimed executions the worker finished with, by recorded outcome"),
	)
	if err != nil {
		return err
	}
	r.executionDuration, err = meter.Float64Histogram(
		executionDurationName,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the worker's handling of one claimed execution"),
	)
	return err
}

// executionMetricViews repeat the dimension allowlist inside the SDK, and set
// explicit boundaries so the histogram does not inherit the millisecond-shaped
// defaults. See TestDurationHistogramsCanDistinguishSubSecondLatency for what
// those defaults do to a seconds-valued instrument.
func executionMetricViews() []sdkmetric.View {
	return []sdkmetric.View{
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: executionOutcomesName},
			sdkmetric.Stream{AttributeFilter: attribute.NewAllowKeysFilter(
				dimensionExecutionResult, dimensionFailureReason,
			)},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: executionDurationName},
			sdkmetric.Stream{
				AttributeFilter: attribute.NewAllowKeysFilter(dimensionExecutionResult),
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
					Boundaries: executionDurationBoundaries,
				},
			},
		),
	}
}

// allMetricViews is the single place every instrument's view is registered.
// Assembling them here rather than at the call site means adding an instrument
// family without its allowlist is a visible omission instead of a silent one.
func allMetricViews() []sdkmetric.View {
	views := httpMetricViews()
	views = append(views, semanticMetricViews()...)
	return append(views, executionMetricViews()...)
}

type workerTracer struct {
	runtime *Runtime
}

// BeginExecution opens a consumer span covering one claimed execution.
//
// The identity attributes deliberately reuse the names the spine's phase spans
// already use. That is what makes one predicate on an execution identity match
// both this span and every phase beneath it; giving the worker its own names
// would mean two queries to answer one question.
//
// The span kind is Consumer because that is what this is: work arriving from a
// queue rather than from a caller waiting on it. It is not a child of the
// submission that queued it, and cannot be -- that request finished long before,
// and its span with it. Connecting the two needs a link carrying the submission's
// trace context through the queue, which is a durable-storage decision rather
// than a tracing one.
func (t workerTracer) BeginExecution(
	ctx context.Context, observation worker.ExecutionObservation,
) (context.Context, func(worker.ExecutionOutcome)) {
	attributes := make([]attribute.KeyValue, 0, 3)
	if observation.PlanID != "" {
		attributes = append(attributes,
			attribute.String(attributePlanID, string(observation.PlanID)))
	}
	if observation.RunID != "" {
		attributes = append(attributes,
			attribute.String(attributeRunID, string(observation.RunID)))
	}
	if observation.ExecutionID != "" {
		attributes = append(attributes,
			attribute.String(attributeExecutionID, string(observation.ExecutionID)))
	}

	// time.Now carries a monotonic component, so the recorded duration is
	// unaffected by wall-clock adjustment. No semantic value observes this clock.
	started := time.Now()
	ctx, span := t.runtime.tracerProvider.Tracer(instrumentationName).Start(
		ctx,
		executionSpanName,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attributes...),
	)

	return ctx, func(outcome worker.ExecutionOutcome) {
		t.finish(ctx, span, outcome, time.Since(started))
	}
}

// finish records the outcome on both signals and closes the span.
//
// The span and the counter are derived from the same outcome value, so telemetry
// cannot describe an execution one way in a trace and another way on a graph.
//
// Only a recorded terminal failure sets an error status. An abandoned execution
// is not an error: the work comes back when its lease lapses, and marking it as
// an error would put a red span in front of an operator for an ordinary
// shutdown.
func (t workerTracer) finish(
	ctx context.Context, span trace.Span, outcome worker.ExecutionOutcome, elapsed time.Duration,
) {
	defer span.End()

	result, admitted := admittedExecutionResult(outcome.Kind)
	if !admitted {
		// An unadmitted outcome fails closed by omission, per the ratified rule
		// for optional dimensions: labelling it would assert a classification the
		// worker never made, and inventing a bucket for it would put a value in
		// the vocabulary that nothing here can define. The span still closes,
		// because an unclosed span is worse than an unlabelled one, and no metric
		// point is recorded at all.
		span.SetStatus(codes.Error, "")
		return
	}

	span.SetAttributes(attribute.String(attributeExecutionResult, result))
	resultDimension := attribute.String(dimensionExecutionResult, result)
	t.runtime.executionDuration.Record(ctx, elapsed.Seconds(),
		metric.WithAttributes(resultDimension))

	if outcome.Kind != worker.OutcomeFailed {
		t.runtime.executionOutcomes.Add(ctx, 1, metric.WithAttributes(resultDimension))
		span.SetStatus(codes.Ok, "")
		return
	}

	countAttributes := []attribute.KeyValue{resultDimension}
	if reason, ok := admittedFailureReason(outcome.Reason); ok {
		span.SetAttributes(attribute.String(attributeFailureReason, reason))
		countAttributes = append(countAttributes, attribute.String(dimensionFailureReason, reason))
	}
	t.runtime.executionOutcomes.Add(ctx, 1, metric.WithAttributes(countAttributes...))
	span.SetStatus(codes.Error, result)
}

func admittedExecutionResult(kind worker.OutcomeKind) (string, bool) {
	switch kind {
	case worker.OutcomeAnswered:
		return executionResultAnswered, true
	case worker.OutcomeAbandoned:
		return executionResultAbandoned, true
	case worker.OutcomeFailed:
		return executionResultFailed, true
	default:
		return "", false
	}
}

// admittedFailureReason admits only the bounded reasons this package recognizes.
// An unrecognized one is omitted rather than copied through: a reason on a span
// is a claim about why work stopped, and passing an unknown token along would
// publish a classification nothing here can vouch for.
func admittedFailureReason(reason string) (string, bool) {
	switch reason {
	case worker.ReasonPlanAbsent, worker.ReasonIdentityMismatch,
		worker.ReasonInvalidInput, worker.ReasonInternalError:
		return reason, true
	default:
		return "", false
	}
}
