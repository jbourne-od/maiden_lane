# Maiden Lane

![Maiden Lane deterministic transformation engine](docs/images/cover_image.png)

Maiden Lane is a deterministic transformation system for compiling, executing,
explaining, comparing, and gating mapper transformations.

The repository contains the runnable HTTP application shell, local build and
container support, and an internal pure in-memory walking skeleton of the
semantic engine: a standard-library-only kernel that compiles a closed rule
declaration into an immutable plan, executes it through atomic structural
patches, records an accepted-only journal, seals content-addressed checkpoints,
and assesses consumer readiness over them. Identical semantic inputs produce
identical artifact identities, and a failed protected invariant leaves the last
verified checkpoint byte-identical.

That engine is now reachable over HTTP. A tenant-scoped `/v1` surface compiles
declarations into a plan and executes it, with `api/openapi.yaml` as the
authoritative contract that Go server code and clients are generated from.

Compiled plans can be stored durably in PostgreSQL. There is still **no
promotion, publication, worker mode, or production hours-of-service policy**;
executions themselves are not yet persisted, the sanitized team-HOS fixture is a
walking-skeleton fixture rather than a real rule, and execution is synchronous
rather than the asynchronous shape the High-Level Design specifies.
Authentication is delegated to a deployment gateway; this process enforces
tenant scoping but verifies no credentials.

### Running it

```bash
# Everything in one process: an API and an in-process worker.
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080

# Or split, which is the shape the High-Level Design targets.
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080 --no-worker
go run ./cmd/maiden-lane work
```

The API and the worker are two modes of one binary. `serve` runs a worker in
process by default, and it must: with in-memory storage a separate worker
process cannot see the queue, so an enqueued execution would never run and every
read would report `pending` forever. Splitting them requires durable storage,
which is what makes the queue visible to both.

### Storage

| Variable | Meaning |
|---|---|
| `MAIDEN_LANE_DATABASE_URL` | PostgreSQL connection URL. When unset, plans and executions are held in process memory. |

Absent configuration keeps everything in memory, so a local run needs no
database. The process says so at startup, because an operator who expected
durability should learn it then rather than after a restart.

A configured URL that cannot be reached **blocks startup**. Falling back to
memory would be the worst available outcome: nothing would look wrong until the
first restart, by which point the artifacts are already gone.

Stored plans are never trusted on the way back out. A read recompiles the stored
declarations and returns a plan only if the recompiled identity matches the one
stored beside it, so a corrupted, truncated, or substituted row fails closed
rather than answering under an identity it did not produce.

## Requirements

- Go 1.26 or newer; the repository currently selects Go 1.26.6.
- POSIX `/bin/sh`, `make`, and ordinary userland tools used by the recipes:
  `find`, `grep`, `sed`, `mkdir`, `sleep`, and `curl`.
- Docker for container checks; `curl` sends the health request from the host.

Staticcheck 2026.2rc1 and govulncheck v1.6.0 are tracked in `go.mod` and run
through `go tool`; workstation-global installations are not used.

## Quick start

```bash
make verify
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

The current HTTP surface is:

| Operation | Meaning |
|---|---|
| `GET /healthz` | Process liveness, returning `204 No Content`. |
| `GET /readyz` | Process readiness, returning `204 No Content`. |
| `POST /v1/plans` | Compile declarations into an immutable plan. |
| `GET /v1/plans/{planID}` | Retrieve a plan, including the declarations the compiler accepted. |
| `POST /v1/executions` | Queue an execution of a plan over pinned inputs. Returns `202` with its identities. |
| `GET /v1/executions/{executionID}` | Report an execution's status and, once finished, its result. |

Every `/v1` operation requires the `X-Maiden-Lane-Tenant` header and is scoped
by it. An artifact belonging to another tenant is reported as `404`, never
`403`, so possession of an identifier reveals nothing about its existence.

### Reading a result

Two response conventions are worth knowing before writing a client.

**Executions are asynchronous.** Submission returns identities; a worker runs
the execution and the result appears at the read operation. There is no
synchronous variant, so there is one execution path and one lifecycle, and
worker availability never affects a submission's response.

**A deterministic semantic outcome is an answer, not an error.** A failed
protected invariant means the computation correctly refused to commit, so a
finished execution reports it through `result.failure` alongside every artifact
that verified beforehand. `failureReason` is different: it is a bounded
operational code meaning the execution could not be attempted at all. Only the
service's inability to reach an answer becomes an RFC 9457
`application/problem+json` document, and a readiness verdict of `needs_input` is
a successful assessment.

**Execution identity is derived, not allocated.** Repeating an identical
request reproduces the same `semanticRunID` and `executionID`; changing only
the executor identity preserves the semantic run, changes the execution, and
leaves sealed checkpoint digests untouched. Idempotency therefore needs no
request keys, no deduplication store, and no expiry policy.

One consequence is worth knowing before you rely on it: because identity is
derived, an execution that reaches a terminal failure cannot be cleared by
resubmitting, since the same request resolves to the same record. Retrying a
terminally failed execution needs an explicit operation, which does not exist
yet.

### Generating a client

`api/openapi.yaml` is authoritative. Server code is generated from it and never
the reverse, and `make openapi-check` fails the build if the two disagree, so a
client generated from the document matches what the service actually serves.

```bash
make openapi        # regenerate Go server and test client
make openapi-check  # fail if generated code has drifted
```

## Observability

Maiden Lane writes structured JSON application logs to standard output using
the Go standard library's `log/slog`. OpenTelemetry traces and metrics are
disabled by default, so local startup does not contact a collector:

```bash
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

