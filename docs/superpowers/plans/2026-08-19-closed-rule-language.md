# The closed rule language

## Why this programme exists

HLD goal 3: *"Structural operations cover inserts, deletes, updates, merges, splits, and
relation changes."* The engine has two operators — `OperatorFormRelatedEntity` and
`OperatorAggregateRelatedFields` — and neither is a structural operation in that sense. They
are one specific merge and one specific aggregation, shaped around a two-driver team.

`execute_form.go:26` reads `len(sources) != 2`: the operator cannot form anything but a pair.
A rule names its inputs by `CanonicalSourceKey` (`declaration.go:52`), so one rule handles one
team and a thousand teams needs a thousand rules. Write-conflict analysis is field-path
granular (`compile.go:797-807`), so two rules writing different teams' `team.assignment_key`
are refused as an unresolved conflict.

The consequence is that no real ruleset compiles. Not slowly — at all.

## How long this document should be

Four rounds of adversarial review produced thirty findings on this plan. Every one was real.
Rounds two, three and four each contained defects introduced by the previous round's fixes.

The distribution is the useful part. The decisions below mostly held. Almost every finding
landed in prose about slices that do not exist yet, and nearly all had one shape: **a confident
statement about code nobody had read.** A representative sample, all mine:

- "Nothing derives read/write sets." `deriveTransformation` has done exactly that, with a
  ratified diagnostic, since before this plan existed.
- "`CompleteTuple` takes three paths." The kernel accepts any positive arity; three is the
  *fixture's* number — the mistake this programme exists to undo, made inside the plan to
  undo it.
- "`invariant_code` carries author-supplied strings." It is fed from kernel constants through
  a closed switch; no author string can reach it.
- "HLD goal 3 needs `Delete`, `Merge`, `Split`, `Relate`, `Unrelate`." `Relate` ships already.

That failure mode does not stop by writing more carefully. It stops when the compiler and the
tests are the ones checking, which is why this document is now as short as it can be: the
decisions that survived review, the slices buildable against code that exists, and a bare index
of everything else. **The index carries file references and no analysis** — every paragraph of
reasoning about unbuilt work has proven to be a place to assert something false.

## Decisions

### 1. Selection is not filtering, and grouping is the hard half

`When Expr` (sketch §8) filters. Forming a team from drivers sharing a key requires *grouping*,
which a filter cannot express, and `Merge`/`Split` are meaningless without it.

A rule declares a **selector**: an entity kind plus an optional grouping expression. Ungrouped,
it applies once per matching entity; grouped, once per distinct group key, with the transform
seeing the group's members. Cardinality becomes a declared property of the selector rather
than `len(sources) != 2` inside an executor.

### 2. Iteration order is an identity problem, not a style question

Today determinism partly comes from rules naming their inputs, so there is nothing to order. A
set-scoped rule must iterate, and nondeterminism there changes patch order, the journal, and
every downstream identity.

Selection results are canonically ordered by entity identity, and group keys by canonical value
bytes, before anything executes. Stated because it is the property most likely to be satisfied
by accident and broken later by a map iteration. It needs a test that fails on unordered
iteration, not a comment.

### 3. Derived read/write sets extend what exists

HLD §8 requires the compiler to derive reads and writes and to fail when authored declarations
disagree. `deriveTransformation` (`compile.go:610-731`) already does this for the operator
payload and emits `DECLARED_ACCESS_MISMATCH`, which is ratified and in the OpenAPI enum. The
missing half is derivation over an **expression tree**, and it extends that function rather
than standing a second derivation path beside it. Two paths would be the textbook shape of two
checks each individually correct with a hole between them.

### 4. Write-conflict analysis gets less wrong, and may get harder elsewhere

Today the analysis is *always* over-strict for a multi-instance ruleset. With set-scoped rules
the declaration becomes true, and two rules writing a path really are in conflict.

It does not become *correct*. Nothing is instance-aware and nothing lets the compiler prove two
selectors disjoint, so two set-scoped rules writing `team.status` under mutually exclusive
predicates are still refused — sound and fail-closed, not correct.

