# HTTP API Boundary Implementation Plan

> **For agentic workers:** Implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each task ends in a review checkpoint; do not commit unless the owner authorizes it.

**Goal:** Give the semantic spine its first caller. Expose a small, tenant-scoped, spec-first HTTP surface that compiles a plan and executes it synchronously, with `api/openapi.yaml` as the authoritative generated-code source of truth so clients can be generated mechanically.

**Architecture:** `api/openapi.yaml` is the single authoritative wire contract. `oapi-codegen` generates chi server interfaces, request/response types, and a test client from it; generated files are never hand-edited and CI fails on drift. `internal/httpapi` translates wire DTOs into `internal/app` commands and RFC 9457 problems, and contains no transformation semantics. `internal/ports` owns the storage interfaces the HLD specifies; `internal/adapters/memory` implements them in-process. `internal/semantic` is untouched.

**Tech Stack:** Go 1.26.6, existing chi v5 and OpenTelemetry stacks, `github.com/oapi-codegen/oapi-codegen/v2` v2.8.0 as a tracked `go tool`, and `github.com/oapi-codegen/runtime` v1.7.0 as the only new runtime dependency.

## Global Constraints

- The authority order is [Inviolates](../../../Inviolates.md), the [High-Level Design](../specs/2026-08-11-maiden-lane-high-level-design.md), explicit contracts/tests, this plan, the current Implementation Guide, and existing code.
- Read `AGENTS.md` before every task; inspect `git status` and preserve unrelated work.
- Use RED -> GREEN -> REFACTOR for every behavioral step. Observe a failure for the intended reason before implementing.
- `internal/semantic` must not change. It is a completed, verified kernel; this slice adds a caller, not new meaning. The Task 10 import-boundary test must keep passing untouched.
- **JSON is never a canonicalizer.** Wire DTOs carry identities and digests as opaque strings produced by the semantic kernel. The API must never hash a DTO, derive an identity from JSON, or reconstruct a digest. Canonical identity comes only from the kernel's binary encoders (Inviolate 4, design section 6).
- Handlers translate wire DTOs into application commands and back. They must not re-evaluate a rule, invariant, readiness verdict, or checkpoint validity.
- `api/openapi.yaml` is authoritative. Go server types are generated from it, never the reverse. Generated files identify themselves as generated, have exactly one source, are reproducibly regenerable, and are never edited by hand (AGENTS.md section 16).
- Every `/v1` operation is tenant scoped. A read for an identifier belonging to another tenant returns `404`, never `403`: an existence leak is an authorization failure.
- All errors use RFC 9457 `application/problem+json` with a closed problem-type vocabulary. Problem documents carry no payload, entity reference, evidence body, stack, or raw dependency text.
- A deterministic semantic outcome is a successful HTTP response, not a problem. A failed protected invariant, an artifact integrity failure, and a `needs_input` readiness verdict are answers, not errors.
- Do not add persistence beyond the in-memory adapter, publication, promotion, comparison, worker modes, AWS orchestration, or authentication. Do not widen the semantic kernel to serve the wire.
- Update current-state documentation only after the corresponding code exists.

## Ratified Owner Decisions

Recorded 2026-08-16. These were the questions this slice could not answer from the code or the HLD alone.

1. **Scope:** plans plus synchronous executions. The spine becomes callable over HTTP in this slice.
2. **Storage:** in-memory behind `internal/ports`, per HLD section 15. Postgres and S3 adapters are a later swap and must not require changes to `internal/app` or `internal/semantic`.
3. **Tenancy:** a tenant identifier is required on every `/v1` operation and scopes every read and write. Authentication is delegated to the gateway/ALB and is deliberately not implemented here.

## Interim Deviation From HLD Section 16

The HLD specifies that execution creation returns `202 Accepted` with a `SemanticRunID` and `ExecutionID`, with results retrieved later. That shape presumes a job queue, a worker mode, and durable storage, none of which exist.

**This slice implements `POST /v1/executions` synchronously, returning `200 OK` with the complete result.**

This is an interim implementation deviation, not an architectural amendment. The HLD continues to describe the target. The deviation is recorded here, in the OpenAPI operation description, in the SDD ledger, and in the Implementation Guide's known gaps, so it cannot become the accidental architecture.

**Exit condition:** when a worker mode and durable storage exist, `POST /v1/executions` returns `202` per the HLD and the synchronous path is removed or moved behind an explicit preference. Retiring the deviation is a required part of that slice, not optional cleanup.

Two consequences follow and must be preserved:

