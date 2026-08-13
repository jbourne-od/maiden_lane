# Progressive Semantic Spine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the smallest pure in-memory Maiden Lane semantic spine that executes the ratified two-transition sanitized team-HOS fixture, seals C1 and C2, assesses CM and optimizer readiness, preserves C1 when T2 rejects, and emits non-authoritative operational telemetry.

**Architecture:** A compact `internal/semantic` package owns typed immutable values, closed declarations, deterministic compilation, atomic patches, the reference executor, accepted journals, checkpoint sealing, profile compilation, readiness assessment, and artifact identities. `internal/fixtures/teamhos` contains only the ratified sanitized declarations and data. `internal/app` coordinates the lifecycle and owns a closed observation contract; `internal/observability` implements that contract with the existing OTel runtime without entering semantic dependencies.

**Tech Stack:** Go 1.26.5, Go standard library including `crypto/sha256`, the repository's existing OpenTelemetry Go v1.45.0 stack, and existing test/static-analysis tools. Add no runtime dependency.

## Global Constraints

- The authority order is [Inviolates](../../../Inviolates.md), [High-Level Design](../specs/2026-08-11-maiden-lane-high-level-design.md), explicit contracts/tests, the [ratified walking-skeleton design](../specs/2026-08-13-progressive-semantic-spine-design.md), the current Implementation Guide, and existing code.
- Read `AGENTS.md` and any more-specific `AGENTS.md` before every task; inspect `git status` and preserve unrelated work.
- Use RED -> GREEN -> REFACTOR for every behavioral step. A failure must be observed for the intended reason before implementation.
- Do not commit, push, or open a PR unless the owner explicitly authorizes it. The review checkpoint at the end of each task replaces the writing-plans skill's normal commit step.
- `internal/semantic` is pure: no OTel, logging, HTTP, AWS, persistence, stochflow, environment, filesystem, network, wall clock, randomness, mutable globals, or unstable map iteration.
- Certified meaning is closed and typed. Do not add callbacks, arbitrary functions, reflection-driven semantics, dynamic field names, a generic DSL, arbitrary SQL, or a catch-all protected code.
- The plan contains exactly `form_team.v1` then `aggregate_team_hos.v1`, with checkpoints `team_formed.v1` and `team_hos_aggregated.v1`.
- T1 reads no HOS fields. T2 owns source-tuple completeness, non-negative durations, `driving <= elapsed`, anchor equality, componentwise-max reduction, emitted-value checks, and aggregate validity.
- `form_team.v1` emits one atomic `Insert + Relate + Relate` patch and preserves both drivers. `aggregate_team_hos.v1` emits one atomic `Update` with an explicit absent-field before-image.
- Accepted journals contain committed transitions only. Rejections and integrity failures remain separate immutable reports.
- Canonical encodings are narrow, artifact-specific, binary, domain-tagged, and versioned. Do not hash JSON or ordinary Go map order.
- Semantic IDs use fixed SHA-256 v1 canonical bytes. `ExecutionID` and `AttemptID` never enter checkpoint/journal canonical identity.
- C1 is valid and sealable with no team aggregate fields; CM is ready and optimizer is `needs_input`. A passing T2 seals C2 and makes both ready.
- Anchor mismatch is `HOS_ANCHOR_MISMATCH` before patch materialization. It leaves C1 and the T1 accepted prefix byte-identical and creates no C2.
- Readiness scope is explicit `all team entities` with universal aggregation and documented vacuous-empty semantics. Profile ordering is proved from normalized implication, not names.
- `Run` returns `(SpineResult, error)`: semantic rejection has a typed failed result and nil Go error; machinery failure retains the last independently verified dependency-closed prefix with non-nil Go error.
- Observers are no-error and non-authoritative. Trace IDs are limited to `PlanID`, `SemanticRunID`, and `ExecutionID`; metrics receive no identities or digests.
- Do not add HTTP/API/CLI surfaces, persistence, publication, promotion, comparison, AWS orchestration, parallelism, SQL/dbt, full patch algebra, or production HOS policy.
- Update current-state documentation only after corresponding code exists. Do not edit the HLD, Inviolates, progressive amendment, glossary, or OpenAPI in this slice.

---

## Planned Repository Shape

```text
internal/semantic/doc.go                 package boundary and invariants
internal/semantic/value.go               named IDs, codes, typed scalar values
internal/semantic/canonical.go           narrow binary encoder and SHA-256 v1
internal/semantic/state.go               immutable schema/entity/relation/state
internal/semantic/declaration.go         closed rule/profile source declarations
internal/semantic/compile.go             deterministic plan/profile compilation
internal/semantic/patch.go               Insert/Relate/Update apply and inverse
internal/semantic/execute.go             closed reference transition executor
internal/semantic/journal.go             accepted entries and prefix identities
internal/semantic/checkpoint.go          checkpoint sealing and manifests
internal/semantic/profile.go             compiled readiness assessment
internal/semantic/*_test.go              focused unit/property/golden-vector tests
internal/fixtures/teamhos/fixture.go      ratified declarations and two S0 variants
internal/fixtures/teamhos/fixture_test.go fixture isolation and determinism tests
internal/app/observation.go               closed app-owned observation carrier
internal/app/result.go                    immutable dependency-closed SpineResult
internal/app/errors.go                    safe typed machinery errors
internal/app/progressive.go               Run orchestration and verified frontier
internal/app/observation_test.go          carrier closure and noninterference tests
internal/app/progressive_test.go          golden lifecycle and machinery failures
internal/observability/semantic.go        app.Observer OTel implementation
internal/observability/semantic_test.go   span/metric/privacy/noninterference tests
internal/observability/runtime.go         semantic instrument registration
internal/observability/runtime_test.go    runtime registration/lifecycle coverage
README.md                                 implemented semantic-slice summary
METRICS.md                                exact semantic metric contract
ERRORS.md                                 owned machinery-error contract
docs/implementation/implementation-guide.md current package/runtime description
```

No semantic file should become a miscellaneous home. If an implementation file approaches 350 lines excluding comments, split it by the responsibilities above before adding another concern.

## Fixed Cross-Task Interfaces

These names lock task boundaries. Minor unexported helper changes are allowed; exported or cross-package changes require updating this plan and all dependent tasks before proceeding.

```go
package semantic

type Digest string // Generic SHA-256 value only where an artifact kind is itself tagged.
type InputLineageID string
type EntityID string
type EntityKind string
type FieldName string
type RelationKind string
type RuleID string
type CheckpointKey string
type SchemaDigest string
type RulesetDigest string
type CompilationInputDigest string
type StateDigest string
type PlanID string
type InputID string
type WorldID string
type SemanticRunID string
type ProvenancePolicyID string
type ExecutorIdentity string
type ExecutionID string
type PatchDigest string
type JournalEntryDigest string
type JournalPrefixDigest string
type InvariantResultDigest string
type CheckpointID string
type CheckpointArtifactID string
type CheckpointArtifactDigest string
type ProfileID string
type AssessmentID string
type AssessmentDigest string
type CompilationFailureDigest string
type FailureReportDigest string
type CompilerSemanticsVersion string

type ValueKind uint8
const (
	ValueString ValueKind = iota + 1
	ValueAtom
	ValueInt64
)

func NewStringValue(string) (Value, error)
func NewAtomValue(string) (Value, error)
func NewInt64Value(int64) Value

type CompileRequest struct {
	Schema                   SchemaDeclaration
	Rules                    RulesetDeclaration
	Profiles                 []ProfileDeclaration
	CompilerSemanticsVersion CompilerSemanticsVersion
}

func Compile(CompileRequest) (Compilation, error)

type RunBindingRequest struct {
	Plan             Plan
	InitialState     State
	World            World
	ExecutorIdentity ExecutorIdentity
	Policy           ProvenancePolicy
}

func BindRun(RunBindingRequest) (RunBinding, error)
func ExecuteTransition(RunBinding, RuleID, State, Journal) (TransitionOutcome, error)
func Seal(SealRequest) (SealOutcome, error)
func Assess(AssessmentRequest) (AssessmentOutcome, error)
```

