package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for MutexContention.
// 1ms mirrors HighSchedulingLatency — both are "goroutine waiting for goroutine".
const (
	DefaultMutexWarnThreshold  = 1 * time.Millisecond
	DefaultMutexErrorThreshold = 10 * time.Millisecond
)

// MutexContention detects goroutines that wait too long to acquire a sync
// primitive (sync.Mutex, sync.RWMutex, sync.WaitGroup).
//
// When a goroutine calls Lock() on a held mutex the runtime parks it:
// it transitions to GoWaiting and gives up its P. It sits frozen until
// the holder calls Unlock(), at which point the runtime transitions the waiter
// to GoRunnable. The contention duration is the time between those two events.
//
// The block reason the runtime attaches for all sync primitives is "sync".
// This means the rule catches Mutex, RWMutex, and WaitGroup waits but cannot
// distinguish between them. sync.Cond.Wait uses a separate reason and is not
// captured here.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewMutexContention()
//	rule.WarnThreshold = 500 * time.Microsecond
type MutexContention struct {
	WarnThreshold  time.Duration
	ErrorThreshold time.Duration

	// waitingSince maps each goroutine that entered GoWaiting on a sync
	// primitive to the time it did so. Entries are deleted on GoRunnable.
	waitingSince map[trace.GoID]trace.Time
}

// NewMutexContention creates a MutexContention rule with default thresholds.
func NewMutexContention() *MutexContention {
	return &MutexContention{
		WarnThreshold:  DefaultMutexWarnThreshold,
		ErrorThreshold: DefaultMutexErrorThreshold,
		waitingSince:   make(map[trace.GoID]trace.Time),
	}
}

// Name implements findings.Rule.
func (r *MutexContention) Name() string { return "MutexContention" }

// Process implements findings.Rule. It records the moment a goroutine enters
// GoWaiting on a sync primitive and emits a finding when it is woken to
// GoRunnable if the wait exceeds a threshold.
func (r *MutexContention) Process(ev trace.Event) []findings.Finding {
	if ev.Kind() != trace.EventStateTransition {
		return nil
	}
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return nil
	}

	id := st.Resource.Goroutine()
	from, to := st.Goroutine()

	switch {
	case to == trace.GoWaiting && st.Reason == "sync":
		// Only track goroutines whose block we witnessed from a known state.
		// GoUndetermined means the goroutine was already waiting at trace start
		// — we have no start time for it.
		if from != trace.GoUndetermined {
			r.waitingSince[id] = ev.Time()
		}

	case from == trace.GoWaiting:
		// The goroutine was woken by Unlock(). We only have an entry if we
		// previously saw it block on a sync primitive, so non-mutex waits
		// (channel, sleep, etc.) are naturally ignored.
		if since, ok := r.waitingSince[id]; ok {
			duration := ev.Time().Sub(since)
			delete(r.waitingSince, id)
			return r.evaluate(id, duration, time.Duration(since))
		}
	}

	return nil
}

// Flush implements findings.Rule. A goroutine still in GoWaiting at stream end
// has not been woken yet; without a complete duration we emit nothing.
func (r *MutexContention) Flush() []findings.Finding { return nil }

func (r *MutexContention) evaluate(id trace.GoID, duration, timestamp time.Duration) []findings.Finding {
	var sev findings.Severity
	var threshold time.Duration

	switch {
	case duration >= r.ErrorThreshold:
		sev = findings.Error
		threshold = r.ErrorThreshold
	case duration >= r.WarnThreshold:
		sev = findings.Warn
		threshold = r.WarnThreshold
	default:
		return nil
	}

	d := duration.Round(time.Microsecond)
	return []findings.Finding{{
		Severity:    sev,
		Rule:        r.Name(),
		Message:     fmt.Sprintf("goroutine %d waited %v to acquire a mutex (threshold: %v)", id, d, threshold),
		Detail:      fmt.Sprintf("Goroutine %d spent %v blocked waiting for a sync primitive. Any request it was handling stalled for this duration.", id, d),
		Suggestion:  "Check if the mutex holder is doing slow work (I/O, DB calls) while holding the lock. Consider narrowing the critical section, sharding the mutex, or using sync.RWMutex for read-heavy workloads.",
		Timestamp:   timestamp,
		GoroutineID: uint64(id),
	}}
}
