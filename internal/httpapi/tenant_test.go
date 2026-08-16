package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Production break caught: a route that reached a handler without a tenant
// would perform an unscoped read, and the scoping boundary would exist only by
// convention at each call site.
func TestVersionedRoutesRequireATenant(t *testing.T) {
	reached := false
	handler := requireTenant(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/plans", nil))

	if reached {
		t.Fatal("the handler ran without a tenant")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
}

// Production break caught: normalizing a malformed tenant instead of rejecting
// it would silently map several distinct inputs onto one scope, which is a
// cross-tenant read with extra steps.
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
			reached := false
			handler := requireTenant(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))
			request := httptest.NewRequest(http.MethodGet, "/v1/plans", nil)
			request.Header.Set(tenantHeader, test.value)

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if reached {
				t.Fatalf("handler ran with a malformed tenant %q", test.value)
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", recorder.Code)
			}
			// The rejected value must never be echoed: a problem document is a
			// response body, and reflecting attacker input into one is how a
			// diagnostic aid becomes an injection vector. Blank inputs have no
			// meaningful needle to search for, so they are checked only for
			// rejection above.
			if strings.TrimSpace(test.value) != "" {
				if body := recorder.Body.String(); strings.Contains(body, test.value) {
					t.Fatalf("response echoed the rejected tenant: %s", body)
				}
			}
			var raw map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
				t.Fatalf("invalid problem JSON: %v", err)
			}
		})
	}
}

// Production break caught: a valid tenant that failed to reach the handler
// context would make every scoped read fall back to an empty tenant.
func TestValidTenantReachesTheHandler(t *testing.T) {
	var seen string
	handler := requireTenant(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		tenant, ok := tenantFrom(r.Context())
		if !ok {
			t.Error("handler could not read the tenant it was scoped to")
			return
		}
		seen = string(tenant)
	}))

	request := httptest.NewRequest(http.MethodGet, "/v1/plans", nil)
	request.Header.Set(tenantHeader, "acme-corp-1")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "acme-corp-1" {
		t.Fatalf("tenant = %q, want acme-corp-1", seen)
	}
}

// Production break caught: reading a tenant from a context that never carried
// one must fail closed. If absence returned an empty-but-valid tenant, a
// missing middleware would produce a silently unscoped query.
func TestTenantFromEmptyContextFailsClosed(t *testing.T) {
	tenant, ok := tenantFrom(t.Context())
	if ok {
		t.Fatalf("an unscoped context yielded tenant %q", tenant)
	}
	if tenant != "" {
		t.Fatalf("tenant = %q, want empty", tenant)
	}
}