- The synchronous response body is the same artifact projection a later `GET /v1/executions/{executionID}` will return, so clients written now keep working when the async shape lands.
- The deviation must not leak into semantic meaning. `ExecutionID` is already derived deterministically by the kernel, so the HLD's idempotency rule holds today without any request-deduplication machinery: repeating an identical semantic request reproduces the same `SemanticRunID` and `ExecutionID`, and changing only the executor identity produces a different `ExecutionID` over the same `SemanticRunID`. This must be asserted by test, not assumed.

## Planned Repository Shape

```text
api/openapi.yaml                          authoritative wire contract
api/oapi-codegen-server.yaml              server generation configuration
api/oapi-codegen-client.yaml              test-client generation configuration
internal/httpapi/openapi_gen.go           GENERATED server types and chi wiring
internal/httpapi/router.go                route registration and middleware order
internal/httpapi/problem.go               RFC 9457 rendering and closed problem vocabulary
internal/httpapi/tenant.go                tenant extraction and scoping middleware
internal/httpapi/wire.go                  DTO translation to and from app/semantic values
internal/httpapi/plans.go                 plan creation and retrieval handlers
internal/httpapi/executions.go            execution handler
internal/httpapi/*_test.go                contract, problem, tenancy, and handler tests
internal/httpapiclient/client_gen.go      GENERATED client used only by tests
internal/ports/storage.go                 storage interfaces owned by the application
internal/adapters/memory/store.go         in-process tenant-scoped implementation
internal/adapters/memory/store_test.go    isolation, immutability, and concurrency tests
```

## Fixed Cross-Task Interfaces

```go
package ports

// PlanRecord is one compiled plan retained for later execution. It holds the
// kernel's own typed values; nothing here is re-derived from JSON.
type PlanRecord struct {
	TenantID    TenantID
	PlanID      semantic.PlanID
	Compilation semantic.Compilation
}

type TenantID string

// PlanStore is tenant-scoped by construction: no method can read across
// tenants, so a handler cannot forget to filter.
type PlanStore interface {
	PutPlan(context.Context, PlanRecord) error
	GetPlan(context.Context, TenantID, semantic.PlanID) (PlanRecord, bool, error)
}
```

```go
package httpapi

// Dependencies is explicit constructor injection; there is no service locator.
type Dependencies struct {
	Plans   ports.PlanStore
	Runner  SpineRunner
	Observer app.Observer
}

// SpineRunner is the consumer-owned narrow interface over the application use
// case, so handler tests do not need the full kernel.
type SpineRunner interface {
	Run(context.Context, app.Request, app.Observer) (app.SpineResult, error)
}

func NewRouter(Dependencies) http.Handler
```

## Ratified Closed Problem Vocabulary

Problem `type` values are stable URIs under `https://maiden-lane.optimaldynamics.com/problems/`. Implement exactly these; add none opportunistically.

| Slug | Status | Meaning |
|---|---|---|
| `invalid-request` | 400 | Malformed JSON, missing required field, or unparsable value. |
| `tenant-required` | 400 | The tenant identifier header was absent or malformed. |
| `not-found` | 404 | No such artifact for this tenant. Also returned for another tenant's artifact, and for an unmatched path. |
| `method-not-allowed` | 405 | The resource does not support the requested method. Added during Task 2: the router already answered 405, and rendering it as a problem keeps every failure decodable by one generated type. |
| `unsupported-media-type` | 415 | Request body was not `application/json`. |
| `invalid-plan` | 422 | Compilation rejected the declarations. Carries the closed compiler diagnostic codes and no `planID`. |
| `invalid-semantic-input` | 422 | Canonical input was incomplete or unsupported at the request boundary. |
| `internal-error` | 500 | Machinery inconsistency. Carries a stable code and no internal detail. |
| `dependency-unavailable` | 503 | A required dependency was unavailable. Retryable. |

`invalid-plan` is a problem because no plan exists. A protected invariant failure during execution is **not** in this table: it is a successful `200` response carrying a typed failure, because the run produced a real, deterministic answer.

---

### Task 1: Authoritative Contract and Generated-Code Toolchain

**Files:**
- Modify: `api/openapi.yaml`
- Create: `api/oapi-codegen-server.yaml`, `api/oapi-codegen-client.yaml`
- Create: `internal/httpapi/openapi_gen.go`, `internal/httpapiclient/client_gen.go` (both generated)
- Modify: `Makefile`, `go.mod`, `go.sum`, `.github/workflows/pipeline.yml`
- Create: `internal/httpapi/contract_test.go`

- [ ] **Step 1: Write the authoritative contract first**

