package httpapi

import (
	"net/http"
	"slices"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// The contract these tests exist to hold: a deterministic semantic outcome is
// a successful response, not a problem document.
//
// A failed protected invariant means the computation correctly refused to
// commit. That is a finding the caller asked for, delivered exactly as
// requested; answering 5xx would tell an operator their service is broken when
// it is working precisely as designed, and would invite a retry that can only
// produce the identical refusal.

// Production break caught: the passing incident must return the whole verified
// frontier. A response missing a checkpoint or an assessment would understate
// what was actually sealed and assessed.
func TestCreateExecutionReturnsTheCompleteResult(t *testing.T) {
	router := newTestRouter(t)
	plan := createPlan(t, router, "acme", fixtureDeclarations(t))

	recorder := postJSON(t, router, "/v1/executions", "acme",
		executionRequest(t, plan.PlanID, teamhos.Passing))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	var execution openapiv1.Execution
	decodeBody(t, recorder, &execution)

	if execution.SpineStatus != openapiv1.ExecutionSpineStatusSucceeded {
		t.Fatalf("spineStatus = %s, want succeeded", execution.SpineStatus)
	}
	if execution.ExecutionStatus == nil || *execution.ExecutionStatus != openapiv1.ExecutionExecutionStatusSucceeded {
		t.Fatalf("executionStatus = %v, want succeeded", execution.ExecutionStatus)
	}
	if execution.Failure != nil {
		t.Fatalf("a successful spine reported a failure: %+v", execution.Failure)
	}
	if len(execution.Checkpoints) != 2 {
		t.Fatalf("checkpoints = %d, want 2", len(execution.Checkpoints))
	}
	if len(execution.Assessments) != 4 {
		t.Fatalf("assessments = %d, want 4", len(execution.Assessments))
	}
	if execution.AcceptedRules == nil ||
		!slices.Equal(*execution.AcceptedRules, []string{"form_team.v1", "aggregate_team_hos.v1"}) {
		t.Fatalf("acceptedRules = %v", execution.AcceptedRules)
	}
	if execution.SemanticRunID == nil || execution.ExecutionID == nil {
		t.Fatal("execution omitted its run or execution identity")
	}

	// The readiness answers are the point of the whole lifecycle: cm is ready at
	// both checkpoints, optimizer only after the aggregate commits.
	verdicts := map[string]openapiv1.ReadinessVerdict{}
	for _, assessment := range execution.Assessments {
		verdicts[assessment.ProfileKey+"@"+string(assessment.CheckpointArtifactID)] = assessment.Verdict
	}
	needsInput := 0
	for _, verdict := range verdicts {
		if verdict == openapiv1.ReadinessVerdictNeedsInput {
			needsInput++
		}
	}
	if needsInput != 1 {
		t.Fatalf("needs_input verdicts = %d, want exactly 1 (optimizer at C1)", needsInput)
	}
}

// Production break caught: rendering a protected invariant failure as a 5xx
// would page an operator for a correct refusal and invite a retry that can
// only reproduce it. It must be a 200 carrying the typed failure and the
// verified prefix that survived.
func TestRejectedExecutionIsASuccessfulResponse(t *testing.T) {
	router := newTestRouter(t)
	plan := createPlan(t, router, "acme", fixtureDeclarations(t))

	recorder := postJSON(t, router, "/v1/executions", "acme",
		executionRequest(t, plan.PlanID, teamhos.AnchorMismatch))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var execution openapiv1.Execution
	decodeBody(t, recorder, &execution)

	if execution.SpineStatus != openapiv1.ExecutionSpineStatusFailed {
		t.Fatalf("spineStatus = %s, want failed", execution.SpineStatus)
	}
	if execution.Failure == nil {
		t.Fatal("a rejected execution carried no failure")
	}
	if execution.Failure.Kind != openapiv1.SemanticFailureKindProtectedInvariantFailed {
		t.Fatalf("failure kind = %s", execution.Failure.Kind)
	}
	if execution.Failure.Code == nil || *execution.Failure.Code != "HOS_ANCHOR_MISMATCH" {
		t.Fatalf("failure code = %v, want HOS_ANCHOR_MISMATCH", execution.Failure.Code)
	}

	// The verified prefix survives: C1 was sealed and assessed before T2 was
	// attempted, and none of that is discarded by the later refusal.
	if len(execution.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %d, want 1 (C1 retained)", len(execution.Checkpoints))
	}
	if len(execution.Assessments) != 2 {
		t.Fatalf("assessments = %d, want 2 (both profiles at C1)", len(execution.Assessments))
	}
	if execution.AcceptedRules == nil ||
		!slices.Equal(*execution.AcceptedRules, []string{"form_team.v1"}) {
		t.Fatalf("acceptedRules = %v, want only the committed transition", execution.AcceptedRules)
	}
}

// Production break caught: execution identity is derived, not allocated. If it
// were allocated, repeating a request would fork the artifact graph and the
// HLD's idempotency rule would need a deduplication table to hold.
func TestExecutionIdentityIsDerivedNotAllocated(t *testing.T) {
	router := newTestRouter(t)
	plan := createPlan(t, router, "acme", fixtureDeclarations(t))
	request := executionRequest(t, plan.PlanID, teamhos.Passing)

	first := mustExecute(t, router, "acme", request)
	second := mustExecute(t, router, "acme", request)

	if *first.SemanticRunID != *second.SemanticRunID {
		t.Fatalf("repeating a request changed the run identity: %s vs %s",
			*first.SemanticRunID, *second.SemanticRunID)
	}
	if *first.ExecutionID != *second.ExecutionID {
		t.Fatalf("repeating a request changed the execution identity: %s vs %s",
			*first.ExecutionID, *second.ExecutionID)
	}

	// Changing only the executor preserves what was computed and changes who
	// computed it.
	otherExecutor := request
	otherExecutor.ExecutorIdentity.Version =
		"sha256:3d1c8f2b6a5e4d7c9b0a1f2e3d4c5b6a7988776655443322110ffeeddccbbaa9"
	third := mustExecute(t, router, "acme", otherExecutor)

	if *third.SemanticRunID != *first.SemanticRunID {
		t.Fatalf("a different executor changed the semantic run: %s vs %s",
			*third.SemanticRunID, *first.SemanticRunID)
	}
	if *third.ExecutionID == *first.ExecutionID {
		t.Fatal("a different executor produced the same execution identity")
	}
	// The sealed artifacts are backend independent, so they must be unchanged.
	if third.Checkpoints[0].Digest != first.Checkpoints[0].Digest {
		t.Fatal("a different executor changed a sealed checkpoint")
	}
}

// Production break caught: executing a plan belonging to another tenant must
// be indistinguishable from executing one that does not exist.
func TestCreateExecutionHidesOtherTenantsPlans(t *testing.T) {
	router := newTestRouter(t)
	plan := createPlan(t, router, "acme", fixtureDeclarations(t))
	request := executionRequest(t, plan.PlanID, teamhos.Passing)

	foreign := postJSON(t, router, "/v1/executions", "globex", request)
	absent := postJSON(t, router, "/v1/executions", "acme", executionRequest(t,
		"sha256:0000000000000000000000000000000000000000000000000000000000000000", teamhos.Passing))

	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant status = %d, want 404", foreign.Code)
	}
	if foreign.Body.String() != absent.Body.String() {
		t.Fatalf("a foreign plan is distinguishable from an absent one:\n%s\n%s",
			foreign.Body.String(), absent.Body.String())
	}
}

