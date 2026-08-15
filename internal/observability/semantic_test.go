package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Production break caught: naming a span from an app value rather than the
// observability-owned closed phase vocabulary would let a future app phase
// silently mint a new span name outside the ratified allowlist.
func TestSemanticObserverPassingSpanContract(t *testing.T) {
	runtime, spans, _ := newSemanticFixture(t)

	result, err := app.Run(t.Context(), requestFromFixture(t, teamhos.Passing), runtime.SemanticObserver())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status() != app.SpineSucceeded {
		t.Fatalf("status = %v, want succeeded", result.Status())
	}

	ended := spans.Ended()
	if got := spanNameCounts(ended); !mapsEqual(got, map[string]int{
		"maiden_lane.semantic.execute_spine":      1,
		"maiden_lane.semantic.compile":            1,
		"maiden_lane.semantic.execute_transition": 2,
		"maiden_lane.semantic.seal_checkpoint":    2,
		"maiden_lane.semantic.assess_readiness":   4,
	}) {
		t.Fatalf("span names = %v", got)
	}
	assertNoUnsetSpans(t, ended)
	for _, span := range ended {
		if span.Status().Code != codes.Ok {
			t.Errorf("span %q status = %v, want OK", span.Name(), span.Status())
		}
	}
	assertNoUnsetSpans(t, ended)
	assertSingleWellFormedTrace(t, ended)
	assertOnlyAdmittedSpanAttributes(t, ended)
}

