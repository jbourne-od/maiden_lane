# Progressive Semantic Spine Walking Skeleton Design

**Status:** Ratified

**Date:** 2026-08-13

**Highest repository authority:** [Ratified Maiden Lane Inviolates](../../../Inviolates.md)

**Normative architecture:** [Maiden Lane High-Level Design](2026-08-11-maiden-lane-high-level-design.md)

**Approved amendment:** [Progressive Completeness and Consumer-Scoped Publication Design](2026-08-12-progressive-completeness-design.md)

## 1. Purpose

This slice establishes the smallest pure in-memory semantic spine that proves
Maiden Lane's central architecture end to end. It uses one sanitized team-HOS
incident fixture, exactly two typed transformations, and one named checkpoint
after each transformation.

The slice must demonstrate all of the following together:

- structural state changes are atomic, attributable, and reversible for the
  operations used by the fixture;
- only accepted changes enter the semantic journal;
- protected invariants prevent invalid state from committing;
- a valid prefix can seal before a later consumer's requirements are met;
- readiness is checkpoint- and profile-specific rather than a property of the
  record in isolation;
- a failed suffix neither mutates nor invalidates a sealed prefix;
- canonical plans, states, artifacts, assessments, and identities are stable
  under irrelevant declaration and map ordering; and
- operational observability explains execution without influencing semantic
  behavior or becoming a second semantic journal.

This design ratifies team-HOS only as the first sanitized golden incident
fixture. It does not designate team-HOS as Maiden Lane's first production
transformation, claim Optimal Dynamics production semantics, or authorize a
customer rollout.

## 2. Authority and terminology

The [Inviolates](../../../Inviolates.md) remain controlling. In particular,
this slice preserves semantic ownership, deterministic execution, structural
and attributable changes, fail-closed protected invariants, pinned replay,
one progressive transformation spine, consumer-relative completeness, and the
separation of sealing, readiness, promotion, and publication.

The terms `valid`, `sealed`, `ready`, and `needs_input` have the meanings in
the HLD and progressive-completeness amendment:

- a valid state satisfies every protected invariant applicable at that
  boundary;
- a sealed checkpoint is valid, immutable, internally consistent, and
  replayable;
- a readiness assessment asks whether one sealed checkpoint satisfies one
  compiled consumer profile; and
- `needs_input` is a lawful readiness result, not invalid execution.

This slice does not implement promotion or publication. It proves that a C1
artifact remains eligible for a later CM promotion decision after T2 fails;
it does not claim that any publication gate has passed.

## 3. Chosen architecture

### 3.1 Package responsibilities

The implementation will use a compact semantic kernel with isolated fixture
declarations:

```text
internal/fixtures/teamhos
        │ sanitized typed declarations and golden data
        ▼
internal/semantic
        │ pure compile, execute, seal, and assess kernel
        ▼
internal/app
        │ lifecycle orchestration and operational observation
        ▼
internal/observability
```

The arrows show conceptual use, not permission to reverse dependency
boundaries. The concrete source dependency is:

- `internal/semantic` depends only on the Go standard library and receives
  every semantic input explicitly;
- `internal/app` owns the use-case orchestration and the narrow observer
  contract it consumes;
- `internal/observability` may implement that app-owned observer using the
  existing OpenTelemetry foundation; and
- `internal/fixtures/teamhos` supplies ordinary typed values to semantic and
  application tests. Production binaries do not import the fixture package.

`internal/semantic` must not import `internal/app`, `internal/observability`,
OTel, logging, HTTP, AWS, persistence, stochflow, the filesystem, the network,
the environment, or the wall clock. Telemetry is an observation of the use
case, never an input to semantic decisions or identities.

The exact internal file split is an implementation-plan decision. The package
ownership and dependency direction are design decisions.

### 3.2 Semantic kernel ownership

The compact kernel owns:

- immutable typed schemas, states, entities, fields, and relations;
- two closed transformation operator variants;
- deterministic compilation and plan ordering;
- the `Insert`, `Relate`, and `Update` structural operations used here;
- operation before-images and inverse application for that supported subset;
- protected invariant evaluation;
- accepted journal entries and separate failure reports;
- canonical encodings and layered semantic identities;
- checkpoint declarations and sealing; and
- compiled completeness profiles and immutable readiness assessments.

The kernel is intentionally not a generic rule engine. It exposes closed typed
data whose reads, writes, predicates, output identities, relation traversals,
reductions, and invariant obligations can be derived statically.

### 3.3 Exactly two closed transformations

The certified plan contains exactly these operator variants:

1. `FormRelatedEntity`, instantiated as rule `form_team.v1`. It declares fixed
   source and output kinds, an exact normalized pair of typed source
   references, a grouping field, exact source cardinality, copied fields, a
   relation kind, and a typed output-key expression.
2. `AggregateRelatedFields`, instantiated as rule
   `aggregate_team_hos.v1`. It declares a fixed relation traversal, required
   source-tuple fields, closed source predicates, equal-anchor fields,
   destination fields, typed reduction operations, closed result predicates,
   and a typed reference to the team output slot of `form_team.v1`.

There is no arbitrary callback, reflection-driven expression evaluator,
dynamic field name, embedded Go function, generic DSL parser, or arbitrary SQL
escape hatch. Compilation derives accesses and rejects declarations whose
stated accesses disagree with the operator's actual reads and writes.

The plan has the fixed progressive shape:

```text
S0
 └─ T1 form_team.v1
       └─ C1 team_formed.v1
             └─ T2 aggregate_team_hos.v1
                   └─ C2 team_hos_aggregated.v1
```

### 3.4 Invariant ownership

This slice does not introduce a free-standing invariant DSL. Each closed
operator variant determines the invariant obligations that can be expressed in
its typed declaration, and the compiler derives the corresponding reads and
applicability boundary.

Every derived obligation becomes a typed immutable invariant declaration with
an operator-relative canonical declaration key, stable code, closed scope
(`rule_precondition`, `candidate_postcondition`, `operation`, or
`checkpoint_prefix`), derived reads, and boundary applicability. Rule authors
cannot attach callbacks or undeclared predicates. The complete declarations
enter `RulesetDigest` and the canonical plan.

Invariant ownership is exact:

- typed state construction enforces structural representation only: declared
  kinds and fields, value types, field-name legality, unique entity and
  relation keys, and canonical encodability;
- `FormRelatedEntity` owns only the source identity/kind, grouping-field,
  synthetic-identity, copied-field, and resulting relation/cardinality
  obligations required to form the related entity;
- `AggregateRelatedFields` owns source-tuple completeness, declared scalar
  predicates, anchor equality, reduction correctness, destination-tuple
  completeness, and final scalar predicates;
- patch application owns operation legality and before-image checks; and
- checkpoint sealing owns completeness and consistency of the artifact set
  required to support the checkpoint claim.

The team-HOS declaration instantiates the aggregate operator's closed scalar
predicates as non-negative integer checks and `driving <= elapsed` for source
and emitted tuples. These predicates are data in the typed T2 declaration;
they are not hard-coded into `FormRelatedEntity`, a callback, or the generic
state representation.

The aggregate operator's v1 predicate/reduction tags are exactly
`CompleteTuple`, `NonNegativeInt`, `EqualFieldAcrossSources`,
`LessOrEqualFields`, and `ReduceInt64Max`, plus the derived checks that emitted
fields equal their declared source anchor and reductions. Adding another
predicate or reduction tag is a semantic-format change, not a fixture-local Go
extension.

