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

// --- HTML tests ---

func sampleFindings() []findings.Finding {
	return []findings.Finding{
		{
			Severity:    findings.Error,
			Rule:        "GCPauseSpike",
			Message:     "GC pause of 45ms exceeds Error threshold",
			Detail:      "A stop-the-world GC pause lasted 45ms.",
			Suggestion:  "Reduce allocation rate.",
			Timestamp:   1204 * time.Millisecond,
		},
		{
			Severity:    findings.Warn,
			Rule:        "ProcessorStarvation",
			Message:     "P 2 was idle for 12ms",
			Detail:      "Processor 2 sat idle for 12ms.",
			Suggestion:  "Check for thread exhaustion.",
			Timestamp:   850 * time.Millisecond,
			GoroutineID: 0,
		},
		{
			Severity:    findings.Info,
			Rule:        "HighSchedulingLatency",
			Message:     "goroutine 7 waited 2ms",
			Timestamp:   300 * time.Millisecond,
			GoroutineID: 7,
		},
	}
}

func sampleMeta() report.Meta {
	return report.Meta{
		Source:     "http://localhost:6060/debug/pprof/trace",
		CapturedAt: time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC),
		Duration:   5 * time.Second,
		RuleCount:  7,
	}
}

func TestWriteHTMLDocumentStructure(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, sampleFindings(), sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"<!DOCTYPE html>", "<html", "</html>", "<title>", "</head>", "<body>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestWriteHTMLNoFindingsShowsOKBadge(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, nil, sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "b-ok") {
		t.Error("expected green ok badge for no findings")
	}
	if strings.Contains(out, "<details>") {
		t.Error("expected no <details> elements when there are no findings")
	}
}

func TestWriteHTMLFindingFieldsPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, sampleFindings(), sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"GCPauseSpike",
		"GC pause of 45ms exceeds Error threshold",
		"A stop-the-world GC pause lasted 45ms.",
		"Reduce allocation rate.",
		"ProcessorStarvation",
		"HighSchedulingLatency",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestWriteHTMLSeverityClasses(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, sampleFindings(), sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"sev-error", "sev-warn", "sev-info"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing severity class %q", want)
		}
	}
}

func TestWriteHTMLSeverityCountBadges(t *testing.T) {
	var buf bytes.Buffer
	// 1 error, 1 warn, 1 info
	if err := report.WriteHTML(&buf, sampleFindings(), sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"1 ERROR", "1 WARN", "1 INFO"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing count badge %q", want)
		}
	}
}

func TestWriteHTMLMetaFieldsPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, nil, sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"http://localhost:6060/debug/pprof/trace",
		"2026-05-20 14:00:00 UTC",
		"5s",
		"rules: 7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing meta field %q", want)
		}
	}
}

func TestWriteHTMLCapturedAtZeroOmitted(t *testing.T) {
	meta := report.Meta{Source: "trace.out", RuleCount: 7} // zero CapturedAt
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, nil, meta); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	if strings.Contains(buf.String(), "captured:") {
		t.Error("zero CapturedAt should not render a 'captured:' field")
	}
}

func TestWriteHTMLDurationZeroOmitted(t *testing.T) {
	meta := report.Meta{Source: "trace.out", RuleCount: 7} // zero Duration
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, nil, meta); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	if strings.Contains(buf.String(), "duration:") {
		t.Error("zero Duration should not render a 'duration:' field")
	}
}

func TestWriteHTMLGoroutineIDRenderedWhenNonZero(t *testing.T) {
	fs := []findings.Finding{
		{Severity: findings.Info, Rule: "R", Message: "m", GoroutineID: 42, Timestamp: 1},
	}
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, fs, sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	if !strings.Contains(buf.String(), "42") {
		t.Error("expected GoroutineID 42 in output")
	}
}

func TestWriteHTMLGoroutineIDOmittedWhenZero(t *testing.T) {
	fs := []findings.Finding{
		{Severity: findings.Info, Rule: "R", Message: "m", GoroutineID: 0, Timestamp: 1},
	}
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, fs, sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	if strings.Contains(buf.String(), "goroutine") {
		t.Error("GoroutineID 0 should not render a goroutine row")
	}
}

func TestWriteHTMLEscapesSpecialChars(t *testing.T) {
	fs := []findings.Finding{
		{
			Severity: findings.Warn,
			Rule:     "R",
			Message:  "m",
			Detail:   `<script>alert("xss")</script>`,
			Timestamp: 1,
		},
	}
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, fs, sampleMeta()); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `<script>alert("xss")</script>`) {
		t.Error("raw XSS payload found unescaped in output — HTML escaping not working")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("expected &lt;script&gt; escaped entity in output")
	}
}

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
