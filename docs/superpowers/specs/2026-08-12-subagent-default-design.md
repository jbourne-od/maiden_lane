# Subagent-Default Workflow Design

**Status:** Approved design

## Goal

Make subagent execution the explicit default for Maiden Lane agent work so
implementation, investigation, and review benefit from isolated context and
independent verification.

## Rule

Add this policy to `AGENTS.md` section 22, before its existing division and
ownership guidance:

> Default to subagent execution whenever multi-agent support is available and
> authorized. Work inline only when a concrete reason makes delegation unsafe,
> impossible, or materially less effective; state that reason before
> proceeding.

The rule is a strong default, not an impossible absolute. Tool availability,
authorization, tightly coupled file ownership, or a concrete loss of
effectiveness may justify inline work. Convenience or habit does not.

## Existing Safeguards

Retain the current requirements that:

- work must divide without conflicting ownership;
- multiple writers should not edit the same tightly coupled files without
  explicit coordination;
- one agent owns final integration;
- independent diffs are inspected and verified together;
- substantial changes receive an independent review focused on Inviolates and
  hidden semantic changes.

## Scope and Verification

This is an agent-working-policy change only. It does not alter application
behavior or repository architecture. Verification consists of checking the
new rule's exact placement, confirming the existing safeguards remain intact,
and ensuring `AGENTS.md` contains no contradictory default.
