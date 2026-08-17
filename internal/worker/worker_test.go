package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

// Production break caught: the ratified lifecycle must survive the queue. If the
// worker dropped a checkpoint, an assessment, or the accepted-rule order, the
// stored answer would understate what the run actually sealed and every later
// reader would inherit that.
func TestWorkerCompletesAPassingExecution(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)

	worked, err := fixture.worker.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !worked {
		t.Fatal("the worker found no work")
	}

	record := fixture.mustGet(t)
	if record.Status != ports.ExecutionSucceeded {
		t.Fatalf("status = %s, want succeeded", record.Status)
	}
	if record.Result == nil {
		t.Fatal("a completed execution carries no result")
	}
	if record.Result.SpineStatus != "succeeded" {
		t.Fatalf("spine status = %s", record.Result.SpineStatus)
	}
	if len(record.Result.Checkpoints) != 2 {
		t.Fatalf("checkpoints = %d, want 2", len(record.Result.Checkpoints))
	}
	if len(record.Result.Assessments) != 4 {
		t.Fatalf("assessments = %d, want 4", len(record.Result.Assessments))
	}
	for i, checkpoint := range record.Result.Checkpoints {
		if len(checkpoint.CanonicalBytes) == 0 {
			t.Errorf("checkpoint %d was stored without its artifact bytes", i)
		}
	}
	// Exactly one needs_input: the optimizer at the first checkpoint.
	needsInput := 0
	for _, assessment := range record.Result.Assessments {
		if assessment.Verdict == semantic.NeedsInput {
			needsInput++
			if len(assessment.MissingRequirements) == 0 {
				t.Error("a needs_input assessment recorded no missing requirements")
			}
		}
	}
	if needsInput != 1 {
		t.Fatalf("needs_input assessments = %d, want 1", needsInput)
	}
	if record.FailureReason != "" {
		t.Fatalf("a succeeded execution carries a failure reason: %q", record.FailureReason)
	}
}

// Production break caught: a deterministic refusal is an answer, so it must be a
// completed execution carrying a typed failure. Recording it as a worker failure
// would misreport a working system and invite a retry that can only reproduce it.
func TestWorkerCompletesARejectedExecution(t *testing.T) {
	fixture := newFixture(t, teamhos.AnchorMismatch)

	if _, err := fixture.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	record := fixture.mustGet(t)
	if record.Result == nil {
		t.Fatal("a rejected execution carries no result")
	}
	if record.Result.Failure == nil {
		t.Fatal("a rejected execution records no typed failure")
	}
	if record.Result.Failure.Code != "HOS_ANCHOR_MISMATCH" {
		t.Fatalf("failure code = %q", record.Result.Failure.Code)
	}
	// The verified prefix survives the refusal.
	if len(record.Result.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %d, want the retained one", len(record.Result.Checkpoints))
	}
	if len(record.Result.AcceptedRules) != 1 {
		t.Fatalf("accepted rules = %v, want only the committed transition", record.Result.AcceptedRules)
	}
	// Not an operational failure: nothing was unable to run.
	if record.FailureReason != "" {
		t.Fatalf("a semantic refusal was recorded as an operational failure: %q", record.FailureReason)
	}
	// The queue is done with it either way.
	if _, found, err := fixture.executions.Claim(t.Context(), time.Minute); err != nil || found {
		t.Fatalf("a completed rejection was claimable again: found=%t err=%v", found, err)
	}
}

// Production break caught: this is the assertion that makes at-least-once
// delivery defensible. If a reclaimed execution produced different artifacts, a
// duplicate attempt would fork the artifact graph and the queue would need
// exactly-once machinery to be correct.
func TestReclaimedExecutionProducesIdenticalArtifacts(t *testing.T) {
	first := newFixture(t, teamhos.Passing)
	if _, err := first.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	original := first.mustGet(t)

	// A second, independent queue holding the same request stands in for the
	// execution having been abandoned and reclaimed.
	second := newFixtureFrom(t, first.request)
	if _, err := second.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	repeated := second.mustGet(t)

	if repeated.Result.FinalStateDigest != original.Result.FinalStateDigest {
		t.Fatalf("final state digest = %s, want %s",
			repeated.Result.FinalStateDigest, original.Result.FinalStateDigest)
	}
	if repeated.Result.JournalPrefixDigest != original.Result.JournalPrefixDigest {
		t.Fatal("the accepted history differs between attempts")
	}
	if len(repeated.Result.Checkpoints) != len(original.Result.Checkpoints) {
		t.Fatalf("checkpoints = %d, want %d",
			len(repeated.Result.Checkpoints), len(original.Result.Checkpoints))
	}
	for i := range original.Result.Checkpoints {
		want, got := original.Result.Checkpoints[i], repeated.Result.Checkpoints[i]
		if got.CheckpointArtifactID != want.CheckpointArtifactID || got.Digest != want.Digest {
			t.Errorf("checkpoint %d identity differs between attempts", i)
		}
		if string(got.CanonicalBytes) != string(want.CanonicalBytes) {
			t.Errorf("checkpoint %d bytes differ between attempts", i)
		}
	}
	for i := range original.Result.Assessments {
		want, got := original.Result.Assessments[i], repeated.Result.Assessments[i]
		if got.AssessmentID != want.AssessmentID || got.Digest != want.Digest {
			t.Errorf("assessment %d identity differs between attempts", i)
		}
	}
}

