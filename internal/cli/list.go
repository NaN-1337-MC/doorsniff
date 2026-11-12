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
	"strconv"
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
	"last_login": {"LAST LOGIN", 12},
	"players":   {"PLAYERS", 10},
	"latency":   {"LATENCY", 9},
	"last_seen": {"LAST SEEN", 20},
	"motd":      {"MOTD", 36},
	"map":       {"MAP", 16},
	"country":     {"COUNTRY", 10},
	"plugins":     {"PLUGINS", 20},
	"protocol":    {"PROTOCOL", 10},
	"player_names": {"PLAYER NAMES", 30},
	"last_activity": {"LAST ACTIVITY", 20},
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
		reverseFlag    bool
		limitFlag      int
		databaseFlag   string
		playerFlag     string
		mapFlag        string
		countryFlag    string
		latencyFlag    string
		sinceFlag      string
		activeFlag     string
		pluginFlag     string
		protocolFlag   string
		addressFlag    string
		lastLoginFlag  string
	)

	fs.StringVar(&versionFlag, "version", "", "filter by version (e.g. 1.21.1 or 1.21.1,26.1.2)")
	fs.StringVar(&engineFlag, "engine", "", "filter by engine, case-insensitive (e.g. paper, purpur)")
	fs.StringVar(&playersFlag, "players", "", "filter by player count (e.g. >1, 1-10, <50)")
	fs.StringVar(&maxPlayersFlag, "maxplayers", "", "filter by max players (e.g. >=100, 50-200)")
	fs.StringVar(&portFlag, "port", "", "filter by port or range (e.g. 25565 or 25500-25600)")
	fs.StringVar(&motdFlag, "motd", "", "search MOTD for keywords")
	fs.StringVar(&loginModeFlag, "login", "", "filter by login mode (cracked, premium, whitelist, banned, kicked, full)")
	fs.StringVar(&sortFlag, "sort", cfg.ListSort, "sort by: players, latency, version, port, recent, oldest, login")
	fs.BoolVar(&reverseFlag, "reverse", false, "reverse sort order")
	fs.IntVar(&limitFlag, "limit", cfg.ListLimit, "max results to show (0 = no limit)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")
	fs.StringVar(&playerFlag, "player", "", "filter by player name (substring match, comma-separated for OR)")
	fs.StringVar(&mapFlag, "map", "", "filter by map/world name (substring match)")
	fs.StringVar(&countryFlag, "country", "", "filter by country (substring match)")
	fs.StringVar(&latencyFlag, "latency", "", "filter by latency in ms (e.g. <100, 50-200, >300)")
	fs.StringVar(&sinceFlag, "since", "", "filter by last seen (e.g. 1h, 2d, unix timestamp, or ISO date)")
	fs.StringVar(&activeFlag, "active", "", "filter by activity in last duration (e.g. 1h, 7d, 30m)")
	fs.StringVar(&pluginFlag, "plugin", "", "filter by plugin name (substring match, comma-separated for OR)")
	fs.StringVar(&protocolFlag, "protocol", "", "filter by protocol version (exact match)")
	fs.StringVar(&addressFlag, "address", "", "filter by IP address (substring match)")
	fs.StringVar(&lastLoginFlag, "last-login", "", "filter by last_login state (disconnect, unknown, full, kicked, banned)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, listUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

	orderBy, err := ParseSortKey(sortFlag, reverseFlag)
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

	versions := ExpandVersionToSQLPatterns(versionFlag)

	latencyFilter, err := ParsePlayerFilter(latencyFlag)
	if err != nil {
		return fmt.Errorf("invalid --latency: %w", err)
	}
	latencySQL, latencyArgs := latencyFilter.ToSQLColumn("last_latency_ms")

	sinceTime, err := ParseSinceFilter(sinceFlag)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}

	activeTime, err := ParseDurationFilter(activeFlag)
	if err != nil {
		return fmt.Errorf("invalid --active: %w", err)
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

	if err := db.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensuring schema: %w", err)
	}

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
		ActiveSince:   activeTime,
		Plugin:        pluginNames,
		Protocol:      protocol,
		Address:       addressFlag,
		LastLogin:     normalizeLastLogin(lastLoginFlag),
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
			case "last_login":
				row[j] = loginModeLabel(s.LastLogin)
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
			case "last_activity":
				if s.LastActivity.IsZero() {
					row[j] = "-"
				} else {
					row[j] = s.LastActivity.Format(dateFormat)
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

func normalizeLastLogin(s string) string {
	switch strings.ToLower(s) {
	case "disconnect":
		return "disconnect"
	case "unknown":
		return "unknown"
	case "full":
		return "full"
	case "kicked", "kick":
		return "kicked"
	case "banned", "ban":
		return "banned"
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
	b.WriteString("  --reverse        reverse sort order\n")
	b.WriteString("  --limit N        max results (0 = no limit)\n")
	b.WriteString("  --database PATH  override database path\n")
	b.WriteString("  --player TEXT    filter by player name (substring, comma-separated for OR)\n")
	b.WriteString("  --map TEXT       filter by map/world name (substring)\n")
	b.WriteString("  --country TEXT   filter by country (substring)\n")
	b.WriteString("  --latency EXPR   filter by latency in ms (e.g. <100, 50-200, >300)\n")
	b.WriteString("  --since DURATION filter by last seen (e.g. 1h, 48h, unix timestamp, or ISO date)\n")
	b.WriteString("  --active DUR     filter by last player activity (e.g. 1h, 7d, 30m)\n")
	b.WriteString("  --plugin TEXT    filter by plugin name (substring, comma-separated for OR)\n")
	b.WriteString("  --protocol N     filter by protocol version (exact match)\n")
	b.WriteString("  --address TEXT   filter by IP address (substring)\n")
	b.WriteString("  --last-login S   filter by last_login state (disconnect, unknown, full, kicked, banned)\n")
	b.WriteString("\nFilter operators for counts: =, >, <, >=, <=, - (range)\n")
	b.WriteString("Examples:\n")
	b.WriteString("  doorsniff list\n")
	b.WriteString("  doorsniff list --version=1.21.1 --engine=paper --players='>1'\n")
	b.WriteString("  doorsniff list --players='1-10' --sort=players\n")
	b.WriteString("  doorsniff list --login=cracked\n")
	b.WriteString("  doorsniff list --sort=login\n")
	b.WriteString("  doorsniff list --player Notch,Herobrine\n")
	b.WriteString("  doorsniff list --map world --since 2d\n")
	b.WriteString("  doorsniff list --active 1d\n")
	b.WriteString("  doorsniff list --latency <200 --country US\n")
	return b.String()
}

// runListServer handles `doorsniff list server <ip>`.
func runListServer(args []string) error {
	fs := flag.NewFlagSet("list server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		playerFlag  string
		sinceFlag  string
		untilFlag  string
		orderFlag  string
		limitFlag  int
		databaseFlag string
	)
	fs.StringVar(&playerFlag, "player", "", "filter by player name (comma-separated)")
	fs.StringVar(&sinceFlag, "since", "", "filter by time (e.g. 7d, 24h, unix timestamp, ISO date)")
	fs.StringVar(&untilFlag, "until", "", "filter by time until (e.g. 7d, 24h, unix timestamp, ISO date)")
	fs.StringVar(&orderFlag, "sort", "recent", "sort by: recent (default) | oldest | player")
	fs.IntVar(&limitFlag, "limit", 0, "max rows (0 = unlimited)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")

	if err := fs.Parse(rearrangeFlagsFirst(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, listServerUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: doorsniff list server <ip> [flags]")
	}

	target := fs.Args()[0]
	var ip string
	var port uint16 = 25565
	if idx := strings.Index(target, ":"); idx > 0 {
		ip = target[:idx]
		p, err := strconv.ParseUint(target[idx+1:], 10, 16)
		if err != nil {
			return fmt.Errorf("invalid port in %q", target)
		}
		port = uint16(p)
	} else {
		ip = target
	}

	since, err := ParseSinceFilter(sinceFlag)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, err := ParseUntilFilter(untilFlag)
	if err != nil {
		return fmt.Errorf("invalid --until: %w", err)
	}

	players := ParsePlayerNames(playerFlag)

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
	defer db.Close()

	if err := db.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensuring schema: %w", err)
	}
	repo := storage.NewRepository(db)

	orderForScans := orderFlag
	if orderForScans == "player" {
		orderForScans = "recent"
	}

	scanOpts := storage.ListServerScansOptions{
		Since: since,
		Until: until,
		Limit: limitFlag,
		Order: orderForScans,
	}
	scans, err := repo.ListServerScans(ctx, ip, port, scanOpts)
	if err != nil {
		return fmt.Errorf("querying server scans: %w", err)
	}

	if len(scans) == 0 {
		fmt.Println("no scan history found.")
		return nil
	}

	var playerName string
	if len(players) == 1 {
		playerName = players[0]
	}

	// Build a map of seen_at -> []player_names for this server. We always
	// pull all events in the time range so that the timeline can both:
	//   1) tell whether a given player was online (--player mode); and
	//   2) list every player present at each scan (default mode).
	playersByScan := make(map[string][]string)
	events, err := repo.GetPlayerEventsForServer(ctx, ip, port, storage.PlayerEventsOptions{
		Since: since,
		Until: until,
	})
	if err != nil {
		return fmt.Errorf("querying player events: %w", err)
	}
	for _, e := range events {
		key := e.SeenAt.Format(time.RFC3339)
		playersByScan[key] = append(playersByScan[key], e.PlayerName)
	}

	onlineTimes := make(map[string]bool)
	if playerName != "" {
		for _, e := range events {
			if e.PlayerName == playerName {
				onlineTimes[e.SeenAt.Format(time.RFC3339)] = true
			}
		}
	}

	printServerScanTimeline(os.Stdout, scans, playerName, onlineTimes, playersByScan, cfg.ListDateFormat, cfg.ListServerPlayerMax)
	return nil
}

// runListPlayer handles `doorsniff list player <name>`.
func runListPlayer(args []string) error {
	fs := flag.NewFlagSet("list player", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		serverFlag  string
		sinceFlag   string
		untilFlag   string
		orderFlag   string
		limitFlag   int
		databaseFlag string
	)
	fs.StringVar(&serverFlag, "server", "", "filter by server IP:port (comma-separated)")
	fs.StringVar(&sinceFlag, "since", "", "filter by time (e.g. 7d, 24h, unix timestamp, ISO date)")
	fs.StringVar(&untilFlag, "until", "", "filter by time until (e.g. 7d, 24h, unix timestamp, ISO date)")
	fs.StringVar(&orderFlag, "sort", "recent", "sort by: recent (default) | oldest | server")
	fs.IntVar(&limitFlag, "limit", 0, "max rows (0 = unlimited)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")

	if err := fs.Parse(rearrangeFlagsFirst(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, listPlayerUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: doorsniff list player <name> [flags]")
	}

	playerName := fs.Args()[0]

	since, err := ParseSinceFilter(sinceFlag)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, err := ParseUntilFilter(untilFlag)
	if err != nil {
		return fmt.Errorf("invalid --until: %w", err)
	}

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
	defer db.Close()

	if err := db.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensuring schema: %w", err)
	}
	repo := storage.NewRepository(db)

	opts := storage.PlayerEventsOptions{
		Since: since,
		Until: until,
		Limit: limitFlag,
		Order: orderFlag,
	}
	events, err := repo.GetPlayerEventsForPlayer(ctx, playerName, opts)
	if err != nil {
		return fmt.Errorf("querying player events: %w", err)
	}

	printPlayerEventsForPlayer(os.Stdout, events, cfg.ListDateFormat)
	return nil
}

func printPlayerEventsForServer(w io.Writer, events []model.PlayerEvent, dateFormat string) {
	if len(events) == 0 {
		fmt.Println("no player history found.")
		return
	}
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headers := []string{"SEEN", "PLAYER", "ID"}
	widths := []int{20, 20, 36}
	fmt.Fprintf(w, "%-*s %-*s %-*s\n", widths[0], headers[0], widths[1], headers[1], widths[2], headers[2])
	for _, e := range events {
		seen := e.SeenAt.Format(dateFormat)
		playerID := e.PlayerID
		if playerID == "" {
			playerID = "-"
		}
		fmt.Fprintf(w, "%-*s %-*s %-*s\n",
			widths[0], dimStyle.Render(seen),
			widths[1], e.PlayerName,
			widths[2], dimStyle.Render(playerID))
	}
}

func printPlayerEventsForPlayer(w io.Writer, events []model.PlayerEvent, dateFormat string) {
	if len(events) == 0 {
		fmt.Println("no player history found.")
		return
	}
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headers := []string{"SEEN", "SERVER", "ID"}
	widths := []int{20, 22, 36}
	fmt.Fprintf(w, "%-*s %-*s %-*s\n", widths[0], headers[0], widths[1], headers[1], widths[2], headers[2])
	for _, e := range events {
		seen := e.SeenAt.Format(dateFormat)
		addr := fmt.Sprintf("%s:%d", e.ServerIP, e.ServerPort)
		playerID := e.PlayerID
		if playerID == "" {
			playerID = "-"
		}
		fmt.Fprintf(w, "%-*s %-*s %-*s\n",
			widths[0], dimStyle.Render(seen),
			widths[1], addr,
			widths[2], dimStyle.Render(playerID))
	}
}

func printServerScanTimeline(w io.Writer, scans []model.ServerScan, playerName string, onlineTimes map[string]bool, playersByScan map[string][]string, dateFormat string, playerMax int) {
	var headers []string
	var rows [][]string
	for _, scan := range scans {
		seenStr := scan.ScannedAt.Format(dateFormat)
		key := scan.ScannedAt.Format(time.RFC3339)
		switch {
		case playerName != "":
			// per-player mode: show ONLINE / ABSENT / NO_RESPONSE
			statusStr := "ABSENT"
			if !scan.HadResponse {
				statusStr = "NO_RESPONSE"
			} else if onlineTimes[key] {
				statusStr = "ONLINE"
			}
			rows = append(rows, []string{seenStr, statusStr, playerName})
		case !scan.HadResponse:
			rows = append(rows, []string{seenStr, "NO", "-"})
		default:
			players := playersByScan[key]
			row := []string{seenStr, "YES", formatPlayerList(players, playerMax)}
			rows = append(rows, row)
		}
	}

	if playerName != "" {
		headers = []string{"SEEN", "STATUS", "PLAYER"}
	} else {
		headers = []string{"SEEN", "RESPONSE", "PLAYERS"}
	}

	widths := []int{20, 14, 40}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Width(widths[col])
			if row == table.HeaderRow {
				return s.Foreground(lipgloss.Color("252")).Bold(true)
			}
			if col == 0 {
				return s.Foreground(lipgloss.Color("245"))
			}
			if col == 1 {
				// status/response column — color based on the row content.
				if row >= 0 && row < len(rows) {
					switch rows[row][1] {
					case "YES", "ONLINE":
						return s.Foreground(lipgloss.Color("82")).Bold(true)
					case "NO", "ABSENT", "NO_RESPONSE":
						return s.Foreground(lipgloss.Color("241"))
					}
				}
			}
			if col == 2 && playerName != "" {
				return s.Foreground(lipgloss.Color("245"))
			}
			return s
		})

	fmt.Fprintln(w)
	fmt.Fprintln(w, t.Render())
	fmt.Fprintf(w, "  %s\n\n", lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(
		fmt.Sprintf("%d scan(s) shown", len(scans))))
}

