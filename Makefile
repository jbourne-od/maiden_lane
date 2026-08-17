SHELL := /bin/sh

GO ?= go
BINARY_DIR ?= bin
BINARY ?= $(BINARY_DIR)/maiden-lane
IMAGE ?= maiden-lane:local

.PHONY: help fmt fmt-check mod-check tool-versions openapi openapi-check vet staticcheck test test-race vulncheck build verify store-check container-build container-smoke container-check

help:
	@echo "fmt              format Go source"
	@echo "fmt-check        fail if Go source is not formatted"
	@echo "mod-check        fail if go.mod or go.sum is not tidy"
	@echo "openapi          regenerate Go code from api/openapi.yaml"
	@echo "openapi-check    fail if generated code has drifted from api/openapi.yaml"
	@echo "tool-versions    verify pinned analysis tool versions"
	@echo "vet              run go vet"
	@echo "staticcheck      run the pinned Staticcheck"
	@echo "test             run unit tests"
	@echo "test-race        run unit tests with the race detector"
	@echo "vulncheck        run the pinned govulncheck"
	@echo "build            build $(BINARY)"
	@echo "verify           run the complete local CI sequence"
	@echo "store-check      run the PostgreSQL adapter tests against a throwaway database"
	@echo "container-check  build and smoke-test $(IMAGE)"

fmt:
	@gofmt="$$( $(GO) env GOROOT)/bin/gofmt"; \
	find . \
		-type d \( -name .git -o -name .worktrees -o -name .superpowers \) -prune \
		-o -type f -name '*.go' -exec "$$gofmt" -w {} +

fmt-check:
	@gofmt="$$( $(GO) env GOROOT)/bin/gofmt"; \
	files="$$(find . \
		-type d \( -name .git -o -name .worktrees -o -name .superpowers \) -prune \
		-o -type f -name '*.go' -exec "$$gofmt" -l {} +)"; \
	if [ -n "$$files" ]; then \
		echo "Go files need formatting:"; \
		echo "$$files"; \
		exit 1; \
	fi

mod-check:
	$(GO) mod tidy -diff

tool-versions:
	@actual="$$( $(GO) tool staticcheck -version )"; \
	test "$$actual" = "staticcheck 2026.2rc1 (0.8.0-rc.1)" || { \
		echo "unexpected Staticcheck version: $$actual"; \
		exit 1; \
	}
	@$(GO) tool govulncheck -version | grep -F "Scanner: govulncheck@v1.6.0" >/dev/null || { \
		echo "govulncheck is not pinned to v1.6.0"; \
		exit 1; \
	}

# api/openapi.yaml is the authoritative wire contract. These targets generate
# Go from it and never the reverse; the generated files are not hand-edited.
openapi:
	cd api && $(GO) tool oapi-codegen -config oapi-codegen-server.yaml openapi.yaml
	cd api && $(GO) tool oapi-codegen -config oapi-codegen-client.yaml openapi.yaml

# Regenerate into a scratch directory and compare. This fails both when the
# contract changed without regenerating and when a generated file was edited by
# hand, which are the two ways a published contract silently stops being true.
openapi-check:
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	sed "s|^output: .*|output: $$tmp/server.go|" api/oapi-codegen-server.yaml > "$$tmp/server.yaml"; \
	sed "s|^output: .*|output: $$tmp/client.go|" api/oapi-codegen-client.yaml > "$$tmp/client.yaml"; \
	( cd api && $(GO) tool oapi-codegen -config "$$tmp/server.yaml" openapi.yaml ) && \
	( cd api && $(GO) tool oapi-codegen -config "$$tmp/client.yaml" openapi.yaml ) && \
	if ! diff -u internal/httpapi/openapiv1/openapi_gen.go "$$tmp/server.go" >/dev/null 2>&1; then \
		echo "generated server code is stale or hand-edited; run: make openapi"; \
		diff -u internal/httpapi/openapiv1/openapi_gen.go "$$tmp/server.go" | head -40; \
		exit 1; \
	fi; \
	if ! diff -u internal/httpapiclient/client_gen.go "$$tmp/client.go" >/dev/null 2>&1; then \
		echo "generated client code is stale or hand-edited; run: make openapi"; \
		diff -u internal/httpapiclient/client_gen.go "$$tmp/client.go" | head -40; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

