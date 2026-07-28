package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/renanbray/doorsniff/internal/model"
	"github.com/renanbray/doorsniff/internal/protocol/login"
	"github.com/renanbray/doorsniff/internal/protocol/query"
	"github.com/renanbray/doorsniff/internal/protocol/slp"
	"github.com/renanbray/doorsniff/internal/storage"
)

// Stats summarises a scan run. Probed counts every target examined;
// Found counts the targets that responded with a Minecraft server.
// The counters are atomic so the progress printer can poll them
// concurrently with the workers.
type Stats struct {
	Probed   atomic.Int64
	Found    atomic.Int64
	Errors   atomic.Int64
	Duration time.Duration
}

// PoolConfig is the configuration for Run. Repo may be nil for
// dry-runs that only count, but the MVP always passes one.
type PoolConfig struct {
	Workers int
	QPS     int
	Timeout time.Duration
	Repo    *storage.Repository
	Verbose bool
	ASCII   bool

	Writer  io.Writer // defaults to os.Stdout when nil
	Styles  *Styles   // defaults to NewStyles(ASCII) when nil
	Total   int64     // total targets; 0 disables the progress bar
	PortsLo uint16
	PortsHi uint16

	// Login, when true, performs a login handshake after successful
	// SLP to determine online/offline mode.
	Login   bool
	LoginMode login.StealthMode
	LoginUser string
	// PlayerHistory, when true, records each player seen in a scan
	// into the player_events table for historical tracking.
	PlayerHistory bool

	// State is the shared progress state. It must be the same
	// instance passed to generateTargets (or NewProgressState when
	// the caller does not use the generator). When nil, Run
	// creates a throwaway instance.
	State *ProgressState

	// Ticker is the sticky progress bar. When non-nil the worker
	// calls ticker.InsertAbove to display found-server blocks
	// without scrolling the bar.
	Ticker *Ticker

	// ProcessedHook, when non-nil, is called by the worker for
	// every target after the probe completes (success or error).
	// Used by tests to assert that each target is processed
	// exactly once.
	ProcessedHook func(model.Target)
}

// Run consumes the target channel and dispatches each target to a
// worker. Workers run with concurrency limited to cfg.Workers and a
// global rate limit of cfg.QPS. A single DB writer goroutine drains
// the result channel and persists servers.
//
// When cfg.Total > 0, a background goroutine prints a live progress
// line every 500ms; the static config header is printed once at the
// start. Successful discoveries are printed via Styles.RenderFound.
//
// On context cancellation, in-flight probes return. Run returns when
// all workers have exited, the DB writer has drained the result
// channel, and the progress goroutine has stopped.
//
// The returned *Stats must not be modified by the caller; counters
// are owned by Run and shared with the progress renderer.
func Run(ctx context.Context, cfg PoolConfig, targets <-chan model.Target) (*Stats, error) {
	start := time.Now()

	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.QPS <= 0 {
		cfg.QPS = 1
	}

	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	styles := cfg.Styles
	if styles == nil {
		styles = NewStyles(cfg.ASCII)
	}

	limiter := rate.NewLimiter(rate.Limit(cfg.QPS), cfg.QPS)
	results := make(chan model.Server, 256)

	stats := &Stats{}
	state := cfg.State
	if state == nil {
		state = NewProgressState()
	}

	var tick *Ticker
	if cfg.Total > 0 {
		prog := &Progress{
			Total:   cfg.Total,
			Probed:  &stats.Probed,
			Found:   &stats.Found,
			Errors:  &stats.Errors,
			Started: start,
			PortsLo: cfg.PortsLo,
			PortsHi: cfg.PortsHi,
			QPS:     cfg.QPS,
			Workers: cfg.Workers,
			Timeout: cfg.Timeout,
			Out:     w,
			Styles:  styles,
			State:   state,
		}
		tick = NewTicker(prog)
		tick.Start(500 * time.Millisecond)
		cfg.Ticker = tick
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		if cfg.Repo == nil {
			for range results {
			}
			return
		}
		for s := range results {
			if err := cfg.Repo.UpsertServer(ctx, s); err != nil {
				slog.Warn("upsert failed", "ip", s.IP, "port", s.Port, "err", err)
			}
		}
	}()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.Workers)

	for i := 0; i < cfg.Workers; i++ {
		wk := &worker{
			targets:       targets,
			limiter:       limiter,
			timeout:       cfg.Timeout,
			verbose:       cfg.Verbose,
			w:             w,
			styles:        styles,
			results:       results,
			stats:         stats,
			tick:          cfg.Ticker,
			state:         state,
			processedHook: cfg.ProcessedHook,
			login:         cfg.Login,
			loginMode:     cfg.LoginMode,
			loginUser:     cfg.LoginUser,
			playerHistory: cfg.PlayerHistory,
			repo:          cfg.Repo,
		}
		g.Go(func() error {
			return wk.run(gctx)
		})
	}

	workerErr := g.Wait()
	close(results)
	<-writerDone

	if tick != nil {
		tick.Stop()
	}

	stats.Duration = time.Since(start)
	return stats, workerErr
}

