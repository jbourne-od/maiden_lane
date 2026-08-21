// Package app orchestrates the progressive semantic spine use case. It
// invokes the semantic kernel in the ratified order, advances an
// independently verified dependency-closed frontier, and owns the closed,
// non-authoritative observation contract consumed by operational telemetry.
//
// The package imports only the standard library and internal/semantic
// (Inviolates 12 and 13): it reinterprets no rule, patch, invariant, or
// readiness meaning, and its observation carrier admits no customer data,
// entity reference, digest, or free-form string (Inviolate 17). Trace
// references are limited to PlanID, SemanticRunID, and ExecutionID.
package app

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Observer receives ordered, non-authoritative begin/end phase events. It
// cannot return an error, replace the context used by semantic functions,
// alter control flow, change a verdict, or contribute to semantic identity.
type Observer interface {
	BeginPhase(context.Context, PhaseObservation)
	EndPhase(context.Context, PhaseObservation)
}

// ObservationEvent is the closed begin/end event tag.
type ObservationEvent uint8

const (
	ObservationBegin ObservationEvent = iota + 1
	ObservationEnd
)

// Phase is the closed observation phase vocabulary (design section 11.3).
type Phase uint8

const (
	PhaseCompile Phase = iota + 1
	PhaseExecuteTransition
	PhaseSealCheckpoint
	PhaseAssessReadiness
	PhaseExecuteSpine
)

// String returns the ratified operational phase token.
func (p Phase) String() string {
	switch p {
	case PhaseCompile:
		return "compile"
	case PhaseExecuteTransition:
		return "execute_transition"
	case PhaseSealCheckpoint:
		return "seal_checkpoint"
	case PhaseAssessReadiness:
		return "assess_readiness"
	case PhaseExecuteSpine:
		return "execute_spine"
	default:
		return ""
	}
}

// PhaseResult is the closed terminal phase classification (design 11.3).
type PhaseResult uint8

const (
	ResultSuccess PhaseResult = iota + 1
	ResultReady
	ResultNeedsInput
	ResultInvalidPlan
	ResultProtectedInvariantFailed
	ResultArtifactIntegrityFailed
	ResultInvalidInput
	ResultCancelled
	ResultInfrastructureUnavailable
	ResultInternalError
)

// String returns the ratified closed observation-result token.
func (r PhaseResult) String() string {
	switch r {
	case ResultSuccess:
		return "success"
	case ResultReady:
		return "ready"
	case ResultNeedsInput:
		return "needs_input"
	case ResultInvalidPlan:
		return "invalid_plan"
	case ResultProtectedInvariantFailed:
		return "protected_invariant_failed"
	case ResultArtifactIntegrityFailed:
		return "artifact_integrity_failed"
	case ResultInvalidInput:
		return "invalid_input"
	case ResultCancelled:
		return "cancelled"
	case ResultInfrastructureUnavailable:
		return "infrastructure_unavailable"
	case ResultInternalError:
		return "internal_error"
	default:
		return ""
	}
}

// TransitionKind, CheckpointKind, and ProfileKind are the bounded operational
// kinds ratified for this slice (design 11.2). They are operational
// classifications, never substitutes for canonical semantic identities.
type TransitionKind uint8

const (
	TransitionFormTeam TransitionKind = iota + 1
	TransitionAggregateTeamHOS
)

// String returns the bounded transition token.
func (k TransitionKind) String() string {
	switch k {
	case TransitionFormTeam:
		return "form_team.v1"
	case TransitionAggregateTeamHOS:
		return "aggregate_team_hos.v1"
	default:
		return ""
	}
}

// CheckpointKind is the bounded checkpoint classification.
type CheckpointKind uint8

const (
	CheckpointTeamFormed CheckpointKind = iota + 1
	CheckpointTeamHOSAggregated
)

// String returns the bounded checkpoint token.
func (k CheckpointKind) String() string {
	switch k {
	case CheckpointTeamFormed:
		return "team_formed.v1"
	case CheckpointTeamHOSAggregated:
		return "team_hos_aggregated.v1"
	default:
		return ""
	}
}

