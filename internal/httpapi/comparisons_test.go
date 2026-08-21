package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

type comparisonTestFixture struct {
	router    http.Handler
	store     *memory.Store
	baseline  semantic.Plan
	candidate semantic.Plan
	request   openapiv1.CreateComparisonRequest
}

func newComparisonTestFixture(t *testing.T) comparisonTestFixture {
	t.Helper()
	store := memory.NewStore()
	baseline, baselineRecord := comparisonPlan(t, "acme", teamhos.CheckpointTeamHOSAggregated)
	candidate, candidateRecord := comparisonPlan(t, "acme", teamhos.CheckpointTeamHOSRevised)

	if err := store.PutPlan(context.Background(), baselineRecord); err != nil {
		t.Fatalf("store.PutPlan baseline: %v", err)
	}
	if err := store.PutPlan(context.Background(), candidateRecord); err != nil {
		t.Fatalf("store.PutPlan candidate: %v", err)
	}

	router := NewRouter(Dependencies{
		Plans:       store,
		Executions:  store,
		Comparisons: store,
	})

	request := openapiv1.CreateComparisonRequest{
		BaselinePlanID:      string(baseline.ID()),
		CandidatePlanID:     string(candidate.ID()),
		BaselineCheckpoint:  string(teamhos.CheckpointTeamHOSAggregated),
		CandidateCheckpoint: string(teamhos.CheckpointTeamHOSRevised),
		ProfileID:           "sha256:0000000000000000000000000000000000000000000000000000000000000001",
		WorldID:             "sha256:0000000000000000000000000000000000000000000000000000000000000002",
		CorpusID:            "sha256:0000000000000000000000000000000000000000000000000000000000000003",
		Correspondences: []openapiv1.CheckpointPair{
			{
				Baseline:  string(teamhos.CheckpointTeamFormed),
				Candidate: string(teamhos.CheckpointTeamFormed),
			},
			{
				Baseline:  string(teamhos.CheckpointTeamHOSAggregated),
				Candidate: string(teamhos.CheckpointTeamHOSRevised),
			},
		},
	}

	return comparisonTestFixture{
		router:    router,
		store:     store,
		baseline:  baseline,
		candidate: candidate,
		request:   request,
	}
}

func comparisonPlan(t *testing.T, tenant ports.TenantID, finalKey semantic.CheckpointKey) (semantic.Plan, ports.PlanRecord) {
	t.Helper()
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	request := inputs.Compilation
	checkpoints := make([]semantic.CheckpointDeclaration, len(request.Rules.Checkpoints))
	copy(checkpoints, request.Rules.Checkpoints)
	renamed := false
	for i := range checkpoints {
		if checkpoints[i].Key == teamhos.CheckpointTeamHOSAggregated {
			checkpoints[i].Key = finalKey
			renamed = true
		}
	}
	if !renamed {
		t.Fatal("the ratified fixture no longer declares the checkpoint this fixture renames")
	}
	request.Rules.Checkpoints = checkpoints

	compilation, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("the fixture did not compile: %v", failure.Diagnostics())
	}
	return plan, ports.PlanRecord{
		TenantID:    tenant,
		PlanID:      plan.ID(),
		Input:       compilation.Input(),
		Schema:      inputs.InitialState.Schema(),
		Compilation: compilation,
	}
}

