# GCAssist

Detects goroutines that block waiting to perform GC mark assist work.

---

## The concept

Go's garbage collector runs **concurrently** — background GC goroutines mark live objects while
your application keeps running. But if your code allocates faster than the GC can mark, the
runtime activates a safety valve called **GC assist**: before an over-budget goroutine's next
allocation is served, the runtime forces it to help the GC by marking some objects itself.

Most of the time this assist work is done **inline** — the goroutine marks a few objects and
immediately gets its allocation. No blocking occurs. The problem arises when there is no
marking work available yet because the background GC workers haven't produced any. In that
case, the goroutine transitions to `GoWaiting` with reason
`"GC mark assist wait for work"` — it is frozen until the GC provides work.

The distributed-systems analogy: a database that, when its write queue is full, makes the
**client** do some of the indexing before accepting the write. The client is not busy doing
useful work — it is doing the database's job, waiting for the database to hand it a task.

### Why this is hard to spot otherwise

GC assist waits do not appear in CPU profiles (the goroutine is not running) and do not
appear as blocking I/O. They look like invisible latency that correlates with allocation rate,
not CPU usage or I/O. Execution traces are currently the only way to see them.

---

## How the rule works

The rule watches `EventStateTransition` events for goroutine resource kinds:

1. On any known state → `GoWaiting` with reason `"GC mark assist wait for work"` — records
   `waitingSince[goroutineID] = (timestamp, stack)`.
2. On `GoWaiting` → `GoRunnable` — computes the wait duration, deletes the map entry,
   and emits a finding if the duration exceeds a threshold.

Goroutines seen as `GoUndetermined → GoWaiting` at trace start are skipped: the wait
started before the trace, so there is no start timestamp.

---

## Thresholds

| Severity | Default | Meaning |
|----------|---------|---------|
| Warn     | 1 ms    | Any assist wait this long is adding measurable latency to requests |
| Error    | 10 ms   | Severe stall — the GC is significantly behind the allocation rate |

Tune after construction:

```go
rule := rules.NewGCAssist()
rule.WarnThreshold  = 500 * time.Microsecond
rule.ErrorThreshold = 5   * time.Millisecond
```

---

## Example finding

```
[WARN] GCAssist — goroutine 91 blocked in GC mark assist for 2.1ms (threshold: 1ms)
  Detail:     Goroutine 91 waited 2.1ms for GC to provide marking work before its allocation was served. The GC could not keep up with the allocation rate.
  Suggestion: Reduce allocation rate in hot paths. Use sync.Pool for frequently allocated objects, pre-size slices and maps, avoid unnecessary string/[]byte conversions. Consider lowering GOGC to trigger more frequent but shorter GC cycles.
  Timestamp:  1.830s
  GoroutineID: 91
```

---

## What to do when you see this

1. **Find the allocating hot path** — the stack attached to the finding points to where the
   goroutine was trying to allocate. Use `pprof` heap allocation profiles to quantify how
   much that site allocates.

2. **Reduce allocation rate in the hot path:**
   - Use `sync.Pool` for objects that are created and discarded at high frequency (e.g.,
     request buffers, JSON encoders, temporary slices).
   - Pre-size slices and maps: `make([]T, 0, n)` avoids repeated growth copies.
   - Avoid `string([]byte)` and `[]byte(string)` conversions in tight loops — each one
     allocates.
   - Reuse buffers with `bytes.Buffer.Reset()` instead of creating new ones.

3. **Tune `GOGC`** — the default `GOGC=100` means GC triggers when live heap doubles. A
   lower value (e.g., `GOGC=50`) runs GC more often but with less work per cycle, reducing
   the chance of falling behind. Set with `debug.SetGCPercent` or the `GOGC` environment
   variable.

4. **Use `GOMEMLIMIT`** (Go 1.19+) — setting a memory limit via `runtime/debug.SetMemoryLimit`
   enables the GC to run more aggressively before hitting the limit, reducing burst assists.

---

## Limitations

- Only **blocking** assist waits are detected. Non-blocking assists (where the goroutine
  does marking work inline without blocking) are not visible as state transitions and
  cannot be measured by this rule — they show up as CPU time instead.
- Assist waits are probabilistic: they require the GC to be running and behind at the exact
  moment a goroutine needs to allocate. Short traces under light load may show none.
- A goroutine still waiting in GC assist at the end of the trace has no complete duration;
  no finding is emitted for it.
