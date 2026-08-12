# Maiden Lane Inviolates

**Status:** Ratified; highest repository-level architectural authority

**Ratified:** 2026-08-12

## Purpose

This document intentionally uses **Inviolate**, not *invariant*.

An **Inviolate** is a project law: a boundary that Maiden Lane's design,
implementation, operation, and review process may not cross. An **invariant** is
a formal property checked by the compiler, executor, promotion gate, or another
mechanism. Invariants may enforce Inviolates, but the terms are not
interchangeable.

This document is the highest repository-level architectural authority. Its
ratification establishes the following amendment discipline:

- Inviolate numbers never move and are never reused.
- A retired Inviolate remains as a numbered tombstone that identifies its
  replacement or explains why it no longer applies.
- Changing the meaning of an Inviolate requires explicit approval and a
  deliberate amendment. Affected designs, contracts, tests, and authority
  references change with it; implementation cannot silently reinterpret it.
- Operational difficulty is evidence for a proposed amendment, not permission
  to route around an Inviolate.
- A demonstrated Inviolate violation blocks approval regardless of passing
  tests or apparent operational success.

## 0. Run-affecting configuration is explicit state

Operational configuration may come from files, environment variables, or
configuration libraries. Anything capable of changing a plan, semantic input,
world interpretation, result, provenance, gate verdict, or publishability must
instead be supplied explicitly, pinned, and identified as part of the run.
Semantic code may not discover it from ambient configuration.

## 1. There is one closed, typed source of semantic meaning

Rules are expressed through Maiden Lane's defined semantic model and compile
into one canonical semantic plan. Arbitrary Go functions, SQL fragments,
dynamic field names, hidden access, and backend-specific interpretations are
not part of the certified path.

## 2. Semantic and execution identities are separate and deterministic

`SemanticRunID` identifies semantic intent. `ExecutionID` identifies a fixed
execution contract, including executor identity and required provenance policy;
changing either creates a new `ExecutionID` without changing the
`SemanticRunID`. `AttemptID` identifies an operational attempt only. Clocks,
UUIDs, randomness, backend serialization, generated SQL, row order, and attempt
details cannot affect semantic identity. Synthetic entity identities must be
derived deterministically from declared semantic inputs and keys. Checkpoint,
completeness-profile, and readiness-assessment identities are likewise derived
only from their declared canonical semantic inputs.

## 3. Semantic artifacts are immutable, canonical, and content-addressed

Inputs, worlds, rule sets, plans, states, checkpoints, completeness profiles,
readiness assessments, journals, and outputs cannot mutate beneath an
execution, comparison, publication, or replay. Maiden Lane owns their canonical
representation; storage and hashing adapters do not decide what the bytes
mean.

## 4. Validation fails closed

Static plan validation and dynamic operation, rule, and execution invariants
are distinct gates. Statically invalid programs produce no plan. Failed
protected invariants produce no publishable artifact. Invalid semantics are
never accepted on a best-effort basis. A consumer-specific `needs_input`
verdict is not an invariant failure and cannot be used to declare a lawful
checkpoint invalid.

## 5. Execution never publishes directly

Executors evaluate a semantic plan and produce candidate artifacts, including
sealed checkpoints. Publication is a separate, authorized, validated, atomic
pointer update to an already immutable artifact; it never reruns transformation
or completeness-assessment logic.

## 6. State transitions are structural, attributable, and atomic

Inserts, deletes, updates, merges, splits, relations, and other changes are
represented as explicit semantic patches. A proposed transition either
validates and commits completely with adequate evidence or does not alter
authoritative state.

## 7. Committed history means committed semantics

Accepted journal entries describe only transitions that became authoritative.
Rejected proposals, invariant violations, and incomplete attempts belong in
separate immutable failure or operational records. A sealed checkpoint remains
immutable if downstream work fails; a later integrity discovery produces an
append-only quarantine record rather than rewritten history.

## 8. Replay is exact, not approximate

Every input capable of affecting execution—including data, world state,
schemas, rules, policies, catalogs, and reference data—is pinned. Repeating an
`ExecutionID` returns its existing artifacts or reproduces the identical
canonical semantic representation. Compression, container formats, archive
metadata, storage envelopes, and other physical encodings do not define
semantic equality. Divergence in the canonical semantic representation is an
integrity failure. New human or reference evidence creates a descendant
semantic run rather than silently changing the pinned world of an existing
execution.

