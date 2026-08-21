package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/httpapi"
)

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			httpapi.NewRouter(testDependencies()).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
			}
			if recorder.Body.Len() != 0 {
				t.Fatalf("body = %q, want an empty body", recorder.Body.String())
			}
		})
	}
}

func TestRouterRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	httpapi.NewRouter(testDependencies()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestRouterReturnsNotFoundForUnknownPath(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	httpapi.NewRouter(testDependencies()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestOpenAPIRecordsImplementedHealthSurface(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}

	contract := string(data)
	for _, fragment := range []string{"openapi: 3.1.0", "/healthz:", "/readyz:"} {
		if !strings.Contains(contract, fragment) {
			t.Errorf("OpenAPI contract does not contain %q", fragment)
		}
	}
	if count := strings.Count(contract, `"204":`); count != 2 {
		t.Errorf("204 response count = %d, want 2", count)
	}
}

// Production break caught: requiring a tenant on the health endpoints would
// make a load balancer probe fail closed, because a probe carries no headers.
//
// The complementary property, that every registered /v1 route rejects a
// missing tenant, is asserted against the real routes in the task that adds
// them. It is deliberately not asserted here: no /v1 route exists yet, and a
// test that enumerates an empty set passes without proving anything. The
// middleware itself is covered in isolation by TestVersionedRoutesRequireATenant.
func TestHealthEndpointsNeedNoTenant(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(testDependencies())

	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s without a tenant = %d, want 204", path, recorder.Code)
		}
	}
}

// Production break caught: a router-default 404 is text/plain, which is the one
// response a generated client cannot decode with the contract's problem type.
func TestUnmatchedRoutesStillAnswerAsProblems(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	httpapi.NewRouter(testDependencies()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/no-such-route", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	if !strings.Contains(recorder.Body.String(), "/problems/not-found") {
		t.Fatalf("body is not a ratified problem: %s", recorder.Body.String())
	}
}

// testDependencies builds a router over one real in-process adapter serving both
// ports, as the process wires it. Routing and problem rendering are what these
// tests exercise, and a stub would not exercise the same wiring.
func testDependencies() httpapi.Dependencies {
	store := memory.NewStore()
	return httpapi.Dependencies{
		Plans:        store,
		Executions:   store,
		Policies:     store,
		Publications: store,
		Comparisons:  store,
	}
}
