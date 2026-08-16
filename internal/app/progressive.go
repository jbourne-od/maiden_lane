package app

import (
	"context"
	"errors"
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Request carries one complete set of canonical spine inputs. The
// application pins them unchanged; it never reinterprets or augments them.
type Request struct {
	Compilation      semantic.CompileRequest
	InitialState     semantic.State
	World            semantic.World
	ExecutorIdentity semantic.ExecutorIdentity
	Policy           semantic.ProvenancePolicy
}

// operations is the unexported machinery seam. It exists so package tests
// can prove orchestration inability handling; it is not a rule callback and
// never enters the certified semantic plan. None of its functions accepts a
// context, so the private observation context cannot reach semantic code.
type operations struct {
	compile func(semantic.CompileRequest) (semantic.Compilation, error)
	bind    func(semantic.RunBindingRequest) (semantic.RunBinding, error)
	execute func(semantic.RunBinding, semantic.RuleID, semantic.State, semantic.Journal) (semantic.TransitionOutcome, error)
	seal    func(semantic.SealRequest) (semantic.SealOutcome, error)
	assess  func(semantic.AssessmentRequest) (semantic.AssessmentOutcome, error)
}

func productionOperations() operations {
	return operations{
		compile: semantic.Compile,
		bind:    semantic.BindRun,
		execute: semantic.ExecuteTransition,
		seal:    semantic.Seal,
		assess:  semantic.Assess,
	}
}

// errOutcomeContradiction marks a semantic outcome that is neither a value
// nor a typed failure: an internal contradiction, never hostile input.
var errOutcomeContradiction = errors.New("semantic outcome carries neither value nor typed failure")

// Run executes the progressive semantic spine: compile, bind, then for every
// compiled transformation in plan order execute the transition, seal every
// checkpoint declared at that boundary, and assess each sealed checkpoint
// under every compiled profile.
//
// Semantic rejection and machinery failure share no return channel: a
// deterministic semantic rejection returns a populated typed result with a
// nil Go error, while machinery inability returns the last independently
// verified dependency-closed prefix with a non-nil Go error.
func Run(ctx context.Context, request Request, observer Observer) (SpineResult, error) {
	return runWithOperations(ctx, request, observer, productionOperations())
}

func runWithOperations(ctx context.Context, request Request, observer Observer, ops operations) (SpineResult, error) {
	run := &spineRun{ctx: ctx, dispatch: newDispatcher(ctx, observer), ops: ops}
	run.dispatch.begin(run.observation(PhaseExecuteSpine))
	if err := ctx.Err(); err != nil {
		return run.machinery(PhaseObservation{}, false, err, true)
	}
	if err := validateRequest(request); err != nil {
		run.dispatch.end(run.observation(PhaseExecuteSpine), ResultInvalidInput)
		return SpineResult{}, err
	}

	compileObservation := run.observation(PhaseCompile)
	run.dispatch.begin(compileObservation)
	compilation, err := run.ops.compile(request.Compilation)
	if err != nil {
		return run.machinery(compileObservation, true, err, true)
	}
	if failure, ok := compilation.Failure(); ok {
		return run.invalidPlan(compileObservation, failure), nil
	}
	plan, ok := compilation.Plan()
	if !ok {
		return run.machinery(compileObservation, true, errOutcomeContradiction, false)
	}
	run.plan, run.planID, run.profiles = &plan, plan.ID(), compilation.Profiles()
	run.dispatch.end(run.observation(PhaseCompile), ResultSuccess)

	if err := ctx.Err(); err != nil {
		return run.machinery(PhaseObservation{}, false, err, true)
	}
	binding, err := run.ops.bind(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     request.InitialState,
		World:            request.World,
		ExecutorIdentity: request.ExecutorIdentity,
		Policy:           request.Policy,
	})
	if err != nil {
		return run.machinery(PhaseObservation{}, false, err, true)
	}
	run.runID, run.execID = binding.SemanticRunID(), binding.ExecutionID()
	run.executionEstablished = true
	initial := request.InitialState
	run.state, run.journal = &initial, semantic.NewJournal()

	for _, transformation := range plan.Transformations() {
		rule := transformation.Declaration().ID
		if err := ctx.Err(); err != nil {
			return run.machinery(PhaseObservation{}, false, err, false)
		}
		observation := run.observation(PhaseExecuteTransition)
		observation.transition = transitionKindForRule(rule)
		run.dispatch.begin(observation)
		outcome, err := run.ops.execute(binding, rule, *run.state, run.journal)
		if err != nil {
			return run.machinery(observation, true, err, false)
		}
		if failure, ok := outcome.Failure(); ok {
			return run.semanticRejection(observation, failure, &transformation), nil
		}
		state := outcome.State()
		run.state, run.journal = &state, outcome.Journal()
		observation.counts.AcceptedInserts, observation.counts.AcceptedRelates, observation.counts.AcceptedUpdates =
			committedOperationCounts(outcome.Patch())
		run.dispatch.end(observation, ResultSuccess)

		for _, checkpoint := range plan.Checkpoints() {
			if checkpoint.After != rule {
				continue
			}
			result, done, err := run.sealAndAssess(binding, checkpoint, outcome.InvariantResults())
			if done {
				return result, err
			}
		}
	}

	run.dispatch.end(run.observation(PhaseExecuteSpine), ResultSuccess)
	return SpineResult{status: SpineSucceeded, executionStatus: ExecutionSucceeded, plan: run.plan,
		semanticRunID: run.runID, executionID: run.execID,
		profiles: run.retainedProfiles(), state: run.state, journal: run.journal,
		checkpoints: slices.Clone(run.checkpoints), assessments: slices.Clone(run.assessments)}, nil
}

