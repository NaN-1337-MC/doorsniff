package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/renanbray/swoomps/internal/model"
	"github.com/renanbray/swoomps/internal/storage"
)

// Options configures a full scan. All fields except Repo and SeedsDir
// have sensible defaults applied in Run.
type Options struct {
	Workers  int
	QPS      int
	Timeout  string // duration string e.g. "3s"; empty = default
	PortsLo  uint16
	PortsHi  uint16
	Repo     *storage.Repository
	SeedsDir string
	Seeds    []model.Seed // when set, SeedsDir is ignored
	Verbose  bool
	ASCII    bool
	Resume   bool
}

// RunScan loads seeds from opts.SeedsDir (or uses opts.Seeds when
// provided), generates targets across [opts.PortsLo, opts.PortsHi],
// and dispatches them to the worker pool. It returns the aggregated stats and the first non-context
// error from the pool, if any.
func RunScan(ctx context.Context, opts Options) (Stats, error) {
	var seeds []model.Seed
	if len(opts.Seeds) > 0 {
		seeds = opts.Seeds
	} else {
		var err error
		seeds, err = LoadSeeds(opts.SeedsDir)
		if err != nil {
			return Stats{}, fmt.Errorf("loading seeds from %q: %w", opts.SeedsDir, err)
		}
		if len(seeds) == 0 {
			return Stats{}, fmt.Errorf("no seeds found in %q", opts.SeedsDir)
		}
	}

	// --- resume support ------------------------------------------------
	var progress map[string]uint16
	var skipped int
	if opts.Resume && opts.Repo != nil {
		p, err := opts.Repo.LoadProgress(ctx)
		if err != nil {
			return Stats{}, fmt.Errorf("loading scan progress: %w", err)
		}
		progress = p
	}

	// onSeedDone is called by generateTargets each time a seed finishes
	// all of its ports. It persists the checkpoint to the DB.
	var onSeedDone func(seedIP string, lastPort uint16)
	if opts.Resume && opts.Repo != nil {
		onSeedDone = func(seedIP string, lastPort uint16) {
			if err := opts.Repo.SaveProgress(ctx, seedIP, lastPort); err != nil {
				slog.Error("failed to save scan progress", "seed", seedIP, "err", err)
			}
		}
	}

	// Remove fully-completed seeds and compute total remaining targets.
	var remaining []model.Seed
	for _, s := range seeds {
		if _, done := progress[s.Host]; done {
			skipped++
			continue
		}
		remaining = append(remaining, s)
	}

	if skipped > 0 {
		slog.Info("resuming scan", "skipped_seeds", skipped, "remaining_seeds", len(remaining))
	}

	// Adjust the "last port scanned" for the seed that was interrupted.
	// generateTargets will start from (lastPort+1) for that seed.
	timeout, err := parseTimeout(opts.Timeout)
	if err != nil {
		return Stats{}, err
	}

	total := int64(len(remaining)) * int64(opts.PortsHi-opts.PortsLo+1)
	targets := generateTargets(ctx, remaining, opts.PortsLo, opts.PortsHi, progress, onSeedDone)

	return Run(ctx, PoolConfig{
		Workers:  opts.Workers,
		QPS:      opts.QPS,
		Timeout:  timeout,
		Repo:     opts.Repo,
		Verbose:  opts.Verbose,
		ASCII:    opts.ASCII,
		Total:    total,
		PortsLo:  opts.PortsLo,
		PortsHi:  opts.PortsHi,
	}, targets)
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
func generateTargets(ctx context.Context, seeds []model.Seed, lo, hi uint16, progress map[string]uint16, onSeedDone func(string, uint16)) <-chan model.Target {
	out := make(chan model.Target, 1024)
	go func() {
		defer close(out)
		for _, s := range seeds {
			startPort := lo
			if progress != nil {
				if lastPort, ok := progress[s.Host]; ok {
					// Seed was interrupted — resume from lastPort+1.
					if lastPort >= hi {
						continue // already fully scanned
					}
					startPort = lastPort + 1
				}
			}

			var lastPort uint16
			for p := startPort; p <= hi; p++ {
				select {
				case <-ctx.Done():
					return
				case out <- model.Target{IP: s.Host, Port: p}:
				}
				lastPort = p
			}

			if onSeedDone != nil {
				onSeedDone(s.Host, lastPort)
			}
		}
	}()
	return out
}

func parseTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 3 * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", s, err)
	}
	return d, nil
}
