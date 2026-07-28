package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/renanbray/doorsniff/internal/model"
)

// newTestDB opens an in-memory SQLite, applies the schema, and returns
// a Repository plus a cleanup func. t.Helper is implied via t.
func newTestDB(t *testing.T) (*Repository, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.EnsureSchema(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ensure schema: %v", err)
	}
	return NewRepository(db), func() { _ = db.Close() }
}

func TestUpsertAndList(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	s1 := model.Server{
		IP:              "192.168.1.10",
		Port:            25565,
		FirstSeen:       now,
		LastSeen:        now,
		LastLatencyMs:   42,
		VersionProtocol: 765,
		VersionName:     "1.20.4",
		MOTD:            "A Minecraft Server",
		NumPlayers:      12,
		MaxPlayers:      100,
		SamplePlayers: []model.SamplePlayer{
			{Name: "Notch", ID: "uuid-1"},
		},
		QueryEnabled: true,
		Plugins: []model.Plugin{
			{Name: "EssentialsX", Version: "2.20.1"},
		},
		Country:   "BR",
		ProbeKind: model.ProbeSLPAndQ,
	}

	ctx := context.Background()
	if err := repo.UpsertServer(ctx, s1); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.ListServers(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].IP != s1.IP || got[0].Port != s1.Port {
		t.Errorf("ip/port = %s:%d, want %s:%d", got[0].IP, got[0].Port, s1.IP, s1.Port)
	}
	if got[0].VersionName != s1.VersionName {
		t.Errorf("version = %q, want %q", got[0].VersionName, s1.VersionName)
	}
	if got[0].NumPlayers != s1.NumPlayers {
		t.Errorf("num_players = %d, want %d", got[0].NumPlayers, s1.NumPlayers)
	}
	if len(got[0].SamplePlayers) != 1 || got[0].SamplePlayers[0].Name != "Notch" {
		t.Errorf("sample_players mismatch: %+v", got[0].SamplePlayers)
	}
	if len(got[0].Plugins) != 1 || got[0].Plugins[0].Name != "EssentialsX" {
		t.Errorf("plugins mismatch: %+v", got[0].Plugins)
	}
}

func TestUpsertUpdatesOnConflict(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Second)
	s := model.Server{
		IP:        "10.0.0.1",
		Port:      25565,
		FirstSeen: t0,
		LastSeen:  t0,
	}
	if err := repo.UpsertServer(ctx, s); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	t1 := t0.Add(time.Minute)
	s.LastSeen = t1
	s.NumPlayers = 99
	s.VersionName = "1.21"
	if err := repo.UpsertServer(ctx, s); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := repo.ListServers(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (upsert should update, not insert)", len(got))
	}
	if !got[0].LastSeen.Equal(t1) {
		t.Errorf("last_seen = %v, want %v", got[0].LastSeen, t1)
	}
	if !got[0].FirstSeen.Equal(t0) {
		t.Errorf("first_seen = %v, want %v (must be preserved)", got[0].FirstSeen, t0)
	}
	if got[0].NumPlayers != 99 {
		t.Errorf("num_players = %d, want 99", got[0].NumPlayers)
	}
	if got[0].VersionName != "1.21" {
		t.Errorf("version_name = %q, want 1.21", got[0].VersionName)
	}
}

