// Package httpapi owns Maiden Lane's HTTP transport boundary.
//
// It translates HTTP requests into application operations and responses back
// into HTTP. It must not define transformation semantics, promotion policy, or
// publication authority.
//
// The wire contract is api/openapi.yaml, and the Go types in openapiv1 are
// generated from it. Authority runs in one direction: the contract is edited,
// then code is regenerated, never the reverse. Routing comes from the generated
// wrapper for the same reason, so a route cannot exist that the published
// contract does not declare.
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
)

// NewRouter returns the complete HTTP surface implemented by this revision.
func NewRouter(deps Dependencies) http.Handler {
	base := chi.NewRouter()

	// Every unmatched request still answers in the API's own error format. A
	// plain-text 404 from the router would be the one response a client could
	// not parse with the generated problem type.
	base.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, problemNotFound, nil)
	})
	base.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, problemMethodNotAllowed, nil)
	})

	return openapiv1.HandlerWithOptions(&server{deps: deps}, openapiv1.ChiServerOptions{
		BaseRouter:       base,
		ErrorHandlerFunc: writeParameterProblem,
		Middlewares:      []openapiv1.MiddlewareFunc{instrumentVersionedRoutes(deps.Instrumenter)},
	})
}

// writeParameterProblem renders the generated wrapper's parameter-binding
// failures. Without it the generated default answers text/plain, which is the
// one response shape a generated client cannot decode.
//
// The bound value is never echoed, so a malformed header or path segment
// cannot reflect attacker-controlled input into a response body.
func writeParameterProblem(w http.ResponseWriter, _ *http.Request, err error) {
	var missingHeader *openapiv1.RequiredHeaderError
	var repeatedParam *openapiv1.TooManyValuesForParamError
	switch {
	case errors.As(err, &missingHeader), errors.As(err, &repeatedParam):
		// The only required header in this contract is the tenant, and a
		// repeated one is equally unscoped: neither identifies a single tenant.
		writeProblem(w, problemTenantRequired, nil)
	default:
		writeProblem(w, problemInvalidRequest, nil)
	}
}

// instrumentVersionedRoutes wraps each matched versioned route in HTTP
// telemetry.
//
// The dimension is the registered route pattern, never the request path. That
// distinction is the whole cardinality boundary: /v1/plans/{planID} is one
// bounded series, while the path it matched carries a content digest and would
// mint a new series per plan. The pattern comes from chi's route context, which
// is registration metadata this process controls, rather than from anything the
// caller sent.
//
// Health probes are deliberately excluded, matching the existing contract that
// only matched non-health handlers are instrumented.
func instrumentVersionedRoutes(instrumenter RouteInstrumenter) openapiv1.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		if instrumenter == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pattern := chi.RouteContext(r.Context()).RoutePattern()
			if !strings.HasPrefix(pattern, "/v1/") {
				next.ServeHTTP(w, r)
				return
			}
			// The wrapped handler is built per request and deliberately not
			// cached: the generated wrapper binds this request's decoded
			// parameters into next, so a handler reused across requests would
			// serve one request's parameters to another.
			instrumenter.InstrumentHTTPRoute(r.Method, pattern, next).ServeHTTP(w, r)
		})
	}
}
