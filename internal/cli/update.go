package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/renanbray/doorsniff/internal/config"
	"github.com/renanbray/doorsniff/internal/model"
	"github.com/renanbray/doorsniff/internal/protocol/login"
	"github.com/renanbray/doorsniff/internal/protocol/query"
	"github.com/renanbray/doorsniff/internal/protocol/slp"
	"github.com/renanbray/doorsniff/internal/scanner"
	"github.com/renanbray/doorsniff/internal/storage"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

func runUpdate(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		versionFlag      string
		engineFlag       string
		playersFlag      string
		maxPlayersFlag   string
		portFlag         string
		motdFlag         string
		qpsFlag          int
		workersFlag      int
		timeoutFlag      time.Duration
		limitFlag        int
		databaseFlag     string
		loginFlag        bool
		loginModeFlag    string
		loginUserFlag    string
		debugLoginFlag   bool
		asciiFlag        bool
	)

	fs.StringVar(&versionFlag, "version", "", "filter by version (e.g. 1.21.1)")
	fs.StringVar(&engineFlag, "engine", "", "filter by server engine, case-insensitive (e.g. paper, purpur)")
	fs.StringVar(&playersFlag, "players", "", "filter by player count (e.g. >1, 1-10, <50)")
	fs.StringVar(&maxPlayersFlag, "maxplayers", "", "filter by max players (e.g. >=100, 50-200)")
	fs.StringVar(&portFlag, "port", "", "filter by port or range (e.g. 25565 or 25500-25600)")
	fs.StringVar(&motdFlag, "motd", "", "search MOTD for keywords")
	fs.IntVar(&qpsFlag, "qps", cfg.UpdateQPS, "global QPS limit")
	fs.IntVar(&workersFlag, "workers", cfg.UpdateWorkers, "worker pool size")
	fs.DurationVar(&timeoutFlag, "timeout", cfg.UpdateTimeout, "per-probe timeout")
	fs.IntVar(&limitFlag, "limit", cfg.UpdateLimit, "max servers to update (0 = all)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")
	fs.BoolVar(&loginFlag, "login", cfg.UpdateLogin, "perform login handshake to detect online/offline mode (deprecated: prefer --login-mode)")
	fs.StringVar(&loginModeFlag, "login-mode", cfg.UpdateLoginMode, "login handshake aggressiveness: off | detect | intrusive")
	fs.StringVar(&loginUserFlag, "login-user", cfg.LoginUser, "username for login handshake")
	fs.BoolVar(&debugLoginFlag, "debug-login", cfg.DebugLogin, "dump raw login packet bytes + disconnect reasons to stderr (debug)")
	fs.BoolVar(&asciiFlag, "ascii", cfg.ASCII, "plain output (no colour, ASCII border and bar)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, updateUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

	if debugLoginFlag {
		handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
		slog.SetDefault(slog.New(handler))
	}

	playerFilter, err := ParsePlayerFilter(playersFlag)
	if err != nil {
		return fmt.Errorf("invalid --players: %w", err)
	}
	playerSQL, playerArgs := playerFilter.ToSQL()

	maxPlayerFilter, err := ParsePlayerFilter(maxPlayersFlag)
	if err != nil {
		return fmt.Errorf("invalid --maxplayers: %w", err)
	}
	maxPlayerSQL, maxPlayerArgs := maxPlayerFilter.ToSQLColumn("max_players")

	portLo, portHi, err := ParsePortFilter(portFlag)
	if err != nil {
		return fmt.Errorf("invalid --port: %w", err)
	}

	versions := ParseVersionList(versionFlag)

	loginMode, err := login.ParseStealthMode(loginModeFlag)
	if err != nil {
		return fmt.Errorf("parsing --login-mode: %w", err)
	}
	if !loginFlag {
		loginMode = login.StealthOff
	}

	dbPath := cfg.DatabasePath
	if databaseFlag != "" {
		dbPath = databaseFlag
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensuring schema: %w", err)
	}

	repo := storage.NewRepository(db)

	listQuery := storage.ListQuery{
		PlayerSQL:     playerSQL,
		PlayerArgs:   playerArgs,
		MaxPlayerSQL: maxPlayerSQL,
		MaxPlayerArgs: maxPlayerArgs,
		Versions:     versions,
		Engine:       engineFlag,
		MOTD:         motdFlag,
		PortLo:       portLo,
		PortHi:       portHi,
		OrderBy:      "last_seen ASC",
		Limit:        limitFlag,
	}

	servers, err := repo.ListServersFiltered(ctx, listQuery)
	if err != nil {
		return fmt.Errorf("querying servers: %w", err)
	}

	if len(servers) == 0 {
		fmt.Println("no servers found matching the filters.")
		return nil
	}

	ascii := asciiFlag
	styles := scanner.NewStyles(ascii)

	start := time.Now()
	ustats := &updateStats{
		checked: atomic.Int64{},
		changed: atomic.Int64{},
	}
	state := scanner.NewProgressState()

	uprog := &updateProgress{
		Total:   int64(len(servers)),
		Checked: &ustats.checked,
		Changed: &ustats.changed,
		Started: start,
		Out:     os.Stdout,
		Styles:  styles,
		State:   state,
	}

	ticker := newUpdateTicker(uprog)
	ticker.Start(500 * time.Millisecond)

	limiter := rate.NewLimiter(rate.Limit(qpsFlag), qpsFlag)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workersFlag)

	for i := range servers {
		srv := servers[i]
		g.Go(func(srv model.Server) func() error {
			return func() error {
				select {
				case <-gctx.Done():
					return gctx.Err()
				default:
				}

				if err := limiter.Wait(gctx); err != nil {
					return err
				}

				addr := fmt.Sprintf("%s:%d", srv.IP, srv.Port)
				addrPtr := new(string)
				*addrPtr = addr
				state.Current.Store(addrPtr)

			s, err := probeServer(gctx, srv.IP, srv.Port, timeoutFlag, loginMode, loginUserFlag, srv.VersionProtocol)
			if err != nil {
				ustats.checked.Add(1)
				// Even if the server didn't respond, record that we tried.
				// last_seen becomes "last time we attempted contact" rather
				// than "last time server was reachable".
				s = srv
				s.LastSeen = time.Now().UTC()
				if err := repo.UpsertServer(gctx, s); err != nil {
					return nil
				}
				return nil
			}

			s.FirstSeen = srv.FirstSeen
			if err := repo.UpsertServer(gctx, s); err != nil {
				ustats.checked.Add(1)
				return nil
			}

			ustats.checked.Add(1)

			if hasChanges(srv, s) {
				ustats.changed.Add(1)
				block := formatServerChanged(srv, s, styles)
				ticker.InsertAbove(block)
			}

				return nil
			}
		}(srv))
	}

	if err := g.Wait(); err != nil {
		if ctx.Err() != nil {
			ticker.Stop()
			fmt.Println("\nupdate interrupted.")
			return nil
		}
	}

	ticker.Stop()

	duration := time.Since(start)
	pps := 0.0
	if duration > 0 {
		pps = float64(ustats.checked.Load()) / duration.Seconds()
	}

	fmt.Printf("\n")
	printUpdateSummary(os.Stdout, ustats, duration, pps, styles)
	return nil
}

