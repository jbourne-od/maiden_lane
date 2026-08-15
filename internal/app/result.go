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
