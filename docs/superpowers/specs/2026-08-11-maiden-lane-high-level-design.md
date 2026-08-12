# Maiden Lane High-Level Design

**Status:** Approved design for the current documentation phase

**Date:** 2026-08-11

**Audience:** Data engineering, platform engineering, application engineering, and technical leadership

## 1. Executive summary

Maiden Lane is a Go service for compiling, executing, explaining, and gating data transformations. Its purpose is to make an entire class of customer-discovered mapper defects impossible to deploy.

The current mapper treats SQL and dbt models as both the execution representation and the source of business meaning. Maiden Lane separates those concerns. Customer semantics are expressed as closed, typed transformation rules and compiled into an immutable, backend-independent semantic plan. The plan can initially execute in Go and may later compile into SQL or dbt without changing the meaning of the rules.

A transformation does not directly mutate published state. It proposes an attributable structural patch, validates the patch and resulting candidate state, and records a semantic journal. A candidate becomes publishable only after protected invariants and regression policy pass. Publication is an atomic pointer update to an immutable artifact.

Maiden Lane is a separate Go module. It may reuse stable infrastructure primitives from stochflow, but only through Maiden Lane-owned ports and adapters. Stochflow's agent contracts, economic statechart, and agent-specific journal do not define Maiden Lane's domain.

## 2. Problem statement

SQL remains an effective execution technology for set-oriented warehouse transformations. The problem is that mapper semantics have leaked into SQL, Jinja, generated SQL, Python UDFs, and per-customer YAML. This produces several systemic failure modes:

- Business concepts such as team state, delivery state, and entity scope lack one typed definition.
- Invalid aggregate states can be represented and published successfully.
- Customer-specific rules can hide their dependencies inside arbitrary SQL.
- Mutable reference data and catalogs make historical execution difficult to reproduce.
- Fused SQL obscures which semantic rule changed an entity and why.
- Most failures are discovered by inspecting final relations, sometimes after a customer sees them.

The target is not to eliminate SQL. It is to make SQL one possible backend for semantics defined elsewhere.

## 3. Goals

Maiden Lane will establish the following properties:

1. Transformation semantics belong to Maiden Lane, not to an execution backend.
2. Rules compile into an immutable, inspectable, content-addressed semantic plan.
3. Structural operations cover inserts, deletes, updates, merges, splits, and relation changes.
4. Static plan validation and dynamic state validation are separate, fail-closed phases.
5. Journals describe semantic changes rather than backend mechanics.
6. Everything that can affect replay is pinned and content-addressed.
7. A failed protected invariant cannot produce a publishable artifact.
8. Baseline and candidate plans can execute against the same historical world and be compared before publication.
9. Execution backends must prove semantic equivalence to the reference Go executor.

## 4. Non-goals for this design phase

This design does not commit to:

- A production implementation milestone or first customer rollout.
- A final JSON or YAML surface syntax for the rule language.
- A SQL or dbt backend.
- Parallel transformer execution.
- Arbitrary SQL in the certified transformation language.
- Visual rule authoring or automated rule repair.
- Multi-region active-active operation.
- A general-purpose workflow engine.

These are outside the current design exercise, not promises on a roadmap.

## 5. Architectural decisions

### 5.1 Separate application with a narrow stochflow boundary

Three approaches were considered:

| Approach | Decision |
|---|---|
| Separate application with selective stochflow reuse | Chosen. It preserves Maiden Lane's domain while reusing suitable infrastructure. |
| Compile transformations directly into the current stochflow statechart | Rejected. Its contracts, execution vocabulary, economic state, and journal are agent-specific. |
| Implement every primitive independently | Rejected for now. It would duplicate stable byte-level hashing and comparison work. |

Maiden Lane defines its own ports:

```go
type ContentHasher interface {
	HashCanonical(data []byte) Digest
}

type CandidateComparator interface {
	Compare(ctx context.Context, baseline, candidate ExecutionRef) (Comparison, error)
}
```

