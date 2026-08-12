# Initial Repository Scaffolding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a minimal runnable Maiden Lane Go application, local verification interface, production-shaped container, and GitHub Actions pipeline that publishes tested immutable images to ECR.

**Architecture:** Keep composition and process lifecycle in `cmd/maiden-lane` and HTTP transport behavior in `internal/httpapi`; do not scaffold transformation packages before their semantics are designed. Track Go analysis tools through Go 1.26 tool directives, use one non-root image for the API and future worker modes, and make ECR publication the current CD boundary.

**Tech Stack:** Go 1.26.5, chi v5.3.1, Staticcheck 2026.2rc1 (`honnef.co/go/tools@v0.8.0-rc.1`), govulncheck v1.6.0, Docker, GitHub Actions, Amazon ECR, GitHub OIDC.

## Global Constraints

- Work in an isolated `codex/` worktree created with `superpowers:using-git-worktrees` before implementation.
- Module path: `github.com/optimaldynamics/maiden-lane`.
- Language floor: `go 1.26.0`; initial selected toolchain: `go1.26.5`.
- Staticcheck must remain pinned to release `2026.2rc1`, represented by module version `v0.8.0-rc.1`.
- govulncheck must remain pinned to `v1.6.0`.
- Do not use `@latest` in repository or CI tool installation.
- Do not add semantic model, compiler, executor, invariant, provenance, promotion, persistence, AWS adapter, or stochflow packages.
- Do not implement the future worker command.
- Do not add stable typed-error entries to `ERRORS.md` or exported metrics to `METRICS.md`.
- Use RED → GREEN → REFACTOR for HTTP and command behavior.
- Add package and “why” documentation for Python-oriented maintainers without narrating obvious Go syntax.
- Keep the implementation guide strictly current-state; Git is the historical record.
- ECR is publication only. Do not add ECS, Batch, database, S3, or deployment mutations.
- Preserve the HLD, Inviolates, glossary, error registry, and metrics registry unless a task below explicitly names a required edit.

---

### Task 1: Establish the Go module and tracked analysis tools

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `.gitignore`

**Interfaces:**
- Produces: Go module `github.com/optimaldynamics/maiden-lane`; tracked commands `go tool staticcheck` and `go tool govulncheck`.
- Consumes: Go 1.26.5 installed locally or downloaded through Go toolchain selection.

- [ ] **Step 1: Initialize the module with the exact language and toolchain contracts**

Run:

```bash
go mod init github.com/optimaldynamics/maiden-lane
go mod edit -go=1.26.0 -toolchain=go1.26.5
```

Expected `go.mod` prefix:

```text
module github.com/optimaldynamics/maiden-lane

go 1.26.0

toolchain go1.26.5
```

- [ ] **Step 2: Add the analysis tools through Go tool directives**

Run:

```bash
go get -tool honnef.co/go/tools/cmd/staticcheck@2026.2rc1
go get -tool golang.org/x/vuln/cmd/govulncheck@v1.6.0
go mod tidy
```

Inspect `go.mod`. It must contain tool declarations for:

```text
golang.org/x/vuln/cmd/govulncheck
honnef.co/go/tools/cmd/staticcheck
```

and direct module requirements resolving to:

```text
golang.org/x/vuln v1.6.0
honnef.co/go/tools v0.8.0-rc.1
```

- [ ] **Step 3: Add repository-local ignore rules**

Create `.gitignore`:

```gitignore
# Local build and test artifacts.
/bin/
/coverage.out

# Local configuration may contain credentials and never belongs in Git.
.env
.env.*

# Workstation metadata.
.DS_Store
.idea/
.vscode/
```

- [ ] **Step 4: Verify the selected tools rather than ambient binaries**

Run:

```bash
go version
go tool staticcheck -version
go tool govulncheck -version
go mod tidy -diff
```

Expected evidence includes:

```text
go version go1.26.5
staticcheck 2026.2rc1 (v0.8.0-rc.1)
Scanner: govulncheck@v1.6.0
```

`go mod tidy -diff` must print no diff.

- [ ] **Step 5: Commit the module foundation**

```bash
git add go.mod go.sum .gitignore
git commit -m "build: initialize Go module and tools"
```

---

### Task 2: Define and implement the HTTP health boundary

**Files:**
- Create: `internal/httpapi/router_test.go`
- Create: `internal/httpapi/router.go`
- Create: `api/openapi.yaml`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `func httpapi.NewRouter() http.Handler`.
- Produces: `GET /healthz` and `GET /readyz`, both returning `204 No Content` with empty bodies.
- Consumes: `github.com/go-chi/chi/v5@v5.3.1`.

