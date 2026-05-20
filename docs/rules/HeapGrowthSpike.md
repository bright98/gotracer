# HeapGrowthSpike

Detects rapid growth of the Go heap over the trace window, a signal of high allocation
pressure that leads to more frequent GC cycles and potential GC assist slowdowns.

---

## The concept

The Go heap is where dynamically allocated objects live. The GC's job is to periodically
scan it and free objects that nothing points to anymore. It decides when to run based on
**GOGC** (default: 100): when the live heap grows 100% beyond what was alive after the
last GC, start a new cycle.

**Rapid heap growth** means the application is allocating faster than GC can reclaim. This
has a cascade effect:

1. **More GC cycles.** The heap goal is reached sooner, triggering GC more often.
2. **More STW pauses.** Each GC cycle brings two stop-the-world pauses (see GCPauseSpike).
3. **GC assist.** If goroutines allocate faster than the background GC can mark, the
   runtime forces them to do GC work themselves before serving their own allocations.
   This is invisible in CPU profiles but shows up as unexplained latency.

Think of it like a database buffer pool under write pressure: at some point the system
spends more time flushing dirty pages (GC) than executing queries (your requests).

---

## How the rule works

The rule watches `/memory/classes/heap/objects:bytes` metric events — the bytes of live
heap objects, sampled by the runtime at every trace generation boundary (~every few ms).

It records the **first** and **last** samples, then in `Flush()` computes two signals:

| Signal | Formula |
|--------|---------|
| Absolute growth | `last − first` in bytes |
| Growth rate | `growth / elapsed_seconds` in bytes per second |

Either signal can trigger a finding independently; the higher severity wins. The rate
signal catches fast accumulation even in short traces where the absolute delta looks small
(e.g., 60 MB over 1 second fires on rate before it fires on absolute growth).

> **Implementation note:** Only the first and last samples are used. Intermediate
> fluctuations — e.g., a GC cycle that temporarily lowers the heap mid-trace — do not
> affect the finding. This focuses attention on the net trend rather than transient spikes.

---

## Thresholds

| Signal | Severity | Default |
|--------|----------|---------|
| Absolute growth | Warn | 100 MB |
| Absolute growth | Error | 500 MB |
| Growth rate | Warn | 50 MB/s |
| Growth rate | Error | 200 MB/s |

Tune after construction:

```go
rule := rules.NewHeapGrowthSpike()
rule.WarnGrowthBytes     = 50 * 1024 * 1024   // 50 MB
rule.WarnRateBytesPerSec = 20 * 1024 * 1024   // 20 MB/s
```

---

## Example finding

```
[WARN] HeapGrowthSpike — heap grew by 120.3 MB at 60.2 MB/s over 2s (45.0 MB → 165.3 MB)
  Detail:     The live heap grew from 45.0 MB to 165.3 MB (+120.3 MB) over 2s, a rate of
              60.2 MB/s. Rapid heap growth triggers more frequent GC cycles and can force
              goroutines into GC assist, adding latency on top of STW pauses.
  Suggestion: Check allocation rate with 'go tool pprof -alloc_space'. Look for hot paths
              that allocate large or many short-lived objects. Consider sync.Pool for
              frequently allocated types, setting GOMEMLIMIT to cap heap growth, or
              increasing GOGC to reduce GC frequency.
```

---

## What to do when you see this

1. **Profile allocation sites.** `go tool pprof -alloc_space http://host/debug/pprof/heap`
   shows where allocations are coming from, ranked by total bytes allocated (not just what's
   live right now). This catches hot allocation paths even if most objects are short-lived.

2. **Look for large per-request allocations.** JSON decoding, protobuf unmarshalling, and
   string/byte slice operations are common culprits. Reading a large payload into a single
   `[]byte` before processing it allocates all at once.

3. **Use `sync.Pool` for frequently allocated types.** If a type is allocated and freed
   many times per second (e.g., request context objects, buffers), pooling can dramatically
   reduce GC pressure:
   ```go
   var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
   buf := bufPool.Get().(*bytes.Buffer)
   buf.Reset()
   defer bufPool.Put(buf)
   ```

4. **Set `GOMEMLIMIT`.** Introduced in Go 1.19, this caps the total memory the runtime
   uses. When the heap approaches the limit, the GC runs more aggressively rather than
   growing without bound. Combine with tuned GOGC:
   ```
   GOMEMLIMIT=512MiB GOGC=off ./myservice  # let GOMEMLIMIT drive GC entirely
   ```

5. **Increase `GOGC`.** A higher GOGC (e.g., 200) means GC triggers less often at the
   cost of higher peak memory. If you have memory headroom, this can reduce GC frequency
   and thus reduce heap growth events.

---

## Limitations

- Only net growth (first → last sample) is measured. A trace that starts and ends at the
  same heap size but spikes in the middle will not produce a finding. Use `GCPauseSpike`
  as a complementary signal for mid-trace GC activity.
- The rate is computed over the full trace window. A burst at the start followed by idle
  time will show a low average rate even if the burst itself was extreme.
- GC assist latency (goroutines forced to do marking work) is not directly measurable with
  this rule; it manifests as unexplained request latency. Use heap growth as a leading
  indicator and correlate with tail latency in your metrics system.
- Requires at least two heap metric samples; a trace shorter than one generation boundary
  (~a few milliseconds) will not produce a finding.
