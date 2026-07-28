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

Each scenario is a BLOCKING mutation under `meta`. It drives entity-service into
a failure mode, logs **every request it makes** (one line to stdout), runs until
its stop condition, then returns the RAW result of the operation that stopped it
— rendered with the SAME result type as the matching oracle query (no bespoke
wrapper). Two feedback channels, no dashboard required:

1. **The returned payload** — the actual `EntityResult`/`ListResult` of the
   breaking operation (e.g. a `FAIL` createEntity, or a slow but `PASS` list).
2. **The live log stream** — `docker compose logs -f dgs-service` shows each
   request scroll by, then the FAILED line.

Run one at a time from the GraphiQL playground (`localhost:8080`). Query it with
the SAME selection you'd use on the matching query/mutation:

```graphql
mutation { meta { scenario1 { verdict message entity { id } } } }   # like createEntity
mutation { meta { scenario2 { verdict message count entities { id name } } } }  # like listEntities
mutation { meta { scenario3 { verdict message entity { id } } } }   # like createEntity
```

| Mutation | Returns | Teaches | What happens | Stops when | Typical result |
|----------|---------|---------|--------------|------------|----------------|
| `scenario1` | `EntityResult` | **metrics** | single writer POSTs ~256KB entities flat-out; whole-file re-marshal ramps the heap to the 256m limit | first create fails (OOM) | ~30s → `FAIL "create: service unreachable"` |
| `scenario2` | `ListResult` | **traces** | grows the store, timing a list each batch; every list re-scans the whole file | a list exceeds 80ms (healthy ~1ms) | ~80s → `PASS "listed 3250 entities"` (slow, not failed) |
| `scenario3` | `EntityResult` | **logs** | 8 concurrent writers tear the non-atomic store file | first operation returns a 5xx | ~1s → `FAIL "create: internal error reported by service"` |

The returned verdict/message is the WHETHER symptom. WHERE/WHY lives in
entity-service telemetry: scenario1 → the Go-memory metric ramp + `es-flood-oom`
alert; scenario2 → the list-latency trace/metric + `es-slow-list` alert;
scenario3 → the Loki log `failed to parse entity store file` + `es-corruption`
alert. Dashboard-free, the returned result + log stream already say "it broke,
and here's the operation that broke".

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
