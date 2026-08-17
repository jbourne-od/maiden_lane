# Plan — publication and promotion

**Status:** Active. Authority is the High-Level Design §14, §14.1, and §14.2, and
the progressive-completeness amendment. Where this plan is narrower than the HLD,
it says so and says why.

## Owner decisions ratified 2026-08-19

**Migrations use `dbmate`, applied by an explicit step, and the application
verifies rather than migrates.** This supersedes an earlier decision in favour of
`pressly/goose` applying migrations at startup; the owner raised dbmate, and the
argument that decided it is least privilege.

If the binary applies migrations, its runtime database role needs DDL rights,
which means the process serving requests can `ALTER` or `DROP` the tables holding
sealed artifacts. For a system whose proposition is that its record lineage is
immutable and attributable, that is the one place a structural guarantee would be
replaced by trust. It is the same reasoning already enforced elsewhere here:
storage *cannot* lie about identity rather than being relied upon not to.

Three supporting reasons, in descending weight: a long migration or a backfill
does not belong inside a starting process, where an advisory lock makes N−1 tasks
wait and a slow index build can outlast a health-check grace period; migration and
deploy stay separately reversible, so rolling a binary back does not leave the
schema ahead of the code with no deliberate step to undo; and plain
`-- migrate:up` SQL is reviewable by a contributor who does not read Go.

The usual objection — someone forgets the step — is answered by the pattern this
repository already uses: the binary refuses to start, exactly as an unreachable
configured database already blocks startup rather than falling back to memory.

dbmate is pinned in `go.mod` and invoked through `go tool`, so nobody needs a
workstation-global install and CI gets the same version a developer does. The
README is explicit that pinned tools exist for that reason, and CI already runs
`make store-check`, so making it the exception would have meant an extra setup
step there. The cost is real and accepted: dbmate bundles drivers for BigQuery,
ClickHouse, MySQL, and SQLite, so pinning it added roughly 850 lines to `go.sum`.
None of it reaches the application binary, which was verified rather than assumed.

The verification is precise rather than a hand-maintained version number. The
adapter embeds its migration files **to read, never to apply**, so the binary knows
exactly which versions it was built against, and refuses a database missing any of
them. That needs only `SELECT` on one table. It catches both failure directions: a
new binary against an un-migrated database, and a rolled-back binary against a
schema it does not recognise. A database *ahead* of the binary is accepted, because
that is the normal state during a rolling deploy.

**An unevaluated protected gate clause blocks publication.** Three of the HLD's
ten gate clauses cannot be evaluated yet. They will record `not_evaluated` and
refuse publication rather than passing by omission.

The consequence is deliberate and worth stating in one place: **nothing will
publish until comparison, regression, and certification exist.** Publication
becomes demonstrable only after those. This was chosen over a provisional
publication kind because a weaker path living beside its replacement is exactly
what the `202`-only decision retired, and over a non-blocking `not_evaluated`
because that would publish under a gate name the HLD defines more strictly.

## What §14.1 requires, and what is reachable

`Promotable(Target, C_k, P) = Sealed(C_k) ∧ Ready(C_k, P) ∧ GatesPass(Target, C_k, P)`

| Gate clause | Reachable now | Notes |
|---|---|---|
| Successful static plan validation | yes | A `PlanID` exists only for a plan that validated. |
| Sealed selected checkpoint with at least `changes` provenance | yes | `semantic.ChangesProvenance` exists and executions pin a policy. |
| All protected dynamic invariants applicable to the prefix passed | yes | The spine already refuses to seal otherwise. |
| A `ready` assessment under the target's pinned `ProfileID` | yes | Assessments are sealed artifacts carrying a verdict and a profile identity. |
| Pinned input, world, schema, ruleset, compiler, run, execution, checkpoint, profile, assessment identities | yes | All ten are already derived and stored. |
| Internally consistent checkpoint state, journal-prefix, assessment, and invariant-result digests | yes | Retained canonical bytes make this checkable rather than assumed. |
| No conflicting concurrent publication | yes | A compare-and-swap on the pointer. |
| **Baseline and candidate executions over the same replay corpus** | **no** | Needs a corpus concept, a baseline/candidate pairing, and §14.2 comparison identity including the explicit checkpoint-correspondence mapping. |
| **No protected metric regression** | **no** | Needs a protected-metric concept and a regression policy. Nothing in the codebase measures a semantic metric. |
| **A backend certified against the reference executor** | **no** | Needs an executor certification concept. Today `ExecutorIdentity` is pinned but never certified. |

Seven of ten are reachable. The three that are not are each a programme rather
than a task, which is why they are separated below rather than folded in.

## Where this code belongs

HLD §15 puts `promotion` among the domain packages under `internal/app`, above
`ports`. That split is load-bearing here:

- **Gate evaluation is pure.** `Promotable` is a deterministic function of sealed
  artifacts and an immutable policy. It decides meaning, so it belongs on the
  kernel side of the boundary and must reach no clock, no database, and no
  network.
- **Publication is stateful.** A versioned pointer with compare-and-swap is a
  storage concern and belongs behind `ports` with adapters, exactly like the
  execution queue.

Keeping those apart is what allows the gate to be tested exhaustively without a
database and the pointer to be tested for concurrency without the kernel.

## Slices

**Slice 1 — migrations.** Replace the implicit `CREATE TABLE IF NOT EXISTS`
applied on open with explicit versioned migrations, before any new table exists
to make it awkward. `store.go` already names this as the boundary. Includes: the
existing schema captured as the initial migration in a way that adopts an
already-populated database without recreating anything; a decision recorded about
whether the process applies migrations at startup or refuses to serve a database
at the wrong version; and `make store-check` proving both a fresh database and an
already-migrated one reach the same schema.

**Slice 2 — target policy.** An immutable, versioned policy keyed by tenant,
customer, and target that explicitly binds the `ProfileID` required for
publication. Immutability matters for the same reason plans are immutable: a
publication record pins the policy version that authorized it, so a policy that
could change would make an old authorization unexplainable.

**Slice 3 — the gate.** A pure evaluation producing a per-clause result rather
than a boolean, so an operator can see which clause refused. `GateVerdict` is
`not_evaluated | pass | fail`, and a protected clause at `not_evaluated` refuses.
This slice is where the three unreachable clauses appear explicitly as
`not_evaluated`, which is also what makes their absence visible rather than
forgotten.

**Slice 4 — publication.** Compare-and-swap on a versioned pointer keyed by
tenant, customer, and target, pinning the policy version, profile, assessment,
checkpoint, semantic run, and execution that authorized it. It never reruns a
transformation or a readiness evaluation. A conflicting publication fails rather
than overwriting a newer result. With Slice 3's decision in force, this slice
ships able to publish nothing, and its tests assert that refusal.

**Later programmes, in the order that unblocks publication:** a replay corpus and
§14.2 comparison identity; protected metrics and a regression policy; backend
certification against the reference executor.

## Constraints carried from earlier slices

- `internal/semantic` keeps its import allowlist; a gate that needs a clock or a
  store is a gate in the wrong package.
- Identities are re-derived and compared, never read and trusted.
- Telemetry is non-authoritative: no gate or publication outcome may depend on it.
- Closed vocabularies are distinct types with no catch-all member.
- `make verify` stays runnable with no Docker and no database.
- Run it for real before claiming it works. Two defects in the execution slice and
  two in the observability slices were found only by running the binary or reading
  rendered output, none by the test suite.
