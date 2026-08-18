SHELL := /bin/sh

GO ?= go
BINARY_DIR ?= bin
BINARY ?= $(BINARY_DIR)/maiden-lane
IMAGE ?= maiden-lane:local

.PHONY: help fmt fmt-check mod-check tool-versions openapi openapi-check vet staticcheck test test-race vulncheck build verify migrate migrate-status store-check container-build container-smoke container-check observe-preflight observe-up observe-down observe-logs demo

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
	@echo "migrate          apply pending migrations to $$MAIDEN_LANE_DATABASE_URL"
	@echo "migrate-status   show which migrations are applied"
	@echo "store-check      run the PostgreSQL adapter tests against a throwaway database"
	@echo "container-check  build and smoke-test $(IMAGE)"
	@echo "observe-up       start the local collector, Tempo, Prometheus, and Grafana"
	@echo "observe-down     stop the observability stack and discard its data"
	@echo "observe-logs     follow the observability stack logs"
	@echo "demo             build, then walk one semantic run against a local server"

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
# Migrations are an explicit step, never something the application does to itself.
# The application holds no DDL privilege by design, so that a compromised process
# cannot rewrite the tables holding sealed artifacts; see
# internal/adapters/postgres/schema.go.
#
# dbmate is pinned in go.mod and run through `go tool`, so this needs no
# workstation-global install and CI gets the same version a developer does.
MIGRATIONS_DIR ?= internal/adapters/postgres/migrations
DBMATE = $(GO) tool dbmate --migrations-dir $(MIGRATIONS_DIR) --no-dump-schema

migrate:
	@test -n "$(MAIDEN_LANE_DATABASE_URL)" || { \
		echo "MAIDEN_LANE_DATABASE_URL is not set"; \
		exit 1; \
	}
	@$(DBMATE) --url "$(MAIDEN_LANE_DATABASE_URL)" up

