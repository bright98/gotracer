package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for GoroutineLeakGrowth.
const (
	DefaultLeakWarnThreshold  int     = 100  // net goroutine growth to warn
	DefaultLeakErrorThreshold int     = 500  // net goroutine growth to error
	DefaultLeakWarnRatio      float64 = 0.25 // 25% growth relative to baseline
	DefaultLeakErrorRatio     float64 = 1.0  // 100% growth (count doubled)
)

// GoroutineLeakGrowth detects a significant net increase in goroutines over
// the trace window, which is a strong signal of a goroutine leak.
//
// The rule counts three things from state transition events:
//
//   - atStart: goroutines alive when tracing began (GoUndetermined → any state)
//   - created: goroutines spawned during the trace (GoNotExist → any state)
//   - exited:  goroutines that terminated during the trace (any state → GoNotExist)
//
// Net growth = created − exited. A positive value means goroutines are
// accumulating faster than they are being cleaned up.
//
// A finding is emitted in Flush() — after the full trace is processed — if the
// net growth exceeds either the absolute threshold OR the relative threshold
// (as a fraction of atStart). The dual check catches leaks in small services
// (where absolute numbers are low but growth ratios are alarming) and large
// services (where a 25% ratio is noise but +500 goroutines is not).
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewGoroutineLeakGrowth()
//	rule.WarnThreshold = 50
//	rule.WarnRatio     = 0.10
type GoroutineLeakGrowth struct {
	WarnThreshold  int
	ErrorThreshold int
	WarnRatio      float64
	ErrorRatio     float64

	atStart  int        // goroutines alive at trace start
	created  int        // goroutines created during trace
	exited   int        // goroutines that exited during trace
	firstEvt trace.Time // timestamp of the first goroutine event seen
	hasFirst bool
}

// NewGoroutineLeakGrowth creates a GoroutineLeakGrowth rule with default thresholds.
func NewGoroutineLeakGrowth() *GoroutineLeakGrowth {
	return &GoroutineLeakGrowth{
		WarnThreshold:  DefaultLeakWarnThreshold,
		ErrorThreshold: DefaultLeakErrorThreshold,
		WarnRatio:      DefaultLeakWarnRatio,
		ErrorRatio:     DefaultLeakErrorRatio,
	}
}

// Name implements findings.Rule.
func (r *GoroutineLeakGrowth) Name() string { return "GoroutineLeakGrowth" }

// Process implements findings.Rule. It counts goroutine creation, exit, and
// baseline events; the finding is emitted in Flush once the full window is known.
func (r *GoroutineLeakGrowth) Process(ev trace.Event) []findings.Finding {
	if ev.Kind() != trace.EventStateTransition {
		return nil
	}
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return nil
	}

	from, to := st.Goroutine()

	if !r.hasFirst {
		r.firstEvt = ev.Time()
		r.hasFirst = true
	}

	switch {
	case from == trace.GoUndetermined && to != trace.GoNotExist:
		// Goroutine existed before tracing started.
		r.atStart++

	case from == trace.GoNotExist && to != trace.GoNotExist:
		// New goroutine spawned during the trace.
		r.created++

	case to == trace.GoNotExist:
		// Goroutine terminated.
		r.exited++
	}

	return nil
}

// Flush implements findings.Rule. It computes net goroutine growth and emits
// a finding if either the absolute or relative threshold is exceeded.
func (r *GoroutineLeakGrowth) Flush() []findings.Finding {
	if !r.hasFirst {
		return nil
	}

	growth := r.created - r.exited
	if growth <= 0 {
		return nil // goroutines are not accumulating
	}

	var sev findings.Severity
	switch {
	case growth >= r.ErrorThreshold:
		sev = findings.Error
	case r.atStart > 0 && float64(growth)/float64(r.atStart) >= r.ErrorRatio:
		sev = findings.Error
	case growth >= r.WarnThreshold:
		sev = findings.Warn
	case r.atStart > 0 && float64(growth)/float64(r.atStart) >= r.WarnRatio:
		sev = findings.Warn
	default:
		return nil
	}

	var ratioStr string
	if r.atStart > 0 {
		ratioStr = fmt.Sprintf(" (%.0f%% of baseline)", float64(growth)/float64(r.atStart)*100)
	}

	return []findings.Finding{{
		Severity: sev,
		Rule:     r.Name(),
		Message: fmt.Sprintf(
			"goroutine count grew by +%d%s during trace (created %d, exited %d, baseline %d)",
			growth, ratioStr, r.created, r.exited, r.atStart,
		),
		Detail: fmt.Sprintf(
			"%d goroutines were created and only %d exited during the trace window, "+
				"leaving %d goroutines accumulated on top of the %d that existed at trace start.",
			r.created, r.exited, growth, r.atStart,
		),
		Suggestion: "Look for goroutines blocked on channels that are never closed, " +
			"missing context cancellation, or goroutine-spawning loops without backpressure. " +
			"Use 'go tool pprof -http=: http://host/debug/pprof/goroutine' to inspect live stacks.",
		Timestamp: time.Duration(r.firstEvt),
	}}
}
