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

And the open question: dependency edges come from `intersects(writer.writes, reader.reads)`
(`compile.go:787`) and mutual edges are a `DEPENDENCY_CYCLE` (`compile.go:394`). Under
set-scoped rules over one entity kind, read and write sets overlap more readily, so
**set-scoping may produce more cycles, not fewer.** Slice 3 delivers the fixture that can
answer this; the analysis is not declared adequate before then.

## Committed slices

**Slice 0 — commit the measurements.** A benchmark for execution cost against state size, and
a compile-only table case for a multi-rule ruleset recording the diagnostics it produces. The
figures motivating this programme came from throwaway probes and are not reproducible:
`grep 'func Benchmark'` returns nothing. This runs first so later slices measure against
something committed. It cannot answer decision 4's question, which needs set-scoped rules and
therefore slice 3.

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

**Slice 2 — evaluation.** Evaluating an `Expr` against a state and pinned world.
Deterministic, total, refusing rather than defaulting on absent fields.

**Slice 3 — selection and grouping.** The selector, canonical ordering of matches and group
keys, declared cardinality, the determinism test, and the set-scoped multi-rule fixture
decision 4 needs.

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
`storagecontract/policies.go:324`, consuming one of those constants in a non-test file.

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
