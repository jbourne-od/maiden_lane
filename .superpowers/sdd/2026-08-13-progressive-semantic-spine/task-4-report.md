# Task 4 Report: Closed Reference Executor, Invariants, and Accepted Journal

## Status

Implemented the Task 4 compiled-plan reference executor and accepted-only
semantic journal. The change stops at transition execution and accepted
history; it does not add checkpoint sealing, readiness, fixtures, application
orchestration, or semantic telemetry.

## Implementation

- Added verified `RunBinding` construction. Binding re-encodes and verifies the
  plan, initial state (including its schema), and pinned world before deriving
  `InputID`, `SemanticRunID`, provenance-policy identity, or `ExecutionID`.
- Added the owner-ratified structured executor identity: canonical ASCII
  backend token plus immutable version digest. Executor identity participates
  only in `ExecutionID`; the sole v1 provenance policy is `changes.v1`.
- Implemented compiled-rule-only, next-rule transition selection with
  non-panicking rejection of rules outside the bound plan, exact accepted
  plan-prefix validation, and replayed state/journal frontier checks.
- Implemented `FormRelatedEntity` for `form_team.v1`: explicit source
  resolution, nonempty/equal grouping key, HLD synthetic ID from sorted
  progenitors and typed output key, one schema-bound atomic
  `Insert+Relate+Relate` patch, exact member cardinality, and no HOS reads.
- Implemented `AggregateRelatedFields` for `aggregate_team_hos.v1`: resolves
  T1's typed output slot through accepted patch history, traverses the explicit
  member relations, checks tuple completeness, nonnegative durations,
  `driving<=elapsed`, and equal anchors before patch construction, then applies
  one absent-before-image `Update`, validates emitted values, and appends only
  after every protected check passes.
- Added compiler-declaration-keyed invariant results and the exact tagged
  protected/integrity failure variants and closed codes. Evidence contains only
  sorted typed entity/fact/invariant/artifact references, never raw values,
  source keys, or human prose.
- Added immutable `Journal`, `JournalEntry`, entry/prefix identities, complete
  prefix invariant-result-set identity, and validation that prevents zero,
  failing, incomplete, corrupted, disconnected, or reordered entries from
  becoming a bound prefix.
- Added the owner-ratified v1 `ArtifactKind` rank/token union and private
  `ArtifactRef`, `FactRef`, and `InvariantEvidenceRef` layouts.
- Documented all new v1 byte layouts in `canonical.go` and updated the living
  implementation guide to describe only current repository capabilities.

## Files

- `internal/semantic/binding.go` — verified run construction and layered
  identity recomputation.
- `internal/semantic/failure.go` — closed integrity/protected failure artifacts
  and immutable safe references.
- `internal/semantic/execute.go` — transition outcome/dispatcher, accepted
  outcome construction, and common invariant/journal construction.
- `internal/semantic/journal_verify.go` — structured journal-entry identity,
  link, and replay verification with exact verified-prefix retention.
- `internal/semantic/execute_form.go` — closed related-entity operator.
- `internal/semantic/execute_aggregate.go` — closed related-field aggregate
  operator and typed output-slot resolution.
- `internal/semantic/journal.go` — immutable accepted entries/journal and safe
  invariant/fact evidence values.
- `internal/semantic/execute_test.go` — T1/T2 behavioral, failure-boundary,
  ordering, aggregate, tie-evidence, and binding tests.
- `internal/semantic/journal_test.go` — accepted-history immutability,
  cross-executor differential, frontier, entry order, and literal vectors.
- `internal/semantic/canonical.go` — narrow Task 4 v1 encodings.
- `internal/semantic/value.go` — structured executor identity.
- `docs/implementation/implementation-guide.md` — current capability/map/gaps.

## Strict RED -> GREEN evidence

1. T1/binding RED:
   `go test ./internal/semantic -run 'Test(ExecuteFormTeam|RunBinding)' -count=1`
   failed to compile because `ExecuteTransition`, `NewJournal`, `RunBinding`,
   `TransitionOutcome`, and failure types did not exist. After the minimal T1
   and binding implementation, the same command passed.
