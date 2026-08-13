# Maiden Lane Implementation Guide

**Status:** Living, non-normative description of the current repository

This guide describes only what exists at this revision. Rewrite it when the
implementation changes; do not retain historical package layouts here. Git is
the history. The ratified Inviolates and then the HLD outrank this guide.

## Current capabilities

- One Go module and one `maiden-lane` binary.
- A `serve` command with explicit listen configuration and graceful shutdown.
- A chi router exposing `/healthz` and `/readyz`.
- Structured JSON application logging through `log/slog`.
- Explicit OpenTelemetry trace and metric providers with disabled or
  OTLP-over-HTTP/protobuf operation.
- Privacy-safe registered-route HTTP instrumentation and bounded telemetry
  flush/shutdown after the HTTP server drains.
- Tracked Staticcheck and govulncheck tools.
- Local verification and a non-root container build.
- GitHub Actions verification and ECR image publication.

There is no transformation model, compiler, executor, sealed-checkpoint model,
completeness profile, readiness assessment, worker, persistence adapter,
promotion gate, semantic telemetry, or stable typed application error.

## Current repository map

```text
api/openapi.yaml                 current health wire contract
cmd/maiden-lane/main.go          CLI, process composition, server lifecycle
internal/httpapi/router.go       HTTP transport routes and handlers
internal/observability/          operational config, slog, OTel runtime and HTTP instrumentation
Dockerfile                       non-root application image
Makefile                         explicit local verification commands
.github/workflows/pipeline.yml   CI and ECR publication
```

Only implemented packages appear in this map.

## Runtime flow

1. `processMain` creates a signal-aware process context and defers its cleanup
   before returning an exit code to `main`.
2. `execute` loads and validates operational observability configuration.
3. `internal/observability` constructs the JSON `slog` logger, explicit W3C
   trace-context propagator, and either no-op providers or OTLP/HTTP trace and
   metric providers. Unsupported upstream `OTEL_GO_X_*` policy is rejected at
   configuration and provider boundaries; an export pipeline is permanently
   closed if such policy appears after startup.
4. `run` parses the explicit `serve` command and listen-address flag.
5. `serve` binds the requested TCP address, and `serveListener` runs
   `http.Server` with the health-only chi router and a sanitized error logger.
6. Cancellation or an unexpected serving failure enters the same drain path.
   The server allows active handlers up to ten seconds to finish, then
   force-closes on timeout while preserving the drain and close errors.
7. `execute` creates a fresh ten-second context, flushes and shuts down OTel,
   then returns the joined command and telemetry result for the final exit
   decision.

The observability runtime owns a registered-route wrapper for future
non-health handlers. It accepts trace context, removes baggage, emits only
trusted route templates and closed attributes, explicitly marks every ended
span `OK` or `Error`, and records the three metrics in `METRICS.md`. The current
health and readiness handlers are deliberately not wrapped, so the instruments
are registered but no production HTTP points are currently recorded.

Standard OTel HTTP server middleware is not used because it captures raw
request-derived attributes before Maiden Lane can enforce its privacy and
cardinality boundary.

## Go orientation for Python-oriented contributors

- `cmd/maiden-lane` is a `package main`; building it creates the executable.
- `internal/httpapi` can be imported only from within this module, which keeps
  transport details from becoming a public library contract.
- `internal/observability` is also an infrastructure-only package. Semantic
  packages must not import it or make decisions from telemetry state.
- `context.Context` carries cancellation across call boundaries. It is passed
  explicitly rather than discovered globally. Here it carries cancellation and
  trace context, never Maiden Lane transformation semantics.
- `%w` preserves an error's causal value so callers can use `errors.Is` and
  `errors.As`; do not inspect error-message strings for behavior.
- Goroutines do not propagate errors automatically. The server lifecycle uses a
  buffered channel so `http.Server.Serve` always has somewhere to report its
  terminal result while the main goroutine coordinates shutdown.
- `os.Exit` does not run deferred functions. `processMain` therefore returns an
  exit code only after signal cleanup, server drain, and telemetry shutdown;
  only the small `main` function calls `os.Exit`.
- `httptest` exercises an `http.Handler` without binding a real port. The
  lifecycle test uses a real loopback listener because cancellation behavior is
  the property under test.
- Repository tools are declared in `go.mod` and invoked as `go tool <name>`.

## Verification

```bash
make verify
make container-check
```

`make verify` is authoritative. For make-independent diagnosis, this is its
current stage sequence with the same tool arguments and build output path:

```sh
set -eu

# make fmt-check
gofmt_binary="$(go env GOROOT)/bin/gofmt"
unformatted="$(find . \
  -type d \( -name .git -o -name .worktrees -o -name .superpowers \) -prune \
  -o -type f -name '*.go' -exec "$gofmt_binary" -l {} +)"
if [ -n "$unformatted" ]; then
  echo "Go files need formatting:"
  echo "$unformatted"
  exit 1
fi

# make mod-check
go mod tidy -diff

# make tool-versions
staticcheck_version="$(go tool staticcheck -version)"
if [ "$staticcheck_version" != "staticcheck 2026.2rc1 (0.8.0-rc.1)" ]; then
  echo "unexpected Staticcheck version: $staticcheck_version"
  exit 1
fi
go tool govulncheck -version | grep -F "Scanner: govulncheck@v1.6.0" >/dev/null || {
  echo "govulncheck is not pinned to v1.6.0"
  exit 1
}

go vet ./...                       # make vet
go tool staticcheck ./...          # make staticcheck
go test ./...                      # make test
go test -race ./...                # make test-race
go tool govulncheck ./...          # make vulncheck
mkdir -p bin                       # make build
go build -trimpath -o bin/maiden-lane ./cmd/maiden-lane
```

Make additionally owns the exact stage ordering and the human-readable failure
messages around formatting and tool-version assertions. The current recipes
require POSIX `/bin/sh`, `find`, `grep`, `sed`, `mkdir`, `sleep`, and `curl`,
plus Docker for the container targets.

## Known gaps

The semantic transformation system and AWS runtime adapters described by the
HLD are not implemented. There are no application-operation spans, worker or
adapter spans, OTel log export, collector deployment, or wrapped non-health
production routes. Their eventual package boundaries will be documented here
only after the corresponding code exists.
