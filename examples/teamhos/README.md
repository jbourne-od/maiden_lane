# Example payloads

Three request bodies for the public HTTP API, and the material `scripts/demo.sh`
walks through. They are the sanitized team-HOS golden incident from the ratified
walking-skeleton design, written as the API actually receives them.

| File | Endpoint | What it is |
| --- | --- | --- |
| `plan.json` | `POST /v1/plans` | A schema, two transformations, two checkpoints, two completeness profiles. |
| `execution.json` | `POST /v1/executions` | Two drivers reporting the same duty anchor. Runs to completion. |
| `execution-anchor-mismatch.json` | `POST /v1/executions` | The same input with one observation changed. Deterministically refuses. |

Both executions name the plan `plan.json` compiles to, so post the plan first.

```sh
make demo                                # browser client at http://127.0.0.1:8090
make demo-terminal                       # the same walkthrough as terminal output
scripts/demo.sh http://127.0.0.1:8080    # narrate against a server you started
```

Both demos start from these files. The browser client loads them at startup and lets
the driver observations be edited, so what you see first is exactly what is committed
here.

## Why the two executions differ by one field

`execution-anchor-mismatch.json` differs from `execution.json` in exactly one
place: driver B's `hos_anchor` is `T1` rather than `T0`. Everything else — the
plan, the lineage, the other driver, the hours, the executor identity — is
identical.

That is the point. The two drivers are no longer describing the same duty period,
so no defensible team HOS tuple exists for them, and the aggregation boundary
refuses with `HOS_ANCHOR_MISMATCH`. Because only one field moved, the refusal has
exactly one possible cause. A demo where several things changed at once would be
an anecdote rather than a demonstration.

The refusal leaves `team_formed.v1` sealed. The prefix that was justified is kept;
only the boundary that could not be justified seals nothing.

## These files cannot drift

`internal/httpapi/examples_test.go` posts these exact bytes through the real
router and asserts the resulting identities equal the ones the kernel derives for
the ratified fixture: `plan.json` must compile to the fixture's `PlanID`, and each
execution must be accepted under the fixture variant's `SemanticRunID` and
`ExecutionID`.

Identity rather than shape, deliberately. A payload that had silently drifted into
declaring a *different* program would still parse and still return `201`. It could
not produce the same content-derived identity, so it fails in a test rather than in
front of an audience.

A further test counts the leaf differences between the two executions and requires
exactly one, at `initialState.entities[1].fields.hos_anchor.atom`.

## Reading a request

Clients supply `canonicalSourceKey`, never an entity identity. Identities are
derived by the kernel from the pinned `lineage`, so a client cannot invent or
collide one, and a different lineage is a different input rather than the same
input relabelled.

`world.references` being empty is a real, versioned empty world — not a missing
value.

`executorIdentity` affects `executionID` and nothing else. Running the same
semantic input on a different backend preserves `semanticRunID`, which is what
makes "did the meaning change, or only the machine" an answerable question.
