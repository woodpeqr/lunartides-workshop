# Instructor tooling

Not part of the student experience. Scripts to (re)build the reference Grafana
dashboard + alerts and to smoke-test the full stack.

Grafana dashboards and alert rules live in `grafana/data/grafana.db` (gitignored,
local only). These scripts recreate them against a running stack, so the
reference setup is reproducible without shipping it to students.

## Scripts

Run against a running stack (`docker compose up`), Grafana at `localhost:3001`
(admin/lunartides), Prometheus at `localhost:9090`, dgs-service at `localhost:8080`.

- `build_dashboard.py` — pushes the "entity-service — Observability" dashboard
  (RED, latency, store growth, Go runtime, logs) into grafana.db.
- `build_alerts.py` — creates the 3 scenario alerts (folder "entity-service
  alerts", 10s eval interval).
- `verify_surface.py` — exercises every dgs-service query/mutation incl. the
  `meta` namespace against the live stack; expects all PASS.

```bash
python3 instructor/build_dashboard.py
python3 instructor/build_alerts.py
python3 instructor/verify_surface.py
```

## Scenarios → lesson → signal → alert

Toggle via dgs-service GraphiQL (`localhost:8080`):
`mutation { meta { scenario(id: N) { active message } } }` (toggle same id off).

| id | Name | Teaches | What happens | Fires |
|----|------|---------|--------------|-------|
| 1 | createFlood | **metrics** | one writer creates fat entities; the whole store file is re-marshalled per write, Go heap ramps to the 256m limit → OOM | `es-flood-oom` (Go memory > 130MB) |
| 2 | slowList | **traces** | seeds ~3000 entities, then readers hammer `GET /entities`; each list re-scans the whole multi-MB file → p95 climbs | `es-slow-list` (list p95 > 30ms; healthy ~1ms) |
| 3 | corruption | **logs** | 8 concurrent writers tear the non-atomic store file; reads then fail to parse → 5xx + error log | `es-corruption` (5xx rate > 0.2/s) + Loki log `failed to parse entity store file` |

## Watching requests fail in the playground

Two ways to see individual request outcomes flip PASS→FAIL as a scenario degrades
entity-service, both from the GraphiQL playground at `localhost:8080`:

1. **Re-run a probe on a loop.** With a scenario active, put this in the editor
   and press Cmd/Ctrl+Enter repeatedly (GraphiQL has no built-in auto-repeat, so
   it's manual re-run, or hold the shortcut):
   ```graphql
   mutation { probeRoundTrip(name:"x", type:"switch", status:"active") { verdict message entity { id version } } }
   ```
   Healthy → `PASS "round-trip verified"`. Under load → `FAIL` with a symptom.

2. **One-shot burst.** `probeBurst` fires N probes server-side and returns each
   attempt, so a SINGLE run shows the PASS/FAIL mix:
   ```graphql
   mutation { probeBurst(count: 20) { passed failed attempts { verdict message entity { id } } } }
   ```
   During the corruption scenario this returns e.g. `passed:0 failed:20`, each
   attempt carrying `"create step failed (internal error reported by service)"`.

`listEntities` now returns the full records (`entities { id name type status
version }`), not just a count — handy for eyeballing what the service actually
holds between scenario runs.

Notes:
- Scenario 1 uses a single writer on purpose: concurrent writers would tear the
  file (scenario 3's failure) and stall the heap ramp before OOM.
- Scenario 2 uses few readers (2) so concurrent whole-file unmarshals stay under
  the OOM ceiling — it must show latency, not crash.
- entity-service has `restart: unless-stopped` so the flood OOM is repeatable.
  After an OOM the store file may be large/torn; `mutation { meta { wipe } }`
  resets it. Between scenario runs, wipe (or recreate the container) for a clean
  baseline.
- Metric export interval is 5s (`OTEL_METRIC_EXPORT_INTERVAL`), alert eval 10s,
  so ramps are visible and alerts fire within the scenario's lifetime.