And the analysis has an exemption that matters in the *other* direction: `compile.go:800` skips
a pair entirely when a dependency path already orders it. So a shared write path is accepted,
not refused, whenever an edge exists. Since edges come from `intersects(writer.writes,
reader.reads)` and set-scoped rules over one entity kind overlap reads and writes far more
readily, set-scoping moves pairs from *refused* to *ordered and accepted*. That is the fail-open
direction, it is the opposite of the over-strictness this decision started from, and it needs
an argument nobody has made.

And the open question: dependency edges come from `intersects(writer.writes, reader.reads)`
(`compile.go:787`) and mutual edges are a `DEPENDENCY_CYCLE` (`compile.go:394`). Under
set-scoped rules over one entity kind, read and write sets overlap more readily, so
**set-scoping may produce more cycles, not fewer.** Slice 3 delivers the fixture that can
answer this; the analysis is not declared adequate before then.

## Committed slices

**Slice 0 — commit the measurements. Done, and shipped with this plan.** The figures
motivating this programme came from throwaway probes that were deleted, and a claim nobody can
reproduce is not evidence, so the plan does not land without them.

`BenchmarkExecutionByStateSize` (`internal/app/execution_bench_test.go`) holds the rule count
at two and grows the state. Observed: 15.1ms at 1,000 entities and 30.6ms at 2,000 — linear
from roughly 200 upward at about 15µs per entity, with allocation linear too at roughly 30KB
and 94 allocations per entity.

**That linearity is the finding, not the reassurance, and an earlier draft of this plan had it
exactly backwards.** The two rules resolve two named drivers, so every other entity is state
they never read. Cost confined to the work the rules do would be *flat* in entity count. It is
linear, so the threshold for "per-rule cost scales with total state" is any growth at all, and
the measurement demonstrates the hazard rather than ruling it out.

Reading the code says where the slope comes from and it is worse than the benchmark can show.
`ExecuteTransition` calls `replayVerifiedJournal`, which restarts from the binding's initial
state and re-applies every prior patch, so transition *k* costs Θ(*k*·E) and a run of *R*
transitions is **Θ(R²·E)**. `verifyState`, `Seal` and `evaluateProfileOverState` each add
further state-proportional work per transition. Under one rule pair per team, *R* and E both
grow with the fleet, making a run **Θ(N³)**.

That figure is derived from reading the code, not measured, and this is recorded rather than
resolved: measuring it needs a multi-rule plan that compiles, which today's operators cannot
express. It is blocked on the set-scoped selector and belongs to slice 3.

**A candidate remedy, recorded now and not adopted.** The full replay may be avoidable without
weakening anything. Re-verifying the entire prefix on every transition defends against a
journal altered between transitions, which cannot happen inside a single `Run` — the loop just
produced those entries. Where it genuinely bites is at a rehydration boundary, where the
journal comes from storage. If that is the only threat, verifying fully on entry and then
incrementally as the journal extends — transition *k* verifying only entry *k* against the
already-verified state from *k−1* — proves the same property at Θ(R·E) instead of Θ(R²·E).

Refutation condition, so this is testable rather than merely plausible: it fails if any
transition can observe a journal it did not itself extend, or if a verified state can be
mutated between transitions. Both are questions about `ExecuteTransition`'s callers, not about
the rule language, so this belongs to whoever owns the executor rather than to a slice here —
but it is recorded because the cost it addresses is the one this programme will expose.

The consequence for how this programme is framed: "the engine is not slow, it is not
expressive" is only true at the scale the current fixture can reach. Expressiveness is still
the blocker, because nothing runs at all today — but a set-scoped selector that makes a fleet
*expressible* will expose a cost curve the two-rule benchmark cannot see, and journal replay
per transition is the first thing to look at when it does.

