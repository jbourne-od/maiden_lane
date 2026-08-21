package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

// The contract these tests hold: submission returns identities and never a
// result, a read reports the lifecycle honestly, and a deterministic semantic
// refusal is a finished execution carrying a typed failure.
//
// A refusal is not a problem document, because the computation produced a real
// answer. Reporting it as 5xx would page an operator for a working system and
// invite a retry that can only reproduce it.

// Production break caught: submission must not block on execution. Returning a
// result here would reinstate the synchronous behaviour this slice retired and
// couple every response to worker availability.
func TestCreateExecutionAcceptsWithoutRunning(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))

	recorder := postJSON(t, fixture.router, "/v1/executions", "acme",
		executionRequest(t, plan.PlanID, teamhos.Passing))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}

	var accepted openapiv1.ExecutionAccepted
	decodeBody(t, recorder, &accepted)
	if accepted.ExecutionID == "" || accepted.SemanticRunID == "" {
		t.Fatalf("accepted response omitted identities: %+v", accepted)
	}
	if accepted.PlanID != plan.PlanID {
		t.Fatalf("planID = %s, want %s", accepted.PlanID, plan.PlanID)
	}
	if accepted.ExecutionStatus != openapiv1.ExecutionStatusPending {
		t.Fatalf("status = %s, want pending", accepted.ExecutionStatus)
	}

	// Nothing has run, and the read says so rather than inventing a result.
	execution := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if execution.ExecutionStatus != openapiv1.ExecutionStatusPending {
		t.Fatalf("status = %s, want pending", execution.ExecutionStatus)
	}
	if execution.Result != nil {
		t.Fatal("a pending execution carries a result")
	}
}

// Production break caught: identity is derived, not allocated, so resubmitting
// must return the same identities and create no second execution. If it
// allocated, the queue would need a deduplication key and an expiry policy to
// recover a property the identity function already provides.
func TestResubmissionIsIdempotent(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	request := executionRequest(t, plan.PlanID, teamhos.Passing)

	first := acceptExecution(t, fixture.router, "acme", request)
	second := acceptExecution(t, fixture.router, "acme", request)

	if first.ExecutionID != second.ExecutionID {
		t.Fatalf("resubmission produced %s then %s", first.ExecutionID, second.ExecutionID)
	}
	if first.SemanticRunID != second.SemanticRunID {
		t.Fatal("resubmission changed the semantic run identity")
	}

	// Exactly one execution exists to be worked.
	if _, found, err := fixture.store.Claim(t.Context(), time.Minute); err != nil || !found {
		t.Fatalf("Claim: found=%t err=%v", found, err)
	}
	if _, found, err := fixture.store.Claim(t.Context(), time.Minute); err != nil || found {
		t.Fatalf("a second execution was queued: found=%t err=%v", found, err)
	}
}

// Production break caught: changing only the executor must preserve what was
// computed and change only who computed it, or the two identities do not mean
// what the design says they mean.
func TestExecutorIdentityChangesOnlyTheExecution(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	request := executionRequest(t, plan.PlanID, teamhos.Passing)

	first := acceptExecution(t, fixture.router, "acme", request)

	other := request
	other.ExecutorIdentity.Version =
		"sha256:3d1c8f2b6a5e4d7c9b0a1f2e3d4c5b6a7988776655443322110ffeeddccbbaa9"
	second := acceptExecution(t, fixture.router, "acme", other)

	if second.SemanticRunID != first.SemanticRunID {
		t.Fatal("a different executor changed the semantic run")
	}
	if second.ExecutionID == first.ExecutionID {
		t.Fatal("a different executor produced the same execution identity")
	}
}

