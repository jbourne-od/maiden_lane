# Maiden Lane Error Catalog

**Status:** Active; the `internal/app` machinery errors are registered

This document is the registry for Maiden Lane-owned typed errors. It records
the errors that callers, workers, gates, or operators are expected to recognize
and act upon. It does not catalog arbitrary wrapped implementation errors from
dependencies.

When a registered error is introduced or changed, update this file in the same
change. Each entry must:

- identify the Go error type and stable external code, when one exists;
- state what the error means rather than merely where it occurred;
- classify whether repetition can plausibly change the outcome;
- state its effect on publication eligibility;
- expose only bounded, customer-safe metadata.

Deterministic semantic failures are not retryable. Error wrapping must preserve
the causal error so callers can use `errors.Is` or `errors.As` without parsing
message text. Retry classifications must conform to Inviolate 18.

## What belongs in this catalog

Maiden Lane separates two failure channels that must never be conflated.

A **semantic result** is a deterministic answer about meaning: an invalid plan,
a failed protected invariant, or an artifact integrity failure. The spine
returns these as typed values with a **nil** Go error. They are not listed
here, they are not retryable, and re-running identical inputs reproduces them
exactly.

A **machinery error** is the application's inability to reach an answer:
cancellation, an unavailable dependency, malformed input at the initial
boundary, or an internal inconsistency. Only these are Go errors, and only
these are cataloged below.

The distinction is load-bearing. Treating a protected invariant failure as a
retryable error would invite a caller to retry until an invalid artifact
slipped through, which Inviolate 6 forbids.

## Registered errors

| Error type | Stable code | Meaning | Retry classification | Publication consequence |
|---|---|---|---|---|
| `app.InvalidInputError` | `COMPILATION_REQUEST_INCOMPLETE` | The initial request omitted a compiler semantics version or declared no transformation. | Not retryable; the caller must supply complete canonical input. | No execution and no artifact exist. |
| `app.InvalidInputError` | `RUN_BINDING_INPUT_INCOMPLETE` | The initial request omitted an initial state, world, executor identity, or provenance policy. | Not retryable; the caller must supply complete canonical input. | No execution and no artifact exist. |
| `app.InfrastructureUnavailableError` | `REQUIRED_DEPENDENCY_UNAVAILABLE` | A required application-boundary dependency was unavailable. The vocabulary exists; this in-memory slice has no such production dependency yet. | Retryable; repetition can plausibly change the outcome. | The last independently verified dependency-closed prefix is retained; the unreached suffix produced no artifact. |

This slice implements no publication or promotion path, so the publication
column states which artifacts survive the failure rather than describing a
gate that does not exist yet. Update it when publication is implemented.

## HTTP problem types

The HTTP boundary reports failure as RFC 9457 `application/problem+json` using
a closed vocabulary of stable type URIs under
`https://maiden-lane.optimaldynamics.com/problems/`. A call site selects a
ratified type; it cannot compose a new one.

| Type | Status | Meaning |
|---|---|---|
| `invalid-request` | 400 | The body was malformed, carried an unknown member, or omitted a required reference. |
| `tenant-required` | 400 | The tenant header was absent, repeated, or not of the declared form. |
| `not-found` | 404 | No such artifact for this tenant. Also returned for another tenant's artifact and for an unmatched path. |
| `method-not-allowed` | 405 | The resource does not support the requested method. |
| `unsupported-media-type` | 415 | The request body was not `application/json`. |
| `invalid-plan` | 422 | Compilation rejected the declarations. Carries the closed diagnostic codes and no `planID`. |
| `invalid-semantic-input` | 422 | Canonical input was incomplete or unsupported, including declarations the compiler cannot canonicalize at all. |
| `internal-error` | 500 | An internal inconsistency, including a stored plan that no longer reproduces its own identity when recompiled. Storage integrity failures surface here rather than as a client error, because the caller's request was valid and the fault is entirely server side. |
| `dependency-unavailable` | 503 | A required dependency was unavailable, or the caller's context was cancelled. Retryable. |

Two boundaries matter more than the table.

**A deterministic semantic outcome is never a problem document.** A failed
protected invariant, an artifact integrity failure, and a `needs_input`
readiness verdict are answers the computation produced. `POST /v1/executions`
returns them as `200` with a typed `failure` field and every artifact that
verified beforehand. Reporting them as `5xx` would misclassify a correct
refusal as a service fault and invite a retry that can only reproduce it.

**Problem documents carry fixed text only.** Titles and details are constants
selected by type. No payload, digest, entity reference, evidence body, tenant
value, or Go error text is representable in one, so a problem is safe to log
verbatim and cannot reflect caller input back to a caller.

`app.Run` additionally wraps every machinery failure in an unexported error
whose text names only the closed phase that failed. The wrapper is not part of
the catalog because callers must not match on it: use `errors.Is` for
`context.Canceled` and `context.DeadlineExceeded`, and `errors.As` for the two
registered types above.

Every message here is fixed text plus a closed code token. No payload,
identifier, path, digest, or raw dependency text appears in any error string,
so an error is safe to log verbatim. Causes are preserved through `Unwrap`, so
diagnosis uses the cause chain rather than message parsing.
