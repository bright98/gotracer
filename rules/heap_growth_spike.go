package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

const heapObjectsMetricName = "/memory/classes/heap/objects:bytes"

// Default thresholds for HeapGrowthSpike.
const (
	DefaultHeapWarnGrowthBytes    uint64  = 100 * 1024 * 1024  // 100 MB absolute growth
	DefaultHeapErrorGrowthBytes   uint64  = 500 * 1024 * 1024  // 500 MB
	DefaultHeapWarnRateBytesPerSec  float64 = 50 * 1024 * 1024  // 50 MB/s
	DefaultHeapErrorRateBytesPerSec float64 = 200 * 1024 * 1024 // 200 MB/s
)

// HeapGrowthSpike detects rapid growth of the Go heap over the trace window.
//
// Rapid heap growth triggers more frequent GC cycles (more STW pauses) and
// can force goroutines into GC assist — where they must do GC marking work
// before their own allocations are served, adding invisible latency on top of
// STW pauses.
//
// The rule watches /memory/classes/heap/objects:bytes metric events, records
// the first and last samples, then in Flush() checks two independent signals:
//
//   - Absolute growth: final − baseline in bytes
//   - Growth rate: growth / elapsed time in bytes per second
//
// The rate signal catches fast accumulation even in short traces where the
// absolute delta might look small. Either signal can fire independently; the
// higher severity wins.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewHeapGrowthSpike()
//	rule.WarnGrowthBytes     = 50 * 1024 * 1024    // 50 MB
//	rule.WarnRateBytesPerSec = 20 * 1024 * 1024    // 20 MB/s
type HeapGrowthSpike struct {
	WarnGrowthBytes     uint64
	ErrorGrowthBytes    uint64
	WarnRateBytesPerSec  float64
	ErrorRateBytesPerSec float64

	firstBytes uint64
	firstTime  trace.Time
	lastBytes  uint64
	lastTime   trace.Time
	samples    int
}

// NewHeapGrowthSpike creates a HeapGrowthSpike rule with default thresholds.
func NewHeapGrowthSpike() *HeapGrowthSpike {
	return &HeapGrowthSpike{
		WarnGrowthBytes:      DefaultHeapWarnGrowthBytes,
		ErrorGrowthBytes:     DefaultHeapErrorGrowthBytes,
		WarnRateBytesPerSec:  DefaultHeapWarnRateBytesPerSec,
		ErrorRateBytesPerSec: DefaultHeapErrorRateBytesPerSec,
	}
}

// Name implements findings.Rule.
func (r *HeapGrowthSpike) Name() string { return "HeapGrowthSpike" }

// Process implements findings.Rule. It collects heap size samples from metric
// events; the finding is emitted in Flush once the full window is known.
func (r *HeapGrowthSpike) Process(ev trace.Event) []findings.Finding {
	if ev.Kind() != trace.EventMetric {
		return nil
	}
	m := ev.Metric()
	if m.Name != heapObjectsMetricName {
		return nil
	}

	bytes := m.Value.Uint64()
	if r.samples == 0 {
		r.firstBytes = bytes
		r.firstTime = ev.Time()
	}
	r.lastBytes = bytes
	r.lastTime = ev.Time()
	r.samples++

	return nil
}

// Flush implements findings.Rule. It computes absolute growth and growth rate
// across the trace window and emits a finding if either threshold is exceeded.
// Requires at least two samples; if the heap only appeared once, no finding
// is emitted.
func (r *HeapGrowthSpike) Flush() []findings.Finding {
	if r.samples < 2 {
		return nil
	}
	if r.lastBytes <= r.firstBytes {
		return nil // heap shrank or stayed flat — no spike
	}

	growth := r.lastBytes - r.firstBytes
	elapsed := time.Duration(r.lastTime - r.firstTime)
	var rate float64
	if elapsed > 0 {
		rate = float64(growth) / elapsed.Seconds()
	}

	var sev findings.Severity
	switch {
	case growth >= r.ErrorGrowthBytes || rate >= r.ErrorRateBytesPerSec:
		sev = findings.Error
	case growth >= r.WarnGrowthBytes || rate >= r.WarnRateBytesPerSec:
		sev = findings.Warn
	default:
		return nil
	}

	return []findings.Finding{{
		Severity: sev,
		Rule:     r.Name(),
		Message: fmt.Sprintf(
			"heap grew by %s at %s/s over %v (%s → %s)",
			formatBytes(growth), formatBytes(uint64(rate)),
			elapsed.Round(time.Millisecond),
			formatBytes(r.firstBytes), formatBytes(r.lastBytes),
		),
		Detail: fmt.Sprintf(
			"The live heap grew from %s to %s (+%s) over %v, a rate of %s/s. "+
				"Rapid heap growth triggers more frequent GC cycles and can force "+
				"goroutines into GC assist, adding latency on top of STW pauses.",
			formatBytes(r.firstBytes), formatBytes(r.lastBytes),
			formatBytes(growth), elapsed.Round(time.Millisecond),
			formatBytes(uint64(rate)),
		),
		Suggestion: "Check allocation rate with 'go tool pprof -alloc_space'. " +
			"Look for hot paths that allocate large or many short-lived objects. " +
			"Consider sync.Pool for frequently allocated types, setting GOMEMLIMIT " +
			"to cap heap growth, or increasing GOGC to reduce GC frequency.",
		Timestamp: time.Duration(r.firstTime),
	}}
}

// formatBytes formats a byte count as a human-readable string (KB, MB, GB).
func formatBytes(b uint64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1024))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
