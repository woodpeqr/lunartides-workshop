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

**Every error also carries the verbatim exchange in `extensions.exchange`** —
exactly what went over the wire, ordered most-telling-first (bodies last):
```json
"extensions": {
  "exchange": {
    "response": { "status": 500, "body": "{\"error\":\"unexpected end of JSON input\"}\n" },
    "request":  { "path": "/entities", "method": "POST", "body": "<verbatim JSON>" }
  }
}
```
GET/DELETE requests have no `body`. On a transport failure (e.g. the service
OOM-died mid-request) there is no `response` — the absence itself says "no reply
came back", distinct from a 4xx/5xx where the service did answer.

Each scenario is a BLOCKING mutation under `meta`. It drives entity-service into
a failure mode and runs **until entity-service fails** — so the expected outcome
is always a GraphQL error (the induced failure). Every scenario field is typed
`String!`, but that string is only the by-contract return on the essentially
never-reached no-failure path (`"scenario N stopped"`); the bugs are never fixed,
nothing "survives". Two feedback channels, no dashboard required:

1. **The GraphQL error** — the symptom, with the verbatim exchange in
   `extensions.exchange` (except the slow-list timeout, which is a dgs-side
   judgement with no service error to attach).
2. **The live log stream** — `docker compose logs -f dgs-service` (with `log:true`).

Run one at a time from the GraphiQL playground (`localhost:8080`) — the fields
are scalars, no sub-selection:

```graphql
mutation { meta { scenario1 } }
mutation { meta { scenario2 } }
mutation { meta { scenario3 } }
```

Add `log: true` to stream every request + response verbatim to the dgs-service
container logs while the scenario runs:
```graphql
mutation { meta { scenario3(log: true) } }
```
Default is silent. The failing operation's exchange is on the error's
`extensions` regardless of `log`.

| Mutation | Teaches | What happens | Ends with |
|----------|---------|--------------|-----------|
| `scenario1` | **metrics** | single writer POSTs ~256KB entities flat-out; whole-file re-marshal ramps the heap to the 256m limit | ~30s → `errors[]: "create: service unreachable"` (OOM) |
| `scenario2` | **traces** | grows the store, timing a list each batch; every list re-scans the whole file, so latency climbs | ~30s → `errors[]: "GET /entities timed out — the request took longer than 50ms"` |
| `scenario3` | **logs** | 8 concurrent writers tear the non-atomic store file | ~1s → `errors[]: "create: internal error reported by service"` (5xx) |

Note on the slow-list bound: it is **50ms** (healthy is ~1ms), not higher,
because entity-service re-marshals the whole store on every list — the transient
allocation OOMs the 256m container at ~100–150ms of list latency, before a larger
bound could be reached. The error names the bound, never the actual duration.

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
