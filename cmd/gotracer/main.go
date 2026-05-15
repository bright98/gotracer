package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bright98/gotracer/analyzer"
	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/report"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "gotracer",
		Short: "analyze Go execution traces and emit structured findings",
	}
	rootCmd.AddCommand(newAnalyzeCmd())

	if err := rootCmd.Execute(); err != nil {
		// cobra already printed the error; non-zero exit for argument errors.
		os.Exit(2)
	}
}

func newAnalyzeCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "analyze <file>",
		Short: "analyze a Go execution trace file and report findings",
		Args:  cobra.ExactArgs(1),
		// RunE is used so cobra handles --help and flag errors for us.
		// We call os.Exit directly for our custom codes (1=findings, 2=parse error)
		// so that cobra does not print a redundant error message.
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

	// No rules registered yet — Phase 2 adds the first rule (GCPauseSpike).
	a := analyzer.New()
	fs, err := a.Run(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}

	if err := report.Print(os.Stdout, fs, format); err != nil {
		fmt.Fprintf(os.Stderr, "gotracer: %v\n", err)
		os.Exit(2)
	}

	// Exit 1 if any finding is at Warn or Error severity.
	for _, finding := range fs {
		if finding.Severity >= findings.Warn {
			os.Exit(1)
		}
	}
}
