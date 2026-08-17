// Package worker runs queued executions.
//
// It owns no semantic decisions. It claims work, invokes the application use
// case, and stores what that returns. Every judgement about meaning belongs to
// the kernel, and every judgement about lifecycle belongs to the store.
//
// The distinction the worker does make is between an answer and an inability.
// A deterministic semantic rejection is a completed execution carrying a typed
// failure, because the computation produced a real result: retrying reproduces
// it exactly, so recording it as a worker failure would both misreport it and
// invite pointless repetition. An inability to reach an answer is different, and
// is split again by whether repetition could plausibly change the outcome.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// SpineRunner is the consumer-owned narrow interface over the application use
// case, declared here so worker tests need not stand up the whole kernel.
type SpineRunner interface {
	Run(context.Context, app.Request, app.Observer) (app.SpineResult, error)
}

// Bounded failure reasons. Each is a closed token: an operator sees why an
// execution stopped without any payload, identity, or dependency text.
const (
	reasonPlanAbsent       = "plan_absent"
	reasonIdentityMismatch = "identity_mismatch"
	reasonInvalidInput     = "invalid_semantic_input"
	reasonInternalError    = "internal_error"
)

// Worker claims and runs executions.
type Worker struct {
	plans      ports.PlanStore
	executions ports.ExecutionStore
	runner     SpineRunner
	observer   app.Observer
	logger     *slog.Logger

	// lease bounds how long a claim survives without progress, and idle bounds
	// how long the loop waits when the queue is empty. Both are operational and
	// never reach the kernel.
	lease time.Duration
	idle  time.Duration
}

// Options configures a worker. Zero durations take documented defaults.
type Options struct {
	Plans      ports.PlanStore
	Executions ports.ExecutionStore
	Runner     SpineRunner
	Observer   app.Observer
	Logger     *slog.Logger
	Lease      time.Duration
	Idle       time.Duration
}

// New returns a worker.
func New(options Options) *Worker {
	lease := options.Lease
	if lease <= 0 {
		// Long enough that an ordinary execution finishes well inside it, short
		// enough that a dead worker's work is reclaimed promptly.
		lease = 2 * time.Minute
	}
	idle := options.Idle
	if idle <= 0 {
		idle = 250 * time.Millisecond
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Worker{
		plans: options.Plans, executions: options.Executions,
		runner: options.Runner, observer: options.Observer,
		logger: logger, lease: lease, idle: idle,
	}
}

// Run claims and processes executions until the context is done.
//
// It returns nil on cancellation: being told to stop is not a failure. A claimed
// execution left unfinished stays claimable once its lease expires, so shutting
// down mid-execution loses no work.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		worked, err := w.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// A claim-level failure is operational. The worker keeps serving
			// rather than exiting, because one poisoned row or one transient
			// database error must not take a worker offline.
			w.logger.Error("execution poll failed", "code", "execution_poll_failed")
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.idle):
		}
	}
}

// RunOnce claims at most one execution and processes it, reporting whether it
// found work.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	request, found, err := w.executions.Claim(ctx, w.lease)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	w.process(ctx, request)
	return true, nil
}