// Production break caught: the ratified lifecycle must survive the queue and both
// projections. A dropped checkpoint or assessment would understate what the run
// actually sealed.
func TestCompletedExecutionReportsTheWholeResult(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	accepted := acceptExecution(t, fixture.router, "acme", executionRequest(t, plan.PlanID, teamhos.Passing))

	fixture.drain(t)

	execution := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if execution.ExecutionStatus != openapiv1.ExecutionStatusSucceeded {
		t.Fatalf("status = %s, want succeeded", execution.ExecutionStatus)
	}
	if execution.Result == nil {
		t.Fatal("a finished execution carries no result")
	}
	if execution.Result.SpineStatus != openapiv1.ExecutionResultSpineStatusSucceeded {
		t.Fatalf("spine status = %s", execution.Result.SpineStatus)
	}
	if len(execution.Result.Checkpoints) != 2 {
		t.Fatalf("checkpoints = %d, want 2", len(execution.Result.Checkpoints))
	}
	if len(execution.Result.Assessments) != 4 {
		t.Fatalf("assessments = %d, want 4", len(execution.Result.Assessments))
	}
	if execution.Result.AcceptedRules == nil ||
		!slices.Equal(*execution.Result.AcceptedRules, []string{"form_team.v1", "aggregate_team_hos.v1"}) {
		t.Fatalf("accepted rules = %v", execution.Result.AcceptedRules)
	}
	for _, identity := range []*openapiv1.Digest{
		execution.Result.InputID, execution.Result.WorldID,
		execution.Result.FinalStateDigest, execution.Result.JournalPrefixDigest,
	} {
		if identity == nil || *identity == "" {
			t.Error("a finished result omitted an advertised identity")
		}
	}
	if execution.Result.Failure != nil {
		t.Fatalf("a successful execution reports a failure: %+v", execution.Result.Failure)
	}
	if execution.FailureReason != nil {
		t.Fatalf("a successful execution reports an operational failure: %v", *execution.FailureReason)
	}
}

// Production break caught: a deterministic refusal is a finished execution with a
// typed failure, not an operational failure and not a problem document.
func TestRejectedExecutionIsReportedAsAnAnswer(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	accepted := acceptExecution(t, fixture.router, "acme",
		executionRequest(t, plan.PlanID, teamhos.AnchorMismatch))

	fixture.drain(t)

	execution := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if execution.Result == nil || execution.Result.Failure == nil {
		t.Fatal("a rejected execution reports no typed failure")
	}
	if execution.Result.Failure.Code == nil || *execution.Result.Failure.Code != "SELECTION_GUARD_UNSATISFIED" {
		t.Fatalf("failure code = %v", *execution.Result.Failure.Code)
	}
	if execution.Result.Failure.Kind != openapiv1.SemanticFailureKindProtectedInvariantFailed {
		t.Fatalf("failure kind = %s", execution.Result.Failure.Kind)
	}
	// The verified prefix survives the refusal.
	if len(execution.Result.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %d, want the retained one", len(execution.Result.Checkpoints))
	}
	if len(execution.Result.Assessments) != 2 {
		t.Fatalf("assessments = %d, want 2", len(execution.Result.Assessments))
	}
	// Nothing was unable to run, so no operational reason is reported.
	if execution.FailureReason != nil {
		t.Fatalf("a semantic refusal was reported operationally: %v", *execution.FailureReason)
	}
}

// Production break caught: another tenant's execution must be indistinguishable
// from one that never existed.
func TestGetExecutionHidesOtherTenants(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	accepted := acceptExecution(t, fixture.router, "acme", executionRequest(t, plan.PlanID, teamhos.Passing))

	foreign := get(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID), "globex")
	absent := get(t, fixture.router, "/v1/executions/sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000", "acme")

	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant status = %d, want 404", foreign.Code)
	}
	if foreign.Body.String() != absent.Body.String() {
		t.Fatalf("a foreign execution is distinguishable from an absent one:\n%s\n%s",
			foreign.Body.String(), absent.Body.String())
	}
}

