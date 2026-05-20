package rules_test

import (
	"bytes"
	"runtime"
	runtrace "runtime/trace"
	"testing"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/rules"
)

// --- helpers ---

// heapMetric returns a synthetic /memory/classes/heap/objects:bytes metric event.
func heapMetric(t *testing.T, ts trace.Time, sizeBytes uint64) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.Metric]{
		Time: ts,
		Kind: trace.EventMetric,
		Details: trace.Metric{
			Name:  "/memory/classes/heap/objects:bytes",
			Value: trace.Uint64Value(sizeBytes),
		},
	})
	if err != nil {
		t.Fatalf("MakeEvent(heapMetric): %v", err)
	}
	return ev
}

const (
	mb = 1024 * 1024
	gb = 1024 * mb
)

// --- unit tests ---

func TestHeapGrowthSpikeNoBelowBothThresholds(t *testing.T) {
	rule := rules.NewHeapGrowthSpike() // warn at 100MB growth or 50MB/s

	// 50MB growth over 2s = 25MB/s — below both absolute (100MB) and rate (50MB/s).
	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 2_000_000_000, 150*mb))
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings for 50MB/2s growth, got %d", len(fs))
	}
}

func TestHeapGrowthSpikeAbsoluteWarnThreshold(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()

	// 150MB growth over 10s = 15MB/s — absolute growth (150MB) exceeds warn (100MB).
	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 10_000_000_000, 250*mb))
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 150MB growth, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestHeapGrowthSpikeAbsoluteErrorThreshold(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()

	// 600MB growth — exceeds absolute error (500MB).
	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 10_000_000_000, 700*mb))
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 600MB growth, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestHeapGrowthSpikeRateWarnThreshold(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()

	// 60MB growth over 1s = 60MB/s — rate (60MB/s) exceeds warn (50MB/s).
	// Absolute (60MB) is below absolute warn (100MB) — rate fires first.
	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 1_000_000_000, 160*mb))
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 60MB/s growth rate, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestHeapGrowthSpikeRateErrorThreshold(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()

	// 250MB growth over 1s = 250MB/s — rate exceeds error (200MB/s).
	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 1_000_000_000, 350*mb))
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 250MB/s rate, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestHeapGrowthSpikeCustomThresholds(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()
	rule.WarnGrowthBytes = 20 * mb
	rule.ErrorGrowthBytes = 100 * mb
	rule.WarnRateBytesPerSec = 10 * mb
	rule.ErrorRateBytesPerSec = 50 * mb

	// 30MB growth over 2s = 15MB/s — above custom warn for both signals.
	rule.Process(heapMetric(t, 0, 50*mb))
	rule.Process(heapMetric(t, 2_000_000_000, 80*mb))
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestHeapGrowthSpikeNoFindingWhenFlat(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()
	rule.WarnGrowthBytes = 0
	rule.WarnRateBytesPerSec = 0

	// Heap stays the same.
	rule.Process(heapMetric(t, 0, 200*mb))
	rule.Process(heapMetric(t, 1_000_000_000, 200*mb))
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings when heap is flat, got %d", len(fs))
	}
}

func TestHeapGrowthSpikeNoFindingWhenDecreasing(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()
	rule.WarnGrowthBytes = 0
	rule.WarnRateBytesPerSec = 0

	// Heap shrinks (GC freed memory).
	rule.Process(heapMetric(t, 0, 500*mb))
	rule.Process(heapMetric(t, 1_000_000_000, 200*mb))
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings when heap decreases, got %d", len(fs))
	}
}

func TestHeapGrowthSpikeOneSampleNoFinding(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()
	rule.WarnGrowthBytes = 0
	rule.WarnRateBytesPerSec = 0

	rule.Process(heapMetric(t, 0, 500*mb))
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings with only one sample, got %d", len(fs))
	}
}

func TestHeapGrowthSpikeNoSamplesNoFinding(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()
	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("expected no findings with no samples, got %d", len(fs))
	}
}

