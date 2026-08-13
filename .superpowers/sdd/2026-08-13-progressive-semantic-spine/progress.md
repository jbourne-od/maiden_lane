# SDD ledger — plan: docs/superpowers/plans/2026-08-13-progressive-semantic-spine.md
Setup: isolated worktree `codex/progressive-semantic-spine` at planning commit `5c75f7f`; baseline `go test ./...` passed.
Task 1 clarification: no leaf encoding table existed; authorized the first narrow v1 table with exact domain tags and fixed fields under the ratified canonical rules. This is an implementation-format decision, not new team-HOS semantics.
Task 1: minor (deferred): add explicit acceptance tests that state construction does not enforce assignment equality or team member cardinality; final review must triage.
Task 1: fix round 1/5 (2 addressed, 0 open — deterministic invalid-field priority; alias-proof state/schema/world immutability tests; commits 4208d8b..fb304ee).
Task 1: complete (commits 5c75f7f..fb304ee, review clean).
Task 2: fix round 1 paused before edits — unresolved write/write conflict must fail closed, but the ratified diagnostic vocabulary has no truthful code; owner decision required before changing the canonical format contract.
Task 2: owner approved `WRITE_CONFLICT_UNRESOLVED`; design/plan amended in local commit `b9e280f`. Fix round 1 resumed.
Task 2: fix round 1/5 (4 addressed, 0 open — unresolved write conflicts; target entity access; ambiguous normalized declarations; frozen literals; commits b9e280f..4197396).
Task 2: complete (commits fb304ee..4197396, review clean; contract amendment b9e280f).
Task 3: owner approved schema-bound patches and receipt-bound inverse after review exposed unsafe destructive undo; design/plan amended in local commit `4c5cde5`. Fix round 1 started.
Task 3: fix round 1/5 (3 addressed, 0 open — receipt-authorized inverse; schema fail-closed errors; multi-field rollback; schema-bound vectors; commits 4c5cde5..0dca950).
Task 3: complete (commits 4197396..0dca950, review clean; contract amendment 4c5cde5).
Task 4 clarification: executor identity follows HLD structured `backend@<digest>` syntax (validated canonical backend token plus version digest); cross-executor exclusion uses two `go` version digests, not a speculative backend. Provenance policy remains sole `changes.v1`.
Task 4 clarification: ratified the full slice's closed ArtifactKind rank (`plan`, `compiled_profile`, `state`, `world`, `patch`, `journal_entry`, `journal_prefix`, `invariant_result_set`, `checkpoint_artifact`, `readiness_assessment`); ArtifactRef is kind plus content digest. FactRef is EntityRef plus FieldName; InvariantEvidenceRef is compiler-derived declaration key. Reference fields are private and semantic-only, never telemetry.
Task 4: structured HLD executor identity clarification recorded in design/plan commit `068ce00` (canonical backend token plus version digest; tests use two `go` version digests).
Task 4: implementation commit `51295fa`; independent review Ready=no. Fix round 1/5 opened for four confirmed findings: reject empty HOS anchors before patch materialization; execute the compiled typed output-key field; return truthful per-artifact integrity code/kind/digest evidence for journal verification failures; canonicalize invariant evidence refs independently of evaluation order.
Task 4: fix round 1/5 (4 addressed, 0 open — non-empty source/emitted anchors; compiled output-key identity; structured journal integrity classification with exact verified-prefix evidence; sorted/deduplicated invariant evidence refs).
