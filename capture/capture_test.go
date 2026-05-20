package capture_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bright98/gotracer/capture"
)

func TestFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("trace-data"))
	}))
	defer srv.Close()

	rc, err := capture.Fetch(context.Background(), srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "trace-data" {
		t.Errorf("body = %q, want %q", string(body), "trace-data")
	}
}

func TestFetchNon200ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := capture.Fetch(context.Background(), srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not mention status 404", err.Error())
	}
}

func TestFetchSecondsParamSetFromDuration(t *testing.T) {
	var gotSeconds string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSeconds = r.URL.Query().Get("seconds")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rc, err := capture.Fetch(context.Background(), srv.URL, 7*time.Second)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	rc.Close()

	if gotSeconds != "7" {
		t.Errorf("seconds param = %q, want %q", gotSeconds, "7")
	}
}

// Sub-second durations must clamp to 1 so the pprof endpoint gets a valid window.
func TestFetchSubSecondDurationClampsToOne(t *testing.T) {
	var gotSeconds string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSeconds = r.URL.Query().Get("seconds")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rc, err := capture.Fetch(context.Background(), srv.URL, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	rc.Close()

	if gotSeconds != "1" {
		t.Errorf("seconds param = %q, want 1 (sub-second should clamp to 1)", gotSeconds)
	}
}

func TestFetchPreservesExistingQueryParams(t *testing.T) {
	var gotDebug, gotSeconds string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDebug = r.URL.Query().Get("debug")
		gotSeconds = r.URL.Query().Get("seconds")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rc, err := capture.Fetch(context.Background(), srv.URL+"?debug=1", 3*time.Second)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	rc.Close()

	if gotDebug != "1" {
		t.Error("existing query param 'debug' was dropped")
	}
	if gotSeconds != "3" {
		t.Errorf("seconds param = %q, want 3", gotSeconds)
	}
}

func TestFetchInvalidURLReturnsError(t *testing.T) {
	_, err := capture.Fetch(context.Background(), "://not-a-url", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid URL") {
		t.Errorf("error %q does not mention 'invalid URL'", err.Error())
	}
}

func TestFetchCancelledContextReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Fetch

	_, err := capture.Fetch(ctx, srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
