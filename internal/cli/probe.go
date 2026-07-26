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

	"github.com/renanbray/swoomps/internal/config"
	"github.com/renanbray/swoomps/internal/scanner"
	"github.com/renanbray/swoomps/internal/storage"
)

// runProbe is the entry point for `swoomps probe`. It loads config,
// opens the database, kicks off the scanner against the seeds
// directory, and prints a summary on exit.
func runProbe(args []string) error {
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
	)
	fs.StringVar(&portsFlag, "ports", "1-65535", "port range, e.g. 25565, 25500-25600, 1-65535")
	fs.IntVar(&qpsFlag, "qps", 50, "global QPS limit")
	fs.IntVar(&workersFlag, "workers", 64, "worker pool size")
	fs.DurationVar(&timeoutFlag, "timeout", 3*time.Second, "per-probe timeout")
	fs.BoolVar(&verboseFlag, "verbose", false, "verbose (per-target error lines + debug logging)")
	fs.BoolVar(&verboseShortFlag, "v", false, "short alias for --verbose")
	fs.BoolVar(&asciiFlag, "ascii", false, "plain output (no colour, ASCII border and bar)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")
	fs.StringVar(&seedsFlag, "seeds", "", "override seeds directory")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, probeUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

	verbose := verboseFlag || verboseShortFlag

	lo, hi, err := scanner.ParsePortRange(portsFlag)
	if err != nil {
		return fmt.Errorf("parsing --ports: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	seedsDir := cfg.SeedsDir
	if seedsFlag != "" {
		seedsDir = seedsFlag
	}
	dbPath := cfg.DatabasePath
	if databaseFlag != "" {
		dbPath = databaseFlag
	}

	setupLogging(verbose)

	styles := scanner.NewStyles(asciiFlag)

	fmt.Fprintf(os.Stdout, "%s %s\n",
		styles.Label.Render("seeds:"),
		styles.Value.Render(seedsDir))
	fmt.Fprintf(os.Stdout, "%s %s\n",
		styles.Label.Render("db:   "),
		styles.Value.Render(dbPath))

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

	stats, err := scanner.RunScan(ctx, scanner.Options{
		Workers:  workersFlag,
		QPS:      qpsFlag,
		Timeout:  timeoutFlag.String(),
		PortsLo:  lo,
		PortsHi:  hi,
		Repo:     repo,
		SeedsDir: seedsDir,
		Verbose:  verbose,
		ASCII:    asciiFlag,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "[ERROR] scanner: %v\n", err)
	}

	printSummary(os.Stdout, stats, dbPath, styles)

	return nil
}

// printSummary writes the final aggregated block.
func printSummary(w io.Writer, stats scanner.Stats, dbPath string, styles *scanner.Styles) {
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
	b.WriteString("swoomps probe — run a scan against the configured seeds\n\n")
	b.WriteString("Usage:\n  swoomps probe [flags]\n\n")
	b.WriteString("Flags:\n")
	b.WriteString("  --ports RANGE       port range (default 1-65535)\n")
	b.WriteString("  --qps N             global QPS limit (default 50)\n")
	b.WriteString("  --workers N         worker pool size (default 64)\n")
	b.WriteString("  --timeout DUR       per-probe timeout (default 3s)\n")
	b.WriteString("  --seeds DIR         override seeds directory\n")
	b.WriteString("  --database PATH     override database path\n")
	b.WriteString("  --verbose, -v       show per-target error lines (timeouts, SLP failures)\n")
	b.WriteString("  --ascii             plain output (no colour, ASCII border and bar)\n")
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
