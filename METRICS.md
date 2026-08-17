# Maiden Lane Metrics Catalog

**Status:** Active

This document is the registry for operational telemetry metrics exported by
Maiden Lane. It records the metric names and semantics that dashboards, alerts,
and operators may rely upon. Semantic comparison measurements and protected
regression results are domain artifacts, not entries in this catalog unless
they are also exported as telemetry instruments.

When an exported metric is introduced, renamed, or materially changed, update
this file in the same change. Each entry must:

- give the exact exported name;
- identify the instrument kind and unit;
- list every permitted attribute or label and its bounded value set;
- explain what is measured and when the instrument is recorded;
- avoid customer data, semantic provenance, and unbounded identifiers.

Customer IDs, entity IDs, `SemanticRunID`, `ExecutionID`, `AttemptID`, and other
unbounded identifiers are forbidden as metric dimensions. Metric definitions
must conform to Inviolate 17.

## Exported metrics

| Name | Instrument | Unit | Permitted attributes or labels | Meaning |
|---|---|---|---|---|
| `http.server.request.duration` | `Float64Histogram` | `s` | `http.request.method`, `http.route`, optional `http.response.status_code` | Duration of a matched non-health HTTP server request |
| `http.server.request.body.size` | `Int64Histogram` | `By` | `http.request.method`, `http.route`, optional `http.response.status_code` | Request body bytes actually observed by the server wrapper |
| `http.server.response.body.size` | `Int64Histogram` | `By` | `http.request.method`, `http.route`, optional `http.response.status_code` | Response body bytes written by the server wrapper |
| `maiden_lane.semantic.phase.duration` | `Float64Histogram` | `s` | `phase`, `result` | Duration of one completed semantic spine phase |
| `maiden_lane.semantic.structural.operations` | `Int64Counter` | `operations` | `operation_kind`, `result` | Structural operations of a committed patch, or of a materialized patch that was atomically refused |
| `maiden_lane.semantic.checkpoints` | `Int64Counter` | `checkpoints` | `result` | Checkpoints whose seal committed, and seal requests actually refused |
| `maiden_lane.semantic.invariant.failures` | `Int64Counter` | `failures` | `invariant_code` | Failing protected invariant results produced by the spine |
| `maiden_lane.semantic.readiness.assessments` | `Int64Counter` | `assessments` | `profile_kind`, `verdict` | Completed immutable readiness assessments |

The permitted values are deliberately closed or bounded:

- `http.request.method` is one of `GET`, `HEAD`, `POST`, `PUT`, `DELETE`,
  `CONNECT`, `OPTIONS`, `TRACE`, `PATCH`, or `OTHER`.
- `http.route` is a trusted route template supplied at handler registration,
  never a request path or parameter value.
- `http.response.status_code` is present only for a valid observed terminal
  status from 100 through 599. It is omitted when no valid status exists.

The three HTTP instruments are registered when the observability runtime
starts. They record only for handlers explicitly wrapped at registration.
Health, readiness, unmatched, and method-not-allowed requests are excluded.

The versioned `/v1` routes are wrapped, so the production process now exports
HTTP request points for them. The dimension is always the registered route
pattern, never the request path: `/v1/plans/{planID}` is one bounded series,
while the path it matched carries a content digest and would mint a new series
per plan. Because plan identities are caller-influenced, using the path would
make metric cardinality growable by anyone able to call the API.

### Semantic dimension values

The five semantic instruments admit exactly these values:

- `phase`: `compile`, `execute_transition`, `seal_checkpoint`,
  `assess_readiness`, `execute_spine`.
- `result` on `phase.duration`: `success`, `ready`, `needs_input`,
  `invalid_plan`, `protected_invariant_failed`, `artifact_integrity_failed`,
  `invalid_input`, `cancelled`, `infrastructure_unavailable`,
  `internal_error`.
- `result` on `structural.operations`: `accepted` or `rejected`.
- `result` on `checkpoints`: `sealed` or `rejected`.
- `operation_kind`: `insert`, `relate`, `update`.
- `profile_kind`: `cm.v1` or `optimizer.v1`. This is a bounded operational
  category and is never a `ProfileID`.
- `verdict`: `ready` or `needs_input`.
- `invariant_code`: the closed operation-invariant and rule-invariant codes.
  Compilation diagnostics and integrity codes are deliberately excluded, since
  neither is a protected invariant failure.

### Histogram bucket boundaries

Every histogram here declares explicit boundaries through an OTel view. Leaving
the aggregation unset inherits the SDK's default boundaries, which begin
`[0, 5, 10, 25, ...]` and are shaped for milliseconds. Both duration
instruments are measured in **seconds**, so the defaults put every real
observation in one bucket.

This is recorded because it is not a theoretical concern. With the defaults in
place, a measured run had a mean phase duration of 104 microseconds, all 17
observations fell below `le=5`, and `histogram_quantile(0.95, ...)` reported
**4.75 seconds** — wrong by roughly four orders of magnitude, and plausible
enough that an operator would act on it.

