package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
)

// recordingInstrumenter captures what would become a telemetry dimension.
type recordingInstrumenter struct {
	mu      sync.Mutex
	wrapped []string
}

func (i *recordingInstrumenter) InstrumentHTTPRoute(method, pattern string, next http.Handler) http.Handler {
	i.mu.Lock()
	i.wrapped = append(i.wrapped, method+" "+pattern)
	i.mu.Unlock()
	return next
}

func (i *recordingInstrumenter) observed() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.wrapped)
}

// Production break caught: instrumenting by request path instead of route
// pattern would mint one metric series per plan identity. Plan IDs are content
// digests, so the cardinality is unbounded and attacker-influenced: anyone able
// to call the API could grow the metric store without limit. This is the exact
// failure the observability slice's route-template rule exists to prevent.
func TestInstrumentationUsesRoutePatternsNeverRequestPaths(t *testing.T) {
	instrumenter := &recordingInstrumenter{}
	router := NewRouter(Dependencies{
		Plans:        memory.NewStore(),
		Runner:       ProductionRunner(),
		Instrumenter: instrumenter,
	})

	created := createPlan(t, router, "acme", fixtureDeclarations(t))
	get(t, router, "/v1/plans/"+string(created.PlanID), "acme")
	postJSON(t, router, "/v1/executions", "acme", executionRequest(t, created.PlanID, teamhos.Passing))

	observed := instrumenter.observed()
	if len(observed) == 0 {
		t.Fatal("no route was instrumented")
	}
	for _, entry := range observed {
		if strings.Contains(entry, "sha256:") {
			t.Errorf("a content digest reached a telemetry dimension: %q", entry)
		}
		if !strings.Contains(entry, "/v1/") {
			t.Errorf("a non-versioned route was instrumented: %q", entry)
		}
	}
	// The parameterized route must appear as its template.
	if !slices.Contains(observed, "GET /v1/plans/{planID}") {
		t.Errorf("observed = %v, want the GET route as its template", observed)
	}
}

// Production break caught: wrapping the health probes would put load-balancer
// traffic into request metrics and contradicts the existing contract that only
// matched non-health handlers are instrumented.
func TestHealthProbesAreNeverInstrumented(t *testing.T) {
	instrumenter := &recordingInstrumenter{}
	router := NewRouter(Dependencies{
		Plans:        memory.NewStore(),
		Runner:       ProductionRunner(),
		Instrumenter: instrumenter,
	})

	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := get(t, router, path, "")
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s = %d, want 204", path, recorder.Code)
		}
	}
	if observed := instrumenter.observed(); len(observed) != 0 {
		t.Fatalf("health probes were instrumented: %v", observed)
	}
}

// Production break caught: a nil instrumenter must serve the same routes rather
// than panic, because telemetry is non-authoritative and its absence cannot be
// allowed to change what the API does.
func TestRoutesServeWithoutAnInstrumenter(t *testing.T) {
	router := NewRouter(Dependencies{Plans: memory.NewStore(), Runner: ProductionRunner()})
	created := createPlan(t, router, "acme", fixtureDeclarations(t))
	if created.PlanID == "" {
		t.Fatal("no plan was created without an instrumenter")
	}
}