// ProfileKind is the bounded profile classification; it is never ProfileID.
type ProfileKind uint8

const (
	ProfileCM ProfileKind = iota + 1
	ProfileOptimizer
)

// String returns the bounded profile token.
func (k ProfileKind) String() string {
	switch k {
	case ProfileCM:
		return "cm.v1"
	case ProfileOptimizer:
		return "optimizer.v1"
	default:
		return ""
	}
}

// ObservationCode is the closed tagged code carried by an observation. Its
// values are exactly the ratified compilation-diagnostic, operation-
// invariant, rule-invariant, and integrity codes; no other code is
// representable in telemetry.
type ObservationCode uint8

const (
	CodeUnknownField ObservationCode = iota + 1
	CodeUnsupportedOperator
	CodeDeclaredAccessMismatch
	CodeWriteConflictUnresolved
	CodeDependencyCycle
	CodeProfileOrderUnprovable
	CodeOpEntityIdentityCollision ObservationCode = iota + 7
	CodeOpUpdateTargetNotFound
	CodeOpBeforeImageMismatch
	CodeOpRelationAlreadyPresent
	CodeOpRelationEndpointMissing
	CodeArtifactDigestMismatch
	CodeArtifactLinkInconsistent
	CodeAssessmentIdentityConflict
	CodeReplayDivergence
	CodeSelectionCardinalityInvalid
	CodeSelectionEmpty
	CodeSelectionGuardUnsatisfied
	CodeSelectionExpressionUnavailable
)

// String returns the stable closed code token.
func (c ObservationCode) String() string {
	switch c {
	case CodeUnknownField:
		return "UNKNOWN_FIELD"
	case CodeUnsupportedOperator:
		return "UNSUPPORTED_OPERATOR"
	case CodeDeclaredAccessMismatch:
		return "DECLARED_ACCESS_MISMATCH"
	case CodeWriteConflictUnresolved:
		return "WRITE_CONFLICT_UNRESOLVED"
	case CodeDependencyCycle:
		return "DEPENDENCY_CYCLE"
	case CodeProfileOrderUnprovable:
		return "PROFILE_ORDER_UNPROVABLE"
	case CodeOpEntityIdentityCollision:
		return "OP_ENTITY_IDENTITY_COLLISION"
	case CodeOpUpdateTargetNotFound:
		return "OP_UPDATE_TARGET_NOT_FOUND"
	case CodeOpBeforeImageMismatch:
		return "OP_BEFORE_IMAGE_MISMATCH"
	case CodeOpRelationAlreadyPresent:
		return "OP_RELATION_ALREADY_PRESENT"
	case CodeOpRelationEndpointMissing:
		return "OP_RELATION_ENDPOINT_MISSING"
	case CodeSelectionCardinalityInvalid:
		return "SELECTION_CARDINALITY_INVALID"
	case CodeSelectionEmpty:
		return "SELECTION_EMPTY"
	case CodeSelectionGuardUnsatisfied:
		return "SELECTION_GUARD_UNSATISFIED"
	case CodeSelectionExpressionUnavailable:
		return "SELECTION_EXPRESSION_UNAVAILABLE"
	case CodeArtifactDigestMismatch:
		return "ARTIFACT_DIGEST_MISMATCH"
	case CodeArtifactLinkInconsistent:
		return "ARTIFACT_LINK_INCONSISTENT"
	case CodeAssessmentIdentityConflict:
		return "ASSESSMENT_IDENTITY_CONFLICT"
	case CodeReplayDivergence:
		return "REPLAY_DIVERGENCE"
	default:
		return ""
	}
}

// ObservedPlanID, ObservedSemanticRunID, and ObservedExecutionID are the only
// trace references admitted across the observer boundary. They are distinct
// app-owned types so semantic identities never cross the interface directly.
type (
	ObservedPlanID        string
	ObservedSemanticRunID string
	ObservedExecutionID   string
)

// PhaseObservation is the closed app-owned observation carrier. It has no
// public constructor: only Run creates observations, so no caller can widen
// the carrier into a generic metadata channel.
type PhaseObservation struct {
	event      ObservationEvent
	phase      Phase
	result     PhaseResult
	planID     ObservedPlanID
	runID      ObservedSemanticRunID
	execID     ObservedExecutionID
	transition TransitionKind
	checkpoint CheckpointKind
	profile    ProfileKind
	code       ObservationCode
	counts     MetricObservation
}

