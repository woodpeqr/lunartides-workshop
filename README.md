# Lunartides Observability Workshop

A hands-on workshop on OpenTelemetry.

You own **vcs**, a small source-control service. Its code is correct but has no
safeguards, so under load it crashes or degrades — and telemetry is the only way
to see when and why.

**dgs** exercises vcs and reports a blunt PASS/FAIL verdict. When it complains,
you open Grafana and use metrics, traces, and logs to find the cause. Your job is
to instrument vcs and build the dashboards and alerts that reveal a failure
before dgs ever complains.

Everything you need runs locally with one command.

## Run

```bash
docker compose up
```

Then open:

| What | Where |
|---|---|
| Grafana — build your dashboards and alerts here | http://localhost:3001 |
| dgs — run the oracle, watch it PASS/FAIL | http://localhost:8080 |
| vcs — the service you instrument | http://localhost:8081 |

Grafana starts empty and needs no login. Dashboards and alerts you build persist
across restarts.

Stop:

```bash
docker compose down       # keep your dashboards and alerts
docker compose down -v    # also wipe them
```

## Environment variables

**vcs**

| Variable | Default | Purpose |
|---|---|---|
| `VCS_ADDR` | `:8081` | Listen address |
| `OTEL_SERVICE_NAME` | `vcs` | Service name in telemetry |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` | Where telemetry is sent |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Disable TLS to the collector |

**dgs**

| Variable | Default | Purpose |
|---|---|---|
| `DGS_ADDR` | `:8080` | Listen address |
| `VCS_BASE_URL` | `http://vcs:8081` | vcs service to exercise |
