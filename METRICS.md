# Maiden Lane Metrics Catalog

**Status:** Reserved; no metrics are exported yet

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

No metrics are currently exported.

| Name | Instrument | Unit | Permitted attributes or labels | Meaning |
|---|---|---|---|---|