// spineRun holds one invocation's dispatcher, trace references, and private
// independently verified dependency-closed frontier.
type spineRun struct {
	ctx      context.Context
	dispatch dispatcher
	ops      operations

	planID semantic.PlanID
	runID  semantic.SemanticRunID
	execID semantic.ExecutionID

	executionEstablished bool
	plan                 *semantic.Plan
	profiles             []semantic.CompiledProfile
	state                *semantic.State
	journal              semantic.Journal
	checkpoints          []semantic.CheckpointArtifact
	assessments          []semantic.Assessment
}

func (r *spineRun) observation(phase Phase) PhaseObservation {
	return PhaseObservation{phase: phase, planID: ObservedPlanID(r.planID),
		runID: ObservedSemanticRunID(r.runID), execID: ObservedExecutionID(r.execID)}
}

// sealAndAssess seals one declared checkpoint over the current frontier and
// assesses it under every compiled profile in compiled order. done reports
// that the spine terminated inside this boundary.
func (r *spineRun) sealAndAssess(binding semantic.RunBinding, checkpoint semantic.CheckpointDeclaration, invariantResults []semantic.InvariantResult) (SpineResult, bool, error) {
	if err := r.ctx.Err(); err != nil {
		result, wrapped := r.machinery(PhaseObservation{}, false, err, false)
		return result, true, wrapped
	}
	observation := r.observation(PhaseSealCheckpoint)
	observation.checkpoint = checkpointKindForKey(checkpoint.Key)
	r.dispatch.begin(observation)
	outcome, err := r.ops.seal(semantic.SealRequest{Binding: binding, Checkpoint: checkpoint.Key,
		State: *r.state, Journal: r.journal, InvariantResults: invariantResults,
		KnownArtifacts: slices.Clone(r.checkpoints)})
	if err != nil {
		result, wrapped := r.machinery(observation, true, err, false)
		return result, true, wrapped
	}
	if failure, ok := outcome.Failure(); ok {
		return r.semanticRejection(observation, failure, nil), true, nil
	}
	artifact, ok := outcome.Artifact()
	if !ok {
		result, wrapped := r.machinery(observation, true, errOutcomeContradiction, false)
		return result, true, wrapped
	}
	r.checkpoints = append(r.checkpoints, artifact)
	r.dispatch.end(observation, ResultSuccess)

	for _, profile := range r.profiles {
		if err := r.ctx.Err(); err != nil {
			result, wrapped := r.machinery(PhaseObservation{}, false, err, false)
			return result, true, wrapped
		}
		assessObservation := r.observation(PhaseAssessReadiness)
		assessObservation.checkpoint = observation.checkpoint
		assessObservation.profile = profileKindForKey(profile.Key())
		r.dispatch.begin(assessObservation)
		assessOutcome, err := r.ops.assess(semantic.AssessmentRequest{Checkpoint: artifact,
			State: *r.state, Profile: profile, KnownAssessments: slices.Clone(r.assessments)})
		if err != nil {
			result, wrapped := r.machinery(assessObservation, true, err, false)
			return result, true, wrapped
		}
		if failure, ok := assessOutcome.Failure(); ok {
			r.excludeUnverifiedCheckpoint(artifact, failure)
			return r.semanticRejection(assessObservation, failure, nil), true, nil
		}
		assessment, ok := assessOutcome.Assessment()
		if !ok {
			result, wrapped := r.machinery(assessObservation, true, errOutcomeContradiction, false)
			return result, true, wrapped
		}
		r.assessments = append(r.assessments, assessment)
		verdict := ResultReady
		if assessment.Verdict() == semantic.NeedsInput {
			verdict = ResultNeedsInput
		}
		r.dispatch.end(assessObservation, verdict)
	}
	return SpineResult{}, false, nil
}

