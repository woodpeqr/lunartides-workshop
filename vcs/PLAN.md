# vcs — Implementation PLAN (subject service)

> Implementation plan for filling the existing `vcs/` scaffold with its real —
> and deliberately buggy — source-control logic. This is a PLAN, not code.
>
> Authority order (frozen): `../CONTRACT.md` → `../WORKSHOP_PLAN.md` → `../REQS.md`.
> Wire shapes (CONTRACT §3) are **frozen**: a bug alters *values/behavior*, never
> the *shape* of a successful response. In-memory only, no auth, no persistence,
> Go 1.26. Telemetry PLUMBING is already wired and trusted (`internal/telemetry`);
> this plan never rebuilds it — it only specifies the *signal* seams and the bugs
> that signal must reveal.

---

## 0. Design thesis

Every bug obeys one rule (CONTRACT §5, WORKSHOP §5): **subtle in code, loud in
exactly one signal.** The service ships correct-looking code and *minimal* base
telemetry. The master (dgs) complains bluntly (`checkout FAILED: expected 3
files, got 2`) but never says where or why. The student must add signal to a hot
path and let that signal localize the fault.

The catalogue is biased toward:
- **S-02 contradiction** — two signals that must agree but don't (writes vs
  stored+deduped; files committed vs files checked out; counter vs map length).
- **S-03 invariant** — leak / latency budget / error-rate≈0 / monotonicity.
- **One race** — correct single-threaded, wrong under load; needs the
  metric→trace→log handoff.

We deliberately avoid pure-logic "returns 42 should be 43" bugs as the spine —
those are master-only-visible and reduce telemetry to decoration.

---

## 1. Architecture fill-in (per scaffold file)

### 1.1 `internal/store/store.go` — the buggy core

Existing shapes stay: `Store{ mu sync.RWMutex; objects map[string][]byte;
commits map[string]Commit; refs map[string]string }`, `Commit{ID,Parent,Message,Files}`,
`New`, `reset`, `Wipe` (trusted). Fill the stub methods.

**Hashing (trusted, shared helper).**
```
func hashContent(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
```
Lowercase hex sha256 (CONTRACT §2, §6.2). Used for both object hashes and, over
the canonical commit serialization, for commit ids.

**Canonical commit serialization (trusted, deterministic).** Commit id must be
stable regardless of Go map iteration order:
```
func canonical(c Commit) []byte
  // "parent\x00" + parent
  // "\nmessage\x00" + message
  // for each path in SORTED order: "\nfile\x00" + path + "\x00" + hash
  // id = hashContent(canonical(c))   // computed with c.ID == "" during hashing
```
Sort paths with `sort.Strings` before serializing. `parent==""` for root.

**Internal fields added (beyond the scaffold three) — the leak/race surfaces:**
- `objectCount int` — a denormalized object counter (BUG-04 race surface).
- `checkoutCache map[string]map[string]string` — reassembled-file cache keyed by
  resolved commit id (BUG-01 leak surface). NOT cleared anywhere.

**Method behaviors (buggy specifics live in §2; here is the intended shape):**
- `PutObject(content []byte) (hash string, err error)` — hash content, dedup on
  map key = hash, return hash. (Bugs: BUG-02 normalize, BUG-03 empty-skip,
  BUG-04 counter.)
- `GetObject(hash string) ([]byte, error)` — map lookup; `ErrNotFound` if absent.
- `PutCommit(files map[string]string, parent, message string) (id, err)` — for
  each path, `PutObject([]byte(content))` → record `path→hash`; build `Commit`,
  compute id via `canonical`, store in `commits`. (Feeds BUG-02/03 through the
  object path.)
- `GetCommit(id) (Commit, error)` — map lookup; `ErrNotFound` if absent.
- `SetRef(name, commitID) error` — `refs[name]=commitID` under `Lock`.
- `GetRef(name) (string, error)` — map lookup; `ErrNotFound` if absent.
- `Checkout(ref) (map[string]string, error)` — resolve `ref` as ref-name first
  (`refs[ref]`), else treat as commit id (CONTRACT §6.4); load the commit; for
  each `path→hash`, `GetObject(hash)` → `files[path]=string(content)`. (Bugs:
  BUG-01 cache, BUG-03 swallow, BUG-05 parent-walk.)
