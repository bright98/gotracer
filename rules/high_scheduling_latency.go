package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for HighSchedulingLatency.
const (
	DefaultSchedulingWarnThreshold  = 1 * time.Millisecond
	DefaultSchedulingErrorThreshold = 10 * time.Millisecond
)

// HighSchedulingLatency detects goroutines that spend too long in the run
// queue before a P picks them up.
//
// Scheduling latency is the gap between a goroutine becoming Runnable (ready
// to execute) and actually starting to Run (a P scheduled it). During this
// gap the goroutine is doing nothing — if it was handling a request, that
// request is stalled.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewHighSchedulingLatency()
//	rule.WarnThreshold = 500 * time.Microsecond
type HighSchedulingLatency struct {
	WarnThreshold  time.Duration
	ErrorThreshold time.Duration

	// runnableSince maps each goroutine that entered the Runnable state to the
	// time and stack recorded at that transition. Entries are deleted when the
	// goroutine transitions to Running.
	//
	// TODO: bound map size for very long traces with goroutine leaks.
	runnableSince map[trace.GoID]goEntry
}

// NewHighSchedulingLatency creates a HighSchedulingLatency rule with default
// thresholds: 1ms for Warn, 10ms for Error.
func NewHighSchedulingLatency() *HighSchedulingLatency {
	return &HighSchedulingLatency{
		WarnThreshold:  DefaultSchedulingWarnThreshold,
		ErrorThreshold: DefaultSchedulingErrorThreshold,
		runnableSince:  make(map[trace.GoID]goEntry),
	}
}

// Name implements findings.Rule.
func (r *HighSchedulingLatency) Name() string { return "HighSchedulingLatency" }

// Process implements findings.Rule. It tracks every goroutine that enters the
// Runnable state and emits a finding when the matching Running transition
// reveals a latency above the threshold.
func (r *HighSchedulingLatency) Process(ev trace.Event) []findings.Finding {
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
	case to == trace.GoRunnable:
		// Only record goroutines whose Runnable transition we witnessed
		// from a known state. GoUndetermined means the goroutine was already
		// runnable when the trace started — we have no start time for it.
		if from != trace.GoUndetermined {
			r.runnableSince[id] = goEntry{at: ev.Time(), stack: extractStack(ev)}
		}

	case from == trace.GoRunnable && to == trace.GoRunning:
		if entry, ok := r.runnableSince[id]; ok {
			latency := ev.Time().Sub(entry.at)
			delete(r.runnableSince, id) // release the slot immediately
			return r.evaluate(id, latency, time.Duration(entry.at), entry.stack)
		}
	}

	return nil
}

// Flush implements findings.Rule. Goroutines still in runnableSince at stream
// end never ran during the trace window; we cannot compute their latency.
func (r *HighSchedulingLatency) Flush() []findings.Finding { return nil }

func (r *HighSchedulingLatency) evaluate(id trace.GoID, latency, timestamp time.Duration, stack []string) []findings.Finding {
	var sev findings.Severity
	var threshold time.Duration

	switch {
	case latency >= r.ErrorThreshold:
		sev = findings.Error
		threshold = r.ErrorThreshold
	case latency >= r.WarnThreshold:
		sev = findings.Warn
		threshold = r.WarnThreshold
	default:
		return nil
	}

	l := latency.Round(time.Microsecond)
	return []findings.Finding{{
		Severity:    sev,
		Rule:        r.Name(),
		Message:     fmt.Sprintf("goroutine %d waited %v to be scheduled (threshold: %v)", id, l, threshold),
		Detail:      fmt.Sprintf("Goroutine %d spent %v in the run queue before a P picked it up. Any request it was processing was stalled for this duration.", id, l),
		Suggestion:  "Check if GOMAXPROCS matches available CPU cores. Look for long-running CPU-bound goroutines that starve the scheduler, or CGo calls that hold OS threads.",
		Timestamp:   timestamp,
		GoroutineID: uint64(id),
		Stack:       stack,
	}}
}
