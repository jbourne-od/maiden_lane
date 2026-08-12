# Maiden Lane Implementation Guide

**Status:** Living, non-normative description of the current repository

This guide describes only what exists at this revision. Rewrite it when the
implementation changes; do not retain historical package layouts here. Git is
the history. The ratified Inviolates and then the HLD outrank this guide.

## Current capabilities

- One Go module and one `maiden-lane` binary.
- A `serve` command with explicit listen configuration and graceful shutdown.
- A chi router exposing `/healthz` and `/readyz`.
- Tracked Staticcheck and govulncheck tools.
- Local verification and a non-root container build.
- GitHub Actions verification and ECR image publication.

There is no transformation model, compiler, executor, sealed-checkpoint model,
completeness profile, readiness assessment, worker, persistence adapter,
promotion gate, exported metric, or stable typed application error.

## Current repository map

```text
api/openapi.yaml                 current health wire contract
cmd/maiden-lane/main.go          CLI, process composition, server lifecycle
internal/httpapi/router.go       HTTP transport routes and handlers
Dockerfile                       non-root application image
Makefile                         explicit local verification commands
.github/workflows/pipeline.yml   CI and ECR publication
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
HLD are not implemented. Their eventual package boundaries will be documented
here only after the corresponding code exists.
