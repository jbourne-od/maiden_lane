package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Production break caught: plan identity is content derived, so compiling the
// same declarations twice must produce the same planID. If creation allocated
// an identity instead, two identical submissions would fork into two plans and
// replay would have nothing stable to point at.
func TestCreatePlanIsContentAddressed(t *testing.T) {
	router := newTestRouter(t)
	declarations := fixtureDeclarations(t)

	first := createPlan(t, router, "acme", declarations)
	second := createPlan(t, router, "acme", declarations)

	if first.PlanID != second.PlanID {
		t.Fatalf("identical declarations produced %s and %s", first.PlanID, second.PlanID)
	}
	if first.PlanID == "" {
		t.Fatal("created plan has no identity")
	}
	if len(first.Profiles) != 2 {
		t.Fatalf("compiled profiles = %d, want 2", len(first.Profiles))
	}
	if !slices.Equal(first.Rules, []string{"form_team.v1", "aggregate_team_hos.v1"}) {
		t.Fatalf("rules = %v", first.Rules)
	}
	// Creation returns identities only; the debug projection belongs on read.
	if first.Declarations != nil {
		t.Fatal("creation response carried declarations")
	}
}

// Production break caught: an invalid plan must carry its diagnostics and no
// identity. Returning a planID for a program that did not compile would name
// an artifact that does not exist.
func TestCreatePlanRejectsInvalidDeclarations(t *testing.T) {
	router := newTestRouter(t)
	declarations := fixtureDeclarations(t)
	for i := range declarations.Rules.Transformations {
		if declarations.Rules.Transformations[i].Id == "form_team.v1" {
			reads := append(*declarations.Rules.Transformations[i].DeclaredReads, "driver.field_that_does_not_exist")
			declarations.Rules.Transformations[i].DeclaredReads = &reads
		}
	}

	recorder := postJSON(t, router, "/v1/plans", "acme", declarations)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}

	var problem openapiv1.Problem
	decodeBody(t, recorder, &problem)
	if problem.Diagnostics == nil {
		t.Fatal("invalid plan carried no diagnostics")
	}
	found := false
	for _, diagnostic := range *problem.Diagnostics {
		if diagnostic.Code == openapiv1.CompilerDiagnosticCodeUNKNOWNFIELD {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want UNKNOWN_FIELD", *problem.Diagnostics)
	}
	// The rejected field name is the caller's own declaration text and must not
	// be republished.
	if bytes.Contains(recorder.Body.Bytes(), []byte("field_that_does_not_exist")) {
		t.Fatalf("problem echoed the caller's declaration: %s", recorder.Body.String())
	}
}

// Production break caught: retrieval is the only way to resolve a PlanID
// observed in a trace back to what the plan declares, because telemetry cannot
// carry declarations. It must return them, and they must be the compiled ones.
func TestGetPlanReturnsCompiledDeclarations(t *testing.T) {
	router := newTestRouter(t)
	created := createPlan(t, router, "acme", fixtureDeclarations(t))

	recorder := get(t, router, "/v1/plans/"+string(created.PlanID), "acme")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var plan openapiv1.Plan
	decodeBody(t, recorder, &plan)

	if plan.PlanID != created.PlanID {
		t.Fatalf("planID = %s, want %s", plan.PlanID, created.PlanID)
	}
	if plan.Declarations == nil {
		t.Fatal("retrieved plan carried no declarations")
	}
	if plan.Declarations.CompilerSemanticsVersion == "" {
		t.Fatal("declarations omit the pinned compiler version")
	}
	if len(plan.Declarations.Rules.Transformations) != 2 {
		t.Fatalf("declared transformations = %d, want 2", len(plan.Declarations.Rules.Transformations))
	}

	// Resubmitting the retrieved declarations must reproduce the same plan. If
	// it did not, the projection would describe a plan that does not exist.
	resubmitted := createPlan(t, router, "acme", *plan.Declarations)
	if resubmitted.PlanID != created.PlanID {
		t.Fatalf("resubmitted planID = %s, want %s", resubmitted.PlanID, created.PlanID)
	}
}

// Production break caught: reporting another tenant's plan as anything but
// absent leaks its existence, which is an authorization failure even when the
// body is withheld.
func TestGetPlanHidesOtherTenantsPlans(t *testing.T) {
	router := newTestRouter(t)
	created := createPlan(t, router, "acme", fixtureDeclarations(t))

	intruder := get(t, router, "/v1/plans/"+string(created.PlanID), "globex")
	missing := get(t, router, "/v1/plans/sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000", "acme")

	if intruder.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant status = %d, want 404", intruder.Code)
	}
	// Indistinguishable from a plan that never existed: same status, same body.
	if intruder.Body.String() != missing.Body.String() {
		t.Fatalf("a foreign plan is distinguishable from an absent one:\n%s\n%s",
			intruder.Body.String(), missing.Body.String())
	}
}

