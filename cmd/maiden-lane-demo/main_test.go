package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The page must open on the committed payloads, because those are the ones
// internal/httpapi/examples_test.go pins to the ratified fixture's identities. If
// the settings endpoint served anything else, the demo would show a program nobody
// verified while looking exactly as convincing.
func TestSettingsServeTheCommittedPayloads(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "plan.json", `{"compilerSemanticsVersion":"v1"}`)
	writeFile(t, directory, "execution.json", `{"planID":"sha256:abc"}`)

	loaded, err := loadSettings(directory, "http://grafana.example", "acme")
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if got := string(loaded.Plan); got != `{"compilerSemanticsVersion":"v1"}` {
		t.Fatalf("plan = %s, want the file's exact bytes", got)
	}
	if got := string(loaded.Execution); got != `{"planID":"sha256:abc"}` {
		t.Fatalf("execution = %s, want the file's exact bytes", got)
	}
	if loaded.Tenant != "acme" || loaded.Grafana != "http://grafana.example" {
		t.Fatalf("settings carried %q / %q", loaded.Tenant, loaded.Grafana)
	}
}

// A missing or malformed payload must stop the demo from starting.
//
// The alternative is a server that comes up and a page that breaks once someone is
// watching, which sends them to look at the service rather than at the file. This is
// the whole reason the files are read at startup instead of on request.
func TestMalformedPayloadsRefuseToStart(t *testing.T) {
	t.Run("absent directory", func(t *testing.T) {
		if _, err := loadSettings(filepath.Join(t.TempDir(), "nope"), "", "acme"); err == nil {
			t.Fatal("a missing examples directory started successfully")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		directory := t.TempDir()
		writeFile(t, directory, "plan.json", `{"truncated":`)
		writeFile(t, directory, "execution.json", `{}`)
		_, err := loadSettings(directory, "", "acme")
		if err == nil {
			t.Fatal("a malformed plan started successfully")
		}
		if !strings.Contains(err.Error(), "not valid JSON") {
			t.Fatalf("error = %v, want it to name the problem", err)
		}
	})

	t.Run("missing execution", func(t *testing.T) {
		directory := t.TempDir()
		writeFile(t, directory, "plan.json", `{}`)
		if _, err := loadSettings(directory, "", "acme"); err == nil {
			t.Fatal("a missing execution payload started successfully")
		}
	})
}

// The proxy must pass the request through unaltered, and in particular must not
// supply the tenant header itself.
//
// The page's credibility rests on the browser's network tab being a truthful record
// of the API being exercised. A proxy that quietly added headers would mean the
// service received a request nobody could see, and the demo would be demonstrating
// the proxy.
func TestTheProxyDoesNotSpeakForThePage(t *testing.T) {
	var received *http.Request
	var body []byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Clone(r.Context())
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"planID":"sha256:proxied"}`))
	}))
	defer api.Close()

	ui := startUI(t, api.URL)
	defer ui.Close()

	request, err := http.NewRequest(http.MethodPost, ui.URL+"/v1/plans", strings.NewReader(`{"declared":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Maiden-Lane-Tenant", "acme")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post through the proxy: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want the API's 201", response.StatusCode)
	}
	if received == nil {
		t.Fatal("the API was never reached")
	}
	if got := received.URL.Path; got != "/v1/plans" {
		t.Fatalf("the API saw path %q", got)
	}
	if got := received.Header.Get("X-Maiden-Lane-Tenant"); got != "acme" {
		t.Fatalf("tenant header reached the API as %q, want the page's own value", got)
	}
	if got := string(body); got != `{"declared":true}` {
		t.Fatalf("the API saw body %q, want it unaltered", got)
	}
	// Nothing may be invented on the way through.
	if got := received.Header.Get("Authorization"); got != "" {
		t.Fatalf("the proxy added an Authorization header: %q", got)
	}
}

// An unreachable service must produce a legible answer rather than a bare failure.
// The demo's most likely breakage is that nobody started the service, and the page
// has to be able to say so.
func TestAnUnreachableServiceIsReportedAsSuch(t *testing.T) {
	// A port nothing listens on: bind one, learn its address, release it.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := closed.URL
	closed.Close()

	ui := startUI(t, address)
	defer ui.Close()

	response, err := http.Get(ui.URL + "/v1/plans")
	if err != nil {
		t.Fatalf("request through the proxy: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	var problem map[string]string
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatalf("decode the failure: %v", err)
	}
	if !strings.Contains(problem["detail"], address) {
		t.Fatalf("detail = %q, want it to name the address it could not reach", problem["detail"])
	}
}

// The settings endpoint and the page itself must both be served, since a demo that
// serves one without the other is a blank screen.
func TestThePageAndItsSettingsAreBothServed(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	ui := startUI(t, api.URL)
	defer ui.Close()

	for path, wanted := range map[string]string{
		"/":              "Maiden Lane",
		"/app.js":        "POST /v1/executions",
		"/app.css":       "--accent",
		"/demo/settings": `"tenant"`,
	} {
		response, err := http.Get(ui.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		content, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, response.StatusCode)
		}
		if !strings.Contains(string(content), wanted) {
			t.Fatalf("GET %s did not contain %q", path, wanted)
		}
	}
}

// startUI builds the demo handler over a temporary payload directory, exercising the
// same construction main uses.
func startUI(t *testing.T, apiBase string) *httptest.Server {
	t.Helper()
	directory := t.TempDir()
	writeFile(t, directory, "plan.json", `{"compilerSemanticsVersion":"v1"}`)
	writeFile(t, directory, "execution.json", `{"planID":"sha256:abc"}`)

	handler, err := newHandler(apiBase, directory, "http://grafana.example", "acme")
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return httptest.NewServer(handler)
}

func writeFile(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
