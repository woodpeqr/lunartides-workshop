# Observability 101
### or: How to Stop Guessing and Start Knowing

---

## Agenda

- What even *is* observability?
- Why it matters — aka "why your on-call rotation hates you right now"
- The Three Pillars: Logs, Metrics, Traces
- Using them together like an adult
- Instrumentation — the part where you actually do work

---

---

# 1. What is Observability?

### (no, it's not just "add more logs")

---

## The One Question That Rules Them All

> "Can you answer questions about your system **without deploying new code**?"

- Yes → you have observability. Congrats.
- No → you are debugging via vibes.
- Observability is a **property** of a system, not a dashboard you buy.

---

## Black Box vs. White Box

**Black box**
- You see inputs and outputs
- You guess what's happening inside
- Basically: "it works on my machine" energy

**White box**
- Internal state is exposed as data
- You can ask arbitrary questions about what's happening
- Basically: trust, but verify

---

## Observability vs. Monitoring

| Monitoring | Observability |
|---|---|
| "Is it broken?" | "Why is it broken?" |
| Known failure modes | Unknown failure modes |
| Alerts on thresholds | Answers questions you didn't know you'd need to ask |
| That dashboard nobody reads | Saving your neck at 3am |

Monitoring tells you the house is on fire.
Observability tells you which wire caused it.

---

## Why This Matters Now