2. T2 RED:
   `go test ./internal/semantic -run 'TestExecuteAggregate' -count=1`
   failed every aggregate test with `aggregate related fields is not
   implemented`. After the ordered T2 implementation, it passed.
3. Runtime-order RED:
   `go test ./internal/semantic -run
   TestExecuteAggregateTeamHOSChecksDurationBeforeAnchorEquality -count=1`
   failed with `HOS_ANCHOR_MISMATCH, want HOS_DURATION_INVALID`. Separating
   canonical predicate order from required evaluation order made it pass.
4. Accepted-history RED:
   `go test ./internal/semantic -run
   TestJournalAppendAcceptedRejectsUnverifiedEntry -count=1` failed because a
   zero entry appended. Entry identity/link verification made it pass.
5. Canonical-vector RED:
   `go test ./internal/semantic -run
   TestJournalAndRunBindingCanonicalVectors -count=1 -v` logged ten complete
   byte/digest vectors and deliberately failed. Replacing the logger with
   independent literal hex and SHA-256 expectations made it pass.
6. Independent-review RED batch named these production breaks before testing:
   fabricated passes for unevaluated obligations; Go-error routing for corrupt
   established-run artifacts; out-of-order/repeated rules; incomplete accepted
   invariant sets; ignored copied-field sources and `CompleteTuple` fields;
   trusted cached binding IDs; aliased optional digest pointers; and
   current-rule-only result sets. The focused batch failed on those boundaries;
   evaluated-only runtime results, typed integrity outcomes, exact prefix replay,
   compiled-field reads, identity recomputation, scalar copies, and cumulative
   prefix results made it pass.
7. Verified-frontier preservation RED:
   `TestExecuteTransitionReturnsTypedIntegrityFailureForCorruptEstablishedArtifacts`
   was extended to C1 and failed until journal replay preceded supplied-state
   integrity checking. It now preserves the verified C1 state and one-entry
   journal while rejecting the corrupt state with nil Go error.
8. Refactor GREEN: after splitting the 1,204-line implementation by semantic
   responsibility, the focused package passed with production files of
   195/290/332/111/310 lines (binding/failure/core/form/aggregate).

Each added test names the production break it catches. Expected values use
literal independent vectors rather than the implementation's encoder to derive
the asserted byte or digest value.

## Independent vectors

Literal full canonical hex plus digest/identity is frozen for:

1. provenance-policy tuple;
2. `InputID` tuple;
3. `SemanticRunID` tuple;
4. `ExecutionID` tuple;
5. synthetic-team identity tuple;
6. accepted journal entry;
7. accepted journal prefix;
8. complete invariant-result set;
9. `ProtectedInvariantFailureReport`; and
10. `ArtifactIntegrityFailureReport`.

The cross-executor test uses backend `go` with two distinct literal executor
version digests. It proves different `ExecutionID` values but identical state,
patch, entry, prefix, and invariant-result artifacts without asserting a second
production backend.

## Verification evidence

- Baseline before changes: `go test ./...` passed all four packages.
- Targeted after implementation:
  `go test ./internal/semantic -run 'Test(Execute|Journal|RunBinding)'
  -count=1` passed.
- `git diff --check` passed.
- `go vet ./...` passed.
- `go tool staticcheck ./...` passed.
- `go test ./... -count=1` passed all four packages.
- `go test -race ./... -count=1` passed all four packages.
- `go tool govulncheck ./...` reported `No vulnerabilities found.`
- `go build -trimpath -o bin/maiden-lane ./cmd/maiden-lane` passed.

- Final post-review focused package:
  `go test ./internal/semantic -count=1` passed.
- Final authoritative `make verify` passed: module tidy diff, vet, staticcheck,
  all tests, all race tests, govulncheck (`No vulnerabilities found`), and the
  trimmed application build.

## Self-review

