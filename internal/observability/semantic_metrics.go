package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/optimaldynamics/maiden-lane/internal/app"
)

// This file owns the semantic metric contract: instrument registration, the
// normalized bounded projection of one completed phase, and recording.
//
// The runtime registers these instruments because the internal use case exists
// after this slice. With no public caller, the production process records no
// semantic points yet; tests exercise recording without inventing an HTTP or
// CLI surface.

// Ratified instrument names and units (design section 11.4).
const (
	semanticPhaseDurationName     = "maiden_lane.semantic.phase.duration"
	semanticOperationsName        = "maiden_lane.semantic.structural.operations"
	semanticCheckpointsName       = "maiden_lane.semantic.checkpoints"
	semanticInvariantFailuresName = "maiden_lane.semantic.invariant.failures"
	semanticAssessmentsName       = "maiden_lane.semantic.readiness.assessments"
)

// Ratified metric dimension keys (design section 11.4). These names are part
// of the registered metric contract and are deliberately unprefixed.
const (
	dimensionPhase         = "phase"
	dimensionResult        = "result"
	dimensionOperationKind = "operation_kind"
	dimensionInvariantCode = "invariant_code"
	dimensionProfileKind   = "profile_kind"
	dimensionVerdict       = "verdict"
)

// operationCount is one normalized structural-operation metric point.
type operationCount struct {
	kind   operationKind
	result operationResult
	count  uint64
}

// semanticMeasurement is the observability-owned normalized projection of one
// completed phase. It holds only closed dimensions and non-negative counts:
// there is no field in which an identity, digest, or free-form string could
// reach a metric.
type semanticMeasurement struct {
	phase          observationPhase
	result         observationResult
	duration       time.Duration
	operations     []operationCount
	checkpoint     checkpointResult
	invariantCode  closedCode
	invariantCount uint64
	profile        profileKind
	verdict        readinessVerdict
}

// semanticMeasurementFor normalizes the app metric projection. It receives the
// projection rather than the whole observation, so the observed identities are
// structurally unreachable from metric recording.
func semanticMeasurementFor(projection app.MetricObservation, duration time.Duration) semanticMeasurement {
	phase := observedPhase(projection.Phase)
	result := observedResult(projection.Result)
	measurement := semanticMeasurement{phase: phase, result: result, duration: duration}

	for _, candidate := range []operationCount{
		{operationInsert, operationAccepted, projection.AcceptedInserts},
		{operationRelate, operationAccepted, projection.AcceptedRelates},
		{operationUpdate, operationAccepted, projection.AcceptedUpdates},
		{operationInsert, operationRejected, projection.RejectedInserts},
		{operationRelate, operationRejected, projection.RejectedRelates},
		{operationUpdate, operationRejected, projection.RejectedUpdates},
	} {
		if candidate.count > 0 {
			measurement.operations = append(measurement.operations, candidate)
		}
	}

	// Only an actual seal outcome counts. Machinery inability during sealing is
	// not a refusal, and an unreached checkpoint is not a rejected one.
	if phase == phaseSealCheckpoint {
		switch {
		case result == resultSuccess:
			measurement.checkpoint = checkpointSealed
		case result.semanticRejection():
			measurement.checkpoint = checkpointRejected
		}
	}

	if projection.InvariantFailures > 0 {
		if code, ok := observedInvariantCode(projection.Code); ok {
			measurement.invariantCode, measurement.invariantCount = code, projection.InvariantFailures
		}
	}

	// One completed immutable assessment per readiness phase that produced a
	// verdict. A rejected or unreached assessment records nothing.
	//
	// An unadmitted profile kind drops the point rather than labeling it, per
	// the ratified optional-dimension rule in semantic_dimensions.go. This is
	// unreachable in the ratified slice, whose only profiles are cm.v1 and
	// optimizer.v1; a lost point here means app grew a profile this package has
	// not admitted, which is a boundary defect to fix, not a value to invent.
	if phase == phaseAssessReadiness && (result == resultReady || result == resultNeedsInput) {
		if kind, ok := observedProfileKind(projection.Profile); ok {
			measurement.profile = kind
			measurement.verdict = verdictReady
			if result == resultNeedsInput {
				measurement.verdict = verdictNeedsInput
			}
		}
	}
	return measurement
}

