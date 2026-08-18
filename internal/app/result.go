package app

import (
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// SpineStatus is the closed terminal spine vocabulary ratified by the design
// (section 9.1): succeeded, failed, and invalid_plan. The zero value marks
// the zero result returned when machinery fails before any meaningful
// artifact exists.
type SpineStatus uint8

const (
	SpineSucceeded SpineStatus = iota + 1
	SpineFailed
	SpineInvalidPlan
)

// String returns the ratified status token.
func (s SpineStatus) String() string {
	switch s {
	case SpineSucceeded:
		return "succeeded"
	case SpineFailed:
		return "failed"
	case SpineInvalidPlan:
		return "invalid_plan"
	default:
		return ""
	}
}

// ExecutionStatus follows the HLD's closed pending/running/succeeded/failed
// execution lifecycle. Returned terminal results contain only succeeded or
// failed.
//
// Ratified ownership decision (owner-approved plan amendment, 2026-08-14):
// ExecutionStatus is application-owned control-plane state and is excluded
// from canonical semantic identity and artifact encoding. It describes what
// happened while the computation ran, never what the computation meant.
// Equivalent semantic executions must remain equivalent regardless of
// application lifecycle representation: adding lifecycle vocabulary here
// (queued, retrying, timed out, cancelled, ...) must never force a semantic
// schema or format version change, and adding a semantic outcome must never
// require this type to grow. Do not move this type into internal/semantic
// to "fix" the original plan text's package spelling; the plan was amended
// because that spelling was an abstraction-boundary mistake.
type ExecutionStatus uint8

const (
	ExecutionPending ExecutionStatus = iota + 1
	ExecutionRunning
	ExecutionSucceeded
	ExecutionFailed
)

// String returns the closed execution lifecycle token.
func (s ExecutionStatus) String() string {
	switch s {
	case ExecutionPending:
		return "pending"
	case ExecutionRunning:
		return "running"
	case ExecutionSucceeded:
		return "succeeded"
	case ExecutionFailed:
		return "failed"
	default:
		return ""
	}
}

// SpineResult is the immutable dependency-closed outcome of one spine run.
// Everything it retains passed its own verification, and every artifact a
// retained value references is itself retained: assessments only with their
// checkpoint and compiled profile, checkpoints only with their accepted
// journal prefix and state.
type SpineResult struct {
	status             SpineStatus
	executionStatus    ExecutionStatus
	semanticRunID      semantic.SemanticRunID
	executionID        semantic.ExecutionID
	inputID            semantic.InputID
	worldID            semantic.WorldID
	journalPrefix      semantic.JournalPrefixDigest
	plan               *semantic.Plan
	profiles           []semantic.CompiledProfile
	compilationFailure *semantic.CompilationFailure
	semanticFailure    *semantic.FailureReport
	state              *semantic.State
	journal            semantic.Journal
	checkpoints        []semantic.CheckpointArtifact
	assessments        []semantic.Assessment
}

// Status returns the closed terminal spine status; zero for the zero result.
func (r SpineResult) Status() SpineStatus { return r.status }

// ExecutionStatus returns the closed execution status, present only once an
// execution was established.
func (r SpineResult) ExecutionStatus() (ExecutionStatus, bool) {
	return r.executionStatus, r.executionStatus != 0
}

// SemanticRunID returns the identity of the semantic run, present once
// binding established one. It identifies what was computed, independently of
// which executor computed it, so two backends running the same semantic
// request share it.
func (r SpineResult) SemanticRunID() (semantic.SemanticRunID, bool) {
	return r.semanticRunID, r.semanticRunID != ""
}

// ExecutionID returns the identity of this execution, present once binding
// established one. Unlike SemanticRunID it incorporates the executor identity,
// so changing only the executor produces a different ExecutionID over the same
// semantic run. Both are derived, never allocated: repeating an identical
// request reproduces both.
func (r SpineResult) ExecutionID() (semantic.ExecutionID, bool) {
	return r.executionID, r.executionID != ""
}

// InputID returns the identity of the pinned input, present once binding
// established one. With WorldID it names everything the run was pinned to, so
// a caller holding these can reconstruct which inputs produced an artifact.
func (r SpineResult) InputID() (semantic.InputID, bool) {
	return r.inputID, r.inputID != ""
}

// WorldID returns the identity of the pinned execution world.
func (r SpineResult) WorldID() (semantic.WorldID, bool) {
	return r.worldID, r.worldID != ""
}

// JournalPrefixDigest returns the accepted history's content identity at the
// point the run finished, whether it succeeded or retained a prefix. It covers
// committed transitions only; rejections never entered it.
func (r SpineResult) JournalPrefixDigest() (semantic.JournalPrefixDigest, bool) {
	return r.journalPrefix, r.journalPrefix != ""
}

// Plan returns the compiled plan when compilation succeeded.
func (r SpineResult) Plan() (semantic.Plan, bool) {
	if r.plan == nil {
		return semantic.Plan{}, false
	}
	return *r.plan, true
}

// Profiles returns every compiled completeness profile referenced by a
// retained assessment, in compiled-profile order.
func (r SpineResult) Profiles() []semantic.CompiledProfile {
	return slices.Clone(r.profiles)
}

// CompilationFailure returns the deterministic invalid-plan value, when
// compilation rejected the request.
func (r SpineResult) CompilationFailure() (semantic.CompilationFailure, bool) {
	if r.compilationFailure == nil {
		return semantic.CompilationFailure{}, false
	}
	return *r.compilationFailure, true
}

// SemanticFailure returns the at-most-one terminal semantic failure report.
func (r SpineResult) SemanticFailure() (semantic.FailureReport, bool) {
	if r.semanticFailure == nil {
		return semantic.FailureReport{}, false
	}
	return *r.semanticFailure, true
}

// State returns the last independently verified immutable state, when one
// exists.
func (r SpineResult) State() (semantic.State, bool) {
	if r.state == nil {
		return semantic.State{}, false
	}
	return *r.state, true
}

// Journal returns the dependency-closed accepted semantic-journal prefix.
func (r SpineResult) Journal() semantic.Journal { return r.journal }

// Checkpoints returns every independently verified sealed checkpoint in the
// retained prefix, in seal order.
func (r SpineResult) Checkpoints() []semantic.CheckpointArtifact {
	return slices.Clone(r.checkpoints)
}

// Assessments returns every independently verified readiness assessment
// whose checkpoint and profile remain retained, in assessment order.
func (r SpineResult) Assessments() []semantic.Assessment {
	return slices.Clone(r.assessments)
}

// CheckpointReceipt is evidence that one execution actually produced one sealed
// checkpoint.
//
// It exists because that relation cannot be recovered from the artifacts. A
// CheckpointArtifact deliberately carries SemanticRunID and not ExecutionID:
// executor identity is excluded from checkpoint identity, so one semantic run can
// be executed by several backends and each produces the same checkpoint. A
// RunBinding does not supply the missing half either, because BindRun happens
// BEFORE execution and establishes only that an ExecutionID is a valid execution
// contract for a SemanticRunID. Holding a binding and a checkpoint whose runs agree
// therefore proves E → S ← C, which is strictly weaker than E → C: a caller could
// bind a second executor over the identical semantic request, never execute it, and
// pair that ExecutionID with a checkpoint some other execution produced. The record
// would name an execution that did not produce the artifact beside it — complete
// looking, and auditing to a contradiction.
//
// SpineResult is the right authority to mint this because it is the only value that
// holds both halves as facts: the ExecutionID that ran and the checkpoints that
// execution actually retained. Its fields are unexported and it is returned only by
// running the spine, so a receipt cannot be assembled by a caller.
//
// This is an in-process receipt. Reconstructing the relation after the process that
// executed is gone is a separate problem, and the same one as authenticating a
// checkpoint read back from storage; it is not solved here.
type CheckpointReceipt struct {
	executionID   semantic.ExecutionID
	semanticRunID semantic.SemanticRunID
	checkpointID  semantic.CheckpointArtifactID
}

// ExecutionID is the execution that produced the checkpoint.
func (r CheckpointReceipt) ExecutionID() semantic.ExecutionID { return r.executionID }

// SemanticRunID is the semantic run that execution carried out.
func (r CheckpointReceipt) SemanticRunID() semantic.SemanticRunID { return r.semanticRunID }

// CheckpointArtifactID is the checkpoint this receipt is for. A receipt is for one
// checkpoint, not for the run: an execution can retain several, and a receipt that
// covered all of them would let one checkpoint's evidence stand for another's.
func (r CheckpointReceipt) CheckpointArtifactID() semantic.CheckpointArtifactID {
	return r.checkpointID
}

// ReceiptFor returns evidence that this execution produced the given sealed
// checkpoint, or reports that it did not.
//
// Membership is checked against the checkpoints this result actually retained, by
// both claim identity and manifest digest. Comparing only the identity would accept
// an artifact whose manifest differs from the retained one — a state Seal already
// refuses to produce, so this is redundant today and costs one comparison to stay
// true if that ever changes.
//
// A result that established no execution mints nothing. Nor does a checkpoint that
// was excluded from the retained frontier, which matters because a checkpoint can be
// sealed and then dropped when its assessment fails verification: publishing one of
// those would publish an artifact this run deliberately did not stand behind.
func (r SpineResult) ReceiptFor(artifact semantic.CheckpointArtifact) (CheckpointReceipt, bool) {
	if r.executionID == "" || r.semanticRunID == "" || artifact.ID() == "" {
		return CheckpointReceipt{}, false
	}
	for _, retained := range r.checkpoints {
		if retained.ID() == artifact.ID() && retained.Digest() == artifact.Digest() {
			return CheckpointReceipt{
				executionID:   r.executionID,
				semanticRunID: r.semanticRunID,
				checkpointID:  artifact.ID(),
			}, true
		}
	}
	return CheckpointReceipt{}, false
}