type updateStats struct {
	checked atomic.Int64
	changed atomic.Int64
}

type updateProgress struct {
	Total   int64
	Checked *atomic.Int64
	Changed *atomic.Int64
	Started time.Time
	Out     io.Writer
	Styles  *scanner.Styles
	State   *scanner.ProgressState
}

func (p *updateProgress) Header() string {
	return "[update]"
}

func (p *updateProgress) Render() string {
	checked := p.Checked.Load()
	if checked > p.Total {
		checked = p.Total
	}
	pct := 0.0
	if p.Total > 0 {
		pct = float64(checked) / float64(p.Total) * 100
	}
	elapsed := time.Since(p.Started)
	var eta time.Duration
	if elapsed > 0 && checked > 0 {
		pps := float64(checked) / elapsed.Seconds()
		if pps > 0 {
			eta = time.Duration(float64(p.Total-checked)/pps*float64(time.Second))
		}
	}

	const width = 30
	filledChar, emptyChar := "█", "░"
	if p.Styles != nil && p.Styles.Ascii() {
		filledChar, emptyChar = "#", "."
	}

	filled := int(float64(width) * pct / 100)
	if filled > width {
		filled = width
	}
	bar := p.Styles.BarFilled.Render(strings.Repeat(filledChar, filled)) +
		p.Styles.BarEmpty.Render(strings.Repeat(emptyChar, width-filled))

	var seedLabel string
	if p.State != nil {
		if cp := p.State.Current.Load(); cp != nil && *cp != "" {
			seedLabel = *cp
		}
	}

	changed := p.Changed.Load()
	line := fmt.Sprintf("%s  %s  %s  ETA %s  changed=%s",
		bar,
		p.Styles.Pct.Render(fmt.Sprintf("%5.1f%%", pct)),
		p.Styles.Meta.Render(fmt.Sprintf("(%d/%d)", checked, p.Total)),
		p.Styles.Eta.Render(formatDuration(eta)),
		p.Styles.Value.Render(fmt.Sprintf("%d", changed)),
	)
	if seedLabel != "" {
		line += "  " + p.Styles.Meta.Render("scanning: ") + p.Styles.Value.Render(seedLabel)
	}
	return line
}