All returned semantic values expose read-only getters that clone slices and byte buffers. Constructors and compilers clone caller-owned input. No getter exposes an internal map.

```go
package teamhos

type Variant uint8
const (
	Passing Variant = iota + 1
	AnchorMismatch
)

type Inputs struct {
	Compilation     semantic.CompileRequest
	InitialState    semantic.State
	World           semantic.World
	ExecutorIdentity semantic.ExecutorIdentity
	Policy          semantic.ProvenancePolicy
}

func New(Variant) (Inputs, error)
```

```go
package app

type Request struct {
	Compilation      semantic.CompileRequest
	InitialState     semantic.State
	World            semantic.World
	ExecutorIdentity semantic.ExecutorIdentity
	Policy           semantic.ProvenancePolicy
}

type Observer interface {
	BeginPhase(context.Context, PhaseObservation)
	EndPhase(context.Context, PhaseObservation)
}

func Run(context.Context, Request, Observer) (SpineResult, error)
func DiscardObserver() Observer

func (r SpineResult) ExecutionStatus() (semantic.ExecutionStatus, bool)
func (r SpineResult) CompilationFailure() (semantic.CompilationFailure, bool)
func (r SpineResult) SemanticFailure() (semantic.FailureReport, bool)
```

`internal/observability` implements `app.Observer`; `internal/app` never imports `internal/observability`.

The implementation uses a package-owned `contentHasher` port whose only production implementation is the fixed standard-library `sha256.v1` adapter. Callers cannot select a different semantic hashing algorithm. Artifact APIs return the distinct digest/identity types above; they must not collapse `CheckpointArtifactID` into `CheckpointArtifactDigest` or `AssessmentID` into `AssessmentDigest` merely because all render as `sha256:<hex>`.

## Ratified Closed Vocabularies

Implement these spellings exactly as distinct named code types. Human prose never substitutes for them.

- Failure kinds: `INVALID_PLAN`, `PROTECTED_INVARIANT_FAILED`, `ARTIFACT_INTEGRITY_FAILED`.
- Operation invariant codes: `OP_ENTITY_IDENTITY_COLLISION`, `OP_UPDATE_TARGET_NOT_FOUND`, `OP_BEFORE_IMAGE_MISMATCH`, `OP_RELATION_ALREADY_PRESENT`, `OP_RELATION_ENDPOINT_MISSING`.
- Rule invariant codes: `DECLARED_SOURCE_NOT_FOUND`, `DECLARED_SOURCE_KIND_INVALID`, `TEAM_ASSIGNMENT_KEY_INVALID`, `TEAM_ASSIGNMENT_KEY_MISMATCH`, `TEAM_MEMBER_CARDINALITY_INVALID`, `HOS_TUPLE_INCOMPLETE`, `HOS_DURATION_INVALID`, `HOS_ANCHOR_MISMATCH`, `HOS_AGGREGATE_INVALID`.
- Requirement codes: `TEAM_ASSIGNMENT_KEY_REQUIRED`, `TEAM_AGGREGATION_ANCHOR_REQUIRED`, `TEAM_ELAPSED_DURATION_REQUIRED`, `TEAM_DRIVING_DURATION_REQUIRED`.
- Compilation diagnostics: `UNKNOWN_FIELD`, `UNSUPPORTED_OPERATOR`, `DECLARED_ACCESS_MISMATCH`, `WRITE_CONFLICT_UNRESOLVED`, `DEPENDENCY_CYCLE`, `PROFILE_ORDER_UNPROVABLE`.
- Integrity codes: `ARTIFACT_DIGEST_MISMATCH`, `ARTIFACT_LINK_INCONSISTENT`, `ASSESSMENT_IDENTITY_CONFLICT`, `REPLAY_DIVERGENCE`.

`OperationInvariantCode`, `InvariantCode`, `RequirementCode`, `CompilationDiagnosticCode`, `IntegrityCode`, `ArtifactKind`, `FailureKind`, `ReadinessVerdict`, `SpineStatus`, and `ExecutionStatus` remain separate Go types. There is no catch-all protected code.

---

### Task 1: Typed Values, Immutable State, and Canonical Leaf Identity

**Files:**
- Create: `internal/semantic/doc.go`
- Create: `internal/semantic/value.go`
- Create: `internal/semantic/canonical.go`
- Create: `internal/semantic/state.go`
- Create: `internal/semantic/value_test.go`
- Create: `internal/semantic/state_test.go`
- Create: `internal/semantic/canonical_test.go`

**Interfaces:**
- Consumes: standard-library `bytes`, `crypto/sha256`, `encoding/binary`, `encoding/hex`, `slices`, `sort`, `strings`, `unicode/utf8`.
- Produces: immutable typed values, schemas, entities, relations, states, worlds, source IDs, state/world digests, canonical byte helpers used by every later task.

- [ ] **Step 1: Write failing typed-value and schema-boundary tests**

Cover exact UTF-8 behavior, absent versus present, optional driver HOS fields, unknown fields, and duplicate declarations:

```go
func TestNewStateAllowsMissingOptionalDriverHOS(t *testing.T) {
	schema := fixtureSchemaForStateTests(t)
	driver := mustEntity(t, "driver", "driver-id", map[FieldName]Value{
		"assignment_key": mustString(t, "assignment-X"),
	})
	state, err := NewState(schema, mustLineage(t), []Entity{driver}, nil)
	if err != nil { t.Fatalf("NewState: %v", err) }
	if _, ok := state.Entity(driver.Ref()); !ok { t.Fatal("driver missing") }
}

func TestNewStateRejectsWrongTypedField(t *testing.T) {
	_, err := NewState(fixtureSchemaForStateTests(t), mustLineage(t), []Entity{
		mustEntity(t, "driver", "driver-id", map[FieldName]Value{
			"hos_elapsed_hours": mustString(t, "ten"),
		}),
	}, nil)
	if err == nil { t.Fatal("NewState accepted wrong field type") }
}
```

- [ ] **Step 2: Run the leaf tests and observe RED**

Run: `go test ./internal/semantic -run 'TestNew(State|String|Atom)' -count=1`

Expected: compilation fails because the semantic package and constructors do not exist.

- [ ] **Step 3: Implement named types, typed values, schema validation, and immutable state**

Use private fields and cloning constructors. `SchemaDeclaration` contains sorted `EntityDeclaration` and `RelationDeclaration` values; each relation declaration fixes its relation kind and permitted from/to entity kinds. Fields declare `RequiredAtConstruction` independently of their value type. The ratified fixture will mark driver HOS fields optional and T1/T2 obligations will enforce use-specific presence later.

```go
type EntityRef struct { Kind EntityKind; ID EntityID }
type Relation struct { Kind RelationKind; From, To EntityRef }

func NewSchema([]EntityDeclaration, []RelationDeclaration) (Schema, error)
func NewEntity(EntityRef, map[FieldName]Value) (Entity, error)
func NewState(Schema, InputLineageID, []Entity, []Relation) (State, error)
func (s State) Entity(EntityRef) (Entity, bool)
func (s State) Entities() []Entity
func (s State) Relations() []Relation
func (s State) CanonicalBytes() []byte
func (s State) Digest() StateDigest
```

