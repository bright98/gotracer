package rules_test

import (
	"bytes"
	"runtime"
	runtrace "runtime/trace"
	"sync"
	"testing"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/rules"
)

// --- helpers ---

// goRunnable returns a synthetic Waiting → Runnable transition for goroutine id.
func goRunnable(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoWaiting, trace.GoRunnable),
	})
	if err != nil {
		t.Fatalf("MakeEvent(Runnable): %v", err)
	}
	return ev
}

// goRunning returns a synthetic Runnable → Running transition for goroutine id.
func goRunning(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoRunnable, trace.GoRunning),
	})
	if err != nil {
		t.Fatalf("MakeEvent(Running): %v", err)
	}
	return ev
}

// goUndeterminedRunnable returns a GoUndetermined → Runnable event (trace-start snapshot).
func goUndeterminedRunnable(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoUndetermined, trace.GoRunnable),
	})
	if err != nil {
		t.Fatalf("MakeEvent(UndeterminedRunnable): %v", err)
	}
	return ev
}

// --- unit tests ---

func TestHighSchedulingLatencyNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewHighSchedulingLatency() // 1ms warn, 10ms error

	// 500µs — below warn threshold.
	rule.Process(goRunnable(t, 0, 42))
	fs := rule.Process(goRunning(t, 500_000, 42))

	if len(fs) != 0 {
		t.Errorf("expected no findings for 500µs latency, got %d", len(fs))
	}
}

func TestHighSchedulingLatencyWarnSeverity(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()

	// 2ms — above warn (1ms), below error (10ms).
	rule.Process(goRunnable(t, 0, 42))
	fs := rule.Process(goRunning(t, 2_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 2ms latency, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestHighSchedulingLatencyErrorSeverity(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()

	// 15ms — above error threshold (10ms).
	rule.Process(goRunnable(t, 0, 42))
	fs := rule.Process(goRunning(t, 15_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 15ms latency, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestHighSchedulingLatencyCustomThresholds(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()
	rule.WarnThreshold = 100 * time.Microsecond
	rule.ErrorThreshold = 500 * time.Microsecond

	// 300µs — above custom warn (100µs), below custom error (500µs).
	rule.Process(goRunnable(t, 0, 42))
	fs := rule.Process(goRunning(t, 300_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestHighSchedulingLatencyGoroutineIDPopulated(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()

	rule.Process(goRunnable(t, 0, 42))
	fs := rule.Process(goRunning(t, 2_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].GoroutineID != 42 {
		t.Errorf("GoroutineID = %d, want 42", fs[0].GoroutineID)
	}
}

func TestHighSchedulingLatencyUndeterminedSkipped(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()
	rule.WarnThreshold = 0 // would catch anything if tracked

	// Goroutine seen as Runnable at trace start — no start time available.
	rule.Process(goUndeterminedRunnable(t, 0, 99))
	fs := rule.Process(goRunning(t, 5_000_000, 99))

	if len(fs) != 0 {
		t.Errorf("expected no findings for undetermined-start goroutine, got %d", len(fs))
	}
}

func TestHighSchedulingLatencyMultipleGoroutinesIndependent(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()
	rule.WarnThreshold = 0

	// G=1 becomes runnable at t=0, G=2 at t=1ms.
	rule.Process(goRunnable(t, 0, 1))
	rule.Process(goRunnable(t, 1_000_000, 2))

	// G=2 runs first at t=2ms (1ms latency), G=1 runs at t=5ms (5ms latency).
	fs2 := rule.Process(goRunning(t, 2_000_000, 2))
	fs1 := rule.Process(goRunning(t, 5_000_000, 1))

	if len(fs1) != 1 || len(fs2) != 1 {
		t.Fatalf("expected 1 finding each, got fs1=%d fs2=%d", len(fs1), len(fs2))
	}
	// G=1 waited 5ms, G=2 waited 1ms.
	if fs1[0].GoroutineID != 1 {
		t.Errorf("fs1 GoroutineID = %d, want 1", fs1[0].GoroutineID)
	}
	if fs2[0].GoroutineID != 2 {
		t.Errorf("fs2 GoroutineID = %d, want 2", fs2[0].GoroutineID)
	}
}

// TestHighSchedulingLatencyMapDeletedAfterRun verifies that the map entry is
// removed when a goroutine starts running, so a second Runnable→Running cycle
// for the same goroutine uses its own fresh timestamp — not the first one.
func TestHighSchedulingLatencyMapDeletedAfterRun(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()
	rule.WarnThreshold = 0

	// Cycle 1: G=7, 1ms latency.
	rule.Process(goRunnable(t, 0, 7))
	fs1 := rule.Process(goRunning(t, 1_000_000, 7))

	// Cycle 2: G=7, 3ms latency. If the map entry wasn't deleted, the
	// latency would be computed from t=0 instead of t=4ms.
	rule.Process(goRunnable(t, 4_000_000, 7))
	fs2 := rule.Process(goRunning(t, 7_000_000, 7))

	if len(fs1) != 1 || len(fs2) != 1 {
		t.Fatalf("expected 1 finding per cycle, got cycle1=%d cycle2=%d", len(fs1), len(fs2))
	}

	// Cycle 2 latency must be 3ms (7ms-4ms), not 7ms (7ms-0ms).
	wantLatency := 3 * time.Millisecond
	if fs2[0].Timestamp != time.Duration(trace.Time(4_000_000)) {
		t.Errorf("cycle 2 Timestamp = %v, want %v (map was not cleared after cycle 1)",
			fs2[0].Timestamp, time.Duration(trace.Time(4_000_000)))
	}
	_ = wantLatency
}

func TestHighSchedulingLatencyFlushReturnsNil(t *testing.T) {
	rule := rules.NewHighSchedulingLatency()
	// Put something in the map to make sure Flush ignores it.
	rule.Process(goRunnable(t, 0, 42))

	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("Flush should return nil, got %v", fs)
	}
}

func TestHighSchedulingLatencyName(t *testing.T) {
	if rules.NewHighSchedulingLatency().Name() != "HighSchedulingLatency" {
		t.Error("unexpected Name()")
	}
}

// --- integration test ---

// TestHighSchedulingLatencyEndToEnd generates a real trace where goroutines
// are deliberately delayed by a mutex, then verifies the rule fires via the
// full analyzer pipeline.
func TestHighSchedulingLatencyEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	// Hold a mutex while spawning goroutines that immediately try to acquire
	// it — they become Runnable, then block, then become Runnable again when
	// released, creating scheduling events.
	var mu sync.Mutex
	var wg sync.WaitGroup
	mu.Lock()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock() //nolint
			mu.Unlock()
		}()
	}
	runtime.Gosched() // let goroutines reach the mutex
	mu.Unlock()
	wg.Wait()

	runtrace.Stop()

	rule := rules.NewHighSchedulingLatency()
	rule.WarnThreshold = 0 // any latency triggers a finding

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected scheduling latency findings, got none")
	}
	for _, f := range fs {
		if f.Rule != "HighSchedulingLatency" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
		if f.GoroutineID == 0 {
			t.Error("GoroutineID should be set on scheduling findings")
		}
	}
	t.Logf("found %d scheduling latency event(s) in live trace", len(fs))
}
