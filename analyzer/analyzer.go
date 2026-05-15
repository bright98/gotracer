// Package analyzer runs the event loop over a Go execution trace, fanning
// each event out to all registered rules and collecting their findings.
package analyzer

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/bright98/gotracer/findings"
	"github.com/bright98/gotracer/reader"
)

// Analyzer fans each trace event out to all registered rules.
type Analyzer struct {
	rules []findings.Rule
}

// New creates an Analyzer with the given set of rules.
// Call with no arguments to create a no-op analyzer (useful for pipeline testing).
func New(rules ...findings.Rule) *Analyzer {
	return &Analyzer{rules: rules}
}

// Run processes the entire trace from src, passing each event to every rule,
// then calling Flush on all rules once the stream ends.
// Returns findings sorted by Timestamp.
//
// A non-nil error means the trace could not be opened or parsed — the caller
// should treat this as an exit-code-2 condition.
func (a *Analyzer) Run(src io.Reader) ([]findings.Finding, error) {
	r, err := reader.New(src)
	if err != nil {
		return nil, fmt.Errorf("opening trace: %w", err)
	}

	var all []findings.Finding

	for {
		ev, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading trace: %w", err)
		}
		for _, rule := range a.rules {
			all = append(all, rule.Process(ev)...)
		}
	}

	for _, rule := range a.rules {
		all = append(all, rule.Flush()...)
	}

	// Sort by trace timestamp so output is chronological regardless of
	// which rule emitted each finding.
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp < all[j].Timestamp
	})

	return all, nil
}