State construction enforces representation and declared types only. It must not enforce assignment equality, HOS tuple completeness, duration inequality, team membership, or consumer completeness.

Add RED cases showing the fixture's `member` relation is declared as `team -> driver`, and that an unknown relation kind or a relation with reversed/undeclared endpoint kinds is rejected by schema validation/state construction. Task 2 must also reject a rule whose declared relation traversal is absent from this schema.

- [ ] **Step 4: Write failing canonical-order, immutability, and lineage tests**

```go
func TestStateCanonicalBytesIgnoreMapAndEntityInsertionOrder(t *testing.T) {
	a := stateWithOrder(t, []string{"A", "B"}, []string{"assignment_key", "hos_anchor"})
	b := stateWithOrder(t, []string{"B", "A"}, []string{"hos_anchor", "assignment_key"})
	if !bytes.Equal(a.CanonicalBytes(), b.CanonicalBytes()) || a.Digest() != b.Digest() {
		t.Fatal("semantic order changed state identity")
	}
}

func TestStateDefensivelyCopiesConstructorInputsAndGetterResults(t *testing.T) {
	fields := map[FieldName]Value{"assignment_key": mustString(t, "X")}
	state := stateFromMutableFields(t, fields)
	before := state.CanonicalBytes()
	fields["assignment_key"] = mustString(t, "mutated")
	got := state.Entities(); got[0] = Entity{}
	if !bytes.Equal(before, state.CanonicalBytes()) { t.Fatal("state mutated") }
}

func TestObservationChangePreservesLineageAndSourceEntityID(t *testing.T) {
	lineage := mustLineage(t)
	a := SourceEntityID(lineage, "driver", "A")
	if a != SourceEntityID(lineage, "driver", "A") { t.Fatal("source ID drift") }
	if a == SourceEntityID(mustOtherLineage(t), "driver", "A") { t.Fatal("lineage omitted") }
}
```

- [ ] **Step 5: Implement the artifact-specific binary writer and leaf identities**

Implement an unexported canonical encoder with explicit `tag`, `uint64`, `int64`, `optional`, `string`, and `digest` methods. Each top-level artifact method writes its own domain tag and version. Strings use exact validated UTF-8 bytes with no normalization; sets/maps become sorted typed sequences. The only hasher is the package-owned `contentHasher` port with an unexported standard-library `sha256V1Hasher` implementation over already-canonical bytes.

Implement:

```go
func NewInputLineageID(namespace, rootKey string) (InputLineageID, error)
func SourceEntityID(InputLineageID, EntityKind, string) EntityID
func NewWorld([]WorldReference) (World, error)
func (w World) ID() WorldID
```

The empty world is a real versioned zero-count artifact.

- [ ] **Step 6: Freeze leaf canonical vectors and verify GREEN**

Add literal expected canonical hex and `sha256:<lowercase>` strings for the lineage root, one source entity ID input tuple, the empty world, and one state. Calculate the expected bytes independently from the encoding table with a one-off test-only/manual construction that does not call the production encoder, hard-code the result, and have a reviewer compare every field against the encoding specification. Do not generate the expected values inside the golden test. This literal-byte-plus-digest rule applies to every later v1 artifact vector as well.

Run:

```bash
gofmt -w internal/semantic/*.go
go test ./internal/semantic -run 'Test(State|World|InputLineage|SourceEntity|Canonical)' -count=1
git diff --check
```

Expected: targeted tests pass; `go.mod` and `go.sum` are unchanged.

- [ ] **Step 7: Review checkpoint**

Inspect `git diff -- internal/semantic`. Verify no `encoding/json`, reflection, time, environment, I/O, OTel, mutable global, UUID, or customer-specific HOS predicate exists. Do not commit.

### Task 2: Closed Declarations and Deterministic Compilation

**Files:**
- Create: `internal/semantic/declaration.go`
- Create: `internal/semantic/compile.go`
- Create: `internal/semantic/compile_test.go`
- Modify: `internal/semantic/canonical.go`

**Interfaces:**
- Consumes: Task 1 schema/value/canonical types.
- Produces: `CompileRequest`, `Compilation`, immutable `Plan`, compiled profiles, `CompilationInputDigest`, `PlanID`, `ProfileID`, derived accesses, invariant declarations, dependency order, and typed compilation diagnostics.

- [ ] **Step 1: Write failing closed-union and derived-access tests**

Declare the two closed variants and prove that T1 cannot read HOS:

```go
func TestCompileDerivesExactTeamHOSAccess(t *testing.T) {
	result, err := Compile(compileFixtureRequest(t, false))
	if err != nil { t.Fatalf("Compile: %v", err) }
	plan, ok := result.Plan(); if !ok { t.Fatal("no plan") }
	form := plan.MustTransformation("form_team.v1")
	if form.Reads("driver.hos_anchor") || form.Reads("driver.hos_elapsed_hours") {
		t.Fatalf("T1 reads suffix-only HOS: %v", form.ReadSet())
	}
	aggregate := plan.MustTransformation("aggregate_team_hos.v1")
	for _, path := range []FieldPath{"driver.hos_anchor", "driver.hos_elapsed_hours", "driver.hos_driving_hours"} {
		if !aggregate.Reads(path) { t.Fatalf("T2 does not read %s", path) }
	}
}
```

Add table cases for unknown fields, unsupported operator tags, multiple active union variants, missing typed output key, declared/derived access disagreement, unresolved output-slot reference, unresolved write/write conflict, and dependency cycle. Assert the exact `CompilationDiagnosticCode` ordering and absence of `PlanID`/`ProfileID` on failure. Use `WRITE_CONFLICT_UNRESOLVED` only when overlapping writers have no dependency path ordering either writer before the other.

- [ ] **Step 2: Run compiler tests and observe RED**

Run: `go test ./internal/semantic -run 'TestCompile' -count=1`

Expected: compilation fails because declaration and compiler types do not exist.

- [ ] **Step 3: Implement the closed declaration model**

Use one tagged union with exactly one payload:

```go
type OperatorKind uint8
const (
	OperatorFormRelatedEntity OperatorKind = iota + 1
	OperatorAggregateRelatedFields
)

type TransformationDeclaration struct {
	ID        RuleID
	DeclaredReads, DeclaredWrites []FieldPath
	Form      *FormRelatedEntityDeclaration
	Aggregate *AggregateRelatedFieldsDeclaration
}
```

`AggregateRelatedFieldsDeclaration` admits only the ratified v1 tags: `CompleteTuple`, `NonNegativeInt`, `EqualFieldAcrossSources`, `LessOrEqualFields`, `ReduceInt64Max`, and derived emitted-value checks. It contains field references, never callbacks.

Profiles use explicit `AllEntitiesOfKind` scope, `AllSelected` aggregation, and `FieldPresent` atoms with stable `RequirementCode` values.

- [ ] **Step 4: Implement deterministic compilation and failure identity**

`Compile` must:

1. canonicalize the complete request and create `CompilationInputDigest`;
2. validate schema/rules/profiles and collect typed diagnostics;
3. derive entity/relation/field accesses and invariant declarations;
4. reject authored/derived disagreement;
5. reject overlapping writers with `WRITE_CONFLICT_UNRESOLVED` unless the dependency graph contains a path ordering one before the other;
6. topologically order dependencies using a canonical semantic-key priority queue;
7. prove profile implications only for identical scope/aggregation/atom semantics and requirement-set containment; and
8. return either an immutable plan plus compiled profiles or one canonical compilation failure, never a partial accepted program.