// Event returns the begin/end tag.
func (o PhaseObservation) Event() ObservationEvent { return o.event }

// Phase returns the closed phase.
func (o PhaseObservation) Phase() Phase { return o.phase }

// Result returns the terminal classification carried by end events only.
func (o PhaseObservation) Result() (PhaseResult, bool) { return o.result, o.result != 0 }

// PlanID returns the observed plan trace reference, when established.
func (o PhaseObservation) PlanID() (ObservedPlanID, bool) { return o.planID, o.planID != "" }

// SemanticRunID returns the observed run trace reference, when established.
func (o PhaseObservation) SemanticRunID() (ObservedSemanticRunID, bool) {
	return o.runID, o.runID != ""
}

// ExecutionID returns the observed execution trace reference, when
// established.
func (o PhaseObservation) ExecutionID() (ObservedExecutionID, bool) {
	return o.execID, o.execID != ""
}

// Transition returns the bounded transition kind, when applicable.
func (o PhaseObservation) Transition() (TransitionKind, bool) {
	return o.transition, o.transition != 0
}

// Checkpoint returns the bounded checkpoint kind, when applicable.
func (o PhaseObservation) Checkpoint() (CheckpointKind, bool) {
	return o.checkpoint, o.checkpoint != 0
}

// Profile returns the bounded profile kind, when applicable.
func (o PhaseObservation) Profile() (ProfileKind, bool) { return o.profile, o.profile != 0 }

// Code returns the tagged closed code, when one classified this observation.
func (o PhaseObservation) Code() (ObservationCode, bool) { return o.code, o.code != 0 }

// MetricProjection returns the bounded dimension/count projection used for
// metric recording. It deliberately contains no trace reference, identity,
// digest, or free-form string field.
func (o PhaseObservation) MetricProjection() MetricObservation {
	projection := o.counts
	projection.Phase = o.phase
	projection.Result = o.result
	projection.Transition = o.transition
	projection.Checkpoint = o.checkpoint
	projection.Profile = o.profile
	projection.Code = o.code
	return projection
}

// MetricObservation carries only bounded closed enums and non-negative
// counts. Structural-operation counts follow the ratified recording rules:
// accepted counts appear only after a whole patch commits, and rejected
// counts appear only when a materialized patch was atomically refused.
type MetricObservation struct {
	Phase             Phase
	Result            PhaseResult
	Transition        TransitionKind
	Checkpoint        CheckpointKind
	Profile           ProfileKind
	Code              ObservationCode
	AcceptedInserts   uint64
	AcceptedRelates   uint64
	AcceptedUpdates   uint64
	RejectedInserts   uint64
	RejectedRelates   uint64
	RejectedUpdates   uint64
	InvariantFailures uint64
}

type discardObserver struct{}

func (discardObserver) BeginPhase(context.Context, PhaseObservation) {}
func (discardObserver) EndPhase(context.Context, PhaseObservation)   {}

// DiscardObserver returns the no-op observer. A nil observer passed to Run
// behaves identically.
func DiscardObserver() Observer { return discardObserver{} }

// observationContextKey privately marks the one derived observation context
// each Run invocation creates for its observer calls only.
type observationContextKey struct{}

// dispatcher delivers observations on the private derived context and
// recovers observer panics: an observer failure is operational and can never
// change the semantic result, the verified frontier, or the returned error.
type dispatcher struct {
	ctx      context.Context
	observer Observer
}

func newDispatcher(ctx context.Context, observer Observer) dispatcher {
	if observer == nil {
		observer = discardObserver{}
	}
	return dispatcher{ctx: context.WithValue(ctx, observationContextKey{}, new(int)), observer: observer}
}

func (d dispatcher) begin(observation PhaseObservation) {
	defer recoverObserverPanic()
	observation.event = ObservationBegin
	d.observer.BeginPhase(d.ctx, observation)
}

