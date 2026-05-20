package rules_test

import (
	"bytes"
	runtrace "runtime/trace"
	"testing"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/rules"
)

// --- helpers ---

// goCreate returns a synthetic GoNotExist → GoRunnable transition (new goroutine spawned).
func goCreate(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: trace.GoID(1), // the creating goroutine (not the one being created)
		Details:   trace.MakeGoStateTransition(id, trace.GoNotExist, trace.GoRunnable),
	})
	if err != nil {
		t.Fatalf("MakeEvent(goCreate): %v", err)
	}
	return ev
}

// goExit returns a synthetic GoRunning → GoNotExist transition (goroutine terminated).
func goExit(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoRunning, trace.GoNotExist),
	})
	if err != nil {
		t.Fatalf("MakeEvent(goExit): %v", err)
	}
	return ev
}

// goAtStart simulates a goroutine that was alive when the trace started
// (GoUndetermined → GoRunnable). Reuses the goUndeterminedRunnable helper
// defined in high_scheduling_latency_test.go.
func goAtStart(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	return goUndeterminedRunnable(t, ts, id)
}

// --- unit tests ---

func TestGoroutineLeakGrowthNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth() // warn at +100

	// Baseline of 200, 50 created, 10 exited → growth = +40, below warn (100).
	for i := 0; i < 200; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 50; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	for i := 0; i < 10; i++ {
		rule.Process(goExit(t, 2_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings for +40 growth, got %d", len(fs))
	}
}

func TestGoroutineLeakGrowthAbsoluteWarnThreshold(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()

	// Baseline 500, 150 created, 0 exited → growth = +150, above absolute warn (100).
	// Ratio = 150/500 = 30% — above relative warn (25%) too, still just Warn.
	for i := 0; i < 500; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 150; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for +150 growth, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGoroutineLeakGrowthAbsoluteErrorThreshold(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()

	// 600 created, 0 exited → growth = +600, above absolute error (500).
	for i := 0; i < 600; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for +600 growth, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestGoroutineLeakGrowthRelativeWarnThreshold(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()

	// Baseline 100, growth +30 → 30% ratio, above relative warn (25%).
	// Absolute growth (30) is below absolute warn (100) — ratio fires first.
	for i := 0; i < 100; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 30; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 30%% growth, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGoroutineLeakGrowthRelativeErrorThreshold(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()

	// Baseline 80, growth +80 → 100% ratio, exceeds relative error (100%).
	for i := 0; i < 80; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 80; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 100%% growth, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestGoroutineLeakGrowthCustomThresholds(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()
	rule.WarnThreshold = 10
	rule.ErrorThreshold = 50
	rule.WarnRatio = 0.05
	rule.ErrorRatio = 0.50

	// +20 goroutines from baseline 200: absolute 20 > warn 10; ratio 10% > warn 5%.
	for i := 0; i < 200; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 20; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGoroutineLeakGrowthNoFindingWhenBalanced(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()

	// 200 created, 200 exited → net growth = 0.
	for i := 0; i < 200; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	for i := 0; i < 200; i++ {
		rule.Process(goExit(t, 2_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings when goroutines are balanced, got %d", len(fs))
	}
}

func TestGoroutineLeakGrowthNoFindingWhenNegative(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()

	// More exiting than created (old goroutines cleaning up) — not a leak.
	for i := 0; i < 200; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 50; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	for i := 0; i < 100; i++ {
		rule.Process(goExit(t, 2_000_000_000, trace.GoID(i+10)))
	}
	// growth = 50 - 100 = -50
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings when goroutines are decreasing, got %d", len(fs))
	}
}

func TestGoroutineLeakGrowthNoEventsNoFinding(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()
	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("expected no findings with no events, got %d", len(fs))
	}
}

func TestGoroutineLeakGrowthNoBaselineRatioSkipped(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()
	rule.WarnThreshold = 1000 // high absolute threshold
	rule.WarnRatio = 0.01     // low ratio — but atStart = 0 so ratio is skipped

	// 50 created, 0 exited, no atStart goroutines.
	for i := 0; i < 50; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	// growth = 50, below absolute warn (1000); no ratio check because atStart = 0.
	fs := rule.Flush()

	if len(fs) != 0 {
		t.Errorf("expected no findings when atStart=0 and growth below absolute threshold, got %d", len(fs))
	}
}

func TestGoroutineLeakGrowthFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewGoroutineLeakGrowth()
	rule.WarnThreshold = 0

	for i := 0; i < 50; i++ {
		rule.Process(goAtStart(t, 0, trace.GoID(i+10)))
	}
	for i := 0; i < 20; i++ {
		rule.Process(goCreate(t, 1_000_000_000, trace.GoID(i+1000)))
	}
	fs := rule.Flush()

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "GoroutineLeakGrowth" {
		t.Errorf("Rule = %q, want GoroutineLeakGrowth", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
	if f.GoroutineID != 0 {
		t.Error("GoroutineID should be 0 — this is a process-level finding")
	}
}

func TestGoroutineLeakGrowthName(t *testing.T) {
	if rules.NewGoroutineLeakGrowth().Name() != "GoroutineLeakGrowth" {
		t.Error("unexpected Name()")
	}
}

// --- integration test ---

// TestGoroutineLeakGrowthEndToEnd generates a real trace where goroutines are
// spawned and kept alive throughout the trace window, then verifies the rule
// fires through the full analyzer pipeline.
//
// The goroutines block on a channel that is only closed after tracing stops,
// ensuring they are counted as created but never as exited within the trace.
func TestGoroutineLeakGrowthEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	block := make(chan struct{})
	for i := 0; i < 200; i++ {
		go func() { <-block }()
	}
	time.Sleep(10 * time.Millisecond) // let goroutines be created and traced

	runtrace.Stop()
	close(block) // let them exit after tracing is done

	rule := rules.NewGoroutineLeakGrowth()
	rule.WarnThreshold = 50 // lower than default to account for test overhead

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected GoroutineLeakGrowth finding from spawned goroutines, got none")
	}
	for _, f := range fs {
		if f.Rule != "GoroutineLeakGrowth" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
	}
	t.Logf("finding: %s", fs[0].Message)
}
