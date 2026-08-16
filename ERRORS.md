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

`app.Run` additionally wraps every machinery failure in an unexported error
whose text names only the closed phase that failed. The wrapper is not part of
the catalog because callers must not match on it: use `errors.Is` for
`context.Canceled` and `context.DeadlineExceeded`, and `errors.As` for the two
registered types above.

Every message here is fixed text plus a closed code token. No payload,
identifier, path, digest, or raw dependency text appears in any error string,
so an error is safe to log verbatim. Causes are preserved through `Unwrap`, so
diagnosis uses the cause chain rather than message parsing.
