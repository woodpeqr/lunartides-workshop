# Observability Stack

A pre-configured local observability stack built on OpenTelemetry, Prometheus, Loki, Tempo, and Grafana.

## Stack Components

- **OTel Collector** — receives telemetry over gRPC (:4317) and HTTP (:4318), forwards to Prometheus, Loki, and Tempo
- **Prometheus** — metrics storage and querying
- **Loki** — log aggregation
- **Tempo** — distributed tracing backend
- **Grafana** — dashboards and data exploration; pre-provisioned with Prometheus, Loki, and Tempo data sources
- **vcs** — student-owned REST source-control service (the workshop subject); telemetry pre-wired, business logic deliberately buggy. Built from `./vcs`.
- **dgs** — instructor-built GraphQL master / blunt PASS/FAIL oracle over `vcs`; zero telemetry. Built from the sibling `../lunartides-workers` repo.

## Prerequisites

- Docker Desktop
- Git
- Go 1.26+ (only for running the services on the host; the stack builds them in-container)
- The sibling `lunartides-workers` repo checked out next to this one (provides the `dgs` service; compose builds `../lunartides-workers`)

## Setup

1. Clone the repository:

   ```bash
   git clone https://github.com/woodpeqr/lunartides-workshop.git
   cd lunartides-workshop
   ```

2. Start the stack (builds `vcs` + `dgs` and starts the infra):

   ```bash
   docker compose up --build
   ```

## Endpoints

| Service | Address |
|---|---|
| Grafana | http://localhost:3001 |
| dgs GraphiQL (master oracle) | http://localhost:8080 |
| vcs REST (subject) | http://localhost:8081 |
| OTel Collector gRPC | :4317 |
| OTel Collector HTTP | :4318 |
| Prometheus | http://localhost:9090 |

Grafana anonymous access is enabled — no login required.

## Stopping the Stack

```bash
docker compose down
```

To remove persisted volumes as well:

```bash
docker compose down -v
```
