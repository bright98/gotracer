# GCPauseSpike

Detects stop-the-world (STW) GC pauses that exceed a duration threshold.

---

## The concept

Go's garbage collector is mostly concurrent — the mark phase runs alongside your goroutines.
But every GC cycle still requires two short **stop-the-world** moments:

1. **Mark start** — the runtime freezes everything briefly to snapshot root pointers.
2. **Mark termination** — it freezes again to finalize the mark phase before sweeping.

During an STW pause, **every goroutine in the process is frozen**. No work happens. No
requests are served. The pause is invisible in CPU profiles (the goroutines aren't running,
so they don't show up), which is why the trace is the right tool to catch it.

Think of it like a distributed systems epoch barrier: all workers must stop and wait for
the coordinator to take a consistent snapshot before anyone can proceed.

---

## How the rule works

The Go runtime emits `EventRangeBegin` / `EventRangeEnd` pairs with a name like
`"stop-the-world (GC mark termination)"`. The rule:

1. On `EventRangeBegin` with a `"stop-the-world (…)"` name — records the start timestamp.
2. On the matching `EventRangeEnd` — computes the duration and emits a finding if it
   exceeds a threshold.

`EventRangeActive` (emitted when the trace starts mid-pause) is intentionally ignored:
without a start timestamp, the duration cannot be computed.

---

## Thresholds

| Severity | Default | Meaning |
|----------|---------|---------|
| Warn     | 1 ms    | Sub-millisecond is the target for a well-tuned service; 1ms is worth investigating |
| Error    | 10 ms   | Definitively impacting tail latency at this point |

Tune after construction:

```go
rule := rules.NewGCPauseSpike()
rule.WarnThreshold  = 500 * time.Microsecond
rule.ErrorThreshold = 5 * time.Millisecond
```

---

## Example finding

```
[ERROR] GCPauseSpike — GC STW pause (GC mark termination) of 14.2ms exceeds 10ms threshold
  Detail:     A stop-the-world pause froze all goroutines for 14.2ms. Every in-flight request was stalled for this duration.
  Suggestion: Check allocation rate with 'go tool pprof -alloc_space'. Consider setting GOMEMLIMIT to bound heap growth, or increasing GOGC to reduce GC frequency.
  Timestamp:  1.234s
```

---

## What to do when you see this

1. **Check allocation rate** — `go tool pprof -alloc_space` shows which code paths allocate
   the most. High allocation rate → more frequent GC → more STW pauses.
2. **Set `GOMEMLIMIT`** — bounds heap growth so the runtime doesn't accumulate a large heap
   that takes longer to mark. Introduced in Go 1.19.
3. **Increase `GOGC`** — a higher value (default 100) means GC triggers less often at the
   cost of higher peak memory. Useful if you have headroom.
4. **Reduce pointer-heavy structures** — the GC must trace every pointer. Flat structs with
   fewer pointers scan faster, shortening the mark phase and thus the STW.
5. **Use `sync.Pool`** for short-lived allocations — reduces GC pressure by reusing objects.

---

## Limitations

- Only measures the STW portion of GC. GC assist (goroutines helping the GC mark phase)
  also adds latency but is not captured here — see `HeapGrowthSpike` for that signal.
- A trace started mid-pause (`EventRangeActive`) cannot produce a finding because the
  start time is unknown.
