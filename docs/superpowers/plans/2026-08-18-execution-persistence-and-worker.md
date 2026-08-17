# Execution Persistence and Worker Implementation Plan

> **For agentic workers:** Implement this plan task-by-task. Each task ends in a review checkpoint; do not commit unless the owner authorizes it.

**Goal:** Persist executions and run them in a worker, retiring the interim synchronous deviation so `POST /v1/executions` returns `202 Accepted` as the High-Level Design specifies.

**Architecture:** The executions table is the queue. A worker claims work with `SELECT … FOR UPDATE SKIP LOCKED`, runs the spine, and stores the result. `POST /v1/executions` enqueues and returns identities; `GET /v1/executions/{executionID}` returns status and, once complete, the result. Both storage adapters implement one shared contract, as with plans.

**Tech Stack:** Go 1.26.6, existing pgx v5 and chi stacks. No new dependency.

## Global Constraints

- The authority order is [Inviolates](../../../Inviolates.md), the [High-Level Design](../specs/2026-08-11-maiden-lane-high-level-design.md), explicit contracts/tests, this plan, the Implementation Guide, and existing code.
- Use RED -> GREEN -> REFACTOR. Observe a failure for the intended reason before implementing.
- `internal/semantic` must not change. The kernel is complete; this slice adds a caller and a store, not new meaning.
- **Storage may never become a source of semantic meaning.** Identities are re-derived and compared, never read and trusted.
- **No identity-bearing value is stored as `jsonb`.** Identity-bearing content is `bytea`; `jsonb` is permitted only for operational metadata that is never hashed.
- The worker observes wall-clock time for leases. That is operational state and must not reach the semantic kernel, enter any canonical encoding, or influence any artifact identity.
- `make verify` must remain runnable with no Docker and no database.
- Do not add AWS Batch, an outbox, a dispatcher, EventBridge reconciliation, publication, promotion, comparison, or retries with backoff policy.

## Ratified Owner Decision

Recorded 2026-08-18. **`POST /v1/executions` returns `202` only.** One execution path and one lifecycle. A caller wanting a result polls the read endpoint; a convenience wrapper belongs in a client, not in a second server mode. This fully retires the interim deviation rather than preserving it beside its replacement.

This is a breaking contract change. It is acceptable now because no deployment exists and no client depends on the synchronous shape; it would not be acceptable later, which is a reason to do it now.

## Three Properties This Slice Gets For Free

These are consequences of the kernel's determinism, not machinery to build. Each removes something a queue normally needs, and each must be asserted rather than assumed.

**Submission is idempotent by construction.** `ExecutionID` is derived from the semantic request, so submitting identical inputs twice yields the same identity, finds the row already present, and returns the same `202`. There is no idempotency key, no deduplication store, and no expiry policy to get wrong.

**At-least-once delivery is sufficient.** A worker that dies mid-execution leaves a claim that a lease expiry reclaims. Re-running is safe because execution is deterministic: the second attempt produces byte-identical artifacts. Duplicate execution wastes CPU and can never produce a divergent result, so this slice needs no fencing tokens and no exactly-once protocol.

**No outbox is required.** The HLD specifies a transactional outbox because dispatch there targets AWS Batch, an external system, and a dual write to two systems needs reconciling. A worker polling the same PostgreSQL that holds the work has no second system and therefore no dual write. When Batch arrives, the outbox arrives with it.

## Consequence of the 202 Switch: In-Memory Mode Needs an In-Process Worker

With storage in memory, a separate worker process cannot see the queue, so an enqueued execution would never run and every read would report `pending` forever. That would make the default configuration silently useless.

Therefore `serve` runs an in-process worker by default, and `--no-worker` disables it for the deployment shape where a separate `work` process handles execution. The API still answers `202` immediately in both cases: the worker is in the same process, not in the response path, so nothing about availability affects the response.

## Fixed Cross-Task Interfaces

```go
package ports

type ExecutionStatus string

const (
	ExecutionPending   ExecutionStatus = "pending"
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionSucceeded ExecutionStatus = "succeeded"
	ExecutionFailed    ExecutionStatus = "failed"
)

// ExecutionRequest is the pinned semantic input of one execution, stored so a
// worker can run it and so a reclaimed attempt reproduces it exactly.
type ExecutionRequest struct {
	TenantID    TenantID
	ExecutionID semantic.ExecutionID
	RunID       semantic.SemanticRunID
	PlanID      semantic.PlanID
	Input       ExecutionInput
}

// ExecutionResult is the completed projection: identities, digests, closed
// codes, and the canonical bytes of the artifacts the run sealed.
type ExecutionResult struct { /* ... */ }

type ExecutionStore interface {
	// Enqueue is idempotent on ExecutionID and reports whether it created the
	// execution or found it already present.
	Enqueue(context.Context, ExecutionRequest) (created bool, err error)

	// Claim leases one pending execution for the given duration, or reports
	// that none is available.
	Claim(context.Context, time.Duration) (ExecutionRequest, bool, error)

	Complete(context.Context, ExecutionResult) error
	Fail(context.Context, TenantID, semantic.ExecutionID, string) error

	Get(context.Context, TenantID, semantic.ExecutionID) (ExecutionRecord, bool, error)
}
```