- T2 evaluates tuple completeness, both nonnegative checks, and
  `driving<=elapsed` before anchor equality and before `NewPatch`.
- The mismatch path has no patch digest, returns the exact C1 state and journal
  prefix, and appends no T2 entry.
- `executeFormRelatedEntity` contains no HOS field/code/predicate read.
- T2 identifies the team from T1's typed accepted output rather than an ambient
  entity scan or undeclared assignment-key recomputation.
- Journal entry/prefix bytes contain no executor, execution, attempt, clock,
  backend, hostname, trace, job, retry, or storage metadata.
- Patch receipts remain application proof only; Task 4 adds no receipt identity.
- Protected semantic rejection is populated outcome plus nil error. Once a run
  is established, deterministic state/journal artifact corruption is a typed
  `ARTIFACT_INTEGRITY_FAILED` outcome with nil error and only the replay-verified
  frontier retained. Pre-binding malformed input and impossible internal
  contradictions remain Go errors.
- Runtime evaluation records only obligations actually evaluated; canonical
  sorting cannot fabricate a passing result for a later false obligation.
- Every accepted entry carries the exact distinct passing declaration set for
  its compiled rule, and the outcome exposes the complete accepted prefix set.
- `FormRelatedEntity` reads each declared copied source field, and aggregate
  tuple checks evaluate every declared `CompleteTuple` field.
- Optional failure-report digest pointers are copied before canonical identity
  is frozen, and every cached binding identity is recomputed at use.
- No sealing, readiness, fixture, app, OTel, persistence, or publication code
  was added.

## Concerns

No known Task 4 contract concern remains. The independent review findings were
resolved and reverified. Statically unreachable protected codes (for example a
source-kind mismatch after compiler-enforced typed source references) remain in
the exact closed taxonomy but are not manufactured through artificial domain
states merely to exercise a runtime branch.

## Fix round 1/5

Independent review found four execution-boundary defects, all resolved with a
focused RED -> GREEN cycle:

- Equal empty source anchor atoms previously passed equality and committed.
  `TestExecuteAggregateTeamHOSRejectsEmptySourceAnchorBeforePatch` reproduced
  the acceptance; T2 now returns `HOS_TUPLE_INCOMPLETE` before patch creation.
  `TestValidateAggregateCandidateRejectsEmptyEmittedAnchor` also protects the
  emitted aggregate boundary.
- T1 previously used `GroupingField` as the synthetic output key regardless of
  the compiled `OutputKey.Field`. `TestExecuteFormTeamUsesDistinctCompiledOutputKey`
  reproduced the inert key; T1 now reads the output-key field from every
  explicit source, requires a common valid typed value, records its safe fact
  refs, and derives identity from it deterministically.
- Journal replay flattened every defect into a journal-prefix link failure.
  `TestExecuteTransitionClassifiesJournalIntegrityFailures` now freezes entry
  self-digest mismatch as `ARTIFACT_DIGEST_MISMATCH`, replayed result mismatch
  as `REPLAY_DIVERGENCE`, and plan/predecessor links as
  `ARTIFACT_LINK_INCONSISTENT`, all on the concrete `journal_entry` content
  digest with exact safe optional digest evidence and retained C1 frontier.
  Embedded patch identity corruption likewise implicates the concrete `patch`
  content digest rather than its enclosing entry or a fabricated prefix ref.
- Protected failure evidence refs followed runtime evaluation order.
  `TestProtectedFailureCanonicalizesInvariantEvidenceRefs` reproduced the
  identity drift; refs are now sorted/deduplicated by declaration key while
  runtime results retain evaluation order. The independently frozen protected
  failure vector changed legitimately to
  `sha256:677c45d2f45b89b5b046e8dc908426c821bf5bdee5a8d762599c50c43512c7be`.

Focused tests and `go test ./internal/semantic -count=1` pass. A fresh
`make verify` also passed module tidy diff, vet, staticcheck, all tests, all
race tests, govulncheck (`No vulnerabilities found`), and the trimmed binary
build. The fix commit is recorded at handoff.