// Production break caught: this is the route-level scoping assertion deferred
// from the task that added the tenant validator, now that real routes exist.
//
// The routes are enumerated from the router itself rather than listed here, so
// an operation added later is covered without anyone remembering to extend
// this test. That is the whole point: a hand-written table proves only that
// nobody edited the table.
func TestEveryVersionedRouteRejectsAMalformedTenant(t *testing.T) {
	router := newTestRouter(t)

	routes, ok := router.(chi.Routes)
	if !ok {
		t.Fatal("router does not expose its registered routes; this test cannot enumerate them")
	}

	type operation struct{ method, path string }
	var versioned []operation
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/v1/") {
			return nil
		}
		// Substitute any path parameter with a well-formed value, so the only
		// thing wrong with the request is its tenant.
		path := pathParameterPattern.ReplaceAllString(route, placeholderDigest)
		versioned = append(versioned, operation{method: method, path: path})
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(versioned) == 0 {
		t.Fatal("no versioned routes were discovered; the walk found nothing to check")
	}

	for _, op := range versioned {
		for _, tenant := range []string{"", "ACME", "acme_corp", "../etc", "acme\x00", strings.Repeat("a", 129)} {
			var recorder *httptest.ResponseRecorder
			if op.method == http.MethodGet {
				recorder = get(t, router, op.path, tenant)
			} else {
				// An empty object is enough: scoping is enforced before a body
				// is read, so no route should get as far as parsing it.
				recorder = postJSON(t, router, op.path, tenant, map[string]any{})
			}
			if recorder.Code != http.StatusBadRequest {
				t.Errorf("%s %s with tenant %q = %d, want 400",
					op.method, op.path, tenant, recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("%s %s with tenant %q: Content-Type = %q", op.method, op.path, tenant, got)
			}
		}
	}
}

// pathParameterPattern matches a chi path parameter such as {planID}.
var pathParameterPattern = regexp.MustCompile(`\{[^}]+\}`)

const placeholderDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// Production break caught: a body this operation cannot accept must be
// refused, and a non-JSON body must be distinguishable from an invalid one.
func TestCreatePlanRejectsMalformedBodies(t *testing.T) {
	router := newTestRouter(t)

	unknownMember := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/plans",
		bytes.NewReader([]byte(`{"compilerSemanticsVersion":"v1","surprise":true}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(tenantHeader, "acme")
	router.ServeHTTP(unknownMember, request)
	if unknownMember.Code != http.StatusBadRequest {
		t.Errorf("unknown member status = %d, want 400", unknownMember.Code)
	}

	wrongMedia := httptest.NewRecorder()
	formRequest := httptest.NewRequest(http.MethodPost, "/v1/plans", bytes.NewReader([]byte(`a=b`)))
	formRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formRequest.Header.Set(tenantHeader, "acme")
	router.ServeHTTP(wrongMedia, formRequest)
	if wrongMedia.Code != http.StatusUnsupportedMediaType {
		t.Errorf("wrong media type status = %d, want 415", wrongMedia.Code)
	}
}

// fixtureDeclarations renders the ratified team-HOS declarations as the wire
// document a client would send.
func fixtureDeclarations(t *testing.T) openapiv1.PlanDeclarations {
	t.Helper()
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	schema, err := semantic.NewSchema(
		inputs.Compilation.Schema.EntityDeclarations(),
		inputs.Compilation.Schema.RelationDeclarations(),
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}
	// The wire document is obtained by projecting the compiled plan, so the
	// fixture and the contract cannot drift apart. No production test hook is
	// exported to do this: the test lives in the package instead.
	declarations, err := declarationsToWire(plan, compilation.Profiles(), schema,
		inputs.Compilation.CompilerSemanticsVersion)
	if err != nil {
		t.Fatalf("declarationsToWire: %v", err)
	}
	return declarations
}

func createPlan(t *testing.T, router http.Handler, tenant string, declarations openapiv1.PlanDeclarations) openapiv1.Plan {
	t.Helper()
	recorder := postJSON(t, router, "/v1/plans", tenant, declarations)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create plan status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	var plan openapiv1.Plan
	decodeBody(t, recorder, &plan)
	return plan
}

func postJSON(t *testing.T, router http.Handler, path, tenant string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if tenant != "" {
		request.Header.Set(tenantHeader, tenant)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func get(t *testing.T, router http.Handler, path, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if tenant != "" {
		request.Header.Set(tenantHeader, tenant)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, recorder.Body.String())
	}
}

// Production break caught: a document the compiler cannot canonicalize at all,
// such as one with no pinned compiler semantics version, is the caller's
// document being unusable. Reporting it as a 500 would tell an operator the
// service is broken and invite a retry that can only fail identically.
func TestUncanonicalizableDeclarationsAreNotAServerFault(t *testing.T) {
	router := newTestRouter(t)

	recorder := postJSON(t, router, "/v1/plans", "acme", map[string]any{})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	var problem openapiv1.Problem
	decodeBody(t, recorder, &problem)
	if problem.Type != problemBaseURI+"invalid-semantic-input" {
		t.Fatalf("problem type = %s, want invalid-semantic-input", problem.Type)
	}
}

// newTestRouter builds the router over one adapter serving both ports, as the
// process wires it. Two separate stores would model a plan store and an execution
// queue that cannot see each other, which the composition never produces.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(oneStoreDependencies())
}

func oneStoreDependencies() Dependencies {
	store := memory.NewStore()
	return Dependencies{Plans: store, Executions: store}
}

// Production break caught by running the binary, not by the suite: the compiler
// accepts a ruleset with no transformations and returns a plan, but the
// application refuses to execute one. A caller could therefore create a plan,
// submit an execution against it, receive 202, and find the execution terminally
// failed — with no way to clear it, because execution identity is derived and
// resubmitting returns the same failed row. Refusing at creation keeps one rule
// rather than two and closes the trap at the earliest point.
func TestPlanWithNoTransformationsIsRefused(t *testing.T) {
	router := newTestRouter(t)
	declarations := fixtureDeclarations(t)
	declarations.Rules.Transformations = nil

	recorder := postJSON(t, router, "/v1/plans", "acme", declarations)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	var problem openapiv1.Problem
	decodeBody(t, recorder, &problem)
	if problem.Type != problemBaseURI+"invalid-semantic-input" {
		t.Fatalf("problem type = %s", problem.Type)
	}
}
