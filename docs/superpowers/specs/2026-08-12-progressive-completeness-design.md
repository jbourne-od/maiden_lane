# Progressive Completeness and Consumer-Scoped Publication Design

**Status:** Approved design amendment

**Date:** 2026-08-12

**Normative architecture:** [Maiden Lane High-Level Design](2026-08-11-maiden-lane-high-level-design.md)

**Highest repository authority:** [Ratified Maiden Lane Inviolates](../../../Inviolates.md)

## 1. Purpose

Maiden Lane must support consumers with materially different information
requirements without creating separate transformation pipelines. Commitment
Manager may lawfully use a sparse record that would be unusable by the
optimizer. Requiring optimizer completeness everywhere would delay useful CM
work; maintaining separate CM and optimizer transformations would duplicate
semantics and allow them to drift.

This design makes completeness a typed assessment over one progressive state
lineage. It separates:

1. whether a checkpoint is a valid semantic artifact;
2. whether that checkpoint is ready for a named consumer contract; and
3. whether it may be promoted and published to a particular target.

Maiden Lane no longer defines one globally complete record. It defines valid,
immutable states of knowledge, while consumers explicitly identify how much
knowledge they require before acting.

## 2. One transformation spine

A canonical plan defines one ordered transformation spine with named semantic
checkpoints:

\[
S_0 \xrightarrow{T_1} \cdots \xrightarrow{T_k}
\underbrace{S_k}_{C_{interpreted}}
\xrightarrow{T_{k+1}} \cdots \xrightarrow{T_n}
\underbrace{S_n}_{C_{enriched}}
\]

A checkpoint is a declared plan boundary, not a second plan and not a routing
decision. Each realized checkpoint identifies the immutable state at that
boundary and the corresponding semantic-journal prefix.

Consumers do not select different transformation semantics. They assess and
publish suitable checkpoints from the same spine. Execution still realizes the
one requested semantic run; this amendment does not introduce partial-run
execution contracts or early-stop identity semantics.

## 3. Validity, completeness, and readiness

Validity and completeness answer different questions:

\[
Valid(C_k)
\]

means the checkpoint is internally lawful under every protected invariant
applicable to its plan prefix.

\[
Readiness(C_k, P) \rightarrow
\{ready, needs\_input\} + evidence
\]

means the checkpoint does or does not satisfy completeness profile `P`.

A profile may require typed field presence, value validity, relations,
cardinality, or other closed semantic predicates. It cannot transform state,
invent data, waive a protected invariant, or select an alternate pipeline.
Profiles compile deterministically against a schema into immutable,
content-addressed contracts.

A profile also declares the entity and relation scope it assesses and how
individual results aggregate into the target verdict. It cannot silently omit
an entity that fails its requirements. If a target intentionally consumes a
subset, that selection must be an explicit typed output contract with
attributable provenance rather than an assessment side effect.

`needs_input` is a normal readiness verdict. It is not an invalid state, a
failed execution, or a retryable error. Its assessment artifact records stable
requirement codes and safe evidence references without exposing customer data
in operational metadata.

Consequently, one checkpoint may be ready for CM and not ready for the
optimizer without contradiction:

```text
Checkpoint C7
  valid:          yes
  CMReady:        ready
  OptimizerReady: needs_input
```

## 4. The completeness dial

The completeness dial is an explicit implication relationship between typed
profiles, not a scalar score or a collection of arbitrary thresholds.

For example:

\[
P_{CM} \preceq P_{Optimizer}
\]

means the profile compiler can prove:

\[
Ready(S, P_{Optimizer}) \Rightarrow Ready(S, P_{CM})
\]

Profiles form a partial order because future consumer contracts may be
incomparable. The design does not force every use case onto one global linear
scale. Profile ordering also does not claim that every transformation
monotonically adds fields; each checkpoint is assessed independently.

## 5. Sealed checkpoints

A checkpoint becomes **sealed** only when:

- its state has an immutable canonical digest;
- its required semantic-journal prefix is complete;
- every protected invariant applicable to that prefix passes;
- its state, journal, and invariant-result digests are internally consistent;
- every semantic input required for exact replay is pinned.

Sealing says that a checkpoint is a complete semantic artifact. It does not
say that the checkpoint is ready for every consumer or publishable to any
target. Replaying a sealed checkpoint is required to reproduce its identical
canonical representation; divergence is an integrity defect.

A downstream semantic failure cannot retroactively invalidate a sealed
checkpoint. If a later discovery proves that the checkpoint itself had an
integrity defect or failed an invariant applicable to that checkpoint, Maiden
Lane records an append-only quarantine decision. Quarantine prevents new
certified publication and triggers the appropriate operational response; it
does not rewrite or delete immutable history.

## 6. Identity model

Completeness introduces identities without changing the meaning of an
existing semantic run:

\[
ProfileID = H(CanonicalProfile)
\]

\[
CheckpointArtifactID = H(
SemanticRunID,
CheckpointID,
StateDigest,
JournalPrefixDigest,
InvariantResultDigest,
ProvenancePolicy)
\]