// Production break caught: input the kernel cannot bind must be refused at
// submission rather than queued. A queued execution that cannot run reaches a
// terminal failure, and because identity is derived, resubmitting would return
// that same failed row forever.
func TestUnbindableInputIsRefusedAtSubmission(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))

	tests := []struct {
		name   string
		mutate func(*openapiv1.CreateExecutionRequest)
	}{
		{"undeclared field", func(r *openapiv1.CreateExecutionRequest) {
			r.InitialState.Entities[0].Fields["not_in_schema"] = openapiv1.Value{
				Kind: openapiv1.ValueKindString, String: stringPtr("x"),
			}
		}},
		{"malformed executor version", func(r *openapiv1.CreateExecutionRequest) {
			r.ExecutorIdentity.Version = "v1.2.3"
		}},
		{"empty lineage", func(r *openapiv1.CreateExecutionRequest) {
			r.InitialState.Lineage.RootKey = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := executionRequest(t, plan.PlanID, teamhos.Passing)
			test.mutate(&request)

			recorder := postJSON(t, fixture.router, "/v1/executions", "acme", request)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
			}
			// Nothing was queued.
			if _, found, err := fixture.store.Claim(t.Context(), time.Minute); err != nil || found {
				t.Fatalf("a refused submission queued work: found=%t err=%v", found, err)
			}
		})
	}
}

// Production break caught: an execution referencing a plan this tenant cannot see
// must be absent, not queued against someone else's plan.
func TestExecutionAgainstAForeignPlanIsAbsent(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))

	recorder := postJSON(t, fixture.router, "/v1/executions", "globex",
		executionRequest(t, plan.PlanID, teamhos.Passing))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
	if _, found, err := fixture.store.Claim(t.Context(), time.Minute); err != nil || found {
		t.Fatalf("a foreign submission queued work: found=%t err=%v", found, err)
	}
}

type executionFixture struct {
	store  *memory.Store
	router http.Handler
	worker *worker.Worker
}

// drain runs the queue to completion, standing in for a worker process.
func (f *executionFixture) drain(t *testing.T) {
	t.Helper()
	for range 8 {
		worked, err := f.worker.RunOnce(t.Context())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("the queue did not drain")
}

func newExecutionFixture(t *testing.T) *executionFixture {
	t.Helper()
	store := memory.NewStore()
	return &executionFixture{
		store:  store,
		router: NewRouter(Dependencies{Plans: store, Executions: store}),
		worker: worker.New(worker.Options{
			Plans: store, Executions: store, Runner: spineRunner{},
		}),
	}
}

type spineRunner struct{}

func (spineRunner) Run(ctx context.Context, request app.Request, observer app.Observer) (app.SpineResult, error) {
	return app.Run(ctx, request, observer)
}

func acceptExecution(t *testing.T, router http.Handler, tenant string, request openapiv1.CreateExecutionRequest) openapiv1.ExecutionAccepted {
	t.Helper()
	recorder := postJSON(t, router, "/v1/executions", tenant, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("accept status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	var accepted openapiv1.ExecutionAccepted
	decodeBody(t, recorder, &accepted)
	return accepted
}

func getExecution(t *testing.T, router http.Handler, tenant string, executionID openapiv1.Digest) openapiv1.Execution {
	t.Helper()
	recorder := get(t, router, "/v1/executions/"+string(executionID), tenant)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var execution openapiv1.Execution
	decodeBody(t, recorder, &execution)
	return execution
}

// executionRequest renders a fixture variant as the wire document a client would
// send: source keys and a lineage, never entity identities.
func executionRequest(t *testing.T, planID openapiv1.Digest, variant teamhos.Variant) openapiv1.CreateExecutionRequest {
	t.Helper()
	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}

	lineage := inputs.InitialState.InputLineageID()
	entities := make([]openapiv1.EntityInput, 0, 2)
	for _, entity := range inputs.InitialState.Entities() {
		key := ""
		for _, candidate := range []string{"A", "B"} {
			if semantic.SourceEntityID(lineage, entity.Ref().Kind, candidate) == entity.Ref().ID {
				key = candidate
			}
		}
		if key == "" {
			t.Fatalf("entity %v has no ratified source key", entity.Ref())
		}
		fields := map[string]openapiv1.Value{}
		for name, value := range entity.Fields() {
			fields[string(name)] = valueToWire(value)
		}
		entities = append(entities, openapiv1.EntityInput{
			Kind:               string(entity.Ref().Kind),
			CanonicalSourceKey: key,
			Fields:             fields,
		})
	}

	return openapiv1.CreateExecutionRequest{
		PlanID: planID,
		InitialState: openapiv1.StateInput{
			Lineage: openapiv1.InputLineage{
				Namespace: "maiden-lane.sanitized-fixture",
				RootKey:   "team-hos-team-ab",
			},
			Entities: entities,
		},
		ExecutorIdentity: openapiv1.ExecutorIdentity{
			Backend: "go",
			Version: "sha256:1c0d5a3e9b7f2c4d6a8e0b1f3d5c7a9e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a",
		},
		ProvenancePolicy: openapiv1.CreateExecutionRequestProvenancePolicyChangesV1,
	}
}

func stringPtr(s string) *string { return &s }

func TestReattemptFailedExecution(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	accepted := acceptExecution(t, fixture.router, "acme", executionRequest(t, plan.PlanID, teamhos.Passing))

	// Claim and fail the attempt
	attempt, ok, err := fixture.store.Claim(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v, err=%v", ok, err)
	}
	if err := fixture.store.Fail(context.Background(), "acme", semantic.ExecutionID(accepted.ExecutionID), attempt.AttemptID, "worker_crashed"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Verify status is failed
	exec := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if exec.ExecutionStatus != openapiv1.ExecutionStatusFailed {
		t.Fatalf("status = %s, want failed", exec.ExecutionStatus)
	}

	// Reattempt execution -> 202 Accepted
	reattemptRecorder := postJSON(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID)+"/reattempt", "acme", nil)
	if reattemptRecorder.Code != http.StatusAccepted {
		t.Fatalf("reattempt status = %d, want 202; body = %s", reattemptRecorder.Code, reattemptRecorder.Body.String())
	}

	// Verify status returned to pending
	execAfter := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if execAfter.ExecutionStatus != openapiv1.ExecutionStatusPending {
		t.Fatalf("status after reattempt = %s, want pending", execAfter.ExecutionStatus)
	}

	// Reattempt while pending -> 409 Conflict
	conflictRecorder := postJSON(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID)+"/reattempt", "acme", nil)
	if conflictRecorder.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", conflictRecorder.Code)
	}

	// Reattempt non-existent execution -> 404 Not Found
	missingRecorder := postJSON(t, fixture.router, "/v1/executions/sha256:0000000000000000000000000000000000000000000000000000000000000000/reattempt", "acme", nil)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing execution status = %d, want 404", missingRecorder.Code)
	}
}

