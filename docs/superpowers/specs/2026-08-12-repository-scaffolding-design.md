# Maiden Lane Repository Scaffolding Design

**Status:** Approved for implementation planning

**Date:** 2026-08-12

**Normative architecture:** [Maiden Lane High-Level Design](2026-08-11-maiden-lane-high-level-design.md)

**Highest repository authority:** [Ratified Maiden Lane Inviolates](../../../Inviolates.md)

## 1. Purpose

This design establishes a runnable Go application shell and a basic CI/CD
pipeline without inventing transformation semantics or prematurely scaffolding
the candidate package tree from the provisional implementation sketch.

The scaffold proves that the repository can:

- build and test with the required Go toolchain;
- run an HTTP process with explicit lifecycle behavior;
- expose the health endpoints already named by the HLD;
- build and smoke-test a production-shaped container;
- enforce formatting, static analysis, tests, race detection, and vulnerability
  analysis in CI;
- publish an immutable, tested image to Amazon ECR from trusted GitHub events.

## 2. Goals

1. Establish the Go module `github.com/optimaldynamics/maiden-lane`.
2. Require Go 1.26 or newer and initially select Go 1.26.5.
3. Pin Staticcheck `2026.2rc1` and govulncheck `v1.6.0` as tracked Go tools.
4. Provide one `maiden-lane` binary with a minimal `serve` command.
5. Implement `/healthz` and `/readyz` through a small chi boundary.
6. Make startup, cancellation, and graceful shutdown explicit and testable.
7. Build and smoke-test a non-root Linux container.
8. Run CI for pull requests, `main`, and version tags.
9. Publish the exact tested release image to ECR using GitHub OIDC.
10. Make the code approachable to a Python-first engineering organization.

## 3. Non-goals

This scaffold does not implement or commit to:

- the rule language, compiler, semantic model, executor, invariant engine,
  checkpoint sealing, completeness profiles, readiness assessments, provenance
  journal, comparison engine, promotion gate, or publication path;
- the future `worker --execution-id` command;
- PostgreSQL, S3, AWS Batch, ECS, or stochflow adapters;
- typed Maiden Lane error contracts;
- exported operational metrics;
- application deployment to ECS or AWS Batch;
- AWS infrastructure provisioning;
- a first customer transformation or production vertical slice.

The provisional Go implementation sketch remains exploratory. Its unused
packages will not be created as empty directories or placeholder types.

## 4. Repository shape

The initial implementation adds only files with an immediate responsibility:

```text
go.mod
go.sum
Makefile
Dockerfile
.dockerignore
.gitignore
.github/
    dependabot.yml
    workflows/
        pipeline.yml
api/
    openapi.yaml
cmd/
    maiden-lane/
        main.go
        main_test.go
internal/
    httpapi/
        router.go
        router_test.go
docs/
    implementation/
        implementation-guide.md
```

`README.md` and `AGENTS.md` will be updated in the same change. `ERRORS.md` and
`METRICS.md` remain reserved and empty because the scaffold introduces no
stable typed application error or exported metric.

Composition remains in `cmd/maiden-lane`. HTTP translation remains in
`internal/httpapi`. No business meaning belongs in either package.

The living implementation guide is a current-state inventory, not a record of
earlier intentions. It documents only packages, commands, dependencies, and
boundaries that exist in the repository at that revision. Future packages may
appear only as clearly labeled known gaps, never in a repository tree that
implies they have been implemented. When a package is added, removed, renamed,
split, or collapsed, the same change rewrites the guide to reflect the new
current state. Git retains the historical shapes.

## 5. Go and tool version contracts

The module uses:

```text
module github.com/optimaldynamics/maiden-lane
go 1.26.0
toolchain go1.26.5
```

Go 1.26 tool directives track repository tools in `go.mod`, allowing local and
CI execution through `go tool` without depending on whatever binary happens to
be installed on the workstation.

The initial pins are:

| Tool | Requested release | Go module version |
|---|---|---|
| Staticcheck | `2026.2rc1` | `honnef.co/go/tools@v0.8.0-rc.1` |
| govulncheck | `v1.6.0` | `golang.org/x/vuln@v1.6.0` |

The Staticcheck release name and module version differ because the upstream
`2026.2rc1` tag resolves to semantic module version `v0.8.0-rc.1`. CI verifies
the actual Staticcheck version so an accidental replacement cannot silently
weaken the requested pin.

Tool upgrades are deliberate repository changes. CI never installs either tool
using `@latest`.

## 6. Application shell

The initial command is:

```text
maiden-lane serve --listen-address=:8080
```

The listen address is operational configuration. It is supplied through an
explicit CLI flag with a documented default and does not affect semantic state.
The scaffold introduces no environment-variable configuration and no
configuration framework.

Calling the binary with a missing or unknown command, or an invalid flag,
returns a concise usage error and a nonzero exit status. The future `worker`
mode is described as future work rather than implemented as a placeholder that
cannot honor its contract.

The HTTP server:

- uses `signal.NotifyContext` to receive `SIGINT` and `SIGTERM`;
- has explicit read-header and idle timeouts;
- performs bounded graceful shutdown after cancellation;
- preserves listener and shutdown error causes with `%w`;
- emits structured `slog` lifecycle records containing bounded operational
  metadata only.

## 7. HTTP boundary

The chi router initially exposes:

| Method | Path | Response | Meaning |
|---|---|---|---|
| `GET` | `/healthz` | `204 No Content` | The process is alive. |
| `GET` | `/readyz` | `204 No Content` | The process can accept requests. |

Readiness initially equals liveness because the process has no external
dependencies. This meaning must change when a dependency required to serve
requests is introduced.

