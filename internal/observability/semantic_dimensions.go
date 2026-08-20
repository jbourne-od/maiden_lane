package observability

import (
	"go.opentelemetry.io/otel/codes"

	"github.com/optimaldynamics/maiden-lane/internal/app"
)

// This file owns the observability copy of every closed telemetry dimension
// vocabulary, plus the exhaustive mapping from the app-owned observation
// carrier into it.
//
// The duplication is deliberate. internal/app owns what the spine reports;
// this package owns what may leave the process as an attribute. Because only a
// token produced by a mapper below can reach a span or metric, widening the app
// vocabulary cannot widen telemetry without an edit here, and an app value this
// package has not admitted can never export its raw representation as an
// unbounded attribute (Inviolate 17, design sections 11.2 to 11.4).
//
// Ratified rule for unadmitted values (owner decision, 2026-08-15), preserved
// here verbatim so a later cleanup cannot quietly make the two cases uniform:
//
//	Optional bounded dimensions fail closed by omission, not by substitution.
//	A required dimension must always exist: a span needs a name and a status,
//	and a completed phase needs a duration point, so an unadmitted phase or
//	result maps to internal_error. That value is deliberately outside design
//	11.4's enumerated phase list and is a tripwire, not a category. An optional
//	dimension already has an honest absent representation, so an unadmitted
//	value is omitted rather than labeled: emitting profile_kind=internal_error
//	would place a value outside the ratified exact-value list into a bounded
//	vocabulary and would assert a classification the spine never made.
//	Invariant: telemetry may drop a point it cannot truthfully label, but it
//	may never invent or widen a bounded dimension value.

// observationPhase is the observability-owned closed phase dimension.
// phaseUnknown is the fixed fallback for an app value this package has not
// admitted; it never exports the unrecognized value itself.
type observationPhase uint8

const (
	phaseUnknown observationPhase = iota
	phaseCompile
	phaseExecuteTransition
	phaseSealCheckpoint
	phaseAssessReadiness
	phaseExecuteSpine
)

func (p observationPhase) String() string {
	switch p {
	case phaseCompile:
		return "compile"
	case phaseExecuteTransition:
		return "execute_transition"
	case phaseSealCheckpoint:
		return "seal_checkpoint"
	case phaseAssessReadiness:
		return "assess_readiness"
	case phaseExecuteSpine:
		return "execute_spine"
	default:
		return "internal_error"
	}
}

// observationResult is the observability-owned closed terminal classification.
type observationResult uint8

const (
	resultSuccess observationResult = iota + 1
	resultReady
	resultNeedsInput
	resultInvalidPlan
	resultProtectedInvariantFailed
	resultArtifactIntegrityFailed
	resultInvalidInput
	resultCancelled
	resultInfrastructureUnavailable
	resultInternalError
)

func (r observationResult) String() string {
	switch r {
	case resultSuccess:
		return "success"
	case resultReady:
		return "ready"
	case resultNeedsInput:
		return "needs_input"
	case resultInvalidPlan:
		return "invalid_plan"
	case resultProtectedInvariantFailed:
		return "protected_invariant_failed"
	case resultArtifactIntegrityFailed:
		return "artifact_integrity_failed"
	case resultInvalidInput:
		return "invalid_input"
	case resultCancelled:
		return "cancelled"
	case resultInfrastructureUnavailable:
		return "infrastructure_unavailable"
	default:
		return "internal_error"
	}
}

// spanStatus maps a terminal classification onto an explicit span status. No
// completed phase is ever left UNSET, and an expected readiness answer of
// needs_input is a success, not an operational error.
func (r observationResult) spanStatus() codes.Code {
	switch r {
	case resultSuccess, resultReady, resultNeedsInput:
		return codes.Ok
	default:
		return codes.Error
	}
}

// semanticRejection reports whether a classification is a deterministic
// semantic refusal rather than operational inability. Only a refusal may
// record a rejected checkpoint point.
func (r observationResult) semanticRejection() bool {
	return r == resultProtectedInvariantFailed || r == resultArtifactIntegrityFailed
}

// operationKind is the observability-owned closed structural-operation
// dimension.
type operationKind uint8

const (
	operationInsert operationKind = iota + 1
	operationRelate
	operationUpdate
)

func (k operationKind) String() string {
	switch k {
	case operationInsert:
		return "insert"
	case operationRelate:
		return "relate"
	case operationUpdate:
		return "update"
	default:
		return "internal_error"
	}
}

// operationResult distinguishes operations of a committed patch from those
// merely proposed by a materialized patch that was atomically refused.
type operationResult uint8

