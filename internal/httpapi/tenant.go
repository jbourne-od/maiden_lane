package httpapi

import (
	"context"
	"net/http"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

// This file owns the tenant scoping boundary.
//
// Authentication is delegated to the deployment gateway: this process trusts
// the header it is given. What it does not delegate is scoping. Every
// versioned operation must arrive with a well-formed tenant, and every stored
// read is keyed by it, so possession of an artifact identity authorizes
// nothing on its own (HLD section 16).
//
// The middleware rejects rather than normalizes. Normalizing a malformed
// identifier would quietly map several distinct inputs onto one scope, which
// is a cross-tenant read with extra steps.

// tenantHeader carries the scope for every versioned operation. It matches the
// parameter declared in api/openapi.yaml.
const tenantHeader = "X-Maiden-Lane-Tenant"

// maxTenantLength bounds the identifier so it cannot become an unbounded
// string in a map key, a log line, or a future metric dimension.
const maxTenantLength = 128

// tenantContextKey is a private type, so no other package can plant or read a
// tenant in the context. The scope can only come from this middleware.
type tenantContextKey struct{}

// requireTenant validates the tenant header and attaches it to the request
// context. A request without a well-formed tenant never reaches a handler.
func requireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := parseTenant(r.Header.Get(tenantHeader))
		if !ok {
			// The rejected value is deliberately not echoed. Reflecting
			// attacker-controlled input into a response body is how a
			// diagnostic aid becomes an injection vector.
			writeProblem(w, problemTenantRequired, nil)
			return
		}
		ctx := context.WithValue(r.Context(), tenantContextKey{}, tenant)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// parseTenant accepts only the exact form the contract declares:
// `^[a-z0-9][a-z0-9-]*$` within the length bound. The check is written out
// rather than delegated to a regular expression so its cost is fixed and its
// behavior on non-ASCII bytes is obvious.
func parseTenant(raw string) (ports.TenantID, bool) {
	if len(raw) == 0 || len(raw) > maxTenantLength {
		return "", false
	}
	for i := range len(raw) {
		c := raw[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return "", false
		}
	}
	return ports.TenantID(raw), true
}

// tenantFrom reports the scope established for this request.
//
// It fails closed: a context that never passed through requireTenant yields
// no tenant rather than an empty one, so a missing middleware cannot silently
// become an unscoped query.
func tenantFrom(ctx context.Context) (ports.TenantID, bool) {
	tenant, ok := ctx.Value(tenantContextKey{}).(ports.TenantID)
	if !ok || tenant == "" {
		return "", false
	}
	return tenant, true
}
