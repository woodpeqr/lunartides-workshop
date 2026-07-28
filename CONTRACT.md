# Service Contract — dgs ⇄ vcs

> Canonical interface truth for the two-service workshop system. Authored by the
> orchestrator BEFORE either service is planned or implemented, so that the
> instructor-built master (**dgs**) and the student-owned subject (**vcs**) agree
> on every wire detail. **Neither planning nor implementation may deviate from
> this document without updating it here first.**

Names: **dgs** / **dgs-service** = master (GraphQL client + oracle).
**vcs** / **vcs-service** = subject (REST source-control service the student owns).
The "master"/"subject" words appear ONLY in internal design prose, never in code,
package names, identifiers, or user-facing strings.

---

## 1. Topology & runtime

```
dgs (GraphQL :8080, NO telemetry)  ──HTTP/REST──▶  vcs (REST :8081, OTel-wired)
                                                        │ OTLP gRPC :4317
                                                        ▼
                              OTel Collector → Prometheus / Loki / Tempo → Grafana :3001
```

- **vcs** lives in `lunartides-workshop/vcs/` (alongside the infra + docker-compose stack).
- **dgs** lives in `lunartides-workers/`.
- Both run as **containers** in the docker-compose stack (compose services `vcs`
  and `dgs`; dgs's build context is the sibling `../lunartides-workers` repo).
  `docker compose up --build` starts the whole system. Either service may also be
  run on the host via `go run` for a fast edit loop (override the endpoint env).
- **Go 1.26** for both modules.
- Default ports (env-overridable), published to the host: vcs `VCS_ADDR=:8081`,
  dgs `DGS_ADDR=:8080`. dgs→vcs base URL: `VCS_BASE_URL=http://vcs:8081` in
  compose, `http://localhost:8081` on the host.
- vcs OTLP target: `OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317` in compose
  (`localhost:4317` on the host), gRPC insecure, `OTEL_SERVICE_NAME=vcs`.

---

## 2. Domain model (in-memory, vcs owns all state)

- **Object** — content-addressed blob. `hash = hex(sha256(content))`. Store is a
  `map[hash]→[]byte`. Storing identical content twice **deduplicates** (same hash,
  no second copy).
- **Commit** — flat snapshot. `{ id, parent, message, files: map[path]→hash }`.
  **No tree objects.** `id = hex(sha256(canonical-serialization(commit)))`.
  `parent` is a commit id or `""` for root.
- **Ref** — named pointer: `map[name]→commitID`. A branch is just a ref.
- All state is process-local and in-memory. `wipe` clears everything.

---

## 3. vcs REST API (subject — the surface dgs calls)

All bodies JSON unless noted. All responses JSON. Content transported as UTF-8
JSON strings (see §5 on encoding — this is a deliberate bug surface).

| # | Method & path | Request body | Success | Purpose |
|---|---|---|---|---|
| R1 | `POST /objects` | `{"content": "<string>"}` | `201 {"hash":"<hex>"}` | Store blob, content-addressed + dedup |
| R2 | `GET /objects/{hash}` | — | `200 {"content":"<string>"}` | Fetch blob by hash |
| R3 | `POST /commits` | `{"files":{"<path>":"<content>"},"parent":"<id|>","message":"<str>"}` | `201 {"id":"<hex>"}` | Create flat snapshot commit |
| R4 | `GET /commits/{id}` | — | `200 {"id","parent","message","files":{path:hash}}` | Commit metadata |
| R5 | `PUT /refs/{name}` | `{"commit":"<id>"}` | `200 {"name","commit"}` | Point a ref at a commit |
| R6 | `GET /refs/{name}` | — | `200 {"name","commit":"<id>"}` | Resolve ref → commit id |
| R7 | `GET /checkout/{ref}` | — | `200 {"files":{"<path>":"<content>"}}` | Reassemble files at ref (ref = ref-name OR commit id) |
| R8 | `POST /wipe` | — | `200 {"ok":true}` | **Utility. Reset ALL state. MUST be correct.** |
| R9 | `GET /healthz` | — | `200 {"status":"ok"}` | Liveness |
| R10 *(stretch)* | `GET /diff?a={ref}&b={ref}` | — | `200 {"added":[],"removed":[],"changed":[]}` | Path-level diff |

Notes:
- `POST /commits` accepts **inline content** per path (not pre-stored hashes); the
  handler stores each blob (R1 semantics) then records `path→hash`. This is what
  makes checkout a true round-trip.
- **Round-trip oracle:** `checkout(ref_of(commit(files))) == files` must hold for
  correct code. It will NOT for some inputs — that is the workshop.
- Error shape (all 4xx/5xx): `{"error":"<message>"}`. Status codes: `400` bad
  input, `404` unknown hash/ref/commit, `500` internal.

---

## 4. dgs GraphQL API (master — client + blunt oracle, NO telemetry)

dgs is a thin GraphQL layer over the vcs REST surface. It emits **zero telemetry**.
Its assertions report **WHETHER** something is wrong (symptom level), **never WHERE
or WHY**. Three operation categories (per WORKSHOP_PLAN §2):

### Schema (SDL)

```graphql
type Query {
  # wraps vcs GETs; asserts where a golden answer exists
  getObject(hash: ID!): ObjectResult!
  resolveRef(name: String!): RefResult!
  getCommit(id: ID!): CommitResult!
  # THE round-trip oracle: commits `files`, checks checkout == files
  checkoutRoundTrip(ref: String!): CheckResult!

  # workshop tooling — ignore its internals (see Meta below)
  meta: MetaQuery!
}

type Mutation {
  # "the tests" — wrap vcs POSTs, assert result
  storeContent(content: String!): StoreResult!
  commit(files: [FileInput!]!, parent: ID, message: String!): CommitResult!
  setRef(name: String!, commit: ID!): RefResult!
  # round-trip probe: store `files` via commit, then assert checkout equality
  probeRoundTrip(files: [FileInput!]!, message: String): CheckResult!

  # workshop tooling — ignore its internals (see Meta below)
  meta: MetaMutation!
}

# ── Meta namespace (workshop plumbing, NOT the oracle) ─────────────────────
# Top level is the oracle; `meta` is instructor tooling. Students are told to
# treat it as a black box: reset the world and toggle failure scenarios.
type MetaQuery {
  scenarioStatus: ScenarioStatus!
}

type MetaMutation {
  # master-control (trusted plumbing, NOT a test) — resets stateful world
  wipe: WipeResult!
  # failure-mode toggle; one scenario active at a time (REDESIGN_PLAN §4)
  scenario(id: Int!): ScenarioResult!
}

input FileInput { path: String!, content: String! }

# every result carries a blunt verdict; details deliberately shallow
type CheckResult    { verdict: Verdict!, message: String!, ref: String }
type StoreResult    { verdict: Verdict!, message: String!, hash: ID }
type CommitResult   { verdict: Verdict!, message: String!, id: ID, files: [FileEntry!] }
type RefResult      { verdict: Verdict!, message: String!, name: String, commit: ID }
type ObjectResult   { verdict: Verdict!, message: String!, content: String }
type WipeResult     { verdict: Verdict!, message: String! }
type ScenarioResult { active: Boolean!, message: String! }
type ScenarioStatus { id: Int, active: Boolean!, since: String }
type FileEntry      { path: String!, hash: String! }

enum Verdict { PASS FAIL }
```

### Oracle behavior
- **Queries + non-wipe Mutations = blunt tests.** They call vcs, and where a
  golden answer exists they assert it, returning `PASS`/`FAIL` with a
  symptom-level message only, e.g. `checkout FAILED: expected 3 files, got 2` or
  `round-trip FAILED for path "logo.bin": content mismatch`. **Never** name a vcs
  function, line, span, or internal cause.
- **`meta { wipe }` = master-control mutation.** Trusted utility. Calls
  `POST /wipe`. Guaranteed functional. Not a test — lets students reset between
  attempts. **`meta { scenario(id) }`** toggles a failure driver (object flood /
  deep chain / ref race); it drives load against vcs and is never an oracle
  assertion. Both live under `meta` to keep the top level oracle-only.
- dgs must treat vcs failures gracefully: a vcs `4xx/5xx` or malformed body →
  `verdict: FAIL` with the symptom, never a GraphQL 500 crash. dgs itself is
  correct; only its *verdicts* reflect vcs bugs.
- GraphQL served at `POST /graphql`; GraphiQL/playground optional at `GET /`.

---

## 5. Correctness boundary — what is trusted vs buggy

| Component | Correctness | Rationale |
|---|---|---|
| dgs (all of it) | **Correct.** Trusted plumbing + honest oracle. | It is the downstream client filing tickets. |
| vcs `POST /wipe` | **Correct.** | Students must reset the world reliably. |
| vcs `GET /healthz` | **Correct.** | Liveness must be trustworthy. |
| vcs OTel plumbing (providers→collector) | **Correct + pre-wired.** | Students write *signal*, not plumbing. |
| vcs **base signal** | **Minimal — example only.** | Making it observable IS the workshop. |
| vcs store / commit / refs / checkout / diff | **Functionally correct.** | Logic is right; it lacks safeguards. |

Design shift (REDESIGN_PLAN §1) — from wrong values to missing safeguards:
- vcs is a **correct** implementation. Serial oracle mutations all PASS on a clean
  vcs. The fault is **absent limits**, not wrong logic: no request body cap, no
  object-map ceiling, O(n) ancestry walk in reassemble, unbounded checkout cache,
  no concurrency limit on checkout.
- Under load these turn into **crashes and degradation** (OOM, latency blowup, an
  intermittent concurrent-read race). Telemetry is the only way to see *that* it
  happened and *why*.
- Failure is driven on demand by `meta { scenario(id) }` (three scenarios,
  REDESIGN_PLAN §4). The full scenario catalogue lives in the **dgs plan**, not
  here. This contract only guarantees the *wire shape* is stable.

---

## 6. Interop invariants (both planners depend on these)

1. Wire shapes in §3/§4 are frozen. A bug changes *behavior/values*, never the
   *shape* of a successful response.
2. `hash` and commit `id` are lowercase hex sha256. dgs never recomputes them — it
   trusts vcs's returned ids and only checks round-trip *content* equality.
3. dgs↔vcs is HTTP/JSON over `VCS_BASE_URL`. No shared Go module, no shared types
   package — they are independent services agreeing only on JSON.
4. `checkout` accepts a ref-name first; if no such ref, it treats the path segment
   as a commit id. dgs's `checkoutRoundTrip` uses whichever it created.
5. Startup order for a working demo is handled by compose `depends_on`:
   `docker compose up --build` brings up infra → vcs → dgs in order. (Host runs
   must follow the same order manually: infra → vcs → dgs.)