```go
type Compilation struct { /* private */ }
func (c Compilation) Plan() (Plan, bool)
func (c Compilation) Profiles() []CompiledProfile
func (c Compilation) Failure() (CompilationFailure, bool)
func (p Plan) ID() PlanID
func (p Plan) Transformations() []CompiledTransformation
func (p Plan) Checkpoints() []CheckpointDeclaration
```

- [ ] **Step 5: Write shuffled-declaration and sensitivity tests**

Permute schema fields, rule declarations, explicit source pair order, checkpoint declarations, invariant construction, profile declarations, and requirement atoms. Require identical plan/profile bytes and IDs. Change one source reference and require a different `PlanID`; change only profile requirements and require the same `PlanID` but different `ProfileID` and `CompilationInputDigest`.

Add the negative order proof:

```go
func TestCompileRejectsUnprovableOptimizerImpliesCM(t *testing.T) {
	req := compileFixtureRequest(t, false)
	req.Profiles = optimizerMissingCMAtom(req.Profiles)
	result, err := Compile(req)
	if err != nil { t.Fatalf("Compile: %v", err) }
	failure, ok := result.Failure(); if !ok { t.Fatal("invalid profiles compiled") }
	assertDiagnostic(t, failure, ProfileOrderUnprovable)
}
```

- [ ] **Step 6: Freeze compiler canonical vectors and verify GREEN**

Freeze literal canonical hex and digest/ID vectors for schema, ruleset, compiler input, compiled plan, both compiled profiles, and the invalid-profile compilation failure. Expected canonical bytes must be independently constructed and hard-coded, not produced by the encoder under test.

Run:

```bash
gofmt -w internal/semantic/*.go
go test ./internal/semantic -run 'TestCompile' -count=1
git diff --check
```

- [ ] **Step 7: Review checkpoint**

Inspect derived access and plan canonicalization. Verify there are exactly two operator tags, no callbacks/general expression interpreter, no authored-order tie breaker, and no `PlanID` on invalid compilation. Do not commit.

### Task 3: Atomic Structural Patch and Inverse

**Files:**
- Create: `internal/semantic/patch.go`
- Create: `internal/semantic/patch_test.go`
- Modify: `internal/semantic/canonical.go`

**Interfaces:**
- Consumes: immutable Task 1 state and Task 2 compiled declarations.
- Produces: immutable schema-bound `Patch`, `Insert`, `Relate`, and `Update` variants, `PatchDigest`, atomic `ApplyPatch`, accepted-application receipts, receipt-bound `UndoPatch`, closed operation failures, and ordinary errors for malformed/schema-incompatible calls.

- [ ] **Step 1: Write failing staged-apply and all-or-nothing tests**

```go
func TestApplyPatchStagesInsertBeforeRelationsAtomically(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	patch := MustPatch(
		InsertOperation(team),
		RelateOperation(memberRelation(team.Ref(), drivers[1])),
		RelateOperation(memberRelation(team.Ref(), drivers[0])),
	)
	outcome, err := ApplyPatch(before, patch)
	if err != nil { t.Fatalf("ApplyPatch: %v", err) }
	after, failure := outcome.State(), outcome.Failure()
	if failure != nil { t.Fatalf("ApplyPatch: %v", failure.Code()) }
	if !after.HasRelation(memberRelation(team.Ref(), drivers[0])) || !after.HasRelation(memberRelation(team.Ref(), drivers[1])) {
		t.Fatal("relations missing")
	}
}

func TestApplyPatchFailureLeavesPredecessorByteIdentical(t *testing.T) {
	for _, patch := range patchesFailingAtOperationOneTwoOrThree(t) {
		before := patchPredecessor(t, patch)
		canonical := before.CanonicalBytes()
		outcome, err := ApplyPatch(before, patch)
		if err != nil { t.Fatalf("ApplyPatch: %v", err) }
		failure := outcome.Failure()
		if failure == nil { t.Fatal("invalid patch committed") }
		if !bytes.Equal(canonical, before.CanonicalBytes()) { t.Fatal("predecessor mutated") }
	}
}
```

Assert exact codes for collision, missing update target, update before-image mismatch, duplicate relation, and missing staged endpoint.

- [ ] **Step 2: Run patch tests and observe RED**

Run: `go test ./internal/semantic -run 'Test(ApplyPatch|UndoPatch|Patch)' -count=1`

Expected: compilation fails because structural operations do not exist.

- [ ] **Step 3: Implement the closed patch subset and canonical order**

```go
type OperationKind uint8
const (
	OperationInsert OperationKind = iota + 1
	OperationRelate
	OperationUpdate
)

func NewPatch(Schema, []Operation) (Patch, error)
func ApplyPatch(State, Patch) (PatchOutcome, error)
func UndoPatch(State, Patch, AcceptedPatchReceipt) (PatchOutcome, error)
```

`NewPatch` validates every operation against the supplied schema and binds the patch to its digest. Canonical patch bytes include `SchemaDigest`, followed by operation rank `Insert < Relate < Update`, then typed key. `ApplyPatch` verifies the state/patch schema link, validates all predecessor expectations, stages operations on a deep candidate, validates final referential integrity, and returns the candidate only after the whole patch passes. Success is the only path that returns an `AcceptedPatchReceipt` binding patch, predecessor, and result digests. Protected failure returns the exact predecessor with no receipt; malformed/schema-incompatible calls use the Go error channel.

`UndoPatch` requires the accepted receipt, verifies its patch and current-result links, processes the accepted sequence in reverse, verifies every after-image, and requires the reconstructed predecessor digest to equal the receipt. The receipt has no public arbitrary constructor. A patch by itself cannot authorize destructive inverse application.

`Update` stores exact field before/after images; absence is an explicit tagged before value. Do not store a caller-owned entity map or invent Delete/Unrelate/Merge/Split.

- [ ] **Step 4: Add inverse, order, immutability, and digest tests**

Require receipt-bound `UndoPatch` after a successful `ApplyPatch` to reproduce canonical `S` for generated lawful Insert/Relate/Update patches. Reject a missing/mismatched receipt and prove an insert patch cannot remove an independently existing identical entity without accepted-application evidence. Add a multi-field update whose later before-image fails and prove no earlier field changes. Reject schema-invalid operations at construction and patch/state schema mismatch without panic or predecessor mutation. Reverse relation proposal order and require identical patch bytes/digest. Mutate source slices and getter results and require stable bytes. Freeze literal canonical hex plus digest vectors for one schema-bound T1 patch and one schema-bound T2 patch.

- [ ] **Step 5: Verify GREEN**

```bash
gofmt -w internal/semantic/*.go
go test ./internal/semantic -run 'Test(ApplyPatch|UndoPatch|Patch)' -count=1
git diff --check
```

- [ ] **Step 6: Review checkpoint**

Confirm no partial state escapes, rejected operations are not journal entries, and relation validation deliberately recognizes a prior insert in the same candidate. Do not commit.

### Task 4: Closed Reference Executor, Invariants, and Accepted Journal

**Files:**
- Create: `internal/semantic/execute.go`
- Create: `internal/semantic/journal.go`
- Create: `internal/semantic/execute_test.go`
- Create: `internal/semantic/journal_test.go`
- Modify: `internal/semantic/canonical.go`

**Interfaces:**
- Consumes: compiled plan, immutable state/world, and Task 3 patch engine.
- Produces: `RunBinding`, `TransitionOutcome`, invariant results/failure reports, accepted `Journal`, entry/prefix digests, and exact implementations of the two operators.

- [ ] **Step 1: Write failing form-team transition tests**