- [ ] **Step 1: Write failing router behavior tests**

Create `internal/httpapi/router_test.go`:

```go
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
```

The OpenAPI test deliberately checks only the tiny surface implemented in this scaffold. A full OpenAPI parser is not justified until the business API begins.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/httpapi
```

Expected: compilation fails because `internal/httpapi` and `NewRouter` do not exist.

- [ ] **Step 3: Add chi and implement the minimal transport boundary**

Run:

```bash
go get github.com/go-chi/chi/v5@v5.3.1
```

Create `internal/httpapi/router.go`:

```go
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
```

- [ ] **Step 4: Write the authoritative health-only OpenAPI contract**

Create `api/openapi.yaml`:

```yaml
openapi: 3.1.0
info:
  title: Maiden Lane API
  version: 0.0.0
  description: The HTTP surface currently implemented by Maiden Lane.
paths:
  /healthz:
    get:
      operationId: getHealth
      summary: Report process liveness
      responses:
        "204":
          description: The Maiden Lane process is alive.
  /readyz:
    get:
      operationId: getReadiness
      summary: Report process readiness
      responses:
        "204":
          description: The Maiden Lane process can accept requests.
```

- [ ] **Step 5: Run tests and verify GREEN**

Run:

```bash
gofmt -w internal/httpapi/router.go internal/httpapi/router_test.go
go test ./internal/httpapi
go mod tidy
```

Expected: all `internal/httpapi` tests pass and module files are tidy.

- [ ] **Step 6: Commit the HTTP boundary**

```bash
git add api/openapi.yaml internal/httpapi go.mod go.sum
git commit -m "feat: add health HTTP boundary"
```

---

### Task 3: Implement the command and graceful server lifecycle

**Files:**
- Create: `cmd/maiden-lane/main_test.go`
- Create: `cmd/maiden-lane/main.go`

**Interfaces:**
- Produces: binary command `maiden-lane serve --listen-address=<address>`.
- Consumes: `httpapi.NewRouter()`.
- Produces: graceful shutdown on context cancellation and process signals.

- [ ] **Step 1: Write failing command and lifecycle tests**

Create `cmd/maiden-lane/main_test.go`:

```go
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunRequiresCommand(t *testing.T) {
	t.Parallel()

	var stderr strings.Builder
	err := run(context.Background(), nil, &stderr, testLogger(), nil)
	if err == nil {
		t.Fatal("run error = nil, want a command error")
	}
	if !strings.Contains(stderr.String(), "maiden-lane serve") {
		t.Fatalf("usage = %q, want serve command", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"unknown"}, io.Discard, testLogger(), nil)
	if err == nil {
		t.Fatal("run error = nil, want unknown-command error")
	}
}

func TestRunPassesExplicitListenAddress(t *testing.T) {
	t.Parallel()

	var gotAddress string
	serve := func(_ context.Context, address string, _ *slog.Logger) error {
		gotAddress = address
		return nil
	}

	err := run(
		context.Background(),
		[]string{"serve", "--listen-address=127.0.0.1:9090"},
		io.Discard,
		testLogger(),
		serve,
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotAddress != "127.0.0.1:9090" {
		t.Fatalf("listen address = %q, want %q", gotAddress, "127.0.0.1:9090")
	}
}

func TestRunRejectsUnexpectedServeArguments(t *testing.T) {
	t.Parallel()

	err := run(
		context.Background(),
		[]string{"serve", "unexpected"},
		io.Discard,
		testLogger(),
		func(context.Context, string, *slog.Logger) error { return nil },
	)
	if err == nil {
		t.Fatal("run error = nil, want unexpected-argument error")
	}
}

func TestServeListenerStopsAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(ctx, listener, testLogger())
	}()

	url := "http://" + listener.Addr().String() + "/healthz"
	waitForStatus(t, url, http.StatusNoContent)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func TestServePreservesListenFailure(t *testing.T) {
	t.Parallel()

	err := serve(context.Background(), "invalid address", testLogger())
	if err == nil {
		t.Fatal("serve error = nil, want listen failure")
	}

	var operationError *net.OpError
	if !errors.As(err, &operationError) {
		t.Fatalf("serve error %T does not preserve *net.OpError", err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()

	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s did not return status %d before deadline", url, want)
}
```

The polling helper waits for an observable condition instead of sleeping for an assumed startup duration.

- [ ] **Step 2: Run the command tests and verify RED**

Run:

```bash
go test ./cmd/maiden-lane
```

Expected: compilation fails because `run`, `serve`, and `serveListener` do not exist.

- [ ] **Step 3: Implement the command and documented server lifecycle**

Create `cmd/maiden-lane/main.go`:

```go
// Command maiden-lane is the process entry point for Maiden Lane.
//
// This package owns process composition, operational command-line parsing, and
// lifecycle signals. Transformation semantics belong in inward domain packages
// and must never be introduced here.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/httpapi"
)

const (
	defaultListenAddress = ":8080"
	readHeaderTimeout    = 5 * time.Second
	idleTimeout          = 60 * time.Second
	shutdownTimeout      = 10 * time.Second
)

type serveCommand func(context.Context, string, *slog.Logger) error

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stderr, logger, serve); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	stderr io.Writer,
	logger *slog.Logger,
	serveCommand serveCommand,
) error {
	if len(args) == 0 {
		writeUsage(stderr)
		return errors.New("command is required")
	}

	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		listenAddress := flags.String(
			"listen-address",
			defaultListenAddress,
			"TCP address on which the HTTP server listens",
		)
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("parse serve flags: %w", err)
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected serve arguments: %q", flags.Args())
		}
		return serveCommand(ctx, *listenAddress, logger)
	default:
		writeUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func writeUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, "usage: maiden-lane serve [--listen-address=:8080]")
}