const (
	operationAccepted operationResult = iota + 1
	operationRejected
)

func (r operationResult) String() string {
	if r == operationAccepted {
		return "accepted"
	}
	return "rejected"
}

// checkpointResult is present only when a seal actually committed or an actual
// seal request was refused. An unreached checkpoint records nothing.
type checkpointResult uint8

const (
	checkpointNone checkpointResult = iota
	checkpointSealed
	checkpointRejected
)

func (r checkpointResult) String() string {
	if r == checkpointSealed {
		return "sealed"
	}
	return "rejected"
}

// profileKind is a bounded operational category. It is never a ProfileID.
type profileKind uint8

const (
	profileCM profileKind = iota + 1
	profileOptimizer
)

func (k profileKind) String() string {
	if k == profileCM {
		return "cm.v1"
	}
	return "optimizer.v1"
}

// readinessVerdict is the bounded readiness dimension.
type readinessVerdict uint8

const (
	verdictNone readinessVerdict = iota
	verdictReady
	verdictNeedsInput
)

func (v readinessVerdict) String() string {
	if v == verdictReady {
		return "ready"
	}
	return "needs_input"
}

// transitionKind and checkpointKind are the bounded operational span kinds.
type transitionKind uint8

const (
	transitionFormTeam transitionKind = iota + 1
	transitionAggregateTeamHOS
)

func (k transitionKind) String() string {
	if k == transitionFormTeam {
		return "form_team.v1"
	}
	return "aggregate_team_hos.v1"
}

type checkpointKind uint8

const (
	checkpointTeamFormed checkpointKind = iota + 1
	checkpointTeamHOSAggregated
)

func (k checkpointKind) String() string {
	if k == checkpointTeamFormed {
		return "team_formed.v1"
	}
	return "team_hos_aggregated.v1"
}

// closedCode carries a stable code token. Its field is unexported, so the only
// way to obtain one is through observedCode or observedInvariantCode: an
// arbitrary string cannot be forged into a telemetry dimension.
type closedCode struct{ token string }

func (c closedCode) String() string { return c.token }

func (c closedCode) present() bool { return c.token != "" }

// observedPhase maps an app phase onto this package's closed vocabulary.
func observedPhase(phase app.Phase) observationPhase {
	switch phase {
	case app.PhaseCompile:
		return phaseCompile
	case app.PhaseExecuteTransition:
		return phaseExecuteTransition
	case app.PhaseSealCheckpoint:
		return phaseSealCheckpoint
	case app.PhaseAssessReadiness:
		return phaseAssessReadiness
	case app.PhaseExecuteSpine:
		return phaseExecuteSpine
	default:
		return phaseUnknown
	}
}

// observedResult maps an app classification onto this package's closed
// vocabulary. An absent or unadmitted value becomes internal_error so no
// completed phase is left unclassified.
func observedResult(result app.PhaseResult) observationResult {
	switch result {
	case app.ResultSuccess:
		return resultSuccess
	case app.ResultReady:
		return resultReady
	case app.ResultNeedsInput:
		return resultNeedsInput
	case app.ResultInvalidPlan:
		return resultInvalidPlan
	case app.ResultProtectedInvariantFailed:
		return resultProtectedInvariantFailed
	case app.ResultArtifactIntegrityFailed:
		return resultArtifactIntegrityFailed
	case app.ResultInvalidInput:
		return resultInvalidInput
	case app.ResultCancelled:
		return resultCancelled
	case app.ResultInfrastructureUnavailable:
		return resultInfrastructureUnavailable
	default:
		return resultInternalError
	}
}

// The three bounded kinds are optional dimensions: the boolean result reports
// admission, and an unadmitted value is omitted by every caller rather than
// substituted. See the ratified optional-dimension rule at the top of this file.
func observedTransitionKind(kind app.TransitionKind) (transitionKind, bool) {
	switch kind {
	case app.TransitionFormTeam:
		return transitionFormTeam, true
	case app.TransitionAggregateTeamHOS:
		return transitionAggregateTeamHOS, true
	default:
		return 0, false
	}
}

func observedCheckpointKind(kind app.CheckpointKind) (checkpointKind, bool) {
	switch kind {
	case app.CheckpointTeamFormed:
		return checkpointTeamFormed, true
	case app.CheckpointTeamHOSAggregated:
		return checkpointTeamHOSAggregated, true
	default:
		return 0, false
	}
}

