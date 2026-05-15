package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/report"
)

func TestPrintHumanNoFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Print(&buf, nil, report.FormatHuman); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if got := buf.String(); got != "no findings\n" {
		t.Errorf("got %q, want %q", got, "no findings\n")
	}
}

func TestPrintHumanEmptySlice(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Print(&buf, []findings.Finding{}, report.FormatHuman); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if got := buf.String(); got != "no findings\n" {
		t.Errorf("got %q, want %q", got, "no findings\n")
	}
}

func TestPrintHumanContainsExpectedFields(t *testing.T) {
	fs := []findings.Finding{
		{
			Severity:  findings.Warn,
			Rule:      "GCPauseSpike",
			Message:   "GC STW pause exceeded threshold",
			Timestamp: 500 * time.Millisecond,
		},
		{
			Severity:  findings.Error,
			Rule:      "HighSchedulingLatency",
			Message:   "goroutine waited 50ms to be scheduled",
			Timestamp: 1200 * time.Millisecond,
		},
	}

	var buf bytes.Buffer
	if err := report.Print(&buf, fs, report.FormatHuman); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"SEVERITY", "RULE", "TIMESTAMP", "MESSAGE", // header
		"WARN", "GCPauseSpike", "GC STW pause exceeded threshold",
		"ERROR", "HighSchedulingLatency",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestPrintHumanUnknownFormatFallsBackToHuman(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Print(&buf, nil, "totally-unknown"); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(buf.String(), "no findings") {
		t.Errorf("expected human fallback output, got: %s", buf.String())
	}
}

func TestPrintJSONOneLinePerFinding(t *testing.T) {
	fs := []findings.Finding{
		{Severity: findings.Info, Rule: "A", Message: "msg a", Timestamp: 1 * time.Second},
		{Severity: findings.Warn, Rule: "B", Message: "msg b", Timestamp: 2 * time.Second},
		{Severity: findings.Error, Rule: "C", Message: "msg c", Timestamp: 3 * time.Second},
	}

	var buf bytes.Buffer
	if err := report.Print(&buf, fs, report.FormatJSON); err != nil {
		t.Fatalf("Print: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(fs) {
		t.Fatalf("got %d lines, want %d\noutput:\n%s", len(lines), len(fs), buf.String())
	}

	// Each line must be valid JSON.
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %s", i, line)
		}
	}
}

func TestPrintJSONContent(t *testing.T) {
	fs := []findings.Finding{
		{
			Severity:    findings.Error,
			Rule:        "MutexContention",
			Message:     "high mutex wait time",
			Detail:      "goroutine 7 waited 200ms",
			GoroutineID: 7,
			Timestamp:   200 * time.Millisecond,
		},
	}

	var buf bytes.Buffer
	if err := report.Print(&buf, fs, report.FormatJSON); err != nil {
		t.Fatalf("Print: %v", err)
	}

	var got findings.Finding
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Severity != findings.Error {
		t.Errorf("Severity = %v, want Error", got.Severity)
	}
	if got.Rule != "MutexContention" {
		t.Errorf("Rule = %q, want MutexContention", got.Rule)
	}
	if got.GoroutineID != 7 {
		t.Errorf("GoroutineID = %d, want 7", got.GoroutineID)
	}
	if got.Timestamp != 200*time.Millisecond {
		t.Errorf("Timestamp = %v, want 200ms", got.Timestamp)
	}
}

func TestPrintJSONNoFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Print(&buf, nil, report.FormatJSON); err != nil {
		t.Fatalf("Print: %v", err)
	}
	// Empty input → empty output (no "no findings" text in JSON mode).
	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Errorf("expected empty output for no findings in JSON mode, got %q", got)
	}
}