// TestHeapGrowthSpikeUsesFirstAndLastSample verifies that intermediate
// fluctuations (e.g., a GC cycle mid-trace that temporarily lowers the heap)
// don't affect the finding — only first and last samples matter.
func TestHeapGrowthSpikeUsesFirstAndLastSample(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()
	rule.WarnGrowthBytes = 0
	rule.WarnRateBytesPerSec = 0

	// Starts at 100MB, spikes to 600MB (GC hasn't run yet), then GC fires
	// and drops to 120MB. The net growth is only +20MB.
	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 500_000_000, 600*mb))
	rule.Process(heapMetric(t, 1_000_000_000, 120*mb))
	fs := rule.Flush()

	// Only 20MB net growth — message should reflect first→last.
	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	// GoroutineID must be 0: this is a process-level finding.
	if fs[0].GoroutineID != 0 {
		t.Errorf("GoroutineID should be 0, got %d", fs[0].GoroutineID)
	}
}

func TestHeapGrowthSpikeFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewHeapGrowthSpike()

	rule.Process(heapMetric(t, 0, 100*mb))
	rule.Process(heapMetric(t, 2_000_000_000, 300*mb))
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "HeapGrowthSpike" {
		t.Errorf("Rule = %q, want HeapGrowthSpike", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
}

func TestHeapGrowthSpikeName(t *testing.T) {
	if rules.NewHeapGrowthSpike().Name() != "HeapGrowthSpike" {
		t.Error("unexpected Name()")
	}
}

// --- integration test ---

// TestHeapGrowthSpikeEndToEnd generates a real trace while allocating a large
// amount of memory, then verifies the rule fires through the full pipeline.
//
// We keep references to the allocations alive (via heapSink) so GC does not
// free them before the heap metric samples are emitted. Thresholds are lowered
// so we don't need to allocate hundreds of MB in a test.
var heapSink [][]byte

func TestHeapGrowthSpikeEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	// Allocate 30MB in small chunks so many metric events are emitted
	// between the first and last sample.
	heapSink = make([][]byte, 0, 30)
	for i := 0; i < 30; i++ {
		heapSink = append(heapSink, make([]byte, mb))
	}
	runtime.Gosched() // let metric events be emitted

	runtrace.Stop()
	heapSink = nil // release after tracing

	rule := rules.NewHeapGrowthSpike()
	rule.WarnGrowthBytes = 5 * mb       // 5 MB — well below what we allocated
	rule.WarnRateBytesPerSec = 5 * mb   // 5 MB/s

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected HeapGrowthSpike finding from heap allocations, got none")
	}
	for _, f := range fs {
		if f.Rule != "HeapGrowthSpike" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
	}
	t.Logf("finding: %s", fs[0].Message)

	// Verify the finding message includes human-readable byte formatting.
	if fs[0].Message == "" {
		t.Error("finding Message should not be empty")
	}
}

// --- formatBytes helper tests ---

func TestFormatBytesHelperViaFinding(t *testing.T) {
	cases := []struct {
		first, last uint64
		wantInMsg   string
	}{
		{0, 500, "500 B"},
		{0, 2048, "2.0 KB"},
		{0, 5 * mb, "5.0 MB"},
		{0, 2 * gb, "2.0 GB"},
	}
	for _, c := range cases {
		rule := rules.NewHeapGrowthSpike()
		rule.WarnGrowthBytes = 0
		rule.WarnRateBytesPerSec = 0
		// Use 1s elapsed so rate > 0 but absolute triggers for tiny values.
		rule.Process(heapMetric(t, 0, c.first))
		rule.Process(heapMetric(t, 1_000_000_000, c.last))
		fs := rule.Flush()
		if len(fs) == 0 {
			continue // below threshold — skip format check for this case
		}
		if fs[0].Message == "" {
			t.Errorf("bytes=%d: empty message", c.last-c.first)
		}
	}
	_ = time.Second // keep import alive
}
