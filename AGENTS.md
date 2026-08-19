# AGENTS.md

## Purpose

Maiden Lane is a deterministic transformation system for compiling, executing, explaining, comparing, and gating mapper transformations.

The primary engineering objective is not merely to produce correct output on known examples. It is to make broad classes of invalid mapper states unrepresentable or non-publishable, while preserving enough information to explain, replay, compare, and audit every accepted transformation.

Optimize for:

1. Correctness and preservation of invariants.
2. Determinism and reproducibility.
3. Semantic clarity.
4. Auditability and explainability.
5. Operational simplicity.
6. Measured performance.

Do not trade a higher-ranked property for a lower-ranked one without an explicit architectural decision.

---

# 1. Read Before Changing Code

Before making a nontrivial change:

1. Read this `AGENTS.md`.
2. Read the ratified [**Maiden Lane Inviolates**](Inviolates.md).
3. Read the [**Maiden Lane High-Level Design**](docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md).
4. Read the current [**Implementation Guide**](docs/implementation/implementation-guide.md).
5. Inspect the relevant packages, tests, schemas, and interfaces.
6. Inspect `git status` and preserve unrelated work.
7. Look for more specific `AGENTS.md` files in the package subtree.

Do not infer architecture from filenames or current implementation alone. The codebase may intentionally be incomplete relative to the design.

For bug fixes, understand the violated invariant before changing implementation.

For architectural work, understand the intended semantic boundary before writing code.

---

# 2. Authority and Decision Hierarchy

This hierarchy governs normal implementation work. If a task explicitly authorizes changing the HLD or an API, schema, format, or behavioral contract, that artifact is in scope for intentional revision and is not authority against its own change. All unaffected Inviolates, protected invariants, contracts, and tests retain their normal priority.

An ordinary task does not authorize changing an Inviolate. That requires an
explicitly approved amendment following `Inviolates.md`, with affected
authorities, designs, contracts, and tests updated deliberately.

When sources disagree, use this order:

1. **[Ratified Maiden Lane Inviolates](Inviolates.md)**
2. **Maiden Lane High-Level Design**
3. **Explicit API/schema/format contracts and authoritative behavioral tests**
4. **Current task requirements**
5. **Implementation Guide**
6. **Existing implementation**
7. **Historical patterns and convenience**

An implementation that contradicts the HLD is not evidence that the HLD should be ignored.

A test that contradicts a protected invariant is not authoritative merely because it currently passes.

Existing code is evidence of current state, not necessarily desired architecture.

## 2.1 High-Level Design

The **High-Level Design is normative subject to the ratified Inviolates**.

Follow it unless doing so would violate an Inviolate or reveal a genuine contradiction in the design.

Do not casually reinterpret, route around, or weaken the HLD to make implementation easier.

If implementation exposes a flaw in the HLD:

* identify the conflicting Inviolate, contract, or assumption explicitly;
* do not silently implement a different architecture;
* make the architectural change intentional and reviewable;
* update the HLD when the architecture itself changes.

Changes to the HLD should be comparatively rare and should describe durable architecture, not transient implementation state.

## 2.2 Implementation Guide

The current **Implementation Guide is deliberately non-normative**. The [**Provisional Go Implementation Sketch**](docs/implementation/2026-08-11-provisional-go-implementation-sketch.md) remains exploratory context only; it is not a description of the current implementation, a sequencing plan, or authorization to scaffold its candidate files.

Treat it as a living description of:

* what currently exists;
* what has been implemented;
* current package and component boundaries;
* temporary implementation choices;
* known gaps.

Implementation plans own future sequencing and intended implementation
direction. Do not put planned package layouts or future capabilities in the
Implementation Guide before the corresponding implementation exists.

It is a guide, not a contract.

Expect it to change frequently as implementation teaches us more.

Once the guide exists, update it in the same change whenever implementation materially changes so that it describes the repository as it now exists.

Do not preserve stale implementation-guide text for historical value. **Git is the history.**

Do not use the Implementation Guide to override an Inviolate, invariant, or the HLD.

