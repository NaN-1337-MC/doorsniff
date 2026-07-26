package scanner

import (
	"context"
	"fmt"
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
	Verbose  bool
	ASCII    bool
}

// RunScan loads seeds from opts.SeedsDir, generates targets across
// [opts.PortsLo, opts.PortsHi], and dispatches them to the worker
// pool. It returns the aggregated stats and the first non-context
// error from the pool, if any.
func RunScan(ctx context.Context, opts Options) (Stats, error) {
	seeds, err := LoadSeeds(opts.SeedsDir)
	if err != nil {
		return Stats{}, fmt.Errorf("loading seeds from %q: %w", opts.SeedsDir, err)
	}
	if len(seeds) == 0 {
		return Stats{}, fmt.Errorf("no seeds found in %q", opts.SeedsDir)
	}

	timeout, err := parseTimeout(opts.Timeout)
	if err != nil {
		return Stats{}, err
	}

	total := int64(len(seeds)) * int64(opts.PortsHi-opts.PortsLo+1)
	targets := generateTargets(ctx, seeds, opts.PortsLo, opts.PortsHi)

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
func generateTargets(ctx context.Context, seeds []model.Seed, lo, hi uint16) <-chan model.Target {
	out := make(chan model.Target, 1024)
	go func() {
		defer close(out)
		for _, s := range seeds {
			for p := lo; p <= hi; p++ {
				select {
				case <-ctx.Done():
					return
				case out <- model.Target{IP: s.Host, Port: p}:
				}
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
