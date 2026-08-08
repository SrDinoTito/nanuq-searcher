package search

import (
	"testing"
)

// Concurrency benchmarks for the search pipeline.
//
// They reuse buildLoadService from load_test.go (deterministic fake engines,
// 1-5ms simulated latency, fresh results per call) so the measured numbers
// reflect the pipeline's concurrency behavior, not real network I/O.
//
// Each variant fixes the parallelism with b.SetParallelism and lets the
// runner distribute b.N over the parallel goroutines via b.RunParallel,
// measuring throughput degradation as concurrency grows.

func newBenchService() (*SearchService, int) {
	const (
		numEngines = 8
		perEngine  = 4
	)
	svc, _, want := buildLoadService(numEngines, perEngine)
	return svc, want
}

// benchSearchParallel runs one search per iteration on a shared service.
func benchSearchParallel(b *testing.B, parallelism int) {
	svc, want := newBenchService()
	names := []string{
		"load-e0", "load-e1", "load-e2", "load-e3",
		"load-e4", "load-e5", "load-e6", "load-e7",
	}
	raw := rawTextQueryForLoad(names, "parallel benchmark query")

	// Keep the run short: with -benchtime=100x the runner already fixes
	// b.N around 100; this guard prevents accidental very large b.N.
	if b.N > 200 {
		b.N = 200
	}

	b.SetParallelism(parallelism)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			container := svc.Search(raw)
			if container == nil {
				b.Fatal("nil container")
			}
			if got := len(container.GetOrderedResults()); got != want {
				b.Fatalf("got %d results, want %d", got, want)
			}
		}
	})
}

func BenchmarkSearchParallel_1(b *testing.B)  { benchSearchParallel(b, 1) }
func BenchmarkSearchParallel_4(b *testing.B)  { benchSearchParallel(b, 4) }
func BenchmarkSearchParallel_8(b *testing.B)  { benchSearchParallel(b, 8) }
func BenchmarkSearchParallel_16(b *testing.B) { benchSearchParallel(b, 16) }
func BenchmarkSearchParallel_32(b *testing.B) { benchSearchParallel(b, 32) }
