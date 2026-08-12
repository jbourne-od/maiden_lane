// Package httpapi owns Maiden Lane's HTTP transport boundary.
//
// It translates HTTP requests into application operations and responses back
// into HTTP. It must not define transformation semantics, promotion policy, or
// publication authority.
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter returns the complete HTTP surface implemented by this revision.
func NewRouter() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", noContent)

	// Readiness currently means process readiness because Maiden Lane has no
	// required external dependencies. Introduce dependency checks here only when
	// the process cannot serve correctly without those dependencies.
	router.Get("/readyz", noContent)

	return router
}

func noContent(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusNoContent)
}
