package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// The example payloads under examples/teamhos are the demo's material and the
// first thing anyone reads to learn what a request looks like. They are checked
// here by identity rather than by shape.
//
// A shape check -- does it parse, does it return 201 -- would pass for a payload
// that had silently drifted into declaring a different program. Identity is the
// assertion that cannot be satisfied by an approximation: plan identity is derived
// from the canonical declarations, so a committed payload that yields the ratified
// fixture's PlanID IS the ratified plan, down to the byte. Anything else fails
// here rather than in front of an audience.
const examplesDirectory = "../../examples/teamhos"

func TestTheExamplePlanIsTheRatifiedFixturePlan(t *testing.T) {
	router := newTestRouter(t)

	plan := createPlanFromFile(t, router, "plan.json")
	if plan.PlanID != openapiv1.Digest(fixturePlanID(t)) {
		t.Fatalf("examples/teamhos/plan.json compiles to %s, but the ratified fixture "+
			"compiles to %s: the example has drifted into declaring a different program",
			plan.PlanID, fixturePlanID(t))
	}

	// The demo narrates two checkpoints and two profiles. If the example stopped
	// declaring either, the script would describe something the payload does not do.
	if len(plan.Checkpoints) != 2 {
		t.Fatalf("checkpoints = %d, want 2", len(plan.Checkpoints))
	}
	if len(plan.Profiles) != 2 {
		t.Fatalf("compiled profiles = %d, want 2", len(plan.Profiles))
	}
}

// Each example execution must be accepted under the identity the kernel derives
// for the corresponding fixture variant. This is what makes the demo's claims
// checkable: the payload the audience reads is the input the ratified fixture
// describes, not a lookalike.
func TestTheExampleExecutionsCarryTheFixtureIdentities(t *testing.T) {
	for _, test := range []struct {
		file    string
		variant teamhos.Variant
	}{
		{"execution.json", teamhos.Passing},
		{"execution-anchor-mismatch.json", teamhos.AnchorMismatch},
	} {
		t.Run(test.file, func(t *testing.T) {
			router := newTestRouter(t)
			createPlanFromFile(t, router, "plan.json")

			accepted := createExecutionFromFile(t, router, test.file)
			expectedRun, expectedExecution := fixtureIdentities(t, test.variant)
			if accepted.SemanticRunID != openapiv1.Digest(expectedRun) {
				t.Fatalf("semanticRunID = %s, want the fixture's %s",
					accepted.SemanticRunID, expectedRun)
			}
			if accepted.ExecutionID != openapiv1.Digest(expectedExecution) {
				t.Fatalf("executionID = %s, want the fixture's %s",
					accepted.ExecutionID, expectedExecution)
			}
			if accepted.PlanID != openapiv1.Digest(fixturePlanID(t)) {
				t.Fatalf("planID = %s, want %s", accepted.PlanID, fixturePlanID(t))
			}
		})
	}
}

// The demo's central claim, asserted rather than narrated: the two example
// executions differ in exactly one field, and that one field changes the semantic
// run while leaving the program identical.
//
// If the payloads ever diverge in more than the anchor, the demo would be showing
// an effect with more than one possible cause, which is the difference between a
// demonstration and an anecdote.
func TestTheTwoExampleExecutionsDifferInExactlyOneObservation(t *testing.T) {
	passing := readExampleObject(t, "execution.json")
	mismatch := readExampleObject(t, "execution-anchor-mismatch.json")

	differences := jsonDifferences(t, "", passing, mismatch)
	if len(differences) != 1 {
		t.Fatalf("the examples differ in %d places, want exactly 1: %v", len(differences), differences)
	}
	if got := differences[0]; got != "initialState.entities[1].fields.hos_anchor.atom" {
		t.Fatalf("the difference is at %q, want driver B's hos_anchor", got)
	}

	// Same program, different run. The plan is upstream of the observation, so it
	// must not move; the run is derived from the observation, so it must.
	router := newTestRouter(t)
	createPlanFromFile(t, router, "plan.json")
	first := createExecutionFromFile(t, router, "execution.json")
	second := createExecutionFromFile(t, router, "execution-anchor-mismatch.json")

	if first.PlanID != second.PlanID {
		t.Fatalf("one observation changed the plan: %s then %s", first.PlanID, second.PlanID)
	}
	if first.SemanticRunID == second.SemanticRunID {
		t.Fatal("two different observations produced one semantic run")
	}
}

