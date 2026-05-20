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

// procGoesIdle returns a synthetic ProcRunning → ProcIdle transition.
// EventConfig.Proc must match Details.Resource.Proc() so MakeEvent encodes it
// as EvProcStop (natural idle) rather than EvProcSteal.
func procGoesIdle(t *testing.T, ts trace.Time, id trace.ProcID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:    ts,
		Kind:    trace.EventStateTransition,
		Proc:    id,
		Details: trace.MakeProcStateTransition(id, trace.ProcRunning, trace.ProcIdle),
	})
	if err != nil {
		t.Fatalf("MakeEvent(procGoesIdle): %v", err)
	}
	return ev
}

// procGoesRunning returns a synthetic ProcIdle → ProcRunning transition.
func procGoesRunning(t *testing.T, ts trace.Time, id trace.ProcID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:    ts,
		Kind:    trace.EventStateTransition,
		Proc:    id,
		Details: trace.MakeProcStateTransition(id, trace.ProcIdle, trace.ProcRunning),
	})
	if err != nil {
		t.Fatalf("MakeEvent(procGoesRunning): %v", err)
	}
	return ev
}

// procUndeterminedIdle returns a ProcUndetermined → ProcIdle event (P was
// already idle when the trace started).
func procUndeterminedIdle(t *testing.T, ts trace.Time, id trace.ProcID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:    ts,
		Kind:    trace.EventStateTransition,
		Proc:    id,
		Details: trace.MakeProcStateTransition(id, trace.ProcUndetermined, trace.ProcIdle),
	})
	if err != nil {
		t.Fatalf("MakeEvent(procUndeterminedIdle): %v", err)
	}
	return ev
}

// --- unit tests ---

func TestProcessorStarvationNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewProcessorStarvation() // 10ms warn, 100ms error

	// 5ms idle — below the 10ms warn threshold.
	rule.Process(procGoesIdle(t, 0, 1))
	fs := rule.Process(procGoesRunning(t, 5_000_000, 1))

	if len(fs) != 0 {
		t.Errorf("expected no findings for 5ms idle, got %d", len(fs))
	}
}