// process runs one claimed execution.
//
// It never returns an error: every outcome is recorded against the execution
// itself, which is where a caller looks. A panic is contained here so one bad
// execution cannot take the worker down.
func (w *Worker) process(ctx context.Context, request ports.ExecutionRequest) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// A panic is an internal defect, and execution is deterministic, so
			// a second attempt on identical input would panic identically.
			// Retrying is futile, so the execution is failed rather than left to
			// spin. The recovered value is deliberately discarded: it could
			// carry payload, and a bounded code is enough to find the cause in
			// logs and traces.
			w.logger.Error("execution panicked", "code", reasonInternalError)
			w.fail(ctx, request, reasonInternalError)
		}
	}()

	plan, found, err := w.plans.GetPlan(ctx, request.TenantID, request.PlanID)
	if err != nil {
		// Reaching storage failed. Repetition can plausibly succeed, so the
		// execution is left claimable rather than failed.
		w.logger.Error("plan could not be read", "code", "plan_read_failed")
		return
	}
	if !found {
		// The plan is gone, so this execution can never run. That is terminal.
		w.fail(ctx, request, reasonPlanAbsent)
		return
	}

	if err := w.verifyIdentity(plan, request); err != nil {
		// The claimed identity is not the one this input derives. Something
		// altered the row, and executing it would seal artifacts under an
		// identity the kernel never produced for these inputs.
		w.logger.Error("execution identity could not be reproduced", "code", reasonIdentityMismatch)
		w.fail(ctx, request, reasonIdentityMismatch)
		return
	}

	result, err := w.runner.Run(ctx, app.Request{
		Compilation:      plan.Input.Request(),
		InitialState:     request.Input.InitialState,
		World:            request.Input.World,
		ExecutorIdentity: request.Input.ExecutorIdentity,
		Policy:           request.Input.Policy,
	}, w.observer)
	if err != nil {
		w.recordInability(ctx, request, err)
		return
	}

	// A nil error means the computation produced an answer, including when that
	// answer is a refusal. Both are completed executions.
	if err := w.executions.Complete(ctx, projectResult(request, result)); err != nil {
		w.logger.Error("result could not be stored", "code", "result_store_failed")
	}
}

// verifyIdentity re-derives the execution identity and requires it to match the
// one the execution was claimed under.
//
// This is where the constraint that identities are re-derived rather than read
// and trusted is actually honoured for executions. The store cannot do it: an
// ExecutionID comes from binding a run against a plan, and the store has only a
// PlanID. The worker has both, so the check belongs here.
func (w *Worker) verifyIdentity(plan ports.PlanRecord, request ports.ExecutionRequest) error {
	compiled, ok := plan.Compilation.Plan()
	if !ok {
		return errors.New("stored plan carries no compiled plan")
	}
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             compiled,
		InitialState:     request.Input.InitialState,
		World:            request.Input.World,
		ExecutorIdentity: request.Input.ExecutorIdentity,
		Policy:           request.Input.Policy,
	})
	if err != nil {
		return fmt.Errorf("claimed input cannot be bound: %w", err)
	}
	if binding.ExecutionID() != request.ExecutionID {
		return errors.New("re-derived execution identity differs from the claimed one")
	}
	if binding.SemanticRunID() != request.RunID {
		return errors.New("re-derived run identity differs from the claimed one")
	}
	return nil
}

// recordInability classifies a machinery failure by whether repeating it could
// plausibly change the outcome.
//
// Retryable causes leave the execution claimable, so an expired lease brings it
// back. Deterministic ones are terminal, because leaving them claimable would
// spin forever on an input that cannot succeed.
func (w *Worker) recordInability(ctx context.Context, request ports.ExecutionRequest, cause error) {
	switch {
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		// Shutdown or a deadline. The work is untouched and will be reclaimed.
		w.logger.Info("execution abandoned before completion", "code", "execution_abandoned")
	case errors.As(cause, &app.InfrastructureUnavailableError{}):
		w.logger.Error("execution dependency unavailable", "code", "dependency_unavailable")
	case errors.As(cause, &app.InvalidInputError{}):
		// The pinned input is not usable and never will be.
		w.fail(ctx, request, reasonInvalidInput)
	default:
		// An internal inconsistency is deterministic on identical input.
		w.logger.Error("execution failed internally", "code", reasonInternalError)
		w.fail(ctx, request, reasonInternalError)
	}
}

func (w *Worker) fail(ctx context.Context, request ports.ExecutionRequest, reason string) {
	// A cancelled context cannot record anything, and the execution is better
	// left claimable than lost, so the attempt is skipped rather than forced.
	if ctx.Err() != nil {
		return
	}
	if err := w.executions.Fail(ctx, request.TenantID, request.ExecutionID, reason); err != nil {
		w.logger.Error("execution failure could not be recorded", "code", "failure_record_failed")
	}
}

