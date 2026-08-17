package httpapi

import (
	"strings"
	"testing"
)

// Production break caught: normalizing a malformed tenant instead of rejecting
// it would silently map several distinct inputs onto one scope, which is a
// cross-tenant read with extra steps.
//
// This exercises the validator directly. That every registered /v1 route
// actually applies it is proved separately, over the real routes, by
// TestEveryVersionedRouteRejectsAMalformedTenant.
func TestMalformedTenantsAreRejectedNotNormalized(t *testing.T) {
	hostile := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"uppercase", "ACME"},
		{"leading dash", "-acme"},
		{"underscore", "acme_corp"},
		{"path traversal", "../../etc/passwd"},
		{"newline injection", "acme\r\nX-Injected: 1"},
		{"null byte", "acme\x00"},
		{"unicode lookalike", "acmе"}, // Cyrillic 'е'
		{"too long", strings.Repeat("a", 129)},
		{"sql-ish", "acme'; DROP TABLE plans;--"},
		{"wildcard", "*"},
	}
	for _, test := range hostile {
		t.Run(test.name, func(t *testing.T) {
			if tenant, ok := parseTenant(test.value); ok {
				t.Fatalf("parseTenant accepted %q as %q", test.value, tenant)
			}
		})
	}
}

// Production break caught: a valid tenant must be accepted unchanged, or every
// request would be refused rather than only malformed ones.
func TestValidTenantsAreAcceptedVerbatim(t *testing.T) {
	for _, value := range []string{"acme", "acme-corp-1", "a", "0", strings.Repeat("a", 128)} {
		tenant, ok := parseTenant(value)
		if !ok {
			t.Errorf("parseTenant rejected the well-formed tenant %q", value)
			continue
		}
		if string(tenant) != value {
			t.Errorf("parseTenant(%q) = %q; the value was altered", value, tenant)
		}
	}
}