`TestMultiInstanceRulesetBaseline` (`internal/semantic/multirule_baseline_test.go`) compiles a
ruleset with one form transformation per instance and records the refusal: C(N,2) unresolved
write conflicts, confirmed at 2, 3 and 10 instances — 45 at ten, because nothing in a field
path distinguishes one team from another. It is labelled a baseline rather than a
specification, since this programme is expected to change it.

Neither answers decision 4's question, which is about **set-scoped** rules and needs slice 3.

**Slice 1 — the expression AST.** `Expr` as a closed union, canonical bytes, compile-time type
derivation. No execution, no rules.

Golden vectors pin the **encoding scheme** — how a node of a given kind is encoded, and that
the kind byte participates — not an inventory of node kinds, which the sketch explicitly
predicts will change. Kind bytes are append-only.

Slice 1 also settles **whether the union admits a binder**, because deferring it is unsafe in
either direction. If bare field paths inside a group-scoped expression get an *implicit* member
scope in slices 1–3, a later explicit binder changes what those paths mean without changing
their bytes — worse than an invalidated vector, and undetectable by one. (An earlier draft
argued a binder necessarily rewrites existing nodes' encoding. That is false: a binding node
plus a variable-reference node carrying a de Bruijn index adds two ordinary kinds and changes
nothing existing. The reason to decide early is the implicit-scope trap, not encoding churn.)

**Slice 2 — selection and grouping.** The selector, canonical ordering of matches and group
keys, declared cardinality, the determinism test, and the set-scoped multi-rule fixture
decision 4 needs.

**This comes before evaluation, and an earlier ordering had it the other way round.** Slice 1
settled that there is no ambient scope, so a `Field` path names an entity *kind* and no entity
*instance*. Until a selector binds one, no `ExprField` node denotes a value and "evaluate an
expression against a state" has no meaning to implement — an evaluator written first would
have to invent the binding, which is precisely the silent reinterpretation slice 1's decision
exists to prevent.

Slice 2 also owns a constraint slice 1 cannot express: today `equal(driver.x, team.y)`
type-checks, because `ExprType` carries only the scalar kind. Whether an expression may name
more than one entity kind is a question about what a selector binds, so it is answered here.

**Slice 3 — evaluation.** Evaluating an `Expr` against a state, a pinned world and a binding
from slice 2. Deterministic, total, refusing rather than defaulting on absent fields.

## Index of everything else

Sites and constraints, with references and without analysis. Each needs an owner before the
programme ends; none is scheduled here, because the shape of the slice depends on decisions
slices 0–3 have not made.

**Structural operations still missing:** `Delete`, `Merge`, `Split`, `Unrelate`. `Relate` is
implemented (`patch.go:16,110-112`). `Merge`/`Split` carry the synthesized-entity identity
problem `OutputKeyExpression` solves for one shape.

**Aggregate vocabulary:** `AggregatePredicateKind`'s four tags and `ReductionKind`'s single
`ReduceInt64Max` (`declaration.go:36-45`). Replacements must express all four predicates —
`CompleteTuple` any positive arity, `LessOrEqualFields` exactly two, the other two exactly one
(`compile.go:975-982`) — and must state how a bare field path inside a group-scoped expression
resolves to a member.

**Invariants:** author-supplied codes, pre/postconditions, and a protected flag. The sketch's
`Protected bool` will not do: its zero value is *unprotected*, so an omitted field yields an
invariant whose failure permits a publishable artifact (Inviolate 4). It needs a third
*unspecified* state that refuses, the shape `internal/promotion` uses where `NotEvaluated` is
not one of the substantive answers (`gate.go:20-40`). Note `wire.go:114-116` defaults an absent
optional bool to `false`, and `wire.go:26`'s "never coerced or dropped" rule covers *unknown or
malformed* input, not absent input — so the boundary needs an explicit absence check rather
than a reliance on that rule.

