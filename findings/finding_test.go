package findings_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bright98/gotracer/findings"
)

func TestSeverityString(t *testing.T) {
	cases := []struct {
		s    findings.Severity
		want string
	}{
		{findings.Info, "INFO"},
		{findings.Warn, "WARN"},
		{findings.Error, "ERROR"},
		{findings.Severity(99), "UNKNOWN"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Severity(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestSeverityJSONRoundTrip(t *testing.T) {
	for _, s := range []findings.Severity{findings.Info, findings.Warn, findings.Error} {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", s, err)
		}
		var got findings.Severity
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		if got != s {
			t.Errorf("round-trip: got %v, want %v", got, s)
		}
	}
}

func TestSeverityUnmarshalUnknown(t *testing.T) {
	var s findings.Severity
	if err := json.Unmarshal([]byte(`"CRITICAL"`), &s); err == nil {
		t.Error("expected error for unknown severity, got nil")
	}
}

// TestFindingJSONSeverityIsString verifies that Severity encodes as a string,
// not the underlying int. This matters for any consumer reading the JSON output.
func TestFindingJSONSeverityIsString(t *testing.T) {
	f := findings.Finding{
		Severity:  findings.Warn,
		Rule:      "GCPauseSpike",
		Message:   "GC STW pause exceeded threshold",
		Timestamp: 500 * time.Millisecond,
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}

	if got := raw["severity"]; got != "WARN" {
		t.Errorf("severity = %v (%T), want string \"WARN\"", got, got)
	}
}

func TestFindingJSONTimestampIsNanoseconds(t *testing.T) {
	f := findings.Finding{
		Severity:  findings.Info,
		Rule:      "R",
		Message:   "m",
		Timestamp: 1234 * time.Millisecond,
	}

	data, _ := json.Marshal(f)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	wantNs := float64((1234 * time.Millisecond).Nanoseconds())
	if got := raw["timestamp_ns"]; got != wantNs {
		t.Errorf("timestamp_ns = %v, want %v", got, wantNs)
	}
}

// TestFindingJSONOmitsZeroAndEmptyFields verifies that zero-value optional
// fields are omitted from JSON so consumers don't see noise.
func TestFindingJSONOmitsZeroAndEmptyFields(t *testing.T) {
	f := findings.Finding{
		Severity: findings.Info,
		Rule:     "R",
		Message:  "m",
		// GoroutineID zero, Detail/Suggestion/Stack empty
	}

	data, _ := json.Marshal(f)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	for _, field := range []string{"goroutine_id", "detail", "suggestion", "stack"} {
		if _, ok := raw[field]; ok {
			t.Errorf("field %q should be omitted when zero/empty", field)
		}
	}
}

func TestFindingJSONRoundTrip(t *testing.T) {
	orig := findings.Finding{
		Severity:    findings.Error,
		Rule:        "MutexContention",
		Message:     "high mutex wait time",
		Detail:      "goroutine 42 waited 200ms",
		Suggestion:  "reduce lock scope",
		Timestamp:   2 * time.Second,
		GoroutineID: 42,
		Stack:       []string{"main.handler", "sync.(*Mutex).Lock"},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got findings.Finding
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Severity != orig.Severity {
		t.Errorf("Severity: got %v, want %v", got.Severity, orig.Severity)
	}
	if got.Rule != orig.Rule {
		t.Errorf("Rule: got %q, want %q", got.Rule, orig.Rule)
	}
	if got.GoroutineID != orig.GoroutineID {
		t.Errorf("GoroutineID: got %d, want %d", got.GoroutineID, orig.GoroutineID)
	}
	if got.Timestamp != orig.Timestamp {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, orig.Timestamp)
	}
	if len(got.Stack) != len(orig.Stack) || got.Stack[0] != orig.Stack[0] {
		t.Errorf("Stack: got %v, want %v", got.Stack, orig.Stack)
	}
}
