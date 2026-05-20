package rules

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for GCPauseSpike.
// Sub-millisecond is the target for a well-tuned Go service; 10ms is
// definitively impacting request latency.
const (
	DefaultGCPauseWarnThreshold  = 1 * time.Millisecond
	DefaultGCPauseErrorThreshold = 10 * time.Millisecond
)

// GCPauseSpike detects stop-the-world GC pauses that exceed a threshold.
//
// Each GC cycle produces two STW pauses: one before the mark phase and one
// after. During an STW pause every goroutine is frozen — including those
// serving requests — so long pauses directly inflate tail latency.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewGCPauseSpike()
//	rule.WarnThreshold = 500 * time.Microsecond
type GCPauseSpike struct {
	WarnThreshold  time.Duration
	ErrorThreshold time.Duration

	// pendingStart records the Time of the most recent "stop-the-world"
	// RangeBegin. hasPending is false if no STW is currently in flight,
	// which also guards against an EventRangeActive at trace start where
	// we cannot compute a meaningful duration.
	pendingStart trace.Time
	hasPending   bool
}

// NewGCPauseSpike creates a GCPauseSpike rule with default thresholds.
func NewGCPauseSpike() *GCPauseSpike {
	return &GCPauseSpike{
		WarnThreshold:  DefaultGCPauseWarnThreshold,
		ErrorThreshold: DefaultGCPauseErrorThreshold,
	}
}

// Name implements findings.Rule.
func (r *GCPauseSpike) Name() string { return "GCPauseSpike" }

// Process implements findings.Rule. It records the start of an STW range and
// emits a finding when the matching end arrives and the duration exceeds a
// threshold.
func (r *GCPauseSpike) Process(ev trace.Event) []findings.Finding {
	switch ev.Kind() {
	case trace.EventRangeBegin:
		if isSTW(ev.Range().Name) {
			r.pendingStart = ev.Time()
			r.hasPending = true
		}

	case trace.EventRangeEnd:
		if isSTW(ev.Range().Name) && r.hasPending {
			duration := ev.Time().Sub(r.pendingStart)
			ts := time.Duration(r.pendingStart)
			reason := stwReason(ev.Range().Name)
			r.hasPending = false
			return r.evaluate(reason, duration, ts)
		}
	}
	return nil
}

// Flush implements findings.Rule. GCPauseSpike emits findings on RangeEnd
// events, so there is nothing pending at stream end.
func (r *GCPauseSpike) Flush() []findings.Finding { return nil }

func (r *GCPauseSpike) evaluate(reason string, duration, timestamp time.Duration) []findings.Finding {
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
		Severity:  sev,
		Rule:      r.Name(),
		Message:   fmt.Sprintf("GC STW pause (%s) of %v exceeds %v threshold", reason, d, threshold),
		Detail:    fmt.Sprintf("A stop-the-world pause froze all goroutines for %v. Every in-flight request was stalled for this duration.", d),
		Suggestion: "Check allocation rate with 'go tool pprof -alloc_space'. " +
			"Consider setting GOMEMLIMIT to bound heap growth, or increasing GOGC to reduce GC frequency.",
		Timestamp: timestamp,
	}}
}

// isSTW reports whether a range name is a stop-the-world pause.
// The runtime always uses the form "stop-the-world (reason)".
func isSTW(name string) bool {
	return strings.HasPrefix(name, "stop-the-world (")
}

// stwReason extracts the reason from "stop-the-world (reason)".
func stwReason(name string) string {
	name = strings.TrimPrefix(name, "stop-the-world (")
	return strings.TrimSuffix(name, ")")
}