// Production break caught: canonical input the kernel refuses must be a 422
// naming invalid semantic input, not an internal error. It is the caller's
// document that is wrong, and the distinction tells them whether to retry.
func TestCreateExecutionRejectsUnusableCanonicalInput(t *testing.T) {
	router := newTestRouter(t)
	plan := createPlan(t, router, "acme", fixtureDeclarations(t))

	tests := []struct {
		name   string
		mutate func(*openapiv1.CreateExecutionRequest)
	}{
		{"undeclared field", func(request *openapiv1.CreateExecutionRequest) {
			request.InitialState.Entities[0].Fields["not_in_schema"] = openapiv1.Value{
				Kind: openapiv1.ValueKindString, String: stringPtr("x"),
			}
		}},
		{"wrong value type", func(request *openapiv1.CreateExecutionRequest) {
			request.InitialState.Entities[0].Fields["hos_elapsed_hours"] = openapiv1.Value{
				Kind: openapiv1.ValueKindString, String: stringPtr("ten"),
			}
		}},
		{"malformed executor version", func(request *openapiv1.CreateExecutionRequest) {
			request.ExecutorIdentity.Version = "v1.2.3"
		}},
		{"empty lineage", func(request *openapiv1.CreateExecutionRequest) {
			request.InitialState.Lineage.RootKey = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := executionRequest(t, plan.PlanID, teamhos.Passing)
			test.mutate(&request)

			recorder := postJSON(t, router, "/v1/executions", "acme", request)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
			}
			var problem openapiv1.Problem
			decodeBody(t, recorder, &problem)
			if problem.Type != problemBaseURI+"invalid-semantic-input" {
				t.Fatalf("problem type = %s", problem.Type)
			}
		})
	}
}

func mustExecute(t *testing.T, router http.Handler, tenant string, request openapiv1.CreateExecutionRequest) openapiv1.Execution {
	t.Helper()
	recorder := postJSON(t, router, "/v1/executions", tenant, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("execute status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var execution openapiv1.Execution
	decodeBody(t, recorder, &execution)
	return execution
}

// executionRequest renders a fixture variant as the wire document a client
// would send: source keys and a lineage, never entity identities.
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
