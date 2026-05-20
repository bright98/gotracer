package rules

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"

	"github.com/bright98/gotracer/findings"
)

// Default thresholds for BlockedOnSyscall.
// 10ms covers most legitimate I/O; 100ms is definitively stalling requests.
const (
	DefaultSyscallWarnThreshold  = 10 * time.Millisecond
	DefaultSyscallErrorThreshold = 100 * time.Millisecond
)

// BlockedOnSyscall detects goroutines that spend excessive time blocked in a
// system call.
//
// When a goroutine makes a blocking syscall the runtime parks its P so other
// goroutines can keep running. If the syscall takes too long — waiting on a
// slow disk, a locked file, or a blocking CGo call — any request the goroutine
// was serving is stalled for exactly that duration.
//
// Note: most Go network I/O does NOT appear here. The runtime's netpoller
// handles sockets in non-blocking mode; those goroutines enter GoWaiting, not
// GoSyscall. This rule catches raw file I/O, direct syscall.Syscall calls, and
// CGo that falls outside the netpoller.
//
// Thresholds can be tuned after construction:
//
//	rule := rules.NewBlockedOnSyscall()
//	rule.WarnThreshold = 5 * time.Millisecond
type BlockedOnSyscall struct {
	WarnThreshold  time.Duration
	ErrorThreshold time.Duration

	// syscallSince maps each goroutine that entered GoSyscall to the time it
	// did so. The entry is deleted when the goroutine leaves GoSyscall, so the
	// map never holds stale entries.
	syscallSince map[trace.GoID]trace.Time
}

// NewBlockedOnSyscall creates a BlockedOnSyscall rule with default thresholds.
func NewBlockedOnSyscall() *BlockedOnSyscall {
	return &BlockedOnSyscall{
		WarnThreshold:  DefaultSyscallWarnThreshold,
		ErrorThreshold: DefaultSyscallErrorThreshold,
		syscallSince:   make(map[trace.GoID]trace.Time),
	}
}

// Name implements findings.Rule.
func (r *BlockedOnSyscall) Name() string { return "BlockedOnSyscall" }

// Process implements findings.Rule. It records the moment a goroutine enters
// GoSyscall and emits a finding when it exits — to either GoRunning (P
// retained) or GoRunnable (P was stolen while blocked) — if the duration
// exceeds a threshold.
func (r *BlockedOnSyscall) Process(ev trace.Event) []findings.Finding {
	if ev.Kind() != trace.EventStateTransition {
		return nil
	}
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return nil
	}

	id := st.Resource.Goroutine()
	from, to := st.Goroutine()

	switch {
	case to == trace.GoSyscall:
		// Only track goroutines whose syscall entry we witnessed from a known
		// state. GoUndetermined means the goroutine was already in a syscall
		// when the trace started — we have no start time for it.
		if from != trace.GoUndetermined {
			r.syscallSince[id] = ev.Time()
		}

	case from == trace.GoSyscall:
		// Both GoSyscall→GoRunning and GoSyscall→GoRunnable mean the syscall
		// is done; the difference is just whether the runtime found a free P
		// immediately or had to re-queue the goroutine.
		if since, ok := r.syscallSince[id]; ok {
			duration := ev.Time().Sub(since)
			delete(r.syscallSince, id)
			return r.evaluate(id, duration, time.Duration(since))
		}
	}

	return nil
}

// Flush implements findings.Rule. A goroutine still in GoSyscall at stream end
// has not returned yet; without a complete duration we emit nothing.
func (r *BlockedOnSyscall) Flush() []findings.Finding { return nil }

func (r *BlockedOnSyscall) evaluate(id trace.GoID, duration, timestamp time.Duration) []findings.Finding {
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
		Severity:    sev,
		Rule:        r.Name(),
		Message:     fmt.Sprintf("goroutine %d blocked in syscall for %v (threshold: %v)", id, d, threshold),
		Detail:      fmt.Sprintf("Goroutine %d spent %v blocked in a system call. Any request it was handling stalled for this duration.", id, d),
		Suggestion:  "Check for slow disk I/O, network reads without deadlines, or blocking CGo calls. Add context deadlines to I/O operations and prefer non-blocking alternatives.",
		Timestamp:   timestamp,
		GoroutineID: uint64(id),
	}}
}
