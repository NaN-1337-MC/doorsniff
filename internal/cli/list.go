package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/renanbray/swoomps/internal/config"
	"github.com/renanbray/swoomps/internal/model"
	"github.com/renanbray/swoomps/internal/storage"
)

var tableHeaders = []string{"#", "ADDRESS", "VERSION", "LOGIN", "PLAYERS", "LATENCY", "LAST SEEN", "MOTD", "MAP"}

var tableWidths = []int{5, 22, 20, 10, 10, 9, 20, 36, 16}

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		versionFlag    string
		engineFlag     string
		playersFlag    string
		maxPlayersFlag string
		portFlag       string
		motdFlag       string
		loginModeFlag  string
		sortFlag       string
		limitFlag      int
		databaseFlag   string
	)

	fs.StringVar(&versionFlag, "version", "", "filter by version (e.g. 1.21.1 or 1.21.1,26.1.2)")
	fs.StringVar(&engineFlag, "engine", "", "filter by server engine (e.g. paper, purpur)")
	fs.StringVar(&playersFlag, "players", "", "filter by player count (e.g. >1, 1-10, <50)")
	fs.StringVar(&maxPlayersFlag, "maxplayers", "", "filter by max players (e.g. >=100, 50-200)")
	fs.StringVar(&portFlag, "port", "", "filter by port (e.g. 25565 or 25500-25600)")
	fs.StringVar(&motdFlag, "motd", "", "search MOTD for keywords")
	fs.StringVar(&loginModeFlag, "login", "", "filter by login mode (cracked, premium, whitelist, banned, kicked, full)")
	fs.StringVar(&sortFlag, "sort", "recent", "sort by: players, latency, version, port, recent, oldest, login")
	fs.IntVar(&limitFlag, "limit", 50, "max results to show")
	fs.StringVar(&databaseFlag, "database", "", "override database path")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, listUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

	orderBy, err := ParseSortKey(sortFlag)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	playerFilter, err := ParsePlayerFilter(playersFlag)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	playerSQL, playerArgs := playerFilter.ToSQL()

	maxPlayerFilter, err := ParsePlayerFilter(maxPlayersFlag)
	if err != nil {
		return fmt.Errorf("invalid --maxplayers: %w", err)
	}
	maxPlayerSQL, maxPlayerArgs := maxPlayerFilter.ToSQLColumn("max_players")

	portLo, portHi, err := ParsePortFilter(portFlag)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	versions := ParseVersionList(versionFlag)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
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

	repo := storage.NewRepository(db)

	query := storage.ListQuery{
		PlayerSQL:    playerSQL,
		PlayerArgs:   playerArgs,
		MaxPlayerSQL: maxPlayerSQL,
		MaxPlayerArgs: maxPlayerArgs,
		Versions:     versions,
		Engine:       engineFlag,
		MOTD:         motdFlag,
		LoginMode:    normalizeLoginMode(loginModeFlag),
		PortLo:       portLo,
		PortHi:       portHi,
		OrderBy:      orderBy,
		Limit:        limitFlag,
	}

	servers, err := repo.ListServersFiltered(ctx, query)
	if err != nil {
		return fmt.Errorf("querying servers: %w", err)
	}

	if len(servers) == 0 {
		fmt.Println("no servers found matching the filters.")
		return nil
	}

	printServerTable(os.Stdout, servers)
	return nil
}