func observedProfileKind(kind app.ProfileKind) (profileKind, bool) {
	switch kind {
	case app.ProfileCM:
		return profileCM, true
	case app.ProfileOptimizer:
		return profileOptimizer, true
	default:
		return 0, false
	}
}

// observedCode admits the complete closed code vocabulary to span attributes.
// The tokens are repeated here rather than delegated to app so that widening
// the app vocabulary cannot widen telemetry without an edit in this package.
func observedCode(code app.ObservationCode) (closedCode, bool) {
	switch code {
	case app.CodeUnknownField:
		return closedCode{"UNKNOWN_FIELD"}, true
	case app.CodeUnsupportedOperator:
		return closedCode{"UNSUPPORTED_OPERATOR"}, true
	case app.CodeDeclaredAccessMismatch:
		return closedCode{"DECLARED_ACCESS_MISMATCH"}, true
	case app.CodeWriteConflictUnresolved:
		return closedCode{"WRITE_CONFLICT_UNRESOLVED"}, true
	case app.CodeDependencyCycle:
		return closedCode{"DEPENDENCY_CYCLE"}, true
	case app.CodeProfileOrderUnprovable:
		return closedCode{"PROFILE_ORDER_UNPROVABLE"}, true
	case app.CodeArtifactDigestMismatch:
		return closedCode{"ARTIFACT_DIGEST_MISMATCH"}, true
	case app.CodeArtifactLinkInconsistent:
		return closedCode{"ARTIFACT_LINK_INCONSISTENT"}, true
	case app.CodeAssessmentIdentityConflict:
		return closedCode{"ASSESSMENT_IDENTITY_CONFLICT"}, true
	case app.CodeReplayDivergence:
		return closedCode{"REPLAY_DIVERGENCE"}, true
	default:
		return observedInvariantCode(code)
	}
}

// observedInvariantCode admits only the operation-invariant and rule-invariant
// subset, which is the ratified vocabulary of the invariant_code metric
// dimension. A compilation diagnostic or integrity code is not an invariant
// failure and must not be counted as one.
func observedInvariantCode(code app.ObservationCode) (closedCode, bool) {
	switch code {
	case app.CodeOpEntityIdentityCollision:
		return closedCode{"OP_ENTITY_IDENTITY_COLLISION"}, true
	case app.CodeOpUpdateTargetNotFound:
		return closedCode{"OP_UPDATE_TARGET_NOT_FOUND"}, true
	case app.CodeOpBeforeImageMismatch:
		return closedCode{"OP_BEFORE_IMAGE_MISMATCH"}, true
	case app.CodeOpRelationAlreadyPresent:
		return closedCode{"OP_RELATION_ALREADY_PRESENT"}, true
	case app.CodeOpRelationEndpointMissing:
		return closedCode{"OP_RELATION_ENDPOINT_MISSING"}, true
	case app.CodeDeclaredSourceNotFound:
		return closedCode{"DECLARED_SOURCE_NOT_FOUND"}, true
	case app.CodeDeclaredSourceKindInvalid:
		return closedCode{"DECLARED_SOURCE_KIND_INVALID"}, true
	case app.CodeTeamAssignmentKeyInvalid:
		return closedCode{"TEAM_ASSIGNMENT_KEY_INVALID"}, true
	case app.CodeTeamAssignmentKeyMismatch:
		return closedCode{"TEAM_ASSIGNMENT_KEY_MISMATCH"}, true
	case app.CodeTeamMemberCardinalityInvalid:
		return closedCode{"TEAM_MEMBER_CARDINALITY_INVALID"}, true
	case app.CodeHOSTupleIncomplete:
		return closedCode{"HOS_TUPLE_INCOMPLETE"}, true
	case app.CodeHOSDurationInvalid:
		return closedCode{"HOS_DURATION_INVALID"}, true
	case app.CodeHOSAnchorMismatch:
		return closedCode{"HOS_ANCHOR_MISMATCH"}, true
	case app.CodeHOSAggregateInvalid:
		return closedCode{"HOS_AGGREGATE_INVALID"}, true
	case app.CodeSelectionCardinalityInvalid:
		return closedCode{"SELECTION_CARDINALITY_INVALID"}, true
	case app.CodeSelectionEmpty:
		return closedCode{"SELECTION_EMPTY"}, true
	case app.CodeSelectionGuardUnsatisfied:
		return closedCode{"SELECTION_GUARD_UNSATISFIED"}, true
	case app.CodeSelectionExpressionUnavailable:
		return closedCode{"SELECTION_EXPRESSION_UNAVAILABLE"}, true
	default:
		return closedCode{}, false
	}
}
