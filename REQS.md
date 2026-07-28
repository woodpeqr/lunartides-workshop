# Workshop Requirements

## Context

Part of a 3-hour internal session for interns and associates.

- **Seminar** (~30 min) — delivered by presenter: what are logs, spans, traces, metrics, OTel concepts. Not this repo's concern.
- **Workshop** (~90 min, flexible) — this repo.

## Audience

Junior developers / interns. Assume basic Go knowledge, no prior observability experience.

## Workshop Goal

Participants leave having written real instrumentation code against a live observability backend they can query with their own eyes.

## Stack (pre-built, no participant setup required)

Docker Compose provides:
- OTel Collector (gRPC :4317, HTTP :4318)
- Prometheus (metrics)
- Loki (logs)
- Tempo (traces)
- Grafana (dashboards, :3001)

Stack must start with `docker compose up` and work out of the box.

## Service Scaffold (pre-built by instructor)

A Go service is provided with:
- OTel SDK wired: tracer, meter, and logger providers initialized and pointed at the collector
- HTTP server skeleton with graceful shutdown and config loading
- Instrumentation hooks already in place: middleware, span creation sites, metric instruments defined

Participants do NOT configure OTel plumbing. Signal flow works from minute one.

## Participant Task

Implement the actual business logic inside the scaffold — endpoint handlers, data processing, whatever the service "does".

Their own code must immediately light up in traces, metrics, and logs.

## Ambiguity Requirement

Workshop tasks must contain deliberate ambiguity. Valid sources:

1. **Underspecified assignment** — task description intentionally leaves "correct" behavior open to interpretation; participants must reason about it
2. **Latent faults in pre-built code** — bugs that are invisible without telemetry (e.g. silent errors, bad latency distribution, incorrect metric counts, off-by-one in a counter)
3. **Verification via telemetry only** — correct behavior can only be confirmed by reading traces/metrics/logs, not by reading the code

**Core constraint:** observability is not optional decoration — it is the *only* way to complete the workshop correctly.

## Out of Scope

- Multi-language support (Go only)
- Auth, persistence, production hardening
- Anything requiring internet access during the workshop beyond initial `docker compose build`