- `Wipe()` — **trusted, unchanged**: `reset()` under `Lock`. See BUG-01 note on
  the wire-vs-heap boundary — `reset()` clears the three observable maps; the
  cache leak is a *heap retention*, not a wire-state violation.

> Concurrency contract for the buggy methods: writers take `mu.Lock()`; readers
> take `mu.RLock()`. BUG-04 deliberately performs a read-modify-write of
> `objectCount` inside the `RLock` section — that is the planted race.

### 1.2 `internal/handlers/handlers.go` — R1–R10 wire behavior

Replace each `notImplemented(w)` with real decode → store-call → encode. Wire
shapes are frozen (CONTRACT §3). Keep `writeJSON` / `writeError` helpers. Error
mapping: `store.ErrNotFound → 404`, JSON decode error / missing field → `400`,
anything else → `500`, body `{"error":"<msg>"}`.

| R | Handler | Decode | Store call | Success |
|---|---|---|---|---|
| R1 | `CreateObject` | `{"content":string}` | `PutObject([]byte(content))` | `201 {"hash":hex}` |
| R2 | `GetObject` | path `{hash}` | `GetObject(hash)` | `200 {"content":string}` |
| R3 | `CreateCommit` | `{"files":{path:content},"parent":str,"message":str}` | `PutCommit(...)` | `201 {"id":hex}` |
| R4 | `GetCommit` | path `{id}` | `GetCommit(id)` | `200 {id,parent,message,files:{path:hash}}` |
| R5 | `SetRef` | path `{name}` + `{"commit":id}` | `SetRef(name,commit)` | `200 {"name","commit"}` |
| R6 | `GetRef` | path `{name}` | `GetRef(name)` | `200 {"name","commit":id}` |
| R7 | `Checkout` | path `{ref}` | `Checkout(ref)` | `200 {"files":{path:content}}` |
| R8 | `Wipe` | — | `Wipe()` | `200 {"ok":true}` (**trusted, keep as-is**) |
| R9 | `Healthz` | — | — | `200 {"status":"ok"}` (**trusted, keep as-is**) |
| R10 | `Diff` | `?a=&b=` | `Checkout(a)`, `Checkout(b)` + compare | `200 {"added","removed","changed"}` |

`CreateCommit` accepts **inline content per path** (CONTRACT §3 note): the
handler/store stores each blob (R1 semantics) then records `path→hash`. This is
what makes checkout a genuine round-trip and is the carrier for BUG-02/03.

### 1.3 `cmd/vcs/main.go` — add the request middleware seam

Unchanged except: wrap `mux` in ONE middleware that emits the **base signal**
(§3): top-level span + request counter. Everything else stays (config, telemetry
init, graceful shutdown). This is the only main.go change.

### 1.4 `internal/telemetry/telemetry.go` — DO NOT EDIT

Pre-wired, trusted (CONTRACT §5). Students obtain instruments via the installed
globals: `otel.Tracer("vcs")`, `otel.Meter("vcs")`, `global.Logger("vcs")`.

---

## 2. THE BUG CATALOGUE (the heart of the plan)

Six bugs. BUG-01..04 are the required spine (one per star signal + one race).
BUG-05 adds an S-03 latency invariant; BUG-06 rides R10 (stretch). Metrics reach
Prometheus under the collector namespace `lunartides` (e.g.
`lunartides_vcs_checkout_cache_entries`).

---

### BUG-01 — Checkout cache never evicted (STAR: METRICS · S-03 leak, + S-02)

- **(a) Location:** `store.go` → `Checkout`, plus new field `checkoutCache
  map[string]map[string]string`.