Do not turn temporary implementation choices into permanent architecture merely because they were written in the guide.

---

# 3. Core Architectural Invariants

These are not suggestions.

## 3.1 Maiden Lane owns transformation semantics

Business meaning belongs to Maiden Lane's semantic model.

Execution technologies are implementations of that meaning.

Do not encode independent business semantics directly in:

* SQL;
* dbt;
* HTTP handlers;
* persistence adapters;
* AWS orchestration;
* generated code;
* customer-specific escape hatches.

A future SQL/dbt backend consumes the canonical semantic plan. It does not independently reinterpret the original rule source.

There must be one source of semantic meaning.

## 3.2 The semantic plan is the execution contract

Rules compile into a deterministic, immutable, inspectable, backend-independent semantic plan.

Executors implement the plan.

Do not introduce backend-specific meaning into the canonical plan unless the HLD explicitly defines that capability as semantic.

A backend optimization must preserve observable semantics.

## 3.3 Semantic code is pure

Domain and semantic packages must not implicitly observe:

* wall-clock time;
* environment variables;
* mutable global state;
* network state;
* filesystem state;
* uncontrolled randomness;
* unstable map iteration;
* unpinned external catalogs or reference data.

Anything capable of affecting a semantic result must be explicit, versioned, or supplied through the pinned execution world.

I/O belongs at boundaries.

Inject nondeterminism rather than hiding it.

## 3.4 Execution is deterministic

Given identical semantic inputs, execution must produce identical semantic outputs.

This includes, as applicable:

* final state;
* structural journal;
* invariant results;
* content identities;
* synthetic entity identities;
* canonical ordering.

Never depend on Go map iteration order.

Sort where ordering contributes to canonical representation.

Never generate semantic identities using random UUIDs or wall-clock values.

## 3.5 State changes are structural and attributable

Do not reduce the transformation model to field assignment.

The semantic change model must be able to represent structural operations such as:

* insert;
* delete;
* update;
* merge;
* split;
* relate;
* unrelate.

A journal records **what happened semantically**, not how a particular backend happened to execute it.

Do not leak SQL statement numbers, warehouse row ranges, Batch job mechanics, or similar backend details into semantic provenance.

## 3.6 Fail closed

A failed protected invariant cannot produce a publishable artifact.

Do not:

* convert protected invariant failures into warnings;
* catch and suppress them;
* weaken them to get a fixture through;
* add bypass flags to the normal publication path;
* treat successful execution as equivalent to successful validation.

Execution success, gate success, and publication are distinct states.

## 3.7 Replay must be real

Historical replay is valid only if everything capable of affecting execution is pinned.

Do not perform unjournaled semantic reads against mutable external state.

Reference data, schemas, policies, catalogs, rule sets, and other execution inputs must participate in the pinned world or equivalent immutable identity model.

If the same semantic run cannot be reproduced, treat that as an integrity defect rather than normal drift.

## 3.8 Stochflow is infrastructure, not Maiden Lane's domain

Maiden Lane may reuse selected stable stochflow infrastructure only through Maiden Lane-owned ports and adapters.

Semantic packages must not import stochflow.

Stochflow's:

* agent vocabulary;
* economic statechart;
* agent contracts;
* resource accounting;
* agent journal schema

must not leak into Maiden Lane's domain.

Only the designated stochflow adapter package may import the stochflow module.

Maiden Lane owns canonical semantic meaning. Shared infrastructure may implement narrow capabilities such as hashing or comparison.

---

# 4. Rule Language Discipline

The certified rule language is intentionally closed and statically analyzable.

That restriction is a feature.

Rules should make dependencies explicit and mechanically derivable:

* reads;
* writes;
* predicates;
* operators;
* preconditions;
* postconditions;
* evidence requirements;
* ordering dependencies.

The compiler, not the rule author, is authoritative about derived read/write behavior.

If declared access and derived access disagree, compilation should fail.

Do not introduce:

* arbitrary code execution;
* dynamic field names;
* hidden reads;
* hidden writes;
* arbitrary SQL

into the certified path merely to make a customer exception easier to express.