func printServerTable(w io.Writer, servers []model.Server) {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	// Build plain string rows.
	rows := make([][]string, len(servers))
	for i, s := range servers {
		motd := s.MOTD
		if len(motd) > 42 {
			motd = motd[:39] + "..."
		}
		motd = strings.ReplaceAll(motd, "\n", " ")

		rows[i] = []string{
			fmt.Sprintf("%d", i+1),
			s.Address(),
			s.VersionName,
			loginModeLabel(s.LoginMode),
			fmt.Sprintf("%d/%d", s.NumPlayers, s.MaxPlayers),
			fmt.Sprintf("%dms", s.LastLatencyMs),
			s.LastSeen.Format("2006-01-02 15:04"),
			motd,
			s.Map,
		}
	}

	// StyleFunc applies per-cell width + conditional colors.
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(tableHeaders...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Width(tableWidths[col])

			// Header row: bold + white.
			if row == table.HeaderRow {
				return s.Foreground(lipgloss.Color("252")).Bold(true)
			}

			server := servers[row]

			// Ordinal column: dim.
			if col == 0 {
				return s.Foreground(lipgloss.Color("245"))
			}

			// Players column: green when > 0.
			if col == 4 && server.NumPlayers > 0 {
				return s.Foreground(lipgloss.Color("42")).Bold(true)
			}

			// Latency column: color gradient.
			if col == 5 {
				return s.Foreground(latencyColor(server.LastLatencyMs))
			}

			return s
		})

	fmt.Fprintln(w)
	fmt.Fprintln(w, t.Render())
	fmt.Fprintf(w, "  %s\n\n", dimStyle.Render(fmt.Sprintf("%d server(s) found", len(servers))))
}

func loginModeLabel(mode string) string {
	switch mode {
	case "offline":
		return "cracked"
	case "online":
		return "premium"
	case "whitelisted":
		return "whitelist"
	case "banned":
		return "banned"
	case "kicked":
		return "kicked"
	case "full":
		return "full"
	case "disconnect":
		return "disconnect"
	default:
		return "-"
	}
}

func normalizeLoginMode(s string) string {
	switch strings.ToLower(s) {
	case "cracked", "offline":
		return "offline"
	case "premium", "online":
		return "online"
	case "whitelist", "whitelisted":
		return "whitelisted"
	case "banned", "ban":
		return "banned"
	case "kicked", "kick":
		return "kicked"
	case "full":
		return "full"
	default:
		return ""
	}
}

func latencyColor(ms int64) lipgloss.Color {
	switch {
	case ms < 200:
		return lipgloss.Color("42")
	case ms < 350:
		return lipgloss.Color("112")
	case ms < 500:
		return lipgloss.Color("220")
	case ms < 750:
		return lipgloss.Color("208")
	default:
		return lipgloss.Color("196")
	}
}

func listUsage() string {
	var b strings.Builder
	b.WriteString("swoomps list — query and display found servers\n\n")
	b.WriteString("Usage:\n  swoomps list [flags]\n\n")
	b.WriteString("Flags:\n")
	b.WriteString("  --version VER   filter by version, comma-separated (e.g. 1.21.1,26.1.2)\n")
	b.WriteString("  --engine ENG    filter by engine, case-insensitive (e.g. paper, purpur)\n")
	b.WriteString("  --players EXPR  filter by player count (e.g. >1, 1-10, <50)\n")
	b.WriteString("  --maxplayers EXPR filter by max players (e.g. >=100, 50-200)\n")
	b.WriteString("  --port PORT     filter by port or range (e.g. 25565 or 25500-25600)\n")
	b.WriteString("  --motd TEXT     search MOTD for keywords\n")
	b.WriteString("  --login MODE   filter by login mode (cracked, premium, whitelist, banned, kicked, full)\n")
	b.WriteString("  --sort KEY      sort by: players, latency, version, port, recent, oldest, login\n")
	b.WriteString("  --limit N       max results (default 50)\n")
	b.WriteString("  --database PATH override database path\n")
	b.WriteString("\nPlayer filter operators: =, >, <, >=, <=, - (range)\n")
	b.WriteString("Examples:\n")
	b.WriteString("  swoomps list\n")
	b.WriteString("  swoomps list --version=1.21.1 --engine=paper --players='>1'\n")
	b.WriteString("  swoomps list --players='1-10' --sort=players\n")
	b.WriteString("  swoomps list --login=cracked\n")
	b.WriteString("  swoomps list --sort=login\n")
	return b.String()
}
