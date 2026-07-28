# Redesign Plan — Lunartides Workshop

> Captures every decision made in the design session. Authoritative over
> WORKSHOP_PLAN.md and CONTRACT.md where they conflict.

---

## 1. Core design shift

**Before:** `vcs` carried subtle wrong-value bugs. Students used telemetry to
find bad values.

**After:** `vcs` is functionally correct. It lacks safeguards. Under load it
crashes or degrades. Telemetry is the only way to know it happened and why.

Two workshop phases:

| Phase | Question | Tool |
|---|---|---|
| 1 | Is the service alive? | Grafana health alert, container monitoring |
| 2 | What is it doing internally? | Metrics, traces, logs — built by students |

---

## 2. VCS service changes

### 2.1 Fix all logic bugs

These are value-logic errors. Fix them so `vcs` is a correct implementation:

| Bug | Location | Fix |
|---|---|---|
| Diff logic inverted | `handlers.go:219` | Iterating `filesB` means `stillInB` is always true — dead branch. A path present in both refs with different content is always `changed`. Replace entire inner `if` with `changed = append(changed, path)`. |
| Silent blob skip | `store.go:265` | `continue` silently drops a path when its object is missing. Return a descriptive error so callers know the commit is partially unresolvable. |
| Wipe cache leak | `store.go:reset` | `checkoutCache` not cleared on `Wipe`. Add `s.checkoutCache = make(map[string]map[string]string)` inside `reset()`. |
| objectCount race | `store.go:121–126` | Increment inside `RLock`; multiple goroutines pass the `exists=false` check simultaneously. Move increment inside `Lock` with double-check pattern. |

### 2.2 Keep — missing safeguards (intentional)

These are not bugs. They are absent limits that make `vcs` crashable under load:

| Missing safeguard | Effect | Triggered by |
|---|---|---|
| No request body size limit on `POST /objects` | Unbounded heap allocation per request | Scenario 1 |
| No max object count on `objects` map | Heap grows until OOM kill | Scenario 1 |
| O(n) commit ancestry walk in `reassemble` | Latency grows linearly with chain depth | Scenario 2 |
| `checkoutCache` never evicted | Cache grows unbounded over time | Scenario 2 (secondary) |
| No concurrency limit on `Checkout` | Goroutine pile-up under concurrent load | Scenario 3 |

### 2.3 Docker memory limit

Add `mem_limit: 256m` to the `vcs` service in `docker-compose.yml`. This makes
OOM timing deterministic regardless of host machine specs. Scenario 1 reliably
kills the container in a predictable timeframe.

### 2.4 OTel signal — stay minimal

Base build emits only:

- One top-level server span per incoming request (method, route, status code).
  No child spans.
- One counter `vcs_requests_total` with label `route`.

Both exist to show students the OTel API pattern, not to give useful signal.
Everything else — latency histograms, memory gauges, structured logs, child spans
on store/checkout internals — is what students build.

---

## 3. Workers/DGS schema restructure

### 3.1 Grouping

Oracle ops (the "tests") stay top-level. Workshop plumbing moves under `meta`.
Students are told: "top level is the oracle, `meta` is workshop tooling, ignore
its internals."

### 3.2 New schema

```graphql
# ── Oracle queries (student-facing) ──────────────────────────────────────
type Query {
  getObject(hash: ID!): ObjectResult!
  resolveRef(name: String!): RefResult!
  getCommit(id: ID!): CommitResult!
  checkoutRoundTrip(ref: String!): CheckResult!

  meta: MetaQuery!
}

# ── Oracle mutations (student-facing) ────────────────────────────────────
type Mutation {
  storeContent(content: String!): StoreResult!
  commit(files: [FileInput!]!, parent: ID, message: String!): CommitResult!
  setRef(name: String!, commit: ID!): RefResult!
  probeRoundTrip(files: [FileInput!]!, message: String): CheckResult!

  meta: MetaMutation!
}

# ── Meta ─────────────────────────────────────────────────────────────────
type MetaQuery {
  scenarioStatus: ScenarioStatus!
}

type MetaMutation {
  wipe: WipeResult!
  scenario(id: Int!): ScenarioResult!
}

type ScenarioResult {
  active:  Boolean!
  message: String!
}

type ScenarioStatus {
  id:     Int       # null = none active
  active: Boolean!
  since:  String    # RFC3339, null when inactive
}
```

All existing result types (`ObjectResult`, `StoreResult`, `CommitResult`,
`RefResult`, `CheckResult`, `WipeResult`, `FileEntry`, `FileInput`, `Verdict`)
unchanged.

`wipe` moves from top-level mutation to `meta { wipe }`. Resolver logic
unchanged. All docs updated (see §6).