// Production break caught: classifying readiness needs_input as a span error
// would page an operator for a correct, expected semantic answer.
func TestSemanticObserverNeedsInputIsOK(t *testing.T) {
	runtime, spans, _ := newSemanticFixture(t)

	if _, err := app.Run(t.Context(), requestFromFixture(t, teamhos.Passing), runtime.SemanticObserver()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	span := findSpan(t, spans.Ended(), "maiden_lane.semantic.assess_readiness", "optimizer.v1", "needs_input")
	if span.Status().Code != codes.Ok {
		t.Fatalf("status = %v, want OK", span.Status())
	}

	// Every readiness span carries its own checkpoint, profile, and verdict, and
	// none of the four is an operational error.
	got := map[string]int{}
	for _, ended := range spans.Ended() {
		if ended.Name() != "maiden_lane.semantic.assess_readiness" {
			continue
		}
		attributes := spanAttributeMap(ended)
		got[fmt.Sprintf("%s/%s/%s", attributes[attributeCheckpointKind],
			attributes[attributeProfileKind], attributes[attributeResult])]++
		if ended.Status().Code != codes.Ok {
			t.Errorf("readiness span %v status = %v, want OK", attributes, ended.Status())
		}
	}
	if !mapsEqual(got, map[string]int{
		"team_formed.v1/cm.v1/ready":                1,
		"team_formed.v1/optimizer.v1/needs_input":   1,
		"team_hos_aggregated.v1/cm.v1/ready":        1,
		"team_hos_aggregated.v1/optimizer.v1/ready": 1,
	}) {
		t.Fatalf("readiness spans = %v", got)
	}
}

func TestSemanticObserverPassingMetrics(t *testing.T) {
	runtime, _, reader := newSemanticFixture(t)

	if _, err := app.Run(t.Context(), requestFromFixture(t, teamhos.Passing), runtime.SemanticObserver()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	metrics := collectMetrics(t, reader)
	assertInstrumentUnits(t, metrics)

	assertHistogramPoints(t, metrics, semanticPhaseDurationName, map[string]uint64{
		"phase=compile,result=success":              1,
		"phase=execute_transition,result=success":   2,
		"phase=seal_checkpoint,result=success":      2,
		"phase=assess_readiness,result=ready":       3,
		"phase=assess_readiness,result=needs_input": 1,
		"phase=execute_spine,result=success":        1,
	})
	assertSumPoints(t, metrics, semanticOperationsName, map[string]int64{
		"operation_kind=insert,result=accepted": 1,
		"operation_kind=relate,result=accepted": 2,
		"operation_kind=update,result=accepted": 1,
	})
	assertSumPoints(t, metrics, semanticCheckpointsName, map[string]int64{"result=sealed": 2})
	assertSumPoints(t, metrics, semanticAssessmentsName, map[string]int64{
		"profile_kind=cm.v1,verdict=ready":              2,
		"profile_kind=optimizer.v1,verdict=needs_input": 1,
		"profile_kind=optimizer.v1,verdict=ready":       1,
	})
	if _, present := metrics[semanticInvariantFailuresName]; present {
		t.Fatalf("passing spine recorded an invariant failure: %#v", metrics[semanticInvariantFailuresName])
	}
}

// Production break caught: counting an unreached C2 as a rejected checkpoint,
// or an unmaterialized patch as a rejected update, would make telemetry claim
// artifacts that never existed.
func TestSemanticObserverAnchorMismatch(t *testing.T) {
	runtime, spans, reader := newSemanticFixture(t)

	result, err := app.Run(t.Context(), requestFromFixture(t, teamhos.AnchorMismatch), runtime.SemanticObserver())
	if err != nil {
		t.Fatalf("semantic rejection returned Go error: %v", err)
	}
	if result.Status() != app.SpineFailed {
		t.Fatalf("status = %v, want failed", result.Status())
	}

	ended := spans.Ended()
	if got := spanNameCounts(ended); !mapsEqual(got, map[string]int{
		"maiden_lane.semantic.execute_spine":      1,
		"maiden_lane.semantic.compile":            1,
		"maiden_lane.semantic.execute_transition": 2,
		"maiden_lane.semantic.seal_checkpoint":    1,
		"maiden_lane.semantic.assess_readiness":   2,
	}) {
		t.Fatalf("span names = %v", got)
	}
	rejected := findSpan(t, ended, "maiden_lane.semantic.execute_transition",
		"aggregate_team_hos.v1", "protected_invariant_failed")
	if rejected.Status().Code != codes.Error {
		t.Fatalf("rejected transition status = %v, want Error", rejected.Status())
	}
	if got := spanAttributeMap(rejected)[attributeCode]; got != "HOS_ANCHOR_MISMATCH" {
		t.Fatalf("code attribute = %#v, want HOS_ANCHOR_MISMATCH", got)
	}
	spine := findSpan(t, ended, "maiden_lane.semantic.execute_spine", "", "protected_invariant_failed")
	if spine.Status().Code != codes.Error {
		t.Fatalf("spine status = %v, want Error", spine.Status())
	}
	assertNoUnsetSpans(t, ended)
	assertSingleWellFormedTrace(t, ended)
	assertOnlyAdmittedSpanAttributes(t, ended)

	metrics := collectMetrics(t, reader)
	assertSumPoints(t, metrics, semanticOperationsName, map[string]int64{
		"operation_kind=insert,result=accepted": 1,
		"operation_kind=relate,result=accepted": 2,
	})
	assertSumPoints(t, metrics, semanticCheckpointsName, map[string]int64{"result=sealed": 1})
	assertSumPoints(t, metrics, semanticInvariantFailuresName,
		map[string]int64{"invariant_code=HOS_ANCHOR_MISMATCH": 1})
	assertSumPoints(t, metrics, semanticAssessmentsName, map[string]int64{
		"profile_kind=cm.v1,verdict=ready":              1,
		"profile_kind=optimizer.v1,verdict=needs_input": 1,
	})
	assertHistogramPoints(t, metrics, semanticPhaseDurationName, map[string]uint64{
		"phase=compile,result=success":                               1,
		"phase=execute_transition,result=success":                    1,
		"phase=execute_transition,result=protected_invariant_failed": 1,
		"phase=seal_checkpoint,result=success":                       1,
		"phase=assess_readiness,result=ready":                        1,
		"phase=assess_readiness,result=needs_input":                  1,
		"phase=execute_spine,result=protected_invariant_failed":      1,
	})
}

func TestSemanticObserverInvalidPlan(t *testing.T) {
	runtime, spans, reader := newSemanticFixture(t)

	result, err := app.Run(t.Context(), invalidPlanRequest(t, "driver.field_that_does_not_exist"), runtime.SemanticObserver())
	if err != nil {
		t.Fatalf("invalid plan returned Go error: %v", err)
	}
	if result.Status() != app.SpineInvalidPlan {
		t.Fatalf("status = %v, want invalid plan", result.Status())
	}

	ended := spans.Ended()
	if got := spanNameCounts(ended); !mapsEqual(got, map[string]int{
		"maiden_lane.semantic.execute_spine": 1,
		"maiden_lane.semantic.compile":       1,
	}) {
		t.Fatalf("span names = %v", got)
	}
	assertNoUnsetSpans(t, ended)
	compile := findSpan(t, ended, "maiden_lane.semantic.compile", "", "invalid_plan")
	if compile.Status().Code != codes.Error {
		t.Fatalf("compile status = %v, want Error", compile.Status())
	}
	if got := spanAttributeMap(compile)[attributeCode]; got != "UNKNOWN_FIELD" {
		t.Fatalf("code attribute = %#v, want UNKNOWN_FIELD", got)
	}
	assertOnlyAdmittedSpanAttributes(t, ended)

	metrics := collectMetrics(t, reader)
	assertHistogramPoints(t, metrics, semanticPhaseDurationName, map[string]uint64{
		"phase=compile,result=invalid_plan":       1,
		"phase=execute_spine,result=invalid_plan": 1,
	})
	for _, name := range []string{semanticOperationsName, semanticCheckpointsName,
		semanticInvariantFailuresName, semanticAssessmentsName} {
		if _, present := metrics[name]; present {
			t.Errorf("invalid plan recorded %q", name)
		}
	}
}

// Production break caught: echoing a rejected declaration's raw field path into
// a span attribute or metric dimension would republish hostile caller input as
// telemetry and blow out metric cardinality.
func TestSemanticObserverRejectsHostileInputInTelemetry(t *testing.T) {
	const hostile = "driver.hostile_<script>_ünicode_value"
	runtime, spans, reader := newSemanticFixture(t)

	if _, err := app.Run(t.Context(), invalidPlanRequest(t, hostile), runtime.SemanticObserver()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	needle := strings.TrimPrefix(hostile, "driver.")
	for _, span := range spans.Ended() {
		if strings.Contains(span.Name(), needle) {
			t.Errorf("span name leaked hostile input: %q", span.Name())
		}
		if strings.Contains(span.Status().Description, needle) {
			t.Errorf("span status leaked hostile input: %q", span.Status().Description)
		}
		for key, value := range spanAttributeMap(span) {
			if strings.Contains(fmt.Sprint(value), needle) {
				t.Errorf("span attribute %q leaked hostile input: %#v", key, value)
			}
		}
	}
	for name, measurement := range collectMetrics(t, reader) {
		for key, set := range attributeSets(t, measurement) {
			if strings.Contains(key, needle) {
				t.Errorf("metric %q leaked hostile input: %q %v", name, key, set)
			}
		}
	}
}

// Production break caught: a semantic result that differed between observers
// would make telemetry authoritative over meaning, violating Inviolate 17.
func TestSemanticObserverCannotChangeTheSemanticResult(t *testing.T) {
	recording, _, _ := newSemanticFixture(t)
	failingExporter := newFailingExporterFixture(t)
	disabled := newDisabledFixture(t)

	observers := map[string]app.Observer{
		"nil":              nil,
		"discard":          app.DiscardObserver(),
		"recording":        recording.SemanticObserver(),
		"failing_exporter": failingExporter.SemanticObserver(),
		"disabled_runtime": disabled.SemanticObserver(),
	}
	names := make([]string, 0, len(observers))
	for name := range observers {
		names = append(names, name)
	}
	sort.Strings(names)

	var reference string
	for _, name := range names {
		result, err := app.Run(t.Context(), requestFromFixture(t, teamhos.Passing), observers[name])
		if err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		projection := spineProjection(t, result)
		if reference == "" {
			reference = projection
			continue
		}
		if projection != reference {
			t.Fatalf("observer %q changed the semantic result:\n got %s\nwant %s", name, projection, reference)
		}
	}
}

// Production break caught: storing the phase stack on Runtime rather than per
// run would cross-parent concurrent runs and corrupt every trace.
func TestSemanticObserverIsolatesConcurrentRuns(t *testing.T) {
	runtime, spans, _ := newSemanticFixture(t)
	observer := runtime.SemanticObserver()

	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if _, err := app.Run(context.Background(), requestFromFixture(t, teamhos.Passing), observer); err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}
	group.Wait()

	traces := map[trace.TraceID][]sdktrace.ReadOnlySpan{}
	for _, span := range spans.Ended() {
		id := span.SpanContext().TraceID()
		traces[id] = append(traces[id], span)
	}
	if len(traces) != 4 {
		t.Fatalf("distinct traces = %d, want 4", len(traces))
	}
	for id, spansInTrace := range traces {
		if len(spansInTrace) != 10 {
			t.Errorf("trace %s has %d spans, want 10", id, len(spansInTrace))
		}
		assertSingleWellFormedTrace(t, spansInTrace)
	}
	if remaining := observer.(*semanticObserver).activeRuns(); remaining != 0 {
		t.Fatalf("observer retained %d run stacks after completion", remaining)
	}
}

// Production break caught: forwarding an unmapped app enum value would export
// its raw numeric representation as an unbounded telemetry dimension.
func TestSemanticDimensionMappingIsExhaustiveAndClosed(t *testing.T) {
	phases := map[app.Phase]string{
		app.PhaseCompile: "compile", app.PhaseExecuteTransition: "execute_transition",
		app.PhaseSealCheckpoint: "seal_checkpoint", app.PhaseAssessReadiness: "assess_readiness",
		app.PhaseExecuteSpine: "execute_spine",
	}
	for value, want := range phases {
		if got := observedPhase(value); got.String() != want {
			t.Errorf("observedPhase(%v) = %q, want %q", value, got, want)
		}
	}
	if got := observedPhase(app.Phase(200)); got.String() != "internal_error" {
		t.Errorf("unknown phase = %q, want internal_error", got)
	}

	results := map[app.PhaseResult]struct {
		token  string
		status codes.Code
	}{
		app.ResultSuccess:                   {"success", codes.Ok},
		app.ResultReady:                     {"ready", codes.Ok},
		app.ResultNeedsInput:                {"needs_input", codes.Ok},
		app.ResultInvalidPlan:               {"invalid_plan", codes.Error},
		app.ResultProtectedInvariantFailed:  {"protected_invariant_failed", codes.Error},
		app.ResultArtifactIntegrityFailed:   {"artifact_integrity_failed", codes.Error},
		app.ResultInvalidInput:              {"invalid_input", codes.Error},
		app.ResultCancelled:                 {"cancelled", codes.Error},
		app.ResultInfrastructureUnavailable: {"infrastructure_unavailable", codes.Error},
		app.ResultInternalError:             {"internal_error", codes.Error},
	}
	for value, want := range results {
		got := observedResult(value)
		if got.String() != want.token {
			t.Errorf("observedResult(%v) = %q, want %q", value, got, want.token)
		}
		if status := got.spanStatus(); status != want.status {
			t.Errorf("result %q status = %v, want %v", got, status, want.status)
		}
	}
	if got := observedResult(app.PhaseResult(200)); got.String() != "internal_error" {
		t.Errorf("unknown result = %q, want internal_error", got)
	}
	if got := observedResult(0); got.String() != "internal_error" {
		t.Errorf("absent result = %q, want internal_error", got)
	}

	kinds := map[app.ProfileKind]string{app.ProfileCM: "cm.v1", app.ProfileOptimizer: "optimizer.v1"}
	for value, want := range kinds {
		if got, ok := observedProfileKind(value); !ok || got.String() != want {
			t.Errorf("observedProfileKind(%v) = %q/%t, want %q/true", value, got, ok, want)
		}
	}
	if _, ok := observedProfileKind(app.ProfileKind(200)); ok {
		t.Error("unknown profile kind was admitted")
	}
	transitions := map[app.TransitionKind]string{
		app.TransitionFormTeam: "form_team.v1", app.TransitionAggregateTeamHOS: "aggregate_team_hos.v1",
	}
	for value, want := range transitions {
		if got, ok := observedTransitionKind(value); !ok || got.String() != want {
			t.Errorf("observedTransitionKind(%v) = %q/%t, want %q/true", value, got, ok, want)
		}
	}
	if _, ok := observedTransitionKind(app.TransitionKind(200)); ok {
		t.Error("unknown transition kind was admitted")
	}
	checkpoints := map[app.CheckpointKind]string{
		app.CheckpointTeamFormed: "team_formed.v1", app.CheckpointTeamHOSAggregated: "team_hos_aggregated.v1",
	}
	for value, want := range checkpoints {
		if got, ok := observedCheckpointKind(value); !ok || got.String() != want {
			t.Errorf("observedCheckpointKind(%v) = %q/%t, want %q/true", value, got, ok, want)
		}
	}
	if _, ok := observedCheckpointKind(app.CheckpointKind(200)); ok {
		t.Error("unknown checkpoint kind was admitted")
	}
}

// Production break caught: admitting a compilation-diagnostic or integrity code
// as an invariant-failure metric dimension would mix closed vocabularies and
// misreport which protected invariants actually failed.
func TestSemanticCodeMappingSeparatesSpanAndInvariantVocabularies(t *testing.T) {
	spanOnly := map[app.ObservationCode]string{
		app.CodeUnknownField: "UNKNOWN_FIELD", app.CodeUnsupportedOperator: "UNSUPPORTED_OPERATOR",
		app.CodeDeclaredAccessMismatch:  "DECLARED_ACCESS_MISMATCH",
		app.CodeWriteConflictUnresolved: "WRITE_CONFLICT_UNRESOLVED",
		app.CodeDependencyCycle:         "DEPENDENCY_CYCLE", app.CodeProfileOrderUnprovable: "PROFILE_ORDER_UNPROVABLE",
		app.CodeArtifactDigestMismatch:     "ARTIFACT_DIGEST_MISMATCH",
		app.CodeArtifactLinkInconsistent:   "ARTIFACT_LINK_INCONSISTENT",
		app.CodeAssessmentIdentityConflict: "ASSESSMENT_IDENTITY_CONFLICT",
		app.CodeReplayDivergence:           "REPLAY_DIVERGENCE",
	}
	invariants := map[app.ObservationCode]string{
		app.CodeOpEntityIdentityCollision:    "OP_ENTITY_IDENTITY_COLLISION",
		app.CodeOpUpdateTargetNotFound:       "OP_UPDATE_TARGET_NOT_FOUND",
		app.CodeOpBeforeImageMismatch:        "OP_BEFORE_IMAGE_MISMATCH",
		app.CodeOpRelationAlreadyPresent:     "OP_RELATION_ALREADY_PRESENT",
		app.CodeOpRelationEndpointMissing:    "OP_RELATION_ENDPOINT_MISSING",
		app.CodeDeclaredSourceNotFound:       "DECLARED_SOURCE_NOT_FOUND",
		app.CodeDeclaredSourceKindInvalid:    "DECLARED_SOURCE_KIND_INVALID",
		app.CodeTeamAssignmentKeyInvalid:     "TEAM_ASSIGNMENT_KEY_INVALID",
		app.CodeTeamAssignmentKeyMismatch:    "TEAM_ASSIGNMENT_KEY_MISMATCH",
		app.CodeTeamMemberCardinalityInvalid: "TEAM_MEMBER_CARDINALITY_INVALID",
		app.CodeHOSTupleIncomplete:           "HOS_TUPLE_INCOMPLETE", app.CodeHOSDurationInvalid: "HOS_DURATION_INVALID",
		app.CodeHOSAnchorMismatch: "HOS_ANCHOR_MISMATCH", app.CodeHOSAggregateInvalid: "HOS_AGGREGATE_INVALID",
	}
	for value, want := range spanOnly {
		got, ok := observedCode(value)
		if !ok || got.String() != want {
			t.Errorf("observedCode(%v) = %q/%t, want %q/true", value, got, ok, want)
		}
		if _, ok := observedInvariantCode(value); ok {
			t.Errorf("observedInvariantCode admitted non-invariant code %q", want)
		}
	}
	for value, want := range invariants {
		got, ok := observedCode(value)
		if !ok || got.String() != want {
			t.Errorf("observedCode(%v) = %q/%t, want %q/true", value, got, ok, want)
		}
		invariant, ok := observedInvariantCode(value)
		if !ok || invariant.String() != want {
			t.Errorf("observedInvariantCode(%v) = %q/%t, want %q/true", value, invariant, ok, want)
		}
	}
	if _, ok := observedCode(app.ObservationCode(200)); ok {
		t.Error("unknown code was admitted to spans")
	}
	if _, ok := observedCode(0); ok {
		t.Error("absent code was admitted to spans")
	}
	if _, ok := observedInvariantCode(app.ObservationCode(200)); ok {
		t.Error("unknown code was admitted to invariant metrics")
	}
}

// Production break caught: an atomically rejected materialized patch whose
// proposed operations were not projected as rejected, or a refused seal that
// recorded no rejected checkpoint, would hide fail-closed behavior from
// operators. The complete injected observation sequences behind these
// projections are proved by internal/app package tests; this asserts the
// observability half records the exact points.
func TestSemanticMetricRecordingFromNormalizedDimensions(t *testing.T) {
	runtime, _, reader := newSemanticFixture(t)
	ctx := t.Context()

	rejectedCode, ok := observedInvariantCode(app.CodeOpEntityIdentityCollision)
	if !ok {
		t.Fatal("OP_ENTITY_IDENTITY_COLLISION is not an invariant dimension")
	}
	runtime.recordSemanticMeasurement(ctx, semanticMeasurement{
		phase:    observedPhase(app.PhaseExecuteTransition),
		result:   observedResult(app.ResultProtectedInvariantFailed),
		duration: 3 * time.Millisecond,
		operations: []operationCount{
			{kind: operationInsert, result: operationRejected, count: 1},
			{kind: operationRelate, result: operationRejected, count: 2},
		},
		invariantCode:  rejectedCode,
		invariantCount: 1,
	})
	runtime.recordSemanticMeasurement(ctx, semanticMeasurement{
		phase:      observedPhase(app.PhaseSealCheckpoint),
		result:     observedResult(app.ResultArtifactIntegrityFailed),
		duration:   time.Millisecond,
		checkpoint: checkpointRejected,
	})

	metrics := collectMetrics(t, reader)
	assertSumPoints(t, metrics, semanticOperationsName, map[string]int64{
		"operation_kind=insert,result=rejected": 1,
		"operation_kind=relate,result=rejected": 2,
	})
	assertSumPoints(t, metrics, semanticCheckpointsName, map[string]int64{"result=rejected": 1})
	assertSumPoints(t, metrics, semanticInvariantFailuresName,
		map[string]int64{"invariant_code=OP_ENTITY_IDENTITY_COLLISION": 1})
	assertHistogramPoints(t, metrics, semanticPhaseDurationName, map[string]uint64{
		"phase=execute_transition,result=protected_invariant_failed": 1,
		"phase=seal_checkpoint,result=artifact_integrity_failed":     1,
	})
	if _, present := metrics[semanticAssessmentsName]; present {
		t.Error("rejection recorded a readiness assessment")
	}
}

// Production break caught: substituting a placeholder for an unadmitted
// optional dimension would put a value outside the ratified exact-value list
// into a bounded vocabulary and assert a classification the spine never made.
// The ratified rule is that optional dimensions fail closed by omission while
// the always-required phase and result fall back to internal_error.
func TestSemanticOptionalDimensionsFailClosedByOmission(t *testing.T) {
	runtime, _, reader := newSemanticFixture(t)

	measurement := semanticMeasurementFor(app.MetricObservation{
		Phase:   app.PhaseAssessReadiness,
		Result:  app.ResultReady,
		Profile: app.ProfileKind(200),
	}, time.Millisecond)
	if measurement.verdict != verdictNone || measurement.profile != 0 {
		t.Fatalf("unadmitted profile was labeled: %+v", measurement)
	}
	runtime.recordSemanticMeasurement(t.Context(), measurement)

	metrics := collectMetrics(t, reader)
	if _, present := metrics[semanticAssessmentsName]; present {
		t.Errorf("unadmitted profile kind still recorded an assessment: %#v", metrics[semanticAssessmentsName])
	}
	// The always-required dimensions still resolve, so the phase is never lost.
	assertHistogramPoints(t, metrics, semanticPhaseDurationName,
		map[string]uint64{"phase=assess_readiness,result=ready": 1})

	// The required dimensions behave the other way. PhaseObservation has no
	// public constructor, so a zero-valued carrier is the only unadmitted phase
	// this package can be handed; it must still produce a named, explicitly
	// failed span and a duration point rather than vanish.
	tripwire, tripwireSpans, tripwireReader := newSemanticFixture(t)
	observer := tripwire.SemanticObserver()
	observer.BeginPhase(t.Context(), app.PhaseObservation{})
	observer.EndPhase(t.Context(), app.PhaseObservation{})

	ended := tripwireSpans.Ended()
	if len(ended) != 1 {
		t.Fatalf("unadmitted phase produced %d spans, want 1", len(ended))
	}
	if ended[0].Name() != "maiden_lane.semantic.internal_error" {
		t.Errorf("span name = %q, want the internal_error tripwire", ended[0].Name())
	}
	if ended[0].Status().Code != codes.Error {
		t.Errorf("status = %v, want Error", ended[0].Status())
	}
	assertHistogramPoints(t, collectMetrics(t, tripwireReader), semanticPhaseDurationName,
		map[string]uint64{"phase=internal_error,result=internal_error": 1})
}

// Production break caught: an unmatched or out-of-order end event that left a
// span open would leak the span and its parent stack entry for the process's
// lifetime.
func TestSemanticObserverRetainsNoStateAfterUnbalancedEvents(t *testing.T) {
	runtime, spans, _ := newSemanticFixture(t)
	observer := runtime.SemanticObserver().(*semanticObserver)
	ctx := context.Background()

	// An end event for a phase that never began is ignored entirely.
	observer.EndPhase(ctx, app.PhaseObservation{})
	if got := len(spans.Ended()); got != 0 {
		t.Fatalf("unmatched end produced %d spans", got)
	}
	if got := observer.activeRuns(); got != 0 {
		t.Fatalf("unmatched end retained %d run stacks", got)
	}
}

func newSemanticFixture(t *testing.T) (*Runtime, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
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
		sdkmetric.WithView(semanticMetricViews()...),
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
	if err := runtime.registerSemanticInstruments(); err != nil {
		t.Fatalf("register semantic instruments: %v", err)
	}
	return runtime, spanRecorder, metricReader
}

// newFailingExporterFixture builds a runtime whose span exporter fails
// asynchronously inside the batch processor, well after the observer returns.
func newFailingExporterFixture(t *testing.T) *Runtime {
	t.Helper()
	exporter := &failingSpanExporter{err: errors.New("exporter unavailable")}
	processor := sdktrace.NewBatchSpanProcessor(exporter, sdktrace.WithBatchTimeout(time.Millisecond))
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader),
		sdkmetric.WithResource(resource.Empty()))
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider Shutdown: %v", err)
		}
	})
	runtime := &Runtime{tracerProvider: tracerProvider, meterProvider: meterProvider,
		propagator: propagation.TraceContext{}}
	if err := runtime.registerSemanticInstruments(); err != nil {
		t.Fatalf("register semantic instruments: %v", err)
	}
	return runtime
}