Only `internal/adapters/stochflow` may import stochflow. Maiden Lane owns its canonical byte formats in `internal/canonical`; hashing adapters receive only bytes that Maiden Lane has already canonicalized. Initial stochflow reuse may include its byte-level digest helper from `journal` and the generic paired comparison behavior in `compare`. The adapter can be replaced without changing any semantic package or reinterpreting a Maiden Lane value.

Canonicalization and hashing are separate contracts: Maiden Lane decides what
the bytes mean, while `ContentHasher` applies the configured digest algorithm
to those bytes. A canonical format or digest algorithm change requires an
explicit version migration rather than an invisible adapter change.

The dependency is pinned to a tag or exact pseudo-version. A local `replace github.com/optimaldynamics/stochflow => ../stochflow` directive is acceptable for workstation development but is not a deployment strategy.

### 5.2 One source of semantic meaning

Rules never compile independently into separate Go and SQL interpretations:

```text
rules + schema + compiler version
                │
                ▼
       canonical semantic plan
          ┌─────┴─────┐
          ▼           ▼
     Go executor   SQL compiler
```

The Go executor is the reference implementation. A future SQL compiler consumes the canonical semantic plan, not the original rule document.

### 5.3 Logical immutability

States, plans, rule sets, worlds, journals, and outputs are immutable semantic values. An implementation may use copy-on-write structures, chunked files, or database transactions, provided the externally visible semantics remain immutable and content-addressed.

## 6. Identity model

Identity is layered so semantic intent is independent of physical execution:

\[
InputID = H(S_0, C)
\]

\[
PlanID = H(P)
\]

\[
SemanticRunID = H(InputID, PlanID)
\]

\[
ExecutionID = H(SemanticRunID, ExecutorIdentity, ProvenancePolicy)
\]

Where:

- `S0` is the canonical input state.
- `C` is the pinned execution world.
- `P` is the canonical semantic plan.
- `SemanticRunID` identifies the requested computation independently of how it is executed.
- `ExecutorIdentity` includes the backend and its version, such as `go@<digest>` or `sql-snowflake@<digest>`.
- `ProvenancePolicy` affects required execution artifacts but not the requested computation.

An operational retry has a separate `AttemptID`. Attempts may change timing and infrastructure placement but cannot change the semantic inputs or executor identity of an execution.

Repeating the same `ExecutionID` must return existing artifacts or reproduce byte-identical artifacts. Any divergence is a hard integrity failure. Two certified executors for one `SemanticRunID` must produce identical final state, semantic journal, and invariant-result digests at the required provenance level. Operational metadata such as timestamps, Batch job IDs, and attempt counts is excluded from those semantic artifacts.

Publication points to an immutable artifact produced by a certified
`ExecutionID`, while retaining both `SemanticRunID` and `ExecutionID` in its
audit record. This permits backend certification to state, without collapsing
the executions into one identity:

```text
SemanticRunID X

Go ExecutionID  → state digest A, journal digest B
SQL ExecutionID → state digest A, journal digest B
```

## 7. Semantic state and pinned world

### 7.1 State

State is a typed entity graph:

```text
State
├── schema digest
├── entities
│   └── EntityRef(kind, source ID) → typed Entity
├── relations
│   └── Relation(kind, from, to)
└── state digest
```

Entity identities are stable within an input lineage. Relations are explicit values rather than incidental foreign-key joins. Field values carry schema-defined types; semantic code does not depend on untyped maps.

#### 7.1.1 Source and synthetic entity identity

Source entity IDs are deterministically namespaced from the input lineage,
entity kind, and canonical source key. Every operator that creates an entity
must also provide a typed, deterministic output-key expression in the semantic
plan. The expression may read only declared fields and pinned world values.

The reference construction for a synthetic identity is:

\[
EntityID = H_c(
  "maiden-lane.synthetic-entity.v1",
  InputLineageID,
  EntityKind,
  RuleID,
  CanonicalProgenitors,
  SemanticOutputKey
)
\]

