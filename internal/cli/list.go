package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/renanbray/doorsniff/internal/config"
	"github.com/renanbray/doorsniff/internal/model"
	"github.com/renanbray/doorsniff/internal/storage"
)

var tableHeaders = []string{"#", "ADDRESS", "VERSION", "LOGIN", "PLAYERS", "LATENCY", "LAST SEEN", "MOTD", "MAP"}

var tableWidths = []int{5, 22, 20, 10, 10, 9, 20, 36, 16}

var availableColumns = map[string]struct {
	header string
	width  int
}{
	"ordinal":   {"#", 5},
	"address":    {"ADDRESS", 22},
	"version":    {"VERSION", 20},
	"login":     {"LOGIN", 10},
	"players":   {"PLAYERS", 10},
	"latency":   {"LATENCY", 9},
	"last_seen": {"LAST SEEN", 20},
	"motd":      {"MOTD", 36},
	"map":       {"MAP", 16},
	"country":     {"COUNTRY", 10},
	"plugins":     {"PLUGINS", 20},
	"protocol":    {"PROTOCOL", 10},
	"player_names": {"PLAYER NAMES", 30},
}

var emptyCellStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

func runList(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

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
		playerFlag     string
		mapFlag        string
		countryFlag    string
		latencyFlag    string
		sinceFlag      string
		pluginFlag     string
		protocolFlag   string
		addressFlag    string
	)

	fs.StringVar(&versionFlag, "version", "", "filter by version (e.g. 1.21.1 or 1.21.1,26.1.2)")
	fs.StringVar(&engineFlag, "engine", "", "filter by engine, case-insensitive (e.g. paper, purpur)")
	fs.StringVar(&playersFlag, "players", "", "filter by player count (e.g. >1, 1-10, <50)")
	fs.StringVar(&maxPlayersFlag, "maxplayers", "", "filter by max players (e.g. >=100, 50-200)")
	fs.StringVar(&portFlag, "port", "", "filter by port or range (e.g. 25565 or 25500-25600)")
	fs.StringVar(&motdFlag, "motd", "", "search MOTD for keywords")
	fs.StringVar(&loginModeFlag, "login", "", "filter by login mode (cracked, premium, whitelist, banned, kicked, full)")
	fs.StringVar(&sortFlag, "sort", cfg.ListSort, "sort by: players, latency, version, port, recent, oldest, login")
	fs.IntVar(&limitFlag, "limit", cfg.ListLimit, "max results to show (0 = no limit)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")
	fs.StringVar(&playerFlag, "player", "", "filter by player name (substring match, comma-separated for OR)")
	fs.StringVar(&mapFlag, "map", "", "filter by map/world name (substring match)")
	fs.StringVar(&countryFlag, "country", "", "filter by country (substring match)")
	fs.StringVar(&latencyFlag, "latency", "", "filter by latency in ms (e.g. <100, 50-200, >300)")
	fs.StringVar(&sinceFlag, "since", "", "filter by last seen (e.g. 1h, 2d, unix timestamp, or ISO date)")
	fs.StringVar(&pluginFlag, "plugin", "", "filter by plugin name (substring match, comma-separated for OR)")
	fs.StringVar(&protocolFlag, "protocol", "", "filter by protocol version (exact match)")
	fs.StringVar(&addressFlag, "address", "", "filter by IP address (substring match)")

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

	latencyFilter, err := ParsePlayerFilter(latencyFlag)
	if err != nil {
		return fmt.Errorf("invalid --latency: %w", err)
	}
	latencySQL, latencyArgs := latencyFilter.ToSQLColumn("last_latency_ms")

	sinceTime, err := ParseSinceFilter(sinceFlag)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}

	protocol, err := ParseProtocol(protocolFlag)
	if err != nil {
		return fmt.Errorf("invalid --protocol: %w", err)
	}

	playerNames := ParsePlayerNames(playerFlag)
	pluginNames := ParsePluginNames(pluginFlag)

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
		PlayerSQL:     playerSQL,
		PlayerArgs:    playerArgs,
		MaxPlayerSQL:  maxPlayerSQL,
		MaxPlayerArgs: maxPlayerArgs,
		Versions:      versions,
		Engine:        engineFlag,
		MOTD:          motdFlag,
		LoginMode:     normalizeLoginMode(loginModeFlag),
		PortLo:        portLo,
		PortHi:        portHi,
		OrderBy:       orderBy,
		Limit:         limitFlag,
		Player:        playerNames,
		Map:           mapFlag,
		Country:       countryFlag,
		LatencySQL:    latencySQL,
		LatencyArgs:   latencyArgs,
		Since:         sinceTime,
		Plugin:        pluginNames,
		Protocol:      protocol,
		Address:       addressFlag,
	}

	servers, err := repo.ListServersFiltered(ctx, query)
	if err != nil {
		return fmt.Errorf("querying servers: %w", err)
	}

	if len(servers) == 0 {
		fmt.Println("no servers found matching the filters.")
		return nil
	}

	// Validate columns
	validatedColumns, colWarnings := validateListColumns(cfg.ListColumns)
	for _, w := range colWarnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}

	// Validate date format
	validatedDateFormat, dateWarnings := validateDateFormat(cfg.ListDateFormat)
	for _, w := range dateWarnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}

	printServerTable(os.Stdout, servers, validatedColumns, validatedDateFormat, cfg)
	return nil
}