type updateTicker struct {
	prog *updateProgress
	out  io.Writer
	done chan struct{}
	wg   sync.WaitGroup

	// opCh feeds every output operation to a single writer goroutine.
	// All cursor movement and printing happens in that goroutine, so
	// concurrent calls (ticker redraws + workers calling InsertAbove)
	// cannot interleave their escape codes on the wire.
	opCh chan tickerOp

	// outFile is kept so the writer goroutine can emit cursor control
	// codes via raw fmt.Fprintf — termenv adds overhead we don't need.
	outFile *os.File
	tty     bool
}

// tickerOp is one unit of work delivered to the writer goroutine.
type tickerOp struct {
	// block is a multi-line card to print above the bar. Empty means
	// "just refresh the bar".
	block string
}

func newUpdateTicker(prog *updateProgress) *updateTicker {
	var outFile *os.File
	isTTY := false
	if f, ok := prog.Out.(*os.File); ok {
		isTTY = term.IsTerminal(f.Fd())
		outFile = f
	}
	return &updateTicker{
		prog:    prog,
		out:     os.Stdout,
		done:    make(chan struct{}),
		opCh:    make(chan tickerOp, 128),
		outFile: outFile,
		tty:     isTTY,
	}
}

func (t *updateTicker) Start(interval time.Duration) {
	// Header sits above the bar; together they form the initial sticky
	// block. We do not use SaveCursorPosition because cursor save/restore
	// is unreliable across terminals and tends to drift after many writes.
	fmt.Fprintln(t.out, t.prog.Header())
	fmt.Fprintln(t.out, t.prog.Render())

	t.wg.Add(2)

	// Single writer goroutine. Every escape sequence — cursor up,
	// clear line, block text, bar — flows through this goroutine in
	// order, so there is zero interleaving.
	go func() {
		defer t.wg.Done()
		for op := range t.opCh {
			t.handle(op)
		}
	}()

	// Periodic redraw: just pushes an empty op so the writer repaints
	// the bar with the latest counters.
	go func() {
		defer t.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				select {
				case t.opCh <- tickerOp{}:
				case <-t.done:
					return
				}
			}
		}
	}()
}

// handle emits one block + bar reprint under the single-writer invariant.
//
// TTY layout maintained:
//   - The bar is always exactly 1 line above the cursor (i.e. the last
//     line printed before this call ended).
//   - To insert a block: move up 1 line (to the bar), clear that line,
//     then write the block, then write the bar. The block now occupies
//     the bar's old row; the bar moves down with the cursor.
//   - On a refresh op (empty block), still move up 1 and clear so the
//     bar is replaced in place instead of stacking new lines.
func (t *updateTicker) handle(op tickerOp) {
	if !t.tty {
		if op.block != "" {
			_, _ = io.WriteString(t.outFile, op.block)
		}
		return
	}
	if op.block == "" {
		// Refresh tick: cursor sits 1 line below the bar. Move up,
		// clear that line, then redraw the bar in the same row.
		_, _ = io.WriteString(t.outFile, "\x1b[1A\x1b[2K")
	} else {
		// Insert: same cursor up + clear, then write the block. The
		// block now occupies the bar's old row; the bar moves down
		// by len(block).
		_, _ = io.WriteString(t.outFile, "\x1b[1A\x1b[2K")
		_, _ = io.WriteString(t.outFile, op.block)
	}
	_, _ = io.WriteString(t.outFile, t.prog.Render())
	_, _ = io.WriteString(t.outFile, "\n")
}