# Generated packages are excluded because their findings are not actionable:
# the files are reproduced from api/openapi.yaml and must never be hand-edited.
# The exclusion is by package and named explicitly, so no check is weakened for
# any hand-written code.
GENERATED_PACKAGES := \
	github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1 \
	github.com/optimaldynamics/maiden-lane/internal/httpapiclient

staticcheck:
	@packages="$$( $(GO) list ./... | grep -v -x -F -e '$(word 1,$(GENERATED_PACKAGES))' -e '$(word 2,$(GENERATED_PACKAGES))' )"; \
	$(GO) tool staticcheck $$packages

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vulncheck:
	$(GO) tool govulncheck ./...

build:
	mkdir -p $(BINARY_DIR)
	$(GO) build -trimpath -o $(BINARY) ./cmd/maiden-lane

verify: fmt-check mod-check tool-versions openapi-check vet staticcheck test test-race vulncheck build

# The PostgreSQL adapter tests need a database, so they live here rather than in
# verify: `make verify` must stay runnable with no Docker and no database.
#
# The target supplies the database URL itself, so the adapter tests never skip
# when this runs, and it then asserts that they actually executed. A silent skip
# would look exactly like a pass and would leave the adapter unverified in CI.
STORE_CONTAINER ?= maiden-lane-store-check
STORE_PORT ?= 55433
STORE_URL ?= "postgres://postgres:maiden@127.0.0.1:$(STORE_PORT)/maidenlane?sslmode=disable"

store-check:
	@set -eu; \
	docker rm --force $(STORE_CONTAINER) >/dev/null 2>&1 || true; \
	docker run --detach --name $(STORE_CONTAINER) \
		--env POSTGRES_PASSWORD=maiden --env POSTGRES_DB=maidenlane \
		--publish 127.0.0.1:$(STORE_PORT):5432 postgres:17-alpine >/dev/null; \
	trap 'docker rm --force $(STORE_CONTAINER) >/dev/null 2>&1 || true' EXIT INT TERM; \
	ready=""; \
	attempt=0; \
	while [ "$$attempt" -lt 60 ]; do \
		if docker exec $(STORE_CONTAINER) pg_isready --username postgres --dbname maidenlane >/dev/null 2>&1; then \
			ready="yes"; \
			break; \
		fi; \
		attempt=$$((attempt + 1)); \
		sleep 0.5; \
	done; \
	test -n "$$ready" || { \
		echo "PostgreSQL did not become ready"; \
		docker logs $(STORE_CONTAINER); \
		exit 1; \
	}; \
	if ! output="$$(MAIDEN_LANE_TEST_POSTGRES_URL=$(STORE_URL) \
		$(GO) test -count=1 -v ./internal/adapters/postgres 2>&1)"; then \
		echo "$$output"; \
		exit 1; \
	fi; \
	echo "$$output"; \
	if echo "$$output" | grep -q -- "--- SKIP"; then \
		echo "adapter tests skipped; store-check must never skip them"; \
		exit 1; \
	fi; \
	echo "$$output" | grep -q -- "--- PASS: TestCorruptedRowsFailClosed" || { \
		echo "the corruption tests did not run"; \
		exit 1; \
	}

container-build:
	docker build --tag $(IMAGE) .

container-smoke:
	@set -eu; \
	container_name="maiden-lane-smoke-$$$$"; \
	container_id="$$(docker run --detach --name "$$container_name" --publish 127.0.0.1::8080 $(IMAGE))"; \
	trap 'docker rm --force "$$container_id" >/dev/null 2>&1 || true' EXIT INT TERM; \
	port="$$(docker port "$$container_id" 8080/tcp | sed -n 's/.*://p')"; \
	status=""; \
	attempt=0; \
	while [ "$$attempt" -lt 40 ]; do \
		status="$$(curl --silent --connect-timeout 0.25 --max-time 0.5 --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$$port/healthz" || true)"; \
		[ "$$status" = "204" ] && break; \
		attempt=$$((attempt + 1)); \
		sleep 0.25; \
	done; \
	test "$$status" = "204" || { \
		echo "container health status = $$status, want 204"; \
		docker logs "$$container_id"; \
		exit 1; \
	}; \
	user="$$(docker inspect --format '{{.Config.User}}' "$$container_id")"; \
	test "$$user" = "65532:65532" || { \
		echo "container user = $$user, want 65532:65532"; \
		exit 1; \
	}

container-check: container-build container-smoke
