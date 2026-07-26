//go:build integration

package scanner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/renanbray/swoomps/internal/model"
	"github.com/renanbray/swoomps/internal/protocol/slp"
	"github.com/renanbray/swoomps/internal/protocol/slp/slptest"
	"github.com/renanbray/swoomps/internal/scanner"
	"github.com/renanbray/swoomps/internal/storage"
)

// newTestDB opens an in-memory SQLite (shared cache so multiple
// connections see the same data) and returns a Repository + cleanup.
func newTestDB(t *testing.T) (*storage.Repository, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.EnsureSchema(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ensure schema: %v", err)
	}
	return storage.NewRepository(db), func() { _ = db.Close() }
}

// writeSeedFile writes lines to a temp file and returns its path.
func writeSeedFile(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "seeds.txt")
	var data []byte
	for _, l := range lines {
		data = append(data, []byte(l+"\n")...)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	return path
}

func TestE2E_SeedFileToDB(t *testing.T) {
	srv := slptest.Start(t, slptest.Config{
		Version: slp.Response{
			Version:     slp.Version{Name: "1.21.1", Protocol: 767},
			Players:     slp.Players{Max: 50, Online: 12},
			Description: "E2E Test Server",
		},
	})

	seedPath := writeSeedFile(t, srv.Addr())
	repo, closeDB := newTestDB(t)
	defer closeDB()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	seeds, err := scanner.LoadSeedFile(seedPath)
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}

	stats, err := scanner.RunScan(ctx, scanner.Options{
		Workers: 4,
		QPS:     50,
		Timeout: 2 * time.Second,
		PortsLo: srv.Port(),
		PortsHi: srv.Port(),
		Repo:    repo,
		Seeds:   seeds,
	})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}

	if stats.Found.Load() != 1 {
		t.Errorf("found = %d, want 1", stats.Found.Load())
	}

	servers, err := repo.ListServers(ctx, storage.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers in DB = %d, want 1", len(servers))
	}

	s := servers[0]
	if s.VersionName != "1.21.1" {
		t.Errorf("version = %q, want %q", s.VersionName, "1.21.1")
	}
	if s.VersionProtocol != 767 {
		t.Errorf("protocol = %d, want 767", s.VersionProtocol)
	}
	if s.MOTD != "E2E Test Server" {
		t.Errorf("motd = %q, want %q", s.MOTD, "E2E Test Server")
	}
	if s.NumPlayers != 12 {
		t.Errorf("num_players = %d, want 12", s.NumPlayers)
	}
	if s.MaxPlayers != 50 {
		t.Errorf("max_players = %d, want 50", s.MaxPlayers)
	}
	if s.ProbeKind != model.ProbeSLP {
		t.Errorf("probe_kind = %q, want %q", s.ProbeKind, model.ProbeSLP)
	}
	if s.IP != "127.0.0.1" {
		t.Errorf("ip = %q, want %q", s.IP, "127.0.0.1")
	}
	if s.Port != srv.Port() {
		t.Errorf("port = %d, want %d", s.Port, srv.Port())
	}
}

func TestE2E_LegacyFallback(t *testing.T) {
	srv := slptest.Start(t, slptest.Config{
		Legacy: true,
		LegacyKick: slptest.LegacyKick{
			Protocol:   "47",
			Version:    "1.8.9",
			MOTD:       "Legacy E2E",
			NumPlayers: 3,
			MaxPlayers: 20,
		},
	})

	seedPath := writeSeedFile(t, srv.Addr())
	repo, closeDB := newTestDB(t)
	defer closeDB()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	seeds, err := scanner.LoadSeedFile(seedPath)
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}

	stats, err := scanner.RunScan(ctx, scanner.Options{
		Workers: 4,
		QPS:     50,
		Timeout: 2 * time.Second,
		PortsLo: srv.Port(),
		PortsHi: srv.Port(),
		Repo:    repo,
		Seeds:   seeds,
	})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}

	if stats.Found.Load() != 1 {
		t.Errorf("found = %d, want 1", stats.Found.Load())
	}

	servers, err := repo.ListServers(ctx, storage.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers in DB = %d, want 1", len(servers))
	}

	s := servers[0]
	if s.ProbeKind != model.ProbeLegacy {
		t.Errorf("probe_kind = %q, want %q", s.ProbeKind, model.ProbeLegacy)
	}
	if s.VersionName != "1.8.9" {
		t.Errorf("version = %q, want %q", s.VersionName, "1.8.9")
	}
	if s.MOTD != "Legacy E2E" {
		t.Errorf("motd = %q, want %q", s.MOTD, "Legacy E2E")
	}
	if s.NumPlayers != 3 {
		t.Errorf("num_players = %d, want 3", s.NumPlayers)
	}
	if s.MaxPlayers != 20 {
		t.Errorf("max_players = %d, want 20", s.MaxPlayers)
	}
}