func serve(ctx context.Context, address string, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", address, err)
	}
	return serveListener(ctx, listener, logger)
}

func serveListener(ctx context.Context, listener net.Listener, logger *slog.Logger) error {
	server := &http.Server{
		Handler:           httpapi.NewRouter(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	logger.Info("HTTP server started", "address", listener.Addr().String())

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		// Shutdown uses a fresh context because the process context is already
		// canceled. Reusing ctx would turn graceful shutdown into an immediate
		// abort, which is a subtle difference for readers coming from frameworks
		// that create a separate shutdown deadline automatically.
	}

	logger.Info("HTTP server stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP while shutting down: %w", err)
	}

	logger.Info("HTTP server stopped")
	return nil
}
```

- [ ] **Step 4: Run tests and verify GREEN**

Run:

```bash
gofmt -w cmd/maiden-lane/main.go cmd/maiden-lane/main_test.go
go test ./cmd/maiden-lane ./internal/httpapi
go test -race ./cmd/maiden-lane ./internal/httpapi
go build -trimpath -o /tmp/maiden-lane ./cmd/maiden-lane
```

Expected: all tests pass without races and the binary builds.

- [ ] **Step 5: Manually verify the process boundary**

Run the server in one terminal:

```bash
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

From another terminal:

```bash
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
```

Expected: both responses are `204 No Content`. Send `SIGINT`; the process logs its bounded shutdown and exits successfully.

- [ ] **Step 6: Commit the runnable application shell**

```bash
git add cmd/maiden-lane
git commit -m "feat: add Maiden Lane serve command"
```

---

### Task 4: Add local verification and the non-root container

**Files:**
- Create: `Makefile`
- Create: `Dockerfile`
- Create: `.dockerignore`

**Interfaces:**
- Produces: `make verify`, `make container-build`, `make container-smoke`, and `make container-check`.
- Produces: OCI image whose entry point is `/maiden-lane` and default command is `serve --listen-address=:8080`.

- [ ] **Step 1: Verify the repository does not yet provide the agreed developer interface**

Run:

```bash
make verify
```

Expected: failure because `Makefile` does not exist or has no `verify` target.

- [ ] **Step 2: Add explicit local verification targets**

Create `Makefile`:

```make
SHELL := /bin/sh

GO ?= go
BINARY_DIR ?= bin
BINARY ?= $(BINARY_DIR)/maiden-lane
IMAGE ?= maiden-lane:local

.PHONY: help fmt fmt-check mod-check tool-versions vet staticcheck test test-race vulncheck build verify container-build container-smoke container-check

help:
	@echo "fmt              format Go source"
	@echo "fmt-check        fail if Go source is not formatted"
	@echo "mod-check        fail if go.mod or go.sum is not tidy"
	@echo "tool-versions    verify pinned analysis tool versions"
	@echo "vet              run go vet"
	@echo "staticcheck      run the pinned Staticcheck"
	@echo "test             run unit tests"
	@echo "test-race        run unit tests with the race detector"
	@echo "vulncheck        run the pinned govulncheck"
	@echo "build            build $(BINARY)"
	@echo "verify           run the complete local CI sequence"
	@echo "container-check  build and smoke-test $(IMAGE)"

fmt:
	$(GO) fmt ./...

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "Go files need formatting:"; \
		echo "$$files"; \
		exit 1; \
	fi

mod-check:
	$(GO) mod tidy -diff

tool-versions:
	@actual="$$( $(GO) tool staticcheck -version )"; \
	test "$$actual" = "staticcheck 2026.2rc1 (v0.8.0-rc.1)" || { \
		echo "unexpected Staticcheck version: $$actual"; \
		exit 1; \
	}
	@$(GO) tool govulncheck -version | grep -F "Scanner: govulncheck@v1.6.0" >/dev/null || { \
		echo "govulncheck is not pinned to v1.6.0"; \
		exit 1; \
	}

vet:
	$(GO) vet ./...

staticcheck:
	$(GO) tool staticcheck ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vulncheck:
	$(GO) tool govulncheck ./...

build:
	mkdir -p $(BINARY_DIR)
	$(GO) build -trimpath -o $(BINARY) ./cmd/maiden-lane

verify: fmt-check mod-check tool-versions vet staticcheck test test-race vulncheck build

container-build:
	docker build --tag $(IMAGE) .

container-smoke:
	@set -eu; \
	container_name="maiden-lane-smoke-$$$$"; \
	container_id="$$(docker run --detach --name "$$container_name" --publish 127.0.0.1::8080 $(IMAGE))"; \
	trap 'docker rm --force "$$container_id" >/dev/null 2>&1 || true' EXIT INT TERM; \
	port="$$(docker port "$$container_id" 8080/tcp | sed -n 's/.*://p')"; \
	status=""; \
	attempt=0; \
	while [ "$$attempt" -lt 40 ]; do \
		status="$$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$$port/healthz" || true)"; \
		[ "$$status" = "204" ] && break; \
		attempt=$$((attempt + 1)); \
		sleep 0.25; \
	done; \
	test "$$status" = "204" || { \
		echo "container health status = $$status, want 204"; \
		docker logs "$$container_id"; \
		exit 1; \
	}; \
	user="$$(docker inspect --format '{{.Config.User}}' "$$container_id")"; \
	test "$$user" = "65532:65532" || { \
		echo "container user = $$user, want 65532:65532"; \
		exit 1; \
	}

container-check: container-build container-smoke
```

- [ ] **Step 3: Add a pinned multi-stage container**

Create `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /out/maiden-lane ./cmd/maiden-lane

FROM scratch

# The current server has no outbound calls, but copying the standard CA bundle
# keeps the minimal runtime ready for future TLS clients without adding a shell
# or package manager.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/maiden-lane /maiden-lane

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/maiden-lane"]
CMD ["serve", "--listen-address=:8080"]
```

Create `.dockerignore`:

```dockerignore
.git
.github
bin
coverage.out
docs
*.md
.env
.env.*
```

- [ ] **Step 4: Run the complete local and container checks**

Run:

```bash
make verify
make container-check
```

Expected: all Go checks pass; the image returns health status 204 and reports container user `65532:65532`.

- [ ] **Step 5: Commit local build and container support**

```bash
git add Makefile Dockerfile .dockerignore
git commit -m "build: add verification and container targets"
```

---

### Task 5: Write current-state documentation and strengthen contributor guidance

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Create: `docs/implementation/implementation-guide.md`
- Verify unchanged: `ERRORS.md`
- Verify unchanged: `METRICS.md`

**Interfaces:**
- Produces: user-facing setup and operations reference.
- Produces: maintainer-facing current repository map and Python-to-Go code tour.
- Produces: durable code-documentation expectations in `AGENTS.md`.

- [ ] **Step 1: Replace the blank README with the actual developer interface**

Write `README.md`:

```markdown
# Maiden Lane

Maiden Lane is a deterministic transformation system for compiling, executing,
explaining, comparing, and gating mapper transformations. The repository
currently contains the runnable HTTP application shell plus local build and
container support; the transformation engine has not been implemented.

## Requirements

- Go 1.26 or newer; the repository currently selects Go 1.26.5.
- Docker for container checks.
- `make` for the documented convenience targets. Every target prints or wraps
  ordinary Go and Docker commands.

Staticcheck 2026.2rc1 and govulncheck v1.6.0 are tracked in `go.mod` and run
through `go tool`; workstation-global installations are not used.

## Quick start

```bash
make verify
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

The current HTTP surface is:

- `GET /healthz` — process liveness, returning `204 No Content`.
- `GET /readyz` — process readiness, returning `204 No Content`.

## Common commands

```bash
make help
make fmt
make verify
make container-check
```

The complete verification sequence is:

```bash
gofmt -l .
go mod tidy -diff
go vet ./...
go tool staticcheck ./...
go test ./...
go test -race ./...
go tool govulncheck ./...
go build -trimpath ./cmd/maiden-lane
```

## CI/CD

No remote pipeline exists at this revision. `make verify` and
`make container-check` are the local release prerequisites.

## Design references

- [Inviolates](Inviolates.md)
- [High-Level Design](docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)
- [Current Implementation Guide](docs/implementation/implementation-guide.md)
- [Glossary](GLOSSARY.md)
- [Error Catalog](ERRORS.md)
- [Metrics Catalog](METRICS.md)
```

- [ ] **Step 2: Create the living implementation guide as a current-state map**

Create `docs/implementation/implementation-guide.md`:

```markdown
# Maiden Lane Implementation Guide

**Status:** Living, non-normative description of the current repository

This guide describes only what exists at this revision. Rewrite it when the
implementation changes; do not retain historical package layouts here. Git is
the history. The HLD and any ratified Inviolates outrank this guide.

## Current capabilities

- One Go module and one `maiden-lane` binary.
- A `serve` command with explicit listen configuration and graceful shutdown.
- A chi router exposing `/healthz` and `/readyz`.
- Tracked Staticcheck and govulncheck tools.
- Local verification and a non-root container build.

There is no transformation model, compiler, executor, worker, persistence
adapter, promotion gate, exported metric, or stable typed application error.

## Current repository map

```text
api/openapi.yaml                 current health wire contract
cmd/maiden-lane/main.go          CLI, process composition, server lifecycle
internal/httpapi/router.go       HTTP transport routes and handlers
Dockerfile                       non-root application image
Makefile                         explicit local verification commands
```

Only implemented packages appear in this map.

## Runtime flow

1. `main` creates a structured logger and signal-aware process context.
2. `run` parses the explicit `serve` command and listen-address flag.
3. `serve` binds the requested TCP address.
4. `serveListener` starts `http.Server` with the chi router.
5. Cancellation creates a separate bounded shutdown context and drains active
   requests before returning.

## Go orientation for Python-oriented contributors

- `cmd/maiden-lane` is a `package main`; building it creates the executable.
- `internal/httpapi` can be imported only from within this module, which keeps
  transport details from becoming a public library contract.
- `context.Context` carries cancellation across call boundaries. It is passed
  explicitly rather than discovered globally.
- `%w` preserves an error's causal value so callers can use `errors.Is` and
  `errors.As`; do not inspect error-message strings for behavior.
- Goroutines do not propagate errors automatically. The server lifecycle uses a
  buffered channel so `http.Server.Serve` always has somewhere to report its
  terminal result while the main goroutine coordinates shutdown.
- `httptest` exercises an `http.Handler` without binding a real port. The
  lifecycle test uses a real loopback listener because cancellation behavior is
  the property under test.
- Repository tools are declared in `go.mod` and invoked as `go tool <name>`.

## Verification

```bash
make verify
make container-check
```

Run individual commands from `Makefile` directly when diagnosing a failure.

## Known gaps

The semantic transformation system and AWS runtime adapters described by the
HLD are not implemented. Their eventual package boundaries will be documented
here only after the corresponding code exists.
```

- [ ] **Step 3: Point AGENTS.md at the living guide and add the Python-first documentation convention**

In `AGENTS.md` section 1, replace the conditional implementation-guide entry with:

```markdown
3. Read the current [**Implementation Guide**](docs/implementation/implementation-guide.md).
```

In section 10 after the existing “Comment why” guidance, add:

```markdown
Assume many Maiden Lane contributors and reviewers are more familiar with
Python than Go. Use slightly more implementation documentation than a typical
Go repository when it materially reduces that gap:

* give every nontrivial package a package-level explanation of its ownership
  and dependency boundary;
* document exported declarations normally;
* explain non-obvious determinism, ordering, canonicalization, lifecycle,
  concurrency, retry, and fail-closed decisions near the code that implements
  them;
* explain Go idioms whose safety consequence may not be apparent to a
  Python-oriented reader.