func TestGetExecutionCheckpoint(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	accepted := acceptExecution(t, fixture.router, "acme", executionRequest(t, plan.PlanID, teamhos.Passing))

	// Run worker to finish the execution
	workerInstance := worker.New(worker.Options{
		Plans:      fixture.store,
		Executions: fixture.store,
		Runner:     spineRunner{},
	})
	ran, err := workerInstance.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce: ran=%v, err=%v", ran, err)
	}

	// 1. GET valid checkpoint -> 200 OK
	recorder := get(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID)+"/checkpoints/team_formed.v1", "acme")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET checkpoint status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	var detail openapiv1.ExecutionCheckpointDetail
	decodeBody(t, recorder, &detail)
	if detail.CheckpointKey != "team_formed.v1" || detail.CheckpointID == "" || detail.CheckpointArtifactID == "" {
		t.Fatalf("unexpected checkpoint detail: %+v", detail)
	}
	if len(detail.Assessments) == 0 {
		t.Fatalf("expected assessments on checkpoint detail, got 0")
	}

	// 2. GET non-existent checkpoint key on finished execution -> 404 Not Found
	missingCPRecorder := get(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID)+"/checkpoints/unknown_checkpoint", "acme")
	if missingCPRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing checkpoint status = %d, want 404", missingCPRecorder.Code)
	}

	// 3. GET checkpoint cross-tenant -> 404 Not Found
	crossRecorder := get(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID)+"/checkpoints/team_formed.v1", "other-tenant")
	if crossRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status = %d, want 404", crossRecorder.Code)
	}

	// 4. GET checkpoint when store is unavailable -> 503 Service Unavailable
	unavailRouter := NewRouter(Dependencies{})
	unavailRecorder := get(t, unavailRouter, "/v1/executions/"+string(accepted.ExecutionID)+"/checkpoints/team_formed.v1", "acme")
	if unavailRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavail status = %d, want 503", unavailRecorder.Code)
	}
}