func (t *updateTicker) InsertAbove(block string) {
	if block == "" {
		return
	}
	if !t.tty {
		_, _ = io.WriteString(t.outFile, block)
		return
	}
	select {
	case t.opCh <- tickerOp{block: block}:
	case <-t.done:
	}
}

func (t *updateTicker) Stop() {
	// Signal the ticker loop to exit, then close opCh so the writer
	// goroutine's `range t.opCh` loop terminates. Without closing opCh
	// the writer goroutine would block forever, leaking and keeping the
	// process alive after all work is done.
	close(t.done)
	close(t.opCh)
	t.wg.Wait()
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0:00"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func hasChanges(old, new model.Server) bool {
	return old.VersionName != new.VersionName ||
		old.MOTD != new.MOTD ||
		old.LoginMode != new.LoginMode ||
		old.Map != new.Map ||
		old.GameType != new.GameType ||
		!samePlugins(old.Plugins, new.Plugins)
}

func samePlugins(a, b []model.Plugin) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Version != b[i].Version {
			return false
		}
	}
	return true
}

func formatServerChanged(old, new model.Server, styles *scanner.Styles) string {
	var b strings.Builder

	green := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)

	ipStyle := styles.IPIdle
	if new.NumPlayers > 0 {
		ipStyle = styles.IPActive
	}

	fmt.Fprint(&b, styles.FoundPrefix.Render("[~] "))
	fmt.Fprint(&b, ipStyle.Render(new.Address()))
	fmt.Fprintln(&b)

	var rows []string

	var r1 strings.Builder
	r1.WriteString(styles.Label.Render("Version:  "))
	if old.VersionName != new.VersionName {
		r1.WriteString(green.Render(new.VersionName))
	} else {
		r1.WriteString(styles.Value.Render(new.VersionName))
	}
	if new.VersionProtocol != 0 {
		r1.WriteString(styles.Label.Render("  (proto "))
		if old.VersionProtocol != new.VersionProtocol {
			r1.WriteString(green.Render(fmt.Sprintf("%d", new.VersionProtocol)))
		} else {
			r1.WriteString(styles.Value.Render(fmt.Sprintf("%d", new.VersionProtocol)))
		}
		r1.WriteString(styles.Label.Render(")"))
	}
	r1.WriteString(styles.Label.Render("    Players:  "))
	r1.WriteString(styles.Value.Render(fmt.Sprintf("%d/%d", new.NumPlayers, new.MaxPlayers)))
	r1.WriteString(styles.Label.Render("    Latency:  "))
	r1.WriteString(styles.Value.Render(fmt.Sprintf("%dms", new.LastLatencyMs)))
	if new.ProbeKind != "" {
		r1.WriteString(styles.Label.Render("    Kind:  "))
		r1.WriteString(styles.Value.Render(new.ProbeKind))
	}
	rows = append(rows, r1.String())

	if new.MOTD != "" {
		motd := new.MOTD
		if len(motd) > 70 {
			motd = motd[:67] + "..."
		}
		var r2 strings.Builder
		r2.WriteString(styles.Label.Render("MOTD:     "))
		if old.MOTD != new.MOTD {
			r2.WriteString(green.Render(motd))
		} else {
			r2.WriteString(styles.Value.Render(motd))
		}
		rows = append(rows, r2.String())
	}

	if new.Map != "" || new.GameType != "" {
		var r3 strings.Builder
		if new.Map != "" {
			r3.WriteString(styles.Label.Render("Map:      "))
			if old.Map != new.Map {
				r3.WriteString(green.Render(new.Map))
			} else {
				r3.WriteString(styles.Value.Render(new.Map))
			}
		}
		if new.GameType != "" {
			if r3.Len() > 0 {
				r3.WriteString(styles.Label.Render("    "))
			}
			r3.WriteString(styles.Label.Render("Mode:     "))
			if old.GameType != new.GameType {
				r3.WriteString(green.Render(new.GameType))
			} else {
				r3.WriteString(styles.Value.Render(new.GameType))
			}
		}
		rows = append(rows, r3.String())
	}

	if len(new.Plugins) > 0 {
		var r4 strings.Builder
		r4.WriteString(styles.Label.Render("Plugins:  "))
		plugins := formatPlugins(new.Plugins)
		if len(plugins) > 60 {
			plugins = plugins[:57] + "..."
		}
		if !samePlugins(old.Plugins, new.Plugins) {
			r4.WriteString(green.Render(plugins))
		} else {
			r4.WriteString(styles.Value.Render(plugins))
		}
		rows = append(rows, r4.String())
	}

	if new.LoginMode != "" {
		var r5 strings.Builder
		r5.WriteString(styles.Label.Render("Login:    "))
		if old.LoginMode != new.LoginMode {
			r5.WriteString(green.Render(loginModeLabel(new.LoginMode)))
		} else {
			r5.WriteString(styles.Value.Render(loginModeLabel(new.LoginMode)))
		}
		rows = append(rows, r5.String())
	}

	content := strings.Join(rows, "\n")
	fmt.Fprintln(&b, styles.Card.Render(content))
	fmt.Fprintln(&b)
	return b.String()
}

