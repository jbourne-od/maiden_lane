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
- A pure, standard-library semantic kernel with immutable typed schemas,
  values, entity-graph states, pinned worlds, and canonical content identities.
- Deterministic compilation and execution of the closed rule language:
  selectors, member expressions, group predicates (`all_members`, `any_members`,
  `all_equal`), group reductions (`count`, `sum`, `min`, `max`), assignments,
  checkpoint boundaries, invariant obligations, and completeness-profile
  declarations.
- An immutable, schema-bound, content-addressed atomic patch subset containing
  exactly `Insert`, `Relate`, and `Update`, with closed operation failures,
  explicit update before-images, success-only accepted-application receipts,
  and receipt-authorized verified inverse application.
- A deterministic reference executor for the compiled plan, including verified
  run binding, authorable select-and-assign transformations with group reductions,
  compiler-derived protected invariant results, typed semantic failure reports,
  and immutable accepted-only journals.
- Versioned identities for provenance policy, semantic input/run/execution,
  synthetic entities, accepted journal entries and prefixes, invariant-result
  sets, and protected/integrity failure reports. Executor build identity affects
  only `ExecutionID`; accepted semantic artifacts remain backend-independent.
- Pure checkpoint sealing at exact compiled plan prefixes. A sealed immutable
  manifest binds its declared checkpoint identity, plan/run/input/world/policy
  replay links, accepted journal-prefix digest, complete applicable protected
  invariant-result digest, and canonical state digest. Claim identity remains
  distinct from full-manifest content identity.
- Replay-verified refusal for incomplete or corrupt prefixes, state divergence,
  incomplete protected evidence, and one-claim/two-manifest conflicts. These
  established-run defects are typed integrity results and produce no checkpoint.
- Immutable readiness assessment over sealed checkpoints with explicit
  entity-kind scope, universal aggregation, documented vacuous-empty semantics,
  and profile ordering proved from normalized implication rather than names.
  Assessment identity is derived from the checkpoint artifact and profile, and
  assessing mutates no state or journal.
- A data-only ratified team-HOS fixture package holding the sanitized
  declarations and the two initial-state variants. It implements no
  transformer, evaluator, or alternate canonicalizer, and no production binary
  imports it.
- An application spine that orchestrates compile, bind, transition, seal, and
  assess generically over the compiled plan, advancing an independently
  verified dependency-closed frontier. Semantic rejection returns a typed
  result with a nil Go error; machinery inability returns the retained prefix
  with a non-nil error.
- A closed, non-authoritative observation contract owned by the application,
  and an OpenTelemetry adapter that implements it with five spans and the five
  registered semantic instruments in `METRICS.md`.
- Stable typed application machinery errors with fixed safe text and preserved
  cause chains, registered in `ERRORS.md`.
- A tenant-scoped HTTP surface: plan compilation, plan retrieval including
  declarations the compiler accepted, asynchronous execution submission,
  execution reads, comparison contract creation (`POST /v1/comparisons`),
  comparison contract retrieval (`GET /v1/comparisons/{comparisonID}`), and
  promotion gate evaluation (`POST /v1/publications`). `api/openapi.yaml` is the
  authoritative contract; Go server and client code are generated from it and a
  drift gate runs inside `make verify`.
- Replay corpus identity, comparison policy and comparison identity in the
  kernel, with immutable persistence and rehydration.
- Durable executions and a work queue. The executions table *is* the queue;
  a worker claims with `SELECT … FOR UPDATE SKIP LOCKED`, runs the spine, and
  stores the result.
- A worker in `internal/worker`, run either in the serving process or as a
  separate `work` mode of the same binary.
- Storage interfaces owned by the application in `internal/ports`, with an
  in-process adapter in `internal/adapters/memory` and a durable PostgreSQL
  adapter in `internal/adapters/postgres`. Both are held to one shared
  behavioural contract in `internal/ports/storagecontract`, which is what makes
  substitutability a tested property rather than a claim.

There is no AWS Batch dispatch or production team-HOS policy.

## Current repository map

