package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/httpapi"
)

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			httpapi.NewRouter().ServeHTTP(recorder, request)

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
	httpapi.NewRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestRouterReturnsNotFoundForUnknownPath(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	httpapi.NewRouter().ServeHTTP(recorder, request)

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