func formatPlugins(plugins []model.Plugin) string {
	var parts []string
	for _, p := range plugins {
		if p.Version != "" {
			parts = append(parts, p.Name+" "+p.Version)
		} else {
			parts = append(parts, p.Name)
		}
	}
	return strings.Join(parts, "; ")
}

func printUpdateSummary(w io.Writer, stats *updateStats, d time.Duration, pps float64, styles *scanner.Styles) {
	ruleStyle := styles.Meta
	rule := ruleStyle.Render(strings.Repeat("─", 56))

	fmt.Fprintln(w, rule)
	fmt.Fprintf(w, "%s\n", styles.Pct.Render("  Update Summary"))
	fmt.Fprintln(w, rule)

	rows := [][2]string{
		{"Checked", fmt.Sprintf("%d", stats.checked.Load())},
		{"Changed", fmt.Sprintf("%d", stats.changed.Load())},
		{"Duration", d.Round(time.Second).String()},
		{"Throughput", fmt.Sprintf("%.1f pps", pps)},
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %s  %s\n",
			styles.Label.Render(fmt.Sprintf("%-10s", r[0]+":")),
			styles.Value.Render(r[1]))
	}
	fmt.Fprintln(w, rule)
}

func probeServer(ctx context.Context, host string, port uint16, timeout time.Duration, loginMode login.StealthMode, loginUser string, protoVersion int) (model.Server, error) {
	open, err := probe(ctx, host, port, timeout)
	if err != nil {
		return model.Server{}, err
	}
	if !open {
		return model.Server{}, fmt.Errorf("port closed")
	}

	resp, latency, err := slp.Ping(ctx, host, port, timeout)
	if err != nil {
		var legacy *slp.LegacyResponse
		var legacyLatency time.Duration
		if isConnReset(err) {
			legacy, legacyLatency, err = slp.PingLegacy(ctx, host, port, timeout)
		}
		if legacy != nil && err == nil {
			return model.Server{
				IP:              host,
				Port:            port,
				LastSeen:        time.Now().UTC(),
				LastLatencyMs:   legacyLatency.Milliseconds(),
				VersionName:     legacy.Version,
				MOTD:            legacy.MOTD,
				NumPlayers:       legacy.NumPlayers,
				MaxPlayers:      legacy.MaxPlayers,
				QueryEnabled:    false,
				ProbeKind:       model.ProbeLegacy,
			}, nil
		}
		return model.Server{}, err
	}

	s := model.Server{
		IP:              host,
		Port:            port,
		LastSeen:        time.Now().UTC(),
		LastLatencyMs:   latency.Milliseconds(),
		VersionProtocol: resp.Version.Protocol,
		VersionName:     resp.Version.Name,
		MOTD:            resp.Description,
		NumPlayers:      resp.Players.Online,
		MaxPlayers:      resp.Players.Max,
		QueryEnabled:    false,
		ProbeKind:       model.ProbeSLP,
	}

	if len(resp.Players.Sample) > 0 {
		s.SamplePlayers = make([]model.SamplePlayer, len(resp.Players.Sample))
		for i, p := range resp.Players.Sample {
			s.SamplePlayers[i] = model.SamplePlayer{Name: p.Name, ID: p.ID}
		}
	}

	qr, _, qerr := query.Query(ctx, host, port, timeout)
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
	}

	if loginMode != login.StealthOff {
		pv := int32(login.ProtocolVersionLegacy())
		if protoVersion > 0 {
			pv = int32(protoVersion)
		}
		lr, lerr := login.Handshake(ctx, host, port, timeout, loginUser, pv, loginMode, slog.Default())
		if lerr != nil {
			slog.Debug("login probe failed", "host", host, "port", port, "err", lerr)
		}
		if lr != nil {
			modeStr := lr.Mode.String()
			if lr.Mode.IsPersistent() {
				s.LoginMode = modeStr
			} else {
				s.LastLogin = modeStr
			}
		}
	}

	return s, nil
}