// The example execution must name the plan the example plan compiles to. A stale
// planID here is the failure that would look like a broken server rather than a
// stale file, because the request is well formed and the plan genuinely is absent.
func TestTheExampleExecutionsNameTheExamplePlan(t *testing.T) {
	for _, file := range []string{"execution.json", "execution-anchor-mismatch.json"} {
		var request struct {
			PlanID string `json:"planID"`
		}
		decodeExample(t, file, &request)
		if request.PlanID != string(fixturePlanID(t)) {
			t.Fatalf("%s names plan %s, but the example plan compiles to %s",
				file, request.PlanID, fixturePlanID(t))
		}
	}
}

func fixturePlanID(t *testing.T) semantic.PlanID {
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
		t.Fatal("fixture did not compile")
	}
	return plan.ID()
}

func fixtureIdentities(t *testing.T, variant teamhos.Variant) (semantic.SemanticRunID, semantic.ExecutionID) {
	t.Helper()
	inputs, err := teamhos.New(variant)
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
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	return binding.SemanticRunID(), binding.ExecutionID()
}

func createPlanFromFile(t *testing.T, router http.Handler, file string) openapiv1.Plan {
	t.Helper()
	recorder := postExample(t, router, "/v1/plans", file)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create plan from %s: status %d: %s", file, recorder.Code, recorder.Body.String())
	}
	var plan openapiv1.Plan
	decodeBody(t, recorder, &plan)
	return plan
}

func createExecutionFromFile(t *testing.T, router http.Handler, file string) openapiv1.ExecutionAccepted {
	t.Helper()
	recorder := postExample(t, router, "/v1/executions", file)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("create execution from %s: status %d: %s", file, recorder.Code, recorder.Body.String())
	}
	var accepted openapiv1.ExecutionAccepted
	decodeBody(t, recorder, &accepted)
	return accepted
}

// postExample sends the committed file's exact bytes rather than a re-marshalled
// value, so the thing under test is the file the demo actually posts.
func postExample(t *testing.T, router http.Handler, path, file string) *httptest.ResponseRecorder {
	t.Helper()
	body := readExample(t, file)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(tenantHeader, "acme")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func readExample(t *testing.T, file string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(examplesDirectory, file))
	if err != nil {
		t.Fatalf("read example %s: %v", file, err)
	}
	return body
}

func decodeExample(t *testing.T, file string, target any) {
	t.Helper()
	if err := json.Unmarshal(readExample(t, file), target); err != nil {
		t.Fatalf("decode example %s: %v", file, err)
	}
}

func readExampleObject(t *testing.T, file string) map[string]any {
	t.Helper()
	var object map[string]any
	decodeExample(t, file, &object)
	return object
}

// jsonDifferences reports every leaf path at which two decoded JSON values
// disagree, so "they differ in one field" is a counted fact rather than an
// impression from reading a diff.
func jsonDifferences(t *testing.T, path string, left, right any) []string {
	t.Helper()
	switch typedLeft := left.(type) {
	case map[string]any:
		typedRight, ok := right.(map[string]any)
		if !ok {
			return []string{path}
		}
		keys := map[string]bool{}
		for key := range typedLeft {
			keys[key] = true
		}
		for key := range typedRight {
			keys[key] = true
		}
		differences := make([]string, 0)
		for key := range keys {
			leftValue, inLeft := typedLeft[key]
			rightValue, inRight := typedRight[key]
			if !inLeft || !inRight {
				differences = append(differences, join(path, key))
				continue
			}
			differences = append(differences, jsonDifferences(t, join(path, key), leftValue, rightValue)...)
		}
		return differences
	case []any:
		typedRight, ok := right.([]any)
		if !ok || len(typedLeft) != len(typedRight) {
			return []string{path}
		}
		differences := make([]string, 0)
		for i := range typedLeft {
			differences = append(differences,
				jsonDifferences(t, indexed(path, i), typedLeft[i], typedRight[i])...)
		}
		return differences
	default:
		if left != right {
			return []string{path}
		}
		return nil
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func indexed(path string, index int) string {
	return path + "[" + strconv.Itoa(index) + "]"
}