`api/openapi.yaml` is the authoritative wire contract for these endpoints.
Handlers contain no semantic or publication behavior.

## 8. Documentation for a Python-first team

The project retains idiomatic Go's preference for concise code, but assumes
many maintainers and reviewers will be more familiar with Python than Go.

The following documentation standard applies:

- Every nontrivial package has package-level documentation explaining its
  responsibility, dependency boundary, and what it deliberately does not own.
- Exported declarations have normal Go doc comments.
- Non-obvious internal code explains why it is structured as it is, especially
  around determinism, canonicalization, identity, atomic transitions,
  concurrency, cancellation, fail-closed behavior, retry classification, and
  customer-data boundaries.
- Tests explain the protected property when the reason is not apparent from the
  test name and assertions.
- The implementation guide includes a compact code tour and explains Go
  conventions that are likely to surprise Python-oriented contributors.
- The guide describes the repository that exists now; it does not preserve
  obsolete package maps or promote speculative package boundaries into current
  architecture.
- Comments do not narrate syntax or duplicate obvious control flow.
- Comments are not a second source of semantic authority. Inviolates, the HLD,
  explicit contracts, and authoritative tests remain controlling.

`AGENTS.md` will be tightened to preserve this as an ongoing project convention.

## 9. Local developer interface

The `Makefile` is a discoverable wrapper over ordinary Go and Docker commands;
it does not hide custom build semantics. It provides focused targets for:

- formatting and format verification;
- module-tidiness verification;
- `go vet`;
- Staticcheck;
- unit tests;
- race tests;
- govulncheck;
- binary build;
- container build and smoke test;
- the complete local verification sequence.

The exact commands remain visible in `README.md` and the implementation guide,
so contributors can run them without `make` when diagnosing failures.

## 10. Container

The `Dockerfile` uses a pinned Go 1.26.5 build image and produces a statically
linked Linux binary. The runtime image contains only the binary and the minimum
runtime material needed for outbound TLS. It runs with a fixed non-root numeric
UID/GID and has no shell or package manager.

The image entry point is the `maiden-lane` binary and its default command starts
the server on port 8080. ECS or Batch can later override the command without
requiring separate images.

CI starts the built image, requests `/healthz` from outside the container, and
requires `204 No Content`. This checks the assembled image rather than merely
the Go process used during unit tests.

## 11. CI pipeline

One GitHub Actions workflow runs on:

- pull requests;
- pushes to `main`;
- tags matching `v*`.

The verification job performs, in order:

1. checkout;
2. Go 1.26.5 setup with module caching;
3. dependency download;
4. `gofmt` verification;
5. `go mod tidy` drift detection;
6. `go vet ./...`;
7. pinned Staticcheck over `./...`;
8. `go test ./...`;
9. `go test -race ./...`;
10. pinned govulncheck over `./...`;
11. binary build.

The container job runs only after verification succeeds. Pull requests build
and smoke-test the image without publishing it.

Workflow actions are pinned to full commit SHAs with readable version comments.
The workflow grants only the permissions required by each job.

## 12. ECR publication

For pushes to `main` and `v*` tags, the release job builds the image once,
smoke-tests that local image, and then pushes that same image to ECR.

AWS authentication uses GitHub OIDC and a narrowly scoped role. The repository
must define:

| GitHub repository variable | Meaning |
|---|---|
| `AWS_REGION` | Region containing the ECR repository. |
| `AWS_ROLE_ARN` | OIDC-assumable role permitted to publish the image. |
| `ECR_REPOSITORY` | Existing ECR repository name. |

Missing release configuration fails with a clear error instead of silently
skipping publication. No long-lived AWS access keys are used.

Every published image receives `sha-<full-commit>` as its immutable tag. A
`v*` Git ref also produces the corresponding version tag. The pipeline does not
publish floating `latest` or `main` tags. The ECR repository is expected to
enforce tag immutability. The job reports the resulting image digest.

Publication to ECR is the current CD boundary. The workflow does not modify ECS
services, Batch definitions, databases, object stores, or publication state
inside Maiden Lane.

## 13. Dependency updates

Dependabot checks:

- Go modules, including tracked tool modules;
- GitHub Actions;
- the Docker base image.

Updates remain ordinary pull requests and must pass the full pipeline. Tool or
runtime upgrades cannot bypass the pinned-version and verification contracts.

## 14. Tests and acceptance criteria

Behavioral tests cover:

- exact health and readiness status codes and empty response bodies;
- unsupported routes and methods;
- missing, unknown, and malformed CLI input;
- cancellation and bounded graceful shutdown;
- container startup and a live health request.

The scaffold is accepted only when:

- `go mod tidy` produces no diff;
- formatting, vet, Staticcheck, unit tests, race tests, and govulncheck pass;
- the binary builds with Go 1.26.5;
- the container builds, runs as non-root, and passes its health smoke test;
- the OpenAPI document matches the implemented health surface;
- `ERRORS.md` and `METRICS.md` contain no invented entries;
- the implementation guide describes the repository as it actually exists;
- the implementation guide contains no historical or speculative repository
  tree presented as current state;
- the final diff contains no speculative semantic, checkpoint, completeness,
  readiness, or AWS deployment resources.

## 15. Failure behavior

CI failures block image publication. Missing AWS configuration, OIDC failure,
container build failure, smoke-test failure, or ECR push failure makes the CD
job fail visibly.

No retry loop is added around deterministic compiler, test, lint, or
vulnerability failures. GitHub or AWS may retry transient infrastructure at
their service boundary, but the workflow does not reinterpret a failed check as
success.

The scaffold introduces no stable Maiden Lane typed errors, so `ERRORS.md`
remains an empty registry. It exports no operational metrics, so `METRICS.md`
also remains an empty registry.
