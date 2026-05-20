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

// goBlock returns a synthetic GoRunning → GoWaiting transition with the given
// block reason. For mutex contention the reason must be "sync".
func goBlock(t *testing.T, ts trace.Time, id trace.GoID, reason string) trace.Event {
	t.Helper()
	st := trace.MakeGoStateTransition(id, trace.GoRunning, trace.GoWaiting)
	st.Reason = reason
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   st,
	})
	if err != nil {
		t.Fatalf("MakeEvent(Block): %v", err)
	}
	return ev
}

// goUnblock returns a synthetic GoWaiting → GoRunnable transition (mutex
// holder called Unlock, waking this goroutine).
func goUnblock(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoWaiting, trace.GoRunnable),
	})
	if err != nil {
		t.Fatalf("MakeEvent(Unblock): %v", err)
	}
	return ev
}

// goUndeterminedWaiting returns a GoUndetermined → GoWaiting event (goroutine
// was already waiting on a mutex when the trace started).
func goUndeterminedWaiting(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	// Reason is not encoded for status events — the rule must not track these.
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoUndetermined, trace.GoWaiting),
	})
	if err != nil {
		t.Fatalf("MakeEvent(UndeterminedWaiting): %v", err)
	}
	return ev
}

// --- unit tests ---

func TestMutexContentionNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewMutexContention() // 1ms warn, 10ms error

	// 500µs — below the 1ms warn threshold.
	rule.Process(goBlock(t, 0, 42, "sync"))
	fs := rule.Process(goUnblock(t, 500_000, 42))

	if len(fs) != 0 {
		t.Errorf("expected no findings for 500µs wait, got %d", len(fs))
	}
}

