package scanner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/renanbray/swoomps/internal/model"
)

// TestRunProcessesEachTargetOnce verifies that every target yielded
// by the generator is processed exactly once, even with high
// concurrency.
//
// Each target has a unique port (0..N-1). The worker calls
// processedHook with the target's port. After Run returns, every
// port must have been seen exactly once. Because the hook runs in
// the worker (after the probe), and we read the counters only
// after Run returns, there is no race.
func TestRunProcessesEachTargetOnce(t *testing.T) {
	const N = 100

	var (
		mu     sync.Mutex
		hits   = make(map[uint16]int, N)
	)

	cfg := PoolConfig{
		Workers: 32,
		QPS:     1000,
		Timeout: 100 * time.Millisecond,
		Repo:    nil,
		ProcessedHook: func(t model.Target) {
			mu.Lock()
			hits[t.Port]++
			mu.Unlock()
		},
	}

	pgen := make(chan model.Target, N)
	go func() {
		defer close(pgen)
		for i := 0; i < N; i++ {
			pgen <- model.Target{IP: "127.0.0.1", Port: uint16(i)}
		}
	}()

	if _, err := Run(context.Background(), cfg, pgen); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < N; i++ {
		if hits[uint16(i)] != 1 {
			t.Errorf("target port %d seen %d times, want 1", i, hits[uint16(i)])
		}
	}
}