func TestProcessorStarvationWarnSeverity(t *testing.T) {
	rule := rules.NewProcessorStarvation()

	// 20ms idle — above warn (10ms), below error (100ms).
	rule.Process(procGoesIdle(t, 0, 1))
	fs := rule.Process(procGoesRunning(t, 20_000_000, 1))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 20ms idle, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestProcessorStarvationErrorSeverity(t *testing.T) {
	rule := rules.NewProcessorStarvation()

	// 150ms idle — above error threshold (100ms).
	rule.Process(procGoesIdle(t, 0, 1))
	fs := rule.Process(procGoesRunning(t, 150_000_000, 1))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 150ms idle, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestProcessorStarvationCustomThresholds(t *testing.T) {
	rule := rules.NewProcessorStarvation()
	rule.WarnThreshold = 1 * time.Millisecond
	rule.ErrorThreshold = 5 * time.Millisecond

	// 3ms — above custom warn (1ms), below custom error (5ms).
	rule.Process(procGoesIdle(t, 0, 1))
	fs := rule.Process(procGoesRunning(t, 3_000_000, 1))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestProcessorStarvationUndeterminedSkipped(t *testing.T) {
	rule := rules.NewProcessorStarvation()
	rule.WarnThreshold = 0

	// P was already idle at trace start — no start time to compute from.
	rule.Process(procUndeterminedIdle(t, 0, 1))
	fs := rule.Process(procGoesRunning(t, 50_000_000, 1))

	if len(fs) != 0 {
		t.Errorf("expected no findings for undetermined-start P, got %d", len(fs))
	}
}

func TestProcessorStarvationRunningWithoutIdleIgnored(t *testing.T) {
	rule := rules.NewProcessorStarvation()
	rule.WarnThreshold = 0

	// ProcRunning event with no preceding ProcIdle — should not panic or emit.
	fs := rule.Process(procGoesRunning(t, 50_000_000, 7))
	if len(fs) != 0 {
		t.Errorf("expected no findings for orphan running event, got %d", len(fs))
	}
}

func TestProcessorStarvationMapDeletedAfterRunning(t *testing.T) {
	rule := rules.NewProcessorStarvation()
	rule.WarnThreshold = 0

	// Cycle 1: P=2 idle at t=0, running at t=5ms.
	rule.Process(procGoesIdle(t, 0, 2))
	rule.Process(procGoesRunning(t, 5_000_000, 2))

	// Cycle 2: P=2 idle at t=10ms, running at t=13ms (3ms idle).
	// If the map entry from cycle 1 wasn't deleted, duration would be 13ms.
	rule.Process(procGoesIdle(t, 10_000_000, 2))
	fs2 := rule.Process(procGoesRunning(t, 13_000_000, 2))

	if len(fs2) != 1 {
		t.Fatalf("expected 1 finding for cycle 2, got %d", len(fs2))
	}
	if fs2[0].Timestamp != time.Duration(trace.Time(10_000_000)) {
		t.Errorf("cycle 2 Timestamp = %v, want %v (map not cleared after cycle 1)",
			fs2[0].Timestamp, time.Duration(trace.Time(10_000_000)))
	}
}

func TestProcessorStarvationMultiplePsIndependent(t *testing.T) {
	rule := rules.NewProcessorStarvation()
	rule.WarnThreshold = 0

	// P=1 goes idle at t=0, P=2 goes idle at t=5ms.
	rule.Process(procGoesIdle(t, 0, 1))
	rule.Process(procGoesIdle(t, 5_000_000, 2))

	// P=2 resumes at t=15ms (10ms idle), P=1 resumes at t=30ms (30ms idle).
	fs2 := rule.Process(procGoesRunning(t, 15_000_000, 2))
	fs1 := rule.Process(procGoesRunning(t, 30_000_000, 1))

	if len(fs1) != 1 || len(fs2) != 1 {
		t.Fatalf("expected 1 finding each, got fs1=%d fs2=%d", len(fs1), len(fs2))
	}
}

func TestProcessorStarvationFlushReturnsNil(t *testing.T) {
	rule := rules.NewProcessorStarvation()
	// P is idle at stream end — Flush should not emit for incomplete periods.
	rule.Process(procGoesIdle(t, 0, 1))

	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("Flush should return nil, got %v", fs)
	}
}

func TestProcessorStarvationName(t *testing.T) {
	if rules.NewProcessorStarvation().Name() != "ProcessorStarvation" {
		t.Error("unexpected Name()")
	}
}

func TestProcessorStarvationFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewProcessorStarvation()

	rule.Process(procGoesIdle(t, 0, 3))
	fs := rule.Process(procGoesRunning(t, 20_000_000, 3))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "ProcessorStarvation" {
		t.Errorf("Rule = %q, want ProcessorStarvation", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
	// ProcessorStarvation is a P-level finding; GoroutineID should be 0.
	if f.GoroutineID != 0 {
		t.Errorf("GoroutineID should be 0 for a P-level finding, got %d", f.GoroutineID)
	}
}

// --- integration test ---

// TestProcessorStarvationEndToEnd generates a real trace, then verifies the
// rule fires through the full analyzer pipeline.
//
// We use a brief sleep to drain the run queue and let Ps go idle, then spawn
// goroutines to bring them back. WarnThreshold is set to 0 so any idle
// duration — however short — triggers a finding.
func TestProcessorStarvationEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	// Let Ps go idle briefly by draining work, then resume with new goroutines.
	done := make(chan struct{})
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		go func() {
			time.Sleep(time.Millisecond) // briefly idle this goroutine's P
			done <- struct{}{}
		}()
	}
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		<-done
	}

	runtrace.Stop()

	rule := rules.NewProcessorStarvation()
	rule.WarnThreshold = 0 // any idle duration triggers a finding

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected ProcessorStarvation findings from P idle transitions, got none")
	}
	for _, f := range fs {
		if f.Rule != "ProcessorStarvation" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
	}
	t.Logf("found %d P idle event(s) in live trace", len(fs))
}