func TestReattemptSemanticRefusalIsRefused(t *testing.T) {
	fixture := newExecutionFixture(t)
	plan := createPlan(t, fixture.router, "acme", fixtureDeclarations(t))
	// Use invariant-violating variant to cause a deterministic semantic failure
	accepted := acceptExecution(t, fixture.router, "acme", executionRequest(t, plan.PlanID, teamhos.AnchorMismatch))

	// Run worker to execute and fail semantically
	workerInstance := worker.New(worker.Options{
		Plans:      fixture.store,
		Executions: fixture.store,
		Runner:     spineRunner{},
	})
	ran, err := workerInstance.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce: ran=%v, err=%v", ran, err)
	}

	exec := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if exec.ExecutionStatus != openapiv1.ExecutionStatusFailed || exec.Result == nil || exec.Result.Failure == nil {
		t.Fatalf("expected failed execution with semantic result, got %+v", exec)
	}

	// Attempting to reattempt a deterministically failed execution -> 409 Conflict
	reattemptRecorder := postJSON(t, fixture.router, "/v1/executions/"+string(accepted.ExecutionID)+"/reattempt", "acme", nil)
	if reattemptRecorder.Code != http.StatusConflict {
		t.Fatalf("reattempt status = %d, want 409; body = %s", reattemptRecorder.Code, reattemptRecorder.Body.String())
	}
}

func TestGetExecutionCheckpointWithZeroAssessmentsSerializesEmptyArray(t *testing.T) {
	store := memory.NewStore()
	router := NewRouter(Dependencies{Executions: store})

	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}

	world, _ := semantic.NewWorld(nil)
	executor, _ := semantic.NewExecutorIdentity("go", "sha256:1c0d5a3e9b7f2c4d6a8e0b1f3d5c7a9e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a")

	execReq := ports.ExecutionRequest{
		TenantID:    "acme",
		ExecutionID: semantic.ExecutionID("sha256:0000000000000000000000000000000000000000000000000000000000000003"),
		RunID:       semantic.SemanticRunID("sha256:0000000000000000000000000000000000000000000000000000000000000002"),
		PlanID:      semantic.PlanID("sha256:0000000000000000000000000000000000000000000000000000000000000001"),
		Input: ports.ExecutionInput{
			InitialState:     inputs.InitialState,
			World:            world,
			ExecutorIdentity: executor,
			Policy:           semantic.ChangesProvenance,
		},
	}

	if _, err := store.Enqueue(context.Background(), execReq); err != nil {
		t.Fatalf("store.Enqueue: %v", err)
	}

	attempt, ok, err := store.Claim(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("store.Claim: ok=%v, err=%v", ok, err)
	}

	result := ports.ExecutionResult{
		TenantID:    "acme",
		ExecutionID: execReq.ExecutionID,
		Status:      ports.ExecutionSucceeded,
		SpineStatus: "succeeded",
		Checkpoints: []ports.SealedCheckpoint{
			{
				CheckpointKey:        "checkpoint_no_assessments",
				CheckpointID:         "sha256:0000000000000000000000000000000000000000000000000000000000000010",
				CheckpointArtifactID: "sha256:0000000000000000000000000000000000000000000000000000000000000020",
				Digest:               "sha256:0000000000000000000000000000000000000000000000000000000000000030",
				StateDigest:          "sha256:0000000000000000000000000000000000000000000000000000000000000040",
			},
		},
		Assessments: nil, // Zero assessments
	}

	if err := store.Complete(context.Background(), attempt.AttemptID, result); err != nil {
		t.Fatalf("store.Complete: %v", err)
	}

	recorder := get(t, router, "/v1/executions/"+string(execReq.ExecutionID)+"/checkpoints/checkpoint_no_assessments", "acme")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET checkpoint status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}

	// Verify JSON body contains `"assessments":[]` and not `"assessments":null`
	bodyStr := recorder.Body.String()
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"assessments":[]`)) {
		t.Fatalf("expected assessments to serialize as [], body = %s", bodyStr)
	}
}