`Hc` means hashing Maiden Lane's canonical encoding of the tuple.
`CanonicalProgenitors` is a canonical sequence of input identities. For an
unordered operation it is sorted by `EntityID`; when roles are semantically
meaningful it is a sequence of `(role, EntityID)` sorted by role and then ID.
`SemanticOutputKey` distinguishes multiple outputs of the same kind, including
the outputs of a split.

The compiler rejects a creating operator without a valid output-key expression.
The executor rejects an identity collision. Wall-clock values, random UUIDs,
backend row order, attempt identity, and execution identity are forbidden from
entity-ID construction. Consequently, certified backends create the same
entity IDs for the same `SemanticRunID`.

### 7.2 World

The execution world contains every external value a rule may observe, referenced by immutable digest or version:

```text
World
├── customer input snapshot
├── location reference snapshot
├── timezone catalog snapshot
├── rate or mileage reference snapshot
├── schema version
└── policy/configuration snapshots
```

Semantic execution performs no unjournaled external reads. A transform that needs reference data reads it from the pinned world supplied to the execution.

## 8. Closed rule language

A rule is a typed declaration containing:

- Stable rule identity and version.
- A closed predicate expression.
- Typed transformation operator and operands.
- Fields and relations read.
- Fields and relations written.
- Explicit ordering requirements where data dependencies alone are ambiguous.
- Preconditions and postconditions.
- Evidence requirements.

The initial grammar is limited to statically analyzable constructs such as typed comparisons, Boolean composition, null and existence checks, deterministic lookups, bounded arithmetic, aggregates, and the structural operations defined below.

The compiler derives read and write sets from the expression tree and operator. If the authored declarations disagree with the derived sets, compilation fails. Data-dependent field names and dynamic code evaluation are prohibited in the certified language.

Arbitrary SQL is not supported by the current design. If an `opaque_sql` migration mechanism is introduced later, it must be visibly unsafe, carry degraded guarantees, prohibit fusion, and remain non-publishable through the normal certified path.

## 9. Immutable semantic plan

Compilation is deterministic:

\[
P = Compile(Rules, Schema, CompilerVersion)
\]

A plan contains:

```text
Plan
├── format version
├── compiler version
├── schema digest
├── ruleset digest
├── canonical ordered transformations
├── derived read/write sets
├── dependency edges and stable execution levels
├── preconditions and postconditions
├── execution-level invariants
├── backend requirements
└── plan digest
```

The plan is serializable, inspectable, backend-independent, immutable, and content-addressed. Canonical ordering is part of the format contract.

### 9.1 Static validation

Compilation rejects:

- Unknown entities, relations, fields, rules, or operators.
- Type-incompatible comparisons, assignments, or aggregates.
- Undeclared or mismatched reads and writes.
- Missing dependencies.
- Dependency cycles.
- Write/write conflicts without explicit ordering.
- Invalid rule composition.
- Operators that cannot satisfy the requested provenance policy.

A failed compilation does not produce a `PlanID`.

## 10. Transformation and patch model

The core transition is:

\[
(S_{n+1}, \Delta_n) = T_n(S_n, C)
\]

A transformer never mutates shared state. It reads an immutable state and pinned world, then proposes a structural patch.

```go
type Transformer interface {
	Describe() TransformSpec
	Propose(ctx context.Context, state State, world World) (Patch, error)
}

type TransformSpec struct {
	ID             RuleID
	Reads          []FieldPath
	Writes         []FieldPath
	Preconditions  []Invariant
	Postconditions []Invariant
}
```

The closed structural operation set begins with:

- `Insert(entity)`
- `Delete(entity, beforeImage)`
- `Update(entity, beforeFields, afterFields)`
- `Merge(inputs, beforeImages, output)`
- `Split(input, beforeImage, outputs)`
- `Relate(from, relation, to)`
- `Unrelate(from, relation, to)`

Before-images may be embedded or referenced by digest. Physical storage may compress and deduplicate them without changing their semantic meaning.

