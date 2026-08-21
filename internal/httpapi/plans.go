package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Dependencies is explicit constructor injection. There is no service locator
// and no package-level state, so a test constructs exactly the server it means
// to exercise.
type Dependencies struct {
	Plans      ports.PlanStore
	Executions ports.ExecutionStore
	Observer   app.Observer

	// Policies, Publications, Comparisons, and Corpora serve the promotion gate, the
	// publication pointer, and replay comparison contracts. All are optional:
	// a nil store makes the corresponding routes report the feature as
	// unavailable rather than panicking, which is what lets a deployment serve
	// the semantic surface without a control plane configured.
	Policies     ports.PolicyStore
	Publications ports.PublicationStore
	Comparisons  ports.ComparisonStore
	Corpora      ports.CorpusStore

	// Instrumenter wraps versioned routes in HTTP telemetry. It is optional:
	// a nil instrumenter serves the same routes untelemetered, which is what
	// handler tests use. Health probes are never wrapped.
	Instrumenter RouteInstrumenter
}

// RouteInstrumenter is the consumer-owned narrow view of the telemetry runtime.
// Declaring it here keeps the dependency pointing one way: this package never
// imports internal/observability, so telemetry cannot reach into transport
// decisions.
type RouteInstrumenter interface {
	InstrumentHTTPRoute(method, pattern string, next http.Handler) http.Handler
}

// server implements the generated ServerInterface. Routing comes from
// api/openapi.yaml through the generated wrapper, so a route cannot drift from
// the published contract.
type server struct {
	deps Dependencies
}

var _ openapiv1.ServerInterface = (*server)(nil)

func (s *server) GetHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) GetReadiness(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// CreatePlan compiles declarations into an immutable plan.
//
// Compilation is the semantic kernel's job. This handler translates, stores,
// and projects; it makes no decision about whether declarations are valid.
func (s *server) CreatePlan(w http.ResponseWriter, r *http.Request, params openapiv1.CreatePlanParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}

	var declarations openapiv1.PlanDeclarations
	if err := decodeJSON(r, &declarations); err != nil {
		writeDecodeProblem(w, err)
		return
	}

	if len(declarations.Rules.Transformations) == 0 {
		// A plan with no transformations compiles, but the application refuses
		// to execute one, so creating it would hand back an artifact that is
		// guaranteed useless. Refusing here keeps one rule instead of two, and
		// it matters more than it looks: an execution that cannot run reaches a
		// terminal failure, and because execution identity is derived the same
		// request can never be resubmitted to clear it.
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	request, err := compileRequestFromWire(declarations)
	if err != nil {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}
	schema, err := schemaFromWire(declarations.Schema)
	if err != nil {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	compilation, err := semantic.Compile(request)
	if err != nil {
		// The compiler splits its two failure modes. An invalid *program* is a
		// typed compilation failure, handled below with its diagnostics. A Go
		// error means the request could not be canonicalized at all, for
		// example with no pinned compiler semantics version. That is the
		// caller's document being unusable, not an internal inconsistency, so
		// it must not be reported as a server fault.
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}
	if failure, present := compilation.Failure(); present {
		// An invalid plan carries its closed diagnostics and no planID,
		// because no plan exists to name.
		writeProblem(w, problemInvalidPlan, diagnosticCodes(failure))
		return
	}
	plan, present := compilation.Plan()
	if !present {
		writeProblem(w, problemInternalError, nil)
		return
	}

	record := ports.PlanRecord{
		TenantID:    tenant,
		PlanID:      plan.ID(),
		Input:       compilation.Input(),
		Schema:      schema,
		Compilation: compilation,
	}
	if err := s.deps.Plans.PutPlan(r.Context(), record); err != nil {
		writeStorageProblem(w, err)
		return
	}

	// Creation returns identities only. Whoever just submitted declarations
	// already holds them; the debug projection belongs on retrieval.
	projected, err := planToWire(plan, compilation.Profiles(), schema, request.CompilerSemanticsVersion, false)
	if err != nil {
		// The plan compiled but cannot be described in this contract version. Refusing is
		// the only honest answer: the alternative is a response describing a rule the
		// server does not hold.
		writeProblem(w, problemInternalError, nil)
		return
	}
	writeJSON(w, http.StatusCreated, projected)
}

// GetPlan returns a stored plan, including the declarations the compiler
// accepted. Another tenant's plan is reported as absent.
func (s *server) GetPlan(w http.ResponseWriter, r *http.Request, planID openapiv1.Digest, params openapiv1.GetPlanParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}

	record, found, err := s.deps.Plans.GetPlan(r.Context(), tenant, semantic.PlanID(planID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}
	plan, present := record.Compilation.Plan()
	if !present {
		writeProblem(w, problemInternalError, nil)
		return
	}

	projected, err := planToWire(plan, record.Compilation.Profiles(),
		record.Schema, plan.CompilerVersion(), true)
	if err != nil {
		writeProblem(w, problemInternalError, nil)
		return
	}
	writeJSON(w, http.StatusOK, projected)
}

// scope validates the tenant header and answers the request when it is
// malformed. The generated wrapper guarantees the header is present, because
// the contract declares it required; this validates its form.
func (s *server) scope(w http.ResponseWriter, raw openapiv1.TenantHeader) (ports.TenantID, bool) {
	tenant, ok := parseTenant(string(raw))
	if !ok {
		writeProblem(w, problemTenantRequired, nil)
		return "", false
	}
	return tenant, true
}

// diagnosticCodes projects a compilation failure's closed diagnostic codes.
// Only the codes travel: a diagnostic's subject and detail describe the
// caller's own declarations and are not republished.
func diagnosticCodes(failure semantic.CompilationFailure) []string {
	diagnostics := failure.Diagnostics()
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, string(diagnostic.Code()))
	}
	return codes
}

// writeDecodeProblem distinguishes an unsupported media type from a body this
// operation cannot accept, so a client can tell "wrong format" from "wrong
// content".
func writeDecodeProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnsupportedMediaType) {
		writeProblem(w, problemUnsupportedMediaType, nil)
		return
	}
	writeProblem(w, problemInvalidRequest, nil)
}

// writeStorageProblem maps a storage failure onto the closed vocabulary.
// Cancellation is the caller giving up, not an internal defect.
func writeStorageProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	writeProblem(w, problemInternalError, nil)
}

func writeJSON(w http.ResponseWriter, status int, document any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The document is built from kernel values and closed tokens, so encoding
	// cannot fail on unsupported values. A write failure means the client is
	// gone, which nothing here can act on.
	_ = json.NewEncoder(w).Encode(document)
}
