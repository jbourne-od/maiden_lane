# Maiden Lane Error Catalog

**Status:** Reserved; no error types are defined yet

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

## Registered errors

No error types have been defined.

| Error type | Stable code | Meaning | Retry classification | Publication consequence |
|---|---|---|---|---|
