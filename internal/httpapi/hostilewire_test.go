package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
)

// This corpus exists because of a defect the typed handler tests could not
// reach.
//
// POST /v1/plans with an empty JSON document once returned 500. The suite was
// green throughout, because building requests from generated Go types made an
// entire class of documents — syntactically valid JSON that cannot be
// canonicalized into a compiler request — awkward to express by accident.
// Running the real binary broke through that abstraction immediately.
//
// The lesson is not that unit tests are untrustworthy. It is that a typed
// harness cannot easily reach that class, so these cases are written as raw
// bytes rather than as more typed happy-path requests. Add to this corpus when
// a new shape of hostile or degenerate wire input is discovered; do not
// translate it into a typed literal, which would put it back out of reach.

// hostileBody is one raw request body and the marker that must never appear in
// a problem document.
type hostileBody struct {
	name   string
	body   string
	marker string
}

// malformedCorpus holds documents that are structurally unusable for the
// operation. Each must be refused, never accepted and never a server fault.
func malformedCorpus() []hostileBody {
	return []hostileBody{
		{"empty body", "", ""},
		{"null document", `null`, ""},
		{"array instead of object", `[]`, ""},
		{"bare string", `"bare-string-marker"`, "bare-string-marker"},
		{"number", `42`, ""},
		{"truncated object", `{"compilerSemanticsVersion":`, ""},
		{"unterminated string", `{"compilerSemanticsVersion":"v1`, ""},
		{"trailing garbage", `{"compilerSemanticsVersion":"v1"}<script>`, "<script>"},
		{"two documents", `{"compilerSemanticsVersion":"v1"}{"compilerSemanticsVersion":"v2"}`, ""},
		{"unknown member", `{"compilerSemanticsVersion":"v1","evil":"payload-marker"}`, "payload-marker"},
		{"wrong member type", `{"compilerSemanticsVersion":{"nested":"object-marker"}}`, "object-marker"},
		{"deeply nested", `{"compilerSemanticsVersion":"v1","schema":` +
			strings.Repeat(`{"entities":[{"kind":"a","fields":[]},`, 40) +
			strings.Repeat(`]}`, 40) + `}`, ""},
	}
}

// hostileValueCorpus holds documents that are structurally VALID but carry
// alarming string content. These are deliberately not required to be refused.
//
// The kernel treats a string as exact UTF-8 bytes with no normalization, and a
// caller's own odd compiler-version token is their data, not an attack on this
// service: it changes the plan identity, which is correct, and it is returned
// only to the tenant that sent it. What must hold is narrower and more
// important — the service must never fault on them, and must never reflect
// them into a problem document, which is the response an operator or a shared
// error channel is most likely to render.
func hostileValueCorpus() []hostileBody {
	return []hostileBody{
		{"null byte in value", "{\"compilerSemanticsVersion\":\"v1\\u0000nul-marker\"}", "nul-marker"},
		{"script injection in value", `{"compilerSemanticsVersion":"<img src=x onerror=alert(1)>"}`, "onerror"},
		{"sql-ish value", `{"compilerSemanticsVersion":"'; DROP TABLE plans;--"}`, "DROP TABLE"},
		{"path traversal value", `{"compilerSemanticsVersion":"../../../etc/passwd"}`, "etc/passwd"},
		{"very long value", `{"compilerSemanticsVersion":"` + strings.Repeat("a", 200000) + `"}`, ""},
		{"duplicate members", `{"compilerSemanticsVersion":"v1","compilerSemanticsVersion":"v2"}`, ""},
		{"unicode direction override", "{\"compilerSemanticsVersion\":\"v1\\u202Emarker-rtl\"}", "marker-rtl"},
	}
}

// acceptableHostileStatuses are the only answers a degenerate document may
// receive. A 500 would tell an operator the service is broken when the caller
// sent something it could never accept, and would invite a retry that can only
// fail identically.
var acceptableHostileStatuses = []int{
	http.StatusBadRequest,
	http.StatusUnsupportedMediaType,
	http.StatusUnprocessableEntity,
}