Consequently T1 never reads HOS fields. HOS evidence may be present at C1, but
its fitness for aggregation is a T2 question. This keeps the valid CM prefix
independent of facts required only by its suffix while retaining
consumer-independent validity at every declared boundary.

## 4. Approved sanitized team-HOS fixture

### 4.1 Schema and exact value semantics

The fixture models one logical team composed from exactly two source driver
entities. Source labels `driver:A`, `driver:B`, and `team:AB` are human-facing
fixture shorthand, not raw semantic identities.

The fields are deliberately semantic fixture names rather than asserted
production-schema names:

| Entity | Field | Type and validity |
|---|---|---|
| driver | `assignment_key` | when present, validated UTF-8 string; T1 requires present and non-empty |
| driver | `hos_anchor` | when present, validated UTF-8 atom; T2 requires present and non-empty; equality is exact byte equality |
| driver | `hos_elapsed_hours` | when present, signed 64-bit integer; T2 requires present and non-negative |
| driver | `hos_driving_hours` | when present, signed 64-bit integer; T2 requires present, non-negative, and no greater than elapsed |
| team | `assignment_key` | T1 emits a present, non-empty validated UTF-8 string |
| team | `aggregation_anchor` | absent at C1; otherwise non-empty validated UTF-8 atom |
| team | `elapsed_duration_hours` | absent at C1; otherwise signed 64-bit integer constrained by T2 |
| team | `driving_duration_hours` | absent at C1; otherwise signed 64-bit integer constrained by T2 |

Field presence is distinct from a field containing a value. This slice has no
semantic `null` value. A missing field is absent; an explicit null is rejected
at the typed input boundary.

Integer hours keep the fixture exact and hand-calculable. They are not a
general duration representation decision for production schemas.

### 4.2 Initial state S0

S0 contains exactly two source driver entities and no team or membership
relation. The transformation scope is the explicit pair of source entity
references supplied by the fixture; the executor must not discover candidates
by iterating every driver or grouping an ambient map.

The normalized source pair is semantic plan content in this walking skeleton.
Each `SourceRef` is a typed `(entity kind, sanitized canonical source key)`
resolved only within S0's pinned input lineage. The pair sorts by its canonical
typed encoding. Both fixture variants use the same lineage and source
references, so their plan is identical even though their state content and
`InputID` differ. T2 names T1's typed team output slot rather than searching
for “the team.” This leaves the pinned world as an explicit empty world and
introduces no hidden execution-scope input.

The two drivers have:

- distinct canonical source identities in one input lineage;
- the same `assignment_key` in any passing T1 case; and
- complete, individually lawful `(hos_anchor, elapsed, driving)` tuples, where
  `driving <= elapsed`.

`InputLineageID` identifies the continuing logical record lineage, not one
input snapshot or execution. It is the digest of a canonical typed lineage-root
declaration. For this sanitized fixture the declaration is exactly:

```text
LineageRootV1
  namespace = "maiden-lane.sanitized-fixture"
  root_key  = "team-hos-team-ab"
```

The passing and rejected variants intentionally share this `InputLineageID`.
Their different S0 content produces different `InitialStateDigest`, `InputID`,
and `SemanticRunID` values without changing source identity lineage. A future
production acquisition contract must separately define its own canonical
lineage-root declaration; it may not reuse this fixture key or derive lineage
from a mutable snapshot, clock, execution, or attempt.

Source entity IDs are deterministically namespaced from the input lineage,
entity kind, and canonical source key as required by the HLD. Sanitized labels
`A` and `B` are source keys inside the fixture, not customer identifiers.

### 4.3 Synthetic team identity

`form_team.v1` creates a team identity using the HLD synthetic-identity
construction:

```text
H(
  input lineage,
  entity kind "team",
  RuleID "form_team.v1",
  sorted canonical progenitor EntityIDs,
  typed output key derived from the common assignment_key
)
```

The actual encoding is domain-tagged and versioned as specified in Section 8.
The two progenitors are sorted by canonical `EntityID`, because member roles
are unordered. Wall-clock time, authoring order, map order, execution identity,
attempt identity, and backend identity are forbidden from this construction.

The output key is typed and deterministic. `team:AB` remains only a display
shorthand; it is not substituted for the canonical synthetic ID.

## 5. T1: `form_team.v1`

### 5.1 Preconditions and protected validity

T1 is explicitly proposed for two source drivers. It rejects, rather than
silently declining, when the proposal is structurally or semantically
incompatible. Before materializing its patch, T1 verifies:

- both declared source entities exist and are distinct drivers;
- both source `assignment_key` values are present and non-empty; and
- the assignment keys are equal.

T1's derived reads therefore contain no HOS field. Source-tuple completeness,
duration validity, and anchor coherence first become applicable to the
aggregation rule at T2. This is what allows an otherwise valid, CM-ready team
prefix to survive any later HOS-specific rejection.

Different assignment keys for drivers explicitly proposed as one team violate
the protected invariant `TEAM_ASSIGNMENT_KEY_MISMATCH`; they are not a no-op or
an ordinary non-selection result.

### 5.2 Atomic structural patch

The accepted T1 patch contains three structural operations:

```text
Insert(team)
Relate(team, member, driver:A)
Relate(team, member, driver:B)
```

`Insert + Relate` is intentional. The driver entities remain independently
meaningful and authoritative for their observations, so T1 does not consume or
replace them. `Merge` remains reserved for true semantic consolidation.

The patch is validated and applied to an isolated candidate as one atomic
unit. Intra-patch endpoint validation sees the staged team inserted earlier in
the same patch. The two relation operations are sorted by canonical endpoint,
not authoring or input order. Before commit, the candidate must have:

- referential integrity;
- one inserted team with no identity collision;
- exactly two distinct `team --member--> driver` relations; and
- those relations pointing to the two explicit progenitors.

The insert records the authoritative expectation that the team identity was
absent. Each relation records the authoritative expectation that the relation
was absent. If any operation, candidate invariant, or postcondition fails, no
operation commits and no T1 journal entry is appended.

Canonical patch order uses a closed operation rank followed by each
operation's typed canonical key. For this supported subset, `Insert` sorts
before `Relate`, which sorts before `Update`; relations then sort by relation
kind, source, and destination. This order both makes bytes stable and preserves
the staged endpoint dependency needed by T1.

Inverse application processes the accepted operation sequence in reverse, so
T1 removes its relations before removing their team endpoint. Undo is defined
only for the structural subset implemented by this slice and must verify the
accepted after-image before changing the candidate.

### 5.3 C1 state and seal

An accepted T1 produces:

```text
driver:A     unchanged
driver:B     unchanged
team:AB      assignment_key only
team:AB --member--> driver:A
team:AB --member--> driver:B
```

The team aggregate tuple is wholly absent at C1. That is a lawful state, not a
suppressed invariant violation.

Every team in this fixture must always have exactly two distinct canonical
member relations. That cardinality is protected structural validity, not a CM
completeness requirement. A one-member team is invalid rather than merely
CM-incomplete.

C1 may seal once its applicable protected invariants and replay links pass. It
is then immutable and replayable independently of any suffix result.

## 6. T2: `aggregate_team_hos.v1`

### 6.1 Approved fixture-only reduction

For this sanitized fixture only:

\[
anchor_{team}=anchor_A=anchor_B
\]

\[
elapsed_{team}=\max(elapsed_A,elapsed_B)
\]

\[
driving_{team}=\max(driving_A,driving_B)
\]

Componentwise maximum is deterministic, symmetric, order-independent,
hand-calculable, and mathematically closed under lawful source tuples:

\[
d_i \le e_i \Rightarrow \max(d_A,d_B) \le \max(e_A,e_B)
\]

