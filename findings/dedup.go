package findings

import (
	"sort"
	"strings"
)

// Deduplicate collapses findings that share the same rule and code location
// into a single representative Finding. The worst finding in each group
// (highest severity, then earliest timestamp) is kept; its Count field is set
// to the total number of occurrences.
//
// Result is sorted: Error before Warn before Info, then by Count descending
// (most frequent problems first).
func Deduplicate(fs []Finding) []Finding {
	type key struct{ rule, loc string }
	type group struct {
		rep   Finding
		count int
	}

	var order []key
	groups := make(map[key]*group)

	for _, f := range fs {
		k := key{rule: f.Rule, loc: topLocation(f.Stack)}
		g, exists := groups[k]
		if !exists {
			g = &group{rep: f}
			groups[k] = g
			order = append(order, k)
		}
		g.count++
		if f.Severity > g.rep.Severity {
			g.rep = f
		}
	}

	result := make([]Finding, 0, len(order))
	for _, k := range order {
		g := groups[k]
		g.rep.Count = g.count
		result = append(result, g.rep)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Severity != result[j].Severity {
			return result[i].Severity > result[j].Severity
		}
		return result[i].Count > result[j].Count
	})

	return result
}

// topLocation returns the "file:line" of the first user-code frame in stack,
// or "" when the stack is empty or contains only runtime/stdlib frames.
func topLocation(stack []string) string {
	for _, frame := range stack {
		parts := strings.SplitN(frame, "\n\t", 2)
		if len(parts) < 2 {
			continue
		}
		if IsUserFrame(parts[0]) {
			return parts[1]
		}
	}
	return ""
}

// IsUserFrame reports whether funcName belongs to user code rather than the
// Go runtime or standard library.
func IsUserFrame(funcName string) bool {
	for _, pfx := range []string{
		"runtime.", "sync.", "syscall.", "os.", "io.",
		"internal/", "reflect.", "encoding/", "fmt.", "net/",
	} {
		if strings.HasPrefix(funcName, pfx) {
			return false
		}
	}
	return funcName != ""
}