- One user request can touch 10+ services
- Something slow in service D shows up as an error in service A
- You cannot attach a debugger to production (please don't try)
- Without observability: you find out your system is broken from a user tweet
- With observability: you find out first, fix it, pretend it never happened

---

---

# 2. Why it Matters — Reliability

### aka: "how to have a conversation about incidents without crying"

---

## "Reliable" Doesn't Mean "Never Fails"

- Everything fails. Accept this.
- Reliable = "fails in predictable, acceptable ways"
- Reliable = your users don't notice (or forgive you fast when they do)
- You cannot improve what you cannot measure — classic wisdom, still true

---

## SLIs — What Are You Even Measuring?

**SLI** = Service Level Indicator = a number that reflects user experience

Common ones:
- Request success rate (% non-5xx responses)
- Latency (how long users wait — p99 matters more than average)
- Availability (is the thing even reachable?)

Key rule: if users don't feel it, it's probably not a good SLI.

---

## SLOs — You Need Goals, Bud

**SLO** = Service Level Objective = the target for your SLI

Examples:
- "99.9% of requests succeed over 30 days"
- "p99 latency stays under 500ms"

SLOs are contracts with yourself.
Break them: bad day.
Don't track them: worse day, delayed by weeks.

---

## Error Budgets — The Fun Part

- SLO = 99.9% uptime → error budget = 0.1% failures allowed per month
- Every incident, risky deploy, and experiment **spends** that budget
- Budget gone → freeze risky changes, fix reliability first
- Turns "are we reliable enough?" from a feelings debate into a maths debate

Much healthier. Slightly less fun at parties.

---

## What Actually Breaks in Production

Real things that happen to real people:
- Cascading failures — one slow service takes down everything upstream
- Silent data corruption — no errors, just wrong answers (the scariest)
- Memory leaks — fine for hours, catastrophic crater at 2am
- Thundering herd — you restart a service, it gets slammed, it crashes again
- Flaky third-party APIs — not your fault, still your incident

Observability doesn't prevent these. It just means you don't spend 4 hours staring at logs wondering what happened.

---

---

# 3. The Three Pillars

### Logs. Metrics. Traces. The holy trinity of "what is going on."

---

## Overview

```
┌─────────┐   ┌─────────┐   ┌─────────┐
│  LOGS   │   │ METRICS │   │ TRACES  │
│         │   │         │   │         │
│ Events  │   │ Numbers │   │Journeys │
│ over    │   │ over    │   │ across  │
│ time    │   │ time    │   │services │
└─────────┘   └─────────┘   └─────────┘
```

Think of them as three different friends describing the same party:
- Logs: "at 10:43pm, Dave knocked over the punch bowl"
- Metrics: "punch consumption spiked 400% between 10pm and 11pm"
- Traces: "Dave went kitchen → living room → punch bowl in 4 seconds"

---

---

## Pillar 1: Logs

### The oldest trick in the book. Still valid.

---

## What Are Logs?

- Discrete, timestamped records that something happened
- The original observability primitive — `console.log` is technically a log
- You have been writing logs since day one. Probably bad ones.

```
2024-03-15T10:23:41Z INFO  user_login user_id=42 ip=192.168.1.1 duration_ms=34
2024-03-15T10:23:42Z ERROR payment_failed order_id=9901 reason="card_declined"
```

---

## Structured vs. Unstructured Logs

**Unstructured** (please don't)
```
[ERROR] Something went wrong with the payment thing for that order
```
- Humans can read it
- Machines weep

**Structured** (yes, always)
```json
{"level":"error","event":"payment_failed","order_id":9901,"reason":"card_declined"}
```
- Machines can query it
- Humans can still read it
- You can actually search for it at 3am without losing your mind

---

## What Logs Are Good For

- "What exactly happened to order 9901?"
- Debugging a known incident in a known time window
- Audit trails — who did what, when, so you can point fingers correctly
- One-off weird things that don't fit any metric

**Limitations**
- High volume = high cost. Log everything = broke everything.
- Not aggregated — hard to spot trends
- Without correlation, each log is an island

---

---

## Pillar 2: Metrics

### Numbers. Over time. On a dashboard. You love to see it.

---

## What Are Metrics?

- Numerical measurements aggregated over time
- "How many? How fast? How full? How broken?"
- Cheap to store, fast to query, great for alerts

```
http_requests_total{method="POST", status="200"} 4291
http_request_duration_seconds{p99} 0.342
memory_usage_bytes 1073741824
```

---

## Common Metric Types

| Type | Meaning | Example |
|---|---|---|
| Counter | Only goes up | Total requests served |
| Gauge | Goes up and down | Current memory usage |
| Histogram | Distribution of values | Request latency buckets |

Histograms = you can calculate percentiles.
Percentiles = you know what your slow users experience.
Averages = lying to yourself. Don't use averages alone.

---

## Cardinality — The Trap Everyone Falls Into

**Cardinality** = number of unique label combinations your metric has

```
# Fine — a few statuses
http_requests_total{status="200"}

# Career-limiting move
http_requests_total{user_id="u_8f2k3j"}  ← one series per user = millions of series
```

- High cardinality kills your metrics backend
- Labels should have **bounded** value sets (status codes: fine. user IDs: no.)
- Learn this now. Save yourself a very awkward on-call handover.

---

## What Metrics Are Good For

- Dashboards showing health over time
- Alerting when something crosses a threshold
- Spotting regressions immediately after a deploy
- Capacity planning ("we'll need 3x more servers by Q3")

**Limitations**
- Pre-aggregated — you can only ask questions you anticipated
- No detail about individual events
- Won't tell you *why*, only *that*

---

---

## Pillar 3: Traces

### Where it gets fancy. And genuinely very cool.

---

## What Are Traces?

- A record of a **request's full journey** through your system
- Entry point → every service → response
- Made up of **spans**

```
[  User Request  ──────────────────────────── 342ms  ]
  [  API Gateway  ─── 12ms ]
                 [  Auth Service ── 45ms ]
                              [  DB Query ─────────── 180ms ]  ← here's your problem
                                          [ Cache ─ 3ms ]
```

No trace: "checkout is slow, idk why, have fun"
With trace: "DB query is slow, here it is, you're welcome"

---

## Spans

A **span** = one unit of work inside a trace:
- Name, start time, duration
- Attributes (key-value context — user ID, order ID, region)
- Events (timestamped notes — "retry attempt 1", "cache miss")
- Status: OK or Error (with a message)

Spans nest into a tree. Root span = the whole operation. Leaf spans = the actual work.

---

## Context Propagation

- Traces span multiple services via **context propagation**
- A trace ID travels with every request (HTTP headers, message queue metadata)
- Each service creates spans under the same trace ID
- Without propagation: disconnected fragments. Useless.

This requires coordination. Every service in the chain needs to pass the context along. It's a team sport.

---

## What Traces Are Good For

- "Why is checkout slow? WHERE is it slow?"
- Cross-service debugging — find the actual root cause, not just the symptom
- Understanding what your system actually does under real traffic
- Spotting that one service quietly taking 90% of request time

**Limitations**
- You can't keep every trace at scale — sampling required
- Requires buy-in across all services to work
- Still not great for "what happened to this specific user over 3 days"

---

---

# 4. Signals Working Together

### The Avengers, but for debugging

---

## The Pillars Cover Each Other's Blind Spots

Alone: each pillar leaves you with half the picture.
Together: you can actually answer "what happened and why."

```
Metrics  →  "p99 latency spiked at 14:32"         (WHAT happened)
Traces   →  "Checkout → DB is taking 1.8s"         (WHERE it happened)
Logs     →  "connection pool exhausted, retrying"  (WHY it happened)
```

This is the dream. This is what you're building toward.

---

## A Real Incident Walkthrough

**14:32** — Alert fires: `checkout_latency_p99 > 2s`
You: "okay something is wrong"
*(metric told you — WHAT)*

**14:33** — Open a slow trace
Span tree: `DB query` taking 1.8s. Normal: 50ms.
You: "okay it's the DB"
*(trace told you — WHERE)*

**14:34** — Pull logs from DB service at 14:32
```
WARN  connection_pool_exhausted pool_size=10 waiting=47
```
You: "ah. pool too small."
*(log told you — WHY)*

**14:35** — Root cause found, fix deployed, incident resolved.
Without all three: still guessing at 16:00.

---

## Correlation Is the Power Move

The real unlock: **linking signals together**
- Trace ID embedded in log lines → jump straight from trace to its logs
- Metric anomaly links to example traces from that window
- Span attributes match log fields → filter logs by span context

This is called **correlated telemetry**.
It makes investigation feel like following a thread instead of searching a haystack.

---

## What You Can Ask With All Three

- "Show me all requests where checkout took > 1s this hour" *(traces)*
- "What was DB query rate during the incident?" *(metrics)*
- "What did the payment service log for order 9901?" *(logs)*
- "Show me logs from the errored spans in this trace" *(all three, the good stuff)*

None of these are easy with one pillar. All of them are fast with three, done right.

---

---

# 5. Instrumentation Basics

### You actually have to write code for this. Sorry.

---

## What Is Instrumentation?

- Adding code (or config) to emit telemetry — logs, metrics, traces
- No instrumentation = no observability = no answers = bad time
- Signal quality = instrumentation quality. You own this.

Think of it like leaving breadcrumbs.
Bad instrumentation = breadcrumbs made of bread in the rain.

---

## Manual vs. Automatic Instrumentation

**Automatic**
- Libraries instrument HTTP servers, DB clients, queues for you
- Zero code changes to get baseline spans and metrics
- Fast to set up, great starting point
- Coverage stops at what the library understands

**Manual**
- You add spans and attributes for your own business logic
- "User completed checkout", "recommendation model returned 0 results"
- More effort, massively higher signal value
- This is the stuff that actually helps you debug YOUR problems

Best strategy: **both**. Auto for infrastructure, manual for the things that actually matter to your product.

---

## Garbage In, Garbage Out

You will be tempted to cut corners. Don't.

Common mistakes:
- Everything logged as ERROR because you couldn't decide (wolf who cried error)
- Span named `"HTTP POST"` instead of `"checkout.process_payment"` — useless
- No user ID or order ID on spans — you'll regret this during an incident
- Metric labels with user IDs in them — cardinality bomb
- Forgetting to propagate trace context — your traces are now just vibes

Good instrumentation takes discipline. It pays off the first time you have an incident and solve it in 5 minutes instead of 5 hours.

---

## Instrumentation Checklist

Before you ship anything new, ask:

- [ ] Errors logged with enough context to actually debug them?
- [ ] Key operations wrapped in spans with meaningful names?
- [ ] Business-relevant attributes on spans (user ID, order ID, etc)?
- [ ] SLI metrics emitted (success rate, latency, throughput)?
- [ ] Trace context propagated to all downstream calls?

All five checked = you will survive your first on-call rotation.
Zero checked = good luck, godspeed.

---

---

# Recap

### You made it. Here's what you actually learned.

---

## The TL;DR

**Observability** = can you answer questions about prod without deploying code?

**Reliability** = SLIs measure it, SLOs target it, error budgets make it actionable

**Three Pillars:**
- Logs → discrete events, always structured
- Metrics → aggregated numbers, watch your cardinality
- Traces → request journeys across services, requires propagation

**Together** → correlated signals let you go from "something is wrong" to "here's exactly why" fast

**Instrumentation** → auto for infra, manual for business logic, quality matters more than quantity

---

## Key Takeaways

1. Build observability in from day one — retrofitting it is painful and humbling
2. Structured logs, percentile metrics, distributed traces — the holy trinity
3. Correlate everything — trace ID in logs, metrics linked to traces
4. You cannot improve what you cannot measure
5. Good instrumentation is a team habit, not a platform feature

---

## Questions?

*(if your question is "can I just not do this" — no)*

---