Expand `api/openapi.yaml` to 3.1.0 covering `/healthz`, `/readyz`, `POST /v1/plans`, `GET /v1/plans/{planID}`, and `POST /v1/executions`. Define the RFC 9457 `Problem` schema, the tenant header parameter, and every DTO. Give every operation an `operationId`, because generated client method names come from it. Document the synchronous-execution deviation in that operation's description.

- [ ] **Step 2: Add the generator as a tracked tool and observe RED**

Add `oapi-codegen` to the `tool` directive alongside Staticcheck and govulncheck, and `oapi-codegen/runtime` as a runtime dependency. Add `make openapi` (regenerate) and `make openapi-check` (regenerate into a temporary location and fail on any difference).

Run `make openapi-check`. Expected: it fails because no generated file exists yet.

- [ ] **Step 3: Generate and wire the drift gate**

Run `make openapi`. Add `openapi-check` to `verify` before `vet`, and to the CI workflow. Confirm generated files carry their "Code generated ... DO NOT EDIT." header.

- [ ] **Step 4: Prove the contract is authoritative, not decorative**

Write tests asserting the generated server interface has a method per `operationId`, and that a hand-edit to a generated file is caught. Prove regeneration is deterministic by generating twice and comparing bytes.

- [ ] **Step 5: Review checkpoint**

Verify `go.mod` gained exactly the intended tool and runtime dependency, and that no generated file is hand-edited. Do not commit.

### Task 2: RFC 9457 Problems and Tenant Scoping

**Files:**
- Create: `internal/httpapi/problem.go`, `internal/httpapi/tenant.go`
- Create: `internal/httpapi/problem_test.go`, `internal/httpapi/tenant_test.go`
- Modify: `internal/httpapi/router.go`

- [ ] **Step 1: Write failing problem and tenancy tests**

Assert every problem renders `application/problem+json` with the exact status, stable type URI, and no leaked internals. Feed hostile tenant headers, oversized values, and control characters; assert rejection without echoing the input. Assert a missing tenant on a `/v1` route is `400` while `/healthz` and `/readyz` remain unauthenticated and untenanted.

- [ ] **Step 2: Observe RED, then implement**

Implement the closed problem vocabulary and a tenant middleware that validates and attaches a bounded tenant identifier to the request context. Reject rather than normalize a malformed identifier.

- [ ] **Step 3: Prove the no-existence-leak rule**

Assert that a well-formed identifier belonging to another tenant is indistinguishable from a nonexistent one: same status, same body, no timing-dependent branch on existence before the tenant filter.

- [ ] **Step 4: Review checkpoint**

Confirm no problem document contains a payload, digest, evidence reference, or Go error text. Do not commit.

### Task 3: Storage Port and In-Memory Adapter

**Files:**
- Create: `internal/ports/storage.go`
- Create: `internal/adapters/memory/store.go`, `internal/adapters/memory/store_test.go`

- [ ] **Step 1: Write failing isolation and immutability tests**

Two tenants storing the same `PlanID` must not observe each other. A stored record must not change when the caller mutates its input afterward, and a retrieved record must not change when the caller mutates the result. Concurrent readers and writers must be race-clean.

- [ ] **Step 2: Observe RED, then implement**

Implement the port and a mutex-protected in-memory store. Tenant scoping is structural: the key includes the tenant, so no query can omit it.

- [ ] **Step 3: Document durability honestly**

The package comment must state that records are lost on restart, that this is a single-process adapter, and that the port exists so a durable adapter can replace it without touching `internal/app`.

- [ ] **Step 4: Review checkpoint**

Confirm the adapter imports no AWS SDK, database driver, or semantic internals beyond the public kernel types. Do not commit.

### Task 4: Wire Translation Without a Second Canonicalizer

**Files:**
- Create: `internal/httpapi/wire.go`, `internal/httpapi/wire_test.go`

**This is the highest-risk task in the slice.** Translation is where a wire format quietly becomes a second source of semantic meaning.

- [ ] **Step 1: Write failing translation tests**

Build a state DTO, translate it into a `semantic.State` against a plan's schema, and assert the resulting `StateDigest` equals the digest the kernel produces for the equivalent directly-constructed state. Assert that a DTO with fields in a different order produces the identical digest, and that an unknown field or wrong-typed value is rejected as `invalid-request` rather than silently dropped.

- [ ] **Step 2: Observe RED, then implement**

Translate DTOs into kernel constructors and let the kernel validate. Project artifacts outward as identities, digests, and closed tokens.

- [ ] **Step 3: Prove JSON never becomes identity**

Assert no code path hashes a DTO: identities in responses must be byte-equal to the ones the kernel produced, obtained by projection rather than recomputation. Add a test that mutating a response DTO cannot change any stored artifact.

