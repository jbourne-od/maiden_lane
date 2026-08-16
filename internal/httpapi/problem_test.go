package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
)

// Production break caught: a problem rendered with the wrong media type is not
// a problem document to any conforming client, so error handling silently
// degrades to "some 4xx with a body".
func TestProblemsRenderTheRatifiedContract(t *testing.T) {
	tests := []struct {
		kind   problemKind
		status int
		slug   string
	}{
		{problemInvalidRequest, http.StatusBadRequest, "invalid-request"},
		{problemTenantRequired, http.StatusBadRequest, "tenant-required"},
		{problemNotFound, http.StatusNotFound, "not-found"},
		{problemUnsupportedMediaType, http.StatusUnsupportedMediaType, "unsupported-media-type"},
		{problemInvalidPlan, http.StatusUnprocessableEntity, "invalid-plan"},
		{problemInvalidSemanticInput, http.StatusUnprocessableEntity, "invalid-semantic-input"},
		{problemInternalError, http.StatusInternalServerError, "internal-error"},
		{problemDependencyUnavailable, http.StatusServiceUnavailable, "dependency-unavailable"},
	}
	for _, test := range tests {
		t.Run(test.slug, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeProblem(recorder, test.kind, nil)

			if recorder.Code != test.status {
				t.Errorf("status = %d, want %d", recorder.Code, test.status)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			var problem openapiv1.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatalf("problem body is not valid JSON: %v", err)
			}
			if want := problemBaseURI + test.slug; problem.Type != want {
				t.Errorf("type = %q, want %q", problem.Type, want)
			}
			if problem.Status != int32(test.status) {
				t.Errorf("body status = %d, want %d", problem.Status, test.status)
			}
			if problem.Title == "" {
				t.Error("problem has no title")
			}
		})
	}
}

// Production break caught: the whole point of the closed vocabulary is that a
// caller cannot mint a new problem type at a call site. An unknown kind must
// degrade to internal-error rather than emit an empty or invented type URI.
func TestUnknownProblemKindDegradesToInternalError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeProblem(recorder, problemKind("not-a-ratified-kind"), nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	var problem openapiv1.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if want := problemBaseURI + "internal-error"; problem.Type != want {
		t.Fatalf("type = %q, want %q", problem.Type, want)
	}
	if strings.Contains(recorder.Body.String(), "not-a-ratified-kind") {
		t.Fatal("the unrecognized kind was echoed into the response")
	}
}

// Production break caught: compilation diagnostics are the one place a problem
// carries detail, and they must be closed codes only. A diagnostic subject or
// message would republish the caller's declarations, and an unrecognized code
// would leak whatever string produced it.
func TestCompilationDiagnosticsCarryOnlyClosedCodes(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeProblem(recorder, problemInvalidPlan, []string{"UNKNOWN_FIELD", "DEPENDENCY_CYCLE"})

	var problem openapiv1.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if problem.Diagnostics == nil {
		t.Fatal("invalid-plan problem carries no diagnostics")
	}
	got := make([]string, 0, len(*problem.Diagnostics))
	for _, diagnostic := range *problem.Diagnostics {
		got = append(got, string(diagnostic.Code))
	}
	if !slices.Equal(got, []string{"UNKNOWN_FIELD", "DEPENDENCY_CYCLE"}) {
		t.Fatalf("diagnostics = %v", got)
	}
}

// Production break caught: a diagnostic code from outside the ratified
// vocabulary would put an unbounded string into a published response.
func TestUnrecognizedDiagnosticCodesAreDropped(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeProblem(recorder, problemInvalidPlan, []string{"UNKNOWN_FIELD", "SOMETHING_INVENTED", ""})

	body := recorder.Body.String()
	if strings.Contains(body, "SOMETHING_INVENTED") {
		t.Fatal("an unratified diagnostic code reached the response")
	}
	var problem openapiv1.Problem
	if err := json.Unmarshal([]byte(body), &problem); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if problem.Diagnostics == nil || len(*problem.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want only the ratified code", problem.Diagnostics)
	}
}

// Production break caught: a problem document is the most likely place for
// internal detail to escape, because it is written on the failure path where
// the tempting thing is to include the cause.
func TestProblemsNeverCarryDetailBeyondFixedText(t *testing.T) {
	for kind := range problemCatalog {
		recorder := httptest.NewRecorder()
		writeProblem(recorder, kind, nil)

		var raw map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		for key := range raw {
			switch key {
			case "type", "title", "status", "detail", "code", "diagnostics":
			default:
				t.Errorf("problem %q carries unadmitted member %q", kind, key)
			}
		}
	}
}
