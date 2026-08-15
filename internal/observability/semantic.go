package observability

import (
	"context"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/optimaldynamics/maiden-lane/internal/app"
)

// This file implements app.Observer with the process OpenTelemetry runtime.
// The dependency direction is one-way: internal/app owns the closed observation
// carrier and never imports this package, so telemetry cannot reach into
// semantic meaning. The adapter is non-authoritative by construction: it
// returns nothing, never replaces the context handed to semantic code, and
// cannot change a verdict, the verified frontier, or the returned error.

const semanticSpanPrefix = "maiden_lane.semantic."

// Span attribute keys. Unlike the ratified metric dimensions these are
// namespaced, because one trace mixes HTTP and semantic spans and a bare
// "phase" or "result" key would collide with other instrumentation.
const (
	attributePhase             = semanticSpanPrefix + "phase"
	attributeResult            = semanticSpanPrefix + "result"
	attributeCode              = semanticSpanPrefix + "code"
	attributePlanID            = semanticSpanPrefix + "plan_id"
	attributeRunID             = semanticSpanPrefix + "run_id"
	attributeExecutionID       = semanticSpanPrefix + "execution_id"
	attributeTransitionKind    = semanticSpanPrefix + "transition_kind"
	attributeCheckpointKind    = semanticSpanPrefix + "checkpoint_kind"
	attributeProfileKind       = semanticSpanPrefix + "profile_kind"
	attributeAcceptedInserts   = semanticSpanPrefix + "accepted.inserts"
	attributeAcceptedRelates   = semanticSpanPrefix + "accepted.relates"
	attributeAcceptedUpdates   = semanticSpanPrefix + "accepted.updates"
	attributeRejectedInserts   = semanticSpanPrefix + "rejected.inserts"
	attributeRejectedRelates   = semanticSpanPrefix + "rejected.relates"
	attributeRejectedUpdates   = semanticSpanPrefix + "rejected.updates"
	attributeInvariantFailures = semanticSpanPrefix + "invariant_failures"
)

// SemanticObserver returns a fresh app.Observer for the semantic spine. Each
// call yields an independent adapter; Runtime itself holds no per-run state,
// so an adapter can serve concurrent runs and none can outlive its runtime.
func (r *Runtime) SemanticObserver() app.Observer {
	return &semanticObserver{runtime: r, runs: map[context.Context][]phaseSpan{}}
}

// semanticObserver keeps one span stack per in-flight run. app.Run derives one
// private context per invocation and passes only that context to observer
// calls, so the context value itself is this adapter's run key. It is never
// used to reach semantic code or to carry data back.
type semanticObserver struct {
	runtime *Runtime

	mu   sync.Mutex
	runs map[context.Context][]phaseSpan
}

// phaseSpan is one open phase: its closed phase, the span, the context that
// parents nested phases, and an operational start reading. time.Now carries a
// monotonic component, so the recorded duration is unaffected by wall-clock
// adjustment; no semantic value ever observes this clock.
type phaseSpan struct {
	phase observationPhase
	ctx   context.Context
	span  trace.Span
	start time.Time
}

// BeginPhase opens a span parented by this run's currently innermost phase.
// It deliberately discards the context OTel returns for anything except
// parenting nested phases: the context handed to semantic code never changes.
func (o *semanticObserver) BeginPhase(ctx context.Context, observation app.PhaseObservation) {
	phase := observedPhase(observation.Phase())

	o.mu.Lock()
	defer o.mu.Unlock()

	parent := ctx
	if stack := o.runs[ctx]; len(stack) > 0 {
		parent = stack[len(stack)-1].ctx
	}
	spanCtx, span := o.runtime.tracerProvider.Tracer(instrumentationName).Start(
		parent,
		semanticSpanPrefix+phase.String(),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(spanAttributes(observation, phase, 0)...),
	)
	o.runs[ctx] = append(o.runs[ctx], phaseSpan{phase: phase, ctx: spanCtx, span: span, start: time.Now()})
}