Comments explain implementation. They do not override the Inviolates, HLD,
contracts, or authoritative tests, and they should not narrate obvious syntax.
```

- [ ] **Step 4: Verify documentation describes only current code**

Run:

```bash
rg -n 'internal/(model|rules|compile|execute|invariant|provenance|promotion|ports|adapters)' docs/implementation/implementation-guide.md
test "$(rg -c '^\| Error type \|' ERRORS.md)" -eq 1
test "$(rg -c '^\| Name \|' METRICS.md)" -eq 1
test "$(rg -c '^\|[^-].*\|$' ERRORS.md)" -eq 1
test "$(rg -c '^\|[^-].*\|$' METRICS.md)" -eq 1
git diff --check
```

Expected:

- The first command has no matches.
- Each registry still contains only its header row and no invented entry.
- The diff check is clean.

- [ ] **Step 5: Commit current-state documentation**

```bash
git add README.md AGENTS.md docs/implementation/implementation-guide.md
git commit -m "docs: describe current application scaffold"
```

---

### Task 6: Add GitHub Actions CI and ECR publication

**Files:**
- Create: `.github/workflows/pipeline.yml`
- Create: `.github/dependabot.yml`
- Modify: `README.md`
- Modify: `docs/implementation/implementation-guide.md`

**Interfaces:**
- Consumes repository variables: `AWS_REGION`, `AWS_ROLE_ARN`, `ECR_REPOSITORY`.
- Requires the named ECR repository to enforce immutable tags.
- Produces verification results for pull requests, `main`, and `v*` tags.
- Produces immutable ECR tag `sha-<full-commit>` and an additional `v*` tag for version refs.

- [ ] **Step 1: Record the exact action revisions before writing the workflow**

Use these verified action pins:

```text
actions/checkout v7.0.1
  3d3c42e5aac5ba805825da76410c181273ba90b1
actions/setup-go v7.0.0
  b7ad1dad31e06c5925ef5d2fc7ad053ef454303e
aws-actions/configure-aws-credentials v6.2.3
  e6de054238d6b7531b4efff3b6587d9aade6a06c
aws-actions/amazon-ecr-login v2.1.6
  d539f0932e70871a027e9d5a9d8fc38589180a64
```

- [ ] **Step 2: Create the verification, container, and publication workflow**

Create `.github/workflows/pipeline.yml`:

```yaml
name: pipeline

on:
  pull_request:
  push:
    branches:
      - main
    tags:
      - "v*"

