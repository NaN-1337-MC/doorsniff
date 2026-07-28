package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/renanbray/doorsniff/internal/config"
	"github.com/renanbray/doorsniff/internal/model"
	"github.com/renanbray/doorsniff/internal/protocol/login"
	"github.com/renanbray/doorsniff/internal/scanner"
	"github.com/renanbray/doorsniff/internal/storage"
)

// probeConfig is the fully-resolved configuration for a single
// `doorsniff probe` invocation. It is the result of merging CLI
// flags, environment, and the user config file, and the input
// to the rest of the probe pipeline.
type probeConfig struct {
	portsLo     uint16
	portsHi     uint16
	qps         int
	workers     int
	timeout     time.Duration
	verbose     bool
	ascii       bool
	resume      bool
	fileFlag    string
	seedsDir    string
	dbPath      string
	login       bool
	loginMode   login.StealthMode
	loginUser   string
}

// runProbe is the entry point for `doorsniff probe`. It loads config,
// opens the database, kicks off the scanner against the seeds
// directory, and prints a summary on exit.
func runProbe(args []string) error {
	pc, err := parseProbeFlags(args)
	if err != nil {
		return err
	}
	if pc == nil {
		// --help was printed.
		return nil
	}

	setupLogging(pc.verbose)
	styles := scanner.NewStyles(pc.ascii)
	printHeader(os.Stdout, styles, pc)

	repo, dbClose, err := openProbeRepo(pc.dbPath)
	if err != nil {
		return err
	}
	defer dbClose()

	stats := runAndSummarize(os.Stdout, pc, repo)
	if stats != nil {
		printSummary(os.Stdout, stats, pc.dbPath, styles)
	}
	return nil
}

// parseProbeFlags resolves flags, environment, and config into a
// single probeConfig. It does not open the database or start any
// goroutines — pure parsing.
func parseProbeFlags(args []string) (*probeConfig, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		portsFlag        string
		qpsFlag          int
		workersFlag      int
		timeoutFlag      time.Duration
		verboseFlag      bool
		verboseShortFlag bool
		asciiFlag        bool
		databaseFlag     string
		seedsFlag        string
		fileFlag         string
		resumeFlag       bool
		loginFlag        bool
		loginModeFlag    string
		loginUserFlag    string
	)
	fs.StringVar(&portsFlag, "ports", cfg.Ports, "port range, e.g. 25565, 25500-25600, 1-65535")
	fs.IntVar(&qpsFlag, "qps", cfg.ProbeQPS, "global QPS limit")
	fs.IntVar(&workersFlag, "workers", cfg.ProbeWorkers, "worker pool size")
	fs.DurationVar(&timeoutFlag, "timeout", cfg.ProbeTimeout, "per-probe timeout")
	fs.BoolVar(&verboseFlag, "verbose", false, "verbose (per-target error lines + debug logging)")
	fs.BoolVar(&verboseShortFlag, "v", false, "short alias for --verbose")
	fs.BoolVar(&asciiFlag, "ascii", cfg.ASCII, "plain output (no colour, ASCII border and bar)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")
	fs.StringVar(&seedsFlag, "seeds", "", "override seeds directory")
	fs.StringVar(&fileFlag, "file", "", "read seeds from a single file instead of the seeds directory")
	fs.BoolVar(&resumeFlag, "resume", cfg.Resume, "skip seeds that were fully scanned in a previous run")
	fs.BoolVar(&loginFlag, "login", cfg.ProbeLogin, "perform login handshake to detect online/offline mode (deprecated: prefer --login-mode)")
	fs.StringVar(&loginModeFlag, "login-mode", cfg.ProbeLoginMode, "login handshake aggressiveness: off | detect | intrusive")
	fs.StringVar(&loginUserFlag, "login-user", cfg.LoginUser, "username for login handshake")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, probeUsage())
			return nil, nil // sentinel: caller treats as no-op
		}
		return nil, fmt.Errorf("parsing flags: %w", err)
	}

	lo, hi, err := scanner.ParsePortRange(portsFlag)
	if err != nil {
		return nil, fmt.Errorf("parsing --ports: %w", err)
	}

	loginMode, err := login.ParseStealthMode(loginModeFlag)
	if err != nil {
		return nil, fmt.Errorf("parsing --login-mode: %w", err)
	}

	// --login=false is the explicit "no handshake" request; map it
	// to mode=off regardless of --login-mode. The legacy --login
	// flag is honoured otherwise so existing configs keep working.
	if !loginFlag {
		loginMode = login.StealthOff
	}

	seedsDir := cfg.SeedsDir
	if seedsFlag != "" {
		seedsDir = seedsFlag
	}
	dbPath := cfg.DatabasePath
	if databaseFlag != "" {
		dbPath = databaseFlag
	}

	return &probeConfig{
		portsLo:   lo,
		portsHi:   hi,
		qps:       qpsFlag,
		workers:   workersFlag,
		timeout:   timeoutFlag,
		verbose:   verboseFlag || verboseShortFlag,
		ascii:     asciiFlag,
		resume:    resumeFlag,
		fileFlag:  fileFlag,
		seedsDir:  seedsDir,
		dbPath:    dbPath,
		login:     loginMode != login.StealthOff,
		loginMode: loginMode,
		loginUser: loginUserFlag,
	}, nil
}