// newDisabledFixture builds the production-shaped runtime with telemetry
// disabled: its noop providers must make the observer completely harmless.
func newDisabledFixture(t *testing.T) *Runtime {
	t.Helper()
	cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	runtime, err := newRuntime(t.Context(), cfg, io.Discard, factories{})
	if err != nil {
		t.Fatalf("newRuntime: %v", err)
	}
	return runtime
}

func requestFromFixture(t *testing.T, variant teamhos.Variant) app.Request {
	t.Helper()
	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	return app.Request{
		Compilation:      inputs.Compilation,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	}
}

// invalidPlanRequest declares an undeclarable read so compilation rejects with
// UNKNOWN_FIELD while carrying the caller's raw field path in its diagnostic.
func invalidPlanRequest(t *testing.T, field semantic.FieldPath) app.Request {
	t.Helper()
	request := requestFromFixture(t, teamhos.Passing)
	for i, transformation := range request.Compilation.Rules.Transformations {
		if transformation.ID == teamhos.RuleFormTeam {
			reads := slices.Clone(transformation.DeclaredReads)
			request.Compilation.Rules.Transformations[i].DeclaredReads = append(reads, field)
		}
	}
	return request
}

// spineProjection renders the canonical, observer-independent content of a
// spine result: identities and digests only, never wall-clock or telemetry.
func spineProjection(t *testing.T, result app.SpineResult) string {
	t.Helper()
	var builder strings.Builder
	fmt.Fprintf(&builder, "status=%s", result.Status())
	if status, ok := result.ExecutionStatus(); ok {
		fmt.Fprintf(&builder, " execution=%s", status)
	}
	if plan, ok := result.Plan(); ok {
		fmt.Fprintf(&builder, " plan=%s", plan.ID())
	}
	if state, ok := result.State(); ok {
		fmt.Fprintf(&builder, " state=%s", state.Digest())
	}
	for _, profile := range result.Profiles() {
		fmt.Fprintf(&builder, " profile=%s", profile.ID())
	}
	for _, checkpoint := range result.Checkpoints() {
		fmt.Fprintf(&builder, " checkpoint=%s/%s", checkpoint.ID(), checkpoint.Digest())
	}
	for _, assessment := range result.Assessments() {
		fmt.Fprintf(&builder, " assessment=%s/%s/%s",
			assessment.ID(), assessment.Digest(), assessment.Verdict())
	}
	if failure, ok := result.SemanticFailure(); ok {
		fmt.Fprintf(&builder, " failure=%s", failure.Kind())
	}
	return builder.String()
}