If an unsafe migration escape hatch is ever introduced, its degraded guarantees must be explicit. It must not quietly masquerade as a certified transformation.

---

# 5. Compiler Rules

The compiler should reject invalid semantic programs as early as possible.

Prefer compile-time rejection over runtime surprise when the property is statically knowable.

Examples include:

* unknown entities or fields;
* invalid operators;
* incompatible types;
* undeclared access;
* missing dependencies;
* dependency cycles;
* unresolved write/write conflicts;
* invalid rule composition;
* unsupported provenance requirements.

Compilation must be deterministic.

Authoring order must not accidentally change canonical plan identity when semantics are otherwise identical.

Do not "best effort" an invalid plan.

---

# 6. Executor Rules

The Go executor is the reference semantic implementation unless the HLD changes that decision.

Keep it:

* simple;
* explicit;
* deterministic;
* obviously correct;
* independently testable.

Do not prematurely optimize the reference executor into something difficult to reason about.

Preserve a straightforward reference path even when adding optimized execution.

Future execution backends must be compared against the reference semantics rather than merely tested on a few expected outputs.

Optimization is permitted only after semantic equivalence can be demonstrated.

---

# 7. Invariants

Invariants are part of the domain model, not scattered defensive checks.

Keep distinctions clear among:

* operation invariants;
* rule invariants;
* execution invariants;
* soft quality policies.

Whenever possible, encode known failure classes as invariants instead of adding another downstream cleanup step.

For a bug caused by an invalid state, ask:

> What invariant would have made this state impossible to commit or publish?

Prefer fixing that boundary over adding a special-case correction after the invalid state already exists.

Do not weaken an invariant to accommodate existing bad data without an explicit policy decision.

---

# 8. Test-Driven Development

Use **RED → GREEN → REFACTOR** for behavioral changes.

For a bug:

1. reproduce it with the smallest meaningful failing test or incident fixture;
2. identify the violated invariant;
3. make the test fail for the correct reason;
4. implement the smallest correct fix;
5. refactor only after behavior is protected.

For new behavior:

1. specify externally observable semantics first;
2. write tests for the semantic contract;
3. implement;
4. refactor while preserving those tests.

Do not write a large implementation and then backfill tests that merely describe whatever happened to be built.

## Never weaken tests to clear a failure

Do not:

* widen tolerances without justification;
* reduce sample counts;
* cherry-pick passing seeds;
* delete edge cases;
* clip inconvenient values;
* weaken assertions;
* convert failures into skips;
* rewrite expected output merely to match a new implementation.

If a test is wrong, explain why the specification is wrong and correct the specification deliberately.

---

# 9. Required Test Character

Prefer tests that establish durable properties over examples that exercise syntax.

Important classes include:

* deterministic replay tests;
* patch application/undo property tests;
* compiler determinism tests;
* invariant tests;
* golden incident fixtures;
* structural merge/split tests;
* canonical encoding tests;
* content-identity tests;
* idempotency tests;
* crash/resume tests where persistence is involved;
* publication race tests;
* backend differential tests.

Real mapper incidents make valuable permanent fixtures.

When fixing an incident class, preserve the fixture so the architecture cannot casually regress into representing the same failure again.

---

# 10. Go Engineering Style

Prefer boring, idiomatic Go.

Clarity beats cleverness.

## Prefer

* standard library first;
* small packages with clear ownership;
* domain-oriented names;
* explicit data flow;
* explicit dependencies;
* explicit error handling;
* composition over frameworks;
* consumer-owned narrow interfaces;
* concrete types inside packages;
* table-driven tests where appropriate;
* `%w` error wrapping;
* `context.Context` at I/O and cancellation boundaries;
* deterministic data structures and canonical ordering.

## Avoid

* speculative abstraction;
* giant interfaces;
* dependency-injection frameworks;
* service locators;
* global mutable state;
* magic registration;
* reflection when ordinary types suffice;
* generic helpers that erase domain meaning;
* clever concurrency;
* premature distributed architecture.

Keep functions small enough that their invariants fit in working memory.

Comment **why**, especially:

* invariants;
* mathematical intent;
* canonicalization rules;
* ordering decisions;
* non-obvious tradeoffs.

Assume many Maiden Lane contributors and reviewers are more familiar with
Python than Go. Use slightly more implementation documentation than a typical
Go repository when it materially reduces that gap:

* give every nontrivial package a package-level explanation of its ownership
  and dependency boundary;
* document exported declarations normally;
* explain non-obvious determinism, ordering, canonicalization, lifecycle,
  concurrency, retry, and fail-closed decisions near the code that implements
  them;
* explain Go idioms whose safety consequence may not be apparent to a
  Python-oriented reader.

Comments explain implementation. They do not override the Inviolates, HLD,
contracts, or authoritative tests, and they should not narrate obvious syntax.

Do not narrate obvious Go syntax.

---

# 11. Interfaces and Boundaries

Interfaces belong primarily at architectural boundaries and should normally be owned by the consumer.

Do not create an interface merely because a concrete type exists.

Use interfaces to isolate:

* persistence;
* artifact storage;
* hashing implementation;
* comparison implementation;
* dispatch;
* external reference acquisition;
* other genuine side effects.

Domain packages should not depend on AWS SDKs, database drivers, HTTP frameworks, or stochflow.

HTTP handlers translate wire contracts into application commands. They contain no transformation semantics.

Persistence adapters persist semantic/application state. They do not decide business meaning.

Composition belongs near executable entry points.

---

# 12. Refactoring

Refactor when the current structure makes the next correct change harder or obscures an invariant.

Do not refactor merely because an LLM can imagine a more abstract architecture.

Before significant refactoring:

* establish behavioral tests;
* identify the boundary being improved;
* preserve semantics;
* keep the diff focused.

Prefer deleting accidental complexity over adding abstraction around it.

Do not continue appending logic to an oversized file simply because adding another branch is locally cheaper.

If a component no longer has one understandable responsibility, split it along semantic boundaries.

Do not perform unrelated cleanup while fixing a focused defect.

---

# 13. Performance

Correctness and determinism precede optimization.

Measure before optimizing.

Do not introduce:

* transformer fusion;
* parallel execution;
* caching;
* SQL pushdown;
* compressed provenance;
* specialized storage structures

because they "should be faster."

Establish a baseline and identify an actual bottleneck.

Optimized paths must be compared against a trusted reference implementation.

For transformations that change evaluation order or numerical behavior, use property, metamorphic, or differential tests rather than relying only on final example outputs.

Performance work may change execution mechanics. It may not silently change semantic meaning or required provenance.

---

# 14. Concurrency

Concurrency is an optimization, not architecture.

Use it only when independence has been established.

Concurrency must be:

* bounded;
* cancellable;
* deterministic at the semantic boundary;
* free of goroutine leaks;
* race-tested.

Independent transforms may execute concurrently only if the resulting state, journal, invariant results, and semantic identities remain equivalent to reference sequential execution.

---

# 15. Persistence and Durable State

Keep control-plane metadata separate from immutable data-plane artifacts as described by the HLD.

Durable semantic history should be append-only or immutable where the design requires it.

Derived indexes, projections, caches, and views should be rebuildable from authoritative state.

Do not make a derived database representation the only source of semantic truth.

Publication should expose an already validated immutable artifact. It must not rerun transformation logic.

Rollback should normally restore a prior publication pointer rather than attempt clever production-time inverse execution.

---

# 16. Generated Code

Generated artifacts must:

* identify themselves as generated;
* have one authoritative source;
* be reproducibly regenerable;
* not be edited by hand.

When changing a generator or authoritative specification:

1. modify the source;
2. regenerate;
3. inspect the generated diff;
4. run relevant tests;
5. verify a second regeneration is clean.

CI should detect generated-code drift where practical.

---

# 17. Errors

Errors should preserve useful causal information without leaking customer data.

Differentiate at least conceptually among:

* invalid input/specification;
* invariant violation;
* semantic integrity failure;
* concurrency conflict;
* transient infrastructure failure;
* cancellation;
* backend divergence.

