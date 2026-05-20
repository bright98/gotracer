# MutexContention

Detects goroutines that wait too long to acquire a sync primitive (Mutex, RWMutex, WaitGroup).

---

## The concept

When a goroutine calls `Lock()` on a mutex that is already held, the Go runtime **parks**
it: it transitions the goroutine to `GoWaiting` and hands its P to something else. The
goroutine is frozen — doing nothing — until the holder calls `Unlock()`, at which point
the runtime wakes it up to `GoRunnable`.

**Contention time** is the gap between parking and being woken. During that gap the
goroutine — and any request it was serving — is stalled.

Think of it like a row lock in a database. A transaction tries to update a row, finds it
locked, and blocks. It doesn't busy-poll; the database queues it until the lock is
released. If the holder spends 200ms doing a slow query while holding the lock, every
waiter pays that 200ms.

This is distinct from:
- **HighSchedulingLatency** — goroutine is ready but no P is free
- **BlockedOnSyscall** — goroutine is waiting for the OS kernel
- **MutexContention** — goroutine is waiting for *another goroutine* to release a lock

---

## How the rule works

The Go runtime uses a single block reason string `"sync"` for all `sync` package
primitives (Mutex, RWMutex, WaitGroup). The rule watches `EventStateTransition` events:

1. On `GoRunning → GoWaiting` with `Reason == "sync"` — records
   `waitingSince[goroutineID] = timestamp`. All other `GoWaiting` reasons (channel, sleep,
   network, select) are ignored.
2. On `GoWaiting → GoRunnable` — if the goroutine is in `waitingSince`, computes the
   duration, deletes the entry, and emits a finding if it exceeds a threshold.

The exit check requires no reason filter: only goroutines that entered via a `"sync"`
block are ever in the map, so non-mutex waiters are naturally excluded on exit too.

Goroutines seen as `GoUndetermined → GoWaiting` at trace start are skipped: the wait
began before the trace, so no start timestamp is available.

---

## Thresholds

| Severity | Default | Meaning |
|----------|---------|---------|
| Warn     | 1 ms    | Any contention above 1ms is worth investigating |
| Error    | 10 ms   | A goroutine waited 10ms+ for a lock; requests are visibly affected |

Tune after construction:

```go
rule := rules.NewMutexContention()
rule.WarnThreshold  = 500 * time.Microsecond
rule.ErrorThreshold = 5 * time.Millisecond
```

---

## Example finding

```
[WARN] MutexContention — goroutine 112 waited 3.2ms to acquire a mutex (threshold: 1ms)
  Detail:     Goroutine 112 spent 3.2ms blocked waiting for a sync primitive. Any request it was handling stalled for this duration.
  Suggestion: Check if the mutex holder is doing slow work (I/O, DB calls) while holding the lock. Consider narrowing the critical section, sharding the mutex, or using sync.RWMutex for read-heavy workloads.
  Timestamp:  0.814s
  GoroutineID: 112
```

---

## What to do when you see this

1. **Find the holder, not just the waiter.** The finding tells you which goroutine waited,
   but the problem is in the goroutine *holding* the lock. Use the timestamp to correlate
   with other trace events or pprof profiles to identify the holder's hot path.

2. **Narrow the critical section.** Move any I/O, DB calls, or computation outside the
   locked region. Hold the lock only for the minimum time needed to touch shared state.

3. **Use `sync.RWMutex` for read-heavy workloads.** If most goroutines only read shared
   state, `RLock()` allows concurrent reads. Only writes need an exclusive lock.

4. **Shard the mutex.** If contention is on a single global structure (a cache, a registry),
   split it into N independent shards each with its own lock. A goroutine hashes its key
   to pick a shard, reducing contention by factor N.

5. **Look for lock convoys.** If many goroutines all have the same contention wait time,
   they queued up behind a single holder. Reducing the hold time breaks the convoy.

---

## Limitations

- The block reason `"sync"` covers Mutex, RWMutex, and WaitGroup. The rule cannot
  distinguish between them — if you need to tell them apart, cross-reference with goroutine
  stacks in `go tool trace`.
- `sync.Cond.Wait` uses reason `"sync.(*Cond).Wait"` and is **not** captured by this rule.
- Goroutines that acquire the lock via spinning (fast path, no `GoWaiting`) are invisible
  to this rule — only contention that results in parking is captured.
- A goroutine still in `GoWaiting` at the end of the trace has not been woken yet; no
  finding is emitted for it.
