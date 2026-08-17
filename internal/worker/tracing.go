package worker

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// ExecutionTracer observes the worker's handling of one claimed execution.
//
// It exists because of an asymmetry that only appeared once traces were read
// rather than asserted. Spans attach to whatever is already in the context they
// are given, and the spine's observer is forbidden from replacing that context,
// so an execution driven over HTTP nests under the request span while one driven
// by the worker had nothing above it. A single logical execution produced one
// trace for the 202 that queued it and a second, rootless trace for the work
// itself, with no path between them.
//
// Unlike app.Observer this returns a context, and that is its entire purpose:
// putting a span in scope is what gives the spine's phases a root and the
// worker's log records something to correlate to. It stays non-authoritative in
// the same sense as app.Observer -- it cannot alter what the worker does, what
// the spine decides, or any identity -- and a nil tracer is supported.
type ExecutionTracer interface {
	BeginExecution(context.Context, ExecutionObservation) (context.Context, func(ExecutionOutcome))
}

// ExecutionObservation identifies the claimed execution being processed.
//
// It carries the same three identities the spine's phase spans already carry so
// that one query can match the worker span and everything beneath it. It
// deliberately carries no tenant: the spine's spans do not, and widening that is
// a privacy decision rather than a plumbing one.
type ExecutionObservation struct {
	PlanID      semantic.PlanID
	RunID       semantic.SemanticRunID
	ExecutionID semantic.ExecutionID
}

// OutcomeKind is the closed vocabulary for how the worker finished with a
// claimed execution. It is the distinction this package is built around, stated
// once so telemetry cannot describe an execution differently from the store.
type OutcomeKind uint8

const (
	// OutcomeAnswered means the spine produced a result and it was stored. A
	// deterministic semantic refusal is answered rather than failed: the
	// computation reached a real conclusion, and repeating it reproduces that
	// conclusion exactly.
	OutcomeAnswered OutcomeKind = iota + 1
	// OutcomeAbandoned means the execution was left claimable, so an expired
	// lease brings it back. It is not an error state, and it is deliberately
	// distinct from failure: an operator reading it should expect the work to
	// happen, not to need intervention.
	OutcomeAbandoned
	// OutcomeFailed means a terminal failure was actually recorded against the
	// execution and it will not be retried.
	OutcomeFailed
)

// Bounded failure reasons. Each is a closed token: an operator sees why an
// execution stopped without any payload, identity, or dependency text.
//
// These are exported because ExecutionOutcome.Reason is part of this package's
// surface, and a bounded field whose permitted values are unnameable outside the
// package is bounded in comment only.
const (
	ReasonPlanAbsent       = "plan_absent"
	ReasonIdentityMismatch = "identity_mismatch"
	ReasonInvalidInput     = "invalid_semantic_input"
	ReasonInternalError    = "internal_error"
)

// ExecutionOutcome is what the worker did with one claimed execution. Reason is
// one of the bounded failure tokens above and is empty unless Kind is
// OutcomeFailed.
//
// The outcome reports what was recorded rather than what was intended. An
// intended failure that could not be written is abandoned, because the execution
// is still claimable and calling it failed would describe a state no reader can
// observe.
type ExecutionOutcome struct {
	Kind   OutcomeKind
	Reason string
}
