# Maiden Lane Glossary

**Status:** Non-normative reference

This glossary provides shared language for the [Maiden Lane High-Level
Design](docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)
and [Maiden Lane Inviolates](Inviolates.md). It does not create requirements or
override either source. The HLD is normative for the current design phase;
`Inviolates.md` becomes the highest repository-level architectural authority
only after it is explicitly ratified.

Terms are alphabetical. A definition describes Maiden Lane usage, which may be
narrower than the same word's general software-engineering meaning.

## Terms

**Accepted journal entry** — An immutable semantic record of a transition that
validated and committed. Rejected proposals and incomplete attempts do not
produce accepted entries.

**Artifact** — An immutable stored object referenced by identity or digest.
Examples include rules, plans, pinned worlds, states, journal segments, failure
reports, and outputs.

**Attempt / `AttemptID`** — One operational effort to complete an
`ExecutionID`. A retry receives a new `AttemptID`; it may change timing or
infrastructure placement but cannot change the fixed execution contract.

**Backend** — A physical implementation of the canonical semantic plan, such
as the reference Go executor or a future SQL/dbt executor. A backend implements
meaning defined by Maiden Lane; it does not define independent meaning.

**Backend certification** — Differential proof that a non-reference backend
produces the same required canonical state, semantic journal, semantic entity
identities, and invariant results as the reference executor. The backends have
different `ExecutionID` values because executor identity is part of the fixed
execution contract.

**Baseline** — The accepted or otherwise designated plan and executions against
which a candidate is evaluated over the same replay corpus and pinned world.

**Candidate** — A proposed plan, execution, checkpoint, state, or artifact under
evaluation. A candidate is not authoritative merely because execution reached
or produced it.

**Checkpoint / `CheckpointID`** — A named boundary after a complete prefix of a
canonical semantic plan. It identifies where a state and semantic-journal
prefix may be sealed; it is not a separate transformation plan or a consumer
route.

**Checkpoint artifact / `CheckpointArtifactID`** — The immutable realization of
a plan checkpoint for one semantic run. Its canonical identity binds the
checkpoint declaration, state digest, journal-prefix digest, applicable
invariant-result digest, and provenance policy. Producing-executor identity is
recorded separately and does not change canonical checkpoint identity.

**Canonical semantic representation** — Maiden Lane-owned, versioned bytes that
define semantic equality and content identity. Compression, archive metadata,
storage envelopes, and backend serialization are not part of this
representation unless the canonical format explicitly says otherwise.

**Certified path** — The Maiden Lane path that preserves every applicable
Inviolate and can produce artifacts eligible for normal publication.
Experimental or unsafe tooling exists outside this path.

**Comparison** — Paired evaluation of a baseline and candidate using the same
pinned input, historical world, replay corpus, and applicable comparison
policy. For checkpoint promotion it also uses the same completeness profile and
explicitly corresponding checkpoint semantics. Its result is evidence for the
promotion gate.

**Completeness** — Sufficiency of a valid checkpoint for a named consumer
contract. It is evaluated relative to a profile and is not an intrinsic
property of state.

**Completeness profile / `ProfileID`** — A closed, typed, immutable consumer
contract describing the information required for a particular use. A profile
declares assessment scope and aggregation, and assesses a checkpoint without
transforming state, waiving invariants, silently dropping non-ready entities,
or selecting a different pipeline. Its canonical content identity is
`ProfileID`.

**Comparison metric** — A domain measurement used to compare baseline and
candidate behavior and evaluate regression policy. It is part of semantic gate
evidence, not automatically an exported operational telemetry instrument.

**Content address** — An identity derived from an artifact's canonical semantic
representation. Equal canonical content has the same content address.

**Control plane** — Mutable operational metadata used to coordinate compilation,
execution, attempts, gates, and publication pointers. Control-plane records
refer to immutable artifacts rather than becoming the sole copy of semantic
history.

**Data plane** — Immutable semantic payloads such as inputs, worlds, states,
journals, before-images, and outputs. The current HLD places large data-plane
artifacts in content-addressed object storage.

**Digest** — The output of the configured hash over Maiden Lane-owned canonical
bytes. A digest implementation identifies bytes; it does not canonicalize Go
values or define their meaning.

**Entity / `EntityID`** — A typed node in semantic state and its stable identity.
Source entity identities derive from input lineage, source kind, and canonical
source key; synthetic identities derive deterministically from their semantic
construction.

