// Package httpapi owns Maiden Lane's HTTP transport boundary.
//
// It translates HTTP requests into application operations and responses back
// into HTTP. It must not define transformation semantics, promotion policy, or
// publication authority.
//
// The wire contract is api/openapi.yaml, and the Go types in openapiv1 are
// generated from it. Authority runs in one direction: the contract is edited,
// then code is regenerated, never the reverse.
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter returns the complete HTTP surface implemented by this revision.
//
// The versioned subtree carries the tenant boundary as group middleware rather
// than as a per-handler call, so a handler added later is scoped by
// construction and cannot forget to check.
func NewRouter() http.Handler {
	router := chi.NewRouter()

	// Every unmatched request still answers in the API's own error format. A
	// plain-text 404 from the router would be the one response a client could
	// not parse with the generated problem type.
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, problemNotFound, nil)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, problemMethodNotAllowed, nil)
	})

	// Health and readiness are deliberately outside the versioned subtree: they
	// carry no tenant and no authentication, because a load balancer probe has
	// neither and must not be able to fail closed on a missing header.
	router.Get("/healthz", noContent)

	// Readiness currently means process readiness because Maiden Lane has no
	// required external dependencies. Introduce dependency checks here only when
	// the process cannot serve correctly without those dependencies.
	router.Get("/readyz", noContent)

	router.Route("/v1", func(versioned chi.Router) {
		versioned.Use(requireTenant)
		// The subtree owns its own miss handlers so that an unmatched versioned
		// path is still answered from inside the tenant boundary. Without this,
		// chi resolves the miss against the parent router and the scoping
		// middleware never runs.
		versioned.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeProblem(w, problemNotFound, nil)
		})
		versioned.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeProblem(w, problemMethodNotAllowed, nil)
		})
	})

	return router
}

func noContent(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusNoContent)
}