- **(b) Subtle vs correct:**
  ```go
  // BUGGY: memoize every checkout keyed by resolved commit id; never evicted,
  // never bounded, never touched by reset()/Wipe.
  func (s *Store) Checkout(ref string) (map[string]string, error) {
      cid := s.resolve(ref)
      s.mu.RLock()
      if hit, ok := s.checkoutCache[cid]; ok { s.mu.RUnlock(); return hit, nil }
      s.mu.RUnlock()
      files := s.reassemble(cid)          // real work
      s.mu.Lock(); s.checkoutCache[cid] = files; s.mu.Unlock()   // <-- unbounded
      return files, nil
  }
  // CORRECT: reassemble each time (flat snapshots are cheap), OR a bounded/
  // wipe-cleared cache. The immutable-key cache is not *wrong per request* — it
  // just leaks. Every distinct commit ever checked out is retained forever.
  ```
  Because the key is the immutable commit id, results are never *stale* — the
  cache looks like a smart, correct optimization. It simply never releases.
- **(c) Star signal + student telemetry:** METRICS. Reads should be memory-flat
  under steady load; here heap ramps monotonically. Student adds an
  **observable gauge** `vcs.checkout.cache.entries` reading `len(checkoutCache)`
  (or a process/heap-alloc gauge). Under dgs read traffic the gauge climbs and
  never returns to baseline. Alert: gauge slope > 0 over a read-only window.
- **(d) Master symptom (blunt):** dgs sees nothing wrong per-request initially
  (results correct) → eventually memory pressure / OOM → `checkoutRoundTrip
  FAILED: vcs unreachable`. Blunt, cause-free.
- **(e) Source:** S-03 (invariant: memory stable under read-only load / return
  to baseline). Secondary S-02 (cache entries vs distinct commits diverge from
  any reasonable working set).
