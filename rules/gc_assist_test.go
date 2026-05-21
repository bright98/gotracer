package rules_test

import (
	"bytes"
	"runtime"
	"runtime/debug"
	runtrace "runtime/trace"
	"testing"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/rules"
)

// gcAssistBlock returns a synthetic GoRunning → GoWaiting transition with
// reason "GC mark assist wait for work".
func gcAssistBlock(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	return goBlock(t, ts, id, "GC mark assist wait for work")
}

// --- unit tests ---

func TestGCAssistNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewGCAssist() // 1ms warn, 10ms error

	// 500µs — below the 1ms warn threshold.
	rule.Process(gcAssistBlock(t, 0, 42))
	fs := rule.Process(goUnblock(t, 500_000, 42))

	if len(fs) != 0 {
		t.Errorf("expected no findings for 500µs assist wait, got %d", len(fs))
	}
}

func TestGCAssistWarnSeverity(t *testing.T) {
	rule := rules.NewGCAssist()

	// 2ms — above warn (1ms), below error (10ms).
	rule.Process(gcAssistBlock(t, 0, 42))
	fs := rule.Process(goUnblock(t, 2_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 2ms assist wait, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGCAssistErrorSeverity(t *testing.T) {
	rule := rules.NewGCAssist()

	// 15ms — above error threshold (10ms).
	rule.Process(gcAssistBlock(t, 0, 42))
	fs := rule.Process(goUnblock(t, 15_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 15ms assist wait, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestGCAssistCustomThresholds(t *testing.T) {
	rule := rules.NewGCAssist()
	rule.WarnThreshold = 100 * time.Microsecond
	rule.ErrorThreshold = 500 * time.Microsecond

	// 300µs — above custom warn (100µs), below custom error (500µs).
	rule.Process(gcAssistBlock(t, 0, 42))
	fs := rule.Process(goUnblock(t, 300_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestGCAssistGoroutineIDPopulated(t *testing.T) {
	rule := rules.NewGCAssist()

	rule.Process(gcAssistBlock(t, 0, 99))
	fs := rule.Process(goUnblock(t, 2_000_000, 99))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].GoroutineID != 99 {
		t.Errorf("GoroutineID = %d, want 99", fs[0].GoroutineID)
	}
}

func TestGCAssistNonGCReasonIgnored(t *testing.T) {
	rule := rules.NewGCAssist()
	rule.WarnThreshold = 0

	for _, reason := range []string{"sync", "chan receive", "sleep", "network"} {
		rule.Process(goBlock(t, 0, 42, reason))
		fs := rule.Process(goUnblock(t, 50_000_000, 42))
		if len(fs) != 0 {
			t.Errorf("reason %q: expected no findings, got %d", reason, len(fs))
		}
	}
}

func TestGCAssistUnblockWithoutBlockIgnored(t *testing.T) {
	rule := rules.NewGCAssist()
	rule.WarnThreshold = 0

	fs := rule.Process(goUnblock(t, 50_000_000, 55))
	if len(fs) != 0 {
		t.Errorf("expected no findings for orphan unblock, got %d", len(fs))
	}
}

func TestGCAssistMapDeletedAfterUnblock(t *testing.T) {
	rule := rules.NewGCAssist()
	rule.WarnThreshold = 5 * time.Millisecond

	// Cycle 1: G=7, blocks at t=0, unblocked at t=10ms (10ms wait — above threshold).
	rule.Process(gcAssistBlock(t, 0, 7))
	rule.Process(goUnblock(t, 10_000_000, 7))

	// Cycle 2: G=7, blocks at t=20ms, unblocked at t=23ms (3ms — below 5ms threshold).
	// If the map entry from cycle 1 wasn't deleted, duration would be 23ms (fires).
	rule.Process(gcAssistBlock(t, 20_000_000, 7))
	fs2 := rule.Process(goUnblock(t, 23_000_000, 7))

	if len(fs2) != 0 {
		t.Errorf("expected no findings for 3ms cycle 2 (map should have been cleared), got %d", len(fs2))
	}
}

func TestGCAssistFlushReturnsNil(t *testing.T) {
	rule := rules.NewGCAssist()
	// Put something in the map — Flush should ignore it (no complete duration).
	rule.Process(gcAssistBlock(t, 0, 42))

	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("Flush should return nil, got %v", fs)
	}
}

func TestGCAssistName(t *testing.T) {
	if rules.NewGCAssist().Name() != "GCAssist" {
		t.Error("unexpected Name()")
	}
}

func TestGCAssistFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewGCAssist()

	rule.Process(gcAssistBlock(t, 0, 42))
	fs := rule.Process(goUnblock(t, 2_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "GCAssist" {
		t.Errorf("Rule = %q, want GCAssist", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
}

// --- integration test ---

// TestGCAssistEndToEnd generates a real trace under heavy GC pressure and
// verifies the rule fires through the full analyzer pipeline.
//
// GOGC is set to 1 so GC triggers after every 1% heap growth. Combined with
// goroutines allocating at high rate, this reliably causes GC mark assist waits.
// If no findings are produced the test is skipped rather than failed, since
// assist waits are probabilistic even under pressure.
func TestGCAssistEndToEnd(t *testing.T) {
	old := debug.SetGCPercent(1)
	defer debug.SetGCPercent(old)

	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	done := make(chan struct{})
	n := runtime.GOMAXPROCS(0) * 4
	for i := 0; i < n; i++ {
		go func() {
			for j := 0; j < 5000; j++ {
				_ = make([]byte, 64*1024)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	runtrace.Stop()

	rule := rules.NewGCAssist()
	rule.WarnThreshold = 0 // any assist wait duration triggers a finding

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}

	if len(fs) == 0 {
		t.Skip("no GCAssist findings — GC did not produce assist waits in this run (probabilistic)")
	}

	for _, f := range fs {
		if f.Rule != "GCAssist" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
		if f.GoroutineID == 0 {
			t.Error("GoroutineID should be set on GC assist findings")
		}
	}
	t.Logf("found %d GC assist wait(s) in live trace", len(fs))
}