Execution of one transformation is atomic:

```text
read immutable state
        │
        ▼
evaluate preconditions
        │
        ▼
propose structural patch
        │
        ▼
validate operation legality
        │
        ▼
apply to isolated candidate
        │
        ▼
validate postconditions
        │
        ▼
commit next state + semantic journal entry
```

If a check fails, the transition does not commit. A separate immutable failure report captures the rejected proposal, safe entity references, invariant results, and evidence digests. Accepted semantic journals contain only committed transitions.

Rollback of a published result normally restores an earlier immutable publication pointer. Inverse patch application exists for replay, diagnosis, and branch construction; it is not the primary production rollback mechanism.

## 11. Dependency planning and execution

The compiler builds dependency edges from semantic access:

- A write followed by a read establishes ordering.
- A write/write conflict requires an explicit ordering edge or fails compilation.
- Cycles fail compilation.
- Independent transforms form stable execution levels.

The reference executor initially processes rules sequentially in stable lexical order within each level. This makes determinism easy to establish. A future executor may run independent transforms concurrently if it produces the same state, journal, and invariant digests.

## 12. Dynamic invariants

A valid plan does not imply a valid execution. Dynamic validation operates at three scopes:

### Operation invariants

- Referenced inputs exist in the predecessor state.
- Before-images match the predecessor state.
- New identities do not collide.
- Relation endpoints exist.
- Structural cardinality is valid.

### Rule invariants

- A team merge uses a coherent aggregation anchor.
- Driving time does not exceed elapsed time.
- A split or merge produces the required number and type of entities.
- Derived values satisfy customer-specific semantic constraints.

### Execution invariants

- Referential integrity holds across the complete candidate graph.
- Required engine entities and fields exist.
- Fanout and cardinality remain within declared bounds.
- Every protected customer and engine invariant passes.

Protected invariants are unwaivable in the normal publication path. Soft quality policies may permit a separately authorized and journaled approval.

## 13. Semantic provenance

Provenance is a requested execution policy:

| Mode | Records | Use |
|---|---|---|
| `summary` | Rules fired, affected entity references, counts, and state digests | High-volume observation; insufficient for publication |
| `changes` | Structural operations, before/after values or image references, evidence digests, and invariant results | Minimum publishable mode |
| `full` | `changes` plus every intermediate state manifest and evaluation detail | Shadow executions, UAT, incidents, and backend certification |

The journal describes what occurred semantically:

```text
Merge(driver:A, driver:B → team:AB)
```

It does not describe backend mechanics such as a SQL statement number or warehouse row range.

Fusion is allowed only when the backend can still emit the required semantic provenance. A backend that produces the final relation but cannot produce the required journal is not certified for that execution policy.

## 14. Semantic-run, execution, and promotion lifecycle

```mermaid
flowchart TD
    R["Ruleset + schema"] --> C["Compile and statically validate"]
    C -->|"invalid"| CR["Reject; no PlanID"]
    C -->|"valid"| P["Immutable PlanID"]
    P --> I["Pin input and world; create InputID"]
    I --> SR["Create SemanticRunID"]
    SR --> X["Select executor and provenance; create ExecutionID"]
    X --> E["Execute candidate"]
    E -->|"execution failure"| EF["Non-publishable execution"]
    E --> S["Candidate state + semantic journal"]
    S --> V["Dynamic invariants"]
    V -->|"fail"| VF["Non-publishable execution"]
    V -->|"pass"| B["Paired baseline/candidate replay"]
    B --> G["Promotion gate"]
    G -->|"fail"| GF["Non-publishable execution"]
    G -->|"pass"| A["Publishable immutable artifact"]
    A --> PUB["Explicit atomic publication"]
```

Execution, gate, and publication state are independent:

```go
type ExecutionStatus string  // pending, running, succeeded, failed
type GateVerdict string      // not_evaluated, pass, fail
type PublicationStatus string // unpublished, published, superseded
```

