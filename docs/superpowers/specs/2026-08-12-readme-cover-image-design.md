# README Cover Image Design

**Status:** Approved design

## Goal

Use the official Maiden Lane cover image to give the repository README a
clear visual identity without turning normative or implementation documents
into branded duplicates.

## Design

- Track the existing PNG at `docs/images/cover_image.png`.
- Render it directly below the `# Maiden Lane` heading in `README.md`.
- Use ordinary Markdown image syntax with repository-relative path
  `docs/images/cover_image.png` so the image renders on GitHub and from a
  checked-out repository.
- Use concise, meaningful alt text describing Maiden Lane as a deterministic
  transformation engine.
- Keep the HLD, Inviolates, glossary, and implementation guide text-first;
  they will not repeat the cover image.

## Scope

This change adds one existing binary asset and one README image reference. It
does not alter application behavior, architecture, build inputs, published
container contents, or the meaning of any design document.

## Verification

- Confirm the tracked image is a valid PNG at the approved path.
- Confirm the README reference resolves to that exact file.
- Confirm the image remains excluded from the Docker build context through the
  existing `docs` rule.
- Confirm the repository diff contains only the image, README reference, and
  this design record.