Enable OTLP over HTTP/protobuf explicitly:

```bash
OTEL_TRACES_EXPORTER=otlp \
OTEL_METRICS_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.example \
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

Operational configuration does not participate in Maiden Lane semantic output
or identity. Supported variables are:

| Variable | Accepted values or meaning |
|---|---|
| `LOG_LEVEL` | `debug`, `info`, `warn`, or `error`; default `info` |
| `OTEL_TRACES_EXPORTER` | `none` or `otlp`; default `none` |
| `OTEL_METRICS_EXPORTER` | `none` or `otlp`; default `none` |
| `OTEL_SERVICE_NAME` | 1–128 UTF-8 bytes without control characters; default `maiden-lane` |

For each suffix below, the signal-specific
`OTEL_EXPORTER_OTLP_TRACES_<SUFFIX>` or
`OTEL_EXPORTER_OTLP_METRICS_<SUFFIX>` takes precedence over the global
`OTEL_EXPORTER_OTLP_<SUFFIX>`:

| Suffix | Accepted values or meaning |
|---|---|
| `ENDPOINT` | Absolute `http` or `https` URL without credentials, query, or fragment. Default `https://localhost:4318`; the global endpoint receives `/v1/traces` or `/v1/metrics`, while a signal endpoint is used as supplied. |
| `PROTOCOL` | `http/protobuf` only |
| `HEADERS` | Comma-separated `header=value` pairs with unique case-insensitive names; values may be percent encoded |
| `TIMEOUT` | Positive integer milliseconds; default `10000` |
| `COMPRESSION` | `none` or `gzip`; default `none` |
| `INSECURE` | Optional; when present it must be a true form (`1`, `t`, `T`, `true`, `TRUE`, `True`) for an `http` endpoint or a false form (`0`, `f`, `F`, `false`, `FALSE`, `False`) for an `https` endpoint |
| `CERTIFICATE` | Path to a readable PEM root certificate; system roots are used for HTTPS when absent |
| `CLIENT_CERTIFICATE` | Path to a readable PEM client certificate; requires the effective `CLIENT_KEY` after signal-specific precedence |
| `CLIENT_KEY` | Path to the paired readable PEM client key; requires the effective `CLIENT_CERTIFICATE` |

`OTEL_RESOURCE_ATTRIBUTES` is intentionally unsupported and must be unset or
empty. The upstream Go SDK's experimental `OTEL_GO_X_OBSERVABILITY`,
`OTEL_GO_X_SELF_OBSERVABILITY`, `OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE`,
`OTEL_GO_X_PER_SERIES_START_TIMESTAMPS`, `OTEL_GO_X_RESOURCE`,
`OTEL_GO_X_CARDINALITY_LIMIT`, and `OTEL_GO_X_EXEMPLAR` variables must also be
unset or empty; Maiden Lane fixes those policies explicitly instead of
inheriting experimental ambient behavior.
OTLP/gRPC, OpenTelemetry log export, baggage propagation, and arbitrary
resource attributes are not part of this foundation. Exporter and HTTP
diagnostic text is sanitized before it reaches ordinary logs.

The observability runtime also registers the five semantic instruments in
[METRICS.md](METRICS.md) and supplies an observer to the semantic spine.
`POST /v1/executions` is a public caller of that spine, so a running process
does record semantic points and emit semantic spans: one execution produces the
HTTP server span plus `compile`, `execute_spine`, `execute_transition`,
`seal_checkpoint`, and `assess_readiness`. Telemetry is strictly
non-authoritative: the semantic result is byte-identical whether the observer
is absent, recording, or backed by a failing exporter.

### Rendering it locally

[`deploy/observability`](deploy/observability/README.md) has a development stack
— collector, Tempo, Prometheus, and Grafana with a provisioned dashboard — that
displays the above without any cloud account:

```bash
make observe-up
```

It publishes loopback-only ports, refuses to start if another process already
holds one, and prints the exporter variables to copy. `make observe-down`
discards it and its data. The stack is a development aid and is deliberately not
part of `make verify`: it needs Docker, and what it produces is something to
look at rather than an assertion.

## Common commands

```bash
make help
make fmt
make verify
make store-check
make container-check
make observe-up
```

`make verify` needs no Docker and no database. `make store-check` runs the
PostgreSQL adapter against a throwaway container, `make container-check`
builds and smoke-tests the image, and `make observe-up` starts the local
observability stack.

`make verify` is the authoritative complete local verification command. Its
`Makefile` recipe enforces formatting and module tidiness, verifies the pinned
analysis-tool versions, runs vet, static analysis, unit tests, race tests, and
the vulnerability scan, then builds `bin/maiden-lane`.

For make-independent diagnosis, the following runs the same stages, in the
same order, with the same tool arguments and output-bearing build path:

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

`make verify` remains authoritative: Make owns the stage ordering and the
human-readable failure messages around formatting and version assertions. Use
the individual Make targets when that extra failure context is useful.

## CI/CD

Pull requests run the complete Go verification sequence and a container health
smoke test. Pushes to `main` and `v*` tags publish the tested image to Amazon
ECR using GitHub OIDC. Publication requires the repository variables
`AWS_REGION`, `AWS_ROLE_ARN`, and `ECR_REPOSITORY`.

CD currently stops at immutable ECR publication. It does not deploy ECS or AWS
Batch resources.

## Design references

- [Inviolates](Inviolates.md)
- [High-Level Design](docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)
- [Current Implementation Guide](docs/implementation/implementation-guide.md)
- [Glossary](GLOSSARY.md)
- [Error Catalog](ERRORS.md)
- [Metrics Catalog](METRICS.md)