| Instrument | Boundaries | Why |
|---|---|---|
| `maiden_lane.semantic.phase.duration` | `0.0001` … `10` s, sixteen boundaries concentrated below 100 ms | Phases are in-process transformations over loaded state; observed p50 is around 150 µs. |
| `http.server.request.duration` | The HTTP semantic conventions' recommended set, verbatim | This is semantic-convention surface, so dashboards, alert libraries, and managed backends expect that distribution. Coarse for an in-memory response, correct once a network and a database are in the path. |
| `http.server.request.body.size`, `http.server.response.body.size` | `64` B … `4` MiB | The conventions recommend no distribution and the default one stops at 10000, which a plan declaration for the ratified fixture already exceeds at 7225 bytes. |

Changing a boundary set changes every stored series and silently invalidates
recorded history, so treat it as a breaking change to this catalog.

### Prometheus names

The names above are OTel instrument names. An operator writing queries needs
the translated Prometheus names, which differ: dots become underscores, the unit
is appended as a suffix, and counters gain `_total`. The names below were read
back out of Prometheus rather than derived on paper, because a query written
against a guessed name returns no data and looks exactly like an idle system.

| OTel instrument | Prometheus series |
|---|---|
| `maiden_lane.semantic.phase.duration` | `maiden_lane_semantic_phase_duration_seconds_{bucket,sum,count}` |
| `maiden_lane.semantic.structural.operations` | `maiden_lane_semantic_structural_operations_total` |
| `maiden_lane.semantic.checkpoints` | `maiden_lane_semantic_checkpoints_total` |
| `maiden_lane.semantic.invariant.failures` | `maiden_lane_semantic_invariant_failures_total` |
| `maiden_lane.semantic.readiness.assessments` | `maiden_lane_semantic_readiness_assessments_total` |
| `http.server.request.duration` | `http_server_request_duration_seconds_{bucket,sum,count}` |
| `http.server.request.body.size` | `http_server_request_body_size_bytes_{bucket,sum,count}` |
| `http.server.response.body.size` | `http_server_response_body_size_bytes_{bucket,sum,count}` |

Attribute keys are translated the same way: `operation_kind` and `result` keep
their names, while `http.request.method` becomes `http_request_method`.

The four counters escape having their unit appended twice only because each
instrument name already ends in its own unit word — `maiden_lane.semantic.checkpoints`
with unit `checkpoints` yields `maiden_lane_semantic_checkpoints_total`, not
`..._checkpoints_checkpoints_total`. Renaming one of these instruments without
also changing its unit to a braced UCUM annotation would start duplicating the
suffix.

Application metrics reach Prometheus by remote write on the SDK's periodic
export cycle of **60 seconds**, not on Prometheus's scrape interval. Any
`rate()` or `increase()` window must therefore span at least two minutes;
a shorter window contains at most one sample and returns nothing.

### Semantic recording rules

- Phase duration records once when each started phase completes, carrying its
  exact terminal result. Readiness phases use `ready` or `needs_input`; other
  successful phases use `success`. The outer `execute_spine` duration always
  receives the terminal result of the whole use case, even when a nested phase
  rejects.
- Structural operations count accepted operations only after the whole patch
  commits, so a transition that forms a team records one insert and two
  relates. If a materialized patch is atomically refused, every operation it
  proposed counts once as `rejected`. A rejection that happens before any patch
  is materialized records no operation at all.
- Checkpoints count `sealed` only after a seal commits and `rejected` only when
  an actual seal request is refused. A checkpoint the run never reached is not
  a rejected checkpoint, and machinery failure during sealing is not a refusal.
- Invariant failures increment once per failing protected invariant result,
  including a pre-patch rejection.
- Readiness assessments increment once per completed immutable assessment. No
  assessment is recorded for a checkpoint that does not exist.

These rules exist so telemetry can never imply that an unmaterialized patch, an
unreached checkpoint, or an absent assessment existed.

Unadmitted values fail closed rather than being relabeled. An optional
dimension is omitted when its value is not one of the closed tokens above,
because emitting a placeholder would assert a classification the spine never
made; the always-required `phase` and `result` instead fall back to
`internal_error`, which is a deliberate tripwire rather than a category.

The semantic instruments are registered because the corresponding use case
exists, and `POST /v1/executions` is now a public caller of it, so the
production process does record semantic points. Nothing about the recording
rules changes: an execution driven over HTTP produces exactly the phases,
structural operations, checkpoints, invariant failures, and readiness
assessments described above, because the observer is the same non-authoritative
adapter the use case has always been given.

No identity reaches a metric dimension along that path. Tenant identifiers,
plan identities, run and execution identities, and artifact digests are absent
from every instrument here by construction: the semantic instruments receive
only a bounded projection with no identity fields, and the HTTP instruments
receive only the registered route pattern.

Exemplars are disabled. OTel views repeat each instrument's attribute
allowlist inside the SDK, so a future recording call cannot add a dimension.
Metric points cannot carry trace attributes outside the label allowlist above.
