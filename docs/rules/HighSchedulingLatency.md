# HighSchedulingLatency

Detects goroutines that wait too long in the run queue before a processor picks them up.

---

## The concept

Go's scheduler has three actors: **G** (goroutine), **M** (OS thread), and **P** (processor).
A P is a logical execution slot — only a goroutine holding a P can run. There are exactly
`GOMAXPROCS` Ps at any time.

When a goroutine is ready to run (e.g., it just received from a channel, or its mutex was
unlocked), it enters the **Runnable** state and joins a run queue. It stays there until a P
becomes free and picks it up — at which point it enters **Running**.

**Scheduling latency** is the time between Runnable and Running. During that gap the
goroutine is doing nothing. If it was handling a request, that request is stalled.

Think of it like a task queue in a worker pool: a job is enqueued (`Runnable`) but all
workers are busy. The job sits idle until a worker is free (`Running`). The latency is
the queue wait time, not the processing time.

---

## How the rule works

The rule watches `EventStateTransition` events for goroutine resource kinds:

1. On `GoWaiting → GoRunnable` (or any known state → `GoRunnable`) — records
   `runnableSince[goroutineID] = timestamp`.
2. On `GoRunnable → GoRunning` — computes `now - runnableSince[id]`, deletes the
   map entry, and emits a finding if the latency exceeds a threshold.

Goroutines seen as `GoUndetermined → GoRunnable` at trace start are skipped: without
a known start time the latency cannot be computed.

---

## Thresholds

| Severity | Default | Meaning |
|----------|---------|---------|
| Warn     | 1 ms    | Worth investigating — the scheduler should pick up goroutines in microseconds |
| Error    | 10 ms   | A goroutine waited 10ms+ to be scheduled; requests are noticeably affected |

Tune after construction:

```go
rule := rules.NewHighSchedulingLatency()
rule.WarnThreshold  = 500 * time.Microsecond
rule.ErrorThreshold = 5 * time.Millisecond
```

---

## Example finding

```
[WARN] HighSchedulingLatency — goroutine 47 waited 3.8ms to be scheduled (threshold: 1ms)
  Detail:     Goroutine 47 spent 3.8ms in the run queue before a P picked it up. Any request it was processing was stalled for this duration.
  Suggestion: Check if GOMAXPROCS matches available CPU cores. Look for long-running CPU-bound goroutines that starve the scheduler, or CGo calls that hold OS threads.
  Timestamp:  0.512s
  GoroutineID: 47
```

---

## What to do when you see this

1. **Check `GOMAXPROCS`** — should match the number of available CPU cores. In containers,
   Go may default to the host's core count, not the container's CPU limit. Use
   [`automaxprocs`](https://github.com/uber-go/automaxprocs) to fix this automatically.
2. **Find CPU-bound goroutines** — a long-running goroutine that never yields starves
   others. Look for tight loops without `runtime.Gosched()` or channel operations.
3. **Audit CGo usage** — CGo calls hold an M (OS thread) but release the P. If many CGo
   calls are in flight simultaneously, Ps can be temporarily exhausted.
4. **Look for lock convoy patterns** — many goroutines all waiting on the same mutex will
   all become Runnable at once when it's released, flooding the run queue.

---

## Limitations

- Goroutines already Runnable when the trace starts (`GoUndetermined`) are skipped — their
  wait time started before the trace and cannot be measured.
- The map entry is deleted on the first `GoRunning` transition. If a goroutine is preempted
  and goes back to Runnable, that second wait is measured independently.
- High scheduling latency caused by `GOMAXPROCS` being too low for the workload may show
  up across many goroutines — look for patterns in `GoroutineID` distribution.