// recordSemanticMeasurement records the bounded points for one completed
// phase. Every attribute value is a closed token produced by this package.
func (r *Runtime) recordSemanticMeasurement(ctx context.Context, measurement semanticMeasurement) {
	r.semanticPhaseDuration.Record(ctx, measurement.duration.Seconds(), metric.WithAttributes(
		attribute.String(dimensionPhase, measurement.phase.String()),
		attribute.String(dimensionResult, measurement.result.String()),
	))
	for _, operation := range measurement.operations {
		r.semanticOperations.Add(ctx, int64(operation.count), metric.WithAttributes(
			attribute.String(dimensionOperationKind, operation.kind.String()),
			attribute.String(dimensionResult, operation.result.String()),
		))
	}
	if measurement.checkpoint != checkpointNone {
		r.semanticCheckpoints.Add(ctx, 1, metric.WithAttributes(
			attribute.String(dimensionResult, measurement.checkpoint.String()),
		))
	}
	if measurement.invariantCode.present() && measurement.invariantCount > 0 {
		r.semanticInvariantFailures.Add(ctx, int64(measurement.invariantCount), metric.WithAttributes(
			attribute.String(dimensionInvariantCode, measurement.invariantCode.String()),
		))
	}
	if measurement.verdict != verdictNone {
		r.semanticAssessments.Add(ctx, 1, metric.WithAttributes(
			attribute.String(dimensionProfileKind, measurement.profile.String()),
			attribute.String(dimensionVerdict, measurement.verdict.String()),
		))
	}
}

func (r *Runtime) registerSemanticInstruments() error {
	meter := r.meterProvider.Meter(instrumentationName)
	var err error
	r.semanticPhaseDuration, err = meter.Float64Histogram(
		semanticPhaseDurationName,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of one completed semantic spine phase"),
	)
	if err != nil {
		return err
	}
	r.semanticOperations, err = meter.Int64Counter(
		semanticOperationsName,
		metric.WithUnit("operations"),
		metric.WithDescription("Structural operations of committed or atomically refused patches"),
	)
	if err != nil {
		return err
	}
	r.semanticCheckpoints, err = meter.Int64Counter(
		semanticCheckpointsName,
		metric.WithUnit("checkpoints"),
		metric.WithDescription("Sealed checkpoints and refused seal requests"),
	)
	if err != nil {
		return err
	}
	r.semanticInvariantFailures, err = meter.Int64Counter(
		semanticInvariantFailuresName,
		metric.WithUnit("failures"),
		metric.WithDescription("Failing protected invariant results produced by the spine"),
	)
	if err != nil {
		return err
	}
	r.semanticAssessments, err = meter.Int64Counter(
		semanticAssessmentsName,
		metric.WithUnit("assessments"),
		metric.WithDescription("Completed immutable readiness assessments"),
	)
	return err
}

// semanticMetricViews repeat the dimension allowlist inside the SDK so a
// future recording call cannot add an unbounded attribute. Views do not filter
// exemplar attributes; newMetricProvider separately disables exemplars.
func semanticMetricViews() []sdkmetric.View {
	allowed := map[string][]attribute.Key{
		semanticPhaseDurationName:     {dimensionPhase, dimensionResult},
		semanticOperationsName:        {dimensionOperationKind, dimensionResult},
		semanticCheckpointsName:       {dimensionResult},
		semanticInvariantFailuresName: {dimensionInvariantCode},
		semanticAssessmentsName:       {dimensionProfileKind, dimensionVerdict},
	}
	views := make([]sdkmetric.View, 0, len(allowed))
	for _, name := range []string{semanticPhaseDurationName, semanticOperationsName,
		semanticCheckpointsName, semanticInvariantFailuresName, semanticAssessmentsName} {
		stream := sdkmetric.Stream{
			AttributeFilter: attribute.NewAllowKeysFilter(allowed[name]...),
		}
		if name == semanticPhaseDurationName {
			stream.Aggregation = sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: semanticPhaseDurationBoundaries,
			}
		}
		views = append(views, sdkmetric.NewView(sdkmetric.Instrument{Name: name}, stream))
	}
	return views
}

// semanticPhaseDurationBoundaries are matched to what this instrument actually
// measures. Spine phases are in-process transformations over already-loaded
// state, and a measured run put the mean phase at 104 microseconds, so the
// scale of interest starts well below a millisecond.
//
// Leaving the aggregation unset would inherit the SDK's default boundaries,
// which begin [0, 5, 10, 25, ...]. Those are shaped for milliseconds, and
// against a seconds-valued instrument they collapse every real observation into
// a single bucket. That is not merely imprecise: an operator asking for p95 gets
// a confident answer several thousand times larger than the truth.
//
// The top of the range stays at ten seconds deliberately. A phase that slow
// means something is wrong in a way a percentile will not diagnose, and the
// bucket count is the cardinality this instrument pays for on every dimension
// combination.
var semanticPhaseDurationBoundaries = []float64{
	0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025,
	0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}
