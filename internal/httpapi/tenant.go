package httpapi

import (
	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

// This file owns tenant validation.
//
// Authentication is delegated to the deployment gateway: this process trusts
// the header it is given. What it does not delegate is scoping. Every
// versioned operation must arrive with a well-formed tenant, and every stored
// read is keyed by it, so possession of an artifact identity authorizes
// nothing on its own (HLD section 16).
//
// Presence is enforced by the generated wrapper, because the contract declares
// the header required; form is enforced here. Deliberately one validation
// path: a second one would be a second place to get it wrong, and the two
// could disagree about what counts as a tenant.
//
// Validation rejects rather than normalizes. Normalizing a malformed
// identifier would quietly map several distinct inputs onto one scope, which
// is a cross-tenant read with extra steps.

// tenantHeader carries the scope for every versioned operation. It matches the
// parameter declared in api/openapi.yaml.
const tenantHeader = "X-Maiden-Lane-Tenant"

// maxTenantLength bounds the identifier so it cannot become an unbounded
// string in a map key, a log line, or a future metric dimension.
const maxTenantLength = 128

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