// Production break caught: this closes the semantic half of the review finding
// that identities were read and trusted. If the claimed identity is not the one
// the pinned input derives, executing it would seal artifacts under an identity
// the kernel never produced for those inputs.
func TestWorkerRefusesAnExecutionWhoseIdentityDoesNotReproduce(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)

	// Enqueue the same input under a plausible but wrong execution identity, as
	// a tampered or mis-restored row would present it.
	tampered := fixture.request
	tampered.ExecutionID = semantic.ExecutionID("sha256:" +
		"9999999999999999999999999999999999999999999999999999999999999999")
	if _, err := fixture.executions.Enqueue(t.Context(), tampered); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Drain both: the genuine one completes, the tampered one is refused.
	for range 2 {
		if _, err := fixture.worker.RunOnce(t.Context()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	}

	record, found, err := fixture.executions.Get(t.Context(), fixture.request.TenantID, tampered.ExecutionID)
	if err != nil || !found {
		t.Fatalf("Get: found=%t err=%v", found, err)
	}
	if record.Status != ports.ExecutionFailed {
		t.Fatalf("status = %s, want failed", record.Status)
	}
	if record.FailureReason != "identity_mismatch" {
		t.Fatalf("reason = %q, want identity_mismatch", record.FailureReason)
	}
	if record.Result != nil {
		t.Fatal("a refused execution recorded a semantic result")
	}
}

// Production break caught: an execution whose plan is gone can never run, so it
// must reach a terminal state rather than being retried forever.
func TestWorkerFailsAnExecutionWithNoPlan(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	// A store with no plans stands in for a plan that is absent for this tenant.
	fixture.worker = worker.New(worker.Options{
		Plans:      memory.NewStore(),
		Executions: fixture.executions,
		Runner:     productionRunner{},
	})

	if _, err := fixture.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	record := fixture.mustGet(t)
	if record.Status != ports.ExecutionFailed || record.FailureReason != "plan_absent" {
		t.Fatalf("status=%s reason=%q, want failed/plan_absent", record.Status, record.FailureReason)
	}
}

// Production break caught: a retryable inability must leave the execution
// claimable. Marking it failed would be permanent, because enqueueing is
// idempotent on a derived identity and could never resubmit it.
func TestRetryableInabilityLeavesTheExecutionClaimable(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	fixture.worker = worker.New(worker.Options{
		Plans:      fixture.plans,
		Executions: fixture.executions,
		Runner: stubRunner{err: app.InfrastructureUnavailableError{
			Code: app.InfrastructureDependencyUnavailable, Cause: errors.New("upstream"),
		}},
		Lease: time.Millisecond,
	})

	if _, err := fixture.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	record := fixture.mustGet(t)
	if record.Status == ports.ExecutionFailed {
		t.Fatal("a retryable inability was recorded as a terminal failure")
	}
	if record.Result != nil {
		t.Fatal("an unfinished execution carries a result")
	}

	// Once the lease lapses it comes back.
	time.Sleep(30 * time.Millisecond)
	if _, found, err := fixture.executions.Claim(t.Context(), time.Minute); err != nil || !found {
		t.Fatalf("the execution did not become claimable again: found=%t err=%v", found, err)
	}
}

// Production break caught: a deterministic inability must be terminal, or the
// worker would spin forever on an input that cannot succeed.
func TestDeterministicInabilityIsTerminal(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	fixture.worker = worker.New(worker.Options{
		Plans:      fixture.plans,
		Executions: fixture.executions,
		Runner:     stubRunner{err: app.InvalidInputError{Code: app.InputRunBindingIncomplete}},
	})

	if _, err := fixture.worker.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	record := fixture.mustGet(t)
	if record.Status != ports.ExecutionFailed {
		t.Fatalf("status = %s, want failed", record.Status)
	}
	if record.FailureReason != "invalid_semantic_input" {
		t.Fatalf("reason = %q", record.FailureReason)
	}
}

