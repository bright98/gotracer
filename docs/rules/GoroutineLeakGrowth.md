# GoroutineLeakGrowth

Detects a significant net increase in goroutine count over the trace window — a strong
signal of a goroutine leak.

---

## The concept

A goroutine leak is when goroutines are created but never terminate. They pile up over
time, consuming memory (~8KB stack each at minimum) and eventually OS threads. In
production, a slow leak can go unnoticed for hours until the service OOMs or latency
degrades from scheduler pressure.

In a healthy service, goroutine count **breathes** with load: it rises when requests
arrive and falls as they complete. A leak looks like a **ratchet**: it goes up, never
comes back down.

Common causes:
- A goroutine blocked forever on a channel nobody writes to or closes
- A goroutine waiting on a context that is never cancelled
- A goroutine-spawning loop with no backpressure (e.g., spawning a goroutine per
  message without bounding concurrency)
- CGo callbacks that start goroutines without pairing them with exits

---

## How the rule works

The rule counts three things by watching goroutine state transitions:

| Event | Counted as |
|-------|-----------|
| `GoUndetermined → any` | `atStart` — goroutines alive at trace start |
| `GoNotExist → any` | `created` — goroutines spawned during trace |
| `any → GoNotExist` | `exited` — goroutines that terminated during trace |

**Net growth = created − exited**

A positive net growth means goroutines are accumulating. In `Flush()` — after the full
trace has been processed — the rule checks this value against two independent thresholds:
absolute growth and relative growth (as a fraction of `atStart`). The higher severity wins.

> **Note:** the goroutine count metric `/sched/goroutines:goroutines` is not emitted in
> the `golang.org/x/exp/trace` event stream available to this tool. The event-counting
> approach is used instead and is more accurate for detecting accumulation within a trace.

---

## Thresholds

| Signal | Severity | Default |
|--------|----------|---------|
| Absolute growth | Warn | +100 goroutines |
| Absolute growth | Error | +500 goroutines |
| Relative growth (% of baseline) | Warn | 25% |
| Relative growth (% of baseline) | Error | 100% (doubled) |

Either signal triggers a finding; the higher severity wins. The relative check is skipped
if there are no goroutines in the baseline (`atStart = 0`).

Tune after construction:

```go
rule := rules.NewGoroutineLeakGrowth()
rule.WarnThreshold = 50
rule.WarnRatio     = 0.10  // 10%
```

---

## Example finding

```
[ERROR] GoroutineLeakGrowth — goroutine count grew by +203 (1353% of baseline) during trace (created 203, exited 0, baseline 15)
  Detail:     203 goroutines were created and only 0 exited during the trace window,
              leaving 203 goroutines accumulated on top of the 15 that existed at trace start.
  Suggestion: Look for goroutines blocked on channels that are never closed, missing context
              cancellation, or goroutine-spawning loops without backpressure. Use
              'go tool pprof -http=: http://host/debug/pprof/goroutine' to inspect live stacks.
```

---

## What to do when you see this

1. **Capture a goroutine profile.** `go tool pprof -http=: http://host/debug/pprof/goroutine`
   shows the stack trace for every live goroutine, grouped by stack. The most common stack
   in the leak is your culprit.

2. **Look for blocked channels.** A goroutine blocked on `<-ch` where `ch` is never closed
   or written to is the most common leak. Search for channel variables that outlive their
   senders/receivers.

3. **Check context propagation.** Goroutines that accept a `context.Context` must respect
   cancellation. A goroutine that ignores `ctx.Done()` will outlive the request it was
   serving.

4. **Add backpressure to spawning loops.** If a loop spawns a goroutine per item, use a
   semaphore or `errgroup` with a bounded count to prevent unbounded growth:
   ```go
   g, ctx := errgroup.WithContext(ctx)
   sem := semaphore.NewWeighted(maxConcurrency)
   for _, item := range items {
       sem.Acquire(ctx, 1)
       g.Go(func() error {
           defer sem.Release(1)
           return process(item)
       })
   }
   ```

5. **Use `goleak` in tests.** The [`go.uber.org/goleak`](https://github.com/uber-go/goleak)
   package detects goroutine leaks at test teardown time, catching issues before production.

---

## Limitations

- A trace is a short window (typically 5–30s). Slow leaks (+1 goroutine per minute) may
  not produce a finding within a single trace. Compare traces taken minutes apart to detect
  slow trends.
- Goroutines created just before the trace ends haven't had time to exit. In a healthy
  service at steady load this is a small number; a very large `created − exited` count
  with many goroutines in short-lived states is a reliable leak signal.
- The rule emits at most one finding per trace (it is a `Flush()`-based rule).
- Goroutines alive at trace start that exit during the trace reduce `exited` and can
  partially mask a leak. If old goroutines are cleaning up while new ones are leaking,
  the net growth may look smaller than the actual accumulation rate.