func spanNameCounts(spans []sdktrace.ReadOnlySpan) map[string]int {
	counts := map[string]int{}
	for _, span := range spans {
		counts[span.Name()]++
	}
	return counts
}

func mapsEqual(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

// findSpan locates the unique ended span with the given name, optional bounded
// kind attribute value, and result attribute.
func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name, kind, result string) sdktrace.ReadOnlySpan {
	t.Helper()
	var found []sdktrace.ReadOnlySpan
	for _, span := range spans {
		if span.Name() != name {
			continue
		}
		attributes := spanAttributeMap(span)
		if attributes[attributeResult] != result {
			continue
		}
		if kind != "" && attributes[attributeProfileKind] != kind &&
			attributes[attributeTransitionKind] != kind && attributes[attributeCheckpointKind] != kind {
			continue
		}
		found = append(found, span)
	}
	if len(found) != 1 {
		t.Fatalf("span %q kind=%q result=%q matched %d spans", name, kind, result, len(found))
	}
	return found[0]
}

// assertNoUnsetSpans enforces the ratified rule that no terminal phase is left
// with an implicit UNSET status.
func assertNoUnsetSpans(t *testing.T, spans []sdktrace.ReadOnlySpan) {
	t.Helper()
	for _, span := range spans {
		if span.Status().Code == codes.Unset {
			t.Errorf("span %q left UNSET", span.Name())
		}
	}
}

