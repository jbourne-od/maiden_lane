# Subagent Default and README Cover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make subagent execution Maiden Lane's explicit agent-work default and add the approved official cover image to the repository README.

**Architecture:** Keep the policy and visual changes independent: `AGENTS.md` owns the durable working rule, while `README.md` references one byte-identical documentation asset. Neither change alters application behavior, architecture, build contents, or normative Maiden Lane semantics.

**Tech Stack:** Markdown, PNG, Git.

## Global Constraints

- Work only within `/Users/jacob/Development/od/maiden_lane/...`.
- Work in the existing isolated branch `codex/initial-repository-scaffold`.
- Preserve the ratified Inviolates, HLD, glossary, implementation guide, error registry, metrics registry, Go application, and CI/CD behavior.
- Make subagent execution the default whenever multi-agent support is available and authorized.
- Permit inline work only for a concrete reason that makes delegation unsafe, impossible, or materially less effective, and require that reason to be stated before proceeding.
- Retain the existing multi-agent ownership, integration, and independent-review safeguards.
- Source cover: `/Users/jacob/Development/od/maiden_lane/docs/images/cover_image.png`.
- Destination cover: `docs/images/cover_image.png` in the active worktree.
- Destination SHA-256: `eced712b1114b86c05d72ac28d4195948f69c9f76a51ced2b2f559f6925213b0`.
- Destination format: `2172 x 724`, 8-bit RGB, non-interlaced PNG.
- Render the cover only in `README.md`, directly below the title, with exact alt text `Maiden Lane deterministic transformation engine`.
- Preserve unrelated untracked workstation files in the canonical checkout.

---

### Task 1: Make subagent execution the default

**Files:**
- Modify: `AGENTS.md:728`

**Interfaces:**
- Consumes: the approved subagent-default design in `docs/superpowers/specs/2026-08-12-subagent-default-design.md`.
- Produces: the durable default workflow for future Maiden Lane agent tasks.

- [ ] **Step 1: Confirm the current policy does not yet express the approved default**

Run:

```bash
if rg -n 'Default to subagent execution' AGENTS.md; then
  echo 'approved default already exists unexpectedly' >&2
  exit 1
fi
```

Expected: success with no output, demonstrating that the approved rule is not already present.

- [ ] **Step 2: Insert the approved rule at the start of section 22**

Change the opening of section 22 to exactly:

```markdown
# 22. Working With Multiple Agents

Default to subagent execution whenever multi-agent support is available and
authorized. Work inline only when a concrete reason makes delegation unsafe,
impossible, or materially less effective; state that reason before proceeding.

Use multiple agents only when work can genuinely be divided without conflicting
ownership.
```

Keep the remainder of section 22 unchanged.

- [ ] **Step 3: Verify the rule and existing safeguards together**

Run:

```bash
test "$(rg -c 'Default to subagent execution' AGENTS.md)" -eq 1
test "$(rg -c 'state that reason before proceeding' AGENTS.md)" -eq 1
rg -n 'Avoid assigning multiple writers|One agent should own the final integration|inspect every diff|Use an independent review pass' AGENTS.md
git diff --check
git diff -- AGENTS.md
```

Expected: one default rule, one exception-reporting rule, and all four existing safeguards remain visible.

- [ ] **Step 4: Commit the policy change**

```bash
git add AGENTS.md
git commit -m "docs: default to subagent execution"
```

---

### Task 2: Add the official README cover

**Files:**
- Create: `docs/images/cover_image.png`
- Modify: `README.md:1`

**Interfaces:**
- Consumes: the approved source PNG and `docs/superpowers/specs/2026-08-12-readme-cover-image-design.md`.
- Produces: one tracked documentation asset and one repository-relative README reference.

- [ ] **Step 1: Verify the source asset**

Run:

```bash
source_image=/Users/jacob/Development/od/maiden_lane/docs/images/cover_image.png
test -f "$source_image"
test "$(shasum -a 256 "$source_image" | awk '{print $1}')" = "eced712b1114b86c05d72ac28d4195948f69c9f76a51ced2b2f559f6925213b0"
file "$source_image"
```

Expected `file` output includes:

```text
PNG image data, 2172 x 724, 8-bit/color RGB, non-interlaced
```

- [ ] **Step 2: Copy the asset byte-for-byte**

Run:

```bash
mkdir -p docs/images
cp /Users/jacob/Development/od/maiden_lane/docs/images/cover_image.png docs/images/cover_image.png
cmp -s /Users/jacob/Development/od/maiden_lane/docs/images/cover_image.png docs/images/cover_image.png
```

Expected: `cmp` exits successfully with no output.

- [ ] **Step 3: Add the README cover reference**

Change the beginning of `README.md` to exactly:

```markdown
# Maiden Lane

![Maiden Lane deterministic transformation engine](docs/images/cover_image.png)

Maiden Lane is a deterministic transformation system for compiling, executing,
```

Leave the remainder of the README unchanged.

- [ ] **Step 4: Verify the cover and implementation scope**

Run:

```bash
test "$(shasum -a 256 docs/images/cover_image.png | awk '{print $1}')" = "eced712b1114b86c05d72ac28d4195948f69c9f76a51ced2b2f559f6925213b0"
file docs/images/cover_image.png
test "$(sed -n '3p' README.md)" = '![Maiden Lane deterministic transformation engine](docs/images/cover_image.png)'
rg -n '^docs$' .dockerignore
git diff --check
git status --short
git diff --stat
```

Expected:

- the image has the approved digest and format;
- the README reference is on line 3 and resolves to the tracked path;
- `.dockerignore` continues to exclude `docs` from the application image context;
- this task changes only `README.md` and `docs/images/cover_image.png`.

- [ ] **Step 5: Commit the cover image**

```bash
git add README.md docs/images/cover_image.png
git commit -m "docs: add README cover image"
```

---

### Task 3: Verify the combined documentation change

**Files:**
- Verify: `AGENTS.md`
- Verify: `README.md`
- Verify: `docs/images/cover_image.png`

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: final evidence that the policy and cover coexist without application or architecture changes.

- [ ] **Step 1: Run the focused acceptance checks**

Run:

```bash
test "$(rg -c 'Default to subagent execution' AGENTS.md)" -eq 1
test "$(sed -n '3p' README.md)" = '![Maiden Lane deterministic transformation engine](docs/images/cover_image.png)'
test "$(shasum -a 256 docs/images/cover_image.png | awk '{print $1}')" = "eced712b1114b86c05d72ac28d4195948f69c9f76a51ced2b2f559f6925213b0"
git diff --check
git status --short
```

Expected: all checks pass and the worktree is clean.

- [ ] **Step 2: Audit branch scope**

Run:

```bash
git diff --stat d68588e2d1a0ad5043362f19becbcb6d3de5c952...
git diff --name-status d68588e2d1a0ad5043362f19becbcb6d3de5c952...
```

Expected: after the already approved design and plan records, implementation changes are limited to `AGENTS.md`, `README.md`, and `docs/images/cover_image.png`; application, HLD, Inviolates, registries, and CI/CD files are unchanged.

- [ ] **Step 3: Perform independent final review**

Use `superpowers:requesting-code-review` to verify policy wording, safeguard retention, image identity, README placement, and scope. Address any Critical or Important findings, then run the focused acceptance checks again.
