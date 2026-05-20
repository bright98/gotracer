// Package capture fetches a Go runtime execution trace from a live service's
// pprof HTTP endpoint (/debug/pprof/trace).
package capture

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Fetch requests a runtime trace from a live Go service. It appends
// ?seconds=N to rawURL, sends a GET request, and returns the response body
// for the caller to read and close.
//
// The HTTP client timeout is duration + 15s: the service blocks for the full
// duration collecting the trace before flushing the response, so the timeout
// must exceed the capture window.
//
// The caller is responsible for closing the returned ReadCloser.
func Fetch(ctx context.Context, rawURL string, duration time.Duration) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	q := u.Query()
	q.Set("seconds", strconv.Itoa(max(1, int(duration.Seconds()))))
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: duration + 15*time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("capture request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("server returned %s — is /debug/pprof/trace registered?", resp.Status)
	}
	return resp.Body, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