func TestMutexContentionWarnSeverity(t *testing.T) {
	rule := rules.NewMutexContention()

	// 2ms — above warn (1ms), below error (10ms).
	rule.Process(goBlock(t, 0, 42, "sync"))
	fs := rule.Process(goUnblock(t, 2_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 2ms wait, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestMutexContentionErrorSeverity(t *testing.T) {
	rule := rules.NewMutexContention()

	// 15ms — above error threshold (10ms).
	rule.Process(goBlock(t, 0, 42, "sync"))
	fs := rule.Process(goUnblock(t, 15_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 15ms wait, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestMutexContentionCustomThresholds(t *testing.T) {
	rule := rules.NewMutexContention()
	rule.WarnThreshold = 100 * time.Microsecond
	rule.ErrorThreshold = 500 * time.Microsecond

	// 300µs — above custom warn (100µs), below custom error (500µs).
	rule.Process(goBlock(t, 0, 42, "sync"))
	fs := rule.Process(goUnblock(t, 300_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestMutexContentionGoroutineIDPopulated(t *testing.T) {
	rule := rules.NewMutexContention()

	rule.Process(goBlock(t, 0, 99, "sync"))
	fs := rule.Process(goUnblock(t, 2_000_000, 99))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].GoroutineID != 99 {
		t.Errorf("GoroutineID = %d, want 99", fs[0].GoroutineID)
	}
}

// TestMutexContentionNonSyncReasonIgnored verifies that goroutines entering
// GoWaiting for non-mutex reasons (channel, sleep, etc.) are not tracked.
func TestMutexContentionNonSyncReasonIgnored(t *testing.T) {
	rule := rules.NewMutexContention()
	rule.WarnThreshold = 0 // would catch anything if tracked

	for _, reason := range []string{"chan receive", "chan send", "sleep", "network", "select"} {
		rule.Process(goBlock(t, 0, 42, reason))
		fs := rule.Process(goUnblock(t, 50_000_000, 42))
		if len(fs) != 0 {
			t.Errorf("reason %q: expected no findings, got %d", reason, len(fs))
		}
	}
}

func TestMutexContentionUndeterminedSkipped(t *testing.T) {
	rule := rules.NewMutexContention()
	rule.WarnThreshold = 0

	// Goroutine was already waiting when the trace started — no start time.
	rule.Process(goUndeterminedWaiting(t, 0, 77))
	fs := rule.Process(goUnblock(t, 50_000_000, 77))

	if len(fs) != 0 {
		t.Errorf("expected no findings for undetermined-start goroutine, got %d", len(fs))
	}
}

func TestMutexContentionUnblockWithoutBlockIgnored(t *testing.T) {
	rule := rules.NewMutexContention()
	rule.WarnThreshold = 0

	// Unblock without a preceding block — should not panic or emit.
	fs := rule.Process(goUnblock(t, 50_000_000, 55))
	if len(fs) != 0 {
		t.Errorf("expected no findings for orphan unblock, got %d", len(fs))
	}
}

func TestMutexContentionMapDeletedAfterUnblock(t *testing.T) {
	rule := rules.NewMutexContention()
	rule.WarnThreshold = 0

	// Cycle 1: G=7, blocks at t=0, unblocked at t=5ms.
	rule.Process(goBlock(t, 0, 7, "sync"))
	rule.Process(goUnblock(t, 5_000_000, 7))

	// Cycle 2: G=7, blocks at t=10ms, unblocked at t=13ms (3ms).
	// If the map entry from cycle 1 wasn't deleted, duration would be 13ms.
	rule.Process(goBlock(t, 10_000_000, 7, "sync"))
	fs2 := rule.Process(goUnblock(t, 13_000_000, 7))

	if len(fs2) != 1 {
		t.Fatalf("expected 1 finding for cycle 2, got %d", len(fs2))
	}
	if fs2[0].Timestamp != time.Duration(trace.Time(10_000_000)) {
		t.Errorf("cycle 2 Timestamp = %v, want %v (map was not cleared after cycle 1)",
			fs2[0].Timestamp, time.Duration(trace.Time(10_000_000)))
	}
}

func TestMutexContentionMultipleGoroutinesIndependent(t *testing.T) {
	rule := rules.NewMutexContention()
	rule.WarnThreshold = 0

	// G=1 blocks at t=0, G=2 blocks at t=2ms.
	rule.Process(goBlock(t, 0, 1, "sync"))
	rule.Process(goBlock(t, 2_000_000, 2, "sync"))

	// G=2 unblocked at t=5ms (3ms wait), G=1 unblocked at t=9ms (9ms wait).
	fs2 := rule.Process(goUnblock(t, 5_000_000, 2))
	fs1 := rule.Process(goUnblock(t, 9_000_000, 1))

	if len(fs1) != 1 || len(fs2) != 1 {
		t.Fatalf("expected 1 finding each, got fs1=%d fs2=%d", len(fs1), len(fs2))
	}
	if fs1[0].GoroutineID != 1 {
		t.Errorf("fs1 GoroutineID = %d, want 1", fs1[0].GoroutineID)
	}
	if fs2[0].GoroutineID != 2 {
		t.Errorf("fs2 GoroutineID = %d, want 2", fs2[0].GoroutineID)
	}
}

func TestMutexContentionFlushReturnsNil(t *testing.T) {
	rule := rules.NewMutexContention()
	// Put something in the map — Flush should ignore it.
	rule.Process(goBlock(t, 0, 42, "sync"))

	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("Flush should return nil, got %v", fs)
	}
}

func TestMutexContentionName(t *testing.T) {
	if rules.NewMutexContention().Name() != "MutexContention" {
		t.Error("unexpected Name()")
	}
}

func TestMutexContentionFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewMutexContention()

	rule.Process(goBlock(t, 0, 42, "sync"))
	fs := rule.Process(goUnblock(t, 2_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "MutexContention" {
		t.Errorf("Rule = %q, want MutexContention", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
}

// --- integration test ---

// TestMutexContentionEndToEnd generates a real trace where goroutines contend
// on a sync.Mutex, then verifies the rule fires through the full analyzer pipeline.
//
// The lock is held for 5ms so contending goroutines have time to actually park
// (enter GoWaiting) rather than spinning. WarnThreshold is set to 0 so any
// contention duration triggers a finding.
func TestMutexContentionEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	var mu sync.Mutex
	done := make(chan struct{})

	mu.Lock()
	for i := 0; i < 5; i++ {
		go func() {
			mu.Lock() //nolint — goroutines park here while mu is held
			mu.Unlock()
			done <- struct{}{}
		}()
	}
	runtime.Gosched()                // let goroutines reach the mutex and park
	time.Sleep(5 * time.Millisecond) // hold long enough for goroutines to enter GoWaiting
	mu.Unlock()

	for i := 0; i < 5; i++ {
		<-done
	}

	runtrace.Stop()

	rule := rules.NewMutexContention()
	rule.WarnThreshold = 0 // any contention duration triggers a finding

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected MutexContention findings from real contention, got none")
	}
	for _, f := range fs {
		if f.Rule != "MutexContention" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
		if f.GoroutineID == 0 {
			t.Error("GoroutineID should be set on mutex contention findings")
		}
	}
	t.Logf("found %d mutex contention event(s) in live trace", len(fs))
}
