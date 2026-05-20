package rules_test

import (
	"bytes"
	"os"
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

// goEnterSyscall returns a synthetic Running → GoSyscall transition.
func goEnterSyscall(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoRunning, trace.GoSyscall),
	})
	if err != nil {
		t.Fatalf("MakeEvent(EnterSyscall): %v", err)
	}
	return ev
}

// goExitSyscallRunning returns a synthetic GoSyscall → GoRunning transition
// (goroutine's P was available when the syscall returned).
func goExitSyscallRunning(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoSyscall, trace.GoRunning),
	})
	if err != nil {
		t.Fatalf("MakeEvent(ExitSyscallRunning): %v", err)
	}
	return ev
}

// goExitSyscallRunnable returns a synthetic GoSyscall → GoRunnable transition
// (P was stolen while the goroutine was blocked; it must be rescheduled).
func goExitSyscallRunnable(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoSyscall, trace.GoRunnable),
	})
	if err != nil {
		t.Fatalf("MakeEvent(ExitSyscallRunnable): %v", err)
	}
	return ev
}

// goUndeterminedSyscall returns a GoUndetermined → GoSyscall event
// (goroutine was already in a syscall when the trace started).
func goUndeterminedSyscall(t *testing.T, ts trace.Time, id trace.GoID) trace.Event {
	t.Helper()
	ev, err := trace.MakeEvent(trace.EventConfig[trace.StateTransition]{
		Time:      ts,
		Kind:      trace.EventStateTransition,
		Goroutine: id,
		Details:   trace.MakeGoStateTransition(id, trace.GoUndetermined, trace.GoSyscall),
	})
	if err != nil {
		t.Fatalf("MakeEvent(UndeterminedSyscall): %v", err)
	}
	return ev
}

// --- unit tests ---

func TestBlockedOnSyscallNoBelowWarnThreshold(t *testing.T) {
	rule := rules.NewBlockedOnSyscall() // 10ms warn, 100ms error

	// 5ms — below the 10ms warn threshold.
	rule.Process(goEnterSyscall(t, 0, 42))
	fs := rule.Process(goExitSyscallRunning(t, 5_000_000, 42))

	if len(fs) != 0 {
		t.Errorf("expected no findings for 5ms syscall, got %d", len(fs))
	}
}

