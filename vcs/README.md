# vcs — source-control service (workshop subject)

The student-owned REST service for the *"Observing the Garbage That Is Our Code"*
workshop. Minimal, in-memory, content-addressed source control. Deliberately
buggy in its VCS logic; the OTel plumbing is pre-wired and trusted.

See the repo-root `CONTRACT.md` (frozen wire contract) and `WORKSHOP_PLAN.md`.

## Prerequisites

- **Go 1.26** (the toolchain auto-downloads if you have 1.21+ with
  `GOTOOLCHAIN=auto`, the default).
- The observability stack running: from the repo root, `docker compose up`.
  This provides the OTel Collector (gRPC :4317), Prometheus, Loki, Tempo, and
  Grafana (:3001).

## Run

vcs runs as a **container** in the docker-compose stack (service `vcs`). From
the repo root:

```sh
docker compose up --build
```

This starts the whole system — infra + `vcs` + `dgs` — with one command. `vcs`
listens on `:8081` (published to the host) and exports OTLP to
`otel-collector:4317` over the compose network.

Optional host run for a fast edit loop (point OTLP at the published collector):

```sh
cd vcs
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 go run ./cmd/vcs
```

## Configuration (environment variables)

| Variable | Default | Purpose |
|---|---|---|
| `VCS_ADDR` | `:8081` | HTTP listen address |
| `OTEL_SERVICE_NAME` | `vcs` | Service name on emitted telemetry |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC collector endpoint (compose sets `otel-collector:4317`) |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Plaintext OTLP (workshop collector) |

## Routes (CONTRACT §3)

| # | Method & path | Purpose |
|---|---|---|
| R1 | `POST /objects` | Store blob (content-addressed + dedup) |
| R2 | `GET /objects/{hash}` | Fetch blob by hash |
| R3 | `POST /commits` | Create flat snapshot commit |
| R4 | `GET /commits/{id}` | Commit metadata |
| R5 | `PUT /refs/{name}` | Point a ref at a commit |
| R6 | `GET /refs/{name}` | Resolve ref → commit id |
| R7 | `GET /checkout/{ref}` | Reassemble files at ref (round-trip oracle) |
| R8 | `POST /wipe` | Reset all state (trusted utility) |
| R9 | `GET /healthz` | Liveness (trusted) |
| R10 | `GET /diff?a=&b=` | Path-level diff (stretch) |

## Layout

```
vcs/
├── cmd/vcs/main.go              # config, telemetry init, mux wiring, graceful shutdown
├── internal/telemetry/          # OTel provider plumbing (PRE-WIRED, do not edit)
├── internal/store/              # in-memory objects/commits/refs (buggy logic — the workshop)
├── internal/handlers/           # one handler per route (CONTRACT §3)
├── Dockerfile                   # multi-stage build → distroless (compose service "vcs")
└── go.mod
```

## Your job (the workshop)

The plumbing works from minute one. Adding **signal** — spans, metrics, and
structured logs on the hot paths — is the exercise. Grep for `TODO(workshop)`
to find where signal belongs, and `internal/telemetry` for how the providers
are wired.
