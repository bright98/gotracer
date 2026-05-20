package rules_test

import (
	"bytes"
	"runtime"
	runtrace "runtime/trace"
	"strings"
	"testing"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/rules"
)

// --- helpers ---

// stwBegin returns a synthetic EventRangeBegin for a stop-the-world pause.
func stwBegin(t *testing.T, ts trace.Time) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.Range]{
		Time:      ts,
		Kind:      trace.EventRangeBegin,
		Goroutine: trace.GoID(1),
		Details: trace.Range{
			Name:  "stop-the-world (GC mark termination)",
			Scope: trace.MakeResourceID(trace.GoID(1)),
		},
	})
	if err != nil {
		t.Fatalf("MakeEvent(RangeBegin): %v", err)
	}
	return ev
}

// stwEnd returns a synthetic EventRangeEnd for a stop-the-world pause.
func stwEnd(t *testing.T, ts trace.Time) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.Range]{
		Time:      ts,
		Kind:      trace.EventRangeEnd,
		Goroutine: trace.GoID(1),
		Details: trace.Range{
			Name:  "stop-the-world (GC mark termination)",
			Scope: trace.MakeResourceID(trace.GoID(1)),
		},
	})
	if err != nil {
		t.Fatalf("MakeEvent(RangeEnd): %v", err)
	}
	return ev
}

// --- unit tests ---

func TestGCPauseSpikeNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewGCPauseSpike() // 1ms warn, 10ms error

	// 500µs pause — below the 1ms warn threshold.
	rule.Process(stwBegin(t, 0))
	fs := rule.Process(stwEnd(t, 500_000))

	if len(fs) != 0 {
		t.Errorf("expected no findings for 500µs pause, got %d", len(fs))
	}
}

func TestGCPauseSpikeWarnSeverity(t *testing.T) {
	rule := rules.NewGCPauseSpike()

	// 2ms pause — above warn (1ms), below error (10ms).
	rule.Process(stwBegin(t, 0))
	fs := rule.Process(stwEnd(t, 2_000_000))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 2ms pause, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGCPauseSpikeErrorSeverity(t *testing.T) {
	rule := rules.NewGCPauseSpike()

	// 15ms pause — above error threshold (10ms).
	rule.Process(stwBegin(t, 0))
	fs := rule.Process(stwEnd(t, 15_000_000))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 15ms pause, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestGCPauseSpikeCustomThresholds(t *testing.T) {
	rule := rules.NewGCPauseSpike()
	rule.WarnThreshold = 100 * time.Microsecond
	rule.ErrorThreshold = 500 * time.Microsecond

	// 300µs — above custom warn (100µs), below custom error (500µs).
	rule.Process(stwBegin(t, 0))
	fs := rule.Process(stwEnd(t, 300_000))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGCPauseSpikeMultiplePauses(t *testing.T) {
	rule := rules.NewGCPauseSpike()
	rule.WarnThreshold = 0 // catch everything

	var all []findings.Finding
	// First STW: 1ms
	rule.Process(stwBegin(t, 1_000_000))
	all = append(all, rule.Process(stwEnd(t, 2_000_000))...)
	// Second STW: 3ms
	rule.Process(stwBegin(t, 5_000_000))
	all = append(all, rule.Process(stwEnd(t, 8_000_000))...)

	if len(all) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(all))
	}
}

func TestGCPauseSpikeEndWithoutBeginIsIgnored(t *testing.T) {
	rule := rules.NewGCPauseSpike()
	rule.WarnThreshold = 0

	// RangeEnd without a preceding RangeBegin — simulates EventRangeActive at
	// trace start where we don't know the start time.
	fs := rule.Process(stwEnd(t, 5_000_000))
	if len(fs) != 0 {
		t.Errorf("expected no findings for orphan RangeEnd, got %d", len(fs))
	}
}

func TestGCPauseSpikeFlushReturnsNil(t *testing.T) {
	rule := rules.NewGCPauseSpike()
	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("Flush should return nil, got %v", fs)
	}
}

func TestGCPauseSpikeName(t *testing.T) {
	rule := rules.NewGCPauseSpike()
	if rule.Name() != "GCPauseSpike" {
		t.Errorf("Name() = %q, want %q", rule.Name(), "GCPauseSpike")
	}
}

func TestGCPauseSpikeMessageContainsReason(t *testing.T) {
	rule := rules.NewGCPauseSpike()

	rule.Process(stwBegin(t, 0))
	fs := rule.Process(stwEnd(t, 2_000_000))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if !strings.Contains(fs[0].Message, "GC mark termination") {
		t.Errorf("message should contain STW reason; got: %s", fs[0].Message)
	}
}

func TestGCPauseSpikeFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewGCPauseSpike()

	rule.Process(stwBegin(t, 0))
	fs := rule.Process(stwEnd(t, 2_000_000))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "GCPauseSpike" {
		t.Errorf("Rule = %q, want GCPauseSpike", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
}

// --- integration test ---

// gcSink prevents the compiler from optimising away allocations.
var gcSink []byte

// TestGCPauseSpikeEndToEnd generates a real trace in-process, runs it
// through the full analyzer pipeline, and verifies the rule fires.
// WarnThreshold is set to 0 so any pause — however short — produces a finding.
func TestGCPauseSpikeEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}
	for i := 0; i < 5; i++ {
		gcSink = make([]byte, 2<<20) // 2 MB — gives GC something to mark
		runtime.GC()
	}
	runtrace.Stop()

	rule := rules.NewGCPauseSpike()
	rule.WarnThreshold = 0 // any pause triggers a finding

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected findings from real GC activity, got none")
	}
	for _, f := range fs {
		if f.Rule != "GCPauseSpike" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
		if f.Message == "" {
			t.Error("finding Message should not be empty")
		}
	}
	t.Logf("found %d STW pause(s) in live trace", len(fs))
}