- **(f) Repro:** `wipe`, then `for i in 1..5000: commit {f:"v$i"}; checkout <id>`
  with distinct content each iteration → `cache.entries` climbs to 5000 and
  holds. A single checkout looks perfect (trace-blind); the *shape over time*
  is the tell. Note: `wipe` still returns 200 and GETs still 404 correctly —
  the leak is heap retention, so wipe's **wire** contract is honored while the
  **heap** graph exposes the bug (WORKSHOP §3 "surfaces in the process heap
  graph").

---

### BUG-02 — Lossy UTF-8 normalization before hashing (STAR: TRACES · S-02 round-trip)

- **(a) Location:** `store.go` → `PutObject`, first line (on the write path,
  reached by both R1 and R3-via-`PutCommit`).
- **(b) Subtle vs correct:**
  ```go
  // BUGGY: "sanitize" content before hashing/storing.
  func (s *Store) PutObject(content []byte) (string, error) {
      content = []byte(strings.ToValidUTF8(string(content), "")) // drops invalid bytes
      h := hashContent(content)
      ... store content under h ...
      return h, nil
  }
  // CORRECT: hash and store the exact bytes received; never normalize.
  ```
  For valid UTF-8 / ASCII (dgs's default probes, and 95% of inputs) this is a
  no-op → looks correct. For content containing invalid UTF-8 byte sequences
  (or, as an alternate class, NFC/NFD-distinct unicode if `norm.NFC` is used
  instead) the stored bytes differ from the input. The returned hash is the hash
  of the *normalized* content, so R2/checkout faithfully return the normalized
  (corrupted) bytes: `checkout(commit(x)) != x`.
- **(c) Star signal + student telemetry:** TRACES. Student adds child spans on
  the write path: `store.put_object` → `normalize` → `hash` → `store`, with
  attributes `content.len.in`, `content.len.out`, `content.hash`. The trace
  shows `content.len.out < content.len.in` at the `normalize` span for exactly
  the failing input class → localizes step + input class in one request.
- **(d) Master symptom:** `probeRoundTrip FAILED for path "logo.bin": content
  mismatch` (dgs stores files then asserts `checkout == files`). Blunt; names no
  vcs function.
- **(e) Source:** S-02 (round-trip contradiction: input vs checkout output) with
  S-01 backstop. Metrics only say *how many* failed; logs help only if you
  already logged there — the span tree is the hero.
- **(f) Repro:** `commit {"logo.bin": "<string with bytes 0xff 0xfe>"}` (or a
  raw non-UTF8 payload), then `checkout` → returned content shorter than sent.
  Pure-ASCII commits round-trip perfectly, hiding it from casual tests.

---

### BUG-03 — Swallowed "empty blob" error drops files silently (STAR: LOGS · S-02)

- **(a) Location:** two conspiring micro-decisions — `PutObject` (empty-skip)
  and `Checkout`/`reassemble` (swallow).
- **(b) Subtle vs correct:**
  ```go
  // PutObject, BUGGY micro-opt: "don't bother storing empty content"
  if len(content) == 0 { return hashContent(content), nil } // returns hash, stores NOTHING

  // Checkout/reassemble, BUGGY: swallow the resulting miss and skip the path
  content, err := s.getObjectLocked(hash)
  if err != nil { continue }           // <-- caught-and-ignored; 200 OK, fewer files
  files[path] = string(content)
  // CORRECT: store empties too; and a missing blob is a 500/ErrNotFound, never a
  // silent skip.
  ```
  Commit returns `201` with a valid id; `GetCommit` shows all paths (the
  `path→hash` map is intact). Only *checkout* silently omits any path whose
  content was empty, returning `200` with fewer files and no error.
- **(c) Star signal + student telemetry:** LOGS. Metric shows nothing (no error
  surfaced, 200 OK); the checkout span looks clean (no error status). Student
  adds a structured WARN log at the swallow site with high-cardinality detail:
  `msg="blob missing, skipping path" path=<path> hash=<hash>
  commit=<id> trace_id=<...>`. That log line is the only place the dropped path
  + its hash appear.
- **(d) Master symptom:** `checkout FAILED: expected 3 files, got 2`. Blunt count
  mismatch, no path, no reason.
- **(e) Source:** S-02 (files committed ≠ files checked out) with S-01 backstop.
- **(f) Repro:** `commit {"README":"hi", "EMPTY":""}` then `checkout` → response
  `{"files":{"README":"hi"}}` (EMPTY silently gone). The empty-file edge is the
  trigger; any test using only non-empty files never sees it.

---

### BUG-04 — Denormalized object counter incremented under RLock (STAR: PIVOT metric→trace→log · S-02+S-03 · RACE)

- **(a) Location:** `store.go` → `PutObject`, the `objectCount` bookkeeping.
- **(b) Subtle vs correct:**
  ```go
  // BUGGY: existence check + counter bump under RLock (concurrent readers
  // allowed), then a separate Lock for the map store.
  func (s *Store) PutObject(content []byte) (string, error) {
      h := hashContent(content)
      s.mu.RLock()
      _, exists := s.objects[h]
      if !exists { s.objectCount++ }      // <-- read-modify-write under RLock: LOST UPDATE
      s.mu.RUnlock()
      if !exists { s.mu.Lock(); s.objects[h] = content; s.mu.Unlock() }
      return h, nil
  }
  // CORRECT: do the check, the map write, AND the counter under a single Lock,
  // or drop the denormalized counter and read len(s.objects).
  ```
  `RLock` permits concurrent goroutines, so `objectCount++` races → lost
  increments. Single-threaded it is always correct (deceives unit tests). The
  `objects` map itself is written under `Lock`, so `len(objects)` stays right —
  only the denormalized counter drifts low.
- **(c) Star signal + student telemetry (the handoff — this is the point):**
  - METRIC flags the anomaly: student exposes `vcs.objects.count` (from
    `objectCount`) and `vcs.objects.map_len` (from `len(objects)`). They diverge
    under load (S-02 contradiction) and `objects.count` is non-monotonic vs a
    correct `vcs.writes.total` counter (S-03 monotonicity).
  - TRACE shows the request path: overlapping `store.put_object` spans in the
    same window prove concurrency (not a serial miscount).
  - LOG reveals the interleaving: DEBUG log `count.before`/`count.after` with
    `trace_id` + goroutine identity shows two spans reading the same
    `count.before` — the lost update, caught red-handed.
- **(d) Master symptom:** none directly (map is correct!) — surfaces only when
  the student's own contradiction alert fires. This is the deepest bug: the
  master may never complain, matching WORKSHOP §6 success criterion ("own alert
  fires from own telemetry").
- **(e) Source:** S-02 (counter ≠ map length) + S-03 (monotonicity). Pure race.
- **(f) Repro:** `wipe`, then fire N=1000 concurrent `POST /objects` with
  distinct content. `map_len==1000` but `objects.count < 1000` (varies per run).
  Deterministic only under concurrency — the "unit test can't catch this" class.

---

### BUG-05 — Checkout walks full parent ancestry unnecessarily (STAR: METRICS/TRACES · S-03 latency)

- **(a) Location:** `store.go` → `Checkout`/`reassemble`.
- **(b) Subtle vs correct:**
  ```go
  // BUGGY: "resolve full history" — walk parent chain to root, re-reading every
  // ancestor commit + its blobs, before returning the TARGET commit's files.
  for cid := targetID; cid != ""; cid = s.commits[cid].Parent {
      c := s.commits[cid]
      for _, hash := range c.Files { _, _ = s.getObjectLocked(hash) } // wasted reads
  }
  files := assemble(s.commits[targetID])
  // CORRECT: flat snapshots — checkout needs ONLY the target commit's files. No
  // ancestry walk. O(files) not O(depth × files).
  ```
  Correct results, but latency is O(history depth). Cheap early, degrades as the
  commit chain grows.
- **(c) Star signal + student telemetry:** METRICS + TRACES. Student adds a
  latency histogram `vcs.checkout.duration` and child spans per ancestor read.
  p95 climbs with chain depth (S-03 latency budget breach, alertable); the trace
  shows a deep stack of `fetch_parent` spans that shouldn't exist for a flat
  checkout.
- **(d) Master symptom:** `checkoutRoundTrip: slow / timeout` on deep chains;
  otherwise `PASS` (results correct). Blunt.
- **(e) Source:** S-03 (latency invariant; work should be O(files)).
- **(f) Repro:** build a 500-deep linear commit chain (each parent = previous),
  `checkout` the tip → duration grows ~linearly with depth vs a flat baseline.

---

### BUG-06 — Diff off-by-membership on `changed` vs `added` (STAR: LOGS · S-02 · R10 STRETCH)

- **(a) Location:** `handlers.go` → `Diff` (only if R10 implemented).
- **(b) Subtle vs correct:** classify a path present in both `a` and `b` with
  differing content as `added` (or omit from `changed`) due to checking `b` map
  membership before content comparison. Correct: present-in-both + hash differs
  → `changed`; only-in-b → `added`; only-in-a → `removed`.
- **(c) Star signal + student telemetry:** LOGS — per-path classification log
  `path=<> class=<added|removed|changed> a_hash=<> b_hash=<>` reveals the
  misclassification with the exact path.
- **(d) Master symptom:** diff assertion mismatch (if dgs exercises R10).
- **(e) Source:** S-02 (classification contradiction).
- **(f) Repro:** diff two commits sharing a path with different content →
  reported under `added` instead of `changed`.

> Optional extra race if a second concurrency bug is wanted later: `SetRef`
> read-modify-write of a per-ref history under `RLock` → lost ref update. Not in
> the core six; noted for instructor extension.

**Signal coverage check:** METRICS star = BUG-01, BUG-05 (+04 anomaly). TRACES
star = BUG-02 (+05). LOGS star = BUG-03, BUG-06. RACE/pivot = BUG-04. S-02 heavy
(01,02,03,04,06); S-03 heavy (01,04,05); S-01 backstops 02,03,05. No pure-logic
master-only bug is on the spine.

---

## 3. Base (minimal) signal spec — what ships out of the box

Just enough to prove flow works end-to-end (collector → Prom/Loki/Tempo →
Grafana). Emitted from the ONE request middleware added in `main.go`:

- **DATAPOINT-01 — one request counter.** `vcs.requests.total`
  (Int64Counter, attrs `http.route`, `http.method`). Proves metrics reach
  Prometheus (`lunartides_vcs_requests_total`).
- **DATAPOINT-02 — one top-level span per request.** `otel.Tracer("vcs")` span
  named by route, attrs `http.method`, `http.route`, `http.status_code`. Proves
  traces reach Tempo. **No child spans.**
- **DATAPOINT-03 — one startup log.** An `otel/log` record `"vcs starting"`
  (plus the existing stdlib `log.Printf` listen line). Proves logs reach Loki.

**DELIBERATELY ABSENT (the student adds all of this):**
- No child spans on store hot paths (`normalize`/`hash`/`store`/`reassemble`/
  `fetch_parent`) → BUG-02, BUG-05 invisible.
- No store-level metrics: no `checkout.cache.entries`, no `objects.count` vs
  `objects.map_len`, no `writes.total`, no `checkout.duration` histogram →
  BUG-01, BUG-04, BUG-05 invisible.
- No error/warn logs at swallow sites; no high-cardinality attrs (path, hash,
  byte-offset) → BUG-03, BUG-06 invisible.
- No trace-context correlation on logs (no `trace_id`/`span_id` on records) →
  BUG-04 interleaving un-provable.
- No error-rate / status-class metric → generic health invisible.

---

## 4. Seams for student instrumentation (`TODO(workshop)` map)

Markers already exist in `handlers.go` and `store.go`. This plan adds store-side
seams. Each seam is matched to the bug its signal localizes.

| Seam location | Signal the student adds | Localizes |
|---|---|---|
| `PutObject` entry/exit | child spans `normalize`→`hash`→`store`; attrs `len.in/len.out/hash` | BUG-02 |
| `PutObject` counter block | metrics `objects.count`, `objects.map_len`, `writes.total`; DEBUG log `count.before/after`+`trace_id` | BUG-04 |
| `Checkout` cache put | gauge `checkout.cache.entries` / heap gauge | BUG-01 |
| `Checkout`/`reassemble` swallow site | WARN log `path`,`hash`,`commit`,`trace_id` | BUG-03 |
| `Checkout`/`reassemble` ancestry loop | histogram `checkout.duration`; per-ancestor `fetch_parent` spans | BUG-05 |
| `Diff` classify loop | per-path classification log | BUG-06 |
| request middleware (base) | already present — extend with status-class metric, error logs | general health |

Convention: instruments fetched once via package-level `otel.Meter("vcs")` /
`otel.Tracer("vcs")` / `global.Logger("vcs")`; spans propagate `r.Context()` so
child spans nest and logs correlate via injected trace context.

---

## 5. Test strategy

**Principle:** prove wire-shape stability and the trusted utilities; do NOT write
tests that deterministically expose the telemetry-only bugs (that would defeat
the workshop). Tests use only inputs that keep the bugs dormant.

**Unit-testable (ship these):**
- `wipe` correctness (trusted): after writes, `POST /wipe` → all GETs 404, fresh
  ids work. **Must pass.**
- `healthz` (trusted): `200 {"status":"ok"}`. **Must pass.**
- Wire-shape stability for R1–R7: status codes, JSON keys, error envelope
  `{"error":...}`, 404 on unknown hash/ref/id, 400 on bad body. Assert *shape*,
  not deep round-trip values.
- Hashing/canonical-serialization determinism: same commit inputs → same id
  regardless of map order (trusted helpers).
- Happy-path round-trip with **ASCII, non-empty, single-commit, serial** inputs
  only → passes because BUG-02 (needs non-UTF8), BUG-03 (needs empty file),
  BUG-04 (needs concurrency), BUG-05 (needs deep chain) all stay dormant.

**Telemetry-only (NO deterministic test — verified by reading signal):**
- BUG-01 leak (observe gauge slope), BUG-02 encoding (non-UTF8 class), BUG-03
  swallow (empty-file class), BUG-04 race (concurrent load), BUG-05 latency
  (deep chain). Instructor verification lives in §6, not in `go test`.

> Guardrail: keep the test corpus free of empty strings, non-UTF8 bytes,
> concurrency, and deep chains so CI stays green while the garbage lurks.

---

## 6. Build / run / verify checklist

**Build & run:**
```sh
# infra first (CONTRACT §6.5)
cd lunartides-workshop && docker compose up      # collector :4317, Prom, Loki, Tempo, Grafana :3001
cd vcs && go run ./cmd/vcs                        # :8081, OTLP -> localhost:4317
```
Env (all defaulted, CONTRACT §1): `VCS_ADDR=:8081`, `OTEL_SERVICE_NAME=vcs`,
`OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317`, `OTEL_EXPORTER_OTLP_INSECURE=true`.

**Smoke (flow proof):** `curl :8081/healthz` → `{"status":"ok"}`; do one
`POST /objects` then confirm in Grafana that `lunartides_vcs_requests_total`
increments (Prom), a request span appears (Tempo), and the startup log is in
Loki. Base signal only.

**Instructor bug-liveness matrix** (each row = bug is live AND its star signal
reveals it once the student instruments):

| Bug | Repro command | What confirms it's live | Signal that reveals |
|---|---|---|---|
| 01 | loop 5000× distinct commit+checkout | `checkout.cache.entries` climbs, holds after idle/wipe-of-maps | Prom gauge ramp |
| 02 | `POST /objects` non-UTF8, then `GET` | returned `content.len` < sent | Tempo span `len.out<len.in` |
| 03 | commit `{"a":"x","b":""}`, `checkout` | response has 1 file, `200 OK` | Loki WARN `skipping path b` |
| 04 | 1000 concurrent `POST /objects` | `objects.count` < `objects.map_len` | Prom contradiction + Tempo overlap + Loki interleave |
| 05 | 500-deep chain, `checkout` tip | duration ∝ depth | Tempo `fetch_parent` stack, Prom p95 |
| 06 | diff two commits, shared path differing | path under `added` not `changed` | Loki classify log |

An instructor confirms a signal "reveals" the bug when a **student-authored
alert fires from student telemetry and correlates with a dgs FAIL verdict**
(WORKSHOP §6 success criterion).

---

## 7. Implementation ordering (vertical slices)

One endpoint at a time (WORKSHOP §3), each slice = store method + handler +
base-signal passthrough + wire-shape test. Bugs are planted in the slice that
owns the code.

- **SLICE 0 — trusted spine.** `main.go` request middleware (base signal),
  hashing + canonical helpers, `wipe`/`healthz` tests green. No bugs.
- **SLICE 1 — objects (R1/R2).** `PutObject`/`GetObject`. Plant **BUG-02**
  (normalize), **BUG-03**-half (empty-skip), **BUG-04** (RLock counter).
  Dep: SLICE 0 helpers.
- **SLICE 2 — commits (R3/R4).** `PutCommit`/`GetCommit`, canonical id. Routes
  blob storage through R1 → inherits BUG-02/03 on the commit path. Dep: SLICE 1.
- **SLICE 3 — refs (R5/R6).** `SetRef`/`GetRef`. Simple, correct (ref name→id).
  Dep: SLICE 2 (needs commit ids to point at).
- **SLICE 4 — checkout (R7), the round-trip oracle.** `Checkout` +
  `resolve`/`reassemble`. Plant **BUG-01** (cache), **BUG-03**-swallow, **BUG-05**
  (parent walk). Dep: SLICES 1–3 (objects+commits+refs).
- **SLICE 5 — diff (R10 stretch).** `Diff` over two checkouts. Plant **BUG-06**.
  Dep: SLICE 4.

Dependency spine: 0 → 1 → 2 → 3 → 4 → (5). Each slice is demoable end-to-end
through dgs before the next begins, matching the iterative workshop cadence.