// assertSingleWellFormedTrace requires exactly one root execute_spine span with
// every other span parented inside that run's own stack.
func assertSingleWellFormedTrace(t *testing.T, spans []sdktrace.ReadOnlySpan) {
	t.Helper()
	known := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		known[span.SpanContext().SpanID()] = span
	}
	roots := 0
	for _, span := range spans {
		parent := span.Parent()
		if !parent.IsValid() {
			roots++
			if span.Name() != "maiden_lane.semantic.execute_spine" {
				t.Errorf("root span is %q, want execute_spine", span.Name())
			}
			continue
		}
		outer, ok := known[parent.SpanID()]
		if !ok {
			t.Errorf("span %q has a parent outside this run", span.Name())
			continue
		}
		if outer.SpanContext().TraceID() != span.SpanContext().TraceID() {
			t.Errorf("span %q crossed traces", span.Name())
		}
		// Every phase closes before the next begins, so the whole run nests
		// directly under its own outer spine span.
		if outer.Name() != "maiden_lane.semantic.execute_spine" {
			t.Errorf("span %q parent = %q, want execute_spine", span.Name(), outer.Name())
		}
	}
	if roots != 1 {
		t.Errorf("root spans = %d, want 1", roots)
	}
}

// assertOnlyAdmittedSpanAttributes enforces the ratified trace allowlist: every
// key is admitted and every closed-vocabulary value is a ratified token.
func assertOnlyAdmittedSpanAttributes(t *testing.T, spans []sdktrace.ReadOnlySpan) {
	t.Helper()
	vocabularies := map[string][]string{
		attributePhase: {"compile", "execute_transition", "seal_checkpoint", "assess_readiness",
			"execute_spine", "internal_error"},
		attributeResult: {"success", "ready", "needs_input", "invalid_plan", "protected_invariant_failed",
			"artifact_integrity_failed", "invalid_input", "cancelled", "infrastructure_unavailable",
			"internal_error"},
		attributeTransitionKind: {"form_team.v1", "aggregate_team_hos.v1"},
		attributeCheckpointKind: {"team_formed.v1", "team_hos_aggregated.v1"},
		attributeProfileKind:    {"cm.v1", "optimizer.v1"},
	}
	identities := map[string]bool{attributePlanID: true, attributeRunID: true, attributeExecutionID: true}
	counts := map[string]bool{
		attributeAcceptedInserts: true, attributeAcceptedRelates: true, attributeAcceptedUpdates: true,
		attributeRejectedInserts: true, attributeRejectedRelates: true, attributeRejectedUpdates: true,
		attributeInvariantFailures: true,
	}
	for _, span := range spans {
		for key, value := range spanAttributeMap(span) {
			switch {
			case vocabularies[key] != nil:
				token, ok := value.(string)
				if !ok || !slices.Contains(vocabularies[key], token) {
					t.Errorf("span %q attribute %q = %#v is outside its closed vocabulary", span.Name(), key, value)
				}
			case key == attributeCode:
				token, ok := value.(string)
				if !ok || token != strings.ToUpper(token) || token == "" {
					t.Errorf("span %q code attribute = %#v is not a closed code token", span.Name(), value)
				}
			case identities[key]:
				token, ok := value.(string)
				if !ok || !strings.HasPrefix(token, "sha256:") {
					t.Errorf("span %q identity attribute %q = %#v", span.Name(), key, value)
				}
			case counts[key]:
				number, ok := value.(int64)
				if !ok || number <= 0 {
					t.Errorf("span %q count attribute %q = %#v", span.Name(), key, value)
				}
			default:
				t.Errorf("span %q carries unadmitted attribute %q = %#v", span.Name(), key, value)
			}
		}
	}
}