Do not use retries for deterministic semantic failure.

Retry only failures for which repetition can plausibly change the outcome.

Wrap lower-level errors with context while preserving the underlying cause.

---

# 18. Security and Customer Data

Customer data is hostile and sensitive input.

Validate inputs at boundaries.

Do not log:

* raw entity payloads;
* customer records;
* rules containing sensitive values;
* journal bodies;
* secrets;
* tokens;
* credentials.

Prefer identifiers, digests, bounded metadata, and stable error codes.

Do not use customer IDs, entity IDs, `SemanticRunID`, `ExecutionID`, `AttemptID`, or other unbounded identifiers as metric labels.

Secrets belong in designated secret-management boundaries, never source code, fixtures, command arguments, or generated artifacts.

---

# 19. Observability

Observability should explain system behavior without becoming a second copy of customer data.

Use structured logs and OpenTelemetry where the project provides them.

Useful operational signals include:

* `SemanticRunID`, `ExecutionID`, `PlanID`, and `AttemptID` where safe;
* phase;
* duration;
* counts;
* invariant code;
* gate result;
* retry classification;
* artifact digest.

Keep ordinary logs concise.

Use debug-level detail for diagnosis rather than flooding normal operation.

Semantic provenance belongs in the semantic journal, not ordinary application logs.

---

# 20. Dependency Discipline

Prefer established, maintained libraries when they materially reduce risk.

Do not add a dependency to avoid writing a small amount of straightforward Go.

Every dependency introduces:

* upgrade cost;
* security surface;
* semantic assumptions;
* transitive complexity.

Be especially conservative inside semantic packages.

Do not import a large framework to solve a narrow problem already handled cleanly by the standard library or an existing project dependency.

Keep stochflow behind its designated Maiden Lane adapter.

---

# 21. Scope Discipline

Implement the smallest coherent vertical slice that proves the requested behavior.

Do not turn a focused task into:

* a general framework;
* a production infrastructure rollout;
* an unrelated refactor;
* a new abstraction layer;
* speculative future-backend support.

YAGNI applies.

However, do not knowingly create a local shortcut that violates an invariant or the HLD merely because the current demonstration is small.

Temporary implementation is acceptable.

Temporary semantics are not.

---

# 22. Working With Multiple Agents

When multi-agent support is available and authorized, use at least one subagent
for every task unless a concrete reason makes delegation unsafe, impossible, or
materially less effective. State that reason before proceeding inline.

Use more than one subagent only when the work can genuinely be divided without
conflicting ownership.

Good uses include:

* independent codebase investigation;
* separate package implementation;
* test/fixture construction;
* adversarial design review;
* security review;
* independent verification.

Avoid assigning multiple writers to the same files or tightly coupled change unless coordination is explicit.

One agent should own the final integration.

Before merging independent work:

* inspect every diff;
* reconcile assumptions;
* run the combined test suite;
* verify architectural boundaries again.

Use an independent review pass for substantial changes. The reviewing agent should look for invariant violations and hidden semantic changes, not merely style issues.

---

# 23. AI/Agent Working Rules

Do not "vibe" through a failing system by repeatedly adding branches until tests turn green.

Before editing:

* understand the semantic contract;
* identify the relevant Inviolates and invariants;
* locate the authoritative design;
* inspect existing tests;
* understand why the current behavior exists.

Use available planning, debugging, TDD, review, and verification workflows when appropriate.

When uncertain, investigate rather than invent.

Do not fabricate:

* APIs;
* schemas;
* configuration fields;
* package behavior;
* deployment assumptions;
* domain rules.

If information is absent, keep the implementation narrow and make the uncertainty explicit.

Do not claim work is correct or complete without running the relevant verification.

---

# 24. Git Hygiene

Preserve unrelated user changes.

Do not:

* reset unrelated work;
* rewrite history;
* force-push;
* delete unfamiliar files;
* amend another person's commit;
* silently revert code outside the task.

Keep diffs focused.

Inspect the final diff before completion.

Git is the historical record. Documentation describing current implementation should describe the current implementation, not retain stale states for posterity.

