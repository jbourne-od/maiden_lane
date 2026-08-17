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
- Deterministic compilation of the walking skeleton's two closed
  transformation declarations, checkpoint boundaries, invariant obligations,
  and completeness-profile declarations.
- An immutable, schema-bound, content-addressed atomic patch subset containing
  exactly `Insert`, `Relate`, and `Update`, with closed operation failures,
  explicit update before-images, success-only accepted-application receipts,
  and receipt-authorized verified inverse application.
- A deterministic reference executor for the compiled walking-skeleton plan,
  including verified run binding, the closed related-entity and related-field
  aggregate operators, compiler-derived protected invariant results, typed
  semantic failure reports, and immutable accepted-only journals.
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
- A tenant-scoped HTTP surface over the spine: plan compilation, plan
  retrieval including the declarations the compiler accepted, and synchronous
  execution. `api/openapi.yaml` is the authoritative contract; Go server and
  client code are generated from it and a drift gate runs inside `make verify`.
- Storage interfaces owned by the application in `internal/ports`, with an
  in-process adapter in `internal/adapters/memory` and a durable PostgreSQL
  adapter in `internal/adapters/postgres`. Both are held to one shared
  behavioural contract in `internal/ports/storagecontract`, which is what makes
  substitutability a tested property rather than a claim.

There is no worker mode, execution persistence, promotion gate, publication path,
authentication, or production team-HOS policy.

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

- `cmd/maiden-lane` is a `package main`; building it creates the executable.
- `internal/httpapi` can be imported only from within this module, which keeps
  transport details from becoming a public library contract.
- `internal/observability` is also an infrastructure-only package. Semantic
  packages must not import it or make decisions from telemetry state.
- `internal/semantic` is pure and standard-library-only. Its constructors clone
  caller-owned maps and slices, its getters return defensive copies, and its
  canonical patch order stages inserts before relations before updates. Patch
  construction validates every operation against its pinned schema; malformed
  or schema-incompatible calls are ordinary errors rather than protected
  semantic failures.
- The executor selects only transformations already present in a verified
  compiled plan. It validates the state/journal frontier, resolves T2's team
  through T1's accepted typed output patch, and appends history only after a
  complete patch and every applicable protected check pass. Deterministic
  protected rejection returns the predecessor plus typed failure with nil Go
  error; malformed or inconsistent machinery remains on the error channel.
- Formed-entity identity uses the compiled common-source output-key field,
  independently of the grouping field. Aggregate execution requires a
  present, non-empty atom anchor at both the source and emitted boundaries.
- Established-run journal verification retains only the independently replayed
  prefix and distinguishes entry content-digest mismatch, replay divergence,
  and semantic link inconsistency with the implicated entry content digest.
  Protected failure evidence references are sorted and deduplicated separately
  from the truthful runtime result sequence.
- Checkpoint sealing independently replays the accepted journal from pinned S0,
  requires the caller's state and complete passing invariant evidence to match
  that exact declared prefix, then derives `CheckpointID`,
  `CheckpointArtifactID`, and `CheckpointArtifactDigest` as separate layered
  values. Executor identity is verified as part of the execution contract but
  excluded from checkpoint meaning. The in-memory `KnownArtifacts` input only
  detects an identity/content conflict; it is not a registry or persistence.
- Readiness assessment selects every entity of the compiled kind in canonical
  order and evaluates every normalized atom for each one, so an incomplete
  second team cannot be dropped from an answer. An empty selection is
  vacuously ready. Assessment appends no journal entry and never infers its
  scope from a caller-supplied entity.
- `ExecutionStatus` is application-owned control-plane state and is excluded
  from canonical semantic identity and artifact encoding. The semantic layer
  answers what the computation meant; the application and control plane answer
  what happened while it ran. Equivalent semantic executions remain equivalent
  regardless of application lifecycle representation: new lifecycle vocabulary
  such as queued, retrying, timed out, or cancelled must never force a semantic
  schema or version change, and new semantic outcomes must never require the
  execution controller to own semantic vocabulary.
- The transport layer translates and never decides. Handlers map wire documents
  onto kernel constructors and project artifacts back; they evaluate no rule,
  invariant, or readiness verdict. The compiler request for a stored plan is
  rebuilt from its immutable compilation on each use rather than retained,
  because a compiler request is an ordinary authoring structure of exported
  slices and pointers and storing one would hand callers a mutable alias into
  the store.
- JSON is never a canonicalizer. Identities in responses are the kernel's,
  copied verbatim; nothing in the transport layer hashes a document or
  assembles a digest, and a test fails the build on a digest literal or a hash
  import in that package.
- Tenant scoping is structural. The storage key includes the tenant and the
  port exposes no lookup by identity alone, so an unscoped read is not
  expressible rather than merely discouraged. Another tenant's artifact is
  reported as absent, because distinguishing absence from refusal leaks
  existence.
- Storage is never trusted. A Compilation cannot be serialized at all: its
  fields are private, Compile is the only way to obtain one, and the kernel's
  canonical encoders are one-way with no decoder. A durable adapter therefore
  stores the compilation input in its own encoding, recompiles on read, and
  returns a record only if the reproduced plan identity and input digest match
  what was stored. A row that was corrupted, truncated, or replaced with a
  different but entirely valid program fails closed, which no checksum over the
  bytes would catch.
- Identity-bearing content is stored as `bytea`, never `jsonb`. Postgres `jsonb`
  reorders object keys, drops duplicates, and normalizes numeric forms, which for
  a system whose identities derive from exact canonical bytes is a silent
  mutation of the recipe. A test reads `information_schema` so the rule is
  enforced rather than remembered.
- Tenancy is part of the storage primary key rather than a column to filter on,
  so an unscoped read is not expressible against the table.
- Telemetry dimensions fail closed. `internal/observability` re-declares the
  whole closed vocabulary rather than reusing the application's, so widening an
  application enum cannot widen telemetry without a deliberate edit at the
  boundary. An optional dimension whose value is not admitted is omitted rather
  than relabeled, because a placeholder would assert a classification the spine
  never made; the always-required phase and result fall back to
  `internal_error` as a visible tripwire. Telemetry may drop a point it cannot
  truthfully label, but it may never invent or widen a bounded dimension value.
- `context.Context` carries cancellation across call boundaries. It is passed
  explicitly rather than discovered globally. Here it carries cancellation and
  trace context, never Maiden Lane transformation semantics. The observer
  receives a separate derived context created per run; semantic functions
  always receive the caller's original context.
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

Execution is synchronous and returns `200`, while the High-Level Design
specifies `202 Accepted` with a separate read. That deviation is deliberate and
interim: the asynchronous shape requires a worker mode and durable storage,
neither of which exists. The response body is the projection the eventual read
will return, so clients written now keep working. Retiring the deviation is a
required part of the slice that adds a worker, not optional cleanup.

Executions are not persisted; only plans are. The schema is applied implicitly
on open, which is adequate for one table with no migration history and will stop
being adequate at the first schema change: `CREATE TABLE IF NOT EXISTS` cannot
alter an existing table, so the next change needs an explicit migration step.

`ProvenancePolicy` currently has exactly one valid value, `changes.v1`, so the
policy dimension of the identity matrix is proved by construction rather than
by a differential test. `AttemptID` does not exist in this slice; its exclusion
from canonical identity is therefore vacuous today and is pinned only as the
absence of the concept.

There are no worker or adapter spans, OTel log export, collector deployment, or
wrapped non-health production routes. Eventual package boundaries will be
documented here only after the corresponding code exists.