// openProbeRepo opens the database, runs schema migrations, and
// returns a Repository. The returned close func must be called
// once to release the connection pool.
func openProbeRepo(dbPath string) (*storage.Repository, func(), error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		stop()
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	closeFn := func() {
		_ = db.Close()
		stop()
	}

	if err := db.EnsureSchema(ctx); err != nil {
		closeFn()
		return nil, nil, fmt.Errorf("ensuring schema: %w", err)
	}

	return storage.NewRepository(db), closeFn, nil
}

// runAndSummarize resolves seeds from all sources (file, dir, or
// default), runs the scan, and returns the resulting stats.
// Cancellation is logged but not treated as an error (Ctrl+C is the
// expected way to stop a scan).
func runAndSummarize(w io.Writer, pc *probeConfig, repo *storage.Repository) *scanner.Stats {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Resolve seeds from the highest-priority source first.
	var seeds []model.Seed
	switch {
	case pc.fileFlag != "":
		s, err := scanner.LoadSeedFile(pc.fileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] loading seeds from %q: %v\n", pc.fileFlag, err)
			return nil
		}
		if len(s) == 0 {
			fmt.Fprintf(os.Stderr, "[ERROR] no seeds found in %q\n", pc.fileFlag)
			return nil
		}
		seeds = s
	default:
		s, err := scanner.LoadSeeds(pc.seedsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] loading seeds from %q: %v\n", pc.seedsDir, err)
			return nil
		}
		if len(s) == 0 {
			fmt.Fprintf(os.Stderr, "[ERROR] no seeds found in %q\n", pc.seedsDir)
			return nil
		}
		seeds = s
	}

	stats, err := scanner.RunScan(ctx, scanner.Options{
		Workers:    pc.workers,
		QPS:        pc.qps,
		Timeout:    pc.timeout,
		PortsLo:    pc.portsLo,
		PortsHi:    pc.portsHi,
		Repo:       repo,
		Seeds:      seeds,
		Verbose:    pc.verbose,
		ASCII:      pc.ascii,
		Resume:     pc.resume,
		Login:      pc.login,
		LoginMode:  pc.loginMode,
		LoginUser:  pc.loginUser,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "[ERROR] scanner: %v\n", err)
	}
	return stats
}

// printHeader writes the seeds/db banner above the progress bar.
func printHeader(w io.Writer, styles *scanner.Styles, pc *probeConfig) {
	seedsLabel := pc.seedsDir
	if pc.fileFlag != "" {
		seedsLabel = pc.fileFlag
	}
	fmt.Fprintf(w, "%s %s\n",
		styles.Label.Render("seeds:"),
		styles.Value.Render(seedsLabel))
	fmt.Fprintf(w, "%s %s\n",
		styles.Label.Render("db:   "),
		styles.Value.Render(pc.dbPath))
}

// printSummary writes the final aggregated block.
func printSummary(w io.Writer, stats *scanner.Stats, dbPath string, styles *scanner.Styles) {
	titleStyle := styles.Pct
	keyStyle := styles.Label
	accent := styles.Eta
	ruleStyle := styles.Meta

	rule := ruleStyle.Render(strings.Repeat("─", 56))

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, rule)
	fmt.Fprintf(w, "%s\n", titleStyle.Render("  Summary"))
	fmt.Fprintln(w, rule)

	pps := 0.0
	if stats.Duration > 0 {
		pps = float64(stats.Probed.Load()) / stats.Duration.Seconds()
	}

	rows := [][2]string{
		{"Probed", fmt.Sprintf("%d", stats.Probed.Load())},
		{"Found", fmt.Sprintf("%d", stats.Found.Load())},
		{"Errors", fmt.Sprintf("%d", stats.Errors.Load())},
		{"Duration", stats.Duration.Round(time.Second).String()},
		{"Throughput", fmt.Sprintf("%.1f pps", pps)},
		{"Saved to", dbPath},
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %s  %s\n",
			keyStyle.Render(fmt.Sprintf("%-11s", r[0]+":")),
			accent.Render(r[1]))
	}
	fmt.Fprintln(w, rule)
}

func probeUsage() string {
	var b strings.Builder
	b.WriteString("doorsniff probe — run a scan against the configured seeds\n\n")
	b.WriteString("Usage:\n  doorsniff probe [flags]\n\n")
	b.WriteString("Flags:\n")
	b.WriteString(fmt.Sprintf("  --ports RANGE       port range (default %s)\n", config.DefaultPorts))
	b.WriteString(fmt.Sprintf("  --qps N             global QPS limit (default %d)\n", config.DefaultProbeQPS))
	b.WriteString(fmt.Sprintf("  --workers N         worker pool size (default %d)\n", config.DefaultProbeWorkers))
	b.WriteString(fmt.Sprintf("  --timeout DUR       per-probe timeout (default %s)\n", config.DefaultProbeTimeout))
	b.WriteString("  --file PATH         read seeds from a single file\n")
	b.WriteString("  --resume            skip seeds that were fully scanned in a previous run\n")
	b.WriteString("  --seeds DIR         override seeds directory\n")
	b.WriteString("  --database PATH     override database path\n")
	b.WriteString("  --verbose, -v       show per-target error lines (timeouts, SLP failures)\n")
	b.WriteString("  --ascii             plain output (no colour, ASCII border and bar)\n")
	b.WriteString("  --login             DEPRECATED, prefer --login-mode — keeps legacy bool behaviour\n")
	b.WriteString(fmt.Sprintf("  --login-mode MODE   off | detect | intrusive (default %s)\n", config.DefaultProbeLoginMode))
	b.WriteString(fmt.Sprintf("  --login-user STR    username for login handshake (default %s)\n", config.DefaultLoginUser))
	return b.String()
}

func setupLogging(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
