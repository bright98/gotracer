package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/capture"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/report"
	"github.com/bright98/gotracer/rules"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "gotracer",
		Short: "analyze Go execution traces and emit structured findings",
	}
	rootCmd.AddCommand(newAnalyzeCmd())
	rootCmd.AddCommand(newCaptureCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(2)
	}
}

func allRules() []findings.Rule {
	return []findings.Rule{
		rules.NewGCPauseSpike(),
		rules.NewHighSchedulingLatency(),
		rules.NewBlockedOnSyscall(),
		rules.NewMutexContention(),
		rules.NewGoroutineLeakGrowth(),
		rules.NewHeapGrowthSpike(),
		rules.NewProcessorStarvation(),
	}
}

// --- analyze ---

func newAnalyzeCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "analyze <file>",
		Short: "analyze a Go execution trace file and report findings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runAnalyze(args[0], report.Format(format))
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "human", "output format: human or json")
	return cmd
}

func runAnalyze(path string, format report.Format) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()

	a := analyzer.New(allRules()...)
	fs, err := a.Run(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}

	if err := report.Print(os.Stdout, fs, format); err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}

	for _, finding := range fs {
		if finding.Severity >= findings.Warn {
			os.Exit(1)
		}
	}
}

// --- capture ---

func newCaptureCmd() *cobra.Command {
	var (
		rawURL   string
		duration time.Duration
		format   string
		output   string
	)

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "capture a trace from a live service and report findings",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCapture(rawURL, duration, report.Format(format), output)
			return nil
		},
	}
	cmd.Flags().StringVar(&rawURL, "url", "", "pprof trace URL (e.g. http://localhost:6060/debug/pprof/trace)")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "trace capture duration")
	cmd.Flags().StringVarP(&format, "format", "f", "html", "output format: html or json")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file (default: gotracer_<timestamp>.<ext>)")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func runCapture(rawURL string, duration time.Duration, format report.Format, output string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintf(os.Stderr, "capturing trace from %s for %s...\n", rawURL, duration)

	rc, err := capture.Fetch(ctx, rawURL, duration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}
	defer rc.Close()

	capturedAt := time.Now()
	rs := allRules()
	a := analyzer.New(rs...)
	fs, err := a.Run(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}

	if output == "" {
		ext := "html"
		if format == report.FormatJSON {
			ext = "json"
		}
		output = fmt.Sprintf("gotracer_%s.%s", capturedAt.UTC().Format("20060102_150405"), ext)
	}

	outFile, err := os.Create(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}
	defer outFile.Close()

	meta := report.Meta{
		Source:     rawURL,
		CapturedAt: capturedAt,
		Duration:   duration,
		RuleCount:  len(rs),
	}

	switch format {
	case report.FormatHTML:
		err = report.WriteHTML(outFile, fs, meta)
	default:
		err = report.Print(outFile, fs, format)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}

	fmt.Println(output)

	for _, finding := range fs {
		if finding.Severity >= findings.Warn {
			os.Exit(1)
		}
	}
}
