package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/capture"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/report"
	"github.com/bright98/gotracer/rules"
)

const (
	traceURL        = "http://" + pprofAddr + "/debug/pprof/trace"
	warmupDuration  = 3 * time.Second
	captureDuration = 5 * time.Second
)

func main() {
	if err := os.MkdirAll("examples", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir examples: %v\n", err)
		os.Exit(1)
	}

	// workloadCtx is cancelled after the trace is captured so all workload
	// goroutines stop and the process exits cleanly.
	workloadCtx, stopWorkloads := context.WithCancel(context.Background())
	defer stopWorkloads()

	startPprof()
	startWorkloads(workloadCtx)

	fmt.Printf("workloads running — warming up for %s...\n", warmupDuration)
	time.Sleep(warmupDuration)

	fmt.Printf("capturing %s trace from %s...\n\n", captureDuration, traceURL)

	ctx, cancel := context.WithTimeout(context.Background(), captureDuration+30*time.Second)
	defer cancel()

	rc, err := capture.Fetch(ctx, traceURL, captureDuration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture: %v\n", err)
		os.Exit(1)
	}
	defer rc.Close()

	rs := demoRules()
	a := analyzer.New(rs...)
	raw, err := a.Run(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		os.Exit(1)
	}
	fs := findings.Deduplicate(raw)

	fmt.Printf("found %d unique finding(s) (from %d total events)\n\n", len(fs), len(raw))

	// ── human → stdout (capped to keep terminal readable) ───────────────────
	const terminalCap = 40
	display := fs
	truncated := false
	if len(display) > terminalCap {
		display = display[:terminalCap]
		truncated = true
	}
	fmt.Println("── human ──────────────────────────────────────────────────────")
	if err := report.Print(os.Stdout, display, report.FormatHuman); err != nil {
		fmt.Fprintf(os.Stderr, "report: %v\n", err)
	}
	if truncated {
		fmt.Printf("  ... and %d more — see json/html files for the full list\n", len(fs)-terminalCap)
	}

	// ── JSON → file ─────────────────────────────────────────────────────────
	const jsonPath = "examples/e2e_findings.json"
	writeReport(jsonPath, func(f *os.File) error {
		return report.Print(f, fs, report.FormatJSON)
	})
	fmt.Printf("\njson  → %s\n", jsonPath)

	// ── HTML → file ─────────────────────────────────────────────────────────
	const htmlPath = "examples/e2e_report.html"
	writeReport(htmlPath, func(f *os.File) error {
		return report.WriteHTML(f, fs, report.Meta{
			Source:     traceURL,
			CapturedAt: time.Now(),
			Duration:   captureDuration,
			RuleCount:  len(rs),
		})
	})
	fmt.Printf("html  → %s\n", htmlPath)

	stopWorkloads() // signal all workload goroutines to exit
}

// demoRules returns all 8 rules with thresholds tuned to fire reliably in a
// short demo trace. The defaults in the rules themselves are
// production-appropriate; these are only for the demo.
func demoRules() []findings.Rule {
	gc := rules.NewGCPauseSpike()
	gc.WarnThreshold = 200 * time.Microsecond // pointer-rich live set makes GC work harder

	sched := rules.NewHighSchedulingLatency()
	// default 1ms warn is sufficient with GOMAXPROCS×8 competing goroutines

	block := rules.NewBlockedOnSyscall()
	block.WarnThreshold = 2 * time.Millisecond // fsync takes 1–10ms on SSD; meaningful threshold

	mutex := rules.NewMutexContention()
	// default 1ms warn fires easily: 20 goroutines fighting a 5ms-held lock

	leak := rules.NewGoroutineLeakGrowth()
	leak.WarnThreshold = 30 // ~25 goroutines/s × 5s ≈ 125 spawned; fire early

	heap := rules.NewHeapGrowthSpike()
	heap.WarnGrowthBytes = 20 * 1024 * 1024     // 20 MB (default 100 MB)
	heap.WarnRateBytesPerSec = 5 * 1024 * 1024  // 5 MB/s (workload does ~25 MB/s)

	proc := rules.NewProcessorStarvation()
	proc.WarnThreshold = 5 * time.Millisecond // filters sub-ms noise; 80ms burst gaps still fire

	assist := rules.NewGCAssist()
	assist.WarnThreshold = 50 * time.Microsecond // very low: catch any assist wait with GOGC=1

	return []findings.Rule{gc, sched, block, mutex, leak, heap, proc, assist}
}

func writeReport(path string, fn func(*os.File) error) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", path, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := fn(f); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
		os.Exit(1)
	}
}