**Kernel domain vocabulary:** eleven `Team*`/`HOS*` constants (`declaration.go:158-161`,
`:226-232`); `formInvariants`/`aggregateInvariants`, which synthesise invariants from the
operator kind; `validRequirementCode` (`compile.go:1158-1161`), admitting exactly four `Team*`
codes so the profile compiler refuses any non-HOS readiness code;
`storagecontract/policies.go:324`, consuming one of those constants in a non-test file; and
above all `internal/app/observation.go:442-460`, which consumes **seven** of the eleven
constants, with `:409-439` hard-coding the fixture's rule IDs, checkpoint keys and profile keys
as string literals behind three closed enums — that is the *source* of the span attributes the
telemetry entry below describes, so demolishing the symptom without this site leaves closed
enums silently returning zero. `storagecontract/executions.go:957-977` hard-codes the same
fixture strings in a non-test contract file.

**`evaluateProfileOverState`:** an empty explicit selection returns `Ready`
(`profile.go:266-276`), justified in-code by the fixture's T1 plan boundary, which author
rulesets will not have. `TestAssessEmptyScopeIsVacuouslyReady` ratifies it.

**Telemetry.** Of the six ratified metric keys (`semantic_metrics.go:33-40`), `profile_kind`
echoes an author-supplied `ProfileKey`; `invariant_code` does not — it is fed from kernel
constants through a closed switch (`observation.go:442`, `semantic_dimensions.go:367-400`), so
widening the ruleset cannot widen that dimension without an edit in `internal/observability`.
`transition_kind` and `checkpoint_kind` are **span attributes, not metric dimensions**
(`semantic.go:35-36`), and are closed two-value enums that would go permanently dead under an
author ruleset — they need removing or re-scoping, not ignoring.

The behaviour whereby an unadmitted value is omitted rather than labelled is a **ratified owner
decision of 2026-08-15**, preserved verbatim in `semantic_dimensions.go:20-33` so that "a later
cleanup cannot quietly make the two cases uniform". This programme does not reverse it, and no
part of this document should be read as calling it a defect. Any change there is an amendment
that cites it; operational difficulty is evidence for an amendment, not permission to route
around one.

**Wire surface:** `api/openapi.yaml:566,622` enumerates the predicate tags and the operators,
so deleting them breaks a published contract. `internal/httpapi/wire.go` is already the
boundary translator, so "no parser" is true of authored syntax and not of the boundary.
Translating an `Expr` union there without acquiring a decoder in `internal/semantic` is the
most Inviolate-13-sensitive work in the programme.

**Demolition:** `execute_form.go`, `execute_aggregate.go`, their declarations, `teamhos`, and
the demo UI's hard-coded codes, `RULE_MEANINGS`, `CHECKPOINT_MEANINGS`, `PROFILE_MEANINGS` and
driver field list (`app.js:21-75`).

**A field name may contain a dot, and such a field is addressable by nothing.**
`validSemanticName` (`value.go:178-180`) requires only a non-empty valid UTF-8 string, so a
schema may declare a field called `team.name`; `splitFieldPath` refuses every path with two
dots, so no expression or declaration can ever name it. Found by mutation while building slice
1, and left alone deliberately: tightening `validSemanticName` changes which schemas compile
and therefore which identities exist, which is an amendment rather than a slice. Whoever owns
it should decide between refusing the declaration and defining an escape.

**Value model:** instants, durations and exact decimals. `string | atom | int64`
(`value.go:87-92`) cannot express a fourteen-hour window. Sequenced after the AST, because
adding value kinds to a language that cannot select a set produces nothing runnable. Recorded
as absent rather than assumed present.

## Constraints carried from earlier programmes

- **One-way kernel encoding.** New identities get encoders and no decoders.
- **Fail closed structurally.** Zero values refuse; closed vocabularies are walked, not
  supplied as maps; illegal states unrepresentable through unexported fields.
- **A refusal is an answer.** A rule selecting nothing is a successful execution over an empty
  selection; a selector that cannot be type-checked is a compilation failure.
- **Golden vectors where the state space cannot vary a dimension.**
- **Deliberate omissions get written down.** The value model above all.
- **No projection carries authorization weight.** This programme does not touch the gate.