concurrency:
  group: pipeline-${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  verify:
    name: Verify Go application
    runs-on: ubuntu-24.04
    timeout-minutes: 20
    steps:
      - name: Check out repository
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: "1.26.5"
          cache: true

      - name: Download dependencies
        run: go mod download

      - name: Verify application
        run: make verify

  container:
    name: Verify pull-request image
    if: github.event_name == 'pull_request'
    needs: verify
    runs-on: ubuntu-24.04
    timeout-minutes: 15
    steps:
      - name: Check out repository
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - name: Build and smoke-test image
        run: make container-check IMAGE=maiden-lane:ci-${{ github.sha }}

  publish:
    name: Publish tested image to ECR
    if: github.event_name == 'push'
    needs: verify
    runs-on: ubuntu-24.04
    timeout-minutes: 20
    concurrency:
      group: ecr-${{ github.sha }}
      cancel-in-progress: false
    permissions:
      contents: read
      id-token: write
    env:
      AWS_REGION: ${{ vars.AWS_REGION }}
      AWS_ROLE_ARN: ${{ vars.AWS_ROLE_ARN }}
      ECR_REPOSITORY: ${{ vars.ECR_REPOSITORY }}
      LOCAL_IMAGE: maiden-lane:ci-${{ github.sha }}
      SHA_TAG: sha-${{ github.sha }}
    steps:
      - name: Check out repository
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - name: Validate release configuration
        shell: bash
        run: |
          set -euo pipefail
          for variable in AWS_REGION AWS_ROLE_ARN ECR_REPOSITORY; do
            if [[ -z "${!variable}" ]]; then
              echo "missing required repository variable: ${variable}" >&2
              exit 1
            fi
          done

      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@e6de054238d6b7531b4efff3b6587d9aade6a06c # v6.2.3
        with:
          role-to-assume: ${{ env.AWS_ROLE_ARN }}
          aws-region: ${{ env.AWS_REGION }}

      - name: Log in to Amazon ECR
        id: ecr
        uses: aws-actions/amazon-ecr-login@d539f0932e70871a027e9d5a9d8fc38589180a64 # v2.1.6

      - name: Verify immutable ECR repository
        shell: bash
        run: |
          set -euo pipefail
          mutability="$(
            aws ecr describe-repositories \
              --repository-names "$ECR_REPOSITORY" \
              --query 'repositories[0].imageTagMutability' \
              --output text
          )"
          if [[ "$mutability" != "IMMUTABLE" ]]; then
            echo "ECR repository must enforce immutable tags; got $mutability" >&2
            exit 1
          fi

      - name: Locate immutable commit image
        id: existing
        shell: bash
        env:
          ECR_REGISTRY: ${{ steps.ecr.outputs.registry }}
        run: |
          set -euo pipefail
          if aws ecr describe-images \
            --repository-name "$ECR_REPOSITORY" \
            --image-ids imageTag="$SHA_TAG" >/dev/null 2>&1; then
            echo "exists=true" >> "$GITHUB_OUTPUT"
            docker pull "$ECR_REGISTRY/$ECR_REPOSITORY:$SHA_TAG"
            docker tag "$ECR_REGISTRY/$ECR_REPOSITORY:$SHA_TAG" "$LOCAL_IMAGE"
          else
            echo "exists=false" >> "$GITHUB_OUTPUT"
            make container-build IMAGE="$LOCAL_IMAGE"
          fi

      - name: Smoke-test exact release image
        run: make container-smoke IMAGE="$LOCAL_IMAGE"

      - name: Publish immutable commit tag
        if: steps.existing.outputs.exists != 'true'
        shell: bash
        env:
          ECR_REGISTRY: ${{ steps.ecr.outputs.registry }}
        run: |
          set -euo pipefail
          remote="$ECR_REGISTRY/$ECR_REPOSITORY:$SHA_TAG"
          docker tag "$LOCAL_IMAGE" "$remote"
          docker push "$remote"

      - name: Apply immutable version tag
        if: startsWith(github.ref, 'refs/tags/v')
        shell: bash
        env:
          VERSION_TAG: ${{ github.ref_name }}
        run: |
          set -euo pipefail
          sha_digest="$(
            aws ecr describe-images \
              --repository-name "$ECR_REPOSITORY" \
              --image-ids imageTag="$SHA_TAG" \
              --query 'imageDetails[0].imageDigest' \
              --output text
          )"

          version_digest="$(
            aws ecr describe-images \
              --repository-name "$ECR_REPOSITORY" \
              --image-ids imageTag="$VERSION_TAG" \
              --query 'imageDetails[0].imageDigest' \
              --output text 2>/dev/null || true
          )"
          if [[ -n "$version_digest" && "$version_digest" != "None" ]]; then
            if [[ "$version_digest" != "$sha_digest" ]]; then
              echo "version tag $VERSION_TAG already points at a different digest" >&2
              exit 1
            fi
            exit 0
          fi

          manifest="$(
            aws ecr batch-get-image \
              --repository-name "$ECR_REPOSITORY" \
              --image-ids imageTag="$SHA_TAG" \
              --query 'images[0].imageManifest' \
              --output text
          )"
          aws ecr put-image \
            --repository-name "$ECR_REPOSITORY" \
            --image-tag "$VERSION_TAG" \
            --image-manifest "$manifest" >/dev/null

      - name: Report image digest
        shell: bash
        env:
          ECR_REGISTRY: ${{ steps.ecr.outputs.registry }}
        run: |
          set -euo pipefail
          digest="$(
            aws ecr describe-images \
              --repository-name "$ECR_REPOSITORY" \
              --image-ids imageTag="$SHA_TAG" \
              --query 'imageDetails[0].imageDigest' \
              --output text
          )"
          {
            echo "### Published image"
            echo
            echo "\`$ECR_REGISTRY/$ECR_REPOSITORY@$digest\`"
          } >> "$GITHUB_STEP_SUMMARY"
```

The existing-tag branch makes reruns and later `v*` tagging idempotent under ECR tag immutability: an existing commit image is pulled and smoke-tested rather than rebuilt under an immutable tag.

- [ ] **Step 3: Add bounded dependency-update configuration**

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: "/"
    schedule:
      interval: weekly
    groups:
      go-dependencies:
        patterns:
          - "*"

  - package-ecosystem: github-actions
    directory: "/"
    schedule:
      interval: weekly
    groups:
      github-actions:
        patterns:
          - "*"

  - package-ecosystem: docker
    directory: "/"
    schedule:
      interval: weekly
```

- [ ] **Step 4: Update current-state documentation in the same change**

In `README.md`, replace the temporary `CI/CD` section with:

```markdown
## CI/CD

Pull requests run the complete Go verification sequence and a container health
smoke test. Pushes to `main` and `v*` tags publish the tested image to Amazon
ECR using GitHub OIDC. Publication requires the repository variables
`AWS_REGION`, `AWS_ROLE_ARN`, and `ECR_REPOSITORY`.

CD currently stops at immutable ECR publication. It does not deploy ECS or AWS
Batch resources.
```

In `docs/implementation/implementation-guide.md`, add this item to
`Current capabilities`:

```markdown
- GitHub Actions verification and ECR image publication.
```

