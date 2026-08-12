# Maiden Lane

![Maiden Lane deterministic transformation engine](docs/images/cover_image.png)

Maiden Lane is a deterministic transformation system for compiling, executing,
explaining, comparing, and gating mapper transformations. The repository
currently contains the runnable HTTP application shell plus local build and
container support; the transformation engine has not been implemented.

## Requirements

- Go 1.26 or newer; the repository currently selects Go 1.26.5.
- POSIX `/bin/sh`, `make`, and ordinary userland tools used by the recipes:
  `find`, `grep`, `sed`, `mkdir`, `sleep`, and `curl`.
- Docker for container checks; `curl` sends the health request from the host.

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

For make-independent diagnosis, the following runs the same stages, in the
same order, with the same tool arguments and output-bearing build path:

```sh
set -eu

# make fmt-check
gofmt_binary="$(go env GOROOT)/bin/gofmt"
unformatted="$(find . \
  -type d \( -name .git -o -name .worktrees -o -name .superpowers \) -prune \
  -o -type f -name '*.go' -exec "$gofmt_binary" -l {} +)"
if [ -n "$unformatted" ]; then
  echo "Go files need formatting:"
  echo "$unformatted"
  exit 1
fi

# make mod-check
go mod tidy -diff

# make tool-versions
staticcheck_version="$(go tool staticcheck -version)"
if [ "$staticcheck_version" != "staticcheck 2026.2rc1 (0.8.0-rc.1)" ]; then
  echo "unexpected Staticcheck version: $staticcheck_version"
  exit 1
fi
go tool govulncheck -version | grep -F "Scanner: govulncheck@v1.6.0" >/dev/null || {
  echo "govulncheck is not pinned to v1.6.0"
  exit 1
}

go vet ./...                       # make vet
go tool staticcheck ./...          # make staticcheck
go test ./...                      # make test
go test -race ./...                # make test-race
go tool govulncheck ./...          # make vulncheck
mkdir -p bin                       # make build
go build -trimpath -o bin/maiden-lane ./cmd/maiden-lane
```

`make verify` remains authoritative: Make owns the stage ordering and the
human-readable failure messages around formatting and version assertions. Use
the individual Make targets when that extra failure context is useful.

## CI/CD

Pull requests run the complete Go verification sequence and a container health
smoke test. Pushes to `main` and `v*` tags publish the tested image to Amazon
ECR using GitHub OIDC. Publication requires the repository variables
`AWS_REGION`, `AWS_ROLE_ARN`, and `ECR_REPOSITORY`.

CD currently stops at immutable ECR publication. It does not deploy ECS or AWS
Batch resources.

## Design references

- [Inviolates](Inviolates.md)
- [High-Level Design](docs/superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)
- [Current Implementation Guide](docs/implementation/implementation-guide.md)
- [Glossary](GLOSSARY.md)
- [Error Catalog](ERRORS.md)
- [Metrics Catalog](METRICS.md)
