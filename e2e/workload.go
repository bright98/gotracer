// Package main provides a self-contained demo that runs synthetic workloads
// designed to trigger all 7 gotracer rules, then captures and reports findings.
//
// Run with:
//
//	go run ./e2e
package main

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"sync"
	"time"
)

const pprofAddr = "localhost:6061"

// heapSink keeps allocations alive so the GC sees genuine heap growth.
var heapSink [][]byte

// gcLiveSink keeps a pointer-rich tree alive so GC must scan many pointers.
var gcLiveSink *gcNode

type gcNode struct {
	children [4]*gcNode
	data     [128]byte
}

func buildGCTree(depth int) *gcNode {
	if depth == 0 {
		return &gcNode{}
	}
	n := &gcNode{}
	for i := range n.children {
		n.children[i] = buildGCTree(depth - 1)
	}
	return n
}

func startPprof() {
	go func() {
		if err := http.ListenAndServe(pprofAddr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "pprof: %v\n", err)
			os.Exit(1)
		}
	}()
}

// startWorkloads starts all synthetic load goroutines. They all stop when ctx
// is cancelled, so the process exits cleanly after the trace is captured.
func startWorkloads(ctx context.Context) {
	go workloadGCPressure(ctx)
	go workloadMutexContention(ctx)
	go workloadSchedulerStress(ctx)
	go workloadSyscallLoad(ctx)
	go workloadGoroutineGrowth(ctx)
	go workloadHeapGrowth(ctx)
}

// workloadGCPressure allocates and discards large objects to force frequent
// GC cycles. A large pointer-rich live set makes STW mark pauses longer.
func workloadGCPressure(ctx context.Context) {
	gcLiveSink = buildGCTree(8) // 4^8 = 65 536 nodes — large pointer graph
	defer func() { gcLiveSink = nil }()
	for {
		for i := 0; i < 200; i++ {
			_ = make([]byte, 256*1024)
		}
		runtime.GC()
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// workloadMutexContention puts 20 goroutines in contention over a single mutex
// held for 5 ms at a time, producing long wait times.
func workloadMutexContention(ctx context.Context) {
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				mu.Lock()
				time.Sleep(5 * time.Millisecond)
				mu.Unlock()
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}
	<-ctx.Done()
}

// workloadSchedulerStress spawns GOMAXPROCS×8 CPU-bound goroutines in bursts
// so goroutines queue up waiting for a P.
func workloadSchedulerStress(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n := runtime.GOMAXPROCS(0) * 8
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				x := 0
				for j := 0; j < 2_000_000; j++ {
					x += j * j
				}
				_ = x
			}()
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-time.After(80 * time.Millisecond):
		}
	}
}

// workloadSyscallLoad calls f.Sync() (fsync) in a loop. fsync flushes dirty
// pages to physical disk and typically takes 3–10 ms on SSD — long enough to
// hold an OS thread and produce a genuine GoSyscall event.
// WriteAt overwrites offset 0 each time so the file stays at 64 KB; Write
// would append and fill the disk.
func workloadSyscallLoad(ctx context.Context) {
	for i := 0; i < 8; i++ {
		go func() {
			f, err := os.CreateTemp("", "gotracer-e2e-*")
			if err != nil {
				return
			}
			defer os.Remove(f.Name())
			defer f.Close()
			payload := make([]byte, 64*1024)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				f.WriteAt(payload, 0) // overwrite, not append — file stays 64 KB
				f.Sync()
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}
}

// workloadGoroutineGrowth continuously spawns goroutines that outlive the
// trace window so the net goroutine count grows monotonically during the trace.
func workloadGoroutineGrowth(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(40 * time.Millisecond):
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(60 * time.Second):
				}
			}()
		}
	}
}

// workloadHeapGrowth appends large slices to a package-level sink so the GC
// cannot reclaim them, producing a steady heap growth signal.
func workloadHeapGrowth(ctx context.Context) {
	defer func() { heapSink = nil }()
	for {
		chunk := make([]byte, 5*1024*1024)
		heapSink = append(heapSink, chunk)
		if len(heapSink) > 30 {
			heapSink = heapSink[1:]
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}