// formatPlayerList joins player names with ", ". When playerMax > 0,
// only the first playerMax names are shown followed by "+N more".
func formatPlayerList(players []string, playerMax int) string {
	if len(players) == 0 {
		return "-"
	}
	if playerMax <= 0 || len(players) <= playerMax {
		return strings.Join(players, ", ")
	}
	shown := strings.Join(players[:playerMax], ", ")
	return fmt.Sprintf("%s (+%d more)", shown, len(players)-playerMax)
}

func listServerUsage() string {
	return `doorsniff list server <ip> — show scan and player history for a server

Usage:
  doorsniff list server <ip> [flags]

Flags:
  --player TEXT    filter by player name (single name; shows ONLINE/ABSENT/NO_SCAN per scan)
  --since DUR      show events after duration (e.g. 7d, 24h, unix timestamp, ISO date)
  --until DUR      show events before duration (e.g. 7d, 24h, unix timestamp, ISO date)
  --sort ORDER     recent (default) | oldest
  --limit N        max rows (0 = unlimited)
  --database PATH  override database path

Output columns:
  without --player: SEEN | RESPONSE (YES/NO)
  with    --player: SEEN | STATUS (ONLINE/ABSENT/NO_RESPONSE) | PLAYER

Examples:
  doorsniff list server 1.2.3.4:25565
  doorsniff list server 1.2.3.4:25565 --player Notch
  doorsniff list server 1.2.3.4:25565 --since 7d
`
}

func listPlayerUsage() string {
	return `doorsniff list player <name> — show server history for a player

Usage:
  doorsniff list player <name> [flags]

Flags:
  --server IP:PORT  filter by server (comma-separated)
  --since DUR      show events after duration (e.g. 7d, 24h, unix timestamp, ISO date)
  --until DUR      show events before duration (e.g. 7d, 24h, unix timestamp, ISO date)
  --sort ORDER     recent (default) | oldest | server
  --limit N        max rows (0 = unlimited)
  --database PATH  override database path

Examples:
  doorsniff list player Notch
  doorsniff list player Notch --since 30d
  doorsniff list player Notch --sort oldest
`
}