This reduction is only a reconciliation envelope for the sanitized golden
incident. It is not asserted to be production team-HOS semantics and must not
be promoted into a production rule without separate domain approval.

### 6.2 Evaluation and update

T2 traverses the explicit `member` relations from the one team and reads the
two still-authoritative source observations. In deterministic order it:

1. verifies the team and exactly two distinct members exist;
2. requires both drivers to provide complete typed source HOS tuples;
3. requires each source tuple to be non-negative and satisfy
   `driving <= elapsed`;
4. requires exact equality of the two source anchors;
5. computes the two componentwise maxima;
6. proposes one atomic `Update(team)` with an explicit before-image proving
   the three aggregate fields were absent;
7. applies that update to an isolated candidate;
8. verifies the emitted anchor equals the common source anchor;
9. verifies the emitted durations equal the declared reductions; and
10. rechecks `team.driving <= team.elapsed` before commit.

The update preserves team identity, assignment, member relations, and both
source entities. The accepted evidence names both canonical source entity
references and their source fact references in sorted order. When multiple
sources tie for a maximum, evidence retains every tied contributor rather than
choosing one by incidental order.

### 6.3 Boundary-specific aggregate validity

Aggregate validity is declared at the boundary that makes the aggregate claim:

- T1 writes only `team.assignment_key` and the two member relations, so C1's
  team aggregate tuple is wholly absent by construction; and
- an accepted T2 must emit all three correctly typed aggregate fields, the
  common source anchor, the exact declared maxima, non-negative durations, and
  `driving <= elapsed` before C2 can seal.

A proposed partial or unlawful team aggregate tuple fails T2 with
`HOS_AGGREGATE_INVALID`. The kernel does not install a hidden global HOS
validator or make T1 reread suffix-only evidence.

### 6.4 Anchor mismatch rejection boundary

Anchor equality is evaluated as a protected T2 precondition before an update
patch is materialized. On mismatch:

- T2 returns a typed `PROTECTED_INVARIANT_FAILED` result with invariant code
  `HOS_ANCHOR_MISMATCH`;
- the failure report has no `ProposedPatchDigest`, because no patch existed;
- no state change commits;
- no T2 entry enters the accepted journal;
- no C2 exists or seals; and
- the already sealed C1 and its assessments remain byte-identical within that
  execution.

The failure is deterministic and permanent for those semantic inputs. It is
not retried as transient infrastructure failure.

## 7. Completeness profiles and readiness

### 7.1 Explicit scope

Both profiles declare the same closed scope:

- entity selector: all entities of semantic kind `team`;
- relation selector: explicitly empty for field-presence assessment; and
- aggregation: every selected team must satisfy every requirement.

Scope is part of the canonical compiled profile. It is never inferred from map
iteration or from whichever entity happens to be passed to the assessor.
Although the fixture's T1 invariant guarantees one team at its checkpoints,
the generic assessment semantics cover multiple selected teams.

An empty selected entity scope is vacuously `ready` for these universal
field-presence profiles. That behavior is explicit and tested. It cannot mask
this fixture's missing team because the protected T1 plan boundary requires
one correctly formed team before C1 seals.

Membership is not duplicated as a profile field. Exact member cardinality is
protected graph validity. The CM profile only asks which additional
consumer-required field must be present on each already-valid team.

### 7.2 CM profile

`cm.v1` requires:

| Stable requirement code | Presence atom |
|---|---|
| `TEAM_ASSIGNMENT_KEY_REQUIRED` | `team.assignment_key` |

C1 and C2 are CM-ready in both fixture variants whenever those checkpoints
exist and seal.

### 7.3 Optimizer profile

`optimizer.v1` includes the CM atom and additionally requires:

| Stable requirement code | Presence atom |
|---|---|
| `TEAM_AGGREGATION_ANCHOR_REQUIRED` | `team.aggregation_anchor` |
| `TEAM_ELAPSED_DURATION_REQUIRED` | `team.elapsed_duration_hours` |
| `TEAM_DRIVING_DURATION_REQUIRED` | `team.driving_duration_hours` |

At C1 these three fields are lawfully absent, so the result is `needs_input`
with the three stable requirement codes. At a passing C2 all four atoms are
present, so the result is `ready`.

### 7.4 Compiled profile ordering

The compiler proves:

\[
cm.v1 \preceq optimizer.v1
\]

from the normalized declarations rather than a hard-coded consumer name. For
this slice it may prove implication only when scope, aggregation semantics,
atom semantics, and schema typing are identical and one normalized requirement
set is a subset of the other.

If a profile declaration is invalid or its ordering claim is not mechanically
provable, compilation rejects it and produces no `ProfileID`. Future profiles
need not be comparable.

Canonical compiled profile content includes the profile format and compiler
semantics versions, schema digest, explicit scope, aggregation semantics,
normalized requirement atoms, declared implication targets by canonical
declaration key, and the closed proof kind used for each proven implication.
It never embeds another derived `ProfileID`, avoiding recursive identity.

Assessments are immutable artifacts over a sealed checkpoint and a compiled
profile. They never mutate state and never append a transition journal entry.

The canonical assessment record contains:

- assessment format and assessment-semantics versions;
- `CheckpointArtifactID` and `ProfileID`;
- the aggregate `ReadinessVerdict`;
- every selected `EntityRef` in canonical order;
- for every selected entity, every normalized requirement result in canonical
  requirement order, including its `RequirementCode`, a closed
  `satisfied`/`missing` result, and sorted safe `FactRef` evidence; and
- sorted assessment-level safe evidence references, if any.

Ready assessments retain the satisfied results rather than encoding only an
empty failure list. Needs-input assessments retain missing results for every
affected selected entity, so aggregation cannot silently omit a failure.
Cached `AssessmentID`, `AssessmentDigest`, and human prose are excluded from
canonical assessment bytes.

## 8. Canonical encodings and layered identity

### 8.1 Narrow versioned encodings

Each semantic artifact has its own small canonical binary encoding. This slice
does not introduce a universal serializer or use reflection as semantic
meaning.

All v1 encodings use:

- an artifact-specific ASCII domain tag such as `maiden-lane.state.v1`;
- a fixed schema-defined field order;
- unsigned 64-bit big-endian lengths and collection counts;
- signed 64-bit big-endian integer values;
- a one-byte `0` or `1` presence marker for optional values;
- explicit numeric tags for closed unions and enumerations;
- validated UTF-8 strings encoded as their exact bytes, without Unicode
  normalization;
- lists encoded as a count followed by their canonical elements; and
- maps and sets normalized to sorted typed canonical keys before encoding.

Inputs with duplicate normalized keys, duplicate set members, invalid UTF-8,
unknown union tags, out-of-schema fields, or non-canonical typed values are
rejected. Order is retained only where the schema says order is semantic.

For every equation below, `H(x, y, ...)` means SHA-256 over the corresponding
artifact-specific, domain-tagged, versioned canonical tuple. It never means
ambiguous byte concatenation. Digests are rendered as
`sha256:<64 lowercase hexadecimal characters>`. An artifact's cached digest or
identity is excluded from the bytes used to derive that value.

Canonicalization is Maiden Lane-owned. The initial content hasher is a narrow
semantic port over already-canonical bytes with a fixed `sha256.v1` adapter
implemented using the Go standard library. The hasher cannot decide field
meaning or ordering, and no stochflow import is introduced.

Semantic values use defensive copies or immutable construction so caller-held
maps and slices cannot mutate already-compiled plans, states, patches,
journals, checkpoints, profiles, or assessments.