// runWorker is the body of one pool worker. It reads targets until the
// channel is closed or the context is cancelled. When verbose is
// false, per-target error lines are suppressed — only successful
// discoveries and the final summary are printed.
// worker is the body of one pool worker. It carries all the
// dependencies the inner loop needs, so adding a new feature (a
// webhook hook, metrics, a per-IP rate limit) is a one-field change
// instead of a one-parameter change.
type worker struct {
	targets       <-chan model.Target
	limiter       *rate.Limiter
	timeout       time.Duration
	verbose       bool
	w             io.Writer
	styles        *Styles
	results       chan<- model.Server
	stats         *Stats
	tick          *Ticker
	state         *ProgressState
	processedHook func(model.Target)
	login         bool
	loginMode     login.StealthMode
	loginUser     string
	playerHistory bool
	repo          *storage.Repository
}

// run reads targets until the channel is closed or the context is
// cancelled. When verbose is false, per-target error lines are
// suppressed — only successful discoveries and the final summary are
// printed.
func (wk *worker) run(ctx context.Context) error {
	for {
		var t model.Target
		select {
		case <-ctx.Done():
			return ctx.Err()
		case got, ok := <-wk.targets:
			if !ok {
				return nil
			}
			t = got
		}

		wk.processTarget(ctx, t)

		// The processed hook fires once per target, regardless of
		// outcome (success, error, port closed). Used by tests to
		// assert each target is processed exactly once.
		if wk.processedHook != nil {
			wk.processedHook(t)
		}
	}
}

// processTarget runs the probe → SLP → legacy fallback → result
// pipeline for a single target. Each outcome (success, port closed,
// probe error, slp error) updates stats accordingly.
func (wk *worker) processTarget(ctx context.Context, t model.Target) {
	// Update current target display. Heap-allocate so the
	// atomic.Pointer keeps the string alive; a stack-local
	// `addr := ...` would dangle at the next loop iteration.
	addrPtr := new(string)
	*addrPtr = t.Address()
	wk.state.Current.Store(addrPtr)

	if err := wk.limiter.Wait(ctx); err != nil {
		return
	}

	wk.stats.Probed.Add(1)
	// CurrentScanned is owned by the generator; workers do not
	// touch it. Per-IP bar and scanning label can drift by the
	// channel buffer (~5s at 200 qps), which is acceptable.

	open, err := Probe(ctx, t.IP, t.Port, wk.timeout)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		wk.stats.Errors.Add(1)
		slog.Debug("tcp probe error", "ip", t.IP, "port", t.Port, "err", err)
		if wk.verbose {
			fmt.Fprintf(wk.w, "[-] %s:%d  tcp error: %v\n", t.IP, t.Port, err)
		}
		return
	}
	if !open {
		return
	}

	resp, latency, err := slp.Ping(ctx, t.IP, t.Port, wk.timeout)
	if err != nil {
		// If the modern handshake RSTs, fall back to the 1.6
		// legacy ping. This covers BungeeCord with
		// ping_passthrough enabled and pre-1.7 servers.
		var legacy *slp.LegacyResponse
		var legacyLatency time.Duration
		var legacyErr error
		if isConnReset(err) {
			legacy, legacyLatency, legacyErr = slp.PingLegacy(ctx, t.IP, t.Port, wk.timeout)
		}

		if legacy != nil && legacyErr == nil {
			now := time.Now().UTC()
			s := model.Server{
				IP:            t.IP,
				Port:          t.Port,
				FirstSeen:     now,
				LastSeen:      now,
				LastLatencyMs: legacyLatency.Milliseconds(),
				VersionName:   legacy.Version,
				MOTD:          legacy.MOTD,
				NumPlayers:    legacy.NumPlayers,
				MaxPlayers:    legacy.MaxPlayers,
				QueryEnabled:  false,
				ProbeKind:     model.ProbeLegacy,
			}
			select {
			case wk.results <- s:
			case <-ctx.Done():
				return
			}
			wk.stats.Found.Add(1)
			wk.printFound(s)
			wk.recordServerScan(t.IP, t.Port, true, now)
			return
		}

		wk.stats.Errors.Add(1)
		if wk.verbose {
			hint := ""
			switch {
			case isConnReset(err):
				hint = " (server RST — likely BungeeCord/proxy, legacy also failed)"
			case ctx.Err() != nil:
				hint = " (cancelled)"
			}
			fmt.Fprintf(wk.w, "[-] %s:%d  slp failed%s\n", t.IP, t.Port, hint)
		}
		slog.Debug("slp error", "ip", t.IP, "port", t.Port, "err", err)
		return
	}

	now := time.Now().UTC()
	s := model.Server{
		IP:              t.IP,
		Port:            t.Port,
		FirstSeen:       now,
		LastSeen:        now,
		LastLatencyMs:   latency.Milliseconds(),
		VersionProtocol: resp.Version.Protocol,
		VersionName:     resp.Version.Name,
		MOTD:            resp.Description,
		NumPlayers:      resp.Players.Online,
		MaxPlayers:      resp.Players.Max,
		SamplePlayers:   toModelSample(resp.Players.Sample),
		QueryEnabled:    false,
		ProbeKind:       model.ProbeSLP,
	}

	// Send the SLP result immediately so the worker is not blocked
	// by the (slower) Query round-trip. Query enrichment runs async.
	select {
	case wk.results <- s:
	case <-ctx.Done():
		return
	}
	wk.stats.Found.Add(1)
	wk.printFound(s)
	wk.recordServerScan(t.IP, t.Port, true, now)

	// Record player events for SLP sample players before Query
	// enrichment can overwrite SamplePlayers.
	wk.recordPlayerEvents(t.IP, t.Port, s.SamplePlayers, now)

	// Enrich with Query data (UDP) and optionally with login handshake
	// in the background. Both are best-effort enrichments; failures are
	// expected when the server does not have those features enabled.
	// The last write to the results channel is what gets persisted.
	go func() {
		if ctx.Err() != nil {
			return
		}
		qr, _, qerr := query.Query(ctx, t.IP, t.Port, wk.timeout)
		if qerr == nil {
			s.QueryEnabled = true
			s.Map = qr.Map
			s.GameType = qr.GameType
			s.RawQuery = qr.Plugins
			if plugins := query.ParsePlugins(qr.Plugins); len(plugins) > 0 {
				s.Plugins = make([]model.Plugin, len(plugins))
				for i, p := range plugins {
					s.Plugins[i] = model.Plugin{Name: p.Name, Version: p.Version}
				}
			}
			if len(qr.PlayerNames) > 0 && len(s.SamplePlayers) == 0 {
				s.SamplePlayers = make([]model.SamplePlayer, len(qr.PlayerNames))
				for i, name := range qr.PlayerNames {
					s.SamplePlayers[i] = model.SamplePlayer{Name: name}
				}
			}
			s.ProbeKind = model.ProbeSLPAndQ
			// Record Query player names as events (they are the authoritative
			// player list from the server's perspective).
			if len(qr.PlayerNames) > 0 {
				qp := make([]model.SamplePlayer, len(qr.PlayerNames))
				for i, name := range qr.PlayerNames {
					qp[i] = model.SamplePlayer{Name: name}
				}
				wk.recordPlayerEvents(t.IP, t.Port, qp, now)
			}
			select {
			case wk.results <- s:
			case <-ctx.Done():
				return
			}
		}

		if wk.login {
			mode := wk.loginMode
			if mode == "" {
				mode = login.DefaultStealthMode
			}
			loginres, lerr := login.Handshake(ctx, t.IP, t.Port, wk.timeout, wk.loginUser, login.ProtocolVersionLegacy(), mode, slog.Default())
			if lerr != nil {
				slog.Debug("login probe failed", "ip", t.IP, "port", t.Port, "err", lerr)
			}
			if loginres != nil {
				modeStr := loginres.Mode.String()
				if loginres.Mode.IsPersistent() {
					s.LoginMode = modeStr
				} else {
					s.LastLogin = modeStr
				}
				select {
				case wk.results <- s:
				case <-ctx.Done():
				}
			}
		}
	}()
}

