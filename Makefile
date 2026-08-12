SHELL := /bin/sh

GO ?= go
BINARY_DIR ?= bin
BINARY ?= $(BINARY_DIR)/maiden-lane
IMAGE ?= maiden-lane:local

.PHONY: help fmt fmt-check mod-check tool-versions vet staticcheck test test-race vulncheck build verify container-build container-smoke container-check

help:
	@echo "fmt              format Go source"
	@echo "fmt-check        fail if Go source is not formatted"
	@echo "mod-check        fail if go.mod or go.sum is not tidy"
	@echo "tool-versions    verify pinned analysis tool versions"
	@echo "vet              run go vet"
	@echo "staticcheck      run the pinned Staticcheck"
	@echo "test             run unit tests"
	@echo "test-race        run unit tests with the race detector"
	@echo "vulncheck        run the pinned govulncheck"
	@echo "build            build $(BINARY)"
	@echo "verify           run the complete local CI sequence"
	@echo "container-check  build and smoke-test $(IMAGE)"

fmt:
	$(GO) fmt ./...

fmt-check:
	@files="$$(gofmt -l .)"; \
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

vet:
	$(GO) vet ./...

staticcheck:
	$(GO) tool staticcheck ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vulncheck:
	$(GO) tool govulncheck ./...

build:
	mkdir -p $(BINARY_DIR)
	$(GO) build -trimpath -o $(BINARY) ./cmd/maiden-lane

verify: fmt-check mod-check tool-versions vet staticcheck test test-race vulncheck build

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
		status="$$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$$port/healthz" || true)"; \
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
