# ProcessorStarvation

Detects processors (Ps) that sit idle for an unusually long time — a signal that the
scheduler cannot keep all Ps busy despite available work.

---

## The concept

In Go's GMP model, a **P** is an execution permit. A goroutine must hold a P to run,
and a P must be attached to an OS thread (M) to execute. There are exactly `GOMAXPROCS`
Ps at any time.

A P going idle briefly is **healthy**: the local run queue is empty, so the P parks and
waits. The problem is when a P sits idle while goroutines are waiting in the run queue —
the P "wants" to run but cannot. This is **processor starvation**.

The most common cause is **thread exhaustion**: all Ms are occupied by blocking syscalls
or CGo calls, so idle Ps have no free thread to attach to. The CPU is available. The Ps
are available. Goroutines are queued. But there are no OS threads left to do the work.

Think of it like a connection pool where all connections are stuck in long transactions.
New queries arrive, but no connection is free to serve them — even though the database
server itself has idle capacity.

---

## How the rule works

The Go runtime emits `EventStateTransition` for **Proc resources**, just as it does for
goroutines. A P transitions between two states:

| Transition | Meaning |
|------------|---------|
| `ProcRunning → ProcIdle` | P stopped running — run queue is empty |
| `ProcIdle → ProcRunning` | P resumed — a goroutine became available |

The rule:
1. On `ProcRunning → ProcIdle` — records `idleSince[procID] = now`
2. On `ProcIdle → ProcRunning` — computes idle duration, emits a finding if it exceeds a
   threshold

This mirrors `HighSchedulingLatency` exactly, applied to **Proc resources** instead of
Goroutine resources.

Ps already idle when the trace starts (`ProcUndetermined → ProcIdle`) are skipped — no
start time is available for them.

---

## Thresholds

| Severity | Default | Meaning |
|----------|---------|---------|
| Warn     | 10 ms   | P idle this long during serving load is worth investigating |
| Error    | 100 ms  | P idle this long strongly suggests thread exhaustion or misconfiguration |

Tune after construction:

```go
rule := rules.NewProcessorStarvation()
rule.WarnThreshold  = 5 * time.Millisecond
rule.ErrorThreshold = 50 * time.Millisecond
```

---

## Example finding

```
[ERROR] ProcessorStarvation — P 3 was idle for 112ms (threshold: 100ms)
  Detail:     Processor 3 sat idle for 112ms. During this window it could not execute
              any goroutines. If goroutines were waiting in the run queue, this
              represents direct request stall time.
  Suggestion: Check for thread exhaustion: many simultaneous blocking syscalls or CGo
              calls can starve Ps of OS threads. Verify GOMAXPROCS matches available
              cores (use automaxprocs in containers). Look for goroutine-per-request
              patterns without concurrency limits.
  Timestamp:  1.204s
```

---

## What to do when you see this

1. **Correlate with `HighSchedulingLatency`.** A P idle for 50ms while goroutines wait
   50ms to be scheduled is a strong confirmation of starvation. The two findings together
   paint the complete picture.

2. **Check for CGo saturation.** Each CGo call holds an M for its entire duration. If
   many goroutines call CGo simultaneously, the M pool exhausts and Ps go starved. Use
   `go tool pprof -http=: /debug/pprof/threadcreate` to see how many threads are being
   created.

3. **Check for blocking syscall storms.** Many simultaneous goroutines in `GoSyscall`
   state will hold Ms and starve Ps. Correlate with `BlockedOnSyscall` findings — if
   many goroutines are blocked in syscalls at the same time as Ps are idle, that's your
   answer.

4. **Verify `GOMAXPROCS` in containers.** Go defaults `GOMAXPROCS` to the host's CPU
   count, not the container's CPU limit. In a container with 2 CPU cores, Go might think
   it has 64 Ps available — but 62 of them can never actually run. Use
   [`automaxprocs`](https://github.com/uber-go/automaxprocs) to fix this automatically:
   ```go
   import _ "go.uber.org/automaxprocs"
   ```

5. **Add concurrency limits.** Goroutine-per-request patterns without backpressure can
   spawn thousands of goroutines, each competing for CGo or syscall-bound Ms. Use a
   semaphore or `errgroup.WithContext` to cap concurrent operations.

---

## Limitations

- A single P idle for 20ms does not prove starvation — it might just mean the run queue
  was genuinely empty at that moment. Starvation is most confidently identified when
  multiple Ps are idle simultaneously while `HighSchedulingLatency` findings appear at
  the same timestamp.
- Ps still idle at the end of the trace have no complete duration and produce no finding.
- Very short idle periods (sub-millisecond) are normal and expected, even under load.
  The default 10ms warn threshold filters out routine scheduler noise.