A successfully executed pipeline can remain non-publishable.

### 14.1 Promotion requirements

The gate requires:

- Successful static plan validation.
- Successful execution with at least `changes` provenance.
- All protected dynamic invariants passed.
- Pinned input, world, schema, ruleset, compiler, semantic-run, and execution identities.
- Baseline and candidate executions over the same replay corpus.
- No protected metric regression.
- Internally consistent output and journal digests.
- A backend certified against the reference executor.

Publication is a compare-and-swap update of a versioned pointer to an immutable candidate. It never reruns transformations. A conflicting publication fails rather than silently overwriting a newer result.

## 15. Package architecture

Dependencies point inward toward deterministic domain packages:

```text
cmd/maiden-lane
        │
        ▼
internal/app
        │
        ├── compile ── rules
        ├── execute ── model ── invariant
        ├── provenance
        └── promotion
                │
                ▼
              ports
                │
                ▼
adapters/aws | postgres | stochflow
```

Proposed responsibilities:

```text
cmd/maiden-lane/          serve and worker entry points
internal/model/           State, Entity, Relation, Patch, Operation, Digest
internal/canonical/       versioned canonical byte encodings owned by Maiden Lane
internal/rules/           closed rule-language AST and parser
internal/compile/         static validation and canonical Plan construction
internal/execute/         deterministic Go executor and patch application
internal/invariant/       operation, rule, and execution validators
internal/provenance/      semantic journal and evidence records
internal/promotion/       comparisons, gate policy, publication decisions
internal/app/             application use cases and execution lifecycle
internal/ports/           storage, hashing, comparison, and dispatch interfaces
internal/httpapi/         chi routes, handlers, middleware, and wire DTOs
internal/adapters/
    postgres/             metadata, leases, outbox, and publication pointer
    s3/                   immutable artifact storage
    batch/                AWS Batch submission
    stochflow/            byte-level hashing and comparison implementations
api/openapi.yaml          authoritative HTTP contract
```

Only adapters may import AWS SDKs, PostgreSQL drivers, or stochflow. Semantic packages perform no I/O and do not observe wall-clock time, randomness, environment variables, or global mutable state.

## 16. HTTP API

The API uses chi and begins with a deliberately small surface:

```text
POST /v1/plans
GET  /v1/plans/{planID}

GET  /v1/semantic-runs/{semanticRunID}

POST /v1/executions
GET  /v1/executions/{executionID}
GET  /v1/executions/{executionID}/journal
GET  /v1/executions/{executionID}/violations

POST /v1/comparisons
GET  /v1/comparisons/{comparisonID}

POST /v1/publications
GET  /v1/publications/{customerID}

GET  /healthz
GET  /readyz
```

Behavioral rules:

- Plan creation compiles referenced schema and ruleset artifacts. Invalid input returns a validation problem without a `PlanID`.
- Execution creation accepts `PlanID`, pinned input/world references, executor identity, and provenance policy, then returns `202 Accepted` with both `SemanticRunID` and `ExecutionID`.
- Repeating the same semantic request with the same executor and provenance policy returns the same `ExecutionID`; changing only the executor or provenance policy preserves `SemanticRunID` and creates a different `ExecutionID`.
- Journal reads use cursor pagination.
- Publication accepts only a gate-passing execution and requires the expected current publication version.
- All errors use RFC 9457 `application/problem+json`.
- Every operation is tenant/customer scoped; possession of an identifier does not authorize access.

The OpenAPI document is authoritative for wire contracts. HTTP handlers translate wire DTOs into application commands and contain no transformation semantics.

## 17. AWS deployment architecture

The preferred AWS shape maps the useful parts of the Cloud Run service/jobs model onto ECS and AWS Batch:

