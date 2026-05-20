package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for ProcessorStarvation.
const (
	DefaultProcWarnThreshold  = 10 * time.Millisecond
	DefaultProcErrorThreshold = 100 * time.Millisecond
)

// ProcessorStarvation detects processors (Ps) that sit idle for an unusually
// long time, which is a signal that the scheduler is unable to keep all Ps
// busy despite available work.
//
// In Go's GMP model a P is an execution permit — a goroutine must hold a P to
// run, and a P must be attached to an OS thread (M) to execute. When all Ms
// are occupied by blocking syscalls or CGo calls, idle Ps cannot pick up new
// work because there are no free threads to run on. This is thread exhaustion:
// the CPU is available but the scheduler is starved of OS threads.
//
// A P going idle briefly is healthy — it means the run queue was momentarily
// empty. A P idle for tens or hundreds of milliseconds during active load is a
// signal of thread exhaustion, GOMAXPROCS being too low, or severe work
// imbalance across Ps.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewProcessorStarvation()
//	rule.WarnThreshold = 5 * time.Millisecond
type ProcessorStarvation struct {
	WarnThreshold  time.Duration
	ErrorThreshold time.Duration

	// idleSince maps each P that entered ProcIdle to the time it did so.
	// The entry is deleted when the P returns to ProcRunning.
	idleSince map[trace.ProcID]trace.Time
}

// NewProcessorStarvation creates a ProcessorStarvation rule with default thresholds.
func NewProcessorStarvation() *ProcessorStarvation {
	return &ProcessorStarvation{
		WarnThreshold:  DefaultProcWarnThreshold,
		ErrorThreshold: DefaultProcErrorThreshold,
		idleSince:      make(map[trace.ProcID]trace.Time),
	}
}

// Name implements findings.Rule.
func (r *ProcessorStarvation) Name() string { return "ProcessorStarvation" }

// Process implements findings.Rule. It records the moment a P becomes idle and
// emits a finding when it returns to running if the idle duration exceeds a
// threshold.
func (r *ProcessorStarvation) Process(ev trace.Event) []findings.Finding {
	if ev.Kind() != trace.EventStateTransition {
		return nil
	}
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceProc {
		return nil
	}

	id := st.Resource.Proc()
	from, to := st.Proc()

	switch {
	case to == trace.ProcIdle:
		// Only track Ps whose idle entry we witnessed from a known state.
		// ProcUndetermined means the P was already idle when the trace started
		// — we have no start time for it.
		if from != trace.ProcUndetermined {
			r.idleSince[id] = ev.Time()
		}

	case from == trace.ProcIdle:
		if since, ok := r.idleSince[id]; ok {
			idle := ev.Time().Sub(since)
			delete(r.idleSince, id)
			return r.evaluate(id, idle, time.Duration(since))
		}
	}

	return nil
}

// Flush implements findings.Rule. A P still idle at stream end never resumed;
// without a complete duration we emit nothing.
func (r *ProcessorStarvation) Flush() []findings.Finding { return nil }

func (r *ProcessorStarvation) evaluate(id trace.ProcID, idle, timestamp time.Duration) []findings.Finding {
	var sev findings.Severity
	var threshold time.Duration

	switch {
	case idle >= r.ErrorThreshold:
		sev = findings.Error
		threshold = r.ErrorThreshold
	case idle >= r.WarnThreshold:
		sev = findings.Warn
		threshold = r.WarnThreshold
	default:
		return nil
	}

	d := idle.Round(time.Microsecond)
	return []findings.Finding{{
		Severity:  sev,
		Rule:      r.Name(),
		Message:   fmt.Sprintf("P %d was idle for %v (threshold: %v)", id, d, threshold),
		Detail:    fmt.Sprintf("Processor %d sat idle for %v. During this window it could not execute any goroutines. If goroutines were waiting in the run queue, this represents direct request stall time.", id, d),
		Suggestion: "Check for thread exhaustion: many simultaneous blocking syscalls or CGo calls can starve Ps of OS threads. " +
			"Verify GOMAXPROCS matches available cores (use automaxprocs in containers). " +
			"Look for goroutine-per-request patterns without concurrency limits.",
		Timestamp: timestamp,
	}}
}