**Evidence** — Immutable or content-addressed material supporting why a rule
fired, why a patch was accepted or rejected, or why a gate reached its verdict.
Semantic evidence belongs in provenance artifacts, not ordinary logs.

**Execution** — A physical realization of one semantic run under a fixed
executor identity and required provenance policy.

**Execution contract** — The complete fixed request identified by an
`ExecutionID`: the semantic run, executor identity, and required provenance
policy. The semantic plan is its backend-independent semantic contract;
executor and provenance choices define how that contract must be realized.
Changing semantic intent creates a new `SemanticRunID` and therefore a new
`ExecutionID`; changing only executor identity or provenance policy creates a
new `ExecutionID` for the same `SemanticRunID`.

**`ExecutionID`** — The deterministic identity of an execution contract for a
`SemanticRunID`. Operational retries preserve it and receive distinct
`AttemptID` values.

**Execution invariant** — A dynamic property required across a declared
checkpoint prefix or the complete execution result, after individual
operations and rules have been evaluated.

**Executor** — A backend component that consumes the canonical semantic plan and
pinned inputs, proposes and applies validated structural patches, and emits the
required semantic provenance.

**Failure report** — A separate immutable record for a rejected proposal,
failed invariant, integrity failure, or other non-committed semantic outcome.
It is not an accepted journal entry.

**Gate verdict** — The promotion gate's explicit result, such as not evaluated,
pass, or fail. A successful execution does not imply a passing verdict.

**Input state (`S0`)** — The canonical state supplied before execution begins.
It is evaluated together with a pinned world.

**`InputID`** — The content identity of the canonical input state and pinned
world used by a semantic run.

**Invariant** — A formal property checked during compilation, patch application,
rule evaluation, execution validation, or promotion. An invariant is not an
Inviolate, although invariants often enforce Inviolates.

**Invariant code** — A stable, bounded identifier for a particular invariant or
violation class. It can appear in safe operational metadata without exposing
the customer values that caused the result.

**Inviolate** — A named, numbered project law that design, implementation,
operation, and review may not cross. The term is intentionally distinct from
the formal term *invariant*.

**Logical immutability** — The requirement that semantic values cannot visibly
change after identification. Implementations may use copy-on-write structures,
transactions, chunking, or deduplication while preserving immutable external
semantics.

**`needs_input`** — A successful readiness-assessment verdict meaning a valid
checkpoint does not yet satisfy one completeness profile. It is not an
invariant violation, execution failure, or instruction to retry unchanged
semantic inputs.

**Operation invariant** — A dynamic property required for one structural
operation, such as the existence of referenced entities, matching
before-images, valid cardinality, and non-colliding identities.

**Operational metric** — An exported telemetry instrument used for dashboards,
alerts, and system operation. Operational metrics are registered in
[`METRICS.md`](METRICS.md) and are distinct from semantic comparison metrics.

**Patch** — An explicit proposed structural delta containing operations and the
before-state information or immutable references needed for attribution,
validation, replay, and diagnostic inversion.

**Pinned** — Fixed by immutable identity for the life of an execution,
comparison, or replay. A pinned input cannot silently resolve to newer mutable
content.

**Plan / semantic plan** — The deterministic, immutable, inspectable,
backend-independent execution contract produced by compiling typed rules and a
schema. It contains named checkpoint boundaries as well as transformations.
Executors consume this plan rather than reinterpreting authored rules.

**`PlanID`** — The content identity of a canonical semantic plan.

**Promotion** — Evaluation of whether a selected sealed checkpoint has the
passing readiness assessment, complete evidence, protected invariant results,
comparison result, and backend certification required for one publication
target.

**Promotion gate** — The fail-closed application boundary that produces a gate
verdict. It does not publish or rerun transformation logic.

**Protected invariant** — An invariant whose failure makes publication
impossible through the certified path. It cannot be waived as a soft quality
decision.

**Profile ordering** — A compiler-proven implication between completeness
profiles. `A <= B` means every state ready under `B` must also be ready under
`A`. Profiles form a partial order rather than a required global numeric scale.

**Provenance** — The semantic evidence describing what changed, which rule
caused it, why the decision was made, and which invariants were evaluated. It
describes semantic events rather than backend mechanics.

**Provenance policy** — The fixed evidence obligation of an execution. The HLD
defines `summary`, `changes`, and `full` policies; `changes` is the minimum
publishable policy, while `summary` alone is not publishable.