// Production break caught: creating a comparison must persist the derived contract
// and return 201 Created with the canonical question projection.
func TestCreateComparisonHappyPathAndRetrieval(t *testing.T) {
	f := newComparisonTestFixture(t)

	recorder := postJSON(t, f.router, "/v1/comparisons", "acme", f.request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var created openapiv1.Comparison
	decodeBody(t, recorder, &created)

	if created.ComparisonID == "" {
		t.Fatal("comparison has no comparisonID")
	}
	if created.PolicyID == "" {
		t.Fatal("comparison has no policyID")
	}
	if created.BaselinePlanID != f.request.BaselinePlanID {
		t.Fatalf("baselinePlanID = %s, want %s", created.BaselinePlanID, f.request.BaselinePlanID)
	}
	if created.CandidatePlanID != f.request.CandidatePlanID {
		t.Fatalf("candidatePlanID = %s, want %s", created.CandidatePlanID, f.request.CandidatePlanID)
	}
	if len(created.Correspondences) != 2 {
		t.Fatalf("correspondences = %d, want 2", len(created.Correspondences))
	}

	// Retrieve comparison
	getRecorder := get(t, f.router, "/v1/comparisons/"+created.ComparisonID, "acme")
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200: %s", getRecorder.Code, getRecorder.Body.String())
	}

	var retrieved openapiv1.Comparison
	decodeBody(t, getRecorder, &retrieved)

	if retrieved.ComparisonID != created.ComparisonID {
		t.Fatalf("retrieved comparisonID = %s, want %s", retrieved.ComparisonID, created.ComparisonID)
	}
	if retrieved.PolicyID != created.PolicyID {
		t.Fatalf("retrieved policyID = %s, want %s", retrieved.PolicyID, created.PolicyID)
	}
	if retrieved.BaselineCheckpointID != created.BaselineCheckpointID {
		t.Fatalf("baselineCheckpointID = %s, want %s", retrieved.BaselineCheckpointID, created.BaselineCheckpointID)
	}
	if retrieved.CandidateCheckpointID != created.CandidateCheckpointID {
		t.Fatalf("candidateCheckpointID = %s, want %s", retrieved.CandidateCheckpointID, created.CandidateCheckpointID)
	}
	if retrieved.ProfileID != f.request.ProfileID {
		t.Fatalf("profileID = %s, want %s", retrieved.ProfileID, f.request.ProfileID)
	}
	if retrieved.WorldID != f.request.WorldID {
		t.Fatalf("worldID = %s, want %s", retrieved.WorldID, f.request.WorldID)
	}
	if retrieved.CorpusID != f.request.CorpusID {
		t.Fatalf("corpusID = %s, want %s", retrieved.CorpusID, f.request.CorpusID)
	}
}

// Production break caught: comparison identity is content-derived, so resubmitting
// identical inputs must return the same identity without failing.
func TestCreateComparisonIsIdempotent(t *testing.T) {
	f := newComparisonTestFixture(t)

	first := postJSON(t, f.router, "/v1/comparisons", "acme", f.request)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201: %s", first.Code, first.Body.String())
	}
	var firstResp openapiv1.Comparison
	decodeBody(t, first, &firstResp)

	second := postJSON(t, f.router, "/v1/comparisons", "acme", f.request)
	if second.Code != http.StatusCreated {
		t.Fatalf("second status = %d, want 201: %s", second.Code, second.Body.String())
	}
	var secondResp openapiv1.Comparison
	decodeBody(t, second, &secondResp)

	if firstResp.ComparisonID != secondResp.ComparisonID {
		t.Fatalf("first comparisonID = %s, second = %s", firstResp.ComparisonID, secondResp.ComparisonID)
	}
	if firstResp.PolicyID != secondResp.PolicyID {
		t.Fatalf("first policyID = %s, second = %s", firstResp.PolicyID, secondResp.PolicyID)
	}
}