Test deterministic synthetic identity from lineage, rule, sorted progenitors, and assignment-key output. Assert one accepted entry containing one Insert and two sorted Relates; both drivers remain byte-identical; T1 reads no HOS by using an incomplete-HOS S0 that still commits T1.

```go
outcome, err := ExecuteTransition(binding, "form_team.v1", s0, NewJournal())
if err != nil { t.Fatalf("ExecuteTransition: %v", err) }
if failure, ok := outcome.Failure(); ok { t.Fatalf("T1: %s", failure.Code()) }
assertOperationKinds(t, outcome.Patch(), OperationInsert, OperationRelate, OperationRelate)
if got := outcome.Journal().Entries(); len(got) != 1 { t.Fatalf("entries=%d", len(got)) }
```

Add assignment absence/empty/mismatch tests with exact codes and no patch/journal commit.

- [ ] **Step 2: Run T1 tests and observe RED**

Run: `go test ./internal/semantic -run 'TestExecuteFormTeam' -count=1`

- [ ] **Step 3: Implement run binding and `FormRelatedEntity`**

`BindRun` first verifies the compiled plan, initial state, and world canonical identities; requires `InitialState.SchemaDigest == Plan.SchemaDigest`; validates the closed executor identity and provenance policy; and returns an error without any run identity if one check fails. Only then does it compute state/world/input/plan/run/policy/execution identities. `ExecutorIdentity` affects only `ExecutionID`. `ExecuteTransition` selects only a compiled rule from the bound plan; callers cannot pass a new declaration at execution time.

Add RED binding cases for a plan/state schema mismatch, a corrupted state/world identity, an unsupported executor identity, and an invalid policy. Assert that none produces `SemanticRunID` or `ExecutionID`.

T1 verifies explicit source resolution/kind/distinctness, non-empty/equal assignment, computes the synthetic team, proposes the atomic patch, applies it, validates exact member cardinality, and appends one accepted entry only after success.

- [ ] **Step 4: Write failing aggregate and incident-rejection tests**

Cover passing `(T0,10,8)+(T0,7,6)`, incomplete source tuple, negative/illegal source duration, anchor mismatch, emitted aggregate mismatch, and max ties retaining every contributor. The anchor mismatch must assert:

```go
outcome, err := ExecuteTransition(binding, "aggregate_team_hos.v1", c1, journal1)
if err != nil { t.Fatalf("ExecuteTransition: %v", err) }
failure := mustFailure(t, outcome)
if failure.InvariantCode() != HOSAnchorMismatch { t.Fatalf("code=%s", failure.InvariantCode()) }
if _, ok := failure.ProposedPatchDigest(); ok { t.Fatal("precondition created patch") }
if outcome.Journal().PrefixDigest(binding) != journal1.PrefixDigest(binding) { t.Fatal("rejection entered history") }
```

- [ ] **Step 5: Implement `AggregateRelatedFields` and typed failure artifacts**

Traverse the T1 output slot and explicit member relations. Evaluate complete tuple, non-negative, `driving <= elapsed`, then equal anchors before constructing a patch. Build one Update with absent before-images, componentwise maxima, common anchor, and sorted evidence retaining all ties. Apply to a candidate and evaluate emitted anchor/reduction/inequality checks before committing.

Use the exact tagged failure union and code table from the design. Human prose is non-canonical. Failure evidence uses typed semantic refs only and no raw source key/value.

- [ ] **Step 6: Implement accepted journal canonical identity**

```go
func NewJournal() Journal
func (j Journal) AppendAccepted(JournalEntry) Journal
func (j Journal) Entries() []JournalEntry
func (j Journal) PrefixDigest(RunBinding) JournalPrefixDigest
```

Entry bytes include rule, predecessor/result state digests, `PatchDigest`, evidence, and applicable results. Prefix bytes include `SemanticRunID`, `ProvenancePolicyID`, and ordered accepted `JournalEntryDigest` values; they exclude executor, execution, attempt, time, and backend metadata.

- [ ] **Step 7: Add journal/cross-executor/canonical-vector tests and verify GREEN**

Two `RunBinding` values with different executor identities must have different `ExecutionID` and identical accepted state, patch, entry, prefix, and invariant-result digests. Freeze literal canonical hex plus digest/ID vectors for the provenance-policy tuple, `InputID`, `SemanticRunID`, `ExecutionID`, the synthetic-team identity tuple, a journal entry, journal prefix, complete invariant-result set, `ProtectedInvariantFailureReport`, and `ArtifactIntegrityFailureReport`.

```bash
gofmt -w internal/semantic/*.go
go test ./internal/semantic -run 'Test(Execute|Journal|RunBinding)' -count=1
git diff --check
```

- [ ] **Step 8: Review checkpoint**

Confirm T2 preconditions precede patch materialization, journals are accepted-only, and no HOS name appears in generic form-team code. Do not commit.

### Task 5: Valid Sealed Checkpoints and Replay Links

**Files:**
- Create: `internal/semantic/checkpoint.go`
- Create: `internal/semantic/checkpoint_test.go`
- Modify: `internal/semantic/canonical.go`

**Interfaces:**
- Consumes: Task 2 plan/checkpoint declarations and Task 4 binding/state/journal/invariant results.
- Produces: `SealRequest`, `SealOutcome`, immutable `CheckpointArtifact`, `CheckpointID`, `CheckpointArtifactID`, and `CheckpointArtifactDigest`.

- [ ] **Step 1: Write failing seal/refusal tests**

Table-test C1 and C2 success plus missing input/world, wrong checkpoint prefix, incomplete journal, missing/extra/duplicate/failing applicable invariant result, state digest mismatch, and one-ID/two-digest conflict.

```go
func TestSealRefusesIncompleteInvariantSet(t *testing.T) {
	req := completeC1SealRequest(t)
	req.InvariantResults = req.InvariantResults[:len(req.InvariantResults)-1]
	outcome, err := Seal(req)
	if err != nil { t.Fatalf("Seal: %v", err) }
	if outcome.Sealed() { t.Fatal("sealed incomplete invariant evidence") }
	if mustIntegrityCode(t, outcome) != ArtifactLinkInconsistent { t.Fatal("wrong code") }
}
```

- [ ] **Step 2: Run checkpoint tests and observe RED**

Run: `go test ./internal/semantic -run 'TestSeal|TestCheckpoint' -count=1`

- [ ] **Step 3: Implement sealing and complete invariant-set validation**

```go
type SealRequest struct {
	Binding          RunBinding
	Checkpoint       CheckpointKey
	State            State
	Journal          Journal
	InvariantResults []InvariantResult
	KnownArtifacts   []CheckpointArtifact
}

func Seal(SealRequest) (SealOutcome, error)
func (c CheckpointArtifact) ID() CheckpointArtifactID
func (c CheckpointArtifact) Digest() CheckpointArtifactDigest
```

Derive the expected invariant declaration keys from the plan prefix. Validate exact set equality before hashing. The artifact manifest contains plan/checkpoint/run/input/world/policy links plus state, journal-prefix, and invariant-result digests. `CheckpointArtifactID` uses the HLD formula; `CheckpointArtifactDigest` hashes the whole manifest. Compare the candidate with the defensively copied `KnownArtifacts` set so one ID cannot resolve to two digests within the in-memory verified frontier; do not add a global registry or persistence.

- [ ] **Step 4: Add replay, suffix, shuffle, and fixed-vector tests**

