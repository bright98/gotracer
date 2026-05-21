package rules

import (
	"fmt"

	"golang.org/x/exp/trace"
)

// goEntry records the start of a timed event for a goroutine.
type goEntry struct {
	at    trace.Time
	stack []string
}

// extractStack returns the call stack from ev as a slice of strings, one per
// frame, in the standard Go format: "func\n\tfile:line".
// Returns nil when the event carries no stack information.
func extractStack(ev trace.Event) []string {
	var frames []string
	for f := range ev.Stack().Frames() {
		if f.Func == "" && f.File == "" {
			continue
		}
		frames = append(frames, fmt.Sprintf("%s\n\t%s:%d", f.Func, f.File, f.Line))
	}
	return frames
}