Canonical state bytes contain the schema digest, input-lineage identity,
entities sorted by `(kind, EntityID)` with fields sorted by typed field path,
and relations sorted by `(kind, from EntityID, to EntityID)`. Canonical world
bytes contain an ordered set of typed immutable snapshot or configuration
references; the fixture encodes a versioned zero-count set rather than treating
the world as missing.

### 8.2 Identity graph

The slice uses this layered identity graph:

```text
InputLineageID        = H(canonical lineage-root declaration)
SourceEntityID        = H(InputLineageID, EntityKind, CanonicalSourceKey)
SchemaDigest          = H(canonical schema declaration)
RulesetDigest         = H(
  canonical rule, invariant-obligation, and checkpoint declarations
)
CompilationInputDigest = H(
  SchemaDigest,
  RulesetDigest,
  canonical profile source declarations,
  CompilerSemanticsVersion
)
StateDigest           = H(canonical state)
InitialStateDigest    = StateDigest(S0)
WorldID               = H(canonical pinned world)
InputID               = H(InitialStateDigest, WorldID)
PlanID                = H(canonical compiled plan)
SemanticRunID         = H(InputID, PlanID)
ProvenancePolicyID    = H(canonical provenance policy)
ExecutionID           = H(SemanticRunID, ExecutorIdentity, ProvenancePolicyID)
PatchDigest           = H(canonical structural patch)
JournalEntryDigest    = H(canonical accepted journal entry)
JournalPrefixDigest   = H(
  SemanticRunID,
  ProvenancePolicyID,
  ordered JournalEntryDigest values
)
InvariantResultDigest = H(
  canonical complete ordered applicable invariant-result set
)
CheckpointID          = H(PlanID, canonical checkpoint declaration key)
CheckpointArtifactID  = H(
  SemanticRunID,
  CheckpointID,
  StateDigest,
  JournalPrefixDigest,
  InvariantResultDigest,
  ProvenancePolicyID
)
CheckpointArtifactDigest = H(canonical complete checkpoint artifact)
ProfileID             = H(canonical compiled profile)
AssessmentID          = H(CheckpointArtifactID, ProfileID)
AssessmentDigest      = H(canonical assessment)
CompilationFailureDigest = H(
  CompilationInputDigest,
  canonical ordered compiler diagnostics
)
FailureReportDigest   = H(canonical tagged execution failure report)
```

The fixture uses an explicit canonical empty world; it does not omit the world
from input identity. The initial provenance policy is `changes.v1`, the minimum
publishable level in the HLD, and is pinned even though publication is outside
this slice.

`SchemaDigest`, `RulesetDigest`, and `CompilationInputDigest` may exist for a
syntactically canonical request that later fails static semantic validation.
They identify submitted canonical bytes, not an accepted plan. Only successful
compilation produces `PlanID`.

`AssessmentID` identifies the deterministic semantic question for one
checkpoint/profile pair. `AssessmentDigest` content-addresses the answer and
its safe evidence. One `AssessmentID` resolving to two different assessment
digests is an integrity failure, not a second valid outcome.

`CheckpointArtifactID` identifies the realized semantic checkpoint claim
constituted by the exact HLD components in its formula.
`CheckpointArtifactDigest` content-addresses the complete canonical checkpoint
manifest, including the replay links whose identities are already committed by
the semantic run. One `CheckpointArtifactID` resolving to two different
checkpoint artifact digests is `ARTIFACT_LINK_INCONSISTENT`.

`CompilationInputDigest` identifies the canonicalizable compiler request,
including profile source declarations, while `CompilationFailureDigest`
content-addresses its diagnostic answer. It is not a `PlanID` and does not make
an invalid program executable. A malformed request that cannot reach the
canonical compiler-input representation has neither digest and fails at the
calling boundary.

The same identity-versus-content distinction applies whenever a semantic key
identifies a question or declaration rather than the bytes of its realized
artifact.

### 8.3 Plan identity and order

Canonical plan content includes:

- schema and ruleset identities;
- compiler semantics version;
- both complete typed transformation declarations;
- compiler-derived read and write sets;
- dependency edges and stable execution levels;
- both checkpoint declarations and their prefix boundaries;
- invariant applicability by boundary;
- typed output-key expressions; and
- provenance obligations relevant to execution.

The compiler topologically orders transformations by dependency. When multiple
nodes are simultaneously eligible, it uses a canonical semantic key, not
authoring position. This fixture's dependency forces T1 before T2, but the
canonical rule still applies and is tested with shuffled declarations.

Every version entering semantic meaning is a pinned immutable value or digest,
never `latest`.

### 8.4 Journal identity

An accepted journal entry contains semantic rule identity, predecessor and
result state digests, the canonical committed patch, accepted evidence
references, and applicable invariant results. Human prose and backend mechanics
are excluded.

`PatchDigest` covers the complete atomic structural proposal in canonical
operation order. `JournalEntryDigest` covers the complete accepted entry,
including that patch digest and its predecessor/result links. A rejected patch
may have a `PatchDigest` referenced by a failure report, but only an accepted
entry receives a `JournalEntryDigest` that can enter committed history.

The journal-prefix digest covers:

- its own domain and format version;
- `SemanticRunID`;
- `ProvenancePolicyID`; and
- the ordered digests of accepted semantic entries through that checkpoint.

`InvariantResultDigest` covers the complete applicable set, ordered by
canonical invariant declaration key. Every result contains that declaration
key, applicability scope/boundary, pass verdict, stable code, and sorted
canonical evidence references. Sealing derives the expected declaration-key
set from the plan and rejects missing, duplicate, extra, or failing results;
the digest cannot bless a caller-selected subset.

It expressly excludes `ExecutionID`, `ExecutorIdentity`, `AttemptID`, clocks,
hostnames, trace/span IDs, job IDs, retries, and storage locations. Otherwise
two certified backends could not produce the same checkpoint artifact for one
semantic run.

Readiness assessments and rejected proposals are not accepted transition
entries. The negative fixture's accepted prefix contains only T1.

### 8.5 Sealing obligations

A C1 or C2 artifact seals only when it contains internally consistent links to:

- the declared checkpoint and plan;
- the semantic run and every pinned replay input;
- canonical state bytes and `StateDigest`;
- the complete accepted `JournalPrefixDigest` at `changes.v1`;
- canonical passing results for every protected invariant applicable to the
  prefix; and
- the pinned provenance policy; and
- canonical manifest bytes whose `CheckpointArtifactDigest` is the unique
  content digest bound to that `CheckpointArtifactID`.

Missing replay inputs, an incomplete accepted journal, missing applicable
invariant results, a digest mismatch, or a failed applicable invariant prevents
sealing. A protected failure has a separate failure report and does not create
a checkpoint artifact at the failed boundary.

## 9. Results, failures, and accepted history

### 9.1 Typed lifecycle result

The application use case has the semantic signature:

```text
Run(context, request, observer) -> (SpineResult, error)
```

`SpineResult` contains:

- a closed spine status and, only after execution is established, its closed
  execution status;
- the compiled plan when compilation succeeded;
- every successfully compiled completeness profile required by a returned
  assessment;
- the last independently verified immutable state, when one exists;
- the dependency-closed accepted semantic-journal prefix;
- every independently verified sealed checkpoint in that prefix;
- every independently verified readiness assessment whose checkpoint and
  profile remain in that prefix;
- an optional compilation failure value when no plan exists; and
- at most one terminal semantic failure report.

