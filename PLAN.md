# gotracer — Project Roadmap

## What this is
CLI tool + optional middleware library that captures/reads Go runtime execution traces,
runs rule-based analysis, and emits structured findings. The **pgexplain equivalent for
the Go runtime itself** — fills the gap that `go tool trace` leaves: browser-only, no
structured output, unusable in CI.

---

## CLI modes (implement in this order)

```
# Mode 3 — reads a file from disk; no network needed; best for learning
gotracer analyze trace.out

# Mode 2 — captures from standard pprof endpoint; no service changes needed
gotracer capture --url http://localhost:6060/debug/pprof/trace --duration 5s

# Mode 1 — flight recorder; requires middleware embedded in the target service
gotracer snapshot --url http://localhost:8080/debug/gotracer/snapshot
```

---

## Findings — implementation order

Each finding teaches one new Go runtime concept.

| # | Rule | Concept taught | Status |
|---|------|----------------|--------|
| 1 | `GCPauseSpike` | STW pauses, RangeBegin/End events | ✅ done |
| 2 | `HighSchedulingLatency` | GMP scheduler, run queue, Runnable→Running delay | ✅ done |
| 3 | `BlockedOnSyscall` | syscall handling, M thread stealing, netpoller | 🔄 in progress |
| 4 | `MutexContention` | sync primitives in the runtime, block reasons | — |
| 5 | `GoroutineLeakGrowth` | goroutine lifecycle, EventMetric, count over time | ✅ done |
| 6 | `HeapGrowthSpike` | heap metrics, GC pressure, GOGC/GOMEMLIMIT | ✅ done |
| 7 | `ProcessorStarvation` | P idle while work exists, CGo, thread exhaustion | ✅ done |

---

## Repo structure

```
gotracer/
├── capture/       # HTTP capture from /debug/pprof/trace + flight recorder endpoint
├── reader/        # wraps golang.org/x/exp/trace, emits clean Event stream
├── analyzer/      # stateful stream processor, per-goroutine state machines
├── rules/         # one file per finding — same pattern as pgexplain
├── findings/      # Finding type, Severity, Rule interface
├── report/        # terminal + JSON output formatters
├── middleware/    # optional FlightRecorder HTTP handler to embed in services
├── cmd/gotracer   # CLI entry point (cobra)
└── testdata/      # captured .out trace fixtures for unit tests
```

---

## Non-negotiable constraints

- **Streaming:** analyzer must never buffer the full event list
- **Exit codes:** 0 = no findings / Info only · 1 = Warn/Error · 2 = parse/input error
- **Every rule** gets a unit test; stateful rules get an end-to-end test with a real trace
- Idiomatic Go: small interfaces, no global state, explicit error handling
- All exported types have godoc comments

---

## How we work together

Haleh is learning Go runtime internals *through* building this project. These are new territory:
GMP model · Go scheduler · trace event format · GC internals · flight recorder.

**Protocol for each new rule:**
1. Explain the concept in plain language (distributed-systems analogies)
2. Answer questions before writing any code
3. Design the interface/API first, get approval, then implement
4. Add tests in the same session