func printServerTable(w io.Writer, servers []model.Server, columns []string, dateFormat string, cfg *config.Config) {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	// Build headers and widths based on selected columns
	headers := make([]string, len(columns))
	widths := make([]int, len(columns))
	for i, col := range columns {
		info := availableColumns[col]
		headers[i] = info.header
		widths[i] = info.width
	}

	// Build plain string rows.
	rows := make([][]string, len(servers))
	for i, s := range servers {
		motd := s.MOTD
		if len(motd) > cfg.MaxMotdDisplayLen {
			motd = motd[:cfg.MaxMotdDisplayLen-3] + "..."
		}
		motd = strings.ReplaceAll(motd, "\n", " ")

		row := make([]string, len(columns))
		for j, col := range columns {
			switch col {
			case "ordinal":
				row[j] = fmt.Sprintf("%d", i+1)
			case "address":
				row[j] = s.Address()
			case "version":
				row[j] = s.VersionName
			case "login":
				row[j] = loginModeLabel(s.LoginMode)
			case "players":
				row[j] = fmt.Sprintf("%d/%d", s.NumPlayers, s.MaxPlayers)
			case "latency":
				row[j] = fmt.Sprintf("%dms", s.LastLatencyMs)
			case "last_seen":
				if s.LastSeen.IsZero() {
					row[j] = "-"
				} else {
					row[j] = s.LastSeen.Format(dateFormat)
				}
			case "motd":
				row[j] = motd
			case "map":
				row[j] = s.Map
				if row[j] == "" {
					row[j] = "-"
				}
			case "country":
				row[j] = s.Country
				if row[j] == "" {
					row[j] = "-"
				}
			case "plugins":
				if len(s.Plugins) == 0 {
					row[j] = "-"
				} else {
					pluginNames := make([]string, len(s.Plugins))
					for i, p := range s.Plugins {
						pluginNames[i] = p.Name
					}
					row[j] = strings.Join(pluginNames, ", ")
				}
			case "protocol":
				row[j] = fmt.Sprintf("%d", s.VersionProtocol)
				if row[j] == "0" {
					row[j] = "-"
				}
			case "player_names":
				if len(s.SamplePlayers) == 0 {
					row[j] = "-"
				} else {
					names := make([]string, len(s.SamplePlayers))
					for k, p := range s.SamplePlayers {
						names[k] = p.Name
					}
					joined := strings.Join(names, ", ")
					if len(joined) > cfg.MaxPlayerNamesDisplayLen {
						joined = joined[:cfg.MaxPlayerNamesDisplayLen-3] + "..."
					}
					row[j] = joined
				}
			default:
				row[j] = "-"
			}
		}
		rows[i] = row
	}

	// Find column indices for styling
	playersColIdx := slices.Index(columns, "players")
	latencyColIdx := slices.Index(columns, "latency")

	// StyleFunc applies per-cell width + conditional colors.
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if col >= len(widths) {
				return lipgloss.NewStyle()
			}
			s := lipgloss.NewStyle().Width(widths[col])

			// Header row: bold + white.
			if row == table.HeaderRow {
				return s.Foreground(lipgloss.Color("252")).Bold(true)
			}

			if row < 0 || row >= len(servers) {
				return s
			}
			server := servers[row]

			// Ordinal column: dim.
			if col == 0 {
				return s.Foreground(lipgloss.Color("245"))
			}

			// Empty cell: dim
			if col < len(rows[row]) && rows[row][col] == "-" {
				return s.Foreground(lipgloss.Color("240"))
			}

			// Players column: green when > 0.
			if col == playersColIdx && server.NumPlayers > 0 {
				return s.Foreground(lipgloss.Color("42")).Bold(true)
			}

			// Latency column: color gradient.
			if col == latencyColIdx {
				return s.Foreground(latencyColor(server.LastLatencyMs))
			}

			return s
		})

	fmt.Fprintln(w)
	fmt.Fprintln(w, t.Render())
	fmt.Fprintf(w, "  %s\n\n", dimStyle.Render(fmt.Sprintf("%d server(s) found", len(servers))))
}