The terminal `SpineStatus` values are `invalid_plan`, `succeeded`, and
`failed`. Once an execution exists, `ExecutionStatus` follows the HLD's closed
`pending`, `running`, `succeeded`, and `failed` lifecycle; returned terminal
results contain only `succeeded` or `failed`. The passing golden variant
returns spine/execution `succeeded`. The anchor-mismatch variant returns
spine/execution `failed`. Invalid compilation returns spine `invalid_plan` and
has no execution status.

Invalid compilation is a typed terminal result with no `PlanID`,
`SemanticRunID`, `ExecutionID`, or execution status. It carries a deterministic
compilation failure value rather than an execution failure report. A protected
invariant rejection after execution is established is also a typed semantic
result, not an exceptional Go error. The authoritative state and accepted
history stop at the last committed prefix.

Go errors are reserved for inability to perform the requested computation,
including cancellation, malformed or unsupported canonical input at the
internal request boundary, required infrastructure failure at an application
boundary, or an internal consistency defect. Deterministic semantic failures
are not retried.

The return combinations are contractual:

| Outcome | `SpineResult` | Typed failure value | Go error |
|---|---|---|---|
| Successful spine | populated `succeeded` | absent | nil |
| Invalid canonicalizable compiler request | populated `invalid_plan` | compilation failure value | nil |
| Protected semantic rejection | populated `failed` through the accepted predecessor | protected failure report | nil |
| Artifact-integrity rejection | populated `failed` through the last independently verified prefix | integrity failure report | nil |
| Machinery failure after completed work | populated `failed` through the last independently verified prefix | absent | non-nil |
| Machinery failure before any meaningful artifact exists | zero result | absent | non-nil |

“Independently verified prefix” is dependency-closed. Every returned state,
journal entry, invariant result, checkpoint, profile, and assessment has passed
its own integrity checks, and every artifact it references is also retained and
verified. A machinery error does not discard such work merely because later
work could not complete.

For the return contract, meaningful work begins when either a canonical
compilation failure value or a compiled plan has completed. Cancellation or
machinery failure before either exists returns the zero result. A failure after
plan compilation but before execution identity is established returns the plan
in a failed spine result without an execution status.

Cancellation uses `errors.Is` against context cancellation/deadline causes.
Required application-infrastructure failures and internal machinery failures
use distinct typed or sentinel causes. Neither control flow nor telemetry may
classify a Go error by parsing its message.

When an integrity failure implicates an artifact, that artifact and every
dependent checkpoint or assessment are excluded from the returned verified
prefix. The failure report records the last verified state or checkpoint only
when one exists; it never labels the implicated artifact authoritative.
Immutable bytes are not rewritten or deleted. Discovery against an artifact
that was already durably sealed would require the HLD's append-only quarantine
decision, which remains outside this in-memory slice.

### 9.2 Stable failure taxonomy

The closed `FailureKind` values for this slice are:

- `INVALID_PLAN`
- `PROTECTED_INVARIANT_FAILED`
- `ARTIFACT_INTEGRITY_FAILED`

A semantic artifact supplied to the kernel that fails deterministic integrity
validation produces `ARTIFACT_INTEGRITY_FAILED`. An impossible contradiction
created inside Maiden Lane's own implementation is an internal Go error; it is
not disguised as hostile input.

Execution failures are a closed tagged union. A
`ProtectedInvariantFailureReport` always carries
`PROTECTED_INVARIANT_FAILED`, `RuleID`, and a `ProtectedCode` tagged as either
an `OperationInvariantCode` or an `InvariantCode`. An
`ArtifactIntegrityFailureReport` always carries
`ARTIFACT_INTEGRITY_FAILED`, `IntegrityCode`, and `ArtifactKind`; it does not
pretend an artifact-integrity defect is a rule invariant. Both variants carry
their `SemanticRunID` and only the safe evidence applicable to that tag. The
protected variant carries its authoritative predecessor; the integrity variant
may carry only the last independently verified prefix reference.

Patch application itself returns typed operation-invariant results. When patch
application occurs inside an established transition, execution lifts a failing
result into the protected failure report without changing its code. Direct
patch unit tests may inspect the operation result without constructing an
application-level execution report.

The closed operation-invariant codes are:

- `OP_ENTITY_IDENTITY_COLLISION`
- `OP_UPDATE_TARGET_NOT_FOUND`
- `OP_BEFORE_IMAGE_MISMATCH`
- `OP_RELATION_ALREADY_PRESENT`
- `OP_RELATION_ENDPOINT_MISSING`

The closed invariant codes are:

- `DECLARED_SOURCE_NOT_FOUND`
- `DECLARED_SOURCE_KIND_INVALID`
- `TEAM_ASSIGNMENT_KEY_INVALID`
- `TEAM_ASSIGNMENT_KEY_MISMATCH`
- `TEAM_MEMBER_CARDINALITY_INVALID`
- `HOS_TUPLE_INCOMPLETE`
- `HOS_DURATION_INVALID`
- `HOS_ANCHOR_MISMATCH`
- `HOS_AGGREGATE_INVALID`

The closed readiness requirement codes are:

- `TEAM_ASSIGNMENT_KEY_REQUIRED`
- `TEAM_AGGREGATION_ANCHOR_REQUIRED`
- `TEAM_ELAPSED_DURATION_REQUIRED`
- `TEAM_DRIVING_DURATION_REQUIRED`

The minimum closed compilation diagnostic codes exercised by this slice are:

- `UNKNOWN_FIELD`
- `UNSUPPORTED_OPERATOR`
- `DECLARED_ACCESS_MISMATCH`
- `DEPENDENCY_CYCLE`
- `PROFILE_ORDER_UNPROVABLE`

The closed integrity codes are:

- `ARTIFACT_DIGEST_MISMATCH`
- `ARTIFACT_LINK_INCONSISTENT`
- `ASSESSMENT_IDENTITY_CONFLICT`
- `REPLAY_DIVERGENCE`

`OperationInvariantCode`, `InvariantCode`, `RequirementCode`,
`CompilationDiagnosticCode`, `IntegrityCode`, `ArtifactKind`, `FailureKind`,
`ReadinessVerdict`, `SpineStatus`, and `ExecutionStatus` are distinct named
types. Human-readable descriptions do not participate in identities and
cannot substitute for these stable codes.

The required boundary-to-code mapping is:

| Boundary and condition | Tagged protected code |
|---|---|
| Declared source reference does not resolve in S0 | invariant `DECLARED_SOURCE_NOT_FOUND` |
| Resolved source is not the declared entity kind | invariant `DECLARED_SOURCE_KIND_INVALID` |
| T1 source assignment key is absent or empty | invariant `TEAM_ASSIGNMENT_KEY_INVALID` |
| T1 source assignment keys differ | invariant `TEAM_ASSIGNMENT_KEY_MISMATCH` |
| T2 source HOS tuple is incomplete | invariant `HOS_TUPLE_INCOMPLETE` |
| T2 source duration is negative or has driving greater than elapsed | invariant `HOS_DURATION_INVALID` |
| Candidate team does not have exactly two distinct declared members | invariant `TEAM_MEMBER_CARDINALITY_INVALID` |
| T2 source anchors differ | invariant `HOS_ANCHOR_MISMATCH` |
| T2 emitted anchor, reduction, tuple shape, or final inequality is wrong | invariant `HOS_AGGREGATE_INVALID` |
| Inserted identity already exists | operation `OP_ENTITY_IDENTITY_COLLISION` |
| Update target does not exist in the staged candidate | operation `OP_UPDATE_TARGET_NOT_FOUND` |
| Existing update target's expected field/value before-image differs | operation `OP_BEFORE_IMAGE_MISMATCH` |
| Proposed relation already exists in predecessor or staged candidate | operation `OP_RELATION_ALREADY_PRESENT` |
| Proposed relation endpoint is absent after its declared staged dependencies | operation `OP_RELATION_ENDPOINT_MISSING` |