```mermaid
flowchart LR
    C["API client"] --> ALB["Application Load Balancer"]
    ALB --> API["Maiden Lane API<br/>ECS service on Fargate"]
    API --> PG["Aurora or RDS PostgreSQL<br/>control plane"]
    API --> S3["S3<br/>immutable artifacts"]
    API --> OUT["Transactional outbox"]
    OUT --> D["Idempotent dispatcher"]
    D --> BQ["AWS Batch job queue"]
    BQ --> W["Fargate worker"]
    W --> PG
    W --> S3
    BQ --> EB["EventBridge state events"]
    EB --> REC["Idempotent status reconciler"]
    REC --> PG
    ECR["ECR<br/>versioned image"] --> API
    ECR --> W
```

The API and worker are two modes of one image:

```text
maiden-lane serve
maiden-lane worker --execution-id <ExecutionID>
```

The API performs cheap compilation and read operations synchronously. Execution and comparison are asynchronous Batch jobs.

### 17.1 Reliable dispatch

The API commits an execution record and dispatch request in one PostgreSQL transaction. An idempotent dispatcher submits the outbox entry to AWS Batch. A worker receives only `ExecutionID`, obtains an execution lease, records its `AttemptID`, and loads pinned artifacts from PostgreSQL and S3. Duplicate jobs may consume infrastructure but cannot execute the same `ExecutionID` concurrently or publish conflicting results. A retry receives a new `AttemptID` only after the prior lease has ended or expired.

Application failures such as invalid plans and invariant violations are permanent. Only classified infrastructure failures receive bounded retries.

Batch status is operational evidence, not the durable system of record. EventBridge state-change handling is duplicate-safe and monotonic. PostgreSQL and S3 retain the authoritative semantic-run, execution, and artifact history.

### 17.2 Service choices

- ECS on Fargate behind an Application Load Balancer is preferred for the always-available API and consistent VPC/IAM/container operations.
- AWS Batch with a Fargate compute environment is preferred for queued, isolated, long-running transformation jobs.
- App Runner remains a possible API deployment adapter but does not replace the Batch job lifecycle.
- Step Functions is not required initially because the semantic plan is already the workflow. It is reserved for orchestration across independent external services.
- If jobs exceed Fargate capacity limits or concurrency economics, the Batch compute environment can move to EC2 without changing the worker contract.

## 18. Persistence boundaries

PostgreSQL logically stores the following control-plane record groups. These
names describe ownership and relationships; they do not commit this design
phase to one table per item or to a final physical schema:

```text
schema_versions
rulesets
plans
semantic_runs
executions
execution_attempts
invariant_results
comparisons
publication_pointers
dispatch_outbox
```

S3 stores content-addressed data-plane artifacts:

```text
inputs/sha256/<digest>
worlds/sha256/<digest>
plans/sha256/<digest>
journals/<execution-id>/<segment-digest>
outputs/sha256/<digest>
comparisons/sha256/<digest>
```

PostgreSQL contains artifact digests and locations rather than duplicate payload bodies. S3 artifacts are immutable. Existing bytes under a digest must exactly match newly supplied bytes; any mismatch is a fatal integrity error.

Execution transitions use optimistic concurrency and an explicit legal state machine. Journal segments are append-only. A final journal manifest becomes visible only after all referenced segments exist. Publication changes a versioned PostgreSQL pointer in one transaction, so readers cannot observe partially written candidate data.

## 19. Failure model

Failures are classified by whether retry can change the outcome:

| Class | Retry | Publication |
|---|---:|---:|
| Invalid schema, ruleset, or plan | No | Impossible |
| Protected invariant violation | No | Impossible |
| Optimistic concurrency conflict | Caller may retry | Unchanged |
| Transient AWS, PostgreSQL, or network failure | Automatic and bounded | Blocked pending success |
| Artifact digest mismatch | No; operator alert | Impossible |
| Replay or backend divergence | No; operator alert | Impossible |
| Worker timeout or cancellation | Explicit new attempt | Impossible |

Retries always use the original `SemanticRunID`, `ExecutionID`, executor identity, and provenance policy. API problems expose stable codes and bounded safe metadata, never entity values, customer payloads, generated SQL, or journal bodies.

## 20. Security and privacy