func TestListFilters(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	servers := []model.Server{
		{IP: "1.1.1.1", Port: 25565, FirstSeen: now, LastSeen: now, VersionProtocol: 765, VersionName: "1.20.4", Country: "BR"},
		{IP: "2.2.2.2", Port: 25575, FirstSeen: now, LastSeen: now, VersionProtocol: 765, VersionName: "1.20.4", Country: "US"},
		{IP: "3.3.3.3", Port: 25565, FirstSeen: now, LastSeen: now, VersionProtocol: 47, VersionName: "1.8.9", Country: "DE"},
	}

	tests := []struct {
		name string
		opts ListOptions
		want int
	}{
		{name: "no filter", opts: ListOptions{}, want: 3},
		{name: "by version", opts: ListOptions{Version: 47}, want: 1},
		{name: "by country", opts: ListOptions{Country: "US"}, want: 1},
		{name: "by port", opts: ListOptions{Port: 25575}, want: 1},
		{name: "limit 2", opts: ListOptions{Limit: 2}, want: 2},
		{name: "limit 0 returns all (3)", opts: ListOptions{Limit: 0}, want: 3},
		{name: "negative limit returns all", opts: ListOptions{Limit: -10}, want: 3},
		{name: "limit 100 returns all (3)", opts: ListOptions{Limit: 100}, want: 3},
		{name: "version + country (none)", opts: ListOptions{Version: 47, Country: "BR"}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, cleanup := newTestDB(t)
			defer cleanup()
			for _, s := range servers {
				if err := repo.UpsertServer(ctx, s); err != nil {
					t.Fatalf("upsert: %v", err)
				}
			}
			got, err := repo.ListServers(ctx, tt.opts)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("len = %d, want %d", len(got), tt.want)
			}
		})
	}
}

// TestListServersFiltered_LimitZeroIsUnlimited guards the
// regression fixed for --limit=0. Pre-fix, ListServersFiltered
// force-overrode Limit <= 0 to 50, which made `update --limit=0`
// effectively a no-op (treated as "use default"). Post-fix, Limit=0
// (or any negative value) must return every match.
func TestListServersFiltered_LimitZeroIsUnlimited(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	repo, cleanup := newTestDB(t)
	defer cleanup()

	const N = 75 // > 50 so we can prove the legacy "default 50" is gone
	for i := 0; i < N; i++ {
		s := model.Server{
			IP:        fmt.Sprintf("10.0.0.%d", i+1),
			Port:      25565,
			FirstSeen: now,
			LastSeen:  now,
		}
		if err := repo.UpsertServer(ctx, s); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	cases := []struct {
		name    string
		limit   int
		wantLen int
	}{
		{"limit 0 -> all rows", 0, N},
		{"limit -1 -> all rows", -1, N},
		{"limit 10 -> 10 rows", 10, 10},
		{"limit 100 -> all rows (clamped to count)", 100, N},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.ListServersFiltered(ctx, ListQuery{Limit: tc.limit, OrderBy: "last_seen ASC"})
			if err != nil {
				t.Fatalf("ListServersFiltered: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestGetStats(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for i, s := range []model.Server{
		{IP: "1.1.1.1", Port: 25565, FirstSeen: now, LastSeen: now, VersionProtocol: 765, VersionName: "1.20.4", Country: "BR"},
		{IP: "2.2.2.2", Port: 25565, FirstSeen: now, LastSeen: now, VersionProtocol: 765, VersionName: "1.20.4", Country: "BR"},
		{IP: "3.3.3.3", Port: 25575, FirstSeen: now, LastSeen: now, VersionProtocol: 47, VersionName: "1.8.9", Country: "US"},
	} {
		_ = i
		if err := repo.UpsertServer(ctx, s); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	stats, err := repo.GetStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("Total = %d, want 3", stats.Total)
	}
	if len(stats.ByVersion) == 0 {
		t.Fatalf("ByVersion is empty")
	}
	if stats.ByVersion[0].Count != 2 {
		t.Errorf("ByVersion[0].Count = %d, want 2 (1.20.4 has 2 servers)", stats.ByVersion[0].Count)
	}
	if len(stats.ByCountry) != 2 {
		t.Errorf("ByCountry len = %d, want 2", len(stats.ByCountry))
	}
	if len(stats.ByPort) != 2 {
		t.Errorf("ByPort len = %d, want 2", len(stats.ByPort))
	}
}

func TestEnsureSchemaIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.EnsureSchema(ctx); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if err := db.EnsureSchema(ctx); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}
