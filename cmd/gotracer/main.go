package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
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
		Use:     "gotracer",
		Short:   "analyze Go execution traces and emit structured findings",
		Version: buildVersion(),
	}
	rootCmd.AddCommand(newAnalyzeCmd())
	rootCmd.AddCommand(newCaptureCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(2)
	}
}

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Falls back to the module version embedded by go install, then "dev".
var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
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
	var format, output string
	var top int

	cmd := &cobra.Command{
		Use:   "analyze <file>",
		Short: "analyze a Go execution trace file and report findings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runAnalyze(args[0], report.Format(format), output, top)
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "human", "output format: human, json, or html")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file (default: stdout for human/json, gotracer_<timestamp>.html for html)")
	cmd.Flags().IntVar(&top, "top", 0, "show only the worst N findings per rule (0 = no limit)")
	return cmd
}

func runAnalyze(path string, format report.Format, output string, top int) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()

	rs := allRules()
	a := analyzer.New(rs...)
	fs, err := a.Run(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}
	fs = findings.Deduplicate(fs)
	fs = findings.TopN(fs, top)

	if format == report.FormatHTML {
		if output == "" {
			output = fmt.Sprintf("gotracer_%s.html", time.Now().UTC().Format("20060102_150405"))
		}
		outFile, err := os.Create(output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
			os.Exit(2)
		}
		defer outFile.Close()
		meta := report.Meta{Source: path, RuleCount: len(rs)}
		if err := report.WriteHTML(outFile, fs, meta); err != nil {
			fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
			os.Exit(2)
		}
		fmt.Println(output)
	} else {
		var w io.Writer = os.Stdout
		if output != "" {
			outFile, err := os.Create(output)
			if err != nil {
				fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
				os.Exit(2)
			}
			defer outFile.Close()
			w = outFile
			defer fmt.Println(output)
		}
		if err := report.Print(w, fs, format); err != nil {
			fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
			os.Exit(2)
		}
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
		top      int
	)

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "capture a trace from a live service and report findings",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCapture(rawURL, duration, report.Format(format), output, top)
			return nil
		},
	}
	cmd.Flags().StringVar(&rawURL, "url", "", "pprof trace URL (e.g. http://localhost:6060/debug/pprof/trace)")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "trace capture duration")
	cmd.Flags().StringVarP(&format, "format", "f", "html", "output format: html or json")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file (default: gotracer_<timestamp>.<ext>)")
	cmd.Flags().IntVar(&top, "top", 0, "show only the worst N findings per rule (0 = no limit)")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func runCapture(rawURL string, duration time.Duration, format report.Format, output string, top int) {
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
	fs = findings.Deduplicate(fs)
	fs = findings.TopN(fs, top)

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