func probe(ctx context.Context, host string, port uint16, timeout time.Duration) (bool, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		if isConnRefused(err) {
			return false, nil
		}
		return false, err
	}
	defer conn.Close()
	return true, nil
}

func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return isConnRefused(opErr.Err)
	}
	return strings.Contains(err.Error(), "connection refused")
}

func isConnReset(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "reset by peer") || strings.Contains(err.Error(), "connection reset")
}

func updateUsage() string {
	var b strings.Builder
	b.WriteString("doorsniff update — re-probe known servers to refresh their data\n\n")
	b.WriteString("Usage:\n  doorsniff update [flags]\n\n")
	b.WriteString("Flags:\n")
	b.WriteString("  --version VER    filter by version (e.g. 1.21.1,26.1.2)\n")
	b.WriteString("  --engine ENG     filter by server engine, case-insensitive (e.g. paper, purpur)\n")
	b.WriteString("  --players EXPR   filter by player count (e.g. >1, 1-10, <50)\n")
	b.WriteString("  --maxplayers EXPR filter by max players (e.g. >=100, 50-200)\n")
	b.WriteString("  --port PORT      filter by port or range (e.g. 25565 or 25500-25600)\n")
	b.WriteString("  --motd TEXT      search MOTD for keywords\n")
	b.WriteString(fmt.Sprintf("  --qps N          QPS limit (default %d)\n", config.DefaultUpdateQPS))
	b.WriteString(fmt.Sprintf("  --workers N       worker pool size (default %d)\n", config.DefaultUpdateWorkers))
	b.WriteString(fmt.Sprintf("  --timeout DUR    per-probe timeout (default %s)\n", config.DefaultUpdateTimeout))
	b.WriteString(fmt.Sprintf("  --limit N        max servers to update (default %d, 0 = all)\n", config.DefaultUpdateLimit))
	b.WriteString("  --database PATH  override database path\n")
	b.WriteString("  --login          DEPRECATED, prefer --login-mode — keeps legacy bool behaviour\n")
	b.WriteString(fmt.Sprintf("  --login-mode MODE  off | detect | intrusive (default %s)\n", config.DefaultUpdateLoginMode))
	b.WriteString(fmt.Sprintf("  --login-user STR username for login handshake (default %s)\n", config.DefaultLoginUser))
	b.WriteString("  --debug-login    dump raw login packet bytes + disconnect reasons to stderr\n")
	b.WriteString("  --ascii          plain output (no colour, ASCII border and bar)\n")
	b.WriteString("\nServers are updated oldest-first (by last_seen).\n")
	b.WriteString("\nExamples:\n")
	b.WriteString("  doorsniff update\n")
	b.WriteString("  doorsniff update --version=1.21.1 --engine=paper\n")
	b.WriteString("  doorsniff update --players='>0' --maxplayers='>=50'\n")
	b.WriteString("  doorsniff update --login-mode=intrusive\n")
	return b.String()
}
