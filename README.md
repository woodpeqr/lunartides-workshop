# LunarTides OTel Workshop

## The Story

**LunarTides** is a B2B data company that sells a contact enrichment API. When a client sends a contact record, LunarTides runs it through a pipeline of internal services — Flux (data normalization), Rift (external lookup), Swell (scoring), and Shoal (aggregation) — before returning an enriched result with confidence scores.

Clients have been reporting intermittent failures and incorrect scores. LunarTides' engineering team insists their services are functioning correctly.

## Your Role

You are a junior developer at a client company that integrates heavily with the LunarTides enrichment API. Your manager has asked you to investigate the reliability complaints and produce concrete evidence of what is going wrong.

**Your job:** Instrument the MoonTide orchestrator — the piece of code that calls LunarTides' services and assembles the final result — so you can observe the pipeline end-to-end and produce telemetry-backed evidence.

## Instructor Role

Your instructor plays the role of a skeptical codeowner at LunarTides who believes their services are working correctly. They will challenge your findings and ask you to back up claims with data.

## Services

| Service | Owner | Description |
|---|---|---|
| **MoonTide** | You (client) | Orchestrator — calls the workers in sequence, assembles the result |
| **Flux** | LunarTides | Normalizes incoming contact data |
| **Rift** | LunarTides | Looks up the contact against external data sources |
| **Swell** | LunarTides | Computes confidence scores |
| **Shoal** | LunarTides | Aggregates and packages the final enrichment result |

The LunarTides worker binaries are provided as pre-compiled releases. Source code is not available.

## Observability Stack

The workshop environment includes a pre-configured observability stack:

- **Grafana** (http://localhost:3000) — dashboards and data exploration
- **Prometheus** — metrics storage
- **Loki** — log aggregation
- **Tempo** — distributed tracing
- **OpenTelemetry Collector** — receives telemetry from MoonTide on ports 4317 (gRPC) and 4318 (HTTP)

## Getting Started

### Prerequisites

- Docker and Docker Compose
- Git
- One of: Go 1.22+, Python 3.11+, or .NET 8 SDK

### Setup

1. Clone the repository:

   ```bash
   git clone https://github.com/woodpeqr/lunartides-workshop.git
   cd lunartides-workshop
   ```

2. Check out a language branch:

   ```bash
   # Choose one:
   git checkout feature/go
   git checkout feature/python
   git checkout feature/csharp
   ```

3. Build the Docker images (required before the first run — this downloads the worker binaries):

   ```bash
   docker compose build
   ```

4. Start the stack:

   ```bash
   docker compose up
   ```

5. Open Grafana at http://localhost:3000

   Default credentials: admin / lunartides

## Pre-session Checklist

Complete these steps before the workshop begins to avoid spending session time on setup.

- [ ] Docker is installed and running (`docker info` succeeds)
- [ ] Git is installed (`git --version` succeeds)
- [ ] Your chosen language runtime is installed and on PATH
- [ ] You have cloned the repo and checked out your language branch
- [ ] `docker compose build` completes without errors
- [ ] `docker compose up` starts without errors
- [ ] Grafana is reachable at http://localhost:3000
- [ ] You can log in to Grafana (admin / lunartides)

If `docker compose build` fails with a network error, check that you have internet access and that GitHub releases are not blocked on your network.

## Stopping the Stack

```bash
docker compose down
```

To remove persisted volumes as well:

```bash
docker compose down -v
```