Do not commit unless the task or workflow explicitly calls for a commit.

---

# 25. Verification Before Completion

Run the narrowest relevant checks while developing, then broaden before claiming completion.

Use repository-provided commands when they exist.

For Go changes, the expected progression is generally:

1. targeted package tests;
2. `gofmt`;
3. targeted tests again;
4. `go vet` or repository equivalent;
5. static analysis such as `staticcheck` when configured;
6. `go test ./...`;
7. `go test -race ./...` for concurrency or shared-state changes;
8. `govulncheck` when dependency/security scope warrants it;
9. build relevant binaries;
10. integration or generated-code checks where applicable;
11. inspect `git diff`.

Do not claim a command passed unless it was actually run.

If a check cannot be run, state that fact and why.

A clean compile is not proof of semantic correctness.

A passing unit test is not proof of replay correctness.

A successful execution is not proof of publishability.

---

# 26. The Mandatory Adversarial PR Review Gate

Before any pull request is opened, and before any branch is marked ready for human
review, the branch must pass an adversarial review gate.

## Role and separation of concerns

An independent subagent (`adversarial-reviewer`, defined in
`.claude/agents/adversarial-reviewer.md`) is spawned to red-team the diff. Its tools are
`Read`, `Grep` and `Glob`, and it is granted no write tool of any kind, so it cannot edit a
file, run a command, commit, push, or open a pull request. The intent is a capability
boundary rather than a promise, because the whole point of the gate is that the critic
cannot make its own objections disappear and a policy prohibition would rest on the
critic's good behaviour.

The prohibition is nonetheless *also* stated in the reviewer's own instructions, because
the tool list is enforced by the harness and neither this document nor the agent definition
can verify that enforcement. If a runtime ever hands the reviewer a write-capable tool
regardless, the boundary degrades to the policy promise this design exists to avoid; the
reviewer is therefore required to report any mismatch between its actual tools and the three
above.

The cost is deliberate and is paid by the primary agent. The reviewer cannot run the tests,
so it cannot independently reproduce a mutation or confirm that the pipeline is green.
Those remain claims, and the reviewer's report must separate what it checked from what it
could not.

Its mandate is to find defects, and the definitive list of what it looks for lives in the
agent definition, not here: Inviolates 0-19, plus the failure classes this repository has
actually shipped and had caught in human review. That list is deliberately not restated in
this section. A second copy would drift, and a partial copy read as complete would
mis-calibrate anyone reading only this file — which is the precise defect an earlier draft
of this section shipped, having quietly dropped two of the classes.

The separation is the point. The agent that wrote the diff is the worst judge of whether
its tests enforce anything, because it wrote the tests to pass. The reviewer's incentive
is to find the thing that got through, and it is not permitted to relieve that tension by
fixing what it finds.

## Supplying the evidence

The reviewer cannot run `git`, so the primary agent writes four artifacts **outside the
repository** — the session scratchpad — and passes their paths:

1. the **patch**, `git diff <base>...<head>`;
2. the **inventory**, `git diff --name-status <base>...<head>`;
3. the **working-tree status**, `git status --porcelain`, so the branch's own changes are
   distinguishable from unrelated in-flight work;
4. the **claims** — the assertions that would otherwise go in a PR body, written down
   early: what the change does, which mutations were run and what they killed, which
   commands passed.

The claims file exists because the gate runs before a pull request does. Writing the
assertions first is the point rather than an inconvenience: they are checked while they are
still a claim about work, not after they have become a description of merged work.

The reviewer cross-checks the patch against the inventory and spot-checks the files that
matter against disk, because the party under review produced all four. A path in the
inventory that the patch does not account for is a `REJECT`. What the reviewer genuinely
cannot do is discover a file omitted from the patch *and* the inventory — the base tree is
not on disk and it cannot diff against it. That is a real limit of a critic with no `git`,
it is the price of the capability boundary above, and the reviewer is required to record it
rather than imply coverage it does not have.

## Verdict

The reviewer emits a structured report ending in a mandatory verdict.