- [ ] **Step 4: Review checkpoint**

Confirm no `encoding/json` usage exists inside `internal/semantic` and that no digest is constructed in `internal/httpapi`. Do not commit.

### Task 5: Plan Creation and Retrieval

**Files:**
- Create: `internal/httpapi/plans.go`, `internal/httpapi/plans_test.go`

- [ ] **Step 1: Write failing plan tests**

`POST /v1/plans` with valid declarations returns `201` with a `planID` and the compiled profile identities. Invalid declarations return `422 invalid-plan` carrying the closed diagnostic codes and **no** `planID`. `GET /v1/plans/{planID}` returns the stored plan for its own tenant and `404` for another's.

Assert that compiling identical declarations twice yields the same `planID`, since plan identity is content-derived.

- [ ] **Step 2: Observe RED, then implement**

- [ ] **Step 3: Review checkpoint**

Confirm the handler performs no compilation logic of its own. Do not commit.

### Task 6: Synchronous Execution

**Files:**
- Create: `internal/httpapi/executions.go`, `internal/httpapi/executions_test.go`

- [ ] **Step 1: Write the failing result-matrix tests**

This is the contract that matters most; assert the full matrix explicitly:

| Outcome | HTTP | Body |
|---|---|---|
| Spine succeeded | 200 | Complete result: statuses, run and execution identities, checkpoints, assessments |
| Protected invariant failed | 200 | Same projection plus the typed failure and retained verified prefix |
| Artifact integrity failed | 200 | Same projection plus the typed failure |
| Referenced plan not found for tenant | 404 | `not-found` problem |
| Incomplete canonical input | 422 | `invalid-semantic-input` problem |
| Cancellation or timeout | 499/503 | `dependency-unavailable` problem, no partial body |
| Machinery inconsistency | 500 | `internal-error` problem |

Use the ratified team-HOS fixture for both the passing and anchor-mismatch variants. Assert the anchor-mismatch response is `200`, carries `HOS_ANCHOR_MISMATCH`, retains exactly one sealed checkpoint, and reports two readiness assessments.

- [ ] **Step 2: Observe RED, then implement**

- [ ] **Step 3: Prove deterministic identity through the wire**

Assert that repeating an identical request reproduces the same `semanticRunID` and `executionID`, and that changing only the executor identity preserves `semanticRunID` while changing `executionID`. This is the HLD's idempotency rule, satisfied by construction rather than by request deduplication.

- [ ] **Step 4: Review checkpoint**

Confirm a semantic rejection never renders as a problem document. Do not commit.

### Task 7: Instrumentation, Documentation, and Full Verification

**Files:**
- Modify: `internal/httpapi/router.go`, `cmd/maiden-lane/main.go`
- Modify: `README.md`, `METRICS.md`, `ERRORS.md`, `docs/implementation/implementation-guide.md`, `api/openapi.yaml`

- [ ] **Step 1: Wrap the new routes and compose the process**

Register `/v1` routes through the existing `InstrumentHTTPRoute` wrapper so they record the HTTP instruments already in `METRICS.md`, and pass the runtime's `SemanticObserver()` into the execution use case. Health and readiness stay unwrapped.

- [ ] **Step 2: Assert telemetry stays bounded**

The route template, not the request path, must be the metric dimension. Assert no tenant identifier, plan identifier, or digest becomes a metric label.

- [ ] **Step 3: Update current-state documentation**

`README.md` gains the real API surface and must stop claiming there is no public transformation API, while stating what remains absent. `METRICS.md` records that HTTP points are now recorded for wrapped `/v1` routes and that semantic points are now recorded in the running process. `ERRORS.md` registers the problem vocabulary and its relationship to the machinery errors. The Implementation Guide gains the transport layer, the ports/adapters boundary, and the synchronous-execution deviation with its exit condition.

- [ ] **Step 4: Full verification**

```bash
make openapi-check
make verify
make container-check
```

- [ ] **Step 5: Final diff and boundary inspection**

Confirm `internal/semantic` is unchanged, the Task 10 boundary test still passes, no generated file was hand-edited, and `Inviolates.md` and the HLD are untouched.

---

## Execution Order

```text
Task 1 contract and codegen toolchain
  -> Task 2 problems and tenancy
  -> Task 3 storage port and adapter
  -> Task 4 wire translation
  -> Task 5 plans
  -> Task 6 synchronous execution
  -> Task 7 instrumentation, docs, verification
```

Tasks 2 and 3 are independent of each other and both depend only on Task 1. Everything from Task 4 onward is strictly sequential.
