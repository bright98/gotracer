package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for GCAssist.
// 1ms is meaningful: a goroutine blocked in GC assist for 1ms adds that
// directly to request latency. 10ms is a severe stall.
const (
	DefaultGCAssistWarnThreshold  = 1 * time.Millisecond
	DefaultGCAssistErrorThreshold = 10 * time.Millisecond
)

// GCAssist detects goroutines that block waiting to do GC mark assist work.
//
// When the GC cannot keep up with the allocation rate, the runtime forces
// allocating goroutines to help: before serving an allocation, it makes the
// goroutine mark some heap objects on behalf of the GC. Normally this is done
// inline and the goroutine never stops. But when there is no marking work
// available yet (the GC background workers haven't produced it), the goroutine
// blocks in GoWaiting with reason "GC mark assist wait for work" until the GC
// provides work.
//
// This blocking is invisible in CPU profiles — the goroutine is neither running
// nor doing useful work. It shows up as unexplained latency that correlates with
// allocation rate rather than CPU usage.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewGCAssist()
//	rule.WarnThreshold = 500 * time.Microsecond
type GCAssist struct {
	WarnThreshold  time.Duration
	ErrorThreshold time.Duration

	// waitingSince maps each goroutine blocked in GC assist to the time and
	// stack recorded at that entry. Entries are deleted when the goroutine
	// is woken.
	waitingSince map[trace.GoID]goEntry
}

// NewGCAssist creates a GCAssist rule with default thresholds.
func NewGCAssist() *GCAssist {
	return &GCAssist{
		WarnThreshold:  DefaultGCAssistWarnThreshold,
		ErrorThreshold: DefaultGCAssistErrorThreshold,
		waitingSince:   make(map[trace.GoID]goEntry),
	}
}

// Name implements findings.Rule.
func (r *GCAssist) Name() string { return "GCAssist" }

// Process implements findings.Rule. It records when a goroutine enters
// GoWaiting for GC mark assist and emits a finding when it is woken if
// the wait exceeded a threshold.
func (r *GCAssist) Process(ev trace.Event) []findings.Finding {
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
	case to == trace.GoWaiting && st.Reason == "GC mark assist wait for work":
		if from != trace.GoUndetermined {
			r.waitingSince[id] = goEntry{at: ev.Time(), stack: extractStack(ev)}
		}

	case from == trace.GoWaiting:
		if entry, ok := r.waitingSince[id]; ok {
			duration := ev.Time().Sub(entry.at)
			delete(r.waitingSince, id)
			return r.evaluate(id, duration, time.Duration(entry.at), entry.stack)
		}
	}

	return nil
}

// Flush implements findings.Rule. A goroutine still waiting in GC assist at
// stream end has not been woken yet; without a complete duration we emit nothing.
func (r *GCAssist) Flush() []findings.Finding { return nil }

func (r *GCAssist) evaluate(id trace.GoID, duration, timestamp time.Duration, stack []string) []findings.Finding {
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
		Message:     fmt.Sprintf("goroutine %d blocked in GC mark assist for %v (threshold: %v)", id, d, threshold),
		Detail:      fmt.Sprintf("Goroutine %d waited %v for GC to provide marking work before its allocation was served. The GC could not keep up with the allocation rate.", id, d),
		Suggestion:  "Reduce allocation rate in hot paths. Use sync.Pool for frequently allocated objects, pre-size slices and maps, avoid unnecessary string/[]byte conversions. Consider lowering GOGC to trigger more frequent but shorter GC cycles.",
		Timestamp:   timestamp,
		GoroutineID: uint64(id),
		Stack:       stack,
	}}
}