### 3.3 Implementation notes

`meta` fields resolve to a thin namespace struct; sub-fields carry their own
resolvers. Standard graphql-go pattern, no new dependencies.

---

## 4. Scenario system

### 4.1 Toggle contract

`meta { scenario(id: N) }` is a toggle. One scenario active at a time.

| State | Action | Returns |
|---|---|---|
| None active | `scenario(N)` | `active: true`, `"Scenario N activated"` |
| N active | `scenario(N)` | `active: false`, `"Scenario N deactivated"` |
| M active, M≠N | `scenario(N)` | `active: true`, `"Scenario M stopped → Scenario N activated"` |

### 4.2 ScenarioManager

Lives in `workers/dgs`, not in `vcs`:

```go
type ScenarioManager struct {
    mu     sync.Mutex
    active int
    cancel context.CancelFunc
    wg     sync.WaitGroup
    since  time.Time
}
```

Toggle logic:
1. Acquire lock.
2. If a scenario is running: call `cancel()`, `wg.Wait()` (blocks until
   goroutine exits cleanly).
3. If activating: create `context.WithCancel`, `wg.Add(1)`, launch goroutine,
   record `since`.
4. Mutation returns immediately; background loop runs until next toggle.

Scenarios drive `vcs` via the existing `vcsclient.Client`.

### 4.3 Three scenarios

---

#### Scenario 1 — Object flood (teaches: Metrics)

**Mechanism:**
10 goroutines loop continuously. Each iteration generates a unique ~1 MB body
(random bytes, unique per iteration to defeat dedup) and calls `POST /objects`.
No pause. `vcs` has no body size cap and no map size limit. Heap grows
monotonically. Container OOM-kills within the Docker `mem_limit: 256m` ceiling.

**What students observe without telemetry:**
Oracle returns `"service unreachable"`. Container is dead. No idea why or when.

**What telemetry reveals:**
- Memory gauge (`runtime.MemStats.HeapAlloc`): ramp is visible minutes before
  OOM. Alertable — fires *before* the crash, not after.
- `objectCount` gauge: monotonically increasing, no ceiling, confirms unbounded
  growth.
- Logs: per-object entries are noise at this rate. Irrelevant.
- Traces: each individual POST is fast. Spans look healthy. Irrelevant.

**Why metrics wins:** The failure is a shape over time. No single event is
anomalous. Only aggregation over time (a gauge ramp) reveals the problem and
allows an alert before impact.

---

#### Scenario 2 — Deep chain checkout (teaches: Traces)

**Mechanism:**

Phase A (once, at activation): build a linear commit chain of depth 2000.
`c0 → c1 → c2 → ... → c2000`. Each commit has 5 small files. Each commit's id
is unique (different content each level). Do NOT checkout any commit during
setup — leaves cache cold.

Phase B (continuous loop): 5 goroutines each extend the chain by one commit per
iteration and immediately call `GET /checkout/{new_tip_id}`. Every checkout is a
cache miss (new unique id). `reassemble` walks the full ancestry on every call:
O(n) map lookups where n = current chain depth, growing each iteration.

**What students observe without telemetry:**
`checkoutRoundTrip` begins returning `"service unreachable"` or timing out.
Request latency climbs. No error in logs. No obvious crash.

**What telemetry reveals:**
- Latency histogram on `/checkout`: p99 climbs visibly from <1ms to seconds.
  Shows *that* checkout is slow. Can't show *why*.
- Trace with child spans on `resolve`, `reassemble`, ancestry walk: the
  ancestry-walk span dominates. Span attribute `chain_depth=2000` confirms the
  cause. Exactly localizes the hot path.
- Logs: logging every ancestry step is thousands of events per request. Noise,
  not signal.

**Why traces wins:** Latency is a per-request property. The question is *which
part* of the request is slow, not *how often* or *what happened*. Child spans
answer that directly. Metrics can only confirm the symptom; logs would require
impractical per-step noise.

---

#### Scenario 3 — Ref race (teaches: All three — the pivot chain)

**Mechanism:**

Two concurrent goroutine pools:

- **Writer pool** (5 goroutines): tight loop of `CreateCommit` (random files,
  unique content) → `SetRef("race/head", newID)`.
- **Reader pool** (5 goroutines): tight loop of `GetRef("race/head")` →
  `Checkout(resolvedID)` → assert file count matches the expected commit's
  file count.

Under this concurrent load the RLock→Lock gap in `PutObject` (§2.2 kept) creates
a window: a goroutine reads `exists=false` under `RLock`, releases it, another
goroutine writes the object under `Lock`, the first goroutine acquires `Lock` and
writes the same object again (redundant write, correct result). More critically:
`reassemble` may run in the gap between `PutObject`'s `RLock` check and its
`Lock` write for a different hash in a concurrent commit, reading a partially
populated `objects` map. Result: checkout occasionally returns fewer files than
committed — intermittent, non-deterministic, passes all serial tests.

