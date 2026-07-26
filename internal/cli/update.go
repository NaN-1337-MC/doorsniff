package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/renanbray/swoomps/internal/config"
	"github.com/renanbray/swoomps/internal/model"
	"github.com/renanbray/swoomps/internal/protocol/query"
	"github.com/renanbray/swoomps/internal/protocol/slp"
	"github.com/renanbray/swoomps/internal/storage"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		versionFlag    string
		engineFlag     string
		playersFlag    string
		maxPlayersFlag string
		portFlag      string
		motdFlag      string
		qpsFlag       int
		workersFlag   int
		timeoutFlag   time.Duration
		limitFlag     int
		databaseFlag  string
	)

	fs.StringVar(&versionFlag, "version", "", "filter by version (e.g. 1.21.1)")
	fs.StringVar(&engineFlag, "engine", "", "filter by server engine (e.g. paper, purpur)")
	fs.StringVar(&playersFlag, "players", "", "filter by player count (e.g. >1, 1-10, <50)")
	fs.StringVar(&maxPlayersFlag, "maxplayers", "", "filter by max players (e.g. >=100, 50-200)")
	fs.StringVar(&portFlag, "port", "", "filter by port (e.g. 25565 or 25500-25600)")
	fs.StringVar(&motdFlag, "motd", "", "search MOTD for keywords")
	fs.IntVar(&qpsFlag, "qps", 200, "global QPS limit")
	fs.IntVar(&workersFlag, "workers", 64, "worker pool size")
	fs.DurationVar(&timeoutFlag, "timeout", 3*time.Second, "per-probe timeout")
	fs.IntVar(&limitFlag, "limit", 0, "max servers to update (0 = all)")
	fs.StringVar(&databaseFlag, "database", "", "override database path")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, updateUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
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

	fmt.Printf("updating %d server(s)...\n", len(servers))

	limiter := rate.NewLimiter(rate.Limit(qpsFlag), qpsFlag)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workersFlag)

	for i := range servers {
		srv := servers[i]
		g.Go(func() error {
			select {
			case <-gctx.Done():
				return gctx.Err()
			default:
			}

			if err := limiter.Wait(gctx); err != nil {
				return err
			}

			s, err := probeServer(gctx, srv.IP, srv.Port, timeoutFlag)
			if err != nil {
				return nil
			}

			s.FirstSeen = srv.FirstSeen
			if err := repo.UpsertServer(gctx, s); err != nil {
				return nil
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		if ctx.Err() != nil {
			fmt.Println("\nupdate interrupted.")
		}
	}

	fmt.Printf("\nupdate complete. checked %d server(s).\n", len(servers))
	return nil
}

func probeServer(ctx context.Context, host string, port uint16, timeout time.Duration) (model.Server, error) {
	open, err := probe(ctx, host, port, timeout)
	if err != nil || !open {
		return model.Server{}, err
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
	b.WriteString("swoomps update — re-probe known servers to refresh their data\n\n")
	b.WriteString("Usage:\n  swoomps update [flags]\n\n")
	b.WriteString("Flags:\n")
	b.WriteString("  --version VER    filter by version (e.g. 1.21.1,26.1.2)\n")
	b.WriteString("  --engine ENG     filter by engine, case-insensitive (e.g. paper, purpur)\n")
	b.WriteString("  --players EXPR   filter by player count (e.g. >1, 1-10, <50)\n")
	b.WriteString("  --maxplayers EXPR filter by max players (e.g. >=100, 50-200)\n")
	b.WriteString("  --port PORT      filter by port or range (e.g. 25565 or 25500-25600)\n")
	b.WriteString("  --motd TEXT      search MOTD for keywords\n")
	b.WriteString("  --qps N          QPS limit (default 200)\n")
	b.WriteString("  --workers N       worker pool size (default 64)\n")
	b.WriteString("  --timeout DUR    per-probe timeout (default 3s)\n")
	b.WriteString("  --limit N        max servers to update (0 = all)\n")
	b.WriteString("  --database PATH  override database path\n")
	b.WriteString("\nServers are updated oldest-first (by last_seen).\n")
	b.WriteString("\nExamples:\n")
	b.WriteString("  swoomps update\n")
	b.WriteString("  swoomps update --version=1.21.1 --engine=paper\n")
	b.WriteString("  swoomps update --players='>0' --maxplayers='>=50'\n")
	return b.String()
}