Replay S0 through T1 and reproduce C1 bytes. Replay lawful C1 through T2 and reproduce uninterrupted C2. Shuffling invariant-result construction does not change identity. Different executor IDs yield the same checkpoint ID/artifact ID/artifact digest. Freeze literal canonical checkpoint-manifest hex plus checkpoint ID/artifact ID/artifact digest vectors for C1 and C2.

- [ ] **Step 5: Verify GREEN and review**

```bash
gofmt -w internal/semantic/*.go
go test ./internal/semantic -run 'Test(Seal|Checkpoint|Replay)' -count=1
git diff --check
```

Confirm a failed boundary creates no checkpoint artifact and executor identity is absent from manifest identity. Do not commit.

### Task 6: Immutable Readiness Assessment and Profile Implication

**Files:**
- Create: `internal/semantic/profile.go`
- Create: `internal/semantic/profile_test.go`
- Modify: `internal/semantic/canonical.go`

**Interfaces:**
- Consumes: Task 2 compiled profiles and Task 5 sealed checkpoints/state.
- Produces: `AssessmentRequest`, immutable `Assessment`, `ReadinessVerdict`, normalized per-entity/per-requirement results, `AssessmentID`, and `AssessmentDigest`.

- [ ] **Step 1: Write failing C1/C2 assessment tests**

```go
func TestAssessC1ForCMAndOptimizer(t *testing.T) {
	c1, state, cm, optimizer := readinessFixtureC1(t)
	cmOutcome, err := Assess(AssessmentRequest{Checkpoint: c1, State: state, Profile: cm})
	if err != nil { t.Fatalf("Assess CM: %v", err) }
	if got := mustAssessment(t, cmOutcome).Verdict(); got != Ready {
		t.Fatalf("CM=%s", got)
	}
	optimizerOutcome, err := Assess(AssessmentRequest{Checkpoint: c1, State: state, Profile: optimizer})
	if err != nil { t.Fatalf("Assess optimizer: %v", err) }
	assessment := mustAssessment(t, optimizerOutcome)
	if assessment.Verdict() != NeedsInput { t.Fatalf("optimizer=%s", assessment.Verdict()) }
	assertRequirementCodes(t, assessment,
		TeamAggregationAnchorRequired, TeamElapsedDurationRequired, TeamDrivingDurationRequired)
}
```

C2 must be ready for both. Assessment must not change state or journal.

- [ ] **Step 2: Run profile tests and observe RED**

Run: `go test ./internal/semantic -run 'TestAssess|TestProfile' -count=1`

- [ ] **Step 3: Implement explicit-scope universal assessment**

```go
type AssessmentRequest struct {
	Checkpoint      CheckpointArtifact
	State           State
	Profile         CompiledProfile
	KnownAssessments []Assessment
}

func Assess(AssessmentRequest) (AssessmentOutcome, error)
```

Verify checkpoint/state/profile links before assessment. Select every entity of the compiled kind in canonical order; evaluate every normalized atom for every entity; retain satisfied and missing results; aggregate with universal `all`. Empty selection is vacuously ready. `AssessmentID = H(CheckpointArtifactID, ProfileID)` and digest covers the complete canonical answer. Compare the candidate with the defensively copied `KnownAssessments` set so one ID cannot resolve to two digests.

- [ ] **Step 4: Add non-omission, immutability, implication, and vector tests**

Add a second incomplete team and prove it cannot be dropped. Mutate returned evidence/results and prove bytes stable. Generate lawful team states across present/absent field combinations and assert optimizer-ready implies CM-ready. Freeze literal canonical assessment hex plus assessment ID/digest vectors for ready and needs-input results.

- [ ] **Step 5: Verify GREEN and review**

```bash
gofmt -w internal/semantic/*.go
go test ./internal/semantic -run 'Test(Assess|Profile|Readiness)' -count=1
git diff --check
```

Confirm assessment appends no journal entry and does not infer scope from a caller-supplied entity. Do not commit.

### Task 7: Ratified Team-HOS Fixture Package

**Files:**
- Create: `internal/fixtures/teamhos/fixture.go`
- Create: `internal/fixtures/teamhos/fixture_test.go`

**Interfaces:**
- Consumes: complete pure semantic APIs from Tasks 1-6.
- Produces: `teamhos.New(Passing|AnchorMismatch)` and stable fixture constants for rule/checkpoint/profile kinds used by app and observability tests.

- [ ] **Step 1: Write failing fixture-contract tests**

Assert the exact lineage descriptor, sanitized A/B source keys, assignment `X`, passing tuples `(T0,10,8)/(T0,7,6)`, mismatch tuple B `(T1,7,6)`, empty world, `changes.v1`, two rules, two checkpoints, and CM/optimizer requirements.

```go
func TestPassingFixtureCompilesToRatifiedPlan(t *testing.T) {
	in, err := New(Passing); if err != nil { t.Fatalf("New: %v", err) }
	compiled, err := semantic.Compile(in.Compilation); if err != nil { t.Fatalf("Compile: %v", err) }
	plan, ok := compiled.Plan(); if !ok { t.Fatal("fixture did not compile") }
	if got := ruleIDs(plan); !slices.Equal(got, []semantic.RuleID{"form_team.v1", "aggregate_team_hos.v1"}) {
		t.Fatalf("rules=%v", got)
	}
}
```

- [ ] **Step 2: Run fixture tests and observe RED**

Run: `go test ./internal/fixtures/teamhos -count=1`

- [ ] **Step 3: Implement declarations and variants as data only**

`fixture.go` may name team/HOS fields and instantiate closed declarations. It must not implement a transformer, callback, patch executor, readiness evaluator, or alternate canonicalizer. `New` returns fresh defensive values each call. Production `cmd` must not import this package.

- [ ] **Step 4: Add shuffle/isolation and direct-kernel lifecycle tests**

Compile shuffled fixture declarations and require identical IDs. Mutate one returned input and prove a second `New` result is unchanged. Drive the pure APIs directly to assert C1/C2 and anchor-mismatch preservation before introducing app orchestration.

- [ ] **Step 5: Verify GREEN and review**

```bash
gofmt -w internal/fixtures/teamhos/*.go
go test ./internal/fixtures/teamhos -count=1
go test ./internal/semantic -count=1
git diff --check
```

Confirm the max reduction carries the non-production caveat in package documentation and no production binary imports the fixture. Do not commit.

### Task 8: Application Spine, Closed Observation Contract, and Verified Frontier

**Files:**
- Create: `internal/app/observation.go`
- Create: `internal/app/observation_test.go`
- Create: `internal/app/result.go`
- Create: `internal/app/errors.go`
- Create: `internal/app/progressive.go`
- Create: `internal/app/progressive_test.go`

**Interfaces:**
- Consumes: semantic APIs and team-HOS fixtures in external tests.
- Produces: `app.Request`, `Observer`, `PhaseObservation`, `SpineResult`, `Run`, typed machinery errors, and exact lifecycle orchestration.

- [ ] **Step 1: Write failing passing/rejected golden tests**

Use `package app_test` and `teamhos.New`:

```go
func TestRunPassingTeamHOS(t *testing.T) {
	request := requestFromFixture(t, teamhos.Passing)
	result, err := app.Run(t.Context(), request, nil)
	if err != nil { t.Fatalf("Run: %v", err) }
	executionStatus, ok := result.ExecutionStatus()
	if !ok || result.Status() != app.SpineSucceeded || executionStatus != semantic.ExecutionSucceeded {
		t.Fatalf("status=%s", result.Status())
	}
	assertCheckpointReadiness(t, result, "team_formed.v1", semantic.Ready, semantic.NeedsInput)
	assertCheckpointReadiness(t, result, "team_hos_aggregated.v1", semantic.Ready, semantic.Ready)
}

func TestRunRejectedTeamHOSPreservesC1(t *testing.T) {
	result, err := app.Run(t.Context(), requestFromFixture(t, teamhos.AnchorMismatch), nil)
	if err != nil { t.Fatalf("semantic rejection returned Go error: %v", err) }
	assertFailureCode(t, result, semantic.HOSAnchorMismatch)
	assertOnlyAcceptedRules(t, result, "form_team.v1")
	assertOnlyCheckpoint(t, result, "team_formed.v1")
}
```

