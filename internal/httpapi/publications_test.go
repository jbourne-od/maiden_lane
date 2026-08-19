package httpapi

import (
	"net/http"
	"slices"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// THE CENTRAL CONTRACT DECISION: a gate refusal is a 200, not a problem document.
//
// An operator told only "refused" cannot act. The clause list says which requirement
// was not met and whether the answer was fail or not_evaluated, which is the
// difference between "implement something" and "supply something". This is the same
// rule a needs_input readiness verdict follows, and for the same reason: the
// computation produced a real answer.
func TestARefusedPublicationIsASuccessCarryingEveryClause(t *testing.T) {
	fixture := publicationFixture(t)

	recorder := postJSON(t, fixture.router, "/v1/publications", "acme", fixture.request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a refusal is an answer, not a problem: %s",
			recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var decision openapiv1.PublicationDecision
	decodeBody(t, recorder, &decision)

	if decision.Authorized {
		t.Fatal("publication was authorized while three clauses are unanswerable")
	}
	if decision.Outcome != openapiv1.PublicationOutcomeRefused {
		t.Fatalf("outcome = %s, want refused", decision.Outcome)
	}
	if decision.Publication != nil {
		t.Fatal("a refusal carried a publication")
	}
	// All nine, in the contract's stable order. A short list would mean a domain
	// clause has no wire name, which the translation deliberately omits rather than
	// inventing a vocabulary word for.
	if len(decision.Clauses) != 9 {
		t.Fatalf("clauses = %d, want the 9 in HLD §14.1", len(decision.Clauses))
	}

	// Six pass on this candidate. The three refusals are no longer all the same kind:
	// the comparison clause is implemented and simply has no evidence here, while the
	// other two name concepts this build does not have. Distinguishing them on the wire
	// is the whole reason the unevaluated reason is a separate field.
	passed, unsupported, absent := 0, 0, 0
	for _, clause := range decision.Clauses {
		switch clause.Verdict {
		case openapiv1.GateVerdictPass:
			passed++
		case openapiv1.GateVerdictNotEvaluated:
			switch clause.UnevaluatedReason {
			case openapiv1.GateUnevaluatedReasonUnsupportedByBuild:
				unsupported++
			case openapiv1.GateUnevaluatedReasonInformationAbsent:
				absent++
				if clause.Clause != openapiv1.GateClauseResultClauseComparisonCorpus {
					t.Fatalf("clause %s refused for want of evidence; only the comparison "+
						"clause should", clause.Clause)
				}
			default:
				t.Fatalf("clause %s refused as %s", clause.Clause, clause.UnevaluatedReason)
			}
		}
	}
	if passed != 6 || unsupported != 2 || absent != 1 {
		t.Fatalf("passed = %d, unsupported = %d, absent = %d, want 6, 2 and 1",
			passed, unsupported, absent)
	}
	if decision.PolicyVersion != 1 {
		t.Fatalf("policyVersion = %d, want the 1 it was judged under", decision.PolicyVersion)
	}

	// Nothing published. A refusal that advanced the pointer would be worse than no
	// gate at all.
	state := getPublicationState(t, fixture.router, "acme", "cust", "cm")
	if state.Status != openapiv1.PublicationStatusUnpublished {
		t.Fatalf("target status = %s, want unpublished", state.Status)
	}
}

// A target with no policy must refuse rather than 404. An unconfigured destination is
// the ordinary initial state, and reporting it as absent would make a configuration
// gap look like a missing artifact.
func TestAnUnconfiguredTargetRefusesRatherThanReportingAbsence(t *testing.T) {
	fixture := publicationFixtureWithout(t, func(store *memory.Store) {})

	recorder := postJSON(t, fixture.router, "/v1/publications", "acme", fixture.request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var decision openapiv1.PublicationDecision
	decodeBody(t, recorder, &decision)
	if decision.Authorized {
		t.Fatal("publication was authorized with no policy")
	}
	if decision.PolicyVersion != 0 {
		t.Fatalf("policyVersion = %d, want 0 for a target with no policy", decision.PolicyVersion)
	}
	// Every clause unevaluated for want of a rule, not for anything about the
	// candidate.
	for _, clause := range decision.Clauses {
		if clause.UnevaluatedReason != openapiv1.GateUnevaluatedReasonInformationAbsent {
			t.Fatalf("clause %s reason = %s, want information_absent: the missing "+
				"information is the policy", clause.Clause, clause.UnevaluatedReason)
		}
	}
}

// A client names identities and cannot steer which evidence is used. Naming a
// checkpoint the execution did not produce, or an assessment not taken against it, is
// absence: there is nothing to publish.
func TestAClientCannotNameEvidenceTheExecutionDidNotProduce(t *testing.T) {
	fixture := publicationFixture(t)

	for _, test := range []struct {
		name   string
		mutate func(*openapiv1.CreatePublicationRequest)
	}{
		{"a checkpoint from no execution", func(r *openapiv1.CreatePublicationRequest) {
			r.CheckpointArtifactID = openapiv1.Digest(digestLiteral("elsewhere"))
		}},
		{"an assessment from no checkpoint", func(r *openapiv1.CreatePublicationRequest) {
			r.AssessmentID = openapiv1.Digest(digestLiteral("elsewhere"))
		}},
		{"an assessment of a different checkpoint", func(r *openapiv1.CreatePublicationRequest) {
			r.AssessmentID = openapiv1.Digest(fixture.otherAssessment)
		}},
		{"an execution nobody ran", func(r *openapiv1.CreatePublicationRequest) {
			r.ExecutionID = openapiv1.Digest(digestLiteral("elsewhere"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.mutate(&request)
			recorder := postJSON(t, fixture.router, "/v1/publications", "acme", request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
			}
			var problem openapiv1.Problem
			decodeBody(t, recorder, &problem)
			if problem.Type != problemBaseURI+"not-found" {
				t.Fatalf("problem type = %s", problem.Type)
			}
		})
	}
}

// Another tenant's execution is absent, never forbidden. Possession of an identifier
// must reveal nothing about its existence.
func TestPublicationIsTenantScoped(t *testing.T) {
	fixture := publicationFixture(t)

	recorder := postJSON(t, fixture.router, "/v1/publications", "globex", fixture.request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

// A malformed document is refused before any store is read.
func TestAnIncompletePublicationRequestIsRefused(t *testing.T) {
	fixture := publicationFixture(t)

	for _, test := range []struct {
		name   string
		mutate func(*openapiv1.CreatePublicationRequest)
	}{
		{"no customer", func(r *openapiv1.CreatePublicationRequest) { r.CustomerID = "" }},
		{"no target", func(r *openapiv1.CreatePublicationRequest) { r.Target = "" }},
		{"no execution", func(r *openapiv1.CreatePublicationRequest) { r.ExecutionID = "" }},
		{"no checkpoint", func(r *openapiv1.CreatePublicationRequest) { r.CheckpointArtifactID = "" }},
		{"no assessment", func(r *openapiv1.CreatePublicationRequest) { r.AssessmentID = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.mutate(&request)
			recorder := postJSON(t, fixture.router, "/v1/publications", "acme", request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// A target that has never been published to is a 200 reporting unpublished, not a 404.
// It is the ordinary initial state of every target, and an absence would be
// indistinguishable from one the caller has no right to see.
func TestAnUnpublishedTargetIsReportedRatherThanAbsent(t *testing.T) {
	fixture := publicationFixture(t)

	state := getPublicationState(t, fixture.router, "acme", "cust", "cm")
	if state.Status != openapiv1.PublicationStatusUnpublished {
		t.Fatalf("status = %s, want unpublished", state.Status)
	}
	if state.Publication != nil {
		t.Fatal("an unpublished target carried a publication")
	}
	if state.CustomerID != "cust" || state.Target != "cm" {
		t.Fatalf("the key was not echoed: %+v", state)
	}
}

// A publication read must project every pinned identity and derive its status from the
// history rather than storing one. A superseded version stays readable, which is what
// keeps an old decision explainable.
func TestAPublishedTargetProjectsEveryPinnedIdentity(t *testing.T) {
	fixture := publicationFixture(t)

	// The gate authorizes nothing in this build, so the pointer is advanced through
	// the store to exercise the read path. Reaching authorization through the API is
	// impossible and faking a decision is too: the gate is not injectable.
	for version := ports.PublicationVersion(1); version <= 2; version++ {
		if err := fixture.store.Publish(t.Context(), ports.Publication{
			TenantID: "acme", CustomerID: "cust", Target: "cm", Version: version,
			PolicyVersion: 1, ProfileID: fixture.profile, AssessmentID: fixture.assessment,
			CheckpointArtifactID: fixture.checkpoint, SemanticRunID: fixture.run,
			ExecutionID: fixture.execution,
		}); err != nil {
			t.Fatalf("Publish v%d: %v", version, err)
		}
	}

	current := getPublicationState(t, fixture.router, "acme", "cust", "cm")
	if current.Status != openapiv1.PublicationStatusPublished {
		t.Fatalf("status = %s, want published", current.Status)
	}
	if current.Publication == nil {
		t.Fatal("a published target carried no publication")
	}
	if current.Publication.Version != 2 {
		t.Fatalf("version = %d, want the newest", current.Publication.Version)
	}
	for name, pair := range map[string][2]string{
		"profileID":            {string(current.Publication.ProfileID), string(fixture.profile)},
		"assessmentID":         {string(current.Publication.AssessmentID), string(fixture.assessment)},
		"checkpointArtifactID": {string(current.Publication.CheckpointArtifactID), string(fixture.checkpoint)},
		"semanticRunID":        {string(current.Publication.SemanticRunID), string(fixture.run)},
		"executionID":          {string(current.Publication.ExecutionID), string(fixture.execution)},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %s, want %s", name, pair[0], pair[1])
		}
	}

	// Version 1 is superseded, and the status is derived from the history rather than
	// read from a column that could disagree with it.
	older := getPublicationVersion(t, fixture.router, "acme", "cust", "cm", 1)
	if older.Status != openapiv1.PublicationStatusSuperseded {
		t.Fatalf("v1 status = %s, want superseded", older.Status)
	}

	// A version that does not exist IS an absence, unlike a target never published to.
	recorder := get(t, fixture.router, "/v1/publications/cust/cm?version=9", "acme")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status for a missing version = %d, want 404", recorder.Code)
	}
}

// Configured without a control plane, the publication routes report the feature as
// unavailable rather than panicking or claiming the gate was evaluated.
func TestPublicationRoutesWithoutAControlPlaneAreUnavailable(t *testing.T) {
	store := memory.NewStore()
	router := NewRouter(Dependencies{Plans: store, Executions: store})

	recorder := postJSON(t, router, "/v1/publications", "acme",
		openapiv1.CreatePublicationRequest{CustomerID: "cust", Target: "cm"})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", recorder.Code, recorder.Body.String())
	}
	if recorder := get(t, router, "/v1/publications/cust/cm", "acme"); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("read status = %d, want 503", recorder.Code)
	}
}

// Production break caught by construction: every domain clause must have a wire name.
// One without would be silently omitted, and the contract requires exactly nine, so a
// missing mapping would ship a decision that fails its own schema.
func TestEveryClauseHasAWireName(t *testing.T) {
	fixture := publicationFixture(t)
	recorder := postJSON(t, fixture.router, "/v1/publications", "acme", fixture.request)
	var decision openapiv1.PublicationDecision
	decodeBody(t, recorder, &decision)

	seen := make([]string, 0, len(decision.Clauses))
	for _, clause := range decision.Clauses {
		seen = append(seen, string(clause.Clause))
	}
	slices.Sort(seen)
	want := []string{
		"certified_backend", "comparison_corpus", "digest_consistency",
		"no_metric_regression", "pinned_identities", "protected_invariants",
		"ready_assessment", "sealed_with_provenance", "static_validation",
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("clauses = %v, want %v", seen, want)
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

type publicationSetup struct {
	router          http.Handler
	store           *memory.Store
	request         openapiv1.CreatePublicationRequest
	profile         semantic.ProfileID
	assessment      semantic.AssessmentID
	otherAssessment semantic.AssessmentID
	checkpoint      semantic.CheckpointArtifactID
	run             semantic.SemanticRunID
	execution       semantic.ExecutionID
}

func publicationFixture(t *testing.T) publicationSetup {
	t.Helper()
	return publicationFixtureWithout(t, nil)
}

// publicationFixtureWithout stores a plan and a completed execution the way production
// does, then optionally skips recording the target policy.
//
// It runs the real spine and projects through app.Project, because the handler
// rehydrates: a fixture whose stored result was not what production writes would be
// refused as divergent, which is the storage-fidelity check doing its job.
func publicationFixtureWithout(t *testing.T, skipPolicy func(*memory.Store)) publicationSetup {
	t.Helper()

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
		t.Fatal("the fixture did not compile")
	}
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: inputs.InitialState, World: inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}

	store := memory.NewStore()
	if err := store.PutPlan(t.Context(), ports.PlanRecord{
		TenantID: "acme", PlanID: plan.ID(), Input: compilation.Input(),
		Schema: inputs.InitialState.Schema(), Compilation: compilation,
	}); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	request := ports.ExecutionRequest{
		TenantID: "acme", ExecutionID: binding.ExecutionID(),
		RunID: binding.SemanticRunID(), PlanID: plan.ID(),
		Input: ports.ExecutionInput{
			InitialState: inputs.InitialState, World: inputs.World,
			ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
		},
	}
	if _, err := store.Enqueue(t.Context(), request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	result, err := app.Run(t.Context(), app.Request{
		Compilation: inputs.Compilation, InitialState: inputs.InitialState,
		World: inputs.World, ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}, nil)
	if err != nil {
		t.Fatalf("app.Run: %v", err)
	}
	projected, err := app.Project(request, result)
	if err != nil {
		t.Fatalf("app.Project: %v", err)
	}
	if err := store.Complete(t.Context(), projected); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// The last sealed checkpoint, with the assessment that found it ready.
	checkpoints := result.Checkpoints()
	artifact := checkpoints[len(checkpoints)-1]
	var ready, other semantic.Assessment
	for _, assessment := range result.Assessments() {
		switch {
		case assessment.CheckpointArtifactID() == artifact.ID() &&
			assessment.Verdict() == semantic.Ready && ready.ID() == "":
			ready = assessment
		case assessment.CheckpointArtifactID() != artifact.ID() && other.ID() == "":
			other = assessment
		}
	}
	if ready.ID() == "" || other.ID() == "" {
		t.Fatal("the fixture must supply a ready assessment for its checkpoint and one for another")
	}

	if skipPolicy == nil {
		if err := store.PutPolicy(t.Context(), ports.TargetPolicy{
			TenantID: "acme", CustomerID: "cust", Target: "cm",
			Version: 1, RequiredProfileID: ready.ProfileID(),
		}); err != nil {
			t.Fatalf("PutPolicy: %v", err)
		}
	}

	return publicationSetup{
		router: NewRouter(Dependencies{
			Plans: store, Executions: store, Policies: store, Publications: store,
		}),
		store:           store,
		profile:         ready.ProfileID(),
		assessment:      ready.ID(),
		otherAssessment: other.ID(),
		checkpoint:      artifact.ID(),
		run:             binding.SemanticRunID(),
		execution:       binding.ExecutionID(),
		request: openapiv1.CreatePublicationRequest{
			CustomerID:             "cust",
			Target:                 "cm",
			ExecutionID:            openapiv1.Digest(binding.ExecutionID()),
			CheckpointArtifactID:   openapiv1.Digest(artifact.ID()),
			AssessmentID:           openapiv1.Digest(ready.ID()),
			ExpectedCurrentVersion: 0,
		},
	}
}

func getPublicationState(
	t *testing.T, router http.Handler, tenant, customer, target string,
) openapiv1.PublicationState {
	t.Helper()
	recorder := get(t, router, "/v1/publications/"+customer+"/"+target, tenant)
	if recorder.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var state openapiv1.PublicationState
	decodeBody(t, recorder, &state)
	return state
}

func getPublicationVersion(
	t *testing.T, router http.Handler, tenant, customer, target string, version int,
) openapiv1.PublicationState {
	t.Helper()
	recorder := get(t, router,
		"/v1/publications/"+customer+"/"+target+"?version="+itoa(version), tenant)
	if recorder.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var state openapiv1.PublicationState
	decodeBody(t, recorder, &state)
	return state
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func digestLiteral(label string) string {
	sum := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		sum = append(sum, "0123456789abcdef"[(int(label[i%len(label)])+i)%16])
	}
	return "sha256:" + string(sum)
}