There is no catch-all protected code. A newly representable protected failure
requires an intentional closed-code and canonical-format change.

### 9.3 Failure-report evidence

A canonical `ProtectedInvariantFailureReport` contains:

- `SemanticRunID`;
- `PROTECTED_INVARIANT_FAILED`;
- one tagged `ProtectedCode` containing either an `OperationInvariantCode` or
  an `InvariantCode`;
- `RuleID`;
- predecessor `StateDigest`;
- canonical invariant results;
- sorted safe `EntityRef` values;
- sorted `InvariantEvidenceRef` and `FactRef` values; and
- an optional `ProposedPatchDigest` only if a patch was actually materialized.

A canonical `ArtifactIntegrityFailureReport` contains:

- `SemanticRunID`;
- `ARTIFACT_INTEGRITY_FAILED`;
- `IntegrityCode` and `ArtifactKind`;
- the implicated canonical `ArtifactRef`;
- optional `LastVerifiedStateDigest` and `LastVerifiedCheckpointArtifactID`,
  present only when independently verified;
- expected and observed digests where that comparison is safe and applicable;
  and
- canonically sorted safe artifact or evidence references.

Malformed transport bytes, an unsupported encoding version, or cancellation
before semantic execution is established remains a Go error. The typed
artifact report covers deterministic integrity rejection of a semantic
artifact inside an established run, including replay or sealing validation.

An invalid-plan compilation failure is a separate canonical value containing
its `CompilationInputDigest`, `INVALID_PLAN`, and a canonically ordered set of
typed compiler diagnostics. It cannot contain a semantic run, predecessor
state, committed history, or proposed patch because none exists.
`CompilationFailureDigest` identifies that diagnostic value, not the rejected
request; `CompilationInputDigest` identifies the canonicalizable request.
`FailureReportDigest` refers only to one tagged execution-failure variant
above.

Artifact evidence references are canonical semantic references within the
pinned input and state. They do not contain raw payloads, customer source IDs,
database primary keys, backend row locations, or human error strings.
Operational telemetry uses an even narrower representation and must not expose
entity references as metric dimensions.

## 10. Golden lifecycle variants

### 10.1 Passing variant

```text
driver:A = (assignment=X, anchor=T0, elapsed=10, driving=8)
driver:B = (assignment=X, anchor=T0, elapsed=7,  driving=6)
```

Expected lifecycle:

```text
spine status      = succeeded
execution status  = succeeded
T1 commits one atomic Insert+Relate+Relate patch
C1 seals
C1 / cm.v1        = ready
C1 / optimizer.v1 = needs_input
T2 commits one atomic Update patch
team aggregate    = (anchor=T0, elapsed=10, driving=8)
C2 seals
C2 / cm.v1        = ready
C2 / optimizer.v1 = ready
accepted journal  = [form_team.v1, aggregate_team_hos.v1]
```

### 10.2 Rejected anchor-mismatch variant

```text
driver:A = (assignment=X, anchor=T0, elapsed=10, driving=8)
driver:B = (assignment=X, anchor=T1, elapsed=7,  driving=6)
```

Expected lifecycle:

```text
spine status      = failed
execution status  = failed
T1 commits one atomic Insert+Relate+Relate patch
C1 seals
C1 / cm.v1        = ready
C1 / optimizer.v1 = needs_input
T2 rejects HOS_ANCHOR_MISMATCH before proposing a patch
C2 does not exist
accepted journal  = [form_team.v1]
C1 state, artifact, and assessments remain byte-identical
```

The passing and rejected S0 values differ, so their input, run, execution, and
checkpoint artifact identities need not match each other. Preservation is
asserted within the rejected execution: the C1 captured before attempting T2
is exactly the C1 retained after T2 fails.

## 11. Application orchestration and observability

### 11.1 Use-case boundary

`internal/app` orchestrates, in order:

```text
compile plan and profiles
pin S0, empty world, executor identity, and changes.v1 policy
execute T1
seal C1
assess C1 with cm.v1 and optimizer.v1
execute T2
if accepted: seal and assess C2
complete the spine result
```

The application layer does not reinterpret rules, patch meaning, invariants,
canonical bytes, or readiness. It invokes the semantic kernel and observes the
typed results.

After every completed step it advances the independently verified frontier
used by the `(SpineResult, error)` contract. Semantic rejection and machinery
failure share no return channel: the former populates a typed failure with nil
error, while the latter preserves that frontier and returns a non-nil error.

No new HTTP route, public API, CLI command, worker, database, or deployment
path is introduced. The use case is an internal operation exercised by tests
until a later slice deliberately exposes it.

### 11.2 Observer contract

The application package owns a narrow no-error observer interface with ordered
`BeginPhase` and `EndPhase` events. Both methods receive `context.Context` and a
closed app-owned observation value and return nothing. They cannot replace the
context used by semantic functions, return an error, alter control flow,
select a profile, change a verdict, or contribute to semantic identity.

The conceptual app-owned carrier is:

```text
PhaseObservation
  event: begin | end
  phase
  end-only result
  trace references
    optional ObservedPlanID
    optional ObservedSemanticRunID
    optional ObservedExecutionID
  bounded dimensions
    optional TransitionKind
    optional CheckpointKind
    optional ProfileKind
    optional tagged closed code
  bounded counts
```

This is a closed tagged value, not `map[string]any`, a list of arbitrary
attributes, an error string, or a generic digest channel. The trace-reference
allowlist is exactly `PlanID`, `SemanticRunID`, and `ExecutionID`, matching the
HLD's explicit diagnostic allowance. App wraps them in distinct observed-ID
types before they cross the interface.

For this slice the bounded kinds are:

- transition: `form_team.v1`, `aggregate_team_hos.v1`;
- checkpoint: `team_formed.v1`, `team_hos_aggregated.v1`; and
- profile: `cm.v1`, `optimizer.v1`.

The tagged code may contain only a closed compilation diagnostic,
operation-invariant, rule-invariant, integrity, readiness-requirement, or
machinery classification already defined by this design. Counts are
non-negative integers with field names fixed by the observation variant.

Checkpoint IDs, profile IDs, checkpoint artifact IDs, assessment IDs, state
digests, patch digests, journal digests, invariant-result digests, artifact
digests, entity references, evidence references, and arbitrary strings are not
trace-safe in this slice. A cryptographic digest is not presumed safe merely
because it is opaque.

`internal/observability` owns its corresponding closed OTel dimension types
`ObservationPhase`, `OperationKind`, `ObservationResult`, and `ProfileKind`,
and the exhaustive mapping from app observations into them. Trace recording
receives the explicitly allowed trace-reference projection. Metric helpers
receive a separate bounded-dimension/count projection whose type has no ID or
digest fields, and OTel views enforce the same attribute allowlist. This
preserves source dependency direction: app does not import observability.
Neither set is a semantic-kernel type. In particular, bounded `ProfileKind`
values such as `cm.v1` and `optimizer.v1` are operational classifications and
are never substitutes for canonical `ProfileID` values.

Telemetry runtime initialization and shutdown remain operational operations and
may return errors under the existing observability contract. Invalid required
telemetry configuration may block process startup. Export or shutdown failure
may affect operational process status, but it cannot change, discard, or
reinterpret a semantic artifact already produced.

The semantic result must be byte-identical with a recording observer, a
disabled observer, and an observer whose exporter fails asynchronously.

