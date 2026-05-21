// Package main provides a self-contained demo that runs synthetic workloads
// designed to trigger all 7 gotracer rules, then captures and reports findings.
//
// Run with:
//
//	go run ./e2e
package main

import (
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

func startPprof() {
	go func() {
		if err := http.ListenAndServe(pprofAddr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "pprof: %v\n", err)
			os.Exit(1)
		}
	}()
}

func startWorkloads() {
	go workloadGCPressure()
	go workloadMutexContention()
	go workloadSchedulerStress()
	go workloadSyscallLoad()
	go workloadGoroutineGrowth()
	go workloadHeapGrowth()
}

// gcLiveSink keeps a pointer-rich tree alive so the GC must scan many
// pointers during the mark phase, extending STW pause duration.
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

// workloadGCPressure keeps a large pointer-rich object graph alive and forces
// frequent GC cycles. The live set forces GC to scan many pointers, extending
// STW mark-termination pauses.
func workloadGCPressure() {
	gcLiveSink = buildGCTree(8) // 4^8 = 65 536 nodes — large pointer graph
	for {
		for i := 0; i < 200; i++ {
			_ = make([]byte, 256*1024) // 256 KB short-lived garbage
		}
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
	}
}

// workloadMutexContention puts 20 goroutines in contention over a single mutex
// that is held for 5 ms at a time, producing long wait times.
func workloadMutexContention() {
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		go func() {
			for {
				mu.Lock()
				time.Sleep(5 * time.Millisecond) // hold the lock
				mu.Unlock()
				time.Sleep(2 * time.Millisecond) // brief backoff
			}
		}()
	}
	select {} // keep the goroutine alive
}

// workloadSchedulerStress spawns GOMAXPROCS×8 CPU-bound goroutines in bursts
// so goroutines queue up waiting for a P.
func workloadSchedulerStress() {
	for {
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
		// Idle period between bursts — Ps go idle here, triggering ProcessorStarvation.
		time.Sleep(80 * time.Millisecond)
	}
}

// workloadSyscallLoad runs goroutines that make repeated blocking file-write
// syscalls. Regular file I/O goes through entersyscall/exitsyscall, which the
// runtime records as GoSyscall state transitions.
func workloadSyscallLoad() {
	for i := 0; i < 12; i++ {
		go func() {
			f, err := os.CreateTemp("", "gotracer-e2e-*")
			if err != nil {
				return
			}
			defer os.Remove(f.Name())
			defer f.Close()
			payload := make([]byte, 32*1024) // 32 KB per write
			for {
				f.WriteAt(payload, 0)
				time.Sleep(8 * time.Millisecond)
			}
		}()
	}
}

// workloadGoroutineGrowth continuously spawns goroutines that outlive the
// trace window, so the net goroutine count grows monotonically during the trace.
func workloadGoroutineGrowth() {
	for {
		go func() {
			time.Sleep(60 * time.Second) // outlives any capture window
		}()
		time.Sleep(40 * time.Millisecond) // ~25 new goroutines per second
	}
}

// workloadHeapGrowth appends large slices to a package-level sink so the GC
// cannot reclaim them, producing a steady heap growth signal.
func workloadHeapGrowth() {
	for {
		chunk := make([]byte, 5*1024*1024) // 5 MB per tick
		heapSink = append(heapSink, chunk)
		if len(heapSink) > 30 { // cap at ~150 MB to avoid OOM
			heapSink = heapSink[1:]
		}
		time.Sleep(200 * time.Millisecond)
	}
}