// projectResult turns a spine result into the stored projection.
//
// The sealed artifacts' canonical bytes travel with their identities, because
// sealing produces an artifact and keeping only its digest would keep the receipt
// while discarding the goods.
func projectResult(request ports.ExecutionRequest, result app.SpineResult) ports.ExecutionResult {
	projected := ports.ExecutionResult{
		TenantID:    request.TenantID,
		ExecutionID: request.ExecutionID,
		Status:      ports.ExecutionSucceeded,
		SpineStatus: result.Status().String(),
	}
	if result.Status() != app.SpineSucceeded {
		// A deterministic refusal is still a completed execution; the lifecycle
		// status records that the computation did not succeed, while the result
		// records what it decided.
		projected.Status = ports.ExecutionFailed
	}
	if state, ok := result.State(); ok {
		projected.FinalStateDigest = state.Digest()
	}
	if prefix, ok := result.JournalPrefixDigest(); ok {
		projected.JournalPrefixDigest = prefix
	}
	if inputID, ok := result.InputID(); ok {
		projected.InputID = inputID
	}
	if worldID, ok := result.WorldID(); ok {
		projected.WorldID = worldID
	}
	for _, entry := range result.Journal().Entries() {
		projected.AcceptedRules = append(projected.AcceptedRules, entry.RuleID())
	}

	for _, artifact := range result.Checkpoints() {
		projected.Checkpoints = append(projected.Checkpoints, ports.SealedCheckpoint{
			CheckpointKey:        artifact.Checkpoint().Key,
			CheckpointID:         artifact.CheckpointID(),
			CheckpointArtifactID: artifact.ID(),
			Digest:               artifact.Digest(),
			StateDigest:          artifact.StateDigest(),
			CanonicalBytes:       artifact.CanonicalBytes(),
		})
	}

	keys := make(map[semantic.ProfileID]semantic.ProfileKey, len(result.Profiles()))
	for _, profile := range result.Profiles() {
		keys[profile.ID()] = profile.Key()
	}
	for _, assessment := range result.Assessments() {
		projected.Assessments = append(projected.Assessments, ports.StoredAssessment{
			AssessmentID:         assessment.ID(),
			Digest:               assessment.Digest(),
			CheckpointArtifactID: assessment.CheckpointArtifactID(),
			ProfileID:            assessment.ProfileID(),
			ProfileKey:           keys[assessment.ProfileID()],
			Verdict:              assessment.Verdict(),
			MissingRequirements:  missingRequirements(assessment),
			CanonicalBytes:       assessment.CanonicalBytes(),
		})
	}

	if failure, ok := result.SemanticFailure(); ok {
		projected.Failure = &ports.StoredFailure{Kind: failure.Kind(), Code: failureCode(failure)}
	}
	return projected
}

// missingRequirements collects the unsatisfied requirement codes, deduplicated
// across selected entities: the same requirement failing for several entities is
// one reason the checkpoint is not ready, not several.
func missingRequirements(assessment semantic.Assessment) []semantic.RequirementCode {
	seen := map[semantic.RequirementCode]bool{}
	codes := make([]semantic.RequirementCode, 0)
	for _, entity := range assessment.EntityResults() {
		for _, requirement := range entity.Results() {
			if requirement.Satisfied() || seen[requirement.Code()] {
				continue
			}
			seen[requirement.Code()] = true
			codes = append(codes, requirement.Code())
		}
	}
	slices.Sort(codes)
	return codes
}

func failureCode(failure semantic.FailureReport) string {
	if report, ok := failure.ArtifactIntegrity(); ok {
		return string(report.Code())
	}
	if operation := failure.OperationInvariantCode(); operation != "" {
		return string(operation)
	}
	return string(failure.InvariantCode())
}