func TestE2E_PortClosed(t *testing.T) {
	// Use a port that nothing is listening on.
	seedPath := writeSeedFile(t, "127.0.0.1:19999")
	repo, closeDB := newTestDB(t)
	defer closeDB()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	seeds, err := scanner.LoadSeedFile(seedPath)
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}

	stats, err := scanner.RunScan(ctx, scanner.Options{
		Workers: 4,
		QPS:     50,
		Timeout: 1 * time.Second,
		PortsLo: 19999,
		PortsHi: 19999,
		Repo:    repo,
		Seeds:   seeds,
	})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}

	if stats.Found.Load() != 0 {
		t.Errorf("found = %d, want 0", stats.Found.Load())
	}

	servers, err := repo.ListServers(ctx, storage.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("servers in DB = %d, want 0", len(servers))
	}
}

func TestE2E_CIDRExpansion(t *testing.T) {
	// Start 4 fake servers on different loopback IPs, all on the
	// same port. On Linux the entire 127.0.0.0/8 is routable.
	const port uint16 = 25565
	type srvEntry struct {
		ip string
	}
	var servers []srvEntry
	for i := 0; i < 4; i++ {
		ip := fmt.Sprintf("127.0.0.%d", i+1)
		slptest.Start(t, slptest.Config{
			Addr: fmt.Sprintf("%s:%d", ip, port),
			Version: slp.Response{
				Version:     slp.Version{Name: "1.20.1", Protocol: 764},
				Players:     slp.Players{Max: 20, Online: i},
				Description: fmt.Sprintf("server-%d", i),
			},
		})
		servers = append(servers, srvEntry{ip: ip})
	}

	// Build seed lines: one host:port per server.
	var lines []string
	for _, s := range servers {
		lines = append(lines, fmt.Sprintf("%s:%d", s.ip, port))
	}
	seedPath := writeSeedFile(t, lines...)

	repo, closeDB := newTestDB(t)
	defer closeDB()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	seeds, err := scanner.LoadSeedFile(seedPath)
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}

	stats, err := scanner.RunScan(ctx, scanner.Options{
		Workers: 8,
		QPS:     100,
		Timeout: 2 * time.Second,
		PortsLo: port,
		PortsHi: port,
		Repo:    repo,
		Seeds:   seeds,
	})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}

	if stats.Found.Load() != 4 {
		t.Errorf("found = %d, want 4", stats.Found.Load())
	}

	dbServers, err := repo.ListServers(ctx, storage.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(dbServers) != 4 {
		t.Fatalf("servers in DB = %d, want 4", len(dbServers))
	}

	// Verify all 4 IPs are present.
	seen := make(map[string]bool)
	for _, s := range dbServers {
		seen[s.IP] = true
	}
	for _, s := range servers {
		if !seen[s.ip] {
			t.Errorf("server %s not found in DB", s.ip)
		}
	}
}

func TestE2E_ResumeSkipsSeed(t *testing.T) {
	srv := slptest.Start(t, slptest.Config{
		Version: slp.Response{
			Version:     slp.Version{Name: "1.20.4", Protocol: 765},
			Players:     slp.Players{Max: 10, Online: 1},
			Description: "Resume Test",
		},
	})

	seedPath := writeSeedFile(t, srv.Addr())
	repo, closeDB := newTestDB(t)
	defer closeDB()

	ctx := context.Background()

	seeds, err := scanner.LoadSeedFile(seedPath)
	if err != nil {
		t.Fatalf("load seeds: %v", err)
	}

	// First run: scan normally.
	stats1, err := scanner.RunScan(ctx, scanner.Options{
		Workers: 4,
		QPS:     50,
		Timeout: 2 * time.Second,
		PortsLo: srv.Port(),
		PortsHi: srv.Port(),
		Repo:    repo,
		Seeds:   seeds,
	})
	if err != nil {
		t.Fatalf("RunScan (1st): %v", err)
	}
	if stats1.Found.Load() != 1 {
		t.Fatalf("1st run: found = %d, want 1", stats1.Found.Load())
	}

	// Second run with --resume: should skip the seed.
	stats2, err := scanner.RunScan(ctx, scanner.Options{
		Workers: 4,
		QPS:     50,
		Timeout: 2 * time.Second,
		PortsLo: srv.Port(),
		PortsHi: srv.Port(),
		Repo:    repo,
		Seeds:   seeds,
		Resume:  true,
	})
	if err != nil {
		t.Fatalf("RunScan (2nd): %v", err)
	}

	// The second run should have probed 0 targets (seed skipped).
	if stats2.Probed.Load() != 0 {
		t.Errorf("2nd run probed = %d, want 0 (seed should be skipped)", stats2.Probed.Load())
	}

	// DB should still have exactly 1 server (from 1st run).
	servers, err := repo.ListServers(ctx, storage.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("servers in DB = %d, want 1", len(servers))
	}
}