**What students observe without telemetry:**
Oracle's `checkoutRoundTrip("race/head")` fails intermittently with
`"expected N files, got M"`. Passes most of the time. No pattern visible.

**The pivot chain:**

1. **Metric fires first:** `vcs_requests_total{route="/checkout", status="500"}`
   has non-zero rate. Also `objectCount` gauge drifts above actual `len(objects)`
   — the counter is overcounting under concurrent writes (§2.1 fixed the logic
   bug, but the missing safeguard is no concurrency limit, so the map is still
   under pressure). *"Something is wrong sometimes. How often?"*

2. **Trace narrows scope:** Filter to erroring checkout traces. Span tree shows
   `reassemble` returns fast but with fewer files. Span carries `commit_id` and
   `files_expected` vs `files_got` as attributes. *"Which commits fail? Is there
   a pattern in depth or file count?"*

3. **Log confirms the interleaving:** Structured log at the `PutObject` lock gap:
   `{"event":"concurrent_write","hash":"...","goroutine_id":"...","ts":"..."}`.
   Correlate log timestamp with the failing trace's span timestamp. *"Here is
   the exact hash that was in-flight when reassemble ran."*

No single signal is sufficient. Metric reveals frequency, trace reveals location,
log reveals the specific event. The handoff between them is the skill.

---

## 5. Grafana

### 5.1 Starting state: empty

Remove all provisioned dashboards. Starting state is Grafana with no dashboards,
no panels, no alert rules.

Datasources (Prometheus, Loki, Tempo) stay provisioned — students should not
fight datasource wiring.

**File change:** `grafana/provisioning/dashboards/dashboards.yaml` — remove or
point provider at an empty directory.

### 5.2 Persistence via volume

Add a named volume for Grafana's data directory so dashboards and alerts built
during the workshop survive container restarts:

```yaml
# docker-compose.yml
services:
  grafana:
    volumes:
      - grafana_data:/var/lib/grafana

volumes:
  grafana_data:
```

Phase 1 exercise: students create one stat panel ("vcs up/down") and one alert
rule on it. This is the first thing they build and the foundation for everything
after.

---

## 6. Doc updates required

These files reference `wipe` at top-level or describe the old wrong-value bug
design. Update all of them after implementation:

| File | What changes |
|---|---|
| `WORKSHOP_PLAN.md` | Remove bug-class table (§5). Update topology diagram. Update student deliverables. |
| `CONTRACT.md` | Move `wipe` to `meta { wipe }` in SDL. Add `meta` namespace to schema section. |
| `workers/PLAN.md` | Remove Scenario 2 (stale cache). Update scenario list to 3. Update schema SDL to match new structure. |

---

## 7. Implementation order

Each step must leave `go build ./...` and `go test ./...` green.

1. Fix vcs logic bugs (§2.1)
2. Add `mem_limit: 256m` to vcs in `docker-compose.yml` (§2.3)
3. Gut Grafana dashboard provisioning, add `grafana_data` volume (§5)
4. Restructure dgs schema — add `meta` namespace, move `wipe` (§3.2)
5. Implement `ScenarioManager` — toggle logic, goroutine lifecycle (§4.2)
6. Implement Scenario 2 — deep chain (clearest, no race dependency)
7. Implement Scenario 1 — object flood
8. Implement Scenario 3 — ref race, all three signals
9. Update docs (§6)

---

## 8. Workshop arc

```
0:00  Presentation — OTel concepts, logs/metrics/traces/when-to-use  [30 min]

0:30  Workshop starts
      ├─ docker compose up, GraphiQL open
      ├─ Students run oracle mutations — all PASS on clean vcs
      └─ "The service works. Now break it."

0:40  Phase 1 — Is it alive?
      ├─ Instructor: mutation { meta { scenario(id: 1) { active message } } }
      ├─ Students watch oracle mutations return "service unreachable"
      ├─ Exercise: build Grafana stat panel + alert rule for vcs liveness
      └─ Debrief: "you know WHEN it died — not WHY"

1:10  Phase 2 — What is it doing?
      ├─ Scenario 2 (traces): latency climbing — instrument and find where
      ├─ Scenario 3 (all three): intermittent failures — metric → trace → log
      └─ Each: instrument vcs → trigger scenario → observe → locate cause

2:10  Buffer / alerts / discussion
      ├─ "Your alert should fire before the oracle ever complains"
      └─ Stretch: can you instrument vcs to predict Scenario 1 before OOM?

2:30  End (hard stop)
```
