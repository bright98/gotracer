package findings_test

import (
	"testing"

	"github.com/bright98/gotracer/findings"
)

func TestTopNZeroMeansNoLimit(t *testing.T) {
	fs := make([]findings.Finding, 5)
	for i := range fs {
		fs[i] = findings.Finding{Rule: "R"}
	}
	if got := findings.TopN(fs, 0); len(got) != 5 {
		t.Errorf("TopN(0) = %d findings, want 5", len(got))
	}
}

func TestTopNCapPerRule(t *testing.T) {
	fs := []findings.Finding{
		{Rule: "A", Severity: findings.Error},
		{Rule: "A", Severity: findings.Warn},
		{Rule: "A", Severity: findings.Info},
		{Rule: "B", Severity: findings.Warn},
		{Rule: "B", Severity: findings.Info},
	}
	got := findings.TopN(fs, 2)
	if len(got) != 4 {
		t.Fatalf("TopN(2) = %d findings, want 4", len(got))
	}
	countA, countB := 0, 0
	for _, f := range got {
		if f.Rule == "A" {
			countA++
		} else {
			countB++
		}
	}
	if countA != 2 {
		t.Errorf("rule A: got %d findings, want 2", countA)
	}
	if countB != 2 {
		t.Errorf("rule B: got %d findings, want 2", countB)
	}
}

func TestTopNPreservesOrder(t *testing.T) {
	fs := []findings.Finding{
		{Rule: "A", Message: "first"},
		{Rule: "A", Message: "second"},
		{Rule: "A", Message: "third"},
	}
	got := findings.TopN(fs, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %d", len(got))
	}
	if got[0].Message != "first" || got[1].Message != "second" {
		t.Errorf("order not preserved: %v %v", got[0].Message, got[1].Message)
	}
}
