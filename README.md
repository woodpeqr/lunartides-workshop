# Lunartides Observability Workshop

A hands-on workshop on OpenTelemetry.

You own **entity-service**, a small REST service in this repo. It does CRUD over
entities — think network devices: servers, routers, switches, workstations. It
works fine for normal use, but under load it misbehaves — and it ships with
almost no telemetry. Adding that telemetry is how you find out what goes wrong,
and why.

**dgs-service** drives entity-service until it fails and reports a blunt symptom
of what broke — never where or why. Your job is to instrument entity-service and
build the Grafana dashboards and alerts that reveal each failure and explain its
cause.

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
| dgs-service — run a scenario (GraphiQL playground) | http://localhost:8080 |
| entity-service — the service you instrument | http://localhost:8081 |

Grafana starts empty and needs no login. Dashboards and alerts you build persist
across restarts.

## Running a scenario

dgs-service drives entity-service into a failure mode on demand. Open its
GraphiQL playground at http://localhost:8080 and run one of the three scenarios:

```graphql
mutation { meta { scenario1 } }
mutation { meta { scenario2 } }
mutation { meta { scenario3 } }
```

Each blocks until entity-service fails, then returns an error describing the
symptom. Add `log: true` (e.g. `scenario1(log: true)`) to stream every request
and response to the dgs-service container logs
(`docker compose logs -f dgs-service`).

For each scenario your goal is to:

- **define an alert** in Grafana that fires on that failure, and
- **understand what is going wrong and why** — the symptom tells you *that*
  something broke; the telemetry you add to entity-service is how you find the
  cause.

Reset entity-service between scenarios with `mutation { meta { wipe } }`.

## Stop

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