func (d dispatcher) end(observation PhaseObservation, result PhaseResult) {
	defer recoverObserverPanic()
	observation.event = ObservationEnd
	observation.result = result
	d.observer.EndPhase(d.ctx, observation)
}

// recoverObserverPanic swallows an observer panic. The observer contract is
// no-error and non-authoritative; there is no channel through which its
// failure may influence the spine.
func recoverObserverPanic() {
	_ = recover()
}

// transitionKindForRule projects a compiled rule identity onto the bounded
// operational vocabulary. Rules outside the ratified slice have no bounded
// kind and are observed without one.
func transitionKindForRule(rule semantic.RuleID) TransitionKind {
	switch rule {
	case "form_team.v1":
		return TransitionFormTeam
	case "aggregate_team_hos.v1":
		return TransitionAggregateTeamHOS
	default:
		return 0
	}
}

func checkpointKindForKey(key semantic.CheckpointKey) CheckpointKind {
	switch key {
	case "team_formed.v1":
		return CheckpointTeamFormed
	case "team_hos_aggregated.v1":
		return CheckpointTeamHOSAggregated
	default:
		return 0
	}
}

func profileKindForKey(key semantic.ProfileKey) ProfileKind {
	switch key {
	case "cm.v1":
		return ProfileCM
	case "optimizer.v1":
		return ProfileOptimizer
	default:
		return 0
	}
}

// ObservationCodeForInvariant maps a semantic invariant code to its observation code, or
// zero if none is mapped.
//
// Exported so the telemetry package can check its own dimension mapping against
// semantic.AllInvariantCodes rather than against a list re-typed into a test file. Three
// walkers over that vocabulary live outside the semantic package -- this mapping, its string
// rendering, and observedInvariantCode -- and the last of them had no way to be driven from
// the vocabulary without this.
func ObservationCodeForInvariant(code semantic.InvariantCode) ObservationCode {
	return codeForInvariant(code)
}

func codeForInvariant(code semantic.InvariantCode) ObservationCode {
	switch code {
	case semantic.SelectionCardinalityInvalid:
		return CodeSelectionCardinalityInvalid
	case semantic.SelectionEmpty:
		return CodeSelectionEmpty
	case semantic.SelectionGuardUnsatisfied:
		return CodeSelectionGuardUnsatisfied
	case semantic.SelectionExpressionUnavailable:
		return CodeSelectionExpressionUnavailable
	default:
		return 0
	}
}

func codeForOperation(code semantic.OperationInvariantCode) ObservationCode {
	switch code {
	case semantic.OperationEntityIdentityCollision:
		return CodeOpEntityIdentityCollision
	case semantic.OperationUpdateTargetNotFound:
		return CodeOpUpdateTargetNotFound
	case semantic.OperationBeforeImageMismatch:
		return CodeOpBeforeImageMismatch
	case semantic.OperationRelationAlreadyPresent:
		return CodeOpRelationAlreadyPresent
	case semantic.OperationRelationEndpointMissing:
		return CodeOpRelationEndpointMissing
	default:
		return 0
	}
}

func codeForIntegrity(code semantic.IntegrityCode) ObservationCode {
	switch code {
	case semantic.ArtifactDigestMismatch:
		return CodeArtifactDigestMismatch
	case semantic.ArtifactLinkInconsistent:
		return CodeArtifactLinkInconsistent
	case semantic.AssessmentIdentityConflict:
		return CodeAssessmentIdentityConflict
	case semantic.ReplayDivergence:
		return CodeReplayDivergence
	default:
		return 0
	}
}

func codeForDiagnostic(code semantic.CompilationDiagnosticCode) ObservationCode {
	switch code {
	case semantic.UnknownField:
		return CodeUnknownField
	case semantic.UnsupportedOperator:
		return CodeUnsupportedOperator
	case semantic.DeclaredAccessMismatch:
		return CodeDeclaredAccessMismatch
	case semantic.WriteConflictUnresolved:
		return CodeWriteConflictUnresolved
	case semantic.DependencyCycle:
		return CodeDependencyCycle
	case semantic.ProfileOrderUnprovable:
		return CodeProfileOrderUnprovable
	default:
		return 0
	}
}
