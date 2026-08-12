# README Cover Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the approved official Maiden Lane cover image directly below the repository README title.

**Architecture:** Treat the user-supplied PNG as an immutable documentation asset and copy it byte-for-byte into the feature worktree. Reference it once from the README using repository-relative Markdown; do not duplicate it in normative or implementation documents.

**Tech Stack:** Markdown, PNG, Git.

## Global Constraints

- Work only within `/Users/jacob/Development/od/maiden_lane/...`.
- Source asset: `/Users/jacob/Development/od/maiden_lane/docs/images/cover_image.png`.
- Destination asset: `docs/images/cover_image.png` in the active feature worktree.
- The destination SHA-256 must be `eced712b1114b86c05d72ac28d4195948f69c9f76a51ced2b2f559f6925213b0`.
- The image must remain a `2172 x 724`, 8-bit RGB, non-interlaced PNG.
- Render it only in `README.md`, directly below `# Maiden Lane`.
- Use exact alt text `Maiden Lane deterministic transformation engine`.
- Do not modify the image, HLD, Inviolates, glossary, implementation guide, application code, build behavior, or registries.
- Preserve unrelated untracked workstation files in the canonical checkout.

---

### Task 1: Add the official README cover

**Files:**
- Create: `docs/images/cover_image.png`
- Modify: `README.md:1`

**Interfaces:**
- Consumes: the approved user-supplied PNG at the absolute source path above.
- Produces: one tracked documentation asset and one repository-relative README reference.

- [ ] **Step 1: Verify the source asset before copying it**

Run from the feature worktree:

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

- [ ] **Step 2: Copy the approved binary asset byte-for-byte**

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

Leave the rest of the README unchanged.

- [ ] **Step 4: Verify the asset, reference, build-context exclusion, and scope**

Run:

```bash
test -f docs/images/cover_image.png
test "$(shasum -a 256 docs/images/cover_image.png | awk '{print $1}')" = "eced712b1114b86c05d72ac28d4195948f69c9f76a51ced2b2f559f6925213b0"
file docs/images/cover_image.png
test "$(sed -n '3p' README.md)" = '![Maiden Lane deterministic transformation engine](docs/images/cover_image.png)'
rg -n '^docs$' .dockerignore
git diff --check
git status --short
git diff --stat
```

Expected:

- the destination has the approved digest and PNG description;
- the README image reference is line 3 and resolves to the tracked path;
- `.dockerignore` excludes `docs`, so the cover does not enter the application image build context;
- the implementation diff contains only `README.md` and `docs/images/cover_image.png`.

- [ ] **Step 5: Commit the cover image**

```bash
git add README.md docs/images/cover_image.png
git commit -m "docs: add README cover image"
```
