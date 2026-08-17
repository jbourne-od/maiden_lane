# Durable Plan Storage Implementation Plan

> **For agentic workers:** Implement this plan task-by-task. Each task ends in a review checkpoint; do not commit unless the owner authorizes it.

**Goal:** Make stored plans survive a restart, and prove the claim `internal/ports` already makes: that a durable adapter can replace the in-process one without touching `internal/app` or `internal/semantic`.

**Architecture:** A PostgreSQL control-plane adapter joins the in-memory one behind the existing `ports.PlanStore`. Both are held to one shared behavioural contract suite, which is what actually establishes substitutability. Identity-bearing declarations are stored as opaque bytes and re-verified by recompiling on read, so storage cannot lie about semantic identity.

**Tech Stack:** Go 1.26.6, `github.com/jackc/pgx/v5` as the only new runtime dependency, PostgreSQL 17 in Docker for adapter tests. No change to the OpenTelemetry stack.

## Global Constraints

- The authority order is [Inviolates](../../../Inviolates.md), the [High-Level Design](../specs/2026-08-11-maiden-lane-high-level-design.md), explicit contracts/tests, this plan, the Implementation Guide, and existing code.
- Read `AGENTS.md` before every task; inspect `git status` and preserve unrelated work.
- Use RED -> GREEN -> REFACTOR. Observe a failure for the intended reason before implementing.
- `internal/app` and `internal/httpapi` must not change to accommodate the durable adapter. If either needs to change, the port abstraction has failed and that is the finding, not a detail to work around.
- **Storage may never become a source of semantic meaning.** No identity is read from a stored value and trusted. Identity is re-derived by the kernel and compared against what was stored; disagreement is an integrity failure, not a value to return.
- **No identity-bearing value is stored as `jsonb`.** Postgres `jsonb` reorders object keys, drops duplicates, and normalizes numeric forms. For a system whose identity derives from exact canonical bytes that is a silent mutation of the recipe. Identity-bearing content is `bytea`; `jsonb` is permitted only for operational metadata that is never hashed.
- `make verify` must remain runnable with no Docker and no database. Adapter tests that need PostgreSQL live behind a separate target, mirroring how `container-check` already works.
- Do not add S3, a worker mode, async execution, publication, promotion, or comparison. Do not add caching, read replicas, or connection-pool tuning beyond an explicit bounded pool.

## Ratified Owner Decisions

Recorded 2026-08-17.

1. **PostgreSQL is the default store**, not a choice to be abstracted away. Genuine database agnosticism costs the features worth having and buys optionality that is never exercised; `internal/ports` exists to keep I/O out of the domain and to make adapters testable, not to enable vendor swapping.
2. **Blobs stay in PostgreSQL for now.** S3 is deferred until artifact size or cost justifies a second system. One transactional store removes the dual-write class of bug entirely.
3. **`pgvector` is out of scope.** Nothing in a deterministic transformation engine wants embeddings; there is no retrieval step to serve.

## The Blocker This Slice Resolves First

`ports.PlanRecord` currently holds a `semantic.Compilation`. That value **cannot be persisted**: all of its fields are private and `Compile` is the only way to obtain one. The kernel has no decoder at all — every canonical encoder is one-way by design, and there is no `FromBytes`, `Decode`, or `Unmarshal` anywhere in `internal/semantic`.

The in-memory adapter hid this completely, because it stores live Go values.

The port's own doc comment claims a durable adapter should "persist the kernel's canonical bytes verbatim and rehydrate through the kernel's constructors." No constructor accepts bytes, so that instruction is not followable as written.

The resolution keeps the kernel one-way and puts the burden where it belongs:

1. The record carries an **immutable, persistable compilation input** rather than a mutable request or an unpersistable `Compilation`.
2. An adapter serializes that input in whatever form it likes, because the adapter owns its own encoding.
3. On read the adapter **recompiles** and requires the resulting `PlanID` and `CompilationInputDigest` to equal what was stored.

Step 3 is the point. A corrupted, truncated, tampered, or silently re-encoded row cannot produce a plan that claims the stored identity, so storage is structurally unable to lie. It also removes the need to trust the serialization format: if the round trip is lossy in any way that matters, the identities disagree and the read fails closed.

Note the history, so it is not undone: a `semantic.CompileRequest` was briefly held directly and had to be removed, because it is an ordinary authoring structure of exported slices and pointers and storing one handed callers a mutable alias into the store. The fix for that removed the only field an adapter could have persisted. The immutable input value satisfies both constraints at once.

## Fixed Cross-Task Interfaces

```go
package semantic

// CompilationInput is the immutable, persistable input that produced a
// compilation. Its interior is unreachable: Request returns a deep copy.
type CompilationInput struct { /* private */ }

func (i CompilationInput) Request() CompileRequest
func (i CompilationInput) Digest() CompilationInputDigest
func (i CompilationInput) CanonicalBytes() []byte

func (c Compilation) Input() CompilationInput
```

```go
package ports

type PlanRecord struct {
	TenantID TenantID
	PlanID   semantic.PlanID
	Input    semantic.CompilationInput
	Schema   semantic.Schema
	Compilation semantic.Compilation
}
```

