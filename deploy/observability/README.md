# Local observability stack

A development stack that renders what Maiden Lane already emits: an
OpenTelemetry Collector, Tempo for traces, Prometheus for metrics, and Grafana
with a provisioned dashboard.

This is not a deployment artifact. Storage is ephemeral, retention is hours, and
Grafana runs with authentication disabled on a loopback-only port.

```sh
make observe-up      # start it
make observe-logs    # follow the logs
make observe-down    # stop it and discard the data
```

The application is not one of the services. It runs on the host and exports to
the collector, which keeps the ordinary rebuild-and-restart loop free of
container rebuilds:

```sh
export OTEL_TRACES_EXPORTER=otlp
export OTEL_METRICS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
./bin/maiden-lane serve
```

An `http` scheme is enough to select an insecure exporter, so no additional
variable is needed locally. `OTEL_EXPORTER_OTLP_INSECURE`, if set, must agree
with the scheme.

## Port conflicts

The published ports are all overridable, and `make observe-up` refuses to start
when another process already holds one:

```sh
make observe-up ML_GRAFANA_PORT=3900 ML_PROMETHEUS_PORT=9990
```

The variables are `ML_GRAFANA_PORT`, `ML_PROMETHEUS_PORT`, `ML_TEMPO_PORT`,
`ML_OTLP_HTTP_PORT`, `ML_OTLP_GRPC_PORT`, and `ML_COLLECTOR_HEALTH_PORT`.

The refusal exists because the failure it prevents is genuinely hard to read.
Docker reported a successful bind for a port another process already held, the
stack came up reporting every container healthy, and Grafana's URL served an
unrelated application. Every symptom pointed at the stack. On the machine this
was built on, a `devpod` process held both 3000 and 8080 and an editor held
4317.

## What to look at

Grafana has one provisioned dashboard, **Maiden Lane — Semantic Spine**. Its
queries were all written against names read back out of Prometheus, because the
OTLP-to-Prometheus translation rewrites both metric names and label keys, and a
query against a guessed name returns nothing while looking exactly like an idle
system. `METRICS.md` lists the translated names.

Two things are worth knowing before reading it:

- **Rate panels go blank when nothing is running.** A local process is idle most
  of the time. The **Telemetry delivery** panel is scraped from the collector
  itself rather than the application, so it keeps reporting either way and is
  what distinguishes "idle" from "broken". Points accepted but not sent means
  the exporter or its destination; nothing accepted means the application is not
  exporting.
- **Metrics arrive every 60 seconds**, on the SDK's periodic export cycle rather
  than Prometheus's 15-second scrape. The Prometheus datasource declares that
  interval so Grafana computes `$__rate_interval` wide enough to contain two
  samples. It is set deliberately and lowering it makes every rate panel blank.

Traces are searchable in Tempo through Grafana's Explore view. A single
execution produces the HTTP server span plus the five semantic spans:
`compile`, `execute_spine`, `execute_transition`, `seal_checkpoint`, and
`assess_readiness`.

There is no metric-to-trace jump. The usual mechanism is exemplars, which the
application disables on purpose: an exemplar attaches a trace ID to a metric
point, which is a correlation channel the dimension allowlist does not govern.

## Moving this to AWS

The receivers and processors in `collector.yaml` are the durable half — they are
what the application talks to, and they do not change. The exporters are the
swappable tail, and both were chosen because a managed deployment keeps them
rather than replacing them:

- `prometheusremotewrite` is the exporter Amazon Managed Prometheus takes.
  Moving there changes the endpoint and adds the `sigv4auth` extension.
- `otlp` is a wire protocol rather than a vendor. It reaches Tempo here and a
  managed OTLP endpoint elsewhere.

A pull-based `prometheus` exporter would also work locally, but it would be a
local-only shape that has to be rewritten as remote write later. Every component
used here is present in the AWS Distro for OpenTelemetry, which ships a subset
of contrib; staying inside that subset is what keeps the migration a matter of
endpoints and credentials.

The application itself needs no change to move. It speaks OTLP and nothing else:
there is no vendor SDK, no `/metrics` endpoint, and no second definition of any
instrument.

## Not included

- **No log pipeline.** Logs go to stdout as structured JSON and are not
  exported over OTLP. Adding Loki would give log search but not correlation,
  because the log records carry no trace or span IDs; the standard fix is a
  `slog.Handler` that reads the span context off the request context, which
  needs the call sites to pass one. Wiring Loki before that is wiring up the
  half that matters least.
- **No span metrics or service graph.** These need the collector's
  `spanmetrics` connector, which is a real decision about cardinality rather
  than a checkbox.
- **No alerting rules.** The stack answers "what is happening", not "what should
  page someone".