// invalidPlan completes the spine for a deterministic invalid-plan
// compilation: a typed terminal result with no plan, execution, or error.
func (r *spineRun) invalidPlan(observation PhaseObservation, failure semantic.CompilationFailure) SpineResult {
	if diagnostics := failure.Diagnostics(); len(diagnostics) > 0 {
		observation.code = codeForDiagnostic(diagnostics[0].Code())
	}
	r.dispatch.end(observation, ResultInvalidPlan)
	spine := r.observation(PhaseExecuteSpine)
	spine.code = observation.code
	r.dispatch.end(spine, ResultInvalidPlan)
	return SpineResult{status: SpineInvalidPlan, compilationFailure: &failure}
}

// semanticRejection completes the spine for a typed deterministic semantic
// failure: the retained frontier plus exactly one failure report, nil error.
func (r *spineRun) semanticRejection(observation PhaseObservation, failure semantic.FailureReport, transformation *semantic.CompiledTransformation) SpineResult {
	classification, code := classifySemanticFailure(failure)
	observation.code = code
	if failure.Kind() == semantic.ProtectedInvariantFailed {
		observation.counts.InvariantFailures = protectedFailureCount(failure)
		if _, materialized := failure.ProposedPatchDigest(); materialized && transformation != nil {
			observation.counts.RejectedInserts, observation.counts.RejectedRelates, observation.counts.RejectedUpdates =
				proposedOperationCounts(*transformation)
		}
	}
	r.dispatch.end(observation, classification)
	spine := r.observation(PhaseExecuteSpine)
	spine.code = code
	r.dispatch.end(spine, classification)
	result := r.frontierResult()
	result.semanticFailure = &failure
	return result
}

// machinery completes the spine for operational inability: the last
// independently verified dependency-closed prefix plus a non-nil Go error
// with fixed safe text and a preserved cause chain.
func (r *spineRun) machinery(observation PhaseObservation, phaseOpen bool, cause error, initialBoundary bool) (SpineResult, error) {
	classification := classifyMachinery(cause, initialBoundary)
	failedPhase := PhaseExecuteSpine
	if phaseOpen {
		r.dispatch.end(observation, classification)
		failedPhase = observation.phase
	}
	r.dispatch.end(r.observation(PhaseExecuteSpine), classification)
	return r.frontierResult(), machineryError{phase: failedPhase, cause: cause}
}

// frontierResult snapshots the verified frontier. Before a compiled plan
// exists there is no meaningful artifact and the result is zero.
func (r *spineRun) frontierResult() SpineResult {
	if r.plan == nil {
		return SpineResult{}
	}
	result := SpineResult{status: SpineFailed, plan: r.plan, profiles: r.retainedProfiles(),
		state: r.state, journal: r.journal,
		checkpoints: slices.Clone(r.checkpoints), assessments: slices.Clone(r.assessments)}
	if r.executionEstablished {
		result.executionStatus = ExecutionFailed
		// The run and execution identities are reported alongside the retained
		// frontier: a caller diagnosing a failure needs to name the run that
		// produced it.
		result.semanticRunID, result.executionID = r.runID, r.execID
	}
	return result
}

