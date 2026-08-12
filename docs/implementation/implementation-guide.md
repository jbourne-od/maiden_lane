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