\[
AssessmentID = H(CheckpointArtifactID, ProfileID)
\]

The canonical profile includes its format and compiler semantics. A realized
checkpoint artifact includes its declared checkpoint identity, state digest,
journal-prefix digest, applicable invariant-result digest, and provenance
policy. Executor identity is recorded with the production event but does not
enter canonical checkpoint identity; certified backends must produce the same
checkpoint artifact at the same provenance policy.

Changing a profile changes `ProfileID` and `AssessmentID`; it does not change
`PlanID`, `SemanticRunID`, or the historical checkpoint. Adding a consumer
therefore does not reinterpret an existing transformation.

## 7. Record lineage and semantic-run lineage

New evidence cannot be smuggled into an existing execution's pinned world.
Human input or newly acquired evidence creates a descendant semantic run:

```text
SemanticRun A
  C0 -> C1 -> C2 (sealed)
                   |
                   | new evidence E
                   v
SemanticRun B
  parent checkpoint: A:C2
  evidence digest:   H(E)
  C0' -> C1' -> ...
```

The record lineage continues and stable source identities remain traceable,
but the new pinned input or world produces an honest new `InputID` and
`SemanticRunID`. Earlier checkpoints remain immutable descriptions of what was
lawfully knowable from their historical evidence.

This permits records to mature without asking a customer to re-upload data and
without pretending later evidence existed during the original run.

## 8. Promotion and publication

Promotion targets a sealed checkpoint under a pinned completeness profile and
a named publication target:

\[
Promotable(Target, C_k, P) =
Sealed(C_k)
\land Ready(C_k, P)
\land GatesPass(Target, C_k, P)
\]

The full execution may still be running or may later fail after the selected
checkpoint. The selected checkpoint prefix must have completed and sealed
successfully. This allows CM to use a lawful state while optimizer-only work
continues or awaits more information without creating a partial-run execution
contract.

Publication pointers are keyed by tenant, customer, and target. A target is a
stable consumption purpose such as CM upload or optimizer input. An immutable,
versioned target policy explicitly binds the profile required for publication. The
publication record pins that policy version, `ProfileID`, `AssessmentID`,
checkpoint artifact, semantic run, and execution that authorized the update.
CM and optimizer may therefore point to different checkpoints in one record
lineage and advance independently.

Profile identity is recorded with the publication but is not hidden inside the
target key. When a target's required profile changes, the policy change is
explicit and the next publication must satisfy the new pinned profile.

Publication remains an authorized compare-and-swap pointer update. It never
reruns transformations or readiness evaluation.

## 9. Comparison

A promotion comparison must use corresponding checkpoint semantics, the same
completeness profile, and the same historical input/world corpus:

\[
Compare(C_a, C_b, P, W)
\]

Comparison identity includes the profile and checkpoint correspondence.
Comparing an optimizer-ready baseline with a merely CM-ready candidate cannot
support a promotion claim. Backend certification likewise compares checkpoint
state, journal-prefix, and invariant-result digests rather than only the final
run output.

## 10. Failure behavior

The lifecycle distinguishes ordinary incompleteness from failure:

| Condition | Meaning | Consequence |
|---|---|---|
| `needs_input` assessment | Valid state is insufficient for one profile | Target cannot advance; other targets are unaffected |
| Downstream semantic failure | A later transition did not commit | Earlier sealed checkpoints remain eligible |
| Checkpoint invariant failure | The checkpoint prefix is not valid | Checkpoint cannot seal |
| Checkpoint integrity defect | Previously sealed artifact cannot support its claim | Append-only quarantine; no new certified publication |
| New human or reference evidence | The pinned semantic inputs changed | Create a descendant semantic run |

Deterministic readiness evaluation is not retried for a different answer. A
profile change creates a new assessment over the same checkpoint. New evidence
creates a descendant semantic run and assessments over its new checkpoints; an
operational retry does neither.

## 11. Required verification

Future implementation must establish at least these properties:

1. One checkpoint can be ready for CM and `needs_input` for the optimizer.
2. A declared ordering `CM <= Optimizer` satisfies the readiness implication
   for generated states.
3. Readiness assessment never changes the checkpoint state or journal.
4. Assessment cannot silently omit a non-ready in-scope entity.
5. Changing a profile changes assessment identity but not plan, semantic-run,
   or checkpoint identity.
6. A downstream failure does not invalidate a sealed prefix checkpoint.
7. A checkpoint cannot seal without applicable protected invariants and the
   required journal prefix.
8. A target cannot publish a failed or mismatched assessment.
9. CM and optimizer publication pointers advance independently.
10. New evidence creates a new semantic run with explicit checkpoint ancestry.
11. Cross-profile or non-corresponding-checkpoint comparisons fail closed.
12. Certified backends produce identical canonical checkpoint artifacts.

## 12. Scope effect

This amendment changes the HLD's semantic lifecycle, promotion unit, future API
concepts, and persistence vocabulary. It does not change the approved initial
repository scaffold: that slice implements only process lifecycle, health
endpoints, local verification, a container, and CI/CD. No completeness types or
placeholder packages should be added during scaffolding.