```go
package storagecontract // internal/ports/storagecontract

// RunPlanStoreContract asserts every behaviour a PlanStore must have. Both
// adapters run it, which is what establishes substitutability.
func RunPlanStoreContract(t *testing.T, newStore func(*testing.T) ports.PlanStore)
```

---

### Task 1: An Immutable, Persistable Compilation Input

**Files:** Modify `internal/semantic/compile.go`, `internal/semantic/compile_test.go`, `internal/ports/storage.go`, `internal/adapters/memory/store.go`, `internal/httpapi/wire.go`, `internal/httpapi/plans.go`

- [ ] **Step 1: Write failing immutability and sufficiency tests**

Prove `Compilation.Input().Request()` returns a deep copy whose mutation cannot reach the compilation, and that recompiling the returned request reproduces the identical `PlanID`, profile identities, and `CompilationInputDigest`.

- [ ] **Step 2: Observe RED, then implement**

`Compile` retains a cloned request. The clone lives in the kernel beside the types it copies, so a field added to a declaration is handled where it is defined rather than in an adapter that cannot see it. No canonical bytes and no digest change, so every frozen golden vector stays valid; assert that.

- [ ] **Step 3: Carry the input on the record and drop the rebuild**

`ports.PlanRecord` gains `Input`. `internal/httpapi` uses `record.Input.Request()` instead of `compileRequestFor`, which is deleted. Confirm no behaviour changes.

- [ ] **Step 4: Review checkpoint** — confirm the kernel gained no import and no digest moved.

### Task 2: One Shared Port Contract Suite

**Files:** Create `internal/ports/storagecontract/plans.go`; modify `internal/adapters/memory/store_test.go`

- [ ] **Step 1: Extract the behavioural contract**

Move every behavioural assertion the in-memory adapter already passes into a reusable suite: tenant isolation, a foreign tenant reported as absent, idempotent put, incomplete records refused, cancellation honoured, concurrency safety, and stored records unreachable for mutation.

- [ ] **Step 2: Run it against the in-memory adapter and observe GREEN**

The suite must pass unchanged. If it needs weakening to pass, it has stopped describing the contract.

- [ ] **Step 3: Add the assertions only a durable adapter can fail**

Round-trip fidelity: a record written and read back must recompile to the identical plan and profile identities. The in-memory adapter passes trivially, which is the point — the suite is written so that a durable adapter cannot pass by accident.

- [ ] **Step 4: Review checkpoint** — confirm the suite is adapter-agnostic and names no implementation.

### Task 3: The PostgreSQL Adapter

**Files:** Create `internal/adapters/postgres/store.go`, `internal/adapters/postgres/schema.sql`, `internal/adapters/postgres/store_test.go`; modify `go.mod`

- [ ] **Step 1: Write the failing contract run**

Point `RunPlanStoreContract` at the PostgreSQL adapter. Expect failure: nothing exists.

- [ ] **Step 2: Implement schema and adapter**

Declarations are `bytea`, byte-exact. Tenancy is part of the primary key. A unique constraint on `(tenant_id, plan_id)` makes "one identity cannot resolve to two contents" a database-enforced invariant rather than an in-process comparison. Operational columns may be `jsonb`; identity-bearing content may not.

- [ ] **Step 3: Recompile and verify on read**

A read rebuilds the request from stored bytes, recompiles, and requires the `PlanID` and `CompilationInputDigest` to match the stored values. Mismatch is a typed integrity error, never a returned record.

- [ ] **Step 4: Prove storage cannot lie**

Corrupt a stored row directly in SQL — flip bytes, truncate, swap another tenant's declarations in — and require every read to fail closed rather than return a plan under the stored identity. This is the assertion the whole design exists to support.

- [ ] **Step 5: Review checkpoint** — confirm `internal/app` and `internal/httpapi` are untouched.

### Task 4: Docker-Gated Verification

**Files:** Modify `Makefile`, `.github/workflows/pipeline.yml`

- [ ] **Step 1: Add `make store-check`**

Start PostgreSQL in Docker, apply the schema, run the adapter tests, tear down on exit including on failure. `make verify` stays pure Go and must not require Docker or a database.

- [ ] **Step 2: Wire it into CI beside `container-check`** and confirm a missing database fails loudly rather than skipping silently.

### Task 5: Composition, Documentation, and Full Verification

**Files:** Modify `cmd/maiden-lane/main.go`, `internal/observability/config.go`, `README.md`, `docs/implementation/implementation-guide.md`, `ERRORS.md`

- [ ] **Step 1: Select the store by explicit configuration**

Absent configuration keeps the in-memory adapter, so local runs need no database. A configured URL selects PostgreSQL and a failure to reach it blocks startup rather than silently degrading to memory: quietly serving from memory when durability was requested is the worst available outcome.

- [ ] **Step 2: Update current-state documentation** only after the code exists, including that artifacts now survive a restart and what still does not.

- [ ] **Step 3: Full verification** — `make verify`, `make store-check`, `make container-check`.

---

## Execution Order

```text
Task 1 immutable persistable input   (pure Go, unblocks everything)
  -> Task 2 shared contract suite    (pure Go)
  -> Task 3 PostgreSQL adapter
  -> Task 4 Docker-gated verification
  -> Task 5 composition and docs
```

Tasks 1 and 2 need no infrastructure and can be verified entirely by `make verify`.
