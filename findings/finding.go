// Package findings defines the core types shared across all gotracer packages:
// the Finding struct, Severity levels, and the Rule interface that every
// analysis rule must implement.
package findings

import (
	"fmt"
	"time"

	"golang.org/x/exp/trace"
)

// Severity indicates the impact level of a finding.
type Severity int

const (
	Info  Severity = iota // informational, no action needed
	Warn                  // warrants investigation
	Error                 // likely causing user-visible problems
)

// String returns the uppercase label used in output formatting.
func (s Severity) String() string {
	switch s {
	case Info:
		return "INFO"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// MarshalText implements encoding.TextMarshaler so that JSON output
// encodes Severity as a string ("INFO", "WARN", "ERROR") rather than an int.
func (s Severity) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler so that Severity
// round-trips correctly through JSON.
func (s *Severity) UnmarshalText(text []byte) error {
	switch string(text) {
	case "INFO":
		*s = Info
	case "WARN":
		*s = Warn
	case "ERROR":
		*s = Error
	default:
		return fmt.Errorf("unknown severity %q", string(text))
	}
	return nil
}

// Finding is a single analysis result produced by a Rule.
type Finding struct {
	Severity    Severity      `json:"severity"`
	Rule        string        `json:"rule"`
	Message     string        `json:"message"`
	Detail      string        `json:"detail,omitempty"`
	Suggestion  string        `json:"suggestion,omitempty"`
	Timestamp   time.Duration `json:"timestamp_ns"`
	GoroutineID uint64        `json:"goroutine_id,omitempty"` // 0 = process-level finding
	Stack       []string      `json:"stack,omitempty"`
	Count       int           `json:"count,omitempty"` // >1 when this finding represents multiple occurrences
}

// Rule is the interface every analysis rule must implement.
//
// The analyzer calls Process for every event in the trace stream, in arrival
// order. Stateless rules return findings directly from Process. Stateful rules
// (e.g. goroutine leak detection) accumulate state across events and emit
// findings from Flush, which is called once after the stream ends.
type Rule interface {
	Name() string
	Process(ev trace.Event) []Finding
	Flush() []Finding
}