migrate-status:
	@test -n "$(MAIDEN_LANE_DATABASE_URL)" || { \
		echo "MAIDEN_LANE_DATABASE_URL is not set"; \
		exit 1; \
	}
	@$(DBMATE) --url "$(MAIDEN_LANE_DATABASE_URL)" status

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
	$(DBMATE) --url $(STORE_URL) up >/dev/null || { \
		echo "migrations failed against the throwaway database"; \
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

# The observability stack is a development aid, not part of verification. It
# never joins `verify`: it needs Docker, it is long-running rather than
# pass/fail, and what it produces is a thing to look at rather than an
# assertion. The assertions about telemetry live in internal/observability.
OBSERVE_COMPOSE ?= deploy/observability/compose.yaml
ML_GRAFANA_PORT ?= 3000
ML_PROMETHEUS_PORT ?= 9090
ML_TEMPO_PORT ?= 3200
ML_OTLP_HTTP_PORT ?= 4318
ML_OTLP_GRPC_PORT ?= 4317
export ML_GRAFANA_PORT ML_PROMETHEUS_PORT ML_TEMPO_PORT ML_OTLP_HTTP_PORT ML_OTLP_GRPC_PORT

# Every published port is checked for an existing listener first. This is not
# defensiveness for its own sake: Docker Desktop reported a successful bind for a
# port another process already held, the stack came up "healthy", and Grafana's
# URL served an unrelated Express application. A silent loss like that costs far
# more to diagnose than a refusal, because every symptom points at the stack
# rather than at the squatter.
#
# The check runs only when none of the stack's own containers are up, which
# removes any need to recognise the container runtime's listeners. Excluding them
# by process name looked reasonable and was wrong immediately: the names vary by
# runtime -- Docker Desktop, OrbStack, Colima, docker-proxy -- and the first
# attempt flagged the stack's own published ports as conflicts. Asking "is our
# stack down?" is a question with a reliable answer, so when it is down every
# listener on these ports is by definition somebody else's.
observe-preflight:
	@running="$$(docker compose --file $(OBSERVE_COMPOSE) ps --quiet 2>/dev/null | tr -d '[:space:]')"; \
	if [ -n "$$running" ]; then \
		exit 0; \
	fi; \
	conflict=0; \
	for entry in "Grafana:$(ML_GRAFANA_PORT)" "Prometheus:$(ML_PROMETHEUS_PORT)" \
	             "Tempo:$(ML_TEMPO_PORT)" "OTLP/HTTP:$(ML_OTLP_HTTP_PORT)" \
	             "OTLP/gRPC:$(ML_OTLP_GRPC_PORT)"; do \
		name="$${entry%%:*}"; port="$${entry##*:}"; \
		holder="$$(lsof -nP -iTCP:$$port -sTCP:LISTEN 2>/dev/null \
			| awk 'NR>1 {print $$1}' | sort -u | tr '\n' ' ')"; \
		if [ -n "$$holder" ]; then \
			echo "port $$port ($$name) is already held by: $$holder"; \
			conflict=1; \
		fi; \
	done; \
	if [ $$conflict -ne 0 ]; then \
		echo; \
		echo "Refusing to start: Docker may report success and still lose the bind,"; \
		echo "leaving the published URL serving the other process."; \
		echo "Override the port instead, for example:"; \
		echo "  make observe-up ML_GRAFANA_PORT=3900 ML_PROMETHEUS_PORT=9990"; \
		exit 1; \
	fi

observe-up: observe-preflight
	@docker compose --file $(OBSERVE_COMPOSE) up --detach --wait || { \
		docker compose --file $(OBSERVE_COMPOSE) logs --tail 40; \
		exit 1; \
	}
	@echo "Grafana      http://127.0.0.1:$(ML_GRAFANA_PORT)"
	@echo "Prometheus   http://127.0.0.1:$(ML_PROMETHEUS_PORT)"
	@echo "Tempo        http://127.0.0.1:$(ML_TEMPO_PORT)"
	@echo
	@echo "Point a Maiden Lane process at the collector with:"
	@echo "  export OTEL_TRACES_EXPORTER=otlp"
	@echo "  export OTEL_METRICS_EXPORTER=otlp"
	@echo "  export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:$(ML_OTLP_HTTP_PORT)"

observe-down:
	@docker compose --file $(OBSERVE_COMPOSE) down --volumes --remove-orphans

observe-logs:
	@docker compose --file $(OBSERVE_COMPOSE) logs --follow --tail 40

# `demo` is a guided walk through one semantic run, for showing someone what this
# system does. It starts a throwaway in-memory server on an unused port, drives it
# with the committed example payloads over the public HTTP API, and stops it again.
#
# In-memory rather than PostgreSQL on purpose: the demo needs no Docker and leaves
# nothing behind, and durability is not what it is demonstrating. To narrate the
# same runs against real storage, start `serve` yourself with a database URL and
# pass the address:  scripts/demo.sh http://127.0.0.1:8080
#
# Traces and metrics are exported only when the local collector is reachable.
# Enabling them unconditionally would make the common case -- no stack running --
# wait on a connection that is not there, and that delay would read as the demo
# hanging rather than as telemetry being unavailable.
#
# The port is checked for an existing listener before the server starts, and the
# demo refuses rather than proceeding. This repo already paid for the alternative
# once: a bind that appears to succeed while another process holds the port
# produces symptoms that all point at our own code. Override with
# `make demo ML_DEMO_PORT=8123`.
#
# `go run` is deliberately not used to start the server. It compiles to a cache
# path and execs a CHILD process, so the PID a trap can kill is only the wrapper:
# the server survived teardown, kept the port, and the next run refused to start
# because "something already answers" -- which was our own leaked server.
DEMO_URL ?=
ML_DEMO_PORT ?= 8099

demo: build
	@set -eu; \
	if [ -n "$(DEMO_URL)" ]; then \
		exec scripts/demo.sh "$(DEMO_URL)"; \
	fi; \
	port="$(ML_DEMO_PORT)"; \
	if curl --silent --connect-timeout 0.25 --max-time 0.5 --output /dev/null \
		"http://127.0.0.1:$$port/healthz" 2>/dev/null; then \
		echo "something already answers on 127.0.0.1:$$port."; \
		echo "if it is a maiden-lane server, run: scripts/demo.sh http://127.0.0.1:$$port"; \
		echo "otherwise choose another port: make demo ML_DEMO_PORT=8123"; \
		exit 1; \
	fi; \
	log="$$(mktemp -t maiden-lane-demo)"; \
	if curl --silent --connect-timeout 0.25 --max-time 0.5 --output /dev/null \
		"http://127.0.0.1:$(ML_OTLP_HTTP_PORT)/v1/traces" 2>/dev/null; then \
		echo "collector reachable on $(ML_OTLP_HTTP_PORT); exporting traces and metrics"; \
		OTEL_TRACES_EXPORTER=otlp OTEL_METRICS_EXPORTER=otlp \
		OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:$(ML_OTLP_HTTP_PORT)" \
		OTEL_EXPORTER_OTLP_INSECURE=true \
		$(BINARY) serve --listen-address "127.0.0.1:$$port" >"$$log" 2>&1 & \
	else \
		$(BINARY) serve --listen-address "127.0.0.1:$$port" >"$$log" 2>&1 & \
	fi; \
	server=$$!; \
	trap 'kill $$server 2>/dev/null || true; rm -f "$$log"' EXIT INT TERM; \
	ready=""; \
	attempt=0; \
	while [ "$$attempt" -lt 120 ]; do \
		if [ "$$(curl --silent --connect-timeout 0.25 --max-time 0.5 --output /dev/null \
			--write-out '%{http_code}' "http://127.0.0.1:$$port/healthz" || true)" = "204" ]; then \
			ready="yes"; break; \
		fi; \
		kill -0 $$server 2>/dev/null || { echo "the demo server exited:"; cat "$$log"; exit 1; }; \
		attempt=$$((attempt + 1)); \
		sleep 0.25; \
	done; \
	test -n "$$ready" || { echo "the demo server did not become ready"; cat "$$log"; exit 1; }; \
	scripts/demo.sh "http://127.0.0.1:$$port"