### 11.3 Traces

The boundary emits these spans:

- `maiden_lane.semantic.compile`
- `maiden_lane.semantic.execute_transition`
- `maiden_lane.semantic.seal_checkpoint`
- `maiden_lane.semantic.assess_readiness`
- `maiden_lane.semantic.execute_spine`

Every completed phase has an explicit terminal mapping:

| Semantic or operational result | Span status | Closed observation result |
|---|---|---|
| successful phase | OK | `success` |
| readiness `ready` | OK | `ready` |
| readiness `needs_input` | OK | `needs_input` |
| invalid plan | Error | `invalid_plan` |
| protected rejection | Error | `protected_invariant_failed` |
| artifact integrity failure | Error | `artifact_integrity_failed` |
| malformed or unsupported canonical input | Error | `invalid_input` |
| cancellation | Error | `cancelled` |
| required infrastructure unavailable | Error | `infrastructure_unavailable` |
| internal inconsistency | Error | `internal_error` |

No terminal phase is left with an implicit `UNSET` status.

Operational classifications come from the `Run` contract in Section 9.1:

- a typed malformed-input or unsupported-canonical-version cause maps to
  `invalid_input`;
- `context.Canceled` or `context.DeadlineExceeded`, detected with `errors.Is`,
  maps to `cancelled`;
- an explicitly typed required application-infrastructure cause maps to
  `infrastructure_unavailable`; and
- any remaining internal machinery inconsistency maps to `internal_error`.

The phase and outer spine observations end with that classification while the
returned `SpineResult` retains its independently verified prefix. Telemetry
runtime startup failure occurs before `Run`; asynchronous export failure cannot
change a phase result; shutdown happens after application work and may change
only the process's operational exit result, never the already-completed spine
result or span classification.

Trace attributes may contain only the three observed IDs, bounded kinds,
closed codes, phases, results, and bounded counts admitted by Section 11.2.
They must not contain state, patches, journal bodies, field values, anchors,
assignment keys, entity source keys, entity references, evidence bodies,
arbitrary digests, or error text.

### 11.4 Metrics

This slice adds these instruments:

| Instrument | Kind/unit | Attributes |
|---|---|---|
| `maiden_lane.semantic.phase.duration` | histogram, seconds | `phase`, `result` |
| `maiden_lane.semantic.structural.operations` | counter, operations | `operation_kind`, `result` |
| `maiden_lane.semantic.checkpoints` | counter, checkpoints | `result` |
| `maiden_lane.semantic.invariant.failures` | counter, failures | `invariant_code` |
| `maiden_lane.semantic.readiness.assessments` | counter, assessments | `profile_kind`, `verdict` |

The existing observability runtime registers these instruments because the
corresponding internal use case exists after this slice. With no public caller,
the production process records no semantic points yet; private application
tests exercise recording without inventing an HTTP or CLI surface.

The exact permitted dimension values are:

- phase: `compile`, `execute_transition`, `seal_checkpoint`,
  `assess_readiness`, `execute_spine`;
- phase result: `success`, `ready`, `needs_input`, `invalid_plan`,
  `protected_invariant_failed`, `artifact_integrity_failed`, `invalid_input`,
  `cancelled`, `infrastructure_unavailable`, `internal_error`;
- operation kind: `insert`, `relate`, `update`;
- operation result: `accepted`, `rejected`;
- checkpoint result: `sealed`, `rejected`;
- profile kind: `cm.v1`, `optimizer.v1`;
- readiness verdict: `ready`, `needs_input`; and
- invariant code: the closed `OperationInvariantCode` and `InvariantCode`
  values in Section 9.2.

`profile_kind` is a bounded operational category, not `ProfileID`. No semantic,
execution, checkpoint, assessment, attempt, artifact, customer, entity, trace,
or span identity may be used as a metric dimension.

Structural-operation counters record accepted operations only after the whole
patch commits. Each operation in a committed patch increments its kind once
with `accepted`; T1 therefore records one insert and two relates, while T2
records one update. If a materialized patch is rejected atomically, every
operation proposed in that patch increments its kind once with `rejected` even
though none committed. The pre-patch anchor mismatch records no update
operation because no patch existed.

The remaining recording rules are:

- phase duration records once when each started phase completes, with its
  exact terminal phase result; readiness phases use `ready` or `needs_input`,
  while other successful phases use `success`;
- checkpoint count records `sealed` only after a seal commits and `rejected`
  only when an actual seal request is refused; an unreached C2 is not a rejected
  checkpoint;
- invariant-failure count increments once for each produced failing protected
  invariant result, including the pre-patch anchor mismatch; and
- readiness-assessment count increments once for every completed immutable
  assessment, using its bounded profile kind and verdict. No C2 assessment is
  recorded when C2 does not exist.

These rules prevent telemetry from implying that an unmaterialized patch,
checkpoint, or assessment existed. The outer `execute_spine` duration always
receives the terminal result of the use case, even when a nested phase rejects.

Ordinary logs, if emitted, contain stable codes, phases, results, and bounded
counts rather than semantic payloads. The semantic journal remains the source
of provenance; logs and spans are not a second journal.

## 12. Verification contract

Implementation is test-first and must cover the following properties.

### 12.1 Compiler and profile tests

- Rules compile to exactly two typed transformations and two named checkpoint
  boundaries.
- Derived reads and writes match declarations; disagreement, unknown fields,
  invalid operators, dependency cycles, and unprovable profile ordering reject
  without a `PlanID` or `ProfileID` as applicable.
- Shuffled schema, rule, invariant, checkpoint, and profile declaration order
  yields identical canonical bytes and IDs when semantics are unchanged.
- Reversing the same two normalized source references preserves plan bytes;
  changing either reference changes `PlanID`. T2's declaration resolves only
  the typed output slot of T1 and never an inferred ambient team.
- T1's derived access contains assignment and structural facts but no HOS
  fields. T2's derived access contains every declared source/destination HOS
  field and closed predicate dependency.
- The compiler proves `cm.v1 <= optimizer.v1` from identical scope and
  requirement-set containment, not from profile names.

### 12.2 Patch and invariant tests

- T1's insert and two relations commit atomically or not at all.
- Intra-patch relation validation recognizes the staged insert while final
  graph validation still enforces referential integrity and exact cardinality.
- Relation and operation order is canonical under reversed driver input.
- T2's update carries the exact absent-field before-image and complete
  after-image.
- Direct patch tests construct invalid values whose first, second, or third
  operation fails and prove the predecessor remains unchanged; the production
  kernel gains no failure-injection callback or hidden test semantics.
- For the supported `Insert`, `Relate`, and `Update` subset,
  `Undo(Apply(S, patch), patch)` reproduces canonical predecessor bytes.
- Already-created state, patch, and journal values cannot be changed by
  mutating caller-owned maps or slices.
- Every protected failure occurs at its declared boundary with the exact
  operation- or rule-invariant code in Section 9.2 and leaves the accepted
  journal unchanged.
- Incomplete or unlawful source HOS rejects at T2, not T1, and preserves the
  already sealed C1 prefix exactly.

### 12.3 Golden incident tests

- The passing values produce C1 sealed/CM-ready/optimizer-needs-input and C2
  sealed/both-ready with team aggregate `(T0, 10, 8)`.
- The mismatch values produce a sealed CM-ready C1, reject T2 with
  `HOS_ANCHOR_MISMATCH`, materialize no T2 patch, append no T2 journal entry,
  and create no C2.
- The rejected attempt leaves the previously captured C1 state, checkpoint
  bytes, artifact ID, CM assessment, and optimizer assessment unchanged.