- [ ] **Step 2: Run app tests and observe RED**

Run: `go test ./internal/app -run 'TestRun(Passing|Rejected)' -count=1`

- [ ] **Step 3: Implement the closed observer carrier**

Use private fields and read-only getters. There is no public arbitrary constructor; `Run` creates observations.

```go
type Observer interface {
	BeginPhase(context.Context, PhaseObservation)
	EndPhase(context.Context, PhaseObservation)
}

type Phase uint8
type PhaseResult uint8
type TransitionKind uint8
type CheckpointKind uint8
type ProfileKind uint8

func (o PhaseObservation) PlanID() (ObservedPlanID, bool)
func (o PhaseObservation) SemanticRunID() (ObservedSemanticRunID, bool)
func (o PhaseObservation) ExecutionID() (ObservedExecutionID, bool)
func (o PhaseObservation) MetricProjection() MetricObservation
```

`MetricObservation` contains only bounded phase/result/kind/code/count fields. It has no ID/digest/string metadata. Nil observer becomes a no-op.

`Run` creates one private derived observation context per invocation and passes it only to observer calls; semantic functions receive the caller's original context. Observer dispatch recovers an observer panic and treats it as an operational observer failure that cannot change the semantic result, verified frontier, or Go error returned by the use case.

- [ ] **Step 4: Implement orchestration and verified-frontier result**

Run compile -> bind -> T1 -> seal C1 -> two C1 assessments -> T2 -> seal C2 -> two C2 assessments. Advance a private dependency-closed frontier only after each artifact verifies. The result retains every compiled profile referenced by a retained assessment. Return semantic failures with nil error.

For machinery-failure tests, keep default production operations concrete and use an unexported package-test constructor:

```go
type operations struct {
	compile func(semantic.CompileRequest) (semantic.Compilation, error)
	bind    func(semantic.RunBindingRequest) (semantic.RunBinding, error)
	execute func(semantic.RunBinding, semantic.RuleID, semantic.State, semantic.Journal) (semantic.TransitionOutcome, error)
	seal    func(semantic.SealRequest) (semantic.SealOutcome, error)
	assess  func(semantic.AssessmentRequest) (semantic.AssessmentOutcome, error)
}

func runWithOperations(context.Context, Request, Observer, operations) (SpineResult, error)
```

This seam tests orchestration inability; it does not enter the certified plan or allow production rule callbacks.

- [ ] **Step 5: Write machinery and integrity-frontier tests**

Inject cancellation, typed `InfrastructureUnavailableError`, and internal error before work, after compile, after C1, and implicating C1. Restrict `InvalidInputError` injection to malformed/unsupported canonical input at the initial request/canonical-input boundary; once execution exists, deterministic malformed, mismatched, or corrupt semantic artifacts are `ARTIFACT_INTEGRITY_FAILED` semantic results with nil Go error. Assert the exact result/error matrix: semantic failure + nil error; machinery error + no semantic failure; verified C1 retained after suffix failure; implicated C1 and dependent assessments excluded.

Assert optional result accessors explicitly: invalid compilation and post-compile/pre-bind machinery failure have no execution status; invalid compilation alone has a compilation failure; protected/integrity rejection alone has a terminal semantic failure; passing execution has neither failure. Protected rejection and all terminal executions return a present succeeded/failed execution status as required.

Verify observer order and noninterference with a recording observer. Its attempted mutation is impossible because observation fields are private and semantic artifacts are immutable.

Using `runWithOperations` inside `package app`, drive every injected terminal/frontier case plus one materialized-patch rejection and one actual seal rejection through a recording observer. Assert the exact closed `PhaseObservation` sequence, result/code/count projection, and dependency-closed `SpineResult`. This is the authoritative proof for outcomes the public golden request cannot naturally produce.

- [ ] **Step 6: Implement typed machinery errors and classification**

```go
type InvalidInputError struct { Code InvalidInputCode }
type InfrastructureUnavailableError struct { Code InfrastructureCode; Cause error }
```

Implement `Error` with fixed safe text and `Unwrap` where a cause exists. Preserve `context.Canceled`/`DeadlineExceeded` with `%w`/`errors.Is`. Do not expose payload, identifier, path, or raw dependency text.

- [ ] **Step 7: Verify GREEN and review**

```bash
gofmt -w internal/app/*.go
go test ./internal/app -count=1
go test ./internal/fixtures/teamhos ./internal/semantic -count=1
git diff --check
```

Confirm no app code re-evaluates semantic rules or readiness and every returned assessment has its compiled profile retained. Do not commit.

### Task 9: Semantic OTel Adapter and Metric Privacy Boundary

**Files:**
- Create: `internal/observability/semantic.go`
- Create: `internal/observability/semantic_test.go`
- Modify: `internal/observability/runtime.go`
- Modify: `internal/observability/runtime_test.go`

**Interfaces:**
- Consumes: app-owned `Observer`, `PhaseObservation`, and `MetricObservation`; existing explicit tracer/meter providers.
- Produces: `(*Runtime).SemanticObserver() app.Observer`, five spans, five metrics, exact closed mappings, and no semantic influence.

- [ ] **Step 1: Write failing instrument-registration and span tests**

Construct a test runtime with `tracetest.SpanRecorder` and `sdkmetric.ManualReader`. Call the real app spine with `runtime.SemanticObserver()`. Assert exact span names, explicit OK/Error status, result classifications, parent nesting from the observer's per-run stack, and only permitted trace attributes.

```go
func TestSemanticObserverNeedsInputIsOK(t *testing.T) {
	runtime, spans, _ := newSemanticFixture(t)
	_, err := app.Run(t.Context(), requestFromFixture(t, teamhos.Passing), runtime.SemanticObserver())
	if err != nil { t.Fatalf("Run: %v", err) }
	span := findSpan(t, spans.Ended(), "maiden_lane.semantic.assess_readiness", "optimizer.v1", "needs_input")
	if span.Status().Code != codes.Ok { t.Fatalf("status=%v", span.Status()) }
}
```

- [ ] **Step 2: Run semantic observability tests and observe RED**

Run: `go test ./internal/observability -run 'TestSemantic' -count=1`

- [ ] **Step 3: Register semantic instruments and strict views**

Extend `Runtime` with private semantic histograms/counters and call `registerSemanticInstruments()` after HTTP instruments. Add views that retain exactly the design's attributes per instrument; exemplar policy remains off.

Instrument names and units must exactly match:

```text
maiden_lane.semantic.phase.duration             Float64Histogram s
maiden_lane.semantic.structural.operations      Int64Counter operations
maiden_lane.semantic.checkpoints                Int64Counter checkpoints
maiden_lane.semantic.invariant.failures         Int64Counter failures
maiden_lane.semantic.readiness.assessments      Int64Counter assessments
```

- [ ] **Step 4: Implement a fresh observer per Run**

`SemanticObserver` returns a new adapter holding mutex-protected phase stacks keyed by the private observation contexts created by `app.Run`; `Runtime` itself stores no run stack. `BeginPhase` starts the span using that run's current observation-stack parent, never changes the context passed to semantic code, and records a monotonic operational start time. `EndPhase` pops the matching closed phase, applies exhaustive status/attributes, records bounded metrics, and ends the span. Unknown/invalid app values map to `internal_error` without exporting their raw representation.

