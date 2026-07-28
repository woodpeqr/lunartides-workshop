# Lunartides Observability Workshop

A hands-on workshop on OpenTelemetry.

You own **entity-service**, a small REST service in this repo. It does CRUD over
entities — think network devices: servers, routers, switches, workstations. Its
code is correct, but it has no safeguards: a single JSON file is its only store,
read and rewritten whole on every request, with no cache, no locking, and no
limits. Under load it degrades, corrupts that file, or runs out of memory — and
telemetry is the only way to see when and why.

**dgs-service** exercises entity-service and reports a blunt PASS/FAIL verdict.
When it complains, you open Grafana and use metrics, traces, and logs to find
the cause. Your job is to instrument entity-service and build the dashboards and
alerts that reveal a failure before dgs-service ever complains.

dgs-service is a genuine black box: its source is **not in this repo**. The stack
pulls it as a prebuilt image, so you cannot read the oracle to shortcut the
answer — the telemetry you build is the only way through.

Everything you need runs locally with one command.

## Run

```bash
docker compose up
```

Then open:

| What | Where |
|---|---|
| Grafana — build your dashboards and alerts here | http://localhost:3001 |
| dgs-service — run the oracle, watch it PASS/FAIL | http://localhost:8080 |
| entity-service — the service you instrument | http://localhost:8081 |

Grafana starts empty and needs no login. Dashboards and alerts you build persist
across restarts.

Stop:

```bash
docker compose down       # keep your dashboards and alerts
docker compose down -v    # also wipe them
```

## Environment variables

**entity-service**

| Variable | Default | Purpose |
|---|---|---|
| `ENTITY_ADDR` | `:8081` | Listen address |
| `ENTITY_STORE_PATH` | `/data/entities.json` | JSON store file |
| `OTEL_SERVICE_NAME` | `entity-service` | Service name in telemetry |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` | Where telemetry is sent |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Disable TLS to the collector |
| `OTEL_METRIC_EXPORT_INTERVAL` | `5000` | Metric export interval (ms) |

**dgs-service**

| Variable | Default | Purpose |
|---|---|---|
| `DGS_ADDR` | `:8080` | Listen address |
| `ENTITY_BASE_URL` | `http://entity-service:8081` | entity-service to exercise |
