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

The permitted values are deliberately closed or bounded:

- `http.request.method` is one of `GET`, `HEAD`, `POST`, `PUT`, `DELETE`,
  `CONNECT`, `OPTIONS`, `TRACE`, `PATCH`, or `OTHER`.
- `http.route` is a trusted route template supplied at handler registration,
  never a request path or parameter value.
- `http.response.status_code` is present only for a valid observed terminal
  status from 100 through 599. It is omitted when no valid status exists.

The three instruments are registered when the observability runtime starts.
They record only for handlers explicitly wrapped at registration. Health,
readiness, unmatched, and method-not-allowed requests are excluded. The current
production router contains only health and readiness routes, so it exports no
HTTP request points yet.

Exemplars are disabled. Metric points cannot carry trace attributes outside
the label allowlist above.