// printFound sends a discovered-server card to the right sink. When
// a sticky ticker is in use, the card is inserted above the bar
// (cursor control). Otherwise it scrolls to stdout directly.
func (wk *worker) printFound(s model.Server) {
	if wk.tick != nil {
		wk.tick.InsertAbove(wk.styles.FormatFound(s))
		return
	}
	fmt.Fprint(wk.w, wk.styles.FormatFound(s))
}

func (wk *worker) recordPlayerEvents(ip string, port uint16, players []model.SamplePlayer, seenAt time.Time) {
	if !wk.playerHistory || wk.repo == nil || len(players) == 0 {
		return
	}
	for _, p := range players {
		if p.Name == "" {
			continue
		}
		if err := wk.repo.RecordPlayerEvent(context.Background(), ip, port, p, seenAt); err != nil {
			slog.Warn("recording player event", "ip", ip, "port", port, "player", p.Name, "err", err)
		}
	}
}

func (wk *worker) recordServerScan(ip string, port uint16, hadResponse bool, at time.Time) {
	if !wk.playerHistory || wk.repo == nil {
		return
	}
	if err := wk.repo.RecordServerScan(context.Background(), ip, port, hadResponse, at); err != nil {
		slog.Warn("recording server scan", "ip", ip, "port", port, "err", err)
	}
}

func toModelSample(s []slp.SamplePlayer) []model.SamplePlayer {
	if len(s) == 0 {
		return nil
	}
	out := make([]model.SamplePlayer, 0, len(s))
	for _, p := range s {
		out = append(out, model.SamplePlayer{Name: p.Name, ID: p.ID})
	}
	return out
}

// isConnReset returns true when the error chain contains a connection
// reset (RST). The RST-after-SLP-handshake pattern is the signature
// of BungeeCord / Velocity proxies that have not enabled
// ping_passthrough.
func isConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	if strings.Contains(err.Error(), "reset by peer") {
		return true
	}
	return false
}