- Every port method receives explicit tenant and customer scope.
- Workers run in private subnets with narrowly scoped task roles.
- S3 and database storage use KMS-backed encryption.
- Secrets are obtained from Secrets Manager, not embedded in images or job arguments.
- API identities can submit and inspect executions but cannot overwrite artifacts directly.
- Only the publication use case can update the publication pointer.
- Full provenance access requires separate authorization and emits an audit event.
- Logs, ordinary audit events, traces, and metrics contain metadata only.
- Raw customer data exists only in authorized encrypted artifacts and deliberately requested provenance views.

## 21. Observability

OpenTelemetry signals are exported to the shop's AWS observability stack. Traces may use `SemanticRunID`, `ExecutionID`, `PlanID`, and `AttemptID` for diagnosis. Metrics use bounded labels and never use customer IDs, entity IDs, semantic-run IDs, or execution IDs as dimensions.

Primary signals are:

- API and compilation latency.
- Batch queue delay and worker duration.
- Entities read and structural operations emitted.
- Journal size by provenance mode.
- Invariant failures by stable invariant code.
- Retry, crash-resume, and replay-divergence counts.
- Promotion gate verdict counts.
- Publication and rollback counts.

## 22. Verification strategy

Verification targets the system's promised properties:

1. Patch property tests prove deterministic application and `Undo(Apply(S, delta), delta) = S`.
2. Compiler tests prove shuffled authoring order yields the same plan and that cycles, undeclared access, type errors, and write conflicts fail closed.
3. Golden incident fixtures encode team-HOS merge incoherence, timezone-catalog drift, split-load cardinality, and other real mapper defects.
4. Determinism tests randomize map insertion order and require identical state, journal, and invariant digests.
5. Crash tests interrupt each journal boundary and require resume to match uninterrupted execution.
6. Gate tests prove no protected invariant failure can be published.
7. Concurrency tests prove duplicate attempts and competing publication requests cannot corrupt state.
8. API contract tests cover tenant isolation, semantic idempotency, pagination, and safe problem bodies.
9. PostgreSQL, S3, and Batch adapters receive integration tests in service-compatible environments.
10. Every future backend passes differential certification against the Go executor: final state digest, semantic journal digest, and invariant results must match.

## 23. Current deliverable boundary

This phase produces:

- This high-level architecture and semantic model.
- An illustrative Go and chi implementation sketch in Markdown.
- Example interfaces and types sufficient to test whether the boundaries are coherent.
- No commitment to a production vertical slice, infrastructure rollout, DSL format, storage schema, or first customer transformation.

The implementation sketch may use team HOS as an explanatory example, but it does not establish team HOS as the first production milestone.

The first executable milestone will be selected in a later implementation plan after this design, mapper constraints, source data, and the operational environment have been reviewed.

## 24. References

- [Stochflow local architecture](../../../../stochflow/README.md)
- [Choosing an AWS container service](https://docs.aws.amazon.com/decision-guides/latest/containers-on-aws-how-to-choose/choosing-aws-container-service.html)
- [AWS App Runner overview](https://docs.aws.amazon.com/apprunner/latest/dg/what-is-apprunner.html)
- [AWS Batch on Fargate](https://docs.aws.amazon.com/batch/latest/userguide/fargate.html)
- [When to use Fargate for AWS Batch](https://docs.aws.amazon.com/batch/latest/userguide/when-to-use-fargate.html)
- [AWS Batch automated retries](https://docs.aws.amazon.com/batch/latest/userguide/job_retries.html)
- [AWS Batch job states](https://docs.aws.amazon.com/batch/latest/userguide/job_states.html)
- [AWS Batch EventBridge stream](https://docs.aws.amazon.com/batch/latest/userguide/cloudwatch_event_stream.html)
- [Amazon ECS service load balancing](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-load-balancing.html)
- [Step Functions integration with ECS and Fargate](https://docs.aws.amazon.com/step-functions/latest/dg/connect-ecs.html)