// Production break caught: a structurally unusable document must be refused as
// a client error, never accepted and never reported as a server fault. This is
// the class that produced the 500 the typed tests could not reach.
func TestMalformedWireBodiesAreRefusedSafely(t *testing.T) {
	router := NewRouter(Dependencies{Plans: memory.NewStore(), Runner: ProductionRunner()})

	for _, path := range []string{"/v1/plans", "/v1/executions"} {
		for _, hostile := range malformedCorpus() {
			t.Run(path+"/"+hostile.name, func(t *testing.T) {
				recorder := postRaw(t, router, path, "acme", hostile.body)

				if !slices.Contains(acceptableHostileStatuses, recorder.Code) {
					t.Fatalf("status = %d, want one of %v: %s",
						recorder.Code, acceptableHostileStatuses, recorder.Body.String())
				}
				assertSafeProblem(t, recorder, hostile.marker)
			})
		}
	}
}

// Production break caught: alarming string content must not fault the service,
// and must not be reflected into a problem document even when it is refused
// for some other reason.
func TestHostileValuesNeverFaultOrReflect(t *testing.T) {
	router := NewRouter(Dependencies{Plans: memory.NewStore(), Runner: ProductionRunner()})

	for _, path := range []string{"/v1/plans", "/v1/executions"} {
		for _, hostile := range hostileValueCorpus() {
			t.Run(path+"/"+hostile.name, func(t *testing.T) {
				recorder := postRaw(t, router, path, "acme", hostile.body)

				if recorder.Code >= http.StatusInternalServerError {
					t.Fatalf("status = %d; hostile content must never fault the service: %s",
						recorder.Code, recorder.Body.String())
				}
				if recorder.Code >= http.StatusBadRequest {
					assertSafeProblem(t, recorder, hostile.marker)
				}
			})
		}
	}
}

// assertSafeProblem requires a decodable problem from the ratified vocabulary
// that does not reflect the caller's input.
func assertSafeProblem(t *testing.T, recorder *httptest.ResponseRecorder, marker string) {
	t.Helper()
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	if marker != "" && bytes.Contains(recorder.Body.Bytes(), []byte(marker)) {
		t.Fatalf("problem reflected caller input %q: %s", marker, recorder.Body.String())
	}
	var problem struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	decodeBody(t, recorder, &problem)
	if !strings.HasPrefix(problem.Type, problemBaseURI) {
		t.Fatalf("problem type %q is outside the ratified vocabulary", problem.Type)
	}
	if problem.Status != recorder.Code {
		t.Fatalf("problem status %d disagrees with HTTP status %d", problem.Status, recorder.Code)
	}
}

func postRaw(t *testing.T, router http.Handler, path, tenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(tenantHeader, tenant)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// Production break caught: an unbounded body would let one request exhaust
// process memory. The limit must refuse rather than truncate, because a
// truncated document could parse into something the caller never sent.
func TestOversizedBodiesAreRefusedRatherThanTruncated(t *testing.T) {
	router := NewRouter(Dependencies{Plans: memory.NewStore(), Runner: ProductionRunner()})

	oversized := `{"compilerSemanticsVersion":"` + strings.Repeat("a", maxRequestBytes+1024) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/plans", strings.NewReader(oversized))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(tenantHeader, "acme")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if !slices.Contains(acceptableHostileStatuses, recorder.Code) {
		t.Fatalf("status = %d, want a client error", recorder.Code)
	}
}

// Production break caught: a hostile tenant travels on every versioned route,
// so it is checked here against raw bytes as well as through the validator.
func TestHostileTenantsNeverReachAResponseBody(t *testing.T) {
	router := NewRouter(Dependencies{Plans: memory.NewStore(), Runner: ProductionRunner()})

	hostile := []string{
		"<script>alert(1)</script>",
		"acme\r\nX-Injected: yes",
		"../../etc/passwd",
		"'; DROP TABLE plans;--",
		strings.Repeat("z", 500),
	}
	for _, tenant := range hostile {
		request := httptest.NewRequest(http.MethodPost, "/v1/plans", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		// Header.Set would reject a CRLF value at write time; set it directly so
		// the server sees exactly what a hostile client could send.
		request.Header["X-Maiden-Lane-Tenant"] = []string{tenant}

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Errorf("tenant %q = %d, want 400", tenant, recorder.Code)
		}
		if bytes.Contains(recorder.Body.Bytes(), []byte(strings.TrimSpace(tenant))) {
			t.Errorf("response reflected hostile tenant %q: %s", tenant, recorder.Body.String())
		}
		for _, values := range recorder.Header() {
			for _, value := range values {
				if strings.Contains(value, "Injected") {
					t.Errorf("a header was injected from the tenant value: %q", value)
				}
			}
		}
	}
}