---

### Task 1: The Execution Port and Its Contract

**Files:** Modify `internal/ports/storage.go`; create `internal/ports/storagecontract/executions.go`

- [ ] **Step 1: Declare the port**

Tenant scoping is structural, as with plans: every method names the tenant, and no method reads an execution by identity alone.

- [ ] **Step 2: Write the contract suite**

Assert: idempotent enqueue reporting created versus found; a claim returns exactly one pending execution and does not return it again while leased; an expired lease makes it claimable again; completing stores the result and moves the status; failing records a bounded reason and never a raw cause; tenant isolation on every read; another tenant's execution reported absent; concurrent claims never hand the same execution to two workers.

- [ ] **Step 3: Review checkpoint** — the suite must name no implementation and require no clock the caller cannot control.

### Task 2: In-Memory Execution Adapter

**Files:** Modify `internal/adapters/memory/store.go`; create `internal/adapters/memory/executions_test.go`

- [ ] **Step 1: Observe RED against the contract, then implement**

- [ ] **Step 2: Review checkpoint** — confirm the queue is not reachable without a tenant and that leases are honored.

### Task 3: PostgreSQL Execution Adapter

**Files:** Modify `internal/adapters/postgres/schema.sql`, `internal/adapters/postgres/store.go`; create `internal/adapters/postgres/executions_test.go`

- [ ] **Step 1: Observe RED against the contract, then implement**

Claiming uses `SELECT … FOR UPDATE SKIP LOCKED` so concurrent workers never contend for the same row. Identity-bearing content is `bytea`; the queue's own columns are ordinary scalars.

- [ ] **Step 2: Prove the queue under concurrency**

Run several workers against one queue and require every execution to be claimed exactly once while leased, with none lost and none duplicated.

- [ ] **Step 3: Prove storage cannot lie**

A stored result whose identities do not match what it claims must fail closed on read, as with plans.

- [ ] **Step 4: Review checkpoint**

### Task 4: The Worker

**Files:** Create `internal/worker/worker.go`, `internal/worker/worker_test.go`

- [ ] **Step 1: Write failing worker tests**

A claimed execution is run through the spine and its result stored. A semantic rejection is a completed execution carrying a typed failure, not a worker error: the run produced an answer. Machinery inability leaves the execution claimable again rather than marking it failed, because repetition can plausibly change the outcome. A panic in one execution must not take the worker down or lose the lease silently.

- [ ] **Step 2: Observe RED, then implement**

The worker owns no semantic decisions. It claims, invokes the application use case, and stores what it returns.

- [ ] **Step 3: Prove a reclaimed execution is byte-identical**

Run an execution, discard the result, let the lease expire, run it again, and require every identity and digest to match. This is the assertion that makes at-least-once sufficient.

- [ ] **Step 4: Review checkpoint**

### Task 5: The Asynchronous API

**Files:** Modify `api/openapi.yaml`, `internal/httpapi/executions.go`, `internal/httpapi/executions_test.go`, `internal/httpapi/plans.go`

- [ ] **Step 1: Change the contract first**

`POST /v1/executions` becomes `202` returning identities. Add `GET /v1/executions/{executionID}`. Remove the interim-deviation note, because the deviation is gone. Regenerate.

- [ ] **Step 2: Write failing handler tests, then implement**

Submitting twice returns the same identities and creates one execution. A read before the worker runs reports `pending`; after it runs, the complete projection. Another tenant's execution is absent.

- [ ] **Step 3: Review checkpoint** — confirm no synchronous execution path remains.

### Task 6: Composition, Documentation, and Full Verification

**Files:** Modify `cmd/maiden-lane/main.go`, `README.md`, `docs/implementation/implementation-guide.md`, `ERRORS.md`, `METRICS.md`

- [ ] **Step 1: Add the `work` command and the in-process worker**

`serve` runs an in-process worker unless `--no-worker` is given. `work` runs a standalone worker. Both drain on cancellation without abandoning a claimed execution silently.

- [ ] **Step 2: Instrument the worker** with the existing semantic observer, and assert no execution or tenant identity becomes a metric dimension.

- [ ] **Step 3: Update documentation**, including that the interim deviation is retired and that in-memory storage requires the in-process worker.

- [ ] **Step 4: Full verification** — `make verify`, `make store-check`, `make container-check`, plus an end-to-end check against the built binary that an execution submitted to one process is completed and readable.

---

## Execution Order

```text
Task 1 port and contract          (pure Go)
  -> Task 2 in-memory adapter     (pure Go)
  -> Task 3 PostgreSQL adapter
  -> Task 4 worker                (pure Go against either adapter)
  -> Task 5 asynchronous API
  -> Task 6 composition and docs
```