// Production break caught: one bad execution must not take the worker down, and
// the panic must not vanish silently either.
func TestPanicIsContainedAndRecorded(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	fixture.worker = worker.New(worker.Options{
		Plans:      fixture.plans,
		Executions: fixture.executions,
		Runner:     stubRunner{panicValue: "deliberate"},
	})

	worked, err := fixture.worker.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce returned an error rather than containing the panic: %v", err)
	}
	if !worked {
		t.Fatal("the worker reported no work despite claiming one")
	}
	record := fixture.mustGet(t)
	if record.Status != ports.ExecutionFailed || record.FailureReason != "internal_error" {
		t.Fatalf("status=%s reason=%q, want failed/internal_error", record.Status, record.FailureReason)
	}
	// The recovered value could carry a payload, so it must not be stored.
	if record.FailureReason == "deliberate" {
		t.Fatal("the panic value was recorded")
	}
}

// Production break caught: Run must return on cancellation rather than spinning
// or blocking, and must not abandon a claim in a way that loses work.
func TestRunStopsOnCancellation(t *testing.T) {
	fixture := newFixture(t, teamhos.Passing)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- fixture.worker.Run(ctx) }()

	// Let it drain the queue, then stop it.
	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v; cancellation is not a failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if record := fixture.mustGet(t); record.Status != ports.ExecutionSucceeded {
		t.Fatalf("status = %s; the queued execution was not completed before shutdown", record.Status)
	}
}

type fixture struct {
	plans      *memory.Store
	executions *memory.Store
	worker     *worker.Worker
	request    ports.ExecutionRequest
}

func (f *fixture) mustGet(t *testing.T) ports.ExecutionRecord {
	t.Helper()
	record, found, err := f.executions.Get(t.Context(), f.request.TenantID, f.request.ExecutionID)
	if err != nil || !found {
		t.Fatalf("Get: found=%t err=%v", found, err)
	}
	return record
}

// newFixture builds a store holding the ratified team-HOS plan and one queued
// execution over it, with a worker wired to the real application use case.
func newFixture(t *testing.T, variant teamhos.Variant) *fixture {
	t.Helper()
	return newFixtureFrom(t, requestFor(t, variant))
}

func newFixtureFrom(t *testing.T, request ports.ExecutionRequest) *fixture {
	t.Helper()
	store := memory.NewStore()

	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	schema, err := semantic.NewSchema(
		inputs.Compilation.Schema.EntityDeclarations(),
		inputs.Compilation.Schema.RelationDeclarations())
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	if err := store.PutPlan(t.Context(), ports.PlanRecord{
		TenantID: request.TenantID, PlanID: plan.ID(),
		Input: compilation.Input(), Schema: schema, Compilation: compilation,
	}); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	if _, err := store.Enqueue(t.Context(), request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	return &fixture{
		plans: store, executions: store, request: request,
		worker: worker.New(worker.Options{
			Plans: store, Executions: store, Runner: productionRunner{},
		}),
	}
}

// requestFor derives a queued execution from a fixture variant, using a real
// binding so the identities are the ones the kernel actually produces.
func requestFor(t *testing.T, variant teamhos.Variant) ports.ExecutionRequest {
	t.Helper()
	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	// The plan always comes from the passing variant, since the variants differ
	// only in their observations, not their declarations.
	planInputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	compilation, err := semantic.Compile(planInputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, _ := compilation.Plan()

	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: inputs.InitialState, World: inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	return ports.ExecutionRequest{
		TenantID:    "acme",
		ExecutionID: binding.ExecutionID(),
		RunID:       binding.SemanticRunID(),
		PlanID:      plan.ID(),
		Input: ports.ExecutionInput{
			InitialState:     inputs.InitialState,
			World:            inputs.World,
			ExecutorIdentity: inputs.ExecutorIdentity,
			Policy:           inputs.Policy,
		},
	}
}

type productionRunner struct{}

func (productionRunner) Run(ctx context.Context, request app.Request, observer app.Observer) (app.SpineResult, error) {
	return app.Run(ctx, request, observer)
}

// stubRunner injects an outcome the real spine cannot be made to produce on
// demand, so the worker's classification of inability can be driven directly.
type stubRunner struct {
	err        error
	panicValue string
}

func (s stubRunner) Run(context.Context, app.Request, app.Observer) (app.SpineResult, error) {
	if s.panicValue != "" {
		panic(s.panicValue)
	}
	return app.SpineResult{}, s.err
}
