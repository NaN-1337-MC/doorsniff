package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
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

func TestListServersFiltered_LastActivity(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	oldTime := now.Add(-72 * time.Hour)
	freshTime := now.Add(-2 * time.Hour)

	// Server A: has only stale player events.
	if err := repo.UpsertServer(ctx, model.Server{
		IP: "1.1.1.1", Port: 25565, FirstSeen: oldTime, LastSeen: oldTime,
	}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Server B: has only fresh player events.
	if err := repo.UpsertServer(ctx, model.Server{
		IP: "2.2.2.2", Port: 25565, FirstSeen: oldTime, LastSeen: now,
	}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	// Server C: no player events at all.
	if err := repo.UpsertServer(ctx, model.Server{
		IP: "3.3.3.3", Port: 25565, FirstSeen: oldTime, LastSeen: now,
	}); err != nil {
		t.Fatalf("upsert 3: %v", err)
	}

	for _, p := range []struct {
		ip   string
		seen time.Time
		name string
	}{
		{"1.1.1.1", oldTime, "OldPlayer"},
		{"2.2.2.2", freshTime, "FreshPlayer"},
	} {
		if err := repo.RecordPlayerEvent(ctx, p.ip, 25565, model.SamplePlayer{Name: p.name}, p.seen); err != nil {
			t.Fatalf("record event: %v", err)
		}
	}

	got, err := repo.ListServersFiltered(ctx, ListQuery{OrderBy: "ip ASC"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	// LastActivity must be set for A and B, zero for C.
	for _, s := range got {
		switch s.IP {
		case "1.1.1.1":
			if !s.LastActivity.Equal(oldTime) {
				t.Errorf("1.1.1.1 LastActivity = %v, want %v", s.LastActivity, oldTime)
			}
		case "2.2.2.2":
			if !s.LastActivity.Equal(freshTime) {
				t.Errorf("2.2.2.2 LastActivity = %v, want %v", s.LastActivity, freshTime)
			}
		case "3.3.3.3":
			if !s.LastActivity.IsZero() {
				t.Errorf("3.3.3.3 LastActivity = %v, want zero", s.LastActivity)
			}
		}
	}

	// ActiveSince filter: only server B (recent player activity).
	threshold := now.Add(-24 * time.Hour)
	got2, err := repo.ListServersFiltered(ctx, ListQuery{
		OrderBy:     "ip ASC",
		ActiveSince: &threshold,
	})
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(got2) != 1 || got2[0].IP != "2.2.2.2" {
		t.Errorf("ActiveSince filter returned %d rows, want only 2.2.2.2", len(got2))
	}
}

func TestRecordServerScan(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		err := repo.RecordServerScan(ctx, "1.2.3.4", 25565, true, now.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatalf("RecordServerScan: %v", err)
		}
	}

	// Record a non-responsive scan
	err := repo.RecordServerScan(ctx, "1.2.3.4", 25565, false, now.Add(4*time.Hour))
	if err != nil {
		t.Fatalf("RecordServerScan (non-responsive): %v", err)
	}

	scans, err := repo.ListServerScans(ctx, "1.2.3.4", 25565, ListServerScansOptions{})
	if err != nil {
		t.Fatalf("ListServerScans: %v", err)
	}
	if len(scans) != 4 {
		t.Fatalf("len(scans) = %d, want 4", len(scans))
	}
	// Should be DESC by default (most recent first)
	if !scans[0].ScannedAt.After(scans[1].ScannedAt) {
		t.Errorf("scans not in DESC order")
	}
	// Most recent (first in DESC order) should be non-responsive (hour 4)
	if scans[0].HadResponse {
		t.Errorf("scans[0].HadResponse = true, want false (non-responsive was most recent)")
	}
	// Oldest (last) is hour 0 — inserted with hadResponse=true
	if !scans[3].HadResponse {
		t.Errorf("scans[3].HadResponse = false, want true (hour-0 was inserted as responsive)")
	}
}

func TestListServerScans_FilterRange(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		_ = repo.RecordServerScan(ctx, "5.5.5.5", 25565, true, now.Add(time.Duration(i)*time.Hour))
	}

	since := now.Add(1 * time.Hour)
	until := now.Add(3 * time.Hour)
	scans, err := repo.ListServerScans(ctx, "5.5.5.5", 25565, ListServerScansOptions{Since: &since, Until: &until})
	if err != nil {
		t.Fatalf("ListServerScans: %v", err)
	}
	if len(scans) != 3 {
		t.Fatalf("len(scans) = %d, want 3 (hours 1,2,3)", len(scans))
	}
}

func TestListServerScans_LimitOffset(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 10; i++ {
		_ = repo.RecordServerScan(ctx, "6.6.6.6", 25565, true, now.Add(time.Duration(i)*time.Hour))
	}

	scans, err := repo.ListServerScans(ctx, "6.6.6.6", 25565, ListServerScansOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListServerScans limit: %v", err)
	}
	if len(scans) != 3 {
		t.Fatalf("len(scans) = %d, want 3", len(scans))
	}

	// oldest order
	scans, err = repo.ListServerScans(ctx, "6.6.6.6", 25565, ListServerScansOptions{Limit: 3, Order: "oldest"})
	if err != nil {
		t.Fatalf("ListServerScans oldest: %v", err)
	}
	if !scans[0].ScannedAt.Before(scans[1].ScannedAt) {
		t.Errorf("scans not in ASC order")
	}
}

func TestKnownPlayersForServer(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	_ = repo.RecordPlayerEvent(ctx, "7.7.7.7", 25565, model.SamplePlayer{Name: "Notch"}, now)
	_ = repo.RecordPlayerEvent(ctx, "7.7.7.7", 25565, model.SamplePlayer{Name: "Herobrine"}, now.Add(time.Hour))
	_ = repo.RecordPlayerEvent(ctx, "7.7.7.7", 25565, model.SamplePlayer{Name: "Notch"}, now.Add(2*time.Hour)) // duplicate

	players, err := repo.KnownPlayersForServer(ctx, "7.7.7.7", 25565)
	if err != nil {
		t.Fatalf("KnownPlayersForServer: %v", err)
	}
	if len(players) != 2 {
		t.Fatalf("len(players) = %d, want 2", len(players))
	}
	if players[0] != "Notch" {
		t.Errorf("players[0] = %q, want Notch (most recent first)", players[0])
	}
}

// TestListServersFiltered_VersionFilter guards the regression where
// --version was filtered in Go AFTER the SQL LIMIT was applied. With
// LIMIT=N and many non-matching rows, the previous Go-side filter
// would only see rows that happened to be in the top N.
//
// Post-fix: the version filter is pushed into SQL via LIKE so LIMIT
// applies to filtered rows. The test inserts M matching servers plus
// many non-matching ones, and asserts that limit=N returns N matching
// rows regardless of insertion order.
func TestListServersFiltered_VersionFilter(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 25 servers that DO NOT match the version filter — these would
	// push matching servers out of the SQL top-N if the filter were
	// applied after LIMIT.
	for i := 0; i < 25; i++ {
		s := model.Server{
			IP:          fmt.Sprintf("9.9.%d.1", i),
			Port:        25565,
			FirstSeen:   now,
			LastSeen:    now,
			VersionName: "Paper 1.20.4",
		}
		if err := repo.UpsertServer(ctx, s); err != nil {
			t.Fatalf("upsert noise %d: %v", i, err)
		}
	}

	// 5 servers that DO match "26.1.2".
	for i := 0; i < 5; i++ {
		s := model.Server{
			IP:          fmt.Sprintf("8.8.%d.1", i),
			Port:        25565,
			FirstSeen:   now,
			LastSeen:    now,
			VersionName: "Paper 26.1.2",
		}
		if err := repo.UpsertServer(ctx, s); err != nil {
			t.Fatalf("upsert match %d: %v", i, err)
		}
	}

	// With LIMIT=10, the pre-fix behaviour would return at most 10
	// rows after the SQL LIMIT, then Go would filter those — likely
	// 0 because the matching rows sit beyond the top 10. Post-fix
	// (version pushed to SQL), LIMIT applies to filtered rows so we
	// get 5 matching rows regardless.
	got, err := repo.ListServersFiltered(ctx, ListQuery{
		Versions: []string{"26.1.2"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListServersFiltered: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("len = %d, want 5 (version filter must be applied in SQL before LIMIT)", len(got))
	}
	for _, s := range got {
		if !strings.Contains(s.VersionName, "26.1.2") {
			t.Errorf("row %s has VersionName %q, want it to contain 26.1.2", s.Address(), s.VersionName)
		}
	}
}

// TestListServersFiltered_VersionRangePattern guards the range expansion
// (e.g. "1.20.x-1.21.x" → two LIKE patterns) that lives in the CLI.
// The CLI pre-expands and passes the patterns as []string; the SQL
// builder must OR them correctly.
func TestListServersFiltered_VersionRangePattern(t *testing.T) {
	t.Parallel()
	repo, cleanup := newTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	servers := []model.Server{
		{IP: "1.1.1.1", Port: 25565, FirstSeen: now, LastSeen: now, VersionName: "Paper 1.20.4"}, // matches lo
		{IP: "1.1.1.2", Port: 25565, FirstSeen: now, LastSeen: now, VersionName: "Paper 1.21.1"}, // matches hi (upper bound is non-inclusive at 1.22.x)
		{IP: "1.1.1.3", Port: 25565, FirstSeen: now, LastSeen: now, VersionName: "Paper 1.22.0"}, // out of range
		{IP: "1.1.1.4", Port: 25565, FirstSeen: now, LastSeen: now, VersionName: "Paper 1.19.4"}, // out of range
	}
	for _, s := range servers {
		if err := repo.UpsertServer(ctx, s); err != nil {
			t.Fatalf("upsert %s: %v", s.IP, err)
		}
	}

	// Range "1.20.x-1.21.x" expands to patterns ["1.20.", "1.21."].
	// Under "%...%" LIKE, both 1.20.x and 1.21.x match. 1.22.0 and
	// 1.19.4 do NOT.
	got, err := repo.ListServersFiltered(ctx, ListQuery{
		Versions: []string{"1.20.", "1.21."},
	})
	if err != nil {
		t.Fatalf("ListServersFiltered: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (1.20.4 and 1.21.1 should match)", len(got))
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