Add the workflow to `Current repository map`:

```text
.github/workflows/pipeline.yml   CI and ECR publication
```

The guide must not describe the workflow in the earlier documentation commit;
it becomes current only in this CI/CD commit.

- [ ] **Step 5: Validate the workflow structure and release boundaries locally**

Run:

```bash
ruby -e 'require "yaml"; ARGV.each { |file| YAML.load_file(file) }' .github/workflows/pipeline.yml .github/dependabot.yml
rg -n 'latest|main:' .github/workflows/pipeline.yml
rg -n 'ecs|batch|deploy' .github/workflows/pipeline.yml
git diff --check
```

Expected:

- Both YAML files parse.
- No floating image tag is produced. The `main` branch trigger may match the second command and is allowed.
- No ECS, Batch, or application deployment command appears.
- The diff check is clean.

GitHub-hosted execution and AWS OIDC/ECR publication cannot be completed until the repository has a remote and the three repository variables are configured; state that limitation in the final verification report rather than claiming the remote workflow ran.

- [ ] **Step 6: Commit CI/CD configuration and its current-state documentation**

```bash
git add .github/workflows/pipeline.yml .github/dependabot.yml README.md docs/implementation/implementation-guide.md
git commit -m "ci: add verification and ECR publication"
```

---

### Task 7: Run the complete acceptance suite and reconcile current-state docs

**Files:**
- Modify if needed: `docs/implementation/implementation-guide.md`
- Modify if needed: `README.md`
- Verify: all files changed by Tasks 1–6

**Interfaces:**
- Consumes: complete repository scaffold.
- Produces: evidence that the scaffold satisfies the design without speculative packages or stale documentation.

- [ ] **Step 1: Run formatting and module regeneration**

Run:

```bash
make fmt
go mod tidy
git diff --check
```

Inspect any generated module diff. It must be explainable by the imports and tracked tools already introduced.

- [ ] **Step 2: Run the full Go verification suite from a clean command path**

Run:

```bash
make verify
```

Expected:

- tool-version checks pass;
- vet and Staticcheck pass;
- unit and race tests pass;
- govulncheck reports no reachable known vulnerability;
- the binary builds at `bin/maiden-lane`.

- [ ] **Step 3: Run container verification**

Run:

```bash
make container-check IMAGE=maiden-lane:acceptance
docker image inspect maiden-lane:acceptance --format '{{.Config.User}} {{json .Config.Entrypoint}} {{json .Config.Cmd}}'
```

Expected:

```text
65532:65532 ["/maiden-lane"] ["serve","--listen-address=:8080"]
```

- [ ] **Step 4: Audit architectural and documentation boundaries**

Run:

```bash
find internal -mindepth 1 -maxdepth 1 -type d -print | sort
rg -n 'internal/(model|rules|compile|execute|invariant|provenance|promotion|ports|adapters)' docs/implementation/implementation-guide.md
test "$(rg -c '^\| Error type \|' ERRORS.md)" -eq 1
test "$(rg -c '^\| Name \|' METRICS.md)" -eq 1
test "$(rg -c '^\|[^-].*\|$' ERRORS.md)" -eq 1
test "$(rg -c '^\|[^-].*\|$' METRICS.md)" -eq 1
git status --short
git diff --stat main...
git diff main... -- AGENTS.md README.md docs/implementation/implementation-guide.md
```

Expected:

- `internal/httpapi` is the only internal package directory.
- The implementation guide has no speculative package tree.
- Error and metric registries have no entries.
- The diff contains only the approved scaffold and documentation.

- [ ] **Step 5: Re-read the HLD and Inviolates against the final diff**

Confirm explicitly:

- no business meaning entered HTTP, command, container, or CI layers;
- no ambient configuration can affect semantic results;
- no execution path publishes Maiden Lane data;
- no infrastructure adapter or stochflow dependency exists;
- comments explain lifecycle and boundaries without claiming semantic authority;
- ECR publication is an operational image publication, not a Maiden Lane candidate publication.

- [ ] **Step 6: Commit any final generated or documentation reconciliation**

If Task 7 produced an explainable change:

```bash
git add go.mod go.sum README.md docs/implementation/implementation-guide.md
git commit -m "build: reconcile scaffold verification"
```

If there is no diff, do not create an empty commit.

- [ ] **Step 7: Perform branch-completion review**

Invoke `superpowers:requesting-code-review`, address any verified findings, run `make verify` and `make container-check` again, then invoke `superpowers:verification-before-completion` and `superpowers:finishing-a-development-branch` before offering integration.