func assertInstrumentUnits(t *testing.T, metrics map[string]metricdata.Metrics) {
	t.Helper()
	units := map[string]string{
		semanticPhaseDurationName:     "s",
		semanticOperationsName:        "operations",
		semanticCheckpointsName:       "checkpoints",
		semanticInvariantFailuresName: "failures",
		semanticAssessmentsName:       "assessments",
	}
	for name, unit := range units {
		measurement, present := metrics[name]
		if !present {
			continue
		}
		if measurement.Unit != unit {
			t.Errorf("instrument %q unit = %q, want %q", name, measurement.Unit, unit)
		}
	}
}

// attributeSets renders a measurement's data points keyed by their canonical
// sorted attribute rendering.
func attributeSets(t *testing.T, measurement metricdata.Metrics) map[string]any {
	t.Helper()
	points := map[string]any{}
	switch data := measurement.Data.(type) {
	case metricdata.Sum[int64]:
		for _, point := range data.DataPoints {
			points[renderAttributes(point.Attributes)] = point.Value
		}
	case metricdata.Histogram[float64]:
		for _, point := range data.DataPoints {
			points[renderAttributes(point.Attributes)] = point.Count
		}
	default:
		t.Fatalf("metric %q has unexpected data type %T", measurement.Name, measurement.Data)
	}
	return points
}