```text
api/openapi.yaml                 current health wire contract
cmd/maiden-lane/main.go          CLI, process composition, server lifecycle
internal/httpapi/router.go       HTTP transport routes and handlers
internal/observability/          operational config, slog, OTel runtime, HTTP instrumentation, and the semantic observer adapter
internal/semantic/               pure typed state, compiler, atomic patches, reference executor, invariants, journal, checkpoints, and readiness
internal/fixtures/teamhos/       data-only ratified team-HOS declarations and initial-state variants
internal/app/                    spine orchestration, verified frontier, closed observation contract, typed machinery errors
internal/httpapi/                transport: generated routing, problems, tenant scoping, wire translation, handlers
internal/httpapi/openapiv1/      GENERATED server types and routing; never hand-edited
internal/httpapiclient/          GENERATED client, used only by tests
internal/ports/                  storage interfaces owned by the application
internal/adapters/memory/        in-process storage adapter
internal/adapters/postgres/      durable PostgreSQL adapter and its schema
internal/worker/                 claims queued executions and stores their results
internal/ports/storagecontract/  the behavioural contract both adapters must pass
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
   metric providers. Known upstream `OTEL_GO_X_*` switches are rejected at
   configuration and provider boundaries. Production code treats the process
   environment as immutable after startup rather than as a runtime control path.
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

## Semantic spine flow

`app.Run` is reached by `POST /v1/executions`. It executes:

1. compile the plan and profiles;
2. bind the run over the pinned initial state, world, executor identity, and
   provenance policy;
3. for every compiled transformation in plan order, execute the transition;
4. seal every checkpoint declared at that boundary; and
5. assess each sealed checkpoint under every compiled profile.

After each artifact verifies, the run advances a private dependency-closed
frontier. That frontier is what a machinery failure returns, so a caller never
receives an artifact whose dependencies were not themselves verified.

The application reinterprets no rule, patch, invariant, canonical byte, or
readiness answer. It invokes the kernel and observes typed results.

`internal/observability` implements the application's observer interface. The
dependency points one way: observability imports app, and app never imports
observability, so telemetry cannot reach into semantic meaning. Each
`SemanticObserver()` call returns a fresh adapter holding its own per-run span
stacks, which is why one runtime can serve concurrent runs without cross-
parenting their traces.

## Go orientation for Python-oriented contributors

This section assumes fluency in Python and none in Go, because that is the
audience AGENTS.md names. It explains the surprises rather than the syntax.

### The toolchain, if you are expecting a virtualenv

- There is no virtualenv and no `requirements.txt`. Dependencies are pinned in
  `go.mod` and `go.sum`, which together do what a lockfile does; `go build` and
  `go test` fetch what they need into a shared module cache.
- Analysis tools are pinned too, by the `tool` directive in `go.mod`, and run as
  `go tool staticcheck` rather than from a global install. `make tool-versions`
  asserts their exact versions, so a workstation cannot quietly disagree with CI.
  This is the closest thing here to a dev-dependency group.
- The Go version itself is pinned by the `toolchain` line. Go downloads a
  matching toolchain rather than using whatever is on the path.
- `make verify` is the authoritative gate and needs nothing but Go: no Docker, no
  database, no services. Two targets deliberately need more —
  `make store-check` runs the PostgreSQL adapter against a throwaway container,
  and `make container-check` builds and smoke-tests the image. Keeping them
  separate is why the pure-Go work stays verifiable when Docker is unavailable.
- Generated code is committed to the tree, which a Python project usually would
  not do. `api/openapi.yaml` is the authoritative contract, Go is generated from
  it, and `make openapi-check` fails the build if the two disagree. Committing
  the output means a reviewer sees the wire contract change in the diff.

### Structure

- `cmd/maiden-lane` is a `package main`; building it produces the executable.
  It is also the only place composition happens — there is no dependency
  injection framework and no module-level singletons.
- A directory named `internal` is enforced by the compiler: nothing outside this
  module can import it. It is a hard visibility boundary, not a naming
  convention like a leading underscore.
- `internal/ports` declares interfaces the application needs, and
  `internal/adapters/*` implements them. The interfaces live with the consumer
  rather than the implementation, which is the opposite of where a Python
  project often puts an abstract base class.
- What makes two adapters genuinely interchangeable is
  `internal/ports/storagecontract`: one suite of behavioural assertions that
  every adapter must pass. An assertion that only ever ran against one
  implementation would describe that implementation rather than the port.
- The API and the worker are two modes of one binary rather than two services.
  `serve` runs a worker in process unless `--no-worker` is given.

### Testing

- Tests live beside the code in `_test.go` files, and a file in `package foo`
  can reach unexported identifiers while one in `package foo_test` cannot. Both
  appear here deliberately: the second exercises a package the way a caller
  would.
- Table-driven subtests with `t.Run` are the idiom, in place of parametrized
  fixtures.
- `-race` is a real tool, not a linter: it instruments the binary and detects
  concurrent access at runtime. Anything touching the queue or the worker should
  be run under it.
- Several tests deliberately prove a check can fail. A drift gate, a contract
  suite, or an import boundary that has only ever been run against correct code
  has demonstrated nothing, so those are exercised against a deliberately broken
  implementation and then reverted.

### Idioms whose safety consequence is not obvious

- `internal/semantic` is pure and standard-library-only. Its constructors clone
  caller-owned maps and slices and its getters return defensive copies, so a
  caller cannot reach inside an artifact and change it. That is load-bearing:
  a mutable interior would let one holder alter what every later reader sees.
- Assigning a struct in Go copies it shallowly, so two copies share the same
  backing arrays. This is why storing a value with exported slices hands the
  store a mutable alias, and why the deep copy lives in the package that defines
  the type rather than in whichever adapter happens to store it.
- `context.Context` carries cancellation and deadlines across call boundaries
  and is passed explicitly rather than discovered globally. It never carries
  transformation semantics.
- `%w` preserves an error's causal value so callers can use `errors.Is` and
  `errors.As`. Never branch on an error's message text.
- Goroutines do not propagate errors or panics to their caller. A panic in one
  crashes the process unless recovered where it happens, which is why the worker
  contains panics itself.
- `t.Fatalf` from a non-test goroutine is documented misuse; use `t.Errorf` and
  return.
- `os.Exit` skips deferred functions, so only the small `main` calls it, after
  `processMain` has returned an exit code.

### Domain rules that will look like over-engineering until they bite

- Identities are content-derived: the same inputs always produce the same
  identity. That is why submission is idempotent with no deduplication key, and
  why at-least-once delivery is safe — a re-run reproduces byte-identical
  artifacts.
- Storage is never trusted. A stored plan is recompiled on read and returned
  only if the identity it reproduces matches the one stored beside it.
- A deterministic refusal is an answer, not an error. It travels as a result with
  a typed failure, never as a 5xx, because retrying it can only reproduce it.

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

The AWS runtime, persistence, and publication layers described by the HLD are
not implemented. There is no public transformation API, HTTP route, or CLI
command that reaches the semantic spine, so the spine runs only under test.

The patch kernel intentionally supports no delete, unrelate, merge, or split.
Checkpoint sealing implements no promotion or publication behavior, and
comparison, full patch algebra, SQL/dbt backends, and parallel execution are
absent by design at this stage.

The team-HOS rule is a sanitized fixture, not production policy. Its
componentwise-maximum reduction is chosen for determinism in the walking
skeleton and must not be mistaken for a real hours-of-service rule.

Executions are not persisted; only plans are. The schema is applied implicitly
on open, which is adequate for one table with no migration history and will stop
being adequate at the first schema change: `CREATE TABLE IF NOT EXISTS` cannot
alter an existing table, so the next change needs an explicit migration step.

Two limitations follow from deriving identity rather than allocating it. An
execution that reaches a terminal failure cannot be cleared by resubmitting,
because the same request resolves to the same record; retrying one needs an
explicit operation that does not exist yet. And a plan with no transformations
compiles but cannot be executed, so plan creation refuses an empty ruleset rather
than handing back an artifact that is guaranteed useless.

`ProvenancePolicy` currently has exactly one valid value, `changes.v1`, so the
policy dimension of the identity matrix is proved by construction rather than
by a differential test. `AttemptID` does not exist in this slice; its exclusion
from canonical identity is therefore vacuous today and is pinned only as the
absence of the concept.

There are no worker or adapter spans, OTel log export, collector deployment, or
wrapped non-health production routes. Eventual package boundaries will be
documented here only after the corresponding code exists.
