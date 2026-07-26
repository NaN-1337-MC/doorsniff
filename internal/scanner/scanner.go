package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/renanbray/swoomps/internal/model"
	"github.com/renanbray/swoomps/internal/storage"
)

// Options configures a full scan. Seeds must be resolved by the
// caller before calling RunScan; the scanner does not read from the
// filesystem.
type Options struct {
	Workers int
	QPS     int
	Timeout time.Duration // per-probe timeout; 0 = default (1s)
	PortsLo uint16
	PortsHi uint16
	Repo    *storage.Repository
	Seeds   []model.Seed
	Verbose bool
	ASCII   bool
	Resume  bool
}

// RunScan takes already-resolved seeds, generates targets across
// [opts.PortsLo, opts.PortsHi], and dispatches them to the worker
// pool. It returns the aggregated stats and the first non-context
// error from the pool, if any.
//
// The returned *Stats must not be modified by the caller; the
// counters are owned by the worker pool.
func RunScan(ctx context.Context, opts Options) (*Stats, error) {
	if len(opts.Seeds) == 0 {
		return nil, fmt.Errorf("no seeds provided")
	}

	progress, onSeedDone, err := prepareResume(ctx, opts)
	if err != nil {
		return nil, err
	}

	remaining, skipped := skipCompleted(opts.Seeds, progress)
	if skipped > 0 {
		slog.Info("resuming scan", "skipped_seeds", skipped, "remaining_seeds", len(remaining))
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 1 * time.Second
	}

	total := int64(len(remaining)) * int64(opts.PortsHi-opts.PortsLo+1)

	state := NewProgressState()
	targets := generateTargets(ctx, remaining, opts.PortsLo, opts.PortsHi, progress, onSeedDone, state)

	return Run(ctx, PoolConfig{
		Workers: opts.Workers,
		QPS:     opts.QPS,
		Timeout: timeout,
		Repo:    opts.Repo,
		Verbose: opts.Verbose,
		ASCII:   opts.ASCII,
		Total:   total,
		PortsLo: opts.PortsLo,
		PortsHi: opts.PortsHi,
		State:   state,
	}, targets)
}

// prepareResume wires up the resume callbacks. It returns:
//
//   - progress: the per-seed map loaded from the DB when Resume is
//     true. nil when Resume is false or Repo is nil.
//   - onSeedDone: a callback the generator uses to persist
//     completed seeds. Always non-nil when Repo is set, so the DB
//     is kept up to date even on a fresh run (the next --resume
//     will then skip those seeds).
//
// Splitting "load" from "save" matters: a scan started without
// --resume still records progress, so the user can interrupt and
// resume later.
func prepareResume(ctx context.Context, opts Options) (map[string]uint16, func(string, uint16), error) {
	var progress map[string]uint16
	if opts.Resume && opts.Repo != nil {
		p, err := opts.Repo.LoadProgress(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("loading scan progress: %w", err)
		}
		progress = p
	}

	var onSeedDone func(string, uint16)
	if opts.Repo != nil {
		onSeedDone = func(seedIP string, lastPort uint16) {
			if err := opts.Repo.SaveProgress(ctx, seedIP, lastPort); err != nil {
				slog.Error("failed to save scan progress", "seed", seedIP, "err", err)
			}
		}
	}

	return progress, onSeedDone, nil
}

// skipCompleted removes seeds that are present in the progress map
// from the input list and returns the remaining seeds plus the
// number of skipped ones.
func skipCompleted(seeds []model.Seed, progress map[string]uint16) ([]model.Seed, int) {
	if len(progress) == 0 {
		return seeds, 0
	}
	remaining := make([]model.Seed, 0, len(seeds))
	skipped := 0
	for _, s := range seeds {
		if _, done := progress[s.Host]; done {
			skipped++
			continue
		}
		remaining = append(remaining, s)
	}
	return remaining, skipped
}

// generateTargets yields one Target per (seed, port) pair, respecting
// ctx cancellation. The returned channel is closed when all targets
// have been emitted or the context is cancelled.
//
// When progress is non-nil, seeds already present in the map are
// skipped entirely, and seeds that were interrupted start from
// lastPort+1 instead of PortsLo. After a seed completes all its ports,
// onSeedDone (if set) is called so the caller can persist the
// checkpoint.
//
// state tracks which seed is active and how many of its ports have
// been emitted. Only this goroutine writes to state.CurrentSeed and
// state.CurrentScanned.
func generateTargets(ctx context.Context, seeds []model.Seed, lo, hi uint16, progress map[string]uint16, onSeedDone func(string, uint16), state *ProgressState) <-chan model.Target {
	out := make(chan model.Target, 1024)
	go func() {
		defer close(out)
		seen := make(map[string]bool, len(seeds))
		for _, s := range seeds {
			if seen[s.Host] {
				continue
			}
			seen[s.Host] = true

			startPort := lo
			if progress != nil {
				if lastPort, ok := progress[s.Host]; ok {
					if lastPort >= hi {
						continue
					}
					startPort = lastPort + 1
				}
			}

			// Announce new seed and reset per-IP counter. The generator
			// is the only writer of CurrentSeed and CurrentScanned —
			// workers only update Current (last IP:Port processed).
			//
			// hostPtr is heap-allocated so the atomic.Pointer keeps
			// it alive across iterations; a stack-local `host := s.Host`
			// would be unsafe (the pointer would dangle once the
			// loop iteration ends).
			hostPtr := new(string)
			*hostPtr = s.Host
			state.CurrentSeed.Store(hostPtr)
			state.CurrentScanned.Store(0)

			var lastPort uint16
			for p := uint32(startPort); p <= uint32(hi); p++ {
				port := uint16(p)
				select {
				case <-ctx.Done():
					return
				case out <- model.Target{IP: s.Host, Port: port}:
				}
				lastPort = port
				state.CurrentScanned.Add(1)
			}

			if onSeedDone != nil {
				onSeedDone(s.Host, lastPort)
			}
		}
	}()
	return out
}