// retainedProfiles keeps every compiled profile referenced by a retained
// assessment, in compiled-profile order, so the result stays
// dependency-closed without retaining unreferenced compilations.
func (r *spineRun) retainedProfiles() []semantic.CompiledProfile {
	used := make(map[semantic.ProfileID]struct{}, len(r.assessments))
	for _, assessment := range r.assessments {
		used[assessment.ProfileID()] = struct{}{}
	}
	retained := make([]semantic.CompiledProfile, 0, len(used))
	for _, profile := range r.profiles {
		if _, ok := used[profile.ID()]; ok {
			retained = append(retained, profile)
		}
	}
	return retained
}

// excludeUnverifiedCheckpoint applies the assess outcome's own verification
// record: an integrity report retains the checkpoint under assessment only
// if it re-verified that checkpoint's content identity. Otherwise the
// implicated checkpoint and every dependent assessment leave the frontier;
// the immutable bytes themselves are never mutated or deleted.
func (r *spineRun) excludeUnverifiedCheckpoint(artifact semantic.CheckpointArtifact, failure semantic.FailureReport) {
	if report, ok := failure.ArtifactIntegrity(); ok {
		if verified, present := report.LastVerifiedCheckpointArtifactID(); present && verified == artifact.ID() {
			return
		}
	}
	checkpoints := make([]semantic.CheckpointArtifact, 0, len(r.checkpoints))
	for _, checkpoint := range r.checkpoints {
		if checkpoint.ID() != artifact.ID() {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	r.checkpoints = checkpoints
	assessments := make([]semantic.Assessment, 0, len(r.assessments))
	for _, assessment := range r.assessments {
		if assessment.CheckpointArtifactID() != artifact.ID() {
			assessments = append(assessments, assessment)
		}
	}
	r.assessments = assessments
}

// validateRequest performs presence-only canonical-input validation at the
// initial request boundary. It asserts nothing about business meaning; the
// compiler and kernel own all semantic validation.
func validateRequest(request Request) error {
	if request.Compilation.CompilerSemanticsVersion == "" || len(request.Compilation.Rules.Transformations) == 0 {
		return InvalidInputError{Code: InputCompilationRequestIncomplete}
	}
	if request.InitialState.Digest() == "" || request.World.ID() == "" ||
		request.ExecutorIdentity == (semantic.ExecutorIdentity{}) || request.Policy == 0 {
		return InvalidInputError{Code: InputRunBindingIncomplete}
	}
	return nil
}

func classifySemanticFailure(failure semantic.FailureReport) (PhaseResult, ObservationCode) {
	if failure.Kind() == semantic.ArtifactIntegrityFailed {
		if report, ok := failure.ArtifactIntegrity(); ok {
			return ResultArtifactIntegrityFailed, codeForIntegrity(report.Code())
		}
		return ResultArtifactIntegrityFailed, 0
	}
	if operation := failure.OperationInvariantCode(); operation != "" {
		return ResultProtectedInvariantFailed, codeForOperation(operation)
	}
	return ResultProtectedInvariantFailed, codeForInvariant(failure.InvariantCode())
}

// protectedFailureCount counts produced failing protected results: failing
// rule-invariant results plus the typed operation-invariant rejection.
func protectedFailureCount(failure semantic.FailureReport) uint64 {
	count := uint64(0)
	for _, result := range failure.InvariantResults() {
		if !result.Passed() {
			count++
		}
	}
	if failure.OperationInvariantCode() != "" {
		count++
	}
	return count
}

func committedOperationCounts(patch semantic.Patch) (inserts, relates, updates uint64) {
	for _, operation := range patch.Operations() {
		switch operation.Kind() {
		case semantic.OperationInsert:
			inserts++
		case semantic.OperationRelate:
			relates++
		case semantic.OperationUpdate:
			updates++
		}
	}
	return inserts, relates, updates
}

// proposedOperationCounts projects the rejected materialized patch's
// operation counts from the compiled transformation's declared atomic shape:
// the semantic outcome retains no rejected patch, but the compiled operator
// fixes it as one insert plus one relation per declared source for form, and
// exactly one update for aggregate.
func proposedOperationCounts(transformation semantic.CompiledTransformation) (inserts, relates, updates uint64) {
	declaration := transformation.Declaration()
	switch {
	case declaration.Form != nil:
		return 1, uint64(len(declaration.Form.Sources)), 0
	case declaration.Aggregate != nil:
		return 0, 0, 1
	default:
		return 0, 0, 0
	}
}