**Publication** — The authorized compare-and-swap update of a versioned pointer
to an already validated immutable artifact. Publication is separate from
execution and promotion and never reruns transformations or readiness
assessment.

**Publication target / `TargetID`** — A tenant- and customer-scoped consumption
purpose, such as CM upload or optimizer input, with its own versioned
publication pointer and an explicit immutable, versioned policy binding its required
profile. A publication record pins the target-policy version, profile,
assessment, and checkpoint that authorized the pointer update.

**Quarantine** — An append-only control-plane decision recording that a
previously sealed checkpoint has an integrity defect or violated a protected
property applicable to that checkpoint. Quarantine blocks new certified
publication without mutating or deleting the checkpoint artifact.

**Readiness assessment / `AssessmentID`** — Deterministic evaluation of one
sealed checkpoint under one pinned completeness profile. Its result is
`ready` or `needs_input` plus safe evidence and stable requirement codes.

**Record lineage** — The explicit genealogy of a logical source record across
semantic runs. New evidence may create a descendant run linked to a parent
checkpoint while preserving earlier immutable states and their historical
meaning.

**Reference executor** — The trusted executable specification used to certify
other backends. The Go executor holds this role unless an architectural
amendment deliberately reassigns it.

**Replay** — Re-execution from content-identified semantic inputs and a pinned
historical world. Repeating an `ExecutionID` must reproduce its identical
canonical semantic representation or report an integrity failure.

**Replay corpus** — A designated collection of pinned inputs and worlds used to
compare baseline and candidate behavior.

**Rollback** — Restoration of a prior publication pointer to an existing
validated immutable artifact. Inverse patch application supports replay and
diagnosis but is not the normal production rollback mechanism.

**Rule / `RuleID`** — A typed declaration of transformation intent and its
stable semantic identity. The compiler derives rule access and dependency
behavior from the closed rule model.

**Rule invariant** — A dynamic property required before or after one rule, such
as coherent aggregation, valid elapsed-time relationships, or declared
structural cardinality.

**Rule set** — The immutable, content-identified collection of authored rules
compiled together into a semantic plan.

**Run-affecting configuration** — Configuration capable of changing semantic
inputs, interpretation, output, provenance, gate verdict, or publishability. It
is explicit and pinned rather than discovered from ambient process state.

**Sealed checkpoint** — A checkpoint artifact whose immutable state, required
journal prefix, applicable protected-invariant results, and canonical digests
are complete and internally consistent. Sealing establishes validity and
replayability, not consumer readiness or publication eligibility.

**Semantic artifact** — An immutable, canonical value that influences or
records semantic computation, including inputs, worlds, rule sets, plans,
states, checkpoints, completeness profiles, readiness assessments, journals,
and outputs.

**Semantic identity** — An identity determined only by declared semantic
content and canonicalization rules. Clocks, randomness, attempt metadata,
backend row order, and storage encoding cannot affect it.

**Semantic journal** — The ordered, immutable provenance of accepted semantic
transitions. It records structural meaning and evidence, not SQL statement
numbers, warehouse row ranges, or job mechanics.

**Semantic run / `SemanticRunID`** — The requested computation identified by
`InputID` and `PlanID`, independent of executor choice and provenance policy.
One semantic run may have multiple executions. New human or reference evidence
creates a descendant semantic run rather than altering an existing run's pinned
inputs.

**Soft quality policy** — A non-protected gate criterion that may permit a
separately authorized and journaled decision. It cannot waive a protected
invariant or an Inviolate.

**State** — An immutable typed entity graph containing entities, explicit
relations, a schema identity, and a canonical state digest.

**Structural operation** — A semantic state change such as insert, delete,
update, merge, split, relate, or unrelate. Its meaning is independent of how a
backend physically evaluates it.

**Synthetic entity** — An entity created by a semantic operation rather than
read directly from source input. Its identity is derived under a versioned
namespace from the input lineage, entity kind, `RuleID`, canonical progenitor
identities, and declared semantic output key.

**Transformer** — The semantic primitive that reads immutable state and a
pinned world, then proposes a structural patch. It does not mutate shared or
published state directly.

**Validity** — The property that a state or checkpoint satisfies every
protected semantic invariant applicable to it. Validity is consumer-independent
and distinct from completeness: a valid checkpoint may be insufficient for a
particular profile.

**World** — The immutable, content-identified execution context containing
reference datasets, schemas, catalogs, policies, and other external facts that
can affect semantic results. Additional evidence changes the world or input and
therefore cannot be introduced into an already identified semantic run.
