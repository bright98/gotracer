# BlockedOnSyscall

Detects goroutines that spend excessive time blocked in a system call.

---

## The concept

When a goroutine makes a **blocking system call** (e.g., reading from a slow file, waiting
on a socket with no deadline), the Go runtime transitions it to the `GoSyscall` state. The
goroutine is now frozen — it cannot do anything until the OS returns from the syscall.

Crucially, the runtime **steals the goroutine's P** and gives it to another goroutine so
other work can continue. The goroutine is alive but off the scheduler's hands, parked
waiting for the kernel.

Think of it like a microservice that makes a synchronous RPC call with no timeout. The
calling goroutine is stuck — it's not on the CPU, it's not in a queue, it's just waiting
for an external actor (the OS) to respond. Any request the goroutine was serving is
stalled for exactly this duration.

### What generates GoSyscall events (and what doesn't)

**Does produce `GoSyscall`:**
- Raw file I/O (`os.File.Read`, `os.File.Write` on regular files)
- Direct `syscall.Syscall` / `syscall.RawSyscall` calls
- CGo calls (the runtime wraps them as syscalls)

**Does NOT produce `GoSyscall`** (uses `GoWaiting` via the netpoller instead):
- Network socket reads/writes (`net.Conn`, `http.Client`)
- Pipe reads/writes registered with the runtime's netpoller
- `time.Sleep` (uses a runtime timer, not a blocking syscall)

This is an important distinction: most Go network I/O is non-blocking under the hood. The
netpoller parks goroutines in `GoWaiting` while waiting on the kernel, which is invisible
to this rule.

---

## How the rule works

The rule watches `EventStateTransition` events for goroutine resource kinds:

1. On any known state → `GoSyscall` — records `syscallSince[goroutineID] = timestamp`.
2. On `GoSyscall` → `GoRunning` or `GoSyscall` → `GoRunnable` — computes the duration,
   deletes the map entry, and emits a finding if the duration exceeds a threshold.

Both exit paths are handled:
- `GoSyscall → GoRunning`: the goroutine's P was still available when the syscall returned.
- `GoSyscall → GoRunnable`: the P was stolen while the goroutine was blocked; the runtime
  must reschedule it. Either way, the syscall is done.

Goroutines seen as `GoUndetermined → GoSyscall` at trace start are skipped: the syscall
started before the trace, so there is no start timestamp.

---

## Thresholds

| Severity | Default | Meaning |
|----------|---------|---------|
| Warn     | 10 ms   | Legitimate fast I/O is sub-millisecond; 10ms suggests a slow path |
| Error    | 100 ms  | Definitively stalling requests for a noticeable duration |

Tune after construction:

```go
rule := rules.NewBlockedOnSyscall()
rule.WarnThreshold  = 5 * time.Millisecond
rule.ErrorThreshold = 50 * time.Millisecond
```

---

## Example finding

```
[WARN] BlockedOnSyscall — goroutine 83 blocked in syscall for 18.4ms (threshold: 10ms)
  Detail:     Goroutine 83 spent 18.4ms blocked in a system call. Any request it was handling stalled for this duration.
  Suggestion: Check for slow disk I/O, network reads without deadlines, or blocking CGo calls. Add context deadlines to I/O operations and prefer non-blocking alternatives.
  Timestamp:  2.041s
  GoroutineID: 83
```

---

## What to do when you see this

1. **Add deadlines to all I/O** — `os.File.SetDeadline`, `context.WithTimeout` on database
   calls, `http.Client.Timeout`. Any blocking I/O without a deadline is a latency
   time-bomb.
2. **Check for slow disks** — if file reads are slow, consider async I/O patterns or moving
   I/O to a dedicated goroutine pool so it doesn't stall request-handling goroutines.
3. **Audit CGo usage** — CGo calls appear as syscalls to the tracer. A slow C library call
   blocks the goroutine just like a slow disk read. Ensure CGo calls have their own
   timeouts.
4. **Profile which syscall is slow** — combine with `go tool trace`'s goroutine view to
   find the stack at the time of the syscall, or add `pprof` labels to correlate with
   CPU profiles.

---

## Limitations

- Network I/O via Go's standard library is handled by the netpoller and appears as
  `GoWaiting`, not `GoSyscall` — it is invisible to this rule. Use `HighSchedulingLatency`
  as a complementary signal for goroutines waiting on network I/O.
- A goroutine still in `GoSyscall` at the end of the trace has not returned; without a
  complete duration, no finding is emitted for it.
- Very fast syscalls (sub-microsecond) will only appear with `WarnThreshold = 0`, which
  is useful for debugging but too noisy for production analysis.