**`REJECT`** halts the pipeline. Any demonstrated Inviolate breach, any confirmed
instance of a known failure class, a new guard with no mutation evidence or with mutation
evidence that did not actually run, a comment describing behaviour the code lacks, a data
race, a determinism hazard reaching a semantic identity, or a PR-body claim found false.
The primary agent must diagnose the root cause, fix it, and re-run the gate from the
beginning — not argue the finding away, and not patch the symptom the reviewer happened
to name.

**`APPROVE`** is granted only when the reviewer looked for each failure class and found
none, the tests demonstrably enforce what they claim, and every deliberate omission is
written down. It is not the default and it is not granted merely for an empty findings
list.

An uncertain reviewer rejects. A false `REJECT` costs one iteration; a false `APPROVE`
costs an Inviolate.

## When the gate cannot run

Working With Multiple Agents scopes subagent use to when multi-agent support is available
and authorized, and requires the reason to be stated when it is not. This gate inherits
that, and needs it: without it there is a state with no legal exit, because a branch with no
verdict has no `REJECT` to dispute and therefore no escalation move.

If the gate cannot be run, the primary agent states that plainly and prominently — in the
handoff and in the eventual PR body — and does not claim `APPROVE`, does not claim the
Definition of Done is met, and does not imply the diff was reviewed. An unrun gate is a
disclosed gap for a human to weigh, which is a different thing from a passed gate and must
never be presented as one.

## No bypass

Under no circumstances may an agent mark a branch ready for review, merge it, or declare a
task complete while a verdict remains `REJECT`. Opening a pull request under a standing
`REJECT` is permitted in exactly one case, the draft adjudication PR described below, and
such a PR stays draft until a human settles it.

The gate runs after the verification in *Verification Before Completion* passes, not
instead of it. (Sections here are referred to by name rather than number on purpose: this
gate treats a comment that misdescribes the code as a merge blocker, and a hardcoded
section number is a comment that goes stale the first time anything is inserted above it.) A reviewer must never be
asked to review a branch whose tests do not run — that wastes the one process step whose
budget is reserved for what the tooling cannot see.

## Disputing a finding

Disagreeing with a finding is legitimate; acting on the disagreement unilaterally is not.
Silently discarding a `REJECT` is the one failure this gate cannot detect, so the escalation
path is explicit.

A reviewer that is uncertain must reject, which means false rejections are expected rather
than exceptional, and the primary agent needs a way to contest one without either obeying
it or ignoring it. It has two moves and no others:

1. **Produce the evidence.** Most disputes are really requests for evidence — a named test,
   a re-run mutation, a line the reviewer could not reach without running anything. Supply
   it and re-run the gate. This is the normal resolution and it needs no human.
2. **Escalate to a draft pull request.** If the disagreement survives evidence, open a
   **draft** PR whose body carries the finding and the rebuttal side by side, labelled as
   under adjudication. This is the one case where a `REJECT` may result in a PR existing,
   and it is permitted because the alternative is a dispute resolved in a transcript
   nobody can find later.

A draft PR opened under a standing `REJECT` may not be marked ready and may not be merged.
Only a human can settle the dispute: either the finding is withdrawn and the gate re-run to
`APPROVE`, or the finding stands and the diff is fixed. The verdict never clears itself, and
the primary agent never marks its own dispute resolved.

---

# 27. Definition of Done

A change is not done merely because the requested code exists.

Before completion, confirm:

* the relevant Inviolates and invariants are preserved;
* the HLD is still satisfied;
* semantic behavior is tested;
* deterministic behavior remains deterministic;
* no business meaning leaked into an adapter or backend;
* errors fail at the correct boundary;
* provenance remains adequate;
* relevant verification passed;
* the adversarial review gate returned `APPROVE`, or its inability to run was disclosed;
* generated artifacts are current;
* documentation describing current implementation is current;
* the final diff contains no accidental work.

For significant behavioral changes, ask one final question internally:

> Would a future engineer be able to understand why this state transition is valid, reproduce it from pinned inputs, and determine which rule caused it?

If not, the implementation is incomplete.