func validateListColumns(columns []string) ([]string, []string) {
	var validCols []string
	var warnings []string

	allCols := make(map[string]bool)
	for col := range availableColumns {
		allCols[col] = true
	}

	for _, col := range columns {
		if allCols[col] {
			validCols = append(validCols, col)
		} else {
			warnings = append(warnings, fmt.Sprintf("unknown column: %s (ignoring)", col))
		}
	}

	if len(validCols) == 0 {
		warnings = append(warnings, "no valid columns specified, using defaults")
		validCols = config.DefaultListColumns
	}

	return validCols, warnings
}

func validateDateFormat(format string) (string, []string) {
	// Test if the format can be parsed
	now := time.Now()
	_, err := time.Parse(format, now.Format(format))
	if err != nil {
		return config.DefaultListDateFormat, []string{fmt.Sprintf("invalid date format: %s (using default)", format)}
	}
	return format, nil
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
	b.WriteString("doorsniff list — query and display found servers\n\n")
	b.WriteString("Usage:\n  doorsniff list [flags]\n\n")
	b.WriteString("Flags:\n")
	b.WriteString("  --version VER    filter by version, comma-separated (e.g. 1.21.1,26.1.2)\n")
	b.WriteString("  --engine ENG     filter by engine, case-insensitive (e.g. paper, purpur)\n")
	b.WriteString("  --players EXPR   filter by player count (e.g. >1, 1-10, <50)\n")
	b.WriteString("  --maxplayers EXPR filter by max players (e.g. >=100, 50-200)\n")
	b.WriteString("  --port PORT      filter by port or range (e.g. 25565 or 25500-25600)\n")
	b.WriteString("  --motd TEXT      search MOTD for keywords\n")
	b.WriteString("  --login MODE     filter by login mode (cracked, premium, whitelist, banned, kicked, full)\n")
	b.WriteString("  --sort KEY       sort by: players, latency, version, port, recent, oldest, login\n")
	b.WriteString("  --limit N        max results (0 = no limit)\n")
	b.WriteString("  --database PATH  override database path\n")
	b.WriteString("  --player TEXT    filter by player name (substring, comma-separated for OR)\n")
	b.WriteString("  --map TEXT       filter by map/world name (substring)\n")
	b.WriteString("  --country TEXT   filter by country (substring)\n")
	b.WriteString("  --latency EXPR   filter by latency in ms (e.g. <100, 50-200, >300)\n")
	b.WriteString("  --since DURATION filter by last seen (e.g. 1h, 48h, unix timestamp, or ISO date)\n")
	b.WriteString("  --plugin TEXT    filter by plugin name (substring, comma-separated for OR)\n")
	b.WriteString("  --protocol N     filter by protocol version (exact match)\n")
	b.WriteString("  --address TEXT   filter by IP address (substring)\n")
	b.WriteString("\nFilter operators for counts: =, >, <, >=, <=, - (range)\n")
	b.WriteString("Examples:\n")
	b.WriteString("  doorsniff list\n")
	b.WriteString("  doorsniff list --version=1.21.1 --engine=paper --players='>1'\n")
	b.WriteString("  doorsniff list --players='1-10' --sort=players\n")
	b.WriteString("  doorsniff list --login=cracked\n")
	b.WriteString("  doorsniff list --sort=login\n")
	b.WriteString("  doorsniff list --player Notch,Herobrine\n")
	b.WriteString("  doorsniff list --map world --since 2d\n")
	b.WriteString("  doorsniff list --latency <200 --country US\n")
	return b.String()
}
