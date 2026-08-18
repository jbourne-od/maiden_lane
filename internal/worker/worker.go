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

// Worker claims and runs executions.
type Worker struct {
	plans      ports.PlanStore
	executions ports.ExecutionStore
	runner     SpineRunner
	observer   app.Observer
	tracer     ExecutionTracer
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
	Tracer     ExecutionTracer
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
		tracer: options.Tracer, logger: logger, lease: lease, idle: idle,
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

// process runs one claimed execution inside its own span.
//
// It never returns an error: every outcome is recorded against the execution
// itself, which is where a caller looks.
func (w *Worker) process(ctx context.Context, request ports.ExecutionRequest) {
	ctx, end := w.beginExecution(ctx, request)
	end(w.attempt(ctx, request))
}

// beginExecution opens the worker's span and returns both the context that
// parents everything the attempt does and the function that closes it.
//
// The returned context is the one the attempt must use. The spine's observer
// cannot replace the context it is given, so the phases of an execution are
// parented by whatever the worker passes down: handing the original context on
// would leave them rooted nowhere, exactly as they were before this existed.
func (w *Worker) beginExecution(
	ctx context.Context, request ports.ExecutionRequest,
) (context.Context, func(ExecutionOutcome)) {
	if w.tracer == nil {
		return ctx, func(ExecutionOutcome) {}
	}
	return w.tracer.BeginExecution(ctx, ExecutionObservation{
		PlanID:      request.PlanID,
		RunID:       request.RunID,
		ExecutionID: request.ExecutionID,
	})
}

// attempt is the body of one execution, reporting what the worker did with it.
//
// Every exit returns an outcome so telemetry cannot describe the execution
// differently from the store, and each outcome names what was actually recorded
// rather than what was intended: a terminal failure that could not be written
// leaves the execution claimable, and calling that failed would report a state
// no reader can observe.
//
// A panic is contained here so one bad execution cannot take the worker down.
// The recover runs before process closes the span, so a panicking execution
// still reports a terminal failure rather than whatever the zero value happens
// to be.
func (w *Worker) attempt(
	ctx context.Context, request ports.ExecutionRequest,
) (outcome ExecutionOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// A panic is an internal defect, and execution is deterministic, so
			// a second attempt on identical input would panic identically.
			// Retrying is futile, so the execution is failed rather than left to
			// spin. The recovered value is deliberately discarded: it could
			// carry payload, and a bounded code is enough to find the cause in
			// logs and traces.
			w.logger.ErrorContext(ctx, "execution panicked", "code", ReasonInternalError)
			outcome = w.fail(ctx, request, ReasonInternalError)
		}
	}()

	plan, found, err := w.plans.GetPlan(ctx, request.TenantID, request.PlanID)
	if err != nil {
		// Reaching storage failed. Repetition can plausibly succeed, so the
		// execution is left claimable rather than failed.
		w.logger.ErrorContext(ctx, "plan could not be read", "code", "plan_read_failed")
		return ExecutionOutcome{Kind: OutcomeAbandoned}
	}
	if !found {
		// The plan is gone, so this execution can never run. That is terminal.
		return w.fail(ctx, request, ReasonPlanAbsent)
	}

	if err := w.verifyIdentity(plan, request); err != nil {
		// The claimed identity is not the one this input derives. Something
		// altered the row, and executing it would seal artifacts under an
		// identity the kernel never produced for these inputs.
		w.logger.ErrorContext(ctx, "execution identity could not be reproduced",
			"code", ReasonIdentityMismatch)
		return w.fail(ctx, request, ReasonIdentityMismatch)
	}

	result, err := w.runner.Run(ctx, app.Request{
		Compilation:      plan.Input.Request(),
		InitialState:     request.Input.InitialState,
		World:            request.Input.World,
		ExecutorIdentity: request.Input.ExecutorIdentity,
		Policy:           request.Input.Policy,
	}, w.observer)
	if err != nil {
		return w.recordInability(ctx, request, err)
	}

	// A nil error means the computation produced an answer, including when that
	// answer is a refusal. Both are completed executions.
	projected, err := app.Project(request, result)
	if err != nil {
		// The artifacts contradict the identity the row claims. Unreachable given the
		// pre-execution check above, and refused rather than trusted for that reason.
		w.logger.ErrorContext(ctx, "projected result contradicts the claimed identity",
			"code", ReasonIdentityMismatch)
		return w.fail(ctx, request, ReasonIdentityMismatch)
	}
	if err := w.executions.Complete(ctx, projected); err != nil {
		// The computation answered and the answer was lost. The execution stays
		// claimable, so this is abandoned: reporting it answered would promise a
		// result no read can return.
		w.logger.ErrorContext(ctx, "result could not be stored", "code", "result_store_failed")
		return ExecutionOutcome{Kind: OutcomeAbandoned}
	}
	return ExecutionOutcome{Kind: OutcomeAnswered}
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
func (w *Worker) recordInability(
	ctx context.Context, request ports.ExecutionRequest, cause error,
) ExecutionOutcome {
	switch {
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		// Shutdown or a deadline. The work is untouched and will be reclaimed.
		w.logger.InfoContext(ctx, "execution abandoned before completion", "code", "execution_abandoned")
		return ExecutionOutcome{Kind: OutcomeAbandoned}
	case errors.As(cause, &app.InfrastructureUnavailableError{}):
		w.logger.ErrorContext(ctx, "execution dependency unavailable", "code", "dependency_unavailable")
		return ExecutionOutcome{Kind: OutcomeAbandoned}
	case errors.As(cause, &app.InvalidInputError{}):
		// The pinned input is not usable and never will be.
		return w.fail(ctx, request, ReasonInvalidInput)
	default:
		// An internal inconsistency is deterministic on identical input.
		w.logger.ErrorContext(ctx, "execution failed internally", "code", ReasonInternalError)
		return w.fail(ctx, request, ReasonInternalError)
	}
}

// fail records a terminal failure and reports whether that actually happened.
//
// The returned outcome is the recorded state, not the intended one. Both exits
// below leave the execution claimable, so both are abandoned: an operator reading
// "failed" should be able to trust that something terminal was written.
func (w *Worker) fail(
	ctx context.Context, request ports.ExecutionRequest, reason string,
) ExecutionOutcome {
	// A cancelled context cannot record anything, and the execution is better
	// left claimable than lost, so the attempt is skipped rather than forced.
	if ctx.Err() != nil {
		return ExecutionOutcome{Kind: OutcomeAbandoned}
	}
	if err := w.executions.Fail(ctx, request.TenantID, request.ExecutionID, reason); err != nil {
		w.logger.ErrorContext(ctx, "execution failure could not be recorded",
			"code", "failure_record_failed")
		return ExecutionOutcome{Kind: OutcomeAbandoned}
	}
	return ExecutionOutcome{Kind: OutcomeFailed, Reason: reason}
}