// EndPhase pops the matching phase, applies an explicit status and the closed
// attribute set, ends the span, and records the bounded metric points.
func (o *semanticObserver) EndPhase(ctx context.Context, observation app.PhaseObservation) {
	phase := observedPhase(observation.Phase())
	appResult, _ := observation.Result()
	result := observedResult(appResult)

	o.mu.Lock()
	stack := o.runs[ctx]
	index := -1
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].phase == phase {
			index = i
			break
		}
	}
	if index < 0 {
		// Nothing began for this phase. Under the app contract this cannot
		// happen; recording a phase that never started would invent telemetry,
		// so the event is dropped rather than guessed at.
		o.mu.Unlock()
		return
	}
	// Any still-open inner phase was abandoned. Ending it here keeps the
	// adapter leak-free and leaves no span UNSET. The copy matters: the
	// retained stack shares this backing array, so a later push for the same
	// run would otherwise overwrite an entry still waiting to be ended.
	abandoned := slices.Clone(stack[index+1:])
	entry := stack[index]
	if remaining := stack[:index]; len(remaining) == 0 {
		delete(o.runs, ctx)
	} else {
		o.runs[ctx] = remaining
	}
	o.mu.Unlock()

	for i := len(abandoned) - 1; i >= 0; i-- {
		abandoned[i].span.SetStatus(codes.Error, resultInternalError.String())
		abandoned[i].span.End()
	}

	entry.span.SetAttributes(spanAttributes(observation, phase, result)...)
	if status := result.spanStatus(); status == codes.Ok {
		entry.span.SetStatus(status, "")
	} else {
		entry.span.SetStatus(status, result.String())
	}
	entry.span.End()

	o.runtime.recordSemanticMeasurement(ctx,
		semanticMeasurementFor(observation.MetricProjection(), time.Since(entry.start)))
}

// activeRuns reports how many run stacks are open. It exists so tests can
// prove the adapter retains nothing after a run completes.
func (o *semanticObserver) activeRuns() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.runs)
}

// spanAttributes renders the complete ratified trace allowlist: the three
// observed identities, bounded kinds, one closed code, the phase, the terminal
// result, and non-negative bounded counts. Nothing else is representable.
func spanAttributes(observation app.PhaseObservation, phase observationPhase, result observationResult) []attribute.KeyValue {
	attributes := []attribute.KeyValue{attribute.String(attributePhase, phase.String())}
	if result != 0 {
		attributes = append(attributes, attribute.String(attributeResult, result.String()))
	}
	if planID, ok := observation.PlanID(); ok {
		attributes = append(attributes, attribute.String(attributePlanID, string(planID)))
	}
	if runID, ok := observation.SemanticRunID(); ok {
		attributes = append(attributes, attribute.String(attributeRunID, string(runID)))
	}
	if executionID, ok := observation.ExecutionID(); ok {
		attributes = append(attributes, attribute.String(attributeExecutionID, string(executionID)))
	}
	if kind, ok := observation.Transition(); ok {
		if bounded, admitted := observedTransitionKind(kind); admitted {
			attributes = append(attributes, attribute.String(attributeTransitionKind, bounded.String()))
		}
	}
	if kind, ok := observation.Checkpoint(); ok {
		if bounded, admitted := observedCheckpointKind(kind); admitted {
			attributes = append(attributes, attribute.String(attributeCheckpointKind, bounded.String()))
		}
	}
	if kind, ok := observation.Profile(); ok {
		if bounded, admitted := observedProfileKind(kind); admitted {
			attributes = append(attributes, attribute.String(attributeProfileKind, bounded.String()))
		}
	}
	if code, ok := observation.Code(); ok {
		if bounded, admitted := observedCode(code); admitted {
			attributes = append(attributes, attribute.String(attributeCode, bounded.String()))
		}
	}
	counts := observation.MetricProjection()
	for _, count := range []struct {
		key   string
		value uint64
	}{
		{attributeAcceptedInserts, counts.AcceptedInserts},
		{attributeAcceptedRelates, counts.AcceptedRelates},
		{attributeAcceptedUpdates, counts.AcceptedUpdates},
		{attributeRejectedInserts, counts.RejectedInserts},
		{attributeRejectedRelates, counts.RejectedRelates},
		{attributeRejectedUpdates, counts.RejectedUpdates},
		{attributeInvariantFailures, counts.InvariantFailures},
	} {
		if count.value > 0 {
			attributes = append(attributes, attribute.Int64(count.key, int64(count.value)))
		}
	}
	return attributes
}