Only `ObservedPlanID`, `ObservedSemanticRunID`, and `ObservedExecutionID` become trace attributes. Metric recording receives `MetricProjection()` and cannot see those fields.

- [ ] **Step 5: Add metrics, rejection, machinery, and privacy tests**

Assert exact points for both fixtures. Passing records one Insert, two Relates, one Update, two sealed checkpoints, and four readiness assessments. Anchor mismatch records one Insert, two Relates, no Update, one sealed checkpoint, no rejected or sealed C2, exactly two C1 readiness assessments, and one `HOS_ANCHOR_MISMATCH` failure.

Feed hostile assignment/anchor/source strings and machinery causes through test fixtures. Inspect every span attribute, log output, and metric point and assert none contain hostile values, entity refs, evidence, arbitrary digests, error text, checkpoint/profile IDs, or assessment IDs.

Keep real end-to-end `app.Run` coverage in this package for passing, invalid-plan, and anchor-mismatch requests. For outcomes that require the unexported app seam, test the two adapter halves without fabricating an app-owned observation: (1) exhaustively map the publicly available closed app phase/result/code enum values into observability-owned status and dimension values, and (2) drive metric recording from observability-owned normalized dimension/count structs. The corresponding complete injected `PhaseObservation` and metric-projection sequences are already proved inside `internal/app` package tests. Do not export an observation constructor or production test hook. Across the split tests cover `success`, `ready`, `needs_input`, `invalid_plan`, `protected_invariant_failed`, `artifact_integrity_failed`, `invalid_input`, `cancelled`, `infrastructure_unavailable`, and `internal_error`. Every completed phase must have explicit `codes.Ok` or `codes.Error`; none may remain UNSET.

In Task 8's recording-observer tests, prove that one materialized atomic-patch rejection projects every proposed operation as `rejected`, and one actual seal refusal projects exactly one rejected checkpoint. In this package, pass the equivalent observability-owned normalized dimensions/counts to the metric recorder and assert the exact points. Keep these distinct from the end-to-end anchor mismatch, which remains pre-patch and records neither a rejected Update nor any C2 checkpoint point.

Run disabled, recording, and asynchronously exporter-failing observers and compare canonical `SpineResult` projections byte-for-byte.

- [ ] **Step 6: Verify GREEN and review**

```bash
gofmt -w internal/observability/*.go
go test ./internal/observability -run 'TestSemantic' -count=1
go test ./internal/app ./internal/fixtures/teamhos ./internal/semantic -count=1
git diff --check
```

Confirm `internal/semantic` has no OTel import and app does not import observability. Do not wire a CLI/HTTP caller. Do not commit.

### Task 10: Documentation, Constitutional Matrix, and Full Verification

**Files:**
- Modify: `README.md`
- Modify: `METRICS.md`
- Modify: `ERRORS.md`
- Modify: `docs/implementation/implementation-guide.md`
- Modify: any tests from Tasks 1-9 only to add missing coverage, never to weaken assertions

**Interfaces:**
- Consumes: the complete implemented slice.
- Produces: truthful current-state documentation, registered operational contracts, final constitutional evidence, and a verified clean diff.

- [ ] **Step 1: Add the final package-boundary and property matrix tests**

Add a test that walks semantic Go imports with `go list -deps -json` or parses imports with `go/parser` and rejects OTel, observability, HTTP, AWS, stochflow, environment, filesystem, network, time, UUID, and randomness imports. Keep the allowlist explicit and local to the test.

Add a combined shuffle test that permutes declaration/map/relation/evidence/invariant/profile order and compares the complete passing lifecycle's plan, state, patch, journal, checkpoint, and assessment canonical bytes/IDs. Add the identity sensitivity matrix for lineage, observations, world, plan, executor, policy, profile, and attempt exclusion.

- [ ] **Step 2: Run the complete semantic matrix before documentation**

```bash
go test ./internal/semantic ./internal/fixtures/teamhos ./internal/app ./internal/observability -count=1
go test -race ./internal/semantic ./internal/fixtures/teamhos ./internal/app ./internal/observability -count=1
```

Expected: all focused and race tests pass before current-state docs claim the behavior exists.

- [ ] **Step 3: Update current-state documentation**

`README.md` must say the repository now contains an internal pure in-memory walking skeleton, while clearly stating there is still no public transformation API, persistence, production team-HOS rule, promotion, or publication.

`METRICS.md` must register all five exact instruments, types, units, dimension allowlists, and recording timing from the design. State that the runtime registers them but the production process records no semantic points because no public caller exists.

`ERRORS.md` must replace its empty status and register only Maiden Lane-owned machinery errors introduced by Task 8, including stable code, safe meaning, retry classification, and publication consequence. Do not catalog typed semantic results as Go errors.

Rewrite the Implementation Guide's current capabilities, repository map, runtime flow, Go orientation, and gaps to match the actual files. Do not list planned persistence/API/package boundaries.

- [ ] **Step 4: Run formatting and targeted tests again**

```bash
make fmt
go test ./internal/semantic ./internal/fixtures/teamhos ./internal/app ./internal/observability -count=1
git diff --check
```

- [ ] **Step 5: Run the authoritative repository verification**

```bash
make verify
make container-check
```

Expected: format, module tidiness, tool versions, vet, Staticcheck, unit tests, race tests, govulncheck, binary build, container build, and container smoke test all pass. If Docker is unavailable, record that exact limitation and still run every `make verify` stage.

- [ ] **Step 6: Inspect the final diff and constitutional boundaries**

Run:

```bash
git status --short --branch
git diff --stat
git diff --check
git diff -- go.mod go.sum api/openapi.yaml Inviolates.md docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md
rg -n 'internal/observability|go.opentelemetry|log/slog|os\.|time\.|uuid|rand\.' internal/semantic
rg -n 'internal/fixtures/teamhos' cmd internal/httpapi
```

Expected:

- no unexpected dependency or authoritative-contract change;
- no semantic import of operational packages;
- no production import of the fixture;
- no OpenAPI or public route change;
- only intended uncommitted files in `git status`; and
- current-state docs accurately describe the implementation.

- [ ] **Step 7: Independent final reviews**

Dispatch at least two read-only reviewers:

1. an invariant/identity reviewer for canonical bytes, accepted-only provenance, sealing completeness, readiness separation, replay, and the failed-suffix prefix guarantee; and
2. an observability/security reviewer for import direction, result noninterference, trace allowlists, metric cardinality, safe errors/logs, and lifecycle shutdown.

Resolve every blocking finding with RED -> GREEN and rerun the affected targeted tests plus `make verify`. Do not commit unless the owner explicitly requests it.

---

## Execution Order and Agent Ownership

The dependency chain is strict:

```text
Task 1 values/state/canonical leaves
  -> Task 2 declarations/compiler
  -> Task 3 atomic patch
  -> Task 4 executor/journal
  -> Task 5 checkpoint
  -> Task 6 readiness
  -> Task 7 fixture
  -> Task 8 app orchestration
  -> Task 9 observability
  -> Task 10 docs/full verification
```

Use one fresh implementation subagent per task under `superpowers:subagent-driven-development`. Each task receives a specification-conformance review and then a code-quality/invariant review before the next task begins. Because agents share a filesystem, never assign concurrent writers to `internal/semantic`; parallelize only read-only reviews or non-overlapping final audits. The root agent owns integration, every diff inspection, broad verification, and communication with the owner.
