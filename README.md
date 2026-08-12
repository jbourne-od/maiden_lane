# Maiden Lane

Maiden Lane is a deterministic transformation system for compiling, executing,
explaining, comparing, and gating mapper transformations. The repository
currently contains the runnable HTTP application shell plus local build and
container support; the transformation engine has not been implemented.

## Requirements

- Go 1.26 or newer; the repository currently selects Go 1.26.5.
- Docker for container checks.
- `make` for the documented convenience targets. Every target prints or wraps
  ordinary Go and Docker commands.

Staticcheck 2026.2rc1 and govulncheck v1.6.0 are tracked in `go.mod` and run
through `go tool`; workstation-global installations are not used.

## Quick start

```bash
make verify
go run ./cmd/maiden-lane serve --listen-address=127.0.0.1:8080
```

The current HTTP surface is:

- `GET /healthz` — process liveness, returning `204 No Content`.
- `GET /readyz` — process readiness, returning `204 No Content`.

## Common commands

```bash
make help
make fmt
make verify
make container-check
```

`make verify` is the authoritative complete local verification command. Its
`Makefile` recipe enforces formatting and module tidiness, verifies the pinned
analysis-tool versions, runs vet, static analysis, unit tests, race tests, and
the vulnerability scan, then builds `bin/maiden-lane`.

Use the individual Make targets when diagnosing a particular stage; the
`Makefile` is authoritative for their exact commands and failure semantics.

## CI/CD

No remote pipeline exists at this revision. `make verify` and
`make container-check` are the local release prerequisites.

## Design references

- [Inviolates](Inviolates.md)
- [High-Level Design](docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)
- [Current Implementation Guide](docs/implementation/implementation-guide.md)
- [Glossary](GLOSSARY.md)
- [Error Catalog](ERRORS.md)
- [Metrics Catalog](METRICS.md)