## 9. Comparisons share the same world

Baseline and candidate executions must use the same pinned input, historical
world, replay corpus, completeness profile, corresponding checkpoint semantics,
and applicable comparison policy. Results produced from materially different
worlds or readiness contracts cannot support a promotion claim.

## 10. Promotion requires complete evidence

A candidate checkpoint cannot be promoted until that checkpoint is sealed, its
pinned completeness assessment is `ready`, comparison completes successfully,
required provenance exists, every protected gate passes, and the executor is
certified for the requested policy. Downstream execution may still be running
or may later fail after the selected checkpoint; that does not retroactively
block the sealed prefix. There is no `force` path around these requirements.

## 11. Backend choice and optimization cannot change meaning

Go, SQL, dbt, and future backends implement the canonical semantic plan; they
do not reinterpret the original rules. The Go executor is the executable
reference specification unless that role is deliberately reassigned by an
architectural amendment. Every non-reference backend must prove equivalence to
the reference executor. Fusion, concurrency, caching, and pushdown are
permitted only when required state, journal, identity, and invariant results
remain equivalent.

## 12. Infrastructure is subordinate to semantics

Semantic packages perform no I/O and do not observe clocks, randomness,
environment variables, filesystems, networks, global state, or mutable external
catalogs implicitly. AWS, databases, transports, and stochflow remain behind
Maiden Lane-owned boundaries. Stochflow infrastructure may implement narrow
ports but cannot import its domain vocabulary or semantics into Maiden Lane.

## 13. Boundary layers do not invent business meaning

HTTP handlers, persistence adapters, dispatchers, storage layouts, and
deployment orchestration translate, store, retrieve, or schedule decisions
made by the application and semantic model. They do not define
transformations, infer rules, weaken gates, or independently authorize
publication.

## 14. Provenance must support the claim being made

Human-readable summaries and rule-firing counts are insufficient for
publication. Publishable history preserves actual structural changes, evidence,
invariant results, and authoritative before-state values or immutable
references. An executor unable to produce the requested provenance is not
certified for that execution.

## 15. No mechanism within the certified path may weaken an Inviolate

Configuration, feature flags, operators, adapters, migration mechanisms, and
convenience APIs may narrow supported behavior but cannot bypass these laws.
Experimental or unsafe tooling may exist only outside the certified Maiden Lane
path, must advertise its degraded guarantees explicitly, and cannot produce
artifacts eligible for certified publication.

## 16. Tenant scope and authorization are explicit

Every external operation and infrastructure port carries explicit tenant and
customer scope. Possession of an entity, plan, run, execution, or artifact
identifier grants no authority. Scope may not be inferred from storage keys,
ambient process state, or caller convention.

## 17. Customer data is not observability or control-plane metadata

Raw payloads, sensitive rules, journal bodies, and credentials do not appear in
ordinary logs, traces, metrics, errors, job arguments, or control-plane records.
Unbounded customer and semantic identifiers are not metric dimensions. Access
to detailed provenance is separately authorized and audited.

## 18. Deterministic semantic failures are permanent for that execution

Invalid plans, protected invariant failures, replay divergence, and backend
divergence are not retried in the hope of a different answer. Retries are
reserved for classified transient infrastructure failures and may create a new
`AttemptID`, but cannot alter the semantic inputs or any part of the
`ExecutionID`'s fixed execution contract, including executor identity or
provenance policy. `needs_input` is not a retryable failure; new evidence
creates a new assessment or descendant semantic run with new semantic inputs.

## 19. Completeness is consumer-relative and never forks semantics

Maiden Lane has one canonical transformation spine with explicit immutable
checkpoints. Completeness is evaluated by a closed, typed, pinned profile over
a checkpoint; it is not an intrinsic property of state. A lawful checkpoint
may be ready for one consumer and `needs_input` for another. Profiles assess
state but cannot transform it, waive protected invariants, or select alternate
consumer pipelines. Assessment scope and aggregation are explicit; a profile
cannot silently drop a non-ready in-scope entity. Publication is
consumer-target scoped; the target's immutable, versioned policy explicitly binds the
required profile, and every published pointer records that policy version,
checkpoint, profile, and readiness assessment.
