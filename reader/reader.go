// Package reader wraps golang.org/x/exp/trace.Reader and exposes a simple
// event stream to the rest of gotracer. Keeping the upstream trace API
// isolated here means a version change only requires updates in this file.
package reader

import (
	"io"

	"golang.org/x/exp/trace"
)

// Reader wraps trace.Reader and exposes a minimal Read method.
type Reader struct {
	r *trace.Reader
}

// New creates a Reader from any io.Reader containing a Go execution trace.
// Returns an error if the trace header is invalid or the format is unrecognized.
func New(src io.Reader) (*Reader, error) {
	r, err := trace.NewReader(src)
	if err != nil {
		return nil, err
	}
	return &Reader{r: r}, nil
}

// Read returns the next event from the trace.
// Returns io.EOF when the trace is fully consumed.
func (r *Reader) Read() (trace.Event, error) {
	return r.r.ReadEvent()
}
