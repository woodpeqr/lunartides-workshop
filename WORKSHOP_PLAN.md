# Workshop Plan — "Observing the Garbage That Is Our Code"

> 90-minute hands-on segment for **Lunartides** (parody company). Follows the ~30-min seminar
> (logs / spans / traces / metrics / OTel concepts).
> Audience: junior devs / interns. Basic Go, zero observability experience.

---

## 1. Concept

The title puns two ways: the code is *garbage* (subtle latent bugs) and the workshop is about
**seeing** that garbage through telemetry.

The workshop simulates **real service ownership**. A downstream client (the master) depends on the
student's service (the subject). The client complains — bluntly — that something is wrong. The
student owns the misbehaving service and must use observability to find out *where* and *why*.

**Core constraint (from REQS):** observability is not decoration. It is the *only* way to complete
the workshop. This design enforces it structurally — see §4.

---

## 2. Topology — two services

```
┌──────────────────────────┐   GraphQL          ┌───────────────────────────────┐
│  MASTER                  │   (queries +       │  SUBJECT (student-owned)      │
│  - instructor-built      │    mutations       │  - plain REST                 │
│  - GraphQL API           │    wrap REST)      │  - minimal source-control     │
│  - NO telemetry          │ ─────────────────▶ │  - CORRECT logic, but         │
│  - the "downstream       │                    │    NO safeguards (crashes     │
│     client" / test runner│ ◀───────────────── │    /degrades under load)      │
│  - blunt PASS/FAIL       │   real REST resp.  │  - telemetry harness wired,   │
│  - meta: scenario toggle │                    │    base signal minimal        │
└──────────────────────────┘                    └──────────────┬────────────────┘
                                                                │ OTLP
                                              OTel Collector → Prometheus / Loki / Tempo
                                                                │
                                                             Grafana
                                              (student builds: dashboards + ALERT rules)
```

### Master (instructor-built, GraphQL, no telemetry)

A thin GraphQL layer over the subject's REST API. Two purposes: exercise the service, and give a
blunt verdict. Three operation categories:

| GraphQL category | Wraps | Behavior | Correct in subject? |
|---|---|---|---|
| **Query** | subject **GET** endpoints (checkout, resolve ref, …) | Calls the endpoint, **asserts** the result where applicable. | No — hits buggy VCS logic. |
| **Mutation** | subject **POST** endpoints (commit, store, …) | Calls the endpoint with inputs, **asserts** the result. These are "the tests." | No — hits buggy VCS logic. |
| **Meta namespace** | subject **utility** endpoints + load drivers | `meta { wipe }` resets the world; `meta { scenario(id) }` toggles a failure mode. | **Yes — trusted plumbing, not an oracle.** |

- Queries and mutations are the **blunt oracle**: they report *whether* something is wrong at
  symptom level (e.g. `checkout of "race/head": expected 3 files, got 2`), **never** *where* or *why*.
- The **`meta` namespace** is workshop tooling, not a test. `meta { wipe }` is a trusted reset so
  students can start clean between attempts. `meta { scenario(id) }` is the instructor's lever: it
  drives vcs into one of three failure modes on demand (object flood, deep chain, ref race). Students
  are told to treat `meta` as a black box.

### Subject (student-owned, plain REST)

A deliberately scope-cut source-control service. The VCS logic is **functionally correct** — serial
oracle calls all PASS on a clean vcs. What it lacks is **safeguards**: no request body cap, no
object-map ceiling, an O(n) ancestry walk on checkout, an unbounded cache, no concurrency limit.
Under the load a scenario applies, the service **crashes or degrades** (OOM, latency blowup, an
intermittent race). The OTel harness is wired (providers → collector), but the **base build emits
only minimal signal**. Making the service observable is the workshop.

---

## 3. Domain — a minimal source-control service

Source control is chosen because it is **fragile by nature** (encodings, checksums, content
addressing, stateful stacked operations) and dense with **conservation laws and invariants** — the
raw material for telemetry-only bugs.

Built as **vertical slices** — one endpoint at a time, iteratively.

Core surface:
- **store content by hash** (content-addressed, deduplicated)
- **commit** — flat snapshot (`file → content` map) + parent + message. *No tree objects.*
- **refs** — a branch is a pointer to a commit (where a stacked/stateful race lives)
- **checkout** — reassemble the files at a ref. This is the **round-trip oracle**:
  `checkout(commit(x)) == x`. The unbounded ancestry walk + cache here is Scenario 2's target.
