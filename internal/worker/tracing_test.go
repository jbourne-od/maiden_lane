package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

// recordingTracer captures what the worker reported, and marks the context it
// returns so a test can prove the worker used the derived one.
type recordingTracer struct {
	began    []worker.ExecutionObservation
	outcomes []worker.ExecutionOutcome
	key      *int
}

func newRecordingTracer() *recordingTracer {
	return &recordingTracer{key: new(int)}
}

func (t *recordingTracer) BeginExecution(
	ctx context.Context, observation worker.ExecutionObservation,
) (context.Context, func(worker.ExecutionOutcome)) {
	t.began = append(t.began, observation)
	return context.WithValue(ctx, t.key, true), func(outcome worker.ExecutionOutcome) {
		t.outcomes = append(t.outcomes, outcome)
	}
}

// Production break caught: the worker's span only roots the spine's phases if
// the context it derives is the one handed to the use case. Passing the original
// context on would still open and close a worker span, so the trace would look
// entirely plausible while the phase spans remained parentless -- which is the
// defect this whole mechanism exists to remove.
func TestWorkerRunsTheSpineInsideTheTracedContext(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	tracer := newRecordingTracer()

	var sawTracedContext bool
	probe := contextProbeRunner{seen: func(ctx context.Context) {
		sawTracedContext = ctx.Value(tracer.key) != nil
	}}

	traced := worker.New(worker.Options{
		Plans: fixture.plans, Executions: fixture.executions,
		Runner: probe, Tracer: tracer,
	})
	if _, err := traced.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(tracer.began) != 1 {
		t.Fatalf("BeginExecution calls = %d, want 1", len(tracer.began))
	}
	if !sawTracedContext {
		t.Fatal("the spine ran outside the traced context, so its phases would still have no root")
	}
}

// The span has to be findable from the identities the API already handed the
// caller, so the observation must carry them.
func TestWorkerReportsTheClaimedIdentitiesToTheTracer(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	tracer := newRecordingTracer()

	traced := worker.New(worker.Options{
		Plans: fixture.plans, Executions: fixture.executions,
		Runner: productionRunner{}, Tracer: tracer,
	})
	if _, err := traced.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	observed := tracer.began[0]
	if observed.ExecutionID != fixture.request.ExecutionID {
		t.Fatalf("executionID = %q, want %q", observed.ExecutionID, fixture.request.ExecutionID)
	}
	if observed.RunID != fixture.request.RunID {
		t.Fatalf("runID = %q, want %q", observed.RunID, fixture.request.RunID)
	}
	if observed.PlanID != fixture.request.PlanID {
		t.Fatalf("planID = %q, want %q", observed.PlanID, fixture.request.PlanID)
	}
}