func assertSumPoints(t *testing.T, metrics map[string]metricdata.Metrics, name string, want map[string]int64) {
	t.Helper()
	measurement, present := metrics[name]
	if !present {
		t.Fatalf("instrument %q recorded no points, want %v", name, want)
	}
	data, ok := measurement.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("instrument %q data = %T, want Sum[int64]", name, measurement.Data)
	}
	got := map[string]int64{}
	for _, point := range data.DataPoints {
		got[renderAttributes(point.Attributes)] = point.Value
	}
	if len(got) != len(want) {
		t.Fatalf("instrument %q points = %v, want %v", name, got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("instrument %q point %q = %d, want %d (all: %v)", name, key, got[key], value, got)
		}
	}
}

func assertHistogramPoints(t *testing.T, metrics map[string]metricdata.Metrics, name string, want map[string]uint64) {
	t.Helper()
	measurement, present := metrics[name]
	if !present {
		t.Fatalf("instrument %q recorded no points, want %v", name, want)
	}
	data, ok := measurement.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("instrument %q data = %T, want Histogram[float64]", name, measurement.Data)
	}
	got := map[string]uint64{}
	for _, point := range data.DataPoints {
		got[renderAttributes(point.Attributes)] = point.Count
	}
	if len(got) != len(want) {
		t.Fatalf("instrument %q points = %v, want %v", name, got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("instrument %q point %q count = %d, want %d (all: %v)", name, key, got[key], value, got)
		}
	}
}

func renderAttributes(set attribute.Set) string {
	parts := make([]string, 0, set.Len())
	for _, value := range set.ToSlice() {
		parts = append(parts, fmt.Sprintf("%s=%s", value.Key, value.Value.AsString()))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