- **wipe** (utility, via `meta { wipe }`) — reset all state. **Correctly implemented.**

Stateful: later operations build on earlier ones, which is why master-control utilities exist.
State is **in-memory** (some bugs then surface in the process heap graph).

**Stretch:** a read-only `diff(a, b)`.
**Explicitly cut** (scope guardrails): trees/directories, merge/conflict resolution, packfiles/delta
compression, the real git wire protocol.

---

## 4. Why telemetry is mandatory (and not just a unit test)

A unit test answers *whether* one call is correct. If that were the whole game, Grafana would be
decoration. It is not, because of a strict separation of "who answers what":

- **WHETHER** — comes from the **master** (blunt, symptom-level) and from self-evident contradictions.
- **WHERE / WHY** — comes only from telemetry the student builds.

Three sources of correctness, used in distinct roles:

| Source | Provided by | Answers |
|---|---|---|
| **S-01 — external oracle** | master query/mutation assert | WHETHER (blunt) |
| **S-02 — self-evident contradiction** | student-instrumented signals that must agree but don't (e.g. `writes ≠ stored + deduped`) | WHERE, provable without a golden answer |
| **S-03 — invariant violation** | student judgment against a known-healthy truth (latency budget, error rate ~0, counter monotonicity) | WHERE |

The honest loop: **master complains bluntly → student has no idea why from that alone → opens their
telemetry → localizes the bug → builds an alert so the signal fires *before* the master would ever
complain.** The master is the ultimate double-check, exactly like a downstream team filing a ticket.

---

## 5. All three signals are genuinely required

The three **scenarios** (toggled via `meta { scenario(id) }`) are built so **each signal is the hero
and the others are weak**. vcs is correct code with a missing safeguard; each scenario applies the
load that turns one missing safeguard into an observable failure. Full mechanics live in
`REDESIGN_PLAN §4`.

| # | Scenario | Missing safeguard | Hero signal | Why it wins / others fail |
|---|---|---|---|---|
| **1** | **Object flood** | no body cap, no object-map ceiling | **Metrics** | Failure is a *shape over time* — a heap/`objectCount` gauge ramps for minutes and is **alertable before OOM**. No single POST is anomalous (trace blind); per-object logs are noise. |
| **2** | **Deep chain checkout** | O(n) ancestry walk, unbounded cache | **Traces** | Latency is a *per-request* property; child spans on `resolve → reassemble → ancestry walk` localize the hot path with `chain_depth`. Metrics only confirm the symptom; per-step logs are impractical noise. |
| **3** | **Ref race** | no concurrency limit on checkout | **All three (pivot)** | Metric flags the intermittent 500 rate → trace narrows to the failing checkout (`files_expected` vs `files_got`) → structured log at the lock gap names the in-flight hash. No single signal suffices; the *handoff* is the skill. |

Design principle behind the set:
- **Correct-but-unsafeguarded, not wrong-value.** Serial oracle calls PASS; only load reveals the
  fault. This is the purest "a unit test can't catch this" class.
- **One scenario active at a time.** The toggle stops the previous scenario before starting the next
  (REDESIGN_PLAN §4.1), so the signal under study is never muddied by another load source.

---

## 6. Student deliverables

By the end, each participant should be able to:
- **Instrument** the hot paths of the subject — add spans, metrics, and structured logs with
  trace-context correlation, using the pre-wired harness (they write signal, not plumbing).
- **Build dashboards** in Grafana across metrics, traces, and logs.
- **Set up alerting** — define what "unhealthy" means and get told when it happens.
- **Locate** bugs using telemetry, and articulate which signal revealed each and why.
- **Fixing bugs is a stretch goal**, not required. The primary skill is *seeing*.

**Success criterion:** a participant's **own alert fires from their own telemetry**, and it
correlates with a master query/mutation failure. They can say, unprompted: *"the code looked correct,
but the signal said otherwise"* — and show the graph or trace that proved it.

---

## 7. Infrastructure

The observability stack is already built and works:
- OTel Collector (gRPC :4317, HTTP :4318) → Prometheus / Loki / Tempo → Grafana (:3001).
- `docker compose up` starts everything; Grafana datasources pre-provisioned.
- **Go 1.26** is the target (README updated to match).