func TestBlockedOnSyscallWarnSeverity(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()

	// 20ms — above warn (10ms), below error (100ms).
	rule.Process(goEnterSyscall(t, 0, 42))
	fs := rule.Process(goExitSyscallRunning(t, 20_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 20ms syscall, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestBlockedOnSyscallErrorSeverity(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()

	// 150ms — above error threshold (100ms).
	rule.Process(goEnterSyscall(t, 0, 42))
	fs := rule.Process(goExitSyscallRunning(t, 150_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding for 150ms syscall, got %d", len(fs))
	}
	if fs[0].Severity != findings.Error {
		t.Errorf("severity = %v, want Error", fs[0].Severity)
	}
}

func TestBlockedOnSyscallCustomThresholds(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 1 * time.Millisecond
	rule.ErrorThreshold = 5 * time.Millisecond

	// 3ms — above custom warn (1ms), below custom error (5ms).
	rule.Process(goEnterSyscall(t, 0, 42))
	fs := rule.Process(goExitSyscallRunning(t, 3_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].Severity != findings.Warn {
		t.Errorf("severity = %v, want Warn", fs[0].Severity)
	}
}

func TestBlockedOnSyscallGoroutineIDPopulated(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()

	rule.Process(goEnterSyscall(t, 0, 99))
	fs := rule.Process(goExitSyscallRunning(t, 20_000_000, 99))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	if fs[0].GoroutineID != 99 {
		t.Errorf("GoroutineID = %d, want 99", fs[0].GoroutineID)
	}
}

// TestBlockedOnSyscallExitToRunnableAlsoCounted verifies that the rule fires
// when the exit transition is GoSyscall→GoRunnable (P was stolen), not just
// GoSyscall→GoRunning.
func TestBlockedOnSyscallExitToRunnableAlsoCounted(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 0

	rule.Process(goEnterSyscall(t, 0, 42))
	fs := rule.Process(goExitSyscallRunnable(t, 5_000_000, 42))

	if len(fs) != 1 {
		t.Errorf("expected 1 finding on GoSyscall→GoRunnable exit, got %d", len(fs))
	}
}

func TestBlockedOnSyscallUndeterminedSkipped(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 0 // would catch anything if tracked

	// Goroutine was already in a syscall when the trace started — no start time.
	rule.Process(goUndeterminedSyscall(t, 0, 77))
	fs := rule.Process(goExitSyscallRunning(t, 50_000_000, 77))

	if len(fs) != 0 {
		t.Errorf("expected no findings for undetermined-start goroutine, got %d", len(fs))
	}
}

func TestBlockedOnSyscallExitWithoutEntryIgnored(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 0

	// Exit event with no preceding entry — should not panic or emit.
	fs := rule.Process(goExitSyscallRunning(t, 50_000_000, 55))
	if len(fs) != 0 {
		t.Errorf("expected no findings for orphan exit, got %d", len(fs))
	}
}

func TestBlockedOnSyscallMapDeletedAfterExit(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 0

	// Cycle 1: G=7, enters at t=0, exits at t=10ms.
	rule.Process(goEnterSyscall(t, 0, 7))
	rule.Process(goExitSyscallRunning(t, 10_000_000, 7))

	// Cycle 2: G=7, enters at t=20ms, exits at t=23ms (3ms).
	// If the map entry from cycle 1 wasn't deleted, the duration would be
	// computed from t=0 instead of t=20ms.
	rule.Process(goEnterSyscall(t, 20_000_000, 7))
	fs2 := rule.Process(goExitSyscallRunning(t, 23_000_000, 7))

	if len(fs2) != 1 {
		t.Fatalf("expected 1 finding for cycle 2, got %d", len(fs2))
	}
	if fs2[0].Timestamp != time.Duration(trace.Time(20_000_000)) {
		t.Errorf("cycle 2 Timestamp = %v, want %v (map was not cleared after cycle 1)",
			fs2[0].Timestamp, time.Duration(trace.Time(20_000_000)))
	}
}

func TestBlockedOnSyscallMultipleGoroutinesIndependent(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 0

	// G=1 enters syscall at t=0, G=2 enters at t=5ms.
	rule.Process(goEnterSyscall(t, 0, 1))
	rule.Process(goEnterSyscall(t, 5_000_000, 2))

	// G=2 exits at t=15ms (10ms in syscall), G=1 exits at t=30ms (30ms total).
	fs2 := rule.Process(goExitSyscallRunning(t, 15_000_000, 2))
	fs1 := rule.Process(goExitSyscallRunning(t, 30_000_000, 1))

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

func TestBlockedOnSyscallFlushReturnsNil(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()
	// Put something in the map — Flush should ignore it.
	rule.Process(goEnterSyscall(t, 0, 42))

	if fs := rule.Flush(); len(fs) != 0 {
		t.Errorf("Flush should return nil, got %v", fs)
	}
}

func TestBlockedOnSyscallName(t *testing.T) {
	if rules.NewBlockedOnSyscall().Name() != "BlockedOnSyscall" {
		t.Error("unexpected Name()")
	}
}

func TestBlockedOnSyscallFindingFieldsPopulated(t *testing.T) {
	rule := rules.NewBlockedOnSyscall()

	rule.Process(goEnterSyscall(t, 0, 42))
	fs := rule.Process(goExitSyscallRunning(t, 20_000_000, 42))

	if len(fs) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "BlockedOnSyscall" {
		t.Errorf("Rule = %q, want BlockedOnSyscall", f.Rule)
	}
	if f.Detail == "" {
		t.Error("Detail should not be empty")
	}
	if f.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
}

// --- integration test ---

// TestBlockedOnSyscallEndToEnd generates a real trace where goroutines make
// actual blocking syscalls via os.File.Write, then verifies the rule fires
// through the full analyzer pipeline.
//
// Writing to a regular file goes through the syscall package's
// entersyscall/exitsyscall wrappers (unlike network I/O, which goes through
// the netpoller and produces GoWaiting instead). WarnThreshold is set to 0 so
// any duration — however short — triggers a finding.
func TestBlockedOnSyscallEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	if err := runtrace.Start(&buf); err != nil {
		t.Fatalf("trace.Start: %v", err)
	}

	// Spawn goroutines that each write to a temp file — a real blocking syscall.
	done := make(chan struct{})
	payload := make([]byte, 4096)
	for i := 0; i < 5; i++ {
		go func() {
			f, err := os.CreateTemp("", "gotracer-syscall-test-*")
			if err != nil {
				done <- struct{}{}
				return
			}
			f.Write(payload) //nolint
			f.Close()
			os.Remove(f.Name())
			done <- struct{}{}
		}()
	}
	for i := 0; i < 5; i++ {
		<-done
	}
	runtime.Gosched()

	runtrace.Stop()

	rule := rules.NewBlockedOnSyscall()
	rule.WarnThreshold = 0 // any syscall duration triggers a finding

	a := analyzer.New(rule)
	fs, err := a.Run(&buf)
	if err != nil {
		t.Fatalf("analyzer.Run: %v", err)
	}
	if len(fs) == 0 {
		t.Fatal("expected BlockedOnSyscall findings from real syscalls, got none")
	}
	for _, f := range fs {
		if f.Rule != "BlockedOnSyscall" {
			t.Errorf("unexpected rule %q", f.Rule)
		}
		if f.GoroutineID == 0 {
			t.Error("GoroutineID should be set on syscall findings")
		}
	}
	t.Logf("found %d syscall event(s) in live trace", len(fs))
}