// Production break caught: the reported outcome must describe what was actually
// recorded against the execution, not what the worker set out to do. The cases
// below are the ones where those differ, and each is a state an operator would
// otherwise read as settled when the work is in fact still coming back.
func TestWorkerOutcomesDescribeWhatWasRecorded(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		build    func(*testing.T, *fixture, *recordingTracer) *worker.Worker
		wantKind worker.OutcomeKind
		wantWhy  string
	}{
		{
			name: "a stored result is answered",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans: f.plans, Executions: f.executions,
					Runner: productionRunner{}, Tracer: tracer,
				})
			},
			wantKind: worker.OutcomeAnswered,
		},
		{
			name: "an absent plan is a recorded failure",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans: memory.NewStore(), Executions: f.executions,
					Runner: productionRunner{}, Tracer: tracer,
				})
			},
			wantKind: worker.OutcomeFailed,
			wantWhy:  worker.ReasonPlanAbsent,
		},
		{
			name: "a retryable inability leaves it claimable",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans: f.plans, Executions: f.executions, Tracer: tracer,
					Runner: stubRunner{err: app.InfrastructureUnavailableError{
						Code: app.InfrastructureDependencyUnavailable, Cause: errors.New("upstream"),
					}},
				})
			},
			wantKind: worker.OutcomeAbandoned,
		},
		{
			name: "a deterministic inability is a recorded failure",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans: f.plans, Executions: f.executions, Tracer: tracer,
					Runner: stubRunner{err: app.InvalidInputError{Code: app.InputRunBindingIncomplete}},
				})
			},
			wantKind: worker.OutcomeFailed,
			wantWhy:  worker.ReasonInvalidInput,
		},
		{
			name: "a panic is a terminal internal failure",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans: f.plans, Executions: f.executions, Tracer: tracer,
					Runner: stubRunner{panicValue: "defect"},
				})
			},
			wantKind: worker.OutcomeFailed,
			wantWhy:  worker.ReasonInternalError,
		},
		{
			// The worker decided to fail it and could not write that decision, so
			// the execution is still claimable. Reporting failure here would
			// describe a state no reader can observe.
			name: "a failure that could not be written is abandoned",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans:      memory.NewStore(),
					Executions: unwritableFailures{Store: f.executions, err: errors.New("cannot write")},
					Runner:     productionRunner{}, Tracer: tracer,
				})
			},
			wantKind: worker.OutcomeAbandoned,
		},
		{
			// The computation answered and the answer was lost. Reporting it
			// answered would claim a result nothing can return.
			name: "a result that could not be stored is abandoned",
			build: func(_ *testing.T, f *fixture, tracer *recordingTracer) *worker.Worker {
				return worker.New(worker.Options{
					Plans:      f.plans,
					Executions: unwritableResults{Store: f.executions, err: errors.New("cannot write")},
					Runner:     productionRunner{}, Tracer: tracer,
				})
			},
			wantKind: worker.OutcomeAbandoned,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newFixture(t, teamhos.Passing)
			tracer := newRecordingTracer()

			if _, err := testCase.build(t, fixture, tracer).RunOnce(t.Context()); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}

			if len(tracer.outcomes) != 1 {
				t.Fatalf("outcomes reported = %d, want exactly 1", len(tracer.outcomes))
			}
			outcome := tracer.outcomes[0]
			if outcome.Kind != testCase.wantKind {
				t.Fatalf("outcome kind = %d, want %d", outcome.Kind, testCase.wantKind)
			}
			if outcome.Reason != testCase.wantWhy {
				t.Fatalf("outcome reason = %q, want %q", outcome.Reason, testCase.wantWhy)
			}
		})
	}
}

// Telemetry is non-authoritative, so its absence cannot change an execution's
// fate. A worker with no tracer must behave exactly as one with a tracer.
func TestWorkerWithoutATracerCompletesIdentically(t *testing.T) {
	untraced := newFixture(t, teamhos.Passing)
	if _, err := untraced.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	withoutTracer := untraced.mustGet(t)

	traced := newFixture(t, teamhos.Passing)
	tracedWorker := worker.New(worker.Options{
		Plans: traced.plans, Executions: traced.executions,
		Runner: productionRunner{}, Tracer: newRecordingTracer(),
	})
	if _, err := tracedWorker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	withTracer := traced.mustGet(t)

	if withoutTracer.Status != withTracer.Status {
		t.Fatalf("status differed: %s without a tracer, %s with one",
			withoutTracer.Status, withTracer.Status)
	}
	if withoutTracer.Result == nil || withTracer.Result == nil {
		t.Fatal("a completed execution carries no result")
	}
	if withoutTracer.Result.FinalStateDigest != withTracer.Result.FinalStateDigest {
		t.Fatal("tracing changed the artifacts the execution produced")
	}
}

// contextProbeRunner reports the context the worker handed the use case, then
// runs the real spine so the execution still completes normally.
type contextProbeRunner struct {
	seen func(context.Context)
}

func (r contextProbeRunner) Run(
	ctx context.Context, request app.Request, observer app.Observer,
) (app.SpineResult, error) {
	r.seen(ctx)
	return app.Run(ctx, request, observer)
}

// unwritableFailures accepts everything except recording a failure.
type unwritableFailures struct {
	*memory.Store
	err error
}

func (s unwritableFailures) Fail(
	context.Context, ports.TenantID, semantic.ExecutionID, ports.AttemptID, string,
) error {
	return s.err
}

// unwritableResults accepts everything except storing a result.
type unwritableResults struct {
	*memory.Store
	err error
}

func (s unwritableResults) Complete(context.Context, ports.AttemptID, ports.ExecutionResult) error {
	return s.err
}
