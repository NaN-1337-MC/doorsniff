package scanner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renanbray/swoomps/internal/model"
)

// TestRunProcessesEachTargetOnce verifies that every target yielded
// by the generator is processed exactly once, even with high
// concurrency.
func TestRunProcessesEachTargetOnce(t *testing.T) {
	const N = 100
	processed := make([]*atomic.Int64, N)
	for i := 0; i < N; i++ {
		processed[i] = new(atomic.Int64)
	}

	pgen := make(chan model.Target, N)
	go func() {
		defer close(pgen)
		for i := 0; i < N; i++ {
			processed[i].Add(1)
			pgen <- model.Target{IP: "127.0.0.1", Port: uint16(i)}
		}
	}()

	_, err := Run(context.Background(), PoolConfig{
		Workers: 32,
		QPS:     1000,
		Timeout: 100 * time.Millisecond,
		Repo:    nil,
	}, pgen)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait a moment for any straggler processing.
	time.Sleep(50 * time.Millisecond)

	// Each target should be processed exactly once.
	var wg sync.WaitGroup
	wg.Add(N)
	for i, c := range processed {
		go func(i int, c *atomic.Int64) {
			defer wg.Done()
			got := c.Load()
			if got != 1 {
				t.Errorf("target %d processed %d times, want 1", i, got)
			}
		}(i, c)
	}
	wg.Wait()
}