// Production break caught: referencing an absent plan must return 404 rather than
// failing closed or panicking.
func TestCreateComparisonReports404ForAbsentPlans(t *testing.T) {
	f := newComparisonTestFixture(t)

	t.Run("absent baseline plan", func(t *testing.T) {
		req := f.request
		req.BaselinePlanID = "sha256:0000000000000000000000000000000000000000000000000000000000000099"
		recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("absent candidate plan", func(t *testing.T) {
		req := f.request
		req.CandidatePlanID = "sha256:0000000000000000000000000000000000000000000000000000000000000099"
		recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
	})
}

// Production break caught: naming a checkpoint key that neither plan declares must
// produce 422 Unprocessable Entity with a problem document.
func TestCreateComparisonReports422ForUndeclaredCheckpoints(t *testing.T) {
	f := newComparisonTestFixture(t)

	t.Run("undeclared baseline checkpoint", func(t *testing.T) {
		req := f.request
		req.BaselineCheckpoint = "nonexistent.v1"
		recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("undeclared candidate checkpoint", func(t *testing.T) {
		req := f.request
		req.CandidateCheckpoint = "nonexistent.v1"
		recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("undeclared key in correspondences", func(t *testing.T) {
		req := f.request
		req.Correspondences = append(req.Correspondences, openapiv1.CheckpointPair{
			Baseline:  "nonexistent.v1",
			Candidate: string(teamhos.CheckpointTeamHOSRevised),
		})
		recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
	})
}

// Production break caught: a comparison whose compared pair is not declared in the
// correspondence policy must fail closed at construction with 422.
func TestCreateComparisonReports422WhenPolicyDoesNotCorrespondComparedPair(t *testing.T) {
	f := newComparisonTestFixture(t)

	req := f.request
	// Only correspond team_formed, leaving team_hos unmapped
	req.Correspondences = []openapiv1.CheckpointPair{
		{
			Baseline:  string(teamhos.CheckpointTeamFormed),
			Candidate: string(teamhos.CheckpointTeamFormed),
		},
	}
	recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
}

// Production break caught: correspondence mapping must be one-to-one in both directions.
func TestCreateComparisonReports422ForAmbiguousCorrespondence(t *testing.T) {
	f := newComparisonTestFixture(t)

	req := f.request
	// Map same baseline to two candidate keys
	req.Correspondences = []openapiv1.CheckpointPair{
		{
			Baseline:  string(teamhos.CheckpointTeamFormed),
			Candidate: string(teamhos.CheckpointTeamFormed),
		},
		{
			Baseline:  string(teamhos.CheckpointTeamFormed),
			Candidate: string(teamhos.CheckpointTeamHOSRevised),
		},
	}
	recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
}

// Production break caught: missing required fields must fail with 400 Bad Request.
func TestCreateComparisonRequiresAllFields(t *testing.T) {
	f := newComparisonTestFixture(t)

	cases := []struct {
		name string
		edit func(*openapiv1.CreateComparisonRequest)
	}{
		{"missing baseline plan", func(r *openapiv1.CreateComparisonRequest) { r.BaselinePlanID = "" }},
		{"missing candidate plan", func(r *openapiv1.CreateComparisonRequest) { r.CandidatePlanID = "" }},
		{"missing baseline checkpoint", func(r *openapiv1.CreateComparisonRequest) { r.BaselineCheckpoint = "" }},
		{"missing candidate checkpoint", func(r *openapiv1.CreateComparisonRequest) { r.CandidateCheckpoint = "" }},
		{"missing profile", func(r *openapiv1.CreateComparisonRequest) { r.ProfileID = "" }},
		{"missing world", func(r *openapiv1.CreateComparisonRequest) { r.WorldID = "" }},
		{"missing corpus", func(r *openapiv1.CreateComparisonRequest) { r.CorpusID = "" }},
		{"empty correspondences", func(r *openapiv1.CreateComparisonRequest) { r.Correspondences = nil }},
		{"empty pair key", func(r *openapiv1.CreateComparisonRequest) {
			r.Correspondences = []openapiv1.CheckpointPair{{Baseline: "", Candidate: "foo"}}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := f.request
			tc.edit(&req)
			recorder := postJSON(t, f.router, "/v1/comparisons", "acme", req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// Production break caught: GET on an absent comparison must return 404.
func TestGetComparisonReports404ForAbsentComparison(t *testing.T) {
	f := newComparisonTestFixture(t)

	recorder := get(t, f.router, "/v1/comparisons/sha256:0000000000000000000000000000000000000000000000000000000000000099", "acme")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

// Production break caught: tenant scoping must isolate comparisons completely.
func TestComparisonTenantIsolation(t *testing.T) {
	f := newComparisonTestFixture(t)

	recorder := postJSON(t, f.router, "/v1/comparisons", "acme", f.request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	var created openapiv1.Comparison
	decodeBody(t, recorder, &created)

	// Attempt access from another tenant
	otherRecorder := get(t, f.router, "/v1/comparisons/"+created.ComparisonID, "other-tenant")
	if otherRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d, want 404: %s", otherRecorder.Code, otherRecorder.Body.String())
	}
}

// Production break caught: unconfigured store dependencies must report 503 Service Unavailable.
func TestComparisonDependencyUnavailable(t *testing.T) {
	f := newComparisonTestFixture(t)

	t.Run("nil comparisons store", func(t *testing.T) {
		router := NewRouter(Dependencies{
			Plans: f.store,
		})
		recorder := postJSON(t, router, "/v1/comparisons", "acme", f.request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("post status = %d, want 503: %s", recorder.Code, recorder.Body.String())
		}
		getRecorder := get(t, router, "/v1/comparisons/sha256:0000000000000000000000000000000000000000000000000000000000000001", "acme")
		if getRecorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("get status = %d, want 503: %s", getRecorder.Code, getRecorder.Body.String())
		}
	})

	t.Run("nil plans store", func(t *testing.T) {
		router := NewRouter(Dependencies{
			Comparisons: f.store,
		})
		recorder := postJSON(t, router, "/v1/comparisons", "acme", f.request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("post status = %d, want 503: %s", recorder.Code, recorder.Body.String())
		}
	})
}
