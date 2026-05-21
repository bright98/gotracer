// Package report formats and writes findings to an output stream.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bright98/gotracer/findings"
)

// Format controls the output encoding.
type Format string

const (
	FormatHuman Format = "human" // aligned table, human-readable
	FormatJSON  Format = "json"  // newline-delimited JSON, one object per finding
	FormatHTML  Format = "html"  // self-contained HTML report written to a file
)

// Print writes all findings to w in the requested format.
// An unknown format falls back to FormatHuman.
func Print(w io.Writer, fs []findings.Finding, format Format) error {
	switch format {
	case FormatJSON:
		return printJSON(w, fs)
	default:
		return printHuman(w, fs)
	}
}

func printHuman(w io.Writer, fs []findings.Finding) error {
	if len(fs) == 0 {
		_, err := fmt.Fprintln(w, "no findings")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tRULE\tTIMESTAMP\tMESSAGE")
	fmt.Fprintln(tw, "--------\t----\t---------\t-------")
	for _, f := range fs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			f.Severity,
			f.Rule,
			f.Timestamp.Round(time.Millisecond),
			f.Message,
		)
		if frame := topUserFrame(f.Stack); frame != "" {
			fmt.Fprintf(tw, "\t\t\t  at %s\n", frame)
		}
	}
	return tw.Flush()
}

// topUserFrame returns the first stack frame that belongs to user code
// (not the Go runtime or standard library). Returns "" when no such frame exists.
func topUserFrame(stack []string) string {
	for _, entry := range stack {
		// Each entry is "funcName\n\tfile:line".
		// Take only the first line (function name).
		funcName := strings.SplitN(entry, "\n", 2)[0]
		if isUserFrame(funcName) {
			fileLine := ""
			if parts := strings.SplitN(entry, "\n\t", 2); len(parts) == 2 {
				fileLine = parts[1]
			}
			if fileLine != "" {
				return funcName + " (" + fileLine + ")"
			}
			return funcName
		}
	}
	return ""
}

// isUserFrame reports whether a function name belongs to user code rather than
// the Go runtime or standard library.
func isUserFrame(funcName string) bool {
	runtimePrefixes := []string{
		"runtime.", "runtime/", "sync.", "syscall.", "os.", "io.",
		"internal/", "reflect.", "encoding/", "fmt.", "net/",
	}
	for _, pfx := range runtimePrefixes {
		if strings.HasPrefix(funcName, pfx) {
			return false
		}
	}
	return funcName != ""
}

func printJSON(w io.Writer, fs []findings.Finding) error {
	enc := json.NewEncoder(w)
	for _, f := range fs {
		if err := enc.Encode(f); err != nil {
			return err
		}
	}
	return nil
}
