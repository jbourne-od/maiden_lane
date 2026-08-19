package app

import (
	"context"
	"errors"
)

// InvalidInputCode is the closed classification for malformed or incomplete
// canonical input detected at the initial application request boundary. Once
// an execution is established, deterministic malformed, mismatched, or
// corrupt semantic artifacts are ARTIFACT_INTEGRITY_FAILED semantic results,
// never this error.
type InvalidInputCode string

const (
	InputCompilationRequestIncomplete InvalidInputCode = "COMPILATION_REQUEST_INCOMPLETE"
	InputRunBindingIncomplete         InvalidInputCode = "RUN_BINDING_INPUT_INCOMPLETE"

	// InputPublishRequestIncomplete means a publish request was missing a key
	// part or a piece of evidence, so no auditable record could be produced.
	InputPublishRequestIncomplete InvalidInputCode = "PUBLISH_REQUEST_INCOMPLETE"

	// InputPublishReceiptMismatch means the supplied execution receipt is not for
	// the checkpoint being published.
	//
	// It is a separate code from the one above for the same reason the gate has two
	// unevaluated reasons: both refuse, and they call for different action. One is
	// answered by supplying the missing piece, the other by correcting a pairing
	// that is complete and wrong.
	InputPublishReceiptMismatch InvalidInputCode = "PUBLISH_RECEIPT_MISMATCH"

	// InputCorpusRunIncomplete means a corpus run request was missing a key part.
	InputCorpusRunIncomplete InvalidInputCode = "CORPUS_RUN_INCOMPLETE"

	// InputCorpusAbsent and InputCorpusRunPlanAbsent mean the named corpus or plan does
	// not exist for this tenant. They are separate codes because an operator has to know
	// which of the two names was wrong, and a single "not found" would send them to check
	// both.
	InputCorpusAbsent        InvalidInputCode = "CORPUS_ABSENT"
	InputCorpusRunPlanAbsent InvalidInputCode = "CORPUS_RUN_PLAN_ABSENT"

	// InputCorpusSchemaMismatch means the corpus's cases are not under the plan's schema,
	// so no case could execute under it.
	InputCorpusSchemaMismatch InvalidInputCode = "CORPUS_SCHEMA_MISMATCH"

	// InputComparisonIncomplete means an assemble-comparison request was missing a key
	// part, and InputComparisonWorldMismatch means the supplied world is not the one the
	// comparison was identified under.
	InputComparisonIncomplete    InvalidInputCode = "COMPARISON_INCOMPLETE"
	InputComparisonWorldMismatch InvalidInputCode = "COMPARISON_WORLD_MISMATCH"
)

// InvalidInputError reports malformed or unsupported canonical input at the
// initial request boundary. Its text is fixed and safe: no payload,
// identifier, path, or raw dependency text ever appears.
type InvalidInputError struct {
	Code InvalidInputCode
}

// Error returns fixed safe text plus the closed code token only.
func (e InvalidInputError) Error() string {
	return "app: invalid canonical input: " + string(e.Code)
}

// InfrastructureCode is the closed classification for required application
// infrastructure that is unavailable. This in-memory slice defines the
// vocabulary; no production dependency exists yet.
type InfrastructureCode string

const InfrastructureDependencyUnavailable InfrastructureCode = "REQUIRED_DEPENDENCY_UNAVAILABLE"

// InfrastructureUnavailableError reports a required application-boundary
// dependency failure. The cause is preserved for errors.Is/errors.As but is
// never rendered into this error's own fixed safe text.
type InfrastructureUnavailableError struct {
	Code  InfrastructureCode
	Cause error
}

// Error returns fixed safe text plus the closed code token only.
func (e InfrastructureUnavailableError) Error() string {
	return "app: required infrastructure unavailable: " + string(e.Code)
}

// Unwrap exposes the operational cause for errors.Is and errors.As.
func (e InfrastructureUnavailableError) Unwrap() error { return e.Cause }

// machineryError wraps any machinery-failure cause with fixed safe text
// naming only the closed phase. Unwrap preserves the full cause chain so
// errors.Is(context.Canceled), errors.Is(context.DeadlineExceeded), and
// errors.As against the typed errors above keep working, while no raw
// dependency or payload text enters this error's own message.
type machineryError struct {
	phase Phase
	cause error
}

// Error returns fixed safe text plus the closed phase token only.
func (e machineryError) Error() string {
	return "app: machinery failure during " + e.phase.String() + " phase"
}

// Unwrap exposes the cause chain.
func (e machineryError) Unwrap() error { return e.cause }

// classifyMachinery maps a machinery cause onto the closed observation
// result using typed causes only; it never parses error text. A typed
// invalid-input cause is honored only at the initial canonical-input
// boundary; appearing later, it is an internal contradiction.
func classifyMachinery(cause error, initialBoundary bool) PhaseResult {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return ResultCancelled
	}
	var infrastructure InfrastructureUnavailableError
	if errors.As(cause, &infrastructure) {
		return ResultInfrastructureUnavailable
	}
	var invalid InvalidInputError
	if errors.As(cause, &invalid) && initialBoundary {
		return ResultInvalidInput
	}
	return ResultInternalError
}
