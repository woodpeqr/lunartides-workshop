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

## Scenarios — blocking runs with a built-in feedback loop

Errors use GraphQL's native channel: a healthy call returns bare data; a failure
returns `data: null` for that field plus a top-level `errors[]` entry carrying a
blunt symptom (the WHETHER signal, never WHERE/WHY). This applies to the whole
surface — `getEntity`/`createEntity`/… and the scenarios.

**Every error also carries the verbatim exchange in `extensions`** — exactly what
went over the wire, so you can see the request that failed and what the service
returned:
```json
"extensions": {
  "request":  { "method": "POST", "path": "/entities", "body": "<verbatim JSON>" },
  "response": { "status": 500, "body": "{\"error\":\"unexpected end of JSON input\"}\n" }
}
```
GET/DELETE requests have no `body`. On a transport failure there is no `response`.

Each scenario is a BLOCKING mutation under `meta`. It drives entity-service into
a failure mode, logs **every request it makes** (one line to stdout), runs until
its stop condition, then returns the RAW result of the operation that stopped it
— as the SAME type the matching oracle query returns. Two feedback channels, no
dashboard required:

1. **The returned payload / error** — either the breaking operation's data, or a
   GraphQL error with the symptom.
2. **The live log stream** — `docker compose logs -f dgs-service` shows each
   request scroll by, then the FAILED line.

Run one at a time from the GraphiQL playground (`localhost:8080`):

```graphql
mutation { meta { scenario1 { id } } }            # like createEntity → Entity
mutation { meta { scenario2 { id name type } } }  # like listEntities → [Entity!]!
mutation { meta { scenario3 { id } } }            # like createEntity → Entity
```

Add `log: true` to stream every request + response verbatim to the dgs-service
container logs while the scenario runs (`docker compose logs -f dgs-service`):
```graphql
mutation { meta { scenario3(log: true) { id } } }
```
Default is silent. The failing operation's exchange is always on the error's
`extensions` regardless of `log`.

| Mutation | Returns | Teaches | What happens | Stops when | Outcome |
|----------|---------|---------|--------------|------------|---------|
| `scenario1` | `Entity` | **metrics** | single writer POSTs ~256KB entities flat-out; whole-file re-marshal ramps the heap to the 256m limit | first create fails (OOM) | ~30s → `errors[]: "create: service unreachable"` |
| `scenario2` | `[Entity!]!` | **traces** | grows the store, timing a list each batch; every list re-scans the whole file | a list exceeds 80ms (healthy ~1ms) | ~80s → the slow list as data; or `errors[]: "scenario timed out before entity-service failed"` if the bound is never crossed |
| `scenario3` | `Entity` | **logs** | 8 concurrent writers tear the non-atomic store file | first operation returns a 5xx | ~1s → `errors[]: "create: internal error reported by service"` |

The error message is the WHETHER symptom. WHERE/WHY lives in entity-service
telemetry: scenario1 → the Go-memory metric ramp + `es-flood-oom` alert;
scenario2 → the list-latency trace/metric + `es-slow-list` alert; scenario3 →
the Loki log `failed to parse entity store file` + `es-corruption` alert.

`listEntities` returns the full records (`entities { id name type status
version }`), not just a count — handy for eyeballing what the store holds.

Notes:
- One scenario at a time; a second call while one runs returns a busy result.
- Scenario runs block on the HTTP request — if your client times out, the run's
  context is cancelled and it stops. Scenario1/3 finish fast; scenario2 takes
  ~80s (seeding is O(n²): each create re-marshals the whole growing file), so use
  a generous client timeout.
- entity-service has `restart: unless-stopped`, so after scenario1's OOM it comes
  back. `mutation { meta { wipe } }` resets the store between runs for a clean
  baseline.
- The continuous dashboard demo (metrics ramp, alerts firing) still works: a
  scenario runs long enough (scenario1 ~30s, scenario2 ~80s) to move the panels
  and trip its alert. Metric export is 5s, alert eval 10s.