- Replaying S0 through the accepted T1 prefix reproduces C1; replaying a lawful
  C1 through the accepted T2 suffix reproduces C2.

### 12.4 Canonical and identity tests

- Shuffling input map insertion, relation declaration, progenitor input,
  evidence, invariant-result, journal-entry source construction, and profile
  requirement order never changes canonical output or identity.
- Fixed golden byte vectors and SHA-256 vectors protect each v1 encoding from
  accidental drift.
- Fixed vectors cover `CompilationInputDigest`, `PatchDigest`,
  `JournalEntryDigest`, `JournalPrefixDigest`, `InvariantResultDigest`, and
  `CheckpointArtifactDigest`, not only their enclosing artifacts.
- A sensitivity matrix proves that changing each semantic identity input
  changes the identities it should and does not change unrelated layers. In
  particular, executor and attempt changes cannot change checkpoint artifact
  identity, and profile changes cannot change plan or run identity.
- Two certified executor identities for the same semantic run can produce the
  same state, patch, journal-entry, journal-prefix, invariant-result,
  checkpoint-artifact ID, and checkpoint-artifact digest even though their
  `ExecutionID` values differ.
- Changing fixture observations preserves `InputLineageID` and source entity
  IDs but changes state/input/run descendants. Changing the lineage-root
  declaration changes `InputLineageID` and every source/synthetic descendant.
- One `CheckpointArtifactID` cannot resolve to two
  `CheckpointArtifactDigest` values.
- Sealing refuses missing replay inputs, incomplete accepted history, omitted
  applicable invariants, failed invariants, malformed canonical values, and
  digest mismatches.

### 12.5 Readiness properties

- Assessment scope is explicit and covers every selected team in a multi-team
  state; no failing team can be silently omitted.
- Empty scope has the documented vacuous-ready result.
- Assessments are checkpoint/profile-specific immutable values and never enter
  the accepted transition journal.
- Generated lawful states establish the implication
  `Ready(S, optimizer.v1) => Ready(S, cm.v1)` for the compiled ordering.

### 12.6 Observability boundary tests

- Semantic packages have no forbidden operational imports.
- The app observation carrier exposes no arbitrary metadata, error-text, entity
  reference, or generic digest channel. Its only trace IDs are `PlanID`,
  `SemanticRunID`, and `ExecutionID`.
- Metric recording APIs cannot receive trace-reference fields, and exported
  metric views retain only the documented bounded dimensions.
- Disabled, recording, and exporter-failing observation paths produce identical
  semantic result bytes.
- Every terminal path emits the documented span status/result mapping.
- Typed malformed/unsupported input maps to `invalid_input`, not
  `internal_error`; operational classification never parses error text.
- Metrics accept only the documented low-cardinality dimension values.
- Hostile field values, keys, entity references, journal bodies, and raw errors
  never enter metrics, spans, or ordinary logs.
- Existing runtime shutdown still drains application work before bounded
  telemetry shutdown.
- Existing health, configuration, and observability tests remain green.

### 12.7 Partial-result and integrity-frontier tests

- Protected rejection returns a populated failed result and nil Go error.
- Cancellation, typed infrastructure failure, and internal machinery failure
  after C1 return a non-nil Go error, no semantic failure, and the exact C1
  state, T1 journal prefix, sealed checkpoint, and completed assessments.
- Machinery failure before any meaningful artifact returns a zero result and a
  non-nil Go error.
- A suffix integrity failure preserves independently verified C1.
- An integrity failure implicating C1 excludes C1 and its dependent
  assessments, retaining only the earlier dependency-closed verified prefix.
- Integrity handling never mutates or deletes implicated immutable bytes.

### 12.8 Repository verification

Development proceeds through RED, GREEN, and REFACTOR. Before completion the
implementation must run, as applicable:

1. targeted package and golden-fixture tests;
2. `gofmt`;
3. targeted tests again;
4. `go vet ./...`;
5. configured static analysis;
6. `go test ./...`;
7. `go test -race ./...`;
8. `govulncheck ./...` when available;
9. binary and container build checks;
10. repository `make verify` or equivalent; and
11. final diff and generated-artifact inspection.

The implementation change updates `README.md`, the current Implementation
Guide, and the repository-root `METRICS.md` to describe what actually exists. It
updates `ERRORS.md` for each Maiden Lane-owned typed or sentinel machinery
error introduced by the `(SpineResult, error)` contract; standard context
cancellation remains documented behavior rather than a Maiden Lane-owned error
type. No OpenAPI change is expected.

## 13. Alternatives considered

### 13.1 HOS invariant placement

Three placements were considered:

1. T1 could validate source HOS merely because the observations are present.
   This was rejected because team formation does not use them and it couples a
   CM-ready prefix to suffix-only facts.
2. A general independent invariant language could express every fixture
   predicate. This was rejected because the walking skeleton does not justify
   another semantic language beside its two closed operators.
3. The chosen design puts generic structural checks in typed state/patch
   boundaries and HOS tuple, comparison, anchor, and reduction obligations in
   the closed `AggregateRelatedFields` declaration that uses those facts.

The third option preserves static derivation and fixture isolation without
teaching `FormRelatedEntity` HOS semantics.

### 13.2 Full HLD package topology now

One option was to create separate schema, rules, compiler, plan, model,
executor, invariants, journal, checkpoint, completeness, and fixture packages
immediately. Those are plausible long-term boundaries, but this slice is too
small to establish all of them from evidence. Doing so would turn provisional
design prose into speculative repository structure.

The compact semantic kernel is preferred until additional transformations make
a split clarify real ownership.

### 13.3 Team-HOS-only monolith

A single package containing the kernel and all team-HOS names would be smaller,
but it would allow a deliberately non-production reduction to become accidental
platform semantics. Isolating the fixture declaration proves the architecture
without teaching the generic kernel trucking doctrine.

### 13.4 `Merge` for team formation

`Merge` would imply that the two driver entities were consumed or replaced.
That is false for this incident: their observations remain authoritative inputs
to T2. `Insert + Relate` accurately represents association while preserving
progenitors and keeps `Merge` meaningful for future genuine consolidation.

### 13.5 Additive or minimum HOS reduction

Sum and minimum each assert an unapproved domain policy. Componentwise maximum
is chosen only for its deterministic, symmetric, order-independent, and closed
fixture behavior. The fixture caveat is part of the contract.

## 14. Explicit non-goals

This slice does not implement or design:

- production team-HOS semantics or a customer mapper rollout;
- HTTP, OpenAPI, CLI, or public API changes;
- persistence, databases, durable workers, or crash/resume;
- AWS deployment or orchestration changes;
- stochflow integration;
- promotion, gates, publication, comparison, quarantine, or rollback control
  planes;
- a broad DSL, generic rule language, or arbitrary code execution;
- the complete structural patch algebra;
- parallelism, transformer fusion, caching, or performance optimization;
- SQL/dbt or any alternative execution backend;
- a universal canonical serializer;
- broad customer configuration; or
- speculative packages mentioned only in exploratory implementation prose.

## 15. Implementation sequencing constraint

No implementation begins until this written design is ratified and a separate
implementation plan is written and approved. That plan must preserve the
dependency order:

```text
typed schema and closed rules
  -> deterministic plan
  -> atomic patch and accepted journal
  -> valid sealed checkpoint
  -> compiled completeness profile
  -> immutable readiness assessment
  -> application observation at the outer boundary
```

The implementation plan will assign independent file ownership to subagents
where work can be divided safely, retain one final integrator, and require an
independent invariant-focused review before completion.
